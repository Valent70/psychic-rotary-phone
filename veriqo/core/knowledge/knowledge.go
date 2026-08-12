// Package knowledge is Veriqo's Knowledge module: a versioned,
// conflict-aware, confidence-tracked wrapper around
// pkg/kernel/reasoning.KB. Revise (not just Assert) now performs
// genuine Bayesian belief revision via pkg/core/trustcalc.CombineLogOdds
// when new evidence confirms or contradicts an existing fact, instead
// of silently overwriting the previous confidence — closing part of
// "Yang_masih_saya_kritisi.docx" §2's ask for "belief revision" as a
// real mechanism, not orchestration dressed up as intelligence.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/evidence/graph"
	"veriqo/pkg/kernel/reasoning"
)

type Version struct {
	Index      uint64
	Fact       reasoning.Triple
	Confidence float64
	Provenance graph.NodeID
	PrevHash   string
	Hash       string
}

type Conflict struct {
	Existing reasoning.Triple
	New      reasoning.Triple
}

func hashVersion(prevHash string, idx uint64, t reasoning.Triple, confidence float64, provenance graph.NodeID) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%s|%v|%s", prevHash, idx, t.Subject, t.Predicate, t.Object, confidence, provenance)))
	return hex.EncodeToString(sum[:])
}

type Store struct {
	mu         sync.RWMutex
	kb         *reasoning.KB
	history    []Version
	confidence map[string]float64
}

func NewStore() *Store {
	return &Store{kb: reasoning.NewKB(), confidence: make(map[string]float64)}
}

func factKey(t reasoning.Triple) string { return t.Subject + "|" + t.Predicate + "|" + t.Object }

func (s *Store) Conflicts(t reasoning.Triple) []Conflict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Conflict
	for _, existing := range s.kb.Facts() {
		if existing.Subject == t.Subject && existing.Predicate == t.Predicate && existing.Object != t.Object {
			out = append(out, Conflict{Existing: existing, New: t})
		}
	}
	return out
}

// Assert records a fact with a confidence score and optional evidence
// provenance, appending a new hash-chained Version. Does not perform
// belief revision against a prior confidence for the SAME fact — use
// Revise for that.
func (s *Store) Assert(t reasoning.Triple, confidence float64, provenance graph.NodeID) Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assertLocked(t, confidence, provenance)
}

func (s *Store) assertLocked(t reasoning.Triple, confidence float64, provenance graph.NodeID) Version {
	s.kb.Assert(t)
	s.confidence[factKey(t)] = confidence

	idx := uint64(len(s.history) + 1)
	prevHash := ""
	if len(s.history) > 0 {
		prevHash = s.history[len(s.history)-1].Hash
	}
	v := Version{Index: idx, Fact: t, Confidence: confidence, Provenance: provenance, PrevHash: prevHash}
	v.Hash = hashVersion(prevHash, idx, t, confidence, provenance)
	s.history = append(s.history, v)
	return v
}

// Revise performs genuine Bayesian belief revision for a fact that may
// already have a recorded confidence: if the fact was never asserted,
// this is equivalent to Assert. If it was, the existing confidence is
// treated as the prior probability and likelihoodRatio (P(new evidence
// | fact true) / P(new evidence | fact false)) is combined via
// trustcalc.CombineLogOdds to produce a revised posterior confidence —
// not an overwrite, an update.
func (s *Store) Revise(t reasoning.Triple, likelihoodRatio float64, provenance graph.NodeID) Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, existed := s.confidence[factKey(t)]
	if !existed {
		prior = 0.5 // maximally uninformative prior for a never-seen fact
	}
	revised := trustcalc.CombineLogOdds(prior, likelihoodRatio)
	return s.assertLocked(t, revised, provenance)
}

func (s *Store) Confidence(t reasoning.Triple) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.confidence[factKey(t)]
	return c, ok
}

func (s *Store) Infer() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kb.Infer()
}

func (s *Store) AddRule(r reasoning.Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kb.AddRule(r)
}

func (s *Store) Query(pattern reasoning.Triple) []reasoning.Triple {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kb.Query(pattern)
}

func (s *Store) History() []Version {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Version, len(s.history))
	copy(out, s.history)
	return out
}

func (s *Store) VerifyChain() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prevHash := ""
	for _, v := range s.history {
		if v.PrevHash != prevHash {
			return fmt.Errorf("knowledge: version %d: prev hash mismatch", v.Index)
		}
		want := hashVersion(v.PrevHash, v.Index, v.Fact, v.Confidence, v.Provenance)
		if want != v.Hash {
			return fmt.Errorf("knowledge: version %d: hash mismatch (tampered)", v.Index)
		}
		prevHash = v.Hash
	}
	return nil
}

// PropagateConfidence combines premise confidences for a derived fact
// via the noisy-AND rule (product of independent premise confidences)
// — the same combination principle pkg/moat/causal.AggregateSupport
// uses for its noisy-OR, applied here to conjunctive premises: a
// derived fact is only as confident as the weakest link, and multiple
// uncertain premises compound. Used by veriqo/core/intelligence when
// asserting a fact derived through reasoning.KB.Infer from premises
// with known confidences, rather than assigning derived facts a flat
// default confidence.
func PropagateConfidence(premiseConfidences []float64) float64 {
	if len(premiseConfidences) == 0 {
		return 0
	}
	product := 1.0
	for _, c := range premiseConfidences {
		if c < 0 {
			c = 0
		}
		if c > 1 {
			c = 1
		}
		product *= c
	}
	return product
}
