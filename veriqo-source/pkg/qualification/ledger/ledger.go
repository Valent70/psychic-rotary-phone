// Package ledger is the VERIQO qualification evidence ledger.
//
// # The paradigm this implements
//
// The review asked for a change of question. Not:
//
//	How do we close all the gaps?
//
// but:
//
//	What evidence is required to legitimately promote each control to
//	the next assurance level?
//
// That reframing only means something if the evidence is recorded in a
// form somebody else can check. A test that passed and left nothing
// behind is a claim about the past made by the party who benefits from
// it. So every qualification result enters this ledger carrying, as the
// review specified:
//
//	Test -> Execution -> Environment -> Input hash -> Output hash
//	     -> Tool/version -> Result -> Evidence -> Signature
//
// # What this package refuses
//
// A PASS with no evidence artefact. A result whose environment is not
// stated. A claim of external validation with no named validator. Each
// of those is a way for "we ran a test" to be read as "an outside party
// confirmed it", which is the specific confusion the assurance ladder
// exists to prevent.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"

	"veriqo/pkg/evidence/quality"
)

// Level is the assurance ladder. The review's model, in order:
//
//	IMPLEMENTED -> INTEGRATED -> ASSURED -> QUALIFIED
//	            -> EXTERNALLY VALIDATED -> PRODUCTION PROVEN
//
// It is deliberately not TODO -> DONE. Each step names a different kind
// of evidence, and the last three cannot be reached by VERIQO working
// alone.
type Level int

const (
	// Implemented: the capability exists in code and builds.
	Implemented Level = iota
	// Integrated: it composes with the rest of the system on a live
	// path that leaves a record.
	Integrated
	// Assured: it has been deliberately exercised, including its
	// refusals, and the exercise left evidence somebody could check.
	// This is VERIQO's internal ceiling.
	Assured
	// Qualified: an outside party examined the control and stated that
	// the procedure was correctly applied.
	//
	// The name matches pkg/assurance, where QUALIFIED has meant
	// "an outside party examined it" since the matrix was written. Two
	// ladders using the same word for different things would be worse
	// than either ladder alone.
	Qualified
	// ExternallyValidated: an identified party who is not VERIQO
	// attempted to falsify the control and reported what they found.
	// Stronger than Qualified: examination is not the same as attack.
	ExternallyValidated
	// ProductionProven: it has operated under real load with real
	// consequences and the record survived audit.
	ProductionProven
)

var levelNames = map[Level]string{
	Implemented:         "IMPLEMENTED",
	Integrated:          "INTEGRATED",
	Qualified:           "QUALIFIED",
	Assured:             "ASSURED",
	ExternallyValidated: "EXTERNALLY_VALIDATED",
	ProductionProven:    "PRODUCTION_PROVEN",
}

func (l Level) String() string {
	if n, ok := levelNames[l]; ok {
		return n
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

// Levels returns the ladder in order.
func Levels() []Level {
	return []Level{Implemented, Integrated, Assured, Qualified, ExternallyValidated, ProductionProven}
}

// RequiresOutsideParty reports whether a level is unreachable by VERIQO
// working alone. This is the golden rule the review asked for, as a
// function rather than a paragraph.
//
// The line sits at Qualified, not at ExternallyValidated, because
// QUALIFIED already means "an outside party examined it" everywhere
// else in this repository. Moving the line up to make more levels
// self-reachable would be the exact inflation this ladder exists to
// prevent.
func (l Level) RequiresOutsideParty() bool { return l >= Qualified }

// InternalCeiling is the highest level VERIQO can reach by itself.
func InternalCeiling() Level { return Assured }

// Boundary is the three-way distinction the review asked to be made
// unmistakable for investors, customers and regulators.
type Boundary string

const (
	// SelfTested: VERIQO wrote the test, ran the test, and reports the
	// result. This is the honest description of almost everything in
	// the repository.
	SelfTested Boundary = "VERIQO_SELF_TESTED"
	// Proved: the property holds by construction -- a type constraint,
	// a single constructor, an invariant the compiler enforces --
	// rather than because a test happened to pass. Stronger than
	// self-tested because it does not depend on the test's coverage.
	Proved Boundary = "VERIQO_PROVED"
	// ExternallyValidated: an identified party who is not VERIQO
	// examined it. Nothing in VERIQO carries this yet.
	Validated Boundary = "VERIQO_EXTERNALLY_VALIDATED"
)

// Boundaries returns the three, weakest first.
func Boundaries() []Boundary { return []Boundary{SelfTested, Proved, Validated} }

// Meaning states what a boundary does and does not license.
func (b Boundary) Meaning() string {
	switch b {
	case Proved:
		return "holds by construction -- a type constraint or single constructor -- not because a test passed; " +
			"still VERIQO's own reasoning about VERIQO's own code"
	case Validated:
		return "an identified party who is not VERIQO examined it and stated a conclusion"
	default:
		return "VERIQO wrote the test, ran it, and reports the result"
	}
}

// RequiresValidator reports whether the boundary obliges a named
// outside party.
func (b Boundary) RequiresValidator() bool { return b == Validated }

// Result is the outcome of one qualification run.
type Result string

const (
	Pass    Result = "PASS"
	Fail    Result = "FAIL"
	Refused Result = "REFUSED" // the control declined to act; safe, not proof of capability
	Skipped Result = "SKIPPED"
)

var (
	ErrNoControl     = errors.New("ledger: an entry must name the control it qualifies")
	ErrNoTest        = errors.New("ledger: an entry must name the test that produced it")
	ErrNoExecution   = errors.New("ledger: an entry must carry an execution id")
	ErrNoEnvironment = errors.New("ledger: an entry must state the environment it ran in")
	ErrNoTool        = errors.New("ledger: an entry must name the tool and version that ran it")
	ErrNoEvidence    = errors.New("ledger: a PASS must point at an evidence artefact")
	ErrNoValidator   = errors.New("ledger: EXTERNALLY_VALIDATED requires a named validator who is not VERIQO")
	ErrSelfValidator = errors.New("ledger: VERIQO cannot be its own external validator")
	ErrLevelBoundary = errors.New("ledger: the claimed level requires an outside party")
	ErrChainBroken   = errors.New("ledger: the entry chain does not verify")

	ErrEvidenceInsufficient = errors.New("ledger: a PASS cannot rest on evidence the assessment found wanting")
	ErrEvidenceUnassessed   = errors.New("ledger: a level that needs an outside party needs its evidence assessed first")
	ErrLimitsDropped        = errors.New("ledger: the evidence assessment stated limits the entry does not carry")
)

// Entry is one qualification result, with everything needed to argue
// about it without re-running it.
type Entry struct {
	// ControlID is what was qualified: an article number, a capability
	// name, an invariant id.
	ControlID string
	// Test names the test that produced the result.
	Test string
	// ExecutionID identifies this run. Two runs of the same test are
	// two entries, because "it passed once" and "it passes" are
	// different claims.
	ExecutionID string
	// Environment states where it ran, in enough detail that a
	// difference in outcome elsewhere is explicable.
	Environment string
	// InputHash and OutputHash pin what went in and what came out.
	InputHash  string
	OutputHash string
	// Tool and ToolVersion name what ran it.
	Tool        string
	ToolVersion string
	// Result is the outcome.
	Result Result
	// Evidence points at the artefact a reader can fetch.
	Evidence string
	// Level is the assurance level this entry supports.
	Level Level
	// Boundary is who is standing behind it.
	Boundary Boundary
	// Validator names the outside party, for Validated only.
	Validator string
	// Limitations states what this entry does NOT establish. An entry
	// with no limitations is refused for a PASS below, because every
	// real qualification has a boundary and one that names none has
	// not looked for it.
	Limitations []string
	// EvidenceQuality is the nine-attribute assessment of the artefact
	// named in Evidence, when one has been made.
	//
	// It is a pointer because "no assessment" and "an assessment that
	// found nothing" are different states, and a value type cannot tell
	// them apart. The rules below are what make it more than a
	// decoration: an assessment that came out INSUFFICIENT cannot
	// underwrite a PASS, an assessment that came out
	// SUPPORTS_WITH_LIMITS must have its limits repeated in the entry's
	// own Limitations, and a level that requires an outside party
	// requires an assessment to exist at all.
	//
	// The last rule is the one that connects the two halves of the
	// system: VERIQO can reach ASSURED on its own, and the step above
	// it is where somebody else looks at the evidence. Letting that
	// step be taken over evidence nobody assessed would make the
	// boundary a label.
	EvidenceQuality *quality.Assessment

	// Note is free text; nothing reads it.
	Note string

	// hash and prevHash are set by Append.
	hash     string
	prevHash string
}

// Hash returns the entry's content hash, empty until appended.
func (e Entry) Hash() string { return e.hash }

// PrevHash returns the previous entry's hash.
func (e Entry) PrevHash() string { return e.prevHash }

// Validate refuses an entry that cannot support the claim it makes.
func (e Entry) Validate() error {
	if strings.TrimSpace(e.ControlID) == "" {
		return ErrNoControl
	}
	if strings.TrimSpace(e.Test) == "" {
		return ErrNoTest
	}
	if strings.TrimSpace(e.ExecutionID) == "" {
		return ErrNoExecution
	}
	if strings.TrimSpace(e.Environment) == "" {
		return ErrNoEnvironment
	}
	if strings.TrimSpace(e.Tool) == "" || strings.TrimSpace(e.ToolVersion) == "" {
		return ErrNoTool
	}
	switch e.Result {
	case Pass, Fail, Refused, Skipped:
	default:
		return fmt.Errorf("ledger: %q is not a result", e.Result)
	}
	// A PASS with nothing to point at is an assertion.
	if e.Result == Pass {
		if strings.TrimSpace(e.Evidence) == "" {
			return fmt.Errorf("%w: control %s", ErrNoEvidence, e.ControlID)
		}
		if len(e.Limitations) == 0 {
			return fmt.Errorf("ledger: the PASS for control %s states no limitations; "+
				"every qualification has a boundary, and one that names none has not looked for it", e.ControlID)
		}
	}
	// The boundary and the level must agree, and neither may be
	// claimed without the party it implies.
	if e.Boundary.RequiresValidator() && strings.TrimSpace(e.Validator) == "" {
		return fmt.Errorf("%w: control %s", ErrNoValidator, e.ControlID)
	}
	if !e.Boundary.RequiresValidator() && strings.TrimSpace(e.Validator) != "" {
		return fmt.Errorf("ledger: control %s names a validator %q but claims only %s",
			e.ControlID, e.Validator, e.Boundary)
	}
	if strings.TrimSpace(e.Validator) != "" && looksLikeVeriqo(e.Validator) {
		return fmt.Errorf("%w: %q", ErrSelfValidator, e.Validator)
	}
	// A level that needs an outside party may only be claimed at the
	// boundary that names one. This is the single check that stops
	// "we ran the test" becoming "it is qualified".
	if e.Level.RequiresOutsideParty() && e.Boundary != Validated {
		return fmt.Errorf("%w: control %s claims %s at boundary %s",
			ErrLevelBoundary, e.ControlID, e.Level, e.Boundary)
	}
	return e.validateEvidenceQuality()
}

// validateEvidenceQuality is the join between the qualification ladder
// and the nine-attribute evidence assessment.
//
// Without it the two would be parallel vocabularies: one saying how
// far up the ladder a control has climbed, the other saying how good
// the evidence is, and nothing requiring the second to constrain the
// first. That is how a system ends up recording QUALIFIED against an
// artefact whose independence was never assessed.
func (e Entry) validateEvidenceQuality() error {
	if e.EvidenceQuality == nil {
		// An assessment is optional up to the internal ceiling: the
		// entry still has to point at an artefact and state its
		// limitations. Above the ceiling it is not optional, because
		// that is the step where somebody outside is supposed to have
		// looked at the evidence rather than at the claim.
		if e.Level.RequiresOutsideParty() {
			return fmt.Errorf("%w: control %s claims %s with no evidence quality assessment",
				ErrEvidenceUnassessed, e.ControlID, e.Level)
		}
		return nil
	}
	decision, reason, err := e.EvidenceQuality.Decide()
	if err != nil {
		return fmt.Errorf("ledger: control %s carries an invalid evidence assessment: %w", e.ControlID, err)
	}
	switch decision {
	case quality.Insufficient:
		if e.Result == Pass {
			return fmt.Errorf("%w: control %s is a PASS over evidence assessed INSUFFICIENT (%s)",
				ErrEvidenceInsufficient, e.ControlID, reason)
		}
	case quality.Unassessable:
		// Unassessable is not a bad grade; it is unfinished work. It
		// may accompany any result up to the internal ceiling, and
		// nothing above it.
		if e.Level.RequiresOutsideParty() {
			return fmt.Errorf("%w: control %s claims %s over an incomplete assessment (%s)",
				ErrEvidenceUnassessed, e.ControlID, e.Level, reason)
		}
	case quality.SupportsWithLimits:
		// The limits the assessment found must appear in the entry's
		// own Limitations. An assessment that says "ADEQUATE, and here
		// is what it does not cover" is worth nothing if the entry
		// that cites it does not carry that sentence forward.
		if err := e.carriesLimits(); err != nil {
			return err
		}
	}
	return nil
}

// carriesLimits checks that every limit the assessment stated appears
// in the entry's Limitations.
func (e Entry) carriesLimits() error {
	joined := strings.ToLower(strings.Join(e.Limitations, " | "))
	var dropped []string
	for _, attr := range quality.Attributes() {
		j := e.EvidenceQuality.Judgements[attr]
		lim := strings.TrimSpace(j.Limits)
		if j.Grade != quality.Adequate || lim == "" {
			continue
		}
		if !strings.Contains(joined, strings.ToLower(lim)) {
			dropped = append(dropped, fmt.Sprintf("%s: %s", attr, lim))
		}
	}
	if len(dropped) > 0 {
		return fmt.Errorf("%w: control %s drops %s",
			ErrLimitsDropped, e.ControlID, strings.Join(dropped, "; "))
	}
	return nil
}

// looksLikeVeriqo catches the obvious ways VERIQO could be entered as
// its own external validator. It is deliberately a substring check and
// deliberately not exhaustive: it stops the accident, and a party
// determined to launder its own identity through this field is doing
// something no string check would catch.
func looksLikeVeriqo(v string) bool {
	l := strings.ToLower(v)
	for _, s := range []string{"veriqo", "ourselves", "internal team", "self"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// canonicalView is what the entry's hash covers.
func (e Entry) canonicalView() map[string]any {
	return map[string]any{
		"control_id": e.ControlID, "test": e.Test, "execution_id": e.ExecutionID,
		"environment": e.Environment, "input_hash": e.InputHash, "output_hash": e.OutputHash,
		"tool": e.Tool, "tool_version": e.ToolVersion, "result": string(e.Result),
		"evidence": e.Evidence, "level": e.Level.String(), "boundary": string(e.Boundary),
		"validator": e.Validator, "limitations": toAny(sortedCopy(e.Limitations)),
		"evidence_quality": e.qualityView(),
		"prev_hash":        e.prevHash,
	}
}

// qualityView renders the assessment for hashing. An entry whose
// evidence assessment was edited after the fact must not keep its
// hash: the assessment is part of what the entry claims.
func (e Entry) qualityView() any {
	if e.EvidenceQuality == nil {
		return nil
	}
	out := map[string]any{"evidence_version_id": e.EvidenceQuality.EvidenceVersionID}
	for _, attr := range quality.Attributes() {
		j := e.EvidenceQuality.Judgements[attr]
		out[string(attr)] = map[string]any{
			"grade": string(j.Grade), "basis": j.Basis, "limits": j.Limits,
		}
	}
	return out
}

// Ledger is an append-only, hash-linked sequence of qualification
// entries.
type Ledger struct {
	entries []Entry
}

// New returns an empty ledger.
func New() *Ledger { return &Ledger{} }

// Append validates the entry, links it to the previous one, and stores
// it.
func (l *Ledger) Append(e Entry) (Entry, error) {
	if err := e.Validate(); err != nil {
		return Entry{}, err
	}
	if n := len(l.entries); n > 0 {
		e.prevHash = l.entries[n-1].hash
	}
	h, err := jcs.Hash(e.canonicalView())
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: hashing entry: %w", err)
	}
	e.hash = h
	l.entries = append(l.entries, e)
	return e, nil
}

// Entries returns a copy.
func (l *Ledger) Entries() []Entry { return append([]Entry(nil), l.entries...) }

// Verify re-derives every hash and checks every link. A single altered
// field anywhere breaks it.
func (l *Ledger) Verify() error {
	prev := ""
	for i, e := range l.entries {
		if e.prevHash != prev {
			return fmt.Errorf("%w: entry %d links to %q, previous is %q", ErrChainBroken, i, e.prevHash, prev)
		}
		want, err := jcs.Hash(e.canonicalView())
		if err != nil {
			return fmt.Errorf("ledger: rehashing entry %d: %w", i, err)
		}
		if want != e.hash {
			return fmt.Errorf("%w: entry %d records %s, recomputes %s", ErrChainBroken, i, e.hash, want)
		}
		prev = e.hash
	}
	return nil
}

// RootHash returns the head of the chain.
func (l *Ledger) RootHash() string {
	if len(l.entries) == 0 {
		return ""
	}
	return l.entries[len(l.entries)-1].hash
}

// HighestLevelFor returns the highest level supported by a PASSING
// entry for a control, and whether any exists.
//
// A FAIL, a REFUSED and a SKIPPED support nothing. REFUSED especially:
// a control that declined to act was safe, and safety is not evidence
// of capability.
func (l *Ledger) HighestLevelFor(controlID string) (Level, bool) {
	best, found := Implemented, false
	for _, e := range l.entries {
		if e.ControlID != controlID || e.Result != Pass {
			continue
		}
		if !found || e.Level > best {
			best, found = e.Level, true
		}
	}
	return best, found
}

// Report renders the ledger.
func (l *Ledger) Report() string {
	var b strings.Builder
	b.WriteString("VERIQO qualification evidence ledger\n")
	b.WriteString("Every claim carries: control, test, execution, environment, input and\n")
	b.WriteString("output hash, tool and version, result, evidence, level and boundary.\n")
	b.WriteString("A PASS with no evidence artefact and no stated limitation is refused.\n\n")
	if len(l.entries) == 0 {
		b.WriteString("  (empty)\n")
		return b.String()
	}
	for i, e := range l.entries {
		fmt.Fprintf(&b, "%03d  %-10s %-22s %s\n", i+1, e.Result, e.ControlID, e.Test)
		fmt.Fprintf(&b, "     level=%s boundary=%s\n", e.Level, e.Boundary)
		fmt.Fprintf(&b, "     env=%s tool=%s/%s exec=%s\n", e.Environment, e.Tool, e.ToolVersion, e.ExecutionID)
		if e.Evidence != "" {
			fmt.Fprintf(&b, "     evidence=%s\n", e.Evidence)
		}
		if e.Validator != "" {
			fmt.Fprintf(&b, "     validator=%s\n", e.Validator)
		}
		for _, lim := range sortedCopy(e.Limitations) {
			fmt.Fprintf(&b, "     limitation: %s\n", lim)
		}
		fmt.Fprintf(&b, "     hash=%s\n", short(e.hash))
	}
	fmt.Fprintf(&b, "\nroot: %s over %d entries\n", short(l.RootHash()), len(l.entries))
	return b.String()
}

// HashOf is the content hash helper callers use for InputHash and
// OutputHash, so two callers do not disagree about what hashing means.
func HashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func short(h string) string {
	if len(h) > 16 {
		return h[:16]
	}
	return h
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
