// Command veriqo-insurance-cold-replay is the Round 4 work order's own
// requested operational surface for the insurance-domain
// export/discard/reconstruct/replay proof that already exists as
// library code in pkg/insurance/casepack.ColdReplay: a real CLI a
// reader can run themselves, `veriqo-insurance-cold-replay -case
// CASE-ID`, rather than only a unit test.
//
// Named distinctly from the pre-existing cmd/veriqo-cold-replay, which
// is a different, unrelated tool: the core kernel's
// execution-DAG/identity-ledger cold-restart replay
// (execution.ReplayDAGWithResult over an exported
// execution.ReplayRequest). That binary predates this round and has its
// own cross-process test suite (cold_replay_cross_process_test.go) —
// this command must never collide with its name or its `-export` flag
// contract.
//
// It reports the five ColdReplayReport axes casepack.ColdReplay already
// computes (evidence root hash, preservation hash, quantum result,
// policy version, coverage fact count), plus four itemized diff
// categories the work order names by name -- evidence, policy, rule
// (coverage-issue) and authorization differences -- each computed by
// directly comparing the LIVE and the COLD-REPLAYED Result, never by
// re-asserting the aggregate Pass() bit.
//
// Honesty note on "authorization_differences": the seven base synthetic
// cases (CASE-INS-001..007) carry no per-case party-permission model in
// casepack.Result today -- only the golden cross-domain case's
// extension layer (pkg/insurance/casepack/golden.go) does, via
// party.RelationshipRegistry. For a base case this command compares the
// dossier's own human-review authorization gate instead
// (HumanReviewRequired / HumanReviewQuestions); for the golden case it
// compares the actual broker relationship's granted permission set.
// Both are real, computed values -- neither is invented to fill the
// section.
//
// Usage:
//
//	veriqo-insurance-cold-replay -case CASE-INS-002
//	veriqo-insurance-cold-replay -case golden -out /tmp/replay-report.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"

	"veriqo/pkg/insurance/casepack"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/dossier"
	"veriqo/pkg/insurance/policy"
)

// Report is the full cold-replay diff for one case.
type Report struct {
	CaseID     string                    `json:"case_id"`
	SnapshotID string                    `json:"snapshot_id"`
	Axes       casepack.ColdReplayReport `json:"axes"`

	EvidenceDifferences      []string `json:"evidence_differences,omitempty"`
	PolicyDifferences        []string `json:"policy_differences,omitempty"`
	RuleDifferences          []string `json:"rule_differences,omitempty"`
	AuthorizationDifferences []string `json:"authorization_differences,omitempty"`
}

// Pass is derived: the axes must pass AND every diff category must be
// empty. There is no settable verdict field.
func (r Report) Pass() bool {
	return r.Axes.Pass() &&
		len(r.EvidenceDifferences) == 0 && len(r.PolicyDifferences) == 0 &&
		len(r.RuleDifferences) == 0 && len(r.AuthorizationDifferences) == 0
}

func main() {
	caseFlag := flag.String("case", "", `case ID to replay: CASE-INS-001 .. CASE-INS-007, or "golden"`)
	outFlag := flag.String("out", "", "optional path to write the report as JSON")
	flag.Parse()

	if *caseFlag == "" {
		fmt.Fprintln(os.Stderr, `veriqo-insurance-cold-replay: -case is required (CASE-INS-001..007, or "golden")`)
		os.Exit(2)
	}

	report, err := Replay(*caseFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "veriqo-insurance-cold-replay: %v\n", err)
		os.Exit(2)
	}

	printReport(report)

	if *outFlag != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-insurance-cold-replay: marshaling report: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(*outFlag, raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "veriqo-insurance-cold-replay: writing report: %v\n", err)
			os.Exit(2)
		}
	}

	if !report.Pass() {
		os.Exit(1)
	}
}

func printReport(r Report) {
	fmt.Println("===== VERIQO INSURANCE COLD REPLAY =====")
	fmt.Printf("case: %s   snapshot: %s\n\n", r.CaseID, r.SnapshotID)

	axis := func(name string, pass bool) { fmt.Printf("[%s] %s\n", pf(pass), name) }
	axis("evidence_root_hash_match", r.Axes.EvidenceRootHashMatch)
	axis("preservation_hash_match", r.Axes.PreservationHashMatch)
	axis("quantum_result_match", r.Axes.QuantumResultMatch)
	axis("policy_version_match", r.Axes.PolicyVersionMatch)
	axis("coverage_fact_count_match", r.Axes.CoverageFactCountMatch)

	printDiffs := func(name string, diffs []string) {
		if len(diffs) == 0 {
			fmt.Printf("[PASS] %s: none\n", name)
			return
		}
		fmt.Printf("[FAIL] %s:\n", name)
		for _, d := range diffs {
			fmt.Printf("       - %s\n", d)
		}
	}
	fmt.Println()
	printDiffs("evidence_differences", r.EvidenceDifferences)
	printDiffs("policy_differences", r.PolicyDifferences)
	printDiffs("rule_differences", r.RuleDifferences)
	printDiffs("authorization_differences", r.AuthorizationDifferences)

	fmt.Println()
	if r.Pass() {
		fmt.Println("verdict: COLD REPLAY REPRODUCES THE LIVE RESULT EXACTLY.")
	} else {
		fmt.Println("verdict: COLD REPLAY DIVERGED — see the FAIL rows above.")
	}
}

func pf(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}

// Replay runs the export/discard/reconstruct/replay cycle for one case
// ID and assembles the itemized diff report.
func Replay(caseID string) (Report, error) {
	if caseID == "golden" {
		return replayGolden()
	}
	return replayCase(casepack.CaseID(caseID))
}

func replayCase(id casepack.CaseID) (Report, error) {
	c, err := casepack.Get(id)
	if err != nil {
		return Report{}, fmt.Errorf("unknown case %q: %w", id, err)
	}
	live, replayed, axes, err := casepack.ColdReplay(c)
	if err != nil {
		return Report{}, fmt.Errorf("cold replay: %w", err)
	}

	r := Report{CaseID: string(id), SnapshotID: axes.SnapshotID, Axes: axes}
	r.EvidenceDifferences = diffEvidence(live.Built, replayed.Built)
	r.PolicyDifferences = diffPolicy(live.PolicyVersion.VersionID, replayed.PolicyVersion.VersionID, live.PolicyVersion, replayed.PolicyVersion)
	r.RuleDifferences = diffCoverage(live.Coverage, replayed.Coverage)
	r.AuthorizationDifferences = diffAuthorizationByReviewGate(live.Dossier, replayed.Dossier)
	return r, nil
}

func replayGolden() (Report, error) {
	live, err := casepack.DriveGolden()
	if err != nil {
		return Report{}, fmt.Errorf("live golden drive: %w", err)
	}
	replayed, axes, err := casepack.GoldenColdReplay()
	if err != nil {
		return Report{}, fmt.Errorf("golden cold replay: %w", err)
	}

	r := Report{CaseID: "GOLDEN", SnapshotID: axes.SnapshotID, Axes: axes}
	r.EvidenceDifferences = diffEvidence(live.Built, replayed.Built)
	r.PolicyDifferences = diffPolicy(live.PolicyVersion.VersionID, replayed.PolicyVersion.VersionID, live.PolicyVersion, replayed.PolicyVersion)
	r.RuleDifferences = diffCoverage(live.Coverage, replayed.Coverage)
	r.AuthorizationDifferences = diffAuthorizationByRelationship(live, replayed)
	return r, nil
}

func diffEvidence(live, replayed casepack.BuiltEvidence) []string {
	var diffs []string
	seen := map[string]bool{}
	for k, rec := range live.ByKey {
		seen[k] = true
		other, ok := replayed.ByKey[k]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("evidence key %q present in live, missing from replay", k))
		case rec.EvidenceID() != other.EvidenceID():
			diffs = append(diffs, fmt.Sprintf("evidence key %q content diverged: live=%s replay=%s", k, rec.EvidenceID(), other.EvidenceID()))
		}
	}
	for k := range replayed.ByKey {
		if !seen[k] {
			diffs = append(diffs, fmt.Sprintf("evidence key %q present in replay, missing from live", k))
		}
	}
	sort.Strings(diffs)
	return diffs
}

func diffPolicy(liveID, replayedID string, live, replayed policy.Version) []string {
	var diffs []string
	if liveID != replayedID {
		diffs = append(diffs, fmt.Sprintf("policy version ID diverged: live=%s replay=%s", liveID, replayedID))
	}
	if !reflect.DeepEqual(live, replayed) {
		diffs = append(diffs, "policy version content diverged (same or different ID, but the compared Version values are not deep-equal)")
	}
	return diffs
}

func diffCoverage(live, replayed coverage.CoverageAnalysis) []string {
	var diffs []string
	if !reflect.DeepEqual(live, replayed) {
		diffs = append(diffs, "coverage analysis diverged between live and cold-replayed runs")
	}
	return diffs
}

// diffAuthorizationByReviewGate is the base-case fallback: the seven
// synthetic cases carry no per-case party-permission model, so the
// closest real authorization gate this codebase computes for them is
// the dossier's own human-review requirement -- whether the case is
// authorized to proceed to closure without a human, and exactly which
// questions are blocking it if not.
func diffAuthorizationByReviewGate(live, replayed *dossier.Dossier) []string {
	var diffs []string
	if live == nil || replayed == nil {
		if live != replayed {
			diffs = append(diffs, "dossier presence diverged: one run produced a dossier and the other did not")
		}
		return diffs
	}
	if live.HumanReviewRequired != replayed.HumanReviewRequired {
		diffs = append(diffs, fmt.Sprintf("human_review_required diverged: live=%v replay=%v",
			live.HumanReviewRequired, replayed.HumanReviewRequired))
	}
	if !reflect.DeepEqual(live.HumanReviewQuestions, replayed.HumanReviewQuestions) {
		diffs = append(diffs, fmt.Sprintf("human_review_questions diverged: live=%v replay=%v",
			live.HumanReviewQuestions, replayed.HumanReviewQuestions))
	}
	return diffs
}

// diffAuthorizationByRelationship is the golden-case path: it compares
// the actual granted permission set on the broker relationship the
// golden extension layer registers, which is this codebase's one real
// party-authorization model today.
func diffAuthorizationByRelationship(live, replayed *casepack.GoldenResult) []string {
	var diffs []string
	lr, lok := live.Relationships.Get(live.BrokerRelationshipID)
	rr, rok := replayed.Relationships.Get(replayed.BrokerRelationshipID)
	if lok != rok {
		diffs = append(diffs, fmt.Sprintf("broker relationship presence diverged: live=%v replay=%v", lok, rok))
		return diffs
	}
	if !lok {
		return diffs
	}
	if lr.ConsentGiven != rr.ConsentGiven {
		diffs = append(diffs, fmt.Sprintf("broker relationship consent diverged: live=%v replay=%v", lr.ConsentGiven, rr.ConsentGiven))
	}
	if !reflect.DeepEqual(lr.Permissions, rr.Permissions) {
		diffs = append(diffs, fmt.Sprintf("broker relationship permissions diverged: live=%v replay=%v", lr.Permissions, rr.Permissions))
	}
	if lr.Status != rr.Status {
		diffs = append(diffs, fmt.Sprintf("broker relationship status diverged: live=%v replay=%v", lr.Status, rr.Status))
	}
	return diffs
}
