// Package economic closes the "Economic Consequence Engine
// completion" item of the v7.11 audit: a decision must produce a
// deterministic, replayable economic consequence, not a scalar impact
// number.
//
// pkg/moat/digitaltwin already computes a channel-weighted economic
// impact for a twin. That is a point estimate. This package answers
// the question a point estimate cannot: what is the distribution of
// outcomes, and how bad is the bad tail?
//
// It is deliberately discrete. A closed-form parametric distribution
// would be shorter to write and impossible to audit: a regulator
// cannot inspect a lognormal. A named, enumerated scenario set with
// explicit probabilities can be read, argued with, and replayed.
//
// Conventions: outcomes are signed monetary values in one currency
// unit, positive = gain, negative = loss. VaR and CVaR are reported as
// POSITIVE loss magnitudes (the industry convention), so a VaR of 400
// means "a loss of 400", never "a gain of -400".
package economic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"veriqo/pkg/platform/telemetry"
)

var (
	// ErrNoScenarios rejects an empty scenario set. An empty set has no
	// expected value; returning 0 would assert "this decision is free".
	ErrNoScenarios = errors.New("economic: no scenarios")
	// ErrProbabilityMass rejects a scenario set whose probabilities do
	// not sum to 1 within tolerance. Auto-normalising is how a dropped
	// scenario becomes invisible.
	ErrProbabilityMass = errors.New("economic: scenario probabilities must sum to 1")
	// ErrProbabilityRange rejects a non-finite or out-of-range weight.
	ErrProbabilityRange = errors.New("economic: scenario probability outside [0,1]")
	// ErrDuplicateScenario rejects two scenarios with the same ID —
	// otherwise a duplicated tail silently doubles its own mass.
	ErrDuplicateScenario = errors.New("economic: duplicate scenario id")
	// ErrConfidenceLevel rejects a VaR level outside (0,1).
	ErrConfidenceLevel = errors.New("economic: confidence level must be in (0,1)")
	// ErrNoLineage refuses to emit a consequence that cannot be traced
	// back to the decision that caused it — the same invariant
	// pkg/moat/simulation enforces for simulation results.
	ErrNoLineage = errors.New("economic: consequence lineage incomplete")
)

// probabilityTolerance is the mass-sum tolerance. Deliberately tight:
// a 1e-6 slack absorbs float summation error and nothing else.
const probabilityTolerance = 1e-6

// Scenario is one enumerated outcome of a decision.
type Scenario struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Probability float64 `json:"probability"`
	// Outcome is the signed monetary consequence. Negative = loss.
	Outcome float64 `json:"outcome"`
	// Channels decomposes Outcome by economic channel (freight,
	// insurance, sanctions exposure, ...). It is carried for
	// explanation, and is NOT re-summed into Outcome: a caller that
	// disagrees with its own decomposition should be told, not
	// silently corrected.
	Channels map[string]float64 `json:"channels"`
	// EvidenceRefs are the evidence IDs that justify this scenario's
	// probability. A scenario with no justification is allowed but is
	// reported in Consequence.Unjustified.
	EvidenceRefs []string `json:"evidence_refs"`
}

// Consequence is the full, content-addressed economic result.
type Consequence struct {
	DecisionID   string `json:"decision_id"`
	Subject      string `json:"subject"`
	Tick         uint64 `json:"tick"`
	Currency     string `json:"currency"`
	ScenarioHash string `json:"scenario_hash"`

	ExpectedValue float64 `json:"expected_value"`
	ExpectedLoss  float64 `json:"expected_loss"` // E[max(0,-X)], positive
	ExpectedGain  float64 `json:"expected_gain"` // E[max(0, X)], positive
	WorstCase     float64 `json:"worst_case"`    // min outcome (signed)
	BestCase      float64 `json:"best_case"`     // max outcome (signed)
	Variance      float64 `json:"variance"`
	StdDev        float64 `json:"std_dev"`

	ConfidenceLevel float64 `json:"confidence_level"`
	VaR             float64 `json:"var"`  // positive loss magnitude
	CVaR            float64 `json:"cvar"` // positive loss magnitude, >= VaR

	TailProbability float64            `json:"tail_probability"`
	ChannelExpected map[string]float64 `json:"channel_expected"`
	Scenarios       []Scenario         `json:"scenarios"`
	Unjustified     []string           `json:"unjustified"`

	Hash string `json:"hash"`
}

// Input is one economic evaluation request. Every lineage field is
// mandatory: an economic consequence with no decision, no subject and
// no scenario provenance is a number, not evidence.
type Input struct {
	DecisionID      string
	Subject         string
	Currency        string
	Tick            uint64
	ConfidenceLevel float64 // e.g. 0.95; defaults to 0.95 when zero
	Scenarios       []Scenario
}

// Evaluate computes the consequence.
//
// Failure semantics: every rejection is an error, never a
// silently-degraded result. In particular, probability mass that does
// not sum to 1 is rejected rather than normalised, because the most
// common way a risk model understates a tail is by dropping the tail
// scenario and renormalising what is left.
func Evaluate(in Input) (Consequence, error) {
	_, span := telemetry.StartSpan(context.Background(), "economic.Evaluate",
		telemetry.Attribute{Key: "decision_id", Value: in.DecisionID}, telemetry.Attribute{Key: "subject", Value: in.Subject})
	defer span.End()

	if in.DecisionID == "" || in.Subject == "" {
		return Consequence{}, fmt.Errorf("%w: decision id and subject are required", ErrNoLineage)
	}
	if len(in.Scenarios) == 0 {
		return Consequence{}, ErrNoScenarios
	}
	level := in.ConfidenceLevel
	if level == 0 {
		level = 0.95
	}
	if level <= 0 || level >= 1 || math.IsNaN(level) {
		return Consequence{}, ErrConfidenceLevel
	}

	seen := make(map[string]struct{}, len(in.Scenarios))
	var mass float64
	for _, s := range in.Scenarios {
		if s.ID == "" {
			return Consequence{}, fmt.Errorf("%w: scenario id is required", ErrNoLineage)
		}
		if _, dup := seen[s.ID]; dup {
			return Consequence{}, fmt.Errorf("%w: %s", ErrDuplicateScenario, s.ID)
		}
		seen[s.ID] = struct{}{}
		if s.Probability < 0 || s.Probability > 1 || math.IsNaN(s.Probability) {
			return Consequence{}, fmt.Errorf("%w: %s p=%v", ErrProbabilityRange, s.ID, s.Probability)
		}
		if math.IsNaN(s.Outcome) || math.IsInf(s.Outcome, 0) {
			return Consequence{}, fmt.Errorf("economic: scenario %s has a non-finite outcome", s.ID)
		}
		mass += s.Probability
	}
	if math.Abs(mass-1) > probabilityTolerance {
		return Consequence{}, fmt.Errorf("%w: got %v", ErrProbabilityMass, mass)
	}

	// Canonical ordering: ascending outcome (loss first), ID as the
	// tie-break. Every quantile below depends on this order, so it is
	// established once and never re-derived.
	sc := append([]Scenario(nil), in.Scenarios...)
	sort.SliceStable(sc, func(i, j int) bool {
		if sc[i].Outcome != sc[j].Outcome {
			return sc[i].Outcome < sc[j].Outcome
		}
		return sc[i].ID < sc[j].ID
	})

	c := Consequence{
		DecisionID: in.DecisionID, Subject: in.Subject, Currency: in.Currency,
		Tick: in.Tick, ConfidenceLevel: level, Scenarios: sc,
		ChannelExpected: map[string]float64{},
		WorstCase:       sc[0].Outcome, BestCase: sc[len(sc)-1].Outcome,
	}

	for _, s := range sc {
		c.ExpectedValue += s.Probability * s.Outcome
		if s.Outcome < 0 {
			c.ExpectedLoss += s.Probability * (-s.Outcome)
		} else {
			c.ExpectedGain += s.Probability * s.Outcome
		}
		for k, v := range s.Channels {
			c.ChannelExpected[k] += s.Probability * v
		}
		if len(s.EvidenceRefs) == 0 {
			c.Unjustified = append(c.Unjustified, s.ID)
		}
	}
	for _, s := range sc {
		d := s.Outcome - c.ExpectedValue
		c.Variance += s.Probability * d * d
	}
	c.StdDev = math.Sqrt(c.Variance)
	sort.Strings(c.Unjustified)

	// VaR at level L is the loss not exceeded with probability L, i.e.
	// the (1-L) lower quantile of the outcome distribution, reported as
	// a positive magnitude. Discrete quantile: walk the ascending
	// outcomes accumulating mass until it reaches 1-L.
	alpha := 1 - level
	var cum float64
	varOutcome := sc[0].Outcome
	tailIdx := 0
	for i, s := range sc {
		cum += s.Probability
		varOutcome = s.Outcome
		tailIdx = i
		if cum >= alpha-probabilityTolerance {
			break
		}
	}
	c.VaR = math.Max(0, -varOutcome)

	// CVaR is the probability-weighted mean of the tail up to and
	// including the VaR scenario, with the VaR scenario's mass clipped
	// to exactly fill alpha. Without the clip, a single fat scenario
	// straddling the quantile drags CVaR toward its own value and the
	// CVaR >= VaR invariant can break on coarse distributions.
	var tailMass, tailSum float64
	for i := 0; i <= tailIdx; i++ {
		w := sc[i].Probability
		if i == tailIdx {
			if remaining := alpha - tailMass; remaining < w {
				w = math.Max(remaining, 0)
			}
		}
		tailMass += w
		tailSum += w * sc[i].Outcome
	}
	if tailMass > 0 {
		c.CVaR = math.Max(0, -(tailSum / tailMass))
	} else {
		c.CVaR = c.VaR
	}
	if c.CVaR < c.VaR {
		c.CVaR = c.VaR
	}
	c.TailProbability = tailMass

	c.ScenarioHash = hashScenarios(sc)
	c.Hash = c.computeHash()
	return c, nil
}

// TailRisk returns the total probability of an outcome at or below the
// supplied threshold. It is the question an underwriter actually asks:
// "what is the chance this costs me more than X?"
func (c Consequence) TailRisk(threshold float64) float64 {
	var p float64
	for _, s := range c.Scenarios {
		if s.Outcome <= threshold {
			p += s.Probability
		}
	}
	return p
}

// Dominates reports whether c is preferable to other under second-order
// stochastic dominance restricted to the comparisons this engine can
// make honestly: strictly better expected value AND no worse tail.
// Anything else returns false rather than inventing a ranking.
func (c Consequence) Dominates(other Consequence) bool {
	return c.ExpectedValue > other.ExpectedValue &&
		c.CVaR <= other.CVaR &&
		c.WorstCase >= other.WorstCase
}

func hashScenarios(sc []Scenario) string {
	var sb strings.Builder
	sb.WriteString("scenarios/v1\n")
	for _, s := range sc {
		sb.WriteString(s.ID + "|" + fnum(s.Probability) + "|" + fnum(s.Outcome) + "|")
		keys := make([]string, 0, len(s.Channels))
		for k := range s.Channels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(k + "=" + fnum(s.Channels[k]) + ",")
		}
		refs := append([]string(nil), s.EvidenceRefs...)
		sort.Strings(refs)
		sb.WriteString("|" + strings.Join(refs, ";") + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func (c Consequence) computeHash() string {
	var sb strings.Builder
	sb.WriteString("economic_consequence/v1\n")
	sb.WriteString("decision=" + c.DecisionID + "\nsubject=" + c.Subject + "\n")
	sb.WriteString("currency=" + c.Currency + "\ntick=" + strconv.FormatUint(c.Tick, 10) + "\n")
	sb.WriteString("scenarios=" + c.ScenarioHash + "\n")
	sb.WriteString("ev=" + fnum(c.ExpectedValue) + "\nel=" + fnum(c.ExpectedLoss) +
		"\neg=" + fnum(c.ExpectedGain) + "\n")
	sb.WriteString("worst=" + fnum(c.WorstCase) + "\nbest=" + fnum(c.BestCase) + "\n")
	sb.WriteString("var=" + fnum(c.Variance) + "\n")
	sb.WriteString("level=" + fnum(c.ConfidenceLevel) + "\nVaR=" + fnum(c.VaR) +
		"\nCVaR=" + fnum(c.CVaR) + "\n")
	keys := make([]string, 0, len(c.ChannelExpected))
	for k := range c.ChannelExpected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString("ch:" + k + "=" + fnum(c.ChannelExpected[k]) + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// Verify recomputes the consequence hash from the value's own fields,
// so a stored consequence can be checked without the engine.
func Verify(c Consequence) error {
	if hashScenarios(c.Scenarios) != c.ScenarioHash {
		return errors.New("economic: scenario hash mismatch")
	}
	if c.computeHash() != c.Hash {
		return errors.New("economic: consequence hash mismatch")
	}
	return nil
}

func fnum(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }
