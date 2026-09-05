package capsule

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/assurance/register"
	"veriqo/pkg/verification"
)

func built(t *testing.T) (*verification.Bundle, string) {
	t.Helper()
	b, err := BuildCapsule(Options{Commit: "abc1234"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dir := t.TempDir()
	if _, err := b.Write(dir); err != nil {
		t.Fatalf("write: %v", err)
	}
	bundle, err := verification.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return bundle, dir
}

// TestTheCapsuleIsAVerifiableBundle. An assessor's first act is to run
// the verifier over it, so the capsule must survive that.
func TestTheCapsuleIsAVerifiableBundle(t *testing.T) {
	b, _ := built(t)
	r := verification.Verify(b, verification.Options{At: mustTime(t), Revocations: []string{}})
	if !r.Verified() {
		t.Fatalf("the capsule fails its own verifier:\n%s", r.Render())
	}
	// The worked case exists so that no step reports UNVERIFIABLE. A
	// capsule an assessor can read but not CHECK puts them back where
	// they started: believing a document.
	if u := r.Unverifiable(); len(u) != 0 {
		t.Fatalf("the capsule leaves %d step(s) uncheckable, so an assessor cannot run "+
			"the chain against it: %v", len(u), u)
	}
	if r.DerivedQualification != "INTERNALLY_ASSURED" {
		t.Fatalf("derived %s", r.DerivedQualification)
	}
}

// TestTheWorkedCaseSaysThreeTimesThatItIsSynthetic.
//
// A verifiable case in a capsule is the single most misreadable thing
// in it: an assessor who checks it and sees everything pass can carry
// away the impression that VERIQO has been shown to work on data. It
// has been shown to work on data VERIQO wrote for the purpose.
func TestTheWorkedCaseSaysThreeTimesThatItIsSynthetic(t *testing.T) {
	b, _ := built(t)
	found := 0
	for _, path := range []string{"case/README.txt", "passport.json", "CONTENTS.json",
		"case/artefacts/e1v1.txt"} {
		raw, ok := b.File(path)
		if !ok {
			t.Fatalf("missing %s", path)
		}
		if strings.Contains(strings.ToUpper(string(raw)), "SYNTHETIC") {
			found++
		}
	}
	if found < 3 {
		t.Fatalf("only %d of the case's artefacts say it is synthetic", found)
	}
	raw, _ := b.File("case/README.txt")
	for _, want := range []string{
		"establishes about VERIQO's behaviour on REAL data: nothing",
		"ED-007",
		"protects nothing",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the case README omits %q", want)
		}
	}
}

// TestTheRecordedReplayStepIsNotClaimedAsReExecuted.
func TestTheRecordedReplayStepIsNotClaimedAsReExecuted(t *testing.T) {
	b, _ := built(t)
	raw, ok := b.File("replay/steps.json")
	if !ok {
		t.Fatal("no replay record")
	}
	var steps []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &steps); err != nil {
		t.Fatal(err)
	}
	var det, rec int
	for _, s := range steps {
		switch s.Kind {
		case "DETERMINISTIC":
			det++
		case "RECORDED":
			rec++
		default:
			t.Fatalf("%s has kind %q", s.Name, s.Kind)
		}
	}
	if det == 0 || rec == 0 {
		t.Fatalf("the replay record has %d deterministic and %d recorded steps; a capsule "+
			"with only deterministic steps misrepresents what replay establishes in a "+
			"real case", det, rec)
	}
	r := verification.Verify(mustBundle(t), verification.Options{At: mustTime(t),
		Revocations: []string{}})
	if !strings.Contains(r.Render(), "RECORDED rather than re-executed") {
		t.Fatalf("the verifier does not caveat the recorded step:\n%s", r.Render())
	}
}

func mustBundle(t *testing.T) *verification.Bundle {
	t.Helper()
	b, _ := built(t)
	return b
}

// TestTheCapsuleClaimsExactlyInternallyAssured.
//
// Claiming less than the evidence supports costs nothing. Claiming
// more costs the engagement, which is why this is a test.
func TestTheCapsuleClaimsExactlyInternallyAssured(t *testing.T) {
	b, _ := built(t)
	if got := b.Manifest().ClaimedQualification; got != "INTERNALLY_ASSURED" {
		t.Fatalf("the capsule claims %s", got)
	}
}

// TestTheCapsuleStatesWhatItDoesNotContain. An assessor who does not
// know what was withheld cannot scope their assessment.
func TestTheCapsuleStatesWhatItDoesNotContain(t *testing.T) {
	b, _ := built(t)
	raw, ok := b.File("CONTENTS.json")
	if !ok {
		t.Fatal("the capsule has no contents index")
	}
	var c Contents
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Excludes) < 5 {
		t.Fatalf("the capsule lists only %d exclusions", len(c.Excludes))
	}
	joined := strings.Join(c.Excludes, " ")
	for _, want := range []string{"production keys", "external anchor",
		"party other than VERIQO", "real-world documents"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the exclusion list omits %q", want)
		}
	}
	if len(c.HowToStart) == 0 {
		t.Fatal("the capsule does not say where to start")
	}
}

// TestTheThreatModelSaysWhatItDidNotConsider. A threat model listing
// only what was thought about reads as complete.
func TestTheThreatModelSaysWhatItDidNotConsider(t *testing.T) {
	tm := VeriqoThreatModel()
	if len(tm.NotConsidered) < 5 {
		t.Fatalf("only %d threats are recorded as not considered", len(tm.NotConsidered))
	}
	if len(tm.Assumptions) == 0 {
		t.Fatal("the threat model states no assumptions")
	}
	for _, th := range tm.Considered {
		if strings.TrimSpace(th.Residual) == "" {
			t.Fatalf("%s states no residual risk; a control with no residual reads as total",
				th.ID)
		}
		if strings.TrimSpace(th.Tested) == "" {
			t.Fatalf("%s does not say how it was tested or by whom", th.ID)
		}
		if !strings.Contains(strings.ToLower(th.Tested), "veriqo") &&
			!strings.Contains(strings.ToLower(th.Tested), "not tested") {
			t.Fatalf("%s claims testing by somebody other than VERIQO: %q", th.ID, th.Tested)
		}
	}
	joined := strings.Join(tm.NotConsidered, " ")
	for _, want := range []string{"side channels", "toolchain", "post-quantum"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the not-considered list omits %q", want)
		}
	}
}

// TestTheBuildDoesNotClaimUnverifiedReproducibility.
func TestTheBuildDoesNotClaimUnverifiedReproducibility(t *testing.T) {
	b, _ := built(t)
	raw, _ := b.File("build.json")
	var bl Build
	if err := json.Unmarshal(raw, &bl); err != nil {
		t.Fatal(err)
	}
	if bl.ReproducibilityVerified {
		t.Fatal("the capsule claims verified reproducibility; no bit-for-bit comparison " +
			"has been performed")
	}
	if !strings.Contains(bl.Note, "has NOT verified") {
		t.Fatalf("the build note does not state the limitation: %q", bl.Note)
	}
	if len(bl.Reproduce) == 0 {
		t.Fatal("the capsule does not say how to rebuild it")
	}
}

// TestEveryRegisterReachesTheCapsule. A capsule missing one of them
// sends an assessor to ask VERIQO for it, which is the arrangement
// this replaces.
func TestEveryRegisterReachesTheCapsule(t *testing.T) {
	b, _ := built(t)
	for _, path := range []string{
		"assurance/register.txt", "assurance/claims.json", "assurance/debts.json",
		"assurance/controls.json", "assurance/gates.json", "assurance/evidence.json",
		"assurance/ladder.txt", "gates/report.txt", "scorecard/report.txt",
		"failure-classes/report.txt", "failure-classes/cited-tests.json",
		"self-doubt/report.txt", "policy/rules.json", "policy/authority-matrix.json",
		"api/endpoints.json", "redaction/variants.json", "redaction/coverage.txt",
		"threat-model.json", "build.json", "dependencies.json", "README.txt", "VERIFY.txt",
		"CHALLENGE.txt",
	} {
		if _, ok := b.File(path); !ok {
			t.Fatalf("the capsule is missing %s", path)
		}
	}
}

// TestTheCoverageFigureCarriesItsStatusInsideTheCapsule.
func TestTheCoverageFigureCarriesItsStatusInsideTheCapsule(t *testing.T) {
	b, _ := built(t)
	raw, _ := b.File("redaction/coverage.txt")
	s := string(raw)
	if !strings.Contains(s, "ESTIMATE") {
		t.Fatal("the coverage figure reaches an assessor without its status")
	}
	if !strings.Contains(s, "L2_FIXTURE_CORRECTNESS") {
		t.Fatal("the corpus qualification level is not stated")
	}
}

// TestTheEvidenceInTheCapsuleIsAllInternal. The verifier derives the
// qualification from this file, so it must not accidentally contain a
// record that would inflate the derivation.
func TestTheEvidenceInTheCapsuleIsAllInternal(t *testing.T) {
	b, _ := built(t)
	raw, _ := b.File("assurance/evidence.json")
	var ev []struct {
		Class     string `json:"class"`
		Validator struct {
			External bool `json:"external"`
		} `json:"validator"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev) == 0 {
		t.Fatal("the capsule carries no evidence records at all")
	}
	for _, e := range ev {
		if e.Class != "ASSURANCE_INTERNAL" || e.Validator.External {
			t.Fatalf("an external evidence record reached the capsule: %+v", e)
		}
	}
}

// TestTheReadmeTellsTheAssessorTheOnePointThatMatters.
func TestTheReadmeTellsTheAssessorTheOnePointThatMatters(t *testing.T) {
	b, _ := built(t)
	raw, _ := b.File("README.txt")
	s := string(raw)
	for _, want := range []string{
		"do not have to take VERIQO's word",
		"Nothing here has been examined by anybody outside VERIQO",
		"veriqo-verify",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("the README omits %q", want)
		}
	}
}

// TestTheCapsuleIsDeterministic. Two builds must be identical, or an
// assessor cannot tell a change from a rebuild.
func TestTheCapsuleIsDeterministic(t *testing.T) {
	a, _ := built(t)
	c, _ := built(t)
	if a.Digest() != c.Digest() {
		t.Fatalf("two capsule builds differ: %s vs %s", a.Digest(), c.Digest())
	}
}

func mustTime(t *testing.T) time.Time {
	t.Helper()
	return register.AssessedAt()
}

// TestTheCapsuleClaimIsDerivedNotWritten.
//
// Hard-coding the claim would be correct today and a latent defect:
// the day somebody edits the string, or obtains external evidence, the
// capsule would assert a level nothing checked.
func TestTheCapsuleClaimIsDerivedNotWritten(t *testing.T) {
	b, _ := built(t)
	raw, ok := b.File("assurance/emission.json")
	if !ok {
		t.Fatal("the capsule does not record how its claim was derived")
	}
	var r struct {
		Emission struct {
			Surface string `json:"surface"`
			Claimed string `json:"claimed"`
		} `json:"emission"`
		Derived string `json:"derived"`
		Emitted string `json:"emitted"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	// The capsule asks for the top of the ladder deliberately, so the
	// invariant is exercised at the one place it matters most.
	if r.Emission.Claimed != "PRODUCTION_QUALIFIED" {
		t.Fatalf("the capsule asked for %s; asking for what we expect to get leaves the "+
			"invariant untested here", r.Emission.Claimed)
	}
	if r.Verdict != "QUALIFICATION_CLAIM_INVALID" {
		t.Fatalf("verdict = %s", r.Verdict)
	}
	if r.Emitted != "INTERNALLY_ASSURED" || r.Derived != "INTERNALLY_ASSURED" {
		t.Fatalf("emitted %s, derived %s", r.Emitted, r.Derived)
	}
	if r.Emission.Surface != "AUDITOR_CAPSULE" {
		t.Fatalf("surface = %s", r.Emission.Surface)
	}
	// And the manifest carries what the invariant returned, not what
	// was asked for.
	if got := b.Manifest().ClaimedQualification; got != r.Emitted {
		t.Fatalf("the manifest claims %s and the invariant emitted %s", got, r.Emitted)
	}
}

// TestTheCapsuleInvitesAttackRatherThanAssertingSafety.
//
// A capsule assembled to prove the system is safe and one assembled to
// make it easy to prove the system wrong contain almost the same files
// and are completely different documents. The first selects what
// supports the conclusion; only the second tells an assessor where to
// look.
func TestTheCapsuleInvitesAttackRatherThanAssertingSafety(t *testing.T) {
	b, _ := built(t)
	raw, ok := b.File("CHALLENGE.txt")
	if !ok {
		t.Fatal("the capsule carries no challenge document")
	}
	c := string(raw)
	for _, want := range []string{
		"not assembled to show that VERIQO is safe",
		"WHERE WE THINK IT IS WEAKEST",
		"WHAT WOULD COUNT AS PROVING US WRONG",
		"WHAT WOULD NOT BE A USEFUL FINDING",
		"with a stated scope is evidence",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("the challenge document omits %q", want)
		}
	}
	// The weakest points must be named specifically enough to act on,
	// each citing where to look or which debt owns it.
	for _, want := range []string{"pkg/canonical/jcs", "ED-011", "ED-004", "ED-005",
		"ED-010", "test/assurancemutation"} {
		if !strings.Contains(c, want) {
			t.Fatalf("the challenge document does not point at %q", want)
		}
	}
	// It must not be a list of strengths wearing a challenge title.
	if strings.Count(c, "ED-") < 4 {
		t.Fatal("the challenge document cites fewer than four open debts; it is not " +
			"actually pointing at the weak places")
	}
}

// TestChallengeabilityIsExecutableRatherThanDocumentary.
//
// "We invite challenge" usually means an assessor receives a PDF, a
// diagram and a summary of test results, none of which can be run.
// Their only options are to believe the document or to ask for a
// demonstration that the assessed party also runs -- which is where
// they started.
func TestChallengeabilityIsExecutableRatherThanDocumentary(t *testing.T) {
	b, _ := built(t)
	raw, ok := b.File("challenge/kit.json")
	if !ok {
		t.Fatal("the capsule carries no executable challenge kit")
	}
	var k ChallengeKit
	if err := json.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}

	// RUN -> VERIFY -> ATTACK -> COMPARE -> REPORT
	if len(k.Protocol) != 5 {
		t.Fatalf("the protocol has %d steps", len(k.Protocol))
	}
	joined := strings.Join(k.Protocol, " ")
	for _, verb := range []string{"RUN", "VERIFY", "ATTACK", "COMPARE", "REPORT"} {
		if !strings.Contains(joined, verb) {
			t.Fatalf("the protocol omits %s", verb)
		}
	}

	// Every expectation must be a runnable command with its result
	// stated in advance: a package that lets the assessor discover
	// what the program produces cannot be failed.
	if len(k.Expectations) < 5 {
		t.Fatalf("only %d expectations", len(k.Expectations))
	}
	for _, e := range k.Expectations {
		if strings.TrimSpace(e.Command) == "" {
			t.Fatalf("%s has no command", e.ID)
		}
		if strings.TrimSpace(e.Expect) == "" {
			t.Fatalf("%s does not say what to expect, so it cannot fail", e.ID)
		}
		if strings.TrimSpace(e.IfThisFails) == "" {
			t.Fatalf("%s does not say what an unexpected result would mean", e.ID)
		}
		if len(e.MustContain) == 0 && len(e.MustNotContain) == 0 {
			t.Fatalf("%s states nothing checkable", e.ID)
		}
	}

	// Negative cases are what make it a test rather than a
	// demonstration.
	if len(k.NegativeCases) < 6 {
		t.Fatalf("only %d negative cases", len(k.NegativeCases))
	}
	for _, n := range k.NegativeCases {
		if strings.TrimSpace(n.MustProduce) == "" {
			t.Fatalf("%s does not name the refusal it must produce", n.ID)
		}
		if strings.Contains(strings.ToLower(n.MustProduce), "an error") &&
			len(strings.Fields(n.MustProduce)) < 6 {
			t.Fatalf("%s expects only 'an error', which is not specific enough to fail",
				n.ID)
		}
		if strings.TrimSpace(n.IfItPasses) == "" {
			t.Fatalf("%s does not write the finding for the assessor in advance", n.ID)
		}
	}

	// Known failure modes: the part most organisations will not ship.
	if len(k.KnownFailureModes) < 5 {
		t.Fatalf("only %d known failure modes", len(k.KnownFailureModes))
	}
	for _, f := range k.KnownFailureModes {
		if strings.TrimSpace(f.Consequence) == "" || strings.TrimSpace(f.WhyNotFixed) == "" {
			t.Fatalf("%s does not state its consequence or why it is unfixed", f.ID)
		}
	}

	// Input digests must cover the bundle, or an assessor cannot tell
	// whether they are running what we described.
	if len(k.InputDigests) < 20 {
		t.Fatalf("only %d inputs are digested", len(k.InputDigests))
	}
	for _, path := range []string{"assurance/claims.json", "ledger/records.json",
		"passport.json"} {
		if k.InputDigests[path] == "" {
			t.Fatalf("no digest for %s", path)
		}
	}

	// And the negative-result instruction, which is the result most
	// likely to be misreported.
	if !strings.Contains(k.NegativeResultMeaning, "stated scope is evidence") {
		t.Fatalf("the kit does not say what a negative result is worth: %q",
			k.NegativeResultMeaning)
	}
	if !strings.Contains(k.NegativeResultMeaning, "is NOT on that list") {
		t.Fatal("the kit does not say that finding a documented weakness is not a finding")
	}
}

// TestTheKnownFailureModesIncludeTheMutationSuitesOwnLimit.
//
// Resisting known mutations is not proof that unknown ones are
// impossible, and the capsule is where an assessor will look for that
// caveat.
func TestTheKnownFailureModesIncludeTheMutationSuitesOwnLimit(t *testing.T) {
	k := VeriqoChallengeKit()
	var found bool
	for _, f := range k.KnownFailureModes {
		if strings.Contains(f.Consequence, "not proof that") {
			found = true
		}
	}
	if !found {
		t.Fatal("the known failure modes do not admit that the mutation suite proves " +
			"robustness against the classes it thought of, not impossibility")
	}
}
