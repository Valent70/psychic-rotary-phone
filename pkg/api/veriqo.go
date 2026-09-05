package api

import (
	"veriqo/pkg/audit"
	"veriqo/pkg/authority"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/policy"
)

// The classification floors. They are deliberately not uniform: an
// endpoint serving payments and accounts reaches material that is
// RESTRICTED and carries PERSONAL_DATA, and declaring INTERNAL for it
// would let a caller be admitted who should have been turned away
// before anything was read.
func internal() classification.Marking {
	return classification.MustNew(classification.Internal)
}

func confidential() classification.Marking {
	return classification.MustNew(classification.Confidential)
}

func restricted() classification.Marking {
	return classification.MustNew(classification.Restricted, classification.PersonalData)
}

// The VERIQO API surface.
//
// The specification lists the resources: evidence, entities,
// resolutions, claims, hypotheses, reverse-proofs, contradictions,
// independence, findings, cases, replay, qualification, policies,
// audit. Each appears here with the guarantees it must declare.
//
// Notice what is NOT here: there is no endpoint that qualifies a
// claim, approves a finding or merges two entities in one call. Those
// are authority acts with separation-of-duties requirements, and an
// API that exposed them as a single mutation would let a client
// perform a two-party act alone.

func read(resource, op string, cap authority.Capability, action string) Endpoint {
	return Endpoint{Resource: resource, Method: Get, Operation: op,
		Capability: cap, AuditAction: action, Severity: audit.Routine,
		MinClassification:  floorFor(resource),
		RateLimitPerSecond: 50, Burst: 100, Concurrency: 20}
}

// floorFor maps a resource to the least sensitive marking anything
// reachable through it can carry.
func floorFor(resource string) classification.Marking {
	switch resource {
	case "policies", "qualification", "audit", "replay":
		return internal()
	case "entities", "graph", "independence", "hypotheses", "reverse-proofs",
		"contradictions":
		return confidential()
	default:
		// evidence, claims, findings, cases, resolutions: the material
		// a case turns on, and the material most likely to carry
		// personal data.
		return restricted()
	}
}

func list(resource, op string, cap authority.Capability, action string) Endpoint {
	e := read(resource, op, cap, action)
	e.Method = List
	e.RateLimitPerSecond = 10
	e.Burst = 20
	e.Concurrency = 8
	return e
}

func write(resource, op string, cap authority.Capability, action string,
	purposes ...policy.Purpose) Endpoint {
	return Endpoint{Resource: resource, Method: Create, Operation: op,
		Capability: cap, AuditAction: action, Severity: audit.Elevated,
		Purposes: purposes, Replayable: true,
		MinClassification:  floorFor(resource),
		RateLimitPerSecond: 10, Burst: 20, Concurrency: 8}
}

func act(resource, op string, cap authority.Capability, action string,
	sev audit.Severity, purposes ...policy.Purpose) Endpoint {
	e := write(resource, op, cap, action, purposes...)
	e.Method = Action
	e.Severity = sev
	e.RateLimitPerSecond = 5
	e.Burst = 10
	e.Concurrency = 4
	return e
}

// Endpoints returns the VERIQO API surface.
func Endpoints() []Endpoint {
	investigate := []policy.Purpose{policy.CaseInvestigation}
	investigateOrRegulatory := []policy.Purpose{
		policy.CaseInvestigation, policy.RegulatoryProduction}

	return []Endpoint{
		// Evidence fabric.
		read("evidence", "version", authority.View, "evidence.version.read"),
		list("evidence", "versions", authority.View, "evidence.versions.list"),
		read("evidence", "lineage", authority.View, "evidence.lineage.read"),
		read("evidence", "custody", authority.View, "evidence.custody.read"),
		write("evidence", "acquire", authority.Propose, "evidence.acquired", investigate...),
		act("evidence", "redact", authority.Propose, "evidence.redacted",
			audit.Elevated, investigateOrRegulatory...),

		// Knowledge fabric.
		read("entities", "get", authority.View, "entity.read"),
		list("entities", "search", authority.View, "entity.search"),
		write("entities", "propose", authority.Propose, "entity.proposed", investigate...),
		read("resolutions", "get", authority.View, "resolution.read"),
		// A resolution is PROPOSED here; the merge is an approval act
		// and is not exposed as a single mutation.
		write("resolutions", "propose", authority.Propose, "resolution.proposed", investigate...),
		list("graph", "paths", authority.View, "graph.paths"),

		// Reasoning fabric.
		read("claims", "get", authority.View, "claim.read"),
		write("claims", "propose", authority.Propose, "claim.proposed", investigate...),
		act("claims", "challenge", authority.Challenge, "claim.challenged",
			audit.Elevated, investigate...),
		read("hypotheses", "matrix", authority.View, "hypothesis.matrix.read"),
		write("hypotheses", "propose", authority.Propose, "hypothesis.proposed", investigate...),
		read("reverse-proofs", "get", authority.View, "reverseproof.read"),
		write("reverse-proofs", "decompose", authority.Propose, "reverseproof.decomposed",
			investigate...),
		read("contradictions", "list", authority.View, "contradiction.list"),
		read("independence", "assess", authority.View, "independence.assessed"),

		// Qualification.
		read("findings", "get", authority.View, "finding.read"),
		// Approval is an ACTION with the highest severity, requires
		// APPROVE, and the handler enforces separation of duties.
		act("findings", "approve", authority.Approve, "finding.approved",
			audit.Security, investigate...),
		read("qualification", "status", authority.View, "qualification.read"),

		// Case room.
		read("cases", "get", authority.View, "case.read"),
		list("cases", "list", authority.View, "case.list"),
		write("cases", "open", authority.Propose, "case.opened", investigate...),
		act("cases", "resolve", authority.Approve, "case.resolved",
			audit.Security, investigate...),
		act("cases", "export", authority.Export, "case.exported",
			audit.Security, policy.CustomerExport, policy.RegulatoryProduction),

		// Replay and audit.
		read("replay", "manifest", authority.View, "replay.manifest.read"),
		act("replay", "run", authority.Review, "replay.executed",
			audit.Elevated, policy.QualityAssurance, policy.CaseInvestigation),
		list("audit", "records", authority.Review, "audit.read"),

		// Governance.
		read("policies", "get", authority.View, "policy.read"),
		act("policies", "update", authority.Administer, "policy.updated",
			audit.Security, policy.SystemMaintenance),
	}
}
