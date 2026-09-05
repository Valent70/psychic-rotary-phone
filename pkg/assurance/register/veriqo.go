package register

import (
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

// This file is VERIQO's own assurance graph. It consolidates what were
// two separate registers -- the twenty production gates and the
// assurance gap list -- into one structure in which every gate is
// walked to the evidence underneath it.
//
// Everything here is written at the level the evidence actually
// supports. Nothing in this file is above INTERNALLY_ASSURED, because
// no party outside VERIQO has examined any of it, and INTERNALLY_
// ASSURED is the highest rung reachable without one.

// assessedAt is the date this register was last assessed. It is fixed
// rather than read from the clock so that the report is byte-identical
// between runs and a change to it is a visible edit.
var assessedAt = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

const implementer contract.ID = "veriqo-engineering"

// internalEvidence is the shape every current evidence record takes,
// because every one of them is VERIQO's own work.
func internalEvidence(id, summary, scope string) state.Evidence {
	return state.Evidence{
		ID: contract.ID(id), Class: state.Internal, Summary: summary, Scope: scope,
		At: assessedAt, ArtefactHash: "see the reproducible build manifest in the auditor capsule",
		Validator: state.Validator{ID: implementer, Name: "VERIQO engineering"},
	}
}

// Controls is what VERIQO has built, as nodes a claim can attach to.
func Controls() []Control {
	return []Control{
		{ID: "CTL-CANON", Name: "RFC 8785 canonicalisation",
			Packages: []string{"pkg/canonical/jcs"}, Implementer: implementer},
		{ID: "CTL-LEDGER", Name: "Append-only hash-chained decision ledger",
			Packages: []string{"pkg/ledger"}, Implementer: implementer},
		{ID: "CTL-ANCHOR", Name: "External ledger anchoring",
			Packages: []string{"pkg/ledger"}, Implementer: implementer},
		{ID: "CTL-KMS", Name: "Key hierarchy and rotation",
			Packages: []string{"pkg/security/kms"}, Implementer: implementer},
		{ID: "CTL-TENANT", Name: "Cryptographic tenant isolation",
			Packages: []string{"pkg/tenant"}, Implementer: implementer},
		{ID: "CTL-IDENTITY", Name: "Workload and principal identity",
			Packages: []string{"pkg/identity"}, Implementer: implementer},
		{ID: "CTL-POLICY", Name: "Deny-overrides policy engine with an unoverridable core",
			Packages: []string{"pkg/policy", "pkg/authority"}, Implementer: implementer},
		{ID: "CTL-FIREWALL", Name: "Agent tool firewall",
			Packages: []string{"pkg/agents"}, Implementer: implementer},
		{ID: "CTL-AI-LADDER", Name: "AI qualification ladder and forbidden acts",
			Packages: []string{"pkg/ai", "pkg/modelregistry"}, Implementer: implementer},
		{ID: "CTL-REDACT", Name: "Redaction worker and release verification",
			Packages: []string{"pkg/evidence/redaction/worker"}, Implementer: implementer},
		{ID: "CTL-PROVENANCE", Name: "Provenance, custody and evidence versioning",
			Packages:    []string{"pkg/provenance", "pkg/custody", "pkg/evidence/version"},
			Implementer: implementer},
		{ID: "CTL-RESOLUTION", Name: "Entity resolution with five outcomes",
			Packages: []string{"pkg/resolution", "pkg/entity"}, Implementer: implementer},
		{ID: "CTL-INDEPENDENCE", Name: "Source independence assessment",
			Packages: []string{"pkg/qualification/independence"}, Implementer: implementer},
		{ID: "CTL-REPLAY", Name: "Deterministic replay",
			Packages: []string{"pkg/replay", "pkg/contract"}, Implementer: implementer},
		{ID: "CTL-RESILIENCE", Name: "Breaker, bulkhead and backpressure",
			Packages: []string{"pkg/resilience"}, Implementer: implementer},
		{ID: "CTL-RIGHTS", Name: "Licence and purpose limitation",
			Packages: []string{"pkg/rights", "pkg/connectors"}, Implementer: implementer},
		{ID: "CTL-SUPPLY", Name: "Dependency and build integrity",
			Packages: []string{"go.mod", "scripts/deliverable.sh"}, Implementer: implementer},
		{ID: "CTL-DR", Name: "Backup, restore and disaster recovery",
			Packages: []string{"pkg/ledger"}, Implementer: implementer},
	}
}

// Debts is the evidence VERIQO does not have.
//
// Each one is the mirror of a claim that cannot advance: what is
// missing, why, who owns it, who outside must supply it, what it
// costs, and what it blocks.
func Debts() []Debt {
	return []Debt{
		{ID: "ED-001", Missing: "an independent security assessment of the cryptographic " +
			"and isolation controls",
			Why: "no accredited firm has been engaged; the controls have been attacked only " +
				"by the team that wrote them",
			Owner: "Head of Engineering", ExternalDependency: "an accredited penetration " +
				"testing firm with cryptographic review capability",
			Class: state.ExternalRequired, Estimate: "6-10 weeks once engaged",
			Risk: "the isolation and key-hierarchy claims rest entirely on tests written by " +
				"people who knew where the code looks; a design flaw invisible from inside " +
				"would not have been found",
			BlockedClaims:    []contract.ID{"AC-TENANT-ISOLATION", "AC-KEY-HIERARCHY"},
			BlockedGates:     []string{"G4", "G15"},
			AffectedProducts: []string{"every multi-tenant deployment"},
			Raised:           assessedAt},

		{ID: "ED-002", Missing: "a production key root -- an HSM or a cloud KMS with an " +
			"attestation",
			Why:   "no HSM or KMS tenancy has been provisioned; the software root is a test double",
			Owner: "Head of Engineering", ExternalDependency: "an HSM vendor or cloud KMS provider",
			Class: state.ExternalRequired, Estimate: "4-6 weeks",
			Risk: "every signature and every derived key currently traces to a root held in " +
				"process memory. No signed artefact VERIQO produces today is a production " +
				"artefact, and the code refuses to pretend otherwise",
			BlockedClaims: []contract.ID{"AC-KEY-HIERARCHY", "AC-PASSPORT-SIGNATURE"},
			BlockedGates:  []string{"G1"}, Raised: assessedAt},

		{ID: "ED-003", Missing: "an external anchor for ledger checkpoints",
			Why: "VERIQO deliberately does not implement the Anchor interface, because an " +
				"anchor VERIQO controls proves only that VERIQO agrees with itself",
			Owner: "Head of Engineering",
			ExternalDependency: "a timestamping authority or another party willing to " +
				"countersign a checkpoint",
			Class: state.ExternalRequired, Estimate: "2-4 weeks",
			Risk: "a checkpoint proves the chain is internally consistent. Without an anchor " +
				"it does not prove the chain existed at the time it claims, so a wholesale " +
				"rewrite between two observations is undetectable from the artefact alone",
			BlockedClaims: []contract.ID{"AC-LEDGER-TAMPER-EVIDENT"},
			BlockedGates:  []string{"G10"}, Raised: assessedAt},

		{ID: "ED-004", Missing: "a real-world document corpus and an independent recovery " +
			"attempt against redacted derivatives",
			Why: "every fixture in the corpus was built by VERIQO; no customer or industry " +
				"body has supplied real documents, and nobody has tried to recover redacted " +
				"content",
			Owner: "Head of Evidence Engineering",
			ExternalDependency: "a customer or industry body supplying documents, and an " +
				"adversarial recovery lab",
			Class: state.ExternalRequired, Estimate: "unknown -- depends on a data agreement",
			Risk: "the 88% weighted coverage figure is an ESTIMATE over VERIQO's own " +
				"fixtures. Three refused structures are common in real documents, so a real " +
				"population would land there in bulk and the figure would fall. Separately, " +
				"absence of a term in twelve encodings is not irrecoverability",
			BlockedClaims:    []contract.ID{"AC-REDACTION-COVERAGE", "AC-REDACTION-IRREVERSIBLE"},
			BlockedGates:     []string{"G9"},
			AffectedProducts: []string{"any disclosure or regulatory-production workflow"},
			Raised:           assessedAt},

		{ID: "ED-005", Missing: "an independent adversarial test of the agent firewall and " +
			"the injection defence",
			Why: "the defence was designed and attacked by the same party; forty-three " +
				"adversarial tests exist and all were written by the implementer",
			Owner:              "Head of Engineering",
			ExternalDependency: "a red team with AI-agent experience",
			Class:              state.ExternalRequired, Estimate: "4-8 weeks once engaged",
			Risk: "an injection defence that has never been attacked by somebody who does " +
				"not know where it looks has been shown to work, not shown to hold",
			BlockedClaims: []contract.ID{"AC-INJECTION-REFUSED"},
			BlockedGates:  []string{"G16", "G17", "G18"}, Raised: assessedAt},

		{ID: "ED-006", Missing: "operational evidence: a multi-host, multi-region deployment " +
			"with a timed recovery and a soak",
			Why:   "no such environment exists; the longest run to date is measured in seconds",
			Owner: "Head of Platform", Class: state.ProductionRequired,
			ExternalDependency: "production infrastructure and an operator on call",
			Estimate:           "8-12 weeks",
			Risk: "concurrency is exercised in-process only. Nothing is known about behaviour " +
				"under sustained load, partial failure, network partition or restore, and " +
				"the recovery objectives are stated rather than measured",
			BlockedClaims: []contract.ID{"AC-DURABILITY", "AC-REPLAY-AFTER-RESTORE"},
			BlockedGates:  []string{"G3", "G5", "G6", "G11", "G12"}, Raised: assessedAt},

		{ID: "ED-007", Missing: "a live commercial data contract and a case run on data " +
			"VERIQO did not construct",
			Why: "no commercial provider has been contracted; every case run so far uses " +
				"fixtures written alongside the code that consumes them",
			Owner: "Head of Product", ExternalDependency: "a commercial data provider",
			Class: state.ExternalRequired, Estimate: "unknown -- commercial negotiation",
			Risk: "the resolution thresholds are VERIQO's stated choice rather than a " +
				"measured optimum, no false-merge rate has been measured against a labelled " +
				"population, and purpose limitation has never been exercised against a real " +
				"licensor's restrictions",
			BlockedClaims:    []contract.ID{"AC-RESOLUTION-NO-FALSE-MERGE", "AC-RIGHTS-ENFORCED"},
			BlockedGates:     []string{"G2"},
			AffectedProducts: []string{"maritime intelligence", "commodity flow"},
			Raised:           assessedAt},

		{ID: "ED-008", Missing: "a signed software bill of materials, artefact signing and a " +
			"vulnerability feed",
			Why:   "no signing authority or attestation service is configured in this environment",
			Owner: "Head of Platform",
			ExternalDependency: "a signing authority, an attestation service and a " +
				"vulnerability database provider",
			Class: state.ExternalRequired, Estimate: "3-5 weeks",
			Risk: "a consumer of a VERIQO build cannot verify what went into it, and no " +
				"process watches the dependency set for known vulnerabilities",
			BlockedClaims: []contract.ID{"AC-BUILD-INTEGRITY"},
			BlockedGates:  []string{"G8", "G19"}, Raised: assessedAt},

		{ID: "ED-009", Missing: "an evaluation set VERIQO did not construct, for model " +
			"qualification",
			Why: "no external evaluation set exists, so no model has ever been promoted to " +
				"QUALIFIED and the registry's top rung has never been exercised on real data",
			Owner:              "Head of Product",
			ExternalDependency: "an evaluation set from a party that is not VERIQO",
			Class:              state.ExternalRequired, Estimate: "unknown",
			Risk: "the model ladder is enforced but unexercised at its top rung; the " +
				"qualification criteria have never been applied to a model that could fail them",
			BlockedClaims: []contract.ID{"AC-MODEL-QUALIFICATION"},
			BlockedGates:  []string{"G20"}, Raised: assessedAt},

		{ID: "ED-011", Missing: "cross-implementation conformance for the canonicaliser",
			Why: "RFC 8785 conformance is asserted against the RFC's own examples, checked by " +
				"the same party that wrote the implementation. No independent JCS " +
				"implementation has been fed the same inputs and its output compared",
			Owner: "Head of Engineering",
			ExternalDependency: "an independently written RFC 8785 implementation, or the " +
				"maintainers of one",
			Class: state.ExternalRequired, Estimate: "2-3 weeks",
			Risk: "every digest, ledger record, passport and replay comparison in the system " +
				"passes through this one function. A divergence from the standard would be " +
				"invisible from inside -- the system would be perfectly self-consistent and " +
				"silently unable to interoperate with any other implementation, which is the " +
				"exact failure mode a canonical form exists to prevent",
			BlockedClaims: []contract.ID{"AC-CANONICAL-FORM"},
			Raised:        assessedAt},

		{ID: "ED-010", Missing: "a legal opinion on acquisition lawfulness for adverse-media " +
			"and breach-derived source classes",
			Why: "no counsel has reviewed the acquisition, retention or downstream-use " +
				"constraints for these source classes in any target jurisdiction",
			Owner: "General Counsel", ExternalDependency: "external counsel per jurisdiction",
			Class: state.LegalRequired, Estimate: "6-12 weeks per jurisdiction",
			Risk: "the intelligence layer models these constraints in code, and the model is " +
				"engineering's reading of the law rather than counsel's. A wrong reading is " +
				"not a bug, it is unlawful processing",
			BlockedClaims:    []contract.ID{"AC-LAWFUL-ACQUISITION"},
			AffectedProducts: []string{"OSINT and adverse-information screening"},
			Raised:           assessedAt},
	}
}

// Claims is what VERIQO asserts about its own controls.
func Claims() []Claim {
	return []Claim{
		{ID: "AC-LEDGER-TAMPER-EVIDENT", Subject: "decision ledger",
			Assertion: "no edit to, or removal of, a recorded ledger event can go undetected " +
				"when the chain is reopened",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "edit one field of a record in the middle of a written log, and " +
				"separately truncate a log from the front, then reopen each",
			Environment: "in-process, single host, temporary directories",
			Scope: "append and reopen paths; the checkpoint anchor is out of scope because " +
				"no anchor exists",
			Evidence: []state.Evidence{internalEvidence("AE-LEDGER-1",
				"the adversarial suite attacks the chain and the attacks are refused",
				"pkg/ledger, test/adversarial/tamper_test.go")},
			ClosedCounterexamples: []string{
				"a checksum failure anywhere in the log was treated as a torn tail, so " +
					"editing record 2 of 4 silently discarded records 2, 3 and 4 and reopened " +
					"the chain at height 2; fixed in pkg/ledger.tail",
				"the same path let a log truncated from the front open as an empty chain",
			},
			Limitations: []string{
				"a checkpoint proves internal consistency, not that the chain existed when " +
					"it says it did; that needs an external anchor",
			},
			Controls: []string{"CTL-LEDGER"}, Gates: []string{"G10"},
			Debts: []contract.ID{"ED-003"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-TENANT-ISOLATION", Subject: "multi-tenant isolation",
			Assertion: "no derived key, namespace or guarded scope of one tenant is reachable " +
				"from another",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "attempt key and namespace collisions across tenants, the " +
				"field-concatenation attack on the derivation, and every malformed tenant id " +
				"against the guard",
			Environment: "in-process, single host",
			Scope: "key derivation, namespacing and the request guard; no storage, cache or " +
				"index engine is involved because none is deployed",
			Evidence: []state.Evidence{internalEvidence("AE-TENANT-1",
				"derivation is length-prefixed per field and the guard fails closed on nine "+
					"malformed inputs including the zero-valued scope",
				"pkg/tenant, test/adversarial/tenancy_test.go")},
			Limitations: []string{
				"isolation is proven at the key-derivation layer only; a real deployment " +
					"introduces a database, a cache and a search index, none of which exists here",
			},
			Controls: []string{"CTL-TENANT"}, Gates: []string{"G14", "G15"},
			Debts: []contract.ID{"ED-001"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-KEY-HIERARCHY", Subject: "key management",
			Assertion: "every key is derived from a root that is never used directly, and " +
				"each rotation kind leaves the old key in a defined state",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyTested,
			DisproofPath: "attempt to obtain a usable key without the root, and to complete a " +
				"rotation that leaves an old key in no defined state",
			Environment: "in-process, with a software root that refuses to run in production mode",
			Scope:       "derivation and rotation semantics only",
			Evidence: []state.Evidence{internalEvidence("AE-KMS-1",
				"the rotation kinds and their old-key states are enforced and tested",
				"pkg/security/kms")},
			Limitations: []string{
				"the root is a TEST DOUBLE. No claim here extends to a production key root, " +
					"and the code refuses production mode rather than pretending",
			},
			Controls: []string{"CTL-KMS"}, Gates: []string{"G1", "G13"},
			Debts: []contract.ID{"ED-002", "ED-001"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-PASSPORT-SIGNATURE", Subject: "decision passport",
			Assertion: "a passport's limitations are inside the signed payload and cannot be " +
				"separated from it",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "issue a passport, strip or alter a limitation, and check whether " +
				"verification still succeeds",
			Environment: "in-process, with an ephemeral signing key",
			Scope:       "payload construction and verification; key custody is out of scope",
			Evidence: []state.Evidence{internalEvidence("AE-PASSPORT-1",
				"limitations are part of the signed digest and a tampered payload fails "+
					"verification", "pkg/passport")},
			Limitations: []string{"the signing key is ephemeral and held in process memory"},
			Controls:    []string{"CTL-LEDGER", "CTL-KMS"}, Gates: []string{"G1"},
			Debts: []contract.ID{"ED-002"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-REDACTION-COVERAGE", Subject: "redaction",
			Assertion: "a released derivative contains no rendering of any forbidden term in " +
				"any encoding the verifier checks, and any structure the worker cannot decode " +
				"is refused rather than released",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "run the structural corpus and, for each RELEASED derivative, search " +
				"its decompressed view independently of the pipeline's own verdict; then " +
				"submit undecodable and oversized structures and check what comes back",
			Environment: "in-process, over fixtures VERIQO built",
			Scope: "23 structural variants across PDF, XLSX and PPTX; no real-world document " +
				"population",
			Evidence: []state.Evidence{internalEvidence("AE-REDACT-1",
				"17 of 23 variants accepted, 6 refused by design, 0 failed",
				"pkg/evidence/redaction")},
			ClosedCounterexamples: []string{
				"every decompressed read used io.LimitReader, so a part inflating to 256 MiB " +
					"was truncated to 64 MiB, redacted, released and marked Verified; fixed by " +
					"readBounded",
			},
			Limitations: []string{
				"74% structural coverage is the share of VARIANTS, not a pass rate",
				"88% weighted coverage is an ESTIMATE; the prevalence weights are judgements",
				"three refused structures are COMMON in real documents",
			},
			Controls: []string{"CTL-REDACT"}, Gates: []string{"G9"},
			Debts: []contract.ID{"ED-004"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-REDACTION-IRREVERSIBLE", Subject: "redaction",
			Assertion:     "content removed from a derivative cannot be recovered from it",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.Implemented,
			DisproofPath: "an adversarial lab attempts recovery from format-specific remnants " +
				"-- incremental updates, revision history, object streams, embedded thumbnails " +
				"-- over derivatives produced from a real corpus",
			Environment: "none: this disproof path has never been run",
			Scope:       "nothing is established; the claim is recorded so that it is not assumed",
			Limitations: []string{
				"absence of a term in twelve encodings is not the same claim as " +
					"irrecoverability, and nothing in this repository attempts recovery",
			},
			Controls: []string{"CTL-REDACT"}, Gates: []string{"G9"},
			Debts: []contract.ID{"ED-004"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-INJECTION-REFUSED", Subject: "agent tool firewall",
			Assertion: "an instruction embedded in evidence cannot widen an agent's grants, " +
				"change its purpose, or reach a tool it was not launched with",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "plant an instruction in a document and make every call it asks for: " +
				"widen the grants, read another case, switch purpose, reach export and approve",
			Environment: "in-process, with a stub policy decision",
			Scope:       "the firewall's own checks; no model is in the loop",
			Evidence: []state.Evidence{internalEvidence("AE-FIREWALL-1",
				"ten adversarial tests attack the firewall and every attack is refused",
				"pkg/agents, test/adversarial/injection_test.go")},
			Limitations: []string{
				"the attacker was the party that wrote the defence and knew where it looked",
				"no model was in the loop, so nothing here says what a persuaded model attempts",
			},
			Controls: []string{"CTL-FIREWALL", "CTL-AI-LADDER"},
			Gates:    []string{"G16", "G17"},
			Debts:    []contract.ID{"ED-005"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-NO-SELF-QUALIFICATION", Subject: "AI qualification ladder",
			Assertion: "QUALIFIED is unreachable by automation under any policy, and no " +
				"producer can promote its own output",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "write a policy permitting automated QUALIFIED; then climb the " +
				"ladder one automated rung at a time and try to reach the top",
			Environment: "in-process",
			Scope:       "the ladder's promotion rules and the forbidden-act set",
			Evidence: []state.Evidence{internalEvidence("AE-AI-1",
				"AutomatedPolicy.Validate refuses a policy permitting automated QUALIFIED, and "+
					"the climb is refused at the last rung",
				"pkg/ai, test/adversarial/laundering_test.go")},
			Controls: []string{"CTL-AI-LADDER"}, Gates: []string{"G20"},
			Debts: []contract.ID{"ED-009"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-MODEL-QUALIFICATION", Subject: "model registry",
			Assertion: "a model reaches QUALIFIED only on an evaluation over data VERIQO did " +
				"not create",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.Implemented,
			DisproofPath: "attempt to qualify a model against an evaluation set VERIQO built",
			Environment:  "none: no model has been qualified, because no external set exists",
			Scope:        "the registry's rules only",
			Limitations: []string{
				"the top rung has never been exercised, so nothing is known about whether the " +
					"criteria are the right ones",
			},
			Controls: []string{"CTL-AI-LADDER"}, Gates: []string{"G20"},
			Debts: []contract.ID{"ED-009"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-RESOLUTION-NO-FALSE-MERGE", Subject: "entity resolution",
			Assertion: "no contradicted pair is merged, and no merge on a reassignable " +
				"identifier proceeds without a reviewer",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "construct pairs that trip each veto and each caveat and demand the " +
				"outcome band changes; measure a false-merge rate against a labelled population",
			Environment: "in-process, over constructed pairs",
			Scope:       "the decision logic; no measured error rate exists",
			Evidence: []state.Evidence{internalEvidence("AE-RESOLVE-1",
				"contradiction vetoes, caveats apply on every band, and MMSI cannot carry a "+
					"merge alone", "pkg/resolution")},
			Limitations: []string{
				"the 0.85/0.45 thresholds are a stated choice, not a measured optimum",
				"no false-merge rate has been measured against real data",
			},
			Controls: []string{"CTL-RESOLUTION", "CTL-INDEPENDENCE"}, Gates: []string{"G2"},
			Debts: []contract.ID{"ED-007"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-RIGHTS-ENFORCED", Subject: "data rights",
			Assertion: "a derivative may be used only in ways permitted by every source it " +
				"came from",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyTested,
			DisproofPath: "combine licences whose permissions differ and attempt each use the " +
				"intersection forbids; encode a real commercial licence and exercise its " +
				"restrictions",
			Environment: "in-process, over synthetic licences",
			Scope:       "the six use questions and their intersection",
			Evidence: []state.Evidence{internalEvidence("AE-RIGHTS-1",
				"Combine takes the intersection and an empty restriction list means unrestricted",
				"pkg/rights")},
			Limitations: []string{"no commercial licence has ever been encoded from real terms"},
			Controls:    []string{"CTL-RIGHTS"}, Gates: []string{"G2"},
			Debts: []contract.ID{"ED-007"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-LAWFUL-ACQUISITION", Subject: "intelligence source classes",
			Assertion: "material from a source class whose acquisition lawfulness is " +
				"unestablished cannot found a qualified finding",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyTested,
			DisproofPath: "attempt to qualify a finding whose only support is breach-derived " +
				"or unattested adverse-media material",
			Environment: "in-process",
			Scope: "the source-class model in code; it is engineering's reading of the " +
				"constraints, not counsel's",
			Evidence: []state.Evidence{internalEvidence("AE-INTEL-1",
				"restricted source classes are refused as sole support and carry mandatory "+
					"caveats", "pkg/intel/source")},
			Limitations: []string{
				"no lawyer has reviewed this model in any jurisdiction. A wrong reading here " +
					"is not a bug, it is unlawful processing",
			},
			Controls: []string{"CTL-RIGHTS"}, Debts: []contract.ID{"ED-010"},
			Implementer: implementer, At: assessedAt},

		{ID: "AC-DURABILITY", Subject: "decision ledger",
			Assertion:     "an acknowledged append survives a crash",
			RequiredLevel: state.OperationallyProven, CurrentLevel: state.InternallyTested,
			DisproofPath: "kill the process mid-append across many trials and check every " +
				"acknowledged record is present on reopen; then do it on real hardware under load",
			Environment: "in-process, single host, no real fault injection",
			Scope:       "fsync-before-ack and torn-tail recovery",
			Evidence: []state.Evidence{internalEvidence("AE-DURABLE-1",
				"the write is fsynced before the call returns and a torn tail is recovered",
				"pkg/ledger")},
			Limitations: []string{
				"no power loss, no disk failure, no full disk, no multi-host run has been tested",
			},
			Controls: []string{"CTL-LEDGER", "CTL-DR"}, Gates: []string{"G5", "G6", "G11"},
			Debts: []contract.ID{"ED-006"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-REPLAY-AFTER-RESTORE", Subject: "replay",
			Assertion:     "a decision replayed from a restored system reaches the same result",
			RequiredLevel: state.OperationallyProven, CurrentLevel: state.Implemented,
			DisproofPath: "restore a backup into a clean environment and replay a case " +
				"end to end, comparing every intermediate digest",
			Environment: "none: no backup target and no restore has ever been performed",
			Scope:       "nothing is established beyond in-process determinism",
			Limitations: []string{
				"nondeterministic steps replay from a recording, which establishes the " +
					"pipeline's behaviour given those outputs, not that they would recur",
			},
			Controls: []string{"CTL-REPLAY", "CTL-DR"}, Gates: []string{"G12"},
			Debts: []contract.ID{"ED-006"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-CANONICAL-FORM", Subject: "canonicalisation",
			Assertion: "two structurally identical records hash identically, and no input is " +
				"silently repaired before hashing",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "feed the RFC 8785 boundary cases and invalid UTF-8, and check " +
				"whether anything is coerced rather than refused",
			Environment: "in-process",
			Scope:       "RFC 8785 conformance as tested against the boundary cases in the RFC",
			Evidence: []state.Evidence{internalEvidence("AE-CANON-1",
				"ECMAScript number form, UTF-16 key ordering and refusal of invalid UTF-8 and "+
					"non-finite values", "pkg/canonical/jcs")},
			Limitations: []string{
				"conformance is asserted against the RFC's own examples, not against an " +
					"independent implementation's output",
			},
			Controls: []string{"CTL-CANON"}, Debts: []contract.ID{"ED-011"},
			Implementer: implementer, At: assessedAt},

		{ID: "AC-BUILD-INTEGRITY", Subject: "supply chain",
			Assertion:     "a consumer can verify what went into a VERIQO build",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.Specified,
			DisproofPath: "take a delivered artefact and attempt to establish its inputs " +
				"without trusting VERIQO's own statement of them",
			Environment: "none: no SBOM, no signing, no attestation",
			Scope:       "nothing is established",
			Limitations: []string{"the build is reproducible from git; nothing signs it"},
			Controls:    []string{"CTL-SUPPLY"}, Gates: []string{"G8", "G19"},
			Debts: []contract.ID{"ED-008"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-POLICY-CORE", Subject: "policy engine",
			Assertion:     "no configurable rule can permit what the core layer denies",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "write permissive rules for every core denial and check each is " +
				"still refused",
			Environment: "in-process",
			Scope:       "the engine's evaluation order; no external policy bundle is loaded",
			Evidence: []state.Evidence{internalEvidence("AE-POLICY-1",
				"deny-overrides with a core layer no rule can reach", "pkg/policy")},
			Controls: []string{"CTL-POLICY"}, Gates: []string{"G4"},
			Debts: []contract.ID{"ED-001"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-ANCHOR-DELIBERATELY-ABSENT", Subject: "external ledger anchoring",
			Assertion: "VERIQO does not implement the anchor interface, and the absence is a " +
				"design decision rather than an omission",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.Specified,
			DisproofPath: "look for any Anchor implementation in the module, and check whether " +
				"RequireAnchored can be satisfied without a third party",
			Environment: "the repository itself",
			Scope: "the absence only. Nothing here establishes that any anchor VERIQO might " +
				"later integrate is trustworthy",
			Limitations: []string{
				"an anchor VERIQO controls would prove only that VERIQO agrees with itself, " +
					"which is why the interface is declared and left unimplemented",
			},
			Controls: []string{"CTL-ANCHOR"}, Gates: []string{"G10"},
			Debts: []contract.ID{"ED-003"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-PROVENANCE-UNBROKEN", Subject: "provenance and custody",
			Assertion: "processing cannot launder an artefact's origin, and a custody break " +
				"is permanent once recorded",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.InternallyAssured,
			DisproofPath: "substitute content under an honest-looking custody link and check " +
				"whether the break is detected and whether a later well-formed link repairs it",
			Environment: "in-process",
			Scope:       "the recorded-digest comparison; no real chain of custody is involved",
			Evidence: []state.Evidence{internalEvidence("AE-PROV-1",
				"breaks are found from RECORDED digests, never by re-hashing current bytes, "+
					"and are permanent", "pkg/custody, pkg/provenance")},
			Limitations: []string{
				"no evidence provider has confirmed that material VERIQO holds is what they " +
					"supplied",
			},
			Controls: []string{"CTL-PROVENANCE"}, Gates: []string{"G10"},
			Debts: []contract.ID{"ED-003"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-BACKPRESSURE", Subject: "resilience",
			Assertion:     "safety-critical work is never shed under load",
			RequiredLevel: state.OperationallyProven, CurrentLevel: state.InternallyTested,
			DisproofPath: "drive the system past every shed threshold and check what is " +
				"dropped; then do it on real infrastructure",
			Environment: "in-process, synthetic load",
			Scope:       "the priority classes and their thresholds",
			Evidence: []state.Evidence{internalEvidence("AE-RESIL-1",
				"SAFETY has no shed threshold and the breaker admits one probe in half-open",
				"pkg/resilience")},
			Limitations: []string{"no real load has ever been applied"},
			Controls:    []string{"CTL-RESILIENCE"}, Gates: []string{"G6"},
			Debts: []contract.ID{"ED-006"}, Implementer: implementer, At: assessedAt},

		{ID: "AC-WORKLOAD-IDENTITY", Subject: "identity",
			Assertion:     "a workload's identity is attested rather than asserted",
			RequiredLevel: state.ExternallyValidated, CurrentLevel: state.Specified,
			DisproofPath: "present a forged workload identity and check whether it is accepted",
			Environment:  "none: SPIFFE ids are validated syntactically and nothing attests them",
			Scope:        "nothing is established beyond string validation",
			Limitations: []string{
				"the code says so explicitly: SPIFFE validation here is syntactic only, and " +
					"attestation is gate G7",
			},
			Controls: []string{"CTL-IDENTITY"}, Gates: []string{"G7"},
			Debts: []contract.ID{"ED-001"}, Implementer: implementer, At: assessedAt},
	}
}

// GateRefs maps the twenty production gates onto the controls they
// rest on, so a gate can be walked down to evidence rather than
// checked off.
func GateRefs() []GateRef {
	ext := state.ExternallyValidated
	ops := state.OperationallyProven
	return []GateRef{
		{ID: "G1", Title: "HSM/KMS production tenancy", Controls: []string{"CTL-KMS"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G2", Title: "Live commercial data contract",
			Controls: []string{"CTL-RIGHTS", "CTL-RESOLUTION"}, RequiredLevel: ext, Mandatory: true},
		{ID: "G3", Title: "Multi-region infrastructure", Controls: []string{"CTL-DR"},
			RequiredLevel: ops, Mandatory: true},
		{ID: "G4", Title: "Independent penetration test",
			Controls:      []string{"CTL-POLICY", "CTL-TENANT", "CTL-IDENTITY"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G5", Title: "Physical multi-host qualification",
			Controls: []string{"CTL-LEDGER", "CTL-RESILIENCE"}, RequiredLevel: ops, Mandatory: true},
		{ID: "G6", Title: "72-hour soak", Controls: []string{"CTL-RESILIENCE", "CTL-LEDGER"},
			RequiredLevel: ops, Mandatory: true},
		{ID: "G7", Title: "SPIFFE/mTLS production attestation", Controls: []string{"CTL-IDENTITY"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G8", Title: "Vulnerability feed and dependency scan", Controls: []string{"CTL-SUPPLY"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G9", Title: "Independent real-world corpus", Controls: []string{"CTL-REDACT"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G10", Title: "External evidence-provider validation",
			Controls: []string{"CTL-PROVENANCE", "CTL-ANCHOR"}, RequiredLevel: ext, Mandatory: true},
		{ID: "G11", Title: "Disaster recovery test", Controls: []string{"CTL-DR"},
			RequiredLevel: ops, Mandatory: true},
		{ID: "G12", Title: "Restore and replay verification",
			Controls: []string{"CTL-REPLAY", "CTL-DR"}, RequiredLevel: ops, Mandatory: true},
		{ID: "G13", Title: "Key compromise simulation", Controls: []string{"CTL-KMS"},
			RequiredLevel: ops, Mandatory: true},
		{ID: "G14", Title: "Tenant isolation test", Controls: []string{"CTL-TENANT"},
			RequiredLevel: ops, Mandatory: true},
		{ID: "G15", Title: "Cross-tenant exfiltration test", Controls: []string{"CTL-TENANT"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G16", Title: "Agent tool abuse test", Controls: []string{"CTL-FIREWALL"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G17", Title: "Prompt injection test",
			Controls: []string{"CTL-FIREWALL", "CTL-AI-LADDER"}, RequiredLevel: ext, Mandatory: true},
		{ID: "G18", Title: "Data poisoning test",
			Controls: []string{"CTL-PROVENANCE", "CTL-RESOLUTION"}, RequiredLevel: ext, Mandatory: true},
		{ID: "G19", Title: "Supply-chain dependency security", Controls: []string{"CTL-SUPPLY"},
			RequiredLevel: ext, Mandatory: true},
		{ID: "G20", Title: "Model regression qualification", Controls: []string{"CTL-AI-LADDER"},
			RequiredLevel: ext, Mandatory: true},
	}
}

// VeriqoGraph builds the consolidated graph.
func VeriqoGraph() (*Graph, error) {
	return New(Controls(), Claims(), Debts(), GateRefs())
}

// AssessedAt is the date the register was last assessed.
func AssessedAt() time.Time { return assessedAt }
