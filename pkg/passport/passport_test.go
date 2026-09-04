package passport

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
	"veriqo/pkg/findings"
	"veriqo/pkg/identity"
	"veriqo/pkg/reverseproof"
	"veriqo/pkg/uncertainty"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

type kp struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newKP(t *testing.T) kp {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return kp{pub, priv}
}

func (k kp) Sign(m []byte) ([]byte, string, error) {
	return ed25519.Sign(k.priv, m), "veriqo-key-1", nil
}

func (k kp) Verify(m, sig []byte, id string) error {
	if id != "veriqo-key-1" {
		return errors.New("unknown key")
	}
	if !ed25519.Verify(k.pub, m, sig) {
		return errors.New("bad signature")
	}
	return nil
}

func person(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Human, TenantID: "t-acme",
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
}

func finding(t *testing.T) findings.Finding {
	t.Helper()
	cl := claim.Claim{
		ID: "claim:c1", TenantID: "t-acme", CaseID: "case-1",
		Statement: "the cargo discharged was 1,800 MT short",
		Scope: claim.Scope{Subject: "CargoLot L-77", Aspect: "quantity",
			Period: contract.Interval{From: d(2024), To: to(d(2025))}},
		SupportingEvidence:    []string{"ev:survey", "ev:bl"},
		AlternativeHypotheses: []string{"measurement conversion error"},
		DisproofPath:          "a certified density measurement showing incomparable bases",
		Status:                claim.Supported,
		Versions: contract.VersionSet{
			Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
			Policy:    contract.Version{Component: "baseline", Revision: 1},
			Algorithm: contract.Version{Component: "passport", Revision: 1},
		},
	}
	p, err := reverseproof.New(cl, "reverseproof:rp1", []reverseproof.Condition{
		{ID: "C1", Must: "the cargo existed", Expected: "a loading survey",
			Sources: []string{"terminal"}, Diagnosticity: 0.5, AcquisitionCost: 0.2,
			LegallyAccessible: true},
	}, []string{"loading overstated"})
	if err != nil {
		t.Fatal(err)
	}
	p.Set("C1", reverseproof.Satisfied, []string{"ev:C1"}, "")

	var js []uncertainty.Judgement
	for _, dim := range uncertainty.Dimensions() {
		l := uncertainty.High
		if dim == uncertainty.Causal {
			l = uncertainty.Low
		}
		js = append(js, uncertainty.Judgement{Dimension: dim, Level: l, Basis: "assessed"})
	}
	v, err := uncertainty.New("claim:c1", js...)
	if err != nil {
		t.Fatal(err)
	}
	f, err := findings.Mint(findings.MintRequest{
		ID: "finding:f1", CaseID: "case-1", Claim: cl, Proof: p, Confidence: v,
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

func issue(t *testing.T, k kp) Passport {
	t.Helper()
	p, err := Issue(IssueRequest{
		Finding: finding(t), Qualification: "ASSURED",
		IssuedAt: now, Signer: k,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTheLimitationsAreInsideTheSignature.
//
// A passport whose limitations sat outside the signed payload could be
// presented with them stripped and would still verify.
func TestTheLimitationsAreInsideTheSignature(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	if len(p.Payload.Limitations) == 0 {
		t.Fatal("the passport carries no limitations")
	}

	// Strip them, exactly as a party presenting the finding might.
	stripped := p
	stripped.Payload.Limitations = nil
	res, err := Verify(stripped, VerifyOptions{Verifier: k, At: now})
	if err == nil || res.Trustworthy() {
		t.Fatal("A PASSPORT WITH ITS LIMITATIONS REMOVED STILL VERIFIED")
	}
	if !errors.Is(err, ErrTampered) {
		t.Fatalf("the tamper was reported as %v", err)
	}
}

// TestTheConfidenceVectorIsInsideTheSignatureToo.
func TestTheConfidenceVectorIsInsideTheSignature(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	upgraded := p
	v, err := uncertainty.New("claim:c1")
	if err != nil {
		t.Fatal(err)
	}
	for _, dim := range uncertainty.Dimensions() {
		v.Judgements[dim] = uncertainty.Judgement{Dimension: dim,
			Level: uncertainty.High, Basis: "assessed"}
	}
	upgraded.Payload.Confidence = v
	if _, err := Verify(upgraded, VerifyOptions{Verifier: k, At: now}); !errors.Is(err, ErrTampered) {
		t.Fatalf("the confidence vector was upgraded and the passport verified: %v", err)
	}
}

// TestAPassportWithNoLimitationsCannotBeIssued.
func TestAPassportWithNoLimitationsCannotBeIssued(t *testing.T) {
	f := finding(t)
	f.Limitations = nil
	// The finding no longer verifies either, which is the outer guard.
	if _, err := Issue(IssueRequest{Finding: f, Qualification: "ASSURED",
		IssuedAt: now, Signer: newKP(t)}); err == nil {
		t.Fatal("a passport was issued over a finding with no limitations")
	}
}

// TestAnUnmintedFindingCannotBeCertified. FC-001 reaching the
// customer-facing artefact.
func TestAnUnmintedFindingCannotBeCertified(t *testing.T) {
	fabricated := findings.Finding{ID: "finding:forged", CaseID: "case-1", TenantID: "t-acme",
		Limitations: []string{"none"}}
	_, err := Issue(IssueRequest{Finding: fabricated, Qualification: "ASSURED",
		IssuedAt: now, Signer: newKP(t)})
	if !errors.Is(err, ErrNotMinted) {
		t.Fatalf("a fabricated finding was certified: %v", err)
	}
}

// TestTheAbsenceOfExternalValidationIsStatedNotOmitted.
//
// Silence is what a reader fills in optimistically.
func TestTheAbsenceOfExternalValidationIsStatedNotOmitted(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	if p.Payload.IndependentlyValidated {
		t.Fatal("a passport claimed independent validation nobody performed")
	}
	if !strings.Contains(p.Render(), "NOBODY OUTSIDE VERIQO") {
		t.Fatalf("the rendering does not disclose the absence:\n%s", p.Render())
	}
	res, err := Verify(p, VerifyOptions{Verifier: k, At: now})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range res.Caveats {
		if strings.Contains(c, "no party outside VERIQO") {
			found = true
		}
	}
	if !found {
		t.Fatalf("verification did not report the absence of external validation: %v", res.Caveats)
	}
}

// TestAClaimOfValidationMustNameTheValidator.
func TestAClaimOfValidationMustNameTheValidator(t *testing.T) {
	_, err := Issue(IssueRequest{Finding: finding(t), Qualification: "QUALIFIED",
		IndependentlyValidated: true, IssuedAt: now, Signer: newKP(t)})
	if err == nil {
		t.Fatal("an unattributed claim of independent validation was signed")
	}
}

// TestAGenuinePassportVerifiesForAThirdParty. Nothing in the
// verification path needs access to VERIQO.
func TestAGenuinePassportVerifiesForAThirdParty(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	// The third party holds only the public key.
	verifier := kp{pub: k.pub}
	res, err := Verify(p, VerifyOptions{Verifier: verifier, At: now,
		Revocations: []Revocation{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Trustworthy() {
		t.Fatalf("a genuine passport was not trustworthy: %+v", res)
	}
	if !res.RevocationChecked {
		t.Fatal("an empty revocation list was treated as unchecked")
	}
}

// TestAForeignSignatureIsRejected.
func TestAForeignSignatureIsRejected(t *testing.T) {
	issuer := newKP(t)
	p := issue(t, issuer)
	other := newKP(t)
	if _, err := Verify(p, VerifyOptions{Verifier: other, At: now}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a passport verified against the wrong key: %v", err)
	}
}

// TestAnUncheckedRevocationIsACaveatNotAFailure.
//
// Conflating them would make an offline verifier report a genuine
// passport as invalid.
func TestAnUncheckedRevocationIsACaveatNotAFailure(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	res, err := Verify(p, VerifyOptions{Verifier: k, At: now}) // nil revocation list
	if err != nil {
		t.Fatalf("an offline verification failed: %v", err)
	}
	if res.RevocationChecked {
		t.Fatal("a nil revocation list was reported as checked")
	}
	if !res.Trustworthy() {
		t.Fatal("an unchecked revocation made a genuine passport untrustworthy")
	}
	found := false
	for _, c := range res.Caveats {
		if strings.Contains(c, "revocation was NOT checked") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the unchecked revocation was not reported: %v", res.Caveats)
	}
}

// TestARevokedPassportIsRejected.
func TestARevokedPassportIsRejected(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	res, err := Verify(p, VerifyOptions{Verifier: k, At: now, Revocations: []Revocation{
		{FindingID: "finding:f1", At: now, Reason: "the discharge survey was withdrawn"},
	}})
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("a revoked passport verified: %v", err)
	}
	if res.Trustworthy() {
		t.Fatal("a revoked passport was trustworthy")
	}
	if res.RevocationReason == "" {
		t.Fatal("the revocation reason was not reported")
	}
}

// TestAnExpiredPassportIsRejected.
func TestAnExpiredPassportIsRejected(t *testing.T) {
	k := newKP(t)
	expiry := now.Add(24 * time.Hour)
	p, err := Issue(IssueRequest{Finding: finding(t), Qualification: "ASSURED",
		IssuedAt: now, ExpiresAt: &expiry, Signer: k})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(p, VerifyOptions{Verifier: k, At: now.Add(48 * time.Hour)}); !errors.Is(err, ErrExpired) {
		t.Fatalf("an expired passport verified: %v", err)
	}
	if _, err := Verify(p, VerifyOptions{Verifier: k, At: now.Add(time.Hour)}); err != nil {
		t.Fatalf("a live passport was refused: %v", err)
	}
}

// TestThePassportDoesNotCarryTheEvidence. It is a certificate, not a
// bundle; carrying the material would make every verification an
// exercise in redistributing licensed data.
func TestThePassportDoesNotCarryTheEvidence(t *testing.T) {
	p := issue(t, newKP(t))
	if p.Payload.EvidenceRoot == "" {
		t.Fatal("the passport carries no evidence root, so a holder cannot confirm the set")
	}
	rendered := p.Render()
	for _, ref := range []string{"ev:survey", "ev:bl"} {
		if strings.Contains(rendered, ref) {
			t.Errorf("the passport carries the evidence reference %s", ref)
		}
	}
}

// TestTheWeakestDimensionIsSurfacedOnVerification.
func TestTheWeakestDimensionIsSurfacedOnVerification(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	res, err := Verify(p, VerifyOptions{Verifier: k, At: now})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range res.Caveats {
		if strings.Contains(c, "weakest confidence dimension is CAUSAL") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the weak causal dimension was not surfaced: %v", res.Caveats)
	}
}

// TestIssuanceIsDeterministic, so two issues of the same finding at
// the same instant produce the same digest.
func TestIssuanceIsDeterministic(t *testing.T) {
	k := newKP(t)
	a := issue(t, k)
	for i := 0; i < 20; i++ {
		b := issue(t, k)
		if a.Digest != b.Digest {
			t.Fatalf("two issues differ: %s vs %s", a.Digest, b.Digest)
		}
	}
}

// TestAnUnknownSchemaIsRefused, so a future payload shape cannot be
// verified by a reader that does not understand it.
func TestAnUnknownSchemaIsRefused(t *testing.T) {
	k := newKP(t)
	p := issue(t, k)
	p.Payload.Schema = "veriqo.passport/v99"
	if _, err := Verify(p, VerifyOptions{Verifier: k, At: now}); err == nil {
		t.Fatal("a passport with an unknown schema verified")
	}
}
