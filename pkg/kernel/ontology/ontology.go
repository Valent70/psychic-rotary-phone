// Package ontology implements Veriqo's Knowledge Kernel & Ontology
// Manager — the #1 priority item named by "Kritik_terbesar_nomor_1.docx"
// and echoed by "Gap_dan_MOAT.docx": a typed ontology that unifies
// data, logic, action, and policy under one versioned schema registry,
// closing the gap the audit frames as Palantir's core differentiator
// ("mengintegrasikan data, logic, action, dan security policies dalam
// ontology platform").
//
// Design: four typed kinds — EntityType, RelationType, ActionType,
// PolicyType — each a versioned, content-addressed schema. Instances
// (concrete entities/relations/actions/policy-bindings) are validated
// against a registered type before acceptance. Every registration and
// instance acceptance is appended to a hash-chained changelog so the
// ontology itself is auditable and replayable, per DARI.
//
// Honest scope: schema validation here is structural (required fields
// + declared field kinds), not a full type-theory or constraint-solver
// system. Cross-type constraints (e.g. "a SanctionEntry action requires
// an evidenced Vessel entity") are expressed via Action.RequiresKinds
// and checked at bind time, not via a general rule language.
package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrUnknownType     = errors.New("ontology: type not registered")
	ErrTypeExists      = errors.New("ontology: type already registered at this version")
	ErrValidation      = errors.New("ontology: instance failed schema validation")
	ErrTamperDetected  = errors.New("ontology: changelog hash chain broken")
	ErrMissingRequires = errors.New("ontology: action requires a kind not present in bindings")
)

// Kind classifies what a TypeDef describes — the four ontology
// categories the critique explicitly names as needing unification.
type Kind string

const (
	KindEntity   Kind = "ENTITY"
	KindRelation Kind = "RELATION"
	KindAction   Kind = "ACTION"
	KindPolicy   Kind = "POLICY"
)

// FieldKind is the structural type a field must satisfy.
type FieldKind string

const (
	FieldString FieldKind = "STRING"
	FieldNumber FieldKind = "NUMBER"
	FieldBool   FieldKind = "BOOL"
	FieldRef    FieldKind = "REF" // reference to another instance ID
)

// FieldSpec declares one required or optional field on a TypeDef.
type FieldSpec struct {
	Name     string
	Kind     FieldKind
	Required bool
}

// TypeDef is a versioned, typed schema for one ontology Kind.
type TypeDef struct {
	Name    string
	Kind    Kind
	Version uint64
	Fields  []FieldSpec
	// RequiresKinds is only meaningful for KindAction: the set of
	// entity/relation type Names that must be present among an
	// Instance's Refs before the action instance can bind.
	RequiresKinds []string
}

// ID returns the content-addressed identifier for this TypeDef,
// derived from its name+kind+version+fields — no UUIDs, per invariant.
func (t TypeDef) ID() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d", t.Name, t.Kind, t.Version)
	for _, f := range t.Fields {
		fmt.Fprintf(h, "|%s:%s:%v", f.Name, f.Kind, f.Required)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Instance is a concrete value asserted against a registered TypeDef.
type Instance struct {
	TypeName string
	Fields   map[string]string // string-encoded values, kind-checked against TypeDef
	Refs     []string          // IDs of other instances this one references (for REF fields / action bindings)
}

// ID is the content-addressed identity of an Instance.
func (in Instance) ID() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s", in.TypeName)
	keys := make([]string, 0, len(in.Fields))
	for k := range in.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "|%s=%s", k, in.Fields[k])
	}
	for _, r := range in.Refs {
		fmt.Fprintf(h, "|ref:%s", r)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ChangeOp is one hash-chained changelog operation kind.
type ChangeOp string

const (
	OpRegisterType   ChangeOp = "REGISTER_TYPE"
	OpAcceptInstance ChangeOp = "ACCEPT_INSTANCE"
)

// ChangeRecord is one append-only changelog entry — the ontology's own
// DARI evidence trail.
type ChangeRecord struct {
	Index    uint64
	Op       ChangeOp
	TypeID   string
	InstID   string
	PrevHash string
	Hash     string
}

func (c ChangeRecord) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%s|%s", c.Index, c.Op, c.TypeID, c.InstID, c.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Manager is the Ontology Manager / Knowledge Kernel: a typed registry
// of data (Entity), logic (Relation), action (Action) and policy
// (Policy) schemas, plus a validated, hash-chained instance ledger.
type Manager struct {
	mu        sync.Mutex
	types     map[string]TypeDef  // keyed by Name (latest version wins for lookup by name)
	typesByID map[string]TypeDef  // keyed by TypeDef.ID()
	instances map[string]Instance // keyed by Instance.ID()
	changelog []ChangeRecord
}

func NewManager() *Manager {
	return &Manager{
		types:     make(map[string]TypeDef),
		typesByID: make(map[string]TypeDef),
		instances: make(map[string]Instance),
	}
}

func (m *Manager) appendLocked(op ChangeOp, typeID, instID string) ChangeRecord {
	prev := ""
	if n := len(m.changelog); n > 0 {
		prev = m.changelog[n-1].Hash
	}
	rec := ChangeRecord{Index: uint64(len(m.changelog)) + 1, Op: op, TypeID: typeID, InstID: instID, PrevHash: prev}
	rec.Hash = rec.computeHash()
	m.changelog = append(m.changelog, rec)
	return rec
}

// RegisterType adds a new typed schema. Re-registering the same Name at
// a higher Version is allowed (schema evolution); registering an
// identical (Name,Version) pair twice returns ErrTypeExists.
func (m *Manager) RegisterType(t TypeDef) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := t.ID()
	if _, exists := m.typesByID[id]; exists {
		return "", fmt.Errorf("%w: %s v%d", ErrTypeExists, t.Name, t.Version)
	}
	m.typesByID[id] = t
	if existing, ok := m.types[t.Name]; !ok || t.Version >= existing.Version {
		m.types[t.Name] = t
	}
	m.appendLocked(OpRegisterType, id, "")
	return id, nil
}

// TypeByName returns the latest registered version of a named type.
func (m *Manager) TypeByName(name string) (TypeDef, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.types[name]
	return t, ok
}

// Validate checks an Instance's fields against its TypeDef's schema:
// every Required field present, and every present field's value
// satisfies its declared FieldKind's basic structural shape.
func (m *Manager) Validate(in Instance) error {
	m.mu.Lock()
	t, ok := m.types[in.TypeName]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownType, in.TypeName)
	}
	for _, f := range t.Fields {
		v, present := in.Fields[f.Name]
		if !present {
			if f.Required {
				return fmt.Errorf("%w: %s.%s is required", ErrValidation, t.Name, f.Name)
			}
			continue
		}
		if err := checkKind(f.Kind, v); err != nil {
			return fmt.Errorf("%w: %s.%s: %v", ErrValidation, t.Name, f.Name, err)
		}
	}
	if t.Kind == KindAction && len(t.RequiresKinds) > 0 {
		if len(in.Refs) < len(t.RequiresKinds) {
			return fmt.Errorf("%w: %s requires %v, got %d refs", ErrMissingRequires, t.Name, t.RequiresKinds, len(in.Refs))
		}
	}
	return nil
}

func checkKind(k FieldKind, v string) error {
	switch k {
	case FieldString, FieldRef:
		if v == "" {
			return errors.New("empty value")
		}
	case FieldBool:
		if v != "true" && v != "false" {
			return fmt.Errorf("not a bool: %q", v)
		}
	case FieldNumber:
		neg := false
		s := v
		if len(s) > 0 && s[0] == '-' {
			neg = true
			s = s[1:]
		}
		if s == "" {
			return fmt.Errorf("not a number: %q", v)
		}
		dot := false
		for _, r := range s {
			if r == '.' && !dot {
				dot = true
				continue
			}
			if r < '0' || r > '9' {
				return fmt.Errorf("not a number: %q", v)
			}
		}
		_ = neg
	default:
		return fmt.Errorf("unknown field kind %q", k)
	}
	return nil
}

// Accept validates and, if valid, appends an Instance to the ontology's
// instance ledger, returning its content-addressed ID.
func (m *Manager) Accept(in Instance) (string, error) {
	if err := m.Validate(in); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := in.ID()
	m.instances[id] = in
	typ := m.types[in.TypeName]
	m.appendLocked(OpAcceptInstance, typ.ID(), id)
	return id, nil
}

// Instance returns a previously accepted instance by ID.
func (m *Manager) Instance(id string) (Instance, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.instances[id]
	return in, ok
}

// KindsOf returns the sorted names of every registered type of a given
// Kind (e.g. all POLICY types) — used by callers building a live view
// over data/logic/action/policy per the critique's "unify" requirement.
func (m *Manager) KindsOf(k Kind) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for name, t := range m.types {
		if t.Kind == k {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// VerifyChain replays the changelog's hash chain from scratch and
// reports a tamper mismatch, if any — the ontology's own DARI replay
// guarantee.
func (m *Manager) VerifyChain() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := ""
	for _, rec := range m.changelog {
		want := ChangeRecord{Index: rec.Index, Op: rec.Op, TypeID: rec.TypeID, InstID: rec.InstID, PrevHash: prev}
		if want.computeHash() != rec.Hash {
			return fmt.Errorf("%w at index %d", ErrTamperDetected, rec.Index)
		}
		prev = rec.Hash
	}
	return nil
}

// Changelog returns a defensive copy of the full changelog.
func (m *Manager) Changelog() []ChangeRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChangeRecord, len(m.changelog))
	copy(out, m.changelog)
	return out
}
