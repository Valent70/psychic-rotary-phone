package runtime

import (
	"encoding/json"
	"testing"

	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/verification"
	"veriqo/veriqo/registry"
)

func buildRealManager(t *testing.T) (*Manager, *registry.Registry) {
	t.Helper()
	reg, err := registry.New()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	if err := RegisterAll(m, reg); err != nil {
		t.Fatal(err)
	}
	if err := m.StartAll(); err != nil {
		t.Fatal(err)
	}
	return m, reg
}

func TestAllSevenEnginesRegisteredAndHealthy(t *testing.T) {
	m, _ := buildRealManager(t)
	report := m.HealthReport()
	want := []string{"trust", "intent", "evidence", "reasoning", "policy", "verification", "replay"}
	for _, name := range want {
		if report[name] != StatusHealthy {
			t.Fatalf("expected %s healthy, got %v", name, report[name])
		}
	}
}

func TestManagerStopThenHealthReflectsStopped(t *testing.T) {
	m, _ := buildRealManager(t)
	if err := m.StopAll(); err != nil {
		t.Fatal(err)
	}
	report := m.HealthReport()
	for name, status := range report {
		if status != StatusStopped {
			t.Fatalf("%s: expected stopped after StopAll, got %v", name, status)
		}
	}
}

func TestBridgeUnknownMethodErrors(t *testing.T) {
	m, _ := buildRealManager(t)
	if _, err := m.Invoke("trust", "does_not_exist", nil); err == nil {
		t.Fatal("expected error for unknown method")
	}
}

func TestAllEngineMethodsCovered(t *testing.T) {
	m, _ := buildRealManager(t)

	if _, err := m.Invoke("trust", "evaluate", map[string]string{
		"subject_id": "s1", "win_rate": "0.8", "contradiction_rate": "0.1", "tick": "1",
	}); err != nil {
		t.Fatalf("trust.evaluate: %v", err)
	}
	if _, err := m.Invoke("trust", "certify", map[string]string{
		"subject_id": "s1", "win_rate": "0.9", "contradiction_rate": "0.05", "tick": "2",
	}); err != nil {
		t.Fatalf("trust.certify: %v", err)
	}
	if out, err := m.Invoke("trust", "ledger", nil); err != nil || out["result"] == "" {
		t.Fatalf("trust.ledger: %v %v", out, err)
	}
	if out, err := m.Invoke("trust", "verify_ledger", nil); err != nil || out["result"] == "" {
		t.Fatalf("trust.verify_ledger: %v %v", out, err)
	}
	if _, err := m.Invoke("trust", "evaluate", map[string]string{"subject_id": "s"}); err == nil {
		t.Fatal("expected error for missing trust args")
	}

	intentArgs := map[string]string{
		"intent_id": "i1", "subject": "s1", "expected_action": "sail", "observed_action": "sail",
		"expected_risk": "0.2", "observed_risk": "0.2", "tick": "1",
	}
	if _, err := m.Invoke("intent", "verify", intentArgs); err != nil {
		t.Fatalf("intent.verify: %v", err)
	}
	if _, err := m.Invoke("intent", "certify", intentArgs); err != nil {
		t.Fatalf("intent.certify: %v", err)
	}
	if out, err := m.Invoke("intent", "ledger", nil); err != nil || out["result"] == "" {
		t.Fatalf("intent.ledger: %v %v", out, err)
	}
	if _, err := m.Invoke("intent", "verify", map[string]string{}); err == nil {
		t.Fatal("expected error for missing intent args")
	}

	n1, err := m.Invoke("evidence", "add_node", map[string]string{"kind": "raw_observation", "payload": "p1"})
	if err != nil {
		t.Fatalf("evidence.add_node: %v", err)
	}
	n2, err := m.Invoke("evidence", "add_node", map[string]string{"kind": "raw_observation", "payload": "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke("evidence", "add_edge", map[string]string{"from": n2["id"], "to": n1["id"], "relation": "derived_from"}); err != nil {
		t.Fatalf("evidence.add_edge: %v", err)
	}
	if out, err := m.Invoke("evidence", "lineage", map[string]string{"id": n2["id"]}); err != nil || out["result"] == "" {
		t.Fatalf("evidence.lineage: %v %v", out, err)
	}
	if out, err := m.Invoke("evidence", "influenced", map[string]string{"id": n1["id"]}); err != nil || out["result"] == "" {
		t.Fatalf("evidence.influenced: %v %v", out, err)
	}
	if out, err := m.Invoke("evidence", "stats", nil); err != nil || out["result"] == "" {
		t.Fatalf("evidence.stats: %v %v", out, err)
	}

	if _, err := m.Invoke("reasoning", "assert", map[string]string{"subject": "a", "predicate": "knows", "object": "b"}); err != nil {
		t.Fatalf("reasoning.assert: %v", err)
	}
	if _, err := m.Invoke("reasoning", "infer", nil); err != nil {
		t.Fatalf("reasoning.infer: %v", err)
	}
	if out, err := m.Invoke("reasoning", "query", map[string]string{"subject": "a", "predicate": "knows", "object": ""}); err != nil || out["result"] == "" {
		t.Fatalf("reasoning.query: %v %v", out, err)
	}
	if _, err := m.Invoke("reasoning", "explain", map[string]string{"subject": "a", "predicate": "knows", "object": "b"}); err == nil {
		t.Fatal("expected explain on a directly-asserted (non-derived) fact to error")
	}

	if _, err := m.Invoke("policy", "evaluate", map[string]string{
		"action": "FLAG", "subject": "vessel/1", "risk_action": "FLAG", "trust_score": "0.9", "ontology_valid": "true",
	}); err != nil {
		t.Fatalf("policy.evaluate: %v", err)
	}
	if out, err := m.Invoke("policy", "ledger", nil); err != nil || out["result"] == "" {
		t.Fatalf("policy.ledger: %v %v", out, err)
	}
	if out, err := m.Invoke("policy", "verify_chain", nil); err != nil || out["result"] == "" {
		t.Fatalf("policy.verify_chain: %v %v", out, err)
	}
	if _, err := m.Invoke("policy", "evaluate", map[string]string{"action": "x"}); err == nil {
		t.Fatal("expected error for missing policy trust_score")
	}

	obs := contradiction.RawObservation{ClaimKey: "vessel/IMOX|flag_state", SourceID: "src-a", Value: "PANAMA", Tick: 1, SourceReliability: 0.9}
	recPayload, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	params := struct {
		ClaimKey     string
		Tick         uint64
		HalfLifeTick uint64
	}{ClaimKey: "vessel/IMOX|flag_state", Tick: 1, HalfLifeTick: 100}
	paramsPayload, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	records := []verification.EvidenceRecord{
		{Kind: "raw_observation", Index: 0, Payload: recPayload},
		{Kind: "contradiction.ArbitrationParams", Index: 1, Payload: paramsPayload},
	}
	recordsJSON, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}

	replayOut, err := m.Invoke("replay", "contradiction", map[string]string{"subject": "vessel/IMOX|flag_state", "records_json": string(recordsJSON)})
	if err != nil || replayOut["result"] == "" {
		t.Fatalf("replay.contradiction: %v %v", replayOut, err)
	}
	claimed := replayOut["result"]

	verOut, err := m.Invoke("verification", "verify", map[string]string{
		"domain": "contradiction", "subject": "vessel/IMOX|flag_state", "records_json": string(recordsJSON), "claimed_result": claimed,
	})
	if err != nil || verOut["result"] == "" {
		t.Fatalf("verification.verify: %v %v", verOut, err)
	}
	if _, err := m.Invoke("verification", "verify_certificate", map[string]string{"certificate_json": "not valid json"}); err == nil {
		t.Fatal("expected error for malformed certificate_json")
	}
	if _, err := m.Invoke("replay", "fusion", map[string]string{"subject": "vessel/IMOX|flag_state", "records_json": string(recordsJSON)}); err == nil {
		t.Fatal("expected replay.fusion to reject a contradiction-shaped bundle")
	}

	for _, name := range []string{"intent", "evidence", "reasoning", "policy", "verification", "replay"} {
		if _, err := m.Invoke(name, "nope", nil); err == nil {
			t.Fatalf("%s: expected error for unknown method", name)
		}
	}
}
