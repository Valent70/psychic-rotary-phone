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
	// The capsule carries no passport, ledger or artefacts, so several
	// steps are UNVERIFIABLE -- which the verifier reports rather than
	// failing, and which makes the derived state PARTIALLY_VERIFIED.
	if len(r.Failures()) != 0 {
		t.Fatalf("the capsule fails its own verifier:\n%s", r.Render())
	}
	if len(r.Unverifiable()) == 0 {
		t.Fatal("a capsule with no ledger or passport reported nothing unverifiable")
	}
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
