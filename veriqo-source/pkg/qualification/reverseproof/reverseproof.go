// Package reverseproof implements reverse proof (MIP-001 W4.1,
// EQF-001 §3): for a material claim, derive what the world would look
// like if the claim were true, then measure the gap against what was
// actually obtained.
//
// THE INVERSION. Ordinary analytics starts from data and asks what it
// shows. Reverse proof starts from the claim and asks what SHOULD be
// observable if it holds -- then treats every unobtained requirement
// as a named gap rather than as silence. This is what prevents a
// finding from outrunning its evidence: the missing items are part of
// the output, not absent from it.
//
// Seven questions, per the specification:
//
//	What must be true?
//	What should we observe?
//	What evidence would contradict it?
//	What alternative explains the same observations?
//	What evidence is missing?
//	What evidence is unavailable?
//	What evidence would most reduce uncertainty?
//
// The output is an EvidenceRequirementSet -- a list of things that
// would settle the question, each with its obtained/unobtainable/
// unattempted status.
package reverseproof

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/qualification/observability"
)

var (
	ErrEmptyClaim           = errors.New("reverseproof: claim must be non-empty")
	ErrNoConditions         = errors.New("reverseproof: a material claim must state at least one condition that must be true")
	ErrNoRequirements       = errors.New("reverseproof: conditions produced no evidence requirements")
	ErrDuplicateRequirement = errors.New("reverseproof: duplicate requirement ID")
)

// Status is a requirement's fulfilment state.
type Status string

const (
	// Unattempted is the zero value: a requirement nobody pursued.
	// Making this the zero value means a requirement added but never
	// tracked reads as an open gap, not as satisfied.
	Unattempted Status = "UNATTEMPTED"
	// Obtained: the evidence was acquired.
	Obtained Status = "OBTAINED"
	// Unobtainable: sought and demonstrably not acquirable.
	Unobtainable Status = "UNOBTAINABLE"
	// ObservedAbsent: sought, with a passing observability gate, and
	// genuinely not there. This is a POSITIVE result about the world,
	// distinct from Unattempted and Unobtainable.
	ObservedAbsent Status = "OBSERVED_ABSENT"
)

// Condition is something that must be true if the claim holds.
type Condition struct {
	ID          string
	Description string
	// Material marks conditions on which the claim actually turns.
	// An immaterial condition left unmet does not block qualification.
	Material bool
}

// Requirement is one piece of evidence that would test a condition.
type Requirement struct {
	ID          string
	ConditionID string
	Description string
	// ExpectedIfTrue describes what this evidence should show if the
	// claim holds -- stated in advance, so a later observation cannot
	// be reinterpreted to fit.
	ExpectedIfTrue string
	// ContradictsIfShows describes what would count against the claim.
	// A requirement with no falsifying observation is not a test.
	ContradictsIfShows string
	Status             Status
	// AbsenceState is set when Status is ObservedAbsent, carrying the
	// observability gate's verdict so the claim cannot rest on an
	// ungated absence (Article 29).
	AbsenceState observability.State
	// DiagnosticValue in [0,1]: how much obtaining this would reduce
	// uncertainty.
	DiagnosticValue float64
}

// AlternativeHypothesis is a rival explanation for the same
// observations. A reverse proof with no alternatives has not been
// done -- it has only been asserted.
type AlternativeHypothesis struct {
	ID          string
	Description string
	// ExplainsRequirements lists requirement IDs this alternative
	// explains just as well as the primary claim. The larger this set,
	// the weaker the primary claim's discriminating power.
	ExplainsRequirements []string
	// Tested reports whether the alternative was actually evaluated.
	Tested bool
}

// Claim is the input to a reverse proof.
type Claim struct {
	ID          string
	Description string
	Conditions  []Condition
}

// RequirementSet is the output.
type RequirementSet struct {
	ClaimID      string                  `json:"claim_id"`
	Requirements []Requirement           `json:"requirements"`
	Alternatives []AlternativeHypothesis `json:"alternatives"`
	Tick         uint64                  `json:"tick"`
}

// Build assembles a requirement set for a claim, validating that the
// structure is a real test rather than a checklist.
func Build(c Claim, reqs []Requirement, alts []AlternativeHypothesis, tick uint64) (RequirementSet, error) {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Description) == "" {
		return RequirementSet{}, ErrEmptyClaim
	}
	if len(c.Conditions) == 0 {
		return RequirementSet{}, fmt.Errorf("%w: claim %q", ErrNoConditions, c.ID)
	}
	if len(reqs) == 0 {
		return RequirementSet{}, fmt.Errorf("%w: claim %q", ErrNoRequirements, c.ID)
	}

	seen := map[string]bool{}
	condIDs := map[string]bool{}
	for _, cond := range c.Conditions {
		condIDs[cond.ID] = true
	}
	for _, r := range reqs {
		if seen[r.ID] {
			return RequirementSet{}, fmt.Errorf("%w: %q", ErrDuplicateRequirement, r.ID)
		}
		seen[r.ID] = true
		if r.ConditionID != "" && !condIDs[r.ConditionID] {
			return RequirementSet{}, fmt.Errorf("reverseproof: requirement %q references unknown condition %q", r.ID, r.ConditionID)
		}
	}

	out := RequirementSet{ClaimID: c.ID, Tick: tick}
	out.Requirements = append(out.Requirements, reqs...)
	out.Alternatives = append(out.Alternatives, alts...)
	sort.Slice(out.Requirements, func(i, j int) bool { return out.Requirements[i].ID < out.Requirements[j].ID })
	sort.Slice(out.Alternatives, func(i, j int) bool { return out.Alternatives[i].ID < out.Alternatives[j].ID })
	return out, nil
}

// Gap summarizes what a requirement set is still missing.
type Gap struct {
	ClaimID string `json:"claim_id"`
	// Obtained, ObservedAbsent, Unobtainable and Unattempted partition
	// the requirements. All four are reported: an honest gap analysis
	// distinguishes "we looked and it wasn't there" from "we never
	// looked", which a single "missing" bucket would conflate.
	Obtained       []string `json:"obtained"`
	ObservedAbsent []string `json:"observed_absent"`
	Unobtainable   []string `json:"unobtainable"`
	Unattempted    []string `json:"unattempted"`
	// UntestedAlternatives are rival explanations nobody evaluated.
	UntestedAlternatives []string `json:"untested_alternatives"`
	// RemainingDiagnosticValue is the total diagnostic value still
	// unobtained -- how much uncertainty could still be removed.
	RemainingDiagnosticValue float64 `json:"remaining_diagnostic_value"`
	// Complete reports whether every MATERIAL requirement is resolved
	// and every alternative tested.
	Complete bool   `json:"complete"`
	Reason   string `json:"reason"`
}

// Analyze reports the gap. materialConditions names the conditions the
// claim actually turns on; requirements attached to them must be
// resolved (obtained, observed-absent, or demonstrably unobtainable)
// before the set is complete.
func Analyze(rs RequirementSet, materialConditions map[string]bool) Gap {
	g := Gap{ClaimID: rs.ClaimID}
	unresolvedMaterial := 0

	for _, r := range rs.Requirements {
		switch r.Status {
		case Obtained:
			g.Obtained = append(g.Obtained, r.ID)
		case ObservedAbsent:
			g.ObservedAbsent = append(g.ObservedAbsent, r.ID)
		case Unobtainable:
			g.Unobtainable = append(g.Unobtainable, r.ID)
		default:
			g.Unattempted = append(g.Unattempted, r.ID)
			g.RemainingDiagnosticValue += r.DiagnosticValue
			if materialConditions[r.ConditionID] {
				unresolvedMaterial++
			}
		}
	}
	for _, a := range rs.Alternatives {
		if !a.Tested {
			g.UntestedAlternatives = append(g.UntestedAlternatives, a.ID)
		}
	}
	sort.Strings(g.Obtained)
	sort.Strings(g.ObservedAbsent)
	sort.Strings(g.Unobtainable)
	sort.Strings(g.Unattempted)
	sort.Strings(g.UntestedAlternatives)

	switch {
	case unresolvedMaterial > 0:
		g.Reason = fmt.Sprintf("%d material requirement(s) unattempted", unresolvedMaterial)
	case len(g.UntestedAlternatives) > 0:
		g.Reason = fmt.Sprintf("%d alternative hypothesis/hypotheses untested", len(g.UntestedAlternatives))
	default:
		g.Complete = true
		g.Reason = "every material requirement resolved and every alternative tested"
	}
	return g
}

// ValidateFalsifiability checks that each requirement states what
// would contradict the claim. A requirement that can only confirm is
// not a test of anything, and a reverse proof built entirely from such
// requirements is confirmation bias with a schema.
func ValidateFalsifiability(rs RequirementSet) error {
	var weak []string
	for _, r := range rs.Requirements {
		if strings.TrimSpace(r.ContradictsIfShows) == "" {
			weak = append(weak, r.ID)
		}
	}
	if len(weak) > 0 {
		sort.Strings(weak)
		return fmt.Errorf("reverseproof: requirement(s) %s state no falsifying observation; they cannot test the claim",
			strings.Join(weak, ", "))
	}
	return nil
}
