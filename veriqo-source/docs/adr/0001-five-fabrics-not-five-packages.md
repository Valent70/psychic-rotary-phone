# ADR-0001: The five fabrics are capabilities, not five new packages

**Status:** Accepted
**Enforced by:** `pkg/assurance/fabric_test.go` · `TestNoFabricHasItsOwnPackage`

## Context

VERIQO's own documents used fifteen terms for five things: Unified Evidence,
Evidence Fabric, Evidence Engine, Trust Engine, Trust Kernel, Knowledge Fabric,
Intelligent Fabric, Intelligence Layer, Qualification, EQF, Case lineage, Case
Engine, Orchestrator, Execution, workflow.

The obvious fix — create `pkg/tecp`, `pkg/eqf`, `pkg/if`, `pkg/crf`,
`pkg/fref` — would have produced five façades over capabilities that already
exist, and five more places for a second implementation of an owned decision to
hide. The architecture's own anti-duplication rule forbids exactly that.

## Decision

Freeze the vocabulary to five fabrics — TECP, EQF, IF, CRF, FREF — as
**architectural capabilities over existing packages**. Retire each ambiguous
term to exactly one fabric. Audit each fabric along eleven dimensions
(capability, canonical package, entry point, call graph, data flow, state flow,
evidence flow, test, E2E test, replay, fail-closed), where every dimension names
something real that a reviewer can follow.

`pkg/fref` is the single new package and is a **contract**, not an engine: it
owns no stage, names another package as the authority for each of its fourteen
stages, and reports a stage that ran elsewhere as drift.

## Consequences

- A test fails the build if `pkg/tecp`, `pkg/eqf`, `pkg/crf`,
  `pkg/intelligentfabric`, `pkg/trustengine` or `pkg/caseengine` appears.
- Every canonical package named in the audit is resolved against the filesystem,
  so the audit cannot point at code that is not there.
- Each fabric must name the packages that **look like** a rival authority and say
  why each is not one. Those rows are written to be contested.
- Cost: a fabric is not a compile-time entity. Nothing stops code from using
  TECP's packages without going through TECP's entry point; only the layering
  rules and the fabric audit make that visible.
