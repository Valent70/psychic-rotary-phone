package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"veriqo/internal/assurance"
	"veriqo/internal/timestamp"
	"veriqo/pkg/execution"
)

// TestCategoryHashChangesWhenAMemberGateEvidenceChanges is the
// adversarial test audit item P0-01 asked for: a category hash
// (TestManifestHash, SecurityManifestHash, ...) must actually bind to
// its member gates' evidence, not sit as a constant regardless of what
// those gates produced.
func TestCategoryHashChangesWhenAMemberGateEvidenceChanges(t *testing.T) {
	base := map[string]string{"unit": "hash-a", "vet": "hash-b", "chaos": "hash-c"}
	h1 := categoryHash(base, "unit", "vet")

	tampered := map[string]string{"unit": "hash-a-TAMPERED", "vet": "hash-b", "chaos": "hash-c"}
	h2 := categoryHash(tampered, "unit", "vet")

	if h1 == h2 {
		t.Fatal("categoryHash must change when a member gate's evidence hash changes")
	}
}

// TestCategoryHashIsOrderIndependent confirms the hash depends on gate
// content, not on registration/iteration order -- required since Go
// map iteration order is randomized.
func TestCategoryHashIsOrderIndependent(t *testing.T) {
	gates := map[string]string{"a": "1", "b": "2", "c": "3"}
	h1 := categoryHash(gates, "a", "b", "c")
	h2 := categoryHash(gates, "c", "a", "b")
	if h1 != h2 {
		t.Fatal("categoryHash must be independent of the order gate IDs are listed in")
	}
}

// TestCategoryHashIgnoresGatesOutsideItsCategory confirms a category
// hash is NOT simply "hash everything" -- unrelated gate evidence must
// not affect it, or every category field would collapse to the same
// value and the audit's requested per-category binding would be fake.
func TestCategoryHashIgnoresGatesOutsideItsCategory(t *testing.T) {
	gates := map[string]string{"unit": "hash-a", "vet": "hash-b", "chaos": "hash-c"}
	withoutChaosChange := categoryHash(gates, "unit", "vet")

	gatesChaosChanged := map[string]string{"unit": "hash-a", "vet": "hash-b", "chaos": "DIFFERENT"}
	withChaosChanged := categoryHash(gatesChaosChanged, "unit", "vet")

	if withoutChaosChange != withChaosChanged {
		t.Fatal("categoryHash for {unit,vet} must not change when only the unrelated chaos gate's evidence changes")
	}
}

// TestCategoryHashSkipsMissingGatesRatherThanErroring confirms a
// category whose member gate didn't run (e.g. -skip-race) still
// produces a hash over whatever did run, rather than panicking or
// silently returning empty.
func TestCategoryHashSkipsMissingGatesRatherThanErroring(t *testing.T) {
	gates := map[string]string{"unit": "hash-a"}
	h := categoryHash(gates, "unit", "race") // "race" absent
	if h == "" {
		t.Fatal("expected a real hash even when one requested gate ID has no evidence")
	}
	solo := categoryHash(gates, "unit")
	if h != solo {
		t.Fatal("a missing gate ID must be omitted, not folded in as an empty placeholder that changes the hash")
	}
}

// TestEvidenceRootHashChangesWhenAnyGateChanges confirms the root hash
// -- the audit's requested single "prove the whole evidence set with
// one comparison" field -- actually depends on every gate, not just a
// subset.
func TestEvidenceRootHashChangesWhenAnyGateChanges(t *testing.T) {
	gates := map[string]string{"unit": "a", "vet": "b", "chaos": "c", "replay": "d"}
	root1 := evidenceRootHash(gates)

	for _, tamperKey := range []string{"unit", "vet", "chaos", "replay"} {
		tampered := map[string]string{"unit": "a", "vet": "b", "chaos": "c", "replay": "d"}
		tampered[tamperKey] = "TAMPERED"
		if evidenceRootHash(tampered) == root1 {
			t.Fatalf("evidenceRootHash did not change when gate %q's evidence was tampered", tamperKey)
		}
	}
}

// TestBinaryIdentityProducesRealHashAndBuildID exercises the actual
// build: compiles cmd/veriqo-node for real and confirms both a
// deterministic-length hash and a non-empty Go build ID come back.
// Skips if the go toolchain (or `go tool buildid`) is unavailable in
// this environment rather than failing the whole suite over a
// secondary identity field.
func TestBinaryIdentityProducesRealHashAndBuildID(t *testing.T) {
	binaryHash, buildID := binaryIdentity()
	if binaryHash == "" {
		t.Skip("go build ./cmd/veriqo-node did not succeed in this environment; binaryIdentity degrades to empty by design")
	}
	if len(binaryHash) != 64 { // hex-encoded sha256
		t.Fatalf("expected a 64-char hex sha256, got %d chars: %q", len(binaryHash), binaryHash)
	}
	if buildID == "" {
		t.Error("expected a non-empty Go build ID alongside a successful binary hash")
	}
}

// TestBuildEngineRegistryReflectsRealGateResults confirms the audit's
// central P0-05 requirement: no engine may claim VERIFIED unless the
// gates that would prove it actually passed on THIS run.
func TestBuildEngineRegistryReflectsRealGateResults(t *testing.T) {
	allPassed := map[string]bool{"execution_graph": true, "replay_determinism_100x": true}
	reg := buildEngineRegistry(allPassed)
	if len(reg.Engines) == 0 {
		t.Fatal("expected at least one engine entry derived from execution.Registry()")
	}

	sawVerified, sawSkippedTemporal := false, false
	for _, row := range reg.Engines {
		if row.EngineID == string(execution.StageTemporal) {
			sawSkippedTemporal = true
			if contains(row.Status, "VERIFIED") || contains(row.Status, "INTEGRATED") {
				t.Errorf("StageTemporal is unconditionally skipped in the current wiring and must never claim INTEGRATED/VERIFIED, got %v", row.Status)
			}
			if !contains(row.Status, "IMPLEMENTED") {
				t.Error("StageTemporal is implemented (pkg/moat/hbayes exists) even though it isn't wired in -- must still say IMPLEMENTED")
			}
		} else if contains(row.Status, "VERIFIED") {
			sawVerified = true
		}
	}
	if !sawSkippedTemporal {
		t.Fatal("expected StageTemporal to be present in the registry")
	}
	if !sawVerified {
		t.Fatal("expected at least one non-skipped stage to reach VERIFIED when both backing gates passed")
	}
}

// TestBuildEngineRegistryNeverClaimsVerifiedOnAFailedGate is the
// adversarial case: if either backing gate failed on this run, NO
// engine may claim VERIFIED, regardless of how many prior runs passed.
func TestBuildEngineRegistryNeverClaimsVerifiedOnAFailedGate(t *testing.T) {
	cases := []map[string]bool{
		{"execution_graph": false, "replay_determinism_100x": true},
		{"execution_graph": true, "replay_determinism_100x": false},
		{"execution_graph": false, "replay_determinism_100x": false},
		{}, // gate never ran at all (e.g. -skip-race-style omission)
	}
	for _, gatePassed := range cases {
		reg := buildEngineRegistry(gatePassed)
		for _, row := range reg.Engines {
			if contains(row.Status, "VERIFIED") {
				t.Fatalf("engine %s claimed VERIFIED with gatePassed=%v -- must require both gates to actually pass this run", row.EngineID, gatePassed)
			}
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func genTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

// TestAttestTimestampCreatesAndExtendsAPersistedChain is P1-09's core
// property: repeated readiness runs must chain their timestamp entries
// onto the SAME persisted log, not each start a fresh disconnected
// chain -- otherwise the log could never prove ordering across runs.
func TestAttestTimestampCreatesAndExtendsAPersistedChain(t *testing.T) {
	pub, priv := genTestKey(t)
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "timestamp-chain.json")

	release1 := assurance.ReleaseCertificate{CertificateHash: "cert-hash-run-1"}
	release1, err := attestTimestamp(release1, chainPath, priv, "tsa-1", 1000)
	if err != nil {
		t.Fatalf("attestTimestamp (first run): %v", err)
	}
	if release1.TimestampChainSeq != 1 {
		t.Fatalf("first attestation must be seq 1, got %d", release1.TimestampChainSeq)
	}

	release2 := assurance.ReleaseCertificate{CertificateHash: "cert-hash-run-2"}
	release2, err = attestTimestamp(release2, chainPath, priv, "tsa-1", 2000)
	if err != nil {
		t.Fatalf("attestTimestamp (second run): %v", err)
	}
	if release2.TimestampChainSeq != 2 {
		t.Fatalf("second attestation must chain onto the first as seq 2, got %d", release2.TimestampChainSeq)
	}

	raw, err := os.ReadFile(chainPath)
	if err != nil {
		t.Fatalf("read persisted chain: %v", err)
	}
	var chain []timestamp.Entry
	if err := json.Unmarshal(raw, &chain); err != nil {
		t.Fatalf("unmarshal persisted chain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("persisted chain must hold both entries, got %d", len(chain))
	}
	if chain[0].Subject != "cert-hash-run-1" || chain[1].Subject != "cert-hash-run-2" {
		t.Fatalf("persisted entries must attest to the actual certificate hash of their own run: %+v", chain)
	}
	if err := timestamp.VerifyChain(chain, pub); err != nil {
		t.Fatalf("a genuinely well-formed persisted chain must verify: %v", err)
	}
}

// TestAttestTimestampRefusesToBackdateAcrossRuns proves the chain-wide
// monotonicity guarantee survives being re-loaded from disk between
// runs, not just within a single in-memory Authority.
func TestAttestTimestampRefusesToBackdateAcrossRuns(t *testing.T) {
	_, priv := genTestKey(t)
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "timestamp-chain.json")

	if _, err := attestTimestamp(assurance.ReleaseCertificate{CertificateHash: "a"}, chainPath, priv, "tsa-1", 5000); err != nil {
		t.Fatalf("attestTimestamp (first run): %v", err)
	}
	if _, err := attestTimestamp(assurance.ReleaseCertificate{CertificateHash: "b"}, chainPath, priv, "tsa-1", 100); err == nil {
		t.Fatal("a second run claiming an earlier timestamp than the persisted chain's head must be refused")
	}
}

// TestAttestTimestampChainDetectsTamperingOfAPersistedEntry is the
// adversarial case an operator with disk access to evidence/ but not
// the timestamp-authority private key would attempt: editing an
// already-persisted entry's timestamp to backdate history. It must be
// caught by VerifyChain even though attestTimestamp itself only reads
// the tail, not the whole chain's content.
func TestAttestTimestampChainDetectsTamperingOfAPersistedEntry(t *testing.T) {
	pub, priv := genTestKey(t)
	dir := t.TempDir()
	chainPath := filepath.Join(dir, "timestamp-chain.json")

	if _, err := attestTimestamp(assurance.ReleaseCertificate{CertificateHash: "a"}, chainPath, priv, "tsa-1", 1000); err != nil {
		t.Fatalf("attestTimestamp: %v", err)
	}
	raw, err := os.ReadFile(chainPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var chain []timestamp.Entry
	if err := json.Unmarshal(raw, &chain); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	chain[0].Timestamp = 1 // attacker backdates near-epoch without the private key
	if err := timestamp.VerifyChain(chain, pub); err == nil {
		t.Fatal("a tampered persisted entry must fail VerifyChain")
	}
}
