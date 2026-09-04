package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// The authority quintuple.
//
// A canonical object contract already said what an object CONTAINS and
// where it CAME FROM. The review pointed out that neither answers the
// question that actually decides a dispute:
//
//	Who is allowed to say this?
//
// and that the answer is not the same as provenance. Provenance says a
// document arrived from Lloyd's List under a licence. It does not say
// who may declare that the document establishes causation. A system
// that conflates the two ends up treating "we got this from a good
// source" as "we are entitled to conclude this", which is the error
// the whole architecture exists to prevent.
//
// So every canonical object declares five dimensions:
//
//	TYPE     what kind of authority this is
//	SUBJECT  who holds it
//	BASIS    what confers it
//	SCOPE    what it reaches
//	TIME     when it may be exercised
//
// The five are not decoration on the free-text Authority field. They
// are separable questions with different answers, and a dispute
// usually turns on exactly one of them: not "was somebody allowed",
// but "was THIS person allowed, under THAT policy, over THIS field,
// at THAT moment".

// AuthorityType is the kind of authority a declaration confers.
type AuthorityType string

const (
	// AuthorityAcquisition: the right to bring something into VERIQO
	// from outside. It says nothing about what may be concluded.
	AuthorityAcquisition AuthorityType = "ACQUISITION"
	// AuthorityRegistry: the right to move an object through its
	// lifecycle. A registry gate is not an opinion about content.
	AuthorityRegistry AuthorityType = "REGISTRY"
	// AuthorityDerivation: the right to compute a new object from
	// existing ones by a fixed rule. Derivation adds no judgement,
	// which is why it can be delegated to code.
	AuthorityDerivation AuthorityType = "DERIVATION"
	// AuthorityEpistemic: the right to say what the evidence supports.
	// This is the scarce one.
	AuthorityEpistemic AuthorityType = "EPISTEMIC"
	// AuthorityAdjudicative: the right to adopt a conclusion and act
	// on it. Held by a person, never by the system.
	AuthorityAdjudicative AuthorityType = "ADJUDICATIVE"
	// AuthorityCustodial: the right to hold and hand over, without any
	// right to alter or interpret.
	AuthorityCustodial AuthorityType = "CUSTODIAL"
	// AuthorityDeclaratory: the right to state a fact about the world
	// that VERIQO records rather than determines -- a vessel's IMO
	// number, a policy's wording. VERIQO is not the source of truth.
	AuthorityDeclaratory AuthorityType = "DECLARATORY"
)

// AuthorityTypes returns every kind, in the order above.
func AuthorityTypes() []AuthorityType {
	return []AuthorityType{
		AuthorityAcquisition, AuthorityRegistry, AuthorityDerivation,
		AuthorityEpistemic, AuthorityAdjudicative, AuthorityCustodial,
		AuthorityDeclaratory,
	}
}

// Valid reports whether the type is one of the seven.
func (a AuthorityType) Valid() bool {
	for _, k := range AuthorityTypes() {
		if k == a {
			return true
		}
	}
	return false
}

// AuthorityDecl is one object type's answer to "who is allowed to say
// this?", in five separable parts.
type AuthorityDecl struct {
	Type AuthorityType
	// Subject is who holds the authority: a role, an actor class, or a
	// named package where the authority is exercised by code under a
	// fixed rule.
	Subject string
	// Basis is what confers it: a policy version, a registry gate, a
	// type constraint, a constitutional article, an external mandate.
	// "Because the code allows it" is not a basis and is refused.
	Basis string
	// Scope is what the authority reaches -- which fields, which
	// objects, which phases. An unbounded authority is not an
	// authority, it is an owner.
	Scope string
	// Time is when it may be exercised, expressed against the object's
	// lifecycle rather than the clock. Most VERIQO authorities are
	// bounded by a gate: before sealing, after qualification, only
	// while the case is open.
	Time string
}

// Validate refuses an incomplete declaration.
//
// Every field is required. A declaration missing its Basis is the most
// dangerous shape available here: it reads as an authority while
// recording no reason anyone has it, and it is the form a permission
// takes when it grew out of an implementation detail.
func (d AuthorityDecl) Validate() error {
	if !d.Type.Valid() {
		return fmt.Errorf("ontology: %q is not a canonical authority type", d.Type)
	}
	for _, f := range []struct{ name, value string }{
		{"SUBJECT", d.Subject}, {"BASIS", d.Basis},
		{"SCOPE", d.Scope}, {"TIME", d.Time},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("ontology: authority declaration is missing %s", f.name)
		}
	}
	// A basis that points at the implementation is not a basis.
	lower := strings.ToLower(d.Basis)
	for _, weasel := range []string{"the code allows", "it is possible to", "no restriction", "by convention only"} {
		if strings.Contains(lower, weasel) {
			return fmt.Errorf("ontology: %q is not a basis for authority, it is a description of the implementation", d.Basis)
		}
	}
	return nil
}

// String renders the quintuple on one line.
func (d AuthorityDecl) String() string {
	return fmt.Sprintf("%s | subject=%s | basis=%s | scope=%s | time=%s",
		d.Type, d.Subject, d.Basis, d.Scope, d.Time)
}

// Named bases and times reused across objects, so that forty rows do
// not drift into forty phrasings of the same rule.
const (
	basisAuthz          = "pkg/authz grants acquisition authority per source, under a pinned policy version"
	basisRegistryGate   = "the owning registry's transition gate, which checks substantive prerequisites rather than a flag"
	basisTypeConstraint = "a type constraint: the fields are unexported and the only constructor applies the rule"
	basisDomainRecord   = "the external record or party that determines the fact; VERIQO records it and does not determine it"
	basisPolicyVersion  = "a pinned policy version, so the same question asked of a historical case gets the historical answer"

	timeBeforeSeal      = "until the object is sealed; after sealing the content hash makes any change detectable"
	timeBeforeFinalized = "until the version reaches FINALIZED, which is terminal for mutation"
	timeAppendOnly      = "at append time only; an appended entry is never revisited"
	timeCaseOpen        = "while the case has not resolved; the post-resolution mutation ban closes it"
	timeAnyTime         = "at any time; the object records a fact about the world rather than a step in a case"
)

// authorityDecls is the quintuple for every canonical object type.
//
// The table is separate from typeContracts on purpose: provenance and
// authority answer different questions and keeping them in one row
// invites the reader to skim them as one thing.
var authorityDecls = map[ObjectType]AuthorityDecl{
	// --- The evidence spine ------------------------------------------
	ObjectEvidence: {
		Type: AuthorityAcquisition, Subject: "an actor holding acquisition authority for the named source",
		Basis: basisAuthz, Scope: "bringing the item in and recording its acquisition record; nothing about what it establishes",
		Time: "at acquisition; the acquisition record is then immutable",
	},
	ObjectEvidenceVersion: {
		Type: AuthorityRegistry, Subject: "veriqo/pkg/evidence/manifest.Registry",
		Basis: basisRegistryGate, Scope: "the version's lifecycle status and its supersession link; never its bytes",
		Time: timeBeforeFinalized,
	},
	ObjectFact: {
		Type: AuthorityDerivation, Subject: "veriqo/pkg/evidence/semantics extraction pipeline",
		Basis: "a deterministic extraction rule over pinned evidence versions; an AI may propose, never create",
		Scope: "asserting that a fact appears in a named version; not that the fact is true",
		Time:  "after the source version is FINALIZED, so the extraction cites a stable subject",
	},
	ObjectDocument: {
		Type: AuthorityAcquisition, Subject: "an actor holding acquisition authority for the named source",
		Basis: basisAuthz, Scope: "the document's bytes and acquisition record", Time: "at acquisition only",
	},
	ObjectAttestation: {
		Type: AuthorityCustodial, Subject: "the chain operator for entries; an external TSA for tokens",
		Basis: "for a token, the TSA's independence from the matter; for a chain entry, nothing but VERIQO's own record",
		Scope: "asserting existence before a time; never the content's truth",
		Time:  "at the moment of attestation; Kind is derived by Assess and cannot be set by a caller",
	},

	// --- The case spine -----------------------------------------------
	ObjectCase: {
		Type: AuthorityRegistry, Subject: "veriqo/pkg/casefabric, per phase",
		Basis: "casefabric.PhaseContracts, which state the entry and exit conditions of each phase",
		Scope: "the case's phase and the entries on its timeline", Time: "per phase, as the contract for that phase allows",
	},
	ObjectTimeline: {
		Type: AuthorityCustodial, Subject: "the case engine",
		Basis: "append-only construction: the sequence is itself evidence, so an edit destroys what it records",
		Scope: "appending an entry", Time: timeAppendOnly,
	},
	ObjectResolution: {
		Type: AuthorityAdjudicative, Subject: "a resolving authority distinct from the proof author",
		Basis: "casefabric.ResolutionGate, which consumes an authorized decision and a closed reverse proof",
		Scope: "the disposition and summary, within the domain's declared outcome vocabulary; never a prevailing party",
		Time:  "once, when every gate holds; the case is then closed to epistemic change",
	},
	ObjectResolutionPackage: {
		Type: AuthorityDerivation, Subject: "veriqo/pkg/insurance/casepack",
		Basis: "assembly from a resolved case and its sealed proof objects, by a fixed rule",
		Scope: "arrangement and presentation; the package adds no conclusion of its own", Time: "after the case resolves",
	},

	// --- The proof spine ----------------------------------------------
	ObjectProposition: {
		Type: AuthorityEpistemic, Subject: "an analyst",
		Basis: "falsifiability: registration is refused for a proposition nothing could disprove",
		Scope: "stating what is to be established", Time: timeCaseOpen,
	},
	ObjectProofObject: {
		Type: AuthorityEpistemic, Subject: "veriqo/pkg/proof.Seal, over components each supplied by their own authority",
		Basis: "the sufficiency rule, computed in exactly one function and overwriting any author-supplied value",
		Scope: "stance and sufficiency for one proposition on one case", Time: timeBeforeSeal,
	},
	ObjectQualification: {
		Type: AuthorityEpistemic, Subject: "veriqo/pkg/qualification/state.New",
		Basis: basisPolicyVersion, Scope: "one claim's epistemic state; there is no PROVEN state to reach",
		Time: "at assessment; a later assessment is a new record, not an edit",
	},
	ObjectContradiction: {
		Type: AuthorityEpistemic, Subject: "the system detects; an authorized human resolves",
		Basis: "Article 11: dissent is carried through qualification and never deleted",
		Scope: "recording that two evidence versions conflict", Time: "on detection; the record then persists whatever is decided",
	},
	ObjectProofObligation: {
		Type: AuthorityEpistemic, Subject: "veriqo/pkg/qualification/reverseproof.Analyze",
		Basis: "the reverse direction of the execution fabric: what would have to be shown for the claim to hold",
		Scope: "the requirement set and its gaps", Time: "before qualification begins, because it gates qualification",
	},
	ObjectNextBestEvidence: {
		Type: AuthorityDerivation, Subject: "veriqo/pkg/qualification/nextbest",
		Basis: "a fixed rule over an insufficient proof object's unmet requirements",
		Scope: "naming what would close the gap; not asserting it exists or can be obtained",
		Time:  "after a proof object is found insufficient",
	},
	ObjectHypothesis: {
		Type: AuthorityEpistemic, Subject: "an analyst, or an AI acting as a proposer",
		Basis: "Article 8: AI may propose a hypothesis and may not create, alter, qualify or sign evidence",
		Scope: "a rival explanation to be tested", Time: timeCaseOpen,
	},
	ObjectFinding: {
		Type: AuthorityEpistemic, Subject: "veriqo/pkg/proof.NewFinding, and nothing else in the module",
		Basis: basisTypeConstraint + "; the authority path is recorded on the finding and covered by its hash",
		Scope: "exactly one proof object and exactly one case", Time: "after the proof object is sealed and found sufficient",
	},
	ObjectDecision: {
		Type: AuthorityAdjudicative, Subject: "a named authorizer who did not generate the proof object",
		Basis: "proof.Authorize's lineage check, then proof.Decide's adjudication guard",
		Scope: "adopting a finding and naming an operational action; never naming a prevailing party",
		Time:  "after a finding exists; the decision is immutable once made",
	},

	// --- Platform ------------------------------------------------------
	ObjectEvent: {
		Type: AuthorityCustodial, Subject: "the emitting component, through the canonical envelope",
		Basis: "the closed 25-family taxonomy: an event type outside it is refused",
		Scope: "recording that something happened", Time: timeAppendOnly,
	},
	ObjectModel: {
		Type: AuthorityDeclaratory, Subject: "the model registry",
		Basis: "Article 27: material AI contribution is recorded and human-reviewed",
		Scope: "identifying which model ran, at which version", Time: "at invocation",
	},

	// --- Parties --------------------------------------------------------
	ObjectParty: {
		Type: AuthorityDeclaratory, Subject: "the onboarding actor, against an external register",
		Basis: basisDomainRecord, Scope: "identity and role in a matter", Time: timeAnyTime,
	},
	ObjectOrganization: {
		Type: AuthorityDeclaratory, Subject: "the onboarding actor, against a company register",
		Basis: basisDomainRecord, Scope: "legal identity", Time: timeAnyTime,
	},
	ObjectPerson: {
		Type: AuthorityDeclaratory, Subject: "the onboarding actor",
		Basis: basisDomainRecord, Scope: "identity and authorisation to act", Time: timeAnyTime,
	},

	// --- Trade and obligation --------------------------------------------
	ObjectContract: {
		Type: AuthorityDeclaratory, Subject: "the contracting parties",
		Basis: basisDomainRecord, Scope: "the terms as executed; VERIQO does not interpret them", Time: timeAnyTime,
	},
	ObjectClause: {
		Type: AuthorityDeclaratory, Subject: "the contracting parties",
		Basis: basisDomainRecord, Scope: "the clause text and its position in the contract", Time: timeAnyTime,
	},
	ObjectObligation: {
		Type: AuthorityDerivation, Subject: "veriqo/pkg/insurance/obligation",
		Basis: "a fixed reading of a clause into an obligation with a due condition",
		Scope: "what the clause requires; not whether it was met", Time: "after the contract is recorded",
	},
	ObjectBreach: {
		Type: AuthorityEpistemic, Subject: "an analyst, on evidence",
		Basis: "an obligation, a due condition and evidence that the condition was not met",
		Scope: "asserting non-performance; never assigning legal liability", Time: timeCaseOpen,
	},
	ObjectCounterclaim: {
		Type: AuthorityDeclaratory, Subject: "the party advancing it",
		Basis: "the party's own assertion, recorded as theirs", Scope: "what that party claims", Time: timeCaseOpen,
	},
	ObjectClaim: {
		Type: AuthorityDeclaratory, Subject: "the claimant",
		Basis: "the claimant's own submission under the policy", Scope: "what is claimed and on what basis", Time: timeAnyTime,
	},
	ObjectPolicy: {
		Type: AuthorityDeclaratory, Subject: "the insurer, through the policy wording",
		Basis: basisDomainRecord, Scope: "cover, exclusions and limits as written", Time: timeAnyTime,
	},
	ObjectLoss: {
		Type: AuthorityEpistemic, Subject: "an adjuster or surveyor",
		Basis: "observation or survey, recorded with its method", Scope: "what was lost or damaged", Time: timeCaseOpen,
	},
	ObjectQuantum: {
		Type: AuthorityDerivation, Subject: "veriqo/pkg/insurance/quantum",
		Basis: "a stated valuation method over a recorded loss",
		Scope: "the computed amount and its method; not whether it is payable", Time: "after the loss is recorded",
	},
	ObjectCausation: {
		Type: AuthorityEpistemic, Subject: "an analyst, through the causal model",
		Basis: "a causal model whose rival explanations were tested and excluded",
		Scope: "asserting a causal link between events", Time: "after rival hypotheses are tested",
	},
	ObjectResponsibility: {
		Type: AuthorityEpistemic, Subject: "an analyst",
		Basis: "Article 16: the platform does not determine legal liability, so this is factual responsibility only",
		Scope: "who did what; never who is legally liable", Time: timeCaseOpen,
	},

	// --- Domain objects ---------------------------------------------------
	ObjectVessel: {
		Type: AuthorityDeclaratory, Subject: "the flag state and classification society, through their registers",
		Basis: basisDomainRecord, Scope: "vessel identity and particulars", Time: timeAnyTime,
	},
	ObjectVoyage: {
		Type: AuthorityDeclaratory, Subject: "the operator, through voyage records",
		Basis: basisDomainRecord, Scope: "the voyage as declared and observed", Time: timeAnyTime,
	},
	ObjectPort: {
		Type: AuthorityDeclaratory, Subject: "the port authority",
		Basis: basisDomainRecord, Scope: "port identity and location", Time: timeAnyTime,
	},
	ObjectCargo: {
		Type: AuthorityDeclaratory, Subject: "the shipper and the surveyor",
		Basis: basisDomainRecord, Scope: "description, quantity and condition as declared or surveyed", Time: timeAnyTime,
	},
	ObjectShipment: {
		Type: AuthorityDeclaratory, Subject: "the carrier, through transport documents",
		Basis: basisDomainRecord, Scope: "the movement as documented", Time: timeAnyTime,
	},
	ObjectTransaction: {
		Type: AuthorityDeclaratory, Subject: "the transacting parties and their bank",
		Basis: basisDomainRecord, Scope: "the transaction as recorded", Time: timeAnyTime,
	},
}

// AuthorityOf returns the quintuple for an object type.
func AuthorityOf(t ObjectType) (AuthorityDecl, bool) {
	d, ok := authorityDecls[t]
	return d, ok
}

// ValidateAuthorities checks that every canonical object type declares a
// complete quintuple, and that no type declares one without being in the
// contract table.
func ValidateAuthorities() error {
	var missing, orphan []string
	declared := map[ObjectType]bool{}
	for _, c := range typeContracts {
		declared[c.Type] = true
		d, ok := authorityDecls[c.Type]
		if !ok {
			missing = append(missing, string(c.Type))
			continue
		}
		if err := d.Validate(); err != nil {
			return fmt.Errorf("ontology: object %s: %w", c.Type, err)
		}
	}
	for t := range authorityDecls {
		if !declared[t] {
			orphan = append(orphan, string(t))
		}
	}
	sort.Strings(missing)
	sort.Strings(orphan)
	if len(missing) > 0 {
		return fmt.Errorf("ontology: %d object type(s) declare no authority quintuple: %s",
			len(missing), strings.Join(missing, ", "))
	}
	if len(orphan) > 0 {
		return fmt.Errorf("ontology: %d authority quintuple(s) name no canonical object: %s",
			len(orphan), strings.Join(orphan, ", "))
	}
	return nil
}

// AuthorityReport renders the quintuple for every object, grouped by
// authority type.
func AuthorityReport() string {
	var b strings.Builder
	b.WriteString("VERIQO authority quintuple\n")
	b.WriteString("Who is allowed to say this? -- TYPE, SUBJECT, BASIS, SCOPE, TIME.\n")
	b.WriteString("Provenance answers a different question and is recorded separately.\n\n")

	for _, kind := range AuthorityTypes() {
		var types []string
		for t, d := range authorityDecls {
			if d.Type == kind {
				types = append(types, string(t))
			}
		}
		if len(types) == 0 {
			continue
		}
		sort.Strings(types)
		fmt.Fprintf(&b, "%s (%d object(s))\n", kind, len(types))
		for _, t := range types {
			d := authorityDecls[ObjectType(t)]
			fmt.Fprintf(&b, "  %s\n", t)
			fmt.Fprintf(&b, "      subject: %s\n", d.Subject)
			fmt.Fprintf(&b, "      basis:   %s\n", d.Basis)
			fmt.Fprintf(&b, "      scope:   %s\n", d.Scope)
			fmt.Fprintf(&b, "      time:    %s\n", d.Time)
		}
		b.WriteString("\n")
	}
	return b.String()
}
