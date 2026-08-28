package caseroom

import (
	"fmt"

	"veriqo/pkg/insurance/casepack"
)

// This file closes the Round 5 work order's §28 "Golden Case — Final
// Integration Test" requirement: the golden cross-domain case must
// exercise Authorization and Case Room as real steps in its own chain,
// not merely as a separately-tested fixture disconnected from it --
// "INGEST -> VERIFY -> CORROBORATE -> ANALYZE -> INSURANCE -> QUANTUM
// -> REVIEW -> AUTHORIZE -> DOSSIER -> EXPORT -> COLD REPLAY ->
// IDENTICAL RESULT".
//
// It is a caseroom-package file, not a casepack one, because
// pkg/insurance/caseroom/assurance.go already imports casepack (to
// drive the golden case for its own RunAssurance check) -- casepack
// importing caseroom back would be a cycle. Layering here, on top of
// casepack's own already-exported DriveGolden/GoldenColdReplay, is
// exactly the same pattern golden.go itself uses to layer geospatial/
// relationship/salvage/participation/dispute proof on top of the base
// Drive() case: an independent extension, never a fork.

// GoldenCaseRoomResult is the golden case's own Result, plus the
// AUTHORIZE and CASE ROOM steps the order's chain names explicitly.
type GoldenCaseRoomResult struct {
	*casepack.GoldenResult

	// AuthorizedRelationshipID names the relationship Authorize
	// actually cleared -- the golden case's own broker relationship,
	// which attachRelationships (golden.go) grants
	// PermissionAccessCaseRoom specifically so this step has something
	// real to authorize against.
	AuthorizedRelationshipID string
	// View is the permissioned Case Room view BuildView produced for
	// that same relationship.
	View View
}

// DriveGoldenWithCaseRoom drives the golden cross-domain case (the same
// real casepack.DriveGolden every other round's proof uses) and then
// runs it through Authorize and BuildView -- the two steps the order's
// own chain requires that casepack itself cannot perform (the import
// direction only runs the other way).
func DriveGoldenWithCaseRoom() (*GoldenCaseRoomResult, error) {
	gr, err := casepack.DriveGolden()
	if err != nil {
		return nil, fmt.Errorf("caseroom: driving golden case: %w", err)
	}
	return attachCaseRoom(gr)
}

// GoldenColdReplayWithCaseRoom is DriveGoldenWithCaseRoom's cold-replay
// counterpart: it exports the golden case, discards the live state,
// reconstructs and replays it (casepack.GoldenColdReplay's own real
// export/discard/reconstruct/replay cycle), THEN re-runs Authorize and
// BuildView against the REPLAYED result -- proving the order's full
// chain reproduces an IDENTICAL result end to end, authorization and
// case room view included, not merely the domain figures golden.go
// itself already proved cold-replay for.
func GoldenColdReplayWithCaseRoom() (*GoldenCaseRoomResult, casepack.ColdReplayReport, error) {
	replayed, report, err := casepack.GoldenColdReplay()
	if err != nil {
		return nil, report, fmt.Errorf("caseroom: golden cold replay: %w", err)
	}
	result, err := attachCaseRoom(replayed)
	return result, report, err
}

func attachCaseRoom(gr *casepack.GoldenResult) (*GoldenCaseRoomResult, error) {
	rel, err := Authorize(gr.Relationships, gr.BrokerRelationshipID, 500, AuthorizeContext{})
	if err != nil {
		return nil, fmt.Errorf("caseroom: authorizing the golden case's own broker relationship: %w", err)
	}
	view, err := BuildView(gr.Relationships, gr.BrokerRelationshipID, 500, gr.Dossier)
	if err != nil {
		return nil, fmt.Errorf("caseroom: building the golden case's own case room view: %w", err)
	}
	return &GoldenCaseRoomResult{
		GoldenResult:             gr,
		AuthorizedRelationshipID: rel.RelationshipID,
		View:                     view,
	}, nil
}
