package maritime

import (
	"math"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func rep(id string, min int, lat, lon float64) Report {
	return Report{VesselID: id, At: t0.Add(time.Duration(min) * time.Minute),
		Position: Position{LatDeg: lat, LonDeg: lon}, EvidenceRef: "ev:ais-1"}
}

// TestDistanceAgainstAKnownPair.
//
// One degree of latitude is 60 nautical miles by the definition of the
// nautical mile, so a one-degree meridional separation is the check
// that needs no external reference to be believable.
func TestDistanceAgainstAKnownPair(t *testing.T) {
	d, err := DistanceNM(Position{0, 0}, Position{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(d-60) > 0.15 {
		t.Fatalf("one degree of latitude computed as %.4f NM, not ~60", d)
	}
	// A degree of longitude at the equator is the same; at 60 degrees
	// north it is half, by the cosine.
	eq, _ := DistanceNM(Position{0, 0}, Position{0, 1})
	hi, _ := DistanceNM(Position{60, 0}, Position{60, 1})
	if math.Abs(hi/eq-0.5) > 0.01 {
		t.Fatalf("longitude convergence is wrong: %.4f vs %.4f", hi, eq)
	}
	// Antipodal points must not produce NaN through accumulated error.
	anti, err := DistanceNM(Position{0, 0}, Position{0, 180})
	if err != nil || math.IsNaN(anti) {
		t.Fatalf("antipodal distance is %v (%v)", anti, err)
	}
}

// TestBearingIsClockwiseFromNorth.
func TestBearingIsClockwiseFromNorth(t *testing.T) {
	cases := []struct {
		a, b Position
		want float64
	}{
		{Position{0, 0}, Position{1, 0}, 0},
		{Position{0, 0}, Position{0, 1}, 90},
		{Position{1, 0}, Position{0, 0}, 180},
		{Position{0, 1}, Position{0, 0}, 270},
	}
	for _, c := range cases {
		got, err := BearingDeg(c.a, c.b)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-c.want) > 0.5 {
			t.Fatalf("bearing %v->%v = %.2f, want %.0f", c.a, c.b, got, c.want)
		}
	}
}

// TestPositionErrorDoesNotManufactureSpeed.
//
// This is the correction that makes the detector usable. Two reports
// ten seconds apart, each accurate to half a nautical mile, differ by
// a mile with the vessel stationary -- 360 knots on the naive
// computation.
func TestPositionErrorDoesNotManufactureSpeed(t *testing.T) {
	// 0.9 NM apart, 10 seconds, well inside a 2 x 0.5 NM error budget.
	// Not 0,0: that is Null Island, which the detector skips for its
	// own reasons, and using it here would make this test pass for
	// the wrong reason.
	a := Report{VesselID: "v1", At: t0, Position: Position{LatDeg: 20, LonDeg: 30}}
	b := Report{VesselID: "v1", At: t0.Add(10 * time.Second),
		Position: Position{LatDeg: 20.015, LonDeg: 30}}
	tr, err := NewTrack("v1", []Report{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.ImplausibleSpeed(DefaultSpeedLimits()); len(got) != 0 {
		t.Fatalf("position error inside the budget produced %d anomaly(ies): %s",
			len(got), got[0].Observation)
	}
	// A genuinely impossible leg is still caught: 600 NM in an hour.
	c := Report{VesselID: "v1", At: t0.Add(time.Hour), Position: Position{LatDeg: 30, LonDeg: 30}}
	tr2, _ := NewTrack("v1", []Report{a, c})
	got := tr2.ImplausibleSpeed(DefaultSpeedLimits())
	if len(got) != 1 {
		t.Fatalf("600 NM in an hour produced %d anomalies", len(got))
	}
	if got[0].Magnitude <= 1 {
		t.Fatalf("magnitude %.2f does not exceed the limit", got[0].Magnitude)
	}
}

// TestEveryAnomalyCarriesItsInnocentExplanations.
//
// An anomaly presented without them is an accusation wearing the
// clothes of a measurement.
func TestEveryAnomalyCarriesItsInnocentExplanations(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{
		rep("v1", 0, 0, 0),
		rep("v1", 60, 10, 0),
		{VesselID: "v1", At: t0.Add(24 * time.Hour), Position: Position{10.1, 0},
			DraughtM: f(8), EvidenceRef: "ev:ais-2"},
		{VesselID: "v1", At: t0.Add(48 * time.Hour), Position: Position{10.2, 0},
			DraughtM: f(13.5), EvidenceRef: "ev:ais-3"},
		{VesselID: "v1", At: t0.Add(49 * time.Hour), Position: Position{0, 0}},
	})
	var all []Anomaly
	all = append(all, tr.ImplausibleSpeed(DefaultSpeedLimits())...)
	all = append(all, tr.Gaps(DefaultGapPolicy())...)
	all = append(all, tr.NullPositions()...)
	all = append(all, tr.DraughtChange(1)...)
	if len(all) < 4 {
		t.Fatalf("the fixture produced only %d anomalies", len(all))
	}
	for _, a := range all {
		if len(a.Alternatives) == 0 {
			t.Fatalf("%s carries no innocent explanations", a.Kind)
		}
		if strings.TrimSpace(a.Diagnostic) == "" {
			t.Fatalf("%s says nothing about what would separate the explanations", a.Kind)
		}
		if strings.TrimSpace(a.Observation) == "" {
			t.Fatalf("%s states no observation", a.Kind)
		}
	}
}

// TestAGapIsAFactAboutTheRecord. The detector must not say the vessel
// went dark.
func TestAGapIsAFactAboutTheRecord(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{rep("v1", 0, 5, 5), rep("v1", 60*20, 6, 6)})
	got := tr.Gaps(DefaultGapPolicy())
	if len(got) != 1 {
		t.Fatalf("%d gaps", len(got))
	}
	g := got[0]
	if strings.Contains(strings.ToLower(g.Observation), "went dark") ||
		strings.Contains(strings.ToLower(g.Observation), "switched off") {
		t.Fatalf("the observation makes a claim about the vessel: %s", g.Observation)
	}
	if !strings.Contains(strings.Join(g.Alternatives, " "),
		"a fact about the record, not about the vessel") {
		t.Fatalf("the alternatives do not state the distinction: %v", g.Alternatives)
	}
	// A coastal-only network puts the receiver explanation first,
	// because offshore silence is the expected receiver behaviour.
	p := DefaultGapPolicy()
	p.CoastalOnly = true
	got = tr.Gaps(p)
	if !strings.Contains(got[0].Alternatives[0], "EXPECTED behaviour of the receiver") {
		t.Fatalf("a coastal-only network does not lead with the receiver: %v",
			got[0].Alternatives)
	}
}

// TestNullIslandIsNotAPositionInTheGulfOfGuinea.
func TestNullIslandIsNotAPositionInTheGulfOfGuinea(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{
		rep("v1", 0, 51.5, 1.0),
		rep("v1", 1, 0, 0),
		rep("v1", 2, 51.5, 1.0),
	})
	if got := tr.NullPositions(); len(got) != 1 {
		t.Fatalf("%d null positions", len(got))
	}
	// And it must not have produced two implausible-speed alerts by
	// treating 0,0 as somewhere the vessel went.
	if got := tr.ImplausibleSpeed(DefaultSpeedLimits()); len(got) != 0 {
		t.Fatalf("a null position manufactured %d speed anomalies", len(got))
	}
}

// TestASingleBadPositionIsNotAnIdentifierCollision. Promoting a
// measurement to a hypothesis needs a pattern.
func TestASingleBadPositionIsNotAnIdentifierCollision(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{
		rep("v1", 0, 0, 0.5), rep("v1", 30, 20, 0.5), rep("v1", 60, 0.1, 0.5),
	})
	if got := tr.IdentifierCollision(DefaultSpeedLimits()); len(got) != 0 {
		t.Fatalf("two implausible legs from one bad position were called a collision: %s",
			got[0].Observation)
	}
	// Simultaneous reports from separated positions are the one case
	// no measurement error explains.
	tr2, _ := NewTrack("v2", []Report{
		{VesselID: "v2", At: t0, Position: Position{10, 10}},
		{VesselID: "v2", At: t0, Position: Position{40, 40}},
	})
	got := tr2.IdentifierCollision(DefaultSpeedLimits())
	if len(got) != 1 {
		t.Fatalf("simultaneous separated reports produced %d anomalies", len(got))
	}
	if !strings.Contains(got[0].Diagnostic, "identical static data would not") {
		t.Fatalf("the diagnostic does not anticipate impersonation: %s", got[0].Diagnostic)
	}
}

// TestRendezvousIsProximityAndNotATransfer.
func TestRendezvousIsProximityAndNotATransfer(t *testing.T) {
	var a, b []Report
	for i := 0; i <= 24; i++ { // six hours at 15-minute intervals
		min := i * 15
		a = append(a, Report{VesselID: "va", At: t0.Add(time.Duration(min) * time.Minute),
			Position: Position{1.0, 1.0}, SpeedKn: f(0.2)})
		b = append(b, Report{VesselID: "vb", At: t0.Add(time.Duration(min) * time.Minute),
			Position: Position{1.001, 1.001}, SpeedKn: f(0.3)})
	}
	ta, _ := NewTrack("va", a)
	tb, _ := NewTrack("vb", b)
	got := FindRendezvous(ta, tb, DefaultRendezvousPolicy())
	if len(got) != 1 {
		t.Fatalf("%d rendezvous", len(got))
	}
	r := got[0]
	if r.Duration < 5*time.Hour {
		t.Fatalf("duration %s", r.Duration)
	}
	if r.Samples < 20 {
		t.Fatalf("%d samples", r.Samples)
	}
	if !strings.Contains(strings.Join(r.Alternatives, " "), "anchorage") {
		t.Fatalf("the alternatives do not include co-location: %v", r.Alternatives)
	}
	// A transfer at speed is not a transfer.
	for i := range b {
		b[i].SpeedKn = f(14)
	}
	tb2, _ := NewTrack("vb", b)
	if got := FindRendezvous(ta, tb2, DefaultRendezvousPolicy()); len(got) != 0 {
		t.Fatal("two vessels at 14 knots were called a rendezvous")
	}
}

// TestVesselsThatMerelyPassAreNotARendezvous.
func TestVesselsThatMerelyPassAreNotARendezvous(t *testing.T) {
	var a, b []Report
	for i := 0; i <= 12; i++ {
		min := i * 15
		a = append(a, Report{VesselID: "va", At: t0.Add(time.Duration(min) * time.Minute),
			Position: Position{1.0, 1.0 + float64(i)*0.05}, SpeedKn: f(0.5)})
		b = append(b, Report{VesselID: "vb", At: t0.Add(time.Duration(min) * time.Minute),
			Position: Position{1.0, 1.6 - float64(i)*0.05}, SpeedKn: f(0.5)})
	}
	ta, _ := NewTrack("va", a)
	tb, _ := NewTrack("vb", b)
	if got := FindRendezvous(ta, tb, DefaultRendezvousPolicy()); len(got) != 0 {
		t.Fatalf("two vessels crossing were called a rendezvous of %s", got[0].Duration)
	}
}

// TestADraughtChangeLeadsWithTheStaleValueExplanation. Draught is
// entered by hand, and a correction is far more common than a cargo
// operation.
func TestADraughtChangeLeadsWithTheStaleValueExplanation(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{
		{VesselID: "v1", At: t0, Position: Position{1, 1}, DraughtM: f(7.2)},
		{VesselID: "v1", At: t0.Add(30 * time.Hour), Position: Position{1.5, 1},
			DraughtM: f(13.8)},
	})
	got := tr.DraughtChange(1)
	if len(got) != 1 {
		t.Fatalf("%d draught changes", len(got))
	}
	if !strings.Contains(got[0].Alternatives[0], "stale") {
		t.Fatalf("the most common cause is not first: %v", got[0].Alternatives)
	}
	if !strings.Contains(got[0].Observation, "13.80") && !strings.Contains(got[0].Observation, "13.8") {
		t.Fatalf("the observation does not carry the values: %s", got[0].Observation)
	}
}

// TestTrackConstructionSortsWithoutDroppingDuplicates. A duplicate at
// a different position is the collision signal.
func TestTrackConstructionSortsWithoutDroppingDuplicates(t *testing.T) {
	tr, err := NewTrack("v1", []Report{
		rep("v1", 10, 1, 1), rep("v1", 0, 2, 2), rep("v1", 10, 3, 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Reports) != 3 {
		t.Fatalf("%d reports survived construction", len(tr.Reports))
	}
	for i := 1; i < len(tr.Reports); i++ {
		if tr.Reports[i].At.Before(tr.Reports[i-1].At) {
			t.Fatal("reports are not in time order")
		}
	}
	if _, err := NewTrack("v1", []Report{rep("v2", 0, 1, 1)}); err == nil {
		t.Fatal("a report for another vessel was accepted into the track")
	}
	if _, err := NewTrack("v1", nil); err == nil {
		t.Fatal("an empty track was created")
	}
}

// TestInvalidPositionsAreRefused.
func TestInvalidPositionsAreRefused(t *testing.T) {
	for _, p := range []Position{{91, 0}, {-91, 0}, {0, 181}, {0, -181},
		{math.NaN(), 0}, {0, math.Inf(1)}} {
		if err := p.Validate(); err == nil {
			t.Fatalf("%v validated", p)
		}
	}
	r := Report{VesselID: "v1", At: t0, Position: Position{1, 1}, SpeedKn: f(500)}
	if err := r.Validate(); err == nil {
		t.Fatal("a 500-knot merchant vessel validated")
	}
}

// TestSpeedDisagreementIsSeparateFromImplausibleSpeed. Here the
// vessel's own two observations of itself disagree, which no receiver
// problem explains.
func TestSpeedDisagreementIsSeparateFromImplausibleSpeed(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{
		{VesselID: "v1", At: t0, Position: Position{1, 1}, SpeedKn: f(0.1)},
		{VesselID: "v1", At: t0.Add(30 * time.Minute), Position: Position{1.1, 1},
			SpeedKn: f(0.1)},
	})
	got := tr.ReportedSpeedDisagreement(3, DefaultSpeedLimits())
	if len(got) != 1 {
		t.Fatalf("%d disagreements", len(got))
	}
	if !strings.Contains(strings.Join(got[0].Alternatives, " "), "current") {
		t.Fatalf("the alternatives omit the ordinary cause: %v", got[0].Alternatives)
	}
	// And it is not also an implausible speed: ~12 kn is fine.
	if len(tr.ImplausibleSpeed(DefaultSpeedLimits())) != 0 {
		t.Fatal("a legitimate speed was flagged as implausible")
	}
}

// TestEvidenceRefsTravelWithTheAnomaly. A finding drawn from a track
// must be able to cite what it rests on.
func TestEvidenceRefsTravelWithTheAnomaly(t *testing.T) {
	tr, _ := NewTrack("v1", []Report{rep("v1", 0, 0, 0.5), rep("v1", 30, 20, 0.5)})
	got := tr.ImplausibleSpeed(DefaultSpeedLimits())
	if len(got) != 1 || len(got[0].EvidenceRefs) == 0 {
		t.Fatalf("the anomaly carries no evidence references: %+v", got)
	}
	if len(tr.EvidenceRefs()) != 1 {
		t.Fatalf("the track reports %d distinct evidence refs", len(tr.EvidenceRefs()))
	}
}
