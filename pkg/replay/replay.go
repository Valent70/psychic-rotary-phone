// Package replay reconstructs how a conclusion was produced.
//
// # Law 8: replayability is mandatory
//
//	same input + same versions = same deterministic result
//
// The equation is easy to write and easy to make vacuously true. A
// system that records only its output can "replay" by handing the
// output back. What makes a replay meaningful is that it records the
// INPUTS and the VERSIONS separately, re-executes, and compares --
// and that a mismatch is reported rather than resolved in favour of
// whichever run is more convenient.
//
// # The nondeterministic components
//
// Some steps are not deterministic: a model call, an external
// snapshot, a randomised sample. The specification's answer is the
// right one -- record the seed, the model version, the prompt, the
// tool results and the external snapshot, and replay against the
// RECORDING rather than re-issuing the call.
//
// That distinction is carried in the type: a Step is either
// DETERMINISTIC (re-executed) or RECORDED (compared against what was
// captured). A recorded step's replay proves that the rest of the
// pipeline behaves the same GIVEN that step's output; it does not
// prove the step would produce that output again, and Report says so.
//
// # What a divergence means
//
// A divergence is a finding about the system, not an error to be
// smoothed over. It says either the inputs were not fully captured,
// or a version was not recorded, or something is genuinely
// nondeterministic that was believed not to be. All three matter and
// the report distinguishes them.
package replay

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
)

var (
	ErrNoSteps     = errors.New("replay: a manifest with no steps replays nothing")
	ErrNoInputs    = errors.New("replay: a step must record its inputs")
	ErrUnversioned = errors.New("replay: a step must record the versions it ran under")
	ErrNoRecording = errors.New("replay: a nondeterministic step must carry its recording")
	ErrDivergence  = errors.New("replay: the replay diverged from the original")
	ErrUnknownStep = errors.New("replay: unknown step")
	ErrNoExecutor  = errors.New("replay: no executor for a deterministic step")
)

// Determinism says how a step is replayed.
type Determinism string

const (
	// Deterministic: re-executed, and the output must match.
	Deterministic Determinism = "DETERMINISTIC"
	// Recorded: not re-executed. The captured output is used, and the
	// replay proves the REST of the pipeline behaves the same given
	// it.
	Recorded Determinism = "RECORDED"
)

func (d Determinism) Valid() bool { return d == Deterministic || d == Recorded }

// Step is one stage of an execution.
type Step struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Determinism Determinism `json:"determinism"`

	// InputRefs name what went in; InputHash pins it. Both: the refs
	// let a human find the material, the hash makes substitution
	// detectable.
	InputRefs  []string `json:"input_refs"`
	InputHash  string   `json:"input_hash"`
	OutputHash string   `json:"output_hash"`

	Versions contract.VersionSet `json:"versions"`

	// Recording holds what a nondeterministic step produced, with
	// everything needed to understand it: the seed, the model, the
	// prompt, the tool results, the external snapshot.
	Recording *Recording `json:"recording,omitempty"`

	At time.Time `json:"at"`
}

// Recording is the captured output of a nondeterministic step.
type Recording struct {
	Seed         string   `json:"seed,omitempty"`
	ModelID      string   `json:"model_id,omitempty"`
	ModelVersion string   `json:"model_version,omitempty"`
	PromptHash   string   `json:"prompt_hash,omitempty"`
	Temperature  *float64 `json:"temperature,omitempty"`
	ToolResults  []string `json:"tool_results,omitempty"`
	SnapshotRef  string   `json:"snapshot_ref,omitempty"`
	// Output is the digest of what was produced.
	Output string `json:"output"`
	// Why states why this step is not deterministic. A step marked
	// RECORDED with no reason is a deterministic step somebody could
	// not make reproduce.
	Why string `json:"why"`
}

func (s Step) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("replay: a step has no id")
	}
	if !s.Determinism.Valid() {
		return fmt.Errorf("replay: step %s has unknown determinism %q", s.ID, s.Determinism)
	}
	if len(s.InputRefs) == 0 || strings.TrimSpace(s.InputHash) == "" {
		return fmt.Errorf("%w: %s", ErrNoInputs, s.ID)
	}
	if strings.TrimSpace(s.OutputHash) == "" {
		return fmt.Errorf("replay: step %s records no output hash", s.ID)
	}
	if !s.Versions.Complete() {
		return fmt.Errorf("%w: %s is missing %v", ErrUnversioned, s.ID, s.Versions.Missing())
	}
	if s.At.IsZero() {
		return fmt.Errorf("replay: step %s has no instant", s.ID)
	}
	if s.Determinism == Recorded {
		if s.Recording == nil {
			return fmt.Errorf("%w: %s", ErrNoRecording, s.ID)
		}
		if strings.TrimSpace(s.Recording.Why) == "" {
			return fmt.Errorf("replay: step %s is RECORDED and does not say why; a step "+
				"marked nondeterministic with no reason is a deterministic step somebody "+
				"could not make reproduce", s.ID)
		}
		if strings.TrimSpace(s.Recording.Output) == "" {
			return fmt.Errorf("replay: step %s carries a recording with no output", s.ID)
		}
		if s.Recording.Output != s.OutputHash {
			return fmt.Errorf("replay: step %s records output %s and its recording says %s",
				s.ID, short(s.OutputHash), short(s.Recording.Output))
		}
		if s.Recording.ModelID != "" && s.Recording.ModelVersion == "" {
			return fmt.Errorf("replay: step %s names model %s with no version",
				s.ID, s.Recording.ModelID)
		}
	}
	if s.Determinism == Deterministic && s.Recording != nil {
		return fmt.Errorf("replay: step %s is DETERMINISTIC and carries a recording; "+
			"a step that is replayed by re-execution must not also be replayed from a capture",
			s.ID)
	}
	return nil
}

// Manifest is the full execution record for one conclusion.
type Manifest struct {
	ID       contract.ID `json:"id"`
	TenantID string      `json:"tenant_id"`
	CaseID   string      `json:"case_id"`
	// Subject is what this manifest reconstructs: a finding, a
	// resolution, a corpus run.
	Subject string `json:"subject"`

	Steps []Step `json:"steps"`

	ResultHash string    `json:"result_hash"`
	At         time.Time `json:"at"`
}

// New builds and validates a manifest.
func New(id contract.ID, tenantID, caseID, subject string, steps []Step, resultHash string, at time.Time) (*Manifest, error) {
	if len(steps) == 0 {
		return nil, ErrNoSteps
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if err := s.Validate(); err != nil {
			return nil, err
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("replay: duplicate step id %s", s.ID)
		}
		seen[s.ID] = true
	}
	// Steps must be ordered in time. An out-of-order manifest cannot
	// be replayed in the order it claims to record.
	for i := 1; i < len(steps); i++ {
		if steps[i].At.Before(steps[i-1].At) {
			return nil, fmt.Errorf("replay: step %s at %s precedes %s at %s",
				steps[i].ID, steps[i].At.Format(time.RFC3339),
				steps[i-1].ID, steps[i-1].At.Format(time.RFC3339))
		}
	}
	if strings.TrimSpace(resultHash) == "" {
		return nil, errors.New("replay: a manifest records no result")
	}
	return &Manifest{ID: id, TenantID: tenantID, CaseID: caseID, Subject: subject,
		Steps: append([]Step(nil), steps...), ResultHash: resultHash, At: at}, nil
}

// Digest is the manifest's own hash.
func (m *Manifest) Digest() (string, error) { return jcs.Hash(m) }

// Executor re-runs a deterministic step.
//
// It is supplied by the caller because this package cannot know how to
// re-run an entity resolution or a corpus pass. What it does know is
// what the answer must be.
type Executor interface {
	// Execute re-runs the step and returns the output hash it
	// produces.
	Execute(s Step) (outputHash string, err error)
}

// Divergence is one step whose replay did not match.
type Divergence struct {
	StepID   string
	Expected string
	Actual   string
	// Kind names what the divergence means, which is the part a
	// caller can act on.
	Kind   string
	Detail string
}

func (d Divergence) String() string {
	return fmt.Sprintf("step %s: expected %s, replayed %s -- %s (%s)",
		d.StepID, short(d.Expected), short(d.Actual), d.Kind, d.Detail)
}

// Kinds of divergence.
const (
	// InputsNotCaptured: the same recorded inputs produced a different
	// output, which means something the step read was not recorded.
	InputsNotCaptured = "INPUTS_NOT_FULLY_CAPTURED"
	// VersionDrift: a version differs between the record and the
	// replay environment.
	VersionDrift = "VERSION_DRIFT"
	// UndeclaredNondeterminism: a step declared DETERMINISTIC does not
	// reproduce, and nothing else explains it.
	UndeclaredNondeterminism = "UNDECLARED_NONDETERMINISM"
	// ExecutorFailed: the step could not be re-run at all.
	ExecutorFailed = "EXECUTOR_FAILED"
)

// Result is what a replay concluded.
type Result struct {
	Reproduced  bool
	Divergences []Divergence
	// StepsReExecuted and StepsFromRecording are reported separately,
	// because a replay that re-executed one step out of twelve
	// establishes much less than one that re-executed all twelve.
	StepsReExecuted    int
	StepsFromRecording int
	// Caveats are what the replay does NOT establish.
	Caveats []string
}

// Replay re-executes the manifest.
func Replay(m *Manifest, exec Executor, current contract.VersionSet) (Result, error) {
	var res Result
	if exec == nil {
		return res, ErrNoExecutor
	}

	for _, s := range m.Steps {
		if s.Determinism == Recorded {
			res.StepsFromRecording++
			continue
		}
		res.StepsReExecuted++

		// Version drift is checked before re-execution, because a
		// divergence under a different version is not the same finding
		// as one under the same version.
		if drift := driftBetween(s.Versions, current); len(drift) > 0 {
			res.Divergences = append(res.Divergences, Divergence{
				StepID: s.ID, Expected: s.OutputHash, Actual: "",
				Kind: VersionDrift,
				Detail: "the replay environment differs from the recorded versions: " +
					strings.Join(drift, ", ")})
			continue
		}

		got, err := exec.Execute(s)
		if err != nil {
			res.Divergences = append(res.Divergences, Divergence{
				StepID: s.ID, Expected: s.OutputHash, Kind: ExecutorFailed,
				Detail: err.Error()})
			continue
		}
		if got != s.OutputHash {
			res.Divergences = append(res.Divergences, Divergence{
				StepID: s.ID, Expected: s.OutputHash, Actual: got,
				Kind: UndeclaredNondeterminism,
				Detail: "the step is declared DETERMINISTIC and the same recorded inputs " +
					"produced a different output; either an input was not captured or the " +
					"step is not deterministic"})
		}
	}

	res.Reproduced = len(res.Divergences) == 0

	// What the replay does not establish, stated every time.
	if res.StepsFromRecording > 0 {
		res.Caveats = append(res.Caveats, fmt.Sprintf(
			"%d of %d step(s) were replayed FROM A RECORDING rather than re-executed. "+
				"The replay shows the rest of the pipeline behaves the same GIVEN those "+
				"outputs; it does not show those steps would produce them again",
			res.StepsFromRecording, len(m.Steps)))
	}
	if res.StepsReExecuted == 0 {
		res.Caveats = append(res.Caveats,
			"no step was re-executed; this replay establishes nothing about reproducibility")
		res.Reproduced = false
	}
	sort.Slice(res.Divergences, func(i, j int) bool {
		return res.Divergences[i].StepID < res.Divergences[j].StepID
	})
	return res, nil
}

// driftBetween names the versions that differ.
func driftBetween(recorded, current contract.VersionSet) []string {
	var out []string
	cmp := func(name string, a, b contract.Version) {
		if b.Zero() {
			return // the caller did not state this one; not drift
		}
		if a.Component != b.Component || a.Revision != b.Revision {
			out = append(out, fmt.Sprintf("%s recorded %s, now %s", name, a, b))
		}
	}
	cmp("ontology", recorded.Ontology, current.Ontology)
	cmp("policy", recorded.Policy, current.Policy)
	cmp("algorithm", recorded.Algorithm, current.Algorithm)
	cmp("model", recorded.Model, current.Model)
	cmp("prompt", recorded.Prompt, current.Prompt)
	sort.Strings(out)
	return out
}

// Report renders the replay outcome.
func (r Result) Report() string {
	var b strings.Builder
	if r.Reproduced {
		b.WriteString("REPLAY REPRODUCED THE RESULT\n")
	} else {
		b.WriteString("REPLAY DIVERGED\n")
	}
	fmt.Fprintf(&b, "  %d step(s) re-executed, %d replayed from a recording\n",
		r.StepsReExecuted, r.StepsFromRecording)
	for _, d := range r.Divergences {
		fmt.Fprintf(&b, "  - %s\n", d)
	}
	for _, c := range r.Caveats {
		fmt.Fprintf(&b, "  NOTE: %s\n", c)
	}
	return b.String()
}

// Coverage is the share of steps that are actually re-executable.
//
// It is reported because a manifest of twelve steps, eleven of them
// RECORDED, is a manifest whose replay establishes very little -- and
// a system that reported only "reproduced: true" would hide that.
func (m *Manifest) Coverage() float64 {
	if len(m.Steps) == 0 {
		return 0
	}
	n := 0
	for _, s := range m.Steps {
		if s.Determinism == Deterministic {
			n++
		}
	}
	return float64(n) / float64(len(m.Steps))
}

// NondeterministicSteps names the steps that cannot be re-executed,
// with the reason each gave.
func (m *Manifest) NondeterministicSteps() []string {
	var out []string
	for _, s := range m.Steps {
		if s.Determinism == Recorded {
			out = append(out, fmt.Sprintf("%s: %s", s.ID, s.Recording.Why))
		}
	}
	return out
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
