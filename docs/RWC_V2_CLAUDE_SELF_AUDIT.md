# RWC v2 — Self-Audit

Per the brief's §19, before declaring completion: inspection of every file
added by this work (`pkg/rwc/*.go`, `cmd/veriqo-rwc-v2/main.go`) against
each named failure mode. Every claim below was checked mechanically
(`grep`, `go vet`, `go test -race`, or direct code reading) in this
session, not asserted from memory.

## 1. Bypass of UCR/UER

**Not found.** `pkg/rwc/run.go`'s `Run` function calls exactly one entry
point into the native system: `k.Lifecycle.RunUnified(ctx, intent, plan,
caseIn)` — the same `pkg/lifecycle.Orchestrator.RunUnified` every real
production caller (`veriqo/gateway/rest/lifecycle_route.go`'s `POST
/lifecycle/run_unified`) uses. No RWC code calls
`pkg/canonical.Pipeline.RunCanonical` directly, `pkg/execution.Engine.Run`
directly, `veriqo/registry.Registry` (the Registry/UCR administrative
path), or any sub-engine (`Fusion`, `Contradiction`, `Decision`, `Trust`)
directly for a decision. Verified: `grep -rn "RunCanonical\|Execution.Run\|
Registry\." pkg/rwc cmd/veriqo-rwc-v2` returns zero matches outside
`replay.go`'s use of `k.Canonical.Dependencies.ReplayAll()` and
`k.Canonical.Fusion.Head()` — both read-only inspection calls made *after*
`RunUnified` already ran, for replay/ledger-anchor reporting, never a
second execution path.

## 2. Duplicate engines

**Not found.** `InterpretVerdict` (`pkg/rwc/policy.go`) does not compute a
decision — it is a pure function of `ConstraintResult` (evidence-derived
counts) that also cross-checks against the real `decision.Action` and
raises a `consistencyWarning` on mismatch (never silently overrides it).
`ClassifyProvenance` similarly reads only already-computed
`canonical.CanonicalResult` fields (`Truth.Observation.Contradiction`,
`Provenance.Score`). No new Fusion, Arbitration, Trust, or Decision
engine was written.

## 3. Hidden randomness

**Not found.** `grep -rn "math/rand\|crypto/rand" pkg/rwc
cmd/veriqo-rwc-v2` returns zero matches.

## 4. `time.Now()`

**Not found in executable code.** `grep -rn "time\.Now" pkg/rwc
cmd/veriqo-rwc-v2` returns two matches, both inside comments explicitly
documenting its *absence* (`run.go:18`, `main.go:25`), not a call.
`baseTime`/`tick` is a caller-injected `uint64` constant (`1`) threaded
through every case — the brief's required baseTime-injection pattern.

## 5. UUID generation in deterministic paths

**Not found.** `grep -rn "uuid" pkg/rwc cmd/veriqo-rwc-v2` (case-
insensitive) returns zero matches. Every ID (`InputHash`, `ExecutionID`,
`CanonicalHash`, `CertificateHash`) is either SHA-256 content-derived
(`pkg/rwc/types.go`'s `hashOf`) or produced by the native, already-audited
`lifecycle.Intent.ID()` / `execution.Context.ExecutionID` derivation —
neither of which this package modified.

## 6. Nondeterministic map iteration

**Checked, one category found and assessed as safe, documented here
rather than silently left.** `cmd/veriqo-rwc-v2/main.go` builds several
`map[string]any` values (`evidenceExport`, `trustResults`,
`decisionResults`, `ledgerAnchors`, `inputHashes`, and the top-level
`files` map used only to drive the write loop) and ranges over them.
This is safe, not merely convenient, for two independent reasons: (a) Go's
`encoding/json` sorts `map[string]T` keys alphabetically on marshal — the
serialized bytes are deterministic regardless of Go's randomized map
iteration order (verified: reran `go run ./cmd/veriqo-rwc-v2` twice, byte-
identical `evidence/rwc_v2/*.json` — checked with `sha256sum` before
writing this document); (b) the `files` map's iteration order only decides
which *file* gets written in which order on disk, never influences any
individual file's *content*. No hash or certificate value depends on map
iteration order — every hash in `pkg/rwc/types.go`'s `hashOf` is computed
from a Go struct (`CaseRequest`), whose fields marshal in fixed
declaration order, not a map.

## 7. Fake ledger entries

**Not found.** `ledger_anchor` (`pkg/rwc/run.go`) is
`k.Canonical.Fusion.Head()` — the real, live head of the actual
`pkg/moat/fusion.Engine` hash chain this case's arbitration was appended
to by the real `RunUnified` call, independently re-derivable via
`Fusion.VerifyChain()`. No ledger entry, hash, or chain head was invented,
hand-typed, or asserted without a corresponding real `Observe`/`Submit`
call having produced it. What this field is *not* — durable/external
anchoring — is stated explicitly in
`docs/VERIQO_RWC_V2_VALIDATION_REPORT.md` §10 and the baseline doc's gap
G5, not glossed over.

## 8. Hardcoded expected results

**Not found in production code; present, correctly, in test assertions
only.** `grep -n 'CaseID ==\|c\.ID ==\|"RWC-001-[A-E]"' pkg/rwc/*.go
cmd/veriqo-rwc-v2/*.go` (excluding `_test.go`) returns zero matches — no
production file branches on a case ID or vessel name to decide a verdict.
`Verdict` values are assigned in exactly one place outside constant
declarations and tests: `InterpretVerdict` in `pkg/rwc/policy.go`, purely
from `ConstraintResult.Evaluated`/`HardViolations`/`Unresolved` counts —
no case identity is even in scope inside that function. The `want :=
map[string]Verdict{...}` table in `rwc001_test.go` is a **test
assertion** (comparing the brief's stated expected outcome against the
emergent result), which is what the brief's §4 itself specifies
("Expected: Akonikien = PASS", etc.) — this is the correct and required
use of an expected-value table in a test, categorically different from
production code returning a hardcoded verdict.

## 9. Fake external verification

**Not found; the one "corroboration" claim made is real and
network-free.** RWC-002's vessel-identity case reaches CORROBORATED
status via `pkg/rwc/identity_checks.go`'s `ValidateIMOCheckDigit` (the
real IMO Resolution A.600(15) check-digit algorithm — arithmetic on the
claimed 7-digit number itself) and `LookupMMSICountry` (a static,
hard-coded-and-labeled-as-such MID table, explicitly documented as
non-exhaustive and honestly reporting "not in local reference table" for
any MID it doesn't recognize, rather than silently passing or failing).
Neither function makes a network call, reads a file, or reads any value
this session did not compute itself. No claim anywhere in `pkg/rwc`
attributes a result to MagicPort, MarineTraffic, or any other named
external source that was not actually queried — see
`docs/VERIQO_RWC_V2_VALIDATION_REPORT.md` §3/§11 for the explicit
statement that those sources were named by the user as checked but were
never independently reached by this system.

## 10. Undocumented assumptions

Checked against the actual design decisions made; every one below is
documented at its point of use, not silently assumed:

- RWC-001's `PatternScore`/`PriceAnomaly` are both set to the same
  `ViolationRatio` value — documented in `pkg/rwc/policy.go`'s
  `VesselPortPolicy` comment and `pkg/rwc/rwc001.go`'s
  `buildVesselPortCase`.
- The 3-band `ViolationRatio` scheme (0 / 0.5 / 1.0) — documented in
  `pkg/rwc/constraints.go`'s `ViolationRatio` doc comment as "the one free
  design parameter in this adapter".
- `VesselPortPolicy`'s threshold values (0.3 / 0.7) — documented in
  `pkg/rwc/policy.go` with the exact arithmetic justifying them (single-
  source case ⇒ independence discount = 1.0 ⇒ composite = ViolationRatio
  exactly).
- Douala's draft dimension is marked `Unresolved` regardless of the
  numeric comparison outcome — documented in `pkg/rwc/constraints.go`'s
  `EvaluateVesselAtPort`, `TideDependent` case, with the reasoning (no
  live tide feed in this environment).
- Lomé's two conflicting corpus-supplied max-draft figures were not
  resolved, only recorded — documented in `pkg/rwc/ports.go`'s `Notes`
  field for that port.
- RWC-001 candidates B/C/D/E are each declared as the baseline (candidate
  A) plus exactly one named field mutation — documented in
  `pkg/rwc/rwc001.go`'s `RWC001Candidates` doc comment, and structurally
  enforced (each candidate literally copies `RWC001BaselineVessel` and
  changes one field, not independently re-typed).
- This baseline's own top-of-file correction that no RWC v1 corpus exists
  in this repository, contradicting the brief's stated premise — see
  `docs/RWC_V2_NATIVE_INTEGRATION_BASELINE.md`, first paragraph.

## 11. Tests that only test mocks instead of native components

**Not found.** Every test in `pkg/rwc/*_test.go` calls `kernel.New()` —
the real, unmodified `veriqo/kernel` composition root that boots the
actual `Registry`, `Runtime`, `Evidence`, `Intelligence`, `Canonical`,
`Lifecycle`, `Identity`, and `TrustCalculus`/`TrustLedger` — the same
construction a real production process would perform. No test constructs
a fake/mock `Kernel`, a fake `Lifecycle.Orchestrator`, or a stub
`decision.Engine`. `TestRWC001DeterministicReplay` additionally exercises
the real `pkg/replay.Engine`, which by its own package design
(`pkg/replay`'s doc comment) constructs a brand-new `canonical.Pipeline`
with zero shared state with the original run — independence is
structural, not asserted.

## 12. Production claims unsupported by evidence

**Not found.** `docs/VERIQO_RWC_V2_VALIDATION_REPORT.md` explicitly states
the brief's own required claim boundary near its top ("does **not** claim
VERIQO is production ready") and its final classification table marks the
8 pre-existing production blockers, durable ledger anchoring, and live
external corroboration as RED ("not proven / external dependency") rather
than omitting them. `docs/RWC_V2_BLOCKER_REASSESSMENT.md` marks 0 of 8
blockers CLOSED.

---

## Verification commands run for this audit

```
grep -rn "time.Now\|math/rand\|crypto/rand\|uuid" pkg/rwc cmd/veriqo-rwc-v2   # no executable hits
grep -n "CaseID ==\|c\.ID ==\|\"RWC-001-[A-E]\"" pkg/rwc/*.go cmd/veriqo-rwc-v2/*.go   # (excl. _test.go) no hits
go vet ./pkg/rwc/... ./cmd/veriqo-rwc-v2/...     # clean
go test ./pkg/rwc/... -race -v                    # 20/20 pass
go run ./cmd/veriqo-rwc-v2  (run twice)            # identical hashes both times
./scripts/verify.sh                                # ALL CHECKS PASSED, 123 packages
```

No item in the brief's §19 checklist was found unaddressed. Where a
design choice was made rather than avoided (violation-ratio bands, policy
thresholds, PatternScore/PriceAnomaly dual-feed), it is documented at its
source rather than hidden, per §10 above.
