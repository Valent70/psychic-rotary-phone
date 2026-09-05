package register

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

var (
	ErrDanglingRef = errors.New("assurance/register: a reference points at nothing")
	ErrNoSupport   = errors.New("assurance/register: a gate is claimed satisfied and nothing supports it")
	ErrDuplicate   = errors.New("assurance/register: duplicate id")
)

// Control is an implementation that claims can be made about.
//
// It exists as a node rather than a string so that a claim and a gate
// can be shown to be talking about the same thing -- which is exactly
// what two separately maintained registers cannot demonstrate.
type Control struct {
	ID string `json:"id"`
	// Name is what it does.
	Name string `json:"name"`
	// Packages are where it lives, so a reader can go and look.
	Packages []string `json:"packages"`
	// Implementer built it.
	Implementer contract.ID `json:"implementer"`
}

func (c Control) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Name) == "" {
		return errors.New("assurance/register: a control has no id or no name")
	}
	if len(c.Packages) == 0 {
		return fmt.Errorf("assurance/register: control %s names no packages; a control a "+
			"reader cannot go and look at is a description, not a control", c.ID)
	}
	if strings.TrimSpace(string(c.Implementer)) == "" {
		return fmt.Errorf("assurance/register: control %s names no implementer", c.ID)
	}
	return nil
}

// GateRef is a production gate as this graph sees it.
//
// The gate's own definition and lifecycle live in pkg/gates. What is
// recorded here is the SUPPORT relationship: which controls it depends
// on and therefore which claims must hold before it can close.
type GateRef struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Controls []string `json:"controls"`
	// RequiredLevel is the assurance level every supporting claim must
	// reach before this gate may close.
	RequiredLevel state.State `json:"required_level"`
	// Mandatory gates block release; advisory ones inform it.
	Mandatory bool `json:"mandatory"`
}

// Graph is the whole assurance picture in one structure.
//
// Gate -> Control -> Claim -> Evidence -> Validator -> Level ->
// Release Decision. Every hop is checked, so a release cannot be
// justified by a chain that does not actually connect.
type Graph struct {
	controls map[string]Control
	claims   map[contract.ID]Claim
	debts    map[contract.ID]Debt
	gates    map[string]GateRef
}

// New builds a graph and refuses one whose references do not resolve.
func New(controls []Control, claims []Claim, debts []Debt, gates []GateRef) (*Graph, error) {
	g := &Graph{
		controls: map[string]Control{}, claims: map[contract.ID]Claim{},
		debts: map[contract.ID]Debt{}, gates: map[string]GateRef{},
	}
	for _, c := range controls {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if _, dup := g.controls[c.ID]; dup {
			return nil, fmt.Errorf("%w: control %s", ErrDuplicate, c.ID)
		}
		g.controls[c.ID] = c
	}
	for _, d := range debts {
		if err := d.Validate(); err != nil {
			return nil, err
		}
		if _, dup := g.debts[d.ID]; dup {
			return nil, fmt.Errorf("%w: debt %s", ErrDuplicate, d.ID)
		}
		g.debts[d.ID] = d
	}
	for _, c := range claims {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if _, dup := g.claims[c.ID]; dup {
			return nil, fmt.Errorf("%w: claim %s", ErrDuplicate, c.ID)
		}
		for _, ctl := range c.Controls {
			if _, ok := g.controls[ctl]; !ok {
				return nil, fmt.Errorf("%w: claim %s cites control %s", ErrDanglingRef, c.ID, ctl)
			}
		}
		for _, d := range c.Debts {
			if _, ok := g.debts[d]; !ok {
				return nil, fmt.Errorf("%w: claim %s cites debt %s", ErrDanglingRef, c.ID, d)
			}
		}
		g.claims[c.ID] = c
	}
	for _, gt := range gates {
		if strings.TrimSpace(gt.ID) == "" {
			return nil, errors.New("assurance/register: a gate has no id")
		}
		if _, dup := g.gates[gt.ID]; dup {
			return nil, fmt.Errorf("%w: gate %s", ErrDuplicate, gt.ID)
		}
		if len(gt.Controls) == 0 {
			return nil, fmt.Errorf("assurance/register: gate %s names no controls; a gate "+
				"with nothing underneath it can be closed by assertion", gt.ID)
		}
		for _, ctl := range gt.Controls {
			if _, ok := g.controls[ctl]; !ok {
				return nil, fmt.Errorf("%w: gate %s cites control %s", ErrDanglingRef, gt.ID, ctl)
			}
		}
		g.gates[gt.ID] = gt
	}
	// A debt that blocks a claim or gate must block one that exists.
	for _, d := range g.debts {
		for _, c := range d.BlockedClaims {
			if _, ok := g.claims[c]; !ok {
				return nil, fmt.Errorf("%w: debt %s blocks claim %s", ErrDanglingRef, d.ID, c)
			}
		}
		for _, gt := range d.BlockedGates {
			if _, ok := g.gates[gt]; !ok {
				return nil, fmt.Errorf("%w: debt %s blocks gate %s", ErrDanglingRef, d.ID, gt)
			}
		}
	}
	return g, nil
}

// ClaimsFor returns the claims made about a control.
func (g *Graph) ClaimsFor(control string) []Claim {
	var out []Claim
	for _, c := range g.claims {
		for _, ctl := range c.Controls {
			if ctl == control {
				out = append(out, c)
				break
			}
		}
	}
	SortClaims(out)
	return out
}

// GateSupport is what a gate rests on, walked to the bottom.
type GateSupport struct {
	Gate string
	// Claims are every claim under every control the gate names.
	Claims []Claim
	// Unmet are the claims that have not reached the gate's required
	// level, with the reason each falls short.
	Unmet []string
	// Debts are the outstanding evidence debts under it.
	Debts []Debt
	// Independent reports whether ANY supporting claim has independent
	// evidence. A gate whose entire support is self-produced is a gate
	// nobody outside has looked at.
	Independent bool
	// Uncovered are the controls the gate names about which no claim
	// has been made at all. This is the quiet failure: a gate that
	// looks supported because its other controls are.
	Uncovered []string
}

// Closable reports whether every supporting claim has reached the
// gate's required level and no debt is outstanding.
func (s GateSupport) Closable() bool {
	return len(s.Unmet) == 0 && len(s.Uncovered) == 0 && !s.hasOpenDebt()
}

func (s GateSupport) hasOpenDebt() bool {
	for _, d := range s.Debts {
		if d.Open() {
			return true
		}
	}
	return false
}

// Support walks a gate to the bottom of its chain.
func (g *Graph) Support(gateID string, at time.Time) (GateSupport, error) {
	gt, ok := g.gates[gateID]
	if !ok {
		return GateSupport{}, fmt.Errorf("%w: gate %s", ErrDanglingRef, gateID)
	}
	s := GateSupport{Gate: gateID}
	seen := map[contract.ID]bool{}
	for _, ctl := range gt.Controls {
		cs := g.ClaimsFor(ctl)
		if len(cs) == 0 {
			s.Uncovered = append(s.Uncovered, ctl)
			continue
		}
		for _, c := range cs {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			s.Claims = append(s.Claims, c)
			if _, independent := c.Validator(); independent {
				s.Independent = true
			}
			if c.CurrentLevel < gt.RequiredLevel || !c.Current(at) {
				// The shortfall is stated against the GATE's
				// requirement, not the claim's own. A claim can be at
				// the level its own risk demands and still be below
				// what a gate resting on it needs, and reporting the
				// claim's figure there would say a claim is short of
				// something it has already reached.
				var reason string
				switch {
				case !c.Current(at):
					reason = fmt.Sprintf("%s: the evidence expired on %s and has not been renewed",
						c.ID, c.Expiry.Format("2006-01-02"))
				default:
					next := c.CurrentLevel + 1
					reason = fmt.Sprintf("%s: at %s, this gate requires %s. The next rung is "+
						"%s, which requires %s", c.ID, c.CurrentLevel, gt.RequiredLevel,
						next, next.RequiredEvidence())
				}
				s.Unmet = append(s.Unmet, reason)
			}
			for _, d := range c.Debts {
				s.Debts = append(s.Debts, g.debts[d])
			}
		}
	}
	SortClaims(s.Claims)
	SortDebts(s.Debts)
	sort.Strings(s.Unmet)
	sort.Strings(s.Uncovered)
	return s, nil
}

// ReleaseDecision is the answer at the top of the graph.
type ReleaseDecision struct {
	Permitted bool
	// Reasons are why not, one per blocking condition. There is no
	// summary: a reader who acts on the first reason and stops has
	// still not seen the others.
	Reasons []string
	// OpenDebts is every outstanding debt, so the decision and the
	// work list are the same document.
	OpenDebts []Debt
	// SelfProducedGates are mandatory gates whose entire support is
	// VERIQO's own work.
	SelfProducedGates []string
}

// Release walks every mandatory gate and refuses on any that does not
// hold up.
func (g *Graph) Release(at time.Time) ReleaseDecision {
	d := ReleaseDecision{Permitted: true}
	var ids []string
	for id := range g.gates {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		gt := g.gates[id]
		s, err := g.Support(id, at)
		if err != nil {
			d.Permitted = false
			d.Reasons = append(d.Reasons, err.Error())
			continue
		}
		if !gt.Mandatory {
			continue
		}
		for _, u := range s.Uncovered {
			d.Permitted = false
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"%s (%s): control %s is named by the gate and no assurance claim covers it",
				id, gt.Title, u))
		}
		for _, u := range s.Unmet {
			d.Permitted = false
			d.Reasons = append(d.Reasons, fmt.Sprintf("%s (%s): %s", id, gt.Title, u))
		}
		if !s.Independent && len(s.Claims) > 0 {
			d.SelfProducedGates = append(d.SelfProducedGates, id)
		}
	}
	for _, id := range sortedDebtIDs(g.debts) {
		if g.debts[id].Open() {
			d.OpenDebts = append(d.OpenDebts, g.debts[id])
			d.Permitted = false
		}
	}
	if len(d.OpenDebts) > 0 {
		d.Reasons = append(d.Reasons, fmt.Sprintf(
			"%d evidence debt(s) are outstanding", len(d.OpenDebts)))
	}
	sort.Strings(d.Reasons)
	sort.Strings(d.SelfProducedGates)
	return d
}

func sortedDebtIDs(m map[contract.ID]Debt) []contract.ID {
	out := make([]contract.ID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Claims returns every claim, ordered.
func (g *Graph) Claims() []Claim {
	out := make([]Claim, 0, len(g.claims))
	for _, c := range g.claims {
		out = append(out, c)
	}
	SortClaims(out)
	return out
}

// Debts returns every debt, ordered.
func (g *Graph) Debts() []Debt {
	out := make([]Debt, 0, len(g.debts))
	for _, id := range sortedDebtIDs(g.debts) {
		out = append(out, g.debts[id])
	}
	return out
}

// Controls returns every control, ordered.
func (g *Graph) Controls() []Control {
	out := make([]Control, 0, len(g.controls))
	for _, c := range g.controls {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Gates returns every gate reference, ordered.
func (g *Graph) Gates() []GateRef {
	out := make([]GateRef, 0, len(g.gates))
	for _, gt := range g.gates {
		out = append(out, gt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Orphan is a control nothing asserts anything about.
//
// # Why it is a typed finding rather than a missing row
//
// A control with no assurance claim is the quiet gap: work that
// exists, is presumed fine, and has no stated property anybody could
// test. It does not appear in any report of what is unproven, because
// nothing was ever claimed about it -- so it is invisible in exactly
// the way that matters. An audit that only walks the claims will never
// reach it.
//
// Naming it makes it a finding the system produces about itself, with
// a consequence attached: every orphan should acquire either a claim
// or an evidence debt, and until it does, the gate it sits under
// cannot honestly be described as supported.
type Orphan struct {
	Control string `json:"control"`
	Name    string `json:"name"`
	// Gates are the production gates that rest on this control and are
	// therefore resting on nothing.
	Gates []string `json:"gates,omitempty"`
	// Packages says where to look, so the finding is actionable.
	Packages []string `json:"packages,omitempty"`
	// Consequence states what the orphan costs, so it can be
	// prioritised against a claim that merely falls short.
	Consequence string `json:"consequence"`
}

func (o Orphan) String() string {
	g := "no gate"
	if len(o.Gates) > 0 {
		g = strings.Join(o.Gates, ", ")
	}
	return fmt.Sprintf("ASSURANCE_ORPHAN %s (%s): nothing is claimed about it, and %s "+
		"rest(s) on it. %s", o.Control, strings.Join(o.Packages, ", "), g, o.Consequence)
}

// Orphans returns every control about which nothing is claimed.
//
// A good assurance system generates uncomfortable findings. If this
// ever returns nothing on a growing codebase, the likelier explanation
// is that controls stopped being registered than that every one
// acquired a claim.
func (g *Graph) Orphans() []Orphan {
	var out []Orphan
	for id, c := range g.controls {
		if len(g.ClaimsFor(id)) > 0 {
			continue
		}
		o := Orphan{Control: id, Name: c.Name, Packages: c.Packages,
			Consequence: "any gate resting on it is supported by an assumption rather " +
				"than by a claim; it needs either an assurance claim or an evidence debt"}
		for _, gt := range g.gates {
			for _, ctl := range gt.Controls {
				if ctl == id {
					o.Gates = append(o.Gates, gt.ID)
				}
			}
		}
		sort.Strings(o.Gates)
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Control < out[j].Control })
	return out
}

// UnclaimedControls returns the orphans' control ids.
func (g *Graph) UnclaimedControls() []string {
	var out []string
	for _, o := range g.Orphans() {
		out = append(out, o.Control)
	}
	return out
}

// Report renders the whole graph.
func (g *Graph) Report(at time.Time) string {
	var b strings.Builder
	b.WriteString("VERIQO MASTER ASSURANCE GRAPH\n")
	b.WriteString("  gate -> control -> claim -> evidence -> validator -> level -> release\n\n")

	for _, gt := range g.Gates() {
		s, err := g.Support(gt.ID, at)
		if err != nil {
			fmt.Fprintf(&b, "  %-6s ERROR %v\n", gt.ID, err)
			continue
		}
		mark := " "
		if gt.Mandatory {
			mark = "*"
		}
		verdict := "NOT CLOSABLE"
		if s.Closable() {
			verdict = "closable"
		}
		fmt.Fprintf(&b, "%s %-6s %-14s %s\n", mark, gt.ID, verdict, gt.Title)
		fmt.Fprintf(&b, "      requires %s of every supporting claim; %d claim(s), independent evidence: %v\n",
			gt.RequiredLevel, len(s.Claims), s.Independent)
		for _, u := range s.Uncovered {
			fmt.Fprintf(&b, "      UNCOVERED CONTROL: %s\n", u)
		}
		for _, u := range s.Unmet {
			fmt.Fprintf(&b, "      %s\n", u)
		}
	}

	b.WriteString("\nEVIDENCE DEBT\n")
	for _, d := range g.Debts() {
		b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(d.Describe(), "\n"), "\n", "\n  ") + "\n")
	}

	d := g.Release(at)
	b.WriteString("\nRELEASE DECISION\n")
	if d.Permitted {
		b.WriteString("  PERMITTED\n")
	} else {
		fmt.Fprintf(&b, "  NOT PERMITTED (%d reason(s)):\n", len(d.Reasons))
		for _, r := range d.Reasons {
			fmt.Fprintf(&b, "    - %s\n", r)
		}
	}
	if len(d.SelfProducedGates) > 0 {
		fmt.Fprintf(&b, "  %d mandatory gate(s) rest entirely on VERIQO's own evidence: %s\n",
			len(d.SelfProducedGates), strings.Join(d.SelfProducedGates, ", "))
	}
	if orphans := g.Orphans(); len(orphans) > 0 {
		fmt.Fprintf(&b, "\n  %d ASSURANCE ORPHAN(S). A control nothing claims anything "+
			"about does not\n  appear in any report of what is unproven, because nothing "+
			"was ever claimed:\n", len(orphans))
		for _, o := range orphans {
			fmt.Fprintf(&b, "    - %s\n", o)
		}
	}
	return b.String()
}
