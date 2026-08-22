// Package payment is R-050's payment/transaction-evidence ingestion
// contract — one of the five real-world source types the requirement
// traceability matrix named as the genuine engineering gap in
// pkg/connector. Honest scope, stated once here rather than buried:
// this sandbox has no reachable path to any real payment rail (SWIFT,
// ACH, card network, or otherwise), so this package cannot and does
// not claim to be "live" in this session. What it DOES provide is the
// real, tested machinery a live provider integration would plug
// straight into:
//
//  1. Contract — parses raw payment/settlement payloads, structurally
//     validates them, semantically validates them (amount, currency,
//     method), and canonicalizes a valid record into
//     pkg/evidence/ontology.Evidence{Type: TypeFinancialRecord}. It
//     implements pkg/connector.Decoder.
//  2. SimulatedConnector — a deterministic, seeded fixture connector
//     implementing pkg/blockers/livedata.FeedConnector, always tagged
//     ModeSynthetic. livedata's own fixtures already exercise a
//     "SWIFT" source name for its four-source qualification test; this
//     package's Source() is "Payment" — a distinct, more general
//     source identity that a SWIFT, ACH or card-network real
//     connector could each be plugged in under.
package payment

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"veriqo/pkg/connector"
	"veriqo/pkg/evidence/ontology"
)

var (
	ErrMalformed           = errors.New("payment: malformed transaction record")
	ErrSemanticallyInvalid = errors.New("payment: semantically invalid transaction record")
)

// knownMethods is the closed set of settlement rails this contract
// accepts.
var knownMethods = map[string]bool{
	"WIRE": true, "SWIFT": true, "ACH": true, "CARD": true, "RTGS": true,
}

// wireTransaction is this package's payment-transaction wire schema.
type wireTransaction struct {
	TransactionID string  `json:"transaction_id"`
	PayerID       string  `json:"payer_id"`
	PayeeID       string  `json:"payee_id"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Method        string  `json:"method"`
	Reference     string  `json:"reference,omitempty"`
	SettledUTC    int64   `json:"settled_utc"`
}

func decodeWire(payload string) (wireTransaction, error) {
	var w wireTransaction
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return wireTransaction{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	for field, v := range map[string]string{
		"transaction_id": w.TransactionID, "payer_id": w.PayerID, "payee_id": w.PayeeID,
		"currency": w.Currency, "method": w.Method,
	} {
		if strings.TrimSpace(v) == "" {
			return wireTransaction{}, fmt.Errorf("%w: missing %s", ErrMalformed, field)
		}
	}
	if w.SettledUTC <= 0 {
		return wireTransaction{}, fmt.Errorf("%w: missing or non-positive settled_utc", ErrMalformed)
	}
	return w, nil
}

func (w wireTransaction) validateSemantics() error {
	if w.Amount <= 0 {
		return fmt.Errorf("%w: amount %v must be > 0", ErrSemanticallyInvalid, w.Amount)
	}
	if !connector.KnownCurrency(w.Currency) {
		return fmt.Errorf("%w: unrecognized currency %q", ErrSemanticallyInvalid, w.Currency)
	}
	if !knownMethods[w.Method] {
		return fmt.Errorf("%w: unknown method %q", ErrSemanticallyInvalid, w.Method)
	}
	if w.PayerID == w.PayeeID {
		return fmt.Errorf("%w: payer_id and payee_id must differ", ErrSemanticallyInvalid)
	}
	return nil
}

func (w wireTransaction) toOntologyEvidence(source string, receivedAtTick uint64) (ontology.Evidence, error) {
	attrs := map[string]string{
		"currency":       strings.ToUpper(w.Currency),
		"amount":         fmt.Sprintf("%.2f", w.Amount),
		"payer_id":       w.PayerID,
		"payee_id":       w.PayeeID,
		"transaction_id": w.TransactionID,
	}
	if w.Reference != "" {
		attrs["reference"] = w.Reference
	}
	return ontology.New(ontology.Evidence{
		Type:       ontology.TypeFinancialRecord,
		Subject:    "payment:" + w.TransactionID,
		Predicate:  "payment_settled",
		Object:     w.Method,
		Source:     source,
		ObservedAt: uint64(w.SettledUTC), // #nosec G115 -- decodeWire already rejects SettledUTC <= 0
		ReceivedAt: receivedAtTick,
		// A settled transaction record from a payment rail is a
		// near-certain assertion (the rail itself cleared it); this
		// package assigns a fixed high prior rather than inventing a
		// per-record confidence model, matching pkg/connector/bol's
		// documented choice for the same reason.
		Confidence: 0.97,
		Attributes: attrs,
	})
}

// Contract is this package's pkg/connector.Decoder implementation.
type Contract struct {
	Source string
}

func (c Contract) SourceType() string { return "Payment" }

func (c Contract) Decode(payload string, receivedAtTick uint64) (ontology.Evidence, error) {
	w, err := decodeWire(payload)
	if err != nil {
		return ontology.Evidence{}, err
	}
	if err := w.validateSemantics(); err != nil {
		return ontology.Evidence{}, err
	}
	source := c.Source
	if source == "" {
		source = "payment:unspecified"
	}
	return w.toOntologyEvidence(source, receivedAtTick)
}
