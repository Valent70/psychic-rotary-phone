# Security Qualification Pack

Consolidates, for an operator or a real external security reviewer, the
`SECURITY` category's four gates (`security_unit`, `sandbox`, `pentest`,
`hsm_kms`, `spire_mtls`, `supply_chain_scan`) into one document — what
is genuinely closed, what is genuinely blocked, and exactly what real
external act each blocker still needs. Every figure below is read
directly from `evidence/*.json`/`.txt` and `READINESS_MANIFEST.json`;
nothing here is asserted independently of those artifacts.

## VERIFIED_INTERNAL (no external dependency)

| Gate | What it proves |
|---|---|
| `security_unit` | Key lifecycle and authorization invariants (`pkg/platform/security/keys`, `pkg/authz`) pass under `go test`. |
| `sandbox` | The plugin sandbox (`pkg/kernel/sandbox`) fails closed and never silently falls back to an unrestricted execution path. |

## READY_FOR_EXTERNAL_QUALIFICATION (engineering + internal drill both real; only the external act is missing)

| Gate | Internal drill performed | What remains |
|---|---|---|
| `spire_mtls` | A real multi-container SPIRE cluster (one `spire-server`, two independently-attested `spire-agent` containers on a real Docker bridge network) issued real X.509-SVIDs that `pkg/transport/rafttcp`'s `FileCertSource`/`NewServerFromSource`/`NewClientFromSource` genuinely loaded for a real end-to-end mTLS handshake and RPC. Node-scoped workload identity isolation and live per-node revocation were both proven across the cluster. | A production node attestor (cloud-instance-identity, k8s-PSAT, or TPM — the current `join_token` attestor is a test/demo mechanism, not a production one) — a real cloud/orchestration environment to attest against, not code. |
| `supply_chain_scan` | `staticcheck` and `gosec` both run clean (0 findings each) in a network-enabled session; all 89 gosec findings across every prior severity were individually triaged, fixed, or justified with a named reason. `pkg/blockers/supplychain` has a real `go list -m all` dependency-discovery step, a policy engine, and a real HTTP-backed `VulnerabilityProvider`. | `govulncheck`'s feed (`vuln.go.dev`), `osv.dev`, and the GitHub advisory API all return `403` under this sandbox's network policy — re-verified this round by directly re-probing all three hosts and the proxy's own status endpoint, which independently logs the same policy denials. SAST is fully closed; dependency-vulnerability *database* scanning needs a network-permitted environment, not more code. |

(`multi_region_dr` and `scale_qualification`, the other two
`READY_FOR_EXTERNAL_QUALIFICATION` gates, are `OPERATIONS`-category —
see `PRODUCTION_DEPLOYMENT_AND_OPERATIONS_GUIDE.md`.)

## BLOCKED_EXTERNAL (no internal drill possible without the dependency itself)

| Gate | Why no drill is possible | What the external act is |
|---|---|---|
| `pentest` | `pkg/blockers/pentest` runs a real target registry, a real release-identity preflight, and adversarial probes (JWT `alg=none`, unknown `kid`, sandbox path traversal, authz wildcard escalation) directly against this codebase's own production `pkg/api`/`pkg/kernel/sandbox`/`pkg/authz` — but a genuine independent penetration test, by definition, requires an independent third party; this codebase testing itself is not that. | Engage a real, independent security vendor; feed their signed report through `pkg/governance/qualification`. |
| `hsm_kms` | `pkg/platform/security/keys` has a `SoftwareBacked` production guard (`RequireProductionSafe` refuses any software-backed key provider under `env=production`) and `pkg/blockers/hsmkms` proves every failure mode (unavailable, timeout, permission-denied, wrong-key, revoked) fails closed — but qualifying against a *real* HSM/KMS needs a real HSM/KMS. This round re-tested rather than assumed: this environment's placeholder `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` were tried against a real AWS KMS `DescribeKey` call, and AWS itself rejected them (`UnrecognizedClientException`) — confirmed procurement-blocked, not engineering-blocked. | Provision a real HSM/KMS tenancy (AWS KMS, a real HSM, or equivalent) and real credentials. |

`live_data` (commercial market data contracts) is `BLOCKED_EXTERNAL` for
the same structural reason but belongs to the `REAL_WORLD_NETWORK`
category — see `REAL_WORLD_INSURANCE_NETWORK_REPORT.md`.

## What a reviewer should NOT conclude

- A `READY_FOR_EXTERNAL_QUALIFICATION` gate is **not** equivalent to
  "qualified" or "verified" — see `CANONICAL_READINESS_TAXONOMY.md`. It
  means the engineering and the internal drill are both real and
  passing; the external act itself has not happened.
- No gate in this pack has ever been marked `VERIFIED` or
  `EXTERNAL_QUALIFIED` by a simulator, a mock, or a self-attestation.
  `pkg/governance/qualification`'s trust model (registered Ed25519
  identities, release-bound signatures, live revocation checking — see
  `EXTERNAL_QUALIFICATION_POLICY.md`) is the only path from
  `BLOCKED_EXTERNAL`/`READY_FOR_EXTERNAL_QUALIFICATION` to
  `VERIFIED_INTERNAL`, and nothing has been submitted through it this
  round.

## Recommended qualification order

1. `supply_chain_scan` — cheapest to close (a network-permitted CI run
   against the real vulnerability feeds; the pipeline is already built
   and unit-tested against a local mock server).
2. `spire_mtls` — needs a real production node attestor in a real
   cloud/orchestration target; the mTLS handshake itself is already
   proven end-to-end.
3. `hsm_kms` — needs a procured HSM/KMS tenancy; the fail-closed guard
   and every failure mode are already proven.
4. `pentest` — needs a contracted, independent security vendor.
