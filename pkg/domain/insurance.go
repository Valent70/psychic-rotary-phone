package domain

import (
	"veriqo/pkg/hypothesis"
	"veriqo/pkg/reverseproof"
)

// Insurance is where VERIQO's neutrality is most easily lost.
//
// # What VERIQO says, and what it does not
//
// The specification is unambiguous. VERIQO does not say:
//
//	covered / not covered
//
// It says:
//
//	a coverage provision is potentially applicable
//	evidence supporting applicability
//	evidence contradicting applicability
//	missing policy evidence
//	unresolved questions of interpretation
//
// The reason is not timidity. A coverage determination is a contractual
// and often a legal act, made by a party with standing -- the insurer,
// an adjuster acting under authority, ultimately a tribunal. A neutral
// evidence platform that made it would have taken one side's position
// while presenting itself as taking none, which destroys the only
// thing that makes its output useful to both parties.
//
// So the templates decompose APPLICABILITY, and the domain's refusals
// (in domain.go) forbid the determination itself.

// InsuranceTemplates returns the insurance claim shapes.
func InsuranceTemplates() []Template {
	return []Template{
		{
			ID: "INS-COVERAGE-APPLICABILITY", Domain: "insurance",
			Question: "is this coverage provision potentially applicable to this loss?",
			Conditions: []reverseproof.Condition{
				cond("C1", "a policy was in force at the time of the loss",
					"the policy schedule with inception and expiry covering the loss date",
					0.95, 0.2, true, "insurer", "broker"),
				cond("C2", "the subject matter is within the insured interest",
					"the schedule's description of the insured property or interest",
					0.9, 0.2, true, "insurer", "broker"),
				cond("C3", "the loss falls within the period and territory insured",
					"the loss location and date against the policy's geographical limits",
					0.85, 0.2, true, "insurer", "survey"),
				cond("C4", "the peril alleged is among the insured perils",
					"the operative clause and the peril the evidence supports",
					0.9, 0.3, true, "insurer", "survey"),
				cond("C5", "no exclusion on its face removes the loss",
					"each exclusion assessed against the evidenced facts",
					0.9, 0.4, true, "insurer", "survey"),
				cond("C6", "conditions precedent were complied with",
					"notice, sue-and-labour and any warranty compliance records",
					0.8, 0.5, true, "insurer", "assured", "broker"),
				cond("C7", "the proximate cause is identified on the evidence",
					"a causal chain supported by survey and contemporaneous records",
					0.95, 0.7, true, "survey", "vessel log", "weather provider"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the provision is potentially applicable on the evidence available",
					[]string{"a policy in force", "an insured peril evidenced",
						"no exclusion evidenced", "conditions precedent evidenced"}),
				hyp("H2", "an exclusion is potentially engaged",
					[]string{"facts falling within an exclusion's wording",
						"a proximate cause within an excluded category"}),
				hyp("H3", "the loss arises from a peril not insured under this provision",
					[]string{"a causal chain leading outside the operative clause"}),
				hyp("H4", "the question turns on an unresolved point of construction",
					[]string{"wording capable of two readings on the evidenced facts",
						"authority pointing both ways", "no contemporaneous conduct resolving it"}),
				hyp("H5", "the policy evidence is incomplete",
					[]string{"an unproduced endorsement", "an unproduced slip or wording",
						"a broker file not obtained"}),
			},
			Refusals: []string{
				"applicability is not coverage: whether the policy responds is the insurer's " +
					"determination and, if disputed, a tribunal's",
			},
		},
		{
			ID: "INS-CAUSATION", Domain: "insurance",
			Question: "what was the proximate cause of this loss?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the loss is evidenced and quantified",
					"a survey report and a quantification of the damage",
					0.8, 0.4, true, "survey"),
				cond("C2", "a candidate cause is evidenced",
					"contemporaneous records of the event alleged to have caused it",
					0.9, 0.5, true, "vessel log", "survey", "weather provider"),
				cond("C3", "the cause preceded the loss",
					"a timeline placing the event before the damage was observed",
					0.85, 0.2, true, "vessel log", "port authority"),
				cond("C4", "a physical mechanism connects them",
					"an engineering or metocean explanation of how the event produced the damage",
					0.95, 0.7, true, "survey", "expert"),
				cond("C5", "concurrent causes have been considered",
					"each other candidate cause assessed and its contribution stated",
					0.9, 0.5, true, "survey", "expert"),
				cond("C6", "the loss is not explained by pre-existing condition",
					"the condition of the subject matter before the event",
					0.85, 0.6, true, "survey", "class", "maintenance records"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the alleged event proximately caused the loss",
					[]string{"a mechanism", "correct sequence", "no sufficient alternative"}),
				hyp("H2", "a pre-existing condition caused or substantially contributed",
					[]string{"prior damage", "deferred maintenance", "a class recommendation"}),
				hyp("H3", "two causes operated concurrently",
					[]string{"evidence of both", "damage inconsistent with either alone"}),
				hyp("H4", "the causal question cannot be resolved on the surviving evidence",
					[]string{"the subject matter was disposed of before survey",
						"no contemporaneous record of the event"}),
			},
		},
		{
			ID: "INS-QUANTUM-SUPPORT", Domain: "insurance",
			Question: "is the quantum claimed supported by the evidence?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the measure of indemnity under the policy is identified",
					"the valuation clause or the applicable measure",
					0.9, 0.2, true, "insurer", "broker"),
				cond("C2", "the quantity or value lost is measured on a stated basis",
					"a survey or account establishing the loss on a comparable basis",
					0.95, 0.4, true, "survey"),
				cond("C3", "the loss measurement is comparable to the pre-loss measurement",
					"both figures on the same method, temperature and standard",
					0.95, 0.3, true, "survey"),
				cond("C4", "deductibles, franchises and salvage are accounted for",
					"the schedule's deductible and any salvage or recovery realised",
					0.7, 0.2, true, "insurer"),
				cond("C5", "the claim is not duplicated under another head",
					"a reconciliation across heads of claim",
					0.8, 0.3, true, "insurer", "assured"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the quantum is supported on the stated measure",
					[]string{"comparable measurements", "an identified measure of indemnity"}),
				hyp("H2", "part of the difference is a measurement artefact",
					[]string{"differing measurement bases", "a temperature or density difference"}),
				hyp("H3", "the quantum is overstated by duplication",
					[]string{"the same loss under two heads", "salvage not credited"}),
				hyp("H4", "the measure of indemnity is disputed",
					[]string{"a valuation clause capable of two readings",
						"market and contract values diverging materially"}),
			},
		},
	}
}
