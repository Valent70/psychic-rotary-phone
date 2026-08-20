package maritime

import (
	"context"
	"fmt"
)

// PortCallAdapter is a MaritimeEvidenceSource for port-call records
// (berthing/discharge/departure events at a specific port). Fixture
// mode returns a small, deterministic record set built around RWC-001's
// own voyage-leg sequence from Akonikien (pkg/rwc.RWC001VoyageLegs:
// TEMA, LOME, DOUALA, OWENDO, LUANDA) -- the exact port names the
// corpus already carries in pkg/rwc/ports.go -- rather than invented
// sample port names.
//
// This package does not import pkg/rwc (see ais.go's doc comment for
// why); portCallFixtureRecords below is kept in hand-sync with
// pkg/rwc.RWC001VoyageLegs.
//
// Real-file/real-API mode: a real integration would poll a port-call/
// agency system's API (e.g. a PDA/FDA platform, or the "MagicPort"
// source pkg/rwc.RWC002Corpus.SourceReferences names as checked) or
// parse a delivered port-call manifest file, mapping each berth/
// discharge/departure event onto Evidence the same way Fetch does
// below. Not implemented in this sandbox for the same no-egress reason
// documented in ais.go.
type PortCallAdapter struct {
	mode string
}

// NewPortCallAdapterFixture returns a PortCallAdapter whose Fetch
// serves the built-in port-call fixture.
func NewPortCallAdapterFixture() *PortCallAdapter { return &PortCallAdapter{mode: "SIMULATED"} }

func (a *PortCallAdapter) SourceID(ctx context.Context) (string, error) {
	return "PORTCALL_FIXTURE", nil
}

func (a *PortCallAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	return Capabilities{SupportsFixture: true}, nil
}

// portCallFixtureRecords: RWC-001's own voyage-leg sequence
// (pkg/rwc.RWC001VoyageLegs), each leg's berthing event ticked
// sequentially from aisArrivalTick so the whole voyage is a coherent,
// monotonically-increasing timeline anchored to the same corpus tick
// AISAdapter's arrival record uses.
var portCallFixtureRecords = []struct {
	vesselID, portID, eventType string
}{
	{"RWC-001-CANDIDATE-A", "TEMA", "berthing"},
	{"RWC-001-CANDIDATE-A", "LOME", "berthing"},
	{"RWC-001-CANDIDATE-A", "DOUALA", "berthing"},
	{"RWC-001-CANDIDATE-A", "OWENDO", "berthing"},
}

func (a *PortCallAdapter) Fetch(ctx context.Context, req FetchRequest) ([]Evidence, error) {
	if a.mode != "SIMULATED" {
		return nil, newRealModeNotImplemented("PortCallAdapter", a.mode)
	}
	var out []Evidence
	for i, r := range portCallFixtureRecords {
		ts := aisArrivalTick + uint64(i) + 1
		if req.VesselID != "" && req.VesselID != r.vesselID {
			continue
		}
		if req.PortID != "" && req.PortID != r.portID {
			continue
		}
		if ts < req.Since || (req.Until != 0 && ts > req.Until) {
			continue
		}
		payload := []byte(fmt.Sprintf("PORTCALL|vessel=%s|port=%s|event=%s", r.vesselID, r.portID, r.eventType))
		out = append(out, Evidence{
			EventType:  r.eventType,
			Timestamp:  ts,
			Location:   r.portID,
			DeliveryID: fmt.Sprintf("%s@%s#%d", r.vesselID, r.portID, i),
			Sequence:   i,
			RawPayload: payload,
			SourceID:   "PORTCALL_FIXTURE",
			Provider:   "VERIQO_PORTCALL_FIXTURE_ADAPTER",
			Hash:       hashEvidence("portcall", r.eventType, r.vesselID, ts, i, payload),
		})
	}
	return out, nil
}

var _ MaritimeEvidenceSource = (*PortCallAdapter)(nil)
