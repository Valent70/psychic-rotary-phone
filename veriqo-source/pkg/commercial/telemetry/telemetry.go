// Package telemetry is pkg/commercial/api.Store's operational counters
// for Commercialization Sprint item 20 ("Observability"): exactly the
// nine metrics that item names by identifier -- evidence_ingestion_total,
// evidence_verification_failures, custody_chain_failures,
// decision_latency, authorization_denials, action_failures,
// ledger_commit_failures, replay_failures, external_adapter_failures.
//
// Every counter here is incremented from a real branch in
// pkg/commercial/api.Store's own methods (see that package's source for
// exactly which branch increments which counter) -- there is no
// synthetic or simulated value. external_adapter_failures is the one
// counter this reference build never increments: item 11's real
// insurer/adjuster/P&I/AIS integrations are explicitly named as external
// pilot integrations, not built in this engagement, so that counter
// always reads zero here and is reserved for whichever adapter package
// wires a real external call in.
//
// This package deliberately does NOT export a Prometheus (or any other
// wire-format) exposition helper -- Snapshot's JSON struct tags are the
// full item-20 contract, and a real deployment can format that however
// its own metrics backend requires. Building a specific exporter without
// a named backend to target would be exactly the "polishing unneeded
// internal APIs" item 12 tells this engagement to stop doing.
package telemetry

import (
	"sync/atomic"
	"time"
)

// Metrics holds every counter as a lock-free atomic so instrumentation
// never contends with pkg/commercial/api.Store's own mutex.
type Metrics struct {
	evidenceIngestionTotal       atomic.Int64
	evidenceVerificationFailures atomic.Int64
	custodyChainFailures         atomic.Int64
	decisionLatencyTotalNanos    atomic.Int64
	decisionCount                atomic.Int64
	authorizationDenials         atomic.Int64
	actionFailures               atomic.Int64
	ledgerCommitFailures         atomic.Int64
	replayFailures               atomic.Int64
	externalAdapterFailures      atomic.Int64
}

// New returns a zeroed Metrics, ready to be shared (by pointer) across
// every method call a Store handles.
func New() *Metrics { return &Metrics{} }

func (m *Metrics) IncEvidenceIngestion()        { m.evidenceIngestionTotal.Add(1) }
func (m *Metrics) IncEvidenceVerificationFail() { m.evidenceVerificationFailures.Add(1) }
func (m *Metrics) IncCustodyChainFailure()      { m.custodyChainFailures.Add(1) }
func (m *Metrics) IncAuthorizationDenial()      { m.authorizationDenials.Add(1) }
func (m *Metrics) IncActionFailure()            { m.actionFailures.Add(1) }
func (m *Metrics) IncLedgerCommitFailure()      { m.ledgerCommitFailures.Add(1) }
func (m *Metrics) IncReplayFailure()            { m.replayFailures.Add(1) }
func (m *Metrics) IncExternalAdapterFailure()   { m.externalAdapterFailures.Add(1) }

// RecordDecisionLatency records one successfully-completed DecideCase
// call's wall-clock duration into the running total this Snapshot's
// decision_latency_avg_millis divides down from.
func (m *Metrics) RecordDecisionLatency(d time.Duration) {
	m.decisionLatencyTotalNanos.Add(int64(d))
	m.decisionCount.Add(1)
}

// Snapshot is item 20's named metrics, exactly as that item's own
// identifiers read, as a point-in-time read of every counter.
type Snapshot struct {
	EvidenceIngestionTotal       int64   `json:"evidence_ingestion_total"`
	EvidenceVerificationFailures int64   `json:"evidence_verification_failures"`
	CustodyChainFailures         int64   `json:"custody_chain_failures"`
	DecisionLatencyAvgMillis     float64 `json:"decision_latency_avg_millis"`
	DecisionCount                int64   `json:"decision_count"`
	AuthorizationDenials         int64   `json:"authorization_denials"`
	ActionFailures               int64   `json:"action_failures"`
	LedgerCommitFailures         int64   `json:"ledger_commit_failures"`
	ReplayFailures               int64   `json:"replay_failures"`
	ExternalAdapterFailures      int64   `json:"external_adapter_failures"`
}

// Snapshot returns a consistent-enough point-in-time read (each field is
// its own atomic load; this is an operational metrics read, not a
// financial or trust-authority computation, so cross-field atomicity is
// not required).
func (m *Metrics) Snapshot() Snapshot {
	s := Snapshot{
		EvidenceIngestionTotal:       m.evidenceIngestionTotal.Load(),
		EvidenceVerificationFailures: m.evidenceVerificationFailures.Load(),
		CustodyChainFailures:         m.custodyChainFailures.Load(),
		DecisionCount:                m.decisionCount.Load(),
		AuthorizationDenials:         m.authorizationDenials.Load(),
		ActionFailures:               m.actionFailures.Load(),
		LedgerCommitFailures:         m.ledgerCommitFailures.Load(),
		ReplayFailures:               m.replayFailures.Load(),
		ExternalAdapterFailures:      m.externalAdapterFailures.Load(),
	}
	if s.DecisionCount > 0 {
		totalNanos := m.decisionLatencyTotalNanos.Load()
		s.DecisionLatencyAvgMillis = float64(totalNanos) / float64(s.DecisionCount) / float64(time.Millisecond)
	}
	return s
}
