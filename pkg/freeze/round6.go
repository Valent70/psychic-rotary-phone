package freeze

// Round6 is this round's work, weighed against the freeze it declares.
//
// The audit that opened Round 6 made one finding above all others:
// VERIQO was becoming a machine for verifying its own verification
// machinery, and had to stop. So the first test of the freeze is
// whether Round 6 obeyed it, and the register below is written to be
// checked rather than to look disciplined.
//
// Two entries are REFUSED, and both are things this round wanted to
// build. That is the part worth reading.
func Round6() (*Register, error) {
	return NewRegister(6,
		Item{
			Name:    "commercial decision passport (pkg/passport/commercial)",
			Warrant: CustomerPilot, Serves: "B-CORPUS / customer pilot",
			Why: "A pilot customer is shown a conclusion and asked to trust it. The " +
				"passport is the artefact that carries the reasoning, the sources, the " +
				"contradictions and the disproof route; without it a pilot is a demo. " +
				"It is also what the buyer actually buys -- nobody purchases an " +
				"epistemic firewall.",
			Built: true,
		},
		Item{
			Name:    "maritime flagship case (pkg/intel/maritime/flagship)",
			Warrant: CustomerPilot, Serves: "B-AIS / customer pilot",
			Why: "The pilot conversation needs one end-to-end case a maritime reader " +
				"can follow from AIS report to disproof route. Without a worked case " +
				"the data partner is being asked to supply data for a system whose " +
				"output they have never seen.",
			Built: true,
		},
		Item{
			Name:    "ugly-world triage (pkg/intel/maritime, spoof/jam/fault)",
			Warrant: ExternalGate, Serves: "G9: independent real-document corpus",
			Why: "The corpus partner's data will contain spoofed and jammed AIS, " +
				"duplicate reports and wrong clocks. A system that reports 'SPOOFING' " +
				"where jamming and equipment fault are equally consistent will be " +
				"wrong in public on the partner's own data, and there is no second " +
				"first impression with a data partner.",
			Built: true,
		},
		Item{
			Name:    "copy detection for source independence (pkg/qualification/independence)",
			Warrant: ExternalGate, Serves: "G10: external evidence validation",
			Why: "Independence is the dimension an external validator will attack " +
				"first, because false corroboration is the classic failure: fifty " +
				"copies of one wire story read as fifty sources. Counting producers " +
				"does not catch it when the copies do not declare their origin.",
			Built: true,
		},
		Item{
			Name:    "the assurance boundary (pkg/assurance/boundary)",
			Warrant: ExternalGate, Serves: "B-FREEZE",
			Why: "B-FREEZE is the first blocker on the critical path and it is a " +
				"decision, not a build. The boundary is what makes the decision " +
				"checkable: without it, 'we have stopped adding assurance' is a " +
				"statement of intent that the next round's good argument overturns.",
			Built: true,
		},
		Item{
			Name:    "the freeze register (pkg/freeze)",
			Warrant: ExternalGate, Serves: "B-FREEZE",
			Why: "The freeze is only real if a proposal has to name what it serves. " +
				"This register is that requirement, applied first to this round.",
			Built: true,
		},
		Item{
			Name:    "headline metrics with denominators (pkg/dashboard)",
			Warrant: ExternalGate, Serves: "G10: external evidence validation",
			Why: "An assessor reads the headline first. A headline of '37 tests " +
				"passed' answers a question nobody outside engineering asked and " +
				"hides the nine figures that are all zero. The zeroes are the honest " +
				"headline and an assessor needs them stated with their denominators.",
			Built: true,
		},
		Item{
			Name:    "the five battlefields as procurement items",
			Warrant: ExternalGate, Serves: "B-PENTEST",
			Why: "The audit named five battlefields. Four of them are purchases, and " +
				"a battlefield that is not on the procurement graph is an intention.",
			Built: true,
		},

		// ---- Refused. Both were wanted. ----
		Item{
			Name:    "a verifier for veriqo-verify",
			Warrant: Discretionary,
			Why: "REFUSED. This is layer 4 of the assurance ladder and it is exactly " +
				"the runaway the audit named. A verifier VERIQO writes to check the " +
				"verifier VERIQO wrote shares every assumption of what it checks, so " +
				"it costs engineering time and widens nothing. The party who should " +
				"do this is an independent verifier running the published protocol.",
			Built: false,
		},
		Item{
			Name:    "generalised temporal-reasoning engine over the entity graph",
			Warrant: Discretionary,
			Why: "REFUSED. It is the most interesting piece of engineering left in " +
				"the system and no external gate is waiting on it. Under the freeze " +
				"that settles it, and the fact that it is interesting is precisely " +
				"why the rule has to be external to the judgement.",
			Built: false,
		},
	)
}
