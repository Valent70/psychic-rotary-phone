package timeline

import (
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/insurance/evidence"
)

func mustEv(t *testing.T, subject, source string, observedAt, receivedAt uint64, docType string) evidence.Record {
	t.Helper()
	underlying, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     source,
		ObservedAt: observedAt,
		ReceivedAt: receivedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "deadbeef"},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	rec, err := evidence.New("CASE-1", underlying, "PTY-1", evidence.OriginClaimant)
	if err != nil {
		t.Fatalf("evidence.New: %v", err)
	}
	rec.DocumentType = docType
	return rec
}

func mustEvent(t *testing.T, id string, typ EventType, original, tz string, utc uint64, srcEvidence []string) Event {
	t.Helper()
	e, err := New(id, typ, original, tz, utc, srcEvidence, CertaintyProbable, "", "")
	if err != nil {
		t.Fatalf("New event %s: %v", id, err)
	}
	return e
}

// TestBlueprintWorkedExample_DamageDiscoveryTemporalConflict is the
// blueprint's own §14 worked example, reproduced verbatim:
//
//	Claim: Damage discovered 10:00
//	Photo metadata: 11:45
//	POD: 08:30
//	Status: TEMPORAL_CONFLICT
//	Bukan langsung: FRAUD
//
// Three independently sourced accounts, expressed as UTC seconds on an
// arbitrary reference day (10:00, 11:45 and 08:30 respectively). The
// two DAMAGE_DISCOVERY accounts disagree by 1h45m and must be flagged
// TEMPORAL_CONFLICT. No Conflict — here or anywhere in this package —
// may carry any field resembling a fraud/guilt verdict; this test
// asserts that structurally, not just by inspection.
func TestBlueprintWorkedExample_DamageDiscoveryTemporalConflict(t *testing.T) {
	const day = 1_700_000_000 // arbitrary reference epoch, for realism only
	tenAM := uint64(day + 10*3600)
	elevenFortyFive := uint64(day + 11*3600 + 45*60)
	eightThirty := uint64(day + 8*3600 + 30*60)

	tl, err := NewTimeline("CASE-WORKED-EXAMPLE")
	if err != nil {
		t.Fatalf("NewTimeline: %v", err)
	}
	claimDiscovery := mustEvent(t, "EVT-CLAIM-DISCOVERY", TypeDamageDiscovery, "10:00", "", tenAM, []string{"EV-CLAIM"})
	photoDiscovery := mustEvent(t, "EVT-PHOTO-DISCOVERY", TypeDamageDiscovery, "11:45", "", elevenFortyFive, []string{"EV-PHOTO"})
	podDelivery := mustEvent(t, "EVT-POD-DELIVERY", TypeDelivery, "08:30", "", eightThirty, []string{"EV-POD"})

	for _, e := range []Event{claimDiscovery, photoDiscovery, podDelivery} {
		if err := tl.AddEvent(e); err != nil {
			t.Fatalf("AddEvent: %v", err)
		}
	}

	// Deliberately NO evidence registry attached: this reproduces the
	// blueprint's own literal output status, TEMPORAL_CONFLICT — not
	// the more specific PHOTO_TIMESTAMP_MISMATCH category, which is
	// tested separately below.
	d := NewDetector()
	conflicts := d.DetectConflicts(tl)

	var found *Conflict
	for i := range conflicts {
		if conflicts[i].Kind == KindTemporalConflict {
			found = &conflicts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a TEMPORAL_CONFLICT among %d conflicts: %+v", len(conflicts), conflicts)
	}
	if !containsBoth(found.EventIDs, "EVT-CLAIM-DISCOVERY", "EVT-PHOTO-DISCOVERY") {
		t.Fatalf("expected TEMPORAL_CONFLICT to involve both discovery events, got %v", found.EventIDs)
	}

	// Structural proof there is no fraud/guilt field on Conflict at all.
	assertNoFraudField(t, reflect.TypeOf(Conflict{}))
	for _, c := range conflicts {
		lower := strings.ToLower(c.Description)
		if strings.Contains(lower, "fraud") || strings.Contains(lower, "guilty") || strings.Contains(lower, "liable") {
			t.Fatalf("conflict description must never assert fraud/guilt/liability, got: %q", c.Description)
		}
	}
}

func assertNoFraudField(t *testing.T, typ reflect.Type) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "fraud") || strings.Contains(name, "guilt") || strings.Contains(name, "liab") {
			t.Fatalf("Conflict must never have a fraud/guilt/liability field, found: %s", typ.Field(i).Name)
		}
	}
}

func containsBoth(ids []string, a, b string) bool {
	hasA, hasB := false, false
	for _, id := range ids {
		if id == a {
			hasA = true
		}
		if id == b {
			hasB = true
		}
	}
	return hasA && hasB
}

// TestPhotoTimestampMismatch is category 7. Same DAMAGE_DISCOVERY
// disagreement as the worked example, but this time WithEvidence is
// used and one of the two accounts is backed by evidence whose
// DocumentType marks it as a photograph — the more specific category
// must fire instead of the generic one.
func TestPhotoTimestampMismatch(t *testing.T) {
	const base = 1_700_000_000
	tl, _ := NewTimeline("CASE-PHOTO")
	reg := evidence.NewRegistry()

	claimEv := mustEv(t, "S1", "claimant", base, base, "claim_narrative")
	photoEv := mustEv(t, "S1", "claimant", base+6300, base+6300, "photo")
	if err := reg.Submit(claimEv); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := reg.Submit(photoEv); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	claimEvent := mustEvent(t, "EVT-CLAIM", TypeDamageDiscovery, "10:00", "", base+36000, []string{claimEv.EvidenceID()})
	photoEvent := mustEvent(t, "EVT-PHOTO", TypeDamageDiscovery, "11:45", "", base+42300, []string{photoEv.EvidenceID()})
	_ = tl.AddEvent(claimEvent)
	_ = tl.AddEvent(photoEvent)

	d := NewDetector().WithEvidence(reg)
	conflicts := d.DetectConflicts(tl)

	var found bool
	for _, c := range conflicts {
		if c.Kind == KindPhotoTimestampMismatch && containsBoth(c.EventIDs, "EVT-CLAIM", "EVT-PHOTO") {
			found = true
		}
		if c.Kind == KindTemporalConflict && containsBoth(c.EventIDs, "EVT-CLAIM", "EVT-PHOTO") {
			t.Fatalf("expected the photo-specific category, not the generic TEMPORAL_CONFLICT, for this pair")
		}
	}
	if !found {
		t.Fatalf("expected PHOTO_TIMESTAMP_MISMATCH, got %+v", conflicts)
	}
}

// TestSurveyDateMismatch is category 8.
func TestSurveyDateMismatch(t *testing.T) {
	tl, _ := NewTimeline("CASE-SURVEY")
	a := mustEvent(t, "EVT-SURVEY-A", TypeSurvey, "", "", 1000, []string{"EV-1"})
	b := mustEvent(t, "EVT-SURVEY-B", TypeSurvey, "", "", 1000+3600, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindSurveyDateMismatch, "EVT-SURVEY-A", "EVT-SURVEY-B") {
		t.Fatalf("expected SURVEY_DATE_MISMATCH, got %+v", conflicts)
	}
}

// TestDeliveryDateMismatch is category 9.
func TestDeliveryDateMismatch(t *testing.T) {
	tl, _ := NewTimeline("CASE-DELIVERY")
	a := mustEvent(t, "EVT-DEL-A", TypeDelivery, "", "", 5000, []string{"EV-1"})
	b := mustEvent(t, "EVT-DEL-B", TypeDelivery, "", "", 5000+7200, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindDeliveryDateMismatch, "EVT-DEL-A", "EVT-DEL-B") {
		t.Fatalf("expected DELIVERY_DATE_MISMATCH, got %+v", conflicts)
	}
}

// TestConflictingTimestamps_AdversarialCase4 exercises the blueprint
// §35 adversarial case #4, "Conflicting timestamps", on a generic
// (non-specialised) event type — ARRIVAL — via the general
// TEMPORAL_CONFLICT category.
func TestConflictingTimestamps_AdversarialCase4(t *testing.T) {
	tl, _ := NewTimeline("CASE-4")
	a := mustEvent(t, "EVT-ARR-A", TypeArrival, "14:00", "", 50000, []string{"EV-1"})
	b := mustEvent(t, "EVT-ARR-B", TypeArrival, "16:30", "", 50000+9000, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindTemporalConflict, "EVT-ARR-A", "EVT-ARR-B") {
		t.Fatalf("expected TEMPORAL_CONFLICT, got %+v", conflicts)
	}
}

// TestSameTypeEventsBeyondCorrelationWindowAreNotFlagged is the
// negative case: two SURVEY events months apart are legitimately
// distinct occurrences (an initial survey and a much later follow-up),
// not a mismatch, and must not be flagged.
func TestSameTypeEventsBeyondCorrelationWindowAreNotFlagged(t *testing.T) {
	tl, _ := NewTimeline("CASE-NO-CONFLICT")
	sixMonths := uint64(6 * 30 * 24 * 3600)
	a := mustEvent(t, "EVT-SURVEY-A", TypeSurvey, "", "", 1000, []string{"EV-1"})
	b := mustEvent(t, "EVT-SURVEY-B", TypeSurvey, "", "", 1000+sixMonths, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindSurveyDateMismatch {
			t.Fatalf("did not expect SURVEY_DATE_MISMATCH for events six months apart, got %+v", c)
		}
	}
}

// TestWrongTimezone_AdversarialCase5 exercises blueprint §35
// adversarial case #5, "Wrong timezone": two accounts of the same
// occurrence declare different, both-parseable timezone offsets.
func TestWrongTimezone_AdversarialCase5(t *testing.T) {
	tl, _ := NewTimeline("CASE-5")
	a := mustEvent(t, "EVT-TZ-A", TypeLoading, "10:00", "+07:00", 10000, []string{"EV-1"})
	b := mustEvent(t, "EVT-TZ-B", TypeLoading, "10:00", "+00:00", 10000, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindTimezoneConflict, "EVT-TZ-A", "EVT-TZ-B") {
		t.Fatalf("expected TIMEZONE_CONFLICT, got %+v", conflicts)
	}
}

// TestUnparseableTimezoneIsInsufficientDataNotGuessed: an IANA zone
// name this package's small parser cannot resolve must NOT be silently
// treated as any particular offset — no timezone conflict is raised
// for it, per the Golden Rule.
func TestUnparseableTimezoneIsInsufficientDataNotGuessed(t *testing.T) {
	tl, _ := NewTimeline("CASE-TZ-UNKNOWN")
	a := mustEvent(t, "EVT-TZ-A", TypeLoading, "10:00", "Asia/Jakarta", 10000, []string{"EV-1"})
	b := mustEvent(t, "EVT-TZ-B", TypeLoading, "10:00", "+00:00", 10000, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindTimezoneConflict {
			t.Fatalf("did not expect a TIMEZONE_CONFLICT to be guessed for an unparseable zone name, got %+v", c)
		}
	}
}

func TestParseUTCOffsetMinutes(t *testing.T) {
	cases := []struct {
		in      string
		wantMin int
		wantOK  bool
	}{
		{"UTC", 0, true},
		{"Z", 0, true},
		{"+07:00", 420, true},
		{"-05:00", -300, true},
		{"+0700", 420, true},
		{"-0530", -330, true},
		{"GMT+8", 480, true},
		{"Asia/Jakarta", 0, false},
		{"", 0, false},
		{"++07:00", 0, false},
	}
	for _, c := range cases {
		got, ok := parseUTCOffsetMinutes(c.in)
		if ok != c.wantOK {
			t.Errorf("parseUTCOffsetMinutes(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && got != c.wantMin {
			t.Errorf("parseUTCOffsetMinutes(%q) = %d, want %d", c.in, got, c.wantMin)
		}
	}
}

// TestImpossibleSequence: DEPARTURE canonically precedes ARRIVAL, but
// here the reconstructed ARRIVAL time is BEFORE the DEPARTURE time —
// the ship arrives before it leaves.
func TestImpossibleSequence(t *testing.T) {
	tl, _ := NewTimeline("CASE-SEQ")
	departure := mustEvent(t, "EVT-DEP", TypeDeparture, "", "", 20000, []string{"EV-1"})
	arrival := mustEvent(t, "EVT-ARR", TypeArrival, "", "", 10000, []string{"EV-2"}) // earlier than departure
	_ = tl.AddEvent(departure)
	_ = tl.AddEvent(arrival)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindImpossibleSequence, "EVT-DEP", "EVT-ARR") {
		t.Fatalf("expected IMPOSSIBLE_SEQUENCE, got %+v", conflicts)
	}
}

// TestImpossibleSequenceDoesNotDuplicateNoticeBeforeIncident: the
// (INCIDENT, NOTICE) canonical pair is explicitly excluded from the
// generic impossible-sequence detector because it has its own more
// specific category.
func TestImpossibleSequenceDoesNotDuplicateNoticeBeforeIncident(t *testing.T) {
	tl, _ := NewTimeline("CASE-NOTICE-EXCL")
	incident := mustEvent(t, "EVT-INC", TypeIncident, "", "", 20000, []string{"EV-1"})
	notice := mustEvent(t, "EVT-NOT", TypeNotice, "", "", 10000, []string{"EV-2"}) // before incident
	_ = tl.AddEvent(incident)
	_ = tl.AddEvent(notice)

	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindImpossibleSequence {
			t.Fatalf("expected the INCIDENT/NOTICE pair to be handled only by NOTICE_SUBMITTED_BEFORE_INCIDENT, got %+v", c)
		}
	}
	if !hasKindForPair(conflicts, KindNoticeBeforeIncident, "EVT-INC", "EVT-NOT") {
		t.Fatalf("expected NOTICE_SUBMITTED_BEFORE_INCIDENT, got %+v", conflicts)
	}
}

// TestNoticeSubmittedBeforeIncident_AdversarialCase10 is category 10.
func TestNoticeSubmittedBeforeIncident_AdversarialCase10(t *testing.T) {
	tl, _ := NewTimeline("CASE-10")
	incident := mustEvent(t, "EVT-INCIDENT", TypeIncident, "", "", 100000, []string{"EV-1"})
	notice := mustEvent(t, "EVT-NOTICE", TypeNotice, "", "", 99000, []string{"EV-2"})
	_ = tl.AddEvent(incident)
	_ = tl.AddEvent(notice)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindNoticeBeforeIncident, "EVT-INCIDENT", "EVT-NOTICE") {
		t.Fatalf("expected NOTICE_SUBMITTED_BEFORE_INCIDENT, got %+v", conflicts)
	}
}

func TestNoticeAfterIncidentIsNotFlagged(t *testing.T) {
	tl, _ := NewTimeline("CASE-10-OK")
	incident := mustEvent(t, "EVT-INCIDENT", TypeIncident, "", "", 100000, []string{"EV-1"})
	notice := mustEvent(t, "EVT-NOTICE", TypeNotice, "", "", 100500, []string{"EV-2"})
	_ = tl.AddEvent(incident)
	_ = tl.AddEvent(notice)

	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindNoticeBeforeIncident {
			t.Fatalf("did not expect NOTICE_SUBMITTED_BEFORE_INCIDENT when notice follows incident, got %+v", c)
		}
	}
}

// TestDuplicateEvent is category 4: two distinct EventIDs agreeing on
// every descriptive field.
func TestDuplicateEvent(t *testing.T) {
	tl, _ := NewTimeline("CASE-DUP")
	e1, _ := New("EVT-1", TypeLoading, "", "", 1000, []string{"EV-1"}, CertaintyConfirmed, "Port A", "PTY-1")
	e2, _ := New("EVT-2", TypeLoading, "", "", 1000, []string{"EV-2"}, CertaintyConfirmed, "Port A", "PTY-1")
	_ = tl.AddEvent(e1)
	_ = tl.AddEvent(e2)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindDuplicateEvent, "EVT-1", "EVT-2") {
		t.Fatalf("expected DUPLICATE_EVENT, got %+v", conflicts)
	}
}

// TestCorroborationIsNotFlaggedAsDuplicate: same type and time but a
// DIFFERENT actor/location is independent corroboration, not a
// duplicate entry, and must not be flagged as one.
func TestCorroborationIsNotFlaggedAsDuplicate(t *testing.T) {
	tl, _ := NewTimeline("CASE-CORROBORATION")
	e1, _ := New("EVT-1", TypeLoading, "", "", 1000, []string{"EV-1"}, CertaintyConfirmed, "Port A", "PTY-1")
	e2, _ := New("EVT-2", TypeLoading, "", "", 1000, []string{"EV-2"}, CertaintyConfirmed, "Port A", "PTY-2")
	_ = tl.AddEvent(e1)
	_ = tl.AddEvent(e2)

	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindDuplicateEvent {
			t.Fatalf("did not expect DUPLICATE_EVENT for two different actors, got %+v", c)
		}
	}
}

// TestMissingCustodyInterval_AdversarialCase19 is category 5,
// reproducing blueprint §35 adversarial case #19, "Missing custody
// interval": DISCHARGE happens, then nothing at all is recorded until
// DELIVERY weeks later.
func TestMissingCustodyInterval_AdversarialCase19(t *testing.T) {
	tl, _ := NewTimeline("CASE-19")
	discharge := mustEvent(t, "EVT-DISCHARGE", TypeDischarge, "", "", 0, []string{"EV-1"})
	delivery := mustEvent(t, "EVT-DELIVERY", TypeDelivery, "", "", 30*24*3600, []string{"EV-2"}) // 30 days later
	_ = tl.AddEvent(discharge)
	_ = tl.AddEvent(delivery)

	conflicts := NewDetector().DetectConflicts(tl)
	if !hasKindForPair(conflicts, KindMissingInterval, "EVT-DISCHARGE", "EVT-DELIVERY") {
		t.Fatalf("expected MISSING_INTERVAL, got %+v", conflicts)
	}
}

func TestNoMissingIntervalWhenGapIsSmall(t *testing.T) {
	tl, _ := NewTimeline("CASE-19-OK")
	discharge := mustEvent(t, "EVT-DISCHARGE", TypeDischarge, "", "", 0, []string{"EV-1"})
	delivery := mustEvent(t, "EVT-DELIVERY", TypeDelivery, "", "", 3600, []string{"EV-2"}) // 1 hour later
	_ = tl.AddEvent(discharge)
	_ = tl.AddEvent(delivery)

	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindMissingInterval {
			t.Fatalf("did not expect MISSING_INTERVAL for a 1-hour gap, got %+v", c)
		}
	}
}

// TestBackdatedDocument_AdversarialCase6 is category 6, reproducing
// blueprint §35 adversarial case #6, "Backdated document": a document
// cited as evidence for an event is itself dated (ObservedAt) before
// the very event it is offered to prove.
func TestBackdatedDocument_AdversarialCase6(t *testing.T) {
	tl, _ := NewTimeline("CASE-6")
	reg := evidence.NewRegistry()

	// Survey report claims (ObservedAt) to be dated tick 500, but is
	// cited as evidence for a SURVEY event reconstructed at tick 1000
	// — the document predates the event it documents.
	rec := mustEv(t, "S1", "surveyor", 500, 500, "survey_report")
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	survey := mustEvent(t, "EVT-SURVEY", TypeSurvey, "", "", 1000, []string{rec.EvidenceID()})
	_ = tl.AddEvent(survey)

	d := NewDetector().WithEvidence(reg)
	conflicts := d.DetectConflicts(tl)
	var found bool
	for _, c := range conflicts {
		if c.Kind == KindDocumentIssuedAfterClaimedEvent && c.EventIDs[0] == "EVT-SURVEY" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected DOCUMENT_ISSUED_AFTER_CLAIMED_EVENT, got %+v", conflicts)
	}
}

func TestDocumentIssuedAfterClaimedEventFindsNothingWithoutRegistry(t *testing.T) {
	tl, _ := NewTimeline("CASE-6-NO-REG")
	survey := mustEvent(t, "EVT-SURVEY", TypeSurvey, "", "", 1000, []string{"EV-UNRESOLVABLE"})
	_ = tl.AddEvent(survey)

	// No WithEvidence call: category 6 must find nothing, never guess.
	conflicts := NewDetector().DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindDocumentIssuedAfterClaimedEvent {
			t.Fatalf("did not expect DOCUMENT_ISSUED_AFTER_CLAIMED_EVENT without an evidence registry, got %+v", c)
		}
	}
}

func TestDocumentDatedNormallyAfterEventIsNotFlagged(t *testing.T) {
	tl, _ := NewTimeline("CASE-6-OK")
	reg := evidence.NewRegistry()
	rec := mustEv(t, "S1", "surveyor", 2000, 2000, "survey_report") // dated AFTER the event, as normal
	if err := reg.Submit(rec); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	survey := mustEvent(t, "EVT-SURVEY", TypeSurvey, "", "", 1000, []string{rec.EvidenceID()})
	_ = tl.AddEvent(survey)

	conflicts := NewDetector().WithEvidence(reg).DetectConflicts(tl)
	for _, c := range conflicts {
		if c.Kind == KindDocumentIssuedAfterClaimedEvent {
			t.Fatalf("did not expect a flag when the document is dated normally after the event, got %+v", c)
		}
	}
}

func TestKnownConflictKindsHasAllTen(t *testing.T) {
	kinds := KnownConflictKinds()
	if len(kinds) != 10 {
		t.Fatalf("expected 10 conflict kinds, got %d", len(kinds))
	}
	for _, k := range kinds {
		if !IsKnownConflictKind(k) {
			t.Fatalf("expected %s to be known", k)
		}
	}
	if IsKnownConflictKind(ConflictKind("BOGUS")) {
		t.Fatal("expected BOGUS to be unknown")
	}
}

func TestConflictIDIsDeterministic(t *testing.T) {
	tl, _ := NewTimeline("CASE-DET")
	a := mustEvent(t, "EVT-A", TypeSurvey, "", "", 1000, []string{"EV-1"})
	b := mustEvent(t, "EVT-B", TypeSurvey, "", "", 2000, []string{"EV-2"})
	_ = tl.AddEvent(a)
	_ = tl.AddEvent(b)

	first := NewDetector().DetectConflicts(tl)
	second := NewDetector().DetectConflicts(tl)
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("expected identical non-empty results across runs, got %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ConflictID != second[i].ConflictID {
			t.Fatalf("expected deterministic ConflictID, got %q vs %q", first[i].ConflictID, second[i].ConflictID)
		}
	}
}

func hasKindForPair(conflicts []Conflict, kind ConflictKind, a, b string) bool {
	for _, c := range conflicts {
		if c.Kind == kind && containsBoth(c.EventIDs, a, b) {
			return true
		}
	}
	return false
}

// TestDetectConflictsConcurrentSafety: DetectConflicts must not race
// with concurrent readers of the Timeline it was handed a snapshot
// from. Run with `go test -race`.
func TestDetectConflictsConcurrentSafety(t *testing.T) {
	tl, _ := NewTimeline("CASE-RACE")
	for i := 0; i < 10; i++ {
		e := mustEvent(t, "EVT-"+string(rune('A'+i)), TypeSurvey, "", "", uint64(1000+i*10), []string{"EV-1"})
		_ = tl.AddEvent(e)
	}
	d := NewDetector()
	done := make(chan struct{})
	go func() {
		_ = tl.All()
		_ = tl.Sorted()
		close(done)
	}()
	_ = d.DetectConflicts(tl)
	<-done
}
