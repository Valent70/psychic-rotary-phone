package assurance

import (
	"fmt"
	"sort"
	"strings"
)

// The semantic authority audit.
//
// A reviewer put the distinction precisely:
//
//	pkg/proof            decides sufficiency
//	pkg/casefabric       decides sufficiency
//	pkg/caseproofgraph   derives sufficiency
//
//	Derivation is permitted. Decision authority is not.
//
// Every anti-duplication test written before this one checked for
// duplicate PACKAGES — that a fabric had no engine of its own, that a
// domain had not forked the spine. None of them checked for duplicate
// DECISIONS, and that is the failure that actually hurts: two packages
// can share no code, import nothing of each other's, and still both
// answer "is this sufficient?" with no way to tell which answer governs.
//
// So this file names, for each decision the system makes, exactly one
// authority — and names the packages that legitimately DERIVE, COPY or
// RECORD that decision without being an authority for it.
//
// The distinction, stated once:
//
//	DECIDES    computes the answer from inputs and may return a
//	           different answer than anyone else would
//	DERIVES    recomputes the same answer from the same inputs by the
//	           same rule; cannot disagree
//	COPIES     carries the authority's answer verbatim
//	RECORDS    writes the answer down without reading it

// Role is what a package does with respect to one decision.
type Role string

const (
	// RoleDecides: this package is the authority. Exactly one per
	// decision.
	RoleDecides Role = "DECIDES"
	// RoleDerives: recomputes the authority's answer by the authority's
	// own rule. Cannot disagree, because it calls the authority or
	// applies the identical function.
	RoleDerives Role = "DERIVES"
	// RoleCopies: carries the answer verbatim.
	RoleCopies Role = "COPIES"
	// RoleRecords: persists the answer without interpreting it.
	RoleRecords Role = "RECORDS"
)

// Participant is one package's role in one decision.
type Participant struct {
	Package string
	Role    Role
	// How states what the package actually does, precisely enough that
	// a reviewer can check the claim against the code.
	How string
}

// DecisionAuthority is one decision the system makes, its single
// authority, and everyone else who touches it.
type DecisionAuthority struct {
	// Decision is the question, phrased as a question.
	Decision string
	// Authority is the package that answers it. Exactly one.
	Authority string
	// AuthorityEntryPoint is the function that is the answer.
	AuthorityEntryPoint string
	// Participants are the non-authorities, each with its role.
	Participants []Participant
	// WhyNotDuplicated explains why the participants are not rival
	// authorities. This is the field a reviewer should attack.
	WhyNotDuplicated string
}

var decisionAuthorities = []DecisionAuthority{
	{
		Decision:            "Is this proof object sufficient to found a finding?",
		Authority:           "veriqo/pkg/proof",
		AuthorityEntryPoint: "proof.Object.deriveSufficiency, invoked only by proof.Seal",
		Participants: []Participant{
			{"veriqo/pkg/casefabric", RoleCopies,
				"Claim.Sufficiency is copied from the attached object in AttachProof; Claim.Proven() reads that copy and computes nothing"},
			{"veriqo/pkg/caseproofgraph", RoleCopies,
				"the sufficiency attribute on a proof node is read from the object; the graph never evaluates the sufficiency rule"},
			{"veriqo/pkg/assurance", RoleRecords,
				"the traceability matrix cites tests of the rule; it does not apply it"},
		},
		WhyNotDuplicated: "Sufficiency is computed in exactly one function, and Seal overwrites any author-supplied value. " +
			"No other package contains the conjunctive test, so no other package can return a different answer.",
	},
	{
		Decision:            "Does a finding exist for this proof object?",
		Authority:           "veriqo/pkg/proof",
		AuthorityEntryPoint: "proof.NewFinding",
		Participants: []Participant{
			{"veriqo/pkg/casefabric", RoleRecords,
				"RecordFinding writes that a finding exists and checks it belongs to an attached proof; it cannot construct one"},
			{"veriqo/pkg/caseproofgraph", RoleCopies,
				"Build materializes a Finding the caller supplies and refuses one belonging to another proof object"},
		},
		WhyNotDuplicated: "caseproofgraph called proof.NewFinding until the sequencing audit, which made it a second place a finding " +
			"could come into existence. It now materializes only, and TestCaseProofGraphCannotCreateIndependentFindingAuthority " +
			"fails if a finding node appears without one being supplied.",
	},
	{
		Decision:            "Has an authority adopted this finding?",
		Authority:           "veriqo/pkg/proof",
		AuthorityEntryPoint: "proof.Authorize",
		Participants: []Participant{
			{"veriqo/pkg/casefabric", RoleRecords,
				"RecordAuthorizedDecision writes it down and checks the lineage; ResolutionGate consumes it"},
			{"veriqo/pkg/caseproofgraph", RoleCopies,
				"AddDecision materializes the decision node from a Decision the caller holds"},
		},
		WhyNotDuplicated: "AuthorizedFinding has unexported fields and no constructor but Authorize, which refuses a zero finding " +
			"and refuses an authorizer who generated the proof object.",
	},
	{
		Decision:            "What operational action follows from an authorized finding?",
		Authority:           "veriqo/pkg/proof",
		AuthorityEntryPoint: "proof.Decide",
		Participants: []Participant{
			{"veriqo/pkg/insurance/action", RoleDerives,
				"maps a decision onto a domain action; cannot produce a decision"},
			{"veriqo/pkg/casefabric", RoleRecords, "records the decision on the case timeline"},
		},
		WhyNotDuplicated: "Decision is constructible only from an AuthorizedFinding, and the adjudication guard is applied in " +
			"exactly one place. casefabric.Outcome.Validate reuses proof.ProhibitedDecisionFields rather than keeping a second list.",
	},
	{
		Decision:            "What is the qualification state of this claim?",
		Authority:           "veriqo/pkg/qualification/state",
		AuthorityEntryPoint: "state.New",
		Participants: []Participant{
			{"veriqo/pkg/proof", RoleCopies, "Object.Qualification carries the record; deriveStance reads its State"},
			{"veriqo/pkg/governance/qualification", RoleRecords,
				"records operational qualification decisions about releases and gates, which is a different question from a claim's epistemic state"},
			{"veriqo/pkg/caseproofgraph", RoleCopies, "the qualification node carries the state string"},
		},
		WhyNotDuplicated: "There is one state vocabulary and one constructor. proof.deriveStance maps that state onto a stance " +
			"by a fixed table; it cannot invent a state, and there is no PROVEN state for it to reach.",
	},
	{
		Decision:            "Are two sources independent?",
		Authority:           "veriqo/pkg/qualification/independence",
		AuthorityEntryPoint: "independence.Assess",
		Participants: []Participant{
			{"veriqo/pkg/proof", RoleCopies, "TrustAssessment.Verdicts carries the verdicts; the effective source count is copied"},
			{"veriqo/pkg/core/trustcalc", RoleDerives, "computes trust over sources whose independence was assessed here"},
		},
		WhyNotDuplicated: "UNKNOWN is a verdict only this package can produce, and SatisfiesIndependenceRequirement is true only for " +
			"Independent. Nothing downstream can promote UNKNOWN.",
	},
	{
		Decision:            "May this recipient see this evidence?",
		Authority:           "veriqo/pkg/disclosure/access",
		AuthorityEntryPoint: "access.Evaluate",
		Participants: []Participant{
			{"veriqo/pkg/ai/gateway", RoleDerives,
				"refuses forbidden actions first, then delegates rights, privilege and protective orders to access.Evaluate"},
			{"veriqo/pkg/caseproofgraph", RoleDerives,
				"Project specializes the grant per evidence node and asks access.Evaluate; structural nodes are checked against the two levels and the right, which access.Evaluate does not model"},
		},
		WhyNotDuplicated: "The gateway and the graph both call the authority rather than reimplementing it. The graph's own check " +
			"covers nodes that are not evidence versions, where there is no grant to evaluate — inventing a fake version id to " +
			"route them through would be worse than no decision.",
	},
	{
		Decision:            "What phase is this case in?",
		Authority:           "veriqo/pkg/casefabric",
		AuthorityEntryPoint: "casefabric.Case, via its lifecycle methods and CanTransition",
		Participants: []Participant{
			{"veriqo/pkg/insurance/casestate", RoleDerives,
				"owns insurance's fourteen states; every one maps onto a canonical phase, and the mapping is asserted by test"},
			{"veriqo/pkg/workflow", RoleRecords,
				"executes who does what and when; it holds no opinion about what a case knows"},
		},
		WhyNotDuplicated: "A domain state that maps to no canonical phase is refused, so a domain cannot invent a phase. " +
			"The workflow engine is the execution mechanism and the fabric is the semantic spine; four tests hold that line.",
	},
	{
		Decision:            "May this case resolve?",
		Authority:           "veriqo/pkg/casefabric",
		AuthorityEntryPoint: "casefabric.Case.Resolve, gated by ResolutionGate",
		Participants: []Participant{
			{"veriqo/pkg/fref", RoleDerives,
				"computes the closure the gate carries, and states the sequence the gate enforces; it does not resolve cases"},
			{"veriqo/pkg/proof", RoleDerives, "supplies the authorized decision the gate consumes"},
		},
		WhyNotDuplicated: "Resolution is one method. It consumes evidence that prior steps happened rather than asserting them, " +
			"and the gate's fields are values only the real authorities can produce.",
	},
	{
		Decision:            "Does this attestation prove existence before a time?",
		Authority:           "veriqo/pkg/platform/timestamp",
		AuthorityEntryPoint: "timestamp.Assess, which derives Attestation.kind",
		Participants: []Participant{
			{"veriqo/pkg/commercial/dossier", RoleCopies, "reports the attestation kind"},
		},
		WhyNotDuplicated: "Attestation.kind is unexported and derived. No caller can set it, so no caller can claim independent " +
			"attestation without a real token from a party independent of the matter.",
	},
	{
		Decision:            "What level of proof has this conclusion reached?",
		Authority:           "veriqo/pkg/proof",
		AuthorityEntryPoint: "proof.LevelOf and proof.RaiseToExternallyAttested",
		Participants: []Participant{
			{"veriqo/pkg/assurance", RoleRecords,
				"the capability table and the maturity model report levels; they do not compute a proof object's level"},
		},
		WhyNotDuplicated: "The level is derived, never stored on the object. PROOF_EXTERNALLY_ATTESTED requires a named external " +
			"attestor, and no VERIQO code path calls the function that reaches it.",
	},
}

// DecisionAuthorities returns the audit.
func DecisionAuthorities() []DecisionAuthority {
	return append([]DecisionAuthority(nil), decisionAuthorities...)
}

// AuthorityFor returns the single authority for a decision.
func AuthorityFor(decision string) (DecisionAuthority, bool) {
	for _, d := range decisionAuthorities {
		if d.Decision == decision {
			return d, true
		}
	}
	return DecisionAuthority{}, false
}

// ValidateAuthorities checks the audit's own shape: every decision names
// exactly one authority, no participant claims to decide, and every
// participant states what it actually does.
//
// The second check is the one that matters. A participant marked DECIDES
// would be a second authority by the audit's own admission, and the
// audit should fail rather than document the duplication.
func ValidateAuthorities() error {
	seen := map[string]bool{}
	for _, d := range decisionAuthorities {
		if seen[d.Decision] {
			return fmt.Errorf("assurance: decision %q is audited twice", d.Decision)
		}
		seen[d.Decision] = true

		if strings.TrimSpace(d.Authority) == "" || strings.TrimSpace(d.AuthorityEntryPoint) == "" {
			return fmt.Errorf("assurance: decision %q names no authority or no entry point", d.Decision)
		}
		if !strings.HasSuffix(d.Decision, "?") {
			return fmt.Errorf("assurance: decision %q is not phrased as a question, so what is being decided is unclear", d.Decision)
		}
		if strings.TrimSpace(d.WhyNotDuplicated) == "" {
			return fmt.Errorf("assurance: decision %q does not explain why its participants are not rival authorities", d.Decision)
		}
		for _, p := range d.Participants {
			if p.Role == RoleDecides {
				return fmt.Errorf("assurance: decision %q lists %s as a second DECIDES authority", d.Decision, p.Package)
			}
			if p.Package == d.Authority {
				return fmt.Errorf("assurance: decision %q lists its own authority %s as a participant", d.Decision, p.Package)
			}
			if strings.TrimSpace(p.How) == "" {
				return fmt.Errorf("assurance: decision %q does not say what %s actually does", d.Decision, p.Package)
			}
		}
	}
	return nil
}

// PackagesThatDecide returns every package that is an authority for at
// least one decision, with the decisions it owns.
//
// A package owning several decisions is not a problem; two packages
// owning one decision is, and ValidateAuthorities makes that
// unrepresentable.
func PackagesThatDecide() map[string][]string {
	out := map[string][]string{}
	for _, d := range decisionAuthorities {
		out[d.Authority] = append(out[d.Authority], d.Decision)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// AuthorityReport renders the audit.
func AuthorityReport() string {
	var b strings.Builder
	b.WriteString("VERIQO semantic authority audit\n")
	b.WriteString("DECIDES = the authority. DERIVES/COPIES/RECORDS = not an authority.\n\n")
	for _, d := range decisionAuthorities {
		b.WriteString(d.Decision + "\n")
		b.WriteString(fmt.Sprintf("  %-9s %s (%s)\n", RoleDecides, d.Authority, d.AuthorityEntryPoint))
		for _, p := range d.Participants {
			b.WriteString(fmt.Sprintf("  %-9s %s -- %s\n", p.Role, p.Package, p.How))
		}
		b.WriteString("  WHY NOT DUPLICATED: " + d.WhyNotDuplicated + "\n\n")
	}
	return b.String()
}
