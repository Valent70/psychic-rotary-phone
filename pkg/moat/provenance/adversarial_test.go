package provenance

import "testing"

// TestAdversarial_TenSourcesSameUpstream is the exact scenario the
// v7.9 audit's §24 "Provenance adversarial" asks for: 10 source IDs
// all resolving to one shared upstream root should compute independence
// approaching zero, not the caller-asserted 1.0 an attacker could
// previously supply directly to risk.CompositeInput.
func TestAdversarial_TenSourcesSameUpstream(t *testing.T) {
	p := New()
	var ids []string
	for i := 0; i < 10; i++ {
		id := "SOURCE_" + string(rune('A'+i))
		ids = append(ids, id)
		if err := p.Declare(id, "SHARED_ROOT"); err != nil {
			t.Fatalf("declare: %v", err)
		}
	}
	res, err := p.ComputeIndependence(ids)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Ratio > 0.01 {
		t.Fatalf("expected independence ~0 for 10 sources sharing one root, got %v", res.Ratio)
	}
	if res.PairCount != 45 { // C(10,2)
		t.Errorf("expected 45 pairs, got %d", res.PairCount)
	}
}

// TestAdversarial_MultiHopSharedAncestry proves the P0 fix: A->B->C and
// D->C share root C two hops apart through DIFFERENT immediate parents
// — the exact case v7.8's audit said the prior one-hop mechanism missed.
func TestAdversarial_MultiHopSharedAncestry(t *testing.T) {
	p := New()
	must(t, p.Declare("A", "B"))
	must(t, p.Declare("B", "C"))
	must(t, p.Declare("D", "C"))

	shared, err := p.PairShared("A", "D")
	if err != nil {
		t.Fatalf("pairshared: %v", err)
	}
	if !shared {
		t.Fatalf("expected A and D to share ancestry via C (multi-hop), got independent")
	}
}

// TestAdversarial_AttackerCannotAssertIndependence proves the exact
// v7.9 §4 attack: a caller/attacker who sends the SAME upstream for
// every source cannot make the computed ratio report full independence
// simply by naming the sources differently.
func TestAdversarial_AttackerCannotAssertIndependence(t *testing.T) {
	p := New()
	must(t, p.Declare("AIS_VENDOR_A", "SATELLITE_OPERATOR_X"))
	must(t, p.Declare("AIS_VENDOR_B", "SATELLITE_OPERATOR_X_RESELLER"))
	must(t, p.Declare("SATELLITE_OPERATOR_X_RESELLER", "SATELLITE_OPERATOR_X"))

	res, err := p.ComputeIndependence([]string{"AIS_VENDOR_A", "AIS_VENDOR_B"})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Ratio != 0.0 {
		t.Fatalf("expected 0 independence for reseller chain sharing root, got %v", res.Ratio)
	}
}

// TestIndependentSourcesComputeFullIndependence is the control case:
// genuinely undeclared/unrelated sources should compute to full
// independence, not be penalized for having no provenance data.
func TestIndependentSourcesComputeFullIndependence(t *testing.T) {
	p := New()
	res, err := p.ComputeIndependence([]string{"UNRELATED_A", "UNRELATED_B", "UNRELATED_C"})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Ratio != 1.0 {
		t.Fatalf("expected full independence for undeclared sources, got %v", res.Ratio)
	}
}

// TestSingleSourceTriviallyIndependent guards the zero-pair edge case.
func TestSingleSourceTriviallyIndependent(t *testing.T) {
	p := New()
	res, err := p.ComputeIndependence([]string{"ONLY_ONE"})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Ratio != 1.0 || res.PairCount != 0 {
		t.Fatalf("expected trivial full independence with 0 pairs, got %+v", res)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAssess_StatusDistinguishesUnknownFromDeclaredIndependent covers
// the auditor's explicit "declared vs verified" epistemic-honesty ask:
// sources with NO declared ancestry at all must read UNKNOWN, not be
// silently indistinguishable from sources that were checked and found
// independent.
func TestAssess_StatusDistinguishesUnknownFromDeclaredIndependent(t *testing.T) {
	p := New()
	unknown, err := p.Assess([]string{"NEVER_DECLARED_A", "NEVER_DECLARED_B"})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if unknown.Status != StatusUnknown {
		t.Errorf("expected StatusUnknown for undeclared sources, got %s", unknown.Status)
	}

	must(t, p.Declare("X", "ROOT1"))
	must(t, p.Declare("Y", "ROOT2"))
	declared, err := p.Assess([]string{"X", "Y"})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if declared.Status != StatusDeclaredIndependent {
		t.Errorf("expected StatusDeclaredIndependent for declared-and-independent sources, got %s", declared.Status)
	}

	must(t, p.Declare("Z", "ROOT1")) // shares ROOT1 with X
	shared, err := p.Assess([]string{"X", "Z"})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if shared.Status != StatusDeclaredSharedOrigin {
		t.Errorf("expected StatusDeclaredSharedOrigin, got %s", shared.Status)
	}
	if len(shared.SharedAncestors) == 0 || shared.SharedAncestors[0] != "ROOT1" {
		t.Errorf("expected SharedAncestors=[ROOT1], got %v", shared.SharedAncestors)
	}
}
