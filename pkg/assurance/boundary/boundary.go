// Package boundary is where VERIQO stops verifying itself.
//
// # The failure this package exists to prevent
//
// Every layer of assurance in this repository was added for a reason
// that was good at the time. The invariant chokepoint caught a real
// class of defect. The assurance mutation suite attacked the assurance
// graph and found something. The verifier-of-the-verifier found a bug.
// Each step was justified by the step before it, and that is exactly
// the shape of a runaway:
//
//	system
//	  -> assurance of the system
//	    -> assurance of the assurance
//	      -> verifier of the assurance
//	        -> test of the verifier
//	          -> test of the test
//	            -> ...
//
// There is no natural stopping point on that ladder, because at every
// rung the next rung can be justified by the same argument that
// justified the current one. A system that follows it becomes a
// machine for continuously verifying its own verification machinery,
// and value creation stops -- not with a failure, but with a green
// board and nothing shipped.
//
// # The boundary
//
//	VERIQO ASSURANCE KERNEL          layers 0-3, ours
//	--------------------------------  <- the boundary
//	INDEPENDENT VERIFICATION          layer 4, a different party
//	EXTERNAL ASSESSOR                 layer 5, a party with standing
//
// Layers 0 to 3 are VERIQO's and are CLOSED. Layer 4 and above are not
// reachable by writing more Go, and adding a layer 4 inside this
// repository does not produce independent verification: it produces a
// fourth thing VERIQO wrote, checking a third thing VERIQO wrote.
//
// The reason is not fatigue. It is that self-verification has a
// mathematical ceiling on what it can establish. A verifier written by
// the same party, from the same assumptions, in the same repository,
// shares the blind spots of what it verifies. Stacking more of them
// increases cost linearly and shared-blind-spot coverage not at all.
//
// # What this package refuses
//
// Propose() classifies a proposed piece of work. Work at or below the
// boundary is ALLOWED. Work above it is REFUSED, with the reason, and
// the refusal names the party who would have to do it instead.
//
// This package is itself layer 3. It is the last assurance component
// this repository should acquire, and it says so in a test. That is
// deliberately close to self-defeating and is worth stating plainly:
// the argument for adding it is the same argument that justifies every
// other rung. The difference, and the only one, is that this rung
// terminates the ladder rather than extending it. If a future round
// adds a layer 4 while citing this package as precedent, this package
// has failed.
package boundary

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrBeyondBoundary is work that would add another layer of VERIQO
	// checking VERIQO.
	ErrBeyondBoundary = errors.New("boundary: this is VERIQO verifying VERIQO again")
	// ErrUnknownLayer is a layer outside the defined ladder.
	ErrUnknownLayer = errors.New("boundary: unknown layer")
)

// Layer is a rung on the assurance ladder.
type Layer int

const (
	// System is the product: the thing that has value if it works.
	System Layer = iota
	// Assurance checks the system. The invariant chokepoint, the
	// assurance state machine, the register.
	Assurance
	// AssuranceOfAssurance attacks the assurance itself. The assurance
	// mutation suite lives here.
	AssuranceOfAssurance
	// VerifierOfAssurance recomputes rather than reads: veriqo-verify,
	// and this package. This is the last layer VERIQO may occupy.
	VerifierOfAssurance
	// IndependentVerification is a different party running their own
	// checks against the evidence VERIQO packaged. Not reachable from
	// inside this repository.
	IndependentVerification
	// ExternalAssessment is a party with standing to qualify: an
	// accredited assessor, counsel, a red team.
	ExternalAssessment
)

// Ours is the highest layer VERIQO may build.
//
// Everything at or below it is inside the assurance kernel. Everything
// above it requires somebody who is not VERIQO, and no amount of
// engineering moves the line.
const Ours = VerifierOfAssurance

// Layers returns the ladder in order.
func Layers() []Layer {
	return []Layer{System, Assurance, AssuranceOfAssurance, VerifierOfAssurance,
		IndependentVerification, ExternalAssessment}
}

func (l Layer) Valid() bool { return l >= System && l <= ExternalAssessment }

func (l Layer) String() string {
	switch l {
	case System:
		return "SYSTEM"
	case Assurance:
		return "ASSURANCE"
	case AssuranceOfAssurance:
		return "ASSURANCE_OF_ASSURANCE"
	case VerifierOfAssurance:
		return "VERIFIER_OF_ASSURANCE"
	case IndependentVerification:
		return "INDEPENDENT_VERIFICATION"
	case ExternalAssessment:
		return "EXTERNAL_ASSESSMENT"
	}
	return "UNKNOWN"
}

// SelfBuildable reports whether VERIQO can build this layer at all.
func (l Layer) SelfBuildable() bool { return l <= Ours }

// Party names who does the work at this layer.
func (l Layer) Party() string {
	if l.SelfBuildable() {
		return "VERIQO"
	}
	switch l {
	case IndependentVerification:
		return "a party that did not write the system, running the published protocol"
	case ExternalAssessment:
		return "a party with standing to qualify: an accredited assessor, counsel, or a red team"
	}
	return "unknown"
}

// Establishes states what a layer can and cannot settle. The second
// half is the part that gets omitted.
func (l Layer) Establishes() string {
	switch l {
	case System:
		return "that the product does something; nothing about whether it does it correctly"
	case Assurance:
		return "that the system's own rules hold in the cases the author thought of; " +
			"nothing about the cases they did not"
	case AssuranceOfAssurance:
		return "that the rules survive mutation under a chosen operator set; " +
			"nothing about operators nobody chose"
	case VerifierOfAssurance:
		return "that the packaged evidence is internally consistent and recomputes; " +
			"nothing about whether the package is complete or honest"
	case IndependentVerification:
		return "that a second party, running the published protocol, reaches the " +
			"same result from the same bytes"
	case ExternalAssessment:
		return "that a party with standing examined the system and is willing to " +
			"say so under their own name"
	}
	return ""
}

// Verdict is the outcome of proposing a piece of work.
type Verdict string

const (
	// Allowed is work inside the kernel that is still worth doing.
	Allowed Verdict = "ALLOWED"
	// Refused is work above the boundary.
	Refused Verdict = "REFUSED"
	// Redundant is work at a layer that is already closed: another
	// instance of a check the kernel already performs. It is not
	// forbidden, it is simply not the constraint.
	Redundant Verdict = "REDUNDANT"
)

// Proposal is a piece of work somebody wants to do.
type Proposal struct {
	// Name is what it would be called.
	Name string `json:"name"`
	// Layer is where it sits on the ladder.
	Layer Layer `json:"layer"`
	// Rationale is the argument for it. It is recorded and NOT
	// evaluated: every rung on a runaway ladder has a good rationale,
	// so a strong rationale is not evidence that the work belongs.
	Rationale string `json:"rationale"`
	// ClosesExternalGate names a gate or debt this work is a direct
	// dependency of, when it is one. Work that closes an external gate
	// is the only kind that moves the system, and this is where a
	// proposal says so.
	ClosesExternalGate string `json:"closes_external_gate,omitempty"`
}

// Decision is what the boundary says about a proposal.
type Decision struct {
	Proposal Proposal `json:"proposal"`
	Verdict  Verdict  `json:"verdict"`
	// Because is the reason, in terms of what the work would and would
	// not establish.
	Because string `json:"because"`
	// Instead names what to do with the effort, when the answer is no.
	Instead string `json:"instead,omitempty"`
}

// KernelClosed lists the kernel layers that are complete, so a
// proposal at one of them is REDUNDANT rather than ALLOWED.
//
// All four are closed. That is the finding of Round 5 and the reason
// Round 6 is not more of the same.
var KernelClosed = map[Layer]string{
	System:               "the product exists and its packages carry their own tests",
	Assurance:            "the invariant chokepoint, the state machine and the register are in place",
	AssuranceOfAssurance: "the assurance mutation suite runs and states what it does not cover",
	VerifierOfAssurance:  "veriqo-verify recomputes rather than reads, and this boundary exists",
}

// Propose classifies a piece of work against the boundary.
func Propose(p Proposal) (Decision, error) {
	if strings.TrimSpace(p.Name) == "" {
		return Decision{}, errors.New("boundary: a proposal with no name cannot be refused or allowed")
	}
	if !p.Layer.Valid() {
		return Decision{}, fmt.Errorf("%w: %d", ErrUnknownLayer, int(p.Layer))
	}
	if strings.TrimSpace(p.Rationale) == "" {
		return Decision{}, errors.New("boundary: a proposal with no rationale cannot be " +
			"weighed against the ones already refused")
	}

	if !p.Layer.SelfBuildable() {
		return Decision{
			Proposal: p, Verdict: Refused,
			Because: fmt.Sprintf("%s is above the boundary. Building it inside this "+
				"repository produces a fourth thing VERIQO wrote checking a third thing "+
				"VERIQO wrote, which establishes nothing the kernel does not already "+
				"establish.", p.Layer),
			Instead: "engage " + p.Layer.Party() + ". This is a procurement item, not an " +
				"engineering one.",
		}, nil
	}

	if why, closed := KernelClosed[p.Layer]; closed {
		// Work that is a direct dependency of an external gate is
		// allowed even at a closed layer: the constraint is what the
		// work UNBLOCKS, not which layer it sits on.
		if strings.TrimSpace(p.ClosesExternalGate) != "" {
			return Decision{
				Proposal: p, Verdict: Allowed,
				Because: fmt.Sprintf("%s is closed, but this is a direct dependency of %s, "+
					"and an external gate is the only thing that moves the system.",
					p.Layer, p.ClosesExternalGate),
			}, nil
		}
		return Decision{
			Proposal: p, Verdict: Redundant,
			Because: fmt.Sprintf("%s is closed: %s. Another instance of it raises the "+
				"cost of the kernel and not what the kernel establishes.", p.Layer, why),
			Instead: "spend the effort on something a party outside VERIQO is waiting for. " +
				"Run 'veriqoctl procurement' for what that is.",
		}, nil
	}

	return Decision{
		Proposal: p, Verdict: Allowed,
		Because: fmt.Sprintf("%s is inside the kernel and not yet closed.", p.Layer),
	}, nil
}

// Depth is the deepest layer this repository occupies.
func Depth() Layer { return Ours }

// Report renders the ladder, the boundary and what each layer settles.
func Report() string {
	var b strings.Builder
	b.WriteString("THE ASSURANCE BOUNDARY\n")
	b.WriteString("  where VERIQO stops verifying itself, and why the line is there\n\n")
	for _, l := range Layers() {
		if l == IndependentVerification {
			b.WriteString("  " + strings.Repeat("-", 66) + "\n")
			b.WriteString("  THE BOUNDARY. Nothing below this line is reachable by writing Go.\n")
			b.WriteString("  " + strings.Repeat("-", 66) + "\n\n")
		}
		fmt.Fprintf(&b, "  %d  %s\n", int(l), l)
		b.WriteString(wrap("       established: ", l.Establishes()))
		b.WriteString(wrap("       done by:     ", l.Party()))
		if why, closed := KernelClosed[l]; closed {
			b.WriteString(wrap("       CLOSED:      ", why))
		}
		b.WriteString("\n")
	}
	b.WriteString(wrap("  ", "Layers 0-3 are closed. A fifth layer of VERIQO checking VERIQO "+
		"costs engineering time and establishes nothing the fourth does not. The "+
		"remaining distance is not more assurance; it is a party who is not VERIQO."))
	return b.String()
}

// ReportDecisions renders a set of proposals and what the boundary
// said about each, so a reader can disagree with a specific refusal.
func ReportDecisions(ds []Decision) string {
	var b strings.Builder
	counts := map[Verdict]int{}
	for _, d := range ds {
		counts[d.Verdict]++
	}
	b.WriteString("PROPOSALS WEIGHED AGAINST THE BOUNDARY\n")
	fmt.Fprintf(&b, "  %d allowed, %d redundant, %d refused\n\n",
		counts[Allowed], counts[Redundant], counts[Refused])
	sorted := append([]Decision(nil), ds...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Verdict != sorted[j].Verdict {
			return sorted[i].Verdict < sorted[j].Verdict
		}
		return sorted[i].Proposal.Name < sorted[j].Proposal.Name
	})
	for _, d := range sorted {
		fmt.Fprintf(&b, "  %-10s %s\n", d.Verdict, d.Proposal.Name)
		fmt.Fprintf(&b, "             layer %d, %s\n", int(d.Proposal.Layer), d.Proposal.Layer)
		b.WriteString(wrap("             ", d.Because))
		if d.Instead != "" {
			b.WriteString(wrap("             instead: ", d.Instead))
		}
		b.WriteString("\n")
	}
	return b.String()
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
