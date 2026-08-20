package digitaltwin

import (
	"errors"
	"testing"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
)

func mustTwin(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyArbitrationCreatesTwinAndAttribute(t *testing.T) {
	r := NewRegistry()
	res := fusion.ArbitrationResult{
		Claim:            fusion.Claim{Subject: "VESSEL:IMO1", Predicate: "FLAG_STATE"},
		Winner:           "PANAMA",
		WinnerConfidence: 0.9,
	}
	mustTwin(t, r.ApplyArbitration("actor-1", "VESSEL:IMO1", res, 5))

	twin, ok := r.Get("VESSEL:IMO1")
	if !ok {
		t.Fatalf("expected twin to exist after first update")
	}
	attr, ok := twin.Attributes["FLAG_STATE"]
	if !ok || attr.Value != "PANAMA" || attr.Confidence != 0.9 || attr.UpdatedAtTick != 5 {
		t.Fatalf("unexpected attribute state: %+v ok=%v", attr, ok)
	}
}

func TestApplyDecisionUpdatesRiskState(t *testing.T) {
	r := NewRegistry()
	d := decision.Decision{Subject: "VESSEL:IMO1", PolicyName: "dark_vessel_risk", RiskScore: 0.92, Action: decision.ActionEscalate}
	mustTwin(t, r.ApplyDecision("actor-1", "VESSEL:IMO1", d, 20))

	twin, ok := r.Get("VESSEL:IMO1")
	if !ok {
		t.Fatalf("expected twin to exist")
	}
	risk, ok := twin.Risk["dark_vessel_risk"]
	if !ok || risk.RiskScore != 0.92 || risk.Action != decision.ActionEscalate {
		t.Fatalf("unexpected risk state: %+v ok=%v", risk, ok)
	}
}

func TestLaterUpdateOverwritesAttribute(t *testing.T) {
	r := NewRegistry()
	claim := fusion.Claim{Subject: "V1", Predicate: "FLAG_STATE"}
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claim, Winner: "PANAMA", WinnerConfidence: 0.6}, 1))
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claim, Winner: "LIBERIA", WinnerConfidence: 0.95}, 2))

	twin, _ := r.Get("V1")
	if twin.Attributes["FLAG_STATE"].Value != "LIBERIA" {
		t.Fatalf("expected latest arbitration to win, got %+v", twin.Attributes["FLAG_STATE"])
	}
}

func TestGetReturnsDefensiveCopy(t *testing.T) {
	r := NewRegistry()
	claim := fusion.Claim{Subject: "V1", Predicate: "FLAG"}
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claim, Winner: "PANAMA", WinnerConfidence: 0.5}, 1))

	twin, _ := r.Get("V1")
	twin.Attributes["FLAG"] = AttributeState{Value: "MUTATED"}

	twin2, _ := r.Get("V1")
	if twin2.Attributes["FLAG"].Value == "MUTATED" {
		t.Fatalf("Get leaked internal state via returned map")
	}
}

func TestTimelineOrderedAndScopedToEntity(t *testing.T) {
	r := NewRegistry()
	claimA := fusion.Claim{Subject: "V1", Predicate: "FLAG"}
	claimB := fusion.Claim{Subject: "V2", Predicate: "FLAG"}
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claimA, Winner: "X", WinnerConfidence: 0.5}, 1))
	mustTwin(t, r.ApplyArbitration("a", "V2", fusion.ArbitrationResult{Claim: claimB, Winner: "Y", WinnerConfidence: 0.5}, 2))
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claimA, Winner: "Z", WinnerConfidence: 0.5}, 3))

	tl := r.Timeline("V1")
	if len(tl) != 2 || tl[0].Value != "X" || tl[1].Value != "Z" {
		t.Fatalf("unexpected timeline for V1: %+v", tl)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	r := NewRegistry()
	claim := fusion.Claim{Subject: "V1", Predicate: "FLAG"}
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claim, Winner: "X", WinnerConfidence: 0.5}, 1))
	if err := r.VerifyChain(); err != nil {
		t.Fatalf("untampered chain failed verify: %v", err)
	}
	r.log[0].Value = "TAMPERED"
	if err := r.VerifyChain(); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected, got %v", err)
	}
}

// TestDigitalTwinMaintainsConsistentStateAcrossReplay is the exact test
// Yang_masih_kurang.docx names by name (§4.3): twin state must be fully
// reconstructable from the update log alone.
func TestDigitalTwinMaintainsConsistentStateAcrossReplay(t *testing.T) {
	r := NewRegistry()
	claim := fusion.Claim{Subject: "VESSEL:IMO1", Predicate: "FLAG_STATE"}
	mustTwin(t, r.ApplyArbitration("a", "VESSEL:IMO1", fusion.ArbitrationResult{Claim: claim, Winner: "PANAMA", WinnerConfidence: 0.7}, 1))
	mustTwin(t, r.ApplyDecision("a", "VESSEL:IMO1", decision.Decision{PolicyName: "risk", RiskScore: 0.4, Action: decision.ActionFlag}, 2))
	mustTwin(t, r.ApplyArbitration("a", "VESSEL:IMO1", fusion.ArbitrationResult{Claim: claim, Winner: "LIBERIA", WinnerConfidence: 0.95}, 3))

	rebuilt, err := Rebuild(r.ReplayAll())
	mustTwin(t, err)

	if rebuilt.Head() != r.Head() {
		t.Fatalf("rebuilt head %s != original head %s", rebuilt.Head(), r.Head())
	}
	origTwin, _ := r.Get("VESSEL:IMO1")
	rebuiltTwin, _ := rebuilt.Get("VESSEL:IMO1")
	if origTwin.Attributes["FLAG_STATE"] != rebuiltTwin.Attributes["FLAG_STATE"] {
		t.Fatalf("attribute state diverged: orig=%+v rebuilt=%+v",
			origTwin.Attributes["FLAG_STATE"], rebuiltTwin.Attributes["FLAG_STATE"])
	}
	if origTwin.Risk["risk"] != rebuiltTwin.Risk["risk"] {
		t.Fatalf("risk state diverged: orig=%+v rebuilt=%+v", origTwin.Risk["risk"], rebuiltTwin.Risk["risk"])
	}
}

func TestRebuildRejectsTamperedLog(t *testing.T) {
	r := NewRegistry()
	claim := fusion.Claim{Subject: "V1", Predicate: "FLAG"}
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claim, Winner: "X", WinnerConfidence: 0.5}, 1))
	log := r.ReplayAll()
	log[0].Value = "TAMPERED"
	if _, err := Rebuild(log); !errors.Is(err, ErrTamperDetected) {
		t.Fatalf("expected ErrTamperDetected, got %v", err)
	}
}

func TestEntitiesSorted(t *testing.T) {
	r := NewRegistry()
	claim := fusion.Claim{Subject: "x", Predicate: "FLAG"}
	mustTwin(t, r.ApplyArbitration("a", "V3", fusion.ArbitrationResult{Claim: claim, Winner: "x", WinnerConfidence: 0.5}, 1))
	mustTwin(t, r.ApplyArbitration("a", "V1", fusion.ArbitrationResult{Claim: claim, Winner: "x", WinnerConfidence: 0.5}, 2))
	entities := r.Entities()
	if len(entities) != 2 || entities[0] != "V1" || entities[1] != "V3" {
		t.Fatalf("unexpected entity order: %v", entities)
	}
}
