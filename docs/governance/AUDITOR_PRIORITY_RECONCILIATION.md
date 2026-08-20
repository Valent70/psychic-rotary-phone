# Auditor Priority-Phase Reconciliation (R18)

This document reconciles a fresh auditor document ("Fase Prioritas menurut
Auditor harus diselesaikan") against this repository's actual, current
state. The auditor's own document frames VERIQO's status as a split
between two independent axes:

```
ENGINEERING CORE  ~90%+ strong   (Execution, Evidence, Identity, Truth,
                                   Fusion, Bayesian, Decision, Digital
                                   Twin, Replay, Verification)
REAL-WORLD PROOF  ~40-50%        (Live AIS, Live BoL, Calibration corpus,
                                   HSM/KMS, SPIRE production, 100 nodes,
                                   Multi-region, 72h soak, Pentest,
                                   Pilot customer)
```

and lists five priority phases, in the auditor's own stated order. Each
is addressed below with a precise, checkable status — not a rhetorical
one — following this project's standing "no false green" discipline: a
claim of CLOSED must cite a real, reproducible artifact; a claim of
BLOCKED_EXTERNAL must name the specific real-world resource (money,
contract, physical infrastructure, an independent third party) that no
coding session can substitute for without fabricating evidence.

## PRIORITY 1 — Real data acquisition (AIS, BoL/customs, SAR,
commodity/trade, historical outcome data)

**Status: BLOCKED_EXTERNAL, unchanged, explicitly excluded from this
round's scope by standing operator directive.**

This is the `live_data` blocker (`pkg/blockers/livedata`), which the
operator directing this entire multi-round effort explicitly excluded
from the "close every remaining gap" mandate from the outset ("...selain
daripada real data..."). It requires genuine commercial data contracts
with AIS/BoL/SAR/commodity-trade providers — a procurement and legal
action, not an engineering one. What IS real and already built: a
production-shaped `FeedConnector`/`Pipeline` with content-hash dedup and
a proven anti-replay defense across all four source types, which refuses
any `SIMULATED`-mode connector's record tagged `LIVE` (see
`evidence/blockers-qualification-report.json`,
`READY_FOR_REAL_QUALIFICATION`). No further engineering work narrows
this without a real data contract; none was attempted this round, per
standing scope.

## PRIORITY 2 — Calibration corpus (historical events, labels, priors,
likelihoods, model versions, calibration provenance)

**Status: PARTIALLY CLOSED this round — the calibration PROCESS is now
real; the underlying historical DATASET remains BLOCKED_EXTERNAL for the
same reason as Priority 1.**

`pkg/governance/calibration` already closed two of the three parts a
prior audit named distinctly:

- The **contract** half: a `LikelihoodTable` cannot be registered without
  a complete `CalibrationRecord` (calibration_source, model_version,
  prior, effective_tick, dataset_provenance) — `Register`/`BuildObservation`
  fail closed (`ErrNoCalibration`) for anything ungoverned.
- The **wiring** half: `pkg/lifecycle.Orchestrator`'s optional
  `TemporalCalibration` field, when set, makes `RunUnified` call through
  to a real `pkg/moat/hbayes.Model.Infer` over real, provenanced evidence
  — not a stub (`TestRunUnifiedTemporalBayesianStageExecutesWhenCalibrationRegistered`).

This round adds the third, previously-missing part: the **fitting
process** itself. `pkg/governance/calibration/corpus.go`'s `Dataset` and
`Fit` turn a corpus of labeled historical events into a real
`LikelihoodTable` via deterministic frequentist maximum-likelihood
estimation — P(evidence value | hidden state) computed by counting, and
the marginal `Prior` computed the same way, both entirely derived from
`Dataset`, never asserted. `Fit` fails closed
(`ErrInsufficientSamples`) when a declared hidden state has too few
labeled events to estimate reliably, exactly the same "no false green"
discipline as every other refusal in this codebase. `Dataset.Hash()`
content-addresses the corpus so `CalibrationRecord.DatasetProvenance`
cites something checkable, not a free-text claim; the round-trip test
(`TestFittedTableRoundTripsThroughTheRealRegistryAndProducesAnObservation`)
proves a `Fit`-produced table is a genuinely registrable, usable
`LikelihoodTable`, not just numerically correct in isolation.

What remains, and cannot be closed by any coding session: a real corpus
of genuinely investigated, ground-truth-labeled historical events. This
requires either a commercial data-and-labeling contract or years of this
system's own resolved case history — the same category of real-world
dependency as `live_data`, not a code gap. `corpus_test.go` registers a
clearly-labelled `fixture:synthetic-dark-vessel-corpus-v1 (NOT a real
production calibration dataset)` corpus to prove `Fit`'s machinery is
real; it is never claimed to be production data.

## PRIORITY 3 — Real infrastructure qualification (KMS, SPIRE
production, 100-node, multi-region, 72h)

**Status: BLOCKED_EXTERNAL, substantially narrowed across R17 with real
multi-container drills; the literal production-scale requirement is
unchanged and cannot be closed without real procurement.**

This is exactly `hsm_kms` + `spire_mtls` + `scale_qualification` +
`multi_region_dr` + `soak_72h`. R17 already pushed real evidence as far
as this sandbox's actual capabilities allow:

- **KMS**: `pkg/platform/security/keys`'s `SoftwareBacked` production
  guard refuses any software-backed key provider under `env=production`;
  every real failure mode (unavailable, timeout, permission-denied,
  wrong-key, revoked) is proven to fail closed. A real cloud KMS
  adapter was **deliberately not added** this round: this codebase's
  `zero_dependency` gate (`go list -m all`, evidence:
  `evidence/blockers-qualification-report.json`) is a real, enforced,
  intentional architectural property, and any real AWS KMS / GCP KMS /
  Azure Key Vault client requires an external Go module — adding one
  would trade a genuine "we ship zero third-party code" guarantee for a
  cosmetic step toward a blocker that still needs real cloud credentials
  and a paid tenancy to ever qualify. That trade was judged not worth
  making; the interface remains ready to receive a real adapter the
  moment an operator provisions real KMS credentials.
- **SPIRE production**: a real 3-container SPIRE cluster (1 server + 2
  independently-attested agents) proved node-scoped isolation and live
  per-node revocation (`evidence/spire_mtls-multi-container-integration.txt`).
  What remains — a production node attestor (cloud-instance-identity /
  k8s-PSAT / TPM, not the test `join_token` attestor used here) and a
  Workload API client wired into `pkg/transport/rafttcp` — was
  re-examined this round and found to have the same zero-dependency
  tension as KMS: a real go-spiffe Workload API client is an external
  module, and a hand-rolled gRPC-over-Unix-socket client written from
  scratch risks producing something that does not actually speak the
  real protocol correctly — a worse outcome than an honest BLOCKED,
  because it would look closed without being real. Not attempted this
  round for that reason.
- **100-node scale**: 10 genuinely separate Docker containers, real
  HTTP, 50,000 records, zero lost/duplicated
  (`evidence/scale_qualification-multi-container-drill.txt`) — one order
  of magnitude short of the literal 100-node acceptance criterion, which
  requires real cloud/physical infrastructure this sandbox does not have
  and cannot provision.
- **Multi-region**: 3 real `cmd/veriqo-node` containers, a real `docker
  network disconnect` partition, ~500ms RTO / RPO=0
  (`evidence/multi_region_dr-multi-container-drill.txt`) — still not
  real cross-datacenter WAN infrastructure.
- **72h soak**: a genuine, unbroken 60-minute run
  (`evidence/soak-60min-run-log.txt`) backs the standing 2-minute smoke
  pass — 30x, not 72x60x the target. This sandbox's session lifecycle
  cannot honestly stay up for a continuous 72-hour window; no amount of
  retrying changes that physical constraint.

No further engineering narrowing was found tractable this round beyond
what R17 already produced.

## PRIORITY 4 — Independent assurance (external pentest, vulnerability
DB, independent build/reproducibility)

**Status: BLOCKED_EXTERNAL for the vendor and vulnerability-feed
components, unchanged; independent-build reproducibility narrowed this
round with a real cross-runner proof.**

- **External pentest**: unchanged. `pkg/blockers/pentest` runs real
  adversarial probes (JWT alg=none, unknown kid, sandbox path traversal,
  authz wildcard escalation) directly against this codebase's own
  production `pkg/api`/`pkg/kernel/sandbox`/`pkg/authz`
  (`READY_FOR_REAL_QUALIFICATION`), but only a real, independent
  security vendor's signed report can ever satisfy this by construction
  — no session, however long, substitutes for an independent third
  party being independent.
- **Vulnerability DB**: unchanged, confirmed still blocked. `vuln.go.dev`,
  `osv.dev`, and the GitHub advisory API all return 403 under this
  sandbox's own confirmed, explicit organization egress policy — a
  policy denial, not a technical gap. SAST (`gosec`, `staticcheck`) runs
  clean (0 findings) and is fully closed; the vulnerability-database
  query specifically is not.
- **Independent build / reproducibility**: real progress this round.
  `internal/reproducibility` already proved bit-for-bit binary equality
  across two independent *compiler invocations* on one machine (and
  found/fixed two real reproducibility bugs doing so: cgo's system
  compiler embedding temp paths `-trimpath` doesn't cover, and `go
  build`'s default VCS auto-stamping capturing a working-tree dirty bit
  that could differ between two sequential builds under concurrent test
  load — see that package's own header comment for the full account).
  What it could not prove from inside a single `go test` process is two
  builds from genuinely SEPARATE builders. `.github/workflows/reproducible-build.yml`
  (new this round) closes that: two parallel GitHub Actions jobs, each
  on its own freshly-provisioned, isolated ephemeral VM — no shared
  filesystem, no shared `GOCACHE`, no shared process — independently
  build the identical source tree and a third job diffs the resulting
  SHA-256 hashes. This is honestly "two separate machines" narrowing the
  gap real auditors mean by "independent builder" — though both happen
  to be GitHub-hosted `ubuntu-latest` runners on the same toolchain
  vendor, not two independent CI providers or operating systems. That
  narrower remaining gap (a build on a genuinely different provider/OS
  lineage) is real and still open; this workflow does not claim
  otherwise.

## PRIORITY 5 — Pilot customer

**Status: BLOCKED_EXTERNAL, categorically outside any coding session's
authority or capability.**

A pilot customer is a real commercial relationship: sales, contracting,
onboarding, and a real organization choosing to run VERIQO against their
own operations. No amount of engineering effort, however sustained,
substitutes for a real customer existing and agreeing to a pilot — this
is not a technical gap this repository's code can close, and claiming
otherwise would require fabricating a customer relationship that does
not exist. Nothing was attempted here, and nothing honestly could be.

## Summary

| Priority | What's real & closed this round or before | What remains, and why it's categorically external |
|---|---|---|
| 1. Real data acquisition | Real connector/pipeline/anti-replay machinery | Commercial data contracts (excluded from scope by standing directive) |
| 2. Calibration corpus | Contract + wiring + **real fitting process (new, R18)** | A real labeled historical dataset (data acquisition, same category as #1) |
| 3. Infrastructure qualification | Real multi-container drills for SPIRE/DR/scale; fail-closed KMS guard | Real cloud/physical procurement at literal production scale; zero-dependency architecture rules out adding cloud SDKs for a partial, still-unqualifiable step |
| 4. Independent assurance | Real SAST; real adversarial pentest harness; **real cross-runner independent build (new, R18)** | A real vendor's signed report; a blocked vulnerability-DB feed (org policy); a build on a genuinely separate CI provider/OS lineage |
| 5. Pilot customer | — | A real customer choosing to run a pilot — a sales/business outcome, not a code gap |

Every "PARTIALLY CLOSED" or "narrowed" claim above cites a specific,
reproducible artifact in this repository or its evidence directory. Every
"BLOCKED_EXTERNAL" claim names the specific real-world resource — money,
a contract, physical infrastructure, an independent third party, or a
real customer — that no coding session can substitute for without
fabricating evidence, which this project has consistently refused to do.
