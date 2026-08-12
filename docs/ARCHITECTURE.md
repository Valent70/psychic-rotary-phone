# Veriqo Kernel — Architecture

This document describes the layering, data flow, and design invariants
of the Veriqo Go kernel as it exists in this repository. It is written
to be read by an investor's technical diligence team or an incoming
engineer, and it is deliberately honest about what is implemented versus
what is scaffolded/open — see `README.md` for the full gap-mapping table
against every audit document this codebase has been reviewed against.

## Layering

```
cmd/veriqo-demo          investor-facing CLI: runs the full pipeline as a
cmd/veriqo-node          checkpointed multi-agent workflow
                          single-node cluster member binary (raftlite + KG)

pkg/workflow              Planner / Scheduler / Executor / Checkpoint /
                           Recovery — orchestrates the layers below as a
                           deterministic, resumable multi-step run

pkg/moat/...               "MOAT" intelligence layers (reusable domain
                            libraries — no infra logic lives here):
  fusion                    multi-source evidence fusion + truth arbitration
  fusion/hbayes             hierarchical Bayesian provider-correlation model
  contradiction             truth & contradiction intelligence (conflict
                             graph, confidence evolution, source ledger)
  temporal                  bitemporal knowledge graph (valid-time +
                             transaction-time, time-travel queries)
  causal                    causal graph, multi-hop Why(), WhatIf()
  decision                  weighted-factor policy engine + utility/EV/
                             multi-objective + explainable % breakdown
  digitaltwin                per-entity living state + future simulation +
                             economic impact projection
  kg                         deterministic hash-chained knowledge graph
  domain/maritime            typed maritime ontology (11 entity kinds)

pkg/consensus/raftlite     leader election, log replication, atomic
                            membership change (ProposeJointConfChange)
pkg/transport/rafttcp       real TCP + mTLS + SPIFFE-format identity wire
                             transport for raftlite
pkg/transport/flowcontrol   credit-based inflight window + adaptive batching

pkg/platform/audit          append-only, hash-chained audit log
pkg/platform/config          zero-dependency YAML-subset config loader
pkg/platform/observability   Prometheus-text metrics exporter
pkg/platform/telemetry       tracing seam (NoopTracer default, real OTel
                              plugs in via SetGlobalTracer with zero
                              call-site changes — see OBSERVABILITY.md)
```

## Data flow (the "WOW demo" chain)

```
multi-source evidence
   -> fusion.Engine.Submit / Arbitrate      (truth per single claim)
   -> contradiction.Engine.Ingest           (conflict graph, confidence
                                              evolution, source ledger)
   -> hbayes.Model.ComputeEventRisk         (correlation-aware fused risk,
                                              not naive noisy-OR)
   -> temporal.Engine.Assert / AsOf         (bitemporal fact history,
                                              corrections without loss)
   -> causal.Graph.Why / WhatIf             (multi-hop root cause,
                                              counterfactual)
   -> decision.Engine.Decide +
      decision.ExplainBreakdown +
      decision.ComputeUtility               (risk score, action tier,
                                              % explanation, expected value)
   -> digitaltwin.Registry (state) +
      digitaltwin.Simulate (future) +
      digitaltwin.ComputeEconomicImpact      (living entity model, future
                                               trajectory, economic exposure)
```

Every arrow above is a real, tested Go call — see
`test/integration/wow_demo_test.go` for the executable proof, and
`test/integration/dark_vessel_test.go` for the original narrower chain.

## The canonical path (v7.10.2)

The chain above ("WOW demo") and `veriqo/core/intelligence.Loop`'s own
loop (Contradiction -> Knowledge -> Causal -> Learning -> Prediction)
were, until this version, two separately-proven but never-unified
paths — the V7.10.1 whole-repo technical audit's top P0 finding.
`pkg/canonical.Pipeline.RunCanonical` is now the single composed path:
Evidence (Fusion) -> Provenance (computed, never caller-asserted
independence) -> Truth Arbitration -> Contradiction Ingest -> Causal
aggregate support -> Risk (`ScoreWithProvenance`) -> Decision ->
DigitalTwin (attribute + risk + causal projection) -> Economic Impact
-> one `CanonicalCertificate` covering the whole chain, hash-verifiable
end to end. `veriqo/kernel.Kernel.Canonical` wires it with the same
shared `TrustCalculus` every other kernel engine uses.
`cmd/veriqo-gateway` now boots through `veriqo/kernel.New` (the single
composition root) rather than constructing its own standalone
registry, so the OS-level "which path does a real request take"
ambiguity the audit named is closed at the process level; exposing a
dedicated REST route for `Kernel.Canonical` is the next incremental
step (currently reachable programmatically, not yet over HTTP).

## Domain scope (positioning note, v7.10.2)

Everything in this repository — evidence fusion, truth arbitration,
provenance, causal analysis, risk scoring, decision policy, digital
twin, ownership/UBO graphs — is domain-agnostic MOAT infrastructure.
`pkg/moat/domain/maritime` is the one CORE, built-out domain model
(vessel/ownership/sanctions/trade-finance entities) this version
ships and tests against end to end. Any other vertical mentioned in
earlier strategy discussions (energy, banking, insurance,
port/cargo-terminal operations, etc.) is ADJACENT / FUTURE scope: the
underlying engines would apply to them without modification (they are
not maritime-specific), but no domain ontology, entity set, or
integration test for those verticals exists in this repo today. This
note exists so the domain-scope claim in any external-facing material
matches what the code actually proves, not what the platform could
plausibly extend to.

## Design invariants (enforced across every kernel package)

- No `time.Now()` — every event carries an explicit logical `Tick`.
- No UUIDs — every ID is content-addressed (SHA-256 of canonical bytes).
- Append-only logs; no mutation of a past record.
- Deterministic replay: `Rebuild`/`ReplayAll` on an independently-built
  engine from the same log produces a byte-identical chain head hash,
  regardless of map-iteration order (every canonical encoding sorts
  keys explicitly).
- Every non-trivial decision carries an explanation trail, not just a
  number.
- Zero external Go dependencies (`go list -deps` audited every session;
  the only non-stdlib entries are Go's own internal vendoring of
  `golang.org/x/crypto`/`x/net`/`x/sys` used by `crypto/tls` itself).

## What this architecture is, and isn't, today

**Is:** a single-process-per-node reference implementation of every
MOAT intelligence layer, a real (if compact) Raft consensus engine with
atomic membership change, a real TCP+mTLS wire transport, and a
deterministic workflow orchestrator — all genuinely tested with `-race`
and property/fuzz/chaos-style tests where it matters most (durability,
consensus safety, replay determinism).

**Isn't yet:** a multi-host production deployment. See
`docs/THREAT_MODEL.md` and `docs/SLOs.md` for the explicit boundary
between "what is implemented and tested" and "what is documented as the
path to production but not executed in this sandbox" (gRPC-compatible
transport, real OTel collector wiring, SPIRE/OPA runtime enforcement,
100+ node benchmarks, Kubernetes rollout).
