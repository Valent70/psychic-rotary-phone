package casefile

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
	"veriqo/pkg/findings"
	"veriqo/pkg/identity"
	"veriqo/pkg/ontology"
	"veriqo/pkg/reverseproof"
	"veriqo/pkg/uncertainty"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "casefile", Revision: 1},
	}
}

func testClaim(id, statement string) claim.Claim {
	return claim.Claim{
		ID: contract.ID(id), TenantID: "t-acme", CaseID: "case-1",
		Statement: statement,
		Scope: claim.Scope{Subject: "CargoLot L-77", Aspect: "quantity",
			Period: contract.Interval{From: d(2024), To: to(d(2025))}},
		SupportingEvidence:    []string{"ev:survey"},
		AlternativeHypotheses: []string{"measurement conversion error"},
		DisproofPath:          "a certified density measurement showing incomparable bases",
		Status:                claim.Supported,
		Versions:              versions(),
	}
}

func proofFor(t *testing.T, c claim.Claim, id string, satisfy bool) *reverseproof.Proof {
	t.Helper()
	conds := []reverseproof.Condition{
		{ID: "C1", Must: "the cargo existed", Expected: "a loading survey",
			Sources: []string{"terminal"}, Diagnosticity: 0.5, AcquisitionCost: 0.2,
			LegallyAccessible: true},
	}
	p, err := reverseproof.New(c, contract.ID(id), conds, []string{"loading overstated"})
	if err != nil {
		t.Fatal(err)
	}
	if satisfy {
		if err := p.Set("C1", reverseproof.Satisfied, []string{"ev:C1"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func person(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Human, TenantID: "t-acme",
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
}

func mintFinding(t *testing.T, c claim.Claim, p *reverseproof.Proof, id string) findings.Finding {
	t.Helper()
	var js []uncertainty.Judgement
	for _, dim := range uncertainty.Dimensions() {
		js = append(js, uncertainty.Judgement{Dimension: dim, Level: uncertainty.High,
			Basis: "assessed against the case record"})
	}
	v, err := uncertainty.New(string(c.ID), js...)
	if err != nil {
		t.Fatal(err)
	}
	f, err := findings.Mint(findings.MintRequest{
		ID: contract.ID(id), CaseID: "case-1", Claim: c, Proof: p, Confidence: v,
		Limitations: []string{"the survey covers one tank of three"},
		Proposer:    person("human:analyst-1"), Approver: person("human:reviewer-1"),
		ApproverGrant: authority.Grant{Principal: "human:reviewer-1",
			Role: authority.Reviewer, TenantID: "t-acme"},
		At: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func openCase(t *testing.T) *Case {
	t.Helper()
	c, err := New("case-1", "t-acme", "Cargo discrepancy L-77", versions())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// --- FC-007 SEQUENCING_BYPASS -------------------------------------------

// TestCaseCannotResolveOverAnUnprovenMaterialClaim is the negative
// test the failure-class register cites.
func TestCaseCannotResolveOverAnUnprovenMaterialClaim(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was 1,800 MT short")
	if err := c.AddClaim(cl, true, "the quantum rests entirely on this"); err != nil {
		t.Fatal(err)
	}
	// No proof attached.
	f := mintFinding(t, cl, proofFor(t, cl, "reverseproof:rp1", true), "finding:f1")

	err := c.Resolve([]findings.Finding{f}, now)
	if !errors.Is(err, ErrUnprovenClaim) {
		t.Fatalf("a case resolved over an unproven material claim: %v", err)
	}
	if c.Status != Open {
		t.Fatalf("the case moved to %s despite the refusal", c.Status)
	}
}

// TestResolveFirstProveLater is the mutation test: it constructs the
// forbidden ORDER directly and demands rejection.
func TestResolveFirstProveLater(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was 1,800 MT short")
	c.AddClaim(cl, true, "the quantum rests on this")
	p := proofFor(t, cl, "reverseproof:rp1", true)
	f := mintFinding(t, cl, p, "finding:f1")

	// The mutation: resolve, THEN attach the proof.
	if err := c.Resolve([]findings.Finding{f}, now); err == nil {
		t.Fatal("MUTANT SURVIVED: the case resolved before any proof was attached")
	}
	if err := c.AttachProof("claim:c1", p); err != nil {
		t.Fatal(err)
	}
	// Resolving now is legitimate -- the order was corrected.
	if err := c.Resolve([]findings.Finding{f}, now.Add(time.Minute)); err != nil {
		t.Fatalf("MUTANT SURVIVED IN THE OTHER DIRECTION: a correctly ordered case was "+
			"refused, which would push somebody to weaken the gate: %v", err)
	}
	// And a proof cannot be attached AFTER resolution, which would
	// let the record be tidied up retrospectively.
	if err := c.AttachProof("claim:c1", p); !errors.Is(err, ErrClosed) {
		t.Fatalf("MUTANT SURVIVED: a proof was attached to a resolved case: %v", err)
	}
}

// TestAnUngatedLedgerIsCaughtEvenWhenPerfectlyOrdered is the
// regression test, and the half that is usually missing.
//
// Event ORDER is not evidence that anything was checked. A case whose
// events are in exactly the right sequence, but where no gate decision
// was ever recorded, must fail verification.
func TestAnUngatedLedgerIsCaughtEvenWhenPerfectlyOrdered(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was 1,800 MT short")
	c.AddClaim(cl, true, "the quantum rests on this")
	p := proofFor(t, cl, "reverseproof:rp1", true)
	c.AttachProof("claim:c1", p)
	f := mintFinding(t, cl, p, "finding:f1")
	if err := c.Resolve([]findings.Finding{f}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("a properly gated case failed verification: %v", err)
	}

	// Now the mutation: the same case, in the same perfect order, with
	// the gate record removed. Nothing about the ordering changed.
	c.gate = nil
	if err := c.Verify(); !errors.Is(err, ErrNoGate) {
		t.Fatalf("A PERFECTLY ORDERED CASE WITH NO GATE RECORD VERIFIED: %v", err)
	}

	// And a gate that passed but was recorded AFTER the resolution is
	// a gate that checked the outcome, not the input.
	late := Gate{At: now.Add(time.Hour), Passed: true, ClaimsChecked: []string{"claim:c1"}}
	digest, _ := c.gateDigest()
	late.Digest = digest
	c.gate = &late
	if err := c.Verify(); !errors.Is(err, ErrGateAfterResolution) {
		t.Fatalf("a gate recorded after the resolution was accepted: %v", err)
	}
}

// TestAGateCannotBeReusedAfterTheCaseChanges. A gate decision is bound
// to the claims and proofs it saw.
func TestAGateCannotBeReusedAfterTheCaseChanges(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was short")
	c.AddClaim(cl, true, "the quantum rests on this")
	p := proofFor(t, cl, "reverseproof:rp1", true)
	c.AttachProof("claim:c1", p)

	g, err := c.RunGate(now)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Passed {
		t.Fatalf("the gate did not pass: %s", g.Reason)
	}

	// Add a second material claim with no proof, keeping the old gate.
	cl2 := testClaim("claim:c2", "the loss occurred in transit")
	c.AddClaim(cl2, true, "causation rests on this")
	f := mintFinding(t, cl, p, "finding:f1")
	c.findings = []findings.Finding{f}
	c.Status = Resolved
	c.resolvedAt = now.Add(time.Minute)

	if err := c.Verify(); !errors.Is(err, ErrGateIncomplete) {
		t.Fatalf("a stale gate decision covered a changed case: %v", err)
	}
}

// TestAnIncompleteDecompositionDoesNotPassTheGate.
func TestAnIncompleteDecompositionDoesNotPassTheGate(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was short")
	c.AddClaim(cl, true, "the quantum rests on this")
	c.AttachProof("claim:c1", proofFor(t, cl, "reverseproof:rp1", false))

	g, err := c.RunGate(now)
	if err == nil {
		t.Fatal("a gate passed over an unexamined necessary condition")
	}
	if g.Passed {
		t.Fatal("the gate record says it passed")
	}
	if !strings.Contains(g.Reason, "unexamined") {
		t.Fatalf("the gate does not say what it found: %q", g.Reason)
	}
}

// TestARefutedProofDemotesTheClaimWhenAttached, so the case never
// holds a proven-false claim marked SUPPORTED.
func TestARefutedProofDemotesTheClaimWhenAttached(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was short")
	c.AddClaim(cl, true, "the quantum rests on this")
	p := proofFor(t, cl, "reverseproof:rp1", true)
	p.Set("C1", reverseproof.Refuted, []string{"ev:loading-record"}, "the cargo was never loaded")
	if err := c.AttachProof("claim:c1", p); err != nil {
		t.Fatal(err)
	}
	entries := c.Entries()
	if entries[0].Claim.Status != claim.Contradicted {
		t.Fatalf("the claim is %s after its decomposition refuted it", entries[0].Claim.Status)
	}
	if _, err := c.RunGate(now); err == nil {
		t.Fatal("a gate passed over a refuted claim")
	}
}

// TestANonMaterialClaimDoesNotBlockTheGate. The gate applies to what
// the conclusion depends on; a background claim that is merely
// interesting must not stall a case.
func TestANonMaterialClaimDoesNotBlockTheGate(t *testing.T) {
	c := openCase(t)
	material := testClaim("claim:c1", "the cargo was short")
	c.AddClaim(material, true, "the quantum rests on this")
	c.AttachProof("claim:c1", proofFor(t, material, "reverseproof:rp1", true))

	background := testClaim("claim:c2", "the vessel changed flag in 2023")
	background.Status = claim.Inconclusive
	background.AlternativeHypotheses = nil
	if err := c.AddClaim(background, false, ""); err != nil {
		t.Fatal(err)
	}
	g, err := c.RunGate(now)
	if err != nil {
		t.Fatalf("a non-material claim blocked the gate: %v", err)
	}
	if !g.Passed {
		t.Fatalf("the gate did not pass: %s", g.Reason)
	}
	// And the gate names only what it checked.
	if len(g.ClaimsChecked) != 1 || g.ClaimsChecked[0] != "claim:c1" {
		t.Fatalf("the gate checked %v", g.ClaimsChecked)
	}
}

// TestAMaterialClaimMustSayWhyItIsMaterial. Otherwise a claim can be
// reclassified to get past the gate with no record of the decision.
func TestAMaterialClaimMustSayWhyItIsMaterial(t *testing.T) {
	c := openCase(t)
	if err := c.AddClaim(testClaim("claim:c1", "x"), true, ""); err == nil {
		t.Fatal("a material claim was added with no stated reason")
	}
}

// TestACaseWithNoMaterialClaimCannotResolve.
func TestACaseWithNoMaterialClaimCannotResolve(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "x")
	c.AddClaim(cl, false, "")
	f := mintFinding(t, cl, proofFor(t, cl, "reverseproof:rp1", true), "finding:f1")
	if err := c.Resolve([]findings.Finding{f}, now); err == nil {
		t.Fatal("a case with no material claim resolved")
	}
}

// TestOnlyMintedFindingsResolveACase. FC-001 and FC-007 meet here.
func TestOnlyMintedFindingsResolveACase(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was short")
	c.AddClaim(cl, true, "the quantum rests on this")
	c.AttachProof("claim:c1", proofFor(t, cl, "reverseproof:rp1", true))

	fabricated := findings.Finding{ID: "finding:forged", CaseID: "case-1", TenantID: "t-acme"}
	if err := c.Resolve([]findings.Finding{fabricated}, now); !errors.Is(err, findings.ErrNotMinted) {
		t.Fatalf("a fabricated finding resolved a case: %v", err)
	}
}

// TestResolvingTwiceIsRefused.
func TestResolvingTwiceIsRefused(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was short")
	c.AddClaim(cl, true, "the quantum rests on this")
	p := proofFor(t, cl, "reverseproof:rp1", true)
	c.AttachProof("claim:c1", p)
	f := mintFinding(t, cl, p, "finding:f1")
	if err := c.Resolve([]findings.Finding{f}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.Resolve([]findings.Finding{f}, now); !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("a case resolved twice: %v", err)
	}
}

// TestAbandonmentIsARealOutcome. A system with no word for it pushes
// analysts towards manufacturing a conclusion.
func TestAbandonmentIsARealOutcome(t *testing.T) {
	c := openCase(t)
	c.AddClaim(testClaim("claim:c1", "x"), true, "the quantum rests on this")
	if err := c.Abandon("the load-port records were destroyed before we were instructed", now); err != nil {
		t.Fatal(err)
	}
	if c.Status != Abandoned {
		t.Fatalf("status = %s", c.Status)
	}
	if !strings.Contains(strings.Join(c.Limitations, " "), "abandoned") {
		t.Fatalf("the abandonment is not recorded: %v", c.Limitations)
	}
	if err := c.Abandon("again", now); !errors.Is(err, ErrClosed) {
		t.Fatal("an abandoned case was abandoned again")
	}
	// Verify does not complain about a case that never resolved.
	if err := c.Verify(); err != nil {
		t.Fatalf("an abandoned case failed verification: %v", err)
	}
}

// TestTheCaseCollectsItsOwnContradictions, so a reader sees them
// without walking the claim tree.
func TestTheCaseCollectsItsOwnContradictions(t *testing.T) {
	c := openCase(t)
	cl := testClaim("claim:c1", "the cargo was short")
	cl.Status = claim.PartiallySupported
	cl.ContradictingEvidence = []string{"ev:terminal-weighbridge"}
	if err := c.AddClaim(cl, true, "the quantum rests on this"); err != nil {
		t.Fatal(err)
	}
	if len(c.Contradictions) != 1 {
		t.Fatalf("Contradictions = %v", c.Contradictions)
	}
	if !strings.Contains(c.Report(), "contradictions found") {
		t.Fatalf("the report hides the contradictions:\n%s", c.Report())
	}
}

// TestTheReportShowsWhetherTheGateRan.
func TestTheReportShowsWhetherTheGateRan(t *testing.T) {
	c := openCase(t)
	c.AddClaim(testClaim("claim:c1", "x"), true, "the quantum rests on this")
	if !strings.Contains(c.Report(), "sequencing gate: NOT RUN") {
		t.Fatalf("the report does not disclose an unrun gate:\n%s", c.Report())
	}
	c.AttachProof("claim:c1", proofFor(t, testClaim("claim:c1", "x"), "reverseproof:rp1", true))
	c.RunGate(now)
	if !strings.Contains(c.Report(), "sequencing gate at") {
		t.Fatalf("the report does not show the gate decision:\n%s", c.Report())
	}
}

// TestSystemIntegrationProofForEveryDomain is the positive test the
// register cites for FC-007: the gated pipeline runs end to end for
// every domain the ontology declares, not only for maritime.
func TestSystemIntegrationProofForEveryDomain(t *testing.T) {
	ont, err := ontology.Veriqo(1)
	if err != nil {
		t.Fatal(err)
	}
	domains := ont.Domains()
	if len(domains) < 6 {
		t.Fatalf("only %d domains declared: %v", len(domains), domains)
	}

	for _, domain := range domains {
		// Each domain view must carry objects and relationships, or a
		// case in it could not be built at all.
		objs, rels := ont.View(domain)
		if len(objs) == 0 || len(rels) == 0 {
			t.Errorf("%s: view has %d objects and %d relationships", domain, len(objs), len(rels))
			continue
		}

		c, err := New("case-1", "t-acme", domain+" case", versions())
		if err != nil {
			t.Fatalf("%s: %v", domain, err)
		}
		cl := testClaim("claim:c1", "a material conclusion in the "+domain+" domain")
		if err := c.AddClaim(cl, true, "the conclusion rests on this"); err != nil {
			t.Fatalf("%s: %v", domain, err)
		}
		p := proofFor(t, cl, "reverseproof:rp1", true)
		if err := c.AttachProof("claim:c1", p); err != nil {
			t.Fatalf("%s: %v", domain, err)
		}
		f := mintFinding(t, cl, p, "finding:f1")
		if err := c.Resolve([]findings.Finding{f}, now); err != nil {
			t.Fatalf("%s: the gated pipeline did not complete: %v", domain, err)
		}
		if err := c.Verify(); err != nil {
			t.Fatalf("%s: the resolved case does not verify: %v", domain, err)
		}
		// And the gate must have actually been consulted.
		g, ok := c.Gate()
		if !ok || !g.Passed {
			t.Errorf("%s: no passing gate decision was recorded", domain)
		}
	}
}
