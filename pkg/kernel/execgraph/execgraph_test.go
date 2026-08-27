package execgraph

import "testing"

func buildABC(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	if err := g.AddNode("A"); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := g.AddNode("B", "A"); err != nil {
		t.Fatalf("add B: %v", err)
	}
	if err := g.AddNode("C", "B"); err != nil {
		t.Fatalf("add C: %v", err)
	}
	return g
}

func TestAutomaticDependencyChain(t *testing.T) {
	g := buildABC(t)
	if got := g.Frontier(); len(got) != 1 || got[0] != "A" {
		t.Fatalf("expected only A ready, got %v", got)
	}
	if _, err := g.Status("B"); err != nil {
		t.Fatal(err)
	}
	st, _ := g.Status("B")
	if st != Pending {
		t.Fatalf("expected B Pending before A done, got %s", st)
	}
	g.SetStatus("A", Running, 1)
	g.SetStatus("A", Done, 2)
	if got := g.Frontier(); len(got) != 1 || got[0] != "B" {
		t.Fatalf("expected only B ready after A done, got %v", got)
	}
	g.SetStatus("B", Running, 3)
	g.SetStatus("B", Done, 4)
	if got := g.Frontier(); len(got) != 1 || got[0] != "C" {
		t.Fatalf("expected only C ready after B done, got %v", got)
	}
}

func TestCannotRunBeforeReady(t *testing.T) {
	g := buildABC(t)
	if err := g.SetStatus("B", Running, 1); err == nil {
		t.Fatal("expected ErrNotReady running B before A completes")
	}
}

func TestCycleRejected(t *testing.T) {
	g := buildABC(t)
	if err := g.AddEdge("C", "A"); err == nil {
		t.Fatal("expected ErrCycle for C -> A when A -> B -> C already exists")
	}
}

func TestDependenciesAndDependents(t *testing.T) {
	g := buildABC(t)
	if deps := g.Dependencies("B"); len(deps) != 1 || deps[0] != "A" {
		t.Fatalf("unexpected deps: %v", deps)
	}
	if rdeps := g.Dependents("A"); len(rdeps) != 1 || rdeps[0] != "B" {
		t.Fatalf("unexpected rdeps: %v", rdeps)
	}
}

func TestLineageMultiHop(t *testing.T) {
	g := buildABC(t)
	lineage := g.Lineage("C", false, 10)
	if len(lineage) != 2 || lineage[0] != "B" || lineage[1] != "A" {
		t.Fatalf("expected [B A] backward lineage from C, got %v", lineage)
	}
	forward := g.Lineage("A", true, 10)
	if len(forward) != 2 || forward[0] != "B" || forward[1] != "C" {
		t.Fatalf("expected [B C] forward impact from A, got %v", forward)
	}
}

func TestTimelineRecordsTransitions(t *testing.T) {
	g := buildABC(t)
	g.SetStatus("A", Running, 5)
	g.SetStatus("A", Done, 6)
	tl := g.Timeline()
	if len(tl) != 2 {
		t.Fatalf("expected 2 events, got %d", len(tl))
	}
	if tl[0].To != Running || tl[1].To != Done || tl[1].Tick != 6 {
		t.Fatalf("unexpected timeline: %+v", tl)
	}
}

func TestAllDone(t *testing.T) {
	g := buildABC(t)
	if g.AllDone() {
		t.Fatal("expected not all done initially")
	}
	g.SetStatus("A", Running, 1)
	g.SetStatus("A", Done, 1)
	g.SetStatus("B", Running, 2)
	g.SetStatus("B", Done, 2)
	g.SetStatus("C", Running, 3)
	g.SetStatus("C", Done, 3)
	if !g.AllDone() {
		t.Fatal("expected all done")
	}
}
