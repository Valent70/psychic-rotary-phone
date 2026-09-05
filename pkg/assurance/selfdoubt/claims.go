package selfdoubt

import "veriqo/pkg/qualification/ledger"

// Claims is the register of what VERIQO asserts, each with the path by
// which somebody would try to make it false.
//
// # How to read the Outcome column
//
// Most claims here are ESTABLISHED, and every one of them was attacked
// by VERIQO. That is the weakest form of survival available and it is
// recorded on each claim rather than disclaimed once at the top, so a
// reader who quotes one claim carries the qualification with it.
//
// The UNSETTLED claims are the honest ones to look at first: they are
// the assertions for which a disproof path exists on paper and cannot
// be run here, because running it requires a document population, an
// adversary, or a counterparty VERIQO does not have.
var Claims = []Claim{
	{
		ID:        "CLAIM-SEQUENCING",
		Assertion: "no case can resolve without a closed reverse proof, a sealed object, a finding and an authorized decision",
		ProofPath: "the integration proof runs the lawful chain for six domains and asserts VerifyEventOrder and VerifyEventGates on each emitted ledger",
		DisproofPath: "construct a ledger that skips each gate in turn and a Case.Resolve call missing each gate field, " +
			"and demand that every one is refused; the adversarial suite does this in TestResolveFirstProveLater and " +
			"TestAnUngatedLedgerIsCaughtEvenWhenPerfectlyOrdered",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:        "CLAIM-ONE-FINDING-AUTHORITY",
		Assertion: "exactly one function in the module can bring a finding into existence",
		ProofPath: "proof.NewFinding is the only constructor and the Finding's fields are unexported",
		DisproofPath: "parse the whole module and look for any other call to NewFinding from a library package, " +
			"then build a synthetic package containing the violation and confirm the scanner sees it",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:           "CLAIM-UNKNOWN-NOT-INDEPENDENT",
		Assertion:    "an unassessed source pair is never counted towards corroboration",
		ProofPath:    "EffectiveIndependentCount counts a source only if every pairing it takes part in was assessed Independent",
		DisproofPath: "mutation: construct the pair the invariant forbids and try to obtain a corroboration count of two from it",
		Outcome:      Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:        "CLAIM-REDACTION-BYTE-ABSENCE",
		Assertion: "a released derivative contains no rendering of any forbidden term, in any of twelve encodings",
		ProofPath: "the pipeline verifies the decompressed inspectable view of the derivative before releasing it",
		DisproofPath: "run the corpus over twenty-three structural variants and, for each RELEASED derivative, " +
			"search its decompressed view independently of the pipeline's own verdict",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:        "CLAIM-REDACTION-IRREVERSIBLE",
		Assertion: "content removed from a derivative cannot be recovered from it",
		ProofPath: "the forbidden term is absent from every encoding the verifier checks",
		DisproofPath: "an adversarial lab attempts recovery from format-specific remnants -- incremental updates, " +
			"revision history, object streams, embedded thumbnails -- over derivatives produced from a real corpus",
		// Deliberately UNSETTLED. Absence in twelve encodings is not
		// the same claim as irrecoverability, and nothing in this
		// repository attempts recovery.
		Outcome: Unsettled, Level: ledger.Implemented, DisproofRunner: "an adversarial recovery lab (none engaged)",
	},
	{
		ID:        "CLAIM-REDACTION-REAL-WORLD-COVERAGE",
		Assertion: "VERIQO can redact the documents a commercial user would actually send",
		ProofPath: "structural coverage over twenty-three variants, weighted by estimated prevalence",
		DisproofPath: "run the same pipeline over a corpus of documents VERIQO did not create, via VERIQO_CORPUS_DIR, " +
			"and measure the refusal rate rather than estimating it",
		// UNSETTLED and the prevalence weights are estimates, so the
		// proof path is itself weaker than it looks.
		Outcome: Unsettled, Level: ledger.Implemented, DisproofRunner: "a data partner (none engaged)",
	},
	{
		ID:        "CLAIM-TEMPORAL-STANDING",
		Assertion: "no citation of a superseded artefact can be presented as a current claim",
		ProofPath: "the docs rule checks four conditions -- existence, correctness, temporal, semantic -- over every markdown file",
		DisproofPath: "mutation: promote a HISTORICAL reference to CURRENT without a reason, claim HISTORICAL and VALID " +
			"together, and transition away from SUPERSEDED while keeping the successor link",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:        "CLAIM-TRACEABILITY-UNBROKEN",
		Assertion: "the chain requirement to control to code to test to evidence to report cannot break silently",
		ProofPath: "the chain test walks all six hops per article and resolves every CodeRef and TestRef against the filesystem",
		DisproofPath: "delete or rename a cited test and confirm the build fails; this found nine broken citations the " +
			"first time it ran, which is the counterexample that established the path works",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:           "CLAIM-NO-SELF-QUALIFICATION",
		Assertion:    "VERIQO cannot record itself as the external validator of its own control",
		ProofPath:    "the ledger refuses an entry whose validator resolves to VERIQO, and refuses a QUALIFIED level at the self-tested boundary",
		DisproofPath: "mutation: attempt the entry with five spellings of VERIQO as validator, and attempt QUALIFIED while self-tested",
		Outcome:      Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID:           "CLAIM-FIVE-FABRICS-COMPOSE",
		Assertion:    "the five fabrics constitute one executable evidence-to-proof chain across six domains",
		ProofPath:    "the system integration proof runs the chain for each domain and re-verifies five independent ways",
		DisproofPath: "run the chain on real rights-aware commercial data under a data agreement and see whether it still composes",
		// UNSETTLED: the review's own status for this claim, adopted.
		Outcome: Unsettled, Level: ledger.Integrated, DisproofRunner: "a data partner (none engaged)",
	},
	{
		ID:        "CLAIM-TESTS-NOT-OVERFIT",
		Assertion: "the test suite passes because the controls hold, not because the tests are weak",
		ProofPath: "every control has positive and negative tests",
		DisproofPath: "the mutation suite constructs the forbidden value for each invariant and asserts the system " +
			"rejects it; a surviving mutant is a hole whether or not any test exercises it",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID: "CLAIM-LEDGER-TAMPER-EVIDENT",
		Assertion: "no edit to a recorded ledger event can go undetected, and no edit can " +
			"remove records without detection either",
		ProofPath: "every record's digest covers its height and its predecessor's hash, and " +
			"reopening a chain recomputes every link",
		DisproofPath: "edit one field of a record in the middle of a written log and reopen it; " +
			"then truncate a log from the front and reopen it. " +
			"test/adversarial.TestAnEditedLedgerRecordIsDetectedOnReopen and " +
			"TestARemovedLedgerRecordIsDetected",
		ClosedCounterexample: "the disproof path succeeded. A checksum failure ANYWHERE in the " +
			"log was treated as a torn tail, so editing record 2 of 4 silently discarded " +
			"records 2, 3 and 4 and reopened the chain at height 2 with no error -- " +
			"tamper-and-truncate. The same path let a log truncated from the front open as " +
			"an empty chain",
		FixedBy: "pkg/ledger.tail: a torn write is by definition the last thing in a file, so " +
			"damage is accepted as a tail only when what follows it is absent or zero-filled; " +
			"anything else is ErrChainBroken and the ledger refuses to open",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID: "CLAIM-NO-SILENT-TRUNCATION",
		Assertion: "a redacted derivative is never a truncated copy of its original reported " +
			"as verified",
		ProofPath: "every decompressed read is bounded, and the derivative's inspectable view " +
			"is re-derived and searched before release",
		DisproofPath: "submit an OOXML container whose part inflates far past the per-part " +
			"ceiling and check whether what comes back is a refusal or a shortened document " +
			"marked Verified. test/adversarial.TestAZipBombIsRefusedRatherThanExhaustingMemory",
		ClosedCounterexample: "the disproof path succeeded. Every decompressed read used " +
			"io.LimitReader, which succeeds and returns a prefix, so a part inflating to " +
			"256 MiB was truncated to 64 MiB, redacted, released and marked Verified -- with " +
			"192 MiB absent from the derivative and never searched for terms",
		FixedBy: "pkg/evidence/redaction/worker.readBounded refuses at the ceiling instead of " +
			"truncating, with a declared-size pre-check in front of it",
		Outcome: Established, Level: ledger.Assured, DisproofRunner: "VERIQO engineering",
	},
	{
		ID: "CLAIM-INJECTION-STRUCTURALLY-REFUSED",
		Assertion: "an instruction embedded in evidence cannot widen an agent's grants, change " +
			"its purpose, or reach a tool it was not launched with",
		ProofPath: "grants are sealed at Launch, AddGrant exists only to be refused, and every " +
			"scoped argument must be constrained by the grant or the launch fails",
		DisproofPath: "plant an instruction in a document and make every call it asks for: " +
			"widen the grants, read another case, switch purpose, reach export and approve. " +
			"test/adversarial/injection_test.go, ten tests",
		// Deliberately NOT recording a closed counterexample: this
		// disproof path has never produced one, which is exactly the
		// weaker position. The attacker here is the same party that
		// wrote the defence and knows where it looked.
		Outcome: Unsettled, Level: ledger.Implemented,
		DisproofRunner: "VERIQO engineering -- gate G17 requires a red team with AI-agent experience",
	},
}
