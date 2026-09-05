package dashboard

// Veriqo is VERIQO's headline, as of Round 6.
//
// Eight of the nine read zero and the ninth has no denominator. That
// is not a bad quarter; it is what a system looks like before anybody
// outside it has done anything, and the board is built so that fact
// cannot be dressed up.
//
// The three figures VERIQO could put a number against -- replay
// determinism, cross-tenant isolation, disproof-route availability --
// are marked INTERNAL_SCOPE_ONLY, because they are counted over
// material VERIQO wrote. "100% replay determinism" over VERIQO's own
// fixtures is a statement about the fixtures.
func Veriqo() (*Board, error) {
	return NewBoard(
		Measure{
			Name: "Critical controls externally validated", State: Counted,
			Numerator: 0, Denominator: 20,
			Scope: "the twenty mandatory production gates; a control counts only when " +
				"a party outside VERIQO has examined it and is willing to say so",
			Blocks:    "release; the scorecard refuses it for nine stated reasons",
			MovableBy: "an accredited assessor, a cryptographer, and a red team",
		},
		Measure{
			Name: "Real-world corpus coverage", State: NotMeasured,
			Scope: "documents VERIQO did not construct. There are 23 internal fixtures " +
				"and they are not a sample of anything; the denominator is unknown " +
				"until a corpus partner defines the population",
			Blocks:    "G9, and every claim about redaction behaviour on real material",
			MovableBy: "a corpus partner supplying documents with usage rights attached",
		},
		Measure{
			Name: "Independent assessments completed", State: Counted,
			Numerator: 0, Denominator: 5,
			Scope: "the five assessments the gates require: penetration test, " +
				"cryptography review, redaction adversarial lab, legal opinion per " +
				"jurisdiction, model evaluation against a set VERIQO did not build",
			Blocks:    "G4, G15-G18, ED-001, ED-010",
			MovableBy: "five separate firms; none of them is engaged",
		},
		Measure{
			Name: "Production operational evidence (days)", State: Counted,
			Numerator: 0, Denominator: 30,
			Scope: "consecutive days of multi-host operation with failure injection. " +
				"The longest run to date is seconds, in one process, on one host",
			Blocks:    "G3, G5, G6, G11, and the operationally-proven rung",
			MovableBy: "an infrastructure provider and thirty days of calendar time",
		},
		Measure{
			Name: "Replay determinism", State: Internal,
			Numerator: 1, Denominator: 1,
			Scope: "one synthetic case, replayed in-process. It establishes that the " +
				"machinery is deterministic and establishes nothing about determinism " +
				"across hosts, clock skew, or dependency drift",
			Blocks: "nothing yet; it becomes meaningful at multi-host scope",
			MovableBy: "an infrastructure provider; re-running one process on one host " +
				"cannot raise this, however many times it is done",
		},
		Measure{
			Name: "Unauthorised authority attempts blocked", State: NotMeasured,
			Scope: "attempts by a real principal in a real deployment. There is no " +
				"deployment, so there is no denominator. VERIQO's own adversarial " +
				"suite blocks the attempts it was written to block, and reporting " +
				"that as this measure would be counting the author's imagination",
			Blocks:    "G15-G18",
			MovableBy: "a red team that did not write the defence, and real operation",
		},
		Measure{
			Name: "Cross-tenant isolation violations", State: Internal,
			Numerator: 0, Denominator: 7,
			Scope: "the seven cross-tenant attacks in VERIQO's own adversarial suite, " +
				"run in-process. A zero means the attacks VERIQO thought of did not " +
				"succeed, which is a statement about the attacks",
			Blocks:    "G5, and any multi-tenant pilot",
			MovableBy: "an independent red team and a multi-host deployment",
		},
		Measure{
			Name: "Evidence provenance completeness", State: Internal,
			Numerator: 1, Denominator: 1,
			Scope: "the synthetic flagship case, where every observation carries a " +
				"custody record because VERIQO constructed it that way. Real feeds " +
				"arrive with gaps and this figure will fall",
			Blocks:    "G2, G10",
			MovableBy: "a live data contract, at which point the denominator becomes real",
		},
		Measure{
			Name: "Disproof routes available", State: Internal,
			Numerator: 1, Denominator: 1,
			Scope: "findings in the synthetic flagship case that carry a walkable " +
				"disproof route. Every finding VERIQO can currently produce is one it " +
				"also constructed the route for",
			Blocks:    "nothing yet; it is the measure a customer pilot will test",
			MovableBy: "a pilot producing findings VERIQO did not design in advance",
		},
	)
}
