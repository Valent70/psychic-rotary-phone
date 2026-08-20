package verification

import (
	"testing"
)

func sampleEvidencePackage() EvidencePackage {
	return EvidencePackage{
		Subject: "VESSEL:1|FLAG_STATE",
		Records: []EvidenceRecord{
			{Kind: "raw", Index: 1, Payload: []byte(`{"source":"ais-a","value":"PANAMA","weight":0.9}`)},
			{Kind: "raw", Index: 2, Payload: []byte(`{"source":"ais-b","value":"PANAMA","weight":0.85}`)},
			{Kind: "raw", Index: 3, Payload: []byte(`{"source":"port-auth","value":"LIBERIA","weight":0.95}`)},
		},
	}
}

// independentSum replays the SAME algorithm the "original engine" used
// to reach ClaimedResult, but as a pure, freshly-called function with
// zero shared state — standing in for a real domain's Rebuild()/
// ArbitrateClaim() in this package-agnostic test.
func independentSum(pkg EvidencePackage) (string, error) {
	// naive re-derivation: majority value by count, deterministic
	counts := map[string]int{}
	for _, r := range pkg.Records {
		// payload contains the value; extract crudely for the test
		if containsSub(string(r.Payload), "PANAMA") {
			counts["PANAMA"]++
		} else if containsSub(string(r.Payload), "LIBERIA") {
			counts["LIBERIA"]++
		}
	}
	winner := "PANAMA"
	if counts["LIBERIA"] > counts["PANAMA"] {
		winner = "LIBERIA"
	}
	return winner, nil
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestBuildManifestDeterministic(t *testing.T) {
	rp := ReplayPackage{Evidence: sampleEvidencePackage(), ClaimedResult: "PANAMA"}
	m1, err := BuildManifest(rp)
	must(t, err)
	m2, err := BuildManifest(rp)
	must(t, err)
	if m1.RootHash != m2.RootHash {
		t.Fatalf("expected identical root hash across repeated builds: %s vs %s", m1.RootHash, m2.RootHash)
	}
}

func TestBuildManifestEmptyBundleRejected(t *testing.T) {
	_, err := BuildManifest(ReplayPackage{Evidence: EvidencePackage{Subject: "x"}, ClaimedResult: "y"})
	if err != ErrEmptyBundle {
		t.Fatalf("expected ErrEmptyBundle, got %v", err)
	}
}

func TestFullIVFPipeline_HappyPath(t *testing.T) {
	rp := ReplayPackage{Evidence: sampleEvidencePackage(), ClaimedResult: "PANAMA"}
	bundle, err := NewBundle(rp)
	must(t, err)

	// Marshal/Unmarshal round trip, simulating "hand this file to a
	// regulator" — the verifier below only ever sees the bytes.
	raw, err := bundle.Marshal()
	must(t, err)
	roundTripped, err := Unmarshal(raw)
	must(t, err)

	// A SECOND, freshly-constructed Verifier — proves no state leaks
	// in from whatever produced the bundle.
	v := NewVerifier()
	v.RegisterReplayFunc("vessel_flag_state", independentSum)

	result, err := v.Verify("vessel_flag_state", roundTripped)
	must(t, err)
	if !result.ManifestValid {
		t.Fatalf("expected manifest to validate against an untampered bundle")
	}
	if !result.ReplayValid {
		t.Fatalf("expected independent replay to reproduce PANAMA, got %s", result.RecomputedResult)
	}
	if result.Certificate == nil {
		t.Fatalf("expected an AuditCertificate to be issued on full verification success")
	}
	if !VerifyCertificate(*result.Certificate) {
		t.Fatalf("expected the issued certificate to verify against its own hash")
	}
}

func TestIVF_DetectsTamperedEvidence(t *testing.T) {
	rp := ReplayPackage{Evidence: sampleEvidencePackage(), ClaimedResult: "PANAMA"}
	bundle, err := NewBundle(rp)
	must(t, err)

	// tamper with one evidence record's payload AFTER the manifest was
	// computed, without recomputing the manifest — simulating an
	// attacker trying to alter evidence post-hoc.
	bundle.ReplayPackage.Evidence.Records[2].Payload = []byte(`{"source":"port-auth","value":"PANAMA","weight":0.95}`)

	v := NewVerifier()
	v.RegisterReplayFunc("vessel_flag_state", independentSum)
	result, err := v.Verify("vessel_flag_state", bundle)
	must(t, err)
	if result.ManifestValid {
		t.Fatalf("expected manifest validation to FAIL for tampered evidence")
	}
	if result.Certificate != nil {
		t.Fatalf("expected NO certificate to be issued for a tampered bundle")
	}
}

func TestIVF_DetectsClaimedResultMismatch(t *testing.T) {
	// Claimed result disagrees with what the evidence actually supports.
	rp := ReplayPackage{Evidence: sampleEvidencePackage(), ClaimedResult: "LIBERIA"}
	bundle, err := NewBundle(rp)
	must(t, err)

	v := NewVerifier()
	v.RegisterReplayFunc("vessel_flag_state", independentSum)
	result, err := v.Verify("vessel_flag_state", bundle)
	must(t, err)
	if result.ManifestValid != true {
		t.Fatalf("manifest itself is internally consistent (untampered), should still be valid")
	}
	if result.ReplayValid {
		t.Fatalf("expected replay validation to FAIL since independent replay yields PANAMA, not the claimed LIBERIA")
	}
	if result.Certificate != nil {
		t.Fatalf("expected no certificate when claimed result disagrees with independent replay")
	}
}

func TestIVF_MissingReplayFuncErrors(t *testing.T) {
	rp := ReplayPackage{Evidence: sampleEvidencePackage(), ClaimedResult: "PANAMA"}
	bundle, err := NewBundle(rp)
	must(t, err)
	v := NewVerifier()
	_, err = v.Verify("unregistered_domain", bundle)
	if err == nil {
		t.Fatalf("expected error for unregistered domain")
	}
}

func TestVerifyCertificateDetectsTamperedCertificate(t *testing.T) {
	m := BundleManifest{Subject: "s", RootHash: "abc", ClaimedResult: "PANAMA", RecordCount: 3}
	cert := IssueCertificate(m)
	if !VerifyCertificate(cert) {
		t.Fatalf("expected fresh certificate to verify")
	}
	cert.ClaimedResult = "LIBERIA" // tamper after issuance
	if VerifyCertificate(cert) {
		t.Fatalf("expected tampered certificate to fail verification")
	}
}

func TestSortRecordsByIndexDeterministic(t *testing.T) {
	records := []EvidenceRecord{
		{Kind: "raw", Index: 3}, {Kind: "raw", Index: 1}, {Kind: "raw", Index: 2},
	}
	sorted := SortRecordsByIndex(records)
	for i, r := range sorted {
		if r.Index != uint64(i+1) {
			t.Fatalf("expected sorted order 1,2,3, got %v", sorted)
		}
	}
	// original slice must not be mutated
	if records[0].Index != 3 {
		t.Fatalf("SortRecordsByIndex must not mutate the input slice")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEvidenceRecordHashSensitiveToEveryField(t *testing.T) {
	base := EvidenceRecord{Kind: "raw", Index: 1, Payload: []byte("x")}
	h1 := hashRecord(base)
	variants := []EvidenceRecord{
		{Kind: "different", Index: 1, Payload: []byte("x")},
		{Kind: "raw", Index: 2, Payload: []byte("x")},
		{Kind: "raw", Index: 1, Payload: []byte("y")},
	}
	for i, v := range variants {
		if hashRecord(v) == h1 {
			t.Fatalf("variant %d should produce a different hash than base", i)
		}
	}
}
