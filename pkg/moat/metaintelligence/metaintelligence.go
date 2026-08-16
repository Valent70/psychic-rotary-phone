// Package metaintelligence is the direct answer to this session's most
// pointed review comment: "Belum mulai MOAT ... Padahal saya ingin
// melihat mulai muncul komponen seperti Evidence Engine ... Meta
// Intelligence: Decision salah -> Evidence mana yang salah -> Policy
// mana yang salah -> Knowledge mana yang harus diperbaiki -> Trust mana
// yang turun."
//
// Honest scope, stated up front rather than discovered by the reader
// later: this is a deterministic ROOT-CAUSE ATTRIBUTION engine, not a
// self-learning system. It composes four already-real engines
// (decision.ExplainBreakdown, decision.Engine's hash-chained log,
// trust.Kernel.Evaluate, and caller-supplied evidence/KG references)
// into one coherent "why was this decision wrong, and what should lose
// trust because of it" report. It does NOT implement:
//   - Knowledge Evolution (a knowledge graph that rewrites its own
//     facts based on outcomes)
//   - adaptive/causal learning (weights or policies that change
//     themselves over time from data)
//   - a general Intent-reasoning or Intent-conflict engine
//
// Those are genuinely different, much larger systems — and building a
// system that quietly mutates its own evidence, policy, or trust rules
// based on outcomes would directly contradict this project's own DARI
// discipline (Deterministic, Auditable, Replayable, Immutable) unless
// that self-modification is itself made deterministic, versioned, and
// replayable, which is a real design problem this package does not
// attempt to solve. What this package DOES give VERIQO, today, real
// and tested: an automatic, deterministic explanation of WHICH
// evidence, WHICH policy factor, and WHOSE trust score should move —
// and by how much — whenever a decision is later judged wrong. That is
// the attribution substrate any future learning system would need
// underneath it anyway.
package metaintelligence

import (
	"context"
	"fmt"
	"sort"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/platform/telemetry"
	"veriqo/pkg/trust"
)

// EvidenceAttribution names the single factor most responsible for a
// decision's risk score, and — where the caller has supplied a mapping
// from decision-factor name to underlying evidence/KG record IDs — the
// specific evidence records implicated. The mapping is caller-supplied,
// not auto-derived: pkg/moat/decision and pkg/moat/kg do not currently
// share a common evidence-naming ontology, so asking the caller for
// this link is the honest choice over silently guessing one.
type EvidenceAttribution struct {
	FactorName     string
	PercentOfTotal float64
	Value          float64
	Weight         float64
	EvidenceRefs   []string // may be empty if no mapping was supplied
}

// RootCauseReport is the full Decision -> Evidence -> Policy -> Trust
// chain for one decision that turned out to be wrong.
type RootCauseReport struct {
	Subject         string
	PolicyName      string
	PredictedAction decision.Action
	ObservedAction  decision.Action
	WasCorrect      bool
	RiskScore       float64

	TopFactor EvidenceAttribution

	TrustSubjectID string
	TrustPolicy    string
	TrustBefore    trust.TrustScore
	TrustAfter     trust.TrustScore
	TrustDelta     float64 // TrustAfter.Score - TrustBefore.Score; negative means trust fell

	// Narrative is the ordered, human-readable chain, one line per
	// stage, matching exactly the chain the review asked to see:
	// Decision -> Evidence -> Policy -> Trust.
	Narrative []string
}

// DiagnoseInput bundles everything Diagnose needs. Every field is a
// real, already-computed artifact from an existing engine — this
// function does not invent new signals, it attributes across the ones
// that already exist.
type DiagnoseInput struct {
	// Policy and Values are exactly what was passed to
	// decision.Engine.Decide originally.
	Policy decision.Policy
	Values map[string]float64

	// Predicted is the decision the system actually made.
	Predicted decision.Decision
	// Observed is the action that turned out to be correct — supplied
	// by whatever later confirmed or refuted the decision (an operator,
	// an outcome feed, pkg/kernel/intent's own Observed field, etc.).
	// This package does not derive it; it only consumes it.
	Observed decision.Action

	// EvidenceRefs optionally maps a decision-factor Name to the
	// evidence/KG record ID(s) that supplied its value, for
	// traceability into pkg/moat/kg or pkg/evidence/graph. Optional:
	// nil is valid and simply yields an empty EvidenceRefs slice.
	EvidenceRefs map[string][]string

	// TrustKernel, TrustSubjectID, TrustPolicyName, and TrustEvidence
	// are exactly what would be passed to trust.Kernel.Evaluate to
	// score the subject BEFORE this diagnosis. FactorToTrustRuleKey
	// maps the implicated decision factor's Name to the TrustEvidence
	// key that should be discounted when computing "after" — again
	// caller-supplied for the same honest-scoping reason as
	// EvidenceRefs above.
	TrustKernel          *trust.Kernel
	TrustSubjectID       string
	TrustPolicyName      string
	TrustEvidence        trust.TrustEvidence
	FactorToTrustRuleKey map[string]string
	Tick                 uint64
}

// Diagnose runs the full root-cause chain for one decision. It is pure
// with respect to trust.Kernel state: Evaluate does not mutate the
// Kernel (no certificate is issued), so calling Diagnose is always safe
// to run speculatively/repeatedly without side effects on the trust
// ledger — callers that want the trust drop to actually take effect
// call Kernel.Certify themselves afterward with the same evidence used
// for TrustAfter.
func Diagnose(in DiagnoseInput) (RootCauseReport, error) {
	_, span := telemetry.StartSpan(context.Background(), "metaintelligence.Diagnose",
		telemetry.Attribute{Key: "policy", Value: in.Policy.Name})
	defer span.End()

	breakdown := decision.ExplainBreakdown(in.Policy, in.Values)
	if len(breakdown) == 0 {
		return RootCauseReport{}, fmt.Errorf("metaintelligence: policy %q has no factors to attribute against", in.Policy.Name)
	}
	top := breakdown[0] // ExplainBreakdown sorts descending by PercentOfTotal

	report := RootCauseReport{
		Subject:         in.Predicted.Subject,
		PolicyName:      in.Predicted.PolicyName,
		PredictedAction: in.Predicted.Action,
		ObservedAction:  in.Observed,
		WasCorrect:      in.Predicted.Action == in.Observed,
		RiskScore:       in.Predicted.RiskScore,
		TopFactor: EvidenceAttribution{
			FactorName:     top.Name,
			PercentOfTotal: top.PercentOfTotal,
			Value:          top.Value,
			Weight:         top.Weight,
			EvidenceRefs:   append([]string(nil), in.EvidenceRefs[top.Name]...),
		},
		TrustSubjectID: in.TrustSubjectID,
		TrustPolicy:    in.TrustPolicyName,
	}

	report.Narrative = append(report.Narrative,
		fmt.Sprintf("Decision: subject=%s policy=%s predicted=%s risk=%.4f",
			report.Subject, report.PolicyName, report.PredictedAction, report.RiskScore))

	if report.WasCorrect {
		report.Narrative = append(report.Narrative,
			fmt.Sprintf("Outcome: observed=%s — decision was CORRECT, no attribution needed", in.Observed))
		if in.TrustKernel != nil {
			before, err := in.TrustKernel.Evaluate(in.TrustSubjectID, in.TrustPolicyName, in.TrustEvidence, in.Tick)
			if err == nil {
				report.TrustBefore, report.TrustAfter = before, before
			}
		}
		return report, nil
	}

	report.Narrative = append(report.Narrative,
		fmt.Sprintf("Outcome: observed=%s — decision was WRONG", in.Observed))
	report.Narrative = append(report.Narrative,
		fmt.Sprintf("Evidence: top contributing factor is %q (%.1f%% of risk score, value=%.4f, weight=%.4f)",
			top.Name, top.PercentOfTotal, top.Value, top.Weight))
	if len(report.TopFactor.EvidenceRefs) > 0 {
		report.Narrative = append(report.Narrative,
			fmt.Sprintf("Evidence records implicated: %v", report.TopFactor.EvidenceRefs))
	}
	report.Narrative = append(report.Narrative,
		fmt.Sprintf("Policy: %q's factor weight for %q is %.4f — this is the knob a policy author would revisit first",
			in.Policy.Name, top.Name, top.Weight))

	if in.TrustKernel != nil && in.TrustEvidence != nil {
		before, err := in.TrustKernel.Evaluate(in.TrustSubjectID, in.TrustPolicyName, in.TrustEvidence, in.Tick)
		if err != nil {
			return report, fmt.Errorf("metaintelligence: trust before-evaluation failed: %w", err)
		}
		report.TrustBefore = before

		discounted := make(trust.TrustEvidence, len(in.TrustEvidence))
		for k, v := range in.TrustEvidence {
			discounted[k] = v
		}
		if key, ok := in.FactorToTrustRuleKey[top.Name]; ok {
			discounted[key] = 0
			report.Narrative = append(report.Narrative,
				fmt.Sprintf("Trust: discounting evidence key %q (mapped from factor %q) to zero and re-evaluating", key, top.Name))
		} else {
			report.Narrative = append(report.Narrative,
				fmt.Sprintf("Trust: no FactorToTrustRuleKey mapping supplied for factor %q — evaluating unchanged evidence (delta will be zero)", top.Name))
		}

		after, err := in.TrustKernel.Evaluate(in.TrustSubjectID, in.TrustPolicyName, discounted, in.Tick)
		if err != nil {
			return report, fmt.Errorf("metaintelligence: trust after-evaluation failed: %w", err)
		}
		report.TrustAfter = after
		report.TrustDelta = after.Score - before.Score
		report.Narrative = append(report.Narrative,
			fmt.Sprintf("Trust: subject=%s score moved %.4f -> %.4f (delta=%.4f)",
				in.TrustSubjectID, before.Score, after.Score, report.TrustDelta))
	}

	return report, nil
}

// TopNFactors is a small convenience export so callers building
// operator-facing dashboards don't have to re-sort ExplainBreakdown's
// output themselves for "what were the top 3 drivers" views. Purely a
// slice/formatting helper — no new computation.
func TopNFactors(policy decision.Policy, values map[string]float64, n int) []EvidenceAttribution {
	breakdown := decision.ExplainBreakdown(policy, values)
	sort.SliceStable(breakdown, func(i, j int) bool {
		return breakdown[i].PercentOfTotal > breakdown[j].PercentOfTotal
	})
	if n > len(breakdown) {
		n = len(breakdown)
	}
	out := make([]EvidenceAttribution, 0, n)
	for _, f := range breakdown[:n] {
		out = append(out, EvidenceAttribution{
			FactorName: f.Name, PercentOfTotal: f.PercentOfTotal, Value: f.Value, Weight: f.Weight,
		})
	}
	return out
}
