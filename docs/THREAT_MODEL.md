# Veriqo Kernel — Threat Model

Honest scope statement up front: this document describes the threat
model Veriqo's architecture is DESIGNED for, and states plainly which
controls are implemented/tested in this repository versus which are
documented as the production path but not runnable in this offline
sandbox (no access to SPIRE, OPA, or any external registry/network
beyond a small allowlist — confirmed every session of this project).

## Actors

| Actor | Description | Trust level |
|---|---|---|
| Internal operator | Analyst/ops user querying risk assessments, decision outputs, twin state | Authenticated, scoped by role (target) |
| Cluster peer node | Another `raftlite` member in the same Veriqo cluster | Mutually authenticated via mTLS (`pkg/transport/rafttcp`) |
| External data provider | AIS vendor, SAR imagery vendor, sanctions list, port authority feed | Untrusted content, trusted-but-verified identity (`fusion.SourceProfile`) |
| Compromised provider | A legitimate, authenticated source feeding false/manipulated evidence | Explicitly modeled — this is what `pkg/moat/contradiction` and `pkg/moat/fusion/hbayes` exist to detect and bound, not prevent at the transport layer |
| External attacker | Unauthenticated network attacker | Untrusted, no access assumed by default |

## Assets

- **Sanction/compliance data** (`pkg/moat/domain/maritime`, sanction
  entries, ownership chains) — regulatory-sensitive; incorrect handling
  has legal exposure.
- **Vessel/entity risk profiles** (`pkg/moat/digitaltwin`) — commercial
  and reputational sensitivity if leaked or tampered.
- **Evidence provenance and arbitration history** (`pkg/moat/fusion`,
  `pkg/moat/contradiction`) — the auditability of "why did we decide
  this" is itself a protected asset; a tampered chain undermines every
  downstream decision's legal defensibility.
- **Cluster consensus state** (`pkg/consensus/raftlite`,
  `pkg/consensus/durability` where present) — availability and
  correctness of the trust substrate everything else is built on.

## Controls implemented and tested in this repository

- **Hash-chained, append-only logs** on every kernel layer (kg, fusion,
  contradiction, temporal, causal, decision, digitaltwin, audit) —
  `VerifyChain()`/`Rebuild()` detect tampering by re-deriving every hash
  from canonical bytes, not trusting stored fields. Proven by explicit
  tamper-injection tests in every package (e.g.
  `TestVerifyChainDetectsTamper`).
- **mTLS + SPIFFE-format identity** for cluster-to-cluster transport
  (`pkg/transport/rafttcp`) — self-hosted CA, app-level auth-ack fixing
  a real TLS 1.3 handshake-completion-vs-cert-acceptance gap found
  during this project's testing.
- **Atomic membership change** (`pkg/consensus/raftlite.ProposeJointConfChange`)
  — invariant-checked before and after application, preventing a
  malformed or partial membership change from ever being visible
  cluster-wide (see `docs/ARCHITECTURE.md`).
- **Correlation-aware evidence fusion** (`pkg/moat/fusion/hbayes`) — a
  compromised-or-colluding provider that resells the same feed through
  multiple "independent" source IDs cannot linearly inflate confidence;
  the hierarchical model discounts correlated confirmations (see
  `TestCorrelatedSourcesSaturateVsIndependentSources`).
- **Source arbitration ledger** (`pkg/moat/contradiction.SourceStandings`,
  `AdversarialPairs`) — surfaces sources that are structurally opposed
  to the consensus view across many claims, a concrete detection signal
  for a compromised or unreliable provider, for a human analyst to act
  on (this repository does not auto-blacklist a source; that policy
  decision is left to the operator by design).

## Controls documented but NOT executed in this sandbox

- **SPIRE / OPA runtime enforcement.** `pkg/platform/decision`-adjacent
  hooks (`docs/ARCHITECTURE.md`'s `PolicyEngine` interface pattern) are
  the intended integration seam; no SPIRE server or OPA sidecar has run
  against this code, because this sandbox has no network access to fetch
  or run them.
- **gosec / trufflehog / secret scanning in CI.** `.github/workflows/ci.yml`
  declares the jobs; they have not executed here (no outbound network to
  GitHub Actions runners' package sources beyond the small allowlist this
  sandbox has).
- **Formal authn/authz boundary for `cmd/veriqo-api`.** No HTTP/gRPC API
  binary exists yet in this repository (see `README.md` open-items
  table) — until it does, there is no network-facing authz boundary to
  audit beyond the cluster transport's mTLS.

## Data protection posture

- `pkg/platform/audit.AuditStore` stores what callers explicitly pass in
  — it does not currently enforce field-level redaction. A
  `pkg/platform/redact` module (masking IMO numbers, individual
  identities, etc. before they reach a log sink) is an explicit **open
  item**, not implemented in this session — flagging it honestly here
  rather than claiming a redaction capability that does not exist.
- No retention/TTL policy is enforced on any append-only log in this
  repository today; every log grows unbounded in memory for the process
  lifetime. This is acceptable for the reference-implementation/demo
  scope but is called out as a real production gap.
