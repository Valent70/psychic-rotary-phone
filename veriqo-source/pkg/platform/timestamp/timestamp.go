// Package timestamp separates two things that look alike and are not:
// a tamper-evident temporal chain, and an independent temporal
// attestation.
//
// VERIQO operates a self-hosted timestamping facility. It is good at
// what it does: it chains entries so that reordering or altering one is
// detectable, and it gives every record a position in a total order.
// That is integrity chaining, and it is genuinely useful.
//
// It is not a trusted timestamp. RFC 3161 defines a trusted timestamp as
// an assertion, by a Time-Stamping Authority acting as a trusted third
// party, that a datum existed before a particular time. The load-bearing
// words are "third party": the value of the assertion comes from the
// attester being someone other than the party who benefits from it. A
// facility VERIQO runs, for evidence VERIQO holds, in a dispute VERIQO's
// customer is party to, is not a third party however good its
// cryptography.
//
//	VERIQO self-hosted timestamp = tamper-evident temporal chain
//	External TSA                 = independent temporal attestation
//
// The two must never be reported as the same thing, so this package
// makes them different types with different capabilities. A chain
// attestation cannot answer "did this exist before 14:00 on the 3rd?"
// because nothing stops the chain operator from writing whatever time
// it likes; only ProvesExistenceBefore, which only an external
// attestation returns true from, answers that.
//
// The package models attestations. It does not talk to a TSA: acquiring
// real RFC 3161 tokens needs a TSA relationship, which is an external
// dependency VERIQO does not have. Attestations produced here from a
// real token are as good as the token; attestations produced here from
// nothing are honestly labelled NONE.
package timestamp

import (
	"errors"
	"fmt"
	"strings"

	"veriqo/pkg/canonical/jcs"
)

// Kind is what sort of temporal assertion an attestation carries.
//
// None is the zero value. A record that carries no attestation must not
// read as if it carries the strongest one.
type Kind int

const (
	// None: no temporal attestation of any sort.
	None Kind = iota
	// TamperEvidentChain: a position in VERIQO's own hash-linked
	// sequence. Proves ordering and non-alteration within the chain.
	// Proves nothing about wall-clock time.
	TamperEvidentChain
	// IndependentAttestation: an RFC 3161 (or equivalent) token from a
	// Time-Stamping Authority independent of every party to the matter.
	IndependentAttestation
)

func (k Kind) String() string {
	switch k {
	case TamperEvidentChain:
		return "TAMPER_EVIDENT_CHAIN"
	case IndependentAttestation:
		return "INDEPENDENT_ATTESTATION"
	default:
		return "NONE"
	}
}

// ProvesExistenceBefore reports whether this kind of attestation can
// support the RFC 3161 assertion — that the datum existed before a
// stated time.
//
// Only an independent attestation can. This is the single method that
// the whole package exists to get right.
func (k Kind) ProvesExistenceBefore() bool { return k == IndependentAttestation }

// ProvesOrdering reports whether the attestation establishes a position
// relative to other records. Both real kinds do; a chain does it within
// its own sequence, a TSA does it against wall-clock time.
func (k Kind) ProvesOrdering() bool { return k != None }

var (
	ErrNoDigest          = errors.New("timestamp: an attestation must cover a message digest")
	ErrNoChainOperator   = errors.New("timestamp: a chain attestation must name its operator")
	ErrNoPriorHash       = errors.New("timestamp: a chain attestation past the genesis entry must carry a prior hash")
	ErrNoTSA             = errors.New("timestamp: an independent attestation must name its authority")
	ErrNoToken           = errors.New("timestamp: an independent attestation must carry the authority's token")
	ErrNotIndependent    = errors.New("timestamp: the timestamping authority is not independent of a party to the matter")
	ErrDigestMismatch    = errors.New("timestamp: the attestation covers a different digest")
	ErrKindMismatch      = errors.New("timestamp: the attestation kind does not match its contents")
	ErrGenTimeNotOrdered = errors.New("timestamp: the accuracy window is not ordered")
)

// ChainAttestation is a position in VERIQO's tamper-evident temporal
// chain.
//
// Note what it does not have: no wall-clock time field that could be
// mistaken for a trusted one. The chain proves order and integrity.
// Where a deployment records wall-clock time alongside, that time is an
// operational annotation, and putting it here would invite reading it
// as an attested one.
type ChainAttestation struct {
	// Digest is the hash of the datum this entry covers.
	Digest string
	// SequenceNo is the entry's position in the chain.
	SequenceNo uint64
	// PriorEntryHash links to the previous entry. Empty only at the
	// genesis entry.
	PriorEntryHash string
	// EntryHash covers Digest, SequenceNo and PriorEntryHash.
	EntryHash string
	// OperatorID is who runs the chain. Recorded because the operator's
	// identity is exactly what disqualifies the chain from being an
	// independent attestation.
	OperatorID string
}

// NewChainAttestation builds and hashes a chain entry.
func NewChainAttestation(digest string, seq uint64, priorEntryHash, operatorID string) (ChainAttestation, error) {
	if strings.TrimSpace(digest) == "" {
		return ChainAttestation{}, ErrNoDigest
	}
	if strings.TrimSpace(operatorID) == "" {
		return ChainAttestation{}, ErrNoChainOperator
	}
	if seq > 0 && strings.TrimSpace(priorEntryHash) == "" {
		return ChainAttestation{}, fmt.Errorf("%w: sequence %d", ErrNoPriorHash, seq)
	}
	c := ChainAttestation{Digest: digest, SequenceNo: seq, PriorEntryHash: priorEntryHash, OperatorID: operatorID}
	h, err := jcs.Hash(map[string]any{
		"digest": digest, "sequence_no": seq, "prior": priorEntryHash, "operator": operatorID,
	})
	if err != nil {
		return ChainAttestation{}, fmt.Errorf("timestamp: chain entry hash: %w", err)
	}
	c.EntryHash = h
	return c, nil
}

// VerifyChain checks that a run of entries is unbroken and unaltered.
//
// It verifies exactly what a chain can verify: each entry's hash matches
// its contents, and each links to the one before. It says nothing about
// when any of it happened.
func VerifyChain(entries []ChainAttestation) error {
	for i, e := range entries {
		recomputed, err := jcs.Hash(map[string]any{
			"digest": e.Digest, "sequence_no": e.SequenceNo,
			"prior": e.PriorEntryHash, "operator": e.OperatorID,
		})
		if err != nil {
			return fmt.Errorf("timestamp: entry %d: %w", i, err)
		}
		if recomputed != e.EntryHash {
			return fmt.Errorf("timestamp: entry %d (sequence %d) has been altered", i, e.SequenceNo)
		}
		if i == 0 {
			continue
		}
		prev := entries[i-1]
		if e.SequenceNo != prev.SequenceNo+1 {
			return fmt.Errorf("timestamp: entry %d breaks the sequence: %d follows %d", i, e.SequenceNo, prev.SequenceNo)
		}
		if e.PriorEntryHash != prev.EntryHash {
			return fmt.Errorf("timestamp: entry %d does not link to its predecessor", i)
		}
	}
	return nil
}

// TSA identifies a Time-Stamping Authority.
type TSA struct {
	// Name is the authority's identity as it appears in its certificate.
	Name string
	// PolicyOID is the timestamping policy the token was issued under.
	PolicyOID string
	// CertificateFingerprint pins the signing certificate.
	CertificateFingerprint string
	// OperatorID is the legal entity operating the TSA. This is the
	// field independence is judged on, and it is separate from Name
	// because a service name is not an entity.
	OperatorID string
}

// IndependentOf reports whether this authority is independent of every
// listed party — the parties to the matter, and VERIQO itself.
//
// The test is deliberately blunt: an operator that appears anywhere in
// the interested set is not independent. There is no partial
// independence, and no argument from good governance that turns a
// self-run TSA into a third party.
func (t TSA) IndependentOf(interested ...string) bool {
	for _, p := range interested {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(t.OperatorID), strings.TrimSpace(p)) {
			return false
		}
	}
	return true
}

// ExternalAttestation is an RFC 3161-style token: an authority's signed
// assertion that a datum existed before a stated time.
type ExternalAttestation struct {
	// Digest is the message imprint the token covers.
	Digest string
	// Authority issued the token.
	Authority TSA
	// SerialNumber is the authority's identifier for this token.
	SerialNumber string
	// GenTimeUnix is the time asserted by the authority.
	GenTimeUnix int64
	// AccuracySeconds is the authority's stated accuracy. The assertion
	// is that the datum existed before GenTimeUnix+AccuracySeconds; a
	// zero accuracy means the authority stated none, not that it is
	// perfect.
	AccuracySeconds int64
	// Token is the encoded token as issued. VERIQO stores it verbatim so
	// a third party can verify it without trusting VERIQO's parsing.
	Token []byte
	// Ordering is the RFC 3161 ordering flag as issued.
	Ordering bool
}

// UpperBoundUnix is the time before which the datum is asserted to have
// existed, taking the authority's own accuracy into account.
func (e ExternalAttestation) UpperBoundUnix() int64 { return e.GenTimeUnix + e.AccuracySeconds }

// Validate checks the attestation is structurally a real token record.
func (e ExternalAttestation) Validate() error {
	if strings.TrimSpace(e.Digest) == "" {
		return ErrNoDigest
	}
	if strings.TrimSpace(e.Authority.Name) == "" || strings.TrimSpace(e.Authority.OperatorID) == "" {
		return ErrNoTSA
	}
	if len(e.Token) == 0 {
		return ErrNoToken
	}
	if e.AccuracySeconds < 0 {
		return ErrGenTimeNotOrdered
	}
	return nil
}

// Attestation is the temporal standing of one datum: what VERIQO's own
// chain says, and what an outside authority says, kept apart.
//
// Kind is derived by Assess, never set by a caller. That is the whole
// point: the field that decides whether "this existed before then" may
// be claimed is not one anybody can write.
type Attestation struct {
	Digest   string
	Chain    *ChainAttestation
	External *ExternalAttestation
	kind     Kind
	// notIndependentReason explains a downgrade, so a record that looks
	// like it has an external token but is not independent says why.
	notIndependentReason string
}

// Kind returns the derived kind.
func (a Attestation) Kind() Kind { return a.kind }

// NotIndependentReason explains why an external token present on this
// attestation did not raise it to an independent attestation. Empty when
// there is nothing to explain.
func (a Attestation) NotIndependentReason() string { return a.notIndependentReason }

// ProvesExistenceBefore reports whether this attestation supports the
// RFC 3161 assertion, and the upper bound it supports.
//
// A chain-only attestation returns false. This is the method the rest of
// the system asks, and answering it honestly is what stops a
// tamper-evident chain being marketed, reported or pleaded as a trusted
// timestamp.
func (a Attestation) ProvesExistenceBefore() (bound int64, ok bool) {
	if a.kind != IndependentAttestation || a.External == nil {
		return 0, false
	}
	return a.External.UpperBoundUnix(), true
}

// Assess derives the attestation kind from what is actually present and
// actually independent.
//
// interested lists every party the attestation must be independent of:
// the parties to the matter and VERIQO's own operating entity. An
// external token from an authority operated by one of them is retained —
// it is still evidence of something — but it is classified as a chain-
// grade attestation at best, with the reason recorded.
func Assess(digest string, chain *ChainAttestation, external *ExternalAttestation, interested []string) (Attestation, error) {
	if strings.TrimSpace(digest) == "" {
		return Attestation{}, ErrNoDigest
	}
	a := Attestation{Digest: digest, Chain: chain, External: external}

	if chain != nil {
		if chain.Digest != digest {
			return Attestation{}, fmt.Errorf("%w: chain covers %q", ErrDigestMismatch, chain.Digest)
		}
		a.kind = TamperEvidentChain
	}

	if external != nil {
		if err := external.Validate(); err != nil {
			return Attestation{}, err
		}
		if external.Digest != digest {
			return Attestation{}, fmt.Errorf("%w: token covers %q", ErrDigestMismatch, external.Digest)
		}
		if external.Authority.IndependentOf(interested...) {
			a.kind = IndependentAttestation
		} else {
			a.notIndependentReason = fmt.Sprintf(
				"the timestamping authority is operated by %q, which is a party to this matter",
				external.Authority.OperatorID)
			// Deliberately not promoted. A token from an interested
			// operator is not an independent attestation, and the
			// attestation stays at whatever the chain supports.
		}
	}
	return a, nil
}

// RequireIndependent is the strict form, for callers that may not
// proceed on a chain alone — an evidential claim about when something
// existed, a limitation period, a priority dispute.
func RequireIndependent(a Attestation) error {
	if a.kind == IndependentAttestation {
		return nil
	}
	if a.notIndependentReason != "" {
		return fmt.Errorf("%w: %s", ErrNotIndependent, a.notIndependentReason)
	}
	return fmt.Errorf("%w: the attestation is %s, which proves ordering and integrity but not existence before a time",
		ErrNotIndependent, a.kind)
}

// Describe states, in one sentence a non-engineer can act on, what this
// attestation does and does not establish. It exists so reports quote
// the system rather than paraphrasing it.
func Describe(a Attestation) string {
	switch a.kind {
	case IndependentAttestation:
		return fmt.Sprintf(
			"Independently attested by %s: the datum existed before Unix time %d. Ordering and integrity follow.",
			a.External.Authority.Name, a.External.UpperBoundUnix())
	case TamperEvidentChain:
		s := fmt.Sprintf(
			"Recorded in %s's tamper-evident chain at sequence %d: ordering within the chain and non-alteration are established. No independent attestation of wall-clock time.",
			a.Chain.OperatorID, a.Chain.SequenceNo)
		if a.notIndependentReason != "" {
			s += " A timestamp token is present but was not counted: " + a.notIndependentReason + "."
		}
		return s
	default:
		s := "No temporal attestation: neither ordering nor existence before any time is established."
		if a.notIndependentReason != "" {
			// A token is present and was not counted. Saying only "no
			// attestation" would hide that a timestamp exists, which is
			// the thing a reader most needs to know about before they
			// find it themselves and assume it was overlooked.
			s += " A timestamp token is present but was not counted: " + a.notIndependentReason + "."
		}
		return s
	}
}
