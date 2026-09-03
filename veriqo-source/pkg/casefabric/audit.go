package casefabric

import (
	"encoding/json"
	"fmt"

	"veriqo/pkg/contract/event"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/proof"
)

// This file closes the audit-unification gap for the fabric layer.
//
// The gap was never that VERIQO had two audit ledgers — pkg/platform/audit
// has always been the single append point, and pkg/insurance/auditlink
// mirrors the insurance domain into it. The gap was that the mirror was
// domain-specific: a canonical case, which by construction is not
// insurance's, had nowhere to go.
//
// So this file mirrors a case into the same store through the same
// canonical envelope every other subsystem uses. It does not create a
// ledger, a store, or a second notion of an audit event. If you are
// looking for where fabric events are kept, the answer is the one
// AuditStore, same as everything else.

// FabricAggregateType is the aggregate type canonical case events carry.
const FabricAggregateType = "CanonicalCase"

// eventTypeFor maps a timeline entry kind to a canonical event type.
//
// The mapping is explicit rather than derived from the kind string,
// because an unmapped kind must be a build-time omission somebody
// notices, not a silently-passed-through label.
var eventTypeFor = map[string]string{
	"case_opened":         "case.opened",
	"scope_set":           "case.scoped",
	"evidence_added":      "case.evidence_pinned",
	"hypothesis_added":    "case.hypothesis_recorded",
	"hypothesis_tested":   "case.hypothesis_tested",
	"claim_registered":    "case.claim_registered",
	"qualification_begun": "case.qualification_begun",
	"proof_attached":      "case.proof_attached",
	"case_resolved":       "case.resolved",
	"case_suspended":      "case.suspended",
	"case_closed":         "case.closed",
	"case_reopened":       "case.reopened",
	"domain_state_synced": "case.domain_state_synced",
}

// EventTypeFor exposes the mapping so tests can assert every timeline
// kind the case engine can produce has a canonical event type.
func EventTypeFor(kind string) (string, bool) {
	t, ok := eventTypeFor[kind]
	return t, ok
}

// Mirror writes a case's timeline into the canonical audit store and
// returns the canonical event chain it produced.
//
// Both outputs matter. The audit records are what the existing ledger,
// its Merkle root and its independent verifier already understand; the
// event chain is the canonical envelope form with its own hash linkage.
// They are two views of one history, not two histories: every envelope
// is derived from the timeline entry that produced its audit record.
func Mirror(store *audit.AuditStore, c *Case, policyVersion string) ([]audit.AuditRecord, *event.Chain, error) {
	if store == nil {
		return nil, nil, fmt.Errorf("casefabric: no audit store")
	}
	if err := c.VerifyTimeline(); err != nil {
		// A case whose own timeline does not verify must not be
		// mirrored: writing an unverifiable history into the canonical
		// ledger would contaminate the one record everything else
		// trusts.
		return nil, nil, fmt.Errorf("casefabric: refusing to mirror an unverified timeline: %w", err)
	}

	id := c.Identity()
	chain := event.NewChain()
	var records []audit.AuditRecord

	for _, e := range c.Timeline() {
		eventType, ok := eventTypeFor[e.Kind]
		if !ok {
			return nil, nil, fmt.Errorf("casefabric: timeline kind %q has no canonical event type", e.Kind)
		}

		payload := map[string]any{
			"case_id": id.CaseID, "domain": id.Domain, "phase": string(e.Phase),
			"kind": e.Kind, "description": e.Description,
			"sequence_no": e.SequenceNo, "entry_hash": e.EntryHash,
		}
		env, err := chain.Append(event.Envelope{
			EventID:  fmt.Sprintf("%s-%d", id.CaseID, e.SequenceNo),
			TenantID: id.TenantID, CaseID: id.CaseID,
			EventType: eventType, AggregateType: FabricAggregateType, AggregateID: id.CaseID,
			ActorID: e.Actor, ActorType: event.ActorHuman,
			PolicyVersion: policyVersion,
			OccurredAt:    e.Tick, RecordedAt: e.Tick,
		}, payload)
		if err != nil {
			return nil, nil, fmt.Errorf("casefabric: canonical event: %w", err)
		}

		body, err := json.Marshal(map[string]any{
			"envelope": env, "payload": payload,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("casefabric: audit payload: %w", err)
		}
		rec, err := store.Append(e.Actor, eventType, string(body))
		if err != nil {
			return nil, nil, fmt.Errorf("casefabric: audit append: %w", err)
		}
		records = append(records, rec)
	}
	return records, chain, nil
}

// MirrorProof writes a sealed proof object's identity into the canonical
// ledger.
//
// Only the object's identity and verdicts go in — its hash, stance,
// sufficiency, qualification state and the count of evidence versions.
// The evidence itself does not: the ledger is a record that a
// conclusion was reached on a stated basis, not a second copy of the
// evidence, and copying evidence into an audit log is how disclosure
// controls get bypassed.
func MirrorProof(store *audit.AuditStore, actor string, o proof.Object) (audit.AuditRecord, error) {
	if store == nil {
		return audit.AuditRecord{}, fmt.Errorf("casefabric: no audit store")
	}
	if err := proof.VerifyHash(o); err != nil {
		return audit.AuditRecord{}, err
	}
	body, err := json.Marshal(map[string]any{
		"proof_hash": o.CanonicalHash, "case_id": o.Scope.CaseID,
		"proposition_id": o.Proposition.ID, "stance": o.Stance.String(),
		"sufficiency": o.Sufficiency.String(), "qualification": string(o.Qualification.State),
		"evidence_version_count": len(o.EvidenceSet),
		"external_qualification": o.ExternalQualification.Status.String(),
		"authority":              o.Authority.AuthorityID, "policy_version": o.Authority.PolicyVersion,
		"limitation_count": len(o.Limitations),
	})
	if err != nil {
		return audit.AuditRecord{}, err
	}
	return store.Append(actor, "proof.sealed", string(body))
}
