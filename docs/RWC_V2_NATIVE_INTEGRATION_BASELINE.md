# RWC v2 Native Integration Baseline

Round R22. This document records what actually existed on THIS branch
(`claude/close-all-package-gaps-np2kj3`) before RWC v2 was wired into it, what
had to be built, and — just as importantly — what was deliberately *not* built.
Every claim below cites a real file in this tree. Source comments in `pkg/rwc`
refer to this document by section number.

## 0. Provenance of the port, stated up front

RWC v2's domain content (the port constraint table, the vessel/port constraint
evaluator, the IMO check-digit validator, the MMSI MID table, and both corpus
case sets) was authored on a sibling branch,
`claude/l99-veriqo-development-8hmx00`. That branch and this one share only an
empty initial commit; they are two independent rewrites of VERIQO. Nothing was
merged or cherry-picked. The domain files were copied and every call site was
re-bound to this branch's own APIs.

The sibling branch also carried four RWC v2 documents. They are **not** ported,
because they cite file:line locations in a different tree and make claims about
a different implementation. Importing them would have been importing assertions
about code that does not exist here. Two of them (a blocker reassessment and a
self-audit) are superseded by this branch's own round-R23 independent audit, in
`docs/RWC_V2_INDEPENDENT_AUDIT_REPORT.md` and
`docs/RWC_V2_NATIVE_EXECUTION_PROOF.md`.

## 1. No live data path exists on this branch either

The constraint that shaped RWC v2's design on the originating branch holds here
for a different and stronger reason: this branch *does* have a real external
ingestion architecture, and that architecture refuses to pretend.

- `pkg/connector/aisstream` is a real AIS adapter. It parses a real wire schema
  and canonicalizes it through the audited contract hop (`ontology.New`), and it
  stamps every record `OriginClass: provenance.OriginRealExternalUnverified`
  (`pkg/connector/aisstream/client.go:295`). It needs live network egress, which
  this environment does not have.
- `pkg/evidence/provenance` models rights explicitly. A source being technically
  reachable never implies a `RightsState` above `UNKNOWN_PENDING_CONTRACT`; that
  upgrade "is a business/legal act, never a code path"
  (`pkg/evidence/provenance/provenance.go:65-67`).
- `internal/nobypass.ingestionConstructors` enumerates every file permitted to
  perform the adapter hop: the facade plus exactly five audited connectors. The
  list is machine-checked over the whole tree.

**Consequence for RWC v2, and the one design decision that follows from it.**
The originating branch shipped a `pkg/maritime` fixture-file-backed AIS evidence
source, used to supply RWC-001's logical tick. That package was deliberately not
ported. Adding a second, fixture-only maritime evidence source alongside
`pkg/connector/aisstream` is precisely the parallel ingestion path
`internal/nobypass` exists to prevent, and it would have made the bundle look
like it had consulted an AIS feed when it had not. RWC-001's tick stays
caller-injected (`baseTick = 1` in `cmd/veriqo-rwc-v2/main.go`), and the missing
live feed is reported as an open external dependency.

## 2. The canonical path RWC v2 attaches to

RWC v2 introduces no engine. It builds inputs for the path that already exists
and reads its real outputs:

| Stage | Package | Entry point |
|---|---|---|
| Composition root | `veriqo/kernel` | `New()` (`veriqo/kernel/kernel.go:112`) |
| Intent / plan / lifecycle | `pkg/lifecycle` | `Orchestrator.RunUnified` (`pkg/lifecycle/lifecycle.go:452`) |
| Entity resolution | `pkg/identity` | `Resolver.Merge` / `EntityIDAt`, via `resolveCanonicalEntity` |
| Execution DAG | `pkg/execution` | `Engine.Run` (`pkg/execution/execution.go:615`) |
| Canonical MOAT chain | `pkg/canonical` | `Pipeline.RunCanonical` (`pkg/canonical/canonical.go:253`) |
| Independent replay | `pkg/replay` | `Record` / `NewReplayPackage` / `Engine.Replay` |
| Case lineage | `pkg/lineage` | `Ledger.Attach` / `FromCorrelation` / `Completeness` |
| Correlation | `pkg/platform/correlation` | `Key`, produced by `RunUnified` |
| Evidence envelope | `pkg/governance/envelope` | `Envelope.Validate` / `Validator.Check` |

`RunUnified` does not call `RunCanonical` directly — it runs the canonical chain
*through* `pkg/execution.Engine`, which is what calls `RunCanonical`. Any
description of RWC v2's path that omits the execution DAG is wrong; the DAG's
`ExecutionRootHash` is a real artifact of every case and is recorded per case in
the bundle.

## 3. What the two branches independently converged on

The port required no redesign because the following already existed here with
matching shapes: `moat/decision.Policy` / `FactorWeight` / `FlagThreshold` /
`EscalateThreshold`; `canonical.CaseInput.Policy`; `canonical.Pipeline.RunCanonical`;
`replay.Engine`; `kernel.New()`; `lifecycle.Orchestrator.RunUnified`; and the
factor name `"tbml_composite_risk_score"` that `VesselPortPolicy` binds to
(`pkg/moat/intelligence/risk/risk.go:244`).

What differed, and had to be adapted:

- `lifecycle.Result` on this branch carries `Execution *execution.Result`,
  `Correlation correlation.Key`, `CaseID lineage.CaseID`,
  `LegacyIdentityFallbackUsed`, `HumanReviewRequired` and `UnmappedAliasKinds`.
  RWC's `CaseResult` reads all of these; the originating branch had none of them.
- `pkg/maritime` does not exist here (see §1).
- Two governance gates apply here that do not exist there — see §5.

## 4. What was genuinely new work

- **Vessel/port physical constraint modelling.** `pkg/moat/domain/maritime`
  models vessel/port *entities*, ownership graphs and relations. It has no LOA,
  draft, gear, crane or grab fields at all, so `pkg/rwc/constraints.go` is new
  domain logic. It feeds the native decision engine as a computed factor; it does
  not replace it.
- **A PASS/FAIL/CONDITIONAL/INSUFFICIENT_EVIDENCE vocabulary.** No such decision
  class exists elsewhere in this repository; `decision.Action` is
  MONITOR/FLAG/ESCALATE. `InterpretVerdict` projects constraint findings onto the
  corpus's vocabulary. Read its doc comment before relying on it — the projection
  is this package's arithmetic, and the native decision is used to cross-check
  it, not to produce it.
- **A claim/corroboration vocabulary.** `pkg/moat/provenance.ProvenanceStatus`
  answers "do these sources share a declared upstream" and
  `pkg/evidence/provenance.OriginClass` answers "how far from a rights-cleared
  external source is this record". Neither answers "has anything independent of
  the claimant confirmed this claim", which is what RWC-002 needs.

## 5. Governance gates the port had to satisfy

Both are whole-tree scanners that fail the build, not conventions.

- **`internal/nobypass`** — no second evidence authority
  (`fusion.NewEngine`, `contradiction.NewArbitrationEngine`,
  `canonical.NewPipeline`) and no second ingestion path (`ontology.New`) outside
  audited allowlists. `pkg/rwc` calls none of the four and is on none of the
  allowlists. The corpus reaches evidence the way every other caller does: by
  building `canonical.SourceSubmission` values that `RunCanonical` submits to the
  one shared `fusion.Engine`.
- **`internal/entrypoints`** — no parallel governed-decision path, and an
  audited entrypoint matrix. The first port tripped two markers and both were
  fixed by removing the marker rather than widening an allowlist:
  `pkg/rwc/replay.go` returns a named zero certificate instead of constructing a
  `replay.VerificationCertificate{}` literal, and `rwc.CaseResult` does not copy
  the DAG's root hash onto a field of its own.

  One allowlist entry *was* added, deliberately and visibly:
  `cmd/veriqo-rwc-v2/main.go` declares `ExecutionRootHash` on the record it writes
  into the evidence bundle. That is the read-and-report shape the gate already
  allows for `veriqo/gateway/rest/lifecycle_route.go`. The field could have been
  named something shorter and slipped past the textual scanner silently; naming it
  honestly and taking the allowlist entry is the outcome the marker exists to
  produce.

  `cmd/veriqo-rwc-v2` is also registered in the matrix as the **second**
  `GovernedDecisions: true` entrypoint in this repository, because it genuinely
  originates governed decisions through `RunUnified`. The former
  `TestExactlyOneEntrypointProducesGovernedDecisions` is replaced by
  `TestGovernedDecisionEntrypointsAreExactlyTheAuditedSet`, which pins the exact
  set rather than a count.

## 6. Zero dependency

RWC v2 is pure domain logic and arithmetic. `go list -m all` still reports
exactly one module, `veriqo`. The `zero_dependency` gate is untouched.

## 7. What this baseline does not establish

This document describes wiring. It makes no claim about what the corpus
*proves*. That question is answered, adversarially and with code-level evidence,
in `docs/RWC_V2_NATIVE_EXECUTION_PROOF.md` and
`docs/RWC_V2_INDEPENDENT_AUDIT_REPORT.md`, which are the authoritative
statements and which correct several things a reader might otherwise infer from
the vocabulary used here.
