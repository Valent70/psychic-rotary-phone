// Package kms is the VERIQO key hierarchy.
//
// # The hierarchy the specification requires
//
//	HSM/KMS -> tenant key -> case key -> evidence encryption
//	                      -> ledger signing
//
// Each level is derived, not stored, so compromising a case key
// yields that case and nothing above or beside it. The root never
// exists in VERIQO's memory at all: it lives in an HSM or a managed
// KMS, and this package holds a HANDLE to it.
//
// # Why the root is an interface VERIQO does not implement
//
// The same reason the ledger's external anchor is. A root key VERIQO
// generates and holds is a root key VERIQO can lose, exfiltrate or
// silently rotate. Gate G1 is about wiring this to a real HSM, and a
// software implementation in this repository would be
// indistinguishable from a real one at every call site -- so the only
// implementation here is explicitly named a test double and refuses to
// be used in production mode.
//
// # Rotation has four kinds and they are not interchangeable
//
//	SCHEDULED   routine, planned, no urgency
//	EVENT       triggered by a policy event (a leaver, a re-tender)
//	EMERGENCY   a suspected compromise; the old key is quarantined
//	REVOCATION  a confirmed compromise; the old key is destroyed and
//	            everything under it is suspect
//
// The difference that matters is what happens to material encrypted
// under the old key. A scheduled rotation re-wraps it. A revocation
// marks it, and the register can enumerate what is affected --
// which is the same discipline as a revoked model.
package kms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoRoot                 = errors.New("kms: no root key handle")
	ErrTestDoubleInProduction = errors.New("kms: the software root is a TEST DOUBLE and may " +
		"not be used in production; gate G1 is wiring an HSM or managed KMS")
	ErrUnknownKey  = errors.New("kms: unknown key")
	ErrRevoked     = errors.New("kms: the key is revoked")
	ErrQuarantined = errors.New("kms: the key is quarantined pending investigation")
	ErrBadPurpose  = errors.New("kms: unknown key purpose")
	ErrNoReason    = errors.New("kms: a rotation must state why")
	ErrWrongTenant = errors.New("kms: the key belongs to another tenant")
)

// Purpose separates keys by what they protect. A key derived for
// evidence encryption cannot sign a ledger checkpoint, because the two
// have different compromise consequences and different rotation
// cadences.
type Purpose string

const (
	EvidenceEncryption Purpose = "EVIDENCE_ENCRYPTION"
	LedgerSigning      Purpose = "LEDGER_SIGNING"
	PassportSigning    Purpose = "PASSPORT_SIGNING"
	ExportSigning      Purpose = "EXPORT_SIGNING"
	FieldEncryption    Purpose = "FIELD_ENCRYPTION"
)

func Purposes() []Purpose {
	return []Purpose{EvidenceEncryption, LedgerSigning, PassportSigning,
		ExportSigning, FieldEncryption}
}

func (p Purpose) Valid() bool {
	for _, x := range Purposes() {
		if x == p {
			return true
		}
	}
	return false
}

// Root is the handle to the HSM or managed KMS.
//
// VERIQO does not implement this in production. The interface takes a
// LABEL and returns derived material: the root key itself never
// crosses the boundary.
type Root interface {
	// Derive returns key material for a label. The root never leaves
	// the HSM; only derived material does.
	Derive(label string, length int) ([]byte, error)
	// Attested reports whether this root is a real hardware or managed
	// root. A test double answers false, and production refuses it.
	Attested() bool
	// Identifier names the root for the audit record.
	Identifier() string
}

// SoftwareRoot is a TEST DOUBLE. It is deliberately named so, it
// reports Attested() false, and NewHierarchy refuses it in production
// mode.
//
// It exists because tests need a root, and because the alternative --
// a plausible-looking software KMS -- would be used in production by
// somebody who did not read the comment.
type SoftwareRoot struct {
	seed []byte
	id   string
}

// NewSoftwareRoot builds the test double.
func NewSoftwareRoot(id string, seed []byte) (*SoftwareRoot, error) {
	if len(seed) < 32 {
		return nil, errors.New("kms: the test root needs at least 32 bytes of seed")
	}
	cp := make([]byte, len(seed))
	copy(cp, seed)
	return &SoftwareRoot{seed: cp, id: id}, nil
}

func (r *SoftwareRoot) Derive(label string, length int) ([]byte, error) {
	if r == nil || len(r.seed) == 0 {
		return nil, ErrNoRoot
	}
	if length <= 0 || length > 64 {
		return nil, fmt.Errorf("kms: derived length %d out of range", length)
	}
	mac := hmac.New(sha256.New, r.seed)
	writeField(mac, "veriqo/kms/v1")
	writeField(mac, label)
	sum := mac.Sum(nil)
	if length <= len(sum) {
		return sum[:length], nil
	}
	return sum, nil
}

// Attested reports false. Always.
func (r *SoftwareRoot) Attested() bool { return false }

func (r *SoftwareRoot) Identifier() string { return "SOFTWARE-TEST-DOUBLE:" + r.id }

func writeField(h interface{ Write([]byte) (int, error) }, s string) {
	var n [4]byte
	l := len(s)
	n[0], n[1], n[2], n[3] = byte(l>>24), byte(l>>16), byte(l>>8), byte(l)
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(s))
}

// State is a key's standing.
type State string

const (
	Active      State = "ACTIVE"
	Superseded  State = "SUPERSEDED"  // rotated out; still able to decrypt history
	Quarantined State = "QUARANTINED" // suspected compromise; no new use
	Destroyed   State = "DESTROYED"   // confirmed compromise; material is suspect
)

// RotationKind is why a key was rotated.
type RotationKind string

const (
	Scheduled  RotationKind = "SCHEDULED"
	Event      RotationKind = "EVENT"
	Emergency  RotationKind = "EMERGENCY"
	Revocation RotationKind = "REVOCATION"
)

func (k RotationKind) Valid() bool {
	switch k {
	case Scheduled, Event, Emergency, Revocation:
		return true
	}
	return false
}

// OldKeyState is what happens to the superseded key.
func (k RotationKind) OldKeyState() State {
	switch k {
	case Emergency:
		return Quarantined
	case Revocation:
		return Destroyed
	}
	return Superseded
}

// Key is a derived key's metadata. The MATERIAL is not stored here:
// it is derived on demand from the root, so a dump of this structure
// yields no keys.
type Key struct {
	ID       string  `json:"id"`
	TenantID string  `json:"tenant_id"`
	CaseID   string  `json:"case_id,omitempty"`
	Purpose  Purpose `json:"purpose"`

	Generation int   `json:"generation"`
	State      State `json:"state"`

	CreatedAt    time.Time  `json:"created_at"`
	SupersededAt *time.Time `json:"superseded_at,omitempty"`

	// RotatedBecause records why, for the audit trail.
	RotatedBecause RotationKind `json:"rotated_because,omitempty"`
	RotationReason string       `json:"rotation_reason,omitempty"`
}

// label is the derivation path. It is length-prefixed through the
// root's Derive, and it encodes the whole hierarchy so a case key
// cannot collide with a tenant key.
func (k Key) label() string {
	parts := []string{"tenant", k.TenantID, string(k.Purpose)}
	if k.CaseID != "" {
		parts = append(parts, "case", k.CaseID)
	}
	parts = append(parts, "gen", fmt.Sprint(k.Generation))
	return strings.Join(parts, "/")
}

// Usable reports whether a key may be used for NEW work.
func (k Key) Usable() bool { return k.State == Active }

// UsableForDecryption reports whether it may still read history. A
// superseded key can; a destroyed one cannot.
func (k Key) UsableForDecryption() bool {
	return k.State == Active || k.State == Superseded
}

// Hierarchy is the key register.
type Hierarchy struct {
	mu   sync.Mutex
	root Root
	// production refuses an unattested root.
	production bool
	keys       map[string]Key
	// usage records what each key protected, so a revocation can
	// enumerate the affected material.
	usage map[string][]string
}

// NewHierarchy binds a root.
//
// In production mode an unattested root is refused at construction --
// which is the difference between a gate that is checked and a gate
// that is documented.
func NewHierarchy(root Root, production bool) (*Hierarchy, error) {
	if root == nil {
		return nil, ErrNoRoot
	}
	if production && !root.Attested() {
		return nil, fmt.Errorf("%w: root is %s", ErrTestDoubleInProduction, root.Identifier())
	}
	return &Hierarchy{root: root, production: production,
		keys: map[string]Key{}, usage: map[string][]string{}}, nil
}

// RootIdentifier names the root for an audit record.
func (h *Hierarchy) RootIdentifier() string { return h.root.Identifier() }

// Attested reports whether the backing root is real.
func (h *Hierarchy) Attested() bool { return h.root.Attested() }

func keyID(tenantID, caseID string, p Purpose) string {
	if caseID == "" {
		return tenantID + "/" + string(p)
	}
	return tenantID + "/" + caseID + "/" + string(p)
}

// Create derives a first-generation key.
func (h *Hierarchy) Create(tenantID, caseID string, p Purpose, at time.Time) (Key, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if strings.TrimSpace(tenantID) == "" {
		return Key{}, errors.New("kms: no tenant")
	}
	if !p.Valid() {
		return Key{}, fmt.Errorf("%w: %q", ErrBadPurpose, p)
	}
	id := keyID(tenantID, caseID, p)
	if existing, ok := h.keys[id]; ok {
		return existing, nil
	}
	k := Key{ID: id, TenantID: tenantID, CaseID: caseID, Purpose: p,
		Generation: 1, State: Active, CreatedAt: at}
	h.keys[id] = k
	return k, nil
}

// Material derives the key bytes for a key.
//
// It refuses a quarantined or destroyed key, and it refuses to derive
// material for NEW use from a superseded one -- the caller must ask
// for decryption explicitly, which makes reading history a visible
// choice rather than a default.
func (h *Hierarchy) Material(id string, forDecryption bool) ([]byte, error) {
	h.mu.Lock()
	k, ok := h.keys[id]
	h.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, id)
	}
	switch k.State {
	case Destroyed:
		return nil, fmt.Errorf("%w: %s was destroyed after %s", ErrRevoked, id, k.RotatedBecause)
	case Quarantined:
		return nil, fmt.Errorf("%w: %s", ErrQuarantined, id)
	case Superseded:
		if !forDecryption {
			return nil, fmt.Errorf("kms: %s is superseded; it may decrypt history and may "+
				"not encrypt new material", id)
		}
	}
	return h.root.Derive(k.label(), 32)
}

// Rotate advances a key's generation.
func (h *Hierarchy) Rotate(id string, kind RotationKind, reason string, at time.Time) (Key, []string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	k, ok := h.keys[id]
	if !ok {
		return Key{}, nil, fmt.Errorf("%w: %s", ErrUnknownKey, id)
	}
	if !kind.Valid() {
		return Key{}, nil, fmt.Errorf("kms: unknown rotation kind %q", kind)
	}
	if strings.TrimSpace(reason) == "" {
		return Key{}, nil, ErrNoReason
	}
	if k.State == Destroyed {
		return Key{}, nil, fmt.Errorf("%w: %s", ErrRevoked, id)
	}

	old := k
	old.State = kind.OldKeyState()
	old.SupersededAt = &at
	old.RotatedBecause = kind
	old.RotationReason = reason
	// The old generation is kept under a generation-qualified id so
	// history remains decryptable where the kind permits it.
	h.keys[fmt.Sprintf("%s#gen%d", id, old.Generation)] = old

	next := k
	next.Generation = k.Generation + 1
	next.State = Active
	next.CreatedAt = at
	next.SupersededAt = nil
	next.RotatedBecause = ""
	next.RotationReason = ""
	h.keys[id] = next

	// A revocation enumerates what the old key protected.
	var affected []string
	if kind == Revocation || kind == Emergency {
		affected = append([]string(nil), h.usage[id]...)
		sort.Strings(affected)
	}
	return next, affected, nil
}

// RecordUse notes that a key protected an artefact.
func (h *Hierarchy) RecordUse(id, artefactID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	k, ok := h.keys[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, id)
	}
	if !k.Usable() {
		return fmt.Errorf("kms: %s is %s and may not protect new material", id, k.State)
	}
	for _, a := range h.usage[id] {
		if a == artefactID {
			return nil
		}
	}
	h.usage[id] = append(h.usage[id], artefactID)
	return nil
}

// Get returns a key's metadata.
func (h *Hierarchy) Get(id string) (Key, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k, ok := h.keys[id]
	if !ok {
		return Key{}, fmt.Errorf("%w: %s", ErrUnknownKey, id)
	}
	return k, nil
}

// GuardTenant refuses a key from another tenant.
func (h *Hierarchy) GuardTenant(id, tenantID string) error {
	k, err := h.Get(id)
	if err != nil {
		return err
	}
	if k.TenantID != tenantID {
		return fmt.Errorf("%w: %s belongs to %s", ErrWrongTenant, id, k.TenantID)
	}
	return nil
}

// Keys returns the register, sorted.
func (h *Hierarchy) Keys() []Key {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Key, 0, len(h.keys))
	for _, k := range h.keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Generation < out[j].Generation
	})
	return out
}

// Fingerprint returns a non-secret identifier for a key's material,
// so two parties can confirm they hold the same key without either
// disclosing it.
func (h *Hierarchy) Fingerprint(id string) (string, error) {
	m, err := h.Material(id, true)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("veriqo/kms/fingerprint/v1"), m...))
	return hex.EncodeToString(sum[:8]), nil
}

// Report renders the hierarchy without any key material.
func (h *Hierarchy) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "KEY HIERARCHY (root %s, attested=%v)\n",
		h.RootIdentifier(), h.Attested())
	if !h.Attested() {
		b.WriteString("  WARNING: the backing root is not an attested HSM or managed KMS. " +
			"Gate G1 is not satisfied and no production claim rests on these keys.\n")
	}
	for _, k := range h.Keys() {
		fmt.Fprintf(&b, "  %-48s gen %d  %-12s %s\n", k.ID, k.Generation, k.State, k.Purpose)
		if k.RotationReason != "" {
			fmt.Fprintf(&b, "      rotated (%s): %s\n", k.RotatedBecause, k.RotationReason)
		}
	}
	return b.String()
}
