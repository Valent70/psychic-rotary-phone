package metrics

import "fmt"

// VERIQO's own three registers, as of the assurance register's
// assessment date.
//
// The numbers in the first are real and were counted from this
// repository. The second is empty. The third is empty. Those two
// facts, side by side with the first, are the whole point of the
// package: a reader who finds the first section reassuring has made
// the error the separation exists to prevent.

// Engineering returns what the code establishes about itself.
func Engineering() (*Set, error) {
	return New(EngineeringIntegrity,
		Measure{Register: EngineeringIntegrity, Name: "tests", Value: "918",
			Basis: "counted from the module: every function named Test* in a _test.go file",
			Caveat: "a test count is not a quality measure. The cheapest way to raise it " +
				"is to assert what the code already does, and nothing here distinguishes " +
				"those from the tests that would catch a regression"},
		Measure{Register: EngineeringIntegrity, Name: "adversarial tests", Value: "43",
			Basis: "test/adversarial: tests that assume an attacker and assert the " +
				"structural reason an attack cannot work",
			Caveat: "written by the party that wrote the defences, who knew where they " +
				"looked. This is level A1 of three (developer / independent grey-box / " +
				"black-box), and A2 and A3 have not happened"},
		Measure{Register: EngineeringIntegrity, Name: "defects found by attacking",
			Value: "4, all fixed",
			Basis: "the adversarial suites' first runs: two in the ledger's tamper " +
				"detection, one in the redaction worker's size handling, and one in the " +
				"verifier itself -- it silently skipped unparseable JSON",
			Caveat: "this is the most informative number in this register, and it is " +
				"also evidence that the earlier tests were not looking in the right " +
				"places. Two of the three were in controls a marketing claim would rest on"},
		Measure{Register: EngineeringIntegrity, Name: "go vet", Value: "clean",
			Basis: "go vet ./... over the whole module",
			Caveat: "vet catches a small, fixed set of mistakes. It is not a security tool " +
				"and not a correctness tool"},
		Measure{Register: EngineeringIntegrity, Name: "race detector", Value: "not run",
			Basis: "no -race run is part of the verification script",
			Caveat: "concurrency here is exercised in-process only, so a race run would " +
				"cover very little of what a deployment would exercise"},
		Measure{Register: EngineeringIntegrity, Name: "fuzzing", Value: "none",
			Basis: "no fuzz targets exist",
			Caveat: "the canonicaliser and the redaction worker are the two obvious " +
				"targets, and neither has been fuzzed"},
		Measure{Register: EngineeringIntegrity, Name: "mutation testing",
			Value: "partial, by hand",
			Basis: "test/mutation contains hand-written mutants for the invariants the " +
				"failure-class register names",
			Caveat: "hand-written mutants test what their author thought of. No mutation " +
				"tool has been run over the module"},
		Measure{Register: EngineeringIntegrity, Name: "line coverage", Value: "not measured",
			Basis: "no coverage run is part of the verification script",
			Caveat: "coverage would be easy to add and easy to game, and adding it " +
				"without the caveat would put a fourth inflatable number on the board"},
	)
}

// Epistemic returns how honestly the system reasons.
//
// This board is the one most systems do not have, and it is the one
// VERIQO is actually about. Its entries are properties the builder CAN
// establish -- they are about the system's own behaviour, not about
// the world -- which is why it is not empty, and why it must never be
// read as a substitute for the third board.
func Epistemic() (*Set, error) {
	return New(EpistemicIntegrity,
		Measure{Register: EpistemicIntegrity, Name: "unknown handling",
			Value: "four inequalities, enforced",
			Basis: "unreadable != verified, unparseable != absent, missing != valid, " +
				"unknown != negative; the zero epistemic state is UNEXAMINED",
			Caveat: "the inequalities are enforced where they are called. A code path " +
				"that never asks cannot be protected by an answer it did not request"},
		Measure{Register: EpistemicIntegrity, Name: "source independence",
			Value: "producer graph, not source count",
			Basis: "derivation edges are walked to their roots, so five outlets carrying " +
				"one wire story resolve to one producer",
			Caveat: "the edges are supplied by whoever configures the sources. Nothing " +
				"here verifies that a declared origin is the real one"},
		Measure{Register: EpistemicIntegrity, Name: "unassessable handling",
			Value: "its own value, satisfying no threshold",
			Basis: "a structure containing an unattributable source is UNASSESSABLE, and " +
				"SatisfiesCorroboration returns false for it at every threshold",
			Caveat: "this makes the system conservative, not correct: a genuinely " +
				"independent source with poor paperwork is treated the same as a fabricated one"},
		Measure{Register: EpistemicIntegrity, Name: "contradiction handling",
			Value: "veto, not weight",
			Basis: "a contradiction vetoes a merge rather than lowering a score, and is " +
				"classified as knowledge rather than as a fault",
			Caveat: "detecting a contradiction requires both sides to be present. Nothing " +
				"detects a contradiction with evidence nobody acquired"},
		Measure{Register: EpistemicIntegrity, Name: "evidence provenance",
			Value: "producer resolved from the path, not asserted",
			Basis: "SOURCE is distinguished from PRODUCER, and ProducerID walks to the " +
				"first observer so processing cannot launder origin",
			Caveat: "the hop path is a record. Whether the named parties actually handled " +
				"the material is not checkable from inside the system"},
		Measure{Register: EpistemicIntegrity, Name: "hypothesis separation",
			Value: "five typed rungs, citations checked",
			Basis: "OBSERVATION, INFERENCE, HYPOTHESIS, ASSERTION, DECISION are distinct " +
				"types; an assertion may not rest directly on an observation, and VERIQO " +
				"may not record a decision at all",
			Caveat: "the ladder constrains what can be RECORDED. A person who writes a " +
				"hypothesis into an observation's text field has defeated it"},
		Measure{Register: EpistemicIntegrity, Name: "decision traceability",
			Value: "foundation reachable from any statement",
			Basis: "Foundation() returns the observations any statement rests on, and " +
				"JudgementDistance counts the hypothesis rungs between them",
			Caveat: "traceability within a case. It says nothing about evidence that was " +
				"never brought into the case"},
		Measure{Register: EpistemicIntegrity, Name: "challengeability",
			Value: "executable, not documentary",
			Basis: "the capsule carries an independent verifier, a worked case, expected " +
				"outputs, negative cases and a ranked list of our own weakest points",
			Caveat: "nobody outside has run it. An invitation nobody has accepted is an " +
				"invitation, not a result"},
	)
}

// External returns what somebody other than the builder has
// established, or what running the system has shown.
//
// It is empty, and the constructor takes no measures at all rather
// than measures with zero values -- a row reading "independent
// assessments: 0" invites a reader to see a scale with a low value on
// it, when what exists is no scale.
func External() (*Set, error) {
	return New(ExternalQualification)
}

// VeriqoPanel renders all three.
func VeriqoPanel() (string, error) {
	sv, err := Engineering()
	if err != nil {
		return "", err
	}
	aq, err := Epistemic()
	if err != nil {
		return "", err
	}
	pe, err := External()
	if err != nil {
		return "", err
	}
	out, err := Panel(sv, aq, pe)
	if err != nil {
		return "", err
	}
	return out + fmt.Sprintf("\n%s\n", closing), nil
}

const closing = "The first two boards are the ones VERIQO can move on its own, and they " +
	"are\nthe only two with anything in them. That is not a coincidence and it is not\n" +
	"a criticism of the work: it is the shape every system has before somebody\n" +
	"else has looked at it.\n\n" +
	"The middle board is the one that makes VERIQO different, and it is also the\n" +
	"one most easily mistaken for the third. Reasoning honestly about what you\n" +
	"do not know is not the same as somebody else confirming you were right."
