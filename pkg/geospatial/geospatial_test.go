package geospatial

import (
	"errors"
	"math"
	"testing"
)

func almostEqual(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---- Coordinate validation ----

func TestValidateAcceptsValidCoordinate(t *testing.T) {
	if err := (Coordinate{Lat: 51.5, Lon: -0.1}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsInvalidCoordinates(t *testing.T) {
	cases := []struct {
		name string
		c    Coordinate
		want error
	}{
		{"lat too high", Coordinate{Lat: 91, Lon: 0}, ErrLatitudeOutOfRange},
		{"lat too low", Coordinate{Lat: -91, Lon: 0}, ErrLatitudeOutOfRange},
		{"lon too high", Coordinate{Lat: 0, Lon: 181}, ErrLongitudeOutOfRange},
		{"lon too low", Coordinate{Lat: 0, Lon: -181}, ErrLongitudeOutOfRange},
		{"NaN lat", Coordinate{Lat: math.NaN(), Lon: 0}, ErrNotFinite},
		{"Inf lon", Coordinate{Lat: 0, Lon: math.Inf(1)}, ErrNotFinite},
	}
	for _, c := range cases {
		if err := c.c.Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
}

// TestBoundaryCoordinatesAreValid proves the exact boundary values
// (+/-90 lat, +/-180 lon) are accepted, not off-by-one rejected.
func TestBoundaryCoordinatesAreValid(t *testing.T) {
	for _, c := range []Coordinate{
		{Lat: 90, Lon: 180}, {Lat: -90, Lon: -180}, {Lat: 90, Lon: -180}, {Lat: -90, Lon: 180},
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("boundary coordinate %+v should validate: %v", c, err)
		}
	}
}

func TestNormalizeWrapsLongitude(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{190, -170}, {-190, 170}, {180, -180}, {-180, -180}, {360, 0}, {0, 0}, {540, -180},
	}
	for _, c := range cases {
		got := (Coordinate{Lat: 0, Lon: c.in}).Normalize().Lon
		if !almostEqual(got, c.want, 1e-9) {
			t.Errorf("Normalize(%v): expected %v, got %v", c.in, c.want, got)
		}
	}
}

// ---- Distance / bearing ----

// TestDistanceKnownCityPair checks the haversine implementation against
// a widely published reference value: London to Paris is approximately
// 343-344 km great-circle.
func TestDistanceKnownCityPair(t *testing.T) {
	london := Coordinate{Lat: 51.5074, Lon: -0.1278}
	paris := Coordinate{Lat: 48.8566, Lon: 2.3522}
	got := DistanceMeters(london, paris) / 1000
	if got < 340 || got > 347 {
		t.Fatalf("London-Paris distance out of expected range: got %.1f km", got)
	}
}

func TestDistanceZeroForIdenticalPoint(t *testing.T) {
	p := Coordinate{Lat: 10, Lon: 20}
	if d := DistanceMeters(p, p); d != 0 {
		t.Fatalf("expected 0 distance for identical point, got %v", d)
	}
}

// TestDistanceAcrossAntimeridian proves two points just either side of
// +/-180 are computed as CLOSE, not as nearly half the Earth's
// circumference apart (the naive-subtraction bug this package's doc
// comment warns against).
func TestDistanceAcrossAntimeridian(t *testing.T) {
	a := Coordinate{Lat: 0, Lon: 179.9}
	b := Coordinate{Lat: 0, Lon: -179.9}
	got := DistanceMeters(a, b)
	// 0.2 degrees of longitude at the equator is about 22.2 km.
	if got > 30_000 {
		t.Fatalf("expected a short antimeridian-crossing distance, got %.0f m (naive-subtraction bug?)", got)
	}
}

func TestBearingCardinalDirections(t *testing.T) {
	origin := Coordinate{Lat: 0, Lon: 0}
	north := Coordinate{Lat: 1, Lon: 0}
	east := Coordinate{Lat: 0, Lon: 1}
	if b := BearingDegrees(origin, north); !almostEqual(b, 0, 0.5) {
		t.Errorf("expected bearing ~0 (north), got %v", b)
	}
	if b := BearingDegrees(origin, east); !almostEqual(b, 90, 0.5) {
		t.Errorf("expected bearing ~90 (east), got %v", b)
	}
}

// ---- Speed / impossible movement ----

func TestImpliedSpeedKnotsRealisticVessel(t *testing.T) {
	a := Fix{Coordinate: Coordinate{Lat: 0, Lon: 0}, Tick: 0}
	// ~1 nautical mile north in 6 minutes (360 ticks at 1s/tick) => ~10 knots.
	b := Fix{Coordinate: Coordinate{Lat: 1.0 / 60, Lon: 0}, Tick: 360}
	knots, err := ImpliedSpeedKnots(a, b, 1.0)
	if err != nil {
		t.Fatalf("ImpliedSpeedKnots: %v", err)
	}
	if knots < 9 || knots > 11 {
		t.Fatalf("expected ~10 knots, got %.2f", knots)
	}
	if IsImpossibleSpeed(knots) {
		t.Fatalf("10 knots must not be flagged as impossible")
	}
}

// TestImpossibleSpeedDetectsTeleportation is the "AIS gap then 400nm
// away" case the mandate names directly.
func TestImpossibleSpeedDetectsTeleportation(t *testing.T) {
	a := Fix{Coordinate: Coordinate{Lat: 0, Lon: 0}, Tick: 0}
	// 400 nautical miles east, 6 minutes later.
	b := Fix{Coordinate: Coordinate{Lat: 0, Lon: 400.0 / 60}, Tick: 360}
	knots, err := ImpliedSpeedKnots(a, b, 1.0)
	if err != nil {
		t.Fatalf("ImpliedSpeedKnots: %v", err)
	}
	if !IsImpossibleSpeed(knots) {
		t.Fatalf("expected %.0f knots to be flagged impossible", knots)
	}
}

func TestImpliedSpeedRejectsNonPositiveElapsed(t *testing.T) {
	a := Fix{Coordinate: Coordinate{Lat: 0, Lon: 0}, Tick: 100}
	sameTick := Fix{Coordinate: Coordinate{Lat: 1, Lon: 1}, Tick: 100}
	before := Fix{Coordinate: Coordinate{Lat: 1, Lon: 1}, Tick: 50}
	if _, err := ImpliedSpeedKnots(a, sameTick, 1.0); !errors.Is(err, ErrNonPositiveElapsed) {
		t.Errorf("same-tick fixes: expected ErrNonPositiveElapsed, got %v", err)
	}
	if _, err := ImpliedSpeedKnots(a, before, 1.0); !errors.Is(err, ErrNonPositiveElapsed) {
		t.Errorf("b before a: expected ErrNonPositiveElapsed, got %v", err)
	}
}

// ---- Route ----

func TestRouteLengthSumsSegments(t *testing.T) {
	r := Route{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 1}, {Lat: 0, Lon: 2}}
	oneLeg := DistanceMeters(r[0], r[1])
	total := r.LengthMeters()
	if !almostEqual(total, oneLeg*2, oneLeg*0.01) {
		t.Fatalf("expected route length ~%v, got %v", oneLeg*2, total)
	}
}

func TestRouteLengthZeroForShortRoutes(t *testing.T) {
	if l := (Route{}).LengthMeters(); l != 0 {
		t.Fatalf("empty route: expected 0, got %v", l)
	}
	if l := (Route{{Lat: 0, Lon: 0}}).LengthMeters(); l != 0 {
		t.Fatalf("single-point route: expected 0, got %v", l)
	}
}

// ---- Polygon containment ----

func square() Polygon {
	return Polygon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}
}

func TestPolygonContainsInteriorPoint(t *testing.T) {
	if !square().Contains(Coordinate{Lat: 5, Lon: 5}) {
		t.Fatal("expected interior point to be contained")
	}
}

func TestPolygonExcludesExteriorPoint(t *testing.T) {
	if square().Contains(Coordinate{Lat: 20, Lon: 20}) {
		t.Fatal("expected exterior point to be excluded")
	}
}

// TestPolygonBoundaryBehavior documents the ray-casting algorithm's
// standard boundary convention (some edges count as inside, some don't
// — this is the well-known even-odd rule's own behavior, not a bug) by
// checking a point exactly on a vertex is handled without panicking or
// producing a non-deterministic result across repeated calls.
func TestPolygonBoundaryDeterministic(t *testing.T) {
	sq := square()
	onEdge := Coordinate{Lat: 0, Lon: 5}
	first := sq.Contains(onEdge)
	for i := 0; i < 5; i++ {
		if got := sq.Contains(onEdge); got != first {
			t.Fatalf("boundary containment must be deterministic across calls, got %v then %v", first, got)
		}
	}
}

func TestValidatePolygonRejectsDegenerate(t *testing.T) {
	if err := (Polygon{{Lat: 0, Lon: 0}, {Lat: 1, Lon: 1}}).Validate(); !errors.Is(err, ErrDegeneratePolygon) {
		t.Fatalf("expected ErrDegeneratePolygon, got %v", err)
	}
}

// ---- Intersection / proximity ----

func TestIntersectsOverlappingPolygons(t *testing.T) {
	a := Polygon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}
	b := Polygon{{Lat: 5, Lon: 5}, {Lat: 5, Lon: 15}, {Lat: 15, Lon: 15}, {Lat: 15, Lon: 5}}
	if !Intersects(a, b) {
		t.Fatal("expected overlapping polygons to intersect")
	}
}

func TestIntersectsSeparatedPolygons(t *testing.T) {
	a := Polygon{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 1}, {Lat: 1, Lon: 1}, {Lat: 1, Lon: 0}}
	b := Polygon{{Lat: 50, Lon: 50}, {Lat: 50, Lon: 51}, {Lat: 51, Lon: 51}, {Lat: 51, Lon: 50}}
	if Intersects(a, b) {
		t.Fatal("expected far-apart polygons NOT to intersect")
	}
}

func TestIsProximate(t *testing.T) {
	a := Coordinate{Lat: 0, Lon: 0}
	b := Coordinate{Lat: 0, Lon: 0.001} // ~111m
	if !IsProximate(a, b, 200) {
		t.Fatal("expected points ~111m apart to be proximate within 200m")
	}
	if IsProximate(a, b, 50) {
		t.Fatal("expected points ~111m apart NOT to be proximate within 50m")
	}
}

// ---- Geofence / temporal geofence ----

func TestGeofenceContainsAtRespectsEffectiveWindow(t *testing.T) {
	g := Geofence{
		ID: "PORT-1", Kind: ZoneKindPort, Polygon: square(),
		EffectiveFrom: 100, EffectiveTo: 200,
	}
	pt := Coordinate{Lat: 5, Lon: 5}
	if g.ContainsAt(pt, 50) {
		t.Fatal("expected NOT effective before EffectiveFrom")
	}
	if !g.ContainsAt(pt, 150) {
		t.Fatal("expected effective and containing within window")
	}
	if g.ContainsAt(pt, 250) {
		t.Fatal("expected NOT effective after EffectiveTo")
	}
}

func TestGeofenceOpenEndedEffectiveTo(t *testing.T) {
	g := Geofence{ID: "ANCH-1", Kind: ZoneKindAnchorage, Polygon: square(), EffectiveFrom: 10}
	pt := Coordinate{Lat: 5, Lon: 5}
	if !g.ContainsAt(pt, 999_999) {
		t.Fatal("EffectiveTo == 0 must mean open-ended, matching policy.Version's own convention")
	}
}

func TestGeofenceValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		g    Geofence
		want error
	}{
		{"empty id", Geofence{Kind: ZoneKindPort, Polygon: square()}, ErrEmptyGeofenceID},
		{"unknown kind", Geofence{ID: "X", Kind: "BOGUS", Polygon: square()}, ErrUnknownZoneKind},
		{"to before from", Geofence{ID: "X", Kind: ZoneKindPort, Polygon: square(), EffectiveFrom: 100, EffectiveTo: 50}, ErrGeofenceToBeforeFrom},
	}
	for _, c := range cases {
		if err := c.g.Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
}

// ---- Registry ----

func TestRegistryContainingReturnsOverlappingZonesSorted(t *testing.T) {
	reg := NewRegistry()
	port := Geofence{ID: "PORT-B", Kind: ZoneKindPort, Polygon: Polygon{
		{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0},
	}}
	berth := Geofence{ID: "BERTH-A", Kind: ZoneKindBerth, Polygon: Polygon{
		{Lat: 4, Lon: 4}, {Lat: 4, Lon: 6}, {Lat: 6, Lon: 6}, {Lat: 6, Lon: 4},
	}}
	if err := reg.Register(port); err != nil {
		t.Fatalf("Register port: %v", err)
	}
	if err := reg.Register(berth); err != nil {
		t.Fatalf("Register berth: %v", err)
	}
	got := reg.Containing(Coordinate{Lat: 5, Lon: 5}, 0)
	if len(got) != 2 {
		t.Fatalf("expected point inside both port and berth, got %d zones: %+v", len(got), got)
	}
	if got[0].ID != "BERTH-A" || got[1].ID != "PORT-B" {
		t.Fatalf("expected sorted [BERTH-A, PORT-B], got [%s, %s]", got[0].ID, got[1].ID)
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	reg := NewRegistry()
	g := Geofence{ID: "X", Kind: ZoneKindTerminal, Polygon: square()}
	if err := reg.Register(g); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(g); !errors.Is(err, ErrDuplicateGeofence) {
		t.Fatalf("expected ErrDuplicateGeofence, got %v", err)
	}
}

// ---- Fix / uncertainty ----

func TestFixValidateRejectsNegativeUncertainty(t *testing.T) {
	f := Fix{Coordinate: Coordinate{Lat: 0, Lon: 0}, UncertaintyMeters: -1}
	if err := f.Validate(); err == nil {
		t.Fatal("expected negative uncertainty to be rejected")
	}
}
