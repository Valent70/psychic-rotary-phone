# Case Room & Independent Dossier Verifier — Specification

Covers Round 4 deliverables **J** (Case Room Specification) and **K**
(Independent Dossier Verifier), and registers the two as the `CASE_ROOM`
and `DOSSIER` categories of the canonical readiness taxonomy (see
`CANONICAL_READINESS_TAXONOMY.md`).

---

## Part 1 — Case Room

### What is built, this round

`pkg/insurance/caseroom` (`caseroom.go`, `assurance.go`) is the
**access-control core** of the Case Room: given a
`party.RelationshipRegistry`, one specific `RelationshipID`, and the
current tick, `BuildView` decides exactly what that party may see of a
case's dossier, and fails closed by default.

```go
view, err := caseroom.BuildView(relationships, "REL-BROKER-1", tick, dossier)
```

`BuildView` returns `caseroom.ErrNoAccess` — never a partially-populated
`View` — unless the relationship:

1. exists in the registry,
2. is `EffectiveAt(tick)` (consented, not revoked, within its window —
   see `party.Relationship.EffectiveAt`), **and**
3. carries `party.PermissionAccessCaseRoom`.

Once inside, each further `Section` (`TIMELINE_CONFLICTS`,
`EVIDENCE_SUFFICIENCY`, `COVERAGE_ANALYSIS`, `QUANTUM_CALCULATION`,
`RECOVERY_TARGETS`, `DEADLINES`, `HUMAN_REVIEW_QUESTIONS`) is
independently gated: four (`TIMELINE_CONFLICTS`, `COVERAGE_ANALYSIS`,
`QUANTUM_CALCULATION`, `DEADLINES`) are visible to anyone holding
`ACCESS_CASE_ROOM`; three (`EVIDENCE_SUFFICIENCY` →
`VIEW_EVIDENCE`, `RECOVERY_TARGETS` → `RECEIVE_RECOVERY`,
`HUMAN_REVIEW_QUESTIONS` → `ACT_IN_DISPUTE`) require their own named
`party.Permission`. A section the viewer cannot see reports a **zero**
content count, not the underlying real count — redaction is by
omission, never by returning a smaller-but-nonzero number that could be
mistaken for the truth.

The authorization mechanism is **entirely** `party.RelationshipRegistry`
— the same registry the Real-World Insurance Network report describes
(`REAL_WORLD_INSURANCE_NETWORK_REPORT.md`). There is no second,
case-room-local permission engine: `PermissionAccessCaseRoom` is one of
the ten permissions `party.go`'s `relationship.go` already declares.

`caseroom.RunAssurance()` exercises the three fail-closed paths (unknown
relationship, missing permission, still-`PENDING` consent) plus the real
per-section gating on the golden cross-domain case, and is registered as
the mandatory `case_room_access_control` gate in `cmd/veriqo-readiness`.

### What is explicitly NOT built, and why

This is the access-control **core**, not a rendered UI and not a
deployed HTTP surface. Standing up an actual customer-facing web
application — a real domain, TLS termination, a session/authentication
layer, a rendered frontend — is outside what a sandboxed coding session
can honestly claim to have done: it would require real infrastructure,
a real deployment target, and a real customer to authenticate, none of
which this environment can manufacture without fabricating the claim
this program's own governing rules forbid.

What IS honestly deliverable, and is delivered: the real decision logic
such a UI would sit on top of, genuinely tested (including against the
real golden cross-domain case, not only synthetic fixtures), with a
documented interface (`caseroom.View`, `caseroom.BuildView`) a future
HTTP handler or frontend team can wire directly to a request's
authenticated relationship ID. `caseroom_access_control`'s canonical
status is `VERIFIED_INTERNAL` for exactly this reason — this core is
genuinely closed — but the wider "deployed customer-facing surface"
claim stays out of scope for this round rather than being asserted.

### Recommended next steps toward a deployed Case Room

1. An HTTP handler that resolves an authenticated session to a
   `party.PartyID` / `RelationshipID`, calls `caseroom.BuildView`, and
   serializes the resulting `View` — no new authorization logic, a thin
   transport wrapper.
2. A frontend that renders exactly `View.Visible`'s sections and nothing
   else, so the UI cannot accidentally leak a section the backend
   redacted.
3. Session/authentication infrastructure (outside this codebase's scope
   — a real identity provider, real TLS, a real deployment target).

---

## Part 2 — Independent Dossier Verifier

### What is built, this round

`pkg/insurance/dossierverify` (library) and `cmd/veriqo-dossier-verify`
(CLI) implement the tagline **"Don't trust VERIQO. Verify VERIQO."**:
a checker that reads none of a single `Drive()` run's cached fields, and
instead re-derives every claim a dossier makes from the same public,
deterministic, pure-function APIs a real external auditor holding only
the raw case data would have to use.

```
$ veriqo-dossier-verify -case CASE-INS-002
===== VERIQO INDEPENDENT DOSSIER VERIFIER =====
Don't trust VERIQO. Verify VERIQO.
case: CASE-INS-002

[PASS] independent_reproduction     two independent runs agree exactly (evidence_root_hash=...)
[PASS] evidence_chain_integrity     root hash recomputed from 10 raw evidence records, matches the manifest
[PASS] quantum_recomputation        recomputed 150000.00 from ComputeInput via the pure quantum.Compute function, matches
[PASS] cold_replay                  snapshot INSREPLAY-... replayed cold reproduces the live result exactly
[PASS] no_verdict_field             scanned 17 fields on the compiled Dossier type, none is a coverage/liability/settlement verdict

verdict: INDEPENDENTLY VERIFIED — every claim above was recomputed, not read.
```

Five checks, each a real recomputation rather than a re-assertion:

| Check | What it recomputes |
|---|---|
| `independent_reproduction` | Drives the case TWICE, fully independently, from the same declared scenario input, and diffs the evidence root hash, quantum figure and full dossier byte-for-byte. |
| `evidence_chain_integrity` | Re-hashes every raw evidence record via `verification.Verify` — never reads `Manifest.EvidenceRootHash` and trusts it. |
| `quantum_recomputation` | Recomputes the indicative claim value from its own recorded `ComputeInput` via the pure `quantum.Compute` function. |
| `golden_salvage_recomputation` (golden case only) | Independently redoes both the with- and without-salvage recomputations `golden.go` performs, and checks the drop equals the salvage net value exactly. |
| `cold_replay` | Runs the case's own export → discard → reconstruct → replay cycle and checks it reproduces the live result. |
| `no_verdict_field` | A reflection-based scan of the *compiled* `dossier.Dossier` type, confirming no field name matches a coverage/liability/settlement-verdict pattern — checked against the actual type, not a comment. |

`cmd/veriqo-readiness` registers the exact same library call
(`dossierverify.RunAll()`, covering all seven synthetic cases plus the
golden case) as its own mandatory `dossier_verification` gate — the
CLI a human runs and the release gate a machine runs are structurally
the same code, not two implementations that could quietly diverge.

### Honesty note on "independent"

A literal separate organization auditing VERIQO from entirely outside
this codebase needs a real independent party — exactly the class of
external dependency this program's own rules forbid fabricating. What
this package honestly delivers is a checker that trusts **none** of
VERIQO's own cached output and recomputes every claim from first
principles using only the same public APIs an outside party would have.
It is a genuine, structural improvement over "trust the Result object"
— it is not a substitute for a real third-party review, and no artifact
in this program claims otherwise.

### Recommended next steps toward a third-party dossier review

1. Package `dossierverify.Verify`'s output alongside a case's exported
   snapshot (`casepack.Case.Snapshot()`) as a self-contained bundle an
   external reviewer can run without any other part of this repository.
2. Engage a real independent reviewer (functional spec §77's
   independent-review role, `party.RoleIndependentReviewer`) to run the
   bundle against their own tooling and compare.
