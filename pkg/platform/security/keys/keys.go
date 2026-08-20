// Package keys closes PHASE 11 and PHASE 28 of the v7.10.3
// production-readiness audit: a cryptographic trust chain with a real
// key lifecycle, and envelope encryption at rest — without inventing
// any cryptography and without hardcoding an HSM.
//
// The audit's instruction was explicit: "Jangan hardcode HSM. Buat
// interface." KeyProvider is that interface. Three implementations
// ship here — MemoryKeyProvider (tests), FileKeyProvider (single-node
// deployments, keys at rest under an operator-controlled directory),
// and a KMS/HSM adapter shape that a deployment fills in with its
// vendor SDK. The kernel never sees a private key: it sees Sign() and
// PublicKey().
//
// Key LIFECYCLE is the part most systems skip and the part that
// actually matters:
//
//	Root of Trust -> Issuer -> Signing Key -> Certificate -> Evidence
//
//	create -> activate -> rotate -> retire -> revoke
//
// with versions, expiry, and a revocation list. A signature made by a
// key that was valid AT SIGNING TIME stays valid after rotation
// (otherwise every rotation would invalidate history); a signature
// made by a REVOKED key is invalid retroactively (that is what
// revocation means, and it is why revocation and rotation must be
// different operations rather than the same one under two names).
package keys

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Errors.
var (
	ErrUnknownKey    = errors.New("keys: unknown key ID")
	ErrKeyRevoked    = errors.New("keys: key is revoked")
	ErrKeyExpired    = errors.New("keys: key is past its expiry tick")
	ErrKeyNotActive  = errors.New("keys: key is not in ACTIVE state")
	ErrBadSignature  = errors.New("keys: signature does not verify")
	ErrNoActiveKey   = errors.New("keys: no active key for this purpose")
	ErrCiphertextBad = errors.New("keys: ciphertext failed authentication")
	ErrKeyExists     = errors.New("keys: key ID already exists")

	// ErrSoftwareProviderInProduction is returned by RequireProductionSafe
	// when a software-backed KeyProvider (private key material in
	// process memory or on local disk) is wired in under a production
	// environment. hsm_kms's qualification requires this to be checked
	// once at startup, before any key material is ever touched -- not
	// discovered later from a signature that turns out to be backed by
	// the wrong kind of key custody.
	ErrSoftwareProviderInProduction = errors.New("keys: a software-backed key provider cannot be used when the environment is production")
)

// SoftwareBacked is implemented by any KeyProvider whose private key
// material lives outside a real HSM/KMS -- in process memory or on the
// local filesystem. RequireProductionSafe uses it to fail closed rather
// than let a software provider silently stand in for hardware custody
// in a production deployment.
type SoftwareBacked interface {
	SoftwareBacked() bool
}

// RequireProductionSafe refuses to return nil if provider is
// software-backed and env is "production". Call it once at process
// startup: the point is to refuse to start at all, not to catch a
// misconfiguration after keys have already been signed with it.
func RequireProductionSafe(provider KeyProvider, env string) error {
	if env != "production" {
		return nil
	}
	if sb, ok := provider.(SoftwareBacked); ok && sb.SoftwareBacked() {
		return ErrSoftwareProviderInProduction
	}
	return nil
}

// State is a key's lifecycle state.
type State string

const (
	StatePending State = "PENDING"
	StateActive  State = "ACTIVE"
	StateRetired State = "RETIRED" // no new signatures; old ones still verify
	StateRevoked State = "REVOKED" // all signatures invalid, retroactively
)

// Purpose separates signing domains so one compromised key does not
// cross-contaminate: a release-signing key must not be usable to sign
// evidence.
type Purpose string

const (
	PurposeEvidence Purpose = "EVIDENCE"
	PurposeRelease  Purpose = "RELEASE"
	PurposePolicy   Purpose = "POLICY"
	PurposeDataKey  Purpose = "DATA_KEY"
)

// KeyMetadata is everything about a key except its private material.
type KeyMetadata struct {
	KeyID     string  `json:"key_id"`
	Version   int     `json:"version"`
	Purpose   Purpose `json:"purpose"`
	State     State   `json:"state"`
	PublicKey string  `json:"public_key"`
	CreatedAt uint64  `json:"created_at"`
	ExpiresAt uint64  `json:"expires_at"` // 0 = no expiry
	// Predecessor links a rotated key to the one it replaced, forming
	// the auditable key chain.
	Predecessor  string `json:"predecessor,omitempty"`
	RevokedAt    uint64 `json:"revoked_at,omitempty"`
	RevokeReason string `json:"revoke_reason,omitempty"`
}

// KeyProvider is the audit's required abstraction. Deployments swap
// implementations without the kernel changing.
type KeyProvider interface {
	Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error)
	PublicKey(ctx context.Context, keyID string) ([]byte, error)
}

// Manager owns key lifecycle on top of any KeyProvider.
type Manager struct {
	mu       sync.RWMutex
	provider KeyProvider
	meta     map[string]*KeyMetadata
	order    []string
}

// NewManager wraps a provider with lifecycle management.
func NewManager(p KeyProvider) *Manager {
	return &Manager{provider: p, meta: map[string]*KeyMetadata{}}
}

// Register records a key's metadata under management.
func (m *Manager) Register(md KeyMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.meta[md.KeyID]; dup {
		return fmt.Errorf("%w: %s", ErrKeyExists, md.KeyID)
	}
	if md.State == "" {
		md.State = StatePending
	}
	if md.Version == 0 {
		md.Version = 1
	}
	cp := md
	m.meta[md.KeyID] = &cp
	m.order = append(m.order, md.KeyID)
	return nil
}

// Activate moves a PENDING key to ACTIVE.
func (m *Manager) Activate(keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	md, ok := m.meta[keyID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	if md.State == StateRevoked {
		return ErrKeyRevoked
	}
	md.State = StateActive
	return nil
}

// Rotate retires the current key and registers its successor, linking
// them. Signatures made by the retired key REMAIN valid.
func (m *Manager) Rotate(oldKeyID string, successor KeyMetadata) error {
	m.mu.Lock()
	old, ok := m.meta[oldKeyID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrUnknownKey, oldKeyID)
	}
	if old.State == StateRevoked {
		m.mu.Unlock()
		return ErrKeyRevoked
	}
	old.State = StateRetired
	successor.Predecessor = oldKeyID
	successor.Version = old.Version + 1
	successor.Purpose = old.Purpose
	successor.State = StateActive
	m.mu.Unlock()
	return m.Register(successor)
}

// Revoke invalidates a key and everything it signed, retroactively.
func (m *Manager) Revoke(keyID, reason string, tick uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	md, ok := m.meta[keyID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	md.State, md.RevokedAt, md.RevokeReason = StateRevoked, tick, reason
	return nil
}

// Metadata returns a key's metadata.
func (m *Manager) Metadata(keyID string) (KeyMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	md, ok := m.meta[keyID]
	if !ok {
		return KeyMetadata{}, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	return *md, nil
}

// ActiveKeyFor returns the current active key for a purpose.
func (m *Manager) ActiveKeyFor(p Purpose) (KeyMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := append([]string(nil), m.order...)
	sort.Strings(ids)
	best := KeyMetadata{}
	found := false
	for _, id := range ids {
		md := m.meta[id]
		if md.Purpose == p && md.State == StateActive && (!found || md.Version > best.Version) {
			best, found = *md, true
		}
	}
	if !found {
		return KeyMetadata{}, fmt.Errorf("%w: %s", ErrNoActiveKey, p)
	}
	return best, nil
}

// Sign signs a digest with a key, enforcing lifecycle state and expiry.
func (m *Manager) Sign(ctx context.Context, keyID string, digest []byte, tick uint64) ([]byte, error) {
	md, err := m.Metadata(keyID)
	if err != nil {
		return nil, err
	}
	switch {
	case md.State == StateRevoked:
		return nil, ErrKeyRevoked
	case md.State != StateActive:
		return nil, fmt.Errorf("%w: %s is %s", ErrKeyNotActive, keyID, md.State)
	case md.ExpiresAt != 0 && tick > md.ExpiresAt:
		return nil, ErrKeyExpired
	}
	return m.provider.Sign(ctx, keyID, digest)
}

// Verify checks a signature against lifecycle rules AT VERIFICATION
// TIME. A retired key still verifies (history stays valid); a revoked
// key never does.
func (m *Manager) Verify(ctx context.Context, keyID string, digest, sig []byte) error {
	md, err := m.Metadata(keyID)
	if err != nil {
		return err
	}
	if md.State == StateRevoked {
		return fmt.Errorf("%w: %s (%s)", ErrKeyRevoked, keyID, md.RevokeReason)
	}
	pub, err := m.provider.PublicKey(ctx, keyID)
	if err != nil {
		return err
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), digest, sig) {
		return ErrBadSignature
	}
	return nil
}

// --- MemoryKeyProvider ------------------------------------------------

// MemoryKeyProvider holds keys in process memory. Test and development
// use only; it is deliberately NOT the default anywhere.
type MemoryKeyProvider struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PrivateKey
}

// NewMemoryKeyProvider constructs an empty in-memory provider.
func NewMemoryKeyProvider() *MemoryKeyProvider {
	return &MemoryKeyProvider{keys: map[string]ed25519.PrivateKey{}}
}

// SoftwareBacked implements SoftwareBacked: private key material never
// leaves this process's memory.
func (p *MemoryKeyProvider) SoftwareBacked() bool { return true }

// Generate creates and stores a new keypair, returning its metadata.
func (p *MemoryKeyProvider) Generate(keyID string, purpose Purpose, createdAt, expiresAt uint64) (KeyMetadata, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyMetadata{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.keys[keyID]; dup {
		return KeyMetadata{}, fmt.Errorf("%w: %s", ErrKeyExists, keyID)
	}
	p.keys[keyID] = priv
	return KeyMetadata{KeyID: keyID, Version: 1, Purpose: purpose, State: StatePending,
		PublicKey: hex.EncodeToString(pub), CreatedAt: createdAt, ExpiresAt: expiresAt}, nil
}

// Sign implements KeyProvider.
func (p *MemoryKeyProvider) Sign(_ context.Context, keyID string, digest []byte) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	priv, ok := p.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	return ed25519.Sign(priv, digest), nil
}

// PublicKey implements KeyProvider.
func (p *MemoryKeyProvider) PublicKey(_ context.Context, keyID string) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	priv, ok := p.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	return priv.Public().(ed25519.PublicKey), nil
}

// --- FileKeyProvider --------------------------------------------------

// FileKeyProvider stores keys as 0600 files under a directory the
// operator controls. It is the honest single-node answer: no HSM
// claimed, no secret in the binary, and the blast radius is exactly
// the filesystem permissions.
type FileKeyProvider struct {
	dir string
	mu  sync.RWMutex
}

// NewFileKeyProvider creates the directory if needed.
func NewFileKeyProvider(dir string) (*FileKeyProvider, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &FileKeyProvider{dir: dir}, nil
}

// SoftwareBacked implements SoftwareBacked: private key material sits on
// the local filesystem, not in a real HSM/KMS.
func (p *FileKeyProvider) SoftwareBacked() bool { return true }

func (p *FileKeyProvider) path(keyID string) string {
	sum := sha256.Sum256([]byte(keyID))
	return filepath.Join(p.dir, hex.EncodeToString(sum[:])+".key")
}

// Generate writes a new keypair to disk atomically.
func (p *FileKeyProvider) Generate(keyID string, purpose Purpose, createdAt, expiresAt uint64) (KeyMetadata, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyMetadata{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	path := p.path(keyID)
	if _, err := os.Stat(path); err == nil {
		return KeyMetadata{}, fmt.Errorf("%w: %s", ErrKeyExists, keyID)
	}
	blob, err := json.Marshal(map[string]string{"key_id": keyID, "private": hex.EncodeToString(priv)})
	if err != nil {
		return KeyMetadata{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return KeyMetadata{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return KeyMetadata{}, err
	}
	return KeyMetadata{KeyID: keyID, Version: 1, Purpose: purpose, State: StatePending,
		PublicKey: hex.EncodeToString(pub), CreatedAt: createdAt, ExpiresAt: expiresAt}, nil
}

func (p *FileKeyProvider) load(keyID string) (ed25519.PrivateKey, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	blob, err := os.ReadFile(p.path(keyID))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	var m map[string]string
	if err := json.Unmarshal(blob, &m); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(m["private"])
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
	}
	return ed25519.PrivateKey(raw), nil
}

// Sign implements KeyProvider.
func (p *FileKeyProvider) Sign(_ context.Context, keyID string, digest []byte) ([]byte, error) {
	priv, err := p.load(keyID)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(priv, digest), nil
}

// PublicKey implements KeyProvider.
func (p *FileKeyProvider) PublicKey(_ context.Context, keyID string) ([]byte, error) {
	priv, err := p.load(keyID)
	if err != nil {
		return nil, err
	}
	return priv.Public().(ed25519.PublicKey), nil
}

// --- PHASE 28: envelope encryption -----------------------------------

// EnvelopeCiphertext is a data-key-encrypted payload plus the wrapped
// data key. Rotating the KEK re-wraps data keys without touching (or
// re-encrypting) the data itself.
type EnvelopeCiphertext struct {
	KEKKeyID       string `json:"kek_key_id"`
	KEKVersion     int    `json:"kek_version"`
	WrappedDataKey string `json:"wrapped_data_key"`
	Nonce          string `json:"nonce"`
	Ciphertext     string `json:"ciphertext"`
	// AAD binds the ciphertext to its context (tenant, classification),
	// so a blob cannot be replayed into a different context.
	AAD string `json:"aad"`
}

// Envelope performs AES-256-GCM envelope encryption. Standard library
// only — the audit's "jangan membuat crypto sendiri" applied literally.
type Envelope struct {
	mu   sync.RWMutex
	keks map[string][]byte // keyID -> 32-byte KEK
	ver  map[string]int
}

// NewEnvelope constructs an envelope encryptor.
func NewEnvelope() *Envelope {
	return &Envelope{keks: map[string][]byte{}, ver: map[string]int{}}
}

// AddKEK registers a key-encryption key.
func (e *Envelope) AddKEK(keyID string, kek []byte) error {
	if len(kek) != 32 {
		return errors.New("keys: KEK must be 32 bytes (AES-256)")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.keks[keyID] = append([]byte(nil), kek...)
	e.ver[keyID]++
	return nil
}

// GenerateKEK creates a random KEK and registers it.
func (e *Envelope) GenerateKEK(keyID string) error {
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		return err
	}
	return e.AddKEK(keyID, kek)
}

// Encrypt generates a fresh data key per payload, encrypts the payload
// with it, and wraps the data key under the KEK.
func (e *Envelope) Encrypt(kekID string, plaintext, aad []byte) (EnvelopeCiphertext, error) {
	e.mu.RLock()
	kek, ok := e.keks[kekID]
	version := e.ver[kekID]
	e.mu.RUnlock()
	if !ok {
		return EnvelopeCiphertext{}, fmt.Errorf("%w: %s", ErrUnknownKey, kekID)
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return EnvelopeCiphertext{}, err
	}
	ct, nonce, err := gcmSeal(dataKey, plaintext, aad)
	if err != nil {
		return EnvelopeCiphertext{}, err
	}
	wrapped, wnonce, err := gcmSeal(kek, dataKey, []byte(kekID))
	if err != nil {
		return EnvelopeCiphertext{}, err
	}
	return EnvelopeCiphertext{
		KEKKeyID: kekID, KEKVersion: version,
		WrappedDataKey: hex.EncodeToString(append(wnonce, wrapped...)),
		Nonce:          hex.EncodeToString(nonce),
		Ciphertext:     hex.EncodeToString(ct),
		AAD:            hex.EncodeToString(aad),
	}, nil
}

// Decrypt unwraps the data key and decrypts the payload.
func (e *Envelope) Decrypt(ec EnvelopeCiphertext) ([]byte, error) {
	e.mu.RLock()
	kek, ok := e.keks[ec.KEKKeyID]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKey, ec.KEKKeyID)
	}
	wrapped, err := hex.DecodeString(ec.WrappedDataKey)
	if err != nil || len(wrapped) < 12 {
		return nil, ErrCiphertextBad
	}
	dataKey, err := gcmOpen(kek, wrapped[12:], wrapped[:12], []byte(ec.KEKKeyID))
	if err != nil {
		return nil, ErrCiphertextBad
	}
	nonce, err := hex.DecodeString(ec.Nonce)
	if err != nil {
		return nil, ErrCiphertextBad
	}
	ct, err := hex.DecodeString(ec.Ciphertext)
	if err != nil {
		return nil, ErrCiphertextBad
	}
	aad, err := hex.DecodeString(ec.AAD)
	if err != nil {
		return nil, ErrCiphertextBad
	}
	pt, err := gcmOpen(dataKey, ct, nonce, aad)
	if err != nil {
		return nil, ErrCiphertextBad
	}
	return pt, nil
}

// RotateKEK re-wraps an existing ciphertext's data key under a new KEK
// WITHOUT decrypting and re-encrypting the payload — the reason
// envelope encryption exists.
func (e *Envelope) RotateKEK(ec EnvelopeCiphertext, newKEKID string) (EnvelopeCiphertext, error) {
	e.mu.RLock()
	oldKEK, ok1 := e.keks[ec.KEKKeyID]
	newKEK, ok2 := e.keks[newKEKID]
	newVer := e.ver[newKEKID]
	e.mu.RUnlock()
	if !ok1 || !ok2 {
		return EnvelopeCiphertext{}, ErrUnknownKey
	}
	wrapped, err := hex.DecodeString(ec.WrappedDataKey)
	if err != nil || len(wrapped) < 12 {
		return EnvelopeCiphertext{}, ErrCiphertextBad
	}
	dataKey, err := gcmOpen(oldKEK, wrapped[12:], wrapped[:12], []byte(ec.KEKKeyID))
	if err != nil {
		return EnvelopeCiphertext{}, ErrCiphertextBad
	}
	rewrapped, nonce, err := gcmSeal(newKEK, dataKey, []byte(newKEKID))
	if err != nil {
		return EnvelopeCiphertext{}, err
	}
	out := ec
	out.KEKKeyID, out.KEKVersion = newKEKID, newVer
	out.WrappedDataKey = hex.EncodeToString(append(nonce, rewrapped...))
	return out, nil
}

func gcmSeal(key, plaintext, aad []byte) (ct, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return g.Seal(nil, nonce, plaintext, aad), nonce, nil
}

func gcmOpen(key, ct, nonce, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, nonce, ct, aad)
}
