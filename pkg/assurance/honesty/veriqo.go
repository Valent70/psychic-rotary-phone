package honesty

// Veriqo is the honest grading of VERIQO's own honesty checks.
//
// Every check in verify.sh and in the passport layer is graded at the
// level it actually reaches. The uncomfortable results are the point:
// the passport conclusion screen is H1, and the whole suite tops out
// below the level that catches a deliberate overclaim.
func Veriqo() (*Suite, error) {
	return NewSuite(
		Check{Name: "passport conclusion screen", Level: H1,
			Performs: "matches the statement and scope text against a per-kind list of " +
				"phrases, with copulas normalised and disclaiming clauses exempt",
			Where: "pkg/passport.CheckKind"},

		Check{Name: "coverage figures carry ESTIMATE", Level: H1,
			Performs: "greps the corpus report for the word ESTIMATE",
			Where:    "scripts/verify.sh"},

		Check{Name: "self-doubt register names its attacker", Level: H1,
			Performs: "greps the claims report for the sentence stating who ran the " +
				"disproof paths",
			Where: "scripts/verify.sh"},

		Check{Name: "readiness offers no aggregate", Level: H1,
			Performs: "greps the readiness report for a percent sign",
			Where:    "scripts/verify.sh"},

		Check{Name: "every assurance claim has a disproof path", Level: H2,
			Performs: "requires the field, and refuses a disproof path that restates the " +
				"assertion",
			Where: "pkg/assurance/register.Claim.Validate"},

		Check{Name: "every evidence debt has an owner and a risk", Level: H2,
			Performs: "requires both fields on every debt and counts them in the script",
			Where:    "pkg/assurance/register.Debt.Validate, scripts/verify.sh"},

		Check{Name: "every measure states what it does not show", Level: H2,
			Performs: "requires a caveat on every metric, at every register",
			Where:    "pkg/assurance/metrics.Measure.Validate"},

		Check{Name: "a threat model states what it did not consider", Level: H2,
			Performs: "requires the not_considered list and a residual risk on every threat",
			Where:    "pkg/assurance/capsule"},

		Check{Name: "no cited test is missing", Level: H3,
			Performs: "parses the module and checks every test name cited by a " +
				"failure-class invariant is a function that exists, rather than a name in " +
				"a comment",
			Where: "test/architecture"},

		Check{Name: "no surface names a high assurance state", Level: H3,
			Performs: "parses every file outside the assurance layer and fails on a string " +
				"literal naming a state that requires an outside party",
			Where: "test/architecture"},

		Check{Name: "the register's level matches its evidence", Level: H4,
			Performs: "refuses a claim whose current level is not supported by cited " +
				"evidence of the class that level requires, with an independent validator " +
				"where the class needs one",
			Where: "pkg/assurance/register.Claim.Validate"},

		Check{Name: "the emitted state is derived from evidence", Level: H4,
			Performs: "recomputes the supportable state from the evidence held and returns " +
				"the lower of it and the claim, at every enumerated surface",
			Where: "pkg/assurance/invariant.Emit"},

		Check{Name: "the verifier derives rather than reads", Level: H4,
			Performs: "recomputes every digest, rehashes the ledger from genesis, " +
				"recomputes the passport digest before checking its signature, and derives " +
				"the qualification state before comparing it to the claim",
			Where: "pkg/verification, cmd/veriqo-verify"},
	)
}
