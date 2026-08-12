package ontology

import "testing"

// FuzzEvidenceValidation (PHASE 38) drives the ontology's parser and
// validator with arbitrary field content. The invariant under test is
// not "never errors" — most inputs SHOULD error — it is that New()
// never panics and never returns a record whose declared ID disagrees
// with its own content.
func FuzzEvidenceValidation(f *testing.F) {
	f.Add("AISObservation", "s", "p", "o", "src", uint64(1), uint64(2), 0.5, "mmsi", "1")
	f.Add("Correction", "", "p", "", "", uint64(9), uint64(1), 2.0, "", "")
	f.Add("DerivedInference", "s", "p", "o", "veriqo", uint64(0), uint64(0), 1.0, "method", "m")
	f.Fuzz(func(t *testing.T, typ, subj, pred, obj, src string, observed, received uint64, conf float64, ak, av string) {
		e := Evidence{
			Type: Type(typ), Subject: subj, Predicate: pred, Object: obj, Source: src,
			ObservedAt: observed, ReceivedAt: received, Confidence: conf,
			Attributes: map[string]string{ak: av},
		}
		got, err := New(e)
		if err != nil {
			return
		}
		if got.EvidenceID != got.ComputeID() {
			t.Fatalf("accepted evidence whose ID does not match its content")
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("New returned a record its own Validate rejects: %v", err)
		}
	})
}

// FuzzSetHashStability asserts the evidence-set commitment is stable
// and order-invariant for any accepted set.
func FuzzSetHashStability(f *testing.F) {
	f.Add("a", "b", uint64(1), uint64(2))
	f.Fuzz(func(t *testing.T, s1, s2 string, t1, t2 uint64) {
		mk := func(src string, tick uint64) (Evidence, bool) {
			e, err := New(Evidence{Type: TypeObservation, Subject: "s", Predicate: "p",
				Object: "o", Source: src, ObservedAt: tick, Confidence: 0.5})
			return e, err == nil
		}
		a, ok1 := mk(s1, t1)
		b, ok2 := mk(s2, t2)
		if !ok1 || !ok2 {
			return
		}
		if SetHash([]Evidence{a, b}) != SetHash([]Evidence{b, a}) {
			t.Fatal("SetHash is not order-invariant")
		}
	})
}
