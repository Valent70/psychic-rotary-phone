package assurance

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
)

func passingEvidence(gate string) Evidence {
	return NewEvidence(gate, "go test ./...", "ok veriqo/pkg/canonical", 0, 100)
}

func regWithOneMandatory(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(Gate{ID: "unit", Description: "unit tests", Mandatory: true,
		RequiredStatus: StatusVerified, OwnerPackage: "./...", ExitCriteria: "go test ./... passes"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

// TestDeclaredStatusWithoutEvidenceDegrades is the anti-false-green core.
func TestDeclaredStatusWithoutEvidenceDegrades(t *testing.T) {
	r := regWithOneMandatory(t)
	g, _ := r.Get("unit")
	g.Status = StatusProductionReady // a human types the word
	if got := g.EffectiveStatus(); got == StatusProductionReady {
		t.Fatal("a gate declared PRODUCTION_READY with no evidence was believed")
	}
}

func TestGatePassesOnlyWithVerifiableEvidence(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Attach("unit", StatusVerified, passingEvidence("unit")); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if a := r.Assess(); a.Verdict != VerdictProductionReady {
		t.Fatalf("expected PRODUCTION_READY, got %s: %v", a.Verdict, a.Reasons)
	}
}

func TestTamperedEvidenceRejected(t *testing.T) {
	r := regWithOneMandatory(t)
	ev := passingEvidence("unit")
	ev.Output = "ok everything is fine" // edited after the fact
	if err := r.Attach("unit", StatusVerified, ev); err == nil {
		t.Fatal("tampered evidence artifact accepted")
	}
}

func TestFailingCommandNeverPasses(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Attach("unit", StatusVerified, NewEvidence("unit", "go test ./...", "FAIL", 1, 100)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if a := r.Assess(); a.Verdict == VerdictProductionReady {
		t.Fatal("a non-zero exit code produced a passing gate")
	}
}

// TestOneCriticalGapCannotBeCompensated is PHASE 54 verbatim: 100
// passing gates do not buy one failing security gate.
func TestOneCriticalGapCannotBeCompensated(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 100; i++ {
		id := "gate-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := r.Register(Gate{ID: id, Mandatory: true, RequiredStatus: StatusVerified,
			ExitCriteria: "passes"}); err != nil {
			t.Fatalf("Register: %v", err)
		}
		if err := r.Attach(id, StatusVerified, passingEvidence(id)); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	if err := r.Register(Gate{ID: "pentest", Mandatory: true, RequiredStatus: StatusQualified,
		ExitCriteria: "independent third-party penetration test signed off"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Block("pentest", "requires an external security vendor"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	a := r.Assess()
	if a.Verdict != VerdictNotProductionReady {
		t.Fatalf("100/101 gates produced %s — scoring compensated a critical gap", a.Verdict)
	}
	if len(a.BlockedMandatory) != 1 || a.BlockedMandatory[0] != "pentest" {
		t.Fatalf("blocking gate not named: %+v", a.BlockedMandatory)
	}
	joined := strings.Join(a.Reasons, "\n")
	if !strings.Contains(joined, "external security vendor") {
		t.Fatal("blocker reason not surfaced")
	}
}

func TestBlockedRequiresANamedBlocker(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Block("unit", "   "); err == nil {
		t.Fatal("BLOCKED accepted without naming a blocker")
	}
}

func TestWaiverNeverYieldsProductionReady(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Waive("unit", Waiver{Owner: "cto", Justification: "accepted for pilot", ExpiresAtTick: 999, ApprovedBy: "board"}); err != nil {
		t.Fatalf("Waive: %v", err)
	}
	a := r.Assess()
	if a.Verdict != VerdictConditional {
		t.Fatalf("waived mandatory gate produced %s", a.Verdict)
	}
	if a.Verdict == VerdictProductionReady {
		t.Fatal("waiver laundered into PRODUCTION_READY")
	}
}

func TestIncompleteWaiverRejected(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Waive("unit", Waiver{Owner: "cto"}); err == nil {
		t.Fatal("waiver without justification/expiry accepted")
	}
}

func TestEmptyRegistryIsNeverReady(t *testing.T) {
	if a := NewRegistry().Assess(); a.Verdict != VerdictNotProductionReady {
		t.Fatalf("an empty gate set reported %s", a.Verdict)
	}
}

func TestAcceptanceManifestGovernance(t *testing.T) {
	m := AcceptanceManifest{
		Categories: []AcceptanceCategory{
			{Name: "normal", Minimum: 10, Actual: 20},
			{Name: "adversarial", Minimum: 10, Actual: 20},
		},
		MandatoryTests: []string{"TestUnifiedIntentTrustDecisionLifecycle"},
		PresentTests:   []string{"TestUnifiedIntentTrustDecisionLifecycle"},
		TotalMinimum:   40,
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("healthy manifest rejected: %v", err)
	}
	shrunk := m
	shrunk.Categories = []AcceptanceCategory{{Name: "normal", Minimum: 10, Actual: 5}, {Name: "adversarial", Minimum: 10, Actual: 20}}
	if err := shrunk.Validate(); err == nil {
		t.Fatal("category below minimum accepted")
	}
	gone := m
	gone.Categories = []AcceptanceCategory{{Name: "normal", Minimum: 10, Actual: 20}, {Name: "adversarial", Minimum: 10, Actual: 0}}
	if err := gone.Validate(); err == nil {
		t.Fatal("disappeared category accepted")
	}
	deleted := m
	deleted.PresentTests = nil
	if err := deleted.Validate(); err == nil {
		t.Fatal("deleted mandatory central test accepted")
	}
}

func TestReleaseCertificateVerdictCannotBeForged(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Block("unit", "toolchain unavailable"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	cert := ReleaseCertificate{Version: "v7.11.0", Verdict: VerdictProductionReady}
	final := cert.Finalize(r.Assess())
	if final.Verdict != VerdictNotProductionReady {
		t.Fatalf("a hand-set verdict survived Finalize: %s", final.Verdict)
	}
}

func TestReleaseCertificateSignatureAndTamperDetection(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Attach("unit", StatusVerified, passingEvidence("unit")); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	cert := ReleaseCertificate{Version: "v7.11.0", GitCommit: "abc", GoVersion: "go1.22.2"}.Finalize(r.Assess()).Sign(priv, "release-key-1")
	if err := cert.Verify(); err != nil {
		t.Fatalf("signed certificate did not verify: %v", err)
	}
	tampered := cert
	tampered.Verdict = VerdictProductionReady
	tampered.Version = "v9.9.9"
	if err := tampered.Verify(); err == nil {
		t.Fatal("tampered release certificate verified")
	}
}

func TestVerifyTrustedRejectsAForgedKeypairEvenWhenSelfConsistent(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Attach("unit", StatusVerified, passingEvidence("unit")); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	realPub, realPriv, _ := ed25519.GenerateKey(nil)
	genuine := ReleaseCertificate{Version: "v7.12.1", GitCommit: "abc"}.
		Finalize(r.Assess()).Sign(realPriv, "release-key-1")
	trust := TrustedKeys{"release-key-1": realPub}

	if err := genuine.VerifyTrusted(trust); err != nil {
		t.Fatalf("a genuinely signed, trusted certificate must verify: %v", err)
	}

	// A forger cannot edit the certificate and re-sign with a fresh key
	// of their own: Verify() alone would accept this (it is internally
	// self-consistent), which is exactly the "Internal Gap III" the
	// v7.12.1 audit named. VerifyTrusted must not.
	_, forgedPriv, _ := ed25519.GenerateKey(nil)
	forged := ReleaseCertificate{Version: "v7.12.1", GitCommit: "abc", Verdict: VerdictProductionReady}.
		Finalize(r.Assess()).Sign(forgedPriv, "release-key-1")
	if err := forged.Verify(); err != nil {
		t.Fatalf("sanity: the forged certificate should be internally self-consistent, got %v", err)
	}
	if err := forged.VerifyTrusted(trust); !errors.Is(err, ErrManifestTampered) {
		t.Fatalf("a forged certificate signed with an unauthorized key must fail VerifyTrusted, got %v", err)
	}
}

func TestVerifyTrustedRejectsAnUnlistedKeyID(t *testing.T) {
	r := regWithOneMandatory(t)
	_, priv, _ := ed25519.GenerateKey(nil)
	cert := ReleaseCertificate{Version: "v7.12.1"}.Finalize(r.Assess()).Sign(priv, "unknown-key")
	if err := cert.VerifyTrusted(TrustedKeys{"release-key-1": priv.Public().(ed25519.PublicKey)}); !errors.Is(err, ErrUntrustedSigningKey) {
		t.Fatalf("expected ErrUntrustedSigningKey for an unlisted key id, got %v", err)
	}
}

func TestVerifyTrustedRejectsAnUnsignedCertificate(t *testing.T) {
	r := regWithOneMandatory(t)
	cert := ReleaseCertificate{Version: "v7.12.1"}.Finalize(r.Assess())
	if err := cert.VerifyTrusted(TrustedKeys{}); err == nil {
		t.Fatal("an unsigned certificate must never satisfy VerifyTrusted")
	}
}

func TestReadinessManifestRoundTrip(t *testing.T) {
	r := regWithOneMandatory(t)
	if err := r.Attach("unit", StatusVerified, passingEvidence("unit")); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	m := BuildReadinessManifest(r, AcceptanceManifest{
		Categories:     []AcceptanceCategory{{Name: "normal", Minimum: 1, Actual: 1}},
		MandatoryTests: []string{"X"}, PresentTests: []string{"X"}, TotalMinimum: 1,
	}, ReleaseCertificate{Version: "v7.11.0"})
	if m.Release.AcceptanceManifestHash != m.Acceptance.Hash || m.Acceptance.Hash == "" {
		t.Fatal("release certificate does not commit to the acceptance manifest")
	}
	if err := m.Release.Verify(); err != nil {
		t.Fatalf("manifest release certificate invalid: %v", err)
	}
	if _, err := m.JSON(); err != nil {
		t.Fatalf("JSON: %v", err)
	}
}
