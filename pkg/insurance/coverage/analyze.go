package coverage

import (
	"errors"
	"fmt"

	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/policy"
)

var (
	ErrEmptyClaimID          = errors.New("coverage: Input.Claim.ClaimID must be non-empty")
	ErrEmptyPolicyVersionID  = errors.New("coverage: Input.PolicyVersion.VersionID must be non-empty")
	ErrPolicyVersionMismatch = errors.New("coverage: Input.Claim.PolicyVersionID does not match Input.PolicyVersion.VersionID — Analyze must be called with the policy.History.EffectiveAt-resolved Version pinned to this claim, never a different or newer one")
)

// Input is everything Analyze needs: POLICY + INCIDENT + CLAIM +
// EVIDENCE, exactly the four blueprint §18 names as this engine's only
// inputs.
//
// PolicyVersion is deliberately a resolved policy.Version, not a
// policy.History. Blueprint §6 is P0: "VICE harus menentukan: Which
// policy version was effective at the time of the incident? Tidak
// boleh memakai policy terbaru secara otomatis." That resolution is
// policy.History.EffectiveAt's job, already built and tested in
// pkg/insurance/policy — this package does not re-derive it. The
// caller resolves the effective version (typically via
// history.EffectiveAt(incidentTick)) and hands the result in here;
// Analyze only cross-checks it against Claim.PolicyVersionID when that
// field is set (see ErrPolicyVersionMismatch).
//
// INCIDENT is represented as explicit ticks (IncidentTick, NoticeTick,
// NoticeDeadlineTick) rather than re-derived from claim.Claim, because
// claim.Claim itself carries no date fields — that data belongs to the
// timeline engine (blueprint §13) and, for deadlines, the deadline
// engine (blueprint §24); this package consumes their output, it does
// not reimplement timeline reconstruction or deadline calculation.
// NoticeDeadlineTick == 0 means "no deadline rule was supplied", which
// this package answers honestly with StatusPartial/StatusInsufficientEvidence
// rather than guessing a duration.
type Input struct {
	Claim         claim.Claim
	PolicyVersion policy.Version
	Evidence      []evidence.Record

	// TypeDef is the resolved claim.TypeDefinition for Claim.Type
	// (typically claim.TypeRegistry.Get(cl.Type)) — its
	// RequiredEvidence and PolicyQuestions drive facts and questions
	// directly. Nil is accepted (those facts/questions are simply
	// omitted), so this package never re-implements the claim-type
	// registry.
	TypeDef *claim.TypeDefinition

	IncidentTick       uint64 // 0 = unknown
	NoticeTick         uint64 // 0 = unknown
	NoticeDeadlineTick uint64 // 0 = no deadline rule supplied

	// *DocTypes name which evidence.Record.DocumentType values count
	// as evidence toward each fact — e.g. PerilDocTypes might be
	// {"survey_report", "incident_report"}. This package does not
	// guess which document types are relevant to which fact; that
	// mapping is supplied by the caller (who has the claim type's own
	// domain knowledge), matching the blueprint's insistence that
	// coverage facts stay traceable to specific, named evidence.
	PerilDocTypes   []string
	NoticeDocTypes  []string
	QuantumDocTypes []string

	// *ClauseID(s) name the policy.Clause.ClauseID(s) that govern each
	// fact (e.g. PerilClauseID = "§4"). Empty is accepted — the
	// resulting fact simply carries no ClauseIDs — because not every
	// deployment will have clause-purpose tagging available yet.
	PeriodClauseID     string
	PerilClauseID      string
	ExclusionClauseIDs []string
	NoticeClauseID     string
	QuantumClauseID    string
}

// Analyze computes a CoverageAnalysis from in. It performs no legal or
// factual guessing: every CoverageFact is either derived mechanically
// from tick comparisons and evidence.Record.Status, or is explicitly
// StatusInsufficientEvidence when the inputs don't support a
// conclusion. Analyze never returns anything resembling
// "covered: true/false" — see the package doc comment.
func Analyze(in Input) (CoverageAnalysis, error) {
	if in.Claim.ClaimID == "" {
		return CoverageAnalysis{}, ErrEmptyClaimID
	}
	if in.PolicyVersion.VersionID == "" {
		return CoverageAnalysis{}, ErrEmptyPolicyVersionID
	}
	if in.Claim.PolicyVersionID != "" && in.Claim.PolicyVersionID != in.PolicyVersion.VersionID {
		return CoverageAnalysis{}, fmt.Errorf("%w: claim pins %q, got %q", ErrPolicyVersionMismatch, in.Claim.PolicyVersionID, in.PolicyVersion.VersionID)
	}

	var facts []CoverageFact
	facts = append(facts, in.withinPolicyPeriodFact())

	perilFact := in.perilOccurredFact()
	facts = append(facts, perilFact)

	exclusionFacts := in.exclusionFacts()
	facts = append(facts, exclusionFacts...)

	facts = append(facts, in.noticeTimelyFact())
	facts = append(facts, in.quantumSupportedFact())
	facts = append(facts, in.requiredEvidenceFacts()...)

	// A contested exclusion is only in tension with the claim when the
	// peril it might exclude is itself supported — golden rule #2:
	// never silently resolved either way, so this always yields a
	// DISPUTED conflict, never a quiet pass-through.
	var conflicts []CoverageConflict
	if perilFact.Status == StatusSupported {
		for _, ef := range exclusionFacts {
			suffix := ""
			if len(ef.ClauseIDs) > 0 {
				suffix = ef.ClauseIDs[0]
			}
			c, err := NewCoverageConflict(
				"exclusion_vs_peril:"+suffix,
				"the claimed peril appears established by the available evidence, but an exclusion clause potentially applies to it — this tension is contested and is not resolved by this package",
				[]string{perilFact.FactID, ef.FactID},
				ef.ClauseIDs,
			)
			if err != nil {
				return CoverageAnalysis{}, err
			}
			conflicts = append(conflicts, c)
		}
	}

	var questions []CoverageQuestion
	questions = append(questions, in.policyQuestions()...)
	for _, f := range facts {
		if f.Status == StatusInsufficientEvidence {
			questions = append(questions, CoverageQuestion{
				QuestionID:    "insufficient:" + f.FactID,
				Question:      fmt.Sprintf("What additional evidence, if any, is available regarding: %s?", f.Description),
				RelatedFactID: f.FactID,
				ClauseIDs:     append([]string(nil), f.ClauseIDs...),
				Reason:        "this fact could not be evaluated from the evidence supplied — see blueprint §36",
			})
		}
	}

	reviewRequired := false
	var reasons []string
	for _, f := range facts {
		if f.Status != StatusSupported {
			reviewRequired = true
			reasons = append(reasons, fmt.Sprintf("fact %s has status %s", f.FactID, f.Status))
		}
	}
	for _, c := range conflicts {
		reviewRequired = true
		reasons = append(reasons, fmt.Sprintf("conflict %s is unresolved", c.ConflictID))
	}
	for _, q := range questions {
		reviewRequired = true
		reasons = append(reasons, fmt.Sprintf("question %s is open", q.QuestionID))
	}

	return CoverageAnalysis{
		CaseID:          in.Claim.CaseID,
		ClaimID:         in.Claim.ClaimID,
		PolicyID:        in.PolicyVersion.PolicyID,
		PolicyVersionID: in.PolicyVersion.VersionID,
		Facts:           facts,
		Questions:       questions,
		Conflicts:       conflicts,
		ReviewRequired:  reviewRequired,
		ReviewReasons:   reasons,
	}, nil
}

// ---- fact computation ----

// filterByDocType returns every record whose DocumentType exactly
// matches docType, preserving order.
func filterByDocType(records []evidence.Record, docType string) []evidence.Record {
	var out []evidence.Record
	for _, r := range records {
		if r.DocumentType == docType {
			out = append(out, r)
		}
	}
	return out
}

// statusFromRecords derives a FactStatus from a set of matched
// evidence records, using their VERIFICATION status
// (evidence.Record.Status) — never their Origin, matching the
// blueprint's neutrality rule (§28: "Evidence origin ≠ evidence
// truth"). No records at all is StatusInsufficientEvidence, never a
// silently-omitted fact.
func statusFromRecords(records []evidence.Record) (FactStatus, []string) {
	if len(records) == 0 {
		return StatusInsufficientEvidence, nil
	}
	evIDs := make([]string, 0, len(records))
	disputed := false
	allSupported := true
	for _, r := range records {
		evIDs = append(evIDs, r.EvidenceID())
		switch r.Status {
		case evidence.StatusContradicted, evidence.StatusAlterationDetected, evidence.StatusAuthenticityDisputed:
			disputed = true
			allSupported = false
		case evidence.StatusAuthenticitySupported, evidence.StatusCorroborated:
			// counts toward SUPPORTED
		default: // UNVERIFIED, INCOMPLETE, or unrecognised
			allSupported = false
		}
	}
	sorted := sortedCopy(evIDs)
	switch {
	case disputed:
		return StatusDisputed, sorted
	case allSupported:
		return StatusSupported, sorted
	default:
		return StatusPartial, sorted
	}
}

// withinPolicyPeriodFact is §19's "Within policy period" row, computed
// for real from IncidentTick against PolicyVersion's
// [EffectiveFrom, EffectiveTo) window (EffectiveTo == 0 meaning
// open-ended, matching policy.Version's own convention).
func (in Input) withinPolicyPeriodFact() CoverageFact {
	fact := CoverageFact{
		FactID:          "within_policy_period",
		PolicyVersionID: in.PolicyVersion.VersionID,
	}
	if in.PeriodClauseID != "" {
		fact.ClauseIDs = []string{in.PeriodClauseID}
	}
	if in.IncidentTick == 0 {
		fact.Status = StatusInsufficientEvidence
		fact.Description = "whether the claim's incident date falls within the effective policy version's period"
		fact.Notes = "incident tick is unknown; cannot compare against the policy version's effective window"
		return fact
	}
	from, to := in.PolicyVersion.EffectiveFrom, in.PolicyVersion.EffectiveTo
	within := in.IncidentTick >= from && (to == 0 || in.IncidentTick < to)
	fact.Status = StatusSupported
	if within {
		fact.Description = "the claim's incident date falls within the effective policy version's period"
	} else {
		fact.Description = "the claim's incident date falls outside the effective policy version's period"
	}
	fact.Notes = fmt.Sprintf("incident_tick=%d effective_from=%d effective_to=%d", in.IncidentTick, from, to)
	return fact
}

// perilOccurredFact is §19's "Peril occurred" row: whether evidence of
// the configured PerilDocTypes (e.g. survey + incident report)
// supports that the claimed peril occurred.
func (in Input) perilOccurredFact() CoverageFact {
	fact := CoverageFact{
		FactID:          "peril_occurred",
		Description:     "whether the available evidence supports that the claimed peril occurred",
		PolicyVersionID: in.PolicyVersion.VersionID,
	}
	if in.PerilClauseID != "" {
		fact.ClauseIDs = []string{in.PerilClauseID}
	}
	var records []evidence.Record
	for _, dt := range in.PerilDocTypes {
		records = append(records, filterByDocType(in.Evidence, dt)...)
	}
	fact.Status, fact.EvidenceIDs = statusFromRecords(records)
	if len(in.PerilDocTypes) == 0 {
		fact.Notes = "no peril-indicating evidence document types were configured for this analysis"
	}
	return fact
}

// exclusionFacts is §19's "Exclusion applies" row, one per clause in
// ExclusionClauseIDs. Its Status is ALWAYS StatusDisputed: a policy
// exclusion clause that could plausibly apply is, by the blueprint's
// golden rule, never silently resolved either way by this package.
// There is no computation here to get "wrong" — that is the point.
func (in Input) exclusionFacts() []CoverageFact {
	var out []CoverageFact
	for _, clauseID := range in.ExclusionClauseIDs {
		out = append(out, CoverageFact{
			FactID:          "exclusion_applies:" + clauseID,
			Description:     "an exclusion clause potentially applies to this claim and is contested",
			Status:          StatusDisputed,
			PolicyVersionID: in.PolicyVersion.VersionID,
			ClauseIDs:       []string{clauseID},
			Notes:           "presence of a potentially-applicable exclusion clause is never silently resolved by this package — blueprint §36",
		})
	}
	return out
}

// noticeTimelyFact is §19's "Notice timely" row. It also implements
// blueprint §14's "notice submitted before incident" temporal-conflict
// check as a real comparison, not a placeholder.
func (in Input) noticeTimelyFact() CoverageFact {
	fact := CoverageFact{
		FactID:          "notice_timely",
		PolicyVersionID: in.PolicyVersion.VersionID,
	}
	if in.NoticeClauseID != "" {
		fact.ClauseIDs = []string{in.NoticeClauseID}
	}
	if len(in.NoticeDocTypes) > 0 {
		var records []evidence.Record
		for _, dt := range in.NoticeDocTypes {
			records = append(records, filterByDocType(in.Evidence, dt)...)
		}
		evIDs := make([]string, 0, len(records))
		for _, r := range records {
			evIDs = append(evIDs, r.EvidenceID())
		}
		fact.EvidenceIDs = sortedCopy(evIDs)
	}

	switch {
	case in.NoticeTick == 0:
		fact.Status = StatusInsufficientEvidence
		fact.Description = "whether notice of the claim was given timely"
		fact.Notes = "notice tick is unknown"
	case in.IncidentTick != 0 && in.NoticeTick < in.IncidentTick:
		fact.Status = StatusDisputed
		fact.Description = "notice was recorded before the incident it purports to notify — temporal conflict (blueprint §14)"
		fact.Notes = fmt.Sprintf("notice_tick=%d precedes incident_tick=%d", in.NoticeTick, in.IncidentTick)
	case in.NoticeDeadlineTick == 0:
		fact.Status = StatusPartial
		fact.Description = "notice was given, but no notice-deadline rule was supplied to assess timeliness against"
		fact.Notes = fmt.Sprintf("notice_tick=%d", in.NoticeTick)
	case in.NoticeTick <= in.NoticeDeadlineTick:
		fact.Status = StatusSupported
		fact.Description = "notice was given within the applicable deadline"
		fact.Notes = fmt.Sprintf("notice_tick=%d deadline_tick=%d", in.NoticeTick, in.NoticeDeadlineTick)
	default:
		fact.Status = StatusDisputed
		fact.Description = "notice was given after the applicable deadline"
		fact.Notes = fmt.Sprintf("notice_tick=%d deadline_tick=%d", in.NoticeTick, in.NoticeDeadlineTick)
	}
	return fact
}

// quantumSupportedFact is §19's "Quantum supported" row. Its own
// worked example uses two evidence categories (invoice + survey) and
// resolves to Partial — this implementation reproduces that shape
// exactly: PARTIAL when only some of the configured QuantumDocTypes
// have matching evidence, SUPPORTED only when all of them do.
func (in Input) quantumSupportedFact() CoverageFact {
	fact := CoverageFact{
		FactID:          "quantum_supported",
		Description:     "whether the claimed quantum is supported by the required categories of evidence",
		PolicyVersionID: in.PolicyVersion.VersionID,
	}
	if in.QuantumClauseID != "" {
		fact.ClauseIDs = []string{in.QuantumClauseID}
	}
	if len(in.QuantumDocTypes) == 0 {
		fact.Status = StatusInsufficientEvidence
		fact.Notes = "no quantum-supporting evidence document types were configured for this analysis"
		return fact
	}

	matchedTypes := 0
	disputed := false
	var evIDs []string
	for _, dt := range in.QuantumDocTypes {
		recs := filterByDocType(in.Evidence, dt)
		if len(recs) == 0 {
			continue
		}
		matchedTypes++
		for _, r := range recs {
			evIDs = append(evIDs, r.EvidenceID())
			switch r.Status {
			case evidence.StatusContradicted, evidence.StatusAlterationDetected, evidence.StatusAuthenticityDisputed:
				disputed = true
			}
		}
	}
	fact.EvidenceIDs = sortedCopy(evIDs)
	switch {
	case matchedTypes == 0:
		fact.Status = StatusInsufficientEvidence
	case disputed:
		fact.Status = StatusDisputed
	case matchedTypes < len(in.QuantumDocTypes):
		fact.Status = StatusPartial
	default:
		fact.Status = StatusSupported
	}
	return fact
}

// requiredEvidenceFacts produces one CoverageFact per item in
// TypeDef.RequiredEvidence (claim.TypeDefinition — blueprint §4),
// tying the coverage engine directly to the per-claim-type evidence
// requirements the claim registry already declares, instead of
// re-deriving a parallel notion of "what evidence should exist" here.
func (in Input) requiredEvidenceFacts() []CoverageFact {
	if in.TypeDef == nil {
		return nil
	}
	out := make([]CoverageFact, 0, len(in.TypeDef.RequiredEvidence))
	for _, item := range in.TypeDef.RequiredEvidence {
		status, evIDs := statusFromRecords(filterByDocType(in.Evidence, item))
		out = append(out, CoverageFact{
			FactID:          "required_evidence:" + item,
			Description:     fmt.Sprintf("required evidence %q for claim type %s", item, in.Claim.Type),
			Status:          status,
			PolicyVersionID: in.PolicyVersion.VersionID,
			EvidenceIDs:     evIDs,
		})
	}
	return out
}

// policyQuestions surfaces claim.TypeDefinition.PolicyQuestions
// (blueprint §4: "per-claim-type questions the coverage engine should
// be asking") directly as neutrally-phrased CoverageQuestions.
func (in Input) policyQuestions() []CoverageQuestion {
	if in.TypeDef == nil {
		return nil
	}
	out := make([]CoverageQuestion, 0, len(in.TypeDef.PolicyQuestions))
	for i, q := range in.TypeDef.PolicyQuestions {
		out = append(out, CoverageQuestion{
			QuestionID: fmt.Sprintf("policy_question:%d", i+1),
			Question:   q,
			Reason:     fmt.Sprintf("standing policy question registered for claim type %s", in.Claim.Type),
		})
	}
	return out
}
