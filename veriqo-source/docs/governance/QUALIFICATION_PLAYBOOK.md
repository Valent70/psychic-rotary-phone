# Qualification Playbook

An external-facing guide for the real-world parties who would need to
act before VERIQO can move from **Temporary Production Readiness
Candidate** to full production qualification: an insurer, a P&I club, a
reinsurer, a broker, a commodity trader, a surveying company, or an
enterprise customer's own security/compliance team.

## What "Temporary Production Readiness Candidate" means, precisely

See `CANONICAL_READINESS_TAXONOMY.md` for the full derivation. In one
sentence: **every internally-closeable piece of software, architecture,
control and qualification harness is complete and independently
verified; every remaining gap is a real, named, external dependency —
never an unexplained failure, never a simulated pass.** This is
deliberately NOT the same claim as "VERIQO Production Qualified" —
that claim requires the eight items below to actually close, and this
program's own governing rules forbid asserting it before they do.

## The eight external items, and who closes each one

| # | Item | Gate | Canonical status | Who acts | What they provide |
|---|---|---|---|---|---|
| 1 | Independent penetration test | `pentest` | `BLOCKED_EXTERNAL` | A contracted, independent security vendor | A signed report through `pkg/governance/qualification`, submitted by a registered `Reviewer` identity (`docs/governance/TRUSTED_EVIDENCE_REVIEWERS.json`) |
| 2 | 100-node / 1M-envelope physical scale run | `scale_qualification` | `READY_FOR_EXTERNAL_QUALIFICATION` | Infrastructure/platform team | Real distinct multi-host (ideally multi-datacenter) infrastructure — the literal node/envelope COUNT is already met on one physical host; distinct infrastructure is what remains |
| 3 | Multi-region DR drill | `multi_region_dr` | `READY_FOR_EXTERNAL_QUALIFICATION` | Infrastructure/platform team | Real cross-datacenter infrastructure (genuine WAN characteristics, real cloud regions, a real traffic-manager cutover) — the failover mechanics are already proven with real containers on one host |
| 4 | Production HSM/KMS tenancy | `hsm_kms` | `BLOCKED_EXTERNAL` | Security/infrastructure team | A procured HSM/KMS tenancy (AWS KMS or equivalent) and real credentials |
| 5 | Non-synthetic live data feeds | `live_data` | `BLOCKED_EXTERNAL` | Commercial/data-partnerships team | Real commercial data contracts (SWIFT/BoL/AIS/SAR or equivalent) — the ingestion pipeline, dedup and anti-replay defenses are already built and tested |
| 6 | 72-hour continuous soak | `soak_72h` | `READY_FOR_EXTERNAL_QUALIFICATION` | Infrastructure/platform team | A host that can stay up, unbroken, for 72 continuous hours — `VERIQO_SOAK_MINUTES=4320` against the existing, unmodified harness produces the qualifying evidence |
| 7 | Production SPIFFE/SPIRE deployment | `spire_mtls` | `READY_FOR_EXTERNAL_QUALIFICATION` | Security/infrastructure team | A production node attestor (cloud-instance-identity, k8s-PSAT, or TPM) in a real cloud/orchestration environment |
| 8 | Vulnerability database scanning in CI | `supply_chain_scan` | `READY_FOR_EXTERNAL_QUALIFICATION` | Platform/CI team | A network-permitted CI environment that can reach `vuln.go.dev`/`osv.dev` — SAST is already fully closed |

## How to submit evidence

`pkg/governance/qualification`'s trust model (see
`EXTERNAL_QUALIFICATION_POLICY.md`) is the **only** sanctioned path from
`BLOCKED_EXTERNAL`/`READY_FOR_EXTERNAL_QUALIFICATION` to
`VERIFIED_INTERNAL`:

1. Register the submitting `Provider`'s (or `Reviewer`'s) real Ed25519
   public key in the appropriate `docs/governance/TRUSTED_EVIDENCE_
   *.json` file — a deliberate, out-of-band, human act; nothing in this
   codebase can register a trust anchor for itself.
2. Sign the submission (`ProviderSignature`/`ReviewerSignature`) against
   the release's actual `Commit` and `SourceHash` — evidence signed
   against one commit cannot qualify a different one.
3. `VerifyGate` independently checks the signature against the
   registered key, and re-checks on every subsequent run that the
   credential has not since been revoked (`ExpireStale`) — a
   qualification cannot outlive the trust it was built on.

A simulator result, a mock, or a self-attestation is structurally
incapable of producing a `VERIFIED`/`EXTERNAL_QUALIFIED` status through
this path — there is no code path that accepts an unsigned or
unregistered submission.

## For a party evaluating VERIQO for their own use

If you are an insurer, broker, reinsurer, or enterprise customer
deciding whether to pilot VERIQO:

1. **Run the independent verifier yourself.**
   `cmd/veriqo-dossier-verify -case <your-case-or-a-golden-case>`
   recomputes every claim from raw inputs — you do not need to trust
   this program's own cached output. See
   `CASE_ROOM_AND_DOSSIER_VERIFIER_SPECIFICATION.md`.
2. **Run the readiness gate yourself.**
   `go run ./cmd/veriqo-readiness` reproduces the exact same manifest
   this document is generated from — read `temporary_production_
   readiness` directly rather than trusting a PDF summary.
3. **Read the `BLOCKED_EXTERNAL`/`READY_FOR_EXTERNAL_QUALIFICATION`
   rows as an honest disclosure, not a marketing gap.** A vendor that
   shows you exactly which eight things are not yet closed, and why,
   is giving you more actionable information than one that claims
   "100% verified" and cannot show you the evidence.
4. **Engage as one of the named external parties above** if you are
   positioned to close one of the eight items — a real pentest
   engagement, a real data contract, or a real infrastructure
   commitment moves the corresponding gate, and only that gate,
   forward through the qualification trust model.

## What this playbook deliberately does not promise

This playbook does not promise a timeline for closing the eight items —
each depends on a real external party's own schedule, budget, and
willingness to engage, none of which this program controls. It does not
promise that closing all eight yields "VERIQO Production Qualified"
automatically — that claim requires re-running the full readiness gate
after each closure and confirming the resulting verdict honestly, the
same discipline this entire round has applied to itself.
