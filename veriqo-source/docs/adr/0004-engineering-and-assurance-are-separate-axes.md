# ADR-0004: Completion is reported on two axes that never combine

**Status:** Accepted
**Implemented by:** `pkg/assurance`

## Context

"Wave 13 complete" and "production qualified" are different claims, and previous
rounds' status language blurred them. A capability can be fully engineered —
designed, implemented, race-tested, replayed — and still rest entirely on
VERIQO's own word, because nobody outside has ever examined it.

A single completion percentage hides exactly the half a buyer, a regulator or a
tribunal cares about.

## Decision

Two axes, and no method that reduces them to one number:

```
ENGINEERING AXIS   NOT_STARTED -> DESIGNED -> SCAFFOLDED -> IMPLEMENTED
                   -> UNIT_TESTED -> INTEGRATION_TESTED
                   -> ADVERSARIAL_TESTED -> REPLAY_VERIFIED

ASSURANCE AXIS     UNPROVED -> SELF_ASSERTED -> INTERNALLY_PROVED
                   -> EXTERNALLY_VALIDATED -> PRODUCTION_QUALIFIED
```

`Status` has no `Overall()`, no `Score()`, no `Percent()`. `AxisReport` is
asserted by test to contain no `%` character.

Everything from `EXTERNALLY_VALIDATED` up satisfies
`RequiresOutsideParty()`, so no amount of further engineering reaches it.

Alongside, one traceability chain per constitutional article —
CONSTITUTION → CONTROL → CODE → TEST → EVIDENCE → REPLAY → QUALIFICATION →
EXTERNAL PROOF — yielding five verdicts, each naming a *different* kind of
incompleteness: OPEN, INTEGRATION_GAP, ASSURANCE_GAP, EXTERNAL_QUALIFICATION,
QUALIFIED.

## Consequences

- `Open` is the zero `Verdict`; `Unproved` and `NotStarted` the zero levels. A
  control nobody traced is open, never qualified.
- Only `QUALIFIED` is `Closed()`. `EXTERNAL_QUALIFICATION` is *blocked*, which is
  a different thing and must not be reported as done.
- A trace link asserted with no reference is refused, which is what keeps a
  traceability matrix from becoming a spreadsheet of good intentions.
- `TestNothingIsQualified` asserts the current honest position. The day an
  outside party examines a control, that test fails and somebody has to update
  the claim deliberately.
- Cost: the reported position looks worse than a percentage would. It is the
  same position.
