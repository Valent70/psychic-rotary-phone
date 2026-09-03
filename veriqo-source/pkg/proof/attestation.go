package proof

import (
	"errors"
	"fmt"
	"strings"
)

// The three levels of proof.
//
// "Proof Object" and "proof" are not the same word, and conflating them
// is the single most available way for this system to overstate itself.
// A Proof Object is a container: it exists the moment somebody assembles
// one, and assembling one says nothing about whether the contents are
// any good. Qualification says the contents satisfy VERIQO's own rules.
// External attestation says somebody who is not VERIQO looked and
// agreed.
//
//	PROOF_OBJECT_CREATED
//	      ↓
//	PROOF_QUALIFIED
//	      ↓
//	PROOF_EXTERNALLY_ATTESTED
//
// The distance between the second and the third is the distance between
//
//	VERIQO: "the evidence set satisfies the qualification rules"
//
// and
//
//	Independent expert: "we examined the evidence and agree the
//	qualification procedure was correctly applied"
//
// The second sentence cannot be produced from inside this repository at
// any level of engineering effort, and Level 3 is therefore unreachable
// by construction: RaiseToExternallyAttested is the only way in, it
// demands a named external party with a reference, and no code path in
// VERIQO calls it.

// AttestationLevel is how far a conclusion's standing actually reaches.
//
// LevelObjectCreated is the zero value, deliberately. A Proof Object that
// nobody has qualified is exactly a container, and the zero value says
// so rather than flattering it.
type AttestationLevel int

const (
	// LevelObjectCreated: a Proof Object exists. Its components were
	// assembled and it sealed. Nothing about the quality of the
	// contents follows.
	LevelObjectCreated AttestationLevel = iota
	// LevelQualified: the object sealed as SUPPORT and SUFFICIENT, so
	// VERIQO's own qualification rules are satisfied. This is the
	// highest level reachable without anybody outside.
	LevelQualified
	// LevelExternallyAttested: an identified party who is not VERIQO
	// examined the evidence and agreed the qualification procedure was
	// correctly applied.
	LevelExternallyAttested
)

func (l AttestationLevel) String() string {
	switch l {
	case LevelQualified:
		return "PROOF_QUALIFIED"
	case LevelExternallyAttested:
		return "PROOF_EXTERNALLY_ATTESTED"
	default:
		return "PROOF_OBJECT_CREATED"
	}
}

// RequiresOutsideParty reports whether reaching this level needs
// somebody who is not VERIQO. Only LevelExternallyAttested does, which is
// why no amount of further engineering reaches it.
func (l AttestationLevel) RequiresOutsideParty() bool { return l == LevelExternallyAttested }

// IsProof reports whether the level supports the unqualified word
// "proof" to an outside reader.
//
// Only LevelExternallyAttested does. This method exists so that reports ask
// the type rather than the author: a sentence beginning "VERIQO has
// proved" is checkable against it.
func (l AttestationLevel) IsProof() bool { return l == LevelExternallyAttested }

var (
	ErrNoAttestor         = errors.New("proof: external attestation requires a named attestor and reference")
	ErrAttestorIsVeriqo   = errors.New("proof: the attestor may not be VERIQO or any party to the matter")
	ErrNotQualified       = errors.New("proof: only a qualified proof object can be externally attested")
	ErrAttestationSubject = errors.New("proof: the attestation covers a different proof object")
)

// ProcedureAttestation is an outside party's statement about a sealed
// Proof Object.
//
// Note what it attests to: that the *procedure* was correctly applied.
// An external party does not certify that the conclusion is true — no
// party can — and the field is named to keep the difference visible.
type ProcedureAttestation struct {
	// ProofHash pins the exact object examined. An attestation of
	// version 2 is not an attestation of version 3.
	ProofHash string
	// AttestorID is the examining party's legal identity.
	AttestorID string
	// AttestorRole is their standing — accredited assessor, instructed
	// expert, class society, auditor.
	AttestorRole string
	// Reference is the attestor's own identifier for their act.
	Reference string
	// ProcedureCorrectlyApplied is the substance: the attestor's
	// judgement that VERIQO's qualification procedure was correctly
	// applied to this evidence.
	ProcedureCorrectlyApplied bool
	// Reservations carries anything the attestor qualified their
	// statement with. An attestation with reservations is still an
	// attestation, and hiding the reservations would make it a lie.
	Reservations []string
	// AtTick is when the attestation was recorded.
	AtTick uint64
}

// Validate refuses an attestation that cannot be checked or that VERIQO
// could have written itself.
func (e ProcedureAttestation) Validate(interested ...string) error {
	if strings.TrimSpace(e.AttestorID) == "" || strings.TrimSpace(e.Reference) == "" {
		return ErrNoAttestor
	}
	if strings.TrimSpace(e.ProofHash) == "" {
		return ErrAttestationSubject
	}
	for _, p := range interested {
		if p = strings.TrimSpace(p); p != "" && strings.EqualFold(p, strings.TrimSpace(e.AttestorID)) {
			return fmt.Errorf("%w: %q", ErrAttestorIsVeriqo, e.AttestorID)
		}
	}
	return nil
}

// LevelOf reports the attestation level of a sealed Proof Object, given
// whatever external attestations exist for it.
//
// The level is derived, never stored on the object and never settable.
// That is the point: a field an author can write is a field an author
// can write wrongly, and this is the field that decides whether the word
// "proof" may be used.
func LevelOf(o Object, attestations []ProcedureAttestation, interested []string) AttestationLevel {
	if strings.TrimSpace(o.CanonicalHash) == "" {
		return LevelObjectCreated
	}
	if o.Stance != Support || o.Sufficiency != Sufficient {
		return LevelObjectCreated
	}
	for _, a := range attestations {
		if a.ProofHash != o.CanonicalHash || !a.ProcedureCorrectlyApplied {
			continue
		}
		if a.Validate(interested...) == nil {
			return LevelExternallyAttested
		}
	}
	return LevelQualified
}

// RaiseToExternallyAttested is the only way to Level 3, and it is
// deliberately hard.
//
// It requires a sealed, qualified object; an attestation that pins that
// object's hash; a named attestor with a reference who is not VERIQO or
// any party to the matter; and the attestor's positive statement that
// the procedure was correctly applied.
//
// No code path inside VERIQO calls this function. It exists so that the
// day a real assessor produces a real statement, there is a typed place
// to put it — and so that until that day, the level cannot be reached by
// accident.
func RaiseToExternallyAttested(o Object, a ProcedureAttestation, interested []string) (AttestationLevel, error) {
	if err := VerifyHash(o); err != nil {
		return LevelObjectCreated, err
	}
	if o.Stance != Support || o.Sufficiency != Sufficient {
		return LevelObjectCreated, fmt.Errorf("%w: the object is %s/%s", ErrNotQualified, o.Stance, o.Sufficiency)
	}
	if err := a.Validate(interested...); err != nil {
		return LevelQualified, err
	}
	if a.ProofHash != o.CanonicalHash {
		return LevelQualified, fmt.Errorf("%w: attestation covers %q", ErrAttestationSubject, a.ProofHash)
	}
	if !a.ProcedureCorrectlyApplied {
		return LevelQualified, fmt.Errorf("proof: attestor %q did not state the procedure was correctly applied", a.AttestorID)
	}
	return LevelExternallyAttested, nil
}

// DescribeLevel gives a report a sentence to quote instead of writing
// its own, which is how "qualified" becomes "proved" between a package
// and a slide.
func DescribeLevel(l AttestationLevel) string {
	switch l {
	case LevelExternallyAttested:
		return "Externally attested: an identified party who is not VERIQO examined the evidence and agreed the qualification procedure was correctly applied."
	case LevelQualified:
		return "Qualified by VERIQO: the evidence set satisfies VERIQO's own qualification rules. No party outside VERIQO has examined it, and this is not proof in the sense an outside reader would assume."
	default:
		return "A proof object exists. Its components were assembled and it sealed; nothing about the quality of its contents follows from that."
	}
}
