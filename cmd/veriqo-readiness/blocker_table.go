// blocker_table.go closes external audit items A17 and A18.
//
// A17 ("Readiness CLI"): docs/AUDIT_A1_A18_GAP_MAPPING.md found that
// cmd/veriqo-readiness already computes every fact a reader would want --
// status, exit criteria, evidence, blocker reason -- but never prints it in
// the audit's required per-gate shape: STATUS / ACCEPTANCE CRITERION /
// EVIDENCE REQUIRED / EVIDENCE PRESENT / VERIFICATION RESULT / REMAINING
// GAP. buildReadinessTable derives that shape from the SAME
// assurance.Registry main() already builds -- it adds a rendering, not a
// second source of truth.
//
// A18 ("Blocker Mapping"): docs/governance/production-blockers.json
// already declares each blocker's required evidence categories, and
// evidence/*.json / evidence/*.txt already exist as real, gate-produced
// artifacts, but nothing maps one to the other. buildBlockerEvidenceMap
// closes that by globbing the real evidence directory for each blocker's
// real artifact filenames -- it never invents a filename that isn't
// actually on disk.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veriqo/internal/assurance"
)

// ReadinessRow is one gate rendered in the audit's exact required shape.
type ReadinessRow struct {
	GateID              string `json:"gate_id"`
	Status              string `json:"status"`
	AcceptanceCriterion string `json:"acceptance_criterion"`
	EvidenceRequired    string `json:"evidence_required"`
	EvidencePresent     string `json:"evidence_present"`
	VerificationResult  string `json:"verification_result"`
	RemainingGap        string `json:"remaining_gap"`
}

// productionBlocker mirrors the fields of docs/governance/production-
// blockers.json this file actually needs -- a read-only, narrower view,
// not a competing schema (pkg/blockers.Contract remains the authoritative
// Go type for that file; this is just enough to drive EVIDENCE REQUIRED
// text for the 8 gates that are blockers).
type productionBlocker struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	EvidenceRequirements []string `json:"evidence_requirements"`
}

func loadProductionBlockers(path string) map[string]productionBlocker {
	out := map[string]productionBlocker{}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is this file's own hardcoded default, not external input
	if err != nil {
		return out
	}
	var doc struct {
		Blockers []productionBlocker `json:"blockers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out
	}
	for _, b := range doc.Blockers {
		out[b.ID] = b
	}
	return out
}

// evidenceArtifactsFor globs evidenceDir for every real file whose name is
// exactly gateID, or begins with "gateID." or "gateID-" -- so
// "multi_region_dr" matches both multi_region_dr.txt (the exec'd-gate
// artifact, when the gate is exec'd rather than blocked) and the real
// external drill file multi_region_dr-multi-container-drill.txt, without
// this function ever having to hardcode that second filename. Returns
// paths relative to evidenceDir, sorted, so the result is stable across
// runs on the same evidence set.
func evidenceArtifactsFor(evidenceDir, gateID string) []string {
	entries, err := os.ReadDir(evidenceDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == gateID || strings.HasPrefix(name, gateID+".") || strings.HasPrefix(name, gateID+"-") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// buildReadinessTable renders every gate in reg into the audit's exact
// required per-gate shape. blockers is keyed by gate ID for the subset of
// gates that are also production blockers (see productionBlockersPath);
// gates outside that set fall back to their own ExitCriteria as the
// "evidence required" statement, since a non-blocker gate's evidence
// requirement IS its exit criteria (there is no separate declared list
// for those, unlike the 8 blockers).
func buildReadinessTable(reg *assurance.Registry, evidenceDir string, blockers map[string]productionBlocker) []ReadinessRow {
	gates := reg.Gates()
	rows := make([]ReadinessRow, 0, len(gates))
	for _, g := range gates {
		eff := g.EffectiveStatus()
		satisfied := g.Satisfied()

		evidenceRequired := g.ExitCriteria
		if b, ok := blockers[g.ID]; ok && len(b.EvidenceRequirements) > 0 {
			evidenceRequired = strings.Join(b.EvidenceRequirements, "; ")
		}

		present := evidenceArtifactsFor(evidenceDir, g.ID)
		evidencePresent := "NONE"
		if len(present) > 0 {
			evidencePresent = strings.Join(present, ", ")
		}

		verification := string(eff)
		if satisfied {
			verification += " (SATISFIED)"
		} else {
			verification += " (NOT SATISFIED)"
		}

		remainingGap := "none"
		switch {
		case g.Status == assurance.StatusBlocked && g.Blocker != "":
			remainingGap = g.Blocker
		case !satisfied:
			remainingGap = fmt.Sprintf("effective status %s does not yet reach required status %s (%s)", eff, g.RequiredStatus, g.ExitCriteria)
		}

		rows = append(rows, ReadinessRow{
			GateID: g.ID, Status: string(g.Status),
			AcceptanceCriterion: g.ExitCriteria,
			EvidenceRequired:    evidenceRequired,
			EvidencePresent:     evidencePresent,
			VerificationResult:  verification,
			RemainingGap:        remainingGap,
		})
	}
	return rows
}

// renderReadinessTableMarkdown renders rows as a GitHub-flavored markdown
// table, column order matching the audit's own named sequence exactly.
func renderReadinessTableMarkdown(rows []ReadinessRow) string {
	var b strings.Builder
	b.WriteString("# VERIQO Readiness Table (external audit item A17)\n\n")
	b.WriteString("Generated fresh by cmd/veriqo-readiness on every run, from the same assurance.Registry the release verdict itself is computed from -- this table cannot disagree with the verdict because it is not a second source of truth.\n\n")
	b.WriteString("| GATE | STATUS | ACCEPTANCE CRITERION | EVIDENCE REQUIRED | EVIDENCE PRESENT | VERIFICATION RESULT | REMAINING GAP |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	esc := func(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			esc(r.GateID), esc(r.Status), esc(r.AcceptanceCriterion), esc(r.EvidenceRequired),
			esc(r.EvidencePresent), esc(r.VerificationResult), esc(r.RemainingGap))
	}
	return b.String()
}

// blockerEvidenceEntry is one blocker's real, on-disk evidence footprint
// -- external audit item A18.
type blockerEvidenceEntry struct {
	BlockerID          string   `json:"blocker_id"`
	Name               string   `json:"name"`
	GateStatus         string   `json:"gate_status"`
	Satisfied          bool     `json:"satisfied"`
	EvidenceArtifacts  []string `json:"evidence_artifacts"`
	SharedCrossBlocker []string `json:"shared_cross_blocker_artifacts"`
}

type blockerEvidenceMap struct {
	GeneratedFrom string                          `json:"generated_from"`
	Blockers      map[string]blockerEvidenceEntry `json:"blockers"`
}

// crossBlockerArtifacts are the real evidence files produced by running
// ALL blockers together (pkg/blockers/orchestrator.RunAll and the
// external-qualification submission pipeline), so they legitimately
// support every blocker at once rather than exactly one -- this is the
// audit's own point (A18: "map one evidence artifact to multiple
// blockers"), not a placeholder list.
func crossBlockerArtifacts(evidenceDir string) []string {
	var out []string
	for _, name := range []string{"blockers-qualification-report.json", "external-qualification.json"} {
		if _, err := os.Stat(filepath.Join(evidenceDir, name)); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// buildBlockerEvidenceMap maps each of the 8 production blockers (loaded
// from docs/governance/production-blockers.json, the authoritative
// contract file) to the real evidence artifacts already on disk that
// pertain to it -- both the blocker-specific ones (evidenceArtifactsFor)
// and the shared cross-blocker ones every blocker's qualification run
// also produces. It never invents a filename: an entry with zero
// artifacts honestly means zero evidence exists yet for that blocker.
func buildBlockerEvidenceMap(reg *assurance.Registry, evidenceDir string, blockers map[string]productionBlocker) blockerEvidenceMap {
	shared := crossBlockerArtifacts(evidenceDir)
	ids := make([]string, 0, len(blockers))
	for id := range blockers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := blockerEvidenceMap{
		GeneratedFrom: "docs/governance/production-blockers.json (contract) cross-referenced against the real evidence/ directory at this run",
		Blockers:      make(map[string]blockerEvidenceEntry, len(ids)),
	}
	for _, id := range ids {
		b := blockers[id]
		g, ok := reg.Get(id)
		status, satisfied := "UNKNOWN", false
		if ok {
			status, satisfied = string(g.EffectiveStatus()), g.Satisfied()
		}
		out.Blockers[id] = blockerEvidenceEntry{
			BlockerID: id, Name: b.Name, GateStatus: status, Satisfied: satisfied,
			EvidenceArtifacts:  evidenceArtifactsFor(evidenceDir, id),
			SharedCrossBlocker: shared,
		}
	}
	return out
}

// writeReadinessTableAndBlockerMap generates and persists both A17 and
// A18 artifacts. Called from main() after the gate registry and blocked-
// gate loop are both fully populated, so every gate's real, final status
// is reflected -- never a mid-run snapshot.
func writeReadinessTableAndBlockerMap(reg *assurance.Registry, evidenceDir string) {
	blockers := loadProductionBlockers("docs/governance/production-blockers.json")

	rows := buildReadinessTable(reg, evidenceDir, blockers)
	md := renderReadinessTableMarkdown(rows)
	_ = os.WriteFile(filepath.Join(evidenceDir, "A17_READINESS_TABLE.md"), []byte(md), 0o600)
	if rowsJSON, err := json.MarshalIndent(rows, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(evidenceDir, "A17_readiness_table.json"), rowsJSON, 0o600)
	}

	bem := buildBlockerEvidenceMap(reg, evidenceDir, blockers)
	if bemJSON, err := json.MarshalIndent(bem, "", "  "); err == nil {
		_ = os.MkdirAll("docs/governance", 0o750)
		_ = os.WriteFile("docs/governance/BLOCKER_EVIDENCE_MAP.json", bemJSON, 0o600)
	}

	fmt.Println("\n" + md)
}
