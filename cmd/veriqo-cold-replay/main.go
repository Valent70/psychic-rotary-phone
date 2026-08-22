// Command veriqo-cold-replay is the "distributed cold replay"
// acceptance artifact a fresh audit (V7.12.7, item P0-E) asked for:
//
//	export historical execution -> destroy runtime state -> new
//	node/process -> restore immutable history -> replay -> same
//	evidence root, decision, explanation, verification hash.
//
// This binary IS the "new node/process": it is a genuinely separate
// compiled binary, launched as its own OS process, that reads NOTHING
// but an exported pkg/execution.ReplayRequest JSON file from disk --
// no shared memory, no import of whatever produced the export, no
// pointer into the original run's engine. It constructs a brand-new
// execution.Engine and independently rebuilds the entire DAG (every
// stage: evidence ingestion through decision, explanation and the
// verification certificate) purely from that file, exactly mirroring
// test/integration/ivf_cross_process_test.go's already-proven
// cross-process pattern for the fusion/contradiction/decision domains,
// extended to the FULL execution DAG.
//
// Honest scope: this proves cold restoration of evidence root,
// decision, explanation and verification hash from an exported file
// across a real process boundary. Entity resolution is a SEPARATE,
// optional check: pass -identity-export alongside -export to ALSO
// prove pkg/identity.Resolver's own independent ledger cold-restores
// identically -- a genuinely separate process rebuilding a Resolver
// from nothing but pkg/identity.ColdReplayExport's JSON and confirming
// every exported EntityIDAt query reproduces the SAME entity ID the
// original, pre-restart process recorded. Omitting -identity-export
// simply skips that check (not a failure): most callers only care
// about the execution DAG.
//
// Usage:
//
//	veriqo-cold-replay -export path/to/exported-execution.json [-identity-export path/to/exported-identity.json]
//
// Exit codes: 0 = PASSED (every compared node hash matches, and the
// identity check if requested also matched), 1 = FAILED (a divergent
// stage or identity query was found, or replay errored),
// 2 = usage/input error.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"veriqo/pkg/execution"
	"veriqo/pkg/identity"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	var exportPath, identityExportPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-export":
			if i+1 < len(args) {
				exportPath = args[i+1]
			}
		case "-identity-export":
			if i+1 < len(args) {
				identityExportPath = args[i+1]
			}
		}
	}
	if exportPath == "" {
		fmt.Fprintln(stderr, "usage: veriqo-cold-replay -export <exported-execution.json> [-identity-export <exported-identity.json>]")
		return 2
	}

	data, err := os.ReadFile(exportPath) // #nosec G304 G703 -- exportPath is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-cold-replay: reading export: %v\n", err)
		return 2
	}

	// A brand-new Engine backed by a nil pipeline, in a brand-new
	// process: zero shared state with whatever process produced data.
	// context.Background() is genuinely correct here (P0-6): this is a
	// standalone CLI batch job with no inbound request to inherit a
	// context from, unlike pkg/lifecycle.Orchestrator.RunUnified's real
	// Intent entrypoint.
	engine := execution.NewEngine(nil)

	// P0-D/P1-10 fix: a real production execution (anything that ran
	// through pkg/lifecycle.Orchestrator) always binds a live
	// *identity.Resolver to its execution.Engine, which changes
	// IDENTITY_RESOLUTION's own committed node hash (see execution.go's
	// StageIdentityResolution case: the hash input gains an
	// "|identity_ledger_head=..." term whenever Engine.Identity is
	// non-nil). Before this fix, this binary unconditionally replayed
	// with Identity left nil, so cold-replaying ANY real
	// lifecycle-produced execution was structurally guaranteed to
	// diverge at IDENTITY_RESOLUTION regardless of whether the ledger
	// content itself was faithfully restored -- a real defect this
	// round's HTTP-boundary P1-10 test caught. The committed trace
	// itself honestly discloses whether the original run was bound
	// (its IDENTITY_RESOLUTION node's own Detail string says "bound to
	// identity ledger head" only when it was), so that -- not merely
	// whether -identity-export was passed -- decides whether this
	// replay binds an Identity too. This keeps the pre-existing,
	// already-audited -identity-export contract intact: passing it
	// alongside an UNBOUND original execution (this file's own
	// pre-existing tests do exactly that, to independently prove
	// pkg/identity's cold-restore in isolation from the DAG) still only
	// runs the separate identity-queries check below, not a DAG-level
	// binding it never had.
	var peek execution.ReplayRequest
	if err := json.Unmarshal(data, &peek); err != nil {
		fmt.Fprintf(stderr, "veriqo-cold-replay: parsing export: %v\n", err)
		return 2
	}
	originalWasIdentityBound := false
	for _, n := range peek.Committed.Nodes {
		if n.StageID == execution.StageIdentityResolution && strings.Contains(n.Detail, "bound to identity ledger head") {
			originalWasIdentityBound = true
			break
		}
	}

	var idExport identity.ColdReplayExport
	if identityExportPath != "" {
		idData, err := os.ReadFile(identityExportPath) // #nosec G304 G703 -- identityExportPath is an operator-supplied CLI argument, not untrusted input
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-cold-replay: reading identity export: %v\n", err)
			return 2
		}
		if err := json.Unmarshal(idData, &idExport); err != nil {
			fmt.Fprintf(stderr, "veriqo-cold-replay: parsing identity export: %v\n", err)
			return 2
		}
	}

	if originalWasIdentityBound {
		if identityExportPath == "" {
			fmt.Fprintln(stderr, "veriqo-cold-replay: the committed execution's IDENTITY_RESOLUTION stage was "+
				"bound to a real identity ledger at record time; -identity-export is required to cold-replay "+
				"it (proceeding without it would silently diverge, not silently match)")
			return 2
		}
		resolver, err := identity.Rebuild(idExport.Ledger, idExport.Authorities)
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-cold-replay: rebuilding identity resolver from export: %v\n", err)
			return 1
		}
		engine.Identity = resolver
	}

	verdict, out, err := execution.ReplayDAGWithResult(context.Background(), data, engine)
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-cold-replay: REPLAY ERROR: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "veriqo-cold-replay: independent cold-restart replay report\n")
	fmt.Fprintf(stdout, "  original evidence root : %s\n", verdict.OriginalRootHash)
	fmt.Fprintf(stdout, "  replayed evidence root : %s\n", verdict.ReplayRootHash)
	fmt.Fprintf(stdout, "  nodes compared         : %d\n", verdict.NodesCompared)

	dagOK := verdict.Matched
	if dagOK {
		fmt.Fprintf(stdout, "  VERDICT                : PASSED (evidence root, decision, explanation and "+
			"verification certificate all reproduced from cold-restored history alone)\n")
		// A "PASSED" verdict already cryptographically proves every
		// node's own hash (and therefore every field its artifact
		// commits to) is reproduced identically -- these lines exist
		// so an operator can SEE the specific named values a later
		// audit round asked for ("Same: IntentID, EvidenceRoot,
		// EntityID, IdentityLedgerHead, DecisionID, PolicyHash,
		// Bayesian result, Decision, Twin, Verification"), not to
		// perform a SEPARATE check beyond what the node-by-node
		// comparison above already enforces.
		if out != nil {
			var temporalDetail string
			for _, n := range out.Trace.Nodes {
				if n.StageID == execution.StageTemporal {
					temporalDetail = n.Detail
				}
			}
			var identityLedgerHead string
			if engine.Identity != nil {
				identityLedgerHead = engine.Identity.Head()
			}
			policyHash := out.ExpectedPolicyHash
			if policyHash == "" {
				policyHash = out.Case.Policy.Hash()
			}
			fmt.Fprintf(stdout, "  reproduced fields (cold-restored, not independently re-derived beyond "+
				"the node comparison above):\n")
			fmt.Fprintf(stdout, "    IntentID              : %s\n", out.Trace.Context.CaseID)
			fmt.Fprintf(stdout, "    EntityID              : %s\n", out.Canonical.Twin.Entity)
			fmt.Fprintf(stdout, "    IdentityLedgerHead    : %s\n", identityLedgerHead)
			fmt.Fprintf(stdout, "    DecisionID            : %s\n", out.Explanation.DecisionID)
			fmt.Fprintf(stdout, "    PolicyHash            : %s\n", policyHash)
			fmt.Fprintf(stdout, "    Bayesian result       : %s\n", temporalDetail)
			fmt.Fprintf(stdout, "    Decision              : %s\n", out.Decision)
			fmt.Fprintf(stdout, "    TwinHead              : %s\n", out.Canonical.Certificate.TwinHead)
			fmt.Fprintf(stdout, "    VerificationCertID    : %s\n", out.Certificate.VerificationCertificateID)

			// PHASE F3 (P1-13): the same values again, machine-readable,
			// so an assertion set can COMPARE them against the
			// originating process's own values rather than an operator
			// eyeballing the block above. The human-readable lines stay
			// exactly as they were -- every existing consumer of this
			// output is unaffected.
			//
			// This is deliberately emitted only on a PASSED verdict: a
			// diverged replay's field values are the values of a run
			// that did not reproduce, and printing them as if they were
			// comparable would invite exactly the wrong conclusion.
			if raw, err := json.MarshalIndent(replayIdentities(out, engine, policyHash), "", "  "); err == nil {
				fmt.Fprintf(stdout, "%s%s\n%s\n", identitiesBeginMarker, string(raw), identitiesEndMarker)
			}
		}
	} else {
		fmt.Fprintf(stdout, "  divergent stage        : %s\n", verdict.DivergentStage)
		fmt.Fprintf(stdout, "  original node hash     : %s\n", verdict.OriginalNodeHash)
		fmt.Fprintf(stdout, "  replayed node hash     : %s\n", verdict.ReplayNodeHash)
		fmt.Fprintf(stdout, "  VERDICT                : FAILED\n")
	}

	identityOK := true
	if identityExportPath != "" {
		idData, err := os.ReadFile(identityExportPath) // #nosec G304 G703 -- identityExportPath is an operator-supplied CLI argument, not untrusted input
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-cold-replay: reading identity export: %v\n", err)
			return 2
		}
		idVerdict, err := identity.VerifyColdReplay(idData)
		if err != nil {
			fmt.Fprintf(stdout, "  IDENTITY VERDICT       : FAILED (%v)\n", err)
			return 1
		}
		identityOK = idVerdict.Matched
		fmt.Fprintf(stdout, "  identity queries       : %d checked\n", idVerdict.QueriesChecked)
		if identityOK {
			fmt.Fprintf(stdout, "  IDENTITY VERDICT       : PASSED (every exported EntityIDAt query reproduced "+
				"from a cold-restored pkg/identity.Resolver ledger alone)\n")
		} else {
			for _, m := range idVerdict.Mismatches {
				fmt.Fprintf(stdout, "  identity mismatch      : %s\n", m)
			}
			fmt.Fprintf(stdout, "  IDENTITY VERDICT       : FAILED\n")
		}
	}

	if dagOK && identityOK {
		return 0
	}
	return 1
}

// --- PHASE F3 (P1-13): the replay identity set ------------------------

// identitiesBeginMarker and identitiesEndMarker delimit the
// machine-readable identity block in this binary's stdout. Markers
// rather than a separate file so the block travels with the report a
// human already reads, and so nothing has to agree on a second path.
const (
	identitiesBeginMarker = "\n--- BEGIN REPLAY IDENTITIES (JSON) ---\n"
	identitiesEndMarker   = "--- END REPLAY IDENTITIES (JSON) ---"
)

// ReplayIdentities is the thirteen named identities PHASE F3 requires
// to be identical across a genuine Process A -> destroy -> Process B
// replay. Each field is read from the rebuilt Result; none is
// recomputed by a second implementation, because the point is to
// compare what the replay ACTUALLY produced against what the original
// run produced.
type ReplayIdentities struct {
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

// Names returns every field name in a fixed order, so a comparison can
// enumerate the set rather than hardcoding it in two places.
func (ReplayIdentities) Names() []string {
	return []string{
		"intent_id", "execution_id", "evidence_package_id", "entity_id",
		"identity_ledger_head", "policy_hash", "temporal_model_hash",
		"inference_trace_hash", "decision_id", "explanation_hash",
		"digital_twin_consequence_hash", "verification_certificate_id",
		"replay_package_id",
	}
}

// Values returns the identities as a name -> value map, in the same
// vocabulary Names uses.
func (r ReplayIdentities) Values() map[string]string {
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

// replayIdentities extracts the thirteen identities from a Result.
//
// Two of the thirteen deserve their derivation stated explicitly rather
// than left to a reader to infer:
//
//   - temporal_model_hash is the TEMPORAL_BAYESIAN DAG node's own hash.
//     There is no separate "model hash" artifact; the node hash is what
//     the execution actually committed to for that stage, so it is the
//     honest value to compare. When the stage was skipped, the node
//     hash still exists and still differs from an executed one.
//   - inference_trace_hash is the ExecutionRootHash: the single value
//     the whole DAG folds every node fingerprint into. Naming it
//     separately would imply a second, independent commitment that does
//     not exist.
func replayIdentities(out *execution.Result, engine *execution.Engine, policyHash string) ReplayIdentities {
	r := ReplayIdentities{
		IntentID:                  out.Trace.Context.CaseID,
		ExecutionID:               out.Trace.Context.ExecutionID,
		EvidencePackageID:         out.Trace.Context.EvidencePackageID,
		DecisionID:                out.Explanation.DecisionID,
		PolicyHash:                policyHash,
		InferenceTraceHash:        out.ExecutionRootHash,
		ExplanationHash:           out.Explanation.Hash,
		VerificationCertificateID: out.Certificate.VerificationCertificateID,
		ReplayPackageID:           out.ReplayPackage.ReplayPackageID,
	}
	if engine.Identity != nil {
		r.IdentityLedgerHead = engine.Identity.Head()
	}
	if out.Canonical != nil {
		r.EntityID = string(out.Canonical.Twin.Entity)
		r.DigitalTwinConsequenceHash = out.Canonical.Certificate.TwinHead
	}
	for _, n := range out.Trace.Nodes {
		if n.StageID == execution.StageTemporal {
			r.TemporalModelHash = n.Hash
		}
	}
	return r
}

// ParseReplayIdentities extracts the machine-readable identity block
// from this binary's stdout. Exported so an integration test in another
// package can parse a real subprocess's real output rather than
// re-deriving the values in-process, which would defeat the entire
// point of a cross-process proof.
func ParseReplayIdentities(stdout string) (ReplayIdentities, error) {
	start := strings.Index(stdout, strings.TrimPrefix(identitiesBeginMarker, "\n"))
	if start < 0 {
		return ReplayIdentities{}, fmt.Errorf("cold-replay output carries no replay-identity block")
	}
	start += len(strings.TrimPrefix(identitiesBeginMarker, "\n"))
	end := strings.Index(stdout[start:], identitiesEndMarker)
	if end < 0 {
		return ReplayIdentities{}, fmt.Errorf("cold-replay identity block is not terminated")
	}
	var out ReplayIdentities
	if err := json.Unmarshal([]byte(stdout[start:start+end]), &out); err != nil {
		return ReplayIdentities{}, fmt.Errorf("cold-replay identity block is not valid JSON: %w", err)
	}
	return out, nil
}
