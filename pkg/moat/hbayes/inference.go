package hbayes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"

	"veriqo/pkg/platform/telemetry"
)

// Model is the temporal RS-CHDBN: hidden states, a transition model, a
// prior, causal edges and a temporal decay rate.
type Model struct {
	States      []State
	Transition  Transition
	Prior       map[State]float64
	CausalEdges []CausalEdge
	// DecayLambda controls how fast dynamics blend toward the
	// stationary distribution across observation gaps. 0 disables
	// decay (pure Markov).
	DecayLambda float64
}

// NewTemporalModel validates and normalises a temporal model.
func NewTemporalModel(states []State, tr Transition, prior map[State]float64, edges []CausalEdge, decay float64) (*Model, error) {
	if len(states) < 2 {
		return nil, ErrNoStates
	}
	sorted := append([]State(nil), states...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	tr.States = sorted
	if err := tr.Validate(); err != nil {
		return nil, err
	}
	var sum float64
	for _, s := range sorted {
		v, ok := prior[s]
		if !ok || v < 0 {
			return nil, fmt.Errorf("%w: state %s", ErrBadPrior, s)
		}
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 {
		return nil, fmt.Errorf("%w: sums to %.12f", ErrBadPrior, sum)
	}
	for _, e := range edges {
		if _, ok := tr.P[e.Effect]; !ok {
			return nil, fmt.Errorf("%w: causal edge effect %s", ErrUnknownState, e.Effect)
		}
		if e.Strength < -1 || e.Strength > 1 {
			return nil, fmt.Errorf("hbayes: causal edge %s->%s strength %.4f out of [-1,1]", e.Cause, e.Effect, e.Strength)
		}
	}
	return &Model{
		States: sorted, Transition: tr, Prior: normalize(sorted, copyDist(prior)),
		CausalEdges: append([]CausalEdge(nil), edges...), DecayLambda: decay,
	}, nil
}

func copyDist(d map[State]float64) map[State]float64 {
	out := make(map[State]float64, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

// TickObservations groups every observation recorded at one tick.
type TickObservations struct {
	Tick         uint64
	Observations []Observation
}

// StepResult is the model's belief at one tick.
type StepResult struct {
	Tick uint64 `json:"tick"`
	// Filtered is P(state_t | obs_1..t) — what was believed AT the time.
	Filtered map[State]float64 `json:"filtered"`
	// Smoothed is P(state_t | obs_1..T) — hindsight after all evidence.
	Smoothed map[State]float64 `json:"smoothed"`
	// Entropy of the filtered distribution, in bits. Rising entropy
	// across a gap is the visible proof that missing observations
	// INCREASE uncertainty rather than freezing belief.
	Entropy float64 `json:"entropy"`
	// ObservationCount at this tick (0 = propagation only).
	ObservationCount int `json:"observation_count"`
	// EffectiveEvidence is the sum of correlation-discounted
	// observation weights actually applied.
	EffectiveEvidence float64 `json:"effective_evidence"`
	// Interventions applied at this tick.
	Interventions []string `json:"interventions,omitempty"`
}

// InferenceTrace is the complete, replayable record of one inference
// run — every tick's filtered and smoothed posterior plus a content
// hash over the whole thing.
type InferenceTrace struct {
	Steps         []StepResult      `json:"steps"`
	FinalFiltered map[State]float64 `json:"final_filtered"`
	MAPPath       []State           `json:"map_path"`
	LogLikelihood float64           `json:"log_likelihood"`
	TraceHash     string            `json:"trace_hash"`
}

// Infer runs forward filtering and backward smoothing over an ordered
// observation sequence, applying temporal decay across gaps, causal
// interventions, and per-observation correlation discounts.
//
// The whole function is pure: (model, obs, interventions) -> trace.
func (m *Model) Infer(seq []TickObservations, interventions []Intervention) (InferenceTrace, error) {
	_, span := telemetry.StartSpan(context.Background(), "hbayes.Infer",
		telemetry.Attribute{Key: "tick_count", Value: strconv.Itoa(len(seq))})
	defer span.End()

	if len(seq) == 0 {
		return InferenceTrace{}, ErrNoObservations
	}
	for i := 1; i < len(seq); i++ {
		if seq[i].Tick <= seq[i-1].Tick {
			return InferenceTrace{}, fmt.Errorf("%w: tick %d after %d", ErrNonMonotonicTicks, seq[i].Tick, seq[i-1].Tick)
		}
	}
	for _, t := range seq {
		for _, o := range t.Observations {
			if err := o.validate(m.States); err != nil {
				return InferenceTrace{}, err
			}
		}
	}

	n := len(seq)
	alpha := make([]map[State]float64, n) // filtered
	predicted := make([]map[State]float64, n)
	emission := make([]map[State]float64, n)
	scale := make([]float64, n)
	steps := make([]StepResult, n)
	var logLik float64

	for i, t := range seq {
		// --- Predict -------------------------------------------------
		var pred map[State]float64
		var applied []string
		if i == 0 {
			pred = copyDist(m.Prior)
		} else {
			gap := seq[i].Tick - seq[i-1].Tick
			tr := m.Transition.Decayed(gap, m.DecayLambda)
			tr, applied = m.applyInterventions(tr, interventions, seq[i-1].Tick, seq[i].Tick)
			pred = make(map[State]float64, len(m.States))
			for _, to := range m.States {
				var acc float64
				for _, from := range m.States {
					acc += alpha[i-1][from] * tr.P[from][to]
				}
				pred[to] = acc
			}
		}
		predicted[i] = normalize(m.States, pred)

		// --- Update --------------------------------------------------
		emis, effective := m.jointEmission(t.Observations)
		emission[i] = emis
		post := make(map[State]float64, len(m.States))
		var total float64
		for _, s := range m.States {
			post[s] = predicted[i][s] * emis[s]
			total += post[s]
		}
		if total <= 0 {
			return InferenceTrace{}, fmt.Errorf("%w at tick %d", ErrDegenerate, t.Tick)
		}
		scale[i] = total
		logLik += math.Log(total)
		alpha[i] = normalize(m.States, post)

		steps[i] = StepResult{
			Tick: t.Tick, Filtered: alpha[i], Entropy: entropy(m.States, alpha[i]),
			ObservationCount: len(t.Observations), EffectiveEvidence: effective,
			Interventions: applied,
		}
	}

	// --- Backward smoothing (Rauch-Tung-Striebel style for discrete
	// HMM: beta recursion with the same decayed/intervened matrices) --
	beta := make([]map[State]float64, n)
	beta[n-1] = uniformOnes(m.States)
	for i := n - 2; i >= 0; i-- {
		gap := seq[i+1].Tick - seq[i].Tick
		tr := m.Transition.Decayed(gap, m.DecayLambda)
		tr, _ = m.applyInterventions(tr, interventions, seq[i].Tick, seq[i+1].Tick)
		b := make(map[State]float64, len(m.States))
		for _, from := range m.States {
			var acc float64
			for _, to := range m.States {
				acc += tr.P[from][to] * emission[i+1][to] * beta[i+1][to]
			}
			b[from] = acc / scale[i+1]
		}
		beta[i] = b
	}
	for i := 0; i < n; i++ {
		sm := make(map[State]float64, len(m.States))
		for _, s := range m.States {
			sm[s] = alpha[i][s] * beta[i][s]
		}
		steps[i].Smoothed = normalize(m.States, sm)
	}

	trace := InferenceTrace{
		Steps: steps, FinalFiltered: alpha[n-1], LogLikelihood: logLik,
		MAPPath: mapPath(m.States, steps),
	}
	trace.TraceHash = hashTrace(m, trace)
	return trace, nil
}

// jointEmission combines a tick's observations into one emission
// likelihood per state, discounting correlated observations.
//
// Independent observations multiply. A correlated observation is
// applied with exponent (1 - correlation): at correlation 0 it counts
// fully, approaching 1 it contributes nothing. This is the same
// "correlated evidence cannot inflate confidence" invariant PHASE 1
// enforces on weights, expressed in likelihood space.
func (m *Model) jointEmission(obs []Observation) (map[State]float64, float64) {
	out := make(map[State]float64, len(m.States))
	for _, s := range m.States {
		out[s] = 1
	}
	if len(obs) == 0 {
		return out, 0 // no evidence: emission is uninformative, prediction stands
	}
	ordered := append([]Observation(nil), obs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SourceID < ordered[j].SourceID })
	var effective float64
	for _, o := range ordered {
		w := 1 - o.Correlation
		effective += w
		for _, s := range m.States {
			l, ok := o.Likelihood[s]
			if !ok {
				continue // this source says nothing about this state
			}
			// Floor guards against a single zero likelihood collapsing
			// the whole posterior — a contradictory source should
			// argue, not veto.
			if l < 1e-9 {
				l = 1e-9
			}
			out[s] *= math.Pow(l, w)
		}
	}
	return out, effective
}

// applyInterventions shifts transition inflow toward or away from a
// causal edge's effect state when a matching intervention lands inside
// the (from, to] tick window accounting for the edge's lag.
func (m *Model) applyInterventions(tr Transition, ivs []Intervention, fromTick, toTick uint64) (Transition, []string) {
	if len(ivs) == 0 || len(m.CausalEdges) == 0 {
		return tr, nil
	}
	edges := append([]CausalEdge(nil), m.CausalEdges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Cause != edges[j].Cause {
			return edges[i].Cause < edges[j].Cause
		}
		return edges[i].Effect < edges[j].Effect
	})
	sorted := append([]Intervention(nil), ivs...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Tick != sorted[j].Tick {
			return sorted[i].Tick < sorted[j].Tick
		}
		return sorted[i].Cause < sorted[j].Cause
	})

	var applied []string
	out := tr
	for _, e := range edges {
		for _, iv := range sorted {
			if iv.Cause != e.Cause {
				continue
			}
			effectAt := iv.Tick + e.Lag
			if effectAt <= fromTick || effectAt > toTick {
				continue
			}
			shift := e.Strength * clamp01(iv.Magnitude)
			out = shiftInflow(out, e.Effect, shift)
			applied = append(applied, fmt.Sprintf("do(%s)@%d -> %s shift %+.3f", iv.Cause, iv.Tick, e.Effect, shift))
		}
	}
	return out, applied
}

// shiftInflow moves probability mass in every row toward (positive) or
// away from (negative) `target`, renormalising. Mass is taken from /
// given to the other states in proportion to their current mass, so
// the relative ordering of the non-target states is preserved — an
// intervention on one state must not silently reorder the others.
func shiftInflow(tr Transition, target State, shift float64) Transition {
	if shift == 0 {
		return tr
	}
	out := Transition{States: append([]State(nil), tr.States...), P: map[State]map[State]float64{}}
	for _, from := range tr.States {
		row := make(map[State]float64, len(tr.States))
		cur := tr.P[from][target]
		var delta float64
		if shift > 0 {
			delta = (1 - cur) * shift
		} else {
			delta = cur * shift
		}
		rest := 1 - cur
		for _, to := range tr.States {
			if to == target {
				row[to] = cur + delta
				continue
			}
			if rest <= 0 {
				row[to] = 0
				continue
			}
			row[to] = tr.P[from][to] * (rest - delta) / rest
		}
		out.P[from] = normalize(tr.States, row)
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func uniformOnes(states []State) map[State]float64 {
	out := make(map[State]float64, len(states))
	for _, s := range states {
		out[s] = 1
	}
	return out
}

func entropy(states []State, d map[State]float64) float64 {
	var h float64
	for _, s := range states {
		p := d[s]
		if p > 0 {
			h -= p * math.Log2(p)
		}
	}
	return h
}

func mapPath(states []State, steps []StepResult) []State {
	out := make([]State, len(steps))
	for i, st := range steps {
		best, bestP := states[0], -1.0
		for _, s := range states {
			if p := st.Smoothed[s]; p > bestP {
				best, bestP = s, p
			}
		}
		out[i] = best
	}
	return out
}

func hashTrace(m *Model, tr InferenceTrace) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.hbayes.trace/v1|decay=%.9f|", m.DecayLambda)
	for _, s := range m.States {
		fmt.Fprintf(h, "state=%s|", s)
	}
	for _, st := range tr.Steps {
		fmt.Fprintf(h, "t=%d|n=%d|eff=%.9f|", st.Tick, st.ObservationCount, st.EffectiveEvidence)
		for _, s := range m.States {
			fmt.Fprintf(h, "f%s=%.9f|s%s=%.9f|", s, st.Filtered[s], s, st.Smoothed[s])
		}
	}
	fmt.Fprintf(h, "ll=%.9f|", tr.LogLikelihood)
	return hex.EncodeToString(h.Sum(nil))
}

// Replay re-runs inference from the same inputs and confirms the trace
// hash matches — the PHASE 9 replay contract applied to the Bayesian
// layer, so a posterior is as verifiable as an arbitration.
func (m *Model) Replay(seq []TickObservations, interventions []Intervention, want InferenceTrace) error {
	got, err := m.Infer(seq, interventions)
	if err != nil {
		return err
	}
	if got.TraceHash != want.TraceHash {
		return fmt.Errorf("hbayes: replay mismatch: got %s want %s", got.TraceHash, want.TraceHash)
	}
	return nil
}

// Posterior is a convenience for "what do we believe about state s
// right now", the number a decision layer consumes.
func (tr InferenceTrace) Posterior(s State) float64 { return tr.FinalFiltered[s] }

// SmoothedAt returns the hindsight belief at a given tick.
func (tr InferenceTrace) SmoothedAt(tick uint64, s State) (float64, bool) {
	for _, st := range tr.Steps {
		if st.Tick == tick {
			return st.Smoothed[s], true
		}
	}
	return 0, false
}
