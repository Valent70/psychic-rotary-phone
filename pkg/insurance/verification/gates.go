package verification

import (
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/dossier"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/preservation"
	"veriqo/pkg/insurance/quantum"
)

// This file implements the four insurance-specific verification gates
// the functional spec names in §54–§57:
//
//	§54 Coverage Traceability Gate
//	§55 Quantum Reproducibility Gate
//	§56 Preservation Gate
//	§57 Human Review Gate
//
// Every one follows the same discipline the rest of this repository's
// assurance plane uses, and the reason is stated once here rather than
// repeated four times:
//
//   - A gate's verdict is DERIVED from a list of named failures. Every
//     report type below has `Pass() bool { return len(Failures) == 0 }`
//     and NO settable pass/status field. There is no way to write PASS
//     into any of them by hand.
//   - A failure names the specific artifact that failed, not a count.
//     "3 facts untraceable" is not actionable; "fact
//     required_evidence:survey_report cites no evidence and no clause"
//     is.
//   - Nothing here re-implements an analysis. Each gate CHECKS the
//     output another package already produced.
//
// The existing Manifest (verification.go) is untouched and is reused as
// the evidence-set integrity primitive these gates sit alongside.

// ---- §54 Coverage Traceability Gate ---------------------------------

// CoverageTraceabilityReport is the derived answer to the spec §54
// requirement: every coverage conclusion must lead back to a policy
// clause, to evidence, to an effective date, and to a reasoning trace.
type CoverageTraceabilityReport struct {
	CaseID          string `json:"case_id"`
	ClaimID         string `json:"claim_id"`
	PolicyVersionID string `json:"policy_version_id"`

	FactCount int `json:"fact_count"`
	// TraceableFacts counts the facts that satisfy every §54 link.
	TraceableFacts int `json:"traceable_facts"`
	// EffectiveDateBound reports whether the analysis is pinned to a
	// policy version with a real effective window — the §54 "effective
	// date" link.
	EffectiveDateBound bool `json:"effective_date_bound"`
	// ReviewRequiredWhereUnresolved reports whether every unresolved
	// finding actually raised review, rather than being quietly dropped.
	ReviewRequiredWhereUnresolved bool `json:"review_required_where_unresolved"`

	Failures []string `json:"failures,omitempty"`
}

// Pass is derived from Failures. There is no settable verdict field.
func (r CoverageTraceabilityReport) Pass() bool { return len(r.Failures) == 0 }

// VerifyCoverageTraceability checks one coverage analysis against §54.
//
// The version argument is the policy.Version the analysis was performed
// against — the one policy.History.EffectiveAt resolved for the
// incident, which is what makes the "effective date" link real rather
// than nominal. reg supplies the case's evidence so cited EvidenceIDs
// can be confirmed to exist rather than merely to be non-empty.
//
// A fact is traceable when:
//
//   - it is pinned to the same PolicyVersionID as the analysis; AND
//   - it carries a reason a reader can follow (a description); AND
//   - if its status is SUPPORTED or DISPUTED — i.e. it asserts
//     something — it cites at least one clause or at least one piece of
//     evidence that actually exists in the registry.
//
// A fact whose status is INSUFFICIENT_EVIDENCE is NOT required to cite
// evidence: "we could not establish this" is precisely a statement that
// there is no evidence, and demanding a citation would push a caller to
// invent one.
func VerifyCoverageTraceability(a coverage.CoverageAnalysis, version policy.Version, reg *insevidence.Registry) CoverageTraceabilityReport {
	r := CoverageTraceabilityReport{
		CaseID:          a.CaseID,
		ClaimID:         a.ClaimID,
		PolicyVersionID: a.PolicyVersionID,
		FactCount:       len(a.Facts),
	}

	if a.PolicyVersionID == "" {
		r.Failures = append(r.Failures,
			"the coverage analysis is not pinned to any policy version")
	} else if version.VersionID != "" && version.VersionID != a.PolicyVersionID {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"the analysis claims policy version %s but was verified against %s",
			a.PolicyVersionID, version.VersionID))
	}

	// §54's "effective date" link: the version must carry a real
	// effective window, not merely an ID.
	r.EffectiveDateBound = version.VersionID != "" && (version.EffectiveFrom != 0 || version.EffectiveTo != 0)
	if !r.EffectiveDateBound {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"policy version %s carries no effective window, so no coverage conclusion can be bound to an effective date",
			version.VersionID))
	}

	knownClauses := map[string]bool{}
	for _, c := range version.Clauses {
		knownClauses[c.ClauseID] = true
	}

	if len(a.Facts) == 0 {
		r.Failures = append(r.Failures, "the coverage analysis contains no facts at all")
	}

	for _, f := range a.Facts {
		traceable := true
		if f.PolicyVersionID != a.PolicyVersionID {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"fact %q is pinned to policy version %q, not the analysis's %q",
				f.FactID, f.PolicyVersionID, a.PolicyVersionID))
			traceable = false
		}
		if strings.TrimSpace(f.Description) == "" {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"fact %q carries no description, so its conclusion has no reasoning a reader can follow", f.FactID))
			traceable = false
		}
		// Clause citations must resolve to real clauses on the version.
		for _, cid := range f.ClauseIDs {
			if len(version.Clauses) > 0 && !knownClauses[cid] {
				r.Failures = append(r.Failures, fmt.Sprintf(
					"fact %q cites clause %q, which does not exist on policy version %s",
					f.FactID, cid, version.VersionID))
				traceable = false
			}
		}
		// Evidence citations must resolve to real records.
		for _, eid := range f.EvidenceIDs {
			if reg != nil {
				if _, ok := reg.Get(eid); !ok {
					r.Failures = append(r.Failures, fmt.Sprintf(
						"fact %q cites evidence %s, which is not in this case's evidence registry", f.FactID, eid))
					traceable = false
				}
			}
		}
		// An asserting fact must rest on something.
		if f.Status == coverage.StatusSupported || f.Status == coverage.StatusDisputed {
			if len(f.ClauseIDs) == 0 && len(f.EvidenceIDs) == 0 {
				r.Failures = append(r.Failures, fmt.Sprintf(
					"fact %q asserts %s but cites neither a policy clause nor any evidence",
					f.FactID, f.Status))
				traceable = false
			}
		}
		if traceable {
			r.TraceableFacts++
		}
	}

	// §54's last link: an unresolved finding must actually raise review.
	unresolved := false
	for _, f := range a.Facts {
		if f.Status != coverage.StatusSupported {
			unresolved = true
		}
	}
	if len(a.Conflicts) > 0 || len(a.Questions) > 0 {
		unresolved = true
	}
	r.ReviewRequiredWhereUnresolved = !unresolved || a.ReviewRequired
	if !r.ReviewRequiredWhereUnresolved {
		r.Failures = append(r.Failures,
			"the analysis holds unresolved findings but does not require review — an unresolved coverage "+
				"question that raises no review is a conclusion reached by omission")
	}

	return r
}

// ---- §55 Quantum Reproducibility Gate --------------------------------

// QuantumReproducibilityReport is the derived answer to §55: the same
// inputs, at the same effective tick, under the same calculation
// version, must produce the same quantum result.
type QuantumReproducibilityReport struct {
	CalculationID      string `json:"calculation_id"`
	CalculationVersion string `json:"calculation_version"`

	// Recomputed reports whether a fresh Compute over the recorded
	// inputs was possible at all.
	Recomputed bool `json:"recomputed"`
	// Identical reports whether the recomputation matched the recorded
	// result in every arithmetic field.
	Identical bool `json:"identical"`
	// EveryAmountEvidenceBacked reports whether every non-zero input
	// amount cites at least one piece of evidence — §55 is meaningless
	// over numbers that came from nowhere.
	EveryAmountEvidenceBacked bool `json:"every_amount_evidence_backed"`
	// VersionDeclared reports whether the calculation states which
	// version of the formula produced it.
	VersionDeclared bool `json:"version_declared"`

	RecordedIndicativeValue   string `json:"recorded_indicative_value"`
	RecomputedIndicativeValue string `json:"recomputed_indicative_value"`

	Failures []string `json:"failures,omitempty"`
}

// Pass is derived from Failures.
func (r QuantumReproducibilityReport) Pass() bool { return len(r.Failures) == 0 }

// VerifyQuantumReproducibility re-runs quantum.Compute over the SAME
// inputs the recorded calculation was produced from and compares every
// arithmetic field.
//
// This is a genuine recomputation, not a hash comparison: it proves the
// formula is deterministic over these inputs, which is what §55 asks.
// (Determinism across processes follows from quantum's own int64
// minor-unit representation, whose package doc explains why float64 was
// rejected for exactly this requirement.)
func VerifyQuantumReproducibility(recorded quantum.Calculation, in quantum.ComputeInput) QuantumReproducibilityReport {
	r := QuantumReproducibilityReport{
		CalculationID:           recorded.CalculationID,
		CalculationVersion:      recorded.CalculationVersion,
		RecordedIndicativeValue: recorded.IndicativeClaimValue.String(),
	}

	r.VersionDeclared = strings.TrimSpace(recorded.CalculationVersion) != ""
	if !r.VersionDeclared {
		r.Failures = append(r.Failures,
			"the calculation declares no calculation_version, so a future change to the formula could not "+
				"be distinguished from a change in the inputs")
	}

	// Every non-zero input must be evidence-backed. Iterated as an
	// ordered slice, not a map: this report is content-hashed into a
	// readiness evidence artifact, and a gate artifact whose bytes vary
	// between identical runs is not evidence.
	r.EveryAmountEvidenceBacked = true
	for _, operand := range []struct {
		name string
		amt  quantum.EvidenceBackedAmount
	}{
		{"gross_loss", in.GrossLoss},
		{"mitigation", in.Mitigation},
		{"salvage", in.Salvage},
		{"deductible", in.Deductible},
	} {
		if operand.amt.Amount != 0 && len(operand.amt.EvidenceIDs) == 0 {
			r.EveryAmountEvidenceBacked = false
			r.Failures = append(r.Failures, fmt.Sprintf(
				"input %s is %s but cites no evidence — a quantum figure that came from nowhere cannot be reproduced from evidence",
				operand.name, operand.amt.Amount))
		}
	}

	again, err := quantum.Compute(in)
	if err != nil {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"recomputing the calculation from its recorded inputs failed: %v", err))
		return r
	}
	r.Recomputed = true
	r.RecomputedIndicativeValue = again.IndicativeClaimValue.String()

	mismatches := []string{}
	if again.IndicativeClaimValue != recorded.IndicativeClaimValue {
		mismatches = append(mismatches, fmt.Sprintf("indicative_claim_value recorded=%s recomputed=%s",
			recorded.IndicativeClaimValue, again.IndicativeClaimValue))
	}
	// Every operand is compared by its own Amount rather than by struct
	// equality: EvidenceBackedAmount carries a []string of evidence IDs
	// and is therefore not comparable, and the arithmetic claim §55
	// makes is about the FIGURES.
	for name, pair := range map[string][2]quantum.EvidenceBackedAmount{
		"gross_loss": {recorded.GrossLoss, again.GrossLoss},
		"mitigation": {recorded.Mitigation, again.Mitigation},
		"salvage":    {recorded.Salvage, again.Salvage},
		"deductible": {recorded.Deductible, again.Deductible},
	} {
		if pair[0].Amount != pair[1].Amount {
			mismatches = append(mismatches, fmt.Sprintf("%s recorded=%s recomputed=%s",
				name, pair[0].Amount, pair[1].Amount))
		}
	}
	if again.Currency != recorded.Currency {
		mismatches = append(mismatches, fmt.Sprintf("currency recorded=%s recomputed=%s",
			recorded.Currency, again.Currency))
	}
	if again.CalculationVersion != recorded.CalculationVersion {
		mismatches = append(mismatches, fmt.Sprintf("calculation_version recorded=%s recomputed=%s",
			recorded.CalculationVersion, again.CalculationVersion))
	}
	if again.Formula != recorded.Formula {
		mismatches = append(mismatches, fmt.Sprintf("formula recorded=%q recomputed=%q",
			recorded.Formula, again.Formula))
	}

	r.Identical = len(mismatches) == 0
	// Sorted before emission: the operand comparison above iterates a
	// map, and this report is content-hashed into a readiness evidence
	// artifact. A gate artifact whose bytes vary between identical runs
	// is not evidence.
	sort.Strings(mismatches)
	for _, m := range mismatches {
		r.Failures = append(r.Failures, "quantum recomputation diverged: "+m)
	}
	return r
}

// ---- §56 Preservation Gate -------------------------------------------

// PreservationReport is the derived answer to §56. It wraps
// preservation.ChainReport rather than restating its nine checks — the
// preservation package owns them, and this gate's job is to run them
// over a case's orders and aggregate honestly.
type PreservationReport struct {
	CaseID     string `json:"case_id"`
	OrderCount int    `json:"order_count"`
	// Orders holds each order's own chain report.
	Orders []preservation.ChainReport `json:"orders"`
	// EvidenceCoverage reports how many of the case's evidence records
	// are covered by at least one preservation order.
	EvidenceInCase    int `json:"evidence_in_case"`
	EvidencePreserved int `json:"evidence_preserved"`

	Failures []string `json:"failures,omitempty"`
}

// Pass is derived from Failures.
func (r PreservationReport) Pass() bool { return len(r.Failures) == 0 }

// VerifyPreservation runs every order's own §56 chain check and, in
// addition, reports whether the case's evidence is actually covered.
//
// anchoredHashes may name the hash each order was last anchored at (by
// PreservationID); an order with no entry is verified structurally but
// not against a prior hash, and the report says so by leaving
// ChainVerified true — because "never anchored" is a different fact from
// "anchored and changed", and conflating them would manufacture a
// failure.
func VerifyPreservation(caseID string, orders []*preservation.Order, reg *insevidence.Registry, anchoredHashes map[string]string) PreservationReport {
	r := PreservationReport{CaseID: caseID, OrderCount: len(orders)}

	if len(orders) == 0 {
		r.Failures = append(r.Failures,
			"no preservation order exists for this case — evidence held with no recorded trigger, scope or "+
				"custodian is not preserved evidence")
	}

	covered := map[string]bool{}
	for _, o := range orders {
		if o == nil {
			r.Failures = append(r.Failures, "a nil preservation order was supplied")
			continue
		}
		if o.CaseID != caseID {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"preservation order %s belongs to case %s, not %s", o.PreservationID, o.CaseID, caseID))
			continue
		}
		cr := o.Verify(anchoredHashes[o.PreservationID])
		r.Orders = append(r.Orders, cr)
		for _, f := range cr.Failures {
			r.Failures = append(r.Failures, fmt.Sprintf("order %s: %s", o.PreservationID, f))
		}
		for _, id := range o.PreservedIDs() {
			covered[id] = true
		}
	}

	if reg != nil {
		all := reg.All()
		for _, rec := range all {
			if rec.CaseID != caseID {
				continue
			}
			r.EvidenceInCase++
			if covered[rec.EvidenceID()] {
				r.EvidencePreserved++
			}
		}
		var uncovered []string
		for _, rec := range all {
			if rec.CaseID == caseID && !covered[rec.EvidenceID()] {
				uncovered = append(uncovered, rec.EvidenceID())
			}
		}
		sort.Strings(uncovered)
		for _, id := range uncovered {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"evidence %s is in the case but is covered by no preservation order", id))
		}
	}

	return r
}

// ---- §57 Human Review Gate -------------------------------------------

// ReviewAuthorization is a recorded human authorization. It exists so
// this gate can check for one WITHOUT this package inventing a second
// human-authority model: the canonical one is pkg/governance/hitl, and
// the fields below are exactly what a caller reads off a
// hitl.GovernedOutcome. Nothing here creates an authorization.
type ReviewAuthorization struct {
	// ReviewerID is who authorized. Mandatory.
	ReviewerID string `json:"reviewer_id"`
	// CaseRef is the canonical review case this came from — a
	// hitl.Case's CaseID — so a reader can go and check it.
	CaseRef string `json:"case_ref"`
	// Rationale is why, in the reviewer's own words. Mandatory: an
	// authorization with no reasoning is a rubber stamp.
	Rationale string `json:"rationale"`
	// AuthorizedTick is when.
	AuthorizedTick uint64 `json:"authorized_tick"`
	// AddressedQuestions names which of the dossier's own review
	// questions this authorization actually addressed.
	AddressedQuestions []string `json:"addressed_questions,omitempty"`
}

// HumanReviewReport is the derived answer to §57: the system must
// prevent automated finalization when mandatory review is outstanding.
type HumanReviewReport struct {
	CaseID  string `json:"case_id"`
	ClaimID string `json:"claim_id,omitempty"`

	ReviewRequired bool `json:"review_required"`
	// OutstandingQuestions are the dossier's own review questions that
	// no recorded authorization addressed.
	OutstandingQuestions []string `json:"outstanding_questions,omitempty"`
	// AuthorizationCount is how many valid authorizations were supplied.
	AuthorizationCount int `json:"authorization_count"`
	// FinalizationPermitted is the gate's actual answer. Derived.
	FinalizationPermitted bool `json:"finalization_permitted"`

	Failures []string `json:"failures,omitempty"`
}

// Pass is derived from Failures.
func (r HumanReviewReport) Pass() bool { return len(r.Failures) == 0 }

// VerifyHumanReview implements the §57 fail-closed rule: a dossier that
// requires human review may not be finalized until a real authorization
// addressing each outstanding question exists.
//
// Note what this function does NOT do. It does not decide whether review
// was required — dossier.Generate already computed that by aggregating
// other packages' own flags, and re-deriving it here would be a second
// opinion. It checks only that the required authorizations exist and are
// themselves well-formed.
func VerifyHumanReview(d *dossier.Dossier, auths []ReviewAuthorization) HumanReviewReport {
	r := HumanReviewReport{}
	if d == nil {
		r.Failures = append(r.Failures, "no dossier was supplied, so no finalization can be authorized")
		return r
	}
	r.CaseID = d.CaseID
	r.ClaimID = d.ClaimID
	r.ReviewRequired = d.HumanReviewRequired

	addressed := map[string]bool{}
	for i, a := range auths {
		wellFormed := true
		if strings.TrimSpace(a.ReviewerID) == "" {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"authorization %d names no reviewer", i))
			wellFormed = false
		}
		if strings.TrimSpace(a.Rationale) == "" {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"authorization %d by %q records no rationale — an authorization with no reasoning is a rubber stamp",
				i, a.ReviewerID))
			wellFormed = false
		}
		if strings.TrimSpace(a.CaseRef) == "" {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"authorization %d by %q cites no canonical review case, so it cannot be independently checked",
				i, a.ReviewerID))
			wellFormed = false
		}
		if !wellFormed {
			continue
		}
		r.AuthorizationCount++
		for _, q := range a.AddressedQuestions {
			addressed[q] = true
		}
	}

	if r.ReviewRequired {
		for _, q := range d.HumanReviewQuestions {
			if !addressed[q] {
				r.OutstandingQuestions = append(r.OutstandingQuestions, q)
			}
		}
		if r.AuthorizationCount == 0 {
			r.Failures = append(r.Failures,
				"this dossier requires human review and no valid authorization exists — finalization must fail closed")
		}
		for _, q := range r.OutstandingQuestions {
			r.Failures = append(r.Failures, "no authorization addresses the review question: "+q)
		}
	}

	// FinalizationPermitted is derived, never set: it is true only when
	// review was not required, or every required question was addressed
	// by a well-formed authorization.
	r.FinalizationPermitted = !r.ReviewRequired || (r.AuthorizationCount > 0 && len(r.OutstandingQuestions) == 0)
	return r
}

// ---- Aggregate -------------------------------------------------------

// GateReport aggregates all four §54–§57 gates for one case, so a
// caller (or the readiness command) gets one object per case rather than
// four.
type GateReport struct {
	CaseID string `json:"case_id"`

	CoverageTraceability   CoverageTraceabilityReport   `json:"coverage_traceability"`
	QuantumReproducibility QuantumReproducibilityReport `json:"quantum_reproducibility"`
	Preservation           PreservationReport           `json:"preservation"`
	HumanReview            HumanReviewReport            `json:"human_review"`

	// EvidenceManifest is the existing evidence-root manifest, carried
	// alongside so a report is self-contained for an auditor.
	EvidenceManifest Manifest `json:"evidence_manifest"`
}

// Pass is derived: every one of the four gates must pass. Not three of
// four, not a weighted score — the same rule internal/assurance applies
// to release gates, for the same reason.
func (r GateReport) Pass() bool {
	return r.CoverageTraceability.Pass() &&
		r.QuantumReproducibility.Pass() &&
		r.Preservation.Pass() &&
		r.HumanReview.Pass()
}

// Failures returns every failure from every gate, each prefixed with the
// gate it came from.
func (r GateReport) Failures() []string {
	var out []string
	for _, f := range r.CoverageTraceability.Failures {
		out = append(out, "coverage_traceability: "+f)
	}
	for _, f := range r.QuantumReproducibility.Failures {
		out = append(out, "quantum_reproducibility: "+f)
	}
	for _, f := range r.Preservation.Failures {
		out = append(out, "preservation: "+f)
	}
	for _, f := range r.HumanReview.Failures {
		out = append(out, "human_review: "+f)
	}
	return out
}
