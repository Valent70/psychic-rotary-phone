package awskms

// TestAWSKMSIntegration performs REAL calls over the real network
// against a real, already-provisioned AWS KMS asymmetric SIGN_VERIFY
// key, using real IAM credentials. It proves what the offline tests in
// awskms_test.go cannot: that this package's hand-rolled SigV4 signing
// actually authenticates against genuine AWS KMS, and that a real key
// actually signs and its signature actually verifies.
//
// It is gated behind an explicit opt-in and SKIPS (never fails) when
// unset, so `go test ./...` in a sandbox with no AWS credentials and
// no network access -- exactly this repository's default CI/dev
// environment -- passes cleanly:
//
//	VERIQO_AWS_KMS_INTEGRATION_TEST=1     required to run at all
//	AWS_ACCESS_KEY_ID                     required
//	AWS_SECRET_ACCESS_KEY                 required
//	AWS_SESSION_TOKEN                     optional (STS temporary creds)
//	VERIQO_AWS_KMS_TEST_REGION            required, e.g. "us-east-1"
//	VERIQO_AWS_KMS_TEST_KEY_ID            required: a real KMS key ID/ARN,
//	                                       asymmetric, KeyUsage SIGN_VERIFY,
//	                                       Enabled
//	VERIQO_AWS_KMS_TEST_ALGORITHM         required: one of that key's REAL
//	                                       SigningAlgorithms, e.g.
//	                                       "ECDSA_SHA_256" or "EDDSA" --
//	                                       whichever the key actually
//	                                       supports; this test does not
//	                                       assume Ed25519 or anything else
//
// A CI environment with a procured KMS tenancy (see
// docs/governance/HSM_KMS_POLICY.md §5) sets all of the above to opt in
// for real qualification evidence; this sandbox does not, so this test
// is expected to SKIP here.

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"os"
	"strings"
	"testing"
)

func digestForAlgorithm(t *testing.T, algorithm string, msg []byte) []byte {
	t.Helper()
	switch {
	case algorithm == AlgEDDSA:
		// KMS's EDDSA signing takes the raw message, not a pre-hashed
		// digest -- Ed25519 hashes internally. Every other algorithm
		// here is asked for MessageType DIGEST.
		return msg
	case strings.HasSuffix(algorithm, "_256"):
		d := sha256.Sum256(msg)
		return d[:]
	case strings.HasSuffix(algorithm, "_384"):
		d := sha512.Sum384(msg)
		return d[:]
	case strings.HasSuffix(algorithm, "_512"):
		d := sha512.Sum512(msg)
		return d[:]
	default:
		t.Fatalf("digestForAlgorithm: don't know how to hash for algorithm %q", algorithm)
		return nil
	}
}

func TestAWSKMSIntegration(t *testing.T) {
	if os.Getenv("VERIQO_AWS_KMS_INTEGRATION_TEST") != "1" {
		t.Skip("VERIQO_AWS_KMS_INTEGRATION_TEST != 1: skipping real AWS KMS integration test (see this file's doc comment for the required gate and env vars)")
	}
	region := os.Getenv("VERIQO_AWS_KMS_TEST_REGION")
	keyID := os.Getenv("VERIQO_AWS_KMS_TEST_KEY_ID")
	algorithm := os.Getenv("VERIQO_AWS_KMS_TEST_ALGORITHM")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if region == "" || keyID == "" || algorithm == "" || accessKey == "" || secretKey == "" {
		t.Fatal("VERIQO_AWS_KMS_INTEGRATION_TEST=1 but one or more required env vars are unset: " +
			"VERIQO_AWS_KMS_TEST_REGION, VERIQO_AWS_KMS_TEST_KEY_ID, VERIQO_AWS_KMS_TEST_ALGORITHM, " +
			"AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY -- see this file's doc comment")
	}

	creds := StaticCredentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
	}
	provider, err := New(Config{Region: region, SigningAlgorithm: algorithm, Credentials: creds})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// Key-state verification: read the algorithm and enabled state OFF
	// THE REAL KEY, never assumed.
	state, err := provider.DescribeKey(ctx, keyID)
	if err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}
	if !state.Enabled {
		t.Fatalf("configured KMS integration test key is not Enabled: state=%s", state.State)
	}
	found := false
	for _, a := range state.SigningAlgorithms {
		if a == algorithm {
			found = true
		}
	}
	if !found {
		t.Fatalf("configured VERIQO_AWS_KMS_TEST_ALGORITHM=%q is not in this key's real SigningAlgorithms %v -- "+
			"this test never assumes an algorithm the key doesn't actually advertise", algorithm, state.SigningAlgorithms)
	}

	// Public key retrieval.
	pub, err := provider.PublicKey(ctx, keyID)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if len(pub) == 0 {
		t.Fatal("PublicKey returned no bytes")
	}

	// Signing + independent verification (independent of the KMS
	// response: verified locally with Go's own stdlib crypto against
	// the fetched public key, not by trusting KMS's HTTP 200).
	msg := []byte("veriqo awskms integration test " + keyID)
	d := digestForAlgorithm(t, algorithm, msg)
	sig, err := provider.Sign(ctx, keyID, d)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := VerifyIndependently(pub, algorithm, d, sig); err != nil {
		t.Fatalf("independent verification of a real KMS signature failed: %v", err)
	}

	// Wrong-key / tamper rejection against a REAL signature: a
	// different digest must not verify under the same signature.
	otherDigest := digestForAlgorithm(t, algorithm, []byte("a different message"))
	if err := VerifyIndependently(pub, algorithm, otherDigest, sig); err == nil {
		t.Fatal("a real KMS signature verified against a different digest -- wrong-key/tamper rejection failed")
	}

	// Rotation-detection plumbing: RefreshPublicKey against the
	// CURRENT public key must report no rotation (this test does not
	// attempt to trigger a real KMS rotation, which is provider-side
	// and not synchronous -- it only proves the comparison mechanism
	// itself works against the real endpoint).
	_, rotated, err := provider.RefreshPublicKey(ctx, keyID, pub)
	if err != nil {
		t.Fatalf("RefreshPublicKey: %v", err)
	}
	if rotated {
		t.Fatal("RefreshPublicKey reported rotation against the key's own just-fetched public key")
	}
}
