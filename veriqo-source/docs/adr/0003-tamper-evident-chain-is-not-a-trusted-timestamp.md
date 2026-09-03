# ADR-0003: A tamper-evident chain and an RFC 3161 attestation are different types

**Status:** Accepted
**Implemented by:** `pkg/platform/timestamp`

## Context

VERIQO operates a self-hosted timestamping facility that chains entries so
reordering or alteration is detectable. That is genuine integrity chaining.

Earlier reports edged toward describing it as trusted timestamping. RFC 3161
defines a trusted timestamp as an assertion, by a Time-Stamping Authority acting
as a **trusted third party**, that a datum existed before a particular time. The
load-bearing words are "third party": the value comes from the attester being
someone other than the party who benefits. A facility VERIQO runs, for evidence
VERIQO holds, in a dispute VERIQO's customer is party to, is not a third party
however good its cryptography.

## Decision

Make them different types with different capabilities.

```
VERIQO self-hosted timestamp  =  tamper-evident temporal chain
External TSA                  =  independent temporal attestation
```

`Kind.ProvesExistenceBefore()` returns true only for
`IndependentAttestation`. `Attestation.Kind` is **derived by `Assess`**, never
set by a caller — the field that decides whether "this existed before then" may
be claimed is not one anybody can write.

A TSA operated by any party to the matter, VERIQO included, does not raise an
attestation to independent. The token is retained and the downgrade is
explained, but it is not counted.

## Consequences

- `Describe()` gives reports a sentence to quote. A chain-only attestation's
  sentence contains "No independent attestation of wall-clock time" and cannot
  contain "existed before" — asserted by test.
- `ChainAttestation` deliberately has **no wall-clock field**. Adding one would
  invite reading it as an attested time.
- Cost: VERIQO holds no TSA relationship, so no evidence in the system currently
  carries an independent attestation. This decision makes that visible rather
  than fixing it.
