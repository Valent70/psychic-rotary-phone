// Package passport issues the VERIQO Passport: a signed, third-party-
// verifiable statement about a finding.
//
// # What a passport is for
//
// A customer receives a finding. Six months later, in a dispute, the
// other side asks: is this the document VERIQO issued, and does it say
// what it said then? A PDF cannot answer that. A passport can, and it
// can be answered by somebody who does not have access to VERIQO.
//
// # What it carries, and what it deliberately does not
//
// It carries the finding's identity, its evidence root, its
// qualification, its LIMITATIONS, its confidence vector, the versions
// it was produced under, the reviewer, and a replay handle. It does
// NOT carry the evidence itself: a passport is a certificate, not a
// bundle, and putting the material inside it would make every
// verification an exercise in redistributing licensed data.
//
// # The limitations are inside the signature
//
// This is the design decision that matters. A passport whose
// limitations sat outside the signed payload could be presented with
// them stripped, and the signature would still verify. So the payload
// includes them, and a passport with no limitations cannot be issued
// at all.
package passport

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/contract"
	"veriqo/pkg/findings"
	"veriqo/pkg/uncertainty"
)

var (
	ErrNotMinted     = errors.New("passport: the finding was not minted; there is nothing to certify")
	ErrNoLimitations = errors.New("passport: a passport with no stated limitations presents an " +
		"unbounded conclusion")
	ErrNoSigner     = errors.New("passport: no signer")
	ErrBadSignature = errors.New("passport: the signature does not verify")
	ErrTampered     = errors.New("passport: the payload does not match its digest")
	ErrExpired      = errors.New("passport: the passport is outside its validity window")
	ErrRevoked      = errors.New("passport: the passport was revoked")
)

// Signer signs passports. Production backs it with an HSM or KMS.
type Signer interface {
	Sign(message []byte) (signature []byte, keyID string, err error)
}

// Verifier checks a signature. It is separate from Signer because the
// party verifying a passport is by design NOT the party that issued
// it, and giving them the signing interface would be the wrong shape.
type Verifier interface {
	Verify(message, signature []byte, keyID string) error
}

// Payload is what the signature covers.
//
// Every field here is inside the signature. Anything that is not in
// this struct can be altered without breaking verification, which is
// why the limitations, the confidence vector and the qualification are
// all here rather than alongside.
type Payload struct {
	Schema string `json:"schema"`

	FindingID contract.ID `json:"finding_id"`
	CaseID    string      `json:"case_id"`
	TenantID  string      `json:"tenant_id"`

	Statement string `json:"statement"`
	Scope     string `json:"scope"`

	// EvidenceRoot is the digest over the finding's evidence
	// references. It lets a holder confirm they have the same evidence
	// set without the passport carrying the evidence.
	EvidenceRoot string `json:"evidence_root"`
	// FindingDigest is the finding's own content hash.
	FindingDigest string `json:"finding_digest"`

	Qualification string             `json:"qualification"`
	Confidence    uncertainty.Vector `json:"confidence"`
	Limitations   []string           `json:"limitations"`

	// IndependentlyValidated states whether any party outside VERIQO
	// has examined this. It is a required field rather than an
	// optional one, so a passport cannot be silent about it -- silence
	// is what a reader fills in optimistically.
	IndependentlyValidated bool   `json:"independently_validated"`
	ValidatedBy            string `json:"validated_by,omitempty"`

	ProposedBy contract.ID `json:"proposed_by"`
	ApprovedBy contract.ID `json:"approved_by"`
	ApprovedAt time.Time   `json:"approved_at"`

	Versions contract.VersionSet `json:"versions"`

	ReplayReference string `json:"replay_reference"`

	IssuedAt  time.Time  `json:"issued_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Schema is the payload schema identifier, carried inside the
// signature so a verifier knows how to read what it is checking.
const Schema = "veriqo.passport/v1"

// Passport is a signed payload.
type Passport struct {
	Payload   Payload `json:"payload"`
	Digest    string  `json:"digest"`
	Signature string  `json:"signature"`
	KeyID     string  `json:"key_id"`
}

// IssueRequest is what Issue needs.
type IssueRequest struct {
	Finding findings.Finding
	// Qualification is the ladder level this finding reached, as a
	// string, so the passport does not depend on the ladder package.
	Qualification string
	// IndependentlyValidated and ValidatedBy state the external
	// position. Passing false is normal and honest.
	IndependentlyValidated bool
	ValidatedBy            string

	IssuedAt  time.Time
	ExpiresAt *time.Time
	Signer    Signer
}

// Issue produces a passport.
func Issue(req IssueRequest) (Passport, error) {
	if err := findings.Require(req.Finding); err != nil {
		return Passport{}, fmt.Errorf("%w: %v", ErrNotMinted, err)
	}
	if req.Signer == nil {
		return Passport{}, ErrNoSigner
	}
	if req.IssuedAt.IsZero() {
		return Passport{}, errors.New("passport: no issue instant")
	}
	if len(req.Finding.Limitations) == 0 {
		return Passport{}, ErrNoLimitations
	}
	if req.IndependentlyValidated && strings.TrimSpace(req.ValidatedBy) == "" {
		return Passport{}, errors.New("passport: a claim of independent validation must name " +
			"the party that performed it")
	}
	if strings.TrimSpace(req.Qualification) == "" {
		return Passport{}, errors.New("passport: no qualification level")
	}

	fd, err := req.Finding.Digest()
	if err != nil {
		return Passport{}, err
	}
	root, err := jcs.Hash(sortedCopy(req.Finding.EvidenceRefs))
	if err != nil {
		return Passport{}, err
	}

	p := Payload{
		Schema:    Schema,
		FindingID: req.Finding.ID, CaseID: req.Finding.CaseID, TenantID: req.Finding.TenantID,
		Statement: req.Finding.Statement, Scope: req.Finding.Scope.String(),
		EvidenceRoot: root, FindingDigest: fd,
		Qualification:          req.Qualification,
		Confidence:             req.Finding.Confidence,
		Limitations:            sortedCopy(req.Finding.Limitations),
		IndependentlyValidated: req.IndependentlyValidated,
		ValidatedBy:            req.ValidatedBy,
		ProposedBy:             req.Finding.ProposedBy,
		ApprovedBy:             req.Finding.ApprovedBy,
		ApprovedAt:             req.Finding.ApprovedAt,
		Versions:               req.Finding.Versions,
		ReplayReference:        req.Finding.ReplayReference,
		IssuedAt:               req.IssuedAt,
		ExpiresAt:              req.ExpiresAt,
	}

	msg, err := jcs.Canonicalize(p)
	if err != nil {
		return Passport{}, err
	}
	digest := jcs.HashBytes(msg)
	sig, keyID, err := req.Signer.Sign(msg)
	if err != nil {
		return Passport{}, fmt.Errorf("passport: signing: %w", err)
	}
	return Passport{Payload: p, Digest: digest,
		Signature: hex.EncodeToString(sig), KeyID: keyID}, nil
}

// Revocation is a statement that a passport should no longer be
// relied on.
//
// A passport cannot be recalled once issued -- the holder has it. What
// can be published is a revocation, and Verify consults the list it is
// given. A verifier with no revocation list is told so rather than
// left to assume none exists.
type Revocation struct {
	FindingID contract.ID `json:"finding_id"`
	Digest    string      `json:"digest"`
	At        time.Time   `json:"at"`
	Reason    string      `json:"reason"`
}

// VerifyOptions carry what a third party needs to check a passport.
type VerifyOptions struct {
	Verifier Verifier
	// At is the instant to check validity against.
	At time.Time
	// Revocations is the list consulted. A nil list means the verifier
	// has NOT checked revocation, and Verify reports that rather than
	// treating it as "not revoked".
	Revocations []Revocation
}

// Result is what a verification concluded.
type Result struct {
	SignatureValid    bool
	DigestValid       bool
	WithinValidity    bool
	RevocationChecked bool
	Revoked           bool
	RevocationReason  string
	// Caveats are things the verifier must report even on success.
	Caveats []string
}

// Trustworthy reports whether the passport may be relied on.
//
// Revocation NOT having been checked is a caveat, not a failure: the
// passport is genuine and the verifier does not know whether it stands.
// Conflating the two would make an offline verifier report a valid
// passport as invalid.
func (r Result) Trustworthy() bool {
	return r.SignatureValid && r.DigestValid && r.WithinValidity && !r.Revoked
}

// Verify checks a passport. It is written from the verifier's side:
// nothing here needs access to VERIQO.
func Verify(p Passport, opts VerifyOptions) (Result, error) {
	var res Result
	if opts.Verifier == nil {
		return res, ErrNoSigner
	}
	if opts.At.IsZero() {
		return res, errors.New("passport: no instant; validity cannot be evaluated")
	}
	if p.Payload.Schema != Schema {
		return res, fmt.Errorf("passport: unknown schema %q", p.Payload.Schema)
	}

	msg, err := jcs.Canonicalize(p.Payload)
	if err != nil {
		return res, err
	}
	if jcs.HashBytes(msg) != p.Digest {
		return res, fmt.Errorf("%w: the payload was altered after issue", ErrTampered)
	}
	res.DigestValid = true

	sig, err := hex.DecodeString(p.Signature)
	if err != nil {
		return res, fmt.Errorf("passport: signature is not hex: %w", err)
	}
	if err := opts.Verifier.Verify(msg, sig, p.KeyID); err != nil {
		return res, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	res.SignatureValid = true

	res.WithinValidity = !opts.At.Before(p.Payload.IssuedAt)
	if p.Payload.ExpiresAt != nil && !opts.At.Before(*p.Payload.ExpiresAt) {
		res.WithinValidity = false
	}
	if !res.WithinValidity {
		return res, fmt.Errorf("%w: issued %s", ErrExpired,
			p.Payload.IssuedAt.Format(time.RFC3339))
	}

	if opts.Revocations != nil {
		res.RevocationChecked = true
		for _, r := range opts.Revocations {
			if r.FindingID == p.Payload.FindingID || r.Digest == p.Digest {
				res.Revoked = true
				res.RevocationReason = r.Reason
			}
		}
		if res.Revoked {
			return res, fmt.Errorf("%w: %s", ErrRevoked, res.RevocationReason)
		}
	} else {
		res.Caveats = append(res.Caveats,
			"revocation was NOT checked: this passport is genuine, and whether it still "+
				"stands is unknown to this verifier")
	}

	// The caveats a verifier must report even on a clean verification.
	if !p.Payload.IndependentlyValidated {
		res.Caveats = append(res.Caveats,
			"no party outside VERIQO has examined this finding; it was produced, tested and "+
				"approved within VERIQO")
	}
	if len(p.Payload.Limitations) > 0 {
		res.Caveats = append(res.Caveats,
			fmt.Sprintf("the finding states %d limitation(s), which are part of the signed "+
				"payload and travel with it", len(p.Payload.Limitations)))
	}
	if w, l := p.Payload.Confidence.Weakest(); l != uncertainty.High {
		res.Caveats = append(res.Caveats,
			fmt.Sprintf("the weakest confidence dimension is %s (%s)", w, l))
	}
	return res, nil
}

// Render produces the human-readable certificate.
func (p Passport) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "VERIQO PASSPORT (%s)\n", p.Payload.Schema)
	fmt.Fprintf(&b, "  finding:       %s\n", p.Payload.FindingID)
	fmt.Fprintf(&b, "  case:          %s\n", p.Payload.CaseID)
	fmt.Fprintf(&b, "  statement:     %s\n", p.Payload.Statement)
	fmt.Fprintf(&b, "  scope:         %s\n", p.Payload.Scope)
	fmt.Fprintf(&b, "  qualification: %s\n", p.Payload.Qualification)
	if p.Payload.IndependentlyValidated {
		fmt.Fprintf(&b, "  validated by:  %s (outside VERIQO)\n", p.Payload.ValidatedBy)
	} else {
		b.WriteString("  validated by:  NOBODY OUTSIDE VERIQO\n")
	}
	w, l := p.Payload.Confidence.Weakest()
	fmt.Fprintf(&b, "  weakest confidence dimension: %s (%s)\n", w, l)
	b.WriteString("  limitations (inside the signature):\n")
	for _, lim := range p.Payload.Limitations {
		fmt.Fprintf(&b, "    - %s\n", lim)
	}
	fmt.Fprintf(&b, "  evidence root: %s\n", p.Payload.EvidenceRoot)
	fmt.Fprintf(&b, "  replay:        %s\n", p.Payload.ReplayReference)
	fmt.Fprintf(&b, "  digest:        %s\n", p.Digest)
	fmt.Fprintf(&b, "  key:           %s\n", p.KeyID)
	return b.String()
}

func sortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}
