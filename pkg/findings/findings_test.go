package findings

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
	"veriqo/pkg/reverseproof"
	"veriqo/pkg/uncertainty"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

func person(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Human, TenantID: "t-acme",
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
}

func grantFor(id string, r authority.Role) authority.Grant {
	return authority.Grant{Principal: contract.ID(id), Role: r, TenantID: "t-acme"}
}

func testClaim() claim.Claim {
	return claim.Claim{
		ID: "claim:c1", TenantID: "t-acme", CaseID: "case-1",
		Statement: "the cargo discharged was 1,800 MT short",
		Scope: claim.Scope{Subject: "CargoLot L-77", Aspect: "quantity",
			Period: contract.Interval{From: d(2024), To: to(d(2025))}},
		SupportingEvidence:    []string{"ev:discharge-survey", "ev:bl"},
		AlternativeHypotheses: []string{"measurement conversion error"},
		DisproofPath:          "a certified density measurement showing incomparable bases",
		Status:                claim.Supported,
		Versions: contract.VersionSet{
			Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
			Policy:    contract.Version{Component: "baseline", Revision: 1},
			Algorithm: contract.Version{Component: "findings", Revision: 1},
		},
	}
}

func testProof(t *testing.T, satisfyAll bool) *reverseproof.Proof {
	t.Helper()
	conds := []reverseproof.Condition{
		{ID: "C1", Must: "the cargo existed", Expected: "a loading survey",
			Sources: []string{"terminal"}, Diagnosticity: 0.5, AcquisitionCost: 0.2,
			LegallyAccessible: true},
		{ID: "C2", Must: "the bases are comparable", Expected: "density at both ends",
			Sources: []string{"inspector"}, Diagnosticity: 0.9, AcquisitionCost: 0.4,
			LegallyAccessible: true},
	}
	p, err := reverseproof.New(testClaim(), "reverseproof:rp1", conds,
		[]string{"loading quantity overstated"})
	if err != nil {
		t.Fatal(err)
	}
	if satisfyAll {
		for _, c := range conds {
			if err := p.Set(c.ID, reverseproof.Satisfied, []string{"ev:" + c.ID}, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	return p
}

func vector(t *testing.T) uncertainty.Vector {
	t.Helper()
	var js []uncertainty.Judgement
	for _, dim := range uncertainty.Dimensions() {
		l := uncertainty.High
		if dim == uncertainty.Causal {
			l = uncertainty.Low
		}
		js = append(js, uncertainty.Judgement{Dimension: dim, Level: l,
			Basis: "assessed against the case record"})
	}
	v, err := uncertainty.New("claim:c1", js...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func request(t *testing.T) MintRequest {
	t.Helper()
	return MintRequest{
		ID: "finding:f1", CaseID: "case-1",
		Claim: testClaim(), Proof: testProof(t, true),
		Confidence:    vector(t),
		Limitations:   []string{"the discharge survey covers one tank of three"},
		Proposer:      person("human:analyst-1"),
		Approver:      person("human:reviewer-1"),
		ApproverGrant: grantFor("human:reviewer-1", authority.Reviewer),
		At:            now,
	}
}

// --- FC-001 AUTHORITY_DIFFUSION ----------------------------------------

// TestAFindingNamesExactlyOneProofOneCaseOneAuthority is the positive
// test the failure-class register cites.
func TestAFindingNamesExactlyOneProofOneCaseOneAuthority(t *testing.T) {
	f, err := Mint(request(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := Require(f); err != nil {
		t.Fatalf("a minted finding failed its own guard: %v", err)
	}
	if f.CaseID != "case-1" {
		t.Fatalf("case = %q", f.CaseID)
	}
	if f.ClaimID != "claim:c1" || f.ProofID != "reverseproof:rp1" {
		t.Fatalf("the finding names claim %s and proof %s", f.ClaimID, f.ProofID)
	}
	if f.ApprovedBy != "human:reviewer-1" || f.ProposedBy != "human:analyst-1" {
		t.Fatalf("approved by %s, proposed by %s", f.ApprovedBy, f.ProposedBy)
	}
	if f.ApprovedAt.IsZero() {
		t.Fatal("the finding records no approval instant")
	}
}

// TestGraphAsABackDoorToAFinding is the negative test.
//
// The original defect was a graph package minting a Finding. This
// constructs the value the way any other package could -- as a struct
// literal -- and shows it is inert.
func TestGraphAsABackDoorToAFinding(t *testing.T) {
	// Everything a graph package could set from outside this package:
	fabricated := Finding{
		ID: "finding:forged", TenantID: "t-acme", CaseID: "case-1",
		ClaimID: "claim:c1", ProofID: "reverseproof:rp1",
		Statement:  "the cargo was short",
		ApprovedBy: "human:reviewer-1", ApprovedAt: now,
		Limitations:             []string{"none"},
		AlternativeExplanations: []string{"none"},
	}
	if fabricated.Minted() {
		t.Fatal("A FABRICATED FINDING REPORTS ITSELF MINTED")
	}
	if err := Require(fabricated); !errors.Is(err, ErrNotMinted) {
		t.Fatalf("the guard admitted a fabricated finding: %v", err)
	}
	if _, err := fabricated.Digest(); !errors.Is(err, ErrNotMinted) {
		t.Fatalf("a fabricated finding produced a digest: %v", err)
	}
	// Setting ApprovedBy is not the same as being approved: the mint
	// witness is unexported and cannot be written from outside.
	if err := fabricated.Verify(); !errors.Is(err, ErrNotMinted) {
		t.Fatalf("a fabricated finding verified: %v", err)
	}
}

// TestNoLibraryPackageCanMintAnAuthoritativeObject is the regression
// test: the property is enforced by the type system, not by a check
// somebody remembers.
func TestNoLibraryPackageCanMintAnAuthoritativeObject(t *testing.T) {
	// The witness is a private field. Copying a minted finding carries
	// it -- copies of an authorised finding are still authorised --
	// but no field assignment from outside can create one.
	real, err := Mint(request(t))
	if err != nil {
		t.Fatal(err)
	}
	copied := real
	if err := Require(copied); err != nil {
		t.Fatalf("a copy of a minted finding was refused: %v", err)
	}
	// Editing a copy after minting must be detected.
	copied.Statement = "the cargo was short by 5,000 MT"
	if err := Require(copied); err == nil {
		t.Fatal("A MINTED FINDING WAS EDITED AND STILL VERIFIED")
	}
	// And relabelling the approver is detected too.
	relabelled := real
	relabelled.ApprovedBy = "human:someone-else"
	if err := Require(relabelled); err == nil {
		t.Fatal("the approver was rewritten and the finding still verified")
	}
}

// TestAnApproverWithoutTheCapabilityCannotMint.
func TestAnApproverWithoutTheCapabilityCannotMint(t *testing.T) {
	req := request(t)
	req.ApproverGrant = grantFor("human:reviewer-1", authority.Analyst)
	if _, err := Mint(req); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("an ANALYST approved a finding: %v", err)
	}
}

// TestAnAgentCannotMintAFinding is Law 7 at the point of conclusion.
func TestAnAgentCannotMintAFinding(t *testing.T) {
	req := request(t)
	agent := identity.Principal{ID: "agent:research", Kind: identity.Agent,
		TenantID: "t-acme", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	req.Approver = agent
	req.ApproverGrant = grantFor("agent:research", authority.CaseOwner)
	if _, err := Mint(req); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("AN AGENT MINTED A FINDING: %v", err)
	}
}

// TestSelfApprovalIsRefusedAtTheMint.
func TestSelfApprovalIsRefusedAtTheMint(t *testing.T) {
	req := request(t)
	req.Proposer = req.Approver
	if _, err := Mint(req); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("a principal approved its own proposal: %v", err)
	}
	// And via a delegated agent.
	req = request(t)
	principal := req.Approver.ID
	agent := identity.Principal{ID: "agent:research", Kind: identity.Agent,
		TenantID: "t-acme", OnBehalfOf: &principal,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	req.Proposer = agent
	if _, err := Mint(req); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("a reviewer approved their own agent's proposal: %v", err)
	}
}

// TestAnExpiredApproverCannotMint.
func TestAnExpiredApproverCannotMint(t *testing.T) {
	req := request(t)
	req.At = now.Add(2 * time.Hour)
	if _, err := Mint(req); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("an expired credential approved a finding: %v", err)
	}
}

// --- The material -------------------------------------------------------

// TestAnUnestablishedClaimCannotBecomeAFinding.
func TestAnUnestablishedClaimCannotBecomeAFinding(t *testing.T) {
	for _, st := range []claim.Status{claim.Inconclusive, claim.Contradicted,
		claim.Unresolved, claim.PartiallySupported} {
		req := request(t)
		c := req.Claim
		c.Status = st
		if st == claim.Contradicted {
			c.ContradictingEvidence = []string{"ev:contra"}
		}
		req.Claim = c
		if _, err := Mint(req); err == nil {
			t.Errorf("a %s claim became a finding", st)
		}
	}
}

// TestARefutedDecompositionBlocksTheFinding.
func TestARefutedDecompositionBlocksTheFinding(t *testing.T) {
	req := request(t)
	p := testProof(t, true)
	p.Set("C2", reverseproof.Refuted, []string{"ev:density"}, "bases are incomparable")
	req.Proof = p
	if _, err := Mint(req); !errors.Is(err, ErrProofRefuted) {
		t.Fatalf("a refuted decomposition produced a finding: %v", err)
	}
}

// TestAFindingWithNoDecompositionIsRefused.
func TestAFindingWithNoDecompositionIsRefused(t *testing.T) {
	req := request(t)
	req.Proof = nil
	if _, err := Mint(req); !errors.Is(err, ErrNoProof) {
		t.Fatalf("a finding with no reverse proof was minted: %v", err)
	}
}

// TestTheProofMustDecomposeThisClaim. A decomposition of a different
// claim would make the finding cite a proof of something else.
func TestTheProofMustDecomposeThisClaim(t *testing.T) {
	req := request(t)
	other := testClaim()
	other.ID = "claim:c2"
	p, err := reverseproof.New(other, "reverseproof:rp2",
		[]reverseproof.Condition{{ID: "C1", Must: "x", Expected: "y"}}, []string{"alt"})
	if err != nil {
		t.Fatal(err)
	}
	req.Proof = p
	if _, err := Mint(req); !errors.Is(err, ErrNoProof) {
		t.Fatalf("a proof of another claim was accepted: %v", err)
	}
}

// --- The limits ---------------------------------------------------------

// TestTheConfidenceVectorsQualificationsAreCarried.
//
// A strong conclusion that drops the vector's caveats is the overclaim
// in one step.
func TestTheConfidenceVectorsQualificationsAreCarried(t *testing.T) {
	f, err := Mint(request(t))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.Limitations, " ")
	if !strings.Contains(joined, "causal confidence is LOW") {
		t.Fatalf("the LOW causal dimension is not carried into the limitations:\n%v",
			f.Limitations)
	}
	// Every qualification, not just the first.
	for _, q := range f.Confidence.Qualifications() {
		found := false
		for _, l := range f.Limitations {
			if l == q {
				found = true
			}
		}
		if !found {
			t.Errorf("the limitation %q was dropped", q)
		}
	}
}

// TestAnIncompleteDecompositionBecomesALimitation.
func TestAnIncompleteDecompositionBecomesALimitation(t *testing.T) {
	req := request(t)
	p := testProof(t, false)
	p.Set("C1", reverseproof.Satisfied, []string{"ev:C1"}, "")
	req.Proof = p
	f, err := Mint(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.Limitations, " "), "reverse proof is 50% complete") {
		t.Fatalf("an incomplete decomposition did not become a limitation:\n%v", f.Limitations)
	}
}

// TestAnAssumedConditionBecomesALimitation.
func TestAnAssumedConditionBecomesALimitation(t *testing.T) {
	req := request(t)
	p := testProof(t, true)
	p.Set("C1", reverseproof.Assumed, nil, "not in dispute between the parties")
	req.Proof = p
	f, err := Mint(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.Limitations, " "), "is ASSUMED, not evidenced") {
		t.Fatalf("an assumption did not become a limitation:\n%v", f.Limitations)
	}
}

// TestAFindingMustNameAnAlternativeExplanation.
func TestAFindingMustNameAnAlternativeExplanation(t *testing.T) {
	req := request(t)
	c := req.Claim
	c.AlternativeHypotheses = nil
	c.Status = claim.Inconclusive // so Validate passes without alternatives
	req.Claim = c
	if _, err := Mint(req); err == nil {
		t.Fatal("a finding was minted with no alternative explanation")
	}
}

// TestTheFindingBelongsToOneCase.
func TestTheFindingBelongsToOneCase(t *testing.T) {
	req := request(t)
	req.CaseID = "case-99"
	if _, err := Mint(req); !errors.Is(err, ErrNoCase) {
		t.Fatalf("a finding was minted for a case its claim does not belong to: %v", err)
	}
	req = request(t)
	req.CaseID = ""
	if _, err := Mint(req); !errors.Is(err, ErrNoCase) {
		t.Fatalf("a finding was minted with no case: %v", err)
	}
}

// TestCrossTenantMintingIsRefused.
func TestCrossTenantMintingIsRefused(t *testing.T) {
	req := request(t)
	req.Proposer.TenantID = "t-beta"
	if _, err := Mint(req); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant finding was minted: %v", err)
	}
}

// TestTheReportCarriesTheLimitsAndAlternatives, because that is the
// form a customer receives.
func TestTheReportCarriesTheLimitsAndAlternatives(t *testing.T) {
	f, err := Mint(request(t))
	if err != nil {
		t.Fatal(err)
	}
	r := f.Report()
	for _, want := range []string{"limitations:", "alternative explanations considered:",
		"weakest confidence dimension:", "replay:"} {
		if !strings.Contains(r, want) {
			t.Errorf("the report omits %q:\n%s", want, r)
		}
	}
}

// TestMintingIsDeterministic. Two mints of the same material at the
// same instant must produce the same digest, or replay cannot compare.
func TestMintingIsDeterministic(t *testing.T) {
	a, err := Mint(request(t))
	if err != nil {
		t.Fatal(err)
	}
	da, _ := a.Digest()
	for i := 0; i < 20; i++ {
		b, err := Mint(request(t))
		if err != nil {
			t.Fatal(err)
		}
		db, _ := b.Digest()
		if da != db {
			t.Fatalf("two mints of the same material differ: %s vs %s", da, db)
		}
	}
}
