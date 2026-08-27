package assurance

import "testing"

// TestEveryGateCategoryTargetsARealCategory is a structural check on the
// lookup table itself: every entry must name one of the twelve declared
// categories, so a typo in gateCategory can never silently create a
// thirteenth, unlisted bucket.
func TestEveryGateCategoryTargetsARealCategory(t *testing.T) {
	known := map[ReadinessCategory]bool{}
	for _, c := range AllCategories() {
		known[c] = true
	}
	if len(known) != 12 {
		t.Fatalf("expected 12 categories, got %d", len(known))
	}
	for gate, cat := range gateCategory {
		if !known[cat] {
			t.Errorf("gate %q maps to unknown category %q", gate, cat)
		}
	}
}

// TestComposeTemporaryReadinessNamesUnmappedGates proves an unregistered
// gate ID is surfaced as a real gap (UnmappedGates), never silently
// dropped from the composition.
func TestComposeTemporaryReadinessNamesUnmappedGates(t *testing.T) {
	axes := AxesReport{Gates: []GateAxes{
		{GateID: "totally_unknown_gate", Mandatory: true, Canonical: CanonicalVerifiedInternal},
	}}
	report := ComposeTemporaryReadiness(axes)
	if len(report.UnmappedGates) != 1 || report.UnmappedGates[0] != "totally_unknown_gate" {
		t.Fatalf("expected UnmappedGates=[totally_unknown_gate], got %v", report.UnmappedGates)
	}
}

// TestComposeTemporaryReadinessEmptyCategoryIsNotReady proves a category
// with zero registered gates reports NOT_READY rather than defaulting
// to the map's zero value or silently reading as fully verified.
func TestComposeTemporaryReadinessEmptyCategoryIsNotReady(t *testing.T) {
	// An axes report naming every gate EXCEPT any CASE_ROOM one.
	axes := AxesReport{Gates: []GateAxes{
		{GateID: "build", Mandatory: true, Canonical: CanonicalVerifiedInternal},
	}}
	report := ComposeTemporaryReadiness(axes)
	for _, cc := range report.Categories {
		if cc.Category == CategoryCaseRoom {
			if cc.ComposedStatus != CanonicalNotReady {
				t.Fatalf("CASE_ROOM with zero gates: composed status = %q, want NOT_READY", cc.ComposedStatus)
			}
			if len(cc.GateIDs) != 0 {
				t.Fatalf("expected zero gates for CASE_ROOM in this fixture, got %v", cc.GateIDs)
			}
		}
	}
	if report.Verdict != VerdictNotYetCandidate {
		t.Fatalf("Verdict = %q, want NOT_YET_TEMPORARY_CANDIDATE (an empty mandatory category is a real gap)", report.Verdict)
	}
}

// TestComposeTemporaryReadinessWorstGateWinsPerCategory proves a
// category's composed status is the WORST (least-ready) status among
// its own gates -- one BLOCKED_EXTERNAL gate must not be hidden behind
// nine VERIFIED_INTERNAL siblings in the same category.
func TestComposeTemporaryReadinessWorstGateWinsPerCategory(t *testing.T) {
	axes := AxesReport{Gates: []GateAxes{
		{GateID: "pentest", Mandatory: true, Canonical: CanonicalBlockedExternal},
		{GateID: "hsm_kms", Mandatory: true, Canonical: CanonicalVerifiedInternal}, // won't actually happen in prod, but proves the rule
	}}
	report := ComposeTemporaryReadiness(axes)
	for _, cc := range report.Categories {
		if cc.Category == CategorySecurity {
			if cc.ComposedStatus != CanonicalBlockedExternal {
				t.Fatalf("SECURITY composed status = %q, want BLOCKED_EXTERNAL (the worst gate must win)", cc.ComposedStatus)
			}
			if cc.BlockedExternal != 1 || cc.VerifiedInternal != 1 {
				t.Fatalf("expected 1 blocked + 1 verified, got blocked=%d verified=%d", cc.BlockedExternal, cc.VerifiedInternal)
			}
		}
	}
}

// TestComposeTemporaryReadinessCountsExternallyQualifiedSeparately proves
// the category rollup keeps EXTERNALLY_QUALIFIED as its own bucket,
// never folded into VerifiedInternal, and that it outranks
// VerifiedInternal in the worst-status-wins composition (a category
// with one EXTERNALLY_QUALIFIED gate and nothing worse composes to
// EXTERNALLY_QUALIFIED).
func TestComposeTemporaryReadinessCountsExternallyQualifiedSeparately(t *testing.T) {
	axes := AxesReport{Gates: []GateAxes{
		{GateID: "pentest", Mandatory: true, Canonical: CanonicalExternallyQualified},
	}}
	report := ComposeTemporaryReadiness(axes)
	for _, cc := range report.Categories {
		if cc.Category == CategorySecurity {
			if cc.ExternallyQualified != 1 {
				t.Fatalf("SECURITY ExternallyQualified = %d, want 1", cc.ExternallyQualified)
			}
			if cc.ComposedStatus != CanonicalExternallyQualified {
				t.Fatalf("SECURITY composed status = %q, want EXTERNALLY_QUALIFIED", cc.ComposedStatus)
			}
			if cc.VerifiedInternal != 0 {
				t.Fatalf("SECURITY VerifiedInternal = %d, want 0 -- must not be folded together", cc.VerifiedInternal)
			}
		}
	}
}

// TestComposeTemporaryReadinessCandidateVerdictNeverClaimsFullyQualified
// proves the verdict vocabulary structurally cannot express
// "PRODUCTION_QUALIFIED" or a fabricated "all verified" claim: even in
// the best case (every gate VERIFIED_INTERNAL or
// READY_FOR_EXTERNAL_QUALIFICATION, nothing NOT_READY), the verdict is
// exactly the mandate's own required phrase.
func TestComposeTemporaryReadinessCandidateVerdict(t *testing.T) {
	var gates []GateAxes
	for gid := range gateCategory {
		gates = append(gates, GateAxes{GateID: gid, Mandatory: true, Canonical: CanonicalReadyForExternalQualification})
	}
	report := ComposeTemporaryReadiness(AxesReport{Gates: gates})
	if len(report.UnmappedGates) != 0 {
		t.Fatalf("expected no unmapped gates, got %v", report.UnmappedGates)
	}
	if report.Verdict != VerdictTemporaryCandidate {
		t.Fatalf("Verdict = %q, want TEMPORARY_PRODUCTION_READINESS_CANDIDATE", report.Verdict)
	}
	if string(report.Verdict) == "PRODUCTION_QUALIFIED" {
		t.Fatal("the verdict vocabulary must never equal PRODUCTION_QUALIFIED")
	}
}
