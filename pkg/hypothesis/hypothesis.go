// Package hypothesis holds competing explanations and the
// counterfactual engine that tests them.
//
// # The question the engine asks
//
//	If hypothesis H were true, what would we EXPECT to observe?
//
// Then it compares the expectation with the record: expected,
// observed, missing, contradicted. That comparison is what turns a set
// of hypotheses into an argument rather than a list.
//
// # Why hypotheses are held as a SET
//
// A single hypothesis assessed alone is always "consistent with the
// evidence", because evidence that would distinguish it from the
// alternatives was never sought. Competing hypotheses have to be
// enumerated and scored against the SAME evidence, which is what makes
// an observation DIAGNOSTIC -- and diagnosticity is a property of an
// observation relative to a set, not of an observation alone.
//
// # What this package refuses to produce
//
// A winner. Assess returns the hypotheses ranked with their
// inconsistencies, and it says explicitly when two are indistinguishable
// on the available evidence. A ranking that always names a leader lets
// a 51/49 split be reported the same way as a 99/1 one.
package hypothesis

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/contract"
)

var (
	ErrNoStatement      = errors.New("hypothesis: a hypothesis must state what it explains")
	ErrNoExpectation    = errors.New("hypothesis: a hypothesis with no expected observations cannot be tested")
	ErrSingleHypothesis = errors.New("hypothesis: a set with one hypothesis cannot be discriminating; " +
		"assessed alone, every hypothesis is consistent with the evidence")
	ErrDuplicate  = errors.New("hypothesis: duplicate hypothesis id")
	ErrUnknown    = errors.New("hypothesis: unknown hypothesis")
	ErrNoEvidence = errors.New("hypothesis: no evidence was assessed")
)

// Consistency is how an observation bears on a hypothesis.
//
// The scale is the Analysis of Competing Hypotheses one, and the
// asymmetry is deliberate: INCONSISTENT counts for much more than
// CONSISTENT, because evidence consistent with a hypothesis is usually
// consistent with several, while evidence inconsistent with one
// eliminates it.
type Consistency string

const (
	// NotAssessed is the zero value.
	NotAssessed Consistency = ""
	// StronglyConsistent: the hypothesis predicts this observation
	// specifically.
	StronglyConsistent Consistency = "STRONGLY_CONSISTENT"
	Consistent         Consistency = "CONSISTENT"
	// Neutral: the observation does not bear on this hypothesis. Most
	// cells in a real matrix are this, and a matrix without them is
	// one where somebody forced a judgement.
	NeutralC     Consistency = "NEUTRAL"
	Inconsistent Consistency = "INCONSISTENT"
	// StronglyInconsistent: the hypothesis is eliminated unless the
	// observation itself is wrong.
	StronglyInconsistent Consistency = "STRONGLY_INCONSISTENT"
)

func Consistencies() []Consistency {
	return []Consistency{StronglyConsistent, Consistent, NeutralC,
		Inconsistent, StronglyInconsistent, NotAssessed}
}

func (c Consistency) Valid() bool {
	for _, x := range Consistencies() {
		if x == c {
			return true
		}
	}
	return false
}

func (c Consistency) String() string {
	if c == NotAssessed {
		return "NOT_ASSESSED"
	}
	return string(c)
}

// Score is the ACH weight. Inconsistency dominates.
func (c Consistency) Score() float64 {
	switch c {
	case StronglyConsistent:
		return 1
	case Consistent:
		return 0.5
	case NeutralC:
		return 0
	case Inconsistent:
		return -2
	case StronglyInconsistent:
		return -5
	}
	return 0
}

func (c Consistency) Eliminates() bool { return c == StronglyInconsistent }

func (c Consistency) Assessed() bool { return c != NotAssessed }

// Hypothesis is one candidate explanation.
type Hypothesis struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	// Expected names what would be observed if this were the
	// explanation. It is the counterfactual, and a hypothesis without
	// one cannot be tested -- only asserted.
	Expected []string `json:"expected"`
	// Excluded names what would NOT be observed if this were true.
	// Observing one of these is the strongest evidence against it.
	Excluded []string `json:"excluded,omitempty"`
}

func (h Hypothesis) Validate() error {
	if strings.TrimSpace(h.ID) == "" {
		return errors.New("hypothesis: no id")
	}
	if strings.TrimSpace(h.Statement) == "" {
		return fmt.Errorf("%w: %s", ErrNoStatement, h.ID)
	}
	if len(h.Expected) == 0 {
		return fmt.Errorf("%w: %s", ErrNoExpectation, h.ID)
	}
	return nil
}

// Observation is one piece of evidence with its properties.
type Observation struct {
	ID     string `json:"id"`
	Detail string `json:"detail"`

	// Reliability, Independence and Freshness qualify the observation
	// itself. They are separate from consistency because a highly
	// diagnostic observation from an unreliable source should not
	// settle a case, and a single blended weight would let it.
	Reliability  float64 `json:"reliability"`
	Independence float64 `json:"independence"`
	Freshness    float64 `json:"freshness"`

	// TemporalFit and MeasurementCompatibility record whether the
	// observation actually applies: an accurate measurement on an
	// incompatible basis is accurate and irrelevant.
	TemporalFit           bool `json:"temporal_fit"`
	MeasurementCompatible bool `json:"measurement_compatible"`

	EvidenceRefs []string `json:"evidence_refs"`
}

func (o Observation) Validate() error {
	if strings.TrimSpace(o.ID) == "" {
		return errors.New("hypothesis: an observation has no id")
	}
	if len(o.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: observation %s cites nothing", ErrNoEvidence, o.ID)
	}
	for name, v := range map[string]float64{
		"reliability": o.Reliability, "independence": o.Independence, "freshness": o.Freshness} {
		if v < 0 || v > 1 {
			return fmt.Errorf("hypothesis: observation %s has %s %v outside 0..1", o.ID, name, v)
		}
	}
	return nil
}

// Weight is how much this observation may contribute at all.
//
// It multiplies rather than averages, so a zero on any factor makes
// the observation weightless. That is the correct behaviour: an
// observation on an incompatible measurement basis contributes
// nothing however reliable it is.
func (o Observation) Weight() float64 {
	w := o.Reliability * o.Independence * o.Freshness
	if !o.TemporalFit || !o.MeasurementCompatible {
		return 0
	}
	return w
}

// Matrix is the contradiction matrix: hypotheses against observations.
type Matrix struct {
	CaseID   string
	TenantID string

	hypotheses   []Hypothesis
	observations []Observation
	cells        map[string]map[string]Consistency

	Versions contract.VersionSet
}

// NewMatrix builds one. It refuses a single hypothesis.
func NewMatrix(tenantID, caseID string, hs []Hypothesis, versions contract.VersionSet) (*Matrix, error) {
	if len(hs) < 2 {
		return nil, fmt.Errorf("%w: %d supplied", ErrSingleHypothesis, len(hs))
	}
	if !versions.Complete() {
		return nil, fmt.Errorf("%w: %v", contract.ErrUnversioned, versions.Missing())
	}
	m := &Matrix{TenantID: tenantID, CaseID: caseID,
		cells: map[string]map[string]Consistency{}, Versions: versions}
	seen := map[string]bool{}
	for _, h := range hs {
		if err := h.Validate(); err != nil {
			return nil, err
		}
		if seen[h.ID] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicate, h.ID)
		}
		seen[h.ID] = true
		m.hypotheses = append(m.hypotheses, h)
		m.cells[h.ID] = map[string]Consistency{}
	}
	return m, nil
}

// AddObservation records evidence.
func (m *Matrix) AddObservation(o Observation) error {
	if err := o.Validate(); err != nil {
		return err
	}
	for _, e := range m.observations {
		if e.ID == o.ID {
			return fmt.Errorf("hypothesis: observation %s already recorded", o.ID)
		}
	}
	m.observations = append(m.observations, o)
	return nil
}

// Set records how an observation bears on a hypothesis.
func (m *Matrix) Set(hypothesisID, observationID string, c Consistency) error {
	if _, ok := m.cells[hypothesisID]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknown, hypothesisID)
	}
	if !c.Valid() {
		return fmt.Errorf("hypothesis: unknown consistency %q", c)
	}
	found := false
	for _, o := range m.observations {
		if o.ID == observationID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("hypothesis: unknown observation %s", observationID)
	}
	m.cells[hypothesisID][observationID] = c
	return nil
}

// Standing is one hypothesis's position after assessment.
type Standing struct {
	Hypothesis Hypothesis
	// Score is the weighted ACH sum. It ranks; it does not decide.
	Score float64
	// Eliminated is true when a strongly inconsistent observation
	// applies. Elimination is not a low score: it is a different
	// statement.
	Eliminated      bool
	EliminatedBy    []string
	Inconsistencies []string
	// Unassessed names the observations nobody scored against this
	// hypothesis. A leading hypothesis with many of these is leading
	// because it was not tested.
	Unassessed []string
	// Missing names the expected observations that are not among the
	// evidence at all.
	Missing []string
}

// Assessment is the whole result.
type Assessment struct {
	Standings []Standing
	// Indistinguishable is true when the top two are within the
	// margin. A ranking that always names a leader reports a 51/49
	// split the same way as a 99/1 one.
	Indistinguishable bool
	Margin            float64
	// Diagnostic names the observations that actually discriminate.
	// An observation every hypothesis predicts equally is worth
	// nothing, however expensive it was.
	Diagnostic []string
	// NonDiagnostic names the rest, so an acquisition plan does not
	// spend on more of them.
	NonDiagnostic []string
}

// IndistinguishableMargin is the score gap AT OR BELOW which two
// hypotheses are reported as not separated.
//
// The comparison is inclusive deliberately. A margin sitting exactly
// on the threshold is the case where the choice of threshold decides
// the answer, and in that case the safe report is "the evidence does
// not separate these" rather than a leader. It is a stated choice, not
// a measured optimum.
const IndistinguishableMargin = 0.5

// Assess scores every hypothesis against the evidence.
func (m *Matrix) Assess() (Assessment, error) {
	if len(m.observations) == 0 {
		return Assessment{}, ErrNoEvidence
	}
	observed := map[string]bool{}
	for _, o := range m.observations {
		observed[o.ID] = true
		for _, r := range o.EvidenceRefs {
			observed[r] = true
		}
		observed[o.Detail] = true
	}

	var out Assessment
	for _, h := range m.hypotheses {
		s := Standing{Hypothesis: h}
		for _, o := range m.observations {
			c, set := m.cells[h.ID][o.ID]
			if !set || !c.Assessed() {
				s.Unassessed = append(s.Unassessed, o.ID)
				continue
			}
			s.Score += c.Score() * o.Weight()
			if c == Inconsistent || c == StronglyInconsistent {
				s.Inconsistencies = append(s.Inconsistencies,
					fmt.Sprintf("%s (%s)", o.ID, c))
			}
			// Elimination requires a strongly inconsistent observation
			// that actually applies. A zero-weight observation cannot
			// eliminate anything -- which is the rule that stops an
			// incompatible measurement from deciding a case.
			if c.Eliminates() && o.Weight() > 0 {
				s.Eliminated = true
				s.EliminatedBy = append(s.EliminatedBy, o.ID)
			}
		}
		for _, e := range h.Expected {
			if !observed[e] {
				s.Missing = append(s.Missing, e)
			}
		}
		sort.Strings(s.Unassessed)
		sort.Strings(s.Missing)
		out.Standings = append(out.Standings, s)
	}

	// Diagnosticity: an observation discriminates when the
	// hypotheses do not all score it the same way.
	for _, o := range m.observations {
		distinct := map[Consistency]bool{}
		for _, h := range m.hypotheses {
			distinct[m.cells[h.ID][o.ID]] = true
		}
		if len(distinct) > 1 {
			out.Diagnostic = append(out.Diagnostic, o.ID)
		} else {
			out.NonDiagnostic = append(out.NonDiagnostic, o.ID)
		}
	}
	sort.Strings(out.Diagnostic)
	sort.Strings(out.NonDiagnostic)

	// Rank: not eliminated first, then by score, then by id for
	// determinism.
	sort.Slice(out.Standings, func(i, j int) bool {
		a, b := out.Standings[i], out.Standings[j]
		if a.Eliminated != b.Eliminated {
			return !a.Eliminated
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Hypothesis.ID < b.Hypothesis.ID
	})

	live := 0
	for _, s := range out.Standings {
		if !s.Eliminated {
			live++
		}
	}
	if live >= 2 {
		out.Margin = out.Standings[0].Score - out.Standings[1].Score
		out.Indistinguishable = out.Margin <= IndistinguishableMargin
	}
	return out, nil
}

// Report renders the matrix and the assessment.
func (a Assessment) Report() string {
	var b strings.Builder
	b.WriteString("COMPETING HYPOTHESES\n")
	for i, s := range a.Standings {
		status := "live"
		if s.Eliminated {
			status = "ELIMINATED by " + strings.Join(s.EliminatedBy, ", ")
		}
		fmt.Fprintf(&b, "  %d. %-6s score %+.2f  %s\n     %s\n",
			i+1, s.Hypothesis.ID, s.Score, status, s.Hypothesis.Statement)
		if len(s.Inconsistencies) > 0 {
			fmt.Fprintf(&b, "     inconsistent with: %s\n", strings.Join(s.Inconsistencies, ", "))
		}
		if len(s.Unassessed) > 0 {
			fmt.Fprintf(&b, "     NOT ASSESSED against: %s\n", strings.Join(s.Unassessed, ", "))
		}
		if len(s.Missing) > 0 {
			fmt.Fprintf(&b, "     expected and not present: %s\n", strings.Join(s.Missing, ", "))
		}
	}
	if a.Indistinguishable {
		fmt.Fprintf(&b, "  THE LEADING TWO ARE NOT SEPARATED by the available evidence "+
			"(margin %.2f). Reporting a leader here would present a near-tie as a conclusion.\n",
			a.Margin)
	}
	if len(a.Diagnostic) > 0 {
		fmt.Fprintf(&b, "  diagnostic observations: %s\n", strings.Join(a.Diagnostic, ", "))
	}
	if len(a.NonDiagnostic) > 0 {
		fmt.Fprintf(&b, "  NON-diagnostic (every hypothesis scores these alike, so more of "+
			"them settles nothing): %s\n", strings.Join(a.NonDiagnostic, ", "))
	}
	return b.String()
}

// Leader returns the top hypothesis, or false when the evidence does
// not separate the leading two.
//
// Returning a bool rather than always a hypothesis is the point: a
// caller must handle "the evidence does not decide" rather than
// receive a leader and assume it won.
func (a Assessment) Leader() (Standing, bool) {
	if len(a.Standings) == 0 || a.Indistinguishable || a.Standings[0].Eliminated {
		return Standing{}, false
	}
	return a.Standings[0], true
}

// Hypotheses returns the set.
func (m *Matrix) Hypotheses() []Hypothesis { return append([]Hypothesis(nil), m.hypotheses...) }

// Observations returns the evidence.
func (m *Matrix) Observations() []Observation { return append([]Observation(nil), m.observations...) }
