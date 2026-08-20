# External Audit A1–A18 — Gap Mapping

Baseline for the "PART A — YANG HARUS DICODING SEKARANG" audit brief. Mapped
from the actual repository state (post RWC v2 integration,
`docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md`), via 5 parallel read-only
surveys of the real code, before any new code was written. Every claim
below cites a real file:line. This document exists so implementation
reuses what's real and never duplicates an existing engine — the same
discipline `docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md` established.

## Summary table

| Item | State | What's real today | What's genuinely missing |
|---|---|---|---|
| A1 Evidence Qualification Engine | **Missing** | Nearest neighbor: `pkg/governance/qualification` (signature verification, different purpose — `Status` enum is `BLOCKED_EXTERNAL/EVIDENCE_SUBMITTED/.../VERIFIED`, not the required vocabulary); `internal/assurance/gate.go` (release-gate rollup, not per-evidence) | The exact 12-field evidence record, READY/PARTIAL/BLOCKED/NOT_ELIGIBLE output, and the 6 named reason codes — none exist anywhere |
| A2 Data Origin Enforcement | **Partial** | `pkg/blockers/livedata.DataMode` (`SYNTHETIC/REPLAY/LIVE`, real enforced rule at `livedata.go:211-231`) | Only 3 of 5 required values; `pkg/connector/maritime.go`'s `Provenance.Source` is unenforced free text |
| A3 Source/Provider Contract | **Missing** | `ingest.RawRecord`, `connector.Observation`, `canonical.SourceSubmission` — three different partial carriers | 8 of 11 required fields (Coverage, DeliveryID, Sequence, ProviderAttestation, CorrectionPolicy, License, RedistributionRights, Auditability) absent from all three |
| A4 AIS/Port/NOR/SOF Adapter | **Missing (pipeline gap)** | `connector.Feed` does Connector→Registry→`fusion.Claim`; `evidence/ontology.Evidence` is the richer canonical-event carrier | No function converts `Observation`→`Evidence`; `location`, `delivery_id`, `sequence` absent from every type in the pipeline |
| A5 Truth Arbitration | **Strong, small gap** | `pkg/moat/contradiction/arbitration.go` — real, tested, preserves losing observations (`arbitration.go:277-279`), computes conflict/resolution/confidence | No `Reason` field; no fixed source-authority table (only per-observation reliability scalar) |
| A6 Evidence Reconciliation | **Missing** | `wal.RecoveryReport`, `scale.IntegrityReport`, `evidencegraph.RootHash()` — three partial building blocks | The exact 9-counter record and its 4 invariants don't exist anywhere |
| A7 100-Node Harness | **Weak** | `scale.RunQualification` + `cmd/veriqo-scale-node` (real HTTP, proven at 10 nodes/50K records, `evidence/scale_qualification-multi-container-drill.txt`) | No 9-field node identity struct; no seeded 1M-envelope generator; no fault injector wired in; **no `REAL_DERIVED_BENCHMARK` label anywhere** |
| A8 Fault Injection | **Mostly done** | `pkg/chaos` covers node restart, node loss, delay, duplicate, reorder(as jitter), drop, partition — 6 real named scenarios + real evidence (`evidence/chaos-acceptance.json`) | "Retry storm" and "controller restart" scenarios don't exist |
| A9 72h Soak Harness | **Mostly done** | Controller, hash-chained checkpoints, health/memory/goroutine tracking all real and tested; real unbroken 60-min run proven (`evidence/soak-60min-run-log.txt`) | Queue depth, error *rate* (only count exists), restart handling, reconciliation step all missing |
| A10 AWS KMS Abstraction | **Wrong shape** | `keys.KeyProvider` exists but is `Sign(ctx,keyID,digest)`/`PublicKey(ctx,keyID)` — a multi-key registry shape, not the required 3-method single-key shape; `Manager.Verify` hardcoded to ed25519 (`keys.go:279`) | Exact interface shape; `AWSKMSProvider`; algorithm-agnostic verification |
| A11 Trust Registry + Rotation | **Inconsistent** | `keys.State` (`PENDING/ACTIVE/RETIRED/REVOKED`) is close but has no `VERIFY_ONLY`/`EXPIRED`; `pki_rotation.go`'s CRL model **contradicts** `keys.go`'s "history stays verifiable" guarantee at the leaf level | The exact 5-state enum; reconciling the two divergent rotation semantics |
| A12 KMS Negative Tests | **9/11 covered** | Unavailable, timeout, wrong-key, tampered-manifest, tampered-evidence, invalid-signature, revoked-key, expired-trust(partial) all have real tests | "Wrong algorithm" and "missing audit reference" have zero matching tests |
| A13 Supply-Chain Pipeline | **Partial, honest** | Real SBOM (`internal/sbom`), real dependency inventory, vuln-scan framework that refuses fabricated REAL-mode evidence (`supplychain.go:224-226`) — matches the audit's own "don't fabricate" instruction already | KEV and VEX are entirely absent as concepts; OSV attempted-but-network-blocked (honestly documented); no dedicated release-manifest type |
| A14 SPIRE/mTLS Boundary | **Mostly done, misnamed** | `spiffe.IdentityProvider` (not `WorkloadIdentityProvider`) with real SVID/Rotation/Revocation/trust-decision, backed by a genuine mock CA, fully tested | No named `Attestation` type/concept at all |
| A15 DR Harness | **Mostly done** | Real failover, real recovery, real RPO/RTO measurement, correctly refuses REAL-mode/never claims PASS | No genuine snapshot+WAL replay reconstruction; no structured state-hash reconciliation (only single-key comparison) |
| A16 Independent Verifier | **Missing (marked "very important")** | `pkg/verification.Verifier` core is genuinely standalone (zero `veriqo/` internal imports, confirmed) — the right foundation | No unified `cmd/veriqo-verifier`; 3 existing CLIs only ever print binary PASSED/FAILED, never VALID/INVALID/INCOMPLETE/TRUST_REVOKED; no trust-registry/revocation concept in the verification package |
| A17 Readiness CLI | **Data exists, format doesn't** | `cmd/veriqo-readiness` never falsely prints "PASS" (confirmed — real exit codes drive everything); most of the 6 required fields' data already exists as struct fields | Never printed in the required STATUS/ACCEPTANCE CRITERION/EVIDENCE REQUIRED/EVIDENCE PRESENT/VERIFICATION RESULT/REMAINING GAP shape |
| A18 Blocker Mapping | **Missing entirely** | Per-blocker evidence *requirements* exist (`production-blockers.json`); nothing maps one evidence artifact to multiple blockers | Confirmed absent in any JSON/Go structure repo-wide |

## Priority and scope decisions

Given the scope (18 substantial items) and this environment's real
constraints (no AWS/network access, no 100-real-node infrastructure, no
72 continuous hours), implementation below follows the brief's own
instruction pattern used throughout the original brief text: **build the
harness/contract/interface now, be explicit about what still needs real
infrastructure later.** Priority order, matching value and existing
leverage:

1. **A1, A2** — foundational vocabulary other items (A17, A18) build on.
2. **A5, A8, A9** — small, well-scoped extensions to already-strong
   engines; highest confidence, lowest risk of duplicating anything.
3. **A6** — new but small, self-contained, reuses `evidencegraph.RootHash`.
4. **A16** — explicitly marked most important by the audit; the standalone
   `Verifier` core already exists, this is real, achievable work.
5. **A17, A18** — ties everything together; depends on A1 existing first.
6. **A3, A4** — substantial new work (canonical event pipeline); scoped to
   the fields that are genuinely addable without inventing new domain
   semantics.
7. **A10, A11, A12** — A10/A11 require reconciling two existing,
   semantically-inconsistent code paths, which is real, careful work; A12
   adds the 2 missing negative tests.
8. **A13, A14** — extend existing strong packages (rename/wrap
   `IdentityProvider`→`WorkloadIdentityProvider`, add `Attestation` type;
   add VEX-shaped justification export, document KEV/OSV as
   network-gated exactly like existing `govulncheck` honesty).
9. **A7, A15** — the most infrastructure-heavy items. Build the genuinely
   codeable pieces (node identity struct, seeded generator architecture,
   `REAL_DERIVED_BENCHMARK` labeling; DR reconciliation/replay
   scaffolding) without claiming a 100-node/1M-envelope run or a real
   region-loss replay actually happened in this environment — that would
   itself be exactly the fabrication this audit is checking for.

No implementation began before this document existed.
