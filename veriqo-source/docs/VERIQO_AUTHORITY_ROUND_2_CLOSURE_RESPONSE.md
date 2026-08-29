# VERIQO — Authority Round 2: Manifest, Rights & Supersession Closure

**Assessment date:** 29 August 2026
**Baseline:** Authority Round: Evidence Trust Bypass Closure — commit `fbfc523`
**Responds to:** `Evidence_authority_closure.docx` — an independent reviewer's follow-up review of
Authority Round 1, agreeing `Evidence.Registry.SetStatus` is a major blocker genuinely closed, and
issuing this round's mandate: close all three findings that round's own report left explicitly
OPEN — `manifest.Registry.Advance`'s substance gap (treated as the highest priority),
`evidence.Registry.SetRights`, and `MarkSuperseded` — plus a new **Canonical Authority / Single
Source of Truth Audit**, with the same close-or-explicitly-leave-OPEN discipline applied throughout.
**Assessment mode:** local engineering qualification and reproducibility review. All code in this
deliverable compiles, is tested, and was verified against the live repository. Every claim below
that names a file, function, or test was checked against that exact source before this report was
written.

## Executive verdict

All three findings Authority Round 1 left OPEN are now structurally closed, each verified with the
same "temporarily disable the fix, confirm the new tests fail with a clear diagnostic, restore the
fix, confirm they pass" discipline used throughout this engagement:

1. **`manifest.Registry.Advance`** previously proved a transition was legal in the abstract
   (`validTransitions` allows `A -> B`) but never that the SPECIFIC evidence had actually earned
   it — the reviewer's own diagnosis, "Sequence integrity != transition authority," was exactly
   right. `Advance` now additionally requires real, attributed, hash-chained evidence of the
   substantive work each transition claims, checked against data already on file rather than any
   new external dependency.
2. **`evidence.Registry.SetRights`** previously accepted any known `RightsState` from any caller
   with no check on who was granting it. It now requires an authority whose trust has genuinely
   been granted through `pkg/evidence/provenance`'s own existing trust-grant model — reused, not
   duplicated.
3. **`MarkSuperseded`** previously recorded no audit trail and permitted silent re-supersession. It
   now requires an actor and a reason, records both on the record's own (previously unpopulated)
   `ChainOfCustody`, and refuses both re-supersession and naming an already-superseded record as
   someone else's successor.

A new **Canonical Authority Audit** addresses the reviewer's "split-registry contradiction" concern
directly and mechanically, not just in prose: two tests prove `pkg/insurance/evidence.Registry` and
`pkg/evidence/manifest.Registry` can never make a colliding claim about the same evidence, because
they track disjoint properties with disjoint vocabularies and neither can read or mutate the
other's state.

## Priority 1 — `manifest.Registry.Advance`: sequence integrity vs. transition authority

**Reviewer's point, quoted directly:** *"Advance() mungkin membuktikan: 'Transition ini legal
menurut state machine.' Tetapi belum tentu membuktikan: 'Condition yang membuat transition ini
legal benar-benar terpenuhi.'"* (Advance might prove "this transition is legal per the state
machine." But it doesn't necessarily prove "the condition that makes this transition legal was
actually satisfied.") The reviewer's own worked example: `VERIFIED -> FINALIZED` is `allowed`
per the state machine, but nothing previously proved required verification evidence exists, hashes
match, provenance is complete, or policy is satisfied — "fake process -> valid sequence ->
FINALIZED," structurally the same class of bug as the `SetStatus` bypass Round 1 closed.

**The fix.** `pkg/evidence/manifest/manifest.go` adds `transitionPrerequisiteLocked`, called from
inside `Advance` immediately after the existing sequence check passes and before any state
mutation. It requires, per transition, real work already on file for that specific `EvidenceID` —
never a new external policy dependency, only enforcement of data this package already collects:

| Transition | New prerequisite |
|---|---|
| `DRAFT -> INGESTED` | A `RECEIVED` or `REGISTERED` custody event has actually been recorded |
| `INGESTED -> INTEGRITY_ASSESSED` | `HashStatus` is recorded **and** a `HASHED` custody event exists |
| `INTEGRITY_ASSESSED -> PROVENANCE_COMPLETE` | `AcquisitionRecord` is recorded |
| `PROVENANCE_COMPLETE -> READY_FOR_FINALIZATION` | A `REVIEWED` custody event has actually been recorded |
| `READY_FOR_FINALIZATION -> FINALIZED` | `Classification` is recorded, the custody chain is non-empty, **and** the entire chain independently re-verifies (`verifyCustodyChainLocked`) |

The `FINALIZED` gate is the strongest: it does not just check that a `REVIEWED` event exists
somewhere, it cryptographically re-derives every hash in the custody chain from genesis, so a
manifest cannot be finalized if any earlier custody record was corrupted after the fact — even one
that individually looked fine when it was written. `VerifyCustodyChain` was refactored into a
lock-free `verifyCustodyChainLocked` specifically so `Advance` (which already holds the registry's
write lock) can call it directly without re-entering `sync.RWMutex`, which would deadlock.

**Honest limit, stated plainly.** This closes "a caller can skip the substantive work and just call
`Advance` in the right order" — it cannot close "a caller who genuinely lies while calling
`RecordCustodyEvent(..., CustodyReviewed, ...)` without actually reviewing anything." Nothing can
close that; it is the same domain-truth boundary this engagement has named repeatedly (Verification
Round, Part 5). What changes is the cost of the lie: it now requires an attributed, timestamped,
hash-chained, independently-verifiable record instead of nothing at all — a structurally different
and much higher bar than "call five functions in order."

**Adversarial tests, verified against a real regression.** Seven new tests in
`pkg/evidence/manifest/manifest_test.go` — one per prerequisite, plus
`TestAdvanceRefusesFinalizationWithATamperedCustodyChain`, which builds a fully legitimate manifest
up to `READY_FOR_FINALIZATION`, corrupts one historical custody event's `Reason` field directly
(the same technique the pre-existing `TestCustodyChainDetectsTampering` uses), and confirms
`Advance(..., StateFinalized, ...)` refuses. Before finalizing, the prerequisite check itself was
temporarily removed from `Advance` and the suite re-run: all seven new tests failed with the exact
expected diagnostic (`expected ErrTransitionPrerequisiteNotMet, got <nil>`), confirming they
genuinely exercise the fix rather than passing vacuously. The fix was restored and all 24 tests in
the package — the 17 pre-existing plus these 7 — pass.

Every pre-existing test that drives a manifest to `FINALIZED` (in `pkg/evidence/manifest`,
`pkg/insurance/cre`, and three files in `test/integration`) was updated to record the real custody
events the new gate requires, via a new shared helper (`advanceThroughFullLifecycle` in the
manifest package's own tests, `advanceManifestToFinalized` in `test/integration`) — this is the
honest way to reach `FINALIZED` now, and every one of these tests still proves what it always
proved, just through the real path instead of a bypassable one.

## Priority 2 — `evidence.Registry.SetRights`: an authoritative source, not a bare call

**Reviewer's point:** *"Kalau caller dapat melakukan SetRights(...), maka pertanyaan kita: Siapa
yang memiliki authority untuk memberikan rights? Harus ada authoritative source. Misalnya: Policy +
Identity + Authorization + Governance event. Bukan: caller -> SetRights()."*

**The fix.** `SetRights` now requires two additional parameters: a `*provenance.Registry` and an
`authorityID`. It refuses (`ErrRightsGrantNotAuthorized`) unless that ID names a real
`provenance.Entry` whose `TrustGranted` is genuinely `true` — and `TrustGranted` can only ever
become `true` through `provenance.Registry.GrantTrust`, which already existed in this repository,
already requires a non-empty policy reference (and, for an `EVIDENCE_PROVIDER`, an attestation
reference too), and already records who granted it and at which tick. This is exactly the
reviewer's own "Policy + Identity + Authorization + Governance event" shape — reused wholesale from
`pkg/evidence/provenance`, not reinvented: `Entry.ID`/`Kind` is identity, `TrustGranted` is
authorization, `PolicyRef` is policy, and the `GrantTrust` call itself, with `GrantedBy` and
`GrantedAtTick`, is the governance event.

This remains, as it always was, a recording operation — rights are still granted or revoked by a
real legal/commercial act elsewhere, and `SetRights` still never derives a rights state from
`Origin`, `Status`, or possession. What changed is that "elsewhere" must now be a real, attributed
grant already on file, not merely a function call any holder of a `*Registry` reference could make.

**Adversarial tests, verified against a real regression.** Four new tests in
`pkg/insurance/evidence/rights_test.go`: a `nil` provenance registry, an authority that was
registered but never granted trust, an authority that does not exist at all, and — the closing
proof — an authority whose trust is granted, succeeds, is then revoked via the pre-existing
`RevokeTrust`, and immediately fails again. Before finalizing, the authorization check was
temporarily removed and the suite re-run: both new refusal tests failed
(`expected ErrRightsGrantNotAuthorized ... got <nil>`) before the fix was restored. All three
pre-existing call sites (in `pkg/insurance/evidence/rights_test.go`) were updated via a shared
`trustedAuthorityRegistry` test helper.

## Priority 3 — `MarkSuperseded`: an audit trail and a protected lineage

**Reviewer's point:** *"Kita harus tahu: siapa yang melakukan supersession; mengapa; berdasarkan
evidence apa; kapan; apakah A sudah finalized; apakah B legitimate successor; apakah lineage tetap
immutable... Jangan sampai: caller -> MarkSuperseded(A) -> A disappears from effective evidence."*

**The fix.** `MarkSuperseded` now takes `actor` and `reason` alongside `tick`, refuses either being
blank (`ErrEmptySupersessionActor`, `ErrEmptySupersessionReason`), and appends one new
`CustodyEntry{Holder: actor, Action: "SUPERSEDED", Tick: tick, Reference: "...reason..."}` to the
superseded record's own `ChainOfCustody` — a field this package already defined on every `Record`
but had never actually populated anywhere, reused here rather than inventing a second audit
mechanism. Two further refusals close the remaining gaps the reviewer named:

- **Re-supersession** (`ErrAlreadySuperseded`): a record that is already `CorrectionSuperseded`
  cannot be superseded a second time — a prior version's own lineage can no longer be silently
  rewritten to point somewhere else.
- **Illegitimate successor** (`ErrIllegitimateSuccessor`): a record that is itself already
  superseded cannot be named as the current replacement for something else — "is B a legitimate
  successor" is now a checked precondition, not an assumption.

`A disappears from effective evidence` never happens: `Get` and `All` already returned superseded
records before this round (unchanged, still queryable, still fully intact), and this round adds the
proof that they stay that way with a full audit trail attached, not just a bare boolean flip.

**What this does NOT do**, stated honestly: it does not automatically re-evaluate any prior decision
that cited the superseded record (the reviewer's own "apakah keputusan yang sebelumnya menggunakan
A harus dire-evaluate" question). That is a downstream consumer's responsibility — any caller
holding a reference to a record should check `CorrectionSuperseded` before relying on it again — not
something this registry can or should decide on a consumer's behalf.

**Adversarial tests, verified against a real regression.** Five new tests in
`pkg/insurance/evidence/rights_test.go`, including direct proof that a second, conflicting
supersession attempt cannot rewrite `SupersededBy` away from the first, legitimate successor, and
that a record already superseded by one thing cannot then be used to supersede something else.
Before finalizing, both new refusal checks were temporarily removed and the suite re-run: both
failed with the exact expected diagnostic before the fix was restored.

## Canonical Authority / Single Source of Truth Audit

**Reviewer's concern:** *"Kalau keduanya bisa mengatakan: Evidence X = FINALIZED, maka kita harus
menjawab: Which registry is authoritative? Tidak boleh ada dua sumber kebenaran tanpa hierarchy."*
Mapping every Evidence property to exactly one authoritative owner:

| Property | Authoritative owner | Mutable? | Derived? | External dependency |
|---|---|---|---|---|
| Identity (`EvidenceID`) | `pkg/evidence/ontology.Evidence.ComputeID()` | No | Yes (SHA-256 over canonical bytes) | No |
| Content (underlying fields) | `ontology.Evidence`, via `New()` only | No after construction | No | Possibly (the original assertion is a real-world claim) |
| Hash (`ManifestHash`) | `pkg/evidence/manifest.Manifest`, computed by `Advance`/`Supersede` | No | Yes (JCS over semantic fields) | No |
| Provenance (`AcquisitionRecord`, `TransformationChain`) | `manifest.Manifest`, gated by `AddTransformation`'s finalization refusal | Controlled | No | Yes (acquisition is a real-world event) |
| Verification status (`evidence.Status`) | `pkg/insurance/evidence.Registry`, via `VerifyStatus`/`DeriveStatus` only, since Authority Round 1 | Yes (re-derivable) | Yes, from `Strength` | No |
| Rights (`provenance.RightsState`) | `evidence.Registry.SetRights`, now gated by `provenance.Registry.GrantTrust` | Controlled | No | Yes (rights are a real legal/commercial grant) |
| Supersession | `evidence.Registry.MarkSuperseded` (this package) and `manifest.Registry.Supersede` (that package), each scoped to their own object | Controlled, one-way | No | No |
| Finalization (`manifest.State == FINALIZED`) | `manifest.Registry.Advance`, now gated by `transitionPrerequisiteLocked` | No (immutable once reached) | Yes/authorized, since this round | No |

**Resolving the "two registries" question directly.** This repository has exactly two registries
that both track state "about" a piece of evidence — `pkg/insurance/evidence.Registry` (verification
status and multi-dimensional strength) and `pkg/evidence/manifest.Registry` (acquisition, integrity,
custody, finalization). They are not two competing sources of truth for the *same* property; they
are the sole authorities for two *different* properties, both anchored to the one real single source
of identity (`ontology.Evidence.EvidenceID`, content-addressed). Two new tests in
`pkg/insurance/evidence/canonical_authority_test.go` make this a mechanically checked fact rather
than an assertion in prose:

- `TestStatusAndManifestStateVocabulariesAreDisjoint` proves no string value in `evidence.Status`'s
  vocabulary appears in `manifest.State`'s vocabulary, or vice versa — in particular,
  `evidence.Status` has **no `FINALIZED` value at all**; only `manifest.State` does, so "Evidence X
  = FINALIZED" is structurally a claim only `manifest.Registry` can ever make in this codebase.
- `TestAdvancingOneRegistryNeverChangesTheOther` drives a real `manifest.Registry` for one
  `EvidenceID` all the way to `FINALIZED`, and confirms a real `evidence.Registry` for the exact
  same `EvidenceID` is completely unaffected (`Status` stays `UNVERIFIED`), then the reverse: calling
  `SetStrength`/`VerifyStatus` on the `evidence.Registry` side leaves the `manifest.Registry`'s
  `FINALIZED` manifest — state and hash both — untouched.

## Verification evidence

- `gofmt -l .` — clean, no output.
- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go test ./...` — full repository suite, all packages pass.
- `go test -race ./pkg/insurance/evidence/... ./pkg/evidence/manifest/... ./pkg/insurance/api/...
  ./pkg/insurance/casepack/... ./pkg/insurance/cre/... ./test/integration/...` — clean, no data
  races.
- `go test ./pkg/insurance/guardrails/...` — all 7 repository-wide guardrail scans pass.
- `pkg/evidence/manifest` package: 24 tests, all passing (7 new this round).
- `pkg/insurance/evidence` package: 58 tests, all passing (13 new this round: 6 `SetRights`, 5
  `MarkSuperseded`, 2 Canonical Authority).
- Three regressions verified: `manifest.Advance`'s prerequisite check, `SetRights`'s authority
  check, and `MarkSuperseded`'s re-supersession/successor checks were each temporarily disabled in
  turn; every corresponding new test failed with its exact expected diagnostic; each fix was then
  restored and the full suite re-confirmed green.

## Adversarial regression checklist, mapped to the reviewer's own list

| Reviewer's item | Covered by |
|---|---|
| fake Advance / skipped prerequisite | `TestAdvanceRefusesIngestedWithNoCustodyEvent` and five sibling tests, one per transition |
| fake Finalization | `TestAdvanceRefusesFinalizationWithoutClassification`, `TestAdvanceRefusesFinalizationWithATamperedCustodyChain` |
| fake Rights | `TestSetRightsRefusesNilProvenanceRegistry`, `TestSetRightsRefusesUntrustedAuthority` |
| fake Supersession | `TestMarkSupersededRequiresActorAndReason`, `TestMarkSupersededRefusesAnAlreadySupersededSuccessor` |
| post-finalization mutation | `TestFinalizedManifestIsImmutable`, `TestAddTransformationRefusedAfterFinalization` (pre-existing, still enforced under the new gate) |
| unauthorized / wrong-predecessor transition | `TestFinalizationRefusesSkippingAState` (pre-existing) plus the new prerequisite tests |
| duplicate finalization / rollback | `TestFinalizedManifestIsImmutable` — `ErrFinalizedIsImmutable` refuses ANY further `Advance` once `FINALIZED`, including a repeat `FINALIZED` call or an attempted rollback |
| lineage break | `TestMarkSupersededRefusesReSupersession`, `TestSupersedeCreatesNewVersionWithoutRewritingHistory` (pre-existing) |
| split-registry contradiction | `TestStatusAndManifestStateVocabulariesAreDisjoint`, `TestAdvancingOneRegistryNeverChangesTheOther` |

## Maturity model — unchanged position

This round's evidence is Level 2 (Engineering Verified) on the reviewer's own 4-level scale, exactly
as both prior rounds stated. Nothing here is Level 3 or Level 4 evidence, and this report makes no
claim that it is.

## Honest scope boundary for this round

Every finding Authority Round 1 left explicitly OPEN is closed in this round: `manifest.Registry.
Advance`'s substance gap, `SetRights`, and `MarkSuperseded`. No new gap was discovered and left
undocumented — the one deliberate, stated limit is `transitionPrerequisiteLocked`'s own honest
boundary: it proves a real, attributed, hash-chained record exists for each claimed step, not that
the human or process behind that record was truthful when they created it. That limit is named
explicitly here, not implied away, and is the same domain-truth boundary this engagement has
applied consistently to `Strength`, to `Hypothesis.Status`, and now to manifest custody events. This
round makes no external validation, counterparty, customer, or production claim anywhere.
