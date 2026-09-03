package adversarial

import (
	"strings"
	"testing"

	"veriqo/pkg/assurance"
	"veriqo/pkg/casefabric"
	"veriqo/pkg/fref"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/timestamp"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// Adversarial cases against the fabrics built this round.
//
// Each is written as an attempt, not as a check. The question is never
// "does the happy path work" — the E2E test answers that — but "what
// does someone do who wants a conclusion they have not earned, and what
// stops them?"

// --- Attacking the Proof Object --------------------------------------

// TestForgedProofObjectCannotFoundAFinding is the most direct attack:
// construct the object by hand with every verdict field set favourably,
// and try to get a finding out of it.
func TestForgedProofObjectCannotFoundAFinding(t *testing.T) {
	forged := proof.Object{
		Proposition: proof.Proposition{ID: "P-1", Statement: "whatever we need to be true"},
		Stance:      proof.Support,
		Sufficiency: proof.Sufficient,
		// A hash the attacker simply wrote down.
		CanonicalHash: "sha256:looks-convincing",
	}
	if _, err := proof.NewFinding(forged, 10); err == nil {
		t.Fatal("a hand-written proof object with a made-up hash must not found a finding")
	}
	if err := proof.VerifyHash(forged); err == nil {
		t.Fatal("a made-up canonical hash must not verify")
	}
}

// TestSealingLaunderrsNothing: the attacker seals the forged object
// properly, hoping Seal will simply bless whatever it is given.
func TestSealingLaundersNothing(t *testing.T) {
	forged := proof.Object{
		Proposition: proof.Proposition{ID: "P-1", Statement: "whatever we need"},
		Stance:      proof.Support,
		Sufficiency: proof.Sufficient,
	}
	if _, err := proof.Seal(forged); err == nil {
		t.Fatal("Seal must refuse an object with no scope, evidence, authority or limitations")
	}
}

// TestQualificationCannotBeUpgradedAfterSealing: seal honestly, then
// edit the qualification to something stronger.
func TestQualificationCannotBeUpgradedAfterSealing(t *testing.T) {
	o := insufficientObject(t)
	o.Qualification, _ = state.New("CL-1", state.Qualified, "policy-v1", "upgraded", nil, 10)
	if err := proof.VerifyHash(o); err == nil {
		t.Fatal("editing the qualification after sealing must break the canonical hash")
	}
}

// TestReSealingAnEditedObjectStillFailsSufficiency closes the obvious
// follow-up: re-seal after editing, so the hash matches again.
//
// It does not help. Sufficiency is derived from the underlying facts,
// and a qualification state alone does not manufacture an assessed
// source set.
func TestReSealingAnEditedObjectStillFailsSufficiency(t *testing.T) {
	o := insufficientObject(t)
	o.Qualification, _ = state.New("CL-1", state.Qualified, "policy-v1", "upgraded", nil, 10)
	resealed, err := proof.Seal(o)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if resealed.Sufficiency == proof.Sufficient {
		t.Fatal("re-sealing must not manufacture sufficiency the facts do not support")
	}
	if _, err := proof.NewFinding(resealed, 10); err == nil {
		t.Fatal("the re-sealed object still must not found a finding")
	}
}

// TestSelfAuthorizationIsRefused: the pipeline tries to adopt its own
// conclusion, which would leave no authority boundary at all.
func TestSelfAuthorizationIsRefused(t *testing.T) {
	o := sufficientObject(t)
	f, err := proof.NewFinding(o, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	if _, err := proof.Authorize(f, o, o.Provenance.GeneratedBy, "service", "policy-v1", "self", 30); err == nil {
		t.Fatal("the party that generated the proof object must not authorize it")
	}
}

// TestFindingLaunderedThroughAnotherObjectIsRefused: authorize a real
// finding against a different, more favourable proof object.
func TestFindingLaunderedThroughAnotherObjectIsRefused(t *testing.T) {
	weak := insufficientObject(t)
	sealed, _ := proof.Seal(weak)
	strong := sufficientObject(t)

	f, err := proof.NewFinding(strong, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	if _, err := proof.Authorize(f, sealed, "partner-1", "partner", "policy-v1", "adopted", 30); err == nil {
		t.Fatal("a finding must not be authorized against a proof object it did not come from")
	}
}

// --- Attacking the adjudication boundary -----------------------------

// TestAdjudicationByCreativeNaming tries the obvious evasions.
func TestAdjudicationByCreativeNaming(t *testing.T) {
	o := sufficientObject(t)
	f, _ := proof.NewFinding(o, 20)
	a, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted", 30)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	for _, attempt := range []string{"WINNER", "Prevailing_Party", "  verdict  ", "AwArD"} {
		if _, err := proof.Decide(a, "refer", "", map[string]string{attempt: "claimant"}, 40); err == nil {
			t.Fatalf("attribute %q must be refused as adjudication", attempt)
		}
	}
}

// TestAdjudicationSmuggledIntoACaseOutcome tries the other end.
func TestAdjudicationSmuggledIntoACaseOutcome(t *testing.T) {
	for _, o := range []casefabric.Outcome{
		{Disposition: "referred", Summary: "the claimant is the winner"},
		{Disposition: "closed", Summary: "respondent found liable_party"},
		{Disposition: "judgment", Summary: "delivered"},
	} {
		if err := o.Validate(); err == nil {
			t.Fatalf("outcome %+v adjudicates and must be refused", o)
		}
	}
}

// --- Attacking the case fabric ---------------------------------------

// TestDomainCannotBypassTheFabricWithAnInventedState is Law 3 under
// attack: a domain that wants a phase it did not register.
func TestDomainCannotBypassTheFabricWithAnInventedState(t *testing.T) {
	c := scopedCase(t)
	for _, invented := range []string{"SETTLED_QUIETLY", "RESOLVED", "resolved", ""} {
		if err := c.SyncDomainState(invented, "attacker", 9); err == nil {
			t.Fatalf("domain state %q is unmapped and must be refused", invented)
		}
	}
	if c.Phase() != casefabric.PhaseScoped {
		t.Fatalf("a refused sync must not move the phase, got %s", c.Phase())
	}
}

// TestCaseCannotResolveOverAnUnprovenMaterialClaim is the shortcut a
// deadline creates: resolve now, prove later.
func TestCaseCannotResolveOverAnUnprovenMaterialClaim(t *testing.T) {
	c := scopedCase(t)
	mustNil(t, c.AddEvidence([]casefabric.EvidenceRef{
		{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNil(t, c.AddHypothesis(casefabric.Hypothesis{ID: "H-1", Description: "rival", Tested: true}, "a", 4))
	mustNil(t, c.RegisterClaim(casefabric.Claim{ID: "CL-1", Material: true,
		Proposition: proof.Proposition{ID: "P-1", Statement: "contaminated before loading"}}, "a", 5))
	mustNil(t, c.BeginQualification("a", 6))

	if _, err := c.Resolve(casefabric.ResolutionGate{}, "evidence_package_delivered", "", "a", 7); err == nil {
		t.Fatal("a case must not resolve over an unproven material claim")
	}
}

// TestUntestedRivalCannotBeSkipped: the rival hypothesis nobody wanted
// to test.
func TestUntestedRivalCannotBeSkipped(t *testing.T) {
	c := scopedCase(t)
	mustNil(t, c.AddEvidence([]casefabric.EvidenceRef{
		{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNil(t, c.AddHypothesis(casefabric.Hypothesis{ID: "H-1", Description: "inconvenient rival"}, "a", 4))
	mustNil(t, c.BeginQualification("a", 5))
	if _, err := c.Resolve(casefabric.ResolutionGate{}, "closed", "", "a", 6); err == nil {
		t.Fatal("a case must not resolve with an untested rival hypothesis")
	}
	if got := c.UntestedHypotheses(); len(got) != 1 {
		t.Fatalf("the untested rival must be nameable, got %v", got)
	}
}

// TestEditedTimelineIsCaughtBeforeItReachesTheLedger: the attacker
// edits the case history, then mirrors it into the canonical audit
// store.
func TestEditedTimelineIsCaughtBeforeItReachesTheLedger(t *testing.T) {
	c := scopedCase(t)
	store := audit.NewAuditStore()
	if _, _, err := casefabric.Mirror(store, c, "policy-v1", nil); err != nil {
		t.Fatalf("an honest case should mirror: %v", err)
	}
	before := len(store.Snapshot())
	if before == 0 {
		t.Fatal("the mirror produced no records")
	}
	// The timeline itself is unexported and returned only as a copy, so
	// the attack has to go through the accessor — and does not stick.
	c.Timeline()[0].Description = "a different history"
	if err := c.VerifyTimeline(); err != nil {
		t.Fatalf("editing a returned copy must not corrupt the case: %v", err)
	}
}

// --- Attacking the temporal distinction ------------------------------

// TestSelfRunTSACannotManufactureIndependentAttestation is the attack
// the timestamp package exists for: run your own TSA and cite it.
func TestSelfRunTSACannotManufactureIndependentAttestation(t *testing.T) {
	const us = "veriqo-operations-ltd"
	token := &timestamp.ExternalAttestation{
		Digest: "d", Authority: timestamp.TSA{Name: "VERIQO Trusted Time", OperatorID: us},
		SerialNumber: "0x1", GenTimeUnix: 1_700_000_000, Token: []byte{0x30},
	}
	att, err := timestamp.Assess("d", nil, token, []string{us, "claimant-ltd"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if att.Kind() == timestamp.IndependentAttestation {
		t.Fatal("a TSA VERIQO operates must not yield an independent attestation")
	}
	if _, ok := att.ProvesExistenceBefore(); ok {
		t.Fatal("a self-run TSA must not prove existence before a time")
	}
	if err := timestamp.RequireIndependent(att); err == nil {
		t.Fatal("RequireIndependent must refuse it")
	}
	if !strings.Contains(timestamp.Describe(att), "not counted") {
		t.Fatalf("the downgrade must be disclosed, got %q", timestamp.Describe(att))
	}
}

// TestChainAttestationCannotBeDescribedAsProvingTime is the reporting
// attack: quote the system's own sentence and hope it overstates.
func TestChainAttestationCannotBeDescribedAsProvingTime(t *testing.T) {
	entry, err := timestamp.NewChainAttestation("d", 0, "", "veriqo-operations-ltd")
	if err != nil {
		t.Fatalf("NewChainAttestation: %v", err)
	}
	att, _ := timestamp.Assess("d", &entry, nil, nil)
	desc := strings.ToLower(timestamp.Describe(att))
	if strings.Contains(desc, "existed before") {
		t.Fatalf("a chain description must never claim existence before a time: %q", desc)
	}
}

// --- Attacking the execution contract --------------------------------

// TestConclusionWithoutTrustAssessmentIsRefused: run the forward
// pipeline but skip TRUST, which is the stage that costs time.
func TestConclusionWithoutTrustAssessmentIsRefused(t *testing.T) {
	e, err := fref.NewExecution(fref.Forward, "P-1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	for _, s := range []fref.Stage{fref.StageObservation, fref.StageEvidence,
		fref.StageKnowledge, fref.StageReasoning} {
		b, _ := fref.BindingFor(s)
		mustNil(t, e.Complete(s, b.Package, 1, "h", ""))
	}
	if err := e.Complete(fref.StageFinding, "veriqo/pkg/proof", 2, "h", ""); err == nil {
		t.Fatal("a finding must not be reached without passing through TRUST")
	}
}

// TestClosureCatchesEvidenceNoObligationRequired is the subtle one: the
// finding is real, the pipeline ran in order, but it rests on something
// no proof obligation ever called for.
func TestClosureCatchesEvidenceNoObligationRequired(t *testing.T) {
	fwd := completeRun(t, fref.Forward, "P-1")
	rev := completeRun(t, fref.Reverse, "P-1")

	c, err := fref.Close(fwd, rev, []string{"EV-1", "EV-CONVENIENT"}, []string{"EV-1"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.Holds {
		t.Fatal("closure must fail when the finding rests on unrequired evidence")
	}
	if len(c.Unrequired) != 1 || c.Unrequired[0] != "EV-CONVENIENT" {
		t.Fatalf("the unrequired evidence must be named, got %v", c.Unrequired)
	}
}

// --- Attacking the assurance report ----------------------------------

// TestQualifiedCannotBeClaimedWithoutAnExternalReference: the reporting
// attack — mark a control QUALIFIED because it feels finished.
func TestQualifiedCannotBeClaimedWithoutAnExternalReference(t *testing.T) {
	tr := assurance.Trace{
		Article: 1, Control: "a control someone wants to call finished",
		Code: true, CodeRef: "pkg/x", Called: true, CalledRef: "pkg/y",
		Test: true, TestRef: "TestX", Evidence: true, EvidenceRef: "events",
		Replay: true, ReplayRef: "pkg/replay",
		Qualification: true, QualificationRef: "docs/self-assessment.md",
		// ExternalProof asserted with no reference.
		ExternalProof: true,
	}
	if _, err := assurance.Assess(tr); err == nil {
		t.Fatal("an external proof asserted with no reference must be refused")
	}
}

// TestProductionEvidenceForAnUncalledControlIsAContradiction is the
// impossible-chain attack: claim the strong links, skip the weak one.
func TestProductionEvidenceForAnUncalledControlIsAContradiction(t *testing.T) {
	tr := assurance.Trace{
		Article: 1, Control: "c", Code: true, CodeRef: "pkg/x",
		Evidence: true, EvidenceRef: "events", Replay: true, ReplayRef: "pkg/replay",
	}
	if _, err := assurance.Assess(tr); err == nil {
		t.Fatal("production evidence for a control nothing calls must be refused as a contradiction")
	}
}

// TestTheAxisReportCannotBeReducedToAScore is the executive attack:
// "just give me a number".
func TestTheAxisReportCannotBeReducedToAScore(t *testing.T) {
	r := assurance.AxisReport()
	if strings.Contains(r, "%") {
		t.Fatal("the axis report must not express readiness as a percentage")
	}
	rows, _ := assurance.Assemble()
	if strings.Contains(assurance.Summarize(rows).Headline(), "%") {
		t.Fatal("the matrix headline must not be a percentage")
	}
	// And the honest position must survive: nothing is externally
	// validated, and the report must not imply otherwise.
	for _, s := range assurance.Capabilities() {
		if s.Assurance >= assurance.ExternallyValidated {
			t.Fatalf("capability %q claims external validation with no external party", s.Capability)
		}
	}
}

// --- fixtures --------------------------------------------------------

func baseObject(t *testing.T) proof.Object {
	t.Helper()
	claim := reverseproof.Claim{ID: "CL-1", Description: "contamination before loading",
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "pre-load contamination"}}}
	reqs := []reverseproof.Requirement{{ID: "R-1", ConditionID: "cond-1", Description: "pre-load sample",
		ExpectedIfTrue: "contaminant present", ContradictsIfShows: "clean sample",
		Status: reverseproof.Obtained, DiagnosticValue: 0.9}}
	alts := []reverseproof.AlternativeHypothesis{{ID: "A-1", Description: "in transit", Tested: true}}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("reverseproof.Build: %v", err)
	}
	q, err := state.New("CL-1", state.Supported, "policy-v1", "qualified", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	return proof.Object{
		Proposition:  proof.Proposition{ID: "P-1", Statement: "the cargo was contaminated before loading"},
		Scope:        proof.Scope{CaseID: "CASE-1", Matter: "cargo damage"},
		Jurisdiction: proof.Jurisdiction{Code: "SG"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet:  []proof.EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "EV-1-v1", SHA256: "abc", SourceID: "lab-a"}},
		Quality:      proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof: rs, ReverseProofGap: reverseproof.Analyze(rs, map[string]bool{"cond-1": true}),
		Trust:         proof.TrustAssessment{Assessed: true, EffectiveSourceCount: 2},
		Qualification: q,
		Authority:     proof.Authority{AuthorityID: "analyst-1", Role: "analyst", PolicyVersion: "policy-v1"},
		Limitations:   []string{"covers the sampled parcel only"},
		Provenance:    proof.Provenance{GeneratedBy: "pipeline-1", GeneratedAtTick: 10, PipelineVersion: "fref-v1"},
	}
}

func sufficientObject(t *testing.T) proof.Object {
	t.Helper()
	o, err := proof.Seal(baseObject(t))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if o.Sufficiency != proof.Sufficient {
		t.Fatalf("fixture should be sufficient: %s", proof.InsufficiencyReason(o))
	}
	return o
}

func insufficientObject(t *testing.T) proof.Object {
	t.Helper()
	o := baseObject(t)
	o.Trust.Assessed = false
	sealed, err := proof.Seal(o)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed
}

func scopedCase(t *testing.T) *casefabric.Case {
	t.Helper()
	c, err := casefabric.Open(casefabric.Identity{
		CaseID: "CASE-1", TenantID: "tenant-a", Domain: casefabric.DomainInsurance}, "analyst-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := c.SetScope(proof.Scope{CaseID: "CASE-1", Matter: "cargo damage"},
		proof.Jurisdiction{Code: "SG"}, proof.TimeWindow{FromTick: 1, ToTick: 500},
		casefabric.Mission{Statement: "establish the cause", Intent: "quantify", SetBy: "a", SetAtTick: 2},
		"analyst-1", 2); err != nil {
		t.Fatalf("SetScope: %v", err)
	}
	return c
}

func completeRun(t *testing.T, d fref.Direction, subject string) *fref.Execution {
	t.Helper()
	e, err := fref.NewExecution(d, subject)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	for i, s := range fref.Order(d) {
		b, _ := fref.BindingFor(s)
		if err := e.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			t.Fatalf("Complete(%s): %v", s, err)
		}
	}
	return e
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
