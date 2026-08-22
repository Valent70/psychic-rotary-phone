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

*(Phases are recorded here as they land. Anything not listed below has
not been implemented in this round — see the Residual Gate Register.)*

### PHASE 0 — Reconciliation

This document's Step 0 table. No code changed. Every classification
above was produced by reading the actual source, not by trusting the
mandate's own summary of it; three of the mandate's stated leads were
confirmed, and two were refined (I-03 turned out to be PARTIAL rather
than largely-satisfied, because the deadline engine covers deadlines but
not notices; X-1 splits into a prohibition that was already satisfied
and a positive binding that was entirely absent).

---

## What was deliberately NOT changed

*(Recorded per phase as the round proceeds.)*

* **The existing 16-state case machine** — see the reconciliation
  decision above. Its tests are untouched.
* **`pkg/insurance/causation`** — already satisfies I-04. Nothing was
  rebuilt; the round's only causation work is binding its outputs into
  case lineage.
* **`pkg/insurance/verification.Manifest`** — the existing evidence-root
  manifest is reused as-is by the new gates rather than re-derived.
* **The eight externally-blocked gates** — untouched, byte-for-byte.
