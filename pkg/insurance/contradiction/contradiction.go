// Package contradiction is VICE's insurance-domain Contradiction Engine
// — blueprint §25, explicitly named a P0 priority ("Ini salah satu
// P0"). It does NOT reimplement contradiction detection or arbitration:
// the blueprint's own instruction (§1) is "Jangan duplikasi ... yang
// sudah ada. VICE menggunakan engine yang sudah ada", and a real,
// already-tested arbitration engine already exists at
// pkg/moat/contradiction (ArbitrationEngine: Observation -> Evidence ->
// Conflict Detection -> Conflict Graph -> Confidence Update -> Evidence
// Weighting -> Truth Arbitration -> Truth Version, plus its
// ConflictRecords/ResolutionState projection). This package is a thin
// insurance-domain adapter over that engine: it ingests
// pkg/insurance/evidence.Record values as claims/observations into a
// real *contradiction.ArbitrationEngine (aliased "moat" below to avoid
// confusion with this package's own name — the two are intentionally
// same-named per the task's own instruction, since callers importing
// both will alias one), and projects the engine's real output into the
// exact shape §25's own worked example names:
//
//	{
//	  "contradiction_id": "CX-001",
//	  "severity": "HIGH",
//	  "evidence_a": "EV-001",
//	  "evidence_b": "EV-009",
//	  "conflict": "Delivery time differs by 4 hours",
//	  "resolution": "UNRESOLVED",
//	  "human_review_required": true
//	}
//
// §25 names ten pair relationships this engine must be able to detect
// contradictions across: document<->document, document<->timeline,
// document<->AIS, document<->survey, photo<->statement,
// invoice<->quantum, policy<->claim, claimant<->insurer,
// claimant<->carrier, and surveyor<->raw evidence. This package cannot
// re-derive AIS, timeline, or quantum semantics itself — those stay in
// their own packages (blueprint §33's package structure). Instead
// ContradictionRecord carries a PairKind naming WHICH of the ten
// relationship types a given finding represents, while the actual
// comparison mechanics ride entirely on the real ArbitrationEngine:
// nearly every one of those ten pairs reduces to "two sources assert
// different values for the same claim key", which is exactly what
// ArbitrationEngine.ConflictRecords already computes.
//
// Golden rules this package holds itself to (blueprint §36, applied
// concretely below):
//   - Never convert uncertainty into certainty: Detect never invents a
//     winner. Resolution is either ARBITRATED (the real engine reached
//     a >=2/3-confidence winner) or UNRESOLVED (it did not) — mapped
//     directly from moat's own ResolutionState, never re-decided here.
//   - Never treat source authority (Origin) or declared interest
//     (evidence.SourceInterest) as truth: neither field is ever read by
//     any branching logic in this file. See TestSubmitAndDetectAreOriginBlind
//     and TestDeclaredInterestNeverPicksAWinner.
//   - Never double-count dependent evidence: this package performs no
//     naive len(evidence) corroboration counting; see
//     IndependentSupportCount in three_party.go, which defers entirely
//     to evidence.DependencyGraph.IndependentCount.
//   - When evidence is insufficient, fail closed (return an error, or
//     UNRESOLVED/human_review_required=true) — never a guessed
//     conclusion.
package contradiction

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	moat "veriqo/pkg/moat/contradiction"

	"veriqo/pkg/insurance/evidence"
)

// Severity is a closed, three-level severity enum. Three levels — not
// a raw confidence float, and not more than three — because that is
// the same granularity this repo already uses for exactly this kind of
// judgement (evidence.ContradictionLevelRating: NONE/LOW/MEDIUM/HIGH;
// evidence.RelevanceRating and evidence.CorroborationRating:
// HIGH/MEDIUM/LOW). A finer scale would imply a precision the
// underlying arbitration confidence does not actually carry; a coarser
// one (just "contested or not") would lose the real, load-bearing
// distinction between "arbitration reached a decisive majority" and
// "arbitration reached a majority, but only just" — see
// severityFromConflict below for exactly how each band is derived from
// the real ArbitrationEngine's own Confidence output.
type Severity string

const (
	SeverityLow    Severity = "LOW"
	SeverityMedium Severity = "MEDIUM"
	SeverityHigh   Severity = "HIGH"
)

var knownSeverities = map[Severity]bool{SeverityLow: true, SeverityMedium: true, SeverityHigh: true}

// IsKnownSeverity reports whether s is a modelled severity level.
func IsKnownSeverity(s Severity) bool { return knownSeverities[s] }

// Resolution mirrors moat's ResolutionState in this package's own
// vocabulary, restricted to the two outcomes that can ever reach a
// caller here (an UNCONTESTED claim never produces a ContradictionRecord
// at all — see Detect): ARBITRATED when the real engine found a
// confident (>=2/3) winner despite the disagreement, UNRESOLVED
// otherwise. The blueprint's own worked example (§25) uses
// "UNRESOLVED" literally; ARBITRATED is an additive value for the case
// the blueprint itself distinguishes in §12 ("PARTIALLY_SUPPORTED" /
// resolved-with-visible-disagreement) — it is never silent: Sources
// and Values remain fully enumerable via the wrapped engine either way.
type Resolution string

const (
	// ResolutionArbitrated: sources disagreed, but the real
	// ArbitrationEngine's weighted evidence reached a confident (>=2/3)
	// winner. The disagreement is still reported (this IS a
	// ContradictionRecord), never hidden.
	ResolutionArbitrated Resolution = "ARBITRATED"
	// ResolutionUnresolved: sources disagreed and the real engine's
	// confidence in any winner is below the confident-arbitration bar,
	// or tied. Always paired with HumanReviewRequired=true.
	ResolutionUnresolved Resolution = "UNRESOLVED"
)

// PairKind names which of blueprint §25's ten contradiction
// relationship types a ContradictionRecord represents. This package
// never inspects a PairKind to change how it computes a contradiction —
// it is caller-supplied labelling for a Detect() call the caller
// already knows the semantic category of (e.g. the timeline package
// calls Detect with PairDocumentTimeline; VICE's contradiction package
// itself has no notion of what a timeline is).
type PairKind string

const (
	PairDocumentDocument    PairKind = "DOCUMENT_DOCUMENT"
	PairDocumentTimeline    PairKind = "DOCUMENT_TIMELINE"
	PairDocumentAIS         PairKind = "DOCUMENT_AIS"
	PairDocumentSurvey      PairKind = "DOCUMENT_SURVEY"
	PairPhotoStatement      PairKind = "PHOTO_STATEMENT"
	PairInvoiceQuantum      PairKind = "INVOICE_QUANTUM"
	PairPolicyClaim         PairKind = "POLICY_CLAIM"
	PairClaimantInsurer     PairKind = "CLAIMANT_INSURER"
	PairClaimantCarrier     PairKind = "CLAIMANT_CARRIER"
	PairSurveyorRawEvidence PairKind = "SURVEYOR_RAW_EVIDENCE"
)

var knownPairKinds = map[PairKind]bool{
	PairDocumentDocument: true, PairDocumentTimeline: true, PairDocumentAIS: true,
	PairDocumentSurvey: true, PairPhotoStatement: true, PairInvoiceQuantum: true,
	PairPolicyClaim: true, PairClaimantInsurer: true, PairClaimantCarrier: true,
	PairSurveyorRawEvidence: true,
}

// IsKnownPairKind reports whether k is one of §25's ten modelled pair
// relationship types.
func IsKnownPairKind(k PairKind) bool { return knownPairKinds[k] }

// KnownPairKinds returns every modelled PairKind in deterministic order.
func KnownPairKinds() []PairKind {
	out := make([]PairKind, 0, len(knownPairKinds))
	for k := range knownPairKinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ContradictionRecord is this package's output shape: the seven fields
// named verbatim in blueprint §25's own worked JSON example
// (contradiction_id, severity, evidence_a, evidence_b, conflict,
// resolution, human_review_required), plus two additive groups this
// package needs to stay generic and neutrality-compliant:
//
//   - Category names which of §25's ten pair relationship types this
//     finding represents (see PairKind).
//   - EvidenceASourceInterest / EvidenceBSourceInterest surface each
//     side's already-computed blueprint §11 conflict-of-interest
//     context (evidence.SourceInterest) next to the finding, exactly as
//     §11 requires: "VICE hanya menampilkan: source_interest,
//     relationship, potential_conflict dan membiarkan reviewer
//     menilai." These fields are informational ONLY. Severity,
//     Resolution and HumanReviewRequired above are computed from the
//     real ArbitrationEngine's weighted evidence and are never read
//     back from Interest — see TestDeclaredInterestNeverPicksAWinner.
type ContradictionRecord struct {
	ContradictionID     string     `json:"contradiction_id"`
	Severity            Severity   `json:"severity"`
	EvidenceA           string     `json:"evidence_a"`
	EvidenceB           string     `json:"evidence_b"`
	Conflict            string     `json:"conflict"`
	Resolution          Resolution `json:"resolution"`
	HumanReviewRequired bool       `json:"human_review_required"`
	Category            PairKind   `json:"category"`

	EvidenceASourceInterest *evidence.SourceInterest `json:"evidence_a_source_interest,omitempty"`
	EvidenceBSourceInterest *evidence.SourceInterest `json:"evidence_b_source_interest,omitempty"`
}

var (
	ErrEmptyClaimKey      = errors.New("contradiction: claim key must be non-empty")
	ErrEmptyValue         = errors.New("contradiction: value must be non-empty")
	ErrInvalidReliability = errors.New("contradiction: reliability must be in (0,1]")
	ErrEmptyEvidenceID    = errors.New("contradiction: evidence record has an empty EvidenceID (was it built via evidence.New?)")
	ErrUnknownPairKind    = errors.New("contradiction: unknown PairKind")
)

// Engine is VICE's insurance-domain Contradiction Engine. It wraps a
// real *moat.ArbitrationEngine (pkg/moat/contradiction) — every
// arbitration decision (conflict detection, confidence weighting,
// winner selection, human-review gating) is computed by that engine,
// never re-derived here. Engine adds only: (1) an insurance-typed
// ingestion surface (Submit, taking evidence.Record rather than raw
// strings), (2) a lookup from EvidenceID back to the submitted Record
// so §11 conflict-of-interest context can be attached to output, and
// (3) the projection into ContradictionRecord's blueprint §25 shape.
type Engine struct {
	mu sync.RWMutex

	arb *moat.ArbitrationEngine

	// evidenceByID lets Detect attach each side's already-computed
	// SourceInterest as context. Never consulted by any decision logic.
	evidenceByID map[string]evidence.Record
	// claimsByEvidence: EvidenceID -> set of claim keys it has asserted
	// a value for. Used by PairwiseSupport (three_party.go) to find the
	// shared claims two pieces of evidence can actually be compared on.
	claimsByEvidence map[string]map[string]bool
}

// NewEngine returns an Engine wrapping a fresh
// *moat.ArbitrationEngine.
func NewEngine() *Engine {
	return &Engine{
		arb:              moat.NewArbitrationEngine(),
		evidenceByID:     make(map[string]evidence.Record),
		claimsByEvidence: make(map[string]map[string]bool),
	}
}

// ArbitrationEngine returns the real, wrapped engine directly, for
// callers that need its fuller surface (RankHypotheses, TruthLedger,
// VerifyTruthLedger, ...) beyond what this package projects.
func (e *Engine) ArbitrationEngine() *moat.ArbitrationEngine { return e.arb }

// Submit ingests one piece of insurance evidence as an observation on
// claimKey — "this evidence asserts value for claimKey" — into the
// wrapped ArbitrationEngine. reliability must be in (0,1] (matching
// moat.RawObservation's own contract) and, per blueprint §11 and §28,
// MUST NOT be derived from rec.Origin or rec.SourceInterest.Interest by
// the caller: this package never reads either field for any decision,
// and callers that want evidence-quality-driven weighting should derive
// it from rec.Strength (blueprint §9's nine independent dimensions),
// never from who submitted the evidence or their declared interest.
func (e *Engine) Submit(rec evidence.Record, claimKey, value string, reliability float64, tick uint64) error {
	id := rec.EvidenceID()
	if id == "" {
		return ErrEmptyEvidenceID
	}
	if claimKey == "" {
		return ErrEmptyClaimKey
	}
	if value == "" {
		return ErrEmptyValue
	}
	if reliability <= 0 || reliability > 1 {
		return ErrInvalidReliability
	}
	if err := e.arb.Observe(moat.RawObservation{
		ClaimKey: claimKey, SourceID: id, Value: value,
		SourceReliability: reliability, Tick: tick,
	}); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.evidenceByID[id] = rec
	if e.claimsByEvidence[id] == nil {
		e.claimsByEvidence[id] = make(map[string]bool)
	}
	e.claimsByEvidence[id][claimKey] = true
	return nil
}

// Detect runs the real ArbitrationEngine's ConflictRecords for
// claimKey and projects the result into blueprint §25's
// ContradictionRecord shape — one record per pair of disagreeing
// EvidenceIDs. category labels which of §25's ten relationship types
// this call represents (fails closed with ErrUnknownPairKind if not
// one of the ten). description, if non-empty, is used verbatim as
// every produced record's free-text Conflict field (e.g. "Delivery
// time differs by 4 hours" from §25's own example); if empty, a
// generic description is generated from claimKey and the two values.
//
// If every source that reported a value for claimKey agrees
// (moat.ResolutionUncontested), Detect returns (nil, nil): there is no
// contradiction to report — this is not the same thing as
// "insufficient evidence" (see the error case below) and must not be
// conflated with it.
//
// If claimKey has no observations pending at all, Detect fails closed
// and returns the wrapped engine's own error rather than guessing —
// this is the golden rule ("insufficient evidence -> INSUFFICIENT, not
// a guessed conclusion") applied at the API boundary.
func (e *Engine) Detect(claimKey string, category PairKind, description string, tick, halfLifeTicks uint64) ([]ContradictionRecord, error) {
	if claimKey == "" {
		return nil, ErrEmptyClaimKey
	}
	if !IsKnownPairKind(category) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPairKind, category)
	}

	cr, err := e.arb.ConflictRecords(claimKey, tick, halfLifeTicks)
	if err != nil {
		return nil, err
	}
	if cr.ResolutionState == moat.ResolutionUncontested {
		return nil, nil
	}

	severity := severityFromConflict(cr)
	resolution := resolutionFromState(cr.ResolutionState)

	e.mu.RLock()
	defer e.mu.RUnlock()

	var out []ContradictionRecord
	for i := 0; i < len(cr.Sources); i++ {
		for j := i + 1; j < len(cr.Sources); j++ {
			if cr.Values[i] == cr.Values[j] {
				continue
			}
			a, b := cr.Sources[i], cr.Sources[j]
			va, vb := cr.Values[i], cr.Values[j]

			text := description
			if text == "" {
				text = fmt.Sprintf("%s: conflicting values for %q (%q vs %q)", category, claimKey, va, vb)
			}

			rec := ContradictionRecord{
				ContradictionID:     contradictionID(claimKey, category, a, b, va, vb),
				Severity:            severity,
				EvidenceA:           a,
				EvidenceB:           b,
				Conflict:            text,
				Resolution:          resolution,
				HumanReviewRequired: cr.HumanReviewRequired,
				Category:            category,
			}
			if ra, ok := e.evidenceByID[a]; ok {
				si := ra.SourceInterest
				rec.EvidenceASourceInterest = &si
			}
			if rb, ok := e.evidenceByID[b]; ok {
				si := rb.SourceInterest
				rec.EvidenceBSourceInterest = &si
			}
			out = append(out, rec)
		}
	}
	return out, nil
}

// lowSeverityConfidenceThreshold: an ARBITRATED winner at or above this
// confidence is a decisive majority (LOW severity — a confident
// resolution exists even though disagreement occurred). Below it but
// still ARBITRATED (i.e. still >= the wrapped engine's own >=2/3
// confident-winner bar) is MEDIUM: a real winner, but a narrow one.
// Any claim the wrapped engine itself flags HumanReviewRequired is
// always HIGH, regardless of confidence — that flag already IS the
// real engine's own "this is genuinely contested" signal (moat's
// ConflictRecords: HumanReviewRequired is set exactly when no
// candidate reaches its two-thirds confidence bar, or there is a tie),
// and this package defers to it entirely rather than recomputing when
// human review is warranted.
const lowSeverityConfidenceThreshold = 0.85

// severityFromConflict bands the real ArbitrationEngine's own
// HumanReviewRequired flag and Confidence score into this package's
// three-level Severity — no new arbitration math, just banding numbers
// the wrapped engine already computed.
func severityFromConflict(cr moat.ConflictRecord) Severity {
	if cr.HumanReviewRequired {
		return SeverityHigh
	}
	if cr.Confidence >= lowSeverityConfidenceThreshold {
		return SeverityLow
	}
	return SeverityMedium
}

// resolutionFromState maps moat's ResolutionState onto this package's
// Resolution. Only ever called for ARBITRATED or CONTESTED — Detect
// returns early for ResolutionUncontested — but a defensive default is
// still fail-closed (UNRESOLVED, never a guessed ARBITRATED).
func resolutionFromState(s moat.ResolutionState) Resolution {
	switch s {
	case moat.ResolutionArbitrated:
		return ResolutionArbitrated
	default:
		return ResolutionUnresolved
	}
}

// contradictionID is a content-addressed identifier (SHA-256 over the
// claim key, pair kind, and every field that distinguishes this
// disagreement) — deterministic and replay-safe, matching this repo's
// existing convention (no time.Now(), no random UUID) of every other
// hash-chained or content-addressed ID.
func contradictionID(claimKey string, category PairKind, sourceA, sourceB, valueA, valueB string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s=%s|%s=%s", claimKey, category, sourceA, valueA, sourceB, valueB)))
	return "CX-" + hex.EncodeToString(sum[:])[:16]
}
