package timeline

import (
	"errors"
	"sync"
	"testing"
)

func TestNewRejectsEmptyEventID(t *testing.T) {
	if _, err := New("", TypeLoading, "", "", 0, []string{"EV-1"}, CertaintyConfirmed, "", ""); !errors.Is(err, ErrEmptyEventID) {
		t.Fatalf("expected ErrEmptyEventID, got %v", err)
	}
}

func TestNewRejectsUnknownEventType(t *testing.T) {
	_, err := New("EVT-1", EventType("NOT_A_TYPE"), "", "", 0, []string{"EV-1"}, CertaintyConfirmed, "", "")
	if !errors.Is(err, ErrUnknownEventType) {
		t.Fatalf("expected ErrUnknownEventType, got %v", err)
	}
}

func TestNewRejectsNoSourceEvidence(t *testing.T) {
	_, err := New("EVT-1", TypeLoading, "", "", 0, nil, CertaintyConfirmed, "", "")
	if !errors.Is(err, ErrNoSourceEvidence) {
		t.Fatalf("expected ErrNoSourceEvidence, got %v", err)
	}
}

func TestNewRejectsEmptyEvidenceIDInList(t *testing.T) {
	_, err := New("EVT-1", TypeLoading, "", "", 0, []string{""}, CertaintyConfirmed, "", "")
	if err == nil {
		t.Fatal("expected error for empty EvidenceID entry")
	}
}

func TestNewRejectsUnknownCertainty(t *testing.T) {
	_, err := New("EVT-1", TypeLoading, "", "", 0, []string{"EV-1"}, Certainty("MAYBE"), "", "")
	if !errors.Is(err, ErrUnknownCertainty) {
		t.Fatalf("expected ErrUnknownCertainty, got %v", err)
	}
}

func TestNewSucceeds(t *testing.T) {
	e, err := New("EVT-1", TypeLoading, "2024-01-01T10:00:00", "+07:00", 1000, []string{"EV-1"}, CertaintyConfirmed, "Jakarta", "PTY-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.EventID != "EVT-1" || e.EventType != TypeLoading || e.EventTimeUTC != 1000 {
		t.Fatalf("unexpected event: %+v", e)
	}
}

func TestNewTimelineRejectsEmptyCaseID(t *testing.T) {
	if _, err := NewTimeline(""); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestAddEventAndGet(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	e, _ := New("EVT-1", TypeLoading, "", "", 100, []string{"EV-1"}, CertaintyConfirmed, "", "")
	if err := tl.AddEvent(e); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	got, ok := tl.Get("EVT-1")
	if !ok || got.EventID != "EVT-1" {
		t.Fatalf("expected to find EVT-1, got %+v, %v", got, ok)
	}
	if tl.Count() != 1 {
		t.Fatalf("expected count 1, got %d", tl.Count())
	}
}

func TestAddEventRejectsInvalid(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	if err := tl.AddEvent(Event{}); err == nil {
		t.Fatal("expected error for invalid event")
	}
}

func TestAddEventRejectsDuplicateID(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	e, _ := New("EVT-1", TypeLoading, "", "", 100, []string{"EV-1"}, CertaintyConfirmed, "", "")
	if err := tl.AddEvent(e); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if err := tl.AddEvent(e); !errors.Is(err, ErrDuplicateEventID) {
		t.Fatalf("expected ErrDuplicateEventID, got %v", err)
	}
}

func TestSortedOrdersByEventTimeUTCThenEventID(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	e1, _ := New("EVT-B", TypeLoading, "", "", 300, []string{"EV-1"}, CertaintyConfirmed, "", "")
	e2, _ := New("EVT-A", TypeDeparture, "", "", 100, []string{"EV-1"}, CertaintyConfirmed, "", "")
	e3, _ := New("EVT-C", TypeArrival, "", "", 100, []string{"EV-1"}, CertaintyConfirmed, "", "") // tie with e2 on time
	for _, e := range []Event{e1, e2, e3} {
		if err := tl.AddEvent(e); err != nil {
			t.Fatalf("AddEvent: %v", err)
		}
	}
	sorted := tl.Sorted()
	if len(sorted) != 3 {
		t.Fatalf("expected 3 events, got %d", len(sorted))
	}
	// e2 and e3 tie at tick 100; tie-break by EventID ascending: EVT-A < EVT-C.
	if sorted[0].EventID != "EVT-A" || sorted[1].EventID != "EVT-C" || sorted[2].EventID != "EVT-B" {
		t.Fatalf("unexpected order: %v %v %v", sorted[0].EventID, sorted[1].EventID, sorted[2].EventID)
	}
}

func TestByType(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	e1, _ := New("EVT-1", TypeLoading, "", "", 100, []string{"EV-1"}, CertaintyConfirmed, "", "")
	e2, _ := New("EVT-2", TypeDelivery, "", "", 200, []string{"EV-1"}, CertaintyConfirmed, "", "")
	e3, _ := New("EVT-3", TypeLoading, "", "", 50, []string{"EV-1"}, CertaintyConfirmed, "", "")
	for _, e := range []Event{e1, e2, e3} {
		_ = tl.AddEvent(e)
	}
	loadings := tl.ByType(TypeLoading)
	if len(loadings) != 2 {
		t.Fatalf("expected 2 LOADING events, got %d", len(loadings))
	}
	if loadings[0].EventID != "EVT-3" { // sorted: tick 50 before tick 100
		t.Fatalf("expected EVT-3 first, got %s", loadings[0].EventID)
	}
}

func TestKnownEventTypesCoversAllFourteen(t *testing.T) {
	want := []EventType{
		TypeLoading, TypeHandover, TypeDeparture, TypeVoyage, TypeIncident, TypeNotice,
		TypeArrival, TypeDischarge, TypeDelivery, TypeDamageDiscovery, TypeSurvey,
		TypeMitigation, TypeClaim, TypeRecovery,
	}
	got := KnownEventTypes()
	if len(got) != 14 {
		t.Fatalf("expected 14 event types, got %d", len(got))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("position %d: expected %s, got %s", i, w, got[i])
		}
		if !IsKnownEventType(w) {
			t.Fatalf("expected %s to be known", w)
		}
	}
	if IsKnownEventType(EventType("BOGUS")) {
		t.Fatal("expected BOGUS to be unknown")
	}
}

func TestIsKnownCertainty(t *testing.T) {
	for _, c := range []Certainty{CertaintyConfirmed, CertaintyProbable, CertaintyEstimated, CertaintyDisputed, CertaintyUnknown} {
		if !IsKnownCertainty(c) {
			t.Fatalf("expected %s known", c)
		}
	}
	if IsKnownCertainty(Certainty("")) {
		t.Fatal("expected empty certainty to be unknown")
	}
}

// TestTimelineConcurrentAccess exercises AddEvent, Get, All, and Sorted
// concurrently — run with `go test -race` to confirm the RWMutex
// actually protects every access path.
func TestTimelineConcurrentAccess(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "EVT-" + string(rune('A'+n%26)) + string(rune('0'+n/26))
			e, err := New(id, TypeLoading, "", "", uint64(n), []string{"EV-1"}, CertaintyConfirmed, "", "")
			if err != nil {
				return
			}
			_ = tl.AddEvent(e)
			_, _ = tl.Get(id)
			_ = tl.All()
			_ = tl.Sorted()
			_ = tl.Count()
		}(i)
	}
	wg.Wait()
}
