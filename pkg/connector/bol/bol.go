// Package bol is R-050's Bill of Lading / customs ingestion contract
// — one of the five real-world source types the requirement
// traceability matrix named as the genuine engineering gap in
// pkg/connector. Honest scope, stated once here rather than buried:
// this sandbox has no reachable path to any real customs/carrier EDI
// feed, so this package cannot and does not claim to be "live" in
// this session. What it DOES provide is the real, tested machinery a
// live provider integration would plug straight into:
//
//  1. Contract — parses raw BoL/customs-declaration payloads,
//     structurally validates them, semantically validates them
//     (instrument type known, container numbers well-formed), and
//     canonicalizes a valid declaration into
//     pkg/evidence/ontology.Evidence{Type: TypeTradeRecord}. It
//     implements pkg/connector.Decoder.
//  2. SimulatedConnector — a deterministic, seeded fixture connector
//     implementing pkg/blockers/livedata.FeedConnector, always tagged
//     ModeSynthetic.
package bol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"veriqo/pkg/evidence/ontology"
)

var (
	ErrMalformed           = errors.New("bol: malformed bill-of-lading record")
	ErrSemanticallyInvalid = errors.New("bol: semantically invalid bill-of-lading record")
)

// knownInstruments is the closed set of trade-instrument types this
// contract accepts. An instrument this package has never seen declared
// is a fail-closed situation, not a "pass it through anyway" one.
var knownInstruments = map[string]bool{
	"BILL_OF_LADING": true,
	"SEA_WAYBILL":    true,
	"CUSTOMS_ENTRY":  true,
}

// containerNumberRE matches the ISO 6346 container-number SHAPE (four
// letters + seven digits). This package deliberately does not
// implement the ISO 6346 check-digit algorithm — that would be a
// separate, precise numeric contract of its own — so this is a
// structural shape check, not a full checksum validation.
var containerNumberRE = regexp.MustCompile(`^[A-Z]{4}[0-9]{7}$`)

// wireBoL is this package's Bill-of-Lading / customs-declaration wire
// schema. Field names follow common trade-documentation terminology;
// this is not a claim of byte-for-byte compatibility with any specific
// carrier or customs authority's actual EDI format.
type wireBoL struct {
	BoLNumber        string   `json:"bol_number"`
	Instrument       string   `json:"instrument"`
	ShipperID        string   `json:"shipper_id"`
	ConsigneeID      string   `json:"consignee_id"`
	VesselIMO        string   `json:"vessel_imo,omitempty"`
	PortOfLoading    string   `json:"port_of_loading"`
	PortOfDischarge  string   `json:"port_of_discharge"`
	ContainerNumbers []string `json:"container_numbers,omitempty"`
	CargoDescription string   `json:"cargo_description"`
	IssueDateUTC     int64    `json:"issue_date_utc"`
}

func decodeWire(payload string) (wireBoL, error) {
	var w wireBoL
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return wireBoL{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	for field, v := range map[string]string{
		"bol_number": w.BoLNumber, "instrument": w.Instrument, "shipper_id": w.ShipperID,
		"consignee_id": w.ConsigneeID, "port_of_loading": w.PortOfLoading,
		"port_of_discharge": w.PortOfDischarge, "cargo_description": w.CargoDescription,
	} {
		if strings.TrimSpace(v) == "" {
			return wireBoL{}, fmt.Errorf("%w: missing %s", ErrMalformed, field)
		}
	}
	if w.IssueDateUTC <= 0 {
		return wireBoL{}, fmt.Errorf("%w: missing or non-positive issue_date_utc", ErrMalformed)
	}
	return w, nil
}

func (w wireBoL) validateSemantics() error {
	if !knownInstruments[w.Instrument] {
		return fmt.Errorf("%w: unknown instrument %q", ErrSemanticallyInvalid, w.Instrument)
	}
	if w.ShipperID == w.ConsigneeID {
		return fmt.Errorf("%w: shipper_id and consignee_id must differ", ErrSemanticallyInvalid)
	}
	for _, cn := range w.ContainerNumbers {
		if !containerNumberRE.MatchString(cn) {
			return fmt.Errorf("%w: container number %q does not match the ISO 6346 shape", ErrSemanticallyInvalid, cn)
		}
	}
	return nil
}

func (w wireBoL) toOntologyEvidence(source string, receivedAtTick uint64) (ontology.Evidence, error) {
	attrs := map[string]string{
		"instrument":        w.Instrument,
		"bol_number":        w.BoLNumber,
		"shipper_id":        w.ShipperID,
		"consignee_id":      w.ConsigneeID,
		"port_of_loading":   w.PortOfLoading,
		"port_of_discharge": w.PortOfDischarge,
		"container_count":   fmt.Sprintf("%d", len(w.ContainerNumbers)),
	}
	if w.VesselIMO != "" {
		attrs["vessel_imo"] = w.VesselIMO
	}
	if len(w.ContainerNumbers) > 0 {
		attrs["container_numbers"] = strings.Join(w.ContainerNumbers, ",")
	}
	return ontology.New(ontology.Evidence{
		Type:       ontology.TypeTradeRecord,
		Subject:    "bol:" + w.BoLNumber,
		Predicate:  "shipment_declared",
		Object:     w.CargoDescription,
		Source:     source,
		ObservedAt: uint64(w.IssueDateUTC), // #nosec G115 -- decodeWire already rejects IssueDateUTC <= 0
		ReceivedAt: receivedAtTick,
		// Confidence: a customs/carrier-issued document is an asserted
		// declaration, not a sensor measurement -- this package assigns
		// it a fixed high-but-not-certain prior rather than inventing a
		// per-record confidence model. A real deployment should instead
		// weight this via pkg/dataquality's source-reliability Learner,
		// which this package deliberately leaves to the caller.
		Confidence: 0.9,
		Attributes: attrs,
	})
}

// Contract is this package's pkg/connector.Decoder implementation.
type Contract struct {
	Source string
}

func (c Contract) SourceType() string { return "BoL" }

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
		source = "bol:unspecified"
	}
	return w.toOntologyEvidence(source, receivedAtTick)
}
