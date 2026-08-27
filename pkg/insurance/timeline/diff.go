// Condition-at-Loading vs Condition-at-Delivery — blueprint §15,
// described there as a "core insurance engine". The blueprint's own
// diagram:
//
//	CONDITION_LOADING
//	        |
//	CUSTODY_EVENTS
//	        |
//	TRANSPORT_EVENTS
//	        |
//	INCIDENT_EVENTS
//	        |
//	CONDITION_DELIVERY
//
// then a "Difference Engine" comparing quantity, quality, packaging,
// seal, temperature, moisture, and physical condition.
//
// Scope discipline (task instructions, and blueprint §16-17's own
// separation of concerns): this file detects and reports WHAT changed
// between the two condition snapshots. It never explains WHY — no
// causation, no hypothesis, no "the carrier likely caused this". That
// is pkg/insurance/causation's job, deliberately a different package.
package timeline

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Stage distinguishes the two condition snapshots the Difference
// Engine compares.
type Stage string

const (
	StageConditionLoading  Stage = "CONDITION_LOADING"
	StageConditionDelivery Stage = "CONDITION_DELIVERY"
)

var knownStages = map[Stage]bool{StageConditionLoading: true, StageConditionDelivery: true}

// IsKnownStage reports whether s is a modelled condition stage.
func IsKnownStage(s Stage) bool { return knownStages[s] }

// Dimension is one of the seven condition dimensions blueprint §15
// names for the Difference Engine to compare.
type Dimension string

const (
	DimensionQuantity          Dimension = "QUANTITY"
	DimensionQuality           Dimension = "QUALITY"
	DimensionPackaging         Dimension = "PACKAGING"
	DimensionSeal              Dimension = "SEAL"
	DimensionTemperature       Dimension = "TEMPERATURE"
	DimensionMoisture          Dimension = "MOISTURE"
	DimensionPhysicalCondition Dimension = "PHYSICAL_CONDITION"
)

// dimensionOrder fixes the deterministic order Compare reports
// dimensions in — the seven blueprint §15 names, in the order it names
// them.
var dimensionOrder = []Dimension{
	DimensionQuantity, DimensionQuality, DimensionPackaging, DimensionSeal,
	DimensionTemperature, DimensionMoisture, DimensionPhysicalCondition,
}

// DimensionValue is one dimension's recorded value at one Stage,
// always tied to the evidence that supports it. Known is the
// authoritative "was this dimension actually observed" marker —
// deliberately a separate field from Value being empty, so a
// zero-value DimensionValue (Known: false) reads unambiguously as
// "insufficient data" rather than "observed to be empty/absent",
// matching blueprint §36's Golden Rule: "the correct output is an
// explicit unresolved marker for that dimension, never a fabricated
// guess or a silently-skipped field."
type DimensionValue struct {
	Value          string   `json:"value,omitempty"`
	SourceEvidence []string `json:"source_evidence,omitempty"`
	Known          bool     `json:"known"`
}

// NewDimensionValue constructs a KNOWN dimension value, tied to at
// least one piece of evidence — an observed condition dimension VICE
// was never told the basis for is not something this package will
// accept as "known" (blueprint §0: "invent missing evidence" is
// forbidden, and an unevidenced dimension value is exactly that).
var ErrDimensionValueNoEvidence = errors.New("timeline: a known DimensionValue must cite at least one source_evidence ID")

func NewDimensionValue(value string, sourceEvidence ...string) (DimensionValue, error) {
	if len(sourceEvidence) == 0 {
		return DimensionValue{}, ErrDimensionValueNoEvidence
	}
	return DimensionValue{Value: value, SourceEvidence: append([]string(nil), sourceEvidence...), Known: true}, nil
}

// UnresolvedDimension is the explicit "insufficient data" marker for a
// dimension VICE has no evidence for at this Stage.
func UnresolvedDimension() DimensionValue { return DimensionValue{Known: false} }

// Condition is one structured snapshot of cargo condition at one
// Stage, tied to source evidence per dimension (and, optionally, an
// overall SourceEvidence list for evidence that supports the snapshot
// as a whole rather than one specific dimension, e.g. a single survey
// report covering everything).
type Condition struct {
	ConditionID    string   `json:"condition_id"`
	Stage          Stage    `json:"stage"`
	RecordedAtTick uint64   `json:"recorded_at_tick"`
	SourceEvidence []string `json:"source_evidence,omitempty"`

	Quantity          DimensionValue `json:"quantity"`
	Quality           DimensionValue `json:"quality"`
	Packaging         DimensionValue `json:"packaging"`
	Seal              DimensionValue `json:"seal"`
	Temperature       DimensionValue `json:"temperature"`
	Moisture          DimensionValue `json:"moisture"`
	PhysicalCondition DimensionValue `json:"physical_condition"`
}

var (
	ErrEmptyConditionID = errors.New("timeline: ConditionID must be non-empty")
	ErrUnknownStage     = errors.New("timeline: unknown Stage")
)

// Validate checks c's own structural validity. It does NOT require
// every dimension to be Known — an incomplete condition snapshot
// (some dimensions unresolved) is a valid, honest Condition; Compare
// (below) is what surfaces the resulting "insufficient data" markers.
func (c Condition) Validate() error {
	if c.ConditionID == "" {
		return ErrEmptyConditionID
	}
	if !IsKnownStage(c.Stage) {
		return fmt.Errorf("%w: %q", ErrUnknownStage, c.Stage)
	}
	return nil
}

// NewCondition constructs a Condition with every dimension defaulted
// to UnresolvedDimension() — matching the Golden Rule's default: never
// silently assume a dimension was observed. Callers set whichever
// dimensions they actually have evidence for on the returned value.
func NewCondition(conditionID string, stage Stage, recordedAtTick uint64, sourceEvidence ...string) (Condition, error) {
	c := Condition{
		ConditionID:       conditionID,
		Stage:             stage,
		RecordedAtTick:    recordedAtTick,
		SourceEvidence:    append([]string(nil), sourceEvidence...),
		Quantity:          UnresolvedDimension(),
		Quality:           UnresolvedDimension(),
		Packaging:         UnresolvedDimension(),
		Seal:              UnresolvedDimension(),
		Temperature:       UnresolvedDimension(),
		Moisture:          UnresolvedDimension(),
		PhysicalCondition: UnresolvedDimension(),
	}
	if err := c.Validate(); err != nil {
		return Condition{}, err
	}
	return c, nil
}

func (c Condition) dimension(d Dimension) DimensionValue {
	switch d {
	case DimensionQuantity:
		return c.Quantity
	case DimensionQuality:
		return c.Quality
	case DimensionPackaging:
		return c.Packaging
	case DimensionSeal:
		return c.Seal
	case DimensionTemperature:
		return c.Temperature
	case DimensionMoisture:
		return c.Moisture
	case DimensionPhysicalCondition:
		return c.PhysicalCondition
	}
	return DimensionValue{}
}

// CustodyEvent, TransportEvent and IncidentEvent are the three
// intermediate stages blueprint §15's own diagram places between
// CONDITION_LOADING and CONDITION_DELIVERY. This package stores them
// as lightweight, evidenced records for the dossier/audit trail; it
// draws no conclusions from them (again: causation is a different
// package's job). Each references a Timeline Event by EventID when one
// exists, rather than duplicating the event's own fields.
type CustodyEvent struct {
	EventID        string   `json:"event_id,omitempty"`
	Holder         string   `json:"holder"`
	Action         string   `json:"action"`
	Tick           uint64   `json:"tick"`
	SourceEvidence []string `json:"source_evidence,omitempty"`
}

type TransportEvent struct {
	EventID        string   `json:"event_id,omitempty"`
	Description    string   `json:"description"`
	Tick           uint64   `json:"tick"`
	SourceEvidence []string `json:"source_evidence,omitempty"`
}

type IncidentEvent struct {
	EventID        string   `json:"event_id,omitempty"`
	Description    string   `json:"description"`
	Tick           uint64   `json:"tick"`
	SourceEvidence []string `json:"source_evidence,omitempty"`
}

// Chain is the full blueprint §15 structure: a loading condition, the
// intermediate events, and a delivery condition. It is pure storage —
// Compare(chain.Loading, chain.Delivery) is the only analysis this
// package performs over it.
type Chain struct {
	Loading         Condition        `json:"loading"`
	CustodyEvents   []CustodyEvent   `json:"custody_events,omitempty"`
	TransportEvents []TransportEvent `json:"transport_events,omitempty"`
	IncidentEvents  []IncidentEvent  `json:"incident_events,omitempty"`
	Delivery        Condition        `json:"delivery"`
}

// Difference is one dimension's comparison result between a Chain's
// Loading and Delivery conditions.
type Difference struct {
	Dimension Dimension      `json:"dimension"`
	Before    DimensionValue `json:"before"` // loading
	After     DimensionValue `json:"after"`  // delivery
	// Resolved is false when either side is unknown — the dimension
	// could not be compared at all, and Changed carries no meaning.
	// This is the dimension-level "insufficient data" marker: never
	// silently skipped, always present in Compare's output.
	Resolved bool `json:"resolved"`
	// Changed is only meaningful when Resolved is true: both sides
	// were known and their values differ. This package draws NO
	// conclusion about WHY — see the file-level doc comment.
	Changed bool   `json:"changed"`
	Note    string `json:"note"`
}

// Compare is the Difference Engine: it ALWAYS returns exactly one
// Difference per dimension, in dimensionOrder, whether or not that
// dimension changed, and whether or not it could even be evaluated.
// Returning a complete, fixed-shape report — rather than only the
// dimensions that happened to change — is deliberate: a caller must
// never have to wonder whether a missing entry means "unchanged" or
// "not even checked".
//
// Value comparison is a trimmed, case-insensitive string equality
// check — deliberately NOT a unit-aware numeric comparison. Normalising
// "-18C" against "-18.0 Celsius" would require this package to own a
// unit-conversion policy it has no basis to invent; a caller that wants
// numeric deltas should normalise DimensionValue.Value into consistent
// units before calling Compare.
func Compare(loading, delivery Condition) []Difference {
	out := make([]Difference, 0, len(dimensionOrder))
	for _, dim := range dimensionOrder {
		before := loading.dimension(dim)
		after := delivery.dimension(dim)
		d := Difference{Dimension: dim, Before: before, After: after}
		switch {
		case !before.Known || !after.Known:
			d.Resolved = false
			d.Changed = false
			d.Note = insufficientDataNote(dim, before.Known, after.Known)
		case valuesDiffer(before.Value, after.Value):
			d.Resolved = true
			d.Changed = true
			d.Note = fmt.Sprintf("%s changed from %q at loading to %q at delivery.", dim, before.Value, after.Value)
		default:
			d.Resolved = true
			d.Changed = false
			d.Note = fmt.Sprintf("%s recorded as %q at both loading and delivery — no difference detected.", dim, before.Value)
		}
		out = append(out, d)
	}
	return out
}

func insufficientDataNote(dim Dimension, loadingKnown, deliveryKnown bool) string {
	switch {
	case !loadingKnown && !deliveryKnown:
		return fmt.Sprintf("INSUFFICIENT_DATA: %s was not recorded at either loading or delivery.", dim)
	case !loadingKnown:
		return fmt.Sprintf("INSUFFICIENT_DATA: %s was not recorded at loading, so it cannot be compared to delivery.", dim)
	default:
		return fmt.Sprintf("INSUFFICIENT_DATA: %s was not recorded at delivery, so it cannot be compared to loading.", dim)
	}
}

// valuesDiffer reports whether two dimension values meaningfully
// differ. It tries a numeric comparison first (stripping a common
// trailing unit suffix such as "C", "%", "KG" and comparing the
// remaining numbers) purely as a best-effort convenience for the
// common case of a bare numeric reading; if either side does not parse
// as a number it falls back to a trimmed, case-insensitive string
// comparison. This is intentionally simple: it is a difference
// detector, not a unit-conversion engine.
func valuesDiffer(a, b string) bool {
	na, oka := parseLeadingNumber(a)
	nb, okb := parseLeadingNumber(b)
	if oka && okb {
		return na != nb
	}
	return !strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func parseLeadingNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] == '-' || s[end] == '+' || s[end] == '.' || (s[end] >= '0' && s[end] <= '9')) {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Dimensions returns the seven dimensions in the fixed order Compare
// reports them.
func Dimensions() []Dimension {
	out := make([]Dimension, len(dimensionOrder))
	copy(out, dimensionOrder)
	return out
}

// UnresolvedDimensions returns, from a Compare result, the dimensions
// that could NOT be compared (either side missing) — the "insufficient
// data" summary a dossier or coverage-issue engine would want without
// re-deriving it from every Difference.
func UnresolvedDimensions(diffs []Difference) []Dimension {
	var out []Dimension
	for _, d := range diffs {
		if !d.Resolved {
			out = append(out, d.Dimension)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ChangedDimensions returns, from a Compare result, the dimensions
// that WERE resolved and differ.
func ChangedDimensions(diffs []Difference) []Dimension {
	var out []Dimension
	for _, d := range diffs {
		if d.Resolved && d.Changed {
			out = append(out, d.Dimension)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
