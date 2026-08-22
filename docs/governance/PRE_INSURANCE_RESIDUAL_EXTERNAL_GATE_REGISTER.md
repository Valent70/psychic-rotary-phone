# Pre-Insurance Closure Program — Residual External Gate Register (R20)

Everything that remains genuinely external after this program, with the
four-axis separation PHASE E3 (P0-8) introduced.

**None of these gates was advanced by this program.** Their status
strings and blocker reasons in `cmd/veriqo-readiness` are byte-for-byte
unchanged. What changed is that their engineering and internal
qualification progress is now *reported* instead of being hidden behind
a single word — and `TestAxisSeparationNeverAdvancesTheGateItself`
proves that reporting change moves no gate, no assessment and no
release verdict.

## How to read the axes

| Axis | Question it answers | Values it can take |
|---|---|---|
| **Engineering** | Does the code exist, and does its own harness pass? | PASS / FAIL / NOT_RUN |
| **Internal** | Has it been qualified as far as *this* environment honestly allows? | INTERNAL_QUALIFIED / FAIL / NOT_RUN / NOT_APPLICABLE |
| **External** | Has real external evidence qualified it? | EXTERNAL_QUALIFIED / BLOCKED_EXTERNAL / NOT_APPLICABLE |
| **Final** | The composed answer | READY / NOT_READY / BLOCKED_EXTERNAL / WAIVED |

The composition rule is one line, in `axes.go`: **whenever External is
BLOCKED_EXTERNAL, Final is BLOCKED_EXTERNAL**, however green the first
two are. That is the whole point of separating them. An operator can now
tell a gate that is blocked because the code is missing from a gate that
is blocked because a purchase order is missing — a distinction the
single-status model destroyed.

`INTERNAL_QUALIFIED` is **never** a synonym for VERIFIED or QUALIFIED. A
container drill is real evidence about the harness and no evidence at
all about production.

---

## The eight mandatory blocked gates

| Gate | Engineering | Internal qualification | External dependency | Required evidence | Owner |
|---|---|---|---|---|---|
| **scale_qualification** | PASS — `pkg/blockers/scale` runs a real node provider, a real exactly-once integrity check with deliberate loss/duplication injection, at the full literal 1,000,000-record target | INTERNAL_QUALIFIED — 100 genuinely separate Docker containers over real HTTP, 1M records, zero lost, zero duplicated (`evidence/scale_qualification-multi-container-drill.txt`) | Real distinct physical infrastructure. All 100 containers ran on **one physical host**. | p50/p95/p99 latency and throughput at 100 nodes across genuinely separate hosts/datacenters, signed by a benchmark lab registered in `TRUSTED_EVIDENCE_PROVIDERS.json` | Infrastructure / procurement |
| **multi_region_dr** | PASS — `pkg/blockers/dr` models regions as real raftlite nodes on a partition-capable transport, fails the leader, measures RTO/RPO, heals, confirms convergence | INTERNAL_QUALIFIED — 3 real `cmd/veriqo-node` containers on a real bridge network, real `docker network disconnect`, ~500 ms RTO, RPO=0, healed and independently verified (`evidence/multi_region_dr-multi-container-drill.txt`) | Real cross-datacenter WAN infrastructure: WAN latency, partial partitions, real cloud regions, traffic-manager cutover | A DR drill against real cloud regions with measured RPO/RTO, provider-signed | Infrastructure / procurement |
| **soak_72h** | PASS — `pkg/blockers/soak` has Start/Checkpoint/Monitor/DetectLeak/DetectDrift/Finalize with a hash-chained, tamper-evident checkpoint sequence, run identity binding, and restart/resume | INTERNAL_QUALIFIED — one genuine unbroken 60-minute run, zero errors (`evidence/soak-60min-run-log.txt`); 2,188 iterations over 90 s with flat goroutines in the standing smoke pass | A host that can honestly stay up for 72 continuous hours. This is a property of the environment, not of the harness. | A 72-hour continuous run with stable memory, goroutines and ledger integrity — `VERIQO_SOAK_MINUTES=4320` against the same unchanged test on a long-lived host | Operations |
| **hsm_kms** | PASS — `pkg/blockers/hsmkms` proves every failure mode (unavailable, timeout, permission-denied, wrong-key, revoked) fails closed, with the revoked case proving the provider is never touched; `keys.RequireProductionSafe` refuses software-backed providers under `env=production` | NOT_RUN — there is no in-sandbox analogue of a paid tenancy that would mean anything | A real HSM or cloud-KMS tenancy. Re-tested against real AWS KMS this round rather than assumed: AWS itself rejected the placeholder credentials with `UnrecognizedClientException` (`evidence/hsm_kms-real-credential-retest.txt`). Additionally constrained by the `zero_dependency` gate, which rules out adding a cloud SDK. | A `KeyProvider` backed by a real HSM/KMS, exercised through the existing failure matrix, provider-signed | Security / procurement |
| **spire_mtls** | PASS — `pkg/blockers/spiffe` qualifies client-side validation with real X.509 chain verification and a full negative matrix (expired, revoked, untrusted CA, claimed-vs-actual mismatch) | INTERNAL_QUALIFIED — a real 3-container SPIRE cluster with node-scoped isolation and live per-node revocation (`evidence/spire_mtls-multi-container-integration.txt`), plus real SVIDs loaded by `pkg/transport/rafttcp` for a real end-to-end mTLS handshake (`evidence/spire_mtls-rafttcp-live-integration.txt`) | A **production node attestor** — cloud-instance-identity, k8s-PSAT or TPM. `join_token` is a test/demo attestor. Also open: `rafttcp`'s hard-coded `veriqo.global` trust domain, recorded as configurable-trust-domain follow-on work. | Node A and node B attesting, rotating and revoking identities under a production attestor | Platform / infrastructure |
| **pentest** | PASS — `pkg/blockers/pentest` runs real adversarial probes (JWT alg=none, unknown kid, sandbox path traversal, authz wildcard escalation) against this codebase's own production `pkg/api` / `pkg/kernel/sandbox` / `pkg/authz`, with a real release-identity preflight | NOT_RUN — independence is the requirement; no self-run probe can satisfy it by construction | An independent security vendor. This is categorically unsatisfiable from inside the repository. | A signed report from an external security vendor, validated through `pkg/governance/qualification` against a registered provider | Security |
| **supply_chain_scan** | PASS — real `go list -m all` dependency discovery, a policy engine, and a real HTTP-backed `VulnerabilityProvider`. SAST is fully closed: `gosec` and `staticcheck` both run clean, 0 findings (`evidence/supply_chain_scan-gosec-full.txt`) | INTERNAL_QUALIFIED — the pipeline is proven against a local server, since the real endpoints are network-blocked here | A reachable vulnerability database. `vuln.go.dev`, `osv.dev` and the GitHub advisory API all return **403** under this environment's explicit organization egress policy — re-probed directly and via the proxy's own status endpoint (`evidence/supply_chain_scan-vulndb-network-retest.txt`). A policy denial, not a code gap. | `govulncheck` against a reachable feed, reporting clean | Security / IT policy |
| **live_data** | PASS — `pkg/connector/{aisstream,sar,bol,insurance,payment}` each parse a real wire schema, structurally validate, and canonicalize into `ontology.Evidence`; `pkg/blockers/livedata` adds content-hash dedup and a proven anti-replay defence across all four source types, refusing any SIMULATED connector's record tagged LIVE | INTERNAL_QUALIFIED — full pipeline proven end to end against SIMULATED-mode connectors, all deterministic and seeded | Commercial data contracts with AIS/BoL/SAR/commodity-trade providers. A procurement and legal action. **Explicitly excluded from this programme's scope by standing operator directive.** | Ingest qualified against contracted live feeds, with a rights state above `UNKNOWN_PENDING_CONTRACT` granted through a real `GrantTrust` call | Commercial / legal |

**Final axis for all eight: `BLOCKED_EXTERNAL`.**
**Release verdict: `NOT_PRODUCTION_READY`**, and correctly so — a single
blocked mandatory gate makes the whole verdict not-ready, by design.

---

## Non-gate residuals introduced or made visible by this program

These are not readiness gates. They are honest ceilings this program
recorded in code so they cannot be quietly overstated later.

| Item | Engineering status | Internal qualification | External dependency | Required evidence | Owner |
|---|---|---|---|---|---|
| **Telemetry export pipeline** (`telemetry.PipelineQualification()`) | PASS — conversion, redaction at the boundary, delivery, persistence, query by trace and by execution id, loud failure on nil sink / closed exporter | INTERNAL_QUALIFIED — in-process collector proves the semantics | A real OTLP collector endpoint and a deployment that exports to it under production load | Conformance against a real collector; real network delivery with backpressure, retry and partial-batch failure; retention/cardinality/query performance at production volume | Platform / operations |
| **Sandbox confinement** (`sandbox.Qualification()`) | PASS — policy engine denies every escape vector; enforcer refuses a policy whose primitive is absent; supported-but-unapplied is reported NOT closed | INTERNAL_QUALIFIED — kernel primitives probed from `/proc` and `/sys`; refusal logic proven | A production kernel and an adversarial execution drill | A genuinely hostile binary run under the enforcer attempting each of the seven vectors, with the kernel's refusals captured | Security / platform |
| **Full filesystem confinement** (`pivot_root` into a minimal rootfs) | NOT_RUN — deliberately not attempted | NOT_RUN | — | Engineering work, honestly open. `pkg/kernel/plugin`'s own doc comment records why it was not attempted: risking breaking arbitrary plugin binaries that need host shared libraries | Platform |
| **User-namespace UID/GID remapping** | NOT_RUN — probed and reported, not applied | NOT_RUN | — | Engineering work, honestly open | Platform |
| **Allowlist-style seccomp profile** | NOT_RUN — the shipped filter is a denylist, by a documented and deliberate risk trade-off recorded in `cmd/veriqo-plugin-shim` | INTERNAL_QUALIFIED (denylist) | — | Engineering work, honestly open. The denylist trade-off is recorded, not hidden: getting a denylist wrong means "not yet more restrictive than today", never "silently breaks a legitimate plugin" | Platform |
| **Real calibration corpus** (`calibration.StatusExternalDataRequired`) | PASS — Dataset, Fit, deterministic Holdout, held-out Evaluation, CalibratedModel binding all real and tested | NOT_APPLICABLE — a fixture corpus exercises every line and the status still reports `EXTERNAL_DATA_REQUIRED`, by design | A real corpus of genuinely investigated, ground-truth-labeled historical events | Either a commercial data-and-labeling contract, or years of this system's own resolved case history. An operator declares it `REAL_INVESTIGATED` with a named owner; no code can. | Data / commercial |
| **Build provenance contextual fields** | PASS — storage contract complete and content-addressed | NOT_APPLICABLE — builder identity, workflow identity, base image digest, base image SBOM and OS package inventory are honestly UNKNOWN for a non-CI, non-container build | A CI or container build that can answer them | A provenance record produced by the CI workflow, with `BuilderFromEnvironment` / `WorkflowFromEnvironment` populated and a base image digest attached | Release engineering |
| **Independent-builder reproducibility on a different provider/OS lineage** | PASS — bit-for-bit equality across two independently-provisioned ephemeral VMs (`.github/workflows/reproducible-build.yml`) | INTERNAL_QUALIFIED — both runners are GitHub-hosted `ubuntu-latest` on the same toolchain vendor | A second CI provider or a different OS lineage | An identical binary hash produced on a genuinely independent provider | Release engineering |

---

## What would move any of this

Nothing in this repository. Every row above names a specific real-world
resource — money, a contract, physical infrastructure, an independent
third party, a long-lived host, an egress policy change, or a labeled
historical dataset. Each is a procurement, legal or operational action.

When real evidence does arrive, the path is already built and already
tested: package it as a `governance/envelope.Envelope`, sign it with a
provider and reviewer registered in
`docs/governance/TRUSTED_EVIDENCE_{PROVIDERS,REVIEWERS}.json`, and
submit it through `envelope.Validator.Submit`. It will be checked
against this exact release's commit, source hash, binary hash and
artifact root, its expiry, its provider's authorization for that
specific gate, and both signatures — and it will stop at
`EVIDENCE_VALIDATED`, because advancing a gate past that point is an
operator decision with a named person behind it, not something the
validator does on its own.
