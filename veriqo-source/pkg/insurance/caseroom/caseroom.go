// Package caseroom is the Round 4 work order's own named CASE_ROOM
// deliverable (§30): a customer-facing, permissioned view over one
// case's dossier. It reuses the real party.RelationshipRegistry
// authority model this same round built (relationship.go) as its ONLY
// authorization mechanism — no second, case-room-local permission
// engine, matching this codebase's own repeated "don't duplicate an
// engine that already exists" discipline.
//
// Scope note, stated once here rather than left implicit: this is the
// ACCESS-CONTROL CORE of the Case Room, not a rendered UI or a deployed
// HTTP surface — actually deploying a customer-facing web application
// is outside what a coding session can honestly claim to have done.
// What IS honestly buildable and tested here is the real authorization
// logic such a UI would sit on top of: given a relationship registry, a
// specific relationship, and the current tick, decide EXACTLY what that
// party may see of the case, failing closed by default. See
// docs/governance/CASE_ROOM_AND_DOSSIER_VERIFIER_SPECIFICATION.md for
// the full customer-facing surface this core is meant to back.
package caseroom

import (
	"errors"

	"veriqo/pkg/insurance/dossier"
	"veriqo/pkg/insurance/party"
)

// ErrNoAccess is returned whenever the relationship does not currently
// grant access to the case room at all — unknown relationship, not
// EffectiveAt the given tick, or missing PermissionAccessCaseRoom.
// BuildView never returns a partially-populated View alongside this
// error: access is binary at the room's front door, and view content
// is refined only once inside.
var ErrNoAccess = errors.New("caseroom: relationship does not currently grant ACCESS_CASE_ROOM")

// The three errors below are Round 5's own named Case Room Security
// matrix requirements (§11): "tenant A cannot access tenant B", "wrong
// jurisdiction cannot authorize", "wrong scope cannot authorize". Each
// is distinct from ErrNoAccess and from each other, so a caller (and a
// test) can tell WHICH boundary refused the request, not merely that
// one did.
var (
	ErrCrossTenantAccess = errors.New("caseroom: relationship's tenant does not match the required tenant")
	ErrWrongJurisdiction = errors.New("caseroom: relationship's jurisdiction does not match the required jurisdiction")
	ErrWrongScope        = errors.New("caseroom: relationship's scope does not match the required scope")
)

// Section is one named, independently-gated part of a case's dossier a
// Case Room viewer might see.
type Section string

const (
	SectionTimeline    Section = "TIMELINE_CONFLICTS"
	SectionEvidence    Section = "EVIDENCE_SUFFICIENCY"
	SectionCoverage    Section = "COVERAGE_ANALYSIS"
	SectionQuantum     Section = "QUANTUM_CALCULATION"
	SectionRecovery    Section = "RECOVERY_TARGETS"
	SectionDeadlines   Section = "DEADLINES"
	SectionHumanReview Section = "HUMAN_REVIEW_QUESTIONS"
)

// baselineSections are visible to anyone holding PermissionAccessCaseRoom
// alone — no further permission required. gatedSections additionally
// requires the named Permission.
var baselineSections = []Section{SectionTimeline, SectionCoverage, SectionQuantum, SectionDeadlines}

var gatedSections = map[Section]party.Permission{
	SectionEvidence:    party.PermissionViewEvidence,
	SectionRecovery:    party.PermissionReceiveRecovery,
	SectionHumanReview: party.PermissionActInDispute,
}

// View is the redacted, per-viewer projection of one case's dossier.
// Visible lists exactly which Sections this specific relationship, at
// this specific tick, may see — a section present in the underlying
// Dossier but absent from Visible was deliberately withheld, not merely
// forgotten. Content fields are populated ONLY for sections the viewer
// may see; everything else stays zero rather than leaking through a
// populated-but-supposedly-hidden field.
type View struct {
	RelationshipID string     `json:"relationship_id"`
	PartyRole      party.Role `json:"party_role"`
	Visible        []Section  `json:"visible_sections"`

	CaseID string `json:"case_id,omitempty"`
	Status string `json:"status,omitempty"`

	TimelineConflictCount    int `json:"timeline_conflict_count,omitempty"`
	EvidenceIssueCount       int `json:"evidence_issue_count,omitempty"`
	RecoveryTargetCount      int `json:"recovery_target_count,omitempty"`
	DeadlineCount            int `json:"deadline_count,omitempty"`
	HumanReviewQuestionCount int `json:"human_review_question_count,omitempty"`
}

func (v View) canSee(s Section) bool {
	for _, have := range v.Visible {
		if have == s {
			return true
		}
	}
	return false
}

// BuildView constructs the permissioned Case Room view for one
// relationship at one tick. It fails closed: an unknown relationship,
// one not currently EffectiveAt(tick), or one without
// PermissionAccessCaseRoom returns ErrNoAccess and a zero View — never
// a partially-populated one. Within the room, every further section is
// independently gated by its own permission, computed fresh from the
// relationship's CURRENT Permissions — nothing here caches a stale
// grant.
func BuildView(reg *party.RelationshipRegistry, relationshipID string, tick uint64, d *dossier.Dossier) (View, error) {
	rel, ok := reg.Get(relationshipID)
	if !ok || !reg.EffectiveAt(relationshipID, tick) || !rel.HasPermission(party.PermissionAccessCaseRoom) {
		return View{}, ErrNoAccess
	}

	v := View{RelationshipID: relationshipID, PartyRole: rel.Role}
	v.Visible = append(v.Visible, baselineSections...)
	for section, perm := range gatedSections {
		if rel.HasPermission(perm) {
			v.Visible = append(v.Visible, section)
		}
	}

	if d == nil {
		return v, nil
	}
	v.CaseID = d.CaseID
	v.Status = string(d.Status.Stage)
	if v.canSee(SectionTimeline) {
		v.TimelineConflictCount = len(d.TimelineConflicts)
	}
	if v.canSee(SectionEvidence) {
		v.EvidenceIssueCount = len(d.EvidenceSufficiency)
	}
	if v.canSee(SectionRecovery) {
		v.RecoveryTargetCount = len(d.RecoveryTargets)
	}
	if v.canSee(SectionDeadlines) {
		v.DeadlineCount = len(d.Deadlines)
	}
	if v.canSee(SectionHumanReview) {
		v.HumanReviewQuestionCount = len(d.HumanReviewQuestions)
	}
	return v, nil
}

// AuthorizeContext bundles the Case Room Security matrix's contextual
// requirements (Round 5 work order §11) a caller supplies beyond the
// relationship itself — the tenant/jurisdiction/scope a specific
// action is being attempted under. A zero-value field means "no
// restriction on this axis" (matching Relationship's own convention:
// an empty Tenant means "the case's own single tenant", not "match
// nothing").
type AuthorizeContext struct {
	Tenant       string
	Jurisdiction string
	Scope        string
}

// Authorize is the full Case Room Security matrix gate: BuildView's own
// access rule (unknown relationship / not EffectiveAt / missing
// ACCESS_CASE_ROOM -> ErrNoAccess) PLUS three further, independently-
// named checks the order's own adversarial test list requires by name:
//
//   - "tenant A cannot access tenant B": rel.Tenant, if set, must match
//     ctx.Tenant, if required -> ErrCrossTenantAccess.
//   - "wrong jurisdiction cannot authorize": rel.Jurisdiction, if set,
//     must match ctx.Jurisdiction, if required -> ErrWrongJurisdiction.
//   - "wrong scope cannot authorize": rel.Scope, if set, must match
//     ctx.Scope, if required -> ErrWrongScope.
//
// Each check is independent and separately testable: a caller (and a
// test) can tell exactly which boundary refused a request, never just
// "denied". "Revoked authority cannot act" and "expired authority
// cannot act" are already covered by the shared access rule above
// (party.Relationship.EffectiveAt itself enforces both); "delegation
// cannot self-delegate" and "delegation chain cannot exceed policy" are
// enforced earlier, at RelationshipRegistry.Register time, so an
// over-long or self-referential chain can never reach this function at
// all — see party/relationship.go.
//
// A field left empty on EITHER side of a comparison means "not
// restricted on this axis" (an unset Relationship.Tenant is not itself
// a violation, and an unset ctx.Tenant means the caller is not
// requiring tenant isolation for this call) -- matching the same
// "empty means unrestricted" convention Relationship's own fields
// already use, rather than a stricter fail-closed-on-any-empty rule
// that would make every relationship created before this round's
// Tenant/Jurisdiction/Scope fields existed suddenly unauthorizable.
func Authorize(reg *party.RelationshipRegistry, relationshipID string, tick uint64, ctx AuthorizeContext) (party.Relationship, error) {
	rel, ok := reg.Get(relationshipID)
	if !ok || !reg.EffectiveAt(relationshipID, tick) || !rel.HasPermission(party.PermissionAccessCaseRoom) {
		return party.Relationship{}, ErrNoAccess
	}
	if ctx.Tenant != "" && rel.Tenant != "" && rel.Tenant != ctx.Tenant {
		return party.Relationship{}, ErrCrossTenantAccess
	}
	if ctx.Jurisdiction != "" && rel.Jurisdiction != "" && rel.Jurisdiction != ctx.Jurisdiction {
		return party.Relationship{}, ErrWrongJurisdiction
	}
	if ctx.Scope != "" && rel.Scope != "" && rel.Scope != ctx.Scope {
		return party.Relationship{}, ErrWrongScope
	}
	return rel, nil
}
