package bol

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
// synthetic BoL declarations. Implements
// pkg/blockers/livedata.FeedConnector directly, same discipline as
// pkg/connector/sar.SimulatedConnector.
type SimulatedConnector struct {
	pos       int
	connected bool
	authed    bool
	records   []livedata.Record
}

func NewSimulatedConnector(seed int64, n int) *SimulatedConnector {
	return &SimulatedConnector{records: buildFixtureRecords(seed, n)}
}

func (c *SimulatedConnector) Mode() string   { return "SIMULATED" }
func (c *SimulatedConnector) Source() string { return "BoL" }

func (c *SimulatedConnector) Connect(ctx context.Context) error { c.connected = true; return nil }

func (c *SimulatedConnector) Authenticate(ctx context.Context, credentials string) error {
	if !c.connected {
		return errors.New("bol: Authenticate called before Connect")
	}
	if credentials == "" {
		return errors.New("bol: empty credentials rejected")
	}
	c.authed = true
	return nil
}

func (c *SimulatedConnector) Receive(ctx context.Context) (livedata.Record, error) {
	if !c.authed {
		return livedata.Record{}, errors.New("bol: Receive called before Authenticate")
	}
	if c.pos >= len(c.records) {
		return livedata.Record{}, connector.ErrFeedExhausted
	}
	rec := c.records[c.pos]
	c.pos++
	return rec, nil
}

func (c *SimulatedConnector) Close(ctx context.Context) error { c.connected = false; return nil }

var instruments = []string{"BILL_OF_LADING", "SEA_WAYBILL", "CUSTOMS_ENTRY"}
var ports = []string{"SGSIN", "NLRTM", "USLAX", "CNSHA", "AEJEA"}
var cargo = []string{"steel coils", "electronics", "refrigerated produce", "bulk grain", "machinery parts"}

func buildFixtureRecords(seed int64, n int) []livedata.Record {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic seeded FIXTURE data, never the live_data path
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]livedata.Record, 0, n)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		w := wireBoL{
			BoLNumber:        fmt.Sprintf("BOL-%06d", i),
			Instrument:       instruments[rng.Intn(len(instruments))],
			ShipperID:        fmt.Sprintf("SHIPPER-%03d", rng.Intn(50)),
			ConsigneeID:      fmt.Sprintf("CONSIGNEE-%03d", rng.Intn(50)+100),
			VesselIMO:        fmt.Sprintf("%07d", 9100000+rng.Intn(899999)),
			PortOfLoading:    ports[rng.Intn(len(ports))],
			PortOfDischarge:  ports[rng.Intn(len(ports))],
			ContainerNumbers: []string{fmt.Sprintf("MSCU%07d", rng.Intn(9999999))},
			CargoDescription: cargo[rng.Intn(len(cargo))],
			IssueDateUTC:     ts.Unix(),
		}
		payload, _ := json.Marshal(w)
		out = append(out, mkRecord(fmt.Sprintf("BOL-%04d", i), string(payload), ts))
	}
	return out
}

func mkRecord(id, payload string, ts time.Time) livedata.Record {
	return livedata.Record{
		ID: id, Source: "BoL", Payload: payload,
		DataMode: livedata.ModeSynthetic, Timestamp: ts,
		Hash: livedata.ComputeHash("BoL", payload, ts),
	}
}

// --- Deliberately-broken fixtures, for structural/semantic failure-mode tests ---

func FixtureMalformedJSON() string {
	return `{"bol_number": "BOL-000001", "instrument": `
}

func FixtureTruncatedJSON() string {
	return `{"bol_number":"BOL-000001","instrument":"BILL_OF_LADING","shipper_id":"S1"`
}

// FixtureWrongSchema returns well-formed JSON for a completely
// different source type (a SAR-detection-shaped record).
func FixtureWrongSchema() string {
	return `{"scene_id":"SCENE-0001","detection_id":"DET-000001","platform":"Sentinel-1","latitude":10.0,"longitude":20.0,"confidence_pct":85.0,"timestamp_utc":1735689600}`
}

func FixtureMissingRequiredField() string {
	return `{"bol_number":"BOL-000002","instrument":"BILL_OF_LADING","shipper_id":"S1","consignee_id":"C1","port_of_loading":"SGSIN","port_of_discharge":"NLRTM","issue_date_utc":1735689600}`
}

// FixtureSemanticallyInvalid returns a structurally well-formed record
// with an unrecognized instrument type.
func FixtureSemanticallyInvalid() string {
	return `{"bol_number":"BOL-000003","instrument":"PROMISSORY_NOTE","shipper_id":"S1","consignee_id":"C1","port_of_loading":"SGSIN","port_of_discharge":"NLRTM","cargo_description":"steel coils","issue_date_utc":1735689600}`
}

// FixtureBadContainerNumber returns a structurally well-formed record
// whose container number does not match the ISO 6346 shape.
func FixtureBadContainerNumber() string {
	return `{"bol_number":"BOL-000004","instrument":"BILL_OF_LADING","shipper_id":"S1","consignee_id":"C1","port_of_loading":"SGSIN","port_of_discharge":"NLRTM","cargo_description":"steel coils","container_numbers":["NOTACONTAINER"],"issue_date_utc":1735689600}`
}
