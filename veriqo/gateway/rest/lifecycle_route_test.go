package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"veriqo/pkg/lifecycle"
	"veriqo/veriqo/registry"
)

// TestLifecycleRunUnifiedRoute is the real proof for this session's
// P0-8 finding: pkg/lifecycle.Orchestrator.RunUnified -- the audit's
// named Intent entrypoint and (per this session's P0-1 round) the real
// caller of pkg/execution's full production DAG -- previously had NO
// HTTP-facing caller anywhere in this repository. This test drives it
// over a genuine net/http round trip (httptest.NewServer, a real TCP
// listener) and confirms a real Decision, ExecutionRootHash and
// DecisionID come back -- not a stub.
func TestLifecycleRunUnifiedRoute(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	orch := lifecycle.NewOrchestrator(nil)
	srv := NewServer("127.0.0.1:0", reg, orch, ServerOptions{})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"actor_id": "analyst-http-1", "tenant": "acme", "objective": "assess dark-vessel risk",
		"entity_aliases":      []map[string]string{{"kind": "IMO", "value": "9887766"}},
		"required_confidence": 0.6, "temporal_scope": "last-7-days", "tick": 1,
		"requirements": []map[string]any{{"kind": "AIS_STATUS", "required": true, "min_sources": 2}},
		"subject":      "x", "predicate": "AIS_STATUS",
		"submissions": []map[string]any{
			{"source_id": "ais-vendor-a", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "ais-vendor-b", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "port-authority", "value": "ON", "base_reliability": 0.95},
		},
	})

	resp, err := http.Post(ts.URL+"/lifecycle/run_unified", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /lifecycle/run_unified: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got respRunUnified
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.EntityID == "" {
		t.Fatal("expected a real, non-empty EntityID resolved from the posted aliases")
	}
	if got.Decision == "" {
		t.Fatal("expected a real Decision")
	}
	if got.ExecutionRootHash == "" {
		t.Fatal("expected a real ExecutionRootHash from the DAG this request actually ran through")
	}
	if got.DecisionID == "" || got.DecisionID == got.ExecutionID {
		t.Fatalf("expected a real, independently-derived DecisionID (P0-7), got %q (execution_id=%q)",
			got.DecisionID, got.ExecutionID)
	}
	if !got.IVFVerified {
		t.Fatal("expected IVF to independently verify this case's arbitration")
	}
}

// TestLifecycleRunUnifiedRouteRejectsBadJSON proves the handler fails
// closed on malformed input rather than panicking or silently ignoring
// it.
func TestLifecycleRunUnifiedRouteRejectsBadJSON(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	orch := lifecycle.NewOrchestrator(nil)
	srv := NewServer("127.0.0.1:0", reg, orch, ServerOptions{})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/lifecycle/run_unified", "application/json", bytes.NewReader([]byte("{not json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", resp.StatusCode)
	}
}

// TestLifecycleRunUnifiedRouteAbsentWhenOrchestratorNil proves the
// nil-safety this route's own doc comment promises: every OTHER caller
// of NewServer before this route existed passed no orchestrator, and
// must keep working exactly as before -- 404, not a panic.
func TestLifecycleRunUnifiedRouteAbsentWhenOrchestratorNil(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/lifecycle/run_unified", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when no orchestrator is configured, got %d", resp.StatusCode)
	}
}
