package timestamp

import (
	"errors"
	"strings"
	"testing"
)

const veriqo = "veriqo-operations-ltd"

func chain(t *testing.T, digest string, seq uint64, prior string) ChainAttestation {
	t.Helper()
	c, err := NewChainAttestation(digest, seq, prior, veriqo)
	if err != nil {
		t.Fatalf("NewChainAttestation: %v", err)
	}
	return c
}

func token(digest, operator string) *ExternalAttestation {
	return &ExternalAttestation{
		Digest: digest,
		Authority: TSA{Name: "DigiCert Timestamp 2024", PolicyOID: "2.16.840.1.114412.7.1",
			CertificateFingerprint: "aa:bb", OperatorID: operator},
		SerialNumber: "0x2f", GenTimeUnix: 1_700_000_000, AccuracySeconds: 1,
		Token: []byte{0x30, 0x82}, Ordering: true,
	}
}

// TestZeroKindIsNone is the honest default: a record with no
// attestation must not read as if it has the strongest one.
func TestZeroKindIsNone(t *testing.T) {
	var k Kind
	if k != None || k.String() != "NONE" {
		t.Fatalf("the zero Kind must be NONE, got %s", k)
	}
	if k.ProvesExistenceBefore() || k.ProvesOrdering() {
		t.Fatal("NONE proves nothing")
	}
}

// TestOnlyIndependentAttestationProvesExistenceBefore is the single
// distinction this whole package exists to hold.
func TestOnlyIndependentAttestationProvesExistenceBefore(t *testing.T) {
	if TamperEvidentChain.ProvesExistenceBefore() {
		t.Fatal("a tamper-evident chain must never claim to prove existence before a time")
	}
	if !IndependentAttestation.ProvesExistenceBefore() {
		t.Fatal("an independent attestation does prove existence before a time")
	}
	if !TamperEvidentChain.ProvesOrdering() {
		t.Fatal("a chain does prove ordering")
	}
}

// TestChainAloneDoesNotProveExistenceBefore is the same rule at the
// attestation level, which is where the rest of the system asks.
func TestChainAloneDoesNotProveExistenceBefore(t *testing.T) {
	a, err := Assess("digest-1", ptr(chain(t, "digest-1", 0, "")), nil, []string{veriqo, "claimant-ltd"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Kind() != TamperEvidentChain {
		t.Fatalf("expected TAMPER_EVIDENT_CHAIN, got %s", a.Kind())
	}
	if _, ok := a.ProvesExistenceBefore(); ok {
		t.Fatal("a chain-only attestation must not prove existence before a time")
	}
	if err := RequireIndependent(a); !errors.Is(err, ErrNotIndependent) {
		t.Fatalf("expected ErrNotIndependent, got %v", err)
	}
}

func TestExternalTokenFromAThirdPartyIsIndependent(t *testing.T) {
	a, err := Assess("digest-1", ptr(chain(t, "digest-1", 0, "")), token("digest-1", "digicert-inc"),
		[]string{veriqo, "claimant-ltd", "respondent-sa"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Kind() != IndependentAttestation {
		t.Fatalf("expected INDEPENDENT_ATTESTATION, got %s", a.Kind())
	}
	bound, ok := a.ProvesExistenceBefore()
	if !ok || bound != 1_700_000_001 {
		t.Fatalf("expected an upper bound of genTime+accuracy, got %d/%v", bound, ok)
	}
	if err := RequireIndependent(a); err != nil {
		t.Fatalf("RequireIndependent: %v", err)
	}
}

// TestSelfOperatedTSAIsNotIndependent is the adversarial case the
// backlog names: running your own TSA does not make you a third party.
func TestSelfOperatedTSAIsNotIndependent(t *testing.T) {
	a, err := Assess("digest-1", ptr(chain(t, "digest-1", 0, "")), token("digest-1", veriqo),
		[]string{veriqo, "claimant-ltd"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Kind() == IndependentAttestation {
		t.Fatal("a TSA operated by VERIQO itself must not yield an independent attestation")
	}
	if a.NotIndependentReason() == "" {
		t.Fatal("the downgrade must be explained, not silent")
	}
	if _, ok := a.ProvesExistenceBefore(); ok {
		t.Fatal("a self-operated TSA must not prove existence before a time")
	}
	if !strings.Contains(Describe(a), "not counted") {
		t.Fatalf("Describe must disclose the uncounted token, got %q", Describe(a))
	}
}

// TestPartyOperatedTSAIsNotIndependent covers the same defect on the
// other side: a claimant's own timestamping service.
func TestPartyOperatedTSAIsNotIndependent(t *testing.T) {
	a, _ := Assess("digest-1", nil, token("digest-1", "claimant-ltd"), []string{veriqo, "claimant-ltd"})
	if a.Kind() == IndependentAttestation {
		t.Fatal("a TSA operated by a party to the matter is not independent")
	}
	if err := RequireIndependent(a); err == nil {
		t.Fatal("RequireIndependent must refuse it")
	}
}

func TestIndependenceCheckIsCaseInsensitiveAndTrimmed(t *testing.T) {
	tsa := TSA{Name: "n", OperatorID: " Claimant-LTD "}
	if tsa.IndependentOf("claimant-ltd") {
		t.Fatal("independence must not be evaded by case or whitespace")
	}
	if !tsa.IndependentOf("someone-else", "") {
		t.Fatal("an unrelated operator is independent, and an empty party is ignored")
	}
}

// --- Chain integrity -------------------------------------------------

func TestVerifyChainAcceptsAnUnbrokenRun(t *testing.T) {
	c0 := chain(t, "d0", 0, "")
	c1 := chain(t, "d1", 1, c0.EntryHash)
	c2 := chain(t, "d2", 2, c1.EntryHash)
	if err := VerifyChain([]ChainAttestation{c0, c1, c2}); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestVerifyChainDetectsAnAlteredEntry(t *testing.T) {
	c0 := chain(t, "d0", 0, "")
	c1 := chain(t, "d1", 1, c0.EntryHash)
	c1.Digest = "d1-tampered"
	if err := VerifyChain([]ChainAttestation{c0, c1}); err == nil {
		t.Fatal("an altered entry must be detected")
	}
}

func TestVerifyChainDetectsABrokenLink(t *testing.T) {
	c0 := chain(t, "d0", 0, "")
	orphan := chain(t, "d1", 1, "some-other-hash")
	if err := VerifyChain([]ChainAttestation{c0, orphan}); err == nil {
		t.Fatal("a broken link must be detected")
	}
}

func TestVerifyChainDetectsASequenceGap(t *testing.T) {
	c0 := chain(t, "d0", 0, "")
	c2 := chain(t, "d2", 2, c0.EntryHash)
	if err := VerifyChain([]ChainAttestation{c0, c2}); err == nil {
		t.Fatal("a sequence gap must be detected")
	}
}

func TestChainEntryRequiresOperatorAndPrior(t *testing.T) {
	if _, err := NewChainAttestation("d", 0, "", ""); !errors.Is(err, ErrNoChainOperator) {
		t.Fatalf("expected ErrNoChainOperator, got %v", err)
	}
	if _, err := NewChainAttestation("d", 5, "", veriqo); !errors.Is(err, ErrNoPriorHash) {
		t.Fatalf("a non-genesis entry needs a prior hash, got %v", err)
	}
	if _, err := NewChainAttestation("", 0, "", veriqo); !errors.Is(err, ErrNoDigest) {
		t.Fatalf("expected ErrNoDigest, got %v", err)
	}
}

// --- Digest binding --------------------------------------------------

// TestAttestationMustCoverTheDatumItIsClaimedFor stops an attestation
// for one document being presented for another.
func TestAttestationMustCoverTheDatumItIsClaimedFor(t *testing.T) {
	if _, err := Assess("digest-1", ptr(chain(t, "digest-2", 0, "")), nil, nil); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for the chain, got %v", err)
	}
	if _, err := Assess("digest-1", nil, token("digest-2", "digicert-inc"), nil); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for the token, got %v", err)
	}
}

func TestMalformedTokenIsRefused(t *testing.T) {
	tk := token("digest-1", "digicert-inc")
	tk.Token = nil
	if _, err := Assess("digest-1", nil, tk, nil); !errors.Is(err, ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}

	tk2 := token("digest-1", "digicert-inc")
	tk2.Authority.OperatorID = ""
	if _, err := Assess("digest-1", nil, tk2, nil); !errors.Is(err, ErrNoTSA) {
		t.Fatalf("a token with no named operator must be refused, got %v", err)
	}
}

func TestNoAttestationAtAll(t *testing.T) {
	a, err := Assess("digest-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if a.Kind() != None {
		t.Fatalf("expected NONE, got %s", a.Kind())
	}
	if !strings.Contains(Describe(a), "No temporal attestation") {
		t.Fatalf("Describe must say so plainly, got %q", Describe(a))
	}
}

// TestDescribeNeverOverstates checks the sentence the reports quote.
func TestDescribeNeverOverstates(t *testing.T) {
	chainOnly, _ := Assess("d", ptr(chain(t, "d", 0, "")), nil, nil)
	got := Describe(chainOnly)
	if strings.Contains(strings.ToLower(got), "existed before") {
		t.Fatalf("a chain description must not claim existence before a time: %q", got)
	}
	if !strings.Contains(got, "No independent attestation") {
		t.Fatalf("a chain description must state what is absent: %q", got)
	}

	independent, _ := Assess("d", nil, token("d", "digicert-inc"), []string{veriqo})
	if !strings.Contains(Describe(independent), "existed before") {
		t.Fatalf("an independent description should state the assertion: %q", Describe(independent))
	}
}

// TestAccuracyWidensTheBoundRatherThanNarrowingIt: a TSA's stated
// accuracy makes the assertion weaker, never stronger.
func TestAccuracyWidensTheBound(t *testing.T) {
	tk := token("d", "digicert-inc")
	tk.AccuracySeconds = 60
	a, _ := Assess("d", nil, tk, nil)
	bound, ok := a.ProvesExistenceBefore()
	if !ok || bound != tk.GenTimeUnix+60 {
		t.Fatalf("the bound must include the accuracy window, got %d", bound)
	}
	if bound < tk.GenTimeUnix {
		t.Fatal("accuracy must never tighten the bound below genTime")
	}
}

func ptr(c ChainAttestation) *ChainAttestation { return &c }
