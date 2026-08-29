package manifest

import (
	"errors"
	"reflect"
	"testing"
)

func validDraft(evidenceID string) Manifest {
	return Manifest{
		TenantID: "TENANT-1", CaseID: "CASE-1", EvidenceID: evidenceID,
		URI: "s3://bucket/obj", Filename: "doc.pdf", MediaType: "application/pdf",
		ByteSize: 1024, SHA256: "sha256:deadbeef",
		Method: "manual upload", Collector: "PTY-1", Source: "claimant",
		AcquiredAt: 10, ReceivedAt: 10, System: "veriqo-cre", SystemVersion: "v1",
		HashStatus: "COMPUTED", SignatureStatus: "UNSIGNED",
		Classification:    "INTERNAL",
		AcquisitionRecord: "manual upload logged by claims handler PTY-1",
	}
}

// advanceThroughFullLifecycle drives evidenceID from DRAFT all the way
// to FINALIZED, recording the real custody events each transition's
// prerequisite now requires (Authority Round 2's transitionPrerequisiteLocked)
// -- the honest way to reach FINALIZED, mirroring what any real caller
// must now do. A caller who skips one of these RecordCustodyEvent calls
// gets ErrTransitionPrerequisiteNotMet, exercised directly by this
// file's own adversarial tests below.
func advanceThroughFullLifecycle(t *testing.T, reg *Registry, evidenceID string, tick uint64) Manifest {
	t.Helper()
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-received", "PTY-1", CustodyReceived, tick, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateIngested, tick); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "PTY-1", CustodyHashed, tick, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateIntegrityAssessed, tick); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateProvenanceComplete, tick); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	if _, err := reg.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "PTY-1", CustodyReviewed, tick, "reviewed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	if _, err := reg.Advance(evidenceID, StateReadyForFinalization, tick); err != nil {
		t.Fatalf("Advance to READY_FOR_FINALIZATION: %v", err)
	}
	m, err := reg.Advance(evidenceID, StateFinalized, tick)
	if err != nil {
		t.Fatalf("Advance to FINALIZED: %v", err)
	}
	return m
}

func TestRegisterDraftStartsAtVersion1(t *testing.T) {
	reg := NewRegistry()
	m, err := reg.RegisterDraft(validDraft("EV-1"))
	if err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if m.State != StateDraft || m.Version != 1 {
		t.Fatalf("expected DRAFT v1, got %s v%d", m.State, m.Version)
	}
}

func TestRegisterDraftRefusesDuplicateEvidenceID(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	_, err := reg.RegisterDraft(validDraft("EV-1"))
	if !errors.Is(err, ErrVersionAlreadyExists) {
		t.Fatalf("expected ErrVersionAlreadyExists, got %v", err)
	}
}

func TestRegisterDraftRefusesInvalidManifest(t *testing.T) {
	reg := NewRegistry()
	bad := validDraft("EV-1")
	bad.SHA256 = ""
	_, err := reg.RegisterDraft(bad)
	if !errors.Is(err, ErrEmptySHA256) {
		t.Fatalf("expected ErrEmptySHA256, got %v", err)
	}
}

// TestFinalizationStateMachineFollowsExactSequence proves every
// transition VTECP-001 §8 names is legal, in order, and that skipping
// a step is refused.
func TestFinalizationStateMachineFollowsExactSequence(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceThroughFullLifecycle(t, reg, "EV-1", 10)
	m, err := reg.Latest("EV-1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if m.State != StateFinalized {
		t.Fatalf("expected FINALIZED, got %s", m.State)
	}
	if m.ManifestHash == "" {
		t.Fatal("expected a ManifestHash to be computed on finalization")
	}
	if m.FinalizedAt != 10 {
		t.Fatalf("expected FinalizedAt=10, got %d", m.FinalizedAt)
	}
}

func TestFinalizationRefusesSkippingAState(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	// DRAFT -> INTEGRITY_ASSESSED skips INGESTED.
	_, err := reg.Advance("EV-1", StateIntegrityAssessed, 10)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestFinalizedManifestIsImmutable(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceThroughFullLifecycle(t, reg, "EV-1", 10)
	// Attempting ANY further advance -- even to the one legal next
	// state (SUPERSEDED) -- via Advance must be refused; only
	// Supersede() may move a FINALIZED manifest forward, and even that
	// creates a NEW version rather than editing this one.
	_, err := reg.Advance("EV-1", StateSuperseded, 20)
	if !errors.Is(err, ErrFinalizedIsImmutable) {
		t.Fatalf("expected ErrFinalizedIsImmutable, got %v", err)
	}
}

// TestSupersedeCreatesNewVersionWithoutRewritingHistory is the LAW-04
// structural proof: correcting a finalized manifest creates VERSION
// N+1, and VERSION N remains queryable, unchanged, and still
// cryptographically verifiable.
func TestSupersedeCreatesNewVersionWithoutRewritingHistory(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceThroughFullLifecycle(t, reg, "EV-1", 10)
	v1Before, _ := reg.Latest("EV-1")

	corrected := validDraft("EV-1")
	corrected.SHA256 = "sha256:corrected"
	v2, err := reg.Supersede(corrected, 20)
	if err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if v2.Version != 2 || v2.ParentVersion != 1 || v2.State != StateDraft {
		t.Fatalf("expected v2 DRAFT with ParentVersion=1, got v%d parent=%d state=%s", v2.Version, v2.ParentVersion, v2.State)
	}

	versions := reg.Versions("EV-1")
	if len(versions) != 2 {
		t.Fatalf("expected 2 recorded versions, got %d", len(versions))
	}
	if versions[0].State != StateSuperseded {
		t.Fatalf("expected v1 to now read SUPERSEDED, got %s", versions[0].State)
	}
	// Every field of v1 OTHER than State is untouched.
	v1After := versions[0]
	v1After.State = v1Before.State
	if !reflect.DeepEqual(v1After, v1Before) {
		t.Fatalf("v1's other fields changed across Supersede -- history was rewritten, not appended:\nbefore=%+v\nafter=%+v", v1Before, v1After)
	}
	if err := VerifyManifestHash(v1Before); err != nil {
		t.Fatalf("v1 must remain independently hash-verifiable after being superseded: %v", err)
	}
}

func TestSupersedeRefusesAnUnfinalizedParent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	_, err := reg.Supersede(validDraft("EV-1"), 20)
	if !errors.Is(err, ErrParentNotFinalized) {
		t.Fatalf("expected ErrParentNotFinalized, got %v", err)
	}
}

// TestVerifyManifestHashDetectsTampering proves the manifest hash
// genuinely covers the semantic fields -- mutating any one of them
// after finalization must be independently detectable.
func TestVerifyManifestHashDetectsTampering(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceThroughFullLifecycle(t, reg, "EV-1", 10)
	m, _ := reg.Latest("EV-1")
	if err := VerifyManifestHash(m); err != nil {
		t.Fatalf("expected the genuine manifest to verify, got %v", err)
	}
	tampered := m
	tampered.SHA256 = "sha256:tampered"
	if err := VerifyManifestHash(tampered); err == nil {
		t.Fatal("expected a tampered manifest to fail hash verification")
	}
}

// TestCustodyChainIsHashLinked proves Hn = SHA256(Hn-1 || JCS(EventN))
// holds across a real sequence of events, genesis included.
func TestCustodyChainIsHashLinked(t *testing.T) {
	reg := NewRegistry()
	events := []CustodyAction{CustodyReceived, CustodyRegistered, CustodyHashed, CustodyStored}
	var lastHash string
	for i, action := range events {
		e, err := reg.RecordCustodyEvent("EV-1", "EVT-"+string(action), "PTY-1", action, uint64(i*10), "routine", "")
		if err != nil {
			t.Fatalf("RecordCustodyEvent(%s): %v", action, err)
		}
		if i == 0 {
			if e.PreviousHash != GenesisHash {
				t.Fatalf("expected the first event's PreviousHash to be GenesisHash, got %s", e.PreviousHash)
			}
		} else if e.PreviousHash != lastHash {
			t.Fatalf("expected event %d's PreviousHash to equal event %d's EventHash", i, i-1)
		}
		lastHash = e.EventHash
	}
	chain := reg.CustodyChain("EV-1")
	if len(chain) != len(events) {
		t.Fatalf("expected %d chain entries, got %d", len(events), len(chain))
	}
	if err := reg.VerifyCustodyChain("EV-1"); err != nil {
		t.Fatalf("expected the genuine chain to verify, got %v", err)
	}
}

func TestCustodyChainDetectsTampering(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyReceived, 10, "routine", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-2", "PTY-1", CustodyRegistered, 20, "routine", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	// Tamper with the in-memory chain directly to simulate a corrupted
	// record -- exercising VerifyCustodyChain's own detection, not the
	// Registry's write path (which never permits this).
	reg.mu.Lock()
	chain := reg.custody["EV-1"]
	chain[0].Reason = "tampered"
	reg.mu.Unlock()

	if err := reg.VerifyCustodyChain("EV-1"); !errors.Is(err, ErrCustodyChainBroken) {
		t.Fatalf("expected ErrCustodyChainBroken, got %v", err)
	}
}

func TestRecordCustodyEventRefusesUnknownAction(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyAction("BOGUS"), 10, "x", "")
	if !errors.Is(err, ErrUnknownCustodyAction) {
		t.Fatalf("expected ErrUnknownCustodyAction, got %v", err)
	}
}

func TestRecordCustodyEventUpdatesManifestChainHead(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	e, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyReceived, 10, "routine", "")
	if err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	m, _ := reg.Latest("EV-1")
	if m.CustodyChainHead != e.EventHash {
		t.Fatalf("expected the manifest's CustodyChainHead to track the latest custody event, got %s want %s", m.CustodyChainHead, e.EventHash)
	}
}

// TestTransformationChainProvesDerivation is VTECP-001 §10's own
// worked example: ORIGINAL PDF -> OCR -> EXTRACTED TEXT -> NORMALIZED
// DATA -> DERIVED FACT.
func TestTransformationChainProvesDerivation(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	m, err := reg.AddTransformation("EV-1", Transformation{
		SourceVersionID: "EV-1@1", TransformType: "OCR", Transformer: "veriqo-ocr",
		TransformerVersion: "v2.1", InputHash: "sha256:original", OutputHash: "sha256:extracted",
		Tick: 15,
	})
	if err != nil {
		t.Fatalf("AddTransformation: %v", err)
	}
	if len(m.TransformationChain) != 1 {
		t.Fatalf("expected 1 transformation, got %d", len(m.TransformationChain))
	}
	if m.TransformationChain[0].TransformType != "OCR" {
		t.Fatalf("unexpected transform recorded: %+v", m.TransformationChain[0])
	}
}

func TestAddTransformationRefusedAfterFinalization(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	advanceThroughFullLifecycle(t, reg, "EV-1", 10)
	_, err := reg.AddTransformation("EV-1", Transformation{TransformType: "OCR"})
	if !errors.Is(err, ErrFinalizedIsImmutable) {
		t.Fatalf("expected ErrFinalizedIsImmutable, got %v", err)
	}
}

func TestLatestRefusesUnknownEvidenceID(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Latest("EV-NEVER-REGISTERED")
	if !errors.Is(err, ErrManifestNotFound) {
		t.Fatalf("expected ErrManifestNotFound, got %v", err)
	}
}

func TestKnownStateAndCustodyActionVocabulariesAreClosed(t *testing.T) {
	for _, s := range []State{StateDraft, StateIngested, StateIntegrityAssessed, StateProvenanceComplete, StateReadyForFinalization, StateFinalized, StateSuperseded} {
		if !IsKnownState(s) {
			t.Errorf("expected %q to be known", s)
		}
	}
	if IsKnownState("NOT_A_STATE") {
		t.Fatal("an unknown state must never report as known")
	}
	for _, a := range []CustodyAction{CustodyReceived, CustodyRegistered, CustodyHashed, CustodyStored, CustodyAccessed, CustodyTransformed, CustodyDerived, CustodyReviewed, CustodyExported, CustodySuperseded} {
		if !IsKnownCustodyAction(a) {
			t.Errorf("expected %q to be known", a)
		}
	}
	if IsKnownCustodyAction("BOGUS") {
		t.Fatal("an unknown custody action must never report as known")
	}
}

// ---- Authority Round 2: transitionPrerequisiteLocked adversarial tests ----
//
// Perlu_ditutup_dan_ditingkatkan.docx's own framing: "Sequence integrity
// != transition authority" -- validTransitions alone only proves a
// transition is legal in the abstract (state A -> state B is an allowed
// edge), never that the SPECIFIC evidenceID actually earned it. Every
// test below drives a manifest to the edge of a real transition WITHOUT
// performing the substantive work that transition claims, and confirms
// Advance refuses it via ErrTransitionPrerequisiteNotMet -- proving
// "fake process -> valid sequence -> FINALIZED" is no longer possible.

func TestAdvanceRefusesIngestedWithNoCustodyEvent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	// No RecordCustodyEvent call at all -- a caller trying to fake
	// "receipt" happened just by calling Advance in the right order.
	_, err := reg.Advance("EV-1", StateIngested, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet, got %v", err)
	}
}

func TestAdvanceRefusesIntegrityAssessedWithoutHashedCustodyEvent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	// HashStatus is set (validDraft sets it), but no HASHED custody
	// event was ever recorded -- a caller claiming "we hashed it"
	// without the attributed, hash-chained record to back the claim.
	_, err := reg.Advance("EV-1", StateIntegrityAssessed, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet, got %v", err)
	}
}

func TestAdvanceRefusesIntegrityAssessedWithoutHashStatus(t *testing.T) {
	reg := NewRegistry()
	d := validDraft("EV-1")
	d.HashStatus = ""
	if _, err := reg.RegisterDraft(d); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	_, err := reg.Advance("EV-1", StateIntegrityAssessed, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet, got %v", err)
	}
}

func TestAdvanceRefusesProvenanceCompleteWithoutAcquisitionRecord(t *testing.T) {
	reg := NewRegistry()
	d := validDraft("EV-1")
	d.AcquisitionRecord = ""
	if _, err := reg.RegisterDraft(d); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIntegrityAssessed, 10); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	_, err := reg.Advance("EV-1", StateProvenanceComplete, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet, got %v", err)
	}
}

func TestAdvanceRefusesReadyForFinalizationWithoutReviewedCustodyEvent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIntegrityAssessed, 10); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateProvenanceComplete, 10); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	// No REVIEWED custody event -- a caller trying to skip the
	// independent review step entirely.
	_, err := reg.Advance("EV-1", StateReadyForFinalization, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet, got %v", err)
	}
}

func TestAdvanceRefusesFinalizationWithoutClassification(t *testing.T) {
	reg := NewRegistry()
	d := validDraft("EV-1")
	d.Classification = ""
	if _, err := reg.RegisterDraft(d); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	for _, action := range []CustodyAction{CustodyReceived} {
		if _, err := reg.RecordCustodyEvent("EV-1", "EVT-"+string(action), "PTY-1", action, 10, "x", ""); err != nil {
			t.Fatalf("RecordCustodyEvent(%s): %v", action, err)
		}
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIntegrityAssessed, 10); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateProvenanceComplete, 10); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-reviewed", "PTY-1", CustodyReviewed, 10, "reviewed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateReadyForFinalization, 10); err != nil {
		t.Fatalf("Advance to READY_FOR_FINALIZATION: %v", err)
	}
	_, err := reg.Advance("EV-1", StateFinalized, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet, got %v", err)
	}
}

// TestAdvanceRefusesFinalizationWithATamperedCustodyChain is the
// strongest form of the FINALIZED gate: even when every field-level
// prerequisite is satisfied, Advance independently re-verifies the
// WHOLE custody chain's hash linkage before allowing FINALIZED, and
// refuses if any prior event was corrupted -- proving finalization
// depends on genuine end-to-end chain integrity, not merely on each
// individual event having once been recorded honestly.
func TestAdvanceRefusesFinalizationWithATamperedCustodyChain(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIntegrityAssessed, 10); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateProvenanceComplete, 10); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-reviewed", "PTY-1", CustodyReviewed, 10, "reviewed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateReadyForFinalization, 10); err != nil {
		t.Fatalf("Advance to READY_FOR_FINALIZATION: %v", err)
	}
	// Corrupt the in-memory chain directly to simulate a tampered
	// record -- the same technique TestCustodyChainDetectsTampering
	// uses, exercising the detection path rather than the write path
	// (which never permits this).
	reg.mu.Lock()
	chain := reg.custody["EV-1"]
	chain[0].Reason = "tampered after the fact"
	reg.mu.Unlock()

	_, err := reg.Advance("EV-1", StateFinalized, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet for a tampered custody chain, got %v", err)
	}
}

// ---- Final Authority Hardening Round: FINALIZED must imply immutability
// of every hash-covered field, not just a documented convention ----

// TestRecordCustodyEventDoesNotStaleTheFinalizedManifestHash is the
// direct adversarial proof: RecordCustodyEvent previously kept syncing
// CustodyChainHead onto the latest Manifest version even after that
// version reached FINALIZED, silently staling ManifestHash (computed
// over the OLD CustodyChainHead value) out from under it. A later
// custody event -- EXPORTED, ACCESSED, entirely legitimate actions
// against evidence that is already finalized -- must still be
// recordable in the custody LOG, but must never mutate the finalized
// manifest's own already-hashed state.
func TestRecordCustodyEventDoesNotStaleTheFinalizedManifestHash(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	finalized := advanceThroughFullLifecycle(t, reg, "EV-1", 10)
	if finalized.ManifestHash == "" {
		t.Fatal("expected a ManifestHash on the finalized manifest")
	}
	if err := VerifyManifestHash(finalized); err != nil {
		t.Fatalf("expected the freshly finalized manifest to verify, got %v", err)
	}

	// A legitimate, later custody event against already-finalized
	// evidence -- e.g. it was exported for a dispute.
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-exported", "dispute-team", CustodyExported, 20, "exported for arbitration", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(EXPORTED) after finalization: %v", err)
	}
	// The custody LOG grew...
	chain := reg.CustodyChain("EV-1")
	if len(chain) == 0 || chain[len(chain)-1].Action != CustodyExported {
		t.Fatalf("expected the EXPORTED event to be recorded in the custody log, got %+v", chain)
	}
	// ...but the FINALIZED manifest's own state is byte-for-byte
	// unchanged, and its hash still verifies.
	stillLatest, err := reg.Latest("EV-1")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if stillLatest.CustodyChainHead != finalized.CustodyChainHead {
		t.Fatalf("CustodyChainHead changed after finalization: got %s, want %s (frozen at finalization)", stillLatest.CustodyChainHead, finalized.CustodyChainHead)
	}
	if stillLatest.ManifestHash != finalized.ManifestHash {
		t.Fatal("ManifestHash changed after finalization -- a supposedly immutable field was mutated")
	}
	if err := VerifyManifestHash(stillLatest); err != nil {
		t.Fatalf("expected the finalized manifest to still independently verify after a later custody event, got %v", err)
	}
}

// ---- Final Authority Hardening Round: prerequisite identity binding ----
//
// "Prerequisite existence is not enough; prerequisite identity binding
// must also be proven." A HASHED custody event existing somewhere in
// EV-1's chain used to be enough to reach INTEGRITY_ASSESSED, even if
// that event's own content had nothing to do with the manifest's
// actual SHA256. The tests below prove that gap is closed: an event of
// the right ACTION but the WRONG (or missing) ContentHash does not
// satisfy the prerequisite.

func TestAdvanceRefusesHashedEventBoundToDifferentContent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	// A HASHED event is recorded, but it attests to SOME OTHER
	// document's content -- e.g. copy-pasted from a different case, or
	// a genuine mistake -- not the manifest's own "sha256:deadbeef".
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:wrong-document-entirely"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	_, err := reg.Advance("EV-1", StateIntegrityAssessed, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet for a HASHED event bound to the wrong content, got %v", err)
	}
}

func TestAdvanceRefusesHashedEventWithNoContentHashAtAll(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	// The pre-Hardening-Round shape: an event of the right ACTION,
	// existence-only, no content claim at all.
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-hashed", "PTY-1", CustodyHashed, 10, "hashed", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	_, err := reg.Advance("EV-1", StateIntegrityAssessed, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet for a HASHED event with no ContentHash, got %v", err)
	}
}

func TestAdvanceRefusesReviewedEventBoundToDifferentContent(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-received", "PTY-1", CustodyReceived, 10, "received", ""); err != nil {
		t.Fatalf("RecordCustodyEvent(RECEIVED): %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIngested, 10); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-hashed", "PTY-1", CustodyHashed, 10, "hashed", "sha256:deadbeef"); err != nil {
		t.Fatalf("RecordCustodyEvent(HASHED): %v", err)
	}
	if _, err := reg.Advance("EV-1", StateIntegrityAssessed, 10); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := reg.Advance("EV-1", StateProvenanceComplete, 10); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	// The review was genuinely performed, but on the WRONG document.
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-reviewed", "PTY-1", CustodyReviewed, 10, "reviewed", "sha256:a-different-document"); err != nil {
		t.Fatalf("RecordCustodyEvent(REVIEWED): %v", err)
	}
	_, err := reg.Advance("EV-1", StateReadyForFinalization, 10)
	if !errors.Is(err, ErrTransitionPrerequisiteNotMet) {
		t.Fatalf("expected ErrTransitionPrerequisiteNotMet for a REVIEWED event bound to the wrong content, got %v", err)
	}
}

// TestCustodyEventContentHashIsHashChainCovered proves ContentHash is
// itself part of what VerifyCustodyChain protects: an attacker with
// write access to the in-memory chain (the same threat
// TestCustodyChainDetectsTampering already exercises for Reason)
// rewriting an event's ContentHash after the fact must be just as
// detectable.
func TestCustodyEventContentHashIsHashChainCovered(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyHashed, 10, "hashed", "sha256:original"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	reg.mu.Lock()
	chain := reg.custody["EV-1"]
	chain[0].ContentHash = "sha256:swapped-in-after-the-fact"
	reg.mu.Unlock()

	if err := reg.VerifyCustodyChain("EV-1"); !errors.Is(err, ErrCustodyChainBroken) {
		t.Fatalf("expected ErrCustodyChainBroken for a tampered ContentHash, got %v", err)
	}
}

// TestReorderedCustodyChainFailsVerification proves the custody chain's
// hash-linking (Hn = SHA256(Hn-1 || JCS(EventN))) itself detects a
// replayed-out-of-order or spliced chain, not just a single tampered
// field -- one of the Final Authority Hardening Round's own named
// adversarial scenarios ("reordered events"). This package's own
// exported surface has no way to reorder a chain (RecordCustodyEvent
// only ever appends), so this reaches into the same unexported field
// TestCustodyChainDetectsTampering already does, to simulate what a
// corrupted persistence/replay layer replaying events out of order
// would produce.
func TestReorderedCustodyChainFailsVerification(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-1", "actor", CustodyReceived, 10, "first", ""); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "evt-2", "actor", CustodyHashed, 20, "second", "sha256:x"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if err := reg.VerifyCustodyChain("EV-1"); err != nil {
		t.Fatalf("expected the genuine, in-order chain to verify, got %v", err)
	}
	reg.mu.Lock()
	chain := reg.custody["EV-1"]
	chain[0], chain[1] = chain[1], chain[0]
	reg.mu.Unlock()

	if err := reg.VerifyCustodyChain("EV-1"); !errors.Is(err, ErrCustodyChainBroken) {
		t.Fatalf("expected ErrCustodyChainBroken for a reordered custody chain, got %v", err)
	}
}
