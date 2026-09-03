# ADR-0002: Every significant conclusion carries a sealed Proof Object

**Status:** Accepted
**Implemented by:** `pkg/proof`

## Context

A conclusion on its own is not reviewable. "The cargo was contaminated before
loading" is a sentence. What makes it evidence is the apparatus behind it —
which evidence, whose, how independent, what contradicts it, what is missing,
what would have falsified it, who could see it, whether a model touched it, and
what remains outside VERIQO's power to establish.

Before this decision that apparatus was spread across a dozen packages and
reassembled by hand for each report, which meant it could be reassembled
differently, or incompletely, each time.

## Decision

One value carries all of it: a Proof Object with twenty-three components, sealed
with a JCS canonical hash, and a pipeline in which each stage is a distinct type
with unexported fields.

Three properties do the work:

1. **Stance and sufficiency are derived, never asserted.** `Seal` overwrites
   whatever the author wrote. A conclusion cannot be made sufficient by writing
   the word.
2. **Skipping a stage does not compile.** A `Decision` can be built only from an
   `AuthorizedFinding`, which can be built only from a `Finding`, which can be
   built only from a sealed, sufficient object whose hash re-verifies.
3. **Absence is load-bearing.** `Unknown` is the zero `Stance`, `NotDetermined`
   the zero `Sufficiency`, `NotSought` the zero `ExternalStatus`. An object whose
   author forgot says "we do not know", never "supported".

## Consequences

- An insufficient object yields ranked next-best evidence instead of a finding,
  so "what is missing" is a first-class output rather than an omission.
- `Validate` refuses an object with no reverse-proof set, no stated limitations,
  or unpinned evidence. Such an object is not a weaker proof object; it is one
  nobody can check.
- Cost: assembling a Proof Object is heavy. It is meant to be — it is what a
  significant conclusion costs, and the alternative was conclusions nobody could
  audit.
