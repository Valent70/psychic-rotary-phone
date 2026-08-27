# VERIQO Architecture Invariants (PHASE 36)

An invariant here is not a design aspiration. Each one names the test
that would fail if it were violated. An invariant with no enforcing
test is not on this list.

| # | Invariant | Statement | Enforced by |
|---|-----------|-----------|-------------|
| I1 | Determinism | Same input + same context produces the same result | `TestReplayIsDeterministicAcrossEngines`, `TestInferenceIsDeterministicAndReplayable`, `TestSimulationIsDeterministic` |
| I2 | Replay | An independent replay of a recorded execution reproduces it exactly, using only serialized inputs | `TestFullLifecycleReplayMatches`, `TestAcceptance53` |
| I3 | Replay identity | `ReplayResultHash == OriginalResultHash` is the assertion; no identifier is reused across execution/evidence/replay/verdict | `TestReplayIdentitiesAreAllDistinct`, `TestAcceptance71` |
| I4 | Lineage | Every decision and every simulation traces to the evidence it rests on | `TestEverySimulationCarriesCompleteLineage`, `simulation.ErrLineageIncomplete` |
| I5 | Integrity | Tampering with any ledger record, certificate or package is detected | `TestDependencyGraphTamperDetection`, `TestAcceptance72-75`, `TestAcceptance80` |
| I6 | Trust floor | No evidence produces no unjustified confidence; trust may not rise without evidence | `TestTrustIncreaseRequiresEvidence`, `TestAcceptance65` |
| I7 | Dependency | Correlated evidence cannot inflate confidence as if independent | `TestSharedSatelliteProviderCannotInflateConfidence`, `TestCorrelatedObservationsCannotInflateBelief`, `TestAcceptance61` |
| I8 | Fusion gate | Canonical cannot fuse without dependency evaluation | `TestCanonicalUsesEvidenceDependencyGraph`, `TestCanonicalCannotFuseWithoutDependencyEvaluation` |
| I9 | Identity correction | Unmerge and correction preserve historical state; earlier replays stay valid | `TestUnmergePreservesHistoricalReplay`, `TestAcceptance82` |
| I10 | Epistemic separation | Derived inference may never be counted as primary evidence | `TestDerivedEvidenceCannotBeArbitratedAsPrimary`, `TestAcceptance62` |
| I11 | Policy | A denied action cannot commit; DENY always beats ALLOW; default is deny | `TestAcceptance68`, `TestAcceptance107` |
| I12 | Key lifecycle | Rotation preserves historical signatures; revocation invalidates them retroactively | `TestRotationPreservesHistoricalSignatures`, `TestRevocationInvalidatesRetroactively` |
| I13 | Bitemporality | "What was true" and "what we knew" are separately answerable | `TestBitemporalAsOf`, `TestAcceptance57` |
| I14 | No false green | One failing mandatory gate cannot be compensated by any number of passing ones | `TestOneCriticalGapCannotBeCompensated` |
| I15 | Consensus safety | Never two leaders in the same term | `pkg/consensus/raftlite` tests |

## Invariants deliberately NOT claimed

- **Commutativity of merge order for vector clocks.** Order-sensitive by
  design; asserting it would be wrong. (`TestMergeBindsIdentifiersAndIsOrderInvariant`
  claims order-invariance only for the *canonical entity ID*, which is
  a set hash, not for the ledger.)
- **Certificate equality across different actors or ledger positions.**
  Hash-chaining makes position and actor part of the hash on purpose.
