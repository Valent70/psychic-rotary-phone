// Package readiness answers "how ready is it" without a percentage.
//
// # Why not a number
//
// "VERIQO is 85% production ready" is a sentence with no truth conditions.
// It cannot be checked, it cannot be disagreed with in detail, and it
// invites the reader to interpolate: 85% sounds like six weeks. The
// reality it usually describes is a system whose architecture is
// finished and whose external validation has not started -- two facts
// that no single number can carry, because they are not on the same
// scale and one does not compensate for the other.
//
// The failure is the same one the uncertainty package refuses for
// intelligence claims, applied to the system itself: an aggregate lets
// a strong dimension carry a weak one, and the weak one is what the
// reader needs.
//
// So readiness is five dimensions, each with its own state, and there
// is deliberately no Overall(). The honest summary sentence is
// generated FROM the dimensions and names the weakest one.
//
// # The five
//
//	ARCHITECTURE            is the design right
//	SEMANTICS               are the rules enforced rather than documented
//	IMPLEMENTATION          is it built and internally assured
//	PRODUCTION_INFRA        has it run anywhere real
//	EXTERNAL_VALIDATION     has anybody outside looked
//
// The first three are within the builder's power. The last two are not,
// and that asymmetry is the whole point: effort moves the first three
// and cannot move the last two at all.
package readiness

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/assurance/state"
)

var (
	ErrUnknownDimension = errors.New("readiness: unknown dimension")
	ErrNoBasis          = errors.New("readiness: an assessment states no basis")
	ErrSelfAssessed     = errors.New("readiness: a dimension requiring an outside party was assessed from inside")
)

// Dimension is one axis of readiness.
//
// There are nine rather than five, because collapsing security and
// cryptography, or data rights and legal, hides the fact that they are
// blocked on DIFFERENT PARTIES with different lead times. A single
// "security" row at PENDING_EXTERNAL tells a reader to find an
// assessor; two rows tell them to find an assessor and a cryptographer,
// which is a different procurement.
type Dimension string

const (
	Architecture   Dimension = "ARCHITECTURE"
	Semantics      Dimension = "SEMANTICS"
	Implementation Dimension = "IMPLEMENTATION"
	Security       Dimension = "SECURITY"
	Cryptography   Dimension = "CRYPTOGRAPHY"
	Legal          Dimension = "LEGAL"
	DataRights     Dimension = "DATA_RIGHTS"
	Operations     Dimension = "OPERATIONS"
	Production     Dimension = "PRODUCTION"
)

// Dimensions returns all nine, in the order they are assessed.
func Dimensions() []Dimension {
	return []Dimension{Architecture, Semantics, Implementation, Security, Cryptography,
		Legal, DataRights, Operations, Production}
}

func (d Dimension) Valid() bool {
	for _, k := range Dimensions() {
		if k == d {
			return true
		}
	}
	return false
}

// SelfAssessable reports whether the builder can honestly assess this
// dimension alone.
//
// The first three: yes -- a team can judge its own architecture and
// know whether its rules are enforced. The rest: no. "We believe we
// would pass a pentest" is not an assessment, it is a hope, and a
// model that let it be recorded as one would defeat its own purpose.
func (d Dimension) SelfAssessable() bool {
	return d == Architecture || d == Semantics || d == Implementation
}

// Status is where a dimension stands, named by WHO IS BLOCKING IT.
//
// # Why the status names a party
//
// A level -- HIGH, SUBSTANTIAL, PARTIAL -- describes how far along
// something is, and invites the reader to ask how much further. That
// is the right question for work the builder can do and the wrong one
// for everything else, because for most of what remains the answer is
// not "further" but "somebody else".
//
// So a status names the party. PENDING_EXTERNAL is not a position on
// a scale; it is a sentence: this is finished as far as we can finish
// it, and what happens next is not ours to do. A reader who sees
// dimensions at PENDING_EXTERNAL, PENDING_COUNSEL and PENDING_PARTNER
// has learned something a percentage cannot express -- that the
// remaining work is a procurement problem, not an engineering one.
type Status string

const (
	// NotSpecified: nobody has said what this dimension requires. The
	// zero value.
	NotSpecified Status = "NOT_SPECIFIED"
	// InProgress: the builder is working on it and it is unfinished.
	InProgress Status = "IN_PROGRESS"
	// InternallyAssured: the builder has finished and attacked it.
	// This is the ceiling for anything the builder can reach alone.
	InternallyAssured Status = "INTERNALLY_ASSURED"
	// PendingExternal: complete as far as the builder can take it, and
	// waiting on an independent assessor.
	PendingExternal Status = "PENDING_EXTERNAL"
	// PendingCounsel: waiting on a legal opinion. Distinct from
	// PENDING_EXTERNAL because a security assessor cannot answer a
	// lawfulness question and a lawyer cannot answer a cryptographic
	// one -- different queues, different lead times.
	PendingCounsel Status = "PENDING_COUNSEL"
	// PendingPartner: waiting on a commercial party -- a data
	// provider, a customer supplying a corpus, a signing authority.
	PendingPartner Status = "PENDING_PARTNER"
	// NotYetProven: nothing is blocking except that it has never been
	// run. Distinct from the PENDING statuses: no party is being
	// waited on, and the work is available to anyone with the
	// infrastructure.
	NotYetProven Status = "NOT_YET_PROVEN"
	// NotQualified: the terminal negative. It is not a stage on the
	// way to anything; it is the answer until every dimension it rests
	// on is settled.
	NotQualified Status = "NOT_QUALIFIED"
	// Qualified: settled, by whoever was entitled to settle it.
	Qualified Status = "QUALIFIED"
)

// Statuses returns every status in a fixed order.
func Statuses() []Status {
	return []Status{NotSpecified, InProgress, InternallyAssured, PendingExternal,
		PendingCounsel, PendingPartner, NotYetProven, NotQualified, Qualified}
}

func (s Status) Valid() bool {
	for _, k := range Statuses() {
		if k == s {
			return true
		}
	}
	return false
}

// BlockedOn names the party whose action is required, or the empty
// string when nobody is being waited on.
//
// It is derived from the status rather than stored, so the two cannot
// disagree.
func (s Status) BlockedOn() string {
	switch s {
	case PendingExternal:
		return "an independent assessor"
	case PendingCounsel:
		return "legal counsel, per jurisdiction"
	case PendingPartner:
		return "a commercial partner"
	case NotYetProven:
		return "nobody -- this needs infrastructure and a period of operation, not a party"
	case NotQualified:
		return "every dimension below it"
	}
	return ""
}

// SelfReachable reports whether the builder can move a dimension into
// this status alone.
func (s Status) SelfReachable() bool {
	switch s {
	case NotSpecified, InProgress, InternallyAssured:
		return true
	}
	return false
}

// Settled reports whether the dimension needs no further work.
func (s Status) Settled() bool { return s == Qualified }

// Assessment is one dimension's position.
type Assessment struct {
	Dimension Dimension `json:"dimension"`
	Status    Status    `json:"status"`
	// Basis is what the status rests on, in a sentence somebody can
	// disagree with.
	Basis string `json:"basis"`
	// Needs is what would settle it. Required for anything unsettled,
	// and phrased as an action somebody can take rather than as a
	// condition that might obtain.
	Needs []string `json:"needs,omitempty"`
	// AssessedBy names who judged it.
	AssessedBy string `json:"assessed_by"`
	// External marks an assessment by a party that is not the builder.
	External bool `json:"external"`
	// MaxAssuranceState is the highest assurance rung any control in
	// this dimension has reached, tying readiness to the register so
	// the two cannot drift apart.
	MaxAssuranceState state.State `json:"max_assurance_state"`
	// Debts are the evidence debts behind it.
	Debts []string `json:"debts,omitempty"`
}

func (a Assessment) Validate() error {
	if !a.Dimension.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownDimension, a.Dimension)
	}
	if !a.Status.Valid() {
		return fmt.Errorf("readiness: %s has unknown status %q", a.Dimension, a.Status)
	}
	if strings.TrimSpace(a.Basis) == "" {
		return fmt.Errorf("%w: %s", ErrNoBasis, a.Dimension)
	}
	if strings.TrimSpace(a.AssessedBy) == "" {
		return fmt.Errorf("readiness: %s does not say who assessed it", a.Dimension)
	}
	// The rule that makes this model honest rather than decorative: a
	// dimension needing an outside party cannot be recorded by the
	// builder as anything the builder reached.
	if !a.Dimension.SelfAssessable() && !a.External && a.Status == InternallyAssured {
		return fmt.Errorf("%w: %s was recorded as %s by %s, who is not an outside party. "+
			"For this dimension that status is a hope, not an assessment",
			ErrSelfAssessed, a.Dimension, a.Status, a.AssessedBy)
	}
	if a.Status == Qualified && !a.External {
		return fmt.Errorf("%w: %s was recorded as QUALIFIED by the builder",
			ErrSelfAssessed, a.Dimension)
	}
	if !a.Status.Settled() && len(a.Needs) == 0 {
		return fmt.Errorf("readiness: %s is %s and names nothing that would settle it. A "+
			"status with no stated need is a complaint", a.Dimension, a.Status)
	}
	return nil
}

// Profile is the whole picture.
//
// There is deliberately no Overall(), no score, and no percentage.
type Profile struct {
	assessments map[Dimension]Assessment
}

// New builds a profile and requires every dimension.
//
// A missing dimension is not "not applicable": it is one nobody
// assessed, and omitting it from a report is how the weakest axis
// disappears.
func New(as ...Assessment) (*Profile, error) {
	p := &Profile{assessments: map[Dimension]Assessment{}}
	for _, a := range as {
		if err := a.Validate(); err != nil {
			return nil, err
		}
		if _, dup := p.assessments[a.Dimension]; dup {
			return nil, fmt.Errorf("readiness: %s assessed twice", a.Dimension)
		}
		p.assessments[a.Dimension] = a
	}
	var missing []string
	for _, d := range Dimensions() {
		if _, ok := p.assessments[d]; !ok {
			missing = append(missing, string(d))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("readiness: no assessment for %s; an omitted dimension is "+
			"one nobody looked at, not one that does not apply", strings.Join(missing, ", "))
	}
	return p, nil
}

// All returns every assessment in dimension order.
func (p *Profile) All() []Assessment {
	out := make([]Assessment, 0, len(p.assessments))
	for _, d := range Dimensions() {
		out = append(out, p.assessments[d])
	}
	return out
}

// BlockedOn groups the unsettled dimensions by the party that would
// settle them.
//
// This is the readiness answer that is actually useful: not "how
// ready", but "who do we need to call".
func (p *Profile) BlockedOn() map[string][]Dimension {
	out := map[string][]Dimension{}
	for _, d := range Dimensions() {
		a := p.assessments[d]
		if a.Status.Settled() {
			continue
		}
		who := a.Status.BlockedOn()
		if who == "" {
			who = "the builder"
		}
		out[who] = append(out[who], d)
	}
	return out
}

// SelfReachableRemaining returns the dimensions the builder can still
// move alone. It is the honest answer to "what can we do next".
func (p *Profile) SelfReachableRemaining() []Dimension {
	var out []Dimension
	for _, d := range Dimensions() {
		a := p.assessments[d]
		if !a.Status.Settled() && a.Status.SelfReachable() && a.Status != InternallyAssured {
			out = append(out, d)
		}
	}
	return out
}

// ExternallyTouched reports whether any dimension has been assessed by
// somebody who is not the builder.
func (p *Profile) ExternallyTouched() bool {
	for _, a := range p.assessments {
		if a.External {
			return true
		}
	}
	return false
}

// Sentence is the honest one-line summary, generated from the
// assessments so that it cannot drift away from them.
//
// This is the replacement for "VERIQO is 85% production ready".
func (p *Profile) Sentence() string {
	blocked := p.BlockedOn()
	var parties []string
	for who := range blocked {
		if who != "the builder" {
			parties = append(parties, who)
		}
	}
	sort.Strings(parties)
	selfDone := 0
	for _, a := range p.All() {
		if a.Status == InternallyAssured {
			selfDone++
		}
	}
	return fmt.Sprintf(
		"%d of %d dimensions are internally assured -- the ceiling for anything the "+
			"builder can reach alone. The remaining %d are blocked on %s. No aggregate "+
			"figure is offered, because the remaining work is not more of the same work.",
		selfDone, len(Dimensions()), len(Dimensions())-selfDone,
		strings.Join(parties, "; "))
}

// Report renders the profile.
func (p *Profile) Report() string {
	var b strings.Builder
	b.WriteString("READINESS BY DIMENSION\n")
	b.WriteString("  Each status names WHO IS BLOCKING, not how far along it is. For most\n")
	b.WriteString("  of what remains the answer is not 'further' but 'somebody else', and\n")
	b.WriteString("  a percentage cannot say that.\n\n")
	for _, a := range p.All() {
		fmt.Fprintf(&b, "  %-16s -> %s\n", a.Dimension, a.Status)
	}
	b.WriteString("\n")
	for _, a := range p.All() {
		fmt.Fprintf(&b, "  %s [%s]\n", a.Dimension, a.Status)
		fmt.Fprintf(&b, "    %s\n", a.Basis)
		if who := a.Status.BlockedOn(); who != "" {
			fmt.Fprintf(&b, "    blocked on: %s\n", who)
		}
		for _, n := range a.Needs {
			fmt.Fprintf(&b, "    needs: %s\n", n)
		}
		if !a.External {
			fmt.Fprintf(&b, "    assessed by %s (the builder -- not an independent "+
				"assessment)\n", a.AssessedBy)
		} else {
			fmt.Fprintf(&b, "    assessed by %s\n", a.AssessedBy)
		}
		b.WriteString("\n")
	}

	b.WriteString("WHO WE NEED\n")
	blocked := p.BlockedOn()
	var parties []string
	for who := range blocked {
		parties = append(parties, who)
	}
	sort.Strings(parties)
	for _, who := range parties {
		var ds []string
		for _, d := range blocked[who] {
			ds = append(ds, string(d))
		}
		fmt.Fprintf(&b, "  %-52s %s\n", who, strings.Join(ds, ", "))
	}
	if r := p.SelfReachableRemaining(); len(r) > 0 {
		var ds []string
		for _, d := range r {
			ds = append(ds, string(d))
		}
		fmt.Fprintf(&b, "\n  Still movable by the builder alone: %s\n", strings.Join(ds, ", "))
	} else {
		b.WriteString("\n  Nothing remaining is movable by the builder alone.\n")
	}
	if !p.ExternallyTouched() {
		b.WriteString("  No dimension has been assessed by anybody outside the builder.\n")
	}
	b.WriteString("\n  " + p.Sentence() + "\n")
	return b.String()
}

// Veriqo is VERIQO's own readiness, as of the assurance register's
// assessment date.
func Veriqo() (*Profile, error) {
	const by = "VERIQO engineering"
	return New(
		Assessment{Dimension: Architecture, Status: InternallyAssured, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the qualification kernel is separated from the intelligence fabric, " +
				"the design laws each have an enforcement site, and the canonical pipeline " +
				"composes end to end on a worked case",
			Needs: []string{
				"an outside architect to review it; no architecture has survived contact " +
					"with a deployment",
			}},

		Assessment{Dimension: Semantics, Status: InternallyAssured, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the rules are enforced by types and constructors rather than by " +
				"convention: a forbidden AI act cannot be recorded, an assurance promotion " +
				"needing independence cannot be represented without it, a gate cannot be " +
				"closed by editing a field, and a figure cannot be rendered without its " +
				"epistemic status",
			Needs: []string{
				"an independent reading of the invariants; an operator with commit access " +
					"can still change the code",
			}},

		Assessment{Dimension: Implementation, Status: InternallyAssured, AssessedBy: by,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the kernel, the assurance layer, the verification kit and two domain " +
				"surfaces are built, tested and attacked; the adversarial suites have found " +
				"and closed four real defects in the system's own controls",
			Needs: []string{
				"an independent canonicaliser to compare against (ED-011)",
			},
			Debts: []string{"ED-011"}},

		Assessment{Dimension: Security, Status: PendingExternal, AssessedBy: by,
			External:          false,
			MaxAssuranceState: state.InternallyAssured,
			Basis: "the controls are implemented and internally attacked. No party outside " +
				"VERIQO has examined any of them, and the adversarial tests were written by " +
				"the people who wrote the defences",
			Needs: []string{
				"an accredited penetration testing firm (ED-001)",
				"a red team with AI-agent experience for the injection defence (ED-005)",
			},
			Debts: []string{"ED-001", "ED-005"}},

		Assessment{Dimension: Cryptography, Status: PendingExternal, AssessedBy: by,
			MaxAssuranceState: state.InternallyTested,
			Basis: "the key hierarchy and rotation semantics are implemented against a " +
				"software root that refuses production mode. No cryptographer has reviewed " +
				"the derivation, and no anchor exists",
			Needs: []string{
				"an HSM or cloud KMS with an attestation (ED-002)",
				"a timestamping authority or countersigning party (ED-003)",
				"a cryptographic review of the derivation and the canonical form (ED-001, ED-011)",
			},
			Debts: []string{"ED-002", "ED-003", "ED-011"}},

		Assessment{Dimension: Legal, Status: PendingCounsel, AssessedBy: by,
			MaxAssuranceState: state.InternallyTested,
			Basis: "the source-class lawfulness model is engineering's reading of the " +
				"constraints. No lawyer has reviewed it in any jurisdiction, and a wrong " +
				"reading here is not a bug -- it is unlawful processing",
			Needs: []string{
				"external counsel per jurisdiction for the restricted source classes (ED-010)",
			},
			Debts: []string{"ED-010"}},

		Assessment{Dimension: DataRights, Status: PendingPartner, AssessedBy: by,
			MaxAssuranceState: state.InternallyTested,
			Basis: "the six licence questions are asked separately and a derivative takes " +
				"the intersection of its sources. No commercial licence has ever been " +
				"encoded from real terms, and purpose limitation has never been exercised " +
				"against a real licensor's restrictions",
			Needs: []string{
				"a commercial data provider and a signed contract (ED-007)",
				"a customer or industry body supplying a real document corpus (ED-004)",
			},
			Debts: []string{"ED-004", "ED-007"}},

		Assessment{Dimension: Operations, Status: NotYetProven, AssessedBy: by,
			MaxAssuranceState: state.Implemented,
			Basis: "nothing has ever run outside a single process. There is no deployment, " +
				"no multi-host run, no soak beyond seconds, no measured recovery and no " +
				"operational telemetry of any kind",
			Needs: []string{
				"multi-host and multi-region infrastructure (ED-006)",
				"a timed disaster recovery and a restore-and-replay verification (ED-006)",
				"a 72-hour soak under real load (ED-006)",
			},
			Debts: []string{"ED-006"}},

		Assessment{Dimension: Production, Status: NotQualified, AssessedBy: by,
			MaxAssuranceState: state.Implemented,
			Basis: "the terminal answer while every dimension above it is unsettled. It is " +
				"not a stage on the way to anything: it is what the system is, until the " +
				"others are settled by the parties entitled to settle them",
			Needs: []string{
				"every other dimension settled, and then a release decision by a named " +
					"authority",
			}},
	)
}
