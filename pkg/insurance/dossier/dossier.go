// Package dossier assembles VICE's terminal work product for one
// case — blueprint §30. Generate performs NO analysis of its own: it
// only aggregates what every other pkg/insurance package has already
// computed, exactly the way the blueprint's own §1 mandate demands
// ("Jangan duplikasi logic yang sudah ada"). A Dossier is never a
// coverage or liability verdict — see TestDossierHasNoVerdictField —
// and Section 18/19 (human-review questions, audit trail) are real
// computed aggregations, never placeholders.
package dossier

import (
	"errors"

	insurancecase "veriqo/pkg/insurance/case"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/contradiction"
	"veriqo/pkg/insurance/coverage"
	"veriqo/pkg/insurance/deadline"
	"veriqo/pkg/insurance/gap"
	"veriqo/pkg/insurance/mitigation"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/recovery"
	"veriqo/pkg/insurance/timeline"
)

var ErrNilCase = errors.New("dossier: case must not be nil")

// Inputs aggregates every other package's already-computed output for
// one case. All fields are optional (a case may not yet have reached
// every analysis stage); Generate omits whatever is nil.
type Inputs struct {
	ClaimID string

	TimelineConflicts    []timeline.Conflict
	Contradictions       []contradiction.ContradictionRecord
	GapAssessment        *gap.Assessment
	CausationExplanation *causation.Explanation
	QuantumCalculation   *quantum.Calculation
	QuantumDiscrepancy   *quantum.QuantumDiscrepancy
	MitigationImpact     *mitigation.Impact
	RecoveryTargets      []recovery.Target
	Deadlines            []deadline.Deadline
	CoverageAnalysis     *coverage.CoverageAnalysis
}

// AuditEntry is one lifecycle-state transition, read verbatim from
// the case's own StateLog — Section 19 of the dossier.
type AuditEntry struct {
	From string `json:"from"`
	To   string `json:"to"`
	Tick uint64 `json:"tick"`
}

// Dossier is VICE's terminal, per-case work product. Every field here
// is either raw traceable evidence (audit trail), or a real other
// package's already-computed, already-hedged output — there is no
// "final_decision", "approved_amount", "verdict", or similarly named
// field anywhere in this type (see TestDossierHasNoVerdictField, which
// checks this structurally rather than by convention).
type Dossier struct {
	CaseID  string `json:"case_id"`
	ClaimID string `json:"claim_id,omitempty"`

	// Section 19: Audit Trail.
	LifecycleAuditTrail []AuditEntry `json:"lifecycle_audit_trail"`

	TimelineConflicts       []timeline.Conflict                 `json:"timeline_conflicts,omitempty"`
	Contradictions          []contradiction.ContradictionRecord `json:"contradictions,omitempty"`
	EvidenceSufficiency     []gap.Sufficiency                   `json:"evidence_sufficiency,omitempty"`
	CausationExplanation    *causation.Explanation              `json:"causation_explanation,omitempty"`
	QuantumCalculation      *quantum.Calculation                `json:"quantum_calculation,omitempty"`
	QuantumDiscrepancy      *quantum.QuantumDiscrepancy         `json:"quantum_discrepancy,omitempty"`
	MitigationImpact        *mitigation.Impact                  `json:"mitigation_impact,omitempty"`
	RecoveryTargets         []recovery.Target                   `json:"recovery_targets,omitempty"`
	Deadlines               []deadline.Deadline                 `json:"deadlines,omitempty"`
	CoverageAnalysis        *coverage.CoverageAnalysis          `json:"coverage_analysis,omitempty"`
	CoverageIssueMatrixRows []coverage.IssueRow                 `json:"coverage_issue_matrix,omitempty"`

	// Section 18: Human Review Questions — every item here traces back
	// to a HumanReviewRequired flag (or equivalent) some OTHER package
	// already raised; this package invents none of its own.
	HumanReviewQuestions []string `json:"human_review_questions"`
	HumanReviewRequired  bool     `json:"human_review_required"`
}

// Generate assembles the Dossier for c from in.
func Generate(c *insurancecase.Case, in Inputs) (*Dossier, error) {
	if c == nil {
		return nil, ErrNilCase
	}

	d := &Dossier{
		CaseID:  c.CaseID,
		ClaimID: in.ClaimID,
	}

	for _, t := range c.StateLog() {
		d.LifecycleAuditTrail = append(d.LifecycleAuditTrail, AuditEntry{
			From: string(t.From), To: string(t.To), Tick: t.Tick,
		})
	}

	d.TimelineConflicts = in.TimelineConflicts
	d.Contradictions = in.Contradictions
	if in.GapAssessment != nil {
		d.EvidenceSufficiency = in.GapAssessment.SortedByIssue()
	}
	d.CausationExplanation = in.CausationExplanation
	d.QuantumCalculation = in.QuantumCalculation
	d.QuantumDiscrepancy = in.QuantumDiscrepancy
	d.MitigationImpact = in.MitigationImpact
	d.RecoveryTargets = in.RecoveryTargets
	d.Deadlines = in.Deadlines
	d.CoverageAnalysis = in.CoverageAnalysis
	if in.CoverageAnalysis != nil {
		d.CoverageIssueMatrixRows = in.CoverageAnalysis.IssueMatrix().Rows()
	}

	d.HumanReviewQuestions, d.HumanReviewRequired = collectReviewQuestions(d, in)
	return d, nil
}

// collectReviewQuestions walks every input for its own already-raised
// review-required signal. It never derives a NEW reason to require
// human review that the underlying package did not itself flag.
func collectReviewQuestions(d *Dossier, in Inputs) ([]string, bool) {
	var qs []string
	required := false

	for _, cf := range d.TimelineConflicts {
		if cf.HumanReviewRequired {
			required = true
			qs = append(qs, "Timeline conflict "+cf.ConflictID+": "+cf.Description)
		}
	}
	for _, cr := range d.Contradictions {
		if cr.HumanReviewRequired {
			required = true
			qs = append(qs, "Contradiction "+cr.ContradictionID+": "+cr.Conflict)
		}
	}
	if in.GapAssessment != nil {
		for _, s := range in.GapAssessment.SortedByIssue() {
			if s.Status == gap.StatusInsufficient || s.Status == gap.StatusContradicted || s.HumanReviewRequired {
				required = true
				reason := s.Reason
				if reason == "" {
					reason = string(s.Status)
				}
				qs = append(qs, "Evidence issue \""+s.Issue+"\": "+reason)
			}
		}
	}
	if d.QuantumDiscrepancy != nil && d.QuantumDiscrepancy.HasUnresolvedGap() {
		required = true
		qs = append(qs, "Unresolved quantum discrepancy: "+d.QuantumDiscrepancy.DiscrepancyID)
	}
	if d.CoverageAnalysis != nil && d.CoverageAnalysis.ReviewRequired {
		required = true
		qs = append(qs, d.CoverageAnalysis.ReviewReasons...)
	}
	if d.CausationExplanation != nil && len(d.CausationExplanation.Contenders) > 1 {
		required = true
		qs = append(qs, "Causation: multiple tied hypotheses, no single leading cause")
	}

	return qs, required
}
