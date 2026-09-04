package hypothesis

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/contract"
)

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "hypothesis", Revision: 1},
	}
}

// The specification's own worked example.
func cargoHypotheses() []Hypothesis {
	return []Hypothesis{
		{ID: "H1", Statement: "the cargo was physically lost",
			Expected: []string{"loading evidence", "discharge discrepancy", "a physical mechanism"},
			Excluded: []string{"a density mismatch explaining the whole difference"}},
		{ID: "H2", Statement: "a measurement conversion error",
			Expected: []string{"density mismatch", "temperature difference", "calibration issue"}},
		{ID: "H3", Statement: "the loading quantity was overstated",
			Expected: []string{"loading-side inconsistency", "shore-tank discrepancy"}},
	}
}

func obs(id string, weightFactors ...float64) Observation {
	r, i, f := 1.0, 1.0, 1.0
	if len(weightFactors) == 3 {
		r, i, f = weightFactors[0], weightFactors[1], weightFactors[2]
	}
	return Observation{ID: id, Detail: id, Reliability: r, Independence: i, Freshness: f,
		TemporalFit: true, MeasurementCompatible: true, EvidenceRefs: []string{"ev:" + id}}
}

func matrix(t *testing.T) *Matrix {
	t.Helper()
	m, err := NewMatrix("t-acme", "case-1", cargoHypotheses(), versions())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestASingleHypothesisIsRefused. Assessed alone, every hypothesis is
// consistent with the evidence, because evidence that would
// distinguish it was never sought.
func TestASingleHypothesisIsRefused(t *testing.T) {
	_, err := NewMatrix("t-acme", "case-1", cargoHypotheses()[:1], versions())
	if !errors.Is(err, ErrSingleHypothesis) {
		t.Fatalf("a one-hypothesis matrix was built: %v", err)
	}
}

// TestAHypothesisWithNoExpectedObservationsCannotBeTested.
func TestAHypothesisWithNoExpectedObservationsCannotBeTested(t *testing.T) {
	h := Hypothesis{ID: "H1", Statement: "something happened"}
	if err := h.Validate(); !errors.Is(err, ErrNoExpectation) {
		t.Fatalf("an untestable hypothesis was accepted: %v", err)
	}
}

// TestInconsistencyOutweighsConsistency.
//
// Evidence consistent with a hypothesis is usually consistent with
// several; evidence inconsistent with one eliminates it. A symmetric
// scale would let three weak agreements outvote one refutation.
func TestInconsistencyOutweighsConsistency(t *testing.T) {
	if StronglyInconsistent.Score() >= 0 || Inconsistent.Score() >= 0 {
		t.Fatal("inconsistency does not score negatively")
	}
	if -Inconsistent.Score() <= Consistent.Score() {
		t.Fatal("one INCONSISTENT does not outweigh one CONSISTENT")
	}
	if -StronglyInconsistent.Score() <= 3*StronglyConsistent.Score() {
		t.Fatal("a strong refutation is outvoted by three strong agreements")
	}
	if NeutralC.Score() != 0 {
		t.Fatal("NEUTRAL carries weight")
	}
}

// TestEliminationIsNotALowScore. They are different statements and a
// caller must be able to tell them apart.
func TestEliminationIsNotALowScore(t *testing.T) {
	m := matrix(t)
	m.AddObservation(obs("O1"))
	m.AddObservation(obs("O2"))

	m.Set("H1", "O1", StronglyInconsistent)
	m.Set("H1", "O2", StronglyConsistent)
	m.Set("H2", "O1", NeutralC)
	m.Set("H2", "O2", NeutralC)
	m.Set("H3", "O1", NeutralC)
	m.Set("H3", "O2", NeutralC)

	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	var h1 Standing
	for _, s := range a.Standings {
		if s.Hypothesis.ID == "H1" {
			h1 = s
		}
	}
	if !h1.Eliminated {
		t.Fatal("a strongly inconsistent observation did not eliminate")
	}
	if len(h1.EliminatedBy) == 0 {
		t.Fatal("the elimination does not name its cause")
	}
	// And an eliminated hypothesis never leads, whatever its score.
	if a.Standings[0].Hypothesis.ID == "H1" {
		t.Fatal("an eliminated hypothesis ranks first")
	}
}

// TestAZeroWeightObservationCannotEliminate.
//
// This is the rule that stops an accurate measurement on an
// incompatible basis from deciding a case.
func TestAZeroWeightObservationCannotEliminate(t *testing.T) {
	m := matrix(t)
	incompatible := obs("O1")
	incompatible.MeasurementCompatible = false
	m.AddObservation(incompatible)
	m.AddObservation(obs("O2"))

	m.Set("H1", "O1", StronglyInconsistent)
	m.Set("H2", "O1", NeutralC)
	m.Set("H3", "O1", NeutralC)
	m.Set("H1", "O2", NeutralC)
	m.Set("H2", "O2", NeutralC)
	m.Set("H3", "O2", NeutralC)

	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range a.Standings {
		if s.Hypothesis.ID == "H1" && s.Eliminated {
			t.Fatal("AN OBSERVATION ON AN INCOMPATIBLE MEASUREMENT BASIS ELIMINATED A HYPOTHESIS")
		}
	}
	// It is still recorded as an inconsistency, so the reader sees it.
	for _, s := range a.Standings {
		if s.Hypothesis.ID == "H1" && len(s.Inconsistencies) == 0 {
			t.Fatal("the inconsistency was hidden rather than discounted")
		}
	}
	if incompatible.Weight() != 0 {
		t.Fatalf("an incompatible observation has weight %v", incompatible.Weight())
	}
}

// TestWeightMultipliesSoAZeroFactorSilencesTheObservation.
func TestWeightMultipliesSoAZeroFactorSilencesTheObservation(t *testing.T) {
	o := obs("O1", 1.0, 0.0, 1.0) // no independence
	if o.Weight() != 0 {
		t.Fatalf("an observation with zero independence has weight %v", o.Weight())
	}
	stale := obs("O2")
	stale.TemporalFit = false
	if stale.Weight() != 0 {
		t.Fatalf("an observation outside its temporal fit has weight %v", stale.Weight())
	}
}

// TestANearTieIsReportedAsANearTie. A ranking that always names a
// leader reports 51/49 the same way as 99/1.
func TestANearTieIsReportedAsANearTie(t *testing.T) {
	m := matrix(t)
	m.AddObservation(obs("O1"))
	m.Set("H1", "O1", StronglyConsistent)
	m.Set("H2", "O1", Consistent)
	m.Set("H3", "O1", NeutralC)

	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	if !a.Indistinguishable {
		t.Fatalf("a margin of %.2f was reported as a decision", a.Margin)
	}
	if _, ok := a.Leader(); ok {
		t.Fatal("Leader() returned a winner over an undecided set")
	}
	if !strings.Contains(a.Report(), "NOT SEPARATED") {
		t.Fatalf("the report does not disclose the near-tie:\n%s", a.Report())
	}
}

// TestAClearLeadIsReportedAsOne, or the rule refuses everything and is
// useless.
func TestAClearLeadIsReportedAsOne(t *testing.T) {
	m := matrix(t)
	for _, id := range []string{"O1", "O2", "O3"} {
		m.AddObservation(obs(id))
		m.Set("H1", id, StronglyConsistent)
		m.Set("H2", id, Inconsistent)
		m.Set("H3", id, Inconsistent)
	}
	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	leader, ok := a.Leader()
	if !ok {
		t.Fatalf("a clear lead was reported as undecided (margin %.2f)", a.Margin)
	}
	if leader.Hypothesis.ID != "H1" {
		t.Fatalf("the leader is %s", leader.Hypothesis.ID)
	}
}

// TestDiagnosticityIsRelativeToTheSet.
//
// An observation every hypothesis scores alike settles nothing, and an
// acquisition plan must not spend on more of them.
func TestDiagnosticityIsRelativeToTheSet(t *testing.T) {
	m := matrix(t)
	m.AddObservation(obs("O-shared"))
	m.AddObservation(obs("O-discriminating"))

	for _, h := range []string{"H1", "H2", "H3"} {
		m.Set(h, "O-shared", Consistent) // every hypothesis predicts it
	}
	m.Set("H1", "O-discriminating", StronglyConsistent)
	m.Set("H2", "O-discriminating", Inconsistent)
	m.Set("H3", "O-discriminating", NeutralC)

	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Diagnostic) != 1 || a.Diagnostic[0] != "O-discriminating" {
		t.Fatalf("Diagnostic = %v", a.Diagnostic)
	}
	if len(a.NonDiagnostic) != 1 || a.NonDiagnostic[0] != "O-shared" {
		t.Fatalf("NonDiagnostic = %v", a.NonDiagnostic)
	}
	if !strings.Contains(a.Report(), "settles nothing") {
		t.Fatalf("the report does not warn about non-diagnostic evidence:\n%s", a.Report())
	}
}

// TestUnassessedCellsAreReported.
//
// A leading hypothesis with many unassessed observations is leading
// because it was not tested, and the reader has to be able to see that.
func TestUnassessedCellsAreReported(t *testing.T) {
	m := matrix(t)
	m.AddObservation(obs("O1"))
	m.AddObservation(obs("O2"))
	m.Set("H1", "O1", StronglyConsistent)
	// H1 vs O2, and all of H2 and H3, are left unassessed.

	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range a.Standings {
		total += len(s.Unassessed)
	}
	if total != 5 {
		t.Fatalf("%d unassessed cells reported, want 5", total)
	}
	if !strings.Contains(a.Report(), "NOT ASSESSED against") {
		t.Fatalf("the report hides the unassessed cells:\n%s", a.Report())
	}
}

// TestExpectedButAbsentObservationsAreReported. The counterfactual's
// output: H predicts X, X is not in the record.
func TestExpectedButAbsentObservationsAreReported(t *testing.T) {
	m := matrix(t)
	m.AddObservation(Observation{ID: "density mismatch", Detail: "density mismatch",
		Reliability: 1, Independence: 1, Freshness: 1,
		TemporalFit: true, MeasurementCompatible: true, EvidenceRefs: []string{"ev:d"}})
	for _, h := range []string{"H1", "H2", "H3"} {
		m.Set(h, "density mismatch", NeutralC)
	}
	a, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range a.Standings {
		switch s.Hypothesis.ID {
		case "H2":
			// H2 expects a density mismatch, which IS present.
			for _, miss := range s.Missing {
				if miss == "density mismatch" {
					t.Fatal("an observation that is present was reported missing")
				}
			}
		case "H1":
			if len(s.Missing) != 3 {
				t.Fatalf("H1 expects three things, none present; Missing = %v", s.Missing)
			}
		}
	}
}

// TestAssessmentIsDeterministic.
func TestAssessmentIsDeterministic(t *testing.T) {
	m := matrix(t)
	for _, id := range []string{"O1", "O2", "O3"} {
		m.AddObservation(obs(id))
		m.Set("H1", id, Consistent)
		m.Set("H2", id, NeutralC)
		m.Set("H3", id, Inconsistent)
	}
	first, err := m.Assess()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := m.Assess()
		if err != nil {
			t.Fatal(err)
		}
		if got.Report() != first.Report() {
			t.Fatal("the assessment varies between runs")
		}
	}
}

// TestAnUnversionedMatrixIsRefused.
func TestAnUnversionedMatrixIsRefused(t *testing.T) {
	if _, err := NewMatrix("t-acme", "case-1", cargoHypotheses(), contract.VersionSet{}); !errors.Is(err, contract.ErrUnversioned) {
		t.Fatalf("an unversioned matrix was built: %v", err)
	}
}

// TestAnObservationMustCiteEvidence.
func TestAnObservationMustCiteEvidence(t *testing.T) {
	o := Observation{ID: "O1", Reliability: 1, Independence: 1, Freshness: 1}
	if err := o.Validate(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("an observation citing nothing was accepted: %v", err)
	}
}

// TestAssessingWithNoEvidenceIsRefused.
func TestAssessingWithNoEvidenceIsRefused(t *testing.T) {
	m := matrix(t)
	if _, err := m.Assess(); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("an empty matrix was assessed: %v", err)
	}
}

// TestTheZeroConsistencyIsNotNeutral. NEUTRAL is a judgement that the
// observation does not bear on the hypothesis; the zero value means
// nobody judged.
func TestTheZeroConsistencyIsNotNeutral(t *testing.T) {
	var c Consistency
	if c.Assessed() {
		t.Fatal("the zero consistency reports as assessed")
	}
	if c == NeutralC {
		t.Fatal("the zero value IS neutral; unassessed cells would count as judgements")
	}
	if c.String() != "NOT_ASSESSED" {
		t.Fatalf("the zero consistency renders as %q", c.String())
	}
}
