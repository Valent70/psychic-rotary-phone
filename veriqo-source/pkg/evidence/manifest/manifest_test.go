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
		Classification: "INTERNAL",
	}
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
	sequence := []State{
		StateIngested, StateIntegrityAssessed, StateProvenanceComplete,
		StateReadyForFinalization, StateFinalized,
	}
	for _, to := range sequence {
		if _, err := reg.Advance("EV-1", to, 10); err != nil {
			t.Fatalf("Advance to %s: %v", to, err)
		}
	}
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
	for _, to := range []State{StateIngested, StateIntegrityAssessed, StateProvenanceComplete, StateReadyForFinalization, StateFinalized} {
		if _, err := reg.Advance("EV-1", to, 10); err != nil {
			t.Fatalf("Advance to %s: %v", to, err)
		}
	}
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
	for _, to := range []State{StateIngested, StateIntegrityAssessed, StateProvenanceComplete, StateReadyForFinalization, StateFinalized} {
		if _, err := reg.Advance("EV-1", to, 10); err != nil {
			t.Fatalf("Advance to %s: %v", to, err)
		}
	}
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
	for _, to := range []State{StateIngested, StateIntegrityAssessed, StateProvenanceComplete, StateReadyForFinalization, StateFinalized} {
		if _, err := reg.Advance("EV-1", to, 10); err != nil {
			t.Fatalf("Advance to %s: %v", to, err)
		}
	}
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
		e, err := reg.RecordCustodyEvent("EV-1", "EVT-"+string(action), "PTY-1", action, uint64(i*10), "routine")
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
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyReceived, 10, "routine"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := reg.RecordCustodyEvent("EV-1", "EVT-2", "PTY-1", CustodyRegistered, 20, "routine"); err != nil {
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
	_, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyAction("BOGUS"), 10, "x")
	if !errors.Is(err, ErrUnknownCustodyAction) {
		t.Fatalf("expected ErrUnknownCustodyAction, got %v", err)
	}
}

func TestRecordCustodyEventUpdatesManifestChainHead(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.RegisterDraft(validDraft("EV-1")); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	e, err := reg.RecordCustodyEvent("EV-1", "EVT-1", "PTY-1", CustodyReceived, 10, "routine")
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
	for _, to := range []State{StateIngested, StateIntegrityAssessed, StateProvenanceComplete, StateReadyForFinalization, StateFinalized} {
		if _, err := reg.Advance("EV-1", to, 10); err != nil {
			t.Fatalf("Advance to %s: %v", to, err)
		}
	}
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
