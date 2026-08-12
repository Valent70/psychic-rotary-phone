// Package dataquality closes the two P2 capabilities the v7.11 auditor
// added on top of the 12 OPEN areas: a decomposed data-quality vector,
// and bounded, deterministic, auditable source-reliability learning.
//
// The auditor's exact constraint was "Jangan gunakan satu score tanpa
// decomposition" — so Vector is the primary type and the composite is a
// derived, explicitly-weighted projection of it, never the other way
// round. A single 0.72 that cannot say whether the feed is stale or
// merely incomplete is an alert nobody can act on.
//
// The second constraint was that source reliability must not be an
// "arbitrary constant selamanya", but that any update must be bounded,
// deterministic, explainable, versioned, auditable and replayable.
// That is what Learner implements: a Beta-posterior reliability with a
// hard per-update movement cap, a hash-chained update ledger, and a
// deterministic replay from the ledger alone.
package dataquality

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrDimensionRange rejects a quality dimension outside [0,1].
	ErrDimensionRange = errors.New("dataquality: dimension outside [0,1]")
	// ErrWeightSum rejects a weighting that does not sum to 1.
	ErrWeightSum = errors.New("dataquality: composite weights must sum to 1")
	// ErrUnknownSource rejects an update for a source that was never
	// registered — an unregistered source must not be able to teach the
	// system anything about itself.
	ErrUnknownSource = errors.New("dataquality: source not registered")
	// ErrDuplicateObservation rejects re-applying the same outcome
	// observation twice; without it a replayed ledger would compound.
	ErrDuplicateObservation = errors.New("dataquality: duplicate observation id")
	// ErrNonMonotonicTick rejects an out-of-order update. The ledger is
	// append-only in tick order; accepting a backdated update would make
	// "reliability at tick T" ambiguous.
	ErrNonMonotonicTick = errors.New("dataquality: tick must not go backwards")
	// ErrLedgerTampered is returned by VerifyChain.
	ErrLedgerTampered = errors.New("dataquality: reliability ledger hash chain broken")
)

// ---------------------------------------------------------------------
// Quality vector
// ---------------------------------------------------------------------

// Vector is the decomposed data-quality assessment of one batch from
// one source. Every dimension is in [0,1] where 1 is best.
type Vector struct {
	// Completeness: fraction of required fields actually populated.
	Completeness float64 `json:"completeness"`
	// Freshness: 1 at ingest, decaying with observation age.
	Freshness float64 `json:"freshness"`
	// Validity: fraction of records passing schema/range validation.
	Validity float64 `json:"validity"`
	// Consistency: 1 minus the internal contradiction rate.
	Consistency float64 `json:"consistency"`
	// Provenance: how well the batch is attributed (declared upstream,
	// signature, producer). Distinct from reliability: a perfectly
	// attributed feed can still be wrong.
	Provenance float64 `json:"provenance"`
	// Reliability: the learned historical accuracy of the source.
	Reliability float64 `json:"reliability"`
}

// Weights projects a Vector onto a scalar. It is a separate, explicit,
// validated type so that "how we weighted quality" is itself a
// versioned decision that lands in a hash, not a constant buried in a
// function.
type Weights struct {
	Completeness float64 `json:"completeness"`
	Freshness    float64 `json:"freshness"`
	Validity     float64 `json:"validity"`
	Consistency  float64 `json:"consistency"`
	Provenance   float64 `json:"provenance"`
	Reliability  float64 `json:"reliability"`
	Version      uint64  `json:"version"`
}

// DefaultWeights is the reference weighting. Validity and reliability
// carry the most mass because a fresh, complete, well-attributed batch
// of wrong numbers is worse than a stale batch of right ones.
func DefaultWeights() Weights {
	return Weights{
		Completeness: 0.15, Freshness: 0.15, Validity: 0.25,
		Consistency: 0.15, Provenance: 0.10, Reliability: 0.20, Version: 1,
	}
}

func (w Weights) validate() error {
	sum := w.Completeness + w.Freshness + w.Validity + w.Consistency + w.Provenance + w.Reliability
	if math.Abs(sum-1) > 1e-9 {
		return fmt.Errorf("%w: got %v", ErrWeightSum, sum)
	}
	return nil
}

func (v Vector) validate() error {
	for name, x := range map[string]float64{
		"completeness": v.Completeness, "freshness": v.Freshness, "validity": v.Validity,
		"consistency": v.Consistency, "provenance": v.Provenance, "reliability": v.Reliability,
	} {
		if x < 0 || x > 1 || math.IsNaN(x) {
			return fmt.Errorf("%w: %s=%v", ErrDimensionRange, name, x)
		}
	}
	return nil
}

// Assessment is a content-addressed quality verdict for one batch.
type Assessment struct {
	SourceID  string  `json:"source_id"`
	BatchID   string  `json:"batch_id"`
	Tick      uint64  `json:"tick"`
	Vector    Vector  `json:"vector"`
	Weights   Weights `json:"weights"`
	Composite float64 `json:"composite"`
	// Bottleneck names the single worst dimension. A composite alone
	// tells an operator that something is wrong; the bottleneck tells
	// them what to go fix.
	Bottleneck      string   `json:"bottleneck"`
	BottleneckValue float64  `json:"bottleneck_value"`
	Gates           []string `json:"gates_failed"`
	Acceptable      bool     `json:"acceptable"`
	Hash            string   `json:"hash"`
}

// Floors are per-dimension minimums. They exist because a weighted mean
// can hide a zero: a feed with validity 0.0 and everything else at 1.0
// scores 0.75 on DefaultWeights, which is not "acceptable with a
// caveat", it is unusable. Floors are checked before the composite and
// veto it.
type Floors struct {
	Completeness float64
	Freshness    float64
	Validity     float64
	Consistency  float64
	Provenance   float64
	Reliability  float64
	Composite    float64
}

// DefaultFloors returns conservative minimums.
func DefaultFloors() Floors {
	return Floors{Completeness: 0.5, Freshness: 0.2, Validity: 0.8,
		Consistency: 0.5, Provenance: 0.3, Reliability: 0.2, Composite: 0.6}
}

// Assess computes the composite, the bottleneck and the floor verdict.
func Assess(sourceID, batchID string, tick uint64, v Vector, w Weights, fl Floors) (Assessment, error) {
	if err := v.validate(); err != nil {
		return Assessment{}, err
	}
	if err := w.validate(); err != nil {
		return Assessment{}, err
	}
	a := Assessment{SourceID: sourceID, BatchID: batchID, Tick: tick, Vector: v, Weights: w}
	a.Composite = v.Completeness*w.Completeness + v.Freshness*w.Freshness +
		v.Validity*w.Validity + v.Consistency*w.Consistency +
		v.Provenance*w.Provenance + v.Reliability*w.Reliability

	dims := []struct {
		name  string
		value float64
		floor float64
	}{
		{"completeness", v.Completeness, fl.Completeness},
		{"consistency", v.Consistency, fl.Consistency},
		{"freshness", v.Freshness, fl.Freshness},
		{"provenance", v.Provenance, fl.Provenance},
		{"reliability", v.Reliability, fl.Reliability},
		{"validity", v.Validity, fl.Validity},
	}
	a.BottleneckValue = math.Inf(1)
	for _, d := range dims {
		if d.value < a.BottleneckValue {
			a.BottleneckValue, a.Bottleneck = d.value, d.name
		}
		if d.value < d.floor {
			a.Gates = append(a.Gates, d.name+"="+fnum(d.value)+" below floor "+fnum(d.floor))
		}
	}
	if a.Composite < fl.Composite {
		a.Gates = append(a.Gates, "composite="+fnum(a.Composite)+" below floor "+fnum(fl.Composite))
	}
	sort.Strings(a.Gates)
	a.Acceptable = len(a.Gates) == 0
	a.Hash = a.computeHash()
	return a, nil
}

// FreshnessAt is the standard exponential staleness curve, expressed in
// ticks so it is replayable: freshness = 2^(-age/halfLife).
func FreshnessAt(observedTick, nowTick, halfLife uint64) float64 {
	if halfLife == 0 || nowTick <= observedTick {
		return 1
	}
	age := float64(nowTick - observedTick)
	return math.Exp2(-age / float64(halfLife))
}

func (a Assessment) computeHash() string {
	var sb strings.Builder
	sb.WriteString("dq_assessment/v1\nsource=" + a.SourceID + "\nbatch=" + a.BatchID + "\n")
	sb.WriteString("tick=" + strconv.FormatUint(a.Tick, 10) + "\n")
	sb.WriteString("v=" + fnum(a.Vector.Completeness) + "," + fnum(a.Vector.Freshness) + "," +
		fnum(a.Vector.Validity) + "," + fnum(a.Vector.Consistency) + "," +
		fnum(a.Vector.Provenance) + "," + fnum(a.Vector.Reliability) + "\n")
	sb.WriteString("wv=" + strconv.FormatUint(a.Weights.Version, 10) + "\n")
	sb.WriteString("composite=" + fnum(a.Composite) + "\n")
	sb.WriteString("bottleneck=" + a.Bottleneck + "\n")
	for _, g := range a.Gates {
		sb.WriteString("gate=" + g + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------
// Source reliability learning
// ---------------------------------------------------------------------

// Update is one immutable entry in the reliability ledger.
type Update struct {
	Index         uint64   `json:"index"`
	ObservationID string   `json:"observation_id"`
	SourceID      string   `json:"source_id"`
	Tick          uint64   `json:"tick"`
	Correct       bool     `json:"correct"`
	Weight        float64  `json:"weight"`
	Alpha         float64  `json:"alpha"`
	Beta          float64  `json:"beta"`
	Before        float64  `json:"before"`
	Raw           float64  `json:"raw"`     // posterior mean before clamping
	After         float64  `json:"after"`   // post-clamp reliability
	Clamped       bool     `json:"clamped"` // true when the cap bound the move
	Reason        string   `json:"reason"`
	EvidenceRefs  []string `json:"evidence_refs"`
	PrevHash      string   `json:"prev_hash"`
	Hash          string   `json:"hash"`
}

// Config parameterises the learner. All fields are part of the
// learner's own fingerprint, so two learners with different caps are
// not silently interchangeable in a replay.
type Config struct {
	// PriorStrength is the pseudo-count behind the initial reliability.
	// A large value means "we need a lot of evidence to move off the
	// declared authority", which is the right default for a registry
	// source whose authority was assigned by a human.
	PriorStrength float64
	// MaxStepPerUpdate caps how far one observation may move
	// reliability. This is the "bounded" requirement: without it, a
	// single adversarial ground-truth injection can collapse a source.
	MaxStepPerUpdate float64
	// Floor and Ceiling bound reliability away from 0 and 1. A source
	// at exactly 0 can never recover; a source at exactly 1 cannot be
	// contradicted. Both are epistemically wrong.
	Floor   float64
	Ceiling float64
	Version uint64
}

// DefaultConfig returns the reference learner configuration.
func DefaultConfig() Config {
	return Config{PriorStrength: 20, MaxStepPerUpdate: 0.05, Floor: 0.05, Ceiling: 0.98, Version: 1}
}

func (c Config) normalise() Config {
	if c.PriorStrength <= 0 {
		c.PriorStrength = 20
	}
	if c.MaxStepPerUpdate <= 0 {
		c.MaxStepPerUpdate = 0.05
	}
	if c.Floor <= 0 {
		c.Floor = 0.05
	}
	if c.Ceiling <= 0 || c.Ceiling > 1 {
		c.Ceiling = 0.98
	}
	return c
}

type sourceState struct {
	declared float64
	alpha    float64
	beta     float64
	current  float64
	lastTick uint64
}

// Learner maintains reliability for many sources over an append-only,
// hash-chained ledger.
type Learner struct {
	mu     sync.RWMutex
	cfg    Config
	state  map[string]*sourceState
	ledger []Update
	seen   map[string]struct{}
}

// NewLearner constructs a Learner.
func NewLearner(cfg Config) *Learner {
	return &Learner{cfg: cfg.normalise(), state: map[string]*sourceState{}, seen: map[string]struct{}{}}
}

// Register declares a source with its initial (human-assigned or
// registry-derived) reliability.
func (l *Learner) Register(sourceID string, declared float64) error {
	if declared < 0 || declared > 1 || math.IsNaN(declared) {
		return fmt.Errorf("%w: declared=%v", ErrDimensionRange, declared)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	d := clamp(declared, l.cfg.Floor, l.cfg.Ceiling)
	l.state[sourceID] = &sourceState{
		declared: d,
		alpha:    d * l.cfg.PriorStrength,
		beta:     (1 - d) * l.cfg.PriorStrength,
		current:  d,
	}
	return nil
}

// Observe applies one outcome observation.
//
// correct is the verdict of a ground-truth comparison performed
// elsewhere (pkg/moat/calibration owns that comparison). This package
// deliberately does not decide what "correct" means — a reliability
// learner that also defines truth is a closed loop that can talk itself
// into any answer.
func (l *Learner) Observe(observationID, sourceID string, tick uint64, correct bool,
	weight float64, reason string, evidenceRefs []string) (Update, error) {

	if weight <= 0 {
		weight = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	st, ok := l.state[sourceID]
	if !ok {
		return Update{}, fmt.Errorf("%w: %s", ErrUnknownSource, sourceID)
	}
	if _, dup := l.seen[observationID]; dup {
		return Update{}, fmt.Errorf("%w: %s", ErrDuplicateObservation, observationID)
	}
	if tick < st.lastTick {
		return Update{}, fmt.Errorf("%w: %d < %d", ErrNonMonotonicTick, tick, st.lastTick)
	}

	before := st.current
	if correct {
		st.alpha += weight
	} else {
		st.beta += weight
	}
	raw := st.alpha / (st.alpha + st.beta)
	after := clamp(raw, before-l.cfg.MaxStepPerUpdate, before+l.cfg.MaxStepPerUpdate)
	after = clamp(after, l.cfg.Floor, l.cfg.Ceiling)

	u := Update{
		Index: uint64(len(l.ledger)), ObservationID: observationID, SourceID: sourceID,
		Tick: tick, Correct: correct, Weight: weight,
		Alpha: st.alpha, Beta: st.beta,
		Before: before, Raw: raw, After: after,
		Clamped: math.Abs(raw-after) > 1e-12,
		Reason:  reason, EvidenceRefs: append([]string(nil), evidenceRefs...),
	}
	sort.Strings(u.EvidenceRefs)
	if len(l.ledger) > 0 {
		u.PrevHash = l.ledger[len(l.ledger)-1].Hash
	}
	u.Hash = hashUpdate(u)

	st.current = after
	st.lastTick = tick
	l.seen[observationID] = struct{}{}
	l.ledger = append(l.ledger, u)
	return u, nil
}

// Reliability returns the current learned reliability.
func (l *Learner) Reliability(sourceID string) (float64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st, ok := l.state[sourceID]
	if !ok {
		return 0, false
	}
	return st.current, true
}

// ReliabilityAt answers "what did we believe at tick T" by folding the
// ledger, never by reading current state. This is what makes a past
// decision replayable under the reliability that actually applied then.
func (l *Learner) ReliabilityAt(sourceID string, tick uint64) (float64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st, ok := l.state[sourceID]
	if !ok {
		return 0, false
	}
	value := st.declared
	for _, u := range l.ledger {
		if u.SourceID != sourceID || u.Tick > tick {
			continue
		}
		value = u.After
	}
	return value, true
}

// Explain renders the human-readable derivation of a source's current
// reliability.
func (l *Learner) Explain(sourceID string) ([]string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st, ok := l.state[sourceID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, sourceID)
	}
	out := []string{"declared reliability " + fnum(st.declared) +
		" with prior strength " + fnum(l.cfg.PriorStrength)}
	for _, u := range l.ledger {
		if u.SourceID != sourceID {
			continue
		}
		verdict := "incorrect"
		if u.Correct {
			verdict = "correct"
		}
		line := "tick " + strconv.FormatUint(u.Tick, 10) + ": " + verdict +
			" (w=" + fnum(u.Weight) + ") -> " + fnum(u.Before) + " => " + fnum(u.After)
		if u.Clamped {
			line += " [CLAMPED from " + fnum(u.Raw) + " by step cap " + fnum(l.cfg.MaxStepPerUpdate) + "]"
		}
		if u.Reason != "" {
			line += " reason=" + u.Reason
		}
		out = append(out, line)
	}
	return out, nil
}

// Ledger returns a copy of the append-only update ledger.
func (l *Learner) Ledger() []Update {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Update(nil), l.ledger...)
}

// VerifyChain re-derives every hash and every link.
func (l *Learner) VerifyChain() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prev := ""
	for i, u := range l.ledger {
		if u.Index != uint64(i) {
			return fmt.Errorf("%w: index %d out of order", ErrLedgerTampered, u.Index)
		}
		if u.PrevHash != prev {
			return fmt.Errorf("%w: broken link at %d", ErrLedgerTampered, i)
		}
		if hashUpdate(u) != u.Hash {
			return fmt.Errorf("%w: content hash mismatch at %d", ErrLedgerTampered, i)
		}
		prev = u.Hash
	}
	return nil
}

// Head returns the ledger tip hash — the single value a certificate
// commits to in order to pin the reliability model of a decision.
func (l *Learner) Head() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.ledger) == 0 {
		return ""
	}
	return l.ledger[len(l.ledger)-1].Hash
}

// Replay rebuilds a Learner from a ledger and the original declared
// reliabilities, and returns an error if any recomputed value diverges.
// This is the "replayable" half of the auditor's requirement: the
// ledger is not just stored, it is sufficient.
func Replay(cfg Config, declared map[string]float64, ledger []Update) (*Learner, error) {
	l := NewLearner(cfg)
	ids := make([]string, 0, len(declared))
	for id := range declared {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := l.Register(id, declared[id]); err != nil {
			return nil, err
		}
	}
	for _, u := range ledger {
		got, err := l.Observe(u.ObservationID, u.SourceID, u.Tick, u.Correct, u.Weight, u.Reason, u.EvidenceRefs)
		if err != nil {
			return nil, err
		}
		if got.Hash != u.Hash {
			return nil, fmt.Errorf("dataquality: replay divergence at index %d: %s != %s",
				u.Index, got.Hash, u.Hash)
		}
	}
	return l, nil
}

func hashUpdate(u Update) string {
	var sb strings.Builder
	sb.WriteString("dq_reliability_update/v1\n")
	sb.WriteString("i=" + strconv.FormatUint(u.Index, 10) + "\n")
	sb.WriteString("obs=" + u.ObservationID + "\nsrc=" + u.SourceID + "\n")
	sb.WriteString("tick=" + strconv.FormatUint(u.Tick, 10) + "\n")
	sb.WriteString("correct=" + strconv.FormatBool(u.Correct) + "\nw=" + fnum(u.Weight) + "\n")
	sb.WriteString("a=" + fnum(u.Alpha) + "\nb=" + fnum(u.Beta) + "\n")
	sb.WriteString("before=" + fnum(u.Before) + "\nraw=" + fnum(u.Raw) + "\nafter=" + fnum(u.After) + "\n")
	sb.WriteString("clamped=" + strconv.FormatBool(u.Clamped) + "\nreason=" + u.Reason + "\n")
	sb.WriteString("refs=" + strings.Join(u.EvidenceRefs, ";") + "\n")
	sb.WriteString("prev=" + u.PrevHash + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func fnum(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }
