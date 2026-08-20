# VERIQO Production Readiness — Master Document (v7.11.0)

## Definition in force

> Production-ready = implemented + integrated + deterministic + secure +
> observable + recoverable + independently verifiable +
> performance-qualified + operationally deployable + continuously
> regression-protected.

The obsolete definition — "implemented + tests pass = closed" — is no
longer expressible in this repo: `internal/assurance` has no API that
accepts a status without evidence.

## Current verdict

**NOT_PRODUCTION_READY.**

Not because the intelligence core is unfinished — the Wave A core
closure is done and tested — but because eight mandatory gates
(`pentest`, `scale_qualification`, `multi_region_dr`, `hsm_kms`,
`live_data`, `soak_72h`, `spire_mtls`, `supply_chain_scan`) are BLOCKED
on infrastructure and third parties that no sandbox can supply, and
because Waves C–F (storage, API, observability, data governance) remain
OPEN.

Run `./scripts/production-readiness.sh` to reproduce this verdict from
real evidence rather than from this sentence.

## Capability lifecycle

Every capability must walk this path. Skipping a step is what the
assurance plane exists to detect.

```
Requirement -> Implementation -> Integration -> Unit Test ->
Adversarial Test -> Failure Test -> Replay Test ->
Operational Evidence -> Production Gate
```

## Documents

- `REQUIREMENT_TRACEABILITY_MATRIX.md` — every requirement, owner, test, status
- `ARCHITECTURE_INVARIANTS.md` — the invariants, each with its enforcing test
- `RELEASE_GATES.md` — what must pass, and what is blocked and why
- `ACCEPTANCE_POLICY.md` — the append-only acceptance suite contract

## What changed in v7.11.0

Closed with tests: PHASE 0, 1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 14, 15, 28,
34, 35, 36, 38, 52, 53, 54, 55.

Still open, tracked, unclaimed: PHASE 7, 12, 13, 16, 17, 18, 19, 20,
21–27, 29–33, 37, 39–51.
