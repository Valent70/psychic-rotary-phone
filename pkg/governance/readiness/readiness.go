// Package readiness is external audit item A1's Evidence Qualification /
// Readiness Engine: a deterministic evaluator that classifies one piece
// of evidence against explicit acceptance criteria, producing
// READY/PARTIAL/BLOCKED/NOT_ELIGIBLE plus named, reproducible reason
// codes.
//
// This is NOT a second evidence-truth system: it does not decide
// whether a claim is true (that is pkg/moat/contradiction's job) and it
// does not verify a signature (that is pkg/governance/qualification's
// job, reused here as an input signal via Evidence.IntegrityVerified —
// see the doc comment on that field). It answers a narrower, upstream
// question: "is this evidence record complete and trustworthy enough to
// be admitted for evaluation at all." Do not use a readiness score as a
// substitute for verification (the audit's own explicit instruction):
// Evaluate is rule-based and deterministic — the same EvidenceRecord and
// Criteria always produce the same Verdict and the same set of Reasons,
// and there is no numeric "readiness score" output anywhere in this
// package.
package readiness

import (
	"sort"

	"veriqo/pkg/blockers/livedata"
)

// Verdict is the audit's required output vocabulary.
type Verdict string

const (
	VerdictReady      Verdict = "READY"
	VerdictPartial    Verdict = "PARTIAL"
	VerdictBlocked    Verdict = "BLOCKED"
	VerdictNotEligible Verdict = "NOT_ELIGIBLE"
)

// severity orders verdicts from best to worst so Evaluate can take the
// worst severity across every finding as the overall Verdict — a pure
// function of the findings, never a weighted score.
var severityRank = map[Verdict]int{
	VerdictReady:       0,
	VerdictPartial:     1,
	VerdictBlocked:     2,
	VerdictNotEligible: 3,
}

// ReasonCode is the audit's own named example list (A1's "Contoh:"),
// used verbatim.
type ReasonCode string

const (
	ReasonLegalAccessMissing       ReasonCode = "LEGAL_ACCESS_MISSING"
	ReasonProvenanceIncomplete     ReasonCode = "PROVENANCE_INCOMPLETE"
	ReasonHashMismatch             ReasonCode = "HASH_MISMATCH"
	ReasonSourceUntrusted          ReasonCode = "SOURCE_UNTRUSTED"
	ReasonAcceptanceCriterionUnmet ReasonCode = "ACCEPTANCE_CRITERION_UNMET"
	ReasonExternalEvidenceRequired ReasonCode = "EXTERNAL_EVIDENCE_REQUIRED"
)

// reasonSeverity is the fixed, documented mapping from a reason to the
// worst Verdict it can produce — the "deterministic rule table" this
// package's whole design rests on. Changing a mapping here is a
// reviewable, auditable, one-place change; Evaluate itself contains no
// verdict logic beyond "take the worst severity found".
var reasonSeverity = map[ReasonCode]Verdict{
	ReasonHashMismatch:             VerdictBlocked,
	ReasonSourceUntrusted:          VerdictBlocked,
	ReasonLegalAccessMissing:       VerdictNotEligible,
	ReasonExternalEvidenceRequired: VerdictNotEligible,
	ReasonProvenanceIncomplete:     VerdictPartial,
	ReasonAcceptanceCriterionUnmet: VerdictPartial,
}

// AccessStatus is the evidence's legal/technical access state.
type AccessStatus string

const (
	AccessGranted AccessStatus = "ACCESS_GRANTED"
	AccessPending AccessStatus = "ACCESS_PENDING"
	AccessDenied  AccessStatus = "ACCESS_DENIED"
	AccessUnknown AccessStatus = "ACCESS_UNKNOWN"
)

// LegalStatus is the evidence's licensing/authorization state.
type LegalStatus string

const (
	LegalLicensed          LegalStatus = "LICENSED"
	LegalCustomerAuthorized LegalStatus = "CUSTOMER_AUTHORIZED"
	LegalUnlicensed        LegalStatus = "UNLICENSED"
	LegalUnknown           LegalStatus = "UNKNOWN"
)

// ProvenanceCompleteness answers "does this evidence record carry its
// own provenance metadata" — a record-completeness check, deliberately
// distinct from pkg/moat/provenance's claim-independence computation and
// from pkg/rwc's CLAIMED/CORROBORATED/UNVERIFIED/CONTRADICTED (which
// answer "is the CLAIM true", not "is this RECORD's own paperwork
// complete").
type ProvenanceCompleteness string

const (
	ProvenanceComplete ProvenanceCompleteness = "COMPLETE"
	ProvenancePartial  ProvenanceCompleteness = "PARTIAL"
	ProvenanceMissing  ProvenanceCompleteness = "MISSING"
)

// IntegrityStatus answers "did this evidence's declared hash match its
// recomputed hash". A caller that already ran the hash check (e.g.
// pkg/blockers/livedata.Pipeline.Ingest, pkg/evidence/ontology.Evidence
// .ComputeID) should set this from that real result, never guess it.
type IntegrityStatus string

const (
	IntegrityVerified   IntegrityStatus = "VERIFIED"
	IntegrityMismatch   IntegrityStatus = "HASH_MISMATCH"
	IntegrityUnverified IntegrityStatus = "UNVERIFIED"
)

// EvidenceStrength is a qualitative, caller-asserted input tier — NOT
// the readiness verdict, and not a score this package computes: it is
// one input a caller (e.g. pkg/moat/contradiction's arbitration
// confidence, or pkg/moat/intelligence/risk's evidence weighting) may
// already have derived, fed in here as a fact about the evidence, the
// same way DataOrigin and Quality are.
type EvidenceStrength string

const (
	StrengthStrong   EvidenceStrength = "STRONG"
	StrengthModerate EvidenceStrength = "MODERATE"
	StrengthWeak     EvidenceStrength = "WEAK"
	StrengthUnknown  EvidenceStrength = "UNKNOWN"
)

// OperationalStatus is whether the evidence's source is currently
// operational (e.g. a feed connector that's live and reachable) at
// evaluation time.
type OperationalStatus string

const (
	OperationalOK       OperationalStatus = "OPERATIONAL"
	OperationalDegraded OperationalStatus = "DEGRADED"
	OperationalOffline  OperationalStatus = "OFFLINE"
)

// EvidenceRecord is the audit's required 12-field input, field names
// matching the brief's own list (source_id, source_type, data_origin,
// access_status, legal_status, quality, provenance, timestamp,
// integrity, evidence_strength, operational_status,
// blocker_dependencies) mapped onto typed Go fields.
type EvidenceRecord struct {
	SourceID   string
	SourceType string // e.g. "AIS", "PORT_CALL", "NOR", "SOF", "CARGO", "CLAIMS", "CUSTOMER_OPERATIONAL_EVIDENCE" — free text, not a closed enum, since A3's source taxonomy is intentionally open (see docs/AUDIT_A1_A18_GAP_MAPPING.md item A3)
	DataOrigin livedata.DataMode // reuses A2's enum directly — this package never redefines data-origin semantics
	AccessStatus AccessStatus
	LegalStatus  LegalStatus
	// Quality is a caller-supplied qualitative input signal (e.g. from an
	// upstream data-quality check), NOT the output of this package and
	// NOT a substitute for the deterministic rule evaluation below.
	Quality              EvidenceStrength
	Provenance           ProvenanceCompleteness
	HasTimestamp         bool
	Tick                 uint64
	Integrity            IntegrityStatus
	EvidenceStrength     EvidenceStrength
	OperationalStatus    OperationalStatus
	BlockerDependencies  []string // blocker IDs this evidence is submitted against/required by
	// SourceTrusted is whether SourceID appears in the caller's trust
	// registry (e.g. docs/governance/TRUSTED_EVIDENCE_PROVIDERS.json via
	// pkg/governance/qualification.TrustRegistry) — computed by the
	// caller, asserted here as a fact, exactly like Integrity.
	SourceTrusted bool
}

// Criteria is what EvidenceRecord is evaluated against — the acceptance
// policy for one blocker/evidence-requirement, supplied by the caller
// (e.g. read from docs/governance/production-blockers.json's own
// evidence_requirements/acceptance_criteria arrays).
type Criteria struct {
	RequireLegalAccess       bool
	RequireProvenanceComplete bool
	RequireIntegrityVerified bool
	RequireSourceTrusted     bool
	RequireExternalEvidence  bool
	// Named is an explicit list of named acceptance-criterion IDs the
	// caller asserts must each be satisfied (e.g. "zero_replay_acceptance"
	// from a blocker contract) — checked against Satisfied.
	Named []string
}

// Finding is one deterministic rule outcome — a ReasonCode plus the
// human-readable detail explaining exactly which input triggered it, so
// a caller/auditor never has to reverse-engineer why a Verdict was
// reached.
type Finding struct {
	Reason ReasonCode
	Detail string
}

// Result is Evaluate's full, auditable output.
type Result struct {
	Verdict  Verdict
	Findings []Finding
}

// HasReason reports whether Result contains a specific reason code.
func (r Result) HasReason(rc ReasonCode) bool {
	for _, f := range r.Findings {
		if f.Reason == rc {
			return true
		}
	}
	return false
}

// Evaluate deterministically classifies rec against criteria. satisfied
// is the set of Criteria.Named entries the caller has already verified
// as met (e.g. from a real test run's pass/fail) — Evaluate never
// invents whether a named criterion passed; it only checks whether every
// criterion the policy names appears in this set.
//
// Every branch below maps directly to one line of docs/AUDIT_A1_A18_GAP_MAPPING.md's
// A1 entry and to reasonSeverity's fixed table — there is no scoring,
// weighting, or threshold anywhere in this function.
func Evaluate(rec EvidenceRecord, criteria Criteria, satisfied map[string]bool) Result {
	var findings []Finding

	if rec.Integrity == IntegrityMismatch {
		findings = append(findings, Finding{ReasonHashMismatch,
			"evidence's declared hash did not match its recomputed hash"})
	}

	if criteria.RequireSourceTrusted && !rec.SourceTrusted {
		findings = append(findings, Finding{ReasonSourceUntrusted,
			"source_id \"" + rec.SourceID + "\" is not present in the trust registry"})
	}

	if criteria.RequireLegalAccess {
		if rec.AccessStatus != AccessGranted ||
			(rec.LegalStatus != LegalLicensed && rec.LegalStatus != LegalCustomerAuthorized) {
			findings = append(findings, Finding{ReasonLegalAccessMissing,
				"access_status=" + string(rec.AccessStatus) + " legal_status=" + string(rec.LegalStatus)})
		}
	}

	if criteria.RequireExternalEvidence && !livedata.IsIndependentRealObservation(rec.DataOrigin) {
		findings = append(findings, Finding{ReasonExternalEvidenceRequired,
			"criteria requires an independent real-world observation but data_origin=" + string(rec.DataOrigin)})
	}

	if criteria.RequireProvenanceComplete && rec.Provenance != ProvenanceComplete {
		findings = append(findings, Finding{ReasonProvenanceIncomplete,
			"provenance=" + string(rec.Provenance)})
	}

	var unmet []string
	for _, name := range criteria.Named {
		if !satisfied[name] {
			unmet = append(unmet, name)
		}
	}
	if len(unmet) > 0 {
		sort.Strings(unmet) // deterministic ordering regardless of map iteration
		detail := "unmet: "
		for i, n := range unmet {
			if i > 0 {
				detail += ", "
			}
			detail += n
		}
		findings = append(findings, Finding{ReasonAcceptanceCriterionUnmet, detail})
	}

	return Result{Verdict: worstVerdict(findings), Findings: findings}
}

func worstVerdict(findings []Finding) Verdict {
	verdict := VerdictReady
	for _, f := range findings {
		if v := reasonSeverity[f.Reason]; severityRank[v] > severityRank[verdict] {
			verdict = v
		}
	}
	return verdict
}
