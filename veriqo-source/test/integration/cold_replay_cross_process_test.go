// TestColdReplay_CrossProcess proves audit item P0-E ("distributed
// cold replay") for real: export a completed execution's full DAG
// trace to a file, launch cmd/veriqo-cold-replay as a genuinely
// separate compiled binary/OS process (no shared memory, no import of
// this test's own engine instance), and confirm it independently
// rebuilds the evidence root, decision, explanation and verification
// certificate purely from that file -- the same cross-process pattern
// TestIVFCrossProcess_* already proves for the fusion/contradiction/
// decision domains (ivf_cross_process_test.go), extended here to the
// full execution DAG.
//
// TestColdReplay_CrossProcess_IdentityResolutionSurvivesColdRestart
// closes what this file's own doc comment used to name as an explicit,
// out-of-scope gap: entity resolution now cold-restores too, via
// cmd/veriqo-cold-replay's -identity-export flag and pkg/identity's
// own ColdReplayExport/VerifyColdReplay (Rebuild's real replay proof,
// finally wired across a genuine process boundary rather than only
// exercised in-process by pkg/identity's own tests).
package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/identity"
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
	res, err := e.Run(context.Background(), execution.Input{Context: coldReplayExecContext(), Case: coldReplayCaseInput()})
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
	res, err := e.Run(context.Background(), execution.Input{Context: coldReplayExecContext(), Case: coldReplayCaseInput()})
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

// TestColdReplay_CrossProcess_IdentityResolutionSurvivesColdRestart is
// the real closure of the gap this file's own doc comment used to name
// explicitly: pkg/identity.Resolver's independent ledger now cold-
// restores across a genuine process boundary too, not just the
// execution DAG. A real Resolver merges IMO+CALLSIGN aliases for one
// vessel, its ledger is exported alongside the two EntityIDAt answers
// the original (pre-restart) process got, and a genuinely separate
// veriqo-cold-replay process -- launched here alongside the SAME
// -export DAG check this file's other tests already prove -- must
// independently rebuild a fresh Resolver from nothing but the export
// and reproduce the identical entity ID for both aliases.
func TestColdReplay_CrossProcess_IdentityResolutionSurvivesColdRestart(t *testing.T) {
	bin := buildVeriqoColdReplay(t)

	e := execution.NewEngine(nil)
	res, err := e.Run(context.Background(), execution.Input{Context: coldReplayExecContext(), Case: coldReplayCaseInput()})
	if err != nil {
		t.Fatalf("original execution: %v", err)
	}
	req := execution.ReplayRequest{Context: coldReplayExecContext(), Case: coldReplayCaseInput(), Committed: res.Trace}
	execData, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	execPath := filepath.Join(t.TempDir(), "cold-export.json")
	if err := os.WriteFile(execPath, execData, 0o600); err != nil {
		t.Fatalf("write export: %v", err)
	}

	// A real identity resolver, in THIS process: merge IMO/CALLSIGN for
	// one vessel, exactly the shape pkg/lifecycle's own production
	// entity-authority path uses.
	idResolver := identity.NewResolver()
	if err := idResolver.RegisterAuthority(identity.Authority{
		SourceID: "flag-registry", Weight: 1, AuthoritativeFor: []identity.Kind{identity.KindIMO},
	}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	imoAlias := identity.Identifier{Kind: identity.KindIMO, Value: "9998887"}
	csAlias := identity.Identifier{Kind: identity.KindCallsign, Value: "ABCD1"}
	if _, err := idResolver.Merge("op", "flag-registry", imoAlias, csAlias, 1, "cold-replay-identity-fixture"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	originalEntityID, err := idResolver.EntityIDAt(imoAlias, 1)
	if err != nil {
		t.Fatalf("EntityIDAt: %v", err)
	}
	if originalEntityID == "" {
		t.Fatal("expected a real, non-empty entity ID from the original process")
	}

	idExport := identity.ColdReplayExport{
		Ledger:      idResolver.Ledger(),
		Authorities: []identity.Authority{{SourceID: "flag-registry", Weight: 1, AuthoritativeFor: []identity.Kind{identity.KindIMO}}},
		Queries: []identity.ColdReplayQuery{
			{Alias: imoAlias, AsOfTick: 1, ExpectedEntityID: originalEntityID},
			{Alias: csAlias, AsOfTick: 1, ExpectedEntityID: originalEntityID},
		},
	}
	idData, err := json.Marshal(idExport)
	if err != nil {
		t.Fatalf("marshal identity export: %v", err)
	}
	idPath := filepath.Join(t.TempDir(), "cold-identity-export.json")
	if err := os.WriteFile(idPath, idData, 0o600); err != nil {
		t.Fatalf("write identity export: %v", err)
	}

	cmd := exec.Command(bin, "-export", execPath, "-identity-export", idPath)
	out, err := cmd.CombinedOutput()
	t.Logf("veriqo-cold-replay output:\n%s", out)
	if err != nil {
		t.Fatalf("cold-replay process exited non-zero on genuine exports: %v", err)
	}
	if !contains(string(out), "VERDICT                : PASSED") {
		t.Fatalf("expected the DAG VERDICT to PASS, got:\n%s", out)
	}
	if !contains(string(out), "IDENTITY VERDICT       : PASSED") {
		t.Fatalf("expected the independent process to reproduce the SAME entity ID (%s) from a cold-restored "+
			"identity ledger alone, got:\n%s", originalEntityID, out)
	}
	if !contains(string(out), "identity queries       : 2 checked") {
		t.Fatalf("expected exactly 2 identity queries checked, got:\n%s", out)
	}
}

// TestColdReplay_CrossProcess_TamperedIdentityLedgerFails is the
// adversarial companion: an identity export whose ledger was altered
// after the fact must be refused by the independent process, exactly
// like TestColdReplay_CrossProcess_TamperedExportFails already proves
// for the execution DAG's own committed trace.
func TestColdReplay_CrossProcess_TamperedIdentityLedgerFails(t *testing.T) {
	bin := buildVeriqoColdReplay(t)

	e := execution.NewEngine(nil)
	res, err := e.Run(context.Background(), execution.Input{Context: coldReplayExecContext(), Case: coldReplayCaseInput()})
	if err != nil {
		t.Fatalf("original execution: %v", err)
	}
	req := execution.ReplayRequest{Context: coldReplayExecContext(), Case: coldReplayCaseInput(), Committed: res.Trace}
	execData, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	execPath := filepath.Join(t.TempDir(), "cold-export.json")
	if err := os.WriteFile(execPath, execData, 0o600); err != nil {
		t.Fatalf("write export: %v", err)
	}

	idResolver := identity.NewResolver()
	if err := idResolver.RegisterAuthority(identity.Authority{SourceID: "flag-registry", Weight: 1}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	imoAlias := identity.Identifier{Kind: identity.KindIMO, Value: "9998887"}
	csAlias := identity.Identifier{Kind: identity.KindCallsign, Value: "ABCD1"}
	if _, err := idResolver.Merge("op", "flag-registry", imoAlias, csAlias, 1, "cold-replay-identity-fixture"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ledger := idResolver.Ledger()
	ledger[0].Confidence = 0.99 // tamper: alters the event without recomputing its hash

	idExport := identity.ColdReplayExport{Ledger: ledger}
	idData, err := json.Marshal(idExport)
	if err != nil {
		t.Fatalf("marshal identity export: %v", err)
	}
	idPath := filepath.Join(t.TempDir(), "cold-identity-export-tampered.json")
	if err := os.WriteFile(idPath, idData, 0o600); err != nil {
		t.Fatalf("write identity export: %v", err)
	}

	cmd := exec.Command(bin, "-export", execPath, "-identity-export", idPath)
	out, err := cmd.CombinedOutput()
	t.Logf("veriqo-cold-replay output:\n%s", out)
	if err == nil {
		t.Fatal("expected the cold-replay process to exit non-zero on a tampered identity ledger")
	}
	if !contains(string(out), "IDENTITY VERDICT       : FAILED") {
		t.Fatalf("expected the tampered identity ledger to be refused, got:\n%s", out)
	}
}

// --- PHASE F3 (P1-13): replay completeness ---------------------------
//
// The existing tests above prove a cold, cross-process replay
// reproduces the evidence root, the decision, the explanation and the
// verification certificate. PHASE F3 asks for something stricter and
// enumerated: that THIRTEEN specifically-named identities are identical
// across a real Process A -> destroy -> Process B replay.
//
// This does NOT build a second replay engine. It runs the same
// veriqo-cold-replay binary the tests above already launch, and parses
// the machine-readable identity block that binary now emits, so the
// comparison is against what a genuinely separate OS process actually
// produced rather than against anything recomputed in this process.

// coldReplayIdentities mirrors cmd/veriqo-cold-replay's own
// ReplayIdentities. It is duplicated here rather than imported because
// Go cannot import a main package -- the same constraint every
// cross-process test in this repository works around, and the reason
// the binary emits JSON in the first place.
type coldReplayIdentities struct {
	IntentID                   string `json:"intent_id"`
	ExecutionID                string `json:"execution_id"`
	EvidencePackageID          string `json:"evidence_package_id"`
	EntityID                   string `json:"entity_id"`
	IdentityLedgerHead         string `json:"identity_ledger_head"`
	PolicyHash                 string `json:"policy_hash"`
	TemporalModelHash          string `json:"temporal_model_hash"`
	InferenceTraceHash         string `json:"inference_trace_hash"`
	DecisionID                 string `json:"decision_id"`
	ExplanationHash            string `json:"explanation_hash"`
	DigitalTwinConsequenceHash string `json:"digital_twin_consequence_hash"`
	VerificationCertificateID  string `json:"verification_certificate_id"`
	ReplayPackageID            string `json:"replay_package_id"`
}

func (r coldReplayIdentities) values() map[string]string {
	return map[string]string{
		"intent_id": r.IntentID, "execution_id": r.ExecutionID,
		"evidence_package_id": r.EvidencePackageID, "entity_id": r.EntityID,
		"identity_ledger_head": r.IdentityLedgerHead, "policy_hash": r.PolicyHash,
		"temporal_model_hash": r.TemporalModelHash, "inference_trace_hash": r.InferenceTraceHash,
		"decision_id": r.DecisionID, "explanation_hash": r.ExplanationHash,
		"digital_twin_consequence_hash": r.DigitalTwinConsequenceHash,
		"verification_certificate_id":   r.VerificationCertificateID,
		"replay_package_id":             r.ReplayPackageID,
	}
}

// identitiesOf extracts the same thirteen values from an in-process
// Result, using exactly the derivations cmd/veriqo-cold-replay uses.
func identitiesOf(res *execution.Result, resolver *identity.Resolver) coldReplayIdentities {
	out := coldReplayIdentities{
		IntentID:                  res.Trace.Context.CaseID,
		ExecutionID:               res.Trace.Context.ExecutionID,
		EvidencePackageID:         res.Trace.Context.EvidencePackageID,
		DecisionID:                res.Explanation.DecisionID,
		InferenceTraceHash:        res.ExecutionRootHash,
		ExplanationHash:           res.Explanation.Hash,
		VerificationCertificateID: res.Certificate.VerificationCertificateID,
		ReplayPackageID:           res.ReplayPackage.ReplayPackageID,
	}
	out.PolicyHash = res.ExpectedPolicyHash
	if out.PolicyHash == "" {
		out.PolicyHash = res.Case.Policy.Hash()
	}
	if resolver != nil {
		out.IdentityLedgerHead = resolver.Head()
	}
	if res.Canonical != nil {
		out.EntityID = string(res.Canonical.Twin.Entity)
		out.DigitalTwinConsequenceHash = res.Canonical.Certificate.TwinHead
	}
	for _, n := range res.Trace.Nodes {
		if n.StageID == execution.StageTemporal {
			out.TemporalModelHash = n.Hash
		}
	}
	return out
}

func parseColdReplayIdentities(t *testing.T, stdout string) coldReplayIdentities {
	t.Helper()
	const begin = "--- BEGIN REPLAY IDENTITIES (JSON) ---\n"
	const end = "--- END REPLAY IDENTITIES (JSON) ---"
	i := strings.Index(stdout, begin)
	if i < 0 {
		t.Fatalf("cold-replay output carries no identity block:\n%s", stdout)
	}
	i += len(begin)
	j := strings.Index(stdout[i:], end)
	if j < 0 {
		t.Fatalf("cold-replay identity block is not terminated:\n%s", stdout)
	}
	var out coldReplayIdentities
	if err := json.Unmarshal([]byte(stdout[i:i+j]), &out); err != nil {
		t.Fatalf("cold-replay identity block is not valid JSON: %v\n%s", err, stdout[i:i+j])
	}
	return out
}

// TestColdReplay_CrossProcess_AllThirteenIdentitiesMatch is PHASE F3's
// acceptance criterion, stated exactly: Process A runs, its state is
// destroyed, Process B replays from the exported file alone, and all
// thirteen named identities compare equal.
func TestColdReplay_CrossProcess_AllThirteenIdentitiesMatch(t *testing.T) {
	bin := buildVeriqoColdReplay(t)

	// --- Process A: the original execution -------------------------
	resolver := identity.NewResolver()
	if err := resolver.RegisterAuthority(identity.Authority{SourceID: "cold-replay-authority", Weight: 1}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	if _, err := resolver.Merge("cold-replay-test", "cold-replay-authority",
		identity.Identifier{Kind: identity.KindIMO, Value: "7778889"},
		identity.Identifier{Kind: identity.KindCallsign, Value: "COLD1"}, 12, "cold replay identity fixture"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	engine := execution.NewEngine(nil)
	engine.Identity = resolver
	original, err := engine.Run(context.Background(), execution.Input{
		Context: coldReplayExecContext(), Case: coldReplayCaseInput(),
	})
	if err != nil {
		t.Fatalf("original execution: %v", err)
	}
	before := identitiesOf(original, resolver)

	// Every identity must be non-empty before the comparison means
	// anything: comparing "" to "" thirteen times would pass and prove
	// nothing at all. This assertion is what makes the comparison below
	// load-bearing rather than vacuous.
	for name, v := range before.values() {
		if v == "" {
			t.Errorf("identity %q is empty in the ORIGINAL run -- comparing it across processes would be vacuous", name)
		}
	}

	exportPath := filepath.Join(t.TempDir(), "execution.json")
	raw, err := original.ExportReplay()
	if err != nil {
		t.Fatalf("ExportReplay: %v", err)
	}
	if err := os.WriteFile(exportPath, raw, 0o600); err != nil {
		t.Fatalf("write export: %v", err)
	}

	idExportPath := filepath.Join(filepath.Dir(exportPath), "identity.json")
	imoAlias := identity.Identifier{Kind: identity.KindIMO, Value: "7778889"}
	csAlias := identity.Identifier{Kind: identity.KindCallsign, Value: "COLD1"}
	originalEntityID, err := resolver.EntityIDAt(imoAlias, 12)
	if err != nil {
		t.Fatalf("EntityIDAt: %v", err)
	}
	idExport := identity.ColdReplayExport{
		Ledger:      resolver.Ledger(),
		Authorities: []identity.Authority{{SourceID: "cold-replay-authority", Weight: 1}},
		Queries: []identity.ColdReplayQuery{
			{Alias: imoAlias, AsOfTick: 12, ExpectedEntityID: originalEntityID},
			{Alias: csAlias, AsOfTick: 12, ExpectedEntityID: originalEntityID},
		},
	}
	idRaw, err := json.Marshal(idExport)
	if err != nil {
		t.Fatalf("marshal identity export: %v", err)
	}
	if err := os.WriteFile(idExportPath, idRaw, 0o600); err != nil {
		t.Fatalf("write identity export: %v", err)
	}

	// --- destroy: every in-process handle to the original run -------
	engine = nil
	resolver = nil
	original = nil
	_ = engine
	_ = resolver
	_ = original

	// --- Process B: a genuinely separate OS process -----------------
	cmd := exec.Command(bin, "-export", exportPath, "-identity-export", idExportPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cold replay process failed: %v\n%s", err, out)
	}
	after := parseColdReplayIdentities(t, string(out))

	// --- compare all thirteen ---------------------------------------
	beforeValues, afterValues := before.values(), after.values()
	if len(beforeValues) != 13 || len(afterValues) != 13 {
		t.Fatalf("expected 13 identities on each side, got %d and %d", len(beforeValues), len(afterValues))
	}
	names := make([]string, 0, len(beforeValues))
	for n := range beforeValues {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if beforeValues[name] != afterValues[name] {
			t.Errorf("identity %q did not survive the cross-process replay:\n  process A: %q\n  process B: %q",
				name, beforeValues[name], afterValues[name])
		}
	}
}

// TestColdReplay_CrossProcess_IdentityBlockAbsentOnDivergence records a
// deliberate decision: a diverged replay's field values are the values
// of a run that did NOT reproduce, so emitting them as if they were
// comparable would invite exactly the wrong conclusion.
func TestColdReplay_CrossProcess_IdentityBlockAbsentOnDivergence(t *testing.T) {
	bin := buildVeriqoColdReplay(t)

	engine := execution.NewEngine(nil)
	original, err := engine.Run(context.Background(), execution.Input{
		Context: coldReplayExecContext(), Case: coldReplayCaseInput(),
	})
	if err != nil {
		t.Fatalf("original execution: %v", err)
	}
	raw, err := original.ExportReplay()
	if err != nil {
		t.Fatalf("ExportReplay: %v", err)
	}

	// Tamper with the committed trace so the replay must diverge.
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	committed, _ := req["committed_trace"].(map[string]any)
	nodes, _ := committed["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("exported trace has no nodes to tamper with")
	}
	first, _ := nodes[0].(map[string]any)
	first["hash"] = "0000000000000000000000000000000000000000000000000000000000000000"
	tampered, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	exportPath := filepath.Join(t.TempDir(), "tampered.json")
	if err := os.WriteFile(exportPath, tampered, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := exec.Command(bin, "-export", exportPath)
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "BEGIN REPLAY IDENTITIES") {
		t.Fatal("a diverged replay emitted a comparable identity block -- those values describe a run that did not reproduce")
	}
	if !strings.Contains(string(out), "FAILED") {
		t.Fatalf("a tampered export did not produce a FAILED verdict:\n%s", out)
	}
}
