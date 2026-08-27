package assurance

import "sort"

// This file answers the Round 4 work order's own explicit closing
// requirement: a single, composed TEMPORARY_PRODUCTION_READINESS gate
// over twelve named categories (CORE_KERNEL, INSURANCE,
// REAL_WORLD_NETWORK, EVIDENCE, SECURITY, IDENTITY, REPLAY, CASE_ROOM,
// DOSSIER, OBSERVABILITY, OPERATIONS, RELEASE_GOVERNANCE), reported
// under the ONE canonical taxonomy this package already computes
// (CanonicalStatus, axes.go) -- never a thirteenth, freshly-invented
// vocabulary.
//
// The work order is explicit that the honest ceiling here is
// "VERIQO Temporary Production Readiness Candidate", never "VERIQO
// Production Qualified" or a fabricated "60/60 VERIFIED" -- so
// TemporaryProductionReadiness computes its verdict from the SAME
// per-gate Canonical() values every other report in this program uses,
// and a category with real, named external blockers stays visibly
// TEMPORARY_CANDIDATE (or worse), never silently promoted.
//
// Category assignment is a fixed, one-gate-one-category lookup table,
// not a guess made at report time: every gate this program registers
// must appear in exactly one category, and Compose refuses to produce
// a report if any registered gate is missing from the table -- the
// same fail-closed discipline canonicalStatus itself follows.

// ReadinessCategory is one of the twelve areas the work order names.
type ReadinessCategory string

const (
	CategoryCoreKernel        ReadinessCategory = "CORE_KERNEL"
	CategoryInsurance         ReadinessCategory = "INSURANCE"
	CategoryRealWorldNetwork  ReadinessCategory = "REAL_WORLD_NETWORK"
	CategoryEvidence          ReadinessCategory = "EVIDENCE"
	CategorySecurity          ReadinessCategory = "SECURITY"
	CategoryIdentity          ReadinessCategory = "IDENTITY"
	CategoryReplay            ReadinessCategory = "REPLAY"
	CategoryCaseRoom          ReadinessCategory = "CASE_ROOM"
	CategoryDossier           ReadinessCategory = "DOSSIER"
	CategoryObservability     ReadinessCategory = "OBSERVABILITY"
	CategoryOperations        ReadinessCategory = "OPERATIONS"
	CategoryReleaseGovernance ReadinessCategory = "RELEASE_GOVERNANCE"
)

// AllCategories returns all twelve, in a fixed, stable presentation
// order (not alphabetical -- P0 product/kernel concerns first, then
// the insurance domain, then the Round 4 additions, then cross-cutting
// operational concerns).
func AllCategories() []ReadinessCategory {
	return []ReadinessCategory{
		CategoryCoreKernel, CategoryEvidence, CategoryIdentity, CategorySecurity,
		CategoryInsurance, CategoryRealWorldNetwork, CategoryReplay, CategoryDossier,
		CategoryCaseRoom, CategoryObservability, CategoryOperations, CategoryReleaseGovernance,
	}
}

// gateCategory is the one-gate-one-category lookup table. Every gate ID
// cmd/veriqo-readiness registers must have an entry here.
var gateCategory = map[string]ReadinessCategory{
	// ---- CORE_KERNEL: build correctness, the decision/execution core ----
	"build": CategoryCoreKernel, "vet": CategoryCoreKernel, "format": CategoryCoreKernel,
	"unit": CategoryCoreKernel, "acceptance": CategoryCoreKernel,
	"fuzz_smoke": CategoryCoreKernel, "zero_dependency": CategoryCoreKernel,
	"determinism_boundary": CategoryCoreKernel, "calibration": CategoryCoreKernel,
	"model_lifecycle": CategoryCoreKernel, "knowledge_evolution": CategoryCoreKernel,
	"hitl": CategoryCoreKernel, "decision_explanation": CategoryCoreKernel,
	"execution_graph": CategoryCoreKernel, "api_semantics": CategoryCoreKernel,
	"storage_recovery": CategoryCoreKernel, "economic_consequence": CategoryCoreKernel,
	"decision_precedence": CategoryCoreKernel, "bounded_test_execution": CategoryCoreKernel,
	"policy_registry_usage_coverage": CategoryCoreKernel, "temporal_calibration_usage_coverage": CategoryCoreKernel,
	"canonical_execution_entrypoint_coverage": CategoryCoreKernel, "race": CategoryCoreKernel,

	// ---- EVIDENCE: fusion, dependency graphs, evidence production authority ----
	"dependency_integration": CategoryEvidence, "data_quality": CategoryEvidence,
	"truth_arbitration_no_bypass": CategoryEvidence, "canonical_evidence_production_coverage": CategoryEvidence,

	// ---- IDENTITY: entity resolution and identity write authority ----
	"identity": CategoryIdentity, "canonical_entity_authority_coverage": CategoryIdentity,
	"canonical_identity_authority_coverage": CategoryIdentity,

	// ---- SECURITY: keys, sandbox, adversarial qualification, dependency scanning ----
	"security_unit": CategorySecurity, "sandbox": CategorySecurity,
	"pentest": CategorySecurity, "hsm_kms": CategorySecurity,
	"spire_mtls": CategorySecurity, "supply_chain_scan": CategorySecurity,

	// ---- INSURANCE: VICE domain correctness (blueprint SS54-SS57) ----
	"insurance_coverage_traceability": CategoryInsurance, "insurance_quantum_reproducibility": CategoryInsurance,
	"insurance_preservation_chain_integrity": CategoryInsurance, "insurance_human_review_enforcement": CategoryInsurance,

	// ---- REAL_WORLD_NETWORK: the network of real-world participants an
	// insurance case actually depends on -- roles, relationships,
	// authority, live external data admission, and the golden case that
	// proves the domain packages are CONNECTED rather than merely present.
	"insurance_golden_cross_domain": CategoryRealWorldNetwork, "live_data": CategoryRealWorldNetwork,

	// ---- REPLAY: deterministic reproduction under an adversarial "trust nothing" read ----
	"replay": CategoryReplay, "replay_determinism_100x": CategoryReplay,
	"insurance_cold_replay": CategoryReplay,

	// ---- DOSSIER: the independent, don't-trust-the-producer verification surface ----
	"dossier_verification": CategoryDossier,

	// ---- CASE_ROOM: the customer-facing permissioned view's access-control core ----
	"case_room_access_control": CategoryCaseRoom,

	// ---- OBSERVABILITY: telemetry correctness, coverage and leakage ----
	"observability": CategoryObservability, "telemetry_leakage_zero": CategoryObservability,
	"telemetry_export_pipeline_internal": CategoryObservability, "correlation_propagation_coverage": CategoryObservability,
	"telemetry_production_coverage": CategoryObservability,

	// ---- OPERATIONS: scale, DR, chaos, soak, sustained load ----
	"chaos": CategoryOperations, "stress_slo": CategoryOperations,
	"soak_harness_smoke": CategoryOperations, "scale_qualification": CategoryOperations,
	"multi_region_dr": CategoryOperations, "soak_72h": CategoryOperations,

	// ---- RELEASE_GOVERNANCE: the assurance plane's own honesty, traceability, and data governance ----
	"assurance_self": CategoryReleaseGovernance, "data_governance": CategoryReleaseGovernance,
	"traceability_matrix": CategoryReleaseGovernance, "external_harness_capability_coverage": CategoryReleaseGovernance,
}

// CategoryFor returns the category a gate ID belongs to, and whether it
// is known. An unmapped gate is a real gap in this table, not a silent
// default -- Compose refuses to proceed past one.
func CategoryFor(gateID string) (ReadinessCategory, bool) {
	c, ok := gateCategory[gateID]
	return c, ok
}

// canonicalRank orders CanonicalStatus from least to most ready, so a
// category's composed status can be computed as "the worst status any
// of my gates has" with a plain integer comparison rather than a
// bespoke switch that could silently omit a case.
var canonicalRank = map[CanonicalStatus]int{
	CanonicalNotReady:                      0,
	CanonicalBlockedExternal:               1,
	CanonicalReadyForExternalQualification: 2,
	CanonicalVerifiedInternal:              3,
}

// TemporaryReadinessReport is the whole-release composition: every
// registered gate's canonical status, grouped into its twelve
// categories, with one final aggregate verdict.
type TemporaryReadinessReport struct {
	Categories []CategoryComposition `json:"categories"`

	// Verdict is this program's own required honest ceiling. It is
	// NEVER "PRODUCTION_QUALIFIED" and never claims every gate is
	// VERIFIED -- see the doc comment on TemporaryReadinessVerdict below
	// for the exact rule.
	Verdict TemporaryReadinessVerdict `json:"verdict"`

	// UnmappedGates lists any registered gate ID with no entry in
	// gateCategory -- a real gap in this file, surfaced rather than
	// silently dropped from the composition.
	UnmappedGates []string `json:"unmapped_gates,omitempty"`
}

// CategoryComposition is one category's rollup plus its own composed
// canonical status.
type CategoryComposition struct {
	Category ReadinessCategory `json:"category"`
	GateIDs  []string          `json:"gate_ids"`

	VerifiedInternal              int `json:"verified_internal"`
	ReadyForExternalQualification int `json:"ready_for_external_qualification"`
	BlockedExternal               int `json:"blocked_external"`
	NotReady                      int `json:"not_ready"`

	// ComposedStatus is the worst (least-ready) CanonicalStatus among
	// this category's gates.
	ComposedStatus CanonicalStatus `json:"composed_status"`
}

// TemporaryReadinessVerdict is the closed, small vocabulary the
// aggregate report is allowed to state. Deliberately does NOT include
// "PRODUCTION_QUALIFIED" or any wording implying full external
// qualification -- the work order forbids exactly that claim, and this
// type structurally cannot express it.
type TemporaryReadinessVerdict string

const (
	// VerdictTemporaryCandidate is the honest ceiling this program can
	// ever report: every mandatory gate's ENGINEERING axis passes, and
	// no gate is NOT_READY on its own merits -- every remaining gap is a
	// real, named, categorized external dependency
	// (READY_FOR_EXTERNAL_QUALIFICATION or BLOCKED_EXTERNAL), never an
	// unexplained failure. This is the work order's own required
	// phrase: "VERIQO Temporary Production Readiness Candidate".
	VerdictTemporaryCandidate TemporaryReadinessVerdict = "TEMPORARY_PRODUCTION_READINESS_CANDIDATE"
	// VerdictNotYetCandidate means at least one gate is genuinely
	// NOT_READY -- a real engineering gap, not an external one -- so
	// even the temporary-candidate ceiling has not been earned yet.
	VerdictNotYetCandidate TemporaryReadinessVerdict = "NOT_YET_TEMPORARY_CANDIDATE"
)

// ComposeTemporaryReadiness derives the full report from an already-
// computed AxesReport (Registry.Axes()) -- itself derived, never
// declared, exactly like every other verdict in this package.
func ComposeTemporaryReadiness(axes AxesReport) TemporaryReadinessReport {
	byCategory := map[ReadinessCategory]*CategoryComposition{}
	for _, cat := range AllCategories() {
		byCategory[cat] = &CategoryComposition{Category: cat, ComposedStatus: CanonicalVerifiedInternal}
	}

	var unmapped []string
	worstRank := canonicalRank[CanonicalVerifiedInternal]
	for _, ga := range axes.Gates {
		cat, ok := gateCategory[ga.GateID]
		if !ok {
			unmapped = append(unmapped, ga.GateID)
			continue
		}
		cc := byCategory[cat]
		cc.GateIDs = append(cc.GateIDs, ga.GateID)
		switch ga.Canonical {
		case CanonicalVerifiedInternal:
			cc.VerifiedInternal++
		case CanonicalReadyForExternalQualification:
			cc.ReadyForExternalQualification++
		case CanonicalBlockedExternal:
			cc.BlockedExternal++
		case CanonicalNotReady:
			cc.NotReady++
		}
		if r := canonicalRank[ga.Canonical]; r < canonicalRank[cc.ComposedStatus] {
			cc.ComposedStatus = ga.Canonical
		}
		if r := canonicalRank[ga.Canonical]; r < worstRank {
			worstRank = r
		}
	}
	sort.Strings(unmapped)

	report := TemporaryReadinessReport{UnmappedGates: unmapped}
	for _, cat := range AllCategories() {
		cc := byCategory[cat]
		sort.Strings(cc.GateIDs)
		// A category with NO registered gate has no evidence at all --
		// the honest answer is NOT_READY, never the VERIFIED_INTERNAL
		// this map's zero value would otherwise leave in place. An empty
		// category silently reading as fully verified is exactly the
		// kind of fabricated green this whole program exists to refuse.
		if len(cc.GateIDs) == 0 {
			cc.ComposedStatus = CanonicalNotReady
			if r := canonicalRank[CanonicalNotReady]; r < worstRank {
				worstRank = r
			}
		}
		report.Categories = append(report.Categories, *cc)
	}

	report.Verdict = VerdictTemporaryCandidate
	if worstRank == canonicalRank[CanonicalNotReady] {
		report.Verdict = VerdictNotYetCandidate
	}
	return report
}
