// Package api closes PHASES 21-23. The audit was explicit that this is
// "bukan sekadar REST endpoint" — the capability being closed is API
// SEMANTICS:
//
//   - idempotency: key → request hash → stored result, with a hard
//     409 IDEMPOTENCY_CONFLICT when the same key arrives with a
//     different payload;
//   - rate limiting: a deterministic token bucket keyed by
//     tenant/actor/endpoint with explicit burst and refill;
//   - OIDC: real signature, issuer, audience, expiry and claim
//     validation feeding tenant and roles into authorization;
//   - transport parity: REST capability must equal gRPC capability,
//     enforced by a registry rather than by a code review.
//
// Determinism note: nothing here reads the wall clock. Every time-
// dependent operation takes an explicit tick, which is what makes rate
// limiting and token expiry replayable — and what makes the tests below
// able to assert exact bucket levels instead of sleeping.
package api

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Status mirrors the HTTP status an error maps to, so REST and gRPC
// surface the same failure with the same code.
type Status int

const (
	StatusOK                  Status = 200
	StatusBadRequest          Status = 400
	StatusUnauthorized        Status = 401
	StatusForbidden           Status = 403
	StatusNotFound            Status = 404
	StatusIdempotencyConflict Status = 409
	StatusTooManyRequests     Status = 429
	StatusInternal            Status = 500
)

var (
	ErrIdempotencyConflict = errors.New("api: IDEMPOTENCY_CONFLICT")
	ErrInFlight            = errors.New("api: request with this idempotency key is still in flight")
	ErrRateLimited         = errors.New("api: rate limit exceeded")
	ErrTokenMalformed      = errors.New("api: token malformed")
	ErrTokenSignature      = errors.New("api: token signature invalid")
	ErrTokenIssuer         = errors.New("api: token issuer not accepted")
	ErrTokenAudience       = errors.New("api: token audience not accepted")
	ErrTokenExpired        = errors.New("api: token expired")
	ErrTokenNotYetValid    = errors.New("api: token not yet valid")
	ErrTokenClaims         = errors.New("api: token is missing a required claim")
	ErrUnknownKey          = errors.New("api: signing key not found")
	ErrParityViolation     = errors.New("api: REST and gRPC capability sets differ")
	ErrUnsupportedAlg      = errors.New("api: unsupported signing algorithm")
)

// ---------------------------------------------------------------------
// Idempotency
// ---------------------------------------------------------------------

// Entry is a stored idempotent result.
type Entry struct {
	Key         string `json:"key"`
	RequestHash string `json:"request_hash"`
	Endpoint    string `json:"endpoint"`
	Tenant      string `json:"tenant"`
	Status      Status `json:"status"`
	Response    []byte `json:"response"`
	StoredTick  uint64 `json:"stored_tick"`
	Replayed    int    `json:"replayed"`
	Complete    bool   `json:"complete"`
}

// IdempotencyStore holds results keyed by idempotency key.
type IdempotencyStore struct {
	mu      sync.Mutex
	entries map[string]*Entry
	// TTLTicks bounds how long a key is remembered. Zero means forever,
	// which is wrong for a public API and is therefore not the default.
	TTLTicks uint64
}

// NewIdempotencyStore constructs a store.
func NewIdempotencyStore(ttlTicks uint64) *IdempotencyStore {
	return &IdempotencyStore{entries: map[string]*Entry{}, TTLTicks: ttlTicks}
}

// RequestHash canonicalises a request for comparison. Header order and
// JSON key order must not make two identical requests look different,
// so the body is normalised through a sorted re-encode when it parses
// as JSON and hashed verbatim otherwise.
func RequestHash(method, path string, body []byte) string {
	normalised := body
	var v any
	if json.Unmarshal(body, &v) == nil {
		if b, err := json.Marshal(canonicalise(v)); err == nil {
			normalised = b
		}
	}
	h := sha256.New()
	h.Write([]byte("api_request/v1\n" + strings.ToUpper(method) + "\n" + path + "\n"))
	h.Write(normalised)
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalise(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = canonicalise(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = canonicalise(t[i])
		}
		return out
	default:
		return v
	}
}

// Begin claims a key. It returns (entry, true, nil) when a completed
// result already exists and must be replayed verbatim.
//
// The in-flight case is a real error rather than a silent second
// execution: two concurrent requests with one key are a client bug, and
// executing both is exactly the double-charge the mechanism exists to
// prevent.
func (s *IdempotencyStore) Begin(key, endpoint, tenant, requestHash string, tick uint64) (*Entry, bool, error) {
	if key == "" {
		return nil, false, fmt.Errorf("%w: idempotency key is required", ErrTokenClaims)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(tick)

	e, ok := s.entries[key]
	if !ok {
		s.entries[key] = &Entry{Key: key, RequestHash: requestHash, Endpoint: endpoint,
			Tenant: tenant, StoredTick: tick}
		return nil, false, nil
	}
	if e.RequestHash != requestHash || e.Endpoint != endpoint || e.Tenant != tenant {
		return nil, false, fmt.Errorf("%w: key %s was used for a different request", ErrIdempotencyConflict, key)
	}
	if !e.Complete {
		return nil, false, fmt.Errorf("%w: %s", ErrInFlight, key)
	}
	e.Replayed++
	cp := *e
	return &cp, true, nil
}

// Complete stores the result for a claimed key.
func (s *IdempotencyStore) Complete(key string, status Status, response []byte, tick uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return fmt.Errorf("api: idempotency key %s was never begun", key)
	}
	e.Status, e.Response, e.Complete, e.StoredTick = status, append([]byte(nil), response...), true, tick
	return nil
}

// Abandon releases a claimed-but-failed key so the client may retry.
// Without it, a server crash between Begin and Complete would poison the
// key permanently.
func (s *IdempotencyStore) Abandon(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok && !e.Complete {
		delete(s.entries, key)
	}
}

func (s *IdempotencyStore) expireLocked(tick uint64) {
	if s.TTLTicks == 0 {
		return
	}
	for k, e := range s.entries {
		if e.Complete && tick > e.StoredTick+s.TTLTicks {
			delete(s.entries, k)
		}
	}
}

// ---------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------

// LimitRule is a token bucket specification.
type LimitRule struct {
	Endpoint   string  `json:"endpoint"` // "" = default for the tenant
	Burst      float64 `json:"burst"`
	RefillRate float64 `json:"refill_per_tick"`
}

// Decision is a rate-limit verdict.
type Decision struct {
	Allowed        bool    `json:"allowed"`
	Remaining      float64 `json:"remaining"`
	RetryAfterTick uint64  `json:"retry_after_tick"`
	Rule           string  `json:"rule"`
}

type bucket struct {
	tokens   float64
	lastTick uint64
}

// RateLimiter is a deterministic token-bucket limiter keyed by
// tenant|actor|endpoint.
type RateLimiter struct {
	mu      sync.Mutex
	rules   map[string]LimitRule // endpoint -> rule; "" is the default
	buckets map[string]*bucket
}

// NewRateLimiter constructs a limiter with a default rule.
func NewRateLimiter(def LimitRule) *RateLimiter {
	def.Endpoint = ""
	return &RateLimiter{rules: map[string]LimitRule{"": def}, buckets: map[string]*bucket{}}
}

// SetRule installs a per-endpoint rule.
func (r *RateLimiter) SetRule(rule LimitRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[rule.Endpoint] = rule
}

// Allow consumes one token.
//
// The bucket is keyed by tenant AND actor AND endpoint on purpose: a
// tenant-only key lets one noisy service account starve every other
// user of the same tenant, and an actor-only key lets one tenant's
// traffic pattern set another tenant's limits.
func (r *RateLimiter) Allow(tenant, actor, endpoint string, tick uint64) Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, ok := r.rules[endpoint]
	if !ok {
		rule = r.rules[""]
		rule.Endpoint = ""
	}
	key := tenant + "|" + actor + "|" + endpoint
	b, ok := r.buckets[key]
	if !ok {
		b = &bucket{tokens: rule.Burst, lastTick: tick}
		r.buckets[key] = b
	}
	if tick > b.lastTick {
		b.tokens += float64(tick-b.lastTick) * rule.RefillRate
		if b.tokens > rule.Burst {
			b.tokens = rule.Burst
		}
		b.lastTick = tick
	}
	name := rule.Endpoint
	if name == "" {
		name = "default"
	}
	if b.tokens < 1 {
		retry := tick
		if rule.RefillRate > 0 {
			need := 1 - b.tokens
			retry = tick + uint64(need/rule.RefillRate) + 1
		}
		return Decision{Allowed: false, Remaining: b.tokens, RetryAfterTick: retry, Rule: name}
	}
	b.tokens--
	return Decision{Allowed: true, Remaining: b.tokens, Rule: name}
}

// ---------------------------------------------------------------------
// OIDC
// ---------------------------------------------------------------------

// JWK is one RSA public key in the local key set.
type JWK struct {
	KeyID string
	Key   *rsa.PublicKey
}

// Claims is the validated identity extracted from a token.
type Claims struct {
	Subject   string   `json:"sub"`
	Issuer    string   `json:"iss"`
	Audience  []string `json:"aud"`
	Tenant    string   `json:"tenant"`
	Roles     []string `json:"roles"`
	IssuedAt  uint64   `json:"iat"`
	Expiry    uint64   `json:"exp"`
	NotBefore uint64   `json:"nbf"`
	KeyID     string   `json:"kid"`
}

// Verifier validates OIDC tokens against a local key set.
type Verifier struct {
	mu               sync.RWMutex
	keys             map[string]*rsa.PublicKey
	AcceptedIssuer   string
	AcceptedAudience string
	RequiredClaims   []string
	// LeewayTicks absorbs small clock differences. It is explicit
	// because an implicit leeway is how an expired token gets accepted.
	LeewayTicks uint64
}

// NewVerifier constructs a Verifier.
func NewVerifier(issuer, audience string) *Verifier {
	return &Verifier{keys: map[string]*rsa.PublicKey{},
		AcceptedIssuer: issuer, AcceptedAudience: audience,
		RequiredClaims: []string{"sub", "tenant"}}
}

// AddKey registers a public key by key id.
func (v *Verifier) AddKey(kid string, key *rsa.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[kid] = key
}

// AddKeyPEM registers a PKIX-encoded RSA public key.
func (v *Verifier) AddKeyPEM(kid string, der []byte) error {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return err
	}
	rp, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: not an RSA key", ErrUnsupportedAlg)
	}
	v.AddKey(kid, rp)
	return nil
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type jwtPayload struct {
	Sub    string          `json:"sub"`
	Iss    string          `json:"iss"`
	Aud    json.RawMessage `json:"aud"`
	Tenant string          `json:"tenant"`
	Roles  []string        `json:"roles"`
	Iat    uint64          `json:"iat"`
	Exp    uint64          `json:"exp"`
	Nbf    uint64          `json:"nbf"`
}

// Verify validates a compact JWS and returns the claims.
//
// Validation order matters and is deliberate: signature FIRST, then
// issuer, audience, time and claims. Reading claims from an unverified
// token — even to pick the key — is how key-confusion attacks start, so
// only the header's kid is read before verification, and an unknown kid
// is an error rather than a fall-through to "try every key".
func (v *Verifier) Verify(token string, nowTick uint64) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: expected three segments", ErrTokenMalformed)
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: header is not base64url", ErrTokenMalformed)
	}
	var h jwtHeader
	if err := json.Unmarshal(hb, &h); err != nil {
		return Claims{}, fmt.Errorf("%w: header is not JSON", ErrTokenMalformed)
	}
	if h.Alg != "RS256" {
		// "none" and HMAC algorithms are refused outright rather than
		// handled: algorithm agility in a verifier is the vulnerability.
		return Claims{}, fmt.Errorf("%w: %s", ErrUnsupportedAlg, h.Alg)
	}
	v.mu.RLock()
	key, ok := v.keys[h.Kid]
	v.mu.RUnlock()
	if !ok {
		return Claims{}, fmt.Errorf("%w: kid %q", ErrUnknownKey, h.Kid)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: signature is not base64url", ErrTokenMalformed)
	}
	signed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, signed[:], sig); err != nil {
		return Claims{}, ErrTokenSignature
	}

	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: payload is not base64url", ErrTokenMalformed)
	}
	var p jwtPayload
	if err := json.Unmarshal(pb, &p); err != nil {
		return Claims{}, fmt.Errorf("%w: payload is not JSON", ErrTokenMalformed)
	}
	aud := decodeAudience(p.Aud)

	if v.AcceptedIssuer != "" && p.Iss != v.AcceptedIssuer {
		return Claims{}, fmt.Errorf("%w: %q", ErrTokenIssuer, p.Iss)
	}
	if v.AcceptedAudience != "" && !containsStr(aud, v.AcceptedAudience) {
		return Claims{}, fmt.Errorf("%w: %v", ErrTokenAudience, aud)
	}
	if p.Exp != 0 && nowTick > p.Exp+v.LeewayTicks {
		return Claims{}, fmt.Errorf("%w at %d (now %d)", ErrTokenExpired, p.Exp, nowTick)
	}
	if p.Nbf != 0 && nowTick+v.LeewayTicks < p.Nbf {
		return Claims{}, fmt.Errorf("%w until %d (now %d)", ErrTokenNotYetValid, p.Nbf, nowTick)
	}
	for _, c := range v.RequiredClaims {
		switch c {
		case "sub":
			if p.Sub == "" {
				return Claims{}, fmt.Errorf("%w: sub", ErrTokenClaims)
			}
		case "tenant":
			if p.Tenant == "" {
				return Claims{}, fmt.Errorf("%w: tenant", ErrTokenClaims)
			}
		case "roles":
			if len(p.Roles) == 0 {
				return Claims{}, fmt.Errorf("%w: roles", ErrTokenClaims)
			}
		}
	}
	roles := append([]string(nil), p.Roles...)
	sort.Strings(roles)
	return Claims{Subject: p.Sub, Issuer: p.Iss, Audience: aud, Tenant: p.Tenant,
		Roles: roles, IssuedAt: p.Iat, Expiry: p.Exp, NotBefore: p.Nbf, KeyID: h.Kid}, nil
}

func decodeAudience(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Transport parity
// ---------------------------------------------------------------------

// Capability is one API operation, independent of transport.
type Capability struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Idempotent   bool     `json:"idempotent"`
	RequiredRole string   `json:"required_role"`
	Endpoints    []string `json:"endpoints"`
}

// Registry enforces REST/gRPC parity.
type Registry struct {
	mu   sync.RWMutex
	caps map[string]Capability
	rest map[string]bool
	grpc map[string]bool
}

// NewRegistry constructs a Registry.
func NewRegistry() *Registry {
	return &Registry{caps: map[string]Capability{}, rest: map[string]bool{}, grpc: map[string]bool{}}
}

// Declare registers a capability.
func (r *Registry) Declare(c Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps[c.Name] = c
}

// BindREST records that a capability is reachable over REST.
func (r *Registry) BindREST(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rest[name] = true
}

// BindGRPC records that a capability is reachable over gRPC.
func (r *Registry) BindGRPC(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.grpc[name] = true
}

// VerifyParity fails when the two transports expose different sets.
// It names both directions, because "gRPC is missing X" and "REST is
// missing Y" have different owners.
func (r *Registry) VerifyParity() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var restOnly, grpcOnly, unbound []string
	for name := range r.caps {
		inR, inG := r.rest[name], r.grpc[name]
		switch {
		case inR && !inG:
			restOnly = append(restOnly, name)
		case inG && !inR:
			grpcOnly = append(grpcOnly, name)
		case !inR && !inG:
			unbound = append(unbound, name)
		}
	}
	sort.Strings(restOnly)
	sort.Strings(grpcOnly)
	sort.Strings(unbound)
	if len(restOnly)+len(grpcOnly)+len(unbound) == 0 {
		return nil
	}
	var parts []string
	if len(restOnly) > 0 {
		parts = append(parts, "gRPC missing: "+strings.Join(restOnly, ","))
	}
	if len(grpcOnly) > 0 {
		parts = append(parts, "REST missing: "+strings.Join(grpcOnly, ","))
	}
	if len(unbound) > 0 {
		parts = append(parts, "unbound: "+strings.Join(unbound, ","))
	}
	return fmt.Errorf("%w: %s", ErrParityViolation, strings.Join(parts, "; "))
}

// Capabilities returns the declared set, sorted.
func (r *Registry) Capabilities() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------------------------------------------------------------------
// Gateway — the composed request path
// ---------------------------------------------------------------------

// Request is one inbound call, transport-independent.
type Request struct {
	Capability     string
	Method         string
	Path           string
	Body           []byte
	Token          string
	IdempotencyKey string
	Tick           uint64
}

// Response is the outcome.
type Response struct {
	Status   Status   `json:"status"`
	Body     []byte   `json:"body"`
	Replayed bool     `json:"replayed"`
	Claims   Claims   `json:"claims"`
	Limit    Decision `json:"limit"`
	Error    string   `json:"error,omitempty"`
}

// Gateway composes authentication, rate limiting and idempotency in the
// one order that is correct: authenticate, then limit (so an
// unauthenticated flood cannot consume a tenant's budget), then
// idempotency, then handle.
type Gateway struct {
	Verifier    *Verifier
	Limiter     *RateLimiter
	Idempotency *IdempotencyStore
	Registry    *Registry
}

// Handle runs the request path. handler is invoked only when the
// request is authenticated, within limits and not a replay.
func (g *Gateway) Handle(req Request, handler func(Request, Claims) (Status, []byte, error)) Response {
	claims, err := g.Verifier.Verify(req.Token, req.Tick)
	if err != nil {
		return Response{Status: StatusUnauthorized, Error: err.Error()}
	}
	cap, known := g.capability(req.Capability)
	if !known {
		return Response{Status: StatusNotFound, Claims: claims,
			Error: "unknown capability " + req.Capability}
	}
	if cap.RequiredRole != "" && !containsStr(claims.Roles, cap.RequiredRole) {
		return Response{Status: StatusForbidden, Claims: claims,
			Error: "role " + cap.RequiredRole + " required"}
	}
	dec := g.Limiter.Allow(claims.Tenant, claims.Subject, req.Capability, req.Tick)
	if !dec.Allowed {
		return Response{Status: StatusTooManyRequests, Claims: claims, Limit: dec,
			Error: ErrRateLimited.Error()}
	}
	if !cap.Idempotent {
		st, body, err := handler(req, claims)
		return responseFrom(st, body, err, claims, dec, false)
	}
	rh := RequestHash(req.Method, req.Path, req.Body)
	prior, replay, err := g.Idempotency.Begin(req.IdempotencyKey, req.Capability, claims.Tenant, rh, req.Tick)
	if err != nil {
		st := StatusIdempotencyConflict
		if errors.Is(err, ErrInFlight) {
			st = StatusIdempotencyConflict
		}
		return Response{Status: st, Claims: claims, Limit: dec, Error: err.Error()}
	}
	if replay {
		return Response{Status: prior.Status, Body: prior.Response, Replayed: true,
			Claims: claims, Limit: dec}
	}
	st, body, herr := handler(req, claims)
	if herr != nil {
		g.Idempotency.Abandon(req.IdempotencyKey)
		return responseFrom(st, body, herr, claims, dec, false)
	}
	// Complete can genuinely fail here (not a provably-impossible path):
	// IdempotencyStore is shared across concurrent requests, and Begin
	// (line above, called moments ago on this same key) sweeps TTL-
	// expired entries using whatever tick THAT call carried -- so a
	// different, concurrently-running request's own Begin call, with a
	// later tick, can expire and delete this key while THIS handler is
	// still executing, if handler() runs slower than the store's TTL
	// window. The accepted degradation when that happens: this
	// request's own response is still returned correctly below: it is
	// only a FUTURE retry with the same idempotency key that loses the
	// replay guarantee and re-executes the handler, rather than
	// receiving this call's cached response. That is a narrowing of the
	// idempotency guarantee under a real TTL race, not a correctness
	// bug in the current request/response cycle, so it is not treated
	// as a request-failing error.
	_ = g.Idempotency.Complete(req.IdempotencyKey, st, body, req.Tick)
	return responseFrom(st, body, nil, claims, dec, false)
}

func (g *Gateway) capability(name string) (Capability, bool) {
	if g.Registry == nil {
		return Capability{Name: name, Idempotent: true}, true
	}
	g.Registry.mu.RLock()
	defer g.Registry.mu.RUnlock()
	c, ok := g.Registry.caps[name]
	return c, ok
}

func responseFrom(st Status, body []byte, err error, c Claims, d Decision, replayed bool) Response {
	r := Response{Status: st, Body: body, Claims: c, Limit: d, Replayed: replayed}
	if err != nil {
		if r.Status == 0 || r.Status == StatusOK {
			r.Status = StatusInternal
		}
		r.Error = err.Error()
	}
	return r
}
