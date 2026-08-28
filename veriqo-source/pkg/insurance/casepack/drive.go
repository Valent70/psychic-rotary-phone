package casepack

import (
	"fmt"

	"veriqo/pkg/insurance/api"
	"veriqo/pkg/insurance/canonical"
	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/contradiction"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/deadline"
	"veriqo/pkg/insurance/dossier"
	"veriqo/pkg/insurance/mitigation"
	"veriqo/pkg/insurance/obligation"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/preservation"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/insurance/verification"
	"veriqo/pkg/lineage"
)

// Drive runs one synthetic case through the REAL insurance facade, the
// real preservation order, and the real §54–§57 verification gates.
//
// This is the Final Design §20 "C5 — Replay Engine" decision applied to
// fixtures, and it is the most important structural property of this
// package: a synthetic case and a real case go through the SAME engine.
// There is no fixture-only code path anywhere below — every call is a
// call a production case makes.
//
// What Drive deliberately does NOT do: assert an outcome. It runs the
// case and returns what the domain produced. The tests decide whether
// that matches the case's declared ExpectedOutputs, so a case that
// starts producing something different fails loudly rather than being
// quietly re-blessed by its own driver.
type Result struct {
	CaseID CaseID

	Facade  *api.Facade
	Binding *canonical.Binding
	Order   *preservation.Order

	Built BuiltEvidence

	PolicyVersion    policy.Version
	TimelineConflict []timeline.Conflict
	Contradictions   []contradiction.ContradictionRecord
	Causation        causation.Explanation
	Quantum          quantum.Calculation
	QuantumInput     quantum.ComputeInput
	Discrepancy      *quantum.QuantumDiscrepancy
	Coverage         coverage.CoverageAnalysis
	MitigationImpact mitigation.Impact
	NoticeAssessment obligation.Assessment
	Dossier          *dossier.Dossier
	Manifest         verification.Manifest

	// Gates is the aggregated §54–§57 report for this case.
	Gates verification.GateReport
}

// driveTicks are the lifecycle ticks each case advances through. Fixed
// and shared so every case's audit trail is comparable.
type driveTicks struct {
	parties, policy, claim, ingest, verify, timeline, mapPolicy uint64
	contradictions, causation, quantum, coverage, recovery      uint64
	humanReview, dossier                                        uint64
}

func defaultTicks() driveTicks {
	return driveTicks{
		parties: 10, policy: 20, claim: 30, ingest: 40, verify: 50,
		timeline: 60, mapPolicy: 70, contradictions: 90, causation: 100,
		quantum: 110, coverage: 120, recovery: 130, humanReview: 140, dossier: 150,
	}
}

// Drive executes the full path for one case.
func Drive(c Case, ledger *lineage.Ledger) (*Result, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	ticks := defaultTicks()
	res := &Result{CaseID: c.ID}

	// --- Case + canonical binding ------------------------------------
	claimTypes := claim.NewTypeRegistry()
	for _, def := range append(claim.DefaultTypes(), PackClaimTypes()...) {
		if err := claimTypes.Register(def); err != nil {
			return nil, fmt.Errorf("%s: registering claim type: %w", c.ID, err)
		}
	}
	f, err := api.New(string(c.ID), 0, claimTypes)
	if err != nil {
		return nil, fmt.Errorf("%s: api.New: %w", c.ID, err)
	}
	res.Facade = f

	if ledger != nil {
		b, err := canonical.New(ledger, string(c.ID))
		if err != nil {
			return nil, fmt.Errorf("%s: canonical.New: %w", c.ID, err)
		}
		if err := f.BindCanonical(b); err != nil {
			return nil, fmt.Errorf("%s: BindCanonical: %w", c.ID, err)
		}
		res.Binding = b
	}

	// --- Step 1: parties ---------------------------------------------
	specs := make([]api.PartySpec, 0, len(c.Parties))
	for _, p := range c.Parties {
		specs = append(specs, api.PartySpec{ID: p.ID, Name: p.Name, Roles: p.Roles, EntityRef: p.EntityRef})
	}
	if err := f.IdentifyParties(ticks.parties, specs); err != nil {
		return nil, fmt.Errorf("%s: IdentifyParties: %w", c.ID, err)
	}

	// --- Step 2: policy ----------------------------------------------
	// Every case carries a two-version policy history whose ORIGINAL is
	// in force at the incident and whose endorsement is not, so every
	// case exercises the §6 "never the latest version" rule.
	policyID := "POL-" + string(c.ID)
	hist, err := policy.NewHistory(policyID)
	if err != nil {
		return nil, fmt.Errorf("%s: policy.NewHistory: %w", c.ID, err)
	}
	original := policy.Version{
		PolicyID: policyID, VersionID: policyID + "-V1", PolicyNumber: "PN-" + string(c.ID),
		Insurer: "Meridian Marine Mutual", Insured: string(c.Parties[0].ID),
		Kind: policy.KindOriginal, EffectiveFrom: 1, EffectiveTo: 5000,
		Clauses: []policy.Clause{
			{ClauseID: "cl. 2", DocumentID: policyID, SourceHash: "synthetic:" + policyID + ":cl2", TextSpan: "policy period", Version: "V1"},
			{ClauseID: "cl. 4", DocumentID: policyID, SourceHash: "synthetic:" + policyID + ":cl4", TextSpan: "insured perils", Version: "V1"},
			{ClauseID: "cl. 7.2", DocumentID: policyID, SourceHash: "synthetic:" + policyID + ":cl72", TextSpan: "notice of loss", Version: "V1"},
			{ClauseID: "cl. 8", DocumentID: policyID, SourceHash: "synthetic:" + policyID + ":cl8", TextSpan: "exclusions", Version: "V1"},
			{ClauseID: "cl. 14", DocumentID: policyID, SourceHash: "synthetic:" + policyID + ":cl14", TextSpan: "quantum and adjustment", Version: "V1"},
		},
	}
	endorsement := policy.Version{
		PolicyID: policyID, VersionID: policyID + "-V2", PolicyNumber: "PN-" + string(c.ID),
		Insurer: "Meridian Marine Mutual", Insured: string(c.Parties[0].ID),
		Kind: policy.KindEndorsement, Supersedes: policyID + "-V1", EffectiveFrom: 5000,
	}
	if err := hist.Add(original); err != nil {
		return nil, fmt.Errorf("%s: adding original policy: %w", c.ID, err)
	}
	if err := hist.Add(endorsement); err != nil {
		return nil, fmt.Errorf("%s: adding endorsement: %w", c.ID, err)
	}
	if err := f.RegisterPolicy(ticks.policy, policyID, hist); err != nil {
		return nil, fmt.Errorf("%s: RegisterPolicy: %w", c.ID, err)
	}

	// --- Step 3: claim -----------------------------------------------
	cl, err := claim.New("CLM-"+string(c.ID), string(c.ID), claimTypeFor(c.ID), c.Parties[0].ID, claimTypes)
	if err != nil {
		return nil, fmt.Errorf("%s: claim.New: %w", c.ID, err)
	}
	if err := f.RegisterClaim(ticks.claim, cl); err != nil {
		return nil, fmt.Errorf("%s: RegisterClaim: %w", c.ID, err)
	}

	// --- Step 4: evidence --------------------------------------------
	built, err := c.BuildAllEvidence()
	if err != nil {
		return nil, err
	}
	res.Built = built
	if err := f.IngestEvidence(ticks.ingest, built.Records, nil); err != nil {
		return nil, fmt.Errorf("%s: IngestEvidence: %w", c.ID, err)
	}

	// --- Preservation: the §19/§20 order over this case's evidence ---
	order, err := preservation.New(
		"PRES-"+string(c.ID), string(c.ID), preservation.TriggerPotentialClaim,
		"all evidence submitted under "+string(c.ID),
		"claims operations custodian",
		c.EvidenceTypes(), ticks.ingest, ticks.dossier+1000, "claims-officer",
	)
	if err != nil {
		return nil, fmt.Errorf("%s: preservation.New: %w", c.ID, err)
	}
	for _, rec := range built.Records {
		if err := order.Preserve(rec, "claims operations custodian", ticks.ingest); err != nil {
			return nil, fmt.Errorf("%s: preserving %s: %w", c.ID, rec.EvidenceID(), err)
		}
	}
	res.Order = order

	// --- Step 5: evidence verification --------------------------------
	// Statuses are what a reviewer has already determined about each
	// record; the facade only records them. Nothing here judges
	// authenticity, matching evidence.Registry.SetStatus's own rule.
	if err := f.VerifyEvidence(ticks.verify, verificationStatuses(built), nil); err != nil {
		return nil, fmt.Errorf("%s: VerifyEvidence: %w", c.ID, err)
	}

	// --- Step 6: timeline ---------------------------------------------
	events, err := eventsFor(c, built)
	if err != nil {
		return nil, err
	}
	conflicts, err := f.ReconstructTimeline(ticks.timeline, events)
	if err != nil {
		return nil, fmt.Errorf("%s: ReconstructTimeline: %w", c.ID, err)
	}
	res.TimelineConflict = conflicts

	// --- Step 7: policy mapping (incident inside the ORIGINAL window) --
	version, err := f.MapPolicy(ticks.mapPolicy, 2000, policyID)
	if err != nil {
		return nil, fmt.Errorf("%s: MapPolicy: %w", c.ID, err)
	}
	res.PolicyVersion = version

	// --- Step 8: contradictions ----------------------------------------
	claimKey, obs := contradictionObservationsFor(c, built)
	for _, o := range obs {
		if err := f.SubmitContradictionObservation(o.rec, claimKey, o.value, o.reliability, ticks.contradictions-5); err != nil {
			return nil, fmt.Errorf("%s: SubmitContradictionObservation: %w", c.ID, err)
		}
	}
	recs, err := f.AnalyzeContradictions(ticks.contradictions, claimKey,
		contradiction.PairDocumentSurvey, "sources disagree on "+claimKey, 10_000)
	if err != nil {
		return nil, fmt.Errorf("%s: AnalyzeContradictions: %w", c.ID, err)
	}
	res.Contradictions = recs

	// --- Step 9: causation ----------------------------------------------
	hs, err := hypothesesFor(c, built)
	if err != nil {
		return nil, err
	}
	explanation, err := f.AnalyzeCausation(ticks.causation, hs)
	if err != nil {
		return nil, fmt.Errorf("%s: AnalyzeCausation: %w", c.ID, err)
	}
	res.Causation = explanation

	// --- Step 10: quantum -------------------------------------------------
	in, claimed := quantumInputFor(c, built)
	res.QuantumInput = in
	calc, disc, err := f.ComputeQuantum(ticks.quantum, in, "QD-"+string(c.ID),
		claimed, in.Salvage, in.Deductible)
	if err != nil {
		return nil, fmt.Errorf("%s: ComputeQuantum: %w", c.ID, err)
	}
	res.Quantum = calc
	res.Discrepancy = disc

	// --- Step 11: coverage -------------------------------------------------
	typeDef, _ := claimTypes.Get(cl.Type)
	noticeTick, deadlineTick := noticeTicksFor(c.ID)
	cov, err := f.AnalyzeCoverage(ticks.coverage, coverage.Input{
		Claim:              claimWithVersion(cl, version.VersionID),
		PolicyVersion:      version,
		Evidence:           built.Records,
		TypeDef:            &typeDef,
		IncidentTick:       2000,
		NoticeTick:         noticeTick,
		NoticeDeadlineTick: deadlineTick,
		PerilDocTypes:      []string{"survey_report", "incident_report"},
		NoticeDocTypes:     []string{"notice_letter"},
		QuantumDocTypes:    []string{"invoice", "survey_report"},
		PeriodClauseID:     "cl. 2",
		PerilClauseID:      "cl. 4",
		NoticeClauseID:     "cl. 7.2",
		QuantumClauseID:    "cl. 14",
	})
	if err != nil {
		return nil, fmt.Errorf("%s: AnalyzeCoverage: %w", c.ID, err)
	}
	res.Coverage = cov

	// --- The I-03 notice assessment, alongside coverage ------------------
	res.NoticeAssessment, err = noticeAssessmentFor(c, built, noticeTick, deadlineTick)
	if err != nil {
		return nil, fmt.Errorf("%s: notice assessment: %w", c.ID, err)
	}

	// --- Step 12: recovery -------------------------------------------------
	if _, err := f.AnalyzeRecovery(ticks.recovery, nil); err != nil {
		return nil, fmt.Errorf("%s: AnalyzeRecovery: %w", c.ID, err)
	}

	// --- Non-lifecycle-gated: gap assessment and a deadline rule ---------
	if _, err := f.ComputeGapAssessment("required_evidence", typeDef, len(recs) > 0); err != nil {
		return nil, fmt.Errorf("%s: ComputeGapAssessment: %w", c.ID, err)
	}
	// A mitigation action, so I-06 is exercised end to end rather than
	// only unit-tested. Every case records one: the actor is a real
	// party on the case and the supporting evidence is a real
	// content-addressed record from its own set.
	mitAction, mitErr := mitigationActionFor(c, built)
	if mitErr != nil {
		return nil, mitErr
	}
	impact, err := f.RegisterMitigationAction(mitAction)
	if err != nil {
		return nil, fmt.Errorf("%s: RegisterMitigationAction: %w", c.ID, err)
	}
	res.MitigationImpact = impact

	dr, err := deadline.New("DR-"+string(c.ID), deadline.SourceTypePolicy, "cl. 7.2",
		policyID, "V1", "DAMAGE_DISCOVERY", 24, deadline.CalendarRuleCalendarDays, "UTC")
	if err != nil {
		return nil, fmt.Errorf("%s: deadline.New: %w", c.ID, err)
	}
	if _, err := f.RegisterDeadlineRule(dr, 2000); err != nil {
		return nil, fmt.Errorf("%s: RegisterDeadlineRule: %w", c.ID, err)
	}

	// --- Step 13/14: human review and dossier ------------------------------
	if err := f.MarkHumanReview(ticks.humanReview); err != nil {
		return nil, fmt.Errorf("%s: MarkHumanReview: %w", c.ID, err)
	}
	d, manifest, err := f.GenerateDossier(ticks.dossier)
	if err != nil {
		return nil, fmt.Errorf("%s: GenerateDossier: %w", c.ID, err)
	}
	res.Dossier = d
	res.Manifest = manifest

	// --- The four §54–§57 gates --------------------------------------------
	res.Gates = verification.GateReport{
		CaseID:                 string(c.ID),
		EvidenceManifest:       manifest,
		CoverageTraceability:   verification.VerifyCoverageTraceability(cov, version, f.Case().Evidence),
		QuantumReproducibility: verification.VerifyQuantumReproducibility(calc, in),
		Preservation: verification.VerifyPreservation(string(c.ID),
			[]*preservation.Order{order}, f.Case().Evidence, map[string]string{}),
		HumanReview: verification.VerifyHumanReview(d, authorizationsFor(c.ID, d)),
	}

	return res, nil
}

// Close moves the case to its terminal state. Which terminal state
// applies is the CALLER's decision, informed by (never computed from)
// the dossier's HumanReviewRequired flag — the same rule
// api.Facade.Close documents.
func (r *Result) Close(tick uint64) error {
	to := insurancecase.StateCaseClosed
	if r.Dossier != nil && r.Dossier.HumanReviewRequired {
		to = insurancecase.StateOpenIssues
	}
	return r.Facade.Close(tick, to)
}
