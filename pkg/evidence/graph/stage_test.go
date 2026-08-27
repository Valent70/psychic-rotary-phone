package graph

import (
	"errors"
	"testing"
)

func mustS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMarkStageRejectsUnknownGraphNode(t *testing.T) {
	g := NewGraph()
	st := NewStageTracker(g)
	err := st.MarkStage(NodeID("ghost"), StageSource)
	if err == nil {
		t.Fatalf("expected error for unknown node")
	}
}

func TestMarkStageRejectsDoubleAssignment(t *testing.T) {
	g := NewGraph()
	st := NewStageTracker(g)
	id := g.AddNode("source", "ais-vendor-1")
	mustS(t, st.MarkStage(id, StageSource))
	if err := st.MarkStage(id, StageSource); !errors.Is(err, ErrStageAlreadySet) {
		t.Fatalf("expected ErrStageAlreadySet, got %v", err)
	}
}

func TestMarkStageRejectsInvalidStage(t *testing.T) {
	g := NewGraph()
	st := NewStageTracker(g)
	id := g.AddNode("source", "x")
	if err := st.MarkStage(id, Stage(99)); !errors.Is(err, ErrUnknownStage) {
		t.Fatalf("expected ErrUnknownStage, got %v", err)
	}
}

// TestMarkStageRejectsPipelineRegression is the load-bearing test for
// PRIORITAS #3: a node cannot be labeled earlier in the pipeline than
// something it was derived from.
func TestMarkStageRejectsPipelineRegression(t *testing.T) {
	g := NewGraph()
	st := NewStageTracker(g)
	fused := g.AddNode("claim", "vessel-1-flag=PANAMA")
	raw := g.AddNode("raw_observation", "ais-1 says PANAMA")
	mustS(t, g.AddEdge(fused, raw, RelationDerivedFrom))

	mustS(t, st.MarkStage(raw, StageRawObservation))
	// fused was derived from raw (RawObservation); labeling fused as
	// also RawObservation (or earlier) must be rejected.
	if err := st.MarkStage(fused, StageRawObservation); !errors.Is(err, ErrStageRegressesLineage) {
		t.Fatalf("expected ErrStageRegressesLineage, got %v", err)
	}
	// but a later stage is fine
	mustS(t, st.MarkStage(fused, StageFusion))
}

func TestStageLineageReturnsFullTypedPipeline(t *testing.T) {
	g := NewGraph()
	st := NewStageTracker(g)

	source := g.AddNode("source", "ais-vendor-1")
	raw := g.AddNode("raw_observation", "PANAMA@tick1")
	norm := g.AddNode("normalized", "flag=PANAMA")
	fused := g.AddNode("fused", "PANAMA conf=0.9")
	arb := g.AddNode("truth_version", "PANAMA winner")
	claim := g.AddNode("claim", "vessel-1 flag PANAMA")
	decision := g.AddNode("decision", "FLAG vessel-1")

	mustS(t, g.AddEdge(raw, source, RelationDerivedFrom))
	mustS(t, g.AddEdge(norm, raw, RelationDerivedFrom))
	mustS(t, g.AddEdge(fused, norm, RelationDerivedFrom))
	mustS(t, g.AddEdge(arb, fused, RelationDerivedFrom))
	mustS(t, g.AddEdge(claim, arb, RelationDerivedFrom))
	mustS(t, g.AddEdge(decision, claim, RelationDerivedFrom))

	mustS(t, st.MarkStage(source, StageSource))
	mustS(t, st.MarkStage(raw, StageRawObservation))
	mustS(t, st.MarkStage(norm, StageNormalization))
	mustS(t, st.MarkStage(fused, StageFusion))
	mustS(t, st.MarkStage(arb, StageArbitration))
	mustS(t, st.MarkStage(claim, StageClaim))
	mustS(t, st.MarkStage(decision, StageDecision))

	lineage, err := st.StageLineage(decision)
	mustS(t, err)
	if len(lineage) != 7 {
		t.Fatalf("expected 7 staged nodes in full pipeline lineage, got %d: %+v", len(lineage), lineage)
	}
	for i, want := range []Stage{StageSource, StageRawObservation, StageNormalization, StageFusion, StageArbitration, StageClaim, StageDecision} {
		if lineage[i].Stage != want {
			t.Fatalf("position %d: expected %s, got %s", i, want, lineage[i].Stage)
		}
	}

	completeness, err := st.LineageCompleteness(decision)
	mustS(t, err)
	// TRANSFORMATION stage was skipped in this scenario -> 7 of 8 present
	if diff := completeness - 7.0/8.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected completeness 7/8, got %f", completeness)
	}
}

func TestLineageCompletenessZeroForUnstagedNode(t *testing.T) {
	g := NewGraph()
	st := NewStageTracker(g)
	id := g.AddNode("x", "y")
	c, err := st.LineageCompleteness(id)
	mustS(t, err)
	if c != 0 {
		t.Fatalf("expected 0 completeness for unstaged isolated node, got %f", c)
	}
}
