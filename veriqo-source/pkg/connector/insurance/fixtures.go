package insurance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/connector"
)

// SimulatedConnector produces deterministic, seeded, clearly-labeled
// synthetic claim-evidence submissions. Implements
// pkg/blockers/livedata.FeedConnector directly, same discipline as
// pkg/connector/sar.SimulatedConnector and pkg/connector/bol's.
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
func (c *SimulatedConnector) Source() string { return "Insurance" }

func (c *SimulatedConnector) Connect(ctx context.Context) error { c.connected = true; return nil }

func (c *SimulatedConnector) Authenticate(ctx context.Context, credentials string) error {
	if !c.connected {
		return errors.New("insurance: Authenticate called before Connect")
	}
	if credentials == "" {
		return errors.New("insurance: empty credentials rejected")
	}
	c.authed = true
	return nil
}

func (c *SimulatedConnector) Receive(ctx context.Context) (livedata.Record, error) {
	if !c.authed {
		return livedata.Record{}, errors.New("insurance: Receive called before Authenticate")
	}
	if c.pos >= len(c.records) {
		return livedata.Record{}, connector.ErrFeedExhausted
	}
	rec := c.records[c.pos]
	c.pos++
	return rec, nil
}

func (c *SimulatedConnector) Close(ctx context.Context) error { c.connected = false; return nil }

var docTypes = []string{"CLAIM_FORM", "SURVEY_REPORT", "POLICY_SCHEDULE", "PROOF_OF_LOSS", "REPAIR_INVOICE"}
var currencies = []string{"USD", "EUR", "GBP", "SGD"}

func fixtureDocumentHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func buildFixtureRecords(seed int64, n int) []livedata.Record {
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic seeded FIXTURE data, never the live_data path
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]livedata.Record, 0, n)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		amount := 500 + rng.Float64()*49500
		w := wireClaimEvidence{
			ClaimID:      fmt.Sprintf("CLM-%06d", i),
			PolicyNumber: fmt.Sprintf("POL-%06d", rng.Intn(9999)),
			DocumentHash: fixtureDocumentHash(fmt.Sprintf("fixture-doc-%d-%d", seed, i)),
			DocumentType: docTypes[rng.Intn(len(docTypes))],
			InsuredParty: fmt.Sprintf("PARTY-%03d", rng.Intn(200)),
			ClaimAmount:  &amount,
			Currency:     currencies[rng.Intn(len(currencies))],
			SubmittedUTC: ts.Unix(),
		}
		payload, _ := json.Marshal(w)
		out = append(out, mkRecord(fmt.Sprintf("INS-%04d", i), string(payload), ts))
	}
	return out
}

func mkRecord(id, payload string, ts time.Time) livedata.Record {
	return livedata.Record{
		ID: id, Source: "Insurance", Payload: payload,
		DataMode: livedata.ModeSynthetic, Timestamp: ts,
		Hash: livedata.ComputeHash("Insurance", payload, ts),
	}
}

// --- Deliberately-broken fixtures, for structural/semantic failure-mode tests ---

func FixtureMalformedJSON() string {
	return `{"claim_id": "CLM-000001", "document_hash": `
}

func FixtureTruncatedJSON() string {
	return `{"claim_id":"CLM-000001","policy_number":"POL-000001","document_hash":"` + fixtureDocumentHash("x")
}

// FixtureWrongSchema returns well-formed JSON for a completely
// different source type (a payment-shaped record).
func FixtureWrongSchema() string {
	return `{"transaction_id":"TXN-000001","payer_id":"P1","payee_id":"P2","amount":100.0,"currency":"USD","method":"WIRE","settled_utc":1735689600}`
}

func FixtureMissingRequiredField() string {
	return `{"claim_id":"CLM-000002","policy_number":"POL-000002","document_hash":"` + fixtureDocumentHash("y") + `","insured_party":"PARTY-001","submitted_utc":1735689600}`
}

// FixtureSemanticallyInvalid returns a structurally well-formed record
// whose document_hash is not a valid sha256 hex digest.
func FixtureSemanticallyInvalid() string {
	return `{"claim_id":"CLM-000003","policy_number":"POL-000003","document_hash":"not-a-real-hash","document_type":"CLAIM_FORM","insured_party":"PARTY-001","submitted_utc":1735689600}`
}

// FixtureAmountWithoutCurrency returns a structurally well-formed
// record with a claim_amount but no currency.
func FixtureAmountWithoutCurrency() string {
	return fmt.Sprintf(`{"claim_id":"CLM-000004","policy_number":"POL-000004","document_hash":"%s","document_type":"CLAIM_FORM","insured_party":"PARTY-001","claim_amount":1000.0,"submitted_utc":1735689600}`, fixtureDocumentHash("z"))
}
