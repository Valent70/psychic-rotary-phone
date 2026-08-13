// Package soak is the harness for the audit's soak_72h gate: sustained
// mixed workload (repeated executions through the full DAG — evidence,
// dependency, truth arbitration, fusion, risk, decision, explanation,
// replay, certificate) with periodic resource sampling, producing a
// real evidence/soak-report.json rather than a claim.
//
// No environment available to this session can honestly run this for
// 72 continuous hours: the container this session runs in is reclaimed
// after a period of inactivity, and holding a session open that long
// just to poll a counter is not what "efficient" means either.
// VERIQO_SOAK_MINUTES controls the run length: unset or 0 runs a
// two-minute smoke pass (enough to prove the harness and gate work and
// to catch a gross leak, not enough to satisfy the gate); an operator
// with a genuinely long-lived host sets it to 4320 (72h) and the exact
// same test, unchanged, produces the qualifying evidence. The report
// always states its own actual duration, so a two-minute smoke run can
// never be mistaken for a 72-hour soak downstream.
package soak

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/economic"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

type sample struct {
	AtSeconds   float64 `json:"at_seconds"`
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	Iterations  int     `json:"iterations_so_far"`
}

type report struct {
	RequiredHours      float64  `json:"required_hours"`
	ActualDuration     string   `json:"actual_duration"`
	ActualMinutes      float64  `json:"actual_minutes"`
	IsFullSoak         bool     `json:"is_full_72h_soak"`
	Iterations         int      `json:"total_iterations"`
	Errors             int      `json:"errors"`
	BeforeStateHash    string   `json:"before_state_hash"`
	AfterStateHash     string   `json:"after_state_hash"`
	BaselineGoroutines int      `json:"baseline_goroutines"`
	FinalGoroutines    int      `json:"final_goroutines"`
	MaxGoroutines      int      `json:"max_goroutines"`
	Samples            []sample `json:"samples"`
	Verdict            string   `json:"verdict"`
}

func caseFor(i int) canonical.CaseInput {
	return canonical.CaseInput{
		Entity:  digitaltwin.EntityID(fmt.Sprintf("IMO%07d", 9000000+i%999999)),
		Subject: fmt.Sprintf("IMO%07d", 9000000+i%999999), Predicate: "port_call",
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "singapore", BaseReliability: 0.8, Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "singapore", BaseReliability: 0.8, Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("port-authority"), Value: "singapore", BaseReliability: 0.9, Provider: "port", UpstreamID: "feed-port"},
		},
		Policy: risk.DefaultPolicy(), PatternScore: 0.6, PriceAnomaly: 0.5,
		EconomicChannels: map[string]float64{"freight": 500_000}, Tick: uint64(i),
	}
}

func scenariosFor() []economic.Scenario {
	return []economic.Scenario{
		{ID: "clean", Probability: 0.9, Outcome: 50_000, EvidenceRefs: []string{"e1"}},
		{ID: "detained", Probability: 0.1, Outcome: -200_000, EvidenceRefs: []string{"e2"}},
	}
}

// TestSoak is the permanent soak gate. See the package doc comment for
// how its duration is controlled.
func TestSoak(t *testing.T) {
	minutes := 2.0
	if v := os.Getenv("VERIQO_SOAK_MINUTES"); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("VERIQO_SOAK_MINUTES=%q is not a number: %v", v, err)
		}
		minutes = parsed
	}
	duration := time.Duration(minutes * float64(time.Minute))

	runtime.GC()
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)
	baselineGoroutines := runtime.NumGoroutine()

	engine := execution.NewEngine(nil)
	first, err := engine.Run(execution.Input{Context: ctxFor(0), Case: caseFor(0), Scenarios: scenariosFor(), Currency: "USD"})
	if err != nil {
		t.Fatalf("soak: initial run failed: %v", err)
	}
	beforeHash := first.ExecutionRootHash

	start := time.Now()
	deadline := start.Add(duration)
	iterations, errs, maxGoroutines := 0, 0, baselineGoroutines
	var samples []sample
	nextSampleAt := start
	var last execution.Result

	for time.Now().Before(deadline) {
		res, err := engine.Run(execution.Input{
			Context: ctxFor(iterations + 1), Case: caseFor(iterations + 1),
			Scenarios: scenariosFor(), Currency: "USD",
		})
		iterations++
		if err != nil {
			errs++
		} else {
			last = *res
		}

		if time.Now().After(nextSampleAt) {
			g := runtime.NumGoroutine()
			if g > maxGoroutines {
				maxGoroutines = g
			}
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			samples = append(samples, sample{
				AtSeconds: time.Since(start).Seconds(), Goroutines: g,
				HeapAllocMB: float64(m.HeapAlloc) / (1024 * 1024), Iterations: iterations,
			})
			nextSampleAt = nextSampleAt.Add(sampleInterval(duration))
		}
	}

	runtime.GC()
	finalGoroutines := runtime.NumGoroutine()
	actual := time.Since(start)

	rep := report{
		RequiredHours: 72, ActualDuration: actual.String(), ActualMinutes: actual.Minutes(),
		IsFullSoak: actual >= 72*time.Hour,
		Iterations: iterations, Errors: errs,
		BeforeStateHash: beforeHash, AfterStateHash: last.ExecutionRootHash,
		BaselineGoroutines: baselineGoroutines, FinalGoroutines: finalGoroutines, MaxGoroutines: maxGoroutines,
		Samples: samples,
	}
	switch {
	case errs > 0:
		rep.Verdict = fmt.Sprintf("FAIL: %d/%d iterations errored", errs, iterations)
	case finalGoroutines > baselineGoroutines+50:
		rep.Verdict = fmt.Sprintf("FAIL: goroutine count grew from %d to %d (leak suspected)", baselineGoroutines, finalGoroutines)
	case rep.IsFullSoak:
		rep.Verdict = "PASS: full 72h soak, zero errors, goroutines bounded"
	default:
		rep.Verdict = fmt.Sprintf("PARTIAL: %.1f of the required 72 hours (%.2f%%) -- zero errors, goroutines bounded over this window, but this is NOT a closure of soak_72h", actual.Hours(), actual.Hours()/72*100)
	}

	if errs > 0 {
		t.Fatalf("soak: %d/%d iterations errored", errs, iterations)
	}
	if finalGoroutines > baselineGoroutines+50 {
		t.Fatalf("soak: goroutine count grew from %d to %d over %s — possible leak", baselineGoroutines, finalGoroutines, actual)
	}

	if raw, err := json.MarshalIndent(rep, "", "  "); err == nil {
		_ = os.MkdirAll("../../evidence", 0o755)
		_ = os.WriteFile(filepath.Join("../../evidence", "soak-report.json"), raw, 0o644)
	}
	t.Logf("soak: %s", rep.Verdict)
}

func ctxFor(i int) execution.Context {
	return execution.Context{
		ExecutionID: fmt.Sprintf("soak-exec-%d", i), CaseID: fmt.Sprintf("soak-case-%d", i),
		Tenant: "soak", Actor: "soak-harness", PolicyVersion: "sanctions-screen@3",
		EvidencePackageID: fmt.Sprintf("soak-evp-%d", i), IdentityResolutionVersion: "ident@2",
		LedgerPosition: uint64(i), Tick: uint64(i), ReplayMetadata: "soak",
	}
}

// sampleInterval keeps the report to roughly 60 samples regardless of
// total duration, whether that's a 2-minute smoke run or a 72-hour
// soak.
func sampleInterval(total time.Duration) time.Duration {
	iv := total / 60
	if iv < time.Second {
		return time.Second
	}
	return iv
}
