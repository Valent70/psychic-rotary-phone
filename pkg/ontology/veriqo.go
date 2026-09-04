package ontology

import (
	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/governance/classification"
)

// The VERIQO ontology.
//
// # The chain this file exists to make expressible
//
//	Vessel -> Voyage -> CargoLot -> BillOfLading -> Contract
//	       -> Invoice -> Payment -> InsuranceClaim
//
// Every link in it crosses a domain boundary that a vertical product
// would have made into an integration project. A maritime system knows
// about the vessel and the voyage; a trade-finance system knows about
// the invoice and the payment; neither can answer "was the cargo this
// payment settled ever loaded", which is the question a payment
// diversion or a documentary fraud turns on.
//
// # Domain names are views, not partitions
//
// A Vessel appears in the maritime view and the insurance view. It is
// ONE object; the two views project it differently. The moment a type
// belongs to exactly one view and edges never cross, this is five
// products in a monorepo.

// Domain view names.
const (
	Maritime    = "maritime"
	Insurance   = "insurance"
	Commodity   = "commodity"
	SupplyChain = "supplychain"
	Finance     = "finance"
	Dispute     = "dispute"
)

func Domains() []string {
	return []string{Maritime, Insurance, Commodity, SupplyChain, Finance, Dispute}
}

func internal() classification.Marking {
	return classification.MustNew(classification.Internal)
}

func confidential() classification.Marking {
	return classification.MustNew(classification.Confidential)
}

func restricted(h ...classification.Handling) classification.Marking {
	return classification.MustNew(classification.Restricted, h...)
}

// prop and qty leave the classification UNSET, so the property
// inherits its object's. Only the properties that need to be MORE
// restrictive than their object -- a beneficial owner on an otherwise
// INTERNAL organisation, an account holder's name -- are written out
// in full below.
func prop(name string, t PropertyType, required bool) Property {
	return Property{Name: name, Type: t, Required: required}
}

func qty(name, unit string) Property {
	return Property{Name: name, Type: Quantity, Unit: unit}
}

// VeriqoObjects is the object catalogue.
func VeriqoObjects() []ObjectType {
	return []ObjectType{
		{Name: "Vessel", Kind: entity.Vessel,
			Domains:        []string{Maritime, Insurance, SupplyChain, Dispute},
			Classification: internal(),
			Properties: []Property{
				prop("name", Text, false),
				prop("flag", Text, false),
				prop("vessel_type", Text, false),
				qty("deadweight", "MT"),
				prop("built_year", Number, false),
			}},

		{Name: "Voyage", Kind: entity.Voyage,
			Domains:        []string{Maritime, Commodity, SupplyChain, Dispute},
			Classification: internal(),
			Properties: []Property{
				prop("departed_at", Timestamp, false),
				prop("arrived_at", Timestamp, false),
				prop("origin", Ref, false),
				prop("destination", Ref, false),
			}},

		{Name: "PortCall", Kind: entity.Location,
			Domains:        []string{Maritime, SupplyChain},
			Classification: internal(),
			Properties: []Property{
				prop("arrived_at", Timestamp, true),
				prop("departed_at", Timestamp, false),
				prop("berth", Text, false),
			}},

		{Name: "CargoLot", Kind: entity.CargoLot,
			Domains:        []string{Maritime, Commodity, SupplyChain, Insurance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("commodity", Text, true),
				qty("quantity", "MT"),
				qty("loaded_quantity", "MT"),
				qty("discharged_quantity", "MT"),
				prop("quality_spec", Text, false),
				prop("origin_country", Text, false),
			}},

		{Name: "BillOfLading", Kind: entity.Document,
			Domains:        []string{Maritime, Commodity, Finance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("bl_number", Text, true),
				prop("issued_at", Timestamp, true),
				prop("shipper", Ref, false),
				prop("consignee", Ref, false),
				qty("stated_quantity", "MT"),
			}},

		{Name: "Contract", Kind: entity.Contract,
			Domains:        []string{Commodity, Finance, SupplyChain, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("contract_number", Text, true),
				prop("incoterm", Enum, false),
				prop("governing_law", Text, false),
				qty("contract_quantity", "MT"),
				qty("tolerance_pct", "percent"),
			}},

		{Name: "Invoice", Kind: entity.Document,
			Domains:        []string{Commodity, Finance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("invoice_number", Text, true),
				prop("issued_at", Timestamp, true),
				qty("amount", "currency-minor-units"),
				prop("currency", Text, true),
			}},

		{Name: "Payment", Kind: entity.Payment,
			Domains:        []string{Finance, Dispute},
			Classification: restricted(),
			Properties: []Property{
				qty("amount", "currency-minor-units"),
				prop("currency", Text, true),
				prop("instructed_at", Timestamp, false),
				// Instruction and settlement are separate properties
				// because Law: never infer payment execution from a
				// payment instruction. One field would force the
				// inference.
				prop("settled_at", Timestamp, false),
				prop("reference", Text, false),
			}},

		{Name: "Account", Kind: entity.Account,
			Domains:        []string{Finance, Dispute},
			Classification: restricted(classification.PersonalData),
			Properties: []Property{
				prop("iban", Text, false),
				prop("bic", Text, false),
				Property{Name: "holder_name", Type: Text,
					Classification: restricted(classification.PersonalData)},
			}},

		{Name: "Organisation", Kind: entity.Organisation,
			Domains:        []string{Maritime, Insurance, Commodity, SupplyChain, Finance, Dispute},
			Classification: internal(),
			Properties: []Property{
				prop("legal_name", Text, true),
				prop("jurisdiction", Text, false),
				Property{Name: "beneficial_owner", Type: Ref,
					Classification: restricted(classification.PersonalData)},
			}},

		{Name: "Person", Kind: entity.Person,
			Domains:        []string{Maritime, Insurance, Finance, Dispute},
			Classification: restricted(classification.PersonalData),
			Properties: []Property{
				Property{Name: "full_name", Type: Text, Required: true,
					Classification: restricted(classification.PersonalData)},
				Property{Name: "role", Type: Text,
					Classification: restricted(classification.PersonalData)},
			}},

		{Name: "Facility", Kind: entity.Facility,
			Domains:        []string{Maritime, Commodity, SupplyChain},
			Classification: internal(),
			Properties: []Property{
				prop("name", Text, true),
				prop("unlocode", Text, false),
				prop("location", Geo, false),
			}},

		{Name: "Incident", Kind: entity.Incident,
			Domains:        []string{Maritime, Insurance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("occurred_at", Timestamp, false),
				prop("location", Geo, false),
				prop("description", Text, false),
			}},

		{Name: "InsurancePolicy", Kind: entity.Policy,
			Domains:        []string{Insurance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("policy_number", Text, true),
				prop("inception", Timestamp, true),
				prop("expiry", Timestamp, true),
				qty("limit", "currency-minor-units"),
			}},

		{Name: "InsuranceClaim", Kind: entity.Document,
			Domains:        []string{Insurance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("claim_number", Text, true),
				prop("notified_at", Timestamp, false),
				qty("claimed_amount", "currency-minor-units"),
			}},

		{Name: "Inspection", Kind: entity.Document,
			Domains:        []string{Commodity, Insurance, Dispute},
			Classification: confidential(),
			Properties: []Property{
				prop("inspected_at", Timestamp, true),
				prop("inspector", Ref, false),
				qty("measured_quantity", "MT"),
				prop("measurement_basis", Text, false),
			}},
	}
}

func rel(name, from, to string, c Cardinality, temporal bool, domains ...string) RelationshipType {
	return RelationshipType{Name: name, From: from, To: to, Cardinality: c,
		Temporal: temporal, Domains: domains, Classification: internal()}
}

// VeriqoRelationships is the edge catalogue.
func VeriqoRelationships() []RelationshipType {
	return []RelationshipType{
		// Maritime.
		rel("PERFORMED_VOYAGE", "Vessel", "Voyage", OneToMany, true, Maritime, Dispute),
		rel("CALLED_AT", "Voyage", "PortCall", OneToMany, true, Maritime, SupplyChain),
		rel("PORT_CALL_AT_FACILITY", "PortCall", "Facility", ManyToOne, false, Maritime, SupplyChain),
		rel("CARRIED", "Voyage", "CargoLot", OneToMany, true, Maritime, Commodity, SupplyChain),

		// Ownership and control -- temporal, because "who operated it"
		// is always a question about a period.
		rel("OWNED_BY", "Vessel", "Organisation", ManyToOne, true, Maritime, Insurance, Dispute),
		rel("OPERATED_BY", "Vessel", "Organisation", ManyToOne, true, Maritime, Insurance, Dispute),
		rel("BENEFICIALLY_OWNED_BY", "Organisation", "Person", ManyToMany, true, Finance, Dispute),

		// The document chain -- these are the cross-domain edges.
		rel("EVIDENCED_BY_BL", "CargoLot", "BillOfLading", OneToMany, false,
			Maritime, Commodity, Finance, Dispute),
		rel("UNDER_CONTRACT", "CargoLot", "Contract", ManyToOne, false,
			Commodity, Finance, SupplyChain, Dispute),
		rel("INVOICED_BY", "Contract", "Invoice", OneToMany, false, Commodity, Finance, Dispute),
		rel("SETTLED_BY", "Invoice", "Payment", OneToMany, false, Finance, Dispute),
		rel("PAID_TO", "Payment", "Account", ManyToOne, false, Finance, Dispute),
		rel("ACCOUNT_HELD_BY", "Account", "Organisation", ManyToOne, true, Finance, Dispute),

		// Trade parties.
		rel("SELLER", "Contract", "Organisation", ManyToOne, false, Commodity, Finance, Dispute),
		rel("BUYER", "Contract", "Organisation", ManyToOne, false, Commodity, Finance, Dispute),

		// Insurance.
		rel("COVERED_BY", "Vessel", "InsurancePolicy", ManyToMany, true, Insurance, Dispute),
		rel("CARGO_COVERED_BY", "CargoLot", "InsurancePolicy", ManyToMany, true, Insurance, Dispute),
		rel("CLAIM_UNDER", "InsuranceClaim", "InsurancePolicy", ManyToOne, false, Insurance, Dispute),
		rel("CLAIM_ARISES_FROM", "InsuranceClaim", "Incident", ManyToOne, false, Insurance, Dispute),
		rel("INCIDENT_INVOLVED", "Incident", "Vessel", ManyToMany, false, Maritime, Insurance, Dispute),

		// Measurement -- the input to the quantum engine.
		rel("INSPECTED", "Inspection", "CargoLot", ManyToOne, false, Commodity, Insurance, Dispute),

		// Supply chain.
		rel("SUPPLIED_BY", "CargoLot", "Organisation", ManyToOne, true, SupplyChain, Commodity),
	}
}

// Veriqo builds the canonical ontology at a revision.
func Veriqo(revision uint64) (*Ontology, error) {
	return New(contract.Version{Component: "veriqo-ontology", Revision: revision},
		VeriqoObjects(), VeriqoRelationships())
}
