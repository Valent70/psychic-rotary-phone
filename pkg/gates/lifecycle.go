package gates

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

// A gate is a constrained state transition, not a checkbox.
//
// # The failure this closes
//
// The State type in this package answers "where does this gate stand".
// It does not, on its own, prevent the move that makes every assurance
// programme worthless:
//
//	status: OPEN     ->     status: CLOSED
//
// performed by an engineer editing a file, with no test having been
// run, no findings raised, nothing remediated and nobody retested.
// Every property the gate was supposed to guarantee is gone and the
// artefact looks identical to one that earned it.
//
// So a gate additionally carries a LIFECYCLE, and the lifecycle is a
// machine that admits exactly one predecessor per step. OPEN cannot
// reach VALIDATED. TESTING cannot reach VALIDATED. FINDINGS cannot
// reach VALIDATED. The only path to VALIDATED runs through RETESTED,
// which runs through REMEDIATED, which runs through FINDINGS, which
// runs through TESTING -- and the transitions into and out of TESTING
// require evidence from a party that is not the implementer.
//
// A gate that produced no findings is not a shortcut either: it moves
// FINDINGS -> REMEDIATED with an explicit "nothing to remediate", so
// the record distinguishes "nothing was found" from "nobody looked",
// which are the two situations an unqualified green row conflates.
type Phase string

const (
	// Open: the gate is defined and nothing has begun.
	Open Phase = "OPEN"
	// Ready: the artefacts an outside party needs exist -- a
	// reproducible build, a manifest, test vectors, a verifier. This
	// is the last phase VERIQO can reach alone.
	Ready Phase = "READY"
	// Testing: an outside party is testing. Entering this phase
	// requires that party to be named and attested.
	Testing Phase = "TESTING"
	// Findings: testing produced a result. The result may be "no
	// findings", stated explicitly.
	Findings Phase = "FINDINGS"
	// Remediated: VERIQO has addressed what was found. This phase is
	// self-reachable -- fixing is VERIQO's work.
	Remediated Phase = "REMEDIATED"
	// Retested: the outside party has confirmed the remediation.
	Retested Phase = "RETESTED"
	// Validated: that party states the gate's property holds, in a
	// signed report with a named scope.
	Validated Phase = "VALIDATED"
	// Lapsed: a previously validated gate whose evidence expired or
	// whose subject changed. Reachable from any phase and never
	// skippable on the way back up.
	Lapsed Phase = "LAPSED"
)

var (
	ErrPhaseJump     = errors.New("gates: a gate may not skip a lifecycle phase")
	ErrPhaseEvidence = errors.New("gates: this phase transition requires evidence it did not get")
	ErrPhaseExternal = errors.New("gates: this phase transition requires a party that is not VERIQO")
	ErrUnknownPhase  = errors.New("gates: unknown lifecycle phase")
	ErrFindingsUnmet = errors.New("gates: findings remain unaddressed")
	ErrNoRemediation = errors.New("gates: a remediation names nothing it remediated")
)

// Phases returns the lifecycle in order. Lapsed is excluded: it is
// reachable from anywhere and is not a rung.
func Phases() []Phase {
	return []Phase{Open, Ready, Testing, Findings, Remediated, Retested, Validated}
}

func (p Phase) Valid() bool {
	if p == Lapsed {
		return true
	}
	for _, q := range Phases() {
		if p == q {
			return true
		}
	}
	return false
}

// index returns the phase's position on the ladder, or -1 for Lapsed.
func (p Phase) index() int {
	for i, q := range Phases() {
		if p == q {
			return i
		}
	}
	return -1
}

// SelfReachable reports whether VERIQO can enter this phase alone.
//
// READY and REMEDIATED are VERIQO's own work: preparing the capsule,
// and fixing what was found. Everything else needs the other party.
func (p Phase) SelfReachable() bool {
	switch p {
	case Open, Ready, Remediated, Lapsed:
		return true
	}
	return false
}

// Finding is one thing an outside party found.
type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	// Addressed cites what closed it. Empty means open.
	Addressed string `json:"addressed,omitempty"`
	// Accepted marks a finding VERIQO has decided not to fix, with a
	// stated rationale. An accepted risk is a decision, not a fix, and
	// the two must not look alike.
	Accepted  bool   `json:"accepted,omitempty"`
	Rationale string `json:"rationale,omitempty"`
}

func (f Finding) Open() bool { return strings.TrimSpace(f.Addressed) == "" && !f.Accepted }

func (f Finding) Validate() error {
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Summary) == "" {
		return errors.New("gates: a finding has no id or no summary")
	}
	if f.Accepted && strings.TrimSpace(f.Rationale) == "" {
		return fmt.Errorf("gates: finding %s is accepted with no rationale; an accepted risk "+
			"is a decision and must be readable as one", f.ID)
	}
	return nil
}

// PhaseChange is one recorded lifecycle transition.
type PhaseChange struct {
	From     Phase            `json:"from"`
	To       Phase            `json:"to"`
	By       contract.ID      `json:"by"`
	At       time.Time        `json:"at"`
	Reason   string           `json:"reason"`
	Evidence []state.Evidence `json:"evidence,omitempty"`
	Findings []Finding        `json:"findings,omitempty"`
}

// Lifecycle tracks one gate's progress through the phases.
type Lifecycle struct {
	GateID string `json:"gate_id"`
	// Implementer is the party whose work the gate assesses. External
	// evidence must come from somebody else.
	Implementer contract.ID `json:"implementer"`

	phase    Phase
	history  []PhaseChange
	findings map[string]Finding
}

// NewLifecycle starts a gate at OPEN.
func NewLifecycle(gateID string, implementer contract.ID) (*Lifecycle, error) {
	if strings.TrimSpace(gateID) == "" {
		return nil, errors.New("gates: a lifecycle must name its gate")
	}
	if strings.TrimSpace(string(implementer)) == "" {
		return nil, errors.New("gates: a lifecycle must name the implementer whose work it " +
			"assesses; without it, independence cannot be evaluated")
	}
	return &Lifecycle{GateID: gateID, Implementer: implementer, phase: Open,
		findings: map[string]Finding{}}, nil
}

// Phase returns the current phase.
func (l *Lifecycle) Phase() Phase { return l.phase }

// History returns a copy of every transition.
func (l *Lifecycle) History() []PhaseChange { return append([]PhaseChange(nil), l.history...) }

// Findings returns every finding, ordered.
func (l *Lifecycle) Findings() []Finding {
	out := make([]Finding, 0, len(l.findings))
	for _, f := range l.findings {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// OpenFindings returns the findings nobody has addressed or accepted.
func (l *Lifecycle) OpenFindings() []Finding {
	var out []Finding
	for _, f := range l.Findings() {
		if f.Open() {
			out = append(out, f)
		}
	}
	return out
}

// Advance moves the gate one phase forward.
//
// The rules:
//
//  1. exactly one phase, in order. There is no path from OPEN to
//     VALIDATED however the caller spells it;
//  2. a phase that is not self-reachable requires evidence whose
//     validator is external, attested, and not the implementer;
//  3. FINDINGS -> REMEDIATED requires that every finding is either
//     addressed or explicitly accepted with a rationale;
//  4. RETESTED -> VALIDATED requires external evidence AND no open
//     findings, so a validated gate cannot carry an unanswered one.
func (l *Lifecycle) Advance(to Phase, by contract.ID, at time.Time, reason string,
	ev []state.Evidence, found []Finding) error {

	if !to.Valid() || to == Lapsed {
		return fmt.Errorf("%w: %q (use Lapse to move a gate backwards)", ErrUnknownPhase, to)
	}
	cur, next := l.phase.index(), to.index()
	if cur < 0 {
		return fmt.Errorf("gates: %s is LAPSED; it must be reopened at OPEN", l.GateID)
	}
	if next != cur+1 {
		return fmt.Errorf("%w: %s is %s and the move targets %s. The only path to %s runs "+
			"through %s", ErrPhaseJump, l.GateID, l.phase, to, to, strings.Join(
			phaseNames(Phases()[cur+1:next+1]), " -> "))
	}
	if at.IsZero() {
		return errors.New("gates: a phase change carries no instant")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("gates: a phase change states no reason")
	}

	for _, f := range found {
		if err := f.Validate(); err != nil {
			return err
		}
	}

	if !to.SelfReachable() {
		ok := false
		for _, e := range ev {
			if err := e.Validate(); err != nil {
				return err
			}
			if e.Validator.IndependentOf(l.Implementer) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("%w: %s -> %s, and no cited evidence has a validator "+
				"independent of %s. This is the transition that cannot be performed by "+
				"editing a field", ErrPhaseExternal, l.phase, to, l.Implementer)
		}
	}

	switch to {
	case Findings:
		// Entering FINDINGS records what was found -- possibly
		// nothing, stated explicitly rather than by omission.
		for _, f := range found {
			l.findings[f.ID] = f
		}
	case Remediated:
		// Merge in any updates to findings, then require that nothing
		// is left open.
		for _, f := range found {
			l.findings[f.ID] = f
		}
		if len(l.findings) == 0 {
			return fmt.Errorf("%w: %s reached FINDINGS with no record at all. 'Nothing was "+
				"found' must be recorded as a finding-free result, not as an absent one",
				ErrNoRemediation, l.GateID)
		}
		if open := l.OpenFindings(); len(open) > 0 {
			return fmt.Errorf("%w: %d finding(s) are neither addressed nor accepted: %s",
				ErrFindingsUnmet, len(open), strings.Join(findingIDs(open), ", "))
		}
	case Validated:
		if open := l.OpenFindings(); len(open) > 0 {
			return fmt.Errorf("%w: %s cannot be VALIDATED with %d open finding(s)",
				ErrFindingsUnmet, l.GateID, len(open))
		}
	}

	l.history = append(l.history, PhaseChange{From: l.phase, To: to, By: by, At: at,
		Reason: reason, Evidence: ev, Findings: found})
	l.phase = to
	return nil
}

// Lapse moves a gate out of the lifecycle entirely.
//
// It is deliberately easy: withdrawing a claim is not a claim. A
// lapsed gate must be walked from OPEN again, because the evidence
// that lapsed cannot be partially revived.
func (l *Lifecycle) Lapse(by contract.ID, at time.Time, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("gates: a lapse states no reason")
	}
	if at.IsZero() {
		return errors.New("gates: a lapse carries no instant")
	}
	l.history = append(l.history, PhaseChange{From: l.phase, To: Lapsed, By: by, At: at,
		Reason: reason})
	l.phase = Lapsed
	return nil
}

// Reopen puts a lapsed gate back at OPEN.
func (l *Lifecycle) Reopen(by contract.ID, at time.Time, reason string) error {
	if l.phase != Lapsed {
		return fmt.Errorf("gates: %s is %s, not LAPSED", l.GateID, l.phase)
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("gates: a reopen states no reason")
	}
	l.history = append(l.history, PhaseChange{From: Lapsed, To: Open, By: by, At: at,
		Reason: reason})
	l.phase = Open
	l.findings = map[string]Finding{}
	return nil
}

// Closed reports whether the gate has completed its lifecycle.
func (l *Lifecycle) Closed() bool { return l.phase == Validated }

// ExternallyTouched reports whether any party other than the
// implementer has contributed evidence.
func (l *Lifecycle) ExternallyTouched() bool {
	for _, h := range l.history {
		for _, e := range h.Evidence {
			if e.Validator.IndependentOf(l.Implementer) {
				return true
			}
		}
	}
	return false
}

func phaseNames(ps []Phase) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func findingIDs(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID
	}
	return out
}

// Describe renders a lifecycle.
func (l *Lifecycle) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  phase %s\n", l.GateID, l.phase)
	if !l.ExternallyTouched() {
		b.WriteString("  no party other than the implementer has contributed any evidence\n")
	}
	for _, h := range l.history {
		fmt.Fprintf(&b, "  %s -> %s by %s: %s\n", h.From, h.To, h.By, h.Reason)
	}
	for _, f := range l.Findings() {
		status := "OPEN"
		switch {
		case f.Accepted:
			status = "ACCEPTED: " + f.Rationale
		case !f.Open():
			status = "addressed by " + f.Addressed
		}
		fmt.Fprintf(&b, "  finding %s [%s] %s -- %s\n", f.ID, f.Severity, f.Summary, status)
	}
	return b.String()
}
