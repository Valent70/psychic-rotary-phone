// Package api is VICE's unified Facade — blueprint §32. It wraps one
// *caseinsurance.Case together with every other pkg/insurance
// package's engine, and exposes one method per §32 endpoint. Each
// analysis method does two things, in order: (1) call the real
// underlying package's logic — never a re-implementation — and (2)
// advance the case's lifecycle state exactly one step, so the state
// machine's own one-step-forward-only rule (pkg/insurance/case) is
// what makes "call AnalyzeCausation before EvidenceIngested" or any
// other out-of-order use structurally impossible, not just documented.
//
// Mirrors the precedent set by pkg/evidence/api.Facade: callers get a
// small, named set of doors into VICE, never direct access to any one
// domain package's internals.
package api

import (
	"errors"
	"fmt"

	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/contradiction"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/deadline"
	"veriqo/pkg/insurance/dossier"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/gap"
	"veriqo/pkg/insurance/mitigation"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/recovery"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/insurance/verification"
)

var (
	ErrEmptyCaseID     = errors.New("api: CaseID must be non-empty")
	ErrNoParties       = errors.New("api: at least one party is required")
	ErrClaimNotYetSet  = errors.New("api: no claim has been registered on this case yet")
	ErrPolicyNotYetSet = errors.New("api: no policy has been registered on this case yet")
)

// PartySpec is one party to register during IdentifyParties.
type PartySpec struct {
	ID    party.PartyID
	Name  string
	Roles []party.Role
}

// Facade is VICE's single entry point for driving one case through its
// full lifecycle. The zero value is not usable — construct with New.
type Facade struct {
	c *insurancecase.Case

	claimTypes *claim.TypeRegistry
	evDeps     *evidence.DependencyGraph

	tl                *timeline.Timeline
	timelineConflicts []timeline.Conflict

	contradictionEngine *contradiction.Engine
	contradictions      []contradiction.ContradictionRecord

	gapAssessment *gap.Assessment

	causationExplanation *causation.Explanation

	quantumCalc        *quantum.Calculation
	quantumDiscrepancy *quantum.QuantumDiscrepancy

	mitigationTimeline *mitigation.Timeline
	mitigationImpact   *mitigation.Impact

	recoveryRegistry *recovery.Registry

	deadlineRegistry *deadline.Registry
	deadlines        []deadline.Deadline

	coverageAnalysis *coverage.CoverageAnalysis

	activeClaimID string
}

// New constructs a Facade around a brand-new case in CASE_CREATED,
// with every sub-engine wired and ready. claimTypes may be nil (claim
// registration then skips type validation, mirroring
// pkg/insurance/claim.New's own nil-registry behaviour).
func New(caseID string, createdAtTick uint64, claimTypes *claim.TypeRegistry) (*Facade, error) {
	c, err := insurancecase.New(caseID, createdAtTick)
	if err != nil {
		return nil, err
	}
	tl, err := timeline.NewTimeline(caseID)
	if err != nil {
		return nil, err
	}
	recoveryReg, err := recovery.NewRegistry(caseID)
	if err != nil {
		return nil, err
	}
	mitigationTl, err := mitigation.NewTimeline(caseID)
	if err != nil {
		return nil, err
	}
	return &Facade{
		c:                   c,
		claimTypes:          claimTypes,
		evDeps:              evidence.NewDependencyGraph(),
		tl:                  tl,
		contradictionEngine: contradiction.NewEngine(),
		mitigationTimeline:  mitigationTl,
		recoveryRegistry:    recoveryReg,
		deadlineRegistry:    deadline.NewRegistry(),
	}, nil
}

// Case exposes the underlying case's read-only surface (State,
// StateLog, ...). It does NOT expose Advance — lifecycle progression
// only ever happens through this Facade's own methods.
func (f *Facade) Case() *insurancecase.Case { return f.c }

// IdentifyParties registers every party in specs and advances the
// case to PARTIES_IDENTIFIED. Blueprint §32 step 1.
func (f *Facade) IdentifyParties(tick uint64, specs []PartySpec) error {
	if len(specs) == 0 {
		return ErrNoParties
	}
	for _, s := range specs {
		if _, err := f.c.Parties.Register(s.ID, s.Name, s.Roles...); err != nil {
			return fmt.Errorf("api: registering party %s: %w", s.ID, err)
		}
	}
	return f.c.Advance(insurancecase.StatePartiesIdentified, tick)
}

// RegisterPolicy attaches a policy History (already built by the
// caller — this package never re-implements version selection) and
// advances the case to POLICY_REGISTERED. Blueprint §32 step 2.
func (f *Facade) RegisterPolicy(tick uint64, policyID string, h *policy.History) error {
	if err := f.c.RegisterPolicy(policyID, h); err != nil {
		return err
	}
	return f.c.Advance(insurancecase.StatePolicyRegistered, tick)
}

// RegisterClaim registers cl and advances the case to
// CLAIM_REGISTERED. Blueprint §32 step 3.
func (f *Facade) RegisterClaim(tick uint64, cl claim.Claim) error {
	if err := f.c.RegisterClaim(cl); err != nil {
		return err
	}
	f.activeClaimID = cl.ClaimID
	return f.c.Advance(insurancecase.StateClaimRegistered, tick)
}

// DependencyEdge declares that Child depends on (was derived from, or
// cites) Parent — see evidence.DependencyGraph. A child may have more
// than one parent, which a plain map cannot represent, hence a slice
// of edges rather than a map[child]parent.
type DependencyEdge struct {
	Child  string
	Parent string
}

// IngestEvidence submits every record to the case's evidence registry
// and records any caller-declared dependency edges, then advances the
// case to EVIDENCE_INGESTED. Blueprint §32 step 4.
func (f *Facade) IngestEvidence(tick uint64, records []evidence.Record, dependencies []DependencyEdge) error {
	for _, r := range records {
		if err := f.c.Evidence.Submit(r); err != nil {
			return fmt.Errorf("api: submitting evidence %s: %w", r.EvidenceID(), err)
		}
	}
	for _, e := range dependencies {
		if err := f.evDeps.AddDependency(e.Child, e.Parent); err != nil {
			return fmt.Errorf("api: recording evidence dependency %s->%s: %w", e.Child, e.Parent, err)
		}
	}
	return f.c.Advance(insurancecase.StateEvidenceIngested, tick)
}

// VerifyEvidence applies status/strength updates already determined by
// the caller (authenticity checks, corroboration review, ...) and
// advances the case to EVIDENCE_VERIFIED. Blueprint §32 step 5. This
// package performs no authenticity judgment itself — it only records
// what the caller has already determined, exactly like
// evidence.Registry.SetStatus/SetStrength do.
func (f *Facade) VerifyEvidence(tick uint64, statuses map[string]evidence.Status, strengths map[string]evidence.Strength) error {
	for id, s := range statuses {
		if err := f.c.Evidence.SetStatus(id, s); err != nil {
			return fmt.Errorf("api: setting status for %s: %w", id, err)
		}
	}
	for id, s := range strengths {
		if err := f.c.Evidence.SetStrength(id, s); err != nil {
			return fmt.Errorf("api: setting strength for %s: %w", id, err)
		}
	}
	return f.c.Advance(insurancecase.StateEvidenceVerified, tick)
}

// ReconstructTimeline adds every event to the case's timeline, runs
// real conflict detection over it (pkg/insurance/timeline.Detector,
// evidence-registry-aware), and advances the case to
// TIMELINE_RECONSTRUCTED. Blueprint §32 step 6.
func (f *Facade) ReconstructTimeline(tick uint64, events []timeline.Event) ([]timeline.Conflict, error) {
	for _, e := range events {
		if err := f.tl.AddEvent(e); err != nil {
			return nil, fmt.Errorf("api: adding timeline event %s: %w", e.EventID, err)
		}
	}
	detector := timeline.NewDetector().WithEvidence(f.c.Evidence)
	f.timelineConflicts = detector.DetectConflicts(f.tl)
	if err := f.c.Advance(insurancecase.StateTimelineReconstructed, tick); err != nil {
		return nil, err
	}
	return f.timelineConflicts, nil
}

// MapPolicy resolves which policy version was effective at
// incidentTick (pkg/insurance/policy.History.EffectiveAt — never the
// latest version), records it on the active claim, and advances the
// case to CONTRACT_POLICY_MAPPED. Blueprint §32 step 7.
func (f *Facade) MapPolicy(tick, incidentTick uint64, policyID string) (policy.Version, error) {
	if f.activeClaimID == "" {
		return policy.Version{}, ErrClaimNotYetSet
	}
	v, err := f.c.PolicyEffectiveAt(policyID, incidentTick)
	if err != nil {
		return policy.Version{}, err
	}
	cl, err := f.c.ClaimByID(f.activeClaimID)
	if err != nil {
		return policy.Version{}, err
	}
	cl.PolicyVersionID = v.VersionID
	f.c.Claims[f.activeClaimID] = cl
	return v, f.c.Advance(insurancecase.StatePolicyMapped, tick)
}

// AnalyzeContradictions runs the real contradiction.Engine's Detect
// over claimKey/category and advances the case to
// CONTRADICTIONS_ANALYZED. Blueprint §32 step 8. Submissions to the
// contradiction engine (Submit) are expected to already have happened
// via SubmitContradictionObservation before this is called — Detect
// only projects what has been observed so far, it never fabricates an
// observation.
func (f *Facade) AnalyzeContradictions(tick uint64, claimKey string, category contradiction.PairKind, description string, halfLifeTicks uint64) ([]contradiction.ContradictionRecord, error) {
	recs, err := f.contradictionEngine.Detect(claimKey, category, description, tick, halfLifeTicks)
	if err != nil {
		return nil, err
	}
	f.contradictions = append(f.contradictions, recs...)
	if err := f.c.Advance(insurancecase.StateContradictionsAnalyzed, tick); err != nil {
		return nil, err
	}
	return recs, nil
}

// SubmitContradictionObservation feeds one evidence-backed observation
// into the wrapped contradiction.Engine ahead of AnalyzeContradictions.
// Not itself lifecycle-gated: a case may accumulate observations
// across several evidence submissions before contradictions are
// formally analyzed.
func (f *Facade) SubmitContradictionObservation(rec evidence.Record, claimKey, value string, reliability float64, tick uint64) error {
	return f.contradictionEngine.Submit(rec, claimKey, value, reliability, tick)
}

// AnalyzeCausation runs causation.Explain over hs (built and populated
// by the caller with hypotheses and their supporting/contradicting
// evidence — this package never invents a hypothesis) and advances the
// case to CAUSATION_ANALYZED. Blueprint §32 step 9.
func (f *Facade) AnalyzeCausation(tick uint64, hs *causation.HypothesisSet) (causation.Explanation, error) {
	explanation, err := causation.Explain(hs, f.evDeps)
	if err != nil {
		return causation.Explanation{}, err
	}
	f.causationExplanation = &explanation
	if err := f.c.Advance(insurancecase.StateCausationAnalyzed, tick); err != nil {
		return causation.Explanation{}, err
	}
	return explanation, nil
}

// ComputeQuantum runs quantum.Compute (the §20 formula) and, when
// discrepancyID is non-empty, quantum.DetectDiscrepancy (the §21
// unresolved-gap formula) over claimed/salvage/deductible, then
// advances the case to QUANTUM_ANALYZED. Blueprint §32 step 10.
func (f *Facade) ComputeQuantum(tick uint64, in quantum.ComputeInput, discrepancyID string, claimed, salvage, deductible quantum.EvidenceBackedAmount) (quantum.Calculation, *quantum.QuantumDiscrepancy, error) {
	calc, err := quantum.Compute(in)
	if err != nil {
		return quantum.Calculation{}, nil, err
	}
	f.quantumCalc = &calc

	var discrepancy *quantum.QuantumDiscrepancy
	if discrepancyID != "" {
		d, err := quantum.DetectDiscrepancy(discrepancyID, claimed, calc.GrossLoss, salvage, deductible, in.Currency)
		if err != nil {
			return quantum.Calculation{}, nil, err
		}
		d.CalculationID = calc.CalculationID
		f.quantumDiscrepancy = &d
		discrepancy = &d
	}

	if err := f.c.Advance(insurancecase.StateQuantumAnalyzed, tick); err != nil {
		return quantum.Calculation{}, nil, err
	}
	return calc, discrepancy, nil
}

// AnalyzeCoverage runs coverage.Analyze and advances the case to
// COVERAGE_ISSUES_IDENTIFIED. Blueprint §32 step 11.
func (f *Facade) AnalyzeCoverage(tick uint64, in coverage.Input) (coverage.CoverageAnalysis, error) {
	analysis, err := coverage.Analyze(in)
	if err != nil {
		return coverage.CoverageAnalysis{}, err
	}
	f.coverageAnalysis = &analysis
	if err := f.c.Advance(insurancecase.StateCoverageIssuesIdentified, tick); err != nil {
		return coverage.CoverageAnalysis{}, err
	}
	return analysis, nil
}

// AnalyzeRecovery registers every recovery target and advances the
// case to RECOVERY_ANALYZED. Blueprint §32 step 12.
func (f *Facade) AnalyzeRecovery(tick uint64, targets []recovery.Target) ([]recovery.Target, error) {
	for _, t := range targets {
		if err := f.recoveryRegistry.Register(t); err != nil {
			return nil, fmt.Errorf("api: registering recovery target %s: %w", t.TargetID, err)
		}
	}
	if err := f.c.Advance(insurancecase.StateRecoveryAnalyzed, tick); err != nil {
		return nil, err
	}
	return f.recoveryRegistry.All(), nil
}

// MarkHumanReview advances the case to HUMAN_REVIEW. Blueprint §32
// step 13. It performs no analysis of its own — GenerateDossier is
// what computes whether review is actually required.
func (f *Facade) MarkHumanReview(tick uint64) error {
	return f.c.Advance(insurancecase.StateHumanReview, tick)
}

// ComputeGapAssessment runs gap.DetectGapsFromRegistry /
// SufficiencyFromReport for issue against def and records it in the
// Facade's running gap.Assessment. Not lifecycle-gated: gap assessment
// can be (re-)computed at any point as evidence accumulates.
func (f *Facade) ComputeGapAssessment(issue string, def claim.TypeDefinition, contradicted bool) (gap.Sufficiency, error) {
	if f.gapAssessment == nil {
		if f.activeClaimID == "" {
			return gap.Sufficiency{}, ErrClaimNotYetSet
		}
		f.gapAssessment = gap.NewAssessment(f.activeClaimID)
	}
	report, err := gap.DetectGapsFromRegistry(def, f.c.Evidence, f.c.CaseID)
	if err != nil {
		return gap.Sufficiency{}, err
	}
	suff, err := gap.SufficiencyFromReport(issue, report, contradicted)
	if err != nil {
		return gap.Sufficiency{}, err
	}
	if err := f.gapAssessment.Overwrite(suff); err != nil {
		return gap.Sufficiency{}, err
	}
	return suff, nil
}

// RegisterMitigationAction adds a to the case's mitigation timeline
// and recomputes the running Impact. Not lifecycle-gated: mitigation
// actions can occur throughout a case's real-world timeline, not only
// at one fixed stage.
func (f *Facade) RegisterMitigationAction(a mitigation.Action) (mitigation.Impact, error) {
	if err := f.mitigationTimeline.Add(a); err != nil {
		return mitigation.Impact{}, err
	}
	impact, err := f.mitigationTimeline.Impact()
	if err != nil {
		return mitigation.Impact{}, err
	}
	f.mitigationImpact = &impact
	return impact, nil
}

// RegisterDeadlineRule registers r and recomputes every rule's
// Deadline against triggerTick. Not lifecycle-gated: deadline rules
// may be extracted from different documents at different points in a
// case's life.
func (f *Facade) RegisterDeadlineRule(r deadline.Rule, triggerTick uint64) ([]deadline.Deadline, error) {
	if err := f.deadlineRegistry.Register(r); err != nil {
		return nil, err
	}
	deadlines, err := f.deadlineRegistry.ComputeAll(triggerTick)
	if err != nil {
		return nil, err
	}
	f.deadlines = deadlines
	return deadlines, nil
}

// GenerateDossier assembles the terminal Dossier (pkg/insurance/dossier,
// aggregating everything every prior step computed), produces its
// tamper-evident verification.Manifest, and advances the case to
// DOSSIER_GENERATED. Blueprint §32 step 14.
func (f *Facade) GenerateDossier(tick uint64) (*dossier.Dossier, verification.Manifest, error) {
	in := dossier.Inputs{
		ClaimID:              f.activeClaimID,
		TimelineConflicts:    f.timelineConflicts,
		Contradictions:       f.contradictions,
		GapAssessment:        f.gapAssessment,
		CausationExplanation: f.causationExplanation,
		QuantumCalculation:   f.quantumCalc,
		QuantumDiscrepancy:   f.quantumDiscrepancy,
		MitigationImpact:     f.mitigationImpact,
		RecoveryTargets:      f.recoveryRegistry.All(),
		Deadlines:            f.deadlines,
		CoverageAnalysis:     f.coverageAnalysis,
	}
	d, err := dossier.Generate(f.c, in)
	if err != nil {
		return nil, verification.Manifest{}, err
	}

	manifest, err := verification.Generate(f.c.Evidence, f.c.CaseID, tick)
	if err != nil {
		return nil, verification.Manifest{}, err
	}

	if err := f.c.Advance(insurancecase.StateDossierGenerated, tick); err != nil {
		return nil, verification.Manifest{}, err
	}
	return d, manifest, nil
}

// Close moves a case out of DOSSIER_GENERATED into a terminal state —
// CASE_CLOSED or OPEN_ISSUES, per the case package's own rule that
// terminal transitions are only reachable from DOSSIER_GENERATED.
// Blueprint §32 step 15. Which terminal state applies is the caller's
// decision (informed by, but never computed from, the dossier's
// HumanReviewRequired flag) — this package renders no such verdict
// itself.
func (f *Facade) Close(tick uint64, to insurancecase.State) error {
	return f.c.Advance(to, tick)
}
