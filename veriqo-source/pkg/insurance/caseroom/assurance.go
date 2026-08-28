package caseroom

import (
	"veriqo/pkg/insurance/casepack"
	"veriqo/pkg/insurance/party"
)

// AssuranceSummary is the operational, cmd/veriqo-readiness-facing
// proof that BuildView's fail-closed access control genuinely works —
// the same three properties the unit tests check, re-run here as a
// standalone function so the readiness gate calls the SAME real
// scenarios rather than re-asserting a unit test result by name.
type AssuranceSummary struct {
	// UnknownRelationshipRefused: an unregistered relationship ID gets
	// ErrNoAccess, never a populated View.
	UnknownRelationshipRefused bool `json:"unknown_relationship_refused"`
	// MissingPermissionRefused: a consented, active relationship WITHOUT
	// PermissionAccessCaseRoom still gets ErrNoAccess.
	MissingPermissionRefused bool `json:"missing_permission_refused"`
	// PendingRelationshipRefused: a relationship with the permission but
	// no recorded consent (still PENDING) still gets ErrNoAccess.
	PendingRelationshipRefused bool `json:"pending_relationship_refused"`
	// SectionsGatedIndependently: on the real golden case, a minimal
	// relationship and a fully-permissioned one see DIFFERENT Section
	// sets from the identical dossier.
	SectionsGatedIndependently bool `json:"sections_gated_independently"`
	// RedactedContentStaysZero: a section the viewer cannot see reports
	// a zero content count, never the underlying (larger) real count.
	RedactedContentStaysZero bool `json:"redacted_content_stays_zero"`

	Failures []string `json:"failures,omitempty"`
}

// Pass is derived from Failures — never settable directly.
func (s AssuranceSummary) Pass() bool { return len(s.Failures) == 0 }

// RunAssurance exercises BuildView's fail-closed guarantees against a
// fresh registry plus the real golden cross-domain case, and reports
// what actually happened rather than what was merely asserted.
func RunAssurance() AssuranceSummary {
	s := AssuranceSummary{}

	// ---- fail-closed: unknown relationship ----
	reg, _ := party.NewRelationshipRegistry("CASE-ASSURANCE")
	if _, err := BuildView(reg, "REL-NOPE", 100, nil); err == ErrNoAccess {
		s.UnknownRelationshipRefused = true
	} else {
		s.Failures = append(s.Failures, "an unknown relationship did not return ErrNoAccess")
	}

	// ---- fail-closed: consented but no PermissionAccessCaseRoom ----
	r, err := party.New("REL-NO-PERM", "CASE-ASSURANCE", "PTY-A", "PTY-B", party.RoleBroker, 0)
	if err == nil {
		if err := reg.Register(r); err == nil {
			if err := reg.RecordConsent("REL-NO-PERM", "EV-CONSENT"); err == nil {
				if _, verr := BuildView(reg, "REL-NO-PERM", 100, nil); verr == ErrNoAccess {
					s.MissingPermissionRefused = true
				} else {
					s.Failures = append(s.Failures, "a relationship without ACCESS_CASE_ROOM did not return ErrNoAccess")
				}
			} else {
				s.Failures = append(s.Failures, "RecordConsent: "+err.Error())
			}
		} else {
			s.Failures = append(s.Failures, "Register(no-perm): "+err.Error())
		}
	} else {
		s.Failures = append(s.Failures, "party.New(no-perm): "+err.Error())
	}

	// ---- fail-closed: permission granted but never consented (PENDING) ----
	rp, err := party.New("REL-PENDING", "CASE-ASSURANCE", "PTY-C", "PTY-D", party.RoleBroker, 0)
	if err == nil {
		if err := reg.Register(rp); err == nil {
			if err := reg.GrantPermissions("REL-PENDING", party.PermissionAccessCaseRoom); err == nil {
				if _, verr := BuildView(reg, "REL-PENDING", 100, nil); verr == ErrNoAccess {
					s.PendingRelationshipRefused = true
				} else {
					s.Failures = append(s.Failures, "a PENDING relationship did not return ErrNoAccess")
				}
			} else {
				s.Failures = append(s.Failures, "GrantPermissions(pending): "+err.Error())
			}
		} else {
			s.Failures = append(s.Failures, "Register(pending): "+err.Error())
		}
	} else {
		s.Failures = append(s.Failures, "party.New(pending): "+err.Error())
	}

	// ---- real independent gating on the golden cross-domain case ----
	gr, err := casepack.DriveGolden()
	if err != nil {
		s.Failures = append(s.Failures, "DriveGolden: "+err.Error())
		return s
	}
	if gr.Dossier == nil {
		s.Failures = append(s.Failures, "golden case produced no dossier")
		return s
	}

	goldenReg, _ := party.NewRelationshipRegistry(string(gr.CaseID))
	minimal, _ := party.New("REL-MIN", string(gr.CaseID), "PTY-X", "PTY-Y", party.RoleAuditor, 0)
	_ = goldenReg.Register(minimal)
	_ = goldenReg.GrantPermissions("REL-MIN", party.PermissionAccessCaseRoom)
	_ = goldenReg.RecordConsent("REL-MIN", "EV-MIN")

	full, _ := party.New("REL-FULL", string(gr.CaseID), "PTY-Z", "PTY-Y", party.RoleLossAdjuster, 0)
	_ = goldenReg.Register(full)
	_ = goldenReg.GrantPermissions("REL-FULL",
		party.PermissionAccessCaseRoom, party.PermissionViewEvidence)
	_ = goldenReg.RecordConsent("REL-FULL", "EV-FULL")

	minView, err := BuildView(goldenReg, "REL-MIN", 500, gr.Dossier)
	if err != nil {
		s.Failures = append(s.Failures, "BuildView(minimal) on the golden case: "+err.Error())
		return s
	}
	fullView, err := BuildView(goldenReg, "REL-FULL", 500, gr.Dossier)
	if err != nil {
		s.Failures = append(s.Failures, "BuildView(full) on the golden case: "+err.Error())
		return s
	}

	if !minView.canSee(SectionEvidence) && fullView.canSee(SectionEvidence) {
		s.SectionsGatedIndependently = true
	} else {
		s.Failures = append(s.Failures, "the minimal and fully-permissioned views did not diverge on SectionEvidence")
	}

	if minView.EvidenceIssueCount == 0 && fullView.EvidenceIssueCount == len(gr.Dossier.EvidenceSufficiency) {
		s.RedactedContentStaysZero = true
	} else {
		s.Failures = append(s.Failures, "redacted EvidenceIssueCount was not zero, or the permitted view did not report the real count")
	}

	return s
}
