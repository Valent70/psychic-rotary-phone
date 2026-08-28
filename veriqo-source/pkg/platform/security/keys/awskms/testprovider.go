package awskms

// DeterministicTestProvider is the local/unit-test stand-in this
// package's own mandate requires: "Local tests must use a
// deterministic test provider." It generates REAL Ed25519/ECDSA
// keypairs via Go's standard library crypto (never a fake or stubbed
// signature), but the randomness feeding key generation is a
// deterministic byte stream derived from a fixed seed plus the key ID
// -- so the same seed always produces byte-identical keys across runs,
// with no network call and no dependency on crypto/rand's real
// entropy source for key material. Signing itself still uses
// crypto/rand for the ECDSA nonce, exactly as real signing must; only
// key GENERATION is deterministic.
//
// It implements the full keys.AWSKeyProvider surface (DescribeKey,
// RefreshPublicKey, Rotate) so tests can exercise every failure mode
// and the rotation path this package's mandate requires without ever
// reaching a real KMS endpoint.

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"sync"

	"veriqo/pkg/platform/security/keys"
)

// deterministicReader is an io.Reader producing a reproducible byte
// stream from a fixed seed: block i is SHA-256(seed || i). It is used
// ONLY to seed deterministic key generation for tests -- never for
// anything resembling a production randomness source.
type deterministicReader struct {
	seed    []byte
	counter uint64
	buf     []byte
}

func newDeterministicReader(seed []byte) *deterministicReader {
	return &deterministicReader{seed: append([]byte(nil), seed...)}
}

// ecdhCurveFor maps an elliptic.Curve to its crypto/ecdh equivalent, so
// deterministicECDSAKey can derive the public point via crypto/ecdh
// instead of the deprecated low-level elliptic.Curve.ScalarBaseMult.
func ecdhCurveFor(curve elliptic.Curve) (ecdh.Curve, error) {
	switch curve {
	case elliptic.P256():
		return ecdh.P256(), nil
	case elliptic.P384():
		return ecdh.P384(), nil
	case elliptic.P521():
		return ecdh.P521(), nil
	default:
		return nil, fmt.Errorf("awskms: no crypto/ecdh curve for %s", curve.Params().Name)
	}
}

// deterministicECDSAKey derives an ECDSA private key on curve entirely
// from reader's deterministic byte stream. It deliberately does NOT
// call the stdlib's ecdsa.GenerateKey: that function calls
// crypto/internal/randutil.MaybeReadByte, which consumes zero or one
// extra byte from whatever reader it's given based on real wall-clock
// parity (harmless for real key generation, its purpose there) --
// which silently defeats determinism from a fixed seed, since whether
// that extra byte gets consumed depends on when the test happens to
// run, not on the seed.
//
// It also deliberately avoids the deprecated low-level
// elliptic.Curve.ScalarBaseMult: crypto/ecdh.Curve.NewPrivateKey both
// validates the candidate scalar is in the curve's valid range
// (rejecting and letting us retry deterministically otherwise, the
// same unbiased rejection-sampling approach ecdsa.GenerateKey itself
// uses) and derives the public point, so the private scalar is the
// only value this function reads from reader.
func deterministicECDSAKey(curve elliptic.Curve, reader io.Reader) (*ecdsa.PrivateKey, error) {
	ec, err := ecdhCurveFor(curve)
	if err != nil {
		return nil, err
	}
	byteLen := (curve.Params().N.BitLen() + 7) / 8
	for {
		buf := make([]byte, byteLen)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return nil, err
		}
		ecdhPriv, err := ec.NewPrivateKey(buf)
		if err != nil {
			continue // candidate scalar out of range; deterministically draw more bytes and retry
		}
		pubBytes := ecdhPriv.PublicKey().Bytes() // uncompressed SEC1: 0x04 || X || Y
		if len(pubBytes) != 1+2*byteLen || pubBytes[0] != 0x04 {
			return nil, fmt.Errorf("awskms: unexpected ECDH public key encoding (len=%d)", len(pubBytes))
		}
		priv := new(ecdsa.PrivateKey)
		priv.PublicKey.Curve = curve
		priv.D = new(big.Int).SetBytes(ecdhPriv.Bytes())
		priv.PublicKey.X = new(big.Int).SetBytes(pubBytes[1 : 1+byteLen])
		priv.PublicKey.Y = new(big.Int).SetBytes(pubBytes[1+byteLen:])
		return priv, nil
	}
}

func (r *deterministicReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.buf) == 0 {
			var ctr [8]byte
			binary.BigEndian.PutUint64(ctr[:], r.counter)
			r.counter++
			block := make([]byte, 0, len(r.seed)+8)
			block = append(block, r.seed...)
			block = append(block, ctr[:]...)
			sum := sha256.Sum256(block)
			r.buf = sum[:]
		}
		c := copy(p[n:], r.buf)
		r.buf = r.buf[c:]
		n += c
	}
	return n, nil
}

type testKeyState struct {
	algorithm   string
	enabled     bool
	rotationGen int
	ed25519Priv ed25519.PrivateKey
	ecdsaPriv   *ecdsa.PrivateKey
}

func (st *testKeyState) publicKeyDER() ([]byte, error) {
	switch st.algorithm {
	case AlgEDDSA:
		return x509.MarshalPKIXPublicKey(st.ed25519Priv.Public())
	default:
		return x509.MarshalPKIXPublicKey(&st.ecdsaPriv.PublicKey)
	}
}

// DeterministicTestProvider is an in-memory keys.AWSKeyProvider for
// tests. It is never the default anywhere in production code.
type DeterministicTestProvider struct {
	mu   sync.RWMutex
	seed []byte
	keys map[string]*testKeyState
}

// NewDeterministicTestProvider constructs a test provider whose key
// generation is fully determined by seed: the same seed and the same
// sequence of AddKey/Rotate calls always produce byte-identical keys.
func NewDeterministicTestProvider(seed []byte) *DeterministicTestProvider {
	return &DeterministicTestProvider{seed: append([]byte(nil), seed...), keys: map[string]*testKeyState{}}
}

// SoftwareBacked implements keys.SoftwareBacked: this provider's
// "private keys" live in process memory, generated from a test seed --
// exactly what keys.RequireProductionSafe must refuse under
// env=production, the same as keys.MemoryKeyProvider.
func (p *DeterministicTestProvider) SoftwareBacked() bool { return true }

func generateKeyMaterial(seed []byte, keyID, algorithm string, gen int) (*testKeyState, error) {
	reader := newDeterministicReader(append(append([]byte(nil), seed...), []byte(fmt.Sprintf("%s#%d", keyID, gen))...))
	st := &testKeyState{algorithm: algorithm, enabled: true, rotationGen: gen}
	switch algorithm {
	case AlgEDDSA:
		_, priv, err := ed25519.GenerateKey(reader)
		if err != nil {
			return nil, err
		}
		st.ed25519Priv = priv
	case AlgECDSA256:
		priv, err := deterministicECDSAKey(elliptic.P256(), reader)
		if err != nil {
			return nil, err
		}
		st.ecdsaPriv = priv
	case AlgECDSA384:
		priv, err := deterministicECDSAKey(elliptic.P384(), reader)
		if err != nil {
			return nil, err
		}
		st.ecdsaPriv = priv
	case AlgECDSA512:
		priv, err := deterministicECDSAKey(elliptic.P521(), reader)
		if err != nil {
			return nil, err
		}
		st.ecdsaPriv = priv
	default:
		return nil, fmt.Errorf("%w: %q", ErrAlgorithmNotSupported, algorithm)
	}
	return st, nil
}

// AddKey deterministically generates a new key of the given algorithm
// under keyID and returns its DER-encoded public key. algorithm must
// be one of AlgEDDSA, AlgECDSA256, AlgECDSA384, AlgECDSA512 -- there is
// no default.
func (p *DeterministicTestProvider) AddKey(keyID, algorithm string) ([]byte, error) {
	if !knownAlgorithm(algorithm) {
		return nil, fmt.Errorf("%w: %q", ErrAlgorithmNotSupported, algorithm)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.keys[keyID]; dup {
		return nil, fmt.Errorf("awskms: test provider: key %q already exists", keyID)
	}
	st, err := generateKeyMaterial(p.seed, keyID, algorithm, 0)
	if err != nil {
		return nil, err
	}
	p.keys[keyID] = st
	return st.publicKeyDER()
}

// SetEnabled flips a key's enabled/disabled state, modeling AWS KMS's
// KeyState transitioning to/from "Disabled".
func (p *DeterministicTestProvider) SetEnabled(keyID string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.keys[keyID]
	if !ok {
		return fmt.Errorf("%w: %s", keys.ErrUnknownKey, keyID)
	}
	st.enabled = enabled
	return nil
}

// Rotate simulates AWS KMS automatic key rotation: keyID and algorithm
// stay the same, but the underlying key material is regenerated (under
// a rotation-generation-salted deterministic seed, so it is still
// reproducible, and still different from every prior generation of
// this key). Returns the new DER-encoded public key.
func (p *DeterministicTestProvider) Rotate(keyID string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	old, ok := p.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", keys.ErrUnknownKey, keyID)
	}
	next, err := generateKeyMaterial(p.seed, keyID, old.algorithm, old.rotationGen+1)
	if err != nil {
		return nil, err
	}
	next.enabled = old.enabled
	p.keys[keyID] = next
	return next.publicKeyDER()
}

// Sign implements keys.KeyProvider.
func (p *DeterministicTestProvider) Sign(_ context.Context, keyID string, digest []byte) ([]byte, error) {
	p.mu.RLock()
	st, ok := p.keys[keyID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", keys.ErrUnknownKey, keyID)
	}
	if !st.enabled {
		return nil, fmt.Errorf("%w: key %s is disabled", ErrKeyDisabled, keyID)
	}
	switch st.algorithm {
	case AlgEDDSA:
		return ed25519.Sign(st.ed25519Priv, digest), nil
	case AlgECDSA256, AlgECDSA384, AlgECDSA512:
		// The ECDSA NONCE must come from a real CSPRNG even though key
		// GENERATION above was deterministic -- crypto/rand here is
		// correct real signing behavior, not a determinism violation.
		return ecdsa.SignASN1(rand.Reader, st.ecdsaPriv, digest)
	default:
		return nil, fmt.Errorf("%w: %s", ErrAlgorithmNotSupported, st.algorithm)
	}
}

// PublicKey implements keys.KeyProvider, returning the DER-encoded
// SubjectPublicKeyInfo -- never private key material.
func (p *DeterministicTestProvider) PublicKey(_ context.Context, keyID string) ([]byte, error) {
	p.mu.RLock()
	st, ok := p.keys[keyID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", keys.ErrUnknownKey, keyID)
	}
	return st.publicKeyDER()
}

// DescribeKey implements keys.AWSKeyProvider.
func (p *DeterministicTestProvider) DescribeKey(_ context.Context, keyID string) (keys.RemoteKeyState, error) {
	p.mu.RLock()
	st, ok := p.keys[keyID]
	p.mu.RUnlock()
	if !ok {
		return keys.RemoteKeyState{}, fmt.Errorf("%w: %s", keys.ErrUnknownKey, keyID)
	}
	state := "Enabled"
	if !st.enabled {
		state = "Disabled"
	}
	return keys.RemoteKeyState{
		KeyID:             keyID,
		Enabled:           st.enabled,
		State:             state,
		SigningAlgorithms: []string{st.algorithm},
	}, nil
}

// RefreshPublicKey implements keys.AWSKeyProvider.
func (p *DeterministicTestProvider) RefreshPublicKey(ctx context.Context, keyID string, cached []byte) ([]byte, bool, error) {
	pub, err := p.PublicKey(ctx, keyID)
	if err != nil {
		return nil, false, err
	}
	return pub, !bytes.Equal(pub, cached), nil
}

var _ keys.AWSKeyProvider = (*DeterministicTestProvider)(nil)
var _ keys.AWSKeyProvider = (*AWSKMSProvider)(nil)
