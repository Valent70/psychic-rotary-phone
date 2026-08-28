# VERIQO External Source Trust Model

Status: current as of the "Final Audit result" integration round (R25).
Governing code: `pkg/evidence/provenance`, `pkg/connector`, `pkg/moat/contradiction`.

## 1. The problem this document closes

Before this round, an external data source (AIS, GDELT, a public dashboard, an
open-source dataset) that was merely *reachable* from VERIQO's network had no
structural barrier stopping its data from being treated as trustworthy
evidence. Reachability and trustworthiness are different questions, and
conflating them is exactly the failure mode a Palantir/Bloomberg-grade
external auditor would flag first. This document names the model that now
keeps the two questions structurally separate, and the code that enforces it.

## 2. Two independent axes, never conflated

Every piece of external evidence carries two independent classifications
(`pkg/evidence/provenance.ExternalEvidence`):

- **`OriginClass`** — how far the evidence is from a verified, rights-cleared,
  third-party-attested external source: `SYNTHETIC` → `REPLAY` →
  `REAL_DERIVED_BENCHMARK` → `REAL_EXTERNAL_UNVERIFIED` →
  `REAL_EXTERNAL_AUTHORIZED` → `QUALIFIED_EXTERNAL_EVIDENCE` →
  `VERIFIED_EXTERNAL_EVIDENCE`.
- **`RightsState`** — what VERIQO is *legally permitted* to do with it right
  now, entirely independent of how real or reachable it is:
  `UNKNOWN_PENDING_CONTRACT`, `INTERNAL_ONLY`, `AUTHORIZED_PILOT`,
  `CUSTOMER_FACING_ALLOWED`, `DISPUTE_USE_ALLOWED`, `CALIBRATION_ALLOWED`,
  `TRAINING_ALLOWED`, `REVOKED`, `EXPIRED`.

A source being real and reachable (`OriginRealExternalUnverified`) says
nothing about what VERIQO may do with it — that is `RightsState`'s job alone,
and by default it is `UNKNOWN_PENDING_CONTRACT` until a real contract says
otherwise. `ExternalEvidence.Permits(use)` is the single fail-closed gate
every consumer must call before acting on external evidence; it denies every
`Use` a `RightsState` does not explicitly enumerate, with no default-allow
path anywhere in the implementation.

## 3. Six kinds of external party, never all trusted the same way

`pkg/evidence/provenance.Registry` distinguishes six roles
(`EntityKind`): `SOURCE`, `PROVIDER`, `DATASET`, `EVIDENCE_PROVIDER`,
`ATTESTER`, `REVIEWER`. Registering an entity under any of these kinds
**never** grants trust — `Registry.Register` unconditionally sets
`TrustGranted = false` regardless of what a caller supplies. Trust can only
be granted through a separate, attributable `Registry.GrantTrust` call that
records a policy reference, an actor, and a tick; for `EVIDENCE_PROVIDER`
specifically, an attestation reference is additionally mandatory — a
policy-only grant is refused. This means AISstream, World Monitor, GDELT,
public dashboards, open-source projects, and government websites can all be
registered exactly like any other source, and every one of them stays
untrusted after registration until a real, attributable grant says
otherwise (proven by `TestNamedNeverAutoTrustSourcesStayUntrustedUntilExplicitGrant`
and `TestRegisterNeverAutoGrantsTrustRegardlessOfCallerInput`).

## 4. Pluggable, not hard-wired

`pkg/connector.ExternalEventSource` and `pkg/connector.RiskEnrichmentSource`
are the two interfaces a GDELT, World Monitor, or other OSINT adapter would
implement. VERIQO's core imports neither concrete provider — no adapter ships
in this repository beyond the interfaces and the AIS connector named below.
Every event or signal these interfaces produce carries a
`provenance.ExternalEvidence` envelope, so a future concrete adapter cannot
make its output more trusted than the envelope's own fail-closed rules allow,
regardless of how the adapter is implemented.

## 5. The one concrete connector this round ships: AIS

`pkg/connector/aisstream` is a real, isolated AISstream-compatible WebSocket
connector (transport injected behind a small interface so tests never touch
a real network). Every message it processes is wrapped in a
`provenance.ExternalEvidence` with `OriginClass` and `RightsState` hard-set to
`REAL_EXTERNAL_UNVERIFIED` / `UNKNOWN_PENDING_CONTRACT` — there is no
configuration flag or code path in this package that can produce anything
more permissive; upgrading a source's rights is exclusively
`pkg/evidence/provenance.Registry.GrantTrust`'s job, performed by an operator
against a real contract, never by the connector itself.

## 6. External evidence never overrides internal ground truth

`pkg/moat/contradiction.ArbitrationEngine.ConflictRecords` is where AIS (or
any external source) meets VERIQO's own operational records (NOR, SOF,
port-call, customer records) in arbitration. Every source that reports a
value for a claim is preserved in the resulting `ConflictRecord` — winning or
losing — and a genuine disagreement is reported as `CONTESTED` with
`HumanReviewRequired = true`, never silently resolved. See
`docs/qualification/VERIQO_EXTERNAL_CLOSURE_MATRIX.md` §5 for the worked
AIS/NOR/SOF example this mechanism is built to handle.

## 7. What this model does not claim

This document describes a real, tested trust *mechanism*. It makes no claim
that any concrete external source currently holds a `RightsState` beyond
`UNKNOWN_PENDING_CONTRACT`/`INTERNAL_ONLY` in production — that is a business
and legal decision, tracked in
`docs/data/VERIQO_EXTERNAL_DATA_RIGHTS_REGISTER.md`, not an engineering one.
