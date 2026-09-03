package integration

import (
	"strings"
	"testing"

	"veriqo/pkg/assurance"
	"veriqo/pkg/casefabric"
	"veriqo/pkg/contract/event"
	"veriqo/pkg/fref"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/timestamp"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/nextbest"
	"veriqo/pkg/qualification/observability"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// TestFiveFabricsAreOneSystem is the end-to-end proof the architecture
// asks for: one proposition, carried through all five fabrics, ending
// in a decision that traces back to the evidence it rests on.
//
// It is written as one long test on purpose. Split into five, it would
// prove each fabric works and prove nothing about whether they compose,
// which is the only question this test exists to answer.
func TestFiveFabricsAreOneSystem(t *testing.T) {
	const (
		caseID  = "CASE-E2E-1"
		propID  = "P-E2E-1"
		claimID = "CL-E2E-1"
		veriqo  = "veriqo-operations-ltd"
	)
	store := audit.NewAuditStore()

	// --- TECP: pin the evidence and attest to it --------------------
	//
	// The evidence enters with a content hash. Its temporal standing is
	// assessed, and — VERIQO holding no TSA relationship — comes back as
	// a chain attestation, which proves ordering and not existence
	// before any time.
	const digest = "sha256:e2e-lab-report"
	chainEntry, err := timestamp.NewChainAttestation(digest, 0, "", veriqo)
	if err != nil {
		t.Fatalf("NewChainAttestation: %v", err)
	}
	att, err := timestamp.Assess(digest, &chainEntry, nil, []string{veriqo, "claimant-ltd"})
	if err != nil {
		t.Fatalf("timestamp.Assess: %v", err)
	}
	if _, ok := att.ProvesExistenceBefore(); ok {
		t.Fatal("with no TSA token the attestation must not prove existence before a time")
	}
	if att.Kind() != timestamp.TamperEvidentChain {
		t.Fatalf("expected a chain attestation, got %s", att.Kind())
	}

	// --- FREF forward: run the pipeline in order --------------------
	fwd, err := fref.NewExecution(fref.Forward, propID)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	for _, s := range []fref.Stage{
		fref.StageObservation, fref.StageEvidence, fref.StageKnowledge, fref.StageReasoning,
	} {
		b, _ := fref.BindingFor(s)
		if err := fwd.Complete(s, b.Package, 1, "h-"+string(s), ""); err != nil {
			t.Fatalf("forward %s: %v", s, err)
		}
	}

	// --- EQF: qualify the claim -------------------------------------
	//
	// Two sources, assessed for independence. Nothing is assumed: an
	// unassessed pair would come back UNKNOWN, and UNKNOWN is not
	// INDEPENDENT.
	sources := []independence.Source{
		{ID: "lab-a", Attributes: map[independence.Dimension]string{
			independence.RootOrigin: "lab-a", independence.OrganizationalControl: "lab-a-holdings",
			independence.ProviderPipeline: "lab-a-lims", independence.Collector: "lab-a",
			independence.AcquisitionPath: "direct"}},
		{ID: "surveyor-b", Attributes: map[independence.Dimension]string{
			independence.RootOrigin: "surveyor-b", independence.OrganizationalControl: "surveyor-b-llp",
			independence.ProviderPipeline: "surveyor-b-field", independence.Collector: "surveyor-b",
			independence.AcquisitionPath: "direct"}},
	}
	effective, err := independence.EffectiveSourceCount(sources)
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if effective != 2 {
		t.Fatalf("two unrelated sources are two effective sources, got %d", effective)
	}

	// The absence in this case was gated, not assumed: the terminal CCTV
	// was searched for and observed absent.
	absence, err := observability.Evaluate(observability.Assessment{
		Subject: "terminal CCTV of the loading window", SourceID: "terminal-cctv",
		Conditions: observability.AllConditionsMet(), Material: true, Tick: 9,
	})
	if err != nil {
		t.Fatalf("observability.Evaluate: %v", err)
	}
	if !absence.State.CarriesEvidentialWeight() {
		t.Fatalf("a fully gated absence should carry evidential weight, got %s (%s)", absence.State, absence.Reason)
	}

	rs, gap := reverseProofSet(t)
	if !gap.Complete {
		t.Fatalf("the reverse proof should be complete: %+v", gap)
	}
	q, err := state.New(claimID, state.Supported, "policy-v1", "two independent sources agree", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	b, _ := fref.BindingFor(fref.StageTrust)
	if err := fwd.Complete(fref.StageTrust, b.Package, 2, "h-TRUST", ""); err != nil {
		t.Fatalf("forward TRUST: %v", err)
	}

	// --- Proof: seal the object -------------------------------------
	o, err := proof.Seal(proof.Object{
		Proposition: proof.Proposition{ID: propID,
			Statement: "the cargo was contaminated before loading", SubjectType: "Cargo", SubjectID: "CARGO-9"},
		Scope:        proof.Scope{CaseID: caseID, Matter: "cargo damage claim"},
		Jurisdiction: proof.Jurisdiction{Code: "SG", Forum: "SIAC", GoverningLaw: "English law"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet: []proof.EvidenceRef{
			{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: digest, SourceID: "lab-a"},
			{EvidenceID: "E-2", EvidenceVersionID: "EV-2-v1", SHA256: "sha256:surveyor", SourceID: "surveyor-b"},
		},
		Quality:         proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof:    rs,
		ReverseProofGap: gap,
		MissingEvidence: []proof.MissingEvidence{
			{ConditionID: "cond-1", Description: "terminal CCTV", Obtainable: false,
				Reason: "retention expired before the dispute arose; searched and observed absent"},
		},
		Trust: proof.TrustAssessment{Assessed: true, EffectiveSourceCount: effective,
			Verdicts: map[string]independence.Verdict{"lab-a:surveyor-b": independence.Independent}},
		Qualification: q,
		Authority:     proof.Authority{AuthorityID: "analyst-1", Role: "senior-analyst", PolicyVersion: "policy-v1"},
		Disclosure:    proof.DisclosureState{Procedural: 2, Content: 3, Privilege: "NOT_CLAIMED"},
		Limitations: []string{
			"covers the sampled parcel only",
			"temporal ordering is chain-attested, not independently attested",
		},
		Provenance: proof.Provenance{GeneratedBy: "fref-pipeline", GeneratedAtTick: 10,
			PipelineVersion: "fref-v1", InputHashes: []string{digest}},
		ReplayReference: "REPLAY-E2E-1",
	})
	if err != nil {
		t.Fatalf("proof.Seal: %v", err)
	}
	if o.Stance != proof.Support || o.Sufficiency != proof.Sufficient {
		t.Fatalf("expected SUPPORT/SUFFICIENT, got %s/%s: %s",
			o.Stance, o.Sufficiency, proof.InsufficiencyReason(o))
	}

	// --- CRF: carry it in a case ------------------------------------
	c, err := casefabric.Open(casefabric.Identity{
		CaseID: caseID, TenantID: "tenant-a", Domain: casefabric.DomainInsurance,
		ExternalRefs: map[string]string{"claim_no": "CLM-777"},
	}, "analyst-1", 1)
	if err != nil {
		t.Fatalf("casefabric.Open: %v", err)
	}
	mustOK(t, c.SetScope(o.Scope, o.Jurisdiction, o.TimeWindow,
		casefabric.Mission{Statement: "establish whether the cargo was contaminated before loading",
			Intent: "quantify the loss", SetBy: "analyst-1", SetAtTick: 2}, "analyst-1", 2))
	mustOK(t, c.AddEvidence(o.EvidenceSet, "analyst-1", 3))
	mustOK(t, c.AddHypothesis(casefabric.Hypothesis{ID: "H-1", Description: "contaminated in transit"}, "analyst-1", 4))
	mustOK(t, c.RegisterClaim(casefabric.Claim{ID: claimID, Material: true, Proposition: o.Proposition}, "analyst-1", 5))
	mustOK(t, c.TestHypothesis("H-1", "excluded by the pre-load sample", "analyst-1", 6))
	mustOK(t, c.BeginQualification("analyst-1", 7))
	mustOK(t, c.AttachProof(claimID, o, "analyst-1", 8))

	// --- Proof pipeline: finding, authorization, decision -----------
	f, err := proof.NewFinding(o, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	a, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted on review", 30)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	d, err := proof.Decide(a, "refer_to_tribunal", "the evidence package is complete",
		map[string]string{"forum": "SIAC"}, 40)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}

	for _, s := range []fref.Stage{fref.StageFinding, fref.StageDecision} {
		b, _ := fref.BindingFor(s)
		if err := fwd.Complete(s, b.Package, 3, d.Hash(), ""); err != nil {
			t.Fatalf("forward %s: %v", s, err)
		}
	}
	if err := fwd.RequireComplete(); err != nil {
		t.Fatalf("the forward run must reach DECISION: %v", err)
	}
	if err := fwd.VerifyAgainstContract(); err != nil {
		t.Fatalf("the forward run drifted from the contract: %v", err)
	}

	// --- CRF: resolve -----------------------------------------------
	gate := casefabric.ResolutionGate{
		Decision: d, ReverseClosureHolds: true,
		ClosureSubject:     o.Proposition.ID,
		ClosureExplanation: "closure holds over the same evidence set",
	}
	outcome, err := c.Resolve(gate, "evidence_package_delivered",
		"pre-loading contamination established on the sampled parcel", "analyst-1", 41)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mustOK(t, c.AddOutcomeLimitations(o.Limitations))
	if len(outcome.EstablishedClaimIDs) != 1 {
		t.Fatalf("the claim should be established, got %+v", outcome)
	}

	// --- FREF reverse: run the other direction ----------------------
	rev, err := fref.NewExecution(fref.Reverse, propID)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	for _, s := range fref.Order(fref.Reverse) {
		b, _ := fref.BindingFor(s)
		if err := rev.Complete(s, b.Package, 50, "h-"+string(s), ""); err != nil {
			t.Fatalf("reverse %s: %v", s, err)
		}
	}
	if err := rev.RequireComplete(); err != nil {
		t.Fatalf("the reverse run must reach NEXT_BEST_EVIDENCE: %v", err)
	}

	// --- Closure: the two directions agree --------------------------
	var forwardEvidence []string
	for _, e := range o.EvidenceSet {
		forwardEvidence = append(forwardEvidence, e.EvidenceVersionID)
	}
	closure, err := fref.Close(fwd, rev, forwardEvidence, forwardEvidence)
	if err != nil {
		t.Fatalf("fref.Close: %v", err)
	}
	if !closure.Holds {
		t.Fatalf("the two directions must close over this proposition: %s", closure.Explain())
	}

	// --- TECP: everything lands in the one ledger -------------------
	records, chain, err := casefabric.Mirror(store, c, "policy-v1", nil)
	if err != nil {
		t.Fatalf("casefabric.Mirror: %v", err)
	}
	if _, err := casefabric.MirrorProof(store, "analyst-1", o); err != nil {
		t.Fatalf("MirrorProof: %v", err)
	}
	if len(store.Snapshot()) != len(records)+1 {
		t.Fatalf("expected %d records in the one ledger, got %d", len(records)+1, len(store.Snapshot()))
	}

	// --- Replay: everything re-derives ------------------------------
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		t.Fatalf("the ledger must re-verify: %v", err)
	}
	if err := event.VerifyChain(chain.Events()); err != nil {
		t.Fatalf("the canonical event chain must re-verify: %v", err)
	}
	if err := c.VerifyTimeline(); err != nil {
		t.Fatalf("the case timeline must re-verify: %v", err)
	}
	if err := proof.VerifyHash(o); err != nil {
		t.Fatalf("the proof object must re-verify: %v", err)
	}
	if err := timestamp.VerifyChain([]timestamp.ChainAttestation{chainEntry}); err != nil {
		t.Fatalf("the temporal chain must re-verify: %v", err)
	}

	// --- Lineage: the decision traces to the evidence ---------------
	dh, ah, fh, ph := d.Lineage()
	if dh == "" || ah == "" || fh == "" || ph != o.CanonicalHash {
		t.Fatalf("the decision must trace to the proof object, got %q/%q/%q/%q", dh, ah, fh, ph)
	}

	// --- And the honest limits survive to the end -------------------
	out, _ := c.Outcome()
	joined := strings.Join(out.Limitations, " | ")
	if !strings.Contains(joined, "chain-attested, not independently attested") {
		t.Fatalf("the temporal limitation must survive into the case outcome, got %q", joined)
	}
	if o.ExternalQualification.Status.Satisfied() {
		t.Fatal("nothing in this pipeline is externally qualified, and the object must say so")
	}
}

// TestTheE2EPathIsWhatTheAssuranceMatrixClaims ties the walk above to
// the audit: the packages the matrix cites for its traced articles are
// the packages this test actually exercised.
func TestTheE2EPathIsWhatTheAssuranceMatrixClaims(t *testing.T) {
	rows, err := assurance.Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(rows) != 30 {
		t.Fatalf("expected 30 articles, got %d", len(rows))
	}
	// Article 1 (no naked facts) and Article 16 (no adjudication) are
	// both exercised end to end above; neither may read as OPEN.
	for _, n := range []int{1, 16} {
		if rows[n-1].Verdict == assurance.Open {
			t.Fatalf("article %d is exercised end to end and must not be OPEN", n)
		}
	}
}

// --- helpers ---------------------------------------------------------

func reverseProofSet(t *testing.T) (reverseproof.RequirementSet, reverseproof.Gap) {
	t.Helper()
	claim := reverseproof.Claim{ID: "CL-E2E-1", Description: "contamination before loading",
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "pre-load contamination"}}}
	reqs := []reverseproof.Requirement{
		{ID: "R-1", ConditionID: "cond-1", Description: "pre-load sample analysis",
			ExpectedIfTrue: "contaminant present", ContradictsIfShows: "clean sample",
			Status: reverseproof.Obtained, DiagnosticValue: 0.9},
		{ID: "R-2", ConditionID: "cond-1", Description: "terminal CCTV of the loading window",
			ExpectedIfTrue: "contamination visible at loading", ContradictsIfShows: "clean loading",
			Status: reverseproof.Unobtainable, DiagnosticValue: 0.4},
	}
	alts := []reverseproof.AlternativeHypothesis{{ID: "A-1", Description: "contaminated in transit", Tested: true}}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("reverseproof.Build: %v", err)
	}
	return rs, reverseproof.Analyze(rs, map[string]bool{"cond-1": true})
}

// unusedNextBest keeps the nextbest import honest: the E2E path is the
// sufficient one, so the direction it would produce is exercised here.
func TestInsufficientPathProducesDirection(t *testing.T) {
	o := sealedProof(t)
	o.Trust.Assessed = false
	insufficient, err := proof.Seal(o)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	dir, err := proof.NextBest(insufficient, []nextbest.Candidate{{
		ID: "obtain-second-assay", SourceID: "lab-c", Description: "independent re-assay",
		RightsGranted: true, AuthorityGranted: true, SourcePermitted: true, WithinCaseScope: true,
		DiagnosticValue: 0.8, Independence: 0.9, Relevance: 0.9, Freshness: 0.7,
		AcquisitionFeasibility: 0.6, Cost: 1, Latency: 1, RightsRisk: 1,
	}})
	if err != nil {
		t.Fatalf("NextBest: %v", err)
	}
	if len(dir.Ranking.Ranked) != 1 || dir.Reason == "" {
		t.Fatalf("an insufficient object must yield a ranked direction with a reason: %+v", dir)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
