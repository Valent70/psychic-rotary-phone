package evidence

import "sort"

// This file answers the Round 5 work order's §3 "Real-World Evidence
// Qualification Layer" request.
//
// Before writing a single new status field, this file checked what
// already exists -- and found that most of the order's own required
// list is already real, assessed data in this package: Strength's nine
// independently-rated dimensions (evidence.go, §9) already carry
// Provenance (ProvenanceRating: VERIFIED/UNVERIFIED/UNKNOWN),
// IndependentCorroboration (CorroborationRating: HIGH/MEDIUM/LOW/NONE)
// and ContradictionLevel (ContradictionLevelRating: NONE/LOW/MEDIUM/
// HIGH); the prior round's Corroboration() already answers lifecycle
// corroboration (UNKNOWN/CORROBORATED/CONTRADICTED/SUPERSEDED/
// REVOKED). Building a FOURTH, independently-settable vocabulary
// answering the same underlying questions would be exactly the "one
// gap, two names" drift this codebase's own governance forbids --
// internal/assurance.CanonicalStatus exists specifically to prevent
// that pattern for readiness gates, and the same discipline applies
// here.
//
// What genuinely did NOT exist: the order's own eight-word vocabulary
// (UNKNOWN / SELF_ASSERTED / STRUCTURALLY_VALIDATED / SOURCE_VALIDATED
// / CORROBORATED / INDEPENDENTLY_CORROBORATED / DISPUTED / REJECTED) as
// a single, ready-to-report label a reviewer can read without knowing
// this package's own nine-dimension model. QualificationStatus below
// is a PURE TRANSLATION of the already-assessed Strength dimensions
// into that vocabulary -- never a new source of truth, never settable
// on its own, and it changes if and only if the underlying Strength
// (or Status) it reads from changes.

// QualificationStatus is the order's own eight-value provenance
// vocabulary, verbatim.
type QualificationStatus string

const (
	QualificationUnknown                   QualificationStatus = "UNKNOWN"
	QualificationSelfAsserted              QualificationStatus = "SELF_ASSERTED"
	QualificationStructurallyValidated     QualificationStatus = "STRUCTURALLY_VALIDATED"
	QualificationSourceValidated           QualificationStatus = "SOURCE_VALIDATED"
	QualificationCorroborated              QualificationStatus = "CORROBORATED"
	QualificationIndependentlyCorroborated QualificationStatus = "INDEPENDENTLY_CORROBORATED"
	QualificationDisputed                  QualificationStatus = "DISPUTED"
	QualificationRejected                  QualificationStatus = "REJECTED"
)

var knownQualificationStatuses = map[QualificationStatus]bool{
	QualificationUnknown: true, QualificationSelfAsserted: true, QualificationStructurallyValidated: true,
	QualificationSourceValidated: true, QualificationCorroborated: true, QualificationIndependentlyCorroborated: true,
	QualificationDisputed: true, QualificationRejected: true,
}

// IsKnownQualificationStatus reports whether s is one of the eight
// modelled values.
func IsKnownQualificationStatus(s QualificationStatus) bool { return knownQualificationStatuses[s] }

// KnownQualificationStatuses returns all eight values in deterministic
// order.
func KnownQualificationStatuses() []QualificationStatus {
	out := make([]QualificationStatus, 0, len(knownQualificationStatuses))
	for s := range knownQualificationStatuses {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// QualificationStatus translates r's ALREADY-ASSESSED Strength
// dimensions (and, for the two states Strength alone cannot express,
// r.Status) into the order's own vocabulary. It reads no field it does
// not already have; it sets nothing.
//
// Precedence, most authoritative first: an unassessed Strength
// (Strength.Validate returning ErrStrengthNotAssessed -- "not yet
// rated on any dimension") is UNKNOWN, since this package explicitly
// treats that zero value as "not yet assessed" rather than defaulting
// optimistic. Otherwise:
//
//  1. REJECTED    -- Authenticity is DISPUTED (the strongest signal
//     this package has that the record itself should not be relied
//     on), or r.Status recorded proven alteration.
//  2. DISPUTED    -- ContradictionLevel is anything above NONE, or
//     r.Status is CONTRADICTED/AUTHENTICITY_DISPUTED.
//  3. INDEPENDENTLY_CORROBORATED -- IndependentCorroboration is HIGH or
//     MEDIUM (multiple/strong independent sources agree).
//  4. CORROBORATED -- IndependentCorroboration is LOW (some corroborating
//     support exists, not yet strong).
//  5. SOURCE_VALIDATED -- Provenance dimension is VERIFIED.
//  6. STRUCTURALLY_VALIDATED -- Provenance dimension is UNVERIFIED (the
//     record passed ontology-level structural validation at
//     construction -- see the package doc note on Record.New -- but its
//     source has not itself been separately verified).
//  7. UNKNOWN -- Provenance dimension is UNKNOWN, or Strength was never
//     assessed at all.
//
// SELF_ASSERTED is part of the closed vocabulary (KnownQualificationStatuses
// includes it) but this derivation never produces it: nothing in this
// package's own assessed dimensions distinguishes "merely claimed" from
// "structurally validated" once a Record exists, because ontology.New
// already enforces structural validity before any Record is
// constructed (see the honesty note on Record.New). It remains
// reachable for an external caller describing evidence they have not
// yet run through that construction path.
func (r Record) QualificationStatus() QualificationStatus {
	if err := r.Strength.Validate(); err != nil {
		return QualificationUnknown
	}
	switch {
	case r.Strength.Authenticity == AuthenticityDisputed, r.Status == StatusAlterationDetected:
		return QualificationRejected
	case r.Strength.ContradictionLevel != ContradictionLevelNone,
		r.Status == StatusContradicted, r.Status == StatusAuthenticityDisputed:
		return QualificationDisputed
	case r.Strength.IndependentCorroboration == CorroborationHigh, r.Strength.IndependentCorroboration == CorroborationMedium:
		return QualificationIndependentlyCorroborated
	case r.Strength.IndependentCorroboration == CorroborationLow:
		return QualificationCorroborated
	case r.Strength.Provenance == ProvenanceVerified:
		return QualificationSourceValidated
	case r.Strength.Provenance == ProvenanceUnverified:
		return QualificationStructurallyValidated
	default: // r.Strength.Provenance == ProvenanceUnknown
		return QualificationUnknown
	}
}

// SourceIndependent reports whether r's own evidence is a ROOT in g --
// i.e. not itself derived from, or dependent on, any other evidence
// this case already holds. Reuses DependencyGraph.Root (dependency.go)
// rather than a second independence computation: "is this source
// independent" and "is this a dependency-graph root" are the same
// question this codebase already answers.
func (r Record) SourceIndependent(g *DependencyGraph) bool {
	if g == nil {
		return false
	}
	return g.Root(r.Underlying.EvidenceID)
}
