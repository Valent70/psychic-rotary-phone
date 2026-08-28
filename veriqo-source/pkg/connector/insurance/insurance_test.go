package insurance

import (
	"context"
	"errors"
	"testing"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/connector"
	"veriqo/pkg/evidence/ontology"
)

const testReceivedAt = 1800000000

func TestDecode_ValidFixtureRoundtrips(t *testing.T) {
	recs := buildFixtureRecords(1, 5)
	c := Contract{Source: "insurance-test"}
	for _, rec := range recs {
		ev, err := c.Decode(rec.Payload, testReceivedAt)
		if err != nil {
			t.Fatalf("Decode valid fixture: %v", err)
		}
		if ev.Type != ontology.TypeDocument {
			t.Fatalf("expected TypeDocument, got %s", ev.Type)
		}
		if ev.Attributes["document_hash"] == "" {
			t.Fatal("document_hash attribute must be populated (ontology's own required-attribute rule)")
		}
	}
}

func TestDecode_MalformedJSONFailsClosed(t *testing.T) {
	c := Contract{Source: "insurance-test"}
	if _, err := c.Decode(FixtureMalformedJSON(), testReceivedAt); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

func TestDecode_TruncatedJSONFailsClosed(t *testing.T) {
	c := Contract{Source: "insurance-test"}
	if _, err := c.Decode(FixtureTruncatedJSON(), testReceivedAt); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for truncated payload, got %v", err)
	}
}

func TestDecode_WrongSchemaFailsClosed(t *testing.T) {
	c := Contract{Source: "insurance-test"}
	if _, err := c.Decode(FixtureWrongSchema(), testReceivedAt); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for a payment-shaped payload fed to the insurance decoder, got %v", err)
	}
}

func TestDecode_MissingRequiredFieldFailsClosed(t *testing.T) {
	c := Contract{Source: "insurance-test"}
	if _, err := c.Decode(FixtureMissingRequiredField(), testReceivedAt); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for missing document_type, got %v", err)
	}
}

func TestDecode_SemanticallyInvalidFailsClosed(t *testing.T) {
	c := Contract{Source: "insurance-test"}
	if _, err := c.Decode(FixtureSemanticallyInvalid(), testReceivedAt); !errors.Is(err, ErrSemanticallyInvalid) {
		t.Fatalf("expected ErrSemanticallyInvalid for a malformed document_hash, got %v", err)
	}
}

func TestDecode_AmountWithoutCurrencyFailsClosed(t *testing.T) {
	c := Contract{Source: "insurance-test"}
	if _, err := c.Decode(FixtureAmountWithoutCurrency(), testReceivedAt); !errors.Is(err, ErrSemanticallyInvalid) {
		t.Fatalf("expected ErrSemanticallyInvalid for claim_amount without currency, got %v", err)
	}
}

func TestSimulatedConnector_Deterministic(t *testing.T) {
	a := buildFixtureRecords(42, 10)
	b := buildFixtureRecords(42, 10)
	for i := range a {
		if a[i].Hash != b[i].Hash || a[i].Payload != b[i].Payload {
			t.Fatalf("fixture %d not deterministic across identical seeds", i)
		}
	}
}

func TestSimulatedConnector_ContentHashDedup(t *testing.T) {
	ctx := context.Background()
	pipeline := livedata.NewPipeline()
	conn := NewSimulatedConnector(7, 5)
	dec := Contract{Source: "insurance-test"}

	res, _, err := connector.Drain(ctx, conn, pipeline, "fixture-credentials", dec, testReceivedAt)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Accepted != 5 || res.Ingested != 5 {
		t.Fatalf("expected 5/5 accepted, got %+v", res)
	}

	replay := NewSimulatedConnector(7, 5)
	res2, _, err := connector.Drain(ctx, replay, pipeline, "fixture-credentials", dec, testReceivedAt)
	if err != nil {
		t.Fatalf("Drain replay: %v", err)
	}
	if res2.Accepted != 0 {
		t.Fatalf("expected 0 accepted on replay (dedup), got %d", res2.Accepted)
	}
	if len(res2.Rejected) != 5 {
		t.Fatalf("expected all 5 replayed records rejected, got %d", len(res2.Rejected))
	}
}

func TestSimulatedNeverAcceptedAsLive(t *testing.T) {
	ctx := context.Background()
	pipeline := livedata.NewPipeline()
	conn := NewSimulatedConnector(9, 3)
	conn.records[0].DataMode = livedata.ModeLive
	conn.records[0].Hash = livedata.ComputeHash(conn.records[0].Source, conn.records[0].Payload, conn.records[0].Timestamp)

	dec := Contract{Source: "insurance-test"}
	res, _, err := connector.Drain(ctx, conn, pipeline, "fixture-credentials", dec, testReceivedAt)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Accepted != 2 {
		t.Fatalf("expected 2 accepted (the 2 honestly-tagged records), got %d", res.Accepted)
	}
	found := false
	for _, r := range res.Rejected {
		if r.Record.DataMode == livedata.ModeLive {
			found = true
			if r.Stage != "transport" {
				t.Fatalf("expected the LIVE-tagged fixture record to be rejected at the transport stage, got %s", r.Stage)
			}
		}
	}
	if !found {
		t.Fatal("the SIMULATED-mode connector's record tagged LIVE must be rejected, not silently dropped or accepted")
	}
}
