// Package maritime is vessel-behaviour analysis over position reports.
//
// # What this package is for
//
// The qualification kernel is domain-neutral. This package is one of
// the domain surfaces that sits on top of it, and it exists to make a
// specific point: VERIQO is not an evidence-governance system with
// intelligence bolted on. The kernel's rules only earn their keep when
// something real is being reasoned about, and vessel behaviour is
// where the reasoning is hardest to fake -- positions are numbers,
// physics constrains them, and a claim about a gap in the record is
// checkable in a way a claim about a document rarely is.
//
// # The discipline this package keeps
//
// Every detector here answers a NARROW question and refuses the broad
// one. It reports "these two reports are separated by a distance no
// vessel of this class could cover in the elapsed time". It does NOT
// report "this vessel spoofed its position", because the same
// observation is produced by a receiver clock error, a transposed
// digit, two vessels sharing an identifier, and a deliberate
// falsification -- and choosing between those is a claim about intent
// that needs evidence this package does not have.
//
// So every result carries its alternatives. That is not hedging: an
// anomaly presented without its innocent explanations is an accusation
// wearing the clothes of a measurement.
package maritime

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrBadPosition = errors.New("maritime: position is outside the valid range")
	ErrUnordered   = errors.New("maritime: reports are not in time order")
	ErrTooFew      = errors.New("maritime: not enough reports")
)

// EarthRadiusNM is the mean Earth radius in nautical miles.
//
// It is a sphere, which is wrong: the Earth is an oblate spheroid, and
// great-circle distances computed this way differ from geodesic
// distances by up to roughly 0.3% at the extremes. That error is
// stated here rather than hidden because it matters at the margin --
// a speed just over a threshold may be under it on a better model,
// and this package's job is to be honest about where its answers are
// soft.
const EarthRadiusNM = 3440.065

// Position is one reported position.
type Position struct {
	// LatDeg and LonDeg are degrees, north and east positive.
	LatDeg float64 `json:"lat_deg"`
	LonDeg float64 `json:"lon_deg"`
}

func (p Position) Validate() error {
	if math.IsNaN(p.LatDeg) || math.IsNaN(p.LonDeg) ||
		math.IsInf(p.LatDeg, 0) || math.IsInf(p.LonDeg, 0) {
		return fmt.Errorf("%w: non-finite", ErrBadPosition)
	}
	if p.LatDeg < -90 || p.LatDeg > 90 {
		return fmt.Errorf("%w: latitude %.6f", ErrBadPosition, p.LatDeg)
	}
	if p.LonDeg < -180 || p.LonDeg > 180 {
		return fmt.Errorf("%w: longitude %.6f", ErrBadPosition, p.LonDeg)
	}
	return nil
}

// NullIsland reports whether a position is 0,0.
//
// It has its own function because 0,0 is the single most common
// GARBAGE position in vessel data: it is what an uninitialised field,
// a failed parse and a default struct all produce. Treating it as a
// position in the Gulf of Guinea is a classic way to manufacture an
// implausible-speed alert.
func (p Position) NullIsland() bool {
	return math.Abs(p.LatDeg) < 1e-9 && math.Abs(p.LonDeg) < 1e-9
}

// DistanceNM is the great-circle distance between two positions.
//
// It uses the haversine form, which is numerically well behaved for
// small separations -- the ordinary case here -- where the law of
// cosines loses precision.
func DistanceNM(a, b Position) (float64, error) {
	if err := a.Validate(); err != nil {
		return 0, err
	}
	if err := b.Validate(); err != nil {
		return 0, err
	}
	lat1 := rad(a.LatDeg)
	lat2 := rad(b.LatDeg)
	dLat := lat2 - lat1
	dLon := rad(b.LonDeg - a.LonDeg)

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	// Clamp: accumulated floating-point error can push h just past 1
	// for antipodal points, and Asin of that is NaN.
	if h > 1 {
		h = 1
	}
	return 2 * EarthRadiusNM * math.Asin(math.Sqrt(h)), nil
}

// BearingDeg is the initial great-circle bearing from a to b, in
// degrees clockwise from true north.
func BearingDeg(a, b Position) (float64, error) {
	if err := a.Validate(); err != nil {
		return 0, err
	}
	if err := b.Validate(); err != nil {
		return 0, err
	}
	lat1, lat2 := rad(a.LatDeg), rad(b.LatDeg)
	dLon := rad(b.LonDeg - a.LonDeg)
	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)
	deg := math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
	return deg, nil
}

func rad(d float64) float64 { return d * math.Pi / 180 }

// Report is one position report.
type Report struct {
	// VesselID is the identifier the report was carried under. It is
	// deliberately not called MMSI: an identifier is what was
	// transmitted, and this package never assumes it is the vessel.
	VesselID string    `json:"vessel_id"`
	At       time.Time `json:"at"`
	Position Position  `json:"position"`
	// SpeedKn is speed over ground as REPORTED, which is a separate
	// observation from the speed implied by consecutive positions.
	// Where they disagree, that disagreement is itself a signal.
	SpeedKn *float64 `json:"speed_kn,omitempty"`
	// CourseDeg is course over ground as reported.
	CourseDeg *float64 `json:"course_deg,omitempty"`
	// DraughtM is reported draught. Vessels report it manually, so it
	// is frequently stale and occasionally fictional.
	DraughtM *float64 `json:"draught_m,omitempty"`
	// NavStatus is the reported navigational status, unparsed.
	NavStatus string `json:"nav_status,omitempty"`
	// ReceiverID is which receiver saw it. Terrestrial and satellite
	// receivers have completely different coverage, and a "gap" is
	// often a change of receiver.
	ReceiverID string `json:"receiver_id,omitempty"`
	// EvidenceRef ties the report back to the evidence fabric.
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

func (r Report) Validate() error {
	if strings.TrimSpace(r.VesselID) == "" {
		return errors.New("maritime: a report has no vessel identifier")
	}
	if r.At.IsZero() {
		return errors.New("maritime: a report has no instant")
	}
	if err := r.Position.Validate(); err != nil {
		return err
	}
	if r.SpeedKn != nil && (*r.SpeedKn < 0 || *r.SpeedKn > 120) {
		return fmt.Errorf("maritime: reported speed %.1f kn is outside any plausible range",
			*r.SpeedKn)
	}
	return nil
}

// Track is one identifier's reports, in time order.
type Track struct {
	VesselID string
	Reports  []Report
}

// NewTrack sorts and validates reports.
//
// Sorting rather than refusing out-of-order input is deliberate:
// position feeds arrive out of order routinely, and a package that
// refused them would be unusable. What it will not do is silently drop
// duplicates, because a duplicated report at a different position is
// exactly the signal an identifier collision produces.
func NewTrack(vesselID string, reports []Report) (*Track, error) {
	if strings.TrimSpace(vesselID) == "" {
		return nil, errors.New("maritime: a track has no vessel identifier")
	}
	if len(reports) == 0 {
		return nil, fmt.Errorf("%w: a track needs at least one report", ErrTooFew)
	}
	out := make([]Report, 0, len(reports))
	for _, r := range reports {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		if r.VesselID != vesselID {
			return nil, fmt.Errorf("maritime: report for %s in the track of %s",
				r.VesselID, vesselID)
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return &Track{VesselID: vesselID, Reports: out}, nil
}

// Duration is the span the track covers.
func (t *Track) Duration() time.Duration {
	if len(t.Reports) < 2 {
		return 0
	}
	return t.Reports[len(t.Reports)-1].At.Sub(t.Reports[0].At)
}

// EvidenceRefs returns every distinct evidence reference in the track,
// so a finding drawn from it can cite what it rests on.
func (t *Track) EvidenceRefs() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range t.Reports {
		if r.EvidenceRef == "" || seen[r.EvidenceRef] {
			continue
		}
		seen[r.EvidenceRef] = true
		out = append(out, r.EvidenceRef)
	}
	sort.Strings(out)
	return out
}
