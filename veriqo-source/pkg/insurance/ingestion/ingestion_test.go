package ingestion

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/auditlink"
	"veriqo/pkg/insurance/casestate"
	"veriqo/pkg/insurance/credential"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/network"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/payment"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/platform/audit"
)

// driveToPaymentExecuted builds a real CaseLifecycle, driven through
// every ordinary casestate call (never this package's own) up to
// StatePaymentExecuted -- the precondition this pipeline requires,
// exactly as a production deployment would already have one before an
// external settlement confirmation ever arrives.
func driveToPaymentExecuted(t *testing.T, amount quantum.Amount) *casestate.CaseLifecycle {
	t.Helper()
	cl, err := casestate.New("CASE-INGEST-1")
	if err != nil {
		t.Fatalf("casestate.New: %v", err)
	}
	steps := []struct {
		to   casestate.State
		ev   string
		idem string
	}{
		{casestate.StateAccepted, "", "IDEM-A"},
		{casestate.StateEvidenceExchanged, "EVID-NOTICE", "IDEM-B"},
		{casestate.StateUnderReview, "", "IDEM-C"},
	}
	for _, s := range steps {
		if _, err := cl.Transition(s.to, "PTY-INSURED", party.RoleInsured, s.ev, s.idem, "driving to payment executed", 10); err != nil {
			t.Fatalf("Transition to %s: %v", s.to, err)
		}
	}
	if _, err := cl.Quantify(quantum.Calculation{CalculationID: "QC-INGEST-1", IndicativeClaimValue: amount}, "PTY-CLAIMS-HANDLER", party.RoleClaimsHandler, "IDEM-D", 20); err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if _, err := cl.OpenReserve("RSV-INGEST-1", "CLM-1", amount, "PTY-INSURER", party.RoleInsurer, "initial reserve", "IDEM-RESERVE", 30); err != nil {
		t.Fatalf("OpenReserve: %v", err)
	}
	if _, err := cl.AuthorizePayment("PAY-INGEST-1", "CLM-1", "PTY-PAYEE", amount, "IDEM-PAY", "PTY-CLAIMS-HANDLER", party.RoleClaimsHandler, "PTY-INSURER", party.RoleInsurer, "authorize payment", 40); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	if _, err := cl.ExecutePayment("PTY-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-INGEST-1", "IDEM-EXEC", 50); err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	if cl.State() != casestate.StatePaymentExecuted {
		t.Fatalf("driveToPaymentExecuted: expected StatePaymentExecuted, got %s", cl.State())
	}
	return cl
}

// registerEffectiveIssuer records a real, effective QualificationRecord
// for the insurer party this test's receipts are issued by, AND for
// PTY-CLAIMS-HANDLER -- the actor validEvent uses to close the case.
// Both are needed because IngestExternalSettlement attaches this same
// registry to the CaseLifecycle (FINAL INTERNAL CHECK item G's own
// credential check now applies to the pipeline's own closing
// transition too, not only to the issuer check).
func registerEffectiveIssuer(t *testing.T) *credential.Registry {
	t.Helper()
	reg := credential.NewRegistry()
	if err := reg.RecordQualification(credential.QualificationRecord{
		PartyID: "PTY-INSURER", Role: party.RoleInsurer, State: network.StateSelfAttested,
		RecordedBy: "PTY-INSURER", RecordedAtTick: 0, EffectiveAtTick: 0,
	}); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	if err := reg.RecordQualification(credential.QualificationRecord{
		PartyID: "PTY-CLAIMS-HANDLER", Role: party.RoleClaimsHandler, State: network.StateSelfAttested,
		RecordedBy: "PTY-CLAIMS-HANDLER", RecordedAtTick: 0, EffectiveAtTick: 0,
	}); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	return reg
}

// registerConfirmationEvidence builds and registers a real, content-
// addressed evidence.Record standing in for the bank confirmation
// document a real settlement confirmation would cite.
func registerConfirmationEvidence(t *testing.T) (*evidence.Registry, string) {
	t.Helper()
	raw, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeDocument, Subject: "PAY-INGEST-1", Predicate: "settlement_confirmed_by",
		Object: "PTY-INSURER", Source: "insurer claims portal", ObservedAt: 55, Confidence: 1.0,
		Attributes: map[string]string{"document_hash": "sha256:deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	rec, err := evidence.New("CASE-INGEST-1", raw, "PTY-INSURER", evidence.OriginInsurer)
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	reg := evidence.NewRegistry()
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return reg, rec.EvidenceID()
}

func validEvent(evidenceID string, amount quantum.Amount) ExternalSettlementEvent {
	return ExternalSettlementEvent{
		Receipt: network.ExchangeReceipt{
			CaseID: "CASE-INGEST-1", EvidenceContentHash: "sha256:deadbeef",
			ReceivedByPartyID: "PTY-CLAIMS-HANDLER", ReceivedAtTick: 60,
			Source: "insurer claims portal", IssuerPartyID: "PTY-INSURER",
			ReceiptReference: "PORTAL-CONF-1", VerificationStatus: network.VerificationNotPerformed,
		},
		IssuerRole: party.RoleInsurer,
		Settlement: payment.SettlementEvidence{
			PaymentID: "PAY-INGEST-1", Reference: "REF-INGEST-1", SourceDescription: "insurer claims portal confirmation",
			SettledAmount: amount, ConfirmedAtTick: 60, EvidenceID: evidenceID,
		},
		MaxAdjustment:         0,
		ClosedBy:              "PTY-CLAIMS-HANDLER",
		ClosedRole:            party.RoleClaimsHandler,
		ClosingIdempotencyKey: "IDEM-CLOSE-INGEST",
		ClosingTick:           70,
	}
}

// TestIngestExternalSettlementFullPipelineSucceeds is the Hidden P0
// proof itself: every stage the docx names -- network adapter,
// credential verification, evidence verification, canonical event,
// case state machine, SETTLED, audit ledger, replayable -- runs for
// real, in order, on one real case, ending CLOSED with settlement
// evidence recorded, mirrored into a verifiable audit chain, and
// reproducible by Replay from that chain's own recorded history alone.
func TestIngestExternalSettlementFullPipelineSucceeds(t *testing.T) {
	amount := quantum.Amount(75_000)
	cl := driveToPaymentExecuted(t, amount)
	credReg := registerEffectiveIssuer(t)
	evReg, evidenceID := registerConfirmationEvidence(t)
	store := audit.NewAuditStore()

	result, err := IngestExternalSettlement(cl, store, credReg, evReg, validEvent(evidenceID, amount))
	if err != nil {
		t.Fatalf("IngestExternalSettlement: %v", err)
	}

	if !result.ReceiptValidated {
		t.Error("expected ReceiptValidated=true -- stage 1 (network adapter) did not genuinely run")
	}
	if result.IssuerQualification.PartyID != "PTY-INSURER" {
		t.Error("expected IssuerQualification to be the real record found -- stage 2 (credential verification) did not genuinely run")
	}
	if result.VerifiedEvidence.EvidenceID() != evidenceID {
		t.Error("expected VerifiedEvidence to be the real record found -- stage 3 (evidence verification) did not genuinely run")
	}
	if !result.SettlementRecorded {
		t.Fatal("expected SettlementRecorded=true -- stage 4/5 (case state machine, SETTLED) did not genuinely run")
	}
	if cl.State() != casestate.StateClosed {
		t.Fatalf("expected the case to end CLOSED, got %s", cl.State())
	}
	if ev, settled := cl.Payment.SettlementEvidenceRecorded(); !settled || ev.SettledAmount != amount {
		t.Fatalf("expected genuine SettlementEvidence recorded on the Payment, got settled=%v amount=%s", settled, ev.SettledAmount)
	}
	if result.ClosingTransition.To != casestate.StateClosed {
		t.Error("expected ClosingTransition.To == StateClosed -- the canonical event stage did not genuinely run")
	}

	if len(result.MirroredAuditRecords) == 0 {
		t.Fatal("expected mirrored audit records -- stage 6 (audit ledger) did not genuinely run")
	}
	if len(result.CanonicalEvents) != len(result.MirroredAuditRecords) {
		t.Fatalf("expected every mirrored record to reconstruct into a canonical event, got %d records but %d events", len(result.MirroredAuditRecords), len(result.CanonicalEvents))
	}
	// Independently re-verify the ledger this pipeline produced -- proof
	// the audit stage's own guarantee holds, not merely a self-report.
	if _, err := auditlink.VerifyCanonicalAuthority(store); err != nil {
		t.Fatalf("independent VerifyCanonicalAuthority failed on the pipeline's own ledger: %v", err)
	}

	if !result.Replayed {
		t.Fatal("expected Replayed=true -- stage 7 (replayable) did not genuinely run")
	}
	replayed, err := casestate.Replay(cl.CaseID, cl.History())
	if err != nil {
		t.Fatalf("independent Replay of the pipeline's own case failed: %v", err)
	}
	if replayed.State() != casestate.StateClosed {
		t.Fatalf("expected independent replay to reproduce StateClosed, got %s", replayed.State())
	}
}

func TestIngestExternalSettlementRefusesWhenCaseNotYetPaymentExecuted(t *testing.T) {
	cl, err := casestate.New("CASE-INGEST-EARLY")
	if err != nil {
		t.Fatalf("casestate.New: %v", err)
	}
	credReg := registerEffectiveIssuer(t)
	evReg, evidenceID := registerConfirmationEvidence(t)
	store := audit.NewAuditStore()

	_, err = IngestExternalSettlement(cl, store, credReg, evReg, validEvent(evidenceID, quantum.Amount(75_000)))
	if !errors.Is(err, ErrCaseNotReadyForSettlement) {
		t.Fatalf("expected ErrCaseNotReadyForSettlement for a case still at INVITED, got %v", err)
	}
	if len(store.Snapshot()) != 0 {
		t.Fatal("expected NO audit records for a pipeline refused at its own precondition -- no partial effect")
	}
}

func TestIngestExternalSettlementRefusesAnInvalidReceipt(t *testing.T) {
	amount := quantum.Amount(75_000)
	cl := driveToPaymentExecuted(t, amount)
	credReg := registerEffectiveIssuer(t)
	evReg, evidenceID := registerConfirmationEvidence(t)
	store := audit.NewAuditStore()

	ev := validEvent(evidenceID, amount)
	ev.Receipt.Source = "" // structurally invalid receipt
	_, err := IngestExternalSettlement(cl, store, credReg, evReg, ev)
	if !errors.Is(err, ErrReceiptInvalid) {
		t.Fatalf("expected ErrReceiptInvalid, got %v", err)
	}
	if cl.State() != casestate.StatePaymentExecuted {
		t.Fatalf("expected the case to remain PAYMENT_EXECUTED after a refused receipt, got %s", cl.State())
	}
	if _, settled := cl.Payment.SettlementEvidenceRecorded(); settled {
		t.Fatal("expected NO settlement evidence recorded when the receipt itself was refused -- no partial effect")
	}
}

func TestIngestExternalSettlementRefusesARevokedIssuer(t *testing.T) {
	amount := quantum.Amount(75_000)
	cl := driveToPaymentExecuted(t, amount)
	evReg, evidenceID := registerConfirmationEvidence(t)
	store := audit.NewAuditStore()

	credReg := credential.NewRegistry()
	if err := credReg.RecordQualification(credential.QualificationRecord{
		PartyID: "PTY-INSURER", Role: party.RoleInsurer, State: network.StateSelfAttested,
		RecordedBy: "PTY-INSURER", RecordedAtTick: 0, EffectiveAtTick: 0,
	}); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	if err := credReg.RecordQualification(credential.QualificationRecord{
		PartyID: "PTY-INSURER", Role: party.RoleInsurer, State: network.StateRevoked,
		RecordedBy: "PTY-COMPLIANCE", RecordedAtTick: 55, EffectiveAtTick: 55, RevocationReason: "licence lapsed",
	}); err != nil {
		t.Fatalf("RecordQualification (revoke): %v", err)
	}

	_, err := IngestExternalSettlement(cl, store, credReg, evReg, validEvent(evidenceID, amount))
	if !errors.Is(err, ErrIssuerCredentialNotEffective) {
		t.Fatalf("expected ErrIssuerCredentialNotEffective for a revoked issuer, got %v", err)
	}
	if _, settled := cl.Payment.SettlementEvidenceRecorded(); settled {
		t.Fatal("expected NO settlement evidence recorded when the issuer's credential was revoked -- no partial effect")
	}
}

func TestIngestExternalSettlementRefusesUnverifiableEvidence(t *testing.T) {
	amount := quantum.Amount(75_000)
	cl := driveToPaymentExecuted(t, amount)
	credReg := registerEffectiveIssuer(t)
	store := audit.NewAuditStore()
	emptyEvReg := evidence.NewRegistry() // the confirmation's own evidence was never registered

	_, err := IngestExternalSettlement(cl, store, credReg, emptyEvReg, validEvent("EV-NEVER-REGISTERED", amount))
	if !errors.Is(err, ErrEvidenceNotVerified) {
		t.Fatalf("expected ErrEvidenceNotVerified for an unregistered evidence ID, got %v", err)
	}
	if _, settled := cl.Payment.SettlementEvidenceRecorded(); settled {
		t.Fatal("expected NO settlement evidence recorded when the cited evidence could not be verified -- no partial effect")
	}
}

func TestIngestExternalSettlementRefusesAMismatchedSettledAmount(t *testing.T) {
	amount := quantum.Amount(75_000)
	cl := driveToPaymentExecuted(t, amount)
	credReg := registerEffectiveIssuer(t)
	evReg, evidenceID := registerConfirmationEvidence(t)
	store := audit.NewAuditStore()

	ev := validEvent(evidenceID, amount)
	ev.Settlement.SettledAmount = amount + 1 // documented adjustment is 0 -- must be exact
	_, err := IngestExternalSettlement(cl, store, credReg, evReg, ev)
	if err == nil {
		t.Fatal("expected a settled amount outside the documented adjustment to be refused")
	}
	if cl.State() != casestate.StatePaymentExecuted {
		t.Fatalf("expected the case to remain PAYMENT_EXECUTED after a refused settlement amount, got %s", cl.State())
	}
}
