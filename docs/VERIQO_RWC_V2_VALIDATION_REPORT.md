# VERIQO RWC v2 — Validation Report

Round R22, amended in R23. This report records **what the run did and what came
out of it**, on this branch, from the bundle in `evidence/rwc_v2/`. The R23
amendments are the two places the audit found an R22 statement to be false —
the RWC-002 vessel-identity status, and the bundle's cold-replayability — and
both are marked where they appear rather than silently rewritten. It is a record of execution,
not an assessment of proof. The assessment — including the places where the
vocabulary used below turns out to overstate what happened — is in
`docs/RWC_V2_INDEPENDENT_AUDIT_REPORT.md`, which supersedes this document
wherever the two differ.

Reproduce with:

```
go run ./cmd/veriqo-rwc-v2
```

## Corpus

`VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2`, ten cases, base tick 1, one fresh
`veriqo/kernel.Kernel` per case.

- **RWC-001** — vessel/port physical suitability. Five adversarial candidates,
  each a single-field mutation of one shared baseline vessel struct, evaluated
  against real port-authority constraint figures (Bata, Akonikien, Tema, Owendo,
  Lome, Douala).
- **RWC-002** — cargo/vessel identity and provenance/claim separation. Five
  claim categories over one vessel identity (IMO 8508292 / MMSI 525008029).

## Results

| Case | Verdict / Provenance | Native action | Risk | IVF | Replay | Stages |
|---|---|---|---|---|---|---|
| RWC-001-A | PASS | MONITOR | 0.0000 | ok | match | 12 |
| RWC-001-B | FAIL | ESCALATE | 1.0000 | ok | match | 12 |
| RWC-001-C | FAIL | ESCALATE | 1.0000 | ok | match | 12 |
| RWC-001-D | FAIL | ESCALATE | 1.0000 | ok | match | 12 |
| RWC-001-E | CONDITIONAL | FLAG | 0.5000 | ok | match | 12 |
| RWC-002-CARGO_IDENTITY | UNVERIFIED | MONITOR | 0.0000 | ok | match | 12 |
| RWC-002-DOCUMENT_EXISTENCE | UNVERIFIED | MONITOR | 0.0000 | ok | match | 12 |
| RWC-002-TRANSACTION_SEQUENCE | UNVERIFIED | ESCALATE | 1.0000 | ok | match | 12 |
| RWC-002-VESSEL_IDENTITY | STRUCTURALLY_VALIDATED | MONITOR | 0.0000 | ok | match | 12 |
| RWC-002-VOYAGE_POSITION | UNVERIFIED | MONITOR | 0.0000 | ok | match | 12 |

**Note on RWC-002-VESSEL_IDENTITY.** Round R22 reported this case as
CORROBORATED. The round-R23 audit examined what actually produced that label and
found it an overclaim: the "second source" is deterministic offline arithmetic on
the claimed identifier itself, and the native provenance status for the case is
`UNKNOWN`, not `DECLARED_INDEPENDENT`. The classifier and the test that encoded
the overclaim were both corrected, and the table above shows the corrected
status. See `docs/RWC_V2_INDEPENDENT_AUDIT_REPORT.md` §3.

**No case in this corpus reaches CORROBORATED**, and none can while this
environment has no path to an independent external source.

### Why each RWC-001 verdict came out as it did

Every verdict is a function of `EvaluateVesselAtPort`, which compares a
candidate's declared specification against a port's constraint table with no
vessel-name or case-ID branching anywhere in it.

- **A** (LOA 140 m, draft 7.2 m, geared) at Akonikien: every evaluated dimension
  clean.
- **B** (LOA 151 m) at Akonikien: exceeds the port's stated max LOA of 150 m.
- **C** (draft 8.4 m) at Akonikien: exceeds the port's recommended operational
  maximum of 7.50 m. The operational figure binds, not the 8.00 m absolute.
- **D** (not geared) at Akonikien: the port requires a geared vessel.
- **E** (baseline vessel) at Douala: draft is **UNRESOLVED**, not passed and not
  failed. Douala's corpus entry states the channel draft varies with tide
  (`6.20 m + tide`, range `0.3–2.9 m`). The corpus itself says real-time tide
  confirmation is required, and there is no tide feed here, so the dimension is
  marked unresolved regardless of the static numeric comparison.

### Mutation behaviour

`TestRWC001MutationChangesDecisionOnlyWhenExpected` mutates one field at a time
from the same baseline: port max LOA 150→149 (with a boundary vessel at exactly
150 m) flips PASS→FAIL; vessel LOA 140→151 flips PASS→FAIL; draft 7.2→8.4 flips
PASS→FAIL; geared true→false flips PASS→FAIL; and bowthruster true→false, which
is not an Akonikien constraint, does not change the verdict.

### RWC-002 red-flag detection

`BuildRWC002TransactionCase` counts how many named trade-finance red-flag
phrases literally appear in the corpus's own supplied transaction-claim text.
All three matched — "consignee listed on documentation is seller",
"intermediary settlement", "balance payment after inspection" — giving a ratio
of 1.0, which is what carries that case to a native risk score of 1.0000 and an
ESCALATE action. The count is reproducible from the corpus text; it is not an
asserted risk level.

## Case lineage

Every case registers a real `pkg/lineage` case under one CaseID, and every
chain verifies. Node counts are 8 (RWC-001, single NAME alias), 9 (RWC-002
single-source claims) and 10 (RWC-002 vessel identity, two submissions).

Every case reports `complete=false` with `missing_kinds=[OUTCOME]`. This is the
correct answer, not a gap: `pkg/lineage` requires an OUTCOME node before a case
is complete, and ground truth for a vessel/port suitability judgment or a broker
claim does not exist at case-run time. A complete case here would mean ground
truth had been fabricated.

## Identity resolution

RWC-002's IMO and MMSI aliases resolve through `pkg/identity`, the canonical
entity authority, and the resulting merge leaves a verifiable event in the
identity ledger. No case used the legacy union-find fallback, so
`legacy_identity_fallback_used` and `human_review_required` are false throughout.

RWC-001 declares a single NAME alias, performs no merge, and therefore leaves
the identity ledger empty — its correlation key's
`entity_identity_ledger_head` is legitimately `""`. This is asserted in both
directions by `pkg/rwc`'s tests so neither reading can drift.

## What the bundle's own vocabulary means

- `ledger_anchors.json` records an **in-memory hash-chain head** from
  `pkg/moat/fusion.Engine.Head()` on that case's own fresh in-process engine. It
  is re-derivable via `Fusion.VerifyChain()` while the process lives and is gone
  when it exits. It is not persisted, not a write-ahead log, and not an external
  anchor. Every field in that file says so explicitly.
- `replay_results.json` records an **in-process** replay through a fresh
  `pkg/canonical.Pipeline` with no shared pointer to the original run, comparing
  12 canonical stages. It is not the cross-process cold DAG replay, which
  compares 18 DAG nodes and is a separate capability with its own binary.
  `replay_requests/` carries both halves that binary needs — `<case>.json` (the
  DAG export) and `<case>.identity.json` (the identity ledger and the aliases
  each case resolved). This command does not run the cold replay, and
  `manifest.json` says so. Round R23 verified it by hand: 10 of 10 PASSED, and a
  one-field tamper of an export diverges at `TRUTH_ARBITRATION`.

  The identity export was missing when R22 first shipped this bundle, which made
  every cold replay exit with a usage error rather than a verdict. See the audit
  report §6.
- `evidence_envelope.json` carries a `pkg/governance/envelope` envelope declared
  FIXTURE with origin `REAL_DERIVED_BENCHMARK`, or — when the release identity is
  incomplete, as in the committed bundle — a named refusal listing exactly which
  fields were missing. The command computes release, source hash and SBOM hash
  for real and refuses to invent commit and binary hash.

## Explicit non-claims

Restating what `pkg/rwc.Limitations()` declares, because it is the load-bearing
part of this report:

1. No live data feed of any kind was consulted — no AIS, no port-authority
   system, no vessel registry, no tide service. Every figure was entered by hand
   from supplied real-world text.
2. The RWC-002 identity check is offline arithmetic on the claimed identifiers.
   It can prove an identifier is internally malformed. It cannot confirm the
   vessel exists or that the identifier belongs to it.
3. The MagicPort and MarineTraffic references are themselves broker assertions
   that those sources were checked. Neither was queried by this system.
4. The recorded ledger anchor is in-memory only.
5. The recorded replay is in-process only.
6. The environment has no external infrastructure. Nothing here bears on
   `hsm_kms`, `multi_region_dr`, `pentest`, `scale_qualification`, `soak_72h`,
   `spire_mtls` or `supply_chain_scan`, and it cannot qualify `live_data`.

`READINESS_MANIFEST.json` and the R-numbered rows of
`docs/governance/REQUIREMENT_TRACEABILITY_MATRIX.md` are untouched by this work.
RWC v2 is a validation-corpus exercise, not a mandatory gate.
