// Package evidence defines VERIQO's canonical typed output contract for
// the Intelligence Layer (pkg/moat/intelligence/{patterns,pricing,risk}
// and pkg/moat/domain/maritime's ownership graph) — closing three P0/P1
// findings from "Hasil audit Professional Technical Auditor (V7.9)":
//
//   - P0 "Canonical Intelligence Evidence": every intelligence output
//     becomes typed IntelligenceEvidence (ID, Subject, Type, Value,
//     Score, Provenance, SourceIDs, Dependencies, ModelVersion,
//     PolicyVersion, Tick) instead of a bare float64 with no attached
//     context.
//   - P1 "Score != Probability": RiskScore (a deterministic, documented
//     heuristic formula's output) is a DISTINCT field from
//     CalibratedProbability (only populated once real ground truth has
//     been backtested through pkg/moat/calibration) — so a consumer can
//     never mistake "0.82" for "82% probability of sanctions evasion"
//     when no calibration dataset backs that claim.
//   - P1 "Data quality state": every evidence item carries an explicit
//     DataQuality (VALID/INSUFFICIENT/DEGRADED/INVALID) rather than
//     silently producing a confident-looking score from bad input (a
//     constant reference series, a single data point).
//
// This package also centralizes the P1 "Numeric input validation" the
// audit asks for (reject NaN/+Inf/-Inf/invalid negatives) as one shared
// validator, so patterns/pricing/risk/ownership call ONE reviewed
// implementation instead of five independently-written (and
// independently-buggy) checks.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
)

var (
	ErrNonFiniteInput   = errors.New("evidence: numeric input is NaN or +/-Inf")
	ErrNegativeInput    = errors.New("evidence: numeric input must be non-negative")
	ErrInvalidRatio     = errors.New("evidence: ratio input must be in [0,1]")
	ErrInvalidThreshold = errors.New("evidence: threshold must be finite and non-negative")
)

// ValidateFinite rejects NaN and +/-Inf — the exact gap the audit names
// against pricing's raw sort.Float64s()/division-by-MAD path accepting
// unchecked input.
func ValidateFinite(v float64, label string) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%w: %s = %v", ErrNonFiniteInput, label, v)
	}
	return nil
}

// ValidateNonNegative rejects negative values (e.g. a negative
// observed price, which is economically meaningless for the commodities
// this kernel models) after confirming finiteness.
func ValidateNonNegative(v float64, label string) error {
	if err := ValidateFinite(v, label); err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("%w: %s = %v", ErrNegativeInput, label, v)
	}
	return nil
}

// ValidateRatio rejects anything outside [0,1] after confirming
// finiteness — for ratios like independence, confidence, or a
// normalized score.
func ValidateRatio(v float64, label string) error {
	if err := ValidateFinite(v, label); err != nil {
		return err
	}
	if v < 0 || v > 1 {
		return fmt.Errorf("%w: %s = %v", ErrInvalidRatio, label, v)
	}
	return nil
}

// ValidateThreshold rejects a non-finite or negative configuration
// threshold (e.g. a detector's z-score cutoff) — configuration-time
// validation, distinct from per-observation input validation, per the
// audit's explicit ErrInvalidDetectorConfiguration ask.
func ValidateThreshold(v float64, label string) error {
	if err := ValidateFinite(v, label); err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("%w: %s = %v", ErrInvalidThreshold, label, v)
	}
	return nil
}

// ValidateSeries checks every element of a reference series for
// NaN/Inf, returning the first offending index found (deterministic,
// stable ordering) rather than silently sorting through invalid data.
func ValidateSeries(series []float64, label string) error {
	for i, v := range series {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: %s[%d] = %v", ErrNonFiniteInput, label, i, v)
		}
	}
	return nil
}

// DataQuality is an explicit, typed statement about how much an
// evidence item's score should be trusted based on the INPUT it was
// computed from — orthogonal to the score itself. A constant reference
// series producing a mathematically-valid +Inf z-score is a data
// problem, not a maximum-confidence finding; DataQuality is how that
// distinction survives into the typed evidence record instead of being
// silently lost.
type DataQuality string

const (
	// DataQualityValid: input passed all structural and distributional
	// sanity checks.
	DataQualityValid DataQuality = "VALID"
	// DataQualityInsufficient: too little data to compute a meaningful
	// statistic (e.g. a reference series with < 3 points).
	DataQualityInsufficient DataQuality = "INSUFFICIENT"
	// DataQualityDegraded: input is structurally valid but exhibits a
	// known-suspicious pattern (e.g. a constant/near-constant reference
	// series) that inflates apparent confidence without genuine
	// distributional information.
	DataQualityDegraded DataQuality = "DEGRADED"
	// DataQualityInvalid: input failed a hard validation check (NaN,
	// Inf, negative price) and no score should be trusted.
	DataQualityInvalid DataQuality = "INVALID"
)

// ScoreKind distinguishes what family of number Value/Score represents,
// so a canonical projection into a downstream consumer (e.g.
// decision.Engine) never has to guess.
type ScoreKind string

const (
	// KindRiskScore: a deterministic, documented HEURISTIC combination
	// — reproducible, explainable, NOT a calibrated probability unless
	// CalibratedProbability is also populated.
	KindRiskScore ScoreKind = "RISK_SCORE"
	// KindCalibratedProbability: a genuine probability estimate backed
	// by pkg/moat/calibration ground-truth backtesting. Absent
	// (CalibratedProbability == nil) until real outcomes exist.
	KindCalibratedProbability ScoreKind = "CALIBRATED_PROBABILITY"
)

// IntelligenceEvidence is the canonical typed output every Intelligence
// Layer detector/model produces — the P0 fix replacing a bare float64.
type IntelligenceEvidence struct {
	// ID is content-addressed (SHA-256 of the canonical field encoding)
	// so identical evidence computed twice collapses to the same ID —
	// same discipline as pkg/evidence/graph's NodeID.
	ID string
	// Subject is what this evidence is about (a vessel/voyage ID, an
	// entity ID) — required, non-empty.
	Subject string
	// Type names which detector/model produced this (e.g.
	// "pattern:P001_DARK_ACTIVITY", "pricing:anomaly",
	// "risk:tbml_composite").
	Type string
	// Value is a human-readable summary of what was found (e.g. "HIT",
	// "OVER", "CRITICAL") — the categorical face of Score.
	Value string
	// RiskScore is the deterministic heuristic score in [0,1]. ALWAYS
	// populated. Documented explicitly as NOT a probability (P1: Score
	// != Probability) — see CalibratedProbability.
	RiskScore float64
	// CalibratedProbability is populated ONLY once
	// pkg/moat/calibration.Summarize has processed real ground truth
	// for this evidence Type/Subject; nil otherwise. Consumers MUST NOT
	// treat RiskScore as a probability when this is nil.
	CalibratedProbability *float64
	// CalibrationStatus explains why CalibratedProbability is or isn't
	// populated.
	CalibrationStatus string // "UNCALIBRATED" | "CALIBRATED" | "STALE"
	// CalibrationVersion identifies which calibration ledger state
	// produced CalibratedProbability, for reproducibility — empty when
	// CalibrationStatus is UNCALIBRATED.
	CalibrationVersion string
	// DataQuality states how much this evidence's input should be
	// trusted (P1).
	DataQuality DataQuality
	// Provenance is the computed provenance.IndependenceResult.Ratio
	// for SourceIDs (P0: provenance computed, not given) — 1.0 when
	// SourceIDs has 0 or 1 entries (trivially independent).
	Provenance float64
	// SourceIDs is every evidence source this item was computed from —
	// the input to provenance computation and to calibration credit
	// assignment.
	SourceIDs []string
	// Dependencies names other IntelligenceEvidence IDs this item was
	// derived from (e.g. a risk.Result depends on a patterns.Match set
	// and a pricing.AnomalyResult) — the P0 dependency graph the audit
	// asks for, expressed as explicit typed links rather than being
	// reconstructable only from source code.
	Dependencies []string
	// ModelVersion names the detector/model version that produced this
	// (e.g. "patterns_v1", "risk_tbml_v1").
	ModelVersion string
	// PolicyVersion names the decision.Policy this evidence is meant to
	// be consumed by, when applicable; empty if not policy-bound.
	PolicyVersion string
	Tick          uint64
}

// New constructs an IntelligenceEvidence with a content-addressed ID
// and DataQuality/CalibrationStatus defaults. Callers set fields via
// the returned value (Go structs, not a builder) — New's job is just
// the ID derivation and safe defaults so no caller can forget to set
// CalibrationStatus and accidentally imply a false calibration.
func New(subject, evType, value string, riskScore float64, tick uint64) IntelligenceEvidence {
	e := IntelligenceEvidence{
		Subject:           subject,
		Type:              evType,
		Value:             value,
		RiskScore:         clamp01(riskScore),
		CalibrationStatus: "UNCALIBRATED",
		DataQuality:       DataQualityValid,
		Provenance:        1.0,
		Tick:              tick,
	}
	e.ID = e.computeID()
	return e
}

// WithCalibration attaches a calibrated probability from
// calibration.Engine ground truth, returning a copy with a fresh ID
// (calibration state is part of what the evidence asserts, so the
// content address must change when it does).
func (e IntelligenceEvidence) WithCalibration(prob float64, version string) IntelligenceEvidence {
	p := clamp01(prob)
	e.CalibratedProbability = &p
	e.CalibrationStatus = "CALIBRATED"
	e.CalibrationVersion = version
	e.ID = e.computeID()
	return e
}

// WithProvenance attaches a computed (not asserted) independence ratio
// and the SourceIDs it was computed from, returning a copy with a
// fresh ID.
func (e IntelligenceEvidence) WithProvenance(ratio float64, sourceIDs []string) IntelligenceEvidence {
	e.Provenance = clamp01(ratio)
	e.SourceIDs = append([]string{}, sourceIDs...)
	e.ID = e.computeID()
	return e
}

// WithDataQuality returns a copy with DataQuality set, ID recomputed.
func (e IntelligenceEvidence) WithDataQuality(q DataQuality) IntelligenceEvidence {
	e.DataQuality = q
	e.ID = e.computeID()
	return e
}

func (e IntelligenceEvidence) computeID() string {
	cal := "nil"
	if e.CalibratedProbability != nil {
		cal = fmt.Sprintf("%.10f", *e.CalibratedProbability)
	}
	raw := fmt.Sprintf(
		"subject=%s|type=%s|value=%s|risk=%.10f|cal=%s|calstatus=%s|calver=%s|quality=%s|prov=%.10f|sources=%v|deps=%v|model=%s|policy=%s|tick=%d",
		e.Subject, e.Type, e.Value, e.RiskScore, cal, e.CalibrationStatus, e.CalibrationVersion,
		e.DataQuality, e.Provenance, e.SourceIDs, e.Dependencies, e.ModelVersion, e.PolicyVersion, e.Tick)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
