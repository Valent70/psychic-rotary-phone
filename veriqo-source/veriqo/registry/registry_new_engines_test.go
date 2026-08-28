package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/verification"
)

func TestEvidenceAPI_AddNodeAddEdgeLineageInfluenced(t *testing.T) {
	api := NewEvidenceAPI()
	root, err := api.AddNode("raw_observation", "vessel/IMO1 flagged")
	if err != nil {
		t.Fatalf("AddNode root: %v", err)
	}
	child, err := api.AddNode("decision", "ESCALATE issued")
	if err != nil {
		t.Fatalf("AddNode child: %v", err)
	}
	if _, err := api.AddEdge(child, root, "derived_from"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	lineage, err := api.Lineage(child)
	if err != nil || len(lineage) != 1 || string(lineage[0].ID) != root {
		t.Fatalf("Lineage(child) = %+v, err=%v; want [root]", lineage, err)
	}
	influenced, err := api.Influenced(root)
	if err != nil || len(influenced) != 1 || string(influenced[0].ID) != child {
		t.Fatalf("Influenced(root) = %+v, err=%v; want [child]", influenced, err)
	}
	stats, err := api.Stats()
	if err != nil || stats != "nodes=2 edges=1" {
		t.Fatalf("Stats() = %q, err=%v; want nodes=2 edges=1", stats, err)
	}

	// Cycle rejection must surface as a real error, not be silently absorbed.
	if _, err := api.AddEdge(root, child, "derived_from"); err == nil {
		t.Fatal("expected a cycle error from AddEdge(root, child, derived_from)")
	}
}

func TestReasoningAPI_AssertInferQueryExplain(t *testing.T) {
	api := NewReasoningAPI()
	if _, err := api.Assert("alice", "reports_to", "bob"); err != nil {
		t.Fatalf("Assert 1: %v", err)
	}
	if _, err := api.Assert("bob", "reports_to", "carol"); err != nil {
		t.Fatalf("Assert 2: %v", err)
	}
	// No transitive rule registered by this adapter (rule authoring is
	// out of the flat-transport boundary, matching TrustAPI's one-
	// fixed-policy constraint) — so Infer over plain assertions should
	// derive nothing new, and Query should return exactly the asserted
	// facts.
	n, err := api.Infer()
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if n != 0 {
		t.Fatalf("Infer() = %d new facts, want 0 (no rules registered)", n)
	}
	facts, err := api.Query("alice", "reports_to", "?x")
	if err != nil || len(facts) != 1 || facts[0].Object != "bob" {
		t.Fatalf("Query = %+v, err=%v; want [(alice reports_to bob)]", facts, err)
	}
	if _, err := api.Explain("alice", "reports_to", "bob"); err == nil {
		t.Fatal("Explain on a directly-asserted (not derived) fact should error")
	}
}

func TestPolicyAPI_EvaluateLedgerVerifyChain(t *testing.T) {
	api := NewPolicyAPI()

	// Ontology invalid must Deny regardless of anything else.
	v, err := api.Evaluate("FLAG", "vessel/IMO1", "FLAG", 0.9, "false")
	if err != nil || v != "DENY" {
		t.Fatalf("Evaluate(ontology invalid) = %q, err=%v; want DENY", v, err)
	}
	// Trust below floor must Deny.
	v, err = api.Evaluate("FLAG", "vessel/IMO2", "FLAG", 0.1, "true")
	if err != nil || v != "DENY" {
		t.Fatalf("Evaluate(low trust) = %q, err=%v; want DENY", v, err)
	}
	// ESCALATE risk action requires approval.
	v, err = api.Evaluate("ESCALATE", "vessel/IMO3", "ESCALATE", 0.9, "true")
	if err != nil || v != "REQUIRE_APPROVAL" {
		t.Fatalf("Evaluate(escalate) = %q, err=%v; want REQUIRE_APPROVAL", v, err)
	}
	// Known action, everything else clean -> Allow.
	v, err = api.Evaluate("FLAG", "vessel/IMO4", "FLAG", 0.9, "true")
	if err != nil || v != "ALLOW" {
		t.Fatalf("Evaluate(clean) = %q, err=%v; want ALLOW", v, err)
	}

	ledger, err := api.Ledger()
	if err != nil || len(ledger) != 4 {
		t.Fatalf("Ledger() = %d records, err=%v; want 4", len(ledger), err)
	}
	if _, err := api.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestVerificationAndReplayAPI_ContradictionRoundTrip(t *testing.T) {
	// Build one real contradiction-arbitration claim exactly the way
	// pkg/engine's own IVFReplayFunc test does, so this proves REAL
	// independent recomputation through the registry adapter, not a
	// fabricated string comparison.
	obs := contradiction.RawObservation{
		ClaimKey: "vessel/IMO1|flag_state", SourceID: "src-a",
		Value: "PANAMA", Tick: 1, SourceReliability: 0.9,
	}
	rec, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	params := struct {
		ClaimKey     string
		Tick         uint64
		HalfLifeTick uint64
	}{ClaimKey: "vessel/IMO1|flag_state", Tick: 1, HalfLifeTick: 100}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	records := []verification.EvidenceRecord{
		{Kind: "raw_observation", Index: 0, Payload: rec},
		{Kind: "contradiction.ArbitrationParams", Index: 1, Payload: paramsJSON},
	}
	recordsJSON, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal records: %v", err)
	}

	replayAPI := NewReplayAPI()
	claimed, err := replayAPI.Contradiction("vessel/IMO1|flag_state", string(recordsJSON))
	if err != nil {
		t.Fatalf("ReplayAPI.Contradiction: %v", err)
	}
	if !strings.Contains(claimed, "PANAMA") {
		t.Fatalf("replayed result = %q, want it to contain the arbitrated value PANAMA", claimed)
	}

	verAPI := NewVerificationAPI()
	result, err := verAPI.Verify("contradiction", "vessel/IMO1|flag_state", string(recordsJSON), claimed)
	if err != nil {
		t.Fatalf("VerificationAPI.Verify: %v", err)
	}
	if !strings.Contains(result, "status=VERIFIED") {
		t.Fatalf("Verify result = %q, want status=VERIFIED", result)
	}

	// Tamper with the claimed result: verification must now REJECT.
	tampered, err := verAPI.Verify("contradiction", "vessel/IMO1|flag_state", string(recordsJSON), "TAMPERED")
	if err != nil {
		t.Fatalf("Verify(tampered): %v", err)
	}
	if !strings.Contains(tampered, "status=REJECTED") {
		t.Fatalf("Verify(tampered) = %q, want status=REJECTED", tampered)
	}
}
