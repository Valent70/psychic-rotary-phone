// Decision Intelligence extension — closes two explicit gaps from
// "Gap_dan_kekurangan_yang_harus_ditambah.docx":
//
//   - #6 Decision Intelligence: the base Engine.Decide is still
//     "weighted average vs threshold". This file adds utility, cost,
//     expected value, and multi-objective scoring on top of it, without
//     changing Decide's existing behavior or hash format (zero
//     regression risk to the existing chain).
//   - #7 Explainable AI: investors will ask "why is risk 0.969?" and
//     expect a percentage decomposition ("45% AIS anomaly, 30% insurance
//     mismatch..."), not a prose sentence. ExplainBreakdown produces
//     exactly that, as a first-class, sorted, machine-usable structure.
//
// Everything here is a pure function over Policy/values — it does not
// touch Engine.log or mutate any hash chain, so counterfactual "what if"
// exploration never corrupts the audit trail (same non-mutating
// discipline as causal.WhatIf).
package decision

import "sort"

// FactorContribution is the explainable decomposition of one factor's
// share of a risk score — the concrete "45% AIS anomaly" investor-facing
// artifact.
type FactorContribution struct {
	Name           string
	Value          float64
	Weight         float64
	Contribution   float64 // Weight * Value
	PercentOfTotal float64 // Contribution / sum(all contributions) * 100, 0 if sum is 0
	SignalSupplied bool
}

// ExplainBreakdown computes, for a policy and observed values, the
// percentage each factor contributed to the final risk score — pure,
// deterministic, sorted descending by PercentOfTotal (ties broken by
// name) so the output is stable and presentation-ready.
func ExplainBreakdown(policy Policy, values map[string]float64) []FactorContribution {
	contributions := make([]FactorContribution, 0, len(policy.Factors))
	total := 0.0
	for _, f := range policy.Factors {
		v, present := values[f.Name]
		c := f.Weight * v
		total += c
		contributions = append(contributions, FactorContribution{
			Name: f.Name, Value: v, Weight: f.Weight, Contribution: c, SignalSupplied: present,
		})
	}
	for i := range contributions {
		if total > 0 {
			contributions[i].PercentOfTotal = (contributions[i].Contribution / total) * 100
		}
	}
	sort.Slice(contributions, func(i, j int) bool {
		if contributions[i].PercentOfTotal != contributions[j].PercentOfTotal {
			return contributions[i].PercentOfTotal > contributions[j].PercentOfTotal
		}
		return contributions[i].Name < contributions[j].Name
	})
	return contributions
}

// UtilityInput bundles a policy-based risk assessment with the
// economic/operational dimensions the audit doc asks for: cost, impact,
// probability, and multi-objective weights.
type UtilityInput struct {
	Policy Policy
	Values map[string]float64

	// Cost: the operational cost of acting (e.g. detaining a vessel,
	// triggering a manual review), in the caller's chosen unit.
	Cost float64
	// Impact: the magnitude of harm if the risk materializes and no
	// action is taken, in the same unit as Cost.
	Impact float64
	// ProbabilityOfOccurrence: how likely the risk event actually
	// materializes, in [0,1] — independent of the confidence/risk score
	// itself, which measures "how sure are we", not "how likely is it
	// to happen" (these are related but distinct: a well-corroborated
	// claim can still describe a low-probability future event).
	ProbabilityOfOccurrence float64

	// ObjectiveWeights: multi-objective combination weights. Recognized
	// keys: "risk", "cost", "impact". Missing keys default to 0. Not
	// required to sum to 1 (normalized internally).
	ObjectiveWeights map[string]float64
}

// UtilityResult is the caller-facing output of ComputeUtility.
type UtilityResult struct {
	RiskScore           float64
	ExpectedValue       float64 // ProbabilityOfOccurrence * Impact - Cost
	Utility             float64 // -ExpectedValue framed as "cost of inaction net of acting cost", i.e. benefit of acting
	MultiObjectiveScore float64 // normalized weighted combination of {risk, cost, impact}
	Breakdown           []FactorContribution
}

// ComputeUtility layers economic reasoning on top of a policy's weighted
// risk score: expected value of the risk event, and a multi-objective
// score combining risk with cost and impact per caller-supplied weights.
// riskScore itself is computed with the exact same formula as
// Engine.Decide (weighted average), so ComputeUtility and Decide never
// disagree about the base risk number for the same policy/values.
func ComputeUtility(in UtilityInput) UtilityResult {
	var weightedSum, weightTotal float64
	for _, f := range in.Policy.Factors {
		v := in.Values[f.Name]
		weightedSum += f.Weight * v
		weightTotal += f.Weight
	}
	riskScore := 0.0
	if weightTotal > 0 {
		riskScore = weightedSum / weightTotal
	}

	expectedValue := in.ProbabilityOfOccurrence*in.Impact - in.Cost
	utility := expectedValue // benefit of acting now vs. absorbing the expected loss

	weights := in.ObjectiveWeights
	if weights == nil {
		weights = map[string]float64{"risk": 1}
	}
	wSum := weights["risk"] + weights["cost"] + weights["impact"]
	multiObj := 0.0
	if wSum > 0 {
		// normalize cost/impact into [0,1]-ish contribution by treating
		// them relative to their own magnitude vs impact (cost as a
		// fraction of impact, floored at 0) so units don't need to match
		// risk's [0,1] scale exactly, while staying a pure, explainable
		// function of the inputs.
		normCost := 0.0
		if in.Impact > 0 {
			normCost = in.Cost / in.Impact
			if normCost > 1 {
				normCost = 1
			}
		}
		normImpact := in.ProbabilityOfOccurrence // impact realized only with its probability
		multiObj = (weights["risk"]*riskScore + weights["impact"]*normImpact - weights["cost"]*normCost) / wSum
	}

	return UtilityResult{
		RiskScore:           riskScore,
		ExpectedValue:       expectedValue,
		Utility:             utility,
		MultiObjectiveScore: multiObj,
		Breakdown:           ExplainBreakdown(in.Policy, in.Values),
	}
}

// CounterfactualDelta is the result of comparing a base scenario against
// a "what if" variant with one or more overridden factor values —
// answering "what if AIS anomaly confidence had been 0.2 instead of
// 0.9" without mutating any engine state or audit log.
type CounterfactualDelta struct {
	BaseRiskScore           float64
	CounterfactualRiskScore float64
	Delta                   float64
	BaseAction              Action
	CounterfactualAction    Action
	ChangedFactors          []string
}

// Counterfactual computes a base decision and a "what if" variant purely
// from Policy math — no Engine, no hash chain, no side effects — proven
// non-mutating the same way causal.WhatIf is proven non-mutating.
func Counterfactual(policy Policy, baseValues, overrideValues map[string]float64) CounterfactualDelta {
	merged := make(map[string]float64, len(baseValues)+len(overrideValues))
	for k, v := range baseValues {
		merged[k] = v
	}
	var changed []string
	for k, v := range overrideValues {
		if orig, ok := baseValues[k]; !ok || orig != v {
			changed = append(changed, k)
		}
		merged[k] = v
	}
	sort.Strings(changed)

	baseScore := pureRiskScore(policy, baseValues)
	cfScore := pureRiskScore(policy, merged)

	return CounterfactualDelta{
		BaseRiskScore:           baseScore,
		CounterfactualRiskScore: cfScore,
		Delta:                   cfScore - baseScore,
		BaseAction:              actionFor(policy, baseScore),
		CounterfactualAction:    actionFor(policy, cfScore),
		ChangedFactors:          changed,
	}
}

func pureRiskScore(policy Policy, values map[string]float64) float64 {
	var weightedSum, weightTotal float64
	for _, f := range policy.Factors {
		weightedSum += f.Weight * values[f.Name]
		weightTotal += f.Weight
	}
	if weightTotal == 0 {
		return 0
	}
	return weightedSum / weightTotal
}

func actionFor(policy Policy, riskScore float64) Action {
	switch {
	case riskScore >= policy.EscalateThreshold:
		return ActionEscalate
	case riskScore >= policy.FlagThreshold:
		return ActionFlag
	default:
		return ActionMonitor
	}
}
