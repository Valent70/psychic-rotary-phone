package replay

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

var t0 = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "resolution", Revision: 7},
	}
}

func step(id string, n int, det Determinism) Step {
	s := Step{ID: id, Name: id, Determinism: det,
		InputRefs: []string{"ev:" + id}, InputHash: "in-" + id,
		OutputHash: "out-" + id, Versions: versions(),
		At: t0.Add(time.Duration(n) * time.Minute)}
	if det == Recorded {
		s.Recording = &Recording{Output: "out-" + id,
			ModelID: "extract-v3", ModelVersion: "2026-08-01", PromptHash: "p1",
			Why: "a language model call; the provider does not guarantee token-level determinism"}
	}
	return s
}

// replayer re-executes by returning the recorded output, which is the
// behaviour of a correctly deterministic pipeline.
type replayer struct {
	override map[string]string
	fail     map[string]bool
}

func (r replayer) Execute(s Step) (string, error) {
	if r.fail[s.ID] {
		return "", errors.New("the step could not be re-run in this environment")
	}
	if v, ok := r.override[s.ID]; ok {
		return v, nil
	}
	return s.OutputHash, nil
}

func manifest(t *testing.T, steps ...Step) *Manifest {
	t.Helper()
	m, err := New("replay:r1", "t-acme", "case-1", "finding:f1", steps, "result-1", t0)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestADeterministicPipelineReproduces.
func TestADeterministicPipelineReproduces(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic), step("s2", 1, Deterministic))
	res, err := Replay(m, replayer{}, versions())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reproduced {
		t.Fatalf("a deterministic pipeline diverged:\n%s", res.Report())
	}
	if res.StepsReExecuted != 2 || res.StepsFromRecording != 0 {
		t.Fatalf("re-executed %d, from recording %d", res.StepsReExecuted, res.StepsFromRecording)
	}
	if m.Coverage() != 1 {
		t.Fatalf("coverage = %v", m.Coverage())
	}
}

// TestADivergenceIsReportedNotSmoothedOver.
func TestADivergenceIsReportedNotSmoothedOver(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic), step("s2", 1, Deterministic))
	res, err := Replay(m, replayer{override: map[string]string{"s2": "something-else"}}, versions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Reproduced {
		t.Fatal("A DIVERGENT REPLAY REPORTED AS REPRODUCED")
	}
	if len(res.Divergences) != 1 || res.Divergences[0].StepID != "s2" {
		t.Fatalf("Divergences = %v", res.Divergences)
	}
	if res.Divergences[0].Kind != UndeclaredNondeterminism {
		t.Fatalf("the divergence was classified %s", res.Divergences[0].Kind)
	}
	if !strings.Contains(res.Report(), "REPLAY DIVERGED") {
		t.Fatalf("the report does not lead with the divergence:\n%s", res.Report())
	}
}

// TestVersionDriftIsADistinctFinding.
//
// A divergence under a different version is not the same finding as
// one under the same version, and resolving it the same way would
// misattribute the cause.
func TestVersionDriftIsADistinctFinding(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic))
	drifted := versions()
	drifted.Algorithm = contract.Version{Component: "resolution", Revision: 8}

	res, err := Replay(m, replayer{}, drifted)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reproduced {
		t.Fatal("a replay under a different algorithm version reported reproduction")
	}
	if res.Divergences[0].Kind != VersionDrift {
		t.Fatalf("the drift was classified %s", res.Divergences[0].Kind)
	}
	if !strings.Contains(res.Divergences[0].Detail, "resolution@7") {
		t.Fatalf("the drift does not name the recorded version: %s", res.Divergences[0].Detail)
	}
}

// TestAnUnstatedCurrentVersionIsNotDrift. A caller that does not
// declare a model version is not asserting it changed.
func TestAnUnstatedCurrentVersionIsNotDrift(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic))
	partial := contract.VersionSet{Ontology: versions().Ontology}
	res, err := Replay(m, replayer{}, partial)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reproduced {
		t.Fatalf("an unstated version was treated as drift:\n%s", res.Report())
	}
}

// TestARecordedStepIsNotReExecutedAndTheReplaySaysSo.
//
// This is the caveat that makes the whole mechanism honest.
func TestARecordedStepIsNotReExecutedAndTheReplaySaysSo(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic), step("s2", 1, Recorded))
	res, err := Replay(m, replayer{}, versions())
	if err != nil {
		t.Fatal(err)
	}
	if res.StepsFromRecording != 1 {
		t.Fatalf("from recording = %d", res.StepsFromRecording)
	}
	joined := strings.Join(res.Caveats, " ")
	if !strings.Contains(joined, "FROM A RECORDING") {
		t.Fatalf("the caveat is missing: %v", res.Caveats)
	}
	if !strings.Contains(joined, "does not show those steps would produce them again") {
		t.Fatalf("the caveat understates the limit: %v", res.Caveats)
	}
	if m.Coverage() != 0.5 {
		t.Fatalf("coverage = %v", m.Coverage())
	}
}

// TestAReplayThatReExecutesNothingEstablishesNothing.
//
// A manifest of all-recorded steps would otherwise report
// "reproduced: true" and mean nothing at all.
func TestAReplayThatReExecutesNothingEstablishesNothing(t *testing.T) {
	m := manifest(t, step("s1", 0, Recorded), step("s2", 1, Recorded))
	res, err := Replay(m, replayer{}, versions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Reproduced {
		t.Fatal("A REPLAY THAT RE-EXECUTED NOTHING REPORTED REPRODUCTION")
	}
	if !strings.Contains(strings.Join(res.Caveats, " "), "establishes nothing") {
		t.Fatalf("the caveats do not say so: %v", res.Caveats)
	}
}

// TestARecordedStepMustSayWhyItIsNondeterministic.
//
// A step marked RECORDED with no reason is a deterministic step
// somebody could not make reproduce.
func TestARecordedStepMustSayWhyItIsNondeterministic(t *testing.T) {
	s := step("s1", 0, Recorded)
	s.Recording.Why = ""
	if err := s.Validate(); err == nil {
		t.Fatal("a RECORDED step with no stated reason was accepted")
	}
}

// TestARecordedStepMustCarryItsRecording.
func TestARecordedStepMustCarryItsRecording(t *testing.T) {
	s := step("s1", 0, Recorded)
	s.Recording = nil
	if err := s.Validate(); !errors.Is(err, ErrNoRecording) {
		t.Fatalf("a RECORDED step with no recording was accepted: %v", err)
	}
}

// TestADeterministicStepMayNotCarryARecording. A step replayed by
// re-execution must not also be replayable from a capture -- that
// combination lets a failing re-execution fall back silently.
func TestADeterministicStepMayNotCarryARecording(t *testing.T) {
	s := step("s1", 0, Deterministic)
	s.Recording = &Recording{Output: "out-s1", Why: "just in case"}
	if err := s.Validate(); err == nil {
		t.Fatal("a DETERMINISTIC step carrying a recording was accepted")
	}
}

// TestARecordingMustAgreeWithItsStep. Otherwise the capture and the
// record disagree about what happened.
func TestARecordingMustAgreeWithItsStep(t *testing.T) {
	s := step("s1", 0, Recorded)
	s.Recording.Output = "something-else"
	if err := s.Validate(); err == nil {
		t.Fatal("a recording disagreeing with its step was accepted")
	}
}

// TestAModelMustBeVersioned. "extract-v3" without a version cannot be
// re-pinned later.
func TestAModelMustBeVersioned(t *testing.T) {
	s := step("s1", 0, Recorded)
	s.Recording.ModelVersion = ""
	if err := s.Validate(); err == nil {
		t.Fatal("a recorded model call with no model version was accepted")
	}
}

// TestEveryStepMustRecordItsInputsAndVersions.
func TestEveryStepMustRecordItsInputsAndVersions(t *testing.T) {
	cases := map[string]func(*Step){
		"no input refs": func(s *Step) { s.InputRefs = nil },
		"no input hash": func(s *Step) { s.InputHash = "" },
		"no output":     func(s *Step) { s.OutputHash = "" },
		"no versions":   func(s *Step) { s.Versions = contract.VersionSet{} },
		"no instant":    func(s *Step) { s.At = time.Time{} },
	}
	for name, mutate := range cases {
		s := step("s1", 0, Deterministic)
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("a step with %s was accepted", name)
		}
	}
}

// TestOutOfOrderStepsAreRefused. A manifest cannot be replayed in an
// order it does not record.
func TestOutOfOrderStepsAreRefused(t *testing.T) {
	a := step("s1", 5, Deterministic)
	b := step("s2", 1, Deterministic)
	if _, err := New("replay:r1", "t-acme", "case-1", "f", []Step{a, b}, "r", t0); err == nil {
		t.Fatal("an out-of-order manifest was accepted")
	}
}

// TestAnEmptyManifestReplaysNothing.
func TestAnEmptyManifestReplaysNothing(t *testing.T) {
	if _, err := New("replay:r1", "t-acme", "case-1", "f", nil, "r", t0); !errors.Is(err, ErrNoSteps) {
		t.Fatalf("an empty manifest was accepted: %v", err)
	}
}

// TestAnExecutorFailureIsItsOwnKindOfDivergence.
func TestAnExecutorFailureIsItsOwnKindOfDivergence(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic))
	res, err := Replay(m, replayer{fail: map[string]bool{"s1": true}}, versions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Reproduced {
		t.Fatal("a failed re-execution reported reproduction")
	}
	if res.Divergences[0].Kind != ExecutorFailed {
		t.Fatalf("classified %s", res.Divergences[0].Kind)
	}
}

// TestNoExecutorIsRefused, rather than returning "reproduced".
func TestNoExecutorIsRefused(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic))
	if _, err := Replay(m, nil, versions()); !errors.Is(err, ErrNoExecutor) {
		t.Fatalf("a replay with no executor was accepted: %v", err)
	}
}

// TestTheManifestNamesWhatCannotBeReExecuted, with each reason.
func TestTheManifestNamesWhatCannotBeReExecuted(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic), step("s2", 1, Recorded))
	nd := m.NondeterministicSteps()
	if len(nd) != 1 || !strings.Contains(nd[0], "language model call") {
		t.Fatalf("NondeterministicSteps = %v", nd)
	}
}

// TestTheManifestDigestCoversEveryStep.
func TestTheManifestDigestCoversEveryStep(t *testing.T) {
	m := manifest(t, step("s1", 0, Deterministic))
	base, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	m2 := manifest(t, step("s1", 0, Deterministic))
	m2.Steps[0].OutputHash = "different"
	got, err := m2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got == base {
		t.Fatal("editing a step's output did not change the manifest digest")
	}
}
