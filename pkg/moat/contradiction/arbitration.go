// Truth Arbitration pipeline — closes the explicit critique in
// "Melengkapi_dan_menutup_gap.docx" section 2: the original
// contradiction.Engine (contradiction.go) only INGESTED arbitration
// outcomes computed elsewhere (by fusion.Engine) — it never itself
// decided which hypothesis is most credible. That is a real gap: it is
// conflict-detection + decay + confidence tracking, not arbitration.
//
// This file adds a self-contained pipeline that performs the actual
// nine-stage sequence the critique names:
//
//	Observation -> Evidence -> Claim -> Conflict Detection ->
//	Conflict Graph -> Confidence Update -> Evidence Weighting ->
//	Truth Arbitration -> Truth Version
//
// ArbitrationEngine computes arbitration from raw observations directly
// — it does not require fusion.Engine to have already decided a winner.
// (fusion.Engine remains valid and independently useful for its own
// correlation-aware use cases; this package no longer *depends* on it
// for the arbitration decision itself.)
package contradiction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

// --- Stage 1: Observation -------------------------------------------------

// RawObservation is what a source reports: "I observed VALUE for CLAIM,
// at TICK, and here is my current reliability."
type RawObservation struct {
	ClaimKey          string
	SourceID          string
	Value             string
	SourceReliability float64 // in (0,1]
	Tick              uint64
}

// --- Stage 2: Evidence (weighted, decayed) --------------------------------

// Evidence is a RawObservation promoted with a computed Weight — the
// output of Stage 2 (Evidence) and Stage 7 (Evidence Weighting) combined,
// since weighting is a pure function of the observation + decay params,
// not a separate mutable stage.
type Evidence struct {
	RawObservation
	Weight float64 // SourceReliability, decayed by staleness relative to arbitration tick
}

func computeEvidence(o RawObservation, arbitrationTick uint64, halfLifeTicks uint64) Evidence {
	age := uint64(0)
	if arbitrationTick > o.Tick {
		age = arbitrationTick - o.Tick
	}
	decayed := EvidenceDecay(o.SourceReliability, age, halfLifeTicks)
	return Evidence{RawObservation: o, Weight: decayed}
}

// --- Stage 4/5: Conflict Detection + Conflict Graph -----------------------

// ConflictEdge records that two pieces of evidence for the SAME claim
// assert DIFFERENT values — the atomic unit of Stage 4 (Conflict
// Detection). The full set of edges for a claim IS its Conflict Graph
// (Stage 5) — represented here as an adjacency list rather than a
// separate opaque type, since that is what callers actually need to
// traverse it.
type ConflictEdge struct {
	SourceA, SourceB string
	ValueA, ValueB   string
}

// detectConflicts is Stage 4+5: pairwise-compare every piece of evidence
// for a claim and record every disagreement.
func detectConflicts(evidence []Evidence) []ConflictEdge {
	var edges []ConflictEdge
	for i := 0; i < len(evidence); i++ {
		for j := i + 1; j < len(evidence); j++ {
			if evidence[i].Value != evidence[j].Value {
				edges = append(edges, ConflictEdge{
					SourceA: evidence[i].SourceID, SourceB: evidence[j].SourceID,
					ValueA: evidence[i].Value, ValueB: evidence[j].Value,
				})
			}
		}
	}
	return edges
}

// TruthCandidate is one ranked hypothesis for a claim's true value —
// closes the explicit "Hypothesis Ranking, Truth Candidate" gap named
// in "Yang_masih_belum.docx" section A: TruthVersion alone only exposed
// the winner and a single runner-up; RankHypotheses exposes every
// candidate value in ranked order with its full supporting evidence.
type TruthCandidate struct {
	Rank              int
	Value             string
	Score             float64 // total weighted evidence backing this value
	Confidence        float64 // Score / total score across all candidates
	SupportingSources []string
}

// RankHypotheses re-derives the full ranked candidate list for a claim
// as of arbitrationTick — a superset of what ArbitrateClaim's winner/
// runner-up fields expose, useful when a caller (or an analyst) wants to
// see EVERY hypothesis under consideration, not just the top two.
func (a *ArbitrationEngine) RankHypotheses(claimKey string, arbitrationTick uint64, halfLifeTicks uint64) ([]TruthCandidate, error) {
	a.mu.Lock()
	raw := append([]RawObservation{}, a.pending[claimKey]...)
	a.mu.Unlock()
	if len(raw) == 0 {
		return nil, fmt.Errorf("contradiction: no observations pending for claim %q", claimKey)
	}

	evidence := make([]Evidence, 0, len(raw))
	for _, o := range raw {
		evidence = append(evidence, computeEvidence(o, arbitrationTick, halfLifeTicks))
	}
	scores := valueScore(evidence)

	sourcesByValue := make(map[string][]string)
	for _, e := range evidence {
		sourcesByValue[e.Value] = append(sourcesByValue[e.Value], e.SourceID)
	}

	values := make([]string, 0, len(scores))
	for v := range scores {
		values = append(values, v)
	}
	sort.Strings(values) // deterministic base order before score sort
	sort.SliceStable(values, func(i, j int) bool { return scores[values[i]] > scores[values[j]] })

	total := 0.0
	for _, s := range scores {
		total += s
	}

	out := make([]TruthCandidate, 0, len(values))
	for i, v := range values {
		support := append([]string{}, sourcesByValue[v]...)
		sort.Strings(support)
		conf := 0.0
		if total > 0 {
			conf = scores[v] / total
		}
		out = append(out, TruthCandidate{
			Rank: i + 1, Value: v, Score: scores[v], Confidence: conf, SupportingSources: support,
		})
	}
	return out, nil
}

// --- Stage 6: Confidence Update -------------------------------------------

// valueScore is Stage 6: for each distinct asserted value, sum the
// weighted evidence backing it — the raw, not-yet-normalized confidence
// signal per candidate value.
func valueScore(evidence []Evidence) map[string]float64 {
	scores := make(map[string]float64)
	for _, e := range evidence {
		scores[e.Value] += e.Weight
	}
	return scores
}

// --- Stage 8/9: Truth Arbitration + Truth Version -------------------------

// TruthVersion is Stage 9's output: one hash-chained, versioned
// arbitration decision for a claim. Multiple TruthVersions for the same
// ClaimKey over time form the claim's truth-evolution ledger (distinct
// from, and now upstream of, the Observation-recording Ingest/Record
// path in contradiction.go — ArbitrateClaim can itself feed Ingest via
// ToObservation() below, unifying the two).
type TruthVersion struct {
	Index    uint64
	ClaimKey string

	Value              string
	Confidence         float64 // winning value's score / total score across all candidate values
	RunnerUpValue      string
	RunnerUpConfidence float64

	SupportingSources []string
	ConflictingEdges  []ConflictEdge
	Tick              uint64

	PrevHash string
	Hash     string
}

// ToObservation projects a TruthVersion into the Observation shape the
// existing Ingest/Record ledger (contradiction.go) consumes, so the two
// pipelines compose: ArbitrateClaim (compute) -> Ingest (record
// evolution/standings) is now a single coherent flow instead of two
// disconnected concepts.
func (tv TruthVersion) ToObservation() Observation {
	var losing []string
	seen := make(map[string]bool)
	for _, e := range tv.ConflictingEdges {
		for _, s := range []string{e.SourceA, e.SourceB} {
			if !seen[s] && !containsStr(tv.SupportingSources, s) {
				losing = append(losing, s)
				seen[s] = true
			}
		}
	}
	return Observation{
		ClaimKey: tv.ClaimKey, Winner: tv.Value, WinnerConfidence: tv.Confidence,
		RunnerUp: tv.RunnerUpValue, RunnerUpConfidence: tv.RunnerUpConfidence,
		Contradiction:  len(tv.ConflictingEdges) > 0,
		WinningSources: tv.SupportingSources, LosingSources: losing, Tick: tv.Tick,
	}
}

func containsStr(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// ArbitrationEngine runs the full nine-stage pipeline from raw
// observations to hash-chained TruthVersions — genuine arbitration, not
// a record of someone else's decision.
type ArbitrationEngine struct {
	mu sync.Mutex

	pending map[string][]RawObservation // Stage 1/3: observations grouped by ClaimKey
	ledger  map[string][]TruthVersion   // per-claim TruthVersion chain
	heads   map[string]string           // per-claim chain head hash
}

func NewArbitrationEngine() *ArbitrationEngine {
	return &ArbitrationEngine{
		pending: make(map[string][]RawObservation),
		ledger:  make(map[string][]TruthVersion),
		heads:   make(map[string]string),
	}
}

// Observe is Stage 1+3: record a raw observation under its claim,
// pending arbitration.
func (a *ArbitrationEngine) Observe(o RawObservation) error {
	if o.ClaimKey == "" || o.SourceID == "" {
		return fmt.Errorf("contradiction: observation requires ClaimKey and SourceID")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[o.ClaimKey] = append(a.pending[o.ClaimKey], o)
	return nil
}

// Observations returns a defensive copy of the raw observations pending
// for a claim — used by verification/IVF bundle builders (see
// pkg/engine/adapters.go IVFReplayFunc) to package the TRUE raw
// evidence, not the computed TruthVersion, so an independent verifier
// can re-run ArbitrateClaim from scratch on a fresh engine and compare
// outputs, rather than trusting a description of the result.
func (a *ArbitrationEngine) Observations(claimKey string) []RawObservation {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.pending[claimKey]
	out := make([]RawObservation, len(src))
	copy(out, src)
	return out
}

// ArbitrateClaim runs stages 2 and 4-9 over every observation pending
// for a claim as of arbitrationTick, producing a new hash-chained
// TruthVersion. It does NOT clear pending observations (later
// corrections/additional evidence remain part of the claim's history —
// consistent with the append-only discipline used across this project).
func (a *ArbitrationEngine) ArbitrateClaim(claimKey string, arbitrationTick uint64, halfLifeTicks uint64) (TruthVersion, error) {
	_, span := telemetry.StartSpan(context.Background(), "contradiction.ArbitrateClaim",
		telemetry.Attribute{Key: "claim_key", Value: claimKey})
	defer span.End()

	a.mu.Lock()
	defer a.mu.Unlock()

	raw := a.pending[claimKey]
	if len(raw) == 0 {
		return TruthVersion{}, fmt.Errorf("contradiction: no observations pending for claim %q", claimKey)
	}

	// Stage 2/7: Evidence + Weighting
	evidence := make([]Evidence, 0, len(raw))
	for _, o := range raw {
		evidence = append(evidence, computeEvidence(o, arbitrationTick, halfLifeTicks))
	}

	// Stage 4/5: Conflict Detection + Conflict Graph
	conflicts := detectConflicts(evidence)

	// Stage 6: Confidence Update (per-value weighted score)
	scores := valueScore(evidence)

	// Stage 8: Truth Arbitration — pick the value with the highest total
	// weighted score; deterministic tie-break by lexicographically
	// smallest value so replay is always identical.
	values := make([]string, 0, len(scores))
	for v := range scores {
		values = append(values, v)
	}
	sort.Strings(values)
	sort.SliceStable(values, func(i, j int) bool { return scores[values[i]] > scores[values[j]] })

	winner := values[0]
	total := 0.0
	for _, s := range scores {
		total += s
	}
	confidence := 0.0
	if total > 0 {
		confidence = scores[winner] / total
	}
	runnerUp, runnerUpConf := "", 0.0
	if len(values) > 1 {
		runnerUp = values[1]
		if total > 0 {
			runnerUpConf = scores[runnerUp] / total
		}
	}

	var supporting []string
	for _, e := range evidence {
		if e.Value == winner {
			supporting = append(supporting, e.SourceID)
		}
	}
	sort.Strings(supporting)

	// Stage 9: Truth Version — hash-chained per claim.
	idx := uint64(len(a.ledger[claimKey])) + 1
	prev := a.heads[claimKey]
	tv := TruthVersion{
		Index: idx, ClaimKey: claimKey, Value: winner, Confidence: confidence,
		RunnerUpValue: runnerUp, RunnerUpConfidence: runnerUpConf,
		SupportingSources: supporting, ConflictingEdges: conflicts, Tick: arbitrationTick,
		PrevHash: prev,
	}
	tv.Hash = hashTruthVersion(tv)

	a.ledger[claimKey] = append(a.ledger[claimKey], tv)
	a.heads[claimKey] = tv.Hash
	return tv, nil
}

func hashTruthVersion(tv TruthVersion) string {
	edgeStr := ""
	for _, e := range tv.ConflictingEdges {
		edgeStr += fmt.Sprintf("%s=%s|%s=%s;", e.SourceA, e.ValueA, e.SourceB, e.ValueB)
	}
	b := []byte(fmt.Sprintf("idx=%d|claim=%s|value=%s|conf=%.10f|runnerup=%s|rconf=%.10f|support=%v|edges=%s|tick=%d|prev=%s|",
		tv.Index, tv.ClaimKey, tv.Value, tv.Confidence, tv.RunnerUpValue, tv.RunnerUpConfidence,
		tv.SupportingSources, edgeStr, tv.Tick, tv.PrevHash))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TruthLedger returns the full versioned arbitration history for a
// claim — the concrete "how truth changed over time" artifact, computed
// from genuine arbitration rather than borrowed from elsewhere.
func (a *ArbitrationEngine) TruthLedger(claimKey string) []TruthVersion {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.ledger[claimKey]
	out := make([]TruthVersion, len(src))
	copy(out, src)
	return out
}

// VerifyTruthLedger re-derives every TruthVersion's hash from its fields
// and confirms chain linkage — proving the arbitration history itself is
// tamper-evident, the same discipline as every other chain in this repo.
func (a *ArbitrationEngine) VerifyTruthLedger(claimKey string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := ""
	for i, tv := range a.ledger[claimKey] {
		if tv.Index != uint64(i)+1 {
			return fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if tv.PrevHash != prev {
			return fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, tv.Index)
		}
		recomputed := hashTruthVersion(tv)
		if recomputed != tv.Hash {
			return fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, tv.Index)
		}
		prev = recomputed
	}
	return nil
}
