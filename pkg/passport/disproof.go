package passport

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The Disproof Route.
//
// # Why a disproof PATH is not enough
//
// Every claim in this system already carries a disproof path: a
// sentence saying what would make it false. That is the right idea and
// it is not usable by the person holding the passport, because a
// sentence describes a destination and gives no route to it.
//
// A recipient in a dispute does not need to be told that "a certified
// density measurement showing the bases are not comparable would
// remove the arithmetic". They need to know: who issues such a
// measurement, what it must contain to count, where to send it, and
// what happens to this passport when they do.
//
// So the route is numbered steps, each one an action a named party can
// actually take, ending in a stated consequence. VERIQO does not only
// say "here is why I believe this". It says "here is how you prove me
// wrong, in order, and here is what it costs you".
//
// # Why this is a differentiator rather than a courtesy
//
// A system that publishes the route to its own refutation is making a
// claim about itself that a confident system cannot make: that it
// expects to be wrong sometimes and has already thought about how it
// would find out. An expert report that says "my conclusion is X" and
// an expert report that says "my conclusion is X, and here are the
// four things that would overturn it, in the order I would try them"
// are not the same document, and the second is the one that survives
// cross-examination.
type Step struct {
	// N is the step's position. Order matters: the route is meant to
	// be walked cheapest-first, so a recipient who runs out of budget
	// has still done the most informative thing available.
	N int `json:"n"`
	// Action is what to do, phrased as an instruction.
	Action string `json:"action"`
	// Party is who can do it. A step nobody can perform is not a
	// route, it is an excuse.
	Party string `json:"party"`
	// Produces is what the step yields if it succeeds.
	Produces string `json:"produces"`
	// Effect is what happens to the finding if it does. Stating this
	// is what stops the route being decorative: a step whose success
	// changes nothing should not be in the list.
	Effect string `json:"effect"`
	// Cost is a coarse indication, so the order can be argued with.
	// "unknown" is permitted and common.
	Cost string `json:"cost,omitempty"`
	// Blocked, when non-empty, says why this step cannot currently be
	// taken. A blocked step stays in the route rather than being
	// dropped: a recipient is entitled to know that the cheapest way
	// to refute a finding is closed to them, and why.
	Blocked string `json:"blocked,omitempty"`
}

func (s Step) Validate() error {
	if s.N < 1 {
		return fmt.Errorf("passport: a disproof step has no position")
	}
	for name, v := range map[string]string{
		"action": s.Action, "party": s.Party, "produces": s.Produces, "effect": s.Effect,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("passport: disproof step %d states no %s. A step nobody can "+
				"perform, or whose success changes nothing, is not a route", s.N, name)
		}
	}
	return nil
}

// Route is the ordered set of steps that would overturn a finding.
type Route struct {
	// Overturns names what the route attacks, so a reader can tell
	// whether it addresses the part they doubt.
	Overturns string `json:"overturns"`
	Steps     []Step `json:"steps"`
	// IfAllFail states what is established if every step is taken and
	// none succeeds. It is required, and it is the honest half: a
	// route that only describes refutation implies that surviving it
	// proves the claim, and it does not.
	IfAllFail string `json:"if_all_fail"`
}

var ErrNoRoute = errors.New("passport: a finding states no disproof route")

func (r Route) Validate() error {
	if strings.TrimSpace(r.Overturns) == "" {
		return fmt.Errorf("%w: the route does not say what it attacks", ErrNoRoute)
	}
	if len(r.Steps) == 0 {
		return fmt.Errorf("%w: no steps. A finding that cannot be described as refutable "+
			"is a finding nobody can argue with, which is the opposite of defensible",
			ErrNoRoute)
	}
	if strings.TrimSpace(r.IfAllFail) == "" {
		return fmt.Errorf("%w: the route does not say what surviving it establishes. A "+
			"route that describes only refutation implies that surviving it proves the "+
			"claim, and it does not", ErrNoRoute)
	}
	seen := map[int]bool{}
	for _, s := range r.Steps {
		if err := s.Validate(); err != nil {
			return err
		}
		if seen[s.N] {
			return fmt.Errorf("passport: two disproof steps numbered %d", s.N)
		}
		seen[s.N] = true
	}
	for i := 1; i <= len(r.Steps); i++ {
		if !seen[i] {
			return fmt.Errorf("passport: the disproof route skips step %d; a route with a "+
				"gap cannot be walked", i)
		}
	}
	return nil
}

// Open returns the steps a recipient can actually take now.
func (r Route) Open() []Step {
	var out []Step
	for _, s := range r.Steps {
		if strings.TrimSpace(s.Blocked) == "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// Blocked returns the steps that cannot currently be taken, with the
// reason.
//
// They stay in the route. A recipient is entitled to know that the
// cheapest way to refute a finding is closed to them, and dropping
// such a step would make the route look shorter and easier than it is.
func (r Route) Blocked() []Step {
	var out []Step
	for _, s := range r.Steps {
		if strings.TrimSpace(s.Blocked) != "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// Walkable reports whether any step is open at all.
//
// A route with every step blocked is a finding nobody can currently
// challenge, which is a serious property and must be visible rather
// than inferred from an empty list.
func (r Route) Walkable() bool { return len(r.Open()) > 0 }

// Render writes the route the way it must appear to a recipient.
func (r Route) Render() string {
	var b strings.Builder
	b.WriteString("DISPROOF ROUTE\n")
	fmt.Fprintf(&b, "  to overturn: %s\n\n", r.Overturns)
	steps := append([]Step(nil), r.Steps...)
	sort.Slice(steps, func(i, j int) bool { return steps[i].N < steps[j].N })
	for _, s := range steps {
		fmt.Fprintf(&b, "  %d. %s\n", s.N, s.Action)
		fmt.Fprintf(&b, "       who:      %s\n", s.Party)
		fmt.Fprintf(&b, "       produces: %s\n", s.Produces)
		fmt.Fprintf(&b, "       effect:   %s\n", s.Effect)
		if s.Cost != "" {
			fmt.Fprintf(&b, "       cost:     %s\n", s.Cost)
		}
		if s.Blocked != "" {
			fmt.Fprintf(&b, "       BLOCKED:  %s\n", s.Blocked)
		}
	}
	if !r.Walkable() {
		b.WriteString("\n  EVERY STEP IS BLOCKED. This finding cannot currently be\n")
		b.WriteString("  challenged by its recipient, which is a limitation of the\n")
		b.WriteString("  finding and not a strength of it.\n")
	}
	fmt.Fprintf(&b, "\n  If every step is taken and none succeeds: %s\n", r.IfAllFail)
	return b.String()
}
