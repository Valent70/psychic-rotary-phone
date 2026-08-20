// Package contradiction implements Veriqo's Truth & Contradiction
// Intelligence Engine — the layer the "Gap_dan_kekurangan" audit names as
// the single most important missing MOAT capability (priority #1):
//
//	evidence conflict graph -> confidence propagation -> contradiction
//	scoring -> evidence decay -> source arbitration -> truth evolution
//
// It sits ABOVE pkg/moat/fusion, not inside it: fusion.Engine already
// produces one deterministic ArbitrationResult per claim, hash-chained
// into FusionRecord. This package consumes that same append-only record
// stream (Ingest) and builds three derived, replayable views that fusion
// alone does not provide:
//
//  1. Conflict Graph: which claims are in active, unresolved contention
//     (multiple values with non-trivial confidence still competing), and
//     which sources are structurally opposed to each other across many
//     claims (an "adversarial pair" signal fusion cannot see, because
//     fusion only ever looks at one claim at a time).
//  2. Evidence Decay + Confidence Evolution: how a claim's winning
//     confidence moved, tick over tick, as new evidence arrived —
//     answering "how did the truth evolve", not just "what is the truth
//     now". Decay models source evidence going stale over logical time
//     (older evidence contributes less to a source's current standing).
//  3. Source Arbitration Ledger: a derived (not authoritative — fusion's
//     SourceProfile.BaseReliability remains authoritative for scoring)
//     track record of how often each source has been on the winning vs.
//     losing side of an arbitration, and its current contradiction
//     score (how often it actively conflicts with the majority).
//
// Design invariants (same discipline as kg/fusion/causal/decision):
//   - No time.Now() — everything keyed on logical Tick.
//   - No UUIDs — every derived ID is content-addressed.
//   - Append-only: Ingest never mutates a past record; all views are
//     recomputed deterministically from the ingested log.
//   - Deterministic replay: Rebuild(log) on an independent engine
//     produces byte-identical head hash and byte-identical query
//     results, proven by TestRebuildIsDeterministic.
package contradiction

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
	ErrTamperDetected = errors.New("contradiction: hash chain verification failed")
)

// Observation is one ingested arbitration outcome for a single claim at a
// single tick. It is the minimal projection of fusion.FusionRecord /
// fusion.ArbitrationResult this package needs — kept as a local type (not
// an import of fusion.ArbitrationResult) so this package can also ingest
// synthetic/test data without constructing a full fusion.Engine.
type Observation struct {
	ClaimKey           string
	Winner             string
	WinnerConfidence   float64
	RunnerUp           string
	RunnerUpConfidence float64
	Contradiction      bool
	// WinningSources / LosingSources: which sources' evidence backed the
	// winning value vs. a losing (contested) value at this observation.
	WinningSources []string
	LosingSources  []string
	Tick           uint64
}

// Record is one hash-chained entry in the contradiction engine's own
// append-only log — the audit artifact for "how did truth evolve".
type Record struct {
	Index       uint64
	ClaimKey    string
	Observation Observation
	// ContradictionScore in [0,1]: 0 = fully agreed, 1 = maximally
	// contested (winner and runner-up near-equal confidence).
	ContradictionScore float64
	// ConfidenceDelta: WinnerConfidence at this tick minus the previous
	// observation's WinnerConfidence for the same claim (0 if first).
	ConfidenceDelta float64
	PrevHash        string
	Hash            string
}

// SourceStanding is the derived, read-only arbitration track record for
// one source, as of the most recently ingested observation.
type SourceStanding struct {
	SourceID         string
	Wins             int
	Losses           int
	ContradictionsIn int // observations this source appeared in that were flagged Contradiction
	Observations     int
	CurrentScore     float64 // wins / max(1, wins+losses) — 1.0 = always on the winning side
	LastSeenTick     uint64
}

// Engine is the live Truth & Contradiction Intelligence Engine.
type Engine struct {
	mu sync.RWMutex

	log  []Record
	head string

	// lastWinnerConfidence tracks, per claim, the previous observation's
	// WinnerConfidence — used to compute ConfidenceDelta (truth evolution).
	lastWinnerConfidence map[string]float64
	// evolution: per claim, ordered list of record indices — the "truth
	// evolution" timeline query surface.
	evolution map[string][]uint64

	// standings: derived per-source track record, rebuilt incrementally.
	standings map[string]*SourceStanding
}

func NewEngine() *Engine {
	return &Engine{
		lastWinnerConfidence: make(map[string]float64),
		evolution:            make(map[string][]uint64),
		standings:            make(map[string]*SourceStanding),
	}
}

// contradictionScore computes how contested an observation is: when the
// runner-up's confidence approaches the winner's, the claim is highly
// contested (score -> 1). When there is no runner-up (uncontested single
// value), score is 0. Deliberately a pure function of the two confidence
// values so it is trivially replay-deterministic.
func contradictionScore(winnerConf, runnerUpConf float64) float64 {
	if winnerConf <= 0 {
		return 0
	}
	if runnerUpConf <= 0 {
		return 0
	}
	ratio := runnerUpConf / winnerConf
	if ratio > 1 {
		ratio = 1
	}
	return ratio
}

func canonicalStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}

func canonicalBytes(idx uint64, o Observation, score, delta float64, prev string) []byte {
	return []byte(fmt.Sprintf(
		"idx=%d|claim=%s|winner=%s|wconf=%.10f|runnerup=%s|rconf=%.10f|contra=%t|wsrc=%v|lsrc=%v|tick=%d|score=%.10f|delta=%.10f|prev=%s|",
		idx, o.ClaimKey, o.Winner, o.WinnerConfidence, o.RunnerUp, o.RunnerUpConfidence, o.Contradiction,
		canonicalStrings(o.WinningSources), canonicalStrings(o.LosingSources), o.Tick, score, delta, prev,
	))
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Ingest records one arbitration observation, deriving contradiction
// score and confidence delta, updating the source arbitration ledger, and
// appending a hash-chained Record. Safe for concurrent use.
func (e *Engine) Ingest(o Observation) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ingestLocked(o)
}

func (e *Engine) ingestLocked(o Observation) (Record, error) {
	if o.ClaimKey == "" {
		return Record{}, errors.New("contradiction: empty claim key")
	}
	score := contradictionScore(o.WinnerConfidence, o.RunnerUpConfidence)
	prevConf, hadPrev := e.lastWinnerConfidence[o.ClaimKey]
	delta := 0.0
	if hadPrev {
		delta = o.WinnerConfidence - prevConf
	}

	index := uint64(len(e.log)) + 1
	prev := e.head
	cb := canonicalBytes(index, o, score, delta, prev)
	h := hashOf(cb)

	rec := Record{
		Index:              index,
		ClaimKey:           o.ClaimKey,
		Observation:        o,
		ContradictionScore: score,
		ConfidenceDelta:    delta,
		PrevHash:           prev,
		Hash:               h,
	}

	e.log = append(e.log, rec)
	e.head = h
	e.lastWinnerConfidence[o.ClaimKey] = o.WinnerConfidence
	e.evolution[o.ClaimKey] = append(e.evolution[o.ClaimKey], index)

	for _, s := range o.WinningSources {
		st := e.standingFor(s)
		st.Wins++
		st.Observations++
		st.LastSeenTick = o.Tick
		if o.Contradiction {
			st.ContradictionsIn++
		}
	}
	for _, s := range o.LosingSources {
		st := e.standingFor(s)
		st.Losses++
		st.Observations++
		st.LastSeenTick = o.Tick
		if o.Contradiction {
			st.ContradictionsIn++
		}
	}
	for _, s := range append(append([]string{}, o.WinningSources...), o.LosingSources...) {
		st := e.standings[s]
		denom := st.Wins + st.Losses
		if denom > 0 {
			st.CurrentScore = float64(st.Wins) / float64(denom)
		}
	}

	return rec, nil
}

func (e *Engine) standingFor(sourceID string) *SourceStanding {
	st, ok := e.standings[sourceID]
	if !ok {
		st = &SourceStanding{SourceID: sourceID}
		e.standings[sourceID] = st
	}
	return st
}

// EvidenceDecay applies exponential decay to a base reliability score as
// a function of ticks-since-last-seen. halfLifeTicks controls how fast
// stale evidence loses standing: after halfLifeTicks logical ticks with
// no fresh evidence, a source's effective standing is halved. Pure
// function — no state, always replay-safe.
func EvidenceDecay(baseScore float64, ticksSinceLastSeen uint64, halfLifeTicks uint64) float64 {
	if halfLifeTicks == 0 {
		return baseScore
	}
	decayFactor := math.Pow(0.5, float64(ticksSinceLastSeen)/float64(halfLifeTicks))
	return baseScore * decayFactor
}

// ActiveConflicts returns every claim whose most recent observation is
// still contested above threshold (e.g. 0.6 = runner-up within 60% of
// the winner's confidence) — the "conflict graph" surface: claims still
// in active dispute, sorted for determinism.
func (e *Engine) ActiveConflicts(threshold float64) []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Record
	claims := make([]string, 0, len(e.evolution))
	for c := range e.evolution {
		claims = append(claims, c)
	}
	sort.Strings(claims)
	for _, c := range claims {
		idxs := e.evolution[c]
		last := e.log[idxs[len(idxs)-1]-1]
		if last.ContradictionScore >= threshold {
			out = append(out, last)
		}
	}
	return out
}

// TruthEvolution returns the ordered sequence of records for a claim,
// showing exactly how the winning value/confidence moved over time.
func (e *Engine) TruthEvolution(claimKey string) []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	idxs := e.evolution[claimKey]
	out := make([]Record, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, e.log[i-1])
	}
	return out
}

// SourceStandings returns a defensive, sorted copy of the derived source
// arbitration ledger — sorted by SourceID for determinism.
func (e *Engine) SourceStandings() []SourceStanding {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]SourceStanding, 0, len(e.standings))
	for _, st := range e.standings {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out
}

// AdversarialPairs finds source pairs that have been on opposite sides
// (one winning, one losing on the SAME observation) more than
// minCoOccurrences times — a signal fusion's single-claim view cannot
// surface: which sources are structurally opposed across the whole
// evidence stream, not just one claim.
func (e *Engine) AdversarialPairs(minCoOccurrences int) []AdversarialPair {
	e.mu.RLock()
	defer e.mu.RUnlock()
	counts := make(map[string]int)
	for _, rec := range e.log {
		for _, w := range rec.Observation.WinningSources {
			for _, l := range rec.Observation.LosingSources {
				key := pairKey(w, l)
				counts[key]++
			}
		}
	}
	var out []AdversarialPair
	for key, n := range counts {
		if n >= minCoOccurrences {
			a, b := splitPairKey(key)
			out = append(out, AdversarialPair{SourceA: a, SourceB: b, CoOccurrences: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CoOccurrences != out[j].CoOccurrences {
			return out[i].CoOccurrences > out[j].CoOccurrences
		}
		if out[i].SourceA != out[j].SourceA {
			return out[i].SourceA < out[j].SourceA
		}
		return out[i].SourceB < out[j].SourceB
	})
	return out
}

type AdversarialPair struct {
	SourceA       string
	SourceB       string
	CoOccurrences int
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

func splitPairKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.head
}

// Snapshot returns a defensive copy of the full record log for export or
// audit — every field needed to rebuild the engine deterministically.
func (e *Engine) Snapshot() []Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Record, len(e.log))
	copy(out, e.log)
	return out
}

// VerifyChain re-derives every hash from canonical bytes and confirms
// chain linkage, without trusting the stored Hash field.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for i, rec := range e.log {
		wantIndex := uint64(i) + 1
		if rec.Index != wantIndex {
			return fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		if rec.PrevHash != prev {
			return fmt.Errorf("%w: broken chain linkage at index %d", ErrTamperDetected, rec.Index)
		}
		cb := canonicalBytes(rec.Index, rec.Observation, rec.ContradictionScore, rec.ConfidenceDelta, rec.PrevHash)
		recomputed := hashOf(cb)
		if recomputed != rec.Hash {
			return fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, rec.Index)
		}
		prev = recomputed
	}
	return nil
}

// Rebuild reconstructs a fresh Engine from an exported record log,
// re-deriving contradiction scores/deltas/standings from the raw
// Observations (not trusting the stored derived fields), and verifying
// the hash chain in the process. Proves deterministic replay.
func Rebuild(log []Record) (*Engine, error) {
	e := NewEngine()
	for i, rec := range log {
		wantIndex := uint64(i) + 1
		if rec.Index != wantIndex {
			return nil, fmt.Errorf("%w: index gap at %d", ErrTamperDetected, i)
		}
		got, err := e.ingestLocked(rec.Observation)
		if err != nil {
			return nil, fmt.Errorf("contradiction: replay ingest failed at %d: %w", rec.Index, err)
		}
		if got.Hash != rec.Hash {
			return nil, fmt.Errorf("%w: hash mismatch at index %d", ErrTamperDetected, rec.Index)
		}
	}
	return e, nil
}
