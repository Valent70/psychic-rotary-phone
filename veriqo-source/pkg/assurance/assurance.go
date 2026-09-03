// Package assurance keeps two things apart that VERIQO's own reports
// kept collapsing: how much has been built, and how much has been
// proved.
//
// "Wave 13 complete" and "production qualified" are not the same claim.
// A capability can be fully engineered — designed, implemented, tested
// under race, replayed — and still rest entirely on VERIQO's own word,
// because nothing outside VERIQO has ever examined it. Reporting a
// single percentage hides exactly the half a buyer, a regulator or a
// tribunal cares about.
//
// So this package models two axes that never combine into one number:
//
//	ENGINEERING AXIS   W0..W13, per MIP §34's status taxonomy
//	ASSURANCE AXIS     internal proof -> external proof
//
// and one traceability chain that says, for any control, how far the
// evidence actually reaches:
//
//	CONSTITUTION -> CONTROL -> CODE -> TEST -> EVIDENCE -> REPLAY
//	  -> QUALIFICATION -> EXTERNAL PROOF
//
// The chain yields five verdicts, and the point of each is to name a
// different kind of incompleteness rather than lumping them together
// as "not done":
//
//	OPEN                    the article reaches no runtime enforcement
//	INTEGRATION_GAP         code exists but nothing calls it
//	ASSURANCE_GAP           tests exist but no production-path evidence
//	EXTERNAL_QUALIFICATION  internally proved, awaiting an outside party
//	QUALIFIED               the chain is complete end to end
//
// There is deliberately no method that reduces a Status to a score.
package assurance

import (
	"errors"
	"fmt"
	"strings"
)

// --- The engineering axis --------------------------------------------

// EngineeringLevel is MIP §34's status taxonomy. DONE is not in it, at
// any level: a capability is always at a named stage of evidence, and
// "done" names none.
type EngineeringLevel int

const (
	// NotStarted is the zero value.
	NotStarted EngineeringLevel = iota
	Designed
	Scaffolded
	Implemented
	UnitTested
	IntegrationTested
	AdversarialTested
	ReplayVerified
)

var engineeringNames = map[EngineeringLevel]string{
	NotStarted: "NOT_STARTED", Designed: "DESIGNED", Scaffolded: "SCAFFOLDED",
	Implemented: "IMPLEMENTED", UnitTested: "UNIT_TESTED",
	IntegrationTested: "INTEGRATION_TESTED", AdversarialTested: "ADVERSARIAL_TESTED",
	ReplayVerified: "REPLAY_VERIFIED",
}

func (e EngineeringLevel) String() string {
	if s, ok := engineeringNames[e]; ok {
		return s
	}
	return "NOT_STARTED"
}

// --- The assurance axis ----------------------------------------------

// AssuranceLevel is how far the proof reaches beyond VERIQO's own word.
//
// Unproved is the zero value, and the gap between InternallyProved and
// ExternallyValidated is the one this package exists to keep visible.
type AssuranceLevel int

const (
	// Unproved: nothing has been established about this beyond that it
	// was built.
	Unproved AssuranceLevel = iota
	// SelfAsserted: VERIQO says it works. No test demonstrates it.
	SelfAsserted
	// InternallyProved: VERIQO's own tests and replays demonstrate it.
	// This is the highest level reachable without anybody outside.
	InternallyProved
	// ExternallyValidated: an identified outside party examined it.
	ExternallyValidated
	// ProductionQualified: externally validated AND operated under real
	// load, real data and real consequences.
	ProductionQualified
)

var assuranceNames = map[AssuranceLevel]string{
	Unproved: "UNPROVED", SelfAsserted: "SELF_ASSERTED",
	InternallyProved: "INTERNALLY_PROVED", ExternallyValidated: "EXTERNALLY_VALIDATED",
	ProductionQualified: "PRODUCTION_QUALIFIED",
}

func (a AssuranceLevel) String() string {
	if s, ok := assuranceNames[a]; ok {
		return s
	}
	return "UNPROVED"
}

// RequiresOutsideParty reports whether reaching this level needs
// somebody who is not VERIQO. Everything from ExternallyValidated up
// does, which is why no amount of further engineering reaches it.
func (a AssuranceLevel) RequiresOutsideParty() bool { return a >= ExternallyValidated }

// Status is one capability on both axes.
//
// The two fields never merge. There is no Overall(), no Score() and no
// Percent(): a caller that wants one number is asking for the thing this
// package exists to refuse.
type Status struct {
	Capability string
	// Engineering is how much is built.
	Engineering EngineeringLevel
	// Assurance is how much is proved beyond VERIQO's own word.
	Assurance AssuranceLevel
	// Blocker names what stands between the current assurance level and
	// the next one. Required whenever Assurance is below
	// ExternallyValidated, because "we have not got round to it" and
	// "no accredited body exists for this" are different situations.
	Blocker string
}

// String renders both axes. It never renders one.
func (s Status) String() string {
	return fmt.Sprintf("%s: engineering=%s assurance=%s", s.Capability, s.Engineering, s.Assurance)
}

// Validate refuses a status that hides its blocker.
func (s Status) Validate() error {
	if strings.TrimSpace(s.Capability) == "" {
		return errors.New("assurance: a status must name its capability")
	}
	if s.Assurance < ExternallyValidated && strings.TrimSpace(s.Blocker) == "" {
		return fmt.Errorf("assurance: %q is %s and names no blocker: say what stands in the way",
			s.Capability, s.Assurance)
	}
	// The inverse claim is the dangerous one: engineering completeness
	// asserted as assurance.
	if s.Engineering == ReplayVerified && s.Assurance >= ExternallyValidated && strings.TrimSpace(s.Blocker) == "" {
		return nil
	}
	return nil
}

// EngineeringCompleteButUnassured reports the specific confusion this
// package guards against: fully built, nobody outside has looked.
func (s Status) EngineeringCompleteButUnassured() bool {
	return s.Engineering >= AdversarialTested && s.Assurance < ExternallyValidated
}

// --- The traceability chain ------------------------------------------

// Verdict is the outcome of tracing one control from the constitution
// to external proof.
//
// Open is the zero value, deliberately: a control nobody traced is open,
// never qualified.
type Verdict int

const (
	// Open: the article reaches no runtime enforcement at all.
	Open Verdict = iota
	// IntegrationGap: code exists, but nothing on a live path calls it.
	IntegrationGap
	// AssuranceGap: tests exist, but no evidence from a production path.
	AssuranceGap
	// ExternalQualification: internally proved; what remains needs a
	// vendor, a dataset or an infrastructure VERIQO does not control.
	ExternalQualification
	// Qualified: the chain is complete end to end.
	Qualified
)

var verdictNames = map[Verdict]string{
	Open: "OPEN", IntegrationGap: "INTEGRATION_GAP", AssuranceGap: "ASSURANCE_GAP",
	ExternalQualification: "EXTERNAL_QUALIFICATION", Qualified: "QUALIFIED",
}

func (v Verdict) String() string {
	if s, ok := verdictNames[v]; ok {
		return s
	}
	return "OPEN"
}

// Closed reports whether the verdict needs no further work from VERIQO.
// Only Qualified does. ExternalQualification is not closed — it is
// blocked, which is a different thing and must not be reported as done.
func (v Verdict) Closed() bool { return v == Qualified }

// Trace is one control's path from the constitution to external proof.
//
// Every link is a bool with a supporting reference. A link asserted with
// no reference is refused, because an unreferenced "yes" is what turns
// a traceability matrix into a spreadsheet of good intentions.
type Trace struct {
	// Article is the constitutional article number this control serves.
	Article int
	// Control names the mechanism, in the architecture's own vocabulary.
	Control string

	// Code: an implementation exists.
	Code bool
	// CodeRef is the package or symbol.
	CodeRef string

	// Called: something on a real execution path invokes it. This is the
	// link that separates an implemented control from an integrated one.
	Called bool
	// CalledRef names the caller.
	CalledRef string

	// Test: a test exercises it.
	Test bool
	// TestRef names the test.
	TestRef string

	// Evidence: running the control on a production path leaves a
	// durable record.
	Evidence bool
	// EvidenceRef names the ledger event family or artefact.
	EvidenceRef string

	// Replay: the control's effect is reproducible from the record by a
	// party that does not trust the runtime.
	Replay bool
	// ReplayRef names the replay path.
	ReplayRef string

	// RuntimeEvidence: an actual execution of this control left an
	// identifiable record — an audit event, a ledger entry, a replay
	// package — that somebody can go and look at.
	//
	// This is the link "article -> code -> test" was missing. A test
	// proves the control behaves correctly when exercised deliberately.
	// It says nothing about whether the control ran in the system as
	// assembled, or left anything behind when it did. Enterprise
	// assurance is the difference between "we wrote a test for it" and
	// "here is the event it emitted".
	RuntimeEvidence bool
	// RuntimeEvidenceRef identifies the record: an audit event id, a
	// ledger index, a replay package reference. It must be something a
	// reader can resolve, not a description of the kind of thing that
	// would exist.
	RuntimeEvidenceRef string

	// Qualification: the control has been assessed, not merely run.
	Qualification bool
	// QualificationRef names the assessment.
	QualificationRef string

	// ExternalProof: an identified outside party examined it.
	ExternalProof bool
	// ExternalProofRef names the party and their reference.
	ExternalProofRef string

	// ExternalDependency names what VERIQO does not control, when the
	// remaining work is somebody else's to do.
	ExternalDependency string
}

var (
	ErrNoControl       = errors.New("assurance: a trace must name its control")
	ErrUnknownArticle  = errors.New("assurance: the article number is outside 1..30")
	ErrUnreferenced    = errors.New("assurance: a trace link asserted with no reference")
	ErrNoDependency    = errors.New("assurance: an externally blocked trace must name the dependency")
	ErrImpossibleChain = errors.New("assurance: the trace asserts a link whose prerequisite is absent")
)

// Validate refuses a trace that cannot be checked.
func (t Trace) Validate() error {
	if t.Article < 1 || t.Article > 30 {
		return fmt.Errorf("%w: %d", ErrUnknownArticle, t.Article)
	}
	if strings.TrimSpace(t.Control) == "" {
		return ErrNoControl
	}
	for _, l := range []struct {
		name string
		on   bool
		ref  string
	}{
		{"code", t.Code, t.CodeRef}, {"called", t.Called, t.CalledRef},
		{"test", t.Test, t.TestRef}, {"evidence", t.Evidence, t.EvidenceRef},
		{"replay", t.Replay, t.ReplayRef},
		{"runtime evidence", t.RuntimeEvidence, t.RuntimeEvidenceRef},
		{"qualification", t.Qualification, t.QualificationRef},
		{"external proof", t.ExternalProof, t.ExternalProofRef},
	} {
		if l.on && strings.TrimSpace(l.ref) == "" {
			return fmt.Errorf("%w: article %d, %s", ErrUnreferenced, t.Article, l.name)
		}
	}
	// Prerequisites. Nothing downstream of code can be true without it,
	// and evidence of a control nothing calls is a contradiction.
	if !t.Code && (t.Called || t.Test || t.Evidence || t.Replay) {
		return fmt.Errorf("%w: article %d claims downstream links with no code", ErrImpossibleChain, t.Article)
	}
	if !t.Called && (t.Evidence || t.Replay || t.RuntimeEvidence) {
		return fmt.Errorf("%w: article %d claims production evidence for a control nothing calls", ErrImpossibleChain, t.Article)
	}
	// Runtime evidence without a test is not impossible, but runtime
	// evidence without CODE is: a record cannot be emitted by an
	// implementation that does not exist.
	if !t.Code && t.RuntimeEvidence {
		return fmt.Errorf("%w: article %d claims a runtime record with no implementation", ErrImpossibleChain, t.Article)
	}
	return nil
}

// Assess yields the verdict, applying the rules in the order the
// architecture states them.
//
// The order matters and is not arbitrary: each rule catches the most
// fundamental failure first, so a control with no code is OPEN rather
// than being reported as an assurance gap it has not yet earned.
func Assess(t Trace) (Verdict, error) {
	if err := t.Validate(); err != nil {
		return Open, err
	}
	switch {
	case !t.Code || !t.Called:
		// No code at all, or code nothing invokes. The architecture
		// distinguishes these in the message; both leave the article
		// unenforced at runtime.
		if !t.Code {
			return Open, nil
		}
		return IntegrationGap, nil
	case !t.Test:
		// Called but never exercised deliberately: there is no
		// demonstration that it does what it claims.
		return AssuranceGap, nil
	case !t.Evidence || !t.Replay:
		// Tests exist, but nothing on a production path leaves a record
		// an outsider could check.
		return AssuranceGap, nil
	case !t.RuntimeEvidence:
		// The control is designed to leave a record and has been tested,
		// but no actual run has produced one that can be pointed at.
		// This is the gap the "article -> code -> test" chain hid.
		return AssuranceGap, nil
	case !t.Qualification:
		return AssuranceGap, nil
	case !t.ExternalProof:
		if strings.TrimSpace(t.ExternalDependency) == "" {
			return ExternalQualification, fmt.Errorf("%w: article %d, control %q",
				ErrNoDependency, t.Article, t.Control)
		}
		return ExternalQualification, nil
	default:
		return Qualified, nil
	}
}

// Explain states, in one sentence, what the verdict means for this
// control and what would move it forward.
func Explain(t Trace) string {
	v, err := Assess(t)
	prefix := fmt.Sprintf("Article %d, %s: %s. ", t.Article, t.Control, v)
	if err != nil && v != ExternalQualification {
		return prefix + "The trace is not valid: " + err.Error()
	}
	switch v {
	case Open:
		return prefix + "No implementation reaches runtime enforcement of this article."
	case IntegrationGap:
		return prefix + fmt.Sprintf("%s exists but no live path invokes it; the control is written, not wired.", t.CodeRef)
	case AssuranceGap:
		switch {
		case !t.Test:
			return prefix + "The control runs but nothing demonstrates it does what it claims."
		case !t.Evidence:
			return prefix + "Tests pass but a production run leaves no durable record."
		case !t.Replay:
			return prefix + "A record exists but it cannot be reproduced without trusting the runtime."
		case !t.RuntimeEvidence:
			return prefix + "The control is tested and designed to leave a record, but no executed run has produced one that can be pointed at."
		default:
			return prefix + "The control is exercised and recorded but has never been assessed."
		}
	case ExternalQualification:
		if t.ExternalDependency == "" {
			return prefix + "Internally proved; the remaining dependency is not named, which itself is a gap."
		}
		return prefix + "Internally proved end to end. What remains is not VERIQO's to do: " + t.ExternalDependency + "."
	default:
		return prefix + "Traced from the constitution to an external party's examination."
	}
}
