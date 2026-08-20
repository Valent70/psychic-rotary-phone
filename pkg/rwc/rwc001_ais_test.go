package rwc

import (
	"context"
	"testing"

	"veriqo/pkg/maritime"
	"veriqo/veriqo/kernel"
)

// TestBuildRWC001CaseWithAISMatchesHardcodedTick proves AISAdapter is a
// real, structurally-wired input to RWC-001 case construction, not an
// adapter sitting unused next to the case: BuildRWC001CaseWithAIS
// fetches its Tick from maritime.AISAdapter's fixture instead of the
// literal 1 every other RWC-001 test/call site hardcodes, and this
// asserts the fetched tick is that exact value -- so swapping the
// source changes only where the number structurally comes from.
func TestBuildRWC001CaseWithAISMatchesHardcodedTick(t *testing.T) {
	candA := RWC001Candidates()[0]
	if candA.ID != "A" {
		t.Fatalf("expected RWC001Candidates()[0] to be candidate A, got %s", candA.ID)
	}

	ais := maritime.NewAISAdapterFixture()
	req, cr, err := BuildRWC001CaseWithAIS(context.Background(), candA, ais)
	if err != nil {
		t.Fatalf("BuildRWC001CaseWithAIS: %v", err)
	}
	if req.Tick != 1 {
		t.Fatalf("expected AIS-sourced tick to equal the hardcoded tick=1 every other RWC-001 call site uses, got %d", req.Tick)
	}

	wantReq, wantCR, err := BuildRWC001Case(candA, 1)
	if err != nil {
		t.Fatalf("BuildRWC001Case: %v", err)
	}
	if req.Tick != wantReq.Tick || req.Predicate != wantReq.Predicate || req.PatternScore != wantReq.PatternScore ||
		req.PriceAnomaly != wantReq.PriceAnomaly || len(req.Submissions) != len(wantReq.Submissions) ||
		req.Submissions[0].Value != wantReq.Submissions[0].Value {
		t.Fatalf("AIS-sourced CaseRequest diverges from the hardcoded-tick CaseRequest:\n  got:  %+v\n  want: %+v", req, wantReq)
	}
	if len(cr.HardViolations) != len(wantCR.HardViolations) || len(cr.Unresolved) != len(wantCR.Unresolved) {
		t.Fatalf("AIS-sourced ConstraintResult diverges: got %+v want %+v", cr, wantCR)
	}
}

// TestRWC001CandidateAVerdictUnchangedWithAISWiring runs candidate A
// through the REAL native kernel path twice -- once via the existing
// hardcoded-tick BuildRWC001Case (exactly what
// TestRWC001AdversarialCandidates in rwc001_test.go already exercises),
// once via the new AIS-sourced BuildRWC001CaseWithAIS -- and asserts
// the emergent verdict is identical either way. This is the proof
// required before wiring AISAdapter in: the adapter is a genuine
// pluggable input to the SAME final result, not a change to what the
// case decides.
func TestRWC001CandidateAVerdictUnchangedWithAISWiring(t *testing.T) {
	candA := RWC001Candidates()[0]

	runHardcoded := func(t *testing.T) (Verdict, *CaseResult) {
		k, err := kernel.New()
		if err != nil {
			t.Fatalf("kernel.New: %v", err)
		}
		defer k.Shutdown()
		req, cr, err := BuildRWC001Case(candA, 1)
		if err != nil {
			t.Fatalf("BuildRWC001Case: %v", err)
		}
		res, err := Run(context.Background(), k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		verdict, warn := InterpretVerdict(cr, res.Lifecycle.Canonical.Decision)
		if warn != "" {
			t.Errorf("consistency warning (hardcoded path): %s", warn)
		}
		return verdict, res
	}

	runAIS := func(t *testing.T) (Verdict, *CaseResult) {
		k, err := kernel.New()
		if err != nil {
			t.Fatalf("kernel.New: %v", err)
		}
		defer k.Shutdown()
		ais := maritime.NewAISAdapterFixture()
		req, cr, err := BuildRWC001CaseWithAIS(context.Background(), candA, ais)
		if err != nil {
			t.Fatalf("BuildRWC001CaseWithAIS: %v", err)
		}
		res, err := Run(context.Background(), k, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		verdict, warn := InterpretVerdict(cr, res.Lifecycle.Canonical.Decision)
		if warn != "" {
			t.Errorf("consistency warning (AIS-sourced path): %s", warn)
		}
		return verdict, res
	}

	wantVerdict, wantRes := runHardcoded(t)
	if wantVerdict != VerdictPass {
		t.Fatalf("sanity check failed: candidate A is expected PASS per rwc001_test.go, got %s", wantVerdict)
	}

	gotVerdict, gotRes := runAIS(t)
	if gotVerdict != wantVerdict {
		t.Fatalf("verdict changed after wiring AISAdapter in: got %s, want %s (unchanged)", gotVerdict, wantVerdict)
	}
	if gotRes.DecisionAction != wantRes.DecisionAction {
		t.Fatalf("decision action changed after wiring AISAdapter in: got %s, want %s", gotRes.DecisionAction, wantRes.DecisionAction)
	}
	if gotRes.RiskScore != wantRes.RiskScore {
		t.Fatalf("risk score changed after wiring AISAdapter in: got %v, want %v", gotRes.RiskScore, wantRes.RiskScore)
	}
}

var _ maritime.MaritimeEvidenceSource = (*maritime.AISAdapter)(nil)
