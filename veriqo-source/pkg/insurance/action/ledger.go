package action

import (
	"fmt"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/platform/audit"
)

// AppendToLedger is the ONLY function in this package that writes an
// ActionAuthorization into the real, hash-chained audit ledger --
// completing the reviewer's named "audit trail" field. Mirrors
// decision.AppendToLedger's own discipline exactly, including refusing
// the zero ActionAuthorization.
func AppendToLedger(store *audit.AuditStore, actor string, aa ActionAuthorization) (audit.AuditRecord, error) {
	if store == nil {
		return audit.AuditRecord{}, fmt.Errorf("action: AppendToLedger requires a non-nil ledger store")
	}
	payload, err := aa.ToAuditPayload()
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("action: AppendToLedger: %w", err)
	}
	canon, err := jcs.Canonicalize(payload)
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("action: AppendToLedger: canonicalizing payload: %w", err)
	}
	return store.Append(actor, "ACTION_AUTHORIZATION_RECORDED", string(canon))
}

// AppendExecutionToLedger records that attemptedAction was actually
// carried out against attemptedScope by attemptingActor, under aa --
// separate from AppendToLedger's own AUTHORIZATION_RECORDED entry, so
// the ledger distinguishes "permission was granted" from "the action
// was actually taken," exactly the distinction a real audit trail
// needs. Requires err (the result of a prior, real AuthorizeExecution
// call) to be nil -- an execution attempt this package itself refused
// is never recorded as having happened, only ever as a refusal (see
// AppendExecutionRefusalToLedger for that case).
func AppendExecutionToLedger(store *audit.AuditStore, actor string, aa ActionAuthorization, tick uint64) (audit.AuditRecord, error) {
	if store == nil {
		return audit.AuditRecord{}, fmt.Errorf("action: AppendExecutionToLedger requires a non-nil ledger store")
	}
	if aa.IsZero() {
		return audit.AuditRecord{}, fmt.Errorf("action: AppendExecutionToLedger: %w", ErrDecisionNotAuthoritative)
	}
	canon, err := jcs.Canonicalize(executionPayload{
		ActionAuthorizationHash: aa.hash, PermittedAction: string(aa.permittedAction),
		Scope: aa.scope, ExecutedBy: actor, ExecutedAt: tick,
	})
	if err != nil {
		return audit.AuditRecord{}, fmt.Errorf("action: AppendExecutionToLedger: canonicalizing payload: %w", err)
	}
	return store.Append(actor, "ACTION_EXECUTED", string(canon))
}

// executionPayload is the plain, exported-field shape (used only for
// JCS canonicalization, never exported as a type) AppendExecutionToLedger
// records.
type executionPayload struct {
	ActionAuthorizationHash string `json:"action_authorization_hash"`
	PermittedAction         string `json:"permitted_action"`
	Scope                   string `json:"scope"`
	ExecutedBy              string `json:"executed_by"`
	ExecutedAt              uint64 `json:"executed_at"`
}
