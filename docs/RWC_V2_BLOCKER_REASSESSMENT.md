# RWC v2 — 8-Blocker Reassessment

Per the brief's §17: re-triage each of the 8 existing production blockers
(`docs/governance/production-blockers.json`) in light of what RWC v2
actually proved. Classification vocabulary: `CLOSED`,
`INTERFACE_QUALIFIED`, `REAL_WORLD_VALIDATED`, `SIMULATED`, `OPEN`,
`BLOCKED_EXTERNAL`.

**Rule enforced throughout**: a blocker is never marked `CLOSED` merely
because an interface exists. Every classification below cites the exact
evidence and states what is still missing.

---

## 1. `pentest` — Independent penetration test

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged by RWC v2)

- Evidence: none produced by this work. RWC v2 added new application
  code (`pkg/rwc`, `cmd/veriqo-rwc-v2`) that was not in scope of any prior
  pentest and has not been scanned or attacked by an independent vendor.
- Test: none applicable — `required_capabilities` (`target_registry`,
  `adversarial_test_harness`, `evidence_adapter`) exist in
  `pkg/blockers/pentest`, unmodified by this session.
- Exact remaining requirement: `real_world_dependencies` =
  `independent_security_vendor`, `signed_vendor_report`. Neither is
  obtainable from inside this sandboxed environment.
- Why not closed: no vendor engagement occurred; RWC v2 is new attack
  surface (10 new HTTP-reachable code paths through the existing
  `/lifecycle/run_unified` route) that, if anything, marginally *widens*
  what a future pentest needs to cover, not narrows it.

## 2. `scale_qualification` — 100-node / 1M-evidence-record scale qualification

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged)

- Evidence: RWC v2 ran 10 cases, single-process, single-node. This is
  four orders of magnitude below the 1M-evidence-record target and zero
  nodes below the 100-node target.
- Test: `pkg/blockers/scale` exists and is unmodified; RWC v2 did not
  invoke it.
- Exact remaining requirement: `real_world_dependencies` =
  `100_real_nodes_or_cloud_equivalent`. Not available here.
- Why not closed: RWC v2 demonstrates correctness of the native
  execution path, not throughput or horizontal scale — a materially
  different claim.

## 3. `multi_region_dr` — Multi-region disaster recovery qualification

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged)

- Evidence: none. RWC v2 ran entirely in one process, one region (the
  concept doesn't apply).
- Exact remaining requirement: `real_multi_region_infrastructure` — not
  present in this environment.
- Why not closed: unrelated to what RWC v2 tests.

## 4. `hsm_kms` — HSM/KMS production key custody

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged)

- Evidence: RWC v2's `pkg/rwc/identity_checks.go` performs cryptographic
  *validation* (IMO check-digit arithmetic), not key custody — unrelated
  capability. No certificate in `evidence/rwc_v2/certificates/*.json` is
  signed with an HSM- or KMS-custodied key; VERIQO's existing certificate
  hashing (SHA-256 over content) is integrity-only, not a signature.
- Exact remaining requirement: `procured_hsm_or_cloud_kms_tenancy` — not
  available here.
- Why not closed: RWC v2 neither uses nor tests key custody.

## 5. `live_data` — Live commercial data feed integration (SWIFT/AIS/SAR/BoL)

**Classification: INTERFACE_QUALIFIED** (upgraded from a purely abstract
capability claim — see below for exactly what changed and what did not)

- Evidence: RWC v2 is the first real corpus (as opposed to a synthetic/
  fixture test) to carry maritime real-world data (RWC-001's literal port
  constraints and adversarial vessel candidates) and commodity/trade-
  finance-shaped data (RWC-002's EN590 cargo, vessel identity, and
  transaction-sequence claims naming SWIFT-adjacent instruments —
  MT103/72) end-to-end through `canonical.SourceSubmission` into the real
  Fusion/Provenance/Decision/Certificate/Replay chain, with all 10 cases
  independently replay-verified (`evidence/rwc_v2/replay_results.json`).
  This is real evidence that the `feed_connector_abstraction` and
  `normalization_pipeline` capabilities this blocker names are wide
  enough to carry genuine external-shaped data, not only synthetic
  fixtures.
- Test: `TestRWC001AdversarialCandidates`, `TestRWC002ProvenanceSeparation`
  (`pkg/rwc/*_test.go`), plus the full `go run ./cmd/veriqo-rwc-v2`
  execution.
- Exact remaining requirement: `commercial_data_contracts`,
  `live_feed_credentials` — RWC v2 used brief-supplied literal data, not a
  live AIS/SWIFT/SAR feed connection. `pkg/connector/maritime.go` still
  has zero non-synthetic `Adapter` implementations; `pkg/dataplatform
  /ingest.Fetcher` still has zero implementations. No network call was
  made to MagicPort, MarineTraffic, or any commercial provider anywhere
  in this session (see `docs/VERIQO_RWC_V2_VALIDATION_REPORT.md` §11).
- Why not `REAL_WORLD_VALIDATED` or `CLOSED`: the data path is proven
  end-to-end for data that *arrives already in hand*; the still-missing
  piece is the live connector that fetches it, which is exactly the
  commercial-contract-gated piece this blocker names. `INTERFACE_QUALIFIED`
  is the accurate tier — real evidence the interface is fit for purpose,
  not a claim the live connection exists.

## 6. `soak_72h` — 72-hour continuous soak test

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged)

- Evidence: RWC v2's full corpus (10 cases) executed in under 2 seconds
  wall-clock (`go run ./cmd/veriqo-rwc-v2` timing observed this session).
  Zero hours of continuous operation were demonstrated.
- Exact remaining requirement:
  `72_continuous_wall_clock_hours_on_a_long_lived_host` — this session's
  environment is not a long-lived host suited to an uninterrupted 72-hour
  run.
- Why not closed: unrelated to what RWC v2 tests (correctness, not
  longevity/leak/drift behavior).

## 7. `spire_mtls` — Production SPIRE/mTLS workload identity

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged)

- Evidence: none. RWC v2 ran in-process, no network transport, no
  workload identity involved.
- Exact remaining requirement: `production_spire_cluster`,
  `production_node_attestor` — not available here.
- Why not closed: unrelated to what RWC v2 tests.

## 8. `supply_chain_scan` — Supply-chain security scan (SAST + dependency vulnerability DB)

**Classification: OPEN / BLOCKED_EXTERNAL** (unchanged; RWC v2's own code
was checked by what IS available offline, documented honestly below)

- Evidence: `pkg/rwc` and `cmd/veriqo-rwc-v2` were checked with what this
  sandbox can run offline — `go vet ./...` (clean), `gofmt -l .` (clean),
  and the repo's zero-external-dependency invariant check in
  `scripts/verify.sh` step 4/6 (clean — RWC v2 imports only existing
  `veriqo/...` packages and the Go standard library `crypto/sha256`,
  `encoding/json`, `encoding/hex`, `sort`, `strconv`, `strings`, `context`,
  `fmt`, `os`, `path/filepath` — no new third-party dependency was added).
  This is real evidence but explicitly NOT what this blocker requires.
- Exact remaining requirement: `network_access_to_vulnerability_databases`
  — `golangci-lint`, `govulncheck`, and `gosec` all require fetching from
  proxy.golang.org or a vulnerability database this sandbox's network
  policy blocks (confirmed by `scripts/verify.sh`'s own "NOT run by this
  script" section, unmodified, still accurate after this session).
- Why not closed: SAST and CVE-database lookups genuinely did not run.

---

## Summary table

| Blocker | Status | Changed by RWC v2? |
|---|---|---|
| pentest | OPEN / BLOCKED_EXTERNAL | No |
| scale_qualification | OPEN / BLOCKED_EXTERNAL | No |
| multi_region_dr | OPEN / BLOCKED_EXTERNAL | No |
| hsm_kms | OPEN / BLOCKED_EXTERNAL | No |
| live_data | **INTERFACE_QUALIFIED** | **Yes — upgraded from untested claim to evidenced interface fitness** |
| soak_72h | OPEN / BLOCKED_EXTERNAL | No |
| spire_mtls | OPEN / BLOCKED_EXTERNAL | No |
| supply_chain_scan | OPEN / BLOCKED_EXTERNAL | No |

**0 of 8 blockers are CLOSED.** 1 of 8 (`live_data`) has new, real,
citable evidence supporting a more specific interim classification than
before this session; the other 7 are entirely outside what a sandboxed,
network-restricted environment processing brief-supplied fixture data can
affect, and are reported as such rather than glossed over.
