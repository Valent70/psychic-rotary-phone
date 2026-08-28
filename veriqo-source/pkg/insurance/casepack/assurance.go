package casepack

import (
	"fmt"
	"sort"

	"veriqo/pkg/insurance/verification"
	"veriqo/pkg/lineage"
)

// This file produces the machine-readable evidence the four
// insurance_* readiness gates attach. It runs the whole pack through
// the real domain and reports what happened — it decides nothing, and
// every Pass below is derived from a failure list, never set.

// CaseOutcome is one case's result, flattened for a readiness artifact.
type CaseOutcome struct {
	CaseID string `json:"case_id"`

	EvidenceCount        int    `json:"evidence_count"`
	EvidenceRootHash     string `json:"evidence_root_hash"`
	PreservationHash     string `json:"preservation_order_hash"`
	ResolvedPolicyID     string `json:"resolved_policy_version_id"`
	LineageNodeCount     int    `json:"lineage_node_count"`
	LineageChainVerified bool   `json:"lineage_chain_verified"`

	CoverageTraceabilityPass bool     `json:"coverage_traceability_pass"`
	CoverageFactCount        int      `json:"coverage_fact_count"`
	CoverageTraceableFacts   int      `json:"coverage_traceable_facts"`
	CoverageFailures         []string `json:"coverage_failures,omitempty"`

	QuantumReproducible bool     `json:"quantum_reproducible"`
	QuantumIndicative   string   `json:"quantum_indicative_claim_value"`
	QuantumFailures     []string `json:"quantum_failures,omitempty"`

	PreservationPass       bool     `json:"preservation_pass"`
	PreservationEvidence   int      `json:"preservation_evidence_in_case"`
	PreservationPreserved  int      `json:"preservation_evidence_preserved"`
	PreservationFailures   []string `json:"preservation_failures,omitempty"`
	HumanReviewRequired    bool     `json:"human_review_required"`
	FinalizationRefused    bool     `json:"finalization_refused_without_authorization"`
	FinalizationPermitted  bool     `json:"finalization_permitted_with_authorization"`
	OutstandingQuestionCnt int      `json:"outstanding_review_questions"`
}

// Summary is the whole pack's assurance result.
type Summary struct {
	CaseCount int           `json:"case_count"`
	Cases     []CaseOutcome `json:"cases"`

	// The five per-gate verdicts, each DERIVED from the corresponding
	// failure list below.
	CoverageTraceabilityFailures   []string `json:"coverage_traceability_failures,omitempty"`
	QuantumReproducibilityFailures []string `json:"quantum_reproducibility_failures,omitempty"`
	PreservationFailures           []string `json:"preservation_failures,omitempty"`
	HumanReviewFailures            []string `json:"human_review_failures,omitempty"`
	// ColdReplayFailures backs the §20 "C5" / spec §73 gate: MVP §80
	// item 14, closed this round (see replay.go). Every failure here is
	// one case's ColdReplayReport.Failures, prefixed with its CaseID.
	ColdReplayFailures []string `json:"cold_replay_failures,omitempty"`
}

// CoverageTraceabilityPass reports the §54 gate's verdict over the pack.
func (s Summary) CoverageTraceabilityPass() bool {
	return s.CaseCount > 0 && len(s.CoverageTraceabilityFailures) == 0
}

// QuantumReproducibilityPass reports the §55 gate's verdict.
func (s Summary) QuantumReproducibilityPass() bool {
	return s.CaseCount > 0 && len(s.QuantumReproducibilityFailures) == 0
}

// PreservationPass reports the §56 gate's verdict.
func (s Summary) PreservationPass() bool {
	return s.CaseCount > 0 && len(s.PreservationFailures) == 0
}

// HumanReviewPass reports the §57 gate's verdict.
//
// Note carefully what this gate asserts, because it is NOT "no case
// needed review". Every synthetic case has unresolved findings and
// therefore DOES require review. What the gate proves is that the
// enforcement behaves correctly in BOTH directions:
//
//   - with no authorization, finalization is REFUSED; and
//   - with a complete, well-formed authorization, it is PERMITTED.
//
// A gate that only ever refused would pass trivially and prove nothing.
func (s Summary) HumanReviewPass() bool {
	return s.CaseCount > 0 && len(s.HumanReviewFailures) == 0
}

// ColdReplayPass reports the cold-replay gate's verdict: every case,
// reconstructed from nothing but its own serialised snapshot, must
// reproduce the live result exactly. See replay.go.
func (s Summary) ColdReplayPass() bool {
	return s.CaseCount > 0 && len(s.ColdReplayFailures) == 0
}

// RunAssurance drives every synthetic case through the real domain and
// returns the four gates' evidence. It never panics and never skips a
// case: a case that fails to drive at all is reported as a failure of
// every gate, because a pack that cannot run proves nothing.
func RunAssurance() Summary {
	s := Summary{}
	for _, c := range All() {
		res, err := Drive(c, lineage.NewLedger())
		if err != nil {
			msg := fmt.Sprintf("%s: the case could not be driven at all: %v", c.ID, err)
			s.CoverageTraceabilityFailures = append(s.CoverageTraceabilityFailures, msg)
			s.QuantumReproducibilityFailures = append(s.QuantumReproducibilityFailures, msg)
			s.PreservationFailures = append(s.PreservationFailures, msg)
			s.HumanReviewFailures = append(s.HumanReviewFailures, msg)
			s.ColdReplayFailures = append(s.ColdReplayFailures, msg)
			s.Cases = append(s.Cases, CaseOutcome{CaseID: string(c.ID)})
			continue
		}
		s.CaseCount++

		comp := res.Binding.Completeness()
		out := CaseOutcome{
			CaseID:               string(c.ID),
			EvidenceCount:        res.Manifest.EvidenceCount,
			EvidenceRootHash:     res.Manifest.EvidenceRootHash,
			PreservationHash:     res.Order.Hash(),
			ResolvedPolicyID:     res.PolicyVersion.VersionID,
			LineageNodeCount:     comp.NodeCount,
			LineageChainVerified: comp.ChainVerified,

			CoverageTraceabilityPass: res.Gates.CoverageTraceability.Pass(),
			CoverageFactCount:        res.Gates.CoverageTraceability.FactCount,
			CoverageTraceableFacts:   res.Gates.CoverageTraceability.TraceableFacts,
			CoverageFailures:         res.Gates.CoverageTraceability.Failures,

			QuantumReproducible: res.Gates.QuantumReproducibility.Pass(),
			QuantumIndicative:   res.Gates.QuantumReproducibility.RecordedIndicativeValue,
			QuantumFailures:     res.Gates.QuantumReproducibility.Failures,

			PreservationPass:      res.Gates.Preservation.Pass(),
			PreservationEvidence:  res.Gates.Preservation.EvidenceInCase,
			PreservationPreserved: res.Gates.Preservation.EvidencePreserved,
			PreservationFailures:  res.Gates.Preservation.Failures,

			HumanReviewRequired:    res.Gates.HumanReview.ReviewRequired,
			OutstandingQuestionCnt: len(res.Gates.HumanReview.OutstandingQuestions),
		}

		// --- §54 ---
		for _, f := range res.Gates.CoverageTraceability.Failures {
			s.CoverageTraceabilityFailures = append(s.CoverageTraceabilityFailures, string(c.ID)+": "+f)
		}
		if out.CoverageFactCount == 0 {
			s.CoverageTraceabilityFailures = append(s.CoverageTraceabilityFailures,
				string(c.ID)+": the coverage gate examined zero facts, so its PASS would be vacuous")
		}

		// --- §55 ---
		for _, f := range res.Gates.QuantumReproducibility.Failures {
			s.QuantumReproducibilityFailures = append(s.QuantumReproducibilityFailures, string(c.ID)+": "+f)
		}
		if !res.Gates.QuantumReproducibility.Recomputed {
			s.QuantumReproducibilityFailures = append(s.QuantumReproducibilityFailures,
				string(c.ID)+": the quantum gate never actually recomputed the calculation")
		}

		// --- §56 ---
		for _, f := range res.Gates.Preservation.Failures {
			s.PreservationFailures = append(s.PreservationFailures, string(c.ID)+": "+f)
		}
		if out.PreservationEvidence == 0 {
			s.PreservationFailures = append(s.PreservationFailures,
				string(c.ID)+": the preservation gate saw no evidence, so its PASS would be vacuous")
		}
		if !comp.ChainVerified {
			s.PreservationFailures = append(s.PreservationFailures,
				string(c.ID)+": the case lineage hash chain does not verify")
		}

		// --- §57: both directions ---
		out.FinalizationRefused = !res.Gates.HumanReview.FinalizationPermitted
		if !out.HumanReviewRequired {
			s.HumanReviewFailures = append(s.HumanReviewFailures,
				string(c.ID)+": the case reports no review requirement, so the enforcement is untested by it")
		}
		if !out.FinalizationRefused {
			s.HumanReviewFailures = append(s.HumanReviewFailures,
				string(c.ID)+": finalization was PERMITTED with no authorization — the gate did not fail closed")
		}
		if out.OutstandingQuestionCnt == 0 && out.HumanReviewRequired {
			s.HumanReviewFailures = append(s.HumanReviewFailures,
				string(c.ID)+": review is required but the refusal names no outstanding question")
		}
		authorized := verification.VerifyHumanReview(res.Dossier,
			AuthorizationsSatisfying(res.Dossier, "readiness-gate-reviewer", "HITL-"+string(c.ID), 900))
		out.FinalizationPermitted = authorized.FinalizationPermitted
		if !authorized.Pass() {
			for _, f := range authorized.Failures {
				s.HumanReviewFailures = append(s.HumanReviewFailures,
					string(c.ID)+": a complete authorization was still refused: "+f)
			}
		}
		if !out.FinalizationPermitted {
			s.HumanReviewFailures = append(s.HumanReviewFailures,
				string(c.ID)+": finalization was refused even with a complete authorization — "+
					"a gate that only ever refuses proves nothing")
		}

		// --- §20 "C5" cold replay: a case reconstructed from ONLY its
		// own serialised snapshot must reproduce this exact result. ---
		_, _, coldReport, coldErr := ColdReplay(c)
		if coldErr != nil {
			s.ColdReplayFailures = append(s.ColdReplayFailures,
				fmt.Sprintf("%s: cold replay could not run at all: %v", c.ID, coldErr))
		} else if !coldReport.Pass() {
			for _, f := range coldReport.Failures {
				s.ColdReplayFailures = append(s.ColdReplayFailures, string(c.ID)+": "+f)
			}
		}

		s.Cases = append(s.Cases, out)
	}

	sort.Slice(s.Cases, func(i, j int) bool { return s.Cases[i].CaseID < s.Cases[j].CaseID })
	return s
}
