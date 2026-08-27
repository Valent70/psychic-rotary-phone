# Canonical Readiness Taxonomy

Closes the Round 4 work order's §2 "CRITICAL CORRECTION — READINESS
COUNT MUST BE NORMALIZED" requirement: **one** readiness vocabulary,
used verbatim everywhere a gap is reported, so that "no gap may appear
under two different names" and "the final readiness manifest must have
one source of truth" are structural facts rather than editorial
promises.

## Where it lives

`internal/assurance/temporary_readiness.go` (`CanonicalStatus`, in
`internal/assurance/axes.go`) is the **only** place this vocabulary is
computed. Every other artifact — `READINESS_MANIFEST.json`'s
`axes.gates[].canonical_status`, `docs/governance/
EXTERNAL_DEPENDENCY_REGISTER.json`, this program's PDF reports — is a
read-only projection of that one computation, never a second place that
assigns a status by hand.

## The four values

| Value | Meaning | Derivation |
|---|---|---|
| `VERIFIED_INTERNAL` | The gate's `FINAL` axis is `READY`: either it has no external dependency at all, or a real `EXTERNAL_QUALIFIED` submission (via `pkg/governance/qualification`) closed it. | `Gate.Axes().Final == AxisReady` |
| `READY_FOR_EXTERNAL_QUALIFICATION` | `ENGINEERING` passes and `INTERNAL` is `INTERNAL_QUALIFIED` (a real in-sandbox qualification drill ran and passed), but `FINAL` is still `BLOCKED_EXTERNAL`. Every internally-closeable half of the work is done; only the external act itself (a vendor, a purchase order, a physical host, an independent auditor) is missing. | `Final == AxisBlockedExternal && Internal == AxisInternalQualified` |
| `BLOCKED_EXTERNAL` | `FINAL` is `BLOCKED_EXTERNAL` and no internal qualification drill could even be attempted (`INTERNAL` is `NOT_RUN` or `NOT_APPLICABLE`) — the external dependency is itself a precondition for running any drill (you cannot internally qualify against a real HSM without an HSM; you cannot admit a live data feed without a live data contract; you cannot run a penetration test without an independent pentest firm). | `Final == AxisBlockedExternal && Internal != AxisInternalQualified` |
| `NOT_READY` | `FINAL` is `NOT_READY` (a real engineering failure, no external excuse available) or `WAIVED` (a waiver is a visibly weaker verdict, never a readiness synonym). | `Final == AxisNotReady \|\| Final == AxisWaived` |

The derivation is `internal/assurance/axes.go`'s `canonicalStatus`
function — a pure, unconditionally-total switch over the four-axis
report `Gate.Axes()` already computes from real attached evidence.
There is no fifth branch and no default that reads "assume ready."

## Current state (this release)

As of this round's `READINESS_MANIFEST.json` (62 registered gates, one
real full non-`-skip-race` run merged with a `-skip-race` run for the
remaining 61 — see that file's own `generated_from` field for the exact
provenance of every gate):

- **52 `VERIFIED_INTERNAL`** — every P0 core-kernel, insurance-domain,
  identity, evidence, replay, dossier, observability and
  release-governance gate whose own command exits 0 with no external
  dependency.
- **5 `READY_FOR_EXTERNAL_QUALIFICATION`** — `multi_region_dr`,
  `scale_qualification`, `soak_72h`, `spire_mtls`, `supply_chain_scan`:
  each has a real, passing engineering harness AND a real in-sandbox
  qualification drill (multi-container DR failover, a 100-container
  1,000,000-envelope scale run, a 60-minute soak, a live SPIRE mTLS
  handshake, a clean `staticcheck`/`gosec` pass) — see each gate's own
  `evidence/*.json`/`.txt` artifact for the raw run output.
- **3 `BLOCKED_EXTERNAL`** — `pentest`, `hsm_kms`, `live_data`: each
  genuinely cannot run even an internal drill without the external
  dependency itself (an independent pentest firm; a real HSM/KMS
  tenancy — this round re-tried this environment's placeholder AWS
  credentials against a real `DescribeKey` call and AWS itself rejected
  them, confirming procurement-blocked rather than engineering-blocked;
  a commercial market data contract).
- **0 `NOT_READY`** — no gate fails on its own engineering merits.

52 + 5 + 3 + 0 = 60 core/insurance gates, plus the two Round 4 additions
(`dossier_verification`, `case_room_access_control`), both
`VERIFIED_INTERNAL` — 62 total, every one landing in exactly one
bucket.

## The twelve-category composition

`internal/assurance/temporary_readiness.go`'s `ComposeTemporaryReadiness`
groups every gate into exactly one of twelve named categories
(`CORE_KERNEL`, `EVIDENCE`, `IDENTITY`, `SECURITY`, `INSURANCE`,
`REAL_WORLD_NETWORK`, `REPLAY`, `DOSSIER`, `CASE_ROOM`, `OBSERVABILITY`,
`OPERATIONS`, `RELEASE_GOVERNANCE` — the exact twelve the work order
names) via a fixed, one-gate-one-category lookup table (`gateCategory`).
A category's composed status is the **worst** (least-ready) status
among its own gates — the same "one weak link overrides the rest" rule
`Gate.Axes()` already applies to a single gate's own FINAL axis, lifted
one level up. A category with **zero** registered gates composes to
`NOT_READY`, never to a default green — an empty category is a real gap
in coverage, not evidence of readiness.

`ComposeTemporaryReadiness` refuses to silently drop an unrecognised
gate ID: it is collected into `UnmappedGates` and surfaced in the
report rather than ignored, so a newly-registered gate that nobody
categorized is a visible finding, not a silent omission.

## The final verdict

`TemporaryReadinessVerdict` is a two-value closed type:

- `TEMPORARY_PRODUCTION_READINESS_CANDIDATE` — every gate lands in
  `VERIFIED_INTERNAL`, `READY_FOR_EXTERNAL_QUALIFICATION` or
  `BLOCKED_EXTERNAL`; none is `NOT_READY`. This is the work order's own
  required honest ceiling and the only positive claim this program
  makes.
- `NOT_YET_TEMPORARY_CANDIDATE` — at least one gate (or one entirely
  unregistered category) is `NOT_READY`.

The type structurally cannot express `PRODUCTION_QUALIFIED` or a
`"N/N VERIFIED"` claim — there is no third constant, and nothing in
`cmd/veriqo-readiness` or the PDF-report tooling constructs a
`TemporaryReadinessVerdict` value outside this file. Every artifact
that reports overall readiness must use this exact phrase, or must be
treated as out of compliance with this taxonomy.

Wired into `READINESS_MANIFEST.json` as the top-level
`temporary_production_readiness` key, computed fresh on every manifest
build (`internal/assurance/manifest.go`'s `BuildReadinessManifest`) from
the same `Axes()` call the pre-existing `axes` key already used —
additive only: it changes no `Gate.Status`, no `Assessment`, no release
`Verdict`, exactly like `axes` itself.
