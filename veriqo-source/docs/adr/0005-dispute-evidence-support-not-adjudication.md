# ADR-0005: VERIQO furnishes evidence to a decision-maker and never decides

**Status:** Accepted
**Full statement:** `docs/architecture/DISPUTE_EVIDENCE_SUPPORT.md`

## Context

Every completeness audit carried one row reading `Arbitration — PARTIAL`, with
28 of 29 domains marked IMPLEMENTED beside it. The row measured VERIQO against a
capability it must never have. Building an arbitration engine that determined
who wins would not close a gap; it would open the largest one in the system,
against Article 16.

## Decision

Reframe the capability as **Dispute Evidence Support**, and enforce the boundary
in code at four independent layers:

| Layer | Mechanism |
|---|---|
| `proof.Decide` | refuses any attribute in `ProhibitedDecisionFields()`, case-insensitively |
| `casefabric.Outcome.Validate` | applies the same list — drawn from `proof`, not a second copy — to a case's disposition and summary |
| `qualification/state` | has no `PROVEN`, `ESTABLISHED`, `LEGALLY_ESTABLISHED` or `LIABLE` value; `Parse` names them as forbidden rather than merely unknown |
| `dispute.Position` | carries no field that could mark a contention correct — no weight, no score, no `accepted` |

`casefabric`'s dispute projection resolves at `EVIDENCE_PACKAGE_DELIVERED`, and
the only terminal issue status is `DETERMINED_BY_AUTHORITY`, which attributes
the determination to somebody else.

## Consequences

- The completeness row becomes `Dispute Evidence Support — IMPLEMENTED`,
  measured against the evidence furnished rather than a determination withheld.
- This is not modesty. An evidence system that also decides has an interest in
  the outcome, and an interested party's evidence is worth less. The refusal is
  what makes the output usable.
- Cost: customers who want an answer will not get one from VERIQO. That is the
  product boundary, not a missing feature.
