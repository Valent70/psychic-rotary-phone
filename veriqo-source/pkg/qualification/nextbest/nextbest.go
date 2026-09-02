// Package nextbest implements Next Best Evidence ranking (MIP-001
// §21, EQF-001 §11).
//
// THE ORDERING RULE THAT MATTERS MOST:
//
//	HARD POLICY FILTERS ALWAYS EXECUTE BEFORE OPTIMIZATION.
//
// A candidate that fails rights is REMOVED from the set. It is never
// merely down-weighted into a low score that a sufficiently high
// diagnostic value could later overcome. Article 4 is not a term in a
// ratio -- if it were, a sufficiently valuable piece of evidence would
// eventually outrank its own illegality, which is exactly the failure
// this ordering prevents.
//
// Only after filtering does the priority formula run:
//
//	            DiagnosticValue x Independence x Relevance
//	            x Freshness x AcquisitionFeasibility
//	Priority = ---------------------------------------------
//	            Cost x Latency x RightsRisk
package nextbest

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

var (
	ErrEmptyID      = errors.New("nextbest: candidate ID must be non-empty")
	ErrNonPositive  = errors.New("nextbest: cost, latency and rights risk must be strictly positive")
	ErrOutOfRange   = errors.New("nextbest: numerator factors must lie in [0,1]")
	ErrNoCandidates = errors.New("nextbest: no candidates supplied")
)

// FilterReason records why a candidate was excluded before scoring.
type FilterReason string

const (
	NotFiltered      FilterReason = ""
	NoRights         FilterReason = "NO_RIGHTS"
	NoAuthority      FilterReason = "NO_AUTHORITY"
	ProhibitedSource FilterReason = "PROHIBITED_SOURCE"
	PartyMediated    FilterReason = "PARTY_MEDIATED_WHERE_INDEPENDENCE_REQUIRED"
	OutOfScope       FilterReason = "OUT_OF_CASE_SCOPE"
)

// Candidate is a piece of evidence that could be acquired.
type Candidate struct {
	ID          string
	SourceID    string
	Description string

	// Hard policy gates. Any false value removes the candidate before
	// scoring; none of them is tradeable against diagnostic value.
	RightsGranted    bool
	AuthorityGranted bool
	SourcePermitted  bool
	WithinCaseScope  bool
	// IndependenceRequired marks candidates that must be independently
	// acquired. A party-mediated candidate is filtered when this is set.
	IndependenceRequired bool
	PartyMediated        bool

	// Scoring factors, each in [0,1].
	DiagnosticValue        float64
	Independence           float64
	Relevance              float64
	Freshness              float64
	AcquisitionFeasibility float64

	// Divisors, each strictly positive. RightsRisk is a residual
	// legal-risk multiplier for candidates that pass the hard gates but
	// are not risk-free.
	Cost       float64
	Latency    float64
	RightsRisk float64
}

// Scored is a ranked candidate.
type Scored struct {
	ID       string  `json:"id"`
	SourceID string  `json:"source_id"`
	Priority float64 `json:"priority"`
	Rank     int     `json:"rank"`
}

// Filtered is an excluded candidate and the reason.
type Filtered struct {
	ID     string       `json:"id"`
	Reason FilterReason `json:"reason"`
}

// Ranking is the full result: what was excluded, and what was ranked.
// Both halves are returned deliberately -- a caller that only sees the
// ranked list cannot tell whether the best option was filtered out on
// rights, which is information an analyst needs.
type Ranking struct {
	Ranked   []Scored   `json:"ranked"`
	Excluded []Filtered `json:"excluded"`
}

// filter applies the hard gates. Order of checks determines which
// reason is reported when several apply; most fundamental first.
func filter(c Candidate) FilterReason {
	switch {
	case !c.RightsGranted:
		return NoRights
	case !c.AuthorityGranted:
		return NoAuthority
	case !c.SourcePermitted:
		return ProhibitedSource
	case !c.WithinCaseScope:
		return OutOfScope
	case c.IndependenceRequired && c.PartyMediated:
		return PartyMediated
	default:
		return NotFiltered
	}
}

// Validate checks a candidate's numeric ranges.
func Validate(c Candidate) error {
	if strings.TrimSpace(c.ID) == "" {
		return ErrEmptyID
	}
	for name, v := range map[string]float64{
		"DiagnosticValue": c.DiagnosticValue, "Independence": c.Independence,
		"Relevance": c.Relevance, "Freshness": c.Freshness,
		"AcquisitionFeasibility": c.AcquisitionFeasibility,
	} {
		if v < 0 || v > 1 || math.IsNaN(v) {
			return fmt.Errorf("%w: %s=%v for candidate %q", ErrOutOfRange, name, v, c.ID)
		}
	}
	for name, v := range map[string]float64{
		"Cost": c.Cost, "Latency": c.Latency, "RightsRisk": c.RightsRisk,
	} {
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: %s=%v for candidate %q", ErrNonPositive, name, v, c.ID)
		}
	}
	return nil
}

// Priority computes one candidate's score. It assumes the candidate
// has already passed the hard filters; callers should use Rank.
func Priority(c Candidate) (float64, error) {
	if err := Validate(c); err != nil {
		return 0, err
	}
	num := c.DiagnosticValue * c.Independence * c.Relevance * c.Freshness * c.AcquisitionFeasibility
	den := c.Cost * c.Latency * c.RightsRisk
	return num / den, nil
}

// Rank filters, then scores, then orders. Ties break on candidate ID
// so the ranking is deterministic -- an analyst re-running the same
// query gets the same order, which matters when the output drives an
// acquisition decision that will later be reviewed.
func Rank(candidates []Candidate) (Ranking, error) {
	if len(candidates) == 0 {
		return Ranking{}, ErrNoCandidates
	}
	var out Ranking
	var keep []Candidate

	for _, c := range candidates {
		if strings.TrimSpace(c.ID) == "" {
			return Ranking{}, ErrEmptyID
		}
		if reason := filter(c); reason != NotFiltered {
			out.Excluded = append(out.Excluded, Filtered{ID: c.ID, Reason: reason})
			continue
		}
		if err := Validate(c); err != nil {
			return Ranking{}, err
		}
		keep = append(keep, c)
	}
	sort.Slice(out.Excluded, func(i, j int) bool { return out.Excluded[i].ID < out.Excluded[j].ID })

	for _, c := range keep {
		p, err := Priority(c)
		if err != nil {
			return Ranking{}, err
		}
		out.Ranked = append(out.Ranked, Scored{ID: c.ID, SourceID: c.SourceID, Priority: p})
	}
	sort.Slice(out.Ranked, func(i, j int) bool {
		if out.Ranked[i].Priority != out.Ranked[j].Priority {
			return out.Ranked[i].Priority > out.Ranked[j].Priority
		}
		return out.Ranked[i].ID < out.Ranked[j].ID
	})
	for i := range out.Ranked {
		out.Ranked[i].Rank = i + 1
	}
	return out, nil
}

// Best returns the highest-priority candidate ID, or false when every
// candidate was filtered out. The boolean matters: "nothing is
// permissible" is a different answer from "nothing scored well", and a
// caller must be able to tell them apart.
func Best(r Ranking) (string, bool) {
	if len(r.Ranked) == 0 {
		return "", false
	}
	return r.Ranked[0].ID, true
}
