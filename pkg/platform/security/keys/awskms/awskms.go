// Package awskms is PART 8 of the audit mandate: a production-oriented
// AWS KMS adapter implementing pkg/platform/security/keys's
// KeyProvider (and the wider AWSKeyProvider it adds additively),
// against the real AWS KMS REST/JSON API -- signed by hand with
// crypto/hmac + crypto/sha256, never the AWS SDK, per this module's
// zero-dependency constraint.
//
// This package does not duplicate or relax anything
// pkg/blockers/hsmkms and pkg/platform/security/keys already
// established:
//
//   - keys.KeyProvider stays the audit's required interface. Sign
//     takes a digest and returns a signature; nothing on this
//     interface -- or on AWSKMSProvider -- can return private key
//     material, because AWS KMS itself never exposes it over this API
//     and no method here is shaped to ask.
//   - keys.SoftwareBacked / keys.RequireProductionSafe is the existing
//     production guard. AWSKMSProvider explicitly answers
//     SoftwareBacked() == false: its keys never leave AWS KMS, so a
//     production deployment may use it. DeterministicTestProvider
//     explicitly answers true: it is an in-memory fixture and
//     RequireProductionSafe must refuse it under env=production, the
//     same as keys.MemoryKeyProvider.
//   - pkg/blockers/hsmkms's FailingKeyProvider already proves the
//     fail-closed CONTRACT (unavailable/timeout/permission-denied/
//     wrong-key/revoked never produce a signature) against a fixture.
//     This package is what that contract looks like wired to a real
//     wire protocol: DescribeKey, GetPublicKey, Sign, and their real
//     HTTP/network failure modes.
//
// Algorithm handling is the requirement this package is strictest
// about: SigningAlgorithm is an explicit, required piece of Config
// (never a silent default), and Sign() cross-checks it against the
// key's OWN DescribeKey response before ever calling TrentService.Sign
// -- so this code cannot claim, or fall back to, an algorithm the
// configured KMS key does not actually advertise. If a key's
// SigningAlgorithms happens to list "EDDSA", this package will use
// Ed25519 for it; it does not assume Ed25519 for any key that hasn't
// said so itself.
//
// See docs/governance/HSM_KMS_POLICY.md for the spec this
// implements, and awskms_test.go / awskms_integration_test.go for how
// it is proven: entirely offline against DeterministicTestProvider and
// a fake HTTP transport in normal test runs, and for real -- against a
// real AWS KMS tenancy over the real network -- only when
// VERIQO_AWS_KMS_INTEGRATION_TEST=1 is set.
package awskms

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"veriqo/pkg/platform/security/keys"
)

// Signing algorithms this package knows how to use and independently
// verify. These are AWS KMS's own algorithm names (SigningAlgorithmSpec
// values) -- not invented by this package -- and Config.SigningAlgorithm
// must be exactly one of them. Notably absent: any RSA algorithm, and
// any assumption that a key supports Ed25519 unless its own DescribeKey
// response says AlgEDDSA is in SigningAlgorithms.
const (
	AlgEDDSA    = "EDDSA"         // Ed25519 (AWS KMS KeySpec ECC_EDWARDS25519)
	AlgECDSA256 = "ECDSA_SHA_256" // NIST P-256
	AlgECDSA384 = "ECDSA_SHA_384" // NIST P-384
	AlgECDSA512 = "ECDSA_SHA_512" // NIST P-521 (KMS names the algorithm _512 for the SHA-512 digest; the curve is P-521)
)

func knownAlgorithm(alg string) bool {
	switch alg {
	case AlgEDDSA, AlgECDSA256, AlgECDSA384, AlgECDSA512:
		return true
	default:
		return false
	}
}

// Errors. Each failure path required by the audit mandate has its own
// sentinel so a caller (and a test) can distinguish them with
// errors.Is rather than string matching.
var (
	// ErrKMSUnavailable wraps any network/transport-level failure
	// reaching the KMS endpoint: DNS, connection refused, TLS failure,
	// timeout. There is never a fallback to a local/software key from
	// here -- the caller gets an error, full stop.
	ErrKMSUnavailable = errors.New("awskms: KMS endpoint unavailable")

	// ErrKeyDisabled is returned by Sign when the key's own
	// DescribeKey response reports it is not Enabled. Checked BEFORE
	// any TrentService.Sign call is made.
	ErrKeyDisabled = errors.New("awskms: key is not in an Enabled state")

	// ErrAlgorithmNotSupported is returned when the configured signing
	// algorithm is not present in the key's own SigningAlgorithms list,
	// or when VerifyIndependently is asked to verify against a public
	// key whose real Go type does not match the requested algorithm.
	// There is never a silent fallback to a different algorithm.
	ErrAlgorithmNotSupported = errors.New("awskms: signing algorithm is not supported by this key")

	// ErrSignatureInvalid is VerifyIndependently's failure: the
	// signature does not verify under the given public key and digest,
	// using this package's own stdlib verification -- independent of
	// whatever the signer (real KMS or the test provider) claimed.
	ErrSignatureInvalid = errors.New("awskms: signature does not verify")

	// ErrKMSResponse wraps a malformed or unexpected KMS response body
	// (bad JSON, missing field, bad base64, non-200 status) -- distinct
	// from ErrKMSUnavailable because the endpoint WAS reached.
	ErrKMSResponse = errors.New("awskms: unexpected response from KMS")

	// ErrInvalidConfig is returned by New for an incomplete or invalid
	// Config -- e.g. a missing region, key ID, credential provider, or
	// an algorithm outside the known set. Configuration is validated
	// once at construction, not discovered later from a failed sign.
	ErrInvalidConfig = errors.New("awskms: invalid configuration")
)

// maxResponseBytes bounds how much of a KMS HTTP response body this
// package will read, so a misbehaving or malicious endpoint cannot
// exhaust memory. Real KMS responses (a DER public key, a signature,
// key metadata) are a few KB at most.
const maxResponseBytes = 1 << 20 // 1 MiB

// Credentials is the exact set of values AWS SigV4 signing needs.
// Never hardcoded anywhere in this package -- always supplied by a
// CredentialProvider the deployment controls.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // optional: set for temporary/STS credentials
}

// CredentialProvider supplies AWS credentials on demand, so a
// deployment can back it with environment variables, an instance
// role, a secrets manager, or credential rotation -- this package
// never reads any of those sources itself.
type CredentialProvider interface {
	Credentials(ctx context.Context) (Credentials, error)
}

// StaticCredentials is the simplest CredentialProvider: fixed values
// the caller already resolved (e.g. from its own environment/secret
// reads, done by the deployment, not by this package). It implements
// CredentialProvider directly.
type StaticCredentials Credentials

// Credentials implements CredentialProvider.
func (s StaticCredentials) Credentials(context.Context) (Credentials, error) {
	return Credentials(s), nil
}

// Config configures one AWSKMSProvider. Every field is explicit; there
// is no hardcoded region, key, or algorithm anywhere in this package.
type Config struct {
	// Region is the AWS region hosting the key, e.g. "us-east-1".
	// Required.
	Region string
	// SigningAlgorithm is the SigningAlgorithmSpec this provider will
	// request for every Sign call, e.g. AlgECDSA256 or AlgEDDSA.
	// Required, and validated against the key's own DescribeKey
	// response at Sign time -- see the package doc comment.
	SigningAlgorithm string
	// Credentials resolves AWS credentials for each request. Required.
	Credentials CredentialProvider
	// HTTPClient performs the signed HTTP requests. Optional: if nil,
	// a client with a 10s timeout is used. Tests inject a fake
	// transport here to simulate KMS responses and failures without
	// any real network access.
	HTTPClient httpDoer
	// Endpoint overrides the default
	// "https://kms.<Region>.amazonaws.com/" KMS endpoint. Optional;
	// exists for testing against a local stand-in and for VPC
	// endpoints, never for pointing production at something that
	// isn't KMS.
	Endpoint string
}

// httpDoer is the minimal surface AWSKMSProvider needs from an HTTP
// client -- satisfied by *http.Client and by any fake transport a test
// wants to inject.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// AWSKMSProvider implements keys.AWSKeyProvider (and therefore
// keys.KeyProvider) against the real AWS KMS REST/JSON API.
//
// No method on this type can return private key material: Sign takes
// a digest and returns a signature, PublicKey and DescribeKey return
// only public information, and there is deliberately no method shaped
// like "Export" or "GetPrivateKey" anywhere in this file.
type AWSKMSProvider struct {
	cfg    Config
	client httpDoer
}

// New validates cfg and constructs an AWSKMSProvider. Validation
// happens once, here -- not discovered later from a failed Sign call.
func New(cfg Config) (*AWSKMSProvider, error) {
	switch {
	case cfg.Region == "":
		return nil, fmt.Errorf("%w: Region is required", ErrInvalidConfig)
	case cfg.SigningAlgorithm == "":
		return nil, fmt.Errorf("%w: SigningAlgorithm is required (explicit algorithm configuration; never assumed)", ErrInvalidConfig)
	case !knownAlgorithm(cfg.SigningAlgorithm):
		return nil, fmt.Errorf("%w: SigningAlgorithm %q is not one of the algorithms this package supports", ErrInvalidConfig, cfg.SigningAlgorithm)
	case cfg.Credentials == nil:
		return nil, fmt.Errorf("%w: Credentials provider is required", ErrInvalidConfig)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &AWSKMSProvider{cfg: cfg, client: client}, nil
}

// SoftwareBacked implements keys.SoftwareBacked. AWS KMS never
// releases private key material from the HSMs backing it (asymmetric
// KMS keys are non-exportable by construction), so this adapter is
// explicitly NOT software-backed and keys.RequireProductionSafe must
// accept it under env=production.
func (p *AWSKMSProvider) SoftwareBacked() bool { return false }

func (p *AWSKMSProvider) endpoint() string {
	if p.cfg.Endpoint != "" {
		return p.cfg.Endpoint
	}
	return fmt.Sprintf("https://kms.%s.amazonaws.com/", p.cfg.Region)
}

// call performs one signed KMS JSON-1.1 request for the given
// X-Amz-Target action and returns the raw response body on HTTP 200.
// Any transport-level failure is wrapped in ErrKMSUnavailable; any
// non-200 or unreadable response is wrapped in ErrKMSResponse. There
// is no retry and no fallback here -- one failed call is one failed
// call, reported to the caller to handle.
func (p *AWSKMSProvider) call(ctx context.Context, target string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("awskms: encode request for %s: %w", target, err)
	}
	creds, err := p.cfg.Credentials.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("awskms: resolve credentials for %s: %w", target, err)
	}
	req, err := newSignedRequest(ctx, p.endpoint(), p.cfg.Region, target, payload, creds)
	if err != nil {
		return nil, fmt.Errorf("awskms: build signed request for %s: %w", target, err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrKMSUnavailable, target, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: reading response: %v", ErrKMSResponse, target, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s: HTTP %d: %s", ErrKMSResponse, target, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// --- DescribeKey --------------------------------------------------

type describeKeyResponse struct {
	KeyMetadata struct {
		KeyId             string   `json:"KeyId"`
		KeyState          string   `json:"KeyState"`
		KeySpec           string   `json:"KeySpec"`
		SigningAlgorithms []string `json:"SigningAlgorithms"`
	} `json:"KeyMetadata"`
}

// DescribeKey implements keys.AWSKeyProvider: it calls the real
// TrentService.DescribeKey action and returns the key's live state and
// supported algorithms exactly as KMS reports them.
func (p *AWSKMSProvider) DescribeKey(ctx context.Context, keyID string) (keys.RemoteKeyState, error) {
	body, err := p.call(ctx, "TrentService.DescribeKey", map[string]string{"KeyId": keyID})
	if err != nil {
		return keys.RemoteKeyState{}, err
	}
	var parsed describeKeyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return keys.RemoteKeyState{}, fmt.Errorf("%w: DescribeKey: %v", ErrKMSResponse, err)
	}
	return keys.RemoteKeyState{
		KeyID:             parsed.KeyMetadata.KeyId,
		Enabled:           parsed.KeyMetadata.KeyState == "Enabled",
		State:             parsed.KeyMetadata.KeyState,
		SigningAlgorithms: parsed.KeyMetadata.SigningAlgorithms,
	}, nil
}

// --- GetPublicKey ----------------------------------------------------

type getPublicKeyResponse struct {
	PublicKey         string   `json:"PublicKey"` // base64 DER SubjectPublicKeyInfo
	SigningAlgorithms []string `json:"SigningAlgorithms"`
}

// PublicKey implements keys.KeyProvider. It calls the real
// TrentService.GetPublicKey action and returns the DER-encoded
// SubjectPublicKeyInfo AWS KMS reports -- never private key material;
// KMS's own API has no operation that would return that, and this
// method calls no such operation.
func (p *AWSKMSProvider) PublicKey(ctx context.Context, keyID string) ([]byte, error) {
	body, err := p.call(ctx, "TrentService.GetPublicKey", map[string]string{"KeyId": keyID})
	if err != nil {
		return nil, err
	}
	var parsed getPublicKeyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%w: GetPublicKey: %v", ErrKMSResponse, err)
	}
	pub, err := base64.StdEncoding.DecodeString(parsed.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: GetPublicKey: bad base64 public key: %v", ErrKMSResponse, err)
	}
	return pub, nil
}

// RefreshPublicKey implements keys.AWSKeyProvider: it always fetches
// PublicKey fresh from KMS (bypassing any cache the caller may keep)
// and reports whether the bytes differ from cached -- the rotation
// detection primitive. AWS KMS automatic key rotation keeps KeyID
// stable while replacing the underlying key material, so only a fresh
// byte comparison can detect it; the KeyID alone never can.
func (p *AWSKMSProvider) RefreshPublicKey(ctx context.Context, keyID string, cached []byte) ([]byte, bool, error) {
	pub, err := p.PublicKey(ctx, keyID)
	if err != nil {
		return nil, false, err
	}
	return pub, !bytes.Equal(pub, cached), nil
}

// --- Sign --------------------------------------------------------

type signResponse struct {
	Signature        string `json:"Signature"` // base64
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

// Sign implements keys.KeyProvider against the real TrentService.Sign
// action. Before ever calling it, Sign:
//
//  1. Calls DescribeKey and verifies the key is Enabled -- a disabled
//     key fails closed here, never reaching the signing call
//     (ErrKeyDisabled).
//  2. Verifies the configured SigningAlgorithm is one this specific
//     key's own SigningAlgorithms actually lists -- a mismatch fails
//     closed here too, with no fallback to a different algorithm
//     (ErrAlgorithmNotSupported).
//
// For every algorithm except AlgEDDSA, digest is sent as MessageType
// "DIGEST": callers pre-hash the message themselves (with the hash the
// configured algorithm expects) and KMS signs the digest directly
// rather than re-hashing a raw message. AWS KMS does not support
// MessageType "DIGEST" for EDDSA (Ed25519 hashes its input internally
// as part of the algorithm, so KMS requires MessageType "RAW" and
// expects the raw message, not a pre-hashed digest, in that one case)
// -- callers configured with AlgEDDSA must pass the raw message here,
// not a SHA-256 of it.
func (p *AWSKMSProvider) Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error) {
	if len(digest) == 0 {
		return nil, fmt.Errorf("awskms: Sign: empty digest")
	}
	state, err := p.DescribeKey(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if !state.Enabled {
		return nil, fmt.Errorf("%w: key %s (state=%s)", ErrKeyDisabled, keyID, state.State)
	}
	if !containsString(state.SigningAlgorithms, p.cfg.SigningAlgorithm) {
		return nil, fmt.Errorf("%w: key %s supports %v, configured %q", ErrAlgorithmNotSupported, keyID, state.SigningAlgorithms, p.cfg.SigningAlgorithm)
	}
	messageType := "DIGEST"
	if p.cfg.SigningAlgorithm == AlgEDDSA {
		messageType = "RAW" // KMS does not accept a pre-hashed digest for EDDSA
	}
	body := map[string]string{
		"KeyId":            keyID,
		"Message":          base64.StdEncoding.EncodeToString(digest),
		"MessageType":      messageType,
		"SigningAlgorithm": p.cfg.SigningAlgorithm,
	}
	respBody, err := p.call(ctx, "TrentService.Sign", body)
	if err != nil {
		return nil, err
	}
	var parsed signResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("%w: Sign: %v", ErrKMSResponse, err)
	}
	sig, err := base64.StdEncoding.DecodeString(parsed.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: Sign: bad base64 signature: %v", ErrKMSResponse, err)
	}
	return sig, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// VerifyIndependently verifies a signature against a DER-encoded
// SubjectPublicKeyInfo public key using only Go's standard library
// crypto -- independent of whichever provider (real KMS or
// DeterministicTestProvider) produced the signature, so that provider
// claiming "signed successfully" is never the last word.
//
// It uses algorithm to pick the verification routine, and REFUSES to
// verify if the parsed public key's real Go type does not match what
// that algorithm implies (e.g. algorithm AlgEDDSA against an ECDSA
// key): a wrong-algorithm request must fail closed here too, not
// silently degrade to whatever the key happens to be.
func VerifyIndependently(pubDER []byte, algorithm string, digest, sig []byte) error {
	pub, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		return fmt.Errorf("%w: parse public key: %v", ErrKMSResponse, err)
	}
	switch algorithm {
	case AlgEDDSA:
		edPub, ok := pub.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: algorithm %s requested but key is not Ed25519", ErrAlgorithmNotSupported, algorithm)
		}
		if !ed25519.Verify(edPub, digest, sig) {
			return ErrSignatureInvalid
		}
		return nil
	case AlgECDSA256, AlgECDSA384, AlgECDSA512:
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: algorithm %s requested but key is not ECDSA", ErrAlgorithmNotSupported, algorithm)
		}
		if !ecdsa.VerifyASN1(ecPub, digest, sig) {
			return ErrSignatureInvalid
		}
		return nil
	default:
		return fmt.Errorf("%w: %q is not a known signing algorithm", ErrAlgorithmNotSupported, algorithm)
	}
}
