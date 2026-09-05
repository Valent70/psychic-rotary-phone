package maritime

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Anomaly is something worth a person's attention, with everything
// needed to dismiss it.
//
// Alternatives is not optional and not decoration. An anomaly
// presented without its innocent explanations is an accusation wearing
// the clothes of a measurement, and an analyst who sees a hundred of
// those learns to ignore all of them.
type Anomaly struct {
	Kind string `json:"kind"`
	// VesselID is the identifier, never "the vessel".
	VesselID string `json:"vessel_id"`
	// From and To bound the observation in time.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Observation states what was measured, in units.
	Observation string `json:"observation"`
	// Alternatives are the innocent explanations that produce the same
	// observation, ordered from most to least mundane.
	Alternatives []string `json:"alternatives"`
	// Diagnostic is what evidence would separate the explanations.
	// This is the reverse-proof discipline applied to a detector.
	Diagnostic string `json:"diagnostic"`
	// EvidenceRefs are the reports the observation rests on.
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	// Magnitude lets a caller rank without implying a probability.
	// It is deliberately unitless and deliberately not a score: a
	// number that looked like a confidence would be used as one.
	Magnitude float64 `json:"magnitude"`
}

func (a Anomaly) String() string {
	return fmt.Sprintf("%s [%s] %s", a.Kind, a.VesselID, a.Observation)
}

// SpeedLimits bounds what a hull can do.
//
// The defaults are deliberately generous. A detector tuned to catch
// every anomaly catches mostly noise, and the cost of a false alarm
// here is an analyst's hour; the cost of a systematically over-tuned
// detector is that nobody reads the output.
type SpeedLimits struct {
	// MaxSustainedKn is the highest speed the class can hold. 25 kn
	// covers almost every merchant vessel; container ships are the
	// fast end and tankers and bulkers are far slower.
	MaxSustainedKn float64
	// MaxBurstKn allows for a short sprint and for ordinary position
	// error over a short interval.
	MaxBurstKn float64
	// BurstWindow is how long a burst may last.
	BurstWindow time.Duration
	// PositionErrorNM is the assumed error in a single report. Over a
	// short interval this dominates: two reports a minute apart with
	// 0.5 NM of error can imply 30 kn of motion that never happened.
	PositionErrorNM float64
}

// DefaultSpeedLimits are merchant-vessel defaults.
func DefaultSpeedLimits() SpeedLimits {
	return SpeedLimits{
		MaxSustainedKn:  25,
		MaxBurstKn:      40,
		BurstWindow:     15 * time.Minute,
		PositionErrorNM: 0.5,
	}
}

// ImplausibleSpeed finds consecutive pairs that no hull could have
// covered.
//
// # The correction that makes this usable
//
// The naive computation -- distance over elapsed time -- produces
// enormous false speeds for closely spaced reports, because the
// position error does not shrink with the interval. Two reports ten
// seconds apart, each accurate to half a nautical mile, can differ by
// a nautical mile with the vessel stationary: 360 knots.
//
// So the implied speed is computed after subtracting the assumed
// position error from the distance. A pair whose distance is within
// the error budget implies nothing at all and is skipped rather than
// reported at zero.
func (t *Track) ImplausibleSpeed(lim SpeedLimits) []Anomaly {
	var out []Anomaly
	for i := 1; i < len(t.Reports); i++ {
		a, b := t.Reports[i-1], t.Reports[i]
		dt := b.At.Sub(a.At)
		if dt <= 0 {
			// Two reports at the same instant in different places is
			// its own signal, handled by IdentifierCollision.
			continue
		}
		if a.Position.NullIsland() || b.Position.NullIsland() {
			continue // handled by NullPosition
		}
		d, err := DistanceNM(a.Position, b.Position)
		if err != nil {
			continue
		}
		// Subtract the error budget: what is left is motion that
		// cannot be explained by measurement error.
		explained := 2 * lim.PositionErrorNM
		if d <= explained {
			continue
		}
		hours := dt.Hours()
		implied := (d - explained) / hours

		limit := lim.MaxSustainedKn
		if dt <= lim.BurstWindow {
			limit = lim.MaxBurstKn
		}
		if implied <= limit {
			continue
		}
		out = append(out, Anomaly{
			Kind: "IMPLAUSIBLE_SPEED", VesselID: t.VesselID, From: a.At, To: b.At,
			Observation: fmt.Sprintf(
				"%.1f NM in %s implies %.1f kn after allowing %.1f NM for position error; "+
					"the limit applied is %.0f kn",
				d, dt.Round(time.Second), implied, explained, limit),
			Alternatives: []string{
				"two vessels are transmitting the same identifier, so these are two tracks " +
					"interleaved rather than one vessel moving",
				"a transposed or dropped digit in one position",
				"a receiver clock error placing a report at the wrong instant",
				"a stale report replayed by an aggregator with a fresh timestamp",
				"a deliberately falsified position",
			},
			Diagnostic: "a third report between these two, from a different receiver, " +
				"distinguishes an interleaved second vessel from a single implausible leg",
			EvidenceRefs: refs(a, b),
			Magnitude:    implied / limit,
		})
	}
	return out
}

// GapPolicy configures dark-period detection.
type GapPolicy struct {
	// MinGap is how long a silence must be before it is reported.
	MinGap time.Duration
	// ExpectedInterval is how often reports normally arrive for this
	// vessel and receiver mix. A gap is only meaningful relative to a
	// rate, and the rate differs by an order of magnitude between
	// terrestrial and satellite coverage.
	ExpectedInterval time.Duration
	// CoastalOnly, when set, marks that the observing network has no
	// offshore coverage, in which case a mid-ocean gap is the expected
	// behaviour of the RECEIVER and not of the vessel.
	CoastalOnly bool
}

// DefaultGapPolicy is a conservative default.
func DefaultGapPolicy() GapPolicy {
	return GapPolicy{MinGap: 6 * time.Hour, ExpectedInterval: 10 * time.Minute}
}

// Gaps finds silences in the record.
//
// # The thing this detector must never say
//
// A gap in the record is a fact about the RECORD. "The vessel went
// dark" is a claim about the vessel and about intent, and the two are
// separated by every receiver outage, satellite revisit interval,
// coverage hole, antenna fault and power failure that has ever
// occurred. This detector reports the first and refuses the second,
// and the alternatives list says so at every occurrence rather than
// once in a preamble.
func (t *Track) Gaps(p GapPolicy) []Anomaly {
	var out []Anomaly
	for i := 1; i < len(t.Reports); i++ {
		a, b := t.Reports[i-1], t.Reports[i]
		dt := b.At.Sub(a.At)
		if dt < p.MinGap {
			continue
		}
		alts := []string{
			"the observing network has no coverage where the vessel was; a gap in the " +
				"record is a fact about the record, not about the vessel",
			"a receiver or backhaul outage",
			"a satellite revisit interval longer than the gap",
			"an equipment fault or a power failure aboard",
			"the transponder was switched off, which is lawful in some circumstances and " +
				"required in others",
		}
		if p.CoastalOnly {
			alts = append([]string{
				"the observing network is terrestrial only and this position is offshore, " +
					"where a gap is the EXPECTED behaviour of the receiver",
			}, alts...)
		}
		mult := 1.0
		if p.ExpectedInterval > 0 {
			mult = dt.Seconds() / p.ExpectedInterval.Seconds()
		}
		out = append(out, Anomaly{
			Kind: "REPORTING_GAP", VesselID: t.VesselID, From: a.At, To: b.At,
			Observation: fmt.Sprintf(
				"no report for %s, against an expected interval of %s (%.0fx). The last "+
					"position was %.4f, %.4f and the next was %.4f, %.4f",
				dt.Round(time.Minute), p.ExpectedInterval, mult,
				a.Position.LatDeg, a.Position.LonDeg, b.Position.LatDeg, b.Position.LonDeg),
			Alternatives: alts,
			Diagnostic: "coverage data for the observing network over this area and period " +
				"separates a receiver gap from a transmitter gap; another vessel's " +
				"continuous track through the same water in the same window is the " +
				"cheapest form of it",
			EvidenceRefs: refs(a, b),
			Magnitude:    mult,
		})
	}
	return out
}

// NullPositions finds reports at 0,0.
func (t *Track) NullPositions() []Anomaly {
	var out []Anomaly
	for _, r := range t.Reports {
		if !r.Position.NullIsland() {
			continue
		}
		out = append(out, Anomaly{
			Kind: "NULL_POSITION", VesselID: t.VesselID, From: r.At, To: r.At,
			Observation: "the report carries position 0,0",
			Alternatives: []string{
				"an uninitialised or default field, which is overwhelmingly the usual cause",
				"a failed parse upstream",
				"the vessel is genuinely in the Gulf of Guinea near 0,0",
			},
			Diagnostic:   "the surrounding reports show whether a track passes anywhere near 0,0",
			EvidenceRefs: refs(r),
			Magnitude:    1,
		})
	}
	return out
}

// IdentifierCollision finds evidence that one identifier is carrying
// more than one vessel.
//
// This is separated from ImplausibleSpeed deliberately. An implausible
// speed is a measurement; a collision is a HYPOTHESIS that explains a
// pattern of them, and promoting the one to the other requires the
// pattern -- a single implausible leg is much more likely to be a bad
// position.
func (t *Track) IdentifierCollision(lim SpeedLimits) []Anomaly {
	fast := t.ImplausibleSpeed(lim)
	// Simultaneous reports from different places are the strongest
	// single indicator, because no measurement error explains them.
	var simultaneous int
	for i := 1; i < len(t.Reports); i++ {
		a, b := t.Reports[i-1], t.Reports[i]
		if !a.At.Equal(b.At) {
			continue
		}
		d, err := DistanceNM(a.Position, b.Position)
		if err == nil && d > 2*lim.PositionErrorNM {
			simultaneous++
		}
	}
	if simultaneous == 0 && len(fast) < 3 {
		return nil
	}
	var refs []string
	for _, f := range fast {
		refs = append(refs, f.EvidenceRefs...)
	}
	sort.Strings(refs)
	return []Anomaly{{
		Kind: "IDENTIFIER_COLLISION", VesselID: t.VesselID,
		From: t.Reports[0].At, To: t.Reports[len(t.Reports)-1].At,
		Observation: fmt.Sprintf(
			"%d implausible leg(s) and %d simultaneous report(s) from separated positions. "+
				"A single bad position produces one implausible leg; a pattern of them, "+
				"and any simultaneity, is what two vessels sharing an identifier looks like",
			len(fast), simultaneous),
		Alternatives: []string{
			"two vessels transmitting the same identifier, whether by misconfiguration or " +
				"deliberately",
			"an aggregator merging two feeds under one key",
			"a systematically faulty receiver injecting positions from elsewhere",
		},
		Diagnostic: "static data -- name, dimensions, call sign -- differing between the " +
			"two clusters would settle it; identical static data would not, because that " +
			"is what a deliberate impersonation copies first",
		EvidenceRefs: dedupe(refs),
		Magnitude:    float64(len(fast) + 3*simultaneous),
	}}
}

// Rendezvous is two identifiers close together for a sustained period.
//
// It is emitted as an observation about PROXIMITY, never as a
// ship-to-ship transfer. Two vessels can be close because they are
// transferring cargo, because they are queuing for a berth, because
// they are anchored in the same anchorage, because they passed, or
// because one of the positions is wrong.
type Rendezvous struct {
	A        string        `json:"a"`
	B        string        `json:"b"`
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	Duration time.Duration `json:"duration"`
	// ClosestNM is the minimum separation observed.
	ClosestNM float64 `json:"closest_nm"`
	// Samples is how many paired observations support it. One pair of
	// reports is a coincidence; forty over six hours is a pattern.
	Samples      int      `json:"samples"`
	Alternatives []string `json:"alternatives"`
	Diagnostic   string   `json:"diagnostic"`
}

// RendezvousPolicy configures proximity detection.
type RendezvousPolicy struct {
	// WithinNM is how close counts.
	WithinNM float64
	// MinDuration is how long they must stay close.
	MinDuration time.Duration
	// PairWindow is how far apart two reports may be in time and still
	// be compared. Comparing positions an hour apart and calling them
	// simultaneous is how a passing vessel becomes a rendezvous.
	PairWindow time.Duration
	// MaxSpeedKn, when set above zero, requires both vessels to be
	// slow. A transfer at 15 knots is not a transfer.
	MaxSpeedKn float64
}

func DefaultRendezvousPolicy() RendezvousPolicy {
	return RendezvousPolicy{WithinNM: 0.5, MinDuration: 2 * time.Hour,
		PairWindow: 15 * time.Minute, MaxSpeedKn: 3}
}

// FindRendezvous compares two tracks.
func FindRendezvous(a, b *Track, p RendezvousPolicy) []Rendezvous {
	if a == nil || b == nil || a.VesselID == b.VesselID {
		return nil
	}
	type pair struct {
		at time.Time
		d  float64
	}
	var close []pair
	j := 0
	for _, ra := range a.Reports {
		// Advance j to the first b-report not before ra's window.
		for j < len(b.Reports) && b.Reports[j].At.Before(ra.At.Add(-p.PairWindow)) {
			j++
		}
		for k := j; k < len(b.Reports); k++ {
			rb := b.Reports[k]
			if rb.At.After(ra.At.Add(p.PairWindow)) {
				break
			}
			if ra.Position.NullIsland() || rb.Position.NullIsland() {
				continue
			}
			if p.MaxSpeedKn > 0 {
				if ra.SpeedKn != nil && *ra.SpeedKn > p.MaxSpeedKn {
					continue
				}
				if rb.SpeedKn != nil && *rb.SpeedKn > p.MaxSpeedKn {
					continue
				}
			}
			d, err := DistanceNM(ra.Position, rb.Position)
			if err != nil || d > p.WithinNM {
				continue
			}
			close = append(close, pair{at: ra.At, d: d})
			break
		}
	}
	if len(close) < 2 {
		return nil
	}
	sort.Slice(close, func(i, j int) bool { return close[i].at.Before(close[j].at) })

	var out []Rendezvous
	start := 0
	flush := func(end int) {
		if end-start < 2 {
			return
		}
		dur := close[end-1].at.Sub(close[start].at)
		if dur < p.MinDuration {
			return
		}
		min := close[start].d
		for _, c := range close[start:end] {
			if c.d < min {
				min = c.d
			}
		}
		out = append(out, Rendezvous{
			A: a.VesselID, B: b.VesselID, From: close[start].at, To: close[end-1].at,
			Duration: dur, ClosestNM: min, Samples: end - start,
			Alternatives: []string{
				"both are anchored in the same anchorage or queuing for the same berth",
				"they are alongside in port, or in the same lock or canal transit",
				"one or both positions are wrong",
				"a ship-to-ship transfer",
			},
			Diagnostic: "a draught change in one or both vessels across the window, or a " +
				"port-call record placing them alongside, separates a transfer from " +
				"co-location",
		})
	}
	for i := 1; i < len(close); i++ {
		// A break longer than four pair-windows ends a rendezvous.
		if close[i].at.Sub(close[i-1].at) > 4*p.PairWindow {
			flush(i)
			start = i
		}
	}
	flush(len(close))
	return out
}

// DraughtChange reports a change in reported draught across a window.
//
// Draught is entered by hand, so it is stale as often as it is wrong.
// A change is still one of the few observable proxies for cargo
// movement, which is why it earns a detector -- with the manual-entry
// problem stated at every occurrence.
func (t *Track) DraughtChange(minDelta float64) []Anomaly {
	var last *Report
	var out []Anomaly
	for i := range t.Reports {
		r := t.Reports[i]
		if r.DraughtM == nil {
			continue
		}
		if last == nil {
			last = &t.Reports[i]
			continue
		}
		delta := *r.DraughtM - *last.DraughtM
		if math.Abs(delta) < minDelta {
			continue
		}
		dir := "increased"
		if delta < 0 {
			dir = "decreased"
		}
		out = append(out, Anomaly{
			Kind: "DRAUGHT_CHANGE", VesselID: t.VesselID, From: last.At, To: r.At,
			Observation: fmt.Sprintf("reported draught %s by %.2f m (from %.2f to %.2f) "+
				"over %s", dir, math.Abs(delta), *last.DraughtM, *r.DraughtM,
				r.At.Sub(last.At).Round(time.Hour)),
			Alternatives: []string{
				"the previous value was stale and this is a correction, which is the most " +
					"common cause: draught is entered by hand and often not updated on sailing",
				"ballast operations rather than cargo",
				"a data-entry error in either value",
				"cargo was loaded or discharged",
			},
			Diagnostic: "a port call between the two reports, or a survey report, " +
				"distinguishes a cargo operation from a correction",
			EvidenceRefs: refs(*last, r),
			Magnitude:    math.Abs(delta),
		})
		last = &t.Reports[i]
	}
	return out
}

// ReportedSpeedDisagreement finds reports whose transmitted speed
// disagrees with the speed implied by the surrounding positions.
//
// It is a distinct signal from implausible speed: here the vessel's
// own two observations of itself do not agree, which no coverage gap
// or receiver problem explains.
func (t *Track) ReportedSpeedDisagreement(toleranceKn float64, lim SpeedLimits) []Anomaly {
	var out []Anomaly
	for i := 1; i < len(t.Reports); i++ {
		a, b := t.Reports[i-1], t.Reports[i]
		if a.SpeedKn == nil || b.SpeedKn == nil {
			continue
		}
		dt := b.At.Sub(a.At)
		if dt <= 0 || dt > time.Hour {
			continue
		}
		if a.Position.NullIsland() || b.Position.NullIsland() {
			continue
		}
		d, err := DistanceNM(a.Position, b.Position)
		if err != nil {
			continue
		}
		explained := 2 * lim.PositionErrorNM
		if d <= explained {
			d = 0
		} else {
			d -= explained
		}
		implied := d / dt.Hours()
		mean := (*a.SpeedKn + *b.SpeedKn) / 2
		diff := math.Abs(implied - mean)
		if diff <= toleranceKn {
			continue
		}
		out = append(out, Anomaly{
			Kind: "SPEED_DISAGREEMENT", VesselID: t.VesselID, From: a.At, To: b.At,
			Observation: fmt.Sprintf(
				"positions imply %.1f kn and the reports transmit a mean of %.1f kn, a "+
					"difference of %.1f kn", implied, mean, diff),
			Alternatives: []string{
				"a strong current or tide, which moves the vessel without changing its " +
					"speed through the water",
				"one position is in error",
				"the speed field is not being updated",
				"the position field is being falsified while the speed field is not",
			},
			Diagnostic: "current data for the area and period accounts for the ordinary " +
				"case; a course that also disagrees points the other way",
			EvidenceRefs: refs(a, b),
			Magnitude:    diff,
		})
	}
	return out
}

func refs(rs ...Report) []string {
	var out []string
	for _, r := range rs {
		if r.EvidenceRef != "" {
			out = append(out, r.EvidenceRef)
		}
	}
	return dedupe(out)
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
