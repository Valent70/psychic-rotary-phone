// Package ladder separates what was seen from what it was taken to
// mean.
//
// # The three sentences
//
//	"The vessel stopped at Port X for six hours."
//	"The vessel discharged cargo at Port X."
//	"The carrier breached the contract."
//
// In a report they look alike. In a dispute they are three completely
// different things, and the difference decides who wins:
//
//	the first is an OBSERVATION -- a position feed says so, and the
//	feed can be produced;
//
//	the second is a HYPOTHESIS unless somebody has a cargo document.
//	A stop is consistent with discharge, with bunkering, with waiting
//	for a berth, and with an engine fault;
//
//	the third is an ASSERTION about a legal instrument, and it is not
//	VERIQO's to make at all.
//
// A system that stores all three in the same field, with the same
// confidence type, has already lost the argument. The opposing expert
// will ask "on what evidence did you conclude discharge", and the
// honest answer -- "we inferred it from a stop" -- will be extracted
// under cross-examination rather than volunteered in the report. That
// is the difference between a finding that survives and one that does
// not.
//
// So the rungs are TYPES, the transitions between them are checked,
// and each rung carries what the one below it could not supply.
package ladder

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/epistemic"
)

var (
	ErrUnknownKind        = errors.New("epistemic/ladder: unknown kind")
	ErrSkippedRung        = errors.New("epistemic/ladder: a statement rests on a kind it may not rest on")
	ErrNoBasis            = errors.New("epistemic/ladder: a derived statement cites nothing")
	ErrNotOurs            = errors.New("epistemic/ladder: this kind of statement is not VERIQO's to make")
	ErrObservationDerived = errors.New("epistemic/ladder: an observation cannot be derived from anything")
)

// Kind is where a statement sits between what was seen and what was
// concluded.
type Kind int

const (
	// Observation: something a sensor, a document or a witness
	// recorded. It is the only kind that rests on nothing else, and
	// the only kind that can be produced rather than argued.
	Observation Kind = iota
	// Inference: a mechanical derivation from observations, with no
	// judgement in it. "The two positions are 40 NM apart" is an
	// inference from two observations; anyone with the same
	// observations and the same arithmetic gets the same answer.
	Inference
	// Hypothesis: an account of the observations that could be wrong.
	// It is where judgement enters, and where alternatives must exist
	// -- a hypothesis with no competitors is an assertion in disguise.
	Hypothesis
	// Assertion: a statement about the world offered as true. It
	// requires a hypothesis that survived competition AND a named
	// person who is willing to stand behind it.
	Assertion
	// Decision: an act taken on the strength of an assertion --
	// paying a claim, rejecting a shipment, declining a counterparty.
	// It belongs to whoever has the authority, and never to VERIQO.
	Decision
)

var names = map[Kind]string{
	Observation: "OBSERVATION", Inference: "INFERENCE", Hypothesis: "HYPOTHESIS",
	Assertion: "ASSERTION", Decision: "DECISION",
}

func (k Kind) String() string {
	if n, ok := names[k]; ok {
		return n
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

func (k Kind) MarshalJSON() ([]byte, error) { return []byte(`"` + k.String() + `"`), nil }

func (k Kind) Valid() bool { _, ok := names[k]; return ok }

// Kinds returns every kind in ladder order.
func Kinds() []Kind {
	out := make([]Kind, 0, len(names))
	for k := range names {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func Parse(s string) (Kind, error) {
	for k, v := range names {
		if strings.EqualFold(v, strings.TrimSpace(s)) {
			return k, nil
		}
	}
	return Observation, fmt.Errorf("%w: %q", ErrUnknownKind, s)
}

// Question is what a reader should ask of a statement of this kind.
func (k Kind) Question() string {
	switch k {
	case Observation:
		return "who recorded it, and can the record be produced?"
	case Inference:
		return "would anyone with the same observations reach the same result?"
	case Hypothesis:
		return "what are the competing accounts, and what would separate them?"
	case Assertion:
		return "who is standing behind this, and on which hypothesis?"
	case Decision:
		return "who took it, under what authority, and could they have taken another?"
	}
	return ""
}

// Reversible reports whether a statement of this kind can be undone by
// new evidence without anybody having acted on it.
//
// Everything up to Assertion can. A Decision cannot: money has moved,
// a shipment has been rejected, a counterparty has been declined. That
// asymmetry is why the ladder exists -- the cost of getting the second
// rung wrong is borne at the fifth.
func (k Kind) Reversible() bool { return k < Decision }

// VeriqoMayMake reports whether VERIQO may originate a statement of
// this kind.
//
// Observation, Inference and Hypothesis: yes. Assertion: only with a
// named human standing behind it. Decision: never -- a system that
// decides is not a neutral one, and the whole positioning rests on
// not being a party.
func (k Kind) VeriqoMayMake() bool { return k <= Hypothesis }

// MayRestOn reports whether a statement of kind k may cite a statement
// of kind on.
//
// The rules, and the reasoning:
//
//	an OBSERVATION rests on nothing. If it cites something, it is not
//	an observation -- it is an inference wearing the word;
//
//	an INFERENCE rests on observations and other inferences only. The
//	moment it rests on a hypothesis it inherits that hypothesis's
//	uncertainty and stops being mechanical;
//
//	a HYPOTHESIS may rest on anything below it;
//
//	an ASSERTION rests on a hypothesis, never directly on an
//	observation. "The vessel discharged cargo, because it stopped" is
//	the error this rule exists to name: the stop is an observation and
//	the discharge is an account of it, and skipping the middle rung is
//	skipping the place where alternatives would have been considered;
//
//	a DECISION rests on an assertion.
func (k Kind) MayRestOn(on Kind) bool {
	switch k {
	case Observation:
		return false
	case Inference:
		return on == Observation || on == Inference
	case Hypothesis:
		return on <= Hypothesis
	case Assertion:
		return on == Hypothesis
	case Decision:
		return on == Assertion
	}
	return false
}

// Statement is one node in the chain.
type Statement struct {
	ID   contract.ID `json:"id"`
	Kind Kind        `json:"kind"`
	// Text is the statement itself, in the form it would appear in a
	// report.
	Text string `json:"text"`
	// RestsOn cites the statements this one is built from.
	RestsOn []contract.ID `json:"rests_on,omitempty"`
	// Recorder, for an observation: who recorded it.
	Recorder string `json:"recorder,omitempty"`
	// EvidenceRefs, for an observation: what can be produced.
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	// Method, for an inference: the computation, named so that
	// somebody else can repeat it.
	Method string `json:"method,omitempty"`
	// Alternatives, for a hypothesis: the competing accounts. A
	// hypothesis with none is an assertion in disguise.
	Alternatives []string `json:"alternatives,omitempty"`
	// Discriminator, for a hypothesis: what evidence would separate it
	// from its alternatives.
	Discriminator string `json:"discriminator,omitempty"`
	// StandsBehind, for an assertion: the named person.
	StandsBehind contract.ID `json:"stands_behind,omitempty"`
	// Authority, for a decision: under what power it was taken.
	Authority string `json:"authority,omitempty"`
	// TakenBy, for a decision: who took it. Never VERIQO.
	TakenBy string `json:"taken_by,omitempty"`
	// State is the epistemic state of the underlying material.
	State epistemic.State `json:"state"`
	At    time.Time       `json:"at"`
}

func (s Statement) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" {
		return fmt.Errorf("%w: a statement has no id", contract.ErrMalformedID)
	}
	if !s.Kind.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownKind, s.Kind)
	}
	if strings.TrimSpace(s.Text) == "" {
		return fmt.Errorf("epistemic/ladder: %s says nothing", s.ID)
	}
	if s.At.IsZero() {
		return fmt.Errorf("epistemic/ladder: %s has no instant", s.ID)
	}

	switch s.Kind {
	case Observation:
		if len(s.RestsOn) > 0 {
			return fmt.Errorf("%w: %s is an OBSERVATION and cites %d statement(s). "+
				"A statement built from others is an inference wearing the word",
				ErrObservationDerived, s.ID, len(s.RestsOn))
		}
		if strings.TrimSpace(s.Recorder) == "" {
			return fmt.Errorf("epistemic/ladder: %s is an OBSERVATION and names no "+
				"recorder. An observation nobody made is a hypothesis", s.ID)
		}
		if len(s.EvidenceRefs) == 0 && s.State.MayIncreaseAssurance() {
			return fmt.Errorf("epistemic/ladder: %s is an OBSERVATION in state %s and "+
				"cites no evidence that could be produced", s.ID, s.State)
		}

	case Inference:
		if len(s.RestsOn) == 0 {
			return fmt.Errorf("%w: %s is an INFERENCE and cites nothing", ErrNoBasis, s.ID)
		}
		if strings.TrimSpace(s.Method) == "" {
			return fmt.Errorf("epistemic/ladder: %s is an INFERENCE and names no method. "+
				"An inference nobody can repeat is a judgement", s.ID)
		}

	case Hypothesis:
		if len(s.RestsOn) == 0 {
			return fmt.Errorf("%w: %s is a HYPOTHESIS and cites nothing", ErrNoBasis, s.ID)
		}
		if len(s.Alternatives) == 0 {
			return fmt.Errorf("epistemic/ladder: %s is a HYPOTHESIS with no competing "+
				"accounts. A hypothesis with no alternatives is an assertion in disguise",
				s.ID)
		}
		if strings.TrimSpace(s.Discriminator) == "" {
			return fmt.Errorf("epistemic/ladder: %s is a HYPOTHESIS and does not say what "+
				"evidence would separate it from its alternatives", s.ID)
		}

	case Assertion:
		if strings.TrimSpace(string(s.StandsBehind)) == "" {
			return fmt.Errorf("epistemic/ladder: %s is an ASSERTION and nobody stands "+
				"behind it. An unowned assertion is a rumour with a citation", s.ID)
		}
		if len(s.RestsOn) == 0 {
			return fmt.Errorf("%w: %s is an ASSERTION and cites nothing", ErrNoBasis, s.ID)
		}

	case Decision:
		if strings.TrimSpace(s.TakenBy) == "" || strings.TrimSpace(s.Authority) == "" {
			return fmt.Errorf("epistemic/ladder: %s is a DECISION and does not say who "+
				"took it under what authority", s.ID)
		}
		if strings.EqualFold(s.TakenBy, "veriqo") ||
			strings.Contains(strings.ToLower(s.TakenBy), "veriqo") {
			return fmt.Errorf("%w: %s records VERIQO as taking a DECISION. A system that "+
				"decides is not a neutral one, and the whole positioning rests on not "+
				"being a party", ErrNotOurs, s.ID)
		}
		if len(s.RestsOn) == 0 {
			return fmt.Errorf("%w: %s is a DECISION and cites nothing", ErrNoBasis, s.ID)
		}
	}
	return nil
}

// Chain is a set of statements with their citations resolved.
type Chain struct {
	statements map[contract.ID]Statement
	order      []contract.ID
}

// NewChain builds and checks a chain.
//
// It refuses a citation that skips a rung, which is the error the
// whole package exists to catch: an assertion resting directly on an
// observation has skipped the place where alternatives would have been
// considered.
func NewChain(ss ...Statement) (*Chain, error) {
	c := &Chain{statements: map[contract.ID]Statement{}}
	for _, s := range ss {
		if err := s.Validate(); err != nil {
			return nil, err
		}
		if _, dup := c.statements[s.ID]; dup {
			return nil, fmt.Errorf("epistemic/ladder: %s appears twice", s.ID)
		}
		c.statements[s.ID] = s
		c.order = append(c.order, s.ID)
	}
	for _, id := range c.order {
		s := c.statements[id]
		for _, on := range s.RestsOn {
			cited, ok := c.statements[on]
			if !ok {
				return nil, fmt.Errorf("epistemic/ladder: %s cites %s, which does not exist",
					id, on)
			}
			if !s.Kind.MayRestOn(cited.Kind) {
				return nil, fmt.Errorf("%w: %s is an %s and rests on %s, which is an %s. %s",
					ErrSkippedRung, id, s.Kind, on, cited.Kind, why(s.Kind, cited.Kind))
			}
		}
	}
	if cyc := c.findCycle(); cyc != "" {
		return nil, fmt.Errorf("epistemic/ladder: the chain contains a cycle: %s", cyc)
	}
	return c, nil
}

// why explains a refused citation in the terms that matter.
func why(k, on Kind) string {
	switch {
	case k == Assertion && on == Observation:
		return "'the vessel discharged cargo, because it stopped' is this error: the stop " +
			"is an observation and the discharge is an account of it, and skipping the " +
			"hypothesis rung skips the place where alternatives would have been considered"
	case k == Inference && on >= Hypothesis:
		return "an inference resting on a hypothesis inherits that hypothesis's " +
			"uncertainty and stops being mechanical; call it a hypothesis instead"
	case k == Observation:
		return "an observation rests on nothing; a statement built from others is an " +
			"inference"
	case k == Decision && on != Assertion:
		return "a decision rests on an assertion somebody stands behind, not on the " +
			"material underneath it"
	}
	return "the ladder does not permit this citation"
}

func (c *Chain) findCycle() string {
	const white, grey, black = 0, 1, 2
	colour := map[contract.ID]int{}
	var path []string
	var walk func(contract.ID) string
	walk = func(id contract.ID) string {
		colour[id] = grey
		path = append(path, string(id))
		for _, on := range c.statements[id].RestsOn {
			switch colour[on] {
			case grey:
				return strings.Join(append(path, string(on)), " -> ")
			case white:
				if cy := walk(on); cy != "" {
					return cy
				}
			}
		}
		path = path[:len(path)-1]
		colour[id] = black
		return ""
	}
	for _, id := range c.order {
		if colour[id] == white {
			if cy := walk(id); cy != "" {
				return cy
			}
		}
	}
	return ""
}

// Get returns a statement.
func (c *Chain) Get(id contract.ID) (Statement, bool) {
	s, ok := c.statements[id]
	return s, ok
}

// OfKind returns every statement of a kind, in insertion order.
func (c *Chain) OfKind(k Kind) []Statement {
	var out []Statement
	for _, id := range c.order {
		if c.statements[id].Kind == k {
			out = append(out, c.statements[id])
		}
	}
	return out
}

// Foundation returns the observations a statement ultimately rests on.
//
// It is the question an opposing expert asks first, and the answer
// should be available without anybody reconstructing it.
func (c *Chain) Foundation(id contract.ID) ([]Statement, error) {
	if _, ok := c.statements[id]; !ok {
		return nil, fmt.Errorf("epistemic/ladder: %s does not exist", id)
	}
	seen := map[contract.ID]bool{}
	var out []Statement
	var walk func(contract.ID)
	walk = func(cur contract.ID) {
		if seen[cur] {
			return
		}
		seen[cur] = true
		s := c.statements[cur]
		if s.Kind == Observation {
			out = append(out, s)
			return
		}
		for _, on := range s.RestsOn {
			walk(on)
		}
	}
	walk(id)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// JudgementDistance reports how many hypothesis rungs sit between a
// statement and its observations.
//
// Zero means the statement is mechanical. One means a single account
// was chosen. Two or more means an account was built on an account,
// which is where confidence quietly compounds and where a dispute is
// most likely to be won by the other side.
func (c *Chain) JudgementDistance(id contract.ID) (int, error) {
	if _, ok := c.statements[id]; !ok {
		return 0, fmt.Errorf("epistemic/ladder: %s does not exist", id)
	}
	memo := map[contract.ID]int{}
	var walk func(contract.ID) int
	walk = func(cur contract.ID) int {
		if v, ok := memo[cur]; ok {
			return v
		}
		s := c.statements[cur]
		best := 0
		for _, on := range s.RestsOn {
			if d := walk(on); d > best {
				best = d
			}
		}
		if s.Kind == Hypothesis {
			best++
		}
		memo[cur] = best
		return best
	}
	return walk(id), nil
}

// Report renders the chain so the rungs are visible.
func (c *Chain) Report() string {
	var b strings.Builder
	b.WriteString("EPISTEMIC CHAIN\n")
	b.WriteString("  what was seen, what it was taken to mean, and who said so\n\n")
	for _, k := range Kinds() {
		ss := c.OfKind(k)
		if len(ss) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s -- %s\n", k, k.Question())
		for _, s := range ss {
			fmt.Fprintf(&b, "  %-14s %s\n", s.ID, s.Text)
			switch s.Kind {
			case Observation:
				fmt.Fprintf(&b, "  %-14s   recorded by %s [%s]\n", "", s.Recorder, s.State)
			case Inference:
				fmt.Fprintf(&b, "  %-14s   method: %s\n", "", s.Method)
			case Hypothesis:
				fmt.Fprintf(&b, "  %-14s   competing: %s\n", "",
					strings.Join(s.Alternatives, "; "))
				fmt.Fprintf(&b, "  %-14s   separated by: %s\n", "", s.Discriminator)
			case Assertion:
				fmt.Fprintf(&b, "  %-14s   stands behind it: %s\n", "", s.StandsBehind)
			case Decision:
				fmt.Fprintf(&b, "  %-14s   taken by %s under %s\n", "", s.TakenBy, s.Authority)
			}
			if d, err := c.JudgementDistance(s.ID); err == nil && d > 0 {
				fmt.Fprintf(&b, "  %-14s   %d judgement(s) between this and the "+
					"observations\n", "", d)
			}
		}
		b.WriteString("\n")
	}
	if len(c.OfKind(Decision)) == 0 {
		b.WriteString("  No DECISION is recorded here. VERIQO does not take them: a system\n")
		b.WriteString("  that decides is not a neutral one.\n")
	}
	return b.String()
}
