# VERIQO Insurance — Residual Gate Register (R21)

Everything that remains genuinely external, or genuinely deferred to
Phase 2/3 of the functional spec, after the R21 insurance round. It uses
the same four-axis separation
`PRE_INSURANCE_RESIDUAL_EXTERNAL_GATE_REGISTER.md` introduced, for the
same reason: an operator needs to tell a gate blocked because *code is
missing* from a gate blocked because *a contract, a customer, or a
tribunal has not happened yet*.

**No gate in this document was advanced by this round.** The eight
pre-existing external blockers are semantically unchanged in
`READINESS_MANIFEST.json` — status, blocked reason, mandatory flag and
all four axes byte-identical. This document adds insurance-specific rows
beside them; it moves none of them.

## How to read the axes

| Axis | Question it answers | Values |
|---|---|---|
| **Engineering** | Does the code exist, and does its own harness pass? | PASS / FAIL / NOT_RUN |
| **Internal** | Has it been qualified as far as *this* environment honestly allows? | INTERNAL_QUALIFIED / FAIL / NOT_RUN / NOT_APPLICABLE |
| **External** | Has real external evidence qualified it? | EXTERNAL_QUALIFIED / BLOCKED_EXTERNAL / NOT_APPLICABLE |
| **Final** | The composed answer | READY / NOT_READY / BLOCKED_EXTERNAL / DEFERRED |

The composition rule is unchanged and is one line: **whenever External
is BLOCKED_EXTERNAL, Final is BLOCKED_EXTERNAL**, however green the
first two are.

`INTERNAL_QUALIFIED` is never a synonym for VERIFIED. A synthetic case
passing is real evidence about the *code* and no evidence at all about a
real claim.

---

## A. Genuinely external — a real-world event must happen

These cannot be closed by any amount of engineering inside this
repository. Each names the specific real-world thing that is missing.

| # | Item | Engineering | Internal | External | Required evidence | Owner |
|---|---|---|---|---|---|---|
| **INS-E1** | **Real historical claim replays** (Final Design §38 Definition of Done; spec §50) | PASS — the whole domain runs end to end on seven synthetic cases through the real facade | INTERNAL_QUALIFIED — `TestDrivingIsDeterministic` proves the drive is deterministic across runs | **BLOCKED_EXTERNAL** — no permissioned real historical claim exists in this repository, and none was fabricated | One anonymised, permissioned, real historical claim ingested through the same adapters and replayed to the same decision artifact, with the differences explained by version rather than hidden | Commercial / legal |
| **INS-E2** | **Real customer pilot data** (Final Design §24 RD-3) | PASS — `pkg/connector` is adapter-shaped with no provider hard-coded, and the rights gate is fail-closed | NOT_RUN — deliberately. A drill against synthetic data establishes nothing about real data, and counting it would be the false green this programme exists to prevent | **BLOCKED_EXTERNAL** — requires a Data Processing Agreement, permission, data-use scope, retention and display rules. Explicitly out of this programme's scope by standing operator directive, and unchanged by this round | Production data ingested under a real DPA with a `RightsState` above `UNKNOWN_PENDING_CONTRACT` granted through a real `GrantTrust` call | Commercial / legal |
| **INS-E3** | **A real external legal or arbitration outcome** (I-08) | PASS — `dispute.RecordAward` / `RecordJudgment` accept a real outcome today and require the determining authority and document paragraph | NOT_APPLICABLE — there is no in-sandbox analogue of a tribunal | **BLOCKED_EXTERNAL** — an award or judgment is issued by a tribunal or court. This is categorically unsatisfiable from inside a repository | A real, recorded award or judgment attached to a matter, with its allegations determined by that authority | Legal |
| **INS-E4** | **A real regulatory body's actual finding** (I-08 regulatory half) | PASS — `regulatory.RecordFinding` accepts one today and refuses it without a cited authority and source | NOT_APPLICABLE — same reason | **BLOCKED_EXTERNAL** — a finding is made by a regulator | A real regulatory notice or finding recorded against a matter | Compliance / legal |
| **INS-E5** | **Independent review of an insurance dossier** (spec §77, §78) | PASS — `verification.Manifest` is independently recomputable by any holder of the evidence set | INTERNAL_QUALIFIED — recomputation is proven; the manifest detects addition, removal and alteration | **BLOCKED_EXTERNAL** — "VERIQO says verified" is exactly what spec §78 forbids. Independence is the requirement, and no self-run check can satisfy it by construction | A dossier verified by a loss adjuster, surveyor, auditor or customer-appointed reviewer who does not work on this system, submitted through `pkg/governance/qualification` against a registered provider | Insurance operations |
| **INS-E6** | **Model calibration against a real corpus** (spec §41, §51) | PASS — `pkg/governance/calibration` and `pkg/moat/reliability` exist with real Brier / log-loss / ECE / drift machinery, and `ErrTemporalCalibrationRequired` already fails closed | NOT_RUN — no probabilistic insurance model was built this round, so there is nothing to calibrate. Recorded as not-run rather than not-applicable, because a Phase 3 model would need it | A labelled historical corpus with a named owner, permission, sampling methodology and train/test split | Data science / commercial |

---

## B. Genuinely deferred — in scope, not built this round

These are engineering work this round did not do. Each says what exists,
what does not, and why it was not attempted, so the next round starts
from a fact rather than a re-derivation.

### R22 closure (VERIQO Master Closure Mandate round)

**INS-D1 is now CLOSED.** `pkg/insurance/casepack/replay.go` adds
`Case.Snapshot()` (canonical JSON), `SnapshotID()` (content address),
`ReplayFromSnapshot()` (reconstructs a `Case` from bytes ALONE — no
reference to any prior in-memory value — and drives it through the
identical production path `Drive()` uses), and `ColdReplay()` /
`RunColdReplay()`, which run the comparison and report per-field
divergence. Wired as a fifth mandatory readiness gate,
`insurance_cold_replay`, registered in `cmd/veriqo-readiness` alongside
the original four. `TestColdReplayReproducesIdenticalResultOnEveryCase`
proves it on all seven synthetic cases;
`TestReplayFromSnapshotNeverTouchesTheOriginalCase` proves the replay
path is genuinely cold (the original `Case` is scoped out of reach
before replay runs); `TestColdReplayDetectsADivergedSnapshot` proves the
comparison is a real check, not a vacuous one. The historical entry
below is retained for the record of why it was deferred, not because it
is still accurate.

Three further items this round closed as real engineering (not
mandated by the two frozen design documents, but by the VERIQO Master
Closure Mandate's §10, §18 and §20):

- **Real-world insurance network roles** (§10): `pkg/insurance/party`
  gained ten roles — `COVERHOLDER_MGA`, `UNDERWRITER`, `CO_INSURER`,
  `CLAIMS_HANDLER`, `AVERAGE_ADJUSTER`, `EXPERT`, `SALVAGE_PARTY`,
  `RECOVERY_PARTY`, `REPAIRER`, `BANK_TRADE_FINANCE` — completing the
  mandate's own named chain (Insured → Broker → Coverholder/MGA →
  Underwriter → Insurer → Co-insurer → Reinsurer → P&I Club → Claims
  Handler → Loss Adjuster → Surveyor → Average Adjuster → Expert →
  Salvage Party → Recovery Party → Lawyer → … → Repairer → Bank/Trade
  Finance). A genuine pre-existing bug was found and fixed in the same
  pass: `pkg/insurance/recovery.CategoryPartyRole` still reported "no
  known role" for `Warehouse`/`Manufacturer` after an earlier round had
  already added `party.RoleWarehouse`/`party.RoleManufacturer` — the two
  packages had drifted out of sync while both kept passing their own
  tests. Fixed and covered by `TestCategoryPartyRoleMapping`.
- **Salvage lifecycle** (§18, and §78's explicit "do not declare the
  Insurance System complete without Subrogation, Recovery, Salvage"):
  new package `pkg/insurance/salvage` — damaged asset/cargo, assessment,
  contractor engagement, evidence linkage, disposal, proceeds, expenses,
  and a real `NetValue()`/`TotalNetValueForClaim()` computation (evidence-
  backed, integer minor units, matching `pkg/insurance/quantum`'s own
  representation) that feeds the "impact on quantum" half of §18 —
  callers pass the total into `quantum.ComputeInput.Salvage`. 16 tests,
  including a concurrency test and the same reflection-based
  no-liability-field / no-opaque-confidence-field checks the rest of the
  domain uses. Confirmed picked up by `pkg/insurance/guardrails`'
  whole-tree scan (`TestTheScanReachesEveryInsurancePackage`) with zero
  forbidden patterns found.
- **Co-insurance / reinsurance participation** (§20): new file
  `pkg/insurance/policy/participation.go` adds `Participant` (party,
  role, fixed-point basis-points share, treaty/facultative basis) on
  `policy.Version`, with `Reinsurers()`/`CoInsurers()`/
  `RetainedBasisPoints()` and aggregate validation (co-insured and ceded
  totals each independently capped at 100%). Additive only: a `Version`
  with no `Participants` — the common single-insurer case — is unaffected
  and unchanged.

| # | Item | What exists | What does not | Why not attempted | Final |
|---|---|---|---|---|---|
| ~~**INS-D1**~~ | ~~Per-case insurance replay~~ | See "R22 closure" above. | — | — | **CLOSED (R22)** |
| **INS-D2** | **Case Room API and UI** (Final Design §23 "C8", §28) | `api.Facade` sequences the whole lifecycle and now exposes `Status()`/`Stage()`; every §23 endpoint has a real method behind it | The HTTP surface itself, and the six-panel Case Room | Out of scope for a domain-layer round. The domain must be right before a UI renders it, and `pkg/api` already owns HTTP semantics | **DEFERRED** |
| **INS-D3** | **Rights-aware source adapters** (Final Design §18 "C3", §26 "C4") | `pkg/connector/{aisstream,sar,bol,insurance,payment}` are the five audited adapters, each parsing a real wire schema and failing closed on malformed input. `evidenceapi.SyntheticDocument` is the new fixture adapter | An insurance-specific adapter set, and the Rights Gate placed *before* a Case Room (there is no Case Room yet) | The adapters exist and are provider-neutral; nothing insurance-specific was missing that could be built without a real feed to shape it | **DEFERRED** |
| **INS-D4** | **Reserve intelligence** (spec §27, Phase 2 §81) | `pkg/insurance/quantum` computes an indicative value with full operand lineage | Initial / updated / scenario reserves, worst-expected-best cases | Explicitly Phase 2 in the spec's own §81. Not attempted, and not claimed | **DEFERRED (Phase 2)** |
| **INS-D5** | **Fraud / anomaly indicators** (spec §33, Phase 2) | `pkg/insurance/contradiction` surfaces document and temporal inconsistencies as INDICATORS; CASE-INS-003 and CASE-INS-005 both end in explicit non-determinations | A consolidated indicator surface over the canonical anomaly layer | Phase 2. Deliberately not built as a "fraud engine" — the spec is explicit that these are indicators, never proof, and the domain currently has no field that could record otherwise | **DEFERRED (Phase 2)** |
| **INS-D6** | **Portfolio analytics, customer portal, external reviewer portal** (Phase 2 §81) | — | — | Phase 2 | **DEFERRED (Phase 2)** |
| **INS-D7** | **Predictive claims intelligence, parametric structures, pre-loss alerts, trade-finance integration, cross-case pattern detection** (Phase 3 §82–§84) | — | — | Phase 3. Note that §41's fail-closed rule already applies in advance: any such model would need real calibration (INS-E6) before it could emit anything | **DEFERRED (Phase 3)** |

---

## C. Scoped limitations inside work that IS closed

These are not deferred items — the work is done — but each carries a
limitation that would be dishonest to leave unstated.

| # | Closed item | The limitation, stated |
|---|---|---|
| **INS-L1** | Deadline calendar rules | `pkg/insurance/deadline` implements exactly two calendar rules (CALENDAR_DAYS, BUSINESS_DAYS) and says so in its own doc comment. A real public-holiday calendar per jurisdiction is not implemented, and the enum is honestly limited to two rather than padded with an unimplemented third. Unchanged by this round |
| **INS-L2** | Currency minor units | `quantum.Amount` uses a fixed minor-unit exponent of 2 for every currency rather than a full ISO 4217 exponent table. Documented in the package, not silently assumed. Unchanged by this round |
| **INS-L3** | Business-day tick convention | `deadline`'s business-day arithmetic defines its own tick-to-weekday convention (tick 0 is a Monday, one tick is one day) rather than using a real calendar library. It applies only to BUSINESS_DAYS; CALENDAR_DAYS makes no assumption about what a tick means |
| **INS-L4** | The four insurance gates' scope | All four are **engineering** gates over synthetic cases. They prove the domain's traceability, reproducibility, preservation and human-review enforcement hold. They establish nothing about live customer data, and the gate descriptions in `cmd/veriqo-readiness` say so |
| **INS-L5** | Timeline canonical ordering | `timeline`'s impossible-sequence detection uses a documented canonical claim-lifecycle order and its own comment states this is a simplification and not proof a sequence is truly impossible. Unchanged by this round |
| **INS-L6** | The guardrail tree scan | The whole-tree scans in `pkg/insurance/guardrails` are deliberately blunt substring and AST checks. They can flag an innocent name, and the standing rule is to **rename the field rather than add an exception** — `LegalHold.CoveredEvidence` became `EvidenceInScope` for exactly this reason. Both allowlists are themselves tested for staleness, because an exemption naming a field that no longer exists is a hole waiting for a new field to fall into |
| **INS-L7** | Insurance readiness levels (spec §79) | The spec proposes a seven-level insurance-specific readiness ladder (INSURANCE_CODE_COMPLETE … INSURANCE_VERIFIED). It was **not** implemented as a second status vocabulary, deliberately: `internal/assurance` already owns the canonical status ladder and `pkg/governance/qualification` owns the external-evidence path, and a parallel insurance ladder would be exactly the duplicate the non-duplication rule forbids. The insurance gates therefore report through the existing vocabulary. If a domain-specific ladder is genuinely wanted later, it should be a *projection* of the canonical one, the same way `case.Stage` projects the internal lifecycle |

---

## D. The eight pre-existing external blockers

Unchanged, and re-verified as unchanged this round.
`hsm_kms`, `live_data`, `multi_region_dr`, `pentest`,
`scale_qualification`, `soak_72h`, `spire_mtls`, `supply_chain_scan`
retain their exact status, blocked reason, mandatory flag and all four
axes. Their full detail stays in
`PRE_INSURANCE_RESIDUAL_EXTERNAL_GATE_REGISTER.md`; nothing here
supersedes or restates it.

One of them is directly relevant to insurance and is worth naming:
**`live_data`** is the gate that INS-E2 sits behind. Nothing in this
round touched it, and no insurance gate depends on it or is weakened by
it being blocked.

---

## Composed position

| | Count |
|---|---|
| Insurance items **BLOCKED_EXTERNAL** | 6 (INS-E1 … INS-E6) |
| Insurance items **DEFERRED** | 6 (INS-D2 … INS-D7) |
| Insurance items **CLOSED this round (R22)** | 1 (INS-D1) + 3 not tracked as numbered items (real-world party network, salvage, co/reinsurance participation — see "R22 closure" above) |
| Insurance items **closed with a stated limitation** | 7 (INS-L1 … INS-L7) |
| Pre-existing external blockers, unchanged | 8 |

**Release verdict: `NOT_PRODUCTION_READY`**, and correctly so — a single
blocked mandatory gate makes the whole verdict not-ready, by design, and
there are eight.

The insurance domain's own position, stated without inflation: the eight
frozen domains are implemented and tested; the domain now has FIVE
engineering gates passing over synthetic cases (the original four plus
`insurance_cold_replay`, closed this round); the Definition of Done in
Final Design §38 has ten of its eleven criteria met or internally
qualified — deterministic replay moved from OPEN to VERIFIED this round —
and the eleventh (real historical case replays) remains genuinely
blocked on permissioned data that does not exist inside this repository
and cannot be fabricated to close it.
