// Package hbayes closes PHASE 5 of the v7.10.3 production-readiness
// audit: the three RS-CHDBN gaps VERIQO's own README admitted —
// temporal propagation, explicit hidden-state inference, and causal
// edges between Bayesian nodes.
//
// Relationship to pkg/moat/fusion/hbayes: that package is the
// CROSS-SECTIONAL model — at one instant, how much do N correlated
// sources jointly tell us. It stays exactly as it is. This package is
// the TEMPORAL model: how does an unobservable state (is this vessel
// running dark? is this cargo mis-declared?) evolve over time, and
// what do a sequence of noisy, sometimes-missing, sometimes-
// contradictory observations imply about it — at every tick, not just
// the last one.
//
// Concretely this is a discrete-state hidden Markov model with:
//
//   - forward filtering       P(state_t | obs_1..t)
//   - backward smoothing      P(state_t | obs_1..T)  (hindsight)
//   - transition model        P(state_t+1 | state_t), row-stochastic
//   - temporal decay          transitions blend toward the stationary
//     prior as the gap between ticks grows, so a
//     6-month-old observation does not pin the
//     posterior
//   - missing observations    a tick with no evidence propagates the
//     transition only — uncertainty grows, it does
//     not silently persist
//   - contradictory obs       multiplied in as independent likelihoods
//     within a tick, so they cancel rather than the
//     last one winning
//   - correlated sources      an explicit per-tick correlation
//     discount on the joint likelihood exponent
//   - causal edges            an intervention on a causally upstream
//     node shifts the transition matrix, which is
//     what distinguishes a CAUSAL Bayesian model
//     from a correlational one
//
// Determinism: every routine here is a pure function of its inputs.
// No maps are iterated without sorting, no clock is read, no
// randomness is used. Two runs over identical input produce
// bit-identical posteriors, which is what makes InferenceTrace
// replayable evidence rather than a debug aid.
package hbayes

import (
	"errors"
	"fmt"
	"math"
)

// State is one discrete hidden state label, e.g. "DARK", "NORMAL".
type State string

// Errors.
var (
	ErrNoStates          = errors.New("hbayes: model must declare at least two hidden states")
	ErrUnknownState      = errors.New("hbayes: unknown hidden state")
	ErrNotStochastic     = errors.New("hbayes: transition rows must be non-negative and sum to 1")
	ErrBadPrior          = errors.New("hbayes: prior must be a probability distribution over the declared states")
	ErrBadLikelihood     = errors.New("hbayes: observation likelihood must be in [0,1]")
	ErrNoObservations    = errors.New("hbayes: inference requires at least one observation tick")
	ErrNonMonotonicTicks = errors.New("hbayes: observation ticks must be strictly increasing")
	ErrDegenerate        = errors.New("hbayes: observations are jointly impossible under this model (zero total likelihood)")
	ErrBadCorrelation    = errors.New("hbayes: correlation must be in [0,1)")
)

// Transition is a row-stochastic transition model over states.
type Transition struct {
	States []State
	// P[from][to]
	P map[State]map[State]float64
}

// Validate enforces row-stochasticity to 1e-9.
func (t Transition) Validate() error {
	if len(t.States) < 2 {
		return ErrNoStates
	}
	for _, from := range t.States {
		row, ok := t.P[from]
		if !ok {
			return fmt.Errorf("%w: missing row for %s", ErrNotStochastic, from)
		}
		sum := 0.0
		for _, to := range t.States {
			v, ok := row[to]
			if !ok {
				return fmt.Errorf("%w: missing %s->%s", ErrNotStochastic, from, to)
			}
			if v < 0 {
				return fmt.Errorf("%w: negative %s->%s", ErrNotStochastic, from, to)
			}
			sum += v
		}
		if math.Abs(sum-1) > 1e-9 {
			return fmt.Errorf("%w: row %s sums to %.12f", ErrNotStochastic, from, sum)
		}
	}
	return nil
}

// Stationary computes the model's long-run distribution by power
// iteration — deterministic, fixed iteration count, no convergence
// randomness. This is what temporal decay blends toward.
func (t Transition) Stationary() map[State]float64 {
	n := len(t.States)
	cur := make(map[State]float64, n)
	for _, s := range t.States {
		cur[s] = 1 / float64(n)
	}
	for iter := 0; iter < 512; iter++ {
		next := make(map[State]float64, n)
		for _, to := range t.States {
			var acc float64
			for _, from := range t.States {
				acc += cur[from] * t.P[from][to]
			}
			next[to] = acc
		}
		cur = next
	}
	return normalize(t.States, cur)
}

// Decayed returns the transition matrix applied over `gap` ticks with
// exponential blending toward the stationary distribution at rate
// lambda per tick. gap=1, lambda=0 reproduces P exactly.
//
//	P_eff = (1-w) * P^gap + w * 1·pi^T,  w = 1 - exp(-lambda*(gap-1))
//
// Rationale: two observations one tick apart should be linked by the
// modelled dynamics; two observations 10,000 ticks apart should be
// nearly independent. Without this, an HMM treats a year-old sighting
// as evidence about today with full strength, which is the single most
// common way temporal Bayesian models lie.
func (t Transition) Decayed(gap uint64, lambda float64) Transition {
	if gap == 0 {
		gap = 1
	}
	pow := t.pow(gap)
	if lambda <= 0 || gap == 1 {
		return pow
	}
	w := 1 - math.Exp(-lambda*float64(gap-1))
	pi := t.Stationary()
	out := Transition{States: append([]State(nil), t.States...), P: map[State]map[State]float64{}}
	for _, from := range t.States {
		row := make(map[State]float64, len(t.States))
		for _, to := range t.States {
			row[to] = (1-w)*pow.P[from][to] + w*pi[to]
		}
		out.P[from] = normalize(t.States, row)
	}
	return out
}

// pow computes P^n by repeated squaring, deterministically.
func (t Transition) pow(n uint64) Transition {
	result := identity(t.States)
	base := t
	for n > 0 {
		if n&1 == 1 {
			result = matmul(result, base)
		}
		base = matmul(base, base)
		n >>= 1
	}
	return result
}

func identity(states []State) Transition {
	out := Transition{States: append([]State(nil), states...), P: map[State]map[State]float64{}}
	for _, a := range states {
		row := make(map[State]float64, len(states))
		for _, b := range states {
			if a == b {
				row[b] = 1
			} else {
				row[b] = 0
			}
		}
		out.P[a] = row
	}
	return out
}

func matmul(a, b Transition) Transition {
	out := Transition{States: append([]State(nil), a.States...), P: map[State]map[State]float64{}}
	for _, i := range a.States {
		row := make(map[State]float64, len(a.States))
		for _, k := range a.States {
			var acc float64
			for _, j := range a.States {
				acc += a.P[i][j] * b.P[j][k]
			}
			row[k] = acc
		}
		out.P[i] = row
	}
	return out
}

func normalize(states []State, d map[State]float64) map[State]float64 {
	var sum float64
	for _, s := range states {
		if d[s] < 0 {
			d[s] = 0
		}
		sum += d[s]
	}
	out := make(map[State]float64, len(states))
	if sum == 0 {
		for _, s := range states {
			out[s] = 1 / float64(len(states))
		}
		return out
	}
	for _, s := range states {
		out[s] = d[s] / sum
	}
	return out
}

// Observation is one source's noisy read at one tick, expressed as a
// likelihood P(observation | state) for each state. Expressing
// observations as likelihoods rather than as asserted values is what
// lets contradictory sources cancel correctly instead of overwriting
// each other.
type Observation struct {
	SourceID string
	Tick     uint64
	// Likelihood[state] = P(this observation | that state), in [0,1].
	Likelihood map[State]float64
	// Correlation in [0,1) discounts this observation's independence
	// from others at the same tick — 0 fully independent, approaching
	// 1 fully redundant. Fed from pkg/moat/evidencegraph's shared
	// ancestor fraction (PHASE 1), so the dependency model and the
	// Bayesian model agree by construction rather than by convention.
	Correlation float64
}

func (o Observation) validate(states []State) error {
	if len(o.Likelihood) == 0 {
		return fmt.Errorf("%w: source %s has no likelihoods", ErrBadLikelihood, o.SourceID)
	}
	for _, s := range states {
		v, ok := o.Likelihood[s]
		if !ok {
			continue // partial observations are allowed; missing = uninformative
		}
		if v < 0 || v > 1 {
			return fmt.Errorf("%w: source %s state %s = %.6f", ErrBadLikelihood, o.SourceID, s, v)
		}
	}
	if o.Correlation < 0 || o.Correlation >= 1 {
		return fmt.Errorf("%w: source %s = %.6f", ErrBadCorrelation, o.SourceID, o.Correlation)
	}
	return nil
}

// CausalEdge is an edge between Bayesian nodes with an explicit causal
// interpretation: intervening on Cause shifts the transition dynamics
// of Effect. This is the third README gap — without it the model can
// only answer "what tends to co-occur", never "what happens if we act".
type CausalEdge struct {
	Cause  string // an intervention/exogenous node name
	Effect State  // the hidden state whose inflow is shifted
	// Strength in [-1,1]: positive pushes probability mass INTO Effect
	// when the cause is active, negative pulls it out.
	Strength float64
	// Lag in ticks between intervention and effect.
	Lag uint64
}

// Intervention is a do(X) operator applied at a tick.
type Intervention struct {
	Cause string
	Tick  uint64
	// Magnitude in [0,1] scales the edge strength.
	Magnitude float64
}
