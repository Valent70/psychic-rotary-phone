package evidence

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/provenance"
)

// TestNewDefaultsToUnknownPendingContractNotBlank proves a constructed
// Record carries the canonical fail-closed rights default rather than
// an empty string. The distinction matters: an empty/unrecognised state
// permits NOTHING (a hard denial), whereas UNKNOWN_PENDING_CONTRACT
// permits internal use only — which is the honest position for evidence
// that has arrived but whose commercial terms are not yet settled.
func TestNewDefaultsToUnknownPendingContractNotBlank(t *testing.T) {
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rec.Rights != provenance.RightsUnknownPendingContract {
		t.Fatalf("Rights = %q, want %q", rec.Rights, provenance.RightsUnknownPendingContract)
	}
	if !rec.Permits(provenance.UseInternalOnly) {
		t.Fatal("UNKNOWN_PENDING_CONTRACT must permit internal use")
	}
	for _, u := range []provenance.Use{provenance.UseCustomerFacing, provenance.UseDispute, provenance.UseCalibration, provenance.UseTraining} {
		if rec.Permits(u) {
			t.Fatalf("UNKNOWN_PENDING_CONTRACT must not permit %s", u)
		}
	}
}

// TestRevokedRightsPermitNothing is the insurance-side restatement of
// the canonical rule: REVOKED and EXPIRED permit no use at all,
// including internal use. This is the same fail-closed table
// pkg/evidence/provenance owns — insurance consults it, it does not
// keep its own.
func TestRevokedRightsPermitNothing(t *testing.T) {
	for _, state := range []provenance.RightsState{provenance.RightsRevoked, provenance.RightsExpired} {
		rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		rec.Rights = state
		for _, u := range []provenance.Use{
			provenance.UseInternalOnly, provenance.UseCustomerFacing,
			provenance.UseDispute, provenance.UseCalibration, provenance.UseTraining,
		} {
			if rec.Permits(u) {
				t.Fatalf("%s must permit nothing, but permitted %s", state, u)
			}
		}
	}
}

// TestUnsetRightsPermitNothing: a Record built by hand via a struct
// literal (bypassing New) has an empty Rights field, and an
// unrecognised state must deny every use rather than defaulting open.
func TestUnsetRightsPermitNothing(t *testing.T) {
	var rec Record
	for _, u := range []provenance.Use{provenance.UseInternalOnly, provenance.UseDispute} {
		if rec.Permits(u) {
			t.Fatalf("an unset rights state must permit nothing, but permitted %s", u)
		}
	}
}

// TestPossessionIsNotPermission: a record sitting in the registry is
// still refused for a use its rights do not cover, and the refusal
// names why.
func TestPossessionIsNotPermission(t *testing.T) {
	reg := NewRegistry()
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := reg.RequirePermitted(rec.EvidenceID(), provenance.UseDispute); !errors.Is(err, ErrRightsPermitNothing) {
		t.Fatalf("expected ErrRightsPermitNothing for dispute use, got %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsDisputeUseAllowed); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	if _, err := reg.RequirePermitted(rec.EvidenceID(), provenance.UseDispute); err != nil {
		t.Fatalf("after DISPUTE_USE_ALLOWED, expected permitted, got %v", err)
	}
	// Granting dispute use does not silently grant customer-facing use.
	if _, err := reg.RequirePermitted(rec.EvidenceID(), provenance.UseCustomerFacing); !errors.Is(err, ErrRightsPermitNothing) {
		t.Fatal("DISPUTE_USE_ALLOWED must not permit customer-facing use")
	}
}

func TestSetRightsRefusesUnknownState(t *testing.T) {
	reg := NewRegistry()
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsState("TOTALLY_FINE_HONESTLY")); !errors.Is(err, ErrUnknownRightsState) {
		t.Fatalf("expected ErrUnknownRightsState, got %v", err)
	}
}

// TestSupersessionDeniesUseWithoutMutatingTheOriginal is the evidence
// immutability rule (Final Design §10, spec §72): a correction creates
// a NEW record; the original's content — and therefore its
// content-addressed EvidenceID — is untouched, but it stops being
// usable as if it were current.
func TestSupersessionDeniesUseWithoutMutatingTheOriginal(t *testing.T) {
	reg := NewRegistry()
	oldRec, err := New("CASE-1", mustEvidence(t, "S1", "src-a", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	newRec, err := New("CASE-1", mustEvidence(t, "S1", "src-b", 200), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, r := range []Record{oldRec, newRec} {
		if err := reg.Submit(r); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	oldID, newID := oldRec.EvidenceID(), newRec.EvidenceID()
	beforeContentID := oldRec.Underlying.ComputeID()

	if err := reg.SetRights(oldID, provenance.RightsDisputeUseAllowed); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	if err := reg.MarkSuperseded(oldID, newID); err != nil {
		t.Fatalf("MarkSuperseded: %v", err)
	}

	got, ok := reg.Get(oldID)
	if !ok {
		t.Fatal("the superseded record must still be retrievable — it is history, not garbage")
	}
	if got.EvidenceID() != oldID {
		t.Fatalf("the superseded record's content-addressed ID changed: %s -> %s", oldID, got.EvidenceID())
	}
	// ComputeID is a hash over every semantic field of the underlying
	// ontology.Evidence, so an unchanged ID is a real proof that no
	// field of the original was edited, not just that the ID string was
	// left alone.
	if got.Underlying.ComputeID() != beforeContentID {
		t.Fatal("the superseded record's underlying ontology.Evidence was mutated; it must never be")
	}
	if got.SupersededBy != newID {
		t.Fatalf("SupersededBy = %q, want %q", got.SupersededBy, newID)
	}
	// Rights still say DISPUTE_USE_ALLOWED, but supersession wins.
	if got.Rights != provenance.RightsDisputeUseAllowed {
		t.Fatalf("supersession must not silently rewrite the rights state, got %q", got.Rights)
	}
	if got.Permits(provenance.UseDispute) {
		t.Fatal("a superseded record must permit no use, whatever its rights state says")
	}
}

func TestMarkSupersededRefusesSelfAndUnknown(t *testing.T) {
	reg := NewRegistry()
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	id := rec.EvidenceID()
	if err := reg.MarkSuperseded(id, id); err == nil {
		t.Fatal("a record must not be able to supersede itself")
	}
	if err := reg.MarkSuperseded(id, "not-a-real-evidence-id"); !errors.Is(err, ErrEvidenceNotFound) {
		t.Fatalf("expected ErrEvidenceNotFound for an unregistered superseding record, got %v", err)
	}
}

func TestPermittedForFiltersByRealRights(t *testing.T) {
	reg := NewRegistry()
	var ids []string
	for i, src := range []string{"a", "b", "c"} {
		rec, err := New("CASE-1", mustEvidence(t, "S1", src, uint64(100+i)), "PTY-1", OriginClaimant)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := reg.Submit(rec); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		ids = append(ids, rec.EvidenceID())
	}
	if err := reg.SetRights(ids[0], provenance.RightsDisputeUseAllowed); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	if err := reg.SetRights(ids[1], provenance.RightsRevoked); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	// ids[2] keeps the UNKNOWN_PENDING_CONTRACT default.

	got := reg.PermittedFor(provenance.UseDispute)
	if len(got) != 1 || got[0].EvidenceID() != ids[0] {
		t.Fatalf("PermittedFor(DISPUTE) returned %d records, want exactly the dispute-authorized one", len(got))
	}
	internal := reg.PermittedFor(provenance.UseInternalOnly)
	if len(internal) != 2 {
		t.Fatalf("PermittedFor(INTERNAL_ONLY) = %d, want 2 (the revoked one must be excluded)", len(internal))
	}
}
