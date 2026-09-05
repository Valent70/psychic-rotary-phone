package readiness

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The procurement graph.
//
// # Why PENDING_EXTERNAL is not enough
//
// A status that names a party is a large improvement on a percentage
// and it is still not actionable. "PENDING_EXTERNAL" tells a reader
// that somebody outside is needed. It does not tell them WHICH KIND of
// outsider, what that outsider must produce, what has to be true
// before the engagement can start, how long it takes, what it costs,
// or what else is waiting on the same thing.
//
// Those six answers are the difference between an assurance register
// and a plan. Without them, the register is a list of things that are
// not done -- which is honest, and which a commercial team cannot act
// on. With them, the same register is a procurement schedule, and the
// critical path falls out of it.
//
// # The property that matters commercially
//
// Once each blocker names a validator type and a dependency, the
// register can answer the question a board actually asks: what is the
// longest chain, and what can be started today? That is not an
// assurance question. It is the reason the assurance work has
// commercial value rather than being a cost centre.
type ValidatorType string

const (
	// SecurityAssessor: an accredited penetration testing firm.
	SecurityAssessor ValidatorType = "SECURITY_ASSESSOR"
	// Cryptographer: a reviewer of key derivation, canonical form and
	// signature construction. A different specialist from a pentester,
	// and conflating them is how a cryptographic review becomes a
	// network scan.
	Cryptographer ValidatorType = "CRYPTOGRAPHER"
	// RedTeam: adversarial testing with AI-agent experience.
	RedTeam ValidatorType = "RED_TEAM"
	// Counsel: legal opinion, scoped to a jurisdiction.
	Counsel ValidatorType = "COUNSEL"
	// DataPartner: a commercial provider of licensed data.
	DataPartner ValidatorType = "DATA_PARTNER"
	// CorpusPartner: a customer or industry body supplying real
	// documents.
	CorpusPartner ValidatorType = "CORPUS_PARTNER"
	// InfrastructureProvider: HSM, KMS, signing authority, hosting.
	InfrastructureProvider ValidatorType = "INFRASTRUCTURE_PROVIDER"
	// TimestampAuthority: a party willing to countersign a checkpoint.
	TimestampAuthority ValidatorType = "TIMESTAMP_AUTHORITY"
	// EvaluationPartner: a party supplying an evaluation set VERIQO
	// did not construct.
	EvaluationPartner ValidatorType = "EVALUATION_PARTNER"
	// ReleaseAuthority: the internal named authority. The one
	// validator type that is not external, kept in the same
	// vocabulary so the final step is on the same graph.
	ReleaseAuthorityType ValidatorType = "RELEASE_AUTHORITY"
)

// ValidatorTypes returns every type in a fixed order.
func ValidatorTypes() []ValidatorType {
	return []ValidatorType{SecurityAssessor, Cryptographer, RedTeam, Counsel,
		DataPartner, CorpusPartner, InfrastructureProvider, TimestampAuthority,
		EvaluationPartner, ReleaseAuthorityType}
}

func (v ValidatorType) Valid() bool {
	for _, k := range ValidatorTypes() {
		if k == v {
			return true
		}
	}
	return false
}

// External reports whether this validator is outside the organisation.
func (v ValidatorType) External() bool { return v != ReleaseAuthorityType }

// CostClass is a coarse commercial band.
//
// It is deliberately coarse and deliberately not a currency figure. A
// number would be wrong, would be quoted, and would be out of date;
// a band is enough to sequence the work and cannot be mistaken for a
// quotation.
type CostClass string

const (
	CostLow      CostClass = "LOW"      // weeks of one person, or a small fee
	CostMedium   CostClass = "MEDIUM"   // a scoped engagement
	CostHigh     CostClass = "HIGH"     // a substantial engagement or a contract
	CostUnknown  CostClass = "UNKNOWN"  // genuinely not known
	CostInternal CostClass = "INTERNAL" // no external spend
)

func (c CostClass) Valid() bool {
	switch c {
	case CostLow, CostMedium, CostHigh, CostUnknown, CostInternal:
		return true
	}
	return false
}

var (
	ErrNoOwner    = errors.New("readiness: a blocker has no owner")
	ErrNoEvidence = errors.New("readiness: a blocker does not say what evidence would clear it")
	ErrBadDep     = errors.New("readiness: a blocker depends on something that does not exist")
	ErrDepCycle   = errors.New("readiness: the procurement graph contains a cycle")
)

// Blocker is one thing standing between a dimension and settlement,
// stated so that somebody can go and buy it.
type Blocker struct {
	ID string `json:"id"`
	// Dimension it blocks.
	Dimension Dimension `json:"dimension"`
	// Owner is who inside is accountable for closing it. Not who does
	// the work -- who answers for it not being done.
	Owner string `json:"owner"`
	// Validator is the kind of outsider required.
	Validator ValidatorType `json:"validator_type"`
	// ValidatorQualification narrows the type where it matters --
	// "CREST-equivalent", "admitted in Singapore". An unqualified
	// "security assessor" is a category, not a supplier.
	ValidatorQualification string `json:"validator_qualification,omitempty"`
	// ExpectedEvidence is what the engagement must produce. Stating it
	// in advance is what stops a report arriving that does not answer
	// the question.
	ExpectedEvidence string `json:"expected_evidence"`
	// DependsOn names blockers that must clear first. A pentest before
	// a release-candidate freeze assesses code that will change.
	DependsOn []string `json:"depends_on,omitempty"`
	// LeadTime is the coarse duration once engaged.
	LeadTime string `json:"lead_time"`
	// Cost is the commercial band.
	Cost CostClass `json:"cost_class"`
	// Debts are the evidence debts this blocker corresponds to.
	Debts []string `json:"debts,omitempty"`
	// Startable, when false, says why the engagement cannot begin now.
	NotStartableBecause string `json:"not_startable_because,omitempty"`
}

func (b Blocker) Validate() error {
	if strings.TrimSpace(b.ID) == "" {
		return errors.New("readiness: a blocker has no id")
	}
	if !b.Dimension.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownDimension, b.Dimension)
	}
	if strings.TrimSpace(b.Owner) == "" {
		return fmt.Errorf("%w: %s", ErrNoOwner, b.ID)
	}
	if !b.Validator.Valid() {
		return fmt.Errorf("readiness: %s names unknown validator type %q", b.ID, b.Validator)
	}
	if strings.TrimSpace(b.ExpectedEvidence) == "" {
		return fmt.Errorf("%w: %s. Stating it in advance is what stops a report arriving "+
			"that does not answer the question", ErrNoEvidence, b.ID)
	}
	if strings.TrimSpace(b.LeadTime) == "" {
		return fmt.Errorf("readiness: %s states no lead time", b.ID)
	}
	if !b.Cost.Valid() {
		return fmt.Errorf("readiness: %s has unknown cost class %q", b.ID, b.Cost)
	}
	return nil
}

// Startable reports whether this blocker's engagement could begin now.
func (b Blocker) Startable() bool {
	return len(b.DependsOn) == 0 && strings.TrimSpace(b.NotStartableBecause) == ""
}

// Plan is the procurement graph.
type Plan struct {
	blockers map[string]Blocker
	order    []string
}

// NewPlan builds and checks the graph.
func NewPlan(bs ...Blocker) (*Plan, error) {
	p := &Plan{blockers: map[string]Blocker{}}
	for _, b := range bs {
		if err := b.Validate(); err != nil {
			return nil, err
		}
		if _, dup := p.blockers[b.ID]; dup {
			return nil, fmt.Errorf("readiness: blocker %s appears twice", b.ID)
		}
		p.blockers[b.ID] = b
		p.order = append(p.order, b.ID)
	}
	sort.Strings(p.order)
	for _, id := range p.order {
		for _, dep := range p.blockers[id].DependsOn {
			if _, ok := p.blockers[dep]; !ok {
				return nil, fmt.Errorf("%w: %s depends on %s", ErrBadDep, id, dep)
			}
		}
	}
	if cyc := p.findCycle(); cyc != "" {
		return nil, fmt.Errorf("%w: %s", ErrDepCycle, cyc)
	}
	return p, nil
}

func (p *Plan) findCycle() string {
	const white, grey, black = 0, 1, 2
	colour := map[string]int{}
	var path []string
	var walk func(string) string
	walk = func(id string) string {
		colour[id] = grey
		path = append(path, id)
		for _, dep := range p.blockers[id].DependsOn {
			switch colour[dep] {
			case grey:
				return strings.Join(append(path, dep), " -> ")
			case white:
				if c := walk(dep); c != "" {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		colour[id] = black
		return ""
	}
	for _, id := range p.order {
		if colour[id] == white {
			if c := walk(id); c != "" {
				return c
			}
		}
	}
	return ""
}

// All returns every blocker, ordered.
func (p *Plan) All() []Blocker {
	out := make([]Blocker, 0, len(p.blockers))
	for _, id := range p.order {
		out = append(out, p.blockers[id])
	}
	return out
}

// StartableNow returns the engagements that could begin today.
//
// It is the most useful list in the package: the answer to "what can
// we do this week" from a register that otherwise only says what is
// not done.
func (p *Plan) StartableNow() []Blocker {
	var out []Blocker
	for _, b := range p.All() {
		if b.Startable() {
			out = append(out, b)
		}
	}
	return out
}

// ByValidator groups blockers by the kind of party needed, which is
// how the work is actually bought: one engagement can clear several
// blockers if they need the same specialist.
func (p *Plan) ByValidator() map[ValidatorType][]Blocker {
	out := map[ValidatorType][]Blocker{}
	for _, b := range p.All() {
		out[b.Validator] = append(out[b.Validator], b)
	}
	return out
}

// CriticalPath returns the longest dependency chain, which bounds how
// quickly the whole set can clear however much is spent in parallel.
func (p *Plan) CriticalPath() []string {
	memo := map[string][]string{}
	var walk func(string) []string
	walk = func(id string) []string {
		if v, ok := memo[id]; ok {
			return v
		}
		var best []string
		for _, dep := range p.blockers[id].DependsOn {
			if c := walk(dep); len(c) > len(best) {
				best = c
			}
		}
		out := append(append([]string{}, best...), id)
		memo[id] = out
		return out
	}
	var longest []string
	for _, id := range p.order {
		if c := walk(id); len(c) > len(longest) {
			longest = c
		}
	}
	return longest
}

// Report renders the plan.
func (p *Plan) Report() string {
	var b strings.Builder
	b.WriteString("PROCUREMENT GRAPH\n")
	b.WriteString("  every blocker with the party who clears it, what they must produce,\n")
	b.WriteString("  what has to be true first, and roughly what it costs. A register\n")
	b.WriteString("  without these is a list of things that are not done; with them it\n")
	b.WriteString("  is a schedule.\n\n")

	for _, bl := range p.All() {
		fmt.Fprintf(&b, "  %s  [%s]\n", bl.ID, bl.Dimension)
		fmt.Fprintf(&b, "    owner:      %s\n", bl.Owner)
		v := string(bl.Validator)
		if bl.ValidatorQualification != "" {
			v += " -- " + bl.ValidatorQualification
		}
		fmt.Fprintf(&b, "    validator:  %s\n", v)
		fmt.Fprintf(&b, "    evidence:   %s\n", bl.ExpectedEvidence)
		if len(bl.DependsOn) > 0 {
			fmt.Fprintf(&b, "    depends on: %s\n", strings.Join(bl.DependsOn, ", "))
		}
		fmt.Fprintf(&b, "    lead time:  %s     cost: %s\n", bl.LeadTime, bl.Cost)
		if len(bl.Debts) > 0 {
			fmt.Fprintf(&b, "    debts:      %s\n", strings.Join(bl.Debts, ", "))
		}
		if bl.NotStartableBecause != "" {
			fmt.Fprintf(&b, "    NOT STARTABLE: %s\n", bl.NotStartableBecause)
		}
		b.WriteString("\n")
	}

	b.WriteString("STARTABLE NOW\n")
	sn := p.StartableNow()
	if len(sn) == 0 {
		b.WriteString("  nothing\n")
	}
	for _, bl := range sn {
		fmt.Fprintf(&b, "  %-10s %-24s %s\n", bl.ID, bl.Validator, bl.LeadTime)
	}

	b.WriteString("\nONE ENGAGEMENT PER SPECIALIST\n")
	byV := p.ByValidator()
	var vs []ValidatorType
	for v := range byV {
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	for _, v := range vs {
		var ids []string
		for _, bl := range byV[v] {
			ids = append(ids, bl.ID)
		}
		fmt.Fprintf(&b, "  %-24s clears %s\n", v, strings.Join(ids, ", "))
	}

	cp := p.CriticalPath()
	fmt.Fprintf(&b, "\nCRITICAL PATH (%d step(s))\n  %s\n", len(cp), strings.Join(cp, " -> "))
	b.WriteString("  However much is spent in parallel, the set cannot clear faster than\n")
	b.WriteString("  this chain.\n")
	return b.String()
}
