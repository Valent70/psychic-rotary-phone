// Package modelregistry governs the models VERIQO uses.
//
// # Why a model needs a lifecycle at all
//
// A model is not a library. It changes behaviour without changing its
// version string, it degrades against drifting data, and its output
// underwrites conclusions somebody will be asked to defend. So every
// model carries a stage, and the stage gates what its output may be
// used for:
//
//	DEVELOPMENT  not usable on a case at all
//	VALIDATION   usable on a case, output stays DRAFT
//	QUALIFIED    output may enter the qualification ladder
//	PRODUCTION   qualified and deployed
//	DEPRECATED   still readable for replay, not usable for new work
//	REVOKED      output produced under it is suspect
//
// # REVOKED is the state that matters
//
// A revoked model is not merely unusable going forward. Everything it
// produced becomes suspect, and the registry can enumerate what that
// is -- which is the difference between "we stopped using it" and "we
// know what it touched".
//
// # Promotion needs evidence, and it is one step at a time
//
// The same discipline as the AI ladder and the assurance ladder: a
// model reaches QUALIFIED because an evaluation was recorded, not
// because somebody set a field.
package modelregistry

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
)

var (
	ErrUnknownModel = errors.New("modelregistry: unknown model")
	ErrUnknownStage = errors.New("modelregistry: unknown stage")
	ErrSkippedStage = errors.New("modelregistry: a model advances one stage at a time")
	ErrNoEvaluation = errors.New("modelregistry: this stage requires a recorded evaluation")
	ErrNoApprover   = errors.New("modelregistry: a stage change must name who approved it")
	ErrRevoked      = errors.New("modelregistry: the model is revoked")
	ErrNotUsable    = errors.New("modelregistry: the model may not be used for this")
	ErrDuplicate    = errors.New("modelregistry: the model version is already registered")
	ErrNoCutoff     = errors.New("modelregistry: a model whose training cutoff is unknown must say so")
)

// Stage is a model's position in its lifecycle.
type Stage string

const (
	Development Stage = "DEVELOPMENT"
	Validation  Stage = "VALIDATION"
	Qualified   Stage = "QUALIFIED"
	Production  Stage = "PRODUCTION"
	Deprecated  Stage = "DEPRECATED"
	Revoked     Stage = "REVOKED"
)

// Stages returns the forward ladder. DEPRECATED and REVOKED are
// reachable from anywhere and are not part of it.
func Stages() []Stage {
	return []Stage{Development, Validation, Qualified, Production}
}

func (s Stage) Valid() bool {
	switch s {
	case Development, Validation, Qualified, Production, Deprecated, Revoked:
		return true
	}
	return false
}

// UsableOnACase reports whether a model may run against real case
// material at all.
func (s Stage) UsableOnACase() bool {
	switch s {
	case Validation, Qualified, Production:
		return true
	}
	return false
}

// OutputMayBeQualified reports whether output may enter the
// qualification ladder above DRAFT.
func (s Stage) OutputMayBeQualified() bool {
	return s == Qualified || s == Production
}

// UsableForReplay reports whether a model may be consulted to
// reconstruct a past execution. A deprecated model still can -- that
// is what deprecation means as opposed to revocation.
func (s Stage) UsableForReplay() bool { return s != Revoked }

func rank(s Stage) int {
	for i, x := range Stages() {
		if x == s {
			return i
		}
	}
	return -1
}

// Evaluation is the evidence behind a stage change.
type Evaluation struct {
	ID string `json:"id"`
	// Dataset names what it was evaluated on. "Our own test set" is a
	// real answer and it is a different one from a held-out corpus.
	Dataset string `json:"dataset"`
	// DatasetExternal marks an evaluation over data VERIQO did not
	// create. Required for QUALIFIED and above.
	DatasetExternal bool `json:"dataset_external"`
	// Metrics are the measured results.
	Metrics map[string]float64 `json:"metrics"`
	// Limitations state what the evaluation does not establish. An
	// evaluation with none has not been thought about.
	Limitations []string  `json:"limitations"`
	At          time.Time `json:"at"`
	By          string    `json:"by"`
}

func (e Evaluation) Validate() error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Dataset) == "" {
		return errors.New("modelregistry: an evaluation needs an id and a dataset")
	}
	if len(e.Metrics) == 0 {
		return fmt.Errorf("modelregistry: evaluation %s records no metric", e.ID)
	}
	if len(e.Limitations) == 0 {
		return fmt.Errorf("modelregistry: evaluation %s states no limitations; an evaluation "+
			"that establishes everything has not been thought about", e.ID)
	}
	if e.At.IsZero() || strings.TrimSpace(e.By) == "" {
		return fmt.Errorf("modelregistry: evaluation %s does not say when or by whom", e.ID)
	}
	return nil
}

// Transition is a recorded stage change.
type Transition struct {
	From         Stage       `json:"from"`
	To           Stage       `json:"to"`
	At           time.Time   `json:"at"`
	ApprovedBy   contract.ID `json:"approved_by"`
	Reason       string      `json:"reason"`
	EvaluationID string      `json:"evaluation_id,omitempty"`
}

// Model is a registered model version.
type Model struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Provider string `json:"provider"`

	// WeightsRef points at the weights or the provider's model
	// identifier. It is what makes "the same model" checkable.
	WeightsRef string `json:"weights_ref"`

	// PromptVersion and Temperature are part of the model's identity
	// for VERIQO's purposes: the same weights at a different
	// temperature is a different behaviour.
	PromptVersion contract.Version `json:"prompt_version"`
	Temperature   float64          `json:"temperature"`

	// TrainingCutoff is when the model's knowledge ends.
	// CutoffUnknown must be set when it is not published -- a zero
	// time that means "unknown" and a zero time that means "not
	// recorded" are indistinguishable otherwise.
	TrainingCutoff *time.Time `json:"training_cutoff,omitempty"`
	CutoffUnknown  bool       `json:"cutoff_unknown"`

	Stage       Stage        `json:"stage"`
	Evaluations []Evaluation `json:"evaluations,omitempty"`
	History     []Transition `json:"history,omitempty"`
}

// Key is the registry key: a model is its id AND its version.
func (m Model) Key() string { return m.ID + "@" + m.Version }

func (m Model) Validate() error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Version) == "" {
		return errors.New("modelregistry: a model needs an id and a version")
	}
	if strings.TrimSpace(m.Provider) == "" || strings.TrimSpace(m.WeightsRef) == "" {
		return fmt.Errorf("modelregistry: %s names no provider or weights reference; "+
			"'the same model' would not be checkable", m.Key())
	}
	if !m.Stage.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownStage, m.Stage)
	}
	if m.PromptVersion.Zero() {
		return fmt.Errorf("modelregistry: %s has no prompt version; the same weights with a "+
			"different prompt is a different behaviour", m.Key())
	}
	if m.TrainingCutoff == nil && !m.CutoffUnknown {
		return fmt.Errorf("%w: %s", ErrNoCutoff, m.Key())
	}
	if m.TrainingCutoff != nil && m.CutoffUnknown {
		return fmt.Errorf("modelregistry: %s states a cutoff and marks it unknown", m.Key())
	}
	return nil
}

// KnowledgeCovers reports whether an event falls inside the model's
// training window.
//
// It returns (covered, known). When the cutoff is unknown, known is
// false and covered is meaningless -- which forces a caller to handle
// the case rather than receive a confident false.
func (m Model) KnowledgeCovers(t time.Time) (covered, known bool) {
	if m.CutoffUnknown || m.TrainingCutoff == nil {
		return false, false
	}
	return !t.After(*m.TrainingCutoff), true
}

// Registry holds registered models.
type Registry struct {
	models map[string]Model
	// usage records which artefacts were produced by which model, so
	// a revocation can enumerate what it affects.
	usage map[string][]string
}

func NewRegistry() *Registry {
	return &Registry{models: map[string]Model{}, usage: map[string][]string{}}
}

// Register adds a model at DEVELOPMENT.
//
// It refuses a model registered at any other stage: a model cannot
// arrive already qualified, because qualification is a thing that
// happens in the registry with evidence attached.
func (r *Registry) Register(m Model) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.Stage != Development {
		return fmt.Errorf("modelregistry: %s is registered at %s; a model enters at "+
			"DEVELOPMENT and advances with evidence", m.Key(), m.Stage)
	}
	if _, dup := r.models[m.Key()]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicate, m.Key())
	}
	r.models[m.Key()] = m
	return nil
}

// Get returns a model.
func (r *Registry) Get(id, version string) (Model, error) {
	m, ok := r.models[id+"@"+version]
	if !ok {
		return Model{}, fmt.Errorf("%w: %s@%s", ErrUnknownModel, id, version)
	}
	return m, nil
}

// Advance moves a model one stage forward.
func (r *Registry) Advance(id, version string, to Stage, approver contract.ID,
	at time.Time, reason string, eval *Evaluation) error {

	m, err := r.Get(id, version)
	if err != nil {
		return err
	}
	if m.Stage == Revoked {
		return fmt.Errorf("%w: %s", ErrRevoked, m.Key())
	}
	if !to.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownStage, to)
	}
	if approver == "" {
		return ErrNoApprover
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("modelregistry: a stage change must state why")
	}
	from, next := rank(m.Stage), rank(to)
	if from < 0 || next < 0 {
		return fmt.Errorf("%w: %s -> %s is not a forward transition; use Deprecate or Revoke",
			ErrUnknownStage, m.Stage, to)
	}
	if next != from+1 {
		return fmt.Errorf("%w: %s -> %s", ErrSkippedStage, m.Stage, to)
	}

	// QUALIFIED and above need an evaluation, and it must be over data
	// VERIQO did not create -- which is the same boundary the
	// assurance ladder draws, applied to models.
	if to == Qualified || to == Production {
		if eval == nil {
			return fmt.Errorf("%w: %s -> %s", ErrNoEvaluation, m.Stage, to)
		}
		if err := eval.Validate(); err != nil {
			return err
		}
		if !eval.DatasetExternal {
			return fmt.Errorf("%w: evaluation %s ran on %q, which VERIQO created. "+
				"A model qualified on its author's own data has been shown to work, "+
				"not shown to hold", ErrNoEvaluation, eval.ID, eval.Dataset)
		}
		m.Evaluations = append(m.Evaluations, *eval)
	}

	t := Transition{From: m.Stage, To: to, At: at, ApprovedBy: approver, Reason: reason}
	if eval != nil {
		t.EvaluationID = eval.ID
	}
	m.History = append(m.History, t)
	m.Stage = to
	r.models[m.Key()] = m
	return nil
}

// Deprecate marks a model as no longer for new work. Its output stays
// valid and it remains usable for replay.
func (r *Registry) Deprecate(id, version string, by contract.ID, at time.Time, reason string) error {
	m, err := r.Get(id, version)
	if err != nil {
		return err
	}
	if m.Stage == Revoked {
		return fmt.Errorf("%w: %s", ErrRevoked, m.Key())
	}
	m.History = append(m.History, Transition{From: m.Stage, To: Deprecated,
		At: at, ApprovedBy: by, Reason: reason})
	m.Stage = Deprecated
	r.models[m.Key()] = m
	return nil
}

// Revoke marks a model's output as suspect and returns what it
// touched.
//
// The return value is the point. "We stopped using it" and "we know
// what it touched" are different positions, and only one of them lets
// a customer be told which of their conclusions to re-examine.
func (r *Registry) Revoke(id, version string, by contract.ID, at time.Time, reason string) ([]string, error) {
	m, err := r.Get(id, version)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		return nil, errors.New("modelregistry: a revocation must state why")
	}
	m.History = append(m.History, Transition{From: m.Stage, To: Revoked,
		At: at, ApprovedBy: by, Reason: reason})
	m.Stage = Revoked
	r.models[m.Key()] = m

	affected := append([]string(nil), r.usage[m.Key()]...)
	sort.Strings(affected)
	return affected, nil
}

// RecordUse notes that a model produced an artefact.
func (r *Registry) RecordUse(id, version, artefactID string) error {
	m, err := r.Get(id, version)
	if err != nil {
		return err
	}
	if !m.Stage.UsableOnACase() {
		return fmt.Errorf("%w: %s is at %s", ErrNotUsable, m.Key(), m.Stage)
	}
	key := m.Key()
	for _, a := range r.usage[key] {
		if a == artefactID {
			return nil
		}
	}
	r.usage[key] = append(r.usage[key], artefactID)
	return nil
}

// Affected returns what a model produced.
func (r *Registry) Affected(id, version string) []string {
	out := append([]string(nil), r.usage[id+"@"+version]...)
	sort.Strings(out)
	return out
}

// CheckUsable is the gate a caller invokes before running a model.
func (r *Registry) CheckUsable(id, version string) error {
	m, err := r.Get(id, version)
	if err != nil {
		return err
	}
	if m.Stage == Revoked {
		return fmt.Errorf("%w: %s. Output produced under it is suspect", ErrRevoked, m.Key())
	}
	if !m.Stage.UsableOnACase() {
		return fmt.Errorf("%w: %s is at %s and may not run against case material",
			ErrNotUsable, m.Key(), m.Stage)
	}
	return nil
}

// Models returns every registered model, sorted.
func (r *Registry) Models() []Model {
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Report renders the registry.
func (r *Registry) Report() string {
	var b strings.Builder
	b.WriteString("MODEL REGISTRY\n")
	for _, m := range r.Models() {
		fmt.Fprintf(&b, "  %-28s %-12s %s (temp %.2f, prompt %s)\n",
			m.Key(), m.Stage, m.Provider, m.Temperature, m.PromptVersion)
		if m.CutoffUnknown {
			b.WriteString("    training cutoff: UNKNOWN\n")
		} else {
			fmt.Fprintf(&b, "    training cutoff: %s\n", m.TrainingCutoff.Format("2006-01-02"))
		}
		for _, e := range m.Evaluations {
			ext := "VERIQO-CREATED"
			if e.DatasetExternal {
				ext = "external"
			}
			fmt.Fprintf(&b, "    evaluated on %s (%s): %v\n", e.Dataset, ext, e.Metrics)
			for _, l := range e.Limitations {
				fmt.Fprintf(&b, "      limitation: %s\n", l)
			}
		}
		if n := len(r.Affected(m.ID, m.Version)); n > 0 {
			fmt.Fprintf(&b, "    produced %d artefact(s)\n", n)
		}
	}
	return b.String()
}
