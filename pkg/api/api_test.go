package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

// --- token helpers -----------------------------------------------------

var testKey *rsa.PrivateKey

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	if testKey == nil {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		testKey = k
	}
	return testKey
}

func sign(t *testing.T, k *rsa.PrivateKey, kid string, payload map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	pb, _ := json.Marshal(payload)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func verifier(t *testing.T) *Verifier {
	t.Helper()
	v := NewVerifier("https://issuer.veriqo.test", "veriqo-api")
	v.AddKey("k1", &key(t).PublicKey)
	return v
}

func goodClaims() map[string]any {
	return map[string]any{
		"sub": "svc-analyst", "iss": "https://issuer.veriqo.test", "aud": "veriqo-api",
		"tenant": "acme", "roles": []string{"analyst"}, "iat": 10, "exp": 1000, "nbf": 5,
	}
}

// --- idempotency -------------------------------------------------------

func TestSameKeySamePayloadReplaysTheStoredResult(t *testing.T) {
	s := NewIdempotencyStore(0)
	rh := RequestHash("POST", "/v1/cases", []byte(`{"a":1}`))
	if _, replay, err := s.Begin("k", "createCase", "acme", rh, 1); err != nil || replay {
		t.Fatalf("first call must execute: replay=%v err=%v", replay, err)
	}
	if err := s.Complete("k", StatusOK, []byte(`{"id":"c1"}`), 2); err != nil {
		t.Fatal(err)
	}
	prior, replay, err := s.Begin("k", "createCase", "acme", rh, 3)
	if err != nil || !replay {
		t.Fatalf("second call must replay: %v %v", replay, err)
	}
	if string(prior.Response) != `{"id":"c1"}` || prior.Status != StatusOK {
		t.Fatalf("the stored result must be returned verbatim: %+v", prior)
	}
}

func TestSameKeyDifferentPayloadIsAConflict(t *testing.T) {
	s := NewIdempotencyStore(0)
	a := RequestHash("POST", "/v1/cases", []byte(`{"a":1}`))
	b := RequestHash("POST", "/v1/cases", []byte(`{"a":2}`))
	_, _, _ = s.Begin("k", "createCase", "acme", a, 1)
	_ = s.Complete("k", StatusOK, []byte("ok"), 1)
	if _, _, err := s.Begin("k", "createCase", "acme", b, 2); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected IDEMPOTENCY_CONFLICT, got %v", err)
	}
}

func TestJSONKeyOrderDoesNotChangeTheRequestHash(t *testing.T) {
	a := RequestHash("POST", "/v1/cases", []byte(`{"a":1,"b":{"x":1,"y":2}}`))
	b := RequestHash("POST", "/v1/cases", []byte(`{"b":{"y":2,"x":1},"a":1}`))
	if a != b {
		t.Fatal("two identical requests must hash identically regardless of key order")
	}
	c := RequestHash("POST", "/v1/cases", []byte(`{"a":2}`))
	if a == c {
		t.Fatal("different payloads must hash differently")
	}
}

func TestConcurrentInFlightKeyIsRefusedNotExecutedTwice(t *testing.T) {
	s := NewIdempotencyStore(0)
	rh := RequestHash("POST", "/x", []byte(`{}`))
	_, _, _ = s.Begin("k", "e", "t", rh, 1)
	if _, _, err := s.Begin("k", "e", "t", rh, 1); !errors.Is(err, ErrInFlight) {
		t.Fatalf("an in-flight key must not execute twice, got %v", err)
	}
}

func TestAbandonAllowsRetryAfterFailure(t *testing.T) {
	s := NewIdempotencyStore(0)
	rh := RequestHash("POST", "/x", []byte(`{}`))
	_, _, _ = s.Begin("k", "e", "t", rh, 1)
	s.Abandon("k")
	if _, replay, err := s.Begin("k", "e", "t", rh, 2); err != nil || replay {
		t.Fatalf("an abandoned key must be retryable: %v %v", replay, err)
	}
}

func TestIdempotencyTTLExpiresCompletedKeys(t *testing.T) {
	s := NewIdempotencyStore(10)
	rh := RequestHash("POST", "/x", []byte(`{}`))
	_, _, _ = s.Begin("k", "e", "t", rh, 1)
	_ = s.Complete("k", StatusOK, []byte("v"), 1)
	if _, replay, _ := s.Begin("k", "e", "t", rh, 5); !replay {
		t.Fatal("inside the TTL the key must still replay")
	}
	if _, replay, _ := s.Begin("k", "e", "t", rh, 100); replay {
		t.Fatal("after the TTL the key must be forgotten")
	}
}

// --- rate limiting -----------------------------------------------------

func TestTokenBucketBurstThenRefill(t *testing.T) {
	l := NewRateLimiter(LimitRule{Burst: 3, RefillRate: 0.5})
	for i := 0; i < 3; i++ {
		if d := l.Allow("acme", "a", "read", 0); !d.Allowed {
			t.Fatalf("burst call %d must be allowed", i)
		}
	}
	d := l.Allow("acme", "a", "read", 0)
	if d.Allowed {
		t.Fatal("the fourth call must be limited")
	}
	if d.RetryAfterTick <= 0 {
		t.Fatalf("a retry hint is mandatory: %+v", d)
	}
	if d2 := l.Allow("acme", "a", "read", 2); !d2.Allowed {
		t.Fatalf("after 2 ticks one token must have refilled: %+v", d2)
	}
}

func TestBucketsAreIsolatedPerTenantActorAndEndpoint(t *testing.T) {
	l := NewRateLimiter(LimitRule{Burst: 1, RefillRate: 0})
	if !l.Allow("t1", "a1", "e1", 0).Allowed {
		t.Fatal("first call")
	}
	if l.Allow("t1", "a1", "e1", 0).Allowed {
		t.Fatal("same bucket must be exhausted")
	}
	if !l.Allow("t2", "a1", "e1", 0).Allowed {
		t.Fatal("a different tenant must have its own bucket")
	}
	if !l.Allow("t1", "a2", "e1", 0).Allowed {
		t.Fatal("a different actor must have its own bucket")
	}
	if !l.Allow("t1", "a1", "e2", 0).Allowed {
		t.Fatal("a different endpoint must have its own bucket")
	}
}

func TestPerEndpointRuleOverridesTheDefault(t *testing.T) {
	l := NewRateLimiter(LimitRule{Burst: 10, RefillRate: 1})
	l.SetRule(LimitRule{Endpoint: "expensive", Burst: 1, RefillRate: 0})
	if !l.Allow("t", "a", "expensive", 0).Allowed {
		t.Fatal("first expensive call")
	}
	if l.Allow("t", "a", "expensive", 0).Allowed {
		t.Fatal("the endpoint rule must bind, not the default")
	}
	if !l.Allow("t", "a", "cheap", 0).Allowed {
		t.Fatal("the default rule must still apply elsewhere")
	}
}

func TestRateLimitingIsDeterministic(t *testing.T) {
	run := func() []bool {
		l := NewRateLimiter(LimitRule{Burst: 2, RefillRate: 0.25})
		var out []bool
		for tick := uint64(0); tick < 20; tick++ {
			out = append(out, l.Allow("t", "a", "e", tick).Allowed)
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("rate limiting must be deterministic; diverged at %d", i)
		}
	}
}

// --- OIDC --------------------------------------------------------------

func TestValidTokenIsAccepted(t *testing.T) {
	v := verifier(t)
	c, err := v.Verify(sign(t, key(t), "k1", goodClaims()), 100)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tenant != "acme" || c.Subject != "svc-analyst" || len(c.Roles) != 1 {
		t.Fatalf("claims not extracted: %+v", c)
	}
}

func TestTamperedPayloadFailsSignature(t *testing.T) {
	v := verifier(t)
	tok := sign(t, key(t), "k1", goodClaims())
	forged := map[string]any{"sub": "svc-analyst", "iss": "https://issuer.veriqo.test",
		"aud": "veriqo-api", "tenant": "victim-tenant", "roles": []string{"admin"},
		"exp": 1000, "nbf": 5}
	pb, _ := json.Marshal(forged)
	parts := []byte(tok)
	_ = parts
	// Splice the forged payload onto the original header and signature.
	var hdr, sig string
	for i, p := range splitToken(tok) {
		switch i {
		case 0:
			hdr = p
		case 2:
			sig = p
		}
	}
	spliced := hdr + "." + base64.RawURLEncoding.EncodeToString(pb) + "." + sig
	if _, err := v.Verify(spliced, 100); !errors.Is(err, ErrTokenSignature) {
		t.Fatalf("a spliced payload must fail signature verification, got %v", err)
	}
}

func TestAlgNoneIsRefused(t *testing.T) {
	v := verifier(t)
	hb, _ := json.Marshal(map[string]string{"alg": "none", "kid": "k1"})
	pb, _ := json.Marshal(goodClaims())
	tok := base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(pb) + "."
	if _, err := v.Verify(tok, 100); !errors.Is(err, ErrUnsupportedAlg) && !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("alg=none must be refused, got %v", err)
	}
}

func TestUnknownKeyIDIsRefusedRatherThanTryingEveryKey(t *testing.T) {
	v := verifier(t)
	tok := sign(t, key(t), "attacker-kid", goodClaims())
	if _, err := v.Verify(tok, 100); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an unknown kid must be refused, got %v", err)
	}
}

func TestIssuerAudienceExpiryAndNotBeforeAreEnforced(t *testing.T) {
	v := verifier(t)
	cases := []struct {
		name string
		mut  func(map[string]any)
		tick uint64
		want error
	}{
		{"issuer", func(m map[string]any) { m["iss"] = "https://evil.test" }, 100, ErrTokenIssuer},
		{"audience", func(m map[string]any) { m["aud"] = "other-api" }, 100, ErrTokenAudience},
		{"expiry", func(m map[string]any) {}, 5000, ErrTokenExpired},
		{"notbefore", func(m map[string]any) {}, 1, ErrTokenNotYetValid},
		{"tenant", func(m map[string]any) { delete(m, "tenant") }, 100, ErrTokenClaims},
	}
	for _, c := range cases {
		claims := goodClaims()
		c.mut(claims)
		_, err := v.Verify(sign(t, key(t), "k1", claims), c.tick)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
}

func TestAudienceArrayIsAccepted(t *testing.T) {
	v := verifier(t)
	claims := goodClaims()
	claims["aud"] = []string{"other", "veriqo-api"}
	if _, err := v.Verify(sign(t, key(t), "k1", claims), 100); err != nil {
		t.Fatalf("an audience array containing the accepted value must pass: %v", err)
	}
}

func TestMalformedTokenIsRefused(t *testing.T) {
	v := verifier(t)
	for _, tok := range []string{"", "a.b", "not-base64.$$$.zz", "a.b.c.d"} {
		if _, err := v.Verify(tok, 100); err == nil {
			t.Fatalf("malformed token %q must be refused", tok)
		}
	}
}

// --- parity ------------------------------------------------------------

func TestParityViolationIsDetectedInBothDirections(t *testing.T) {
	r := NewRegistry()
	r.Declare(Capability{Name: "createCase", Idempotent: true})
	r.Declare(Capability{Name: "replayCase"})
	r.BindREST("createCase")
	r.BindGRPC("createCase")
	r.BindREST("replayCase")
	err := r.VerifyParity()
	if !errors.Is(err, ErrParityViolation) || !contains(err.Error(), "gRPC missing") {
		t.Fatalf("a REST-only capability must be reported, got %v", err)
	}
	r.BindGRPC("replayCase")
	if err := r.VerifyParity(); err != nil {
		t.Fatalf("full parity must pass: %v", err)
	}
	r.Declare(Capability{Name: "orphan"})
	if err := r.VerifyParity(); err == nil || !contains(err.Error(), "unbound") {
		t.Fatalf("an unbound capability must be reported, got %v", err)
	}
}

// --- gateway -----------------------------------------------------------

func gateway(t *testing.T) *Gateway {
	t.Helper()
	r := NewRegistry()
	r.Declare(Capability{Name: "createCase", Idempotent: true, RequiredRole: "analyst"})
	r.Declare(Capability{Name: "adminPurge", RequiredRole: "admin"})
	r.BindREST("createCase")
	r.BindGRPC("createCase")
	r.BindREST("adminPurge")
	r.BindGRPC("adminPurge")
	return &Gateway{Verifier: verifier(t), Limiter: NewRateLimiter(LimitRule{Burst: 2, RefillRate: 0}),
		Idempotency: NewIdempotencyStore(0), Registry: r}
}

func TestGatewayRejectsBeforeConsumingRateBudget(t *testing.T) {
	g := gateway(t)
	calls := 0
	h := func(Request, Claims) (Status, []byte, error) { calls++; return StatusOK, []byte("ok"), nil }
	res := g.Handle(Request{Capability: "createCase", Token: "garbage", Tick: 100}, h)
	if res.Status != StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Status)
	}
	if calls != 0 {
		t.Fatal("the handler must not run for an unauthenticated request")
	}
	// The rate budget must be untouched by the rejected request.
	tok := sign(t, key(t), "k1", goodClaims())
	for i := 0; i < 2; i++ {
		r := g.Handle(Request{Capability: "createCase", Token: tok, Method: "POST", Path: "/c",
			Body: []byte(`{"n":` + string(rune('0'+i)) + `}`), IdempotencyKey: "k" + string(rune('0'+i)),
			Tick: 100}, h)
		if r.Status != StatusOK {
			t.Fatalf("call %d should succeed, got %d %s", i, r.Status, r.Error)
		}
	}
}

func TestGatewayEnforcesRoles(t *testing.T) {
	g := gateway(t)
	tok := sign(t, key(t), "k1", goodClaims())
	res := g.Handle(Request{Capability: "adminPurge", Token: tok, Tick: 100},
		func(Request, Claims) (Status, []byte, error) { return StatusOK, nil, nil })
	if res.Status != StatusForbidden {
		t.Fatalf("expected 403 for a missing role, got %d", res.Status)
	}
}

func TestGatewayReplaysIdempotentResultWithoutRerunningTheHandler(t *testing.T) {
	g := gateway(t)
	tok := sign(t, key(t), "k1", goodClaims())
	calls := 0
	h := func(Request, Claims) (Status, []byte, error) {
		calls++
		return StatusOK, []byte("created"), nil
	}
	req := Request{Capability: "createCase", Token: tok, Method: "POST", Path: "/c",
		Body: []byte(`{"a":1}`), IdempotencyKey: "idem-1", Tick: 100}
	if r := g.Handle(req, h); r.Status != StatusOK || r.Replayed {
		t.Fatalf("first call: %+v", r)
	}
	r2 := g.Handle(req, h)
	if !r2.Replayed || string(r2.Body) != "created" {
		t.Fatalf("second call must replay: %+v", r2)
	}
	if calls != 1 {
		t.Fatalf("the handler must run exactly once, ran %d times", calls)
	}
}

func TestGatewayReturns409OnIdempotencyConflict(t *testing.T) {
	g := gateway(t)
	tok := sign(t, key(t), "k1", goodClaims())
	h := func(Request, Claims) (Status, []byte, error) { return StatusOK, []byte("x"), nil }
	base := Request{Capability: "createCase", Token: tok, Method: "POST", Path: "/c",
		IdempotencyKey: "same", Tick: 100}
	base.Body = []byte(`{"a":1}`)
	_ = g.Handle(base, h)
	base.Body = []byte(`{"a":999}`)
	r := g.Handle(base, h)
	if r.Status != StatusIdempotencyConflict {
		t.Fatalf("expected 409, got %d (%s)", r.Status, r.Error)
	}
}

func TestGatewayReturns429WhenLimited(t *testing.T) {
	g := gateway(t)
	tok := sign(t, key(t), "k1", goodClaims())
	h := func(Request, Claims) (Status, []byte, error) { return StatusOK, []byte("x"), nil }
	for i := 0; i < 2; i++ {
		g.Handle(Request{Capability: "createCase", Token: tok, IdempotencyKey: "a" + string(rune('0'+i)),
			Method: "POST", Path: "/c", Body: []byte(`{}`), Tick: 5}, h)
	}
	r := g.Handle(Request{Capability: "createCase", Token: tok, IdempotencyKey: "z",
		Method: "POST", Path: "/c", Body: []byte(`{}`), Tick: 5}, h)
	if r.Status != StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", r.Status)
	}
}

func TestFailedHandlerDoesNotPoisonTheIdempotencyKey(t *testing.T) {
	g := gateway(t)
	tok := sign(t, key(t), "k1", goodClaims())
	fail := func(Request, Claims) (Status, []byte, error) {
		return StatusInternal, nil, errors.New("downstream unavailable")
	}
	ok := func(Request, Claims) (Status, []byte, error) { return StatusOK, []byte("done"), nil }
	req := Request{Capability: "createCase", Token: tok, Method: "POST", Path: "/c",
		Body: []byte(`{}`), IdempotencyKey: "retry-me", Tick: 5}
	if r := g.Handle(req, fail); r.Status != StatusInternal {
		t.Fatalf("expected 500, got %d", r.Status)
	}
	if r := g.Handle(req, ok); r.Status != StatusOK || r.Replayed {
		t.Fatalf("the retry must execute, got %+v", r)
	}
}

func TestUnknownCapabilityIs404(t *testing.T) {
	g := gateway(t)
	tok := sign(t, key(t), "k1", goodClaims())
	r := g.Handle(Request{Capability: "nope", Token: tok, Tick: 100},
		func(Request, Claims) (Status, []byte, error) { return StatusOK, nil, nil })
	if r.Status != StatusNotFound {
		t.Fatalf("expected 404, got %d", r.Status)
	}
}

func splitToken(tok string) []string {
	var out []string
	cur := ""
	for _, r := range tok {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
