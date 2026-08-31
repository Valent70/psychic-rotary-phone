package rest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/pkg/commercial/tenancy"
	"veriqo/pkg/platform/security"
	"veriqo/veriqo/registry"
)

func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	return bytes.NewReader(b)
}

// This file proves Commercialization Sprint P0-B's central adversarial
// claim over a genuine net/http round trip: a caller holding a
// perfectly valid JWT for their own identity cannot act as a tenant
// their membership does not cover, even by naming that tenant directly
// in the request -- the honest gap the reviewer named ("TenantID is
// currently a caller-supplied field ... client tidak boleh memilih
// tenant arbitrarily") is now closed at the HTTP boundary.

func bearerToken(t *testing.T, secret []byte, subject string) string {
	t.Helper()
	tok, err := security.SignHS256(security.Claims{
		Subject: subject, Role: "commercial-user", Exp: time.Now().Add(time.Hour).Unix(),
	}, secret)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}
	return tok
}

func TestTenantBindingRefusesAnAuthenticatedCallerNamingAnUnauthorizedTenant(t *testing.T) {
	secret := []byte("test-secret-tenant-binding")
	store := commercialapi.NewStore()
	membership := tenancy.New()
	membership.Grant("alice", "tenant-A")

	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret: secret, CommercialStore: store, CommercialTenantMembership: membership,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	tok := bearerToken(t, secret, "alice")

	// alice's own tenant: must succeed.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/cases",
		jsonBody(t, map[string]any{"tenant_id": "tenant-A", "case_id": "CASE-BIND-1", "tick": 0}))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/cases (own tenant): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for alice acting as her own tenant-A, got %d", resp.StatusCode)
	}

	// A tenant alice's JWT was never granted: must be refused, even
	// though the JWT itself is completely valid.
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/cases",
		jsonBody(t, map[string]any{"tenant_id": "tenant-B", "case_id": "CASE-BIND-2", "tick": 0}))
	req2.Header.Set("Authorization", "Bearer "+tok)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST /v1/cases (foreign tenant): %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when alice's valid JWT names an unauthorized tenant, got %d", resp2.StatusCode)
	}

	// The refused case must never have been created.
	viewResp := doJSON(t, http.MethodGet, ts.URL+"/v1/cases/CASE-BIND-2?tenant_id=tenant-B", nil)
	defer viewResp.Body.Close()
	if viewResp.StatusCode == http.StatusOK {
		t.Fatal("expected the refused cross-tenant case to never have been created")
	}
}

func TestTenantBindingRequiresATenantIDWhenAuthenticated(t *testing.T) {
	secret := []byte("test-secret-tenant-binding-2")
	store := commercialapi.NewStore()
	membership := tenancy.New()
	membership.Grant("alice", "tenant-A")

	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret: secret, CommercialStore: store, CommercialTenantMembership: membership,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	tok := bearerToken(t, secret, "alice")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/cases",
		jsonBody(t, map[string]any{"case_id": "CASE-BIND-3", "tick": 0}))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an authenticated caller omitting tenant_id, got %d", resp.StatusCode)
	}
}

func TestTenantBindingFailsClosedWhenMembershipIsNotConfigured(t *testing.T) {
	secret := []byte("test-secret-tenant-binding-3")
	store := commercialapi.NewStore()

	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	// JWTSecret set, CommercialTenantMembership deliberately left nil.
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{JWTSecret: secret, CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	tok := bearerToken(t, secret, "alice")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/cases",
		jsonBody(t, map[string]any{"tenant_id": "tenant-A", "case_id": "CASE-BIND-4", "tick": 0}))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 (fail closed) when no Membership is configured for an authenticated deployment, got %d", resp.StatusCode)
	}
}

// TestTenantBindingUnauthenticatedDeploymentTrustsCallerAsBefore proves
// backward compatibility: a deployment with no JWTSecret configured at
// all (every pre-P0-B test of these routes) keeps trusting the
// caller-supplied tenant_id exactly as before -- there is no
// authenticated identity to bind to in the first place.
func TestTenantBindingUnauthenticatedDeploymentTrustsCallerAsBefore(t *testing.T) {
	store := commercialapi.NewStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/cases", map[string]any{"tenant_id": "any-tenant-at-all", "case_id": "CASE-BIND-5", "tick": 0})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for an unauthenticated deployment trusting the caller's own tenant_id, got %d", resp.StatusCode)
	}
}

// TestTenantBindingFullLifecycleForAnAuthorizedSubject proves the
// happy path end to end: a real subject, really authorized for a real
// tenant, can complete the whole evidence-to-replay case lifecycle
// with binding enforced at every step.
func TestTenantBindingFullLifecycleForAnAuthorizedSubject(t *testing.T) {
	secret := []byte("test-secret-tenant-binding-4")
	store := commercialapi.NewStore()
	membership := tenancy.New()
	membership.Grant("bob", "tenant-bob-co")

	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret: secret, CommercialStore: store, CommercialTenantMembership: membership,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	tok := bearerToken(t, secret, "bob")

	authedPost := func(path string, body map[string]any) *http.Response {
		req, err := http.NewRequest(http.MethodPost, ts.URL+path, jsonBody(t, body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return resp
	}
	authedGet := func(path string) *http.Response {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	const caseID = "CASE-BIND-LIFECYCLE-1"
	const evidenceID = "EV-BIND-LIFECYCLE-1"

	resp := authedPost("/v1/cases", map[string]any{"tenant_id": "tenant-bob-co", "case_id": caseID, "tick": 0})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateCase: expected 201, got %d", resp.StatusCode)
	}

	resp = authedPost("/v1/evidence", v1SubmitEvidenceBody("tenant-bob-co", caseID, evidenceID, 10))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("SubmitEvidence: expected 201, got %d", resp.StatusCode)
	}

	resp = authedPost("/v1/cases/"+caseID+"/decide", v1DecideBody("tenant-bob-co", caseID, []string{evidenceID}))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DecideCase: expected 200, got %d", resp.StatusCode)
	}

	resp = authedGet("/v1/cases/" + caseID + "?tenant_id=tenant-bob-co")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetCase: expected 200, got %d", resp.StatusCode)
	}
}
