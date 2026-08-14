// TestColdReplay_CrossProcess proves audit item P0-E ("distributed
// cold replay") for real: export a completed execution's full DAG
// trace to a file, launch cmd/veriqo-cold-replay as a genuinely
// separate compiled binary/OS process (no shared memory, no import of
// this test's own engine instance), and confirm it independently
// rebuilds the evidence root, decision, explanation and verification
// certificate purely from that file -- the same cross-process pattern
// TestIVFCrossProcess_* already proves for the fusion/contradiction/
// decision domains (ivf_cross_process_test.go), extended here to the
// full execution DAG. See cmd/veriqo-cold-replay's own doc comment for
// the explicit scope note: entity resolution is NOT covered, since no
// existing adapter bundles pkg/identity's independent ledger into an
// exportable execution.ReplayRequest.
package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

func buildVeriqoColdReplay(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source location")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	binPath := filepath.Join(t.TempDir(), "veriqo-cold-replay")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/veriqo-cold-replay")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building cmd/veriqo-cold-replay: %v\n%s", err, out)
	}
	return binPath
}

func coldReplayCaseInput() canonical.CaseInput {
	return canonical.CaseInput{
		Entity: digitaltwin.EntityID("IMO7778889"), Subject: "IMO7778889", Predicate: "cold_replay_case",
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("port-monitor"), Value: "dark",
				BaseReliability: 0.9, Provider: "port", UpstreamID: "feed-port"},
			{SourceID: fusion.SourceID("sar-imagery"), Value: "dark",
				BaseReliability: 0.85, Provider: "orbital-corp", UpstreamID: "feed-sar",
				Dependencies: []evidencegraph.DependencyRecord{
					{UpstreamID: evidencegraph.NodeID("model-detector-v3"),
						Kind: evidencegraph.DependsOnTransformation, Confidence: 0.7}}},
		},
		Policy: risk.DefaultPolicy(), PatternScore: 0.6, PriceAnomaly: 0.8,
		EconomicChannels: map[string]float64{"freight": 500_000},
		Tick:             12,
	}
}

func coldReplayExecContext() execution.Context {
	return execution.Context{ExecutionID: "cold-replay-1", CaseID: "cold-case-1", Tenant: "acme",
		Actor: "cold-replay-test", PolicyVersion: "sanctions-screen@3", EvidencePackageID: "evp-cold-1",
		IdentityResolutionVersion: "ident@2", Tick: 12, ReplayMetadata: "cold-replay-cross-process"}
}

// TestColdReplay_CrossProcess_MatchesOriginal is the honest-scope
// happy path: a real execution's exported trace, cold-restored by a
// truly separate process, reproduces the identical evidence root,
// decision, explanation and verification certificate.
func TestColdReplay_CrossProcess_MatchesOriginal(t *testing.T) {
	bin := buildVeriqoColdReplay(t)

	e := execution.NewEngine(nil)
	res, err := e.Run(execution.Input{Context: coldReplayExecContext(), Case: coldReplayCaseInput()})
	if err != nil {
		t.Fatalf("original execution: %v", err)
	}
	if res.ExecutionRootHash == "" {
		t.Fatal("expected a real execution root hash from the original run")
	}

	req := execution.ReplayRequest{Context: coldReplayExecContext(), Case: coldReplayCaseInput(), Committed: res.Trace}
	data, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	exportPath := filepath.Join(t.TempDir(), "cold-export.json")
	if err := os.WriteFile(exportPath, data, 0o600); err != nil {
		t.Fatalf("write export: %v", err)
	}

	cmd := exec.Command(bin, "-export", exportPath)
	out, err := cmd.CombinedOutput()
	t.Logf("veriqo-cold-replay output:\n%s", out)
	if err != nil {
		t.Fatalf("cold-replay process exited non-zero on a genuine export: %v", err)
	}
	if !contains(string(out), "VERDICT                : PASSED") {
		t.Fatalf("expected PASSED verdict from the independent cold-replay process, got:\n%s", out)
	}
	if !contains(string(out), res.ExecutionRootHash) {
		t.Fatalf("expected the cold-replay process's report to reproduce the original execution root hash %s, got:\n%s",
			res.ExecutionRootHash, out)
	}
}

// TestColdReplay_CrossProcess_TamperedExportFails is the adversarial
// case: an export whose committed trace was altered after the fact
// (e.g. a node hash edited to hide a divergence) must be caught by the
// independent process, not silently accepted.
func TestColdReplay_CrossProcess_TamperedExportFails(t *testing.T) {
	bin := buildVeriqoColdReplay(t)

	e := execution.NewEngine(nil)
	res, err := e.Run(execution.Input{Context: coldReplayExecContext(), Case: coldReplayCaseInput()})
	if err != nil {
		t.Fatalf("original execution: %v", err)
	}

	tampered := res.Trace
	if len(tampered.Nodes) == 0 {
		t.Fatal("expected at least one trace node to tamper with")
	}
	tamperedNodes := make([]execution.Node, len(tampered.Nodes))
	copy(tamperedNodes, tampered.Nodes)
	tamperedNodes[0].Hash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	tampered.Nodes = tamperedNodes

	req := execution.ReplayRequest{Context: coldReplayExecContext(), Case: coldReplayCaseInput(), Committed: tampered}
	data, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	exportPath := filepath.Join(t.TempDir(), "tampered-export.json")
	if err := os.WriteFile(exportPath, data, 0o600); err != nil {
		t.Fatalf("write export: %v", err)
	}

	cmd := exec.Command(bin, "-export", exportPath)
	out, err := cmd.CombinedOutput()
	t.Logf("veriqo-cold-replay output:\n%s", out)
	if err == nil {
		t.Fatal("expected the cold-replay process to exit non-zero on a tampered export")
	}
	if !contains(string(out), "VERDICT                : FAILED") {
		t.Fatalf("expected FAILED verdict from the independent cold-replay process, got:\n%s", out)
	}
}
