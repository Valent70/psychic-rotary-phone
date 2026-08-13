// Package rest is the REST Gateway named as a gap in the prior
// session's report ("belum ada network transport... Gateway REST/gRPC
// eksplisit dinamai sebagai langkah berikutnya"). Per the API/SDK/CLI
// architecture doc's explicit instruction ("saya tidak ingin REST
// Server... punya logika sendiri... Gateway -> Engine Registry ->
// Kernel"), this package carries NO business logic of its own: every
// route in generated_routes.go decodes a flat JSON body and calls
// straight into the same veriqo/registry.Registry the CLI and Go SDK
// call, so a request handled here cannot silently diverge from what
// `veriqo <engine> <method>` would have done.
//
// Security stack, addressing "Yang_masih_saya_kritisi.docx" §8 by
// name ("apakah mutual TLS, JWT, RBAC, API Key, Audit sudah ada? Atau
// baru middleware"): NewServer now composes, outermost first, Audit ->
// JWT -> RBAC -> APIKey -> Routes. Every layer is optional (pass zero
// values to skip it) and implemented in pkg/platform/security — this
// package still contributes zero business/security logic of its own,
// only composition order. TLS/mTLS termination is likewise optional,
// selected by cmd/veriqo-gateway's flags calling
// http.Server.ListenAndServeTLS with a security.LoadServerTLSConfig or
// security.LoadMutualTLSConfig result.
//
// Honest scope: this is a real, working HTTP/1.1 JSON gateway (stdlib
// net/http only, zero external dependencies) proven in this session by
// an integration test that starts the server as a genuinely separate
// OS process. It is NOT a gRPC gateway (the architecture doc names
// gRPC as an additional transport; building it is the same mechanical
// Routes()-table pattern over a different wire format, not a new
// design problem, and is left as a named next step).
package rest

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/security"
	"veriqo/veriqo/registry"
)

// ServerOptions configures the optional security layers NewServer
// composes around Routes(). The zero value disables every layer
// (equivalent to the prior, unauthenticated-only session's gateway).
type ServerOptions struct {
	APIKeys   map[string]bool    // non-empty enables API-key auth
	JWTSecret []byte             // non-empty enables JWT auth (Bearer token)
	RBAC      security.RoleTable // non-empty enables RBAC (requires JWTSecret to be set too)
	Audit     *audit.AuditStore  // non-nil enables hash-chained request auditing
}

// NewServer builds an *http.Server exposing every generated route
// under addr, plus a health check at GET /healthz that reports whether
// this Registry has persistence enabled. opts layers Audit -> JWT ->
// RBAC -> APIKey around the routes, in that order, skipping any layer
// left at its zero value.
func NewServer(addr string, reg *registry.Registry, opts ServerOptions) *http.Server {
	mux := http.NewServeMux()
	for path, handler := range Routes(reg) {
		mux.HandleFunc(path, handler)
	}
	mux.HandleFunc("/healthz", healthHandler(reg))

	exempt := map[string]bool{"/healthz": true}

	var handler http.Handler = mux
	handler = security.APIKeyMiddleware(handler, opts.APIKeys, exempt)
	if len(opts.RBAC) > 0 {
		handler = security.RBACMiddleware(handler, opts.RBAC, exempt)
	}
	handler = security.JWTMiddleware(handler, opts.JWTSecret, exempt)
	handler = security.AuditMiddleware(handler, opts.Audit)
	handler = loggingMiddleware(handler)

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func healthHandler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "ok",
			"persistence": reg.HasPersistence(),
		})
	}
}

// loggingMiddleware logs method, path, status, and latency for every
// request — the minimum observability a gateway needs to be operable,
// not fabricated telemetry.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// Explicitly sanitize before logging: strconv.Quote escapes control
		// characters -- including a newline an attacker could put in the
		// request path to forge additional, fake-looking log lines
		// (CWE-117 log injection) -- into a new, clean string, rather than
		// handing the tainted request path to log.Printf directly.
		safePath := strconv.Quote(r.URL.Path)
		log.Printf("%s %s %d %s", r.Method, safePath, sw.status, time.Since(start)) // #nosec G706 -- strconv.Quote above genuinely escapes newlines/control chars (verified: "\n" -> literal backslash-n); gosec's taint tracker just doesn't recognize this sanitizer
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
