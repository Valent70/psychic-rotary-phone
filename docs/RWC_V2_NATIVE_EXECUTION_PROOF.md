# RWC v2 — Native Execution Proof

Round R23, section 1 of the independent audit. This document traces the actual
runtime calls a single RWC v2 case makes, stage by stage, and states for each
one: the exact package and function, the source file, whether it really
executed, whether it was mocked, whether it was bypassed, and whether its output
affected the final decision.

No claim here is made without code-level evidence. Where the evidence is a
committed artifact rather than a source line, the artifact is named and the
value quoted.

The companion document `docs/RWC_V2_INDEPENDENT_AUDIT_REPORT.md` carries
sections 2–12 and the final verdict.

---

## 0. How the evidence was obtained

Three independent sources, cross-checked against each other:

1. **The source tree**, read directly.
2. **The committed DAG traces** in `evidence/rwc_v2/replay_requests/*.json`.
   Each carries `committed_trace.nodes`, one node per DAG stage, with a
   per-stage `status` and `detail` recorded by the engine at run time and
   covered by the execution root hash. A stage's status here is the engine's own
   statement about itself, not this auditor's inference.
3. **A genuinely separate OS process** — `cmd/veriqo-cold-replay`, separately
   compiled, reading nothing but those JSON files — rebuilding each DAG from
   scratch and comparing node hashes.

---

## 1. The one structural fact a reader must understand first

`pkg/execution.Engine.Run` calls `Pipeline.RunCanonical` **once**, up front, at
`pkg/execution/execution.go:651`:

```go
canon, canonErr := e.Pipeline.RunCanonical(ctx.Actor, in.Case)
```

Everything the canonical MOAT chain computes — dependency evaluation, fusion,
arbitration, contradiction, provenance independence, risk, decision, digital
twin — happens inside that single call, in
`pkg/canonical/canonical.go:253-398`. The DAG stages that follow are
**attestation nodes over that already-computed result**: each reads a field of
`canon`, records inputs, outputs and a detail string, and commits an artifact
hash into the node's own hash.

`case StageTruthArbitration` (`execution.go:797`) is representative:

```go
a := canon.Arbitration
record(id, []string{in.Case.Subject + "/" + in.Case.Predicate},
    []string{a.Winner}, StatusOK, "winner "+a.Winner+" at "+..., 
    canon.Certificate.ArbitrationHash, nil)
```

This is real and it is load-bearing — the artifact hash is what makes tampering
detectable, and the cold replay in §4 below proves it — but it is **not** a
second, independent invocation of the arbitration engine.

Four stages are exceptions that do perform work of their own:
`IDENTITY_RESOLUTION` (re-resolves an identifier against the ledger),
`TEMPORAL_BAYESIAN` (calls `hbayes.Model.Infer` when supplied a model),
`TRUST_STATE` (calls `Trust.StateAt` when supplied a subject), and
`ECONOMIC_CONSEQUENCE` (computes a distribution when supplied scenarios).

**A description of RWC v2 as "eighteen engines each independently executing" is
wrong.** The honest description is: one canonical chain executes, and eighteen
DAG nodes commit to what it produced, four of them adding a computation of their
own. This distinction is not a criticism of the architecture — it is what makes
the execution root hash meaningful — but it changes what "the DAG ran" proves.

---

## 2. Stage-by-stage trace

Status column: **RAN** = executed and produced a committed artifact;
**RAN (independent)** = performed a computation not derived from `canon`;
**SKIPPED** = the engine recorded `StatusSkipped` for every case in the corpus.

Nothing in the table is mocked. There is no stub, no fake, and no test double
anywhere on this path; the corpus's *inputs* are hand-entered real-world figures
(see the audit report's §10 provenance matrix), but every engine that processes
them is the production one.

| # | Stage | Package · function | File | Status | Affected the decision? |
|---|---|---|---|---|---|
| — | Composition | `veriqo/kernel.New` | `veriqo/kernel/kernel.go:112` | RAN | Indirectly — builds the one shared Pipeline, Identity and Calculus |
| — | Adapter | `pkg/rwc.Run` | `pkg/rwc/run.go` | RAN | Builds `lifecycle.Intent`, `EvidencePlan`, `canonical.CaseInput`. Calls no engine directly |
| — | Lifecycle | `pkg/lifecycle.Orchestrator.RunUnified` | `pkg/lifecycle/lifecycle.go:452` | RAN | Yes — entity resolution, plan enforcement, then the DAG |
| — | Entity resolution | `pkg/identity.Resolver.Merge` / `.EntityIDAt` | `pkg/identity/identity.go:394,428` | RAN | Yes — sets `CaseInput.Entity`/`.Subject`, which key the fusion claim |
| — | Canonical chain | `pkg/canonical.Pipeline.RunCanonical` | `pkg/canonical/canonical.go:253` | RAN | Yes — this is where the decision is actually computed |
| 1 | `INTENT` | `execution.go` `case StageIntent` | `pkg/execution/execution.go:724` | RAN | No — commits the intent identity |
| 2 | `EVIDENCE_INGESTION` | `case StageEvidenceIngestion` | `:748` | RAN | No — commits the submission count |
| 3 | `IDENTITY_RESOLUTION` | `case StageIdentityResolution` → `Identity.EntityIDAt` | `:754` | **RAN (independent)** | Yes, fail-closed: a mismatch between the re-resolved entity and `Case.Entity` fails the stage (`ErrIdentityMismatch`) |
| 4 | `DEPENDENCY_EVALUATION` | `case StageDependencyEvaluation` over `Pipeline.resolveDependencies` | `:789` / `pkg/canonical/dependency.go:125` | RAN | Yes — the effective source weights it produces are the **only** weights fusion is given |
| 5 | `TRUTH_ARBITRATION` | `case StageTruthArbitration` over `fusion.Engine.Arbitrate` | `:797` / `pkg/moat/fusion/fusion.go:275` | RAN | Yes — the arbitration winner |
| 6 | `CONTRADICTION_ARBITRATION` | `case StageContradiction` over `contradiction.Engine.Ingest` | `:805` | RAN | Yes for provenance status; no for the risk score |
| 7 | `CORRELATION_FUSION` | `case StageFusion` over `provenance.Graph.Assess` | `:811` | RAN | Yes — independence feeds the risk discount |
| 8 | `TEMPORAL_BAYESIAN` | `case StageTemporal` | `:820` | **SKIPPED** (all 10 cases) | No |
| 9 | `CAUSAL_REASONING` | `case StageCausal` over `causal.Graph.AggregateSupport` | `:857` | RAN, aggregate support 0 | No — RWC supplies no `CausalLinks` |
| 10 | `RISK` | `case StageRisk` over `risk.Model.ScoreWithProvenance` | `:863` / `pkg/moat/intelligence/risk/risk.go:125` | RAN | **Yes — this is the score the decision is made from** |
| 11 | `POLICY` | `case StagePolicy` | `:870` | RAN | Yes — binds `rwc001_vessel_port_suitability_v1` |
| 12 | `DECISION` | `case StageDecision` over `decision.Engine.Decide` | `:955` / `pkg/moat/decision/decision.go:171` | RAN | **Yes — this IS the decision** |
| 13 | `TRUST_STATE` | `case StageTrust` | `:962` | **SKIPPED** (all 10 cases) | No |
| 14 | `DIGITAL_TWIN` | `case StageDigitalTwin` over `digitaltwin.Registry` | `:975` | RAN | No — the twin is updated *by* the decision |
| 15 | `ECONOMIC_CONSEQUENCE` | `case StageEconomic` | `:981` | **SKIPPED** (all 10 cases) | No |
| 16 | `EXPLANATION` | `case StageExplanation` → `Engine.buildExplanation` | `:1003` / `:1079` | RAN | No — consolidates 11–12 chain links |
| 17 | `REPLAY_PACKAGE` | `case StageReplayPackage` → `replay.Record`/`NewReplayPackage` | `:1014` | RAN | No |
| 18 | `VERIFICATION_CERTIFICATE` | `case StageVerificationCertificate` → `replay.Engine.Replay` | `:1034` | RAN, "replay matched" | No |
| — | IVF | `pkg/lifecycle.buildIVFBundle` + `verification.Verifier.Verify` | `pkg/lifecycle/lifecycle.go:786,647` | RAN | No |
| — | Certificate | `pkg/lifecycle.hashLifecycleCert` | `pkg/lifecycle/lifecycle.go:259` | RAN | No |
| — | Case lineage | `pkg/lineage.Ledger.FromCorrelation` / `.Attach` | `pkg/lineage/lineage.go:368,202` | RAN | No |
| — | Independent replay | `pkg/replay.Engine.Replay` | `pkg/replay/replay.go:319` | RAN, 12 stages | No |

### The three SKIPPED stages, verbatim from the engine

Quoted from the committed traces, identical for all ten cases:

- `TEMPORAL_BAYESIAN` — *"no temporal observation series supplied for this case"*
- `ECONOMIC_CONSEQUENCE` — *"no scenario set supplied; refusing to invent a distribution"*
- `TRUST_STATE` — *"no trust engine or subject supplied"*

None of the three is a stub or a mock. Each is the engine explicitly declining
to fabricate output it has no input for, which is the correct behaviour. But
**RWC v2 does not exercise them**, and no claim about temporal Bayesian
reasoning, economic consequence modelling or trust-state evaluation can rest on
this corpus.

### Trust is not merely skipped at the DAG stage — it is absent from the whole path

This is the least flattering finding in this document and it is stated plainly.

`veriqo/kernel.New` constructs exactly one `trustcalc.Calculus`, shares it across
Evidence, Intelligence, Intent and Calibration, and hash-chains every `Observe()`
into `Kernel.TrustLedger`. An RWC case touches none of it:

- `pkg/canonical.RunCanonical` never references `Pipeline.Trust`. Verified by
  grep over `pkg/canonical/canonical.go` and `pkg/canonical/dependency.go`:
  **zero** occurrences.
- `pkg/lifecycle.RunUnified` never sets `execution.Input.TrustSubject`. Verified
  by grep over the whole tree: the only non-test writers of that field are in
  `pkg/moat/metaintelligence`, which is not on this path.
- Consequently `Kernel.TrustLedger.Len()` is **0** after a full RWC case.

That last fact is pinned by `TestAuditTrustCalculusDoesNotParticipate` in
`pkg/rwc/audit_test.go`, which fails if it ever becomes false — so this finding
cannot go stale silently.

### What "provenance" actually reported

Every one of the ten cases produced, in the DAG's own committed detail string
for `CORRELATION_FUSION`:

```
independence 1 (UNKNOWN), posterior 0.9499999999999999
```

`UNKNOWN` is `pkg/moat/provenance.StatusUnknown`, whose own documentation says it
means *"NONE of the sources have any declared ancestry at all (never
registered)... flagged UNKNOWN rather than DECLARED_INDEPENDENT so a consumer can
distinguish 'we checked and found nothing' from 'we never had data to check.'"*

The independence score of `1` that accompanies it is the trivial value
`ComputeIndependence` returns when there are no pairs to compare
(`pkg/moat/provenance/provenance.go:279-281`). A consumer reading that `1`
without reading the `UNKNOWN` beside it would conclude the sources were verified
independent. Round R23 found `ClassifyProvenance` doing exactly that and
corrected it; see the audit report's §3.

---

## 3. Where the decision actually comes from

Following the value that becomes `decision_action` in the evidence bundle:

1. `pkg/rwc.EvaluateVesselAtPort` (`pkg/rwc/constraints.go`) compares the
   declared vessel spec against the port table and produces a
   `ConstraintResult`. Pure function of `(vessel, port)`; no vessel-name or
   case-ID branching anywhere in it.
2. `ConstraintResult.ViolationRatio()` collapses that to one of `{0.0, 0.5, 1.0}`.
3. `buildVesselPortCase` puts that ratio into **both** `CaseInput.PatternScore`
   and `CaseInput.PriceAnomaly`.
4. `risk.Model.Score` computes
   `clamp01((0.6·pattern + 0.4·price) · (0.5 + 0.5·independence))`. With both
   inputs equal and independence 1.0, this is exactly the ratio. Asserted against
   the real model by `TestPolicyThresholdsMatchTheRealRiskModel`, not assumed.
5. `risk.ToDecisionValues` projects it as the single factor
   `"tbml_composite_risk_score"` (`risk.go:242`).
6. `decision.Engine.Decide` applies `VesselPortPolicy`'s thresholds (0.3 flag,
   0.7 escalate) and emits MONITOR / FLAG / ESCALATE.

Observed end to end: ratio 0.0 → MONITOR, 0.5 → FLAG, 1.0 → ESCALATE, matching
`evidence/rwc_v2/decision_results.json` for all ten cases.

**The `Verdict` (PASS/FAIL/CONDITIONAL) does not come from this chain.** It is
computed by `InterpretVerdict` from step 1's `ConstraintResult` directly. See
the audit report's §2 and §4.

---

## 4. Cross-process cold replay — performed, not asserted

`cmd/veriqo-cold-replay` was compiled separately and run as its own OS process
against each exported case, reading nothing but the JSON files:

```
veriqo-cold-replay -export evidence/rwc_v2/replay_requests/<case>.json \
                   -identity-export evidence/rwc_v2/replay_requests/<case>.identity.json
```

Result: **10 of 10 PASSED**, 18 DAG nodes compared per case, and for each the
replayed evidence root equalled the original. Identity queries were reproduced
from a `pkg/identity.Resolver` rebuilt from the exported ledger alone (1 query
for each RWC-001 case, 2 for each RWC-002 case).

The pass is not vacuous. Tampering with one field of one exported case —
`"loa_m":151` → `"loa_m":140` in `RWC-001-B` — produced:

```
original evidence root : a50092908641ccef95ee0fa49f6a0e167cf77f4017e8f425e8b3346082731e87
replayed evidence root : 2a49af9a48d8bc612092c15e3bb9445a66f5f413424e353173e23a80bd5ec1d5
divergent stage        : TRUTH_ARBITRATION
VERDICT                : FAILED
```

**This capability did not work when round R22 shipped.** The first audit attempt
found all ten exports exiting with code 2: every RWC execution goes through
`pkg/lifecycle`, which binds a live resolver to the engine, so
`IDENTITY_RESOLUTION`'s node hash carries an identity-ledger term, and the
cold-replay binary correctly *refuses* such an export without `-identity-export`
rather than replaying it into a guaranteed divergence. R22's claim that a real
cross-process replay was "reproducible by anyone" was therefore false when made.
It was corrected in R23-1 by exporting the identity ledger too, and only then did
the runs above become possible.

---

## 5. Certificates, verified independently

Every certificate in `evidence/rwc_v2/certificates/` was re-verified out of band
by recomputing its hash from its own fields:

- `lifecycle.VerifyCertificate` → valid for 10 of 10
- `canonical.VerifyCertificate` over the embedded canonical certificate → valid
  for 10 of 10
- `IVFVerified` true for 10 of 10
- `ReplayID == Canonical.Hash` for 10 of 10, as the contract requires

The certificate is minted by `pkg/lifecycle.hashLifecycleCert`
(`pkg/lifecycle/lifecycle.go:259`), called from `RunUnified` at `:664`.
`cmd/veriqo-rwc-v2` serializes `res.Lifecycle.Certificate` and computes nothing.
This is enforced structurally, not by convention: `internal/entrypoints` guards
literal construction of a `replay.VerificationCertificate` and assignment of an
`ExecutionRootHash` outside an audited allowlist, and `pkg/rwc` appears on
neither list.

---

## 6. Summary of §1's findings

| Question | Answer |
|---|---|
| Did the real kernel, lifecycle, execution DAG and canonical pipeline execute? | Yes, all of them, on every case |
| Was anything mocked or stubbed? | No |
| Was anything bypassed? | No engine was bypassed. Three DAG stages were SKIPPED for lack of input, by the engine's own decision |
| Did the DAG stages independently re-execute the engines? | No — one `RunCanonical` call, eighteen attestation nodes over it, four of which add work of their own |
| Did the native decision output affect the recorded `decision_action`? | Yes |
| Did the native decision output determine the RWC `Verdict`? | **No** — see the audit report's §2 |
| Was trust evaluated? | **No** — the shared calculus recorded zero observations |
| Was the execution reproducible in a separate process? | Yes, 10/10, and a one-field tamper is detected |
