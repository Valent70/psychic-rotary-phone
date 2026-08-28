package caseroom

import "testing"

// TestDriveGoldenWithCaseRoomSucceeds proves the full chain (§28's
// worked example: ... -> AUTHORIZE -> DOSSIER -> ...) runs end to end
// with no error, against the real golden cross-domain case.
func TestDriveGoldenWithCaseRoomSucceeds(t *testing.T) {
	gr, err := DriveGoldenWithCaseRoom()
	if err != nil {
		t.Fatalf("DriveGoldenWithCaseRoom: %v", err)
	}
	if gr.AuthorizedRelationshipID != gr.BrokerRelationshipID {
		t.Fatalf("expected the authorized relationship to be the golden case's own broker relationship %q, got %q",
			gr.BrokerRelationshipID, gr.AuthorizedRelationshipID)
	}
	if !gr.View.canSee(SectionEvidence) {
		t.Fatal("expected the broker's own granted VIEW_EVIDENCE permission to be reflected in the case room view")
	}
}

// TestGoldenColdReplayWithCaseRoomReproducesIdenticalAuthorization is
// the order's own required proof: EXPORT -> DELETE LIVE STATE ->
// IMPORT -> COLD REPLAY -> COMPARE must yield the SAME authorization
// trace and the SAME dossier -- not merely the same domain figures
// golden_test.go (casepack) already proves cold-replay-stable.
func TestGoldenColdReplayWithCaseRoomReproducesIdenticalAuthorization(t *testing.T) {
	live, err := DriveGoldenWithCaseRoom()
	if err != nil {
		t.Fatalf("DriveGoldenWithCaseRoom (live): %v", err)
	}
	replayed, report, err := GoldenColdReplayWithCaseRoom()
	if err != nil {
		t.Fatalf("GoldenColdReplayWithCaseRoom: %v", err)
	}
	if !report.Pass() {
		t.Fatalf("underlying cold replay report did not pass: %v", report.Failures)
	}

	if live.AuthorizedRelationshipID != replayed.AuthorizedRelationshipID {
		t.Fatalf("authorized relationship ID diverged: live=%s replayed=%s",
			live.AuthorizedRelationshipID, replayed.AuthorizedRelationshipID)
	}
	if len(live.View.Visible) != len(replayed.View.Visible) {
		t.Fatalf("case room view section count diverged: live=%v replayed=%v", live.View.Visible, replayed.View.Visible)
	}
	for i := range live.View.Visible {
		if live.View.Visible[i] != replayed.View.Visible[i] {
			t.Fatalf("case room view section %d diverged: live=%v replayed=%v", i, live.View.Visible, replayed.View.Visible)
		}
	}
	if live.View.EvidenceIssueCount != replayed.View.EvidenceIssueCount {
		t.Fatalf("case room view EvidenceIssueCount diverged: live=%d replayed=%d",
			live.View.EvidenceIssueCount, replayed.View.EvidenceIssueCount)
	}
	if live.Manifest.EvidenceRootHash != replayed.Manifest.EvidenceRootHash {
		t.Fatal("evidence root hash diverged between live and cold-replayed authorization chain")
	}
}

// TestGoldenCaseRoomFailsClosedForARevokedBroker proves the full
// chain's own AUTHORIZE step -- not just the standalone caseroom
// package -- genuinely fails closed once authority is withdrawn, using
// the real golden case rather than a synthetic fixture.
func TestGoldenCaseRoomFailsClosedForARevokedBroker(t *testing.T) {
	gr, err := DriveGoldenWithCaseRoom()
	if err != nil {
		t.Fatalf("DriveGoldenWithCaseRoom: %v", err)
	}
	if err := gr.Relationships.Revoke(gr.BrokerRelationshipID, 600, "engagement terminated"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := Authorize(gr.Relationships, gr.BrokerRelationshipID, 700, AuthorizeContext{}); err != ErrNoAccess {
		t.Fatalf("expected ErrNoAccess for the golden case's own broker once revoked, got %v", err)
	}
}
