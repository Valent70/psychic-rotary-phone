package ingest

import "testing"

func TestDefaultValidatorRejectsMissingFields(t *testing.T) {
	v := DefaultValidator{}
	cases := []RawRecord{
		{SourceID: "s1", ClaimKey: "c1", Value: "v1", SourceReliability: 0.9},
		{SourceType: SourceAIS, ClaimKey: "c1", Value: "v1", SourceReliability: 0.9},
		{SourceType: SourceAIS, SourceID: "s1", Value: "v1", SourceReliability: 0.9},
		{SourceType: SourceAIS, SourceID: "s1", ClaimKey: "c1", SourceReliability: 0.9},
		{SourceType: SourceAIS, SourceID: "s1", ClaimKey: "c1", Value: "v1", SourceReliability: 1.5},
	}
	for i, c := range cases {
		if err := v.Validate(c); err == nil {
			t.Fatalf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestDefaultValidatorAcceptsValid(t *testing.T) {
	v := DefaultValidator{}
	r := RawRecord{SourceType: SourceSWIFT, SourceID: "swift-1", ClaimKey: "tx/1", Value: "flagged", SourceReliability: 0.8, Tick: 1}
	if err := v.Validate(r); err != nil {
		t.Fatalf("expected valid record to pass, got %v", err)
	}
}

func TestAdapters(t *testing.T) {
	r := RawRecord{SourceType: SourceAIS, SourceID: "ais-1", ClaimKey: "vessel/IMO1", Value: "at-sea", SourceReliability: 0.85, Tick: 5}
	obs := ToArbitrationObservation(r)
	if obs.ClaimKey != r.ClaimKey || obs.SourceReliability != r.SourceReliability {
		t.Fatalf("unexpected arbitration observation: %+v", obs)
	}
	eo := ToEvidenceObservation(r, 0.9)
	if eo.ClaimKey != r.ClaimKey || eo.Confidence != 0.9 {
		t.Fatalf("unexpected evidence observation: %+v", eo)
	}
}

func TestIngestBatchPartialRejection(t *testing.T) {
	records := []RawRecord{
		{SourceType: SourceAIS, SourceID: "s1", ClaimKey: "c1", Value: "v1", SourceReliability: 0.9},
		{SourceType: SourceAIS, SourceID: "s2", ClaimKey: "", Value: "v1", SourceReliability: 0.9},
	}
	valid, rejected := IngestBatch(records, nil)
	if len(valid) != 1 || len(rejected) != 1 {
		t.Fatalf("expected 1 valid + 1 rejected, got %d/%d", len(valid), len(rejected))
	}
}
