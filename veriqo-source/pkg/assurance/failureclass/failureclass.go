// Package failureclass makes the response to a finding a discipline
// rather than a patch.
//
// # Why a fix is not a closure
//
// The review named the trap precisely: a system can pass its whole test
// suite because the tests were written to match what the code already
// does. Every finding VERIQO has closed so far was closed by fixing the
// site that was found. That is necessary and it is not sufficient,
// because the site that was found is one instance of a class, and the
// class is what recurs.
//
// The discipline is eight stages, and a response that stops before the
// end is a patch:
//
//	FINDING          what was observed, at the site where it was observed
//	ROOT CAUSE       why that site behaved that way
//	FAILURE CLASS    the general shape the root cause is an instance of
//	INVARIANT        the rule that must hold for the whole class
//	POSITIVE TEST    the control operating on the good case
//	NEGATIVE TEST    the control refusing the bad case it was built to refuse
//	MUTATION TEST    the forbidden value constructed directly, and rejected
//	REGRESSION TEST  the test that fails again if the finding returns
//
// The last three are the ones that get skipped. A negative test proves
// the control refuses what it was designed to refuse -- which is the
// control working, not the claim holding. A mutation test constructs
// the forbidden state by hand, going around the normal path, and
// demands the system still reject it: a mutant that survives is a hole
// whether or not any test exercises it. A regression test is the one
// that governs the whole module rather than the one site, so the class
// cannot come back somewhere else.
//
// This package is the register of those chains. It refuses an entry
// that stops early, and an architecture test resolves every test name
// it cites against the module, so a renamed test breaks the build
// instead of quietly emptying the discipline.
package failureclass

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrStageMissing      = errors.New("failureclass: the response stops before the discipline is complete")
	ErrUnknownClass      = errors.New("failureclass: the entry names a failure class that is not declared")
	ErrTestsNotDistinct  = errors.New("failureclass: two stages cite the same test, so one of them is not being done")
	ErrInvariantNotARule = errors.New("failureclass: the invariant is a description, not a rule that can be violated")
	ErrDuplicateID       = errors.New("failureclass: the entry ID appears twice")
)

// Stage is one step of the discipline.
type Stage string

const (
	StageFinding    Stage = "FINDING"
	StageRootCause  Stage = "ROOT_CAUSE"
	StageClass      Stage = "FAILURE_CLASS"
	StageInvariant  Stage = "INVARIANT"
	StagePositive   Stage = "POSITIVE_TEST"
	StageNegative   Stage = "NEGATIVE_TEST"
	StageMutation   Stage = "MUTATION_TEST"
	StageRegression Stage = "REGRESSION_TEST"
)

// Stages returns the discipline in order.
func Stages() []Stage {
	return []Stage{
		StageFinding, StageRootCause, StageClass, StageInvariant,
		StagePositive, StageNegative, StageMutation, StageRegression,
	}
}

// Meaning says what a stage is for, so that a stage cannot be filled in
// with something belonging to a different one.
func (s Stage) Meaning() string {
	switch s {
	case StageFinding:
		return "what was observed, at the site where it was observed"
	case StageRootCause:
		return "why that site behaved that way"
	case StageClass:
		return "the general shape the root cause is an instance of"
	case StageInvariant:
		return "the rule that must hold for the whole class, stated so it can be violated"
	case StagePositive:
		return "the control operating on the good case"
	case StageNegative:
		return "the control refusing the bad case it was built to refuse"
	case StageMutation:
		return "the forbidden value constructed directly, around the normal path, and rejected"
	case StageRegression:
		return "the test that fails again if the finding returns anywhere in the module"
	}
	return ""
}

// Class is the general shape of a failure.
type Class string

const (
	// AuthorityDiffusion: more than one place can bring an
	// authoritative object into existence.
	AuthorityDiffusion Class = "AUTHORITY_DIFFUSION"
	// VacuousVerification: the check passes because it was run over a
	// representation in which the thing it looks for cannot appear.
	VacuousVerification Class = "VACUOUS_VERIFICATION"
	// UnassessedAsAssessed: the absence of an assessment is read as a
	// favourable assessment.
	UnassessedAsAssessed Class = "UNASSESSED_TREATED_AS_ASSESSED"
	// StaleCitation: a reference that was true when written and is not
	// checked against the thing it refers to.
	StaleCitation Class = "STALE_CITATION"
	// FixtureNotGenuine: the test input reproduces the name of the
	// hard case without reproducing the hard case.
	FixtureNotGenuine Class = "FIXTURE_NOT_GENUINE"
	// SelfQualification: the subject of an assurance claim is also its
	// validator.
	SelfQualification Class = "SELF_QUALIFICATION"
	// SequencingBypass: a gate is placed after the thing it is supposed
	// to gate, so it audits rather than prevents.
	SequencingBypass Class = "SEQUENCING_BYPASS"
	// ScopeOverclaim: one number is reported carrying a meaning it does
	// not have.
	ScopeOverclaim Class = "SCOPE_OVERCLAIM"
	// OffsettingAttributes: a strong value on one dimension is allowed
	// to compensate for an absent value on another.
	OffsettingAttributes Class = "OFFSETTING_ATTRIBUTES"
	// IrreversibilityOverclaim: a transformation that hides content is
	// described as one that removes it.
	IrreversibilityOverclaim Class = "IRREVERSIBILITY_OVERCLAIM"
)

// Classes returns every declared class.
func Classes() []Class {
	return []Class{
		AuthorityDiffusion, VacuousVerification, UnassessedAsAssessed,
		StaleCitation, FixtureNotGenuine, SelfQualification,
		SequencingBypass, ScopeOverclaim, OffsettingAttributes,
		IrreversibilityOverclaim,
	}
}

func known(c Class) bool {
	for _, k := range Classes() {
		if k == c {
			return true
		}
	}
	return false
}

// Entry is one finding carried through the whole discipline.
type Entry struct {
	// ID is stable and citable.
	ID string
	// Round names the review round the finding came from, so the
	// register doubles as the history.
	Round string

	Finding   string
	RootCause string
	Class     Class
	Invariant string

	PositiveTest   string
	NegativeTest   string
	MutationTest   string
	RegressionTest string
}

// ruleWords are the forms an invariant can take. An invariant that
// cannot be violated is a description, and a description governs
// nothing.
var ruleWords = []string{"must", "may only", "never", "cannot", "no ", "only "}

// Validate refuses a response that stops early.
func (e Entry) Validate() error {
	missing := e.Missing()
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, s := range missing {
			names = append(names, string(s))
		}
		return fmt.Errorf("%w: %s stops at %s (missing: %s)",
			ErrStageMissing, e.ID, e.LastCompleteStage(), strings.Join(names, ", "))
	}
	if !known(e.Class) {
		return fmt.Errorf("%w: %s names %q", ErrUnknownClass, e.ID, e.Class)
	}
	seen := map[string]Stage{}
	for _, p := range []struct {
		stage Stage
		test  string
	}{
		{StagePositive, e.PositiveTest},
		{StageNegative, e.NegativeTest},
		{StageMutation, e.MutationTest},
		{StageRegression, e.RegressionTest},
	} {
		if prev, dup := seen[p.test]; dup {
			return fmt.Errorf("%w: %s cites %s as both %s and %s",
				ErrTestsNotDistinct, e.ID, p.test, prev, p.stage)
		}
		seen[p.test] = p.stage
	}
	lower := strings.ToLower(e.Invariant)
	isRule := false
	for _, w := range ruleWords {
		if strings.Contains(lower, w) {
			isRule = true
			break
		}
	}
	if !isRule {
		return fmt.Errorf("%w: %s says %q", ErrInvariantNotARule, e.ID, e.Invariant)
	}
	return nil
}

// Missing returns the stages that are not filled in, in order.
func (e Entry) Missing() []Stage {
	var out []Stage
	for _, s := range Stages() {
		if strings.TrimSpace(e.At(s)) == "" {
			out = append(out, s)
		}
	}
	return out
}

// At returns the content of one stage.
func (e Entry) At(s Stage) string {
	switch s {
	case StageFinding:
		return e.Finding
	case StageRootCause:
		return e.RootCause
	case StageClass:
		return string(e.Class)
	case StageInvariant:
		return e.Invariant
	case StagePositive:
		return e.PositiveTest
	case StageNegative:
		return e.NegativeTest
	case StageMutation:
		return e.MutationTest
	case StageRegression:
		return e.RegressionTest
	}
	return ""
}

// LastCompleteStage names how far the response actually got. A response
// that reached POSITIVE_TEST and stopped is the common shape of a
// patch, and naming it is more useful than a boolean.
func (e Entry) LastCompleteStage() Stage {
	last := Stage("NOTHING")
	for _, s := range Stages() {
		if strings.TrimSpace(e.At(s)) == "" {
			return last
		}
		last = s
	}
	return last
}

// Tests returns the four test names in stage order.
func (e Entry) Tests() []string {
	return []string{e.PositiveTest, e.NegativeTest, e.MutationTest, e.RegressionTest}
}

// Describe renders one chain.
func (e Entry) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] round %s\n", e.ID, e.Class, e.Round)
	for _, s := range Stages() {
		if s == StageClass {
			continue
		}
		fmt.Fprintf(&b, "    %-15s %s\n", strings.ToLower(string(s))+":", e.At(s))
	}
	return b.String()
}

// Register is the set of closed findings.
type Register struct {
	entries []Entry
}

// NewRegister validates every entry.
func NewRegister(entries ...Entry) (*Register, error) {
	r := &Register{}
	seen := map[string]bool{}
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			return nil, err
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateID, e.ID)
		}
		seen[e.ID] = true
		r.entries = append(r.entries, e)
	}
	return r, nil
}

// All returns a copy.
func (r *Register) All() []Entry { return append([]Entry(nil), r.entries...) }

// ByClass groups the register.
func (r *Register) ByClass() map[Class][]string {
	out := map[Class][]string{}
	for _, e := range r.entries {
		out[e.Class] = append(out[e.Class], e.ID)
	}
	return out
}

// UncoveredClasses returns declared classes with no closed finding.
// A non-empty result is not a defect: it means a shape has been named
// and not yet met. It becomes a defect the moment something is closed
// against it without going through the register.
func (r *Register) UncoveredClasses() []Class {
	have := r.ByClass()
	var out []Class
	for _, c := range Classes() {
		if len(have[c]) == 0 {
			out = append(out, c)
		}
	}
	return out
}

// CitedTests returns every test name the register depends on.
func (r *Register) CitedTests() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range r.entries {
		for _, t := range e.Tests() {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Report renders the register.
func (r *Register) Report() string {
	var b strings.Builder
	b.WriteString("VERIQO Failure-Class Register\n")
	b.WriteString("A finding is closed when its CLASS is closed, not when its SITE is fixed.\n")
	b.WriteString("Eight stages; a response that stops early is a patch:\n")
	for _, s := range Stages() {
		fmt.Fprintf(&b, "  %-15s %s\n", s, s.Meaning())
	}
	b.WriteString("\n")
	for _, e := range r.entries {
		b.WriteString(e.Describe())
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%d closed finding(s) across %d failure class(es); %d test(s) cited.\n",
		len(r.entries), len(r.ByClass()), len(r.CitedTests()))
	if u := r.UncoveredClasses(); len(u) > 0 {
		names := make([]string, 0, len(u))
		for _, c := range u {
			names = append(names, string(c))
		}
		fmt.Fprintf(&b, "Declared but not yet met: %s\n", strings.Join(names, ", "))
	}
	return b.String()
}
