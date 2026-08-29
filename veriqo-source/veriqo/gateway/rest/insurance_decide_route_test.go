package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/security"
	"veriqo/veriqo/registry"
)

func validInsuranceDecideBody(caseID string) map[string]any {
	return map[string]any{
		"case_id": caseID, "tick": 10,
		"manifests": []map[string]any{
			{
				"evidence_id": "EV-HTTP-1",
				"sha256":      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
				"uri":         "evidence://http-survey.pdf", "filename": "http-survey.pdf",
				"media_type": "application/pdf", "byte_size": 4096,
				"collector": "surveyor-http", "source": "independent-surveyor",
			},
		},
		"hypothesis":              map[string]string{"id": "H1", "description": "water ingress during transit"},
		"supporting_evidence_ids": []string{"EV-HTTP-1"},
		"finding_id":              "finding-http-1",
		"finding":                 map[string]any{"contract_basis": "clause-1", "obligation_ref": "obl-1", "event_ref": "event-1", "quantum_ref": "calc-1", "human_review_required": true},
		"outcome":                 "APPROVED",
		"rationale":               "primary hypothesis substantiated by grounded, finalized evidence",
		"ledger_actor":            "http-caller",
	}
}

// TestInsuranceDecideRouteHappyPath proves the real, live shape: API ->
// Identity (JWT) -> Authorization (RBAC) -> Validation -> Trusted
// pipeline (claimworkflow's gated DAG) -> a real, ledgered Decision --
// over a genuine net/http round trip, not an in-process function call.
func TestInsuranceDecideRouteHappyPath(t *testing.T) {
	secret := []byte("test-secret-insurance-decide")
	ledger := audit.NewAuditStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret:       secret,
		RBAC:            security.RoleTable{"claims-adjuster": {"/insurance/"}},
		InsuranceLedger: ledger,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	tok, err := security.SignHS256(security.Claims{
		Subject: "adjuster-1", Role: "claims-adjuster", Exp: time.Now().Add(time.Hour).Unix(),
	}, secret)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}

	body, _ := json.Marshal(validInsuranceDecideBody("CASE-HTTP-1"))
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/insurance/decide", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /insurance/decide: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got respInsuranceDecide
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Outcome != "APPROVED" {
		t.Fatalf("expected APPROVED, got %s", got.Outcome)
	}
	if got.Hash == "" {
		t.Fatal("expected a real, non-empty Decision hash")
	}
	if len(got.WorkflowStepOrder) != 5 {
		t.Fatalf("expected 5 workflow steps, got %v", got.WorkflowStepOrder)
	}

	recs := ledger.Snapshot()
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 ledger record, got %d", len(recs))
	}
	if recs[0].Actor != "http-caller" {
		t.Fatalf("expected the ledger actor to be the request's own ledger_actor field, got %s", recs[0].Actor)
	}
}

// TestInsuranceDecideRouteRejectsUnauthenticatedCaller is the API ->
// Decision bypass attack the review names first: no Bearer token at
// all. Must be refused by JWTMiddleware (Identity) before the handler
// -- and therefore before the Trusted pipeline -- ever runs: no
// Decision, no ledger entry.
func TestInsuranceDecideRouteRejectsUnauthenticatedCaller(t *testing.T) {
	secret := []byte("test-secret-insurance-decide")
	ledger := audit.NewAuditStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret:       secret,
		RBAC:            security.RoleTable{"claims-adjuster": {"/insurance/"}},
		InsuranceLedger: ledger,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(validInsuranceDecideBody("CASE-HTTP-NOAUTH-1"))
	// Deliberately no Authorization header at all.
	resp, err := http.Post(ts.URL+"/insurance/decide", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a request with no Bearer token, got %d", resp.StatusCode)
	}
	if len(ledger.Snapshot()) != 0 {
		t.Fatalf("expected ZERO ledger records for a rejected unauthenticated request, got %d", len(ledger.Snapshot()))
	}
}

// TestInsuranceDecideRouteRejectsWrongRole proves the Authorization
// layer independently gates this route: a caller with a VALID,
// correctly-signed JWT but a role RBAC never granted "/insurance/"
// access to must still be refused, before the Trusted pipeline runs.
func TestInsuranceDecideRouteRejectsWrongRole(t *testing.T) {
	secret := []byte("test-secret-insurance-decide")
	ledger := audit.NewAuditStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret: secret,
		// "claims-adjuster" is the only role granted /insurance/ -- this
		// caller has a different, VALID, but unauthorized role.
		RBAC:            security.RoleTable{"claims-adjuster": {"/insurance/"}, "auditor": {"/lifecycle/"}},
		InsuranceLedger: ledger,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	tok, err := security.SignHS256(security.Claims{
		Subject: "auditor-1", Role: "auditor", Exp: time.Now().Add(time.Hour).Unix(),
	}, secret)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}

	body, _ := json.Marshal(validInsuranceDecideBody("CASE-HTTP-WRONGROLE-1"))
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/insurance/decide", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a valid but unauthorized role, got %d", resp.StatusCode)
	}
	if len(ledger.Snapshot()) != 0 {
		t.Fatalf("expected ZERO ledger records for a role RBAC refused, got %d", len(ledger.Snapshot()))
	}
}

// TestInsuranceDecideRouteRejectsUngroundedEvidenceEndToEnd proves the
// Trusted pipeline's own gates hold even for an authenticated,
// authorized caller: evidence cited by the hypothesis but never listed
// in "manifests" (never finalized) must be refused with no Decision
// produced -- the API -> Decision boundary the review names, proven
// over the real HTTP surface, not just the Go-level claimworkflow
// tests.
func TestInsuranceDecideRouteRejectsUngroundedEvidenceEndToEnd(t *testing.T) {
	secret := []byte("test-secret-insurance-decide")
	ledger := audit.NewAuditStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{
		JWTSecret:       secret,
		RBAC:            security.RoleTable{"claims-adjuster": {"/insurance/"}},
		InsuranceLedger: ledger,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	tok, err := security.SignHS256(security.Claims{
		Subject: "adjuster-1", Role: "claims-adjuster", Exp: time.Now().Add(time.Hour).Unix(),
	}, secret)
	if err != nil {
		t.Fatalf("SignHS256: %v", err)
	}

	reqBody := validInsuranceDecideBody("CASE-HTTP-UNGROUNDED-1")
	// Cite evidence that was never included in "manifests" at all.
	reqBody["supporting_evidence_ids"] = []string{"EV-HTTP-NEVER-FINALIZED"}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/insurance/decide", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for ungrounded evidence, got %d", resp.StatusCode)
	}
	if len(ledger.Snapshot()) != 0 {
		t.Fatalf("expected ZERO ledger records for a refused decision, got %d", len(ledger.Snapshot()))
	}
}

// TestInsuranceDecideRouteAbsentWhenLedgerNil proves the nil-safety
// this route's own doc comment promises: every OTHER caller of
// NewServer before this route existed passed no InsuranceLedger, and
// must keep working exactly as before -- 404, not a panic.
func TestInsuranceDecideRouteAbsentWhenLedgerNil(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/insurance/decide", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when no InsuranceLedger is configured, got %d", resp.StatusCode)
	}
}
