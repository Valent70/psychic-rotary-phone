# Pre-Insurance Closure Program — Implementation Report (R20)

This document records what the "VERIQO PRE-INSURANCE CLOSURE PROGRAM"
mandate asked for, what this repository already had, what changed, and
what was deliberately left alone. It follows the same discipline as
every other document in this directory: a claim of closure cites a real
test by name, and a claim of "still open" names the specific real-world
resource that is missing.

The mandate's own rule 0 was the governing constraint throughout:

> IF EXISTING_IMPLEMENTATION_ALREADY_SATISFIES_REQUIREMENT → DO NOT
> REIMPLEMENT → ADD ONLY MISSING ENFORCEMENT / TEST / INTEGRATION.

That rule did most of the work. Of the eighteen numbered items, four
were already closed outright, ten were partial (existing machinery
missing enforcement, a contract declaration, or a proof), and four were
genuinely missing. Only the missing and partial deltas were
implemented.

**Insurance was not entered.** Rule 8 ("do NOT enter Insurance
implementation") was respected literally. `pkg/insurance/*` was read
during reconciliation and not modified. The one place this program
touches adjacent ground — case lineage (P0-5) — deliberately implements
no claim, coverage, quantum or dispute concept; it is the foundation
such work would later sit on, and `pkg/lineage`'s own doc comment says
so.

**The eight externally-blocked gates were not touched.** `hsm_kms`,
`live_data`, `multi_region_dr`, `pentest`, `scale_qualification`,
`soak_72h`, `spire_mtls` and `supply_chain_scan` remain BLOCKED with
their existing reasons verbatim. Phase E3 changed only how their status
is *modelled and reported*, and
`TestAxisSeparationNeverAdvancesTheGateItself` proves `EffectiveStatus`,
`Satisfied`, `Assess` and the release verdict are all provably
unchanged by that change.

---

## Step 0 — Reconciliation

Every numbered item, classified against the actual repository before any
code was written.

| # | Item | Classification | Evidence for the classification |
|---|---|---|---|
| P0-1 | Canonical Evidence Write Authority | **PARTIAL** | `pkg/evidence/api.Facade` existed with Submit/Validate/Resolve/Link/Correlate/Arbitrate/Replay/Verify plus the Raw\*/Fusion\* families; `internal/nobypass` already gated three constructors. Missing: the canonical contract was never *declared* as a thing, and no scan covered ingestion paths. |
| P0-2 | Canonical Identity Authority | **PARTIAL** | `pkg/identity` was the resolver and `entityconsistency.ScanProductionAuthority` proved no second `entity.Registry` is constructed. Missing: writer counting, independent-legacy-merge counting, explicit maritime mapping, and the `legacy_identity_fallback_used` / `human_review_required` markers. |
| P0-3 | Canonical Execution Path | **PARTIAL** | Only two `execution.NewEngine` sites existed, and `TestRegistryNeverProducesGovernedDecisionArtifacts` covered exactly two files. Missing: a whole-tree negative test and an entrypoint matrix. |
| P0-4 | Correlation Context | **PARTIAL** | `pkg/platform/correlation.Key` carried all eight identifiers and had a real production caller. Missing: the adversarial suite proving tampering *fails* rather than being silently repaired. |
| P0-5 | Case Lineage | **MISSING** | Nothing aggregated an investigation under one CaseID. `hitl.Case` is a reviewer workflow; `telemetry.Correlation.CaseID` is a span attribute nothing walks; `correlation.Key` joins one execution; `pkg/insurance/*` is out-of-scope business logic. |
| P0-6 | Evidence Envelope | **PARTIAL** | `qualification.ExternalEvidence` carried commit/source_hash/build_hash/environment/measurement; `provenance.ExternalEvidence` carried origin/rights/attestation. Missing: one envelope carrying both, plus binary_hash, sbom_hash, artifact set + root, valid_from, limitations, and any way to say "this is a fixture". |
| P0-7 | Evidence Freshness Gate | **MISSING** | No freshness concept, and no `BLOCKED_STALE_EVIDENCE` outcome anywhere. |
| P0-8 | Readiness Engine Separation | **MISSING** | `internal/assurance` collapsed every gate to one status. |
| P1-9 | Temporal Bayesian Contract | **PARTIAL** | `hbayes`, the five-field `CalibrationRecord`, and `ErrTemporalCalibrationRequired` all existed. Missing: the model-risk metadata and an execution-state enum distinguishing "required but missing" from "optional and skipped". |
| P1-10 | Real-World Calibration Interface | **PARTIAL** | `corpus.go` already had LabeledEvent / Dataset / Dataset.Hash / a real frequentist `Fit`. Missing: Holdout, Evaluation, Model binding, and an `EXTERNAL_DATA_REQUIRED` status. |
| P1-11 | Operational Observability | **PARTIAL** | `pkg/platform/telemetry` had the full schema and a real Recorder, and said in its own comment that it "is not an OTLP exporter and does not pretend to be". Missing: the exporter/collector/store/query layer. |
| P1-12 | Leakage | **MISSING** | No redaction boundary and no leakage suite existed. |
| P1-13 | Replay Completeness | **PARTIAL** | Cross-process cold replay existed and proved evidence root, decision, explanation and certificate. Missing: the enumerated thirteen-identity assertion set. |
| P1-14 | Reproducible Build Provenance | **PARTIAL** | Binary equality existed, plus the cross-runner GH Actions workflow. Missing: anywhere to *store* provenance. |
| P1-15 | Sandbox Hardening | **PARTIAL** | Real seccomp-BPF and `PR_SET_NO_NEW_PRIVS` existed in `cmd/veriqo-plugin-shim`; `pkg/kernel/sandbox` had only `InProcessEnforcer` and `UnenforceableEnforcer`. This exact gap was already recorded in `AUDITOR_PRIORITY_RECONCILIATION.md`'s R19 addendum as investigated-but-not-attempted. |
| P1-16 | External Evidence Validator | **ALREADY CLOSED (nine of ten checks)** | `qualification.Registry.Validate` already checked artifact hash, provider, reviewer, both signatures, gate authorization, revocation, expiry, commit and source hash. Only the envelope bridge was missing. |
| P1-17 | Architecture Coverage Gates | **PARTIAL** | Two of the six existed under other names (`truth_arbitration_no_bypass`, `canonical_entity_authority_coverage`). |
| P2-18 | External Qualification Harness | **ALREADY CLOSED** | All eight harnesses exist in `pkg/blockers/*` with `orchestrator.RunAll` driving them. Only per-capability reporting was missing. |

---

## What changed, phase by phase

### PHASE A (P0-1) — Canonical Evidence Write Authority

**Reused, not rebuilt:** the Facade, the five ingestion adapters
(`pkg/connector/{aisstream,sar,bol,insurance,payment}`), the single
`canonical.Pipeline`, and `internal/nobypass`'s existing walk.

**New:** `pkg/evidence/api.Contract()` declares the canonical evidence
contract by *composing* the versions the owning packages already publish
rather than inventing a number of its own — so the descriptor cannot
drift from the code, and its hash moves when any component does
(`TestContractHashChangesWhenAComponentDoes`). A second scan,
`nobypass.EvidenceProductionCoverage`, answers a different question from
the existing one: `truth_arbitration_no_bypass` proves no second
evidence *authority* exists; the new `canonical_evidence_production_coverage`
gate proves no second *ingestion path* exists, because a caller that
never touches an engine but mints canonical records still bypasses the
contract. Both scans share one walk (`nobypass.checkSet`) rather than a
second copy that could drift.

**Honest scoping recorded in the source:** `provenance.RightsState` is
deliberately outside `ontology.Evidence`'s content hash (that package
documents why), so `TestRightsPreservation` asserts what is actually
true — projection never *upgrades* rights, and a REVOKED envelope
permits nothing — rather than a field-equality that would be false.

### PHASE B (P0-2) — Canonical Identity Authority

**New:** `entityconsistency.ScanIdentityAuthority` counts production
identity *writers* (must be exactly 1 — a registry built once can still
be written from many places, and a write is what merges identities),
independent legacy merges (must be 0), and folds in the existing
registry-construction scan rather than repeating it. The maritime
mapping is now explicit and exhaustive: every one of the eleven
`maritime.EntityKind` values is either mapped to an `identity.Kind` or
marked `UNMAPPED` **with a stated reason**, and a kind with no row at
all is a failure — because "we forgot" and "we decided not to model
this" must not look identical. Four are honestly UNMAPPED: VOYAGE,
CARGO, SANCTION_ENTRY, OWNERSHIP.

`RunUnified`'s legacy fallback now sets
`LegacyIdentityFallbackUsed` / `HumanReviewRequired` /
`UnmappedAliasKinds` on the Result and on the trace, and
`TestLegacyIdentityFallbackIsLoudlyMarked` additionally proves the
fallback never writes the canonical identity ledger.

### PHASE C (P0-3) — Canonical Execution Path

**New:** `internal/entrypoints` — the whole-tree version of the
pre-existing two-file test, plus an audited entrypoint matrix covering
all nine entrypoint kinds. Each matrix row is checked against the real
source: a governed-decision row claiming a canonical path its own file
does not take is a failure, not a comment.
`TestNoGovernedDecisionOutsideCanonicalExecution` passes only if
parallel governed execution paths = 0, and
`TestAuditDetectsAnInjectedBackDoor` proves the scanner detects rather
than passing by never finding anything.

**Limitation stated in the source rather than hidden:** the
`ExecutionRootHash` marker is assignment-shaped, and a textual scanner
cannot distinguish minting from copying. Every allowlisted file was read
and classified individually; the marker's value is that it forces that
classification to recur in review for any new file.

### PHASE D (P0-4) — Correlation Context

**New:** an adversarial suite running through the *real* verification
machinery (`execution.Run`'s Context validation,
`ReplayDAGWithResult`'s node-by-node comparison, `replay.Engine.Replay`'s
fingerprint chain) rather than asserting on the struct. Each test
asserts both halves: the tampered run fails, **and** the identifier was
not quietly rewritten to something plausible.

**A real defect this found:** `execution.Context.validate` did **not**
require `EvidencePackageID`. A governed execution could run, produce a
decision, and emit a correlation key with an empty evidence-package
identity — meaning the one join tying a decision back to the evidence it
was made from did not exist for that run.
`pkg/platform/correlation`'s own `Key` doc comment already described
that field as one `Context.validate` makes mandatory; the description
was aspirational. It is now true. Every production caller already set
it, so nothing that was correct before is affected.

### PHASE D2 (P0-5) — Case Lineage

**Genuinely net-new.** `pkg/lineage.Ledger` registers Intent, Evidence,
Entity, Event, Contradiction, Hypothesis, Policy, Decision,
Verification, Replay and Outcome as hash-chained nodes under one
`CaseID`, each carrying the real identifier its owning subsystem
produced. `Attach` refuses an unknown kind, an empty ref, a duplicate,
and — the load-bearing one — any upstream that does not already resolve
on the same case, because a lineage with a hole in it is not a lineage.

`Completeness` is derived: no field can be set to claim a case is
complete. `FromCorrelation` binds to the **existing**
`correlation.Key` rather than defining a second identifier set, and
omits a node entirely for an empty key field instead of inventing a
placeholder.

Real production wiring: `lifecycle.Orchestrator.Lineage` (opt-in and
nil-safe, exactly like `TemporalCalibration`). The OUTCOME node is
attached by `RecordOutcome`, **not** `RunUnified`, because ground truth
does not exist at case-run time — so a case honestly reports
`Complete=false` until an outcome is recorded.

### PHASE E / E2 / E3 (P0-6, P0-7, P0-8)

**E — `pkg/governance/envelope`:** one envelope for external and
internal evidence. It reuses `qualification.TrustRegistry` for
providers/reviewers and `provenance`'s OriginClass/RightsState/
AttestationState verbatim rather than defining second vocabularies. New:
the release-identity quadruple carried together with the artifact set
and root, the validity window, declared limitations, and an explicit
FIXTURE/LIVE classification that `Check` verifies against `OriginKind`
instead of believing.

**E2 — freshness:** `Freshness` reports the named
`BLOCKED_STALE_EVIDENCE` status rather than a generic failure, so a
stale-evidence refusal can never be misread as an engineering defect in
the gate's own subject matter. A release declaring no value to compare
against fails closed rather than silently passing.

**E3 — readiness axes:** `internal/assurance/axes.go` separates every
gate into ENGINEERING / INTERNAL / EXTERNAL / FINAL. Retrofitted onto
all eight blocked gates: their ENGINEERING axis is backed by a real
in-process run of the same `pkg/blockers/orchestrator` that
`cmd/veriqo-qualification` already drives, and their INTERNAL axis by
the real in-sandbox drill logs already committed to `evidence/`. Axes
are **derived** from the same artifacts `EffectiveStatus` reads; there
is no setter that writes PASS into an axis.

### PHASE E4 (P1-16) — External Evidence Validator

Deliberately an **integration**, not a second validator. Nine of the ten
named checks already existed. The missing piece was the bridge:
`Envelope.ToQualificationEvidence` folds the envelope-only fields
(binary_hash, sbom_hash, artifact_root_hash, classification,
origin_kind, rights_state, attestation, limitations) into the
measurement map so they are covered by the artifact hash both signatures
are made over — proved field by field, i.e. none can be edited after
signing.

`Submit` never calls `Qualify` or `VerifyGate`: it stops at
EVIDENCE_VALIDATED, because advancing a gate is an operator decision
with a named person behind it.

### PHASE E5 (P1-17) — Architecture Coverage Gates

All six now registered mandatory. Four are whole-tree scans. Two —
`policy_registry_usage_coverage` and
`temporal_calibration_usage_coverage` — are deliberately **runtime**
tests rather than scans: "every governed decision commits to the policy
that governed it" is a fact about the DAG, and grepping for it would
prove only that certain text appears in certain files, which is exactly
the evidence standard this project refuses elsewhere.

### PHASE F / F2 (P1-9, P1-10)

**F:** fifteen model-risk metadata fields, each individually required,
plus an `ExecutionState` enum keeping REQUIRED_BUT_MISSING distinct from
OPTIONAL_AND_SKIPPED. Before this, "skipped because no model exists" and
"skipped because the policy did not need one" were the same observable
outcome; for a governed decision those are completely different facts.
`NewContract` refuses every combination that would let a required
calibration be recorded as a skip.

**F2:** deterministic hash-based `Holdout` (so a table is never scored
on its own training data, and inserting one event does not reshuffle the
split), `Evaluate` reusing `pkg/moat/reliability`'s existing
LogLoss/Brier/ECE rather than a second metric implementation, and
`CalibratedModel` binding corpus+fit+evaluation so none can be quoted
without the others. Status is derived from the operator's
**declaration**, never from performance:
`TestNoAmountOfRunningTheMachineryRaisesTheStatus` feeds it 10× more,
perfectly-separable fixture data and confirms it still reports
`EXTERNAL_DATA_REQUIRED`.

### PHASE F3 (P1-13) — Replay Completeness

No second replay engine. The test launches the **same**
`cmd/veriqo-cold-replay` binary the existing cross-process tests use and
parses a machine-readable identity block that binary now emits beside
its unchanged human-readable report. All thirteen identities are
asserted **non-empty in the originating process** before being compared,
so the comparison cannot pass vacuously. The block is deliberately
absent on a diverged replay: those values describe a run that did not
reproduce.

### PHASE H (P1-11, P1-12) — Observability and Leakage

The transport adapter `veriqo_telemetry.go`'s own comment anticipated.
OTel-**compatible**, not OTel: the wire shape is hand-built because the
`zero_dependency` gate is a real enforced architectural property.

Redaction happens at the **exporter**, not the call site, because a
policy depending on every caller remembering to redact is not a policy.
Default-deny by allow-list, with value patterns as defence-in-depth for
sensitive content arriving under an allow-listed key. The leakage suite
asserts each canary appears **nowhere** in the collector — not merely
that one field was redacted, which would pass while the same value
leaked through another field.
`TestCorrelationIdentifiersSurviveRedaction` is the counterweight that
stops a redact-everything implementation passing trivially.

### PHASE I (P1-14) — Build Provenance

A storage-and-validation contract, not an attestation framework — no
in-toto layouts, no DSSE envelopes, no SLSA level assessment, because
nothing here has a contractual requirement for them and inventing one
would be a large surface with no consumer. `internal/environment.Identity`
is embedded whole and `internal/sbom`'s document cited by hash.

An unanswerable field stays **empty** in the Record and is reported
UNKNOWN by `Describe()`; the marker is report vocabulary and never
enters stored provenance. `BuilderFromEnvironment` returns empty outside
CI rather than falling back to a hostname — a workstation hostname is
not a builder identity, and writing one would make an UNKNOWN look
answered.

### PHASE J (P1-15) — Sandbox Hardening

Closes exactly the gap R19's own audit note recorded and left open.
`sandbox.OSEnforcer` reimplements **no** confinement primitive —
namespaces come from `pkg/kernel/plugin`, seccomp and no-new-privs from
the shim, cgroup v2 from `pkg/kernel/resource` — and adds the honest
mapping from a Policy clause to the primitive that must be present.
`Probe()` reads `/proc` and `/sys` rather than inferring from GOOS.

Seven negative tests, one per named escape vector, each proving both
that the policy engine denies the capability **and** that the enforcer
refuses the policy outright when the containing primitive is absent.
`TestSupportedButUnappliedPrimitiveIsNotTreatedAsClosed` covers the
subtlest case: a kernel that supports no-new-privs confines nothing if
the shim that applies it is not deployed.

### PHASE K (P2-18) — External Qualification Harness

Reconciliation found this almost entirely already closed. Nothing was
rebuilt. What did not exist is per-**capability** honesty: the
orchestrator reports READY_FOR_REAL_QUALIFICATION per *blocker*, which
answers "does a harness exist for DR" but not "does the DR harness
exercise failback, or only failover". `blockers.CapabilityRegister` has
34 rows, each naming the Go symbol that implements it, with
`TestEveryCapabilityCitesRealCode` verifying the symbol is genuinely
*declared* in that package's non-test source.

---

## What was deliberately NOT changed, and why

- **`pkg/insurance/*`** — read during reconciliation, not modified.
  Rule 8 excludes it from this round.
- **The eight blocked gates' status and blocker strings** — untouched
  verbatim. Only the reporting axis around them is new.
- **`pkg/moat/hbayes`, `pkg/moat/reliability`, `pkg/canonical`,
  `pkg/replay`, the Facade's nine methods** — all already satisfied
  their requirements; only enforcement, contract declaration and proof
  were added around them.
- **A second replay engine, a second validator, a second correlation
  key, a second provider trust model, a second metric implementation** —
  each was explicitly considered and rejected as the duplicate
  abstraction rule 0 forbids. Where a bridge was needed, a bridge was
  built.
- **Cloud SDKs for a real KMS adapter** — unchanged from the standing
  decision recorded in `AUDITOR_PRIORITY_RECONCILIATION.md`: the
  `zero_dependency` gate is a real, enforced property, and trading it
  for a partial step toward a gate that still needs paid credentials was
  judged not worth making.
- **`pivot_root` filesystem confinement, user-namespace remapping, an
  allowlist-style seccomp profile** — all remain honestly open, recorded
  in `sandbox.Qualification()`'s `NotProven` list rather than quietly
  omitted.

---

## Standing honest ceilings

Two phases produce machinery whose honest ceiling is
`INTERNAL_QUALIFIED`, stated in code rather than in prose so it cannot
be quietly overstated later:

- `telemetry.PipelineQualification()` — the export pipeline's semantics
  are proven; conformance against a real collector, real network
  delivery with backpressure and retry, production-volume retention, and
  the existence of any collector deployment at all are not.
- `sandbox.Qualification()` — the policy engine and the enforcer's
  refusal logic are proven; that a genuinely hostile binary is contained
  is not, and requires an adversarial drill on a production kernel.

Both list what would raise them. Neither can be raised by a code change
in this repository.
