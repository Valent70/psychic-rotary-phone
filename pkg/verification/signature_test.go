package verification

import "testing"

func TestSignAndVerifySignedCertificate(t *testing.T) {
	key, err := GenerateSigningKey()
	must(t, err)

	cert := IssueCertificate(BundleManifest{Subject: "VESSEL:1", RootHash: "abc123", ClaimedResult: "PANAMA", RecordCount: 3})
	signed := key.Sign(cert)

	if err := VerifySigned(signed); err != nil {
		t.Fatalf("expected valid signature to verify, got %v", err)
	}
}

func TestVerifySignedDetectsTamperedCertificate(t *testing.T) {
	key, err := GenerateSigningKey()
	must(t, err)
	cert := IssueCertificate(BundleManifest{Subject: "V1", RootHash: "root", ClaimedResult: "R", RecordCount: 1})
	signed := key.Sign(cert)

	signed.Certificate.ClaimedResult = "TAMPERED" // breaks CertificateHash consistency
	if err := VerifySigned(signed); err == nil {
		t.Fatalf("expected tampered certificate to fail verification")
	}
}

func TestVerifySignedDetectsWrongKey(t *testing.T) {
	key1, err := GenerateSigningKey()
	must(t, err)
	key2, err := GenerateSigningKey()
	must(t, err)

	cert := IssueCertificate(BundleManifest{Subject: "V1", RootHash: "root", ClaimedResult: "R", RecordCount: 1})
	signed := key1.Sign(cert)
	signed.PublicKey = key2.PublicKeyHex() // swap in a different, valid-format key

	if err := VerifySigned(signed); err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature for mismatched key, got %v", err)
	}
}

func TestVerifySignedRejectsMalformedKeyOrSignature(t *testing.T) {
	key, err := GenerateSigningKey()
	must(t, err)
	cert := IssueCertificate(BundleManifest{Subject: "V1", RootHash: "root", ClaimedResult: "R", RecordCount: 1})
	signed := key.Sign(cert)

	badKey := signed
	badKey.PublicKey = "not-hex!!"
	if err := VerifySigned(badKey); err == nil {
		t.Fatalf("expected error for malformed public key")
	}

	badSig := signed
	badSig.Signature = "not-hex!!"
	if err := VerifySigned(badSig); err == nil {
		t.Fatalf("expected error for malformed signature")
	}

	shortKey := signed
	shortKey.PublicKey = "abcd"
	if err := VerifySigned(shortKey); err == nil {
		t.Fatalf("expected error for wrong-length public key")
	}
}

func TestFullIVFPipelineWithSignature(t *testing.T) {
	// End-to-end: build bundle, verify, issue certificate, sign it —
	// the complete chain a regulator would receive.
	rp := ReplayPackage{Evidence: sampleEvidencePackage(), ClaimedResult: "PANAMA"}
	bundle, err := NewBundle(rp)
	must(t, err)

	v := NewVerifier()
	v.RegisterReplayFunc("vessel_flag_state", independentSum)
	result, err := v.Verify("vessel_flag_state", bundle)
	must(t, err)
	if result.Certificate == nil {
		t.Fatalf("expected certificate")
	}

	key, err := GenerateSigningKey()
	must(t, err)
	signed := key.Sign(*result.Certificate)
	if err := VerifySigned(signed); err != nil {
		t.Fatalf("expected signed certificate from full pipeline to verify: %v", err)
	}
}
