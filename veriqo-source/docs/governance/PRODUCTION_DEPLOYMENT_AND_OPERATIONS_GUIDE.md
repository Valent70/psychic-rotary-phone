# Production Deployment & Operations Guide

Consolidates the Round 4 work order's deployment guide, operational
runbook, release governance, data governance and entrypoint-
consolidation deliverables into one document, grounded entirely in
gates this repository already registers and can be re-run by any
operator.

## 1. Entrypoint consolidation

`canonical_execution_entrypoint_coverage` (`CORE_KERNEL`,
`VERIFIED_INTERNAL`) is the enforcement mechanism: every governed
decision must flow `Entrypoint -> Lifecycle -> Execution Engine ->
Policy -> Evidence -> Decision -> Verification` through the **one**
execution engine (`internal/entrypoints.Audit`). The gate's own exit
criteria — "parallel governed execution paths = 0 and no entrypoint-
matrix row claims a canonical path its own source does not take" —
scanned 304 production source files this round and found zero
violations.

The work order's "consolidate into 3 tiers" instruction is already this
repository's structure, not a restructuring to perform:

| Tier | Entry surface | Examples |
|---|---|---|
| **Operator CLIs** | `cmd/veriqo-*` binaries, each a thin wrapper over one library call | `cmd/veriqo-readiness` (release gate), `cmd/veriqo-dossier-verify` / `cmd/veriqo-insurance-cold-replay` (insurance verification), `cmd/veriqo-node` (the production node binary) |
| **Programmatic API** | `pkg/lifecycle.Orchestrator.RunUnified`, `pkg/insurance/api.Facade` | the one production entrypoint every CLI and every test ultimately calls through |
| **Transport surfaces** | `veriqo/gateway/rest`, `pkg/transport/rafttcp` | HTTP/RPC boundaries — never a second decision engine, only marshaling into/out of Tier 2 |

No new consolidation code was needed this round; `canonical_execution_
entrypoint_coverage` already proves the property structurally, on every
`cmd/veriqo-readiness` run.

## 2. Release governance

Every release binds a `ReleaseCertificate` (`internal/assurance/
manifest.go`) to:

- an exact `GitCommit` and source-tree hash (`internal/sbom`, gate
  `assurance_self`'s own sibling checks) — closing the specific defect
  a prior audit found (`SBOM.json` hard-coding a stale version and
  `vcs.commit "unknown"` forever after);
- the `AcceptanceManifest` hash it was assessed against;
- a signature verified against a registered, trusted signing key
  (`docs/governance/TRUSTED_SIGNING_KEYS.json`) via
  `ReleaseCertificate.VerifyTrusted` — never merely
  self-consistency-checked.

`traceability_matrix` (`RELEASE_GOVERNANCE`, `VERIFIED_INTERNAL`)
enforces that `docs/governance/REQUIREMENT_TRACEABILITY_MATRIX.md` is
**generated** from `requirements.json`, never hand-edited — a
requirement cannot silently drift out of sync with its own tracking
document.

`external_harness_capability_coverage` (`RELEASE_GOVERNANCE`,
`VERIFIED_INTERNAL`) requires every one of the eight externally-blocked
gates to declare, capability by capability, exactly what its harness
exercises and what only a real environment can supply — the mechanism
behind every "Internal drill performed / What remains" row in
`SECURITY_QUALIFICATION_PACK.md`.

**Release checklist** (every item independently re-runnable):

1. `go build ./...`, `go vet ./...`, `gofmt -l .` — zero output.
2. `go test ./...` — all green (`unit` gate).
3. `go test -race -count=1 ./...` for consensus-critical packages
   (`race` gate; `./scripts/verify.sh` runs a 5x repeat).
4. `go run ./cmd/veriqo-readiness -out READINESS_MANIFEST.json` — read
   the printed verdict and the `temporary_production_readiness.verdict`
   key; a release ships only when that verdict is
   `TEMPORARY_PRODUCTION_READINESS_CANDIDATE` (never a fabricated
   `PRODUCTION_QUALIFIED`) and no gate is `NOT_READY`.
5. Sign the certificate with a registered key; verify with
   `ReleaseCertificate.VerifyTrusted` before publishing.

## 3. Data governance

`data_governance` (`RELEASE_GOVERNANCE`, `VERIFIED_INTERNAL`) enforces
retention, legal hold, redaction and purge across the platform. Inside
the insurance domain specifically:

- `pkg/insurance/preservation` — every case's evidence sits under a
  preservation order recording trigger, scope, custodian, hash and a
  well-formed custody log (`insurance_preservation_chain_integrity`).
- `pkg/insurance/evidence.Record.Rights` reuses `pkg/evidence/
  provenance.RightsState` (`UNKNOWN_PENDING_CONTRACT` through
  `REVOKED`/`EXPIRED`) as the single rights vocabulary for what VERIQO
  may currently do with a record — never a second, insurance-local
  rights model.
- `pkg/insurance/evidence.Record.CorroborationStatus` (added this
  round, `corroboration.go`) surfaces `REVOKED` and `SUPERSEDED` as
  first-class corroboration facts, derived from the same `Rights`/
  `CorrectionSuperseded` fields — so a purge or a correction is never
  invisible to a reader of a record's corroboration standing.

## 4. Operational runbook

### Starting a node

`cmd/veriqo-node` is the production binary. It requires a `-config`
pointing at a real environment configuration; there is no default
production configuration checked into this repository (matching
`hsm_kms`/`spire_mtls`'s own honest external-dependency status — a real
deployment needs real key material and a real SPIFFE trust domain that
cannot be shipped as a repository default).

### Health and readiness

`internal/assurance` is the single source of truth for "is this release
ready" — never a bespoke healthcheck. An operator asks the same
question a release asks: run `cmd/veriqo-readiness` and read
`temporary_production_readiness.verdict` plus the
`blocked_external_mandatory` list.

### Incident response

1. Reproduce first: this codebase's own DARI discipline
   (Deterministic, Auditable, Replayable, Immutable) means almost every
   incident should be reproducible via cold replay
   (`cmd/veriqo-insurance-cold-replay` for the insurance domain,
   `cmd/veriqo-cold-replay` for the core execution DAG/identity ledger
   — two distinct tools; do not confuse them, see
   `CASE_ROOM_AND_DOSSIER_VERIFIER_SPECIFICATION.md`).
2. Never patch data in place. `pkg/insurance/evidence`'s
   `CorrectionSuperseded`/`SupersededBy` pattern (and
   `party.Relationship.Revoke`, which preserves history rather than
   deleting it) are the only sanctioned ways to correct a recorded
   fact — a future correction never rewrites historical truth.
3. Escalate a genuine external blocker (`BLOCKED_EXTERNAL` in the
   canonical taxonomy) to the owning team named in
   `SECURITY_QUALIFICATION_PACK.md` / `REAL_WORLD_INSURANCE_NETWORK_
   REPORT.md`'s "what remains" columns — never attempt to close it with
   a simulator or a self-attestation; `pkg/governance/qualification`'s
   trust model refuses exactly that.

### Observability

`OBSERVABILITY` category gates (`observability`, `telemetry_leakage_
zero`, `telemetry_export_pipeline_internal`, `correlation_propagation_
coverage`, `telemetry_production_coverage`) are all `VERIFIED_INTERNAL`.
`telemetry_production_coverage` specifically proves production domain
engines (`pkg/moat`, `pkg/kernel`, `pkg/engine`, `pkg/consensus`,
`pkg/execution`, `pkg/governance`, `pkg/authz`, `pkg/identity`,
`pkg/transport`, `pkg/storage`, `veriqo/core`) actually call
`telemetry.StartSpan` — not merely that the telemetry package's own
tests pass — so a correlation ID traced through an incident is a real
signal, not an assumption.
