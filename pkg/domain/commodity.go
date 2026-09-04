package domain

import (
	"veriqo/pkg/hypothesis"
	"veriqo/pkg/reverseproof"
)

// Commodity trade: the domain where a difference between two numbers
// is most often mistaken for a loss.
//
// The templates here exist to force the measurement-basis question to
// the front, because a quantity discrepancy claim that has not asked
// it is arithmetic rather than evidence. pkg/quantum refuses to
// compute across incomparable bases; this is the analytical form of
// the same discipline.

// CommodityTemplates returns the commodity claim shapes.
func CommodityTemplates() []Template {
	return []Template{
		{
			ID: "COM-QUANTITY-DISCREPANCY", Domain: "commodity",
			Question: "is the difference between loaded and discharged quantity a real shortfall?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the loading quantity is established on a stated basis",
					"a loading survey naming method, temperature, density and standard",
					0.85, 0.3, true, "load terminal", "independent inspector"),
				cond("C2", "the discharge quantity is established on a stated basis",
					"a discharge survey naming method, temperature, density and standard",
					0.85, 0.3, true, "discharge terminal", "independent inspector"),
				cond("C3", "the two bases are comparable",
					"the same method, mass basis, temperature reference and standard at both ends",
					0.95, 0.4, true, "both terminals", "independent inspector"),
				cond("C4", "the difference exceeds the contractual tolerance",
					"the tolerance clause and the arithmetic on the contract quantity",
					0.7, 0.1, true, "contract"),
				cond("C5", "no ordinary loss accounts for the difference",
					"clingage, evaporation and line-fill allowances for this cargo and voyage",
					0.8, 0.3, true, "independent inspector", "terminal"),
				cond("C6", "the difference arose during the period in dispute",
					"quantities at each custody transfer along the chain",
					0.9, 0.5, true, "terminals", "vessel"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "cargo was physically lost between load and discharge",
					[]string{"comparable bases", "a difference beyond tolerance and ordinary loss",
						"a physical mechanism"}),
				hyp("H2", "the difference is a measurement-basis artefact",
					[]string{"differing temperature references", "differing mass bases",
						"a density difference between the two calculations"}),
				hyp("H3", "the loading quantity was overstated",
					[]string{"a shore-tank and ship's-figures discrepancy at load",
						"a load-port reconciliation failure"}),
				hyp("H4", "the difference is within ordinary loss for this trade",
					[]string{"clingage and evaporation consistent with the cargo and voyage",
						"a difference comparable to peer voyages"}),
				hyp("H5", "the loss occurred outside the period in dispute",
					[]string{"a discrepancy already present at an earlier custody transfer"}),
			},
		},
		{
			ID: "COM-DOCUMENT-CONSISTENCY", Domain: "commodity",
			Question: "are the trade documents mutually consistent?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the same cargo is identified across the documents",
					"a consistent lot, parcel or B/L reference in each",
					0.8, 0.1, true, "trader", "shipper"),
				cond("C2", "the quantities agree within tolerance",
					"contract, B/L, invoice and inspection quantities reconciled",
					0.85, 0.2, true, "trader", "inspector"),
				cond("C3", "the quality specification is the same across the documents",
					"the contract specification against the certificate of quality",
					0.85, 0.2, true, "trader", "inspector"),
				cond("C4", "the stated origin is consistent and evidenced",
					"a certificate of origin consistent with the loading location",
					0.9, 0.3, true, "chamber of commerce", "terminal"),
				cond("C5", "the parties named are the contracting parties",
					"the contract's parties against those on the B/L and invoice",
					0.8, 0.1, true, "trader"),
				cond("C6", "the dates form a possible sequence",
					"issue dates in an order a real shipment could produce",
					0.75, 0.1, true, "trader"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the documents describe one consistent transaction",
					[]string{"matching references", "reconciled quantities", "a possible date sequence"}),
				hyp("H2", "an ordinary clerical error explains the difference",
					[]string{"a single-field mismatch", "a correction or amendment issued"}),
				hyp("H3", "the documents describe two different cargoes",
					[]string{"divergent quantities and specifications",
						"inconsistent loading locations"}),
				hyp("H4", "a document was altered after issue",
					[]string{"an inconsistency between an original and a copy",
						"a date sequence no real shipment could produce"}),
			},
		},
	}
}
