// This file closes the gap a "Red Flag" review of this repository's own
// prior closure report named as the most serious residual issue: a
// finding.Finding that passes VerifyFindingAgainstHypothesis /
// VerifyFindingProvenance is proof that the check WOULD succeed if run
// -- but nothing forced any caller to actually run it before treating
// the Finding as real. The reviewer's own words: "Kalau jawabannya
// masih tidak [VERIQO memaksa semua Finding production melewati
// verification gate], maka verification function ada, tetapi
// enforcement belum absolute" (if the answer is still no, the
// verification function exists but enforcement is not absolute), and
// named the required shape explicitly:
//
//	Evidence -> Verification -> Hypothesis -> Finding Builder ->
//	Finding Verification Gate -> AUTHORIZED FINDING -> CRE / Dossier / Decision
//
// "Caller -> Finding" directly, skipping the gate, must not be
// expressible for any consumer that requires an authorized result.
//
// AuthorizedFinding makes this a compile-time guarantee, not a
// documented convention. Every one of its fields is unexported. There
// is no way to write `cre.AuthorizedFinding{finding: x}` from outside
// this package -- unexported field names are not visible across a
// package boundary at all, so the only value obtainable from outside
// this package is either the zero value (every accessor then returns a
// zero/empty result, which fails any check a careful consumer runs) or
// a value that actually came out of Authorize.
package cre

import (
	"errors"
	"fmt"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/finding"
)

// AuthorizedFinding is a finding.Finding that has passed the Finding
// Verification Gate. See this file's package-level doc comment for why
// it cannot be forged from outside this package.
type AuthorizedFinding struct {
	finding      finding.Finding
	hypothesisID causation.HypothesisID
	authorizedAt uint64
	hash         string
}

// Finding returns the underlying, now-authorized Finding.
func (a AuthorizedFinding) Finding() finding.Finding { return a.finding }

// HypothesisID names which hypothesis this Finding was authorized
// against.
func (a AuthorizedFinding) HypothesisID() causation.HypothesisID { return a.hypothesisID }

// AuthorizedAt is the tick Authorize ran at.
func (a AuthorizedFinding) AuthorizedAt() uint64 { return a.authorizedAt }

// AuthorizationHash is a deterministic commitment to this specific
// authorization event -- which Finding (by its own Hash), against
// which hypothesis, at which tick -- distinct from Finding().Hash,
// which only commits to the Finding's own content and says nothing
// about whether it was ever actually run through the gate.
func (a AuthorizedFinding) AuthorizationHash() string { return a.hash }

// IsZero reports whether a is the unpopulated zero value -- the only
// value obtainable outside this package without calling Authorize.
func (a AuthorizedFinding) IsZero() bool { return a.hash == "" }

// ErrFindingNotReady is Authorize's refusal when f has not reached
// finding.StatusFinding -- an incomplete Finding is never eligible for
// authorization, regardless of what VerifyFindingAgainstHypothesis or
// VerifyFindingProvenance would say about the fields it does have.
var ErrFindingNotReady = errors.New("cre: finding is not at StatusFinding and cannot be authorized")

// Authorize is the ONLY exported function in this package that can
// produce a populated AuthorizedFinding. It requires f to already be at
// finding.StatusFinding, then runs the full Finding Verification Gate:
// VerifyFindingAgainstHypothesis against the REAL hypothesis within hs,
// and VerifyFindingProvenance against traces (nil is fine when f cites
// no InferenceTrace at all). Both must pass; if either fails, Authorize
// returns the zero AuthorizedFinding and the failure, never a partially
// authorized value.
func Authorize(f finding.Finding, hs *causation.HypothesisSet, hypothesisID causation.HypothesisID,
	traces []inference.InferenceTrace, tick uint64) (AuthorizedFinding, error) {
	if f.Status != finding.StatusFinding {
		return AuthorizedFinding{}, fmt.Errorf("%w: status is %s (missing=%v)", ErrFindingNotReady, f.Status, finding.MissingFields(f))
	}
	if err := VerifyFindingAgainstHypothesis(f, hs, hypothesisID); err != nil {
		return AuthorizedFinding{}, err
	}
	if err := VerifyFindingProvenance(f, traces); err != nil {
		return AuthorizedFinding{}, err
	}
	a := AuthorizedFinding{finding: f, hypothesisID: hypothesisID, authorizedAt: tick}
	a.hash = jcs.MustHash(struct {
		FindingHash  string                 `json:"finding_hash"`
		HypothesisID causation.HypothesisID `json:"hypothesis_id"`
		AuthorizedAt uint64                 `json:"authorized_at"`
	}{f.Hash, hypothesisID, tick})
	return a, nil
}
