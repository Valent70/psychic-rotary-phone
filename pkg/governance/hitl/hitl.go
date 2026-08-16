// Package hitl closes PHASE 49: a real human-in-the-loop decision
// workflow, not a policy-approval queue.
//
//	DECISION_CREATED → REQUIRES_REVIEW → ASSIGNED → UNDER_REVIEW
//	  → APPROVED / REJECTED / ESCALATED → EXECUTED
//
// The governing constraint from the audit is the one that is easiest to
// get wrong:
//
//	"Human decision tidak boleh mengubah historical machine inference.
//	 Itu harus menjadi MachineDecision + HumanDecision = GovernedOutcome."
//
// So MachineDecision is an immutable value captured at creation and
// hashed. A reviewer never edits it. The reviewer's verdict is a
// separate immutable value, and GovernedOutcome is the composition of
// the two — which means "the machine said FLAG, the human overrode to
// ALLOW, and here is who and why" is permanently answerable, and the
// override rate is measurable. A system that lets a human edit the
// model's output loses both of those forever.
package hitl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

var (
	ErrUnknownCase       = errors.New("hitl: unknown review case")
	ErrDuplicateCase     = errors.New("hitl: case already exists")
	ErrIllegalTransition = errors.New("hitl: illegal workflow transition")
	ErrNotAssigned       = errors.New("hitl: case is not assigned to this reviewer")
	ErrSelfReview        = errors.New("hitl: the requesting actor cannot review their own case")
	ErrMissingRationale  = errors.New("hitl: reviewer action requires a rationale")
	ErrNonMonotonicTick  = errors.New("hitl: tick must not go backwards")
	ErrLedgerTampered    = errors.New("hitl: review ledger hash chain broken")
	ErrNoReviewRequired  = errors.New("hitl: this decision does not require review")
	ErrSLABreach         = errors.New("hitl: review SLA breached")
	ErrPacketIncomplete  = errors.New("hitl: reviewer packet is incomplete")
)

// State is the workflow state machine.
type State string

const (
	StateCreated        State = "DECISION_CREATED"
	StateRequiresReview State = "REQUIRES_REVIEW"
	StateAssigned       State = "ASSIGNED"
	StateUnderReview    State = "UNDER_REVIEW"
	StateApproved       State = "APPROVED"
	StateRejected       State = "REJECTED"
	StateEscalated      State = "ESCALATED"
	StateDeferred       State = "DEFERRED"
	StateMoreEvidence   State = "AWAITING_EVIDENCE"
	StateExecuted       State = "EXECUTED"
	StateAbandoned      State = "ABANDONED"
)

var transitions = map[State][]State{
	StateCreated:        {StateRequiresReview, StateExecuted}, // auto-execute when no review is required
	StateRequiresReview: {StateAssigned, StateAbandoned},
	StateAssigned:       {StateUnderReview, StateRequiresReview, StateAbandoned},
	StateUnderReview: {StateApproved, StateRejected, StateEscalated,
		StateDeferred, StateMoreEvidence, StateAbandoned},
	StateMoreEvidence: {StateUnderReview, StateAbandoned},
	StateDeferred:     {StateRequiresReview, StateAbandoned},
	StateEscalated:    {StateAssigned, StateAbandoned},
	StateApproved:     {StateExecuted},
	StateRejected:     {}, // terminal: a rejected decision is never executed
	StateExecuted:     {},
	StateAbandoned:    {},
}

func canGo(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Action is a reviewer verdict.
type Action string

const (
	ActionApprove             Action = "APPROVE"
	ActionReject              Action = "REJECT"
	ActionRequestMoreEvidence Action = "REQUEST_MORE_EVIDENCE"
	ActionEscalate            Action = "ESCALATE"
	ActionDefer               Action = "DEFER"
)

var actionTarget = map[Action]State{
	ActionApprove:             StateApproved,
	ActionReject:              StateRejected,
	ActionRequestMoreEvidence: StateMoreEvidence,
	ActionEscalate:            StateEscalated,
	ActionDefer:               StateDeferred,
}

// MachineDecision is the immutable inference the machine produced. It
// is captured once and hashed; nothing in this package ever writes to
// it after creation.
type MachineDecision struct {
	DecisionID     string   `json:"decision_id"`
	ExecutionID    string   `json:"execution_id"`
	Subject        string   `json:"subject"`
	Action         string   `json:"action"`
	Confidence     float64  `json:"confidence"`
	RiskScore      float64  `json:"risk_score"`
	RiskLabel      string   `json:"risk_label"`
	ExplanationID  string   `json:"explanation_id"`
	EvidenceRoot   string   `json:"evidence_root"`
	ExecutionRoot  string   `json:"execution_root"`
	PolicyVersion  string   `json:"policy_version"`
	ModelVersions  []string `json:"model_versions"`
	SourceVersions []string `json:"source_versions"`
	Tick           uint64   `json:"tick"`
	Hash           string   `json:"hash"`
}

// ReviewerPacket is everything the reviewer must receive, per the
// audit's enumerated list. It is validated on creation: a review
// request that does not carry the evidence, contradictions, causal
// explanation and alternatives is a rubber stamp, so the engine refuses
// to create it rather than producing a decision nobody actually
// reviewed.
type ReviewerPacket struct {
	Evidence       []string `json:"evidence"`
	Contradictions []string `json:"contradictions"`
	Dependencies   []string `json:"dependencies"`
	CausalSummary  string   `json:"causal_summary"`
	RiskSummary    string   `json:"risk_summary"`
	TrustSummary   string   `json:"trust_summary"`
	PolicySummary  string   `json:"policy_summary"`
	Alternatives   []string `json:"alternatives"`
	Uncertainty    []string `json:"uncertainty"`
}

func (p ReviewerPacket) validate() error {
	var missing []string
	if len(p.Evidence) == 0 {
		missing = append(missing, "evidence")
	}
	if p.CausalSummary == "" {
		missing = append(missing, "causal_summary")
	}
	if p.RiskSummary == "" {
		missing = append(missing, "risk_summary")
	}
	if p.TrustSummary == "" {
		missing = append(missing, "trust_summary")
	}
	if p.PolicySummary == "" {
		missing = append(missing, "policy_summary")
	}
	if len(p.Alternatives) == 0 {
		missing = append(missing, "alternatives")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrPacketIncomplete, strings.Join(missing, ", "))
	}
	return nil
}

func (p ReviewerPacket) hash() string {
	var sb strings.Builder
	sb.WriteString("hitl_packet/v1\n")
	writeList := func(k string, v []string) {
		c := append([]string(nil), v...)
		sort.Strings(c)
		sb.WriteString(k + "=" + strings.Join(c, ";") + "\n")
	}
	writeList("evidence", p.Evidence)
	writeList("contradictions", p.Contradictions)
	writeList("dependencies", p.Dependencies)
	writeList("alternatives", p.Alternatives)
	writeList("uncertainty", p.Uncertainty)
	sb.WriteString("causal=" + p.CausalSummary + "\nrisk=" + p.RiskSummary +
		"\ntrust=" + p.TrustSummary + "\npolicy=" + p.PolicySummary + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// HumanDecision is the reviewer's immutable verdict.
type HumanDecision struct {
	Reviewer     string   `json:"reviewer"`
	Action       Action   `json:"action"`
	OverrideTo   string   `json:"override_to"` // non-empty when the human changed the action
	Rationale    string   `json:"rationale"`
	EvidenceRefs []string `json:"evidence_refs"`
	Tick         uint64   `json:"tick"`
	Hash         string   `json:"hash"`
}

// GovernedOutcome is the composition the audit asked for. The machine
// decision and the human decision are both preserved; Effective is what
// actually happens, and Overridden makes disagreement measurable.
type GovernedOutcome struct {
	Machine    MachineDecision `json:"machine"`
	Human      HumanDecision   `json:"human"`
	Effective  string          `json:"effective_action"`
	Overridden bool            `json:"overridden"`
	ExecutedAt uint64          `json:"executed_at"`
	Hash       string          `json:"hash"`
}

// Case is one decision moving through review.
type Case struct {
	ID           string           `json:"id"`
	Machine      MachineDecision  `json:"machine"`
	Packet       ReviewerPacket   `json:"packet"`
	PacketHash   string           `json:"packet_hash"`
	State        State            `json:"state"`
	RequestedBy  string           `json:"requested_by"`
	Assignee     string           `json:"assignee"`
	Priority     int              `json:"priority"`
	CreatedTick  uint64           `json:"created_tick"`
	DeadlineTick uint64           `json:"deadline_tick"`
	Human        *HumanDecision   `json:"human,omitempty"`
	Outcome      *GovernedOutcome `json:"outcome,omitempty"`
	EscalatedTo  string           `json:"escalated_to"`
}

// Entry is one immutable ledger record.
type Entry struct {
	Index    uint64 `json:"index"`
	CaseID   string `json:"case_id"`
	From     State  `json:"from"`
	To       State  `json:"to"`
	Tick     uint64 `json:"tick"`
	Actor    string `json:"actor"`
	Detail   string `json:"detail"`
	Payload  string `json:"payload"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

// ReviewRule decides whether a machine decision needs a human. It is
// data, not code, so the threshold that sends work to humans is itself
// auditable and versioned.
type ReviewRule struct {
	// MinConfidenceForAuto: below this, a human must review.
	MinConfidenceForAuto float64 `json:"min_confidence_for_auto"`
	// ActionsAlwaysReviewed always route to a human regardless of
	// confidence — e.g. ESCALATE and any blocking action.
	ActionsAlwaysReviewed []string `json:"actions_always_reviewed"`
	// MaxRiskForAuto: above this risk score, a human must review.
	MaxRiskForAuto float64 `json:"max_risk_for_auto"`
	// SLATicks is the review deadline.
	SLATicks uint64 `json:"sla_ticks"`
	Version  uint64 `json:"version"`
}

// DefaultReviewRule is the reference policy.
func DefaultReviewRule() ReviewRule {
	return ReviewRule{MinConfidenceForAuto: 0.85, MaxRiskForAuto: 0.6,
		ActionsAlwaysReviewed: []string{"ESCALATE", "BLOCK", "DENY"}, SLATicks: 100, Version: 1}
}

// RequiresReview evaluates the rule against a machine decision.
func (r ReviewRule) RequiresReview(m MachineDecision) (bool, string) {
	for _, a := range r.ActionsAlwaysReviewed {
		if a == m.Action {
			return true, "action " + a + " always requires review"
		}
	}
	if m.Confidence < r.MinConfidenceForAuto {
		return true, "confidence " + fnum(m.Confidence) + " below auto threshold " +
			fnum(r.MinConfidenceForAuto)
	}
	if m.RiskScore > r.MaxRiskForAuto {
		return true, "risk " + fnum(m.RiskScore) + " above auto threshold " + fnum(r.MaxRiskForAuto)
	}
	return false, "confidence and risk within auto-execution bounds"
}

// Engine is the HITL workflow engine.
type Engine struct {
	mu       sync.RWMutex
	rule     ReviewRule
	cases    map[string]*Case
	order    []string
	ledger   []Entry
	lastTick uint64
}

// New constructs an Engine with the supplied review rule.
func New(rule ReviewRule) *Engine {
	return &Engine{rule: rule, cases: map[string]*Case{}}
}

// Submit registers a machine decision. If the rule says no review is
// needed the case goes straight to EXECUTED with an auto GovernedOutcome
// in which Human.Reviewer is "auto" — so even an unreviewed decision
// has the same shape and the same audit trail as a reviewed one.
func (e *Engine) Submit(m MachineDecision, packet ReviewerPacket, requestedBy string,
	priority int, tick uint64) (Case, error) {

	_, span := telemetry.StartSpan(context.Background(), "hitl.Submit",
		telemetry.Attribute{Key: "subject", Value: m.Subject}, telemetry.Attribute{Key: "action", Value: m.Action})
	defer span.End()

	if m.DecisionID == "" || m.Subject == "" {
		return Case{}, fmt.Errorf("%w: decision id and subject are required", ErrUnknownCase)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Case{}, err
	}
	if _, dup := e.cases[m.DecisionID]; dup {
		return Case{}, fmt.Errorf("%w: %s", ErrDuplicateCase, m.DecisionID)
	}
	m.Tick = tick
	m.ModelVersions = sortedCopy(m.ModelVersions)
	m.SourceVersions = sortedCopy(m.SourceVersions)
	m.Hash = hashMachine(m)

	c := &Case{ID: m.DecisionID, Machine: m, RequestedBy: requestedBy,
		Priority: priority, CreatedTick: tick, State: StateCreated}
	e.cases[c.ID] = c
	e.order = append(e.order, c.ID)
	e.appendLocked(Entry{CaseID: c.ID, To: StateCreated, Tick: tick, Actor: requestedBy,
		Detail: "machine action " + m.Action, Payload: m.Hash})

	need, why := e.rule.RequiresReview(m)
	if !need {
		c.State = StateExecuted
		h := HumanDecision{Reviewer: "auto", Action: ActionApprove,
			Rationale: why, Tick: tick}
		h.Hash = hashHuman(h)
		out := GovernedOutcome{Machine: m, Human: h, Effective: m.Action,
			Overridden: false, ExecutedAt: tick}
		out.Hash = hashOutcome(out)
		c.Human, c.Outcome = &h, &out
		e.appendLocked(Entry{CaseID: c.ID, From: StateCreated, To: StateExecuted, Tick: tick,
			Actor: "engine", Detail: "auto-executed: " + why, Payload: out.Hash})
		return copyCase(c), nil
	}
	if err := packet.validate(); err != nil {
		return Case{}, err
	}
	c.Packet = packet
	c.PacketHash = packet.hash()
	c.State = StateRequiresReview
	c.DeadlineTick = tick + e.rule.SLATicks
	e.appendLocked(Entry{CaseID: c.ID, From: StateCreated, To: StateRequiresReview, Tick: tick,
		Actor: "engine", Detail: why, Payload: c.PacketHash})
	return copyCase(c), nil
}

// Assign routes a case to a reviewer.
func (e *Engine) Assign(caseID, reviewer, assignedBy string, tick uint64) (Case, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Case{}, err
	}
	c, ok := e.cases[caseID]
	if !ok {
		return Case{}, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	if !canGo(c.State, StateAssigned) {
		return Case{}, fmt.Errorf("%w: %s %s -> ASSIGNED", ErrIllegalTransition, caseID, c.State)
	}
	if reviewer == c.RequestedBy {
		return Case{}, fmt.Errorf("%w: %s", ErrSelfReview, reviewer)
	}
	if c.State == StateEscalated && reviewer == c.Assignee {
		return Case{}, fmt.Errorf("%w: escalation must reach a different reviewer", ErrSelfReview)
	}
	from := c.State
	c.Assignee = reviewer
	c.State = StateAssigned
	e.appendLocked(Entry{CaseID: caseID, From: from, To: StateAssigned, Tick: tick,
		Actor: assignedBy, Detail: "assigned to " + reviewer, Payload: c.PacketHash})
	return copyCase(c), nil
}

// Open moves an assigned case to UNDER_REVIEW and returns the packet.
func (e *Engine) Open(caseID, reviewer string, tick uint64) (Case, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Case{}, err
	}
	c, ok := e.cases[caseID]
	if !ok {
		return Case{}, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	if !canGo(c.State, StateUnderReview) {
		return Case{}, fmt.Errorf("%w: %s %s -> UNDER_REVIEW", ErrIllegalTransition, caseID, c.State)
	}
	if c.Assignee != reviewer {
		return Case{}, fmt.Errorf("%w: %s is assigned to %s", ErrNotAssigned, caseID, c.Assignee)
	}
	from := c.State
	c.State = StateUnderReview
	e.appendLocked(Entry{CaseID: caseID, From: from, To: StateUnderReview, Tick: tick,
		Actor: reviewer, Detail: "opened", Payload: c.PacketHash})
	return copyCase(c), nil
}

// Act records a reviewer verdict.
//
// overrideTo is the action the human substitutes for the machine's. It
// is recorded separately from the machine action and never overwrites
// it — the whole point of the MachineDecision + HumanDecision split.
func (e *Engine) Act(caseID, reviewer string, action Action, overrideTo, rationale string,
	evidenceRefs []string, tick uint64) (Case, error) {

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Case{}, err
	}
	c, ok := e.cases[caseID]
	if !ok {
		return Case{}, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	target, known := actionTarget[action]
	if !known {
		return Case{}, fmt.Errorf("%w: unknown action %q", ErrIllegalTransition, action)
	}
	if !canGo(c.State, target) {
		return Case{}, fmt.Errorf("%w: %s %s -> %s", ErrIllegalTransition, caseID, c.State, target)
	}
	if c.Assignee != reviewer {
		return Case{}, fmt.Errorf("%w: %s is assigned to %s", ErrNotAssigned, caseID, c.Assignee)
	}
	if strings.TrimSpace(rationale) == "" {
		return Case{}, ErrMissingRationale
	}
	if tick > c.DeadlineTick && c.DeadlineTick > 0 {
		// An SLA breach is recorded, not silently tolerated, but it does
		// not void the review: refusing a late review would leave the
		// case permanently stuck, which is worse than a logged breach.
		e.appendLocked(Entry{CaseID: caseID, From: c.State, To: c.State, Tick: tick,
			Actor: reviewer, Detail: "SLA breached: deadline was tick " +
				strconv.FormatUint(c.DeadlineTick, 10), Payload: c.PacketHash})
	}
	h := HumanDecision{Reviewer: reviewer, Action: action, OverrideTo: overrideTo,
		Rationale: rationale, EvidenceRefs: sortedCopy(evidenceRefs), Tick: tick}
	h.Hash = hashHuman(h)
	c.Human = &h
	from := c.State
	c.State = target
	if action == ActionEscalate {
		c.EscalatedTo = overrideTo
	}
	e.appendLocked(Entry{CaseID: caseID, From: from, To: target, Tick: tick, Actor: reviewer,
		Detail: string(action) + ": " + rationale, Payload: h.Hash})
	return copyCase(c), nil
}

// SupplyEvidence returns an AWAITING_EVIDENCE case to review with the
// additional references appended to the packet. The packet hash changes,
// which is intentional: the reviewer is now looking at a different
// packet, and the ledger should say so.
func (e *Engine) SupplyEvidence(caseID, actor string, refs []string, tick uint64) (Case, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return Case{}, err
	}
	c, ok := e.cases[caseID]
	if !ok {
		return Case{}, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	if !canGo(c.State, StateUnderReview) {
		return Case{}, fmt.Errorf("%w: %s %s -> UNDER_REVIEW", ErrIllegalTransition, caseID, c.State)
	}
	c.Packet.Evidence = sortedCopy(append(append([]string(nil), c.Packet.Evidence...), refs...))
	c.PacketHash = c.Packet.hash()
	from := c.State
	c.State = StateUnderReview
	e.appendLocked(Entry{CaseID: caseID, From: from, To: StateUnderReview, Tick: tick,
		Actor: actor, Detail: "evidence supplied: " + strings.Join(refs, ","),
		Payload: c.PacketHash})
	return copyCase(c), nil
}

// Execute finalises an approved case into a GovernedOutcome.
func (e *Engine) Execute(caseID, actor string, tick uint64) (GovernedOutcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tickLocked(tick); err != nil {
		return GovernedOutcome{}, err
	}
	c, ok := e.cases[caseID]
	if !ok {
		return GovernedOutcome{}, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	if !canGo(c.State, StateExecuted) {
		return GovernedOutcome{}, fmt.Errorf("%w: %s %s -> EXECUTED", ErrIllegalTransition, caseID, c.State)
	}
	effective := c.Machine.Action
	overridden := false
	if c.Human != nil && c.Human.OverrideTo != "" && c.Human.OverrideTo != c.Machine.Action {
		effective = c.Human.OverrideTo
		overridden = true
	}
	out := GovernedOutcome{Machine: c.Machine, Human: *c.Human, Effective: effective,
		Overridden: overridden, ExecutedAt: tick}
	out.Hash = hashOutcome(out)
	c.Outcome = &out
	from := c.State
	c.State = StateExecuted
	e.appendLocked(Entry{CaseID: caseID, From: from, To: StateExecuted, Tick: tick, Actor: actor,
		Detail:  "effective=" + effective + " overridden=" + strconv.FormatBool(overridden),
		Payload: out.Hash})
	return out, nil
}

// Queue returns the cases awaiting human attention, highest priority
// first then oldest first — a deterministic order, so two operators see
// the same queue.
func (e *Engine) Queue() []Case {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Case
	for _, id := range e.order {
		c := e.cases[id]
		switch c.State {
		case StateRequiresReview, StateAssigned, StateUnderReview, StateMoreEvidence,
			StateDeferred, StateEscalated:
			out = append(out, copyCase(c))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if out[i].CreatedTick != out[j].CreatedTick {
			return out[i].CreatedTick < out[j].CreatedTick
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Breaches returns the queued cases past their SLA deadline at tick.
func (e *Engine) Breaches(tick uint64) []Case {
	var out []Case
	for _, c := range e.Queue() {
		if c.DeadlineTick > 0 && tick > c.DeadlineTick {
			out = append(out, c)
		}
	}
	return out
}

// OverrideRate is the fraction of executed cases where the human
// changed the machine's action. It is the single most useful number
// this package produces: a rate near zero means the review is a rubber
// stamp, and a rate near one means the model is not fit for purpose.
func (e *Engine) OverrideRate() (rate float64, executed int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var overrides int
	for _, id := range e.order {
		c := e.cases[id]
		if c.Outcome == nil {
			continue
		}
		executed++
		if c.Outcome.Overridden {
			overrides++
		}
	}
	if executed == 0 {
		return 0, 0
	}
	return float64(overrides) / float64(executed), executed
}

// Case returns a copy.
func (e *Engine) Case(id string) (Case, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.cases[id]
	if !ok {
		return Case{}, false
	}
	return copyCase(c), true
}

// Ledger returns a copy of the review ledger.
func (e *Engine) Ledger() []Entry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Entry(nil), e.ledger...)
}

// Head is the ledger tip hash.
func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.ledger) == 0 {
		return ""
	}
	return e.ledger[len(e.ledger)-1].Hash
}

// VerifyChain re-derives every hash and link.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, en := range e.ledger {
		if en.Index != uint64(i) {
			return fmt.Errorf("%w: index %d out of order", ErrLedgerTampered, en.Index)
		}
		if en.PrevHash != prev {
			return fmt.Errorf("%w: broken link at %d", ErrLedgerTampered, i)
		}
		if hashEntry(en) != en.Hash {
			return fmt.Errorf("%w: content hash mismatch at %d", ErrLedgerTampered, i)
		}
		prev = en.Hash
	}
	return nil
}

// Explain renders a case's full review history.
func (e *Engine) Explain(caseID string) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.cases[caseID]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	var out []string
	for _, en := range e.ledger {
		if en.CaseID != caseID {
			continue
		}
		line := "tick " + strconv.FormatUint(en.Tick, 10) + ": " + string(en.From) + " -> " +
			string(en.To) + " by " + en.Actor
		if en.Detail != "" {
			line += " (" + en.Detail + ")"
		}
		out = append(out, line)
	}
	return out, nil
}

func (e *Engine) tickLocked(tick uint64) error {
	if tick < e.lastTick {
		return fmt.Errorf("%w: %d < %d", ErrNonMonotonicTick, tick, e.lastTick)
	}
	e.lastTick = tick
	return nil
}

func (e *Engine) appendLocked(en Entry) {
	en.Index = uint64(len(e.ledger))
	if len(e.ledger) > 0 {
		en.PrevHash = e.ledger[len(e.ledger)-1].Hash
	}
	en.Hash = hashEntry(en)
	e.ledger = append(e.ledger, en)
}

func copyCase(c *Case) Case {
	cp := *c
	cp.Packet.Evidence = append([]string(nil), c.Packet.Evidence...)
	cp.Packet.Alternatives = append([]string(nil), c.Packet.Alternatives...)
	if c.Human != nil {
		h := *c.Human
		cp.Human = &h
	}
	if c.Outcome != nil {
		o := *c.Outcome
		cp.Outcome = &o
	}
	return cp
}

func hashMachine(m MachineDecision) string {
	var sb strings.Builder
	sb.WriteString("hitl_machine/v1\n" + m.DecisionID + "\n" + m.ExecutionID + "\n" + m.Subject +
		"\n" + m.Action + "\n" + fnum(m.Confidence) + "\n" + fnum(m.RiskScore) + "\n" + m.RiskLabel +
		"\n" + m.ExplanationID + "\n" + m.EvidenceRoot + "\n" + m.ExecutionRoot +
		"\n" + m.PolicyVersion + "\n" + strings.Join(m.ModelVersions, ";") +
		"\n" + strings.Join(m.SourceVersions, ";") + "\n" + strconv.FormatUint(m.Tick, 10) + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func hashHuman(h HumanDecision) string {
	s := "hitl_human/v1\n" + h.Reviewer + "\n" + string(h.Action) + "\n" + h.OverrideTo +
		"\n" + h.Rationale + "\n" + strings.Join(h.EvidenceRefs, ";") +
		"\n" + strconv.FormatUint(h.Tick, 10) + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashOutcome(o GovernedOutcome) string {
	s := "hitl_outcome/v1\nmachine=" + o.Machine.Hash + "\nhuman=" + o.Human.Hash +
		"\neffective=" + o.Effective + "\noverridden=" + strconv.FormatBool(o.Overridden) +
		"\nexecuted=" + strconv.FormatUint(o.ExecutedAt, 10) + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashEntry(en Entry) string {
	s := "hitl_entry/v1\ni=" + strconv.FormatUint(en.Index, 10) + "\ncase=" + en.CaseID +
		"\nfrom=" + string(en.From) + "\nto=" + string(en.To) +
		"\ntick=" + strconv.FormatUint(en.Tick, 10) + "\nactor=" + en.Actor +
		"\ndetail=" + en.Detail + "\npayload=" + en.Payload + "\nprev=" + en.PrevHash + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func fnum(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }
