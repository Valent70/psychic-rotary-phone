package ontology

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func sampleAIS(source string, tick uint64) Evidence {
	return Evidence{
		Type: TypeAISObservation, Subject: "VESSEL:IMO9998887", Predicate: "AIS_STATUS",
		Object: "OFF", Source: source, ObservedAt: tick, ReceivedAt: tick,
		Confidence: 0.9, Attributes: map[string]string{"mmsi": "123456789"},
		Provenance: Provenance{ProducerID: "vendor-a", UpstreamID: "sat-x"},
	}
}

func TestNewIsContentAddressedAndDeterministic(t *testing.T) {
	a, err := New(sampleAIS("vendor-a", 10))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := New(sampleAIS("vendor-a", 10))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.EvidenceID != b.EvidenceID {
		t.Fatal("identical evidence produced different IDs")
	}
	c := sampleAIS("vendor-a", 10)
	c.Object = "ON"
	cc, _ := New(c)
	if cc.EvidenceID == a.EvidenceID {
		t.Fatal("different object produced the same ID")
	}
}

func TestSchemaVersionIsPartOfIdentity(t *testing.T) {
	a, _ := New(sampleAIS("vendor-a", 10))
	b := sampleAIS("vendor-a", 10)
	b.SchemaVersion = "veriqo.evidence/v2"
	bb, _ := New(b)
	if a.EvidenceID == bb.EvidenceID {
		t.Fatal("schema version is not part of the content hash")
	}
}

func TestRequiredAttributesEnforced(t *testing.T) {
	e := sampleAIS("vendor-a", 10)
	delete(e.Attributes, "mmsi")
	if _, err := New(e); !errors.Is(err, ErrMissingRequiredAttr) {
		t.Fatalf("expected missing-attribute rejection, got %v", err)
	}
}

func TestDerivedEvidenceMustDeclareBasis(t *testing.T) {
	e := Evidence{
		Type: TypeDerivedInference, Subject: "s", Predicate: "p", Object: "o",
		Source: "veriqo", ObservedAt: 1, Confidence: 0.5,
		Attributes: map[string]string{"method": "hbayes"},
	}
	if _, err := New(e); !errors.Is(err, ErrDerivedWithoutBasis) {
		t.Fatalf("expected derived-without-basis rejection, got %v", err)
	}
	e.DerivedFrom = []string{"abc"}
	if _, err := New(e); err != nil {
		t.Fatalf("valid derived evidence rejected: %v", err)
	}
}

func TestPrimaryEvidenceMayNotDeclareBasis(t *testing.T) {
	e := sampleAIS("vendor-a", 10)
	e.DerivedFrom = []string{"abc"}
	if _, err := New(e); !errors.Is(err, ErrPrimaryWithBasis) {
		t.Fatalf("expected primary-with-basis rejection, got %v", err)
	}
}

func TestRequirePrimaryRejectsDerived(t *testing.T) {
	prim, _ := New(sampleAIS("vendor-a", 10))
	der, _ := New(Evidence{
		Type: TypeDerivedInference, Subject: "s", Predicate: "p", Object: "o",
		Source: "veriqo", ObservedAt: 1, Confidence: 0.5, DerivedFrom: []string{prim.EvidenceID},
		Attributes: map[string]string{"method": "hbayes"},
	})
	if err := RequirePrimary([]Evidence{prim}); err != nil {
		t.Fatalf("primary evidence rejected: %v", err)
	}
	if err := RequirePrimary([]Evidence{prim, der}); !errors.Is(err, ErrDerivedTreatedAsPrimary) {
		t.Fatalf("derived evidence accepted as primary: %v", err)
	}
}

func TestReceivedBeforeObservedRejected(t *testing.T) {
	e := sampleAIS("vendor-a", 10)
	e.ReceivedAt = 5
	if _, err := New(e); !errors.Is(err, ErrCausallyImpossible) {
		t.Fatalf("expected causal-impossibility rejection, got %v", err)
	}
}

func TestSignatureRoundTripAndTamperDetection(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	_ = pub
	e, _ := New(sampleAIS("vendor-a", 10))
	signed, err := e.Sign(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := signed.VerifySignature(); err != nil {
		t.Fatalf("signature did not verify: %v", err)
	}
	tampered := signed
	tampered.Object = "ON"
	if err := tampered.VerifySignature(); !errors.Is(err, ErrBadSignature) {
		t.Fatal("tampered evidence still verified")
	}
	unsigned, _ := New(sampleAIS("vendor-b", 10))
	if err := unsigned.VerifySignature(); !errors.Is(err, ErrBadSignature) {
		t.Fatal("unsigned evidence verified by default")
	}
}

func TestBitemporalAsOf(t *testing.T) {
	early, _ := New(Evidence{
		Type: TypeAISObservation, Subject: "v", Predicate: "AIS", Object: "OFF",
		Source: "a", ObservedAt: 10, ReceivedAt: 20, ValidFrom: 10, ValidTo: 100,
		Confidence: 0.9, Attributes: map[string]string{"mmsi": "1"},
	})
	late, _ := New(Evidence{
		Type: TypeAISObservation, Subject: "v", Predicate: "AIS", Object: "ON",
		Source: "b", ObservedAt: 30, ReceivedAt: 200, ValidFrom: 30,
		Confidence: 0.9, Attributes: map[string]string{"mmsi": "2"},
	})
	all := []Evidence{early, late}
	// At transaction tick 50 we had only received `early`.
	got := AsOf(all, 50, 50)
	if len(got) != 1 || got[0].Source != "a" {
		t.Fatalf("bitemporal knowledge filter wrong: %+v", got)
	}
	// At transaction tick 300 we know both, but at valid tick 500
	// `early` has expired.
	got = AsOf(all, 300, 500)
	if len(got) != 1 || got[0].Source != "b" {
		t.Fatalf("valid-time filter wrong: %+v", got)
	}
}

func TestCorrectionSupersedes(t *testing.T) {
	orig, _ := New(sampleAIS("vendor-a", 10))
	corr, err := New(Evidence{
		Type: TypeCorrection, Subject: "VESSEL:IMO9998887", Predicate: "AIS_STATUS",
		Object: "ON", Source: "vendor-a", ObservedAt: 12, ReceivedAt: 12,
		Confidence: 1, Supersedes: orig.EvidenceID,
	})
	if err != nil {
		t.Fatalf("New correction: %v", err)
	}
	out := ApplyCorrections([]Evidence{orig, corr})
	for _, e := range out {
		if e.ComputeID() == orig.EvidenceID {
			t.Fatal("superseded evidence survived correction")
		}
	}
	bad := Evidence{Type: TypeCorrection, Subject: "s", Predicate: "p", Source: "x", ObservedAt: 1}
	if _, err := New(bad); !errors.Is(err, ErrCorrectionWithoutTarget) {
		t.Fatalf("correction without target accepted: %v", err)
	}
}

func TestDependencyProjectionEmitsAllKinds(t *testing.T) {
	basis, _ := New(sampleAIS("vendor-a", 10))
	e, _ := New(Evidence{
		Type: TypeDerivedInference, Subject: "s", Predicate: "p", Object: "o",
		Source: "analyst-1", ObservedAt: 11, Confidence: 0.7,
		DerivedFrom: []string{basis.EvidenceID},
		Attributes:  map[string]string{"method": "m"},
		Provenance:  Provenance{ProducerID: "org-x", UpstreamID: "feed-y", TransformationID: "model-z"},
	})
	recs := DependencyProjection(e)
	if len(recs) != 4 {
		t.Fatalf("expected 4 dependency edges, got %d: %+v", len(recs), recs)
	}
	kinds := map[string]bool{}
	for _, r := range recs {
		kinds[string(r.Kind)] = true
	}
	for _, want := range []string{"SOURCE", "PRODUCER", "TRANSFORMATION", "DERIVATION"} {
		if !kinds[want] {
			t.Fatalf("dependency kind %s not projected", want)
		}
	}
}

func TestSetHashOrderInvariant(t *testing.T) {
	a, _ := New(sampleAIS("vendor-a", 10))
	b, _ := New(sampleAIS("vendor-b", 11))
	if SetHash([]Evidence{a, b}) != SetHash([]Evidence{b, a}) {
		t.Fatal("SetHash is not order-invariant")
	}
	c, _ := New(sampleAIS("vendor-c", 12))
	if SetHash([]Evidence{a, b}) == SetHash([]Evidence{a, b, c}) {
		t.Fatal("SetHash ignored an added member")
	}
}

func TestKnownTypesCoversTheAuditList(t *testing.T) {
	if len(KnownTypes()) != 16 {
		t.Fatalf("expected 16 modelled evidence types, got %d", len(KnownTypes()))
	}
}
