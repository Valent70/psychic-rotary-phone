// records.go closes PHASE 1 of the v7.10.3 production-readiness audit:
// the v7.10.3 graph was an in-memory adjacency map with no record
// model, no thread safety, no content addressing, no replay and no
// tamper detection — "available as an independent package" but not
// something a production system could be audited on.
//
// This file adds the audit's REQUIRED DependencyRecord model and turns
// the graph into a first-class VERIQO ledger-bearing engine with the
// same discipline every other engine in this repo already has:
// hash-chained records, VerifyChain(), ReplayAll(), Rebuild() and a
// content-addressed RootHash() that can be embedded in a certificate.
//
// It also adds what the audit's DependencyRecord fields imply but did
// not spell out: TEMPORAL validity. A dependency is not eternal — a
// vendor resells satellite provider X from tick 100 to tick 900 and
// then switches provider. Discounting evidence at tick 1000 for a
// dependency that expired at tick 900 would be as wrong as missing it
// at tick 500. EffectiveWeightAt is therefore the temporal form, and
// EffectiveWeight (kept for API compatibility) is the "all edges
// always valid" special case.
package evidencegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// Errors returned by the record/ledger layer.
var (
	ErrEmptyNode           = errors.New("evidencegraph: DependencyRecord.NodeID must be non-empty")
	ErrEmptyUpstream       = errors.New("evidencegraph: DependencyRecord.UpstreamID must be non-empty")
	ErrSelfDependency      = errors.New("evidencegraph: a node cannot depend on itself")
	ErrBadConfidence       = errors.New("evidencegraph: DependencyRecord.Confidence must be in [0,1]")
	ErrBadValidity         = errors.New("evidencegraph: ValidTo must be 0 (open-ended) or >= ValidFrom")
	ErrUnknownKind         = errors.New("evidencegraph: unknown DependencyKind")
	ErrChainBroken         = errors.New("evidencegraph: dependency ledger hash chain is broken")
	ErrRecordTampered      = errors.New("evidencegraph: dependency record hash does not match its own fields")
	ErrDuplicateDependency = errors.New("evidencegraph: identical dependency record already declared")
)

// DependencyRecord is the audit's required record model, verbatim in
// field set, plus the three fields every ledgered record in this repo
// carries (Index/PrevHash/Hash) so a dependency declaration is as
// auditable as a fusion arbitration or a trust certificate.
//
// Semantics of the identity fields:
//   - NodeID          : the evidence node that DEPENDS (usually a source ID)
//   - UpstreamID      : what it depends ON (feed, producer, model, transform)
//   - Kind            : why they are correlated (see DependencyKind)
//   - Confidence      : how sure we are the dependency is real, [0,1].
//     A dependency we are only 50% sure of discounts
//     half as hard as one we are certain of.
//   - SourceID        : originating raw source, if distinct from NodeID
//   - ProducerID      : the organisation/system that produced the artifact
//   - TransformationID: the deterministic transform applied (model id,
//     algorithm version, enrichment pipeline id)
//   - ValidFrom/ValidTo: temporal validity window in VERIQO ticks;
//     ValidTo == 0 means "still valid"
//   - TxTick          : the tick at which this dependency was DECLARED
//     (transaction time — distinct from valid time; this
//     is bitemporality, and it is what lets a replay of
//     "what did VERIQO know at tick T" be honest)
//   - EvidenceHash    : content hash of the evidence artifact this record
//     was derived from, if any
type DependencyRecord struct {
	NodeID           NodeID
	UpstreamID       NodeID
	Kind             DependencyKind
	Confidence       float64
	SourceID         string
	ProducerID       string
	TransformationID string
	ValidFrom        uint64
	ValidTo          uint64
	TxTick           uint64
	EvidenceHash     string

	// Ledger fields, assigned by Declare — never by the caller.
	Index    uint64
	PrevHash string
	Hash     string
}

// validKind reports whether k is one of the four modelled kinds.
func validKind(k DependencyKind) bool {
	switch k {
	case DependsOnSource, DependsOnProducer, DependsOnDerivation, DependsOnTransformation:
		return true
	}
	return false
}

// contentKey is the deterministic identity of a dependency ASSERTION,
// excluding ledger position — used for duplicate rejection.
func (r DependencyRecord) contentKey() string {
	return fmt.Sprintf("node=%s|up=%s|kind=%s|conf=%.6f|src=%s|prod=%s|xf=%s|from=%d|to=%d|tx=%d|ev=%s",
		r.NodeID, r.UpstreamID, r.Kind, r.Confidence, r.SourceID, r.ProducerID,
		r.TransformationID, r.ValidFrom, r.ValidTo, r.TxTick, r.EvidenceHash)
}

// hashRecord is the content-addressed hash of a record at its ledger
// position. Position and predecessor deliberately affect the hash —
// that is what makes the chain tamper-evident.
func hashRecord(r DependencyRecord) string {
	h := sha256.New()
	fmt.Fprintf(h, "idx=%d|prev=%s|%s", r.Index, r.PrevHash, r.contentKey())
	return hex.EncodeToString(h.Sum(nil))
}

// Validate checks a caller-supplied record before it enters the graph.
func (r DependencyRecord) Validate() error {
	switch {
	case r.NodeID == "":
		return ErrEmptyNode
	case r.UpstreamID == "":
		return ErrEmptyUpstream
	case r.NodeID == r.UpstreamID:
		return ErrSelfDependency
	case r.Confidence < 0 || r.Confidence > 1:
		return ErrBadConfidence
	case r.ValidTo != 0 && r.ValidTo < r.ValidFrom:
		return ErrBadValidity
	case !validKind(r.Kind):
		return ErrUnknownKind
	}
	return nil
}

// Declare records a dependency in the ledger and applies it to the
// graph. It is the ONLY way a dependency with confidence, temporal
// validity or provenance metadata enters the graph; the legacy
// DependsOn helper remains for the simple always-valid,
// confidence-1.0 case and is implemented on top of this.
//
// Declare is safe for concurrent use.
func (g *Graph) Declare(rec DependencyRecord) (DependencyRecord, error) {
	if err := rec.Validate(); err != nil {
		return DependencyRecord{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = map[string]bool{}
	}
	if g.seen[rec.contentKey()] {
		return DependencyRecord{}, ErrDuplicateDependency
	}
	rec.Index = uint64(len(g.ledger))
	if len(g.ledger) > 0 {
		rec.PrevHash = g.ledger[len(g.ledger)-1].Hash
	}
	rec.Hash = hashRecord(rec)
	g.ledger = append(g.ledger, rec)
	g.seen[rec.contentKey()] = true
	g.applyLocked(rec)
	return rec, nil
}

// applyLocked wires a validated record into the adjacency structure.
// Caller must hold g.mu.
func (g *Graph) applyLocked(rec DependencyRecord) {
	if g.edges[rec.NodeID] == nil {
		g.edges[rec.NodeID] = make(map[NodeID]DependencyKind)
	}
	g.edges[rec.NodeID][rec.UpstreamID] = rec.Kind
	if g.meta == nil {
		g.meta = map[edgeKey]DependencyRecord{}
	}
	g.meta[edgeKey{rec.NodeID, rec.UpstreamID}] = rec
}

// Ledger returns a defensive copy of the dependency ledger.
func (g *Graph) Ledger() []DependencyRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]DependencyRecord, len(g.ledger))
	copy(out, g.ledger)
	return out
}

// ReplayAll is Ledger under the repo-wide engine naming convention.
func (g *Graph) ReplayAll() []DependencyRecord { return g.Ledger() }

// Head returns the hash of the most recent dependency record, or ""
// when the ledger is empty.
func (g *Graph) Head() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.ledger) == 0 {
		return ""
	}
	return g.ledger[len(g.ledger)-1].Hash
}

// RootHash is the content-addressed commitment over the WHOLE
// dependency ledger — the value a certificate embeds so that "this
// decision was made under exactly this dependency model" is one hash
// comparison. Empty ledger hashes to the hash of the empty string
// marker, never to "", so an absent graph is distinguishable from a
// tampered one.
func (g *Graph) RootHash() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	h := sha256.New()
	fmt.Fprintf(h, "evidencegraph.v1|n=%d|", len(g.ledger))
	for _, r := range g.ledger {
		fmt.Fprintf(h, "%s|", r.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyChain independently recomputes every record's hash and its
// linkage. Any mutation of any field of any record — including a
// reordering — is detected.
func (g *Graph) VerifyChain() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	prev := ""
	for i, r := range g.ledger {
		if r.Index != uint64(i) {
			return fmt.Errorf("%w: record %d has Index=%d", ErrChainBroken, i, r.Index)
		}
		if r.PrevHash != prev {
			return fmt.Errorf("%w: record %d PrevHash=%q expected %q", ErrChainBroken, i, r.PrevHash, prev)
		}
		if hashRecord(r) != r.Hash {
			return fmt.Errorf("%w: record %d", ErrRecordTampered, i)
		}
		prev = r.Hash
	}
	return nil
}

// Rebuild reconstructs a Graph from a ledger alone — the replay
// proof. A Graph rebuilt from ReplayAll() of another Graph produces
// byte-identical RootHash and byte-identical EffectiveWeightAt output
// for every input, with no access to the original instance.
func Rebuild(records []DependencyRecord) (*Graph, error) {
	g := NewGraph()
	for i, r := range records {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("evidencegraph: rebuild record %d: %w", i, err)
		}
		stripped := r
		stripped.Index, stripped.PrevHash, stripped.Hash = 0, "", ""
		if _, err := g.Declare(stripped); err != nil {
			return nil, fmt.Errorf("evidencegraph: rebuild record %d: %w", i, err)
		}
	}
	if g.RootHash() != rootHashOf(records) {
		return nil, fmt.Errorf("%w: rebuilt root hash differs from supplied ledger", ErrChainBroken)
	}
	return g, nil
}

func rootHashOf(records []DependencyRecord) string {
	h := sha256.New()
	fmt.Fprintf(h, "evidencegraph.v1|n=%d|", len(records))
	for _, r := range records {
		fmt.Fprintf(h, "%s|", r.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- Temporal, confidence-weighted correlation -----------------------

// ancestorsAt is ancestors() restricted to edges whose validity window
// contains tick, carrying the multiplicative confidence of the path.
// A path through two 0.5-confidence edges yields 0.25 — uncertainty in
// the dependency model propagates instead of being rounded to
// "definitely correlated".
func (g *Graph) ancestorsAt(node NodeID, tick uint64, temporal bool) map[NodeID]float64 {
	out := map[NodeID]float64{}
	type item struct {
		id   NodeID
		conf float64
	}
	queue := []item{{node, 1}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		ups := make([]NodeID, 0, len(g.edges[cur.id]))
		for up := range g.edges[cur.id] {
			ups = append(ups, up)
		}
		sort.Slice(ups, func(i, j int) bool { return ups[i] < ups[j] })
		for _, up := range ups {
			conf := 1.0
			if rec, ok := g.meta[edgeKey{cur.id, up}]; ok {
				if temporal && !rec.validAt(tick) {
					continue
				}
				conf = rec.Confidence
			}
			pathConf := cur.conf * conf
			if pathConf <= 0 {
				continue
			}
			if existing, seen := out[up]; !seen || pathConf > existing {
				out[up] = pathConf
				queue = append(queue, item{up, pathConf})
			}
		}
	}
	return out
}

func (r DependencyRecord) validAt(tick uint64) bool {
	if tick < r.ValidFrom {
		return false
	}
	if r.ValidTo != 0 && tick > r.ValidTo {
		return false
	}
	return true
}

// sharedAncestorFractionAt is the confidence- and time-aware form of
// sharedAncestorFraction. Caller must hold at least a read lock.
func (g *Graph) sharedAncestorFractionAt(nodes []NodeID, tick uint64, temporal bool) map[NodeID]float64 {
	sets := make(map[NodeID]map[NodeID]float64, len(nodes))
	for _, n := range nodes {
		sets[n] = g.ancestorsAt(n, tick, temporal)
	}
	out := make(map[NodeID]float64, len(nodes))
	for _, n := range nodes {
		mine := sets[n]
		if len(mine) == 0 {
			out[n] = 0
			continue
		}
		var total, shared float64
		for a, myConf := range mine {
			total += myConf
			best := 0.0
			for _, m := range nodes {
				if m == n {
					continue
				}
				if c, ok := sets[m][a]; ok && c > best {
					best = c
				}
			}
			// Overlap on ancestor a is bounded by the weaker of the two
			// beliefs that the dependency exists at all.
			if best < myConf {
				shared += best
			} else {
				shared += myConf
			}
		}
		if total == 0 {
			out[n] = 0
			continue
		}
		out[n] = shared / total
	}
	return out
}

// EffectiveWeightAt is the production entry point the canonical
// pipeline calls: effective(n) = base(n) * (1 - sharedFraction(n)) with
// only dependencies valid at `tick` considered, and each dependency's
// own Confidence propagated multiplicatively.
//
// Determinism: output depends only on (ledger, nodes, base, tick) — no
// map-iteration order, no clock, no goroutine scheduling.
func (g *Graph) EffectiveWeightAt(nodes []NodeID, base map[NodeID]float64, tick uint64) map[NodeID]float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	shared := g.sharedAncestorFractionAt(nodes, tick, true)
	out := make(map[NodeID]float64, len(nodes))
	for _, n := range nodes {
		out[n] = base[n] * (1 - shared[n])
	}
	return out
}

// SharedFractionAt exposes the raw discount signal for observability
// (the audit's required `evidence_dependency_discount` metric) without
// making callers re-derive it from weights.
func (g *Graph) SharedFractionAt(nodes []NodeID, tick uint64) map[NodeID]float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sharedAncestorFractionAt(nodes, tick, true)
}

// Explain renders a human- and machine-readable justification for a
// node's discount at a tick: which upstreams it shares, with whom, at
// what confidence. This feeds DecisionExplanation's WHAT DEPENDENCY?
// question (PHASE 26).
func (g *Graph) Explain(nodes []NodeID, tick uint64) map[NodeID][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	sets := make(map[NodeID]map[NodeID]float64, len(nodes))
	for _, n := range nodes {
		sets[n] = g.ancestorsAt(n, tick, true)
	}
	out := make(map[NodeID][]string, len(nodes))
	for _, n := range nodes {
		var lines []string
		ancs := make([]NodeID, 0, len(sets[n]))
		for a := range sets[n] {
			ancs = append(ancs, a)
		}
		sort.Slice(ancs, func(i, j int) bool { return ancs[i] < ancs[j] })
		for _, a := range ancs {
			var others []string
			for _, m := range nodes {
				if m == n {
					continue
				}
				if _, ok := sets[m][a]; ok {
					others = append(others, string(m))
				}
			}
			sort.Strings(others)
			if len(others) > 0 {
				kind := "UNKNOWN"
				if rec, ok := g.meta[edgeKey{n, a}]; ok {
					kind = string(rec.Kind)
				}
				lines = append(lines, fmt.Sprintf("shares upstream %s (%s, conf=%.2f) with %v",
					a, kind, sets[n][a], others))
			}
		}
		if len(lines) == 0 {
			lines = []string{"no shared upstream dependency at this tick — counted as fully independent evidence"}
		}
		out[n] = lines
	}
	return out
}
