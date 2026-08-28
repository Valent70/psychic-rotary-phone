// Package finding implements the Case Resolution Engine's "Finding"
// object, per the CRE spec's own schema: SUPPORTED BY / CONTRADICTED
// BY / CONTRACT BASIS / OBLIGATION / EVENT / CAUSATION / QUANTUM /
// CONFIDENCE / HUMAN REVIEW / STATUS.
//
// Finding is deliberately a thin CITATION layer, not a new analysis
// engine: every field is a reference into (or a value copied from) an
// object some other, already-existing package computed --
// causation.Hypothesis / causation.Explanation for SUPPORTED BY /
// CONTRADICTED BY / CAUSATION, obligation.Obligation for CONTRACT
// BASIS / OBLIGATION, timeline.Event for EVENT, quantum.Calculation
// for QUANTUM, pkg/inference.InferenceTrace for a governed CONFIDENCE
// source, and pkg/governance/hitl for HUMAN REVIEW. Finding adds no
// new causal, contractual, or quantum reasoning of its own -- exactly
// the discipline pkg/insurance/dossier.Generate already follows, for
// the same reason: CRE's own "6 MUST NOT" list forbids inventing
// evidence and forbids converting uncertainty into certainty.
//
// The core rule: a Finding never self-declares STATUS. Status is
// always DERIVED by Evaluate from whether every required field is
// actually populated -- mirroring pkg/insurance/verification's
// GateReport.Pass() (derived-only, no settable pass/status field) and
// pkg/explanation.Build()'s refuse-if-incomplete discipline. A
// candidate with even one required field missing is CANDIDATE, never
// FINDING, regardless of what a caller wishes it were. And even once
// every field is populated and Status becomes FINDING, this remains
// what CRE calls it: a well-evidenced candidate conclusion, never a
// legally binding verdict. Finding has -- deliberately, permanently --
// no Verdict, Liable, Guilty, Winner, or ApprovedAmount field. See
// TestFindingHasNoVerdictField.
package finding

import (
	"fmt"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/insurance/causation"
)

// Status is derived-only: see Evaluate. Never set directly by a caller
// with the intention that it stick -- Evaluate always overwrites it.
type Status string

const (
	// StatusCandidate is a Finding still missing one or more required
	// fields -- not yet gated open.
	StatusCandidate Status = "CANDIDATE"
	// StatusFinding is a Finding with every required field populated.
	// It is a well-evidenced candidate conclusion, not a verdict.
	StatusFinding Status = "FINDING"
)

// Finding is the CRE schema object. See the package doc comment for
// what each field cites and why nothing here is asserted outright.
type Finding struct {
	FindingID string `json:"finding_id"`
	CaseID    string `json:"case_id"`

	// SupportedBy / ContradictedBy are evidence.Record.EvidenceID()
	// values -- typically copied from a causation.Hypothesis's own
	// SupportingEvidence/ContradictingEvidence. ContradictionsConsidered
	// must be explicitly set true once contradicting evidence has
	// genuinely been looked for, independent of whether any was found:
	// a nil/empty ContradictedBy is ambiguous between "none exists" and
	// "nobody checked," and only the caller's explicit flag resolves
	// that ambiguity honestly.
	SupportedBy              []string `json:"supported_by,omitempty"`
	ContradictedBy           []string `json:"contradicted_by,omitempty"`
	ContradictionsConsidered bool     `json:"contradictions_considered"`

	// ContractBasis is a clause identifier (e.g. policy.Clause.ClauseID);
	// ObligationRef is an obligation.Obligation.ObligationID. Both
	// mandatory, mirroring obligation.Obligation's own "no clause
	// behind it is this system inventing homework" discipline.
	ContractBasis string `json:"contract_basis,omitempty"`
	ObligationRef string `json:"obligation_ref,omitempty"`

	// EventRef is a timeline.Event.EventID: the fact this Finding is
	// actually about.
	EventRef string `json:"event_ref,omitempty"`

	// Causation is a hedged narrative -- typically
	// causation.Explanation.Narrative -- never a bare "X caused Y."
	Causation string `json:"causation,omitempty"`

	// QuantumRef is a quantum.Calculation.CalculationID (or a
	// quantum.QuantumDiscrepancy reference when the claimed and
	// supported amounts diverge).
	QuantumRef string `json:"quantum_ref,omitempty"`

	// ConfidenceBasis is the CRE schema's "CONFIDENCE" field --
	// deliberately NOT an opaque float. pkg/insurance/guardrails
	// enforces, repo-wide, that no insurance-domain type collapses its
	// evidentiary reasoning into a single unexplained number (Final
	// Design section 39's own forbidden-items list); confidence here is
	// instead the SAME mechanically-derived, decomposed classification
	// causation.Hypothesis.Status already computes from real supporting/
	// contradicting/missing evidence counts (see causation.computeStatus)
	// -- copied verbatim, never re-derived or reinvented by this
	// package. A caller that also has a governed AI confidence signal
	// (e.g. a pkg/inference.InferenceTrace) cites it via
	// SourceInferenceTraceID for audit provenance, but that signal
	// informs, and never substitutes for, ConfidenceBasis.
	ConfidenceBasis        causation.Status `json:"confidence_basis,omitempty"`
	SourceInferenceTraceID string           `json:"source_inference_trace_id,omitempty"`

	// Alternatives lists other hypotheses/explanations that were
	// considered and set aside (e.g. other causation.Hypothesis IDs
	// from the same HypothesisSet). AlternativesConsidered is the same
	// explicit-flag discipline as ContradictionsConsidered: a Finding
	// arrived at without anyone considering an alternative explanation
	// is not gate-eligible, regardless of how strong its own support is.
	Alternatives           []string `json:"alternatives,omitempty"`
	AlternativesConsidered bool     `json:"alternatives_considered"`

	// HumanReviewRequired records whether this Finding needs human
	// sign-off before it can be relied on; HumanReviewDecided records
	// that this was an explicit decision, not the bool zero value by
	// default. HumanReviewedBy, when set, names who actually reviewed
	// it -- Evaluate does not require this to reach StatusFinding
	// (requiring the review to have HAPPENED, not merely been flagged,
	// would make the gate impossible to ever open on a Finding that
	// legitimately needs no review) but a caller enforcing an actual
	// sign-off workflow should check it via pkg/governance/hitl
	// separately, exactly as pkg/insurance/verification.VerifyHumanReview
	// already does for a Dossier.
	HumanReviewRequired bool   `json:"human_review_required"`
	HumanReviewDecided  bool   `json:"human_review_decided"`
	HumanReviewedBy     string `json:"human_reviewed_by,omitempty"`

	Status Status `json:"status"`
	Tick   uint64 `json:"tick"`
	Hash   string `json:"hash"`
}

// MissingFields lists, by stable name, every required field Evaluate
// found unpopulated. An empty result is exactly the condition under
// which Evaluate sets Status to StatusFinding.
func MissingFields(f Finding) []string {
	var missing []string
	if len(f.SupportedBy) == 0 {
		missing = append(missing, "supported_by")
	}
	if !f.ContradictionsConsidered {
		missing = append(missing, "contradicted_by (must be explicitly considered, even if none exists)")
	}
	if f.ContractBasis == "" {
		missing = append(missing, "contract_basis")
	}
	if f.ObligationRef == "" {
		missing = append(missing, "obligation_ref")
	}
	if f.EventRef == "" {
		missing = append(missing, "event_ref")
	}
	if f.Causation == "" {
		missing = append(missing, "causation")
	}
	if f.QuantumRef == "" {
		missing = append(missing, "quantum_ref")
	}
	if f.ConfidenceBasis == "" || !causation.IsKnownStatus(f.ConfidenceBasis) {
		missing = append(missing, "confidence_basis (must be a known causation.Status)")
	}
	if !f.AlternativesConsidered {
		missing = append(missing, "alternatives (must be explicitly considered, even if none exists)")
	}
	if !f.HumanReviewDecided {
		missing = append(missing, "human_review_required (must be explicitly decided)")
	}
	return missing
}

// Evaluate derives f.Status from f's current fields -- the ONLY way
// Status is ever set. It always overwrites whatever Status the caller
// passed in, and always recomputes Hash: a Finding's Status and Hash
// are never independently trustworthy inputs, only outputs.
func Evaluate(f Finding) Finding {
	if len(MissingFields(f)) == 0 {
		f.Status = StatusFinding
	} else {
		f.Status = StatusCandidate
	}
	f.Hash = ""
	f.Hash = jcs.MustHash(f)
	return f
}

// VerifyFindingHash re-derives f's Hash and reports whether it matches
// the recorded value, mirroring pkg/evidence/manifest's
// VerifyManifestHash and pkg/authz's VerifyPolicyDecisionHash.
func VerifyFindingHash(f Finding) error {
	want := f
	want.Hash = ""
	want.Hash = jcs.MustHash(want)
	if want.Hash != f.Hash {
		return fmt.Errorf("finding: hash mismatch: recorded=%s recomputed=%s", f.Hash, want.Hash)
	}
	return nil
}
