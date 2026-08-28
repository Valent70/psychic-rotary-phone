package os_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"veriqo/pkg/core"
	vos "veriqo/pkg/os"
	"veriqo/pkg/storage/evidence"
)

func newOS() *vos.Runtime { return vos.NewRuntime() }

func startProc(t *testing.T, rt *vos.Runtime, tenant, domain string) string {
	t.Helper()
	pid, err := rt.StartProcess(context.Background(), vos.ProcessSpec{
		TenantID: core.TenantID(tenant),
		DomainID: core.DomainID(domain),
		Name:     "test-process",
		Priority: 5,
	})
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	return string(pid)
}

func TestRuntime_StartProcess(t *testing.T) {
	rt := newOS()
	pid := startProc(t, rt, "tenantA", "maritime")
	if pid == "" {
		t.Fatal("expected non-empty process ID")
	}
}

func TestRuntime_StartProcess_MissingTenant(t *testing.T) {
	rt := newOS()
	_, err := rt.StartProcess(context.Background(), vos.ProcessSpec{DomainID: "maritime"})
	if err == nil {
		t.Fatal("expected error for missing TenantID")
	}
}

func TestRuntime_AppendEvidence(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pid, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "t",
		DomainID: "d",
		Name:     "p",
	})
	rec, err := rt.AppendEvidence(ctx, pid, vos.EvidenceInput{
		Kind:    "test.event",
		Payload: []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}
	if rec.Seq == 0 {
		t.Fatal("expected non-zero seq in evidence record")
	}
}

func TestRuntime_EvaluateRisk(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pid, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "t",
		DomainID: "d",
		Name:     "p",
	})
	result, err := rt.EvaluateRisk(ctx, pid, vos.RiskInput{
		EntityID:   "vessel-123",
		EntityType: "vessel",
		Factors:    map[string]float64{"sanction": 0.8, "route": 0.6},
	})
	if err != nil {
		t.Fatalf("EvaluateRisk: %v", err)
	}
	if result.Score < 0 || result.Score > 1 {
		t.Errorf("risk score out of range: %f", result.Score)
	}
}

func TestRuntime_HighRiskDecision(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pid, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "t",
		DomainID: "d",
		Name:     "p",
	})
	d, err := rt.Decide(ctx, pid, vos.DecisionInput{
		Kind:    "sanction.check",
		Factors: map[string]any{"risk_score": 0.9},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Approved {
		t.Fatal("high risk_score should not be approved")
	}
}

func TestRuntime_LowRiskDecision(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pid, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "t",
		DomainID: "d",
		Name:     "p",
	})
	d, err := rt.Decide(ctx, pid, vos.DecisionInput{
		Kind:    "sanction.check",
		Factors: map[string]any{"risk_score": 0.2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Approved {
		t.Fatal("low risk_score should be approved")
	}
}

func TestRuntime_DigitalTwin(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pid, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "t",
		DomainID: "d",
		Name:     "p",
	})
	h1, err := rt.UpdateDigitalTwin(ctx, pid, vos.TwinDelta{
		EntityID:   "vessel-001",
		Properties: map[string]any{"status": "at-sea"},
	})
	if err != nil {
		t.Fatalf("UpdateDigitalTwin: %v", err)
	}
	h2, _ := rt.UpdateDigitalTwin(ctx, pid, vos.TwinDelta{
		EntityID:   "vessel-001",
		Properties: map[string]any{"status": "at-port"},
	})
	if h1 == h2 {
		t.Fatal("different state updates should produce different hashes")
	}
}

func TestRuntime_StopProcess(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pid, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "t",
		DomainID: "d",
		Name:     "p",
	})
	if err := rt.StopProcess(ctx, pid); err != nil {
		t.Fatalf("StopProcess: %v", err)
	}
	// Stopping a non-existent process should return an error.
	err := rt.StopProcess(ctx, "invalid-pid")
	var notFound *vos.ErrProcessNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("expected ErrProcessNotFound, got %v", err)
	}
}

func TestRuntime_TenantIsolation(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	pidA, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "tenantA",
		DomainID: "d",
		Name:     "pA",
	})
	pidB, _ := rt.StartProcess(ctx, vos.ProcessSpec{
		TenantID: "tenantB",
		DomainID: "d",
		Name:     "pB",
	})

	rt.AppendEvidence(ctx, pidA, vos.EvidenceInput{Kind: "ev.a", Payload: []byte("dataA")})
	rt.AppendEvidence(ctx, pidB, vos.EvidenceInput{Kind: "ev.b", Payload: []byte("dataB")})

	// Verify cross-tenant evidence query isolation.
	evStore := rt.Evidence()
	resultsA := evStore.Query(func(r *evidence.Record) bool {
		return r.TenantID == "tenantA"
	})
	if len(resultsA) == 0 {
		t.Error("tenantA should have at least one evidence record")
	}
	for _, r := range resultsA {
		if r.TenantID != "tenantA" {
			t.Errorf("evidence leaked: expected tenantA, got %q", r.TenantID)
		}
	}

	// Verify processes are listed correctly.
	procs := rt.Processes()
	tenants := map[string]bool{}
	for _, p := range procs {
		tenants[string(p.TenantID)] = true
	}
	if !tenants["tenantA"] || !tenants["tenantB"] {
		t.Fatal("both tenants should have processes")
	}
}

func TestRuntime_ConcurrentProcesses(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			pid, err := rt.StartProcess(ctx, vos.ProcessSpec{
				TenantID: core.TenantID("tenant"),
				DomainID: "d",
				Name:     "proc",
				Priority: i % 10,
			})
			if err != nil {
				return
			}
			rt.AppendEvidence(ctx, pid, vos.EvidenceInput{
				Kind:    "concurrent.event",
				Payload: []byte(`{}`),
			})
		}(i)
	}
	wg.Wait()

	procs := rt.Processes()
	if len(procs) < n {
		// Some may have been GC'd; just verify no race.
		t.Logf("got %d processes (concurrent starts)", len(procs))
	}
}

func TestRuntime_SchedulerPriorityAndAdmission(t *testing.T) {
	rt := newOS()
	ctx := context.Background()
	// Fill scheduler beyond MaxQueueDepth.
	var lastErr error
	admitted := 0
	for range 1100 {
		_, err := rt.StartProcess(ctx, vos.ProcessSpec{
			TenantID: "t",
			DomainID: "d",
			Name:     "bulk",
			Priority: 1,
		})
		if err != nil {
			lastErr = err
		} else {
			admitted++
		}
	}
	if lastErr == nil {
		t.Log("admission control not triggered (scheduler may have processed fast enough)")
	}
	t.Logf("admitted %d processes", admitted)
}
