package plan

import "veriqo/pkg/qualification/ledger"

// Items is the plan for every open control.
//
// Two things about this table are worth reading before its contents.
//
// First, most items are BLOCKED. That is the finding, not a failure of
// planning. A control whose next honest level is QUALIFIED cannot be
// promoted by writing code, because QUALIFIED means an outside party
// examined it. Presenting those as engineering tasks would produce a
// roadmap that appears to converge and never does.
//
// Second, the ACTIONABLE items are deliberately few and specific. Each
// is something VERIQO can genuinely finish, and finishing it moves the
// control exactly one rung -- to ASSURED, the internal ceiling. None of
// them reaches QUALIFIED, because none of them can.
var Items = []Item{
	// --- Actionable by VERIQO today ---------------------------------

	{Article: 3, Control: "Transitive source clustering: same-root data counts once",
		From: ledger.Integrated, To: ledger.Assured,
		Obligation: "the corroboration count must never treat an unassessed pair as independent, " +
			"and callers must use the corroboration count rather than the cluster count",
		Method: "EffectiveIndependentCount over sources with a deliberately unassessed dimension, " +
			"plus a survey of every caller that reads a source count",
		Artefact:  "TestUnknownIsNotCountedTowardsCorroboration, and the named unassessed pairs it returns",
		Criteria:  "an unassessed pair yields a count below the number of sources AND is reported by name",
		Validator: VeriqoEngineering},

	{Article: 10, Control: "Replay is verifiable without trusting the runtime",
		From: ledger.Integrated, To: ledger.Assured,
		Obligation: "a third party holding only the ledger and the verifier binary must reach the same verdict",
		Method:     "run cmd/veriqo-commercial-verify as a separate process against an exported ledger",
		Artefact:   "the verifier's exit status and report, and the ledger root hash it checked",
		Criteria:   "the separate process agrees, and disagrees when one byte of the ledger is altered",
		Validator:  VeriqoEngineering},

	{Article: 24, Control: "Every disclosure emits a ledger event",
		From: ledger.Integrated, To: ledger.Assured,
		Obligation: "no disclosure path exists that does not append an event, including a DENIED one",
		Method:     "an AST sweep for callers of access.Evaluate that do not append, plus a negative test per path",
		Artefact:   "the sweep's output and AUDIT-014-redaction.derivative_released as a live example",
		Criteria:   "every path appends; a path that returns a decision without appending fails the build",
		Validator:  VeriqoEngineering},

	{Article: 29, Control: "OBSERVED_ABSENT only after the observability gate",
		From: ledger.Integrated, To: ledger.Assured,
		Obligation: "an absence may not carry evidential weight unless every gate condition was met and recorded",
		Method:     "property test over every combination of gate conditions",
		Artefact:   "the assessment records, each naming which condition was not met",
		Criteria:   "weight is carried only for the all-conditions-met case; every other combination is refused",
		Validator:  VeriqoEngineering},

	// --- Blocked on an independent assessor ---------------------------
	//
	// These controls are implemented, called, tested and leave runtime
	// evidence. What is missing is somebody who is not VERIQO reading
	// the procedure and saying it was correctly applied. That is the
	// definition of QUALIFIED and no test can supply it.

	{Article: 2, Control: "Acquisition records provenance without conferring qualification",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms that a well-sourced item carries no epistemic standing anywhere in the system",
		Method:     "assessor review of the authority quintuple and the acquisition-to-finding path",
		Artefact:   "the assessor's report naming the paths examined",
		Criteria:   "the assessor finds no path by which provenance alone reaches a finding",
		Validator:  IndependentAssessor,
		Blocker:    "no independent assessor is engaged"},

	{Article: 4, Control: "Rights are evaluated before contact, not after",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms no code path touches evidence before access.Evaluate returns",
		Method:     "assessor-directed code review plus an ordering trace over a live request",
		Artefact:   "the ordered ledger of a disclosure request, and the assessor's finding",
		Criteria:   "no evidence read precedes the rights decision on any path the assessor examines",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 7, Control: "Historical cases resolve against their historical policy version",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms a policy change cannot alter a historical case's outcome",
		Method:     "replay a resolved case under a later policy version and compare outcomes",
		Artefact:   "both replay transcripts and their root hashes",
		Criteria:   "the historical outcome is unchanged and the attempt is recorded",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 8, Control: "AI cannot create, alter, qualify or sign evidence",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor attempts to reach evidence authority through every AI surface and fails",
		Method:     "adversarial review of the AI Evidence Gateway by a party who did not build it",
		Artefact:   "the assessor's attempt log, including the attempts that were refused",
		Criteria:   "no attempt produces, alters, qualifies or signs evidence",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 11, Control: "Dissent is carried through qualification, never deleted",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms dissent survives every path from registration to resolution",
		Method:     "assessor-chosen cases traced end to end",
		Artefact:   "the case timelines showing dissent present at each stage",
		Criteria:   "no path removes or suppresses a recorded dissent",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 12, Control: "The same policy applies to every party absent an authorized exception",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms no party receives different treatment without a recorded exception",
		Method:     "differential testing across party roles on identical inputs",
		Artefact:   "the paired decision records per role",
		Criteria:   "outcomes differ only where an authorized exception is recorded",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 13, Control: "Party influence on acquisition is recorded",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms every party-mediated acquisition is marked and carried as a limitation",
		Method:     "assessor review of acquisition records against the sources that produced them",
		Artefact:   "the acquisition records and the proof-object limitations derived from them",
		Criteria:   "no party-mediated item reaches a proof object without its mediation stated",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 14, Control: "Conflicts are declared rather than concealed",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms a conflict cannot be resolved by concealment on any path",
		Method:     "assessor review of the conflict register and its consumers",
		Artefact:   "the conflict register and the decisions that cite it",
		Criteria:   "every recorded conflict appears in the decisions it bears on",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 17, Control: "Redaction never modifies the original",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms the original is byte-identical after every redaction path",
		Method:     "assessor-run redactions with independent hashing of the original before and after",
		Artefact:   "the before and after hashes, computed by the assessor's own tooling",
		Criteria:   "the original's hash is unchanged in every run",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 19, Control: "VERIQO enforces privilege; it does not determine it",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "counsel confirms the system nowhere decides whether privilege attaches",
		Method:     "legal review of the privilege model and its state transitions",
		Artefact:   "counsel's written opinion",
		Criteria:   "counsel finds no determination of privilege, only enforcement of a declared status",
		Validator:  LegalCounsel, Blocker: "no legal review is commissioned"},

	{Article: 20, Control: "View, export, AI processing and training are separate grants",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms no grant implies another on any path",
		Method:     "exhaustive grant-combination testing directed by the assessor",
		Artefact:   "the decision matrix over all grant combinations",
		Criteria:   "no combination yields a right that was not granted",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 21, Control: "A redacted derivative must still pass AI policy",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms a derivative does not inherit the original's AI permissions",
		Method:     "assessor-run gateway evaluation of a derivative under the original's grant",
		Artefact:   "the gateway decisions for original and derivative",
		Criteria:   "the derivative is evaluated on its own grant, not the original's",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 25, Control: "Privilege transitions are immutable events",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms no transition can be edited or removed after the fact",
		Method:     "tamper attempts against the transition record by the assessor",
		Artefact:   "the transition chain and the assessor's tamper log",
		Criteria:   "every alteration is detected",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 26, Control: "Policy change is never quietly applied to history",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms a retroactive application is refused and recorded",
		Method:     "assessor-directed retroactivity attempts across policy versions",
		Artefact:   "the refusal records",
		Criteria:   "every attempt is refused and leaves a record",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 27, Control: "Material AI contribution is recorded and human-reviewed",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms no material AI contribution reaches a proof object without a named reviewer",
		Method:     "assessor review of the AI contribution records against the objects that cite them",
		Artefact:   "the contribution records and reviewer identities",
		Criteria:   "every material contribution names a human reviewer",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 28, Control: "UNKNOWN independence is never treated as INDEPENDENT",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms no caller anywhere reads an unassessed pair as corroboration",
		Method:     "assessor-directed caller survey plus differential testing of both count functions",
		Artefact:   "the caller inventory and the counts each produces",
		Criteria:   "no caller uses the cluster count where corroboration is meant",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 30, Control: "Integrity, provenance, qualification, neutrality and legal determination stay distinct",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an assessor confirms the five concerns are never conflated in any output",
		Method:     "assessor review of every report and dossier the system produces",
		Artefact:   "the reviewed outputs and the assessor's finding",
		Criteria:   "no output presents one concern as another",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	// --- Blocked on an adversarial lab ---------------------------------

	{Article: 18, Control: "Redacted content is absent from the derivative's bytes",
		From: ledger.Assured, To: ledger.ExternallyValidated,
		Obligation: "a party who did not build the workers attempts to recover redacted content and fails, " +
			"over a corpus of documents VERIQO did not create",
		Method: "adversarial recovery against derivatives produced from a supplied corpus, " +
			"with the corpus fed through VERIQO_CORPUS_DIR",
		Artefact: "the lab's recovery attempts, the corpus coverage ratio, and the refusal reasons per document",
		Criteria: "no forbidden term is recovered from any RELEASED derivative, and the refusal rate over " +
			"the real corpus is measured and published rather than estimated",
		Validator: AdversarialLab,
		Blocker: "no adversarial lab is engaged and no external corpus is supplied; the workers refuse " +
			"object streams and cross-reference streams, which are ubiquitous in PDF 1.5+, so the " +
			"real-world refusal rate is expected to be high and is currently unmeasured"},

	// --- Blocked on an external timestamping authority ------------------

	{Article: 5, Control: "Raw bytes are preserved before any transformation",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an attestation from a party independent of the matter fixes the raw bytes in time",
		Method:     "RFC 3161 timestamping of the raw-byte hash at acquisition",
		Artefact:   "the TSA token and its verification record",
		Criteria:   "the token verifies against the TSA's certificate and predates every transformation",
		Validator:  ExternalTSA, Blocker: "no external TSA is engaged; the self-hosted chain is not independent"},

	{Article: 6, Control: "A finalized version is structurally unupdatable",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an outside party confirms finalization cannot be reversed by any interface",
		Method:     "assessor tamper attempts against a finalized version through every entry point",
		Artefact:   "the attempt log and the refusals",
		Criteria:   "no interface mutates a finalized version",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 22, Control: "Every derivative is a new immutable version",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an outside party confirms no derivative overwrites its original version record",
		Method:     "assessor review of the version lineage across derivation paths",
		Artefact:   "the lineage graph and its hashes",
		Criteria:   "every derivative has its own version id and hash, and its original is intact",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	{Article: 23, Control: "Process evidence is itself evidence",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "an outside party confirms the process record is preserved to the same standard as the evidence",
		Method:     "assessor comparison of process-record handling against evidence handling",
		Artefact:   "the two retention and integrity treatments, side by side",
		Criteria:   "process evidence is not held to a weaker standard",
		Validator:  IndependentAssessor, Blocker: "no independent assessor is engaged"},

	// --- Blocked on legal determination ---------------------------------

	{Article: 16, Control: "The platform does not determine legal liability",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "counsel confirms no output constitutes or implies a determination of legal liability",
		Method:     "legal review of every decision and outcome vocabulary the system can emit",
		Artefact:   "counsel's opinion, referencing the vocabularies reviewed",
		Criteria:   "counsel identifies no output that determines liability",
		Validator:  LegalCounsel, Blocker: "no legal review is commissioned"},

	// --- Blocked on real data -------------------------------------------

	{Article: 1, Control: "Finding requires a sealed proof object with a pinned evidence set",
		From: ledger.Assured, To: ledger.Qualified,
		Obligation: "the rule holds on real commercial evidence, not only on fixtures",
		Method:     "run the cross-domain rig on real AIS, survey and policy data under a data agreement",
		Artefact:   "the resulting case, its proof objects and its ledger",
		Criteria:   "no finding is reached without a sealed object over real pinned evidence",
		Validator:  DataPartner,
		Blocker:    "no data agreement exists; the cross-domain rig runs on fixtures"},
}

// AccreditationTrack names the levels above Qualified, which no item in
// this plan reaches.
//
// It is recorded so that a reader of the plan can see the plan STOPS
// short of accreditation and production, rather than inferring that
// completing every item above would finish the job.
const AccreditationTrack = "Every item in this plan stops at QUALIFIED or EXTERNALLY_VALIDATED. " +
	"No item reaches PRODUCTION_PROVEN, which requires operating under real load with real " +
	"consequences and surviving audit, and which no plan written before deployment can schedule."

// Accreditor is named here rather than in an item because no control's
// next rung is accreditation: accreditation follows independent
// validation, and nothing has been independently validated.
var _ = Accreditor
