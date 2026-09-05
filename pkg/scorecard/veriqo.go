package scorecard

import "veriqo/pkg/gates"

// VeriqoScorecard is this repository's honest self-assessment.
//
// # Read the colours carefully
//
// Six dimensions are YELLOW. That is not a hedge. YELLOW here means:
// the mechanism exists, it is tested, and no party outside VERIQO has
// examined it. Marking any of them GREEN would redefine GREEN to mean
// "we tested it ourselves", which is the specific inflation the whole
// system is built to refuse.
//
// Three are RED. Each is RED because something a customer would
// reasonably assume is present is absent, and the basis says which.
//
// Nothing is GREEN. That is the correct state for a system that has
// never met an outside party, and a scorecard that showed otherwise
// would be the artefact this package exists to prevent.

// MandatoryGates are the gates that must be satisfied for any
// production release, whatever the dimension colours say.
//
// They are the ones where a failure is not a degraded service but an
// unsafe one: an unattested key root, an unattested workload identity,
// data held under unknown terms, and an unqualified model.
func MandatoryGates() []string {
	return []string{"G1", "G2", "G7", "G8", "G19", "G20"}
}

// Veriqo builds the current scorecard.
func Veriqo() (*Scorecard, error) {
	g, err := gates.VeriqoRegister()
	if err != nil {
		return nil, err
	}
	return New(g, MandatoryGates(),
		Assessment{Dimension: EvidenceIntegrity, Rating: Yellow,
			Basis: "versioning, provenance and custody are implemented and tested: the raw " +
				"acquisition is immutable, every derivative states what it dropped, and a " +
				"custody break is detected from recorded digests and is permanent",
			Gaps: []string{
				"no evidence provider has confirmed that material VERIQO holds is what they " +
					"supplied (G10)",
				"the redaction corpus is entirely VERIQO-built; the 88% weighted figure is " +
					"an ESTIMATE (G9)",
				"no adversarial recovery attempt has been made against a redacted derivative",
				"an internal adversarial test found the worker silently TRUNCATING an " +
					"oversized part and releasing it as verified; it now refuses, but the " +
					"defect was found by the people who wrote the code (G9, G15)",
			}},

		Assessment{Dimension: EntityIntegrity, Rating: Yellow,
			Basis: "resolution produces five outcomes rather than a merge, contradiction is " +
				"a veto rather than a weight, and a reassignable identifier cannot carry a " +
				"merge without a reviewer",
			Gaps: []string{
				"the thresholds (0.85 / 0.45) are VERIQO's stated choice, not a measured " +
					"optimum over real data",
				"no false-merge rate has been measured against a labelled population (G2)",
			}},

		Assessment{Dimension: ReasoningIntegrity, Rating: Yellow,
			Basis: "every claim carries a disproof path, contradictions demote automatically, " +
				"the reverse proof has no CONFIRMED verdict, hypotheses are scored as a set, " +
				"and the confidence vector has no aggregate number",
			Gaps: []string{
				"no case has been run against evidence VERIQO did not construct",
				"the diagnosticity and prevalence weights are stated estimates",
			}},

		// RED: the security posture is designed and self-tested, and
		// the two things that would make it assessable are absent.
		Assessment{Dimension: Security, Rating: Red,
			Basis: "the controls are implemented -- cryptographic tenant anchoring, a derived " +
				"key hierarchy, a deny-overrides policy core no rule can override, and a " +
				"tool firewall whose grants cannot be widened -- and an internal adversarial " +
				"suite attacks each of them, finding and closing two defects in the process; " +
				"the key root is still a software TEST DOUBLE and no outside party has " +
				"attacked any of it (G1, G4, G7, G15, G16, G17)"},

		Assessment{Dimension: OperationalReliability, Rating: Red,
			Basis: "the ledger is durable, survives a torn write, and refuses to open a log " +
				"whose records were edited or removed -- a distinction an internal " +
				"adversarial test found it was NOT making, and which is now closed -- and " +
				"there is no multi-region deployment, no timed disaster recovery, no " +
				"multi-host run and no soak beyond minutes (G3, G5, G6, G11, G12)"},

		Assessment{Dimension: DataRights, Rating: Yellow,
			Basis: "the six licence questions are asked separately, a derivative takes the " +
				"intersection of its sources, and no source may be configured without a " +
				"licence and a named producer",
			Gaps: []string{
				"no commercial licence has been encoded from real terms (G2)",
				"purpose limitation has never been exercised against a licensor's actual " +
					"restrictions",
			}},

		Assessment{Dimension: AIGovernance, Rating: Yellow,
			Basis: "an AI artefact rises one level at a time, never by its producer, and " +
				"QUALIFIED is unreachable by automation under any policy; models enter at " +
				"DEVELOPMENT and reach QUALIFIED only on an evaluation over data VERIQO did " +
				"not create",
			Gaps: []string{
				"the injection defence has been designed and self-tested, not attacked (G17)",
				"no model has actually been qualified, because no external evaluation set " +
					"exists (G20)",
			}},

		Assessment{Dimension: Replayability, Rating: Yellow,
			Basis: "canonicalisation is RFC 8785, no deterministic path reads the wall clock " +
				"or a random source, and a replay that re-executed nothing reports that it " +
				"establishes nothing",
			Gaps: []string{
				"no replay has been performed against a restored system (G12)",
				"nondeterministic steps are replayed from a recording, which establishes the " +
					"pipeline's behaviour given those outputs and not that they would recur",
			}},

		// RED, and it is the one that governs the others.
		Assessment{Dimension: ExternalValidation, Rating: Red,
			Basis: "no party outside VERIQO has examined, attacked, validated or corroborated " +
				"any part of this system. Thirteen of the twenty gates require one and none " +
				"is satisfied. Every other dimension's YELLOW is bounded by this RED"},
	)
}
