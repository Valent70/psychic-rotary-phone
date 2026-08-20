# Security Policy — Veriqo Kernel

## Reporting

Report suspected vulnerabilities privately to the maintainers rather
than opening a public issue. Include the affected package, a
minimal reproduction, and the invariant you believe is violated.

## Architectural security invariants

These are enforced by design and checked in `scripts/verify.sh` /
CI, not just documented intent:

- **Zero external Go dependencies.** Every package imports only the
  Go standard library (verified via `go list -deps ./...` in CI).
  This eliminates the supply-chain surface of third-party modules
  entirely — there is no `go.sum` to audit because there is nothing
  in it.
- **No `time.Now()`.** All state transitions carry an explicit
  logical `tick` supplied by the caller. This removes an entire
  class of TOCTOU and replay-ambiguity bugs, and makes every kernel
  operation byte-for-byte reproducible for audit and forensics.
- **No UUIDs.** Every identity (`Entity.ID`, `Evidence.ID`,
  `RunID`, ...) is `SHA-256` of canonicalized content. Identity
  cannot be spoofed by presenting a syntactically valid-looking
  random ID; it must match a hash of real content.
- **Append-only, hash-chained logs** across WAL, evidence,
  fusion, causal, digital twin, and workflow checkpointing.
  `VerifyChain()` / `ReplayAll()` on each detects any
  post-hoc tampering.
- **No ledger mutation after append.** Corrections are new records
  referencing prior ones, never in-place edits.

## Known offline-blocked security tooling

This sandbox's network egress allowlist does not include
`proxy.golang.org` or most package registries. As a result the
following are specified and wired into CI (`.github/workflows/ci.yml`)
but have **not** been executed inside this project's development
sandbox — only `go build`, `go vet`, `gofmt`, and `go test -race`
have:

- `golangci-lint` (config present at `.golangci.yml`)
- `govulncheck` (job present in CI)
- SPIRE/SPIFFE workload identity attestation
- OPA policy bundle validation
- `gosec` (included in the golangci-lint config, not run standalone)

Treat these as configured-but-unverified until run in an environment
with registry access.
