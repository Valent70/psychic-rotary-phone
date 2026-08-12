package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"veriqo/pkg/platform/audit"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestAPIKeyMiddleware(t *testing.T) {
	h := APIKeyMiddleware(okHandler(), map[string]bool{"secret": true}, map[string]bool{"/healthz": true})
	req := httptest.NewRequest(http.MethodGet, "/trust/certify", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d", rr.Code)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/trust/certify", nil)
	req2.Header.Set("X-API-Key", "secret")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid key, got %d", rr2.Code)
	}
}

func TestJWTSignAndVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret")
	tok, err := SignHS256(Claims{Subject: "user-1", Role: "operator", Exp: time.Now().Add(time.Hour).Unix()}, secret)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := VerifyHS256(tok, secret)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" || claims.Role != "operator" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestJWTRejectsExpiredToken(t *testing.T) {
	secret := []byte("s")
	tok, _ := SignHS256(Claims{Subject: "u", Exp: time.Now().Add(-time.Hour).Unix()}, secret)
	if _, err := VerifyHS256(tok, secret); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWTRejectsTamperedSignature(t *testing.T) {
	secret := []byte("s")
	tok, _ := SignHS256(Claims{Subject: "u", Exp: time.Now().Add(time.Hour).Unix()}, secret)
	tampered := tok[:len(tok)-2] + "xx"
	if _, err := VerifyHS256(tampered, secret); err == nil {
		t.Fatal("expected error for tampered signature")
	}
}

func TestJWTRejectsWrongSecret(t *testing.T) {
	tok, _ := SignHS256(Claims{Subject: "u", Exp: time.Now().Add(time.Hour).Unix()}, []byte("secret-a"))
	if _, err := VerifyHS256(tok, []byte("secret-b")); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestJWTMiddlewareAttachesClaims(t *testing.T) {
	secret := []byte("s")
	tok, _ := SignHS256(Claims{Subject: "u", Role: "admin", Exp: time.Now().Add(time.Hour).Unix()}, secret)

	var gotRole string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := claimsFromContext(r.Context())
		gotRole = claims.Role
		w.WriteHeader(http.StatusOK)
	})
	h := JWTMiddleware(inner, secret, nil)

	req := httptest.NewRequest(http.MethodGet, "/policy/evaluate", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if gotRole != "admin" {
		t.Fatalf("expected role propagated via context, got %q", gotRole)
	}
}

func TestJWTMiddlewareRejectsMissingBearer(t *testing.T) {
	h := JWTMiddleware(okHandler(), []byte("s"), nil)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestRBACAllowsMatchingPrefixAndRejectsOthers(t *testing.T) {
	secret := []byte("s")
	table := RoleTable{"operator": {"/trust/", "/evidence/"}, "admin": {"*"}}

	inner := okHandler()
	h := JWTMiddleware(RBACMiddleware(inner, table, nil), secret, nil)

	opTok, _ := SignHS256(Claims{Subject: "u1", Role: "operator", Exp: time.Now().Add(time.Hour).Unix()}, secret)
	req := httptest.NewRequest(http.MethodPost, "/trust/certify", nil)
	req.Header.Set("Authorization", "Bearer "+opTok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected operator allowed on /trust/, got %d", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/policy/evaluate", nil)
	req2.Header.Set("Authorization", "Bearer "+opTok)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("expected operator forbidden on /policy/, got %d", rr2.Code)
	}

	adminTok, _ := SignHS256(Claims{Subject: "u2", Role: "admin", Exp: time.Now().Add(time.Hour).Unix()}, secret)
	req3 := httptest.NewRequest(http.MethodPost, "/policy/evaluate", nil)
	req3.Header.Set("Authorization", "Bearer "+adminTok)
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("expected admin (wildcard) allowed, got %d", rr3.Code)
	}
}

func TestRBACRejectsUnknownRole(t *testing.T) {
	secret := []byte("s")
	table := RoleTable{"admin": {"*"}}
	h := JWTMiddleware(RBACMiddleware(okHandler(), table, nil), secret, nil)
	tok, _ := SignHS256(Claims{Subject: "u", Role: "ghost", Exp: time.Now().Add(time.Hour).Unix()}, secret)
	req := httptest.NewRequest(http.MethodGet, "/trust/certify", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unmapped role, got %d", rr.Code)
	}
}

func TestAuditMiddlewareAppendsHashChainedRecord(t *testing.T) {
	store := audit.NewAuditStore()
	h := AuditMiddleware(okHandler(), store)
	req := httptest.NewRequest(http.MethodGet, "/trust/certify", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	snap := store.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(snap))
	}
	if snap[0].Action != "GET /trust/certify" {
		t.Fatalf("unexpected action: %q", snap[0].Action)
	}
	if err := (audit.Auditor{}).VerifyChain(snap); err != nil {
		t.Fatalf("expected clean audit chain: %v", err)
	}
}

func TestAuditMiddlewareUsesJWTSubjectAsActor(t *testing.T) {
	secret := []byte("s")
	store := audit.NewAuditStore()
	chain := JWTMiddleware(AuditMiddleware(okHandler(), store), secret, nil)
	tok, _ := SignHS256(Claims{Subject: "user-42", Role: "operator", Exp: time.Now().Add(time.Hour).Unix()}, secret)
	req := httptest.NewRequest(http.MethodPost, "/trust/certify", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	snap := store.Snapshot()
	if len(snap) != 1 || snap[0].Actor != "user-42" {
		t.Fatalf("expected audit actor = JWT subject, got %+v", snap)
	}
}

func TestLoadServerTLSConfigMissingFile(t *testing.T) {
	if _, err := LoadServerTLSConfig("/nonexistent/cert.pem", "/nonexistent/key.pem"); err == nil {
		t.Fatal("expected error for missing files")
	}
}

func TestLoadMutualTLSConfigMissingCA(t *testing.T) {
	if _, err := LoadMutualTLSConfig("/nonexistent/cert.pem", "/nonexistent/key.pem", "/nonexistent/ca.pem"); err == nil {
		t.Fatal("expected error for missing files")
	}
}
