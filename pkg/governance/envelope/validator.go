package envelope

// PHASE E4 (P1-16) — the External Evidence Validator, as an
// INTEGRATION rather than a new implementation.
//
// Reconciliation first, per rule 0. The program asks for a validator
// that checks signature, provider, reviewer, commit, source hash,
// environment, artifact hash, expiry, revocation and gate
// authorization. Nine of those ten already exist, real and tested, in
// pkg/governance/qualification.Registry.Validate — which independently
// recomputes the artifact hash, resolves provider and reviewer against
// an Ed25519 trust registry (rejecting unknown, revoked or
// out-of-window credentials), verifies BOTH signatures against those
// registered keys, checks gate authorization, checks expiry, and
// rejects evidence bound to a different commit or source hash.
//
// Building a second validator would be exactly the duplicate
// abstraction rule 0 forbids. So this file does not build one. It
// builds the bridge that was actually missing:
//
//	Envelope --> qualification.ExternalEvidence --> Registry.Validate
//	         \-> Freshness (binary_hash / artifact_root / valid_until)
//	         \-> Check     (fixture-vs-LIVE, environment allow-list)
//
// The three checks on the right-hand branches are the ones
// qualification.Validate genuinely does NOT perform, because its
// ExternalEvidence type has no binary_hash, no artifact root, no
// validity-window start, and — the one that matters most here — no way
// at all to say "this evidence is a fixture". Submit runs all of them
// and refuses if any fails.
//
// What this does NOT do, deliberately and per the program's explicit
// instruction: it does not promote any of the eight externally-blocked
// gates. It makes their (still external, still absent) evidence
// CHECKABLE. With no real vendor evidence in existence, every one of
// those gates stays exactly as blocked after this file as before it.

import (
	"errors"
	"fmt"
	"strings"

	"veriqo/pkg/governance/qualification"
)

// ErrNotFresh is returned when an envelope fails the PHASE E2
// freshness gate. It is kept distinct from every rejection reason in
// Check so a caller can tell "this evidence describes a different
// build" from "this evidence is not trustworthy".
var ErrNotFresh = errors.New("envelope: evidence is not fresh for this release")

// SubmissionVerdict is the combined result of putting one envelope
// through every check: this package's own, and the existing
// qualification registry's.
type SubmissionVerdict struct {
	GateID     string           `json:"gate_id"`
	EnvelopeID string           `json:"envelope_id"`
	Accepted   bool             `json:"accepted"`
	Envelope   Verdict          `json:"envelope_verdict"`
	Freshness  FreshnessVerdict `json:"freshness_verdict"`
	// QualificationStatus is the registry's own status for this gate
	// AFTER the submission attempt — the authoritative answer, read
	// from the registry rather than inferred from whether Submit
	// returned an error.
	QualificationStatus qualification.Status `json:"qualification_status"`
	// Reasons lists every refusal, in the order the checks ran.
	Reasons []string `json:"reasons,omitempty"`
}

// ToQualificationEvidence projects an envelope into the shape the
// existing registry consumes. It is a pure field mapping and never an
// upgrade of trust: the signatures, provider and reviewer identities
// carried here are exactly the ones the envelope arrived with, and the
// registry re-verifies every one of them against its own trust anchors.
//
// ArtifactHash is deliberately left for the caller to compute via
// qualification.NewEvidence, because that function is the one place
// that knows the registry's own canonical hash preimage — recomputing
// it here would create a second definition of the same hash, and the
// two would drift.
func (e Envelope) ToQualificationEvidence(providerSig, reviewerSig string, submittedAtTick uint64) qualification.ExternalEvidence {
	measurement := make(map[string]string, len(e.Measurement)+4)
	for k, v := range e.Measurement {
		measurement[k] = v
	}
	// Fold the envelope-only fields into the measurement map so they are
	// covered by the registry's own artifact hash and therefore by both
	// signatures. Without this, binary_hash and the artifact root would
	// be editable after signing.
	measurement["envelope.binary_hash"] = e.BinaryHash
	measurement["envelope.sbom_hash"] = e.SBOMHash
	measurement["envelope.artifact_root_hash"] = e.ArtifactRootHash
	measurement["envelope.classification"] = string(e.Classification)
	measurement["envelope.origin_kind"] = string(e.OriginKind)
	measurement["envelope.rights_state"] = string(e.RightsState)
	measurement["envelope.attestation"] = string(e.Attestation)
	measurement["envelope.id"] = e.ID()
	if len(e.Limitations) > 0 {
		measurement["envelope.limitations"] = strings.Join(e.Limitations, "; ")
	}

	return qualification.NewEvidence(qualification.ExternalEvidence{
		GateID:          e.GateID,
		ProviderID:      e.ProviderID,
		ReviewerID:      e.ReviewerID,
		ReportID:        e.ID(),
		Scope:           e.GateID + " @ " + e.Release,
		Environment:     e.Environment,
		Measurement:     measurement,
		StartTick:       e.ValidFrom,
		EndTick:         e.ValidUntil,
		Commit:          e.Commit,
		SourceHash:      e.SourceHash,
		BuildHash:       e.BinaryHash,
		ExpiresAtTick:   e.ValidUntil,
		SubmittedAtTick: submittedAtTick,

		ProviderSignature: providerSig,
		ReviewerSignature: reviewerSig,
	})
}

// Submit runs the full chain for one envelope against one gate. It is
// the single entry point an operator uses when real external evidence
// finally arrives.
//
// Order matters and is deliberate. The envelope's OWN checks run
// first: a fixture claiming to be LIVE must be refused for that reason,
// before anything spends effort verifying a signature over it. Then
// freshness, then the registry's cryptographic and release-binding
// validation. A failure at any stage stops the chain and the reason is
// recorded.
//
// Submit never calls Qualify or VerifyGate. Advancing a gate past
// EVIDENCE_VALIDATED is an operator decision with a named person behind
// it, and quietly doing it here would be precisely the automated
// promotion this program forbids.
func (v Validator) Submit(reg *qualification.Registry, e Envelope, providerSig, reviewerSig string, nowTick uint64) (SubmissionVerdict, error) {
	out := SubmissionVerdict{GateID: e.GateID, EnvelopeID: e.ID()}
	if reg == nil {
		out.Reasons = append(out.Reasons, "no qualification registry supplied")
		return out, errors.New("envelope: a qualification registry is required")
	}

	// 1. The envelope's own checks (fixture-vs-LIVE, environment,
	//    release identity, provider/reviewer presence, window).
	envVerdict, err := v.Check(e, nowTick)
	out.Envelope = envVerdict
	if err != nil {
		out.Reasons = append(out.Reasons, envVerdict.Reasons...)
		out.QualificationStatus = statusOf(reg, e.GateID)
		return out, err
	}

	// 2. Freshness against the release being qualified.
	out.Freshness = Freshness(e, v.Release, nowTick)
	if out.Freshness.Status != FreshnessPass {
		out.Reasons = append(out.Reasons, out.Freshness.Mismatches...)
		out.QualificationStatus = statusOf(reg, e.GateID)
		return out, fmt.Errorf("%w: %s", ErrNotFresh, strings.Join(out.Freshness.Mismatches, "; "))
	}

	// 3. The existing registry's cryptographic and release-binding
	//    validation. This is where signature, provider, reviewer,
	//    revocation and gate authorization are actually checked -- by
	//    the code that already owned those checks.
	qe := e.ToQualificationEvidence(providerSig, reviewerSig, nowTick)
	if err := reg.SubmitEvidence(e.GateID, qe); err != nil {
		out.Reasons = append(out.Reasons, err.Error())
		out.QualificationStatus = statusOf(reg, e.GateID)
		return out, err
	}
	if err := reg.Validate(e.GateID, nowTick, v.Release.Commit, v.Release.SourceHash); err != nil {
		out.Reasons = append(out.Reasons, err.Error())
		out.QualificationStatus = statusOf(reg, e.GateID)
		return out, err
	}

	out.Accepted = true
	out.QualificationStatus = statusOf(reg, e.GateID)
	return out, nil
}

func statusOf(reg *qualification.Registry, gateID string) qualification.Status {
	rec, ok := reg.Get(gateID)
	if !ok {
		return ""
	}
	return rec.Status
}
