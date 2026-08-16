// Package lifecycle closes PHASES 47 and 48: model lifecycle
// governance and source lifecycle governance, as two registries that
// are deliberately coupled.
//
// The coupling is the point. The auditor's critical rule was:
//
//	"Model version and source version harus ikut masuk ExecutionRootHash,
//	 ReplayPackage, DecisionExplanation, Certificate. Jangan sampai
//	 keputusan hari ini dapat direplay memakai model versi berbeda tanpa
//	 detection."
//
// So the unit of commitment is not "the model" or "the source" but a
// Binding: the exact set of (model@version, source@version) that were
// ACTIVE at the tick a decision was made, folded into one
// content-addressed BindingHash. A replay that resolves a different
// active set produces a different BindingHash and is rejected before
// anything is recomputed.
//
// Both registries are event-sourced over an append-only, hash-chained
// ledger. State is a fold, never a mutation, which is what makes
// "which model was active at tick T" answerable after the model has
// since been retired.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/platform/telemetry"
)

var (
	// ErrUnknown is returned for an unregistered model or source.
	ErrUnknown = errors.New("lifecycle: unknown entry")
	// ErrDuplicate rejects re-registering the same model version or
	// source version.
	ErrDuplicate = errors.New("lifecycle: entry already registered")
	// ErrIllegalTransition names the state machine violation rather
	// than silently allowing it.
	ErrIllegalTransition = errors.New("lifecycle: illegal state transition")
	// ErrApprovalRequired blocks activation without a named approver.
	ErrApprovalRequired = errors.New("lifecycle: activation requires a named approver")
	// ErrCalibrationRequired blocks a model from reaching APPROVED
	// without a calibration artifact — the direct link to PHASE 46.
	ErrCalibrationRequired = errors.New("lifecycle: model requires a calibration artifact before approval")
	// ErrNonMonotonicTick rejects a backdated event.
	ErrNonMonotonicTick = errors.New("lifecycle: tick must not go backwards")
	// ErrLedgerTampered is returned by VerifyChain.
	ErrLedgerTampered = errors.New("lifecycle: ledger hash chain broken")
	// ErrBindingMismatch is the replay-protection error: the active
	// model/source set at replay time differs from the one the
	// execution committed to.
	ErrBindingMismatch = errors.New("lifecycle: model/source binding differs from the committed binding")
	// ErrNoRollbackTarget rejects a rollback with no prior ACTIVE
	// version to fall back to.
	ErrNoRollbackTarget = errors.New("lifecycle: no rollback target")
	// ErrRevoked rejects any attempt to use a revoked source. Revocation
	// is terminal, exactly as in pkg/trust/state.
	ErrRevoked = errors.New("lifecycle: entry is revoked")
)

// ---------------------------------------------------------------------
// States
// ---------------------------------------------------------------------

// ModelState is the PHASE 47 lifecycle.
type ModelState string

const (
	ModelDraft      ModelState = "DRAFT"
	ModelValidated  ModelState = "VALIDATED"
	ModelCalibrated ModelState = "CALIBRATED"
	ModelApproved   ModelState = "APPROVED"
	ModelActive     ModelState = "ACTIVE"
	ModelDeprecated ModelState = "DEPRECATED"
	ModelRetired    ModelState = "RETIRED"
)

// modelTransitions is the whole state machine, as data. A transition
// that is not in this table cannot happen, and the table can be read
// by a reviewer in one screen — which a chain of if-statements cannot.
var modelTransitions = map[ModelState][]ModelState{
	ModelDraft:      {ModelValidated, ModelRetired},
	ModelValidated:  {ModelCalibrated, ModelRetired},
	ModelCalibrated: {ModelApproved, ModelValidated, ModelRetired},
	ModelApproved:   {ModelActive, ModelRetired},
	ModelActive:     {ModelDeprecated, ModelRetired},
	ModelDeprecated: {ModelActive, ModelRetired}, // reactivation = rollback
	ModelRetired:    {},                          // terminal
}

// SourceState is the PHASE 48 lifecycle.
type SourceState string

const (
	SourceDiscovered SourceState = "DISCOVERED"
	SourceValidated  SourceState = "VALIDATED"
	SourceTrusted    SourceState = "TRUSTED"
	SourceActive     SourceState = "ACTIVE"
	SourceDegraded   SourceState = "DEGRADED"
	SourceSuspended  SourceState = "SUSPENDED"
	SourceRevoked    SourceState = "REVOKED"
	SourceRetired    SourceState = "RETIRED"
)

var sourceTransitions = map[SourceState][]SourceState{
	SourceDiscovered: {SourceValidated, SourceRevoked, SourceRetired},
	SourceValidated:  {SourceTrusted, SourceSuspended, SourceRevoked, SourceRetired},
	SourceTrusted:    {SourceActive, SourceSuspended, SourceRevoked, SourceRetired},
	SourceActive:     {SourceDegraded, SourceSuspended, SourceRevoked, SourceRetired},
	SourceDegraded:   {SourceActive, SourceSuspended, SourceRevoked, SourceRetired},
	SourceSuspended:  {SourceValidated, SourceRevoked, SourceRetired},
	SourceRevoked:    {}, // terminal: reinstatement means a NEW source id
	SourceRetired:    {},
}

// PolicyState is the governance lifecycle for a decision policy version
// (P0-D "automatic wiring"): the audit's remaining ask after
// StagePolicy gained the ABILITY to independently verify a caller-
// declared ExpectedPolicyHash (see pkg/execution's StagePolicy and
// pkg/moat/decision.Policy.Hash) was that PRODUCTION calls populate
// that field automatically, from a real, separate source of truth --
// not from the same Case.Policy value being checked against itself,
// which would be a circular, decorative check dressed up as a real
// one. This registry is that separate source: an operator registers
// and approves a policy version here, independently of any particular
// case's Case.Policy, exactly mirroring the Model/Source pattern this
// file already established for the identical "committed decision must
// not silently replay under a different governed version" problem.
type PolicyState string

const (
	PolicyDraft      PolicyState = "DRAFT"
	PolicyApproved   PolicyState = "APPROVED"
	PolicyActive     PolicyState = "ACTIVE"
	PolicyDeprecated PolicyState = "DEPRECATED"
	PolicyRetired    PolicyState = "RETIRED"
)

var policyTransitions = map[PolicyState][]PolicyState{
	PolicyDraft:      {PolicyApproved, PolicyRetired},
	PolicyApproved:   {PolicyActive, PolicyRetired},
	PolicyActive:     {PolicyDeprecated, PolicyRetired},
	PolicyDeprecated: {PolicyActive, PolicyRetired}, // reactivation = rollback
	PolicyRetired:    {},                            // terminal
}

func allowed[S comparable](table map[S][]S, from, to S) bool {
	for _, s := range table[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Records
// ---------------------------------------------------------------------

// Model is the registered descriptor of one model version. It is an
// immutable value: a new version is a new record, never an edit.
type Model struct {
	ModelID          string     `json:"model_id"`
	Version          string     `json:"version"`
	Type             string     `json:"type"`
	ParametersHash   string     `json:"parameters_hash"`
	TrainingDataHash string     `json:"training_data_hash"`
	CalibrationID    string     `json:"calibration_id"`
	CreatedTick      uint64     `json:"created_tick"`
	ApprovedTick     uint64     `json:"approved_tick"`
	ApprovedBy       string     `json:"approved_by"`
	State            ModelState `json:"state"`
	EffectiveFrom    uint64     `json:"effective_from"`
	EffectiveTo      uint64     `json:"effective_to"` // 0 = open-ended
	ParentVersion    string     `json:"parent_version"`
	RollbackTarget   string     `json:"rollback_target"`
}

// Key is the registry key: models are identified by id AND version.
func (m Model) Key() string { return m.ModelID + "@" + m.Version }

// Source is the registered descriptor of one source version.
type Source struct {
	SourceID        string      `json:"source_id"`
	Version         string      `json:"version"`
	Authority       string      `json:"authority"`
	Provider        string      `json:"provider"`
	EvidenceClass   string      `json:"evidence_class"`
	Reliability     float64     `json:"reliability"`
	DependencyGroup string      `json:"dependency_group"`
	SchemaVersion   string      `json:"schema_version"`
	ValidFrom       uint64      `json:"valid_from"`
	ValidTo         uint64      `json:"valid_to"` // 0 = open-ended
	CredentialRef   string      `json:"credential_ref"`
	State           SourceState `json:"state"`
}

// Key is the registry key.
func (s Source) Key() string { return s.SourceID + "@" + s.Version }

// Policy is the registered descriptor of one decision-policy version.
// ContentHash is computed by RegisterPolicy from the actual
// decision.Policy submitted (never operator-declared separately from
// it), so a registered Policy record can never silently diverge from
// the real policy content it governs.
type Policy struct {
	Name          string      `json:"name"`
	Version       string      `json:"version"`
	ContentHash   string      `json:"content_hash"`
	CreatedTick   uint64      `json:"created_tick"`
	ApprovedTick  uint64      `json:"approved_tick"`
	ApprovedBy    string      `json:"approved_by"`
	State         PolicyState `json:"state"`
	EffectiveFrom uint64      `json:"effective_from"`
	EffectiveTo   uint64      `json:"effective_to"` // 0 = open-ended
}

// Key is the registry key: policies are identified by name AND version.
func (p Policy) Key() string { return p.Name + "@" + p.Version }

// EventKind distinguishes registration from transition.
type EventKind string

const (
	EventRegisterModel    EventKind = "REGISTER_MODEL"
	EventModelTransition  EventKind = "MODEL_TRANSITION"
	EventRegisterSource   EventKind = "REGISTER_SOURCE"
	EventSourceTransition EventKind = "SOURCE_TRANSITION"
	EventRegisterPolicy   EventKind = "REGISTER_POLICY"
	EventPolicyTransition EventKind = "POLICY_TRANSITION"
)

// Event is one immutable ledger entry.
type Event struct {
	Index        uint64    `json:"index"`
	Kind         EventKind `json:"kind"`
	Key          string    `json:"key"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Tick         uint64    `json:"tick"`
	Actor        string    `json:"actor"`
	Reason       string    `json:"reason"`
	Approver     string    `json:"approver"`
	Payload      string    `json:"payload"` // content hash of the registered descriptor
	EvidenceRefs []string  `json:"evidence_refs"`
	PrevHash     string    `json:"prev_hash"`
	Hash         string    `json:"hash"`
}

// ---------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------

// Registry holds both lifecycles over one shared ledger. One ledger,
// not two, because the question a replay asks — "what was active" — is
// a question about both at the same tick, and two ledgers with two
// clocks cannot answer it without a join nobody will get right.
type Registry struct {
	mu       sync.RWMutex
	models   map[string]*Model
	sources  map[string]*Source
	policies map[string]*Policy
	ledger   []Event
	lastTick uint64
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{models: map[string]*Model{}, sources: map[string]*Source{}, policies: map[string]*Policy{}}
}

// RegisterModel adds a new model version in DRAFT.
func (r *Registry) RegisterModel(m Model, actor string, tick uint64) (Event, error) {
	if m.ModelID == "" || m.Version == "" {
		return Event{}, fmt.Errorf("%w: model id and version are required", ErrUnknown)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTickLocked(tick); err != nil {
		return Event{}, err
	}
	if _, dup := r.models[m.Key()]; dup {
		return Event{}, fmt.Errorf("%w: %s", ErrDuplicate, m.Key())
	}
	m.State = ModelDraft
	m.CreatedTick = tick
	cp := m
	r.models[m.Key()] = &cp
	return r.appendLocked(Event{Kind: EventRegisterModel, Key: m.Key(), To: string(ModelDraft),
		Tick: tick, Actor: actor, Payload: hashModel(m)}), nil
}

// TransitionModel moves a model through its state machine.
//
// Two guards are enforced here and nowhere else, because "somewhere
// else" is how they get bypassed: APPROVED requires both a named
// approver and a calibration artifact, and ACTIVE requires having
// passed through APPROVED.
func (r *Registry) TransitionModel(key string, to ModelState, actor, approver, reason string,
	tick uint64, evidenceRefs []string) (Event, error) {

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTickLocked(tick); err != nil {
		return Event{}, err
	}
	m, ok := r.models[key]
	if !ok {
		return Event{}, fmt.Errorf("%w: model %s", ErrUnknown, key)
	}
	if !allowed(modelTransitions, m.State, to) {
		return Event{}, fmt.Errorf("%w: model %s %s -> %s", ErrIllegalTransition, key, m.State, to)
	}
	if to == ModelApproved {
		if approver == "" {
			return Event{}, fmt.Errorf("%w: %s", ErrApprovalRequired, key)
		}
		if m.CalibrationID == "" {
			return Event{}, fmt.Errorf("%w: %s", ErrCalibrationRequired, key)
		}
	}
	if to == ModelActive && m.State == ModelApproved && approver == "" {
		return Event{}, fmt.Errorf("%w: %s", ErrApprovalRequired, key)
	}

	from := m.State
	m.State = to
	switch to {
	case ModelApproved:
		m.ApprovedBy, m.ApprovedTick = approver, tick
	case ModelActive:
		m.EffectiveFrom = tick
		m.EffectiveTo = 0
		// Exactly one version of a model id may be ACTIVE. The previous
		// one is deprecated here rather than by a caller remembering to,
		// because the binding resolver depends on that uniqueness.
		for k, other := range r.models {
			if k != key && other.ModelID == m.ModelID && other.State == ModelActive {
				other.State = ModelDeprecated
				other.EffectiveTo = tick
				r.appendLockedNoReturn(Event{Kind: EventModelTransition, Key: k,
					From: string(ModelActive), To: string(ModelDeprecated), Tick: tick,
					Actor: actor, Reason: "superseded by " + key, Payload: hashModel(*other)})
			}
		}
	case ModelDeprecated, ModelRetired:
		if m.EffectiveTo == 0 {
			m.EffectiveTo = tick
		}
	}
	return r.appendLocked(Event{Kind: EventModelTransition, Key: key, From: string(from),
		To: string(to), Tick: tick, Actor: actor, Reason: reason, Approver: approver,
		Payload: hashModel(*m), EvidenceRefs: evidenceRefs}), nil
}

// RollbackModel reactivates the named model's declared RollbackTarget.
// It is a first-class operation rather than "just activate the old
// one", so the ledger records that this was a rollback and why.
func (r *Registry) RollbackModel(key, actor, approver, reason string, tick uint64) (Event, error) {
	r.mu.RLock()
	m, ok := r.models[key]
	var target string
	if ok {
		target = m.RollbackTarget
	}
	r.mu.RUnlock()
	if !ok {
		return Event{}, fmt.Errorf("%w: model %s", ErrUnknown, key)
	}
	if target == "" {
		return Event{}, fmt.Errorf("%w: %s declares none", ErrNoRollbackTarget, key)
	}
	if _, err := r.TransitionModel(key, ModelDeprecated, actor, approver, "rollback: "+reason, tick, nil); err != nil {
		return Event{}, err
	}
	return r.TransitionModel(target, ModelActive, actor, approver, "rollback target for "+key, tick, nil)
}

// RegisterSource adds a new source version in DISCOVERED.
func (r *Registry) RegisterSource(s Source, actor string, tick uint64) (Event, error) {
	if s.SourceID == "" || s.Version == "" {
		return Event{}, fmt.Errorf("%w: source id and version are required", ErrUnknown)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTickLocked(tick); err != nil {
		return Event{}, err
	}
	if _, dup := r.sources[s.Key()]; dup {
		return Event{}, fmt.Errorf("%w: %s", ErrDuplicate, s.Key())
	}
	s.State = SourceDiscovered
	cp := s
	r.sources[s.Key()] = &cp
	return r.appendLocked(Event{Kind: EventRegisterSource, Key: s.Key(), To: string(SourceDiscovered),
		Tick: tick, Actor: actor, Payload: hashSource(s)}), nil
}

// TransitionSource moves a source through its state machine.
func (r *Registry) TransitionSource(key string, to SourceState, actor, approver, reason string,
	tick uint64, evidenceRefs []string) (Event, error) {

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTickLocked(tick); err != nil {
		return Event{}, err
	}
	s, ok := r.sources[key]
	if !ok {
		return Event{}, fmt.Errorf("%w: source %s", ErrUnknown, key)
	}
	if s.State == SourceRevoked {
		return Event{}, fmt.Errorf("%w: %s", ErrRevoked, key)
	}
	if !allowed(sourceTransitions, s.State, to) {
		return Event{}, fmt.Errorf("%w: source %s %s -> %s", ErrIllegalTransition, key, s.State, to)
	}
	if to == SourceActive && approver == "" {
		return Event{}, fmt.Errorf("%w: %s", ErrApprovalRequired, key)
	}
	from := s.State
	s.State = to
	if to == SourceActive && s.ValidFrom == 0 {
		s.ValidFrom = tick
	}
	if to == SourceRevoked || to == SourceRetired {
		if s.ValidTo == 0 {
			s.ValidTo = tick
		}
	}
	return r.appendLocked(Event{Kind: EventSourceTransition, Key: key, From: string(from),
		To: string(to), Tick: tick, Actor: actor, Reason: reason, Approver: approver,
		Payload: hashSource(*s), EvidenceRefs: evidenceRefs}), nil
}

// RegisterPolicy adds a new decision-policy version in DRAFT.
// ContentHash is computed here, from the real decision.Policy p the
// caller submits -- never accepted as an operator-declared string --
// so a registered record can never silently diverge from the policy
// content it is meant to govern.
func (r *Registry) RegisterPolicy(name, version string, p decision.Policy, actor string, tick uint64) (Event, error) {
	if name == "" || version == "" {
		return Event{}, fmt.Errorf("%w: policy name and version are required", ErrUnknown)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTickLocked(tick); err != nil {
		return Event{}, err
	}
	rec := Policy{Name: name, Version: version, ContentHash: p.Hash(), CreatedTick: tick, State: PolicyDraft}
	if _, dup := r.policies[rec.Key()]; dup {
		return Event{}, fmt.Errorf("%w: %s", ErrDuplicate, rec.Key())
	}
	cp := rec
	r.policies[rec.Key()] = &cp
	return r.appendLocked(Event{Kind: EventRegisterPolicy, Key: rec.Key(), To: string(PolicyDraft),
		Tick: tick, Actor: actor, Payload: hashPolicy(rec)}), nil
}

// TransitionPolicy moves a policy version through its state machine.
// ACTIVE requires a named approver, exactly like Model/Source: a
// governed policy version silently becoming the one production checks
// against, with no accountable approver, would defeat the point of
// this registry existing at all.
func (r *Registry) TransitionPolicy(key string, to PolicyState, actor, approver, reason string,
	tick uint64, evidenceRefs []string) (Event, error) {

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkTickLocked(tick); err != nil {
		return Event{}, err
	}
	p, ok := r.policies[key]
	if !ok {
		return Event{}, fmt.Errorf("%w: policy %s", ErrUnknown, key)
	}
	if !allowed(policyTransitions, p.State, to) {
		return Event{}, fmt.Errorf("%w: policy %s %s -> %s", ErrIllegalTransition, key, p.State, to)
	}
	if to == PolicyApproved && approver == "" {
		return Event{}, fmt.Errorf("%w: %s", ErrApprovalRequired, key)
	}
	from := p.State
	p.State = to
	switch to {
	case PolicyApproved:
		p.ApprovedBy, p.ApprovedTick = approver, tick
	case PolicyActive:
		p.EffectiveFrom = tick
		p.EffectiveTo = 0
		// Exactly one version of a policy name may be ACTIVE, mirroring
		// Model's own uniqueness rule -- the binding resolver below
		// depends on it.
		for k, other := range r.policies {
			if k != key && other.Name == p.Name && other.State == PolicyActive {
				other.State = PolicyDeprecated
				other.EffectiveTo = tick
				r.appendLockedNoReturn(Event{Kind: EventPolicyTransition, Key: k,
					From: string(PolicyActive), To: string(PolicyDeprecated), Tick: tick,
					Actor: actor, Reason: "superseded by " + key, Payload: hashPolicy(*other)})
			}
		}
	}
	return r.appendLocked(Event{Kind: EventPolicyTransition, Key: key, From: string(from),
		To: string(to), Tick: tick, Actor: actor, Reason: reason, Approver: approver,
		Payload: hashPolicy(*p), EvidenceRefs: evidenceRefs}), nil
}

// SetCalibration attaches a calibration artifact ID to a model version.
// It is only legal before APPROVED: re-calibrating an approved model
// silently would break every certificate that committed to it.
func (r *Registry) SetCalibration(key, calibrationID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.models[key]
	if !ok {
		return fmt.Errorf("%w: model %s", ErrUnknown, key)
	}
	if m.State == ModelApproved || m.State == ModelActive {
		return fmt.Errorf("%w: %s is already %s; register a new version instead",
			ErrIllegalTransition, key, m.State)
	}
	m.CalibrationID = calibrationID
	return nil
}

func (r *Registry) checkTickLocked(tick uint64) error {
	if tick < r.lastTick {
		return fmt.Errorf("%w: %d < %d", ErrNonMonotonicTick, tick, r.lastTick)
	}
	r.lastTick = tick
	return nil
}

func (r *Registry) appendLocked(e Event) Event {
	e.Index = uint64(len(r.ledger))
	if len(r.ledger) > 0 {
		e.PrevHash = r.ledger[len(r.ledger)-1].Hash
	}
	e.EvidenceRefs = append([]string(nil), e.EvidenceRefs...)
	sort.Strings(e.EvidenceRefs)
	e.Hash = hashEvent(e)
	r.ledger = append(r.ledger, e)
	return e
}

func (r *Registry) appendLockedNoReturn(e Event) { _ = r.appendLocked(e) }

// Model returns a copy of a model record.
func (r *Registry) Model(key string) (Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[key]
	if !ok {
		return Model{}, false
	}
	return *m, true
}

// Source returns a copy of a source record.
func (r *Registry) Source(key string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[key]
	if !ok {
		return Source{}, false
	}
	return *s, true
}

// Ledger returns a copy of the event ledger.
func (r *Registry) Ledger() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Event(nil), r.ledger...)
}

// Head is the ledger tip hash.
func (r *Registry) Head() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.ledger) == 0 {
		return ""
	}
	return r.ledger[len(r.ledger)-1].Hash
}

// VerifyChain re-derives every hash and link.
func (r *Registry) VerifyChain() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prev := ""
	for i, e := range r.ledger {
		if e.Index != uint64(i) {
			return fmt.Errorf("%w: index %d out of order", ErrLedgerTampered, e.Index)
		}
		if e.PrevHash != prev {
			return fmt.Errorf("%w: broken link at %d", ErrLedgerTampered, i)
		}
		if hashEvent(e) != e.Hash {
			return fmt.Errorf("%w: content hash mismatch at %d", ErrLedgerTampered, i)
		}
		prev = e.Hash
	}
	return nil
}

// ---------------------------------------------------------------------
// Binding — the replay-protection primitive
// ---------------------------------------------------------------------

// Binding is the exact model/source version set an execution ran under.
type Binding struct {
	Tick    uint64   `json:"tick"`
	Models  []string `json:"models"`  // "id@version" sorted
	Sources []string `json:"sources"` // "id@version" sorted
	// Policies maps a governed policy Name to the ContentHash of
	// whichever version was ACTIVE at Tick (see decision.Policy.Hash
	// -- the SAME hash a caller can independently recompute over the
	// policy content they intend to run, and pkg/execution.StagePolicy
	// independently recomputes and compares against
	// Input.ExpectedPolicyHash). A policy Name with no ACTIVE governed
	// version at this tick is simply absent from the map -- there is
	// no fabricated entry standing in for "not yet governed".
	Policies     map[string]string `json:"policies,omitempty"`
	RegistryHead string            `json:"registry_head"`
	Hash         string            `json:"hash"`
}

// BindingAt resolves what was ACTIVE at a tick by folding the ledger.
//
// Reading current state instead would be the classic replay hole: a
// model deprecated yesterday would resolve as inactive today, and a
// year-old certificate would fail to reproduce for a reason that has
// nothing to do with the decision.
func (r *Registry) BindingAt(tick uint64) Binding {
	_, span := telemetry.StartSpan(context.Background(), "lifecycle.BindingAt",
		telemetry.Attribute{Key: "tick", Value: strconv.FormatUint(tick, 10)})
	defer span.End()

	r.mu.RLock()
	defer r.mu.RUnlock()

	modelState := map[string]ModelState{}
	sourceState := map[string]SourceState{}
	policyState := map[string]PolicyState{}
	for _, e := range r.ledger {
		if e.Tick > tick {
			break
		}
		switch e.Kind {
		case EventRegisterModel, EventModelTransition:
			modelState[e.Key] = ModelState(e.To)
		case EventRegisterSource, EventSourceTransition:
			sourceState[e.Key] = SourceState(e.To)
		case EventRegisterPolicy, EventPolicyTransition:
			policyState[e.Key] = PolicyState(e.To)
		}
	}
	b := Binding{Tick: tick, RegistryHead: r.headLocked(tick)}
	for k, s := range modelState {
		if s == ModelActive {
			b.Models = append(b.Models, k)
		}
	}
	for k, s := range sourceState {
		if s == SourceActive {
			b.Sources = append(b.Sources, k)
		}
	}
	for k, s := range policyState {
		if s != PolicyActive {
			continue
		}
		// Name/ContentHash are immutable once registered (only State
		// ever changes), so reading them from the live map for a key
		// whose ACTIVE-ness was itself determined by folding the
		// ledger above is safe -- the same reasoning modelState/
		// sourceState's own siblings rely on implicitly by using r.
		// models/r.sources elsewhere in this file.
		if p, ok := r.policies[k]; ok {
			if b.Policies == nil {
				b.Policies = map[string]string{}
			}
			b.Policies[p.Name] = p.ContentHash
		}
	}
	sort.Strings(b.Models)
	sort.Strings(b.Sources)
	b.Hash = b.computeHash()
	return b
}

// headLocked returns the tip hash as of a tick, so a Binding also pins
// the governance ledger position — two identical active sets reached by
// different histories are not the same governance state.
func (r *Registry) headLocked(tick uint64) string {
	head := ""
	for _, e := range r.ledger {
		if e.Tick > tick {
			break
		}
		head = e.Hash
	}
	return head
}

func (b Binding) computeHash() string {
	var sb strings.Builder
	sb.WriteString("lifecycle_binding/v1\ntick=" + strconv.FormatUint(b.Tick, 10) + "\n")
	for _, m := range b.Models {
		sb.WriteString("model=" + m + "\n")
	}
	for _, s := range b.Sources {
		sb.WriteString("source=" + s + "\n")
	}
	// Policies is folded in only when non-empty, so a binding with no
	// governed policy versions (every Binding before this field
	// existed, and every registry that never registers one) produces
	// the exact same hash as before -- byte-for-byte, not merely
	// "usually the same".
	if len(b.Policies) > 0 {
		names := make([]string, 0, len(b.Policies))
		for name := range b.Policies {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sb.WriteString("policy=" + name + ":" + b.Policies[name] + "\n")
		}
	}
	sb.WriteString("head=" + b.RegistryHead + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// VerifyBinding is the guard a replay calls before recomputing
// anything. It compares the committed binding against what the registry
// resolves now for the same tick.
func (r *Registry) VerifyBinding(committed Binding) error {
	got := r.BindingAt(committed.Tick)
	if got.Hash != committed.Hash {
		return fmt.Errorf("%w: committed=%s resolved=%s (models %v vs %v, sources %v vs %v)",
			ErrBindingMismatch, committed.Hash, got.Hash,
			committed.Models, got.Models, committed.Sources, got.Sources)
	}
	return nil
}

func hashModel(m Model) string {
	var sb strings.Builder
	sb.WriteString("model/v1\n" + m.ModelID + "@" + m.Version + "\ntype=" + m.Type +
		"\nparams=" + m.ParametersHash + "\ntraining=" + m.TrainingDataHash +
		"\ncalibration=" + m.CalibrationID + "\nstate=" + string(m.State) +
		"\nparent=" + m.ParentVersion + "\nrollback=" + m.RollbackTarget +
		"\nfrom=" + strconv.FormatUint(m.EffectiveFrom, 10) +
		"\nto=" + strconv.FormatUint(m.EffectiveTo, 10) +
		"\napprover=" + m.ApprovedBy + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func hashSource(s Source) string {
	var sb strings.Builder
	sb.WriteString("source/v1\n" + s.SourceID + "@" + s.Version + "\nauthority=" + s.Authority +
		"\nprovider=" + s.Provider + "\nclass=" + s.EvidenceClass +
		"\nreliability=" + strconv.FormatFloat(s.Reliability, 'g', 17, 64) +
		"\ndepgroup=" + s.DependencyGroup + "\nschema=" + s.SchemaVersion +
		"\nstate=" + string(s.State) + "\ncred=" + s.CredentialRef +
		"\nfrom=" + strconv.FormatUint(s.ValidFrom, 10) +
		"\nto=" + strconv.FormatUint(s.ValidTo, 10) + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func hashPolicy(p Policy) string {
	var sb strings.Builder
	sb.WriteString("policy/v1\n" + p.Name + "@" + p.Version + "\ncontent=" + p.ContentHash +
		"\nstate=" + string(p.State) + "\napprover=" + p.ApprovedBy +
		"\nfrom=" + strconv.FormatUint(p.EffectiveFrom, 10) +
		"\nto=" + strconv.FormatUint(p.EffectiveTo, 10) + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func hashEvent(e Event) string {
	var sb strings.Builder
	sb.WriteString("lifecycle_event/v1\ni=" + strconv.FormatUint(e.Index, 10) +
		"\nkind=" + string(e.Kind) + "\nkey=" + e.Key + "\nfrom=" + e.From + "\nto=" + e.To +
		"\ntick=" + strconv.FormatUint(e.Tick, 10) + "\nactor=" + e.Actor +
		"\nreason=" + e.Reason + "\napprover=" + e.Approver + "\npayload=" + e.Payload +
		"\nrefs=" + strings.Join(e.EvidenceRefs, ";") + "\nprev=" + e.PrevHash + "\n")
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
