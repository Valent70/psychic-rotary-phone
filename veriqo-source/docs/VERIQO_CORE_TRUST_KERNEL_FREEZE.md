# VERIQO -- Core Trust Kernel FREEZE for Commercialization

Response to `Tubtaskan_semua_secara_details.docx` item 1 (PRIORITAS
UTAMA -- FREEZE CORE). Effective as of commit `aa010ed` on branch
`claude/l99-gap-coverage-nv70zy`, the packages listed below are
**FROZEN FOR COMMERCIALIZATION**. This is a declared engineering
policy for the remainder of the Commercialization Sprint, not a Go
language mechanism -- there is no compiler enforcement, only this
document and the discipline of every round that follows it.

## What "frozen" means

**Allowed on frozen packages:**
- Bug fixes (an incorrect result for a documented, intended input)
- Security fixes (a genuine vulnerability, not a hardening preference)
- Integration fixes (a wiring/adapter problem where a frozen package's
  own real, existing API is being called incorrectly by new
  commercial-layer code -- e.g. the DecideClaim/AuthorizeExecution
  wiring already completed in the prior round)
- Test hardening (more coverage of EXISTING behavior; never a new
  invariant that changes behavior)
- API stabilization (freezing wire-format compatibility, deprecation
  notices; never breaking an existing exported signature without a
  compelling security/correctness reason)
- Documentation

**NOT allowed on frozen packages:**
- Architecture redesign for elegance, DX, or "cleaner code" alone
- New abstractions with no concrete commercial-layer caller
- Changing a sealed type's field set, hash input, or verification
  algorithm (this would silently invalidate every already-anchored
  Decision/ActionAuthorization/manifest hash in any deployment)
- Adding new exported mutation surfaces to any sealed type

## Frozen package list

| Area (reviewer's own naming) | Package(s) |
|---|---|
| Decision Trust Boundary | `pkg/insurance/decision` |
| ActionAuthorization | `pkg/insurance/action` |
| Insurance Decision integration | `pkg/insurance/api` (`Facade.DecideClaim` and everything it wires), `pkg/insurance/claimworkflow` |
| Workflow DAG | `pkg/workflow` |
| Trust Calculus | `pkg/moat/decision`, `pkg/moat/intelligence/risk`, `pkg/trust`, `pkg/trust/state` |
| Evidence foundations | `pkg/evidence/manifest`, `pkg/insurance/evidence`, `pkg/insurance/causation`, `pkg/insurance/cre`, `pkg/insurance/finding` |
| Knowledge foundations | `pkg/inference`, `pkg/governance/lifecycle` |
| Intelligence foundations | `pkg/moat/intelligence/*` |
| DARI guarantees (Deterministic, Auditable, Replayable, Immutable) | `pkg/platform/audit` (Merkle/checkpoint/anchor), `pkg/canonical/jcs` (canonicalization) |
| Deterministic execution | `pkg/execution`, `pkg/lifecycle` |
| Ledger primitives | `pkg/platform/audit` |
| Existing authorization controls | `pkg/platform/security`, `pkg/authz` |

Everything the Commercialization Sprint builds from this point forward
is **additive**: new packages under a `pkg/commercial/` tree, new
`cmd/` binaries, and new, separately-versioned HTTP routes -- calling
into the frozen list above through its existing, real, tested public
API, never reaching into its internals or duplicating its logic. This
mirrors the exact "additive, non-disruptive" precedent this
engagement's own gateway routes (`/lifecycle/run_unified`,
`/insurance/decide`) already established.

## Why this matters now

The reviewer's own framing: VERIQO does not need to be "perfect" to
find a pilot customer. Every round up to this one hardened the trust
kernel's own internal correctness. The Commercialization Sprint's job
is to make that kernel externally reachable, demonstrable, and
defensible -- not to keep making it more internally elegant. A frozen
kernel with an honestly-scoped commercial surface beats an
ever-expanding kernel with no external interface at all.
