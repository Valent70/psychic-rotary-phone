package assurance

import (
	"fmt"
	"sort"
	"strings"
)

// This file answers the question the architecture kept asking in prose:
// are TECP, EQF, IF, CRF and FREF five capabilities of one system, or
// five names for whatever happens to be in the repository?
//
// The five are architectural fabrics, deliberately NOT five packages.
// Creating pkg/tecp, pkg/eqf, pkg/if, pkg/crf and pkg/fref-engine would
// have violated the anti-duplication rule the architecture sets for
// itself: five new façades over capabilities that already exist, five
// more places for a second implementation to hide.
//
// So a fabric here is a claim about existing code, audited along the
// eleven dimensions the architecture demands:
//
//	CAPABILITY -> CANONICAL PACKAGE -> ENTRY POINT -> CALL GRAPH
//	 -> DATA FLOW -> STATE FLOW -> EVIDENCE FLOW -> TEST -> E2E TEST
//	 -> REPLAY -> FAIL-CLOSED BEHAVIOUR
//
// Every dimension must be filled for every fabric. A blank is not a
// tidy omission; it is the audit's finding, and FabricAudit reports it.

// FabricID is one of the five canonical fabrics. There are five and
// there will not be a sixth without an amendment to the vocabulary.
type FabricID string

const (
	// TECP: Trust & Evidence Control Plane. Identity, trust, evidence,
	// provenance, authority, policy, audit, ledger.
	TECP FabricID = "TECP"
	// EQF: Epistemic Qualification Fabric. Qualification, independence,
	// contradiction, reverse proof, proof obligations, next best
	// evidence, uncertainty.
	EQF FabricID = "EQF"
	// IF: Intelligent Fabric. Knowledge, reasoning, causal, Bayesian,
	// counterfactual, temporal, economic, digital twin.
	IF FabricID = "IF"
	// CRF: Case Resolution Fabric. Case, mission, intent, hypothesis,
	// claim, evidence, timeline, finding, resolution, outcome.
	CRF FabricID = "CRF"
	// FREF: Forward-Reverse Execution Fabric.
	FREF FabricID = "FREF"
)

// Fabrics returns the five in architectural order.
func Fabrics() []FabricID { return []FabricID{TECP, EQF, IF, CRF, FREF} }

// FabricAudit is one fabric assessed along all eleven dimensions.
//
// Every field is a sentence naming something real. The audit's value is
// entirely in whether those sentences point at code that exists, which
// is why nothing here is a boolean: a "yes" column would have told the
// reader nothing they could check.
type FabricAudit struct {
	Fabric FabricID
	// Capability is what the fabric is for, in one line.
	Capability string
	// CanonicalPackages are the packages that own it. Plural is expected
	// and is not duplication: a fabric is a capability, and capabilities
	// span packages. Two packages deciding the SAME question would be
	// duplication, and that is what DuplicationRisk records.
	CanonicalPackages []string
	// EntryPoint is where a caller enters the fabric.
	EntryPoint string
	// CallGraph is the path through it.
	CallGraph string
	// DataFlow is what moves through it.
	DataFlow string
	// StateFlow is how state advances.
	StateFlow string
	// EvidenceFlow is what durable record it leaves.
	EvidenceFlow string
	// Test names the unit tests.
	Test string
	// E2ETest names the end-to-end proof.
	E2ETest string
	// Replay says how the fabric's output is reproduced.
	Replay string
	// FailClosed says what it refuses when it cannot complete.
	FailClosed string
	// DuplicationRisk names anything that looks like a second authority
	// on the same question, and why it is not one. Empty means none was
	// found, which is a finding a reviewer can contest.
	DuplicationRisk string
	// RetiredSynonyms are the names this fabric absorbs. Every one of
	// them appeared in VERIQO's own documents meaning roughly this, and
	// keeping them all alive is how five people end up describing five
	// architectures.
	RetiredSynonyms []string
}

var fabricAudits = []FabricAudit{
	{
		Fabric:     TECP,
		Capability: "Hold the canonical state of evidence, who may touch it, and what was done to it",
		CanonicalPackages: []string{
			"veriqo/pkg/evidence/manifest", "veriqo/pkg/evidence/provenance",
			"veriqo/pkg/authz", "veriqo/pkg/security/identity",
			"veriqo/pkg/platform/audit", "veriqo/pkg/platform/security/keys",
			"veriqo/pkg/canonical/jcs", "veriqo/pkg/storage/wal",
			"veriqo/pkg/platform/timestamp",
		},
		EntryPoint:   "manifest.Registry (evidence), audit.AuditStore.Append (record), authz (permission)",
		CallGraph:    "ingest -> manifest.Registry.Submit -> authz policy check -> custody event -> audit.AuditStore.Append -> WAL",
		DataFlow:     "raw bytes -> content hash -> evidence version -> custody chain head -> canonical audit record",
		StateFlow:    "DRAFT -> SUBMITTED -> FINALIZED -> (SUPERSEDED). FINALIZED is structurally terminal for mutation",
		EvidenceFlow: "hash-linked audit records with a Merkle root, plus the custody chain on each version",
		Test:         "pkg/evidence/manifest, pkg/platform/audit, pkg/authz, pkg/platform/timestamp",
		E2ETest:      "test/e2e/eight_blockers, test/integration",
		Replay:       "pkg/replay ManifestAdapter restores state from the record; audit.Auditor.VerifyChain re-derives the ledger",
		FailClosed:   "evidence that cannot be pinned to a version and a content hash never enters; a rights failure denies before contact; an unverifiable timeline is refused rather than mirrored",
		DuplicationRisk: "pkg/insurance/auditlink looks like a second ledger and is not: it mirrors into the one AuditStore. " +
			"pkg/insurance/decision and pkg/insurance/action expose AppendToLedger helpers that also write to that same store",
		RetiredSynonyms: []string{"Unified Evidence", "Evidence Fabric", "Evidence Engine", "Trust Engine", "Trust Kernel"},
	},
	{
		Fabric:     EQF,
		Capability: "Decide what the evidence supports, what it does not, and what is missing",
		CanonicalPackages: []string{
			"veriqo/pkg/qualification/state", "veriqo/pkg/qualification/independence",
			"veriqo/pkg/qualification/observability", "veriqo/pkg/qualification/reverseproof",
			"veriqo/pkg/qualification/nextbest", "veriqo/pkg/insurance/contradiction",
		},
		EntryPoint:   "reverseproof.Build (obligations), independence.Assess (sources), state.New (verdict)",
		CallGraph:    "claim -> reverseproof.Build -> reverseproof.Analyze -> independence.Cluster -> state.New -> nextbest.Rank",
		DataFlow:     "claim + conditions -> requirements -> gap -> effective source count -> qualification state -> ranked candidates",
		StateFlow:    "the ten qualification states; there is no PROVEN state and Parse refuses one by name",
		EvidenceFlow: "the qualification record carries its policy version, rationale and material dissent",
		Test:         "pkg/qualification/* (72 tests)",
		E2ETest:      "test/adversarial constitutional suite, pkg/proof sufficiency derivation",
		Replay:       "qualification is a pure function of its inputs; re-running Analyze on the same set reproduces the gap",
		FailClosed:   "UNKNOWN independence is never INDEPENDENT; an unattempted requirement is never observed-absent; a rights-denied candidate is excluded, not scored low",
		DuplicationRisk: "pkg/governance/qualification predates this fabric and records operational qualification decisions; " +
			"it does not compute epistemic state, so the two are not a second opinion on one question",
		RetiredSynonyms: []string{"Qualification", "EQF", "epistemic layer"},
	},
	{
		Fabric:     IF,
		Capability: "Propose explanations. Intelligence proposes; it never concludes",
		CanonicalPackages: []string{
			"veriqo/pkg/moat/kg", "veriqo/pkg/moat/causal", "veriqo/pkg/moat/hbayes",
			"veriqo/pkg/moat/temporal", "veriqo/pkg/moat/economic", "veriqo/pkg/moat/digitaltwin",
			"veriqo/pkg/moat/intelligence", "veriqo/pkg/inference",
		},
		EntryPoint:   "moat/intelligence, moat/causal hypothesis construction",
		CallGraph:    "evidence graph -> entity resolution -> causal structure -> hypothesis set -> (stops)",
		DataFlow:     "evidence + entities -> knowledge graph -> hypotheses with cited inputs -> inference traces",
		StateFlow:    "hypothesis status only; a hypothesis never becomes a finding inside this fabric",
		EvidenceFlow: "pkg/inference.InferenceTrace pins every input a hypothesis rests on",
		Test:         "pkg/moat/* package tests",
		E2ETest:      "the knowledge/intelligence boundary test: intelligence yields a hypothesis, never a finding",
		Replay:       "inference traces cite their inputs, so a hypothesis is re-derivable from the same evidence",
		FailClosed:   "reasoning that cannot cite its inputs produces no hypothesis; an entity resolution below threshold stays unresolved rather than merging two parties",
		DuplicationRisk: "pkg/ucr (Unified Cognitive Reasoning) and pkg/kernel/reasoning both reason. They are not a second " +
			"authority because neither can produce a finding: only pkg/proof can, and only from a sealed sufficient object",
		RetiredSynonyms: []string{"Knowledge Fabric", "Intelligence Layer", "Intelligent Fabric", "moat"},
	},
	{
		Fabric:     CRF,
		Capability: "Hold one case across every domain, from identity to outcome",
		CanonicalPackages: []string{
			"veriqo/pkg/casefabric", "veriqo/pkg/proof",
			"veriqo/pkg/lineage", "veriqo/pkg/insurance/casestate",
		},
		EntryPoint:   "casefabric.Open, then SetScope, AddEvidence, AddHypothesis, RegisterClaim, AttachProof, Resolve",
		CallGraph:    "Open -> SetScope -> AddEvidence -> AddHypothesis -> RegisterClaim -> BeginQualification -> AttachProof (verifies the proof object) -> Resolve",
		DataFlow:     "identity + scope -> pinned evidence -> hypotheses -> claims -> sealed proof objects -> outcome",
		StateFlow:    "nine canonical phases; every domain state maps onto one, and an unmapped state is refused",
		EvidenceFlow: "a hash-chained case timeline, mirrored into the one audit store by casefabric.Mirror",
		Test:         "pkg/casefabric (32 tests), pkg/proof (51 tests)",
		E2ETest:      "test/integration internal-gaps suite, test/adversarial fabric suite",
		Replay:       "casefabric.VerifyTimeline re-derives the chain; proof.VerifyHash re-derives every attached object",
		FailClosed:   "a case cannot resolve past an unproven material claim or an untested rival hypothesis; an outcome that adjudicates is refused",
		DuplicationRisk: "pkg/insurance/case, casestate, caseroom and cre are insurance's own case machinery and look like a rival engine. " +
			"They are projections: casestate's fourteen states are mapped onto the canonical phases and the mapping is asserted by test",
		RetiredSynonyms: []string{"Case Engine", "Case lineage", "case workflow", "Case Resolution Engine"},
	},
	{
		Fabric:     FREF,
		Capability: "Run both directions over the same evidence, and prove they close",
		CanonicalPackages: []string{
			"veriqo/pkg/fref", "veriqo/pkg/workflow", "veriqo/pkg/execution", "veriqo/pkg/kernel/runtime",
		},
		EntryPoint:   "fref.NewExecution(Forward|Reverse, subject), then Complete per stage; fref.Close for the closure",
		CallGraph:    "forward: OBSERVATION -> EVIDENCE -> KNOWLEDGE -> REASONING -> TRUST -> FINDING -> DECISION. reverse: CLAIM -> PROOF_OBLIGATIONS -> REQUIRED_EVIDENCE -> EVIDENCE_GAP -> CONTRADICTION -> QUALIFICATION -> NEXT_BEST_EVIDENCE",
		DataFlow:     "each stage pins an output hash; the closure compares the forward evidence set against the reverse required set",
		StateFlow:    "stages complete in canonical order and once only; a run is complete only at its terminal stage",
		EvidenceFlow: "stage records carry the package that ran, the tick and the pinned output",
		Test:         "pkg/fref (26 tests)",
		E2ETest:      "test/adversarial fabric closure cases",
		Replay:       "an execution is a record of pinned outputs, so a replay re-runs each stage against the same inputs",
		FailClosed:   "a stage cannot complete before its predecessors; a reverse run that stops at QUALIFICATION is incomplete; a closure fails when the finding rests on evidence no obligation required",
		DuplicationRisk: "pkg/workflow and pkg/kernel/execgraph both orchestrate. fref does not orchestrate: it is a contract that " +
			"refuses an execution that skipped or reordered a stage, and it names the package each stage must run in",
		RetiredSynonyms: []string{"Orchestrator", "Execution", "universal workflow", "pipeline"},
	},
}

// FabricAudits returns the five-fabric capability audit.
func FabricAudits() []FabricAudit { return append([]FabricAudit(nil), fabricAudits...) }

// dimensions returns the eleven audited dimensions as name/value pairs,
// so completeness can be checked without eleven hand-written branches.
func (f FabricAudit) dimensions() []struct {
	Name  string
	Value string
} {
	return []struct {
		Name  string
		Value string
	}{
		{"CAPABILITY", f.Capability},
		{"CANONICAL PACKAGE", strings.Join(f.CanonicalPackages, ", ")},
		{"ENTRY POINT", f.EntryPoint},
		{"CALL GRAPH", f.CallGraph},
		{"DATA FLOW", f.DataFlow},
		{"STATE FLOW", f.StateFlow},
		{"EVIDENCE FLOW", f.EvidenceFlow},
		{"TEST", f.Test},
		{"E2E TEST", f.E2ETest},
		{"REPLAY", f.Replay},
		{"FAIL-CLOSED BEHAVIOR", f.FailClosed},
	}
}

// Dimensions exposes the audited dimensions for reporting and testing.
func (f FabricAudit) Dimensions() []struct {
	Name  string
	Value string
} {
	return f.dimensions()
}

// Incomplete returns the dimensions this fabric leaves blank. An empty
// result means the fabric is fully audited, which is a claim about the
// audit, not about the code.
func (f FabricAudit) Incomplete() []string {
	var missing []string
	for _, d := range f.dimensions() {
		if strings.TrimSpace(d.Value) == "" {
			missing = append(missing, d.Name)
		}
	}
	return missing
}

// RetiredVocabulary returns every name the five fabrics absorb, sorted
// and deduplicated.
//
// These are all names that appear in VERIQO's own documents meaning
// roughly one of the five. Left alive, they guarantee that an engineer,
// an investor and a reviewer each read a different architecture out of
// the same repository. Retired, they have exactly one canonical
// replacement each.
func RetiredVocabulary() map[string]FabricID {
	out := map[string]FabricID{}
	for _, f := range fabricAudits {
		for _, s := range f.RetiredSynonyms {
			out[s] = f.Fabric
		}
	}
	return out
}

// FabricReport renders the audit.
func FabricReport() string {
	var b strings.Builder
	for _, f := range fabricAudits {
		b.WriteString(fmt.Sprintf("=== %s ===\n", f.Fabric))
		for _, d := range f.Dimensions() {
			value := d.Value
			if value == "" {
				value = "*** NOT AUDITED ***"
			}
			b.WriteString(fmt.Sprintf("  %-22s %s\n", d.Name, value))
		}
		if f.DuplicationRisk != "" {
			b.WriteString(fmt.Sprintf("  %-22s %s\n", "DUPLICATION RISK", f.DuplicationRisk))
		}
		syn := append([]string(nil), f.RetiredSynonyms...)
		sort.Strings(syn)
		b.WriteString(fmt.Sprintf("  %-22s %s\n\n", "RETIRES", strings.Join(syn, ", ")))
	}
	return b.String()
}
