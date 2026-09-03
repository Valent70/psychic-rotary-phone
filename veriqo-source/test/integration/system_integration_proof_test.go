package integration

import (
	"os"
	"strings"
	"testing"

	"veriqo/pkg/casefabric"
	"veriqo/pkg/caseproofgraph"
	"veriqo/pkg/contract/event"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/fref"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/timestamp"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/observability"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// VERIQO SYSTEM INTEGRATION PROOF
//
// The claim under test:
//
//	The five VERIQO fabrics are not merely co-existing components;
//	they constitute one executable, governed evidence-to-proof
//	operating system.
//
// The proof is the chain, run for every domain:
//
//	CASE -> FORWARD -> EVIDENCE -> INTELLIGENCE -> TRUST -> EQF
//	 -> PROOF -> REVERSE -> FINDING -> AUTHORIZED DECISION
//	 -> LEDGER -> REPLAY
//
// with four properties held throughout:
//
//	no bypass              every stage refuses its predecessor's absence
//	no duplicate engine    each stage runs in the package the contract binds it to
//	no synthetic promotion nothing upgrades its own status
//	fail-closed            the negative cases refuse rather than degrade
//
// What this does NOT prove, stated here so no reader has to infer it:
// the evidence in every run is a FIXTURE. The chain executes and
// records; it has not been run on real rights-aware commercial data.
// That is the LIVE_DATA blocker and it remains BLOCKED_EXTERNAL.
// TestTheIntegrationProofDisclosesItsFixtureBoundary asserts that this
// limitation is stated wherever the proof is reported.

// domainCase is one domain's fixture, expressed in that domain's own
// declared semantics.
type domainCase struct {
	domain      string
	caseID      string
	matter      string
	proposition string
	// domainState is a real state from the domain's registered
	// projection, used to prove the case syncs onto the canonical spine.
	domainState string
	disposition string
	sourceA     string
	sourceB     string
}

func domainCases(t *testing.T) []domainCase {
	t.Helper()
	return []domainCase{
		{casefabric.DomainMaritime, "SIP-MAR-1", "route deviation investigation",
			"the vessel deviated from its declared route between 03:00 and 07:00 UTC on 12 March",
			"EVIDENCE_SECURED", "findings_issued", "ais-aggregator-a", "sar-provider-b"},
		{casefabric.DomainCommodity, "SIP-COM-1", "cargo quality dispute",
			"the cargo was off-specification before loading",
			"SAMPLES_COLLECTED", "quality_determined", "lab-a", "surveyor-b"},
		{casefabric.DomainSupplyChain, "SIP-SUP-1", "disruption attribution",
			"the disruption originated at the named tier-2 supplier",
			"TRACE_COLLECTED", "origin_established", "customs-authority-a", "auditor-b"},
		{casefabric.DomainInsurance, "SIP-INS-1", "cargo damage claim",
			"the loss falls within the policy's insured perils",
			"EVIDENCE_EXCHANGED", "evidence_package_delivered", "adjuster-a", "surveyor-b"},
		{casefabric.DomainTradeFinance, "SIP-TRD-1", "documentary presentation",
			"the presentation is compliant with the credit",
			"DOCUMENTS_EXAMINED", "determination_issued", "issuing-bank-a", "inspection-body-b"},
		{casefabric.DomainDispute, "SIP-DIS-1", "evidence support for arbitration",
			"the evidence package is complete for the issues framed",
			"DISCLOSURE_EXCHANGED", "evidence_package_delivered", "expert-a", "contemporaneous-record-b"},
	}
}

// TestSystemIntegrationProofForEveryDomain is the mandate's centrepiece.
//
// It runs the whole chain for all six domains. Written as one test per
// domain via subtests: each is a complete, independent execution, and a
// failure names the domain that broke.
func TestSystemIntegrationProofForEveryDomain(t *testing.T) {
	for _, dc := range domainCases(t) {
		t.Run(dc.domain, func(t *testing.T) {
			runChain(t, dc)
		})
	}
}

func runChain(t *testing.T, dc domainCase) {
	t.Helper()
	const veriqoEntity = "veriqo-operations-ltd"
	store := audit.NewAuditStore()

	// --- The domain must be on the fabric, with declared semantics ---
	sem, ok := casefabric.SemanticsFor(dc.domain)
	if !ok {
		t.Fatalf("domain %q declares no semantics", dc.domain)
	}
	if err := sem.Validate(); err != nil {
		t.Fatalf("domain %q semantics: %v", dc.domain, err)
	}
	if len(sem.PartyMediatedClasses()) == 0 {
		t.Fatalf("domain %q claims all its evidence is independently acquired", dc.domain)
	}

	// --- TECP: pin evidence, assess its temporal standing ------------
	digest := "sha256:" + dc.caseID + "-primary"
	chainEntry, err := timestamp.NewChainAttestation(digest, 0, "", veriqoEntity)
	if err != nil {
		t.Fatalf("chain attestation: %v", err)
	}
	att, err := timestamp.Assess(digest, &chainEntry, nil, []string{veriqoEntity, "claimant-ltd"})
	if err != nil {
		t.Fatalf("timestamp.Assess: %v", err)
	}
	if _, proven := att.ProvesExistenceBefore(); proven {
		t.Fatal("no TSA relationship exists, so nothing may prove existence before a time")
	}

	// --- FREF forward, in order --------------------------------------
	fwd, err := fref.NewExecution(fref.Forward, dc.proposition)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	completeStages(t, fwd, fref.StageObservation, fref.StageEvidence, fref.StageKnowledge, fref.StageReasoning)

	// NO BYPASS: a finding cannot be reached before trust is assessed.
	if err := fwd.Complete(fref.StageFinding, "veriqo/pkg/proof", 5, "h", ""); err == nil {
		t.Fatal("a finding must not be reachable before TRUST")
	}
	completeStages(t, fwd, fref.StageTrust)

	// --- EQF: independence, gated absence, qualification -------------
	sources := []independence.Source{
		unrelatedSource(dc.sourceA), unrelatedSource(dc.sourceB),
	}
	effective, err := independence.EffectiveSourceCount(sources)
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if effective != 2 {
		t.Fatalf("two unrelated sources are two effective sources, got %d", effective)
	}
	absence, err := observability.Evaluate(observability.Assessment{
		Subject: "the record the domain could not obtain", SourceID: "unavailable-source",
		Conditions: observability.AllConditionsMet(), Material: true, Tick: 9,
	})
	if err != nil {
		t.Fatalf("observability.Evaluate: %v", err)
	}
	if !absence.State.CarriesEvidentialWeight() {
		t.Fatalf("a fully gated absence should carry weight: %s", absence.Reason)
	}

	rs, gap := domainReverseProof(t, dc)
	q, err := state.New(dc.caseID, state.Supported, "policy-v1", "two independent sources agree", nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	// --- PROOF: seal --------------------------------------------------
	o, err := proof.Seal(proof.Object{
		Proposition: proof.Proposition{ID: dc.caseID + "-P", Statement: dc.proposition,
			SubjectType: string(sem.ObjectTypes[0]), SubjectID: dc.caseID + "-SUBJ"},
		Scope:        proof.Scope{CaseID: dc.caseID, Matter: dc.matter},
		Jurisdiction: proof.Jurisdiction{Code: "SG", Forum: "SIAC", GoverningLaw: "English law"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet: []proof.EvidenceRef{
			{EvidenceID: dc.caseID + "-E1", EvidenceVersionID: dc.caseID + "-EV1", SHA256: digest, SourceID: dc.sourceA},
			{EvidenceID: dc.caseID + "-E2", EvidenceVersionID: dc.caseID + "-EV2", SHA256: digest + "-2", SourceID: dc.sourceB},
		},
		Quality:         proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof:    rs,
		ReverseProofGap: gap,
		MissingEvidence: []proof.MissingEvidence{
			{ConditionID: "cond-1", Description: "a record the domain could not obtain",
				Obtainable: false, Reason: "searched and observed absent; retention expired"},
		},
		Trust: proof.TrustAssessment{Assessed: true, EffectiveSourceCount: effective,
			Verdicts: map[string]independence.Verdict{dc.sourceA + ":" + dc.sourceB: independence.Independent}},
		Qualification: q,
		Authority:     proof.Authority{AuthorityID: "analyst-1", Role: "senior-analyst", PolicyVersion: "policy-v1"},
		Disclosure:    proof.DisclosureState{Procedural: 2, Content: 3, Privilege: "NOT_CLAIMED"},
		Limitations: []string{
			"the evidence in this run is a fixture, not real commercial data",
			"temporal ordering is chain-attested, not independently attested",
		},
		Provenance: proof.Provenance{GeneratedBy: "fref-pipeline", GeneratedAtTick: 10,
			PipelineVersion: "fref-v1", InputHashes: []string{digest}},
		ReplayReference: "REPLAY-" + dc.caseID,
	})
	if err != nil {
		t.Fatalf("proof.Seal: %v", err)
	}
	if o.Stance != proof.Support || o.Sufficiency != proof.Sufficient {
		t.Fatalf("expected SUPPORT/SUFFICIENT, got %s/%s: %s",
			o.Stance, o.Sufficiency, proof.InsufficiencyReason(o))
	}

	// NO SYNTHETIC PROMOTION: internally qualified is not proof.
	level := proof.LevelOf(o, nil, []string{veriqoEntity})
	if level != proof.LevelQualified {
		t.Fatalf("expected PROOF_QUALIFIED, got %s", level)
	}
	if level.IsProof() {
		t.Fatal("no internal work reaches PROOF_EXTERNALLY_ATTESTED")
	}

	// --- CRF: carry it in a case --------------------------------------
	c := buildDomainCase(t, dc, o)

	// The domain's own state projected onto the canonical spine during
	// the build. An invented state must not.
	if err := c.SyncDomainState("INVENTED_STATE", "analyst-1", 9); err == nil {
		t.Fatalf("domain %q accepted an unmapped state", dc.domain)
	}

	// --- FREF reverse, and closure — BEFORE qualification -------------
	//
	// This ordering is the sequencing audit's central correction. The
	// reverse direction answers "what evidence would actually be needed
	// to justify this?", and a finding already final when the question
	// is asked has been rubber-stamped, not gated. Running it here makes
	// it a constitutional gate; running it after resolution, as an
	// earlier version of this test did, made it a retrospective audit.
	rev, err := fref.NewExecution(fref.Reverse, dc.proposition)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	completeStages(t, rev, fref.Order(fref.Reverse)...)
	if err := rev.RequireComplete(); err != nil {
		t.Fatalf("the reverse run must reach NEXT_BEST_EVIDENCE: %v", err)
	}
	var evidenceIDs []string
	for _, e := range o.EvidenceSet {
		evidenceIDs = append(evidenceIDs, e.EvidenceVersionID)
	}
	closure, err := fref.Close(fwd, rev, evidenceIDs, evidenceIDs)
	if err != nil {
		t.Fatalf("fref.Close: %v", err)
	}
	if !closure.Holds {
		t.Fatalf("domain %q: the two directions must close: %s", dc.domain, closure.Explain())
	}
	if err := c.RecordReverseClosure(dc.proposition, closure.Holds, "analyst-1", 9); err != nil {
		t.Fatalf("RecordReverseClosure: %v", err)
	}

	if err := c.BeginQualification("analyst-1", 10); err != nil {
		t.Fatalf("BeginQualification: %v", err)
	}
	if err := c.AttachProof(dc.caseID+"-CL", o, "analyst-1", 11); err != nil {
		t.Fatalf("AttachProof: %v", err)
	}

	// --- FINDING -> AUTHORIZED DECISION -------------------------------
	f, err := proof.NewFinding(o, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	if err := c.RecordFinding(f.Hash(), o.CanonicalHash, "analyst-1", 20); err != nil {
		t.Fatalf("RecordFinding: %v", err)
	}
	// NO BYPASS: the pipeline may not adopt its own conclusion.
	if _, err := proof.Authorize(f, o, o.Provenance.GeneratedBy, "service", "policy-v1", "self", 30); err == nil {
		t.Fatalf("domain %q allowed self-authorization", dc.domain)
	}
	a, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted on review", 30)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	// FAIL-CLOSED: the decision may not adjudicate, in any domain.
	if _, err := proof.Decide(a, "refer", "", map[string]string{"prevailing_party": "claimant"}, 40); err == nil {
		t.Fatalf("domain %q allowed an adjudicatory decision", dc.domain)
	}
	d, err := proof.Decide(a, dc.disposition, "the evidence package is complete",
		map[string]string{"forum": "SIAC"}, 40)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := c.RecordAuthorizedDecision(d, "partner-1", 40); err != nil {
		t.Fatalf("RecordAuthorizedDecision: %v", err)
	}
	completeStages(t, fwd, fref.StageFinding, fref.StageDecision)

	if err := fwd.RequireComplete(); err != nil {
		t.Fatalf("the forward run must reach DECISION: %v", err)
	}
	// NO DUPLICATE ENGINE: every stage ran where the contract binds it.
	if err := fwd.VerifyAgainstContract(); err != nil {
		t.Fatalf("domain %q drifted from the execution contract: %v", dc.domain, err)
	}

	// --- CRF: resolve, in the domain's own outcome vocabulary --------
	if !containsString(sem.OutcomeVocabulary, dc.disposition) {
		t.Fatalf("domain %q does not declare the outcome %q", dc.domain, dc.disposition)
	}
	gate := casefabric.ResolutionGate{
		Decision: d, ReverseClosureHolds: closure.Holds,
		ClosureSubject:     dc.proposition,
		ClosureExplanation: closure.Explain(),
	}
	outcome, err := c.Resolve(gate, dc.disposition, "established on the qualified evidence", "partner-1", 41)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := c.AddOutcomeLimitations(o.Limitations); err != nil {
		t.Fatalf("AddOutcomeLimitations: %v", err)
	}
	if len(outcome.EstablishedClaimIDs) != 1 {
		t.Fatalf("expected one established claim, got %+v", outcome)
	}

	// --- The Case Proof Graph -----------------------------------------
	g, err := caseproofgraph.Build(c, map[string]proof.Object{dc.caseID + "-CL": o},
		map[string]proof.Finding{dc.caseID + "-CL": f}, 50)
	if err != nil {
		t.Fatalf("caseproofgraph.Build: %v", err)
	}
	if err := caseproofgraph.AddDecision(g, d); err != nil {
		t.Fatalf("AddDecision: %v", err)
	}
	if err := caseproofgraph.VerifyGraph(g); err != nil {
		t.Fatalf("the case proof graph must verify: %v", err)
	}

	// Rights-aware: a limited recipient gets a real subgraph.
	p, err := caseproofgraph.Project(g, access.Grant{
		EvidenceVersionID: dc.caseID + "-EV1", RecipientID: "observer-1", RecipientRole: "observer",
		Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
		Rights: []access.Right{access.View}, PolicyVersion: "policy-v1",
		Privilege: access.PrivilegeNotClaimed,
	}, "observer-1", 60)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(p.Graph.NodesOfKind(caseproofgraph.NodeEvidence)) != 0 {
		t.Fatalf("domain %q leaked evidence nodes to a C2 recipient", dc.domain)
	}
	if err := caseproofgraph.VerifyGraph(p.Graph); err != nil {
		t.Fatalf("the projection must verify on its own: %v", err)
	}

	// --- LEDGER -------------------------------------------------------
	records, chain, err := casefabric.Mirror(store, c, "policy-v1", map[string]proof.Object{dc.caseID + "-CL": o})
	if err != nil {
		t.Fatalf("Mirror: %v", err)
	}
	if len(store.Snapshot()) != len(records) {
		t.Fatalf("expected everything in the one ledger, got %d records", len(store.Snapshot()))
	}
	// The emitted stream must obey the constitutional sequence and skip
	// no gate. This is the check that would have caught the defect the
	// sequencing audit found.
	var actions []string
	for _, r := range store.Snapshot() {
		actions = append(actions, r.Action)
	}
	if v := fref.VerifyEventOrder(actions); len(v) > 0 {
		t.Fatalf("domain %q emitted an out-of-order ledger: %s", dc.domain, v[0])
	}
	if g := fref.VerifyEventGates(actions); len(g) > 0 {
		t.Fatalf("domain %q skipped a constitutional gate: %s", dc.domain, g[0])
	}

	// --- REPLAY: five independent re-verifications ---------------------
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if err := event.VerifyChain(chain.Events()); err != nil {
		t.Fatalf("event chain: %v", err)
	}
	if err := c.VerifyTimeline(); err != nil {
		t.Fatalf("case timeline: %v", err)
	}
	if err := proof.VerifyHash(o); err != nil {
		t.Fatalf("proof object: %v", err)
	}
	if err := timestamp.VerifyChain([]timestamp.ChainAttestation{chainEntry}); err != nil {
		t.Fatalf("temporal chain: %v", err)
	}

	// --- Lineage and the honest limits --------------------------------
	dh, ah, fh, ph := d.Lineage()
	if dh == "" || ah == "" || fh == "" || ph != o.CanonicalHash {
		t.Fatalf("domain %q: the decision must trace to the proof object", dc.domain)
	}
	out, _ := c.Outcome()
	if !containsSubstring(out.Limitations, "fixture") {
		t.Fatalf("domain %q: the fixture limitation must survive into the outcome, got %v",
			dc.domain, out.Limitations)
	}
	if o.ExternalQualification.Status.Satisfied() {
		t.Fatalf("domain %q claims external qualification with no external party", dc.domain)
	}
}

// TestTheIntegrationProofDisclosesItsFixtureBoundary is the honesty
// gate on the proof itself.
//
// The chain above proves the fabrics compose. It proves nothing about
// real commercial data, and a reader who took it for that would be
// misled — so every run's proof object must carry the limitation, and
// the LIVE_DATA blocker must still be open.
func TestTheIntegrationProofDisclosesItsFixtureBoundary(t *testing.T) {
	src := readSelf(t, "system_integration_proof_test.go")
	if !strings.Contains(src, "LIVE_DATA blocker and it remains BLOCKED_EXTERNAL") {
		t.Fatal("the integration proof must state that its evidence is a fixture and name the blocker that remains")
	}
}

// TestNoDomainHasItsOwnChain is the anti-duplication property at the
// system level: all six domains ran through the same packages.
//
// If a domain needed its own execution path, the fref contract check in
// runChain would have reported drift for it. This test makes the claim
// explicit rather than leaving it implied by six passing subtests.
func TestNoDomainHasItsOwnChain(t *testing.T) {
	seen := map[string]bool{}
	for _, dc := range domainCases(t) {
		if seen[dc.domain] {
			t.Fatalf("domain %q appears twice", dc.domain)
		}
		seen[dc.domain] = true
	}
	if len(seen) != len(casefabric.RegisteredDomains()) {
		t.Fatalf("the integration proof covers %d domains, %d are registered",
			len(seen), len(casefabric.RegisteredDomains()))
	}
	// Every stage of both directions binds to exactly one package.
	for _, d := range []fref.Direction{fref.Forward, fref.Reverse} {
		for _, s := range fref.Order(d) {
			b, ok := fref.BindingFor(s)
			if !ok || b.Package == "" {
				t.Fatalf("stage %s has no bound package", s)
			}
		}
	}
}

// --- helpers ----------------------------------------------------------

func completeStages(t *testing.T, e *fref.Execution, stages ...fref.Stage) {
	t.Helper()
	for i, s := range stages {
		b, _ := fref.BindingFor(s)
		if err := e.Complete(s, b.Package, uint64(i+1), "h-"+string(s), ""); err != nil {
			t.Fatalf("Complete(%s): %v", s, err)
		}
	}
}

func unrelatedSource(id string) independence.Source {
	return independence.Source{ID: id, Attributes: map[independence.Dimension]string{
		independence.RootOrigin: id, independence.OrganizationalControl: id + "-holdings",
		independence.ProviderPipeline: id + "-pipeline", independence.Collector: id,
		independence.AcquisitionPath: "direct",
	}}
}

func domainReverseProof(t *testing.T, dc domainCase) (reverseproof.RequirementSet, reverseproof.Gap) {
	t.Helper()
	claim := reverseproof.Claim{ID: dc.caseID + "-CL", Description: dc.proposition,
		Conditions: []reverseproof.Condition{{ID: "cond-1", Description: "the material condition"}}}
	reqs := []reverseproof.Requirement{
		{ID: "R-1", ConditionID: "cond-1", Description: "the primary evidence the domain requires",
			ExpectedIfTrue: "consistent with the proposition", ContradictsIfShows: "inconsistent with the proposition",
			Status: reverseproof.Obtained, DiagnosticValue: 0.9},
		{ID: "R-2", ConditionID: "cond-1", Description: "a corroborating record the domain could not obtain",
			ExpectedIfTrue: "corroborates", ContradictsIfShows: "contradicts",
			Status: reverseproof.Unobtainable, DiagnosticValue: 0.4},
	}
	alts := []reverseproof.AlternativeHypothesis{
		{ID: "A-1", Description: "the leading rival explanation for this domain", Tested: true},
	}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("reverseproof.Build: %v", err)
	}
	return rs, reverseproof.Analyze(rs, map[string]bool{"cond-1": true})
}

func buildDomainCase(t *testing.T, dc domainCase, o proof.Object) *casefabric.Case {
	t.Helper()
	c, err := casefabric.Open(casefabric.Identity{
		CaseID: dc.caseID, TenantID: "tenant-a", Domain: dc.domain,
	}, "analyst-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	steps := []func() error{
		func() error {
			return c.SetScope(o.Scope, o.Jurisdiction, o.TimeWindow, casefabric.Mission{
				Statement: "establish whether " + dc.proposition, Intent: "furnish evidence",
				SetBy: "analyst-1", SetAtTick: 2}, "analyst-1", 2)
		},
		func() error { return c.AddEvidence(o.EvidenceSet, "analyst-1", 3) },
		// The domain reports its own state here, while gathering. Its
		// vocabulary must project onto the canonical phase; a state that
		// maps to nothing is refused, which is checked in runChain.
		func() error { return c.SyncDomainState(dc.domainState, "analyst-1", 4) },
		func() error {
			return c.AddHypothesis(casefabric.Hypothesis{
				ID: "H-1", Description: "the leading rival explanation"}, "analyst-1", 4)
		},
		func() error {
			return c.RegisterClaim(casefabric.Claim{
				ID: dc.caseID + "-CL", Material: true, Proposition: o.Proposition}, "analyst-1", 5)
		},
		func() error { return c.TestHypothesis("H-1", "excluded on the qualified evidence", "analyst-1", 6) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			t.Fatalf("case step for %s: %v", dc.domain, err)
		}
	}
	return c
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func containsSubstring(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// readSelf reads this test file, so the honesty gate checks the text
// that actually ships rather than a copy of it in an assertion.
func readSelf(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
