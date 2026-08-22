# Pre-Insurance Closure Program — Verification Matrix (R20)

Every item of the mandate, its implementation, the named test that
proves it, the result, where the evidence lives, and its status.

Status vocabulary is this repository's own
(`docs/governance/RELEASE_GATES.md`): only OPEN, IMPLEMENTED,
INTEGRATED, VERIFIED, QUALIFIED, PRODUCTION_READY, BLOCKED and WAIVED
are legal. "Done", "Closed" and "Complete" are not statuses here.

Reproduce any row with `go test -run <TestName> ./<package>/`.

---

## PHASE A — Canonical Evidence Write Authority (P0-1)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Canonical contract version is declared | `pkg/evidence/api.Contract()` — composes the versions the owning packages publish | `TestCanonicalContractVersionIsDeclared` | PASS | `evidence/canonical_evidence_production_coverage.json` | VERIFIED |
| Contract descriptor cannot drift from code | `contractHash` over components + path + type vocabulary | `TestContractHashChangesWhenAComponentDoes` | PASS | same | VERIFIED |
| Unauthorized evidence writers = 0 | `internal/nobypass.Check` (pre-existing, reused) | `TestEvidenceProductionCoverageIsCleanOnTheRealTree` | PASS | `evidence/truth_arbitration_no_bypass.json` | VERIFIED |
| Unauthorized ingestion paths = 0 | `nobypass.EvidenceProductionCoverage` (new scan, shared walk) | `TestEvidenceProductionCoverageDetectsAnInjectedIngestionPath` | PASS | `evidence/canonical_evidence_production_coverage.json` | VERIFIED |
| Gate fails without a declared contract | `EvidenceCoverage.Pass()` | `TestEvidenceProductionCoverageFailsWithoutADeclaredContract` | PASS | same | VERIFIED |
| CrossSubsystemRoundTrip | Facade `Submit` → `Arbitrate` → `Correlate` → `Verify` | `TestCrossSubsystemRoundTrip` | PASS | `pkg/evidence/api/preservation_test.go` | VERIFIED |
| LosslessProjection | `api.Project` / `Projection.Recoverable` | `TestLosslessProjection` | PASS | same | VERIFIED |
| SourceHashPreservation | `ontology.Evidence.ComputeID` through the Facade store | `TestSourceHashPreservation` | PASS | same | VERIFIED |
| RightsPreservation | `provenance.ExternalEvidence.Permits` + `OntologyProvenance` | `TestRightsPreservation` | PASS (scoped — see note) | same | VERIFIED |
| ProvenancePreservation | field-by-field round trip | `TestProvenancePreservation` | PASS | same | VERIFIED |
| CorrectionPreservation | `ontology.TypeCorrection` + `ApplyCorrections` | `TestCorrectionPreservation`, `TestCorrectionWithoutATargetIsRefused` | PASS | same | VERIFIED |
| ContradictionMetadataPreservation | Facade `ObserveRaw` / `RawObservations` / `ArbitrateClaim` | `TestContradictionMetadataPreservation` | PASS | same | VERIFIED |
| ReplayEquality | Facade `Replay` | `TestReplayEquality` | PASS | same | VERIFIED |
| CrossFacadeIdentityEquality | two independent Facades, identical evidence | `TestCrossFacadeIdentityEquality` | PASS | same | VERIFIED |

> **Scoping note on RightsPreservation.** `provenance.RightsState` is
> deliberately outside `ontology.Evidence`'s content hash — that package
> documents why. The test therefore asserts what is actually true
> (projection never *upgrades* rights; a REVOKED envelope permits
> nothing) rather than a field-equality that would be false.

---

## PHASE B — Canonical Identity Authority (P0-2)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Production identity writers = 1 | `entityconsistency.ScanIdentityAuthority` | `TestIdentityAuthorityCoverageOnTheRealTree` | PASS | `evidence/canonical_identity_authority_coverage.json` | VERIFIED |
| Unauthorized writers = 0 | same | `TestScanIdentityAuthorityDetectsAnInjectedIdentityWriter` | PASS | same | VERIFIED |
| Independent legacy merges = 0 | same | `TestScanIdentityAuthorityDetectsAnIndependentLegacyMerge` | PASS | same | VERIFIED |
| Maritime mappings explicit | `entityconsistency.MaritimeMapping` + `maritime.KnownEntityKinds` | `TestEveryMaritimeKindIsExplicitlyMappedOrExplicitlyUnmapped` | PASS (4 of 11 honestly UNMAPPED) | same | VERIFIED |
| Unknown mappings marked UNMAPPED, never guessed | `UnmappedKind` constant; a kind with no row is a failure | `TestAnUnmappedMaritimeKindIsNeverSilentlyGuessed` | PASS | same | VERIFIED |
| Legacy fallback sets `legacy_identity_fallback_used` + `human_review_required` | `lifecycle.Result` fields + span attributes | `TestLegacyIdentityFallbackIsLoudlyMarked` | PASS | `pkg/lifecycle/lifecycle_test.go` | VERIFIED |
| Fallback never writes canonical identity | same test asserts the ledger head is unchanged | `TestLegacyIdentityFallbackIsLoudlyMarked` | PASS | same | VERIFIED |
| Canonical path is never mislabelled as a fallback | control test | `TestCanonicalIdentityPathIsNeverMarkedAsAFallback` | PASS | same | VERIFIED |

---

## PHASE C — Canonical Execution Path (P0-3)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Parallel governed execution paths = 0 | `internal/entrypoints.Audit` (whole-tree) | `TestNoGovernedDecisionOutsideCanonicalExecution` | PASS | `evidence/canonical_execution_entrypoint_coverage.json` | VERIFIED |
| Entrypoint matrix across all nine kinds | `entrypoints.Matrix()` | `TestEveryEntrypointKindIsAccountedFor` | PASS | same | VERIFIED |
| Matrix rows checked against real source | `Audit`'s `MatrixErrors` | `TestAuditDetectsAMatrixRowThatLiesAboutItsPath`, `TestEveryMatrixRowNamesARealFile` | PASS | same | VERIFIED |
| Scanner genuinely detects | injected back door in a temp tree | `TestAuditDetectsAnInjectedBackDoor` | PASS | same | VERIFIED |
| Exactly one governed-decision entrypoint | `Report.GovernedEntrypoints` | `TestExactlyOneEntrypointProducesGovernedDecisions` | PASS | same | VERIFIED |

---

## PHASE D — Correlation Context (P0-4)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| DropEvidencePackageID fails verification | `execution.Context.validate` (made mandatory this round) | `TestAdversarialDropEvidencePackageID` | PASS | `pkg/platform/correlation/adversarial_test.go` | VERIFIED |
| AlterExecutionID fails verification | `execution.ReplayDAGWithResult` node comparison | `TestAdversarialAlterExecutionID` | PASS | same | VERIFIED |
| SwapEntityID fails verification | `execution.ErrIdentityMismatch` | `TestAdversarialSwapEntityID` | PASS | same | VERIFIED |
| ReplaceDecisionID is detectable | content-addressed `decisionID` re-derived by replay | `TestAdversarialReplaceDecisionID` | PASS | same | VERIFIED |
| AlterIdentityLedgerHead fails verification | `replay.Engine.Replay` fingerprint chain | `TestAdversarialAlterIdentityLedgerHead` | PASS | same | VERIFIED |
| No silent regeneration in any case | each test asserts the identifier was not rewritten | all five above | PASS | same | VERIFIED |
| Untampered baseline verifies clean (control) | — | `TestAdversarialUntamperedBaselineVerifiesClean` | PASS | same | VERIFIED |

---

## PHASE D2 — Case Lineage (P0-5)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| One CaseID aggregates all eleven node kinds | `pkg/lineage.Ledger` | `TestFullCaseWalksEndToEndFromASingleCaseID` | PASS | `pkg/lineage/lineage_test.go` | VERIFIED |
| Case lineage completeness = true, proved end to end on a REAL run | `lifecycle.Orchestrator.Lineage` + `recordLineage` | `TestCaseLineageWalksARealRunEndToEndFromOneCaseID` | PASS | `pkg/lifecycle/lifecycle_test.go` | VERIFIED |
| Completeness is derived, never declared | `Ledger.Completeness` | `TestIncompleteCaseIsNeverReportedComplete` | PASS | `pkg/lineage/lineage_test.go` | VERIFIED |
| Lineage is tamper-evident | per-case hash chain | `TestCaseLineageIsTamperEvident`, `TestReorderingNodesBreaksTheChain` | PASS | same | VERIFIED |
| No dangling upstream accepted | `Attach` | `TestAttachRefusesADanglingUpstream` | PASS | same | VERIFIED |
| Binds to the existing correlation key, not a second one | `FromCorrelation` | `TestFromCorrelationOmitsEmptyFieldsInsteadOfInventingPlaceholders`, `TestFromCorrelationAloneIsHonestlyIncomplete` | PASS | same | VERIFIED |
| No insurance business logic implemented | package contains no claim/coverage/quantum/dispute concept | (structural — see `pkg/lineage` doc comment) | N/A | — | VERIFIED |

---

## PHASE E — Evidence Envelope (P0-6)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| One envelope for external and internal evidence | `pkg/governance/envelope.Envelope` | `TestWellFormedLiveEnvelopeIsAccepted` | PASS | `pkg/governance/envelope/envelope_test.go` | VERIFIED |
| Rejects: wrong commit / source hash / artifact hash / environment / expired / revoked provider / unauthorized provider / missing reviewer / fixture-labelled-LIVE | `Validator.Check` | `TestValidatorRejectsEveryNamedFailureMode` (14 subtests) | PASS | same | VERIFIED |
| A fixture can never self-promote | `IsFixture()` reads OriginKind, not the claim | `TestFixtureCanNeverSelfPromote` | PASS | same | VERIFIED |
| Fixture must declare limitations | `Validate` | `TestFixtureEnvelopeMustDeclareLimitations` | PASS | same | VERIFIED |
| Artifact root is order-independent and tamper-evident | `ArtifactRoot` | `TestArtifactRootIsOrderIndependentAndTamperEvident` | PASS | same | VERIFIED |
| Envelope is content-addressed over every field | `Envelope.ID` | `TestEnvelopeIDIsContentAddressedAndDetectsEveryFieldChange` (19 fields) | PASS | same | VERIFIED |

---

## PHASE E2 — Evidence Freshness Gate (P0-7)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| PASS requires commit, source_hash, artifact_root, binary_hash all matching and valid_until ≥ qualification time | `envelope.Freshness` | `TestFreshnessPassesOnAnExactReleaseMatch` | PASS | `pkg/governance/envelope/envelope_test.go` | VERIFIED |
| Otherwise reports `BLOCKED_STALE_EVIDENCE` **by name** | `FreshnessBlockedStale` | `TestFreshnessReportsBlockedStaleEvidenceByName` (5 subtests) | PASS | same | VERIFIED |
| Every divergence reported at once | `Freshness` compares all fields before returning | `TestFreshnessReportsEveryDivergenceAtOnce` | PASS | same | VERIFIED |
| A release with nothing to compare against fails closed | `cmp` helper | `TestFreshnessRefusesAReleaseThatDeclaresNothingToCompareAgainst` | PASS | same | VERIFIED |

---

## PHASE E3 — Readiness Engine Separation (P0-8)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| ENGINEERING / INTERNAL / EXTERNAL / FINAL as four distinct axes | `internal/assurance.Gate.Axes()` | `TestBlockedGateReportsEngineeringAndInternalSeparatelyFromExternal` | PASS | `READINESS_MANIFEST.json` → `axes` | VERIFIED |
| Retrofitted onto the 8 blocked gates | `cmd/veriqo-readiness.attachBlockerAxes` | same | PASS | same | VERIFIED |
| Axis reporting never advances a gate | derived-only; no setter | `TestAxisSeparationNeverAdvancesTheGateItself` | PASS | same | VERIFIED |
| Internal evidence never moves the EXTERNAL axis | `axes.go` structural rule | `TestInternalQualifiedIsNeverExternalQualified` | PASS | same | VERIFIED |
| Absence of evidence is NOT_RUN, never PASS | `axisFromEvidence` | `TestAxisWithNoEvidenceIsNotRunNotPass` | PASS | same | VERIFIED |
| Tampered axis evidence degrades | `Evidence.Passing()` | `TestTamperedAxisEvidenceDegradesRatherThanPasses` | PASS | same | VERIFIED |
| Axes reach the manifest operators read | `BuildReadinessManifest` | `TestReadinessManifestCarriesTheAxes` | PASS | `READINESS_MANIFEST.json` | VERIFIED |

---

## PHASE E4 — External Evidence Validator (P1-16)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Validates signature, provider, reviewer, commit, source hash, environment, artifact hash, expiry, revocation, gate authorization | `qualification.Registry.Validate` (pre-existing) + `envelope.Validator.Submit` bridge | `TestValidatorRejectsEveryExternalEvidenceFailureMode` (14 subtests, each from properly-signed evidence) | PASS | `pkg/governance/envelope/validator_test.go` | VERIFIED |
| Real signed LIVE evidence passes (control) | — | `TestRealSignedLiveEvidencePassesEveryCheck` | PASS | same | VERIFIED |
| Envelope-only fields are covered by the signature | measurement-map projection | `TestEnvelopeOnlyFieldsAreCoveredByTheSignature` (8 fields) | PASS | same | VERIFIED |
| Does **not** promote any of the eight | `Submit` never calls Qualify/VerifyGate | `TestSubmitNeverPromotesAGateOnItsOwn` | PASS | same | VERIFIED |
| An honest fixture still cannot qualify a blocked gate | environment allow-list + classification | `TestHonestFixtureSubmissionIsStillRefusedForTheEightBlockers` | PASS | same | VERIFIED |

---

## PHASE E5 — Architecture Coverage Gates (P1-17)

| Gate | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| `canonical_evidence_production_coverage` (= evidence write coverage) | `nobypass.EvidenceProductionCoverage` | `TestEvidenceProductionCoverageIsCleanOnTheRealTree` | PASS | `evidence/canonical_evidence_production_coverage.json` | VERIFIED |
| `canonical_identity_authority_coverage` | `entityconsistency.ScanIdentityAuthority` | `TestIdentityAuthorityCoverageOnTheRealTree` | PASS | `evidence/canonical_identity_authority_coverage.json` | VERIFIED |
| `canonical_execution_entrypoint_coverage` | `entrypoints.Audit` | `TestNoGovernedDecisionOutsideCanonicalExecution` | PASS | `evidence/canonical_execution_entrypoint_coverage.json` | VERIFIED |
| `policy_registry_usage_coverage` | runtime DAG test (not a scan) | `TestPolicyRegistryUsageCoverageEveryDecisionCommitsToItsPolicy`, `…RefusesAMismatchedPolicy`, `…AcceptsTheMatchingPolicy` | PASS | `evidence/policy_registry_usage_coverage.txt` | VERIFIED |
| `temporal_calibration_usage_coverage` | runtime DAG test | `TestTemporalCalibrationUsageCoverageFailsClosedWhenRequiredAndAbsent`, `…SkipsHonestlyWhenNotRequired`, `…ExecutesWhenSupplied` | PASS | `evidence/temporal_calibration_usage_coverage.txt` | VERIFIED |
| `correlation_propagation_coverage` | runtime + cross-process | `TestCorrelationPropagationCoverageEveryIdentifierIsPopulated`, `…IdentifiersAreDistinct`, `…SurvivesAnIndependentReplay` | PASS | `evidence/correlation_propagation_coverage.txt` | VERIFIED |

---

## PHASE F — Temporal Bayesian Contract (P1-9)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Fifteen model-risk metadata fields, each mandatory | `calibration.ContractMetadata.Validate` | `TestEveryContractMetadataFieldIsIndividuallyRequired` | PASS | `pkg/governance/calibration/contract_test.go` | VERIFIED |
| Explicit execution-state enum (5 values) | `calibration.ExecutionState` | `TestFailsClosedIsExhaustiveOverTheStateVocabulary` | PASS | same | VERIFIED |
| Required calibration can NEVER be recorded as a skip | `NewContract` | `TestRequiredCalibrationCanNeverBeRecordedAsASkip` | PASS | same | VERIFIED |
| Missing model under a requiring policy fails closed | `Registry.ContractFor` → REQUIRED_BUT_MISSING → `Assert` | `TestMissingModelUnderARequiringPolicyFailsClosed` | PASS | same | VERIFIED |
| Mislabelled obligations refused | `NewContract` | `TestMislabelledObligationsAreRefused` | PASS | same | VERIFIED |
| Contract is content-addressed | `contractHash` | `TestContractHashDetectsEveryMeaningfulChange` (9 mutations) | PASS | same | VERIFIED |

---

## PHASE F2 — Real-World Calibration Interface (P1-10)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Receives Real Event / Ground Truth / Label / Dataset / Fit | `corpus.go` (pre-existing, reused) | `TestFitComputesExactFrequentistLikelihoodsFromLabeledEvents` | PASS | `pkg/governance/calibration/corpus_test.go` | VERIFIED |
| Holdout | `calibration.Split` (deterministic, hash-based) | `TestHoldoutIsDeterministicAndDisjoint`, `TestSplitIsStableWhenAnEventIsInsertedInTheMiddle` | PASS | `pipeline_test.go` | VERIFIED |
| Evaluation | `calibration.Evaluate` reusing `pkg/moat/reliability` | `TestEvaluationUsesTheRepositorysOwnMetrics` | PASS | same | VERIFIED |
| Model | `CalibratedModel` binding corpus+fit+evaluation | `TestModelHashBindsCorpusFitAndEvaluationTogether` | PASS | same | VERIFIED |
| Fit happens on the training half only | `BuildModel` | `TestFitHappensOnTheTrainingHalfOnly` | PASS | same | VERIFIED |
| Outcome stays `EXTERNAL_DATA_REQUIRED` until a real corpus arrives | `deriveStatus` reads the declaration, not performance | `TestFixtureCorpusAlwaysReportsExternalDataRequired`, `TestNoAmountOfRunningTheMachineryRaisesTheStatus` | PASS | same | **EXTERNAL_DATA_REQUIRED** (machinery VERIFIED) |
| No code can declare a corpus real | `CorpusDeclaration` is operator-supplied | `TestAnUndeclaredCorpusIsRefused` | PASS | same | VERIFIED |

---

## PHASE F3 — Replay Completeness (P1-13)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| All 13 identities identical across a real Process A → destroy → Process B replay | `cmd/veriqo-cold-replay` identity block + `identitiesOf` comparison | `TestColdReplay_CrossProcess_AllThirteenIdentitiesMatch` | PASS | `test/integration/cold_replay_cross_process_test.go` | VERIFIED |
| Comparison cannot pass vacuously | all 13 asserted non-empty in Process A first | same test | PASS | same | VERIFIED |
| No identity block emitted on divergence | deliberate | `TestColdReplay_CrossProcess_IdentityBlockAbsentOnDivergence` | PASS | same | VERIFIED |
| No second replay engine built | same binary as the pre-existing tests | (structural) | N/A | — | VERIFIED |

---

## PHASE H — Operational Observability + Leakage (P1-11, P1-12)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| VERIQO → OTel-compatible exporter → collector → storage → query | `telemetry.Exporter` / `Collector` | `TestExportPipelineDeliversPersistsAndQueries` | PASS | `evidence/telemetry_export_pipeline_internal.txt` | VERIFIED (semantics) / **INTERNAL_QUALIFIED** (operationally) |
| OTel wire shape | `ResourceSpans`/`ScopeSpans`/`ExportedSpan` | `TestPayloadIsOTelShaped` | PASS | same | VERIFIED |
| Batching preserves all spans | `Exporter.BatchSize` | `TestExporterBatchesLargeSpanSets` | PASS | same | VERIFIED |
| Fails loudly, never discards silently | `ErrNoSink`, `ErrExporterClosed` | `TestExporterFailsLoudlyRatherThanDiscarding` | PASS | same | VERIFIED |
| Honest ceiling stated in code | `PipelineQualification()` | `TestPipelineQualificationIsHonestlyCapped` | PASS | same | VERIFIED |
| **Secret leakage = 0** | exporter-boundary redaction, default-deny | `TestNoSensitiveValueLeavesTheProcessInATraceAttribute`, `TestSecretsInErrorMessagesDoNotLeak` | PASS | `evidence/telemetry_leakage_zero.txt` | VERIFIED |
| **PII leakage = 0** | same | `TestNoSensitiveValueLeavesTheProcessInATraceAttribute` | PASS | same | VERIFIED |
| **Restricted payload leakage = 0** (raw B/L, restricted AIS) | same | same | PASS | same | VERIFIED |
| Commercial + customer-confidential leakage = 0 | same | same | PASS | same | VERIFIED |
| Correlation identifiers survive redaction (counterweight) | allow-list | `TestCorrelationIdentifiersSurviveRedaction` | PASS | same | VERIFIED |
| Findings never carry the redacted value | salted digest only | `TestFindingsNeverCarryTheRedactedValue` | PASS | same | VERIFIED |

---

## PHASE I — Reproducible Build Provenance (P1-14)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Binary equality across independent builds | `internal/reproducibility` (pre-existing) | `TestBinaryEqualityAcrossIndependentBuilds` | PASS | `.github/workflows/reproducible-build.yml` | VERIFIED |
| Stores source commit, dependency lock, compiler, build flags, artifact digest, SBOM | `reproducibility.Record` | `TestEveryAlwaysDeterminableFieldIsIndividuallyRequired` | PASS | `internal/reproducibility/provenance_test.go` | VERIFIED |
| Stores builder identity, workflow identity, base image digest, base image SBOM, OS package inventory | contextual fields | `TestDescribeReportsAHigherFractionWhenMoreIsKnown` | PASS | same | VERIFIED |
| Unanswerable fields report UNKNOWN, never fabricated | `Describe()` / `ValueOrUnknown` | `TestUnanswerableFieldsAreReportedUnknownNotFabricated` | PASS | same | VERIFIED |
| Attestation storage | `Record.WithAttestation` | `TestAttestationCoversTheRecordItSaysItCovers` | PASS | same | VERIFIED |
| Provenance is content-addressed over every field | `ComputeHash` | `TestProvenanceHashDetectsEveryStoredField` (16 mutations) | PASS | same | VERIFIED |
| Real builder/workflow identity for this build | — | `TestCIIdentityHelpersReturnEmptyOutsideCI` | PASS (empty is correct here) | — | **BLOCKED_EXTERNAL** — needs a CI or container build to populate |

---

## PHASE J — Sandbox Hardening (P1-15)

| Escape vector | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Host filesystem access | `sandbox.Policy` allow-list + `OSEnforcer` mount-namespace requirement | `TestEscapeVectorHostFilesystemAccessIsDenied` | PASS | `pkg/kernel/sandbox/osenforcer_test.go` | VERIFIED (policy) / INTERNAL_QUALIFIED (OS) |
| Host network access | network-namespace requirement | `TestEscapeVectorHostNetworkAccessIsDenied` | PASS | same | VERIFIED / INTERNAL_QUALIFIED |
| Privilege escalation | `PR_SET_NO_NEW_PRIVS` requirement | `TestEscapeVectorPrivilegeEscalationIsDenied` | PASS | same | VERIFIED / INTERNAL_QUALIFIED |
| Secret mount access | path + env allow-lists | `TestEscapeVectorSecretMountAccessIsDenied` | PASS | same | VERIFIED / INTERNAL_QUALIFIED |
| Forbidden syscall | seccomp requirement | `TestEscapeVectorForbiddenSyscallIsDenied` | PASS | same | VERIFIED / INTERNAL_QUALIFIED |
| Cgroup escape | cgroup v2 + mount namespace | `TestEscapeVectorCgroupEscapeIsDenied` | PASS | same | VERIFIED / INTERNAL_QUALIFIED |
| Namespace escape | PID ns + mount ns + seccomp | `TestEscapeVectorNamespaceEscapeIsDenied` | PASS | same | VERIFIED / INTERNAL_QUALIFIED |
| Supported-but-unapplied primitive is not "closed" | `needsShim` + `ShimAvailable` | `TestSupportedButUnappliedPrimitiveIsNotTreatedAsClosed` | PASS | same | VERIFIED |
| Enforcer never assumes an absent primitive | `CanEnforce` | `TestEnforcerNeverAssumesAnAbsentPrimitive` | PASS | same | VERIFIED |
| Probe reads the real kernel | `/proc`, `/sys` | `TestProbeReadsTheRealKernelRatherThanAssuming` | PASS | same | VERIFIED |
| Honest ceiling stated in code | `sandbox.Qualification()` | `TestQualificationIsHonestlyCapped` | PASS | same | VERIFIED |
| Containment of a genuinely hostile binary | — | — | not attempted | — | **BLOCKED_EXTERNAL** — needs an adversarial drill on a production kernel |

---

## PHASE K — External Qualification Harness (P2-18)

| Requirement | Implementation | Test | Result | Evidence | Status |
|---|---|---|---|---|---|
| Scale / DR / soak / KMS / SPIRE harnesses exist | `pkg/blockers/*` (pre-existing) | package suites + `orchestrator.RunAll` | PASS | `evidence/blockers-qualification-report.json` | VERIFIED (harness) |
| Per-capability coverage declared | `blockers.CapabilityRegister` (33 rows) | `TestRegisterIsInternallyConsistent` | PASS | `evidence/external_harness_capability_coverage.json` | VERIFIED |
| Register cannot drift into fiction | symbol-declaration check against real source | `TestEveryCapabilityCitesRealCode` | PASS | same | VERIFIED |
| Every gate still names something external | — | `TestEveryGateStillNamesSomethingExternal` | PASS | same | VERIFIED |
| Harness completeness ≠ gate status | — | `TestHarnessCompletenessIsNotGateStatus` | PASS | same | VERIFIED |
| No harness can fake a qualification | vocabulary contains no qualifying value | `TestNoCapabilityStatusCanExpressQualification`, `TestHarnessCanNeverQualifyIsStated` | PASS | same | VERIFIED |
| The qualifications themselves | — | — | not attempted | — | **BLOCKED** (all 8 — see the residual register) |

---

## Hard-gate checklist

The mandate's own "do not consider this done while any of these is
true" list, answered:

| Condition | Status | Proof |
|---|---|---|
| canonical evidence write paths with >0 unauthorized | **0** | `TestEvidenceProductionCoverageIsCleanOnTheRealTree` |
| canonical identity writers >1 | **1** | `TestIdentityAuthorityCoverageOnTheRealTree` |
| governed decision paths outside the execution graph >0 | **0** | `TestNoGovernedDecisionOutsideCanonicalExecution` |
| correlation propagation gaps >0 | **0** | `TestCorrelationPropagationCoverageEveryIdentifierIsPopulated` |
| case lineage incomplete | **complete on a real run** | `TestCaseLineageWalksARealRunEndToEndFromOneCaseID` |
| replay identity mismatch >0 | **0 of 13** | `TestColdReplay_CrossProcess_AllThirteenIdentitiesMatch` |
| stale qualification evidence accepted | **false** | `TestFreshnessReportsBlockedStaleEvidenceByName`, `TestStaleEvidenceIsRefusedWithTheNamedReason` |
| a fixture can self-promote to VERIFIED | **false** | `TestFixtureCanNeverSelfPromote`, `TestHonestFixtureSubmissionIsStillRefusedForTheEightBlockers` |
| required temporal calibration can silently skip | **false** | `TestRequiredCalibrationCanNeverBeRecordedAsASkip`, `TestTemporalCalibrationUsageCoverageFailsClosedWhenRequiredAndAbsent` |
| secret/PII leakage >0 | **0** | `TestNoSensitiveValueLeavesTheProcessInATraceAttribute` |
| production provenance incomplete | **storage complete; contextual fields honestly UNKNOWN here** | `TestUnanswerableFieldsAreReportedUnknownNotFabricated` |
| external evidence validator absent | **present** | `TestValidatorRejectsEveryExternalEvidenceFailureMode` |
