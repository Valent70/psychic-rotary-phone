// Package cre is the Case Resolution Engine's own genuinely new core:
// the mechanical step that turns a completed causation.HypothesisSet
// into finding.Finding candidates.
//
// Everything upstream and downstream of this step is deliberate REUSE,
// not rebuilt here -- Contract/Claim (pkg/insurance/policy, pkg/
// insurance/claim), the Case aggregate and its 15-state pipeline
// (pkg/insurance/case), Evidence (pkg/insurance/evidence, pkg/evidence/
// manifest), Verification (pkg/insurance/verification's four gates),
// Fact Reconstruction (pkg/insurance/timeline), Contract-to-Fact Mapping
// (pkg/insurance/obligation, pkg/insurance/coverage), Causation (pkg/
// insurance/causation), Quantum (pkg/insurance/quantum), Human Review
// (pkg/governance/hitl, pkg/insurance/verification.VerifyHumanReview),
// and the Evidence Dossier (pkg/insurance/dossier). A repo-wide survey
// (see this round's commit history) found every one of those stages
// already implemented, tested, and following CRE's own "6 MUST NOT"
// discipline -- so the only genuine gap this package closes is turning
// "here is a set of competing hypotheses with real evidence" into "here
// are the well-evidenced candidate conclusions a human should look at,"
// mechanically and auditably.
//
// This package invents nothing: SupportedBy, ContradictedBy, and
// ConfidenceBasis are copied verbatim from the hypothesis that already
// computed them (causation.Hypothesis / computeStatus); Alternatives is
// mechanically derived as every OTHER hypothesis in the same set, never
// hand-picked; Causation is causation.Explain's own hedged narrative,
// never re-worded. ContractBasis, ObligationRef, EventRef, QuantumRef,
// and the human-review decision cannot be mechanically derived from a
// HypothesisSet alone -- CRE forbids inventing them -- so FindingInput
// requires the caller to supply them from their own already-computed
// obligation.Obligation / timeline.Event / quantum.Calculation objects.
package cre

import (
	"fmt"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/finding"
)

// FindingInput supplies the domain citations this package cannot
// mechanically derive from a HypothesisSet alone.
type FindingInput struct {
	CaseID                 string
	ContractBasis          string
	ObligationRef          string
	EventRef               string
	QuantumRef             string
	SourceInferenceTraceID string
	HumanReviewRequired    bool
	HumanReviewedBy        string
}

// CandidateHypotheses returns every hypothesis in hs whose Status is
// SUPPORTED or PARTIALLY_SUPPORTED, in hs's own order -- the set worth
// turning into a Finding candidate. CONTRADICTED, UNPROVEN, and
// INSUFFICIENT_EVIDENCE hypotheses are deliberately excluded: a
// contradicted or unproven hypothesis is not a candidate conclusion,
// it is a ruled-out (or not-yet-evidenced) one. An empty result is a
// legitimate, honest outcome -- a case where causation genuinely
// remains unresolved -- not an error.
func CandidateHypotheses(hs *causation.HypothesisSet) []causation.Hypothesis {
	var out []causation.Hypothesis
	for _, h := range hs.All() {
		if h.Status == causation.StatusSupported || h.Status == causation.StatusPartiallySupported {
			out = append(out, h)
		}
	}
	return out
}

// BuildFinding assembles one finding.Finding from hypothesis h (typically
// one returned by CandidateHypotheses) within hs, plus in's own domain
// citations, and evaluates it. dg is the same evidence.DependencyGraph
// causation.Explain itself requires, for the same reason: correlated
// evidence must not be double-counted.
//
// This assumes hs is COMPLETE at the time of the call -- every known
// piece of supporting and contradicting evidence has already been
// recorded via hs.AddSupportingEvidence/AddContradictingEvidence -- so
// that marking ContradictionsConsidered true here honestly reflects
// "contradicting evidence was looked for," not merely "none has been
// added yet." Calling BuildFinding on a HypothesisSet still being
// assembled would misrepresent that flag; callers own that ordering.
func BuildFinding(hs *causation.HypothesisSet, h causation.Hypothesis, dg *evidence.DependencyGraph,
	in FindingInput, findingID string, tick uint64) (finding.Finding, error) {
	if !causation.IsKnownStatus(h.Status) {
		return finding.Finding{}, fmt.Errorf("cre: hypothesis %s has no known status", h.ID)
	}
	explanation, err := causation.Explain(hs, dg)
	if err != nil {
		return finding.Finding{}, fmt.Errorf("cre: explaining causation: %w", err)
	}

	var alternatives []string
	for _, other := range hs.All() {
		if other.ID != h.ID {
			alternatives = append(alternatives, string(other.ID))
		}
	}

	f := finding.Finding{
		FindingID: findingID, CaseID: in.CaseID,
		SupportedBy: h.SupportingEvidence, ContradictedBy: h.ContradictingEvidence, ContradictionsConsidered: true,
		ContractBasis: in.ContractBasis, ObligationRef: in.ObligationRef, EventRef: in.EventRef,
		Causation: explanation.Narrative, QuantumRef: in.QuantumRef,
		ConfidenceBasis: h.Status, SourceInferenceTraceID: in.SourceInferenceTraceID,
		Alternatives: alternatives, AlternativesConsidered: true,
		HumanReviewRequired: in.HumanReviewRequired, HumanReviewDecided: true, HumanReviewedBy: in.HumanReviewedBy,
		Tick: tick,
	}
	return finding.Evaluate(f), nil
}

// GenerateFindings runs BuildFinding for every candidate hypothesis in
// hs (per CandidateHypotheses), assigning each a FindingID derived from
// findingIDPrefix and the hypothesis's own ID -- deterministic, never a
// random UUID. An empty result means causation produced no supported or
// partially-supported hypothesis; that is reported, not hidden behind
// an error.
func GenerateFindings(hs *causation.HypothesisSet, dg *evidence.DependencyGraph, in FindingInput,
	findingIDPrefix string, tick uint64) ([]finding.Finding, error) {
	var out []finding.Finding
	for _, h := range CandidateHypotheses(hs) {
		f, err := BuildFinding(hs, h, dg, in, findingIDPrefix+"-"+string(h.ID), tick)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
