package casefabric

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryPhaseHasACompleteContract is the nine-attribute requirement.
// A blank is the finding, and it cannot ship.
func TestEveryPhaseHasACompleteContract(t *testing.T) {
	if err := ValidateContracts(); err != nil {
		t.Fatalf("ValidateContracts: %v", err)
	}
	if len(PhaseContracts()) != len(Phases()) {
		t.Fatalf("expected %d contracts, got %d", len(Phases()), len(PhaseContracts()))
	}
	for _, c := range PhaseContracts() {
		if missing := c.Incomplete(); len(missing) > 0 {
			t.Fatalf("phase %s is missing: %s", c.State, strings.Join(missing, ", "))
		}
	}
}

// TestEveryPhaseHasAFailureState: a phase with nowhere to fail to fails
// open, which is the one thing the fabric may not do.
func TestEveryPhaseHasAFailureState(t *testing.T) {
	for _, c := range PhaseContracts() {
		if c.FailureState == "" {
			t.Fatalf("phase %s has no failure state and therefore fails open", c.State)
		}
		if _, ok := ContractFor(c.FailureState); !ok {
			t.Fatalf("phase %s fails to %s, which is not a canonical phase", c.State, c.FailureState)
		}
	}
}

// TestEveryPhaseNamesBlockingEvidence is the attribute that separates a
// semantic spine from a workflow: knowledge that stops work, not a gate
// somebody failed to open.
func TestEveryPhaseNamesBlockingEvidence(t *testing.T) {
	for _, c := range PhaseContracts() {
		if len(c.BlockingEvidence) == 0 {
			t.Fatalf("phase %s names no blocking evidence: it has not been thought about", c.State)
		}
	}
}

// TestPhaseContractIsSemanticNotProcedural holds the line the backlog
// drew: Case Fabric is the semantic spine, pkg/workflow is the execution
// mechanism.
//
// A contract whose entry or exit condition describes a procedural act —
// somebody approving, a ticket moving, a queue draining — would mean the
// fabric had become a second workflow engine with nicer names.
func TestPhaseContractIsSemanticNotProcedural(t *testing.T) {
	procedural := []string{
		"clicked", "approved by", "ticket", "queue", "assigned to", "sign-off",
		"submitted the form", "button", "step complete", "task done",
	}
	for _, c := range PhaseContracts() {
		for _, field := range []string{c.Entry, c.Exit} {
			lower := strings.ToLower(field)
			for _, p := range procedural {
				if strings.Contains(lower, p) {
					t.Fatalf("phase %s states a procedural condition (%q): that belongs in pkg/workflow, not the semantic spine",
						c.State, p)
				}
			}
		}
	}
}

// TestCaseFabricDoesNotImportTheWorkflowEngine is the structural half of
// the same rule. Two engines that know about each other converge; two
// that do not stay separable.
func TestCaseFabricDoesNotImportTheWorkflowEngine(t *testing.T) {
	root := moduleRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "pkg", "casefabric"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "pkg", "casefabric", e.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		for _, forbidden := range []string{
			`"veriqo/pkg/workflow"`, `"veriqo/pkg/execution"`, `"veriqo/pkg/kernel/execgraph"`,
		} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s imports %s: the semantic spine must not depend on the execution mechanism",
					e.Name(), forbidden)
			}
		}
	}
}

// TestTheFabricHasNoExecutionVocabulary catches the other direction of
// the same drift: a spine that grows Run, Execute, Schedule or Dispatch
// has started doing the workflow engine's job.
func TestTheFabricHasNoExecutionVocabulary(t *testing.T) {
	root := moduleRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "pkg", "casefabric"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, "pkg", "casefabric", e.Name()))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		for _, forbidden := range []string{
			"func (c *Case) Run(", "func (c *Case) Execute(",
			"func (c *Case) Schedule(", "func (c *Case) Dispatch(",
			"func (c *Case) Enqueue(", "func (c *Case) Retry(",
		} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s defines %s: that is execution, and pkg/workflow owns it", e.Name(), forbidden)
			}
		}
	}
}

// TestEveryPhaseNamesItsOwnerAndAuthority separates the two: the owner
// is whose work it is, the authority is who may move it. Collapsing them
// is how a system ends up letting whoever does the work approve it.
func TestEveryPhaseNamesItsOwnerAndAuthority(t *testing.T) {
	for _, c := range PhaseContracts() {
		if c.Owner == "" || c.Authority == "" {
			t.Fatalf("phase %s must name both an owner and an authority", c.State)
		}
	}
	resolved, _ := ContractFor(PhaseResolved)
	if !strings.Contains(strings.ToLower(resolved.Authority), "distinct from") {
		t.Fatalf("the resolving authority must be distinct from the proof author, got %q", resolved.Authority)
	}
}

// TestEveryPhaseIsReplayable: each phase says how its effect is
// reproduced by a party that does not trust the runtime.
func TestEveryPhaseIsReplayable(t *testing.T) {
	for _, c := range PhaseContracts() {
		if c.ReplayReference == "" {
			t.Fatalf("phase %s has no replay reference", c.State)
		}
	}
}

// TestContractExitConditionsMatchTheCode is the check that keeps the
// contract honest: the exit conditions it states are the ones the case
// engine actually enforces.
func TestContractExitConditionsMatchTheCode(t *testing.T) {
	// UNDER_QUALIFICATION exits on every material claim carrying a
	// sealed proof object. The engine enforces exactly that.
	c := readyForProof(t)
	if _, err := c.Resolve("evidence_package_delivered", "", "a", 9); err == nil {
		t.Fatal("the engine must enforce the stated exit condition for UNDER_QUALIFICATION")
	}

	// HYPOTHESES_FORMED exits on every rival being tested.
	c2 := openScoped(t)
	mustNoErr(t, c2.AddEvidence([]EvidenceRef{{EvidenceID: "E-1", EvidenceVersionID: "v1", SHA256: "abc"}}, "a", 3))
	mustNoErr(t, c2.AddHypothesis(Hypothesis{ID: "H-1", Description: "untested"}, "a", 4))
	mustNoErr(t, c2.BeginQualification("a", 5))
	if _, err := c2.Resolve("closed", "", "a", 6); err == nil {
		t.Fatal("the engine must enforce the stated exit condition for HYPOTHESES_FORMED")
	}
}

func TestRenderContractsCoversEveryPhaseAndAttribute(t *testing.T) {
	out := RenderContracts()
	for _, p := range Phases() {
		if !strings.Contains(out, string(p)) {
			t.Fatalf("phase %s missing from the render", p)
		}
	}
	for _, a := range []string{"ENTRY", "EXIT", "REQUIRED EVIDENCE", "BLOCKING EVIDENCE",
		"AUTHORITY", "OWNER", "FAILURE STATE", "REPLAY"} {
		if !strings.Contains(out, a) {
			t.Fatalf("attribute %s missing from the render", a)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root")
	return ""
}
