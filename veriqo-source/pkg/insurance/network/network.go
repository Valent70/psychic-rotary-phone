// Package network defines the ADAPTER BOUNDARY for VERIQO's real-world
// insurance network — the interfaces a real insurer, broker,
// reinsurer, P&I club, surveyor, loss adjuster, or salvor system would
// implement to exchange evidence and qualification state with VERIQO.
//
// Round 8's own work order (§IV) is explicit: "Bangun: network model,
// adapter interfaces, external qualification states, evidence exchange
// contract... Tanpa fake live integrations" (build the model, the
// adapter interfaces, external qualification states, an evidence
// exchange contract — WITHOUT fake live integrations). This package is
// deliberately INTERFACES AND DATA CONTRACTS ONLY:
//
//   - EvidenceExchangeAdapter and QualificationAdapter are Go
//     interfaces. Neither has a concrete implementation in this
//     package, this round, or anywhere in this repository — a real
//     implementation requires a real counterparty system, which is
//     categorically external and cannot be honestly fabricated inside
//     a sandboxed engineering session (the same rule that keeps
//     hsm_kms, live_data, and pentest BLOCKED_EXTERNAL applies here).
//   - ExchangeRequest/ExchangeReceipt/QualificationState are pure data
//     shapes: what a real exchange contract would carry, not a
//     simulation of one being carried.
//   - Participant roles are DELIBERATELY REUSED from
//     pkg/insurance/party.Role (insurer, broker, reinsurer, P&I club,
//     surveyor, loss adjuster, salvor all already exist there) —
//     see participantRoles below — rather than a second, competing
//     role vocabulary, per this program's own REUSE > EXTEND >
//     REFACTOR > CREATE rule.
//
// The honest status of this package is INTERFACE_READY: the contract
// a real adapter would implement exists, is compiled, and is type-
// checked against a real (if empty) reference implementation in this
// package's own test file — but no external counterparty has ever
// implemented it against a real system, so this package proves
// nothing about live interoperability. See
// BLOCKER_REGISTER.json / FINAL_MASTER_GAP_MATRIX.json for how this
// status is reported alongside the eight genuinely external blockers.
package network

import (
	"context"
	"errors"
	"fmt"

	"veriqo/pkg/insurance/party"
)

// participantRoles names the party.Role values this network models as
// counterparties — the work order's own worked list (§IV): insurer,
// broker, reinsurer, P&I club, surveyor, adjuster, salvage. Every one
// of these already exists in pkg/insurance/party's 45-role registry;
// this function only selects the subset relevant to network exchange,
// it does not declare a new role vocabulary.
func participantRoles() []party.Role {
	return []party.Role{
		party.RoleInsurer, party.RoleBroker, party.RoleReinsurer,
		party.RolePAndIClub, party.RoleSurveyor, party.RoleLossAdjuster,
		party.RoleSalvageParty,
	}
}

// IsNetworkParticipantRole reports whether r is one of the roles this
// package models as a real-world network counterparty.
func IsNetworkParticipantRole(r party.Role) bool {
	for _, known := range participantRoles() {
		if r == known {
			return true
		}
	}
	return false
}

// QualificationState is the EXTERNAL counterparty's own attestation
// state, as reported through the network — deliberately a DIFFERENT
// vocabulary from internal/assurance.CanonicalStatus (which describes
// this REPOSITORY's own readiness gates, never an external party's
// standing). Conflating the two would be exactly the "one gap, two
// names" (or here, "two parties, one name") drift this program's own
// governing rules forbid.
type QualificationState string

const (
	// StateUnverified: VERIQO has recorded a counterparty's identity but
	// no attestation of any kind has been exchanged.
	StateUnverified QualificationState = "UNVERIFIED"
	// StateSelfAttested: the counterparty has asserted its own
	// qualification (e.g. "we are a licensed P&I club in jurisdiction
	// X") with no independent corroboration. Never conflated with
	// externally verified — see this program's own evidence.Strength
	// discipline for the same distinction applied to evidence records.
	StateSelfAttested QualificationState = "SELF_ATTESTED"
	// StateExternallyVerified: a named independent third party
	// corroborated the counterparty's qualification (e.g. a regulator's
	// public register, a real accreditation body).
	StateExternallyVerified QualificationState = "EXTERNALLY_VERIFIED"
	// StateRevoked: a qualification that was once attested or verified
	// has since been withdrawn.
	StateRevoked QualificationState = "REVOKED"
)

var knownStates = map[QualificationState]bool{
	StateUnverified: true, StateSelfAttested: true, StateExternallyVerified: true, StateRevoked: true,
}

// IsKnownQualificationState reports whether s is a modelled state.
func IsKnownQualificationState(s QualificationState) bool { return knownStates[s] }

// ExchangeRequest is what a real evidence exchange contract would
// carry from VERIQO to a counterparty adapter, or vice versa: enough
// to identify the case, the evidence content (by hash, never raw bytes
// embedded in the contract type itself — matching this codebase's
// evidence-by-reference discipline elsewhere), and who is sending it.
type ExchangeRequest struct {
	CaseID              string        `json:"case_id"`
	EvidenceContentHash string        `json:"evidence_content_hash"`
	SenderPartyID       party.PartyID `json:"sender_party_id"`
	SenderRole          party.Role    `json:"sender_role"`
	RequestedAtTick     uint64        `json:"requested_at_tick"`
}

// ReceiptVerificationStatus is whether THIS receipt's own
// ReceiptSignature/CredentialID has actually been cryptographically
// checked, and with what result — a different axis from both
// QualificationState (the counterparty's own attested standing) and
// credential.Status (a credential's lifecycle: active/expired/revoked).
// No verifier exists anywhere in this repository (matching this
// program's "never fabricate a signature or its verification" rule),
// so VerificationNotPerformed is the only value any real caller can
// honestly set today; VerificationVerified/VerificationFailed exist so
// the SHAPE is ready for a real verifier without a second migration.
type ReceiptVerificationStatus string

const (
	VerificationNotPerformed ReceiptVerificationStatus = "NOT_PERFORMED"
	VerificationVerified     ReceiptVerificationStatus = "VERIFIED"
	VerificationFailed       ReceiptVerificationStatus = "VERIFICATION_FAILED"
)

var knownVerificationStatuses = map[ReceiptVerificationStatus]bool{
	VerificationNotPerformed: true, VerificationVerified: true, VerificationFailed: true,
}

// IsKnownVerificationStatus reports whether s is a modelled receipt
// verification status.
func IsKnownVerificationStatus(s ReceiptVerificationStatus) bool { return knownVerificationStatuses[s] }

// ExchangeReceipt is what a real adapter would hand back: an
// acknowledgement the exchange happened, with enough to make the
// acknowledgement itself independently verifiable (a receiver-signed
// hash of what was actually received, not merely an "OK" boolean).
//
// FINAL INTERNAL CHECK item F ("external evidence receipt") names seven
// things every external exchange must carry: source, timestamp, issuer,
// content hash, signature/credential, receipt, and verification status
// — generalising the same shape payment/settlement.go's own
// SettlementEvidence already models for one specific exchange (a bank
// confirmation) to every external exchange this package's own
// EvidenceExchangeAdapter covers. The mapping onto this struct's own
// fields: Source/ReceivedAtTick (source, timestamp), IssuerPartyID
// (issuer), EvidenceContentHash (content hash), ReceiptSignature/
// CredentialID (signature/credential), ReceiptReference (receipt, this
// struct's own identity), VerificationStatus (verification status).
type ExchangeReceipt struct {
	CaseID              string        `json:"case_id"`
	EvidenceContentHash string        `json:"evidence_content_hash"`
	ReceivedByPartyID   party.PartyID `json:"received_by_party_id"`
	ReceivedAtTick      uint64        `json:"received_at_tick"`
	// Source names WHAT/WHO this receipt actually came from (e.g. "P&I
	// club claims portal", "broker EDI gateway") — free text, matching
	// SettlementEvidence.SourceDescription's own "a stated description
	// beats a closed taxonomy of every real-world system" convention,
	// never a hard-coded named vendor (this domain's guardrails forbid
	// that at the whole-tree level).
	Source string `json:"source"`
	// IssuerPartyID is WHO issued this receipt — the counterparty party
	// actually asserting the exchange happened, which is not always the
	// same party as ReceivedByPartyID (a broker's gateway may issue a
	// receipt on behalf of the insurer that ultimately received it).
	IssuerPartyID party.PartyID `json:"issuer_party_id"`
	// ReceiptReference is the receipt's own reference/ID as the issuer
	// names it (a portal confirmation number, an EDI control number) —
	// matching SettlementEvidence.Reference's own free-text convention.
	ReceiptReference string `json:"receipt_reference"`
	// CredentialID, if non-empty, names the pkg/insurance/credential.
	// Credential backing the issuer's authority to issue this receipt —
	// by reference only, matching this domain's evidence-by-reference
	// discipline throughout (never a duplicated credential payload).
	CredentialID string `json:"credential_id,omitempty"`
	// ReceiptSignature is what a real adapter implementation would
	// populate with a real cryptographic signature over the receipt's
	// own content. Empty here: this package defines the SHAPE, never a
	// fabricated signature.
	ReceiptSignature string `json:"receipt_signature,omitempty"`
	// VerificationStatus is whether ReceiptSignature/CredentialID have
	// actually been checked — see ReceiptVerificationStatus's own doc
	// comment for why VerificationNotPerformed is the only honest
	// default today.
	VerificationStatus ReceiptVerificationStatus `json:"verification_status"`
}

var (
	ErrEmptyReceiptCaseID  = errors.New("network: ExchangeReceipt.CaseID must be non-empty")
	ErrEmptyContentHash    = errors.New("network: ExchangeReceipt.EvidenceContentHash must be non-empty")
	ErrEmptyReceivedBy     = errors.New("network: ExchangeReceipt.ReceivedByPartyID must be non-empty")
	ErrEmptySource         = errors.New("network: ExchangeReceipt.Source must be non-empty")
	ErrEmptyIssuer         = errors.New("network: ExchangeReceipt.IssuerPartyID must be non-empty")
	ErrEmptyReceiptRef     = errors.New("network: ExchangeReceipt.ReceiptReference must be non-empty")
	ErrUnknownVerification = errors.New("network: ExchangeReceipt.VerificationStatus is not a known value")
)

// Validate reports whether r carries every field FINAL INTERNAL CHECK
// item F requires of an external evidence receipt: source, timestamp
// (ReceivedAtTick, checked structurally by its own non-zero-ness being
// the caller's responsibility to set — ticks legitimately start at 0
// only for a case's own genesis, never for an external receipt, so this
// package does not additionally gate on it), issuer, content hash,
// receipt reference, and a recognised verification status.
// ReceiptSignature/CredentialID are deliberately NOT required: neither
// has ever been fabricated anywhere in this repository (see their own
// doc comments), and requiring them would force a caller to invent one.
func (r ExchangeReceipt) Validate() error {
	if r.CaseID == "" {
		return ErrEmptyReceiptCaseID
	}
	if r.EvidenceContentHash == "" {
		return ErrEmptyContentHash
	}
	if r.ReceivedByPartyID == "" {
		return ErrEmptyReceivedBy
	}
	if r.Source == "" {
		return ErrEmptySource
	}
	if r.IssuerPartyID == "" {
		return ErrEmptyIssuer
	}
	if r.ReceiptReference == "" {
		return ErrEmptyReceiptRef
	}
	if !IsKnownVerificationStatus(r.VerificationStatus) {
		return fmt.Errorf("%w: %q", ErrUnknownVerification, r.VerificationStatus)
	}
	return nil
}

// EvidenceExchangeAdapter is the interface a real counterparty
// integration would implement to exchange evidence with VERIQO. No
// implementation of this interface exists anywhere in this repository
// — see the package doc for why that is the honest state, not an
// oversight.
type EvidenceExchangeAdapter interface {
	// SubmitEvidence sends req to the counterparty this adapter
	// represents and returns their receipt, or an error if the real
	// exchange failed.
	SubmitEvidence(ctx context.Context, req ExchangeRequest) (ExchangeReceipt, error)
	// FetchQualificationState asks the counterparty (or a real registry
	// this adapter is wired to) for a party's current QualificationState.
	FetchQualificationState(ctx context.Context, p party.PartyID) (QualificationState, error)
}

// QualificationAdapter is the narrower interface a real external
// qualification registry (e.g. a regulator's public license register)
// would implement — separated from EvidenceExchangeAdapter because a
// real deployment may have a qualification source that is NOT the same
// system it exchanges evidence with (e.g. checking P&I Club membership
// against the International Group's own published list, entirely
// independent of any evidence-exchange counterparty).
type QualificationAdapter interface {
	VerifyQualification(ctx context.Context, p party.PartyID, role party.Role) (QualificationState, error)
}
