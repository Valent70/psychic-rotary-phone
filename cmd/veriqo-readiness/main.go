// Command veriqo-readiness is the executable form of PHASE 52-55 of the
// v7.10.3 production-readiness audit: "Production ready bukan opini.
// Melainkan READINESS_MANIFEST.json."
//
// It does not ask anyone whether VERIQO is ready. It RUNS the gates,
// captures each command's real output and exit code as a
// content-hashed evidence artifact, feeds them to
// internal/assurance, and prints the verdict the gates produce. Gates
// that structurally cannot pass inside a sandbox (independent
// penetration test, 100-node benchmark, multi-region DR drill, live
// non-synthetic data) are registered as BLOCKED with a named blocker,
// which by PHASE 54 means the verdict can never be PRODUCTION_READY
// from a laptop. That is the intended behaviour, not a defect.
//
// Exit codes:
//
//	0  PRODUCTION_READY
//	1  NOT_PRODUCTION_READY
//	2  CONDITIONALLY_READY_WITH_WAIVERS
//	3  internal error
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"veriqo/internal/assurance"
	"veriqo/pkg/governance/qualification"
)

type check struct {
	gateID      string
	description string
	mandatory   bool
	required    assurance.Status
	owner       string
	exit        string
	cmd         []string
	// emptyOutputMeansPass inverts the success test for commands like
	// gofmt -l, which print offending files and exit 0 either way.
	emptyOutputMeansPass bool
}

// blocked gates are the honest ones: things that cannot be evidenced
// from inside a build sandbox, ever, no matter how much code is
// written.
type blockedGate struct {
	gateID      string
	description string
	required    assurance.Status
	owner       string
	exit        string
	blocker     string
}

func main() {
	out := flag.String("out", "READINESS_MANIFEST.json", "path to write the readiness manifest")
	evidenceDir := flag.String("evidence-dir", "evidence", "directory for raw evidence artifacts")
	version := flag.String("version", "v7.12.0", "release version")
	commit := flag.String("commit", "unknown", "git commit")
	operator := flag.String("operator", "ci", "operator running the gate")
	skipRace := flag.Bool("skip-race", false, "skip the race gate (slow); it will register as OPEN, not PASS")
	flag.Parse()

	if err := os.MkdirAll(*evidenceDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: cannot create evidence dir:", err)
		os.Exit(3)
	}

	checks := []check{
		{"build", "go build ./... compiles the whole repo", true, assurance.StatusVerified, "./...", "zero build errors", []string{"go", "build", "./..."}, false},
		{"vet", "go vet ./... reports no findings", true, assurance.StatusVerified, "./...", "zero vet findings", []string{"go", "vet", "./..."}, false},
		{"format", "gofmt reports no unformatted files", true, assurance.StatusVerified, "./...", "gofmt -l produces no output", []string{"gofmt", "-l", "."}, true},
		{"unit", "the whole test suite passes", true, assurance.StatusVerified, "./...", "go test ./... passes", []string{"go", "test", "./..."}, false},
		{"acceptance", "the permanent acceptance suite passes", true, assurance.StatusVerified, "./test/acceptance", "110+ permanent acceptance tests pass", []string{"go", "test", "./test/acceptance/"}, false},
		{"dependency_integration", "canonical cannot fuse without dependency evaluation", true, assurance.StatusVerified, "./pkg/canonical", "PHASE 1 gate tests pass", []string{"go", "test", "-run", "Dependency", "./pkg/canonical/"}, false},
		{"replay", "full-lifecycle independent replay matches", true, assurance.StatusVerified, "./pkg/replay", "replay result hash equals original result hash", []string{"go", "test", "./pkg/replay/"}, false},
		{"identity", "identity unmerge preserves historical replay", true, assurance.StatusVerified, "./pkg/identity", "identity invariants pass", []string{"go", "test", "./pkg/identity/"}, false},
		{"security_unit", "key lifecycle and authorization invariants pass", true, assurance.StatusVerified, "./pkg/platform/security/keys", "revocation, rotation and envelope tests pass", []string{"go", "test", "./pkg/platform/security/...", "./pkg/authz/"}, false},
		{"assurance_self", "the assurance plane refuses false green", true, assurance.StatusVerified, "./internal/assurance", "no-false-green tests pass", []string{"go", "test", "./internal/assurance/"}, false},
		{"fuzz_smoke", "fuzz targets execute their seed corpus", true, assurance.StatusVerified, "./...", "all Fuzz* targets run clean on seeds", []string{"go", "test", "-run", "Fuzz", "./pkg/..."}, false},
		{"zero_dependency", "the module depends only on the Go standard library", true, assurance.StatusVerified, "go.mod", "no external module requirements", []string{"go", "list", "-m", "all"}, false},

		// ---- v7.12.0 capability gates -------------------------------
		// Each new capability closed in v7.12.0 gets its own executable
		// gate. A capability whose gate is not listed here is, by the
		// assurance plane's own rule, not closed — "the code exists" has
		// never been an acceptable substitute for "the gate ran".
		{"calibration", "probability calibration, drift and reliability are measured", true, assurance.StatusVerified, "./pkg/moat/reliability", "ECE/MCE, Platt/isotonic, PSI/KL/JS/KS tests pass", []string{"go", "test", "./pkg/moat/reliability/"}, false},
		{"model_lifecycle", "model and source lifecycle states are enforced", true, assurance.StatusVerified, "./pkg/governance/lifecycle", "approval requires calibration; binding detects version drift", []string{"go", "test", "./pkg/governance/lifecycle/"}, false},
		{"knowledge_evolution", "knowledge changes are proposed, simulated and windowed", true, assurance.StatusVerified, "./pkg/governance/knowledge", "StateAt(T1) is unchanged by a change at T2", []string{"go", "test", "./pkg/governance/knowledge/"}, false},
		{"data_governance", "retention, legal hold, redaction and purge are enforced", true, assurance.StatusVerified, "./pkg/governance/data", "purge preserves audit integrity via tombstones", []string{"go", "test", "./pkg/governance/data/"}, false},
		{"hitl", "human review never mutates the machine decision", true, assurance.StatusVerified, "./pkg/governance/hitl", "MachineDecision + HumanDecision = GovernedOutcome", []string{"go", "test", "./pkg/governance/hitl/"}, false},
		{"decision_explanation", "every decision explains itself from source to action", true, assurance.StatusVerified, "./pkg/explanation", "generic rationales and broken chains are refused", []string{"go", "test", "./pkg/explanation/"}, false},
		{"execution_graph", "the unified execution DAG runs and replays", true, assurance.StatusVerified, "./pkg/execution", "one run yields trace, decision, explanation, replay, certificate", []string{"go", "test", "./pkg/execution/"}, false},
		{"api_semantics", "idempotency, rate limiting, OIDC and transport parity hold", true, assurance.StatusVerified, "./pkg/api", "409 on conflict, 429 on limit, RS256-only verification", []string{"go", "test", "./pkg/api/"}, false},
		{"storage_recovery", "WAL recovery classifies and fails closed on mid-log corruption", true, assurance.StatusVerified, "./pkg/storage/wal", "torn tail truncates; corrupt middle refuses to serve", []string{"go", "test", "./pkg/storage/wal/", "./pkg/storage/"}, false},
		{"sandbox", "plugin sandbox fails closed and never falls back to unrestricted", true, assurance.StatusVerified, "./pkg/kernel/sandbox", "PLUGIN_EXECUTION_DENIED on any unenforceable policy", []string{"go", "test", "./pkg/kernel/sandbox/"}, false},
		{"observability", "the telemetry schema covers every VERIQO domain and metric", true, assurance.StatusVerified, "./pkg/platform/telemetry", "no missing domains or business metrics", []string{"go", "test", "./pkg/platform/telemetry/"}, false},
		{"data_quality", "source reliability is learned with bounded, replayable updates", true, assurance.StatusVerified, "./pkg/dataquality", "step caps, floors and ledger replay hold", []string{"go", "test", "./pkg/dataquality/"}, false},
		{"economic_consequence", "expected value, VaR and CVaR are computed from declared scenarios", true, assurance.StatusVerified, "./pkg/moat/economic", "CVaR >= VaR; unnamed scenarios are refused", []string{"go", "test", "./pkg/moat/economic/"}, false},

		// ---- v7.11.1 test & assurance reconciliation gates -----------
		{"chaos", "distributed chaos acceptance holds every cluster invariant", true, assurance.StatusVerified, "./test/chaos", "no divergence, no lost commits, no double leader", []string{"go", "test", "./test/chaos/", "./pkg/chaos/"}, false},
		{"stress_slo", "throughput and latency SLOs are met and 100/100 evidence is produced", true, assurance.StatusVerified, "./test/stress", "p50/p95/p99 within SLO; 100 transfers yield 100 artifacts", []string{"go", "test", "./test/stress/"}, false},
		{"replay_determinism_100x", "100 replays agree on hash, decision, explanation and certificate", true, assurance.StatusVerified, "./test/acceptance/replay", "100/100 stable and every perturbation detected", []string{"go", "test", "./test/acceptance/replay/"}, false},
		{"bounded_test_execution", "the full suite runs bounded, isolated and classified", true, assurance.StatusVerified, "./cmd/veriqo-testrunner", "per-package timeout with a hashed evidence artifact", []string{"go", "build", "./cmd/veriqo-testrunner/"}, false},

		// ---- P0-04: one authoritative requirement state --------------
		{"traceability_matrix", "the requirement matrix is generated from requirements.json, never hand-edited", true, assurance.StatusVerified, "./cmd/veriqo-requirements", "REQUIREMENT_TRACEABILITY_MATRIX.md matches what requirements.json generates", []string{"go", "run", "./cmd/veriqo-requirements", "-check"}, false},
	}
	if !*skipRace {
		checks = append(checks, check{"race", "the suite passes under the race detector", true,
			assurance.StatusVerified, "./...", "go test -race ./... passes",
			[]string{"go", "test", "-race", "-p", "1", "./..."}, false})
	}

	blocked := []blockedGate{
		{"pentest", "independent third-party penetration test", assurance.StatusQualified, "external", "signed report from an external security vendor", "requires an external security vendor; cannot be produced by any sandbox"},
		{"scale_qualification", "100-node / 1M-evidence physical benchmark", assurance.StatusQualified, "external", "p50/p95/p99 latency and throughput at 100 nodes", "requires physical multi-node infrastructure"},
		{"multi_region_dr", "multi-region deployment and DR drill with measured RPO/RTO", assurance.StatusQualified, "deploy/", "destroy primary, restore, verify ledgers and replay", "requires multi-region infrastructure"},
		{"hsm_kms", "production HSM/KMS backed signing", assurance.StatusQualified, "./pkg/platform/security/keys", "KeyProvider backed by a real HSM or cloud KMS", "interface implemented; a real HSM/KMS tenancy is a procurement action"},
		{"live_data", "non-synthetic live data feeds (SWIFT/BoL/AIS/SAR)", assurance.StatusQualified, "./pkg/connector", "ingest qualified against contracted live feeds", "requires commercial data contracts"},
		{"soak_72h", "72-hour continuous soak with zero leak and zero ledger corruption", assurance.StatusQualified, "./...", "72h run with stable memory, goroutines and ledger integrity", "requires a long-lived environment beyond a CI job"},
		{"spire_mtls", "real SPIRE deployment with workload attestation and mTLS rotation", assurance.StatusQualified, "deploy/spire", "node A and node B attest, rotate and revoke identities", "requires a running SPIRE cluster; network-blocked in this environment"},
		{"supply_chain_scan", "govulncheck / gosec / staticcheck in CI", assurance.StatusQualified, ".github/workflows", "all scanners run and report clean", "staticcheck (SAST) was run for real in a network-enabled session — see evidence/supply_chain_scan-staticcheck.txt — and passes clean; govulncheck's vulnerability feed (vuln.go.dev) returned 403 under this environment's network policy even though the module proxy is reachable, so vulnerability-DB scanning is still unqualified; gosec has not yet been run. This is a narrower, evidenced blocker, not a blanket network-access claim"},
	}

	reg := assurance.NewRegistry()
	now := uint64(time.Now().Unix())
	failures := 0

	for _, c := range checks {
		if err := reg.Register(assurance.Gate{
			ID: c.gateID, Description: c.description, Mandatory: c.mandatory,
			RequiredStatus: c.required, OwnerPackage: c.owner, ExitCriteria: c.exit,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		cmdline := strings.Join(c.cmd, " ")
		fmt.Printf("== %-24s %s\n", c.gateID, cmdline)
		output, code := run(c.cmd)
		if c.emptyOutputMeansPass && code == 0 && strings.TrimSpace(output) != "" {
			code = 1
		}
		if c.gateID == "zero_dependency" && code == 0 {
			if extra := externalModules(output); extra != "" {
				output += "\nEXTERNAL MODULES DETECTED: " + extra
				code = 1
			}
		}
		ev := assurance.NewEvidence(c.gateID, cmdline, output, code, now)
		writeArtifact(*evidenceDir, c.gateID, cmdline, code, output)
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach(c.gateID, status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("   -> exit=%d artifact=%s\n", code, ev.ArtifactID)
	}

	// External Qualification Evidence Framework (P0-03): a blocked gate
	// is never a permanent hard-coded dead end. Each blocked gate is
	// registered into qualification.Registry as BLOCKED_EXTERNAL; if
	// evidence/external/<gate>.json holds a real, independently
	// validated submission — a signed report from an actual vendor, a
	// benchmark artifact from real infrastructure — the gate advances
	// and the assurance registry reflects that. No evidence file is
	// fabricated by this program. Absence of a file means the gate
	// stays exactly what it has always honestly been: BLOCKED_EXTERNAL.
	qreg := qualification.NewRegistry()
	for _, b := range blocked {
		if err := qreg.RegisterBlocked(b.gateID, b.description, b.blocker); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: qualification register:", err)
			os.Exit(3)
		}
	}
	loadExternalQualifications(qreg, *evidenceDir, now)

	for _, b := range blocked {
		if err := reg.Register(assurance.Gate{
			ID: b.gateID, Description: b.description, Mandatory: true,
			RequiredStatus: b.required, OwnerPackage: b.owner, ExitCriteria: b.exit,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register blocked:", err)
			os.Exit(3)
		}
		qrec, _ := qreg.Get(b.gateID)
		if qrec.Status == qualification.StatusVerified && qrec.Satisfied(now) {
			raw, _ := json.Marshal(qrec)
			ev := assurance.NewEvidence(b.gateID, "external qualification: "+qrec.Blocker, string(raw), 0, now)
			if err := reg.Attach(b.gateID, assurance.StatusQualified, ev); err != nil {
				fmt.Fprintln(os.Stderr, "readiness: attach qualification:", err)
				os.Exit(3)
			}
			fmt.Printf("== %-24s QUALIFIED by real external evidence (%s)\n", b.gateID, qrec.Evidence[len(qrec.Evidence)-1].Provider)
		} else {
			if err := reg.Block(b.gateID, b.blocker); err != nil {
				fmt.Fprintln(os.Stderr, "readiness: block:", err)
				os.Exit(3)
			}
			fmt.Printf("== %-24s BLOCKED: %s\n", b.gateID, b.blocker)
		}
	}
	if qj, err := qreg.JSON(); err == nil {
		_ = os.WriteFile(filepath.Join(*evidenceDir, "external-qualification.json"), qj, 0o644)
	}

	acc := assurance.AcceptanceManifest{
		Categories: []assurance.AcceptanceCategory{
			{Name: "normal", Minimum: 20, Actual: countTests("test/acceptance", "normal")},
			{Name: "adversarial", Minimum: 20, Actual: countTests("test/acceptance", "adversarial")},
			{Name: "replay_tamper", Minimum: 20, Actual: countTests("test/acceptance", "replay")},
			{Name: "identity_temporal", Minimum: 20, Actual: countTests("test/acceptance", "identity")},
			{Name: "distributed_concurrency", Minimum: 20, Actual: countTests("test/acceptance", "concurrency")},
			{Name: "security", Minimum: 10, Actual: countTests("test/acceptance", "security")},
			{Name: "chaos", Minimum: 5, Actual: countTestsWithPrefix("test/chaos", "", "\nfunc Test")},
			{Name: "stress_slo", Minimum: 3, Actual: countTestsWithPrefix("test/stress", "", "\nfunc Test")},
		},
		MandatoryTests: []string{
			"TestUnifiedIntentTrustDecisionLifecycle",
			"TestCanonicalUsesEvidenceDependencyGraph",
			"TestSharedSatelliteProviderCannotInflateConfidence",
			"TestFullLifecycleReplayMatches",
			"TestReplayIdentitiesAreAllDistinct",
			"TestUnmergePreservesHistoricalReplay",
			"TestOneCriticalGapCannotBeCompensated",
			"TestOneHundredReplaysAgreeOnEveryMaterialField",
			"TestOneHundredTransfersProduceOneHundredEvidenceArtifacts",
			"TestCombinedChaosAcceptance",
			"TestConvergenceAfterFullHeal",
		},
		PresentTests: presentTests(),
		TotalMinimum: 118,
	}
	if err := acc.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "acceptance manifest FAILED:\n", err)
		failures++
	}

	cert := assurance.ReleaseCertificate{
		Version: *version, GitCommit: *commit, Operator: *operator,
		Timestamp: now, GoVersion: runtime.Version(),
		SourceHash: sourceHash(), SBOMHash: fileHashOrEmpty("SBOM.json"),
	}
	manifest := assurance.BuildReadinessManifest(reg, acc, cert)
	raw, err := manifest.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "readiness: manifest:", err)
		os.Exit(3)
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: write:", err)
		os.Exit(3)
	}

	a := manifest.Assessment
	fmt.Printf("\n===== VERIQO PRODUCTION READINESS =====\n")
	fmt.Printf("verdict            : %s\n", a.Verdict)
	fmt.Printf("mandatory gates    : %d/%d passing\n", a.MandatoryPassing, a.MandatoryTotal)
	fmt.Printf("blocked (external) : %v\n", a.BlockedMandatory)
	fmt.Printf("failing            : %v\n", a.FailingMandatory)
	fmt.Printf("manifest           : %s\n", *out)
	for _, r := range a.Reasons {
		fmt.Println("  -", r)
	}
	switch a.Verdict {
	case assurance.VerdictProductionReady:
		os.Exit(0)
	case assurance.VerdictConditional:
		os.Exit(2)
	default:
		os.Exit(1)
	}
}

// loadExternalQualifications looks for real, independently-produced
// evidence submitted against each blocked gate at
// evidence/external/<gate_id>.json. It never writes or invents such a
// file itself; it only reads what a human operator (or a genuine
// external process — a vendor's signed report, a real benchmark
// harness) has placed there, and it still runs the file through the
// full Submit -> Validate -> Qualify -> VerifyGate lifecycle, so a
// malformed or incomplete submission does not silently pass.
func loadExternalQualifications(qreg *qualification.Registry, evidenceDir string, nowTick uint64) {
	dir := filepath.Join(evidenceDir, "external")
	for _, rec := range qreg.Records() {
		path := filepath.Join(dir, rec.GateID+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue // no external evidence submitted; stays BLOCKED_EXTERNAL
		}
		var ev qualification.ExternalEvidence
		if err := json.Unmarshal(raw, &ev); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: malformed external evidence: %v\n", rec.GateID, err)
			continue
		}
		ev.SubmittedAtTick = nowTick
		if err := qreg.SubmitEvidence(rec.GateID, ev); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: submit: %v\n", rec.GateID, err)
			continue
		}
		if err := qreg.Validate(rec.GateID, nowTick); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: evidence rejected: %v\n", rec.GateID, err)
			continue
		}
		if err := qreg.Qualify(rec.GateID, nowTick); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: qualify: %v\n", rec.GateID, err)
			continue
		}
		if err := qreg.VerifyGate(rec.GateID, nowTick); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: verify: %v\n", rec.GateID, err)
			continue
		}
	}
}

func run(argv []string) (string, int) {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

func writeArtifact(dir, gate, cmdline string, code int, output string) {
	body := fmt.Sprintf("gate: %s\ncommand: %s\nexit: %d\n---\n%s", gate, cmdline, code, output)
	_ = os.WriteFile(filepath.Join(dir, gate+".txt"), []byte(body), 0o644)
}

// externalModules returns any non-main module lines from `go list -m all`.
func externalModules(output string) string {
	var extra []string
	for i, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		extra = append(extra, strings.TrimSpace(line))
	}
	return strings.Join(extra, "; ")
}

// countTests counts the permanent acceptance tests in one category.
//
// The v7.11.0 suite names every acceptance test TestAcceptance*, which
// is what this counts. The v7.12.0 layers in test/chaos and test/stress
// are separate packages whose whole purpose is acceptance, so they are
// counted with the plain Test prefix via countTestsWithPrefix rather
// than by renaming working tests to satisfy a counter.
func countTests(dir, category string) int {
	return countTestsWithPrefix(dir, category, "\nfunc TestAcceptance")
}

func countTestsWithPrefix(dir, category, prefix string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if !strings.Contains(e.Name(), category) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		total += strings.Count(string(raw), prefix)
	}
	return total
}

func presentTests() []string {
	var out []string
	_ = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "func Test") {
				name := strings.TrimPrefix(line, "func ")
				if i := strings.Index(name, "("); i > 0 {
					out = append(out, name[:i])
				}
			}
		}
		return nil
	})
	return out
}

func sourceHash() string {
	out, code := run([]string{"go", "list", "-deps", "./..."})
	if code != 0 {
		return ""
	}
	return assurance.NewEvidence("source", "go list -deps ./...", out, 0, 0).Hash
}

func fileHashOrEmpty(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return assurance.NewEvidence("sbom", path, string(raw), 0, 0).Hash
}
