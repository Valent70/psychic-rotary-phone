package workflow

import (
	"errors"
	"testing"

	"veriqo/pkg/platform/audit"
)

func collectorAgent(in AgentInput) (any, error) { return "raw-evidence", nil }

func fusionAgent(in AgentInput) (any, error) {
	raw := in.Outputs["collector"].(string)
	return "fused:" + raw, nil
}

func reasoningAgent(in AgentInput) (any, error) {
	fused := in.Outputs["fusion"].(string)
	return "reasoned:" + fused, nil
}

func decisionAgent(in AgentInput) (any, error) {
	reasoned := in.Outputs["reasoning"].(string)
	return "decision:" + reasoned, nil
}

func reportingAgent(in AgentInput) (any, error) {
	dec := in.Outputs["decision"].(string)
	return "report:" + dec, nil
}

func darkVesselPlan() Plan {
	return Plan{
		Name: "dark_vessel_pipeline",
		Steps: []Step{
			{Name: "collector", Agent: collectorAgent},
			{Name: "fusion", DependsOn: []string{"collector"}, Agent: fusionAgent},
			{Name: "reasoning", DependsOn: []string{"fusion"}, Agent: reasoningAgent},
			{Name: "decision", DependsOn: []string{"reasoning"}, Agent: decisionAgent},
			{Name: "reporting", DependsOn: []string{"decision"}, Agent: reportingAgent},
		},
	}
}

func TestPlannerOrderRespectsDependencies(t *testing.T) {
	order, err := (Planner{}).Order(darkVesselPlan())
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	want := []string{"collector", "fusion", "reasoning", "decision", "reporting"}
	if len(order) != len(want) {
		t.Fatalf("order length mismatch: %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, order[i], want[i], order)
		}
	}
}

func TestPlannerDetectsCycle(t *testing.T) {
	p := Plan{Steps: []Step{
		{Name: "a", DependsOn: []string{"b"}, Agent: collectorAgent},
		{Name: "b", DependsOn: []string{"a"}, Agent: collectorAgent},
	}}
	if _, err := (Planner{}).Order(p); !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("expected ErrCycleDetected, got %v", err)
	}
}

func TestPlannerDetectsUnknownDependency(t *testing.T) {
	p := Plan{Steps: []Step{{Name: "a", DependsOn: []string{"ghost"}, Agent: collectorAgent}}}
	if _, err := (Planner{}).Order(p); !errors.Is(err, ErrUnknownDependency) {
		t.Fatalf("expected ErrUnknownDependency, got %v", err)
	}
}

func TestPlannerDetectsDuplicateStep(t *testing.T) {
	p := Plan{Steps: []Step{{Name: "a", Agent: collectorAgent}, {Name: "a", Agent: collectorAgent}}}
	if _, err := (Planner{}).Order(p); !errors.Is(err, ErrDuplicateStep) {
		t.Fatalf("expected ErrDuplicateStep, got %v", err)
	}
}

func TestExecutorRunsFullPipeline(t *testing.T) {
	store := audit.NewAuditStore()
	ex := NewExecutor(store)
	sched := NewScheduler()
	plan := darkVesselPlan()
	record, err := sched.Schedule(plan, 42)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	record, err = ex.Run(plan, record)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !record.Done {
		t.Fatal("expected run to be Done")
	}
	got := record.Completed["reporting"].Output.(string)
	want := "report:decision:reasoned:fused:raw-evidence"
	if got != want {
		t.Fatalf("unexpected final output: %s", got)
	}
	// Every step should have produced an audit checkpoint plus one
	// RUN_COMPLETE record.
	if len(store.Snapshot()) != len(plan.Steps)+1 {
		t.Fatalf("expected %d audit records, got %d", len(plan.Steps)+1, len(store.Snapshot()))
	}
}

// TestExecutorResumeAfterSimulatedCrash proves the concrete
// Checkpoint+Recovery behavior the audit demands: a workflow that fails
// partway through resumes from the next incomplete step, never
// re-running already-completed (and checkpointed) steps.
func TestExecutorResumeAfterSimulatedCrash(t *testing.T) {
	store := audit.NewAuditStore()
	ex := NewExecutor(store)
	sched := NewScheduler()

	var collectorCalls, fusionCalls int
	failOnce := true
	plan := Plan{
		Name: "resumable",
		Steps: []Step{
			{Name: "collector", Agent: func(in AgentInput) (any, error) {
				collectorCalls++
				return "raw", nil
			}},
			{Name: "fusion", DependsOn: []string{"collector"}, Agent: func(in AgentInput) (any, error) {
				fusionCalls++
				if failOnce {
					failOnce = false
					return nil, errors.New("simulated transient failure")
				}
				return "fused:" + in.Outputs["collector"].(string), nil
			}},
			{Name: "decision", DependsOn: []string{"fusion"}, Agent: func(in AgentInput) (any, error) {
				return "decision:" + in.Outputs["fusion"].(string), nil
			}},
		},
	}

	record, err := sched.Schedule(plan, 1)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	record, err = ex.Run(plan, record)
	if err == nil {
		t.Fatal("expected first run to fail at fusion step")
	}
	if record.Done {
		t.Fatal("run should not be marked Done after a failed step")
	}
	if record.NextIndex != 1 {
		t.Fatalf("expected NextIndex=1 (paused at fusion), got %d", record.NextIndex)
	}

	// Simulate recovery: a new process resumes using only the RunID.
	resumed, err := ex.Resume(record.RunID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed.Done {
		t.Fatal("expected resumed run to complete")
	}
	if collectorCalls != 1 {
		t.Fatalf("collector must not be re-executed on resume, called %d times", collectorCalls)
	}
	if fusionCalls != 2 {
		t.Fatalf("expected fusion to be called twice (fail then succeed), got %d", fusionCalls)
	}
	got := resumed.Completed["decision"].Output.(string)
	if got != "decision:fused:raw" {
		t.Fatalf("unexpected final output after resume: %s", got)
	}
}

func TestResumeUnknownRunID(t *testing.T) {
	ex := NewExecutor(audit.NewAuditStore())
	if _, err := ex.Resume("nonexistent"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("expected ErrNoSuchRun, got %v", err)
	}
}

func TestSchedulerDeterministicRunID(t *testing.T) {
	sched := NewScheduler()
	plan := darkVesselPlan()
	r1, err := sched.Schedule(plan, 7)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := sched.Schedule(plan, 7)
	if err != nil {
		t.Fatal(err)
	}
	if r1.RunID != r2.RunID {
		t.Fatalf("expected deterministic RunID, got %s vs %s", r1.RunID, r2.RunID)
	}
	r3, _ := sched.Schedule(plan, 8)
	if r3.RunID == r1.RunID {
		t.Fatal("expected different tick to produce a different RunID")
	}
}
