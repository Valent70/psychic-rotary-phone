package evidence

import "testing"

func TestAddDependencyRejectsSelfLoop(t *testing.T) {
	g := NewDependencyGraph()
	if err := g.AddDependency("A", "A"); err != ErrDependencySelfLoop {
		t.Fatalf("expected ErrDependencySelfLoop, got %v", err)
	}
}

func TestAddDependencyRejectsCycle(t *testing.T) {
	g := NewDependencyGraph()
	// Survey -> Statement -> Photo
	if err := g.AddDependency("Survey", "Statement"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := g.AddDependency("Statement", "Photo"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	// Photo -> Survey would close the loop.
	if err := g.AddDependency("Photo", "Survey"); err != ErrDependencyCycle {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}
}

// TestPhotoStatementSurveyChain reproduces the blueprint's own §10
// worked example EXACTLY:
//
//	Photo A -> Statement A -> Survey A
//
// and asserts the collapsed independent-source count is 1, not 3.
func TestPhotoStatementSurveyChain(t *testing.T) {
	g := NewDependencyGraph()
	if err := g.AddDependency("StatementA", "PhotoA"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := g.AddDependency("SurveyA", "StatementA"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	ids := []string{"PhotoA", "StatementA", "SurveyA"}
	roots := g.Roots(ids)
	if len(roots) != 1 {
		t.Fatalf("expected 1 independent root for Photo A -> Statement A -> Survey A, got %d: %v", len(roots), roots)
	}
	if roots[0] != "PhotoA" {
		t.Fatalf("expected the independent root to be PhotoA, got %s", roots[0])
	}
	if got := g.IndependentCount(ids); got != 1 {
		t.Fatalf("IndependentCount: expected 1, got %d", got)
	}
}

func TestTrulyIndependentEvidenceCountsSeparately(t *testing.T) {
	g := NewDependencyGraph()
	// No dependencies recorded between A, B, C: each is its own root.
	ids := []string{"A", "B", "C"}
	if got := g.IndependentCount(ids); got != 3 {
		t.Fatalf("expected 3 independent roots for unrelated evidence, got %d", got)
	}
}

func TestRootReportsCorrectly(t *testing.T) {
	g := NewDependencyGraph()
	g.AddDependency("Child", "Parent")
	if g.Root("Child") {
		t.Fatal("expected Child to not be a root (it depends on Parent)")
	}
	if !g.Root("Parent") {
		t.Fatal("expected Parent to be a root (no recorded dependency)")
	}
	if !g.Root("Unrelated") {
		t.Fatal("expected an item with no recorded edges to be a root")
	}
}

func TestDirectParents(t *testing.T) {
	g := NewDependencyGraph()
	g.AddDependency("Survey", "Statement")
	g.AddDependency("Survey", "SiteVisit") // survey cites two independent sources
	parents := g.DirectParents("Survey")
	if len(parents) != 2 {
		t.Fatalf("expected 2 direct parents, got %d: %v", len(parents), parents)
	}
}

func TestPartiallySharedRoots(t *testing.T) {
	g := NewDependencyGraph()
	// StatementB and StatementC both cite PhotoA (shared root), but
	// StatementD is fully independent.
	g.AddDependency("StatementB", "PhotoA")
	g.AddDependency("StatementC", "PhotoA")

	roots := g.Roots([]string{"StatementB", "StatementC", "StatementD"})
	if len(roots) != 2 { // PhotoA (shared) + StatementD (independent)
		t.Fatalf("expected 2 independent roots, got %d: %v", len(roots), roots)
	}
}

func TestAddDependencyRejectsEmptyIDs(t *testing.T) {
	g := NewDependencyGraph()
	if err := g.AddDependency("", "X"); err == nil {
		t.Fatal("expected error for empty child")
	}
	if err := g.AddDependency("X", ""); err == nil {
		t.Fatal("expected error for empty parent")
	}
}
