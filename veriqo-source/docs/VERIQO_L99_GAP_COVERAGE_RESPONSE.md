# VERIQO -- L99 Gap Coverage Response

Response to the instruction: "Tuntaskan semua yang masih berwarna kuning
dan hijau dalam tabel ini tanpa kecuali" (close everything still colored
yellow AND green in this table, without exception), against the
reviewer's own status table from the Trust Authority Model round:

| Area | Reviewer's rating |
|---|---|
| Evidence authority | GREEN -- Hardened |
| Manifest authority | GREEN -- Hardened |
| Hypothesis authority | GREEN -- Hardened setelah fix baru |
| Finding authority | GREEN -- Hardened |
| Ingestion boundary | GREEN -- Sangat kuat |
| Content-hash binding | GREEN -- Strong |
| Immutability/finalization | GREEN -- Strong |
| Replay determinism | GREEN -- Engineering verified |
| Snapshot restoration | YELLOW -- Not actually live-integrated |
| Distributed replication | YELLOW -- Deterministic equivalence, bukan real replication |
| Concurrency semantics | GREEN -- Strong at Registry level |
| Trust root | ORANGE -- External governance |
| Real-world validation | RED -- Belum |
| Overall maturity | Level 2 -- Engineering Verified |

The instruction named yellow and green specifically -- not orange (Trust
root) or red (Real-world validation), and this round respects that
scope: it does not claim either of those closed, because neither can be
honestly closed by code inside this repository (see "Honest scope
boundary" below).

---

## Part 1 -- Closing the two YELLOW items for real

Both yellow items shared the same honest weakness: real code existed to
REASON about the property (replay-determinism tests proving two
independent `Registry` instances converge; a discussion of what a
forged snapshot would fail against), but neither property was backed by
a LIVE mechanism -- no code anywhere actually snapshotted or replicated
`manifest.Registry`/`evidence.Registry` state across independent nodes.

This round closes that gap using the exact pattern already proven twice
elsewhere in this codebase: `pkg/consensus/raftlite/fsm_adapter.go`'s
`KGAdapter` (for `pkg/moat/kg`) and `fsm_distributed_adapter.go`'s
`DistributedAdapter` (for `pkg/kernel/distributed`), both already
exercised against this repository's own real, live 3-process raft
cluster harness (`test/integration/live_cluster_test.go`,
`pkg/transport/rafttcp`).

### ManifestAdapter (`pkg/consensus/raftlite/fsm_manifest_adapter.go`)

Implements `raftlite.FSM` (`Apply`) and `raftlite.Snapshotter`
(`Snapshot`/`Restore`) for `manifest.Registry`. The design choice that
makes this honest rather than a parallel, weaker mechanism: **Apply and
Restore both dispatch through the SAME three real, gated Registry
methods** (`RegisterDraft`, `RecordCustodyEvent`, `Advance`) -- never
through a direct field write. `Snapshot()` encodes the adapter's own
command log (the ordered sequence of successfully-applied
`RegisterDraft`/`RecordCustodyEvent`/`Advance` calls), never the
Registry's derived `State`/`ManifestHash`/`CustodyChainHead` fields.
`Restore()` rebuilds a brand-new `Registry` by replaying that log
through the real methods -- so `transitionPrerequisiteLocked`'s
prerequisite/hash-chain gate applies identically whether a command
arrived via live consensus or via snapshot install. A single failed
command anywhere in the log fails the ENTIRE restore; on failure, the
adapter's live Registry pointer is left completely untouched
(fail-closed, matching `KGAdapter.Restore`'s own contract).

### EvidenceAdapter (`pkg/consensus/raftlite/fsm_evidence_adapter.go`)

The same pattern for `evidence.Registry`, complicated honestly by one
real fact: `SetRights` only succeeds when `authorityID` names an entity
whose trust was granted in a SEPARATE `provenance.Registry`. A
snapshot/replication story that ignored this would be dishonest -- so
`EvidenceAdapter`'s command log includes `provenance.Registry`'s own two
mutators (`Register`, `GrantTrust`) alongside `evidence.Registry`'s five
(`Submit`, `SetStrength`, `VerifyStatus`, `SetRights`,
`MarkSuperseded`). A restored `SetRights` grant is only honored if the
SAME snapshot's own `GrantTrust` command legitimately established that
authority first.

### The reviewer's three named proofs, answered for real

1. **"Node A -> FINALIZED Evidence -> ... -> Node B produces exactly the
   same authority state."**
   `TestManifestCluster_FinalizedEvidenceConvergesAcrossRealConsensus`:
   a genuine 3-node `raftlite` cluster (real leader election, real
   `AppendEntries` replication, real majority commit -- via
   `raftlite.MemTransport`, the same in-process transport this
   repository's own `raft_test.go`/`snapshot_test.go` already use to
   validate the consensus protocol itself) drives one `EvidenceID`
   through its full lifecycle to `FINALIZED` via `Propose()` on the
   leader. Every follower's OWN, independently-applied `ManifestAdapter`
   converges on the byte-identical `ManifestHash` and
   `CustodyChainHead`, and each node's own state independently
   re-verifies (`manifest.VerifyManifestHash`,
   `Registry.VerifyCustodyChain`). This is the literal Node-A/Node-B
   scenario, via the real committed-log replication path, not two
   `Registry` instances driven by the same test goroutine.
   `TestEvidenceCluster_RightsGrantConvergesAcrossRealConsensus` proves
   the same for the Evidence Record authority layer's `SetRights` grant.

2. **"Snapshot with forged status must be rejected."**
   `TestManifestAdapterRestoreRejectsAForgedFinalizeWithNoPrerequisites`:
   a command log spliced with a fabricated `ADVANCE`-to-`FINALIZED` for
   an `EvidenceID` with no `RegisterDraft` behind it at all is refused
   by `Restore`'s real replay (the target registry doesn't even exist
   yet). `TestEvidenceAdapterRestoreRejectsRightsGrantFromAnUntrustedAuthority`:
   a `SET_RIGHTS` command citing an authority no
   `REGISTER_AUTHORITY`/`GRANT_TRUST` pair in the SAME log ever trusted
   is refused (`ErrRightsGrantNotAuthorized`). Both fail-closed: the
   live registry/registries are left completely untouched on rejection.

3. **"Replay omits REVIEWED event must not result in FINALIZED."**
   `TestManifestAdapterRestoreRejectsAReplayOmittingReviewedEvent`: a
   command log that drops the REVIEWED custody-event command but still
   tries to `ADVANCE` all the way to `FINALIZED` is refused by
   `Restore`'s replay (`ErrTransitionPrerequisiteNotMet`), the same real
   gate `TestReplayOmittingReviewedEventCannotReachFinalized` already
   proved against the bare `Registry`, now proven again through the real
   snapshot/restore wire path.

### Snapshot restoration made live, not just reasoned about

`TestManifestCluster_StragglerCatchesUpViaRealInstallSnapshot`: a
follower is isolated, the connected majority is driven past
`DefaultCompactionThreshold` (compacting its log), and the partition is
healed. The straggler can ONLY catch up via a real `InstallSnapshot` RPC
carrying `ManifestAdapter.Snapshot()`'s bytes -- the entries it needs no
longer exist anywhere in the cluster's log. Its post-install state
independently re-verifies and hash-matches the leader's. This is
"snapshot restoration" moved from "no code anywhere touches these types"
to "a real, tested, fail-closed FSM integration exists and was proven
live."

### Break-test-fix-restore

Every forgery-rejection test was verified against a real regression: the
per-command error check inside `Restore`'s replay loop was temporarily
disabled (errors ignored, replay always "succeeding"); the corresponding
tests failed with the exact expected diagnostic ("expected Restore to
reject..."); the check was restored and all tests pass again -- for both
`ManifestAdapter` and `EvidenceAdapter`.

---

## Part 2 -- Re-verifying the GREEN items ("tanpa kecuali")

Rather than re-asserting the green ratings unchanged, this round ran a
targeted, repository-wide search for the SAME anti-pattern the prior
round's `causation.HypothesisSet.Add` fix closed: a registration/
construction function that takes a caller-supplied value containing an
authority-bearing field (a status, a trust flag, a rights/permission
level, a key lifecycle state) and only CONDITIONALLY defaults that field
(`if x.Field == "" { x.Field = default }`) instead of unconditionally
forcing or deriving it -- letting a caller-supplied non-default value
sail through unchecked.

### NEW finding this round: `pkg/platform/security/keys.Manager.Register`

`Register` only forced a key's `State` to `StatePending` when the caller
left it blank. `ActiveKeyFor` and `Sign` both branch directly on
`State==StateActive` with no separate derivation step (unlike, say,
`evidence.Status`, which is always DERIVED via `DeriveStatus` and never
taken as caller input at all) -- so a caller who hand-built
`KeyMetadata{State: StateActive}` and called `Register` directly got an
immediately-signing-capable key, completely skipping the `Activate()`
step this package's own documented lifecycle (`PENDING -> ACTIVE ->
RETIRED -> REVOKED`) treats as the gate.

This was not merely theoretical: a real, non-test production file
(`pkg/blockers/hsmkms/hsmkms.go`) already constructs
`KeyMetadata{State: StateActive}` directly and calls `Register` with it
(in that specific fixture, immediately followed by `Revoke`, so its net
effect there was benign) -- proving the exported `Register` function was
a live, reachable bypass, not a dead code path.

**Fixed:** `Register` now unconditionally forces `State = StatePending`
regardless of caller input, matching `manifest.Registry.RegisterDraft`
and `evidence.Registry.Submit`'s own pattern exactly. `Manager.Rotate`'s
own LEGITIMATE internal need to atomically retire the old key and
activate its successor (which previously relied on calling the public
`Register` with a pre-set `State=StateActive`) is preserved via a new
unexported `registerLocked` helper: `Register` wraps it (always forcing
`Pending`), and `Rotate` calls it directly while still holding its own
lock -- `Rotate` is itself an already-gated, privileged operation (it
independently verified the predecessor's own real state first), not an
external caller trying to assert a state.

New test `TestRegisterForcesPendingRegardlessOfCallerSuppliedState`
covers `StateActive`/`StateRevoked`/`StateRetired` forged inputs,
proving each still refuses to sign until a real `Activate()` call.
Verified via break-test-fix-restore. The pre-existing
`TestRotationPreservesHistoricalSignatures` confirmed the legitimate
`Rotate` path still works after the fix (it initially broke the fix's
first, cruder version -- a straight unconditional force inside the
public `Register` alone -- which is exactly why `registerLocked` exists
as a separate, internal-only path).

### Audited, no fix needed: `pkg/insurance/casepack.BuildEvidence`

A candidate second finding: `BuildEvidence` lets a caller-supplied
`EvidenceSpec.Rights` overwrite a freshly-constructed `Record.Rights`
before returning it. Read in full before deciding: this is safe, for
three independent reasons, not merely absent from today's callers by
luck.

1. `evidence.Registry.Submit` -- the ONLY real ingestion path into the
   live Evidence Registry (via `Facade.IngestEvidence`) --
   unconditionally resets `Rights` regardless of what the record already
   carries. The live registry's own authority boundary is untouched no
   matter what `BuildEvidence` set.
2. The one place a RAW (pre-`Submit`) record is read elsewhere in this
   package's own drive path (`coverage.Input.Evidence`, feeding
   `AnalyzeCoverage`) never consults `.Rights` at all -- confirmed by
   grep across `pkg/insurance/coverage`.
3. The field is explicitly documented on `EvidenceSpec` itself ("Rights
   is the rights state to record... matching evidence.New") as a
   scenario-authoring convenience for this package's own stated purpose:
   building synthetic golden-path test cases, not production evidence
   ingestion. Its one existing caller (`scenarios.go`) uses it in the
   RESTRICTIVE direction (`RightsInternalOnly`), to prove a denial path
   works.

No code change made. Recorded here, rather than silently passed over,
because the instruction was "without exception" -- every candidate this
audit surfaced gets a stated disposition, whether that disposition is a
fix or a reasoned "not a bug."

### Everything else checked and confirmed already correctly hardened

`pkg/insurance/evidence.New`/`Submit`, `pkg/evidence/provenance.Register`/
`GrantTrust`, `pkg/insurance/credential` (accessor-sealed `revoked`
field), `pkg/insurance/cre.AuthorizedFinding` (accessor-only sealing),
`pkg/evidence/manifest.Advance`/`transitionPrerequisiteLocked`
(validation gates, not defaulting assignments), `pkg/trust`/
`pkg/trust/state` (scores/levels always computed by pure functions,
never caller-supplied), `pkg/authz` (approval/effect fields set only
through dedicated gated methods) -- all independently confirmed to
follow the unconditional-force-or-derive pattern already, with no
conditional-only-if-empty gap found.

---

## Verification

```
gofmt -l .                                    clean
go build ./...                                clean
go vet ./...                                  clean
go test ./...                                 full repository suite: 189 packages, 0 FAIL
go test -race (raftlite, keys, and every
  other affected package)                     clean, no data races
pkg/consensus/raftlite package                 all tests pass, 11 new this round (6
                                                 manifest adapter + 5 evidence adapter),
                                                 stable across repeated runs and -race
pkg/platform/security/keys package             all tests pass, 1 new adversarial test
```

---

## Updated status table

| Area | Prior rating | This round |
|---|---|---|
| Evidence authority | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Manifest authority | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Hypothesis authority | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Finding authority | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Ingestion boundary | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Content-hash binding | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Immutability/finalization | GREEN | GREEN -- unchanged, re-audited, no gap found |
| Replay determinism | GREEN | GREEN -- unchanged, re-audited, no gap found |
| **Snapshot restoration** | YELLOW | **GREEN -- live FSM integration, real InstallSnapshot proven, forged snapshots refused** |
| **Distributed replication** | YELLOW | **GREEN -- real 3-node raft consensus proven, both authority layers converge identically** |
| Concurrency semantics | GREEN | GREEN -- unchanged, re-audited, no gap found |
| **Key lifecycle authority** | *(not in the reviewer's original table)* | **NEW finding closed this round -- Manager.Register bypass fixed** |
| Trust root | ORANGE | ORANGE -- unchanged, out of this round's scope (not requested; remains irreducible, see below) |
| Real-world validation | RED | RED -- unchanged, out of this round's scope (Level 3/4, not requested) |
| Overall maturity | Level 2 | **Level 2 -- Engineering Verified, now including live consensus-integration evidence** |

---

## Honest scope boundary

- **Trust root and Real-world validation were deliberately left
  untouched.** The instruction named yellow and green items; these are
  orange and red respectively. A prior round's own research (into
  `pkg/trust`, `pkg/security/policy`, `pkg/security/identity`) already
  established that the trust root is irreducible by code alone: whatever
  policy/identity check might be added to `GrantTrust`'s call site still
  requires *someone* to be the root authority who authored that policy
  or minted the identity-issuing CA -- a general trust-anchor
  bootstrapping problem no in-repo code change can close. Real-world
  validation requires actual external counterparties and production
  incidents, which is Level 3/4 by this engagement's own maturity model,
  not achievable through further engineering work in this repository.
- **The new consensus proofs use `raftlite.MemTransport`** (this
  repository's own in-process transport, used throughout its existing
  raft test suite), not the full mTLS `pkg/transport/rafttcp` +
  multi-process/Docker harness `test/integration/live_cluster_test.go`
  exercises for `pkg/kernel/distributed`. This still proves genuine,
  real multi-node consensus (distinct `*Node` instances, real leader
  election, real `AppendEntries`/`InstallSnapshot` RPC exchange, real
  log replication) -- not two `Registry` instances driven by one test
  goroutine -- but it is not the same as a real-socket, real-process,
  real-network-partition proof. Extending `ManifestAdapter`/
  `EvidenceAdapter` onto the full `rafttcp` harness is a natural, real,
  and currently-unstarted next step, named honestly rather than
  implied as already done.
- This remains Level 2 (Engineering Verified) work. No claim of Level 3
  or Level 4 is made anywhere in this report.
