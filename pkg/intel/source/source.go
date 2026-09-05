// Package source classifies where intelligence material came from, and
// constrains what may be done with it on that basis.
//
// # Why a source class is not a label
//
// The intelligence domains VERIQO serves -- maritime, commodity, trade
// finance, insurance, dispute, compliance -- all draw on material that
// ranges from a public register to a leaked corpus circulating on a
// hidden service. Every real intelligence platform touches that range.
// Pretending otherwise produces a system that is either useless or
// quietly unlawful.
//
// The mistake is to treat the range as a quality gradient: official
// registers are "good", scraped forums are "bad", and an analyst
// weighs them. That framing loses the two things that actually matter,
// which are not about quality at all:
//
//	LAWFULNESS   may this material be acquired, held and used, in this
//	             jurisdiction, for this purpose? A breach corpus is not
//	             low-quality. It may be unlawful to hold, and no amount
//	             of corroboration changes that.
//
//	ATTRIBUTION  can the material be traced to somebody who can be
//	             asked about it? An anonymous paste has no producer, so
//	             Law 2 cannot be satisfied and Law 6 cannot be
//	             evaluated -- two anonymous pastes might be one person.
//
// So a class carries constraints, and the constraints are enforced
// rather than advisory. Material from a class whose lawfulness is
// UNESTABLISHED cannot found a qualified finding, however many sources
// agree with it, and however confident anybody is.
//
// # What this package deliberately does not do
//
// It does not acquire anything. There is no connector here, no
// crawler, no credential, no address. It is a classification and a set
// of refusals -- the part of handling such material that belongs in
// code. Acquisition is a legal and operational decision made by people
// with names, under an opinion from counsel that does not exist yet
// (evidence debt ED-010).
package source

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
)

var (
	ErrUnknownClass   = errors.New("intel/source: unknown source class")
	ErrUnlawful       = errors.New("intel/source: this class may not be acquired without a legal basis")
	ErrSoleSupport    = errors.New("intel/source: this class may not be the sole support for a finding")
	ErrNoProducer     = errors.New("intel/source: this class requires a named producer and has none")
	ErrPurposeRefused = errors.New("intel/source: this purpose is not permitted for this class")
	ErrNoBasis        = errors.New("intel/source: no legal basis is recorded")
	ErrCaveatMissing  = errors.New("intel/source: a mandatory caveat was not carried")
)

// Class is where material came from, at the level of generality that
// changes what may be done with it.
type Class string

const (
	// OfficialRegister: a government or regulator's own published
	// record. Attributable, and its errors are the register's errors
	// rather than ours.
	OfficialRegister Class = "OFFICIAL_REGISTER"
	// LicensedCommercial: a data product bought under a contract. The
	// contract, not this package, says what may be done with it.
	LicensedCommercial Class = "LICENSED_COMMERCIAL"
	// FirstPartyObservation: VERIQO's own sensor or a customer's.
	FirstPartyObservation Class = "FIRST_PARTY_OBSERVATION"
	// CounterpartySubmission: material a party to the matter supplied.
	// Attributable and interested, which is a combination worth
	// naming: an interested party's document is evidence OF the
	// party's position as much as of the fact.
	CounterpartySubmission Class = "COUNTERPARTY_SUBMISSION"
	// PublicWeb: openly published material -- a company website, a
	// news report, a court listing. Attributable in principle and
	// often not in practice.
	PublicWeb Class = "PUBLIC_WEB"
	// AdverseMedia: reporting that alleges wrongdoing. Separated from
	// PublicWeb because an allegation republished by six outlets from
	// one wire story is one source, and because the subject has
	// interests that ordinary reporting does not engage.
	AdverseMedia Class = "ADVERSE_MEDIA"
	// SanctionsList: a designating authority's own list. Attributable,
	// authoritative for what it says, and frequently misread as
	// authoritative for more.
	SanctionsList Class = "SANCTIONS_LIST"
	// AnonymousDisclosure: a paste, a drop, an unattributed leak. No
	// producer, so no independence assessment is possible.
	AnonymousDisclosure Class = "ANONYMOUS_DISCLOSURE"
	// BreachDerived: material obtained through unauthorised access to
	// somebody's system, wherever it now circulates. The defining
	// property is the unauthorised access, not the venue.
	BreachDerived Class = "BREACH_DERIVED"
	// HiddenServiceForum: material from a forum or marketplace reached
	// through an anonymising network.
	//
	// It is a separate class from BreachDerived because the two are
	// routinely conflated and are not the same thing: a forum post
	// discussing a vessel's movements is not stolen data, and a breach
	// corpus hosted on the public web is stolen data. The venue
	// affects ATTRIBUTION; the acquisition affects LAWFULNESS.
	HiddenServiceForum Class = "HIDDEN_SERVICE_FORUM"
	// Inference: material VERIQO derived rather than received. It is a
	// class so that a derived value can never be mistaken for an
	// observation.
	Inference Class = "INFERENCE"
)

// Classes returns every class in a fixed order.
func Classes() []Class {
	return []Class{OfficialRegister, LicensedCommercial, FirstPartyObservation,
		CounterpartySubmission, PublicWeb, AdverseMedia, SanctionsList,
		AnonymousDisclosure, BreachDerived, HiddenServiceForum, Inference}
}

func (c Class) Valid() bool {
	for _, k := range Classes() {
		if k == c {
			return true
		}
	}
	return false
}

// Lawfulness is what is known about whether this material may be held.
type Lawfulness string

const (
	// Established: counsel has given an opinion for a named
	// jurisdiction and purpose.
	Established Lawfulness = "ESTABLISHED"
	// Presumed: the class is ordinarily lawful and nobody has looked
	// at this instance. Honest, common, and not the same as
	// established.
	Presumed Lawfulness = "PRESUMED"
	// Unestablished: nobody knows. The zero-value answer for anything
	// this package cannot reason about.
	Unestablished Lawfulness = "UNESTABLISHED"
	// Prohibited: counsel has said no, or the class is one VERIQO does
	// not handle.
	Prohibited Lawfulness = "PROHIBITED"
)

// Use is what somebody wants to do with the material.
type Use string

const (
	// Screen: check whether something warrants a closer look. The
	// lowest-consequence use, and the one where weak material is most
	// defensible.
	Screen Use = "SCREEN"
	// Lead: direct an investigation towards other evidence.
	Lead Use = "LEAD"
	// Corroborate: add weight to a claim other evidence already
	// supports.
	Corroborate Use = "CORROBORATE"
	// Found: be the evidence a finding rests on.
	Found Use = "FOUND"
	// Disclose: appear in something handed to a third party.
	Disclose Use = "DISCLOSE"
	// Train: be used to fit a model.
	Train Use = "TRAIN"
)

func Uses() []Use { return []Use{Screen, Lead, Corroborate, Found, Disclose, Train} }

// Constraints are what a class carries, regardless of the instance.
type Constraints struct {
	// Attributable: material of this class can, in principle, be
	// traced to a party who can be asked about it.
	Attributable bool
	// RequiresProducer: an instance without a named producer is
	// refused outright.
	RequiresProducer bool
	// MayFound: material of this class may be the sole support for a
	// finding. False does not mean the material is useless -- it means
	// something else must carry the weight.
	MayFound bool
	// CountsForCorroboration: two instances of this class may count as
	// two sources. False where the class cannot support an
	// independence assessment at all, which is the Law 6 case.
	CountsForCorroboration bool
	// PermittedUses is what may be done with it before any
	// instance-level licence or legal basis is considered.
	PermittedUses []Use
	// MinimumClassification is the floor an instance is marked at.
	MinimumClassification classification.Level
	// MandatoryCaveats travel with every claim this material supports,
	// into the finding and into the passport.
	MandatoryCaveats []string
	// LegalBasisRequired: an instance may not be held at all without a
	// recorded basis naming a jurisdiction and an opinion.
	LegalBasisRequired bool
	// Rationale explains the constraints, because a refusal an analyst
	// cannot understand is a refusal they will route around.
	Rationale string
}

// Of returns the constraints for a class.
//
// The table is written out rather than computed, because every line of
// it is a decision somebody should be able to disagree with by reading
// it.
func Of(c Class) (Constraints, error) {
	switch c {
	case OfficialRegister:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: true,
			CountsForCorroboration: true,
			PermittedUses:          []Use{Screen, Lead, Corroborate, Found, Disclose},
			MinimumClassification:  classification.Internal,
			MandatoryCaveats: []string{
				"a register records what was filed, which is not the same as what is true",
			},
			Rationale: "attributable to a body that can be asked, and whose errors are its own",
		}, nil

	case LicensedCommercial:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: true,
			CountsForCorroboration: true,
			PermittedUses:          []Use{Screen, Lead, Corroborate, Found, Disclose},
			MinimumClassification:  classification.Confidential,
			MandatoryCaveats: []string{
				"the licence, not this system, decides what may be done with this material; " +
					"a permitted use here can still be a contractual breach",
			},
			Rationale: "attributable and contracted; the contract governs",
		}, nil

	case FirstPartyObservation:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: true,
			CountsForCorroboration: true,
			PermittedUses:          []Use{Screen, Lead, Corroborate, Found, Disclose, Train},
			MinimumClassification:  classification.Confidential,
			Rationale:              "our own sensor, so the producer is known and the method is ours",
		}, nil

	case CounterpartySubmission:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: true,
			CountsForCorroboration: true,
			PermittedUses:          []Use{Screen, Lead, Corroborate, Found, Disclose},
			MinimumClassification:  classification.Confidential,
			MandatoryCaveats: []string{
				"supplied by a party with an interest in the outcome: it is evidence of " +
					"that party's position as much as of the fact it asserts",
			},
			Rationale: "attributable and interested; the interest must travel with it",
		}, nil

	case PublicWeb:
		return Constraints{
			Attributable: true, RequiresProducer: false, MayFound: false,
			CountsForCorroboration: true,
			PermittedUses:          []Use{Screen, Lead, Corroborate},
			MinimumClassification:  classification.Internal,
			MandatoryCaveats: []string{
				"published material can be changed or withdrawn after observation; what was " +
					"captured is what is held",
			},
			Rationale: "attributable in principle and often not in practice, so it may " +
				"support a claim but not carry it",
		}, nil

	case AdverseMedia:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: false,
			// The critical line. Six outlets republishing one wire
			// story is one source, and treating them as six is the
			// most common way a screening system manufactures
			// confidence.
			CountsForCorroboration: false,
			PermittedUses:          []Use{Screen, Lead},
			MinimumClassification:  classification.Confidential,
			MandatoryCaveats: []string{
				"an allegation is not a finding; this material records that something was " +
					"reported, not that it occurred",
				"republication is not corroboration: outlets carrying one wire story are " +
					"one source",
			},
			LegalBasisRequired: true,
			Rationale: "allegations about identifiable people engage their interests, and " +
				"the republication structure defeats naive independence counting",
		}, nil

	case SanctionsList:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: true,
			CountsForCorroboration: true,
			PermittedUses:          []Use{Screen, Lead, Corroborate, Found, Disclose},
			MinimumClassification:  classification.Internal,
			MandatoryCaveats: []string{
				"a listing is authoritative for the fact of designation and for nothing " +
					"else; it is not a finding about conduct",
				"name matching against a list is a screen, not an identification",
			},
			Rationale: "authoritative for what it actually says, and routinely read as " +
				"saying more",
		}, nil

	case AnonymousDisclosure:
		return Constraints{
			Attributable: false, RequiresProducer: false, MayFound: false,
			// No producer means no independence assessment is
			// possible: two anonymous disclosures might be one person.
			CountsForCorroboration: false,
			PermittedUses:          []Use{Screen, Lead},
			MinimumClassification:  classification.Secret,
			MandatoryCaveats: []string{
				"no producer is identifiable, so nobody can be asked about this material " +
					"and its independence from any other source cannot be assessed",
				"an unattributed disclosure may be a fabrication designed to be found",
			},
			LegalBasisRequired: true,
			Rationale: "Law 2 cannot be satisfied and Law 6 cannot be evaluated; it may " +
				"point somewhere, and it may not carry anything",
		}, nil

	case BreachDerived:
		return Constraints{
			Attributable: false, RequiresProducer: false, MayFound: false,
			CountsForCorroboration: false,
			// Deliberately the narrowest set. Nothing here permits
			// disclosure or training.
			PermittedUses:         []Use{Screen},
			MinimumClassification: classification.Secret,
			MandatoryCaveats: []string{
				"obtained through unauthorised access to somebody's system; holding or " +
					"using it may be unlawful regardless of who else holds it",
				"the subject of this material did not consent to its disclosure and has " +
					"interests that survive the breach",
				"breach corpora are routinely salted, merged and re-sold; content cannot " +
					"be assumed to be what it claims",
			},
			LegalBasisRequired: true,
			Rationale: "the defining property is the unauthorised access, not the venue. " +
				"Lawfulness is the question, not quality, and no amount of corroboration " +
				"answers it",
		}, nil

	case HiddenServiceForum:
		return Constraints{
			Attributable: false, RequiresProducer: false, MayFound: false,
			CountsForCorroboration: false,
			PermittedUses:          []Use{Screen, Lead},
			MinimumClassification:  classification.Secret,
			MandatoryCaveats: []string{
				"the venue is designed to prevent attribution, so no producer can be " +
					"identified and independence cannot be assessed",
				"participants have every incentive to mislead, including about their own " +
					"identity and about each other",
				"observing such a venue may itself be regulated, and the acquisition " +
					"decision is not this system's to make",
			},
			LegalBasisRequired: true,
			Rationale: "an anonymising venue defeats attribution by design. That is a " +
				"structural fact about the material, not a judgement about its content -- " +
				"a forum post is not stolen data, and a breach corpus on the public web is",
		}, nil

	case Inference:
		return Constraints{
			Attributable: true, RequiresProducer: true, MayFound: false,
			CountsForCorroboration: false,
			PermittedUses:          []Use{Screen, Lead, Corroborate},
			MinimumClassification:  classification.Internal,
			MandatoryCaveats: []string{
				"derived rather than observed: it inherits every weakness of its inputs " +
					"and adds the model's own",
			},
			Rationale: "a derived value must never be counted as an independent observation " +
				"of the thing it was derived from",
		}, nil
	}
	return Constraints{}, fmt.Errorf("%w: %q", ErrUnknownClass, c)
}

// LegalBasis is a recorded reason material of a restricted class may
// be held.
type LegalBasis struct {
	// Jurisdiction the opinion covers. An opinion for one jurisdiction
	// says nothing about another.
	Jurisdiction string `json:"jurisdiction"`
	// Purpose it covers. A basis for sanctions screening is not a
	// basis for commercial research.
	Purpose string `json:"purpose"`
	// Opinion identifies the advice. "Engineering's reading" is not an
	// opinion and Validate refuses it.
	Opinion string `json:"opinion"`
	// By names counsel.
	By string `json:"by"`
	// At is when the opinion was given, and Until when it lapses.
	At    time.Time  `json:"at"`
	Until *time.Time `json:"until,omitempty"`
}

func (l LegalBasis) Validate() error {
	if strings.TrimSpace(l.Jurisdiction) == "" || strings.TrimSpace(l.Purpose) == "" {
		return fmt.Errorf("%w: a basis must name a jurisdiction and a purpose; an opinion "+
			"for one of either says nothing about another", ErrNoBasis)
	}
	if strings.TrimSpace(l.Opinion) == "" || strings.TrimSpace(l.By) == "" {
		return fmt.Errorf("%w: a basis must identify the advice and who gave it", ErrNoBasis)
	}
	if l.At.IsZero() {
		return fmt.Errorf("%w: the basis is undated", ErrNoBasis)
	}
	return nil
}

func (l LegalBasis) Current(at time.Time) bool {
	return l.Until == nil || at.Before(*l.Until)
}

// Material is one instance of intelligence material.
type Material struct {
	ID    contract.ID `json:"id"`
	Class Class       `json:"class"`
	// ProducerID is who produced it. Required where the class says so.
	ProducerID string `json:"producer_id,omitempty"`
	// VenueID is where it was found, which is not the same as who
	// produced it. Conflating them is how a forum becomes an author.
	VenueID string `json:"venue_id,omitempty"`
	// Lawfulness is what is known.
	Lawfulness Lawfulness `json:"lawfulness"`
	// Basis, where one exists.
	Basis *LegalBasis `json:"basis,omitempty"`
	// ObservedAt is when it was captured.
	ObservedAt time.Time `json:"observed_at"`
	// ContentHash ties it to bytes.
	ContentHash string `json:"content_hash"`
}

func (m Material) Validate() error {
	if strings.TrimSpace(string(m.ID)) == "" {
		return fmt.Errorf("%w: material has no id", contract.ErrMalformedID)
	}
	con, err := Of(m.Class)
	if err != nil {
		return err
	}
	if con.RequiresProducer && strings.TrimSpace(m.ProducerID) == "" {
		return fmt.Errorf("%w: %s is %s", ErrNoProducer, m.ID, m.Class)
	}
	if m.ObservedAt.IsZero() {
		return fmt.Errorf("intel/source: %s has no observation instant", m.ID)
	}
	if strings.TrimSpace(m.ContentHash) == "" {
		return fmt.Errorf("intel/source: %s has no content hash", m.ID)
	}
	switch m.Lawfulness {
	case Established, Presumed, Unestablished, Prohibited:
	default:
		return fmt.Errorf("intel/source: %s has unknown lawfulness %q", m.ID, m.Lawfulness)
	}
	if m.Lawfulness == Established {
		if m.Basis == nil {
			return fmt.Errorf("%w: %s claims ESTABLISHED lawfulness and records no basis",
				ErrNoBasis, m.ID)
		}
		if err := m.Basis.Validate(); err != nil {
			return fmt.Errorf("%s: %w", m.ID, err)
		}
	}
	if con.LegalBasisRequired && m.Lawfulness != Established {
		return fmt.Errorf("%w: %s is %s, which may not be held without a recorded legal "+
			"basis; it is %s", ErrUnlawful, m.ID, m.Class, m.Lawfulness)
	}
	return nil
}

// Marking is the classification floor this material is held at.
func (m Material) Marking() (classification.Marking, error) {
	con, err := Of(m.Class)
	if err != nil {
		return classification.Marking{}, err
	}
	h := []classification.Handling{}
	if con.LegalBasisRequired {
		h = append(h, classification.NoExport, classification.NoTraining)
	}
	if !con.Attributable {
		h = append(h, classification.NoRedistribution)
	}
	return classification.New(con.MinimumClassification, h...)
}

// Permit decides whether a use is allowed, and says why not.
//
// It is the one function callers need. Everything above is the data it
// reads.
func (m Material) Permit(u Use, at time.Time) error {
	if err := m.Validate(); err != nil {
		return err
	}
	con, _ := Of(m.Class)

	if m.Lawfulness == Prohibited {
		return fmt.Errorf("%w: %s is %s and its lawfulness is PROHIBITED",
			ErrUnlawful, m.ID, m.Class)
	}
	if con.LegalBasisRequired {
		if m.Basis == nil || !m.Basis.Current(at) {
			return fmt.Errorf("%w: %s requires a current legal basis and has none at %s",
				ErrUnlawful, m.ID, at.Format(time.RFC3339))
		}
	}
	for _, p := range con.PermittedUses {
		if p == u {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is %s, which permits %s. %s",
		ErrPurposeRefused, m.ID, m.Class, joinUses(con.PermittedUses), con.Rationale)
}

// Support is a set of material offered in support of a claim.
type Support struct {
	Material []Material
}

// FoundFinding reports whether this support may found a finding, and
// what must accompany it.
//
// The rule the audit's Law 6 and this package's own doc both point at:
// material that cannot found a finding does not become able to by
// being numerous. Ten anonymous disclosures are still zero producers.
func (s Support) FoundFinding(at time.Time) ([]string, error) {
	if len(s.Material) == 0 {
		return nil, fmt.Errorf("%w: nothing was offered", ErrSoleSupport)
	}
	var caveats []string
	founding := 0
	seen := map[string]bool{}

	for _, m := range s.Material {
		if err := m.Permit(Found, at); err != nil {
			// Material that may not found is not an error here: it
			// may still be present as context. What matters is
			// whether anything CAN found.
			if !errors.Is(err, ErrPurposeRefused) {
				return nil, err
			}
		} else {
			founding++
		}
		con, err := Of(m.Class)
		if err != nil {
			return nil, err
		}
		for _, c := range con.MandatoryCaveats {
			if !seen[c] {
				seen[c] = true
				caveats = append(caveats, c)
			}
		}
	}
	sort.Strings(caveats)

	if founding == 0 {
		var classes []string
		for _, m := range s.Material {
			classes = append(classes, string(m.Class))
		}
		sort.Strings(classes)
		return caveats, fmt.Errorf("%w: %d item(s) of material, none of a class that may "+
			"found a finding (%s). Numerousness does not substitute: material that cannot "+
			"carry a finding does not become able to by being repeated",
			ErrSoleSupport, len(s.Material), strings.Join(unique(classes), ", "))
	}
	return caveats, nil
}

// CorroborationCount reports how many of these items may count towards
// corroboration, and names the ones that may not.
//
// This is Law 6 at the source-class layer, before any independence
// assessment: some classes cannot support the assessment at all, and
// counting them would be counting an unanswerable question as a yes.
func (s Support) CorroborationCount() (int, []string) {
	n := 0
	var excluded []string
	for _, m := range s.Material {
		con, err := Of(m.Class)
		if err != nil || !con.CountsForCorroboration {
			excluded = append(excluded, fmt.Sprintf("%s (%s)", m.ID, m.Class))
			continue
		}
		n++
	}
	sort.Strings(excluded)
	return n, excluded
}

func joinUses(us []Use) string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = string(u)
	}
	return strings.Join(out, ", ")
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Describe renders a class's constraints for a reader.
func Describe(c Class) string {
	con, err := Of(c)
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", c)
	fmt.Fprintf(&b, "  attributable:        %v\n", con.Attributable)
	fmt.Fprintf(&b, "  may found a finding: %v\n", con.MayFound)
	fmt.Fprintf(&b, "  counts for corrob.:  %v\n", con.CountsForCorroboration)
	fmt.Fprintf(&b, "  permitted uses:      %s\n", joinUses(con.PermittedUses))
	fmt.Fprintf(&b, "  minimum marking:     %s\n", con.MinimumClassification)
	fmt.Fprintf(&b, "  legal basis needed:  %v\n", con.LegalBasisRequired)
	fmt.Fprintf(&b, "  why:                 %s\n", con.Rationale)
	for _, c := range con.MandatoryCaveats {
		fmt.Fprintf(&b, "  caveat:              %s\n", c)
	}
	return b.String()
}
