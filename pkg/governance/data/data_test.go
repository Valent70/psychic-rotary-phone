package data

import (
	"errors"
	"testing"
)

func engine(t *testing.T) *Engine {
	t.Helper()
	e := New()
	if err := e.SetPolicy(Policy{Tenant: "acme", EvidenceClass: "AIS", Class: ClassConfidential,
		RetentionTicks: 100, GraceTicks: 10, RedactFields: []string{"master_name"}, Version: 1}); err != nil {
		t.Fatal(err)
	}
	return e
}

func fields() []Field {
	return []Field{
		{Name: "mmsi", Value: "123456789", PII: PIINone},
		{Name: "master_name", Value: "A. Bramantyo", PII: PIIName},
		{Name: "lat", Value: "-4.01", PII: PIINone},
	}
}

func TestIngestWithoutPolicyIsRefused(t *testing.T) {
	e := New()
	if _, err := e.Ingest("r1", "acme", "AIS", fields(), 1); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("expected policy requirement, got %v", err)
	}
}

func TestRetentionAdvancesOnlyWhenDue(t *testing.T) {
	e := engine(t)
	if _, err := e.Ingest("r1", "acme", "AIS", fields(), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(50); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Record("r1")
	if r.State != StateActive {
		t.Fatalf("record must stay ACTIVE before retention elapses, got %s", r.State)
	}
	if _, err := e.Evaluate(110); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Record("r1")
	if r.State != StateRedactionRequired {
		t.Fatalf("a PII record past retention should land on REDACTION_REQUIRED, got %s", r.State)
	}
}

func TestTTLOverridesALongerRetention(t *testing.T) {
	e := New()
	_ = e.SetPolicy(Policy{Tenant: "t", EvidenceClass: "SAR", RetentionTicks: 1000,
		TTLTicks: 20, GraceTicks: 0, Version: 1})
	_, _ = e.Ingest("r", "t", "SAR", []Field{{Name: "scene", Value: "x"}}, 0)
	_, _ = e.Evaluate(25)
	r, _ := e.Record("r")
	if r.State == StateActive {
		t.Fatal("a shorter TTL must override the longer retention period")
	}
}

func TestPIIForcesRedactionBeforePurge(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 0)
	_, _ = e.Evaluate(120)
	r, _ := e.Record("r1")
	if r.State != StateRedactionRequired {
		t.Fatalf("a PII-bearing record must route through redaction, got %s", r.State)
	}
	if _, err := e.Purge("r1", "dpo", "retention", 121); !errors.Is(err, ErrRedactionRequired) {
		t.Fatalf("purging un-redacted PII must be refused, got %v", err)
	}
}

func TestRedactionRemovesValuesButKeepsProof(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 0)
	_, _ = e.Evaluate(120)
	r, err := e.Redact("r1", "dpo", "GDPR erasure", 121)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Fields {
		if f.Name == "master_name" {
			if f.Value != "" || !f.Removed || f.PreImageHash == "" {
				t.Fatalf("PII field must be removed with a pre-image proof: %+v", f)
			}
		}
		if f.Name == "mmsi" && f.Removed {
			t.Fatal("a non-PII field must survive redaction")
		}
	}
	ok, err := e.VerifyRedactedField("r1", "master_name", "A. Bramantyo")
	if err != nil || !ok {
		t.Fatalf("the true original must verify against the pre-image: ok=%v err=%v", ok, err)
	}
	bad, _ := e.VerifyRedactedField("r1", "master_name", "Someone Else")
	if bad {
		t.Fatal("a false original must not verify")
	}
}

func TestPurgeEmitsATombstoneThatSurvivesTheContent(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 0)
	_, _ = e.Evaluate(120)
	_, _ = e.Redact("r1", "dpo", "erasure", 121)
	_, _ = e.Evaluate(130)
	r, _ := e.Record("r1")
	if r.State != StatePurgeEligible {
		t.Fatalf("expected PURGE_ELIGIBLE after grace, got %s", r.State)
	}
	originalHash := r.ContentHash

	ts, err := e.Purge("r1", "dpo", "retention expiry", 131)
	if err != nil {
		t.Fatal(err)
	}
	if ts.ContentHash != originalHash {
		t.Fatal("the tombstone must carry the ORIGINAL content hash")
	}
	after, _ := e.Record("r1")
	for _, f := range after.Fields {
		if f.Value != "" {
			t.Fatalf("no value may survive a purge: %+v", f)
		}
	}
	proof, tomb, err := e.ProveExistence("r1")
	if err != nil {
		t.Fatal(err)
	}
	if tomb == nil || proof != originalHash {
		t.Fatal("existence must remain provable after the purge")
	}
}

func TestLegalHoldBeatsRetention(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 0)
	if _, err := e.PlaceHold(Hold{ID: "h1", Matter: "OFAC-2026-11", Tenant: "acme",
		Actor: "legal", Reason: "investigation"}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(500); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Record("r1")
	if r.State != StateHeld {
		t.Fatalf("a held record must never advance, got %s", r.State)
	}
	if _, err := e.Purge("r1", "dpo", "retention", 501); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("purging under hold must be refused, got %v", err)
	}
	if _, err := e.Redact("r1", "dpo", "retention", 502); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("redacting under hold must be refused, got %v", err)
	}
}

func TestReleasingAHoldRestartsTheClockRatherThanPurgingImmediately(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 0)
	_, _ = e.PlaceHold(Hold{ID: "h1", Matter: "m", Tenant: "acme", Actor: "legal"}, 5)
	_, _ = e.Evaluate(5000)
	if err := e.ReleaseHold("h1", "legal", "matter closed", 5001); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Record("r1")
	if r.State != StateActive {
		t.Fatalf("release must return the record to ACTIVE, got %s", r.State)
	}
	if _, err := e.Purge("r1", "dpo", "", 5002); err == nil {
		t.Fatal("a just-released record must not be immediately purgeable")
	}
}

func TestTwoHoldsBothMustBeReleased(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 0)
	_, _ = e.PlaceHold(Hold{ID: "h1", Matter: "m1", Tenant: "acme", Actor: "legal"}, 1)
	_, _ = e.PlaceHold(Hold{ID: "h2", Matter: "m2", Tenant: "acme", Actor: "legal"}, 2)
	_ = e.ReleaseHold("h1", "legal", "", 3)
	r, _ := e.Record("r1")
	if r.State != StateHeld {
		t.Fatalf("one remaining hold must keep the record HELD, got %s", r.State)
	}
	_ = e.ReleaseHold("h2", "legal", "", 4)
	r, _ = e.Record("r1")
	if r.State != StateActive {
		t.Fatalf("releasing the last hold must free the record, got %s", r.State)
	}
}

func TestHoldScopedToOneClassDoesNotCatchAnother(t *testing.T) {
	e := engine(t)
	_ = e.SetPolicy(Policy{Tenant: "acme", EvidenceClass: "BOL", RetentionTicks: 10, Version: 1})
	_, _ = e.Ingest("ais", "acme", "AIS", fields(), 0)
	_, _ = e.Ingest("bol", "acme", "BOL", []Field{{Name: "bill_id", Value: "B1"}}, 0)
	_, _ = e.PlaceHold(Hold{ID: "h", Matter: "m", Tenant: "acme", Class: "BOL", Actor: "legal"}, 1)
	a, _ := e.Record("ais")
	b, _ := e.Record("bol")
	if a.State == StateHeld {
		t.Fatal("a class-scoped hold must not catch another class")
	}
	if b.State != StateHeld {
		t.Fatal("the scoped class must be held")
	}
}

func TestPurgedRecordIsTerminal(t *testing.T) {
	e := New()
	_ = e.SetPolicy(Policy{Tenant: "t", EvidenceClass: "C", RetentionTicks: 1, Version: 1})
	_, _ = e.Ingest("r", "t", "C", []Field{{Name: "a", Value: "1"}}, 0)
	_, _ = e.Evaluate(5)
	_, err := e.Purge("r", "dpo", "expiry", 6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Purge("r", "dpo", "again", 7); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("double purge must be refused, got %v", err)
	}
	if _, err := e.PlaceHold(Hold{ID: "h", Matter: "m", Tenant: "t", Actor: "legal"}, 8); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Record("r")
	if r.State != StatePurged {
		t.Fatalf("a purged record must stay purged even under a later hold, got %s", r.State)
	}
}

func TestDuplicateIngestRejected(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 1)
	if _, err := e.Ingest("r1", "acme", "AIS", fields(), 2); !errors.Is(err, ErrDuplicateRecord) {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}

func TestBackdatedGovernanceRejected(t *testing.T) {
	e := engine(t)
	_, _ = e.Ingest("r1", "acme", "AIS", fields(), 100)
	if _, err := e.Evaluate(50); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("expected monotonic tick rejection, got %v", err)
	}
}

func TestEvaluateIsDeterministicAndTheLedgerIsTamperEvident(t *testing.T) {
	build := func() *Engine {
		e := engine(t)
		_, _ = e.Ingest("a", "acme", "AIS", fields(), 0)
		_, _ = e.Ingest("b", "acme", "AIS", fields(), 0)
		_, _ = e.Evaluate(120)
		_, _ = e.Redact("a", "dpo", "x", 121)
		return e
	}
	e1, e2 := build(), build()
	if e1.Head() != e2.Head() {
		t.Fatal("two identical governance runs must produce identical ledger heads")
	}
	if err := e1.VerifyChain(); err != nil {
		t.Fatal(err)
	}
	e1.mu.Lock()
	e1.ledger[1].Actor = "impostor"
	e1.mu.Unlock()
	if err := e1.VerifyChain(); !errors.Is(err, ErrLedgerTampered) {
		t.Fatalf("expected tamper detection, got %v", err)
	}
}

func TestContentHashIsNeverRecomputedAfterRedaction(t *testing.T) {
	e := engine(t)
	r0, _ := e.Ingest("r1", "acme", "AIS", fields(), 0)
	_, _ = e.Evaluate(120)
	r1, _ := e.Redact("r1", "dpo", "x", 121)
	if r1.ContentHash != r0.ContentHash {
		t.Fatal("redaction must not rewrite the original content hash")
	}
	if r1.RedactionHash == "" || r1.RedactionHash == r0.ContentHash {
		t.Fatal("a distinct post-redaction hash must exist")
	}
}

func TestUnknownRecordAndHoldAreErrors(t *testing.T) {
	e := engine(t)
	if _, err := e.Redact("ghost", "a", "", 1); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("expected unknown record, got %v", err)
	}
	if _, _, err := e.ProveExistence("ghost"); !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("expected unknown record, got %v", err)
	}
	if err := e.ReleaseHold("ghost", "a", "", 2); !errors.Is(err, ErrUnknownHold) {
		t.Fatalf("expected unknown hold, got %v", err)
	}
}
