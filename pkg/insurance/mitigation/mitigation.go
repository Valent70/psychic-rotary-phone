// Package mitigation implements VICE's Mitigation Engine — blueprint
// §22. It records the mitigation actions a party takes after a loss
// event (the blueprint's own worked example: cargo isolated ->
// repacking -> drying -> salvage sale) and computes their financial
// impact.
//
// The blueprint draws a hard line this package enforces structurally,
// not just in a comment: "VICE menghitung impact tetapi tidak
// menentukan apakah mitigation 'reasonable' secara hukum" — VICE
// computes impact but does not decide whether a mitigation was
// legally "reasonable". Nothing in this package's public API (no
// field, no method, no return value) expresses a reasonableness or
// legal-adequacy judgment; TestPublicAPIHasNoReasonablenessJudgment in
// mitigation_test.go checks this by reflection over every exported
// type's fields and method set, not merely by convention.
//
// Money is represented as integer minor units (e.g. cents) plus a
// currency code — never float64. See Amount's doc comment for why.
//
// This package does not reimplement party identity or evidence
// verification: Actor is a party.PartyID (blueprint §3's existing
// party registry) and SupportingEvidence holds the EvidenceID strings
// produced by evidence.Record.EvidenceID() (blueprint §7-§9's existing
// Unified Evidence Engine wrapper) — matching blueprint §1: "Jangan
// duplikasi Evidence Engine ... yang sudah ada."
package mitigation

import (
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/insurance/party"
)

// ---- Amount: money as integer minor units, never float64 ----

// Amount is a monetary value expressed as integer minor units (e.g.
// cents for USD) plus a currency code.
//
// Every financial figure in this package — and, per the blueprint's
// own determinism discipline (§20's calculation_id/formula/
// calculation_version fields, §35's "Determinism tests", §37's
// "quantum calculation reproducible") — must sum, subtract and compare
// EXACTLY the same way on every run, every platform, every Go version.
// float64 cannot make that promise: repeated addition of amounts like
// 0.1 and 0.2 accumulates binary-rounding drift, so the same set of
// mitigation costs summed in a different order can silently produce a
// different total. Integer minor units make Add/Sub exact integer
// arithmetic with no rounding step at all, so a NetImpact is always
// bit-for-bit reproducible from its inputs. TestSumIsExactIntegerArithmetic
// proves this directly with 50 awkward-cent additions.
type Amount struct {
	MinorUnits int64  `json:"minor_units"`
	Currency   string `json:"currency"`
}

// ErrEmptyCurrency is returned when an Amount is built without a
// currency code — a bare integer is not a monetary value.
var ErrEmptyCurrency = errors.New("mitigation: Amount.Currency must be non-empty")

// ErrCurrencyMismatch is returned whenever two Amounts with different
// currencies are combined. This package never silently converts
// currencies (that requires an exchange_rate and rate_source, which is
// the Quantum Engine's concern per blueprint §20 — out of scope here).
var ErrCurrencyMismatch = errors.New("mitigation: currency mismatch")

// NewAmount constructs a validated Amount.
func NewAmount(minorUnits int64, currency string) (Amount, error) {
	if currency == "" {
		return Amount{}, ErrEmptyCurrency
	}
	return Amount{MinorUnits: minorUnits, Currency: currency}, nil
}

// Validate reports whether a is well-formed (non-empty currency). It
// does not restrict sign — NetImpact is a legitimate negative Amount
// (a mitigation chain that cost more than it recovered).
func (a Amount) Validate() error {
	if a.Currency == "" {
		return ErrEmptyCurrency
	}
	return nil
}

// Add returns a+b as exact integer arithmetic. Both operands must
// share a currency.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.Currency != b.Currency {
		return Amount{}, fmt.Errorf("%w: %q vs %q", ErrCurrencyMismatch, a.Currency, b.Currency)
	}
	return Amount{MinorUnits: a.MinorUnits + b.MinorUnits, Currency: a.Currency}, nil
}

// Sub returns a-b as exact integer arithmetic. Both operands must
// share a currency.
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.Currency != b.Currency {
		return Amount{}, fmt.Errorf("%w: %q vs %q", ErrCurrencyMismatch, a.Currency, b.Currency)
	}
	return Amount{MinorUnits: a.MinorUnits - b.MinorUnits, Currency: a.Currency}, nil
}

// ---- Action: the blueprint's own required record fields (§22) ----

var (
	ErrEmptyCaseID         = errors.New("mitigation: CaseID must be non-empty")
	ErrEmptyActionID       = errors.New("mitigation: ActionID must be non-empty")
	ErrEmptyMitigationName = errors.New("mitigation: MitigationAction must be non-empty")
	ErrEmptyActor          = errors.New("mitigation: Actor must be non-empty")
	ErrNegativeCost        = errors.New("mitigation: Cost.MinorUnits must not be negative")
	ErrNegativeAvoidedLoss = errors.New("mitigation: AvoidedLoss.MinorUnits must not be negative")
	ErrInvalidCost         = errors.New("mitigation: Cost is not a valid Amount")
	ErrInvalidAvoidedLoss  = errors.New("mitigation: AvoidedLoss is not a valid Amount")
)

// Action is one mitigation action taken after a loss event — the
// blueprint's own required fields, verbatim: mitigation_action,
// action_time, actor, cost, avoided_loss, supporting_evidence.
//
// Cost and AvoidedLoss are *Amount, not Amount: a nil pointer means
// "not yet quantified at record time" (blueprint §36's golden rule —
// "a mitigation action's cost/avoided_loss figures must never be
// silently guessed when unknown"). A nil Cost/AvoidedLoss is NEVER
// treated as a known Amount{0, ...} by Timeline.Impact — see that
// method's doc comment and TestImpactExcludesUnquantifiedFromSums.
type Action struct {
	ActionID string `json:"action_id"`
	CaseID   string `json:"case_id"`

	MitigationAction string        `json:"mitigation_action"`
	ActionTime       uint64        `json:"action_time"`
	Actor            party.PartyID `json:"actor"`

	Cost        *Amount `json:"cost,omitempty"`
	AvoidedLoss *Amount `json:"avoided_loss,omitempty"`

	SupportingEvidence []string `json:"supporting_evidence,omitempty"`

	// ReferencesActionID optionally names a prior Action in the same
	// case's mitigation chain — the blueprint's own worked example is
	// a sequence (Cargo isolated -> Repacking -> Drying -> Salvage
	// sale) where each step follows from the one before. Empty means
	// this action does not explicitly reference a prior one.
	ReferencesActionID string `json:"references_action_id,omitempty"`
}

// New constructs a validated Action. cost and avoidedLoss may be nil
// (not yet quantified); when non-nil they must be valid Amounts with
// a non-negative MinorUnits (a mitigation cannot cost or avoid a
// negative amount of money — NetImpact, computed separately, is where
// a negative figure can legitimately appear).
func New(caseID, actionID, mitigationAction string, actionTime uint64, actor party.PartyID, cost, avoidedLoss *Amount, supportingEvidence []string, referencesActionID string) (Action, error) {
	if caseID == "" {
		return Action{}, ErrEmptyCaseID
	}
	if actionID == "" {
		return Action{}, ErrEmptyActionID
	}
	if mitigationAction == "" {
		return Action{}, ErrEmptyMitigationName
	}
	if actor == "" {
		return Action{}, ErrEmptyActor
	}
	if cost != nil {
		if err := cost.Validate(); err != nil {
			return Action{}, fmt.Errorf("%w: %v", ErrInvalidCost, err)
		}
		if cost.MinorUnits < 0 {
			return Action{}, ErrNegativeCost
		}
	}
	if avoidedLoss != nil {
		if err := avoidedLoss.Validate(); err != nil {
			return Action{}, fmt.Errorf("%w: %v", ErrInvalidAvoidedLoss, err)
		}
		if avoidedLoss.MinorUnits < 0 {
			return Action{}, ErrNegativeAvoidedLoss
		}
	}

	var ev []string
	if len(supportingEvidence) > 0 {
		ev = make([]string, len(supportingEvidence))
		copy(ev, supportingEvidence)
	}

	return Action{
		ActionID:           actionID,
		CaseID:             caseID,
		MitigationAction:   mitigationAction,
		ActionTime:         actionTime,
		Actor:              actor,
		Cost:               cost,
		AvoidedLoss:        avoidedLoss,
		SupportingEvidence: ev,
		ReferencesActionID: referencesActionID,
	}, nil
}

// ---- Timeline: the ordered set of mitigation actions for one case ----

var (
	ErrTimelineEmptyCaseID = errors.New("mitigation: Timeline CaseID must be non-empty")
	ErrCaseIDMismatch      = errors.New("mitigation: Action.CaseID does not match this Timeline's CaseID")
	ErrDuplicateActionID   = errors.New("mitigation: ActionID already recorded on this Timeline")
	ErrReferencesUnknown   = errors.New("mitigation: ReferencesActionID does not name an Action already on this Timeline")
)

// Timeline is the ordered set of mitigation Actions recorded for one
// case (or one underlying loss event within a case). Thread-safe:
// mitigation actions can be recorded concurrently with other case
// activity, matching the sync.RWMutex discipline used throughout
// pkg/insurance.
type Timeline struct {
	mu      sync.RWMutex
	caseID  string
	actions map[string]Action
	order   []string
}

// NewTimeline returns an empty mitigation Timeline for caseID.
func NewTimeline(caseID string) (*Timeline, error) {
	if caseID == "" {
		return nil, ErrTimelineEmptyCaseID
	}
	return &Timeline{caseID: caseID, actions: make(map[string]Action)}, nil
}

// Add records a new Action on the timeline. Refuses an Action from a
// different case, a duplicate ActionID, or a ReferencesActionID that
// does not already name an Action on this Timeline (fail-closed: you
// cannot chain off a mitigation step VICE has never seen — the same
// discipline policy.History.Add applies to Supersedes).
func (t *Timeline) Add(a Action) error {
	if a.CaseID != t.caseID {
		return fmt.Errorf("%w: action case %q, timeline case %q", ErrCaseIDMismatch, a.CaseID, t.caseID)
	}
	if a.ActionID == "" {
		return ErrEmptyActionID
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.actions[a.ActionID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateActionID, a.ActionID)
	}
	if a.ReferencesActionID != "" {
		if _, ok := t.actions[a.ReferencesActionID]; !ok {
			return fmt.Errorf("%w: %s", ErrReferencesUnknown, a.ReferencesActionID)
		}
	}
	t.actions[a.ActionID] = a
	t.order = append(t.order, a.ActionID)
	return nil
}

// Get returns the Action for actionID.
func (t *Timeline) Get(actionID string) (Action, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	a, ok := t.actions[actionID]
	return a, ok
}

// All returns every recorded Action in the order they were added.
func (t *Timeline) All() []Action {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Action, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, t.actions[id])
	}
	return out
}

// Count returns the number of Actions recorded.
func (t *Timeline) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.actions)
}

// ---- Impact: the computed aggregate (§22) ----

// QuantificationStatus states, as plain fact, whether every recorded
// Action's Cost and AvoidedLoss were known at the time Impact was
// computed. It is NOT a judgment about whether the mitigation was
// adequate, sufficient, or reasonable — only whether the figures
// feeding the sum are complete.
type QuantificationStatus string

const (
	// QuantificationComplete means every recorded Action had both a
	// known Cost and a known AvoidedLoss.
	QuantificationComplete QuantificationStatus = "COMPLETE"
	// QuantificationPartial means at least one Action's Cost or
	// AvoidedLoss (or both) was nil ("not yet quantified") at the
	// time of computation, but at least one quantified Amount existed
	// so a currency — and therefore a sum — could be established.
	QuantificationPartial QuantificationStatus = "PARTIAL"
)

// ErrNoQuantifiedAmounts is returned by Impact when a Timeline holds
// no Action with a known Cost or AvoidedLoss at all — there is no
// currency to sum in, and no total to report. This is the golden-rule
// case made structural: rather than silently returning a zero-valued
// Impact (which would look identical to "every action legitimately
// cost/avoided nothing"), Impact refuses to guess and returns this
// error instead.
var ErrNoQuantifiedAmounts = errors.New("mitigation: no Action on this Timeline has a quantified Cost or AvoidedLoss yet")

// Impact is the computed financial aggregate over one Timeline's
// Actions — blueprint §22's own words: "VICE menghitung impact tetapi
// tidak menentukan apakah mitigation 'reasonable' secara hukum" (VICE
// computes impact but does not decide whether a mitigation is legally
// "reasonable"). Every field here is a genuine sum, count, or list
// over the recorded Actions — nothing here, in this package, or
// anywhere in mitigation's public API expresses a reasonableness or
// legal-adequacy verdict; see TestPublicAPIHasNoReasonablenessJudgment.
type Impact struct {
	Currency string `json:"currency"`

	TotalCost        Amount `json:"total_cost"`
	TotalAvoidedLoss Amount `json:"total_avoided_loss"`
	// NetImpact is TotalAvoidedLoss - TotalCost. It can be negative —
	// a mitigation chain that cost more than it avoided is a fact
	// this package reports plainly, not a verdict it renders.
	NetImpact Amount `json:"net_impact"`

	TotalActionCount      int `json:"total_action_count"`
	QuantifiedActionCount int `json:"quantified_action_count"`

	// ActionsWithUnquantifiedCost / ActionsWithUnquantifiedAvoidedLoss
	// list the ActionIDs whose Cost / AvoidedLoss was nil and were
	// therefore EXCLUDED from TotalCost / TotalAvoidedLoss — never
	// counted as a known zero. Deterministically ordered: Timeline
	// add-order.
	ActionsWithUnquantifiedCost        []string `json:"actions_with_unquantified_cost,omitempty"`
	ActionsWithUnquantifiedAvoidedLoss []string `json:"actions_with_unquantified_avoided_loss,omitempty"`

	Status QuantificationStatus `json:"status"`
}

// Impact computes the aggregate financial impact of every Action
// recorded on t.
//
// A nil Cost or AvoidedLoss ("not yet quantified" — see Action's doc
// comment) is excluded from the corresponding sum entirely; it is
// never treated as Amount{MinorUnits: 0, ...}. An Action whose Cost or
// AvoidedLoss IS an explicit Amount{MinorUnits: 0, ...} (a mitigation
// step that is known to have cost, or avoided, exactly nothing) is a
// genuine known value and DOES participate in the sum — that is the
// distinction blueprint §36's golden rule requires, and
// TestImpactExcludesUnquantifiedFromSums proves both halves of it.
//
// Every quantified Cost and AvoidedLoss across the whole Timeline must
// share one currency; this package never silently converts between
// currencies (that needs an exchange_rate and rate_source, which is
// the Quantum Engine's job per blueprint §20, not this one's).
func (t *Timeline) Impact() (Impact, error) {
	actions := t.All()

	var (
		currency                           string
		totalCostSet, totalLossSet         bool
		totalCost, totalLoss               Amount
		unquantifiedCost, unquantifiedLoss []string
		quantifiedCount                    int
	)

	for _, a := range actions {
		quantifiedThisAction := false

		if a.Cost != nil {
			if currency == "" {
				currency = a.Cost.Currency
			} else if a.Cost.Currency != currency {
				return Impact{}, fmt.Errorf("%w: action %s has cost currency %q, timeline is %q", ErrCurrencyMismatch, a.ActionID, a.Cost.Currency, currency)
			}
			if !totalCostSet {
				totalCost = Amount{MinorUnits: 0, Currency: currency}
				totalCostSet = true
			}
			var err error
			totalCost, err = totalCost.Add(*a.Cost)
			if err != nil {
				return Impact{}, err
			}
			quantifiedThisAction = true
		} else {
			unquantifiedCost = append(unquantifiedCost, a.ActionID)
		}

		if a.AvoidedLoss != nil {
			if currency == "" {
				currency = a.AvoidedLoss.Currency
			} else if a.AvoidedLoss.Currency != currency {
				return Impact{}, fmt.Errorf("%w: action %s has avoided_loss currency %q, timeline is %q", ErrCurrencyMismatch, a.ActionID, a.AvoidedLoss.Currency, currency)
			}
			if !totalLossSet {
				totalLoss = Amount{MinorUnits: 0, Currency: currency}
				totalLossSet = true
			}
			var err error
			totalLoss, err = totalLoss.Add(*a.AvoidedLoss)
			if err != nil {
				return Impact{}, err
			}
			quantifiedThisAction = true
		} else {
			unquantifiedLoss = append(unquantifiedLoss, a.ActionID)
		}

		if quantifiedThisAction {
			quantifiedCount++
		}
	}

	if currency == "" {
		// No Action anywhere had a known Cost or AvoidedLoss: refuse
		// to report a fabricated zero-valued Impact.
		return Impact{}, ErrNoQuantifiedAmounts
	}
	if !totalCostSet {
		totalCost = Amount{MinorUnits: 0, Currency: currency}
	}
	if !totalLossSet {
		totalLoss = Amount{MinorUnits: 0, Currency: currency}
	}

	net, err := totalLoss.Sub(totalCost)
	if err != nil {
		return Impact{}, err
	}

	status := QuantificationComplete
	if len(unquantifiedCost) > 0 || len(unquantifiedLoss) > 0 {
		status = QuantificationPartial
	}

	return Impact{
		Currency:                           currency,
		TotalCost:                          totalCost,
		TotalAvoidedLoss:                   totalLoss,
		NetImpact:                          net,
		TotalActionCount:                   len(actions),
		QuantifiedActionCount:              quantifiedCount,
		ActionsWithUnquantifiedCost:        unquantifiedCost,
		ActionsWithUnquantifiedAvoidedLoss: unquantifiedLoss,
		Status:                             status,
	}, nil
}
