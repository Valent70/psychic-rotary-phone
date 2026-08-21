// Timeline Conflict Detection — blueprint §14. The blueprint's own
// worked example is the spec for this file's tone:
//
//	Claim: Damage discovered 10:00
//	Photo metadata: 11:45
//	POD: 08:30
//	Status: TEMPORAL_CONFLICT
//	Bukan langsung: FRAUD
//
// ("Not directly: FRAUD.") Every function in this file produces a
// Conflict — a flagged discrepancy for human review. None of them ever
// produce, compute, or imply a guilt/fraud/authenticity verdict; the
// Conflict type structurally has no field for one. Detecting a
// disagreement between sources is this package's job; deciding what it
// means is not (blueprint §0: VICE must never "treat claimant evidence
// as truth" or "treat insurer evidence as truth" — and by the same
// principle, must never treat either as a lie).
package timeline

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"veriqo/pkg/insurance/evidence"
)

// ConflictKind is one of the ten automatic conflict categories
// blueprint §14 names. Ten constants, mapping 1:1 to that list.
type ConflictKind string

const (
	// KindTemporalConflict is category 1, "timestamp conflict" — named
	// KindTemporalConflict (not "TIMESTAMP_CONFLICT") because that is
	// the literal status string the blueprint's own §14 worked example
	// outputs.
	KindTemporalConflict ConflictKind = "TEMPORAL_CONFLICT"
	// KindTimezoneConflict is category 2, "timezone conflict".
	KindTimezoneConflict ConflictKind = "TIMEZONE_CONFLICT"
	// KindImpossibleSequence is category 3, "impossible sequence".
	KindImpossibleSequence ConflictKind = "IMPOSSIBLE_SEQUENCE"
	// KindDuplicateEvent is category 4, "duplicate event".
	KindDuplicateEvent ConflictKind = "DUPLICATE_EVENT"
	// KindMissingInterval is category 5, "missing interval".
	KindMissingInterval ConflictKind = "MISSING_INTERVAL"
	// KindDocumentIssuedAfterClaimedEvent is category 6, "document
	// issued after claimed event".
	KindDocumentIssuedAfterClaimedEvent ConflictKind = "DOCUMENT_ISSUED_AFTER_CLAIMED_EVENT"
	// KindPhotoTimestampMismatch is category 7, "photo timestamp
	// mismatch".
	KindPhotoTimestampMismatch ConflictKind = "PHOTO_TIMESTAMP_MISMATCH"
	// KindSurveyDateMismatch is category 8, "survey date mismatch".
	KindSurveyDateMismatch ConflictKind = "SURVEY_DATE_MISMATCH"
	// KindDeliveryDateMismatch is category 9, "delivery date mismatch".
	KindDeliveryDateMismatch ConflictKind = "DELIVERY_DATE_MISMATCH"
	// KindNoticeBeforeIncident is category 10, "notice submitted
	// before incident".
	KindNoticeBeforeIncident ConflictKind = "NOTICE_SUBMITTED_BEFORE_INCIDENT"
)

var knownConflictKinds = map[ConflictKind]bool{
	KindTemporalConflict: true, KindTimezoneConflict: true, KindImpossibleSequence: true,
	KindDuplicateEvent: true, KindMissingInterval: true, KindDocumentIssuedAfterClaimedEvent: true,
	KindPhotoTimestampMismatch: true, KindSurveyDateMismatch: true, KindDeliveryDateMismatch: true,
	KindNoticeBeforeIncident: true,
}

// IsKnownConflictKind reports whether k is one of the ten modelled kinds.
func IsKnownConflictKind(k ConflictKind) bool { return knownConflictKinds[k] }

// KnownConflictKinds returns all ten kinds in the blueprint's own §14
// list order.
func KnownConflictKinds() []ConflictKind {
	return []ConflictKind{
		KindTemporalConflict, KindTimezoneConflict, KindImpossibleSequence, KindDuplicateEvent,
		KindMissingInterval, KindDocumentIssuedAfterClaimedEvent, KindPhotoTimestampMismatch,
		KindSurveyDateMismatch, KindDeliveryDateMismatch, KindNoticeBeforeIncident,
	}
}

// Conflict is a single flagged discrepancy. It is DESCRIPTIVE ONLY:
// what disagrees, between which events/evidence, and why it was
// flagged. It structurally CANNOT carry a fraud, guilt, or authenticity
// verdict — there is no field for one, matching blueprint §14's
// explicit "Bukan langsung: FRAUD" and §0's absolute prohibition on
// VICE deciding liability.
type Conflict struct {
	// ConflictID is deterministic — derived from Kind and the sorted
	// event IDs involved, never from wall-clock time or randomness, so
	// that running detection twice over the same timeline produces
	// byte-identical output (blueprint §37: "deterministic replay
	// passes").
	ConflictID string       `json:"conflict_id"`
	Kind       ConflictKind `json:"kind"`
	// EventIDs are every timeline Event this conflict was found among.
	EventIDs []string `json:"event_ids"`
	// EvidenceIDs are the underlying evidence.Record IDs (where known)
	// this conflict traces back to — never invented, only ever IDs
	// already present on the involved Events' SourceEvidence or
	// resolved via the evidence registry passed to the Detector.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Description is a plain-language, strictly factual account of the
	// discrepancy. It never assigns fault and never states which of
	// the disagreeing sources (if either) is correct.
	Description string `json:"description"`
	// HumanReviewRequired is always true for a detected Conflict —
	// every one of blueprint §0's mandated outputs includes
	// HUMAN_REVIEW_REQUIRED, and a Conflict is exactly the trigger for it.
	HumanReviewRequired bool `json:"human_review_required"`
}

func conflictID(kind ConflictKind, eventIDs []string) string {
	ids := append([]string(nil), eventIDs...)
	sort.Strings(ids)
	return fmt.Sprintf("CFL-%s-%s", kind, strings.Join(ids, "+"))
}

// Detector runs the automatic conflict-detection sweep. Its two
// configuration fields are deliberate, documented policy choices (not
// blueprint-mandated constants — the blueprint names the categories,
// not their thresholds), exposed so a caller can tune them per
// deployment instead of this package silently guessing what "close
// enough to be the same occurrence" or "too long a gap" means for
// their trade.
type Detector struct {
	// CorrelationWindowSeconds: two events of the SAME EventType whose
	// EventTimeUTC values are within this many seconds of each other
	// are treated as competing accounts of what may be the same
	// real-world occurrence, and are compared for timestamp/timezone
	// disagreement. Events of the same type further apart than this are
	// treated as legitimately distinct occurrences (e.g. two separate
	// SURVEY events months apart) and are never compared against each
	// other.
	CorrelationWindowSeconds uint64
	// MinGapForMissingIntervalSeconds: when two chronologically
	// adjacent events (by EventTimeUTC, across ALL types) are farther
	// apart than this with nothing recorded in between, that gap is
	// flagged as MISSING_INTERVAL.
	MinGapForMissingIntervalSeconds uint64
	// Evidence, if set, lets the Detector resolve SourceEvidence IDs to
	// full evidence.Record values (for DocumentType-based refinements:
	// recognising photo evidence for category 7, and reading
	// Underlying.ObservedAt for category 6). Left nil, those two
	// categories simply find nothing rather than guess — the Golden
	// Rule applied to the detector's own inputs.
	Evidence *evidence.Registry
}

// Default policy thresholds — see the Detector field comments for what
// they mean and why they are configurable rather than fixed.
const (
	DefaultCorrelationWindowSeconds        = 72 * 3600      // 3 days
	DefaultMinGapForMissingIntervalSeconds = 14 * 24 * 3600 // 14 days
)

// NewDetector returns a Detector using the package's default
// thresholds and no evidence registry (categories 6 and 7 will find
// nothing until WithEvidence is used).
func NewDetector() *Detector {
	return &Detector{
		CorrelationWindowSeconds:        DefaultCorrelationWindowSeconds,
		MinGapForMissingIntervalSeconds: DefaultMinGapForMissingIntervalSeconds,
	}
}

// WithEvidence attaches an evidence registry and returns the same
// Detector, for chaining.
func (d *Detector) WithEvidence(reg *evidence.Registry) *Detector {
	d.Evidence = reg
	return d
}

// DetectConflicts runs every category this package implements over tl
// and returns every Conflict found, in a deterministic order (grouped
// by category, in the KnownConflictKinds order; within a category, by
// ConflictID). It never mutates tl.
func (d *Detector) DetectConflicts(tl *Timeline) []Conflict {
	events := tl.Sorted()

	var out []Conflict
	out = append(out, d.detectTemporalFamily(events)...)           // categories 1, 2, 7, 8, 9
	out = append(out, d.detectDuplicates(events)...)               // category 4
	out = append(out, d.detectImpossibleSequence(events)...)       // category 3
	out = append(out, d.detectMissingIntervals(events)...)         // category 5
	out = append(out, d.detectDocumentIssuedAfterEvent(events)...) // category 6
	out = append(out, d.detectNoticeBeforeIncident(events)...)     // category 10

	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := kindOrderIndex(out[i].Kind), kindOrderIndex(out[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return out[i].ConflictID < out[j].ConflictID
	})
	return out
}

var kindOrder = KnownConflictKinds()

func kindOrderIndex(k ConflictKind) int {
	for i, kk := range kindOrder {
		if kk == k {
			return i
		}
	}
	return len(kindOrder)
}

// ---- Categories 1, 2, 7, 8, 9: pairwise comparison of same-type,
// time-correlated events ----
//
// These five categories share one mechanism: find pairs of events of
// the SAME EventType whose claimed times are close enough
// (CorrelationWindowSeconds) to plausibly be independent accounts of
// ONE real occurrence, then report how those accounts disagree. Which
// specific Kind is reported is a dispatch on EventType (and, for
// photos, on evidence DocumentType) — see dispatchTemporalKind.
func (d *Detector) detectTemporalFamily(events []Event) []Conflict {
	var out []Conflict
	byType := make(map[EventType][]Event)
	for _, e := range events {
		byType[e.EventType] = append(byType[e.EventType], e)
	}
	for _, t := range canonicalOrder {
		group := byType[t]
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				a, b := group[i], group[j]
				delta := tickDelta(a.EventTimeUTC, b.EventTimeUTC)
				if delta > d.CorrelationWindowSeconds {
					continue // too far apart to plausibly be the same occurrence
				}
				if delta > 0 {
					kind := d.dispatchTemporalKind(t, a, b)
					out = append(out, Conflict{
						ConflictID:  conflictID(kind, []string{a.EventID, b.EventID}),
						Kind:        kind,
						EventIDs:    []string{a.EventID, b.EventID},
						EvidenceIDs: dedupeStrings(append(append([]string(nil), a.SourceEvidence...), b.SourceEvidence...)),
						Description: fmt.Sprintf(
							"Event %s (%s) reports this occurrence at UTC tick %d (original: %q, tz: %q), "+
								"while event %s (%s) reports the SAME event type at UTC tick %d (original: %q, tz: %q) — "+
								"a difference of %d seconds. This is a flagged discrepancy between independently sourced "+
								"accounts; it is not a determination of which account (if either) is correct.",
							a.EventID, a.EventType, a.EventTimeUTC, a.EventTimeOriginal, a.Timezone,
							b.EventID, b.EventType, b.EventTimeUTC, b.EventTimeOriginal, b.Timezone, delta,
						),
						HumanReviewRequired: true,
					})
				}
				if tz := d.timezoneConflict(a, b); tz != nil {
					out = append(out, *tz)
				}
			}
		}
	}
	return out
}

// dispatchTemporalKind picks which of the five temporal-family
// categories a same-type disagreement between a and b belongs to.
// DELIVERY and SURVEY get their own named category directly from
// EventType (categories 9 and 8). DAMAGE_DISCOVERY gets the more
// specific "photo timestamp mismatch" (category 7) when the Detector
// has an evidence registry AND at least one side's supporting evidence
// is recognisable as a photograph (DocumentType containing "photo",
// case-insensitively — the only signal this package has access to;
// evidence with no DocumentType set, or a registry the Detector wasn't
// given, falls back to the generic category rather than guessing).
// Everything else is the generic category 1, TEMPORAL_CONFLICT.
func (d *Detector) dispatchTemporalKind(t EventType, a, b Event) ConflictKind {
	switch t {
	case TypeDelivery:
		return KindDeliveryDateMismatch
	case TypeSurvey:
		return KindSurveyDateMismatch
	}
	if d.Evidence != nil && (d.hasPhotoEvidence(a) || d.hasPhotoEvidence(b)) {
		return KindPhotoTimestampMismatch
	}
	return KindTemporalConflict
}

func (d *Detector) hasPhotoEvidence(e Event) bool {
	if d.Evidence == nil {
		return false
	}
	for _, id := range e.SourceEvidence {
		rec, ok := d.Evidence.Get(id)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(rec.DocumentType), "photo") {
			return true
		}
	}
	return false
}

// timezoneConflict is category 2: a and b are same-type,
// time-correlated events (the caller already established that) whose
// DECLARED timezones are both parseable by parseUTCOffsetMinutes and
// disagree. This check does NOT require EventTimeUTC to differ —
// two sources can name different timezones and still (coincidentally,
// or because one of them silently already normalised to UTC) agree on
// the resolved instant; the declared-timezone disagreement is flagged
// regardless, because it is itself a factual disagreement about which
// zone applies. A Timezone this package's parser cannot read (e.g. a
// bare IANA name like "Asia/Jakarta") is treated as insufficient data
// for THIS check, never guessed at — see parseUTCOffsetMinutes.
func (d *Detector) timezoneConflict(a, b Event) *Conflict {
	if a.Timezone == "" || b.Timezone == "" || a.Timezone == b.Timezone {
		return nil
	}
	offA, okA := parseUTCOffsetMinutes(a.Timezone)
	offB, okB := parseUTCOffsetMinutes(b.Timezone)
	if !okA || !okB || offA == offB {
		return nil
	}
	c := Conflict{
		Kind:        KindTimezoneConflict,
		EventIDs:    []string{a.EventID, b.EventID},
		EvidenceIDs: dedupeStrings(append(append([]string(nil), a.SourceEvidence...), b.SourceEvidence...)),
		Description: fmt.Sprintf(
			"Event %s declares timezone %q (UTC%+03d:%02d) and event %s declares timezone %q (UTC%+03d:%02d) "+
				"for the same event type within a %ds correlation window — the two accounts disagree about which "+
				"timezone applies; this package does not resolve which (if either) declaration is correct.",
			a.EventID, a.Timezone, offA/60, abs(offA%60), b.EventID, b.Timezone, offB/60, abs(offB%60),
			d.CorrelationWindowSeconds,
		),
		HumanReviewRequired: true,
	}
	c.ConflictID = conflictID(c.Kind, c.EventIDs)
	return &c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// parseUTCOffsetMinutes reads a small set of common numeric UTC-offset
// spellings ("+07:00", "-0500", "UTC", "Z", "GMT+8") into a signed
// offset in minutes from UTC. It deliberately does NOT resolve IANA
// zone names (e.g. "Asia/Jakarta") — that would require an embedded
// timezone database this package has no need to carry, and guessing an
// offset for a name it cannot verify would violate the Golden Rule. A
// Timezone string this function cannot parse returns ok=false, and
// every caller in this file treats that as "insufficient data",
// skipping the check rather than assuming a value.
func parseUTCOffsetMinutes(tz string) (minutes int, ok bool) {
	s := strings.TrimSpace(strings.ToUpper(tz))
	if s == "" {
		return 0, false
	}
	if s == "UTC" || s == "Z" || s == "GMT" {
		return 0, true
	}
	s = strings.TrimPrefix(s, "GMT")
	s = strings.TrimPrefix(s, "UTC")
	if s == "" {
		return 0, true
	}
	sign := 1
	switch s[0] {
	case '+':
		sign = 1
		s = s[1:]
	case '-':
		sign = -1
		s = s[1:]
	default:
		return 0, false
	}
	if s == "" {
		return 0, false
	}
	s = strings.ReplaceAll(s, ":", "")
	var hh, mm int
	var err error
	switch len(s) {
	case 1, 2: // "+7", "+07"
		hh, err = strconv.Atoi(s)
	case 3: // "+7:30" already stripped colon -> "730" ambiguous; treat as H:MM
		hh, err = strconv.Atoi(s[:1])
		if err == nil {
			mm, err = strconv.Atoi(s[1:])
		}
	case 4: // "+0700"
		hh, err = strconv.Atoi(s[:2])
		if err == nil {
			mm, err = strconv.Atoi(s[2:])
		}
	default:
		return 0, false
	}
	if err != nil || hh > 14 || mm > 59 {
		return 0, false
	}
	return sign * (hh*60 + mm), true
}

// ---- Category 4: duplicate event ----
//
// A duplicate is defined here as two DISTINCT Event records (different
// EventID) that agree on EventType, EventTimeUTC, Location, and Actor
// — i.e. every descriptive field a human would use to say "this is the
// same log line" agrees, and only identity/evidence differ. This is
// deliberately stricter than "same type, same time" alone: two
// INDEPENDENT sources that happen to report the same type at the same
// resolved instant but disagree on where or who is corroboration
// working as intended, not a duplicate entry, and must not be flagged
// as one.
func (d *Detector) detectDuplicates(events []Event) []Conflict {
	var out []Conflict
	seen := map[string][]Event{}
	key := func(e Event) string {
		return string(e.EventType) + "|" + strconv.FormatUint(e.EventTimeUTC, 10) + "|" + e.Location + "|" + string(e.Actor)
	}
	for _, e := range events {
		k := key(e)
		for _, prior := range seen[k] {
			c := Conflict{
				Kind:        KindDuplicateEvent,
				EventIDs:    []string{prior.EventID, e.EventID},
				EvidenceIDs: dedupeStrings(append(append([]string(nil), prior.SourceEvidence...), e.SourceEvidence...)),
				Description: fmt.Sprintf(
					"Event %s and event %s report the identical event type (%s), UTC tick (%d), location (%q) and "+
						"actor (%q) under two different EventIDs — flagged as a possible duplicate entry rather than "+
						"two independent corroborating accounts.",
					prior.EventID, e.EventID, e.EventType, e.EventTimeUTC, e.Location, e.Actor,
				),
				HumanReviewRequired: true,
			}
			c.ConflictID = conflictID(c.Kind, c.EventIDs)
			out = append(out, c)
		}
		seen[k] = append(seen[k], e)
	}
	return out
}

// ---- Category 3: impossible sequence ----
//
// Definition used here (the blueprint names the category without
// defining "impossible" — §14 just lists it): this package treats the
// blueprint's own §13 event-type list order (LOADING, HANDOVER,
// DEPARTURE, VOYAGE, INCIDENT, NOTICE, ARRIVAL, DISCHARGE, DELIVERY,
// DAMAGE_DISCOVERY, SURVEY, MITIGATION, CLAIM, RECOVERY) as the
// canonical expected chronological order of a claim's lifecycle. For
// any two events A and B where A's type canonically precedes B's type
// in that list, if A's reconstructed EventTimeUTC is STRICTLY AFTER
// B's, that inversion is flagged.
//
// This is a deliberate simplification, not a claim of formal
// impossibility: real claims can legitimately deviate from this order
// in some cases (MITIGATION can begin immediately after INCIDENT, well
// before ARRIVAL; HANDOVER can occur at either end of a voyage). Per
// the Golden Rule this detector intentionally over-flags such
// deviations as review-worthy — never as proven — discrepancies rather
// than trying to encode every domain exception and risk silently
// missing one.
//
// The one canonical pair this function deliberately SKIPS is
// (INCIDENT, NOTICE): that inversion has its own, more specific
// detector (KindNoticeBeforeIncident, category 10) and is not
// duplicated here.
func (d *Detector) detectImpossibleSequence(events []Event) []Conflict {
	var out []Conflict
	seenPairs := map[string]bool{}
	for i := 0; i < len(events); i++ {
		for j := 0; j < len(events); j++ {
			if i == j {
				continue
			}
			a, b := events[i], events[j]
			if a.EventType == b.EventType {
				continue
			}
			ra, okA := canonicalRank[a.EventType]
			rb, okB := canonicalRank[b.EventType]
			if !okA || !okB || ra >= rb {
				continue // only consider a canonically-precedes-b pairs, once
			}
			if a.EventType == TypeIncident && b.EventType == TypeNotice {
				continue // handled by detectNoticeBeforeIncident instead
			}
			if a.EventTimeUTC <= b.EventTimeUTC {
				continue // no inversion
			}
			pairKey := a.EventID + "->" + b.EventID
			if seenPairs[pairKey] {
				continue
			}
			seenPairs[pairKey] = true
			c := Conflict{
				Kind:        KindImpossibleSequence,
				EventIDs:    []string{a.EventID, b.EventID},
				EvidenceIDs: dedupeStrings(append(append([]string(nil), a.SourceEvidence...), b.SourceEvidence...)),
				Description: fmt.Sprintf(
					"Event %s (%s, UTC tick %d) canonically precedes event %s (%s, UTC tick %d) in the expected "+
						"claim lifecycle order (blueprint §13), but its reconstructed time is LATER — an inversion "+
						"flagged for review. This ordering is a documented simplification and is not itself proof "+
						"the sequence is truly impossible for this claim.",
					a.EventID, a.EventType, a.EventTimeUTC, b.EventID, b.EventType, b.EventTimeUTC,
				),
				HumanReviewRequired: true,
			}
			c.ConflictID = conflictID(c.Kind, c.EventIDs)
			out = append(out, c)
		}
	}
	return out
}

// ---- Category 5: missing interval ----
//
// Definition used here: sort the WHOLE timeline (all event types
// together) by EventTimeUTC. For each pair of chronologically adjacent
// events, if the gap between them is at least
// MinGapForMissingIntervalSeconds, flag it — nothing of any kind was
// recorded to account for that span. This package does not attempt to
// name WHICH stage (custody/transport/incident, per §15's diagram) is
// missing, only that a gap exists; that finer classification needs
// domain knowledge (e.g. which party held custody) this package does
// not have. The blueprint's own adversarial case #19, "Missing custody
// interval", is exactly the scenario this guards: cargo discharged,
// then nothing at all recorded until delivery weeks later.
func (d *Detector) detectMissingIntervals(events []Event) []Conflict {
	if len(events) < 2 {
		return nil
	}
	var out []Conflict
	for i := 0; i+1 < len(events); i++ {
		a, b := events[i], events[i+1]
		if b.EventTimeUTC < a.EventTimeUTC {
			continue // events is already sorted by caller; defensive only
		}
		gap := b.EventTimeUTC - a.EventTimeUTC
		if gap < d.MinGapForMissingIntervalSeconds {
			continue
		}
		c := Conflict{
			Kind:        KindMissingInterval,
			EventIDs:    []string{a.EventID, b.EventID},
			EvidenceIDs: dedupeStrings(append(append([]string(nil), a.SourceEvidence...), b.SourceEvidence...)),
			Description: fmt.Sprintf(
				"No event of any kind is recorded between event %s (%s, UTC tick %d) and event %s (%s, UTC tick %d) "+
					"— a gap of %d seconds with no documented custody, transport, or incident activity in between.",
				a.EventID, a.EventType, a.EventTimeUTC, b.EventID, b.EventType, b.EventTimeUTC, gap,
			),
			HumanReviewRequired: true,
		}
		c.ConflictID = conflictID(c.Kind, c.EventIDs)
		out = append(out, c)
	}
	return out
}

// ---- Category 6: document issued after claimed event ----
//
// Definition used here, chosen to be mechanically exact rather than
// speculative: for every event E and every evidence ID it cites in
// SourceEvidence, resolve that ID (via the Detector's evidence
// registry — if none was attached, this category finds nothing, per
// the Golden Rule) and compare the evidence's own bitemporal
// Underlying.ObservedAt (blueprint §8/pkg/evidence/ontology: "when the
// world was that way", i.e. the date the document itself claims to be
// dated) against E.EventTimeUTC (the event that SAME evidence is being
// relied on to establish). If ObservedAt is STRICTLY BEFORE the
// event's own time, that document claims to be dated before the very
// event it is cited as evidence for — only possible if it was actually
// authored later and mis-dated, matching the spirit of blueprint
// adversarial case #6, "Backdated document".
func (d *Detector) detectDocumentIssuedAfterEvent(events []Event) []Conflict {
	if d.Evidence == nil {
		return nil
	}
	var out []Conflict
	for _, e := range events {
		for _, id := range e.SourceEvidence {
			rec, ok := d.Evidence.Get(id)
			if !ok {
				continue
			}
			observed := rec.Underlying.ObservedAt
			if observed >= e.EventTimeUTC {
				continue
			}
			c := Conflict{
				Kind:        KindDocumentIssuedAfterClaimedEvent,
				EventIDs:    []string{e.EventID},
				EvidenceIDs: []string{id},
				Description: fmt.Sprintf(
					"Evidence %s cited as source for event %s (%s, UTC tick %d) is itself dated (observed_at) at "+
						"tick %d — BEFORE the very event it is offered as evidence for. A document cannot validly "+
						"predate the event it documents; this is flagged, not resolved.",
					id, e.EventID, e.EventType, e.EventTimeUTC, observed,
				),
				HumanReviewRequired: true,
			}
			c.ConflictID = conflictID(c.Kind, []string{e.EventID, id})
			out = append(out, c)
		}
	}
	return out
}

// ---- Category 10: notice submitted before incident ----
//
// Direct, cross-type check: every NOTICE event's EventTimeUTC compared
// against every INCIDENT event's EventTimeUTC in the same timeline. A
// notice describing an incident cannot validly predate it.
func (d *Detector) detectNoticeBeforeIncident(events []Event) []Conflict {
	var notices, incidents []Event
	for _, e := range events {
		switch e.EventType {
		case TypeNotice:
			notices = append(notices, e)
		case TypeIncident:
			incidents = append(incidents, e)
		}
	}
	var out []Conflict
	for _, n := range notices {
		for _, inc := range incidents {
			if n.EventTimeUTC >= inc.EventTimeUTC {
				continue
			}
			c := Conflict{
				Kind:        KindNoticeBeforeIncident,
				EventIDs:    []string{n.EventID, inc.EventID},
				EvidenceIDs: dedupeStrings(append(append([]string(nil), n.SourceEvidence...), inc.SourceEvidence...)),
				Description: fmt.Sprintf(
					"Notice event %s is timestamped at UTC tick %d, before incident event %s at UTC tick %d — a "+
						"notice cannot validly predate the incident it reports.",
					n.EventID, n.EventTimeUTC, inc.EventID, inc.EventTimeUTC,
				),
				HumanReviewRequired: true,
			}
			c.ConflictID = conflictID(c.Kind, c.EventIDs)
			out = append(out, c)
		}
	}
	return out
}

// ---- small shared helpers ----

func tickDelta(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
