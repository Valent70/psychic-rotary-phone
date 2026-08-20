# RWC v2 Native Integration Baseline

Status: mapped from the actual VERIQO Enterprise Kernel OS v7.12.1(18) source
tree (`go.mod` module `veriqo`), imported unmodified in commit
`eca7208 "Import VERIQO Enterprise Kernel OS v7.12.1(18) baseline"`. Every
claim below cites a real file:line in that commit. No code has been written
against this baseline yet — this document exists so implementation starts
from what is actually there, not from what the brief assumed was there.

**Correction to the brief's premise, stated up front:** the brief says
"Existing RWC v1 implementation is already present." It is not.
`grep -rn "RWC" --include=*.go` and equivalent searches across the entire
tree return zero matches. There is no RWC v1 corpus, no RWC decision engine,
no RWC fixture data anywhere in this repository. RWC-001 and RWC-002 are
both new work. This is stated honestly here rather than silently assumed
away, per the brief's own rule in §19 ("undocumented assumptions").

---

## 1. Existing native ingestion path

No real-world data ingestion adapter exists, by explicit design:

- `pkg/dataplatform/ingest/ingest.go:1-15` — generic ingestion envelope
  (`RawRecord`, `SourceType` enum including `AIS, SAR, RF, OSINT, DOCUMENT,
  SATELLITE_IMAGERY, BILL_OF_LADING, WEATHER, INSURANCE, SWIFT_MESSAGE,
  CORRESPONDENT_BANKING, PORT_AUTHORITY`), `Fetcher interface { Name()
  string; Fetch() ([]RawRecord, error) }` (`:82-85`). Doc comment states
  outright: "deliberately does NOT implement any real connection to AIS,
  SWIFT, satellite providers... those require commercial data licenses."
  Zero implementations of `Fetcher` ship.
- `pkg/connector/maritime.go:1-23` — maritime-specific adapter seam,
  `Adapter interface { Fetch(ctx) ([]Observation, error); Name() string }`
  (`:60-63`). The only implementation is `SyntheticReplayableAdapter`
  (`:77`), which generates seeded-random fake IMO numbers via `math/rand`
  and tags every record `Provenance.Source = "synthetic:<seed>"` so it can
  never be mistaken for live data. Doc comment: "this sandbox has network
  egress only to developer-tooling domains... there is no reachable path to
  any real AIS/SAR/satellite provider."

**Consequence for RWC v2**: this session also has no live network path to
MagicPort, MarineTraffic, or any AIS provider. RWC-001 and RWC-002 input
must be the literal corpus data supplied in the brief, entered as a fixture
— not fetched. This is consistent with the brief's own §5 instruction ("If
external data is unavailable during execution, do not invent it. Represent
missing corroboration explicitly.").

**The real entry point for new evidence** is not `ingest.Fetcher` (unused
by the canonical path) but `pkg/canonical.CaseInput` /
`canonical.SourceSubmission` (`pkg/canonical/canonical.go:64-117`) — see §2.
These types are already domain-agnostic (`Subject`, `Predicate`, `Value`,
`SourceID` are all plain strings), so RWC-001/002 data can be submitted
through the existing canonical pipeline without a new engine.

## 2. Existing UCR path (Unified Contract Registry)

The brief's "UCR" maps onto `veriqo/contracts/*.yaml` (7 files: `evidence,
intent, policy, reasoning, replay, trust, verification`). Each file's
header states the principle directly (`trust.yaml:1-7`): "This file is the
single source of truth: `veriqo/generator` reads it and emits the CLI
subcommands and Go SDK bindings... one contract, many derived artifacts."
`veriqo/generator/cmd/gen/main.go:29-41` loads all contracts and generates
CLI (`veriqo/cli/generated_commands.go`), Go/Python/TS SDKs, and REST routes
(`veriqo/gateway/rest/generated_routes.go`) — all marked
`// Code generated ... DO NOT EDIT`.

Important qualification: this registry is a **method/RPC surface contract**,
not a payload-schema validator. `Param.Type` is only `string | float | uint`
(`veriqo/contracts` schema via `veriqo/generator/contract.go:41,60`) — no
nested objects. Richer records are passed as JSON-string params, exactly
as `evidence.yaml`/`verification.yaml` already do.

A new contract file can be added purely additively: `contract.go:73-99`
(`LoadContracts`) globs every `*.yaml` in the directory independently and
sorts by engine name — dropping in a new file does not require touching the
existing 7. This satisfies the brief's "V2 must be additive" principle at
the contract layer.

**Architecturally significant finding** (documented in
`docs/ARCHITECTURE.md:147-208` of the repo itself, not something this
survey discovered): `veriqo/registry.Registry` (built from these contracts)
is explicitly **not** the path that produces governed decisions. Quote:
"None of them, alone or chained by a caller, produces a governed decision:
none returns a `certificate_hash`, `decision_id`, or `execution_root_hash`."
This is enforced by a real test,
`veriqo/gateway/rest/architecture_test.go`'s
`TestRegistryNeverProducesGovernedDecisionArtifacts`. So UCR/Registry is
real, but RWC-001/002 — which must produce a decision and a certificate —
cannot go through it as the decision path. It is available for
administrative/inspection use (e.g. exposing individual RWC evidence nodes
via `EvidenceAPI.AddNode`) but is not where the PASS/FAIL/CONDITIONAL
decision is produced.

## 3. Existing UER path (Unified Execution Runtime)

The one canonical execution API, per `docs/ARCHITECTURE.md:167-168`
("Lifecycle/Execution is the ONE canonical execution API for governed
decisions"):

```
pkg/lifecycle.Orchestrator.RunUnified(ctx, Intent, EvidencePlan, canonical.CaseInput)
  -> pkg/execution.Engine.Run(ctx, execution.Input)      [18-stage DAG]
       -> pkg/canonical.Pipeline.RunCanonical(actorID, CaseInput)  [called once]
```

`RunUnified` signature: `pkg/lifecycle/lifecycle.go:412`. HTTP entry point:
`POST /lifecycle/run_unified`, registered in
`veriqo/gateway/rest/lifecycle_route.go:148-155`, using the real inbound
request context (`:221`), not `context.Background()`.

The 18 DAG stages, in dependency order (`pkg/execution/execution.go:128-156,
449-467`), each hashed and topologically ordered deterministically (Kahn's
algorithm + lexicographic tie-break, `:544-587`):

`INTENT → EVIDENCE_INGESTION → IDENTITY_RESOLUTION → DEPENDENCY_EVALUATION
→ TRUTH_ARBITRATION → CONTRADICTION_ARBITRATION → CORRELATION_FUSION →
TEMPORAL_BAYESIAN → CAUSAL_REASONING → RISK → POLICY → DECISION →
TRUST_STATE → DIGITAL_TWIN → ECONOMIC_CONSEQUENCE → EXPLANATION →
REPLAY_PACKAGE → VERIFICATION_CERTIFICATE`

Input: `execution.Input{Context, Case canonical.CaseInput, Scenarios,
Currency, TrustSubject, IdentityAliases, TemporalModel, ...}`
(`execution.go:326-381`). `Context.Tick uint64` is the injected logical
clock — no `time.Now()`, no UUID anywhere in this file (verified by
`internal/determinism.Check`, see §12).

**Naming trap for implementation**: `veriqo/kernel.Kernel.Execution`
(`veriqo/kernel/kernel.go:52`) is a *different, older* package
(`veriqo/core/execution`), not `pkg/execution.Engine`. The real 18-stage DAG
engine is reachable as `Kernel.Lifecycle.Execution` (a field inside
`pkg/lifecycle.Orchestrator`). RWC v2 code must construct/use
`Kernel.Lifecycle.RunUnified`, not `Kernel.Execution`.

## 4. Existing Evidence path

Two layers, not one:

- **DAG-level (what RWC-001/002 actually go through)**: `canonical.CaseInput
  .Submissions []canonical.SourceSubmission` (`canonical.go:64-79`) —
  `SourceID, Value, Provider, UpstreamID string; BaseReliability float64;
  Dependencies []evidencegraph.DependencyRecord`. Domain-agnostic, no fixed
  type enum.
- **Facade-level (for direct evidence inspection/API use)**:
  `pkg/evidence/api.Facade` (`pkg/evidence/api/api.go:98-128`) wraps
  `canonical.Pipeline`, `identity.Resolver`, `replay.Engine`,
  `contradiction.ArbitrationEngine`. Its typed unit is
  `ontology.Evidence` (`pkg/evidence/ontology/ontology.go:156-194`), one of
  **16 fixed types** (`:53-70`: `TypeObservation, TypeMeasurement,
  TypeDocument, TypeStatement, TypeSensorObservation, TypeAISObservation,
  TypeSARObservation, TypeOpticalObservation, TypeRegistryRecord,
  TypeTradeRecord, TypeFinancialRecord, TypeIdentityAssertion,
  TypeDerivedInference, TypePrediction, TypeOutcome, TypeCorrection`). Doc
  comment: "The full set is fixed by the audit; adding one is a
  schema-version event" (`:49-50`). **No `VESSEL_IDENTITY`,
  `CARGO_IDENTITY`, `DOCUMENT_EXISTENCE_CLAIM`, etc. exist as first-class
  types.**

**Gap for §7 of the brief** ("Each important real-world assertion must
become typed evidence... VESSEL_IDENTITY, CARGO_IDENTITY, ... "): the fixed
`ontology.Type` enum does not have these categories. Two ways to satisfy
the requirement without forking a duplicate evidence engine: (a) encode the
category in `Subject`/`Predicate` naming (e.g.
`Predicate="VESSEL_IDENTITY"`) on existing `ontology.Evidence`/
`canonical.SourceSubmission` records, which is queryable and auditable
without touching the enum; or (b) bump `SchemaVersionV1` and add the
constants, which the code frames as a deliberate, auditable act. This
baseline recommends (a) — smallest necessary, no core file touched — for
the RWC v2 adapter, and documents it as a design choice, not silently.

## 5. Existing Trust path

Three distinct trust mechanisms coexist, one of which is shared
cross-system:

- `pkg/core/trustcalc.Calculus` (`trustcalc.go:279`) — Bayesian
  Beta-Bernoulli posterior per subject. `Observe(subject, matched bool,
  weight float64)` (`:339`), `Score(subject) float64` (posterior mean,
  `:371`). This is the "one shared belief object" wired into both
  `veriqo/core/evidence.Engine` and `veriqo/core/intelligence.Loop`
  (`trustcalc.go:25-32`). Backed by `pkg/core/trustcalc/ledger.go` — a
  hash-chained, **in-memory, non-durable** append log (`Entry{Seq, Subject,
  Matched, Weight, PrevHash, Hash}`, `ledger.go:44-52`), with
  `VerifyChain()` (`:130`) and `Replay(...)` (`:161`) proven to reproduce
  the identical `Belief` by test.
- `pkg/trust.Kernel` (`pkg/trust/trust.go:110`) — rule-weighted
  `TrustScore` against a `TrustPolicy`, independent metric bag input.
- `pkg/trust/state.Engine` (`pkg/trust/state/state.go`) — named state
  machine: `Level ∈ {Unknown, Provisional, Trusted, Degraded, Suspect,
  Revoked}` (`:42-48`), this is what the DAG's `TRUST_STATE` stage
  (execution.go stage 13) attributes to.

None of the three implements a per-claim `CLAIMED/CORROBORATED/UNVERIFIED/
CONTRADICTED` state — see §6.

## 6. Existing Knowledge path

**Not wired into the 18-stage canonical DAG.** Three separate knowledge
subsystems exist as standalone packages, none appearing among the 18 DAG
stages listed in §3:

- `veriqo/core/knowledge.Store` — fact/triple store with Bayesian belief
  revision (`Assert`, `Revise`, `PropagateConfidence`).
- `pkg/governance/knowledge.Engine` — governance layer above it: a
  proposal state machine (`DRAFT → VALIDATED → ANALYZED → SIMULATED →
  APPROVED → ACTIVE / REJECTED / SUPERSEDED / REVERTED`).
- `pkg/moat/kg.Graph` — deterministic hash-chained property graph
  (`UpsertNode/UpsertEdge`, `ReplayAll`, `RootHash`).

**Gap, stated honestly**: the brief asks for a "Knowledge path" to be
"actually exercised" as part of the native RWC decision flow. It cannot be,
because Knowledge is not part of `RunUnified`'s DAG today — wiring it in
would be a DAG-topology change to `pkg/execution.execution.go`'s stage
graph, which this baseline treats as out of the "smallest necessary
adapter" scope (§2 of the brief: "Do not duplicate existing core
functionality" cuts both ways — it also means don't rewire the core DAG for
one corpus). RWC v2's implementation will call `pkg/moat/kg.Graph` directly
as an **additive, side-channel** step (recording resolved RWC entities into
the same deterministic hash-chained graph type the rest of the system
uses) so the claim "Knowledge path was exercised" is true and
code-verifiable, while being explicit in the auditor report that this is
parallel to, not inside, `RunUnified`'s DAG.

## 7. Existing Intelligence/decision path

`pkg/moat/decision.Engine.Decide(actorID, subject, policyName string,
values map[string]float64, tick uint64) (Decision, error)`
(`decision.go:171`) — `riskScore = Σ(weight_i·value_i)/Σ(weight_i)`,
thresholded against `Policy.FlagThreshold`/`EscalateThreshold`
(`:209-215`).

**Critical gap, the largest one found**: `decision.Action`
(`decision.go:37-44`) has exactly three values:

```go
const (
    ActionMonitor  Action = "MONITOR"
    ActionFlag     Action = "FLAG"
    ActionEscalate Action = "ESCALATE"
)
```

There is **no PASS/FAIL/CONDITIONAL/INSUFFICIENT_EVIDENCE** anywhere in the
decision engine or any package upstream of it (`pkg/moat/intelligence/risk`
has its own separate `LOW/MEDIUM/HIGH/CRITICAL` label, unrelated). The
brief's §9 requires exactly these four decision classes. This is exactly
the case the brief's §2 anticipates: "If any existing component cannot
support the path, implement the SMALLEST necessary adapter/interface
extension... Do not duplicate existing core functionality."

**Design decision for RWC v2** (recorded here, applied in implementation):
do not fork `decision.Engine` or add a second decision engine. Instead:
1. Model RWC-specific facts (LOA fit, draft fit, gear requirement,
   documentation completeness, etc.) as weighted factors fed into the
   *existing* `decision.Engine.Decide` via `values map[string]float64`, so
   `RiskScore` is computed by the real engine, not a parallel one.
2. Add a small, explicitly-labeled **RWC domain interpretation function**
   that maps `(decision.Action, hard-constraint violations, evidence
   completeness)` onto `{PASS, FAIL, CONDITIONAL, INSUFFICIENT_EVIDENCE}`.
   This function contains no reasoning of its own — it is a deterministic,
   auditable projection of what the native engine already decided plus
   which named hard constraints (e.g. `LOA > 150m`) were violated, which
   is itself computed from evidence, not asserted. This is the "smallest
   necessary adapter" the brief explicitly permits, and it is documented
   here rather than silently added.

## 8. Existing Lifecycle/certificate path

`pkg/lifecycle.LifecycleCertificate` (`lifecycle.go:180-199`):

```go
type LifecycleCertificate struct {
    IntentID, EntityID, EvidencePlanHash string
    UnmetRequirements  []EvidenceRequirement
    Canonical          canonical.CanonicalCertificate  // nested cert, has its own .Hash
    IVFVerified        bool
    IVFCertificateHash string
    ReplayID           string // = Canonical.Hash
    ExecutionRootHash  string
    Hash               string  // sha256 over every field above
}
```

`VerifyCertificate` (`:249-254`) independently recomputes and compares.
Field-name mapping against the brief's §10 requirements: `execution_id` →
`execution.Context.ExecutionID` (surfaced in `Result.Execution`);
`input_hash` → closest existing analogue is `EvidencePlanHash`;
`canonical_hash` → `Canonical.Hash`; `certificate_hash` →
`LifecycleCertificate.Hash`; `ledger_anchor` → **does not exist**, see §9.

## 9. Existing Ledger path

**Honest finding, stated directly because the brief explicitly forbids
papering over this**: no ledger *anchoring* (durable, external, or
cross-restart persistence of a hash chain) exists anywhere in this
codebase. `grep -rn "anchor|Anchor"` across the repository returns only
prose ("anchored to a specific identity-ledger state") — no `Anchor()`
function or field exists.

What does exist and is real, working, tested code: several **independent,
in-memory, hash-chained** ledgers, each with `VerifyChain()`/`Replay()`:
`pkg/core/trustcalc.Ledger`, `pkg/governance/lifecycle.Registry`'s event
log, `identity.Resolver`'s ledger, `pkg/moat/fusion.Engine`'s
`FusionRecord` chain, `pkg/moat/contradiction.ArbitrationEngine`'s
`TruthVersion` chain. Durable persistence exists only for
`veriqo/registry.Registry`'s own state, via `pkg/storage.FileStore`
(crash-atomic snapshot, not a chain). `pkg/storage/wal` is a fully built
and separately-tested write-ahead log with **zero production call sites**
anywhere in the repository (`grep -rn "veriqo/pkg/storage/wal"
--include=*.go` returns nothing).

**Consequence for RWC v2 and the brief's §11**: RWC-001/002 certificates
can carry a `ledger_anchor` field populated with the relevant hash-chain
head (e.g. `trustcalc.Ledger` head, or `identity.Resolver.Ledger().Head()`)
as evidence the record is chained and `VerifyChain()`-provable — but this
must not be labeled "anchored to production ledger infrastructure," because
no durable/external anchoring exists. The auditor report (§16 of the
brief) will state this exactly; this is the honest, non-fabricated answer
to "was the ledger used."

## 10. Existing Replay path

Real, tested, and the strongest-proven part of the system.
`pkg/replay.Engine`:
- `Record(actorID, in canonical.CaseInput, res *canonical.CanonicalResult,
  depLedger, identityLedgerHead) (ExecutionRecord, error)` (`replay.go:188`)
  — computes 12 independent per-stage fingerprints (Evidence, Dependency,
  Provenance, Arbitration, Contradiction, Causal, Risk, Decision, Twin,
  Economic, Certificate, Identity), folded into `OriginalResultHash`.
- `NewReplayPackage(...)` (`:295`), then `Engine.Replay(pkg) →
  VerificationCertificate` (`:319-366`) — builds a **fresh**
  `canonical.Pipeline` (no shared pointer with the original run), re-runs
  `RunCanonical`, and asserts `cert.Match = (ReplayResultHash ==
  OriginalResultHash)`.
- Proven by existing tests: `TestFullLifecycleReplayMatches`
  (`replay_test.go:43-59`), `TestReplayIsDeterministicAcrossEngines`
  (`:170-184`), plus tamper-detection tests that flip one field and assert
  a specific `DivergedStage`.
- Cross-process variant: `cmd/veriqo-cold-replay/main.go` — a genuinely
  separate compiled binary reading only an exported JSON file from disk,
  comparing `OriginalRootHash` vs `ReplayRootHash`, exit code 0/1/2.

This is the mechanism RWC v2's §12 (deterministic replay, P0) and §13
(adversarial/mutation replay) will use directly — no new replay engine
needed.

## 11. Existing extension points (summary)

1. `canonical.CaseInput`/`SourceSubmission` — the real, already
   domain-agnostic input to the DAG. **Primary integration point.**
2. `pkg/dataplatform/ingest.Fetcher` — adapter interface for a future live
   feed; not needed for RWC v2 since input is corpus-supplied, not fetched.
3. `identity.Kind` — fixed enum, already includes `KindIMO`, `KindMMSI`,
   `KindCallsign`, `KindFlag`, `KindPort`, `KindOrganization`, etc.
   (`pkg/identity/identity.go:52-77`) — sufficient for RWC-001/002 vessel
   and entity identity without extension.
4. `veriqo/contracts/*.yaml` — additive; a new `rwc_maritime.yaml` can be
   added without editing the existing 7, for administrative/inspection
   exposure of RWC results (optional, not required for the decision path).
5. `pkg/moat/domain/maritime` — ownership/sanctions entity/relation
   ontology; confirmed by grep to have **zero** LOA/draft/geared/crane/beam/
   DWT fields — RWC-001's port/vessel physical-constraint matching is new
   domain logic, not an extension of existing fields.

## 12. Gaps preventing native RWC integration (consolidated)

| # | Gap | Severity | Planned resolution |
|---|---|---|---|
| G1 | No PASS/FAIL/CONDITIONAL/INSUFFICIENT_EVIDENCE decision class exists (`decision.Action` = MONITOR/FLAG/ESCALATE only) | High | Smallest-necessary RWC-domain interpretation function over the real engine's output + evidence-derived hard-constraint checks (§7 above) |
| G2 | No vessel/port physical-constraint modeling (LOA, draft, geared, cranes) in `pkg/moat/domain/maritime` | High | New RWC-scoped Go types/logic; feeds `decision.Engine` as weighted factors, does not replace it |
| G3 | No CLAIMED/CORROBORATED/UNVERIFIED/CONTRADICTED provenance-status enum anywhere | Medium | Derived classification computed from existing `TruthVersion.Confidence/Contradiction`, `contradiction.Record.ContradictionScore`, `ontology.EpistemicClass` — new vocabulary, not a new belief system |
| G4 | `ontology.Type` evidence taxonomy is fixed/closed; no VESSEL_IDENTITY/CARGO_IDENTITY/etc. | Medium | Encode category via `Subject`/`Predicate` naming on existing types rather than editing the fixed enum |
| G5 | No ledger anchoring (durable/external) exists anywhere | Medium | Use existing in-memory hash-chain heads as `ledger_anchor`, documented honestly as chain-integrity evidence, not production anchoring |
| G6 | Knowledge subsystems not wired into the 18-stage DAG | Medium | Exercise `pkg/moat/kg.Graph` as an additive side-channel, documented as parallel to (not inside) `RunUnified` |
| G7 | No real-world network egress / no live ingestion adapter | Low (expected) | RWC-001/002 use corpus-supplied fixture data per the brief's own §5 instruction |
| G8 | `pkg/kernel/policy` DSL's `evalCondition` only implements eq/neq/in, not lte/gte | Low | Avoided — port/vessel numeric constraints implemented as Go-level RWC logic feeding `decision.Engine` factors, not as DSL rules; DSL left untouched |
| G9 | `veriqo/kernel.Kernel.Execution` field name collides conceptually with `pkg/execution.Engine` but is a different, older package | Low (footgun, not a gap) | Implementation uses `Kernel.Lifecycle.RunUnified` explicitly, never `Kernel.Execution` |

No implementation has begun as of this document. Task tracking (§2 onward
of the brief) proceeds from here.
