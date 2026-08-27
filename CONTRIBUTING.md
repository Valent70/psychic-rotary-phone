# Contributing to Veriqo Kernel

## Invariants (non-negotiable, enforced by review and by `scripts/verify.sh`)

1. **No `time.Now()`.** Every event carries an explicit logical `Tick`
   (`uint64`), supplied by the caller. This is what makes every layer's
   `Rebuild`/`ReplayAll` byte-identical across independent runs.
2. **No UUIDs.** Every identifier is content-addressed: `SHA-256` over a
   canonical byte encoding of the entity's fields, with map keys always
   sorted before hashing.
3. **Append-only.** Never mutate a past log entry. Corrections are new
   entries (see `pkg/moat/temporal` for the canonical pattern).
4. **Zero external Go dependencies.** `go.mod` must never gain a
   `require` line. Run `go list -deps ./...` and confirm every non-stdlib
   entry is Go's own internal vendoring (currently only
   `golang.org/x/crypto`/`x/net`/`x/sys` pulled in by `crypto/tls`
   itself). This is a hard constraint of the current sandbox (no
   `proxy.golang.org` access), not a permanent architectural choice —
   see `docs/OBSERVABILITY.md` for what changes once a real module proxy
   is available.
5. **Every non-trivial decision is explainable.** If your function
   produces a risk score, a winner, or an action, it must also produce
   (or make cheaply derivable via a pure function like
   `decision.ExplainBreakdown`) a human-readable trail of why.

## Before opening a PR

```
gofmt -l .                 # must print nothing
go vet ./...                # must be clean
go build ./...               # must succeed
go test ./... -race -cover   # must pass, note the coverage delta
go list -deps ./... | grep -v '^veriqo'   # eyeball for new external deps
```

`scripts/verify.sh` runs all of the above plus a 5x `-race` repeat on
the historically fragile packages (`pkg/consensus/raftlite`,
`pkg/workflow`, `pkg/platform/audit`) — run it before pushing.

## Commit / PR style

- One logical change per commit; the commit message states which gap or
  audit-doc item it closes, if any (this repository's whole history is
  gap-driven — see `README.md`'s gap-mapping table for the pattern).
- New packages get a package-level doc comment stating: what problem it
  solves, what design invariants it follows, and — critically — an
  **honest scope statement** of what it does NOT do yet. Overclaiming
  in a doc comment is treated as a bug.
- Every new exported type/function that produces a hash-chained record
  needs: a `VerifyChain()`-style tamper detector, a `Rebuild()`-style
  deterministic-replay reconstructor, and a test proving both.

## Backward compatibility

There is no stable public API contract yet (no `v1` tag, no semver
promise) — this is pre-GA reference-implementation code, stated
honestly rather than implying a compatibility guarantee that doesn't
exist. Breaking changes to exported types are acceptable with a note in
the commit message; once a `cmd/veriqo-api` binary ships (see
`README.md` open items), that surface will need real versioning.
