package payment

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
// synthetic payment transactions. Implements
// pkg/blockers/livedata.FeedConnector directly, same discipline as
// this package's siblings (sar, bol, insurance).
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
func (c *SimulatedConnector) Source() string { return "Payment" }

func (c *SimulatedConnector) Connect(ctx context.Context) error { c.connected = true; return nil }

func (c *SimulatedConnector) Authenticate(ctx context.Context, credentials string) error {
	if !c.connected {
		return errors.New("payment: Authenticate called before Connect")
	}
	if credentials == "" {
		return errors.New("payment: empty credentials rejected")
	}
	c.authed = true
	return nil
}

func (c *SimulatedConnector) Receive(ctx context.Context) (livedata.Record, error) {
	if !c.authed {
		return livedata.Record{}, errors.New("payment: Receive called before Authenticate")
	}
	if c.pos >= len(c.records) {
		return livedata.Record{}, connector.ErrFeedExhausted
	}
	rec := c.records[c.pos]
	c.pos++
	return rec, nil
}

func (c *SimulatedConnector) Close(ctx context.Context) error { c.connected = false; return nil }

var methods = []string{"WIRE", "SWIFT", "ACH", "CARD", "RTGS"}
var currencies = []string{"USD", "EUR", "GBP", "JPY", "SGD"}

func buildFixtureRecords(seed int64, n int) []livedata.Record {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic seeded FIXTURE data, never the live_data path
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]livedata.Record, 0, n)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		w := wireTransaction{
			TransactionID: fmt.Sprintf("TXN-%06d", i),
			PayerID:       fmt.Sprintf("ACCT-%04d", rng.Intn(500)),
			PayeeID:       fmt.Sprintf("ACCT-%04d", rng.Intn(500)+500),
			Amount:        10 + rng.Float64()*99990,
			Currency:      currencies[rng.Intn(len(currencies))],
			Method:        methods[rng.Intn(len(methods))],
			Reference:     fmt.Sprintf("INV-%05d", rng.Intn(99999)),
			SettledUTC:    ts.Unix(),
		}
		payload, _ := json.Marshal(w)
		out = append(out, mkRecord(fmt.Sprintf("PAY-%04d", i), string(payload), ts))
	}
	return out
}

func mkRecord(id, payload string, ts time.Time) livedata.Record {
	return livedata.Record{
		ID: id, Source: "Payment", Payload: payload,
		DataMode: livedata.ModeSynthetic, Timestamp: ts,
		Hash: livedata.ComputeHash("Payment", payload, ts),
	}
}

// --- Deliberately-broken fixtures, for structural/semantic failure-mode tests ---

func FixtureMalformedJSON() string {
	return `{"transaction_id": "TXN-000001", "amount": `
}

func FixtureTruncatedJSON() string {
	return `{"transaction_id":"TXN-000001","payer_id":"ACCT-0001","payee_id":"ACCT-0501","amount":100.0`
}

// FixtureWrongSchema returns well-formed JSON for a completely
// different source type (an insurance-claim-shaped record).
func FixtureWrongSchema() string {
	return `{"claim_id":"CLM-000001","policy_number":"POL-000001","document_hash":"0000000000000000000000000000000000000000000000000000000000000000","document_type":"CLAIM_FORM","insured_party":"PARTY-001","submitted_utc":1735689600}`
}

func FixtureMissingRequiredField() string {
	return `{"transaction_id":"TXN-000002","payer_id":"ACCT-0001","payee_id":"ACCT-0501","amount":100.0,"method":"WIRE","settled_utc":1735689600}`
}

// FixtureSemanticallyInvalid returns a structurally well-formed record
// with a non-positive amount.
func FixtureSemanticallyInvalid() string {
	return `{"transaction_id":"TXN-000003","payer_id":"ACCT-0001","payee_id":"ACCT-0501","amount":-50.0,"currency":"USD","method":"WIRE","settled_utc":1735689600}`
}

// FixtureUnknownCurrency returns a structurally well-formed record
// whose currency is not on the allow-list.
func FixtureUnknownCurrency() string {
	return `{"transaction_id":"TXN-000004","payer_id":"ACCT-0001","payee_id":"ACCT-0501","amount":100.0,"currency":"ZZZ","method":"WIRE","settled_utc":1735689600}`
}

// FixtureSamePayerPayee returns a structurally well-formed record
// where payer and payee are the same account.
func FixtureSamePayerPayee() string {
	return `{"transaction_id":"TXN-000005","payer_id":"ACCT-0001","payee_id":"ACCT-0001","amount":100.0,"currency":"USD","method":"WIRE","settled_utc":1735689600}`
}
