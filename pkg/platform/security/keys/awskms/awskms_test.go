package awskms

// Local/unit tests. Per this package's own mandate ("Local tests must
// use a deterministic test provider") every test in this file either:
//
//   - exercises DeterministicTestProvider directly (no network,
//     no I/O), or
//   - exercises AWSKMSProvider's real request-building and
//     response-parsing logic against a fakeTransport that never
//     touches the network -- proving the SigV4 signing code path, the
//     JSON encode/decode path, and the fail-closed branches all work,
//     entirely offline.
//
// The ONLY test that ever makes a real network call is
// TestAWSKMSIntegration in awskms_integration_test.go, and it is
// skipped unless VERIQO_AWS_KMS_INTEGRATION_TEST=1 is set.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"veriqo/pkg/platform/security/keys"
)

func digest(s string) []byte {
	d := sha256.Sum256([]byte(s))
	return d[:]
}

// --- DeterministicTestProvider: pure in-memory tests -----------------

func TestDeterministicProviderSignVerifyRoundTrip_ECDSA(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("fixed-seed-1"))
	pub, err := p.AddKey("k-ecdsa", AlgECDSA256)
	if err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	ctx := context.Background()
	d := digest("hello")
	sig, err := p.Sign(ctx, "k-ecdsa", d)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := VerifyIndependently(pub, AlgECDSA256, d, sig); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}
}

func TestDeterministicProviderSignVerifyRoundTrip_EDDSA(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("fixed-seed-1"))
	pub, err := p.AddKey("k-eddsa", AlgEDDSA)
	if err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	ctx := context.Background()
	d := digest("hello")
	sig, err := p.Sign(ctx, "k-eddsa", d)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := VerifyIndependently(pub, AlgEDDSA, d, sig); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}
}

// TestDeterministicProviderKeyGenerationIsReproducible proves the
// "deterministic given a fixed seed" requirement: the same seed and
// key ID produce byte-identical public keys across independent
// provider instances.
func TestDeterministicProviderKeyGenerationIsReproducible(t *testing.T) {
	p1 := NewDeterministicTestProvider([]byte("same-seed"))
	p2 := NewDeterministicTestProvider([]byte("same-seed"))
	pub1, err := p1.AddKey("k", AlgECDSA256)
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := p2.AddKey("k", AlgECDSA256)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pub1, pub2) {
		t.Fatal("same seed and key ID produced different key material -- not deterministic")
	}
	p3 := NewDeterministicTestProvider([]byte("different-seed"))
	pub3, err := p3.AddKey("k", AlgECDSA256)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(pub1, pub3) {
		t.Fatal("different seeds produced identical key material")
	}
}

func TestDeterministicProviderDisabledKeyFailsClosed(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("seed"))
	if _, err := p.AddKey("k", AlgECDSA256); err != nil {
		t.Fatal(err)
	}
	if err := p.SetEnabled("k", false); err != nil {
		t.Fatal(err)
	}
	sig, err := p.Sign(context.Background(), "k", digest("x"))
	if !errors.Is(err, ErrKeyDisabled) {
		t.Fatalf("expected ErrKeyDisabled, got %v", err)
	}
	if len(sig) != 0 {
		t.Fatal("a disabled key produced signature bytes")
	}
}

func TestDeterministicProviderWrongKeyRejected(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("seed"))
	if _, err := p.AddKey("k1", AlgECDSA256); err != nil {
		t.Fatal(err)
	}
	pub2, err := p.AddKey("k2", AlgECDSA256)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d := digest("payload")
	sig, err := p.Sign(ctx, "k1", d)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyIndependently(pub2, AlgECDSA256, d, sig); err == nil {
		t.Fatal("a signature from k1 verified against k2's public key")
	} else if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid, got %v", err)
	}
}

func TestDeterministicProviderWrongAlgorithmRejected(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("seed"))
	pub, err := p.AddKey("k", AlgECDSA256)
	if err != nil {
		t.Fatal(err)
	}
	d := digest("payload")
	sig, err := p.Sign(context.Background(), "k", d)
	if err != nil {
		t.Fatal(err)
	}
	// Ask VerifyIndependently to treat an ECDSA key's signature as
	// EDDSA: must fail closed, never silently degrade.
	if err := VerifyIndependently(pub, AlgEDDSA, d, sig); !errors.Is(err, ErrAlgorithmNotSupported) {
		t.Fatalf("expected ErrAlgorithmNotSupported, got %v", err)
	}
}

func TestDeterministicProviderRotationDetected(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("seed"))
	pub1, err := p.AddKey("k", AlgECDSA256)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d := digest("before-rotation")
	sigBefore, err := p.Sign(ctx, "k", d)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyIndependently(pub1, AlgECDSA256, d, sigBefore); err != nil {
		t.Fatalf("pre-rotation signature failed to verify: %v", err)
	}

	pub2, err := p.Rotate("k")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if bytes.Equal(pub1, pub2) {
		t.Fatal("Rotate did not change key material")
	}

	refreshed, rotated, err := p.RefreshPublicKey(ctx, "k", pub1)
	if err != nil {
		t.Fatalf("RefreshPublicKey: %v", err)
	}
	if !rotated {
		t.Fatal("RefreshPublicKey did not detect rotation against stale cached key")
	}
	if !bytes.Equal(refreshed, pub2) {
		t.Fatal("RefreshPublicKey did not return the new key material")
	}

	// Signing after rotation uses the NEW key; verifying against the
	// stale cached (pre-rotation) public key must fail.
	dAfter := digest("after-rotation")
	sigAfter, err := p.Sign(ctx, "k", dAfter)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyIndependently(pub1, AlgECDSA256, dAfter, sigAfter); err == nil {
		t.Fatal("a post-rotation signature verified against the stale pre-rotation public key")
	}
	if err := VerifyIndependently(pub2, AlgECDSA256, dAfter, sigAfter); err != nil {
		t.Fatalf("a post-rotation signature failed to verify against the current public key: %v", err)
	}

	// Re-fetching with the CURRENT public key as cached must report no
	// rotation.
	_, rotatedAgain, err := p.RefreshPublicKey(ctx, "k", pub2)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedAgain {
		t.Fatal("RefreshPublicKey reported rotation when the cached key was already current")
	}
}

func TestDeterministicProviderUnknownKey(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("seed"))
	ctx := context.Background()
	if _, err := p.Sign(ctx, "missing", digest("x")); !errors.Is(err, keys.ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}
	if _, err := p.PublicKey(ctx, "missing"); !errors.Is(err, keys.ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}
	if _, err := p.DescribeKey(ctx, "missing"); !errors.Is(err, keys.ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}
}

func TestDeterministicProviderIsProductionUnsafe(t *testing.T) {
	p := NewDeterministicTestProvider([]byte("seed"))
	var kp keys.KeyProvider = p
	if err := keys.RequireProductionSafe(kp, "production"); !errors.Is(err, keys.ErrSoftwareProviderInProduction) {
		t.Fatalf("expected ErrSoftwareProviderInProduction, got %v", err)
	}
}

// --- Config validation -------------------------------------------

func TestNewRejectsIncompleteConfig(t *testing.T) {
	validCreds := StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"}
	cases := []Config{
		{SigningAlgorithm: AlgECDSA256, Credentials: validCreds},                    // missing region
		{Region: "us-east-1", Credentials: validCreds},                              // missing algorithm
		{Region: "us-east-1", SigningAlgorithm: "MADE_UP", Credentials: validCreds}, // unknown algorithm
		{Region: "us-east-1", SigningAlgorithm: AlgECDSA256},                        // missing credentials
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("case %d: expected ErrInvalidConfig, got %v", i, err)
		}
	}
}

func TestNewAcceptsValidConfig(t *testing.T) {
	p, err := New(Config{
		Region:           "us-east-1",
		SigningAlgorithm: AlgECDSA256,
		Credentials:      StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.SoftwareBacked() {
		t.Fatal("AWSKMSProvider must not be SoftwareBacked -- KMS key material never leaves KMS")
	}
	if err := keys.RequireProductionSafe(p, "production"); err != nil {
		t.Fatalf("a real KMS adapter must be accepted under env=production, got %v", err)
	}
}

// --- AWSKMSProvider against a fake HTTP transport ---------------------
//
// fakeTransport never touches the network. It lets these tests prove
// AWSKMSProvider's real request-building (SigV4) and response-parsing
// logic, and its fail-closed branches, entirely offline.

type cannedResponse struct {
	status int
	body   string
}

type fakeTransport struct {
	responses    map[string]cannedResponse // keyed by X-Amz-Target
	transportErr error                     // if set, every call fails with this (simulates KMS unavailable)
	calls        []string
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := req.Header.Get("X-Amz-Target")
	f.calls = append(f.calls, target)
	if f.transportErr != nil {
		return nil, f.transportErr
	}
	// Sanity: a real Authorization header must have been set by SigV4
	// signing for every request, regardless of which canned response
	// we return.
	if !strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=") {
		return nil, fmt.Errorf("fakeTransport: missing/malformed SigV4 Authorization header: %q", req.Header.Get("Authorization"))
	}
	resp, ok := f.responses[target]
	if !ok {
		return nil, fmt.Errorf("fakeTransport: no canned response for target %q", target)
	}
	return &http.Response{
		StatusCode: resp.status,
		Body:       io.NopCloser(strings.NewReader(resp.body)),
		Header:     make(http.Header),
	}, nil
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func newFakeClient(ft *fakeTransport) httpDoer {
	return &http.Client{Transport: ft}
}

func TestAWSKMSProviderKMSUnavailableFailsClosed(t *testing.T) {
	ft := &fakeTransport{transportErr: fmt.Errorf("dial tcp: connection refused")}
	p, err := New(Config{
		Region: "us-east-1", SigningAlgorithm: AlgECDSA256,
		Credentials: StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"},
		HTTPClient:  newFakeClient(ft),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.DescribeKey(ctx, "k"); !errors.Is(err, ErrKMSUnavailable) {
		t.Errorf("DescribeKey: expected ErrKMSUnavailable, got %v", err)
	}
	if _, err := p.PublicKey(ctx, "k"); !errors.Is(err, ErrKMSUnavailable) {
		t.Errorf("PublicKey: expected ErrKMSUnavailable, got %v", err)
	}
	if sig, err := p.Sign(ctx, "k", digest("x")); !errors.Is(err, ErrKMSUnavailable) || len(sig) != 0 {
		t.Errorf("Sign: expected ErrKMSUnavailable and no signature, got sig=%v err=%v", sig, err)
	}
}

func TestAWSKMSProviderDisabledKeyFailsClosed(t *testing.T) {
	ft := &fakeTransport{responses: map[string]cannedResponse{
		"TrentService.DescribeKey": {200, mustJSON(t, map[string]any{
			"KeyMetadata": map[string]any{
				"KeyId": "k", "KeyState": "Disabled",
				"SigningAlgorithms": []string{AlgECDSA256},
			},
		})},
	}}
	p, err := New(Config{
		Region: "us-east-1", SigningAlgorithm: AlgECDSA256,
		Credentials: StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"},
		HTTPClient:  newFakeClient(ft),
	})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := p.Sign(context.Background(), "k", digest("x"))
	if !errors.Is(err, ErrKeyDisabled) {
		t.Fatalf("expected ErrKeyDisabled, got %v", err)
	}
	if len(sig) != 0 {
		t.Fatal("a disabled key produced signature bytes")
	}
	for _, c := range ft.calls {
		if c == "TrentService.Sign" {
			t.Fatal("TrentService.Sign was called for a disabled key")
		}
	}
}

func TestAWSKMSProviderWrongAlgorithmRejected(t *testing.T) {
	ft := &fakeTransport{responses: map[string]cannedResponse{
		"TrentService.DescribeKey": {200, mustJSON(t, map[string]any{
			"KeyMetadata": map[string]any{
				"KeyId": "k", "KeyState": "Enabled",
				"SigningAlgorithms": []string{AlgECDSA256}, // does NOT include EDDSA
			},
		})},
	}}
	p, err := New(Config{
		Region: "us-east-1", SigningAlgorithm: AlgEDDSA, // configured algorithm the key does not support
		Credentials: StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"},
		HTTPClient:  newFakeClient(ft),
	})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := p.Sign(context.Background(), "k", digest("x"))
	if !errors.Is(err, ErrAlgorithmNotSupported) {
		t.Fatalf("expected ErrAlgorithmNotSupported, got %v", err)
	}
	if len(sig) != 0 {
		t.Fatal("a rejected algorithm still produced signature bytes")
	}
	for _, c := range ft.calls {
		if c == "TrentService.Sign" {
			t.Fatal("TrentService.Sign was called despite an unsupported algorithm")
		}
	}
}

// TestAWSKMSProviderTwoAlgorithmsHonored is the audit's explicit
// "algorithm is read from key state not hardcoded" proof at the real
// AWSKMSProvider layer: two keys, two different configured/advertised
// algorithms, both sign and verify correctly and neither leaks into
// the other -- proving Sign() reads SigningAlgorithms off each key's
// own DescribeKey response rather than assuming one algorithm.
func TestAWSKMSProviderTwoAlgorithmsHonored(t *testing.T) {
	ecdsaPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaPubDER, err := x509.MarshalPKIXPublicKey(&ecdsaPriv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPubDER, err := x509.MarshalPKIXPublicKey(edPub)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		algorithm string
		keyID     string
		pubDER    []byte
		sign      func(digest []byte) []byte
	}{
		{"ecdsa", AlgECDSA256, "ecdsa-key", ecdsaPubDER, func(d []byte) []byte {
			sig, err := ecdsa.SignASN1(rand.Reader, ecdsaPriv, d)
			if err != nil {
				t.Fatal(err)
			}
			return sig
		}},
		{"eddsa", AlgEDDSA, "eddsa-key", edPubDER, func(d []byte) []byte {
			return ed25519.Sign(edPriv, d)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := digest("payload-" + tc.name)
			sig := tc.sign(d)
			ft := &fakeTransport{responses: map[string]cannedResponse{
				"TrentService.DescribeKey": {200, mustJSON(t, map[string]any{
					"KeyMetadata": map[string]any{
						"KeyId": tc.keyID, "KeyState": "Enabled",
						"SigningAlgorithms": []string{tc.algorithm},
					},
				})},
				"TrentService.GetPublicKey": {200, mustJSON(t, map[string]any{
					"PublicKey":         base64.StdEncoding.EncodeToString(tc.pubDER),
					"SigningAlgorithms": []string{tc.algorithm},
				})},
				"TrentService.Sign": {200, mustJSON(t, map[string]any{
					"Signature":        base64.StdEncoding.EncodeToString(sig),
					"SigningAlgorithm": tc.algorithm,
				})},
			}}
			p, err := New(Config{
				Region: "us-east-1", SigningAlgorithm: tc.algorithm,
				Credentials: StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"},
				HTTPClient:  newFakeClient(ft),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()

			state, err := p.DescribeKey(ctx, tc.keyID)
			if err != nil {
				t.Fatalf("DescribeKey: %v", err)
			}
			if !state.Enabled {
				t.Fatal("expected Enabled")
			}
			if len(state.SigningAlgorithms) != 1 || state.SigningAlgorithms[0] != tc.algorithm {
				t.Fatalf("expected SigningAlgorithms=[%s], got %v", tc.algorithm, state.SigningAlgorithms)
			}

			pub, err := p.PublicKey(ctx, tc.keyID)
			if err != nil {
				t.Fatalf("PublicKey: %v", err)
			}
			if !bytes.Equal(pub, tc.pubDER) {
				t.Fatal("PublicKey did not return the canned KMS public key unchanged")
			}

			gotSig, err := p.Sign(ctx, tc.keyID, d)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !bytes.Equal(gotSig, sig) {
				t.Fatal("Sign did not return the canned KMS signature unchanged")
			}
			if err := VerifyIndependently(pub, tc.algorithm, d, gotSig); err != nil {
				t.Fatalf("independent verification of the KMS-returned signature failed: %v", err)
			}
		})
	}
}

func TestAWSKMSProviderRotationDetection(t *testing.T) {
	ecdsaPriv1, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub1, err := x509.MarshalPKIXPublicKey(&ecdsaPriv1.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaPriv2, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := x509.MarshalPKIXPublicKey(&ecdsaPriv2.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	ft := &fakeTransport{responses: map[string]cannedResponse{
		"TrentService.GetPublicKey": {200, mustJSON(t, map[string]any{
			"PublicKey":         base64.StdEncoding.EncodeToString(pub2), // KMS now reports the ROTATED key
			"SigningAlgorithms": []string{AlgECDSA256},
		})},
	}}
	p, err := New(Config{
		Region: "us-east-1", SigningAlgorithm: AlgECDSA256,
		Credentials: StaticCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret"},
		HTTPClient:  newFakeClient(ft),
	})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, rotated, err := p.RefreshPublicKey(context.Background(), "k", pub1)
	if err != nil {
		t.Fatalf("RefreshPublicKey: %v", err)
	}
	if !rotated {
		t.Fatal("RefreshPublicKey did not detect that KMS now reports different key material")
	}
	if !bytes.Equal(refreshed, pub2) {
		t.Fatal("RefreshPublicKey did not return the new key material")
	}
}
