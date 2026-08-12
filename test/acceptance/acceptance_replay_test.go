package acceptance

import (
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/verification"
)

// ===== Category 3/5 — REPLAY / TAMPER (10) ==================================

func TestAcceptance21_LifecycleCertificateTamperDetected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-1")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := lifecycle.VerifyCertificate(res.Certificate); err != nil {
		t.Fatalf("expected a freshly-issued certificate to verify clean: %v", err)
	}
	tampered := res.Certificate
	tampered.EntityID = "tampered-entity-id"
	if err := lifecycle.VerifyCertificate(tampered); err == nil {
		t.Fatalf("expected VerifyCertificate to reject a tampered LifecycleCertificate")
	}
}

func TestAcceptance22_CanonicalCertificateTamperDetected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-2")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	cert := res.Canonical.Certificate
	cert.RiskScore += 1
	if err := canonical.VerifyCertificate(cert); err == nil {
		t.Fatalf("expected canonical.VerifyCertificate to reject a tampered certificate")
	}
}

func TestAcceptance23_IVFEmptyBundleRejected(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-3")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if !res.Certificate.IVFVerified {
		t.Fatalf("expected a clean IVF verification for a genuine case")
	}
	tamperedBundle := verification.ReplayPackage{
		Evidence:      verification.EvidencePackage{Subject: "tampered-subject", Records: nil},
		ClaimedResult: "FABRICATED_RESULT",
	}
	if _, err := verification.NewBundle(tamperedBundle); err == nil {
		t.Fatalf("expected NewBundle to reject an empty-evidence bundle (ErrEmptyBundle)")
	}
}

func TestAcceptance24_FusionLedgerVerifies(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-4")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := o.Pipeline.Fusion.VerifyChain(); err != nil {
		t.Fatalf("fusion VerifyChain: %v", err)
	}
}

func TestAcceptance25_ContradictionLedgerVerifies(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-5")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := o.Pipeline.Contradiction.VerifyChain(); err != nil {
		t.Fatalf("contradiction VerifyChain: %v", err)
	}
}

func TestAcceptance26_DecisionLedgerVerifies(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-6")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := o.Pipeline.Decision.VerifyChain(); err != nil {
		t.Fatalf("decision VerifyChain: %v", err)
	}
}

func TestAcceptance27_TwinLedgerVerifies(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-7")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := o.Pipeline.Twin.VerifyChain(); err != nil {
		t.Fatalf("twin VerifyChain: %v", err)
	}
}

func TestAcceptance28_EntityLedgerVerifies(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-8")
	mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := o.Entities.VerifyChain(); err != nil {
		t.Fatalf("entity VerifyChain: %v", err)
	}
}

func TestAcceptance29_CalibrationLedgerVerifiesAfterOutcome(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-9")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if _, err := o.RecordOutcome(res, res.Canonical.Arbitration.Winner, "inspector-rep-9", 2); err != nil {
		t.Fatal(err)
	}
	if err := o.Calibration.VerifyLedger(); err != nil {
		t.Fatalf("calibration VerifyLedger: %v", err)
	}
}

func TestAcceptance30_ReplayIDMatchesIndependentRecompute(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil)
	in := baseIntent("rep-10")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if err := canonical.VerifyCertificate(res.Canonical.Certificate); err != nil {
		t.Fatalf("expected the canonical certificate behind ReplayID to independently verify: %v", err)
	}
	if res.Certificate.ReplayID != res.Canonical.Certificate.Hash {
		t.Fatalf("ReplayID diverged from the independently-verified canonical certificate hash")
	}
}
