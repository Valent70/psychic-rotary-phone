// Command veriqo-node wires the packages built this session into a single
// runnable process: raftlite consensus, the mTLS rafttcp transport, an FSM
// (either the MOAT knowledge graph or the Distributed Kernel registry),
// the audit trail, and stdlib observability.
//
// Session update (cluster raft multi-proses live): earlier this command
// called rafttcp.NewCA() independently in every process, so no two
// OS processes could ever validate each other's leaf certificate — the
// command *looked* like a live cluster entrypoint but could not actually
// form one. Fixed by adding a `gen-ca` subcommand that mints one shared
// CA once, plus -ca-cert/-ca-key flags every node loads it from. See
// test/integration/live_cluster_test.go, which spawns three real OS
// processes of this binary and proves leader election, replication, and
// failover over the network — not in-process, not simulated.
//
// This remains library-wiring, not a hardened production entrypoint —
// there is no cluster join/bootstrap protocol beyond a static peer list,
// no config file parser, no TLS cert rotation. That remains open work.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"veriqo/pkg/consensus/raftlite"
	"veriqo/pkg/kernel/distributed"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/observability"
	"veriqo/pkg/transport/rafttcp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "gen-ca" {
		genCA(os.Args[2:])
		return
	}
	runNode(os.Args[1:])
}

// genCA mints one CA and writes it to <dir>/ca-cert.pem and
// <dir>/ca-key.pem so every node process in the cluster can load the
// identical root of trust. Run once per cluster, before starting nodes.
func genCA(args []string) {
	fs := flag.NewFlagSet("gen-ca", flag.ExitOnError)
	dir := fs.String("dir", ".", "directory to write ca-cert.pem / ca-key.pem into")
	_ = fs.Parse(args) // flag.ExitOnError: never returns a non-nil error, it os.Exit()s

	ca, err := rafttcp.NewCA()
	if err != nil {
		log.Fatalf("veriqo-node gen-ca: %v", err)
	}
	certPath := *dir + "/ca-cert.pem"
	keyPath := *dir + "/ca-key.pem"
	if err := ca.SavePEM(certPath, keyPath); err != nil {
		log.Fatalf("veriqo-node gen-ca: %v", err)
	}
	fmt.Printf("veriqo-node: wrote shared cluster CA to %s and %s\n", certPath, keyPath)
}

func runNode(args []string) {
	fs := flag.NewFlagSet("veriqo-node", flag.ExitOnError)
	id := fs.String("id", "", "this node's ID (must appear in -peers)")
	peersFlag := fs.String("peers", "", "comma-separated id=host:port list, e.g. A=127.0.0.1:9001,B=127.0.0.1:9002")
	listenAddr := fs.String("listen", "127.0.0.1:0", "raft transport listen address")
	adminAddr := fs.String("admin", "", "optional HTTP admin listen address (GET /status, GET /state, POST /propose)")
	caCert := fs.String("ca-cert", "", "path to shared cluster CA certificate (see gen-ca); if empty, an ephemeral single-process CA is generated and the cluster will NOT form across processes")
	caKey := fs.String("ca-key", "", "path to shared cluster CA private key")
	fsmKind := fs.String("fsm", "distributed", "FSM to run: \"distributed\" (Distributed Kernel registry) or \"kg\" (knowledge graph)")
	walDir := fs.String("wal-dir", "", "optional directory for WAL persistence (term/vote/log survive a restart); empty means in-memory only, as every prior session")
	adminToken := fs.String("admin-token", "", "bearer token required for ALL -admin endpoints; the admin server refuses to start without this if -admin is set, since /transfer-leadership and /propose are mutating, powerful endpoints that must not be exposed unauthenticated")
	_ = fs.Parse(args) // flag.ExitOnError: never returns a non-nil error, it os.Exit()s

	if *id == "" {
		log.Fatal("veriqo-node: -id is required")
	}

	peerAddrs := map[string]string{}
	var members []string
	for _, kv := range strings.Split(*peersFlag, ",") {
		if kv == "" {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("veriqo-node: invalid -peers entry %q", kv)
		}
		peerAddrs[parts[0]] = parts[1]
		members = append(members, parts[0])
	}
	members = append(members, *id)

	reg := observability.NewRegistry()
	tracer := observability.NewTracer(nil)
	auditStore := audit.NewAuditStore()

	var fsm raftlite.FSM
	var sink raftlite.ClusterEventSink
	var stateFn func() ([]byte, error)

	switch *fsmKind {
	case "kg":
		graph := kg.NewGraph()
		adapter := raftlite.NewKGAdapter(graph)
		fsm, sink = adapter, adapter
		stateFn = func() ([]byte, error) { return json.Marshal(graph.Snapshot()) }
	case "distributed":
		registry := distributed.NewRegistry()
		adapter := raftlite.NewDistributedAdapter(registry)
		fsm = adapter
		stateFn = func() ([]byte, error) {
			return json.Marshal(map[string]any{
				"nodes":    registry.Nodes(),
				"log_len":  registry.LogLen(),
				"chain_ok": registry.VerifyChain() == nil,
			})
		}
	default:
		log.Fatalf("veriqo-node: unknown -fsm %q (want \"kg\" or \"distributed\")", *fsmKind)
	}

	var peers []string
	for pid := range peerAddrs {
		peers = append(peers, pid)
	}

	var ca *rafttcp.CA
	var err error
	if *caCert != "" && *caKey != "" {
		ca, err = rafttcp.LoadCAPEM(*caCert, *caKey)
		if err != nil {
			log.Fatalf("veriqo-node: load shared CA failed: %v", err)
		}
	} else {
		log.Printf("veriqo-node: WARNING no -ca-cert/-ca-key given; generating an ephemeral per-process CA — this node cannot mTLS-handshake with any other process. Run `veriqo-node gen-ca` once and share the files for a real cluster.")
		ca, err = rafttcp.NewCA()
		if err != nil {
			log.Fatalf("veriqo-node: CA init failed: %v", err)
		}
	}

	leaf, err := ca.IssueLeaf(*id)
	if err != nil {
		log.Fatalf("veriqo-node: issue leaf failed: %v", err)
	}
	client := rafttcp.NewClient(*id, ca, leaf, peerAddrs)
	defer client.CloseAll()

	node := raftlite.NewNode(raftlite.Config{
		ID: *id, Peers: peers, Transport: client, FSM: fsm, Sink: sink,
		Metrics: reg,
		Storage: walStorage(*walDir, *id),
	})

	verifier := rafttcp.NewPeerVerifier(ca, members)
	srv := rafttcp.NewServer(*id, node, ca, leaf, verifier)
	addr, err := srv.Listen(*listenAddr)
	if err != nil {
		log.Fatalf("veriqo-node: listen failed: %v", err)
	}
	defer srv.Close()

	fmt.Printf("veriqo-node %s listening on %s (fsm=%s raft peers: %v)\n", *id, addr, *fsmKind, peers)

	_ = tracer

	ctx, cancel := context.WithCancel(context.Background())
	go node.Run(ctx)

	if *adminAddr != "" {
		if *adminToken == "" {
			log.Fatal("veriqo-node: -admin was set without -admin-token — refusing to start an unauthenticated admin server (it exposes /propose, /transfer-leadership, and other mutating operations); set -admin-token to a real secret to enable it")
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           node.ID(),
				"role":         node.Role().String(),
				"term":         node.Term(),
				"is_leader":    node.IsLeader(),
				"commit_index": node.CommitIndex(),
			})
		})
		mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
			b, err := stateFn()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		})
		mux.HandleFunc("/propose", func(w http.ResponseWriter, r *http.Request) {
			if !node.IsLeader() {
				http.Error(w, "not leader", http.StatusServiceUnavailable)
				return
			}
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			idx, term, err := node.Propose(body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"index": idx, "term": term})
		})
		// /metrics: named counters closing the observability gap named
		// in review — wal_writes, wal_failures, checkquorum_stepdowns,
		// readindex_success, readindex_timeout, leadership_transfers,
		// leadership_transfers_failed — plus whatever else this process
		// records via reg elsewhere. Plain text, sorted, deterministic.
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(reg.Snapshot()))
		})
		// /readindex: linearizable-read confirmation. Returns the commit
		// index a caller must locally WaitApplied() past before trusting
		// a local read — the stale-read guard is enforced by ReadIndex
		// itself (see readindex.go): it REFUSES rather than returning a
		// possibly-stale index when quorum can't be confirmed.
		mux.HandleFunc("/readindex", func(w http.ResponseWriter, r *http.Request) {
			rctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			idx, err := node.ReadIndex(rctx)
			if err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"read_index": idx})
		})
		// /transfer-leadership: the single most powerful admin
		// operation exposed here (it hands cluster leadership to a
		// named peer) — hence the auth+audit+rate-limit wrapping below
		// applies to it same as everything else, not as a special case.
		mux.HandleFunc("/transfer-leadership", func(w http.ResponseWriter, r *http.Request) {
			target := r.URL.Query().Get("target")
			if target == "" {
				http.Error(w, "missing ?target=<peer-id>", http.StatusBadRequest)
				return
			}
			rctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			if err := node.TransferLeadership(rctx, target); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"transferred_to": target})
		})

		secured := adminSecurity(mux, *adminToken, auditStore, *id)
		adminSrv := &http.Server{
			Addr:              *adminAddr,
			Handler:           secured,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("veriqo-node: admin server error: %v", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	cancel()
	os.Exit(0)
}

// walStorage returns a raftlite.Storage rooted at <dir>/<id>.wal, or nil
// (in-memory only, unchanged prior behavior) when dir is empty.
func walStorage(dir, id string) raftlite.Storage {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Fatalf("veriqo-node: -wal-dir %q: %v", dir, err)
	}
	return raftlite.NewFileStorage(filepath.Join(dir, id+".wal"))
}

// tokenBucket is a minimal, dependency-free per-process rate limiter —
// deliberately simple (one shared bucket for all admin traffic, not
// per-IP) because this is a cluster-internal admin surface reached by a
// small number of operators/tools, not a public API. Named explicitly
// as a follow-up if this endpoint is ever exposed more broadly:
// per-caller buckets keyed by authenticated identity.
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	rate     float64 // tokens added per second
	last     time.Time
}

func newTokenBucket(capacity, ratePerSecond float64) *tokenBucket {
	return &tokenBucket{tokens: capacity, capacity: capacity, rate: ratePerSecond, last: time.Now()}
}

func (b *tokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// adminSecurity wraps the admin mux with the three properties named
// explicitly in review as non-negotiable for exposing endpoints like
// /transfer-leadership and /propose: authentication (bearer token,
// constant-time compared), an audit log entry per request (actor is the
// remote address since there is no per-caller identity beyond the
// shared token), and rate limiting (a shared token bucket — see
// tokenBucket doc comment on its scope). Authorization in the
// fine-grained sense (different callers allowed different endpoints)
// is intentionally NOT implemented — this bearer token is all-or-
// nothing admin access, matching the "small number of trusted
// operators" threat model this binary targets; per-endpoint scoped
// tokens are named as follow-up work, not hidden.
func adminSecurity(next http.Handler, token string, store *audit.AuditStore, nodeID string) http.Handler {
	limiter := newTokenBucket(20, 5) // burst 20, sustained 5 req/s
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		want := "Bearer " + token
		if !subtleConstantTimeEqual(got, want) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = store.Append(r.RemoteAddr, "admin_auth_rejected", r.URL.Path)
			return
		}
		if !limiter.Allow() {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = store.Append(r.RemoteAddr, "admin_rate_limited", r.URL.Path)
			return
		}
		_, _ = store.Append(r.RemoteAddr, "admin_request:"+nodeID, r.Method+" "+r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// subtleConstantTimeEqual avoids a timing side-channel on token
// comparison by deferring to crypto/subtle rather than hand-rolling a
// security-sensitive primitive.
func subtleConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
