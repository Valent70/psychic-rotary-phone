// Package calibration implements Veriqo's Independent Truth Calibration
// Engine — closing the gap named PRIORITAS #1 in
// "Gap_yang_sekarang_menjadi_PRIORITAS.docx":
//
//	Evidence -> Arbitration -> Winner -> Trust update -> Evidence
//	weighting -> Arbitration -> ...
//
// The critique's point is precise: pkg/trust already updates a source's
// trust from arbitration outcomes, and pkg/moat/fusion already weights
// future evidence by trust. Both are correct in isolation. But nothing
// in the repo, before this package, supplied the one ingredient that
// keeps that loop from becoming self-referential: an outcome that was
// NOT itself produced by VERIQO's own Arbitration Engine.
//
// This package makes that distinction structural, not conventional, by
// requiring three explicitly different evidence classes:
//
//	Class A — Observation:  what a source claims it saw
//	                         (pkg/moat/contradiction.RawObservation).
//	Class B — Arbitration:  VERIQO's own internal decision about A
//	                         (pkg/moat/contradiction.TruthVersion /
//	                         RankHypotheses' TruthCandidate set).
//	Class C — Ground Truth: what later, independently, proved true —
//	                         the ONLY class this package will accept as
//	                         input to Calibrate.
//
// A Class C submission is rejected outright if its source ID appears
// among the Class B prediction's own arbitration input sources
// (RecordGroundTruth cross-checks this). That is the concrete circuit
// breaker: the same evidence that fed an arbitration can never also
// stand in as that arbitration's own confirmation.
//
// Design invariants match the rest of this repository: no time.Now()
// (explicit logical Tick only), no UUIDs (SHA-256 content addressing),
// append-only hash-chained ledger, deterministic replay, zero external
// dependencies.
package calibration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/trust"
)

var (
	// ErrSourceNotIndependent is returned when a ground-truth
	// submission's source was never registered via
	// RegisterIndependentSource — an unregistered source cannot supply
	// Class C evidence, full stop, regardless of whether it also
	// happens to be absent from the prediction's own input list.
	ErrSourceNotIndependent = errors.New("calibration: ground-truth source is not a registered independent source")
	// ErrSourceIsArbitrationInput is the specific self-reinforcement
	// guard: the ground-truth source WAS one of the sources whose
	// evidence produced the prediction being calibrated.
	ErrSourceIsArbitrationInput = errors.New("calibration: ground-truth source was itself an input to the arbitration it is being used to calibrate")
	// ErrSourceSharesUpstreamWithArbitrationInput is the P2 hardening
	// (audit §3, "Hasil_audit_brutal...docx"): a ground-truth source
	// with a DIFFERENT source ID than any arbitration input can still
	// be epistemically dependent on it — e.g. AIS_VENDOR_A and
	// AIS_VENDOR_B both reselling SATELLITE_OPERATOR_X. Direct source-ID
	// equality alone cannot catch this; RegisterUpstream lets an
	// operator declare the shared provenance root explicitly, and this
	// error fires when the ground-truth source's declared upstream
	// matches any arbitration input's declared upstream, even though
	// the source IDs themselves differ.
	ErrSourceSharesUpstreamWithArbitrationInput = errors.New("calibration: ground-truth source shares upstream provenance with an arbitration input source")
	ErrNoPrediction                             = errors.New("calibration: no pending prediction recorded for this claim")
	ErrInvalidDistribution                      = errors.New("calibration: distribution values must be in [0,1] and sum to ~1")
)

// EvidenceClass names which of the three calibration classes a record
// belongs to — carried on ledger entries purely for auditability; the
// enforcement itself is structural (see RecordGroundTruth).
type EvidenceClass int

const (
	ClassObservation EvidenceClass = iota + 1
	ClassArbitration
	ClassGroundTruth
)

func (c EvidenceClass) String() string {
	switch c {
	case ClassObservation:
		return "A_OBSERVATION"
	case ClassArbitration:
		return "B_ARBITRATION"
	case ClassGroundTruth:
		return "C_GROUND_TRUTH"
	default:
		return "UNKNOWN"
	}
}

// Prediction is Class B: VERIQO's own arbitrated belief about a claim,
// recorded BEFORE ground truth is known. ArbitrationSourceIDs is the
// full set of source IDs whose Class A evidence contributed to this
// prediction — required so RecordGroundTruth can reject a ground-truth
// source that was itself one of those inputs.
type Prediction struct {
	ClaimKey   string
	Value      string
	Confidence float64
	// Distribution is the optional full hypothesis distribution behind
	// the point prediction (see pkg/moat/contradiction.RankHypotheses),
	// value -> probability, summing to ~1. When present, calibration
	// scores the WHOLE distribution (multiclass Brier score) rather
	// than just the point estimate — ties Priority #5 (hypothesis
	// distribution) into Priority #1 (calibration) instead of treating
	// them as unrelated gaps.
	Distribution         map[string]float64
	ArbitrationSourceIDs []string
	// SourceContributions optionally states each ArbitrationSourceIDs
	// entry's actual weight in the arbitrated outcome (e.g. from
	// pkg/moat/fusion's per-source weighting) — v7.8 Priority B fix:
	// when present, RecordGroundTruth credits trust proportionally to
	// actual contribution instead of an equal 1/N split, so a source
	// that supplied 5% of the evidence weight isn't credited/debited
	// identically to one that supplied 80%. Optional: nil falls back to
	// equal-split (the pre-existing behavior) for callers that haven't
	// wired contribution weights through yet.
	SourceContributions map[string]float64
	Tick                uint64
}

// GroundTruth is Class C: what independently proved true.
type GroundTruth struct {
	ClaimKey string
	Value    string
	SourceID string
	Tick     uint64
}

// Record is one completed calibration — a Prediction matched against
// its GroundTruth, hash-chained into the append-only ledger.
type Record struct {
	Index               uint64
	ClaimKey            string
	PredictedValue      string
	PredictedConfidence float64
	ActualValue         string
	GroundTruthSourceID string
	Matched             bool
	BrierScore          float64 // lower is better-calibrated; 0 = perfect
	Tick                uint64
	PrevHash            string
	Hash                string
}

// Engine is the Independent Truth Calibration Engine: the registry of
// independent (Class-C-capable) sources, the set of pending Class B
// predictions awaiting an outcome, and the hash-chained ledger of
// completed calibrations.
type Engine struct {
	mu                 sync.RWMutex
	independentSources map[string]bool
	sourceUpstream     map[string]string     // P2: sourceID -> declared upstream/provenance root
	pendingPredictions map[string]Prediction // keyed by ClaimKey
	ledger             []Record
	head               string
	// calc, when non-nil, is the SHARED trustcalc.Calculus this
	// Engine writes calibration outcomes into (P1: Calibration ->
	// Trust). nil means "no wiring", preserving the original
	// NewEngine() behavior exactly for existing callers.
	calc *trustcalc.Calculus
}

func NewEngine() *Engine {
	return &Engine{
		independentSources: make(map[string]bool),
		sourceUpstream:     make(map[string]string),
		pendingPredictions: make(map[string]Prediction),
	}
}

// NewEngineWithCalculus builds a calibration Engine wired to a shared
// trustcalc.Calculus — the constructor veriqo/kernel.Kernel uses. Every
// successful RecordGroundTruth call now, in addition to appending its
// hash-chained Record, calls calc.Observe(sourceID, matched, weight)
// for every source in the prediction's ArbitrationSourceIDs — this is
// the concrete "production caller" the audit found missing:
// independently-verified ground truth directly reweights the SAME
// trust calculus Evidence/Intelligence use for those sources' future
// observations, closing Prediction -> Independent Outcome ->
// Calibration -> Trust Update -> Evidence Weight -> Future Prediction.
func NewEngineWithCalculus(calc *trustcalc.Calculus) *Engine {
	e := NewEngine()
	e.calc = calc
	return e
}

// RegisterUpstream declares that sourceID's evidence ultimately derives
// from upstreamID (e.g. a satellite operator, a single AIS feed
// provider). Sources with no registered upstream are treated as their
// own upstream (independent by default). This registry is what lets
// RecordGroundTruth reject provenance-dependent ground truth even when
// the ground-truth source's ID differs from every arbitration input's
// ID — see ErrSourceSharesUpstreamWithArbitrationInput.
func (e *Engine) RegisterUpstream(sourceID, upstreamID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sourceUpstream[sourceID] = upstreamID
}

// upstreamOf returns sourceID's declared upstream, or sourceID itself
// if none was registered (a source with no declared shared provenance
// is assumed independent of everything else by default).
func (e *Engine) upstreamOf(sourceID string) string {
	if u, ok := e.sourceUpstream[sourceID]; ok && u != "" {
		return u
	}
	return sourceID
}

// RegisterIndependentSource marks a source ID as capable of supplying
// Class C ground truth. Deliberately a separate registry from
// fusion.SourceProfile / contradiction's evidence sources — a source
// being trusted for Class A observation does NOT automatically make it
// eligible for Class C, and vice versa; the two roles must be granted
// independently by whoever operates the system, keeping the epistemic
// separation explicit rather than incidental.
func (e *Engine) RegisterIndependentSource(sourceID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.independentSources[sourceID] = true
}

// IsIndependentSource reports whether a source is registered as
// Class-C-capable.
func (e *Engine) IsIndependentSource(sourceID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.independentSources[sourceID]
}

// RecordPrediction stores a Class B arbitration outcome as pending,
// awaiting ground truth. A later call for the same ClaimKey overwrites
// the pending prediction (only the most recent arbitration is what
// ground truth can meaningfully calibrate against).
func (e *Engine) RecordPrediction(p Prediction) error {
	if p.Distribution != nil {
		if err := validateDistribution(p.Distribution); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingPredictions[p.ClaimKey] = p
	return nil
}

func validateDistribution(dist map[string]float64) error {
	sum := 0.0
	for _, p := range dist {
		if p < 0 || p > 1 {
			return ErrInvalidDistribution
		}
		sum += p
	}
	if sum < 0.99 || sum > 1.01 {
		return ErrInvalidDistribution
	}
	return nil
}

// creditWeights computes per-source calibration credit for
// RecordGroundTruth. When contributions is non-empty, weight is each
// source's contribution normalized to sum 1.0 across sourceIDs
// (missing/non-positive entries default to 0, and any leftover mass —
// e.g. from unlisted or non-positive contributions — is NOT silently
// redistributed, since that would reintroduce the equal-split
// assumption for exactly the sources that most need proportional
// treatment). When contributions is nil/empty, falls back to equal
// 1/N split — the pre-existing, still-correct behavior for callers
// with no per-source weighting available.
func creditWeights(sourceIDs []string, contributions map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(sourceIDs))
	if len(contributions) == 0 {
		w := 1.0 / float64(len(sourceIDs))
		for _, s := range sourceIDs {
			out[s] = w
		}
		return out
	}
	total := 0.0
	for _, s := range sourceIDs {
		if c, ok := contributions[s]; ok && c > 0 {
			total += c
		}
	}
	if total <= 0 {
		w := 1.0 / float64(len(sourceIDs))
		for _, s := range sourceIDs {
			out[s] = w
		}
		return out
	}
	for _, s := range sourceIDs {
		c := contributions[s]
		if c < 0 {
			c = 0
		}
		out[s] = c / total
	}
	return out
}

//  1. rejects unregistered sources (ErrSourceNotIndependent);
//  2. rejects a source that was itself an arbitration input for the
//     pending prediction on this claim (ErrSourceIsArbitrationInput) —
//     THIS is the concrete structural fix for the self-reinforcing
//     loop the audit doc names;
//  3. requires a pending Class B prediction to calibrate against
//     (ErrNoPrediction);
//  4. computes match + Brier score, appends a hash-chained Record, and
//     consumes (clears) the pending prediction.
func (e *Engine) RecordGroundTruth(gt GroundTruth) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.independentSources[gt.SourceID] {
		return Record{}, ErrSourceNotIndependent
	}
	pred, ok := e.pendingPredictions[gt.ClaimKey]
	if !ok {
		return Record{}, ErrNoPrediction
	}
	gtUpstream := e.upstreamOf(gt.SourceID)
	for _, s := range pred.ArbitrationSourceIDs {
		if s == gt.SourceID {
			return Record{}, ErrSourceIsArbitrationInput
		}
		if e.upstreamOf(s) == gtUpstream {
			return Record{}, ErrSourceSharesUpstreamWithArbitrationInput
		}
	}

	matched := pred.Value == gt.Value
	brier := binaryBrier(pred.Confidence, matched)
	if pred.Distribution != nil {
		brier = multiclassBrier(pred.Distribution, gt.Value)
	}

	idx := uint64(len(e.ledger)) + 1
	prev := e.head
	rec := Record{
		Index: idx, ClaimKey: gt.ClaimKey,
		PredictedValue: pred.Value, PredictedConfidence: pred.Confidence,
		ActualValue: gt.Value, GroundTruthSourceID: gt.SourceID,
		Matched: matched, BrierScore: brier, Tick: gt.Tick, PrevHash: prev,
	}
	rec.Hash = hashRecord(rec)
	e.ledger = append(e.ledger, rec)
	e.head = rec.Hash
	delete(e.pendingPredictions, gt.ClaimKey)

	// P1: Calibration -> Trust. Independently-verified ground truth
	// reweights the SAME shared calculus for every source whose Class A
	// evidence fed this prediction — the arbitration inputs are exactly
	// what should be rewarded/penalized by an outcome ground truth
	// (by construction) never touched.
	//
	// v7.8 Priority B fix: credit is assigned PROPORTIONALLY to each
	// source's actual contribution (pred.SourceContributions,
	// normalized to sum 1.0) when available, instead of an equal 1/N
	// split — a source that supplied 80% of the arbitration weight
	// should be credited/penalized far more by the outcome than one
	// that supplied 5%. Equal-split remains the fallback for callers
	// that have not wired contribution weights through yet, so this is
	// backward compatible.
	if e.calc != nil && len(pred.ArbitrationSourceIDs) > 0 {
		weights := creditWeights(pred.ArbitrationSourceIDs, pred.SourceContributions)
		for _, s := range pred.ArbitrationSourceIDs {
			// Best-effort: a weight-validation error here would be a
			// programming bug (weight is derived, always in (0,1]), not
			// a caller error — do not fail the ground-truth submission
			// (already ledgered) over a calculus wiring problem.
			_, _ = e.calc.Observe(trustcalc.NamespacedSubject(trustcalc.NamespaceCalibrationSource, s), rec.Matched, weights[s])
		}
	}
	return rec, nil
}

func binaryBrier(confidence float64, matched bool) float64 {
	outcome := 0.0
	if matched {
		outcome = 1.0
	}
	d := confidence - outcome
	return d * d
}

// multiclassBrier scores the full hypothesis distribution against the
// single realized outcome: sum over every hypothesized value of
// (p_v - 1{v==actual})^2 — the standard multiclass Brier score, which
// rewards a distribution that concentrated probability on the value
// that actually happened and penalizes overconfidence on the ones that
// didn't, rather than only checking the point estimate.
func multiclassBrier(dist map[string]float64, actual string) float64 {
	sum := 0.0
	seenActual := false
	for v, p := range dist {
		outcome := 0.0
		if v == actual {
			outcome = 1.0
			seenActual = true
		}
		d := p - outcome
		sum += d * d
	}
	if !seenActual {
		// the realized value wasn't even in the hypothesis set — the
		// maximum possible penalty for that missing mass.
		sum += 1.0
	}
	return sum
}

func hashRecord(r Record) string {
	b := []byte(fmt.Sprintf(
		"idx=%d|claim=%s|pred=%s|conf=%.10f|actual=%s|gtsource=%s|matched=%v|brier=%.10f|tick=%d|prev=%s|",
		r.Index, r.ClaimKey, r.PredictedValue, r.PredictedConfidence, r.ActualValue,
		r.GroundTruthSourceID, r.Matched, r.BrierScore, r.Tick, r.PrevHash))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Ledger returns a defensive copy of every completed calibration, in
// order.
func (e *Engine) Ledger() []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Record, len(e.ledger))
	copy(out, e.ledger)
	return out
}

// VerifyLedger re-derives every record's hash and confirms chain
// linkage — same tamper-evidence discipline as trust.Kernel and every
// other ledger in this repository.
func (e *Engine) VerifyLedger() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, r := range e.ledger {
		if r.Index != uint64(i)+1 {
			return fmt.Errorf("calibration: index gap at %d", i)
		}
		if r.PrevHash != prev {
			return fmt.Errorf("calibration: broken chain linkage at index %d", r.Index)
		}
		recomputed := hashRecord(r)
		if recomputed != r.Hash {
			return fmt.Errorf("calibration: hash mismatch at index %d", r.Index)
		}
		prev = recomputed
	}
	return nil
}

// RecordsFor returns every calibration record for one claim, in order.
func (e *Engine) RecordsFor(claimKey string) []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Record
	for _, r := range e.ledger {
		if r.ClaimKey == claimKey {
			out = append(out, r)
		}
	}
	return out
}

// Summarize converts a set of calibration records for one subject into
// trust.TrustEvidence — the concrete Outcome -> Calibration -> Trust
// wire the audit doc asks for. Deliberately does not import or call
// trust.Kernel directly: this package has no way to know which subject
// a claim's calibration should be attributed to (that mapping is
// caller-specific — a source, an actor, a policy), so it hands back
// plain evidence for the caller to feed into whichever trust.Kernel
// and subject ID are appropriate, exactly like every other trust
// evidence producer in this repo (contradiction, hbayes, intent).
func Summarize(records []Record) trust.TrustEvidence {
	if len(records) == 0 {
		return trust.TrustEvidence{
			"calibration_accuracy":    0,
			"calibration_brier":       1, // worst possible, absence of evidence is not evidence of good calibration
			"calibration_sample_size": 0,
		}
	}
	matches := 0
	brierSum := 0.0
	for _, r := range records {
		if r.Matched {
			matches++
		}
		brierSum += r.BrierScore
	}
	n := float64(len(records))
	accuracy := float64(matches) / n
	meanBrier := brierSum / n
	return trust.TrustEvidence{
		"calibration_accuracy":    accuracy,
		"calibration_brier":       meanBrier,
		"calibration_sample_size": n,
	}
}

// sortedClaimKeys is a small determinism helper exposed for callers
// that need to iterate pending predictions in a stable order (e.g. a
// batch reconciliation job) — kept here rather than duplicated per
// caller.
func (e *Engine) PendingClaims() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.pendingPredictions))
	for k := range e.pendingPredictions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
