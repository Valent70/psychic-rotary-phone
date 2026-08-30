package rest

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/commercial/packageverify"
	"veriqo/veriqo/registry"
)

func v1SubmitEvidenceBody(tenantID, caseID, evidenceID string, tick uint64) map[string]any {
	return map[string]any{
		"tenant_id": tenantID, "case_id": caseID, "evidence_id": evidenceID,
		"sha256": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"uri":    "evidence://v1http-survey.pdf", "filename": "v1http-survey.pdf",
		"media_type": "application/pdf", "byte_size": 4096,
		"collector": "surveyor-v1http", "source": "independent-surveyor",
		"domain": map[string]any{
			"insurance": map[string]any{"claim_id": "CLM-1", "policy_id": "POL-1", "evidence_kind": "SURVEY"},
		},
		"tick": tick,
	}
}

func v1DecideBody(tenantID, caseID string, evidenceIDs []string) map[string]any {
	return map[string]any{
		"tenant_id":               tenantID,
		"hypothesis":              map[string]string{"id": "H1", "description": "water ingress during transit"},
		"supporting_evidence_ids": evidenceIDs,
		"finding_id":              "finding-v1http-1",
		"finding": map[string]any{
			"contract_basis": "clause-1", "obligation_ref": "obl-1", "event_ref": "event-1",
			"quantum_ref": "calc-1", "human_review_required": true,
		},
		"outcome":      "APPROVED",
		"rationale":    "primary hypothesis substantiated by grounded, finalized evidence",
		"ledger_actor": "v1http-decision",
		"tick":         10,
	}
}

func v1ActionBody(tenantID, caseID string) map[string]any {
	return map[string]any{
		"tenant_id": tenantID, "actor": "adjuster-v1http-1", "policy_ref": "policy-settlement-v1", "scope": caseID,
		"permitted_action": "APPROVE_SETTLEMENT", "conditions": []string{"reinspection_complete"},
		"authorized_at": 10, "expires_at": 20, "executing_actor": "adjuster-v1http-1", "execution_at": 15,
		"ledger_actor": "v1http-action",
	}
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// TestCommercialV1RoutesFullLifecycleHappyPath proves the whole
// Commercialization Sprint item-5 vertical slice over a genuine
// net/http round trip -- not an in-process Store call -- exactly
// mirroring the 18-step "Definition of Done" journey named by the
// reviewer (submit evidence, see it preserved, verify integrity, see
// custody, decide, authorize an action, generate a dossier, export a
// machine package, independently verify that package, and replay the
// case) without a developer ever touching the Store directly.
func TestCommercialV1RoutesFullLifecycleHappyPath(t *testing.T) {
	store := commercialapi.NewStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	const tenant = "tenant-v1http-A"
	const caseID = "CASE-V1HTTP-1"
	const evidenceID = "EV-V1HTTP-1"

	// 1. Create case.
	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/cases", map[string]any{"tenant_id": tenant, "case_id": caseID, "tick": 0})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/cases: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Submit evidence.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/evidence", v1SubmitEvidenceBody(tenant, caseID, evidenceID, 10))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/evidence: expected 201, got %d", resp.StatusCode)
	}
	var evRec evidencefabric.EvidenceRecord
	if err := json.NewDecoder(resp.Body).Decode(&evRec); err != nil {
		t.Fatalf("decoding evidence response: %v", err)
	}
	resp.Body.Close()
	if !evRec.Integrity.Verified {
		t.Fatal("expected submitted evidence to come back independently verified")
	}

	// 3. Get evidence back.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/evidence/"+evidenceID+"?tenant_id="+tenant, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/evidence/{id}: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Verify evidence.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/evidence/"+evidenceID+"/verify", map[string]any{"tenant_id": tenant})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/evidence/{id}/verify: expected 200, got %d", resp.StatusCode)
	}
	var verifyResp map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		t.Fatalf("decoding verify response: %v", err)
	}
	resp.Body.Close()
	if !verifyResp["verified"] {
		t.Fatal("expected a live re-verification of real, untampered evidence to report verified=true")
	}

	// 5. Custody chain.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/evidence/"+evidenceID+"/custody?tenant_id="+tenant, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/evidence/{id}/custody: expected 200, got %d", resp.StatusCode)
	}
	var custody []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&custody); err != nil {
		t.Fatalf("decoding custody response: %v", err)
	}
	resp.Body.Close()
	if len(custody) == 0 {
		t.Fatal("expected a non-empty custody chain for real, finalized evidence")
	}

	// 6. Decide.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/cases/"+caseID+"/decide", v1DecideBody(tenant, caseID, []string{evidenceID}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/cases/{id}/decide: expected 200, got %d", resp.StatusCode)
	}
	var decResp respDecision
	if err := json.NewDecoder(resp.Body).Decode(&decResp); err != nil {
		t.Fatalf("decoding decide response: %v", err)
	}
	resp.Body.Close()
	if decResp.Outcome != "APPROVED" || decResp.Hash == "" {
		t.Fatalf("unexpected decide response: %+v", decResp)
	}

	// 7. Authorize + execute an action.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/cases/"+caseID+"/actions", v1ActionBody(tenant, caseID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/cases/{id}/actions: expected 200, got %d", resp.StatusCode)
	}
	var actResp respAction
	if err := json.NewDecoder(resp.Body).Decode(&actResp); err != nil {
		t.Fatalf("decoding action response: %v", err)
	}
	resp.Body.Close()
	if actResp.Authorization.Hash == "" || actResp.Receipt.ReceiptID == "" {
		t.Fatalf("unexpected action response: %+v", actResp)
	}

	// 8. Get case view -- should now show decided+acted.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/cases/"+caseID+"?tenant_id="+tenant, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/cases/{id}: expected 200, got %d", resp.StatusCode)
	}
	var caseView commercialapi.CaseView
	if err := json.NewDecoder(resp.Body).Decode(&caseView); err != nil {
		t.Fatalf("decoding case view: %v", err)
	}
	resp.Body.Close()
	if !caseView.Decided || !caseView.ActedOn {
		t.Fatalf("expected case view to reflect decision+action, got %+v", caseView)
	}

	// 9. Human dossier (JSON).
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/cases/"+caseID+"/dossier?tenant_id="+tenant, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/cases/{id}/dossier: expected 200, got %d", resp.StatusCode)
	}
	var dos dossier.Dossier
	if err := json.NewDecoder(resp.Body).Decode(&dos); err != nil {
		t.Fatalf("decoding dossier: %v", err)
	}
	resp.Body.Close()
	if dos.PackageHash == "" {
		t.Fatal("expected a populated dossier with a non-empty package hash")
	}

	// 10. Machine dossier (.zip package).
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/cases/"+caseID+"/dossier?tenant_id="+tenant+"&format=package", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../dossier?format=package: expected 200, got %d", resp.StatusCode)
	}
	pkgBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("reading package bytes: %v", err)
	}
	if resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("expected application/zip content-type, got %s", resp.Header.Get("Content-Type"))
	}

	// 11. Independently verify that package -- the standalone-verifier
	// capability, over HTTP, requiring no tenant scoping at all.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/packages/verify", bytes.NewReader(pkgBytes))
	if err != nil {
		t.Fatalf("building verify-package request: %v", err)
	}
	verifyPkgResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/packages/verify: %v", err)
	}
	defer verifyPkgResp.Body.Close()
	if verifyPkgResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(verifyPkgResp.Body)
		t.Fatalf("expected 200 from a real, untampered package, got %d: %s", verifyPkgResp.StatusCode, body)
	}
	var verifyPkgOut struct {
		AllPassed bool                        `json:"all_passed"`
		Checks    []packageverify.CheckResult `json:"checks"`
	}
	if err := json.NewDecoder(verifyPkgResp.Body).Decode(&verifyPkgOut); err != nil {
		t.Fatalf("decoding verify-package response: %v", err)
	}
	if !verifyPkgOut.AllPassed {
		t.Fatalf("expected all checks to pass (skips allowed) for a real package, got: %+v", verifyPkgOut.Checks)
	}

	// Sanity: the bytes really are a well-formed zip carrying the
	// expected four files.
	zr, err := zip.NewReader(bytes.NewReader(pkgBytes), int64(len(pkgBytes)))
	if err != nil {
		t.Fatalf("package bytes are not a valid zip: %v", err)
	}
	wantFiles := map[string]bool{"dossier.json": false, "dossier.md": false, "manifests.json": false, "ledger.json": false}
	for _, f := range zr.File {
		if _, ok := wantFiles[f.Name]; ok {
			wantFiles[f.Name] = true
		}
	}
	for name, found := range wantFiles {
		if !found {
			t.Fatalf("expected the machine package to contain %s", name)
		}
	}

	// 12. Replay.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/cases/"+caseID+"/replay?tenant_id="+tenant, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/cases/{id}/replay: expected 200, got %d", resp.StatusCode)
	}
	var replay commercialapi.ReplayResult
	if err := json.NewDecoder(resp.Body).Decode(&replay); err != nil {
		t.Fatalf("decoding replay response: %v", err)
	}
	resp.Body.Close()
	if !replay.Converged {
		t.Fatalf("expected replay to converge on identical hashes, got %+v", replay)
	}
	if replay.OriginalDecisionHash != decResp.Hash {
		t.Fatal("expected the replay's OriginalDecisionHash to match the Decision hash returned by /decide")
	}
}

// TestCommercialV1RoutesTenantIsolationOverHTTP proves the Store's
// structural, key-namespaced tenant isolation holds at the HTTP layer
// too, not just in direct Go calls: Tenant B, given Tenant A's own
// case ID, gets a 404 -- never Tenant A's data, and never a 403 that
// would at least confirm the case's existence to an unauthorized
// caller.
func TestCommercialV1RoutesTenantIsolationOverHTTP(t *testing.T) {
	store := commercialapi.NewStore()
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{CommercialStore: store})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	const tenantA = "tenant-v1http-iso-A"
	const tenantB = "tenant-v1http-iso-B"
	const caseID = "CASE-V1HTTP-ISO-1"
	const evidenceID = "EV-V1HTTP-ISO-1"

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/cases", map[string]any{"tenant_id": tenantA, "case_id": caseID, "tick": 0})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/cases (tenant A): expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/evidence", v1SubmitEvidenceBody(tenantA, caseID, evidenceID, 10))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/evidence (tenant A): expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tenant B reading tenant A's case by ID: must be 404, not the data.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/cases/"+caseID+"?tenant_id="+tenantB, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET tenant A's case as tenant B: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tenant B reading tenant A's evidence by ID: must be 404 too.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/evidence/"+evidenceID+"?tenant_id="+tenantB, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET tenant A's evidence as tenant B: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Tenant B attempting to decide tenant A's case: must be refused,
	// no Decision produced.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/cases/"+caseID+"/decide", v1DecideBody(tenantB, caseID, []string{evidenceID}))
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected tenant B to be refused when deciding tenant A's case, got 200")
	}
	resp.Body.Close()

	// Sanity: tenant A itself still works normally.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/cases/"+caseID+"?tenant_id="+tenantA, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET tenant A's own case as tenant A: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestCommercialV1RoutesAbsentWhenStoreNil proves the nil-safety this
// route family's own doc comment promises, mirroring
// TestInsuranceDecideRouteAbsentWhenLedgerNil: every caller of
// NewServer before CommercialStore existed must keep working exactly
// as before -- 404, never a panic.
func TestCommercialV1RoutesAbsentWhenStoreNil(t *testing.T) {
	reg, err := registry.New()
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srv := NewServer("127.0.0.1:0", reg, nil, ServerOptions{})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/cases", map[string]any{"tenant_id": "t", "case_id": "c", "tick": 0})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when no CommercialStore is configured, got %d", resp.StatusCode)
	}
}
