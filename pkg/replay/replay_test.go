package replay

import (
	"encoding/json"
	"errors"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

func caseInput() canonical.CaseInput {
	return canonical.CaseInput{
		Entity: digitaltwin.EntityID("VESSEL:IMO7777777"), Subject: "VESSEL:IMO7777777",
		Predicate: "AIS_STATUS", Tick: 42, Policy: risk.DefaultPolicy(),
		PatternScore: 0.6, PriceAnomaly: 0.3,
		EconomicChannels: map[string]float64{"commodity_flow_usd": 2_000_000},
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-a"), Value: "OFF", BaseReliability: 0.85, Provider: "sat-1", UpstreamID: "feed-1"},
			{SourceID: fusion.SourceID("ais-b"), Value: "OFF", BaseReliability: 0.8, Provider: "sat-2"},
			{SourceID: fusion.SourceID("port"), Value: "ON", BaseReliability: 0.95},
		},
	}
}

func runAndRecord(t *testing.T) (ExecutionRecord, *canonical.CanonicalResult) {
	t.Helper()
	p := canonical.NewPipeline(nil)
	in := caseInput()
	res, err := p.RunCanonical("auditor-original", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	rec, err := Record("auditor-original", in, res, p.Dependencies.ReplayAll(), "identity-ledger-head-fixture")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	return rec, res
}

func TestFullLifecycleReplayMatches(t *testing.T) {
	rec, _ := runAndRecord(t)
	pkg, err := NewReplayPackage("independent-auditor", "nonce-1", rec)
	if err != nil {
		t.Fatalf("NewReplayPackage: %v", err)
	}
	cert, err := NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("independent full-lifecycle replay did not match: %v", err)
	}
	if cert.StagesCompared != len(StageOrder) {
		t.Fatalf("expected %d stages compared, got %d", len(StageOrder), cert.StagesCompared)
	}
}

// TestReplayIdentitiesAreAllDistinct is the literal PHASE 10
// requirement: ReplayID must no longer be the certificate hash.
func TestReplayIdentitiesAreAllDistinct(t *testing.T) {
	rec, canonRes := runAndRecord(t)
	pkg, _ := NewReplayPackage("auditor-a", "n1", rec)
	cert, err := NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	ids := map[string]string{
		"ExecutionID":               cert.ExecutionID,
		"EvidencePackageID":         cert.EvidencePackageID,
		"ReplayPackageID":           cert.ReplayPackageID,
		"OriginalResultHash":        cert.OriginalResultHash,
		"VerificationCertificateID": cert.VerificationCertificateID,
	}
	seen := map[string]string{}
	for name, id := range ids {
		if id == "" {
			t.Fatalf("%s is empty", name)
		}
		if other, dup := seen[id]; dup {
			t.Fatalf("%s and %s share an identity — PHASE 10 violated", name, other)
		}
		seen[id] = name
	}
	if cert.ExecutionID == canonRes.Certificate.Hash {
		t.Fatal("ExecutionID equals the canonical certificate hash — identities not separated")
	}
	// Two auditors replaying the same execution get distinct replay
	// package IDs but agree on the execution identity.
	pkg2, _ := NewReplayPackage("auditor-b", "n2", rec)
	if pkg2.ReplayPackageID == pkg.ReplayPackageID {
		t.Fatal("two independent replay requests share a ReplayPackageID")
	}
	cert2, _ := NewEngine().Replay(pkg2)
	if cert2.ExecutionID != cert.ExecutionID {
		t.Fatal("two replays of one execution disagree on ExecutionID")
	}
	if cert2.VerificationCertificateID == cert.VerificationCertificateID {
		t.Fatal("two distinct verifications produced one certificate identity")
	}
}

func TestReplayDetectsTamperedEvidenceAndLocatesStage(t *testing.T) {
	rec, _ := runAndRecord(t)
	// An attacker flips a source's asserted value in the recorded
	// package, hoping the replay rubber-stamps it.
	rec.CaseInput.Submissions[2].Value = "OFF"
	pkg, _ := NewReplayPackage("auditor", "n", rec)
	cert, err := NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if cert.Match {
		t.Fatal("tampered evidence replayed as matching")
	}
	if err := cert.Assert(); !errors.Is(err, ErrExecutionMismatch) {
		t.Fatalf("expected execution mismatch, got %v", err)
	}
	if cert.DivergedStage == "" {
		t.Fatal("divergence not localised to a stage")
	}
}

func TestReplayRejectsTamperedDependencyLedger(t *testing.T) {
	rec, _ := runAndRecord(t)
	if len(rec.DependencyLedger) == 0 {
		t.Skip("case declared no dependencies")
	}
	rec.DependencyLedger[0].Confidence = 0.01
	pkg, _ := NewReplayPackage("auditor", "n", rec)
	if _, err := NewEngine().Replay(pkg); !errors.Is(err, ErrLedgerTampered) {
		t.Fatalf("tampered dependency ledger accepted: %v", err)
	}
}

func TestReplayPackageWireFormatIsTamperEvident(t *testing.T) {
	rec, _ := runAndRecord(t)
	pkg, _ := NewReplayPackage("auditor", "n", rec)
	raw, err := pkg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.ReplayPackageID != pkg.ReplayPackageID {
		t.Fatal("wire round-trip changed the package identity")
	}
	tampered := pkg
	tampered.RequestedBy = "someone-else"
	rawT, _ := tampered.Marshal()
	if _, err := Unmarshal(rawT); !errors.Is(err, ErrPackageTampered) {
		t.Fatal("tampered replay package accepted")
	}
}

func TestVerificationCertificateIsTamperEvident(t *testing.T) {
	rec, _ := runAndRecord(t)
	pkg, _ := NewReplayPackage("auditor", "n", rec)
	cert, _ := NewEngine().Replay(pkg)
	cert.Match = false // forge a failure into a passing verdict's twin
	if err := VerifyCertificate(cert); !errors.Is(err, ErrCertTampered) {
		t.Fatal("tampered verification certificate accepted")
	}
}

func TestReplayIsDeterministicAcrossEngines(t *testing.T) {
	rec, _ := runAndRecord(t)
	pkg, _ := NewReplayPackage("auditor", "n", rec)
	a, err := NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay a: %v", err)
	}
	b, err := NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay b: %v", err)
	}
	if a.ReplayResultHash != b.ReplayResultHash {
		t.Fatal("two independent replay engines produced different results")
	}
}

func TestEmptyExecutionRejected(t *testing.T) {
	if _, err := NewEngine().Replay(ReplayPackage{}); !errors.Is(err, ErrEmptyPackage) {
		t.Fatalf("empty replay package accepted: %v", err)
	}
	if _, err := Record("a", canonical.CaseInput{}, nil, nil, ""); !errors.Is(err, ErrEmptyPackage) {
		t.Fatalf("empty execution recorded: %v", err)
	}
}

// TestColdReplayPreservesIdentityLedgerHeadByteForByte is the P0-E
// follow-up acceptance test (audit follow-up: "identity ledger belum
// ikut masuk ke ReplayRequest" -- the identity ledger hadn't yet made
// it into the replay request). It proves IdentityLedgerHead survives
// the EXACT cold-replay path the package doc promises: serialize to
// bytes (Marshal), destroy every in-memory reference, reconstruct from
// bytes alone (json.Unmarshal, mirroring what an independent process
// starting from ReplayPackage.Marshal's output would do), and read the
// field back unchanged -- byte-for-byte, not merely "non-empty".
func TestColdReplayPreservesIdentityLedgerHeadByteForByte(t *testing.T) {
	rec, _ := runAndRecord(t)
	if rec.IdentityLedgerHead == "" {
		t.Fatal("test fixture assumption violated: runAndRecord must produce a non-empty IdentityLedgerHead")
	}
	pkg, err := NewReplayPackage("independent-auditor", "nonce-cold", rec)
	if err != nil {
		t.Fatalf("NewReplayPackage: %v", err)
	}

	// Cold path: serialize to bytes, then rebuild EVERYTHING from those
	// bytes alone -- no reference to pkg, rec, or any in-memory value
	// above survives past this point.
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var coldPkg ReplayPackage
	if err := json.Unmarshal(raw, &coldPkg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if coldPkg.Execution.IdentityLedgerHead != rec.IdentityLedgerHead {
		t.Fatalf("IdentityLedgerHead did not survive cold serialization: got %q, want %q",
			coldPkg.Execution.IdentityLedgerHead, rec.IdentityLedgerHead)
	}

	// The identity ledger head must also be part of what an independent
	// replay actually verifies, not a field that merely rides along
	// unexamined: a package whose IdentityLedgerHead was tampered with
	// after signing must not silently verify as a match.
	cert, err := NewEngine().Replay(coldPkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("cold replay of an untampered package should match: %v", err)
	}
}
