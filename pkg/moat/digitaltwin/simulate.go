// Digital Twin simulation extension — closes the audit doc's gap #3:
// "Digital Twin masih sederhana ... seharusnya bisa simulate, predict,
// counterfactual, future state, policy effect, economic impact".
//
// Honest scope statement: this is a deterministic, closed-form
// projection model (exponential approach toward a target risk level),
// NOT a learned/ML forecaster. It is intentionally simple so it stays
// zero-dependency and fully explainable — every projected point is a
// pure function of the twin's current state and the supplied
// SimulationParams, never time.Now(), never randomness. What it adds
// over the base Twin (a single current snapshot) is real: a multi-tick
// FUTURE trajectory, a non-mutating POLICY-EFFECT counterfactual (via
// decision.Counterfactual), and an ECONOMIC IMPACT projection.
package digitaltwin

import (
	"sort"

	"veriqo/pkg/moat/decision"
)

// ProjectedPoint is one predicted future tick in a risk trajectory.
type ProjectedPoint struct {
	Tick      uint64
	RiskScore float64
}

// SimulationParams controls the deterministic projection model.
type SimulationParams struct {
	// TargetRiskScore is the level the trajectory approaches — e.g. a
	// scenario's "if nothing changes, where does this converge" target,
	// or a "if intervention X is applied" target the caller computes
	// externally (e.g. via decision.Counterfactual) and feeds in here.
	TargetRiskScore float64
	// ApproachRate in (0,1]: fraction of the remaining gap to the target
	// closed per tick. Larger = faster convergence. This is a standard
	// discrete exponential-approach (first-order lag) model — the same
	// closed form used for capacity/queue projections, kept because it
	// is simple, deterministic, and its behavior is easy to explain to
	// a non-technical investor ("closes 20% of the gap to target each
	// period").
	ApproachRate float64
	// HorizonTicks: how many future ticks to project.
	HorizonTicks uint64
}

// Simulate projects a twin's named RiskState forward HorizonTicks steps
// using a deterministic first-order approach toward TargetRiskScore. It
// is a pure function of the twin snapshot and params — it does not
// mutate the Registry or twin, and calling it twice with the same inputs
// always yields byte-identical output (proven by
// TestSimulateIsDeterministicAndNonMutating).
func Simulate(twin Twin, policyName string, params SimulationParams) []ProjectedPoint {
	current, ok := twin.Risk[policyName]
	startScore := 0.0
	startTick := uint64(0)
	if ok {
		startScore = current.RiskScore
		startTick = current.UpdatedAtTick
	}
	rate := params.ApproachRate
	if rate <= 0 {
		rate = 1
	}
	if rate > 1 {
		rate = 1
	}

	out := make([]ProjectedPoint, 0, params.HorizonTicks)
	score := startScore
	for i := uint64(1); i <= params.HorizonTicks; i++ {
		gap := params.TargetRiskScore - score
		score = score + gap*rate
		out = append(out, ProjectedPoint{Tick: startTick + i, RiskScore: score})
	}
	return out
}

// PolicyEffectResult bundles a non-mutating counterfactual (via
// decision.Counterfactual) with the resulting projected future
// trajectory under the new policy — answering "what happens to this
// entity's risk trajectory if we change factor X" without touching any
// engine's audit log.
type PolicyEffectResult struct {
	Counterfactual decision.CounterfactualDelta
	Projection     []ProjectedPoint
}

// SimulatePolicyEffect composes decision.Counterfactual (pure risk-score
// delta from changing factor inputs) with Simulate (pure future
// trajectory toward that counterfactual score) — the concrete "policy
// effect" simulation the audit doc asks for.
func SimulatePolicyEffect(twin Twin, policyName string, policy decision.Policy, baseValues, overrideValues map[string]float64, approachRate float64, horizonTicks uint64) PolicyEffectResult {
	cf := decision.Counterfactual(policy, baseValues, overrideValues)
	proj := Simulate(twin, policyName, SimulationParams{
		TargetRiskScore: cf.CounterfactualRiskScore,
		ApproachRate:    approachRate,
		HorizonTicks:    horizonTicks,
	})
	return PolicyEffectResult{Counterfactual: cf, Projection: proj}
}

// EconomicImpactInput describes the downstream economic exposure tied
// to an entity's risk state — the audit doc's explicit "economic
// intelligence" ask (commodity flow, shipping cost, regional trade
// impact) expressed as a generic, composable set of exposure channels
// so callers can plug in domain-specific figures without this package
// needing to know about commodities/refineries/etc.
type EconomicImpactInput struct {
	// Channels maps an exposure channel name (e.g. "commodity_flow_usd",
	// "shipping_cost_usd", "regional_trade_usd") to the total value at
	// stake in that channel if the risk fully materializes.
	Channels map[string]float64
	// RiskScore: probability-like weight in [0,1] applied to each
	// channel's exposure (typically the twin's current or projected
	// RiskScore for the relevant policy).
	RiskScore float64
}

// ChannelImpact is one channel's computed expected economic exposure.
type ChannelImpact struct {
	Channel        string
	Exposure       float64
	ExpectedImpact float64 // Exposure * RiskScore
}

// EconomicImpactResult is the caller-facing output of
// ComputeEconomicImpact.
type EconomicImpactResult struct {
	TotalExposure       float64
	TotalExpectedImpact float64
	Channels            []ChannelImpact
}

// ComputeEconomicImpact translates a risk score into expected economic
// exposure across named channels — a pure, explainable function (no
// hidden macro model), deliberately scoped as "expected value per
// channel" rather than a full economic simulation.
func ComputeEconomicImpact(in EconomicImpactInput) EconomicImpactResult {
	channels := make([]ChannelImpact, 0, len(in.Channels))
	var totalExposure, totalExpected float64
	names := make([]string, 0, len(in.Channels))
	for name := range in.Channels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		exposure := in.Channels[name]
		expected := exposure * in.RiskScore
		totalExposure += exposure
		totalExpected += expected
		channels = append(channels, ChannelImpact{Channel: name, Exposure: exposure, ExpectedImpact: expected})
	}
	return EconomicImpactResult{
		TotalExposure: totalExposure, TotalExpectedImpact: totalExpected, Channels: channels,
	}
}
