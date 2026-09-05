// Package engine is VERIQO's four-stage shape, made refusable.
//
// The system decomposes into four engines and nothing else:
//
//	                 VERIQO
//	                    |
//	   +----------------+----------------+
//	   |                |                |
//	OBSERVE         ARBITRATE         QUALIFY
//	   |                |                |
//	   +----------------+----------------+
//	                    |
//	                 DECIDE
//	                    |
//	            DECISION PASSPORT
//	                    |
//	          +---------+---------+
//	       REPLAY              CHALLENGE
//
// # Why this is a package rather than a diagram
//
// A diagram in a README cannot be violated. Every architecture drawing
// ever produced has been correct on the day it was drawn and wrong
// eighteen months later, because nothing in the build checks it.
//
// The specific failures this package refuses are:
//
//	OBSERVE that ranks         an observation engine that decides which
//	                           reading is right has already arbitrated,
//	                           and the losing reading is gone before
//	                           anybody saw it
//
//	ARBITRATE that qualifies   weighing hypotheses is not the same act
//	                           as judging whether the sources deserve
//	                           weight; merging them means a strong
//	                           argument from a weak source outranks a
//	                           weak argument from a strong one, silently
//
//	QUALIFY that observes      an engine that may fetch what it needs
//	                           will fetch what confirms it
//
//	DECIDE without all three   the shortcut every system takes under
//	                           deadline: observations straight to a
//	                           decision, with the arbitration and the
//	                           qualification written up afterwards
//
// # DECIDE is not VERIQO's
//
// The fourth engine is drawn inside the VERIQO box, and it does not
// belong to VERIQO. VERIQO assembles the decision -- the observations,
// the surviving hypotheses, the qualification of each source, the
// disproof route -- and hands it to a principal who signs it.
//
// This mirrors pkg/epistemic/ladder, where a DECISION rung exists and
// VERIQO may never record one (ladder.ErrNotOurs). The two packages
// state the same rule at different scales, deliberately: the ladder
// states it for a single chain of reasoning, and this states it for
// the system.
//
// An automated principal may run OBSERVE, ARBITRATE and QUALIFY end to
// end. It may not close DECIDE. That is not a limitation of the
// implementation; it is the product.
//
// # The passport is not the end
//
// A decision passport that cannot be replayed and cannot be challenged
// is a PDF with a signature on it. Both outputs are required, and
// Seal() refuses a passport that offers neither.
package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

var (
	ErrUnknownStage = errors.New("engine: unknown stage")
	// ErrStageSkipped is DECIDE reached without one of its inputs.
	ErrStageSkipped = errors.New("engine: a decision was reached without one of its engines")
	// ErrOutOfOrder is a stage recorded before a stage it depends on.
	ErrOutOfOrder = errors.New("engine: a stage ran before what it rests on")
	// ErrStageOverreach is a stage doing another stage's work.
	ErrStageOverreach = errors.New("engine: a stage did another engine's work")
	// ErrNotOurs is VERIQO closing DECIDE.
	ErrNotOurs = errors.New("engine: VERIQO does not decide")
	// ErrAlreadyRecorded is a stage recorded twice. A second run that
	// silently replaced the first would let a disliked arbitration be
	// re-run until it came out differently, with no trace.
	ErrAlreadyRecorded = errors.New("engine: this engine has already run in this passage")
	// ErrNoReplay is a sealed passport that cannot be re-executed.
	ErrNoReplay = errors.New("engine: the passport records no replay manifest")
	// ErrNoChallenge is a sealed passport with no route to disproof.
	ErrNoChallenge = errors.New("engine: the passport offers no route to disproof")
	// ErrEmpty is a stage that produced nothing but claims to have run.
	ErrEmpty = errors.New("engine: an engine reports having run and produced nothing")
)

// Stage is one of the four engines.
type Stage string

const (
	// Observe reads the world. It records what a source said, with the
	// custody and the version it was read at. It does not judge.
	Observe Stage = "OBSERVE"
	// Arbitrate holds competing readings against each other. It
	// produces surviving hypotheses and named contradictions. It does
	// not judge whether the sources deserve the weight it gave them.
	Arbitrate Stage = "ARBITRATE"
	// Qualify judges the standing of what the other two used: source
	// grade, evidence quality, independence, lawful basis. It does not
	// fetch, and it does not rank hypotheses.
	Qualify Stage = "QUALIFY"
	// Decide is an authority act performed by a principal, on the
	// assembled product of the other three.
	Decide Stage = "DECIDE"
)

// Stages returns the four engines in a fixed order.
func Stages() []Stage { return []Stage{Observe, Arbitrate, Qualify, Decide} }

func (s Stage) Valid() bool {
	switch s {
	case Observe, Arbitrate, Qualify, Decide:
		return true
	}
	return false
}

// RestsOn returns the stages that must have run before this one.
//
// OBSERVE rests on nothing -- it is where evidence enters. ARBITRATE
// and QUALIFY both rest on OBSERVE and on nothing else: they are
// SIBLINGS, not a sequence, and either order is legitimate. Making one
// depend on the other would mean arbitration could see the source
// grades before weighing the readings, or qualification could see
// which reading was winning before grading its source. Both are ways
// of grading the answer you want.
func (s Stage) RestsOn() []Stage {
	switch s {
	case Observe:
		return nil
	case Arbitrate, Qualify:
		return []Stage{Observe}
	case Decide:
		return []Stage{Observe, Arbitrate, Qualify}
	}
	return nil
}

// MayBeAutomated reports whether a non-human principal may close this
// stage.
func (s Stage) MayBeAutomated() bool { return s != Decide }

// Produces names what a stage hands on, in the vocabulary of the rest
// of the system. It exists so that a stage's output can be checked
// against what that stage is for.
func (s Stage) Produces() string {
	switch s {
	case Observe:
		return "observations, each with a custody record and an evidence version"
	case Arbitrate:
		return "surviving hypotheses and the contradictions between them, none discarded"
	case Qualify:
		return "a standing for each source and each item of evidence, independent of the answer"
	case Decide:
		return "a signed decision passport, replayable and challengeable"
	}
	return ""
}

// MayNot states the act this stage is forbidden, and why.
//
// These are the four failures the separation exists to prevent, and
// each is a thing a competent engineer would do under deadline.
func (s Stage) MayNot() string {
	switch s {
	case Observe:
		return "rank, score or select between readings -- an observation engine that " +
			"picks the right one destroys the losing reading before anybody sees it"
	case Arbitrate:
		return "grade the sources it is weighing -- a strong argument from a weak source " +
			"would then outrank a weak argument from a strong one, and nothing would say so"
	case Qualify:
		return "fetch evidence -- an engine that may collect what it needs will collect " +
			"what confirms the grade it has already formed"
	case Decide:
		return "be closed by VERIQO -- assembling a decision and taking one are different " +
			"acts, and a system that does both has no principal to hold to it"
	}
	return ""
}

// Product is what one engine handed on.
//
// Count is deliberately not a score. It is how many items the engine
// emitted, so that "ARBITRATE ran and produced nothing" is a
// distinguishable state from "ARBITRATE did not run" -- the first is a
// finding about the evidence, the second is a hole in the process, and
// a system that renders them the same way will present the hole as a
// finding.
type Product struct {
	Stage Stage `json:"stage"`
	// By is the principal that closed the stage.
	By identity.Principal `json:"by"`
	// Refs are the artefacts produced: evidence versions, hypothesis
	// ids, qualification records. A stage that names none produced
	// nothing that can be examined.
	Refs []contract.ID `json:"refs"`
	// Summary is one sentence a reader can disagree with.
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

func (p Product) Validate() error {
	if !p.Stage.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownStage, p.Stage)
	}
	if err := p.By.Validate(); err != nil {
		return fmt.Errorf("engine: %s: %w", p.Stage, err)
	}
	if strings.TrimSpace(p.Summary) == "" {
		return fmt.Errorf("engine: %s produced no summary; a stage nobody can read is a "+
			"stage nobody can disagree with", p.Stage)
	}
	if len(p.Refs) == 0 {
		return fmt.Errorf("%w: %s", ErrEmpty, p.Stage)
	}
	for _, r := range p.Refs {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("engine: %s: %w", p.Stage, err)
		}
	}
	if p.At.IsZero() {
		return fmt.Errorf("engine: %s is not placed in time", p.Stage)
	}
	if !p.Stage.MayBeAutomated() && p.By.Kind.IsAutomated() {
		return fmt.Errorf("%w: %s was closed by %s, which is a %s",
			ErrNotOurs, p.Stage, p.By.ID, p.By.Kind)
	}
	return nil
}

// Passage is one journey through the four engines for one question.
//
// It is not a workflow engine. It records which engines ran, in what
// order, by whom, and refuses the orders that would let a conclusion
// be reached without the parts that make it examinable.
type Passage struct {
	ID       contract.ID `json:"id"`
	TenantID string      `json:"tenant_id"`
	// Question is what is being decided. A passage with no question
	// produces an answer to nothing.
	Question string `json:"question"`

	products map[Stage]Product
	seq      []Stage
}

// NewPassage opens a passage. Nothing has run yet.
func NewPassage(id contract.ID, tenantID, question string) (*Passage, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("engine: a passage is not anchored to a tenant")
	}
	if strings.TrimSpace(question) == "" {
		return nil, errors.New("engine: a passage with no question produces an answer to nothing")
	}
	return &Passage{
		ID: id, TenantID: tenantID, Question: question,
		products: map[Stage]Product{},
	}, nil
}

// Record closes one engine.
//
// It refuses a stage whose inputs have not run, a stage run twice, and
// a stage closed by a principal that may not close it. It does not
// refuse a stage that produced an unwelcome answer, because that is
// not a property this package can see and any check that claimed to
// see it would be a filter on conclusions.
func (p *Passage) Record(prod Product) error {
	if err := prod.Validate(); err != nil {
		return err
	}
	if prod.By.TenantID != p.TenantID {
		return fmt.Errorf("engine: %s was closed by a principal in tenant %q, not %q",
			prod.Stage, prod.By.TenantID, p.TenantID)
	}
	if _, ok := p.products[prod.Stage]; ok {
		return fmt.Errorf("%w: %s. Re-running an engine until it agrees is how a "+
			"conclusion gets laundered", ErrAlreadyRecorded, prod.Stage)
	}
	for _, need := range prod.Stage.RestsOn() {
		if _, ok := p.products[need]; !ok {
			return fmt.Errorf("%w: %s before %s. %s",
				ErrOutOfOrder, prod.Stage, need, whyOrder(prod.Stage, need))
		}
	}
	p.products[prod.Stage] = prod
	p.seq = append(p.seq, prod.Stage)
	return nil
}

// whyOrder explains a refused order in the terms of the thing it
// prevents, rather than restating the rule.
func whyOrder(stage, missing Stage) string {
	switch {
	case stage == Decide:
		switch missing {
		case Observe:
			return "A decision with no observations behind it is an opinion with a " +
				"signature on it."
		case Arbitrate:
			return "Nothing weighed the competing readings, so the decision rests on " +
				"whichever reading happened to be looked at first."
		case Qualify:
			return "No source was graded, so a claim from an anonymous forum post and " +
				"one from a flag registry carried equal weight."
		}
	case stage == Arbitrate:
		return "There is nothing to weigh: arbitration between zero readings always " +
			"produces the answer somebody already had."
	case stage == Qualify:
		return "There is nothing to grade, so any grading is of sources chosen after " +
			"the fact to support a conclusion."
	}
	return ""
}

// Ran reports whether a stage has been closed.
func (p *Passage) Ran(s Stage) bool { _, ok := p.products[s]; return ok }

// Product returns what a stage produced.
func (p *Passage) Product(s Stage) (Product, bool) { pr, ok := p.products[s]; return pr, ok }

// Missing returns the stages DECIDE still waits on, in fixed order.
func (p *Passage) Missing() []Stage {
	var out []Stage
	for _, s := range Decide.RestsOn() {
		if !p.Ran(s) {
			out = append(out, s)
		}
	}
	return out
}

// Order returns the stages in the order they actually ran.
func (p *Passage) Order() []Stage { return append([]Stage(nil), p.seq...) }

// Sealed is a passage that reached a decision, with the two outputs
// that make the decision examinable.
type Sealed struct {
	Passage *Passage
	// ReplayManifest is the id of the replay manifest for this
	// passage. Without it the decision can be read and not re-run, and
	// a decision that cannot be re-run cannot be shown to be wrong for
	// a reason other than disagreement.
	ReplayManifest contract.ID `json:"replay_manifest"`
	// DisproofRoute is the id of the route stating what would have to
	// be true for this decision to be wrong. A decision with no such
	// route is unfalsifiable, and an unfalsifiable decision is not an
	// intelligence product.
	DisproofRoute contract.ID `json:"disproof_route"`
}

// Seal closes the passage into a decision passport.
//
// The two ids are required. This is where the diagram's last row
// becomes enforceable: REPLAY and CHALLENGE are not features that
// might be added later, they are conditions on the passport existing.
func (p *Passage) Seal(replayManifest, disproofRoute contract.ID) (Sealed, error) {
	if missing := p.Missing(); len(missing) > 0 {
		names := make([]string, len(missing))
		for i, m := range missing {
			names[i] = string(m)
		}
		return Sealed{}, fmt.Errorf("%w: %s did not run", ErrStageSkipped,
			strings.Join(names, ", "))
	}
	if !p.Ran(Decide) {
		return Sealed{}, fmt.Errorf("%w: no principal has taken the decision. VERIQO "+
			"assembled it and may not close it", ErrNotOurs)
	}
	if replayManifest == "" {
		return Sealed{}, ErrNoReplay
	}
	if err := replayManifest.Validate(); err != nil {
		return Sealed{}, err
	}
	if disproofRoute == "" {
		return Sealed{}, ErrNoChallenge
	}
	if err := disproofRoute.Validate(); err != nil {
		return Sealed{}, err
	}
	return Sealed{Passage: p, ReplayManifest: replayManifest, DisproofRoute: disproofRoute}, nil
}

// Report renders the passage as something a reader can argue with.
func (p *Passage) Report() string {
	var b strings.Builder
	b.WriteString("PASSAGE " + string(p.ID) + "\n")
	b.WriteString("  question: " + p.Question + "\n\n")
	for _, s := range Stages() {
		prod, ok := p.products[s]
		if !ok {
			fmt.Fprintf(&b, "  %-10s NOT RUN\n", s)
			fmt.Fprintf(&b, "             would produce: %s\n", s.Produces())
			continue
		}
		fmt.Fprintf(&b, "  %-10s closed by %s (%s) at %s\n", s, prod.By.ID, prod.By.Kind,
			prod.At.UTC().Format(time.RFC3339))
		fmt.Fprintf(&b, "             %s\n", prod.Summary)
		refs := append([]contract.ID(nil), prod.Refs...)
		sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
		strs := make([]string, len(refs))
		for i, r := range refs {
			strs[i] = string(r)
		}
		fmt.Fprintf(&b, "             %d artefact(s): %s\n", len(refs), strings.Join(strs, ", "))
	}
	if m := p.Missing(); len(m) > 0 {
		b.WriteString("\n  NOT DECIDABLE. Waiting on:")
		for _, s := range m {
			b.WriteString(" " + string(s))
		}
		b.WriteString("\n")
	} else if !p.Ran(Decide) {
		b.WriteString("\n  ASSEMBLED, NOT DECIDED. Every input is present and no principal\n" +
			"  has taken the decision. VERIQO may not take it.\n")
	}
	return b.String()
}

// Describe renders the four engines and their refusals. It is what
// `veriqoctl engines` prints, and it is the drawing that is checked by
// the tests in this package rather than by nobody.
func Describe() string {
	var b strings.Builder
	b.WriteString("THE FOUR ENGINES\n")
	b.WriteString("  OBSERVE, ARBITRATE and QUALIFY are VERIQO's. DECIDE is not.\n\n")
	for _, s := range Stages() {
		fmt.Fprintf(&b, "  %s\n", s)
		b.WriteString(wrap("    produces  ", s.Produces()))
		b.WriteString(wrap("    may not   ", s.MayNot()))
		rest := s.RestsOn()
		if len(rest) == 0 {
			b.WriteString("    rests on  nothing -- this is where evidence enters\n")
		} else {
			names := make([]string, len(rest))
			for i, r := range rest {
				names[i] = string(r)
			}
			fmt.Fprintf(&b, "    rests on  %s\n", strings.Join(names, ", "))
		}
		if !s.MayBeAutomated() {
			b.WriteString("    closed by a human principal only\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("  A sealed passage carries a replay manifest and a disproof route.\n")
	b.WriteString("  A decision that cannot be re-run and cannot be shown wrong is a\n")
	b.WriteString("  document, not a finding, and Seal() refuses to produce one.\n")
	return b.String()
}

// wrap lays a sentence out under a label so the report fits a terminal.
// A line that wraps in the reader's window is a line they skim.
func wrap(label, text string) string {
	const width = 78
	indent := strings.Repeat(" ", len(label))
	var b strings.Builder
	line := label
	for i, word := range strings.Fields(text) {
		if i > 0 && len(line)+1+len(word) > width {
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			line = indent
		} else if i > 0 {
			line += " "
		}
		line += word
	}
	b.WriteString(strings.TrimRight(line, " ") + "\n")
	return b.String()
}
