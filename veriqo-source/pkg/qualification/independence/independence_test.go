package independence

import (
	"errors"
	"testing"
)

func src(id string, attrs map[Dimension]string) Source {
	return Source{ID: id, Attributes: attrs}
}

// fullyAssessed returns attributes covering every disqualifying
// dimension, so a pair built from two of these can reach Independent.
func fullyAssessed(suffix string) map[Dimension]string {
	m := map[Dimension]string{}
	for _, d := range DisqualifyingDimensions() {
		m[d] = string(d) + "-" + suffix
	}
	return m
}

// TestSharedRootIsDependent is Article 3: two feeds with different
// vendor labels that share a root origin are ONE source.
func TestSharedRootIsDependent(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	b[RootOrigin] = a[RootOrigin] // same constellation, different vendor

	got, err := Assess(src("feed-alpha", a), src("feed-beta", b))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != Dependent {
		t.Fatalf("shared root must be DEPENDENT, got %s: %s", got.Verdict, got.Reason)
	}
	if len(got.SharedDisqualifying) != 1 || got.SharedDisqualifying[0] != RootOrigin {
		t.Fatalf("expected RootOrigin as the shared dimension, got %v", got.SharedDisqualifying)
	}
}

// TestEveryDisqualifyingDimensionIsFatal walks each one individually
// rather than trusting that one representative case implies the rest.
func TestEveryDisqualifyingDimensionIsFatal(t *testing.T) {
	for _, d := range DisqualifyingDimensions() {
		a := fullyAssessed("a")
		b := fullyAssessed("b")
		b[d] = a[d]
		got, err := Assess(src("A", a), src("B", b))
		if err != nil {
			t.Fatalf("Assess sharing %s: %v", d, err)
		}
		if got.Verdict != Dependent {
			t.Fatalf("sharing %s must be DEPENDENT, got %s", d, got.Verdict)
		}
	}
}

// TestUnassessedDimensionYieldsUnknownNotIndependent is Article 28 and
// the expensive rule: silence never becomes independence.
func TestUnassessedDimensionYieldsUnknownNotIndependent(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	delete(b, ModelDependency) // nobody checked whether they share a model

	got, err := Assess(src("A", a), src("B", b))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != Unknown {
		t.Fatalf("an unassessed disqualifying dimension must be UNKNOWN, got %s", got.Verdict)
	}
	if got.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("UNKNOWN must not satisfy an independence requirement")
	}
}

func TestFullyDistinctIsIndependent(t *testing.T) {
	got, err := Assess(src("A", fullyAssessed("a")), src("B", fullyAssessed("b")))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != Independent {
		t.Fatalf("expected INDEPENDENT, got %s: %s", got.Verdict, got.Reason)
	}
	if !got.Verdict.SatisfiesIndependenceRequirement() {
		t.Fatal("INDEPENDENT must satisfy an independence requirement")
	}
}

// TestOnlyIndependentSatisfiesRequirement pins the one-line rule.
func TestOnlyIndependentSatisfiesRequirement(t *testing.T) {
	for _, v := range []Verdict{Dependent, Unknown} {
		if v.SatisfiesIndependenceRequirement() {
			t.Fatalf("%s must not satisfy an independence requirement", v)
		}
	}
	if !Independent.SatisfiesIndependenceRequirement() {
		t.Fatal("INDEPENDENT must satisfy")
	}
}

// TestUnassessedIsTheZeroRelation proves a dimension nobody looked at
// defaults to UNASSESSED, never DISTINCT.
func TestUnassessedIsTheZeroRelation(t *testing.T) {
	var r Relation
	if r != Unassessed || r.String() != "UNASSESSED" {
		t.Fatalf("the zero Relation must be UNASSESSED, got %v", r)
	}
}

func TestEmptyAttributeValueCountsAsUnassessed(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	b[Collector] = "" // present but blank

	got, err := Assess(src("A", a), src("B", b))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != Unknown {
		t.Fatalf("a blank attribute must count as unassessed, got %s", got.Verdict)
	}
}

func TestRequireIndependentFailsOnUnknown(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	delete(b, DataCustody)

	if _, err := RequireIndependent(src("A", a), src("B", b)); !errors.Is(err, ErrNotIndependent) {
		t.Fatalf("expected ErrNotIndependent for UNKNOWN, got %v", err)
	}
}

func TestAssessRejectsSelfComparison(t *testing.T) {
	s := src("same", fullyAssessed("a"))
	if _, err := Assess(s, s); !errors.Is(err, ErrSameSource) {
		t.Fatalf("expected ErrSameSource, got %v", err)
	}
}

func TestAssessRejectsEmptyID(t *testing.T) {
	if _, err := Assess(src("", nil), src("B", nil)); !errors.Is(err, ErrEmptySourceID) {
		t.Fatalf("expected ErrEmptySourceID, got %v", err)
	}
}

// TestClusterIsTransitive is the subtle correctness property: if A
// shares a root with B, and B shares a pipeline with C, then A and C
// are in one cluster even though they share nothing directly.
// Corroboration among them would still be circular.
func TestClusterIsTransitive(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	c := fullyAssessed("c")
	b[RootOrigin] = a[RootOrigin]             // A ~ B
	c[ProviderPipeline] = b[ProviderPipeline] // B ~ C, but A and C share nothing

	// Confirm A and C really are not directly dependent.
	direct, err := Assess(src("A", a), src("C", c))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if direct.Verdict == Dependent {
		t.Fatal("test setup is wrong: A and C should not be directly dependent")
	}

	clusters, err := Cluster([]Source{src("A", a), src("B", b), src("C", c)})
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("A, B and C should form one transitive cluster, got %d clusters: %v", len(clusters), clusters)
	}
	if len(clusters[0]) != 3 {
		t.Fatalf("expected all three in one cluster, got %v", clusters[0])
	}
}

// TestEffectiveSourceCountCollapsesDuplicates is the operational form
// of Article 3: three feeds can be one source.
func TestEffectiveSourceCountCollapsesDuplicates(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	c := fullyAssessed("c")
	b[RootOrigin] = a[RootOrigin]
	c[RootOrigin] = a[RootOrigin]

	n, err := EffectiveSourceCount([]Source{src("A", a), src("B", b), src("C", c)})
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if n != 1 {
		t.Fatalf("three same-root feeds are ONE effective source, got %d", n)
	}
}

func TestEffectiveSourceCountKeepsGenuinelyDistinctSources(t *testing.T) {
	n, err := EffectiveSourceCount([]Source{
		src("A", fullyAssessed("a")), src("B", fullyAssessed("b")),
	})
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("two distinct sources should count as 2, got %d", n)
	}
}

// TestUnknownDoesNotMergeClusters proves Cluster resolves nothing
// optimistically OR pessimistically: only an established dependency
// merges. Unknown pairs are reported by Assess, not silently decided.
func TestUnknownDoesNotMergeClusters(t *testing.T) {
	a := fullyAssessed("a")
	b := fullyAssessed("b")
	delete(b, Collector) // A/B is UNKNOWN, not Dependent

	clusters, err := Cluster([]Source{src("A", a), src("B", b)})
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("an UNKNOWN pair must not be merged into one cluster, got %v", clusters)
	}
}

func TestAssessReportsEveryDimension(t *testing.T) {
	got, err := Assess(src("A", fullyAssessed("a")), src("B", fullyAssessed("b")))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(got.Relations) != len(Dimensions()) {
		t.Fatalf("expected a relation for all %d dimensions, got %d", len(Dimensions()), len(got.Relations))
	}
}

func TestFifteenDimensionsAreDefined(t *testing.T) {
	if len(Dimensions()) != 15 {
		t.Fatalf("the specification names fifteen dimensions, got %d", len(Dimensions()))
	}
}

// TestUnknownIsNotCountedTowardsCorroboration closes an Article 28 gap
// found while proving the cross-domain semantic properties.
//
// EffectiveSourceCount answers "how many clusters is this", and it
// answers conservatively: an unassessed pair does not merge. That is
// correct for clustering and wrong for corroboration, because two
// sources that were never assessed count as two -- which is UNKNOWN
// being read as INDEPENDENT at exactly the point where it matters.
func TestUnknownIsNotCountedTowardsCorroboration(t *testing.T) {
	assessed := Source{ID: "ais-provider", Attributes: fullyAssessed("ais")}
	unassessed := Source{ID: "an-unassessed-feed"}

	// The cluster count says two, and it is not wrong about clusters.
	clusters, err := EffectiveSourceCount([]Source{assessed, unassessed})
	if err != nil {
		t.Fatalf("EffectiveSourceCount: %v", err)
	}
	if clusters != 2 {
		t.Fatalf("cluster count = %d, want 2", clusters)
	}

	// The corroboration count must not.
	count, unknown, err := EffectiveIndependentCount([]Source{assessed, unassessed})
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if count >= 2 {
		t.Fatalf("an unassessed source counted towards corroboration (count=%d): "+
			"UNKNOWN was promoted to INDEPENDENT", count)
	}
	if len(unknown) == 0 {
		t.Fatal("the unassessed pair was not reported: a caller cannot go and assess what it is not told about")
	}
}

// TestFullyAssessedSourcesStillCorroborate. The strict count must not
// be so strict that nothing ever corroborates.
func TestFullyAssessedSourcesStillCorroborate(t *testing.T) {
	count, unknown, err := EffectiveIndependentCount([]Source{
		{ID: "a", Attributes: fullyAssessed("a")},
		{ID: "b", Attributes: fullyAssessed("b")},
		{ID: "c", Attributes: fullyAssessed("c")},
	})
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("fully assessed sources reported unknown pairs: %v", unknown)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

// TestDependentSourcesCountOnce under the strict count too.
func TestDependentSourcesCountOnce(t *testing.T) {
	aAttrs, bAttrs := fullyAssessed("a"), fullyAssessed("b")
	// Same root: a positively established dependency.
	bAttrs[RootOrigin] = aAttrs[RootOrigin]
	a := Source{ID: "a", Attributes: aAttrs}
	b := Source{ID: "b", Attributes: bAttrs}
	count, _, err := EffectiveIndependentCount([]Source{a, b})
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if count != 1 {
		t.Fatalf("two same-root sources count as %d, want 1", count)
	}
}

// TestASingleSourceIsNotDisqualifiedByAnAssessmentThatNeverHappened.
func TestASingleSourceIsNotDisqualifiedByAnAssessmentThatNeverHappened(t *testing.T) {
	count, unknown, err := EffectiveIndependentCount([]Source{{ID: "only-one"}})
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if count != 1 || len(unknown) != 0 {
		t.Fatalf("count=%d unknown=%v; a lone source corroborates nothing but is not disqualified", count, unknown)
	}
}
