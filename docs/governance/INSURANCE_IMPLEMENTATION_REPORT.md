# VERIQO Insurance Intelligence & Assurance System — Implementation Report (R21)

This document records what the two frozen design documents — the
93-section *VERIQO Insurance Intelligence & Assurance System*
functional spec and the 42-section *Veriqo Insurance Final Design*
baseline (VIACP) — asked for, what this repository already had, what
changed in round R21, and what was deliberately left alone.

It follows the same discipline as every other document in this
directory: a claim of closure cites a real test by name, and a claim of
"still open" names the specific real-world resource that is missing.

The governing constraint throughout was the same rule 0 the
pre-insurance round (R20) worked under, restated by the insurance
mandate:

> IF EXISTING_IMPLEMENTATION_ALREADY_SATISFIES_REQUIREMENT → DO NOT
> REIMPLEMENT → ADD ONLY MISSING ENFORCEMENT / TEST / INTEGRATION.

That rule did a very large share of the work. `pkg/insurance/*` already
existed at the start of this round — 17 packages, ~16,800 lines, 31 test
files, built in an earlier round of this same system against an earlier
internal design its own file headers call "blueprint §N" / "VICE". Much
of what the new documents demand was therefore already present under
different section numbers. The reconciliation below classifies every
requirement before any code was written.

**The eight externally-blocked gates were not touched.** `hsm_kms`,
`live_data`, `multi_region_dr`, `pentest`, `scale_qualification`,
`soak_72h`, `spire_mtls` and `supply_chain_scan` remain BLOCKED with
their existing reasons byte-for-byte unchanged. Nothing in this round
advances, waives, or re-words any of them.

---

## Step 0 — Reconciliation

### The eight frozen domains (Final Design §2–§8)

| Domain | Requirement | Classification | Evidence for the classification |
|---|---|---|---|
| **I-01** | Policy & Coverage: policy, version, endorsement, exclusion, deductible, limit, sub-limit, named insured, voyage/territory, effective period, applicable law. Output `COVERAGE_FACT` / `COVERAGE_CONDITION` / `COVERAGE_CONFLICT` / `COVERAGE_GAP` / `HUMAN_REVIEW_REQUIRED`, never `COVERED = TRUE`. | **ALREADY CLOSED** | `pkg/insurance/policy` has `Clause` (document/page/source-hash/text-span/version/effective-date), `Version` (perils, exclusions, deductible, limits, sub-limits, effective from/to) and `History.EffectiveAt` — which resolves the version in force at the incident tick and explicitly refuses to fall back to the latest. `pkg/insurance/coverage` emits exactly `CoverageFact` / `CoverageQuestion` / `CoverageConflict` / `ReviewRequired`, and `TestCoverageAnalysisHasNoCoverageVerdictField` proves by reflection that no boolean coverage verdict field exists anywhere on the output type. |
| **I-02** | Incident & Event Reconstruction: fuse AIS/GPS/port/terminal/NOR/SOF/survey/weather/sensor/photo/email/document into a canonical event timeline; per-event `event_id, case_id, event_time, timezone, ingestion_time, source_id, source_hash, event_type, entity, location, confidence, status`. | **ALREADY CLOSED** | `pkg/insurance/timeline.Event` carries event ID, type, original + UTC time, timezone, certainty and source evidence; `Detector.DetectConflicts` surfaces temporal/duplicate/impossible-sequence/missing-interval/document-after-event/notice-before-incident conflicts rather than silently reconciling. `source_hash` is not restated on the event: it lives on the underlying content-addressed `ontology.Evidence` the event's `SourceEvidence` IDs point at, which is the non-duplication rule working as intended. |
| **I-03** | Notice & Obligation Engine: incident → policy condition → notice requirement → notice deadline → responsible party → required evidence → action → completion. **`LATE NOTICE ≠ COVERAGE DENIED`.** | **PARTIAL** | The *deadline* half was already real and strong: `pkg/insurance/deadline.Rule` carries `source_clause, source_document, effective_version, trigger_event, duration, calendar_rule, timezone`, refuses a zero duration, refuses a policy-sourced rule with no clause, and its own header forbids hard-coding "all maritime claims = 1 year". `ComputeUrgency` derives MET/OVERDUE/DUE_SOON/UPCOMING from three explicit ticks. What was **missing**: (a) the notice object itself — spec §11's `IncidentTime / DiscoveryTime / KnowledgeTime / NoticeDueTime / NoticeSentTime / NoticeReceivedTime / NoticeRecipient / NoticeMethod / NoticeContent / NoticeAcknowledgement / NoticeEvidence` existed nowhere; (b) the obligation graph `CLAUSE → OBLIGATION → TRIGGER → REQUIRED EVIDENCE → DEADLINE → RESPONSIBLE PARTY → STATUS` (Final Design §12) existed nowhere; (c) `LATE ≠ DENIED` was true only *by absence* — `coverage.noticeTimelyFact` marks late notice `DISPUTED` and there is no denial field to set — but nothing asserted the separation structurally, so a future field could have broken it silently. |
| **I-04** | Causation Engine: hypothesis, supporting evidence, contradicting evidence, missing evidence, alternative hypothesis, expert requirement, review status. Never "water ingress caused the damage". | **ALREADY CLOSED** | `pkg/insurance/causation.Hypothesis` has exactly `SupportingEvidence`, `ContradictingEvidence`, `MissingEvidence`, `Dependency`; `HypothesisSet` holds competing hypotheses; `Explain` assembles narrative only from three fixed hedge templates so a bare "X caused Y" is structurally unreachable, and the file header quotes the same prohibition the new design states. Independent-source counting goes through `evidence.DependencyGraph.IndependentCount`, so three derived documents never count as three sources. |
| **I-05** | Quantum Engine: gross loss − salvage − deductible − exclusions + recoverable costs − third-party recovery = provisional exposure, with per-value lineage `SOURCE → EXTRACTION → TRANSFORMATION → CALCULATION → RESULT`. | **PARTIAL** | `pkg/insurance/quantum` has the §20 formula, exact `int64` minor-unit arithmetic (explicitly not float64, for reproducibility), `EvidenceBackedAmount` binding every input to evidence IDs, `Calculation.IndicativeClaimValue` (deliberately not "payable"), a `CalculationVersion`, and `DetectDiscrepancy` which reports the claimed and evidence-supported figures side by side and never picks one. What was **missing**: a *reproducibility gate* — nothing anywhere re-ran a calculation and asserted that identical inputs at an identical effective tick under an identical calculation version produce a byte-identical result. |
| **I-06** | Mitigation Engine: what can still be prevented, preserved, noticed, quarantined, surveyed; recovery opportunity expiry. Never concludes liability. | **ALREADY CLOSED** | `pkg/insurance/mitigation` records actions with actor, cost, avoided loss and supporting evidence, computes `Impact`, and `TestPublicAPIHasNoReasonablenessJudgment` proves by reflection that no exported field or method expresses a legal reasonableness judgment. |
| **I-07** | Recovery / Subrogation: loss → responsible third party → recovery right → subrogation → demand → settlement → recovery → outstanding exposure, each right carrying source, legal basis, amount, deadline, responsible owner, status. | **ALREADY CLOSED** | `pkg/insurance/recovery.Target` carries party, `Basis` (category + source), supporting evidence, potential loss, notice status, limitation status (computed from a deadline tick, not set) and recovery status. `TestNoLiabilityDeterminationField` proves by reflection that no field can be read as "liability confirmed". |
| **I-08** | Dispute / Legal / Regulatory Control: claim → dispute → notice → evidence hold → legal review → negotiation → mediation → arbitration → court → settlement/award/judgment → recovery, with governing law, jurisdiction, arbitration seat, forum, limitation, notice period, evidence requirements, enforcement — as metadata and workflow, never automatic legal advice. | **MISSING** | No `pkg/insurance/dispute` package, no dispute stage machine, no forum/governing-law/arbitration-seat model, no legal-hold object, and no regulatory-matter model (allegation → investigation → finding → settlement → fine → disgorgement → monitor → certification → completion) anywhere in the repository. The two epistemic rules this domain exists to protect — *settlement ≠ every allegation proven* and *monitor requirement ≠ monitor completed* — had nowhere to live. |

### Cross-cutting architecture requirements

| # | Requirement | Classification | Evidence for the classification |
|---|---|---|---|
| X-1 | **Non-duplication** (spec §3, Final Design §1): insurance must not create a second identity, evidence store, replay engine, decision engine, correlation key, policy registry or provenance model. | **ALREADY CLOSED (as a prohibition), PARTIAL (as a positive binding)** | No forbidden construct exists: `pkg/insurance/evidence.Record` wraps a real `ontology.Evidence` and mints no ID of its own; `pkg/insurance/contradiction` is a thin adapter over the real `pkg/moat/contradiction.ArbitrationEngine`; `pkg/insurance/party` explicitly defers entity resolution to `pkg/identity` via `EntityRef`. But the *positive* half was absent — see X-2. |
| X-2 | **Consume the canonical foundation** (spec §2, §58, §92): correlation propagation, case lineage, evidence envelope, rights model. | **MISSING** | Mechanically verified before any code was written: `pkg/insurance/**` imported exactly two non-insurance VERIQO packages — `pkg/evidence/ontology` (9 sites) and `pkg/moat/contradiction` (2 sites). Zero references to `pkg/lineage`, `pkg/platform/correlation`, `pkg/governance/envelope` or `pkg/evidence/provenance` existed anywhere in the insurance tree. This was the single largest genuine gap in the round. |
| X-3 | **Rights-aware evidence** (spec §21–§22, Final Design §19 "C4 — Rights Gate"): every insurance evidence item carries a rights state; possession is never permission; `REVOKED` permits nothing. | **MISSING** | `provenance.RightsState` and its fail-closed `Permits` table already existed and are the canonical model — but `pkg/insurance/evidence` referenced neither. Insurance evidence had no rights dimension at all. |
| X-4 | **Preservation** (spec §19–§20, the fifth core claim question): `PreservationOrder` with trigger, scope, custodian, evidence types, start, deadline, legal-hold state, rights state, hash, chain of custody. | **MISSING** | `evidence.Record` carried a `ChainOfCustody []CustodyEntry` field, and that is genuinely the chain-of-custody half. Nothing else existed: no preservation order, no trigger, no custodian assignment, no legal-hold state, no release. |
| X-5 | **Case state machine** (Final Design §41 STEP 2): `INTAKE → PRESERVED → RECONSTRUCTING → REVIEW_REQUIRED → ACTION_REQUIRED → EVIDENCE_COMPLETE → HUMAN_DECISION → RESOLVED → CLOSED`, plus exception states `DISPUTED, CONTRADICTED, INSUFFICIENT, ON_LEGAL_HOLD, SUPERSEDED`. | **PARTIAL / CONFLICTING** | `pkg/insurance/case` already had its own 16-state, forward-only, one-step-at-a-time machine (`CASE_CREATED → … → DOSSIER_GENERATED → CASE_CLOSED/OPEN_ISSUES`) with real tests for the skip/backward/terminal rules. That machine is *finer-grained* than the new vocabulary and drives the facade's step ordering. The new 9-stage vocabulary did not exist, and neither did any exception-state concept. See "State-machine reconciliation" below for how this was resolved rather than guessed. |
| X-6 | **Verification gates** (spec §53–§57): coverage traceability, quantum reproducibility, preservation chain, human review enforcement. | **PARTIAL** | `pkg/insurance/verification` existed but did exactly one thing: a deterministic SHA-256 evidence-root `Manifest` over a case's evidence set, plus `Verify`. That is a real *evidence-set integrity* check and it is reused unchanged. None of the four named gates existed, and no insurance gate was registered in `internal/assurance` / `cmd/veriqo-readiness`. |
| X-7 | **Human-in-the-loop / decision states** (spec §37–§38, Final Design §14–§15): five-part output split, decision states ending at `AUTHORIZED_DECISION_RECORDED`, fail closed when mandatory review is missing. | **PARTIAL** | `dossier.Dossier` computes `HumanReviewRequired` and `HumanReviewQuestions` by aggregating other packages' already-raised flags, and `TestDossierHasNoVerdictField` proves no verdict field exists. `pkg/governance/hitl` is the canonical human-authority engine and already models reviewer packets, self-review refusal, override logging and a hash-chained ledger. What was **missing**: any binding between the two — nothing prevented a caller from treating a dossier as final while `HumanReviewRequired` was true. |
| X-8 | **Synthetic case pack** (Final Design §33–§36): CASE-INS-001 … CASE-INS-007. | **MISSING** | A repository-wide search for `CASE-INS` returned nothing. No synthetic insurance case existed in any form. |
| X-9 | **Replay** (spec §73, Final Design §20 "C5"): one engine for synthetic and historical cases; a completed claim must be replayable. | **PARTIAL** | `pkg/replay` is the canonical full-lifecycle replay engine and is not duplicated. Insurance had no binding to it and no per-case replay identity. |
| X-10 | **Forbidden list** (Final Design §39). | **ALREADY CLOSED (verified, not assumed)** | Each item checked against the tree: no vendor name (`MarineTraffic`, `ORBCOMM`) appears in `pkg/insurance`; `pkg/connector` is already adapter-shaped with no provider hard-coded into the core; no real court judgment or real company name appears as a rule or classifier target anywhere; no single opaque confidence score exists (evidence strength is nine independently-rated dimensions, causation is supporting/contradicting/missing, quantum is claimed-vs-supported side by side). The round's job here was to keep it that way while adding new types — see the guardrail tests. |

---

## State-machine reconciliation (X-5) — the decision and why

Two machines were in scope and they do not agree:

* the **existing** 16-state sequence in `pkg/insurance/case`, forward-only,
  one step at a time, with terminal states reachable only from
  `DOSSIER_GENERATED`; and
* the **new** 9-stage vocabulary in Final Design §41 STEP 2 plus five
  exception states.

The resolution taken, deliberately and recorded here rather than left
implicit:

> **The new 9-stage vocabulary is the canonical *externally reported*
> lifecycle stage. The existing 16-state sequence remains the internal
> step-ordering mechanism and is not replaced.** Each internal state maps
> to exactly one external stage by a total, tested mapping. The external
> stage is *derived*, never settable. Exception states are an additive,
> recorded overlay that never moves — forward or backward — the internal
> machine.

Reasons:

1. The internal sequence is what makes "call `AnalyzeCausation` before
   evidence is ingested" *structurally* impossible, not merely
   documented. Collapsing 16 states into 9 would delete real ordering
   constraints (there would no longer be a state boundary between, say,
   `CONTRADICTIONS_ANALYZED` and `CAUSATION_ANALYZED`) and would weaken
   the one-step-forward-only invariant that four existing tests assert.
   Deleting an enforcement to match a vocabulary is exactly backwards.
2. The new vocabulary is *reporting* vocabulary. Its own document
   introduces it in the context of a Case Room and a dossier — what a
   claims reviewer, an investor demo or an external status view sees. It
   is coarser on purpose.
3. The exception states are genuinely orthogonal to progress. A case can
   be `ON_LEGAL_HOLD` *and* `RECONSTRUCTING`; `CONTRADICTED` is not a
   position in a sequence. Modelling them as sequence members would have
   forced a false choice between "where the case is" and "what is wrong
   with it".

Consequence: no existing `pkg/insurance/case` test was weakened or
deleted, and the new vocabulary is available everywhere the old one is.

---

## What changed, phase by phase

### PHASE 0 (R21-1) — Reconciliation

This document's Step 0 table. No code changed. Every classification was
produced by reading the actual source, not by trusting the mandate's own
summary of it. Three of the mandate's stated leads were confirmed and two
were refined: I-03 turned out to be PARTIAL rather than largely
satisfied (the deadline engine covers deadlines but not notices), and the
non-duplication requirement split into a prohibition that was already
satisfied and a positive binding that was entirely absent.

### PHASE 1 (R21-2) — The canonical foundation binding, and the Rights Gate

Closes **X-2** (the round's largest genuine gap) and **X-3**.

`pkg/insurance/canonical` is net-new and is the *positive* half of the
non-duplication rule. `Binding` registers each insurance artifact as a
node on ONE `lineage.CaseID` — the insurance CaseID verbatim, because
the whole point of `pkg/lineage` is that one investigation has one
CaseID — carrying the real identifier its owning package already
produced and minting none:

| Insurance artifact | Lineage node | Ref |
|---|---|---|
| `evidence.Record` | EVIDENCE | the underlying content-addressed `ontology.Evidence.EvidenceID` |
| `party.Party` | ENTITY | its `EntityRef`, i.e. `pkg/identity`'s resolved ID |
| `policy.Version` | POLICY | the `VersionID` `History.EffectiveAt` resolved |
| `timeline.Event` | EVENT | its `EventID`, upstream = its source evidence |
| `contradiction.ContradictionRecord` | CONTRADICTION | its `ContradictionID` |
| `causation.Hypothesis` | HYPOTHESIS | its `ID`, upstream = supporting evidence only |
| `verification.Manifest` | VERIFICATION | its recomputable evidence root hash |

Three refusals worth naming, because each is a hole that would otherwise
exist:

* `AttachParty` refuses a `PartyID` with no `EntityRef`. An
  insurance-local label must never become a canonical entity — that
  would be a second identity authority by the back door.
* `AttachExternalEvidence` fails closed three times in order: an
  envelope must exist and validate; a FIXTURE envelope is refused
  outright; and the record's rights must permit the intended use.
* `AttachHypothesis` folds only *supporting* evidence into `Upstream`.
  An upstream edge means "derived from", and a hypothesis is not derived
  from the evidence that cuts against it. The full three-way
  decomposition stays on `causation.Hypothesis`.

**Rights Gate.** `evidence.Record` now carries
`provenance.RightsState` — the canonical vocabulary, consulted through
the single permitted-use table, not a second enum. The functional spec
§22 lists rights values in different words; adopting those words as a
parallel Go type would have been exactly the duplicate provenance model
§3 forbids, and `TestInsuranceDeclaresNoSecondRightsVocabulary` prevents
it. `RightsState.Permits` was extracted in
`pkg/evidence/provenance` so both `ExternalEvidence.Permits` and
insurance consult one table — a consolidation, not a duplication.

`MarkSuperseded` denies every use of a corrected record *without editing
it*: `TestSupersessionDeniesUseWithoutMutatingTheOriginal` proves the
superseded record's content-addressed ID is unchanged, which is what
keeps the earlier state replayable.

### PHASE 2 (R21-3) — The state-machine reconciliation

Closes **X-5**. The decision and its reasoning are recorded in full in
the section above. In code:

* `Stage` is the nine-value external vocabulary, derived on every call
  from a total mapping. `stageOf` has exactly seventeen entries and
  `TestStageMappingIsTotalAndSingleValued` checks the count, so a state
  added to `sequence` without a stage fails rather than silently
  reporting INTAKE.
* `TestStageIsDerivedNotSettable` proves by reflection that `Case` has
  no `Stage` field and no `SetStage`/`AdvanceStage`/`MarkStage` method.
* `Exception` is an append-only overlay guarded by its own mutex, so
  raising one cannot even take the lifecycle lock.
  `TestExceptionNeverMovesTheLifecycle` raises and clears all five at a
  mid-lifecycle point and asserts the state, the derived stage and the
  state log are all untouched; `TestExceptionsAreOrthogonalToStage`
  repeats it at every point in the sequence.
* Raising an exception requires a reason and a *cited artifact*. An
  exception nothing points at is prose, and this package refuses to
  store prose as state.
* `dossier.Dossier` now carries the derived `Status`, so the terminal
  work product reports the canonical vocabulary.

**No existing `pkg/insurance/case` test was weakened or deleted.**

### PHASE 3 (R21-4) — I-08 Dispute / Legal / Regulatory Control

Closes **I-08**, the one domain classified entirely MISSING.

`pkg/insurance/dispute` is modelled on the discipline
`pkg/insurance/causation` already uses for a structurally identical
problem — a question this system must not answer:

* `LegalQuestionStatus` has **exactly one value**,
  `LEGAL_INTERPRETATION_REQUIRED`. There is no second value for a code
  path to reach, and `Matter.AddLegalQuestion` overwrites whatever a
  caller supplied.
* `HistoricalReference` is the only shape in which a real reported
  decision may appear anywhere in this system: recorded text a human
  reads. `TestHistoricalReferenceCannotBecomeARule` proves it has no
  binding/weight/applies/rule field and no non-string field at all, so
  it cannot act as a rule.
* `Forum` requires `SourceDocument` + `SourceClause` + `SourceVersion`,
  and `TestForumRestatesNoDurations` proves it has **no numeric field**
  — a limitation period must be a `deadline.Rule` reference, which is
  precisely how "all maritime claims = 1 year" gets hard-coded when it
  is not.
* The dispute stage machine permits forward *skips* (real disputes skip
  mediation routinely) and records what was skipped; backward moves are
  refused and every move needs a reason.

`pkg/insurance/regulatory` implements the Final Design §36 chain and
carries its two CRITICAL separations:

* **Settlement ≠ every allegation proven**, enforced from three
  directions in both packages: the constructor refuses it, `Validate`
  refuses a hand-built struct carrying it, and the aggregate
  re-validates so it cannot be smuggled in. Withdrawal and
  discontinuance are covered by the same rule. The complement holds
  too — an award or a regulatory finding *can* determine an allegation,
  but never without the determining authority and the document
  paragraph cited — and a genuine prior finding **survives** a later
  settlement, because the rule is "settlement proves nothing", not
  "settlement unproves everything".
* **Monitor requirement ≠ monitor completed.** `MonitorRequirement` has
  no `Completed bool`; `TestMonitorRequirementHasNoCompletedField`
  proves the type has no boolean field *at all*. A single boolean is
  exactly what invites the failure — set at imposition time, or
  defaulted true, it silently converts an ongoing obligation into a
  discharged one. `Completed()` is derived from a real `Certification`
  naming a certifier and a source document, and `ImposeMonitor` refuses
  a monitorship that arrives already certified.

One rename was driven by a guardrail rather than a weakening of it:
`LegalHold.CoveredEvidence` became `EvidenceInScope`, because the
verdict-field scan correctly flagged "covered" as ambiguous with
insurance coverage.

### PHASE 4 (R21-5) — I-03 Notice & Obligation, and Preservation

Closes **I-03** and **X-4**.

`pkg/insurance/obligation` adds the two halves of I-03 that did not
exist — the spec §11 Notice object (eleven named temporal and
procedural facts, none of which existed anywhere) and the Final Design
§12 obligation graph. The deadline half was already real and strong and
is **not** reimplemented: every deadline is a `deadline.Rule` and every
computed tick comes from `deadline.ComputeDeadline`.

**LATE NOTICE ≠ COVERAGE DENIED** previously held only *by absence* —
the coverage engine has no denial field to set. That was true but
unasserted, and an unasserted invariant is one a future field breaks
silently. It is now asserted four ways:

1. `TestComplianceVocabularyCannotExpressDenial` scans the `Compliance`
   vocabulary itself for DENIED / FORFEIT / COVERAGE / VOID / BARRED.
2. `Assessment.CoverageEffect` is a type with exactly one value,
   `NOT_DETERMINED_REQUIRES_POLICY_AND_LEGAL_REVIEW`, checked across all
   five compliance branches.
3. A LATE or NOT_GIVEN assessment produces one review requirement naming
   the policy wording and the applicable law, and no denial language
   appears anywhere in the rendered output.
4. `TestLateNoticeNeverSetsACoverageOutcome` in `pkg/insurance/coverage`
   proves a late notice moves the notice fact to DISPUTED and leaves
   **every other coverage fact byte-identical** to the timely run.

Other honesty properties proven here: an unknown notice time is
INSUFFICIENT_EVIDENCE, never LATE; a missing deadline rule is
UNDETERMINED, never a guessed period; a clause running from discovery
never silently falls back to the incident time; a missing
acknowledgement is uncertainty explicitly labelled "not evidence that it
was not received"; and a late-but-given notice discharges the duty
(COMPLETED with a recorded delay) rather than leaving it OVERDUE.

`pkg/insurance/preservation` implements the spec §19 field list verbatim
and the §20 workflow as the order's lifecycle. Three deliberate
non-duplications: `RightsState` is provenance's; per-item chain of
custody stays on `evidence.Record`; retention and deletion stay with
`pkg/governance/data` (an Order deletes nothing and expires nothing).
`Order.Hash` is a hash-of-hashes over the sorted content-addressed
EvidenceIDs plus the order's own fields — detectable on any change,
independent of arrival order. `PermitsUse` requires **both** the
record's rights and the order's, so a permissive order can never widen a
restricted record.

`TestAnOrderWithNoAccessEventsStillPasses` is a deliberate
*non*-requirement: an order nobody has touched is intact, and demanding
an access event would push a caller to fabricate one.

### PHASE 5 (R21-6) — The four gates and the synthetic case pack

Closes **X-6** and **X-8**.

`pkg/insurance/verification` gains the four §54–§57 gates. Every report
derives `Pass()` from a list of **named** failures and has no settable
pass field; every failure names the specific artifact, not a count.
Nothing re-implements an analysis — each gate checks what another
package produced, and the existing evidence-root `Manifest` is reused
unchanged.

Two design notes that matter:

* The §55 gate is a **genuine recomputation** of `quantum.Compute` over
  the recorded inputs, not a hash comparison. Its operand comparison and
  failure emission are order-stable, because the report is content-hashed
  into a readiness artifact and an artifact whose bytes vary between
  identical runs is not evidence.
* The §57 gate does **not** re-derive whether review was required.
  `dossier.Generate` already computed that by aggregating other
  packages' own flags, and a second opinion would be a second authority.
  It checks only that the required authorizations exist and are
  well-formed.

`pkg/insurance/casepack` is CASE-INS-001 … 007, all seven, all
fictional. `Drive` runs each through the **real** facade — the Final
Design §20 "one engine, not two" rule applied to fixtures. All seven
reach DOSSIER_GENERATED; the three provable gates PASS on all seven; the
§57 gate FAILS CLOSED on all seven, because the pack deliberately
supplies no authorization. A pack that handed itself a rubber stamp
would exercise the permissive path and never the one that matters —
`TestHumanReviewGateOpensWithARealAuthorization` proves the other
direction separately.

**Two existing guardrails caught this work and were obeyed rather than
relaxed**, which is worth recording because it is the discipline working:

1. `internal/nobypass` flagged the case pack as an unauthorized
   canonical-evidence ingestion path. Rather than widen that allowlist
   to accommodate test data — which would weaken a real gate — the
   construction moved *inside* the canonical contract as
   `evidenceapi.SyntheticDocument`, which stamps SYNTHETIC into the
   hashed attributes so a record's fixture provenance cannot be
   separated from the record or overridden by a caller.
2. `pkg/insurance/party`'s role-count test caught the deliberate
   addition of nine roles. Each is named explicitly by one of the two
   design documents (six from the spec §30 recovery list, RESPONDENT for
   I-08, REGULATOR and AUDITOR for its regulatory half). The count was
   re-asserted rather than dropped, and each added role is checked to be
   genuinely registered. Recording a supervisory authority as a
   PORT_AUTHORITY would have been a mislabel.

A third defect was found and fixed during this phase: the pack's
evidence and its events were initially on two different time scales
(opaque ticks vs. Unix epoch seconds), which manufactured
document-issued-after-event conflicts on every record. Both now project
from one scenario-hour scale via `EpochSeconds`, so the conflicts each
case surfaces are the ones it was designed around.

### PHASE 6 (R21-7) — Gate registration, matrix, manifest

The four gates are registered as **mandatory** gates in
`cmd/veriqo-readiness`, driven by `casepack.RunAssurance`, each
attaching a content-hashed artifact produced by actually running all
seven cases.

`docs/governance/requirements.json` gains R-068 … R-075 (phase `INS`),
and `REQUIREMENT_TRACEABILITY_MATRIX.md` is regenerated from it — the
`traceability_matrix` gate confirms the two match.

`READINESS_MANIFEST.json` was regenerated **with race enabled**. An
earlier `--skip-race` run would have silently dropped the `race` gate
from the manifest; that was caught and redone. Result:

* **54 → 58 gates**, nothing dropped, `race` still VERIFIED
* all four `insurance_*` gates VERIFIED and Mandatory
* **the eight externally-blocked gates are semantically unchanged** —
  status, blocked reason, mandatory flag and all four axes byte-identical.
  Their evidence *artifact hashes* differ only because those artifacts
  embed this run's commit and its measured durations, which is what a
  re-run artifact is.

### PHASE 7 (R21-8) — Whole-tree guardrails

`pkg/insurance/guardrails` holds no production code. The per-package
guardrails are real and stay where they are, but a per-package test can
only see its own package, and the failure this project needs to prevent
is a new type in a *new* package added by someone who did not read the
design documents. The tests here parse every non-test file under
`pkg/insurance` and assert four rules across the whole domain at once:
no determination field, no opaque confidence score, no forbidden
canonical duplicate, no hard-coded vendor / judgment / company.

The scan found three things, each fixed rather than exempted:

* A comment in `pkg/insurance/dispute` quoted the design document's
  prohibition *by naming the decision it forbids hard-coding*. The
  comment was rephrased: the rule is the point, not the case name.
* The confidence allowlist was pre-populated with two guesses that do
  not exist. It is now **empty**, and that is a finding: no exported
  type anywhere in `pkg/insurance` carries a float field at all.
* The scan's own reachability check counted packages by fields found,
  which under-reported `pkg/insurance/canonical` (whose `Binding`
  deliberately has only unexported fields). It now counts walked
  directories.

Both allowlists are themselves tested for staleness — an exemption
naming a field that no longer exists is a hole waiting for a new field
to fall into.

---

## What was deliberately NOT changed

* **The existing 16-state case machine.** See the reconciliation
  decision above. Its tests are untouched.
* **`pkg/insurance/causation`** — already satisfies I-04 in full. Nothing
  was rebuilt; the round's only causation work is binding its outputs
  into case lineage.
* **`pkg/insurance/verification.Manifest`** — the existing evidence-root
  manifest is reused by the new gates rather than re-derived.
* **`pkg/insurance/recovery`, `mitigation`, `timeline`, `coverage`,
  `quantum`, `policy`, `claim`, `gap`, `contradiction`, `dossier`** —
  classified ALREADY CLOSED against their domains. The only edits are
  additive: a `Status` field on the dossier, and one new test file each
  for coverage and party.
* **The eight externally-blocked gates** — untouched, and proven so.
* **A second replay engine.** The functional spec §73 and Final Design
  §20 both require per-case replay. `pkg/replay` is the canonical engine
  and was deliberately not duplicated; the *binding* to it is recorded
  as genuinely deferred in the Residual Gate Register rather than
  claimed.
