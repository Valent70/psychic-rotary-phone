package version

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/provenance/temporal"
)

var acq = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func root(content []byte) Version {
	obs := acq.Add(-24 * time.Hour)
	return Version{
		ID: "evidenceversion:e1v1", EvidenceID: "evidence:e1", Version: 1,
		SHA256: jcs.HashBytes(content), MediaType: "application/pdf", SizeBytes: int64(len(content)),
		SourceID: "src:surveyor-ltd", ProducerID: "prod:surveyor-ltd", TenantID: "t-acme",
		AcquiredAt: acq, ObservedAt: &obs,
		Mode: PartySubmission, RightsClass: "commercial-restricted",
		Classification: classification.MustNew(classification.Confidential),
		Validity:       temporal.Valid,
		Transform:      Acquisition,
		ProvenanceRef:  "prov:p1", CustodyRef: "custody:c1",
	}
}

func derived(n uint64, parent contract.ID, tr Transform, note string) Version {
	v := root([]byte("derived-" + string(rune('0'+n))))
	v.ID = contract.ID("evidenceversion:e1v" + string(rune('0'+n)))
	v.Version = n
	v.ParentVersion = &parent
	v.Transform = tr
	v.TransformNote = note
	v.Mode = SystemGenerated
	return v
}

// TestTheRawAcquisitionIsNeverSuperseded is Law 9's practical form.
func TestTheRawAcquisitionIsNeverSuperseded(t *testing.T) {
	content := []byte("%PDF-1.4 original bytes")
	c, err := NewChain(root(content))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Derive(derived(2, "evidenceversion:e1v1", Normalization, "PDF 1.5 structure flattened")); err != nil {
		t.Fatal(err)
	}
	if err := c.Derive(derived(3, "evidenceversion:e1v2", Redaction, "one term replaced")); err != nil {
		t.Fatal(err)
	}
	// The root is byte-identical after three versions.
	if err := c.Root().VerifyContent(content); err != nil {
		t.Fatalf("the original bytes no longer verify: %v", err)
	}
	if c.Root().Version != 1 || c.Head().Version != 3 {
		t.Fatalf("chain is v%d..v%d", c.Root().Version, c.Head().Version)
	}
}

// TestVersion1MayNotHaveAParent, and a later version must.
func TestVersion1MayNotHaveAParent(t *testing.T) {
	v := root([]byte("x"))
	p := contract.ID("evidenceversion:e0v9")
	v.ParentVersion = &p
	if err := v.Validate(); !errors.Is(err, ErrRootHasParent) {
		t.Fatalf("an acquisition with a parent was accepted: %v", err)
	}

	d := derived(2, "evidenceversion:e1v1", Normalization, "note")
	d.ParentVersion = nil
	if err := d.Validate(); !errors.Is(err, ErrDerivedNoParent) {
		t.Fatalf("a derivative with no parent was accepted: %v", err)
	}
}

// TestALossyTransformMustSayWhatItDropped. A derivative that omits
// that is presented as equivalent to its parent.
func TestALossyTransformMustSayWhatItDropped(t *testing.T) {
	for _, tr := range []Transform{Normalization, Extraction, OCR, Redaction, Translation} {
		v := derived(2, "evidenceversion:e1v1", tr, "")
		if err := v.Validate(); err == nil {
			t.Errorf("%s was accepted with no statement of what it dropped", tr)
		}
	}
	// A non-lossy transform does not need one.
	if err := derived(2, "evidenceversion:e1v1", Enrichment, "").Validate(); err != nil {
		t.Fatalf("a non-lossy transform was required to state a loss: %v", err)
	}
}

// TestTheChainCollectsEveryLoss. A finding built on v4 rests on
// something normalised, OCR'd and redacted, and the limitations
// section has to say so without the caller walking the chain.
func TestTheChainCollectsEveryLoss(t *testing.T) {
	c, _ := NewChain(root([]byte("x")))
	c.Derive(derived(2, "evidenceversion:e1v1", Normalization, "structure flattened"))
	c.Derive(derived(3, "evidenceversion:e1v2", OCR, "handwriting not recognised"))
	c.Derive(derived(4, "evidenceversion:e1v3", Redaction, "two names replaced"))

	losses := c.Losses()
	if len(losses) != 3 {
		t.Fatalf("Losses() = %v, want three entries", losses)
	}
	joined := strings.Join(losses, " ")
	for _, want := range []string{"structure flattened", "handwriting not recognised", "two names replaced"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Losses() omits %q", want)
		}
	}
	lineage := c.Lineage()
	if len(lineage) != 4 || !strings.Contains(lineage[0], "ACQUISITION") {
		t.Fatalf("Lineage() = %v", lineage)
	}
}

// TestProcessingDoesNotLaunderProvenance is the substantive rule.
//
// A party's submission that VERIQO normalised and OCR'd is still a
// party's submission. An implementation reading the HEAD's mode would
// report SYSTEM_GENERATED and quietly turn an interested party's
// document into a neutral one.
func TestProcessingDoesNotLaunderProvenance(t *testing.T) {
	c, _ := NewChain(root([]byte("x"))) // root mode is PARTY_SUBMISSION
	c.Derive(derived(2, "evidenceversion:e1v1", Normalization, "flattened"))
	c.Derive(derived(3, "evidenceversion:e1v2", OCR, "handwriting lost"))

	if c.Head().Mode != SystemGenerated {
		t.Fatal("premise changed: the head should be SYSTEM_GENERATED")
	}
	if !c.InterestedPartyOrigin() {
		t.Fatal("PROCESSING LAUNDERED PROVENANCE: a party's submission became neutral " +
			"evidence after being normalised")
	}
}

// TestADerivativeCannotBeClassifiedBelowItsParent.
func TestADerivativeCannotBeClassifiedBelowItsParent(t *testing.T) {
	c, _ := NewChain(root([]byte("x"))) // CONFIDENTIAL
	d := derived(2, "evidenceversion:e1v1", Extraction, "tables only")
	d.Classification = classification.MustNew(classification.Public)
	if err := c.Derive(d); !errors.Is(err, classification.ErrDowngrade) {
		t.Fatalf("an extraction from CONFIDENTIAL material was labelled PUBLIC: %v", err)
	}
}

// TestTheEffectiveClassificationIsTheJoin.
func TestTheEffectiveClassificationIsTheJoin(t *testing.T) {
	c, _ := NewChain(root([]byte("x")))
	d := derived(2, "evidenceversion:e1v1", Enrichment, "")
	d.Classification = classification.MustNew(classification.Restricted, classification.PersonalData)
	if err := c.Derive(d); err != nil {
		t.Fatal(err)
	}
	m, err := c.EffectiveClassification()
	if err != nil {
		t.Fatal(err)
	}
	if m.Level != classification.Restricted || !m.Has(classification.PersonalData) {
		t.Fatalf("effective classification = %s", m)
	}
}

// TestTheChainRefusesAGapOrAFork. Two versions claiming the same
// number, or one skipping a number, would make "version 3" ambiguous
// in a citation.
func TestTheChainRefusesAGapOrAFork(t *testing.T) {
	c, _ := NewChain(root([]byte("x")))
	if err := c.Derive(derived(3, "evidenceversion:e1v1", Enrichment, "")); err == nil {
		t.Fatal("a version skipping a number was accepted")
	}
	c.Derive(derived(2, "evidenceversion:e1v1", Enrichment, ""))
	// A second v2, branching from v1 rather than continuing.
	if err := c.Derive(derived(2, "evidenceversion:e1v1", Enrichment, "")); err == nil {
		t.Fatal("a fork was accepted; version 2 would be ambiguous in a citation")
	}
}

// TestADerivativeMustNameTheActualHeadAsItsParent.
func TestADerivativeMustNameTheActualHeadAsItsParent(t *testing.T) {
	c, _ := NewChain(root([]byte("x")))
	c.Derive(derived(2, "evidenceversion:e1v1", Enrichment, ""))
	// v3 naming v1 as its parent: a rebase that would make the OCR
	// step vanish from the lineage.
	if err := c.Derive(derived(3, "evidenceversion:e1v1", Enrichment, "")); err == nil {
		t.Fatal("a derivative naming a stale parent was accepted; a step would vanish " +
			"from the lineage")
	}
}

// TestObservedAtCannotFollowAcquiredAt. Evidence describing a moment
// later than the moment it was collected is either mislabelled or
// fabricated; neither should enter the fabric quietly.
func TestObservedAtCannotFollowAcquiredAt(t *testing.T) {
	v := root([]byte("x"))
	future := acq.Add(time.Hour)
	v.ObservedAt = &future
	if err := v.Validate(); !errors.Is(err, ErrTimeInverted) {
		t.Fatalf("evidence observed after it was acquired was accepted: %v", err)
	}
}

// TestTheRequiredFieldSetIsEnforced.
func TestTheRequiredFieldSetIsEnforced(t *testing.T) {
	cases := map[string]func(*Version){
		"no source":         func(v *Version) { v.SourceID = "" },
		"no rights class":   func(v *Version) { v.RightsClass = "" },
		"no provenance ref": func(v *Version) { v.ProvenanceRef = "" },
		"no custody ref":    func(v *Version) { v.CustodyRef = "" },
		"no classification": func(v *Version) { v.Classification = classification.Marking{} },
		"no tenant":         func(v *Version) { v.TenantID = "" },
		"no hash":           func(v *Version) { v.SHA256 = "" },
		"short hash":        func(v *Version) { v.SHA256 = "abc123" },
		"no acquisition at": func(v *Version) { v.AcquiredAt = time.Time{} },
		"unknown mode":      func(v *Version) { v.Mode = "SOMEHOW" },
		"version zero":      func(v *Version) { v.Version = 0 },
		"unknown transform": func(v *Version) { v.Transform = "MAGIC" },
	}
	for name, mutate := range cases {
		v := root([]byte("x"))
		mutate(&v)
		if err := v.Validate(); err == nil {
			t.Errorf("a version with %s was accepted", name)
		}
	}
}

// TestContentIsVerifiedByHashNotByMetadata.
func TestContentIsVerifiedByHashNotByMetadata(t *testing.T) {
	content := []byte("the real bytes")
	v := root(content)
	if err := v.VerifyContent(content); err != nil {
		t.Fatalf("the genuine content failed verification: %v", err)
	}
	// Same length, different content: a size or media-type check would
	// pass here.
	if err := v.VerifyContent([]byte("the fake bytes")); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("substituted content of the same length verified: %v", err)
	}
}

// TestTheVersionDigestCoversEveryField. If a field were outside the
// digest, it could be edited after the version entered the ledger.
func TestTheVersionDigestCoversEveryField(t *testing.T) {
	v := root([]byte("x"))
	base, err := v.Digest()
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*Version){
		func(v *Version) { v.SourceID = "src:somebody-else" },
		func(v *Version) { v.Mode = APIPull },
		func(v *Version) { v.RightsClass = "public" },
		func(v *Version) { v.Classification = classification.MustNew(classification.Public) },
		func(v *Version) { v.ProvenanceRef = "prov:other" },
		func(v *Version) { v.CustodyRef = "custody:other" },
		func(v *Version) { v.AcquiredAt = acq.Add(time.Second) },
		func(v *Version) { v.Validity = temporal.Expired },
	}
	for i, mutate := range mutations {
		m := root([]byte("x"))
		mutate(&m)
		got, err := m.Digest()
		if err != nil {
			t.Fatalf("mutation %d: %v", i, err)
		}
		if got == base {
			t.Errorf("mutation %d did not change the digest; that field is editable "+
				"after the version is anchored", i)
		}
	}
}

// TestVersionsFromAnotherTenantAreRefused.
func TestVersionsFromAnotherTenantAreRefused(t *testing.T) {
	c, _ := NewChain(root([]byte("x")))
	d := derived(2, "evidenceversion:e1v1", Enrichment, "")
	d.TenantID = "t-beta"
	if err := c.Derive(d); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant version joined the chain: %v", err)
	}
}

// TestAtRetrievesExactlyWhatWasCited. A finding cites a version; a
// reviewer must be able to get that version and no other.
func TestAtRetrievesExactlyWhatWasCited(t *testing.T) {
	c, _ := NewChain(root([]byte("x")))
	c.Derive(derived(2, "evidenceversion:e1v1", Redaction, "one term"))
	v, err := c.At(1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Transform != Acquisition {
		t.Fatalf("At(1) returned a %s", v.Transform)
	}
	if _, err := c.At(0); err == nil {
		t.Fatal("At(0) resolved; version numbering starts at 1")
	}
	if _, err := c.At(99); err == nil {
		t.Fatal("At(99) resolved on a two-version chain")
	}
}
