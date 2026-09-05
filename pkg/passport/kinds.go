package passport

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A passport is the product, not an output format.
//
// # What a customer is actually buying
//
// Nobody buys an answer. An answer from a system is worth what the
// system's reputation is worth, and in a dispute that is nothing: the
// other side's expert has an answer too. What a customer buys is a
// DEFENSIBLE DECISION PACKAGE -- something that survives being
// attacked by somebody paid to attack it.
//
// That changes what a passport must contain. An answer needs to be
// right. A defensible package needs to state what it rests on, what it
// does not cover, what would overturn it, who approved it, and what
// nobody has checked -- and it needs to do so in the artefact itself,
// because by the time it is challenged the people who made it are not
// in the room.
//
// # Why kinds rather than one shape
//
// The six kinds below are not templates. They differ in what they are
// forbidden to say, and each forbidden statement is one a customer in
// that domain will ask for and must not be given:
//
//	an insurer wants "covered"          -- coverage is the insurer's decision
//	a claimant wants "fraud"            -- fraud is a finding of a tribunal
//	a trader wants "the counterparty
//	  defaulted"                        -- default is a contractual event
//
// A system that says those things is not neutral, and a neutral system
// that says them once is not neutral any more.
type Kind string

const (
	// ClaimQualification: what an insurance claim's evidence supports.
	ClaimQualification Kind = "CLAIM_QUALIFICATION"
	// IncidentEvidence: what is established about a maritime incident.
	IncidentEvidence Kind = "INCIDENT_EVIDENCE"
	// QuantityDiscrepancy: a difference between two measurements, with
	// the basis question answered.
	QuantityDiscrepancy Kind = "QUANTITY_DISCREPANCY"
	// CollateralEvidence: what is established about goods or documents
	// offered as security.
	CollateralEvidence Kind = "COLLATERAL_EVIDENCE"
	// DisputeEvidence: the evidential position in a contested matter.
	DisputeEvidence Kind = "DISPUTE_EVIDENCE"
	// CounterpartyQualification: what is established about a
	// counterparty, for a compliance decision somebody else makes.
	CounterpartyQualification Kind = "COUNTERPARTY_QUALIFICATION"
)

func Kinds() []Kind {
	return []Kind{ClaimQualification, IncidentEvidence, QuantityDiscrepancy,
		CollateralEvidence, DisputeEvidence, CounterpartyQualification}
}

var ErrForbiddenStatement = errors.New("passport: this kind may not make this statement")

// Profile is what a kind requires and what it refuses.
type Profile struct {
	// Question is what the passport answers, phrased so that the
	// answer is about evidence rather than about the world.
	Question string
	// Decides names who makes the decision this passport informs. It
	// is never VERIQO, and stating it in the artefact is what stops a
	// reader treating the passport as the decision.
	Decides string
	// RequiredSections must be present and non-empty.
	RequiredSections []string
	// ForbiddenStatements are the conclusions this kind may not reach,
	// as lowercase phrases checked against the statement text.
	ForbiddenStatements []string
	// WhyForbidden explains each refusal, because a refusal a
	// salesperson cannot explain is one they will promise around.
	WhyForbidden string
}

// ProfileOf returns a kind's profile.
func ProfileOf(k Kind) (Profile, error) {
	switch k {
	case ClaimQualification:
		return Profile{
			Question: "what does the evidence establish about this claim, and what would " +
				"change it?",
			Decides:          "the insurer's claims handler, under the policy",
			RequiredSections: []string{"policy reference", "loss description", "quantum basis"},
			ForbiddenStatements: []string{"is covered", "is not covered", "policy responds",
				"policy does not respond", "is fraudulent", "indemnity is due"},
			WhyForbidden: "coverage is a construction of the policy wording and a decision " +
				"reserved to the insurer. A passport that says 'covered' has made the " +
				"insurer's decision for them, and one that says 'fraudulent' has made a " +
				"tribunal's",
		}, nil

	case IncidentEvidence:
		return Profile{
			Question: "what is established about what happened, and what is not?",
			Decides:  "the investigating authority, the P&I club, or the tribunal",
			RequiredSections: []string{"incident window", "vessel identification basis",
				"position evidence"},
			ForbiddenStatements: []string{"was at fault", "caused the", "was negligent",
				"deliberately", "was responsible for"},
			WhyForbidden: "causation and fault are findings, and the observations here are " +
				"consistent with several accounts. An incident passport that assigns fault " +
				"has skipped the step where somebody hears the other side",
		}, nil

	case QuantityDiscrepancy:
		return Profile{
			Question: "how large is the difference, on what basis, and what could explain " +
				"it other than loss?",
			Decides: "the parties to the contract, and failing that the tribunal",
			RequiredSections: []string{"measurement basis", "tolerance clause",
				"alternative construction"},
			ForbiddenStatements: []string{"was stolen", "was misappropriated",
				"short-delivered deliberately", "the seller is liable"},
			WhyForbidden: "a quantity difference has many causes -- measurement basis, " +
				"temperature, ballast, tolerance construction -- and theft is one of the " +
				"least common. Naming it in the artefact converts an arithmetic result " +
				"into an allegation",
		}, nil

	case CollateralEvidence:
		return Profile{
			Question: "what is established about the existence, condition and title of the " +
				"collateral?",
			Decides:          "the financing bank's credit committee",
			RequiredSections: []string{"document set", "title basis", "inspection evidence"},
			ForbiddenStatements: []string{"is good security", "the facility should",
				"is worth", "title is good", "is unencumbered"},
			WhyForbidden: "sufficiency of security is a credit decision and title is a legal " +
				"one. A passport can establish what documents exist and what an inspector " +
				"saw; it cannot establish that nobody else has a claim over the same goods, " +
				"which is the question that actually matters",
		}, nil

	case DisputeEvidence:
		return Profile{
			Question: "what does each side's evidence establish, and where do they conflict?",
			Decides:  "the tribunal, or the parties in settlement",
			RequiredSections: []string{"contested issues", "each party's evidence",
				"contradictions"},
			ForbiddenStatements: []string{"the claimant will succeed", "the defence fails",
				"is likely to win", "the correct outcome"},
			WhyForbidden: "predicting an outcome is advocacy, and a neutral artefact that " +
				"predicts one has taken a side. The value here is that both parties can use " +
				"the same passport, which stops being true the moment it favours one",
		}, nil

	case CounterpartyQualification:
		return Profile{
			Question: "what is established about this counterparty, and how much of it is " +
				"an allegation?",
			Decides: "the firm's compliance officer, under its own risk appetite",
			RequiredSections: []string{"identification basis", "screening scope",
				"source classes relied on"},
			ForbiddenStatements: []string{"is sanctioned", "is a criminal", "is high risk",
				"should be offboarded", "is engaged in"},
			WhyForbidden: "a name match against a list is a screen, not an identification, " +
				"and adverse media records that something was reported rather than that it " +
				"occurred. 'Is sanctioned' asserts an identity the screening did not " +
				"establish, and 'high risk' is the firm's own rating under its own appetite",
		}, nil
	}
	return Profile{}, fmt.Errorf("passport: unknown kind %q", k)
}

// disclaimers are phrases that turn a forbidden conclusion into an
// explicit refusal to reach it.
//
// "whether the policy responds is not addressed here" contains the
// forbidden phrase and is the OPPOSITE of the thing being guarded
// against. A screen that flagged it would train authors to remove
// their disclaimers, which is the precise inversion of what this
// check exists to achieve.
var disclaimers = []string{
	"whether", "is not addressed", "does not address", "not established",
	"no view is expressed", "is not a finding", "cannot be established",
	"is not concluded", "makes no finding", "is for", "is reserved to",
}

// copulas are normalised so that inflection does not evade the screen.
// "are good security" and "is good security" are the same statement to
// a reader and were, until this normalisation, different strings.
var copulaReplacer = strings.NewReplacer(
	" are ", " is ", " were ", " is ", " was ", " is ", " be ", " is ",
	" have been ", " is ", " has been ", " is ",
)

// CheckKind screens a passport for conclusions its kind may not reach.
//
// # What this is, and what it is not
//
// It is a SCREEN over the statement text, and it is defeatable. An
// author determined to convey "covered" without writing the word can
// do so, and no phrase list will catch them. The real control is that
// a passport is minted by one person and approved by another, and this
// check exists to make the accidental case impossible rather than the
// deliberate one.
//
// It is on the TEXT rather than on the structure deliberately. A
// structural check would be cleaner and would miss the case that
// matters: a sentence that is technically a description of evidence
// and reads, to the person it is handed to, as a conclusion.
//
// Two refinements the naive version got wrong, both discovered by its
// own tests. Copulas are normalised, so "are good security" does not
// slip past a rule written as "is good security". And an occurrence
// inside a disclaiming clause is not a hit: flagging "whether the
// policy responds is not addressed here" would teach authors to delete
// their disclaimers.
func CheckKind(k Kind, statement, scope string, sections map[string]string) error {
	p, err := ProfileOf(k)
	if err != nil {
		return err
	}
	lower := normalise(statement + " " + scope)
	var hit []string
	for _, f := range p.ForbiddenStatements {
		if occursUndisclaimed(lower, normalise(f)) {
			hit = append(hit, f)
		}
	}
	if len(hit) > 0 {
		sort.Strings(hit)
		return fmt.Errorf("%w: a %s passport contains %q. %s. The decision belongs to %s",
			ErrForbiddenStatement, k, strings.Join(hit, ", "), p.WhyForbidden, p.Decides)
	}
	var missing []string
	for _, s := range p.RequiredSections {
		if strings.TrimSpace(sections[s]) == "" {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("passport: a %s passport must state %s; a reader supplies a "+
			"favourable assumption for anything a document leaves out",
			k, strings.Join(missing, ", "))
	}
	return nil
}

func normalise(s string) string {
	return copulaReplacer.Replace(" " + strings.ToLower(strings.Join(strings.Fields(s), " ")) + " ")
}

// occursUndisclaimed reports whether needle appears in hay other than
// inside a disclaiming clause.
//
// The window is the clause the phrase sits in, bounded by sentence and
// clause punctuation, so a disclaimer in one sentence does not license
// a conclusion in the next.
func occursUndisclaimed(hay, needle string) bool {
	from := 0
	for {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		if !disclaimed(clauseAround(hay, i, i+len(needle))) {
			return true
		}
		from = i + len(needle)
	}
}

// clauseAround returns the text of the clause containing [start,end).
func clauseAround(s string, start, end int) string {
	lo := strings.LastIndexAny(s[:start], ".;")
	if lo < 0 {
		lo = 0
	}
	hi := strings.IndexAny(s[end:], ".;")
	if hi < 0 {
		hi = len(s)
	} else {
		hi += end
	}
	return s[lo:hi]
}

func disclaimed(clause string) bool {
	for _, d := range disclaimers {
		if strings.Contains(clause, d) {
			return true
		}
	}
	return false
}

// DescribeKind renders a kind for a reader deciding whether it is what
// they need.
func DescribeKind(k Kind) string {
	p, err := ProfileOf(k)
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", k)
	fmt.Fprintf(&b, "  answers:   %s\n", p.Question)
	fmt.Fprintf(&b, "  decided by: %s -- not by VERIQO\n", p.Decides)
	fmt.Fprintf(&b, "  must state: %s\n", strings.Join(p.RequiredSections, ", "))
	fmt.Fprintf(&b, "  may not say: %s\n", strings.Join(p.ForbiddenStatements, "; "))
	b.WriteString("  (that list is a screen over the text, not a proof: it makes the " +
		"accidental\n   case impossible, and a determined author can convey the same " +
		"thing without\n   the words. The real control is that one person mints and " +
		"another approves)\n")
	fmt.Fprintf(&b, "  why:       %s\n", p.WhyForbidden)
	return b.String()
}
