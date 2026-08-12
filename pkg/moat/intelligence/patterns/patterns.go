// Package patterns implements VERIQO's Sanctions Evasion & Transshipment
// Pattern Library — the concrete answer to two of the nine "Missing
// Intelligence Layers" CRITICAL_FINDINGS.docx names as absent:
// "Sanctions Evasion Pattern Library" and "Transshipment Detection".
//
// Honest scope, stated up front: these are structural, explainable,
// rule-based detectors operating on typed behavioral signals (AIS gaps,
// ship-to-ship transfer proximity to sanctioned ports, flag/ownership
// change timing relative to sanction listing dates). They encode the
// *shape* of known evasion tradecraft documented in public OFAC/IMO
// guidance, not a trained classifier fitted on real case data — no
// labeled sanctions-evasion dataset is available in this environment.
// Every match carries a plain-English Explanation so a human reviewer
// (or the calibration engine, once real ground truth arrives) can judge
// and eventually score each rule's real-world precision. This is a
// detection scaffold designed to accept real ground truth, not a
// finished, validated risk model — CRITICAL_FINDINGS' "Missing
// Validation Frameworks" (backtesting, confidence intervals) remains a
// separate, explicitly open gap.
//
// Design invariants match the rest of the kernel: no time.Now() (every
// evaluation carries an explicit Tick), no UUIDs (pattern IDs are
// stable names), append-only hash-chained match ledger, deterministic
// order-invariant aggregation (learned directly from the hbayes
// ordering-artifact critique in Hasil_audit_brutal — see AggregateScore).
package patterns

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

var (
	ErrTamperDetected     = errors.New("patterns: hash chain verification failed")
	ErrInvalidSignalInput = errors.New("patterns: signal input is NaN, +/-Inf, or negative where a non-negative count/duration is required")
)

// Validate rejects a Signal whose numeric fields are non-finite or
// negative — the v7.9 audit P1 "Numeric input validation" ask, applied
// here since Signal (not just pricing's series) also carries caller-
// supplied numeric fields (AISGapHours, counts, chain length) that feed
// directly into score arithmetic.
func (s Signal) Validate() error {
	fields := map[string]float64{
		"AISGapHours":              s.AISGapHours,
		"DarkPortCallCount":        float64(s.DarkPortCallCount),
		"STSTransferCount":         float64(s.STSTransferCount),
		"FlagChangeCount":          float64(s.FlagChangeCount),
		"OwnershipChangeCount":     float64(s.OwnershipChangeCount),
		"TransshipmentChainLength": float64(s.TransshipmentChainLength),
	}
	for name, v := range fields {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: %s = %v", ErrInvalidSignalInput, name, v)
		}
		if v < 0 {
			return fmt.Errorf("%w: %s = %v (must be non-negative)", ErrInvalidSignalInput, name, v)
		}
	}
	return nil
}

// Category classifies what kind of tradecraft a Pattern targets.
type Category string

const (
	CategorySanctionsEvasion     Category = "SANCTIONS_EVASION"
	CategoryTransshipment        Category = "TRANSSHIPMENT"
	CategoryOwnershipConcealment Category = "OWNERSHIP_CONCEALMENT"
	CategoryIdentitySpoofing     Category = "IDENTITY_SPOOFING"
)

// Signal is the typed set of behavioral observations a caller supplies
// for one vessel-voyage under evaluation. Every field is a structural
// fact (a duration, a count, a boolean condition) derivable from AIS
// history, port-call records, and ownership-registry timelines already
// modeled elsewhere in the kernel (pkg/moat/domain/maritime) — this
// package does not source the raw data itself.
type Signal struct {
	// AISGapHours is the longest continuous AIS transponder silence in
	// the observation window. Legitimate causes exist (equipment
	// failure, high-traffic congestion zones); this is one input among
	// several, not a standalone accusation.
	AISGapHours float64
	// DarkPortCallCount is the number of port calls inferred (via
	// draft change, cargo manifest, or satellite correlation) with no
	// corresponding AIS transponder activity.
	DarkPortCallCount int
	// STSTransferCount is the number of ship-to-ship transfers observed
	// in the window.
	STSTransferCount int
	// STSNearSanctionedJurisdiction is true if any STS transfer
	// occurred within a caller-defined proximity of a jurisdiction
	// under sanctions (the caller supplies the geofence/threshold — this
	// package only consumes the boolean result).
	STSNearSanctionedJurisdiction bool
	// FlagChangeCount is the number of flag-state changes in the window.
	FlagChangeCount int
	// FlagChangeNearSanctionEvent is true if a flag change occurred
	// within a caller-defined window of a relevant sanctions-listing
	// event for the vessel, an owner, or a cargo counterparty.
	FlagChangeNearSanctionEvent bool
	// OwnershipChangeCount is the number of beneficial-ownership
	// changes recorded in the window (see maritime.OwnershipGraph).
	OwnershipChangeCount int
	// OwnershipChangeNearSanctionEvent mirrors FlagChangeNearSanctionEvent
	// for ownership-record changes.
	OwnershipChangeNearSanctionEvent bool
	// DraftChangeWithoutPortCall is true when loaded/laden draft
	// readings change materially with no corresponding declared port
	// call — classic dark-loading signature.
	DraftChangeWithoutPortCall bool
	// IdentitySpoofingSuspected is true when AIS-broadcast MMSI/IMO
	// does not match other correlated evidence for the same physical
	// vessel (e.g. satellite hull-ID correlation via pkg/moat/fusion).
	IdentitySpoofingSuspected bool
	// TransshipmentChainLength is the number of intermediate vessels a
	// cargo lot passed through before reaching its final declared
	// destination, per bill-of-lading / manifest correlation.
	TransshipmentChainLength int
}

// Match is one pattern's evaluation result against a Signal.
type Match struct {
	PatternID   string
	Category    Category
	Hit         bool
	Score       float64 // 0..1, meaningful only when Hit
	Explanation string
}

// Pattern is one named, versioned, independently testable detector.
type Pattern interface {
	ID() string
	Name() string
	Category() Category
	Evaluate(s Signal) Match
}

// --- Concrete patterns -----------------------------------------------

type darkActivityPattern struct{}

func (darkActivityPattern) ID() string         { return "P001_DARK_ACTIVITY" }
func (darkActivityPattern) Name() string       { return "Extended AIS Gap / Dark Port Call" }
func (darkActivityPattern) Category() Category { return CategorySanctionsEvasion }
func (darkActivityPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: darkActivityPattern{}.ID(), Category: darkActivityPattern{}.Category()}
	switch {
	case s.AISGapHours >= 48 || s.DarkPortCallCount >= 1:
		score := clamp01(s.AISGapHours/96.0)*0.6 + clamp01(float64(s.DarkPortCallCount)/2.0)*0.4
		m.Hit = true
		m.Score = score
		m.Explanation = fmt.Sprintf(
			"AIS silent for %.1fh (threshold 48h) with %d dark port call(s): consistent with transponder manipulation to obscure a sanctioned port visit or STS transfer.",
			s.AISGapHours, s.DarkPortCallCount)
	default:
		m.Explanation = fmt.Sprintf("AIS gap %.1fh and %d dark port calls below evasion threshold.", s.AISGapHours, s.DarkPortCallCount)
	}
	return m
}

type flagHoppingPattern struct{}

func (flagHoppingPattern) ID() string         { return "P002_FLAG_HOPPING" }
func (flagHoppingPattern) Name() string       { return "Flag Change Proximate to Sanction Event" }
func (flagHoppingPattern) Category() Category { return CategorySanctionsEvasion }
func (flagHoppingPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: flagHoppingPattern{}.ID(), Category: flagHoppingPattern{}.Category()}
	if s.FlagChangeNearSanctionEvent || s.FlagChangeCount >= 2 {
		score := 0.3
		if s.FlagChangeNearSanctionEvent {
			score += 0.5
		}
		score += clamp01(float64(s.FlagChangeCount)/4.0) * 0.2
		m.Hit = true
		m.Score = clamp01(score)
		m.Explanation = fmt.Sprintf(
			"%d flag change(s) observed; timed-near-sanction-event=%v — reflagging shortly before/after a listing is a documented evasion technique to escape flag-state enforcement.",
			s.FlagChangeCount, s.FlagChangeNearSanctionEvent)
	} else {
		m.Explanation = "No flag-change pattern consistent with evasion timing."
	}
	return m
}

type ownershipTimingPattern struct{}

func (ownershipTimingPattern) ID() string { return "P003_OWNERSHIP_TIMING" }
func (ownershipTimingPattern) Name() string {
	return "Beneficial Ownership Change Proximate to Sanction Event"
}
func (ownershipTimingPattern) Category() Category { return CategoryOwnershipConcealment }
func (ownershipTimingPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: ownershipTimingPattern{}.ID(), Category: ownershipTimingPattern{}.Category()}
	if s.OwnershipChangeNearSanctionEvent {
		score := 0.55 + clamp01(float64(s.OwnershipChangeCount)/3.0)*0.3
		m.Hit = true
		m.Score = clamp01(score)
		m.Explanation = fmt.Sprintf(
			"%d ownership change(s), at least one proximate to a sanction-listing event — consistent with a beneficial-owner attempting to shed a designated entity's registered stake while retaining effective control (see maritime.OwnershipGraph UBO piercing).",
			s.OwnershipChangeCount)
	} else {
		m.Explanation = "No ownership-change timing consistent with evasion."
	}
	return m
}

type stsTransshipmentPattern struct{}

func (stsTransshipmentPattern) ID() string { return "P004_STS_TRANSSHIPMENT" }
func (stsTransshipmentPattern) Name() string {
	return "Ship-to-Ship Transfer Near Sanctioned Jurisdiction"
}
func (stsTransshipmentPattern) Category() Category { return CategoryTransshipment }
func (stsTransshipmentPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: stsTransshipmentPattern{}.ID(), Category: stsTransshipmentPattern{}.Category()}
	if s.STSTransferCount >= 1 && s.STSNearSanctionedJurisdiction {
		score := 0.5 + clamp01(float64(s.STSTransferCount)/3.0)*0.4
		m.Hit = true
		m.Score = clamp01(score)
		m.Explanation = fmt.Sprintf(
			"%d ship-to-ship transfer(s) within proximity of a sanctioned jurisdiction — the dominant real-world technique for laundering origin on sanctioned crude/product cargo (dark STS 'floating storage' transfers).",
			s.STSTransferCount)
	} else if s.STSTransferCount >= 2 {
		m.Hit = true
		m.Score = clamp01(0.25 + float64(s.STSTransferCount)*0.05)
		m.Explanation = fmt.Sprintf("%d ship-to-ship transfers with no sanctioned-jurisdiction proximity flagged — lower-confidence transshipment signal, still worth review.", s.STSTransferCount)
	} else {
		m.Explanation = "No STS transfer pattern indicating transshipment."
	}
	return m
}

type chainLengthPattern struct{}

func (chainLengthPattern) ID() string         { return "P005_TRANSSHIPMENT_CHAIN_LENGTH" }
func (chainLengthPattern) Name() string       { return "Excessive Transshipment Chain Length" }
func (chainLengthPattern) Category() Category { return CategoryTransshipment }
func (chainLengthPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: chainLengthPattern{}.ID(), Category: chainLengthPattern{}.Category()}
	if s.TransshipmentChainLength >= 3 {
		score := clamp01(float64(s.TransshipmentChainLength-2) / 4.0)
		m.Hit = true
		m.Score = clamp01(0.4 + score*0.5)
		m.Explanation = fmt.Sprintf(
			"Cargo passed through %d intermediate vessels before final destination — chain length beyond 2 hops has low legitimate-logistics prevalence and is a recognized origin-laundering signature.",
			s.TransshipmentChainLength)
	} else {
		m.Explanation = fmt.Sprintf("Transshipment chain length %d within normal logistics range.", s.TransshipmentChainLength)
	}
	return m
}

type identitySpoofingPattern struct{}

func (identitySpoofingPattern) ID() string         { return "P006_IDENTITY_SPOOFING" }
func (identitySpoofingPattern) Name() string       { return "AIS Identity Spoofing / Mismatch" }
func (identitySpoofingPattern) Category() Category { return CategoryIdentitySpoofing }
func (identitySpoofingPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: identitySpoofingPattern{}.ID(), Category: identitySpoofingPattern{}.Category()}
	if s.IdentitySpoofingSuspected {
		m.Hit = true
		m.Score = 0.85
		m.Explanation = "Broadcast AIS identity does not match independently correlated evidence (e.g. satellite hull correlation) for the same physical vessel — high-confidence spoofing indicator, since accidental MMSI/IMO mismatches of this kind are rare in well-formed AIS data."
	} else {
		m.Explanation = "No identity mismatch detected."
	}
	return m
}

type draftAnomalyPattern struct{}

func (draftAnomalyPattern) ID() string         { return "P007_DRAFT_ANOMALY" }
func (draftAnomalyPattern) Name() string       { return "Draft Change Without Declared Port Call" }
func (draftAnomalyPattern) Category() Category { return CategorySanctionsEvasion }
func (draftAnomalyPattern) Evaluate(s Signal) Match {
	m := Match{PatternID: draftAnomalyPattern{}.ID(), Category: draftAnomalyPattern{}.Category()}
	if s.DraftChangeWithoutPortCall {
		m.Hit = true
		m.Score = 0.6
		m.Explanation = "Vessel draft changed materially with no declared port call in between — consistent with an undeclared loading/discharge event, typically via STS transfer or an unreported anchorage stop."
	} else {
		m.Explanation = "No draft/port-call inconsistency detected."
	}
	return m
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// StandardPatterns returns the shipped pattern set in stable, sorted
// (by ID) order.
func StandardPatterns() []Pattern {
	ps := []Pattern{
		darkActivityPattern{},
		flagHoppingPattern{},
		ownershipTimingPattern{},
		stsTransshipmentPattern{},
		chainLengthPattern{},
		identitySpoofingPattern{},
		draftAnomalyPattern{},
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID() < ps[j].ID() })
	return ps
}

// matchLedgerRecord is one hash-chained, append-only evaluation record.
type matchLedgerRecord struct {
	Index    uint64
	Subject  string
	Matches  string // canonical joined representation of the Match set
	Tick     uint64
	PrevHash string
	Hash     string
}

// Library evaluates a Signal against a fixed, ordered set of Patterns
// and maintains a hash-chained ledger of every evaluation for audit
// and eventual calibration feedback.
type Library struct {
	mu       sync.RWMutex
	patterns []Pattern
	log      []matchLedgerRecord
	head     string
}

// NewLibrary constructs a Library. Pass StandardPatterns() for the
// shipped detector set, or a caller-curated subset/superset — the
// library itself is agnostic to which patterns it holds.
func NewLibrary(patterns []Pattern) *Library {
	sorted := make([]Pattern, len(patterns))
	copy(sorted, patterns)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID() < sorted[j].ID() })
	return &Library{patterns: sorted}
}

// Evaluate runs every pattern against s in stable ID order and records
// the result set in the hash-chained ledger under subject (e.g. a
// vessel/voyage identifier — typically a maritime.Entity.ID). Invalid
// numeric input (NaN/Inf/negative — v7.9 audit P1) is rejected before
// any pattern runs, rather than silently producing a misleading score.
func (l *Library) Evaluate(subject string, s Signal, tick uint64) ([]Match, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	matches := make([]Match, 0, len(l.patterns))
	var b []byte
	for _, p := range l.patterns {
		m := p.Evaluate(s)
		matches = append(matches, m)
		b = append(b, []byte(fmt.Sprintf("|%s:%v:%.6f", m.PatternID, m.Hit, m.Score))...)
	}
	rec := matchLedgerRecord{
		Index:    uint64(len(l.log)),
		Subject:  subject,
		Matches:  string(b),
		Tick:     tick,
		PrevHash: l.head,
	}
	raw := fmt.Sprintf("index=%d|subject=%s|matches=%s|tick=%d|prev=%s",
		rec.Index, rec.Subject, rec.Matches, rec.Tick, rec.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	rec.Hash = hex.EncodeToString(sum[:])
	l.log = append(l.log, rec)
	l.head = rec.Hash
	return matches, nil
}

// evidenceFamily groups patterns that are typically driven by the SAME
// underlying behavioral observation, closing v7.9 audit §6 "Noisy-OR
// mengasumsikan sesuatu yang belum terbukti": P001 (AIS gap), P004 (STS
// near sanctioned jurisdiction), and P007 (draft change without port
// call) can all fire off one single AIS/behavioral data gap — treating
// their hits as three independent confirmations produces exactly the
// "97.6% risk from one underlying fact" double-counting the audit
// demonstrates with P001=0.8/P004=0.7/P007=0.6.
//
// This mapping is a declared, static, auditable statement of which
// detectors share underlying evidence — the honest minimum the audit
// asks for ("pattern -> evidence dependency graph -> correlation
// cluster -> cluster score -> risk"), not a learned/inferred clustering
// (no labeled co-occurrence dataset exists in this environment, same
// limitation hbayes's correlation matrix already discloses).
type evidenceFamily string

const (
	familyAISBehavioral   evidenceFamily = "AIS_BEHAVIORAL"   // P001, P004, P007: share underlying AIS/track data
	familyFlagState       evidenceFamily = "FLAG_STATE"       // P002: registry-timing evidence, standalone
	familyOwnershipRecord evidenceFamily = "OWNERSHIP_RECORD" // P003: ownership-registry evidence, standalone
	familyChainTopology   evidenceFamily = "CHAIN_TOPOLOGY"   // P005: manifest/BoL chain evidence, standalone
	familyIdentity        evidenceFamily = "IDENTITY"         // P006: cross-source identity correlation, standalone
)

var patternFamily = map[string]evidenceFamily{
	"P001_DARK_ACTIVITY":              familyAISBehavioral,
	"P004_STS_TRANSSHIPMENT":          familyAISBehavioral,
	"P007_DRAFT_ANOMALY":              familyAISBehavioral,
	"P002_FLAG_HOPPING":               familyFlagState,
	"P003_OWNERSHIP_TIMING":           familyOwnershipRecord,
	"P005_TRANSSHIPMENT_CHAIN_LENGTH": familyChainTopology,
	"P006_IDENTITY_SPOOFING":          familyIdentity,
}

// familyOf returns the declared evidence family for a pattern ID, or a
// unique per-pattern family (the pattern ID itself) if undeclared — an
// unmapped/custom pattern is treated as its own standalone family
// rather than silently defaulting into someone else's cluster.
func familyOf(patternID string) evidenceFamily {
	if f, ok := patternFamily[patternID]; ok {
		return f
	}
	return evidenceFamily(patternID)
}

// AggregateScore combines a Match set into one composite 0..1 score
// using CORRELATION-CLUSTER-AWARE noisy-OR, closing v7.9 audit P0
// "Pattern correlation": pattern -> evidence dependency graph ->
// correlation cluster -> cluster score -> risk.
//
// Within a family (patterns sharing declared underlying evidence),
// hits are combined via max() rather than noisy-OR — the family's
// contribution is its single strongest signal, since additional hits
// within the same family are read as multiple views of the same
// underlying fact, not independent confirmations. ACROSS families
// (genuinely different evidence types — AIS behavior vs. flag registry
// vs. ownership registry), noisy-OR combination remains correct: those
// really are independent-ish signal sources.
//
// This directly fixes the audit's worked example: P001=0.8, P004=0.7,
// P007=0.6 (all AIS_BEHAVIORAL family) now combine to max(0.8,0.7,0.6)
// = 0.8 for that family's contribution, not noisy-OR's 97.6% — plus
// whatever independent families (flag/ownership/chain/identity) also
// hit are still noisy-OR'd against the family-level score, since a
// hit from a genuinely different evidence type IS additional
// information.
//
// Still deliberately order-invariant: family grouping and per-family
// max()/cross-family noisy-OR are both commutative over the match set
// regardless of evaluation order — same discipline as the original
// implementation, now with the epistemic fix the audit's §6-§7
// identified as still missing.
func AggregateScore(matches []Match) float64 {
	familyMax := map[evidenceFamily]float64{}
	any := false
	for _, m := range matches {
		if !m.Hit {
			continue
		}
		any = true
		s := clamp01(m.Score)
		f := familyOf(m.PatternID)
		if s > familyMax[f] {
			familyMax[f] = s
		}
	}
	if !any {
		return 0
	}
	product := 1.0
	for _, s := range familyMax {
		product *= (1 - s)
	}
	return clamp01(1 - product)
}

// AggregateScoreRaw is the PRE-correlation-fix behavior (plain noisy-OR
// over every hit, no family clustering) — retained, clearly labeled,
// for callers that explicitly want to see the naive/uncorrected number
// (e.g. for audit comparison or a UI showing "without correlation
// correction"). Production risk scoring should use AggregateScore.
func AggregateScoreRaw(matches []Match) float64 {
	product := 1.0
	any := false
	for _, m := range matches {
		if !m.Hit {
			continue
		}
		any = true
		product *= (1 - clamp01(m.Score))
	}
	if !any {
		return 0
	}
	return clamp01(1 - product)
}

// VerifyChain re-derives every hash in the match ledger, detecting
// tampering.
func (l *Library) VerifyChain() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prev := ""
	for _, rec := range l.log {
		raw := fmt.Sprintf("index=%d|subject=%s|matches=%s|tick=%d|prev=%s",
			rec.Index, rec.Subject, rec.Matches, rec.Tick, prev)
		sum := sha256.Sum256([]byte(raw))
		want := hex.EncodeToString(sum[:])
		if want != rec.Hash {
			return ErrTamperDetected
		}
		prev = rec.Hash
	}
	return nil
}

// Head returns the current ledger hash chain head.
func (l *Library) Head() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.head
}

// PatternCount returns how many patterns this Library holds.
func (l *Library) PatternCount() int { return len(l.patterns) }
