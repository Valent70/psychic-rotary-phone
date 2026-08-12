// Package knowledge closes the "Deterministic Knowledge Evolution"
// P0 of the v7.11 audit.
//
// The audit was explicit that this is not an approval queue. The
// pipeline is:
//
//	KnowledgeProposal → Validation → Conflict Analysis → Impact Analysis
//	  → Simulation → Approval → Activation → Hash Chain
//	  → Versioned Knowledge State
//
// and the critical invariant is:
//
//	"Knowledge state pada tick T1 tidak boleh berubah hanya karena
//	 knowledge berubah pada T2."
//
// That invariant is why knowledge state here is a FOLD over an
// append-only proposal ledger with effective-from/effective-to ticks,
// not a mutable rule set. StateAt(T) replays the ledger up to T; a
// proposal activated at T2 is invisible to StateAt(T1) by construction,
// not by convention.
//
// This package is not veriqo/core/knowledge, which is a fact/triple
// store with Bayesian revision. This is the governance layer above it:
// it governs changes to rules, relations, weights, source authority and
// model parameters — the knobs that decide how facts are interpreted.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrInvalidMutation rejects a structurally malformed proposal.
	ErrInvalidMutation = errors.New("knowledge: invalid mutation")
	// ErrUnknownProposal is returned for an unregistered proposal ID.
	ErrUnknownProposal = errors.New("knowledge: unknown proposal")
	// ErrIllegalTransition names a state machine violation.
	ErrIllegalTransition = errors.New("knowledge: illegal proposal transition")
	// ErrConflict blocks activation while an unresolved conflict with an
	// already-active mutation exists.
	ErrConflict = errors.New("knowledge: unresolved conflict with active knowledge")
	// ErrSimulationRequired blocks approval before an impact simulation
	// has been recorded. A rule change with no simulated blast radius is
	// a production change with unknown consequences.
	ErrSimulationRequired = errors.New("knowledge: approval requires a recorded simulation")
	// ErrApproverIsProposer blocks self-approval. Two-person integrity is
	// a governance property, not a UI nicety.
	ErrApproverIsProposer = errors.New("knowledge: proposer cannot approve their own proposal")
	// ErrMissingJustification blocks a proposal with no reason and no
	// evidence references.
	ErrMissingJustification = errors.New("knowledge: proposal requires a reason and evidence references")
	// ErrNonMonotonicTick rejects backdated governance.
	ErrNonMonotonicTick = errors.New("knowledge: tick must not go backwards")
	// ErrLedgerTampered is returned by VerifyChain.
	ErrLedgerTampered = errors.New("knowledge: ledger hash chain broken")
	// ErrRetroactive refuses an effective-from tick in the past. Allowing
	// it would silently rewrite historical knowledge state — the exact
	// thing the critical invariant forbids.
	ErrRetroactive = errors.New("knowledge: effective-from must not predate activation")
)

// MutationKind is the closed set of governed knowledge changes.
type MutationKind string

const (
	AddRule               MutationKind = "ADD_RULE"
	RemoveRule            MutationKind = "REMOVE_RULE"
	ModifyRule            MutationKind = "MODIFY_RULE"
	AddRelation           MutationKind = "ADD_RELATION"
	RemoveRelation        MutationKind = "REMOVE_RELATION"
	ChangeWeight          MutationKind = "CHANGE_WEIGHT"
	ChangeSourceAuthority MutationKind = "CHANGE_SOURCE_AUTHORITY"
	ChangeModelParameter  MutationKind = "CHANGE_MODEL_PARAMETER"
)

var validKinds = map[MutationKind]bool{
	AddRule: true, RemoveRule: true, ModifyRule: true, AddRelation: true,
	RemoveRelation: true, ChangeWeight: true, ChangeSourceAuthority: true,
	ChangeModelParameter: true,
}

// numericKinds are the mutations whose Value field is meaningful.
var numericKinds = map[MutationKind]bool{
	ChangeWeight: true, ChangeSourceAuthority: true, ChangeModelParameter: true,
}

// Mutation is one atomic knowledge change.
type Mutation struct {
	Kind MutationKind `json:"kind"`
	// Target is the addressed knowledge object: a rule id, a relation
	// "subject|predicate|object", a source id, or "model@param".
	Target string `json:"target"`
	// Body is the new definition for rule/relation mutations.
	Body string `json:"body"`
	// Value is the new numeric value for weight/authority/parameter
	// mutations. Ignored (and required to be zero) for the others.
	Value float64 `json:"value"`
}

func (m Mutation) validate() error {
	if !validKinds[m.Kind] {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidMutation, m.Kind)
	}
	if strings.TrimSpace(m.Target) == "" {
		return fmt.Errorf("%w: target is required", ErrInvalidMutation)
	}
	if numericKinds[m.Kind] {
		if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			return fmt.Errorf("%w: %s needs a finite value", ErrInvalidMutation, m.Kind)
		}
		if m.Kind != ChangeModelParameter && (m.Value < 0 || m.Value > 1) {
			return fmt.Errorf("%w: %s value must be in [0,1], got %v", ErrInvalidMutation, m.Kind, m.Value)
		}
	} else if m.Value != 0 {
		return fmt.Errorf("%w: %s must not carry a numeric value", ErrInvalidMutation, m.Kind)
	}
	switch m.Kind {
	case AddRule, ModifyRule, AddRelation:
		if strings.TrimSpace(m.Body) == "" {
			return fmt.Errorf("%w: %s requires a body", ErrInvalidMutation, m.Kind)
		}
	}
	return nil
}

func (m Mutation) hash() string {
	s := "mutation/v1\n" + string(m.Kind) + "\n" + m.Target + "\n" + m.Body + "\n" +
		strconv.FormatFloat(m.Value, 'g', 17, 64) + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ProposalState is the governance state machine.
type ProposalState string

const (
	StateDraft      ProposalState = "DRAFT"
	StateValidated  ProposalState = "VALIDATED"
	StateAnalyzed   ProposalState = "ANALYZED"  // conflict + impact analysis done
	StateSimulated  ProposalState = "SIMULATED" // dry-run recorded
	StateApproved   ProposalState = "APPROVED"
	StateActive     ProposalState = "ACTIVE"
	StateRejected   ProposalState = "REJECTED"
	StateSuperseded ProposalState = "SUPERSEDED"
	StateReverted   ProposalState = "REVERTED"
)

// ConflictReport names what this proposal collides with.
//
// The distinction between Supersedes and ConflictsWith is the whole
// design. A newer proposal replacing the value of an ALREADY-ACTIVE
// target is not a conflict — it is the normal way knowledge evolves,
// and blocking it would make the engine a write-once store. A genuine
// conflict is two proposals IN FLIGHT over the same target at the same
// time: whichever activated second would silently clobber a change its
// own analysis and simulation never saw.
type ConflictReport struct {
	ConflictsWith []string `json:"conflicts_with"` // in-flight proposal IDs — blocking
	Supersedes    []string `json:"supersedes"`     // active proposal IDs — expected
	Reasons       []string `json:"reasons"`
	Blocking      bool     `json:"blocking"`
}

// ImpactReport is the blast radius of a proposal.
type ImpactReport struct {
	AffectedTargets []string `json:"affected_targets"`
	AffectedRules   int      `json:"affected_rules"`
	// NumericDelta is the magnitude of the change for numeric
	// mutations; 0 for structural ones.
	NumericDelta float64 `json:"numeric_delta"`
	// Severity is LOW/MEDIUM/HIGH, derived not asserted.
	Severity string `json:"severity"`
}

// SimulationResult is the recorded dry-run of a proposal against a
// sample of decisions.
type SimulationResult struct {
	SampleSize      int      `json:"sample_size"`
	ChangedOutcomes int      `json:"changed_outcomes"`
	ChangeRate      float64  `json:"change_rate"`
	Notes           []string `json:"notes"`
	Hash            string   `json:"hash"`
}

func (s SimulationResult) computeHash() string {
	var sb strings.Builder
	sb.WriteString("knowledge_sim/v1\nn=" + strconv.Itoa(s.SampleSize) +
		"\nchanged=" + strconv.Itoa(s.ChangedOutcomes) +
		"\nrate=" + strconv.FormatFloat(s.ChangeRate, 'g', 17, 64) + "\n")
	notes := append([]string(nil), s.Notes...)
	sort.Strings(notes)
	for _, n := range notes {
		sb.WriteString("note=" + n + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// Proposal is one governed knowledge change through its whole life.
type Proposal struct {
	ID            string           `json:"id"`
	Mutations     []Mutation       `json:"mutations"`
	Proposer      string           `json:"proposer"`
	Approver      string           `json:"approver"`
	Reason        string           `json:"reason"`
	EvidenceRefs  []string         `json:"evidence_refs"`
	State         ProposalState    `json:"state"`
	ProposedTick  uint64           `json:"proposed_tick"`
	ApprovedTick  uint64           `json:"approved_tick"`
	EffectiveFrom uint64           `json:"effective_from"`
	EffectiveTo   uint64           `json:"effective_to"` // 0 = still in force
	Conflict      ConflictReport   `json:"conflict"`
	Impact        ImpactReport     `json:"impact"`
	Simulation    SimulationResult `json:"simulation"`
	PreviousRoot  string           `json:"previous_root"`
	NewRoot       string           `json:"new_root"`
}

// Entry is one immutable ledger record.
type Entry struct {
	Index      uint64        `json:"index"`
	ProposalID string        `json:"proposal_id"`
	From       ProposalState `json:"from"`
	To         ProposalState `json:"to"`
	Tick       uint64        `json:"tick"`
	Actor      string        `json:"actor"`
	Detail     string        `json:"detail"`
	Payload    string        `json:"payload"` // content hash of the proposal at this point
	PrevHash   string        `json:"prev_hash"`
	Hash       string        `json:"hash"`
}

var transitions = map[ProposalState][]ProposalState{
	StateDraft:     {StateValidated, StateRejected},
	StateValidated: {StateAnalyzed, StateRejected},
	StateAnalyzed:  {StateSimulated, StateRejected},
	StateSimulated: {StateApproved, StateRejected},
	StateApproved:  {StateActive, StateRejected},
	StateActive:    {StateSuperseded, StateReverted},
	// Terminal.
	StateRejected: {}, StateSuperseded: {}, StateReverted: {},
}

func canGo(from, to ProposalState) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Engine is the knowledge evolution engine.
type Engine struct {
	mu        sync.RWMutex
	proposals map[string]*Proposal
	order     []string
	ledger    []Entry
	lastTick  uint64
}

// New constructs an empty Engine.
func New() *Engine {
	return &Engine{proposals: map[string]*Proposal{}}
}

// Propose registers a DRAFT proposal and immediately runs validation.
// ID is derived from content — no UUIDs, per this repo's invariants —
// so an identical proposal from an identical proposer at an identical
// tick is the same proposal, not a second one.
func (e *Engine) Propose(proposer, reason string, evidenceRefs []string,
	muts []Mutation, tick uint64) (Proposal, error) {

	if strings.TrimSpace(reason) == "" || len(evidenceRefs) == 0 {
		return Proposal{}, ErrMissingJustification
	}
	if len(muts) == 0 {
		return Proposal{}, fmt.Errorf("%w: a proposal must change something", ErrInvalidMutation)
	}
	for _, m := range muts {
		if err := m.validate(); err != nil {
			return Proposal{}, err
		}
	}
	// Deterministic canonical ordering of mutations inside a proposal.
	sorted := append([]Mutation(nil), muts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].hash() < sorted[j].hash() })
	refs := append([]string(nil), evidenceRefs...)
	sort.Strings(refs)

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p := &Proposal{
		Mutations: sorted, Proposer: proposer, Reason: reason, EvidenceRefs: refs,
		State: StateDraft, ProposedTick: tick,
	}
	p.ID = proposalID(*p)
	if _, dup := e.proposals[p.ID]; dup {
		return Proposal{}, fmt.Errorf("%w: identical proposal %s already exists", ErrInvalidMutation, p.ID)
	}
	e.proposals[p.ID] = p
	e.order = append(e.order, p.ID)
	e.appendLocked(Entry{ProposalID: p.ID, To: StateDraft, Tick: tick, Actor: proposer,
		Detail: "proposed", Payload: hashProposal(*p)})

	// Validation is not a separate human step: it is mechanical, so it
	// runs here and either advances or rejects.
	p.State = StateValidated
	e.appendLocked(Entry{ProposalID: p.ID, From: StateDraft, To: StateValidated, Tick: tick,
		Actor: "engine", Detail: "mutations structurally valid", Payload: hashProposal(*p)})
	return *p, nil
}

// Analyze runs conflict and impact analysis against currently-active
// knowledge and advances the proposal to ANALYZED.
func (e *Engine) Analyze(id string, tick uint64) (Proposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	if !canGo(p.State, StateAnalyzed) {
		return Proposal{}, fmt.Errorf("%w: %s %s -> ANALYZED", ErrIllegalTransition, id, p.State)
	}
	p.Conflict = e.conflictsLocked(p)
	p.Impact = e.impactLocked(p)
	p.State = StateAnalyzed
	e.appendLocked(Entry{ProposalID: id, From: StateValidated, To: StateAnalyzed, Tick: tick,
		Actor: "engine", Detail: "conflicts=" + strconv.Itoa(len(p.Conflict.ConflictsWith)) +
			" severity=" + p.Impact.Severity, Payload: hashProposal(*p)})
	return *p, nil
}

// conflictsLocked finds active proposals touching the same targets.
//
// A same-target collision is only BLOCKING when the two mutations are
// not identical: re-asserting the same weight is a no-op, not a
// conflict, and treating it as one would make idempotent redeployment
// impossible.
func (e *Engine) conflictsLocked(p *Proposal) ConflictReport {
	var rep ConflictReport
	targets := map[string]Mutation{}
	for _, m := range p.Mutations {
		targets[string(m.Kind)+":"+m.Target] = m
	}
	inFlight := map[ProposalState]bool{
		StateValidated: true, StateAnalyzed: true, StateSimulated: true, StateApproved: true,
	}
	for _, id := range e.order {
		other := e.proposals[id]
		if other.ID == p.ID {
			continue
		}
		if other.State != StateActive && !inFlight[other.State] {
			continue
		}
		for _, om := range other.Mutations {
			key := string(om.Kind) + ":" + om.Target
			mine, hit := targets[key]
			if !hit {
				// Add/Remove of the same object is also a collision.
				if opp := opposite(om.Kind); opp != "" {
					mine, hit = targets[string(opp)+":"+om.Target]
				}
			}
			if !hit {
				continue
			}
			if other.State == StateActive {
				if mine.hash() == om.hash() {
					rep.Reasons = append(rep.Reasons,
						"no-op: "+om.Target+" already at this value via "+other.ID)
					continue
				}
				rep.Supersedes = append(rep.Supersedes, other.ID)
				rep.Reasons = append(rep.Reasons,
					"target "+om.Target+" supersedes active proposal "+other.ID)
				continue
			}
			rep.ConflictsWith = append(rep.ConflictsWith, other.ID)
			rep.Reasons = append(rep.Reasons,
				"target "+om.Target+" is concurrently governed by in-flight proposal "+other.ID)
			rep.Blocking = true
		}
	}
	sort.Strings(rep.ConflictsWith)
	rep.ConflictsWith = dedupe(rep.ConflictsWith)
	sort.Strings(rep.Supersedes)
	rep.Supersedes = dedupe(rep.Supersedes)
	sort.Strings(rep.Reasons)
	rep.Reasons = dedupe(rep.Reasons)
	return rep
}

func opposite(k MutationKind) MutationKind {
	switch k {
	case AddRule:
		return RemoveRule
	case RemoveRule:
		return AddRule
	case AddRelation:
		return RemoveRelation
	case RemoveRelation:
		return AddRelation
	}
	return ""
}

func (e *Engine) impactLocked(p *Proposal) ImpactReport {
	rep := ImpactReport{}
	seen := map[string]bool{}
	for _, m := range p.Mutations {
		if !seen[m.Target] {
			seen[m.Target] = true
			rep.AffectedTargets = append(rep.AffectedTargets, m.Target)
		}
		switch m.Kind {
		case AddRule, RemoveRule, ModifyRule:
			rep.AffectedRules++
		}
		if numericKinds[m.Kind] {
			prev := e.currentNumericLocked(m.Kind, m.Target)
			d := math.Abs(m.Value - prev)
			if d > rep.NumericDelta {
				rep.NumericDelta = d
			}
		}
	}
	sort.Strings(rep.AffectedTargets)
	switch {
	case rep.NumericDelta >= 0.3 || rep.AffectedRules >= 5 ||
		containsKind(p.Mutations, ChangeSourceAuthority):
		rep.Severity = "HIGH"
	case rep.NumericDelta >= 0.1 || rep.AffectedRules >= 2:
		rep.Severity = "MEDIUM"
	default:
		rep.Severity = "LOW"
	}
	return rep
}

func containsKind(ms []Mutation, k MutationKind) bool {
	for _, m := range ms {
		if m.Kind == k {
			return true
		}
	}
	return false
}

func (e *Engine) currentNumericLocked(kind MutationKind, target string) float64 {
	var v float64
	for _, id := range e.order {
		p := e.proposals[id]
		if p.State != StateActive {
			continue
		}
		for _, m := range p.Mutations {
			if m.Kind == kind && m.Target == target {
				v = m.Value
			}
		}
	}
	return v
}

// Simulate records the dry-run outcome and advances to SIMULATED.
func (e *Engine) Simulate(id string, sim SimulationResult, tick uint64) (Proposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	if !canGo(p.State, StateSimulated) {
		return Proposal{}, fmt.Errorf("%w: %s %s -> SIMULATED", ErrIllegalTransition, id, p.State)
	}
	if sim.SampleSize <= 0 {
		return Proposal{}, fmt.Errorf("%w: simulation over an empty sample proves nothing", ErrSimulationRequired)
	}
	if sim.SampleSize > 0 {
		sim.ChangeRate = float64(sim.ChangedOutcomes) / float64(sim.SampleSize)
	}
	sim.Hash = sim.computeHash()
	p.Simulation = sim
	p.State = StateSimulated
	e.appendLocked(Entry{ProposalID: id, From: StateAnalyzed, To: StateSimulated, Tick: tick,
		Actor: "engine", Detail: "change_rate=" + strconv.FormatFloat(sim.ChangeRate, 'g', 6, 64),
		Payload: hashProposal(*p)})
	return *p, nil
}

// Approve records a named approver and advances to APPROVED.
func (e *Engine) Approve(id, approver string, tick uint64) (Proposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	if !canGo(p.State, StateApproved) {
		return Proposal{}, fmt.Errorf("%w: %s %s -> APPROVED", ErrIllegalTransition, id, p.State)
	}
	if approver == "" {
		return Proposal{}, ErrMissingJustification
	}
	if approver == p.Proposer {
		return Proposal{}, fmt.Errorf("%w: %s", ErrApproverIsProposer, approver)
	}
	if p.Simulation.Hash == "" {
		return Proposal{}, ErrSimulationRequired
	}
	p.Approver, p.ApprovedTick, p.State = approver, tick, StateApproved
	e.appendLocked(Entry{ProposalID: id, From: StateSimulated, To: StateApproved, Tick: tick,
		Actor: approver, Detail: "approved", Payload: hashProposal(*p)})
	return *p, nil
}

// Activate makes an approved proposal effective from effectiveFrom.
//
// Refusing a retroactive effectiveFrom is what enforces the critical
// invariant: nothing activated now may change what StateAt(past) says.
func (e *Engine) Activate(id string, effectiveFrom, tick uint64) (Proposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	if !canGo(p.State, StateActive) {
		return Proposal{}, fmt.Errorf("%w: %s %s -> ACTIVE", ErrIllegalTransition, id, p.State)
	}
	if effectiveFrom < tick {
		return Proposal{}, fmt.Errorf("%w: effective_from=%d activation_tick=%d",
			ErrRetroactive, effectiveFrom, tick)
	}
	// Conflicts are re-evaluated at activation, not trusted from
	// analysis time: something else may have gone active in between.
	if c := e.conflictsLocked(p); c.Blocking {
		p.Conflict = c
		return Proposal{}, fmt.Errorf("%w: %s conflicts with %v", ErrConflict, id, c.ConflictsWith)
	}
	p.PreviousRoot = e.rootLocked()
	p.EffectiveFrom = effectiveFrom
	p.EffectiveTo = 0
	p.State = StateActive

	// Supersede any active proposal governing exactly the same target.
	for _, otherID := range e.order {
		o := e.proposals[otherID]
		if o.ID == p.ID || o.State != StateActive {
			continue
		}
		if sharesTarget(o, p) {
			o.State = StateSuperseded
			o.EffectiveTo = effectiveFrom
			e.appendLocked(Entry{ProposalID: o.ID, From: StateActive, To: StateSuperseded,
				Tick: tick, Actor: "engine", Detail: "superseded by " + p.ID,
				Payload: hashProposal(*o)})
		}
	}
	p.NewRoot = e.rootLocked()
	e.appendLocked(Entry{ProposalID: id, From: StateApproved, To: StateActive, Tick: tick,
		Actor: p.Approver, Detail: "effective_from=" + strconv.FormatUint(effectiveFrom, 10),
		Payload: hashProposal(*p)})
	return *p, nil
}

func sharesTarget(a, b *Proposal) bool {
	set := map[string]bool{}
	for _, m := range a.Mutations {
		set[m.Target] = true
	}
	for _, m := range b.Mutations {
		if set[m.Target] {
			return true
		}
	}
	return false
}

// Reject terminates a proposal at any pre-active state.
func (e *Engine) Reject(id, actor, reason string, tick uint64) (Proposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	if !canGo(p.State, StateRejected) {
		return Proposal{}, fmt.Errorf("%w: %s %s -> REJECTED", ErrIllegalTransition, id, p.State)
	}
	from := p.State
	p.State = StateRejected
	e.appendLocked(Entry{ProposalID: id, From: from, To: StateRejected, Tick: tick,
		Actor: actor, Detail: reason, Payload: hashProposal(*p)})
	return *p, nil
}

// Revert withdraws an active proposal from effectiveTo onwards. It is
// an append, never a deletion: the knowledge that was in force between
// EffectiveFrom and effectiveTo remains true of that window forever.
func (e *Engine) Revert(id, actor, reason string, effectiveTo, tick uint64) (Proposal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTickLocked(tick); err != nil {
		return Proposal{}, err
	}
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	if !canGo(p.State, StateReverted) {
		return Proposal{}, fmt.Errorf("%w: %s %s -> REVERTED", ErrIllegalTransition, id, p.State)
	}
	if effectiveTo < p.EffectiveFrom {
		return Proposal{}, fmt.Errorf("%w: cannot revert before it took effect", ErrRetroactive)
	}
	p.State = StateReverted
	p.EffectiveTo = effectiveTo
	e.appendLocked(Entry{ProposalID: id, From: StateActive, To: StateReverted, Tick: tick,
		Actor: actor, Detail: reason, Payload: hashProposal(*p)})
	return *p, nil
}

// ---------------------------------------------------------------------
// Versioned knowledge state
// ---------------------------------------------------------------------

// State is the resolved knowledge configuration at one tick.
type State struct {
	Tick       uint64             `json:"tick"`
	Rules      map[string]string  `json:"rules"`
	Relations  map[string]string  `json:"relations"`
	Weights    map[string]float64 `json:"weights"`
	Authority  map[string]float64 `json:"authority"`
	Parameters map[string]float64 `json:"parameters"`
	// Applied lists the proposal IDs in force at this tick, in ledger
	// order — the provenance of the state, not just its content.
	Applied  []string `json:"applied"`
	RootHash string   `json:"root_hash"`
}

// StateAt folds the ledger into the knowledge state in force at tick.
//
// This is the invariant-bearing method. It reads EffectiveFrom /
// EffectiveTo, never the proposal's current State field, so a proposal
// reverted at T2 still applies to StateAt(T1) for T1 in its window.
func (e *Engine) StateAt(tick uint64) State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stateAtLocked(tick)
}

func (e *Engine) stateAtLocked(tick uint64) State {
	s := State{Tick: tick,
		Rules: map[string]string{}, Relations: map[string]string{},
		Weights: map[string]float64{}, Authority: map[string]float64{},
		Parameters: map[string]float64{},
	}
	for _, id := range e.order {
		p := e.proposals[id]
		if p.EffectiveFrom == 0 || p.EffectiveFrom > tick {
			continue // never activated, or not yet in force at this tick
		}
		if p.EffectiveTo != 0 && tick >= p.EffectiveTo {
			continue // window has closed by this tick
		}
		s.Applied = append(s.Applied, p.ID)
		for _, m := range p.Mutations {
			switch m.Kind {
			case AddRule, ModifyRule:
				s.Rules[m.Target] = m.Body
			case RemoveRule:
				delete(s.Rules, m.Target)
			case AddRelation:
				s.Relations[m.Target] = m.Body
			case RemoveRelation:
				delete(s.Relations, m.Target)
			case ChangeWeight:
				s.Weights[m.Target] = m.Value
			case ChangeSourceAuthority:
				s.Authority[m.Target] = m.Value
			case ChangeModelParameter:
				s.Parameters[m.Target] = m.Value
			}
		}
	}
	s.RootHash = s.computeRoot()
	return s
}

func (s State) computeRoot() string {
	var sb strings.Builder
	sb.WriteString("knowledge_state/v1\n")
	writeStr := func(label string, m map[string]string) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(label + ":" + k + "=" + m[k] + "\n")
		}
	}
	writeNum := func(label string, m map[string]float64) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(label + ":" + k + "=" + strconv.FormatFloat(m[k], 'g', 17, 64) + "\n")
		}
	}
	writeStr("rule", s.Rules)
	writeStr("rel", s.Relations)
	writeNum("weight", s.Weights)
	writeNum("authority", s.Authority)
	writeNum("param", s.Parameters)
	for _, id := range s.Applied {
		sb.WriteString("applied=" + id + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// rootLocked is the current root, used to chain PreviousRoot/NewRoot.
func (e *Engine) rootLocked() string {
	return e.stateAtLocked(e.lastTick).RootHash
}

// Proposal returns a copy of a proposal.
func (e *Engine) Proposal(id string) (Proposal, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.proposals[id]
	if !ok {
		return Proposal{}, false
	}
	return *p, true
}

// Ledger returns a copy of the governance ledger.
func (e *Engine) Ledger() []Entry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Entry(nil), e.ledger...)
}

// Head returns the ledger tip hash.
func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.ledger) == 0 {
		return ""
	}
	return e.ledger[len(e.ledger)-1].Hash
}

// VerifyChain re-derives every hash and link.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, en := range e.ledger {
		if en.Index != uint64(i) {
			return fmt.Errorf("%w: index %d out of order", ErrLedgerTampered, en.Index)
		}
		if en.PrevHash != prev {
			return fmt.Errorf("%w: broken link at %d", ErrLedgerTampered, i)
		}
		if hashEntry(en) != en.Hash {
			return fmt.Errorf("%w: content hash mismatch at %d", ErrLedgerTampered, i)
		}
		prev = en.Hash
	}
	return nil
}

// Explain renders the derivation of a proposal's current position.
func (e *Engine) Explain(id string) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.proposals[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProposal, id)
	}
	var out []string
	for _, en := range e.ledger {
		if en.ProposalID != id {
			continue
		}
		line := "tick " + strconv.FormatUint(en.Tick, 10) + ": " + string(en.From) + " -> " +
			string(en.To) + " by " + en.Actor
		if en.Detail != "" {
			line += " (" + en.Detail + ")"
		}
		out = append(out, line)
	}
	return out, nil
}

func (e *Engine) checkTickLocked(tick uint64) error {
	if tick < e.lastTick {
		return fmt.Errorf("%w: %d < %d", ErrNonMonotonicTick, tick, e.lastTick)
	}
	e.lastTick = tick
	return nil
}

func (e *Engine) appendLocked(en Entry) {
	en.Index = uint64(len(e.ledger))
	if len(e.ledger) > 0 {
		en.PrevHash = e.ledger[len(e.ledger)-1].Hash
	}
	en.Hash = hashEntry(en)
	e.ledger = append(e.ledger, en)
}

func proposalID(p Proposal) string {
	var sb strings.Builder
	sb.WriteString("knowledge_proposal_id/v1\nproposer=" + p.Proposer +
		"\ntick=" + strconv.FormatUint(p.ProposedTick, 10) + "\nreason=" + p.Reason + "\n")
	for _, m := range p.Mutations {
		sb.WriteString("m=" + m.hash() + "\n")
	}
	for _, r := range p.EvidenceRefs {
		sb.WriteString("ref=" + r + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return "kp-" + hex.EncodeToString(sum[:])[:24]
}

func hashProposal(p Proposal) string {
	var sb strings.Builder
	sb.WriteString("knowledge_proposal/v1\nid=" + p.ID + "\nstate=" + string(p.State) +
		"\nproposer=" + p.Proposer + "\napprover=" + p.Approver +
		"\nfrom=" + strconv.FormatUint(p.EffectiveFrom, 10) +
		"\nto=" + strconv.FormatUint(p.EffectiveTo, 10) +
		"\nsim=" + p.Simulation.Hash + "\nprev_root=" + p.PreviousRoot +
		"\nnew_root=" + p.NewRoot + "\nseverity=" + p.Impact.Severity + "\n")
	for _, m := range p.Mutations {
		sb.WriteString("m=" + m.hash() + "\n")
	}
	for _, c := range p.Conflict.ConflictsWith {
		sb.WriteString("conflict=" + c + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func hashEntry(en Entry) string {
	s := "knowledge_entry/v1\ni=" + strconv.FormatUint(en.Index, 10) +
		"\npid=" + en.ProposalID + "\nfrom=" + string(en.From) + "\nto=" + string(en.To) +
		"\ntick=" + strconv.FormatUint(en.Tick, 10) + "\nactor=" + en.Actor +
		"\ndetail=" + en.Detail + "\npayload=" + en.Payload + "\nprev=" + en.PrevHash + "\n"
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
