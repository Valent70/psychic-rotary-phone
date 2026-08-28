package sar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/connector"
)

// SimulatedConnector produces deterministic, seeded, clearly-labeled
// synthetic SAR detections. It implements
// pkg/blockers/livedata.FeedConnector directly — reusing livedata's
// existing Connect/Authenticate/Receive/Close state machine contract
// and its Mode()=="SIMULATED" convention — rather than inventing a
// parallel connector shape. Every record it emits is DataMode ==
// livedata.ModeSynthetic; it has no code path that can produce
// livedata.ModeLive, which is exactly the property
// livedata.Pipeline.Ingest independently enforces (see
// TestPipeline_SimulatedNeverAcceptedAsLive in sar_test.go).
type SimulatedConnector struct {
	seed      int64
	n         int
	pos       int
	connected bool
	authed    bool
	records   []livedata.Record
}

// NewSimulatedConnector builds a connector that will emit n
// deterministic synthetic detections keyed by seed. Same seed, same
// count => byte-identical output, every time — required for this
// fixture to be a credible, replayable stand-in until a real provider
// is wired in.
func NewSimulatedConnector(seed int64, n int) *SimulatedConnector {
	return &SimulatedConnector{seed: seed, n: n, records: buildFixtureRecords(seed, n)}
}

func (c *SimulatedConnector) Mode() string   { return "SIMULATED" }
func (c *SimulatedConnector) Source() string { return "SAR" }

func (c *SimulatedConnector) Connect(ctx context.Context) error { c.connected = true; return nil }

func (c *SimulatedConnector) Authenticate(ctx context.Context, credentials string) error {
	if !c.connected {
		return errors.New("sar: Authenticate called before Connect")
	}
	if credentials == "" {
		return errors.New("sar: empty credentials rejected")
	}
	c.authed = true
	return nil
}

func (c *SimulatedConnector) Receive(ctx context.Context) (livedata.Record, error) {
	if !c.authed {
		return livedata.Record{}, errors.New("sar: Receive called before Authenticate")
	}
	if c.pos >= len(c.records) {
		return livedata.Record{}, connector.ErrFeedExhausted
	}
	rec := c.records[c.pos]
	c.pos++
	return rec, nil
}

func (c *SimulatedConnector) Close(ctx context.Context) error { c.connected = false; return nil }

// buildFixtureRecords deterministically generates n valid, well-formed
// SAR detections, each wrapped as a livedata.Record with a correct
// content hash (payload/source/timestamp), tagged ModeSynthetic.
func buildFixtureRecords(seed int64, n int) []livedata.Record {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic seeded FIXTURE data, never the live_data path
	platforms := []string{"Sentinel-1", "ICEYE-X", "Capella", "Umbra"}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]livedata.Record, 0, n)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		lat := -60 + rng.Float64()*120
		lon := -180 + rng.Float64()*360
		conf := 40 + rng.Float64()*60
		length := 50 + rng.Float64()*250
		w := wireDetection{
			SceneID:       fmt.Sprintf("SCENE-%04d", i),
			DetectionID:   fmt.Sprintf("DET-%06d", i),
			Platform:      platforms[rng.Intn(len(platforms))],
			Latitude:      &lat,
			Longitude:     &lon,
			LengthMeters:  &length,
			ConfidencePct: &conf,
			TimestampUTC:  ts.Unix(),
		}
		payload, _ := json.Marshal(w)
		out = append(out, mkRecord(fmt.Sprintf("SAR-%04d", i), string(payload), ts))
	}
	return out
}

func mkRecord(id, payload string, ts time.Time) livedata.Record {
	return livedata.Record{
		ID: id, Source: "SAR", Payload: payload,
		DataMode: livedata.ModeSynthetic, Timestamp: ts,
		Hash: livedata.ComputeHash("SAR", payload, ts),
	}
}

// --- Deliberately-broken fixtures, for structural/semantic failure-mode tests ---

// FixtureMalformedJSON returns a payload that is not valid JSON at
// all.
func FixtureMalformedJSON() string {
	return `{"scene_id": "SCENE-0001", "detection_id": `
}

// FixtureTruncatedJSON returns syntactically-incomplete JSON — an
// object opened but never closed, simulating a connection dropped
// mid-message.
func FixtureTruncatedJSON() string {
	return `{"scene_id": "SCENE-0001", "detection_id": "DET-000001", "platform": "Sentinel-1", "latitude": 12.5`
}

// FixtureWrongSchema returns well-formed JSON for a COMPLETELY
// different source type (a BoL-shaped record), to prove this
// package's decoder fails closed on wrong-schema input rather than
// coercing whatever fields happen to overlap.
func FixtureWrongSchema() string {
	return `{"bol_number":"BOL-0001","instrument":"BILL_OF_LADING","shipper_id":"S1","consignee_id":"C1","port_of_loading":"SGSIN","port_of_discharge":"NLRTM","cargo_description":"steel coils","issue_date_utc":1735689600}`
}

// FixtureMissingRequiredField returns structurally-plausible JSON
// missing one mandatory field (confidence_pct).
func FixtureMissingRequiredField() string {
	return `{"scene_id":"SCENE-0002","detection_id":"DET-000002","platform":"ICEYE-X","latitude":10.0,"longitude":20.0,"timestamp_utc":1735689600}`
}

// FixtureSemanticallyInvalid returns structurally well-formed JSON
// whose values are out of range (latitude > 90).
func FixtureSemanticallyInvalid() string {
	return `{"scene_id":"SCENE-0003","detection_id":"DET-000003","platform":"Capella","latitude":174.2,"longitude":20.0,"confidence_pct":85.0,"timestamp_utc":1735689600}`
}
