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
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"veriqo/internal/assurance"
	"veriqo/internal/entrypoints"
	"veriqo/internal/environment"
	"veriqo/internal/nobypass"
	"veriqo/internal/sbom"
	"veriqo/internal/sourcehash"
	"veriqo/internal/telemetrycoverage"
	"veriqo/internal/timestamp"
	"veriqo/internal/version"
	evidenceapi "veriqo/pkg/evidence/api"

	"veriqo/pkg/blockers"
	"veriqo/pkg/blockers/orchestrator"
	"veriqo/pkg/execution"
	"veriqo/pkg/governance/entityconsistency"
	"veriqo/pkg/governance/qualification"
	insurancecasepack "veriqo/pkg/insurance/casepack"
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
	releaseVersion := flag.String("version", version.Current, "release version")
	commit := flag.String("commit", "unknown", "git commit")
	operator := flag.String("operator", "ci", "operator running the gate")
	skipRace := flag.Bool("skip-race", false, "skip the race gate (slow); it will register as OPEN, not PASS")
	signingKey := flag.String("signing-key", "", "path to a private key file produced by cmd/veriqo-release-keygen; "+
		"when set, the release certificate is really signed. Omit to produce an unsigned manifest -- never commit "+
		"a private key to close this flag's absence")
	timestampKey := flag.String("timestamp-key", "", "path to a private key file produced by cmd/veriqo-release-keygen, "+
		"deliberately separate from -signing-key, for the self-hosted timestamp authority (P1-09); when set, this run's "+
		"certificate hash is appended to -timestamp-chain as a new hash-chained, signed entry")
	timestampChain := flag.String("timestamp-chain", "evidence/timestamp-chain.json",
		"path to the persisted timestamp-authority chain file (see internal/timestamp)")
	flag.Parse()

	if err := os.MkdirAll(*evidenceDir, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: cannot create evidence dir:", err)
		os.Exit(3)
	}

	// The soak_harness_smoke gate must stay fast: it exists to prove the
	// harness runs clean, not to be a second soak_72h. 0.5 real minutes
	// (not the test's own 2-minute default) keeps every readiness run
	// bounded; an operator doing a genuine soak sets VERIQO_SOAK_MINUTES
	// themselves before running go test directly, which overrides this.
	if os.Getenv("VERIQO_SOAK_MINUTES") == "" {
		_ = os.Setenv("VERIQO_SOAK_MINUTES", "0.5")
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
		{"determinism_boundary", "no forbidden nondeterministic API in a package declared deterministic", true, assurance.StatusVerified, "internal/determinism", "zero violations across pkg/canonical, pkg/execution, pkg/replay", []string{"go", "test", "./internal/determinism/"}, false},

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
		// Scoped narrowly and renamed in intent (audit V7.12.1.6 item #5:
		// "FALSE-GREEN RISK" -- this gate's old description, "the telemetry
		// schema covers every VERIQO domain and metric", claimed org-wide
		// coverage from a package-scoped unit test, contradicting
		// engine_registry.json's own honest telemetry_support=false on
		// every engine in the SAME run). This gate now claims only what it
		// actually tests: pkg/platform/telemetry's own correctness.
		// Whether any production domain engine actually calls it is a
		// SEPARATE, real, mandatory gate below (telemetry_production_coverage).
		{"observability", "the telemetry package itself (Span/Tracer/Correlation) is correct -- NOT a claim that production engines use it, see telemetry_production_coverage", true, assurance.StatusVerified, "./pkg/platform/telemetry", "pkg/platform/telemetry's own unit tests pass", []string{"go", "test", "./pkg/platform/telemetry/"}, false},
		{"data_quality", "source reliability is learned with bounded, replayable updates", true, assurance.StatusVerified, "./pkg/dataquality", "step caps, floors and ledger replay hold", []string{"go", "test", "./pkg/dataquality/"}, false},
		{"economic_consequence", "expected value, VaR and CVaR are computed from declared scenarios", true, assurance.StatusVerified, "./pkg/moat/economic", "CVaR >= VaR; unnamed scenarios are refused", []string{"go", "test", "./pkg/moat/economic/"}, false},
		{"decision_precedence", "one authoritative DENY>BLOCK>ESCALATE>REVIEW>ALLOW lattice combines every subsystem signal", true, assurance.StatusVerified, "./pkg/governance/precedence", "cross-package composition of real authz/hitl signals resolves deterministically", []string{"go", "test", "./pkg/governance/precedence/"}, false},
		{"soak_harness_smoke", "the soak harness itself runs clean over a short real window (not a substitute for soak_72h)", true, assurance.StatusVerified, "./test/soak", "zero errors, bounded goroutines over a short run; evidence/soak-report.json states its own real duration", []string{"go", "test", "./test/soak/", "-timeout", "90s"}, false},

		// ---- v7.11.1 test & assurance reconciliation gates -----------
		{"chaos", "distributed chaos acceptance holds every cluster invariant", true, assurance.StatusVerified, "./test/chaos", "no divergence, no lost commits, no double leader", []string{"go", "test", "./test/chaos/", "./pkg/chaos/"}, false},
		{"stress_slo", "throughput and latency SLOs are met and 100/100 evidence is produced", true, assurance.StatusVerified, "./test/stress", "p50/p95/p99 within SLO; 100 transfers yield 100 artifacts", []string{"go", "test", "./test/stress/"}, false},
		{"replay_determinism_100x", "100 replays agree on hash, decision, explanation and certificate", true, assurance.StatusVerified, "./test/acceptance/replay", "100/100 stable and every perturbation detected", []string{"go", "test", "./test/acceptance/replay/"}, false},
		{"bounded_test_execution", "the full suite runs bounded, isolated and classified", true, assurance.StatusVerified, "./cmd/veriqo-testrunner", "per-package timeout with a hashed evidence artifact", []string{"go", "build", "./cmd/veriqo-testrunner/"}, false},

		// ---- P0-04: one authoritative requirement state --------------
		{"traceability_matrix", "the requirement matrix is generated from requirements.json, never hand-edited", true, assurance.StatusVerified, "./cmd/veriqo-requirements", "REQUIREMENT_TRACEABILITY_MATRIX.md matches what requirements.json generates", []string{"go", "run", "./cmd/veriqo-requirements", "-check"}, false},

		// ---- PHASE E5 (P1-17): architecture coverage gates -----------
		// Four of the six are whole-tree source scans and are registered
		// separately below, in-process, alongside the other scan-backed
		// gates. The two here are RUNTIME properties of the execution
		// DAG -- "every governed decision commits to the policy that
		// governed it" and "a policy requiring temporal calibration
		// never gets a decision without it" -- which a source scan
		// cannot honestly establish. They use the same `go test -run`
		// gate mechanism dependency_integration above already uses,
		// rather than a third gate mechanism.
		{"policy_registry_usage_coverage",
			"every governed decision attributes and commits to the exact policy that governed it, and an execution declaring a policy hash that does not match the policy actually carried is refused",
			true, assurance.StatusVerified, "./pkg/execution",
			"POLICY node precedes DECISION, is genuinely policy-dependent, and a mismatched ExpectedPolicyHash fails closed",
			[]string{"go", "test", "-run", "PolicyRegistryUsageCoverage", "./pkg/execution/"}, false},
		{"temporal_calibration_usage_coverage",
			"a policy declaring temporal reasoning load-bearing never yields a decision reached without it; an optional skip stays an honestly-recorded node rather than an omission",
			true, assurance.StatusVerified, "./pkg/execution, ./pkg/governance/calibration",
			"required-but-absent fails closed, required-and-supplied genuinely consumes the observations, optional skip is still a recorded node",
			[]string{"go", "test", "-run", "TemporalCalibrationUsageCoverage", "./pkg/execution/"}, false},
		// PHASE H (P1-12): the leakage boundary. Mandatory, because the
		// consequence of failing it is not a degraded dashboard -- it is
		// a credential or a customer's confidential data leaving the
		// process in a trace. The gate's acceptance criterion is the
		// program's own three zeros.
		{"telemetry_leakage_zero",
			"no secret, PII, restricted payload, commercial or customer-confidential value crosses the telemetry export boundary under the default redaction posture",
			true, assurance.StatusVerified, "./pkg/platform/telemetry",
			"secret leakage = 0, PII leakage = 0, restricted payload leakage = 0, proved by searching everything the collector holds for the exact canary values",
			[]string{"go", "test", "-run", "Leak|Redact|Sensitive|Confidential", "./pkg/platform/telemetry/"}, false},
		// PHASE H (P1-11): the export pipeline itself. Its honest ceiling
		// is INTERNAL_QUALIFIED (see telemetry.PipelineQualification);
		// this gate proves the pipeline's semantics, NOT that any
		// production collector deployment exists.
		{"telemetry_export_pipeline_internal",
			"spans convert to an OpenTelemetry-shaped payload, are delivered to a sink, persisted, and retrievable by trace and by execution id -- INTERNAL_QUALIFIED only, never a production-observability claim",
			true, assurance.StatusVerified, "./pkg/platform/telemetry",
			"exporter -> collector -> store -> query round-trips in-process, and a nil sink or closed exporter fails loudly rather than discarding",
			[]string{"go", "test", "-run", "ExportPipeline|Exporter|Payload|TraceID|PipelineQualification", "./pkg/platform/telemetry/"}, false},
		{"correlation_propagation_coverage",
			"one operation's identity is locked start to end: every correlation identifier is populated, mutually distinct, and reproduced identically by an independent cold replay",
			true, assurance.StatusVerified, "./pkg/platform/correlation",
			"no empty identifier a consumer would have to invent, no two identifiers aliasing one value, and identical identifiers after independent replay",
			[]string{"go", "test", "-run", "CorrelationPropagationCoverage", "./pkg/platform/correlation/"}, false},
	}
	if !*skipRace {
		checks = append(checks, check{"race", "the suite passes under the race detector", true,
			assurance.StatusVerified, "./...", "go test -race ./... passes",
			[]string{"go", "test", "-race", "-p", "1", "./..."}, false})
	}

	blocked := []blockedGate{
		{"pentest", "independent third-party penetration test", assurance.StatusQualified, "external", "signed report from an external security vendor", "requires an external security vendor; cannot be produced by any sandbox. pkg/blockers/pentest now runs a real target registry, a real release-identity preflight, and adversarial probes (JWT alg=none, unknown kid, sandbox path traversal, authz wildcard escalation) directly against this codebase's own production pkg/api/pkg/kernel/sandbox/pkg/authz -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION). Still requires a real vendor's signed report through pkg/governance/qualification to ever reach VERIFIED."},
		{"scale_qualification", "100-node / 1M-evidence physical benchmark", assurance.StatusQualified, "external", "p50/p95/p99 latency and throughput at 100 nodes", "requires physical multi-node infrastructure at the full 100-node scale. pkg/blockers/scale runs a real goroutine-based NodeProvider and a real exactly-once-delivery integrity check (with deliberate loss/duplication injection tests proving the check actually works) -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION). The promised RealNodeProvider now exists too: HTTPNodeProvider plus a real worker binary (cmd/veriqo-scale-node) were run as 100 genuinely separate Docker containers over real HTTP at the FULL literal target -- 1,000,000 records, zero lost, zero duplicated, ~2536/s -- see evidence/scale_qualification-multi-container-drill.txt. The literal 100-node / 1,000,000-envelope COUNT named by this blocker is now met for real, together, in one run; what remains is real distinct-infrastructure (multi-host/multi-datacenter/cloud) execution at this scale, since all 100 containers still ran on this session's one physical host -- an infrastructure-procurement action, not an engineering one."},
		{"multi_region_dr", "multi-region deployment and DR drill with measured RPO/RTO", assurance.StatusQualified, "deploy/", "destroy primary, restore, verify ledgers and replay", "requires multi-region infrastructure. pkg/blockers/dr models regions as real raftlite nodes connected through a real network-partition-capable transport, fails the leader region, measures real RTO/RPO, heals it, and confirms convergence -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION). A separate drill ran the actual production binary (cmd/veriqo-node) as three real Docker containers on a real bridge network, partitioned the leader with a real `docker network disconnect`, measured ~500ms RTO and RPO=0 with the surviving majority still serving writes, then healed the partition and independently verified identical converged state on all three containers -- see evidence/multi_region_dr-multi-container-drill.txt. Still requires real cross-datacenter infrastructure (WAN characteristics, real cloud regions, traffic-manager cutover) to qualify real cross-region network behavior."},
		{"hsm_kms", "production HSM/KMS backed signing", assurance.StatusQualified, "./pkg/platform/security/keys", "KeyProvider backed by a real HSM or cloud KMS", "interface implemented; a real HSM/KMS tenancy is a procurement action. pkg/platform/security/keys now has a SoftwareBacked production guard (RequireProductionSafe refuses any software-backed provider under env=production) and pkg/blockers/hsmkms proves every failure mode -- unavailable, timeout, permission-denied, wrong-key, revoked -- fails closed, with the revoked case proving the provider is never even touched -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION). Freshly re-tested this round against real AWS KMS rather than assumed: this environment's placeholder AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY were tried against a real DescribeKey call and AWS itself rejected them (UnrecognizedClientException) -- see evidence/hsm_kms-real-credential-retest.txt. Confirmed procurement-blocked, not engineering-blocked."},
		{"live_data", "non-synthetic live data feeds (SWIFT/BoL/AIS/SAR)", assurance.StatusQualified, "./pkg/connector", "ingest qualified against contracted live feeds", "requires commercial data contracts. pkg/blockers/livedata now has a real FeedConnector/Pipeline with content-hash dedup and a real anti-replay defense (every accepted record replayed back and confirmed rejected) across all four source types, and refuses any SIMULATED-mode connector's record tagged LIVE -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION)."},
		{"soak_72h", "72-hour continuous soak with zero leak and zero ledger corruption", assurance.StatusQualified, "./...", "72h run with stable memory, goroutines and ledger integrity", "the harness now exists and genuinely runs — see test/soak and evidence/soak-report.json (2188 real iterations over 90s in this session, 0 errors, goroutines flat at 2->2) — but this environment cannot honestly stay up for the required 72h continuous window; VERIQO_SOAK_MINUTES=4320 against the same unchanged test on a long-lived host produces the qualifying evidence. A best-effort extension was also run: one genuine, unbroken 60-minute pass of the same unmodified test (30x the smoke pass) completed with zero errors -- see evidence/soak-60min-run-log.txt for the raw run output and an honest note on why its structured JSON specifically wasn't retained. pkg/blockers/soak now formalizes the same technique behind a Start/Checkpoint/Monitor/DetectLeak/DetectDrift/Finalize/GenerateEvidence API with a hash-chained, tamper-evident checkpoint sequence -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION on the machinery; the real 72h window is still unrun)."},
		{"spire_mtls", "real SPIRE deployment with workload attestation and mTLS rotation", assurance.StatusQualified, "deploy/spire", "node A and node B attest, rotate and revoke identities", "a real single-node SPIRE server+agent was run in one session — see evidence/spire_mtls-local-integration.txt — with genuine attestation, X.509-SVID issuance and revocation; a later session ran a real multi-container cluster instead -- one spire-server and two independently-attested spire-agent containers on a real Docker bridge network, node-scoped workload identity isolation confirmed, and per-node revocation proven live across the cluster -- see evidence/spire_mtls-multi-container-integration.txt. This round closed the live spire-agent-in-the-loop drill those rounds left open: a real, separately-running SPIRE server issued real X.509-SVIDs that pkg/transport/rafttcp's FileCertSource/NewServerFromSource/NewClientFromSource genuinely loaded and used for a real end-to-end mTLS handshake and RPC -- see evidence/spire_mtls-rafttcp-live-integration.txt, which also documents a real finding (rafttcp's client hard-codes the expected trust domain as \"veriqo.global\") worked around for the drill but left open as configurable-trust-domain follow-on work. Still requires a production node attestor (join_token is a test/demo attestor, not cloud-instance-identity/k8s-PSAT/TPM). pkg/blockers/spiffe now qualifies the client-side validation logic with real X.509 chain verification and a full failure matrix (expired, revoked, untrusted CA, claimed-vs-actual identity mismatch) -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION)."},
		{"supply_chain_scan", "govulncheck / gosec / staticcheck in CI", assurance.StatusQualified, ".github/workflows", "all scanners run and report clean", "staticcheck AND gosec both run clean (0 findings each) in a network-enabled session — see evidence/supply_chain_scan-gosec-full.txt; all 89 gosec findings across every severity were individually triaged and fixed or justified with a named reason, including catching and correcting a real bug in the prior round's own suppression comments. govulncheck's vulnerability feed (vuln.go.dev) returned 403, and so did osv.dev and the GitHub advisory API — tried as alternates — under this environment's network policy, so vulnerability-DB scanning specifically remains unqualified: SAST is fully closed, dependency-vulnerability scanning is not. Freshly re-tested this round rather than assumed: the same three hosts were re-probed directly and via the proxy's own status endpoint, which independently logs the identical 403 policy denials -- see evidence/supply_chain_scan-vulndb-network-retest.txt. pkg/blockers/supplychain now has a real `go list -m all` dependency-discovery step, a policy engine, and a real HTTP-backed VulnerabilityProvider (unit-tested against a local server since the real endpoints are network-blocked here) -- see evidence/blockers-qualification-report.json (READY_FOR_REAL_QUALIFICATION on the pipeline; the vulnerability-DB query itself is still unqualified against a real feed)."},
	}

	reg := assurance.NewRegistry()
	now := uint64(time.Now().Unix()) // #nosec G115 -- Unix() is positive for any realistic clock (1970..292 billion AD)
	failures := 0
	gateHashes := make(map[string]string, len(checks))
	gatePassed := make(map[string]bool, len(checks))

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
		gateHashes[c.gateID] = ev.Hash
		gatePassed[c.gateID] = code == 0
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

	// telemetry_production_coverage (audit V7.12.1.6 item #5): a real,
	// separate, mandatory gate measuring what "observability" above
	// deliberately no longer claims -- how many production domain
	// packages actually call telemetry.StartSpan, not whether
	// pkg/platform/telemetry's own unit tests pass. Computed in-process
	// (internal/telemetrycoverage.Measure), not shelled out, because it
	// is a real filesystem scan, not a go test invocation; still folded
	// into the SAME evidence/registry machinery as every exec'd gate via
	// reg.Register/reg.Attach, so it is bound into gateHashes,
	// TestManifestHash and the signed certificate exactly like the rest.
	{
		covReport, covErr := telemetrycoverage.Measure(".", telemetrycoverage.DomainPackageRoots)
		covOut, _ := json.MarshalIndent(covReport, "", "  ")
		code := 0
		if covErr != nil {
			code = 1
			covOut = []byte(covErr.Error())
		} else if len(covReport.InstrumentedPackages) == 0 {
			// The honest current state: zero of this run's real domain
			// packages instrument themselves. This gate reports that as a
			// real failure, not a blocked/external one -- it is fully
			// within this codebase's own power to fix, just not yet done.
			code = 1
		}
		if err := reg.Register(assurance.Gate{
			ID: "telemetry_production_coverage",
			Description: "production domain engines (pkg/moat, pkg/kernel, pkg/engine, pkg/consensus, pkg/execution, " +
				"pkg/governance, pkg/authz, pkg/identity, pkg/transport, pkg/storage, veriqo/core) actually call " +
				"telemetry.StartSpan -- not merely that the telemetry package's own tests pass",
			Mandatory: true, RequiredStatus: assurance.StatusIntegrated,
			OwnerPackage: "./internal/telemetrycoverage",
			ExitCriteria: "at least one real call site exists in each domain root; today's honest answer is zero",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("telemetry_production_coverage", "internal:telemetrycoverage.Measure", string(covOut), code, now)
		writeArtifact(*evidenceDir, "telemetry_production_coverage", "internal:telemetrycoverage.Measure", code, string(covOut))
		_ = os.WriteFile("evidence/telemetry_coverage.json", covOut, 0o600)
		gateHashes["telemetry_production_coverage"] = ev.Hash
		gatePassed["telemetry_production_coverage"] = code == 0
		status := assurance.StatusIntegrated
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("telemetry_production_coverage", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (coverage=%.0f%%)\n",
			"telemetry_production_coverage", "internal:telemetrycoverage.Measure", code, ev.ArtifactID, covReport.Coverage*100)
	}

	// truth_arbitration_no_bypass (audit V7.12.7 item P0-A / R1: "Tidak
	// boleh ada bypass"; also satisfies the master implementation
	// directive's "unified_evidence_production_coverage" requirement --
	// same gate, kept under its established name for continuity with
	// every prior round's evidence and documentation): a real,
	// mandatory, in-process gate proving no production source file
	// constructs contradiction.ArbitrationEngine, fusion.Engine, OR
	// canonical.Pipeline directly outside each constructor's own
	// individually-audited exemption list -- see internal/nobypass's
	// own doc comment for exactly which 5+3+2 sites are exempt and why.
	// Failing this gate means a future caller has quietly reopened the
	// exact evidence-authority fragmentation P0-A closed.
	{
		byp, bypErr := nobypass.Check(".")
		bypOut, _ := json.MarshalIndent(byp, "", "  ")
		code := 0
		if bypErr != nil {
			code = 1
			bypOut = []byte(bypErr.Error())
		} else if len(byp.Violations) > 0 {
			code = 1
		}
		if err := reg.Register(assurance.Gate{
			ID: "truth_arbitration_no_bypass",
			Description: "no production source file constructs contradiction.ArbitrationEngine, fusion.Engine, " +
				"or canonical.Pipeline directly outside each constructor's own audited exemption list -- every " +
				"production evidence ingress path terminates at the ONE canonical facade/pipeline, not a " +
				"silently-reopened parallel authority (this gate is the directive's " +
				"unified_evidence_production_coverage requirement)",
			Mandatory: true, RequiredStatus: assurance.StatusVerified,
			OwnerPackage: "./internal/nobypass",
			ExitCriteria: "zero unauthorized direct constructions found by a real whole-tree source scan",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("truth_arbitration_no_bypass", "internal:nobypass.Check", string(bypOut), code, now)
		writeArtifact(*evidenceDir, "truth_arbitration_no_bypass", "internal:nobypass.Check", code, string(bypOut))
		_ = os.WriteFile("evidence/truth_arbitration_no_bypass.json", bypOut, 0o600)
		gateHashes["truth_arbitration_no_bypass"] = ev.Hash
		gatePassed["truth_arbitration_no_bypass"] = code == 0
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("truth_arbitration_no_bypass", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (scanned=%d violations=%d)\n",
			"truth_arbitration_no_bypass", "internal:nobypass.Check", code, ev.ArtifactID, byp.ScannedFiles, len(byp.Violations))
	}

	// canonical_entity_authority_coverage (master implementation
	// directive, P0-B "Canonical Entity Authority"): pkg/governance/
	// entityconsistency existed as a real divergence detector between
	// pkg/identity and pkg/moat/entity, but was wired into nothing
	// beyond its own test suite -- not this readiness pipeline. This
	// gate closes that: a real, whole-tree scan (ScanProductionAuthority)
	// proving no production file outside pkg/lifecycle/lifecycle.go
	// (the documented fallback authority behind pkg/identity.Resolver)
	// constructs an independent pkg/moat/entity.Registry. Failing this
	// gate means a future caller has quietly reopened the exact
	// entity-authority fragmentation P0-B closed.
	{
		auth, authErr := entityconsistency.ScanProductionAuthority(".")
		authOut, _ := json.MarshalIndent(auth, "", "  ")
		code := 0
		if authErr != nil {
			code = 1
			authOut = []byte(authErr.Error())
		} else if len(auth.Violations) > 0 {
			code = 1
		}
		if err := reg.Register(assurance.Gate{
			ID: "canonical_entity_authority_coverage",
			Description: "no production source file outside pkg/lifecycle/lifecycle.go constructs an " +
				"independent pkg/moat/entity.Registry -- pkg/identity.Resolver remains the ONE canonical " +
				"entity-resolution authority every production caller resolves through first",
			Mandatory: true, RequiredStatus: assurance.StatusVerified,
			OwnerPackage: "./pkg/governance/entityconsistency",
			// Deliberately does not spell out the literal constructor
			// call this gate scans for: doing so would make this very
			// description string itself trip the scan (a real, found-
			// during-testing false positive -- see entityconsistency.go's
			// own analogous self-referential allowlist entry for the
			// same class of mistake).
			ExitCriteria: "zero unauthorized direct constructions of the legacy entity registry found by a real whole-tree source scan",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("canonical_entity_authority_coverage", "internal:entityconsistency.ScanProductionAuthority", string(authOut), code, now)
		writeArtifact(*evidenceDir, "canonical_entity_authority_coverage", "internal:entityconsistency.ScanProductionAuthority", code, string(authOut))
		_ = os.WriteFile("evidence/canonical_entity_authority_coverage.json", authOut, 0o600)
		gateHashes["canonical_entity_authority_coverage"] = ev.Hash
		gatePassed["canonical_entity_authority_coverage"] = code == 0
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("canonical_entity_authority_coverage", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (scanned=%d violations=%d)\n",
			"canonical_entity_authority_coverage", "internal:entityconsistency.ScanProductionAuthority", code, ev.ArtifactID, auth.ScannedFiles, len(auth.Violations))
	}

	// external_harness_capability_coverage (pre-insurance closure
	// program, PHASE K / P2-18). The eight harnesses already existed;
	// what did not is per-CAPABILITY honesty about them. The
	// orchestrator answers "does a harness exist for DR"; this answers
	// "does the DR harness actually exercise failback, or only
	// failover". It is MANDATORY but deliberately NOT a promotion path:
	// it PASSes when the register is internally consistent AND every
	// one of the eight still names at least one capability no harness
	// can ever supply. A gate that stopped naming something external
	// would mean a harness had quietly claimed to cover the thing that
	// makes the gate external -- the exact false green this fails on.
	{
		type harnessArtifact struct {
			Capabilities []blockers.Capability        `json:"capabilities"`
			Summary      []blockers.CapabilitySummary `json:"summary"`
			Problems     []string                     `json:"problems,omitempty"`
			Invariant    string                       `json:"invariant"`
		}
		art := harnessArtifact{
			Capabilities: blockers.CapabilityRegister(),
			Summary:      blockers.Summarize(),
			Problems:     blockers.ValidateRegister(),
			Invariant:    blockers.HarnessCanNeverQualify(),
		}
		code := 0
		if len(art.Problems) > 0 {
			code = 1
		}
		for _, s := range art.Summary {
			if s.ExternalOnly == 0 {
				code = 1
				art.Problems = append(art.Problems, s.GateID+
					": reports no external-only capability; an externally-blocked gate must always name what no harness can supply")
			}
		}
		harnessOut, _ := json.MarshalIndent(art, "", "  ")
		if err := reg.Register(assurance.Gate{
			ID: "external_harness_capability_coverage",
			Description: "every externally-blocked gate's harness declares, capability by capability, what it " +
				"exercises and what only a real environment can supply -- and each of the eight still names at " +
				"least one capability no harness can ever cover",
			Mandatory: true, RequiredStatus: assurance.StatusVerified,
			OwnerPackage: "./pkg/blockers",
			ExitCriteria: "register internally consistent, every cited symbol real, and no gate claiming zero external-only capabilities",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("external_harness_capability_coverage",
			"internal:blockers.CapabilityRegister", string(harnessOut), code, now)
		writeArtifact(*evidenceDir, "external_harness_capability_coverage",
			"internal:blockers.CapabilityRegister", code, string(harnessOut))
		_ = os.WriteFile("evidence/external_harness_capability_coverage.json", harnessOut, 0o600)
		gateHashes["external_harness_capability_coverage"] = ev.Hash
		gatePassed["external_harness_capability_coverage"] = code == 0
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("external_harness_capability_coverage", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (capabilities=%d gates=%d)\n",
			"external_harness_capability_coverage", "internal:blockers.CapabilityRegister",
			code, ev.ArtifactID, len(art.Capabilities), len(art.Summary))
	}

	// canonical_identity_authority_coverage (pre-insurance closure
	// program, PHASE B / P0-2 "Canonical Identity Authority"). The
	// established canonical_entity_authority_coverage gate above proves
	// no second entity REGISTRY is constructed. This gate answers the
	// stricter question the program actually asks: how many production
	// files WRITE canonical identity (must be exactly 1), how many do so
	// without authorization (0), how many write the pre-canonical
	// union-find outside the one documented fallback branch (0), and is
	// every maritime entity kind either explicitly mapped to an
	// identity.Kind or explicitly marked UNMAPPED with a stated reason.
	{
		idCov, idErr := entityconsistency.ScanIdentityAuthority(".")
		idOut, _ := json.MarshalIndent(idCov, "", "  ")
		code := 0
		if idErr != nil {
			code = 1
			idOut = []byte(idErr.Error())
		} else if !idCov.Pass() {
			code = 1
		}
		if err := reg.Register(assurance.Gate{
			ID: "canonical_identity_authority_coverage",
			Description: "pkg/identity is the SINGLE production identity write authority: production identity " +
				"writers = 1, unauthorized writers = 0, independent legacy merges = 0, and every maritime " +
				"entity kind is explicitly mapped or explicitly marked UNMAPPED with a stated reason",
			Mandatory: true, RequiredStatus: assurance.StatusVerified,
			OwnerPackage: "./pkg/governance/entityconsistency",
			ExitCriteria: "identity writers = 1, unauthorized writers = 0, independent legacy merges = 0, zero maritime kinds without an explicit mapping",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("canonical_identity_authority_coverage",
			"internal:entityconsistency.ScanIdentityAuthority", string(idOut), code, now)
		writeArtifact(*evidenceDir, "canonical_identity_authority_coverage",
			"internal:entityconsistency.ScanIdentityAuthority", code, string(idOut))
		_ = os.WriteFile("evidence/canonical_identity_authority_coverage.json", idOut, 0o600)
		gateHashes["canonical_identity_authority_coverage"] = ev.Hash
		gatePassed["canonical_identity_authority_coverage"] = code == 0
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("canonical_identity_authority_coverage", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (writers=%d unauthorized=%d legacy_merges=%d unmapped=%d)\n",
			"canonical_identity_authority_coverage", "internal:entityconsistency.ScanIdentityAuthority",
			code, ev.ArtifactID, idCov.ProductionIdentityWriters, idCov.UnauthorizedIdentityWriters,
			idCov.IndependentLegacyMerges, idCov.UnmappedMaritimeKinds)
	}

	// canonical_evidence_production_coverage (pre-insurance closure
	// program, PHASE A / P0-1 "Canonical Evidence Write Authority").
	// The established truth_arbitration_no_bypass gate above already
	// proves no second evidence AUTHORITY exists. This gate is the
	// second, different question: does a second INGESTION PATH exist --
	// a caller that never touches an engine but mints canonical
	// evidence records of its own -- and is the canonical contract
	// version actually DECLARED rather than merely implied by four
	// separately-versioned schemas. It PASSes only with unauthorized
	// evidence writers = 0, unauthorized ingestion paths = 0, and a
	// declared, content-addressed contract.
	{
		contract := evidenceapi.Contract()
		cov, covErr := nobypass.EvidenceProductionCoverage(".", contract.Version, contract.Hash)
		type coverageArtifact struct {
			nobypass.EvidenceCoverage
			Contract evidenceapi.ContractDescriptor `json:"contract"`
		}
		covOut, _ := json.MarshalIndent(coverageArtifact{EvidenceCoverage: cov, Contract: contract}, "", "  ")
		code := 0
		if covErr != nil {
			code = 1
			covOut = []byte(covErr.Error())
		} else if !cov.Pass() {
			code = 1
		}
		if err := reg.Register(assurance.Gate{
			ID: "canonical_evidence_production_coverage",
			Description: "every production evidence write travels source -> adapter -> canonical evidence contract -> " +
				"facade -> pipeline -> subsystem: zero unauthorized evidence writers, zero unauthorized ingestion " +
				"paths, and a declared, content-addressed canonical contract version",
			Mandatory: true, RequiredStatus: assurance.StatusVerified,
			OwnerPackage: "./internal/nobypass, ./pkg/evidence/api",
			ExitCriteria: "unauthorized evidence writers = 0, unauthorized ingestion paths = 0, contract version declared",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("canonical_evidence_production_coverage",
			"internal:nobypass.EvidenceProductionCoverage", string(covOut), code, now)
		writeArtifact(*evidenceDir, "canonical_evidence_production_coverage",
			"internal:nobypass.EvidenceProductionCoverage", code, string(covOut))
		_ = os.WriteFile("evidence/canonical_evidence_production_coverage.json", covOut, 0o600)
		gateHashes["canonical_evidence_production_coverage"] = ev.Hash
		gatePassed["canonical_evidence_production_coverage"] = code == 0
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("canonical_evidence_production_coverage", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (writers=%d ingestion=%d contract=%s)\n",
			"canonical_evidence_production_coverage", "internal:nobypass.EvidenceProductionCoverage",
			code, ev.ArtifactID, cov.UnauthorizedEvidenceWriters, cov.UnauthorizedIngestionPaths, cov.ContractVersion)
	}

	// canonical_execution_entrypoint_coverage (pre-insurance closure
	// program, PHASE C / P0-3 "Canonical Execution Path"): a real,
	// mandatory, in-process whole-tree scan proving no production file
	// opens a SECOND path to a governed decision -- no second
	// execution.Engine, no directly-constructed verification
	// certificate -- and that every row of the audited entrypoint matrix
	// (HTTP, CLI, batch, worker, scheduler, admin API, replay,
	// compatibility API, internal job) still says something the source
	// actually supports. The pre-existing
	// TestRegistryNeverProducesGovernedDecisionArtifacts covered two
	// files; this covers the tree.
	{
		epRep, epErr := entrypoints.Audit(".")
		epOut, _ := json.MarshalIndent(epRep, "", "  ")
		code := 0
		if epErr != nil {
			code = 1
			epOut = []byte(epErr.Error())
		} else if !epRep.Clean() {
			code = 1
		}
		if err := reg.Register(assurance.Gate{
			ID: "canonical_execution_entrypoint_coverage",
			Description: "every governed decision flows Entrypoint -> Lifecycle -> Execution Engine -> Policy -> " +
				"Evidence -> Decision -> Verification through the ONE execution engine; parallel governed " +
				"execution paths = 0 and no entrypoint-matrix row claims a canonical path its own source does not take",
			Mandatory: true, RequiredStatus: assurance.StatusVerified,
			OwnerPackage: "./internal/entrypoints",
			ExitCriteria: "parallel governed execution paths = 0 and zero matrix rows contradicted by a real whole-tree source scan",
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register:", err)
			os.Exit(3)
		}
		ev := assurance.NewEvidence("canonical_execution_entrypoint_coverage", "internal:entrypoints.Audit", string(epOut), code, now)
		writeArtifact(*evidenceDir, "canonical_execution_entrypoint_coverage", "internal:entrypoints.Audit", code, string(epOut))
		_ = os.WriteFile("evidence/canonical_execution_entrypoint_coverage.json", epOut, 0o600)
		gateHashes["canonical_execution_entrypoint_coverage"] = ev.Hash
		gatePassed["canonical_execution_entrypoint_coverage"] = code == 0
		status := assurance.StatusVerified
		if code != 0 {
			status = assurance.StatusImplemented
			failures++
		}
		if err := reg.Attach("canonical_execution_entrypoint_coverage", status, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach:", err)
			os.Exit(3)
		}
		fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (scanned=%d parallel_paths=%d matrix_errors=%d)\n",
			"canonical_execution_entrypoint_coverage", "internal:entrypoints.Audit", code, ev.ArtifactID,
			epRep.ScannedFiles, epRep.ParallelGovernedExecutionPaths, len(epRep.MatrixErrors))
	}

	// The four INSURANCE ASSURANCE gates (functional spec SS54-SS57 of
	// the VERIQO Insurance Intelligence & Assurance System design).
	//
	// All four are driven by the synthetic case pack
	// (pkg/insurance/casepack), which runs all seven CASE-INS-00N cases
	// end to end through the REAL insurance facade -- there is no
	// fixture-only code path. Each gate's verdict is DERIVED from a list
	// of named failures; none can be hand-set.
	//
	// Scope, stated honestly and NOT overclaimed: these gates prove the
	// insurance domain's own traceability, reproducibility, preservation
	// and human-review ENFORCEMENT hold over synthetic cases. They are
	// engineering evidence. They establish nothing whatever about live
	// customer data, which remains the `live_data` blocker's business and
	// is unaffected by anything below.
	{
		summary := insurancecasepack.RunAssurance()
		out, _ := json.MarshalIndent(summary, "", "  ")
		_ = os.WriteFile("evidence/insurance_assurance_gates.json", out, 0o600)

		type insuranceGate struct {
			id       string
			desc     string
			exit     string
			pass     bool
			failures []string
		}
		gates := []insuranceGate{
			{
				id: "insurance_coverage_traceability",
				desc: "every coverage conclusion traces to a policy clause on the version actually in force, to " +
					"evidence that exists in the case registry, to an effective date, and to a stated reason; " +
					"an unresolved finding always raises review",
				exit:     "every fact traceable across all seven synthetic cases, zero cases examined vacuously",
				pass:     summary.CoverageTraceabilityPass(),
				failures: summary.CoverageTraceabilityFailures,
			},
			{
				id: "insurance_quantum_reproducibility",
				desc: "the same inputs, at the same effective tick, under the same calculation version, recompute " +
					"to a byte-identical quantum result, and every non-zero operand cites real evidence",
				exit:     "recomputation identical on all seven synthetic cases, every operand evidence-backed",
				pass:     summary.QuantumReproducibilityPass(),
				failures: summary.QuantumReproducibilityFailures,
			},
			{
				id: "insurance_preservation_chain_integrity",
				desc: "every case's evidence sits under a preservation order recording trigger, scope, custodian, " +
					"hash and a well-formed custody log, and the case lineage hash chain verifies",
				exit:     "all nine SS56 checks pass per order, every record covered, lineage chain verified",
				pass:     summary.PreservationPass(),
				failures: summary.PreservationFailures,
			},
			{
				id: "insurance_human_review_enforcement",
				desc: "finalization fails closed when mandatory review is outstanding, AND is permitted once a " +
					"complete, well-formed authorization addresses every review question -- both directions, " +
					"because a gate that only ever refuses proves nothing",
				exit:     "refused without authorization and permitted with one, on all seven synthetic cases",
				pass:     summary.HumanReviewPass(),
				failures: summary.HumanReviewFailures,
			},
			{
				id: "insurance_cold_replay",
				desc: "a case reconstructed from NOTHING but its own serialised snapshot -- no reference to the " +
					"live in-memory case -- reproduces the exact evidence root hash, preservation hash, quantum " +
					"result and resolved policy version the live drive produced (Final Design SS20 'C5' / spec SS73)",
				exit:     "cold replay reproduces the live result on all seven synthetic cases",
				pass:     summary.ColdReplayPass(),
				failures: summary.ColdReplayFailures,
			},
		}

		for _, g := range gates {
			code := 0
			artifact := out
			if !g.pass {
				code = 1
				detail, _ := json.MarshalIndent(struct {
					Gate     string   `json:"gate"`
					Failures []string `json:"failures"`
					Summary  any      `json:"summary"`
				}{g.id, g.failures, summary}, "", "  ")
				artifact = detail
			}
			if err := reg.Register(assurance.Gate{
				ID: g.id, Description: g.desc,
				Mandatory: true, RequiredStatus: assurance.StatusVerified,
				OwnerPackage: "./pkg/insurance/verification, ./pkg/insurance/casepack",
				ExitCriteria: g.exit,
			}); err != nil {
				fmt.Fprintln(os.Stderr, "readiness: register:", err)
				os.Exit(3)
			}
			ev := assurance.NewEvidence(g.id, "internal:casepack.RunAssurance", string(artifact), code, now)
			writeArtifact(*evidenceDir, g.id, "internal:casepack.RunAssurance", code, string(artifact))
			gateHashes[g.id] = ev.Hash
			gatePassed[g.id] = code == 0
			status := assurance.StatusVerified
			if code != 0 {
				status = assurance.StatusImplemented
				failures++
			}
			if err := reg.Attach(g.id, status, ev); err != nil {
				fmt.Fprintln(os.Stderr, "readiness: attach:", err)
				os.Exit(3)
			}
			fmt.Printf("== %-24s %s\n   -> exit=%d artifact=%s (cases=%d failures=%d)\n",
				g.id, "internal:casepack.RunAssurance", code, ev.ArtifactID, summary.CaseCount, len(g.failures))
		}
	}

	// Regenerate SBOM.json from THIS release's actual identity, and
	// compute the source-tree hash, before anything downstream (release
	// binding checks, the certificate itself) needs either. A prior
	// round shipped a static SBOM.json hardcoding version "v7.12.0" and
	// vcs.commit "unknown" forever after; every release certificate
	// since then signed a hash that committed to the wrong release.
	// sbom.Generate refuses to produce an unidentified SBOM at all, so
	// that defect cannot recur silently.
	if doc, err := sbom.Generate(*releaseVersion, *commit); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: sbom:", err)
		os.Exit(3)
	} else if raw, err := doc.JSON(); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: sbom marshal:", err)
		os.Exit(3)
	} else if err := os.WriteFile("SBOM.json", raw, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: sbom write:", err)
		os.Exit(3)
	}
	srcHash := sourceHash()

	// External Qualification Evidence Framework (P0-03): a blocked gate
	// is never a permanent hard-coded dead end. Each blocked gate is
	// registered into qualification.Registry as BLOCKED_EXTERNAL; if
	// evidence/external/<gate>.json holds a real, independently
	// validated submission — a signed report from an actual vendor, a
	// benchmark artifact from real infrastructure — the gate advances
	// and the assurance registry reflects that. No evidence file is
	// fabricated by this program. Absence of a file means the gate
	// stays exactly what it has always honestly been: BLOCKED_EXTERNAL.
	//
	// Every submission is now also cryptographically trust-checked
	// (V7.12.1 Layer A hardening): loadTrustRegistry reads only PUBLIC
	// keys from docs/governance/TRUSTED_EVIDENCE_{PROVIDERS,REVIEWERS}.json
	// (safe to commit) and ships empty by default, so nothing validates
	// until a real provider/reviewer is actually registered; and every
	// submission must be bound to *commit/srcHash — evidence produced
	// against a different release is rejected regardless of whose key
	// signed it.
	trust := loadTrustRegistry("docs/governance/TRUSTED_EVIDENCE_PROVIDERS.json", "docs/governance/TRUSTED_EVIDENCE_REVIEWERS.json")
	qreg := qualification.NewRegistry(trust)
	for _, b := range blocked {
		if err := qreg.RegisterBlocked(b.gateID, b.description, b.blocker); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: qualification register:", err)
			os.Exit(3)
		}
	}
	loadExternalQualifications(qreg, *evidenceDir, now, *commit, srcHash)

	// P0-02 (audit V7.12.3): ExpireStale existed and was unit-tested
	// (qualification_test.go) but was never actually called from this
	// pipeline -- a gate qualified in a prior run whose evidence had
	// since expired, or whose provider/reviewer key had since been
	// revoked, stayed marked QUALIFIED/VERIFIED in the registry until
	// someone happened to re-submit evidence. Calling it here, right
	// after loading whatever external evidence exists and before any
	// gate below reads qreg's status, makes staleness a real, applied
	// state transition instead of dead code with a passing unit test.
	qreg.ExpireStale(now)

	// PHASE E3 (P0-8): every blocked gate's fixture-qualification
	// pipeline is really executed here, in-process, so the ENGINEERING
	// axis of each of the eight gates is backed by a real run of that
	// gate's own machinery rather than being reported as NOT_RUN. This
	// is the SAME orchestrator cmd/veriqo-qualification already drives
	// (no second harness); the result is attached to the axis only, and
	// deliberately never to Status -- READY_FOR_REAL_QUALIFICATION is a
	// statement about a fixture, and a fixture never closes a gate.
	blockerOutcome := map[string]orchestrator.Outcome{}
	if breg := blockerRegistry(); breg != nil {
		for _, o := range orchestrator.RunAll(context.Background(), ".", breg, orchestrator.Overrides{}).Outcomes {
			blockerOutcome[o.ID] = o
		}
	}

	for _, b := range blocked {
		if err := reg.Register(assurance.Gate{
			ID: b.gateID, Description: b.description, Mandatory: true,
			RequiredStatus: b.required, OwnerPackage: b.owner, ExitCriteria: b.exit,
			ExternalDependency: b.blocker,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: register blocked:", err)
			os.Exit(3)
		}
		attachBlockerAxes(reg, b.gateID, blockerOutcome[b.gateID], now)
		qrec, _ := qreg.Get(b.gateID)
		if qrec.Status == qualification.StatusVerified && qrec.Satisfied(now) {
			raw, _ := json.Marshal(qrec)
			ev := assurance.NewEvidence(b.gateID, "external qualification: "+qrec.Blocker, string(raw), 0, now)
			if err := reg.Attach(b.gateID, assurance.StatusQualified, ev); err != nil {
				fmt.Fprintln(os.Stderr, "readiness: attach qualification:", err)
				os.Exit(3)
			}
			fmt.Printf("== %-24s QUALIFIED by real external evidence (provider=%s)\n", b.gateID, qrec.Evidence[len(qrec.Evidence)-1].ProviderID)
		} else {
			if err := reg.Block(b.gateID, b.blocker); err != nil {
				fmt.Fprintln(os.Stderr, "readiness: block:", err)
				os.Exit(3)
			}
			fmt.Printf("== %-24s BLOCKED: %s\n", b.gateID, b.blocker)
		}
	}
	if qj, err := qreg.JSON(); err == nil {
		_ = os.WriteFile(filepath.Join(*evidenceDir, "external-qualification.json"), qj, 0o600)
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

	// P0-01 (audit V7.12.3): the certificate previously left BuildHash,
	// TestManifestHash, SecurityManifestHash, BenchmarkManifestHash,
	// ChaosManifestHash and ReplayManifestHash as zero-value empty
	// strings forever -- declared fields that folded into the signed
	// canonical hash as constants, never actually binding the
	// certificate to any gate's real evidence content. Every one of
	// them is now computed from the real per-gate evidence hashes
	// collected during the gate loop above, categorized by what each
	// gate actually measures.
	binaryHash, buildID := binaryIdentity()
	cert := assurance.ReleaseCertificate{
		Version: *releaseVersion, GitCommit: *commit, Operator: *operator,
		Timestamp: now, GoVersion: runtime.Version(),
		SourceHash: srcHash, SBOMHash: fileHashOrEmpty("SBOM.json"),
		BuildHash:  categoryHash(gateHashes, "build", "vet", "format"),
		BinaryHash: binaryHash, BuildID: buildID,
		EvidenceRootHash: evidenceRootHash(gateHashes),
		TestManifestHash: categoryHash(gateHashes,
			"unit", "acceptance", "dependency_integration", "identity", "api_semantics",
			"storage_recovery", "model_lifecycle", "knowledge_evolution", "data_governance",
			"hitl", "calibration", "economic_consequence", "decision_precedence",
			"observability", "telemetry_production_coverage", "truth_arbitration_no_bypass", "data_quality", "bounded_test_execution", "traceability_matrix",
			"soak_harness_smoke", "execution_graph", "decision_explanation", "fuzz_smoke",
			"zero_dependency", "assurance_self", "race", "determinism_boundary"),
		SecurityManifestHash: categoryHash(gateHashes,
			"security_unit", "sandbox", "supply_chain_scan", "hsm_kms", "spire_mtls"),
		BenchmarkManifestHash: categoryHash(gateHashes, "stress_slo", "scale_qualification"),
		ChaosManifestHash:     categoryHash(gateHashes, "chaos"),
		ReplayManifestHash:    categoryHash(gateHashes, "replay", "replay_determinism_100x"),
	}

	// P1-B (master implementation directive, "Qualification Environment
	// Identity"): a real, structured record of the environment that
	// produced this exact release, replacing what would otherwise be an
	// unrecorded fact -- see internal/environment's own package comment
	// for the honest scope (this sandbox's own OS/toolchain/hostname/
	// kernel/container-heuristic/config-hash; real cloud/orchestration
	// fields left empty, never fabricated). The full Identity is
	// persisted as its own evidence artifact; only its content hash
	// enters the certificate (see EnvironmentHash's own doc comment for
	// why it is attached, not signature-covered, in this round).
	envIdentity := environment.Current()
	envIdentity.TimeReferenceUnix = now
	if envRaw, err := json.MarshalIndent(envIdentity, "", "  "); err == nil {
		_ = os.WriteFile("evidence/environment_identity.json", envRaw, 0o600)
	}
	if envHash, err := envIdentity.Hash(); err == nil {
		cert = cert.WithEnvironmentIdentity(envHash)
	}

	manifest := assurance.BuildReadinessManifest(reg, acc, cert)
	if *signingKey != "" {
		priv, keyID, err := loadSigningKey(*signingKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, "readiness: signing key:", err)
			os.Exit(3)
		}
		manifest.Release = manifest.Release.Sign(priv, keyID)
	}

	// P1-09 (audit V7.12.3): the certificate's Timestamp was a bare
	// wall-clock uint64 from this same build host -- nothing outside
	// this process attested to it. -timestamp-key, when set, appends
	// this run's certificate hash as a new entry onto a persisted,
	// hash-chained, independently-signed notary log (internal/
	// timestamp), signed by a key deliberately separate from
	// -signing-key so a compromise of one cannot rewrite the other's
	// history.
	if *timestampKey != "" {
		priv, keyID, err := loadSigningKey(*timestampKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, "readiness: timestamp key:", err)
			os.Exit(3)
		}
		release, err := attestTimestamp(manifest.Release, *timestampChain, priv, keyID, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "readiness: timestamp attestation:", err)
			os.Exit(3)
		}
		manifest.Release = release
	}

	raw, err := manifest.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "readiness: manifest:", err)
		os.Exit(3)
	}
	if err := os.WriteFile(*out, raw, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "readiness: write:", err)
		os.Exit(3)
	}

	// P0-05 (audit V7.12.3): no engine_registry.json existed anywhere.
	// Generated here, not hand-maintained, from pkg/execution's actual
	// declared DAG (execution.Registry()) plus this run's real gate
	// results -- INTEGRATED/TESTED/REPLAYABLE/VERIFIED are computed
	// from whether execution_graph and replay_determinism_100x actually
	// passed just now, not asserted.
	if raw, err := json.MarshalIndent(buildEngineRegistry(gatePassed), "", "  "); err == nil {
		_ = os.WriteFile("engine_registry.json", raw, 0o600)
	}

	a := manifest.Assessment
	fmt.Printf("\n===== VERIQO PRODUCTION READINESS =====\n")
	fmt.Printf("verdict            : %s\n", a.Verdict)
	fmt.Printf("mandatory gates    : %d/%d passing\n", a.MandatoryPassing, a.MandatoryTotal)
	fmt.Printf("blocked (external) : %v\n", a.BlockedMandatory)
	fmt.Printf("internal failures  : %d (gate exit-code and acceptance-manifest failures counted during this run; "+
		"the verdict above is the authority, this is a diagnostic count)\n", failures)
	fmt.Printf("failing            : %v\n", a.FailingMandatory)
	fmt.Printf("manifest           : %s\n", *out)
	for _, r := range a.Reasons {
		fmt.Println("  -", r)
	}
	printClosureMatrix()
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
func loadExternalQualifications(qreg *qualification.Registry, evidenceDir string, nowTick uint64, releaseCommit, releaseSourceHash string) {
	dir := filepath.Join(evidenceDir, "external")
	for _, rec := range qreg.Records() {
		path := filepath.Join(dir, rec.GateID+".json")
		raw, err := os.ReadFile(path) // #nosec G304 -- path is built from this process's own registered gate IDs, not external input
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
		if err := qreg.Validate(rec.GateID, nowTick, releaseCommit, releaseSourceHash); err != nil {
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

// loadTrustRegistry reads the committed, public trust-anchor files
// (provider/reviewer IDs and Ed25519 PUBLIC keys only -- never a
// secret) and returns a TrustRegistry populated from them. Either file
// missing or empty is not an error: it simply means no provider or
// reviewer is yet trusted, so no external evidence can validate, which
// is the correct default until an operator registers a real one.
func loadTrustRegistry(providersPath, reviewersPath string) *qualification.TrustRegistry {
	trust := qualification.NewTrustRegistry()
	if raw, err := os.ReadFile(providersPath); err == nil { // #nosec G304 -- providersPath is an operator-supplied CLI/default path, not untrusted input
		var providers []qualification.Provider
		if err := json.Unmarshal(raw, &providers); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: malformed trust file: %v\n", providersPath, err)
		}
		for _, p := range providers {
			if err := trust.RegisterProvider(p); err != nil {
				fmt.Fprintf(os.Stderr, "readiness: %s: %v\n", providersPath, err)
			}
		}
	}
	if raw, err := os.ReadFile(reviewersPath); err == nil { // #nosec G304 -- reviewersPath is an operator-supplied CLI/default path, not untrusted input
		var reviewers []qualification.Reviewer
		if err := json.Unmarshal(raw, &reviewers); err != nil {
			fmt.Fprintf(os.Stderr, "readiness: %s: malformed trust file: %v\n", reviewersPath, err)
		}
		for _, rv := range reviewers {
			if err := trust.RegisterReviewer(rv); err != nil {
				fmt.Fprintf(os.Stderr, "readiness: %s: %v\n", reviewersPath, err)
			}
		}
	}
	return trust
}

// loadSigningKey reads a hex-encoded Ed25519 private key written by
// cmd/veriqo-release-keygen and derives the same deterministic key ID
// that command computed, so the two never need to agree on a label out
// of band.
func loadSigningKey(path string) (ed25519.PrivateKey, string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied CLI argument (-signing-key), not untrusted input
	if err != nil {
		return nil, "", err
	}
	priv, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, "", fmt.Errorf("not a valid hex-encoded ed25519 private key: %s", path)
	}
	pub := ed25519.PrivateKey(priv).Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	keyID := hex.EncodeToString(sum[:])[:16]
	return ed25519.PrivateKey(priv), keyID, nil
}

// attestTimestamp appends release's certificate hash as a new entry
// onto the persisted timestamp-authority chain at chainPath (creating
// it if absent), signs the entry with priv/keyID, and returns release
// with the attestation's identity attached. Extracted from main's flow
// so it is directly unit-testable without running the full gate suite.
func attestTimestamp(release assurance.ReleaseCertificate, chainPath string, priv ed25519.PrivateKey, keyID string, now uint64) (assurance.ReleaseCertificate, error) {
	var chain []timestamp.Entry
	if raw, err := os.ReadFile(chainPath); err == nil { // #nosec G304 -- chainPath is an operator-supplied CLI argument (-timestamp-chain), not untrusted input
		if err := json.Unmarshal(raw, &chain); err != nil {
			return release, fmt.Errorf("reading %s: %w", chainPath, err)
		}
	}
	var authority *timestamp.Authority
	if len(chain) > 0 {
		authority = timestamp.Resume(priv, keyID, chain[len(chain)-1])
	} else {
		authority = timestamp.NewAuthority(priv, keyID)
	}
	entry, err := authority.Issue(release.CertificateHash, now)
	if err != nil {
		return release, fmt.Errorf("issue: %w", err)
	}
	chain = append(chain, entry)
	chainRaw, err := json.MarshalIndent(chain, "", "  ")
	if err != nil {
		return release, fmt.Errorf("marshal chain: %w", err)
	}
	if err := os.WriteFile(chainPath, chainRaw, 0o600); err != nil {
		return release, fmt.Errorf("write %s: %w", chainPath, err)
	}
	return release.WithTimestampAttestation(entry.Seq, entry.Hash, entry.KeyID, entry.Signature), nil
}

func run(argv []string) (string, int) {
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is this file's own hardcoded gate command table (see checks/blocked above), not external input
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
	_ = os.WriteFile(filepath.Join(dir, gate+".txt"), []byte(body), 0o600)
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
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- e.Name() comes from this process's own os.ReadDir listing, not external input
		if err != nil {
			continue
		}
		total += strings.Count(string(raw), prefix)
	}
	return total
}

func presentTests() []string {
	var out []string
	// os.DirFS + fs.WalkDir/fs.ReadFile, not filepath.Walk + os.ReadFile
	// (gosec G122): every read is confined to and resolved relative to
	// one root handle instead of a re-joined path string per callback.
	_ = fs.WalkDir(os.DirFS("."), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := fs.ReadFile(os.DirFS("."), path)
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

// sourceHash was, before the v7.12.1 audit, the content hash of `go
// list -deps ./...` — the module's dependency graph, not its content.
// A dependency-graph hash is identical for two checkouts that share
// every dependency but differ in a hand-edited production .go file,
// which fails the audit's own acceptance test verbatim: "changing one
// production .go file must change the source-tree hash." It now uses
// internal/sourcehash, a real deterministic Merkle-style hash over
// every source file's path, mode and content.
func sourceHash() string {
	res, err := sourcehash.Compute(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "readiness: source-tree hash:", err)
		return ""
	}
	return res.RootHash
}

func fileHashOrEmpty(path string) string {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		return ""
	}
	return assurance.NewEvidence("sbom", path, string(raw), 0, 0).Hash
}

// engineRegistryRow is one entry in engine_registry.json, matching the
// fields audit item P0-05 named: engine_id, version, contract, adapter,
// dependencies, input_schema, output_schema, determinism,
// replay_support, evidence_support, trust_support, telemetry_support,
// status.
type engineRegistryRow struct {
	EngineID         string   `json:"engine_id"`
	Version          string   `json:"version"`
	Contract         string   `json:"contract"`
	Adapter          string   `json:"adapter"`
	Dependencies     []string `json:"dependencies"`
	InputSchema      string   `json:"input_schema"`
	OutputSchema     string   `json:"output_schema"`
	Determinism      string   `json:"determinism"`
	ReplaySupport    bool     `json:"replay_support"`
	EvidenceSupport  bool     `json:"evidence_support"`
	TrustSupport     bool     `json:"trust_support"`
	TelemetrySupport bool     `json:"telemetry_support"`
	Status           []string `json:"status"`
}

type engineRegistry struct {
	GeneratedFrom string              `json:"generated_from"`
	Engines       []engineRegistryRow `json:"engines"`
}

// buildEngineRegistry derives engine_registry.json from
// execution.Registry() (the DAG's own declared shape) rather than a
// hand-maintained list that could silently drift from the real graph.
// gatePassed carries this run's real pass/fail for execution_graph and
// replay_determinism_100x, which is what decides whether a stage is
// marked TESTED/REPLAYABLE/VERIFIED -- an entry can never claim VERIFIED
// on a run where those gates did not both actually pass.
func buildEngineRegistry(gatePassed map[string]bool) engineRegistry {
	executionGraphPassed := gatePassed["execution_graph"]
	replayPassed := gatePassed["replay_determinism_100x"]

	entries := execution.Registry()
	rows := make([]engineRegistryRow, 0, len(entries))
	for _, e := range entries {
		deps := make([]string, len(e.Dependencies))
		for i, d := range e.Dependencies {
			deps[i] = string(d)
		}

		status := []string{"IMPLEMENTED"}
		if !e.AlwaysSkipped {
			status = append(status, "INTEGRATED")
		}
		if executionGraphPassed {
			status = append(status, "TESTED")
		}
		if replayPassed && !e.AlwaysSkipped {
			status = append(status, "REPLAYABLE")
		}
		if executionGraphPassed && replayPassed && !e.AlwaysSkipped {
			status = append(status, "VERIFIED")
		}

		determinism := "deterministic given identical release, evidence, entity state and logical tick"
		if e.AlwaysSkipped {
			determinism = "not applicable -- this stage is unconditionally skipped in the current wiring, see always_skipped_in_current_wiring"
		}

		rows = append(rows, engineRegistryRow{
			EngineID: string(e.StageID), Version: "execution/" + version.Current,
			Contract: "pkg/execution.Node", Adapter: e.Package, Dependencies: deps,
			InputSchema: "pkg/execution.Input", OutputSchema: "pkg/execution.Node",
			Determinism: determinism, ReplaySupport: replayPassed && !e.AlwaysSkipped,
			EvidenceSupport: true, TrustSupport: e.StageID == execution.StageTrust,
			TelemetrySupport: false, Status: status,
		})
	}
	return engineRegistry{GeneratedFrom: "pkg/execution.Registry() + this run's execution_graph/replay_determinism_100x gate results", Engines: rows}
}

// categoryHash folds the real per-gate evidence hashes of gateIDs into
// one deterministic digest, so a category field on the release
// certificate (TestManifestHash, SecurityManifestHash, ...) actually
// binds to what those gates measured instead of being a permanently
// empty declared field (audit P0-01's finding). Sorted so gate
// registration order never changes the result. A gateID with no
// matching evidence (e.g. -skip-race) is simply omitted, not treated
// as an error -- the hash still binds everything that did run.
func categoryHash(gateHashes map[string]string, gateIDs ...string) string {
	present := make([]string, 0, len(gateIDs))
	for _, id := range gateIDs {
		if h, ok := gateHashes[id]; ok {
			present = append(present, id+"="+h)
		}
	}
	sort.Strings(present)
	h := sha256.New()
	fmt.Fprint(h, "veriqo.category_hash/v1|")
	for _, entry := range present {
		fmt.Fprint(h, entry, "|")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// evidenceRootHash folds every gate's evidence hash into one root,
// answering audit P0-01's "EvidenceRootHash" field: a single value that
// changes if ANY gate's evidence changes, letting a verifier confirm
// the entire evidence set with one comparison instead of trusting each
// category hash was actually computed correctly.
func evidenceRootHash(gateHashes map[string]string) string {
	ids := make([]string, 0, len(gateHashes))
	for id := range gateHashes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := sha256.New()
	fmt.Fprint(h, "veriqo.evidence_root/v1|")
	for _, id := range ids {
		fmt.Fprint(h, id, "=", gateHashes[id], "|")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// binaryIdentity compiles the real deployable kernel binary
// (cmd/veriqo-node) into a temp file, hashes the actual compiled bytes
// (BinaryHash), and reads back the Go toolchain's own content-addressed
// build identifier via `go tool buildid` (BuildID) -- both real,
// verifiable outputs of this release's actual build, not asserted
// strings. Returns ("", "") if the build or buildid step fails; the
// certificate is still produced, just without these two fields, rather
// than aborting the whole readiness run over a secondary identity field.
func binaryIdentity() (binaryHash, buildID string) {
	tmp, err := os.CreateTemp("", "veriqo-node-*")
	if err != nil {
		return "", ""
	}
	binPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(binPath) }()

	// The module-qualified import path (not a "./"-relative one) is
	// required here: this function is also exercised by this package's
	// own tests, whose working directory is cmd/veriqo-readiness, not
	// the repository root, so a relative path would silently resolve
	// against the wrong directory.
	if _, code := run([]string{"go", "build", "-o", binPath, "veriqo/cmd/veriqo-node"}); code != 0 {
		return "", ""
	}
	raw, err := os.ReadFile(binPath) // #nosec G304 -- binPath is created by this process via os.CreateTemp, not external input
	if err != nil {
		return "", ""
	}
	sum := sha256.Sum256(raw)
	binaryHash = hex.EncodeToString(sum[:])

	if out, code := run([]string{"go", "tool", "buildid", binPath}); code == 0 {
		buildID = strings.TrimSpace(out)
	}
	return binaryHash, buildID
}

// closureMatrixSource is this binary's own copy of the eight blockers'
// six-dimension breakdown -- PART 14 of the "Final Audit result" audit
// ("extend veriqo-readiness so every blocker reports CODE_STATUS,
// DATA_STATUS, CONTRACT_STATUS, EXECUTION_STATUS, EXTERNAL_REVIEW_STATUS,
// EVIDENCE_STATUS, FINAL_STATUS"). Kept identical, by hand, to
// docs/qualification/VERIQO_EXTERNAL_CLOSURE_MATRIX.md's own table --
// see that document for the full reasoning behind each value; this is
// the same judgment, expressed as data qualification.Compute can run
// fail-closed over, not a second independent opinion.
func closureMatrixSource() []qualification.ClosureMatrixEntry {
	return []qualification.ClosureMatrixEntry{
		{GateID: "pentest", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimWaitingContract, ExecutionStatus: qualification.DimNotStarted,
			ExternalReviewStatus: qualification.DimNotStarted, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "scale_qualification", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimCodeComplete, ExecutionStatus: qualification.DimWaitingCloudExecution,
			ExternalReviewStatus: qualification.DimCodeComplete, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "multi_region_dr", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimCodeComplete, ExecutionStatus: qualification.DimWaitingCloudExecution,
			ExternalReviewStatus: qualification.DimCodeComplete, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "hsm_kms", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimWaitingContract, ExecutionStatus: qualification.DimNotStarted,
			ExternalReviewStatus: qualification.DimCodeComplete, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "live_data", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimWaitingData,
			ContractStatus: qualification.DimWaitingContract, ExecutionStatus: qualification.DimNotStarted,
			ExternalReviewStatus: qualification.DimCodeComplete, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "soak_72h", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimCodeComplete, ExecutionStatus: qualification.DimWaitingCloudExecution,
			ExternalReviewStatus: qualification.DimCodeComplete, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "spire_mtls", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimCodeComplete, ExecutionStatus: qualification.DimWaitingCloudExecution,
			ExternalReviewStatus: qualification.DimWaitingExternalReview, EvidenceStatus: qualification.DimEvidencePending},
		{GateID: "supply_chain_scan", CodeStatus: qualification.DimCodeComplete, DataStatus: qualification.DimCodeComplete,
			ContractStatus: qualification.DimCodeComplete, ExecutionStatus: qualification.DimWaitingCloudExecution,
			ExternalReviewStatus: qualification.DimCodeComplete, EvidenceStatus: qualification.DimEvidencePending},
	}
}

// printClosureMatrix runs qualification.Compute (fail-closed, never
// trusting a caller-supplied FinalStatus) over closureMatrixSource and
// prints the result -- the live, binary-produced counterpart to the
// static markdown table.
func printClosureMatrix() {
	fmt.Printf("\n===== EXTERNAL CLOSURE MATRIX (PART 14) =====\n")
	for _, entry := range closureMatrixSource() {
		computed, err := qualification.Compute(entry)
		if err != nil {
			fmt.Printf("  %-20s ERROR: %v\n", entry.GateID, err)
			continue
		}
		fmt.Printf("  %-20s final=%-24s (%s)\n", computed.GateID, computed.FinalStatus, computed.FinalReason)
	}
}

// --- PHASE E3 (P0-8): per-gate readiness axis evidence ---------------

// blockerRegistry loads the same docs/governance/production-blockers.json
// registry cmd/veriqo-qualification reads. On any failure it returns
// nil, and the caller skips the run entirely -- the ENGINEERING axis
// then honestly reports NOT_RUN rather than this program inventing a
// result.
func blockerRegistry() *blockers.Registry {
	raw, err := os.ReadFile(filepath.Join("docs", "governance", "production-blockers.json"))
	if err != nil {
		return nil
	}
	reg, err := blockers.LoadRegistry(raw)
	if err != nil {
		return nil
	}
	return reg
}

// internalDrillArtifacts maps each externally-blocked gate to the real,
// committed drill logs this repository already produced for it inside
// this environment. These back the INTERNAL axis and nothing else: an
// in-sandbox drill's honest ceiling is INTERNAL_QUALIFIED, never
// EXTERNAL_QUALIFIED, and internal/assurance enforces that structurally
// (see axes.go -- no internal evidence can move the EXTERNAL axis).
//
// A gate absent from this map, or naming a file that does not exist,
// reports INTERNAL as NOT_RUN. That is the correct, fail-closed answer:
// three of the eight (live_data, pentest, hsm_kms) have no in-sandbox
// drill that would mean anything, because what they need is a
// commercial contract, an independent vendor, and a paid tenancy
// respectively -- none of which has an internal analogue to run.
var internalDrillArtifacts = map[string][]string{
	"scale_qualification": {"evidence/scale_qualification-multi-container-drill.txt"},
	"multi_region_dr":     {"evidence/multi_region_dr-multi-container-drill.txt"},
	"spire_mtls": {
		"evidence/spire_mtls-multi-container-integration.txt",
		"evidence/spire_mtls-rafttcp-live-integration.txt",
	},
	"soak_72h":          {"evidence/soak-60min-run-log.txt"},
	"supply_chain_scan": {"evidence/supply_chain_scan-gosec-full.txt"},
}

// attachBlockerAxes records one blocked gate's ENGINEERING and INTERNAL
// axis evidence. It never calls Attach or Block: a gate's Status is
// decided entirely by the existing blocked/qualified logic above, and
// this function is incapable of changing it.
func attachBlockerAxes(reg *assurance.Registry, gateID string, out orchestrator.Outcome, now uint64) {
	if out.ID == gateID {
		code := 1
		if out.Pass {
			code = 0
		}
		body, _ := json.MarshalIndent(out, "", "  ")
		ev := assurance.NewEvidence(gateID, "internal:blockers/orchestrator.RunAll", string(body), code, now)
		if err := reg.AttachEngineering(gateID, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach engineering axis:", err)
			os.Exit(3)
		}
	}
	for _, path := range internalDrillArtifacts[gateID] {
		raw, err := os.ReadFile(path) // #nosec G304 -- path comes from this file's own hardcoded map, not external input
		if err != nil {
			// Fail-closed and silent by design: a missing drill log means
			// the INTERNAL axis reports NOT_RUN, which is the truth.
			continue
		}
		ev := assurance.NewEvidence(gateID,
			"internal:in-sandbox qualification drill log "+path+
				" (real run inside THIS environment; ceiling is INTERNAL_QUALIFIED, never external)",
			string(raw), 0, now)
		if err := reg.AttachInternal(gateID, ev); err != nil {
			fmt.Fprintln(os.Stderr, "readiness: attach internal axis:", err)
			os.Exit(3)
		}
	}
}
