package hsmkms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fixedClock returns a deterministic time.Time so signature tests are
// reproducible across runs.
func fixedClock() time.Time {
	return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
}

func testProvider() *AWSKMSProvider {
	return &AWSKMSProvider{
		Region:           "us-east-1",
		AccessKeyID:      "AKIAEXAMPLE",
		SecretAccessKey:  "supersecretkeymaterial1234567890",
		Endpoint:         "https://kms.us-east-1.amazonaws.com",
		SigningAlgorithm: "ECDSA_SHA_256",
		now:              fixedClock,
	}
}

func signedAuthHeader(t *testing.T, p *AWSKMSProvider, body []byte) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.endpoint()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "TrentService.Sign")
	if err := p.signV4(req, body, p.clock()); err != nil {
		t.Fatalf("signV4: %v", err)
	}
	return req.Header.Get("Authorization")
}

// TestSignV4SameInputsSameSignature proves the signer is deterministic:
// identical provider config, request shape, body and timestamp must
// always produce the byte-identical Authorization header.
func TestSignV4SameInputsSameSignature(t *testing.T) {
	p := testProvider()
	body := []byte(`{"KeyId":"alias/test","Message":"ZGlnZXN0","MessageType":"DIGEST","SigningAlgorithm":"ECDSA_SHA_256"}`)
	a1 := signedAuthHeader(t, p, body)
	a2 := signedAuthHeader(t, p, body)
	if a1 != a2 {
		t.Fatalf("expected identical Authorization headers for identical inputs, got:\n%s\nvs\n%s", a1, a2)
	}
	if !strings.HasPrefix(a1, "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20260820/us-east-1/kms/aws4_request") {
		t.Fatalf("unexpected Authorization header shape: %s", a1)
	}
}

// TestSignV4DifferentSecretDifferentSignature proves the signature
// actually depends on the secret key, not just the visible request
// shape -- the whole point of SigV4 authentication.
func TestSignV4DifferentSecretDifferentSignature(t *testing.T) {
	body := []byte(`{"KeyId":"alias/test"}`)
	p1 := testProvider()
	p2 := testProvider()
	p2.SecretAccessKey = "a-completely-different-secret-key"

	a1 := signedAuthHeader(t, p1, body)
	a2 := signedAuthHeader(t, p2, body)
	if a1 == a2 {
		t.Fatal("expected different secret keys to produce different signatures")
	}
}

// TestSignV4TamperingChangesSignature proves the signature covers the
// request body: changing so much as one byte of the payload after
// signing must invalidate the previously-computed signature.
func TestSignV4TamperingChangesSignature(t *testing.T) {
	p := testProvider()
	original := []byte(`{"KeyId":"alias/test","Message":"ZGlnZXN0"}`)
	tampered := []byte(`{"KeyId":"alias/test","Message":"VEFNUEVSRUQ="}`)

	a1 := signedAuthHeader(t, p, original)
	a2 := signedAuthHeader(t, p, tampered)
	if a1 == a2 {
		t.Fatal("expected tampering the request body to change the signature")
	}
}

// TestDeriveSigningKeyDeterministic proves the AWS4-HMAC-SHA256 key
// derivation chain (secret -> date -> region -> service -> aws4_request)
// is a pure function of its inputs.
func TestDeriveSigningKeyDeterministic(t *testing.T) {
	k1 := deriveSigningKey("secret-key-material", "20260820", "us-east-1", "kms")
	k2 := deriveSigningKey("secret-key-material", "20260820", "us-east-1", "kms")
	if !hmac.Equal(k1, k2) {
		t.Fatal("expected deriveSigningKey to be deterministic for identical inputs")
	}
	if len(k1) != sha256.Size {
		t.Fatalf("expected a 32-byte HMAC-SHA256 signing key, got %d bytes", len(k1))
	}
}

// TestDeriveSigningKeyVariesByEveryComponent proves each step of the
// derivation chain actually depends on its input -- a bug that dropped
// the region or service from the chain (signing every request the same
// way regardless of where it was headed) would defeat SigV4's purpose
// but wouldn't be caught by a same-input/different-input test on the
// secret alone.
func TestDeriveSigningKeyVariesByEveryComponent(t *testing.T) {
	base := deriveSigningKey("secret", "20260820", "us-east-1", "kms")
	variants := map[string][]byte{
		"secret":  deriveSigningKey("different-secret", "20260820", "us-east-1", "kms"),
		"date":    deriveSigningKey("secret", "20260821", "us-east-1", "kms"),
		"region":  deriveSigningKey("secret", "20260820", "us-west-2", "kms"),
		"service": deriveSigningKey("secret", "20260820", "us-east-1", "s3"),
	}
	for name, variant := range variants {
		if hmac.Equal(base, variant) {
			t.Errorf("changing %s did not change the derived signing key", name)
		}
	}
}

// TestUriEncodeComponentPreservesUnreservedCharacters proves the
// path/query encoder follows SigV4's unreserved-character rule: RFC
// 3986 unreserved characters pass through unescaped, everything else is
// percent-encoded with uppercase hex.
func TestUriEncodeComponentPreservesUnreservedCharacters(t *testing.T) {
	got := uriEncodeComponent("alias/test key+plus")
	want := "alias%2Ftest%20key%2Bplus"
	if got != want {
		t.Fatalf("uriEncodeComponent: got %q, want %q", got, want)
	}
	if got := uriEncodeComponent("ABCabc012-_.~"); got != "ABCabc012-_.~" {
		t.Fatalf("expected unreserved characters untouched, got %q", got)
	}
}

// TestAWSKMSProviderMode confirms AWSKMSProvider self-reports as REAL,
// which is what RefuseRealProvider keys off of.
func TestAWSKMSProviderMode(t *testing.T) {
	p := &AWSKMSProvider{}
	if p.Mode() != "REAL" {
		t.Fatalf("expected Mode() == REAL, got %q", p.Mode())
	}
}

// TestRefuseRealProviderRejectsAWSKMSProvider is the invariant
// AWSKMSProvider exists to be checked against: it must never silently
// pass through a guard meant to keep REAL-mode providers out of a
// fixture qualification path.
func TestRefuseRealProviderRejectsAWSKMSProvider(t *testing.T) {
	if err := RefuseRealProvider(&AWSKMSProvider{}); err == nil {
		t.Fatal("expected RefuseRealProvider to reject an AWSKMSProvider")
	}
}

func TestRefuseRealProviderAcceptsFixtureProviders(t *testing.T) {
	for name, p := range map[string]interface {
		Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error)
		PublicKey(ctx context.Context, keyID string) ([]byte, error)
	}{
		"local_test": NewLocalTestKeyProvider(),
	} {
		if err := RefuseRealProvider(p); err != nil {
			t.Errorf("%s: expected RefuseRealProvider to accept a non-REAL provider, got %v", name, err)
		}
	}
}

// --- httptest round-trip: proves the full request/response plumbing --

// fakeKMSHandler mimics just enough of AWS KMS's JSON protocol to prove
// AWSKMSProvider builds a well-formed, correctly-headered signed
// request and correctly decodes the JSON/base64 response shape KMS
// actually returns. It does NOT validate against real AWS -- see the
// package doc comment for what is and is not proven here.
func fakeKMSHandler(t *testing.T, wantAccessKeyID string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+wantAccessKeyID+"/") {
			t.Errorf("unexpected or missing Authorization header: %q", auth)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-amz-json-1.1" {
			t.Errorf("unexpected Content-Type: %q", ct)
		}
		target := r.Header.Get("X-Amz-Target")
		switch target {
		case "TrentService.Sign":
			var req kmsSignRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode Sign request: %v", err)
			}
			if req.MessageType != "DIGEST" {
				t.Errorf("expected MessageType=DIGEST, got %q", req.MessageType)
			}
			resp := kmsSignResponse{
				KeyId:            req.KeyId,
				Signature:        base64.StdEncoding.EncodeToString([]byte("fixture-signature-bytes")),
				SigningAlgorithm: req.SigningAlgorithm,
			}
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_ = json.NewEncoder(w).Encode(resp)
		case "TrentService.GetPublicKey":
			var req kmsGetPublicKeyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode GetPublicKey request: %v", err)
			}
			resp := kmsGetPublicKeyResponse{
				KeyId:     req.KeyId,
				PublicKey: base64.StdEncoding.EncodeToString([]byte("fixture-der-spki-public-key")),
			}
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected X-Amz-Target: %q", target)
			w.WriteHeader(http.StatusBadRequest)
		}
	}
}

func TestAWSKMSProviderSignRoundTripsAgainstFakeServer(t *testing.T) {
	srv := httptest.NewServer(fakeKMSHandler(t, "AKIAEXAMPLE"))
	defer srv.Close()

	p := testProvider()
	p.Endpoint = srv.URL
	p.HTTPClient = srv.Client()

	sig, err := p.Sign(context.Background(), "alias/test-key", []byte("a-real-looking-digest"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if string(sig) != "fixture-signature-bytes" {
		t.Fatalf("unexpected signature bytes: %q", sig)
	}
}

func TestAWSKMSProviderPublicKeyRoundTripsAgainstFakeServer(t *testing.T) {
	srv := httptest.NewServer(fakeKMSHandler(t, "AKIAEXAMPLE"))
	defer srv.Close()

	p := testProvider()
	p.Endpoint = srv.URL
	p.HTTPClient = srv.Client()

	pub, err := p.PublicKey(context.Background(), "alias/test-key")
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if string(pub) != "fixture-der-spki-public-key" {
		t.Fatalf("unexpected public key bytes: %q", pub)
	}
}

func TestAWSKMSProviderSignRequiresSigningAlgorithm(t *testing.T) {
	p := testProvider()
	p.SigningAlgorithm = ""
	if _, err := p.Sign(context.Background(), "alias/test-key", []byte("digest")); err == nil {
		t.Fatal("expected Sign to fail without a SigningAlgorithm set")
	}
}

func TestAWSKMSProviderRejectsNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"__type":"AccessDeniedException","message":"fixture denial"}`))
	}))
	defer srv.Close()

	p := testProvider()
	p.Endpoint = srv.URL
	p.HTTPClient = srv.Client()

	if _, err := p.Sign(context.Background(), "alias/test-key", []byte("digest")); err == nil {
		t.Fatal("expected Sign to fail closed on a non-200 KMS response")
	}
}
