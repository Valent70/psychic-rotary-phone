// Package causation implements VICE's Causation Engine and Causation
// Output — blueprint §16-§17.
//
// §16 is explicit about the modelling approach: "Jangan menggunakan
// single-cause model. Gunakan hypothesis model." — every causal
// question this package analyses (e.g. "what caused the cargo damage
// discovered at delivery?") is represented as a SET of competing,
// mutually-exclusive CAUSE_HYPOTHESIS values, never collapsed into one
// asserted cause. §16's own worked example for a cargo-damage-shaped
// case is reproduced in DefaultCargoDamageHypotheses:
//
//	H1 = pre-existing damage    H5 = container failure
//	H2 = loading damage         H6 = terminal handling
//	H3 = carrier handling       H7 = post-delivery storage
//	H4 = weather                H8 = unknown
//
// §17 is even more explicit about what this package must never emit:
//
//	"VICE tidak boleh menghasilkan: Carrier caused the damage."
//
// and gives the shape every causation output must take instead:
//
//	"Evidence currently supports Hypothesis H3 more strongly than
//	alternative hypotheses; however, causation remains unresolved
//	because the custody record between discharge and warehouse
//	receipt is incomplete."
//
// Explain (explain.go) is the ONLY function in this package that
// produces natural-language causation output, and it is built so that
// a bare "X caused Y" sentence is structurally impossible: Narrative is
// always assembled from a fixed hedge template that names a hypothesis
// only RELATIVELY ("more strongly than alternative hypotheses") and
// always states why causation remains unresolved.
//
// # Why this package does not reuse pkg/moat/causal.Graph
//
// The task that produced this package asked, correctly, whether
// pkg/moat/causal.Graph — VERIQO's real causal-graph engine — should be
// the storage substrate for Hypothesis/HypothesisSet. After reading it
// carefully, the answer is no, for reasons specific to what causal.Graph
// models versus what §16 requires:
//
//  1. causal.Edge carries exactly (Cause, Effect, Strength, Lag,
//     Condition), and Strength is a single float64 in (0,1] representing
//     an EMPIRICAL FREQUENCY built up from many independent, hash-chained
//     Observe calls over time ("how often cause precedes effect"). A
//     causation hypothesis in one insurance case is the opposite shape:
//     it is evaluated ONCE, from a small, named list of
//     SupportingEvidence / ContradictingEvidence / MissingEvidence
//     EvidenceIDs and a five-value Status enum (§16) — there is no
//     natural mapping from "list of EvidenceIDs" onto a single re-
//     observable Strength scalar, and forcing one would silently
//     reintroduce exactly the "satu angka confidence yang terlalu
//     sederhana" collapse §9 forbids for evidence strength.
//  2. causal.Graph.AggregateSupport combines every direct cause of one
//     effect via noisy-OR — correct when multiple causes act
//     SIMULTANEOUSLY and INDEPENDENTLY to help produce an effect (its
//     own worked example: AIS_OFF and other signals jointly raising
//     confidence in one incident). A hypothesis set is the opposite:
//     H1..H8 are COMPETING, MUTUALLY EXCLUSIVE explanations for the SAME
//     damage — at most one is actually what happened. Running them
//     through noisy-OR would misrepresent eight competing theories as
//     eight contributing causes, exactly the "single aggregated cause"
//     framing §16 tells us not to build.
//  3. The blueprint's Dependency field (§16: "H7 post-delivery storage
//     may depend on custody-interval evidence that H2 loading damage
//     would also need") is an EVIDENTIARY dependency between two
//     hypotheses' evaluations, not a claim that one hypothesis CAUSES
//     another. Modelling it as a causal.Edge (Cause: H2, Effect: H7)
//     would misstate it as "loading damage causes post-delivery storage
//     damage", which is not what the blueprint means at all.
//
// None of this is a knock on causal.Graph — Why()/AggregateSupport()/
// WhatIf() are exactly right for chains of empirically-observed events
// (AIS_OFF -> STS -> CARGO_CHANGE), which is a genuinely different
// object from a single case's competing-hypothesis ledger. Every other
// sibling package in pkg/insurance (evidence.Registry, policy.History,
// claim.TypeRegistry, party.Registry, gap.Assessment) is likewise a
// small, bespoke, mutex-guarded aggregate rather than a reuse of a
// generic graph type, even where one superficially overlaps — this
// package follows that same established precedent. What this package
// DOES reuse is pkg/insurance/evidence.DependencyGraph, optionally, to
// avoid double-counting evidence that is itself dependent (see
// DeriveStatus and NetSupport below) — the golden rule (§36) this
// package is actually on the hook for.
package causation

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/insurance/evidence"
)

// ---- Status: the blueprint's own closed five-value enum (§16) ----

// Status is a Hypothesis's evaluation status. The blueprint states this
// as an exact, closed five-item list — deliberately closed, unlike
// claim.Type, because §16 gives it as a fixed enumeration, not an
// extensible registry.
type Status string

const (
	StatusSupported            Status = "SUPPORTED"
	StatusPartiallySupported   Status = "PARTIALLY_SUPPORTED"
	StatusContradicted         Status = "CONTRADICTED"
	StatusUnproven             Status = "UNPROVEN"
	StatusInsufficientEvidence Status = "INSUFFICIENT_EVIDENCE"
)

var knownStatuses = map[Status]bool{
	StatusSupported: true, StatusPartiallySupported: true, StatusContradicted: true,
	StatusUnproven: true, StatusInsufficientEvidence: true,
}

// IsKnownStatus reports whether s is one of the five modelled statuses.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// ---- Hypothesis (§16) ----

// HypothesisID identifies one CAUSE_HYPOTHESIS within a HypothesisSet
// (e.g. "H1".."H8" in the blueprint's own worked example). It is
// caller-assigned and scoped to one HypothesisSet, the same way
// party.PartyID is scoped to one case.
type HypothesisID string

// Hypothesis is one candidate explanation for a causal question, with
// EXACTLY the fields §16 names: an identifier, a description,
// supporting/contradicting/missing evidence, a dependency list, and a
// status.
//
// SupportingEvidence and ContradictingEvidence hold
// evidence.Record.EvidenceID() values — real, content-addressed
// evidence identifiers already in the case's evidence.Registry. This
// package never mints or invents an EvidenceID (blueprint §0: "VICE
// MUST NOT invent missing evidence") — it only ever records IDs the
// caller supplies from evidence already ingested elsewhere.
//
// MissingEvidence, in contrast, is deliberately NOT a list of
// EvidenceIDs: it is the hypothesis-scoped analogue of what
// pkg/insurance/gap's Missing Evidence Engine (§26) does at the claim
// level — plain-text descriptions of evidence that WOULD support or
// refute this specific hypothesis but has not been submitted (the
// blueprint's own §17 example: "the custody record between discharge
// and warehouse receipt is incomplete"). Kept as a self-contained
// []string per this package's mandate, rather than importing gap.
//
// Dependency names other HypothesisIDs (within the same HypothesisSet)
// whose evidence this hypothesis's evaluation depends on — the
// blueprint's own example: H7 (post-delivery storage) may depend on the
// same custody-interval evidence H2 (loading damage) would also need.
// This is an evidentiary relationship between two evaluations, not a
// causal claim that one hypothesis causes another (see the package doc
// comment for why this is deliberately NOT modelled as a
// pkg/moat/causal.Edge).
type Hypothesis struct {
	ID                    HypothesisID   `json:"id"`
	Description           string         `json:"description"`
	SupportingEvidence    []string       `json:"supporting_evidence,omitempty"`
	ContradictingEvidence []string       `json:"contradicting_evidence,omitempty"`
	MissingEvidence       []string       `json:"missing_evidence,omitempty"`
	Dependency            []HypothesisID `json:"dependency,omitempty"`
	Status                Status         `json:"status"`
}

var (
	ErrEmptyHypothesisID   = errors.New("causation: HypothesisID must be non-empty")
	ErrEmptyDescription    = errors.New("causation: Description must be non-empty")
	ErrUnknownStatusValue  = errors.New("causation: unknown Status")
	ErrEmptyEvidenceID     = errors.New("causation: EvidenceID must be non-empty")
	ErrEmptyMissingEntry   = errors.New("causation: missing-evidence description must be non-empty")
	ErrDuplicateHypothesis = errors.New("causation: this HypothesisID is already in the set")
	ErrHypothesisNotFound  = errors.New("causation: HypothesisID not found in this set")
	ErrSelfDependency      = errors.New("causation: a hypothesis cannot depend on itself")
	ErrUnknownDependency   = errors.New("causation: dependency references a HypothesisID not in this set")
	ErrDependencyCycle     = errors.New("causation: this dependency would create a cycle among hypotheses")
	ErrEmptyCaseID         = errors.New("causation: CaseID must be non-empty")
	ErrEmptyQuestion       = errors.New("causation: Question must be non-empty")
)

// NewHypothesis constructs a Hypothesis with no evidence recorded yet
// and Status StatusUnproven — the correct starting status for a
// hypothesis nothing has yet spoken for or against (see DeriveStatus).
func NewHypothesis(id HypothesisID, description string) (Hypothesis, error) {
	if id == "" {
		return Hypothesis{}, ErrEmptyHypothesisID
	}
	if description == "" {
		return Hypothesis{}, ErrEmptyDescription
	}
	return Hypothesis{ID: id, Description: description, Status: StatusUnproven}, nil
}

// DefaultCargoDamageHypotheses reproduces the blueprint's own §16
// worked example verbatim: H1..H8 for a cargo-damage-shaped case, each
// starting with no evidence recorded and Status StatusUnproven. Callers
// (or tests) Add these into a HypothesisSet and then record evidence
// against them as the case's evidence.Registry is populated.
func DefaultCargoDamageHypotheses() []Hypothesis {
	seed := func(id HypothesisID, desc string) Hypothesis {
		h, err := NewHypothesis(id, desc)
		if err != nil {
			// Unreachable: every id/desc below is a non-empty literal.
			panic(err)
		}
		return h
	}
	return []Hypothesis{
		seed("H1", "Pre-existing damage: the cargo was already damaged before it was loaded."),
		seed("H2", "Loading damage: the cargo was damaged during the loading process."),
		seed("H3", "Carrier handling: the cargo was damaged as a result of the carrier's handling during carriage."),
		seed("H4", "Weather: the cargo was damaged as a result of weather conditions encountered during the voyage."),
		seed("H5", "Container failure: the cargo was damaged due to a failure of the container (structural, seal, or refrigeration)."),
		seed("H6", "Terminal handling: the cargo was damaged during handling at a port or terminal."),
		seed("H7", "Post-delivery storage: the cargo was damaged after delivery, during storage in the consignee's or a third party's custody."),
		seed("H8", "Unknown: the cause of damage cannot currently be attributed to any of the other modelled hypotheses."),
	}
}

// ---- Status derivation (§16, §36) ----
//
// computeStatus is the REAL engine logic behind Status: a hypothesis's
// Status is always the deterministic consequence of its own evidence
// counts, never a value the caller sets by hand with no computation
// behind it. HypothesisSet.RecomputeStatuses (below) is the only code
// path that ever assigns a stored Hypothesis's Status field.
//
// The exact rule, in priority order:
//
//  1. sc == 0 && cc == 0 && mc > 0  -> INSUFFICIENT_EVIDENCE.
//     Nothing supports or contradicts this hypothesis yet, but at least
//     one specific evidence gap has been identified against it. This is
//     the blueprint's own golden rule (§36) applied directly: "When
//     evidence is insufficient... the correct output is
//     INSUFFICIENT_EVIDENCE — not a guessed conclusion."
//  2. sc == 0 && cc == 0 && mc == 0 -> UNPROVEN.
//     Genuinely nothing recorded either way — distinct from case 1
//     because no evidence gap has even been identified yet (the
//     hypothesis has not been analysed for what evidence it would
//     need), so INSUFFICIENT_EVIDENCE would overstate how far analysis
//     has actually got.
//  3. cc > sc -> CONTRADICTED.
//     Contradicting evidence strictly outweighs supporting evidence
//     (this also covers sc == 0 && cc > 0).
//  4. cc > 0 || mc > 0 (and cc <= sc, sc > 0) -> PARTIALLY_SUPPORTED.
//     Supporting evidence is at least as strong as contradicting
//     evidence, BUT either some contradicting evidence exists or a
//     known evidence gap (MissingEvidence) is on record. This is the
//     mechanical enforcement of the golden rule this task calls out
//     explicitly: "a hypothesis with only partial support must never
//     be reported as SUPPORTED outright." A hypothesis with
//     overwhelming supporting evidence but even ONE MissingEvidence
//     entry is PARTIALLY_SUPPORTED, never SUPPORTED — uncertainty is
//     never converted into certainty just because supporting evidence
//     is plentiful.
//  5. cc == 0 && mc == 0 && sc > 0 -> SUPPORTED.
//     Supporting evidence exists, nothing contradicts it, and no
//     evidence gap is recorded against it. Even here, SUPPORTED is a
//     statement about the EVIDENCE RECORD, never a causation verdict —
//     Explain (explain.go) still never lets a SUPPORTED hypothesis be
//     phrased as a bare, unqualified "X caused Y".
//
// sc and cc are computed via countIndependentEvidence: when a
// DependencyGraph is supplied, evidence counting goes through
// evidence.DependencyGraph.IndependentCount rather than len(), so that
// e.g. Photo A -> Statement A -> Survey A (the blueprint's own §10
// worked example) counts as ONE independent supporting item, not three
// — the golden rule's "never double-count dependent evidence" (§36).
func computeStatus(h Hypothesis, dg *evidence.DependencyGraph) Status {
	sc := countIndependentEvidence(h.SupportingEvidence, dg)
	cc := countIndependentEvidence(h.ContradictingEvidence, dg)
	mc := len(h.MissingEvidence)

	switch {
	case sc == 0 && cc == 0 && mc > 0:
		return StatusInsufficientEvidence
	case sc == 0 && cc == 0:
		return StatusUnproven
	case cc > sc:
		return StatusContradicted
	case cc > 0 || mc > 0:
		return StatusPartiallySupported
	default:
		return StatusSupported
	}
}

// DeriveStatus is the exported form of computeStatus, for callers and
// tests that want the status a Hypothesis WOULD compute to without
// mutating a HypothesisSet. dg may be nil, in which case supporting and
// contradicting evidence are counted by literal deduplicated
// EvidenceID, with no cross-item independence analysis.
func DeriveStatus(h Hypothesis, dg *evidence.DependencyGraph) Status {
	return computeStatus(h, dg)
}

// countIndependentEvidence counts ids as independent items: through
// dg.IndependentCount when dg is non-nil (see the package/DeriveStatus
// docs for why), or as a deduplicated literal count otherwise. Either
// way, a literal duplicate EvidenceID appearing twice in the same list
// is never counted twice.
func countIndependentEvidence(ids []string, dg *evidence.DependencyGraph) int {
	if len(ids) == 0 {
		return 0
	}
	if dg != nil {
		return dg.IndependentCount(ids)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			seen[id] = true
		}
	}
	return len(seen)
}

// NetSupport is a per-hypothesis ranking key: independence-adjusted
// supporting-evidence count minus independence-adjusted contradicting-
// evidence count. It is used ONLY to compare hypotheses against each
// other (see Explain, in explain.go) — it is deliberately NOT a
// probability, confidence score, or anything presented to a human as a
// certainty measure, which is exactly why it is a plain int rather than
// a normalised float: the blueprint (§9) is explicit that VICE must not
// collapse multi-dimensional evidence assessment into "satu angka
// confidence yang terlalu sederhana", and this package extends that
// caution to causation ranking as well.
func NetSupport(h Hypothesis, dg *evidence.DependencyGraph) int {
	return countIndependentEvidence(h.SupportingEvidence, dg) - countIndependentEvidence(h.ContradictingEvidence, dg)
}

// ---- HypothesisSet: the aggregate for one causal question (§16) ----

// HypothesisSet holds every competing Hypothesis for ONE causal
// question on one case (e.g. "what caused the cargo damage discovered
// at delivery?"). Safe for concurrent use — evidence can arrive and be
// recorded against different hypotheses concurrently during analysis,
// the same way evidence.Registry and party.Registry are built to allow.
type HypothesisSet struct {
	mu sync.RWMutex

	CaseID   string
	ClaimID  string
	Question string

	hypotheses map[HypothesisID]Hypothesis
	order      []HypothesisID
}

// NewHypothesisSet constructs an empty HypothesisSet for one causal
// question. ClaimID is optional (a case may run causation analysis
// before every claim is individually tracked); CaseID and Question are
// required — a causal question with no stated question is not
// analysable, and one with no case is not attributable to anything.
func NewHypothesisSet(caseID, claimID, question string) (*HypothesisSet, error) {
	if caseID == "" {
		return nil, ErrEmptyCaseID
	}
	if question == "" {
		return nil, ErrEmptyQuestion
	}
	return &HypothesisSet{
		CaseID: caseID, ClaimID: claimID, Question: question,
		hypotheses: make(map[HypothesisID]Hypothesis),
	}, nil
}

// Add registers a new Hypothesis in the set. Refuses a duplicate
// HypothesisID. Status is always forced to StatusUnproven, matching
// NewHypothesis, regardless of what the caller passed in h -- exactly
// like manifest.RegisterDraft forcing State=DRAFT and evidence.Submit
// resetting Status to StatusUnverified: a Hypothesis's Status is an
// AUTHORITATIVE, derived value (see computeStatus/DeriveStatus, driven
// only by AddSupportingEvidence/AddContradictingEvidence/
// AddMissingEvidence/RecomputeStatuses), never something a caller -- or
// a JSON deserializer, which sets exported fields exactly the same way
// a struct literal does -- may assert directly by handing Add a
// pre-populated Hypothesis{Status: StatusSupported}. This closes the
// same class of gap INV-001 ("no caller may directly construct an
// authoritative state") names for every other subsystem in this
// repository.
func (hs *HypothesisSet) Add(h Hypothesis) error {
	if h.ID == "" {
		return ErrEmptyHypothesisID
	}
	if h.Description == "" {
		return ErrEmptyDescription
	}
	h.Status = StatusUnproven

	hs.mu.Lock()
	defer hs.mu.Unlock()
	if _, exists := hs.hypotheses[h.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateHypothesis, h.ID)
	}
	hs.hypotheses[h.ID] = h
	hs.order = append(hs.order, h.ID)
	return nil
}

// Get returns the Hypothesis for id.
func (hs *HypothesisSet) Get(id HypothesisID) (Hypothesis, bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	h, ok := hs.hypotheses[id]
	return h, ok
}

// All returns every Hypothesis in the set, in the order it was Added —
// deterministic regardless of Go map iteration order.
func (hs *HypothesisSet) All() []Hypothesis {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	out := make([]Hypothesis, 0, len(hs.order))
	for _, id := range hs.order {
		out = append(out, hs.hypotheses[id])
	}
	return out
}

// Count returns the number of hypotheses in the set.
func (hs *HypothesisSet) Count() int {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	return len(hs.order)
}

// AddSupportingEvidence records evidenceID as supporting hypothesis id,
// then immediately recomputes that hypothesis's Status (via
// computeStatus with dg == nil — literal deduplicated counting). Call
// RecomputeStatuses afterward with a real DependencyGraph for an
// independence-adjusted refresh once the case's dependency graph is
// available.
func (hs *HypothesisSet) AddSupportingEvidence(id HypothesisID, evidenceID string) error {
	return hs.addEvidence(id, evidenceID, true)
}

// AddContradictingEvidence records evidenceID as contradicting
// hypothesis id, and recomputes Status the same way
// AddSupportingEvidence does.
func (hs *HypothesisSet) AddContradictingEvidence(id HypothesisID, evidenceID string) error {
	return hs.addEvidence(id, evidenceID, false)
}

func (hs *HypothesisSet) addEvidence(id HypothesisID, evidenceID string, supporting bool) error {
	if evidenceID == "" {
		return ErrEmptyEvidenceID
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	h, ok := hs.hypotheses[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrHypothesisNotFound, id)
	}
	if supporting {
		if containsString(h.SupportingEvidence, evidenceID) {
			return nil // idempotent: already recorded
		}
		h.SupportingEvidence = append(h.SupportingEvidence, evidenceID)
	} else {
		if containsString(h.ContradictingEvidence, evidenceID) {
			return nil
		}
		h.ContradictingEvidence = append(h.ContradictingEvidence, evidenceID)
	}
	h.Status = computeStatus(h, nil)
	hs.hypotheses[id] = h
	return nil
}

// AddMissingEvidence records a plain-text description of evidence that
// would support or refute hypothesis id but has not been submitted —
// this package's hypothesis-scoped analogue of pkg/insurance/gap's
// EVIDENCE_GAP (§26), kept self-contained (see the Hypothesis doc
// comment). Recomputes Status immediately, the same way the evidence
// setters do.
func (hs *HypothesisSet) AddMissingEvidence(id HypothesisID, description string) error {
	if description == "" {
		return ErrEmptyMissingEntry
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	h, ok := hs.hypotheses[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrHypothesisNotFound, id)
	}
	if containsString(h.MissingEvidence, description) {
		return nil
	}
	h.MissingEvidence = append(h.MissingEvidence, description)
	h.Status = computeStatus(h, nil)
	hs.hypotheses[id] = h
	return nil
}

// AddDependency records that hypothesis id's evaluation depends on
// hypothesis dependsOn's evidence (the blueprint's own example: H7
// depends on H2 because both need the same custody-interval evidence).
// Refuses a self-dependency, a reference to a HypothesisID not in this
// set, and any dependency that would create a cycle among hypotheses.
func (hs *HypothesisSet) AddDependency(id, dependsOn HypothesisID) error {
	if id == dependsOn {
		return ErrSelfDependency
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	h, ok := hs.hypotheses[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrHypothesisNotFound, id)
	}
	if _, ok := hs.hypotheses[dependsOn]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownDependency, dependsOn)
	}
	if hs.pathExistsLocked(dependsOn, id) {
		return fmt.Errorf("%w: %s -> %s", ErrDependencyCycle, id, dependsOn)
	}
	for _, existing := range h.Dependency {
		if existing == dependsOn {
			return nil // idempotent: already recorded
		}
	}
	h.Dependency = append(h.Dependency, dependsOn)
	hs.hypotheses[id] = h
	return nil
}

// pathExistsLocked reports whether there is a dependency path from
// `from` to `to` (from depends on ... depends on to), by walking
// Dependency edges already recorded. Caller must hold hs.mu.
func (hs *HypothesisSet) pathExistsLocked(from, to HypothesisID) bool {
	if from == to {
		return true
	}
	visited := map[HypothesisID]bool{from: true}
	stack := []HypothesisID{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, dep := range hs.hypotheses[n].Dependency {
			if dep == to {
				return true
			}
			if !visited[dep] {
				visited[dep] = true
				stack = append(stack, dep)
			}
		}
	}
	return false
}

// RecomputeStatuses recomputes every hypothesis's Status from its
// current evidence lists, via computeStatus. Pass a real
// evidence.DependencyGraph (built from the case's evidence) for an
// independence-adjusted recompute that will not double-count dependent
// evidence shared between hypotheses' lists; pass nil for a literal
// deduplicated-count recompute (what AddSupportingEvidence /
// AddContradictingEvidence / AddMissingEvidence already do
// automatically after each mutation).
func (hs *HypothesisSet) RecomputeStatuses(dg *evidence.DependencyGraph) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	for _, id := range hs.order {
		h := hs.hypotheses[id]
		h.Status = computeStatus(h, dg)
		hs.hypotheses[id] = h
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// SortedIDs returns ids sorted lexicographically — for callers that
// want stable output independent of a HypothesisSet's own Add order
// (mirrors claim.SortedTypes).
func SortedIDs(ids []HypothesisID) []HypothesisID {
	out := append([]HypothesisID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
