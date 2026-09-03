// Package fref is the Forward–Reverse Execution Fabric: VERIQO's
// architecture contract stated as code rather than as a diagram.
//
// VERIQO runs in two directions over the same evidence.
//
// Forward is how most systems work — observations arrive, become
// evidence, become knowledge, are reasoned over, are trusted to some
// degree, become a finding, and found a decision:
//
//	OBSERVATION -> EVIDENCE -> KNOWLEDGE -> REASONING -> TRUST -> FINDING -> DECISION
//
// Reverse is what makes VERIQO a proof-oriented system rather than a
// detection engine. It starts from a decision or a claim somebody
// asserts, and asks what would have to be true:
//
//	DECISION/CLAIM -> PROOF OBLIGATIONS -> REQUIRED EVIDENCE -> EVIDENCE GAP
//	  -> CONTRADICTION -> QUALIFICATION -> NEXT BEST EVIDENCE
//
// Both directions existed in the repository, spread across the EQF, the
// intelligence layer and the workflow engine. What did not exist was a
// contract saying they are one architecture, in what order their stages
// must run, and what it means for the two directions to close over the
// same claim. That absence is what this package fills.
//
// It is a contract, not an engine. Every stage names the package that
// actually does the work, and the contract's job is to refuse an
// execution that skipped a stage, ran stages out of order, or claimed a
// closure the two directions do not support. Nothing here recomputes
// what pkg/qualification, pkg/proof or pkg/casefabric already decide.
package fref

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Direction is which way an execution runs.
type Direction string

const (
	Forward Direction = "FORWARD"
	Reverse Direction = "REVERSE"
)

// Stage is one position in either pipeline.
type Stage string

// Forward stages.
const (
	StageObservation Stage = "OBSERVATION"
	StageEvidence    Stage = "EVIDENCE"
	StageKnowledge   Stage = "KNOWLEDGE"
	StageReasoning   Stage = "REASONING"
	StageTrust       Stage = "TRUST"
	StageFinding     Stage = "FINDING"
	StageDecision    Stage = "DECISION"
)

// Reverse stages.
const (
	StageClaim            Stage = "CLAIM"
	StageProofObligations Stage = "PROOF_OBLIGATIONS"
	StageRequiredEvidence Stage = "REQUIRED_EVIDENCE"
	StageEvidenceGap      Stage = "EVIDENCE_GAP"
	StageContradiction    Stage = "CONTRADICTION"
	StageQualification    Stage = "QUALIFICATION"
	StageNextBestEvidence Stage = "NEXT_BEST_EVIDENCE"
)

var forwardOrder = []Stage{
	StageObservation, StageEvidence, StageKnowledge,
	StageReasoning, StageTrust, StageFinding, StageDecision,
}

var reverseOrder = []Stage{
	StageClaim, StageProofObligations, StageRequiredEvidence,
	StageEvidenceGap, StageContradiction, StageQualification, StageNextBestEvidence,
}

// Order returns the canonical stage order for a direction.
func Order(d Direction) []Stage {
	switch d {
	case Forward:
		return append([]Stage(nil), forwardOrder...)
	case Reverse:
		return append([]Stage(nil), reverseOrder...)
	default:
		return nil
	}
}

// Binding names the package that actually performs a stage, and what it
// is authoritative for.
//
// The bindings are the anti-duplication record: if a stage had no
// binding, this package would be describing work nothing does, and if
// two stages bound to the same authority for the same decision, that
// would be the duplicate-engine smell the architecture forbids.
type Binding struct {
	Stage Stage
	// Package is the import path that owns this stage.
	Package string
	// Authoritative states what that package decides, in one line.
	Authoritative string
	// FailClosed states what happens when the stage cannot complete.
	// Every stage must have an answer, and the answer is never
	// "continue with a default".
	FailClosed string
}

var bindings = []Binding{
	// Forward.
	{StageObservation, "veriqo/pkg/dataplatform/ingest",
		"what was observed, from which source, under which licence",
		"an unlicensed or unattributed observation is rejected at ingest, not down-weighted"},
	{StageEvidence, "veriqo/pkg/evidence/manifest",
		"the canonical evidence record, its versions and its custody chain",
		"evidence that cannot be pinned to a version and a content hash never enters the fabric"},
	{StageKnowledge, "veriqo/pkg/moat/kg",
		"entities, links and the knowledge graph over evidence",
		"an entity resolution below threshold stays unresolved rather than merging two parties"},
	{StageReasoning, "veriqo/pkg/moat/causal",
		"hypotheses and causal structure — proposals, never findings",
		"reasoning that cannot cite its inputs produces no hypothesis"},
	{StageTrust, "veriqo/pkg/core/trustcalc",
		"the trust standing of sources and the evidence built on them",
		"an unassessed source is UNKNOWN, never assumed independent"},
	{StageFinding, "veriqo/pkg/proof",
		"whether a sealed proof object is sufficient to found a finding",
		"an insufficient object founds no finding; it yields next-best evidence instead"},
	{StageDecision, "veriqo/pkg/proof",
		"the operational act taken on an authorized finding",
		"a decision without an authorized finding cannot be constructed"},

	// Reverse.
	{StageClaim, "veriqo/pkg/casefabric",
		"the proposition a case must establish, registered before it is proved",
		"a claim with no falsifiable proposition is refused at registration"},
	{StageProofObligations, "veriqo/pkg/qualification/reverseproof",
		"what would have to be shown for the claim to hold",
		"a requirement with no falsifying observation is not a test and is rejected"},
	{StageRequiredEvidence, "veriqo/pkg/qualification/reverseproof",
		"the evidence each obligation calls for",
		"an obligation referencing an unknown condition fails the build of the set"},
	{StageEvidenceGap, "veriqo/pkg/qualification/reverseproof",
		"obtained, observed-absent, unobtainable and unattempted, kept apart",
		"'we never looked' is never reported as 'it was not there'"},
	{StageContradiction, "veriqo/pkg/insurance/contradiction",
		"conflicts within and against the evidence set",
		"an unresolved material contradiction defeats sufficiency rather than being averaged away"},
	{StageQualification, "veriqo/pkg/qualification/state",
		"the qualification state of the claim",
		"there is no PROVEN state to reach; the type system has none"},
	{StageNextBestEvidence, "veriqo/pkg/qualification/nextbest",
		"what is worth obtaining next, after hard rights filters",
		"a rights-denied candidate is excluded, never scored low"},
}

// Bindings returns the stage-to-package contract.
func Bindings() []Binding { return append([]Binding(nil), bindings...) }

// BindingFor returns the binding for a stage.
func BindingFor(s Stage) (Binding, bool) {
	for _, b := range bindings {
		if b.Stage == s {
			return b, true
		}
	}
	return Binding{}, false
}

var (
	ErrUnknownStage    = errors.New("fref: the stage does not belong to this direction")
	ErrOutOfOrder      = errors.New("fref: stages must complete in the canonical order")
	ErrStageRepeated   = errors.New("fref: a stage cannot complete twice in one execution")
	ErrIncomplete      = errors.New("fref: the execution did not reach its terminal stage")
	ErrNoSubject       = errors.New("fref: an execution requires a subject")
	ErrStageNotReached = errors.New("fref: the stage was never reached")
	ErrNoClosure       = errors.New("fref: the two directions do not close over the same claim")
)

// StageRecord is one completed stage.
type StageRecord struct {
	Stage Stage
	// Package is the package that actually ran, recorded so an execution
	// can be checked against the contract rather than trusted to have
	// followed it.
	Package string
	// Tick is the logical time of completion.
	Tick uint64
	// OutputHash pins whatever the stage produced.
	OutputHash string
	// Note carries the stage's own summary.
	Note string
}

// Execution is one run in one direction.
//
// The zero Execution is unusable; NewExecution is the constructor. The
// point of the type is Complete: it is the only way to add a stage, and
// it refuses anything the contract forbids.
type Execution struct {
	direction Direction
	// Subject is the claim, proposition or case the run is about.
	subject string
	records []StageRecord
	seen    map[Stage]bool
}

// NewExecution starts a run.
func NewExecution(d Direction, subject string) (*Execution, error) {
	if Order(d) == nil {
		return nil, fmt.Errorf("fref: unknown direction %q", d)
	}
	if strings.TrimSpace(subject) == "" {
		return nil, ErrNoSubject
	}
	return &Execution{direction: d, subject: subject, seen: map[Stage]bool{}}, nil
}

func (e *Execution) Direction() Direction { return e.direction }
func (e *Execution) Subject() string      { return e.subject }

// Records returns a copy of the completed stages.
func (e *Execution) Records() []StageRecord { return append([]StageRecord(nil), e.records...) }

// Complete records a stage.
//
// It enforces three things: the stage belongs to this direction, it has
// not already run, and every earlier stage in the canonical order has
// run. That last check is the whole contract — it is what makes
// "TRUST comes before FINDING" a property of the system rather than a
// convention people follow when they remember to.
func (e *Execution) Complete(s Stage, pkg string, tick uint64, outputHash, note string) error {
	order := Order(e.direction)
	idx := -1
	for i, st := range order {
		if st == s {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %s is not a %s stage", ErrUnknownStage, s, e.direction)
	}
	if e.seen[s] {
		return fmt.Errorf("%w: %s", ErrStageRepeated, s)
	}
	for _, earlier := range order[:idx] {
		if !e.seen[earlier] {
			return fmt.Errorf("%w: %s cannot complete before %s", ErrOutOfOrder, s, earlier)
		}
	}

	e.seen[s] = true
	e.records = append(e.records, StageRecord{
		Stage: s, Package: pkg, Tick: tick, OutputHash: outputHash, Note: note,
	})
	return nil
}

// Reached reports whether a stage completed.
func (e *Execution) Reached(s Stage) bool { return e.seen[s] }

// Terminal returns the direction's final stage.
func (e *Execution) Terminal() Stage {
	order := Order(e.direction)
	return order[len(order)-1]
}

// FurthestStage returns the last stage completed, and false for a run
// that has completed none.
func (e *Execution) FurthestStage() (Stage, bool) {
	if len(e.records) == 0 {
		return "", false
	}
	return e.records[len(e.records)-1].Stage, true
}

// OutputOf returns the pinned output of a completed stage.
func (e *Execution) OutputOf(s Stage) (string, error) {
	for _, r := range e.records {
		if r.Stage == s {
			return r.OutputHash, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrStageNotReached, s)
}

// VerifyAgainstContract checks that every completed stage ran in the
// package the contract binds it to.
//
// A stage that ran somewhere else is not necessarily wrong — but it is a
// second implementation of a stage that already has an owner, which is
// the duplicate-engine failure the architecture forbids, and it should
// surface here rather than in a review six months later.
func (e *Execution) VerifyAgainstContract() error {
	var drift []string
	for _, r := range e.records {
		b, ok := BindingFor(r.Stage)
		if !ok {
			drift = append(drift, fmt.Sprintf("%s: no contract binding", r.Stage))
			continue
		}
		if r.Package != "" && r.Package != b.Package {
			drift = append(drift, fmt.Sprintf("%s: ran in %s, contract binds it to %s", r.Stage, r.Package, b.Package))
		}
	}
	if len(drift) > 0 {
		sort.Strings(drift)
		return fmt.Errorf("fref: execution drifted from the contract: %s", strings.Join(drift, "; "))
	}
	return nil
}

// RequireComplete refuses a run that stopped short of its terminal stage.
//
// A forward run is complete at DECISION. A reverse run is complete at
// NEXT_BEST_EVIDENCE — note that this makes "we know what to get next"
// the successful end of a reverse run, not a failure. A reverse run that
// stops at QUALIFICATION has diagnosed the gap without saying what to do
// about it, which is exactly the half-finished analysis the reverse
// direction exists to prevent.
func (e *Execution) RequireComplete() error {
	term := e.Terminal()
	if !e.seen[term] {
		reached, _ := e.FurthestStage()
		return fmt.Errorf("%w: reached %s, terminal stage is %s", ErrIncomplete, reached, term)
	}
	return nil
}

// --- Closure ---------------------------------------------------------

// Closure is the check that the two directions are one architecture
// rather than two pipelines that happen to share a repository.
//
// It holds when a forward run and a reverse run over the same subject
// agree on the evidence: every evidence output the forward run relied on
// is one the reverse run's required-evidence stage actually called for.
// Forward evidence the reverse direction never required means the
// finding rests on something no proof obligation asked for — which is
// how a system ends up "supported" by evidence nobody can say why it
// needed.
type Closure struct {
	Subject string
	// ForwardEvidence and RequiredEvidence are the pinned hashes from
	// each direction.
	ForwardEvidence  []string
	RequiredEvidence []string
	// Unrequired is forward evidence no proof obligation called for.
	Unrequired []string
	// Unmet is required evidence the forward run never used.
	Unmet []string
	Holds bool
}

// Close compares a forward and a reverse execution over one subject.
//
// forwardEvidence and requiredEvidence are supplied by the caller rather
// than dug out of the stage records, because what counts as "the
// evidence a finding rests on" is a question pkg/proof answers (its
// EvidenceSet) and this package must not answer a second time.
func Close(fwd, rev *Execution, forwardEvidence, requiredEvidence []string) (Closure, error) {
	if fwd.direction != Forward || rev.direction != Reverse {
		return Closure{}, errors.New("fref: closure needs one forward and one reverse execution")
	}
	if fwd.subject != rev.subject {
		return Closure{}, fmt.Errorf("%w: forward is about %q, reverse about %q", ErrNoClosure, fwd.subject, rev.subject)
	}
	if err := fwd.RequireComplete(); err != nil {
		return Closure{}, fmt.Errorf("forward: %w", err)
	}
	if !rev.Reached(StageRequiredEvidence) {
		return Closure{}, fmt.Errorf("reverse: %w: %s", ErrStageNotReached, StageRequiredEvidence)
	}

	required := map[string]bool{}
	for _, h := range requiredEvidence {
		required[h] = true
	}
	used := map[string]bool{}
	for _, h := range forwardEvidence {
		used[h] = true
	}

	c := Closure{
		Subject:          fwd.subject,
		ForwardEvidence:  sortedCopy(forwardEvidence),
		RequiredEvidence: sortedCopy(requiredEvidence),
	}
	for _, h := range c.ForwardEvidence {
		if !required[h] {
			c.Unrequired = append(c.Unrequired, h)
		}
	}
	for _, h := range c.RequiredEvidence {
		if !used[h] {
			c.Unmet = append(c.Unmet, h)
		}
	}
	c.Holds = len(c.Unrequired) == 0 && len(c.Unmet) == 0
	return c, nil
}

// Explain states why a closure does or does not hold, for a reader who
// will act on it.
func (c Closure) Explain() string {
	if c.Holds {
		return fmt.Sprintf("Closure holds for %q: every piece of evidence the finding rests on was called for by a proof obligation, and every obligation was met.", c.Subject)
	}
	var parts []string
	if len(c.Unrequired) > 0 {
		parts = append(parts, fmt.Sprintf("%d piece(s) of evidence support the finding that no proof obligation required: %s",
			len(c.Unrequired), strings.Join(c.Unrequired, ", ")))
	}
	if len(c.Unmet) > 0 {
		parts = append(parts, fmt.Sprintf("%d proof obligation(s) were never met by the forward run: %s",
			len(c.Unmet), strings.Join(c.Unmet, ", ")))
	}
	return fmt.Sprintf("Closure fails for %q: %s.", c.Subject, strings.Join(parts, "; "))
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}
