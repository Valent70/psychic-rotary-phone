package maritime

import (
	"context"
	"fmt"
)

// AISAdapter is a MaritimeEvidenceSource for AIS (Automatic
// Identification System) position/arrival reports. Fixture mode
// returns a small, deterministic, built-in AIS record set shaped
// around RWC-001's own Ranchan Maritime corpus (see pkg/rwc/ports.go,
// pkg/rwc/rwc001.go) -- vessel "RWC-001-CANDIDATE-A" arriving at
// "AKONIKIEN" and departing toward "TEMA", the corpus's own baseline
// candidate/port/voyage-leg values -- rather than disconnected invented
// sample data.
//
// This package deliberately does not import pkg/rwc: pkg/rwc imports
// this package to wire AISAdapter into RWC-001's case construction (see
// rwc001.go's BuildRWC001CaseWithAIS), so importing the other way would
// be a cycle. The literal values in aisFixtureRecords below are kept in
// hand-sync with pkg/rwc's own corpus; ais_test.go cross-checks the
// arrival tick specifically against what rwc001_test.go already
// hardcodes.
//
// Real-file/real-API mode: a real AIS integration would poll a
// provider's REST/streaming API (e.g. MarineTraffic, Spire -- the same
// providers RWC-002's corpus names as "checked" in
// pkg/rwc.RWC002Corpus.SourceReferences) or parse a delivered NMEA/AIVDM
// file, filtered by MMSI/IMO and a time window, and map each position/
// status report onto Evidence the same way Fetch does below -- the
// adapter would change, callers would not (the same "adapter swaps,
// the engine doesn't change" shape as every other real-vs-fixture pair
// in this codebase). Not implemented in this sandbox: no network or
// file egress available here to verify a real implementation against
// (see docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md §1's own
// no-network-egress finding, reused rather than re-litigated).
type AISAdapter struct {
	mode string // "SIMULATED" is the only mode this package can construct today
}

// NewAISAdapterFixture returns an AISAdapter whose Fetch serves the
// built-in AIS fixture.
func NewAISAdapterFixture() *AISAdapter { return &AISAdapter{mode: "SIMULATED"} }

func (a *AISAdapter) SourceID(ctx context.Context) (string, error) { return "AIS_FIXTURE", nil }

func (a *AISAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	return Capabilities{SupportsFixture: true}, nil
}

// aisArrivalTick is the logical tick RWC-001's own test suite already
// hardcodes for this corpus (pkg/rwc/rwc001_test.go's
// BuildRWC001Case(c, 1), pkg/rwc/corpus.go's BuildCorpusV2(tick)
// callers). The fixture below reports the baseline candidate's arrival
// at exactly this tick so that wiring AISAdapter into RWC-001's case
// construction changes only where the tick structurally comes from,
// never its value -- see rwc001.go's BuildRWC001CaseWithAIS.
const aisArrivalTick uint64 = 1

// aisFixtureRecords is the built-in AIS fixture: RWC-001's baseline
// candidate ("RWC-001-CANDIDATE-A" -- pkg/rwc.RWC001BaselineVessel)
// reported arriving at Akonikien (candidate A's port per
// pkg/rwc.RWC001Candidates) and, separately, a later position report
// toward Tema (the first RWC001VoyageLegs entry).
var aisFixtureRecords = []struct {
	vesselID, portID, eventType, location string
	timestamp                             uint64
}{
	{"RWC-001-CANDIDATE-A", "AKONIKIEN", "arrival", "AKONIKIEN", aisArrivalTick},
	{"RWC-001-CANDIDATE-A", "TEMA", "departure", "AKONIKIEN", aisArrivalTick + 1},
}

// Fetch implements MaritimeEvidenceSource. Since/Until filter on
// timestamp inclusively; Until==0 means "no upper bound" (this
// fixture's ticks are always >=1, so 0 is never itself a real
// timestamp -- the same "0 = not stated" convention pkg/rwc.Port uses
// for its own optional numeric fields).
func (a *AISAdapter) Fetch(ctx context.Context, req FetchRequest) ([]Evidence, error) {
	if a.mode != "SIMULATED" {
		return nil, newRealModeNotImplemented("AISAdapter", a.mode)
	}
	var out []Evidence
	seq := 0
	for _, r := range aisFixtureRecords {
		if req.VesselID != "" && req.VesselID != r.vesselID {
			continue
		}
		if req.PortID != "" && req.PortID != r.portID {
			continue
		}
		if r.timestamp < req.Since || (req.Until != 0 && r.timestamp > req.Until) {
			continue
		}
		payload := []byte(fmt.Sprintf("AIS|vessel=%s|port=%s|event=%s", r.vesselID, r.portID, r.eventType))
		out = append(out, Evidence{
			EventType:  r.eventType,
			Timestamp:  r.timestamp,
			Location:   r.location,
			DeliveryID: r.vesselID + "@" + r.portID,
			Sequence:   seq,
			RawPayload: payload,
			SourceID:   "AIS_FIXTURE",
			Provider:   "VERIQO_AIS_FIXTURE_ADAPTER",
			Hash:       hashEvidence("ais", r.eventType, r.vesselID, r.timestamp, seq, payload),
		})
		seq++
	}
	return out, nil
}

// ArrivalTick returns the arrival-tick evidence (EventType=="arrival")
// for one vessel/port pair, or ok=false if no such record was fetched.
// This is the single-value convenience wrapper pkg/rwc's
// BuildRWC001CaseWithAIS uses -- Fetch itself stays generic (a caller
// wanting departures, or every record, calls Fetch directly).
func ArrivalTick(records []Evidence) (tick uint64, ok bool) {
	for _, r := range records {
		if r.EventType == "arrival" {
			return r.Timestamp, true
		}
	}
	return 0, false
}

var _ MaritimeEvidenceSource = (*AISAdapter)(nil)
