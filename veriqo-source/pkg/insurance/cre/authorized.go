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
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/finding"
)

// AuthorizedFinding is a finding.Finding that has passed the Finding
// Verification Gate. See this file's package-level doc comment for why
// it cannot be forged from outside this package.
//
// Unexported fields alone stop cross-package CONSTRUCTION, but not
// cross-package MUTATION of what is already sealed inside one: Go
// copies a struct's slice fields by header (pointer, length, capacity),
// not by cloning the backing array, so returning finding.Finding by
// value from an accessor would still hand out a reference to the same
// backing arrays this type's own hash was computed over. A caller
// holding that reference could mutate SupportedBy/ContradictedBy/
// Alternatives in place, silently corrupting a.finding out from under
// its own already-computed AuthorizationHash with no error raised
// anywhere. cloneFinding closes this at BOTH ends: Authorize clones f
// before sealing it in (so a caller who keeps their own reference to
// the Finding they built cannot reach back in after the fact), and
// Finding() clones again before returning (so a caller mutating what
// they got back cannot reach in either). Every call to Finding()
// therefore returns a fully independent copy.
type AuthorizedFinding struct {
	finding      finding.Finding
	hypothesisID causation.HypothesisID
	authorizedAt uint64
	hash         string
}

// cloneFinding returns f with SupportedBy/ContradictedBy/Alternatives
// deep-copied -- the only reference-type fields finding.Finding has
// (see finding.Finding's own definition: everything else is a scalar).
func cloneFinding(f finding.Finding) finding.Finding {
	f.SupportedBy = append([]string(nil), f.SupportedBy...)
	f.ContradictedBy = append([]string(nil), f.ContradictedBy...)
	f.Alternatives = append([]string(nil), f.Alternatives...)
	return f
}

// Finding returns the underlying, now-authorized Finding as an
// independent copy -- mutating the result can never affect a's own
// sealed state, and never affects any other copy a previous or future
// call to Finding() returned either.
func (a AuthorizedFinding) Finding() finding.Finding { return cloneFinding(a.finding) }

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
	// Clone before sealing: f's slice fields may still be referenced by
	// whatever code the caller used to build it (see cloneFinding's own
	// doc comment). Everything from here on reads/hashes the clone, not
	// the caller-supplied f, so nothing the caller still holds a
	// reference to can reach into a's internal state after this point.
	a := AuthorizedFinding{finding: cloneFinding(f), hypothesisID: hypothesisID, authorizedAt: tick}
	a.hash = jcs.MustHash(struct {
		FindingHash  string                 `json:"finding_hash"`
		HypothesisID causation.HypothesisID `json:"hypothesis_id"`
		AuthorizedAt uint64                 `json:"authorized_at"`
	}{f.Hash, hypothesisID, tick})
	return a, nil
}

// ErrEvidenceNotGrounded is AuthorizeGrounded's refusal when a cited
// evidence ID does not resolve to a real, FINALIZED, hash-verified
// manifest.Manifest.
var ErrEvidenceNotGrounded = errors.New("cre: Finding cites evidence that is not grounded in a real, finalized, hash-verified manifest")

// AuthorizeGrounded is Authorize, strengthened with evidence-hash-level
// lineage verification: "Finding.Hash == recomputedHash" proves f is
// internally consistent; it does not prove the evidence f cites is
// real. Authorize (via VerifyFindingAgainstHypothesis) already confirms
// SupportedBy/ContradictedBy match the real hypothesis's own evidence
// REFERENCES -- but a reference is just a string ID, and nothing in
// Authorize alone confirms that ID names evidence that actually exists,
// let alone evidence whose own integrity independently verifies.
// AuthorizeGrounded closes that: every evidence ID f cites in
// SupportedBy or ContradictedBy must resolve, in manifests, to a
// manifest.Manifest whose latest version is FINALIZED and whose own
// hash independently verifies via manifest.VerifyManifestHash. manifests
// must not be nil -- a caller with no real evidence-manifest registry
// to check against should call Authorize directly rather than pass an
// empty one and have every citation trivially fail.
func AuthorizeGrounded(f finding.Finding, hs *causation.HypothesisSet, hypothesisID causation.HypothesisID,
	traces []inference.InferenceTrace, manifests *manifest.Registry, tick uint64) (AuthorizedFinding, error) {
	if manifests == nil {
		return AuthorizedFinding{}, fmt.Errorf("%w: no manifest registry was supplied to ground evidence against", ErrEvidenceNotGrounded)
	}
	cited := append(append([]string(nil), f.SupportedBy...), f.ContradictedBy...)
	for _, evidenceID := range cited {
		m, err := manifests.Latest(evidenceID)
		if err != nil {
			return AuthorizedFinding{}, fmt.Errorf("%w: %s: %v", ErrEvidenceNotGrounded, evidenceID, err)
		}
		if m.State != manifest.StateFinalized {
			return AuthorizedFinding{}, fmt.Errorf("%w: %s is not FINALIZED (state=%s)", ErrEvidenceNotGrounded, evidenceID, m.State)
		}
		if err := manifest.VerifyManifestHash(m); err != nil {
			return AuthorizedFinding{}, fmt.Errorf("%w: %s: %v", ErrEvidenceNotGrounded, evidenceID, err)
		}
	}
	return Authorize(f, hs, hypothesisID, traces, tick)
}
