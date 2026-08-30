package commercialapi

import (
	"errors"
	"testing"

	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/audit"
)

func mustSubmitEvidence(t *testing.T, s *Store, tenantID, caseID, evidenceID string, tick uint64) evidencefabric.EvidenceRecord {
	t.Helper()
	rec, err := s.SubmitEvidence(EvidenceInput{
		TenantID: tenantID, CaseID: caseID, EvidenceID: evidenceID,
		SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		URI:    "evidence://api-survey.pdf", Filename: "api-survey.pdf", MediaType: "application/pdf",
		ByteSize: 4096, Collector: "surveyor-api", Source: "independent-surveyor",
		Domain: evidencefabric.DomainMetadata{Insurance: &evidencefabric.InsuranceMetadata{ClaimID: "CLM-1", PolicyID: "POL-1", EvidenceKind: "SURVEY"}},
		Tick:   tick,
	})
	if err != nil {
		t.Fatalf("SubmitEvidence: %v", err)
	}
	return rec
}

func decideInput(tenantID, caseID string) DecideInput {
	return DecideInput{
		TenantID: tenantID, CaseID: caseID,
		Hypothesis:            causation.Hypothesis{ID: "H1", Description: "water ingress during transit"},
		SupportingEvidenceIDs: []string{"EV-API-1"},
		FindingID:             "finding-api-1",
		Finding: cre.FindingInput{
			CaseID: caseID, ContractBasis: "clause-1", ObligationRef: "obl-1",
			EventRef: "event-1", QuantumRef: "calc-1", HumanReviewRequired: true,
		},
		Outcome:     decision.OutcomeApproved,
		Rationale:   "primary hypothesis substantiated by grounded, finalized evidence",
		LedgerActor: "commercialapi-test-decision",
		Tick:        10,
	}
}

func actionInput(tenantID, caseID string) ActionInput {
	return ActionInput{
		TenantID: tenantID, CaseID: caseID, Actor: "adjuster-api-1", PolicyRef: "policy-settlement-v1", Scope: caseID,
		PermittedAction: action.ActionApproveSettlement, Conditions: []string{"reinspection_complete"},
		AuthorizedAt: 10, ExpiresAt: 20, ExecutingActor: "adjuster-api-1", ExecutionAt: 15,
		LedgerActor: "commercialapi-test-action",
	}
}

func TestFullCaseLifecycleThroughTheStore(t *testing.T) {
	s := NewStore()
	const tenant = "tenant-A"
	const caseID = "CASE-API-1"

	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, "EV-API-1", 10)

	view, err := s.GetCase(tenant, caseID)
	if err != nil {
		t.Fatalf("GetCase: %v", err)
	}
	if len(view.EvidenceIDs) != 1 || view.Decided {
		t.Fatalf("unexpected case view before decision: %+v", view)
	}

	verified, err := s.VerifyEvidence(tenant, "EV-API-1")
	if err != nil {
		t.Fatalf("VerifyEvidence: %v", err)
	}
	if !verified {
		t.Fatal("expected real, freshly-submitted evidence to verify")
	}

	custody, err := s.GetCustody(tenant, "EV-API-1")
	if err != nil {
		t.Fatalf("GetCustody: %v", err)
	}
	if len(custody) != 3 {
		t.Fatalf("expected 3 custody events, got %d", len(custody))
	}

	d, err := s.DecideCase(decideInput(tenant, caseID))
	if err != nil {
		t.Fatalf("DecideCase: %v", err)
	}
	if d.IsZero() {
		t.Fatal("expected a populated Decision")
	}

	aa, receipt, err := s.ActOnCase(actionInput(tenant, caseID))
	if err != nil {
		t.Fatalf("ActOnCase: %v", err)
	}
	if aa.IsZero() || receipt.ReceiptID == "" {
		t.Fatal("expected a populated ActionAuthorization and Receipt")
	}

	dos, err := s.GenerateDossier(tenant, caseID)
	if err != nil {
		t.Fatalf("GenerateDossier: %v", err)
	}
	if dos.PackageHash == "" {
		t.Fatal("expected a populated dossier")
	}

	outPath := t.TempDir() + "/package.zip"
	if _, err := s.WriteDossierPackage(tenant, caseID, outPath); err != nil {
		t.Fatalf("WriteDossierPackage: %v", err)
	}

	replay, err := s.Replay(tenant, caseID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replay.Converged {
		t.Fatalf("expected replay to converge: %+v", replay)
	}
	if replay.OriginalDecisionHash != d.Hash() {
		t.Fatal("expected the replay's OriginalDecisionHash to match the real Decision hash")
	}

	view, err = s.GetCase(tenant, caseID)
	if err != nil {
		t.Fatalf("GetCase (after decide/act): %v", err)
	}
	if !view.Decided || !view.ActedOn || view.Outcome != "APPROVED" {
		t.Fatalf("unexpected case view after decision+action: %+v", view)
	}

	recs := s.Ledger().Snapshot()
	if len(recs) != 3 {
		t.Fatalf("expected 3 ledger records, got %d", len(recs))
	}
	if err := (audit.Auditor{}).VerifyChain(recs); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestGenerateDossierPopulatesRealCorroborationAndContradictions
// proves the dossier's Corroboration/Contradictions rows are derived
// from the real, independently-grounded Finding.SupportedBy/
// ContradictedBy evidence citations -- not left empty the way
// dossier.New's own zero-Corroboration default would otherwise read,
// which would misleadingly suggest no contradiction was ever
// considered when one plainly was.
func TestGenerateDossierPopulatesRealCorroborationAndContradictions(t *testing.T) {
	s := NewStore()
	const tenant = "tenant-A"
	const caseID = "CASE-API-CONTRADICT-1"

	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, "EV-SUPPORT-1", 10)
	mustSubmitEvidence(t, s, tenant, caseID, "EV-CONTRADICT-1", 10)

	di := decideInput(tenant, caseID)
	di.SupportingEvidenceIDs = []string{"EV-SUPPORT-1"}
	di.ContradictingEvidenceIDs = []string{"EV-CONTRADICT-1"}
	if _, err := s.DecideCase(di); err != nil {
		t.Fatalf("DecideCase: %v", err)
	}
	if _, _, err := s.ActOnCase(actionInput(tenant, caseID)); err != nil {
		t.Fatalf("ActOnCase: %v", err)
	}

	dos, err := s.GenerateDossier(tenant, caseID)
	if err != nil {
		t.Fatalf("GenerateDossier: %v", err)
	}
	if len(dos.Corroboration) != 1 || dos.Corroboration[0] != "EV-SUPPORT-1 supports hypothesis H1" {
		t.Fatalf("expected a real Corroboration row citing EV-SUPPORT-1, got %+v", dos.Corroboration)
	}
	if len(dos.Contradictions) != 1 || dos.Contradictions[0] != "EV-CONTRADICT-1 contradicts hypothesis H1" {
		t.Fatalf("expected a real Contradictions row citing EV-CONTRADICT-1, got %+v", dos.Contradictions)
	}
}

// TestMetricsReflectRealActivity proves pkg/commercial/telemetry's
// counters are wired to real Store branches, not decorative: a
// successful evidence submission increments evidence_ingestion_total, a
// successful decision records a real (non-negative) latency sample, and
// a deliberately-invalid action authorization (missing PolicyRef)
// increments authorization_denials -- the exact failure category item
// 20 names.
func TestMetricsReflectRealActivity(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-A", "CASE-API-METRICS-1"

	before := s.Metrics().Snapshot()

	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, "EV-API-1", 10)
	if _, err := s.DecideCase(decideInput(tenant, caseID)); err != nil {
		t.Fatalf("DecideCase: %v", err)
	}

	badAction := actionInput(tenant, caseID)
	badAction.PolicyRef = ""
	if _, _, err := s.ActOnCase(badAction); err == nil {
		t.Fatal("expected ActOnCase to refuse an action with an empty PolicyRef")
	}

	after := s.Metrics().Snapshot()
	if after.EvidenceIngestionTotal != before.EvidenceIngestionTotal+1 {
		t.Fatalf("expected evidence_ingestion_total to increment by 1, got before=%d after=%d",
			before.EvidenceIngestionTotal, after.EvidenceIngestionTotal)
	}
	if after.DecisionCount != before.DecisionCount+1 {
		t.Fatalf("expected decision_count to increment by 1, got before=%d after=%d",
			before.DecisionCount, after.DecisionCount)
	}
	if after.AuthorizationDenials != before.AuthorizationDenials+1 {
		t.Fatalf("expected authorization_denials to increment by 1, got before=%d after=%d",
			before.AuthorizationDenials, after.AuthorizationDenials)
	}
}

func TestDecideCaseRejectsUnknownCase(t *testing.T) {
	s := NewStore()
	if _, err := s.DecideCase(decideInput("tenant-A", "CASE-DOES-NOT-EXIST")); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("expected ErrCaseNotFound, got %v", err)
	}
}

func TestDecideCaseRefusesBeingCalledTwice(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-A", "CASE-API-TWICE-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatal(err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, "EV-API-1", 10)
	if _, err := s.DecideCase(decideInput(tenant, caseID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideCase(decideInput(tenant, caseID)); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("expected ErrAlreadyDecided, got %v", err)
	}
}

func TestActOnCaseRefusesBeforeDecision(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-A", "CASE-API-NODECISION-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ActOnCase(actionInput(tenant, caseID)); !errors.Is(err, ErrNotYetDecided) {
		t.Fatalf("expected ErrNotYetDecided, got %v", err)
	}
}

func TestDecideCaseRejectsUngroundedEvidence(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-A", "CASE-API-UNGROUNDED-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatal(err)
	}
	// Deliberately never submitted -- EV-API-1 has no manifest.
	if _, err := s.DecideCase(decideInput(tenant, caseID)); err == nil {
		t.Fatal("expected DecideCase to refuse citing evidence that was never submitted")
	}
}

// isolationRefused reports whether err is one of the two shapes a
// cross-tenant lookup is allowed to fail with: ErrTenantMismatch (a
// resource found but owned by someone else) or ErrCaseNotFound/
// ErrEvidenceNotFound (the resource simply does not exist in the
// CALLER's own tenant namespace at all -- see the doc comment below on
// why this Store's key design makes this the actual path every one of
// these checks takes, and why that is a STRONGER isolation guarantee,
// not a weaker one).
func isolationRefused(err error) bool {
	return errors.Is(err, ErrTenantMismatch) || errors.Is(err, ErrCaseNotFound) || errors.Is(err, ErrEvidenceNotFound)
}

// TestTenantAIsolationFromTenantB is the reviewer's own explicit
// mandatory security gate (item 19): "Tenant A cannot read Tenant B,
// Tenant A cannot modify Tenant B, Tenant A cannot replay Tenant B
// authorization."
//
// Every Store method keys its internal maps by tenantID+"/"+ID (see
// caseKey/evidenceKey) -- so a cross-tenant lookup never even reaches
// the "found the record, now check its owner" branch (ErrTenantMismatch);
// it fails one step earlier, as ErrCaseNotFound/ErrEvidenceNotFound,
// because tenant A's own namespace genuinely has no such record. This
// is STRUCTURAL isolation (the wrong tenant's ID cannot even be used to
// locate the record) rather than a runtime ownership check a future
// change could accidentally skip -- the identical "impossible by
// construction beats a check that could be forgotten" discipline this
// engagement's sealed types (Decision, ActionAuthorization) already use
// one layer down. ErrTenantMismatch remains as defense in depth for any
// future lookup path that resolves a record before checking its tenant.
func TestTenantAIsolationFromTenantB(t *testing.T) {
	s := NewStore()
	const tenantA, tenantB = "tenant-A", "tenant-B"
	const caseID = "CASE-SHARED-ID-1" // deliberately the SAME case ID, different tenants

	if err := s.CreateCase(tenantB, caseID, 0); err != nil {
		t.Fatalf("CreateCase(tenantB): %v", err)
	}
	mustSubmitEvidence(t, s, tenantB, caseID, "EV-API-1", 10)
	dB, err := s.DecideCase(decideInput(tenantB, caseID))
	if err != nil {
		t.Fatalf("DecideCase(tenantB): %v", err)
	}
	if _, _, err := s.ActOnCase(actionInput(tenantB, caseID)); err != nil {
		t.Fatalf("ActOnCase(tenantB): %v", err)
	}

	t.Run("cannot_read_case", func(t *testing.T) {
		if _, err := s.GetCase(tenantA, caseID); !isolationRefused(err) {
			t.Fatalf("expected a refusal reading tenant B's case as tenant A, got %v", err)
		}
	})

	t.Run("cannot_read_evidence", func(t *testing.T) {
		if _, err := s.GetEvidence(tenantA, "EV-API-1"); !isolationRefused(err) {
			t.Fatalf("expected a refusal reading tenant B's evidence as tenant A, got %v", err)
		}
	})

	t.Run("cannot_verify_evidence", func(t *testing.T) {
		if _, err := s.VerifyEvidence(tenantA, "EV-API-1"); !isolationRefused(err) {
			t.Fatalf("expected a refusal verifying tenant B's evidence as tenant A, got %v", err)
		}
	})

	t.Run("cannot_read_custody", func(t *testing.T) {
		if _, err := s.GetCustody(tenantA, "EV-API-1"); !isolationRefused(err) {
			t.Fatalf("expected a refusal reading tenant B's custody chain as tenant A, got %v", err)
		}
	})

	t.Run("cannot_modify_via_decide", func(t *testing.T) {
		// Tenant A attempts to decide TENANT B's case (same CaseID
		// string, different tenant) -- must be refused as not found
		// (from tenant A's own namespace) or mismatched, never silently
		// operate on tenant B's real case.
		if _, err := s.DecideCase(decideInput(tenantA, caseID)); !isolationRefused(err) {
			t.Fatalf("expected a refusal deciding tenant B's caseID as tenant A, got %v", err)
		}
	})

	t.Run("cannot_act_on_case", func(t *testing.T) {
		if _, _, err := s.ActOnCase(actionInput(tenantA, caseID)); !isolationRefused(err) {
			t.Fatalf("expected a refusal acting on tenant B's caseID as tenant A, got %v", err)
		}
	})

	t.Run("cannot_generate_dossier", func(t *testing.T) {
		if _, err := s.GenerateDossier(tenantA, caseID); !isolationRefused(err) {
			t.Fatalf("expected a refusal generating tenant B's dossier as tenant A, got %v", err)
		}
	})

	t.Run("cannot_replay_authorization", func(t *testing.T) {
		if _, err := s.Replay(tenantA, caseID); !isolationRefused(err) {
			t.Fatalf("expected a refusal replaying tenant B's case as tenant A, got %v", err)
		}
	})

	// Sanity: tenant B itself can still do everything normally --
	// isolation refuses the WRONG tenant, never the RIGHT one.
	t.Run("tenant_B_itself_still_works", func(t *testing.T) {
		view, err := s.GetCase(tenantB, caseID)
		if err != nil {
			t.Fatalf("expected tenant B to still read its own case: %v", err)
		}
		if view.Outcome != string(dB.Outcome()) {
			t.Fatalf("unexpected outcome for tenant B's own case: %+v", view)
		}
	})
}

// TestTwoTenantsCanUseTheIdenticalCaseIDIndependently proves case IDs
// are scoped PER TENANT, not globally unique -- two different
// customers using the same case-numbering convention must never
// collide.
func TestTwoTenantsCanUseTheIdenticalCaseIDIndependently(t *testing.T) {
	s := NewStore()
	const caseID = "CASE-0001"
	if err := s.CreateCase("tenant-A", caseID, 0); err != nil {
		t.Fatalf("CreateCase(tenant-A): %v", err)
	}
	if err := s.CreateCase("tenant-B", caseID, 0); err != nil {
		t.Fatalf("CreateCase(tenant-B): expected the identical CaseID to be usable by a different tenant, got %v", err)
	}
}
