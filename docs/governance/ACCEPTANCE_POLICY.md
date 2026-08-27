# Acceptance Suite Policy (PHASE 34/35)

The permanent acceptance suite lives in `test/acceptance/`. It is
**append-only**.

## Permanent minimums

| Category | Minimum | Current |
|----------|---------|---------|
| normal | 20 | 20 |
| adversarial | 20 | 20 |
| replay_tamper | 20 | 20 |
| identity_temporal | 20 | 20 |
| distributed_concurrency | 20 | 20 |
| security | 10 | 10 |
| **total** | **110** | **110** |

## Mandatory named tests

Deleting any of these fails the release gate, regardless of how many
other tests pass:

- `TestUnifiedIntentTrustDecisionLifecycle`
- `TestCanonicalUsesEvidenceDependencyGraph`
- `TestSharedSatelliteProviderCannotInflateConfidence`
- `TestFullLifecycleReplayMatches`
- `TestReplayIdentitiesAreAllDistinct`
- `TestUnmergePreservesHistoricalReplay`
- `TestOneCriticalGapCannotBeCompensated`

## Rules

1. A category count may increase. It may never decrease.
2. A mandatory test may be renamed only by updating the manifest in the
   same commit, with the rename visible in review.
3. New capability adds tests to the relevant category; it does not
   replace existing ones.
4. Enforced mechanically by `internal/assurance.AcceptanceManifest.Validate`,
   invoked by `cmd/veriqo-readiness`.

## Categories not yet present

`api`, `persistence`, `disaster_recovery` — these require the REST/gRPC
surface, the WAL/snapshot storage layer and a DR drill respectively.
They are tracked as OPEN in the traceability matrix rather than
back-filled with tests that would not exercise anything real.
