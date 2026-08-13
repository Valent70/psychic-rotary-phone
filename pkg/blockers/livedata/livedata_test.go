package livedata

import (
	"context"
	"testing"
	"time"

	"veriqo/pkg/blockers"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "live_data", Name: "Live commercial data feed integration", Version: "v1",
		RequiredCapabilities:      []string{"feed_connector_abstraction", "anti_replay"},
		RealWorldDependencies:     []string{"commercial_data_contracts"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"connector_conformance_report", "dedup_verification"},
		FailureConditions:         []string{"fixture_data_tagged_as_live", "replay_accepted_as_new"},
		AcceptanceCriteria:        []string{"zero_replay_acceptance"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestRunQualificationAcrossFourSources(t *testing.T) {
	ctx := context.Background()
	c := goodContract()
	connectors := []FeedConnector{
		NewSyntheticConnector("SWIFT", 20),
		NewSyntheticConnector("AIS", 20),
		NewSyntheticConnector("SAR", 20),
		NewSyntheticConnector("BoL", 20),
	}
	result, err := RunQualification(ctx, c, connectors)
	if err != nil {
		t.Fatalf("RunQualification: %v (measurements=%v)", err, result.Measurements)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure: %s (measurements=%v)", result.FailureReason, result.Measurements)
	}
	if result.Measurements["first_pass_accepted"] != "80" {
		t.Fatalf("expected 80 accepted records, got %s", result.Measurements["first_pass_accepted"])
	}
	if result.Measurements["replay_rejected"] != "80" {
		t.Fatalf("expected all 80 replayed records rejected, got %s", result.Measurements["replay_rejected"])
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
}

func TestRunQualificationRejectsRealModeConnector(t *testing.T) {
	ctx := context.Background()
	c := goodContract()
	_, err := RunQualification(ctx, c, []FeedConnector{&fakeRealConnector{}})
	if err == nil {
		t.Fatal("RunQualification must refuse a REAL-mode connector")
	}
}

func TestPipelineRejectsFixtureRecordTaggedLive(t *testing.T) {
	p := NewPipeline()
	ts := time.Now()
	rec := Record{ID: "x", Source: "SWIFT", Payload: "p", DataMode: ModeLive, Timestamp: ts}
	rec.Hash = ComputeHash(rec.Source, rec.Payload, rec.Timestamp)
	accepted, reason := p.Ingest(rec, "SIMULATED")
	if accepted {
		t.Fatal("a SIMULATED-mode connector's record tagged LIVE must be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestPipelineRejectsMissingDataMode(t *testing.T) {
	p := NewPipeline()
	ts := time.Now()
	rec := Record{ID: "x", Source: "AIS", Payload: "p", Timestamp: ts}
	rec.Hash = ComputeHash(rec.Source, rec.Payload, rec.Timestamp)
	accepted, _ := p.Ingest(rec, "SIMULATED")
	if accepted {
		t.Fatal("a record with no data_mode tag must be rejected")
	}
}

func TestPipelineRejectsHashMismatch(t *testing.T) {
	p := NewPipeline()
	rec := Record{ID: "x", Source: "AIS", Payload: "p", DataMode: ModeSynthetic, Timestamp: time.Now(), Hash: "not-the-real-hash"}
	accepted, reason := p.Ingest(rec, "SIMULATED")
	if accepted {
		t.Fatal("a record whose hash does not match its content must be rejected")
	}
	if reason != "content hash does not match recomputed hash" {
		t.Fatalf("unexpected rejection reason: %s", reason)
	}
}

func TestPipelineRejectsExactDuplicate(t *testing.T) {
	p := NewPipeline()
	ts := time.Now()
	rec := Record{ID: "x", Source: "AIS", Payload: "p", DataMode: ModeSynthetic, Timestamp: ts}
	rec.Hash = ComputeHash(rec.Source, rec.Payload, rec.Timestamp)
	if accepted, _ := p.Ingest(rec, "SIMULATED"); !accepted {
		t.Fatal("first ingestion of a well-formed record must be accepted")
	}
	if accepted, _ := p.Ingest(rec, "SIMULATED"); accepted {
		t.Fatal("second ingestion of the identical record must be rejected as a duplicate")
	}
}

type fakeRealConnector struct{}

func (f *fakeRealConnector) Mode() string   { return "REAL" }
func (f *fakeRealConnector) Source() string { return "SWIFT" }
func (f *fakeRealConnector) Connect(ctx context.Context) error {
	panic("must not be called")
}
func (f *fakeRealConnector) Authenticate(ctx context.Context, credentials string) error {
	panic("must not be called")
}
func (f *fakeRealConnector) Receive(ctx context.Context) (Record, error) {
	panic("must not be called")
}
func (f *fakeRealConnector) Close(ctx context.Context) error { panic("must not be called") }
