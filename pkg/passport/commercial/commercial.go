// Package commercial is the Decision Passport as a product.
//
// # Why this exists separately from pkg/passport
//
// pkg/passport carries the machine-checkable decision record: the
// signed payload, the disproof route, the counts. It is correct and it
// is not what anybody buys.
//
// Nobody purchases an epistemic firewall, an assurance graph, engine
// separation or a mutation suite. What a customer purchases is the
// answer to one question:
//
//	"Show me why you reached this conclusion, what evidence supports
//	 it, how independent those sources are, what you rejected, and
//	 exactly what would prove it wrong."
//
// That is this document. Everything else in this repository is
// machinery for making it truthful.
//
// # The shape is load-bearing
//
// The order of the sections is an argument. Observations come before
// hypotheses, because a reader who is given the conclusion first reads
// the evidence as support for it. Contradictions come before
// hypotheses, not in an appendix, because a passport that buries its
// conflicts is a sales document. The disproof route comes before the
// replay block, because how to attack this matters more than how to
// re-run it.
//
// The last section is EXTERNAL STATUS, and on every passport VERIQO
// can currently produce it reads NOT_EXTERNALLY_QUALIFIED. It is last
// so that it is the thing the reader leaves with.
//
// # What Validate refuses
//
// A passport with no contradictions section, no limitations, no
// disproof route, or no named decision authority. Each of those
// omissions makes the document more persuasive and less honest, which
// is precisely why they have to be structurally impossible rather than
// discouraged.
package commercial

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	// ErrMissingSection is a section the reader needs and cannot see.
	ErrMissingSection = errors.New("commercial: the passport omits a section a challenger needs")
	// ErrVeriqoDecided is a passport whose decision authority is VERIQO.
	ErrVeriqoDecided = errors.New("commercial: VERIQO is named as the decision authority")
	// ErrNoAlternative is a hypothesis set with nothing to compete.
	ErrNoAlternative = errors.New("commercial: one hypothesis is not a hypothesis set")
)

// Case is the header block: what matter this is, over what window,
// under whose law, for what declared purpose.
//
// DeclaredPurpose is not administrative. Evidence lawful for screening
// may be unlawful to found a decision on, so a passport that does not
// state what it was produced FOR cannot be checked against the rights
// under which its sources were acquired.
type Case struct {
	ID              string    `json:"case_id"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	Jurisdiction    string    `json:"jurisdiction"`
	DeclaredPurpose string    `json:"declared_purpose"`
	Tenant          string    `json:"tenant"`
}

// Observation is something that was seen, with who recorded it.
type Observation struct {
	Ref  string    `json:"ref"` // O1, O2...
	What string    `json:"what"`
	At   time.Time `json:"at"`
	// SourceRef ties it to a row in the provenance table. An
	// observation with no source is an assertion in the observation
	// section, which is the most effective place to hide one.
	SourceRef string `json:"source_ref"`
}

// Source is one row of the provenance table.
type Source struct {
	Ref string `json:"ref"` // S1, S2...
	// Producer is who made the information, not who sold it. An
	// aggregator is not a producer, and conflating them is how fifty
	// resellers of one feed become fifty sources.
	Producer    string    `json:"producer"`
	Vendor      string    `json:"vendor,omitempty"`
	Acquisition string    `json:"acquisition"`
	Rights      string    `json:"rights"`
	Timestamp   time.Time `json:"timestamp"`
	// LawfulFor names the uses this material may be put to. Empty
	// means unknown, which is not the same as unrestricted.
	LawfulFor []string `json:"lawful_for,omitempty"`
}

// Independence is the section a serious reader turns to first.
type Independence struct {
	// Accounts is how many separate reports were received.
	Accounts int `json:"accounts"`
	// Producers is how many distinct producers those reduce to.
	Producers int `json:"producers"`
	// EffectiveObservations is the count after copy detection. It is
	// the only one of the three that means anything for corroboration.
	EffectiveObservations int `json:"effective_observations"`
	// Dimensions assessed, and the ones that were not.
	Assessed []string `json:"assessed"`
	Unknown  []string `json:"unknown"`
	// Note carries the sentence that stops the count being read as a
	// finding of independence.
	Note string `json:"note"`
}

// Contradiction is a conflict in the evidence, stated in full.
type Contradiction struct {
	Ref string `json:"ref"` // C1, C2...
	// Between names the observations that conflict.
	Between []string `json:"between"`
	What    string   `json:"what"`
	// Resolved states how it was settled, or says it was not. An
	// unresolved contradiction stays in the passport.
	Resolved string `json:"resolved,omitempty"`
}

// Hypothesis is a competing account of the same evidence.
type Hypothesis struct {
	Ref         string   `json:"ref"` // H1, H2...
	Statement   string   `json:"statement"`
	SupportedBy []string `json:"supported_by,omitempty"`
	// Against is the evidence that tells against it. A hypothesis with
	// nothing against it has usually not been examined.
	Against []string `json:"against,omitempty"`
	// Standing is where it currently sits: LEADING, OPEN, WEAKENED,
	// ELIMINATED. Eliminated hypotheses stay in the document.
	Standing string `json:"standing"`
	// Discriminator names what would separate this from the others.
	Discriminator string `json:"discriminator,omitempty"`
}

// Qualification is the four-way split: what is established, what is
// not, what was refused, and what nobody looked at.
//
// The fourth is the one that is normally missing, and it is the one a
// challenger needs most.
type Qualification struct {
	Verified   []string `json:"verified"`
	Unverified []string `json:"unverified"`
	Refused    []string `json:"refused"`
	Unknown    []string `json:"unknown"`
}

// Decision is the conclusion, its confidence state, and who took it.
type Decision struct {
	Conclusion string `json:"conclusion"`
	// ConfidenceState is a NAMED state, never a number. A percentage
	// here would be multiplied by something downstream.
	ConfidenceState string `json:"confidence_state"`
	// Authority is the principal who took it. It may not be VERIQO.
	Authority     string    `json:"authority"`
	AuthorityRole string    `json:"authority_role"`
	At            time.Time `json:"at"`
}

// Replay is what is needed to re-execute the reasoning.
type Replay struct {
	Manifest  string `json:"manifest"`
	Policy    string `json:"policy"`
	Model     string `json:"model,omitempty"`
	Version   string `json:"version"`
	Hash      string `json:"hash"`
	Signature string `json:"signature"`
}

// Passport is the whole document.
type Passport struct {
	Case           Case            `json:"case"`
	Question       string          `json:"question"`
	Observations   []Observation   `json:"observations"`
	Sources        []Source        `json:"sources"`
	Independence   Independence    `json:"independence"`
	Contradictions []Contradiction `json:"contradictions"`
	Hypotheses     []Hypothesis    `json:"hypotheses"`
	Qualification  Qualification   `json:"qualification"`
	Decision       Decision        `json:"decision"`
	DisproofRoute  []string        `json:"disproof_route"`
	Replay         Replay          `json:"replay"`
	Limitations    []string        `json:"limitations"`
	// ExternalStatus is the last thing the reader sees.
	AssuranceState  string `json:"assurance_state"`
	ExternalStatus  string `json:"external_status"`
	SyntheticNotice string `json:"synthetic_notice,omitempty"`
}

// Validate refuses a passport a challenger could not use.
func (p Passport) Validate() error {
	if strings.TrimSpace(p.Case.ID) == "" {
		return fmt.Errorf("%w: case id", ErrMissingSection)
	}
	if strings.TrimSpace(p.Case.DeclaredPurpose) == "" {
		return fmt.Errorf("%w: declared purpose. Without it, the rights under which "+
			"the sources were acquired cannot be checked against what the passport is "+
			"used for", ErrMissingSection)
	}
	if strings.TrimSpace(p.Case.Jurisdiction) == "" {
		return fmt.Errorf("%w: jurisdiction", ErrMissingSection)
	}
	if p.Case.To.Before(p.Case.From) {
		return errors.New("commercial: the case window ends before it begins")
	}
	if strings.TrimSpace(p.Question) == "" {
		return fmt.Errorf("%w: question. A passport answering an unstated question "+
			"invites the reader to supply their own", ErrMissingSection)
	}
	if len(p.Observations) == 0 {
		return fmt.Errorf("%w: observations", ErrMissingSection)
	}
	if len(p.Sources) == 0 {
		return fmt.Errorf("%w: source provenance", ErrMissingSection)
	}

	known := map[string]bool{}
	for _, s := range p.Sources {
		if strings.TrimSpace(s.Producer) == "" {
			return fmt.Errorf("commercial: source %s names no producer; a vendor is not "+
				"a producer", s.Ref)
		}
		if strings.TrimSpace(s.Rights) == "" {
			return fmt.Errorf("commercial: source %s states no rights", s.Ref)
		}
		if strings.TrimSpace(s.Acquisition) == "" {
			return fmt.Errorf("commercial: source %s does not say how it was acquired", s.Ref)
		}
		known[s.Ref] = true
	}
	for _, o := range p.Observations {
		if strings.TrimSpace(o.SourceRef) == "" || !known[o.SourceRef] {
			return fmt.Errorf("commercial: observation %s cites source %q, which is not "+
				"in the provenance table", o.Ref, o.SourceRef)
		}
	}

	if p.Independence.EffectiveObservations > p.Independence.Accounts {
		return errors.New("commercial: more effective observations than accounts")
	}
	if strings.TrimSpace(p.Independence.Note) == "" {
		return fmt.Errorf("%w: the independence note. A producer count without it reads "+
			"as a finding that the producers are independent", ErrMissingSection)
	}
	if len(p.Independence.Unknown) == 0 && len(p.Independence.Assessed) == 0 {
		return fmt.Errorf("%w: independence dimensions", ErrMissingSection)
	}

	// Contradictions may legitimately be empty -- but the section must
	// be a considered emptiness, which is what Limitations carries.
	if len(p.Hypotheses) < 2 {
		return fmt.Errorf("%w: %d given. A single account of the evidence has not been "+
			"tested against anything", ErrNoAlternative, len(p.Hypotheses))
	}
	leading := 0
	for _, h := range p.Hypotheses {
		if strings.TrimSpace(h.Standing) == "" {
			return fmt.Errorf("commercial: hypothesis %s has no standing", h.Ref)
		}
		if strings.EqualFold(h.Standing, "LEADING") {
			leading++
		}
	}
	if leading > 1 {
		return fmt.Errorf("commercial: %d hypotheses are marked LEADING", leading)
	}

	if len(p.Qualification.Unknown) == 0 {
		return fmt.Errorf("%w: the UNKNOWN column of the qualification section. A case "+
			"with nothing unknown has not been examined", ErrMissingSection)
	}
	if strings.TrimSpace(p.Decision.Conclusion) == "" {
		return fmt.Errorf("%w: conclusion", ErrMissingSection)
	}
	if strings.TrimSpace(p.Decision.ConfidenceState) == "" {
		return fmt.Errorf("%w: confidence state", ErrMissingSection)
	}
	if strings.ContainsAny(p.Decision.ConfidenceState, "0123456789%") {
		return fmt.Errorf("commercial: the confidence state %q carries a number; a "+
			"figure here is multiplied by something downstream and becomes a score",
			p.Decision.ConfidenceState)
	}
	auth := strings.ToUpper(p.Decision.Authority)
	if strings.TrimSpace(auth) == "" {
		return fmt.Errorf("%w: decision authority", ErrMissingSection)
	}
	if strings.Contains(auth, "VERIQO") || strings.HasPrefix(auth, "AGENT:") ||
		strings.HasPrefix(auth, "SERVICE:") {
		return fmt.Errorf("%w: %q. VERIQO assembles a decision and does not take one",
			ErrVeriqoDecided, p.Decision.Authority)
	}
	if len(p.DisproofRoute) == 0 {
		return fmt.Errorf("%w: disproof route. A conclusion with no route to overturning "+
			"it is unfalsifiable, and an unfalsifiable conclusion is not an "+
			"intelligence product", ErrMissingSection)
	}
	if strings.TrimSpace(p.Replay.Manifest) == "" || strings.TrimSpace(p.Replay.Hash) == "" {
		return fmt.Errorf("%w: replay manifest and hash", ErrMissingSection)
	}
	if len(p.Limitations) == 0 {
		return fmt.Errorf("%w: limitations. A passport that states none is claiming to "+
			"have settled everything it touched", ErrMissingSection)
	}
	if strings.TrimSpace(p.ExternalStatus) == "" {
		return fmt.Errorf("%w: external status", ErrMissingSection)
	}
	return nil
}

const rule = "──────────────────────────────────────────────────────────────────────────"

// Render produces the document.
func (p Passport) Render() string {
	var b strings.Builder
	b.WriteString("VERIQO DECISION PASSPORT\n")
	b.WriteString(rule + "\n\n")
	if p.SyntheticNotice != "" {
		b.WriteString(wrap("  ", strings.ToUpper(p.SyntheticNotice)))
		b.WriteString("\n")
	}

	sec(&b, "CASE")
	fmt.Fprintf(&b, "  CASE ID           %s\n", p.Case.ID)
	fmt.Fprintf(&b, "  TIME WINDOW       %s to %s\n",
		p.Case.From.UTC().Format("2006-01-02 15:04Z"), p.Case.To.UTC().Format("2006-01-02 15:04Z"))
	b.WriteString(wrap("  JURISDICTION      ", p.Case.Jurisdiction))
	b.WriteString(wrap("  DECLARED PURPOSE  ", p.Case.DeclaredPurpose))
	if p.Case.Tenant != "" {
		fmt.Fprintf(&b, "  TENANT            %s\n", p.Case.Tenant)
	}

	sec(&b, "QUESTION")
	b.WriteString(wrap("  ", p.Question))

	sec(&b, "OBSERVATIONS")
	for _, o := range p.Observations {
		fmt.Fprintf(&b, "  %-4s %s\n", o.Ref, o.At.UTC().Format("2006-01-02 15:04Z"))
		b.WriteString(wrap("       ", o.What))
		fmt.Fprintf(&b, "       source: %s\n", o.SourceRef)
	}

	sec(&b, "SOURCE PROVENANCE")
	for _, s := range p.Sources {
		fmt.Fprintf(&b, "  %-4s %s\n", s.Ref, s.Producer)
		if s.Vendor != "" {
			fmt.Fprintf(&b, "       vendor:      %s (a vendor is not a producer)\n", s.Vendor)
		}
		b.WriteString(wrap("       acquisition: ", s.Acquisition))
		b.WriteString(wrap("       rights:      ", s.Rights))
		fmt.Fprintf(&b, "       timestamp:   %s\n", s.Timestamp.UTC().Format(time.RFC3339))
		if len(s.LawfulFor) > 0 {
			b.WriteString(wrap("       lawful for:  ", strings.Join(s.LawfulFor, ", ")))
		} else {
			b.WriteString("       lawful for:  UNKNOWN -- which is not the same as unrestricted\n")
		}
	}

	sec(&b, "INDEPENDENCE")
	fmt.Fprintf(&b, "  accounts received        %d\n", p.Independence.Accounts)
	fmt.Fprintf(&b, "  distinct producers       %d\n", p.Independence.Producers)
	fmt.Fprintf(&b, "  effective observations   %d\n", p.Independence.EffectiveObservations)
	if len(p.Independence.Assessed) > 0 {
		b.WriteString(wrap("  dimensions assessed      ",
			strings.Join(p.Independence.Assessed, ", ")))
	}
	if len(p.Independence.Unknown) > 0 {
		b.WriteString(wrap("  dimensions UNKNOWN       ",
			strings.Join(p.Independence.Unknown, ", ")))
	}
	b.WriteString("\n")
	b.WriteString(wrap("  ", p.Independence.Note))

	sec(&b, "CONTRADICTIONS")
	if len(p.Contradictions) == 0 {
		b.WriteString("  None found. That is a statement about what was compared, not\n")
		b.WriteString("  about the world; see LIMITATIONS.\n")
	}
	for _, c := range p.Contradictions {
		fmt.Fprintf(&b, "  %-4s between %s\n", c.Ref, strings.Join(c.Between, " and "))
		b.WriteString(wrap("       ", c.What))
		if c.Resolved != "" {
			b.WriteString(wrap("       resolved: ", c.Resolved))
		} else {
			b.WriteString("       UNRESOLVED. It stays in the passport.\n")
		}
	}

	sec(&b, "HYPOTHESES")
	for _, h := range p.Hypotheses {
		fmt.Fprintf(&b, "  %-4s [%s]\n", h.Ref, h.Standing)
		b.WriteString(wrap("       ", h.Statement))
		if len(h.SupportedBy) > 0 {
			b.WriteString(wrap("       for:     ", strings.Join(h.SupportedBy, ", ")))
		}
		if len(h.Against) > 0 {
			b.WriteString(wrap("       against: ", strings.Join(h.Against, ", ")))
		}
		if h.Discriminator != "" {
			b.WriteString(wrap("       would separate it: ", h.Discriminator))
		}
	}

	sec(&b, "QUALIFICATION")
	col(&b, "VERIFIED", p.Qualification.Verified)
	col(&b, "UNVERIFIED", p.Qualification.Unverified)
	col(&b, "REFUSED", p.Qualification.Refused)
	col(&b, "UNKNOWN", p.Qualification.Unknown)

	sec(&b, "DECISION")
	b.WriteString(wrap("  conclusion:       ", p.Decision.Conclusion))
	fmt.Fprintf(&b, "  confidence state: %s\n", p.Decision.ConfidenceState)
	fmt.Fprintf(&b, "  authority:        %s", p.Decision.Authority)
	if p.Decision.AuthorityRole != "" {
		fmt.Fprintf(&b, " (%s)", p.Decision.AuthorityRole)
	}
	b.WriteString("\n")
	if !p.Decision.At.IsZero() {
		fmt.Fprintf(&b, "  taken at:         %s\n", p.Decision.At.UTC().Format(time.RFC3339))
	}
	b.WriteString("\n  VERIQO assembled this decision. It did not take it.\n")

	sec(&b, "DISPROOF ROUTE")
	b.WriteString("  What would show this conclusion to be wrong:\n\n")
	for i, d := range p.DisproofRoute {
		b.WriteString(wrap(fmt.Sprintf("  %d. ", i+1), d))
	}

	sec(&b, "REPLAY")
	fmt.Fprintf(&b, "  manifest   %s\n", p.Replay.Manifest)
	fmt.Fprintf(&b, "  policy     %s\n", p.Replay.Policy)
	if p.Replay.Model != "" {
		b.WriteString(wrap("  model      ", p.Replay.Model))
	}
	fmt.Fprintf(&b, "  version    %s\n", p.Replay.Version)
	fmt.Fprintf(&b, "  hash       %s\n", p.Replay.Hash)
	b.WriteString(wrap("  signature  ", p.Replay.Signature))

	sec(&b, "LIMITATIONS")
	for _, l := range p.Limitations {
		b.WriteString(wrap("  - ", l))
	}

	sec(&b, "EXTERNAL STATUS")
	fmt.Fprintf(&b, "  %s\n", p.AssuranceState)
	fmt.Fprintf(&b, "  %s\n\n", p.ExternalStatus)
	b.WriteString(wrap("  ", "No party outside VERIQO has examined the reasoning in this "+
		"passport, the system that produced it, or the evidence it rests on. Everything "+
		"above is VERIQO's account of its own work."))
	return b.String()
}

func sec(b *strings.Builder, name string) {
	b.WriteString("\n" + rule + "\n\n")
	b.WriteString(name + "\n\n")
}

func col(b *strings.Builder, name string, xs []string) {
	fmt.Fprintf(b, "  %s\n", name)
	if len(xs) == 0 {
		b.WriteString("    (none)\n")
	}
	sorted := append([]string(nil), xs...)
	sort.Strings(sorted)
	for _, x := range sorted {
		b.WriteString(wrap("    - ", x))
	}
	b.WriteString("\n")
}

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
