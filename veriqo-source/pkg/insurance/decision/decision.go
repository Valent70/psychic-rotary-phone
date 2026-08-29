// Package decision closes the gap named explicitly in the reviewer's
// "OS TRUST INTEGRATION CLOSURE" instruction: pkg/moat/decision.Engine
// is a real, weighted-factor decision-scoring mechanism, but it takes
// no input from this repository's authority chain at all -- nothing
// downstream of a cre.AuthorizedFinding exists to consume it. This
// package is that consumer: the ONLY way an authoritative Decision can
// be produced is from a genuine, already-authorized
// cre.AuthorizedFinding, via MakeDecision.
//
// This follows the exact sealed-type discipline cre.AuthorizedFinding
// itself uses (see pkg/insurance/cre/authorized.go's own package doc
// comment for why): every field of Decision is unexported, so there is
// no way to write `decision.Decision{outcome: "APPROVED"}` from outside
// this package -- unexported field names are not visible across a
// package boundary at all. The only value obtainable from outside this
// package is either the zero value (whose accessors all report empty/
// zero, and whose IsZero() reports true) or a value that actually came
// out of MakeDecision. Because MakeDecision itself requires a real
// cre.AuthorizedFinding -- itself unconstructable outside pkg/insurance/cre
// without passing cre.Authorize/AuthorizeGrounded's own verification
// gate -- this closes the FULL chain:
//
//	Evidence -> Manifest -> Hypothesis -> Finding -> AuthorizedFinding -> Decision
//
// with no alternate path to an authoritative Decision at any link.
package decision

import (
	"errors"
	"fmt"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/insurance/cre"
)

// Outcome is the closed, small vocabulary of verdicts a Decision may
// carry. Deliberately NOT a caller-supplied free string: an unmodelled
// outcome is refused outright (see MakeDecision), matching this
// repository's repeated "no unrecognised enum value is silently
// accepted" discipline (manifest.State, evidence.Status,
// causation.Status, provenance.RightsState all follow the same shape).
// This package takes no position on WHAT the outcome should be for any
// given Finding -- that judgment belongs to whatever real decision
// logic calls MakeDecision (e.g. pkg/moat/decision.Engine's own scoring,
// a claims adjuster, a rules engine) -- it only guarantees that
// WHICHEVER outcome is recorded is bound, immutably and verifiably, to
// a real, authorized Finding.
type Outcome string

const (
	OutcomeApproved  Outcome = "APPROVED"
	OutcomeDenied    Outcome = "DENIED"
	OutcomePartial   Outcome = "PARTIALLY_APPROVED"
	OutcomeDeferred  Outcome = "DEFERRED"
	OutcomeEscalated Outcome = "ESCALATED"
)

var knownOutcomes = map[Outcome]bool{
	OutcomeApproved: true, OutcomeDenied: true, OutcomePartial: true,
	OutcomeDeferred: true, OutcomeEscalated: true,
}

// IsKnownOutcome reports whether o is one of the five modelled outcomes.
func IsKnownOutcome(o Outcome) bool { return knownOutcomes[o] }

// Decision is a Finding-authorized verdict. Every field is unexported;
// see this file's own package doc comment for why. Obtain one only via
// MakeDecision.
type Decision struct {
	// findingHash / authorizationHash / hypothesisID are copied
	// verbatim from the AuthorizedFinding that authorized this
	// Decision -- never re-derived, never caller-supplied -- so a
	// Decision always carries its own, permanent, checkable provenance
	// trail back to the exact Finding and the exact authorization event
	// that produced it (INV-005's "identity-bound to the same evidence
	// content" applied one layer further downstream).
	findingHash       string
	authorizationHash string
	hypothesisID      string

	outcome   Outcome
	rationale string
	decidedAt uint64

	// hash is this Decision's own canonical commitment -- a
	// deterministic, pure function of every field above (see
	// decisionHashInput), computed once inside MakeDecision and never
	// mutated afterward. Two independently-computed Decisions over the
	// identical inputs are byte-identical, which is what makes replay
	// closure (proving a full-system replay reproduces the same
	// decision artifact) meaningful.
	hash string
}

// ErrFindingNotAuthorized is MakeDecision's refusal when af is the zero
// AuthorizedFinding (IsZero()==true) -- i.e. it never actually passed
// through cre.Authorize/AuthorizeGrounded's verification gate. This is
// the direct implementation of "rejection of unauthorized findings":
// no Decision can ever be produced from a Finding that was never
// authorized, because there is no other way to obtain a non-zero
// AuthorizedFinding value at all.
var ErrFindingNotAuthorized = errors.New("decision: cannot decide from an unauthorized (zero-value) AuthorizedFinding")

// ErrUnknownOutcome is MakeDecision's refusal when outcome does not
// name one of the five modelled Outcome values.
var ErrUnknownOutcome = errors.New("decision: unknown Outcome")

// ErrEmptyRationale is MakeDecision's refusal when rationale is blank.
// A Decision with no stated reason is not audit-trail-eligible: "why"
// must be on file, not left to be reconstructed later from context that
// may not survive -- the same discipline
// evidence.Registry.MarkSuperseded already requires for its own reason
// parameter.
var ErrEmptyRationale = errors.New("decision: rationale must be non-empty")

// decisionHashInput is the exact, ordered set of fields
// canonicalized/hashed to produce Decision.hash. A plain struct (not
// Decision itself) so the hash input is visibly and permanently
// decoupled from Decision's own internal field layout -- adding an
// unrelated bookkeeping field to Decision later can never silently
// change what the hash covers.
type decisionHashInput struct {
	FindingHash       string `json:"finding_hash"`
	AuthorizationHash string `json:"authorization_hash"`
	HypothesisID      string `json:"hypothesis_id"`
	Outcome           string `json:"outcome"`
	Rationale         string `json:"rationale"`
	DecidedAt         uint64 `json:"decided_at"`
}

// MakeDecision is the ONLY exported function in this package that can
// produce a populated Decision. It requires af to be a genuine,
// authorized finding (not the zero value), outcome to be a known
// value, and rationale to be non-empty. Canonicalization and hashing go
// through pkg/canonical/jcs -- the same deterministic, no-time.Now(),
// no-randomness mechanism every other authority-bearing hash in this
// repository already uses, which is what makes two independent replays
// of the identical inputs converge on the identical Decision.hash.
func MakeDecision(af cre.AuthorizedFinding, outcome Outcome, rationale string, tick uint64) (Decision, error) {
	if af.IsZero() {
		return Decision{}, ErrFindingNotAuthorized
	}
	if !IsKnownOutcome(outcome) {
		return Decision{}, fmt.Errorf("%w: %q", ErrUnknownOutcome, outcome)
	}
	if strings.TrimSpace(rationale) == "" {
		return Decision{}, ErrEmptyRationale
	}

	d := Decision{
		findingHash:       af.Finding().Hash,
		authorizationHash: af.AuthorizationHash(),
		hypothesisID:      string(af.HypothesisID()),
		outcome:           outcome,
		rationale:         rationale,
		decidedAt:         tick,
	}
	d.hash = jcs.MustHash(decisionHashInput{
		FindingHash: d.findingHash, AuthorizationHash: d.authorizationHash,
		HypothesisID: d.hypothesisID, Outcome: string(d.outcome),
		Rationale: d.rationale, DecidedAt: d.decidedAt,
	})
	return d, nil
}

// FindingHash / AuthorizationHash / HypothesisID / Outcome / Rationale /
// DecidedAt / Hash are Decision's read-only accessors -- the only way
// any caller, inside or outside this package, ever observes a
// Decision's content.
func (d Decision) FindingHash() string       { return d.findingHash }
func (d Decision) AuthorizationHash() string { return d.authorizationHash }
func (d Decision) HypothesisID() string      { return d.hypothesisID }
func (d Decision) Outcome() Outcome          { return d.outcome }
func (d Decision) Rationale() string         { return d.rationale }
func (d Decision) DecidedAt() uint64         { return d.decidedAt }
func (d Decision) Hash() string              { return d.hash }

// IsZero reports whether d is the unpopulated zero value -- the only
// value obtainable outside this package without calling MakeDecision.
func (d Decision) IsZero() bool { return d.hash == "" }

// ErrDecisionHashMismatch is VerifyDecisionProvenance's refusal when a
// Decision's own recorded hash does not match what its own recorded
// fields recompute to -- proof the value was tampered with after
// MakeDecision produced it (e.g. round-tripped through a hand-edited
// JSON snapshot; see ToAuditPayload's own doc comment on why that
// round trip can never manufacture a live Decision anyway).
var ErrDecisionHashMismatch = errors.New("decision: recorded hash does not match recomputed hash")

// ErrDecisionProvenanceMismatch is VerifyDecisionProvenance's refusal
// when a Decision's cited FindingHash/AuthorizationHash/HypothesisID do
// not match the AuthorizedFinding it is checked against -- i.e. this
// Decision was not actually authorized by THIS finding, whatever else
// it might claim.
var ErrDecisionProvenanceMismatch = errors.New("decision: Decision's cited provenance does not match the given AuthorizedFinding")

// VerifyDecisionProvenance re-derives d's own hash from its own
// recorded fields (catching any tampering) AND confirms d's cited
// FindingHash/AuthorizationHash/HypothesisID actually match af's real
// values (catching a Decision that cites the WRONG finding) -- the same
// re-verify-by-recomputing discipline
// cre.VerifyFindingAgainstHypothesis/manifest.VerifyManifestHash already
// use. This is "provenance preservation" made checkable, not just
// asserted: Trust Propagation (Evidence provenance -> Finding
// provenance -> Decision provenance) is proven here to not have been
// lost or substituted.
func VerifyDecisionProvenance(d Decision, af cre.AuthorizedFinding) error {
	if d.IsZero() {
		return fmt.Errorf("%w: Decision is the zero value", ErrDecisionHashMismatch)
	}
	want := jcs.MustHash(decisionHashInput{
		FindingHash: d.findingHash, AuthorizationHash: d.authorizationHash,
		HypothesisID: d.hypothesisID, Outcome: string(d.outcome),
		Rationale: d.rationale, DecidedAt: d.decidedAt,
	})
	if want != d.hash {
		return fmt.Errorf("%w: recorded=%s recomputed=%s", ErrDecisionHashMismatch, d.hash, want)
	}
	if af.IsZero() {
		return fmt.Errorf("%w: given AuthorizedFinding is the zero value", ErrDecisionProvenanceMismatch)
	}
	if d.findingHash != af.Finding().Hash {
		return fmt.Errorf("%w: FindingHash %s does not match the given finding's real Hash %s", ErrDecisionProvenanceMismatch, d.findingHash, af.Finding().Hash)
	}
	if d.authorizationHash != af.AuthorizationHash() {
		return fmt.Errorf("%w: AuthorizationHash %s does not match the given finding's real AuthorizationHash %s", ErrDecisionProvenanceMismatch, d.authorizationHash, af.AuthorizationHash())
	}
	if d.hypothesisID != string(af.HypothesisID()) {
		return fmt.Errorf("%w: HypothesisID %s does not match the given finding's real HypothesisID %s", ErrDecisionProvenanceMismatch, d.hypothesisID, string(af.HypothesisID()))
	}
	return nil
}

// AuditPayload is the plain, exported, JSON-serializable snapshot of a
// Decision's own committed fields -- for writing to an audit ledger
// (pkg/platform/audit.AuditStore.Append takes a plain string payload).
// Deliberately a SEPARATE, one-way type from Decision itself: nothing
// in this package ever accepts an AuditPayload as input to reconstruct
// a live, trusted Decision. Serialization can produce a permanent audit
// record; it can never manufacture authority (INV-006) -- there is no
// UnmarshalPayload/FromAuditPayload function anywhere in this package,
// on purpose.
type AuditPayload struct {
	FindingHash       string `json:"finding_hash"`
	AuthorizationHash string `json:"authorization_hash"`
	HypothesisID      string `json:"hypothesis_id"`
	Outcome           string `json:"outcome"`
	Rationale         string `json:"rationale"`
	DecidedAt         uint64 `json:"decided_at"`
	DecisionHash      string `json:"decision_hash"`
}

// ToAuditPayload returns d's audit-ledger snapshot. Refuses the zero
// Decision -- there is nothing authoritative to record.
func (d Decision) ToAuditPayload() (AuditPayload, error) {
	if d.IsZero() {
		return AuditPayload{}, fmt.Errorf("%w: cannot produce an audit payload for the zero-value Decision", ErrFindingNotAuthorized)
	}
	return AuditPayload{
		FindingHash: d.findingHash, AuthorizationHash: d.authorizationHash,
		HypothesisID: d.hypothesisID, Outcome: string(d.outcome),
		Rationale: d.rationale, DecidedAt: d.decidedAt, DecisionHash: d.hash,
	}, nil
}
