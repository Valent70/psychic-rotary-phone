package commercialapi

import (
	"errors"
	"testing"

	"veriqo/pkg/governance/data"
)

func TestSubmitEvidencePlacesItUnderPreservationGovernance(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-preservation-A", "CASE-PRESERVATION-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, "EV-PRESERVATION-1", 10)

	state, err := s.EvidenceRetentionState(tenant, "EV-PRESERVATION-1")
	if err != nil {
		t.Fatalf("EvidenceRetentionState: %v", err)
	}
	if state != data.StateActive {
		t.Fatalf("expected freshly-submitted evidence to be ACTIVE, got %s", state)
	}
}

func TestLegalHoldBlocksPurgeAndRetentionProgressUntilReleased(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-preservation-hold", "CASE-PRESERVATION-HOLD-1"
	const evidenceID = "EV-PRESERVATION-HOLD-1"

	// A retention policy with zero grace/retention so a single
	// EvaluateRetention call would normally drive the record straight
	// to PURGE_ELIGIBLE -- the strongest possible test that a hold
	// actually blocks progress, not merely "hasn't gotten there yet."
	if err := s.SetRetentionPolicy(tenant, 0, 0, 0); err != nil {
		t.Fatalf("SetRetentionPolicy: %v", err)
	}
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, evidenceID, 0)

	if _, err := s.PlaceLegalHold(tenant, "MATTER-1", "dispute over EV-PRESERVATION-HOLD-1", "compliance-officer", "pending litigation", 1); err != nil {
		t.Fatalf("PlaceLegalHold: %v", err)
	}

	state, err := s.EvidenceRetentionState(tenant, evidenceID)
	if err != nil {
		t.Fatalf("EvidenceRetentionState: %v", err)
	}
	if state != data.StateHeld {
		t.Fatalf("expected HELD immediately after PlaceLegalHold, got %s", state)
	}

	if _, err := s.EvaluateRetention(100); err != nil {
		t.Fatalf("EvaluateRetention: %v", err)
	}
	state, err = s.EvidenceRetentionState(tenant, evidenceID)
	if err != nil {
		t.Fatalf("EvidenceRetentionState after Evaluate: %v", err)
	}
	if state != data.StateHeld {
		t.Fatalf("expected evidence to remain HELD across EvaluateRetention (a zero-retention policy would otherwise reach PURGE_ELIGIBLE in one call), got %s", state)
	}

	if _, err := s.PurgeEvidence(tenant, evidenceID, "someone", "trying anyway", 100); !errors.Is(err, data.ErrLegalHold) {
		t.Fatalf("expected PurgeEvidence to be refused with ErrLegalHold while under hold, got %v", err)
	}

	if err := s.ReleaseLegalHold(tenant, "MATTER-1", "compliance-officer", "litigation resolved", 200); err != nil {
		t.Fatalf("ReleaseLegalHold: %v", err)
	}
	state, err = s.EvidenceRetentionState(tenant, evidenceID)
	if err != nil {
		t.Fatalf("EvidenceRetentionState after release: %v", err)
	}
	if state != data.StateActive {
		t.Fatalf("expected ACTIVE immediately after the last hold releases, got %s", state)
	}

	if _, err := s.EvaluateRetention(201); err != nil {
		t.Fatalf("EvaluateRetention after release: %v", err)
	}
	state, err = s.EvidenceRetentionState(tenant, evidenceID)
	if err != nil {
		t.Fatalf("EvidenceRetentionState after post-release Evaluate: %v", err)
	}
	if state != data.StatePurgeEligible {
		t.Fatalf("expected PURGE_ELIGIBLE once the hold is gone and retention/grace have elapsed, got %s", state)
	}

	tomb, err := s.PurgeEvidence(tenant, evidenceID, "compliance-officer", "retention period expired, no active hold", 201)
	if err != nil {
		t.Fatalf("PurgeEvidence: %v", err)
	}
	if tomb.RecordID != evidenceID {
		t.Fatalf("expected the tombstone to name %s, got %s", evidenceID, tomb.RecordID)
	}

	finalState, err := s.EvidenceRetentionState(tenant, evidenceID)
	if err != nil {
		t.Fatalf("EvidenceRetentionState after purge: %v", err)
	}
	if finalState != data.StatePurged {
		t.Fatalf("expected PURGED after a successful purge, got %s", finalState)
	}
}

func TestPlaceLegalHoldRecordsARealCustodyEvent(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-preservation-custody", "CASE-PRESERVATION-CUSTODY-1"
	const evidenceID = "EV-PRESERVATION-CUSTODY-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, evidenceID, 0)

	before, err := s.GetCustody(tenant, evidenceID)
	if err != nil {
		t.Fatalf("GetCustody (before): %v", err)
	}

	if _, err := s.PlaceLegalHold(tenant, "MATTER-CUSTODY-1", "custody continuity check", "actor-1", "reason-1", 5); err != nil {
		t.Fatalf("PlaceLegalHold: %v", err)
	}

	after, err := s.GetCustody(tenant, evidenceID)
	if err != nil {
		t.Fatalf("GetCustody (after): %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("expected exactly one new custody event after PlaceLegalHold, before=%d after=%d", len(before), len(after))
	}
	newEvent := after[len(after)-1]
	if newEvent.Reason == "" || newEvent.Action != "ACCESSED" {
		t.Fatalf("expected the new custody event to record the hold with a real reason, got %+v", newEvent)
	}
}

func TestPreservationLedgerVerifiesAsARealHashChain(t *testing.T) {
	s := NewStore()
	const tenant, caseID = "tenant-preservation-ledger", "CASE-PRESERVATION-LEDGER-1"
	const evidenceID = "EV-PRESERVATION-LEDGER-1"
	if err := s.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenant, caseID, evidenceID, 0)
	if _, err := s.PlaceLegalHold(tenant, "MATTER-LEDGER-1", "ledger check", "actor-1", "reason-1", 1); err != nil {
		t.Fatalf("PlaceLegalHold: %v", err)
	}
	if err := s.ReleaseLegalHold(tenant, "MATTER-LEDGER-1", "actor-1", "done", 2); err != nil {
		t.Fatalf("ReleaseLegalHold: %v", err)
	}

	events := s.PreservationLedger()
	if len(events) == 0 {
		t.Fatal("expected a non-empty preservation ledger after real governance activity")
	}
	if err := s.VerifyPreservationChain(); err != nil {
		t.Fatalf("VerifyPreservationChain: %v", err)
	}
}

func TestEvidenceRetentionStateAndPurgeRespectTenantIsolation(t *testing.T) {
	s := NewStore()
	const tenantA, tenantB, caseID = "tenant-preservation-iso-A", "tenant-preservation-iso-B", "CASE-PRESERVATION-ISO-1"
	const evidenceID = "EV-PRESERVATION-ISO-1"
	if err := s.CreateCase(tenantA, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s, tenantA, caseID, evidenceID, 0)

	// Same structural isolation as the rest of this Store (see
	// TestTenantAIsolationFromTenantB's own doc comment): tenant B's
	// lookup key (tenantB+"/"+evidenceID) was never registered at all,
	// so it fails at "not found" before any ownership comparison would
	// even run -- a stronger guarantee than a runtime tenant check that
	// could be forgotten in some future code path.
	if _, err := s.EvidenceRetentionState(tenantB, evidenceID); !errors.Is(err, ErrEvidenceNotFound) && !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant B reading tenant A's retention state to be refused, got %v", err)
	}
	if _, err := s.PurgeEvidence(tenantB, evidenceID, "attacker", "trying anyway", 999); !errors.Is(err, ErrEvidenceNotFound) && !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("expected tenant B purging tenant A's evidence to be refused, got %v", err)
	}
}
