# External qualification evidence

This directory is where real, independently-produced evidence against
one of the eight `BLOCKED_EXTERNAL` gates gets submitted — a signed
report from an actual penetration-test vendor, a benchmark artifact
from a real 100-node cluster, a DR drill log with measured RPO/RTO,
and so on. See `pkg/governance/qualification` for the lifecycle this
evidence is put through (`BLOCKED_EXTERNAL → EVIDENCE_SUBMITTED →
EVIDENCE_VALIDATED → QUALIFIED → VERIFIED`), `docs/governance/
EXTERNAL_QUALIFICATION_POLICY.md` for the cryptographic trust model,
and `cmd/veriqo-readiness/main.go`'s `loadExternalQualifications` for
how a file placed here is picked up.

**Nothing in this directory is checked in by default, and nothing here
is fabricated by any tool in this repository.** An empty directory
means every one of the eight gates is exactly as blocked as it has
always honestly been. A file only belongs here once the real-world
event it describes has actually happened — and even then it validates
only if the provider and reviewer who signed it are real, registered
trust anchors (see below); a well-formed-but-unregistered submission
is rejected just as loudly as a missing one.

## Format

One file per gate: `<gate_id>.json`, matching
`qualification.ExternalEvidence`:

```json
{
  "gate_id": "pentest",
  "provider_id": "<a provider_id already registered in docs/governance/TRUSTED_EVIDENCE_PROVIDERS.json>",
  "reviewer_id": "<a reviewer_id already registered in docs/governance/TRUSTED_EVIDENCE_REVIEWERS.json>",
  "report_id": "<the provider's own reference for the report/run>",
  "scope": "<what was actually covered>",
  "environment": "<where it actually ran — never this sandbox>",
  "commit": "<the exact git commit of the release this evidence qualifies>",
  "source_hash": "<that release's internal/sourcehash root hash — see READINESS_MANIFEST.json>",
  "measurement": {"critical_findings": "0", "high_findings": "1"},
  "start_tick": 0,
  "end_tick": 0,
  "expires_at_tick": 0,
  "artifact_hash": "<computed by qualification.NewEvidence — do not hand-write this>",
  "provider_signature": "<hex Ed25519 signature by the provider's REAL private key over artifact_hash>",
  "reviewer_signature": "<hex Ed25519 signature by the reviewer's REAL private key over artifact_hash>"
}
```

`Validate` independently recomputes `artifact_hash` from every other
field and refuses a mismatch; resolves `provider_id`/`reviewer_id`
against the trust registry and refuses anything unregistered, expired,
revoked, or (for providers) not authorized for this specific gate;
verifies both signatures against the *registered* public keys, not
whatever key produced them; and refuses evidence whose `commit` or
`source_hash` does not match the release actually being assessed.
There is no field a submitter can set that causes a gate to advance
without every one of those checks passing — including this readiness
engine's own submission of the gate ID and blocker text, which come
from the hard-coded, honest `blocked` table in
`cmd/veriqo-readiness/main.go`, not from this file.
