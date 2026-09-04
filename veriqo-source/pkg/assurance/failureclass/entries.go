package failureclass

// Closed is the register of findings this repository has carried
// through the whole discipline. Each entry is a real finding from a
// real review round, not an illustration.
//
// The register is deliberately written as data rather than prose: the
// architecture test resolves every test name here against the module,
// so a renamed or deleted test breaks the build instead of leaving the
// chain looking intact.
var Closed = []Entry{
	{
		ID:    "FC-001",
		Round: "CS",
		Finding: "pkg/caseproofgraph called proof.NewFinding, so the graph package could " +
			"bring a Finding into existence as well as the proof pipeline",
		RootCause: "the authoritative constructor was exported and reachable from any package, " +
			"so authority was a convention rather than a property of the type system",
		Class: AuthorityDiffusion,
		Invariant: "exactly one function in the module may construct a Finding; every other " +
			"package must reference one it was given",
		PositiveTest:   "TestAFindingNamesExactlyOneProofOneCaseOneAuthority",
		NegativeTest:   "TestGraphAsABackDoorToAFinding",
		MutationTest:   "TestTheScannerCatchesAGraphPackageThatMintsFindings",
		RegressionTest: "TestNoLibraryPackageCanMintAnAuthoritativeObject",
	},
	{
		ID:    "FC-002",
		Round: "CS",
		Finding: "searching the bytes of a PDF or an OOXML package for a forbidden term found " +
			"nothing even when the term was present in the document",
		RootCause: "verification ran over the container, and every one of these formats stores " +
			"its content compressed, so the term does not exist as a byte string at that level",
		Class: VacuousVerification,
		Invariant: "verification must run over the decompressed inspectable view of a derivative, " +
			"never over its container bytes",
		PositiveTest:   "TestEachWorkerProducesAVerifiedDerivative",
		NegativeTest:   "TestCompressionWouldHaveHiddenTheTerm",
		MutationTest:   "TestNoVariantLeaks",
		RegressionTest: "TestTheChainStatesTheCompressionLimitation",
	},
	{
		ID:    "FC-003",
		Round: "F",
		Finding: "EffectiveSourceCount, which counts distinct source clusters, was used as the " +
			"corroboration count, so a pair whose independence was never assessed counted as two",
		RootCause: "two different questions -- how many distinct sources, and how many " +
			"independently corroborating sources -- were answered by one function",
		Class: UnassessedAsAssessed,
		Invariant: "a source may only count towards corroboration if every pairing it takes part " +
			"in was assessed Independent; UNKNOWN never contributes",
		PositiveTest:   "TestFullyAssessedSourcesStillCorroborate",
		NegativeTest:   "TestUnknownIsNotCountedTowardsCorroboration",
		MutationTest:   "TestMutationUnknownBecomesIndependent",
		RegressionTest: "TestUnassessedDimensionYieldsUnknownNotIndependent",
	},
	{
		ID:    "FC-004",
		Round: "G",
		Finding: "nine constitutional articles cited test names that no longer existed anywhere " +
			"in the module, and the matrix rendered green",
		RootCause: "TestRef was free text that no test resolved, so a rename silently detached " +
			"the control from its proof",
		Class: StaleCitation,
		Invariant: "every cited test must name a test declared in the module, and every document " +
			"citation must carry a temporal standing that is checked against the runtime record",
		PositiveTest:   "TestEveryCitedRuntimeRecordExists",
		NegativeTest:   "TestAStaleCitationIsRecognisedAsStale",
		MutationTest:   "TestMutationHistoricalBecomesCurrent",
		RegressionTest: "TestEveryTestReferenceNamesATestThatExists",
	},
	{
		ID:    "FC-005",
		Round: "H",
		Finding: "the object-stream and cross-reference-stream corpus fixtures were not real " +
			"containers: they carried the forbidden term in a plain dictionary with the " +
			"structure named only in a comment",
		RootCause: "the fixture was written to make a test pass rather than to reproduce the " +
			"structure the test claimed to cover, which is the overfitting the review named " +
			"appearing inside our own corpus",
		Class: FixtureNotGenuine,
		Invariant: "a corpus fixture must be a genuine container whose forbidden term is not " +
			"findable in its raw bytes until the container is unpacked",
		PositiveTest:   "TestEveryVariantBuildsARealContainerCarryingTheTerm",
		NegativeTest:   "TestTheOOXMLVariantsAreGenuinelyCompressed",
		MutationTest:   "TestTheObjectStreamFixtureIsGenuine",
		RegressionTest: "TestTheUbiquitousPDF15StructuresAreNoLongerRefused",
	},
	{
		ID:    "FC-006",
		Round: "G",
		Finding: "nothing prevented recording VERIQO as the external validator of a VERIQO " +
			"control, or recording a QUALIFIED level while the boundary was self-tested",
		RootCause: "the level and the validator were independent fields with no rule connecting " +
			"them, so the ladder could be climbed without leaving the building",
		Class: SelfQualification,
		Invariant: "a QUALIFIED entry must name a validator that does not resolve to VERIQO and " +
			"must sit at the externally-validated boundary",
		PositiveTest:   "TestTheLadderIsTheReviewersLadder",
		NegativeTest:   "TestVeriqoCannotBeItsOwnExternalValidator",
		MutationTest:   "TestMutationVeriqoBecomesItsOwnValidator",
		RegressionTest: "TestAQualifiedClaimNeedsTheValidatedBoundary",
	},
	{
		ID:    "FC-007",
		Round: "CS",
		Finding: "a case could reach a resolved outcome and have its reverse proof closed " +
			"afterwards, with the ledger showing both events and no ordering violation",
		RootCause: "the reverse proof was implemented as a post-hoc audit over the emitted " +
			"events rather than as a precondition of the transition",
		Class: SequencingBypass,
		Invariant: "Case.Resolve must consume a finalized, reverse-closed, authorized decision; " +
			"no ordering audit may substitute for the gate",
		PositiveTest:   "TestSystemIntegrationProofForEveryDomain",
		NegativeTest:   "TestCaseCannotResolveOverAnUnprovenMaterialClaim",
		MutationTest:   "TestResolveFirstProveLater",
		RegressionTest: "TestAnUngatedLedgerIsCaughtEvenWhenPerfectlyOrdered",
	},
	{
		ID:    "FC-008",
		Round: "H",
		Finding: "the Article 18 corpus reported 15 of 23 and that number was read as a pass " +
			"rate, when 8 of the 23 were structures the pipeline refused outright",
		RootCause: "one ratio was made to carry two meanings -- how much of the corpus the " +
			"pipeline handles, and how much of it the pipeline handles correctly -- and " +
			"neither was weighted by how often the structures actually occur",
		Class: ScopeOverclaim,
		Invariant: "coverage must be reported as coverage, never as a pass rate, and must be " +
			"accompanied by a prevalence-weighted estimate and its named gap",
		PositiveTest:   "TestTheCoverageRatioIsReportedAsCoverageNotAsAPassRate",
		NegativeTest:   "TestTheWeightedGapIsReported",
		MutationTest:   "TestMutationRefusedBecomesFailed",
		RegressionTest: "TestWeightedCoverageIsReportedAsAnEstimate",
	},
	{
		ID:    "FC-009",
		Round: "H",
		Finding: "evidence with a strong integrity story and no independence at all would have " +
			"been read as sufficient, because the assessment had no place to record that " +
			"independence was absent rather than weak",
		RootCause: "quality was treated as one judgement instead of nine separate questions, so " +
			"a strong answer to one could stand in for a missing answer to another",
		Class: OffsettingAttributes,
		Invariant: "no evidence attribute may offset another, and an attribute that was not " +
			"assessed must be reported as unassessed rather than as adequate",
		PositiveTest:   "TestEveryStrongVectorSupports",
		NegativeTest:   "TestStrongIntegrityDoesNotOffsetAbsentIndependence",
		MutationTest:   "TestMutationNotAssessedBecomesAssessed",
		RegressionTest: "TestNotAssessedIsNotSufficient",
	},
	{
		ID:    "FC-010",
		Round: "CS",
		Finding: "a derivative with a black rectangle drawn over the text, and one with the " +
			"term stripped from a single encoding, both presented as irreversibly redacted",
		RootCause: "the pipeline verified the transformation it performed rather than the " +
			"property it claimed, so hiding content and removing content were indistinguishable " +
			"to the verifier",
		Class: IrreversibilityOverclaim,
		Invariant: "a derivative may only be released after the forbidden term is shown absent " +
			"from every encoding the verifier checks, in the decompressed view",
		PositiveTest:   "TestXMLEscapedFormsAreRemoved",
		NegativeTest:   "TestRedactionThatStripsOneEncodingOnly",
		MutationTest:   "TestVisualOnlyRedactionPresentedAsIrreversible",
		RegressionTest: "TestTheDerivativeIsDeterministic",
	},
}
