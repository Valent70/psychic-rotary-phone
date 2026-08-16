// Package hbayes implements a hierarchical Bayesian correlation model for
// multi-source evidence fusion — the explicit successor the audit doc
// ("Gap_dan_kekurangan_yang_harus_ditambah.docx", section 4/C) asks for
// beyond fusion's noisy-OR approximation.
//
// Honest scope statement: this is NOT a general-purpose probabilistic
// programming engine (no MCMC, no variational inference, no learned
// posteriors from data). It is a small, closed-form 3-level hierarchical
// model — exactly the structure the audit doc specifies:
//
//	Level 1: latent true state        (is the claim's asserted event real?)
//	Level 2: provider groups          (shared upstream correlation)
//	Level 3: individual signals       (AIS, SAR, STS, manifest, sanction...)
//
// with an explicit correlation matrix between provider groups (not just
// "same provider = fully correlated, different = fully independent" as
// fusion.fuseGroup currently approximates). This lets Veriqo answer, and
// explain, questions like: "three vendors reselling the same satellite
// feed" should NOT triple-count as three independent confirmations, but
// "three vendors with 0.3 pairwise correlation" should partially count.
//
// The model is a deterministic, replayable pure-math layer: same inputs
// always produce the same posterior, no time.Now(), no external deps.
package hbayes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"veriqo/pkg/platform/telemetry"
)

var (
	ErrUnknownGroup       = errors.New("hbayes: unknown provider group")
	ErrDimensionMismatch  = errors.New("hbayes: correlation matrix dimension mismatch")
	ErrNotSymmetric       = errors.New("hbayes: correlation matrix must be symmetric")
	ErrInvalidCorrelation = errors.New("hbayes: correlation values must be in [-1,1] with 1.0 on the diagonal")
	// ErrNotPositiveSemidefinite is the P3 hardening named in
	// "Hasil_audit_brutal...docx" §14: a matrix can be square,
	// symmetric, unit-diagonal, and every entry within [-1,1] while
	// still not being a mathematically valid correlation matrix — e.g.
	// pairwise correlations of +0.9/+0.9/-0.9 among three variables are
	// individually in-range but jointly impossible (no random vector
	// has that covariance structure). Validate rejects any matrix that
	// fails a Cholesky-based PSD check.
	ErrNotPositiveSemidefinite = errors.New("hbayes: correlation matrix is not positive semidefinite (pairwise-valid but not jointly realizable)")
)

// ProviderGroupID identifies a group of signals sharing upstream data
// lineage (e.g. "AIS_VENDORS", "SAR_PROVIDERS", "SANCTION_LISTS").
type ProviderGroupID string

// ReliabilityPrior is the Level-2 prior belief in a provider group's
// trustworthiness, expressed as a Beta-like mean/variance summary
// (kept as mean+variance rather than full Beta(a,b) so callers can
// supply priors from simple elicitation without needing conjugate-prior
// math — ComputePosteriorReliabilities converts internally).
type ReliabilityPrior struct {
	GroupID  ProviderGroupID
	Mean     float64 // prior mean reliability in (0,1)
	Variance float64 // prior variance; smaller = more confident prior
}

// CorrelationMatrix is the Level-2 structure describing how correlated
// provider groups' signals are with each other. Groups sharing a high
// correlation (e.g. 0.8 between two AIS resellers of the same satellite
// feed) contribute diminishing marginal evidence when they agree.
type CorrelationMatrix struct {
	Groups []ProviderGroupID
	Values [][]float64 // Values[i][j] = correlation between Groups[i], Groups[j]
}

// Validate checks the matrix is square, symmetric, has 1.0 on the
// diagonal, and every value is in [-1,1].
func (m CorrelationMatrix) Validate() error {
	n := len(m.Groups)
	if len(m.Values) != n {
		return ErrDimensionMismatch
	}
	for i, row := range m.Values {
		if len(row) != n {
			return ErrDimensionMismatch
		}
		for j, v := range row {
			if v < -1 || v > 1 {
				return ErrInvalidCorrelation
			}
			if i == j && math.Abs(v-1.0) > 1e-9 {
				return ErrInvalidCorrelation
			}
			if m.Values[j][i] != v {
				return ErrNotSymmetric
			}
		}
	}
	if n > 0 && !isPositiveSemidefinite(m.Values) {
		return ErrNotPositiveSemidefinite
	}
	return nil
}

// isPositiveSemidefinite checks PSD via a Cholesky-Banachiewicz
// decomposition with a small numerical tolerance: a real symmetric
// matrix is PSD iff its Cholesky decomposition succeeds with no
// negative pivot beyond floating-point noise. This is the standard,
// numerically cheap way to validate a correlation matrix without
// pulling in an external linear-algebra dependency (this repo's
// zero-external-dependency invariant) — no eigenvalue solver needed,
// just forward substitution.
func isPositiveSemidefinite(vals [][]float64) bool {
	n := len(vals)
	const tol = -1e-9
	l := make([][]float64, n)
	for i := range l {
		l[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := vals[i][j]
			for k := 0; k < j; k++ {
				sum -= l[i][k] * l[j][k]
			}
			if i == j {
				if sum < tol {
					return false
				}
				if sum < 0 {
					sum = 0 // clamp floating-point noise at the boundary
				}
				l[i][j] = math.Sqrt(sum)
			} else if l[j][j] > 1e-12 {
				l[i][j] = sum / l[j][j]
			} else if math.Abs(sum) > 1e-9 {
				// zero pivot but a nonzero entry depends on it — the
				// matrix is singular in a way inconsistent with PSD.
				return false
			}
		}
	}
	return true
}

func (m CorrelationMatrix) indexOf(g ProviderGroupID) (int, bool) {
	for i, gg := range m.Groups {
		if gg == g {
			return i, true
		}
	}
	return -1, false
}

func (m CorrelationMatrix) correlationBetween(a, b ProviderGroupID) (float64, error) {
	if a == b {
		return 1.0, nil
	}
	ia, ok := m.indexOf(a)
	if !ok {
		return 0, ErrUnknownGroup
	}
	ib, ok := m.indexOf(b)
	if !ok {
		return 0, ErrUnknownGroup
	}
	return m.Values[ia][ib], nil
}

// Signal is one Level-3 individual observation: a source, its provider
// group, and the reliability it asserts for the observed claim value.
type Signal struct {
	SourceID string
	Group    ProviderGroupID
	// Confirms indicates whether this signal supports (true) or
	// contradicts (false) the hypothesis being evaluated.
	Confirms bool
	// Reliability is the source-level reliability in (0,1], as already
	// tracked by fusion.SourceProfile — passed through unchanged.
	Reliability float64
}

// Model bundles the Level-2 structure (priors + correlation) needed to
// compute posteriors and event risk from Level-3 signals.
type Model struct {
	Priors      map[ProviderGroupID]ReliabilityPrior
	Correlation CorrelationMatrix
}

func NewModel(priors []ReliabilityPrior, corr CorrelationMatrix) (*Model, error) {
	if err := corr.Validate(); err != nil {
		return nil, err
	}
	pm := make(map[ProviderGroupID]ReliabilityPrior, len(priors))
	for _, p := range priors {
		pm[p.GroupID] = p
	}
	for _, g := range corr.Groups {
		if _, ok := pm[g]; !ok {
			return nil, ErrUnknownGroup
		}
	}
	return &Model{Priors: pm, Correlation: corr}, nil
}

// PosteriorReliability is the Level-2 posterior for one provider group
// after observing how many signals from it confirmed vs. contradicted.
type PosteriorReliability struct {
	GroupID       ProviderGroupID
	PriorMean     float64
	PosteriorMean float64
	Confirms      int
	Contradicts   int
}

// ComputePosteriorReliabilities updates each provider group's Level-2
// reliability belief using a simple, closed-form Bayesian update
// (moment-matched Beta update via mean/variance -> pseudo-counts), given
// the observed confirm/contradict tally per group from a signal set.
// This is the "posterior on reliability itself" the audit doc asks for,
// deliberately kept closed-form (no MCMC) to preserve zero-dependency,
// deterministic replay.
func (m *Model) ComputePosteriorReliabilities(signals []Signal) []PosteriorReliability {
	tally := make(map[ProviderGroupID][2]int) // [confirms, contradicts]
	for _, s := range signals {
		t := tally[s.Group]
		if s.Confirms {
			t[0]++
		} else {
			t[1]++
		}
		tally[s.Group] = t
	}

	groups := make([]ProviderGroupID, 0, len(m.Priors))
	for g := range m.Priors {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })

	out := make([]PosteriorReliability, 0, len(groups))
	for _, g := range groups {
		prior := m.Priors[g]
		t := tally[g]
		confirms, contradicts := t[0], t[1]

		// Beta moment-matching: mean=mu, var=v => alpha=mu*((mu(1-mu)/v)-1)
		mu := prior.Mean
		v := prior.Variance
		if v <= 0 {
			v = 0.01
		}
		if mu <= 0 || mu >= 1 {
			mu = 0.5
		}
		kappa := (mu*(1-mu))/v - 1
		if kappa < 2 {
			kappa = 2 // floor so alpha/beta stay positive and pseudo-counts are sane
		}
		alpha := mu * kappa
		beta := (1 - mu) * kappa

		postAlpha := alpha + float64(confirms)
		postBeta := beta + float64(contradicts)
		postMean := postAlpha / (postAlpha + postBeta)

		out = append(out, PosteriorReliability{
			GroupID: g, PriorMean: prior.Mean, PosteriorMean: postMean,
			Confirms: confirms, Contradicts: contradicts,
		})
	}
	return out
}

// EventRiskResult is the Level-1 output: the fused, correlation-aware
// probability that the hypothesis (e.g. "vessel is engaged in a sanction
// evasion pattern") is true given all Level-3 signals.
type EventRiskResult struct {
	Probability float64
	// EffectiveIndependentSignals: how many "fully independent" signals
	// this evidence set is equivalent to, after correlation discounting —
	// the concrete number that proves saturation (e.g. "3 correlated AIS
	// resellers count as ~1.2 independent confirmations, not 3").
	EffectiveIndependentSignals float64
	GroupContributions          []GroupContribution
	// NegativeEvidenceDiscount is the P3 hardening ("Hasil_audit_brutal
	// ...docx" §13, "explicit negative evidence"): the combined,
	// correlation-discounted probability contributed by CONTRADICTING
	// signals (Confirms=false), which explains away Probability
	// multiplicatively (Probability = confirmProb*(1-this)). Zero when
	// no contradicting signals were supplied — identical to the prior
	// behavior, which silently dropped them.
	NegativeEvidenceDiscount float64
}

type GroupContribution struct {
	GroupID          ProviderGroupID
	RawSignalCount   int
	DiscountedWeight float64 // after intra-group correlation is accounted for
}

// ComputeEventRisk fuses Level-3 confirming signals into a Level-1 event
// probability, respecting Level-2 correlation: signals within a highly
// correlated group are discounted (their combined weight saturates
// rather than compounding independently), and pairwise correlation
// between DIFFERENT groups further discounts the cross-group combination.
//
// Algorithm (closed-form, deterministic):
//  1. Within each group, effective confirming weight = 1 - (1-avgReliability)^(1 + (n-1)*(1-rho_intra))
//     where rho_intra is the group's self-correlation floor (we use the
//     matrix diagonal as 1.0 by definition, and treat same-group signals
//     as correlated at strength = mean off-diagonal correlation the group
//     has with itself via a configurable IntraGroupCorrelation on the
//     prior's Variance-derived confidence — kept simple and explicit
//     below via the passed-in intraCorrelation parameter per group).
//  2. Across groups, combine group-level weights via noisy-OR but scaled
//     down by the average pairwise inter-group correlation, so two
//     strongly correlated groups (e.g. AIS + a SAR vendor fed by the same
//     satellite operator) don't double-count either.
func (m *Model) ComputeEventRisk(signals []Signal, intraGroupCorrelation map[ProviderGroupID]float64) (EventRiskResult, error) {
	_, span := telemetry.StartSpan(context.Background(), "hbayes.ComputeEventRisk",
		telemetry.Attribute{Key: "signal_count", Value: fmt.Sprintf("%d", len(signals))})
	defer span.End()

	// P3 hardening (audit §13): use the Level-2 POSTERIOR reliability —
	// not the raw per-signal Reliability field — as each group's
	// confirming-weight basis. This is the actual hierarchical-Bayesian
	// step: ComputePosteriorReliabilities already folds the group's
	// prior together with how many of ITS OWN signals confirmed vs.
	// contradicted; using it here (instead of a plain average of raw
	// signal.Reliability) means Level-1 event risk is genuinely built
	// on the Level-2 posterior, not a parallel, disconnected number.
	posteriors := m.ComputePosteriorReliabilities(signals)
	posteriorMean := make(map[ProviderGroupID]float64, len(posteriors))
	for _, p := range posteriors {
		posteriorMean[p.GroupID] = p.PosteriorMean
	}

	confirmProb, effectiveTotal, contributions, err := m.combineDirectional(signals, true, intraGroupCorrelation, posteriorMean)
	if err != nil {
		return EventRiskResult{}, err
	}
	// P3 hardening (audit §13, "explicit negative evidence"): signals
	// with Confirms=false were previously dropped entirely from
	// ComputeEventRisk (only used by ComputePosteriorReliabilities'
	// tally). They now explicitly explain away the confirming
	// probability, combined with the identical correlation-aware
	// machinery so a contradicting signal from a correlated source is
	// discounted the same way a confirming one would be.
	contraProb, _, _, err := m.combineDirectional(signals, false, intraGroupCorrelation, posteriorMean)
	if err != nil {
		return EventRiskResult{}, err
	}

	return EventRiskResult{
		Probability:                 confirmProb * (1 - contraProb),
		EffectiveIndependentSignals: effectiveTotal,
		GroupContributions:          contributions,
		NegativeEvidenceDiscount:    contraProb,
	}, nil
}

// combineDirectional runs the correlation-aware, order-invariant
// combination for either the confirming (wantConfirms=true) or
// contradicting (wantConfirms=false) subset of signals. Order
// invariance (P3 hardening, audit §15): the original algorithm
// discounted each group only by its correlation to groups already
// combined earlier in a fixed sort order — mathematically an ordering
// artifact (permuting which group is "first" changes the result even
// though the input SET is identical). This version instead computes,
// for every active group, its average pairwise correlation to every
// OTHER active group (a value that depends only on the set of active
// groups, never on iteration order) and combines via a plain product —
// a commutative, associative operation — so the result is provably the
// same for any permutation of the same group set.
func (m *Model) combineDirectional(signals []Signal, wantConfirms bool, intraGroupCorrelation map[ProviderGroupID]float64, posteriorMean map[ProviderGroupID]float64) (float64, float64, []GroupContribution, error) {
	byGroup := make(map[ProviderGroupID][]Signal)
	for _, s := range signals {
		if s.Confirms == wantConfirms {
			byGroup[s.Group] = append(byGroup[s.Group], s)
		}
	}
	groups := make([]ProviderGroupID, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] }) // stable output order only, not semantically load-bearing

	groupWeight := make(map[ProviderGroupID]float64, len(groups))
	contributions := make([]GroupContribution, 0, len(groups))
	effectiveTotal := 0.0

	for _, g := range groups {
		sigs := byGroup[g]
		rho := intraGroupCorrelation[g]
		if rho < 0 {
			rho = 0
		}
		if rho > 1 {
			rho = 1
		}
		avgRel, ok := posteriorMean[g]
		if !ok {
			// group absent from the Level-2 posterior map (shouldn't
			// normally happen — every group with a prior gets a
			// posterior — but fall back to the raw signal average
			// rather than panicking or silently zeroing the group out).
			for _, s := range sigs {
				avgRel += s.Reliability
			}
			if len(sigs) > 0 {
				avgRel /= float64(len(sigs))
			}
		}
		n := float64(len(sigs))
		effectiveN := 1 + (n-1)*(1-rho)
		weight := 1 - math.Pow(1-avgRel, effectiveN)
		groupWeight[g] = weight
		effectiveTotal += effectiveN
		contributions = append(contributions, GroupContribution{
			GroupID: g, RawSignalCount: len(sigs), DiscountedWeight: weight,
		})
	}

	if len(groups) == 0 {
		return 0, 0, contributions, nil
	}

	logComplement := 0.0
	for _, g := range groups {
		w := groupWeight[g]
		avgCorr := 0.0
		if len(groups) > 1 {
			sum := 0.0
			for _, other := range groups {
				if other == g {
					continue
				}
				corr, err := m.Correlation.correlationBetween(g, other)
				if err != nil {
					return 0, 0, nil, err
				}
				if corr < 0 {
					corr = 0
				}
				sum += corr
			}
			avgCorr = sum / float64(len(groups)-1)
		}
		effW := w * (1 - avgCorr)
		if effW > 0.999999 {
			effW = 0.999999
		}
		if effW < 0 {
			effW = 0
		}
		logComplement += math.Log(1 - effW)
	}
	prob := 1 - math.Exp(logComplement)
	return prob, effectiveTotal, contributions, nil
}
