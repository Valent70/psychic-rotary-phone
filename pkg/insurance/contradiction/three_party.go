// Three-Party Evidence Model — blueprint §12. The blueprint's own
// worked structure: PARTY A, PARTY B and INSURER each submit evidence;
// the engine then computes pairwise support across every relationship
// among them AND against an independent source (A<->B, A<->C, B<->C,
// A<->Independent, B<->Independent, C<->Independent), reporting one of
// SUPPORTED / CONTRADICTED / PARTIALLY_SUPPORTED / UNRESOLVED for each.
//
// This is deliberately NOT a new four-value classifier invented from
// scratch. Per claim key shared by a pair, the actual agree/disagree
// decision is made by the SAME real ArbitrationEngine.ConflictRecords
// this package already wraps in contradiction.go (scoped, via
// pairwiseResolution below, to just that pair's own observations for
// that one claim) — this file only aggregates that real, per-claim
// ResolutionState across every claim key the pair shares into the
// four-value SupportStatus §12 names.
package contradiction

import (
	"errors"
	"fmt"
	"sort"

	moat "veriqo/pkg/moat/contradiction"

	"veriqo/pkg/insurance/evidence"
)

// SupportStatus is blueprint §12's own four-value output set for one
// pairwise evidence relationship.
type SupportStatus string

const (
	SupportSupported          SupportStatus = "SUPPORTED"
	SupportContradicted       SupportStatus = "CONTRADICTED"
	SupportPartiallySupported SupportStatus = "PARTIALLY_SUPPORTED"
	SupportUnresolved         SupportStatus = "UNRESOLVED"
)

var knownSupportStatuses = map[SupportStatus]bool{
	SupportSupported: true, SupportContradicted: true,
	SupportPartiallySupported: true, SupportUnresolved: true,
}

// IsKnownSupportStatus reports whether s is one of §12's four modelled
// pairwise support outcomes.
func IsKnownSupportStatus(s SupportStatus) bool { return knownSupportStatuses[s] }

// PairSupport is the computed relationship between two pieces of
// evidence (identified by EvidenceID) across every claim key they both
// asserted a value for.
type PairSupport struct {
	EvidenceA string        `json:"evidence_a"`
	EvidenceB string        `json:"evidence_b"`
	Status    SupportStatus `json:"status"`

	SharedClaims      int `json:"shared_claims"`
	AgreeingClaims    int `json:"agreeing_claims"`
	ConflictingClaims int `json:"conflicting_claims"`
}

// PairwiseSupport computes evidenceIDA <-> evidenceIDB support per
// blueprint §12. It never re-derives arbitration itself: for every
// claim key both pieces of evidence asserted a value for, it asks the
// real wrapped ArbitrationEngine (scoped to just that pair, via
// pairwiseResolution) whether they agree.
//
//   - No shared claim keys at all: UNRESOLVED. This is the golden-rule
//     case — there genuinely is not enough overlapping evidence to say
//     anything, so the correct output is UNRESOLVED, not a guess.
//   - Every shared claim agrees: SUPPORTED.
//   - Every shared claim conflicts: CONTRADICTED.
//   - A mix of both: PARTIALLY_SUPPORTED.
func (e *Engine) PairwiseSupport(evidenceIDA, evidenceIDB string, tick, halfLifeTicks uint64) (PairSupport, error) {
	if evidenceIDA == "" || evidenceIDB == "" {
		return PairSupport{}, ErrEmptyEvidenceID
	}

	e.mu.RLock()
	claimsA := e.claimsByEvidence[evidenceIDA]
	claimsB := e.claimsByEvidence[evidenceIDB]
	var shared []string
	for claim := range claimsA {
		if claimsB[claim] {
			shared = append(shared, claim)
		}
	}
	e.mu.RUnlock()
	sort.Strings(shared) // deterministic iteration order

	result := PairSupport{EvidenceA: evidenceIDA, EvidenceB: evidenceIDB, SharedClaims: len(shared)}

	if len(shared) == 0 {
		result.Status = SupportUnresolved
		return result, nil
	}

	for _, claim := range shared {
		state, err := e.pairwiseResolution(evidenceIDA, evidenceIDB, claim, tick, halfLifeTicks)
		if err != nil {
			return PairSupport{}, fmt.Errorf("contradiction: pairwise resolution for claim %q: %w", claim, err)
		}
		if state == moat.ResolutionUncontested {
			result.AgreeingClaims++
		} else {
			result.ConflictingClaims++
		}
	}

	switch {
	case result.ConflictingClaims == 0:
		result.Status = SupportSupported
	case result.AgreeingClaims == 0:
		result.Status = SupportContradicted
	default:
		result.Status = SupportPartiallySupported
	}
	return result, nil
}

// pairwiseResolution asks the real ArbitrationEngine whether
// evidenceIDA and evidenceIDB agree on claimKey, scoped to ONLY their
// two observations (built as an ephemeral scoped engine fed with the
// same real moat.RawObservation values already recorded by Submit) so
// that a third party's evidence on the same claim key does not affect
// this specific pairwise comparison. The decision itself
// (UNCONTESTED/ARBITRATED/CONTESTED) is entirely the wrapped engine's
// own ConflictRecords computation — this function only filters its
// input, it does not touch the arbitration math.
func (e *Engine) pairwiseResolution(evidenceIDA, evidenceIDB, claimKey string, tick, halfLifeTicks uint64) (moat.ResolutionState, error) {
	all := e.arb.Observations(claimKey)
	var filtered []moat.RawObservation
	for _, o := range all {
		if o.SourceID == evidenceIDA || o.SourceID == evidenceIDB {
			filtered = append(filtered, o)
		}
	}
	if len(filtered) < 2 {
		return "", fmt.Errorf("contradiction: expected observations from both %s and %s on claim %q, found %d", evidenceIDA, evidenceIDB, claimKey, len(filtered))
	}

	scoped := moat.NewArbitrationEngine()
	for _, o := range filtered {
		if err := scoped.Observe(o); err != nil {
			return "", err
		}
	}
	cr, err := scoped.ConflictRecords(claimKey, tick, halfLifeTicks)
	if err != nil {
		return "", err
	}
	return cr.ResolutionState, nil
}

// ErrNilDependencyGraph is returned by IndependentSupportCount when
// called with a nil graph — this package refuses to silently fall back
// to a naive len(evidenceIDs) count, which is exactly the double-
// counting mistake blueprint §10/§36 forbid.
var ErrNilDependencyGraph = errors.New("contradiction: DependencyGraph must not be nil")

// IndependentSupportCount returns the number of DISTINCT independent
// root sources among evidenceIDs — blueprint §10's own worked example
// (Photo A -> Statement A -> Survey A is ONE independent source, not
// three) and golden rule §36 ("Never double-count dependent
// evidence"). This package never computes its own corroboration count:
// it defers entirely to evidence.DependencyGraph.IndependentCount, the
// one authoritative dependency-aware implementation.
func IndependentSupportCount(graph *evidence.DependencyGraph, evidenceIDs []string) (int, error) {
	if graph == nil {
		return 0, ErrNilDependencyGraph
	}
	return graph.IndependentCount(evidenceIDs), nil
}
