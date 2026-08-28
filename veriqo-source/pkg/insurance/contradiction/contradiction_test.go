package contradiction

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
)

// mustEvidence builds a distinct, valid evidence.Record. subject/source
// are varied by callers to guarantee distinct content-addressed
// EvidenceIDs (ontology.Evidence is content-addressed — two calls with
// identical fields would collide onto the same ID).
func mustEvidence(t *testing.T, subject, source string, observedAt uint64, origin evidence.Origin, interest evidence.SourceInterest) evidence.Record {
	t.Helper()
	ev, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     "claim_fact",
		Source:     source,
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	rec, err := evidence.New("CASE-1", ev, party.PartyID("PTY-"+source), origin)
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	rec.SourceInterest = interest
	return rec
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDetectMatchesBlueprintWorkedExample reproduces §25's own worked
// JSON example almost literally: a document (POD) says one delivery
// time, a survey says a delivery time 4 hours later, and the engine
// must report a HIGH-severity, UNRESOLVED, human-review-required
// contradiction between the two EvidenceIDs.
func TestDetectMatchesBlueprintWorkedExample(t *testing.T) {
	e := NewEngine()

	pod := mustEvidence(t, "delivery", "pod-doc", 100, evidence.OriginCarrier, evidence.SourceInterest{Interest: evidence.InterestCarrier})
	survey := mustEvidence(t, "delivery", "survey-doc", 100, evidence.OriginSurveyor, evidence.SourceInterest{Interest: evidence.InterestIndependent})

	claimKey := "CASE-1|delivery_time"
	must(t, e.Submit(pod, claimKey, "10:00", 1.0, 100))
	must(t, e.Submit(survey, claimKey, "14:00", 1.0, 100))

	records, err := e.Detect(claimKey, PairDocumentSurvey, "Delivery time differs by 4 hours", 100, 1000)
	must(t, err)
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 contradiction record, got %d", len(records))
	}
	rec := records[0]

	if !strings.HasPrefix(rec.ContradictionID, "CX-") {
		t.Fatalf("expected contradiction_id to start with CX-, got %q", rec.ContradictionID)
	}
	if rec.Severity != SeverityHigh {
		t.Fatalf("expected severity HIGH (tied equal-reliability sources), got %s", rec.Severity)
	}
	if rec.Resolution != ResolutionUnresolved {
		t.Fatalf("expected resolution UNRESOLVED (matching blueprint's own example), got %s", rec.Resolution)
	}
	if !rec.HumanReviewRequired {
		t.Fatal("expected human_review_required=true")
	}
	if rec.Conflict != "Delivery time differs by 4 hours" {
		t.Fatalf("expected conflict text to match verbatim, got %q", rec.Conflict)
	}
	if rec.Category != PairDocumentSurvey {
		t.Fatalf("expected category DOCUMENT_SURVEY, got %s", rec.Category)
	}
	ids := map[string]bool{pod.EvidenceID(): true, survey.EvidenceID(): true}
	if !ids[rec.EvidenceA] || !ids[rec.EvidenceB] || rec.EvidenceA == rec.EvidenceB {
		t.Fatalf("expected evidence_a/evidence_b to be the two distinct submitted EvidenceIDs, got %q / %q", rec.EvidenceA, rec.EvidenceB)
	}
	if rec.EvidenceASourceInterest == nil || rec.EvidenceBSourceInterest == nil {
		t.Fatal("expected both sides' SourceInterest to be surfaced as context")
	}
}

// TestDetectReturnsEmptyWhenSourcesAgree proves agreement is not
// reported as a contradiction at all.
func TestDetectReturnsEmptyWhenSourcesAgree(t *testing.T) {
	e := NewEngine()
	a := mustEvidence(t, "seal", "src-a", 1, evidence.OriginClaimant, evidence.SourceInterest{Interest: evidence.InterestClaimant})
	b := mustEvidence(t, "seal", "src-b", 1, evidence.OriginInsurer, evidence.SourceInterest{Interest: evidence.InterestInsurer})
	claimKey := "CASE-2|seal_status"
	must(t, e.Submit(a, claimKey, "INTACT", 0.9, 1))
	must(t, e.Submit(b, claimKey, "INTACT", 0.9, 1))

	records, err := e.Detect(claimKey, PairClaimantInsurer, "", 1, 100)
	must(t, err)
	if records != nil {
		t.Fatalf("expected nil (no contradiction) when sources agree, got %v", records)
	}
}

// TestDetectFailsClosedWithNoObservations is the API-boundary form of
// the golden rule: insufficient evidence must fail closed, never guess.
func TestDetectFailsClosedWithNoObservations(t *testing.T) {
	e := NewEngine()
	_, err := e.Detect("nonexistent-claim", PairDocumentDocument, "", 1, 100)
	if err == nil {
		t.Fatal("expected an error for a claim with no pending observations")
	}
}

func TestDetectRejectsEmptyClaimKey(t *testing.T) {
	e := NewEngine()
	if _, err := e.Detect("", PairDocumentDocument, "", 1, 100); !errors.Is(err, ErrEmptyClaimKey) {
		t.Fatalf("expected ErrEmptyClaimKey, got %v", err)
	}
}

func TestDetectRejectsUnknownPairKind(t *testing.T) {
	e := NewEngine()
	if _, err := e.Detect("K", PairKind("NOT_A_REAL_KIND"), "", 1, 100); !errors.Is(err, ErrUnknownPairKind) {
		t.Fatalf("expected ErrUnknownPairKind, got %v", err)
	}
}

func TestSubmitRejectsEmptyEvidenceID(t *testing.T) {
	e := NewEngine()
	if err := e.Submit(evidence.Record{}, "K", "V", 1.0, 1); !errors.Is(err, ErrEmptyEvidenceID) {
		t.Fatalf("expected ErrEmptyEvidenceID, got %v", err)
	}
}

func TestSubmitRejectsEmptyClaimKey(t *testing.T) {
	e := NewEngine()
	rec := mustEvidence(t, "s", "src", 1, evidence.OriginClaimant, evidence.SourceInterest{})
	if err := e.Submit(rec, "", "V", 1.0, 1); !errors.Is(err, ErrEmptyClaimKey) {
		t.Fatalf("expected ErrEmptyClaimKey, got %v", err)
	}
}

func TestSubmitRejectsEmptyValue(t *testing.T) {
	e := NewEngine()
	rec := mustEvidence(t, "s", "src", 1, evidence.OriginClaimant, evidence.SourceInterest{})
	if err := e.Submit(rec, "K", "", 1.0, 1); !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("expected ErrEmptyValue, got %v", err)
	}
}

func TestSubmitRejectsInvalidReliability(t *testing.T) {
	e := NewEngine()
	rec := mustEvidence(t, "s", "src", 1, evidence.OriginClaimant, evidence.SourceInterest{})
	if err := e.Submit(rec, "K", "V", 0, 1); !errors.Is(err, ErrInvalidReliability) {
		t.Fatalf("expected ErrInvalidReliability for 0, got %v", err)
	}
	if err := e.Submit(rec, "K", "V", 1.5, 1); !errors.Is(err, ErrInvalidReliability) {
		t.Fatalf("expected ErrInvalidReliability for >1, got %v", err)
	}
}

// TestSeverityLowForDecisiveMajority: a decisive, high-confidence
// arbitrated winner (>= 0.85 confidence) must be LOW severity, not
// HIGH — severity must track the real engine's own confidence, not
// merely "a conflict existed".
func TestSeverityLowForDecisiveMajority(t *testing.T) {
	e := NewEngine()
	claimKey := "CASE-3|flag_state"
	agree := []string{"w", "x", "y", "z"}
	for i, name := range agree {
		rec := mustEvidence(t, "flag", name, uint64(i), evidence.OriginIndependent, evidence.SourceInterest{Interest: evidence.InterestIndependent})
		must(t, e.Submit(rec, claimKey, "PANAMA", 1.0, 1))
	}
	dissent := mustEvidence(t, "flag", "stale-scrape", 99, evidence.OriginIndependent, evidence.SourceInterest{Interest: evidence.InterestIndependent})
	must(t, e.Submit(dissent, claimKey, "LIBERIA", 0.1, 1))

	records, err := e.Detect(claimKey, PairDocumentDocument, "", 1, 1000)
	must(t, err)
	if len(records) == 0 {
		t.Fatal("expected at least one contradiction record")
	}
	for _, rec := range records {
		if rec.Severity != SeverityLow {
			t.Fatalf("expected LOW severity for a decisive majority, got %s", rec.Severity)
		}
		if rec.Resolution != ResolutionArbitrated {
			t.Fatalf("expected ARBITRATED resolution, got %s", rec.Resolution)
		}
		if rec.HumanReviewRequired {
			t.Fatal("a decisive, confident arbitration should not require human review")
		}
	}
}

// TestSeverityMediumForNarrowArbitratedMajority: a winner that clears
// the confident-arbitration bar (exactly 2/3) but not decisively
// (< 0.85) must be MEDIUM, distinguishing it from both a tied/contested
// HIGH case and a decisive LOW case.
func TestSeverityMediumForNarrowArbitratedMajority(t *testing.T) {
	e := NewEngine()
	claimKey := "CASE-4|flag_state"
	a := mustEvidence(t, "flag", "src-a", 1, evidence.OriginClaimant, evidence.SourceInterest{Interest: evidence.InterestClaimant})
	b := mustEvidence(t, "flag", "src-b", 1, evidence.OriginClaimant, evidence.SourceInterest{Interest: evidence.InterestClaimant})
	c := mustEvidence(t, "flag", "src-c", 1, evidence.OriginInsurer, evidence.SourceInterest{Interest: evidence.InterestInsurer})
	must(t, e.Submit(a, claimKey, "PANAMA", 1.0, 1))
	must(t, e.Submit(b, claimKey, "PANAMA", 1.0, 1))
	must(t, e.Submit(c, claimKey, "LIBERIA", 1.0, 1))

	records, err := e.Detect(claimKey, PairDocumentDocument, "", 1, 1000)
	must(t, err)
	if len(records) == 0 {
		t.Fatal("expected at least one contradiction record")
	}
	for _, rec := range records {
		if rec.Severity != SeverityMedium {
			t.Fatalf("expected MEDIUM severity for a narrow (2/3) arbitrated majority, got %s", rec.Severity)
		}
		if rec.Resolution != ResolutionArbitrated {
			t.Fatalf("expected ARBITRATED resolution, got %s", rec.Resolution)
		}
	}
}

func TestKnownPairKindsHasAllTenBlueprintPairs(t *testing.T) {
	kinds := KnownPairKinds()
	if len(kinds) != 10 {
		t.Fatalf("expected exactly the 10 pair kinds §25 names, got %d: %v", len(kinds), kinds)
	}
	for _, k := range kinds {
		if !IsKnownPairKind(k) {
			t.Fatalf("KnownPairKinds returned a kind IsKnownPairKind rejects: %s", k)
		}
	}
}

// TestDetectIsDeterministic proves replay-determinism: two
// independently built engines fed the identical input, in the same
// order, produce byte-identical output.
func TestDetectIsDeterministic(t *testing.T) {
	build := func(t *testing.T) []ContradictionRecord {
		e := NewEngine()
		a := mustEvidence(t, "delivery", "pod-doc", 100, evidence.OriginCarrier, evidence.SourceInterest{Interest: evidence.InterestCarrier})
		b := mustEvidence(t, "delivery", "survey-doc", 100, evidence.OriginSurveyor, evidence.SourceInterest{Interest: evidence.InterestIndependent})
		claimKey := "CASE-1|delivery_time"
		must(t, e.Submit(a, claimKey, "10:00", 1.0, 100))
		must(t, e.Submit(b, claimKey, "14:00", 1.0, 100))
		records, err := e.Detect(claimKey, PairDocumentSurvey, "Delivery time differs by 4 hours", 100, 1000)
		must(t, err)
		return records
	}
	r1 := build(t)
	r2 := build(t)
	if len(r1) != 1 || len(r2) != 1 {
		t.Fatalf("expected 1 record each, got %d and %d", len(r1), len(r2))
	}
	// Struct equality (!=) would compare the SourceInterest pointer
	// fields by address, not by value; use DeepEqual to compare content.
	if !reflect.DeepEqual(r1[0], r2[0]) {
		t.Fatalf("expected byte-identical replay, got %+v vs %+v", r1[0], r2[0])
	}
}

// TestSubmitAndDetectConcurrentRace exercises Submit/Detect from many
// goroutines concurrently (run with -race) — blueprint §35's "Race
// tests" requirement.
func TestSubmitAndDetectConcurrentRace(t *testing.T) {
	e := NewEngine()
	done := make(chan struct{})
	const n = 20
	for i := 0; i < n; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			rec := mustEvidence(t, "race", "race-src", uint64(i), evidence.OriginClaimant, evidence.SourceInterest{Interest: evidence.InterestClaimant})
			claimKey := "CASE-RACE|field"
			value := "A"
			if i%2 == 0 {
				value = "B"
			}
			_ = e.Submit(rec, claimKey, value, 1.0, uint64(i))
			_, _ = e.Detect(claimKey, PairDocumentDocument, "", uint64(i), 1000)
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}
