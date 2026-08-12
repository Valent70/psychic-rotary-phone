// Package fusion implements Veriqo's MOAT Multi-Source Evidence Fusion and
// Truth Arbitration Engine — the layer both technical audits (Masalah
// terbesar nomor 1-3, Berikut_analisa_deep_dive) identify as the actual
// commercial differentiator, sitting above the Raft trust substrate
// (pkg/consensus/raftlite) and the Knowledge Graph (pkg/moat/kg).
//
// Problem it solves: multiple independent sources (AIS providers, SAR
// imagery, port records, sanctions lists, customs declarations) assert
// conflicting values for the same real-world claim (e.g. a vessel's flag
// state, position, or ownership). Veriqo must decide — deterministically,
// auditably, and explainably — which value is "truth", propagate
// confidence across corroborating evidence, and flag contradictions for
// human/analyst review rather than silently picking a side.
//
// Design invariants (same discipline as raftlite/kg):
//   - No time.Now() — every event carries an explicit logical Tick.
//   - No UUIDs — every ID is SHA-256 derived from canonical content.
//   - Append-only evidence log; arbitration never mutates past evidence.
//   - Deterministic replay: ReplayAll() on independently-built engines
//     from the same evidence stream produces a byte-identical chain hash,
//     regardless of ingestion order (evidence is re-sorted canonically
//     before every fusion computation, not processed in arrival order).
//   - Every arbitration is explainable: the result carries a step-by-step
//     Explanation, not just a winning value.
//   - Every arbitration is a single ordered write into kg.Graph (when a
//     graph sink is attached), matching the deep-dive audit's own
//     recommendation that "every KG mutation must originate from one
//     ordered entry" — the fusion engine is that ordering actor for the
//     truth layer, the same way raftlite.Adapter is for cluster topology.
package fusion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"veriqo/pkg/moat/kg"
)

var (
	ErrUnknownSource      = errors.New("fusion: source not registered")
	ErrInvalidReliability = errors.New("fusion: reliability must be in (0,1]")
	ErrNoEvidence         = errors.New("fusion: no evidence for claim")
	ErrTamperDetected     = errors.New("fusion: hash chain verification failed")
	ErrDuplicateEvidence  = errors.New("fusion: evidence with this ID already submitted")
)

// SourceID identifies an evidence provider (AIS feed, SAR imagery vendor,
// sanctions list, port authority record system, customs system, etc).
type SourceID string

// SourceProfile is the fusion engine's prior belief in a source's
// trustworthiness, in (0,1]. It is looked up at submission time and
// copied into the Evidence record — later re-scoring a source does not
// retroactively change past evidence, preserving replay determinism.
type SourceProfile struct {
	ID              SourceID
	BaseReliability float64
	// Provider identifies the upstream data lineage this source draws
	// from (e.g. a satellite feed operator two AIS vendors both resell).
	// Sources sharing a non-empty Provider are treated as correlated,
	// not independent, in fuseGroup — see correlation fix below. Empty
	// Provider means "this source is its own independent provider".
	Provider string
}

// Claim identifies a single real-world assertion slot: "what is the value
// of Predicate for Subject". Two pieces of evidence are "about the same
// thing" iff their Claim.Key() matches.
type Claim struct {
	Subject   string // e.g. "VESSEL:IMO9876543"
	Predicate string // e.g. "FLAG_STATE", "AIS_POSITION", "BENEFICIAL_OWNER"
}

// Key returns the canonical, deterministic identity string for a Claim.
func (c Claim) Key() string { return c.Subject + "|" + c.Predicate }

// Evidence is one immutable, source-attributed assertion about a Claim.
type Evidence struct {
	ID          string // SHA-256(claim.Key|source|value|tick), assigned by Submit
	Claim       Claim
	Source      SourceID
	Value       string  // canonical string form of the asserted value
	Reliability float64 // copied from SourceProfile at submission time
	Provider    string  // copied from SourceProfile at submission time
	Tick        uint64
}

// deriveEvidenceID computes the deterministic content-addressed ID for a
// piece of evidence. No randomness, no clock — pure function of content.
func deriveEvidenceID(c Claim, src SourceID, value string, tick uint64) string {
	raw := fmt.Sprintf("claim=%s|source=%s|value=%s|tick=%d", c.Key(), src, value, tick)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// FusionRecord is one hash-chained entry in the arbitration audit log —
// the fusion-layer analogue of kg.Mutation. It is the artifact an
// investor or auditor verifies to confirm a truth decision was reached
// deterministically from disclosed evidence, not asserted by fiat.
type FusionRecord struct {
	Index              uint64
	ActorID            string
	ClaimKey           string
	Winner             string
	WinnerConfidence   float64
	RunnerUp           string
	RunnerUpConfidence float64
	Contradiction      bool
	EvidenceIDs        []string // sorted, canonical
	Tick               uint64
	PrevHash           string
	Hash               string
}

// ArbitrationResult is the caller-facing output of Arbitrate — everything
// needed to explain and act on a truth decision.
type ArbitrationResult struct {
	Claim              Claim
	Winner             string
	WinnerConfidence   float64
	RunnerUp           string
	RunnerUpConfidence float64
	Contradiction      bool
	EvidenceCount      int
	Explanation        []string
}

// Engine is the live Multi-Source Evidence Fusion + Truth Arbitration
// engine. It is safe for concurrent use.
type Engine struct {
	mu       sync.RWMutex
	sources  map[SourceID]SourceProfile
	evidence map[string][]Evidence // claim key -> append-only evidence
	seenID   map[string]bool
	log      []FusionRecord
	head     string
	graph    *kg.Graph // optional deterministic sink; nil = fusion-only mode
}

// NewEngine constructs a fusion engine. graph may be nil to run the
// fusion/arbitration logic standalone without writing into the Knowledge
// Graph (useful for unit testing the math in isolation).
func NewEngine(graph *kg.Graph) *Engine {
	return &Engine{
		sources:  make(map[SourceID]SourceProfile),
		evidence: make(map[string][]Evidence),
		seenID:   make(map[string]bool),
		graph:    graph,
	}
}

// RegisterSource records a source's prior reliability. Re-registering an
// existing source updates its profile for *future* Submit calls only.
func (e *Engine) RegisterSource(p SourceProfile) error {
	if !validReliability(p.BaseReliability) {
		return ErrInvalidReliability
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sources[p.ID] = p
	return nil
}

func validReliability(r float64) bool {
	return r > 0 && r <= 1
}

// Submit ingests one piece of evidence. The evidence ID is derived
// deterministically from its content, so submitting byte-identical
// evidence twice (same claim/source/value/tick) is rejected as a
// duplicate rather than silently double-counted.
func (e *Engine) Submit(src SourceID, claim Claim, value string, tick uint64) (Evidence, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	profile, ok := e.sources[src]
	if !ok {
		return Evidence{}, ErrUnknownSource
	}

	id := deriveEvidenceID(claim, src, value, tick)
	if e.seenID[id] {
		return Evidence{}, ErrDuplicateEvidence
	}

	ev := Evidence{
		ID:          id,
		Claim:       claim,
		Source:      src,
		Value:       value,
		Reliability: profile.BaseReliability,
		Provider:    profile.Provider,
		Tick:        tick,
	}
	e.evidence[claim.Key()] = append(e.evidence[claim.Key()], ev)
	e.seenID[id] = true
	return ev, nil
}

// canonicalOrder returns a claim's evidence sorted deterministically by
// (Tick, Source, ID) — independent of Go map iteration or arrival order,
// so fusion math always consumes evidence in the same sequence on every
// replay.
func canonicalOrder(list []Evidence) []Evidence {
	out := make([]Evidence, len(list))
	copy(out, list)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tick != out[j].Tick {
			return out[i].Tick < out[j].Tick
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// fuseGroup combines evidence supporting the same value into one fused
// confidence, correctly accounting for source correlation (the gap named
// explicitly in Yang_masih_kurang.docx: three AIS vendors reselling the
// same satellite feed are NOT three independent 0.9 witnesses — naive
// noisy-OR across them is overconfident).
//
// Method: evidence is clustered by effective provider (Evidence.Provider,
// or the source ID itself when Provider is empty — an undeclared-lineage
// source is assumed independent). Within a provider cluster, correlated
// evidence adds no independent information, so only the single
// highest-reliability item counts. Across distinct provider clusters,
// evidence IS independent and combined via noisy-OR in log-space:
//
//	combined = 1 - Π_providers (1 - max_reliability_in_provider)
//
// This is a strict generalization of plain noisy-OR: when every source
// declares a distinct (or empty) Provider — the common case — it
// collapses to exactly the original formula, so it changes no prior
// behavior for uncorrelated data.
func fuseGroup(group []Evidence) float64 {
	byProvider := make(map[string]float64)
	for _, ev := range group {
		key := ev.Provider
		if key == "" {
			key = string(ev.Source)
		}
		if r := ev.Reliability; r > byProvider[key] {
			byProvider[key] = r
		}
	}
	keys := make([]string, 0, len(byProvider))
	for k := range byProvider {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic regardless of map iteration order

	logComplement := 0.0
	for _, k := range keys {
		logComplement += math.Log(1 - byProvider[k])
	}
	return 1 - math.Exp(logComplement)
}

// Arbitrate performs Truth Arbitration for a claim: it groups all
// submitted evidence by asserted value, fuses confidence within each
// group, and deterministically selects the winning value. Ties in fused
// confidence are broken by lexicographically smallest value string, then
// by earliest supporting tick — both pure functions of stored data, so
// the outcome never depends on wall-clock or goroutine scheduling.
func (e *Engine) Arbitrate(actorID string, claim Claim, tick uint64) (ArbitrationResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	all := e.evidence[claim.Key()]
	if len(all) == 0 {
		return ArbitrationResult{}, ErrNoEvidence
	}
	ordered := canonicalOrder(all)

	groups := make(map[string][]Evidence)
	var values []string
	for _, ev := range ordered {
		if _, exists := groups[ev.Value]; !exists {
			values = append(values, ev.Value)
		}
		groups[ev.Value] = append(groups[ev.Value], ev)
	}
	sort.Strings(values) // deterministic base ordering before scoring

	scoredValues := make([]scoredValue, 0, len(values))
	for _, v := range values {
		g := groups[v]
		conf := fuseGroup(g)
		firstTick := g[0].Tick
		for _, ev := range g {
			if ev.Tick < firstTick {
				firstTick = ev.Tick
			}
		}
		scoredValues = append(scoredValues, scoredValue{v, conf, firstTick})
	}
	sort.Slice(scoredValues, func(i, j int) bool {
		if scoredValues[i].confidence != scoredValues[j].confidence {
			return scoredValues[i].confidence > scoredValues[j].confidence // highest confidence first
		}
		if scoredValues[i].firstTick != scoredValues[j].firstTick {
			return scoredValues[i].firstTick < scoredValues[j].firstTick // earliest evidence wins ties
		}
		return scoredValues[i].value < scoredValues[j].value // lexicographic final tiebreak
	})

	winner := scoredValues[0]
	result := ArbitrationResult{
		Claim:            claim,
		Winner:           winner.value,
		WinnerConfidence: winner.confidence,
		EvidenceCount:    len(ordered),
		Contradiction:    len(scoredValues) > 1,
	}
	if len(scoredValues) > 1 {
		result.RunnerUp = scoredValues[1].value
		result.RunnerUpConfidence = scoredValues[1].confidence
	}

	result.Explanation = explain(claim, groups, scoredValues, winner.value)

	evIDs := make([]string, 0, len(ordered))
	for _, ev := range ordered {
		evIDs = append(evIDs, ev.ID)
	}
	sort.Strings(evIDs)

	rec := FusionRecord{
		Index:            uint64(len(e.log)),
		ActorID:          actorID,
		ClaimKey:         claim.Key(),
		Winner:           winner.value,
		WinnerConfidence: winner.confidence,
		Contradiction:    result.Contradiction,
		EvidenceIDs:      evIDs,
		Tick:             tick,
		PrevHash:         e.head,
	}
	if result.Contradiction {
		rec.RunnerUp = scoredValues[1].value
		rec.RunnerUpConfidence = scoredValues[1].confidence
	}
	rec.Hash = hashRecord(rec)
	e.log = append(e.log, rec)
	e.head = rec.Hash

	if e.graph != nil {
		if err := e.writeToGraph(actorID, claim, groups, result); err != nil {
			return ArbitrationResult{}, fmt.Errorf("fusion: kg write failed: %w", err)
		}
	}

	return result, nil
}

// explain renders a human-readable, deterministic reasoning trail: which
// evidence contributed to which value, and why the winner won. This is
// the "Explainable Decision" capability named explicitly in the audit
// gap list — arbitration is never a black box.
// scoredValue holds one candidate value's fused confidence and the
// earliest tick among its supporting evidence, used both to rank
// candidates in Arbitrate and to render the Explanation trail.
type scoredValue struct {
	value      string
	confidence float64
	firstTick  uint64
}

func explain(claim Claim, groups map[string][]Evidence, scored []scoredValue, winner string) []string {
	lines := []string{
		fmt.Sprintf("claim %s: %d competing value(s)", claim.Key(), len(scored)),
	}
	for _, s := range scored {
		g := groups[s.value]
		srcList := make([]string, 0, len(g))
		for _, ev := range g {
			srcList = append(srcList, fmt.Sprintf("%s(r=%.2f@t%d)", ev.Source, ev.Reliability, ev.Tick))
		}
		sort.Strings(srcList)
		tag := ""
		if s.value == winner {
			tag = " <- WINNER"
		}
		lines = append(lines, fmt.Sprintf("  value=%q fused_confidence=%.4f evidence=[%s]%s",
			s.value, s.confidence, joinComma(srcList), tag))
	}
	return lines
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// writeToGraph performs the single ordered KG write for this arbitration:
// a Claim node, an Assertion node for the winning value, an
// AssertedTruth edge between them, and a SupportsClaim edge from every
// contributing evidence's source node. This matches the deep-dive
// audit's explicit recommendation ("setiap mutation KG harus lahir dari
// satu entry [ordered]") — Arbitrate is that single ordered entry point.
func (e *Engine) writeToGraph(actorID string, claim Claim, groups map[string][]Evidence, result ArbitrationResult) error {
	claimNodeID := "claim:" + claim.Key()
	if _, err := e.graph.UpsertNode(actorID, kg.Node{
		ID:   claimNodeID,
		Kind: "Claim",
		Props: map[string]string{
			"subject":   claim.Subject,
			"predicate": claim.Predicate,
		},
	}); err != nil {
		return err
	}

	assertionNodeID := "assertion:" + claim.Key() + "::" + result.Winner
	if _, err := e.graph.UpsertNode(actorID, kg.Node{
		ID:   assertionNodeID,
		Kind: "Assertion",
		Props: map[string]string{
			"value":         result.Winner,
			"confidence":    fmt.Sprintf("%.6f", result.WinnerConfidence),
			"contradiction": fmt.Sprintf("%v", result.Contradiction),
		},
	}); err != nil {
		return err
	}

	// A re-arbitration of the same claim reuses the AssertedTruth edge ID
	// ("truth:<claimKey>"); delete-then-reinsert keeps exactly one live
	// truth edge per claim, matching Arbitrate's "latest truth" semantics.
	_, _ = e.graph.DeleteEdge(actorID, "truth:"+claim.Key())
	if _, err := e.graph.UpsertEdge(actorID, kg.Edge{
		ID:   "truth:" + claim.Key(),
		From: claimNodeID,
		To:   assertionNodeID,
		Kind: "AssertedTruth",
	}); err != nil {
		return err
	}

	for value, g := range groups {
		for _, ev := range g {
			sourceNodeID := "source:" + string(ev.Source)
			if _, err := e.graph.UpsertNode(actorID, kg.Node{
				ID:   sourceNodeID,
				Kind: "EvidenceSource",
				Props: map[string]string{
					"source_id": string(ev.Source),
				},
			}); err != nil {
				return err
			}
			targetNodeID := "assertion:" + claim.Key() + "::" + value
			if value != result.Winner {
				// Ensure non-winning assertion nodes also exist so
				// contradiction edges are queryable, not orphaned.
				if _, err := e.graph.UpsertNode(actorID, kg.Node{
					ID:   targetNodeID,
					Kind: "Assertion",
					Props: map[string]string{
						"value":         value,
						"contradiction": "true",
					},
				}); err != nil {
					return err
				}
			}
			// Each evidence ID is content-derived, so its SupportsClaim
			// edge ID is also stable; ErrEdgeExists here only occurs on
			// a re-arbitration replay and is tolerated, not an error.
			edgeID := "supports:" + ev.ID
			if _, err := e.graph.UpsertEdge(actorID, kg.Edge{
				ID:   edgeID,
				From: sourceNodeID,
				To:   targetNodeID,
				Kind: "SupportsClaim",
				Props: map[string]string{
					"reliability": fmt.Sprintf("%.6f", ev.Reliability),
					"tick":        fmt.Sprintf("%d", ev.Tick),
				},
			}); err != nil && !errors.Is(err, kg.ErrEdgeExists) {
				return err
			}
		}
	}
	return nil
}

// hashRecord computes the canonical, deterministic hash of a FusionRecord
// chained to the previous record's hash.
func hashRecord(r FusionRecord) string {
	raw := fmt.Sprintf("idx=%d|actor=%s|claim=%s|winner=%s|wconf=%.6f|runnerup=%s|rconf=%.6f|contra=%v|evidence=%v|tick=%d|prev=%s",
		r.Index, r.ActorID, r.ClaimKey, r.Winner, r.WinnerConfidence,
		r.RunnerUp, r.RunnerUpConfidence, r.Contradiction, r.EvidenceIDs, r.Tick, r.PrevHash)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// VerifyChain re-derives every record's hash from its stored fields and
// confirms the chain is unbroken — the same tamper-detection contract as
// kg.Graph.VerifyChain and raftlite's replay proofs.
func (e *Engine) VerifyChain() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prev := ""
	for _, r := range e.log {
		if r.PrevHash != prev {
			return ErrTamperDetected
		}
		want := hashRecord(FusionRecord{
			Index: r.Index, ActorID: r.ActorID, ClaimKey: r.ClaimKey,
			Winner: r.Winner, WinnerConfidence: r.WinnerConfidence,
			RunnerUp: r.RunnerUp, RunnerUpConfidence: r.RunnerUpConfidence,
			Contradiction: r.Contradiction, EvidenceIDs: r.EvidenceIDs,
			Tick: r.Tick, PrevHash: r.PrevHash,
		})
		if want != r.Hash {
			return ErrTamperDetected
		}
		prev = r.Hash
	}
	return nil
}

// ReplayAll returns a defensive copy of the full arbitration log, for
// external replay/audit verification (e.g. rebuilding an independent
// engine from the same evidence stream and comparing chain heads).
func (e *Engine) ReplayAll() []FusionRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]FusionRecord, len(e.log))
	copy(out, e.log)
	return out
}

// Head returns the current chain head hash ("" if no arbitrations yet).
func (e *Engine) Head() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.head
}

// EvidenceFor returns a defensive copy of all evidence submitted for a
// claim, in canonical order.
func (e *Engine) EvidenceFor(claim Claim) []Evidence {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return canonicalOrder(e.evidence[claim.Key()])
}
