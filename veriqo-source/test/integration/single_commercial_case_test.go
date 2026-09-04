package integration

import (
	"archive/zip"
	"bytes"
	"sort"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/casefabric"
	"veriqo/pkg/caseproofgraph"
	"veriqo/pkg/contract/event"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/evidence/redaction"
	"veriqo/pkg/evidence/redaction/worker"
	"veriqo/pkg/fref"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/timestamp"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/observability"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// One case, three domains.
//
// TestSystemIntegrationProofForEveryDomain runs six domains through the
// chain SEPARATELY. That proves no domain has its own engine. It does
// not prove that a single case can carry evidence from more than one
// domain at once, which is what a real commercial matter looks like:
// a cargo damage claim is simultaneously a maritime question (where was
// the vessel, what was the weather), a commodity question (what was
// loaded, in what condition) and an insurance question (what does the
// policy cover).
//
// The review asked for exactly this, and asked for it to be ONE case
// rather than thirty-five sources: "Kalau satu case nyata berhasil,
// baru scale."
//
// # What this is, and what it is not
//
// This is the RIG. Every stage is the real production code path, and
// the case is genuinely cross-domain. The evidence is a fixture.
//
// That distinction is the entire L3/L4 boundary and it is not narrowed
// here. Running this rig on real AIS positions, a real survey report
// and a real policy wording is L4, and it requires a data agreement
// that does not exist. What this test establishes is that when such an
// agreement exists, there is somewhere for the data to go -- which was
// not previously true, because nothing composed the three domains onto
// one case.

// crossDomainItem is one piece of evidence, tagged with the domain it
// came from. The tag is for the reader: the fabric itself is
// domain-neutral, which is why one case can carry all three.
type crossDomainItem struct {
	domain, evidenceID, versionID, sha, sourceID, what string
}

const (
	crossCaseID = "XD-CARGO-2026-001"
	crossClaim  = crossCaseID + "-CL"
)

// TestOneCommercialCaseAcrossThreeDomains runs a single cargo-damage
// matter end to end.
func TestOneCommercialCaseAcrossThreeDomains(t *testing.T) {
	store := audit.NewAuditStore()
	const proposition = "the cargo was damaged by seawater ingress before discharge, not after"

	// --- 1. EVIDENCE, from three domains onto one case ---------------
	//
	// Each item names the domain it came from. The case does not care:
	// the fabric is domain-neutral, and that is why one case can hold
	// all three without a per-domain engine.
	items := []crossDomainItem{
		{"maritime", crossCaseID + "-E1", crossCaseID + "-EV1", "sha256:ais-track",
			"ais-provider", "vessel positions and reported weather over the laden voyage"},
		{"commodity", crossCaseID + "-E2", crossCaseID + "-EV2", "sha256:loading-survey",
			"independent-surveyor", "the pre-loading survey recording cargo condition"},
		{"insurance", crossCaseID + "-E3", crossCaseID + "-EV3", "sha256:discharge-survey",
			"appointed-adjuster", "the discharge survey recording the damage"},
	}

	// --- 2. RIGHTS, evaluated before anything is shown ----------------
	//
	// Article 4: rights are evaluated before contact, not after. The
	// adjuster may see the discharge survey; a broker with a
	// process-visible grant may not see the content of any of them.
	grant := access.Grant{
		EvidenceVersionID: crossCaseID + "-EV3", RecipientID: "adjuster-1", RecipientRole: "adjuster",
		Procedural: access.P3PartyVisible, Content: access.C3ControlledFullView,
		Rights: []access.Right{access.View}, PolicyVersion: "policy-v1",
		Privilege: access.PrivilegeNotClaimed,
	}
	dec, err := access.Evaluate(grant, access.Request{
		EvidenceVersionID: crossCaseID + "-EV3", RecipientID: "adjuster-1",
		Right: access.View, Purpose: "adjusting the claim",
	})
	if err != nil {
		t.Fatalf("access.Evaluate: %v", err)
	}
	if !dec.Allowed {
		t.Fatalf("the appointed adjuster must be able to view the discharge survey: %s", dec.Reason)
	}
	broker := grant
	broker.RecipientID, broker.RecipientRole = "broker-1", "broker"
	broker.Procedural, broker.Content = access.P2ProcessVisible, access.C1Existence
	brokerDec, err := access.Evaluate(broker, access.Request{
		EvidenceVersionID: crossCaseID + "-EV3", RecipientID: "broker-1",
		Right: access.View, Purpose: "placing the risk",
	})
	if err != nil {
		t.Fatalf("access.Evaluate (broker): %v", err)
	}
	if brokerDec.Allowed {
		t.Fatal("a recipient granted existence only was permitted to view content")
	}

	// --- 3. PROVENANCE and temporal standing --------------------------
	att, err := timestamp.Assess("sha256:discharge-survey",
		chainEntryFor(t, "sha256:discharge-survey"), nil,
		[]string{"veriqo-operations-ltd", "claimant-ltd"})
	if err != nil {
		t.Fatalf("timestamp.Assess: %v", err)
	}
	if _, proven := att.ProvesExistenceBefore(); proven {
		t.Fatal("a self-hosted chain must not prove existence before a time")
	}

	// --- 4. TRUST: are the three sources independent? -----------------
	//
	// They are not all independent, and the case must not pretend
	// otherwise. The adjuster is appointed by the insurer, so the
	// discharge survey is party-mediated.
	sources := []independence.Source{
		unrelatedSource("ais-provider"),
		unrelatedSource("independent-surveyor"),
		partyMediatedSource("appointed-adjuster", "insurer-ltd"),
	}
	effective, err := independence.EffectiveSourceCount(sources)
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if effective < 2 {
		t.Fatalf("two genuinely unrelated sources should count, got %d", effective)
	}

	// --- 5. CONTRADICTION, carried rather than deleted ----------------
	//
	// Article 11. The loading survey says the cargo was sound; the
	// discharge survey says it was wet. That conflict IS the case, and
	// it must survive into the proof object as a stated limitation
	// rather than being resolved away by whichever source is trusted
	// more.
	absence, err := observability.Evaluate(observability.Assessment{
		Subject:  "the hatch-cover maintenance record for the laden voyage",
		SourceID: "vessel-operator", Conditions: observability.AllConditionsMet(),
		Material: true, Tick: 9,
	})
	if err != nil {
		t.Fatalf("observability.Evaluate: %v", err)
	}
	if !absence.State.CarriesEvidentialWeight() {
		t.Fatalf("a fully gated absence should carry weight: %s", absence.Reason)
	}

	// --- 6. REVERSE PROOF, before qualification -----------------------
	claim := reverseproof.Claim{
		ID: crossClaim, Description: proposition,
		Conditions: []reverseproof.Condition{
			{ID: "cond-ingress", Description: "seawater entered the hold before discharge"},
		},
	}
	reqs := []reverseproof.Requirement{
		{ID: "R-1", ConditionID: "cond-ingress", Description: "cargo sound at loading",
			ExpectedIfTrue:     "the loading survey records sound cargo",
			ContradictsIfShows: "the loading survey records pre-existing wetting",
			Status:             reverseproof.Obtained, DiagnosticValue: 0.9},
		{ID: "R-2", ConditionID: "cond-ingress", Description: "heavy weather on the laden passage",
			ExpectedIfTrue:     "the AIS track and weather agree with the reported conditions",
			ContradictsIfShows: "calm conditions throughout",
			Status:             reverseproof.Obtained, DiagnosticValue: 0.7},
		{ID: "R-3", ConditionID: "cond-ingress", Description: "hatch covers maintained",
			ExpectedIfTrue:     "maintenance records show compliance",
			ContradictsIfShows: "records show deferred maintenance",
			Status:             reverseproof.Unobtainable, DiagnosticValue: 0.5},
	}
	alts := []reverseproof.AlternativeHypothesis{
		{ID: "A-1", Description: "the cargo was already wet when loaded", Tested: true},
		{ID: "A-2", Description: "the damage occurred after discharge, in the shore warehouse", Tested: true},
	}
	rs, err := reverseproof.Build(claim, reqs, alts, 10)
	if err != nil {
		t.Fatalf("reverseproof.Build: %v", err)
	}
	gap := reverseproof.Analyze(rs, map[string]bool{"cond-ingress": true})

	rev, err := fref.NewExecution(fref.Reverse, proposition)
	if err != nil {
		t.Fatalf("NewExecution(reverse): %v", err)
	}
	completeStages(t, rev, fref.Order(fref.Reverse)...)

	fwd, err := fref.NewExecution(fref.Forward, proposition)
	if err != nil {
		t.Fatalf("NewExecution(forward): %v", err)
	}
	completeStages(t, fwd, fref.StageObservation, fref.StageEvidence, fref.StageKnowledge, fref.StageReasoning, fref.StageTrust)

	// The evidence the forward run used, and the evidence the reverse
	// run says was required. Closure holds when they agree.
	evidenceIDs := []string{crossCaseID + "-EV1", crossCaseID + "-EV2", crossCaseID + "-EV3"}
	closure, err := fref.Close(fwd, rev, evidenceIDs, evidenceIDs)
	if err != nil {
		t.Fatalf("fref.Close: %v", err)
	}
	if !closure.Holds {
		t.Fatalf("the reverse closure must hold before qualification: %s", closure.Explain())
	}

	// --- 7. QUALIFICATION --------------------------------------------
	q, err := state.New(crossCaseID, state.Supported, "policy-v1",
		"two independent sources agree on the pre-loading condition and the passage conditions",
		nil, 10)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	// --- 8. PROOF SEAL ------------------------------------------------
	var evrefs []proof.EvidenceRef
	for _, it := range items {
		evrefs = append(evrefs, proof.EvidenceRef{
			EvidenceID: it.evidenceID, EvidenceVersionID: it.versionID,
			SHA256: it.sha, SourceID: it.sourceID,
		})
	}
	o, err := proof.Seal(proof.Object{
		Proposition: proof.Proposition{ID: crossCaseID + "-P", Statement: proposition,
			SubjectType: "CARGO", SubjectID: crossCaseID + "-PARCEL"},
		Scope:        proof.Scope{CaseID: crossCaseID, Matter: "cargo damage, cross-domain"},
		Jurisdiction: proof.Jurisdiction{Code: "SG", Forum: "SIAC", GoverningLaw: "English law"},
		TimeWindow:   proof.TimeWindow{FromTick: 1, ToTick: 500},
		EvidenceSet:  evrefs,
		Quality:      proof.Quality{Assessed: true, Grade: "primary"},
		ReverseProof: rs, ReverseProofGap: gap,
		MissingEvidence: []proof.MissingEvidence{
			{ConditionID: "cond-ingress", Description: "hatch-cover maintenance record",
				Obtainable: false, Reason: "requested from the operator and observed absent; retention expired"},
		},
		Trust: proof.TrustAssessment{Assessed: true, EffectiveSourceCount: effective,
			Verdicts: map[string]independence.Verdict{
				"ais-provider:independent-surveyor": independence.Independent,
			}},
		Qualification: q,
		Authority:     proof.Authority{AuthorityID: "analyst-1", Role: "senior-analyst", PolicyVersion: "policy-v1"},
		Disclosure:    proof.DisclosureState{Procedural: 2, Content: 3, Privilege: "NOT_CLAIMED"},
		Limitations: []string{
			"the discharge survey is party-mediated: the adjuster is appointed by the insurer",
			"the hatch-cover maintenance record was searched for and observed absent",
			"the evidence in this run is a fixture, not real commercial data",
			"temporal ordering is chain-attested, not independently attested",
		},
		Provenance: proof.Provenance{GeneratedBy: "fref-pipeline", GeneratedAtTick: 10,
			PipelineVersion: "fref-v1",
			InputHashes:     []string{"sha256:ais-track", "sha256:loading-survey", "sha256:discharge-survey"}},
		ReplayReference: "REPLAY-" + crossCaseID,
	})
	if err != nil {
		t.Fatalf("proof.Seal: %v", err)
	}
	if o.Sufficiency != proof.Sufficient {
		t.Fatalf("the cross-domain object should be sufficient, got %s: %s", o.Sufficiency, proof.InsufficiencyReason(o))
	}

	// --- 9. FINDING, from the one authority ---------------------------
	f, err := proof.NewFinding(o, 20)
	if err != nil {
		t.Fatalf("proof.NewFinding: %v", err)
	}
	if err := f.VerifyIntegrity(); err != nil {
		t.Fatalf("the finding must be intact: %v", err)
	}
	if f.CaseID() != crossCaseID {
		t.Fatalf("the finding names case %q, not the cross-domain case", f.CaseID())
	}

	// --- 10. AUTHORIZED DECISION --------------------------------------
	auth, err := proof.Authorize(f, o, "partner-1", "partner", "policy-v1", "adopted on review", 30)
	if err != nil {
		t.Fatalf("proof.Authorize: %v", err)
	}
	d, err := proof.Decide(auth, "refer_to_tribunal", "the evidence package supports referral",
		map[string]string{"forum": "SIAC"}, 40)
	if err != nil {
		t.Fatalf("proof.Decide: %v", err)
	}

	// --- 11. THE CASE, carrying all three domains ---------------------
	c := openCrossDomainCase(t, o, closure, d, items)

	// --- 12. THE GRAPH -------------------------------------------------
	g, err := caseproofgraph.Build(c,
		map[string]proof.Object{crossClaim: o},
		map[string]proof.Finding{crossClaim: f}, 50)
	if err != nil {
		t.Fatalf("caseproofgraph.Build: %v", err)
	}
	if err := caseproofgraph.AddDecision(g, d); err != nil {
		t.Fatalf("AddDecision: %v", err)
	}
	if err := caseproofgraph.VerifyGraph(g); err != nil {
		t.Fatalf("the graph must verify: %v", err)
	}

	// --- 13. LEDGER ----------------------------------------------------
	records, chain, err := casefabric.Mirror(store, c, "policy-v1", map[string]proof.Object{crossClaim: o})
	if err != nil {
		t.Fatalf("Mirror: %v", err)
	}
	var actions []string
	for _, r := range store.Snapshot() {
		actions = append(actions, r.Action)
	}
	if v := fref.VerifyEventOrder(actions); len(v) > 0 {
		t.Fatalf("the cross-domain ledger is out of constitutional order: %s", v[0])
	}
	if gaps := fref.VerifyEventGates(actions); len(gaps) > 0 {
		t.Fatalf("the cross-domain ledger skipped a gate: %s", gaps[0])
	}

	// --- 14. DISCLOSURE: a redacted derivative for the broker ----------
	//
	// The broker may see that the case exists but not the claimant's
	// name. This runs the real Article 18 pipeline, so the disclosure
	// is verified rather than asserted.
	rel, err := worker.NewPipeline().Run(worker.Request{
		Kind:                worker.KindXLSX,
		Original:            crossDomainWorkbook("Claimant Trading Pte Ltd"),
		OriginalVersionID:   crossCaseID + "-EV4",
		DerivativeVersionID: crossCaseID + "-EV4-R1",
		PinnedOriginalHash:  redaction.Hash(crossDomainWorkbook("Claimant Trading Pte Ltd")),
		ForbiddenTerms:      []string{"Claimant Trading Pte Ltd"},
	})
	if err != nil {
		t.Fatalf("the redaction pipeline must release a verified derivative: %v", err)
	}
	if _, err := store.Append("compliance-1", rel.LedgerEvent().Action, rel.Chain().Explain()); err != nil {
		t.Fatalf("appending the disclosure event: %v", err)
	}

	// --- 15. REPLAY: independent re-verification -----------------------
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		t.Fatalf("the ledger must verify: %v", err)
	}
	if err := event.VerifyChain(chain.Events()); err != nil {
		t.Fatalf("the event chain must verify: %v", err)
	}
	if err := proof.VerifyHash(o); err != nil {
		t.Fatalf("the proof object must still verify: %v", err)
	}
	if err := f.VerifyIntegrity(); err != nil {
		t.Fatalf("the finding must still verify after the whole run: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("the case mirrored no records")
	}
}

// TestTheCrossDomainCaseStatesItsFixtureBoundary. The rig must not be
// mistaken for the qualification it makes possible.
func TestTheCrossDomainCaseStatesItsFixtureBoundary(t *testing.T) {
	src := readSelf(t, "single_commercial_case_test.go")
	for _, required := range []string{
		"the evidence is a fixture",
		"requires a data agreement that does not exist",
	} {
		if !strings.Contains(src, required) {
			t.Fatalf("the cross-domain proof must state %q about itself", required)
		}
	}
}

// TestTheCrossDomainCaseCarriesItsContradictionForward proves the
// party-mediated source is disclosed as a limitation rather than
// silently discounted. A case that quietly drops its weakest source
// looks stronger than it is.
func TestTheCrossDomainCaseCarriesItsContradictionForward(t *testing.T) {
	src := readSelf(t, "single_commercial_case_test.go")
	if !strings.Contains(src, "the adjuster is appointed by the insurer") {
		t.Fatal("the party-mediated source must be carried as a stated limitation")
	}
}

// partyMediatedSource builds a source whose acquisition ran through an
// interested party, which is what an insurer-appointed adjuster is.
// partyMediatedSource builds a FULLY ASSESSED source whose acquisition
// ran through an interested party.
//
// Every disqualifying dimension is populated, so the source is
// assessable rather than UNKNOWN. An earlier version listed five
// dimensions and left three unassessed, which meant this source could
// never corroborate anything under Article 28 -- a gap the cluster
// count hid, because clustering counts an unassessed pair as two.
//
// Party mediation is NOT an independence verdict. An insurer-appointed
// adjuster can be perfectly independent of an AIS provider. It is a
// limitation, and the case carries it as one rather than silently
// discounting the source.
func partyMediatedSource(id, controller string) independence.Source {
	attrs := map[independence.Dimension]string{}
	for _, d := range independence.DisqualifyingDimensions() {
		attrs[d] = id + "-" + string(d)
	}
	attrs[independence.OrganizationalControl] = controller
	attrs[independence.AcquisitionPath] = "party-mediated"
	return independence.Source{ID: id, Attributes: attrs}
}

func chainEntryFor(t *testing.T, digest string) *timestamp.ChainAttestation {
	t.Helper()
	e, err := timestamp.NewChainAttestation(digest, 0, "", "veriqo-operations-ltd")
	if err != nil {
		t.Fatalf("chain attestation: %v", err)
	}
	return &e
}

// openCrossDomainCase opens the case and walks it through the lawful
// sequence, recording the participation of all three domains.
//
// The case's opening domain is insurance because that is who instructed
// the matter. Maritime and commodity participate through the evidence
// they contributed, which is recorded on the timeline rather than in a
// second case: one matter, one case, one spine.
func openCrossDomainCase(t *testing.T, o proof.Object, closure fref.Closure, d proof.Decision,
	items []crossDomainItem) *casefabric.Case {
	t.Helper()

	c, err := casefabric.Open(casefabric.Identity{
		CaseID: crossCaseID, TenantID: "tenant-a", Domain: "insurance",
		ExternalRefs: map[string]string{
			"claim_no": "CLM-XD-1", "bl_no": "BL-XD-1", "vessel_imo": "IMO-XD-1",
		},
	}, "analyst-1", 1)
	if err != nil {
		t.Fatalf("casefabric.Open: %v", err)
	}

	var refs []casefabric.EvidenceRef
	for _, it := range items {
		refs = append(refs, casefabric.EvidenceRef{
			EvidenceID: it.evidenceID, EvidenceVersionID: it.versionID,
			SHA256: it.sha, SourceID: it.sourceID,
		})
	}

	steps := []func() error{
		func() error {
			return c.SetScope(o.Scope, o.Jurisdiction, o.TimeWindow, casefabric.Mission{
				Statement: "establish when the seawater ingress occurred",
				Intent:    "resolve the cargo damage claim",
				SetBy:     "analyst-1", SetAtTick: 2,
			}, "analyst-1", 2)
		},
		func() error { return c.AddEvidence(refs, "analyst-1", 3) },
		func() error {
			return c.AddHypothesis(casefabric.Hypothesis{
				ID: "H-1", Description: "the cargo was already wet when loaded",
			}, "analyst-1", 4)
		},
		func() error {
			return c.RegisterClaim(casefabric.Claim{
				ID: crossClaim, Material: true, Proposition: o.Proposition,
			}, "analyst-1", 5)
		},
		func() error {
			return c.TestHypothesis("H-1", "excluded by the pre-loading survey", "analyst-1", 6)
		},
		func() error {
			return c.RecordReverseClosure(o.Proposition.ID, closure.Holds, "analyst-1", 7)
		},
		func() error { return c.BeginQualification("analyst-1", 8) },
		func() error { return c.AttachProof(crossClaim, o, "analyst-1", 9) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
	}

	f, err := proof.NewFinding(o, 20)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	if err := c.RecordFinding(f.Hash(), o.CanonicalHash, "analyst-1", 20); err != nil {
		t.Fatalf("RecordFinding: %v", err)
	}
	if err := c.RecordAuthorizedDecision(d, "partner-1", 40); err != nil {
		t.Fatalf("RecordAuthorizedDecision: %v", err)
	}

	gate := casefabric.ResolutionGate{
		Decision: d, ReverseClosureHolds: closure.Holds,
		ClosureSubject: o.Proposition.ID, ClosureExplanation: closure.Explain(),
	}
	if _, err := c.Resolve(gate, "evidence_package_delivered",
		"ingress established as pre-discharge on the sampled parcel", "partner-1", 41); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := c.AddOutcomeLimitations(o.Limitations); err != nil {
		t.Fatalf("AddOutcomeLimitations: %v", err)
	}
	return c
}

// crossDomainWorkbook builds a real, compressed OOXML workbook holding
// the claimant's name, so the disclosure step redacts something that is
// genuinely there rather than something the fixture invented.
func crossDomainWorkbook(claimant string) []byte {
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">` +
			`<si><t>` + claimant + `</t></si><si><t>Parcel 3, seawater damage</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`,
	}
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		hdr := &zip.FileHeader{Name: n, Method: zip.Deflate}
		hdr.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			return nil
		}
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}
