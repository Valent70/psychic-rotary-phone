// Package gates is the production readiness register.
//
// # Why the eight blockers became twenty permanent gates
//
// The specification is explicit: the eight existing blockers are not
// deleted when they are closed. They become PERMANENT GATES, and
// twelve more join them. The difference matters -- a blocker is
// something you clear once and forget; a gate is something that must
// hold at every release, and one that silently stops holding is the
// commonest way a system regresses out of production readiness.
//
// # BLOCKED_EXTERNAL is the honest state, not a failure
//
// Most of these gates cannot be satisfied by writing code. An
// independent penetration test requires a pentest firm. A 72-hour soak
// requires 72 hours. An external corpus requires documents VERIQO did
// not create. Reporting those as "failing" would be wrong -- nothing
// is broken -- and reporting them as "not applicable" would be worse.
// They are BLOCKED_EXTERNAL: the work is understood, VERIQO cannot do
// it alone, and the register says who could.
//
// # The rule that keeps the register honest
//
// A gate may only be marked SATISFIED with EVIDENCE, and evidence that
// VERIQO produced cannot satisfy a gate whose whole point is that
// somebody else looks. RequiresExternalParty() is checked, not
// documented.
package gates

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrUnknownGate   = errors.New("gates: unknown gate")
	ErrNoEvidence    = errors.New("gates: a gate may not be satisfied without evidence")
	ErrSelfSatisfied = errors.New("gates: this gate requires an outside party and the " +
		"evidence is VERIQO's own")
	ErrNoAttestor   = errors.New("gates: external evidence must name who produced it")
	ErrUnknownState = errors.New("gates: unknown state")
)

// State is a gate's standing.
type State string

const (
	// NotStarted is the zero value.
	NotStarted State = ""
	// InProgress: work is under way inside VERIQO.
	InProgress State = "IN_PROGRESS"
	// BlockedExternal: VERIQO cannot proceed alone. This is a
	// legitimate steady state and it is not a failure.
	BlockedExternal State = "BLOCKED_EXTERNAL"
	// Satisfied: evidence exists and, where required, an outside party
	// produced it.
	Satisfied State = "SATISFIED"
	// Regressed: the gate was satisfied and no longer is. It is a
	// separate state from NOT_STARTED because they need different
	// responses -- and because a gate that quietly returned to
	// NOT_STARTED would look like work that had never begun.
	Regressed State = "REGRESSED"
)

func States() []State {
	return []State{NotStarted, InProgress, BlockedExternal, Satisfied, Regressed}
}

func (s State) Valid() bool {
	for _, x := range States() {
		if x == s {
			return true
		}
	}
	return false
}

func (s State) String() string {
	if s == NotStarted {
		return "NOT_STARTED"
	}
	return string(s)
}

// Blocking reports whether this state prevents a production release.
// Everything except SATISFIED does, including BLOCKED_EXTERNAL --
// which is the point: an honest blocker still blocks.
func (s State) Blocking() bool { return s != Satisfied }

// Category groups the gates.
type Category string

const (
	Infrastructure Category = "INFRASTRUCTURE"
	Security       Category = "SECURITY"
	DataAndRights  Category = "DATA_AND_RIGHTS"
	Qualification  Category = "QUALIFICATION"
	Resilience     Category = "RESILIENCE"
	AIGovernance   Category = "AI_GOVERNANCE"
)

// Gate is one permanent production requirement.
type Gate struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category Category `json:"category"`
	// What states plainly what must be true.
	What string `json:"what"`
	// Why states what goes wrong if it is not.
	Why string `json:"why"`
	// RequiresExternalParty marks a gate VERIQO cannot satisfy alone.
	RequiresExternalParty bool `json:"requires_external_party"`
	// WhoCouldSatisfy names the kind of party that could. It is
	// required for external gates: "somebody else" is not a plan.
	WhoCouldSatisfy string `json:"who_could_satisfy,omitempty"`
}

func (g Gate) Validate() error {
	if strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Name) == "" {
		return errors.New("gates: a gate needs an id and a name")
	}
	if strings.TrimSpace(g.What) == "" || strings.TrimSpace(g.Why) == "" {
		return fmt.Errorf("gates: %s does not say what it requires or why", g.ID)
	}
	if g.RequiresExternalParty && strings.TrimSpace(g.WhoCouldSatisfy) == "" {
		return fmt.Errorf("gates: %s requires an outside party and does not say which kind; "+
			"'somebody else' is not a plan", g.ID)
	}
	return nil
}

// Evidence is what a gate's status rests on.
type Evidence struct {
	Description string    `json:"description"`
	Ref         string    `json:"ref"`
	At          time.Time `json:"at"`
	// ProducedBy names who produced it.
	ProducedBy string `json:"produced_by"`
	// External marks evidence from outside VERIQO.
	External bool `json:"external"`
}

func (e Evidence) Validate() error {
	if strings.TrimSpace(e.Description) == "" || strings.TrimSpace(e.Ref) == "" {
		return errors.New("gates: evidence needs a description and a reference")
	}
	if strings.TrimSpace(e.ProducedBy) == "" {
		return ErrNoAttestor
	}
	if e.At.IsZero() {
		return errors.New("gates: evidence has no date")
	}
	if e.External && looksLikeVeriqo(e.ProducedBy) {
		return fmt.Errorf("%w: %q is marked external", ErrSelfSatisfied, e.ProducedBy)
	}
	return nil
}

func looksLikeVeriqo(s string) bool {
	return strings.Contains(strings.ToLower(s), "veriqo")
}

// Status is a gate's current standing.
type Status struct {
	GateID   string     `json:"gate_id"`
	State    State      `json:"state"`
	Evidence []Evidence `json:"evidence,omitempty"`
	// Note explains the state, especially a block.
	Note string    `json:"note"`
	At   time.Time `json:"at"`
}

// Register holds the gates and their statuses.
type Register struct {
	gates  map[string]Gate
	order  []string
	status map[string]Status
}

// NewRegister builds the register.
func NewRegister(gs []Gate) (*Register, error) {
	r := &Register{gates: map[string]Gate{}, status: map[string]Status{}}
	for _, g := range gs {
		if err := g.Validate(); err != nil {
			return nil, err
		}
		if _, dup := r.gates[g.ID]; dup {
			return nil, fmt.Errorf("gates: duplicate gate %s", g.ID)
		}
		r.gates[g.ID] = g
		r.order = append(r.order, g.ID)
		r.status[g.ID] = Status{GateID: g.ID, State: NotStarted}
	}
	return r, nil
}

// Set records a gate's state.
//
// The check that keeps the register honest: a gate requiring an
// outside party cannot be SATISFIED by VERIQO's own evidence.
func (r *Register) Set(id string, state State, note string, ev ...Evidence) error {
	g, ok := r.gates[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, id)
	}
	if !state.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownState, state)
	}
	for _, e := range ev {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	if state == Satisfied {
		if len(ev) == 0 {
			return fmt.Errorf("%w: %s", ErrNoEvidence, id)
		}
		if g.RequiresExternalParty {
			external := false
			for _, e := range ev {
				if e.External {
					external = true
				}
			}
			if !external {
				return fmt.Errorf("%w: %s requires %s and no external evidence was supplied",
					ErrSelfSatisfied, id, g.WhoCouldSatisfy)
			}
		}
	}
	if state == BlockedExternal && strings.TrimSpace(note) == "" {
		return fmt.Errorf("gates: %s is BLOCKED_EXTERNAL and does not say what is blocking it", id)
	}
	prev := r.status[id]
	if prev.State == Satisfied && state != Satisfied && state != Regressed {
		// A gate that was satisfied and is no longer is REGRESSED, not
		// merely back where it started.
		state = Regressed
		if note == "" {
			note = "this gate was previously satisfied"
		}
	}
	r.status[id] = Status{GateID: id, State: state, Evidence: ev, Note: note, At: time.Now().UTC()}
	return nil
}

// SetAt is Set with an explicit instant, for deterministic use.
func (r *Register) SetAt(id string, state State, note string, at time.Time, ev ...Evidence) error {
	if err := r.Set(id, state, note, ev...); err != nil {
		return err
	}
	s := r.status[id]
	s.At = at
	r.status[id] = s
	return nil
}

// Status returns a gate's standing.
func (r *Register) Status(id string) (Status, error) {
	s, ok := r.status[id]
	if !ok {
		return Status{}, fmt.Errorf("%w: %s", ErrUnknownGate, id)
	}
	return s, nil
}

// Gate returns a gate's definition.
func (r *Register) Gate(id string) (Gate, error) {
	g, ok := r.gates[id]
	if !ok {
		return Gate{}, fmt.Errorf("%w: %s", ErrUnknownGate, id)
	}
	return g, nil
}

// Gates returns every gate in registration order.
func (r *Register) Gates() []Gate {
	out := make([]Gate, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.gates[id])
	}
	return out
}

// Blocking returns every gate that prevents a production release.
func (r *Register) Blocking() []Status {
	var out []Status
	for _, id := range r.order {
		if s := r.status[id]; s.State.Blocking() {
			out = append(out, s)
		}
	}
	return out
}

// ProductionReady reports whether every gate is satisfied, and why not
// when it is not.
func (r *Register) ProductionReady() (bool, []string) {
	blocking := r.Blocking()
	if len(blocking) == 0 {
		return true, nil
	}
	reasons := make([]string, 0, len(blocking))
	for _, s := range blocking {
		g := r.gates[s.GateID]
		reasons = append(reasons, fmt.Sprintf("%s (%s): %s", g.ID, s.State, g.Name))
	}
	return false, reasons
}

// ExternallyBlocked returns the gates VERIQO cannot satisfy alone,
// with who could.
func (r *Register) ExternallyBlocked() []string {
	var out []string
	for _, id := range r.order {
		g := r.gates[id]
		if !g.RequiresExternalParty {
			continue
		}
		if s := r.status[id]; s.State != Satisfied {
			out = append(out, fmt.Sprintf("%s: %s -- needs %s", g.ID, g.Name, g.WhoCouldSatisfy))
		}
	}
	return out
}

// Regressions returns gates that were satisfied and are not now.
func (r *Register) Regressions() []Status {
	var out []Status
	for _, id := range r.order {
		if s := r.status[id]; s.State == Regressed {
			out = append(out, s)
		}
	}
	return out
}

// Report renders the register.
func (r *Register) Report() string {
	var b strings.Builder
	b.WriteString("PRODUCTION READINESS GATES\n")
	byCat := map[Category][]string{}
	for _, id := range r.order {
		g := r.gates[id]
		byCat[g.Category] = append(byCat[g.Category], id)
	}
	cats := make([]Category, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i] < cats[j] })

	for _, c := range cats {
		fmt.Fprintf(&b, "  %s\n", c)
		for _, id := range byCat[c] {
			g, s := r.gates[id], r.status[id]
			mark := " "
			if g.RequiresExternalParty {
				mark = "*"
			}
			fmt.Fprintf(&b, "   %s %-6s %-17s %s\n", mark, g.ID, s.State, g.Name)
			if s.Note != "" {
				fmt.Fprintf(&b, "        %s\n", s.Note)
			}
		}
	}
	ready, reasons := r.ProductionReady()
	if ready {
		b.WriteString("\n  Every gate is satisfied.\n")
	} else {
		fmt.Fprintf(&b, "\n  NOT PRODUCTION READY: %d gate(s) blocking.\n", len(reasons))
	}
	if ext := r.ExternallyBlocked(); len(ext) > 0 {
		fmt.Fprintf(&b, "  %d gate(s) cannot be satisfied by VERIQO alone:\n", len(ext))
		for _, e := range ext {
			fmt.Fprintf(&b, "    %s\n", e)
		}
	}
	if reg := r.Regressions(); len(reg) > 0 {
		fmt.Fprintf(&b, "  %d gate(s) REGRESSED from a previously satisfied state:\n", len(reg))
		for _, s := range reg {
			fmt.Fprintf(&b, "    %s: %s\n", s.GateID, s.Note)
		}
	}
	b.WriteString("  (* requires a party outside VERIQO)\n")
	return b.String()
}
