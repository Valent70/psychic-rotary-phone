package event

import (
	"errors"
	"testing"
)

func validEnvelope() Envelope {
	return Envelope{
		EventID: "EV-1", TenantID: "tenant-a", CaseID: "CASE-1",
		EventType: "evidence.ingested", AggregateType: "Evidence", AggregateID: "E-1",
		ActorID: "collector-1", ActorType: ActorService,
		Purpose: "acquisition", PolicyVersion: "policy-v1",
		OccurredAt: 10, RecordedAt: 11,
	}
}

func TestAppendAssignsSequenceAndLinksToGenesis(t *testing.T) {
	c := NewChain()
	if c.Head() != GenesisHash {
		t.Fatalf("an empty chain must head at GENESIS, got %q", c.Head())
	}
	e, err := c.Append(validEnvelope(), map[string]any{"sha256": "abc"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e.SequenceNo != 0 {
		t.Fatalf("first event must be sequence 0, got %d", e.SequenceNo)
	}
	if e.PreviousEventHash != GenesisHash {
		t.Fatalf("first event must link to GENESIS, got %q", e.PreviousEventHash)
	}
	if e.EventHash == "" || e.PayloadHash == "" {
		t.Fatal("Append must populate both hashes")
	}
}

// TestAppendIgnoresCallerSuppliedSequenceAndPrevious is the
// forge-resistance property: a caller who could choose its own
// sequence number or predecessor could fork the chain silently.
func TestAppendIgnoresCallerSuppliedSequenceAndPrevious(t *testing.T) {
	c := NewChain()
	if _, err := c.Append(validEnvelope(), nil); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	forged := validEnvelope()
	forged.EventID = "EV-2"
	forged.SequenceNo = 99
	forged.PreviousEventHash = "attacker-chosen-parent"

	got, err := c.Append(forged, nil)
	if err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if got.SequenceNo != 1 {
		t.Fatalf("chain must assign sequence 1, not the caller's 99; got %d", got.SequenceNo)
	}
	if got.PreviousEventHash == "attacker-chosen-parent" {
		t.Fatal("chain accepted a caller-chosen predecessor")
	}
	if err := VerifyChain(c.Events()); err != nil {
		t.Fatalf("chain must still verify: %v", err)
	}
}

// TestAppendComputesPayloadHashRatherThanTrustingIt proves a caller
// cannot record a payload hash that does not match the payload.
func TestAppendComputesPayloadHashRatherThanTrustingIt(t *testing.T) {
	c := NewChain()
	lying := validEnvelope()
	lying.PayloadHash = "0000000000000000000000000000000000000000000000000000000000000000"

	got, err := c.Append(lying, map[string]any{"real": "payload"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got.PayloadHash == lying.PayloadHash {
		t.Fatal("Append trusted the caller's payload hash instead of computing it")
	}
	if err := VerifyPayload(got, map[string]any{"real": "payload"}); err != nil {
		t.Fatalf("the computed payload hash must verify against the real payload: %v", err)
	}
}

func TestVerifyChainAcceptsAWellFormedChain(t *testing.T) {
	c := NewChain()
	for i, et := range []string{"case.opened", "evidence.ingested", "qualification.recorded"} {
		e := validEnvelope()
		e.EventID = string(rune('A'+i)) + "-1"
		e.EventType = et
		if _, err := c.Append(e, map[string]any{"n": i}); err != nil {
			t.Fatalf("Append %s: %v", et, err)
		}
	}
	if err := VerifyChain(c.Events()); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestVerifyChainDetectsATamperedField is MIP §23's "ledger tampering"
// adversarial case: altering any hashed field breaks re-derivation.
func TestVerifyChainDetectsATamperedField(t *testing.T) {
	c := NewChain()
	for i := 0; i < 3; i++ {
		e := validEnvelope()
		e.EventID = "EV-" + string(rune('1'+i))
		if _, err := c.Append(e, map[string]any{"n": i}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	events := c.Events()
	events[1].Purpose = "quietly-changed"

	err := VerifyChain(events)
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch for a tampered field, got %v", err)
	}
}

// TestVerifyChainDetectsARemovedEvent proves deletion is detectable —
// the sequence and linkage both break.
func TestVerifyChainDetectsARemovedEvent(t *testing.T) {
	c := NewChain()
	for i := 0; i < 3; i++ {
		e := validEnvelope()
		e.EventID = "EV-" + string(rune('1'+i))
		if _, err := c.Append(e, nil); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	events := c.Events()
	shortened := []Envelope{events[0], events[2]} // middle event removed

	if err := VerifyChain(shortened); err == nil {
		t.Fatal("removing an event from the middle must break verification")
	}
}

// TestVerifyChainDetectsReordering proves the chain is order-bound.
func TestVerifyChainDetectsReordering(t *testing.T) {
	c := NewChain()
	for i := 0; i < 3; i++ {
		e := validEnvelope()
		e.EventID = "EV-" + string(rune('1'+i))
		if _, err := c.Append(e, nil); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	events := c.Events()
	events[0], events[1] = events[1], events[0]

	if err := VerifyChain(events); err == nil {
		t.Fatal("reordering events must break verification")
	}
}

// TestVerifyPayloadDetectsASwappedPayload covers the case a perfect
// chain cannot catch: the linkage is intact but the payload behind a
// recorded hash was replaced.
func TestVerifyPayloadDetectsASwappedPayload(t *testing.T) {
	c := NewChain()
	e, err := c.Append(validEnvelope(), map[string]any{"amount": 100})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := VerifyChain(c.Events()); err != nil {
		t.Fatalf("the chain itself is intact: %v", err)
	}
	if err := VerifyPayload(e, map[string]any{"amount": 999}); !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("a swapped payload must fail VerifyPayload, got %v", err)
	}
}

// TestEventHashIsNotPartOfWhatItHashes proves the hash-then-bind rule.
func TestEventHashIsNotPartOfWhatItHashes(t *testing.T) {
	e := validEnvelope()
	e.PayloadHash = "deadbeef"
	e.PreviousEventHash = GenesisHash

	e.EventHash = ""
	h1, err := ComputeEventHash(e)
	if err != nil {
		t.Fatalf("ComputeEventHash: %v", err)
	}
	e.EventHash = h1
	h2, err := ComputeEventHash(e)
	if err != nil {
		t.Fatalf("ComputeEventHash: %v", err)
	}
	if h1 != h2 {
		t.Fatal("setting EventHash changed the computed hash; the hash is inside its own preimage")
	}
}

// TestSignatureIsNotPartOfWhatItSigns is the same rule for the
// authority signature.
func TestSignatureIsNotPartOfWhatItSigns(t *testing.T) {
	e := validEnvelope()
	e.PayloadHash = "deadbeef"
	before, err := ComputeEventHash(e)
	if err != nil {
		t.Fatalf("ComputeEventHash: %v", err)
	}
	e.AuthoritySignature = "ed25519:abc123"
	after, err := ComputeEventHash(e)
	if err != nil {
		t.Fatalf("ComputeEventHash: %v", err)
	}
	if before != after {
		t.Fatal("the authority signature entered the event hash; a signature cannot be inside what it signs")
	}
}

func TestHashesAreDeterministicAcrossRuns(t *testing.T) {
	c1, c2 := NewChain(), NewChain()
	p := map[string]any{"z": 1, "a": 2, "m": 3} // deliberately unordered
	e1, err := c1.Append(validEnvelope(), p)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	e2, err := c2.Append(validEnvelope(), map[string]any{"m": 3, "a": 2, "z": 1})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if e1.PayloadHash != e2.PayloadHash {
		t.Fatal("payload hashing is not canonical: key order changed the hash")
	}
	if e1.EventHash != e2.EventHash {
		t.Fatal("event hashing is not deterministic across chains with identical content")
	}
}

// --- Validation ---

func TestValidateRejectsUnknownEventFamily(t *testing.T) {
	e := validEnvelope()
	e.EventType = "telepathy.happened"
	if err := Validate(e); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("expected ErrUnknownFamily, got %v", err)
	}
}

func TestValidateAcceptsEveryCanonicalFamily(t *testing.T) {
	for _, fam := range Families() {
		e := validEnvelope()
		e.EventType = fam + ".something_happened"
		if err := Validate(e); err != nil {
			t.Fatalf("family %q must be accepted, got %v", fam, err)
		}
	}
}

func TestValidateRejectsMalformedEventType(t *testing.T) {
	for _, bad := range []string{"evidence", ".ingested", "evidence.", ""} {
		e := validEnvelope()
		e.EventType = bad
		if err := Validate(e); err == nil {
			t.Fatalf("event type %q must be rejected", bad)
		}
	}
}

// TestValidateRequiresPolicyVersion enforces Article 7's precondition:
// an event with no governing policy version cannot be replayed under
// the policy that governed it.
func TestValidateRequiresPolicyVersion(t *testing.T) {
	e := validEnvelope()
	e.PolicyVersion = ""
	if err := Validate(e); !errors.Is(err, ErrEmptyPolicyVersion) {
		t.Fatalf("expected ErrEmptyPolicyVersion, got %v", err)
	}
}

func TestValidateRejectsUnknownActorType(t *testing.T) {
	e := validEnvelope()
	e.ActorType = "ROBOT"
	if err := Validate(e); !errors.Is(err, ErrUnknownActorType) {
		t.Fatalf("expected ErrUnknownActorType, got %v", err)
	}
}

func TestAppendRejectsAnInvalidEnvelope(t *testing.T) {
	c := NewChain()
	bad := validEnvelope()
	bad.TenantID = ""
	if _, err := c.Append(bad, nil); !errors.Is(err, ErrEmptyTenantID) {
		t.Fatalf("expected ErrEmptyTenantID, got %v", err)
	}
	if c.Len() != 0 {
		t.Fatal("a rejected envelope must not be appended")
	}
}

// --- Query helpers ---

// TestAIEventsAreSeparatelyIdentifiable is Article 27's auditability
// property: AI influence must be answerable from the log alone.
func TestAIEventsAreSeparatelyIdentifiable(t *testing.T) {
	c := NewChain()
	human := validEnvelope()
	human.ActorType = ActorHuman
	human.EventType = "qualification.approved"
	if _, err := c.Append(human, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	ai := validEnvelope()
	ai.EventID = "EV-AI"
	ai.ActorType = ActorAI
	ai.ActorID = "aureum-v1"
	ai.EventType = "ai.hypothesis_proposed"
	if _, err := c.Append(ai, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := AIEvents(c.Events())
	if len(got) != 1 || got[0].ActorID != "aureum-v1" {
		t.Fatalf("expected exactly the one AI event, got %+v", got)
	}
}

// TestPolicyVersionsSurfaceRetroactivity supports Article 26: more
// than one policy version across a case is the signal to look at.
func TestPolicyVersionsSurfaceRetroactivity(t *testing.T) {
	c := NewChain()
	a := validEnvelope()
	a.PolicyVersion = "policy-2025-01"
	if _, err := c.Append(a, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	b := validEnvelope()
	b.EventID = "EV-2"
	b.PolicyVersion = "policy-2026-09"
	if _, err := c.Append(b, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := PolicyVersions(c.Events())
	if len(got) != 2 || got[0] != "policy-2025-01" || got[1] != "policy-2026-09" {
		t.Fatalf("expected both policy versions sorted, got %v", got)
	}
}

func TestFamilyCountsSummarizesAChain(t *testing.T) {
	c := NewChain()
	for _, et := range []string{"evidence.ingested", "evidence.finalized", "disclosure.granted"} {
		e := validEnvelope()
		e.EventType = et
		if _, err := c.Append(e, nil); err != nil {
			t.Fatalf("Append %s: %v", et, err)
		}
	}
	counts := FamilyCounts(c.Events())
	if counts["evidence"] != 2 || counts["disclosure"] != 1 {
		t.Fatalf("unexpected family counts: %v", counts)
	}
}

func TestEventsReturnsACopy(t *testing.T) {
	c := NewChain()
	if _, err := c.Append(validEnvelope(), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got := c.Events()
	got[0].EventID = "MUTATED"
	if c.Events()[0].EventID == "MUTATED" {
		t.Fatal("Events() exposed the chain's own backing array")
	}
}
