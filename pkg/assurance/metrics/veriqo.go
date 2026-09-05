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

// Software returns what the builder has established alone.
func Software() (*Set, error) {
	return New(SoftwareVerification,
		Measure{Register: SoftwareVerification, Name: "tests", Value: "918",
			Basis: "counted from the module: every function named Test* in a _test.go file",
			Caveat: "a test count is not a quality measure. The cheapest way to raise it " +
				"is to assert what the code already does, and nothing here distinguishes " +
				"those from the tests that would catch a regression"},
		Measure{Register: SoftwareVerification, Name: "adversarial tests", Value: "43",
			Basis: "test/adversarial: tests that assume an attacker and assert the " +
				"structural reason an attack cannot work",
			Caveat: "written by the party that wrote the defences, who knew where they " +
				"looked. This is level A1 of three (developer / independent grey-box / " +
				"black-box), and A2 and A3 have not happened"},
		Measure{Register: SoftwareVerification, Name: "defects found by attacking",
			Value: "4, all fixed",
			Basis: "the adversarial suites' first runs: two in the ledger's tamper " +
				"detection, one in the redaction worker's size handling, and one in the " +
				"verifier itself -- it silently skipped unparseable JSON",
			Caveat: "this is the most informative number in this register, and it is " +
				"also evidence that the earlier tests were not looking in the right " +
				"places. Two of the three were in controls a marketing claim would rest on"},
		Measure{Register: SoftwareVerification, Name: "go vet", Value: "clean",
			Basis: "go vet ./... over the whole module",
			Caveat: "vet catches a small, fixed set of mistakes. It is not a security tool " +
				"and not a correctness tool"},
		Measure{Register: SoftwareVerification, Name: "race detector", Value: "not run",
			Basis: "no -race run is part of the verification script",
			Caveat: "concurrency here is exercised in-process only, so a race run would " +
				"cover very little of what a deployment would exercise"},
		Measure{Register: SoftwareVerification, Name: "fuzzing", Value: "none",
			Basis: "no fuzz targets exist",
			Caveat: "the canonicaliser and the redaction worker are the two obvious " +
				"targets, and neither has been fuzzed"},
		Measure{Register: SoftwareVerification, Name: "mutation testing",
			Value: "partial, by hand",
			Basis: "test/mutation contains hand-written mutants for the invariants the " +
				"failure-class register names",
			Caveat: "hand-written mutants test what their author thought of. No mutation " +
				"tool has been run over the module"},
		Measure{Register: SoftwareVerification, Name: "line coverage", Value: "not measured",
			Basis: "no coverage run is part of the verification script",
			Caveat: "coverage would be easy to add and easy to game, and adding it " +
				"without the caveat would put a fourth inflatable number on the board"},
	)
}

// Assurance returns what somebody other than the builder has
// established. It is empty, and the constructor takes no measures at
// all rather than taking measures with zero values -- a row reading
// "independent assessments: 0" invites a reader to see a scale with a
// low value on it, when what exists is no scale.
func Assurance() (*Set, error) {
	return New(AssuranceQualification)
}

// Production returns what running the system has established. Also
// empty, for the same reason.
func Production() (*Set, error) {
	return New(ProductionEvidence)
}

// VeriqoPanel renders all three.
func VeriqoPanel() (string, error) {
	sv, err := Software()
	if err != nil {
		return "", err
	}
	aq, err := Assurance()
	if err != nil {
		return "", err
	}
	pe, err := Production()
	if err != nil {
		return "", err
	}
	out, err := Panel(sv, aq, pe)
	if err != nil {
		return "", err
	}
	return out + fmt.Sprintf("\n%s\n", closing), nil
}

const closing = "The first register is the only one VERIQO can move on its own, and it " +
	"is\nthe only one with anything in it. That is not a coincidence and it is not\n" +
	"a criticism of the work: it is the shape every system has before somebody\n" +
	"else has looked at it. The registers are kept apart so that shape stays\n" +
	"visible instead of averaging away."
