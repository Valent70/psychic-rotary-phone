package telemetry

import (
	"testing"
	"time"
)

func TestZeroValueSnapshotIsAllZero(t *testing.T) {
	m := New()
	s := m.Snapshot()
	if s.EvidenceIngestionTotal != 0 || s.DecisionCount != 0 || s.DecisionLatencyAvgMillis != 0 {
		t.Fatalf("expected an all-zero Snapshot for a fresh Metrics, got %+v", s)
	}
}

func TestEachCounterIncrementsIndependently(t *testing.T) {
	m := New()
	m.IncEvidenceIngestion()
	m.IncEvidenceIngestion()
	m.IncEvidenceVerificationFail()
	m.IncCustodyChainFailure()
	m.IncAuthorizationDenial()
	m.IncActionFailure()
	m.IncLedgerCommitFailure()
	m.IncReplayFailure()
	m.IncExternalAdapterFailure()

	s := m.Snapshot()
	want := Snapshot{
		EvidenceIngestionTotal: 2, EvidenceVerificationFailures: 1, CustodyChainFailures: 1,
		AuthorizationDenials: 1, ActionFailures: 1, LedgerCommitFailures: 1, ReplayFailures: 1,
		ExternalAdapterFailures: 1,
	}
	if s != want {
		t.Fatalf("Snapshot = %+v, want %+v", s, want)
	}
}

func TestRecordDecisionLatencyComputesAnAverage(t *testing.T) {
	m := New()
	m.RecordDecisionLatency(10 * time.Millisecond)
	m.RecordDecisionLatency(30 * time.Millisecond)

	s := m.Snapshot()
	if s.DecisionCount != 2 {
		t.Fatalf("expected DecisionCount=2, got %d", s.DecisionCount)
	}
	if s.DecisionLatencyAvgMillis != 20 {
		t.Fatalf("expected avg=20ms, got %v", s.DecisionLatencyAvgMillis)
	}
}
