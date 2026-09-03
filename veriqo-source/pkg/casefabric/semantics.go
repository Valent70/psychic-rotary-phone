package casefabric

import (
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/ontology"
)

// Domain semantics.
//
// Six domains projecting onto one case spine is only worth having if
// each domain actually says something. A projection that maps states and
// stops has told you where the domain sits in the lifecycle and nothing
// about what the domain is for.
//
// So each domain declares five things:
//
//	ontology           the canonical object types it works in
//	evidence classes   what counts as evidence here, and from where
//	proof obligations  what this domain's claims typically must show
//	rules              the domain's own constraints on the shared fabric
//	outcome vocabulary how a case in this domain ends
//
// All five are DATA. That is the load-bearing constraint. A domain that
// needed its own engine to express its semantics would be a domain that
// had forked the fabric, which is the failure the whole architecture is
// arranged to prevent. Semantics declared as data project onto the
// shared machinery; semantics declared as code become a rival to it.
//
// TestDomainSemanticsAreDataNotEngines holds that line.

// EvidenceClass is a kind of evidence a domain works with, and where it
// comes from.
type EvidenceClass struct {
	Name string
	// Source names the real-world origin. This is the field that makes
	// the LIVE_DATA blocker concrete: every one of these is currently a
	// fixture, and naming the real source is what says so.
	Source string
	// Independence records whether the class is typically acquired
	// independently of the parties, or mediated by one of them. A
	// party-mediated class is not thereby worthless — it is thereby
	// not independent, and the difference must be visible.
	PartyMediated bool
}

// ProofObligation is a claim shape this domain routinely has to
// establish, and what would falsify it.
type ProofObligation struct {
	Claim string
	// Requires is what must be shown.
	Requires []string
	// FalsifiedBy is what would defeat it. An obligation with no
	// falsifier is not a test, and Validate refuses one.
	FalsifiedBy string
}

// Semantics is one domain's full declaration.
type Semantics struct {
	Domain string
	// ObjectTypes are the canonical types this domain works in. Every
	// one must be registered: a domain working in an unregistered type
	// is working outside the ontology.
	ObjectTypes     []ontology.ObjectType
	EvidenceClasses []EvidenceClass
	Obligations     []ProofObligation
	// Rules are the domain's own constraints, expressed against the
	// shared fabric rather than as domain code.
	Rules []string
	// OutcomeVocabulary is how a case in this domain ends. No entry may
	// adjudicate.
	OutcomeVocabulary []string
}

var domainSemantics = []Semantics{
	{
		Domain: DomainMaritime,
		ObjectTypes: []ontology.ObjectType{
			ontology.ObjectVessel, ontology.ObjectVoyage, ontology.ObjectPort,
			ontology.ObjectEvent, ontology.ObjectCausation, ontology.ObjectLoss,
		},
		EvidenceClasses: []EvidenceClass{
			{Name: "AIS position track", Source: "AIS aggregator (terrestrial and satellite)"},
			{Name: "SAR imagery", Source: "synthetic-aperture radar provider"},
			{Name: "optical EO imagery", Source: "earth-observation provider"},
			{Name: "port call record", Source: "port authority or agent"},
			{Name: "weather hindcast", Source: "meteorological service"},
			{Name: "vessel log extract", Source: "the operator", PartyMediated: true},
			{Name: "class survey report", Source: "classification society"},
		},
		Obligations: []ProofObligation{
			{Claim: "the vessel deviated from its declared route",
				Requires:    []string{"a declared route", "a position track covering the window", "an observability verdict for any gap in the track"},
				FalsifiedBy: "a position track within the declared corridor for the whole window"},
			{Claim: "a dark period was deliberate rather than a coverage gap",
				Requires:    []string{"coverage adequacy for the window", "an OBSERVED_ABSENT verdict, not an unattempted one", "a rival explanation tested"},
				FalsifiedBy: "a coverage gap in the source that explains the absence without any act of the vessel"},
		},
		Rules: []string{
			"an AIS gap is an absence, and an absence carries weight only after the nine-condition observability gate",
			"vessel identity below the resolution threshold stays unresolved rather than merging two vessels",
			"position from a single provider is a single source however many feeds it aggregates",
		},
		OutcomeVocabulary: []string{"findings_issued", "evidence_package_delivered", "no_further_action", "referred_to_authority"},
	},
	{
		Domain: DomainCommodity,
		ObjectTypes: []ontology.ObjectType{
			ontology.ObjectCargo, ontology.ObjectShipment, ontology.ObjectDocument,
			ontology.ObjectLoss, ontology.ObjectQuantum, ontology.ObjectCausation,
		},
		EvidenceClasses: []EvidenceClass{
			{Name: "independent assay certificate", Source: "accredited laboratory"},
			{Name: "draft survey report", Source: "appointed surveyor"},
			{Name: "loading tally", Source: "the terminal", PartyMediated: true},
			{Name: "sealed sample chain of custody", Source: "the sampling party", PartyMediated: true},
			{Name: "temperature and humidity log", Source: "container telemetry provider"},
			{Name: "bill of lading", Source: "the carrier", PartyMediated: true},
		},
		Obligations: []ProofObligation{
			{Claim: "the cargo was off-specification before loading",
				Requires:    []string{"a pre-load sample analysed by an independent laboratory", "an unbroken sample chain of custody", "the in-transit hypothesis tested"},
				FalsifiedBy: "a clean pre-load sample from a properly sealed and custodied specimen"},
			{Claim: "the shortage arose in carriage rather than at loading",
				Requires:    []string{"a load-port figure", "a discharge-port figure", "a stated measurement tolerance for both"},
				FalsifiedBy: "a discrepancy within the combined measurement tolerance of the two surveys"},
		},
		Rules: []string{
			"an assay from a laboratory a party appointed is party-mediated, and independence is assessed accordingly",
			"quantity claims are never established from a single measurement without a stated tolerance",
			"a sealed sample with a broken custody chain is evidence of the break, not of the contents",
		},
		OutcomeVocabulary: []string{"quality_determined", "evidence_package_delivered", "no_further_action", "referred_to_arbitration"},
	},
	{
		Domain: DomainSupplyChain,
		ObjectTypes: []ontology.ObjectType{
			ontology.ObjectShipment, ontology.ObjectOrganization, ontology.ObjectEvent,
			ontology.ObjectBreach, ontology.ObjectResponsibility, ontology.ObjectTimeline,
		},
		EvidenceClasses: []EvidenceClass{
			{Name: "customs declaration", Source: "the customs authority"},
			{Name: "sanctions and screening list", Source: "the issuing authority"},
			{Name: "carrier milestone feed", Source: "the carrier", PartyMediated: true},
			{Name: "supplier audit report", Source: "an appointed auditor"},
			{Name: "certificate of origin", Source: "the issuing chamber or authority"},
			{Name: "purchase order and contract", Source: "the transacting parties", PartyMediated: true},
		},
		Obligations: []ProofObligation{
			{Claim: "the disruption originated at a named tier-2 supplier",
				Requires:    []string{"a traced chain from the disruption to that tier", "the intermediate tiers excluded", "an alternative origin tested"},
				FalsifiedBy: "an intermediate tier that independently accounts for the disruption"},
			{Claim: "the consignment involves a sanctioned party",
				Requires:    []string{"the list version in force at the material time", "an identity match above the resolution threshold", "the beneficial-ownership chain where the listing reaches it"},
				FalsifiedBy: "a name match that fails identity resolution, which is a coincidence of names"},
		},
		Rules: []string{
			"a sanctions name match is a hypothesis until identity resolution clears the threshold; it is never a finding on its own",
			"the list version in force at the material time governs, not today's list -- Article 7",
			"a carrier's own milestone feed is party-mediated and is not independent corroboration of the carrier's performance",
		},
		OutcomeVocabulary: []string{"origin_established", "evidence_package_delivered", "no_further_action", "referred_to_authority"},
	},
	{
		Domain: DomainInsurance,
		ObjectTypes: []ontology.ObjectType{
			ontology.ObjectClaim, ontology.ObjectPolicy, ontology.ObjectLoss,
			ontology.ObjectQuantum, ontology.ObjectCausation, ontology.ObjectObligation,
		},
		EvidenceClasses: []EvidenceClass{
			{Name: "policy wording and endorsements", Source: "the insurer", PartyMediated: true},
			{Name: "loss adjuster report", Source: "an appointed adjuster"},
			{Name: "P&I club correspondence", Source: "the club", PartyMediated: true},
			{Name: "repair invoice and quotation", Source: "the repairer"},
			{Name: "survey report", Source: "an appointed surveyor"},
			{Name: "notice of claim", Source: "the claimant", PartyMediated: true},
		},
		Obligations: []ProofObligation{
			{Claim: "the loss falls within the policy's insured perils",
				Requires:    []string{"the policy version in force at the loss", "the proximate cause established", "each exclusion considered and addressed"},
				FalsifiedBy: "an exclusion that applies on the established facts"},
			{Claim: "the quantum is the sum claimed",
				Requires:    []string{"an evidence-backed amount for every component", "the deductible and limits applied", "any betterment identified"},
				FalsifiedBy: "a component with no evidential backing, or double-counting between components"},
		},
		Rules: []string{
			"coverage is a question for the insurer and the policy; VERIQO establishes facts and does not determine liability",
			"every amount cites the evidence it rests on; an unbacked figure is not a quantum",
			"payment authority and payment execution authority are disjoint role sets -- one party never does both",
		},
		OutcomeVocabulary: []string{"evidence_package_delivered", "quantum_computed", "no_further_action", "referred_to_arbitration"},
	},
	{
		Domain: DomainTradeFinance,
		ObjectTypes: []ontology.ObjectType{
			ontology.ObjectContract, ontology.ObjectClause, ontology.ObjectDocument,
			ontology.ObjectTransaction, ontology.ObjectObligation, ontology.ObjectBreach,
		},
		EvidenceClasses: []EvidenceClass{
			{Name: "documentary credit and amendments", Source: "the issuing bank"},
			{Name: "presented document set", Source: "the beneficiary", PartyMediated: true},
			{Name: "electronic bill of lading record", Source: "an MLETR-conformant eBL platform"},
			{Name: "inspection certificate", Source: "the named inspection body"},
			{Name: "SWIFT message trace", Source: "the messaging network"},
		},
		Obligations: []ProofObligation{
			{Claim: "the presentation is compliant with the credit",
				Requires:    []string{"the credit and every amendment in force", "each required document present", "each discrepancy identified against a specific term"},
				FalsifiedBy: "a document that fails a stated term of the credit"},
			{Claim: "the eBL holder is the party claiming to be",
				Requires:    []string{"the platform's transfer record", "an unbroken transfer chain from issuance", "the platform's own reliability assessment"},
				FalsifiedBy: "a transfer in the chain that the platform cannot evidence"},
		},
		Rules: []string{
			"discrepancy is determined against a stated term of the credit, never as a general impression of the document set",
			"an eBL platform's reliability is an external qualification question; VERIQO records the platform's assertion and does not certify it",
			"a document a party presented is party-mediated evidence of the transaction, not independent evidence of the facts it recites",
		},
		OutcomeVocabulary: []string{"determination_issued", "evidence_package_delivered", "no_further_action", "referred_to_issuing_bank"},
	},
	{
		Domain: DomainDispute,
		ObjectTypes: []ontology.ObjectType{
			ontology.ObjectClaim, ontology.ObjectCounterclaim, ontology.ObjectContradiction,
			ontology.ObjectProofObligation, ontology.ObjectTimeline, ontology.ObjectFinding,
		},
		EvidenceClasses: []EvidenceClass{
			{Name: "pleadings and statements of case", Source: "the parties", PartyMediated: true},
			{Name: "disclosed document set", Source: "the disclosing party", PartyMediated: true},
			{Name: "expert report", Source: "an appointed expert"},
			{Name: "witness statement", Source: "the witness", PartyMediated: true},
			{Name: "contemporaneous record", Source: "whichever party held it", PartyMediated: true},
		},
		Obligations: []ProofObligation{
			{Claim: "the evidence package is complete for the issues framed",
				Requires:    []string{"every framed issue decomposed into supporting, contradicting and missing evidence", "each party's position recorded verbatim", "the legal questions separated from the factual ones"},
				FalsifiedBy: "a framed issue with no evidence decomposition"},
		},
		Rules: []string{
			"VERIQO furnishes facts, evidence, timelines, contradictions, causation hypotheses, quantum and proof obligations; the arbitrator, court or authorized decision-maker decides",
			"two parties' positions sitting side by side, unreconciled, IS the output -- neither is weighted",
			"a legal question is marked AWAITING_LEGAL_INTERPRETATION and left there; the system does not answer it",
		},
		OutcomeVocabulary: []string{"evidence_package_delivered", "no_further_action", "matter_withdrawn"},
	},
}

// DomainSemantics returns every domain's declaration.
func DomainSemantics() []Semantics { return append([]Semantics(nil), domainSemantics...) }

// SemanticsFor returns one domain's declaration.
func SemanticsFor(domain string) (Semantics, bool) {
	for _, s := range domainSemantics {
		if s.Domain == domain {
			return s, true
		}
	}
	return Semantics{}, false
}

// Validate refuses a declaration that is incomplete or that adjudicates.
func (s Semantics) Validate() error {
	if strings.TrimSpace(s.Domain) == "" {
		return fmt.Errorf("casefabric: a semantics declaration must name its domain")
	}
	if _, ok := Lookup(s.Domain); !ok {
		return fmt.Errorf("casefabric: domain %q declares semantics but is not registered with the fabric", s.Domain)
	}
	if len(s.ObjectTypes) == 0 {
		return fmt.Errorf("casefabric: domain %q declares no ontology", s.Domain)
	}
	for _, t := range s.ObjectTypes {
		if !ontology.IsKnownObjectType(t) {
			return fmt.Errorf("casefabric: domain %q works in unregistered object type %q", s.Domain, t)
		}
	}
	if len(s.EvidenceClasses) == 0 {
		return fmt.Errorf("casefabric: domain %q declares no evidence classes", s.Domain)
	}
	for _, e := range s.EvidenceClasses {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Source) == "" {
			return fmt.Errorf("casefabric: domain %q declares an evidence class with no name or no source", s.Domain)
		}
	}
	if len(s.Obligations) == 0 {
		return fmt.Errorf("casefabric: domain %q declares no proof obligations", s.Domain)
	}
	for _, o := range s.Obligations {
		if strings.TrimSpace(o.Claim) == "" || len(o.Requires) == 0 {
			return fmt.Errorf("casefabric: domain %q declares an obligation with no claim or no requirements", s.Domain)
		}
		if strings.TrimSpace(o.FalsifiedBy) == "" {
			return fmt.Errorf("casefabric: domain %q obligation %q states no falsifier, so it is not a test",
				s.Domain, o.Claim)
		}
	}
	if len(s.Rules) == 0 {
		return fmt.Errorf("casefabric: domain %q declares no rules", s.Domain)
	}
	if len(s.OutcomeVocabulary) == 0 {
		return fmt.Errorf("casefabric: domain %q declares no outcome vocabulary", s.Domain)
	}
	for _, o := range s.OutcomeVocabulary {
		if err := (Outcome{Disposition: o, Summary: "vocabulary check"}).Validate(); err != nil {
			return fmt.Errorf("casefabric: domain %q outcome %q adjudicates: %w", s.Domain, o, err)
		}
	}
	return nil
}

// ValidateAllSemantics checks every registered domain has a complete,
// non-adjudicatory declaration.
func ValidateAllSemantics() error {
	declared := map[string]bool{}
	for _, s := range domainSemantics {
		if declared[s.Domain] {
			return fmt.Errorf("casefabric: domain %q declares semantics twice", s.Domain)
		}
		declared[s.Domain] = true
		if err := s.Validate(); err != nil {
			return err
		}
	}
	var missing []string
	for _, d := range RegisteredDomains() {
		if !declared[d] {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("casefabric: %d registered domain(s) declare no semantics: %s",
			len(missing), strings.Join(missing, ", "))
	}
	return nil
}

// PartyMediatedClasses returns the evidence classes a domain acquires
// through a party.
//
// Exported because it is the honest answer to "how much of this domain's
// evidence is independent?", and the answer is usually less flattering
// than the domain's own description of itself.
func (s Semantics) PartyMediatedClasses() []string {
	var out []string
	for _, e := range s.EvidenceClasses {
		if e.PartyMediated {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out
}

// RenderSemantics writes the domain declarations.
func RenderSemantics() string {
	var b strings.Builder
	for _, s := range domainSemantics {
		b.WriteString(fmt.Sprintf("=== %s ===\n", strings.ToUpper(s.Domain)))
		types := make([]string, 0, len(s.ObjectTypes))
		for _, t := range s.ObjectTypes {
			types = append(types, string(t))
		}
		b.WriteString(fmt.Sprintf("  ONTOLOGY        %s\n", strings.Join(types, ", ")))
		for i, e := range s.EvidenceClasses {
			label := "EVIDENCE"
			if i > 0 {
				label = ""
			}
			mediated := ""
			if e.PartyMediated {
				mediated = "  [party-mediated]"
			}
			b.WriteString(fmt.Sprintf("  %-15s %s <- %s%s\n", label, e.Name, e.Source, mediated))
		}
		for i, o := range s.Obligations {
			label := "OBLIGATIONS"
			if i > 0 {
				label = ""
			}
			b.WriteString(fmt.Sprintf("  %-15s %s\n", label, o.Claim))
			b.WriteString(fmt.Sprintf("  %-15s   requires: %s\n", "", strings.Join(o.Requires, "; ")))
			b.WriteString(fmt.Sprintf("  %-15s   falsified by: %s\n", "", o.FalsifiedBy))
		}
		for i, r := range s.Rules {
			label := "RULES"
			if i > 0 {
				label = ""
			}
			b.WriteString(fmt.Sprintf("  %-15s %s\n", label, r))
		}
		b.WriteString(fmt.Sprintf("  OUTCOMES        %s\n\n", strings.Join(s.OutcomeVocabulary, ", ")))
	}
	return b.String()
}
