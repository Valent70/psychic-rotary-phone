package casefabric

import (
	"fmt"
	"sort"
	"strings"
)

// The phase contract.
//
// Nine phases are a list. A list is what a workflow engine has, and the
// risk this file exists to close is that the Case Fabric quietly becomes
// one under a new name — a state machine with pretty phase names, sitting
// beside pkg/workflow doing the same job twice.
//
// The difference is what a phase MEANS, not what it is called. So every
// phase declares nine things:
//
//	state              the canonical phase itself
//	entry condition    what must hold for a case to be in it
//	exit condition     what must hold before it may leave
//	required evidence  what the phase cannot proceed without
//	blocking evidence  what, if present, prevents progress
//	authority          who may move the case through it
//	owner              whose work the phase is
//	failure state      where the case goes when the phase cannot complete
//	replay reference   how the phase's effect is reproduced from the record
//
// Entry and exit conditions are semantic, not procedural. "Every material
// claim carries a sealed proof object" is a statement about knowledge;
// "the reviewer clicked approve" would be a statement about a workflow.
// The first belongs here. The second belongs in pkg/workflow, which
// remains the execution mechanism and is not duplicated.
//
// The division, stated once:
//
//	Case Fabric    the semantic spine — what is known, and what that means
//	Workflow       the execution mechanism — who does what, when, in what order
//
// TestPhaseContractIsSemanticNotProcedural holds that line by test.

// PhaseContract is one phase's full definition.
type PhaseContract struct {
	State Phase
	// Entry is what must hold for a case to be in this phase.
	Entry string
	// Exit is what must hold before it may leave.
	Exit string
	// RequiredEvidence is what the phase cannot proceed without. Named
	// as evidence classes, not as documents: "a pinned evidence version
	// for every claim", not "the survey report".
	RequiredEvidence []string
	// BlockingEvidence is what, if present, prevents progress. This is
	// the field a workflow engine does not have: it is knowledge that
	// stops work, not a gate somebody failed to open.
	BlockingEvidence []string
	// Authority is who may move the case through this phase.
	Authority string
	// Owner is whose work the phase is.
	Owner string
	// FailureState is where the case goes when the phase cannot
	// complete. Never empty: a phase with no failure state fails open.
	FailureState Phase
	// ReplayReference is how the phase's effect is reproduced from the
	// record by a party that does not trust the runtime.
	ReplayReference string
}

var phaseContracts = []PhaseContract{
	{
		State: PhaseOpened,
		Entry: "a case identity exists: case id, tenant and a registered domain",
		Exit:  "the matter, jurisdiction and time window are fixed and a mission is stated",
		RequiredEvidence: []string{
			"a registered domain projection for the opening domain",
		},
		BlockingEvidence: []string{
			"an existing open case over the same matter and tenant, which would split one dispute into two records",
		},
		Authority:       "any actor with case-open authority in the opening domain",
		Owner:           "the opening domain",
		FailureState:    PhaseClosed,
		ReplayReference: "casefabric timeline entry kind=case_opened; VerifyTimeline re-derives the chain",
	},
	{
		State: PhaseScoped,
		Entry: "matter, jurisdiction and an ordered time window are fixed; a mission statement names what the case must establish",
		Exit:  "at least one evidence version is pinned within scope",
		RequiredEvidence: []string{
			"a jurisdiction code",
			"an ordered time window",
			"a mission statement fixed before the evidence arrives",
		},
		BlockingEvidence: []string{
			"a legal hold that forbids collection in this jurisdiction",
			"a scope naming a different case, which would attach this work to the wrong matter",
		},
		Authority:       "an actor with scoping authority; the mission may not be rewritten after evidence arrives",
		Owner:           "the case analyst",
		FailureState:    PhaseSuspended,
		ReplayReference: "timeline kind=scope_set carries the jurisdiction and mission at the tick they were fixed",
	},
	{
		State: PhaseEvidenceGathering,
		Entry: "the case is scoped",
		Exit:  "at least one rival hypothesis is on the record",
		RequiredEvidence: []string{
			"every reference pins an evidence version id and a content hash",
			"a source id per reference, so independence can be assessed later",
		},
		BlockingEvidence: []string{
			"an unlicensed acquisition — Article 4 denies before contact, not after",
			"evidence whose custody chain is broken",
		},
		Authority:       "an actor with acquisition authority for the source, per pkg/authz",
		Owner:           "the acquisition function",
		FailureState:    PhaseSuspended,
		ReplayReference: "timeline kind=evidence_pinned; each version re-derivable by content hash",
	},
	{
		State: PhaseHypothesesFormed,
		Entry: "evidence is pinned and at least one rival explanation is recorded",
		Exit:  "every rival hypothesis has been tested, and claims are registered",
		RequiredEvidence: []string{
			"at least one rival hypothesis — a case with a single explanation has not been investigated",
			"a falsifiable proposition per registered claim",
		},
		BlockingEvidence: []string{
			"a hypothesis with no stated way to test it, which is a narrative rather than a rival",
		},
		Authority:       "an analyst; hypothesis formation is intelligence work and produces no finding",
		Owner:           "the intelligence function (pkg/moat/causal)",
		FailureState:    PhaseEvidenceGathering,
		ReplayReference: "timeline kinds hypothesis_recorded and hypothesis_tested carry the outcome in the tester's words",
	},
	{
		State: PhaseUnderQualification,
		Entry: "at least one rival hypothesis exists on the record",
		Exit:  "every material claim carries a sealed proof object that re-verifies",
		RequiredEvidence: []string{
			"a reverse-proof requirement set per claim",
			"an independence assessment over the source set",
			"an observability verdict for every asserted absence",
		},
		BlockingEvidence: []string{
			"an unresolved material contradiction — carried, never averaged away",
			"a material AI contribution with no named human reviewer",
			"an unassessed source set, which is UNKNOWN and never INDEPENDENT",
		},
		Authority:       "the qualification authority named in the proof object, under a pinned policy version",
		Owner:           "the Epistemic Qualification Fabric",
		FailureState:    PhaseEvidenceGathering,
		ReplayReference: "proof.VerifyHash re-derives each attached object from its components",
	},
	{
		State: PhaseResolved,
		Entry: "every material claim is proven and every rival hypothesis tested",
		Exit:  "an outcome is recorded that states what was established and what was not",
		RequiredEvidence: []string{
			"a non-adjudicatory disposition",
			"the limitations carried forward from every proof object the outcome rests on",
		},
		BlockingEvidence: []string{
			"an unproven material claim",
			"an untested rival hypothesis",
			"an adjudicatory disposition or summary — VERIQO does not name a prevailing party",
		},
		Authority:       "an authority distinct from the party that generated the proof objects",
		Owner:           "the Case Resolution Fabric",
		FailureState:    PhaseUnderQualification,
		ReplayReference: "timeline kind=case_resolved carries the established and unestablished claim lists",
	},
	{
		State: PhaseSuspended,
		Entry: "work has stopped for a stated cause",
		Exit:  "the cause is resolved and the case returns to the phase it can support",
		RequiredEvidence: []string{
			"a stated cause — suspension with no cause is indistinguishable from neglect",
		},
		BlockingEvidence: []string{
			"a legal hold still in force",
		},
		Authority:       "any actor with case authority; suspension is always permitted",
		Owner:           "the case analyst",
		FailureState:    PhaseClosed,
		ReplayReference: "timeline kind=case_suspended carries the cause at the tick it was raised",
	},
	{
		State: PhaseClosed,
		Entry: "the case is terminal for now",
		Exit:  "new evidence justifies reopening",
		RequiredEvidence: []string{
			"a stated reason for closure",
		},
		BlockingEvidence: []string{
			"a retention obligation or legal hold that forbids closing the record",
		},
		Authority:       "an actor with case-close authority",
		Owner:           "the case analyst",
		FailureState:    PhaseClosed,
		ReplayReference: "timeline kind=case_closed; the full prior record is retained",
	},
	{
		State: PhaseReopened,
		Entry: "a closed case has new evidence and a stated reason",
		Exit:  "the case re-enters evidence gathering or qualification",
		RequiredEvidence: []string{
			"a stated reason naming what is new",
		},
		BlockingEvidence: []string{
			"a reopening with no new evidence, which is a re-argument rather than a reopening",
		},
		Authority:       "an actor with reopen authority",
		Owner:           "the case analyst",
		FailureState:    PhaseClosed,
		ReplayReference: "timeline kind=case_reopened; the prior outcome is cleared but the record before it stands",
	},
}

// PhaseContracts returns the contract for every canonical phase.
func PhaseContracts() []PhaseContract { return append([]PhaseContract(nil), phaseContracts...) }

// ContractFor returns one phase's contract.
func ContractFor(p Phase) (PhaseContract, bool) {
	for _, c := range phaseContracts {
		if c.State == p {
			return c, true
		}
	}
	return PhaseContract{}, false
}

// Incomplete returns the attributes this contract leaves blank.
//
// A blank is the finding. A phase with no failure state fails open; a
// phase with no blocking evidence has not been thought about.
func (c PhaseContract) Incomplete() []string {
	var missing []string
	if strings.TrimSpace(c.Entry) == "" {
		missing = append(missing, "entry condition")
	}
	if strings.TrimSpace(c.Exit) == "" {
		missing = append(missing, "exit condition")
	}
	if len(c.RequiredEvidence) == 0 {
		missing = append(missing, "required evidence")
	}
	if len(c.BlockingEvidence) == 0 {
		missing = append(missing, "blocking evidence")
	}
	if strings.TrimSpace(c.Authority) == "" {
		missing = append(missing, "authority")
	}
	if strings.TrimSpace(c.Owner) == "" {
		missing = append(missing, "owner")
	}
	if strings.TrimSpace(string(c.FailureState)) == "" {
		missing = append(missing, "failure state")
	}
	if strings.TrimSpace(c.ReplayReference) == "" {
		missing = append(missing, "replay reference")
	}
	sort.Strings(missing)
	return missing
}

// ValidateContracts checks the whole set: every canonical phase has a
// contract, every contract is complete, and every failure state is a
// real phase.
func ValidateContracts() error {
	byPhase := map[Phase]bool{}
	for _, c := range phaseContracts {
		if byPhase[c.State] {
			return fmt.Errorf("casefabric: phase %s has more than one contract", c.State)
		}
		byPhase[c.State] = true

		if missing := c.Incomplete(); len(missing) > 0 {
			return fmt.Errorf("casefabric: phase %s has no %s", c.State, strings.Join(missing, ", "))
		}
		if _, ok := ContractFor(c.FailureState); !ok {
			return fmt.Errorf("casefabric: phase %s fails to %s, which is not a canonical phase", c.State, c.FailureState)
		}
	}
	for _, p := range Phases() {
		if !byPhase[p] {
			return fmt.Errorf("casefabric: phase %s has no contract", p)
		}
	}
	return nil
}

// RenderContracts writes the contract table.
func RenderContracts() string {
	var b strings.Builder
	for _, c := range phaseContracts {
		b.WriteString(fmt.Sprintf("=== %s ===\n", c.State))
		b.WriteString(fmt.Sprintf("  %-18s %s\n", "ENTRY", c.Entry))
		b.WriteString(fmt.Sprintf("  %-18s %s\n", "EXIT", c.Exit))
		for i, r := range c.RequiredEvidence {
			label := "REQUIRED EVIDENCE"
			if i > 0 {
				label = ""
			}
			b.WriteString(fmt.Sprintf("  %-18s %s\n", label, r))
		}
		for i, r := range c.BlockingEvidence {
			label := "BLOCKING EVIDENCE"
			if i > 0 {
				label = ""
			}
			b.WriteString(fmt.Sprintf("  %-18s %s\n", label, r))
		}
		b.WriteString(fmt.Sprintf("  %-18s %s\n", "AUTHORITY", c.Authority))
		b.WriteString(fmt.Sprintf("  %-18s %s\n", "OWNER", c.Owner))
		b.WriteString(fmt.Sprintf("  %-18s %s\n", "FAILURE STATE", c.FailureState))
		b.WriteString(fmt.Sprintf("  %-18s %s\n\n", "REPLAY", c.ReplayReference))
	}
	return b.String()
}
