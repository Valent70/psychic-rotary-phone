// Package geospatial is VERIQO's coordinate, distance, geofence and
// route-geometry engine — closing the VERIQO Final Remaining Gap
// Closure Order's GEO-001 item ("P0 — Geospatial / Geofence Engine"),
// an explicitly discovered gap: no such package existed in this
// repository before this round.
//
// Scope, stated precisely because this package sits under both the
// maritime domain (AIS/SAR/EO tracks, port calls, anchorage, STS, route
// deviation) and the insurance domain (an incident location must be
// linkable to a voyage, insured interest, policy, claim, evidence,
// causation, survey, dispute): this package owns coordinates, distance,
// bearing, speed, polygons and geofences as PURE GEOMETRY. It has no
// dependency on pkg/domain/maritime or pkg/insurance — either domain
// imports THIS package, never the other way round, so no domain-specific
// assumption (a vessel, a policy, a claim) leaks into geometry that has
// nothing to do with any of them.
//
// Why float64 here, when the rest of this codebase forbids float64 for
// authoritative monetary calculations: WGS84 coordinates are not
// financial figures under a reproducibility requirement of the kind
// quantum.Amount exists to satisfy — a coordinate's precision is bounded
// by the SOURCE's own accuracy (a GPS fix is not exact to 1e-15 degrees
// regardless of representation), and every geodesy library in the
// industry (Go's own, PostGIS, S2, GEOS) represents coordinates in
// float64 for exactly this reason. Where this package's OUTPUT feeds a
// monetary calculation (it does not, today), that boundary would need
// the same fixed-point discipline pkg/insurance/quantum already uses;
// nothing here does that.
package geospatial

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// ---- Coordinate ----

// Coordinate is a WGS84 latitude/longitude pair in decimal degrees.
type Coordinate struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

var (
	ErrLatitudeOutOfRange  = errors.New("geospatial: latitude must be in [-90, 90]")
	ErrLongitudeOutOfRange = errors.New("geospatial: longitude must be in [-180, 180]")
	ErrNotFinite           = errors.New("geospatial: coordinate component is NaN or infinite")
)

// Validate reports whether c is a structurally valid coordinate: finite,
// latitude in [-90, 90], longitude in [-180, 180]. It does NOT normalize
// — a caller with a raw longitude outside [-180, 180] (e.g. 190,
// commonly produced by naive dead-reckoning across the antimeridian)
// must call Normalize first, deliberately, rather than have this
// function silently wrap it.
func (c Coordinate) Validate() error {
	if math.IsNaN(c.Lat) || math.IsInf(c.Lat, 0) || math.IsNaN(c.Lon) || math.IsInf(c.Lon, 0) {
		return ErrNotFinite
	}
	if c.Lat < -90 || c.Lat > 90 {
		return fmt.Errorf("%w: %f", ErrLatitudeOutOfRange, c.Lat)
	}
	if c.Lon < -180 || c.Lon > 180 {
		return fmt.Errorf("%w: %f", ErrLongitudeOutOfRange, c.Lon)
	}
	return nil
}

// Normalize returns c with its longitude wrapped into [-180, 180] —
// e.g. 190 -> -170, -190 -> 170. Latitude is NOT wrapped (a latitude
// outside [-90, 90] is a malformed fix, never a wraparound case) and is
// passed through unchanged, so Validate is still needed after
// Normalize to catch that.
func (c Coordinate) Normalize() Coordinate {
	lon := math.Mod(c.Lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	return Coordinate{Lat: c.Lat, Lon: lon - 180}
}

// ---- Uncertainty ----

// Fix is one observed Coordinate with an explicit horizontal uncertainty
// radius, honestly representing GEO-001's "coordinate uncertainty"
// requirement: a GPS/AIS fix is never exact, and this package never
// treats an unstated uncertainty as zero. UncertaintyMeters == 0 means
// "not stated by the source", not "exact" — callers that need to
// distinguish the two check the source's own metadata, matching
// evidence.Record's own discipline for absent vs. zero.
type Fix struct {
	Coordinate
	// Tick is the logical time of this fix (matches this repository's
	// injected-tick convention — never wall-clock time).
	Tick uint64 `json:"tick"`
	// UncertaintyMeters is the source-reported horizontal accuracy
	// radius, when stated.
	UncertaintyMeters float64 `json:"uncertainty_meters,omitempty"`
}

// Validate checks the embedded Coordinate and that UncertaintyMeters, if
// set, is non-negative.
func (f Fix) Validate() error {
	if err := f.Coordinate.Validate(); err != nil {
		return err
	}
	if f.UncertaintyMeters < 0 {
		return errors.New("geospatial: Fix.UncertaintyMeters must be >= 0")
	}
	return nil
}

// ---- Distance, bearing, speed ----

// EarthRadiusMeters is the mean Earth radius used by every distance
// calculation in this package (WGS84 mean radius, IUGG value).
const EarthRadiusMeters = 6371008.8

func toRadians(deg float64) float64 { return deg * math.Pi / 180 }

// DistanceMeters returns the great-circle (haversine) distance between
// a and b in meters. Haversine is used deliberately rather than a naive
// planar (Pythagorean) distance on raw lat/lon degrees: the latter is
// wrong by a latitude-dependent factor everywhere except the equator,
// and breaks down entirely near the poles and across the antimeridian.
// Haversine handles the antimeridian correctly with no special case:
// it operates on the actual angular separation (via the trigonometric
// functions of each coordinate), not on a naive numeric subtraction of
// longitudes, so a pair straddling +/-180 is not a special case here.
func DistanceMeters(a, b Coordinate) float64 {
	lat1, lat2 := toRadians(a.Lat), toRadians(b.Lat)
	dLat := toRadians(b.Lat - a.Lat)
	dLon := toRadians(b.Lon - a.Lon)
	sinDLat2 := math.Sin(dLat / 2)
	sinDLon2 := math.Sin(dLon / 2)
	h := sinDLat2*sinDLat2 + math.Cos(lat1)*math.Cos(lat2)*sinDLon2*sinDLon2
	h = math.Min(1, math.Max(0, h)) // clamp for float rounding at antipodal points
	c := 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
	return EarthRadiusMeters * c
}

// BearingDegrees returns the initial great-circle bearing from a to b,
// in degrees clockwise from true north, in [0, 360).
func BearingDegrees(a, b Coordinate) float64 {
	lat1, lat2 := toRadians(a.Lat), toRadians(b.Lat)
	dLon := toRadians(b.Lon - a.Lon)
	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)
	brng := math.Atan2(y, x) * 180 / math.Pi
	return math.Mod(brng+360, 360)
}

// TickSeconds converts a tick delta to elapsed seconds. This package
// takes the same "1 tick = 1 second" convention pkg/insurance/casepack
// documents for its own scenario ticks UNLESS a caller passes a
// different tickSeconds scale to ImpliedSpeedKnots directly — see that
// function's tickSeconds parameter.
const DefaultTickSeconds = 1.0

var (
	ErrNonPositiveElapsed = errors.New("geospatial: elapsed time between two fixes must be > 0")
)

// ImpliedSpeedKnots computes the speed implied by two fixes: the
// great-circle distance between them divided by the elapsed time
// (a.Tick to b.Tick, at tickSeconds seconds per tick). Returns an error
// for a non-positive elapsed time (including two fixes at the same tick,
// or b before a) rather than returning +Inf or a negative speed —
// exactly the "impossible chronology" this package's callers (the
// mandate's own §42 Temporal Consistency requirement) must be able to
// detect.
func ImpliedSpeedKnots(a, b Fix, tickSeconds float64) (float64, error) {
	if b.Tick <= a.Tick {
		return 0, ErrNonPositiveElapsed
	}
	elapsedSeconds := float64(b.Tick-a.Tick) * tickSeconds
	if elapsedSeconds <= 0 {
		return 0, ErrNonPositiveElapsed
	}
	meters := DistanceMeters(a.Coordinate, b.Coordinate)
	metersPerSecond := meters / elapsedSeconds
	const metersPerSecondToKnots = 1.9438444924574
	return metersPerSecond * metersPerSecondToKnots, nil
}

// MaxPlausibleVesselSpeedKnots is a configurable ceiling used by
// IsImpossibleSpeed: no ordinary surface vessel exceeds this. Set high
// enough to avoid flagging fast craft while still catching the "AIS fix
// 400nm away 6 minutes later" class of impossibility the mandate names.
// A caller with domain-specific knowledge (e.g. a known hydrofoil fleet)
// passes its own threshold to IsImpossibleSpeedAt instead of relying on
// this default.
const MaxPlausibleVesselSpeedKnots = 60.0

// IsImpossibleSpeed reports whether impliedKnots exceeds
// MaxPlausibleVesselSpeedKnots. This is a PURE physical-plausibility
// signal — it never labels the cause (spoofing, sensor error, relay
// lag); that classification belongs to the truth-arbitration/
// contradiction layer this package feeds, matching the mandate's own
// "Do not treat anomaly as fraud" rule (§67).
func IsImpossibleSpeed(impliedKnots float64) bool {
	return IsImpossibleSpeedAt(impliedKnots, MaxPlausibleVesselSpeedKnots)
}

// IsImpossibleSpeedAt is IsImpossibleSpeed against an explicit ceiling.
func IsImpossibleSpeedAt(impliedKnots, maxPlausibleKnots float64) bool {
	return impliedKnots > maxPlausibleKnots
}

// ---- Route geometry ----

// Route is an ordered polyline of coordinates (e.g. a voyage track or a
// planned route).
type Route []Coordinate

// LengthMeters sums the great-circle distance between every consecutive
// pair of points. A Route of 0 or 1 points has length 0.
func (r Route) LengthMeters() float64 {
	var total float64
	for i := 1; i < len(r); i++ {
		total += DistanceMeters(r[i-1], r[i])
	}
	return total
}

// Validate reports the first invalid Coordinate found, if any.
func (r Route) Validate() error {
	for i, c := range r {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("geospatial: route point %d: %w", i, err)
		}
	}
	return nil
}

// ---- Polygon and containment ----

// Polygon is a closed ring of coordinates (the first and last point are
// implicitly connected; callers do not need to repeat the first point
// at the end).
type Polygon []Coordinate

var ErrDegeneratePolygon = errors.New("geospatial: a polygon needs at least 3 distinct vertices")

// Validate reports whether p has at least 3 vertices and every vertex is
// itself a valid Coordinate.
func (p Polygon) Validate() error {
	if len(p) < 3 {
		return ErrDegeneratePolygon
	}
	for i, c := range p {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("geospatial: polygon vertex %d: %w", i, err)
		}
	}
	return nil
}

// Contains reports whether pt lies inside p, using the standard ray-
// casting (even-odd) algorithm on the polygon's own lat/lon plane.
//
// Antimeridian caveat, stated rather than silently mishandled: this
// algorithm operates on raw longitude values, so a polygon whose
// vertices straddle +/-180 (e.g. a port area spanning the antimeridian)
// must have its vertices expressed in a single continuous longitude
// range (e.g. one side shifted into (180, 360) or (-360, -180)) by the
// CALLER before being passed here — this package does not silently
// reproject such a polygon, because guessing which side to shift could
// silently invert the polygon's own intended area. PortPolygons in
// practice essentially never straddle the antimeridian (no major port
// sits astride it), so this is a documented limitation, not a hidden
// one — TestContainsNearAntimeridianRequiresCallerNormalizedVertices
// demonstrates the caveat directly.
func (p Polygon) Contains(pt Coordinate) bool {
	inside := false
	n := len(p)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		pi, pj := p[i], p[j]
		if ((pi.Lat > pt.Lat) != (pj.Lat > pt.Lat)) &&
			(pt.Lon < (pj.Lon-pi.Lon)*(pt.Lat-pi.Lat)/(pj.Lat-pi.Lat)+pi.Lon) {
			inside = !inside
		}
	}
	return inside
}

// boundingBox returns the min/max lat/lon of p.
func (p Polygon) boundingBox() (minLat, maxLat, minLon, maxLon float64) {
	minLat, maxLat = p[0].Lat, p[0].Lat
	minLon, maxLon = p[0].Lon, p[0].Lon
	for _, c := range p[1:] {
		minLat = math.Min(minLat, c.Lat)
		maxLat = math.Max(maxLat, c.Lat)
		minLon = math.Min(minLon, c.Lon)
		maxLon = math.Max(maxLon, c.Lon)
	}
	return
}

// segmentsIntersect reports whether segment p1-p2 properly or partially
// intersects segment p3-p4, using the standard orientation test.
func segmentsIntersect(p1, p2, p3, p4 Coordinate) bool {
	orient := func(a, b, c Coordinate) float64 {
		return (b.Lon-a.Lon)*(c.Lat-a.Lat) - (b.Lat-a.Lat)*(c.Lon-a.Lon)
	}
	onSegment := func(a, b, c Coordinate) bool {
		return math.Min(a.Lon, b.Lon)-1e-12 <= c.Lon && c.Lon <= math.Max(a.Lon, b.Lon)+1e-12 &&
			math.Min(a.Lat, b.Lat)-1e-12 <= c.Lat && c.Lat <= math.Max(a.Lat, b.Lat)+1e-12
	}
	d1, d2 := orient(p3, p4, p1), orient(p3, p4, p2)
	d3, d4 := orient(p1, p2, p3), orient(p1, p2, p4)
	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) && ((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true
	}
	if d1 == 0 && onSegment(p3, p4, p1) {
		return true
	}
	if d2 == 0 && onSegment(p3, p4, p2) {
		return true
	}
	if d3 == 0 && onSegment(p1, p2, p3) {
		return true
	}
	if d4 == 0 && onSegment(p1, p2, p4) {
		return true
	}
	return false
}

// Intersects reports whether polygons a and b overlap: either polygon
// contains a vertex of the other, or any pair of their edges cross. A
// fast bounding-box rejection runs first, so two clearly separated
// polygons never pay for the full edge scan — the SpatialIntersect
// GEO-001 itself names.
func Intersects(a, b Polygon) bool {
	aMinLat, aMaxLat, aMinLon, aMaxLon := a.boundingBox()
	bMinLat, bMaxLat, bMinLon, bMaxLon := b.boundingBox()
	if aMaxLat < bMinLat || bMaxLat < aMinLat || aMaxLon < bMinLon || bMaxLon < aMinLon {
		return false
	}
	for _, v := range a {
		if b.Contains(v) {
			return true
		}
	}
	for _, v := range b {
		if a.Contains(v) {
			return true
		}
	}
	na, nb := len(a), len(b)
	for i := 0; i < na; i++ {
		a1, a2 := a[i], a[(i+1)%na]
		for j := 0; j < nb; j++ {
			b1, b2 := b[j], b[(j+1)%nb]
			if segmentsIntersect(a1, a2, b1, b2) {
				return true
			}
		}
	}
	return false
}

// IsProximate reports whether a and b are within withinMeters of each
// other — the "spatial proximity" GEO-001 names (e.g. two vessels close
// enough for a plausible STS transfer).
func IsProximate(a, b Coordinate, withinMeters float64) bool {
	return DistanceMeters(a, b) <= withinMeters
}

// ---- Zones / Geofences ----

// ZoneKind classifies the operational-area types GEO-001 names by name:
// port, berth, anchorage, terminal, or a caller-defined operational
// zone. Closed enum, matching this codebase's own discipline for
// classification vocabularies.
type ZoneKind string

const (
	ZoneKindPort        ZoneKind = "PORT"
	ZoneKindBerth       ZoneKind = "BERTH"
	ZoneKindAnchorage   ZoneKind = "ANCHORAGE"
	ZoneKindTerminal    ZoneKind = "TERMINAL"
	ZoneKindOperational ZoneKind = "OPERATIONAL_ZONE"
)

var knownZoneKinds = map[ZoneKind]bool{
	ZoneKindPort: true, ZoneKindBerth: true, ZoneKindAnchorage: true,
	ZoneKindTerminal: true, ZoneKindOperational: true,
}

// IsKnownZoneKind reports whether k is a modelled zone kind.
func IsKnownZoneKind(k ZoneKind) bool { return knownZoneKinds[k] }

// Geofence is a named, typed, optionally TIME-BOUNDED polygon — GEO-001's
// "configurable operational zones" plus "temporal geofence" in one type.
// EffectiveFrom/EffectiveTo == 0 for either means "unbounded on that
// side", matching policy.Version's own EffectiveTo convention (0 = open-
// ended) for consistency across the codebase rather than inventing a
// second "unbounded" convention.
type Geofence struct {
	ID      string   `json:"id"`
	Kind    ZoneKind `json:"kind"`
	Polygon Polygon  `json:"polygon"`

	EffectiveFrom uint64 `json:"effective_from,omitempty"`
	EffectiveTo   uint64 `json:"effective_to,omitempty"`
}

var (
	ErrEmptyGeofenceID      = errors.New("geospatial: Geofence.ID must be non-empty")
	ErrUnknownZoneKind      = errors.New("geospatial: unknown Geofence.Kind")
	ErrGeofenceToBeforeFrom = errors.New("geospatial: Geofence.EffectiveTo must be 0 (open) or >= EffectiveFrom")
)

// Validate checks the geofence's own structural well-formedness.
func (g Geofence) Validate() error {
	if g.ID == "" {
		return ErrEmptyGeofenceID
	}
	if !IsKnownZoneKind(g.Kind) {
		return fmt.Errorf("%w: %q", ErrUnknownZoneKind, g.Kind)
	}
	if err := g.Polygon.Validate(); err != nil {
		return err
	}
	if g.EffectiveTo != 0 && g.EffectiveTo < g.EffectiveFrom {
		return ErrGeofenceToBeforeFrom
	}
	return nil
}

// EffectiveAt reports whether g is in force at tick.
func (g Geofence) EffectiveAt(tick uint64) bool {
	if tick < g.EffectiveFrom {
		return false
	}
	if g.EffectiveTo != 0 && tick > g.EffectiveTo {
		return false
	}
	return true
}

// ContainsAt reports whether pt is inside g AND g is effective at tick —
// the actual "temporal geofence" check GEO-001 names: a coordinate
// inside the polygon of a zone that was not yet, or no longer, in force
// at the relevant tick does not count as being IN that zone.
func (g Geofence) ContainsAt(pt Coordinate, tick uint64) bool {
	return g.EffectiveAt(tick) && g.Polygon.Contains(pt)
}

// ---- Registry ----

var ErrDuplicateGeofence = errors.New("geospatial: Geofence.ID already registered")
var ErrGeofenceNotFound = errors.New("geospatial: Geofence.ID not found")

// Registry holds a named, deterministically-ordered set of geofences —
// e.g. every port/berth/anchorage/terminal/operational zone relevant to
// one voyage or one case.
type Registry struct {
	fences map[string]Geofence
	order  []string
}

// NewRegistry returns an empty geofence registry.
func NewRegistry() *Registry {
	return &Registry{fences: make(map[string]Geofence)}
}

// Register adds a new, already-valid Geofence.
func (r *Registry) Register(g Geofence) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if _, exists := r.fences[g.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateGeofence, g.ID)
	}
	r.fences[g.ID] = g
	r.order = append(r.order, g.ID)
	return nil
}

// Get returns the geofence for id.
func (r *Registry) Get(id string) (Geofence, bool) {
	g, ok := r.fences[id]
	return g, ok
}

// All returns every registered geofence in registration order.
func (r *Registry) All() []Geofence {
	out := make([]Geofence, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.fences[id])
	}
	return out
}

// Containing returns every geofence that contains pt at tick, sorted by
// ID for deterministic output (a point can legitimately sit inside more
// than one zone at once — e.g. a berth inside a port).
func (r *Registry) Containing(pt Coordinate, tick uint64) []Geofence {
	var out []Geofence
	for _, id := range r.order {
		g := r.fences[id]
		if g.ContainsAt(pt, tick) {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
