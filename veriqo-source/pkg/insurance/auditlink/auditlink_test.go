package auditlink

import (
	"testing"

	caseinsurance "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/payment"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/reserve"
	"veriqo/pkg/platform/audit"
)

func TestMirrorCaseAppendsEveryTransition(t *testing.T) {
	c, err := caseinsurance.New("CASE-AUDIT-1", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Advance(caseinsurance.StatePartiesIdentified, 10); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := c.Advance(caseinsurance.StatePolicyRegistered, 20); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	store := audit.NewAuditStore()
	recs, err := MirrorCase(store, c, "CASE-AUDIT-1")
	if err != nil {
		t.Fatalf("MirrorCase: %v", err)
	}
	if len(recs) != len(c.StateLog()) {
		t.Fatalf("expected %d mirrored records, got %d", len(c.StateLog()), len(recs))
	}
	if len(store.Snapshot()) != 2 {
		t.Fatalf("expected 2 records in store, got %d", len(store.Snapshot()))
	}
	if err := VerifyUnified(store); err != nil {
		t.Fatalf("VerifyUnified: %v", err)
	}
}

func TestMirrorPaymentHistoryAppendsEveryEvent(t *testing.T) {
	p, err := payment.New("PAY-AUDIT-1", "CLM-1", "CASE-1", "PTY-PAYEE", quantum.Amount(1000), "IDEM-1", "PTY-CREATOR", "created", 10)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	if _, err := p.Authorize("PTY-AUTHORIZER", party.RoleInsurer, "authorized", 20); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	store := audit.NewAuditStore()
	recs, err := MirrorPaymentHistory(store, p)
	if err != nil {
		t.Fatalf("MirrorPaymentHistory: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 mirrored records (create+authorize), got %d", len(recs))
	}
	if err := VerifyUnified(store); err != nil {
		t.Fatalf("VerifyUnified: %v", err)
	}
}

func TestMirrorReserveHistoryAppendsEveryEntry(t *testing.T) {
	r, err := reserve.New("RSV-AUDIT-1", "CLM-1", "CASE-1", quantum.Amount(5000), "PTY-INSURER", party.RoleInsurer, "opened", 10)
	if err != nil {
		t.Fatalf("reserve.New: %v", err)
	}
	if err := r.Approve("PTY-HANDLER", party.RoleClaimsHandler, 20); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	store := audit.NewAuditStore()
	recs, err := MirrorReserveHistory(store, r)
	if err != nil {
		t.Fatalf("MirrorReserveHistory: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 mirrored records (set+approve), got %d", len(recs))
	}
}

func TestMirroringMultipleDomainsIntoOneStoreStaysOneVerifiableChain(t *testing.T) {
	// The crux of "unified audit": case, payment, and reserve events all
	// land in the SAME AuditStore and the WHOLE chain (not per-domain
	// sub-chains) still verifies as one hash-linked ledger.
	c, err := caseinsurance.New("CASE-UNIFIED-1", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Advance(caseinsurance.StatePartiesIdentified, 10); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	p, err := payment.New("PAY-UNIFIED-1", "CLM-1", "CASE-UNIFIED-1", "PTY-PAYEE", quantum.Amount(1000), "IDEM-1", "PTY-CREATOR", "created", 20)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	r, err := reserve.New("RSV-UNIFIED-1", "CLM-1", "CASE-UNIFIED-1", quantum.Amount(1000), "PTY-INSURER", party.RoleInsurer, "opened", 30)
	if err != nil {
		t.Fatalf("reserve.New: %v", err)
	}

	store := audit.NewAuditStore()
	if _, err := MirrorCase(store, c, "CASE-UNIFIED-1"); err != nil {
		t.Fatalf("MirrorCase: %v", err)
	}
	if _, err := MirrorPaymentHistory(store, p); err != nil {
		t.Fatalf("MirrorPaymentHistory: %v", err)
	}
	if _, err := MirrorReserveHistory(store, r); err != nil {
		t.Fatalf("MirrorReserveHistory: %v", err)
	}

	snap := store.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 total records across all three domains, got %d", len(snap))
	}
	if err := VerifyUnified(store); err != nil {
		t.Fatalf("expected the whole multi-domain chain to verify as ONE ledger: %v", err)
	}
	// Every record's index is strictly sequential across domains -- proof
	// there is one shared chain, not three independent ones that happen
	// to share a Go value.
	for i, rec := range snap {
		if rec.Index != uint64(i)+1 {
			t.Fatalf("expected sequential index %d, got %d", i+1, rec.Index)
		}
	}
}

func TestMirrorCaseRefusesNilCase(t *testing.T) {
	store := audit.NewAuditStore()
	if _, err := MirrorCase(store, nil, "X"); err == nil {
		t.Fatal("expected MirrorCase to refuse a nil case")
	}
}
