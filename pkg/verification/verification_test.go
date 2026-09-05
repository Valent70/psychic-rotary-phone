package verification

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var vat = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

type fixture struct {
	dir  string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// genuine builds a bundle that should verify.
func genuine(t *testing.T, mutate func(*Builder)) fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBuilder("case-1", "INTERNALLY_ASSURED", vat)
	if err != nil {
		t.Fatal(err)
	}

	artefact := []byte("LOADING SURVEY: 60,000.000 MT")
	sum := sha256.Sum256(artefact)
	must(t, b.Add("artefacts/e1v1.txt", artefact))
	must(t, b.AddJSON("evidence/versions.json", []map[string]any{{
		"id": "evidenceversion:e1v1", "sha256": hex.EncodeToString(sum[:]),
		"artefact_path": "artefacts/e1v1.txt", "size_bytes": len(artefact),
	}}))
	must(t, b.AddJSON("provenance/records.json", []map[string]any{{
		"id": "provenance:p1", "source_content_hash": hex.EncodeToString(sum[:]),
		"path": []map[string]any{
			{"party_id": "load-terminal", "role": "OBSERVER", "at": vat.Add(-2 * time.Hour)},
			{"party_id": "inspector-a", "role": "PRODUCER", "at": vat.Add(-time.Hour)},
			{"party_id": "veriqo", "role": "RECIPIENT", "at": vat},
		},
	}}))

	// A real two-record chain, hashed the way the ledger does it.
	events := []map[string]any{
		{"actor": "human:analyst-1", "action": "EVIDENCE_READ", "outcome": "SUCCEEDED"},
		{"actor": "human:reviewer-1", "action": "FINDING_APPROVED", "outcome": "SUCCEEDED"},
	}
	var recs []map[string]any
	prev := "veriqo-ledger-genesis-v1"
	for i, ev := range events {
		h, err := RecordDigest(uint64(i), prev, ev)
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, map[string]any{
			"height": i, "prev_hash": prev, "event": ev, "hash": h})
		prev = h
	}
	must(t, b.AddJSON("ledger/records.json", recs))

	// A replay record with one deterministic step and one recorded.
	out := map[string]any{"excess_over_tolerance": 1500.0, "unit": "MT"}
	canon, err := DefaultCanonicalizer().Canonicalize(out)
	if err != nil {
		t.Fatal(err)
	}
	osum := sha256.Sum256(canon)
	must(t, b.AddJSON("replay/steps.json", []map[string]any{
		{"name": "quantum", "kind": "DETERMINISTIC", "input": map[string]any{"x": 1},
			"output": out, "output_hash": hex.EncodeToString(osum[:])},
		{"name": "model-extraction", "kind": "RECORDED", "input": map[string]any{"x": 2},
			"output_hash": "recorded"},
	}))

	// A passport signed over the recomputed digest of its payload.
	payload := map[string]any{
		"schema": "veriqo.passport/v1", "finding_id": "finding:f1", "case_id": "case-1",
		"statement":               "the cargo discharged was 1,500 MT short",
		"limitations":             []any{"the discharge survey covers one tank of three"},
		"independently_validated": false,
	}
	pcanon, err := DefaultCanonicalizer().Canonicalize(payload)
	if err != nil {
		t.Fatal(err)
	}
	psum := sha256.Sum256(pcanon)
	digest := hex.EncodeToString(psum[:])
	must(t, b.AddJSON("passport.json", map[string]any{
		"payload": payload, "digest": digest,
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(digest))),
		"key_id":    "veriqo-key-1",
	}))
	must(t, b.AddJSON("assurance/evidence.json", []map[string]any{{
		"class": "ASSURANCE_INTERNAL", "summary": "our own tests",
		"validator": map[string]any{"id": "veriqo-engineering", "external": false},
	}}))
	must(t, b.Add("README.txt", []byte(README)))
	must(t, b.AddPublicKey("veriqo-key-1", pub))

	if mutate != nil {
		mutate(b)
	}
	dir := t.TempDir()
	if _, err := b.Write(dir); err != nil {
		t.Fatal(err)
	}
	return fixture{dir: dir, pub: pub, priv: priv}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func verify(t *testing.T, f fixture, opts Options) Report {
	t.Helper()
	b, err := Open(f.dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opts.At.IsZero() {
		opts.At = vat
	}
	return Verify(b, opts)
}

// TestAGenuineBundleVerifies, and says exactly what it could not do.
func TestAGenuineBundleVerifies(t *testing.T) {
	f := genuine(t, nil)
	r := verify(t, f, Options{TrustedKeys: map[string]ed25519.PublicKey{"veriqo-key-1": f.pub},
		Revocations: []string{}})
	if !r.Verified() {
		t.Fatalf("a genuine bundle did not verify:\n%s", r.Render())
	}
	if r.DerivedQualification != "INTERNALLY_ASSURED" {
		t.Fatalf("derived %s", r.DerivedQualification)
	}
	// Even on success the limits are stated.
	joined := strings.Join(r.Limits, " ")
	for _, want := range []string{"key authenticity", "external anchor", "never put in the bundle"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the report omits the limit %q", want)
		}
	}
	// And the recorded replay step is reported, not silently counted.
	if !strings.Contains(r.Render(), "RECORDED rather than re-executed") {
		t.Fatalf("the recorded step is not caveated:\n%s", r.Render())
	}
}

// TestATamperedArtefactIsCaught. The digest is recomputed from the
// bytes, so changing the bytes changes the answer.
func TestATamperedArtefactIsCaught(t *testing.T) {
	f := genuine(t, nil)
	p := filepath.Join(f.dir, "artefacts", "e1v1.txt")
	if err := os.WriteFile(p, []byte("LOADING SURVEY: 58,200.000 MT"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The manifest catches it first, which is itself the right answer.
	if _, err := Open(f.dir); err == nil {
		t.Fatal("a bundle whose bytes do not match its manifest opened cleanly")
	}
	// Now the harder case: the attacker updates the manifest too, so
	// only the evidence record disagrees.
	fixManifest(t, f.dir)
	b, err := Open(f.dir)
	if err != nil {
		t.Fatalf("open after manifest repair: %v", err)
	}
	r := Verify(b, Options{At: vat})
	if r.Verified() {
		t.Fatalf("a substituted artefact verified:\n%s", r.Render())
	}
	if !strings.Contains(strings.Join(r.Failures(), " "), "recorded digest") {
		t.Fatalf("the failure does not name the digest mismatch: %v", r.Failures())
	}
}

// TestAnEditedLedgerRecordIsCaughtByRecomputation. The chain carries
// its own hashes; a verifier that read them would confirm only that
// the file describes itself.
func TestAnEditedLedgerRecordIsCaughtByRecomputation(t *testing.T) {
	f := genuine(t, nil)
	p := filepath.Join(f.dir, "ledger", "records.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"FINDING_APPROVED"`, `"FINDING_REJECTED"`, 1)
	if edited == string(raw) {
		t.Fatal("the fixture no longer contains the token being edited")
	}
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	fixManifest(t, f.dir)

	b, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(b, Options{At: vat})
	if r.Verified() {
		t.Fatalf("an edited ledger verified:\n%s", r.Render())
	}
	if !strings.Contains(strings.Join(r.Failures(), " "), "altered since it was written") {
		t.Fatalf("the failure does not say what happened: %v", r.Failures())
	}
	// And a failure anywhere refutes the qualification rather than
	// leaving it standing.
	if r.DerivedQualification != "REFUTED" {
		t.Fatalf("derived qualification after a failure is %s", r.DerivedQualification)
	}
}

// TestASwappedPassportPayloadIsCaught. The digest is recomputed from
// the payload, never taken from the digest field -- a verifier that
// checked the signature against the supplied digest would accept any
// payload at all.
func TestASwappedPassportPayloadIsCaught(t *testing.T) {
	f := genuine(t, nil)
	p := filepath.Join(f.dir, "passport.json")
	raw, _ := os.ReadFile(p)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	payload := doc["payload"].(map[string]any)
	payload["statement"] = "the cargo discharged in full"
	// The digest and signature fields are left untouched: this is the
	// attack, not a mistake.
	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	fixManifest(t, f.dir)

	b, _ := Open(f.dir)
	r := Verify(b, Options{At: vat,
		TrustedKeys: map[string]ed25519.PublicKey{"veriqo-key-1": f.pub}})
	if r.Verified() {
		t.Fatalf("a swapped payload verified:\n%s", r.Render())
	}
	if !strings.Contains(strings.Join(r.Failures(), " "), "altered since it was signed") {
		t.Fatalf("the failure does not name the alteration: %v", r.Failures())
	}
}

// TestAMissingKeyIsUnverifiableNotFailed. Reporting "cannot check" as
// "invalid" trains readers to ignore failures.
func TestAMissingKeyIsUnverifiableNotFailed(t *testing.T) {
	f := genuine(t, nil)
	b, _ := Open(f.dir)
	r := Verify(b, Options{At: vat, TrustedKeys: map[string]ed25519.PublicKey{"other": f.pub}})
	var sig Step
	for _, s := range r.Steps {
		if s.Name == "signature" {
			sig = s
		}
	}
	if sig.Outcome != Unverifiable {
		t.Fatalf("a missing key gave %s: %s", sig.Outcome, sig.Detail)
	}
	if !strings.Contains(sig.Detail, "does match its own digest") {
		t.Fatalf("the step does not say what it DID establish: %s", sig.Detail)
	}
}

// TestKeysFromTheBundleAreReportedAsWorthless. The caveat must appear
// whether or not the signature checks out.
func TestKeysFromTheBundleAreReportedAsWorthless(t *testing.T) {
	f := genuine(t, nil)
	r := verify(t, f, Options{Revocations: []string{}})
	var sig Step
	for _, s := range r.Steps {
		if s.Name == "signature" {
			sig = s
		}
	}
	if sig.Outcome != Pass {
		t.Fatalf("the signature step gave %s", sig.Outcome)
	}
	if !strings.Contains(strings.Join(sig.Caveats, " "), "establishes nothing about whose keys") {
		t.Fatalf("bundle-supplied keys were not caveated: %v", sig.Caveats)
	}
}

// TestUncheckedRevocationIsNotTreatedAsNotRevoked.
func TestUncheckedRevocationIsNotTreatedAsNotRevoked(t *testing.T) {
	f := genuine(t, nil)
	r := verify(t, f, Options{}) // nil revocation list
	var rev Step
	for _, s := range r.Steps {
		if s.Name == "revocation" {
			rev = s
		}
	}
	if rev.Outcome != Unverifiable {
		t.Fatalf("an unchecked revocation gave %s", rev.Outcome)
	}
	if !strings.Contains(rev.Detail, "different answers") {
		t.Fatalf("the step does not distinguish not-checked from not-revoked: %s", rev.Detail)
	}
}

// TestTheVerifierContradictsAnInflatedClaimRatherThanAdoptingIt.
//
// This is the property that makes the kit a verifier rather than a
// relay: it derives the qualification and reports the difference.
func TestTheVerifierContradictsAnInflatedClaimRatherThanAdoptingIt(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = priv
	f := genuine(t, nil)
	// Rewrite the manifest's claim to something the bundle does not
	// support, exactly as an over-eager vendor would.
	mp := filepath.Join(f.dir, "manifest.json")
	raw, _ := os.ReadFile(mp)
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.ClaimedQualification = "PRODUCTION_QUALIFIED"
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(mp, out, 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Verify(b, Options{At: vat, Revocations: []string{},
		TrustedKeys: map[string]ed25519.PublicKey{"veriqo-key-1": f.pub}})
	if r.Verified() {
		t.Fatal("a bundle claiming PRODUCTION_QUALIFIED on internal evidence verified")
	}
	if r.DerivedQualification != "INTERNALLY_ASSURED" {
		t.Fatalf("derived %s", r.DerivedQualification)
	}
	if !strings.Contains(r.Render(), "THESE DISAGREE. Believe the derived value.") {
		t.Fatalf("the report does not contradict the claim:\n%s", r.Render())
	}
	_ = pub
}

// TestAReplayThatReExecutedNothingSaysSo.
func TestAReplayThatReExecutedNothingSaysSo(t *testing.T) {
	f := genuine(t, nil)
	p := filepath.Join(f.dir, "replay", "steps.json")
	if err := os.WriteFile(p, []byte(`[{"name":"a","kind":"RECORDED","output_hash":"x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	fixManifest(t, f.dir)
	b, _ := Open(f.dir)
	r := Verify(b, Options{At: vat})
	var st Step
	for _, s := range r.Steps {
		if s.Name == "replay" {
			st = s
		}
	}
	if st.Outcome != Unverifiable {
		t.Fatalf("an all-recorded replay gave %s", st.Outcome)
	}
	if !strings.Contains(st.Detail, "establishes nothing") {
		t.Fatalf("the step does not say what it failed to establish: %s", st.Detail)
	}
}

// TestABundleWithNothingInItIsRefused.
func TestABundleWithNothingInItIsRefused(t *testing.T) {
	b, err := NewBuilder("case-1", "INTERNALLY_ASSURED", vat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(t.TempDir()); err == nil {
		t.Fatal("an empty bundle was written")
	}
	if _, err := NewBuilder("case-1", "", vat); err == nil {
		t.Fatal("a bundle with no claim was created; a verifier would have nothing to disagree with")
	}
	if err := b.Add("../escape.txt", []byte("x")); err == nil {
		t.Fatal("a path escaping the bundle was accepted")
	}
	if err := b.Add("manifest.json", []byte("x")); err == nil {
		t.Fatal("a caller overwrote the manifest")
	}
}

// TestTheBundleIsDeterministic. Two builds of the same inputs must
// have the same digest, or a difference means nothing.
func TestTheBundleIsDeterministic(t *testing.T) {
	build := func() string {
		b, _ := NewBuilder("case-1", "INTERNALLY_ASSURED", vat)
		must(t, b.AddJSON("a.json", map[string]any{"z": 1, "a": 2}))
		must(t, b.Add("b.txt", []byte("hello")))
		dir := t.TempDir()
		if _, err := b.Write(dir); err != nil {
			t.Fatal(err)
		}
		bundle, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return bundle.Digest()
	}
	if a, c := build(), build(); a != c {
		t.Fatalf("two builds of the same bundle differ: %s vs %s", a, c)
	}
}

// TestTheReadmeTravelsWithTheBundle. Instructions separated from the
// thing they describe are instructions nobody reads.
func TestTheReadmeTravelsWithTheBundle(t *testing.T) {
	f := genuine(t, nil)
	b, _ := Open(f.dir)
	raw, ok := b.File("README.txt")
	if !ok {
		t.Fatal("the bundle carries no README")
	}
	for _, want := range []string{"without asking VERIQO anything",
		"internally perfect", "external anchor"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the README omits %q", want)
		}
	}
}

// fixManifest recomputes the manifest after a test has tampered with a
// file. It stands in for an attacker who is competent enough to update
// the index -- which is the case the deeper steps exist for.
func fixManifest(t *testing.T, dir string) {
	t.Helper()
	mp := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for p := range m.Files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		m.Files[p] = hex.EncodeToString(sum[:])
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(mp, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
