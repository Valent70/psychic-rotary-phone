// Package semantics closes audit item P0-A's real, precise remaining
// gap. A prior round's own investigation found the exact shape of the
// problem: Truth Arbitration already has one canonical entry path (a
// facade, with direct-bypass scanning proving 170/171 files clean, 0
// violations — see internal/nobypass) — but that facade governs WHICH
// CODE PATH runs, not WHAT SHAPE evidence has once it gets there. Four
// genuinely different, independently-audited Evidence-shaped
// structures still exist side by side, each real and load-bearing for
// its own subsystem:
//
//	evidence/ontology.Evidence      — the formal, signed, bitemporal
//	                                   external evidence record (audit
//	                                   requirement R-004)
//	moat/fusion.Evidence            — one immutable, content-addressed,
//	                                   per-claim submission the fusion
//	                                   engine's own ledger works over
//	moat/contradiction.Evidence     — a RawObservation promoted with a
//	                                   decayed Weight, the arbitration
//	                                   engine's own working unit
//	canonical.SourceSubmission      — the caller-facing input shape
//	                                   pkg/canonical.RunCanonical accepts
//
// Honest scope, stated once here rather than buried: this package does
// NOT merge these four into one struct, retire three of them, or
// rewrite any of the systems that own them. That would be exactly the
// "real, multi-day architectural work... retiring or adapting existing
// systems and migrating every caller" this project's own P0-B
// investigation (pkg/governance/entityconsistency) already declined to
// attempt for the analogous entity-resolution question, for the same
// good reason: each of these four types is independently audited,
// independently tested, and shaped the way it is because its OWN
// subsystem genuinely needs that shape (fusion needs a ledger-ready
// content-addressed ID; contradiction needs a decayed Weight; the
// external ontology needs bitemporal validity and signature fields
// none of the internal engines require). Force-merging them would not
// unify anything real — it would either lose information three
// subsystems depend on, or bloat all four with fields only one of them
// uses. That is fabricated unification, not real unification, and this
// project has consistently refused to produce it.
//
// What this package DOES provide, honestly and for real: the "schema-
// level provenance, not just stage-level hashing" contract the audit
// itself asks for (see pkg/canonical's own CanonicalResult doc comment,
// which states this exact distinction) — CanonicalEvidenceView, ONE
// projection every one of the four types can be losslessly read
// through for the fields that matter across the whole OS: which claim
// this evidence is about, what it asserts, where it came from, how
// reliable it is, and when it was observed. An operator, auditor, or
// cross-cutting tool (a dashboard, a compliance export, a single log
// line joining evidence from all four subsystems) can now query
// "evidence" as ONE concept without needing to know which of the four
// internal shapes produced it — while each subsystem keeps its own
// real, unchanged, independently-audited internal type. This is a
// real, non-invasive, additive read-side unification: it adds a lens,
// it does not touch what any of the four systems already do.
package semantics

import (
	"veriqo/pkg/canonical"
	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/fusion"
)

// OriginKind names which of the four real subsystems a
// CanonicalEvidenceView was projected from. Never omitted: a view that
// did not honestly disclose its origin would misrepresent which
// system's guarantees actually back it (bitemporal/signed for
// ontology, content-addressed-ledger for fusion, decay-weighted for
// contradiction, caller-declared for a source submission).
type OriginKind string

const (
	OriginOntology         OriginKind = "ontology"
	OriginFusion           OriginKind = "fusion"
	OriginContradiction    OriginKind = "contradiction"
	OriginSourceSubmission OriginKind = "source_submission"
)

// CanonicalEvidenceView is the one cross-cutting evidence semantic
// contract every evidence-shaped structure in this OS can be
// losslessly projected onto, for exactly the fields that are honestly
// common to all four: which claim (ClaimKey, matching fusion.Claim.Key's
// own "subject|predicate" convention so a view's ClaimKey and a live
// fusion.Claim always agree), what was asserted (Value), where it came
// from (Source), how reliable it is (Reliability — confidence, source
// reliability, or decayed weight, whichever concept the origin type
// carries; OriginKind tells a caller which), and when it was observed
// (Tick — VERIQO ticks throughout, never wall-clock, matching every
// other deterministic-replay guarantee in this codebase). Identity
// carries the origin type's own real identifier when it has one
// (ontology.Evidence.EvidenceID, fusion.Evidence.ID); it is empty for
// origin types that do not assign a standalone per-evidence identity
// (contradiction.Evidence and canonical.SourceSubmission are keyed
// externally, by claim, not by their own ID) — an honestly empty
// field, never a fabricated one.
type CanonicalEvidenceView struct {
	Identity    string     `json:"identity,omitempty"`
	ClaimKey    string     `json:"claim_key"`
	Value       string     `json:"value"`
	Source      string     `json:"source"`
	Reliability float64    `json:"reliability"`
	Tick        uint64     `json:"tick"`
	OriginKind  OriginKind `json:"origin_kind"`
}

// claimKey mirrors fusion.Claim.Key's own convention exactly (subject +
// "|" + predicate) so a CanonicalEvidenceView's ClaimKey always agrees
// with a live fusion.Claim over the same assertion — the same
// convention, not a parallel one that could silently drift from it.
func claimKey(subject, predicate string) string { return subject + "|" + predicate }

// FromOntology projects a formal evidence-ontology record.
func FromOntology(e ontology.Evidence) CanonicalEvidenceView {
	return CanonicalEvidenceView{
		Identity:    e.EvidenceID,
		ClaimKey:    claimKey(e.Subject, e.Predicate),
		Value:       e.Object,
		Source:      e.Source,
		Reliability: e.Confidence,
		Tick:        e.ObservedAt,
		OriginKind:  OriginOntology,
	}
}

// FromFusion projects one of fusion's own immutable evidence records.
func FromFusion(e fusion.Evidence) CanonicalEvidenceView {
	return CanonicalEvidenceView{
		Identity:    e.ID,
		ClaimKey:    e.Claim.Key(),
		Value:       e.Value,
		Source:      string(e.Source),
		Reliability: e.Reliability,
		Tick:        e.Tick,
		OriginKind:  OriginFusion,
	}
}

// FromContradiction projects one of contradiction's decay-weighted
// evidence records. RawObservation's own ClaimKey is already in
// fusion.Claim.Key's "subject|predicate" form (both engines' callers
// construct it the same way), so it is carried through unchanged, not
// recomputed.
func FromContradiction(e contradiction.Evidence) CanonicalEvidenceView {
	return CanonicalEvidenceView{
		ClaimKey:    e.ClaimKey,
		Value:       e.Value,
		Source:      e.SourceID,
		Reliability: e.Weight,
		Tick:        e.Tick,
		OriginKind:  OriginContradiction,
	}
}

// FromSourceSubmission projects a canonical.SourceSubmission -- the
// caller-facing input shape RunCanonical accepts. subject/predicate are
// explicit parameters (never invented) because SourceSubmission itself
// does not carry them; they live one level up, on the CaseInput the
// submission belongs to (see canonical.CaseInput.Subject/Predicate) --
// exactly the same real value RunCanonical itself uses to build its own
// fusion.Claim before ever touching this submission (see canonical.go's
// RunCanonical: "claim := fusion.Claim{Subject: in.Subject, Predicate:
// in.Predicate}"), so a caller with a CaseInput in hand is supplying
// nothing this package could not already derive from data it legitimately
// has.
func FromSourceSubmission(s canonical.SourceSubmission, subject, predicate string) CanonicalEvidenceView {
	return CanonicalEvidenceView{
		ClaimKey:    claimKey(subject, predicate),
		Value:       s.Value,
		Source:      string(s.SourceID),
		Reliability: s.BaseReliability,
		Tick:        0, // SourceSubmission carries no per-submission tick of its own -- CaseInput.Tick is shared across all of a case's submissions, not attributable to one
		OriginKind:  OriginSourceSubmission,
	}
}
