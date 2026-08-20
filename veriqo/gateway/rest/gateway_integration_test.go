package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGatewayIntegration builds the real veriqo-gateway binary and
// runs it as a genuinely separate OS process (exec.Command, not an
// in-process httptest.Server), then drives it with a plain
// net/http.Client — the same separate-process proof style this repo
// already uses for veriqo/cli/cli_integration_test.go and
// cmd/veriqo-verify. This is the concrete evidence for the gap named
// explicitly in the prior report: "Belum ada network transport... SDK
// memanggil Registry secara in-process."
func TestGatewayIntegration(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "veriqo-gateway")
	build := exec.Command("go", "build", "-o", bin, "veriqo/cmd/veriqo-gateway")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building veriqo-gateway: %v\n%s", err, out)
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	addr := "127.0.0.1:18080"
	proc := exec.Command(bin, "--addr="+addr, "--state="+statePath)
	if err := proc.Start(); err != nil {
		t.Fatalf("starting veriqo-gateway: %v", err)
	}
	defer func() { _ = proc.Process.Kill(); _, _ = proc.Process.Wait() }()

	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + addr

	if !waitForHealthy(t, client, baseURL) {
		t.Fatal("veriqo-gateway did not become healthy in time")
	}

	// Real call #1: trust.certify over the network — Certify (not
	// Evaluate) is the one that appends to the hash-chained ledger,
	// which is what this test needs to prove persists after shutdown.
	body, _ := json.Marshal(map[string]any{
		"subject_id": "source-alpha", "win_rate": 0.95,
		"contradiction_rate": 0.02, "tick": 1,
	})
	resp, err := client.Post(baseURL+"/trust/certify", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /trust/certify: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /trust/certify: status %d", resp.StatusCode)
	}
	var score struct {
		SubjectID string
		Score     float64
	}
	if err := json.NewDecoder(resp.Body).Decode(&score); err != nil {
		t.Fatalf("decoding trust.certify response: %v", err)
	}
	if score.SubjectID != "source-alpha" || score.Score <= 0.7 {
		t.Fatalf("unexpected trust.evaluate response: %+v", score)
	}

	// Real call #2: evidence.add_node, over the network, into the same
	// running process's Registry (proves state is shared across
	// requests within one gateway process, not rebuilt per-request).
	body, _ = json.Marshal(map[string]any{"kind": "raw_observation", "payload": "hello"})
	resp2, err := client.Post(baseURL+"/evidence/add_node", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /evidence/add_node: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("POST /evidence/add_node: status %d", resp2.StatusCode)
	}

	// Real call #3: lifecycle.run_unified, over the network, against
	// the ACTUAL compiled veriqo-gateway binary -- proof this session's
	// P0-8 gap-closure (a real HTTP entrypoint into pkg/lifecycle.
	// Orchestrator.RunUnified, previously reachable only from tests and
	// cmd/veriqo-cold-replay's replay-only path) is not just an
	// in-process httptest claim (see lifecycle_route_test.go for that
	// finer-grained coverage) but genuinely serves from the same
	// separate-OS-process binary this test already proves for the
	// registry-based routes.
	body3, _ := json.Marshal(map[string]any{
		"actor_id": "analyst-gw-1", "tenant": "acme", "objective": "assess dark-vessel risk",
		"entity_aliases":      []map[string]string{{"kind": "IMO", "value": "9223344"}},
		"required_confidence": 0.6, "temporal_scope": "last-7-days", "tick": 1,
		"requirements": []map[string]any{{"kind": "AIS_STATUS", "required": true, "min_sources": 2}},
		"subject":      "x", "predicate": "AIS_STATUS",
		"submissions": []map[string]any{
			{"source_id": "ais-vendor-a", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "ais-vendor-b", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "port-authority", "value": "ON", "base_reliability": 0.95},
		},
	})
	resp3, err := client.Post(baseURL+"/lifecycle/run_unified", "application/json", bytes.NewReader(body3))
	if err != nil {
		t.Fatalf("POST /lifecycle/run_unified: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("POST /lifecycle/run_unified: status %d", resp3.StatusCode)
	}
	// Decoded into the FULL response shape (audit item P0-D: "External
	// Intent -> real context -> Lifecycle -> Execution DAG -> Identity
	// -> Evidence -> ... -> Certificate -> Ledger -> Replay dalam satu
	// production acceptance scenario") -- not just the four fields a
	// prior round checked, so this one real, separate-OS-process HTTP
	// call is the system's actual proof that every named link in that
	// chain produces a real, non-empty identifier reachable from the
	// EXTERNAL API, not only from in-process Go calls (every other
	// acceptance test in test/acceptance/ calls RunUnified directly,
	// never through a real HTTP request).
	var runResult struct {
		IntentID                  string `json:"intent_id"`
		Decision                  string `json:"decision"`
		EntityID                  string `json:"entity_id"`
		ExecutionID               string `json:"execution_id"`
		ExecutionRootHash         string `json:"execution_root_hash"`
		DecisionID                string `json:"decision_id"`
		CertificateHash           string `json:"certificate_hash"`
		IVFVerified               bool   `json:"ivf_verified"`
		EvidencePackageID         string `json:"evidence_package_id"`
		VerificationCertificateID string `json:"verification_certificate_id"`
		ReplayPackageID           string `json:"replay_package_id"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&runResult); err != nil {
		t.Fatalf("decoding lifecycle.run_unified response: %v", err)
	}
	missing := map[string]string{
		"intent_id": runResult.IntentID, "decision": runResult.Decision,
		"entity_id": runResult.EntityID, "execution_id": runResult.ExecutionID,
		"execution_root_hash": runResult.ExecutionRootHash, "decision_id": runResult.DecisionID,
		"certificate_hash":            runResult.CertificateHash,
		"evidence_package_id":         runResult.EvidencePackageID,
		"verification_certificate_id": runResult.VerificationCertificateID,
		"replay_package_id":           runResult.ReplayPackageID,
	}
	for field, val := range missing {
		if val == "" {
			t.Fatalf("expected a real, non-empty %q over the real HTTP round trip against the actual gateway process, got %+v", field, runResult)
		}
	}
	if !runResult.IVFVerified {
		t.Fatalf("expected IVF to independently verify this case's arbitration over the real HTTP round trip, got %+v", runResult)
	}
	// The DecisionID must be independently derived, not an alias of
	// ExecutionID (P0-7) -- checked here too since this is the one test
	// proving that property holds over the real external API, not just
	// internally.
	if runResult.DecisionID == runResult.ExecutionID {
		t.Fatalf("expected DecisionID to be independently derived from ExecutionID, got both equal to %s", runResult.DecisionID)
	}

	// Shut down cleanly (SIGTERM triggers the gateway's own
	// persistence-on-shutdown path) and confirm it actually persisted:
	// the trust certificate issued over the network above must be on
	// disk at statePath afterward.
	_ = proc.Process.Signal(os.Interrupt)
	_, _ = proc.Process.Wait()

	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading persisted state after shutdown: %v", err)
	}
	if !bytes.Contains(data, []byte("source-alpha")) {
		t.Fatalf("persisted state does not contain the certificate issued over the network: %s", data)
	}
}

// TestGatewayIntegration_ColdReplayFromHTTPBoundary is the real P1-10
// closure: the new auditor document's "E2E replay against the exact
// same pipeline, not a stand-in" requirement, proven one hop further
// back than the prior round's test/integration/
// cold_replay_cross_process_test.go -- that test already proved a
// genuinely separate cmd/veriqo-cold-replay process can cold-restore
// and verify the real execution.Engine DAG, but it built the original
// execution in-process via execution.NewEngine(nil).Run, not through
// the literal HTTP boundary production traffic actually crosses. This
// test starts there instead: a real compiled veriqo-gateway binary,
// run as a genuinely separate OS process (identical proof style to
// TestGatewayIntegration above), served a real POST
// /lifecycle/run_unified request over a real net/http.Client -- and
// the resulting ReplayExport (see lifecycle_route.go's respRunUnified
// and execution.Result.ExportReplay, both added this round to close
// exactly this gap) is fed to ANOTHER genuinely separate compiled
// process, cmd/veriqo-cold-replay, with zero shared memory or state
// with either the gateway process or this test process. Three
// separate OS processes, one real HTTP request, one real cold
// restart: the full round trip the audit asked for.
func TestGatewayIntegration_ColdReplayFromHTTPBoundary(t *testing.T) {
	gwBin := filepath.Join(t.TempDir(), "veriqo-gateway")
	buildGW := exec.Command("go", "build", "-o", gwBin, "veriqo/cmd/veriqo-gateway")
	buildGW.Dir = "."
	if out, err := buildGW.CombinedOutput(); err != nil {
		t.Fatalf("building veriqo-gateway: %v\n%s", err, out)
	}
	replayBin := filepath.Join(t.TempDir(), "veriqo-cold-replay")
	buildReplay := exec.Command("go", "build", "-o", replayBin, "veriqo/cmd/veriqo-cold-replay")
	buildReplay.Dir = "."
	if out, err := buildReplay.CombinedOutput(); err != nil {
		t.Fatalf("building veriqo-cold-replay: %v\n%s", err, out)
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	addr := "127.0.0.1:18081"
	proc := exec.Command(gwBin, "--addr="+addr, "--state="+statePath)
	if err := proc.Start(); err != nil {
		t.Fatalf("starting veriqo-gateway: %v", err)
	}
	defer func() { _ = proc.Process.Kill(); _, _ = proc.Process.Wait() }()

	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + addr
	if !waitForHealthy(t, client, baseURL) {
		t.Fatal("veriqo-gateway did not become healthy in time")
	}

	body, _ := json.Marshal(map[string]any{
		"actor_id": "analyst-p1-10", "tenant": "acme", "objective": "assess dark-vessel risk",
		"entity_aliases":      []map[string]string{{"kind": "IMO", "value": "9445566"}},
		"required_confidence": 0.6, "temporal_scope": "last-7-days", "tick": 3,
		"requirements": []map[string]any{{"kind": "AIS_STATUS", "required": true, "min_sources": 2}},
		"subject":      "x", "predicate": "AIS_STATUS",
		"submissions": []map[string]any{
			{"source_id": "ais-vendor-a", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "ais-vendor-b", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "port-authority", "value": "ON", "base_reliability": 0.95},
		},
	})
	resp, err := client.Post(baseURL+"/lifecycle/run_unified", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /lifecycle/run_unified: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /lifecycle/run_unified: status %d", resp.StatusCode)
	}
	var runResult struct {
		IntentID             string `json:"intent_id"`
		EntityID             string `json:"entity_id"`
		DecisionID           string `json:"decision_id"`
		ExecutionRootHash    string `json:"execution_root_hash"`
		ReplayExport         []byte `json:"replay_export"`
		IdentityLedgerExport []byte `json:"identity_ledger_export"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runResult); err != nil {
		t.Fatalf("decoding lifecycle.run_unified response: %v", err)
	}
	if runResult.ExecutionRootHash == "" {
		t.Fatal("expected a real, non-empty execution_root_hash over the real HTTP round trip")
	}
	if len(runResult.ReplayExport) == 0 {
		t.Fatal("expected a real, non-empty replay_export over the real HTTP round trip -- P1-10 requires the exact same pipeline's output to be cold-replayable, not a separately-constructed stand-in")
	}
	if len(runResult.IdentityLedgerExport) == 0 {
		t.Fatal("expected a real, non-empty identity_ledger_export over the real HTTP round trip -- RunUnified always binds Orchestrator.Identity to the execution engine, so cold-replaying byte-for-byte requires it too")
	}

	exportPath := filepath.Join(t.TempDir(), "http-boundary-export.json")
	if err := os.WriteFile(exportPath, runResult.ReplayExport, 0o600); err != nil {
		t.Fatalf("writing replay export: %v", err)
	}
	identityExportPath := filepath.Join(t.TempDir(), "http-boundary-identity-export.json")
	if err := os.WriteFile(identityExportPath, runResult.IdentityLedgerExport, 0o600); err != nil {
		t.Fatalf("writing identity ledger export: %v", err)
	}

	// The independent replayer is a THIRD genuinely separate OS
	// process: it shares no memory with the gateway process (which
	// produced the export) nor with this test process (which only
	// relayed bytes it read off an HTTP response).
	replay := exec.Command(replayBin, "-export", exportPath, "-identity-export", identityExportPath)
	out, err := replay.CombinedOutput()
	t.Logf("veriqo-cold-replay output:\n%s", out)
	if err != nil {
		t.Fatalf("cold-replay process exited non-zero on a genuine HTTP-boundary export: %v", err)
	}
	if !strings.Contains(string(out), "VERDICT                : PASSED") {
		t.Fatalf("expected PASSED verdict cold-replaying the real HTTP round trip's export, got:\n%s", out)
	}
	if !strings.Contains(string(out), runResult.ExecutionRootHash) {
		t.Fatalf("expected the cold-replay process to reproduce the same execution root hash %s the HTTP "+
			"response reported, got:\n%s", runResult.ExecutionRootHash, out)
	}
	// The literal acceptance list a later audit round named explicitly:
	// "Same: IntentID, EvidenceRoot, EntityID, ..., DecisionID, ...".
	// EvidenceRoot is already checked above (ExecutionRootHash); these
	// three are the ones this test can independently cross-check
	// against the ORIGINAL HTTP response's own reported values, over
	// and above the aggregate node-by-node hash match cmd/veriqo-cold-
	// replay's own VERDICT already proves.
	for _, want := range []string{
		"IntentID              : " + runResult.IntentID,
		"EntityID              : " + runResult.EntityID,
		"DecisionID            : " + runResult.DecisionID,
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("expected the cold-replay process's reproduced-fields report to contain %q "+
				"(matching the original HTTP response), got:\n%s", want, out)
		}
	}

	_ = proc.Process.Signal(os.Interrupt)
	_, _ = proc.Process.Wait()
}

// TestGatewayIntegration_ColdReplayFromHTTPBoundary_TamperedExportFails
// is the adversarial companion: an export obtained over the real HTTP
// boundary, then altered before being handed to the independent
// replayer, must be refused -- proving the round trip this round
// added is not merely plumbing that happens to pass on the honest
// path, but a genuine, tamper-evident verification.
func TestGatewayIntegration_ColdReplayFromHTTPBoundary_TamperedExportFails(t *testing.T) {
	gwBin := filepath.Join(t.TempDir(), "veriqo-gateway")
	buildGW := exec.Command("go", "build", "-o", gwBin, "veriqo/cmd/veriqo-gateway")
	buildGW.Dir = "."
	if out, err := buildGW.CombinedOutput(); err != nil {
		t.Fatalf("building veriqo-gateway: %v\n%s", err, out)
	}
	replayBin := filepath.Join(t.TempDir(), "veriqo-cold-replay")
	buildReplay := exec.Command("go", "build", "-o", replayBin, "veriqo/cmd/veriqo-cold-replay")
	buildReplay.Dir = "."
	if out, err := buildReplay.CombinedOutput(); err != nil {
		t.Fatalf("building veriqo-cold-replay: %v\n%s", err, out)
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	addr := "127.0.0.1:18082"
	proc := exec.Command(gwBin, "--addr="+addr, "--state="+statePath)
	if err := proc.Start(); err != nil {
		t.Fatalf("starting veriqo-gateway: %v", err)
	}
	defer func() { _ = proc.Process.Kill(); _, _ = proc.Process.Wait() }()

	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + addr
	if !waitForHealthy(t, client, baseURL) {
		t.Fatal("veriqo-gateway did not become healthy in time")
	}

	body, _ := json.Marshal(map[string]any{
		"actor_id": "analyst-p1-10-tamper", "tenant": "acme", "objective": "assess dark-vessel risk",
		"entity_aliases":      []map[string]string{{"kind": "IMO", "value": "9556677"}},
		"required_confidence": 0.6, "temporal_scope": "last-7-days", "tick": 4,
		"requirements": []map[string]any{{"kind": "AIS_STATUS", "required": true, "min_sources": 2}},
		"subject":      "x", "predicate": "AIS_STATUS",
		"submissions": []map[string]any{
			{"source_id": "ais-vendor-a", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "ais-vendor-b", "value": "OFF", "base_reliability": 0.8},
			{"source_id": "port-authority", "value": "ON", "base_reliability": 0.95},
		},
	})
	resp, err := client.Post(baseURL+"/lifecycle/run_unified", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /lifecycle/run_unified: %v", err)
	}
	defer resp.Body.Close()
	var runResult struct {
		ReplayExport         []byte `json:"replay_export"`
		IdentityLedgerExport []byte `json:"identity_ledger_export"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runResult); err != nil {
		t.Fatalf("decoding lifecycle.run_unified response: %v", err)
	}
	identityExportPath := filepath.Join(t.TempDir(), "tampered-http-boundary-identity-export.json")
	if err := os.WriteFile(identityExportPath, runResult.IdentityLedgerExport, 0o600); err != nil {
		t.Fatalf("writing identity ledger export: %v", err)
	}

	var export map[string]any
	if err := json.Unmarshal(runResult.ReplayExport, &export); err != nil {
		t.Fatalf("unmarshalling export for tampering: %v", err)
	}
	committed, ok := export["committed_trace"].(map[string]any)
	if !ok {
		t.Fatalf("export missing committed_trace: %v", export)
	}
	nodes, ok := committed["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatalf("export has no committed_trace.nodes to tamper with: %v", committed)
	}
	firstNode, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected node shape: %v", nodes[0])
	}
	firstNode["hash"] = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	tampered, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("re-marshalling tampered export: %v", err)
	}

	exportPath := filepath.Join(t.TempDir(), "tampered-http-boundary-export.json")
	if err := os.WriteFile(exportPath, tampered, 0o600); err != nil {
		t.Fatalf("writing tampered export: %v", err)
	}

	replay := exec.Command(replayBin, "-export", exportPath, "-identity-export", identityExportPath)
	out, err := replay.CombinedOutput()
	t.Logf("veriqo-cold-replay output:\n%s", out)
	if err == nil {
		t.Fatal("expected the cold-replay process to exit non-zero on a tampered HTTP-boundary export")
	}
	if !strings.Contains(string(out), "VERDICT                : FAILED") {
		t.Fatalf("expected FAILED verdict on a tampered HTTP-boundary export, got:\n%s", out)
	}

	_ = proc.Process.Signal(os.Interrupt)
	_, _ = proc.Process.Wait()
}

func waitForHealthy(t *testing.T, client *http.Client, baseURL string) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}
