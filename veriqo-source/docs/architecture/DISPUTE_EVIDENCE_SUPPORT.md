# Dispute Evidence Support — Not Adjudication

**Status:** IMPLEMENTED · UNIT_TESTED · ADVERSARIAL_TESTED
**Supersedes:** the "Arbitration — PARTIAL" row in `INSURANCE_COMPLETENESS_AUDIT.json`

---

## 1. The row that was wrong

Every completeness audit VERIQO has produced carried one row reading
`Arbitration — PARTIAL`, with 28 of 29 domains marked IMPLEMENTED beside it.

That row was mis-framed. It measured VERIQO against a capability VERIQO must
never have. An "arbitration engine" that determined who wins would violate
Article 16 of the Evidence Constitution, and building it would not close a gap —
it would open the largest one in the system.

The row is replaced by this one:

> **Dispute Evidence Support — IMPLEMENTED.** Non-adjudicatory by constitutional
> design. Completeness is measured against the evidence VERIQO furnishes to a
> decision-maker, not against a determination VERIQO must not make.

---

## 2. What VERIQO does, and where it stops

```
                         VERIQO
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
      facts             evidence            timeline
   contradictions   causation hypotheses    quantum
  proof obligations   missing evidence   independence
        │                   │                   │
        └───────────────────┼───────────────────┘
                            ▼
              ARBITRATOR · COURT · AUTHORIZED
                    DECISION-MAKER
                            │
                            ▼
                       DETERMINATION
```

Everything above the line is VERIQO's. The determination is not, and no amount
of confidence in the evidence moves the line.

This is not modesty. It is the product. An evidence system that also decides
has an interest in the outcome, and an interested party's evidence is worth
less — to a tribunal, to a regulator, to the other side — than a disinterested
party's. VERIQO's refusal to decide is what makes what it produces usable.

---

## 3. Where the boundary is enforced

The boundary is not a policy anybody has to remember. It is checked in four
places, by code, and the tests below fail if any of them is relaxed.

| Layer | Mechanism | Test |
|---|---|---|
| Proof pipeline | `proof.Decide` refuses any attribute named in `proof.ProhibitedDecisionFields()` — `prevailing_party`, `winner`, `liable_party`, `at_fault`, `award`, `judgment`, `verdict`, `ruling` — case-insensitively | `TestDecisionMayNotAdjudicate` |
| Case fabric | `casefabric.Outcome.Validate` applies the same list to a case's disposition and summary, drawing on `proof`'s list rather than keeping a second one | `TestOutcomeMayNotAdjudicate` |
| Qualification | There is no `PROVEN`, `ESTABLISHED`, `LEGALLY_ESTABLISHED` or `LIABLE` state to reach. `state.Parse` distinguishes a forbidden state from an unknown one, so asking for one is an explicit error | `TestForbiddenStatesAreRefused` |
| Dispute domain | `dispute.Position` carries no field that could mark a contention correct — no weight, no score, no `accepted`. Two positions sit side by side, unreconciled, and that *is* the output | `TestDisputeRecordsPositionsWithoutPreferringOne` |

The dispute domain's status vocabulary carries the same discipline. The only
terminal status is `DETERMINED_BY_AUTHORITY`, which attributes the determination
to somebody else and records *what* they determined in a cited outcome — not in
the status. There is no `UPHELD`, no `DISMISSED`, no `AWARDED`.

`casefabric`'s dispute projection is shaped the same way: a dispute case
resolves at `EVIDENCE_PACKAGE_DELIVERED`. Delivering the package is the
successful end of a VERIQO dispute case.

---

## 4. What "complete" means for this domain

A dispute capability is complete when the decision-maker has what they need:

- [x] **Issues framed neutrally** — `dispute.Issue.Question`, phrased as a question
- [x] **Both parties' positions recorded verbatim** — `dispute.Position`, unweighted
- [x] **Evidence decomposed per issue** — supporting, contradicting and missing held apart, never collapsed into a score (`Issue.Decompose`)
- [x] **Legal questions separated from factual ones** — `LegalQuestion`, with `AWAITING_LEGAL_INTERPRETATION` as a real status
- [x] **Causation hypotheses with rivals tested** — `pkg/insurance/causation`, and `casefabric` refuses to resolve with an untested rival
- [x] **Quantum computed and evidence-backed** — `pkg/insurance/quantum`
- [x] **Proof obligations and gaps** — `pkg/qualification/reverseproof`, distinguishing obtained / observed-absent / unobtainable / unattempted
- [x] **Independence assessed** — `pkg/qualification/independence`, where UNKNOWN is never INDEPENDENT
- [x] **Missing evidence and next-best steps** — `pkg/qualification/nextbest`, rights-filtered before ranking
- [x] **Legal hold** — `dispute.LegalHold`
- [x] **The whole package replayable** — `pkg/replay`

Every row is built. None of them decides anything.

---

## 5. What this does not claim

- No tribunal has accepted a VERIQO evidence package. None has been offered one.
- No counsel has confirmed the issue decomposition matches how disputes are
  actually pleaded in any forum.
- The `DETERMINED_BY_AUTHORITY` path has never carried a real authority's real
  determination.
- Nothing here makes VERIQO's evidence admissible anywhere. Admissibility is a
  question for the forum, and the forum has not been asked.

These are assurance gaps, on the second axis (see
`pkg/assurance`). They are not engineering gaps, and no further engineering
closes them.
