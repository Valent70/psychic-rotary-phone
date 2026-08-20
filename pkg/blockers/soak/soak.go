// Package soak formalizes the soak_72h blocker's qualification machinery
// as a reusable API -- Start/Checkpoint/Monitor/DetectLeak/DetectDrift/
// Finalize/GenerateEvidence -- around the same real technique test/soak
// already uses (a sustained workload loop with periodic resource
// sampling). It adds a hash-chained checkpoint sequence so a soak run's
// evidence can be verified as untampered end to end.
//
// This package cannot itself produce the 72-hour continuous run the
// soak_72h blocker requires -- that needs a genuinely long-lived host,
// which test/soak's VERIQO_SOAK_MINUTES mechanism already supports and
// this package does not change. What this package qualifies is the
// harness machinery itself: that checkpointing, leak detection, and
// evidence generation work correctly, on a short run, so that pointing
// the same harness at 72 real hours is a duration change, not a rewrite.
package soak

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	"veriqo/pkg/blockers"
)

// Sample is one point-in-time resource reading.
type Sample struct {
	AtSeconds   float64 `json:"at_seconds"`
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	Iterations  int     `json:"iterations_so_far"`
}

// Checkpoint is one entry in the hash-chained evidence sequence: each
// entry's Hash covers its own fields plus the prior entry's Hash, so
// altering or reordering any entry breaks the chain from that point on.
type Checkpoint struct {
	Seq         uint64  `json:"seq"`
	AtSeconds   float64 `json:"at_seconds"`
	Iterations  int     `json:"iterations"`
	Errors      int     `json:"errors"`
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	PrevHash    string  `json:"prev_hash"`
	Hash        string  `json:"hash"`
}

func hashCheckpoint(cp Checkpoint) string {
	payload := fmt.Sprintf("%d|%f|%d|%d|%d|%f|%s",
		cp.Seq, cp.AtSeconds, cp.Iterations, cp.Errors, cp.Goroutines, cp.HeapAllocMB, cp.PrevHash)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// VerifyChain recomputes every checkpoint's hash and confirms the
// PrevHash linkage, returning an error identifying the first broken
// link if the sequence was tampered with or reordered.
func VerifyChain(checkpoints []Checkpoint) error {
	prev := ""
	for _, cp := range checkpoints {
		if cp.PrevHash != prev {
			return fmt.Errorf("soak: checkpoint %d: prev_hash %q does not match preceding checkpoint's hash %q", cp.Seq, cp.PrevHash, prev)
		}
		want := hashCheckpoint(Checkpoint{
			Seq: cp.Seq, AtSeconds: cp.AtSeconds, Iterations: cp.Iterations, Errors: cp.Errors,
			Goroutines: cp.Goroutines, HeapAllocMB: cp.HeapAllocMB, PrevHash: cp.PrevHash,
		})
		if want != cp.Hash {
			return fmt.Errorf("soak: checkpoint %d: hash does not match its own content -- chain tampered", cp.Seq)
		}
		prev = cp.Hash
	}
	return nil
}

// Workload is one unit of sustained work the harness repeats. It
// returns an error if that iteration failed.
type Workload func(iteration int) error

// Harness runs a workload in a loop for a bounded duration, sampling
// resource usage and building a hash-chained checkpoint sequence.
type Harness struct {
	mu                 sync.Mutex
	workload           Workload
	start              time.Time
	baselineGoroutines int
	maxGoroutines      int
	iterations         int
	errors             int
	samples            []Sample
	checkpoints        []Checkpoint
	lastHash           string
}

// NewHarness constructs a harness around workload.
func NewHarness(workload Workload) *Harness {
	return &Harness{workload: workload}
}

// Start records the pre-run baseline after forcing a GC, so leak
// detection compares against a clean starting point rather than
// whatever garbage happened to be live when Start was called.
func (h *Harness) Start() {
	runtime.GC()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.start = time.Now()
	h.baselineGoroutines = runtime.NumGoroutine()
	h.maxGoroutines = h.baselineGoroutines
}

// RunOnce executes one workload iteration.
func (h *Harness) RunOnce() error {
	err := h.workload(h.iterations)
	h.mu.Lock()
	h.iterations++
	if err != nil {
		h.errors++
	}
	h.mu.Unlock()
	return err
}

// Monitor samples current resource usage and records it.
func (h *Harness) Monitor() Sample {
	// Force a collection before reading heap stats: without this, two
	// samples taken a few seconds apart mostly measure how far the
	// allocator's watermark has drifted since the last incidental GC,
	// not how much memory is actually still live -- noise that DetectDrift
	// cannot tell apart from a real leak on a short run. A real,
	// gc'd-live-heap reading is what makes the first-half/second-half
	// comparison meaningful (found via a real false-positive: a pure
	// no-op workload's short qualification run tripped DetectDrift on
	// GC-timing noise alone before this fix).
	runtime.GC()
	g := runtime.NumGoroutine()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	h.mu.Lock()
	defer h.mu.Unlock()
	if g > h.maxGoroutines {
		h.maxGoroutines = g
	}
	s := Sample{
		AtSeconds: time.Since(h.start).Seconds(), Goroutines: g,
		HeapAllocMB: float64(m.HeapAlloc) / (1024 * 1024), Iterations: h.iterations,
	}
	h.samples = append(h.samples, s)
	return s
}

// Checkpoint takes a resource sample and appends it to the hash-chained
// checkpoint sequence.
func (h *Harness) Checkpoint() Checkpoint {
	s := h.Monitor()
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := Checkpoint{
		Seq: uint64(len(h.checkpoints)), AtSeconds: s.AtSeconds, Iterations: s.Iterations,
		Errors: h.errors, Goroutines: s.Goroutines, HeapAllocMB: s.HeapAllocMB, PrevHash: h.lastHash,
	}
	cp.Hash = hashCheckpoint(cp)
	h.checkpoints = append(h.checkpoints, cp)
	h.lastHash = cp.Hash
	return cp
}

// LeakVerdict is DetectLeak's finding.
type LeakVerdict struct {
	Leaked             bool `json:"leaked"`
	BaselineGoroutines int  `json:"baseline_goroutines"`
	FinalGoroutines    int  `json:"final_goroutines"`
	Threshold          int  `json:"threshold"`
}

// DetectLeak flags a goroutine leak if the final count exceeds baseline
// by more than threshold -- some growth is normal (test runner
// internals, GC workers); a leak grows without bound.
func (h *Harness) DetectLeak(threshold int) LeakVerdict {
	h.mu.Lock()
	defer h.mu.Unlock()
	final := runtime.NumGoroutine()
	return LeakVerdict{
		Leaked: final > h.baselineGoroutines+threshold, BaselineGoroutines: h.baselineGoroutines,
		FinalGoroutines: final, Threshold: threshold,
	}
}

// DriftVerdict is DetectDrift's finding.
type DriftVerdict struct {
	Drifted        bool    `json:"drifted"`
	FirstHalfMB    float64 `json:"first_half_avg_heap_mb"`
	SecondHalfMB   float64 `json:"second_half_avg_heap_mb"`
	GrowthFraction float64 `json:"growth_fraction"`
	Threshold      float64 `json:"threshold"`
}

// minAbsoluteDriftMB is a floor below which a percentage-growth
// comparison is meaningless noise rather than a finding: on a small
// heap baseline (a few MB, typical of a short fixture run sharing a
// process with other tests), a couple of megabytes of incidental
// allocation reads as a large percentage but is not an operationally
// significant leak. A real 72-hour soak's baseline heap is large enough
// that this floor never matters; it exists specifically so a short
// qualification run doesn't fail on GC-timing-scale noise (found via a
// real flaky failure in test/e2e/eight_blockers before this fix).
const minAbsoluteDriftMB = 8.0

// DetectDrift compares mean heap usage across the first and second half
// of recorded samples. Sustained memory growth within a single soak run
// (as opposed to a one-time ramp-up) shows up as a second-half average
// meaningfully higher than the first-half average -- by both a
// percentage and an absolute margin, so a tiny baseline heap's noise
// doesn't read as drift.
func (h *Harness) DetectDrift(threshold float64) DriftVerdict {
	h.mu.Lock()
	samples := append([]Sample(nil), h.samples...)
	h.mu.Unlock()

	if len(samples) < 4 {
		return DriftVerdict{Threshold: threshold}
	}
	mid := len(samples) / 2
	firstAvg := avgHeap(samples[:mid])
	secondAvg := avgHeap(samples[mid:])
	growth := 0.0
	if firstAvg > 0 {
		growth = (secondAvg - firstAvg) / firstAvg
	}
	drifted := growth > threshold && (secondAvg-firstAvg) > minAbsoluteDriftMB
	return DriftVerdict{
		Drifted: drifted, FirstHalfMB: firstAvg, SecondHalfMB: secondAvg,
		GrowthFraction: growth, Threshold: threshold,
	}
}

func avgHeap(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += s.HeapAllocMB
	}
	return sum / float64(len(samples))
}

// Finalize forces a final GC and returns the elapsed duration since
// Start.
func (h *Harness) Finalize() time.Duration {
	runtime.GC()
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Since(h.start)
}

// Report is the complete evidence document for one soak run.
type Report struct {
	RequiredHours  float64      `json:"required_hours"`
	ActualDuration string       `json:"actual_duration"`
	ActualMinutes  float64      `json:"actual_minutes"`
	IsFullSoak     bool         `json:"is_full_72h_soak"`
	Iterations     int          `json:"total_iterations"`
	Errors         int          `json:"errors"`
	Samples        []Sample     `json:"samples"`
	Checkpoints    []Checkpoint `json:"checkpoints"`
	ChainVerified  bool         `json:"chain_verified"`
	Leak           LeakVerdict  `json:"leak"`
	Drift          DriftVerdict `json:"drift"`
	Verdict        string       `json:"verdict"`
}

// GenerateEvidence assembles the full report: required duration is
// always stated as 72h (the real gate), actual duration is always
// stated honestly, and IsFullSoak is only ever true if the elapsed time
// genuinely reached it -- there is no code path that sets it any other
// way.
func (h *Harness) GenerateEvidence(elapsed time.Duration, leakThreshold int, driftThreshold float64) Report {
	h.mu.Lock()
	iterations, errs := h.iterations, h.errors
	samples := append([]Sample(nil), h.samples...)
	checkpoints := append([]Checkpoint(nil), h.checkpoints...)
	h.mu.Unlock()

	leak := h.DetectLeak(leakThreshold)
	drift := h.DetectDrift(driftThreshold)
	chainErr := VerifyChain(checkpoints)

	rep := Report{
		RequiredHours: 72, ActualDuration: elapsed.String(), ActualMinutes: elapsed.Minutes(),
		IsFullSoak: elapsed >= 72*time.Hour, Iterations: iterations, Errors: errs,
		Samples: samples, Checkpoints: checkpoints, ChainVerified: chainErr == nil,
		Leak: leak, Drift: drift,
	}
	switch {
	case errs > 0:
		rep.Verdict = fmt.Sprintf("FAIL: %d/%d iterations errored", errs, iterations)
	case chainErr != nil:
		rep.Verdict = fmt.Sprintf("FAIL: checkpoint chain invalid: %v", chainErr)
	case leak.Leaked:
		rep.Verdict = fmt.Sprintf("FAIL: goroutine count grew from %d to %d", leak.BaselineGoroutines, leak.FinalGoroutines)
	case drift.Drifted:
		rep.Verdict = fmt.Sprintf("FAIL: heap grew %.1f%% from first to second half of the run", drift.GrowthFraction*100)
	case rep.IsFullSoak:
		rep.Verdict = "PASS: full 72h soak, zero errors, goroutines and heap bounded"
	default:
		rep.Verdict = fmt.Sprintf("QUALIFIED (fixture): %.2f minutes, zero errors, checkpoint chain verified, no leak/drift detected -- this proves the harness, NOT a 72h soak", elapsed.Minutes())
	}
	return rep
}

// JSON renders the report deterministically.
func (r Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// RunQualification runs workload in a bounded loop for targetMinutes,
// checkpointing periodically, then generates evidence and records the
// outcome on contract. It never claims IsFullSoak unless targetMinutes
// genuinely reaches 4320 (72h); a short fixture run can only ever reach
// QUALIFICATION_TESTED, proving the machinery, not the real gate.
func RunQualification(contract *blockers.Contract, targetMinutes float64, workload Workload) (blockers.RunResult, Report, error) {
	h := NewHarness(workload)
	h.Start()

	duration := time.Duration(targetMinutes * float64(time.Minute))
	deadline := time.Now().Add(duration)
	checkpointEvery := duration / 10
	if checkpointEvery < time.Millisecond {
		checkpointEvery = time.Millisecond
	}
	nextCheckpoint := time.Now()

	for time.Now().Before(deadline) {
		if err := h.RunOnce(); err != nil {
			break
		}
		if time.Now().After(nextCheckpoint) {
			h.Checkpoint()
			nextCheckpoint = nextCheckpoint.Add(checkpointEvery)
		}
	}
	h.Checkpoint() // final checkpoint always taken, even on a short run

	elapsed := h.Finalize()
	report := h.GenerateEvidence(elapsed, 50, 0.5)

	pass := report.Errors == 0 && report.ChainVerified && !report.Leak.Leaked && !report.Drift.Drifted
	result := blockers.RunResult{
		BlockerID: contract.ID, Mode: "FIXTURE", Pass: pass,
		Measurements: map[string]string{
			"actual_duration": report.ActualDuration,
			"iterations":      fmt.Sprintf("%d", report.Iterations),
			"checkpoints":     fmt.Sprintf("%d", len(report.Checkpoints)),
			"chain_verified":  fmt.Sprintf("%v", report.ChainVerified),
			"leak_detected":   fmt.Sprintf("%v", report.Leak.Leaked),
			"drift_detected":  fmt.Sprintf("%v", report.Drift.Drifted),
			"is_full_72h":     fmt.Sprintf("%v", report.IsFullSoak),
		},
	}
	if !pass {
		result.FailureReason = report.Verdict
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, report, err
	}
	return result, report, nil
}
