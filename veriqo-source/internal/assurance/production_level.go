package assurance

// This file answers the Round 7 work order's explicit §XX request: stop
// reporting readiness as a single verdict, and instead make the three
// distinct claims a reader actually needs to tell apart:
//
//	Level 1  ENGINEERING_READY               the code exists and its own
//	                                          gates pass -- says nothing
//	                                          about qualification at all.
//	Level 2  TEMPORARY_PRODUCTION_READINESS  engineering is done AND every
//	                                          gate has at least reached an
//	                                          internal qualification
//	                                          ceiling -- no gate is simply
//	                                          NOT_READY. Remaining gaps are
//	                                          real, named, external ones.
//	Level 3  PRODUCTION_QUALIFIED             every gate that ever had an
//	                                          external dependency has real,
//	                                          signed external evidence
//	                                          closing it. No gate is
//	                                          BLOCKED_EXTERNAL or merely
//	                                          READY_FOR_EXTERNAL_QUALIFICATION.
//
// Like every other verdict in this package, ProductionReadinessLevel is
// DERIVED from AxesReport and TemporaryReadinessReport -- there is no
// setter, and nothing in cmd/veriqo-readiness or anywhere else can move
// this value except by genuinely changing gate evidence. Level 3
// structurally requires ExternallyQualifiedCount + VerifiedInternal to
// equal the whole gate count, which as of this release is impossible
// without real external qualification evidence actually attached (see
// axes.go's AxisExternalQualified derivation) -- so this file cannot,
// by construction, report Level 3 for a fabricated pass.

// ProductionReadinessLevel is the closed, three-value hierarchy.
type ProductionReadinessLevel string

const (
	// LevelEngineeringReady: every mandatory gate's ENGINEERING axis is
	// PASS. This says the code exists and runs; it makes no claim at all
	// about qualification, internal or external.
	LevelEngineeringReady ProductionReadinessLevel = "ENGINEERING_READY"
	// LevelTemporaryProductionReadiness: Level 1 holds, and
	// ComposeTemporaryReadiness's own verdict is
	// TEMPORARY_PRODUCTION_READINESS_CANDIDATE -- no gate is NOT_READY.
	// This is the honest ceiling a sandboxed engineering engagement can
	// reach on its own; it is NOT a claim that VERIQO has been proven in
	// a real production environment.
	LevelTemporaryProductionReadiness ProductionReadinessLevel = "TEMPORARY_PRODUCTION_READINESS"
	// LevelProductionQualified: every gate that ever carried an external
	// dependency has been closed by real external evidence
	// (AxisExternalQualified) -- zero gates remain
	// READY_FOR_EXTERNAL_QUALIFICATION or BLOCKED_EXTERNAL. This value
	// cannot be reached by internal engineering or internal qualification
	// work alone, no matter how thorough, because canonicalStatus (see
	// axes.go) only ever assigns CanonicalExternallyQualified when real,
	// release-bound, signed external evidence was attached through
	// pkg/governance/qualification.
	LevelProductionQualified ProductionReadinessLevel = "PRODUCTION_QUALIFIED"
	// LevelBelowEngineeringReady: at least one mandatory gate's own
	// ENGINEERING axis has not passed. The honest floor -- there is a
	// real, unresolved engineering gap, not merely an external one.
	LevelBelowEngineeringReady ProductionReadinessLevel = "BELOW_ENGINEERING_READY"
)

// ProductionReadinessReport names the level reached, plus the exact
// counts that justify it, so a reader never has to trust the label
// alone.
type ProductionReadinessReport struct {
	Level ProductionReadinessLevel `json:"level"`

	// MandatoryEngineeringPassing / MandatoryEngineeringTotal justify
	// Level 1.
	MandatoryEngineeringPassing int `json:"mandatory_engineering_passing"`
	MandatoryEngineeringTotal   int `json:"mandatory_engineering_total"`

	// TemporaryReadinessVerdict is ComposeTemporaryReadiness's own
	// verdict, echoed here so Level 2's justification is never a
	// second, independently-computed opinion.
	TemporaryReadinessVerdict TemporaryReadinessVerdict `json:"temporary_readiness_verdict"`

	// RemainingForLevel3 lists every gate ID still short of
	// EXTERNALLY_QUALIFIED or VERIFIED_INTERNAL -- exactly what would
	// need real external evidence for Level 3 to be reachable. Empty
	// only when Level is PRODUCTION_QUALIFIED.
	RemainingForLevel3 []string `json:"remaining_for_level_3,omitempty"`

	// Note explains, in one sentence, why Level is what it is.
	Note string `json:"note"`
}

// ComposeProductionReadinessLevel derives the three-level report from
// an already-computed AxesReport and TemporaryReadinessReport. Neither
// input is recomputed here -- this function only reads their already-
// derived fields, so it cannot disagree with the reports it is built
// from.
func ComposeProductionReadinessLevel(axes AxesReport, tpr TemporaryReadinessReport) ProductionReadinessReport {
	mandatoryTotal, mandatoryPassing := 0, 0
	var remaining []string
	for _, g := range axes.Gates {
		if g.Mandatory {
			mandatoryTotal++
			if g.Engineering == AxisPass {
				mandatoryPassing++
			}
		}
		if g.Canonical != CanonicalVerifiedInternal && g.Canonical != CanonicalExternallyQualified {
			remaining = append(remaining, g.GateID)
		}
	}

	rep := ProductionReadinessReport{
		MandatoryEngineeringPassing: mandatoryPassing,
		MandatoryEngineeringTotal:   mandatoryTotal,
		TemporaryReadinessVerdict:   tpr.Verdict,
		RemainingForLevel3:          remaining,
	}

	switch {
	case mandatoryPassing < mandatoryTotal:
		rep.Level = LevelBelowEngineeringReady
		rep.Note = "at least one mandatory gate's own ENGINEERING axis has not passed -- a real, unresolved engineering gap"
	case tpr.Verdict != VerdictTemporaryCandidate:
		rep.Level = LevelEngineeringReady
		rep.Note = "engineering passes, but at least one gate is NOT_READY on its own merits, so even the temporary-candidate ceiling is not yet earned"
	case len(remaining) == 0:
		rep.Level = LevelProductionQualified
		rep.Note = "every gate that ever carried an external dependency has real, signed external evidence closing it"
	default:
		rep.Level = LevelTemporaryProductionReadiness
		rep.Note = "engineering and internal qualification are both complete; every remaining gap is a real, named external dependency awaiting real external evidence -- this is the honest ceiling a sandboxed engagement can reach on its own"
	}
	return rep
}
