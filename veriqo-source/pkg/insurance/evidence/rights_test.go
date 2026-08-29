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

// trustedAuthorityRegistry returns a real provenance.Registry with one
// entity whose trust has genuinely been granted via GrantTrust (the
// only way TrustGranted ever becomes true), plus that entity's ID --
// the authoritative source SetRights now requires (Authority Round 2).
func trustedAuthorityRegistry(t *testing.T) (*provenance.Registry, string) {
	t.Helper()
	provReg := provenance.NewRegistry()
	const authorityID = "governance-lead-1"
	if err := provReg.Register(provenance.Entry{ID: authorityID, Kind: provenance.KindReviewer, Name: "Governance Lead"}); err != nil {
		t.Fatalf("provenance.Register: %v", err)
	}
	if err := provReg.GrantTrust(authorityID, "policy://rights-grant-v1", "", "compliance-officer", 1); err != nil {
		t.Fatalf("provenance.GrantTrust: %v", err)
	}
	return provReg, authorityID
}

// TestPossessionIsNotPermission: a record sitting in the registry is
// still refused for a use its rights do not cover, and the refusal
// names why.
func TestPossessionIsNotPermission(t *testing.T) {
	reg := NewRegistry()
	provReg, authorityID := trustedAuthorityRegistry(t)
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
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsDisputeUseAllowed, provReg, authorityID); err != nil {
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
	provReg, authorityID := trustedAuthorityRegistry(t)
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsState("TOTALLY_FINE_HONESTLY"), provReg, authorityID); !errors.Is(err, ErrUnknownRightsState) {
		t.Fatalf("expected ErrUnknownRightsState, got %v", err)
	}
}

// TestSetRightsRefusesNilProvenanceRegistry and
// TestSetRightsRefusesUntrustedAuthority are the direct adversarial
// proof for the Authority Round 2 fix: a caller cannot set rights by
// simply calling SetRights, whatever authorityID they name, unless
// that authority is a real entity with genuinely granted trust on
// file.
func TestSetRightsRefusesNilProvenanceRegistry(t *testing.T) {
	reg := NewRegistry()
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsCustomerFacingAllowed, nil, "anyone"); !errors.Is(err, ErrRightsGrantNotAuthorized) {
		t.Fatalf("expected ErrRightsGrantNotAuthorized for a nil provenance registry, got %v", err)
	}
}

func TestSetRightsRefusesUntrustedAuthority(t *testing.T) {
	reg := NewRegistry()
	rec, err := New("CASE-1", mustEvidence(t, "S1", "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	provReg := provenance.NewRegistry()
	// Registered, but trust was never granted -- Register alone must
	// never be enough (Entry.TrustGranted starts, and here stays,
	// false).
	if err := provReg.Register(provenance.Entry{ID: "unproven-party", Kind: provenance.KindReviewer, Name: "Someone"}); err != nil {
		t.Fatalf("provenance.Register: %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsCustomerFacingAllowed, provReg, "unproven-party"); !errors.Is(err, ErrRightsGrantNotAuthorized) {
		t.Fatalf("expected ErrRightsGrantNotAuthorized for an untrusted authority, got %v", err)
	}
	// Also refuses an authority that was never even registered.
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsCustomerFacingAllowed, provReg, "never-registered"); !errors.Is(err, ErrRightsGrantNotAuthorized) {
		t.Fatalf("expected ErrRightsGrantNotAuthorized for an unregistered authority, got %v", err)
	}
	// A subsequent revocation must also close the door again.
	if err := provReg.GrantTrust("unproven-party", "policy://x", "", "someone", 1); err != nil {
		t.Fatalf("GrantTrust: %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsCustomerFacingAllowed, provReg, "unproven-party"); err != nil {
		t.Fatalf("expected the now-trusted authority to succeed, got %v", err)
	}
	if err := provReg.RevokeTrust("unproven-party"); err != nil {
		t.Fatalf("RevokeTrust: %v", err)
	}
	if err := reg.SetRights(rec.EvidenceID(), provenance.RightsRevoked, provReg, "unproven-party"); !errors.Is(err, ErrRightsGrantNotAuthorized) {
		t.Fatalf("expected ErrRightsGrantNotAuthorized once trust is revoked, got %v", err)
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

	provReg, authorityID := trustedAuthorityRegistry(t)
	if err := reg.SetRights(oldID, provenance.RightsDisputeUseAllowed, provReg, authorityID); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	if err := reg.MarkSuperseded(oldID, newID, "claims-reviewer-1", "original scan was low resolution; re-scanned original document", 200); err != nil {
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
	if err := reg.MarkSuperseded(id, id, "actor", "reason", 100); err == nil {
		t.Fatal("a record must not be able to supersede itself")
	}
	if err := reg.MarkSuperseded(id, "not-a-real-evidence-id", "actor", "reason", 100); !errors.Is(err, ErrEvidenceNotFound) {
		t.Fatalf("expected ErrEvidenceNotFound for an unregistered superseding record, got %v", err)
	}
}

// TestMarkSupersededRequiresActorAndReason is Authority Round 2's own
// audit-trail requirement made concrete: "siapa, mengapa, kapan" (who,
// why, when) must be on file for every supersession, not reconstructed
// after the fact.
func TestMarkSupersededRequiresActorAndReason(t *testing.T) {
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
	if err := reg.MarkSuperseded(oldID, newID, "", "a real reason", 100); !errors.Is(err, ErrEmptySupersessionActor) {
		t.Fatalf("expected ErrEmptySupersessionActor for a blank actor, got %v", err)
	}
	if err := reg.MarkSuperseded(oldID, newID, "a real actor", "", 100); !errors.Is(err, ErrEmptySupersessionReason) {
		t.Fatalf("expected ErrEmptySupersessionReason for a blank reason, got %v", err)
	}
}

// TestMarkSupersededRecordsAnImmutableAuditTrail proves the "who, why,
// when" lands on the record's own ChainOfCustody -- an already-existing
// field reused rather than a second audit mechanism invented -- and
// that the superseded record's identity and prior content stay
// completely intact: A does not disappear from effective evidence
// (reg.Get/All still return it), it is only marked, never rewritten.
func TestMarkSupersededRecordsAnImmutableAuditTrail(t *testing.T) {
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
	const actor, reason = "claims-reviewer-7", "corrected: original was a partial scan"
	if err := reg.MarkSuperseded(oldID, newID, actor, reason, 250); err != nil {
		t.Fatalf("MarkSuperseded: %v", err)
	}
	got, ok := reg.Get(oldID)
	if !ok {
		t.Fatal("the superseded record must still be present in the registry (Get) -- it must never disappear")
	}
	all := reg.All()
	found := false
	for _, r := range all {
		if r.EvidenceID() == oldID {
			found = true
		}
	}
	if !found {
		t.Fatal("the superseded record must still be present in All() -- it must never disappear from the effective set's own history")
	}
	if len(got.ChainOfCustody) != 1 {
		t.Fatalf("expected exactly 1 custody entry recording the supersession, got %d", len(got.ChainOfCustody))
	}
	entry := got.ChainOfCustody[0]
	if entry.Holder != actor {
		t.Fatalf("custody entry Holder = %q, want %q (the actor who performed the supersession)", entry.Holder, actor)
	}
	if entry.Action != "SUPERSEDED" {
		t.Fatalf("custody entry Action = %q, want SUPERSEDED", entry.Action)
	}
	if entry.Tick != 250 {
		t.Fatalf("custody entry Tick = %d, want 250 (when the supersession happened)", entry.Tick)
	}
	if entry.Reference == "" {
		t.Fatal("custody entry Reference must record the reason -- why the supersession happened")
	}
}

// TestMarkSupersededRefusesReSupersession is the direct adversarial
// proof for the re-supersession gap this round closes: a second call
// against an already-superseded record must never silently rewrite
// which record replaced it.
func TestMarkSupersededRefusesReSupersession(t *testing.T) {
	reg := NewRegistry()
	oldRec, err := New("CASE-1", mustEvidence(t, "S1", "src-a", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	replacement1, err := New("CASE-1", mustEvidence(t, "S1", "src-b", 200), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	replacement2, err := New("CASE-1", mustEvidence(t, "S1", "src-c", 300), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, r := range []Record{oldRec, replacement1, replacement2} {
		if err := reg.Submit(r); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	oldID := oldRec.EvidenceID()
	if err := reg.MarkSuperseded(oldID, replacement1.EvidenceID(), "actor", "first correction", 200); err != nil {
		t.Fatalf("first MarkSuperseded: %v", err)
	}
	// A second, conflicting supersession of the SAME record must be
	// refused -- not silently overwrite SupersededBy with a different
	// successor.
	err = reg.MarkSuperseded(oldID, replacement2.EvidenceID(), "actor", "second, conflicting correction", 300)
	if !errors.Is(err, ErrAlreadySuperseded) {
		t.Fatalf("expected ErrAlreadySuperseded, got %v", err)
	}
	got, _ := reg.Get(oldID)
	if got.SupersededBy != replacement1.EvidenceID() {
		t.Fatalf("SupersededBy was rewritten by the refused second call: got %q, want %q (the first, legitimate successor)", got.SupersededBy, replacement1.EvidenceID())
	}
}

// TestMarkSupersededRefusesAnAlreadySupersededSuccessor is the
// "apakah B legitimate successor" check: a record that is not itself
// current cannot become the new current version for something else.
func TestMarkSupersededRefusesAnAlreadySupersededSuccessor(t *testing.T) {
	reg := NewRegistry()
	a, err := New("CASE-1", mustEvidence(t, "S1", "src-a", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := New("CASE-1", mustEvidence(t, "S1", "src-b", 200), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c, err := New("CASE-1", mustEvidence(t, "S1", "src-c", 300), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, r := range []Record{a, b, c} {
		if err := reg.Submit(r); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	// B supersedes A, so B is now the current record.
	if err := reg.MarkSuperseded(a.EvidenceID(), b.EvidenceID(), "actor", "A -> B", 200); err != nil {
		t.Fatalf("MarkSuperseded(A, B): %v", err)
	}
	// C supersedes B, so B is now ITSELF superseded.
	if err := reg.MarkSuperseded(b.EvidenceID(), c.EvidenceID(), "actor", "B -> C", 300); err != nil {
		t.Fatalf("MarkSuperseded(B, C): %v", err)
	}
	// A caller now tries to claim B (already superseded by C) is the
	// successor for some fourth record -- refused, because B is no
	// longer a legitimate current record.
	d, err := New("CASE-1", mustEvidence(t, "S1", "src-d", 400), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Submit(d); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	err = reg.MarkSuperseded(d.EvidenceID(), b.EvidenceID(), "actor", "trying to use a stale successor", 400)
	if !errors.Is(err, ErrIllegitimateSuccessor) {
		t.Fatalf("expected ErrIllegitimateSuccessor, got %v", err)
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
	provReg, authorityID := trustedAuthorityRegistry(t)
	if err := reg.SetRights(ids[0], provenance.RightsDisputeUseAllowed, provReg, authorityID); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	if err := reg.SetRights(ids[1], provenance.RightsRevoked, provReg, authorityID); err != nil {
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
