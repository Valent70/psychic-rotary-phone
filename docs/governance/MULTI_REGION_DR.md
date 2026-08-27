# Multi-Region Disaster Recovery — Runbook

Status: **operational runbook**, written so a real drill can execute
against it the day real multi-region infrastructure exists. It is not
itself evidence that a drill happened — the `multi_region_dr` gate
stays `BLOCKED_EXTERNAL` until one actually runs, is measured, and is
submitted per `docs/governance/EXTERNAL_QUALIFICATION_POLICY.md`.

## 1. Target topology

```
Region A (primary)              Region B (secondary)
  VERIQO nodes (raft cluster)     VERIQO nodes (raft cluster)
  WAL + snapshots                 WAL + snapshots (replicated)
  Evidence store                  Evidence store (replicated)
  Trust anchor files (read-only,  Trust anchor files (identical copy —
    committed to the repo, so       these are public keys in git, so
    already identical in both       cross-region consistency is free)
    regions by construction)
```

A third region (C) is recommended for quorum-preserving failover
(A fails, B and C still form a raft majority) but the mandatory
minimum is two.

## 2. What already exists to build this on

- `pkg/consensus/raftlite` already implements membership
  reconfiguration (add/remove/replace node, joint consensus — see
  `confchange.go`, `jointconsensus.go`), snapshot install, and log
  compaction with straggler catch-up (`snapshot.go`). Cross-region
  replication is the same raft replication mechanism at higher
  latency, not new code.
- `pkg/storage/wal` already classifies and fails closed on corruption
  (torn write, corrupt middle) — the exact failure mode a
  region-loss-mid-write scenario produces.
- `internal/sourcehash` + the release certificate give both regions a
  way to cryptographically confirm they are running identical code
  before/after a failover.

## 3. Required RPO/RTO targets (to be set by the engagement owner)

| Metric | Target | Measured how |
|---|---|---|
| RPO (data loss window) | ≤ N minutes | last committed raft index replicated to region B vs. region A's index at failure time |
| RTO (recovery time) | ≤ N minutes | wall-clock from declared region-A loss to region B accepting writes as new leader region |

No estimated RPO/RTO is acceptable evidence — both must be the
actually-observed values from a real drill.

## 4. Mandatory drill scenarios

1. **Primary region total outage** — kill all region-A nodes
   simultaneously; region B must elect a leader and continue serving.
2. **Inter-region network partition** — sever A↔B while both stay up
   internally; confirm no split-brain (raft's own quorum rule should
   prevent A from continuing to commit without B, if B holds the
   majority, or vice versa — this is the property under test).
3. **Storage failure** — corrupt/lose a region's WAL; confirm recovery
   classification (torn write vs. corrupt middle) behaves per
   `pkg/storage/wal`'s existing, tested rules, at the region level.
4. **Node loss within the surviving region** — lose a minority of
   region-B nodes during failover; recovery must still complete.
5. **Control-plane loss** — lose whatever orchestrates failover
   itself (not just the VERIQO nodes); confirm a documented manual
   procedure exists as a fallback.
6. **Partial region degradation** — simulate high latency/packet loss
   rather than total loss; confirm the system degrades rather than
   silently corrupting state.
7. **Recovery into a clean region** — rebuild a lost region from
   snapshot + WAL replay; confirm `RecoveredStateHash ==
   ExpectedCommittedStateHash` (or the divergence is explicitly
   classified and proven safe — never silently accepted).
8. **Failback** — after region A recovers, return primary status to
   it without data loss or a second failover event.
9. **Split-brain prevention** — deliberately try to make both regions
   believe they are primary; the drill fails unless this is
   structurally impossible (raft quorum), not just unlikely.

## 5. Procedure (per drill)

1. Record pre-drill state: `internal/sourcehash` root hash on both
   regions, raft commit index on both regions, READINESS_MANIFEST.json
   from both.
2. Execute the scenario. Record wall-clock timestamps at every state
   transition (failure declared, failover started, new leader region
   accepting writes, old region rejoined).
3. Compute RPO (committed-index delta) and RTO (elapsed time) from
   those timestamps.
4. Verify final state: `RecoveredStateHash == ExpectedCommittedStateHash`,
   or document and justify the divergence.
5. Record PASS/FAIL against the target from §3, with full evidence
   (logs, timestamps, hashes) — not a summary.

## 6. Closure evidence

All nine scenarios in §4, each with a PASS/FAIL verdict, actual
RPO/RTO, and state-hash verification, submitted as
`evidence/external/multi_region_dr.json` per
`pkg/governance/qualification`, signed by whoever operated the drill
(`ProviderID` of type `DR_assessor` or `cloud_provider`) and an
internal reviewer, bound to the release commit under test.
