package fref

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The constitutional sequencing law.
//
// A reviewer reading the runtime artefact — not the report — found this:
//
//	AUDIT-009 case.resolved
//	AUDIT-010 proof.sealed
//
// and, in the integration test, reverse closure running *after*
// Case.Resolve. Both are the same defect wearing different clothes.
//
// If the reverse direction runs after resolution, it is a post-hoc audit
// rather than a constitutional gate. That inverts what the reverse
// direction is for. Reverse proof answers "what evidence would actually
// be needed to justify this finding?", and a finding that is already
// final when the question is asked has not been gated by the answer —
// it has been rubber-stamped by it.
//
// So the law is coupled, not two pipelines that happen to share a
// repository:
//
//	CASE -> SCOPE -> EVIDENCE -> HYPOTHESIS -> REVERSE PROOF
//	 -> QUALIFICATION -> PROOF SEAL -> FINDING -> AUTHORIZED DECISION
//	 -> CASE RESOLUTION -> LEDGER -> REPLAY
//
// and never:
//
//	FORWARD -> DECISION -> RESOLUTION -> REVERSE
//
// This file makes that ordering executable in two forms: a Sequence that
// refuses an out-of-order step as it happens, and VerifyEventOrder that
// checks a recorded event stream after the fact. The first stops the
// defect; the second catches it in artefacts produced before the first
// existed.

// Step is one position in the coupled law.
//
// These are deliberately not the same vocabulary as the per-direction
// Stage constants. A Stage is a position within one direction; a Step is
// a position in the single coupled order that both directions and the
// case lifecycle share. Conflating them is what let the two run
// independently in the first place.
type Step string

const (
	StepCase               Step = "CASE"
	StepScope              Step = "SCOPE"
	StepEvidence           Step = "EVIDENCE"
	StepHypothesis         Step = "HYPOTHESIS"
	StepReverseProof       Step = "REVERSE_PROOF"
	StepQualification      Step = "QUALIFICATION"
	StepProofSeal          Step = "PROOF_SEAL"
	StepFinding            Step = "FINDING"
	StepAuthorizedDecision Step = "AUTHORIZED_DECISION"
	StepCaseResolution     Step = "CASE_RESOLUTION"
	StepLedger             Step = "LEDGER"
	StepReplay             Step = "REPLAY"
)

var canonicalSequence = []Step{
	StepCase, StepScope, StepEvidence, StepHypothesis, StepReverseProof,
	StepQualification, StepProofSeal, StepFinding, StepAuthorizedDecision,
	StepCaseResolution, StepLedger, StepReplay,
}

// CanonicalSequence returns the coupled law in order.
func CanonicalSequence() []Step { return append([]Step(nil), canonicalSequence...) }

// PositionOf returns a step's index in the law, and false for a step
// that is not in it.
func PositionOf(s Step) (int, bool) {
	for i, step := range canonicalSequence {
		if step == s {
			return i, true
		}
	}
	return 0, false
}

// MustPrecede reports whether a must come before b under the law.
func MustPrecede(a, b Step) bool {
	ia, oka := PositionOf(a)
	ib, okb := PositionOf(b)
	return oka && okb && ia < ib
}

var (
	ErrUnknownStep       = errors.New("fref: the step is not part of the constitutional sequence")
	ErrStepOutOfOrder    = errors.New("fref: the step would violate the constitutional sequence")
	ErrStepRepeated      = errors.New("fref: a step cannot complete twice in one sequence")
	ErrSequenceGap       = errors.New("fref: a step completed before a required predecessor")
	ErrNoSequenceSubject = errors.New("fref: a sequence requires a subject")
)

// Sequence records one case's progress through the coupled law and
// refuses a step taken out of order.
//
// It is deliberately strict about the two orderings the reviewer found
// broken, and those are worth naming because they are the reason this
// type exists:
//
//   - REVERSE_PROOF precedes QUALIFICATION, which precedes PROOF_SEAL.
//     The obligations are fixed before the verdict, and the verdict
//     before the seal.
//   - CASE_RESOLUTION comes after AUTHORIZED_DECISION, which comes after
//     FINDING, which comes after PROOF_SEAL. A case cannot resolve on a
//     conclusion that has not been sealed, founded and adopted.
type Sequence struct {
	subject string
	taken   []Step
	seen    map[Step]bool
}

// NewSequence starts a sequence for one subject — a case, a claim or a
// proposition.
func NewSequence(subject string) (*Sequence, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, ErrNoSequenceSubject
	}
	return &Sequence{subject: subject, seen: map[Step]bool{}}, nil
}

func (s *Sequence) Subject() string { return s.subject }

// Taken returns the steps completed, in the order they completed.
func (s *Sequence) Taken() []Step { return append([]Step(nil), s.taken...) }

// Reached reports whether a step has completed.
func (s *Sequence) Reached(step Step) bool { return s.seen[step] }

// Complete records a step, refusing anything the law forbids.
//
// Unlike Execution.Complete, this does NOT require every earlier step:
// a case may legitimately skip LEDGER in a dry run, or reach REPLAY
// without a separate LEDGER step in a deployment that treats them as
// one. What it refuses is a step taken *before* one the law says must
// precede it — which is the actual defect, and the weaker rule catches
// it without inventing false requirements.
func (s *Sequence) Complete(step Step) error {
	pos, ok := PositionOf(step)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownStep, step)
	}
	if s.seen[step] {
		return fmt.Errorf("%w: %s", ErrStepRepeated, step)
	}
	// Nothing already taken may sit later in the law than this step.
	for _, taken := range s.taken {
		if tp, _ := PositionOf(taken); tp > pos {
			return fmt.Errorf("%w: %s cannot follow %s", ErrStepOutOfOrder, step, taken)
		}
	}
	s.seen[step] = true
	s.taken = append(s.taken, step)
	return nil
}

// RequireGatesFor refuses a step whose constitutional gates have not
// been passed.
//
// The gates are the law's teeth. MustPrecede states the order; this
// states which predecessors are *mandatory* rather than merely earlier,
// and it is the check that would have caught the reviewer's finding:
// CASE_RESOLUTION requires REVERSE_PROOF, PROOF_SEAL, FINDING and
// AUTHORIZED_DECISION, so a resolution reached before the reverse
// direction closed is refused rather than recorded.
func (s *Sequence) RequireGatesFor(step Step) error {
	gates, ok := requiredGates[step]
	if !ok {
		return nil
	}
	var missing []string
	for _, g := range gates {
		if !s.seen[g] {
			missing = append(missing, string(g))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: %s requires %s", ErrSequenceGap, step, strings.Join(missing, ", "))
	}
	return nil
}

// requiredGates names the mandatory predecessors of each step.
//
// Read the CASE_RESOLUTION row first: it is the whole point. A case may
// not resolve until the reverse direction has closed, the proof object
// is sealed, a finding is founded on it, and an authority has adopted
// that finding.
var requiredGates = map[Step][]Step{
	StepScope:              {StepCase},
	StepEvidence:           {StepScope},
	StepHypothesis:         {StepEvidence},
	StepReverseProof:       {StepHypothesis},
	StepQualification:      {StepReverseProof},
	StepProofSeal:          {StepQualification},
	StepFinding:            {StepProofSeal},
	StepAuthorizedDecision: {StepFinding},
	StepCaseResolution:     {StepReverseProof, StepProofSeal, StepFinding, StepAuthorizedDecision},
	StepLedger:             {StepCaseResolution},
	StepReplay:             {StepLedger},
}

// RequiredGates returns the mandatory predecessors of a step.
func RequiredGates(step Step) []Step {
	return append([]Step(nil), requiredGates[step]...)
}

// CompleteGated is Complete plus the gate check: the form a caller
// should use when it wants the law enforced rather than merely recorded.
func (s *Sequence) CompleteGated(step Step) error {
	if err := s.RequireGatesFor(step); err != nil {
		return err
	}
	return s.Complete(step)
}

// --- Verifying a recorded stream --------------------------------------

// EventStepFor maps a canonical audit event action onto the step it
// represents.
//
// Events that are not sequence-bearing — an actor being recorded, a
// domain state syncing — map to nothing and are skipped by the verifier
// rather than treated as violations.
var eventStep = map[string]Step{
	"case.opened":                  StepCase,
	"case.scoped":                  StepScope,
	"case.evidence_pinned":         StepEvidence,
	"case.hypothesis_recorded":     StepHypothesis,
	"case.hypothesis_tested":       StepHypothesis,
	"qualification.reverse_closed": StepReverseProof,
	"case.qualification_begun":     StepQualification,
	"proof.sealed":                 StepProofSeal,
	"case.proof_attached":          StepProofSeal,
	"claim.finding_founded":        StepFinding,
	"case.decision_authorized":     StepAuthorizedDecision,
	"case.resolved":                StepCaseResolution,
}

// EventStepFor returns the step an event action represents.
func EventStepFor(action string) (Step, bool) {
	s, ok := eventStep[action]
	return s, ok
}

// OrderViolation is one out-of-order pair found in a recorded stream.
type OrderViolation struct {
	// EarlierIndex and LaterIndex are positions in the stream.
	EarlierIndex int
	LaterIndex   int
	// Recorded is the step that appeared first in the stream but should
	// have come second under the law.
	Recorded Step
	// Expected is the step that should have preceded it.
	Expected Step
	Detail   string
}

func (v OrderViolation) String() string {
	return fmt.Sprintf("%s (position %d) was recorded before %s (position %d): %s",
		v.Recorded, v.EarlierIndex, v.Expected, v.LaterIndex, v.Detail)
}

// VerifyEventOrder checks a recorded stream of canonical event actions
// against the law.
//
// This is the after-the-fact half. It exists because the artefact that
// exposed this defect was already written: a Sequence would have refused
// the bad order as it happened, but only a verifier can tell you that a
// ledger you already have is out of order.
func VerifyEventOrder(actions []string) []OrderViolation {
	type seen struct {
		step  Step
		index int
	}
	var stream []seen
	for i, a := range actions {
		if st, ok := eventStep[a]; ok {
			stream = append(stream, seen{step: st, index: i})
		}
	}

	var violations []OrderViolation
	for i := 0; i < len(stream); i++ {
		for j := i + 1; j < len(stream); j++ {
			// stream[i] happened first. If the law says stream[j]'s step
			// must precede stream[i]'s, the record is out of order.
			if MustPrecede(stream[j].step, stream[i].step) {
				violations = append(violations, OrderViolation{
					EarlierIndex: stream[i].index, LaterIndex: stream[j].index,
					Recorded: stream[i].step, Expected: stream[j].step,
					Detail: fmt.Sprintf("the constitutional sequence requires %s before %s",
						stream[j].step, stream[i].step),
				})
			}
		}
	}
	return violations
}

// VerifyEventGates checks that every gating step a stream *reached* had
// its mandatory predecessors present in that stream.
//
// This closes a hole VerifyEventOrder leaves open. Order alone cannot
// catch an omission: a ledger that reaches CASE_RESOLUTION having never
// recorded a reverse closure is perfectly ordered and completely
// ungated, and the first version of this verifier passed it. Absence is
// not order, and it has to be checked separately.
func VerifyEventGates(actions []string) []string {
	present := map[Step]bool{}
	var order []Step
	for _, a := range actions {
		if st, ok := eventStep[a]; ok {
			if !present[st] {
				order = append(order, st)
			}
			present[st] = true
		}
	}

	var missing []string
	for _, st := range order {
		for _, gate := range requiredGates[st] {
			if !present[gate] {
				missing = append(missing, fmt.Sprintf(
					"%s was recorded but its gate %s never was", st, gate))
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// ExplainSequence renders the law, so a reader can check an
// implementation against it without reading this file.
func ExplainSequence() string {
	var b strings.Builder
	b.WriteString("VERIQO constitutional sequence\n\n")
	for i, s := range canonicalSequence {
		gates := requiredGates[s]
		if len(gates) == 0 {
			b.WriteString(fmt.Sprintf("  %2d. %-20s (no gates: this is where a case begins)\n", i+1, s))
			continue
		}
		names := make([]string, 0, len(gates))
		for _, g := range gates {
			names = append(names, string(g))
		}
		b.WriteString(fmt.Sprintf("  %2d. %-20s gated on: %s\n", i+1, s, strings.Join(names, ", ")))
	}
	b.WriteString("\nThe rule this exists to enforce: REVERSE_PROOF, PROOF_SEAL, FINDING and\n")
	b.WriteString("AUTHORIZED_DECISION all gate CASE_RESOLUTION. Reverse proof is a\n")
	b.WriteString("constitutional gate, not a retrospective audit.\n")
	return b.String()
}
