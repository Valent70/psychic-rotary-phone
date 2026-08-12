// stage.go closes PRIORITAS #3 in
// "Gap_yang_sekarang_menjadi_PRIORITAS.docx": "Evidence provenance
// harus naik satu tingkat" — the critique's specific point is that
// pkg/evidence/graph already has source identity (Node.Kind/Payload)
// and generic ancestry (Lineage/Provenance), but nothing distinguishes
// WHICH STAGE of the pipeline a given node represents:
//
//	Source -> Raw Observation -> Transformation -> Normalization
//	-> Fusion -> Arbitration -> Claim -> Decision
//
// "Setiap tahap harus dapat ditelusuri" (every stage must be
// traceable) — so trust becomes trust in the ENTIRE evidence lineage,
// not just trust in the originating source.
//
// StageTracker is a thin layer on top of an existing *Graph (not a
// second copy of its data): it records which canonical Stage each
// already-registered node belongs to, and rejects a stage assignment
// that would go BACKWARD relative to any node it was recorded as
// derived_from — a node fused from raw observations cannot later be
// mislabeled as itself a raw observation, structurally.
package graph

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrUnknownStage          = errors.New("graph: unknown stage")
	ErrStageAlreadySet       = errors.New("graph: node already has a stage assigned")
	ErrStageRegressesLineage = errors.New("graph: assigned stage is earlier in the pipeline than a node this was derived from")
)

// Stage is one canonical position in Veriqo's evidence pipeline.
type Stage int

const (
	StageSource         Stage = iota + 1 // where an evidence-producing party/system is identified
	StageRawObservation                  // Class A — what a source asserted
	StageTransformation                  // format/unit conversion, extraction
	StageNormalization                   // canonicalization onto a shared schema/value space
	StageFusion                          // multi-source combination (pkg/moat/fusion)
	StageArbitration                     // Class B — truth arbitration (pkg/moat/contradiction)
	StageClaim                           // the resulting asserted fact
	StageDecision                        // a decision/action made from the claim (pkg/moat/decision)
)

// CanonicalStages is the full ordered pipeline, exported so callers can
// compute lineage completeness against the same authoritative list this
// package validates against.
var CanonicalStages = []Stage{
	StageSource, StageRawObservation, StageTransformation, StageNormalization,
	StageFusion, StageArbitration, StageClaim, StageDecision,
}

func (s Stage) String() string {
	switch s {
	case StageSource:
		return "SOURCE"
	case StageRawObservation:
		return "RAW_OBSERVATION"
	case StageTransformation:
		return "TRANSFORMATION"
	case StageNormalization:
		return "NORMALIZATION"
	case StageFusion:
		return "FUSION"
	case StageArbitration:
		return "ARBITRATION"
	case StageClaim:
		return "CLAIM"
	case StageDecision:
		return "DECISION"
	default:
		return "UNKNOWN"
	}
}

func (s Stage) valid() bool {
	return s >= StageSource && s <= StageDecision
}

// StageAnnotation pairs a node with its recorded pipeline stage.
type StageAnnotation struct {
	Node  Node
	Stage Stage
}

// StageTracker records a Stage per NodeID for an existing *Graph. It is
// deliberately a separate type rather than an added field on Graph
// itself: Graph stays a generic, stage-agnostic content-addressed store
// (any caller can use it without opting into the pipeline-stage model),
// and StageTracker is the opt-in typed layer this package's own doc
// comment says the audit wants "one level up".
type StageTracker struct {
	g  *Graph
	mu sync.RWMutex
	// stages[id] = Stage the node was marked as. Deliberately
	// append-only in effect (MarkStage refuses to overwrite an existing
	// assignment, matching the graph's own append-only discipline) —
	// a re-derivation should be a NEW node (new content -> new NodeID),
	// not a mutation of an old one's stage.
	stages map[NodeID]Stage
}

// NewStageTracker wraps an existing Graph. The Graph must already
// exist and be populated via its own AddNode/AddEdge API; StageTracker
// only annotates nodes that are already there.
func NewStageTracker(g *Graph) *StageTracker {
	return &StageTracker{g: g, stages: make(map[NodeID]Stage)}
}

// MarkStage records the pipeline stage for an already-registered node.
// Rejects: unknown stage values, re-assignment of an already-marked
// node, a node not present in the underlying graph, and — the
// substantive check — a stage that is EARLIER in the canonical
// pipeline than any node this one was recorded as derived_from. That
// last check is what makes "every stage must be traceable" a property
// the type system/API enforces rather than something callers must
// remember to get right.
func (st *StageTracker) MarkStage(id NodeID, stage Stage) error {
	if !stage.valid() {
		return fmt.Errorf("%w: %d", ErrUnknownStage, stage)
	}
	if _, ok := st.g.Node(id); !ok {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, exists := st.stages[id]; exists {
		return fmt.Errorf("%w: %s", ErrStageAlreadySet, id)
	}
	// Check direct parents (derived_from targets) already staged don't
	// sit at or after this node's proposed stage — a node cannot be
	// earlier in the pipeline than something it was built from.
	for _, e := range st.g.Provenance(id) {
		if e.From != id || e.Relation != RelationDerivedFrom {
			continue
		}
		if parentStage, ok := st.stages[e.To]; ok && parentStage >= stage {
			return fmt.Errorf("%w: %s (%s) derived from %s (%s)", ErrStageRegressesLineage, id, stage, e.To, parentStage)
		}
	}
	st.stages[id] = stage
	return nil
}

// StageOf returns the recorded stage for a node, if any.
func (st *StageTracker) StageOf(id NodeID) (Stage, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s, ok := st.stages[id]
	return s, ok
}

// StageLineage returns the full ancestry of a node (via the underlying
// Graph's Lineage) paired with each ancestor's recorded stage, sorted
// by stage rank then NodeID — the concrete "every stage traceable"
// artifact: a caller can render this directly as
// Source -> Raw -> Transform -> Normalize -> Fusion -> Arbitration ->
// Claim -> Decision for any node, with gaps immediately visible as
// missing stages in the list.
func (st *StageTracker) StageLineage(id NodeID) ([]StageAnnotation, error) {
	nodes, err := st.g.Lineage(id)
	if err != nil {
		return nil, err
	}
	// Lineage does not include the node itself; include it so the
	// returned list is the complete pipeline trace ending at id.
	if self, ok := st.g.Node(id); ok {
		nodes = append(nodes, self)
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]StageAnnotation, 0, len(nodes))
	for _, n := range nodes {
		if s, ok := st.stages[n.ID]; ok {
			out = append(out, StageAnnotation{Node: n, Stage: s})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		return out[i].Node.ID < out[j].Node.ID
	})
	return out, nil
}

// LineageCompleteness reports what fraction of the 8 canonical stages
// are represented somewhere in a node's traceable ancestry (including
// itself) — a concrete, computable answer to "trust bukan hanya trust
// terhadap source, tetapi trust terhadap entire evidence lineage": a
// Decision whose lineage skips straight from Source to Decision with
// no recorded Fusion/Arbitration/Claim stages in between scores lower
// than one with every intermediate stage present, independent of
// confidence numbers, and can be used as a gating input to policy or
// trust evaluation the same way calibration_accuracy is.
func (st *StageTracker) LineageCompleteness(id NodeID) (float64, error) {
	annotated, err := st.StageLineage(id)
	if err != nil {
		return 0, err
	}
	present := make(map[Stage]bool, len(annotated))
	for _, a := range annotated {
		present[a.Stage] = true
	}
	count := 0
	for _, s := range CanonicalStages {
		if present[s] {
			count++
		}
	}
	return float64(count) / float64(len(CanonicalStages)), nil
}
