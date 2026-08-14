// Package stress closes V7.11.1 P0-3 and P0-5.
//
// The audit asked for a stress layer that produces throughput, p50, p95,
// p99, error rate, memory and goroutine counts — and that FAILS rather
// than warns when an agreed SLO is missed. It also asked that the
// 100-transfer scenario produce evidence 100/100, not merely a test file
// that exists.
//
// Two deliberate choices:
//
//   - The SLOs are declared as data in slo.go-style structs below, so
//     "what did we promise" is answerable without reading assertions.
//   - Latency is measured per operation and reported as real order
//     statistics. A mean would hide exactly the tail these numbers exist
//     to expose.
//
// These are LOCAL numbers on one machine. They are honest about that:
// the 100-node / 1M-evidence scale target remains BLOCKED_EXTERNAL and
// nothing here claims otherwise.
package stress

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

// SLO is the agreed performance contract for a scenario.
type SLO struct {
	Name             string  `json:"name"`
	MinThroughputOps float64 `json:"min_throughput_ops_per_sec"`
	MaxP50MS         float64 `json:"max_p50_ms"`
	MaxP95MS         float64 `json:"max_p95_ms"`
	MaxP99MS         float64 `json:"max_p99_ms"`
	MaxErrorRate     float64 `json:"max_error_rate"`
	MaxGoroutineLeak int     `json:"max_goroutine_leak"`
	MaxHeapGrowthMB  float64 `json:"max_heap_growth_mb"`
}

// Result is the measured outcome.
type Result struct {
	SLO              SLO      `json:"slo"`
	Operations       int      `json:"operations"`
	Errors           int      `json:"errors"`
	ErrorRate        float64  `json:"error_rate"`
	ThroughputOps    float64  `json:"throughput_ops_per_sec"`
	P50MS            float64  `json:"p50_ms"`
	P95MS            float64  `json:"p95_ms"`
	P99MS            float64  `json:"p99_ms"`
	MaxMS            float64  `json:"max_ms"`
	GoroutinesBefore int      `json:"goroutines_before"`
	GoroutinesAfter  int      `json:"goroutines_after"`
	HeapGrowthMB     float64  `json:"heap_growth_mb"`
	EvidenceProduced int      `json:"evidence_produced"`
	Breaches         []string `json:"breaches"`
	Passed           bool     `json:"passed"`
}

// percentile returns the p-th percentile of a sorted duration slice
// using nearest-rank, which is the definition that does not invent a
// value the system never produced.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p/100*float64(len(sorted)) + 0.5)
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

func (r *Result) evaluate() {
	if r.ThroughputOps < r.SLO.MinThroughputOps {
		r.Breaches = append(r.Breaches, "throughput below SLO")
	}
	if r.SLO.MaxP50MS > 0 && r.P50MS > r.SLO.MaxP50MS {
		r.Breaches = append(r.Breaches, "p50 above SLO")
	}
	if r.SLO.MaxP95MS > 0 && r.P95MS > r.SLO.MaxP95MS {
		r.Breaches = append(r.Breaches, "p95 above SLO")
	}
	if r.SLO.MaxP99MS > 0 && r.P99MS > r.SLO.MaxP99MS {
		r.Breaches = append(r.Breaches, "p99 above SLO")
	}
	if r.ErrorRate > r.SLO.MaxErrorRate {
		r.Breaches = append(r.Breaches, "error rate above SLO")
	}
	if leak := r.GoroutinesAfter - r.GoroutinesBefore; leak > r.SLO.MaxGoroutineLeak {
		r.Breaches = append(r.Breaches, "goroutine leak")
	}
	if r.SLO.MaxHeapGrowthMB > 0 && r.HeapGrowthMB > r.SLO.MaxHeapGrowthMB {
		r.Breaches = append(r.Breaches, "heap growth above SLO")
	}
	r.Passed = len(r.Breaches) == 0
}

func writeEvidence(t *testing.T, name string, r Result) {
	t.Helper()
	dir := filepath.Join("..", "..", "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("evidence dir: %v", err)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
		t.Logf("evidence write: %v", err)
	}
}

func caseInput(i int, tick uint64) canonical.CaseInput {
	subject := "IMO" + itoa(9000000+i)
	return canonical.CaseInput{
		Entity: digitaltwin.EntityID(subject), Subject: subject, Predicate: "port_call",
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-a"), Value: "singapore", BaseReliability: 0.8,
				Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("ais-b"), Value: "singapore", BaseReliability: 0.8,
				Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("port"), Value: "singapore", BaseReliability: 0.9,
				Provider: "port", UpstreamID: "feed-port"},
			{SourceID: fusion.SourceID("broker"), Value: "jakarta", BaseReliability: 0.5,
				Provider: "broker", Dependencies: []evidencegraph.DependencyRecord{
					{UpstreamID: evidencegraph.NodeID("model-v3"),
						Kind: evidencegraph.DependsOnTransformation, Confidence: 0.6}}},
		},
		Policy: risk.DefaultPolicy(), PatternScore: 0.7, PriceAnomaly: 0.9,
		EconomicChannels: map[string]float64{"freight": 1_000_000},
		Tick:             tick,
	}
}

func execContext(i int, tick uint64) execution.Context {
	return execution.Context{ExecutionID: "stress-" + itoa(i), CaseID: "case-" + itoa(i),
		Tenant: "acme", Actor: "stress", PolicyVersion: "sanctions-screen@3",
		EvidencePackageID: "evp-" + itoa(i), IdentityResolutionVersion: "ident@2", Tick: tick}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestOneHundredTransfersProduceOneHundredEvidenceArtifacts closes P0-5.
//
// The assertion the audit demanded is exact: 100 transfers must yield
// 100 complete evidence artifacts — execution root, decision,
// explanation, replay package and verification certificate — not 100
// attempts of which most succeeded.
func TestOneHundredTransfersProduceOneHundredEvidenceArtifacts(t *testing.T) {
	const n = 100
	slo := SLO{Name: "100_transfer_evidence", MinThroughputOps: 5,
		MaxP95MS: 500, MaxP99MS: 1000, MaxErrorRate: 0, MaxGoroutineLeak: 4,
		MaxHeapGrowthMB: 512}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	gBefore := runtime.NumGoroutine()

	latencies := make([]float64, 0, n)
	complete := 0
	errs := 0
	start := time.Now()

	for i := 0; i < n; i++ {
		e := execution.NewEngine(nil)
		opStart := time.Now()
		res, err := e.Run(context.Background(), execution.Input{Context: execContext(i, uint64(i+1)),
			Case: caseInput(i, uint64(i+1))})
		latencies = append(latencies, float64(time.Since(opStart).Microseconds())/1000.0)
		if err != nil {
			errs++
			continue
		}
		// "Evidence produced" means every mandatory artifact is present
		// AND the certificate says the independent replay matched. A
		// certificate that reports divergence is not evidence.
		if res.ExecutionRootHash != "" && res.Decision != "" &&
			res.Explanation.Hash != "" && res.ReplayPackage.ReplayPackageID != "" &&
			res.Certificate.VerificationCertificateID != "" && res.Certificate.Match {
			complete++
		}
	}
	elapsed := time.Since(start)

	runtime.GC()
	runtime.ReadMemStats(&after)
	sort.Float64s(latencies)

	r := Result{SLO: slo, Operations: n, Errors: errs,
		ErrorRate:        float64(errs) / float64(n),
		ThroughputOps:    float64(n) / elapsed.Seconds(),
		P50MS:            percentile(latencies, 50),
		P95MS:            percentile(latencies, 95),
		P99MS:            percentile(latencies, 99),
		MaxMS:            latencies[len(latencies)-1],
		GoroutinesBefore: gBefore, GoroutinesAfter: runtime.NumGoroutine(),
		HeapGrowthMB:     float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / (1 << 20),
		EvidenceProduced: complete}
	r.evaluate()
	writeEvidence(t, "stress-100-transfers.json", r)

	if complete != n {
		t.Fatalf("evidence must be %d/%d, got %d/%d", n, n, complete, n)
	}
	if !r.Passed {
		t.Fatalf("SLO breached: %v (throughput %.1f/s p50=%.1fms p95=%.1fms p99=%.1fms err=%.3f)",
			r.Breaches, r.ThroughputOps, r.P50MS, r.P95MS, r.P99MS, r.ErrorRate)
	}
	t.Logf("100/100 evidence; %.1f ops/s p50=%.1fms p95=%.1fms p99=%.1fms heap+%.1fMB",
		r.ThroughputOps, r.P50MS, r.P95MS, r.P99MS, r.HeapGrowthMB)
}

// TestSustainedThroughputMeetsSLO closes P0-3 for the sustained path.
func TestSustainedThroughputMeetsSLO(t *testing.T) {
	if testing.Short() {
		t.Skip("sustained throughput is not part of the short suite")
	}
	if raceEnabled {
		// Correctness still runs under -race everywhere else; only the
		// throughput ASSERTION is suspended, and it says so out loud
		// rather than quietly lowering the number.
		t.Skip("throughput SLOs are stated for an uninstrumented build; skipped under -race")
	}
	const n = 400
	slo := SLO{Name: "sustained_execution", MinThroughputOps: 10,
		MaxP50MS: 200, MaxP95MS: 400, MaxP99MS: 800, MaxErrorRate: 0,
		MaxGoroutineLeak: 4, MaxHeapGrowthMB: 1024}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	gBefore := runtime.NumGoroutine()

	// One engine reused across the whole run: this exercises accumulated
	// ledger state, which is where a throughput regression would actually
	// appear. A fresh engine per operation would measure nothing but
	// allocation.
	e := execution.NewEngine(nil)
	latencies := make([]float64, 0, n)
	errs := 0
	start := time.Now()
	for i := 0; i < n; i++ {
		opStart := time.Now()
		if _, err := e.Run(context.Background(), execution.Input{Context: execContext(i, uint64(i+1)),
			Case: caseInput(i, uint64(i+1))}); err != nil {
			errs++
		}
		latencies = append(latencies, float64(time.Since(opStart).Microseconds())/1000.0)
	}
	elapsed := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	sort.Float64s(latencies)

	r := Result{SLO: slo, Operations: n, Errors: errs,
		ErrorRate:        float64(errs) / float64(n),
		ThroughputOps:    float64(n) / elapsed.Seconds(),
		P50MS:            percentile(latencies, 50),
		P95MS:            percentile(latencies, 95),
		P99MS:            percentile(latencies, 99),
		MaxMS:            latencies[len(latencies)-1],
		GoroutinesBefore: gBefore, GoroutinesAfter: runtime.NumGoroutine(),
		HeapGrowthMB: float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / (1 << 20)}
	r.evaluate()
	writeEvidence(t, "stress-sustained-throughput.json", r)

	if !r.Passed {
		t.Fatalf("SLO breached: %v (throughput %.1f/s p50=%.1fms p95=%.1fms p99=%.1fms)",
			r.Breaches, r.ThroughputOps, r.P50MS, r.P95MS, r.P99MS)
	}
	t.Logf("%.1f ops/s p50=%.1fms p95=%.1fms p99=%.1fms goroutines %d->%d heap+%.1fMB",
		r.ThroughputOps, r.P50MS, r.P95MS, r.P99MS, r.GoroutinesBefore, r.GoroutinesAfter,
		r.HeapGrowthMB)
}

// TestSLOEvaluationFailsWhenItShould proves the gate can fail. A
// threshold check that has never been observed failing is not a gate.
func TestSLOEvaluationFailsWhenItShould(t *testing.T) {
	r := Result{SLO: SLO{MinThroughputOps: 100, MaxP99MS: 10, MaxErrorRate: 0},
		ThroughputOps: 5, P99MS: 900, ErrorRate: 0.2,
		GoroutinesBefore: 1, GoroutinesAfter: 50}
	r.evaluate()
	if r.Passed {
		t.Fatal("a result breaching every SLO must not pass")
	}
	if len(r.Breaches) < 4 {
		t.Fatalf("every breach must be named individually: %v", r.Breaches)
	}
}

// TestPercentileIsNearestRank pins the definition so a future refactor
// cannot quietly switch to an interpolating percentile that reports a
// latency the system never actually produced.
func TestPercentileIsNearestRank(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(s, 50); got != 5 {
		t.Fatalf("p50 must be an observed value, got %v", got)
	}
	if got := percentile(s, 99); got != 10 {
		t.Fatalf("p99 must be an observed value, got %v", got)
	}
	if got := percentile(nil, 95); got != 0 {
		t.Fatalf("an empty sample must not fabricate a latency, got %v", got)
	}
}
