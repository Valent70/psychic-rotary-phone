// Package correlation is the honest, additive first slice of audit
// item "P0-F — Unified observability":
//
//	intent_id, execution_id, evidence_id, entity_id, decision_id,
//	verification_id, replay_id -> universal correlation identifiers
//
// P0-F itself is explicitly gated on P0-D ("Baru setelah execution path
// canonical" -- "only once the execution path is canonical"): a
// correlation record can only be as honest as the identifiers it
// correlates. A prior round's own investigation of P0-D found that
// intent and a genuinely independent decision identifier did not exist
// as real, per-execution values in pkg/execution's wired DAG.
//
// P0-7 closed the DecisionID half: pkg/execution.Engine.Run now derives
// DecisionID as a content-addressed hash over the ExecutionID plus the
// decision's own real content (subject, policy, action, risk score),
// not an alias of ExecutionID -- see execution.go's own decisionID
// function. IntentID remains empty from FromExecutionResult specifically
// (pkg/execution itself still never imports pkg/kernel/intent, so
// fabricating it at THIS layer would be exactly the kind of invented
// correlation this project's reports have consistently refused to
// produce) -- but pkg/lifecycle.Orchestrator.RunUnified, which DOES
// hold the originating Intent, now fills it in for real after calling
// FromExecutionResult (see lifecycle.go's Result.Correlation field),
// closing the audit's P1-07 "Unified Observability" gap at the one
// layer that actually has every one of these seven identifiers
// simultaneously -- and giving this package's Key its first real
// production caller anywhere in the repository (previously "sudah ada,
// tetapi belum menjadi universal runtime primitive" -- exists, but was
// never wired into anything real).
//
// What Key DOES provide, honestly: the six identifiers that ARE real
// and already produced by a single execution.Result today --
// ExecutionID, EvidencePackageID, EntityID (when produced through
// pkg/lifecycle after P0-B, this is pkg/identity's own canonical
// resolution, not a bare caller-supplied string -- see Key's own field
// comment), the now-independently-derived DecisionID, the replay
// identity, and the independent verification identity -- packaged as
// one struct any log line, telemetry span, or audit record can attach
// to join those six together. IntentID stays present as a field
// (matching the audit's named vocabulary exactly) but is deliberately
// left empty with FromExecutionResult, since no real value exists to
// put there yet.
//
// A later round gave pkg/execution.Engine an optional real identity-
// ledger binding (Engine.Identity, see execution.go's own doc comment):
// when set, IDENTITY_RESOLUTION's node hash commits to the resolver's
// own Head(). WithIdentityLedgerHead lets a caller that also holds that
// same *identity.Resolver attach the identical value here too -- one
// more real correlation identifier, still traceable to something the
// caller actually holds, not fabricated.
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
	// DecisionID is explanation.DecisionExplanation's own field. As of
	// P0-7 it is a genuinely independent, content-addressed identifier
	// (execution.go's decisionID function: a hash over ExecutionID plus
	// the decision's own subject/policy/action/risk-score), not an
	// alias of ExecutionID.
	DecisionID string
	// VerificationCertificateID is replay.VerificationCertificate's own
	// field -- the real, independent-replay-produced identity IVF
	// verification carries.
	VerificationCertificateID string
	// ReplayPackageID is replay.ReplayPackage's own field.
	ReplayPackageID string
	// EntityIdentityLedgerHead is empty unless the execution.Engine that
	// produced this Key's data had a non-nil Identity resolver bound
	// (see pkg/execution.Engine.Identity, added alongside P0-D's
	// IDENTITY_RESOLUTION binding). When set via WithIdentityLedgerHead,
	// it is the SAME real, verifiable *identity.Resolver.Head() value
	// pkg/execution's own IDENTITY_RESOLUTION stage already committed
	// into its node hash -- not a separate, disconnected commitment.
	EntityIdentityLedgerHead string
}

// WithIdentityLedgerHead returns a copy of k with
// EntityIdentityLedgerHead set. FromExecutionResult cannot populate
// this field itself -- execution.Result does not expose the identity
// resolver it was built with, only the DAG stage's already-hashed
// commitment to it -- so a caller that also holds the *identity.Resolver
// it bound into the Engine (via Engine.Identity) attaches it explicitly.
// This keeps Key's core invariant intact: every non-empty field still
// traces back to something real the caller actually holds, never a
// guessed or default value.
func (k Key) WithIdentityLedgerHead(head string) Key {
	k.EntityIdentityLedgerHead = head
	return k
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
