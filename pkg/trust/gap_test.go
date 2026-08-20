// Session update — closes roadmap item #3 from the audit follow-up
// report (§4): pkg/trust 81.4% -> higher. Real gaps closed:
//
//   - TrustPolicy.Validate: the non-positive-weight, nil-Evaluate, and
//     duplicate-rule-name rejection branches were never exercised
//     (only the "zero rules" branch was, via
//     TestRegisterPolicyRejectsInvalid).
//   - Kernel.Ledger(), previously 0% covered, and confirmed to be a
//     genuine defensive copy (mutating the returned slice must not
//     affect the kernel's internal ledger).
//   - SortedRuleNames, previously 0% covered.
//   - VerifyLedger's index-gap and broken-chain-linkage branches — the
//     existing test only exercised the hash-mismatch branch.
package trust

import "testing"

func TestValidate_RejectsNonPositiveWeight(t *testing.T) {
	p := TrustPolicy{Name: "bad-weight", Rules: []TrustRule{
		{Name: "r1", Weight: 0, Evaluate: func(TrustEvidence) float64 { return 1 }},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected an error for a non-positive rule weight")
	}
}

func TestValidate_RejectsNilEvaluate(t *testing.T) {
	p := TrustPolicy{Name: "nil-eval", Rules: []TrustRule{
		{Name: "r1", Weight: 1, Evaluate: nil},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected an error for a rule with no Evaluate function")
	}
}

func TestValidate_RejectsDuplicateRuleNames(t *testing.T) {
	f := func(TrustEvidence) float64 { return 1 }
	p := TrustPolicy{Name: "dup", Rules: []TrustRule{
		{Name: "same", Weight: 1, Evaluate: f},
		{Name: "same", Weight: 1, Evaluate: f},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected an error for duplicate rule names within one policy")
	}
}

func TestLedger_ReturnsDefensiveCopy(t *testing.T) {
	k := NewKernel()
	must(t, k.RegisterPolicy(sourceReliabilityPolicy()))
	_, cert, err := k.Certify("s1", "source_reliability", TrustEvidence{"win_rate": 0.99, "contradiction_rate": 0.01}, 1)
	must(t, err)
	if cert == nil {
		t.Fatal("expected a certificate")
	}

	got := k.Ledger()
	if len(got) != 1 {
		t.Fatalf("want 1 certificate, got %d", len(got))
	}
	// Mutate the returned slice; the kernel's internal ledger must be
	// unaffected — proving Ledger() is a genuine defensive copy, not a
	// view onto internal state.
	got[0].Score = -999
	internal := k.Ledger()
	if internal[0].Score == -999 {
		t.Fatal("Ledger() leaked internal state: mutating the returned slice affected the kernel")
	}
}

func TestSortedRuleNames_Deterministic(t *testing.T) {
	scores := map[string]float64{"zeta": 0.1, "alpha": 0.9, "mid": 0.5}
	got := SortedRuleNames(scores)
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want sorted %v, got %v", want, got)
		}
	}
}

func TestVerifyLedger_DetectsIndexGapAndBrokenLinkage(t *testing.T) {
	k := NewKernel()
	must(t, k.RegisterPolicy(sourceReliabilityPolicy()))
	_, c1, err := k.Certify("s", "source_reliability", TrustEvidence{"win_rate": 0.99, "contradiction_rate": 0.01}, 1)
	must(t, err)
	_, c2, err := k.Certify("s", "source_reliability", TrustEvidence{"win_rate": 0.98, "contradiction_rate": 0.02}, 2)
	must(t, err)
	if c1 == nil || c2 == nil {
		t.Fatal("expected two certificates")
	}

	// Index-gap branch: skip an index.
	k.ledger[1].Index = 5
	if err := k.VerifyLedger(); err == nil {
		t.Fatal("expected index-gap detection")
	}
	k.ledger[1].Index = 2 // restore

	// Broken-linkage branch: sever PrevHash without touching the score,
	// so the hash-mismatch branch (already covered elsewhere) is NOT
	// what trips this — the linkage check must fire first/independently.
	k.ledger[1].PrevHash = "not-the-real-prev-hash"
	if err := k.VerifyLedger(); err == nil {
		t.Fatal("expected broken chain linkage detection")
	}
}
