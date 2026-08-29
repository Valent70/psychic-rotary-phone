package evidence

import (
	"testing"

	"veriqo/pkg/evidence/manifest"
)

// This file is the "split-registry contradiction" adversarial test
// Authority Round 2 (Perlu_ditutup_dan_ditingkatkan.docx item 12) asked
// for directly: "Kalau keduanya bisa mengatakan: Evidence X = FINALIZED,
// maka kita harus menjawab: Which registry is authoritative?"
//
// This repository has exactly two registries that both track state
// "about" a piece of evidence: Registry (this package -- verification
// status and multi-dimensional strength) and manifest.Registry
// (acquisition/integrity/custody/finalization). The Canonical Authority
// audit in this round's report maps every Evidence property to exactly
// one authoritative owner; this test makes the specific claim the
// reviewer worried about mechanically checkable rather than merely
// asserted in prose: the two registries' own status vocabularies never
// overlap, so neither can be mistaken for making a claim the other
// owns, and advancing one never silently changes what the other reports.

// TestStatusAndManifestStateVocabulariesAreDisjoint proves the two
// registries cannot even LOOK like they are making the same claim: no
// string value in evidence.Status's vocabulary appears anywhere in
// manifest.State's vocabulary, or vice versa. In particular,
// evidence.Status has no "FINALIZED" value at all -- only
// manifest.State does -- so "Evidence X = FINALIZED" is a claim only
// manifest.Registry can ever make in this codebase; Registry
// (this package) structurally cannot echo it, correctly or otherwise.
func TestStatusAndManifestStateVocabulariesAreDisjoint(t *testing.T) {
	statusValues := map[Status]bool{
		StatusUnverified: true, StatusAuthenticitySupported: true, StatusAuthenticityDisputed: true,
		StatusAlterationDetected: true, StatusIncomplete: true, StatusCorroborated: true, StatusContradicted: true,
	}
	manifestStates := []manifest.State{
		manifest.StateDraft, manifest.StateIngested, manifest.StateIntegrityAssessed,
		manifest.StateProvenanceComplete, manifest.StateReadyForFinalization,
		manifest.StateFinalized, manifest.StateSuperseded,
	}
	for _, ms := range manifestStates {
		if statusValues[Status(ms)] {
			t.Fatalf("manifest.State %q collides with an evidence.Status value -- a caller could confuse one registry's claim for the other's", ms)
		}
	}
	for s := range statusValues {
		if manifest.IsKnownState(manifest.State(s)) {
			t.Fatalf("evidence.Status %q collides with a manifest.State value -- a caller could confuse one registry's claim for the other's", s)
		}
	}
	// The specific claim the reviewer worried about: no evidence.Status
	// value literally reads "FINALIZED". Only manifest.State does.
	if statusValues[Status("FINALIZED")] {
		t.Fatal("evidence.Status must never define a FINALIZED value -- that claim belongs solely to manifest.State")
	}
}

// TestAdvancingOneRegistryNeverChangesTheOther is the behavioral half
// of the same proof: for the SAME EvidenceID, driving manifest.Registry
// all the way to FINALIZED has zero effect on Registry's own Status for
// that ID (it stays exactly what THIS package's own VerifyStatus
// derived, or StatusUnverified if never assessed), and vice versa --
// setting a Strength/Status in this package never advances or mutates
// any manifest.Manifest. The two registries are independent stores,
// each the sole authority for its own axis, wired together only by
// sharing the same EvidenceID string -- never by one reading or
// mutating the other's internal state.
func TestAdvancingOneRegistryNeverChangesTheOther(t *testing.T) {
	const evidenceID = "EV-CANONICAL-1"

	evReg := NewRegistry()
	rec, err := New("CASE-1", mustEvidence(t, evidenceID, "src", 100), "PTY-1", OriginClaimant)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := evReg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	before, _ := evReg.Get(rec.EvidenceID())
	if before.Status != StatusUnverified {
		t.Fatalf("expected the fresh record to start UNVERIFIED, got %v", before.Status)
	}

	manifestReg := manifest.NewRegistry()
	if _, err := manifestReg.RegisterDraft(manifest.Manifest{
		TenantID: "t1", CaseID: "CASE-1", EvidenceID: rec.EvidenceID(), Version: 1,
		URI: "evidence://x.pdf", Filename: "x.pdf", MediaType: "application/pdf",
		ByteSize: 100, SHA256: "sha256:deadbeef", Method: "UPLOAD", Collector: "c", Source: "s",
		AcquiredAt: 1, ReceivedAt: 1, HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "test acquisition",
	}); err != nil {
		t.Fatalf("RegisterDraft: %v", err)
	}
	if _, err := manifestReg.RecordCustodyEvent(rec.EvidenceID(), "evt-1", "actor", manifest.CustodyReceived, 1, "x"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := manifestReg.Advance(rec.EvidenceID(), manifest.StateIngested, 1); err != nil {
		t.Fatalf("Advance to INGESTED: %v", err)
	}
	if _, err := manifestReg.RecordCustodyEvent(rec.EvidenceID(), "evt-2", "actor", manifest.CustodyHashed, 1, "x"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := manifestReg.Advance(rec.EvidenceID(), manifest.StateIntegrityAssessed, 1); err != nil {
		t.Fatalf("Advance to INTEGRITY_ASSESSED: %v", err)
	}
	if _, err := manifestReg.Advance(rec.EvidenceID(), manifest.StateProvenanceComplete, 1); err != nil {
		t.Fatalf("Advance to PROVENANCE_COMPLETE: %v", err)
	}
	if _, err := manifestReg.RecordCustodyEvent(rec.EvidenceID(), "evt-3", "actor", manifest.CustodyReviewed, 1, "x"); err != nil {
		t.Fatalf("RecordCustodyEvent: %v", err)
	}
	if _, err := manifestReg.Advance(rec.EvidenceID(), manifest.StateReadyForFinalization, 1); err != nil {
		t.Fatalf("Advance to READY_FOR_FINALIZATION: %v", err)
	}
	finalized, err := manifestReg.Advance(rec.EvidenceID(), manifest.StateFinalized, 1)
	if err != nil {
		t.Fatalf("Advance to FINALIZED: %v", err)
	}
	if finalized.State != manifest.StateFinalized {
		t.Fatalf("expected the manifest to reach FINALIZED, got %s", finalized.State)
	}

	// The evidence.Registry's own record for the SAME EvidenceID must
	// be completely unaffected -- still exactly StatusUnverified, since
	// nothing in this test ever called SetStrength/VerifyStatus on it.
	after, _ := evReg.Get(rec.EvidenceID())
	if after.Status != StatusUnverified {
		t.Fatalf("evidence.Registry's Status changed after manifest.Registry reached FINALIZED -- split-registry contradiction: got %v, want %v (unchanged)", after.Status, StatusUnverified)
	}

	// And the reverse: deriving a Status in evidence.Registry must
	// never touch the manifest at all.
	if err := evReg.SetStrength(rec.EvidenceID(), Strength{
		Authenticity: AuthenticitySupported, Integrity: IntegrityVerified,
		Completeness: CompletenessComplete, ContradictionLevel: ContradictionLevelNone,
	}); err != nil {
		t.Fatalf("SetStrength: %v", err)
	}
	if _, err := evReg.VerifyStatus(rec.EvidenceID()); err != nil {
		t.Fatalf("VerifyStatus: %v", err)
	}
	stillFinalized, err := manifestReg.Latest(rec.EvidenceID())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if stillFinalized.State != manifest.StateFinalized || stillFinalized.ManifestHash != finalized.ManifestHash {
		t.Fatal("manifest.Registry's own FINALIZED manifest changed after evidence.Registry.VerifyStatus was called -- split-registry contradiction")
	}
}
