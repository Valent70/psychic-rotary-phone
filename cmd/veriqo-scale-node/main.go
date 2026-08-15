// Command veriqo-scale-node is a real, standalone HTTP worker satisfying
// the exact wire semantics pkg/blockers/scale.NodeProvider's Submit/
// Collect describe: POST /submit hands it one evidence record, GET
// /collect returns everything this process has ever received. It exists
// so pkg/blockers/scale.HTTPNodeProvider has a real REAL-mode peer to
// drive over an actual network, the "future RealNodeProvider... is a
// provider swap, not a rewrite" this package's doc comment anticipated
// — this binary is the node side of that swap.
//
// Deliberately minimal: no dedup, no persistence, no retry logic. It
// records exactly what it receives, in order, so the SAME integrity
// check pkg/blockers/scale.checkIntegrity already runs against
// SimulatedNodeProvider's output can run unmodified against this
// process's /collect response — real loss or duplication (network
// retries, process restarts) shows up as a real mismatch, not a
// simulated one.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type evidenceRecord struct {
	ID      string `json:"id"`
	Payload string `json:"payload"`
}

type processedRecord struct {
	NodeID     string    `json:"node_id"`
	RecordID   string    `json:"record_id"`
	ReceivedAt time.Time `json:"received_at"`
}

func main() {
	fs := flag.NewFlagSet("veriqo-scale-node", flag.ExitOnError)
	id := fs.String("id", "", "this node's ID, stamped into every processed record")
	listenAddr := fs.String("listen", "0.0.0.0:8090", "HTTP listen address")
	token := fs.String("token", "", "bearer token required for /submit and /collect")
	_ = fs.Parse(os.Args[1:]) // flag.ExitOnError: never returns a non-nil error, it os.Exit()s

	if *id == "" {
		log.Fatal("veriqo-scale-node: -id is required")
	}
	if *token == "" {
		log.Fatal("veriqo-scale-node: -token is required — refusing to serve an unauthenticated node")
	}

	var (
		mu        sync.Mutex
		processed []processedRecord
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var rec evidenceRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		processed = append(processed, processedRecord{NodeID: *id, RecordID: rec.ID, ReceivedAt: time.Now()})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/collect", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		out := make([]processedRecord, len(processed))
		copy(out, processed)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	secured := authGuard(mux, *token)
	srv := &http.Server{
		Addr:              *listenAddr,
		Handler:           secured,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Printf("veriqo-scale-node %s listening on %s\n", *id, *listenAddr)
	log.Fatal(srv.ListenAndServe())
}

// authGuard requires a constant-time-compared bearer token on every
// request, mirroring cmd/veriqo-node's adminSecurity token check. No
// rate limiting or audit trail here: this binary's threat model is a
// throwaway qualification-drill worker on a private network, not a
// production admin surface with the same stakes as /propose or
// /transfer-leadership.
func authGuard(next http.Handler, token string) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
