package capsule

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/verification"
)

// Executable challengeability.
//
// # The failure this closes
//
// "We invite challenge" is easy to say and usually means an assessor
// receives a PDF, an architecture diagram and a summary of test
// results. None of those can be run. The assessor's only options are
// to believe the document or to ask for a demonstration that the
// assessed party also runs -- which is where they started.
//
// Challengeability that means anything requires the assessor to be
// able to say "I do not believe you" and then:
//
//	RUN -> VERIFY -> ATTACK -> COMPARE -> REPORT
//
// Each of those verbs needs something specific in the package:
//
//	RUN      a reproducible input and a command
//	VERIFY   an independent verifier, and digests to check against
//	ATTACK   negative cases with the outcome each must produce
//	COMPARE  expected outputs, stated in advance
//	REPORT   a statement of what a negative result means
//
// # Why expected outputs must be stated in advance
//
// A package that ships inputs and a program, and lets the assessor
// discover what the program produces, cannot be failed: whatever comes
// out is what it does. Stating the expected output first is what turns
// a demonstration into a test -- and it is the assessor's protection
// against a subtly wrong answer that looks plausible.
type Expectation struct {
	// ID is citable in a report.
	ID string `json:"id"`
	// Command is what to run, verbatim.
	Command string `json:"command"`
	// Expect describes the result in words a reader can check against
	// what they see.
	Expect string `json:"expect"`
	// MustContain are strings that must appear in the output. They are
	// deliberately phrases rather than exact output: an exact match
	// would break on a whitespace change and teach the assessor to
	// ignore the check.
	MustContain []string `json:"must_contain,omitempty"`
	// MustNotContain are strings whose presence is a failure.
	MustNotContain []string `json:"must_not_contain,omitempty"`
	// ExitZero states whether the command should succeed.
	ExitZero bool `json:"exit_zero"`
	// IfThisFails says what an unexpected result would mean, so a
	// finding arrives with its significance already attached.
	IfThisFails string `json:"if_this_fails"`
}

// NegativeCase is an attack with the outcome it must produce.
//
// A challenge package containing only things that pass is a
// demonstration. The negative cases are what make it a test: each is
// a deliberate corruption, and each names the refusal it must produce.
type NegativeCase struct {
	ID string `json:"id"`
	// Do is the corruption, described precisely enough to perform.
	Do string `json:"do"`
	// MustProduce is the outcome. A case whose expected outcome is
	// "an error" is not specific enough to fail.
	MustProduce string `json:"must_produce"`
	// IfItPasses is the finding. This is the sentence an assessor
	// would put in their report, written for them in advance so that
	// its significance is not left to their judgement of a system they
	// have just met.
	IfItPasses string `json:"if_it_passes"`
}

// KnownFailureMode is something that is wrong and has not been fixed.
//
// Shipping these is the part most organisations will not do, and it is
// the part that makes the rest credible: an assessor who finds a
// documented weakness learns the documentation is honest, and an
// assessor who finds an undocumented one has found something real.
type KnownFailureMode struct {
	ID string `json:"id"`
	// What is wrong.
	What string `json:"what"`
	// Consequence is what it costs.
	Consequence string `json:"consequence"`
	// WhyNotFixed is the honest reason. "We have not got to it" is an
	// acceptable answer and is more informative than silence.
	WhyNotFixed string `json:"why_not_fixed"`
	// Debt is the evidence debt that owns it, where one does.
	Debt string `json:"debt,omitempty"`
}

// ChallengeKit is the executable half of the challenge package.
type ChallengeKit struct {
	// Protocol is the five verbs, stated so the assessor knows the
	// shape of what they are being asked to do.
	Protocol []string `json:"protocol"`
	// Expectations turn a demonstration into a test.
	Expectations []Expectation `json:"expectations"`
	// NegativeCases are the attacks that must be refused.
	NegativeCases []NegativeCase `json:"negative_cases"`
	// KnownFailureModes are what is wrong and unfixed.
	KnownFailureModes []KnownFailureMode `json:"known_failure_modes"`
	// InputDigests tie every reproducible input to its bytes, so an
	// assessor can tell whether they are running what we described.
	InputDigests map[string]string `json:"input_digests"`
	// NegativeResultMeaning states what it means if the assessor finds
	// nothing, which is the result most likely to be misreported.
	NegativeResultMeaning string `json:"negative_result_meaning"`
}

// VeriqoChallengeKit is the kit for this repository.
func VeriqoChallengeKit() ChallengeKit {
	return ChallengeKit{
		Protocol: []string{
			"RUN     -- every command below, from the source in this capsule",
			"VERIFY  -- veriqo-verify, ideally with your own canonicaliser and keys",
			"ATTACK  -- the negative cases; each must produce the stated refusal",
			"COMPARE -- what you saw against the expectations stated in advance",
			"REPORT  -- including a negative result, with the scope you covered",
		},

		Expectations: []Expectation{
			{ID: "X-01", Command: "go run ./cmd/veriqo-verify ../capsule",
				Expect:   "every step PASS except revocation, which is UNVERIFIABLE without a list you supply",
				ExitZero: true,
				MustContain: []string{
					"derived qualification: INTERNALLY_ASSURED",
					"It is NOT a qualification of VERIQO",
					"key authenticity cannot be established",
				},
				MustNotContain: []string{"PRODUCTION_QUALIFIED", "EXTERNALLY_VALIDATED"},
				IfThisFails: "either the capsule is not the one we built, or the verifier " +
					"accepts something it should not. Both are findings"},

			{ID: "X-02", Command: "go test ./...",
				Expect: "every package passes", ExitZero: true,
				MustNotContain: []string{"FAIL"},
				IfThisFails: "a failing test in a delivered capsule means the build was " +
					"not what was described"},

			{ID: "X-03", Command: "./scripts/verify.sh",
				Expect:   "all checks pass, and a list of things it explicitly does NOT run",
				ExitZero: true,
				MustContain: []string{"not run", "independent red team",
					"H5 external review of claims"},
				IfThisFails: "if the not-run list has shrunk without an external party " +
					"having acted, the script has started overstating itself"},

			{ID: "X-04", Command: "go run ./cmd/veriqoctl assurance",
				Expect: "no gate closable, release refused, every mandatory gate resting " +
					"on VERIQO's own evidence", ExitZero: true,
				MustContain: []string{"NOT PERMITTED",
					"rest entirely on VERIQO's own evidence"},
				IfThisFails: "a closable gate without an external party having acted means " +
					"the assurance graph has been edited rather than earned"},

			{ID: "X-05", Command: "go run ./cmd/veriqoctl honesty",
				Expect:   "the levels grouped, never counted as a fraction",
				ExitZero: true,
				MustContain: []string{"INTERNAL CLAIM SCREENING",
					"EXTERNAL CLAIM VALIDATION", "NOT PERFORMED"},
				MustNotContain: []string{"4/5", "80%"},
				IfThisFails: "a fraction anywhere here means the pseudo-certification " +
					"failure has returned"},

			{ID: "X-06", Command: "go run ./cmd/veriqoctl metrics",
				Expect:   "three boards, no total, the external one empty",
				ExitZero: true,
				MustContain: []string{"no total below",
					"The EXTERNAL QUALIFICATION board is EMPTY"},
				IfThisFails: "a total across the boards means the separation has been lost"},
		},

		NegativeCases: []NegativeCase{
			{ID: "N-01",
				Do: "edit one field of any record in the capsule's ledger/records.json, " +
					"then recompute manifest.json so the bytes match their digests",
				MustProduce: "veriqo-verify FAILs at ledger-lineage, naming the record and " +
					"saying it has been altered since it was written",
				IfItPasses: "the ledger is not tamper-evident, and the manifest check is " +
					"the only real defence -- which an attacker who updates it faces nothing"},

			{ID: "N-02",
				Do: "change the statement inside passport.json's payload, leaving the " +
					"digest and signature fields untouched, and repair the manifest",
				MustProduce: "veriqo-verify FAILs at signature, saying the payload has been " +
					"altered since it was signed",
				IfItPasses: "the verifier is checking the signature against the digest the " +
					"document supplies, which accepts any payload at all"},

			{ID: "N-03",
				Do: "edit manifest.json's claimed_qualification to PRODUCTION_QUALIFIED",
				MustProduce: "veriqo-verify FAILs at qualification-state and prints THESE " +
					"DISAGREE, deriving INTERNALLY_ASSURED",
				IfItPasses: "the verifier is relaying a claimed status rather than deriving " +
					"one, which is the difference between verification and repetition"},

			{ID: "N-04",
				Do: "replace any .json file in the capsule with text that is not valid JSON, " +
					"and repair the manifest",
				MustProduce: "veriqo-verify FAILs at canonicalize, saying a document that " +
					"cannot be read cannot be verified",
				IfItPasses: "an unreadable document is passing by being unreadable, which " +
					"is the worst direction for that error to run"},

			{ID: "N-05",
				Do: "in the source, add a package outside pkg/assurance that returns the " +
					"string PRODUCTION_QUALIFIED, then run go test ./test/architecture/",
				MustProduce: "the architecture test FAILs, naming the file and line",
				IfItPasses: "Law 11 is a package property rather than a system invariant, " +
					"and any surface can publish a state the evidence does not support"},

			{ID: "N-06",
				Do: "in pkg/assurance/register/veriqo.go, raise any claim's CurrentLevel to " +
					"EXTERNALLY_VALIDATED without adding evidence, then run " +
					"go test ./pkg/assurance/register/",
				MustProduce: "the claim fails validation for citing no evidence of the " +
					"required class",
				IfItPasses: "an assurance level can be set rather than earned"},

			{ID: "N-07",
				Do: "construct two sources in pkg/qualification/independence whose " +
					"derivation edges both lead to one root, and ask for a corroboration " +
					"count of two",
				MustProduce: "SatisfiesCorroboration(2) returns false and the count resolves " +
					"to one producer",
				IfItPasses: "republication is being counted as corroboration"},
		},

		KnownFailureModes: []KnownFailureMode{
			{ID: "K-01",
				What: "the default verifier canonicalises with the same implementation the " +
					"system used",
				Consequence: "a defect in RFC 8785 conformance is invisible to the verifier: " +
					"both sides make the same mistake and agree",
				WhyNotFixed: "writing a second implementation here would test its author " +
					"rather than the seam. The seam exists for you to supply your own",
				Debt: "ED-011"},

			{ID: "K-02",
				What:        "the redaction claim is absence in twelve encodings",
				Consequence: "it is NOT a claim of irrecoverability, and it is routinely read as one",
				WhyNotFixed: "nobody has attempted recovery. We cannot attempt it credibly " +
					"against our own derivatives",
				Debt: "ED-004"},

			{ID: "K-03",
				What: "the key root is a software test double",
				Consequence: "no signature this system produces is a production signature, " +
					"and the code refuses production mode rather than pretending",
				WhyNotFixed: "no HSM or KMS tenancy has been provisioned", Debt: "ED-002"},

			{ID: "K-04",
				What: "no external anchor exists, deliberately",
				Consequence: "a checkpoint proves the chain is internally consistent, not " +
					"that it existed before you received it. A wholesale rewrite between " +
					"two observations is invisible from the artefact alone",
				WhyNotFixed: "an anchor VERIQO controls would prove only that VERIQO agrees " +
					"with itself", Debt: "ED-003"},

			{ID: "K-05",
				What: "the epistemic ladder constrains what can be RECORDED, not what can " +
					"be written",
				Consequence: "a person who puts a hypothesis into an observation's text " +
					"field has defeated it, and nothing here detects that",
				WhyNotFixed: "detecting it would require understanding the sentence, which " +
					"is a claim we are not willing to make"},

			{ID: "K-06",
				What: "the mutation suite covers nine struct fields",
				Consequence: "a different serialisation, API path, default value, migration, " +
					"database state or deployment configuration could reach the same end " +
					"and is not covered. Resisting known mutations is not proof that " +
					"unknown ones are impossible",
				WhyNotFixed: "the classes we have not thought of are, by construction, the " +
					"ones we cannot enumerate"},

			{ID: "K-07",
				What: "the resolution thresholds are a stated choice",
				Consequence: "no false-merge rate has been measured against a labelled " +
					"population, so 0.85 and 0.45 are defensible and unvalidated",
				WhyNotFixed: "no labelled population exists here", Debt: "ED-007"},
		},

		NegativeResultMeaning: "If you find nothing, please say what you looked at and what " +
			"you did not. A negative result with a stated scope is evidence and we will " +
			"record it as such, naming you. A negative result without one is silence, and " +
			"we already have plenty of that. In particular: finding nothing in the known " +
			"failure modes above is not a finding -- we listed them. The value is in what " +
			"is NOT on that list.",
	}
}

// addChallengeKit puts the executable half in the capsule and ties
// every input to its digest.
func addChallengeKit(b *verification.Builder, files map[string][]byte) error {
	k := VeriqoChallengeKit()
	k.InputDigests = map[string]string{}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		k.InputDigests[p] = hex.EncodeToString(sum[:])
	}
	return b.AddJSON("challenge/kit.json", k)
}

// Render writes the kit for a reader who prefers prose to JSON.
func (k ChallengeKit) Render() string {
	var b strings.Builder
	b.WriteString("EXECUTABLE CHALLENGE KIT\n")
	b.WriteString("  'We invite challenge' usually means a PDF and a diagram, neither of\n")
	b.WriteString("  which can be run. This is the runnable half.\n\n")
	for _, p := range k.Protocol {
		fmt.Fprintf(&b, "  %s\n", p)
	}

	b.WriteString("\nEXPECTED OUTPUTS -- stated in advance, so the result can fail\n")
	for _, e := range k.Expectations {
		fmt.Fprintf(&b, "\n  %s  %s\n", e.ID, e.Command)
		fmt.Fprintf(&b, "      expect: %s\n", e.Expect)
		for _, m := range e.MustContain {
			fmt.Fprintf(&b, "      must contain:     %q\n", m)
		}
		for _, m := range e.MustNotContain {
			fmt.Fprintf(&b, "      must NOT contain: %q\n", m)
		}
		fmt.Fprintf(&b, "      if it fails: %s\n", e.IfThisFails)
	}

	b.WriteString("\nNEGATIVE CASES -- attacks that must be refused\n")
	for _, n := range k.NegativeCases {
		fmt.Fprintf(&b, "\n  %s  %s\n", n.ID, n.Do)
		fmt.Fprintf(&b, "      must produce: %s\n", n.MustProduce)
		fmt.Fprintf(&b, "      if it passes: %s\n", n.IfItPasses)
	}

	b.WriteString("\nKNOWN FAILURE MODES -- wrong, and not fixed\n")
	for _, f := range k.KnownFailureModes {
		fmt.Fprintf(&b, "\n  %s  %s\n", f.ID, f.What)
		fmt.Fprintf(&b, "      consequence: %s\n", f.Consequence)
		fmt.Fprintf(&b, "      not fixed:   %s\n", f.WhyNotFixed)
		if f.Debt != "" {
			fmt.Fprintf(&b, "      debt:        %s\n", f.Debt)
		}
	}

	b.WriteString("\nIF YOU FIND NOTHING\n  " + k.NegativeResultMeaning + "\n")
	return b.String()
}
