// Command veriqo-rwc-v2 executes VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2
// (RWC-001 + RWC-002) through the real native VERIQO execution path —
// veriqo/kernel.Kernel -> pkg/lifecycle.Orchestrator.RunUnified ->
// pkg/execution.Engine -> pkg/canonical.Pipeline.RunCanonical — records
// and independently replays every case (pkg/replay.Engine), and writes
// the audit evidence bundle to evidence/rwc_v2/ per the brief's §15.
//
// Every artifact this command writes is generated from a real
// RunUnified/Replay call made in this process during this run — nothing
// in evidence/rwc_v2/ is hand-written.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"veriqo/pkg/rwc"
	"veriqo/veriqo/kernel"
)

const baseTime uint64 = 1 // fixed logical tick — no time.Now() anywhere in this command

type caseRecord struct {
	CaseID              string   `json:"case_id"`
	RWCID               string   `json:"rwc_id"`
	Kind                string   `json:"kind"`
	InputHash           string   `json:"input_hash"`
	ExecutionID         string   `json:"execution_id"`
	CanonicalHash       string   `json:"canonical_hash"`
	CertificateHash     string   `json:"certificate_hash"`
	LedgerAnchor        string   `json:"ledger_anchor"`
	DecisionAction      string   `json:"decision_action"`
	RiskScore           float64  `json:"risk_score"`
	Verdict             string   `json:"verdict,omitempty"`
	ConsistencyWarn     string   `json:"consistency_warning,omitempty"`
	ProvenanceStatus    string   `json:"provenance_status,omitempty"`
	HardViolations      []string `json:"hard_violations,omitempty"`
	Unresolved          []string `json:"unresolved,omitempty"`
	RedFlagsMatched     []string `json:"red_flags_matched,omitempty"`
	IVFVerified         bool     `json:"ivf_verified"`
	ReplayMatch         bool     `json:"replay_match"`
	ReplayOriginalHash  string   `json:"replay_original_hash"`
	ReplayResultHash    string   `json:"replay_result_hash"`
	ReplayDivergedStage string   `json:"replay_diverged_stage,omitempty"`
	StagesCompared      int      `json:"stages_compared"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}

func run() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	bundleDir := filepath.Join(repoRoot, "evidence", "rwc_v2")
	certDir := filepath.Join(bundleDir, "certificates")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return err
	}

	cases, err := rwc.BuildCorpusV2(baseTime)
	if err != nil {
		return fmt.Errorf("building corpus: %w", err)
	}

	var records []caseRecord
	var inputHashes = map[string]string{}
	var evidenceExport = map[string]any{}
	var trustResults = map[string]any{}
	var decisionResults = map[string]any{}
	var ledgerAnchors = map[string]string{}
	var replayResults = map[string]any{}

	allPass := true

	for _, c := range cases {
		k, err := kernel.New()
		if err != nil {
			return fmt.Errorf("kernel.New for %s: %w", c.ID, err)
		}

		res, err := rwc.Run(context.Background(), k, c.Request)
		if err != nil {
			_ = k.Shutdown()
			return fmt.Errorf("Run(%s): %w", c.ID, err)
		}

		rec := caseRecord{
			CaseID: c.ID, RWCID: c.RWCID, Kind: string(c.Kind),
			InputHash: res.InputHash, ExecutionID: res.ExecutionID,
			CanonicalHash: res.CanonicalHash, CertificateHash: res.CertificateHash,
			LedgerAnchor: res.LedgerAnchor, DecisionAction: res.DecisionAction,
			RiskScore: res.RiskScore, IVFVerified: res.Lifecycle.IVFResult.ManifestValid && res.Lifecycle.IVFResult.ReplayValid,
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
			status := rwc.ClassifyProvenance(res.Lifecycle.Canonical, len(c.Request.Submissions))
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
		ledgerAnchors[c.ID] = res.LedgerAnchor
		trustResults[c.ID] = map[string]any{
			"provenance_status": res.Lifecycle.Canonical.Provenance.Status,
			"provenance_score":  res.Lifecycle.Canonical.Provenance.Score,
			"risk_label":        res.Lifecycle.Canonical.Risk.Label,
			"risk_score":        res.Lifecycle.Canonical.Risk.Score,
		}
		decisionResults[c.ID] = map[string]any{
			"action":      res.DecisionAction,
			"risk_score":  res.RiskScore,
			"explanation": res.Lifecycle.Canonical.Decision.Explanation,
		}
		evidenceExport[c.ID] = map[string]any{
			"predicate":            c.Request.Predicate,
			"submission_count":     len(c.Request.Submissions),
			"arbitration_winner":   res.Lifecycle.Canonical.Arbitration.Winner,
			"contradiction":        res.Lifecycle.Canonical.Truth.Observation.Contradiction,
			"independent_families": res.Lifecycle.Canonical.Dependency.IndependentFamilyCount(),
		}

		certBytes, _ := json.MarshalIndent(res.Lifecycle.Certificate, "", "  ")
		if err := os.WriteFile(filepath.Join(certDir, c.ID+".json"), certBytes, 0o644); err != nil {
			_ = k.Shutdown()
			return err
		}

		if err := k.Shutdown(); err != nil {
			return fmt.Errorf("kernel.Shutdown(%s): %w", c.ID, err)
		}
	}

	sort.Slice(records, func(i, j int) bool { return records[i].CaseID < records[j].CaseID })

	replayResults["cases"] = records
	replayResults["all_replays_matched"] = allPass

	manifest := map[string]any{
		"corpus_version": rwc.CorpusVersion,
		"base_tick":      baseTime,
		"case_count":     len(cases),
		"case_ids":       caseIDs(cases),
		"all_pass":       allPass,
	}

	files := map[string]any{
		"manifest.json":           manifest,
		"corpus_manifest.json":    caseIDs(cases),
		"execution_manifest.json": records,
		"input_hashes.json":       inputHashes,
		"evidence_export.json":    evidenceExport,
		"trust_results.json":      trustResults,
		"decision_results.json":   decisionResults,
		"replay_results.json":     replayResults,
		"ledger_anchors.json":     ledgerAnchors,
	}
	for name, v := range files {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(bundleDir, name), b, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	fmt.Printf("VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2: %d cases executed, all_pass=%v\n", len(cases), allPass)
	for _, r := range records {
		if r.Kind == string(rwc.KindVesselPortSuitability) {
			fmt.Printf("  %-20s verdict=%-22s decision=%-9s replay_match=%v\n", r.CaseID, r.Verdict, r.DecisionAction, r.ReplayMatch)
		} else {
			fmt.Printf("  %-20s provenance=%-14s decision=%-9s replay_match=%v\n", r.CaseID, r.ProvenanceStatus, r.DecisionAction, r.ReplayMatch)
		}
	}
	if !allPass {
		return fmt.Errorf("one or more cases failed replay or consistency checks — see evidence/rwc_v2/execution_manifest.json")
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
