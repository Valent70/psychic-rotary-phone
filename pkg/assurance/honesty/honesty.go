// Package honesty grades the strength of an honesty check, so that a
// weak one cannot be reported as a strong one.
//
// # The irony this package exists to prevent
//
// VERIQO builds honesty checks to detect overclaim. If an honesty
// check is itself a keyword screen, then:
//
//	honesty checker -> FALSE PASS -> false assurance
//
// and the system has manufactured exactly the confidence it exists to
// refuse -- with more credibility than an unchecked claim would have
// had, because it now carries a green tick.
//
// The failure is not that lexical screening is useless. It is that
// calling it "honesty verification" is an overclaim about an
// overclaim detector, which is the most self-undermining thing this
// codebase could do.
//
// # The five levels
//
//	H1  CLAIM_LANGUAGE_SCREENING
//	    Does the text contain phrases we have decided not to use?
//	    Defeated by paraphrase. Catches the accidental case only.
//
//	H2  STRUCTURAL_CLAIM_ANALYSIS
//	    Does the artefact have the parts a claim of this kind
//	    requires -- a scope, a disproof path, a stated limitation?
//	    Defeated by filling the fields with nothing.
//
//	H3  SEMANTIC_CONTRADICTION_ANALYSIS
//	    Does the artefact contradict itself, or contradict another
//	    artefact it cites? Catches the case where the summary says
//	    more than the body.
//
//	H4  EVIDENCE_TO_CLAIM_VALIDATION
//	    Does the evidence cited actually support the claim made?
//	    This is the first level that can catch a well-written lie.
//
//	H5  INDEPENDENT_EXTERNAL_REVIEW
//	    Did somebody who is not the author read it and say so?
//	    The only level that catches what the author could not see.
//
// # The naming rule
//
// A check may only be described using the name of the level it
// actually reaches. H1 is CLAIM_LANGUAGE_SCREENING and must never be
// called honesty verification, an honesty check, or validation. The
// Describe method is the only sanctioned rendering, and it states the
// level's defeat condition every time -- because a reader who is told
// what defeats a check cannot mistake it for one that nothing defeats.
package honesty

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrUnknownLevel = errors.New("honesty: unknown level")
	ErrOverstated   = errors.New("honesty: a check is described at a level above the one it performs")
)

// Level is how strong a check is.
type Level int

const (
	// None is the zero value: no check at all. It is a level so that
	// an unpopulated struct does not default to something better.
	None Level = iota
	// H1: lexical. Never call this honesty verification.
	H1
	// H2: structural.
	H2
	// H3: semantic contradiction.
	H3
	// H4: evidence-to-claim.
	H4
	// H5: independent external review.
	H5
)

var names = map[Level]string{
	None: "NO_CHECK",
	H1:   "CLAIM_LANGUAGE_SCREENING",
	H2:   "STRUCTURAL_CLAIM_ANALYSIS",
	H3:   "SEMANTIC_CONTRADICTION_ANALYSIS",
	H4:   "EVIDENCE_TO_CLAIM_VALIDATION",
	H5:   "INDEPENDENT_EXTERNAL_REVIEW",
}

// forbiddenNames are descriptions no level below H5 may use, because
// each implies a completeness none of them has.
var forbiddenNames = []string{
	"honesty verification", "honesty verified", "verified honest",
	"honesty check passed", "proven honest", "guaranteed accurate",
	"fully verified", "independently verified",
}

func (l Level) String() string {
	if n, ok := names[l]; ok {
		return n
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

func (l Level) MarshalJSON() ([]byte, error) { return []byte(`"` + l.String() + `"`), nil }

func (l Level) Valid() bool { _, ok := names[l]; return ok }

// Levels returns every level, weakest first.
func Levels() []Level {
	out := make([]Level, 0, len(names))
	for l := range names {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Question is what the level actually asks.
func (l Level) Question() string {
	switch l {
	case H1:
		return "does the text contain phrases we have decided not to use?"
	case H2:
		return "does the artefact have the parts a claim of this kind requires?"
	case H3:
		return "does the artefact contradict itself, or something it cites?"
	case H4:
		return "does the evidence cited actually support the claim made?"
	case H5:
		return "did somebody who is not the author read it and say so?"
	}
	return "nothing was asked"
}

// DefeatedBy states how the level fails.
//
// It is a required part of every rendering. A reader who is told what
// defeats a check cannot mistake it for one that nothing defeats, and
// that mistake is the whole failure mode.
func (l Level) DefeatedBy() string {
	switch l {
	case None:
		return "everything: nothing was checked"
	case H1:
		return "paraphrase. An author who avoids the words conveys the claim anyway, and " +
			"no phrase list catches them. It catches the accidental case only"
	case H2:
		return "filling the required fields with text that says nothing. Presence is not " +
			"substance"
	case H3:
		return "a claim that is internally consistent and wrong. Consistency is not truth"
	case H4:
		return "evidence that is itself mistaken, and by anything the author did not " +
			"think to cite"
	case H5:
		return "a reviewer who shares the author's assumptions, or whose scope excluded " +
			"the thing that matters. It is the strongest level available and it is not a " +
			"guarantee"
	}
	return ""
}

// CatchesADeliberateOverclaim reports whether the level can catch an
// author who is trying.
//
// Only H4 and H5. H1 through H3 catch carelessness, which is worth
// catching and is a different thing.
func (l Level) CatchesADeliberateOverclaim() bool { return l >= H4 }

// RequiresAnOutsideParty reports whether the level can be performed by
// the author.
func (l Level) RequiresAnOutsideParty() bool { return l == H5 }

// Parse resolves a level name.
func Parse(s string) (Level, error) {
	for k, v := range names {
		if strings.EqualFold(v, strings.TrimSpace(s)) {
			return k, nil
		}
	}
	return None, fmt.Errorf("%w: %q", ErrUnknownLevel, s)
}

// Check is one honesty check with the level it actually reaches.
type Check struct {
	// Name is what the check is called in the codebase.
	Name string `json:"name"`
	// Level is what it actually performs.
	Level Level `json:"level"`
	// Performs describes the mechanism, so the level can be argued
	// with rather than accepted.
	Performs string `json:"performs"`
	// Where is the code or script that runs it.
	Where string `json:"where"`
}

func (c Check) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("honesty: a check has no name")
	}
	if !c.Level.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownLevel, c.Level)
	}
	if strings.TrimSpace(c.Performs) == "" {
		return fmt.Errorf("honesty: %s does not say what it does, so its level cannot be "+
			"argued with", c.Name)
	}
	if strings.TrimSpace(c.Where) == "" {
		return fmt.Errorf("honesty: %s does not say where it runs", c.Name)
	}
	return DescribeSafely(c.Level, c.Name+" "+c.Performs)
}

// DescribeSafely refuses a description that claims more than the level
// supports.
//
// This is the naming rule as code. A check at H1 described as "honesty
// verification" is an overclaim about an overclaim detector, and it is
// refused at the point the description is written.
func DescribeSafely(l Level, description string) error {
	if l >= H5 {
		return nil
	}
	lower := strings.ToLower(description)
	for _, f := range forbiddenNames {
		if strings.Contains(lower, f) {
			return fmt.Errorf("%w: %q describes a %s check, which is defeated by %s",
				ErrOverstated, f, l, l.DefeatedBy())
		}
	}
	return nil
}

// Describe renders a check with its defeat condition.
func (c Check) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-38s %s\n", c.Name, c.Level)
	fmt.Fprintf(&b, "  asks:        %s\n", c.Level.Question())
	fmt.Fprintf(&b, "  performs:    %s\n", c.Performs)
	fmt.Fprintf(&b, "  where:       %s\n", c.Where)
	fmt.Fprintf(&b, "  defeated by: %s\n", c.Level.DefeatedBy())
	return b.String()
}

// Suite is the set of checks a system runs.
type Suite struct {
	checks []Check
}

// NewSuite validates every check.
func NewSuite(cs ...Check) (*Suite, error) {
	s := &Suite{}
	seen := map[string]bool{}
	for _, c := range cs {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("honesty: %s appears twice", c.Name)
		}
		seen[c.Name] = true
		s.checks = append(s.checks, c)
	}
	sort.Slice(s.checks, func(i, j int) bool {
		if s.checks[i].Level != s.checks[j].Level {
			return s.checks[i].Level > s.checks[j].Level
		}
		return s.checks[i].Name < s.checks[j].Name
	})
	return s, nil
}

// Highest returns the strongest level any check in the suite reaches.
//
// It is the number a reader should be given, and it is deliberately
// NOT an average: a suite of forty H1 checks and no H4 check reaches
// H1, and averaging would let quantity substitute for strength --
// which is the test-inflation failure in a different costume.
func (s *Suite) Highest() Level {
	h := None
	for _, c := range s.checks {
		if c.Level > h {
			h = c.Level
		}
	}
	return h
}

// Checks returns every check, strongest first.
func (s *Suite) Checks() []Check { return append([]Check(nil), s.checks...) }

// Missing returns the levels the suite does not reach at all.
func (s *Suite) Missing() []Level {
	have := map[Level]bool{}
	for _, c := range s.checks {
		have[c.Level] = true
	}
	var out []Level
	for _, l := range Levels() {
		if l != None && !have[l] {
			out = append(out, l)
		}
	}
	return out
}

// Report renders the suite.
func (s *Suite) Report() string {
	var b strings.Builder
	b.WriteString("HONESTY CHECK LEVELS\n")
	b.WriteString("  A check that detects overclaim can itself overclaim. These are\n")
	b.WriteString("  graded so that a weak check cannot be reported as a strong one.\n\n")
	for _, c := range s.checks {
		b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(c.Describe(), "\n"),
			"\n", "\n  ") + "\n\n")
	}
	h := s.Highest()
	fmt.Fprintf(&b, "  Highest level reached: %s\n", h)
	if !h.CatchesADeliberateOverclaim() {
		b.WriteString("  No check here can catch an author who is trying. H1 to H3 catch\n")
		b.WriteString("  carelessness, which is worth catching and is a different thing.\n")
	}
	if m := s.Missing(); len(m) > 0 {
		var ns []string
		for _, l := range m {
			ns = append(ns, l.String())
		}
		fmt.Fprintf(&b, "  Not performed at all: %s\n", strings.Join(ns, ", "))
	}
	b.WriteString("\n  The count of checks is not the measure. A suite of forty H1 checks\n")
	b.WriteString("  reaches H1, and reporting an average would let quantity substitute\n")
	b.WriteString("  for strength -- the test-inflation failure in a different costume.\n")
	return b.String()
}
