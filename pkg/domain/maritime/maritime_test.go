package maritime_test

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/domain/maritime"
	vos "veriqo/pkg/os"
)

func newPipeline() *maritime.Pipeline {
	return maritime.NewPipeline(vos.NewRuntime(), "maritime-tenant")
}

func aisEvent(mmsi string) maritime.AISEvent {
	return maritime.AISEvent{
		MMSI:       mmsi,
		IMO:        "IMO1234567",
		VesselName: "MV Test Vessel",
		VesselType: maritime.VesselTypeTanker,
		Latitude:   27.123,
		Longitude:  56.456,
		Speed:      12.5,
		Heading:    180,
		Timestamp:  time.Now(),
	}
}

func TestPipeline_NormalVessel(t *testing.T) {
	p := newPipeline()
	result, err := p.Process(context.Background(), aisEvent("123456789"), nil)
	if err != nil {
		t.Fatalf("process error: %v", err)
	}
	if result.ProcessID == "" {
		t.Fatal("expected non-empty process ID")
	}
	if result.Decision.Approved != (result.RiskResult.Score < 0.7) {
		t.Error("decision approval should be consistent with risk score")
	}
}

func TestPipeline_SanctionedPortCall(t *testing.T) {
	p := newPipeline()
	event := aisEvent("987654321")
	portCall := &maritime.PortCall{
		CallID:       "CALL-001",
		MMSI:         "987654321",
		PortUNLOCode: "IRBND", // Bandar Abbas — sanctioned
		PortName:     "Bandar Abbas",
		Country:      "IR",
	}
	result, err := p.Process(context.Background(), event, portCall)
	if err != nil {
		t.Fatalf("process error: %v", err)
	}
	// Sanctioned port should raise risk and denial.
	if result.RiskResult.Score < 0.5 {
		t.Errorf("sanctioned port call should raise risk score, got %.2f", result.RiskResult.Score)
	}
	if result.Decision.Approved {
		t.Error("sanctioned port call should not be approved")
	}
}

func TestPipeline_Deterministic(t *testing.T) {
	// Run identical input twice; both digital twin hashes should be equal
	// if the input is the same (determinism test).
	event := maritime.AISEvent{
		MMSI:       "111222333",
		VesselName: "MV Deterministic",
		VesselType: maritime.VesselTypeCargo,
		Latitude:   1.0, Longitude: 1.0, Speed: 10, Heading: 90,
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// Two separate runtimes with same input.
	p1 := maritime.NewPipeline(vos.NewRuntime(), "t1")
	p2 := maritime.NewPipeline(vos.NewRuntime(), "t1")

	r1, err := p1.Process(context.Background(), event, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p2.Process(context.Background(), event, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Risk scores must be identical.
	if r1.RiskResult.Score != r2.RiskResult.Score {
		t.Errorf("non-deterministic risk score: %.4f vs %.4f", r1.RiskResult.Score, r2.RiskResult.Score)
	}
	// Decisions must be identical.
	if r1.Decision.Approved != r2.Decision.Approved {
		t.Error("non-deterministic decision")
	}
}

func TestPipeline_EvidenceRecorded(t *testing.T) {
	rt := vos.NewRuntime()
	p := maritime.NewPipeline(rt, "tenant-evidence")
	_, err := p.Process(context.Background(), aisEvent("999000111"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Multiple evidence records should have been written.
	if rt.Evidence().Len() < 3 {
		t.Errorf("expected at least 3 evidence records, got %d", rt.Evidence().Len())
	}
	// Evidence chain must be intact.
	if err := rt.Evidence().Verify(); err != nil {
		t.Fatalf("evidence chain broken: %v", err)
	}
}

func TestVesselRiskFactors_Sanctioned(t *testing.T) {
	event := aisEvent("000111222")
	portCall := &maritime.PortCall{PortUNLOCode: "IRBND"}
	factors := maritime.VesselRiskFactors(event, portCall)
	if factors["sanctioned_port"] != 1.0 {
		t.Error("sanctioned port should set factor to 1.0")
	}
}

func TestVesselRiskFactors_AISAnomaly(t *testing.T) {
	event := maritime.AISEvent{MMSI: "X", Speed: 0, Heading: 0}
	factors := maritime.VesselRiskFactors(event, nil)
	if factors["ais_anomaly"] == 0 {
		t.Error("AIS anomaly (speed=0, heading=0) should be detected")
	}
}

// ─── Benchmark ───────────────────────────────────────────────────────────────

func BenchmarkPipeline_Process(b *testing.B) {
	p := newPipeline()
	event := aisEvent("bench-mmsi")
	b.ResetTimer()
	for range b.N {
		_, _ = p.Process(context.Background(), event, nil)
	}
}
