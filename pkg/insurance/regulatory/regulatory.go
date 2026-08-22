// Package regulatory is the regulatory half of domain I-08. The Final
// Design §36 gives its whole model in one chain:
//
//	Allegation → Investigation → Regulatory finding → Settlement →
//	Fine → Disgorgement → Monitor → Certification → Completion
//
// and then states the two rules that are the entire reason this package
// exists as code rather than a diagram:
//
//	Settlement          ≠  every allegation proven
//	Monitor requirement ≠  monitor completed
//
// Both are enforced structurally here, not by comment:
//
//   - An Allegation's Result can only become PROVEN through a recorded
//     TRIBUNAL or REGULATORY finding that cites its authority and source
//     document. RecordSettlement cannot set it, and Validate refuses a
//     settled matter carrying a proven allegation. This mirrors
//     pkg/insurance/dispute's identical rule for the same reason.
//   - A MonitorRequirement models the requirement and the completion as
//     two SEPARATE, independently-recorded facts. There is no single
//     "monitor: done" boolean, and Completed() is false until a real
//     certification with a named certifier and a source document has
//     been recorded. Imposing a monitor and completing one are different
//     events, often years apart, and a great many regulatory matters
//     have the first without ever reaching the second.
//
// Scope discipline: this package models a REGULATORY MATTER as
// metadata and workflow. It contains no list of regulators, no
// jurisdiction table, no penalty schedule and no rule derived from any
// real regulatory action — the Final Design's forbidden list names
// hard-coding a real company as a classifier target explicitly, and the
// honest generalisation is that this package knows about no real entity
// at all. Everything it holds is what a human recorded, with the source
// cited.
package regulatory

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ---- Stage ----------------------------------------------------------

// Stage is one step of the §36 chain. Like a dispute, a regulatory
// matter can legitimately skip stages — many matters close after
// investigation with no finding at all — so Advance permits forward
// moves and records what was skipped, and refuses backward moves.
type Stage string

const (
	StageAllegation     Stage = "ALLEGATION"
	StageInvestigation  Stage = "INVESTIGATION"
	StageFinding        Stage = "REGULATORY_FINDING"
	StageSettlement     Stage = "SETTLEMENT"
	StageFine           Stage = "FINE"
	StageDisgorgement   Stage = "DISGORGEMENT"
	StageMonitor        Stage = "MONITOR"
	StageCertification  Stage = "CERTIFICATION"
	StageCompletion     Stage = "COMPLETION"
	StageClosedNoAction Stage = "CLOSED_NO_ACTION"
)

// StageOrder returns the chain in the design document's own order.
// CLOSED_NO_ACTION sits at the end because it is terminal, but it is
// reachable from anywhere: a regulator can close a matter at any point,
// and pretending otherwise would force a false progression.
func StageOrder() []Stage {
	return []Stage{
		StageAllegation, StageInvestigation, StageFinding, StageSettlement,
		StageFine, StageDisgorgement, StageMonitor, StageCertification,
		StageCompletion, StageClosedNoAction,
	}
}

var stageIndex = func() map[Stage]int {
	m := make(map[Stage]int, len(StageOrder()))
	for i, s := range StageOrder() {
		m[s] = i
	}
	return m
}()

// IsKnownStage reports whether s is a modelled stage.
func IsKnownStage(s Stage) bool { _, ok := stageIndex[s]; return ok }

// StageTransition is one recorded move through the chain.
type StageTransition struct {
	From    Stage   `json:"from"`
	To      Stage   `json:"to"`
	Skipped []Stage `json:"skipped,omitempty"`
	Tick    uint64  `json:"tick"`
	Reason  string  `json:"reason"`
}

// ---- Allegation -----------------------------------------------------

// AllegationResult is what, if anything, was actually established about
// an allegation. NOT_DETERMINED is the default and the overwhelmingly
// common real-world value.
type AllegationResult string

const (
	// ResultAlleged: asserted, nothing established.
	ResultAlleged AllegationResult = "ALLEGED"
	// ResultUnderInvestigation: being examined, nothing established.
	ResultUnderInvestigation AllegationResult = "UNDER_INVESTIGATION"
	// ResultNotDetermined: the matter ended without this allegation
	// being decided either way. This is what a settlement leaves behind.
	ResultNotDetermined AllegationResult = "NOT_DETERMINED"
	// ResultWithdrawn: withdrawn before any determination.
	ResultWithdrawn AllegationResult = "WITHDRAWN"
	// ResultNotProven: an authority determined it was not made out.
	ResultNotProven AllegationResult = "NOT_PROVEN"
	// ResultProven: an authority determined it WAS made out. Reachable
	// only through RecordFinding, and only with the authority and the
	// source document cited.
	ResultProven AllegationResult = "PROVEN"
)

var knownResults = map[AllegationResult]bool{
	ResultAlleged: true, ResultUnderInvestigation: true, ResultNotDetermined: true,
	ResultWithdrawn: true, ResultNotProven: true, ResultProven: true,
}

// IsKnownResult reports whether r is a modelled allegation result.
func IsKnownResult(r AllegationResult) bool { return knownResults[r] }

// FindingKind is the kind of authority that made a determination.
type FindingKind string

const (
	// FindingRegulatory: a regulator's own formal finding.
	FindingRegulatory FindingKind = "REGULATORY_FINDING"
	// FindingTribunal: a tribunal or court determination.
	FindingTribunal FindingKind = "TRIBUNAL_FINDING"
	// FindingSettlementOnly: the matter resolved by agreement. A
	// settlement-only record can NEVER carry a proven allegation — see
	// the package doc.
	FindingSettlementOnly FindingKind = "SETTLEMENT_ONLY"
)

var knownFindingKinds = map[FindingKind]bool{
	FindingRegulatory: true, FindingTribunal: true, FindingSettlementOnly: true,
}

// IsKnownFindingKind reports whether k is a modelled finding kind.
func IsKnownFindingKind(k FindingKind) bool { return knownFindingKinds[k] }

// Allegation is one thing asserted against a party in a regulatory
// matter, plus what became of it.
type Allegation struct {
	AllegationID string `json:"allegation_id"`
	// Description is what was alleged, in the alleging authority's own
	// words as recorded.
	Description string           `json:"description"`
	Result      AllegationResult `json:"result"`

	// DeterminedByKind, DeterminedBy and SourceDocument are mandatory
	// whenever Result is PROVEN or NOT_PROVEN.
	DeterminedByKind FindingKind `json:"determined_by_kind,omitempty"`
	DeterminedBy     string      `json:"determined_by,omitempty"`
	SourceDocument   string      `json:"source_document,omitempty"`

	// SupportingEvidence / ContradictingEvidence / MissingEvidence keep
	// the same three-way decomposition the rest of this domain uses.
	// Never collapsed into one score.
	SupportingEvidence    []string `json:"supporting_evidence,omitempty"`
	ContradictingEvidence []string `json:"contradicting_evidence,omitempty"`
	MissingEvidence       []string `json:"missing_evidence,omitempty"`

	Note string `json:"note,omitempty"`
}

var (
	ErrEmptyAllegationID = errors.New("regulatory: AllegationID must be non-empty")
	ErrEmptyDescription  = errors.New("regulatory: an Allegation must record what was alleged")
	ErrUnknownResult     = errors.New("regulatory: unknown AllegationResult")
	ErrUnknownFinding    = errors.New("regulatory: unknown FindingKind")
	ErrSettlementProves  = errors.New(
		"regulatory: a settlement determines nothing — an allegation resolved SETTLEMENT_ONLY cannot be " +
			"recorded PROVEN or NOT_PROVEN; only a regulatory or tribunal finding determines an allegation")
	ErrDeterminationNeedsSource = errors.New(
		"regulatory: an allegation recorded PROVEN or NOT_PROVEN must cite the determining authority, " +
			"its kind, and the source document")
)

// Validate checks a's own internal consistency, including the
// settlement rule.
func (a Allegation) Validate() error {
	if a.AllegationID == "" {
		return ErrEmptyAllegationID
	}
	if strings.TrimSpace(a.Description) == "" {
		return ErrEmptyDescription
	}
	if !IsKnownResult(a.Result) {
		return fmt.Errorf("%w: %q", ErrUnknownResult, a.Result)
	}
	determined := a.Result == ResultProven || a.Result == ResultNotProven
	if !determined {
		return nil
	}
	if a.DeterminedByKind == FindingSettlementOnly {
		return fmt.Errorf("%w: allegation %s recorded %s", ErrSettlementProves, a.AllegationID, a.Result)
	}
	if !IsKnownFindingKind(a.DeterminedByKind) {
		return fmt.Errorf("%w: %q", ErrUnknownFinding, a.DeterminedByKind)
	}
	if strings.TrimSpace(a.DeterminedBy) == "" || strings.TrimSpace(a.SourceDocument) == "" {
		return fmt.Errorf("%w: allegation %s", ErrDeterminationNeedsSource, a.AllegationID)
	}
	return nil
}

// NewAllegation records a fresh allegation in ResultAlleged — the only
// honest starting state.
func NewAllegation(allegationID, description string) (Allegation, error) {
	if allegationID == "" {
		return Allegation{}, ErrEmptyAllegationID
	}
	if strings.TrimSpace(description) == "" {
		return Allegation{}, ErrEmptyDescription
	}
	return Allegation{AllegationID: allegationID, Description: description, Result: ResultAlleged}, nil
}

// ---- Monetary consequences ------------------------------------------

// MonetaryKind distinguishes the two money outcomes §36 names. They are
// different things and are deliberately not merged: a fine is a penalty,
// disgorgement is the return of a gain, and reporting one as the other
// misstates what happened.
type MonetaryKind string

const (
	MonetaryFine         MonetaryKind = "FINE"
	MonetaryDisgorgement MonetaryKind = "DISGORGEMENT"
)

// MonetaryOutcome is one recorded money consequence. The amount is
// recorded in minor units plus a currency code, matching
// pkg/insurance/quantum's own representation and rationale (exact
// integer arithmetic, never float64) without importing it — this is a
// recorded external figure, not a computed one, so it carries no
// evidence-backed derivation.
type MonetaryOutcome struct {
	Kind        MonetaryKind `json:"kind"`
	AmountMinor int64        `json:"amount_minor"`
	Currency    string       `json:"currency"`
	// ImposedBy and SourceDocument are mandatory: an amount with no
	// authority behind it is a number someone typed.
	ImposedBy      string `json:"imposed_by"`
	SourceDocument string `json:"source_document"`
	RecordedTick   uint64 `json:"recorded_tick"`
}

var ErrMonetaryNoSource = errors.New(
	"regulatory: a fine or disgorgement must record the authority that imposed it and the source document")

// Validate checks the monetary outcome's own consistency.
func (m MonetaryOutcome) Validate() error {
	if m.Kind != MonetaryFine && m.Kind != MonetaryDisgorgement {
		return fmt.Errorf("regulatory: unknown MonetaryKind %q", m.Kind)
	}
	if m.AmountMinor < 0 {
		return errors.New("regulatory: a fine or disgorgement amount cannot be negative")
	}
	if strings.TrimSpace(m.Currency) == "" {
		return errors.New("regulatory: a monetary outcome must state its currency")
	}
	if strings.TrimSpace(m.ImposedBy) == "" || strings.TrimSpace(m.SourceDocument) == "" {
		return ErrMonetaryNoSource
	}
	return nil
}

// ---- Monitor: requirement and completion, held apart ----------------

// Certification is a recorded attestation that a monitorship obligation
// was discharged. It is the ONLY thing that can make a
// MonitorRequirement report Completed, and it requires a named
// certifier and a source document.
type Certification struct {
	CertifiedBy    string `json:"certified_by"`
	SourceDocument string `json:"source_document"`
	CertifiedTick  uint64 `json:"certified_tick"`
	// Scope is what was actually certified, in the certifier's words —
	// often narrower than the requirement.
	Scope string `json:"scope,omitempty"`
}

var ErrCertificationIncomplete = errors.New(
	"regulatory: a certification must name the certifier and cite the source document")

// Validate checks the certification's own consistency.
func (c Certification) Validate() error {
	if strings.TrimSpace(c.CertifiedBy) == "" || strings.TrimSpace(c.SourceDocument) == "" {
		return ErrCertificationIncomplete
	}
	return nil
}

// MonitorRequirement models a monitorship as TWO separate facts: that
// one was imposed, and (separately, later, maybe never) that it was
// certified complete.
//
// There is deliberately no `Completed bool` field. The Final Design's
// rule "Monitor requirement ≠ monitor completed" is exactly the failure
// a single boolean invites: a field set at imposition time, or defaulted
// true, silently converts an ongoing obligation into a discharged one.
// Completion here is derived from the presence of a real Certification.
type MonitorRequirement struct {
	MonitorID string `json:"monitor_id"`
	// Requirement is what was imposed, in the imposing authority's words.
	Requirement string `json:"requirement"`
	// ImposedBy and SourceDocument record where the obligation comes
	// from.
	ImposedBy      string `json:"imposed_by"`
	SourceDocument string `json:"source_document"`
	ImposedTick    uint64 `json:"imposed_tick"`
	// ExpectedDurationNote is recorded free text (e.g. "three annual
	// reports"). Deliberately NOT a computed deadline: a monitorship
	// term is read off an order, and this package hard-codes no periods.
	ExpectedDurationNote string `json:"expected_duration_note,omitempty"`

	// Certifications are the attestations recorded against this
	// monitorship, oldest first. Empty is the normal state for a live
	// monitorship.
	Certifications []Certification `json:"certifications,omitempty"`
}

var (
	ErrEmptyMonitorID  = errors.New("regulatory: MonitorID must be non-empty")
	ErrMonitorNoSource = errors.New(
		"regulatory: a monitor requirement must record what was imposed, by whom, and the source document")
)

// Validate checks the requirement's own consistency.
func (m MonitorRequirement) Validate() error {
	if m.MonitorID == "" {
		return ErrEmptyMonitorID
	}
	if strings.TrimSpace(m.Requirement) == "" ||
		strings.TrimSpace(m.ImposedBy) == "" ||
		strings.TrimSpace(m.SourceDocument) == "" {
		return ErrMonitorNoSource
	}
	for _, c := range m.Certifications {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Completed reports whether this monitorship has been certified
// complete. It is DERIVED from the presence of at least one valid
// certification — there is no field to set, so an imposed monitorship
// can never read as a discharged one.
func (m MonitorRequirement) Completed() bool {
	for _, c := range m.Certifications {
		if c.Validate() == nil {
			return true
		}
	}
	return false
}

// Status renders the monitorship's honest position in words, for a
// dossier or a Case Room panel.
func (m MonitorRequirement) Status() string {
	if m.Completed() {
		return "MONITOR_CERTIFIED_COMPLETE"
	}
	return "MONITOR_REQUIRED_NOT_CERTIFIED_COMPLETE"
}

// ---- Matter ---------------------------------------------------------

var (
	ErrEmptyMatterID      = errors.New("regulatory: MatterID must be non-empty")
	ErrEmptyCaseID        = errors.New("regulatory: CaseID must be non-empty")
	ErrEmptyAuthority     = errors.New("regulatory: a regulatory matter must name the authority it is before")
	ErrUnknownStage       = errors.New("regulatory: unknown Stage")
	ErrStageBackward      = errors.New("regulatory: a regulatory matter cannot move backward through the chain")
	ErrEmptyReason        = errors.New("regulatory: advancing a regulatory matter must record why")
	ErrDuplicateAlleged   = errors.New("regulatory: this AllegationID is already on this matter")
	ErrAllegationNotFound = errors.New("regulatory: AllegationID not found on this matter")
	ErrDuplicateMonitor   = errors.New("regulatory: this MonitorID is already on this matter")
	ErrMonitorNotFound    = errors.New("regulatory: MonitorID not found on this matter")
)

// Matter is one regulatory matter connected to an insurance case.
type Matter struct {
	mu sync.RWMutex

	MatterID string
	CaseID   string
	// Authority is the body the matter is before, recorded generically.
	// This package ships no list of real regulators.
	Authority string
	// Jurisdiction is recorded as given.
	Jurisdiction string

	stage Stage
	log   []StageTransition

	allegations     map[string]Allegation
	allegationOrder []string

	monetary []MonetaryOutcome

	monitors     map[string]MonitorRequirement
	monitorOrder []string

	settled            bool
	settlementDocument string
}

// NewMatter opens a regulatory matter at StageAllegation.
func NewMatter(matterID, caseID, authority, jurisdiction string, tick uint64) (*Matter, error) {
	if matterID == "" {
		return nil, ErrEmptyMatterID
	}
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	if strings.TrimSpace(authority) == "" {
		return nil, ErrEmptyAuthority
	}
	return &Matter{
		MatterID: matterID, CaseID: caseID, Authority: authority, Jurisdiction: jurisdiction,
		stage: StageAllegation,
		log: []StageTransition{{
			From: StageAllegation, To: StageAllegation, Tick: tick, Reason: "regulatory matter opened",
		}},
		allegations: map[string]Allegation{},
		monitors:    map[string]MonitorRequirement{},
	}, nil
}

// Stage returns the matter's current stage.
func (m *Matter) Stage() Stage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stage
}

// StageLog returns every recorded transition, oldest first.
func (m *Matter) StageLog() []StageTransition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]StageTransition, len(m.log))
	copy(out, m.log)
	return out
}

// Advance moves the matter forward, recording skipped stages. Backward
// moves are refused.
func (m *Matter) Advance(to Stage, reason string, tick uint64) error {
	if !IsKnownStage(to) {
		return fmt.Errorf("%w: %q", ErrUnknownStage, to)
	}
	if strings.TrimSpace(reason) == "" {
		return ErrEmptyReason
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fromIdx, toIdx := stageIndex[m.stage], stageIndex[to]
	if toIdx < fromIdx {
		return fmt.Errorf("%w: %s -> %s", ErrStageBackward, m.stage, to)
	}
	if toIdx == fromIdx {
		return nil
	}
	var skipped []Stage
	order := StageOrder()
	for i := fromIdx + 1; i < toIdx; i++ {
		skipped = append(skipped, order[i])
	}
	m.log = append(m.log, StageTransition{From: m.stage, To: to, Skipped: skipped, Tick: tick, Reason: reason})
	m.stage = to
	return nil
}

// AddAllegation records an allegation on this matter.
func (m *Matter) AddAllegation(a Allegation) error {
	if err := a.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.allegations[a.AllegationID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateAlleged, a.AllegationID)
	}
	m.allegations[a.AllegationID] = a
	m.allegationOrder = append(m.allegationOrder, a.AllegationID)
	return nil
}

// RecordFinding records that an authority determined one allegation.
// This is the ONLY path to ResultProven / ResultNotProven, and it
// requires the finding kind, the determining authority and the source
// document. A settlement-only "finding" is refused outright.
func (m *Matter) RecordFinding(allegationID string, kind FindingKind, result AllegationResult, determinedBy, sourceDocument string) error {
	if !IsKnownFindingKind(kind) {
		return fmt.Errorf("%w: %q", ErrUnknownFinding, kind)
	}
	if kind == FindingSettlementOnly && (result == ResultProven || result == ResultNotProven) {
		return fmt.Errorf("%w: allegation %s", ErrSettlementProves, allegationID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.allegations[allegationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrAllegationNotFound, allegationID)
	}
	updated := a
	updated.Result = result
	updated.DeterminedByKind = kind
	updated.DeterminedBy = determinedBy
	updated.SourceDocument = sourceDocument
	if err := updated.Validate(); err != nil {
		return err
	}
	m.allegations[allegationID] = updated
	return nil
}

// RecordSettlement records that the matter resolved by agreement.
//
// **Settlement ≠ every allegation proven.** Every allegation that has
// NOT already been determined by a real regulatory or tribunal finding
// becomes NOT_DETERMINED — never PROVEN. Allegations a genuine prior
// finding already determined keep that determination, because a
// settlement following a finding does not erase the finding.
func (m *Matter) RecordSettlement(sourceDocument string, tick uint64) error {
	if strings.TrimSpace(sourceDocument) == "" {
		return errors.New("regulatory: a settlement must cite the settlement instrument")
	}
	m.mu.Lock()
	for _, id := range m.allegationOrder {
		a := m.allegations[id]
		alreadyDetermined := (a.Result == ResultProven || a.Result == ResultNotProven) &&
			(a.DeterminedByKind == FindingRegulatory || a.DeterminedByKind == FindingTribunal)
		if alreadyDetermined {
			continue
		}
		a.Result = ResultNotDetermined
		a.DeterminedByKind = FindingSettlementOnly
		a.DeterminedBy = ""
		a.SourceDocument = ""
		if a.Note == "" {
			a.Note = "resolved by settlement; not adjudicated"
		}
		m.allegations[id] = a
	}
	m.settled = true
	m.settlementDocument = sourceDocument
	m.mu.Unlock()
	return m.Advance(StageSettlement, "settlement recorded: "+sourceDocument, tick)
}

// Settled reports whether a settlement has been recorded.
func (m *Matter) Settled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settled
}

// Allegations returns every allegation in the order added.
func (m *Matter) Allegations() []Allegation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Allegation, 0, len(m.allegationOrder))
	for _, id := range m.allegationOrder {
		out = append(out, m.allegations[id])
	}
	return out
}

// ProvenAllegations returns only the allegations an authority actually
// determined were made out. On a settled matter with no prior finding
// this is ALWAYS empty — the honest answer, and the one this package
// exists to make impossible to get wrong.
func (m *Matter) ProvenAllegations() []Allegation {
	var out []Allegation
	for _, a := range m.Allegations() {
		if a.Result == ResultProven {
			out = append(out, a)
		}
	}
	return out
}

// UndeterminedAllegations returns the allegations nobody decided.
func (m *Matter) UndeterminedAllegations() []Allegation {
	var out []Allegation
	for _, a := range m.Allegations() {
		if a.Result == ResultNotDetermined {
			out = append(out, a)
		}
	}
	return out
}

// RecordMonetaryOutcome records a fine or a disgorgement.
func (m *Matter) RecordMonetaryOutcome(o MonetaryOutcome) error {
	if err := o.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monetary = append(m.monetary, o)
	return nil
}

// MonetaryOutcomes returns every recorded fine and disgorgement.
func (m *Matter) MonetaryOutcomes() []MonetaryOutcome {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MonetaryOutcome, len(m.monetary))
	copy(out, m.monetary)
	return out
}

// ImposeMonitor records that a monitorship obligation was imposed. It
// does NOT, and cannot, record that one was completed.
func (m *Matter) ImposeMonitor(mr MonitorRequirement) error {
	if err := mr.Validate(); err != nil {
		return err
	}
	if len(mr.Certifications) > 0 {
		return errors.New(
			"regulatory: a monitor requirement is imposed without certifications — " +
				"record completion separately via CertifyMonitor, so imposing and completing stay distinct events")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.monitors[mr.MonitorID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateMonitor, mr.MonitorID)
	}
	m.monitors[mr.MonitorID] = mr
	m.monitorOrder = append(m.monitorOrder, mr.MonitorID)
	return nil
}

// CertifyMonitor records a certification against an imposed
// monitorship. This is the only way a monitorship can come to report
// Completed.
func (m *Matter) CertifyMonitor(monitorID string, c Certification) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mr, ok := m.monitors[monitorID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrMonitorNotFound, monitorID)
	}
	mr.Certifications = append(mr.Certifications, c)
	m.monitors[monitorID] = mr
	return nil
}

// Monitors returns every monitorship in the order imposed.
func (m *Matter) Monitors() []MonitorRequirement {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]MonitorRequirement, 0, len(m.monitorOrder))
	for _, id := range m.monitorOrder {
		out = append(out, m.monitors[id])
	}
	return out
}

// OutstandingMonitors returns the monitorships that have been imposed
// and NOT certified complete. A non-empty result is the honest answer
// to "is this matter finished" — and it is the answer a single
// "monitor: done" boolean would have hidden.
func (m *Matter) OutstandingMonitors() []MonitorRequirement {
	var out []MonitorRequirement
	for _, mr := range m.Monitors() {
		if !mr.Completed() {
			out = append(out, mr)
		}
	}
	return out
}

// CompletionBlockers returns, in words, every reason this matter cannot
// honestly be described as complete. An empty result means every
// imposed obligation has a real recorded discharge.
func (m *Matter) CompletionBlockers() []string {
	var out []string
	for _, mr := range m.OutstandingMonitors() {
		out = append(out, fmt.Sprintf(
			"monitor %s imposed by %s is %s", mr.MonitorID, mr.ImposedBy, mr.Status()))
	}
	for _, a := range m.Allegations() {
		if a.Result == ResultAlleged || a.Result == ResultUnderInvestigation {
			out = append(out, fmt.Sprintf(
				"allegation %s is %s — no authority has determined it", a.AllegationID, a.Result))
		}
	}
	return out
}
