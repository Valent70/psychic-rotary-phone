# RWC v2 — Independent Audit Report

Round R23. This is an adversarial audit of round R22's own RWC v2 work,
conducted as if by an external auditor hostile to the project. Its purpose is to
find out where the previous round's claims were too strong and correct them —
not to defend the implementation.

Section 1 (the native path trace) is a separate document:
`docs/RWC_V2_NATIVE_EXECUTION_PROOF.md`. Sections 2–12 are below.

Where a finding required a code change, the change is in commit `R23-1` and is
described in the relevant section. No feature was added, no RWC-003 was created,
and nothing was improved except to correct a false claim or an invalid test.

**Three things the previous round asserted turned out to be false and are
corrected here.** They are listed up front rather than buried:

1. RWC-002's vessel identity was reported `CORROBORATED`. Nothing outside the
   claim had been consulted. Corrected to `STRUCTURALLY_VALIDATED` (§3).
2. The evidence bundle was described as cold-replayable in a separate process.
   It was not; all ten cases failed with a usage error. Corrected by exporting
   the identity ledger, after which 10 of 10 pass (§6).
3. A mutation subtest was named `..._flips_baseline_to_FAIL` and does not mutate
   against the baseline. Renamed (§5).

---

## 2. False-positive audit — can the adapter produce PASS/FAIL without the engine?

**Yes, for the `Verdict`. Demonstrated, not argued.**

`InterpretVerdict(cr ConstraintResult, dec decision.Decision)` selects the
verdict from a switch that reads only `cr`. `dec` is consulted solely to build
the `consistencyWarning` string. Handing it a zero-valued `decision.Decision` —
which is what you get when no engine ran at all — returns the identical verdict:

| Decision passed in | Verdict | Warning |
|---|---|---|
| The real engine's (`ESCALATE`) | `FAIL` | *(empty)* |
| `decision.Decision{}` — no engine at all | `FAIL` | *"native decision.Action= does not match the action expected from ConstraintResult (ESCALATE)"* |
| A deliberately wrong one (`MONITOR`) | `FAIL` | *"native decision.Action=MONITOR does not match..."* |

Pinned by `TestAuditVerdictIsProducibleWithoutTheNativeEngine` in
`pkg/rwc/audit_test.go`, which fails if this ever stops being true — so the
finding cannot go stale.

### Why this does not make the corpus meaningless

The bypass is **detectable and detected**. The warning fires in both failure
cases, and every real caller treats a non-empty warning as a failure:

- `cmd/veriqo-rwc-v2` sets `allPass = false` and exits non-zero.
- `TestRWC001AdversarialCandidates` calls `t.Errorf` on it.

So no shipped artifact can contain a verdict that disagrees with the native
decision. But that is a *guardrail*, not a derivation. The precise, honest
statement is:

> The RWC `Verdict` is computed by the adapter's own arithmetic. The native
> decision is used to cross-check it and a disagreement is fatal. A verdict is
> therefore corroborated by native execution, not produced by it.

Round R22 already wrote this into `Verdict`'s doc comment and
`InterpretVerdict`'s. This audit confirms both are accurate and adds the
executable demonstration. **No further code change was made**: turning the
warning into a hard error inside `InterpretVerdict` would be a behavioural
improvement, and this audit's mandate forbids improvements.

### Could the whole pipeline be faked?

No. `cmd/veriqo-rwc-v2` cannot reach a decision without `RunUnified`, because
`pkg/rwc.Run` is the only path it has and `Run` does nothing but build inputs and
call it. It cannot construct its own engine: `internal/entrypoints` fails the
build on any `execution.NewEngine(` outside three audited files, and
`internal/nobypass` does the same for `fusion.NewEngine(`,
`contradiction.NewArbitrationEngine(`, `canonical.NewPipeline(` and
`ontology.New(`. `pkg/rwc` and `cmd/veriqo-rwc-v2` are on none of those lists.

---

## 3. Corroboration audit

**Structural consistency is not independent corroboration. RWC v2 was calling it
that. Corrected.**

### The question the mandate asks, answered

For RWC-002 vessel identity: **(A) only IMO/MMSI structural validation
occurred.** No external source corroborated the identity.

The evidence, read from the code rather than from comments:

- `pkg/rwc/identity_checks.go` `ValidateIMOCheckDigit` implements IMO Resolution
  A.600(15): digits 1–6 weighted 7,6,5,4,3,2, summed, mod 10, compared to digit
  7. For the corpus's claimed IMO `8508292`: 56+30+0+32+6+18 = 142; 142 mod 10 =
  2; claimed check digit 2; **valid**. This is arithmetic over the claimed
  number. It consults nothing.
- `LookupMMSICountry` indexes a seven-entry hard-coded map by the MMSI's first
  three digits. `525` → Indonesia, consistent with the claimed vessel name
  (`GUNUNG KEMALA / PERTAMINA 8003`). This is a prefix lookup in a local table.
  It consults nothing.
- There is no network call anywhere in `pkg/rwc`. `go list -deps ./pkg/rwc/`
  reaches neither `net/http` nor `crypto/tls`, so no HTTP or TLS client is even
  linkable from this package. (`net` and `net/url` do appear, pulled in
  transitively by the telemetry and URL-parsing helpers further down the
  dependency tree; neither is used here to open a connection.)

A check digit proves a number is well-formed. It cannot prove the vessel exists,
and it cannot prove the number belongs to that vessel — a forger who wants a
valid IMO number computes one, from published arithmetic.

### The second, subtler error

`ClassifyProvenance` also read `res.Provenance.Score` without reading
`res.Provenance.Status`. Every RWC v2 case produces, in the DAG's own committed
trace:

```
independence 1 (UNKNOWN), posterior 0.9499999999999999
```

`StatusUnknown` means, in `pkg/moat/provenance`'s own words, *"NONE of the
sources have any declared ancestry at all"* — flagged distinctly from
`DECLARED_INDEPENDENT` precisely so a consumer can tell *"we checked and found
nothing"* from *"we never had data to check"*. The accompanying score of `1.0` is
the trivial value returned when there are no pairs to compare. The old rule
treated that trivial 1.0 as evidence of independence.

### What changed

`pkg/rwc/types.go` gains one constant and one small vocabulary:

- `StatusStructurallyValidated`, sitting between `UNVERIFIED` and
  `CORROBORATED`.
- `SourceEpistemicKind`: a source is an `OBSERVATION` (a party reporting on the
  world) or `DERIVED_FROM_CLAIM` (a computation whose only input is the claim).
  `VERIQO_STRUCTURAL_IDENTITY_VALIDATOR` is declared as the latter.

`ClassifyProvenance` now requires two independent **observations** *and*
`Status == DECLARED_INDEPENDENT` before it will say `CORROBORATED`.

### Every RWC-002 classification, before and after

| Case | R22 said | R23 says | Why |
|---|---|---|---|
| `RWC-002-VESSEL_IDENTITY` | `CORROBORATED` | **`STRUCTURALLY_VALIDATED`** | One broker observation + one derived check; provenance status `UNKNOWN` |
| `RWC-002-VOYAGE_POSITION` | `UNVERIFIED` | `UNVERIFIED` | Single broker source |
| `RWC-002-CARGO_IDENTITY` | `UNVERIFIED` | `UNVERIFIED` | Single broker source |
| `RWC-002-DOCUMENT_EXISTENCE` | `UNVERIFIED` | `UNVERIFIED` | Single broker source; no document was inspected or hashed |
| `RWC-002-TRANSACTION_SEQUENCE` | `UNVERIFIED` | `UNVERIFIED` | Single broker source |

**No case in RWC v2 reaches `CORROBORATED`, and none can** while this
environment has no path to an independent external source. `CONTRADICTED` also
never occurs: `evidence_export.json` reports `contradiction: false` for all ten
cases, which is expected — nine cases have a single source and the tenth's two
sources agree.

The test that encoded the overclaim was itself invalid.
`vessel_identity_corroborated_by_structural_check` states the error in its own
name once read slowly. It now asserts the honest status *and* explicitly fails on
the old one.

---

## 4. Decision audit — are PASS/FAIL results hard-coded?

**The results are computed. The expectations are hard-coded, which is what a
test oracle is. But one class of test could have passed vacuously, and that is
corrected.**

### The results are not hard-coded

`EvaluateVesselAtPort` (`pkg/rwc/constraints.go`) contains no vessel name and no
case ID. Verified by reading it: every branch keys off `Port` table fields and
`Vessel` numeric fields only. The same function produces all five RWC-001
outcomes; changing a field changes the outcome (§5).

The native decision is likewise computed, through the chain traced in the
execution-proof document §3, and lands where the arithmetic says it should:
ratio 0.0 → MONITOR, 0.5 → FLAG, 1.0 → ESCALATE for all ten cases.

### The vacuity risk that was real

`TestRWC001AdversarialCandidates` asserts `Verdict` values. Since `Verdict` does
not depend on the engine (§2), that test's *primary* assertion could in principle
be satisfied with the engine absent. It is saved only by its one
`if warn != ""` line.

That is too thin a thread for the mandate's "remove or flag any test that could
pass without exercising actual decision computation". R23 adds
`TestAuditCausalMutationMovesTheNativeDecision`, which asserts directly on
`decision.Action` and `RiskScore` from the native engine and never looks at
`Verdict` at all. Each of its three rows changes exactly one field of one shared
baseline struct and requires the native action to move MONITOR → ESCALATE and
the native risk to rise.

The existing test is flagged rather than deleted: it tests something real (that
the corpus's stated expectations still hold end to end), and the cross-check
line is genuine.

---

## 5. Mutation audit

Requirements checked one by one against `TestRWC001MutationChangesDecisionOnlyWhenExpected`,
`TestAuditCausalMutationMovesTheNativeDecision` and
`TestAuditMutationChangesEveryHashEvenWhenTheDecisionHolds`.

| Requirement | Finding |
|---|---|
| Exactly one causal input changed | **Met**, with one naming defect corrected — see below |
| All unrelated inputs remained identical | **Met.** Every mutant is `base := RWC001BaselineVessel` followed by a single field assignment. There is no re-typed lookalike struct anywhere |
| Decision changed only where logically expected | **Met.** LOA 140→151, draft 7.2→8.4, geared true→false each move MONITOR → ESCALATE. Bowthruster true→false and crane 30→31 leave the decision at MONITOR/0.0 |
| Certificate / hash changed when appropriate | **Met, and this was previously untested.** R23 adds the assertion |
| Irrelevant mutation does not alter the decision | **Met** |

### The naming defect

`port_max_LOA_150_to_149_flips_baseline_to_FAIL` does not mutate the port
against the baseline. The baseline's LOA is 140 m and fits under both 150 m and
149 m, so the mutation would be unobservable; the body correctly uses a boundary
vessel at exactly 150 m. Only the name claimed otherwise. Renamed to
`port_max_LOA_150_to_149_flips_a_boundary_vessel_to_FAIL`. The mutation itself
was always strictly single-field: before and after share the same vessel and
differ only in `Port.MaxLOA`.

### The hash finding, measured

Toggling `Bowthruster` — which no Akonikien constraint reads:

| Identifier | Baseline | Bowthruster flipped | Changed? |
|---|---|---|---|
| Decision action | `MONITOR` | `MONITOR` | no (correct) |
| Risk score | `0` | `0` | no (correct) |
| `input_hash` | `4f7a8da21f52…` | `0f34a5479545…` | **yes** |
| `canonical_hash` | `07cace1a4841…` | `ff16d3e1a223…` | **yes** |
| `certificate_hash` | `5b14c33ed0f4…` | `fcecab6ac6cd…` | **yes** |
| `execution_root_hash` | `2dd0f5455c8b…` | `e54c401d8da0…` | **yes** |

Both halves matter. A decision that moved would mean the constraint model reads
fields it should not. A certificate that did not move would mean the certificate
is not actually committing to the evidence it claims to cover.

---

## 6. Replay audit

Two distinct capabilities were conflated in R22's language, and one of them did
not work. Both statements are now precise.

### 6a. In-process canonical replay — works, and is genuinely isolated

`pkg/rwc.VerifyReplay` records the exact `execution.Result.Case` and
`CanonicalResult` the engine committed to (not the caller's original request,
which `RunUnified` mutates during entity resolution), wraps them in a real
`replay.ReplayPackage`, and replays through `replay.Engine`, which builds a
**brand-new** `canonical.Pipeline` sharing no pointer with the original.

Result: 10 of 10 matched, 12 canonical stages compared per case.

Cached output is not being reused: `pkg/replay.Engine.Replay` is a method on an
empty struct with no state to cache, and its fresh pipeline is one of the five
audited `canonical.NewPipeline(` construction sites, each justified in
`internal/nobypass` — the justification for this one being precisely "no access
to the originals".

### 6b. Cross-process cold DAG replay — did not work in R22; now does

The audit ran `cmd/veriqo-cold-replay`, a separately compiled binary, as its own
OS process against each exported case. **The first attempt failed on all ten
cases with exit code 2:**

> *the committed execution's IDENTITY_RESOLUTION stage was bound to a real
> identity ledger at record time; -identity-export is required to cold-replay it
> (proceeding without it would silently diverge, not silently match)*

The refusal is correct behaviour by the cold-replay binary. The defect was the
bundle's: R22 exported the DAG request but not the identity ledger, while
claiming the replay was "reproducible by anyone". **That claim was false when
made.**

Corrected in R23-1: the bundle now also writes
`replay_requests/<case>.identity.json`, carrying the real ledger plus the actual
aliases each case resolved and the exact entity ID this process obtained.

Re-run result: **10 of 10 PASSED**, 18 DAG nodes compared per case, replayed
evidence root equal to the original for every case, and identity queries
reproduced from a resolver rebuilt from the exported ledger alone (1 query per
RWC-001 case, 2 per RWC-002 case).

### The negative control

A pass proves nothing unless a failure is reachable. Changing one field of one
exported case (`"loa_m":151` → `"loa_m":140` in `RWC-001-B`) produced:

```
original evidence root : a50092908641ccef95ee0fa49f6a0e167cf77f4017e8f425e8b3346082731e87
replayed evidence root : 2a49af9a48d8bc612092c15e3bb9445a66f5f413424e353173e23a80bd5ec1d5
divergent stage        : TRUTH_ARBITRATION
VERDICT                : FAILED
```

### Byte-level identity, itemised

| Artifact | Verified identical on replay? | How |
|---|---|---|
| Canonical input | Yes | It IS the replay input; any change diverges (negative control above) |
| Evidence ordering | Yes | `canonical.SortedSourceIDs` sorts deterministically; the arbitration node hash covers the ordering |
| Decision | Yes | Reported per case by cold replay (`Decision : ESCALATE`) |
| Explanation | Yes | `explanation_hash` reproduced |
| Certificate | Yes | `verification_certificate_id` reproduced |
| Hashes | Yes | 18/18 node hashes plus the root, per case |

---

## 7. Certificate audit

**The certificate is generated by the native lifecycle mechanism. The RWC
command computes nothing.**

- Minted by `pkg/lifecycle.hashLifecycleCert` (`pkg/lifecycle/lifecycle.go:259`),
  called from `RunUnified` at `:664`.
- `cmd/veriqo-rwc-v2` serializes `res.Lifecycle.Certificate` and nothing else.
- Independent re-verification of all ten committed certificates by recomputing
  each hash from its own fields: `lifecycle.VerifyCertificate` valid 10/10,
  `canonical.VerifyCertificate` over the embedded canonical certificate valid
  10/10, `IVFVerified` true 10/10, `ReplayID == Canonical.Hash` 10/10.

This is enforced structurally, not by convention. `internal/entrypoints` guards
literal construction of `replay.VerificationCertificate{}` and assignment of
`ExecutionRootHash` outside audited files. R22's first port tripped both markers
and fixed them by removing the marker rather than widening an allowlist —
`pkg/rwc/replay.go` returns a named zero value, and `rwc.CaseResult` does not
carry a root-hash field.

One allowlist entry was added: `cmd/veriqo-rwc-v2/main.go` declares an
`ExecutionRootHash` field on the record it writes into the bundle, filled from
`res.Lifecycle.Certificate.ExecutionRootHash`. This auditor checked both
occurrences by reading them — one struct tag, one assignment from a real
lifecycle result — and confirms the command computes no root hash. The field
could have been named something shorter and slipped past the textual scanner
silently; naming it honestly and taking the reviewed allowlist entry is the
correct outcome.

---

## 8. Ledger audit — what "ledger" actually means here

The mandate asks for four categories to be separated. They are:

| Category | Present in RWC v2? | Evidence |
|---|---|---|
| **In-memory hash chain** | **Yes** | `pkg/moat/fusion.Engine.Head()`, recorded per case in `evidence/rwc_v2/ledger_anchors.json`. Re-derivable via `Fusion.VerifyChain()` while the process lives. Also: `pkg/identity`'s event ledger, `pkg/lineage`'s per-case chain, `pkg/moat/kg`'s mutation log, `trustcalc.Ledger` (empty here) |
| **Persisted WAL** | **No** | A real write-ahead log exists at `pkg/storage/wal` with CRC, fsync policy, segment rotation and six-way recovery classification. Its only importer anywhere in the tree is `cmd/veriqo-readiness`. Nothing on the RWC path touches it |
| **Durable local storage** | **No** | `veriqo/registry` supports `WithStatePath` persistence, but `cmd/veriqo-rwc-v2` calls `kernel.New()` with no options, so `HasPersistence()` is false and `Shutdown()` saves nothing. Every chain above dies with the process |
| **External durable anchor** | **No, and no such mechanism exists in this repository at all** | Grep for notarization, timestamp authority, RFC 3161 or external anchoring across `pkg/` and `internal/` returns only RWC's own disclaimers saying it does not do this |

**A hash chain is not an external ledger anchor, and this bundle does not call it
one.** `ledger_anchors.json` records, per case, `"kind": "IN_MEMORY_HASH_CHAIN"`,
`"durable": false`, `"externally_anchored": false`. The JSON key itself is
`ledger_anchor_in_memory_hash_chain_head`, so a reader cannot skim the file and
come away with the wrong impression.

---

## 9. Knowledge graph audit

**The Knowledge Graph participates. It does not decide. Both halves measured.**

It participates, and this is not a paper claim:

- `veriqo/kernel.New` → `canonical.NewPipeline` builds one `kg.NewGraph()` and
  passes it into `fusion.NewEngine(g)` (`pkg/canonical/canonical.go:230-235`).
- `fusion.Engine.Arbitrate` performs a single ordered write per arbitration when
  a graph is attached (`pkg/moat/fusion/fusion.go:361` → `writeToGraph` at
  `:421`): claim node, winner node, truth edge, per-source nodes and edges.
- Measured on a real RWC case: a fresh kernel's graph holds 0 mutations; after
  one case it holds **5**, with a non-empty `RootHash()` and a snapshot matching
  the log length.

It does not decide:

- Nothing in `canonical.RunCanonical` reads the graph back before deciding.
  Verified by reading the function end to end: the graph is reachable only as
  `Pipeline.KG` and as fusion's private `graph` field, and neither is read on the
  decision path.
- Therefore no KG content can influence a decision, and no multi-hop or
  relationship reasoning was exercised by this corpus.
- `pkg/moat/domain/maritime`'s ownership graph and multi-hop traversal — the
  parts of this repository that would constitute real graph *reasoning* — are
  not touched by RWC v2 at all.

Both facts are pinned by `TestAuditKnowledgeGraphIsWrittenButDoesNotDecide`.

**Verdict for this section: KG participation as an ordered arbitration sink is
PROVEN. KG participation in the decision path is NOT PROVEN, and nothing here
should be read as claiming graph reasoning was validated.**

---

## 10. Real-world data audit — provenance matrix

Classification vocabulary, as the mandate defines it. `SOURCE_SUPPLIED` means the
value was provided to this system as real-world reference data;
`CLAIM` means a named party asserts it; `STRUCTURALLY_VALIDATED` means a
deterministic offline check over the value itself succeeded; `CORROBORATED`
means an independent source confirmed it; `UNVERIFIED` means nothing checked it;
`CONTRADICTED` means a check disagreed.

### RWC-001 — port constraint figures

Every figure was transcribed by hand from supplied real port-authority text. None
was fetched, and none was checked against any authority.

| Field | Value | Classification |
|---|---|---|
| Bata max LOA / beam / draft | 300 m / 40 m / 14.5 m | SOURCE_SUPPLIED, UNVERIFIED |
| Akonikien max LOA | 150 m | SOURCE_SUPPLIED, UNVERIFIED |
| Akonikien max draft (absolute / operational) | 8.00 m / 7.50 m | SOURCE_SUPPLIED, UNVERIFIED |
| Akonikien geared requirement | required | SOURCE_SUPPLIED, UNVERIFIED |
| Tema berth drafts | 10 m (berth 3), 8.2 m (berths 13/14) | SOURCE_SUPPLIED, UNVERIFIED |
| Owendo max draft | 10.80 m | SOURCE_SUPPLIED, UNVERIFIED |
| Lome max draft | 10 m / 11.5 m — **two figures supplied** | SOURCE_SUPPLIED, UNVERIFIED, internally inconsistent |
| Douala channel draft + tide range | 6.20 m + tide; 0.3–2.9 m | SOURCE_SUPPLIED, UNVERIFIED |
| Loading / discharge rates | e.g. 5,000 MT PWWD SHINC | SOURCE_SUPPLIED, UNVERIFIED, never numerically evaluated |

Three honest notes a reader is owed:

- **The Lome entry carries two mutually inconsistent maxima** (10 m and 11.5 m),
  recorded as such in `ports.go`'s own Notes. RWC-001 never evaluates a vessel at
  Lome, so the inconsistency is inert — but it is corpus data that was not
  resolved, not data that was validated.
- **Tema's table records the conservative 8.2 m** rather than the 10 m berth. That
  is a judgement made when the corpus was entered, documented in an inline
  comment. It is a modelling choice, not a fact from the source.
- **`RWC001VoyageLegs` is corpus data that is never exercised.** `ports.go:88`
  declares the voyage legs `TEMA, LOME, DOUALA, OWENDO, LUANDA`, and grep over
  the whole tree finds no reader. One of those legs, `LUANDA`, has no entry in
  the `Ports` table at all — so had it ever been evaluated, `BuildRWC001Case`
  would have returned an `INSUFFICIENT_EVIDENCE` case rather than a suitability
  judgement. This is unused input, not a defect in the reasoning, but a reader
  scanning the corpus should not assume the voyage was assessed. It was not.

### RWC-001 — vessel candidates

| Field | Classification |
|---|---|
| `RWC-001-CANDIDATE-A…E` names, LOA, draft, geared, crane, grab, bowthruster | **CONSTRUCTED TEST INPUT** — not a claim about any real vessel |

This deserves emphasis because the corpus is called a *real-world* validation
corpus. RWC-001's vessels are adversarial probes built by mutating one baseline
struct. **Only the port side of RWC-001 is real-world-derived.** RWC-001
validates the system's reasoning over real constraint data; it does not validate
any real vessel/port pairing that occurred.

### RWC-002 — vessel identity

| Field | Value | Classification |
|---|---|---|
| Vessel name | `GUNUNG KEMALA / PERTAMINA 8003` | CLAIM, UNVERIFIED |
| IMO number | `8508292` | CLAIM, **STRUCTURALLY_VALIDATED** (check digit: 142 mod 10 = 2 = claimed) |
| MMSI | `525008029` | CLAIM, **STRUCTURALLY_VALIDATED** (MID 525 → Indonesia, consistent with the claimed Pertamina name) |
| Identity as a whole | — | **STRUCTURALLY_VALIDATED, not CORROBORATED** (§3) |

### RWC-002 — cargo, voyage, documents, transaction

| Field | Value | Classification |
|---|---|---|
| Product | `EN590 10ppm` | CLAIM, UNVERIFIED |
| Quantity | `50,000 MT` | CLAIM, UNVERIFIED |
| Price | `630-10` | CLAIM, UNVERIFIED — not parsed, not compared to any reference |
| Voyage / position | departed Poti; reported at Lome anchorage | CLAIM, UNVERIFIED |
| 7 claimed documents (BoL, Q88, NOR, …) | listed | CLAIM, UNVERIFIED — **existence is claimed; no document was inspected, hashed, or seen** |
| 12 transaction-sequence claims | listed | CLAIM, UNVERIFIED as to truth; **3 of 3 named red-flag phrases matched** by literal substring against the corpus's own supplied text, which is a reproducible computation over the claim, not a finding about the world |
| MagicPort / MarineTraffic | named as checked | **CLAIM, UNVERIFIED.** Neither was queried by this system. That these sources were consulted is itself only a broker assertion |

### Aggregate

| Classification | Count of distinct corpus fields |
|---|---|
| SOURCE_SUPPLIED (real-world reference data, unverified) | 9 port constraint groups |
| CONSTRUCTED TEST INPUT | 5 vessel candidates |
| CLAIM + STRUCTURALLY_VALIDATED | 2 (IMO, MMSI) |
| CLAIM + UNVERIFIED | 8 (name, product, quantity, price, voyage, documents, transaction, source references) |
| CORROBORATED | **0** |
| CONTRADICTED | **0** (`contradiction: false` for all ten cases) |

---

## 11. Blocker audit

Reassessed using **only** evidence RWC v2 generated. Vocabulary is this
document's own, per the mandate, and is **informational commentary scoped to this
audit** — it is not the repository's canonical gate vocabulary.

`READINESS_MANIFEST.json` is untouched by rounds R22 and R23. All eight gates
remain `BLOCKED` there, and nothing below changes that.

| Blocker | Manifest status (unchanged) | This audit's classification | Evidence, and why it goes no further |
|---|---|---|---|
| `hsm_kms` | BLOCKED | **OPEN** | RWC v2 produces no signature and touches no key material. Its certificates are SHA-256 content commitments, not signatures. Zero evidence either way |
| `live_data` | BLOCKED | **BLOCKED_EXTERNAL** | RWC v2 is the clearest possible evidence that live data is *absent*: every corpus figure was hand-entered, no network call exists in `pkg/rwc`, and the bundle's own envelope declares `FIXTURE` / `REAL_DERIVED_BENCHMARK` with six explicit limitations. The envelope names `gate_id=live_data` and is **refused** for it — its provider is not a registered trust anchor, and `TestCorpusEnvelopeCannotQualifyABlockedGate` holds that refusal in place |
| `multi_region_dr` | BLOCKED | **OPEN** | Single process, single machine, no region concept exercised. Zero evidence |
| `pentest` | BLOCKED | **BLOCKED_EXTERNAL** | Requires an independent third party. RWC v2 is self-produced by definition and cannot contribute |
| `scale_qualification` | BLOCKED | **OPEN** | Ten cases, one submission each (two for one case). The requirement is 100 nodes and 1M evidence records. Ten is not a scale measurement, and reporting it as partial progress would be misleading |
| `soak_72h` | BLOCKED | **OPEN** | Total runtime under one second. Zero bearing on 72-hour continuous operation |
| `spire_mtls` | BLOCKED | **OPEN** | No transport, no workload attestation, no certificate rotation exercised |
| `supply_chain_scan` | BLOCKED | **OPEN** | RWC v2 keeps `go list -m all` at exactly one module (`veriqo`), so it introduces no new supply-chain surface — but *introducing no risk* is not *scanning for risk*. The gate wants govulncheck/gosec/staticcheck in CI and RWC v2 provides none of it |

**Nothing is CLOSED. Nothing is VALIDATED. Nothing is INTERFACE_QUALIFIED.** No
blocker is closed merely because a test exists, which is exactly what the mandate
warned against. The one place RWC v2 contributes real evidence is `live_data`,
and it contributes evidence in the *negative* direction: a machine-checked
demonstration that this corpus cannot qualify that gate.

---

## 12. Final auditor verdict

GREEN = proven. YELLOW = partially proven. RED = not proven.

| # | Question | Verdict | Basis |
|---|---|---|---|
| 1 | Does RWC v2 prove native VERIQO execution? | **GREEN** | All ten cases run `kernel.New` → `RunUnified` → `execution.Engine` → `RunCanonical` → IVF → certificate → replay. Fifteen of eighteen DAG stages OK, three honestly SKIPPED for lack of input. Nothing mocked, nothing bypassed. Two whole-tree gates make a second path structurally impossible. **Qualified:** the eighteen stages are attestation nodes over *one* `RunCanonical` call, not eighteen independent engine invocations |
| 2 | Does it prove real-world data ingestion? | **RED** | No ingestion occurred. Every figure was hand-entered as a Go literal. `pkg/connector/aisstream` — this repository's real AIS adapter — was not used and cannot be used without network egress and a rights upgrade. RWC v2 proves real-world data *processing*, which is a different and lesser claim |
| 3 | Does it prove provenance? | **YELLOW** | The provenance *machinery* genuinely ran: dependency evaluation gates fusion weights, `provenance.Graph.Assess` computed independence per case, and the assessment is committed in the DAG. But it returned `UNKNOWN` for every case — no source declared any ancestry, so there was nothing to assess. Provenance was exercised; provenance was not demonstrated |
| 4 | Does it prove truth arbitration? | **YELLOW** | Fusion arbitration and contradiction ingestion ran on every case and their outputs are committed. But nine of ten cases have a single source, so arbitration had nothing to arbitrate between, and no case produced a contradiction. Separately, the **trust** layer did not participate at all: `TRUST_STATE` is SKIPPED for every case and the shared trust calculus recorded **zero** observations |
| 5 | Does it prove decision reasoning? | **YELLOW** | The native decision is genuinely computed and is causally sensitive to the evidence: three single-field mutations each move MONITOR → ESCALATE, two irrelevant ones do not. But the RWC `Verdict` — the PASS/FAIL/CONDITIONAL a reader will actually look at — is the adapter's own arithmetic, cross-checked against the engine rather than derived from it (§2). Not GREEN while those are two computations |
| 6 | Does it prove deterministic replay? | **GREEN** | In-process canonical replay 10/10 through a fresh pipeline; cross-process cold DAG replay 10/10 in a separately compiled binary, 18 nodes per case, identity queries reproduced from a cold-restored ledger; a one-field tamper diverges at `TRUTH_ARBITRATION` and is reported FAILED. **This was RED until R23-1**, when the missing identity export was found and added |
| 7 | Does it prove certificate generation? | **GREEN** | Minted by `pkg/lifecycle.hashLifecycleCert`, not by the RWC command. All ten independently re-verified at both lifecycle and canonical layers, with `IVFVerified` true and `ReplayID == Canonical.Hash` throughout. The command's inability to mint one is enforced by `internal/entrypoints`, not by convention |
| 8 | Does it prove ledger anchoring? | **RED** | There is an in-memory hash chain and nothing else. No WAL is written, no state is persisted, no external anchor mechanism exists anywhere in this repository. The bundle says so in its own field names |
| 9 | Does it prove Knowledge Graph participation? | **YELLOW** | Participation as an ordered arbitration sink is PROVEN and measured: 5 mutations per case with a verified root hash. Participation in the decision path is **NOT PROVEN** — nothing reads the graph back before deciding, and no graph reasoning was exercised |
| 10 | Does it prove production readiness? | **RED** | Emphatically not, and RWC v2 was never capable of proving it. All eight production blockers remain BLOCKED and this audit closes none of them. Ten cases in under a second in a sandbox with no network, no persistence, no key material and no external infrastructure is a validation-corpus exercise, not a readiness argument |

### Tally

**GREEN 3** (native execution, deterministic replay, certificate generation) ·
**YELLOW 4** (provenance, truth arbitration, decision reasoning, knowledge graph)
· **RED 3** (real-world data ingestion, ledger anchoring, production readiness).

### The single sentence an external auditor should take away

RWC v2 proves that this repository's real engines executed a real-world-derived
corpus deterministically, reproducibly across a process boundary, and under
certificates that verify — and it proves, just as clearly, that no live data was
ingested, no claim was independently corroborated, nothing was durably anchored,
and no production blocker moved.

---

## Appendix: what a re-audit should re-run

Every finding above is reproducible:

```
go test ./pkg/rwc/ -run TestAudit -v          # the audit's own pinned findings
go run ./cmd/veriqo-rwc-v2                     # regenerate the bundle from a real run
go build -o /tmp/cr ./cmd/veriqo-cold-replay   # then, per case:
/tmp/cr -export evidence/rwc_v2/replay_requests/<case>.json \
        -identity-export evidence/rwc_v2/replay_requests/<case>.identity.json
```

`pkg/rwc/audit_test.go` exists so that a change which quietly falsifies a claim
in this report fails the build. Each of its failure messages says to re-derive
this document rather than relax the test.
