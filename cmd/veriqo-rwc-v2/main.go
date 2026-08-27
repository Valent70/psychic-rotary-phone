// Command veriqo-rwc-v2 executes VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2
// (RWC-001 + RWC-002) through this repository's real native execution
// path and writes the audit evidence bundle to evidence/rwc_v2/.
//
// The path is the production one, not a private harness:
//
//	veriqo/kernel.New -> pkg/lifecycle.Orchestrator.RunUnified
//	-> pkg/execution.Engine (the 16-stage DAG, which is what calls
//	   pkg/canonical.Pipeline.RunCanonical)
//	-> Evidence/Provenance/Fusion/Truth/Causal/Risk/Decision/Twin
//	-> IVF verification -> LifecycleCertificate
//	-> pkg/replay.Engine (independent re-execution)
//
// Every artifact this command writes is generated from a real
// RunUnified/Replay call made in this process during this run. Nothing
// in evidence/rwc_v2/ is hand-written, and nothing was copied from
// another branch's evidence directory: a bundle copied from a different
// implementation would be fabricated evidence about THIS one.
//
// GOVERNED-DECISION STATUS. This is a real governed-decision entrypoint:
// it originates decisions through the canonical path, so it is
// registered as such in internal/entrypoints' audited matrix rather than
// left as an unlisted back door.
//
// Usage:
//
//	veriqo-rwc-v2 [-commit <sha>] [-binary-hash <sha256:...>] [-out <dir>]
//
// -commit and -binary-hash are the two pieces of release identity this
// process cannot compute for itself. Supplied together with the release
// version, source hash and SBOM hash (all three of which ARE computed
// here, for real), they let the command emit a pkg/governance/envelope
// evidence envelope for the bundle. Omitted, the command writes a named
// refusal into the bundle instead of an envelope with invented fields.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"veriqo/internal/sourcehash"
	"veriqo/pkg/governance/envelope"
	"veriqo/pkg/identity"
	"veriqo/pkg/rwc"
	"veriqo/veriqo/kernel"
)

// baseTick is the fixed logical clock every case runs at. There is no
// time.Now() anywhere in this command: a wall clock would make the
// bundle irreproducible and every hash in it a moving target.
const baseTick uint64 = 1

// envelopeValidityTicks is the window the emitted evidence envelope
// declares. It is expressed in the same logical ticks the rest of this
// command uses, per pkg/governance/envelope's own note that this
// repository has no wall clock inside the kernel.
const envelopeValidityTicks uint64 = 1_000_000

type caseRecord struct {
	CaseID string `json:"case_id"`
	RWCID  string `json:"rwc_id"`
	Kind   string `json:"kind"`

	InputHash       string `json:"input_hash"`
	ExecutionID     string `json:"execution_id"`
	CanonicalHash   string `json:"canonical_hash"`
	CertificateHash string `json:"certificate_hash"`
	// ExecutionRootHash is the pkg/execution DAG's own root hash for this
	// run, READ from lifecycle.Result.Certificate (which itself copied it
	// from the engine) and reported here. Nothing in this command
	// computes one.
	//
	// The field is deliberately named exactly what it holds, which trips
	// internal/entrypoints' assignment-shaped marker of the same name and
	// puts this file on that gate's allowlist with a written
	// justification. A shorter field name would have slipped past the
	// scanner silently; that is precisely the outcome the marker exists
	// to prevent, and dodging it by renaming would have been the same
	// evasion the gate is written to catch.
	ExecutionRootHash string `json:"execution_root_hash"`
	// LedgerAnchor is an in-process, in-memory fusion hash-chain head.
	// Not durable, not external. See rwc.CaseResult.LedgerAnchor.
	LedgerAnchor string `json:"ledger_anchor_in_memory_hash_chain_head"`

	DecisionAction string  `json:"decision_action"`
	RiskScore      float64 `json:"risk_score"`

	Verdict          string   `json:"verdict,omitempty"`
	ConsistencyWarn  string   `json:"consistency_warning,omitempty"`
	ProvenanceStatus string   `json:"provenance_status,omitempty"`
	HardViolations   []string `json:"hard_violations,omitempty"`
	Unresolved       []string `json:"unresolved,omitempty"`
	RedFlagsMatched  []string `json:"red_flags_matched,omitempty"`

	IVFVerified         bool   `json:"ivf_verified"`
	ReplayMatch         bool   `json:"replay_match"`
	ReplayOriginalHash  string `json:"replay_original_hash"`
	ReplayResultHash    string `json:"replay_result_hash"`
	ReplayDivergedStage string `json:"replay_diverged_stage,omitempty"`
	StagesCompared      int    `json:"stages_compared"`

	LineageCaseID       string   `json:"lineage_case_id"`
	LineageNodeCount    int      `json:"lineage_node_count"`
	LineageChainOK      bool     `json:"lineage_chain_verified"`
	LineageComplete     bool     `json:"lineage_complete"`
	LineageMissingKinds []string `json:"lineage_missing_kinds,omitempty"`

	LegacyIdentityFallback bool `json:"legacy_identity_fallback_used"`
	HumanReviewRequired    bool `json:"human_review_required"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("veriqo-rwc-v2", flag.ContinueOnError)
	commit := fs.String("commit", "", "release commit SHA; required for an evidence envelope")
	binaryHash := fs.String("binary-hash", "", "release binary hash; required for an evidence envelope")
	outDir := fs.String("out", "", "output directory (default: <repo>/evidence/rwc_v2)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	bundleDir := *outDir
	if bundleDir == "" {
		bundleDir = filepath.Join(repoRoot, "evidence", "rwc_v2")
	}
	certDir := filepath.Join(bundleDir, "certificates")
	replayDir := filepath.Join(bundleDir, "replay_requests")
	for _, d := range []string{certDir, replayDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}

	cases, err := rwc.BuildCorpusV2(baseTick)
	if err != nil {
		return fmt.Errorf("building corpus: %w", err)
	}

	var records []caseRecord
	inputHashes := map[string]string{}
	evidenceExport := map[string]any{}
	trustResults := map[string]any{}
	decisionResults := map[string]any{}
	ledgerAnchors := map[string]any{}
	correlationKeys := map[string]any{}
	caseLineage := map[string]any{}

	allPass := true

	for _, c := range cases {
		// A fresh Kernel per case: every engine, ledger and trust state
		// starts empty, so one case's arbitration history cannot influence
		// the next one's decision or its replay.
		k, err := kernel.New()
		if err != nil {
			return fmt.Errorf("kernel.New for %s: %w", c.ID, err)
		}
		ledger := rwc.EnableCaseLineage(k)

		res, err := rwc.Run(context.Background(), k, c.Request)
		if err != nil {
			_ = k.Shutdown()
			return fmt.Errorf("Run(%s): %w", c.ID, err)
		}

		rec := caseRecord{
			CaseID: c.ID, RWCID: c.RWCID, Kind: string(c.Kind),
			InputHash: res.InputHash, ExecutionID: res.ExecutionID,
			CanonicalHash: res.CanonicalHash, CertificateHash: res.CertificateHash,
			ExecutionRootHash: res.Lifecycle.Certificate.ExecutionRootHash,
			LedgerAnchor:      res.LedgerAnchor,
			DecisionAction:    res.DecisionAction, RiskScore: res.RiskScore,
			IVFVerified:            res.Lifecycle.IVFResult.ManifestValid && res.Lifecycle.IVFResult.ReplayValid,
			LineageCaseID:          string(res.LineageCaseID),
			LegacyIdentityFallback: res.Lifecycle.LegacyIdentityFallbackUsed,
			HumanReviewRequired:    res.Lifecycle.HumanReviewRequired,
		}
		if rec.LegacyIdentityFallback {
			allPass = false
		}

		comp := ledger.Completeness(res.LineageCaseID)
		rec.LineageNodeCount = comp.NodeCount
		rec.LineageChainOK = comp.ChainVerified
		rec.LineageComplete = comp.Complete
		for _, kind := range comp.MissingKinds {
			rec.LineageMissingKinds = append(rec.LineageMissingKinds, string(kind))
		}
		if !comp.ChainVerified {
			allPass = false
		}

		switch c.Kind {
		case rwc.KindVesselPortSuitability:
			verdict, warn := rwc.InterpretVerdict(c.Constraint, res.Lifecycle.Canonical.Decision)
			rec.Verdict = string(verdict)
			rec.ConsistencyWarn = warn
			rec.HardViolations = c.Constraint.HardViolations
			rec.Unresolved = c.Constraint.Unresolved
			if warn != "" {
				allPass = false
			}
		case rwc.KindProvenanceClaim:
			status := rwc.ClassifyProvenance(res.Lifecycle.Canonical, c.Request.Submissions)
			rec.ProvenanceStatus = string(status)
			rec.RedFlagsMatched = c.RedFlagsMatched
		}

		cert, replayErr := rwc.VerifyReplay(k, "veriqo-rwc-v2-cmd", res)
		if replayErr != nil {
			_ = k.Shutdown()
			return fmt.Errorf("VerifyReplay(%s): %w", c.ID, replayErr)
		}
		rec.ReplayOriginalHash = cert.OriginalResultHash
		rec.ReplayResultHash = cert.ReplayResultHash
		rec.ReplayMatch = cert.Match
		rec.ReplayDivergedStage = cert.DivergedStage
		rec.StagesCompared = cert.StagesCompared
		if !cert.Match {
			allPass = false
		}

		records = append(records, rec)
		inputHashes[c.ID] = res.InputHash
		ledgerAnchors[c.ID] = map[string]any{
			"fusion_chain_head":   res.LedgerAnchor,
			"kind":                "IN_MEMORY_HASH_CHAIN",
			"durable":             false,
			"externally_anchored": false,
			"note": "pkg/moat/fusion.Engine.Head() for this case's own fresh in-process engine; " +
				"re-derivable via Fusion.VerifyChain() while the process lives, and gone when it exits",
		}
		trustResults[c.ID] = map[string]any{
			"provenance_status":             res.Lifecycle.Canonical.Provenance.Status,
			"provenance_score":              res.Lifecycle.Canonical.Provenance.Score,
			"provenance_shared_ancestors":   res.Lifecycle.Canonical.Provenance.SharedAncestors,
			"provenance_evidence_ids":       res.Lifecycle.Canonical.Provenance.EvidenceIDs,
			"risk_label":                    res.Lifecycle.Canonical.Risk.Label,
			"risk_score":                    res.Lifecycle.Canonical.Risk.Score,
			"risk_breakdown":                res.Lifecycle.Canonical.Risk.Breakdown,
			"arbitration_winner_confidence": res.Lifecycle.Canonical.Arbitration.WinnerConfidence,
		}
		decisionResults[c.ID] = map[string]any{
			"action":        res.DecisionAction,
			"risk_score":    res.RiskScore,
			"policy":        c.Request.Policy.Name,
			"policy_hash":   c.Request.Policy.Hash(),
			"explanation":   res.Lifecycle.Canonical.Decision.Explanation,
			"pattern_score": c.Request.PatternScore,
			"price_anomaly": c.Request.PriceAnomaly,
		}
		evidenceExport[c.ID] = map[string]any{
			"predicate":               c.Request.Predicate,
			"submission_count":        len(c.Request.Submissions),
			"source_ids":              res.Lifecycle.SourceIDs,
			"arbitration_winner":      res.Lifecycle.Canonical.Arbitration.Winner,
			"contradiction":           res.Lifecycle.Canonical.Truth.Observation.Contradiction,
			"independent_families":    res.Lifecycle.Canonical.Dependency.IndependentFamilyCount(),
			"dependency_root_hash":    res.Lifecycle.Canonical.Certificate.DependencyRootHash,
			"max_dependency_discount": res.Lifecycle.Canonical.Certificate.MaxDependencyDiscount,
		}
		correlationKeys[c.ID] = res.Correlation
		caseLineage[c.ID] = map[string]any{
			"case_id":        string(res.LineageCaseID),
			"node_count":     comp.NodeCount,
			"chain_verified": comp.ChainVerified,
			"complete":       comp.Complete,
			"present_kinds":  comp.PresentKinds,
			"missing_kinds":  comp.MissingKinds,
			"note": "Complete=false is the correct answer: pkg/lineage requires an OUTCOME node " +
				"and no ground truth exists for these cases at case-run time",
		}

		if err := writeJSON(filepath.Join(certDir, c.ID+".json"), res.Lifecycle.Certificate); err != nil {
			_ = k.Shutdown()
			return err
		}
		// The exact bytes cmd/veriqo-cold-replay consumes to rebuild this
		// execution's whole DAG in a genuinely separate OS process. This
		// command does not run that replay itself — it cannot, because
		// constructing a second execution.Engine is guarded by
		// internal/entrypoints — so it exports the input and says so.
		exportBytes, err := res.Lifecycle.Execution.ExportReplay()
		if err != nil {
			_ = k.Shutdown()
			return fmt.Errorf("ExportReplay(%s): %w", c.ID, err)
		}
		if err := os.WriteFile(filepath.Join(replayDir, c.ID+".json"), exportBytes, 0o600); err != nil {
			_ = k.Shutdown()
			return err
		}
		// The identity half of the same export. Round R23's audit found
		// that shipping only the DAG export made the bundle's
		// cold-replayability claim FALSE: every RWC execution goes through
		// pkg/lifecycle, which binds a live *identity.Resolver to the
		// engine, so IDENTITY_RESOLUTION's committed node hash carries an
		// identity-ledger term. cmd/veriqo-cold-replay correctly REFUSES
		// such an export without -identity-export rather than replaying it
		// into a guaranteed divergence, so all ten cases exited 2 (usage
		// error), not 0. This file is what makes the claim true.
		//
		// Queries are the real aliases this case resolved, with the exact
		// entity ID this process got, so the cold replay must reproduce
		// entity resolution itself and not merely rebuild a ledger.
		idExport := identity.ColdReplayExport{Ledger: k.Identity.Ledger()}
		for _, alias := range c.Request.EntityAliases {
			idExport.Queries = append(idExport.Queries, identity.ColdReplayQuery{
				Alias:            identity.Identifier{Kind: identity.Kind(alias.Kind), Value: alias.Value},
				AsOfTick:         c.Request.Tick,
				ExpectedEntityID: string(res.Lifecycle.EntityID),
			})
		}
		if err := writeJSON(filepath.Join(replayDir, c.ID+".identity.json"), idExport); err != nil {
			_ = k.Shutdown()
			return err
		}

		if err := k.Shutdown(); err != nil {
			return fmt.Errorf("kernel.Shutdown(%s): %w", c.ID, err)
		}
	}

	sort.Slice(records, func(i, j int) bool { return records[i].CaseID < records[j].CaseID })

	manifest := map[string]any{
		"corpus_version":       rwc.CorpusVersion,
		"base_tick":            baseTick,
		"case_count":           len(cases),
		"case_ids":             caseIDs(cases),
		"all_pass":             allPass,
		"native_path":          "veriqo/kernel.New -> pkg/lifecycle.Orchestrator.RunUnified -> pkg/execution.Engine -> pkg/canonical.Pipeline.RunCanonical -> IVF -> LifecycleCertificate -> pkg/replay.Engine",
		"kernel_per_case":      true,
		"replay_kind":          "IN_PROCESS_FRESH_PIPELINE",
		"cross_process_replay": "NOT RUN BY THIS COMMAND — DAG export (<case>.json) and identity export (<case>.identity.json) written to replay_requests/ for cmd/veriqo-cold-replay -export <case>.json -identity-export <case>.identity.json",
	}

	files := map[string]any{
		"manifest.json":           manifest,
		"corpus_manifest.json":    caseIDs(cases),
		"execution_manifest.json": records,
		"input_hashes.json":       inputHashes,
		"evidence_export.json":    evidenceExport,
		"trust_results.json":      trustResults,
		"decision_results.json":   decisionResults,
		"ledger_anchors.json":     ledgerAnchors,
		"correlation_keys.json":   correlationKeys,
		"case_lineage.json":       caseLineage,
		"replay_results.json": map[string]any{
			"cases":               records,
			"all_replays_matched": allPass,
			"replay_engine":       "pkg/replay.Engine (fresh canonical.Pipeline, no shared pointer with the original run)",
		},
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeJSON(filepath.Join(bundleDir, name), files[name]); err != nil {
			return err
		}
	}

	if err := writeEnvelope(repoRoot, bundleDir, *commit, *binaryHash, len(cases), allPass, records); err != nil {
		return err
	}

	fmt.Printf("VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2: %d cases executed, all_pass=%v\n", len(cases), allPass)
	for _, r := range records {
		if r.Kind == string(rwc.KindVesselPortSuitability) {
			fmt.Printf("  %-30s verdict=%-22s decision=%-9s replay_match=%v\n", r.CaseID, r.Verdict, r.DecisionAction, r.ReplayMatch)
		} else {
			fmt.Printf("  %-30s provenance=%-14s decision=%-9s replay_match=%v\n", r.CaseID, r.ProvenanceStatus, r.DecisionAction, r.ReplayMatch)
		}
	}
	if !allPass {
		return fmt.Errorf("one or more cases failed replay, lineage or consistency checks — see %s",
			filepath.Join(bundleDir, "execution_manifest.json"))
	}
	return nil
}

// writeEnvelope emits the bundle's pkg/governance/envelope evidence
// envelope — or, when the release identity is incomplete, a named
// refusal explaining exactly which fields were missing.
//
// The refusal is the point. An envelope carries a release identity
// quadruple, and this process can genuinely compute only three of the
// five fields it needs (version from VERSION, source hash from
// internal/sourcehash over the real tree, SBOM hash from the real
// SBOM.json). Filling the other two with a plausible-looking value would
// produce an envelope that validates and means nothing.
func writeEnvelope(repoRoot, bundleDir, commit, binaryHash string, caseCount int, allPass bool, records []caseRecord) error {
	version, err := os.ReadFile(filepath.Join(repoRoot, "VERSION")) // #nosec G304 -- repoRoot is this process's own module root
	if err != nil {
		return fmt.Errorf("reading VERSION: %w", err)
	}
	src, err := sourcehash.Compute(repoRoot)
	if err != nil {
		return fmt.Errorf("computing source hash: %w", err)
	}
	sbomHash, err := fileHash(filepath.Join(repoRoot, "SBOM.json"))
	if err != nil {
		return fmt.Errorf("hashing SBOM.json: %w", err)
	}

	id := rwc.ReleaseIdentity{
		Release:    strings.TrimSpace(string(version)),
		Commit:     commit,
		SourceHash: src.RootHash,
		BinaryHash: binaryHash,
		SBOMHash:   sbomHash,
	}
	if !id.Complete() {
		var missing []string
		if id.Commit == "" {
			missing = append(missing, "commit (pass -commit)")
		}
		if id.BinaryHash == "" {
			missing = append(missing, "binary_hash (pass -binary-hash)")
		}
		return writeJSON(filepath.Join(bundleDir, "evidence_envelope.json"), map[string]any{
			"envelope_emitted": false,
			"reason": "release identity incomplete; an evidence envelope binds to one exact build " +
				"and this process cannot compute every field of that identity for itself",
			"missing_fields":       missing,
			"computed_release":     id.Release,
			"computed_source_hash": id.SourceHash,
			"computed_sbom_hash":   id.SBOMHash,
			"limitations":          rwc.Limitations(),
		})
	}

	arts, err := bundleArtifacts(bundleDir)
	if err != nil {
		return err
	}
	env := rwc.CorpusEnvelope(id, arts, map[string]string{
		"cases_executed":      strconv.Itoa(caseCount),
		"all_pass":            strconv.FormatBool(allPass),
		"replays_matched":     strconv.Itoa(countReplayMatches(records)),
		"artifacts_in_bundle": strconv.Itoa(len(arts)),
	}, 1, envelopeValidityTicks)
	if err := env.Validate(); err != nil {
		return fmt.Errorf("the emitted evidence envelope does not satisfy its own contract: %w", err)
	}
	return writeJSON(filepath.Join(bundleDir, "evidence_envelope.json"), map[string]any{
		"envelope_emitted": true,
		"envelope_id":      env.ID(),
		"envelope":         env,
		"note": "declared FIXTURE. It names gate_id=live_data because that is the gate this corpus " +
			"speaks to, and it is refused for that gate: its provider is not a registered trust " +
			"anchor and this repository registers none for it. See " +
			"pkg/rwc.TestCorpusEnvelopeCannotQualifyABlockedGate.",
	})
}

func countReplayMatches(records []caseRecord) int {
	n := 0
	for _, r := range records {
		if r.ReplayMatch {
			n++
		}
	}
	return n
}

// bundleArtifacts hashes every file this run actually wrote into the
// bundle, so the envelope's artifact root commits to the real bytes on
// disk rather than to a list of names.
func bundleArtifacts(bundleDir string) ([]envelope.Artifact, error) {
	var arts []envelope.Artifact
	err := filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(path) == "evidence_envelope.json" {
			return nil
		}
		h, err := fileHash(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(bundleDir, path)
		if err != nil {
			return err
		}
		arts = append(arts, envelope.Artifact{
			Name: filepath.ToSlash(rel), Hash: h, Bytes: uint64(info.Size()),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Name < arts[j].Name })
	return arts, nil
}

func fileHash(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is derived from this process's own module root
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func caseIDs(cases []rwc.CorpusCase) []string {
	out := make([]string, len(cases))
	for i, c := range cases {
		out[i] = c.ID
	}
	sort.Strings(out)
	return out
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s upward", dir)
		}
		dir = parent
	}
}
