package contract

import (
	"testing"
	"time"
)

// TestARefusalIsNotADefect is the distinction the mutation suite
// attacks in both directions. It is asserted here, at the definition,
// so a change to the enum cannot pass without touching this test.
func TestARefusalIsNotADefect(t *testing.T) {
	if Refused.IsDefect() {
		t.Fatal("REFUSED classified as a defect: correct behaviour would be reported as a bug")
	}
	if !Failed.IsDefect() {
		t.Fatal("FAILED not classified as a defect: a bug would be hidden behind a safe-looking word")
	}
	if Unresolved.IsDefect() {
		t.Fatal("UNRESOLVED classified as a defect: reaching no conclusion is an answer")
	}
}

// TestOnlySucceededIsADetermination: UNRESOLVED must never be read as
// a conclusion, which is what happens when code tests `!= Failed`.
func TestOnlySucceededIsADetermination(t *testing.T) {
	for _, o := range []Outcome{Refused, Failed, Unresolved} {
		if o.IsDetermination() {
			t.Fatalf("%s treated as a determination", o)
		}
	}
	if !Succeeded.IsDetermination() {
		t.Fatal("SUCCEEDED is not a determination")
	}
}

// TestIDsAreNotRandom guards Law: no uuid.New() on deterministic
// paths. Two constructions of the same logical ID must be equal.
func TestIDsAreNotRandom(t *testing.T) {
	a, err := NewID("evidence", "sha256.abc123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewID("evidence", "sha256.abc123")
	if a != b {
		t.Fatalf("identical inputs produced different IDs: %s vs %s", a, b)
	}
	if a.Kind() != "evidence" || a.Local() != "sha256.abc123" {
		t.Fatalf("decomposition wrong: %q / %q", a.Kind(), a.Local())
	}
}

func TestMalformedIDsAreRefused(t *testing.T) {
	bad := []string{
		"",
		"nokind",
		"UPPER:x",
		"kind:",
		":local",
		"kind:has space",
		"kind:has/slash",
		"9kind:x",
	}
	for _, s := range bad {
		if err := ID(s).Validate(); err == nil {
			t.Errorf("malformed ID %q accepted", s)
		}
	}
}

// TestAnOpenIntervalIsNotForever. An unrecorded end is missing
// information; treating it as "still true" is how a lapsed charter
// stays current in a graph.
func TestAnOpenIntervalIsNotForever(t *testing.T) {
	iv := Interval{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if !iv.Open() {
		t.Fatal("interval with no end not reported as open")
	}
	// Contains still answers the mechanical question, but Open() is
	// what a caller must consult before asserting present truth.
	if !iv.Contains(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("open interval should mechanically contain later instants")
	}
}

func TestIntervalOverlap(t *testing.T) {
	d := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
	to := func(t time.Time) *time.Time { return &t }

	a := Interval{From: d(2020), To: to(d(2022))}
	b := Interval{From: d(2022), To: to(d(2024))}
	c := Interval{From: d(2021), To: to(d(2023))}

	if a.Overlaps(b) {
		t.Fatal("half-open intervals meeting at a boundary must not overlap")
	}
	if !a.Overlaps(c) {
		t.Fatal("genuinely overlapping intervals reported as disjoint")
	}
	if !b.Overlaps(c) {
		t.Fatal("genuinely overlapping intervals reported as disjoint")
	}
}

func TestIntervalValidity(t *testing.T) {
	d := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
	to := func(t time.Time) *time.Time { return &t }

	if (Interval{}).Valid() {
		t.Fatal("zero interval accepted")
	}
	if (Interval{From: d(2022), To: to(d(2020))}).Valid() {
		t.Fatal("inverted interval accepted")
	}
	if (Interval{From: d(2022), To: to(d(2022))}).Valid() {
		t.Fatal("empty half-open interval accepted")
	}
	if !(Interval{From: d(2020), To: to(d(2022))}).Valid() {
		t.Fatal("ordinary interval refused")
	}
}

// TestAVersionSetNamesWhatIsMissing. An incomplete replay manifest must
// say which version nobody recorded, not merely that it is incomplete.
func TestAVersionSetNamesWhatIsMissing(t *testing.T) {
	vs := VersionSet{Ontology: Version{Component: "maritime", Revision: 3}}
	if vs.Complete() {
		t.Fatal("incomplete version set reported complete")
	}
	missing := vs.Missing()
	if len(missing) != 2 {
		t.Fatalf("expected policy and algorithm missing, got %v", missing)
	}
	vs.Policy = Version{Component: "default", Revision: 1}
	vs.Algorithm = Version{Component: "resolution", Revision: 7}
	if !vs.Complete() {
		t.Fatalf("complete set reported incomplete: missing %v", vs.Missing())
	}
}

// TestFixedClockReplacesTimeNow: replay supplies the recorded instant,
// so a deterministic path never reads the wall clock.
func TestFixedClockReplacesTimeNow(t *testing.T) {
	want := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	var c Clock = FixedClock(want)
	if !c.Now().Equal(want) {
		t.Fatalf("FixedClock returned %v", c.Now())
	}
	if !c.Now().Equal(c.Now()) {
		t.Fatal("FixedClock is not fixed")
	}
}
