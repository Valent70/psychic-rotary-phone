package api

import (
	"crypto/ed25519"
	"errors"
	"reflect"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/intelligence/risk"
)

var priv ed25519.PrivateKey

func init() {
	_, p, _ := ed25519.GenerateKey(nil)
	priv = p
}

func ais(source, object, producer, upstream string, conf float64, tick uint64) ontology.Evidence {
	e, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeAISObservation, Subject: "VESSEL:IMO4444444", Predicate: "AIS_STATUS",
		Object: object, Source: source, ObservedAt: tick, Confidence: conf,
		Attributes: map[string]string{"mmsi": "123"},
		Provenance: ontology.Provenance{ProducerID: producer, UpstreamID: upstream},
	})
	if err != nil {
		panic(err)
	}
	signed, err := e.Sign(priv)
	if err != nil {
		panic(err)
	}
	return signed
}

func newFacade() *Facade { return New(nil, nil, DefaultConfig()) }

// TestFacadeExposesNoInternalEngine is the PHASE 3 acceptance
// criterion, checked structurally via reflection: no exported field of
// Facade may hand a caller a fusion/contradiction/dependency engine.
func TestFacadeExposesNoInternalEngine(t *testing.T) {
	tp := reflect.TypeOf(Facade{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.IsExported() {
			t.Fatalf("Facade exports field %s (%s) — a production caller can reach an internal engine", f.Name, f.Type)
		}
	}
}

func TestUnsignedEvidenceRejectedWhenRequired(t *testing.T) {
	f := newFacade()
	unsigned, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeAISObservation, Subject: "s", Predicate: "p", Object: "OFF",
		Source: "a", ObservedAt: 1, Confidence: 0.8, Attributes: map[string]string{"mmsi": "1"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.Submit(unsigned); !errors.Is(err, ErrUnsignedRejected) {
		t.Fatalf("unsigned evidence accepted under RequireSignatures: %v", err)
	}
}

func TestSubmitAutoLinksProvenanceIntoDependencyGraph(t *testing.T) {
	f := newFacade()
	if _, err := f.Submit(ais("vendor-a", "OFF", "sat-op-x", "feed-1", 0.8, 1)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := f.Submit(ais("vendor-b", "OFF", "sat-op-x", "feed-2", 0.8, 2)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	shared, families, err := f.Correlate("VESSEL:IMO4444444", "AIS_STATUS", 5)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	if shared["vendor-a"] <= 0 {
		t.Fatalf("shared producer not linked at ingest: %+v", shared)
	}
	if len(families) != 1 {
		t.Fatalf("two vendors on one producer should be one family, got %d", len(families))
	}
}

func TestArbitrateThenReplayThroughFacade(t *testing.T) {
	f := newFacade()
	if _, err := f.Submit(ais("vendor-a", "OFF", "sat-op-x", "", 0.85, 1)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := f.Submit(ais("port", "ON", "", "", 0.95, 2)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	res, err := f.Arbitrate("actor", "VESSEL:IMO4444444", "AIS_STATUS", risk.DefaultPolicy(), 10)
	if err != nil {
		t.Fatalf("Arbitrate: %v", err)
	}
	if res.Certificate.Hash == "" {
		t.Fatal("no canonical certificate produced")
	}
	cert, err := f.Replay("VESSEL:IMO4444444", "AIS_STATUS", "external-auditor", "nonce-1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("facade replay did not match: %v", err)
	}
	if err := f.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDerivedEvidenceCannotBeArbitratedAsPrimary(t *testing.T) {
	f := newFacade()
	base := ais("vendor-a", "OFF", "", "", 0.8, 1)
	if _, err := f.Submit(base); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	derived, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeDerivedInference, Subject: "VESSEL:IMO4444444", Predicate: "AIS_STATUS",
		Object: "OFF", Source: "veriqo-model", ObservedAt: 2, Confidence: 0.9,
		DerivedFrom: []string{base.EvidenceID}, Attributes: map[string]string{"method": "hbayes"},
	})
	if err != nil {
		t.Fatalf("New derived: %v", err)
	}
	signed, err := derived.Sign(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := f.Submit(signed); err != nil {
		t.Fatalf("Submit derived: %v", err)
	}
	if _, err := f.Arbitrate("actor", "VESSEL:IMO4444444", "AIS_STATUS", risk.DefaultPolicy(), 5); !errors.Is(err, ErrDerivedAsPrimary) {
		t.Fatalf("VERIQO laundered its own inference back in as primary evidence: %v", err)
	}
}

func TestArbitrateWithoutEvidenceOrPolicyRejected(t *testing.T) {
	f := newFacade()
	if _, err := f.Arbitrate("a", "s", "p", risk.DefaultPolicy(), 1); !errors.Is(err, ErrNoEvidence) {
		t.Fatal("arbitrated with no evidence")
	}
	if _, err := f.Replay("s", "p", "auditor", "n"); !errors.Is(err, ErrUnknownClaim) {
		t.Fatal("replayed an unknown claim")
	}
}

func TestManualLinkIsIdempotent(t *testing.T) {
	f := newFacade()
	for i := 0; i < 3; i++ {
		if err := f.Link("vendor-a", "subcontractor-z", evidencegraph.DependsOnProducer, 0.7, 1); err != nil {
			t.Fatalf("Link: %v", err)
		}
	}
}

func TestResolveGoesThroughTheFacade(t *testing.T) {
	f := newFacade()
	if err := f.Identity().RegisterAuthority(identity.Authority{
		SourceID: "flag-registry", Weight: 1, AuthoritativeFor: []identity.Kind{identity.KindIMO},
	}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	if _, err := f.Identity().Merge("op", "flag-registry",
		identity.Identifier{Kind: identity.KindIMO, Value: "4444444"},
		identity.Identifier{Kind: identity.KindCallsign, Value: "ZZZZ"}, 1, "verified"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	res, err := f.Resolve(identity.Identifier{Kind: identity.KindIMO, Value: "4444444"}, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EntityID == "" || res.Confidence <= 0 {
		t.Fatalf("resolution empty: %+v", res)
	}
}
