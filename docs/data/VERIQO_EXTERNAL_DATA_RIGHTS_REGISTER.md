# VERIQO External Data Rights Register

Status: current as of the "Final Audit result" integration round (R25).
This is the human-readable companion to
`pkg/evidence/provenance.Registry` — a live register belongs in that
code-backed store; this document is the currently-known state, for
readers who cannot query the running system directly.

The non-negotiable rule this register exists to enforce, verbatim from the
governing audit: never convert "real data" into "authorized evidence", never
convert "publicly accessible" into "commercially reusable", never convert
"open source" into "unrestricted data rights".

## 1. How to read this table

- **Origin class** and **Rights state** use the exact vocabulary defined in
  `pkg/evidence/provenance` (see
  `docs/architecture/VERIQO_EXTERNAL_SOURCE_TRUST_MODEL.md`).
- **Trust granted?** reflects `pkg/evidence/provenance.Registry` state: no
  source in this table has ever had `GrantTrust` called against it as of
  this round. This is a fact, not an oversight — closing it requires a real
  commercial or legal decision outside engineering's authority.

## 2. Register

| Source | Provider | Origin class | Rights state | Trust granted? | Notes |
|---|---|---|---|---|---|
| AIS (AISstream-compatible feed) | aisstream.io (illustrative; not contracted) | `REAL_EXTERNAL_UNVERIFIED` (once connected) | `UNKNOWN_PENDING_CONTRACT` | No | `pkg/connector/aisstream` is real, tested code; it has never connected to a real aisstream.io endpoint in this sandbox (no network egress). No commercial data agreement exists. |
| GDELT | The GDELT Project | Not yet connected | `UNKNOWN_PENDING_CONTRACT` | No | Only the pluggable `ExternalEventSource` interface exists (`pkg/connector`); no concrete GDELT client ships in this repository, per design (PART 6: no hard dependency). GDELT's own terms are public/open — "open" is explicitly NOT "unrestricted data rights" per the audit's own non-negotiable rule; a real integration must review GDELT's terms before any rights upgrade. |
| World Monitor | (unspecified vendor) | Not yet connected | `UNKNOWN_PENDING_CONTRACT` | No | Same posture as GDELT — interface only, `RiskEnrichmentSource`, no concrete client. |
| Public government dashboards | (varies by jurisdiction) | Not applicable — no connector exists | `UNKNOWN_PENDING_CONTRACT` | No | Explicitly named in the audit as a source that must never auto-become an authority evidence source. No code in this repository treats a public dashboard as an evidence provider. |
| NOR / SOF / port-call / customer operational records | VERIQO's own operational ingestion | `REAL_EXTERNAL_AUTHORIZED` or internal, depending on deployment contract | Deployment-specific (`INTERNAL_ONLY` at minimum) | Deployment-specific | These are the operator's own contracted business records, not third-party OSINT — their rights status is a deployment-time configuration this document does not universally assert. |
| SPIRE / SPIFFE identity infrastructure | Self-hosted (this org's own SPIRE deployment) | N/A — infrastructure, not evidence | N/A | N/A | Not an evidence source; included here only to state explicitly that it is out of scope for this register. |
| Vulnerability databases (vuln.go.dev / osv.dev / GitHub Advisory) | Google / OSV / GitHub | `REAL_EXTERNAL_UNVERIFIED` when reachable | Public data, `INTERNAL_ONLY` use (SAST/dependency scanning) | No formal grant recorded | These returned 403 under this sandbox's network policy across every session this round's evidence covers (`evidence/supply_chain_scan-*.txt`); `pkg/blockers/supplychain`'s offline `VulnerabilityDatabase` fixture is a separate, explicitly-labeled offline snapshot, not a live feed. |

## 3. What "Trust granted? No" means operationally

Every `RightsState = UNKNOWN_PENDING_CONTRACT` row above is enforced, not
just documented: `provenance.ExternalEvidence.Permits` denies every
`Use` except `UseInternalOnly` for that state (see
`TestUnknownPendingContractPermitsNothingButInternalUse`), and
`pkg/evidence/provenance.Registry.IsTrustedEvidenceProvider` returns `false`
for every entity in this table because none has received a `GrantTrust` call.
Any code path that treats one of these sources as customer-facing,
dispute-usable, calibration-usable, or training-usable evidence today would
be a real bug — none currently exists, verified by this round's test suite
(`pkg/evidence/provenance`, `pkg/connector`).

## 4. Updating this register

When a real commercial or legal agreement is signed for any source above:

1. Call `pkg/evidence/provenance.Registry.GrantTrust` with the real policy
   reference and (for an `EVIDENCE_PROVIDER`) a real attestation reference.
2. Update this table's Rights state / Trust granted columns to match.
3. Update `docs/qualification/VERIQO_EXTERNAL_CLOSURE_MATRIX.md`'s
   `live_data` row's `data_status` dimension accordingly.

No engineering change alone can update this register — every row change
above requires a real external event (a signed contract, a legal review)
this repository cannot produce on its own.
