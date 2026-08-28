// This file closes this program's own Round 8 self-review gap G3
// ("Insurance Network Contract v2"): network.go's original
// EvidenceExchangeAdapter/QualificationAdapter pair modelled only two
// steps of a real counterparty's lifecycle (submit evidence, check
// qualification). A real insurer/broker/P&I/surveyor/regulator
// integration needs the FULL exchange lifecycle the reviewer's own
// worked list names: Identity, Authority, Evidence Exchange,
// Qualification, Case Invitation, Case Acceptance, Evidence Submission,
// Evidence Receipt, Clarification Request, Response, Review, Outcome,
// Revocation.
//
// Same discipline as network.go's own doc comment states and this
// package's test file proves structurally: every interface below is a
// DATA CONTRACT AND ADAPTER BOUNDARY ONLY. No concrete implementation
// of any interface in this file exists anywhere in this repository — a
// real one requires a real counterparty system, which is external and
// cannot be honestly fabricated inside a sandboxed engineering session
// (network_lifecycle_test.go's referenceLifecycleAdapter proves the
// contract compiles and is implementable, and nothing more).
//
// Deliberately reuses rather than re-derives: PartyID/Role from
// pkg/insurance/party, QualificationState/ExchangeRequest/
// ExchangeReceipt from this package's own network.go, and never
// introduces a second verdict/outcome vocabulary — CaseOutcome below is
// the NETWORK PROCESS'S own terminal state (was the exchange concluded,
// handed off, or withdrawn), never a coverage/liability/settlement
// determination; VERIQO's no-verdict rule (pkg/insurance/guardrails)
// applies here exactly as it does to Dossier.
package network

import (
	"context"
	"errors"

	"veriqo/pkg/insurance/party"
)

// ---- Identity ----------------------------------------------------------

// Identity is what a real counterparty system would assert about one
// party's own real-world identity — deliberately NOT a second identity
// engine (this package's own governing rule forbids
// InsuranceIdentity-shaped duplicates): it carries only what an
// EXTERNAL counterparty adapter would hand back, keyed by the SAME
// party.PartyID this repository already uses everywhere else.
type Identity struct {
	PartyID          party.PartyID `json:"party_id"`
	LegalName        string        `json:"legal_name"`
	Jurisdiction     string        `json:"jurisdiction,omitempty"`
	RegisteredNumber string        `json:"registered_number,omitempty"`
	ResolvedAtTick   uint64        `json:"resolved_at_tick"`
}

// IdentityAdapter is the interface a real counterparty registry (a
// corporate registry, a regulator's own identity register) would
// implement to resolve a party.PartyID to a real-world Identity.
type IdentityAdapter interface {
	ResolveIdentity(ctx context.Context, p party.PartyID) (Identity, error)
}

// ---- Authority -----------------------------------------------------------

// AuthorityGrant is what a real counterparty would attest about
// whether a party currently holds delegated authority to act in a
// given Role — distinct from QualificationState (which is about
// whether a party IS qualified to hold the role at all) and from
// party.Relationship.Authority (which is this REPOSITORY's own
// internally-recorded authority claim). AuthorityGrant is the
// EXTERNAL counterparty's own corroboration of that claim.
type AuthorityGrant struct {
	PartyID    party.PartyID `json:"party_id"`
	Role       party.Role    `json:"role"`
	Held       bool          `json:"held"`
	GrantedBy  string        `json:"granted_by,omitempty"`
	VerifiedAt uint64        `json:"verified_at_tick"`
}

// AuthorityAdapter is the interface a real authority-verifying
// counterparty (e.g. a P&I Club confirming a claims handler is
// currently authorised to bind the club) would implement.
type AuthorityAdapter interface {
	VerifyAuthority(ctx context.Context, p party.PartyID, role party.Role) (AuthorityGrant, error)
}

// ---- Case invitation / acceptance ---------------------------------------

// CaseInvitation is what VERIQO would send a real counterparty to
// invite them into one case's evidence exchange — the first step of
// the real lifecycle before any evidence moves.
type CaseInvitation struct {
	CaseID           string        `json:"case_id"`
	InvitedPartyID   party.PartyID `json:"invited_party_id"`
	InvitedRole      party.Role    `json:"invited_role"`
	InvitedByPartyID party.PartyID `json:"invited_by_party_id"`
	InvitedAtTick    uint64        `json:"invited_at_tick"`
}

// CaseAcceptance is the counterparty's own response to a CaseInvitation
// — Accepted false with a stated DeclineReason is a first-class,
// equally valid outcome, never treated as an error.
type CaseAcceptance struct {
	CaseID          string        `json:"case_id"`
	PartyID         party.PartyID `json:"party_id"`
	Accepted        bool          `json:"accepted"`
	DeclineReason   string        `json:"decline_reason,omitempty"`
	RespondedAtTick uint64        `json:"responded_at_tick"`
}

// CaseLifecycleAdapter is the interface a real counterparty would
// implement to be invited into, and respond to, one case.
type CaseLifecycleAdapter interface {
	InviteToCase(ctx context.Context, inv CaseInvitation) error
	RespondToInvitation(ctx context.Context, acc CaseAcceptance) (CaseAcceptance, error)
}

// ---- Clarification -------------------------------------------------------

// ClarificationRequest is what a real counterparty (or VERIQO) would
// send when a submitted evidence exchange needs a specific question
// answered before it can be relied on — distinct from a fresh
// ExchangeRequest because it references EXISTING exchanged evidence by
// hash rather than submitting new evidence outright.
type ClarificationRequest struct {
	CaseID              string        `json:"case_id"`
	EvidenceContentHash string        `json:"evidence_content_hash"`
	RequestedByPartyID  party.PartyID `json:"requested_by_party_id"`
	Question            string        `json:"question"`
	RequestedAtTick     uint64        `json:"requested_at_tick"`
}

// ClarificationResponse answers a ClarificationRequest. SupportingEvidenceHash
// is optional — a clarification may be answered in text alone, or may
// point to a further piece of exchanged evidence (by content hash,
// matching this package's evidence-by-reference discipline throughout).
type ClarificationResponse struct {
	CaseID                 string        `json:"case_id"`
	EvidenceContentHash    string        `json:"evidence_content_hash"`
	RespondingPartyID      party.PartyID `json:"responding_party_id"`
	Answer                 string        `json:"answer"`
	SupportingEvidenceHash string        `json:"supporting_evidence_hash,omitempty"`
	RespondedAtTick        uint64        `json:"responded_at_tick"`
}

// ClarificationAdapter is the interface a real counterparty would
// implement to request, and respond to, a clarification on previously
// exchanged evidence.
type ClarificationAdapter interface {
	RequestClarification(ctx context.Context, req ClarificationRequest) error
	SubmitClarificationResponse(ctx context.Context, resp ClarificationResponse) error
}

// ---- Review / outcome -----------------------------------------------------

// CaseReview is a counterparty's own recorded review comment on a
// case's exchange — free text, matching party.Relationship.Scope's own
// reasoning (a closed taxonomy of every real-world review comment
// would be a worse fit than a stated comment).
type CaseReview struct {
	CaseID          string        `json:"case_id"`
	ReviewerPartyID party.PartyID `json:"reviewer_party_id"`
	Comment         string        `json:"comment"`
	ReviewedAtTick  uint64        `json:"reviewed_at_tick"`
}

// OutcomeKind is the NETWORK EXCHANGE's own terminal state — never a
// coverage, liability or settlement determination (see this file's own
// package doc comment). It describes only whether the counterparty
// exchange itself concluded, was withdrawn, or was handed off — not
// anything about who is right on the underlying claim.
type OutcomeKind string

const (
	// OutcomeExchangeConcluded: the counterparty exchange for this case
	// reached its natural end (all requested evidence/clarifications
	// answered) — says nothing about the claim's own coverage outcome.
	OutcomeExchangeConcluded OutcomeKind = "EXCHANGE_CONCLUDED"
	// OutcomeWithdrawn: the counterparty (or VERIQO) withdrew from the
	// exchange before it concluded.
	OutcomeWithdrawn OutcomeKind = "WITHDRAWN"
	// OutcomeHandedOff: the exchange was handed to a different
	// counterparty adapter (e.g. escalated from a broker to the insurer
	// directly).
	OutcomeHandedOff OutcomeKind = "HANDED_OFF"
)

var knownOutcomeKinds = map[OutcomeKind]bool{
	OutcomeExchangeConcluded: true, OutcomeWithdrawn: true, OutcomeHandedOff: true,
}

// IsKnownOutcomeKind reports whether k is a modelled outcome kind.
func IsKnownOutcomeKind(k OutcomeKind) bool { return knownOutcomeKinds[k] }

// CaseOutcome records how the counterparty EXCHANGE (never the claim
// itself) ended.
type CaseOutcome struct {
	CaseID            string        `json:"case_id"`
	Kind              OutcomeKind   `json:"kind"`
	RecordedByPartyID party.PartyID `json:"recorded_by_party_id"`
	Detail            string        `json:"detail,omitempty"`
	RecordedAtTick    uint64        `json:"recorded_at_tick"`
}

// ReviewAdapter is the interface a real counterparty would implement to
// submit a review comment and record how the exchange concluded.
type ReviewAdapter interface {
	SubmitReview(ctx context.Context, review CaseReview) error
	RecordOutcome(ctx context.Context, outcome CaseOutcome) error
}

// ---- Revocation -----------------------------------------------------------

// Revocation withdraws a previously granted authority, qualification,
// or case invitation — the terminal event in every one of this file's
// sub-lifecycles. Deliberately one shared shape rather than three
// near-identical ones (RevokedAuthority/RevokedQualification/
// RevokedInvitation): What is the RevocationSubject naming which of
// those it withdraws.
type RevocationSubject string

const (
	RevocationSubjectAuthority     RevocationSubject = "AUTHORITY"
	RevocationSubjectQualification RevocationSubject = "QUALIFICATION"
	RevocationSubjectInvitation    RevocationSubject = "CASE_INVITATION"
)

var knownRevocationSubjects = map[RevocationSubject]bool{
	RevocationSubjectAuthority: true, RevocationSubjectQualification: true, RevocationSubjectInvitation: true,
}

// IsKnownRevocationSubject reports whether s is a modelled revocation subject.
func IsKnownRevocationSubject(s RevocationSubject) bool { return knownRevocationSubjects[s] }

// Revocation is what a real counterparty (or VERIQO) would issue to
// withdraw a previously granted authority, qualification, or case
// invitation.
type Revocation struct {
	PartyID          party.PartyID     `json:"party_id"`
	Subject          RevocationSubject `json:"subject"`
	CaseID           string            `json:"case_id,omitempty"`
	Reason           string            `json:"reason"`
	RevokedByPartyID party.PartyID     `json:"revoked_by_party_id"`
	RevokedAtTick    uint64            `json:"revoked_at_tick"`
}

// RevocationAdapter is the interface a real counterparty would
// implement to issue a Revocation.
type RevocationAdapter interface {
	Revoke(ctx context.Context, rev Revocation) error
}

// ---- The full lifecycle contract -----------------------------------------

// ErrRevocationMissingReason guards Revocation's own minimal
// invariant — matching party.Relationship's own "REVOKED needs a
// reason" rule, applied here to the network boundary.
var ErrRevocationMissingReason = errors.New("network: Revocation.Reason must be non-empty")

// Validate checks rev's own internal consistency. A real adapter
// implementation would call this before accepting a Revocation, the
// same way policy.Participant.Validate is called before
// AllocateCoInsurance ever runs.
func (rev Revocation) Validate() error {
	if rev.PartyID == "" {
		return errors.New("network: Revocation.PartyID must be non-empty")
	}
	if !IsKnownRevocationSubject(rev.Subject) {
		return errors.New("network: unknown Revocation.Subject")
	}
	if rev.Reason == "" {
		return ErrRevocationMissingReason
	}
	return nil
}

// LifecycleAdapter composes every adapter interface in this file into
// the ONE full contract a real, full-lifecycle counterparty
// integration would implement — Identity, Authority, Evidence Exchange
// and Qualification (embedded from network.go), Case Invitation/
// Acceptance, Clarification, Review/Outcome, and Revocation. A real
// adapter is free to implement only the narrower interfaces it
// actually needs (e.g. a pure qualification registry implements only
// QualificationAdapter) — LifecycleAdapter exists so a full-scope
// counterparty integration has ONE named contract to target, per the
// reviewer's own G3 instruction to extend network.go's interface to
// "mencakup lifecycle nyata" (cover the real lifecycle).
type LifecycleAdapter interface {
	IdentityAdapter
	AuthorityAdapter
	EvidenceExchangeAdapter
	QualificationAdapter
	CaseLifecycleAdapter
	ClarificationAdapter
	ReviewAdapter
	RevocationAdapter
}
