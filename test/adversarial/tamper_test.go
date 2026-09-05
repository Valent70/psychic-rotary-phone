package adversarial

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
	"veriqo/pkg/custody"
	"veriqo/pkg/ledger"
)

type ed25519Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newSigner(t *testing.T) ed25519Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519Signer{priv: priv, pub: pub}
}

func (s ed25519Signer) Sign(m []byte) ([]byte, string, error) {
	return ed25519.Sign(s.priv, m), "adversarial-key-1", nil
}

func (s ed25519Signer) Verify(m, sig []byte, keyID string) error {
	if keyID != "adversarial-key-1" {
		return errors.New("unknown key")
	}
	if !ed25519.Verify(s.pub, m, sig) {
		return errors.New("bad signature")
	}
	return nil
}

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "adversarial", Revision: 1},
	}
}

func event(tenantID string, i int, outcome contract.Outcome) ledger.Event {
	return ledger.Event{
		Actor: "human:analyst-1", TenantID: tenantID,
		Action: "EVIDENCE_READ", Subject: "evidenceversion:e1v1",
		At: at.Add(time.Duration(i) * time.Minute), Purpose: "CASE_INVESTIGATION",
		PolicyDecision: "PERMIT baseline/read", Versions: versions(),
		Outcome: outcome,
	}
}

func filledLedger(t *testing.T, dir, tenantID string, s ledger.Signer, n int) {
	t.Helper()
	l, err := ledger.Open(dir, tenantID, ledger.Options{Signer: s})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := l.Append(event(tenantID, i, contract.Succeeded)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestAnEditedLedgerRecordIsDetectedOnReopen. The attack is the
// obvious one: change what an event says after the fact. It has to be
// caught by recomputation, not by a flag.
func TestAnEditedLedgerRecordIsDetectedOnReopen(t *testing.T) {
	dir := t.TempDir()
	s := newSigner(t)
	filledLedger(t, dir, "t-acme", s, 4)

	path := filepath.Join(dir, "chain.log")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the outcome of a middle record without touching anything
	// else. The bytes stay the same length, so the framing still
	// parses -- only the digest disagrees.
	edited := strings.Replace(string(raw), `"outcome":"SUCCEEDED"`, `"outcome":"REFUSED"`, 1)
	if edited == string(raw) {
		t.Fatal("the fixture no longer contains the token being edited")
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ledger.Open(dir, "t-acme", ledger.Options{Signer: s}); err == nil {
		t.Fatal("an edited ledger reopened cleanly")
	} else if !errors.Is(err, ledger.ErrChainBroken) && !errors.Is(err, ledger.ErrBadRecord) {
		t.Fatalf("the failure is not reported as a chain break: %v", err)
	}
}

// TestARemovedLedgerRecordIsDetected. Deleting an inconvenient event
// is the more attractive attack, because it leaves nothing to explain.
func TestARemovedLedgerRecordIsDetected(t *testing.T) {
	dir := t.TempDir()
	s := newSigner(t)
	filledLedger(t, dir, "t-acme", s, 4)

	path := filepath.Join(dir, "chain.log")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Drop the first framed record. Every subsequent record now sits
	// at the wrong height and points at a predecessor that is gone.
	l, err := ledger.Open(dir, "t-acme", ledger.Options{Signer: s})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := l.Records()
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if len(recs) != 4 {
		t.Fatalf("fixture has %d records", len(recs))
	}
	// Frame layout is length-prefixed; find the first record's extent
	// by re-encoding is fragile, so cut a whole prefix of the file and
	// assert the reopen fails rather than silently accepting a
	// shortened chain.
	if err := os.WriteFile(path, raw[len(raw)/2:], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Open(dir, "t-acme", ledger.Options{Signer: s}); err == nil {
		t.Fatal("a truncated-from-the-front ledger reopened cleanly")
	}
}

// TestACheckpointDoesNotVerifyAgainstADifferentChain. A checkpoint
// lifted from one tenant and presented for another must fail, or a
// signed checkpoint becomes a universal "this log is fine" token.
func TestACheckpointDoesNotVerifyAgainstADifferentChain(t *testing.T) {
	s := newSigner(t)
	dirA, dirB := t.TempDir(), t.TempDir()
	filledLedger(t, dirA, "t-acme", s, 3)
	filledLedger(t, dirB, "t-globex", s, 3)

	la, err := ledger.Open(dirA, "t-acme", ledger.Options{Signer: s})
	if err != nil {
		t.Fatal(err)
	}
	defer la.Close()
	cp, err := la.Checkpoint(at)
	if err != nil {
		t.Fatal(err)
	}
	if err := la.VerifyCheckpoint(cp); err != nil {
		t.Fatalf("a checkpoint failed against its own chain: %v", err)
	}

	lb, err := ledger.Open(dirB, "t-globex", ledger.Options{Signer: s})
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()
	if err := lb.VerifyCheckpoint(cp); err == nil {
		t.Fatal("a checkpoint verified against a chain it does not describe")
	}
}

// TestAnUnanchoredCheckpointIsNotSilentlyAccepted. A self-signed
// checkpoint proves the operator agrees with themselves. Production
// requires an external anchor, and RequireAnchored is the function
// that refuses to pretend otherwise.
func TestAnUnanchoredCheckpointIsNotSilentlyAccepted(t *testing.T) {
	s := newSigner(t)
	dir := t.TempDir()
	filledLedger(t, dir, "t-acme", s, 2)
	l, err := ledger.Open(dir, "t-acme", ledger.Options{Signer: s})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	cp, err := l.Checkpoint(at)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Anchored() {
		t.Fatal("a checkpoint claimed to be anchored with no anchor configured")
	}
	if err := ledger.RequireAnchored(cp); !errors.Is(err, ledger.ErrNotAnchored) {
		t.Fatalf("an unanchored checkpoint passed the production check: %v", err)
	}
}

// TestALedgerWithNoSignerCannotBeOpened. An unsigned ledger produces
// unsigned checkpoints, which attest to nothing; the refusal belongs
// at startup where somebody will see it.
func TestALedgerWithNoSignerCannotBeOpened(t *testing.T) {
	if _, err := ledger.Open(t.TempDir(), "t-acme", ledger.Options{}); !errors.Is(err, ledger.ErrNoSigner) {
		t.Fatalf("an unsigned ledger opened: %v", err)
	}
}

// TestSubstitutedContentIsCaughtByTheRecordedDigest.
//
// The custody chain records what each holder received and released.
// A holder that swaps the material and records the NEW digest as
// received is the interesting case: the break is between their
// received digest and the previous holder's released digest, and it
// is found by comparing RECORDED values -- never by re-hashing the
// current bytes, which would make substitution invisible.
func TestSubstitutedContentIsCaughtByTheRecordedDigest(t *testing.T) {
	original := jcs.HashBytes([]byte("SURVEY REPORT: 60,000.000 MT"))
	substitute := jcs.HashBytes([]byte("SURVEY REPORT: 58,200.000 MT"))

	c, err := custody.New("evidenceversion:e1v1", "t-acme", custody.Link{
		HolderID: "ingest", Action: custody.Acquired, At: at,
		ReceivedDigest: original, ReleasedDigest: original,
		Authorization: "PERMIT baseline/ingest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Intact() {
		t.Fatal("a fresh chain is broken")
	}
	// The dishonest holder. Nothing here is syntactically wrong.
	if err := c.Append(custody.Link{
		HolderID: "processor", Action: custody.Transferred, At: at.Add(time.Hour),
		ReceivedDigest: substitute, ReleasedDigest: substitute,
		Authorization: "PERMIT baseline/transfer",
	}); err != nil {
		// Append may accept it and let Breaks() report it; either is a
		// detection, silence is not.
		if !strings.Contains(err.Error(), "digest") && !strings.Contains(err.Error(), "custody") {
			t.Fatalf("unexpected append failure: %v", err)
		}
		return
	}
	if c.Intact() {
		t.Fatal("a substituted digest left the chain intact")
	}
	breaks := c.Breaks()
	if len(breaks) == 0 {
		t.Fatal("no break was reported")
	}
	if !strings.Contains(c.Limitation(), "custody") {
		t.Fatalf("the limitation does not say what happened: %q", c.Limitation())
	}
	// And the break is permanent: appending a well-formed link after
	// it does not repair the record.
	if err := c.Append(custody.Link{
		HolderID: "archive", Action: custody.Stored, At: at.Add(2 * time.Hour),
		ReceivedDigest: substitute, ReleasedDigest: substitute,
		Authorization: "PERMIT baseline/store",
	}); err != nil {
		t.Fatalf("append after a break: %v", err)
	}
	if c.Intact() {
		t.Fatal("a later well-formed link erased an earlier break")
	}
}

// TestASealedChainRefusesFurtherLinks. Sealing is what makes an
// exhibit an exhibit; if it can be appended to afterwards it is a
// label, not a control.
func TestASealedChainRefusesFurtherLinks(t *testing.T) {
	d := jcs.HashBytes([]byte("exhibit"))
	c, err := custody.New("evidenceversion:e1v1", "t-acme", custody.Link{
		HolderID: "ingest", Action: custody.Acquired, At: at,
		ReceivedDigest: d, ReleasedDigest: d, Authorization: "PERMIT baseline/ingest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Seal("evidence-locker", "PERMIT baseline/seal", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !c.IsSealed() {
		t.Fatal("Seal did not seal")
	}
	if err := c.Append(custody.Link{
		HolderID: "processor", Action: custody.Processed, At: at.Add(2 * time.Hour),
		ReceivedDigest: d, ReleasedDigest: d, Authorization: "PERMIT baseline/process",
		Note: "reprocessing",
	}); err == nil {
		t.Fatal("a sealed chain accepted a further link")
	}
}

// TestForgedSignatureBytesDoNotVerify guards the primitive itself:
// a base64-shaped blob of the right length is not a signature.
func TestForgedSignatureBytesDoNotVerify(t *testing.T) {
	s := newSigner(t)
	msg := []byte("veriqo.passport/v1 payload")
	sig, keyID, err := s.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(msg, sig, keyID); err != nil {
		t.Fatalf("a genuine signature failed: %v", err)
	}
	forged := make([]byte, len(sig))
	copy(forged, sig)
	forged[0] ^= 0xff
	if err := s.Verify(msg, forged, keyID); err == nil {
		t.Fatal("a flipped signature verified")
	}
	if err := s.Verify([]byte("veriqo.passport/v1 PAYLOAD"), sig, keyID); err == nil {
		t.Fatal("a signature verified over different bytes")
	}
	if err := s.Verify(msg, sig, "attacker-key-1"); err == nil {
		t.Fatal("a signature verified under an unknown key id")
	}
	if _, err := base64.StdEncoding.DecodeString(base64.StdEncoding.EncodeToString(sig)); err != nil {
		t.Fatal(err)
	}
}
