package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/veriqo/registry"
)

// TestLivezAlwaysReportsAlive proves GET /livez is the trivial,
// dependency-free check its own doc comment promises: it must report
// 200 even when nothing else about this deployment (no Commercial
// Store, no registry persistence) is configured.
func TestLivezAlwaysReportsAlive(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/livez")
	if err != nil {
		t.Fatalf("GET /livez: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /livez: expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /livez body: %v", err)
	}
	if body["status"] != "alive" {
		t.Fatalf("expected status=alive, got %+v", body)
	}
}

// TestReadyzReportsReadyWithNoCommercialStoreConfigured proves a
// deployment that never wired in a Commercial Store (nil
// CommercialStore, the pre-P0-A/pre-Commercial-API default) is not
// penalized by the new readiness check -- there is nothing to be
// unready about.
func TestReadyzReportsReadyWithNoCommercialStoreConfigured(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /readyz: expected 200, got %d", resp.StatusCode)
	}
}

// TestReadyzReportsReadyWithAHealthyDurableCommercialStore and
// TestReadyzReportsNotReadyOnceCommercialStoreIsClosed together prove
// /readyz actually tracks the Commercial Store's real lifecycle, not a
// hardcoded 200 -- the central claim of wiring Store.Healthy into this
// probe at all.
func TestReadyzReportsReadyWithAHealthyDurableCommercialStore(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	walDir := filepath.Join(t.TempDir(), "wal")
	store, _, err := commercialapi.NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	defer store.Close()

	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /readyz: expected 200 with an open durable Store, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /readyz body: %v", err)
	}
	if body["status"] != "ready" {
		t.Fatalf("expected status=ready, got %+v", body)
	}
}

func TestReadyzReportsNotReadyOnceCommercialStoreIsClosed(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	walDir := filepath.Join(t.TempDir(), "wal")
	store, _, err := commercialapi.NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz: expected 503 with a closed durable Store, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /readyz body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Fatalf("expected status=not_ready, got %+v", body)
	}
}

// TestOversizedRequestBodyIsRejected proves the P0-6 security-hardening
// fix directly: a POST body larger than this route's configured limit
// must be rejected with a clean 4xx, not silently buffered without
// bound. POST /v1/cases uses maxJSONBodyBytes; this test sends a body
// that exceeds it by padding an oversized field.
func TestOversizedRequestBodyIsRejected(t *testing.T) {
	store := commercialapi.NewStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	oversized := strings.Repeat("A", maxJSONBodyBytes+1024)
	payload, err := json.Marshal(map[string]any{
		"tenant_id": "tenant-oversized", "case_id": oversized, "tick": 0,
	})
	if err != nil {
		t.Fatalf("marshaling oversized payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/cases", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/cases with an oversized body: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a body over the configured limit, got %d", resp.StatusCode)
	}
}

// TestWellUnderLimitRequestBodyStillSucceeds guards against the
// hardening fix being too aggressive -- an ordinary, well-formed
// request (comfortably under maxJSONBodyBytes) must still succeed.
func TestWellUnderLimitRequestBodyStillSucceeds(t *testing.T) {
	store := commercialapi.NewStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/cases", map[string]any{
		"tenant_id": "tenant-normal-size", "case_id": "CASE-NORMAL-SIZE-1", "tick": 0,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/cases with a normal-size body: expected 201, got %d", resp.StatusCode)
	}
}
