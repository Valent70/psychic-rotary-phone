package caseroom

import (
	"testing"

	"veriqo/pkg/insurance/casepack"
	"veriqo/pkg/insurance/party"
)

func TestBuildViewFailsClosedForUnknownRelationship(t *testing.T) {
	reg, err := party.NewRelationshipRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewRelationshipRegistry: %v", err)
	}
	if _, err := BuildView(reg, "REL-NOPE", 100, nil); err != ErrNoAccess {
		t.Fatalf("expected ErrNoAccess for an unknown relationship, got %v", err)
	}
}

func TestBuildViewFailsClosedWithoutAccessCaseRoomPermission(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	r, err := party.New("REL-1", "CASE-1", "PTY-BROKER", "PTY-INSURED", party.RoleBroker, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.RecordConsent("REL-1", "EV-CONSENT"); err != nil {
		t.Fatalf("RecordConsent: %v", err)
	}
	// Consented and active, but no PermissionAccessCaseRoom granted.
	if _, err := BuildView(reg, "REL-1", 100, nil); err != ErrNoAccess {
		t.Fatalf("expected ErrNoAccess without ACCESS_CASE_ROOM, got %v", err)
	}
}

func TestBuildViewFailsClosedOncePending(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	r, _ := party.New("REL-1", "CASE-1", "PTY-BROKER", "PTY-INSURED", party.RoleBroker, 0)
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.GrantPermissions("REL-1", party.PermissionAccessCaseRoom); err != nil {
		t.Fatalf("GrantPermissions: %v", err)
	}
	// Never consented -> stays PENDING -> never EffectiveAt.
	if _, err := BuildView(reg, "REL-1", 100, nil); err != ErrNoAccess {
		t.Fatalf("expected ErrNoAccess for a PENDING (unconsented) relationship, got %v", err)
	}
}

// TestBuildViewGatesEachSectionIndependently is the core proof: two
// relationships with different granted permissions see different
// Section sets from the SAME dossier, and content fields are populated
// only where Visible actually lists that section.
func TestBuildViewGatesEachSectionIndependently(t *testing.T) {
	gr, err := casepack.DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	if gr.Dossier == nil {
		t.Fatal("expected the golden case to have produced a dossier")
	}

	reg, _ := party.NewRelationshipRegistry(string(gr.CaseID))

	minimal, err := party.New("REL-MINIMAL", string(gr.CaseID), "PTY-OUTSIDER", "PTY-INSURED", party.RoleAuditor, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(minimal); err != nil {
		t.Fatalf("Register(minimal): %v", err)
	}
	if err := reg.GrantPermissions("REL-MINIMAL", party.PermissionAccessCaseRoom); err != nil {
		t.Fatalf("GrantPermissions(minimal): %v", err)
	}
	if err := reg.RecordConsent("REL-MINIMAL", "EV-CONSENT-MINIMAL"); err != nil {
		t.Fatalf("RecordConsent(minimal): %v", err)
	}

	full, err := party.New("REL-FULL", string(gr.CaseID), "PTY-ADJUSTER", "PTY-INSURED", party.RoleLossAdjuster, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(full); err != nil {
		t.Fatalf("Register(full): %v", err)
	}
	if err := reg.GrantPermissions("REL-FULL",
		party.PermissionAccessCaseRoom, party.PermissionViewEvidence,
		party.PermissionReceiveRecovery, party.PermissionActInDispute); err != nil {
		t.Fatalf("GrantPermissions(full): %v", err)
	}
	if err := reg.RecordConsent("REL-FULL", "EV-CONSENT-FULL"); err != nil {
		t.Fatalf("RecordConsent(full): %v", err)
	}

	minView, err := BuildView(reg, "REL-MINIMAL", 500, gr.Dossier)
	if err != nil {
		t.Fatalf("BuildView(minimal): %v", err)
	}
	if minView.canSee(SectionEvidence) || minView.canSee(SectionRecovery) || minView.canSee(SectionHumanReview) {
		t.Fatalf("minimal relationship must not see gated sections, got %v", minView.Visible)
	}
	if !minView.canSee(SectionTimeline) || !minView.canSee(SectionQuantum) {
		t.Fatalf("minimal relationship must still see baseline sections, got %v", minView.Visible)
	}
	if minView.EvidenceIssueCount != 0 {
		t.Fatalf("minimal relationship's EvidenceIssueCount must stay zero (redacted), got %d", minView.EvidenceIssueCount)
	}

	fullView, err := BuildView(reg, "REL-FULL", 500, gr.Dossier)
	if err != nil {
		t.Fatalf("BuildView(full): %v", err)
	}
	if !fullView.canSee(SectionEvidence) || !fullView.canSee(SectionRecovery) || !fullView.canSee(SectionHumanReview) {
		t.Fatalf("full relationship must see every gated section, got %v", fullView.Visible)
	}
	if fullView.CaseID != gr.Dossier.CaseID {
		t.Fatalf("expected CaseID %q, got %q", gr.Dossier.CaseID, fullView.CaseID)
	}
}

// TestBuildViewOnGoldenBrokerRelationship exercises the real broker
// relationship the golden case itself registers (party.go's own
// attachRelationships), rather than only a purpose-built fixture.
func TestBuildViewOnGoldenBrokerRelationship(t *testing.T) {
	gr, err := casepack.DriveGolden()
	if err != nil {
		t.Fatalf("DriveGolden: %v", err)
	}
	brokerRel, ok := gr.Relationships.Get(gr.BrokerRelationshipID)
	if !ok {
		t.Fatal("expected the golden case's broker relationship to be registered")
	}
	view, err := BuildView(gr.Relationships, gr.BrokerRelationshipID, 500, gr.Dossier)
	if brokerRel.HasPermission(party.PermissionAccessCaseRoom) {
		if err != nil {
			t.Fatalf("BuildView(broker): %v", err)
		}
		if view.RelationshipID != gr.BrokerRelationshipID {
			t.Fatalf("expected RelationshipID %q, got %q", gr.BrokerRelationshipID, view.RelationshipID)
		}
	} else if err != ErrNoAccess {
		t.Fatalf("broker relationship has no ACCESS_CASE_ROOM permission; expected ErrNoAccess, got %v", err)
	}
}

func TestBuildViewWithNilDossierStillReportsPermittedSections(t *testing.T) {
	reg, _ := party.NewRelationshipRegistry("CASE-1")
	r, _ := party.New("REL-1", "CASE-1", "PTY-BROKER", "PTY-INSURED", party.RoleBroker, 0)
	if err := reg.Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.GrantPermissions("REL-1", party.PermissionAccessCaseRoom); err != nil {
		t.Fatalf("GrantPermissions: %v", err)
	}
	if err := reg.RecordConsent("REL-1", "EV-CONSENT"); err != nil {
		t.Fatalf("RecordConsent: %v", err)
	}
	v, err := BuildView(reg, "REL-1", 100, nil)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	if len(v.Visible) == 0 {
		t.Fatal("expected baseline sections to be visible even with no dossier")
	}
	if v.CaseID != "" {
		t.Fatalf("expected empty CaseID with a nil dossier, got %q", v.CaseID)
	}
}

func TestRunAssurancePasses(t *testing.T) {
	s := RunAssurance()
	if !s.Pass() {
		t.Fatalf("RunAssurance did not pass: %+v", s)
	}
	if !s.UnknownRelationshipRefused || !s.MissingPermissionRefused || !s.PendingRelationshipRefused ||
		!s.SectionsGatedIndependently || !s.RedactedContentStaysZero {
		t.Fatalf("expected every assurance check to be true, got %+v", s)
	}
}
