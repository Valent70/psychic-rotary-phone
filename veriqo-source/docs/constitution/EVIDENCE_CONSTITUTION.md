# VERIQO Evidence Constitution

**Status:** FROZEN as of MIP-001 W0 (Architecture Freeze)
**Executable form:** `pkg/constitution` — every article below has a
machine-checkable representation. MIP-001 §4 requires this: *"These
shall become executable policy tests, not merely documentation."*

---

## 0. What this document is

Thirty invariants that bind every VERIQO subsystem. They are not
aspirations and not style guidance. An article is satisfied only when
a check in `pkg/constitution` can evaluate a concrete artifact against
it and return a determinate verdict.

Three verdicts exist, and the third is load-bearing:

| Verdict | Meaning |
|---|---|
| `SATISFIED` | The article was checked against this input and holds. |
| `VIOLATED` | The article was checked and does **not** hold. |
| `NOT_EVALUABLE` | The input does not carry what the article needs to be judged. **Never** read as satisfied. |

`NOT_EVALUABLE` exists because the alternative — treating an
unanswerable question as a pass — is precisely the failure mode this
constitution is written to prevent. It is the constitutional analogue
of the verifier's `SKIP`.

---

## 1. The articles

### Group A — Evidence and truth

**Article 1 — No Naked Facts.**
No finding may exist without evidence lineage. A finding that cites no
evidence, or cites evidence with no traceable lineage, is not a finding.

**Article 2 — No Truth by Acquisition.**
Acquiring an artifact establishes that it was acquired. It does not
establish that its contents are true. `ACQUIRED ≠ TRUE`.

**Article 3 — No Corroboration by Duplication.**
Two artifacts sharing a root origin, provider pipeline, sensor,
collector, or organizational control are **one** source for
corroboration purposes. Same-root data can never satisfy an
independent two-source requirement.

**Article 4 — No Authorization, No Contact.**
A rights or authority failure denies acquisition before any contact
with the source occurs. The check precedes the network call; it is not
a post-hoc audit.

**Article 5 — Raw Before Transform.**
The raw artifact must be preserved before any transformation runs. A
parsed representation that outlives its raw original is invalid
evidence.

**Article 6 — Immutable After Finalization.**
A finalized evidence version is never updated. Correction produces a
new version with explicit lineage to the old.

**Article 22 — Evidence Version Is Immutable.**
Every derivative is a new version. Restated separately from Article 6
because Article 6 governs the original and Article 22 governs the
derivative chain.

### Group B — Epistemic discipline

**Article 28 — No Unsupported Independence.**
Independence is derived from lineage, never asserted. An unproven
dependency relation is `UNKNOWN`, and `UNKNOWN` is not `INDEPENDENT`.

**Article 29 — No Unqualified Absence.**
Absence may only be reported as `OBSERVED_ABSENT` after the
observability gate passes. Otherwise it is `EXPECTED_BUT_NOT_TESTED`,
`NOT_OBSERVABLE`, `NOT_COLLECTABLE`, `PARTIAL_COVERAGE`,
`SOURCE_UNAVAILABLE`, or `INCONCLUSIVE`.

**Article 30 — No Absolute Epistemic Claims.**
Integrity, provenance, qualification, procedural neutrality, and legal
determination are five distinct claims. Collapsing any two is a
violation.

**Article 11 — Disagreement Must Remain Visible.**
Dissent is never deleted, downgraded, or silently resolved. Material
unresolved dissent must appear in the final finding.

### Group C — AI authority

**Article 8 — AI Has No Evidence Authority.**
No AI system may create, alter, qualify, or sign authoritative
evidence. AI proposes; the evidence fabric disposes.

**Article 27 — No Silent AI Influence.**
Material AI contribution is recorded — model, version, prompt hash,
input evidence versions, policy version, output hash, human reviewer.
Influence without a contribution record is a violation.

**Article 21 — Redaction Does Not Imply AI Eligibility.**
A redacted derivative is not automatically AI-processable. It must
independently pass AI processing policy.

### Group D — Access and disclosure

**Article 20 — Access Does Not Imply Use.**
`VIEW ≠ SEARCH ≠ COPY ≠ PRINT ≠ DOWNLOAD ≠ EXPORT ≠ REDISTRIBUTE ≠
AI_PROCESS ≠ RAG ≠ TRAIN`. Each is separately granted.

**Article 24 — No Silent Disclosure.**
Every disclosure is an event on the canonical ledger. There is no
disclosure path that does not emit one.

**Article 19 — Privilege Is Authority-Determined.**
VERIQO enforces a privilege decision made by an authorized legal
authority. It never makes the substantive determination itself.

**Article 25 — No Silent Privilege Change.**
A privilege status transition is an immutable event.

### Group E — Redaction

**Article 17 — No Destructive Redaction.**
The original is never modified. Redaction produces a derivative.

**Article 18 — No Visual-Only Redaction.**
A black rectangle drawn over text is not redaction. The content must
be absent from the derivative's bytes, metadata, and recoverable
layers.

### Group F — Procedure and neutrality

**Article 12 — Procedural Symmetry.**
Parties are treated under the same policy unless an authorized,
recorded exception exists.

**Article 13 — Party Influence Must Be Disclosed.**
Which party requested what evidence, what scope, what source, what was
rejected — all recorded as process evidence.

**Article 14 — Conflict Must Be Declared.**
Conflicts of interest for people, reviewers, providers, models,
vendors, and data dependencies are declared, never concealed.

**Article 15 — No Outcome-Contingent Neutrality.**
VERIQO may not benefit differentially from the outcome of a dispute it
provides evidence for.

**Article 16 — No Adjudication by Platform.**
VERIQO does not determine legal liability. A qualification is not a
verdict.

### Group G — Policy and time

**Article 7 — Historical Policy Pinning.**
A historical case is evaluated under the policy version in force at the
time, not today's.

**Article 26 — No Silent Policy Retroactivity.**
A policy change is never quietly applied to an already-computed
historical result.

### Group H — Verification

**Article 10 — Replay Must Be Independent.**
Replay must be verifiable without trusting the primary runtime.

**Article 23 — Audit Is Evidence.**
Process evidence — how the evidence was obtained, decided, and
disclosed — is itself evidence, and is governed by the same
constitution.

**Article 9 — ZKP Has Bounded Meaning.**
A zero-knowledge proof establishes exactly the predicate it was
constructed for. It does not establish the truth of the underlying
world, liability, causation, legal validity, or admissibility.

---

## 2. Article dependency

Some articles subsume others; violating the deeper one violates both.

```
Article 2 (No Truth by Acquisition)
  └─ Article 3 (No Corroboration by Duplication)
       └─ Article 28 (No Unsupported Independence)

Article 30 (No Absolute Epistemic Claims)
  ├─ Article 16 (No Adjudication by Platform)
  └─ Article 29 (No Unqualified Absence)

Article 8 (AI Has No Evidence Authority)
  ├─ Article 27 (No Silent AI Influence)
  └─ Article 21 (Redaction ≠ AI Eligibility)

Article 6 (Immutable After Finalization)
  └─ Article 22 (Evidence Version Is Immutable)
       └─ Article 17 (No Destructive Redaction)
```

A checker reporting Article 3 satisfied while Article 28 is violated is
itself defective — `pkg/constitution` enforces this consistency.

---

## 3. Enforcement classes

Not every article is enforceable by the same mechanism. Stating which
is which prevents the illusion that a document check equals a runtime
guarantee.

| Class | Meaning | Articles |
|---|---|---|
| **STRUCTURAL** | The type system or state machine makes violation unrepresentable. | 6, 17, 22 |
| **CHECKED** | A runtime check refuses the operation. | 1, 3, 4, 5, 8, 19, 20, 24, 25, 28, 29 |
| **RECORDED** | Violation is not preventable, but is always detectable after the fact from the ledger. | 11, 12, 13, 14, 23, 26, 27 |
| **DECLARED** | An organizational commitment the software surfaces but cannot enforce alone. | 15, 16, 30 |
| **BOUNDED** | Enforced by construction of the mechanism's own semantics. | 9, 10, 21 |
| **PINNED** | Enforced by version binding. | 7 |

**DECLARED is the honest class.** Article 15 (no outcome-contingent
neutrality) cannot be enforced by code — it is a commercial
arrangement. `pkg/constitution` marks these as `DECLARED` and reports
them as requiring external attestation rather than pretending a unit
test settles them.

---

## 4. Relationship to the existing invariant set

An earlier round produced INV-001..INV-010 in the Trust Authority
Model. Those remain in force. This constitution is broader (governance,
disclosure, AI, procedure) and does not supersede them; where they
overlap, the stricter reading governs. `pkg/constitution` documents the
mapping per-article.

---

## 5. Amendment

Articles are frozen. An amendment requires a new numbered article, a
recorded rationale, and a version bump on the constitution document
hash — never an edit in place. This document's own governance follows
Article 6.
