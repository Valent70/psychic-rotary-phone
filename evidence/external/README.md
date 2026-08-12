# External qualification evidence

This directory is where real, independently-produced evidence against
one of the eight `BLOCKED_EXTERNAL` gates gets submitted — a signed
report from an actual penetration-test vendor, a benchmark artifact
from a real 100-node cluster, a DR drill log with measured RPO/RTO, and
so on. See `pkg/governance/qualification` for the lifecycle this
evidence is put through (`BLOCKED_EXTERNAL → EVIDENCE_SUBMITTED →
EVIDENCE_VALIDATED → QUALIFIED → VERIFIED`) and
`cmd/veriqo-readiness/main.go`'s `loadExternalQualifications` for how a
file placed here is picked up.

**Nothing in this directory is checked in by default, and nothing here
is fabricated by any tool in this repository.** An empty directory
means every one of the eight gates is exactly as blocked as it has
always honestly been. A file only belongs here once the real-world
event it describes has actually happened.

## Format

One file per gate: `<gate_id>.json`, matching
`qualification.ExternalEvidence`:

```json
{
  "gate_id": "pentest",
  "provider": "<the vendor or lab that actually produced this>",
  "report_id": "<their reference for the report/run>",
  "scope": "<what was actually covered>",
  "environment": "<where it actually ran — never this sandbox>",
  "commit": "<the git commit the evidence was produced against>",
  "measurement": {"critical_findings": "0", "high_findings": "1"},
  "start_tick": 0,
  "end_tick": 0,
  "reviewer": "<the internal reviewer who accepted this external evidence>",
  "signature": "<a real signature over the artifact, not a placeholder>",
  "expires_at_tick": 0,
  "artifact_hash": "<computed by qualification.NewEvidence — do not hand-write this>"
}
```

`Validate` independently recomputes `artifact_hash` from every other
field and refuses a mismatch (`ErrArtifactHashInvalid`), refuses a
submission with no `reviewer` or `signature`, and refuses evidence
whose `expires_at_tick` has already passed. There is no field a
submitter can set that causes a gate to advance without also passing
that check — including this readiness engine's own submission of the
gate ID and blocker text, which come from the hard-coded, honest
`blocked` table in `cmd/veriqo-readiness/main.go`, not from this file.
