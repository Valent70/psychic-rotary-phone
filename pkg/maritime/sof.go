package maritime

import (
	"context"
	"fmt"
)

// SOFAdapter is a MaritimeEvidenceSource for Statement of Facts events
// (commenced/completed loading or discharge, laytime-relevant
// milestones). Fixture mode returns a small, deterministic record set
// built around RWC-001's own Bata port loading-rate figure (pkg/rwc
// .Ports["BATA"].LoadingRate == "5,000 MT PWWD SHINC") -- the corpus's
// own literal operational data -- rather than an invented rate.
//
// This package does not import pkg/rwc (see ais.go's doc comment for
// why); the literal values below are kept in hand-sync with
// pkg/rwc.Ports["BATA"].
//
// Real-file/real-API mode: a real integration would parse a delivered
// SOF document (agency-issued, typically PDF/scan) or poll a terminal
// operations system for commence/complete timestamps, mapping each
// milestone onto Evidence the same way Fetch does below. Not
// implemented in this sandbox for the same no-egress reason documented
// in ais.go.
type SOFAdapter struct {
	mode string
}

// NewSOFAdapterFixture returns a SOFAdapter whose Fetch serves the
// built-in SOF fixture.
func NewSOFAdapterFixture() *SOFAdapter { return &SOFAdapter{mode: "SIMULATED"} }

func (a *SOFAdapter) SourceID(ctx context.Context) (string, error) { return "SOF_FIXTURE", nil }

func (a *SOFAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	return Capabilities{SupportsFixture: true}, nil
}

const sofFixtureRate = "5,000 MT PWWD SHINC" // pkg/rwc.Ports["BATA"].LoadingRate, literal

var sofFixtureRecords = []struct {
	vesselID, portID, eventType string
}{
	{"RWC-001-CANDIDATE-A", "BATA", "commenced_loading"},
	{"RWC-001-CANDIDATE-A", "BATA", "completed_loading"},
}

func (a *SOFAdapter) Fetch(ctx context.Context, req FetchRequest) ([]Evidence, error) {
	if a.mode != "SIMULATED" {
		return nil, newRealModeNotImplemented("SOFAdapter", a.mode)
	}
	var out []Evidence
	for i, r := range sofFixtureRecords {
		ts := aisArrivalTick + 200 + uint64(i) // outside RWC-001's own voyage-tick range: SOF here is illustrative Bata loading ops, not part of the Akonikien voyage sequence portcall.go models
		if req.VesselID != "" && req.VesselID != r.vesselID {
			continue
		}
		if req.PortID != "" && req.PortID != r.portID {
			continue
		}
		if ts < req.Since || (req.Until != 0 && ts > req.Until) {
			continue
		}
		payload := []byte(fmt.Sprintf("SOF|vessel=%s|port=%s|event=%s|rate=%s", r.vesselID, r.portID, r.eventType, sofFixtureRate))
		out = append(out, Evidence{
			EventType:  r.eventType,
			Timestamp:  ts,
			Location:   r.portID,
			DeliveryID: fmt.Sprintf("%s@%s#SOF%d", r.vesselID, r.portID, i),
			Sequence:   i,
			RawPayload: payload,
			SourceID:   "SOF_FIXTURE",
			Provider:   "VERIQO_SOF_FIXTURE_ADAPTER",
			Hash:       hashEvidence("sof", r.eventType, r.vesselID, ts, i, payload),
		})
	}
	return out, nil
}

var _ MaritimeEvidenceSource = (*SOFAdapter)(nil)
