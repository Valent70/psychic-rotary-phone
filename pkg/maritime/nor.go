package maritime

import (
	"context"
	"fmt"
)

// NORAdapter is a MaritimeEvidenceSource for Notice of Readiness
// tender events. Fixture mode returns a single, deterministic NOR
// record built around RWC-002's own corpus (pkg/rwc.RWC002Corpus): the
// vessel "GUNUNG KEMALA / PERTAMINA 8003", tendering NOR at "Lome
// anchorage" -- the corpus's own literal VoyageClaim text
// ("...currently reported at Lome anchorage") -- and "Notice of
// Readiness" is itself one of RWC002Corpus.DocumentationClaimed's own
// entries, not an invented document type.
//
// This package does not import pkg/rwc (see ais.go's doc comment for
// why); the literal values below are kept in hand-sync with
// pkg/rwc.RWC002Corpus.
//
// Real-file/real-API mode: a real integration would parse a delivered
// NOR document (PDF/scan/EDI) or poll an agency/port system for tender
// timestamps, mapping the tender event onto Evidence the same way
// Fetch does below. Not implemented in this sandbox for the same
// no-egress reason documented in ais.go -- and per rwc002.go's own
// "do NOT convert broker statements into verified facts" discipline,
// this fixture's NOR record is explicitly a CLAIMED document, not
// evidence that the underlying document was independently inspected.
type NORAdapter struct {
	mode string
}

// NewNORAdapterFixture returns an NORAdapter whose Fetch serves the
// built-in NOR fixture.
func NewNORAdapterFixture() *NORAdapter { return &NORAdapter{mode: "SIMULATED"} }

func (a *NORAdapter) SourceID(ctx context.Context) (string, error) { return "NOR_FIXTURE", nil }

func (a *NORAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	return Capabilities{SupportsFixture: true}, nil
}

const (
	norFixtureVesselID = "GUNUNG KEMALA / PERTAMINA 8003" // pkg/rwc.RWC002Corpus.VesselName
	norFixturePortID   = "LOME"                           // pkg/rwc.RWC002Corpus.VoyageClaim: "...currently reported at Lome anchorage"
	norFixtureTick     = aisArrivalTick + 100             // deliberately outside RWC-001's own voyage-tick range: RWC-002 is a separate corpus case, not a continuation of RWC-001's timeline
)

func (a *NORAdapter) Fetch(ctx context.Context, req FetchRequest) ([]Evidence, error) {
	if a.mode != "SIMULATED" {
		return nil, newRealModeNotImplemented("NORAdapter", a.mode)
	}
	if req.VesselID != "" && req.VesselID != norFixtureVesselID {
		return nil, nil
	}
	if req.PortID != "" && req.PortID != norFixturePortID {
		return nil, nil
	}
	if norFixtureTick < req.Since || (req.Until != 0 && norFixtureTick > req.Until) {
		return nil, nil
	}
	payload := []byte(fmt.Sprintf("NOR|vessel=%s|port=%s|event=nor_tendered", norFixtureVesselID, norFixturePortID))
	return []Evidence{{
		EventType:  "nor_tendered",
		Timestamp:  norFixtureTick,
		Location:   norFixturePortID + " anchorage",
		DeliveryID: norFixtureVesselID + "@" + norFixturePortID + "#NOR",
		Sequence:   0,
		RawPayload: payload,
		SourceID:   "NOR_FIXTURE",
		Provider:   "BROKER_TRANSACTION_DOCS", // matches pkg/rwc.RWC002Corpus's own provider naming for claimed (not independently verified) documents
		Hash:       hashEvidence("nor", "nor_tendered", norFixtureVesselID, norFixtureTick, 0, payload),
	}}, nil
}

var _ MaritimeEvidenceSource = (*NORAdapter)(nil)
