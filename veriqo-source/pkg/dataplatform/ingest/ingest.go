// Package ingest is the generic data-receiver framework
// "Yang_masih_saya_kritisi.docx" §7 explicitly endorses: "Dia
// mengatakan: generic schema. Saya setuju. Karena: lebih baik: schema
// dulu daripada fake connector. Ini keputusan yang benar." Unchanged
// from the prior session's design for exactly that reason.
//
// This package defines the SHAPE every future data source (AIS, SAR,
// RF, OSINT, satellite imagery metadata, Bills of Lading, weather,
// insurance, SWIFT messages) will arrive in, and a validator + two
// adapters converting a validated record into the shapes
// pkg/moat/contradiction and veriqo/core/evidence already consume. It
// deliberately does NOT implement any real connection to AIS, SWIFT,
// satellite providers, or any other external feed — those require
// commercial data licenses and legal partnerships per CRITICAL_FINDINGS
// (BRUTAL HONESTY), not code.
package ingest

import (
	"fmt"

	"veriqo/pkg/moat/contradiction"
	"veriqo/veriqo/core/evidence"
)

type SourceType string

const (
	SourceAIS               SourceType = "AIS"
	SourceSAR               SourceType = "SAR"
	SourceRF                SourceType = "RF"
	SourceOSINT             SourceType = "OSINT"
	SourceDocument          SourceType = "DOCUMENT"
	SourceSatelliteImagery  SourceType = "SATELLITE_IMAGERY"
	SourceBillOfLading      SourceType = "BILL_OF_LADING"
	SourceWeather           SourceType = "WEATHER"
	SourceInsurance         SourceType = "INSURANCE"
	SourceSWIFT             SourceType = "SWIFT_MESSAGE"
	SourceCorrespondentBank SourceType = "CORRESPONDENT_BANKING"
	SourcePortAuthority     SourceType = "PORT_AUTHORITY"
)

// RawRecord is the generic envelope every source type is normalized
// into before reaching the intelligence stack.
type RawRecord struct {
	SourceType        SourceType
	SourceID          string
	ClaimKey          string
	Value             string
	SourceReliability float64 // in (0,1]
	Tick              uint64
	Metadata          map[string]string
}

type Validator interface {
	Validate(RawRecord) error
}

type DefaultValidator struct{}

func (DefaultValidator) Validate(r RawRecord) error {
	if r.SourceType == "" {
		return fmt.Errorf("ingest: SourceType is required")
	}
	if r.SourceID == "" {
		return fmt.Errorf("ingest: SourceID is required")
	}
	if r.ClaimKey == "" {
		return fmt.Errorf("ingest: ClaimKey is required")
	}
	if r.Value == "" {
		return fmt.Errorf("ingest: Value is required")
	}
	if r.SourceReliability <= 0 || r.SourceReliability > 1 {
		return fmt.Errorf("ingest: SourceReliability must be in (0,1], got %v", r.SourceReliability)
	}
	return nil
}

// Fetcher is the interface a real data-source connector implements
// once a data partnership exists. This package ships zero
// implementations of it.
type Fetcher interface {
	Name() string
	Fetch() ([]RawRecord, error)
}

func ToArbitrationObservation(r RawRecord) contradiction.RawObservation {
	return contradiction.RawObservation{
		ClaimKey: r.ClaimKey, SourceID: r.SourceID, Value: r.Value,
		SourceReliability: r.SourceReliability, Tick: r.Tick,
	}
}

func ToEvidenceObservation(r RawRecord, confidence float64) evidence.Observation {
	return evidence.Observation{
		ClaimKey: r.ClaimKey, SourceID: r.SourceID, Value: r.Value,
		Confidence: confidence, Tick: r.Tick,
	}
}

func IngestBatch(records []RawRecord, v Validator) (valid []RawRecord, rejected []error) {
	if v == nil {
		v = DefaultValidator{}
	}
	for _, r := range records {
		if err := v.Validate(r); err != nil {
			rejected = append(rejected, fmt.Errorf("record %q/%q: %w", r.SourceType, r.SourceID, err))
			continue
		}
		valid = append(valid, r)
	}
	return valid, rejected
}
