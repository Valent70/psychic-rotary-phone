// Command veriqo-gateway runs the REST Gateway as a standalone, real
// OS process: `veriqo-gateway --addr=:8080 [--state=/path] [--api-key=...]
// [--jwt-secret=...] [--rbac=role:prefix,prefix;role2:prefix] [--audit]
// [--tls-cert=... --tls-key=...] [--mtls-ca=...]`.
// It builds exactly one veriqo/registry.Registry and serves every
// generated route (see veriqo/gateway/rest) over it, with the full
// security stack (API Key, JWT, RBAC, Audit, TLS/mTLS) from
// pkg/platform/security composed via veriqo/gateway/rest.ServerOptions.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/security"
	"veriqo/veriqo/gateway/rest"
	"veriqo/veriqo/kernel"
	"veriqo/veriqo/registry"
)

// parseRBAC parses "role:prefix,prefix;role2:prefix3" into a
// security.RoleTable.
func parseRBAC(spec string) security.RoleTable {
	table := security.RoleTable{}
	if spec == "" {
		return table
	}
	for _, roleSpec := range strings.Split(spec, ";") {
		parts := strings.SplitN(roleSpec, ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		var prefixes []string
		for _, p := range strings.Split(parts[1], ",") {
			if p != "" {
				prefixes = append(prefixes, p)
			}
		}
		table[parts[0]] = prefixes
	}
	return table
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	statePath := flag.String("state", "", "path to a persistence state file (optional; empty = purely in-memory)")
	apiKey := flag.String("api-key", "", "if set, require this value in the X-API-Key header on every route except /healthz")
	jwtSecret := flag.String("jwt-secret", "", "if set, require a valid HS256 Bearer JWT on every route except /healthz")
	rbacSpec := flag.String("rbac", "", "RBAC role table, e.g. 'operator:/trust/,/evidence/;admin:*' (requires --jwt-secret)")
	enableAudit := flag.Bool("audit", false, "if set, append a hash-chained audit record for every request")
	tlsCert := flag.String("tls-cert", "", "path to a TLS certificate file (optional; empty = plain HTTP)")
	tlsKey := flag.String("tls-key", "", "path to the TLS private key file (required if --tls-cert is set)")
	mtlsCA := flag.String("mtls-ca", "", "path to a CA cert file; if set with --tls-cert/--tls-key, requires and verifies client certificates (mutual TLS)")
	flag.Parse()

	var regOpts []registry.Option
	if *statePath != "" {
		regOpts = append(regOpts, registry.WithStatePath(*statePath))
	}
	// v7.10.2 gap-closure P0 (V7.10.1 audit: "gateway belum
	// menggunakan kernel.Kernel — pakai registry.New() langsung"):
	// the gateway now boots through the single composition root
	// (veriqo/kernel.New) instead of building its own standalone
	// registry.Registry. It serves REST routes over the exact same
	// k.Registry a bare registry.New() would have produced (so no
	// route/behavior change for existing endpoints), but the process
	// now also has k.TrustCalculus/k.Canonical/k.Intent/k.Calibration
	// available on the one shared kernel instance for future route
	// wiring, and shutdown goes through k.Shutdown() (which already
	// stops every Core Runtime member and persists state) instead of
	// re-implementing that sequence locally.
	k, err := kernel.New(regOpts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-gateway: booting kernel:", err)
		os.Exit(1)
	}
	reg := k.Registry

	srvOpts := rest.ServerOptions{}
	if *apiKey != "" {
		srvOpts.APIKeys = map[string]bool{*apiKey: true}
	}
	if *jwtSecret != "" {
		srvOpts.JWTSecret = []byte(*jwtSecret)
	}
	if *rbacSpec != "" {
		srvOpts.RBAC = parseRBAC(*rbacSpec)
	}
	if *enableAudit {
		srvOpts.Audit = audit.NewAuditStore()
	}
	srv := rest.NewServer(*addr, reg, srvOpts)

	if (*tlsCert == "") != (*tlsKey == "") {
		fmt.Fprintln(os.Stderr, "veriqo-gateway: --tls-cert and --tls-key must be set together")
		os.Exit(1)
	}
	var tlsErr error
	if *tlsCert != "" && *mtlsCA != "" {
		cfg, err := security.LoadMutualTLSConfig(*tlsCert, *tlsKey, *mtlsCA)
		if err == nil {
			srv.TLSConfig = cfg
		}
		tlsErr = err
	}
	if tlsErr != nil {
		fmt.Fprintln(os.Stderr, "veriqo-gateway: loading mTLS config:", tlsErr)
		os.Exit(1)
	}

	go func() {
		log.Printf("veriqo-gateway: listening on %s (persistence=%t, api_key=%t, jwt=%t, rbac=%t, audit=%t, tls=%t, mtls=%t)",
			*addr, reg.HasPersistence(), *apiKey != "", *jwtSecret != "", *rbacSpec != "", *enableAudit, *tlsCert != "", *mtlsCA != "")
		var err error
		if *tlsCert != "" {
			err = srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("veriqo-gateway: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	if err := k.Shutdown(); err != nil {
		log.Printf("veriqo-gateway: kernel shutdown: %v", err)
	}
	_ = srv.Close()
}
