// Package obligation is domain I-03 of the frozen Insurance design —
// the Notice & Obligation Engine. It closes the half of I-03 that did
// not exist: the deadline half was already real and strong in
// pkg/insurance/deadline (a Rule carrying source_clause,
// source_document, effective_version, trigger_event, duration,
// calendar_rule and timezone, refusing a zero duration and refusing a
// policy-sourced rule with no clause), and this package does NOT
// reimplement any of it — every deadline here is a deadline.Rule and
// every computed deadline tick comes from deadline.ComputeDeadline.
//
// What this package adds:
//
//  1. **The Notice object.** The functional spec §11 names eleven
//     temporal and procedural facts a notice analysis must model —
//     IncidentTime, DiscoveryTime, KnowledgeTime, NoticeDueTime,
//     NoticeSentTime, NoticeReceivedTime, NoticeRecipient, NoticeMethod,
//     NoticeContent, NoticeAcknowledgement, NoticeEvidence — and none of
//     them existed anywhere. Assessment computes notice_delay,
//     notice_deadline, notice_compliance and notice_uncertainty from
//     them.
//
//  2. **The obligation graph** the Final Design §12 draws:
//     CLAUSE → OBLIGATION → TRIGGER → REQUIRED EVIDENCE → DEADLINE →
//     RESPONSIBLE PARTY → STATUS. This is what lets the system answer
//     "why is it asking me to do this?" with a clause reference rather
//     than a shrug.
//
// # LATE NOTICE ≠ COVERAGE DENIED
//
// The Final Design states this in a box of its own, and it is the one
// rule this package exists to make structurally impossible to break:
//
//	Jika notice terlambat:  NOTICE_STATUS: LATE
//	But:                    LATE NOTICE ≠ COVERAGE DENIED
//	Itu harus menjadi policy/legal review issue.
//
// Before this package the separation held only BY ABSENCE — the coverage
// engine marks late notice DISPUTED and there is simply no denial field
// in the system to set. That is true but fragile: nothing asserted it,
// so a future field could have broken it silently.
//
// Here it is asserted three ways:
//
//   - Compliance is a vocabulary about the NOTICE, not about the claim.
//     Its values are TIMELY / LATE / NOT_YET_DUE / NOT_GIVEN /
//     UNDETERMINED. There is no DENIED, no FORFEITED, no
//     COVERAGE_LOST — and TestComplianceVocabularyCannotExpressDenial
//     enforces that by scanning the vocabulary itself.
//   - Every Assessment carries a CoverageEffect field whose type has
//     exactly ONE value: NOT_DETERMINED_REQUIRES_POLICY_AND_LEGAL_REVIEW.
//     Like dispute.LegalQuestionStatus, a one-valued type is how you
//     make a code path unreachable rather than merely unused.
//   - A LATE assessment produces a ReviewRequirement naming the policy
//     wording and the applicable law as the things that govern the
//     consequence. TestLateNoticeProducesReviewNotDenial proves a late
//     notice yields exactly that and nothing stronger.
package obligation

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/insurance/deadline"
	"veriqo/pkg/insurance/party"
)

// ---- Notice compliance: a vocabulary about the notice ----------------

// Compliance is what happened with the NOTICE. Every value describes
// the notice's own timing and nothing else. There is deliberately no
// value expressing a consequence for the claim: the consequence of late
// notice is governed by the policy wording and the applicable law, and
// is a matter for policy/legal review.
type Compliance string

const (
	// ComplianceTimely: notice was given at or before the deadline.
	ComplianceTimely Compliance = "TIMELY"
	// ComplianceLate: notice was given, but after the deadline. This says
	// nothing whatever about coverage.
	ComplianceLate Compliance = "LATE"
	// ComplianceNotYetDue: the deadline has not passed and no notice has
	// been given yet. Not a failure — just not due.
	ComplianceNotYetDue Compliance = "NOT_YET_DUE"
	// ComplianceNotGiven: the deadline has passed and no notice was
	// given at all. Distinct from LATE, which requires a notice to exist.
	ComplianceNotGiven Compliance = "NOT_GIVEN"
	// ComplianceUndetermined: the inputs do not permit an assessment —
	// e.g. no deadline rule was supplied, or the trigger time is unknown.
	// The honest output, never a guess.
	ComplianceUndetermined Compliance = "UNDETERMINED"
)

var knownCompliance = map[Compliance]bool{
	ComplianceTimely: true, ComplianceLate: true, ComplianceNotYetDue: true,
	ComplianceNotGiven: true, ComplianceUndetermined: true,
}

// IsKnownCompliance reports whether c is a modelled compliance value.
func IsKnownCompliance(c Compliance) bool { return knownCompliance[c] }

// ComplianceVocabulary returns every modelled compliance value, in a
// stable order. Exposed so the guardrail test can scan it and so a UI
// can render the legend without re-declaring the list.
func ComplianceVocabulary() []Compliance {
	return []Compliance{
		ComplianceTimely, ComplianceLate, ComplianceNotYetDue,
		ComplianceNotGiven, ComplianceUndetermined,
	}
}

// ---- CoverageEffect: the one-valued type -----------------------------

// CoverageEffect is what a notice assessment says about coverage. It has
// exactly one value, and that is the entire design.
type CoverageEffect string

// EffectNotDetermined is the ONLY CoverageEffect. A notice assessment —
// timely, late, or absent — never determines coverage. What late notice
// does to a claim depends on the policy wording, the governing law, and
// often on whether the insurer was prejudiced; none of those is a
// computation, and all of them are a human's to perform.
const EffectNotDetermined CoverageEffect = "NOT_DETERMINED_REQUIRES_POLICY_AND_LEGAL_REVIEW"

// ---- Notice ----------------------------------------------------------

// Notice is the functional spec §11 notice model, with each of the
// eleven named facts as its own field. Times are VERIQO ticks, matching
// the rest of pkg/insurance; 0 means "not known", never "at the epoch",
// and every consumer below treats 0 as unknown explicitly.
type Notice struct {
	NoticeID string `json:"notice_id"`
	CaseID   string `json:"case_id"`
	ClaimID  string `json:"claim_id,omitempty"`

	// The four times that can start a notice clock. Which one actually
	// does is a matter for the policy wording — see TriggerBasis on the
	// obligation, which names the one the clause specifies. This package
	// records all four and lets the clause choose; it never picks one
	// itself.
	IncidentTime  uint64 `json:"incident_time,omitempty"`
	DiscoveryTime uint64 `json:"discovery_time,omitempty"`
	KnowledgeTime uint64 `json:"knowledge_time,omitempty"`

	// NoticeDueTime is DERIVED by Assess from a deadline.Rule; a
	// caller-set value here is overwritten. It is on the struct so a
	// stored Notice round-trips its computed due time.
	NoticeDueTime uint64 `json:"notice_due_time,omitempty"`

	NoticeSentTime     uint64 `json:"notice_sent_time,omitempty"`
	NoticeReceivedTime uint64 `json:"notice_received_time,omitempty"`

	NoticeRecipient party.PartyID `json:"notice_recipient,omitempty"`
	NoticeGivenBy   party.PartyID `json:"notice_given_by,omitempty"`
	// NoticeMethod is how it was given, recorded as stated (e.g. "email",
	// "recorded delivery", "broker portal").
	NoticeMethod string `json:"notice_method,omitempty"`
	// NoticeContent is what the notice said, recorded, never summarised
	// by this package.
	NoticeContent string `json:"notice_content,omitempty"`

	// NoticeAcknowledgement records an acknowledgement if one exists.
	// Absence is meaningful and is reported as uncertainty, never
	// silently treated as "not received".
	NoticeAcknowledgement string `json:"notice_acknowledgement,omitempty"`
	AcknowledgedTime      uint64 `json:"acknowledged_time,omitempty"`

	// NoticeEvidence names the EvidenceIDs backing the above.
	NoticeEvidence []string `json:"notice_evidence,omitempty"`
}

var (
	ErrEmptyNoticeID = errors.New("obligation: NoticeID must be non-empty")
	ErrEmptyCaseID   = errors.New("obligation: CaseID must be non-empty")
)

// NewNotice constructs a Notice with only its identity set. Every
// temporal fact is filled in by the caller from real evidence.
func NewNotice(noticeID, caseID string) (Notice, error) {
	if noticeID == "" {
		return Notice{}, ErrEmptyNoticeID
	}
	if caseID == "" {
		return Notice{}, ErrEmptyCaseID
	}
	return Notice{NoticeID: noticeID, CaseID: caseID}, nil
}

// Given reports whether a notice was actually given.
func (n Notice) Given() bool { return n.NoticeSentTime != 0 || n.NoticeReceivedTime != 0 }

// EffectiveGivenTime is the tick a notice counts as given at. It
// prefers the RECEIVED time when known — most wordings speak of notice
// being received — and falls back to the sent time. Which one a
// particular clause means is recorded on the obligation
// (ComplianceBasis), and Assess honours that; this method is the
// default used when a clause does not say.
func (n Notice) EffectiveGivenTime() uint64 {
	if n.NoticeReceivedTime != 0 {
		return n.NoticeReceivedTime
	}
	return n.NoticeSentTime
}

// ---- The obligation graph (Final Design §12) -------------------------

// TriggerBasis names WHICH recorded time a clause's clock runs from.
// The clause says; this package records what it said and never chooses.
type TriggerBasis string

const (
	TriggerFromIncident  TriggerBasis = "FROM_INCIDENT"
	TriggerFromDiscovery TriggerBasis = "FROM_DISCOVERY"
	TriggerFromKnowledge TriggerBasis = "FROM_KNOWLEDGE"
)

var knownTriggerBases = map[TriggerBasis]bool{
	TriggerFromIncident: true, TriggerFromDiscovery: true, TriggerFromKnowledge: true,
}

// IsKnownTriggerBasis reports whether b is a modelled trigger basis.
func IsKnownTriggerBasis(b TriggerBasis) bool { return knownTriggerBases[b] }

// ComplianceBasis names whether a clause measures compliance by when
// notice was SENT or when it was RECEIVED. Recorded from the clause.
type ComplianceBasis string

const (
	ComplianceBySent     ComplianceBasis = "BY_SENT_TIME"
	ComplianceByReceived ComplianceBasis = "BY_RECEIVED_TIME"
	// ComplianceBasisUnspecified: the clause does not say. Assess then
	// uses Notice.EffectiveGivenTime and records the ambiguity as
	// uncertainty rather than silently picking one.
	ComplianceBasisUnspecified ComplianceBasis = "UNSPECIFIED_IN_CLAUSE"
)

var knownComplianceBases = map[ComplianceBasis]bool{
	ComplianceBySent: true, ComplianceByReceived: true, ComplianceBasisUnspecified: true,
}

// IsKnownComplianceBasis reports whether b is a modelled basis.
func IsKnownComplianceBasis(b ComplianceBasis) bool { return knownComplianceBases[b] }

// Status is an obligation's own workflow state — about the ACTION, not
// about the claim.
type Status string

const (
	StatusOpen        Status = "OPEN"
	StatusInProgress  Status = "IN_PROGRESS"
	StatusCompleted   Status = "COMPLETED"
	StatusOverdue     Status = "OVERDUE"
	StatusNotAppl     Status = "NOT_APPLICABLE"
	StatusUndetermind Status = "UNDETERMINED"
)

var knownStatuses = map[Status]bool{
	StatusOpen: true, StatusInProgress: true, StatusCompleted: true,
	StatusOverdue: true, StatusNotAppl: true, StatusUndetermind: true,
}

// IsKnownStatus reports whether s is a modelled obligation status.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// StatusVocabulary returns every modelled status, in a stable order.
func StatusVocabulary() []Status {
	return []Status{
		StatusOpen, StatusInProgress, StatusCompleted,
		StatusOverdue, StatusNotAppl, StatusUndetermind,
	}
}

// Obligation is one row of the Final Design §12 graph: a clause imposes
// a duty, triggered by an event, requiring evidence, due by a deadline,
// owned by a party, with a status.
//
// The deadline is a REFERENCE to a deadline.Rule, never a duration
// restated here. That is the whole reason the graph is trustworthy: ask
// "why is this due on this date" and the answer is a clause in a
// document at a version, not a constant in this file.
type Obligation struct {
	ObligationID string `json:"obligation_id"`
	CaseID       string `json:"case_id"`

	// Duty is what must be done, in the clause's own terms.
	Duty string `json:"duty"`

	// SourceClause / SourceDocument / SourceVersion are where the duty
	// comes from. All three mandatory — an obligation with no clause
	// behind it is this system inventing homework.
	SourceClause   string `json:"source_clause"`
	SourceDocument string `json:"source_document"`
	SourceVersion  string `json:"source_version"`

	// TriggerEvent names the event type that starts the clock, and
	// TriggerBasis names which recorded time that event corresponds to.
	TriggerEvent string       `json:"trigger_event"`
	TriggerBasis TriggerBasis `json:"trigger_basis"`

	// RequiredEvidence is what must be produced to discharge the duty.
	RequiredEvidence []string `json:"required_evidence,omitempty"`

	// DeadlineRuleID references the pkg/insurance/deadline Rule that
	// carries the period. Empty means no deadline rule has been
	// extracted yet, which Assess reports as UNDETERMINED — never as
	// "no deadline".
	DeadlineRuleID string `json:"deadline_rule_id,omitempty"`

	// ComplianceBasis is whether the clause measures by sent or received
	// time.
	ComplianceBasis ComplianceBasis `json:"compliance_basis"`

	// ResponsibleParty is who must perform the duty.
	ResponsibleParty party.PartyID `json:"responsible_party"`

	Status Status `json:"status"`

	// CompletedAtTick is when the duty was actually discharged; 0 means
	// not yet. DischargingEvidence names what proves it.
	CompletedAtTick     uint64   `json:"completed_at_tick,omitempty"`
	DischargingEvidence []string `json:"discharging_evidence,omitempty"`
}

var (
	ErrEmptyObligationID = errors.New("obligation: ObligationID must be non-empty")
	ErrEmptyDuty         = errors.New("obligation: an Obligation must state what must be done")
	ErrNoClauseSource    = errors.New(
		"obligation: an Obligation requires SourceClause, SourceDocument and SourceVersion — " +
			"an obligation with no clause behind it is this system inventing homework")
	ErrEmptyTrigger        = errors.New("obligation: an Obligation must name the event that triggers it")
	ErrUnknownTriggerBasis = errors.New("obligation: unknown TriggerBasis")
	ErrUnknownComplBasis   = errors.New("obligation: unknown ComplianceBasis")
	ErrNoResponsibleParty  = errors.New("obligation: an Obligation must name the party responsible for it")
	ErrUnknownStatus       = errors.New("obligation: unknown Status")
)

// Validate checks o's own internal consistency.
func (o Obligation) Validate() error {
	if o.ObligationID == "" {
		return ErrEmptyObligationID
	}
	if o.CaseID == "" {
		return ErrEmptyCaseID
	}
	if strings.TrimSpace(o.Duty) == "" {
		return ErrEmptyDuty
	}
	if o.SourceClause == "" || o.SourceDocument == "" || o.SourceVersion == "" {
		return ErrNoClauseSource
	}
	if strings.TrimSpace(o.TriggerEvent) == "" {
		return ErrEmptyTrigger
	}
	if !IsKnownTriggerBasis(o.TriggerBasis) {
		return fmt.Errorf("%w: %q", ErrUnknownTriggerBasis, o.TriggerBasis)
	}
	if !IsKnownComplianceBasis(o.ComplianceBasis) {
		return fmt.Errorf("%w: %q", ErrUnknownComplBasis, o.ComplianceBasis)
	}
	if o.ResponsibleParty == "" {
		return ErrNoResponsibleParty
	}
	if !IsKnownStatus(o.Status) {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, o.Status)
	}
	return nil
}

// Explain answers "why is the system asking for this?" in one traceable
// sentence, built entirely from the obligation's own recorded fields —
// no template invents a reason the clause did not give.
func (o Obligation) Explain() string {
	return fmt.Sprintf(
		"%s (%s, version %s) requires: %s. Triggered by %s (%s). Responsible: %s. Required evidence: %s.",
		o.SourceClause, o.SourceDocument, o.SourceVersion, o.Duty,
		o.TriggerEvent, o.TriggerBasis, o.ResponsibleParty,
		joinOrNone(o.RequiredEvidence))
}

func joinOrNone(ss []string) string {
	if len(ss) == 0 {
		return "none recorded"
	}
	return strings.Join(ss, ", ")
}

// ---- Assessment ------------------------------------------------------

// ReviewRequirement is a piece of work this package hands to a human,
// naming what must be reviewed and under what authority. It is the ONLY
// thing a late notice produces beyond the factual compliance value.
type ReviewRequirement struct {
	// Requirement is what must be reviewed.
	Requirement string `json:"requirement"`
	// GovernedBy names what determines the answer — the policy wording,
	// the applicable law — so the reviewer knows where to look.
	GovernedBy []string `json:"governed_by"`
	// Reviewer names the kind of authority that must perform it.
	Reviewer string `json:"reviewer"`
}

// Assessment is the computed notice analysis: the spec §11 outputs
// (notice_delay, notice_deadline, notice_compliance, notice_uncertainty)
// plus the mandatory one-valued CoverageEffect.
//
// There is no field on this type that can express a coverage or claim
// outcome, and TestAssessmentHasNoDenialField proves it by reflection.
type Assessment struct {
	NoticeID     string `json:"notice_id"`
	ObligationID string `json:"obligation_id,omitempty"`
	CaseID       string `json:"case_id"`

	// TriggerTick is the tick the clock actually ran from, and
	// TriggerBasis names which recorded fact that was.
	TriggerTick  uint64       `json:"trigger_tick,omitempty"`
	TriggerBasis TriggerBasis `json:"trigger_basis,omitempty"`

	// NoticeDeadlineTick is the computed deadline, from
	// deadline.ComputeDeadline. 0 means no deadline could be computed.
	NoticeDeadlineTick uint64 `json:"notice_deadline_tick,omitempty"`
	// DeadlineRuleID and DeadlineSourceClause carry the deadline's own
	// provenance through to the assessment, so a reader never has to ask
	// where the date came from.
	DeadlineRuleID       string `json:"deadline_rule_id,omitempty"`
	DeadlineSourceClause string `json:"deadline_source_clause,omitempty"`

	// GivenTick is the tick notice counts as given at under the clause's
	// ComplianceBasis; 0 when no notice was given.
	GivenTick uint64 `json:"given_tick,omitempty"`

	// DelayTicks is how late the notice was: given - deadline, and only
	// when the notice was genuinely late. Zero for every other case —
	// never a negative-as-unsigned wrap, and never "how early" dressed up
	// as a delay.
	DelayTicks uint64 `json:"delay_ticks"`

	Compliance Compliance `json:"compliance"`

	// CoverageEffect is always EffectNotDetermined. Its type has exactly
	// one value.
	CoverageEffect CoverageEffect `json:"coverage_effect"`

	// Uncertainty names every fact that could not be established, in the
	// assessment's own words. Empty means everything needed was known —
	// which is itself a claim this package only makes when true.
	Uncertainty []string `json:"uncertainty,omitempty"`

	// ReviewRequirements is the work handed to a human.
	ReviewRequirements []ReviewRequirement `json:"review_requirements,omitempty"`

	// SupportingEvidence carries the notice's own evidence IDs through.
	SupportingEvidence []string `json:"supporting_evidence,omitempty"`
}

// LateNoticeReviewRequirement is the exact review a late notice
// produces. It is a package-level value rather than an inline literal so
// the guardrail test can assert on the real thing every caller gets.
func LateNoticeReviewRequirement() ReviewRequirement {
	return ReviewRequirement{
		Requirement: "Determine the consequence, if any, of late notice under this policy. " +
			"Late notice is a factual finding about the notice; whether it affects the claim " +
			"depends on the policy wording and the applicable law, and frequently on whether " +
			"the insurer was prejudiced. This system makes no such determination.",
		GovernedBy: []string{
			"the applicable policy wording",
			"the governing law and jurisdiction recorded for this matter",
		},
		Reviewer: "claims/legal review",
	}
}

// Assess computes the notice analysis for one notice against one
// obligation, using the supplied deadline.Rule.
//
// rule may be the zero Rule, in which case no deadline can be computed
// and the assessment honestly reports UNDETERMINED with the missing
// input named — never a guessed period.
//
// nowTick is the caller's current logical tick, used only to
// distinguish NOT_YET_DUE from NOT_GIVEN when no notice exists.
func Assess(n Notice, o Obligation, rule deadline.Rule, nowTick uint64) (Assessment, error) {
	if n.NoticeID == "" {
		return Assessment{}, ErrEmptyNoticeID
	}
	if n.CaseID == "" {
		return Assessment{}, ErrEmptyCaseID
	}
	if err := o.Validate(); err != nil {
		return Assessment{}, err
	}

	a := Assessment{
		NoticeID:           n.NoticeID,
		ObligationID:       o.ObligationID,
		CaseID:             n.CaseID,
		TriggerBasis:       o.TriggerBasis,
		CoverageEffect:     EffectNotDetermined,
		SupportingEvidence: append([]string(nil), n.NoticeEvidence...),
	}

	// 1. Which recorded time does the clause's clock run from?
	switch o.TriggerBasis {
	case TriggerFromIncident:
		a.TriggerTick = n.IncidentTime
	case TriggerFromDiscovery:
		a.TriggerTick = n.DiscoveryTime
	case TriggerFromKnowledge:
		a.TriggerTick = n.KnowledgeTime
	}
	if a.TriggerTick == 0 {
		a.Uncertainty = append(a.Uncertainty, fmt.Sprintf(
			"the clause runs from %s, but that time is not recorded on this notice", o.TriggerBasis))
	}

	// 2. When was notice given, under the clause's own basis?
	switch o.ComplianceBasis {
	case ComplianceBySent:
		a.GivenTick = n.NoticeSentTime
		if n.NoticeSentTime == 0 && n.NoticeReceivedTime != 0 {
			a.Uncertainty = append(a.Uncertainty,
				"the clause measures by the time notice was sent, but only a received time is recorded")
		}
	case ComplianceByReceived:
		a.GivenTick = n.NoticeReceivedTime
		if n.NoticeReceivedTime == 0 && n.NoticeSentTime != 0 {
			a.Uncertainty = append(a.Uncertainty,
				"the clause measures by the time notice was received, but only a sent time is recorded")
		}
	case ComplianceBasisUnspecified:
		a.GivenTick = n.EffectiveGivenTime()
		if n.Given() {
			a.Uncertainty = append(a.Uncertainty,
				"the clause does not state whether compliance is measured by the sent or the received "+
					"time; the received time was used where available")
		}
	}

	// 3. The deadline, computed by the deadline engine — never here.
	haveRule := rule.RuleID != ""
	if haveRule {
		if err := rule.Validate(); err != nil {
			return Assessment{}, fmt.Errorf("obligation: the supplied deadline rule is invalid: %w", err)
		}
		if a.TriggerTick != 0 {
			d, err := deadline.ComputeDeadline(rule, a.TriggerTick)
			if err != nil {
				return Assessment{}, fmt.Errorf("obligation: computing the notice deadline: %w", err)
			}
			a.NoticeDeadlineTick = d.DeadlineTick
		}
		a.DeadlineRuleID = rule.RuleID
		a.DeadlineSourceClause = rule.SourceClause
	} else {
		a.Uncertainty = append(a.Uncertainty,
			"no deadline rule has been extracted for this obligation, so timeliness cannot be assessed")
	}

	// 4. Compliance — a statement about the notice, and only the notice.
	switch {
	case a.NoticeDeadlineTick == 0:
		a.Compliance = ComplianceUndetermined
	case !n.Given():
		if nowTick > a.NoticeDeadlineTick {
			a.Compliance = ComplianceNotGiven
		} else {
			a.Compliance = ComplianceNotYetDue
		}
	case a.GivenTick == 0:
		// A notice exists but not at a time the clause's basis can read.
		a.Compliance = ComplianceUndetermined
	case a.GivenTick <= a.NoticeDeadlineTick:
		a.Compliance = ComplianceTimely
	default:
		a.Compliance = ComplianceLate
		a.DelayTicks = a.GivenTick - a.NoticeDeadlineTick
	}

	// 5. Acknowledgement: absence is uncertainty, never "not received".
	if n.Given() && n.NoticeAcknowledgement == "" {
		a.Uncertainty = append(a.Uncertainty,
			"no acknowledgement of the notice is recorded; this is not evidence that it was not received")
	}

	// 6. The review a human must perform. A LATE or NOT_GIVEN notice
	// hands over a review requirement — and nothing stronger.
	if a.Compliance == ComplianceLate || a.Compliance == ComplianceNotGiven {
		a.ReviewRequirements = append(a.ReviewRequirements, LateNoticeReviewRequirement())
	}
	if a.Compliance == ComplianceUndetermined {
		a.ReviewRequirements = append(a.ReviewRequirements, ReviewRequirement{
			Requirement: "Establish the missing notice facts named in this assessment's uncertainty list " +
				"before any timeliness conclusion is drawn.",
			GovernedBy: []string{"the applicable policy wording"},
			Reviewer:   "claims handler",
		})
	}

	return a, nil
}

// ObligationStatusFor derives an obligation's status from a computed
// assessment. It is a small, total mapping and it never returns a status
// about the CLAIM — only about the duty.
func ObligationStatusFor(a Assessment) Status {
	switch a.Compliance {
	case ComplianceTimely:
		return StatusCompleted
	case ComplianceLate:
		// The duty WAS discharged, just late. That is COMPLETED with a
		// recorded delay, not OVERDUE — an obligation performed late is
		// not an obligation still outstanding.
		return StatusCompleted
	case ComplianceNotGiven:
		return StatusOverdue
	case ComplianceNotYetDue:
		return StatusOpen
	default:
		return StatusUndetermind
	}
}

// ---- Registry --------------------------------------------------------

var (
	ErrDuplicateObligation = errors.New("obligation: this ObligationID is already registered")
	ErrObligationNotFound  = errors.New("obligation: ObligationID not found")
)

// Registry holds one case's obligations. Safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	caseID string
	items  map[string]Obligation
	order  []string
}

// NewRegistry returns an empty obligation registry for one case.
func NewRegistry(caseID string) (*Registry, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	return &Registry{caseID: caseID, items: map[string]Obligation{}}, nil
}

// CaseID returns the case this registry belongs to.
func (r *Registry) CaseID() string { return r.caseID }

// Register adds a validated obligation.
func (r *Registry) Register(o Obligation) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if o.CaseID != r.caseID {
		return fmt.Errorf("obligation: obligation %s belongs to case %s, not %s", o.ObligationID, o.CaseID, r.caseID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[o.ObligationID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateObligation, o.ObligationID)
	}
	r.items[o.ObligationID] = o
	r.order = append(r.order, o.ObligationID)
	return nil
}

// Get returns one obligation.
func (r *Registry) Get(obligationID string) (Obligation, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.items[obligationID]
	return o, ok
}

// All returns every obligation in registration order.
func (r *Registry) All() []Obligation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Obligation, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.items[id])
	}
	return out
}

// SetStatus records an obligation's status.
func (r *Registry) SetStatus(obligationID string, s Status) error {
	if !IsKnownStatus(s) {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, s)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.items[obligationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrObligationNotFound, obligationID)
	}
	o.Status = s
	r.items[obligationID] = o
	return nil
}

// Discharge records that an obligation was performed, with the evidence
// that proves it. It refuses to record a discharge with no evidence:
// an obligation marked done with nothing behind it is the hand-set
// status this repository's governance forbids everywhere else.
func (r *Registry) Discharge(obligationID string, atTick uint64, evidenceIDs []string) error {
	if len(evidenceIDs) == 0 {
		return errors.New(
			"obligation: discharging an obligation requires the evidence that proves it was performed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.items[obligationID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrObligationNotFound, obligationID)
	}
	o.CompletedAtTick = atTick
	o.DischargingEvidence = append(o.DischargingEvidence, evidenceIDs...)
	o.Status = StatusCompleted
	r.items[obligationID] = o
	return nil
}

// Outstanding returns every obligation not yet completed, in order.
func (r *Registry) Outstanding() []Obligation {
	var out []Obligation
	for _, o := range r.All() {
		if o.Status != StatusCompleted && o.Status != StatusNotAppl {
			out = append(out, o)
		}
	}
	return out
}

// ByResponsibleParty returns every obligation owned by p, sorted by ID
// for deterministic rendering.
func (r *Registry) ByResponsibleParty(p party.PartyID) []Obligation {
	var out []Obligation
	for _, o := range r.All() {
		if o.ResponsibleParty == p {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObligationID < out[j].ObligationID })
	return out
}
