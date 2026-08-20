package security

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
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
	tampered := tamperJWTSignature(t, tok)
	if _, err := VerifyHS256(tampered, secret); err == nil {
		t.Fatal("expected error for tampered signature")
	}
}

// TestTamperJWTSignatureGuaranteesADifferentDecodedSignature is the
// direct, structural proof behind tamperJWTSignature's own claim: the
// decoded signature bytes must differ, unconditionally, not merely
// with high probability. Run across many distinct real tokens (each
// signed with its own wall-clock-dependent Exp claim, so each exercises
// a different real signature value) to demonstrate this holds
// regardless of what the original signature's trailing byte happened
// to be -- the exact property flipLastChar's character-level approach
// could not guarantee.
func TestTamperJWTSignatureGuaranteesADifferentDecodedSignature(t *testing.T) {
	secret := []byte("s")
	for i := 0; i < 200; i++ {
		tok, err := SignHS256(Claims{Subject: "u", Exp: time.Now().Add(time.Duration(i) * time.Second).Unix()}, secret)
		if err != nil {
			t.Fatalf("SignHS256: %v", err)
		}
		tampered := tamperJWTSignature(t, tok)

		origParts := strings.Split(tok, ".")
		tampParts := strings.Split(tampered, ".")
		if origParts[0] != tampParts[0] || origParts[1] != tampParts[1] {
			t.Fatal("tamperJWTSignature must leave header and payload untouched")
		}
		origSig, err := base64.RawURLEncoding.DecodeString(origParts[2])
		if err != nil {
			t.Fatalf("decode original signature: %v", err)
		}
		tampSig, err := base64.RawURLEncoding.DecodeString(tampParts[2])
		if err != nil {
			t.Fatalf("decode tampered signature: %v", err)
		}
		if len(origSig) != len(tampSig) {
			t.Fatalf("iteration %d: signature length changed: %d -> %d", i, len(origSig), len(tampSig))
		}
		if origSig[len(origSig)-1] == tampSig[len(tampSig)-1] {
			t.Fatalf("iteration %d: tampered decoded signature's last byte equals the original's -- the exact collision class this fix exists to eliminate", i)
		}
	}
}

// tamperJWTSignature decodes the JWT's own signature segment to raw
// bytes, XORs the last one with 0xFF, and re-encodes -- guaranteeing
// the DECODED signature differs, not merely the encoded character.
//
// This is a real, second-generation fix: this test's own prior
// approach, flipLastChar, replaced only the token's last ENCODED
// character with a fixed literal ('x' or 'y') -- closing the earlier-
// diagnosed "same literal as the original" flake class, but
// reintroducing a subtler instance of the identical root problem.
// base64 (RawURLEncoding, no padding) over a 32-byte HMAC-SHA256
// signature produces 43 characters, and the LAST character encodes
// only 4 real bits of signature data plus 2 always-zero padding bits
// Go's decoder does not verify are actually zero -- so 4 of the 64
// base64 alphabet characters (confirmed empirically: exactly 3 others
// besides the original) decode to the IDENTICAL raw signature bytes
// despite being different glyphs. flipLastChar's fixed replacement
// had roughly a 3-in-63 chance, for any given real (wall-clock-
// dependent, hence different every run) signature, of landing in that
// collision set -- reproducing live on real GitHub Actions CI
// ("expected error for tampered signature") despite dozens of clean
// local runs, exactly the kind of run-dependent flake this discipline
// exists to catch rather than dismiss as noise. Operating on the
// DECODED bytes, the same technique pkg/platform/security/keys's own
// flipLastHexByte already uses, has no such collision class: XOR 0xFF
// changes the actual signature value unconditionally.
func tamperJWTSignature(t *testing.T, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected a 3-part JWT (header.payload.signature), got %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("cannot tamper an empty signature")
	}
	sig[len(sig)-1] ^= 0xFF
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	return strings.Join(parts, ".")
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
