package ledger

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

// ed25519Signer is a test signer. Production uses an HSM/KMS-backed
// one; the interface is what keeps key material out of this package.
type ed25519Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newSigner(t *testing.T) *ed25519Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &ed25519Signer{priv: priv, pub: pub}
}

func (s *ed25519Signer) Sign(m []byte) ([]byte, string, error) {
	return ed25519.Sign(s.priv, m), "test-key-1", nil
}

func (s *ed25519Signer) Verify(m, sig []byte, keyID string) error {
	if keyID != "test-key-1" {
		return errors.New("unknown key")
	}
	if !ed25519.Verify(s.pub, m, sig) {
		return errors.New("signature does not verify")
	}
	return nil
}

// fakeAnchor stands in for a third-party anchoring service in tests
// only. It is named "fake" so nobody mistakes it for one.
type fakeAnchor struct {
	published map[string]Checkpoint
	fail      bool
}

func newFakeAnchor() *fakeAnchor {
	return &fakeAnchor{published: map[string]Checkpoint{}}
}

func (a *fakeAnchor) Publish(c Checkpoint) (string, time.Time, error) {
	if a.fail {
		return "", time.Time{}, errors.New("anchor unavailable")
	}
	ref := "anchor-" + c.Root[:8]
	a.published[ref] = c
	return ref, c.At.Add(time.Minute), nil
}

func (a *fakeAnchor) Confirm(ref string, c Checkpoint) error {
	got, ok := a.published[ref]
	if !ok || got.Root != c.Root {
		return errors.New("no such receipt")
	}
	return nil
}

func (a *fakeAnchor) Name() string { return "fake-anchor" }

var at0 = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func event(n int) Event {
	return Event{
		Actor: "human:analyst-1", TenantID: "t-acme",
		Action: "evidence.ingested", Subject: "evidence:e" + string(rune('0'+n)),
		At: at0.Add(time.Duration(n) * time.Minute), Purpose: "CASE_INVESTIGATION",
		PolicyDecision: "PERMIT baseline/clearance",
		Outcome:        contract.Succeeded,
		Versions: contract.VersionSet{
			Ontology:  contract.Version{Component: "maritime", Revision: 1},
			Policy:    contract.Version{Component: "baseline", Revision: 1},
			Algorithm: contract.Version{Component: "ingest", Revision: 1},
		},
	}
}

func open(t *testing.T, dir string, anchor Anchor) *Ledger {
	t.Helper()
	l, err := Open(dir, "t-acme", Options{Signer: newSigner(t), Anchor: anchor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// TestTheChainSurvivesAReopen is the property an in-memory ledger does
// not have, and the reason this package exists.
func TestTheChainSurvivesAReopen(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 5; i++ {
		if _, err := l.Append(event(i)); err != nil {
			t.Fatal(err)
		}
	}
	head := l.Head()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := open(t, dir, nil)
	if reopened.Height() != 5 {
		t.Fatalf("height after reopen = %d, want 5", reopened.Height())
	}
	if reopened.Head() != head {
		t.Fatalf("head after reopen = %s, want %s", short(reopened.Head()), short(head))
	}
	if err := reopened.Verify(); err != nil {
		t.Fatalf("chain does not verify after reopen: %v", err)
	}
	// And a new append links to the recovered head.
	r, err := reopened.Append(event(5))
	if err != nil {
		t.Fatal(err)
	}
	if r.PrevHash != head || r.Height != 5 {
		t.Fatalf("the appended record did not continue the chain: height %d prev %s", r.Height, short(r.PrevHash))
	}
}

// TestAnAlteredRecordBreaksTheChain, detected on read rather than only
// at write time.
func TestAnAlteredRecordBreaksTheChain(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(event(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Rewrite record 1's event, keeping the framing valid: this is the
	// realistic attack, an editor with disk access, not a random flip.
	path := filepath.Join(dir, "chain.log")
	recs, tail, err := scan(path)
	if err != nil || tail >= 0 {
		t.Fatalf("scan: %v tail=%d", err, tail)
	}
	recs[1].Event.Outcome = contract.Failed
	rewrite(t, path, recs)

	if _, err := Open(dir, "t-acme", Options{Signer: newSigner(t)}); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("an edited record opened cleanly: %v", err)
	}
}

// TestAReorderedChainIsDetected. Height and PrevHash are inside the
// digest, so a record cannot be moved.
func TestAReorderedChainIsDetected(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(event(i)); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	path := filepath.Join(dir, "chain.log")
	recs, _, _ := scan(path)
	recs[0], recs[1] = recs[1], recs[0]
	rewrite(t, path, recs)

	if _, err := Open(dir, "t-acme", Options{Signer: newSigner(t)}); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("a reordered chain opened cleanly: %v", err)
	}
}

// TestADeletedRecordIsDetected. Removing a record from the middle
// leaves a gap the links refuse.
func TestADeletedRecordIsDetected(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 4; i++ {
		l.Append(event(i))
	}
	l.Close()

	path := filepath.Join(dir, "chain.log")
	recs, _, _ := scan(path)
	rewrite(t, path, append(recs[:1:1], recs[2:]...))

	if _, err := Open(dir, "t-acme", Options{Signer: newSigner(t)}); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("a chain with a deleted record opened cleanly: %v", err)
	}
}

func rewrite(t *testing.T, path string, recs []Record) {
	t.Helper()
	var buf []byte
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, frame(b)...)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestATornTailIsTruncatedAndTheRestSurvives. A ledger that refused to
// open after a power failure is a ledger nobody keeps.
func TestATornTailIsTruncatedAndTheRestSurvives(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 3; i++ {
		l.Append(event(i))
	}
	goodHead := l.Head()
	l.Close()

	path := filepath.Join(dir, "chain.log")
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Append a half-written record: a valid-looking header and a
	// payload that stops early.
	partial := frame([]byte(`{"height":3,"prev_hash":"x","event":{},"hash":"y"}`))
	if err := os.WriteFile(path, append(full, partial[:len(partial)-10]...), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened := open(t, dir, nil)
	if reopened.Height() != 3 {
		t.Fatalf("height after a torn tail = %d, want 3", reopened.Height())
	}
	if reopened.Head() != goodHead {
		t.Fatal("the head moved after truncating a partial record")
	}
	if err := reopened.Verify(); err != nil {
		t.Fatalf("the surviving chain does not verify: %v", err)
	}
	// And the log is usable again.
	if _, err := reopened.Append(event(9)); err != nil {
		t.Fatalf("cannot append after recovering from a torn tail: %v", err)
	}
}

// TestACorruptPayloadIsTreatedAsTheTail rather than as corruption of
// everything before it.
func TestACorruptPayloadIsTreatedAsTheTail(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 3; i++ {
		l.Append(event(i))
	}
	l.Close()

	path := filepath.Join(dir, "chain.log")
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 0xff // damage the last record's payload
	os.WriteFile(path, b, 0o600)

	reopened := open(t, dir, nil)
	if reopened.Height() != 2 {
		t.Fatalf("height = %d, want 2 (the two intact records)", reopened.Height())
	}
}

// --- Validation ---------------------------------------------------------

// TestAnIncompleteEventIsRefused. Each of these is a field the
// specification requires a ledger event to carry.
func TestAnIncompleteEventIsRefused(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	cases := map[string]func(*Event){
		"no actor":           func(e *Event) { e.Actor = "" },
		"no tenant":          func(e *Event) { e.TenantID = "" },
		"no action":          func(e *Event) { e.Action = "" },
		"no subject":         func(e *Event) { e.Subject = "" },
		"no instant":         func(e *Event) { e.At = time.Time{} },
		"no purpose":         func(e *Event) { e.Purpose = "" },
		"no policy decision": func(e *Event) { e.PolicyDecision = "" },
		"bad outcome":        func(e *Event) { e.Outcome = "OK" },
		"no versions":        func(e *Event) { e.Versions = contract.VersionSet{} },
	}
	for name, mutate := range cases {
		e := event(0)
		mutate(&e)
		if _, err := l.Append(e); err == nil {
			t.Errorf("an event with %s was appended", name)
		}
	}
	if l.Height() != 0 {
		t.Fatalf("%d invalid events reached the chain", l.Height())
	}
}

// TestAnEventForAnotherTenantIsRefused.
func TestAnEventForAnotherTenantIsRefused(t *testing.T) {
	l := open(t, t.TempDir(), nil)
	e := event(0)
	e.TenantID = "t-beta"
	if _, err := l.Append(e); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant event was appended: %v", err)
	}
}

// TestALedgerWithNoSignerIsRefusedAtOpen. A chain with no checkpoint
// must be replayed from genesis by anyone verifying it.
func TestALedgerWithNoSignerIsRefusedAtOpen(t *testing.T) {
	if _, err := Open(t.TempDir(), "t-acme", Options{}); !errors.Is(err, ErrNoSigner) {
		t.Fatalf("a ledger with no signer opened: %v", err)
	}
}

// --- Checkpoints and anchoring ------------------------------------------

func TestACheckpointVerifiesAgainstTheChain(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	for i := 0; i < 3; i++ {
		l.Append(event(i))
	}
	c, err := l.Checkpoint(at0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Height != 3 || c.Root != l.Head() {
		t.Fatalf("checkpoint height %d root %s", c.Height, short(c.Root))
	}
	if err := l.VerifyCheckpoint(c); err != nil {
		t.Fatalf("a fresh checkpoint does not verify: %v", err)
	}
}

// TestATamperedCheckpointFailsVerification.
func TestATamperedCheckpointFailsVerification(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	l.Append(event(0))
	c, err := l.Checkpoint(at0)
	if err != nil {
		t.Fatal(err)
	}
	c.Height = 99
	if err := l.VerifyCheckpoint(c); err == nil {
		t.Fatal("a checkpoint with an edited height verified")
	}
	c2, _ := l.Checkpoint(at0)
	c2.Root = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := l.VerifyCheckpoint(c2); err == nil {
		t.Fatal("a checkpoint with an edited root verified")
	}
}

// TestAnUnanchoredCheckpointIsSignedAndAttestedByNobody. This is the
// distinction the specification insists on and the one a green test
// suite most easily hides.
func TestAnUnanchoredCheckpointIsSignedAndAttestedByNobody(t *testing.T) {
	l := open(t, t.TempDir(), nil) // no anchor configured
	l.Append(event(0))
	c, err := l.Checkpoint(at0)
	if err != nil {
		t.Fatal(err)
	}
	// It is internally valid...
	if err := l.VerifyCheckpoint(c); err != nil {
		t.Fatalf("the checkpoint does not verify: %v", err)
	}
	// ...and it is not evidence to anybody outside VERIQO.
	if c.Anchored() {
		t.Fatal("a checkpoint with no anchor reported itself anchored")
	}
	if err := RequireAnchored(c); !errors.Is(err, ErrNotAnchored) {
		t.Fatalf("RequireAnchored accepted a VERIQO-only checkpoint: %v", err)
	}
}

func TestAnAnchoredCheckpointCarriesItsReceipt(t *testing.T) {
	a := newFakeAnchor()
	l := open(t, t.TempDir(), a)
	l.Append(event(0))
	c, err := l.Checkpoint(at0)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Anchored() {
		t.Fatal("a checkpoint published to an anchor reported itself unanchored")
	}
	if err := RequireAnchored(c); err != nil {
		t.Fatalf("an anchored checkpoint was refused: %v", err)
	}
	if err := a.Confirm(c.AnchorRef, c); err != nil {
		t.Fatalf("the receipt does not confirm: %v", err)
	}
}

// TestTheSignatureDoesNotCoverTheAnchorReceipt, because the receipt is
// produced after signing. If it did, every anchored checkpoint would
// fail its own verification.
func TestTheSignatureDoesNotCoverTheAnchorReceipt(t *testing.T) {
	l := open(t, t.TempDir(), newFakeAnchor())
	l.Append(event(0))
	c, err := l.Checkpoint(at0)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.VerifyCheckpoint(c); err != nil {
		t.Fatalf("an anchored checkpoint failed its own signature check: %v", err)
	}
}

// TestAnAnchorFailureIsReportedNotSwallowed. Silently returning an
// unanchored checkpoint when anchoring was configured would turn a
// production outage into a quiet loss of third-party attestation.
func TestAnAnchorFailureIsReportedNotSwallowed(t *testing.T) {
	a := newFakeAnchor()
	a.fail = true
	l := open(t, t.TempDir(), a)
	l.Append(event(0))
	c, err := l.Checkpoint(at0)
	if err == nil {
		t.Fatal("an anchoring failure was swallowed")
	}
	if c.Anchored() {
		t.Fatal("a checkpoint reported itself anchored after the anchor failed")
	}
}

// TestCheckpointsArePersisted so a restart can present prior
// checkpoints without replaying the chain.
func TestCheckpointsArePersisted(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	l.Append(event(0))
	if _, err := l.Checkpoint(at0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "checkpoints.jsonl"))
	if err != nil {
		t.Fatalf("checkpoints were not persisted: %v", err)
	}
	var c Checkpoint
	if err := json.Unmarshal(b[:len(b)-1], &c); err != nil {
		t.Fatal(err)
	}
	if c.Height != 1 {
		t.Fatalf("persisted checkpoint height = %d", c.Height)
	}
}

// TestTheGenesisLinkIsNotTheZeroValue. An all-zero previous hash is
// indistinguishable from an unset field.
func TestTheGenesisLinkIsNotTheZeroValue(t *testing.T) {
	l := open(t, t.TempDir(), nil)
	r, err := l.Append(event(0))
	if err != nil {
		t.Fatal(err)
	}
	if r.PrevHash == "" {
		t.Fatal("the first record links to the empty string")
	}
	if r.PrevHash != GenesisHash {
		t.Fatalf("the first record links to %q", r.PrevHash)
	}
}

func TestAppendAfterCloseIsRefused(t *testing.T) {
	l := open(t, t.TempDir(), nil)
	l.Close()
	if _, err := l.Append(event(0)); !errors.Is(err, ErrClosed) {
		t.Fatalf("appended to a closed ledger: %v", err)
	}
}

// TestConcurrentAppendsProduceAConsistentChain.
func TestConcurrentAppendsProduceAConsistentChain(t *testing.T) {
	dir := t.TempDir()
	l := open(t, dir, nil)
	const n = 40
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			e := event(0)
			e.At = at0.Add(time.Duration(i) * time.Second)
			_, err := l.Append(e)
			done <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if l.Height() != n {
		t.Fatalf("height = %d, want %d", l.Height(), n)
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("concurrent appends broke the chain: %v", err)
	}
}
