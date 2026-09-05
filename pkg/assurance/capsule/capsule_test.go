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
