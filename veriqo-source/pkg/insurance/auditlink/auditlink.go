// Package auditlink is the CANONICAL AUDIT EVENT layer this program's
// own Round 8 self-review (gap G2, "Unified Audit") found missing:
//
//	Insurance Dossier AuditTrail
//	            |
//	    Canonical Audit Event   <- this package
//	            |
//	      Platform Audit
//
// Before this package, pkg/insurance/dossier's own AuditTrail (built
// from a Case's StateLog) and pkg/platform/audit's AuditStore
// (the investor/auditor hash-chained ledger cmd/veriqo-node,
// cmd/veriqo-gateway and pkg/workflow already write to) were two
// INDEPENDENT truths: nothing anywhere translated one into the other,
// so a case's own lifecycle trail and the shared platform ledger could
// silently diverge with no way to detect it.
//
// auditlink is deliberately NOT a second audit engine (Final Design
// §39's "Jangan duplikasi logic" rule): it holds no storage of its own.
// It only MIRRORS already-computed insurance-domain events (a case's
// own StateLog, a payment's or reserve's own append-only history) into
// an existing audit.AuditStore, in one canonical event shape, so both
// the insurance domain's own trail and the platform's shared ledger
// become views over the SAME hash-chained record rather than two
// records that happen to agree today.
package auditlink

import (
	"encoding/json"
	"fmt"

	caseinsurance "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/payment"
	"veriqo/pkg/insurance/reserve"
	"veriqo/pkg/platform/audit"
)

// Domain names which insurance-domain subsystem a canonical event
// originated from — carried in the platform ledger's own Actor field
// (as "Domain:Subject") so a real auditor can filter the ONE ledger by
// subsystem without needing a second index.
type Domain string

const (
	DomainCase    Domain = "CASE"
	DomainPayment Domain = "PAYMENT"
	DomainReserve Domain = "RESERVE"
)

// detail is the deterministic JSON payload shape every canonical event
// mirrors into audit.AuditRecord.Payload — unexported: callers never
// construct one directly, only via this package's Mirror* functions,
// which is what keeps every mirrored event in the SAME shape regardless
// of which domain it came from.
type detail struct {
	Domain  Domain `json:"domain"`
	Subject string `json:"subject"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Tick    uint64 `json:"tick"`
}

func appendCanonical(store *audit.AuditStore, actor, action string, d detail) (audit.AuditRecord, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("auditlink: encoding canonical event: %w", err)
	}
	return store.Append(actor, action, string(payload))
}

// MirrorCase appends every one of c's own StateLog transitions into
// store, in canonical shape — the "Insurance Dossier AuditTrail" half
// of the unification. subject is normally the case's own CaseID.
// Deliberately reads c.StateLog() (already computed by case.Advance)
// rather than re-deriving lifecycle transitions from anywhere else.
func MirrorCase(store *audit.AuditStore, c *caseinsurance.Case, subject string) ([]audit.AuditRecord, error) {
	if c == nil {
		return nil, fmt.Errorf("auditlink: MirrorCase: case must not be nil")
	}
	actor := string(DomainCase) + ":" + subject
	var out []audit.AuditRecord
	for _, t := range c.StateLog() {
		rec, err := appendCanonical(store, actor, "STATE_TRANSITION", detail{
			Domain: DomainCase, Subject: subject, From: string(t.From), To: string(t.To), Tick: t.Tick,
		})
		if err != nil {
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// MirrorPaymentHistory appends every one of p's own PaymentEvent
// history entries into store, in canonical shape.
func MirrorPaymentHistory(store *audit.AuditStore, p *payment.PaymentRecord) ([]audit.AuditRecord, error) {
	if p == nil {
		return nil, fmt.Errorf("auditlink: MirrorPaymentHistory: payment record must not be nil")
	}
	actor := string(DomainPayment) + ":" + p.PaymentID
	var out []audit.AuditRecord
	for _, ev := range p.History() {
		rec, err := appendCanonical(store, actor, string(ev.Action), detail{
			Domain: DomainPayment, Subject: p.PaymentID, Reason: ev.Reason, Tick: ev.Tick,
		})
		if err != nil {
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// MirrorReserveHistory appends every one of r's own Entry history
// entries into store, in canonical shape.
func MirrorReserveHistory(store *audit.AuditStore, r *reserve.Reserve) ([]audit.AuditRecord, error) {
	if r == nil {
		return nil, fmt.Errorf("auditlink: MirrorReserveHistory: reserve must not be nil")
	}
	actor := string(DomainReserve) + ":" + r.ReserveID
	var out []audit.AuditRecord
	for _, ev := range r.History() {
		rec, err := appendCanonical(store, actor, string(ev.Action), detail{
			Domain: DomainReserve, Subject: r.ReserveID, Reason: ev.Reason, Tick: ev.Tick,
		})
		if err != nil {
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// VerifyUnified independently re-derives every hash in store's current
// snapshot and checks it against store's own reported RootHash — the
// same third-party verification audit.Auditor already provides,
// exposed here so a caller proving "no two independent audit truths"
// does not need to import pkg/platform/audit itself just to call it.
func VerifyUnified(store *audit.AuditStore) error {
	return audit.Auditor{}.VerifySnapshot(store.Snapshot(), store.RootHash())
}
