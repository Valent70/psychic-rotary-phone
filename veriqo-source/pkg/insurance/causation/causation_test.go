package causation

import (
	"errors"
	"sync"
	"testing"

	"veriqo/pkg/insurance/evidence"
)

// ---- Hypothesis / NewHypothesis ----

func TestNewHypothesisValidates(t *testing.T) {
	if _, err := NewHypothesis("", "desc"); err != ErrEmptyHypothesisID {
		t.Fatalf("expected ErrEmptyHypothesisID, got %v", err)
	}
	if _, err := NewHypothesis("H1", ""); err != ErrEmptyDescription {
		t.Fatalf("expected ErrEmptyDescription, got %v", err)
	}
	h, err := NewHypothesis("H1", "pre-existing damage")
	if err != nil {
		t.Fatalf("NewHypothesis: %v", err)
	}
	if h.Status != StatusUnproven {
		t.Fatalf("expected fresh hypothesis to start UNPROVEN, got %s", h.Status)
	}
}

func TestIsKnownStatus(t *testing.T) {
	for _, s := range []Status{StatusSupported, StatusPartiallySupported, StatusContradicted, StatusUnproven, StatusInsufficientEvidence} {
		if !IsKnownStatus(s) {
			t.Fatalf("expected %s to be known", s)
		}
	}
	if IsKnownStatus("BOGUS") {
		t.Fatal("expected BOGUS to be unknown")
	}
}

// ---- DefaultCargoDamageHypotheses: the named H1-H8 worked example ----

// TestH1ThroughH8WorkedExample reproduces the blueprint's §16 worked
// example verbatim (H1 = pre-existing damage ... H8 = unknown), wires
// up evidence the way a real cargo-damage case would, and verifies the
// full pipeline: Add -> evidence -> Status derivation -> Dependency ->
// Explain, ending in the blueprint's own §17 output shape.
func TestH1ThroughH8WorkedExample(t *testing.T) {
	hyps := DefaultCargoDamageHypotheses()
	if len(hyps) != 8 {
		t.Fatalf("expected 8 hypotheses, got %d", len(hyps))
	}
	wantIDs := []HypothesisID{"H1", "H2", "H3", "H4", "H5", "H6", "H7", "H8"}
	for i, h := range hyps {
		if h.ID != wantIDs[i] {
			t.Fatalf("hypothesis %d: expected ID %s, got %s", i, wantIDs[i], h.ID)
		}
		if h.Description == "" {
			t.Fatalf("hypothesis %s: expected a non-empty description", h.ID)
		}
		if h.Status != StatusUnproven {
			t.Fatalf("hypothesis %s: expected fresh Status UNPROVEN, got %s", h.ID, h.Status)
		}
	}

	hs, err := NewHypothesisSet("CASE-001", "CLM-001", "What caused the cargo damage discovered at delivery?")
	if err != nil {
		t.Fatalf("NewHypothesisSet: %v", err)
	}
	for _, h := range hyps {
		if err := hs.Add(h); err != nil {
			t.Fatalf("Add(%s): %v", h.ID, err)
		}
	}
	if hs.Count() != 8 {
		t.Fatalf("expected 8 hypotheses in set, got %d", hs.Count())
	}

	// H3 (carrier handling) is the strongly-evidenced hypothesis: five
	// independent supporting items, nothing contradicting it, but ONE
	// missing-evidence gap on record (the blueprint's own §17 example).
	for i, evID := range []string{"EV-101", "EV-102", "EV-103", "EV-104", "EV-105"} {
		if err := hs.AddSupportingEvidence("H3", evID); err != nil {
			t.Fatalf("AddSupportingEvidence H3 #%d: %v", i, err)
		}
	}
	if err := hs.AddMissingEvidence("H3", "the custody record between discharge and warehouse receipt is incomplete"); err != nil {
		t.Fatalf("AddMissingEvidence H3: %v", err)
	}

	// The alternatives are weaker: H1 has one contradicting item, H2
	// has one supporting item, the rest remain UNPROVEN.
	if err := hs.AddContradictingEvidence("H1", "EV-201"); err != nil {
		t.Fatalf("AddContradictingEvidence H1: %v", err)
	}
	if err := hs.AddSupportingEvidence("H2", "EV-301"); err != nil {
		t.Fatalf("AddSupportingEvidence H2: %v", err)
	}

	// H7 (post-delivery storage) depends on H2 (loading damage) — both
	// need the same custody-interval evidence, the blueprint's own §16
	// dependency example.
	if err := hs.AddDependency("H7", "H2"); err != nil {
		t.Fatalf("AddDependency H7->H2: %v", err)
	}
	h7, _ := hs.Get("H7")
	if len(h7.Dependency) != 1 || h7.Dependency[0] != "H2" {
		t.Fatalf("expected H7 to depend on H2, got %v", h7.Dependency)
	}

	// Golden rule check: H3 has overwhelming supporting evidence (5
	// items) but ONE missing-evidence gap, so it must NEVER be reported
	// SUPPORTED outright.
	h3, ok := hs.Get("H3")
	if !ok {
		t.Fatal("expected H3 to be present")
	}
	if h3.Status != StatusPartiallySupported {
		t.Fatalf("expected H3 to be PARTIALLY_SUPPORTED despite 5 supporting items, because of the recorded evidence gap; got %s", h3.Status)
	}

	h1, _ := hs.Get("H1")
	if h1.Status != StatusContradicted {
		t.Fatalf("expected H1 (sc=0, cc=1) to be CONTRADICTED, got %s", h1.Status)
	}
	h2, _ := hs.Get("H2")
	if h2.Status != StatusSupported {
		t.Fatalf("expected H2 (sc=1, cc=0, mc=0) to be SUPPORTED, got %s", h2.Status)
	}
	h4, _ := hs.Get("H4")
	if h4.Status != StatusUnproven {
		t.Fatalf("expected untouched H4 to remain UNPROVEN, got %s", h4.Status)
	}

	// Explain must reproduce the blueprint's §17 output almost
	// verbatim: H3 leads, and the caveat is the custody-record gap.
	exp, err := Explain(hs, nil)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.LeadingHypothesis != "H3" {
		t.Fatalf("expected H3 to lead (5 supporting vs at most 1 elsewhere), got %s", exp.LeadingHypothesis)
	}
	wantNarrative := "Evidence currently supports Hypothesis H3 more strongly than alternative hypotheses; however, causation remains unresolved because the custody record between discharge and warehouse receipt is incomplete."
	if exp.Narrative != wantNarrative {
		t.Fatalf("narrative mismatch:\n got:  %s\n want: %s", exp.Narrative, wantNarrative)
	}
}

// ---- computeStatus / DeriveStatus: table-driven, every branch named ----

func TestDeriveStatus(t *testing.T) {
	cases := []struct {
		name    string
		support []string
		contra  []string
		missing []string
		want    Status
	}{
		{"fresh hypothesis, nothing recorded", nil, nil, nil, StatusUnproven},
		{"only a gap identified, no evidence either way", nil, nil, []string{"raw logger data"}, StatusInsufficientEvidence},
		{"pure contradiction", nil, []string{"EV-1"}, nil, StatusContradicted},
		{"contradiction outweighs support", []string{"EV-1"}, []string{"EV-2", "EV-3"}, nil, StatusContradicted},
		{"clean support, no gaps, no contradiction", []string{"EV-1", "EV-2"}, nil, nil, StatusSupported},
		{"support equals contradiction", []string{"EV-1"}, []string{"EV-2"}, nil, StatusPartiallySupported},
		{"strong support but a recorded gap", []string{"EV-1", "EV-2", "EV-3"}, nil, []string{"pre-trip inspection"}, StatusPartiallySupported},
		{"support with minor contradiction", []string{"EV-1", "EV-2"}, []string{"EV-3"}, nil, StatusPartiallySupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Hypothesis{ID: "H1", Description: "x", SupportingEvidence: c.support, ContradictingEvidence: c.contra, MissingEvidence: c.missing}
			got := DeriveStatus(h, nil)
			if got != c.want {
				t.Fatalf("DeriveStatus: got %s, want %s", got, c.want)
			}
		})
	}
}

// TestDeriveStatusNeverGuessesWhenInsufficient is the golden-rule test
// (§36): a hypothesis with a known gap and no evidence either way must
// be INSUFFICIENT_EVIDENCE, never SUPPORTED or CONTRADICTED.
func TestDeriveStatusNeverGuessesWhenInsufficient(t *testing.T) {
	h := Hypothesis{ID: "H5", Description: "container failure", MissingEvidence: []string{"container inspection certificate"}}
	got := DeriveStatus(h, nil)
	if got != StatusInsufficientEvidence {
		t.Fatalf("expected INSUFFICIENT_EVIDENCE, got %s", got)
	}
}

// TestDeriveStatusNeverPromotesPartialToSupported is the golden-rule
// test this task calls out explicitly: overwhelming supporting evidence
// must never be reported as SUPPORTED while a MissingEvidence entry is
// on record.
func TestDeriveStatusNeverPromotesPartialToSupported(t *testing.T) {
	h := Hypothesis{
		ID:                 "H3",
		Description:        "carrier handling",
		SupportingEvidence: []string{"EV-1", "EV-2", "EV-3", "EV-4", "EV-5", "EV-6", "EV-7", "EV-8"},
		MissingEvidence:    []string{"custody record between discharge and warehouse receipt is incomplete"},
	}
	got := DeriveStatus(h, nil)
	if got == StatusSupported {
		t.Fatal("golden rule violated: a hypothesis with a recorded evidence gap must never be SUPPORTED outright")
	}
	if got != StatusPartiallySupported {
		t.Fatalf("expected PARTIALLY_SUPPORTED, got %s", got)
	}
}

// TestDeriveStatusIndependenceAdjusted reproduces the blueprint's own
// §10 worked example (Photo A -> Statement A -> Survey A) as supporting
// evidence for one hypothesis, and shows that with a DependencyGraph
// supplied, three dependent items collapse to ONE independent
// supporting item — changing the derived Status versus naive len()
// counting. This is the "never double-count dependent evidence" golden
// rule (§36) applied to causation status derivation.
func TestDeriveStatusIndependenceAdjusted(t *testing.T) {
	dg := evidence.NewDependencyGraph()
	if err := dg.AddDependency("StatementA", "PhotoA"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := dg.AddDependency("SurveyA", "StatementA"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	h := Hypothesis{
		ID:                    "H3",
		Description:           "carrier handling",
		SupportingEvidence:    []string{"PhotoA", "StatementA", "SurveyA"},
		ContradictingEvidence: []string{"EV-Contra"},
	}

	// Naive counting: sc=3, cc=1 -> cc <= sc, cc > 0 -> PARTIALLY_SUPPORTED.
	naive := DeriveStatus(h, nil)
	if naive != StatusPartiallySupported {
		t.Fatalf("naive: expected PARTIALLY_SUPPORTED, got %s", naive)
	}

	// Independence-adjusted: sc collapses to 1 independent root
	// (PhotoA), cc=1 -> cc == sc, cc > 0 -> still PARTIALLY_SUPPORTED,
	// but for a DIFFERENT reason (equal, not merely outnumbering) —
	// verify the independent count itself, which is the golden-rule
	// mechanism under test.
	adjusted := DeriveStatus(h, dg)
	if adjusted != StatusPartiallySupported {
		t.Fatalf("adjusted: expected PARTIALLY_SUPPORTED, got %s", adjusted)
	}
	if got := NetSupport(h, dg); got != 0 {
		t.Fatalf("expected independence-adjusted NetSupport 1-1=0, got %d", got)
	}
	if got := NetSupport(h, nil); got != 2 {
		t.Fatalf("expected naive NetSupport 3-1=2, got %d", got)
	}
}

// ---- HypothesisSet: construction and Add ----

func TestNewHypothesisSetValidates(t *testing.T) {
	if _, err := NewHypothesisSet("", "CLM-1", "why?"); err != ErrEmptyCaseID {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
	if _, err := NewHypothesisSet("CASE-1", "CLM-1", ""); err != ErrEmptyQuestion {
		t.Fatalf("expected ErrEmptyQuestion, got %v", err)
	}
	hs, err := NewHypothesisSet("CASE-1", "", "why?")
	if err != nil {
		t.Fatalf("NewHypothesisSet: %v", err)
	}
	if hs.ClaimID != "" {
		t.Fatal("expected empty ClaimID to be accepted")
	}
}

func TestHypothesisSetAddRejectsDuplicateAndInvalid(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h1, _ := NewHypothesis("H1", "pre-existing damage")
	if err := hs.Add(h1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := hs.Add(h1); err == nil {
		t.Fatal("expected duplicate Add to fail")
	}
	if err := hs.Add(Hypothesis{ID: "", Description: "x"}); err != ErrEmptyHypothesisID {
		t.Fatalf("expected ErrEmptyHypothesisID, got %v", err)
	}
	if err := hs.Add(Hypothesis{ID: "H2", Description: ""}); err != ErrEmptyDescription {
		t.Fatalf("expected ErrEmptyDescription, got %v", err)
	}
	// A caller-supplied Status -- valid-looking (StatusSupported) or
	// outright bogus -- is never trusted: Add always forces
	// StatusUnproven, exactly like manifest.RegisterDraft forcing
	// State=DRAFT and evidence.Submit resetting Status to
	// StatusUnverified. This is INV-001 ("no caller may directly
	// construct an authoritative state") applied to HypothesisSet.
	if err := hs.Add(Hypothesis{ID: "H3", Description: "x", Status: "BOGUS"}); err != nil {
		t.Fatalf("Add with a bogus caller-supplied Status must still succeed (the value is discarded, not validated): %v", err)
	}
	got, _ := hs.Get("H3")
	if got.Status != StatusUnproven {
		t.Fatalf("expected Add to force Status=StatusUnproven regardless of the caller-supplied value, got %q", got.Status)
	}
}

// TestHypothesisSetAddNeverTrustsACallerAssertedSupportedStatus is the
// direct adversarial version of the above: a caller hands Add a
// Hypothesis that already claims StatusSupported, with SupportingEvidence
// pre-populated to make the claim look plausible -- exactly the shape of
// exploit the Final Authority Hardening Round proved against
// evidence.Registry.Submit (a hand-built Record{Status:
// StatusCorroborated} bypassing every downstream authority gate). Add
// must refuse to trust it: the stored Hypothesis reads StatusUnproven
// until AddSupportingEvidence/AddContradictingEvidence/RecomputeStatuses
// actually derives a status from real, recorded evidence.
func TestHypothesisSetAddNeverTrustsACallerAssertedSupportedStatus(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	forged := Hypothesis{
		ID: "H1", Description: "pre-existing damage",
		Status:             StatusSupported,
		SupportingEvidence: []string{"EV-forged-1", "EV-forged-2"},
	}
	if err := hs.Add(forged); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := hs.Get("H1")
	if !ok {
		t.Fatal("expected H1 to be present")
	}
	if got.Status != StatusUnproven {
		t.Fatalf("expected a caller-asserted StatusSupported to be discarded on Add (got %q) -- Status must only ever be reached via computeStatus", got.Status)
	}
}

func TestHypothesisSetAllIsInsertionOrder(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	for _, id := range []HypothesisID{"H8", "H1", "H4"} {
		h, _ := NewHypothesis(id, "desc")
		if err := hs.Add(h); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}
	all := hs.All()
	wantOrder := []HypothesisID{"H8", "H1", "H4"}
	for i, h := range all {
		if h.ID != wantOrder[i] {
			t.Fatalf("position %d: expected %s, got %s", i, wantOrder[i], h.ID)
		}
	}
	// Determinism: repeated calls return the same order.
	all2 := hs.All()
	for i := range all {
		if all[i].ID != all2[i].ID {
			t.Fatal("expected All() to be deterministic across calls")
		}
	}
}

// ---- Evidence mutators ----

func TestAddSupportingEvidenceRequiresKnownHypothesis(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	if err := hs.AddSupportingEvidence("H99", "EV-1"); err == nil {
		t.Fatal("expected error for unknown hypothesis")
	}
}

func TestAddSupportingEvidenceRejectsEmptyID(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h, _ := NewHypothesis("H1", "desc")
	hs.Add(h)
	if err := hs.AddSupportingEvidence("H1", ""); err != ErrEmptyEvidenceID {
		t.Fatalf("expected ErrEmptyEvidenceID, got %v", err)
	}
}

func TestAddSupportingEvidenceIsIdempotent(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h, _ := NewHypothesis("H1", "desc")
	hs.Add(h)
	if err := hs.AddSupportingEvidence("H1", "EV-1"); err != nil {
		t.Fatalf("AddSupportingEvidence: %v", err)
	}
	if err := hs.AddSupportingEvidence("H1", "EV-1"); err != nil {
		t.Fatalf("AddSupportingEvidence (repeat): %v", err)
	}
	got, _ := hs.Get("H1")
	if len(got.SupportingEvidence) != 1 {
		t.Fatalf("expected exactly 1 supporting item after repeat submission, got %d", len(got.SupportingEvidence))
	}
}

func TestAddContradictingEvidence(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h, _ := NewHypothesis("H1", "desc")
	hs.Add(h)
	if err := hs.AddContradictingEvidence("H1", "EV-2"); err != nil {
		t.Fatalf("AddContradictingEvidence: %v", err)
	}
	got, _ := hs.Get("H1")
	if len(got.ContradictingEvidence) != 1 || got.ContradictingEvidence[0] != "EV-2" {
		t.Fatalf("unexpected ContradictingEvidence: %v", got.ContradictingEvidence)
	}
	if got.Status != StatusContradicted {
		t.Fatalf("expected auto-recomputed CONTRADICTED status, got %s", got.Status)
	}
}

func TestAddMissingEvidenceRejectsEmpty(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h, _ := NewHypothesis("H1", "desc")
	hs.Add(h)
	if err := hs.AddMissingEvidence("H1", ""); err != ErrEmptyMissingEntry {
		t.Fatalf("expected ErrEmptyMissingEntry, got %v", err)
	}
	if err := hs.AddMissingEvidence("H1", "raw logger data"); err != nil {
		t.Fatalf("AddMissingEvidence: %v", err)
	}
	got, _ := hs.Get("H1")
	if got.Status != StatusInsufficientEvidence {
		t.Fatalf("expected auto-recomputed INSUFFICIENT_EVIDENCE, got %s", got.Status)
	}
}

// ---- Dependency ----

func TestAddDependencyRejectsSelfAndUnknown(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h1, _ := NewHypothesis("H1", "desc")
	hs.Add(h1)
	if err := hs.AddDependency("H1", "H1"); err != ErrSelfDependency {
		t.Fatalf("expected ErrSelfDependency, got %v", err)
	}
	if err := hs.AddDependency("H1", "H99"); err == nil {
		t.Fatal("expected error referencing an unknown dependency")
	}
	if err := hs.AddDependency("H99", "H1"); err == nil {
		t.Fatal("expected error for unknown hypothesis id")
	}
}

func TestAddDependencyRejectsCycle(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	for _, id := range []HypothesisID{"H1", "H2", "H3"} {
		h, _ := NewHypothesis(id, "desc")
		hs.Add(h)
	}
	if err := hs.AddDependency("H1", "H2"); err != nil {
		t.Fatalf("AddDependency H1->H2: %v", err)
	}
	if err := hs.AddDependency("H2", "H3"); err != nil {
		t.Fatalf("AddDependency H2->H3: %v", err)
	}
	// H3 -> H1 would close the loop H1 -> H2 -> H3 -> H1.
	if err := hs.AddDependency("H3", "H1"); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}
}

func TestAddDependencyIsIdempotent(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	for _, id := range []HypothesisID{"H7", "H2"} {
		h, _ := NewHypothesis(id, "desc")
		hs.Add(h)
	}
	if err := hs.AddDependency("H7", "H2"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := hs.AddDependency("H7", "H2"); err != nil {
		t.Fatalf("AddDependency (repeat): %v", err)
	}
	got, _ := hs.Get("H7")
	if len(got.Dependency) != 1 {
		t.Fatalf("expected exactly 1 dependency after repeat, got %d", len(got.Dependency))
	}
}

// ---- RecomputeStatuses ----

func TestRecomputeStatusesWithDependencyGraph(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	h, _ := NewHypothesis("H3", "carrier handling")
	hs.Add(h)

	dg := evidence.NewDependencyGraph()
	dg.AddDependency("StatementA", "PhotoA")
	dg.AddDependency("SurveyA", "StatementA")

	// Bypass the auto-recompute-on-add path by constructing evidence
	// lists directly through the mutators, then force an
	// independence-aware recompute.
	hs.AddSupportingEvidence("H3", "PhotoA")
	hs.AddSupportingEvidence("H3", "StatementA")
	hs.AddSupportingEvidence("H3", "SurveyA")

	before, _ := hs.Get("H3")
	if before.Status != StatusSupported {
		t.Fatalf("expected naive SUPPORTED before independence-aware recompute, got %s", before.Status)
	}

	hs.RecomputeStatuses(dg)
	after, _ := hs.Get("H3")
	// Still SUPPORTED (no contradiction, no missing evidence) — the
	// independence adjustment changes the COUNT (3 -> 1), not
	// necessarily the Status in this particular case, but it must not
	// panic or corrupt the record, and NetSupport must reflect it.
	if after.Status != StatusSupported {
		t.Fatalf("expected SUPPORTED after independence-aware recompute, got %s", after.Status)
	}
	if got := NetSupport(after, dg); got != 1 {
		t.Fatalf("expected independence-adjusted NetSupport 1, got %d", got)
	}
	if got := NetSupport(after, nil); got != 3 {
		t.Fatalf("expected naive NetSupport 3, got %d", got)
	}
}

// ---- SortedIDs ----

func TestSortedIDs(t *testing.T) {
	got := SortedIDs([]HypothesisID{"H8", "H1", "H3"})
	want := []HypothesisID{"H1", "H3", "H8"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortedIDs: got %v, want %v", got, want)
		}
	}
}

// ---- Concurrency / race safety ----

// TestHypothesisSetConcurrentAccess exercises HypothesisSet's mutex
// under concurrent evidence submission from multiple goroutines — run
// with `go test -race` to verify no data race, matching the blueprint's
// testing requirements (§35: "Race tests").
func TestHypothesisSetConcurrentAccess(t *testing.T) {
	hs, _ := NewHypothesisSet("CASE-1", "CLM-1", "why?")
	for _, id := range []HypothesisID{"H1", "H2", "H3"} {
		h, _ := NewHypothesis(id, "desc")
		hs.Add(h)
	}

	var wg sync.WaitGroup
	ids := []HypothesisID{"H1", "H2", "H3"}
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := ids[n%len(ids)]
			hs.AddSupportingEvidence(id, "EV-concurrent")
			hs.AddMissingEvidence(id, "some gap")
			_ = hs.All()
			_ = hs.Count()
			hs.RecomputeStatuses(nil)
		}(g)
	}
	wg.Wait()

	if hs.Count() != 3 {
		t.Fatalf("expected 3 hypotheses to survive concurrent access, got %d", hs.Count())
	}
}
