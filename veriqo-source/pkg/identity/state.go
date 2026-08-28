package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// state is the resolution state derived by folding the event ledger up
// to a transaction tick. It is never stored — always recomputed — so
// there is exactly one source of truth (the ledger) and no possibility
// of state and ledger disagreeing.
type state struct {
	// parent implements union-find over MERGED identifiers, honouring
	// unmerges by rebuilding from scratch on each fold.
	parent map[string]string
	// assertions[a][b] = accumulated (noisy-OR) belief that a and b
	// denote the same entity, from ASSERT events.
	assertions map[string]map[string]float64
	// reasons[a][b] carries the human-readable justification.
	reasons map[string]map[string][]string
	// corrections maps a wrong identifier key to its replacement.
	corrections map[string]string
	// known is every identifier key seen.
	known map[string]Identifier
}

// foldLocked replays the ledger prefix with TxTick <= asOf. Caller
// must hold at least a read lock.
func (r *Resolver) foldLocked(asOf uint64) *state {
	st := &state{
		parent:      map[string]string{},
		assertions:  map[string]map[string]float64{},
		reasons:     map[string]map[string][]string{},
		corrections: map[string]string{},
		known:       map[string]Identifier{},
	}
	// Unmerged pairs are collected first so a MERGE that a later
	// UNMERGE retracts is never applied — but ONLY when the unmerge is
	// itself within the asOf window. Replaying an earlier tick still
	// sees the merge, which is the historical-validity invariant.
	unmerged := map[[2]string]bool{}
	for _, e := range r.ledger {
		if e.TxTick > asOf {
			continue
		}
		if e.Type == EventUnmerge {
			unmerged[pairKey(e.A.Key(), e.B.Key())] = true
		}
	}
	for _, e := range r.ledger {
		if e.TxTick > asOf {
			continue
		}
		st.known[e.A.Key()] = e.A
		st.known[e.B.Key()] = e.B
		switch e.Type {
		case EventAssert:
			st.addAssertion(e.A.Key(), e.B.Key(), e.Confidence,
				fmt.Sprintf("%s asserted %s~%s (conf %.2f): %s", e.SourceID, e.A.Key(), e.B.Key(), e.Confidence, e.Reason))
		case EventMerge:
			if !unmerged[pairKey(e.A.Key(), e.B.Key())] {
				st.union(e.A.Key(), e.B.Key())
				st.addAssertion(e.A.Key(), e.B.Key(), 1,
					fmt.Sprintf("merged by %s: %s", e.ActorID, e.Reason))
			}
		case EventCorrect:
			st.corrections[e.A.Key()] = e.B.Key()
		}
	}
	return st
}

func pairKey(a, b string) [2]string {
	if a < b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

func (s *state) addAssertion(a, b string, conf float64, reason string) {
	add := func(x, y string) {
		if s.assertions[x] == nil {
			s.assertions[x] = map[string]float64{}
			s.reasons[x] = map[string][]string{}
		}
		// Noisy-OR: independent corroboration saturates toward 1.
		prev := s.assertions[x][y]
		s.assertions[x][y] = prev + conf*(1-prev)
		s.reasons[x][y] = append(s.reasons[x][y], reason)
	}
	add(a, b)
	add(b, a)
}

func (s *state) find(x string) string {
	if _, ok := s.parent[x]; !ok {
		return ""
	}
	for s.parent[x] != x {
		s.parent[x] = s.parent[s.parent[x]]
		x = s.parent[x]
	}
	return x
}

func (s *state) root(x string) string { return s.find(x) }

func (s *state) union(a, b string) {
	for _, k := range []string{a, b} {
		if _, ok := s.parent[k]; !ok {
			s.parent[k] = k
		}
	}
	ra, rb := s.find(a), s.find(b)
	if ra == rb {
		return
	}
	// Deterministic: the lexicographically smaller root always wins,
	// which makes the resulting entity ID merge-order invariant.
	if ra < rb {
		s.parent[rb] = ra
	} else {
		s.parent[ra] = rb
	}
}

// membersOf returns every identifier key currently bound to the same
// entity as x, sorted.
func (s *state) membersOf(x string) []string {
	root := s.find(x)
	if root == "" {
		return []string{x}
	}
	var out []string
	for k := range s.parent {
		if s.find(k) == root {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// entityIDOf derives a content-addressed canonical entity ID from the
// SET of bound identifiers — merge-order invariant by construction.
func entityIDOf(members []string) string {
	sorted := append([]string(nil), members...)
	sort.Strings(sorted)
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.entity/v1|")
	for _, m := range sorted {
		fmt.Fprintf(h, "%s|", m)
	}
	return "ENT:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// candidatesFor generates and scores every entity the query could
// denote. Two mechanisms contribute:
//   - MERGED membership: a hard binding, confidence 1.0.
//   - ASSERTION paths: soft evidence, confidence = accumulated
//     noisy-OR belief, transitively decayed one hop.
func (s *state) candidatesFor(q Identifier) []Candidate {
	key := q.Key()
	if repl, ok := s.corrections[key]; ok {
		key = repl
	}
	scores := map[string]float64{}
	matched := map[string][]string{}

	// Hard binding first.
	if s.find(key) != "" {
		members := s.membersOf(key)
		id := entityIDOf(members)
		scores[id] = 1
		matched[id] = append(matched[id], fmt.Sprintf("bound to %d merged identifier(s): %v", len(members), members))
	}

	// Soft assertion evidence: each asserted partner suggests the
	// entity that partner currently belongs to (or a singleton entity
	// for that partner).
	partners := make([]string, 0, len(s.assertions[key]))
	for p := range s.assertions[key] {
		partners = append(partners, p)
	}
	sort.Strings(partners)
	for _, p := range partners {
		conf := s.assertions[key][p]
		var id string
		if s.find(p) != "" {
			id = entityIDOf(s.membersOf(p))
		} else {
			id = entityIDOf([]string{p})
		}
		prev := scores[id]
		scores[id] = prev + conf*(1-prev)
		reasons := s.reasons[key][p]
		if len(reasons) > 0 {
			matched[id] = append(matched[id], reasons...)
		}
	}

	// A never-seen identifier is its own singleton entity at low
	// confidence — "we have no reason to think this is anything else".
	if len(scores) == 0 {
		id := entityIDOf([]string{key})
		scores[id] = discriminatingPower[q.Kind]
		matched[id] = []string{"no linking evidence; treated as its own entity"}
	}

	out := make([]Candidate, 0, len(scores))
	for id, sc := range scores {
		ms := append([]string(nil), matched[id]...)
		sort.Strings(ms)
		out = append(out, Candidate{EntityID: id, Score: sc, Confidence: sc, Matched: ms})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].EntityID < out[j].EntityID
	})
	return out
}

// EntityIDAt returns the canonical entity ID an identifier resolves to
// at a transaction tick — the function historical replay calls.
func (r *Resolver) EntityIDAt(q Identifier, asOfTick uint64) (string, error) {
	if err := q.Validate(); err != nil {
		return "", err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := r.foldLocked(asOfTick)
	key := q.Key()
	if repl, ok := st.corrections[key]; ok {
		key = repl
	}
	if st.find(key) != "" {
		return entityIDOf(st.membersOf(key)), nil
	}
	return entityIDOf([]string{key}), nil
}

// SameEntityAt reports whether two identifiers were bound to the same
// entity as of a transaction tick.
func (r *Resolver) SameEntityAt(a, b Identifier, asOfTick uint64) (bool, error) {
	ida, err := r.EntityIDAt(a, asOfTick)
	if err != nil {
		return false, err
	}
	idb, err := r.EntityIDAt(b, asOfTick)
	if err != nil {
		return false, err
	}
	return ida == idb, nil
}
