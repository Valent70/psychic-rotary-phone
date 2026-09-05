package gates

// The twenty permanent gates.
//
// G1-G8 are the original eight blockers, kept as gates rather than
// deleted when closed. G9-G20 are the additions the specification
// names. Thirteen of the twenty require a party outside VERIQO, which
// is not a shortfall in the engineering -- it is what the word
// "independent" means, and a register that showed three external gates
// would be one where somebody had redefined the word.

// Veriqo returns the gate definitions.
func Veriqo() []Gate {
	return []Gate{
		// --- The original eight ------------------------------------
		{ID: "G1", Name: "HSM/KMS production tenancy", Category: Security,
			What: "tenant, case and signing keys are rooted in an attested HSM or managed " +
				"KMS tenancy, not in software",
			Why: "a software root is a key VERIQO can lose, exfiltrate or silently rotate, " +
				"and every encryption and signature claim rests on it",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "an HSM vendor or cloud KMS provider, with an attestation"},

		{ID: "G2", Name: "Live commercial data contract", Category: DataAndRights,
			What: "at least one commercial evidence source is contracted, with terms VERIQO " +
				"has read and encoded as a licence",
			Why: "every coverage and independence figure is currently computed over data " +
				"VERIQO created; none of it has met a real feed",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a commercial data provider"},

		{ID: "G3", Name: "Multi-region infrastructure", Category: Infrastructure,
			What: "evidence, graph, ledger and compute are deployed in at least two regions " +
				"with independent recoverability",
			Why: "a single-region deployment has no answer to a region loss, and data " +
				"residency cannot be honoured from one region"},

		{ID: "G4", Name: "Independent penetration test", Category: Security,
			What: "a firm that is not VERIQO has attempted to break the system and reported " +
				"what they found",
			Why: "every security control here was designed and tested by the party it " +
				"protects; surviving one's own attack is the weakest form of survival",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "an accredited penetration testing firm"},

		{ID: "G5", Name: "Physical multi-host qualification", Category: Infrastructure,
			What: "the consensus and replication paths have run across physically separate " +
				"hosts, not goroutines in one process",
			Why: "in-process concurrency does not exhibit partition, clock skew or partial " +
				"failure, which are the conditions those paths exist for"},

		{ID: "G6", Name: "72-hour soak", Category: Resilience,
			What: "the system has run continuously for at least 72 hours under representative " +
				"load with no unbounded growth",
			Why: "leaks, unbounded queues and clock-related defects appear on a timescale " +
				"no test suite reaches"},

		{ID: "G7", Name: "SPIFFE/mTLS production attestation", Category: Security,
			What: "every workload holds an SVID issued by a real SPIRE deployment and every " +
				"internal call is mutually authenticated",
			Why: "VERIQO validates the SHAPE of a SPIFFE ID today; nothing proves a workload " +
				"holds the identity it names",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a SPIRE deployment operated separately from the workloads"},

		{ID: "G8", Name: "Vulnerability feed and dependency scan", Category: Security,
			What: "dependencies are scanned against a live vulnerability feed on every build",
			Why: "a dependency with a known critical vulnerability is a compromise waiting " +
				"for somebody to notice it is available",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a vulnerability database provider"},

		// --- The twelve additions -----------------------------------
		{ID: "G9", Name: "Independent real-world corpus", Category: Qualification,
			What: "the document pipeline has run over a corpus VERIQO did not create",
			Why: "the 88% weighted coverage figure is an ESTIMATE over VERIQO's own " +
				"fixtures; it becomes a measurement the first time it meets real documents",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a customer or an industry body supplying a document corpus"},

		{ID: "G10", Name: "External evidence-provider validation", Category: Qualification,
			What: "a provider has confirmed that material VERIQO holds is what they supplied",
			Why: "the provenance chain records what VERIQO believes it received; nobody " +
				"upstream has been asked to confirm it",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "an evidence provider willing to confirm a content hash"},

		{ID: "G11", Name: "Disaster recovery test", Category: Resilience,
			What: "a full recovery from backup has been performed and timed against a stated " +
				"RPO and RTO",
			Why: "a documented recovery procedure that has never been executed is a plan, " +
				"not a capability"},

		{ID: "G12", Name: "Restore and replay verification", Category: Resilience,
			What: "a restored system has replayed a historical case and produced the same " +
				"result",
			Why: "a restore that recovers the bytes and not the conclusions has recovered " +
				"a database, not a case"},

		{ID: "G13", Name: "Key compromise simulation", Category: Security,
			What: "a key compromise has been simulated end to end: revocation, enumeration " +
				"of affected material, re-keying and customer notification",
			Why: "the revocation path is the least exercised and the most consequential; a " +
				"path that has never run is a path with unknown failures"},

		{ID: "G14", Name: "Tenant isolation test", Category: Security,
			What: "every isolation surface has been tested for leakage under adversarial " +
				"conditions",
			Why: "isolation that holds on the happy path and fails under a crafted query is " +
				"isolation that will be discovered by a customer"},

		{ID: "G15", Name: "Cross-tenant exfiltration test", Category: Security,
			What: "a deliberate attempt has been made to move data between tenants through " +
				"every surface, including cache, search, graph and agent context",
			Why: "the isolation test proves the boundary holds where it was checked; this " +
				"proves somebody looked for where it was not",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a red team that is not the team that built the isolation"},

		{ID: "G16", Name: "Agent tool abuse test", Category: AIGovernance,
			What: "the tool firewall has been attacked: scope escape, budget exhaustion, " +
				"argument smuggling and grant widening",
			Why: "the firewall is the boundary between an agent that helps and an agent " +
				"that exfiltrates, and it has only been tested by its author",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a red team with AI-agent experience"},

		{ID: "G17", Name: "Prompt injection test", Category: AIGovernance,
			What: "documents and tool results carrying injected instructions have been fed " +
				"through the full agent pipeline",
			Why: "the defence is structural, and a structural defence that nobody has " +
				"attacked is a design nobody has attacked",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a red team with AI-agent experience"},

		{ID: "G18", Name: "Data poisoning test", Category: AIGovernance,
			What: "crafted evidence has been introduced to see whether it can move a " +
				"conclusion without moving the confidence vector or the contradiction record",
			Why: "an attacker who can add evidence can move a conclusion; the question is " +
				"whether they can do it invisibly",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a red team"},

		{ID: "G19", Name: "Supply-chain dependency security", Category: Security,
			What: "an SBOM is produced per build, artefacts are signed, and the build is " +
				"reproducible where feasible",
			Why:                   "VERIQO's own integrity claims are worth what its build pipeline is worth",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "a signing authority and an attestation service"},

		{ID: "G20", Name: "Model regression qualification", Category: AIGovernance,
			What: "every model in PRODUCTION has been re-evaluated against a held-out set " +
				"since its last change, and the results are recorded",
			Why: "a model that degrades silently degrades every conclusion downstream of " +
				"it, and nothing in the system notices",
			RequiresExternalParty: true,
			WhoCouldSatisfy:       "an evaluation set VERIQO did not construct"},
	}
}

// VeriqoRegister builds the register with the current, honest state.
//
// Nothing here is aspirational. Each state is what is actually true of
// this repository, and the notes say why -- which is the whole value
// of the register: a reader can disagree with a specific claim rather
// than with a summary.
func VeriqoRegister() (*Register, error) {
	r, err := NewRegister(Veriqo())
	if err != nil {
		return nil, err
	}
	blocked := map[string]string{
		"G1": "the key hierarchy is implemented and its root is a software TEST DOUBLE " +
			"that refuses to run in production mode",
		"G2": "no commercial data contract is in place; every figure is computed over " +
			"VERIQO-created material",
		"G4": "no penetration test has been commissioned",
		"G7": "SPIFFE IDs are validated syntactically; no SPIRE deployment attests them",
		"G8": "no vulnerability feed is reachable from this environment",
		"G9": "VERIQO_CORPUS_DIR is wired and empty; the weighted coverage figure remains " +
			"an ESTIMATE",
		"G10": "no provider has been asked to confirm a content hash",
		"G15": "the isolation tests are written and run by the team that built the isolation",
		"G16": "the firewall's tests are written by its author",
		"G17": "the injection defence has been designed and self-tested, not attacked",
		"G18": "no poisoning attempt has been made",
		"G19": "no SBOM or artefact signing is configured in this environment",
		"G20": "no held-out evaluation set exists that VERIQO did not construct",
	}
	inProgress := map[string]string{
		"G3":  "the architecture supports it; no multi-region deployment exists",
		"G5":  "concurrency is exercised in-process only; no multi-host run has been performed",
		"G6":  "the longest run in this environment is minutes, not 72 hours",
		"G11": "backup and restore are designed; no timed recovery has been performed",
		"G12": "the replay engine exists and has not been run against a restored system",
		"G13": "the revocation path is implemented and enumerates affected material; " +
			"no end-to-end compromise has been simulated",
		"G14": "isolation is enforced by key derivation and tested per surface; no " +
			"adversarial leakage testing has been done",
	}
	for id, note := range blocked {
		if err := r.Set(id, BlockedExternal, note); err != nil {
			return nil, err
		}
	}
	for id, note := range inProgress {
		if err := r.Set(id, InProgress, note); err != nil {
			return nil, err
		}
	}
	return r, nil
}
