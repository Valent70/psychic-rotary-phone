package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// The canonical object contract.
//
// Forty object types is not an achievement. A count is a count: it says
// nothing about whether the fortieth object has an identity anybody can
// resolve, a schema anybody can validate, a hash anybody can recompute,
// or a rule saying who may change it.
//
// So every registered ObjectType must declare nine things:
//
//	object id        how one instance is named, uniquely
//	schema version   which version of the shape this is
//	canonicalization how it is serialized before hashing
//	content hash     what the hash covers
//	provenance       where an instance comes from
//	authority        who may create and change one
//	lifecycle        the states it moves through
//	mutation rule    what may change after creation, and what may not
//	replay behaviour how an instance is reproduced from the record
//
// A type with a blank in any of those is not a canonical object; it is a
// name. ValidateContracts finds them, and the package test fails the
// build rather than letting the count stand in for the contract.

// TypeContract is one object type's nine declarations.
type TypeContract struct {
	Type ObjectType
	// Owner is the package authoritative for instances of this type.
	Owner string

	ObjectID         string
	SchemaVersion    string
	Canonicalization string
	ContentHash      string
	Provenance       string
	Authority        string
	Lifecycle        string
	MutationRule     string
	ReplayBehaviour  string
}

// hashSemantics is the canonicalization every hashed VERIQO object uses.
// Named once so forty rows do not drift into forty descriptions of the
// same thing.
const hashSemantics = "RFC 8785 JCS via pkg/canonical/jcs; the hash field is excluded from its own computation"

// Common lifecycle and mutation shapes, named once for the same reason.
const (
	immutableAfterCreate = "immutable after creation; a change is a new instance with a new id, never an edit"
	immutableAfterSeal   = "mutable until sealed, immutable after; the seal computes the content hash and any later edit breaks it"
	versionedLifecycle   = "DRAFT -> SUBMITTED -> FINALIZED -> (SUPERSEDED); FINALIZED is terminal for mutation"
	appendOnlyLifecycle  = "append-only; entries are never edited or removed, because the sequence is itself evidence"
	replayFromLedger     = "reproduced from the canonical audit ledger; audit.Auditor.VerifyChain re-derives it"
	replayFromHash       = "reproduced by recomputing the content hash from the instance's own components"
)

var typeContracts = []TypeContract{
	// --- The evidence spine -------------------------------------------
	{Type: ObjectEvidence, Owner: "veriqo/pkg/evidence/manifest",
		ObjectID: "EvidenceID, unique per tenant", SchemaVersion: "manifest/v1",
		Canonicalization: hashSemantics, ContentHash: "SHA-256 over the raw bytes as received",
		Provenance: "acquisition record naming source, licence and acquisition path",
		Authority:  "an actor with acquisition authority for the source (pkg/authz)",
		Lifecycle:  versionedLifecycle, MutationRule: immutableAfterCreate,
		ReplayBehaviour: replayFromLedger},
	{Type: ObjectEvidenceVersion, Owner: "veriqo/pkg/evidence/manifest",
		ObjectID: "EvidenceVersionID, unique and never reused", SchemaVersion: "manifest/v1",
		Canonicalization: hashSemantics, ContentHash: "SHA-256 over this version's bytes",
		Provenance: "derivation lineage back to the version it came from",
		Authority:  "the manifest registry; SetStatus and Advance are gated on substantive prerequisites",
		Lifecycle:  versionedLifecycle, MutationRule: "immutable once FINALIZED; the custody chain head freezes with it",
		ReplayBehaviour: "pkg/replay ManifestAdapter restores state from the record"},
	{Type: ObjectFact, Owner: "veriqo/pkg/evidence/semantics",
		ObjectID: "FactID", SchemaVersion: "semantics/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the asserted fact and its evidence version references",
		Provenance:  "the evidence versions the fact is read from", Authority: "the extraction pipeline; no AI may create one",
		Lifecycle: immutableAfterCreate, MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromHash},
	{Type: ObjectDocument, Owner: "veriqo/pkg/evidence/manifest",
		ObjectID: "DocumentID", SchemaVersion: "manifest/v1", Canonicalization: hashSemantics,
		ContentHash: "SHA-256 over the document bytes", Provenance: "the acquisition record naming source, licence and acquisition path",
		Authority: "an actor with acquisition authority for the source (pkg/authz)", Lifecycle: versionedLifecycle,
		MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromLedger},
	{Type: ObjectAttestation, Owner: "veriqo/pkg/platform/timestamp",
		ObjectID: "chain entry hash, or the TSA's authority and serial", SchemaVersion: "timestamp/v1",
		Canonicalization: hashSemantics,
		ContentHash:      "chain entries hash digest+sequence+prior+operator; external tokens are stored verbatim",
		Provenance:       "the chain operator, or the issuing authority's certificate",
		Authority:        "the chain operator for entries; the TSA for tokens. Kind is derived by Assess, never set",
		Lifecycle:        appendOnlyLifecycle, MutationRule: immutableAfterCreate,
		ReplayBehaviour: "timestamp.VerifyChain recomputes every entry and its linkage"},

	// --- Case and resolution ------------------------------------------
	{Type: ObjectCase, Owner: "veriqo/pkg/casefabric",
		ObjectID: "CaseID, scoped by tenant and opening domain", SchemaVersion: "casefabric/v1",
		Canonicalization: hashSemantics, ContentHash: "the timeline head hash covers the case's whole history",
		Provenance:      "the opening actor and domain, recorded in the first timeline entry",
		Authority:       "per-phase, declared in casefabric.PhaseContracts",
		Lifecycle:       "nine canonical phases; every domain state maps onto one",
		MutationRule:    "state advances only through the case engine's methods; the timeline is append-only",
		ReplayBehaviour: "casefabric.VerifyTimeline re-derives the hash chain"},
	{Type: ObjectTimeline, Owner: "veriqo/pkg/casefabric",
		ObjectID: "CaseID plus sequence number", SchemaVersion: "casefabric/v1",
		Canonicalization: hashSemantics, ContentHash: "each entry hashes its content and its predecessor",
		Provenance: "the actor and tick on every entry", Authority: "written only by the case engine",
		Lifecycle: appendOnlyLifecycle, MutationRule: "never edited; a correction is a new entry",
		ReplayBehaviour: "casefabric.VerifyTimeline"},
	{Type: ObjectResolution, Owner: "veriqo/pkg/casefabric",
		ObjectID: "CaseID plus resolution tick", SchemaVersion: "casefabric/v1",
		Canonicalization: hashSemantics, ContentHash: "covered by the timeline entry that records it",
		Provenance: "the resolving authority", Authority: "an authority distinct from the proof author",
		Lifecycle:       "produced once per resolution; cleared on reopening, with the prior record retained",
		MutationRule:    "limitations may be added; established and unestablished claim lists are computed, never supplied",
		ReplayBehaviour: replayFromLedger},
	{Type: ObjectResolutionPackage, Owner: "veriqo/pkg/insurance/casepack",
		ObjectID: "PackageID", SchemaVersion: "casepack/v1", Canonicalization: hashSemantics,
		ContentHash: "manifest hash over every included artefact",
		Provenance:  "the case and the proof objects it is assembled from",
		Authority:   "the case authority", Lifecycle: immutableAfterSeal,
		MutationRule: immutableAfterSeal, ReplayBehaviour: "the package verifies standalone via the independent verifier"},

	// --- Proof and qualification --------------------------------------
	{Type: ObjectProposition, Owner: "veriqo/pkg/proof",
		ObjectID: "PropositionID", SchemaVersion: "proof/v1", Canonicalization: hashSemantics,
		ContentHash: "covered by the proof object's canonical hash",
		Provenance:  "the case and actor that registered it",
		Authority:   "an analyst; a proposition must be falsifiable or registration is refused",
		Lifecycle:   immutableAfterCreate, MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromHash},
	{Type: ObjectProofObject, Owner: "veriqo/pkg/proof",
		ObjectID: "CanonicalHash", SchemaVersion: "proof/v1", Canonicalization: hashSemantics,
		ContentHash:     "JCS hash over all twenty-three components, excluding the hash and signature",
		Provenance:      "Provenance component: generator, pipeline version and pinned input hashes",
		Authority:       "the Authority component, with a pinned policy version",
		Lifecycle:       "created -> sealed -> (externally attested). Levels derive; none is settable",
		MutationRule:    "immutable after sealing; Seal overwrites any author-supplied stance or sufficiency",
		ReplayBehaviour: "proof.VerifyHash recomputes the hash from the components"},
	{Type: ObjectQualification, Owner: "veriqo/pkg/qualification/state",
		ObjectID: "ClaimID plus policy version", SchemaVersion: "qualification/v1",
		Canonicalization: hashSemantics, ContentHash: "covered by the proof object carrying it",
		Provenance: "the rationale, the tick and the evidence set the verdict was reached over", Authority: "the qualification authority under a pinned policy",
		Lifecycle:       "one of ten states; there is no PROVEN state and Parse refuses one by name",
		MutationRule:    "a new qualification supersedes; material dissent is carried, never deleted",
		ReplayBehaviour: "a pure function of its inputs; re-running reproduces the state"},
	{Type: ObjectContradiction, Owner: "veriqo/pkg/insurance/contradiction",
		ObjectID: "ContradictionID", SchemaVersion: "proof/v1", Canonicalization: hashSemantics,
		ContentHash: "covered by the proof object's hash, including its resolved flag",
		Provenance:  "the evidence versions in conflict", Authority: "an authorized human resolves; the system only detects",
		Lifecycle:       "raised -> (resolved). An unresolved material contradiction defeats sufficiency",
		MutationRule:    "the conflict itself is immutable; only the resolution may be added",
		ReplayBehaviour: replayFromHash},
	{Type: ObjectProofObligation, Owner: "veriqo/pkg/qualification/reverseproof",
		ObjectID: "RequirementID within a claim's requirement set", SchemaVersion: "reverseproof/v1",
		Canonicalization: hashSemantics, ContentHash: "covered by the proof object's hash",
		Provenance:      "the claim and condition it derives from",
		Authority:       "the analyst who built the set; a requirement with no falsifying observation is refused",
		Lifecycle:       "unattempted -> obtained | observed-absent | unobtainable",
		MutationRule:    "the expectation is fixed in advance so a later observation cannot be reinterpreted to fit",
		ReplayBehaviour: "reverseproof.Analyze is pure: re-running it over the same set reproduces the gap exactly"},
	{Type: ObjectNextBestEvidence, Owner: "veriqo/pkg/qualification/nextbest",
		ObjectID: "CandidateID", SchemaVersion: "nextbest/v1", Canonicalization: hashSemantics,
		ContentHash:  "covered by the direction's proof hash",
		Provenance:   "the insufficient proof object that produced the direction",
		Authority:    "hard rights gates run before scoring; a denied candidate is excluded, never ranked",
		Lifecycle:    "proposed -> (pursued | excluded). What was not pursued, and why, stays on the record",
		MutationRule: immutableAfterCreate, ReplayBehaviour: "nextbest.Rank is deterministic with an id tie-break: the same candidates reproduce the same ranking"},
	{Type: ObjectHypothesis, Owner: "veriqo/pkg/moat/causal",
		ObjectID: "HypothesisID", SchemaVersion: "causal/v1", Canonicalization: hashSemantics,
		ContentHash:     "hash over the hypothesis and its cited inference trace",
		Provenance:      "pkg/inference.InferenceTrace pins every input",
		Authority:       "intelligence proposes; a hypothesis can never become a finding inside this fabric",
		Lifecycle:       "proposed -> tested. A case cannot resolve with an untested rival",
		MutationRule:    "the statement is immutable; the test outcome is appended",
		ReplayBehaviour: "re-derivable from the same evidence via the trace"},
	{Type: ObjectFinding, Owner: "veriqo/pkg/proof",
		ObjectID: "finding hash", SchemaVersion: "proof/v1", Canonicalization: hashSemantics,
		ContentHash:     "hash over the proof hash, proposition, stance, qualification and limitations",
		Provenance:      "the sealed proof object it derives from, re-verified at construction",
		Authority:       "produced only from a sufficient object; adopted only by an authority who is not the author",
		Lifecycle:       "derived -> authorized. The zero value cannot be authorized",
		MutationRule:    "all fields unexported; every accessor returns a copy",
		ReplayBehaviour: replayFromHash},
	{Type: ObjectDecision, Owner: "veriqo/pkg/proof",
		ObjectID: "decision hash", SchemaVersion: "proof/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the authorized finding, action, rationale and attributes",
		Provenance:  "Lineage() walks decision -> authorized -> finding -> proof object",
		Authority:   "constructible only from an AuthorizedFinding; adjudicatory attributes are refused",
		Lifecycle:   immutableAfterCreate, MutationRule: "Attributes() returns a copy, so none can be added after construction",
		ReplayBehaviour: replayFromLedger},

	// --- Governance and identity --------------------------------------
	{Type: ObjectEvent, Owner: "veriqo/pkg/contract/event",
		ObjectID: "EventID, with sequence number assigned by the chain", SchemaVersion: "event/v1",
		Canonicalization: hashSemantics, ContentHash: "EventHash covers everything but itself and the signature",
		Provenance: "ActorID and ActorType on every envelope",
		Authority:  "Chain.Append assigns sequence and previous hash; a caller cannot supply them",
		Lifecycle:  appendOnlyLifecycle, MutationRule: immutableAfterCreate,
		ReplayBehaviour: "event.VerifyChain is a pure function over the envelopes"},
	{Type: ObjectModel, Owner: "veriqo/pkg/ai/gateway",
		ObjectID: "model id plus version", SchemaVersion: "gateway/v1", Canonicalization: hashSemantics,
		ContentHash: "contribution records hash the prompt and output",
		Provenance:  "provider, version and training permission", Authority: "an allowlist that fails closed when empty",
		Lifecycle: "approved -> (withdrawn)", MutationRule: "a version change is a different model, never an update",
		ReplayBehaviour: "contribution hashes are recomputable from the recorded prompt and output"},
	{Type: ObjectParty, Owner: "veriqo/pkg/insurance/party",
		ObjectID: "PartyID", SchemaVersion: "party/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over identity and role assignments", Provenance: "the onboarding record and the registry extract the identity was verified against",
		Authority: "the participant registry, which is the sole creator of these identities", Lifecycle: "invited -> active -> (suspended | withdrawn)",
		MutationRule: "role changes are events, never in-place edits", ReplayBehaviour: replayFromLedger},
	{Type: ObjectOrganization, Owner: "veriqo/pkg/insurance/party",
		ObjectID: "OrganizationID", SchemaVersion: "party/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over legal identity", Provenance: "the onboarding record and the registry extract the identity was verified against", Authority: "the participant registry, which is the sole creator of these identities",
		Lifecycle: "registered -> (dissolved)", MutationRule: "identity immutable; attributes versioned",
		ReplayBehaviour: replayFromLedger},
	{Type: ObjectPerson, Owner: "veriqo/pkg/insurance/party",
		ObjectID: "PersonID", SchemaVersion: "party/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over identity attributes", Provenance: "the onboarding record and the registry extract the identity was verified against",
		Authority: "the participant registry, which is the sole creator of these identities", Lifecycle: "registered -> (withdrawn)",
		MutationRule:    "identity immutable; attributes versioned. Erasure never edits in place -- it goes through pkg/governance/data",
		ReplayBehaviour: replayFromLedger},

	// --- Contract and obligation --------------------------------------
	{Type: ObjectContract, Owner: "veriqo/pkg/domain/trade",
		ObjectID: "ContractID", SchemaVersion: "trade/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the contract document version", Provenance: "the evidence version it is read from",
		Authority: "the parties; VERIQO records, it does not construe", Lifecycle: versionedLifecycle,
		MutationRule: "an amendment is a new version", ReplayBehaviour: replayFromHash},
	{Type: ObjectClause, Owner: "veriqo/pkg/domain/trade",
		ObjectID: "ContractID plus clause reference", SchemaVersion: "trade/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the clause text and its position", Provenance: "the contract version",
		Authority: "the parties who agreed the contract; VERIQO records the clause and does not construe it", Lifecycle: immutableAfterCreate, MutationRule: immutableAfterCreate,
		ReplayBehaviour: replayFromHash},
	{Type: ObjectObligation, Owner: "veriqo/pkg/insurance/obligation",
		ObjectID: "ObligationID", SchemaVersion: "obligation/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the obligation and its source clause", Provenance: "the contract clause and version the obligation is read from",
		Authority: "the contract; VERIQO identifies, it does not impose", Lifecycle: "open -> (discharged | breached)",
		MutationRule: "the obligation text is immutable; status transitions are appended as events, never edited in place", ReplayBehaviour: replayFromLedger},
	{Type: ObjectBreach, Owner: "veriqo/pkg/insurance/obligation",
		ObjectID: "BreachID", SchemaVersion: "obligation/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the breach and the obligation breached",
		Provenance:  "the evidence and obligation it rests on", Authority: "alleged by a party; established only by a decision-maker",
		Lifecycle:       "alleged -> (established by an authority | not pursued)",
		MutationRule:    "an allegation is immutable; the authority's determination is recorded separately",
		ReplayBehaviour: replayFromLedger},
	{Type: ObjectCounterclaim, Owner: "veriqo/pkg/insurance/dispute",
		ObjectID: "CounterclaimID", SchemaVersion: "dispute/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the counterclaim and its cited evidence", Provenance: "the raising party and the evidence versions they cite",
		Authority: "the party raising it; VERIQO attaches no weight", Lifecycle: "raised -> (withdrawn | determined by an authority)",
		MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromLedger},

	// --- Insurance ----------------------------------------------------
	{Type: ObjectClaim, Owner: "veriqo/pkg/insurance/claim",
		ObjectID: "ClaimID", SchemaVersion: "claim/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the claim and its evidence references", Provenance: "the notifying party and the notification evidence version",
		Authority: "the claimant notifies; the insurer determines", Lifecycle: "notified -> ... -> closed, via casestate's fourteen states",
		MutationRule: "the notified claim is immutable; amount changes are appended as events with their own authority checks", ReplayBehaviour: replayFromLedger},
	{Type: ObjectPolicy, Owner: "veriqo/pkg/insurance/coverage",
		ObjectID: "PolicyID", SchemaVersion: "coverage/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the policy wording version", Provenance: "the policy document evidence version the wording is read from",
		Authority: "the insurer, whose wording it is; VERIQO does not construe it", Lifecycle: versionedLifecycle, MutationRule: "an endorsement is a new version",
		ReplayBehaviour: replayFromHash},
	{Type: ObjectLoss, Owner: "veriqo/pkg/insurance/quantum",
		ObjectID: "LossID", SchemaVersion: "quantum/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the loss and its evidence backing", Provenance: "the evidence-backed amounts it aggregates",
		Authority: "computed, never asserted; every amount cites its evidence", Lifecycle: "estimated -> quantified -> (settled)",
		MutationRule: "a revision is a new calculation with its own id", ReplayBehaviour: replayFromHash},
	{Type: ObjectQuantum, Owner: "veriqo/pkg/insurance/quantum",
		ObjectID: "CalculationID", SchemaVersion: "quantum/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the calculation inputs and result", Provenance: "the evidence-backed inputs",
		Authority: "deterministic computation; no discretionary adjustment without an authority record",
		Lifecycle: immutableAfterCreate, MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromHash},
	{Type: ObjectCausation, Owner: "veriqo/pkg/insurance/causation",
		ObjectID: "HypothesisID within a hypothesis set", SchemaVersion: "causation/v1",
		Canonicalization: hashSemantics, ContentHash: "hash over the causal chain and its supporting evidence",
		Provenance: "the evidence versions and inference traces the chain is built from", Authority: "proposed by intelligence; never self-promoting to a finding",
		Lifecycle:       "proposed -> tested -> (supported | excluded)",
		MutationRule:    "the chain is immutable; status transitions are recorded",
		ReplayBehaviour: replayFromHash},
	{Type: ObjectResponsibility, Owner: "veriqo/pkg/insurance/causation",
		ObjectID: "ResponsibilityID", SchemaVersion: "causation/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the attribution and its basis", Provenance: "the causal hypothesis it rests on",
		Authority:    "attributed as an analytical position, never as a legal determination",
		Lifecycle:    "attributed -> (accepted | disputed | determined by an authority)",
		MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromLedger},

	// --- Maritime and logistics ---------------------------------------
	{Type: ObjectVessel, Owner: "veriqo/pkg/domain/maritime",
		ObjectID: "IMO number where known, otherwise a VERIQO vessel id", SchemaVersion: "maritime/v1",
		Canonicalization: hashSemantics, ContentHash: "hash over identity attributes at a point in time",
		Provenance:   "the registry or source the identity is read from",
		Authority:    "identity resolution below threshold stays unresolved rather than merging two vessels",
		Lifecycle:    "registered -> (renamed | reflagged | scrapped), each a new version",
		MutationRule: "identity attributes are versioned, never overwritten", ReplayBehaviour: replayFromHash},
	{Type: ObjectVoyage, Owner: "veriqo/pkg/domain/maritime",
		ObjectID: "VoyageID", SchemaVersion: "maritime/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the voyage and its port calls", Provenance: "the position and port-call sources",
		Authority: "constructed from evidence; a gap in coverage is an observability question, not an inference",
		Lifecycle: "planned -> underway -> completed", MutationRule: "corrections are new versions",
		ReplayBehaviour: replayFromHash},
	{Type: ObjectPort, Owner: "veriqo/pkg/domain/maritime",
		ObjectID: "UN/LOCODE", SchemaVersion: "maritime/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the port reference data version", Provenance: "the reference source and the version of it that was loaded",
		Authority: "reference data, not evidence; used to resolve, never to establish",
		Lifecycle: versionedLifecycle, MutationRule: immutableAfterCreate, ReplayBehaviour: replayFromHash},
	{Type: ObjectCargo, Owner: "veriqo/pkg/domain/commodity",
		ObjectID: "CargoID", SchemaVersion: "commodity/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over description, quantity and quality attributes", Provenance: "the shipping documents the declaration is read from",
		Authority: "declared by the shipper; assay results are separate evidence",
		Lifecycle: "declared -> loaded -> discharged", MutationRule: "a re-declaration is a new version",
		ReplayBehaviour: replayFromHash},
	{Type: ObjectShipment, Owner: "veriqo/pkg/domain/supplychain",
		ObjectID: "ShipmentID", SchemaVersion: "supplychain/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the shipment and its leg references", Provenance: "the transport documents the shipment is constructed from",
		Authority: "the parties to the carriage, whose declarations VERIQO records", Lifecycle: "booked -> in transit -> delivered",
		MutationRule: "each leg is appended", ReplayBehaviour: replayFromLedger},
	{Type: ObjectTransaction, Owner: "veriqo/pkg/domain/trade",
		ObjectID: "TransactionID", SchemaVersion: "trade/v1", Canonicalization: hashSemantics,
		ContentHash: "hash over the transaction terms", Provenance: "the instrument or instruction it derives from",
		Authority: "the transacting parties", Lifecycle: "instructed -> settled -> (reversed)",
		MutationRule:    "a reversal is a new transaction referencing the original",
		ReplayBehaviour: replayFromLedger},
}

// TypeContracts returns the declared contract for every canonical
// object type.
func TypeContracts() []TypeContract { return append([]TypeContract(nil), typeContracts...) }

// ContractFor returns one type's contract.
func ContractForType(t ObjectType) (TypeContract, bool) {
	for _, c := range typeContracts {
		if c.Type == t {
			return c, true
		}
	}
	return TypeContract{}, false
}

// Incomplete returns the declarations this contract leaves blank.
func (c TypeContract) Incomplete() []string {
	var missing []string
	for _, f := range []struct {
		name  string
		value string
	}{
		{"owner", c.Owner}, {"object id", c.ObjectID}, {"schema version", c.SchemaVersion},
		{"canonicalization", c.Canonicalization}, {"content hash", c.ContentHash},
		{"provenance", c.Provenance}, {"authority", c.Authority},
		{"lifecycle", c.Lifecycle}, {"mutation rule", c.MutationRule},
		{"replay behaviour", c.ReplayBehaviour},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	sort.Strings(missing)
	return missing
}

// ValidateObjectContracts checks that every registered object type has a
// complete contract, and that no contract describes a type nobody
// registered.
//
// This is what turns "forty canonical objects" from a count into a
// claim. A type without a contract is a name; a contract without a type
// is documentation of something that does not exist.
func ValidateObjectContracts() error {
	declared := map[ObjectType]bool{}
	for _, c := range typeContracts {
		if declared[c.Type] {
			return fmt.Errorf("ontology: object type %q has more than one contract", c.Type)
		}
		declared[c.Type] = true

		if !IsKnownObjectType(c.Type) {
			return fmt.Errorf("ontology: contract declared for unregistered object type %q", c.Type)
		}
		if missing := c.Incomplete(); len(missing) > 0 {
			return fmt.Errorf("ontology: object type %q declares no %s", c.Type, strings.Join(missing, ", "))
		}
	}
	var undeclared []string
	for _, t := range KnownObjectTypes() {
		if !declared[t] {
			undeclared = append(undeclared, string(t))
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return fmt.Errorf("ontology: %d object type(s) have no contract and are therefore names, not canonical objects: %s",
			len(undeclared), strings.Join(undeclared, ", "))
	}
	return nil
}

// RenderObjectContracts writes the contract table.
func RenderObjectContracts() string {
	rows := TypeContracts()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Type < rows[j].Type })
	var b strings.Builder
	for _, c := range rows {
		b.WriteString(fmt.Sprintf("=== %s (%s) ===\n", c.Type, c.Owner))
		for _, f := range [][2]string{
			{"OBJECT ID", c.ObjectID}, {"SCHEMA VERSION", c.SchemaVersion},
			{"CANONICALIZATION", c.Canonicalization}, {"CONTENT HASH", c.ContentHash},
			{"PROVENANCE", c.Provenance}, {"AUTHORITY", c.Authority},
			{"LIFECYCLE", c.Lifecycle}, {"MUTATION RULE", c.MutationRule},
			{"REPLAY", c.ReplayBehaviour},
		} {
			b.WriteString(fmt.Sprintf("  %-18s %s\n", f[0], f[1]))
		}
		b.WriteString("\n")
	}
	return b.String()
}
