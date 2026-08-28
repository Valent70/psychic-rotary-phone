package assurance

import "testing"

// allCategoryGates builds one GateAxes per real, registered gate ID (so
// every one of the twelve categories has at least one gate and none of
// ComposeTemporaryReadiness's own empty-category NOT_READY rule fires
// for reasons unrelated to what a given test is actually exercising),
// with every gate given the same Engineering/Canonical pair.
func allCategoryGates(engineering AxisStatus, canonical CanonicalStatus) []GateAxes {
	var gates []GateAxes
	for gid := range gateCategory {
		gates = append(gates, GateAxes{
			GateID: gid, Mandatory: true,
			Engineering: engineering, Canonical: canonical,
		})
	}
	return gates
}

// TestProductionReadinessLevelBelowEngineeringReadyWhenAGateFails proves
// the floor: any mandatory gate whose own ENGINEERING axis has not
// passed reports BELOW_ENGINEERING_READY, regardless of what every
// other gate says.
func TestProductionReadinessLevelBelowEngineeringReadyWhenAGateFails(t *testing.T) {
	axes := AxesReport{Gates: []GateAxes{
		{GateID: "build", Mandatory: true, Engineering: AxisFail, Canonical: CanonicalNotReady},
	}}
	tpr := ComposeTemporaryReadiness(axes)
	rep := ComposeProductionReadinessLevel(axes, tpr)
	if rep.Level != LevelBelowEngineeringReady {
		t.Fatalf("Level = %q, want BELOW_ENGINEERING_READY", rep.Level)
	}
	if rep.MandatoryEngineeringPassing != 0 || rep.MandatoryEngineeringTotal != 1 {
		t.Fatalf("engineering counts = %d/%d, want 0/1", rep.MandatoryEngineeringPassing, rep.MandatoryEngineeringTotal)
	}
}

// TestProductionReadinessLevelEngineeringReadyButNotYetCandidate proves
// Level 1 alone: every gate's engineering passes, but at least one gate
// is genuinely NOT_READY (not merely externally blocked), so the
// temporary-candidate ceiling has not been earned.
func TestProductionReadinessLevelEngineeringReadyButNotYetCandidate(t *testing.T) {
	axes := AxesReport{Gates: allCategoryGates(AxisPass, CanonicalNotReady)}
	tpr := ComposeTemporaryReadiness(axes)
	if tpr.Verdict != VerdictNotYetCandidate {
		t.Fatalf("fixture bug: tpr.Verdict = %q, want NOT_YET_TEMPORARY_CANDIDATE", tpr.Verdict)
	}
	rep := ComposeProductionReadinessLevel(axes, tpr)
	if rep.Level != LevelEngineeringReady {
		t.Fatalf("Level = %q, want ENGINEERING_READY", rep.Level)
	}
	if rep.MandatoryEngineeringPassing != rep.MandatoryEngineeringTotal {
		t.Fatalf("expected all engineering gates passing, got %d/%d", rep.MandatoryEngineeringPassing, rep.MandatoryEngineeringTotal)
	}
}

// TestProductionReadinessLevelTemporaryProductionReadinessWithRemainingGaps
// proves Level 2: engineering and internal qualification are both
// complete (every gate READY_FOR_EXTERNAL_QUALIFICATION, none
// NOT_READY), so the honest ceiling is TEMPORARY_PRODUCTION_READINESS —
// and RemainingForLevel3 names every gate still short of Level 3,
// proving the report never silently claims more than it can support.
func TestProductionReadinessLevelTemporaryProductionReadinessWithRemainingGaps(t *testing.T) {
	axes := AxesReport{Gates: allCategoryGates(AxisPass, CanonicalReadyForExternalQualification)}
	tpr := ComposeTemporaryReadiness(axes)
	rep := ComposeProductionReadinessLevel(axes, tpr)
	if rep.Level != LevelTemporaryProductionReadiness {
		t.Fatalf("Level = %q, want TEMPORARY_PRODUCTION_READINESS", rep.Level)
	}
	if len(rep.RemainingForLevel3) != len(axes.Gates) {
		t.Fatalf("RemainingForLevel3 = %d gates, want all %d gates named (none reached Level 3 yet)",
			len(rep.RemainingForLevel3), len(axes.Gates))
	}
}

// TestProductionReadinessLevelProductionQualifiedOnlyWhenEveryGateClears
// proves Level 3 can only be reached when every single gate is
// VERIFIED_INTERNAL or EXTERNALLY_QUALIFIED -- and, critically, that
// this cannot be produced by internal engineering alone: it requires
// the SAME CanonicalExternallyQualified/CanonicalVerifiedInternal
// values axes.go itself only ever assigns from real evidence, not a
// value this file invents independently.
func TestProductionReadinessLevelProductionQualifiedOnlyWhenEveryGateClears(t *testing.T) {
	axes := AxesReport{Gates: allCategoryGates(AxisPass, CanonicalVerifiedInternal)}
	tpr := ComposeTemporaryReadiness(axes)
	rep := ComposeProductionReadinessLevel(axes, tpr)
	if rep.Level != LevelProductionQualified {
		t.Fatalf("Level = %q, want PRODUCTION_QUALIFIED", rep.Level)
	}
	if len(rep.RemainingForLevel3) != 0 {
		t.Fatalf("RemainingForLevel3 = %v, want empty", rep.RemainingForLevel3)
	}
}

// TestProductionReadinessLevelOneBlockedGateDeniesLevel3 proves a
// single BLOCKED_EXTERNAL gate among an otherwise fully verified
// release still denies Level 3 -- mirroring gate.go's own
// "one critical gap cannot be compensated" rule at the level-hierarchy
// layer.
func TestProductionReadinessLevelOneBlockedGateDeniesLevel3(t *testing.T) {
	gates := allCategoryGates(AxisPass, CanonicalVerifiedInternal)
	gates[0].Canonical = CanonicalBlockedExternal
	axes := AxesReport{Gates: gates}
	tpr := ComposeTemporaryReadiness(axes)
	rep := ComposeProductionReadinessLevel(axes, tpr)
	if rep.Level == LevelProductionQualified {
		t.Fatal("a single BLOCKED_EXTERNAL gate must never allow PRODUCTION_QUALIFIED")
	}
	if len(rep.RemainingForLevel3) != 1 || rep.RemainingForLevel3[0] != gates[0].GateID {
		t.Fatalf("RemainingForLevel3 = %v, want exactly [%s]", rep.RemainingForLevel3, gates[0].GateID)
	}
}

// TestProductionReadinessLevelVocabularyNeverOverlapsCanonicalStatus
// guards against the exact drift this program's own governing rule
// forbids: ProductionReadinessLevel is a DIFFERENT vocabulary answering
// a different question (how far the WHOLE RELEASE has come) from
// CanonicalStatus (what ONE GATE has reached), and the two must never
// share a literal string value that could let a reader confuse them.
func TestProductionReadinessLevelVocabularyNeverOverlapsCanonicalStatus(t *testing.T) {
	levels := []ProductionReadinessLevel{
		LevelBelowEngineeringReady, LevelEngineeringReady,
		LevelTemporaryProductionReadiness, LevelProductionQualified,
	}
	canonicals := []CanonicalStatus{
		CanonicalNotReady, CanonicalBlockedExternal,
		CanonicalReadyForExternalQualification, CanonicalVerifiedInternal,
		CanonicalExternallyQualified,
	}
	for _, l := range levels {
		for _, c := range canonicals {
			if string(l) == string(c) {
				t.Fatalf("ProductionReadinessLevel %q collides with CanonicalStatus %q", l, c)
			}
		}
	}
}
