// Package scorecard computes and TRACKS an Internal Engineering Moat
// Readiness Score. "Yang_masih_saya_kritisi.docx" §6 pushed back on
// the prior session's one-shot Compute(Inputs) function directly:
// "MOAT tidak boleh hanya menjadi score. Saya ingin: MOAT menjadi:
// continuous measurement." Tracker is that response: Compute (below)
// is unchanged pure math, but it now feeds a Tracker that retains a
// bounded rolling history of every measurement, exposing Trend()
// (is engineering maturity improving, flat, or regressing over the
// tracked window) rather than a single point-in-time figure a caller
// could quote once and never revisit.
//
// The non-negotiable scope statement from the prior session is
// unchanged and still enforced by the struct itself, not left to a
// reader's memory: EngineeringMaturityScore measures internal
// contract/ledger/verification discipline ONLY. ExternalDataMoat is
// always present, always non-empty, and always names the same honest
// gap: exclusive data partnerships (SWIFT, satellite, B/L, insurance,
// port authority, IC access) are business/legal acquisitions this
// codebase cannot fabricate — see CRITICAL_FINDINGS (BRUTAL HONESTY).
package scorecard

import (
	"fmt"
	"sync"
)

type Inputs struct {
	TrustLedgerVerified      bool
	KnowledgeChainVerified   bool
	ArbitratedClaimsCount    int
	ContradictionsResolved   int
	ReplayVerificationsTotal int
	ReplayVerificationsPass  int
	EvidenceGraphNodeCount   int
	EvidenceGraphEdgeCount   int
	EnginesRegistered        int
}

type Report struct {
	EngineeringMaturityScore float64
	Breakdown                map[string]float64
	ExternalDataMoat         string
	Caveats                  []string
}

const expectedEngines = 7

// Compute derives an EngineeringMaturityScore in [0,100] from Inputs —
// pure function, unchanged math from the prior session, still weighted
// across five measurable dimensions: ledger integrity (25%), engine
// completeness (20%), contradiction resolution activity (20%), replay
// verification pass rate (20%), evidence graph richness (15%, capped).
func Compute(in Inputs) Report {
	breakdown := make(map[string]float64, 5)

	ledgerScore := 0.0
	if in.TrustLedgerVerified {
		ledgerScore += 12.5
	}
	if in.KnowledgeChainVerified {
		ledgerScore += 12.5
	}
	breakdown["ledger_integrity"] = ledgerScore

	completeness := 20.0 * float64(in.EnginesRegistered) / float64(expectedEngines)
	if completeness > 20 {
		completeness = 20
	}
	breakdown["engine_completeness"] = completeness

	contradictionScore := 0.0
	if in.ArbitratedClaimsCount > 0 {
		ratio := float64(in.ContradictionsResolved) / float64(in.ArbitratedClaimsCount)
		if ratio > 1 {
			ratio = 1
		}
		contradictionScore = 20 * ratio
	}
	breakdown["contradiction_resolution"] = contradictionScore

	replayScore := 0.0
	if in.ReplayVerificationsTotal > 0 {
		passRate := float64(in.ReplayVerificationsPass) / float64(in.ReplayVerificationsTotal)
		if passRate > 1 {
			passRate = 1
		}
		replayScore = 20 * passRate
	}
	breakdown["replay_verification"] = replayScore

	graphSignal := float64(in.EvidenceGraphNodeCount + in.EvidenceGraphEdgeCount)
	graphScore := 15.0
	if graphSignal < 100 {
		graphScore = 15.0 * graphSignal / 100.0
	}
	breakdown["evidence_graph_richness"] = graphScore

	total := ledgerScore + completeness + contradictionScore + replayScore + graphScore

	return Report{
		EngineeringMaturityScore: total,
		Breakdown:                breakdown,
		ExternalDataMoat: "NOT ASSESSED by this scorecard. Exclusive data partnerships (SWIFT, satellite, " +
			"B/L, insurance, port authority feedback) and Intelligence Community access are " +
			"business/legal acquisitions, not code — see CRITICAL_FINDINGS (BRUTAL HONESTY) " +
			"Tier 3 for the honest gap list and cost/timeline estimates.",
		Caveats: []string{
			"EngineeringMaturityScore reflects internal contract/ledger/verification discipline only.",
			"A high score here does NOT imply competitive parity with data-moat-backed competitors (Palantir/Kpler/Windward).",
			"ContradictionsResolved counts claims where an arbitration winner was chosen despite conflicting evidence, not claims verified against independent ground truth (no ground-truth feedback loop exists yet).",
		},
	}
}

// Sample is one Tracker.Record call — a Report tagged with the tick it
// was measured at, so Trend() can reason about ordering without
// depending on wall-clock time (this repo's explicit-Tick discipline,
// same as pkg/moat/causal and pkg/moat/contradiction).
type Sample struct {
	Tick   uint64
	Report Report
}

// Trend summarizes direction and magnitude of change across a
// Tracker's retained history.
type Trend struct {
	Direction string // "improving", "flat", "regressing", "insufficient_data"
	Delta     float64
	Samples   int
}

const defaultMaxHistory = 500

// Tracker turns the one-shot Compute function into the continuous
// measurement the critique asked for: Record appends a new Report to a
// bounded rolling history (oldest evicted past maxHistory), and Trend
// / History let a caller see the trajectory, not just the latest
// number.
type Tracker struct {
	mu         sync.Mutex
	maxHistory int
	history    []Sample
}

// NewTracker builds a Tracker retaining up to maxHistory samples (<=0
// uses a sensible default of 500).
func NewTracker(maxHistory int) *Tracker {
	if maxHistory <= 0 {
		maxHistory = defaultMaxHistory
	}
	return &Tracker{maxHistory: maxHistory}
}

// Record computes a fresh Report from in and appends it to the
// tracked history at tick, evicting the oldest sample if over capacity.
func (t *Tracker) Record(tick uint64, in Inputs) Report {
	report := Compute(in)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.history = append(t.history, Sample{Tick: tick, Report: report})
	if len(t.history) > t.maxHistory {
		t.history = t.history[len(t.history)-t.maxHistory:]
	}
	return report
}

// Latest returns the most recent recorded sample, if any.
func (t *Tracker) Latest() (Sample, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.history) == 0 {
		return Sample{}, false
	}
	return t.history[len(t.history)-1], true
}

// History returns every retained sample, oldest first.
func (t *Tracker) History() []Sample {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Sample, len(t.history))
	copy(out, t.history)
	return out
}

// Trend compares the earliest and latest retained samples. Fewer than
// 2 samples yields Direction "insufficient_data" rather than a
// misleading trend from a single point.
func (t *Tracker) Trend() Trend {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.history) < 2 {
		return Trend{Direction: "insufficient_data", Samples: len(t.history)}
	}
	first := t.history[0].Report.EngineeringMaturityScore
	last := t.history[len(t.history)-1].Report.EngineeringMaturityScore
	delta := last - first
	direction := "flat"
	if delta > 0.5 {
		direction = "improving"
	} else if delta < -0.5 {
		direction = "regressing"
	}
	return Trend{Direction: direction, Delta: delta, Samples: len(t.history)}
}

// String renders a one-line human summary of the latest sample plus
// trend — for logs/CLI output, never a substitute for reading the full
// Report/Trend structs programmatically.
func (t *Tracker) String() string {
	latest, ok := t.Latest()
	if !ok {
		return "scorecard: no samples recorded yet"
	}
	trend := t.Trend()
	return fmt.Sprintf("scorecard: score=%.1f trend=%s (Δ%.1f over %d samples) as of tick=%d",
		latest.Report.EngineeringMaturityScore, trend.Direction, trend.Delta, trend.Samples, latest.Tick)
}
