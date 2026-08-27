# External Qualification Trust Model

Closes the V7.12.1 Palantir-style audit's Layer A ("fix the external
qualification system before closing the eight blockers") and P0-03 of
the V7.12.0 integrated audit. Implementation: `pkg/governance/qualification`.

## Why this exists

A blocked gate (`pentest`, `scale_qualification`, `multi_region_dr`,
`hsm_kms`, `live_data`, `soak_72h`, `spire_mtls`, `supply_chain_scan`)
must never become green because a JSON file merely *claims* a reviewer
name and a signature string. The prior revision of this package
checked `Reviewer != ""` and `Signature != ""` — sufficient to catch a
blank field, insufficient to catch a forged one. This revision adds a
real trust model:

- **Registered identity, not free text.** A `Provider` and a
  `Reviewer` are entries in `TrustRegistry`, each with an Ed25519
  public key, a validity window, and (for providers) the specific
  gates they're authorized to attest to. `docs/governance/
  TRUSTED_EVIDENCE_PROVIDERS.json` and `TRUSTED_EVIDENCE_REVIEWERS.json`
  are the committed, **public-key-only** (never a secret) trust
  anchors `cmd/veriqo-readiness` loads at startup. Both ship empty —
  nothing validates until a human operator registers a real provider
  or reviewer's real public key.
- **Cryptographic signatures, independently verified.** Every
  submission carries `ProviderSignature` and `ReviewerSignature`, each
  checked with `ed25519.Verify` against the *registered* public key —
  never the submission's own claim about who signed it.
- **Release binding.** Every submission carries `Commit` and
  `SourceHash`; `Validate` rejects any mismatch against the release
  actually being assessed. Evidence signed against one commit cannot
  qualify a different one, closing the exact defect this same audit
  round found in `SBOM.json` (a stale, wrong release identity signed
  into a certificate) — see `internal/sbom`.
- **Revocation is live, not just at submission time.** `VerifyGate`
  and `ExpireStale` re-check that the provider and reviewer credentials
  backing an already-QUALIFIED gate have not since been revoked, so a
  qualification cannot outlive the trust it was built on.

## What this does not do

It does not, and cannot, close any of the eight blockers. No code run
in this repository can conjure a real penetration-test vendor, a
physical 100-node cluster, or a production HSM/KMS tenancy — see the
closure report for what real-world action each one actually requires.
`docs/governance/external-evidence-policy.json` documents the intended
per-gate policy (currently matching, but not yet separately loaded or
enforced by, the maximal check set `pkg/governance/qualification`
already hardcodes identically for all eight gates).

## Adversarial coverage

`pkg/governance/qualification/qualification_test.go` runs the "no
false green" campaign this trust model must survive: tampered
artifact hash, forged signature, wrong public key, unknown/revoked
provider, unknown/revoked reviewer, provider not authorized for the
gate, wrong release commit, wrong source hash, expired evidence,
evidence replayed onto a different gate, and evidence from a prior
release. Every one is rejected.
