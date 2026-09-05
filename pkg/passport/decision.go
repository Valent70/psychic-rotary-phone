package passport

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The Decision Passport as a surface.
//
// # What it is for
//
// A dashboard says "here is the answer". This says: here is the
// answer, the evidence, the contradictions, the provenance, the
// uncertainty, and exactly what would overturn it.
//
// The difference matters at the moment the answer is challenged --
// which, in a dispute, an insurance claim or a compliance decision, is
// the only moment that counts. A dashboard's answer has to be defended
// by whoever is holding it, from memory. A passport defends itself,
// because everything the challenger would ask for is already in it,
// including the parts that weaken it.
//
// # The rule that makes it credible
//
// The unflattering numbers come FIRST. Contradictions and unresolved
// questions are rendered above the hypotheses, and the qualification
// and external-validation lines are the last thing a reader sees.
// Ordering is not presentation here: a document that leads with its
// conclusion and buries its contradictions is making an argument, and
// this is not supposed to be an argument.
type Counts struct {
	// Observations is how many raw observations were considered.
	Observations int `json:"observations"`
	// Producers is how many distinct parties produced them.
	Producers int `json:"producers"`
	// IndependentProducers is how many of those are independent of one
	// another. It is normally smaller than Producers, and the gap is
	// the most informative number on the page.
	IndependentProducers int `json:"independent_producers"`
	// Contradictions is how many conflicts were found.
	Contradictions int `json:"contradictions"`
	// Unresolved is how many questions the case could not settle.
	Unresolved int `json:"unresolved"`
	// Unassessable is how many sources could not be placed in the
	// producer structure at all. Distinct from Unresolved: nobody can
	// resolve these without new information about the sources
	// themselves.
	Unassessable int `json:"unassessable"`
}

// Hypothesis is one competing account, with its weight.
type Hypothesis struct {
	ID        string  `json:"id"`
	Statement string  `json:"statement"`
	Weight    float64 `json:"weight"`
	// Eliminated marks an account the evidence rules out, which is
	// kept rather than dropped: knowing what was considered and
	// excluded is most of what makes a conclusion defensible.
	Eliminated bool   `json:"eliminated,omitempty"`
	Because    string `json:"because,omitempty"`
}

// Decision is the flagship passport surface.
type Decision struct {
	// Case identifies the matter.
	Case string `json:"case"`
	// Subject is what it is about, in the reader's terms.
	Subject string `json:"subject"`
	// Question is what was asked. A passport answering an unstated
	// question invites the reader to supply their own.
	Question string `json:"question"`

	Counts Counts `json:"counts"`

	// EvidenceQuality is the weakest dimension across the evidence,
	// named rather than scored.
	EvidenceQuality string `json:"evidence_quality"`
	// Timeline is the sequence the case rests on.
	Timeline []string `json:"timeline,omitempty"`
	// Hypotheses are the competing accounts.
	Hypotheses []Hypothesis `json:"hypotheses"`
	// Basis is the reasoning, stated so it can be attacked.
	Basis string `json:"basis"`
	// Contradictions are the conflicts found, stated in full.
	Contradictions []string `json:"contradictions,omitempty"`
	// Unresolved are the questions the case could not settle.
	Unresolved []string `json:"unresolved,omitempty"`
	// Limitations are what the finding does not establish.
	Limitations []string `json:"limitations"`

	// Route is how to overturn it.
	Route Route `json:"disproof_route"`

	// ReplayVerifiable states whether the reasoning can be re-executed.
	ReplayVerifiable bool `json:"replay_verifiable"`
	// Qualification is the assurance state of the finding.
	Qualification string `json:"qualification"`
	// ExternallyValidated states whether anybody outside examined it.
	ExternallyValidated bool   `json:"externally_validated"`
	ValidatedBy         string `json:"validated_by,omitempty"`
}

var ErrIncompletePassport = errors.New("passport: the decision passport omits a required part")

// Validate refuses a passport a challenger could not use.
func (d Decision) Validate() error {
	for name, v := range map[string]string{
		"case": d.Case, "subject": d.Subject, "question": d.Question, "basis": d.Basis,
		"evidence quality": d.EvidenceQuality,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%w: no %s", ErrIncompletePassport, name)
		}
	}
	if len(d.Hypotheses) == 0 {
		return fmt.Errorf("%w: no competing hypotheses. A conclusion with no alternatives "+
			"considered is an assertion", ErrIncompletePassport)
	}
	if len(d.Limitations) == 0 {
		return fmt.Errorf("%w: no limitations. A finding that establishes everything it "+
			"touches has not been examined", ErrIncompletePassport)
	}
	if err := d.Route.Validate(); err != nil {
		return err
	}
	if d.Counts.IndependentProducers > d.Counts.Producers {
		return fmt.Errorf("passport: %d independent producers among %d producers",
			d.Counts.IndependentProducers, d.Counts.Producers)
	}
	if d.Counts.Producers > d.Counts.Observations {
		return fmt.Errorf("passport: %d producers for %d observations",
			d.Counts.Producers, d.Counts.Observations)
	}
	if d.ExternallyValidated && strings.TrimSpace(d.ValidatedBy) == "" {
		return fmt.Errorf("%w: claims external validation and names no party",
			ErrIncompletePassport)
	}
	// A contradiction count that disagrees with the list is the kind
	// of drift that turns a summary into a misrepresentation.
	if d.Counts.Contradictions != len(d.Contradictions) {
		return fmt.Errorf("passport: the header says %d contradictions and %d are listed",
			d.Counts.Contradictions, len(d.Contradictions))
	}
	if d.Counts.Unresolved != len(d.Unresolved) {
		return fmt.Errorf("passport: the header says %d unresolved and %d are listed",
			d.Counts.Unresolved, len(d.Unresolved))
	}
	return nil
}

// Leading returns the hypothesis with the greatest weight, and whether
// it is meaningfully ahead of the next.
//
// The second return value is what stops a reader treating 0.36 against
// 0.34 as a conclusion.
func (d Decision) Leading() (Hypothesis, bool) {
	var live []Hypothesis
	for _, h := range d.Hypotheses {
		if !h.Eliminated {
			live = append(live, h)
		}
	}
	if len(live) == 0 {
		return Hypothesis{}, false
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Weight > live[j].Weight })
	if len(live) == 1 {
		return live[0], true
	}
	// The same margin the hypothesis package uses, and the same
	// conservative comparison: a margin exactly at the threshold is
	// reported as undecided.
	decided := live[0].Weight-live[1].Weight > 0.5*live[1].Weight
	return live[0], decided
}

// Render writes the passport.
func (d Decision) Render() string {
	var b strings.Builder
	b.WriteString("DECISION PASSPORT\n")
	b.WriteString(strings.Repeat("=", 66) + "\n\n")
	fmt.Fprintf(&b, "CASE       %s\n", d.Case)
	fmt.Fprintf(&b, "SUBJECT    %s\n", d.Subject)
	fmt.Fprintf(&b, "QUESTION   %s\n\n", d.Question)

	// The unflattering numbers first. A document that leads with its
	// conclusion and buries its contradictions is making an argument.
	c := d.Counts
	fmt.Fprintf(&b, "OBSERVATIONS           %d\n", c.Observations)
	fmt.Fprintf(&b, "PRODUCERS              %d\n", c.Producers)
	fmt.Fprintf(&b, "INDEPENDENT PRODUCERS  %d", c.IndependentProducers)
	if c.IndependentProducers < c.Producers {
		fmt.Fprintf(&b, "   <- %d producer(s) share an origin; republication is not "+
			"corroboration", c.Producers-c.IndependentProducers)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "CONTRADICTIONS         %d\n", c.Contradictions)
	fmt.Fprintf(&b, "UNRESOLVED             %d\n", c.Unresolved)
	if c.Unassessable > 0 {
		fmt.Fprintf(&b, "UNASSESSABLE           %d   <- these cannot be resolved without "+
			"new information about the sources themselves\n", c.Unassessable)
	}
	fmt.Fprintf(&b, "\nEVIDENCE QUALITY       weakest dimension: %s\n", d.EvidenceQuality)

	if len(d.Timeline) > 0 {
		b.WriteString("\nTIMELINE\n")
		for _, t := range d.Timeline {
			fmt.Fprintf(&b, "  %s\n", t)
		}
	}

	if len(d.Contradictions) > 0 {
		b.WriteString("\nCONTRADICTIONS\n")
		for _, x := range d.Contradictions {
			fmt.Fprintf(&b, "  %s\n", x)
		}
	}
	if len(d.Unresolved) > 0 {
		b.WriteString("\nUNRESOLVED\n")
		for _, x := range d.Unresolved {
			fmt.Fprintf(&b, "  %s\n", x)
		}
	}

	b.WriteString("\nHYPOTHESES\n")
	hs := append([]Hypothesis(nil), d.Hypotheses...)
	sort.Slice(hs, func(i, j int) bool { return hs[i].Weight > hs[j].Weight })
	for _, h := range hs {
		mark := " "
		if h.Eliminated {
			mark = "x"
		}
		fmt.Fprintf(&b, "  %s %-4s %.2f  %s\n", mark, h.ID, h.Weight, h.Statement)
		if h.Because != "" {
			fmt.Fprintf(&b, "         %s\n", h.Because)
		}
	}
	if lead, decided := d.Leading(); lead.ID != "" && !decided {
		fmt.Fprintf(&b, "\n  NO HYPOTHESIS IS MEANINGFULLY AHEAD. %s leads and the margin\n"+
			"  over the next is not large enough to decide between them.\n", lead.ID)
	}

	fmt.Fprintf(&b, "\nDECISION BASIS\n  %s\n", d.Basis)

	b.WriteString("\nLIMITATIONS\n")
	for _, l := range d.Limitations {
		fmt.Fprintf(&b, "  %s\n", l)
	}

	b.WriteString("\n" + d.Route.Render())

	b.WriteString("\n" + strings.Repeat("-", 66) + "\n")
	replay := "NOT VERIFIABLE"
	if d.ReplayVerifiable {
		replay = "VERIFIABLE -- the reasoning can be re-executed from the evidence"
	}
	fmt.Fprintf(&b, "REPLAY               %s\n", replay)
	fmt.Fprintf(&b, "QUALIFICATION        %s\n", d.Qualification)
	if d.ExternallyValidated {
		fmt.Fprintf(&b, "EXTERNAL VALIDATION  %s\n", d.ValidatedBy)
	} else {
		b.WriteString("EXTERNAL VALIDATION  NOT PERFORMED. No party outside the issuer has\n")
		b.WriteString("                     examined this finding, its evidence or its\n")
		b.WriteString("                     reasoning.\n")
	}
	return b.String()
}
