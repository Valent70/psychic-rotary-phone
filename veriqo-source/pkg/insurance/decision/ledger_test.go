package decision

import (
	"errors"
	"testing"

	"veriqo/pkg/platform/audit"
)

func TestAppendToLedgerRecordsARealDecision(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d, err := MakeDecision(af, OutcomeApproved, "ledger test", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	store := audit.NewAuditStore()
	rec, err := AppendToLedger(store, "decision-engine", d)
	if err != nil {
		t.Fatalf("AppendToLedger: %v", err)
	}
	if rec.Index != 1 {
		t.Fatalf("expected the first ledger entry to be index 1, got %d", rec.Index)
	}
	if err := (audit.Auditor{}).VerifyChain(store.Snapshot()); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestAppendToLedgerRefusesTheZeroDecision(t *testing.T) {
	store := audit.NewAuditStore()
	var zero Decision
	_, err := AppendToLedger(store, "decision-engine", zero)
	if !errors.Is(err, ErrFindingNotAuthorized) {
		t.Fatalf("expected ErrFindingNotAuthorized, got %v", err)
	}
	if len(store.Snapshot()) != 0 {
		t.Fatal("expected the ledger to remain empty after a refused append")
	}
}

func TestAppendToLedgerRefusesANilStore(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d, err := MakeDecision(af, OutcomeApproved, "nil store test", 1)
	if err != nil {
		t.Fatalf("MakeDecision: %v", err)
	}
	if _, err := AppendToLedger(nil, "decision-engine", d); err == nil {
		t.Fatal("expected AppendToLedger to refuse a nil store")
	}
}

func TestAppendToLedgerTwoIdenticalDecisionsProduceIdenticalPayloads(t *testing.T) {
	af := buildAuthorizedFinding(t)
	d1, err := MakeDecision(af, OutcomeApproved, "determinism check", 5)
	if err != nil {
		t.Fatalf("MakeDecision (1): %v", err)
	}
	d2, err := MakeDecision(af, OutcomeApproved, "determinism check", 5)
	if err != nil {
		t.Fatalf("MakeDecision (2): %v", err)
	}
	store1, store2 := audit.NewAuditStore(), audit.NewAuditStore()
	rec1, err := AppendToLedger(store1, "decision-engine", d1)
	if err != nil {
		t.Fatalf("AppendToLedger (1): %v", err)
	}
	rec2, err := AppendToLedger(store2, "decision-engine", d2)
	if err != nil {
		t.Fatalf("AppendToLedger (2): %v", err)
	}
	if rec1.Payload != rec2.Payload {
		t.Fatalf("expected identical Decisions to produce identical ledger payloads: %q != %q", rec1.Payload, rec2.Payload)
	}
	if rec1.Hash != rec2.Hash {
		t.Fatalf("expected identical Decisions appended to fresh stores to produce identical AuditRecord hashes: %s != %s", rec1.Hash, rec2.Hash)
	}
}
