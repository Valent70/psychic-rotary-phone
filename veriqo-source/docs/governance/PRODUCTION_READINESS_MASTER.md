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

## What changed since (through round R19)

This section is a snapshot note, not a rewrite of the paragraphs above —
`REQUIREMENT_TRACEABILITY_MATRIX.md` (generated from
`docs/governance/requirements.json`, never hand-edited) remains the one
authoritative, currently-accurate source for per-requirement status; the
PHASE-list summary above reflects v7.11.0 and has not been kept in lock
step release-by-release. As of R19, of the matrix's 51 tracked
requirements: 46 VERIFIED (7 of those with an inline caveat naming
exactly what's still missing), 1 IMPLEMENTED-but-not-executed-here, 0
OPEN, 4 BLOCKED on infrastructure/third parties genuinely outside any
coding session's reach. The verdict above (**NOT_PRODUCTION_READY**) is
unchanged and expected to stay that way until the same 8 mandatory
gates this document already names are unblocked by real procurement —
narrowing the requirement count is a different axis from clearing those
8 gates, and this round changed only the former.

R19 specifically closed R-050 (the one row that was genuinely OPEN, not
BLOCKED — real ingestion contracts for SAR/BoL/insurance/payment,
`pkg/connector/{sar,bol,insurance,payment}`) and narrowed R-029's
standing caveat (`pkg/consensus/raftlite`: an explicit learner/voter
role distinction, adversarial corrupted/truncated/stale-snapshot tests,
and a named leader-during-reconfiguration scenario, all closed with real
tests). See `docs/governance/AUDITOR_PRIORITY_RECONCILIATION.md`'s R19
addendum for the full detail, including the one honest narrowing that
remains open (joint consensus does not yet compose with learners) and a
confirmed-not-stale finding on R-028's own caveat.
