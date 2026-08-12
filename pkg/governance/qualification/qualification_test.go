package qualification

import (
	"errors"
	"testing"
)

func goodEvidence(gate string) ExternalEvidence {
	e := ExternalEvidence{
		GateID: gate, Provider: "Acme Security Labs", ReportID: "ASL-2026-0442",
		Scope: "REST, gRPC, plugin boundary, storage", Environment: "staging-mirror",
		Commit:      "abc123",
		Measurement: map[string]string{"critical_findings": "0", "high_findings": "0"},
		StartTick:   100, EndTick: 200,
		Reviewer: "ciso@example.com", Signature: "sig-real-not-fabricated",
		ExpiresAtTick: 100000, SubmittedAtTick: 250,
	}
	return NewEvidence(e)
}

func TestBlockedGateCannotBeSatisfiedWithoutEvidence(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterBlocked("pentest", "independent pentest", "needs external vendor"); err != nil {
		t.Fatal(err)
	}
	rec, ok := r.Get("pentest")
	if !ok {
		t.Fatal("expected gate to be registered")
	}
	if rec.Status != StatusBlockedExternal {
		t.Fatalf("a freshly registered gate must be BLOCKED_EXTERNAL, got %s", rec.Status)
	}
	if rec.Satisfied(0) {
		t.Fatal("a blocked gate with no evidence must never be Satisfied")
	}
}

func TestDuplicateRegistrationRefused(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("pentest", "d", "b")
	if err := r.RegisterBlocked("pentest", "d", "b"); !errors.Is(err, ErrDuplicateGate) {
		t.Fatalf("expected ErrDuplicateGate, got %v", err)
	}
}

func TestFullLifecycleReachesVerified(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("supply_chain_scan", "scanners in CI", "needs network egress")
	ev := goodEvidence("supply_chain_scan")
	if err := r.SubmitEvidence("supply_chain_scan", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("supply_chain_scan", 300); err != nil {
		t.Fatal(err)
	}
	if err := r.Qualify("supply_chain_scan", 300); err != nil {
		t.Fatal(err)
	}
	rec, _ := r.Get("supply_chain_scan")
	if !rec.Satisfied(300) {
		t.Fatal("a qualified gate with unexpired evidence must be Satisfied")
	}
	if err := r.VerifyGate("supply_chain_scan", 300); err != nil {
		t.Fatal(err)
	}
	rec, _ = r.Get("supply_chain_scan")
	if rec.Status != StatusVerified {
		t.Fatalf("expected VERIFIED, got %s", rec.Status)
	}
	if len(rec.History) != 4 {
		t.Fatalf("expected 4 recorded transitions (submit, validate, qualify, verify), got %d: %+v",
			len(rec.History), rec.History)
	}
}

func TestTamperedArtifactHashRejected(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("pentest", "d", "b")
	ev := goodEvidence("pentest")
	ev.Measurement["critical_findings"] = "1" // mutate content after hashing
	if err := r.SubmitEvidence("pentest", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("pentest", 300); !errors.Is(err, ErrArtifactHashInvalid) {
		t.Fatalf("expected ErrArtifactHashInvalid, got %v", err)
	}
	rec, _ := r.Get("pentest")
	if rec.Status != StatusEvidenceRejected {
		t.Fatalf("tampered evidence must land in EVIDENCE_REJECTED, got %s", rec.Status)
	}
}

func TestIncompleteEvidenceRejected(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("hsm_kms", "d", "b")
	ev := NewEvidence(ExternalEvidence{GateID: "hsm_kms", Provider: "CloudKMS Inc"})
	if err := r.SubmitEvidence("hsm_kms", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("hsm_kms", 1); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("expected ErrEvidenceIncomplete, got %v", err)
	}
}

func TestUnreviewedEvidenceCannotQualify(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("spire_mtls", "d", "b")
	ev := goodEvidence("spire_mtls")
	ev.Reviewer = ""
	ev.ArtifactHash = computeArtifactHash(ev) // hash doesn't cover reviewer/signature by design
	if err := r.SubmitEvidence("spire_mtls", ev); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("spire_mtls", 300); !errors.Is(err, ErrNotReviewed) {
		t.Fatalf("expected ErrNotReviewed, got %v", err)
	}
}

func TestExpiredEvidenceCannotBeVerified(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("soak_72h", "d", "b")
	ev := goodEvidence("soak_72h")
	ev.ExpiresAtTick = 400
	ev.ArtifactHash = computeArtifactHash(ev)
	_ = r.SubmitEvidence("soak_72h", ev)
	_ = r.Validate("soak_72h", 300)
	_ = r.Qualify("soak_72h", 300)
	// Time passes beyond expiry before verification is attempted.
	if err := r.VerifyGate("soak_72h", 500); !errors.Is(err, ErrAlreadyExpired) {
		t.Fatalf("expected ErrAlreadyExpired, got %v", err)
	}
	rec, _ := r.Get("soak_72h")
	if rec.Status != StatusExpired {
		t.Fatalf("expected EXPIRED, got %s", rec.Status)
	}
	if rec.Satisfied(500) {
		t.Fatal("expired evidence must never be Satisfied")
	}
}

func TestExpireStaleDemotesLapsedQualifications(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("scale_qualification", "d", "b")
	ev := goodEvidence("scale_qualification")
	ev.ExpiresAtTick = 400
	ev.ArtifactHash = computeArtifactHash(ev)
	_ = r.SubmitEvidence("scale_qualification", ev)
	_ = r.Validate("scale_qualification", 300)
	_ = r.Qualify("scale_qualification", 300)
	r.ExpireStale(500)
	rec, _ := r.Get("scale_qualification")
	if rec.Status != StatusExpired {
		t.Fatalf("expected ExpireStale to demote a lapsed QUALIFIED record, got %s", rec.Status)
	}
}

func TestRejectedEvidenceCanBeResubmitted(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("multi_region_dr", "d", "b")
	bad := NewEvidence(ExternalEvidence{GateID: "multi_region_dr", Provider: "x"})
	_ = r.SubmitEvidence("multi_region_dr", bad)
	if err := r.Validate("multi_region_dr", 1); err == nil {
		t.Fatal("expected the incomplete submission to be rejected")
	}
	good := goodEvidence("multi_region_dr")
	if err := r.SubmitEvidence("multi_region_dr", good); err != nil {
		t.Fatalf("resubmission after rejection must be allowed: %v", err)
	}
	if err := r.Validate("multi_region_dr", 300); err != nil {
		t.Fatal(err)
	}
}

func TestCannotSubmitEvidenceMidValidation(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterBlocked("live_data", "d", "b")
	_ = r.SubmitEvidence("live_data", goodEvidence("live_data"))
	_ = r.Validate("live_data", 300)
	// Now EVIDENCE_VALIDATED; submitting again mid-lifecycle is illegal —
	// evidence must be qualified/verified or rejected/expired first.
	if err := r.SubmitEvidence("live_data", goodEvidence("live_data")); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("expected ErrIllegalTransition, got %v", err)
	}
}

func TestUnknownGateOperationsFail(t *testing.T) {
	r := NewRegistry()
	if err := r.SubmitEvidence("nonexistent", goodEvidence("nonexistent")); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
	if err := r.Validate("nonexistent", 1); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
	if err := r.Qualify("nonexistent", 1); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
	if err := r.VerifyGate("nonexistent", 1); !errors.Is(err, ErrUnknownGate) {
		t.Fatalf("expected ErrUnknownGate, got %v", err)
	}
}
