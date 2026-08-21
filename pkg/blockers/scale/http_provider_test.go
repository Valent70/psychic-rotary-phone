package scale

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeScaleNode is a minimal in-process stand-in for cmd/veriqo-scale-node,
// implementing the identical wire protocol (POST /submit, GET /collect,
// bearer auth) so this test exercises HTTPNodeProvider against a real
// net/http.Server and real JSON encoding, not a mock of the provider
// itself.
func fakeScaleNode(t *testing.T, id, token string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var processed []httpProcessedRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var rec EvidenceRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		processed = append(processed, httpProcessedRecord{NodeID: id, RecordID: rec.ID, ReceivedAt: time.Now()})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/collect", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		out := make([]httpProcessedRecord, len(processed))
		copy(out, processed)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPNodeProvider_NoLossNoDuplication(t *testing.T) {
	const token = "test-scale-token"
	const nodeCount = 5
	const recordCount = 2000

	addrs := make(map[string]string, nodeCount)
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("http-node-%02d", i)
		srv := fakeScaleNode(t, id, token)
		addrs[id] = srv.URL
	}

	provider := &HTTPNodeProvider{NodeAddrs: addrs, Token: token}
	if provider.Mode() != "REAL" {
		t.Fatalf("expected Mode() REAL, got %q", provider.Mode())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nodes, err := provider.Provision(ctx, nodeCount)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(nodes) != nodeCount {
		t.Fatalf("expected %d nodes, got %d", nodeCount, len(nodes))
	}

	submittedIDs := make([]string, 0, recordCount)
	for i := 0; i < recordCount; i++ {
		id := fmt.Sprintf("ev-%08d", i)
		node := nodes[i%len(nodes)]
		if err := provider.Submit(ctx, node, EvidenceRecord{ID: id, Payload: "synthetic"}); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
		submittedIDs = append(submittedIDs, id)
	}

	processed, err := provider.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	report := checkIntegrity(submittedIDs, processed)
	if !report.Clean() {
		t.Fatalf("expected clean integrity report, got lost=%v duplicated=%v", report.Lost, report.Duplicated)
	}
	if report.Submitted != recordCount || report.Processed != recordCount {
		t.Fatalf("expected submitted=processed=%d, got submitted=%d processed=%d", recordCount, report.Submitted, report.Processed)
	}

	if err := provider.Destroy(ctx, nodes); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestHTTPNodeProvider_RejectsWrongToken(t *testing.T) {
	srv := fakeScaleNode(t, "n0", "right-token")
	provider := &HTTPNodeProvider{NodeAddrs: map[string]string{"n0": srv.URL}, Token: "wrong-token"}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodes, err := provider.Provision(ctx, 1)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	err = provider.Submit(ctx, nodes[0], EvidenceRecord{ID: "ev-1", Payload: "x"})
	if err == nil {
		t.Fatal("expected Submit to fail with the wrong bearer token, it succeeded")
	}
}

func TestHTTPNodeProvider_ProvisionRefusesMoreThanConfigured(t *testing.T) {
	srv := fakeScaleNode(t, "n0", "tok")
	provider := &HTTPNodeProvider{NodeAddrs: map[string]string{"n0": srv.URL}, Token: "tok"}
	if _, err := provider.Provision(context.Background(), 5); err == nil {
		t.Fatal("expected Provision(5) to fail with only 1 configured address, it succeeded")
	}
}

// TestHTTPNodeProvider_RealDockerContainers drives real, separately
// running cmd/veriqo-scale-node processes -- not httptest fixtures --
// when VERIQO_SCALE_DOCKER_ADDRS is set to a comma-separated
// id=http://host:port list (matching cmd/veriqo-node's -peers flag
// style). Unset by default, so CI and every ordinary local run skip it
// exactly like TestSoak skips the real 72h window without
// VERIQO_SOAK_MINUTES: this is the harness, not a fabricated pass.
// Point it at real Docker containers running cmd/veriqo-scale-node to
// produce the real evidence backing
// evidence/scale_qualification-multi-container-drill.txt.
// VERIQO_SCALE_DOCKER_TIMEOUT (a time.ParseDuration string, default
// "2m") overrides the Provision+Submit+Collect deadline -- the default
// is enough for the harness's own small default recordCount, but a
// drill run at real target volumes (e.g. 1,000,000 envelopes) needs
// more wall-clock time than 2 minutes on a resource-shared single host,
// which is a real throughput fact this test surfaced, not a defect.
func TestHTTPNodeProvider_RealDockerContainers(t *testing.T) {
	spec := os.Getenv("VERIQO_SCALE_DOCKER_ADDRS")
	if spec == "" {
		t.Skip("VERIQO_SCALE_DOCKER_ADDRS not set -- skipping the real multi-container drill (see package doc)")
	}
	token := os.Getenv("VERIQO_SCALE_DOCKER_TOKEN")
	if token == "" {
		t.Fatal("VERIQO_SCALE_DOCKER_ADDRS is set but VERIQO_SCALE_DOCKER_TOKEN is not")
	}

	addrs := map[string]string{}
	for _, kv := range strings.Split(spec, ",") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			t.Fatalf("VERIQO_SCALE_DOCKER_ADDRS: invalid entry %q", kv)
		}
		addrs[parts[0]] = parts[1]
	}

	recordCount := 2000
	if v := os.Getenv("VERIQO_SCALE_DOCKER_RECORDS"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &recordCount); err != nil {
			t.Fatalf("VERIQO_SCALE_DOCKER_RECORDS=%q: %v", v, err)
		}
	}

	timeout := 2 * time.Minute
	if v := os.Getenv("VERIQO_SCALE_DOCKER_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("VERIQO_SCALE_DOCKER_TIMEOUT=%q: %v", v, err)
		}
		timeout = d
	}

	provider := &HTTPNodeProvider{NodeAddrs: addrs, Token: token}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	nodes, err := provider.Provision(ctx, len(addrs))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Logf("provisioned %d real nodes: %v", len(nodes), nodes)

	submittedIDs := make([]string, 0, recordCount)
	start := time.Now()
	for i := 0; i < recordCount; i++ {
		id := fmt.Sprintf("ev-%08d", i)
		node := nodes[i%len(nodes)]
		if err := provider.Submit(ctx, node, EvidenceRecord{ID: id, Payload: "synthetic"}); err != nil {
			t.Fatalf("Submit %s to real node %s: %v", id, node.ID, err)
		}
		submittedIDs = append(submittedIDs, id)
	}
	elapsed := time.Since(start)

	processed, err := provider.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	report := checkIntegrity(submittedIDs, processed)
	t.Logf("real multi-container drill: nodes=%d records=%d elapsed=%s throughput=%.2f/s submitted=%d processed=%d lost=%d duplicated=%d",
		len(nodes), recordCount, elapsed, float64(recordCount)/elapsed.Seconds(), report.Submitted, report.Processed, len(report.Lost), len(report.Duplicated))
	if !report.Clean() {
		t.Fatalf("real multi-container drill FAILED integrity: lost=%v duplicated=%v", report.Lost, report.Duplicated)
	}
}
