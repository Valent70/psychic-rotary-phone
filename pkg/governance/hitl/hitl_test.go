package hitl

import (
	"errors"
	"testing"
)

func machine(action string, conf, risk float64) MachineDecision {
	return MachineDecision{DecisionID: "d1", ExecutionID: "x1", Subject: "IMO9999999",
		Action: action, Confidence: conf, RiskScore: risk, RiskLabel: "HIGH",
		ExplanationID: "ex1", EvidenceRoot: "ev1", ExecutionRoot: "xr1",
		PolicyVersion: "p1", ModelVersions: []string{"risk@2"}, SourceVersions: []string{"ais@1"}}
}

func packet() ReviewerPacket {
	return ReviewerPacket{
		Evidence:       []string{"e1", "e2"},
		Contradictions: []string{"c1"},
		Dependencies:   []string{"dep1"},
		CausalSummary:  "AIS gap causes elevated dark-activity risk",
		RiskSummary:    "0.82 HIGH",
		TrustSummary:   "entity trust DEGRADED",
		PolicySummary:  "sanctions-screen v1 flagged",
		Alternatives:   []string{"MONITOR (rejected: risk above threshold)"},
		Uncertainty:    []string{"SAR coverage gap 6h"},
	}
}

func toUnderReview(t *testing.T, e *Engine) *Engine {
	t.Helper()
	if _, err := e.Submit(machine("FLAG", 0.6, 0.82), packet(), "analyst", 5, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Assign("d1", "reviewer-1", "supervisor", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Open("d1", "reviewer-1", 3); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLowConfidenceRoutesToAHuman(t *testing.T) {
	e := New(DefaultReviewRule())
	c, err := e.Submit(machine("FLAG", 0.6, 0.4), packet(), "analyst", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != StateRequiresReview {
		t.Fatalf("expected REQUIRES_REVIEW, got %s", c.State)
	}
	if c.DeadlineTick != 1+DefaultReviewRule().SLATicks {
		t.Fatalf("an SLA deadline must be set, got %d", c.DeadlineTick)
	}
}

func TestHighConfidenceLowRiskAutoExecutesWithTheSameShape(t *testing.T) {
	e := New(DefaultReviewRule())
	c, err := e.Submit(machine("MONITOR", 0.95, 0.1), ReviewerPacket{}, "analyst", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != StateExecuted {
		t.Fatalf("expected auto execution, got %s", c.State)
	}
	if c.Outcome == nil || c.Outcome.Human.Reviewer != "auto" || c.Outcome.Overridden {
		t.Fatalf("an auto-executed case must still carry a governed outcome: %+v", c.Outcome)
	}
}

func TestAlwaysReviewedActionOverridesHighConfidence(t *testing.T) {
	e := New(DefaultReviewRule())
	c, err := e.Submit(machine("ESCALATE", 0.99, 0.01), packet(), "analyst", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != StateRequiresReview {
		t.Fatalf("an always-reviewed action must never auto-execute, got %s", c.State)
	}
}

func TestIncompletePacketIsRefused(t *testing.T) {
	e := New(DefaultReviewRule())
	p := packet()
	p.Alternatives = nil
	p.CausalSummary = ""
	_, err := e.Submit(machine("FLAG", 0.5, 0.9), p, "analyst", 1, 1)
	if !errors.Is(err, ErrPacketIncomplete) {
		t.Fatalf("a rubber-stamp packet must be refused, got %v", err)
	}
}

func TestSelfReviewRefused(t *testing.T) {
	e := New(DefaultReviewRule())
	_, _ = e.Submit(machine("FLAG", 0.5, 0.9), packet(), "analyst", 1, 1)
	if _, err := e.Assign("d1", "analyst", "supervisor", 2); !errors.Is(err, ErrSelfReview) {
		t.Fatalf("the requester must not review their own case, got %v", err)
	}
}

func TestOnlyTheAssigneeMayActOnACase(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	if _, err := e.Act("d1", "reviewer-2", ActionApprove, "", "looks fine", nil, 4); !errors.Is(err, ErrNotAssigned) {
		t.Fatalf("a non-assignee must not act, got %v", err)
	}
	if _, err := e.Open("d1", "reviewer-2", 4); err == nil {
		t.Fatal("a non-assignee must not open the case")
	}
}

func TestRationaleIsMandatory(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	if _, err := e.Act("d1", "reviewer-1", ActionReject, "", "   ", nil, 4); !errors.Is(err, ErrMissingRationale) {
		t.Fatalf("expected rationale requirement, got %v", err)
	}
}

func TestHumanOverrideDoesNotMutateTheMachineDecision(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	before, _ := e.Case("d1")
	machineHash := before.Machine.Hash

	if _, err := e.Act("d1", "reviewer-1", ActionApprove, "MONITOR",
		"vessel identity confirmed by registry; downgrade", []string{"e9"}, 4); err != nil {
		t.Fatal(err)
	}
	out, err := e.Execute("d1", "reviewer-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if out.Machine.Action != "FLAG" {
		t.Fatalf("the machine action must survive the override, got %s", out.Machine.Action)
	}
	if out.Machine.Hash != machineHash {
		t.Fatal("the machine decision hash must be unchanged by human review")
	}
	if out.Effective != "MONITOR" || !out.Overridden {
		t.Fatalf("the effective action must be the human's: %+v", out)
	}
	if out.Human.Rationale == "" || out.Human.Reviewer != "reviewer-1" {
		t.Fatalf("the human decision must be preserved separately: %+v", out.Human)
	}
}

func TestApprovalWithoutOverrideKeepsTheMachineAction(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	_, _ = e.Act("d1", "reviewer-1", ActionApprove, "", "concur with the model", nil, 4)
	out, err := e.Execute("d1", "reviewer-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if out.Effective != "FLAG" || out.Overridden {
		t.Fatalf("an unqualified approval must not count as an override: %+v", out)
	}
}

func TestRejectedCaseIsNeverExecuted(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	if _, err := e.Act("d1", "reviewer-1", ActionReject, "", "insufficient evidence", nil, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute("d1", "reviewer-1", 5); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("a rejected case must be terminal, got %v", err)
	}
}

func TestRequestMoreEvidenceReopensWithANewPacketHash(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	before, _ := e.Case("d1")
	if _, err := e.Act("d1", "reviewer-1", ActionRequestMoreEvidence, "",
		"need SAR corroboration", nil, 4); err != nil {
		t.Fatal(err)
	}
	c, err := e.SupplyEvidence("d1", "analyst", []string{"sar-77"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != StateUnderReview {
		t.Fatalf("supplying evidence must return the case to review, got %s", c.State)
	}
	if c.PacketHash == before.PacketHash {
		t.Fatal("a changed packet must change the packet hash")
	}
	if _, err := e.Act("d1", "reviewer-1", ActionApprove, "", "corroborated", nil, 6); err != nil {
		t.Fatal(err)
	}
}

func TestEscalationMustReachADifferentReviewer(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	if _, err := e.Act("d1", "reviewer-1", ActionEscalate, "senior-desk",
		"sanctions nexus beyond my authority", nil, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Assign("d1", "reviewer-1", "supervisor", 5); !errors.Is(err, ErrSelfReview) {
		t.Fatalf("escalation back to the same reviewer must be refused, got %v", err)
	}
	if _, err := e.Assign("d1", "reviewer-2", "supervisor", 6); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Open("d1", "reviewer-2", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Act("d1", "reviewer-2", ActionApprove, "", "confirmed at senior desk", nil, 8); err != nil {
		t.Fatal(err)
	}
}

func TestDeferReturnsTheCaseToTheQueue(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	if _, err := e.Act("d1", "reviewer-1", ActionDefer, "", "awaiting shift handover", nil, 4); err != nil {
		t.Fatal(err)
	}
	q := e.Queue()
	if len(q) != 1 || q[0].State != StateDeferred {
		t.Fatalf("a deferred case must stay in the queue: %+v", q)
	}
	if _, err := e.Assign("d1", "reviewer-2", "supervisor", 5); err == nil {
		t.Fatal("a deferred case must be re-queued before reassignment")
	}
}

func TestSLABreachIsRecordedNotSilentlyTolerated(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	late := uint64(1 + DefaultReviewRule().SLATicks + 10)
	if b := e.Breaches(late); len(b) != 1 {
		t.Fatalf("expected one SLA breach, got %d", len(b))
	}
	if _, err := e.Act("d1", "reviewer-1", ActionApprove, "", "late but correct", nil, late); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, en := range e.Ledger() {
		if en.CaseID == "d1" && contains(en.Detail, "SLA breached") {
			found = true
		}
	}
	if !found {
		t.Fatal("an SLA breach must appear in the immutable ledger")
	}
}

func TestQueueOrderingIsDeterministic(t *testing.T) {
	e := New(DefaultReviewRule())
	for i, id := range []string{"a", "b", "c"} {
		m := machine("FLAG", 0.5, 0.9)
		m.DecisionID = id
		if _, err := e.Submit(m, packet(), "analyst", i, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	first := e.Queue()
	if len(first) != 3 || first[0].ID != "c" {
		t.Fatalf("highest priority must come first: %v", ids(first))
	}
	for i := 0; i < 10; i++ {
		if got := ids(e.Queue()); got != ids(first) {
			t.Fatalf("queue ordering must be stable: %s vs %s", got, ids(first))
		}
	}
}

func TestOverrideRateIsMeasurable(t *testing.T) {
	e := New(DefaultReviewRule())
	for i, id := range []string{"a", "b", "c", "d"} {
		m := machine("FLAG", 0.5, 0.9)
		m.DecisionID = id
		tick := uint64(10 * (i + 1))
		if _, err := e.Submit(m, packet(), "analyst", 1, tick); err != nil {
			t.Fatal(err)
		}
		_, _ = e.Assign(id, "reviewer-1", "sup", tick+1)
		_, _ = e.Open(id, "reviewer-1", tick+2)
		override := ""
		if i < 1 {
			override = "MONITOR"
		}
		if _, err := e.Act(id, "reviewer-1", ActionApprove, override, "r", nil, tick+3); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Execute(id, "reviewer-1", tick+4); err != nil {
			t.Fatal(err)
		}
	}
	rate, executed := e.OverrideRate()
	if executed != 4 {
		t.Fatalf("expected 4 executed cases, got %d", executed)
	}
	if rate != 0.25 {
		t.Fatalf("expected a 0.25 override rate, got %v", rate)
	}
}

func TestDuplicateSubmissionRejected(t *testing.T) {
	e := New(DefaultReviewRule())
	_, _ = e.Submit(machine("FLAG", 0.5, 0.9), packet(), "analyst", 1, 1)
	if _, err := e.Submit(machine("FLAG", 0.5, 0.9), packet(), "analyst", 1, 2); !errors.Is(err, ErrDuplicateCase) {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}

func TestBackdatedWorkflowEventRejected(t *testing.T) {
	e := New(DefaultReviewRule())
	_, _ = e.Submit(machine("FLAG", 0.5, 0.9), packet(), "analyst", 1, 100)
	if _, err := e.Assign("d1", "reviewer-1", "sup", 50); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("expected monotonic tick rejection, got %v", err)
	}
}

func TestLedgerIsTamperEvidentAndExplains(t *testing.T) {
	e := toUnderReview(t, New(DefaultReviewRule()))
	_, _ = e.Act("d1", "reviewer-1", ActionApprove, "", "ok", nil, 4)
	_, _ = e.Execute("d1", "reviewer-1", 5)
	if err := e.VerifyChain(); err != nil {
		t.Fatal(err)
	}
	lines, err := e.Explain("d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 5 {
		t.Fatalf("the explanation must cover the whole workflow, got %v", lines)
	}
	e.mu.Lock()
	e.ledger[3].Actor = "impostor"
	e.mu.Unlock()
	if err := e.VerifyChain(); !errors.Is(err, ErrLedgerTampered) {
		t.Fatalf("expected tamper detection, got %v", err)
	}
}

func TestUnknownCaseIsAnError(t *testing.T) {
	e := New(DefaultReviewRule())
	if _, err := e.Assign("ghost", "r", "s", 1); !errors.Is(err, ErrUnknownCase) {
		t.Fatalf("expected unknown case, got %v", err)
	}
	if _, err := e.Execute("ghost", "r", 2); !errors.Is(err, ErrUnknownCase) {
		t.Fatalf("expected unknown case, got %v", err)
	}
}

func ids(cs []Case) string {
	s := ""
	for _, c := range cs {
		s += c.ID + ","
	}
	return s
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
