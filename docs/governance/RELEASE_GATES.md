# VERIQO Release Gates (PHASE 53/54)

Run: `./scripts/production-readiness.sh`

Produces `READINESS_MANIFEST.json` plus per-gate evidence artifacts in
`evidence/`. Each artifact is content-hashed; a gate declared VERIFIED
without a passing artifact is automatically degraded by
`internal/assurance.Gate.EffectiveStatus`.

## Mandatory gates evidenced in-repo

| Gate | Exit criteria |
|------|---------------|
| `build` | `go build ./...` clean |
| `vet` | `go vet ./...` clean |
| `format` | `gofmt -l .` empty |
| `unit` | `go test ./...` passes |
| `race` | `go test -race -p 1 ./...` passes |
| `acceptance` | permanent acceptance suite passes |
| `dependency_integration` | PHASE 1 gate tests pass |
| `replay` | full-lifecycle independent replay matches |
| `identity` | unmerge preserves historical replay |
| `security_unit` | key lifecycle + authorization invariants pass |
| `assurance_self` | the assurance plane refuses false green |
| `fuzz_smoke` | all fuzz targets run clean on seeds |
| `zero_dependency` | no external Go modules |

## Mandatory gates BLOCKED (cannot be evidenced from a build sandbox)

`pentest`, `scale_qualification`, `multi_region_dr`, `hsm_kms`,
`live_data`, `soak_72h`, `spire_mtls`, `supply_chain_scan`.

Each carries a named blocker in the manifest. **Because these are
mandatory and blocked, `production-readiness.sh` exits non-zero and
the verdict is `NOT_PRODUCTION_READY`.** This is correct behaviour, not
a bug: a laptop must not be able to certify a distributed trust system.

## Status vocabulary

Only `OPEN`, `IMPLEMENTED`, `INTEGRATED`, `VERIFIED`, `QUALIFIED`,
`PRODUCTION_READY`, `BLOCKED`, `WAIVED`. The words "Done", "Closed" and
"Complete" are not statuses and do not appear in the manifest.

A waived mandatory gate yields `CONDITIONALLY_READY_WITH_WAIVERS` —
never `PRODUCTION_READY` — and the waiver's owner, approver, expiry and
justification are part of the signed certificate.
