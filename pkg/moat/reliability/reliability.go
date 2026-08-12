// Package reliability closes PHASE 46 of the v7.11 audit: the
// calibration and reliability capability that pkg/moat/calibration
// deliberately left unfinished (it computes Brier score and nothing
// else).
//
// This package is NOT a second calibration ledger. It consumes
// (predicted probability, observed outcome) pairs — which is exactly
// what calibration.Record already carries — and answers the questions
// Brier alone cannot:
//
//   - Log loss: is the model confidently wrong? (Brier is bounded and
//     forgiving; log loss is unbounded and is what actually punishes a
//     0.99 prediction on a negative outcome.)
//   - Expected Calibration Error: when the model says 0.8, does the
//     event happen 80% of the time?
//   - Reliability curve: the per-bin decomposition of that answer,
//     with Wilson intervals so a 2-sample bin is not read as evidence.
//   - Calibration models: Platt scaling and isotonic regression (PAV),
//     emitted as versioned, content-addressed artifacts so a decision
//     can never be replayed under a different calibration map without
//     detection.
//   - Drift: PSI, KL, JS and two-sample KS between a frozen reference
//     distribution and a current one.
//
// Determinism rules of this repo apply: no wall clock (ticks only), no
// map iteration order in any hash, no randomness. Every artifact is
// SHA-256 content-addressed over a key-sorted canonical serialisation.
package reliability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrNoSamples is returned by every metric that is undefined on an
	// empty sample set. Returning 0 would be a lie: 0 is a perfect
	// score for ECE and log loss.
	ErrNoSamples = errors.New("reliability: no samples")
	// ErrProbabilityRange rejects a predicted probability outside
	// [0,1]. Silently clamping is how a broken upstream model hides.
	ErrProbabilityRange = errors.New("reliability: predicted probability outside [0,1]")
	// ErrBinCount rejects a non-positive bin count.
	ErrBinCount = errors.New("reliability: bin count must be positive")
	// ErrDistributionShape rejects reference/current histograms that
	// cannot be compared bucket-for-bucket.
	ErrDistributionShape = errors.New("reliability: distributions have different support")
	// ErrNotFitted is returned when a calibrator is used before Fit.
	ErrNotFitted = errors.New("reliability: calibrator not fitted")
	// ErrDegraded is returned by (Assessment).Err when the model has
	// failed its calibration health thresholds. A degraded model is
	// never silently "healthy" — PHASE 46's explicit requirement.
	ErrDegraded = errors.New("reliability: calibration degraded beyond threshold")
)

// epsilon bounds log loss so a probability of exactly 0 or 1 does not
// produce +Inf. 1e-15 is the standard choice and is documented rather
// than hidden because it is a real (if tiny) modelling decision.
const epsilon = 1e-15

// Sample is one (prediction, outcome) pair. Weight allows a caller to
// aggregate identical samples without materialising them; it defaults
// to 1 when non-positive.
type Sample struct {
	ClaimKey  string
	Predicted float64 // P(outcome = true), in [0,1]
	Outcome   bool
	Weight    float64
	Tick      uint64
}

func (s Sample) weight() float64 {
	if s.Weight <= 0 {
		return 1
	}
	return s.Weight
}

func validate(samples []Sample) error {
	if len(samples) == 0 {
		return ErrNoSamples
	}
	for i, s := range samples {
		if s.Predicted < 0 || s.Predicted > 1 || math.IsNaN(s.Predicted) {
			return fmt.Errorf("%w: sample %d p=%v", ErrProbabilityRange, i, s.Predicted)
		}
	}
	return nil
}

// LogLoss is the mean binary cross-entropy, -(y·log p + (1-y)·log(1-p)).
// Unlike Brier it is unbounded, which is the point: it is the metric
// that makes confident wrongness expensive.
func LogLoss(samples []Sample) (float64, error) {
	if err := validate(samples); err != nil {
		return 0, err
	}
	var sum, wsum float64
	for _, s := range samples {
		p := math.Min(math.Max(s.Predicted, epsilon), 1-epsilon)
		w := s.weight()
		if s.Outcome {
			sum += -w * math.Log(p)
		} else {
			sum += -w * math.Log(1-p)
		}
		wsum += w
	}
	return sum / wsum, nil
}

// BrierScore is the mean squared error of the probability. It exists
// here so a caller can obtain the full metric set from one package;
// pkg/moat/calibration remains the ledger of record for per-claim
// Brier attribution and is not duplicated.
func BrierScore(samples []Sample) (float64, error) {
	if err := validate(samples); err != nil {
		return 0, err
	}
	var sum, wsum float64
	for _, s := range samples {
		y := 0.0
		if s.Outcome {
			y = 1
		}
		w := s.weight()
		d := s.Predicted - y
		sum += w * d * d
		wsum += w
	}
	return sum / wsum, nil
}

// Bin is one bucket of the reliability curve.
type Bin struct {
	Index          int     `json:"index"`
	LowerBound     float64 `json:"lower_bound"`
	UpperBound     float64 `json:"upper_bound"`
	Count          float64 `json:"count"`
	MeanPredicted  float64 `json:"mean_predicted"`
	ObservedRate   float64 `json:"observed_rate"`
	IntervalLow    float64 `json:"interval_low"`
	IntervalHigh   float64 `json:"interval_high"`
	AbsoluteGap    float64 `json:"absolute_gap"`
	SignedGap      float64 `json:"signed_gap"` // observed - predicted; negative = overconfident
	StatisticallyO bool    `json:"statistically_significant"`
}

// ReliabilityCurve is the binned decomposition plus the aggregate
// calibration errors derived from it.
type ReliabilityCurve struct {
	Bins       []Bin   `json:"bins"`
	ECE        float64 `json:"ece"`            // sample-weighted mean |gap|
	MCE        float64 `json:"mce"`            // worst populated bin gap
	Overconf   float64 `json:"overconfidence"` // weighted mean of max(0, predicted-observed)
	Underconf  float64 `json:"underconfidence"`
	SampleSize float64 `json:"sample_size"`
	BinCount   int     `json:"bin_count"`
	Hash       string  `json:"hash"`
}

// BuildReliabilityCurve computes equal-width bins over [0,1].
//
// Design note worth defending: equal-width (not equal-mass) bins are
// used because ECE is reported against a fixed, comparable partition
// across releases. Equal-mass bins move their own boundaries when the
// prediction distribution shifts, which makes two ECE numbers from two
// releases silently incomparable — the exact failure mode a drift
// monitor exists to catch.
func BuildReliabilityCurve(samples []Sample, bins int) (ReliabilityCurve, error) {
	if bins <= 0 {
		return ReliabilityCurve{}, ErrBinCount
	}
	if err := validate(samples); err != nil {
		return ReliabilityCurve{}, err
	}
	width := 1.0 / float64(bins)
	sumP := make([]float64, bins)
	sumY := make([]float64, bins)
	cnt := make([]float64, bins)

	for _, s := range samples {
		idx := int(s.Predicted / width)
		if idx >= bins {
			idx = bins - 1 // p == 1.0 belongs to the top bin
		}
		w := s.weight()
		cnt[idx] += w
		sumP[idx] += w * s.Predicted
		if s.Outcome {
			sumY[idx] += w
		}
	}

	c := ReliabilityCurve{BinCount: bins}
	var total, eceNum, over, under float64
	for i := 0; i < bins; i++ {
		b := Bin{
			Index:      i,
			LowerBound: float64(i) * width,
			UpperBound: float64(i+1) * width,
			Count:      cnt[i],
		}
		if cnt[i] > 0 {
			b.MeanPredicted = sumP[i] / cnt[i]
			b.ObservedRate = sumY[i] / cnt[i]
			b.SignedGap = b.ObservedRate - b.MeanPredicted
			b.AbsoluteGap = math.Abs(b.SignedGap)
			b.IntervalLow, b.IntervalHigh = wilson(sumY[i], cnt[i])
			// A bin is only "significant" if the predicted value falls
			// outside the observed frequency's own interval. Without
			// this, a 2-sample bin with a 0.5 gap dominates ECE and
			// triggers a false drift alarm.
			b.StatisticallyO = b.MeanPredicted < b.IntervalLow || b.MeanPredicted > b.IntervalHigh
			eceNum += cnt[i] * b.AbsoluteGap
			if b.AbsoluteGap > c.MCE {
				c.MCE = b.AbsoluteGap
			}
			if b.SignedGap < 0 {
				over += cnt[i] * (-b.SignedGap)
			} else {
				under += cnt[i] * b.SignedGap
			}
			total += cnt[i]
		}
		c.Bins = append(c.Bins, b)
	}
	c.SampleSize = total
	if total > 0 {
		c.ECE = eceNum / total
		c.Overconf = over / total
		c.Underconf = under / total
	}
	c.Hash = c.computeHash()
	return c, nil
}

func (c ReliabilityCurve) computeHash() string {
	var sb strings.Builder
	sb.WriteString("reliability_curve/v1\n")
	sb.WriteString("bins=" + strconv.Itoa(c.BinCount) + "\n")
	sb.WriteString("n=" + f(c.SampleSize) + "\n")
	for _, b := range c.Bins {
		sb.WriteString(strconv.Itoa(b.Index) + "|" + f(b.Count) + "|" + f(b.MeanPredicted) +
			"|" + f(b.ObservedRate) + "|" + f(b.IntervalLow) + "|" + f(b.IntervalHigh) + "\n")
	}
	sb.WriteString("ece=" + f(c.ECE) + "\nmce=" + f(c.MCE) + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// wilson returns the Wilson score interval at ~95% for k successes in
// n trials. Chosen over the normal approximation because the normal
// interval is degenerate (width 0) at k=0 and k=n, which is precisely
// where a sparse calibration bin sits.
func wilson(k, n float64) (float64, float64) {
	if n <= 0 {
		return 0, 1
	}
	const z = 1.959963984540054
	p := k / n
	den := 1 + z*z/n
	centre := (p + z*z/(2*n)) / den
	half := z * math.Sqrt((p*(1-p)+z*z/(4*n))/n) / den
	lo := centre - half
	hi := centre + half
	return math.Max(0, lo), math.Min(1, hi)
}

// ExpectedCalibrationError is the headline PHASE 46 metric.
func ExpectedCalibrationError(samples []Sample, bins int) (float64, error) {
	c, err := BuildReliabilityCurve(samples, bins)
	if err != nil {
		return 0, err
	}
	return c.ECE, nil
}

// ---------------------------------------------------------------------
// Calibration models
// ---------------------------------------------------------------------

// CalibratorKind names the mapping family.
type CalibratorKind string

const (
	KindIdentity CalibratorKind = "IDENTITY"
	KindPlatt    CalibratorKind = "PLATT"
	KindIsotonic CalibratorKind = "ISOTONIC"
)

// Calibrator is a fitted, versioned, content-addressed probability map.
// It is a value type on purpose: a calibration artifact that can be
// mutated after being committed to a decision hash is not an artifact.
type Calibrator struct {
	Kind        CalibratorKind `json:"kind"`
	Version     uint64         `json:"version"`
	FittedTick  uint64         `json:"fitted_tick"`
	SampleCount int            `json:"sample_count"`

	// Platt parameters: p' = sigmoid(A·logit(p) + B)
	A float64 `json:"a"`
	B float64 `json:"b"`

	// Isotonic step function, strictly ascending in X.
	X []float64 `json:"x"`
	Y []float64 `json:"y"`

	ArtifactID string `json:"artifact_id"`
}

// FitPlatt fits a one-dimensional logistic regression on the logit of
// the raw probability by Newton-Raphson (deterministic, no RNG, fixed
// iteration cap). Platt's own label smoothing is applied so a
// perfectly separable sample set does not drive |A| to infinity.
func FitPlatt(samples []Sample, tick uint64, version uint64) (Calibrator, error) {
	if err := validate(samples); err != nil {
		return Calibrator{}, err
	}
	var pos, neg float64
	for _, s := range samples {
		if s.Outcome {
			pos += s.weight()
		} else {
			neg += s.weight()
		}
	}
	hiTarget := (pos + 1) / (pos + 2)
	loTarget := 1 / (neg + 2)

	x := make([]float64, len(samples))
	t := make([]float64, len(samples))
	w := make([]float64, len(samples))
	for i, s := range samples {
		x[i] = logit(s.Predicted)
		if s.Outcome {
			t[i] = hiTarget
		} else {
			t[i] = loTarget
		}
		w[i] = s.weight()
	}

	// Damped Newton with backtracking (Lin, Weng & Lin 2007). Plain
	// Newton is not usable here: when every sample carries the same raw
	// probability the design matrix is rank-1, the Hessian determinant
	// is exactly zero in exact arithmetic, and an unguarded solve
	// amplifies floating-point cancellation into a divergent step. The
	// line search makes the fit safe on degenerate data instead of
	// silently returning a saturated calibrator.
	obj := func(a, b float64) float64 {
		var s float64
		for i := range x {
			z := a*x[i] + b
			p := sigmoid(z)
			p = math.Min(math.Max(p, epsilon), 1-epsilon)
			s += w[i] * (-t[i]*math.Log(p) - (1-t[i])*math.Log(1-p))
		}
		return s
	}

	a, b := 1.0, 0.0
	cur := obj(a, b)
	for iter := 0; iter < 200; iter++ {
		var g0, g1, h00, h01, h11 float64
		for i := range x {
			p := sigmoid(a*x[i] + b)
			d := p - t[i]
			v := math.Max(p*(1-p), 1e-12)
			g0 += w[i] * d * x[i]
			g1 += w[i] * d
			h00 += w[i] * v * x[i] * x[i]
			h01 += w[i] * v * x[i]
			h11 += w[i] * v
		}
		if math.Abs(g0) < 1e-10 && math.Abs(g1) < 1e-10 {
			break
		}
		// Scale-aware ridge: a fixed absolute epsilon is meaningless
		// when the Hessian entries are O(100).
		r := 1e-8 * math.Max(1, h00+h11)
		h00 += r
		h11 += r
		det := h00*h11 - h01*h01
		if det <= 0 || math.IsNaN(det) {
			break
		}
		da := (h11*g0 - h01*g1) / det
		db := (h00*g1 - h01*g0) / det

		step := 1.0
		moved := false
		for k := 0; k < 40; k++ {
			na, nb := a-step*da, b-step*db
			if next := obj(na, nb); next < cur-1e-15 {
				a, b, cur = na, nb, next
				moved = true
				break
			}
			step /= 2
		}
		if !moved {
			break
		}
	}
	c := Calibrator{Kind: KindPlatt, Version: version, FittedTick: tick,
		SampleCount: len(samples), A: a, B: b}
	c.ArtifactID = c.computeID()
	return c, nil
}

// FitIsotonic fits a monotone step function by Pool Adjacent Violators.
// Ties in the predicted value are pooled before the PAV pass so the
// result does not depend on the caller's input ordering — the classic
// silent non-determinism in isotonic implementations.
func FitIsotonic(samples []Sample, tick uint64, version uint64) (Calibrator, error) {
	if err := validate(samples); err != nil {
		return Calibrator{}, err
	}
	sorted := append([]Sample(nil), samples...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Predicted != sorted[j].Predicted {
			return sorted[i].Predicted < sorted[j].Predicted
		}
		return sorted[i].ClaimKey < sorted[j].ClaimKey
	})

	// Pool exact ties first.
	var xs, ys, ws []float64
	for i := 0; i < len(sorted); {
		j := i
		var sw, sy float64
		for j < len(sorted) && sorted[j].Predicted == sorted[i].Predicted {
			w := sorted[j].weight()
			sw += w
			if sorted[j].Outcome {
				sy += w
			}
			j++
		}
		xs = append(xs, sorted[i].Predicted)
		ys = append(ys, sy/sw)
		ws = append(ws, sw)
		i = j
	}

	// PAV.
	type block struct{ y, w, x float64 }
	var st []block
	for i := range xs {
		b := block{y: ys[i], w: ws[i], x: xs[i]}
		st = append(st, b)
		for len(st) >= 2 && st[len(st)-2].y > st[len(st)-1].y {
			b2 := st[len(st)-1]
			b1 := st[len(st)-2]
			merged := block{
				y: (b1.y*b1.w + b2.y*b2.w) / (b1.w + b2.w),
				w: b1.w + b2.w,
				x: b2.x, // block keyed by its upper bound
			}
			st = st[:len(st)-2]
			st = append(st, merged)
		}
	}
	c := Calibrator{Kind: KindIsotonic, Version: version, FittedTick: tick, SampleCount: len(samples)}
	for _, b := range st {
		c.X = append(c.X, b.x)
		c.Y = append(c.Y, b.y)
	}
	c.ArtifactID = c.computeID()
	return c, nil
}

// Identity returns the no-op calibrator, so a caller always has a
// non-nil, hashable calibration artifact to commit to — "uncalibrated"
// becomes an explicit, replayable state rather than an absent field.
func Identity(tick, version uint64) Calibrator {
	c := Calibrator{Kind: KindIdentity, Version: version, FittedTick: tick}
	c.ArtifactID = c.computeID()
	return c
}

// Apply maps a raw probability through the fitted calibrator.
func (c Calibrator) Apply(p float64) (float64, error) {
	if p < 0 || p > 1 || math.IsNaN(p) {
		return 0, ErrProbabilityRange
	}
	switch c.Kind {
	case KindIdentity:
		return p, nil
	case KindPlatt:
		return sigmoid(c.A*logit(p) + c.B), nil
	case KindIsotonic:
		if len(c.X) == 0 {
			return 0, ErrNotFitted
		}
		i := sort.SearchFloat64s(c.X, p)
		if i >= len(c.X) {
			i = len(c.X) - 1
		}
		return c.Y[i], nil
	default:
		return 0, ErrNotFitted
	}
}

func (c Calibrator) computeID() string {
	var sb strings.Builder
	sb.WriteString("calibrator/v1\nkind=" + string(c.Kind) + "\n")
	sb.WriteString("version=" + strconv.FormatUint(c.Version, 10) + "\n")
	sb.WriteString("tick=" + strconv.FormatUint(c.FittedTick, 10) + "\n")
	sb.WriteString("n=" + strconv.Itoa(c.SampleCount) + "\n")
	sb.WriteString("a=" + f(c.A) + "\nb=" + f(c.B) + "\n")
	for i := range c.X {
		sb.WriteString("s" + strconv.Itoa(i) + "=" + f(c.X[i]) + ":" + f(c.Y[i]) + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}

func logit(p float64) float64 {
	p = math.Min(math.Max(p, epsilon), 1-epsilon)
	return math.Log(p / (1 - p))
}

// ---------------------------------------------------------------------
// Drift
// ---------------------------------------------------------------------

// DriftKind names what is being monitored. Every kind uses the same
// algorithms; the kind exists so an alert says which surface moved.
type DriftKind string

const (
	DriftFeature           DriftKind = "FEATURE"
	DriftEvidenceDistrib   DriftKind = "EVIDENCE_DISTRIBUTION"
	DriftSourceReliability DriftKind = "SOURCE_RELIABILITY"
	DriftPrediction        DriftKind = "PREDICTION"
	DriftCalibration       DriftKind = "CALIBRATION"
)

// Distribution is a named, bucketed empirical distribution. Buckets
// are held as a sorted slice, never a map, because a hash over map
// iteration order is not a hash.
type Distribution struct {
	Buckets []string  `json:"buckets"`
	Mass    []float64 `json:"mass"`
}

// NewDistribution builds a normalised Distribution from counts.
func NewDistribution(counts map[string]float64) (Distribution, error) {
	if len(counts) == 0 {
		return Distribution{}, ErrNoSamples
	}
	keys := make([]string, 0, len(counts))
	var total float64
	for k, v := range counts {
		if v < 0 {
			return Distribution{}, fmt.Errorf("reliability: negative count for bucket %q", k)
		}
		keys = append(keys, k)
		total += v
	}
	if total <= 0 {
		return Distribution{}, ErrNoSamples
	}
	sort.Strings(keys)
	d := Distribution{Buckets: keys, Mass: make([]float64, len(keys))}
	for i, k := range keys {
		d.Mass[i] = counts[k] / total
	}
	return d, nil
}

func align(ref, cur Distribution) error {
	if len(ref.Buckets) != len(cur.Buckets) {
		return ErrDistributionShape
	}
	for i := range ref.Buckets {
		if ref.Buckets[i] != cur.Buckets[i] {
			return ErrDistributionShape
		}
	}
	return nil
}

// smoothing keeps PSI and KL finite when a bucket empties completely.
// A vanished bucket is a strong drift signal, not a divide-by-zero.
const smoothing = 1e-6

// PSI is the Population Stability Index, Σ (c−r)·ln(c/r).
func PSI(ref, cur Distribution) (float64, error) {
	if err := align(ref, cur); err != nil {
		return 0, err
	}
	var psi float64
	for i := range ref.Mass {
		r := math.Max(ref.Mass[i], smoothing)
		c := math.Max(cur.Mass[i], smoothing)
		psi += (c - r) * math.Log(c/r)
	}
	return psi, nil
}

// KLDivergence is D(cur ‖ ref) in nats — asymmetric by definition.
func KLDivergence(cur, ref Distribution) (float64, error) {
	if err := align(ref, cur); err != nil {
		return 0, err
	}
	var kl float64
	for i := range ref.Mass {
		c := math.Max(cur.Mass[i], smoothing)
		r := math.Max(ref.Mass[i], smoothing)
		kl += c * math.Log(c/r)
	}
	return kl, nil
}

// JSDivergence is the symmetric, bounded ([0, ln2]) Jensen-Shannon
// divergence. It is the one to alert on: KL can be unbounded and PSI
// has no natural scale across differently-shaped supports.
func JSDivergence(a, b Distribution) (float64, error) {
	if err := align(a, b); err != nil {
		return 0, err
	}
	m := Distribution{Buckets: a.Buckets, Mass: make([]float64, len(a.Mass))}
	for i := range a.Mass {
		m.Mass[i] = 0.5 * (a.Mass[i] + b.Mass[i])
	}
	ka, err := KLDivergence(a, m)
	if err != nil {
		return 0, err
	}
	kb, err := KLDivergence(b, m)
	if err != nil {
		return 0, err
	}
	return 0.5 * (ka + kb), nil
}

// KSStatistic is the two-sample Kolmogorov-Smirnov D for continuous
// samples, plus its asymptotic p-value. Used where a value is genuinely
// continuous (latency, confidence) and bucketing would destroy signal.
func KSStatistic(ref, cur []float64) (d float64, pValue float64, err error) {
	if len(ref) == 0 || len(cur) == 0 {
		return 0, 0, ErrNoSamples
	}
	a := append([]float64(nil), ref...)
	b := append([]float64(nil), cur...)
	sort.Float64s(a)
	sort.Float64s(b)
	i, j := 0, 0
	na, nb := float64(len(a)), float64(len(b))
	for i < len(a) && j < len(b) {
		var x float64
		if a[i] <= b[j] {
			x = a[i]
		} else {
			x = b[j]
		}
		for i < len(a) && a[i] <= x {
			i++
		}
		for j < len(b) && b[j] <= x {
			j++
		}
		if gap := math.Abs(float64(i)/na - float64(j)/nb); gap > d {
			d = gap
		}
	}
	en := math.Sqrt(na * nb / (na + nb))
	pValue = ksProb((en + 0.12 + 0.11/en) * d)
	return d, pValue, nil
}

// ksProb is the Kolmogorov distribution Q(λ) = 2·Σ (−1)^{k−1} e^{−2k²λ²}.
func ksProb(lambda float64) float64 {
	if lambda <= 0 {
		return 1
	}
	const eps1, eps2 = 1e-6, 1e-10
	fac, sum, termbf := 2.0, 0.0, 0.0
	a2 := -2 * lambda * lambda
	for k := 1; k <= 100; k++ {
		term := fac * math.Exp(a2*float64(k)*float64(k))
		sum += term
		if math.Abs(term) <= eps1*termbf || math.Abs(term) <= eps2*sum {
			return math.Min(1, math.Max(0, sum))
		}
		fac = -fac
		termbf = math.Abs(term)
	}
	return 1
}

// DriftReport is the content-addressed result of one drift evaluation.
type DriftReport struct {
	Kind        DriftKind `json:"kind"`
	Subject     string    `json:"subject"`
	Tick        uint64    `json:"tick"`
	PSI         float64   `json:"psi"`
	KL          float64   `json:"kl"`
	JS          float64   `json:"js"`
	KSStat      float64   `json:"ks_statistic"`
	KSPValue    float64   `json:"ks_p_value"`
	HasKS       bool      `json:"has_ks"`
	Drifted     bool      `json:"drifted"`
	Reasons     []string  `json:"reasons"`
	ReferenceID string    `json:"reference_id"`
	Hash        string    `json:"hash"`
}

// Thresholds are the alerting bands. Defaults follow the conventional
// industry reading of PSI (<0.1 stable, 0.1–0.25 moderate, >0.25 shift)
// and are configurable because "conventional" is not "correct for your
// data".
type Thresholds struct {
	PSI        float64
	JS         float64
	KSPValue   float64
	ECE        float64
	MaxLogLoss float64
}

// DefaultThresholds returns the documented defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{PSI: 0.25, JS: 0.10, KSPValue: 0.01, ECE: 0.10, MaxLogLoss: 0.70}
}

// DetectDrift compares a frozen reference against a current window.
// continuousRef/continuousCur are optional; when both are non-empty the
// KS test is run in addition to the bucketed measures.
func DetectDrift(kind DriftKind, subject string, referenceID string, ref, cur Distribution,
	continuousRef, continuousCur []float64, th Thresholds, tick uint64) (DriftReport, error) {

	r := DriftReport{Kind: kind, Subject: subject, Tick: tick, ReferenceID: referenceID}
	var err error
	if r.PSI, err = PSI(ref, cur); err != nil {
		return DriftReport{}, err
	}
	if r.KL, err = KLDivergence(cur, ref); err != nil {
		return DriftReport{}, err
	}
	if r.JS, err = JSDivergence(ref, cur); err != nil {
		return DriftReport{}, err
	}
	if len(continuousRef) > 0 && len(continuousCur) > 0 {
		if r.KSStat, r.KSPValue, err = KSStatistic(continuousRef, continuousCur); err != nil {
			return DriftReport{}, err
		}
		r.HasKS = true
	}
	if r.PSI > th.PSI {
		r.Drifted = true
		r.Reasons = append(r.Reasons, "psi="+f(r.PSI)+" exceeds "+f(th.PSI))
	}
	if r.JS > th.JS {
		r.Drifted = true
		r.Reasons = append(r.Reasons, "js="+f(r.JS)+" exceeds "+f(th.JS))
	}
	if r.HasKS && r.KSPValue < th.KSPValue {
		r.Drifted = true
		r.Reasons = append(r.Reasons, "ks p="+f(r.KSPValue)+" below "+f(th.KSPValue))
	}
	sort.Strings(r.Reasons)
	r.Hash = r.computeHash()
	return r, nil
}

func (r DriftReport) computeHash() string {
	var sb strings.Builder
	sb.WriteString("drift/v1\nkind=" + string(r.Kind) + "\nsubject=" + r.Subject + "\n")
	sb.WriteString("ref=" + r.ReferenceID + "\ntick=" + strconv.FormatUint(r.Tick, 10) + "\n")
	sb.WriteString("psi=" + f(r.PSI) + "\nkl=" + f(r.KL) + "\njs=" + f(r.JS) + "\n")
	sb.WriteString("ks=" + f(r.KSStat) + "\nksp=" + f(r.KSPValue) + "\n")
	sb.WriteString("drifted=" + strconv.FormatBool(r.Drifted) + "\n")
	for _, x := range r.Reasons {
		sb.WriteString("reason=" + x + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------
// Assessment — the health verdict
// ---------------------------------------------------------------------

// Health is the model's calibration verdict.
type Health string

const (
	HealthCalibrated   Health = "CALIBRATED"
	HealthDegraded     Health = "DEGRADED"
	HealthInsufficient Health = "INSUFFICIENT_DATA"
)

// Assessment is the full, content-addressed reliability verdict for one
// model version at one tick. It is what the CALIBRATION_GATE consumes.
type Assessment struct {
	ModelID       string           `json:"model_id"`
	ModelVersion  string           `json:"model_version"`
	Tick          uint64           `json:"tick"`
	SampleCount   int              `json:"sample_count"`
	LogLoss       float64          `json:"log_loss"`
	Brier         float64          `json:"brier"`
	Curve         ReliabilityCurve `json:"curve"`
	CalibratorID  string           `json:"calibrator_id"`
	Drift         []DriftReport    `json:"drift"`
	Health        Health           `json:"health"`
	Findings      []string         `json:"findings"`
	MinimumSample int              `json:"minimum_sample"`
	Hash          string           `json:"hash"`
}

// Err converts a degraded verdict into an error so a caller cannot
// ignore it by reading only the numbers. PHASE 46: "Jika ECE >
// threshold maka model tidak boleh otomatis dianggap healthy."
func (a Assessment) Err() error {
	if a.Health == HealthDegraded {
		return fmt.Errorf("%w: %s", ErrDegraded, strings.Join(a.Findings, "; "))
	}
	return nil
}

// Assess computes every metric and returns the verdict.
//
// minimumSample exists because declaring a model CALIBRATED on nine
// observations is worse than declaring nothing: it is a false green,
// which is the failure mode the whole assurance plane was built to
// prevent. Below the minimum the verdict is INSUFFICIENT_DATA, which is
// neither healthy nor degraded.
func Assess(modelID, modelVersion string, samples []Sample, bins int, calibratorID string,
	drift []DriftReport, th Thresholds, minimumSample int, tick uint64) (Assessment, error) {

	a := Assessment{ModelID: modelID, ModelVersion: modelVersion, Tick: tick,
		SampleCount: len(samples), CalibratorID: calibratorID, MinimumSample: minimumSample}

	if len(samples) == 0 {
		return Assessment{}, ErrNoSamples
	}
	var err error
	if a.LogLoss, err = LogLoss(samples); err != nil {
		return Assessment{}, err
	}
	if a.Brier, err = BrierScore(samples); err != nil {
		return Assessment{}, err
	}
	if a.Curve, err = BuildReliabilityCurve(samples, bins); err != nil {
		return Assessment{}, err
	}
	a.Drift = append(a.Drift, drift...)
	sort.SliceStable(a.Drift, func(i, j int) bool { return a.Drift[i].Hash < a.Drift[j].Hash })

	if len(samples) < minimumSample {
		a.Health = HealthInsufficient
		a.Findings = append(a.Findings,
			"only "+strconv.Itoa(len(samples))+" samples; minimum is "+strconv.Itoa(minimumSample))
		a.Hash = a.computeHash()
		return a, nil
	}
	if a.Curve.ECE > th.ECE {
		a.Findings = append(a.Findings, "ece="+f(a.Curve.ECE)+" exceeds "+f(th.ECE))
	}
	if a.LogLoss > th.MaxLogLoss {
		a.Findings = append(a.Findings, "log_loss="+f(a.LogLoss)+" exceeds "+f(th.MaxLogLoss))
	}
	for _, d := range a.Drift {
		if d.Drifted {
			a.Findings = append(a.Findings, "drift "+string(d.Kind)+" on "+d.Subject)
		}
	}
	sort.Strings(a.Findings)
	if len(a.Findings) > 0 {
		a.Health = HealthDegraded
	} else {
		a.Health = HealthCalibrated
	}
	a.Hash = a.computeHash()
	return a, nil
}

func (a Assessment) computeHash() string {
	var sb strings.Builder
	sb.WriteString("reliability_assessment/v1\n")
	sb.WriteString("model=" + a.ModelID + "@" + a.ModelVersion + "\n")
	sb.WriteString("tick=" + strconv.FormatUint(a.Tick, 10) + "\n")
	sb.WriteString("n=" + strconv.Itoa(a.SampleCount) + "\n")
	sb.WriteString("logloss=" + f(a.LogLoss) + "\nbrier=" + f(a.Brier) + "\n")
	sb.WriteString("curve=" + a.Curve.Hash + "\ncalibrator=" + a.CalibratorID + "\n")
	for _, d := range a.Drift {
		sb.WriteString("drift=" + d.Hash + "\n")
	}
	sb.WriteString("health=" + string(a.Health) + "\n")
	for _, x := range a.Findings {
		sb.WriteString("finding=" + x + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// f formats a float deterministically for hashing. 'g' with 17
// significant digits round-trips an IEEE-754 double exactly, so two
// runs on two machines hash identically.
func f(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }
