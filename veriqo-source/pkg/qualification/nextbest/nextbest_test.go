package nextbest

import (
	"errors"
	"testing"
)

// permitted returns a candidate that passes every hard gate, so tests
// can vary one thing at a time.
func permitted(id string) Candidate {
	return Candidate{
		ID: id, SourceID: "src-" + id,
		RightsGranted: true, AuthorityGranted: true,
		SourcePermitted: true, WithinCaseScope: true,
		DiagnosticValue: 0.5, Independence: 0.5, Relevance: 0.5,
		Freshness: 0.5, AcquisitionFeasibility: 0.5,
		Cost: 1, Latency: 1, RightsRisk: 1,
	}
}

// TestHardFiltersRunBeforeOptimization is THE property of this
// package. A candidate with maximal diagnostic value and zero cost is
// still removed when rights are absent -- Article 4 is not a term in a
// ratio.
func TestHardFiltersRunBeforeOptimization(t *testing.T) {
	irresistible := permitted("perfect")
	irresistible.RightsGranted = false
	irresistible.DiagnosticValue = 1
	irresistible.Independence = 1
	irresistible.Relevance = 1
	irresistible.Freshness = 1
	irresistible.AcquisitionFeasibility = 1
	irresistible.Cost = 0.0001
	irresistible.Latency = 0.0001
	irresistible.RightsRisk = 0.0001

	modest := permitted("lawful")

	r, err := Rank([]Candidate{irresistible, modest})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	for _, s := range r.Ranked {
		if s.ID == "perfect" {
			t.Fatal("a rights-denied candidate was ranked; the hard filter did not precede optimization")
		}
	}
	best, ok := Best(r)
	if !ok || best != "lawful" {
		t.Fatalf("expected the lawful candidate to win, got %q (ok=%v)", best, ok)
	}
}

// TestEveryHardGateExcludes walks each gate individually.
func TestEveryHardGateExcludes(t *testing.T) {
	cases := map[string]func(*Candidate){
		string(NoRights):         func(c *Candidate) { c.RightsGranted = false },
		string(NoAuthority):      func(c *Candidate) { c.AuthorityGranted = false },
		string(ProhibitedSource): func(c *Candidate) { c.SourcePermitted = false },
		string(OutOfScope):       func(c *Candidate) { c.WithinCaseScope = false },
	}
	for want, mut := range cases {
		c := permitted("x")
		mut(&c)
		r, err := Rank([]Candidate{c})
		if err != nil {
			t.Fatalf("Rank: %v", err)
		}
		if len(r.Ranked) != 0 {
			t.Fatalf("%s: candidate should have been excluded", want)
		}
		if len(r.Excluded) != 1 || string(r.Excluded[0].Reason) != want {
			t.Fatalf("expected exclusion reason %s, got %+v", want, r.Excluded)
		}
	}
}

// TestPartyMediatedFilteredOnlyWhenIndependenceRequired proves the
// gate is contextual: party-mediated evidence is still evidence, and
// is only excluded where independence is actually required.
func TestPartyMediatedFilteredOnlyWhenIndependenceRequired(t *testing.T) {
	c := permitted("party-doc")
	c.PartyMediated = true

	r, err := Rank([]Candidate{c})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Ranked) != 1 {
		t.Fatal("party-mediated evidence should be permitted when independence is not required")
	}

	c.IndependenceRequired = true
	r2, err := Rank([]Candidate{c})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r2.Ranked) != 0 || r2.Excluded[0].Reason != PartyMediated {
		t.Fatalf("expected exclusion where independence is required, got %+v", r2)
	}
}

// TestExcludedCandidatesAreReported proves an analyst can tell that
// the best option was filtered out on rights rather than simply
// scoring poorly.
func TestExcludedCandidatesAreReported(t *testing.T) {
	denied := permitted("denied")
	denied.RightsGranted = false

	r, err := Rank([]Candidate{denied, permitted("ok")})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(r.Excluded) != 1 || r.Excluded[0].ID != "denied" {
		t.Fatalf("exclusions must be reported, got %+v", r.Excluded)
	}
}

func TestPriorityFormulaOrdersCorrectly(t *testing.T) {
	high := permitted("high")
	high.DiagnosticValue = 0.9
	low := permitted("low")
	low.DiagnosticValue = 0.1

	r, err := Rank([]Candidate{low, high})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if r.Ranked[0].ID != "high" || r.Ranked[0].Rank != 1 {
		t.Fatalf("higher diagnostic value must rank first, got %+v", r.Ranked)
	}
	if r.Ranked[1].Rank != 2 {
		t.Fatalf("ranks must be sequential, got %+v", r.Ranked)
	}
}

// TestCostAndLatencyReducePriority checks the divisor behaves.
func TestCostAndLatencyReducePriority(t *testing.T) {
	cheap := permitted("cheap")
	dear := permitted("dear")
	dear.Cost = 10

	pc, err := Priority(cheap)
	if err != nil {
		t.Fatalf("Priority: %v", err)
	}
	pd, err := Priority(dear)
	if err != nil {
		t.Fatalf("Priority: %v", err)
	}
	if !(pc > pd) {
		t.Fatalf("higher cost must lower priority: cheap=%v dear=%v", pc, pd)
	}
}

// TestRankingIsDeterministic matters because the output drives an
// acquisition decision that will later be reviewed.
func TestRankingIsDeterministic(t *testing.T) {
	a, b, c := permitted("a"), permitted("b"), permitted("c") // identical scores
	r1, err := Rank([]Candidate{a, b, c})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	r2, err := Rank([]Candidate{c, b, a}) // different input order
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	for i := range r1.Ranked {
		if r1.Ranked[i].ID != r2.Ranked[i].ID {
			t.Fatalf("ranking is not deterministic under input reordering: %+v vs %+v", r1.Ranked, r2.Ranked)
		}
	}
	if r1.Ranked[0].ID != "a" {
		t.Fatalf("ties must break on ID, got %q first", r1.Ranked[0].ID)
	}
}

// TestBestDistinguishesEmptyFromFiltered is the semantic that matters
// operationally: "nothing is permissible" differs from "nothing scored
// well".
func TestBestDistinguishesEmptyFromFiltered(t *testing.T) {
	denied := permitted("denied")
	denied.RightsGranted = false

	r, err := Rank([]Candidate{denied})
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if _, ok := Best(r); ok {
		t.Fatal("Best must report false when every candidate was filtered out")
	}
	if len(r.Excluded) != 1 {
		t.Fatal("the caller must still be able to see why")
	}
}

func TestValidateRejectsOutOfRangeFactors(t *testing.T) {
	for _, bad := range []float64{-0.1, 1.1} {
		c := permitted("x")
		c.DiagnosticValue = bad
		if err := Validate(c); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("DiagnosticValue=%v must be rejected, got %v", bad, err)
		}
	}
}

func TestValidateRejectsNonPositiveDivisors(t *testing.T) {
	for _, bad := range []float64{0, -1} {
		c := permitted("x")
		c.Cost = bad
		if err := Validate(c); !errors.Is(err, ErrNonPositive) {
			t.Fatalf("Cost=%v must be rejected, got %v", bad, err)
		}
	}
}

func TestRankRejectsEmptyInput(t *testing.T) {
	if _, err := Rank(nil); !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("expected ErrNoCandidates, got %v", err)
	}
}

func TestRankRejectsEmptyCandidateID(t *testing.T) {
	c := permitted("")
	if _, err := Rank([]Candidate{c}); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("expected ErrEmptyID, got %v", err)
	}
}
