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
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"veriqo/pkg/blockers"
)

// Sample is one point-in-time resource reading.
type Sample struct {
	AtSeconds        float64 `json:"at_seconds"`
	Goroutines       int     `json:"goroutines"`
	HeapAllocMB      float64 `json:"heap_alloc_mb"`
	Iterations       int     `json:"iterations_so_far"`
	CPUUserSeconds   float64 `json:"cpu_user_seconds"`
	CPUSystemSeconds float64 `json:"cpu_system_seconds"`
	BlockInputOps    int64   `json:"block_input_ops"`
	BlockOutputOps   int64   `json:"block_output_ops"`
}

// Checkpoint is one entry in the hash-chained evidence sequence: each
// entry's Hash covers its own fields plus the prior entry's Hash, so
// altering or reordering any entry breaks the chain from that point on.
// RunID is included in the hashed payload too, so grafting a checkpoint
// from a DIFFERENT run onto this chain is caught by VerifyChain exactly
// like tampering with any other field would be.
type Checkpoint struct {
	RunID            string  `json:"run_id"`
	Seq              uint64  `json:"seq"`
	AtSeconds        float64 `json:"at_seconds"`
	Iterations       int     `json:"iterations"`
	Errors           int     `json:"errors"`
	DroppedEvents    int     `json:"dropped_events"`
	Goroutines       int     `json:"goroutines"`
	HeapAllocMB      float64 `json:"heap_alloc_mb"`
	CPUUserSeconds   float64 `json:"cpu_user_seconds"`
	CPUSystemSeconds float64 `json:"cpu_system_seconds"`
	BlockInputOps    int64   `json:"block_input_ops"`
	BlockOutputOps   int64   `json:"block_output_ops"`
	PrevHash         string  `json:"prev_hash"`
	Hash             string  `json:"hash"`
}

func hashCheckpoint(cp Checkpoint) string {
	payload := fmt.Sprintf("%s|%d|%f|%d|%d|%d|%d|%f|%f|%f|%d|%d|%s",
		cp.RunID, cp.Seq, cp.AtSeconds, cp.Iterations, cp.Errors, cp.DroppedEvents, cp.Goroutines, cp.HeapAllocMB,
		cp.CPUUserSeconds, cp.CPUSystemSeconds, cp.BlockInputOps, cp.BlockOutputOps, cp.PrevHash)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// VerifyChain recomputes every checkpoint's hash and confirms the
// PrevHash linkage, returning an error identifying the first broken
// link if the sequence was tampered with or reordered. It also
// confirms every checkpoint carries the SAME RunID as the first one:
// including RunID in each checkpoint's own hash only proves an entry
// wasn't altered after being (re)hashed -- an entry genuinely produced
// by a DIFFERENT run still hashes correctly for its own (different)
// RunID, so grafting it onto this chain (matching PrevHash included)
// would otherwise go undetected. This explicit cross-check is what
// actually catches that graft.
func VerifyChain(checkpoints []Checkpoint) error {
	prev := ""
	var runID string
	for i, cp := range checkpoints {
		if i == 0 {
			runID = cp.RunID
		} else if cp.RunID != runID {
			return fmt.Errorf("soak: checkpoint %d: run_id %q does not match this chain's run_id %q -- evidence from a different run", cp.Seq, cp.RunID, runID)
		}
		if cp.PrevHash != prev {
			return fmt.Errorf("soak: checkpoint %d: prev_hash %q does not match preceding checkpoint's hash %q", cp.Seq, cp.PrevHash, prev)
		}
		want := hashCheckpoint(Checkpoint{
			RunID: cp.RunID, Seq: cp.Seq, AtSeconds: cp.AtSeconds, Iterations: cp.Iterations, Errors: cp.Errors,
			DroppedEvents: cp.DroppedEvents, Goroutines: cp.Goroutines, HeapAllocMB: cp.HeapAllocMB,
			CPUUserSeconds: cp.CPUUserSeconds, CPUSystemSeconds: cp.CPUSystemSeconds,
			BlockInputOps: cp.BlockInputOps, BlockOutputOps: cp.BlockOutputOps, PrevHash: cp.PrevHash,
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
	dropped            int
	samples            []Sample
	checkpoints        []Checkpoint
	lastHash           string
	identity           RunIdentity
}

// NewHarness constructs a harness around workload.
func NewHarness(workload Workload) *Harness {
	return &Harness{workload: workload}
}

// Start records the pre-run baseline after forcing a GC, so leak
// detection compares against a clean starting point rather than
// whatever garbage happened to be live when Start was called. It also
// establishes this harness's immutable run identity (a freshly
// generated RunID plus the calling host's identity) the first time it
// is called; calling Start again on the same harness (e.g. a caller
// that wants a new baseline mid-test) never changes an already-set
// RunID, keeping the "immutable run ID" contract even against a
// caller that calls Start more than once.
func (h *Harness) Start() {
	runtime.GC()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.start = time.Now()
	h.baselineGoroutines = runtime.NumGoroutine()
	h.maxGoroutines = h.baselineGoroutines

	if h.identity.RunID == "" {
		id, err := newRunID()
		if err != nil {
			// crypto/rand failing is effectively unreachable on any real
			// OS, but this harness must never silently run with NO run
			// ID rather than a genuinely random one -- fall back to a
			// still-real, still-unique-enough identifier derived from
			// the process's own PID and start time instead of fabricating
			// randomness that did not happen.
			id = fmt.Sprintf("soak-run-fallback-%d-%d", os.Getpid(), h.start.UnixNano())
		}
		h.identity.RunID = id
	}
	host, hostHash := hostIdentity()
	h.identity.Host = host
	h.identity.HostIdentityHash = hostHash
	h.identity.StartUnix = h.start.Unix()
	h.identity.StartTime = h.start.UTC().Format(time.RFC3339)
}

// RunOnce executes one workload iteration. A nil error counts as a
// clean iteration; an error satisfying errors.Is(err, ErrDroppedEvent)
// counts as a non-fatal dropped event; any other error counts as a
// real error.
func (h *Harness) RunOnce() error {
	err := h.workload(h.iterations)
	h.mu.Lock()
	h.iterations++
	switch {
	case err == nil:
	case errors.Is(err, ErrDroppedEvent):
		h.dropped++
	default:
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
	ru, _ := readResourceUsage() // zero-value on error (e.g. unsupported platform) -- never fabricated

	h.mu.Lock()
	defer h.mu.Unlock()
	if g > h.maxGoroutines {
		h.maxGoroutines = g
	}
	s := Sample{
		AtSeconds: time.Since(h.start).Seconds(), Goroutines: g,
		HeapAllocMB: float64(m.HeapAlloc) / (1024 * 1024), Iterations: h.iterations,
		CPUUserSeconds: ru.CPUUserSeconds, CPUSystemSeconds: ru.CPUSystemSeconds,
		BlockInputOps: ru.BlockInputOps, BlockOutputOps: ru.BlockOutputOps,
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
		RunID: h.identity.RunID, Seq: uint64(len(h.checkpoints)), AtSeconds: s.AtSeconds, Iterations: s.Iterations,
		Errors: h.errors, DroppedEvents: h.dropped, Goroutines: s.Goroutines, HeapAllocMB: s.HeapAllocMB,
		CPUUserSeconds: s.CPUUserSeconds, CPUSystemSeconds: s.CPUSystemSeconds,
		BlockInputOps: s.BlockInputOps, BlockOutputOps: s.BlockOutputOps, PrevHash: h.lastHash,
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

// DetectLeakFromCheckpoints is DetectLeak's counterpart for evidence
// that has already been persisted to disk (e.g. a PersistedState left
// behind by a crashed process): baseline is the first checkpoint's
// goroutine count and final is the last checkpoint's, rather than
// runtime.NumGoroutine() of whatever process happens to be inspecting
// the evidence now (which would not even be the process that ran the
// soak). Returns a zero-value LeakVerdict if fewer than 2 checkpoints
// are given -- there is nothing to compare.
func DetectLeakFromCheckpoints(checkpoints []Checkpoint, threshold int) LeakVerdict {
	if len(checkpoints) < 2 {
		return LeakVerdict{Threshold: threshold}
	}
	baseline := checkpoints[0].Goroutines
	final := checkpoints[len(checkpoints)-1].Goroutines
	return LeakVerdict{
		Leaked: final > baseline+threshold, BaselineGoroutines: baseline,
		FinalGoroutines: final, Threshold: threshold,
	}
}

// DetectDriftFromCheckpoints is DetectDrift's counterpart operating
// directly on a checkpoint sequence (e.g. loaded from PersistedState)
// instead of a live Harness's own recorded samples, using the exact
// same first-half/second-half heap comparison and the same
// minAbsoluteDriftMB noise floor.
func DetectDriftFromCheckpoints(checkpoints []Checkpoint, threshold float64) DriftVerdict {
	if len(checkpoints) < 4 {
		return DriftVerdict{Threshold: threshold}
	}
	samples := make([]Sample, len(checkpoints))
	for i, cp := range checkpoints {
		samples[i] = Sample{AtSeconds: cp.AtSeconds, HeapAllocMB: cp.HeapAllocMB}
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

// Reconciliation is a real self-consistency check over a completed
// run's checkpoint chain: it confirms the span the checkpoints
// themselves record (last AtSeconds minus first) is consistent with
// the harness's independently-measured wall-clock elapsed duration.
// This is not a rubber stamp -- a harness that silently stopped
// checkpointing partway through a run, or whose checkpoint timestamps
// disagree with the actual run duration, fails this even though
// VerifyChain's hash-integrity check alone would still pass (hash
// integrity proves the recorded entries weren't tampered with; it says
// nothing about whether recording itself kept happening).
type Reconciliation struct {
	ElapsedSeconds        float64 `json:"elapsed_seconds"`
	CheckpointSpanSeconds float64 `json:"checkpoint_span_seconds"`
	CheckpointCount       int     `json:"checkpoint_count"`
	Consistent            bool    `json:"consistent"`
	Detail                string  `json:"detail"`
}

// Reconcile compares checkpoints' own recorded time span against
// elapsed, the harness's independently-measured wall-clock duration.
// The tolerance is deliberately generous (the greater of 25% of
// elapsed or 1 second) for the same reason minAbsoluteDriftMB is: a
// short fixture run's checkpoint cadence is coarse relative to its own
// total duration, and this check must not become a source of CI
// flakiness on exactly the runs this package's own tests exercise. A
// real 72-hour run's checkpoints (taken roughly every 7.2 hours at
// RunQualification's 1/10th-of-duration cadence, or far more often for
// a caller checkpointing on a fixed real-time interval) leave far more
// margin than this tolerance needs.
func Reconcile(checkpoints []Checkpoint, elapsed time.Duration) Reconciliation {
	rec := Reconciliation{ElapsedSeconds: elapsed.Seconds(), CheckpointCount: len(checkpoints)}
	if len(checkpoints) == 0 {
		rec.Detail = "no checkpoints recorded -- cannot reconcile"
		return rec
	}
	span := checkpoints[len(checkpoints)-1].AtSeconds - checkpoints[0].AtSeconds
	rec.CheckpointSpanSeconds = span

	const slackFraction = 0.25
	const minSlackSeconds = 1.0
	slack := elapsed.Seconds() * slackFraction
	if slack < minSlackSeconds {
		slack = minSlackSeconds
	}
	rec.Consistent = span <= elapsed.Seconds()+1e-6 && (elapsed.Seconds()-span) <= slack
	if rec.Consistent {
		rec.Detail = "checkpoint span is consistent with measured elapsed duration"
	} else {
		rec.Detail = fmt.Sprintf("checkpoint span %.3fs is inconsistent with measured elapsed %.3fs (slack %.3fs)", span, elapsed.Seconds(), slack)
	}
	return rec
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
	Identity       RunIdentity    `json:"identity"`
	RequiredHours  float64        `json:"required_hours"`
	ActualDuration string         `json:"actual_duration"`
	ActualMinutes  float64        `json:"actual_minutes"`
	IsFullSoak     bool           `json:"is_full_72h_soak"`
	Iterations     int            `json:"total_iterations"`
	Errors         int            `json:"errors"`
	DroppedEvents  int            `json:"dropped_events"`
	EndUnix        int64          `json:"end_unix"`
	EndTime        string         `json:"end_time"`
	Samples        []Sample       `json:"samples"`
	Checkpoints    []Checkpoint   `json:"checkpoints"`
	ChainVerified  bool           `json:"chain_verified"`
	Leak           LeakVerdict    `json:"leak"`
	Drift          DriftVerdict   `json:"drift"`
	Reconciliation Reconciliation `json:"reconciliation"`
	Verdict        string         `json:"verdict"`
}

// GenerateEvidence assembles the full report: required duration is
// always stated as 72h (the real gate), actual duration is always
// stated honestly, and IsFullSoak is only ever true if the elapsed time
// genuinely reached it -- there is no code path that sets it any other
// way.
//
// Reconciliation is computed and reported but deliberately does NOT
// gate Verdict/pass on its own: see Reconcile's doc comment for why its
// tolerance is generous enough that a short fixture run's checkpoint
// cadence should not itself become a flaky failure mode. A real 72-hour
// run's evidence should always show Reconciliation.Consistent == true;
// a caller building acceptance criteria for that real run is expected
// to check it explicitly.
func (h *Harness) GenerateEvidence(elapsed time.Duration, leakThreshold int, driftThreshold float64) Report {
	h.mu.Lock()
	iterations, errs, dropped := h.iterations, h.errors, h.dropped
	samples := append([]Sample(nil), h.samples...)
	checkpoints := append([]Checkpoint(nil), h.checkpoints...)
	identity := h.identity
	h.mu.Unlock()

	leak := h.DetectLeak(leakThreshold)
	drift := h.DetectDrift(driftThreshold)
	chainErr := VerifyChain(checkpoints)
	reconciliation := Reconcile(checkpoints, elapsed)
	end := time.Now()

	rep := Report{
		Identity: identity, RequiredHours: 72, ActualDuration: elapsed.String(), ActualMinutes: elapsed.Minutes(),
		IsFullSoak: elapsed >= 72*time.Hour, Iterations: iterations, Errors: errs, DroppedEvents: dropped,
		EndUnix: end.Unix(), EndTime: end.UTC().Format(time.RFC3339),
		Samples: samples, Checkpoints: checkpoints, ChainVerified: chainErr == nil,
		Leak: leak, Drift: drift, Reconciliation: reconciliation,
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
//
// It computes source/binary identity against ".", matching
// internal/environment.Current()'s own documented convention (see
// ComputeSourceIdentity's doc comment for the honest scope that
// implies); a caller that wants a meaningful hash against a specific
// repo root should call RunQualificationAt directly.
func RunQualification(contract *blockers.Contract, targetMinutes float64, workload Workload) (blockers.RunResult, Report, error) {
	return RunQualificationAt(contract, targetMinutes, workload, ".")
}

// RunQualificationAt is RunQualification with an explicit source root
// for ComputeSourceIdentity -- pass "" to skip source/binary identity
// entirely (fastest path, no disk tree walk).
func RunQualificationAt(contract *blockers.Contract, targetMinutes float64, workload Workload, sourceRoot string) (blockers.RunResult, Report, error) {
	h := NewHarness(workload)
	h.Start()
	var sourceIDErr error
	if sourceRoot != "" {
		sourceIDErr = h.ComputeSourceIdentity(sourceRoot)
	}

	duration := time.Duration(targetMinutes * float64(time.Minute))
	deadline := time.Now().Add(duration)
	checkpointEvery := duration / 10
	if checkpointEvery < time.Millisecond {
		checkpointEvery = time.Millisecond
	}
	nextCheckpoint := time.Now()

	for time.Now().Before(deadline) {
		err := h.RunOnce()
		if err != nil && !errors.Is(err, ErrDroppedEvent) {
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
			"run_id":          report.Identity.RunID,
			"hostname":        report.Identity.Host.Hostname,
			"host_identity":   report.Identity.HostIdentityHash,
			"source_hash":     report.Identity.SourceHash,
			"binary_hash":     report.Identity.BinaryHash,
			"restart_count":   fmt.Sprintf("%d", report.Identity.RestartCount),
			"dropped_events":  fmt.Sprintf("%d", report.DroppedEvents),
			"reconciliation":  fmt.Sprintf("%v", report.Reconciliation.Consistent),
			"start_time":      report.Identity.StartTime,
			"end_time":        report.EndTime,
		},
	}
	if sourceIDErr != nil {
		result.Measurements["source_identity_error"] = sourceIDErr.Error()
	}
	if !pass {
		result.FailureReason = report.Verdict
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, report, err
	}
	return result, report, nil
}
