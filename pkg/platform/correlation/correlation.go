// Package correlation is the honest, additive first slice of audit
// item "P0-F — Unified observability":
//
//	intent_id, execution_id, evidence_id, entity_id, decision_id,
//	verification_id, replay_id -> universal correlation identifiers
//
// P0-F itself is explicitly gated on P0-D ("Baru setelah execution path
// canonical" -- "only once the execution path is canonical"): a
// correlation record can only be as honest as the identifiers it
// correlates, and a prior round's own investigation of P0-D found that
// two of the audit's seven -- intent and a genuinely independent
// decision identifier -- do not exist as real, per-execution values in
// pkg/execution's wired DAG today (pkg/kernel/intent is never imported
// by pkg/execution, and DecisionID is currently set to ExecutionID
// itself, not a distinct identifier -- see execution.go's own Explain
// construction). Fabricating either here would be exactly the kind of
// invented correlation this project's reports have consistently
// refused to produce.
//
// What Key DOES provide, honestly: the five identifiers that ARE real
// and already produced by a single execution.Result today --
// ExecutionID, EvidencePackageID, EntityID (when produced through
// pkg/lifecycle after P0-B, this is pkg/identity's own canonical
// resolution, not a bare caller-supplied string -- see Key's own field
// comment), the replay identity, and the independent verification
// identity -- packaged as one struct any log line, telemetry span, or
// audit record can attach to join those five together. IntentID stays
// present as a field (matching the audit's named vocabulary exactly)
// but is deliberately left empty with FromExecutionResult, since no
// real value exists to put there yet.
package correlation

import "veriqo/pkg/execution"

// Key is one execution's correlation identifiers, using the audit's
// own field names. Every non-empty field here is a REAL identifier
// pkg/execution's Result already produced for THIS specific run -- Key
// invents nothing; it only packages what already exists.
type Key struct {
	// IntentID is always empty from FromExecutionResult: pkg/execution
	// has no wiring to pkg/kernel/intent today (see this package's own
	// doc comment). Present so callers that DO have an IntentID from
	// elsewhere in their own call chain (e.g. pkg/lifecycle.Intent.ID())
	// have a field to fill in without inventing a new key shape.
	IntentID string
	// ExecutionID is execution.Context's own identifier, the one ID
	// this pipeline already threads through Decision, Explanation and
	// Replay (see FromExecutionResult).
	ExecutionID string
	// EvidencePackageID is execution.Context's own field, set by
	// whatever built the Input (see pkg/execution's own Context.validate).
	EvidencePackageID string
	// EntityID is res.Canonical.Certificate.Subject -- the entity this
	// execution decided about. Its provenance depends on the caller:
	// when produced through pkg/lifecycle.Orchestrator.RunUnified (the
	// audit's P0-B canonical choke point), this is pkg/identity's own
	// resolved canonical ID for every alias Kind it models; when an
	// execution.Input is constructed some other way, it is only
	// whatever Subject string that caller supplied. Key does not (and
	// cannot) distinguish the two cases -- it reports the value, not a
	// provenance claim about it.
	EntityID string
	// DecisionID is explanation.DecisionExplanation's own field.
	// Currently always equal to ExecutionID (pkg/execution's Explain
	// construction sets DecisionID = ctx.ExecutionID, not an
	// independently-derived value) -- reported as-is, not disguised as
	// a distinct identifier it is not yet.
	DecisionID string
	// VerificationCertificateID is replay.VerificationCertificate's own
	// field -- the real, independent-replay-produced identity IVF
	// verification carries.
	VerificationCertificateID string
	// ReplayPackageID is replay.ReplayPackage's own field.
	ReplayPackageID string
}

// FromExecutionResult builds a Key from every identifier a single real
// execution.Result already carries. This is the ONLY constructor: Key
// has no other way to be populated with non-empty Execution/Evidence/
// Entity/Decision/Verification/Replay fields, so a Key can never claim
// an identifier that didn't come from an actual execution.
func FromExecutionResult(res execution.Result) Key {
	entityID := ""
	if res.Canonical != nil {
		entityID = res.Canonical.Certificate.Subject
	}
	return Key{
		ExecutionID:               res.Trace.Context.ExecutionID,
		EvidencePackageID:         res.Trace.Context.EvidencePackageID,
		EntityID:                  entityID,
		DecisionID:                res.Explanation.DecisionID,
		VerificationCertificateID: res.Certificate.VerificationCertificateID,
		ReplayPackageID:           res.ReplayPackage.ReplayPackageID,
	}
}
