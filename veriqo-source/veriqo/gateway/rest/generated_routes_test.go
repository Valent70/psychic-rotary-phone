package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/lifecycle"
	"veriqo/veriqo/registry"
)

// TestGeneratedRouteSurfacesSaveStateFailureAs500 is the real proof
// for a bug this round's own suppressed-error sweep found: every
// generated handler used to silently discard reg.SaveState()'s error
// (`_ = reg.SaveState()`), which meant a request whose in-memory
// mutation succeeded but whose durable persistence failed still
// returned 200 OK with a correct-looking body -- invisible data loss
// on the next restart, exactly the false-green result this project's
// whole discipline exists to prevent. veriqo/generator/codegen.go's
// template (and the regenerated generated_routes.go) now surfaces
// that failure as 500. This test forces a real SaveState failure (a
// state path whose parent segment is a plain file, not a directory,
// so the write cannot possibly succeed) and confirms the HTTP
// response is 500, not 200.
func TestGeneratedRouteSurfacesSaveStateFailureAs500(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	statePath := filepath.Join(sub, "state.json")

	// Construct with a genuinely valid, writable path first (New itself
	// loads any existing state, so the path must be real at
	// construction time) -- then, AFTER construction, replace the
	// containing directory with a plain file. Every subsequent
	// SaveState call tries to write "through" a file where a directory
	// must be, which fails structurally (ENOTDIR) regardless of file
	// permissions or which user is running the test.
	reg, err := registry.New(registry.WithStatePath(statePath))
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	if err := os.RemoveAll(sub); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.WriteFile(sub, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !reg.HasPersistence() {
		t.Fatal("expected HasPersistence() true with WithStatePath set")
	}
	orch := lifecycle.NewOrchestrator(nil, nil)
	srv := NewServer("127.0.0.1:0", reg, orch, ServerOptions{})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"kind": "raw_observation", "payload": "hello"})
	resp, err := http.Post(ts.URL+"/evidence/add_node", "application/json", bytes.NewReader(body)) // #nosec G107 -- ts.URL is this test's own httptest.NewServer address
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when SaveState fails, got %d", resp.StatusCode)
	}
}

// TestGeneratedRouteSucceedsWhenPersistenceIsHealthy is the negative-
// space companion: a healthy, writable state path must still return
// 200 with the real result -- proving the 500 above is genuinely
// conditioned on the SaveState failure, not an unconditional
// regression.
func TestGeneratedRouteSucceedsWhenPersistenceIsHealthy(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	reg, err := registry.New(registry.WithStatePath(statePath))
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	orch := lifecycle.NewOrchestrator(nil, nil)
	srv := NewServer("127.0.0.1:0", reg, orch, ServerOptions{})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"kind": "raw_observation", "payload": "hello"})
	resp, err := http.Post(ts.URL+"/evidence/add_node", "application/json", bytes.NewReader(body)) // #nosec G107 -- ts.URL is this test's own httptest.NewServer address
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with healthy persistence, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state file to actually be written: %v", err)
	}
}
