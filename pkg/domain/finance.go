package domain

import (
	"veriqo/pkg/hypothesis"
	"veriqo/pkg/reverseproof"
)

// Trade finance: the domain with the sharpest single rule.
//
// # Never infer execution from instruction
//
//	never infer payment execution from payment instruction.
//
// An instruction is a request. A payment is a movement of funds. They
// are recorded in different systems, they fail independently, and the
// gap between them is exactly where a diversion lives: the instruction
// says one beneficiary, the movement went to another, and a system
// that treated the instruction as the payment would show nothing
// wrong.
//
// So the ontology gives Payment separate instructed_at and settled_at
// properties, the domain refusals forbid "the payment was executed",
// and the payment-diversion template requires the SETTLEMENT to be
// evidenced separately from the instruction.

// FinanceTemplates returns the trade-finance claim shapes.
func FinanceTemplates() []Template {
	return []Template{
		{
			ID: "FIN-PAYMENT-DIVERSION", Domain: "finance",
			Question: "was a payment directed to a beneficiary other than the one entitled?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the entitled beneficiary is established from the contract",
					"the contract or invoice naming the payee and its account",
					0.9, 0.2, true, "counterparty", "trader"),
				cond("C2", "a payment instruction was issued",
					"the instruction as sent, with its beneficiary details",
					0.85, 0.3, true, "bank", "payer"),
				cond("C3", "the instruction's beneficiary differs from the entitled one",
					"a comparison of account details between contract and instruction",
					0.95, 0.1, true, "internal analysis"),
				// The condition that carries the rule.
				cond("C4", "funds actually moved",
					"a bank statement or settlement confirmation, which is a DIFFERENT "+
						"record from the instruction",
					0.95, 0.5, true, "bank"),
				cond("C5", "the change of details was not authorised",
					"the absence of a signed amendment or an authenticated instruction to change",
					0.9, 0.4, true, "counterparty", "payer"),
				cond("C6", "the change was communicated through a channel that can be "+
					"authenticated",
					"the email headers, portal log or authenticated message carrying it",
					0.85, 0.4, true, "payer", "email provider"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the payment was diverted by an unauthorised change of details",
					[]string{"a beneficiary mismatch", "evidenced settlement",
						"no authorised amendment", "an unauthenticated change channel"},
					"a signed amendment changing the account"),
				hyp("H2", "the counterparty legitimately changed its bank details",
					[]string{"an authenticated notification", "a signed amendment",
						"subsequent invoices to the same new account"}),
				hyp("H3", "the instruction was issued and never settled",
					[]string{"an instruction with no matching statement entry",
						"a rejection or recall record"}),
				hyp("H4", "the mismatch is a records artefact",
					[]string{"an intermediary or correspondent account in the chain",
						"a group treasury account collecting for the entity"}),
			},
			Refusals: []string{
				"an instruction is not a payment: settlement must be evidenced separately",
			},
		},
		{
			ID: "FIN-INVOICE-MISMATCH", Domain: "finance",
			Question: "does the invoice correspond to goods that were actually shipped?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the invoice is linked to a specific contract and consignment",
					"contract and B/L references on the invoice",
					0.8, 0.1, true, "trader"),
				cond("C2", "a shipment matching that consignment is evidenced",
					"a bill of lading and an independent shipment observation",
					0.9, 0.4, true, "carrier", "terminal", "AIS provider"),
				cond("C3", "the invoiced quantity matches the shipped quantity within tolerance",
					"the invoice quantity against the B/L and inspection quantities",
					0.85, 0.2, true, "trader", "inspector"),
				cond("C4", "the invoiced price follows the contractual pricing mechanism",
					"the pricing clause applied to the correct period or index",
					0.85, 0.3, true, "contract", "price reporting agency"),
				cond("C5", "the invoice has not already been presented",
					"a search for the same invoice or B/L under another financing",
					0.95, 0.5, true, "bank", "trader"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the invoice corresponds to a real shipment on contractual terms",
					[]string{"a matching B/L", "an independent shipment observation",
						"a reconciled quantity and price"}),
				hyp("H2", "the invoice is duplicated against one shipment",
					[]string{"the same B/L financed twice", "two invoices, one consignment"}),
				hyp("H3", "the pricing was applied on the wrong basis",
					[]string{"an index period inconsistent with the clause",
						"a premium not provided for"}),
				hyp("H4", "no shipment corresponding to the invoice took place",
					[]string{"no vessel movement consistent with the B/L",
						"no terminal record of the parcel"}),
			},
		},
		{
			ID: "FIN-AUTHORIZATION-CHAIN", Domain: "finance",
			Question: "was this payment authorised by somebody entitled to authorise it?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the authority framework is established",
					"the mandate or delegation schedule in force at the time",
					0.9, 0.3, true, "payer", "bank"),
				cond("C2", "the authoriser is identified",
					"the authenticated identity that approved the instruction",
					0.9, 0.3, true, "payer", "bank"),
				cond("C3", "the authoriser held that authority at that time",
					"the mandate as it stood on the date, not as it stands now",
					0.95, 0.3, true, "payer"),
				cond("C4", "the amount is within their limit",
					"the limit applicable to that authoriser and payment type",
					0.85, 0.2, true, "payer"),
				cond("C5", "any dual-authorisation requirement was met",
					"the second approval, from a different person",
					0.9, 0.2, true, "payer", "bank"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the payment was properly authorised",
					[]string{"an identified authoriser", "authority held at the time",
						"the amount within limit", "any second approval present"}),
				hyp("H2", "the authoriser's authority had lapsed or changed",
					[]string{"a mandate amendment before the date",
						"a leaver record preceding the payment"}),
				hyp("H3", "the authorisation was made under a compromised credential",
					[]string{"an authentication from an unusual device or location",
						"the named authoriser's denial"}),
				hyp("H4", "the authority framework is not established on the evidence",
					[]string{"no mandate produced for the period",
						"a mandate produced in its current rather than contemporaneous form"}),
			},
		},
	}
}
