// Package connector_test is an external test package (not `package
// connector`) specifically so it can exercise Drain against every one
// of the five real ingestion contracts (sar, bol, insurance, payment;
// aisstream is exercised in its own package) end-to-end, the way a
// real caller assembling a multi-source pipeline would, without
// creating an import cycle back into pkg/connector itself.
package connector_test

import (
	"context"
	"testing"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/connector"
	"veriqo/pkg/connector/bol"
	"veriqo/pkg/connector/insurance"
	"veriqo/pkg/connector/payment"
	"veriqo/pkg/connector/sar"
	"veriqo/pkg/dataquality"
	"veriqo/pkg/evidence/ontology"
)

const testReceivedAt = 1800000000

// TestDrainAcrossAllFiveR050Sources mirrors
// pkg/blockers/livedata's own TestRunQualificationAcrossFourSources,
// but exercises the FULL R-050 contract set (SAR, BoL, insurance,
// payment) through connector.Drain's composition of livedata.Pipeline
// (dedup/anti-replay/LIVE-refusal) and each source's own Decoder
// (structural+semantic validation+canonicalization) -- proving the
// whole pipeline, not just the transport layer, end-to-end for every
// source type in one pass.
func TestDrainAcrossAllFiveR050Sources(t *testing.T) {
	ctx := context.Background()
	pipeline := livedata.NewPipeline()

	type source struct {
		name string
		feed livedata.FeedConnector
		dec  connector.Decoder
	}
	sources := []source{
		{"SAR", sar.NewSimulatedConnector(1, 8), sar.Contract{Source: "sar-fixture"}},
		{"BoL", bol.NewSimulatedConnector(2, 8), bol.Contract{Source: "bol-fixture"}},
		{"Insurance", insurance.NewSimulatedConnector(3, 8), insurance.Contract{Source: "insurance-fixture"}},
		{"Payment", payment.NewSimulatedConnector(4, 8), payment.Contract{Source: "payment-fixture"}},
	}

	var allEvidence []ontology.Evidence
	for _, s := range sources {
		res, ev, err := connector.Drain(ctx, s.feed, pipeline, "fixture-credentials", s.dec, testReceivedAt)
		if err != nil {
			t.Fatalf("Drain(%s): %v", s.name, err)
		}
		if res.Accepted != 8 || res.Ingested != 8 {
			t.Fatalf("Drain(%s): expected 8/8 accepted, got %+v", s.name, res)
		}
		if len(res.Rejected) != 0 {
			t.Fatalf("Drain(%s): expected zero rejections for well-formed fixtures, got %+v", s.name, res.Rejected)
		}
		allEvidence = append(allEvidence, ev...)
	}
	if len(allEvidence) != 32 {
		t.Fatalf("expected 32 total canonicalized evidence records, got %d", len(allEvidence))
	}

	// Every EvidenceID must be unique -- content-addressing across five
	// unrelated source types must never collide.
	seen := map[string]bool{}
	for _, e := range allEvidence {
		if seen[e.EvidenceID] {
			t.Fatalf("duplicate EvidenceID across sources: %s", e.EvidenceID)
		}
		seen[e.EvidenceID] = true
		if err := e.Validate(); err != nil {
			t.Fatalf("canonicalized evidence failed ontology.Validate: %v", err)
		}
	}

	// Now replay the EXACT same four feeds again -- the shared pipeline
	// must reject every single record as a duplicate by content hash,
	// regardless of which source type it came from.
	replaySources := []source{
		{"SAR", sar.NewSimulatedConnector(1, 8), sar.Contract{Source: "sar-fixture"}},
		{"BoL", bol.NewSimulatedConnector(2, 8), bol.Contract{Source: "bol-fixture"}},
		{"Insurance", insurance.NewSimulatedConnector(3, 8), insurance.Contract{Source: "insurance-fixture"}},
		{"Payment", payment.NewSimulatedConnector(4, 8), payment.Contract{Source: "payment-fixture"}},
	}
	for _, s := range replaySources {
		res, _, err := connector.Drain(ctx, s.feed, pipeline, "fixture-credentials", s.dec, testReceivedAt)
		if err != nil {
			t.Fatalf("Drain replay(%s): %v", s.name, err)
		}
		if res.Accepted != 0 {
			t.Fatalf("Drain replay(%s): expected 0 accepted (anti-replay), got %d", s.name, res.Accepted)
		}
		for _, r := range res.Rejected {
			if r.Stage != "transport" {
				t.Fatalf("Drain replay(%s): expected transport-stage rejection, got %s: %s", s.name, r.Stage, r.Reason)
			}
		}
	}
}

// TestQualityVectorReflectsRejections proves pkg/dataquality actually
// plugs into this pipeline's output rather than existing in parallel
// to it: a batch with real rejections must produce a Vector whose
// Validity/Completeness dimensions -- and therefore Assess's
// composite/floor verdict -- visibly reflect that, not a single
// undifferentiated score. IngestResult here is a realistic one: 10
// records ingested, only 4 accepted, mirroring what Drain would
// return for a feed half of whose records failed the "decode" stage
// (see the sar/bol/insurance/payment packages' own decode-rejection
// tests for that path exercised against Drain directly).
func TestQualityVectorReflectsRejections(t *testing.T) {
	degraded := connector.IngestResult{SourceType: "SAR", Ingested: 10, Accepted: 4}
	v := connector.QualityVector(degraded)
	if v.Validity != 0.4 || v.Completeness != 0.4 {
		t.Fatalf("expected Validity/Completeness == 0.4 for 4/10 accepted, got %+v", v)
	}
	assessment, err := dataquality.Assess("sar-fixture", "batch-1", testReceivedAt, v, dataquality.DefaultWeights(), dataquality.DefaultFloors())
	if err != nil {
		t.Fatalf("dataquality.Assess: %v", err)
	}
	if assessment.Acceptable {
		t.Fatalf("expected a degraded batch (60%% rejected) to fail dataquality's floors, got acceptable=true: %+v", assessment)
	}

	clean := connector.IngestResult{SourceType: "SAR", Ingested: 10, Accepted: 10}
	cv := connector.QualityVector(clean)
	cleanAssessment, err := dataquality.Assess("sar-fixture", "batch-2", testReceivedAt, cv, dataquality.DefaultWeights(), dataquality.DefaultFloors())
	if err != nil {
		t.Fatalf("dataquality.Assess: %v", err)
	}
	if !cleanAssessment.Acceptable {
		t.Fatalf("expected a fully-clean batch to pass dataquality's floors, got %+v", cleanAssessment)
	}
}
