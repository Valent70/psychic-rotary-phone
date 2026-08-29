package decision

import (
	"fmt"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/platform/audit"
)

// AppendToLedger is the ONLY function in this package that writes a
// Decision into the real, hash-chained audit ledger
// (pkg/platform/audit.AuditStore -- the same Merkle-anchored ledger
// VTECP's own "Ledger Merkle + Anchor + Verify API" work built).
// Refuses the zero Decision (nothing authoritative to record), so the
// ledger can never contain an entry claiming to be a decision that was
// never actually produced by MakeDecision.
//
// The payload written is d's own ToAuditPayload snapshot,
// JCS-canonicalized -- the same deterministic, sorted-key encoding
// every other authority-bearing hash in this repository uses, so that
// two independent replays of the identical Decision produce the
// identical ledger payload string (and therefore the identical
// AuditRecord.Hash and, eventually, the identical ledger RootHash).
// This is what makes "Replay Closure" (same input -> ... -> same ledger
// artifact) a checkable claim rather than an assertion: see
// test/integration's replay-closure test.
func AppendToLedger(store *audit.AuditStore, actor string, d Decision) (audit.AuditRecord, error) {
	if store == nil {
		return audit.AuditRecord{}, fmt.Errorf("decision: AppendToLedger requires a non-nil ledger store")
	}
	payload, err := d.ToAuditPayload()
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("decision: AppendToLedger: %w", err)
	}
	canon, err := jcs.Canonicalize(payload)
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("decision: AppendToLedger: canonicalizing payload: %w", err)
	}
	return store.Append(actor, "DECISION_RECORDED", string(canon))
}
