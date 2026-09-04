package domain

import (
	"veriqo/pkg/hypothesis"
	"veriqo/pkg/reverseproof"
)

// Supply chain: surface-level depth, as the specification says, and
// real.
//
// The characteristic error here is concluding a dependency from an
// absence of alternatives IN THE DATA VERIQO HOLDS. A supplier that
// looks single-source in a graph built from shipping records may have
// three qualified alternatives that have never shipped. So the
// single-source template's necessary conditions include establishing
// that the search covered the alternatives, not only that none was
// found.

// SupplyChainTemplates returns the supply-chain claim shapes.
func SupplyChainTemplates() []Template {
	return []Template{
		{
			ID: "SC-SINGLE-SOURCE-RISK", Domain: "supplychain",
			Question: "is this input effectively single-sourced?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the input is identified precisely enough to have substitutes",
					"a specification a second supplier could be assessed against",
					0.8, 0.2, true, "customer", "engineering"),
				cond("C2", "the observed supply comes from one supplier",
					"shipment or purchase records over a representative period",
					0.7, 0.3, true, "customer", "carrier", "customs"),
				cond("C3", "the search for alternatives was actually performed",
					"a record of which qualified alternatives were assessed",
					0.9, 0.4, true, "customer", "market data"),
				cond("C4", "no qualified alternative exists that has simply not shipped",
					"qualification or approval records for other suppliers",
					0.95, 0.6, true, "customer", "certification body"),
				cond("C5", "switching is materially constrained",
					"qualification lead time, tooling, or contractual exclusivity",
					0.85, 0.4, true, "customer", "contract"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the input is genuinely single-sourced",
					[]string{"one supplier in the records", "a completed alternatives search",
						"no qualified alternative", "a material switching constraint"}),
				hyp("H2", "alternatives exist and have not recently shipped",
					[]string{"other qualified suppliers on the approved list",
						"historic shipments from a second source"}),
				hyp("H3", "the apparent concentration is an artefact of the data held",
					[]string{"records covering one lane or one period only",
						"purchases through an intermediary obscuring the producer"}),
				hyp("H4", "the concentration is deliberate and contractually managed",
					[]string{"a sole-supply agreement with agreed continuity provisions",
						"a contracted buffer stock"}),
			},
		},
		{
			ID: "SC-DELIVERY-INCONSISTENCY", Domain: "supplychain",
			Question: "does the delivery record match what was contracted and shipped?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the contracted delivery terms are established",
					"the purchase order or contract with incoterm and date",
					0.85, 0.1, true, "customer", "supplier"),
				cond("C2", "the shipment is evidenced",
					"a transport document naming the goods, quantity and route",
					0.85, 0.2, true, "carrier", "supplier"),
				cond("C3", "the receipt is evidenced",
					"a goods-received note or warehouse record",
					0.85, 0.2, true, "customer", "warehouse"),
				cond("C4", "the three describe the same consignment",
					"consistent references, quantities and descriptions",
					0.9, 0.2, true, "customer", "supplier", "carrier"),
				cond("C5", "any difference is not explained by a partial or split delivery",
					"the delivery schedule and any agreed splits",
					0.8, 0.2, true, "customer", "supplier"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the delivery differs materially from what was contracted",
					[]string{"a quantity or date difference beyond the agreed terms",
						"no agreed split explaining it"}),
				hyp("H2", "the difference is a split or partial delivery",
					[]string{"a delivery schedule", "a subsequent consignment completing it"}),
				hyp("H3", "the records describe different consignments",
					[]string{"divergent references", "incompatible transport routes"}),
				hyp("H4", "the receipt record is incomplete",
					[]string{"goods received and not booked in",
						"a warehouse system gap over the period"}),
			},
		},
	}
}
