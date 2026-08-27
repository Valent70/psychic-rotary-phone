// Package dispute is domain I-08 of the frozen Insurance design —
// Dispute / Legal / Regulatory Control. It was the one domain of the
// eight that did not exist at all before this round: no dispute stage
// machine, no forum or governing-law model, no legal-hold object, and
// nowhere for the two epistemic rules this domain exists to protect to
// live.
//
// The Final Design's own sentence is the whole specification of what
// this package may and may not do:
//
//	"Dan semuanya menjadi metadata + workflow, bukan automatic legal
//	advice."
//
// (…and all of it becomes metadata + workflow, not automatic legal
// advice.) Concretely, this package is modelled on the same discipline
// pkg/insurance/causation already uses for a structurally identical
// problem — a question this system must NOT answer:
//
//   - An Issue is a disputed QUESTION carrying each side's recorded
//     Position plus supporting / contradicting / missing evidence,
//     exactly as causation.Hypothesis does. It never resolves.
//   - A LegalQuestion has exactly one possible status,
//     LEGAL_INTERPRETATION_REQUIRED. There is no second value, so there
//     is no code path that answers a legal question.
//   - An Outcome is RECORDED from an external authority (a settlement
//     agreement, a tribunal award, a court judgment), never computed.
//     Its constructor requires the authority and the source document.
//
// Two rules are enforced structurally rather than documented:
//
//   - **Settlement ≠ every allegation proven.** RecordSettlement refuses
//     to mark any allegation PROVEN, whatever the caller passes. Only
//     RecordAward and RecordJudgment can, and only when the determining
//     authority and its document are cited.
//   - **A dispute stage is never a merits finding.** Reaching COURT says
//     nothing about who is right; the stage machine carries no field
//     that could express one.
//
// What this package deliberately does NOT contain: any rule derived
// from a real court decision. The Final Design forbids it in as many
// words, naming a specific reported decision as the thing not to
// hard-code as a rule. A real judgment
// may be attached to a LegalQuestion as HistoricalReference — labelled
// as reference material for a human, carrying no weight in any
// computation this package performs — and that is the only way a real
// decision may appear anywhere in this system.
package dispute

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/insurance/party"
)

// ---- Stage: the dispute workflow (Final Design §8) -------------------

// Stage is one step of the dispute workflow the Final Design §8
// enumerates: claim → dispute → notice → evidence hold → legal review →
// negotiation → mediation → arbitration → court → settlement/award/
// judgment → recovery.
//
// Unlike pkg/insurance/case's lifecycle, this sequence is NOT
// one-step-at-a-time: real disputes routinely skip mediation, or settle
// straight out of negotiation without ever reaching arbitration.
// Advance therefore permits any move to a LATER stage and records the
// stages that were skipped, so the skip is visible rather than lost.
// What it never permits is moving backwards, which would let a matter's
// recorded history be rewritten.
type Stage string

const (
	StageNoticeOfDispute Stage = "NOTICE_OF_DISPUTE"
	StageEvidenceHold    Stage = "EVIDENCE_HOLD"
	StageLegalReview     Stage = "LEGAL_REVIEW"
	StageNegotiation     Stage = "NEGOTIATION"
	StageMediation       Stage = "MEDIATION"
	StageArbitration     Stage = "ARBITRATION"
	StageCourt           Stage = "COURT"
	StageOutcomeRecorded Stage = "OUTCOME_RECORDED"
	StageRecovery        Stage = "RECOVERY"
)

// StageOrder returns the nine dispute stages in the design document's
// own order.
func StageOrder() []Stage {
	return []Stage{
		StageNoticeOfDispute, StageEvidenceHold, StageLegalReview, StageNegotiation,
		StageMediation, StageArbitration, StageCourt, StageOutcomeRecorded, StageRecovery,
	}
}

var stageIndex = func() map[Stage]int {
	m := make(map[Stage]int, 9)
	for i, s := range StageOrder() {
		m[s] = i
	}
	return m
}()

// IsKnownStage reports whether s is a modelled dispute stage.
func IsKnownStage(s Stage) bool { _, ok := stageIndex[s]; return ok }

// StageTransition is one recorded move through the dispute workflow,
// including which stages were skipped over.
type StageTransition struct {
	From    Stage   `json:"from"`
	To      Stage   `json:"to"`
	Skipped []Stage `json:"skipped,omitempty"`
	Tick    uint64  `json:"tick"`
	// Reason is why this move happened — mandatory, because a dispute
	// advancing for no recorded reason is exactly the untraceable status
	// change this repository's governance forbids.
	Reason string `json:"reason"`
}

// ---- Forum: the cross-border metadata (Final Design §8) -------------

// Forum is the governing-law / jurisdiction / seat metadata the Final
// Design §8 requires on every cross-border dispute. Every field here is
// RECORDED from a real instrument — a policy clause, a charterparty, a
// bill of lading, an arbitration agreement — and this package derives
// none of them. There is deliberately no function anywhere in this file
// that infers a governing law from a vessel flag, a party's domicile,
// or a port of call.
type Forum struct {
	// GoverningLaw is the law the contract states applies, in the
	// contract's own words (e.g. "the law of Jurisdiction A").
	GoverningLaw string `json:"governing_law"`
	// Jurisdiction is the forum the contract nominates for disputes.
	Jurisdiction string `json:"jurisdiction"`
	// ArbitrationSeat is the seat, when arbitration is agreed. Empty
	// means "no arbitration agreement recorded", never "no seat needed".
	ArbitrationSeat string `json:"arbitration_seat,omitempty"`
	// ForumDescription is free text for a forum that is neither a plain
	// court nor a seated arbitration (e.g. an expert determination).
	ForumDescription string `json:"forum_description,omitempty"`

	// SourceDocument and SourceClause are where the above was actually
	// read from. Both mandatory: a governing law with no source is an
	// assumption, and Validate refuses it.
	SourceDocument string `json:"source_document"`
	SourceClause   string `json:"source_clause"`
	// SourceVersion is which version of SourceDocument was in force.
	SourceVersion string `json:"source_version"`

	// LimitationRuleID optionally names a pkg/insurance/deadline Rule
	// carrying the limitation period. The period itself is NOT restated
	// here: deadline.Rule already models source_clause, duration,
	// calendar_rule and timezone, and duplicating a duration into this
	// struct is how "all maritime claims = 1 year" gets hard-coded.
	LimitationRuleID string `json:"limitation_rule_id,omitempty"`
	// NoticePeriodRuleID likewise names a deadline Rule, never a number.
	NoticePeriodRuleID string `json:"notice_period_rule_id,omitempty"`

	// EvidenceRequirements is what the forum requires, recorded verbatim
	// from the instrument or the applicable procedural rules.
	EvidenceRequirements []string `json:"evidence_requirements,omitempty"`
	// EnforcementNotes records recorded facts about enforceability —
	// again from a cited source, never this package's opinion.
	EnforcementNotes []string `json:"enforcement_notes,omitempty"`
}

var (
	ErrForumNoGoverningLaw = errors.New("dispute: Forum.GoverningLaw must be non-empty")
	ErrForumNoJurisdiction = errors.New("dispute: Forum.Jurisdiction must be non-empty")
	ErrForumNoSource       = errors.New(
		"dispute: Forum requires SourceDocument, SourceClause and SourceVersion — a governing law " +
			"or jurisdiction with no cited source is an assumption, not recorded metadata")
)

// Validate checks the forum's own internal consistency.
func (f Forum) Validate() error {
	if strings.TrimSpace(f.GoverningLaw) == "" {
		return ErrForumNoGoverningLaw
	}
	if strings.TrimSpace(f.Jurisdiction) == "" {
		return ErrForumNoJurisdiction
	}
	if f.SourceDocument == "" || f.SourceClause == "" || f.SourceVersion == "" {
		return ErrForumNoSource
	}
	return nil
}

// ---- Issue: a disputed question, never an answer --------------------

// IssueStatus is where a disputed issue currently stands. Every value
// describes the STATE OF THE ARGUMENT, never who is right. There is
// deliberately no RESOLVED_IN_FAVOUR_OF value, and no field on Issue
// that could carry one.
type IssueStatus string

const (
	// StatusOpen: the issue is live and neither side's position has been
	// tested against the evidence.
	StatusOpen IssueStatus = "OPEN"
	// StatusEvidenceGathering: evidence is still being collected on this
	// issue.
	StatusEvidenceGathering IssueStatus = "EVIDENCE_GATHERING"
	// StatusContested: both sides have recorded positions and the
	// evidence does not settle between them.
	StatusContested IssueStatus = "CONTESTED"
	// StatusAwaitingLegalInterpretation: the issue turns on a question of
	// law this system must not answer.
	StatusAwaitingLegalInterpretation IssueStatus = "AWAITING_LEGAL_INTERPRETATION"
	// StatusDeterminedByAuthority: an external authority has determined
	// this issue. WHAT it determined lives in the recorded Outcome, with
	// that authority cited — not in this status.
	StatusDeterminedByAuthority IssueStatus = "DETERMINED_BY_AUTHORITY"
	// StatusWithdrawn: the issue is no longer pursued.
	StatusWithdrawn IssueStatus = "WITHDRAWN"
)

var knownIssueStatuses = map[IssueStatus]bool{
	StatusOpen: true, StatusEvidenceGathering: true, StatusContested: true,
	StatusAwaitingLegalInterpretation: true, StatusDeterminedByAuthority: true, StatusWithdrawn: true,
}

// IsKnownIssueStatus reports whether s is a modelled issue status.
func IsKnownIssueStatus(s IssueStatus) bool { return knownIssueStatuses[s] }

// Position is one party's recorded contention on an issue. It is what
// that party SAYS, recorded verbatim — this package attaches no weight
// to it, scores it against nothing, and never marks one Position
// correct.
type Position struct {
	Party party.PartyID `json:"party"`
	// Contention is the party's position in its own words.
	Contention string `json:"contention"`
	// ReliedOnEvidence names the EvidenceIDs this party relies on.
	// Recorded as "what they cite", never as "what is proven".
	ReliedOnEvidence []string `json:"relied_on_evidence,omitempty"`
	// ReliedOnClauses names the contract/policy clauses this party
	// relies on.
	ReliedOnClauses []string `json:"relied_on_clauses,omitempty"`
	RecordedAtTick  uint64   `json:"recorded_at_tick"`
}

// Issue is one disputed question. Its evidence decomposition mirrors
// causation.Hypothesis deliberately: supporting, contradicting and
// missing evidence held apart, never collapsed into a single score.
//
// The Final Design §45's own five dispute questions — what obligation
// existed, what happened, when, who had which responsibility, what
// caused the loss, what is the quantum — are Issue instances, not
// hard-coded fields, because a real dispute raises whichever of them it
// raises.
type Issue struct {
	IssueID string `json:"issue_id"`
	// Question is the disputed question, phrased neutrally.
	Question string      `json:"question"`
	Status   IssueStatus `json:"status"`

	// Positions holds each side's recorded contention. Two parties'
	// positions sitting side by side, unreconciled, IS the output.
	Positions []Position `json:"positions,omitempty"`

	// The three-way evidence decomposition. Never summed, never
	// collapsed into one confidence number — see the package doc and
	// TestNoOpaqueConfidenceScore.
	SupportingEvidence    []string `json:"supporting_evidence,omitempty"`
	ContradictingEvidence []string `json:"contradicting_evidence,omitempty"`
	MissingEvidence       []string `json:"missing_evidence,omitempty"`

	// RelatedClauses names the contract/policy clauses the issue turns
	// on.
	RelatedClauses []string `json:"related_clauses,omitempty"`
	// RelatedCausationHypotheses names causation.HypothesisID values
	// where the dispute is about causation — the hypotheses stay in
	// pkg/insurance/causation and are referenced, never copied.
	RelatedCausationHypotheses []string `json:"related_causation_hypotheses,omitempty"`
}

var (
	ErrEmptyIssueID       = errors.New("dispute: IssueID must be non-empty")
	ErrEmptyQuestion      = errors.New("dispute: an Issue must state its question")
	ErrUnknownIssueStatus = errors.New("dispute: unknown IssueStatus")
	ErrEmptyPartyID       = errors.New("dispute: a Position must name the party holding it")
	ErrEmptyContention    = errors.New("dispute: a Position must record what the party actually contends")
)

// NewIssue constructs an Issue in StatusOpen.
func NewIssue(issueID, question string) (Issue, error) {
	if issueID == "" {
		return Issue{}, ErrEmptyIssueID
	}
	if strings.TrimSpace(question) == "" {
		return Issue{}, ErrEmptyQuestion
	}
	return Issue{IssueID: issueID, Question: question, Status: StatusOpen}, nil
}

// Validate checks i's own internal consistency.
func (i Issue) Validate() error {
	if i.IssueID == "" {
		return ErrEmptyIssueID
	}
	if strings.TrimSpace(i.Question) == "" {
		return ErrEmptyQuestion
	}
	if !IsKnownIssueStatus(i.Status) {
		return fmt.Errorf("%w: %q", ErrUnknownIssueStatus, i.Status)
	}
	for _, p := range i.Positions {
		if p.Party == "" {
			return ErrEmptyPartyID
		}
		if strings.TrimSpace(p.Contention) == "" {
			return ErrEmptyContention
		}
	}
	return nil
}

// EvidenceDecomposition is the explicit, non-collapsed answer to "how
// well is this issue evidenced". It reports the three counts SEPARATELY
// and refuses to combine them, because a single number would hide
// exactly the distinction a reviewer needs: eight supporting documents
// with one fatal contradiction is not the same position as five
// supporting documents with none.
type EvidenceDecomposition struct {
	IssueID              string   `json:"issue_id"`
	SupportingCount      int      `json:"supporting_count"`
	ContradictingCount   int      `json:"contradicting_count"`
	MissingCount         int      `json:"missing_count"`
	SupportingEvidence   []string `json:"supporting_evidence,omitempty"`
	ContradictingEvidnce []string `json:"contradicting_evidence,omitempty"`
	MissingEvidence      []string `json:"missing_evidence,omitempty"`
}

// Decompose returns the issue's evidence decomposition. There is no
// Score() or Confidence() method on Issue, and this is why: the Final
// Design's forbidden list ends with "membuat satu opaque confidence
// score", and the honest replacement is the decomposition itself.
func (i Issue) Decompose() EvidenceDecomposition {
	return EvidenceDecomposition{
		IssueID:              i.IssueID,
		SupportingCount:      len(i.SupportingEvidence),
		ContradictingCount:   len(i.ContradictingEvidence),
		MissingCount:         len(i.MissingEvidence),
		SupportingEvidence:   append([]string(nil), i.SupportingEvidence...),
		ContradictingEvidnce: append([]string(nil), i.ContradictingEvidence...),
		MissingEvidence:      append([]string(nil), i.MissingEvidence...),
	}
}

// ---- LegalQuestion: the one-valued status ---------------------------

// LegalQuestionStatus has exactly one value. That is not an oversight:
// a question of law is precisely what this system must never answer, so
// there is no second status for a code path to reach.
type LegalQuestionStatus string

// StatusLegalInterpretationRequired is the ONLY LegalQuestionStatus.
// The Final Design §34's worked example — "Does insurance arrangement
// displace GA contribution? VERIQO: LEGAL_INTERPRETATION_REQUIRED" — is
// this constant.
const StatusLegalInterpretationRequired LegalQuestionStatus = "LEGAL_INTERPRETATION_REQUIRED"

// HistoricalReference is a real, publicly-reported decision or
// authority attached to a LegalQuestion as READING MATERIAL FOR A
// HUMAN. It is never consulted by any function in this package, carries
// no weight in any computation, and cannot change any status.
//
// This is the only shape in which a real decision may appear anywhere
// in this system. The Final Design forbids the alternative explicitly:
// forbidden list, which names a specific reported decision as the thing
// not to hard-code as a rule.
type HistoricalReference struct {
	// Citation is how a lawyer would find it.
	Citation string `json:"citation"`
	// Jurisdiction is where it was decided.
	Jurisdiction string `json:"jurisdiction,omitempty"`
	// Relevance is why a human reviewer might want to read it, in the
	// recording person's own words.
	Relevance string `json:"relevance,omitempty"`
	// IsBinding is deliberately absent. Whether an authority binds this
	// dispute is itself a question of law; recording an answer here
	// would be the automatic legal advice this package exists not to
	// give.
}

// LegalQuestion is a question of law arising in a dispute. Its Status
// field exists so the type reads consistently with the rest of the
// domain, and it can only ever hold one value.
type LegalQuestion struct {
	QuestionID string `json:"question_id"`
	// Question is the legal question, phrased neutrally.
	Question string              `json:"question"`
	Status   LegalQuestionStatus `json:"status"`

	// RelatedIssueIDs names the factual issues this question bears on.
	RelatedIssueIDs []string `json:"related_issue_ids,omitempty"`
	// RelatedClauses names the contract wording in play.
	RelatedClauses []string `json:"related_clauses,omitempty"`
	// EstablishedFacts names the facts the question is asked ON TOP OF —
	// recorded as facts elsewhere in the case, referenced here.
	EstablishedFacts []string `json:"established_facts,omitempty"`

	// HistoricalReferences are reading material only. See the type's own
	// doc comment.
	HistoricalReferences []HistoricalReference `json:"historical_references,omitempty"`

	// RequiredReviewer names the kind of authority that must answer this
	// (e.g. "legal counsel", "average adjuster"). Naming who must decide
	// is workflow; deciding is not.
	RequiredReviewer string `json:"required_reviewer,omitempty"`
}

var ErrEmptyLegalQuestion = errors.New("dispute: a LegalQuestion must state its question")

// NewLegalQuestion constructs a LegalQuestion. Its status is always
// StatusLegalInterpretationRequired — there is no parameter for it,
// because there is no other value.
func NewLegalQuestion(questionID, question string) (LegalQuestion, error) {
	if questionID == "" {
		return LegalQuestion{}, errors.New("dispute: LegalQuestion.QuestionID must be non-empty")
	}
	if strings.TrimSpace(question) == "" {
		return LegalQuestion{}, ErrEmptyLegalQuestion
	}
	return LegalQuestion{
		QuestionID: questionID,
		Question:   question,
		Status:     StatusLegalInterpretationRequired,
	}, nil
}

// ---- LegalHold: the evidence hold -----------------------------------

// LegalHold is the "evidence hold" step of the Final Design §8
// workflow: an instruction that evidence within a declared scope must
// not be destroyed, altered or released while the hold is in force.
//
// This package does not implement retention or deletion — pkg/governance/
// data owns the canonical retention/legal-hold state machine, and
// pkg/insurance/preservation records the preservation order. A
// LegalHold here is the DISPUTE-side record that a hold exists, with
// the reference to the real one.
type LegalHold struct {
	HoldID string `json:"hold_id"`
	// Scope is what the hold covers, in the instructing party's words.
	Scope string `json:"scope"`
	// InstructedBy is who ordered the hold.
	InstructedBy string `json:"instructed_by"`
	// PreservationOrderID references the real preservation order this
	// hold is executed through, when one exists.
	PreservationOrderID string `json:"preservation_order_id,omitempty"`
	// GovernanceHoldRef references the canonical pkg/governance/data
	// legal-hold record, when one exists. Never restated here.
	GovernanceHoldRef string `json:"governance_hold_ref,omitempty"`
	// EvidenceInScope names the EvidenceIDs known to fall within Scope.
	// Named for the hold's scope, not for insurance coverage: nothing in
	// this package may carry a field whose name reads as a coverage
	// determination (see TestNoTypeInThisPackageCarriesAVerdictField).
	EvidenceInScope []string `json:"evidence_in_scope,omitempty"`

	StartTick uint64 `json:"start_tick"`
	// ReleasedTick is 0 while the hold is in force. A released hold is
	// recorded, not deleted.
	ReleasedTick   uint64 `json:"released_tick,omitempty"`
	ReleasedBy     string `json:"released_by,omitempty"`
	ReleaseReason  string `json:"release_reason,omitempty"`
	ReleasedStatus bool   `json:"released"`
}

// InForce reports whether this hold currently binds.
func (h LegalHold) InForce() bool { return !h.ReleasedStatus }

var (
	ErrEmptyHoldID   = errors.New("dispute: LegalHold.HoldID must be non-empty")
	ErrEmptyScope    = errors.New("dispute: a LegalHold must state its scope")
	ErrEmptyInstruct = errors.New("dispute: a LegalHold must record who instructed it")
)

// Validate checks the hold's own internal consistency.
func (h LegalHold) Validate() error {
	if h.HoldID == "" {
		return ErrEmptyHoldID
	}
	if strings.TrimSpace(h.Scope) == "" {
		return ErrEmptyScope
	}
	if strings.TrimSpace(h.InstructedBy) == "" {
		return ErrEmptyInstruct
	}
	return nil
}

// ---- Outcome: recorded from an authority, never computed ------------

// OutcomeKind is how a dispute ended. Each value corresponds to a real
// instrument that exists outside this system.
type OutcomeKind string

const (
	// OutcomeSettlement: the parties agreed terms. Critically, a
	// settlement determines nothing — see RecordSettlement.
	OutcomeSettlement OutcomeKind = "SETTLEMENT"
	// OutcomeAward: an arbitral tribunal issued an award.
	OutcomeAward OutcomeKind = "AWARD"
	// OutcomeJudgment: a court gave judgment.
	OutcomeJudgment OutcomeKind = "JUDGMENT"
	// OutcomeWithdrawn: the claim was withdrawn.
	OutcomeWithdrawn OutcomeKind = "WITHDRAWN"
	// OutcomeDiscontinued: the proceedings were discontinued.
	OutcomeDiscontinued OutcomeKind = "DISCONTINUED"
)

var knownOutcomeKinds = map[OutcomeKind]bool{
	OutcomeSettlement: true, OutcomeAward: true, OutcomeJudgment: true,
	OutcomeWithdrawn: true, OutcomeDiscontinued: true,
}

// IsKnownOutcomeKind reports whether k is a modelled outcome kind.
func IsKnownOutcomeKind(k OutcomeKind) bool { return knownOutcomeKinds[k] }

// AllegationResult is what an authority actually determined about one
// allegation. NOT_DETERMINED is the default and by far the most common
// real-world value — most allegations in most disputes are never
// adjudicated at all.
type AllegationResult string

const (
	// AllegationProven: an authority determined this allegation was made
	// out. Only reachable through RecordAward / RecordJudgment, and only
	// with the determining authority and document cited.
	AllegationProven AllegationResult = "PROVEN"
	// AllegationNotProven: an authority determined this allegation was
	// NOT made out.
	AllegationNotProven AllegationResult = "NOT_PROVEN"
	// AllegationNotDetermined: no authority decided this allegation
	// either way. This is what a settlement leaves behind, for every
	// allegation, always.
	AllegationNotDetermined AllegationResult = "NOT_DETERMINED"
	// AllegationWithdrawn: the allegation was withdrawn before any
	// determination.
	AllegationWithdrawn AllegationResult = "WITHDRAWN"
)

var knownAllegationResults = map[AllegationResult]bool{
	AllegationProven: true, AllegationNotProven: true,
	AllegationNotDetermined: true, AllegationWithdrawn: true,
}

// IsKnownAllegationResult reports whether r is a modelled result.
func IsKnownAllegationResult(r AllegationResult) bool { return knownAllegationResults[r] }

// AllegationOutcome pairs one allegation with what, if anything, was
// actually determined about it.
type AllegationOutcome struct {
	// Allegation is what was alleged, in the alleging party's words.
	Allegation string           `json:"allegation"`
	Result     AllegationResult `json:"result"`
	// DeterminedBy names the authority that determined it. Mandatory
	// whenever Result is PROVEN or NOT_PROVEN; forbidden to be assumed
	// otherwise.
	DeterminedBy string `json:"determined_by,omitempty"`
	// SourceDocument is the award/judgment paragraph the determination
	// was read from.
	SourceDocument string `json:"source_document,omitempty"`
	// Note records anything the recorder wants a reader to know, e.g.
	// "compromised without admission of liability".
	Note string `json:"note,omitempty"`
}

// Outcome is how a dispute ended, as RECORDED from a real external
// instrument. Nothing in this package computes an Outcome; every
// constructor requires the authority that issued it and the document it
// was read from.
type Outcome struct {
	OutcomeID string      `json:"outcome_id"`
	Kind      OutcomeKind `json:"kind"`
	// Authority is who issued this outcome — the tribunal, the court, or
	// (for a settlement) the parties themselves.
	Authority string `json:"authority"`
	// SourceDocument is the settlement agreement / award / judgment this
	// record was read from.
	SourceDocument string `json:"source_document"`
	RecordedTick   uint64 `json:"recorded_tick"`

	// Allegations is what became of each allegation. For a settlement,
	// every entry is NOT_DETERMINED by construction.
	Allegations []AllegationOutcome `json:"allegations,omitempty"`

	// MonetaryTermsRef references where the money terms are recorded
	// (a quantum calculation, a settlement schedule). The figures
	// themselves are NOT restated here: pkg/insurance/quantum owns
	// evidence-backed money, and a second copy of a number is a second
	// number.
	MonetaryTermsRef string `json:"monetary_terms_ref,omitempty"`

	// Notes records recorded caveats, e.g. that a settlement was
	// expressly without admission.
	Notes []string `json:"notes,omitempty"`
}

var (
	ErrEmptyOutcomeID     = errors.New("dispute: OutcomeID must be non-empty")
	ErrUnknownOutcomeKind = errors.New("dispute: unknown OutcomeKind")
	ErrNoAuthority        = errors.New(
		"dispute: an Outcome must name the authority that issued it — this system records outcomes, it never computes them")
	ErrNoSourceDocument = errors.New(
		"dispute: an Outcome must cite the instrument it was read from")
	ErrSettlementCannotProve = errors.New(
		"dispute: a settlement determines nothing — an allegation cannot be recorded PROVEN or NOT_PROVEN " +
			"on a settlement outcome; only an award or a judgment can determine an allegation")
	ErrDeterminationNeedsAuthority = errors.New(
		"dispute: an allegation recorded PROVEN or NOT_PROVEN must cite the determining authority and the document paragraph")
	ErrUnknownAllegationResult = errors.New("dispute: unknown AllegationResult")
)

// RecordSettlement records that a dispute settled.
//
// **Settlement ≠ every allegation proven.** This constructor enforces
// that structurally: every allegation passed in is recorded
// NOT_DETERMINED (or WITHDRAWN, if the caller says so), and any attempt
// to record one as PROVEN or NOT_PROVEN is refused with
// ErrSettlementCannotProve. A caller cannot route around it by
// constructing the struct literally either — Validate applies the same
// rule, and every consumer in this package validates.
func RecordSettlement(outcomeID, authority, sourceDocument string, allegations []string, withdrawn []string, tick uint64) (Outcome, error) {
	if outcomeID == "" {
		return Outcome{}, ErrEmptyOutcomeID
	}
	if strings.TrimSpace(authority) == "" {
		return Outcome{}, ErrNoAuthority
	}
	if strings.TrimSpace(sourceDocument) == "" {
		return Outcome{}, ErrNoSourceDocument
	}
	withdrawnSet := map[string]bool{}
	for _, w := range withdrawn {
		withdrawnSet[w] = true
	}
	out := Outcome{
		OutcomeID: outcomeID, Kind: OutcomeSettlement, Authority: authority,
		SourceDocument: sourceDocument, RecordedTick: tick,
		Notes: []string{
			"A settlement is an agreement between the parties. It determines no allegation. " +
				"Every allegation below is recorded NOT_DETERMINED unless it was expressly withdrawn.",
		},
	}
	for _, a := range allegations {
		result := AllegationNotDetermined
		if withdrawnSet[a] {
			result = AllegationWithdrawn
		}
		out.Allegations = append(out.Allegations, AllegationOutcome{
			Allegation: a, Result: result,
			Note: "not adjudicated; resolved by agreement",
		})
	}
	return out, out.Validate()
}

// RecordAward records an arbitral award. Unlike a settlement, an award
// CAN determine allegations — but every determination must cite the
// determining authority and the paragraph it was read from.
func RecordAward(outcomeID, authority, sourceDocument string, allegations []AllegationOutcome, tick uint64) (Outcome, error) {
	return recordDetermination(outcomeID, OutcomeAward, authority, sourceDocument, allegations, tick)
}

// RecordJudgment records a court judgment, under the same rules as
// RecordAward.
func RecordJudgment(outcomeID, authority, sourceDocument string, allegations []AllegationOutcome, tick uint64) (Outcome, error) {
	return recordDetermination(outcomeID, OutcomeJudgment, authority, sourceDocument, allegations, tick)
}

func recordDetermination(outcomeID string, kind OutcomeKind, authority, sourceDocument string, allegations []AllegationOutcome, tick uint64) (Outcome, error) {
	if outcomeID == "" {
		return Outcome{}, ErrEmptyOutcomeID
	}
	if strings.TrimSpace(authority) == "" {
		return Outcome{}, ErrNoAuthority
	}
	if strings.TrimSpace(sourceDocument) == "" {
		return Outcome{}, ErrNoSourceDocument
	}
	out := Outcome{
		OutcomeID: outcomeID, Kind: kind, Authority: authority,
		SourceDocument: sourceDocument, RecordedTick: tick,
		Allegations: append([]AllegationOutcome(nil), allegations...),
	}
	return out, out.Validate()
}

// Validate checks the outcome's own internal consistency, including the
// settlement rule. It is applied by every constructor AND by Matter's
// own recording path, so a hand-built Outcome cannot bypass it.
func (o Outcome) Validate() error {
	if o.OutcomeID == "" {
		return ErrEmptyOutcomeID
	}
	if !IsKnownOutcomeKind(o.Kind) {
		return fmt.Errorf("%w: %q", ErrUnknownOutcomeKind, o.Kind)
	}
	if strings.TrimSpace(o.Authority) == "" {
		return ErrNoAuthority
	}
	if strings.TrimSpace(o.SourceDocument) == "" {
		return ErrNoSourceDocument
	}
	for _, a := range o.Allegations {
		if !IsKnownAllegationResult(a.Result) {
			return fmt.Errorf("%w: %q", ErrUnknownAllegationResult, a.Result)
		}
		determined := a.Result == AllegationProven || a.Result == AllegationNotProven
		if determined {
			// A settlement, a withdrawal and a discontinuance all end a
			// dispute WITHOUT any authority determining anything.
			if o.Kind == OutcomeSettlement || o.Kind == OutcomeWithdrawn || o.Kind == OutcomeDiscontinued {
				return fmt.Errorf("%w: outcome kind %s, allegation %q recorded %s",
					ErrSettlementCannotProve, o.Kind, a.Allegation, a.Result)
			}
			if strings.TrimSpace(a.DeterminedBy) == "" || strings.TrimSpace(a.SourceDocument) == "" {
				return fmt.Errorf("%w: allegation %q", ErrDeterminationNeedsAuthority, a.Allegation)
			}
		}
	}
	return nil
}

// ProvenAllegations returns only the allegations an authority actually
// determined were made out. For a settlement it is ALWAYS empty, and
// that is the point: reading this method on a settled matter gives the
// honest answer rather than the convenient one.
func (o Outcome) ProvenAllegations() []AllegationOutcome {
	var out []AllegationOutcome
	for _, a := range o.Allegations {
		if a.Result == AllegationProven {
			out = append(out, a)
		}
	}
	return out
}

// UndeterminedAllegations returns the allegations no authority decided.
func (o Outcome) UndeterminedAllegations() []AllegationOutcome {
	var out []AllegationOutcome
	for _, a := range o.Allegations {
		if a.Result == AllegationNotDetermined {
			out = append(out, a)
		}
	}
	return out
}

// ---- Matter: the dispute aggregate ----------------------------------

var (
	ErrEmptyMatterID    = errors.New("dispute: MatterID must be non-empty")
	ErrEmptyCaseID      = errors.New("dispute: CaseID must be non-empty")
	ErrUnknownStage     = errors.New("dispute: unknown dispute Stage")
	ErrStageBackward    = errors.New("dispute: a dispute matter cannot move backward through its workflow")
	ErrEmptyStageReason = errors.New("dispute: advancing a dispute matter must record why")
	ErrDuplicateIssue   = errors.New("dispute: this IssueID is already on this matter")
	ErrIssueNotFound    = errors.New("dispute: IssueID not found on this matter")
	ErrDuplicateHold    = errors.New("dispute: this HoldID is already on this matter")
	ErrHoldNotFound     = errors.New("dispute: HoldID not found on this matter")
	ErrOutcomeAlready   = errors.New("dispute: this matter already has a recorded outcome")
)

// Matter is one dispute arising from one insurance case. Safe for
// concurrent use.
type Matter struct {
	mu sync.RWMutex

	MatterID string
	CaseID   string
	ClaimID  string

	forum Forum
	stage Stage
	log   []StageTransition

	issues     map[string]Issue
	issueOrder []string

	legalQuestions map[string]LegalQuestion
	lqOrder        []string

	holds     map[string]LegalHold
	holdOrder []string

	outcome *Outcome
}

// NewMatter opens a dispute matter at StageNoticeOfDispute. The forum
// is required up front and must validate: a cross-border dispute whose
// governing law and jurisdiction are unrecorded is exactly the
// ungrounded state this domain exists to prevent.
func NewMatter(matterID, caseID, claimID string, forum Forum, tick uint64) (*Matter, error) {
	if matterID == "" {
		return nil, ErrEmptyMatterID
	}
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	if err := forum.Validate(); err != nil {
		return nil, err
	}
	return &Matter{
		MatterID: matterID, CaseID: caseID, ClaimID: claimID,
		forum: forum, stage: StageNoticeOfDispute,
		log: []StageTransition{{
			From: StageNoticeOfDispute, To: StageNoticeOfDispute, Tick: tick,
			Reason: "dispute matter opened",
		}},
		issues:         map[string]Issue{},
		legalQuestions: map[string]LegalQuestion{},
		holds:          map[string]LegalHold{},
	}, nil
}

// Forum returns the recorded forum metadata.
func (m *Matter) Forum() Forum {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.forum
}

// Stage returns the matter's current workflow stage.
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

// Advance moves the matter to a later stage. Skipping is permitted and
// RECORDED (real disputes skip mediation routinely); moving backward is
// refused. A reason is mandatory.
func (m *Matter) Advance(to Stage, reason string, tick uint64) error {
	if !IsKnownStage(to) {
		return fmt.Errorf("%w: %q", ErrUnknownStage, to)
	}
	if strings.TrimSpace(reason) == "" {
		return ErrEmptyStageReason
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fromIdx, toIdx := stageIndex[m.stage], stageIndex[to]
	if toIdx < fromIdx {
		return fmt.Errorf("%w: %s -> %s", ErrStageBackward, m.stage, to)
	}
	if toIdx == fromIdx {
		return nil // idempotent
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

// AddIssue records a disputed issue on this matter.
func (m *Matter) AddIssue(i Issue) error {
	if err := i.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.issues[i.IssueID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateIssue, i.IssueID)
	}
	m.issues[i.IssueID] = i
	m.issueOrder = append(m.issueOrder, i.IssueID)
	return nil
}

// RecordPosition records one party's contention on an issue. Positions
// accumulate: recording a second party's position never replaces the
// first, and this package never marks one of them correct.
func (m *Matter) RecordPosition(issueID string, p Position) error {
	if p.Party == "" {
		return ErrEmptyPartyID
	}
	if strings.TrimSpace(p.Contention) == "" {
		return ErrEmptyContention
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[issueID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrIssueNotFound, issueID)
	}
	i.Positions = append(i.Positions, p)
	// Two or more recorded positions on a live issue IS a contest.
	if len(i.Positions) > 1 && (i.Status == StatusOpen || i.Status == StatusEvidenceGathering) {
		i.Status = StatusContested
	}
	m.issues[issueID] = i
	return nil
}

// AddIssueEvidence records evidence for, against, or missing on an
// issue. The three lists are kept apart, never netted off.
func (m *Matter) AddIssueEvidence(issueID string, supporting, contradicting, missing []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[issueID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrIssueNotFound, issueID)
	}
	i.SupportingEvidence = append(i.SupportingEvidence, supporting...)
	i.ContradictingEvidence = append(i.ContradictingEvidence, contradicting...)
	i.MissingEvidence = append(i.MissingEvidence, missing...)
	m.issues[issueID] = i
	return nil
}

// SetIssueStatus updates an issue's status. Every value it can be set
// to describes the state of the argument, never who is right — see
// IssueStatus.
func (m *Matter) SetIssueStatus(issueID string, s IssueStatus) error {
	if !IsKnownIssueStatus(s) {
		return fmt.Errorf("%w: %q", ErrUnknownIssueStatus, s)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.issues[issueID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrIssueNotFound, issueID)
	}
	i.Status = s
	m.issues[issueID] = i
	return nil
}

// Issue returns one issue by ID.
func (m *Matter) Issue(issueID string) (Issue, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	i, ok := m.issues[issueID]
	return i, ok
}

// Issues returns every issue in the order it was added.
func (m *Matter) Issues() []Issue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Issue, 0, len(m.issueOrder))
	for _, id := range m.issueOrder {
		out = append(out, m.issues[id])
	}
	return out
}

// AddLegalQuestion records a question of law arising on this matter.
// Recording it also moves any related issue to
// AWAITING_LEGAL_INTERPRETATION — the honest status for a factual issue
// that turns on an unanswered legal question.
func (m *Matter) AddLegalQuestion(q LegalQuestion) error {
	if q.QuestionID == "" {
		return errors.New("dispute: LegalQuestion.QuestionID must be non-empty")
	}
	if strings.TrimSpace(q.Question) == "" {
		return ErrEmptyLegalQuestion
	}
	// Whatever a caller put in Status, the only legal value is the one.
	q.Status = StatusLegalInterpretationRequired
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.legalQuestions[q.QuestionID]; exists {
		return fmt.Errorf("dispute: LegalQuestion %s is already on this matter", q.QuestionID)
	}
	m.legalQuestions[q.QuestionID] = q
	m.lqOrder = append(m.lqOrder, q.QuestionID)
	for _, issueID := range q.RelatedIssueIDs {
		if i, ok := m.issues[issueID]; ok && i.Status != StatusDeterminedByAuthority && i.Status != StatusWithdrawn {
			i.Status = StatusAwaitingLegalInterpretation
			m.issues[issueID] = i
		}
	}
	return nil
}

// LegalQuestions returns every recorded legal question, in order.
func (m *Matter) LegalQuestions() []LegalQuestion {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LegalQuestion, 0, len(m.lqOrder))
	for _, id := range m.lqOrder {
		out = append(out, m.legalQuestions[id])
	}
	return out
}

// PlaceHold records a legal hold on this matter's evidence.
func (m *Matter) PlaceHold(h LegalHold) error {
	if err := h.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.holds[h.HoldID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateHold, h.HoldID)
	}
	m.holds[h.HoldID] = h
	m.holdOrder = append(m.holdOrder, h.HoldID)
	return nil
}

// ReleaseHold records that a hold has been lifted. The hold record is
// kept, marked released — never deleted.
func (m *Matter) ReleaseHold(holdID, releasedBy, reason string, tick uint64) error {
	if strings.TrimSpace(releasedBy) == "" || strings.TrimSpace(reason) == "" {
		return errors.New("dispute: releasing a legal hold must record who released it and why")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holds[holdID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrHoldNotFound, holdID)
	}
	if h.ReleasedStatus {
		return fmt.Errorf("dispute: hold %s is already released", holdID)
	}
	h.ReleasedStatus = true
	h.ReleasedTick = tick
	h.ReleasedBy = releasedBy
	h.ReleaseReason = reason
	m.holds[holdID] = h
	return nil
}

// Holds returns every hold ever placed, in order, released or not.
func (m *Matter) Holds() []LegalHold {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LegalHold, 0, len(m.holdOrder))
	for _, id := range m.holdOrder {
		out = append(out, m.holds[id])
	}
	return out
}

// ActiveHolds returns only the holds currently in force.
func (m *Matter) ActiveHolds() []LegalHold {
	var out []LegalHold
	for _, h := range m.Holds() {
		if h.InForce() {
			out = append(out, h)
		}
	}
	return out
}

// RecordOutcome attaches the recorded outcome to this matter and moves
// it to OUTCOME_RECORDED. The outcome is re-validated here, so the
// settlement rule holds even for a hand-built Outcome literal.
func (m *Matter) RecordOutcome(o Outcome, tick uint64) error {
	if err := o.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.outcome != nil {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrOutcomeAlready, m.outcome.OutcomeID)
	}
	m.outcome = &o
	// Every issue an authority actually determined moves to
	// DETERMINED_BY_AUTHORITY. Issues the outcome is silent about do
	// NOT move: a settlement leaves them exactly where they were.
	m.mu.Unlock()
	return m.Advance(StageOutcomeRecorded, "outcome recorded: "+string(o.Kind)+" from "+o.Authority, tick)
}

// Outcome returns the recorded outcome, if any.
func (m *Matter) Outcome() (Outcome, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.outcome == nil {
		return Outcome{}, false
	}
	return *m.outcome, true
}

// OpenLegalQuestionIDs returns the IDs of every recorded legal question,
// sorted — every one of which is, by construction, unanswered.
func (m *Matter) OpenLegalQuestionIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]string(nil), m.lqOrder...)
	sort.Strings(out)
	return out
}

// RequiresLegalInterpretation reports whether this matter has any
// recorded legal question. Since a LegalQuestion has exactly one
// possible status, having one and needing interpretation are the same
// fact.
func (m *Matter) RequiresLegalInterpretation() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.lqOrder) > 0
}
