# VERIQO Insurance — Verification Matrix (R21)

Every row states a requirement from one of the two frozen design
documents, the code that implements it, the test that proves it, what
that test actually establishes, and the evidence a reader can go and
check. Status vocabulary is `docs/governance/RELEASE_GATES.md`'s.

Two words are used precisely throughout and are not interchangeable:

* **VERIFIED** — real code, real tests, and the tests prove the specific
  property claimed. This is the ceiling for anything provable from
  inside this repository.
* **INTERNAL_QUALIFIED** — proven as far as synthetic cases honestly
  allow, and explicitly *not* qualification against real data.

Nothing in this document is VERIFIED on the strength of a synthetic case
alone where the requirement is about the real world. Those rows say so,
and they are carried in `INSURANCE_RESIDUAL_GATE_REGISTER.md`.

---

## 1. The eight frozen domains (Final Design §2–§8)

| # | Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|---|
| **I-01** | Policy, version, endorsement, exclusion, deductible, limit, sub-limit, effective period, applicable law. Output COVERAGE_FACT / CONDITION / CONFLICT / GAP / HUMAN_REVIEW_REQUIRED, never `COVERED = TRUE` | `pkg/insurance/policy`, `pkg/insurance/coverage` | `TestCoverageAnalysisHasNoCoverageVerdictField`, `TestEffectiveAtSelectsHistoricalVersionNotLatest` | No boolean coverage verdict field exists on the output type, by reflection; the version in force at the incident is resolved, never the latest | pre-existing suite, unchanged this round | **VERIFIED** |
| **I-02** | Fuse AIS/port/terminal/NOR/SOF/survey/weather/sensor/document into a canonical event timeline with per-event source and confidence | `pkg/insurance/timeline` | `TestBlueprintWorkedExample_DamageDiscoveryTemporalConflict`, `TestNoticeTimely_DisputedWhenNoticeBeforeIncident`, `TestCompareOneSidedInsufficientData` | Independently sourced accounts stay separate events; disagreement is surfaced, never reconciled silently | pre-existing suite | **VERIFIED** |
| **I-03** | Notice & Obligation: incident → condition → requirement → deadline → responsible party → evidence → action → completion. **LATE NOTICE ≠ COVERAGE DENIED** | `pkg/insurance/obligation` (new), `pkg/insurance/deadline` (reused, not rebuilt) | `TestComplianceVocabularyCannotExpressDenial`, `TestCoverageEffectHasExactlyOneValue`, `TestLateNoticeProducesReviewNotDenial`, `TestAssessmentHasNoDenialField`, `TestLateNoticeNeverSetsACoverageOutcome` | The compliance vocabulary cannot express a claim consequence; `CoverageEffect` is one-valued across all five branches; a late notice yields one review requirement naming the policy wording and applicable law and leaves every other coverage fact byte-identical | R21-5 | **VERIFIED** |
| **I-04** | Causation as competing hypotheses with supporting / contradicting / missing evidence; never a single asserted cause | `pkg/insurance/causation` (pre-existing, **not rebuilt**) | `TestExplainAdversarialNeverHidesTheGap`, `TestEveryCaseCausationIsHedged`, `TestCase004ProducesNoSingleAssertedCause` | Narrative is assembled only from fixed hedge templates, so a bare "X caused Y" is structurally unreachable; proven again end-to-end on all seven synthetic cases | pre-existing suite + R21-6 | **VERIFIED** |
| **I-05** | Quantum with per-value lineage SOURCE → EXTRACTION → TRANSFORMATION → CALCULATION → RESULT | `pkg/insurance/quantum` (pre-existing), `pkg/insurance/verification` (§55 gate, new) | `TestQuantumIsGenuinelyRecomputed`, `TestComputeDeterminism`, `TestDetectDiscrepancyNoGapWhenFiguresAgree` | Identical inputs at an identical version recompute to a byte-identical result; every non-zero operand cites real evidence; claimed and supported figures are reported side by side with no winner chosen | R21-6 | **VERIFIED** |
| **I-06** | Mitigation: what can still be prevented, preserved, noticed, quarantined, surveyed. Never concludes liability | `pkg/insurance/mitigation` (pre-existing) | `TestPublicAPIHasNoReasonablenessJudgment` | No exported field or method expresses a legal reasonableness judgment, by reflection | pre-existing suite | **VERIFIED** |
| **I-07** | Recovery / subrogation with source, legal basis, amount, deadline, owner, status per right | `pkg/insurance/recovery` (pre-existing) | `TestNoLiabilityDeterminationField`, `TestComputeLimitationStatus` | No field can be read as "liability confirmed"; limitation status is computed from a deadline tick, never set | pre-existing suite | **VERIFIED** |
| **I-08** | Dispute / Legal / Regulatory: notice → hold → legal review → negotiation → mediation → arbitration → court → outcome → recovery, with governing law, jurisdiction, seat, forum, limitation, notice period, evidence requirements, enforcement — as metadata and workflow, never automatic legal advice | `pkg/insurance/dispute` (new), `pkg/insurance/regulatory` (new) | `TestLegalQuestionHasExactlyOneStatus`, `TestHistoricalReferenceCannotBecomeARule`, `TestForumRequiresACitedSource`, `TestForumRestatesNoDurations`, `TestSettlementNeverImpliesAllegationsProven`, `TestMonitorRequirementIsNotMonitorCompletion` | A legal question has exactly one possible status, so no code path answers one; a real decision can only exist as inert reading material; a forum has no numeric field, so a limitation period must be a `deadline.Rule` reference | R21-4 | **VERIFIED** |

---

## 2. Cross-cutting architecture (functional spec §3, §58, §92)

| # | Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|---|
| X-1 | Non-duplication: no second identity / evidence store / replay / decision engine / correlation key / policy registry / provenance model | whole tree | `TestNoForbiddenCanonicalDuplicateIsDeclaredAnywhere`, `TestInsuranceDeclaresNoSecondRightsVocabulary` | Every non-test file under `pkg/insurance` is parsed; no top-level declaration carries a forbidden name, and no package declares its own `RightsState` | R21-2, R21-8 | **VERIFIED** |
| X-2 | Consume the canonical foundation: correlation propagation, case lineage, evidence envelope | `pkg/insurance/canonical`, `pkg/insurance/api` | `TestFacadeBindsEveryStepToOneCanonicalCaseLineage`, `TestEveryCaseBindsToOneCanonicalCaseLineage`, `TestBindCorrelationRegistersTheRealIdentifiers`, `TestDanglingUpstreamIsRefused` | Each lifecycle step registers its real artifact under ONE lineage CaseID; a correlation key's identifiers fold into the same case; a dangling upstream is refused rather than stored | R21-2, R21-6 | **VERIFIED** |
| X-3 | Rights-aware evidence: possession is never permission; REVOKED permits nothing | `pkg/insurance/evidence`, `pkg/evidence/provenance` | `TestRevokedRightsPermitNothing`, `TestPossessionIsNotPermission`, `TestUnsetRightsPermitNothing`, `TestPermitsUseRequiresBothTheRecordAndTheOrder` | REVOKED and EXPIRED permit nothing including internal use; an unset state permits nothing; a permissive preservation order cannot widen a restricted record | R21-2, R21-5 | **VERIFIED** |
| X-4 | Preservation: trigger, scope, custodian, evidence types, start, deadline, legal-hold state, rights, hash, chain of custody | `pkg/insurance/preservation` | `TestNewRequiresEveryStructuralField`, `TestHashChangesWhenThePreservedSetChanges`, `TestReleaseIsRefusedWhileAHoldIsInForce` | Every §19 field is mandatory at construction; the order hash is tamper-evident and arrival-order independent; a live legal hold blocks release | R21-5 | **VERIFIED** |
| X-5 | Case state machine: 9 stages + 5 exception states | `pkg/insurance/case/stage.go` | `TestStageMappingIsTotalAndSingleValued`, `TestStageIsDerivedNotSettable`, `TestExceptionNeverMovesTheLifecycle`, `TestStageIsMonotonicAlongTheInternalSequence` | The mapping is total and single-valued with a checked entry count; the stage cannot be stored or set; exceptions move nothing, at any point in the sequence | R21-3 | **VERIFIED** |
| X-6 | Verification gates §54–§57 | `pkg/insurance/verification/gates.go`, `cmd/veriqo-readiness` | `TestThreeGatesPassOnEveryCase`, `TestHumanReviewGateFailsClosedOnEveryCase`, `TestHumanReviewGateOpensWithARealAuthorization`, `TestARubberStampAuthorizationIsRefused` | Coverage traceability, quantum reproducibility and preservation PASS on all seven cases; human review fails closed without authorization and opens with a complete one; a rubber stamp is refused | R21-6, R21-7 | **VERIFIED** |
| X-7 | Human-in-the-loop; fail closed when mandatory review is missing | `pkg/insurance/verification`, `pkg/insurance/dossier` | `TestHumanReviewGateFailsClosedOnEveryCase`, `TestDossierHasNoVerdictField` | Finalization is refused while any review question is unaddressed, and the refusal names each one | R21-6 | **VERIFIED** |
| X-8 | Synthetic case pack CASE-INS-001…007 | `pkg/insurance/casepack` | `TestAllSevenCasesExistAndValidate`, `TestEveryCaseDrivesTheFullFacadePath`, `TestCasesAreDeterministicFixtures`, `TestNoRealWorldEntityAppearsInThePack` | All seven exist, drive end to end through the real facade, reproduce byte-identical content-addressed IDs across runs, and name no real-world entity anywhere in the package source | R21-6 | **VERIFIED** |
| X-9 | Per-case replay (spec §73, Final Design §20 "C5") | `pkg/replay` exists and is **not** duplicated; `pkg/insurance/casepack/replay.go` (R22) adds `Snapshot`/`ReplayFromSnapshot`/`ColdReplay` | `TestColdReplayReproducesIdenticalResultOnEveryCase`, `TestReplayFromSnapshotNeverTouchesTheOriginalCase`, `TestColdReplayDetectsADivergedSnapshot` | A case reconstructed from nothing but its own serialised snapshot reproduces the live evidence root hash, preservation hash, quantum result and resolved policy version on all seven cases; the replay path never references the original in-memory case | R22 | **VERIFIED (R22)** |
| X-10 | The Final Design §39 forbidden list | whole tree | `TestNoVendorJudgmentOrCompanyIsHardCodedAnywhere`, `TestNoOpaqueConfidenceScoreAnywhereInTheInsuranceDomain`, `TestNoDeterminationFieldAnywhereInTheInsuranceDomain`, `TestAFixtureCaseCanNeverReportAsLive` | No named vendor, real judgment or real company anywhere; no float or confidence field on any exported type; no determination field; a fixture case cannot claim live provenance | R21-6, R21-8 | **VERIFIED** |

---

## 3. The MVP checklist (functional spec §80)

The spec names fifteen items for the first operational release.

| # | §80 item | Where | Test | Status |
|---|---|---|---|---|
| 1 | Policy ingestion | `pkg/insurance/policy` (`Clause` carries document, page, source hash, text span, version, effective date) | `TestTracesToClause_MismatchedVersionFails` | **VERIFIED** |
| 2 | Policy semantic model | `policy.Version` + `policy.History.EffectiveAt` | `TestEffectiveAtSelectsHistoricalVersionNotLatest`, `TestEveryCaseUsesTheHistoricalPolicyVersion` | **VERIFIED** |
| 3 | Case | `pkg/insurance/case` + derived `Stage`/`Status` | `TestStageAdvancesInLockstepWithTheRealLifecycle` | **VERIFIED** |
| 4 | FNOL / claim | `pkg/insurance/claim` (extensible registry, 24 seed types + 2 pack types) | `TestExtensibleRegistryAcceptsANonBuiltinType` | **VERIFIED** |
| 5 | Evidence preservation | `pkg/insurance/preservation` | `TestNewRequiresEveryStructuralField`, `TestPreservationCoversEveryRecord` | **VERIFIED** |
| 6 | Timeline | `pkg/insurance/timeline` | conflict-detection suite | **VERIFIED** |
| 7 | Coverage analysis | `pkg/insurance/coverage` | `TestCoverageAnalysisHasNoCoverageVerdictField` | **VERIFIED** |
| 8 | Notice analysis | `pkg/insurance/obligation` | `TestLateNoticeProducesReviewNotDenial` | **VERIFIED** |
| 9 | Causation analysis | `pkg/insurance/causation` | `TestEveryCaseCausationIsHedged` | **VERIFIED** |
| 10 | Quantum | `pkg/insurance/quantum` | `TestQuantumIsGenuinelyRecomputed` | **VERIFIED** |
| 11 | Contradiction analysis | `pkg/insurance/contradiction` (adapter over the real `pkg/moat` arbitration engine) | `TestEveryCaseSurfacesItsContradiction` | **VERIFIED** |
| 12 | Human review | `pkg/insurance/verification` §57 gate + `pkg/governance/hitl` as the canonical authority | `TestHumanReviewGateFailsClosedOnEveryCase` | **VERIFIED** |
| 13 | Claim dossier | `pkg/insurance/dossier` | `TestDossierHasNoVerdictField`, `TestEveryCaseDossierRequiresReviewAndCarriesNoVerdict` | **VERIFIED** |
| 14 | Replay | `pkg/replay` (canonical, not duplicated) + `pkg/insurance/casepack/replay.go` (R22) | `TestColdReplayReproducesIdenticalResultOnEveryCase` | **VERIFIED (R22)** |
| 15 | Verification | `pkg/insurance/verification` (`Manifest` + five gates, R22 adds `insurance_cold_replay`) | `TestVerifyDetectsAlteredEvidence`, `TestThreeGatesPassOnEveryCase` | **VERIFIED** |

**15 of 15 closed as of R22.** Item 14 (replay) moved from OPEN to
VERIFIED this round: `pkg/insurance/casepack/replay.go` serialises a
case to canonical JSON, reconstructs it from those bytes ALONE (no
reference to the original in-memory value), drives the reconstruction
through the same production path (`Drive`) a live case takes, and
compares the two results field by field. This is a real cold replay,
not a restatement of drive-determinism — see the Residual Register's R22
closure note for why the earlier round declined to claim this on
drive-determinism alone.

---

## 4. The acceptance test (Final Design §37)

| Group | Criterion | Test | Status |
|---|---|---|---|
| **Evidence** | Original preserved | `TestSupersessionDeniesUseWithoutMutatingTheOriginal` | **VERIFIED** |
| | Hash verified | `TestVerifyDetectsAlteredEvidence`, `TestVerifyDetectsAHashMismatch` | **VERIFIED** |
| | Provenance complete | `TestEverySyntheticRecordCarriesItsOriginInItsContentHash` | **VERIFIED** |
| | Chain of custody complete | `TestPerItemEventsRequireACoveredItemAndAnActor` | **VERIFIED** |
| | Rights recorded | `TestRevokedRightsPermitNothing`, `TestPossessionIsNotPermission` | **VERIFIED** |
| **Timeline** | Timezone preserved | `timeline.Event.Timezone` carried verbatim; `TestWrongTimezone_AdversarialCase5` | **VERIFIED** |
| | Source time preserved | `EventTimeOriginal` never reparsed; `TestUnparseableTimezoneIsInsufficientDataNotGuessed` | **VERIFIED** |
| | Ingestion time preserved | `ontology.Evidence` bitemporality (canonical, not duplicated) | **VERIFIED** |
| | Corrections versioned | `TestSupersessionDeniesUseWithoutMutatingTheOriginal` | **VERIFIED** |
| | Replay deterministic | `TestDrivingIsDeterministic` (same evidence root, preservation hash and quantum across runs) | **INTERNAL_QUALIFIED** — determinism of the *drive*, not a cold cross-process replay |
| **Intelligence** | Contradictions detected | `TestEveryCaseSurfacesItsContradiction` | **VERIFIED** |
| | Inference separated | `causation.Hypothesis` three-way decomposition; `TestNoOpaqueConfidenceScoreAnywhereInTheInsuranceDomain` | **VERIFIED** |
| | Confidence decomposed | same; the tree scan finds **no float field anywhere** in the domain | **VERIFIED** |
| | Missing evidence identified | `pkg/insurance/gap`; `TestDetectGapsExactSetDifferenceNoFabrication` | **VERIFIED** |
| **Insurance** | Coverage mapped | `TestCoverageTraceabilityActuallyChecked` | **VERIFIED** |
| | Notice calculated | `TestLateNoticeProducesReviewNotDenial` | **VERIFIED** |
| | Causation hypotheses | `TestEveryCaseCausationIsHedged` | **VERIFIED** |
| | Quantum lineage | `TestQuantumIsGenuinelyRecomputed` (every operand evidence-backed) | **VERIFIED** |
| | Mitigation | `pkg/insurance/mitigation` suite | **VERIFIED** |
| | Recovery / subrogation | `pkg/insurance/recovery` suite | **VERIFIED** |
| **Governance** | Human authority | `pkg/governance/hitl` (canonical) + `verification.ReviewAuthorization` | **VERIFIED** |
| | Human decision | `TestHumanReviewGateOpensWithARealAuthorization` | **VERIFIED** |
| | Override log | `pkg/governance/hitl` hash-chained ledger (canonical, pre-existing) | **VERIFIED** |
| | Legal hold | `TestReleaseIsRefusedWhileAHoldIsInForce`, `TestLegalHoldIsRecordedAndReleasedNeverDeleted` | **VERIFIED** |
| | Export audit | `preservation.Order.RecordExport` + `TestPerItemEventsRequireACoveredItemAndAnActor` | **VERIFIED** |
| **Final** | Dossier generated | `TestEveryCaseDrivesTheFullFacadePath` | **VERIFIED** |
| | Hash manifest generated | `TestEveryCaseDrivesTheFullFacadePath` (manifest verified per case) | **VERIFIED** |
| | Replay succeeds | `TestColdReplayReproducesIdenticalResultOnEveryCase` | **VERIFIED (R22)** — cold replay from a serialised snapshot reproduces the live result on all seven cases |
| | Independent verification succeeds | `verification.Verify` recomputes the evidence root from the registry alone | **INTERNAL_QUALIFIED** — independently *recomputable*, but not independently *reviewed* |

---

## 5. Definition of Done (Final Design §38)

| Criterion | Status | Note |
|---|---|---|
| Synthetic case passes | **VERIFIED** | All seven, end to end through the real facade |
| Real historical case replays | **BLOCKED (external)** | Requires permissioned real historical data. No such data exists in this repository, and none was fabricated |
| Rights Gate passes | **VERIFIED** | `TestPossessionIsNotPermission`, `TestPermitsUseRequiresBothTheRecordAndTheOrder` |
| Evidence verification passes | **VERIFIED** | `TestVerifyDetectsAlteredEvidence` |
| Contradiction test passes | **VERIFIED** | `TestEveryCaseSurfacesItsContradiction` |
| Deadline test passes | **VERIFIED** | `pkg/insurance/deadline` suite + `TestNoDeadlineRuleYieldsUndeterminedNotAGuess` |
| Human review passes | **VERIFIED** | Both directions: fails closed, and opens with a complete authorization |
| Dossier export passes | **VERIFIED** | `TestEveryCaseDrivesTheFullFacadePath` |
| Independent verification passes | **INTERNAL_QUALIFIED** | Recomputable by any holder of the evidence set; not externally reviewed |
| Deterministic replay passes | **VERIFIED (R22)** | Cold replay from a serialised snapshot (no reference to the live case) reproduces the evidence root hash, preservation hash, quantum result and resolved policy version, on all seven cases — `insurance_cold_replay` readiness gate |
| Race / concurrency tests pass | **VERIFIED** | `go test -race -p 1 ./pkg/insurance/...` clean; the `race` gate is VERIFIED in `READINESS_MANIFEST.json` |

**9 VERIFIED, 2 INTERNAL_QUALIFIED, 0 OPEN, 1 BLOCKED-external (as of
R22; was 8/2/1/1).** The Definition of Done is therefore still **not
met** — one criterion (real historical case replays) is blocked on
external data that does not exist inside this repository and cannot be
fabricated to close it — and this document says so rather than reporting
the nine.

---

## 6. The §39 forbidden list — each item, and how it is prevented

| Forbidden | How it is prevented | Test |
|---|---|---|
| Waiting for real customer data | The whole pack exists and runs today | `TestEveryCaseDrivesTheFullFacadePath` |
| A custom engine per customer | One facade, one path; the pack uses it unchanged | `Drive` has no fixture-only branch |
| An AI legal verdict | `LegalQuestionStatus` has exactly one value | `TestLegalQuestionHasExactlyOneStatus` |
| An automatic liability engine | No determination field exists anywhere in the domain | `TestNoDeterminationFieldAnywhereInTheInsuranceDomain` |
| A separate evidence engine | Forbidden names scanned tree-wide; evidence minted only through the canonical contract | `TestNoForbiddenCanonicalDuplicateIsDeclaredAnywhere`, `internal/nobypass` |
| A separate trust engine | same | same |
| Hard-coding a named AIS vendor | Tree-wide source scan | `TestNoVendorJudgmentOrCompanyIsHardCodedAnywhere` |
| Hard-coding one AIS provider | `pkg/connector` is adapter-shaped; the pack's AIS source is a role, not a vendor | same |
| Hard-coding a real judgment as a rule | A real decision can only be a `HistoricalReference`, proven inert | `TestHistoricalReferenceCannotBecomeARule` |
| Hard-coding a real company as a classifier target | CASE-INS-005 is entirely role-designated; tree-wide scan | `TestNoRealWorldEntityAppearsInThePack` |
| Mixing synthetic with live data | A FIXTURE envelope is refused as external evidence | `TestFixtureEnvelopeIsRefusedAsExternalEvidence` |
| Calling a historical replay "live" | Provenance is a package constant, not a per-case field | `TestAFixtureCaseCanNeverReportAsLive` |
| Mutating original evidence | Correction supersedes; the content-addressed ID is unchanged | `TestSupersessionDeniesUseWithoutMutatingTheOriginal` |
| One opaque confidence score | No exported type in the domain has a float field at all | `TestNoOpaqueConfidenceScoreAnywhereInTheInsuranceDomain` |

---

## 7. The five registered readiness gates

As regenerated in `READINESS_MANIFEST.json` (59 gates as of R22 — was
58 — `race` VERIFIED, the eight external blockers semantically
unchanged):

| Gate | Mandatory | Status | What it actually proves |
|---|---|---|---|
| `insurance_coverage_traceability` | yes | VERIFIED | Every coverage fact on all seven cases traces to a clause on the version in force, to evidence present in the registry, to an effective date, and to a stated reason; unresolved findings always raise review |
| `insurance_quantum_reproducibility` | yes | VERIFIED | A genuine re-run of `quantum.Compute` produces an identical result on all seven; every non-zero operand cites evidence; a calculation version is declared |
| `insurance_preservation_chain_integrity` | yes | VERIFIED | All nine §56 checks pass per order, every record is covered, and the case lineage hash chain verifies |
| `insurance_human_review_enforcement` | yes | VERIFIED | Finalization is refused with no authorization **and** permitted with a complete one — both directions, because a gate that only ever refuses proves nothing |
| `insurance_cold_replay` | yes | VERIFIED (R22) | A case reconstructed from nothing but its own serialised snapshot reproduces the live evidence root hash, preservation hash, quantum result and resolved policy version, on all seven cases |

**Scope, stated rather than left to be inferred:** all five are
ENGINEERING gates over synthetic cases. They establish nothing about live
customer data, which remains the `live_data` blocker's business and is
unaffected by any of them.
