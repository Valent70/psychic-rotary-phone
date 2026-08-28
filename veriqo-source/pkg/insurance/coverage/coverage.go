// Package coverage is VICE's coverage-analysis engine — blueprint §18
// ("Coverage Analysis") and §19 ("Coverage Issue Matrix").
//
// The blueprint states this package's single constraint almost as a
// warning, and this package is built around obeying it structurally,
// not just by convention:
//
//	Coverage engine hanya melakukan:
//	POLICY + INCIDENT + CLAIM + EVIDENCE
//	lalu menghasilkan:
//	COVERAGE_FACTS, COVERAGE_QUESTIONS, COVERAGE_CONFLICTS,
//	COVERAGE_REVIEW_REQUIRED.
//	Bukan: COVERED = TRUE secara otomatis.
//
// Concretely: Analyze takes an already EffectiveAt-resolved
// policy.Version (§6 — never re-derived here; see the Input.PolicyVersion
// doc comment), a claim.Claim, and the case's evidence.Record set, and
// returns a CoverageAnalysis holding exactly the four output categories
// the blueprint names. There is no field, method, or function anywhere
// in this package's exported API that can produce a "covered:
// true/false" verdict — TestCoverageAnalysisHasNoCoverageVerdictField
// checks this by reflection so the guarantee is enforced at test time,
// not just by code review.
//
// # Status vocabulary
//
// The blueprint's own §19 worked example uses exactly three status
// words: Supported, Disputed, Partial. This package mirrors those
// (uppercased, matching the rest of pkg/insurance's enum convention —
// see evidence.Status's own SUPPORTED/DISPUTED-shaped values, which
// FactStatus deliberately echoes rather than reinventing) and adds a
// fourth the blueprint's golden rule (§36) demands: "When evidence is
// insufficient, the correct output is INSUFFICIENT_EVIDENCE — not a
// guessed conclusion." A fact this package cannot evaluate from the
// evidence it was given is INSUFFICIENT_EVIDENCE explicitly; it is
// never omitted from the output and never defaults to SUPPORTED.
package coverage

import (
	"errors"
	"sort"
)

// FactStatus is the resolution state of one CoverageFact or
// CoverageConflict. See the package doc comment for why these four
// values, and only these four, exist.
type FactStatus string

const (
	// StatusSupported: the available policy text and evidence support
	// this finding without contradiction.
	StatusSupported FactStatus = "SUPPORTED"
	// StatusDisputed: the finding is contested — either the evidence
	// conflicts, or a policy clause (e.g. an exclusion) plausibly cuts
	// against it. Never silently resolved either way.
	StatusDisputed FactStatus = "DISPUTED"
	// StatusPartial: some but not all of what the finding requires is
	// established.
	StatusPartial FactStatus = "PARTIAL"
	// StatusInsufficientEvidence: this package could not evaluate the
	// finding from what it was given. The blueprint's golden rule
	// (§36) names this exact case: never a guessed conclusion, never a
	// silent default to SUPPORTED, never simply left out of the output.
	StatusInsufficientEvidence FactStatus = "INSUFFICIENT_EVIDENCE"
)

var knownStatuses = map[FactStatus]bool{
	StatusSupported: true, StatusDisputed: true, StatusPartial: true, StatusInsufficientEvidence: true,
}

// IsKnownStatus reports whether s is a modelled FactStatus.
func IsKnownStatus(s FactStatus) bool { return knownStatuses[s] }

// CoverageFact is one traceable factual finding the engine derived
// from POLICY + INCIDENT + CLAIM + EVIDENCE — e.g. "peril occurred" or
// "within policy period". It is deliberately NOT a coverage verdict:
// it states what is or isn't supported, tied to the specific
// policy.Clause(s)/policy.Version it references and the specific
// evidence.Record EvidenceID(s) behind it, so a human reviewer can
// trace every word back to a source.
type CoverageFact struct {
	FactID      string     `json:"fact_id"`
	Description string     `json:"description"`
	Status      FactStatus `json:"status"`

	// PolicyVersionID ties this fact to the exact policy.Version it was
	// evaluated against (its VersionID, never just "the policy").
	PolicyVersionID string `json:"policy_version_id"`
	// ClauseIDs names the policy.Clause.ClauseID(s) this fact relies
	// on, if any (e.g. "§4"). Empty when the fact is computed purely
	// from incident/claim data with no specific clause behind it.
	ClauseIDs []string `json:"clause_ids,omitempty"`
	// EvidenceIDs names the evidence.Record EvidenceID(s) (content
	// addressed, from the Unified Evidence Engine) supporting this
	// fact. Empty is valid and meaningful — see StatusInsufficientEvidence.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`

	Notes string `json:"notes,omitempty"`
}

// CoverageQuestion is an open question the engine could not resolve
// from the available policy text and evidence. Phrased neutrally —
// never leading toward coverage ("this should be covered because...")
// or denial ("this looks excluded because...").
type CoverageQuestion struct {
	QuestionID string `json:"question_id"`
	Question   string `json:"question"`

	// RelatedFactID optionally names the CoverageFact this question
	// grew out of (e.g. a fact that came back INSUFFICIENT_EVIDENCE).
	// Empty when the question is a standing per-claim-type question
	// (claim.TypeDefinition.PolicyQuestions) not tied to one fact.
	RelatedFactID string   `json:"related_fact_id,omitempty"`
	ClauseIDs     []string `json:"clause_ids,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// CoverageConflict records where two facts, or a fact and a policy
// clause, appear to be in tension — e.g. an exclusion clause that
// could apply to an otherwise-established peril. A CoverageConflict's
// Status is DISPUTED by construction (NewCoverageConflict enforces
// this): a conflict this package could resolve one way or the other
// would not be a conflict, it would just be a fact.
type CoverageConflict struct {
	ConflictID  string     `json:"conflict_id"`
	Description string     `json:"description"`
	Status      FactStatus `json:"status"`

	FactIDs   []string `json:"fact_ids,omitempty"`
	ClauseIDs []string `json:"clause_ids,omitempty"`
}

// CoverageAnalysis is the aggregate Analyze returns: exactly the four
// output categories blueprint §18 names, plus the case/claim-level
// ReviewRequired marker. There is intentionally no boolean coverage
// verdict field anywhere on this type — see
// TestCoverageAnalysisHasNoCoverageVerdictField.
type CoverageAnalysis struct {
	CaseID          string `json:"case_id"`
	ClaimID         string `json:"claim_id"`
	PolicyID        string `json:"policy_id"`
	PolicyVersionID string `json:"policy_version_id"`

	Facts     []CoverageFact     `json:"coverage_facts"`
	Questions []CoverageQuestion `json:"coverage_questions"`
	Conflicts []CoverageConflict `json:"coverage_conflicts"`

	// ReviewRequired is set whenever ANY fact or conflict is not
	// cleanly resolved (i.e. any Facts[i].Status != StatusSupported,
	// any Conflicts entry exists, or any Questions entry exists).
	// Blueprint §18/§29: coverage is never finalised by this package —
	// ReviewRequired is the mechanism that makes that structural rather
	// than a matter of the caller remembering to check.
	ReviewRequired bool     `json:"coverage_review_required"`
	ReviewReasons  []string `json:"review_reasons,omitempty"`
}

var (
	ErrEmptyFactID         = errors.New("coverage: FactID must be non-empty")
	ErrUnknownFactStatus   = errors.New("coverage: unknown FactStatus")
	ErrEmptyQuestionID     = errors.New("coverage: QuestionID must be non-empty")
	ErrEmptyQuestion       = errors.New("coverage: Question text must be non-empty")
	ErrEmptyConflictID     = errors.New("coverage: ConflictID must be non-empty")
	ErrConflictNotDisputed = errors.New("coverage: a CoverageConflict must carry StatusDisputed — an already-resolved tension is not a conflict")
)

// Validate checks f's own internal consistency.
func (f CoverageFact) Validate() error {
	if f.FactID == "" {
		return ErrEmptyFactID
	}
	if !IsKnownStatus(f.Status) {
		return ErrUnknownFactStatus
	}
	return nil
}

// Validate checks q's own internal consistency.
func (q CoverageQuestion) Validate() error {
	if q.QuestionID == "" {
		return ErrEmptyQuestionID
	}
	if q.Question == "" {
		return ErrEmptyQuestion
	}
	return nil
}

// NewCoverageConflict constructs a CoverageConflict. Status is always
// StatusDisputed — a CoverageConflict, by definition, is a tension
// this package is not resolving one way or the other (blueprint golden
// rule: "an exclusion clause that could apply but is contested must
// produce status Disputed/ReviewRequired, never silently resolved
// either way").
func NewCoverageConflict(conflictID, description string, factIDs, clauseIDs []string) (CoverageConflict, error) {
	if conflictID == "" {
		return CoverageConflict{}, ErrEmptyConflictID
	}
	return CoverageConflict{
		ConflictID:  conflictID,
		Description: description,
		Status:      StatusDisputed,
		FactIDs:     append([]string(nil), factIDs...),
		ClauseIDs:   append([]string(nil), clauseIDs...),
	}, nil
}

// Validate checks c's own internal consistency.
func (c CoverageConflict) Validate() error {
	if c.ConflictID == "" {
		return ErrEmptyConflictID
	}
	if c.Status != StatusDisputed {
		return ErrConflictNotDisputed
	}
	return nil
}

// sortedCopy returns a sorted copy of ss, never mutating the input —
// used everywhere this package emits evidence/clause ID lists so
// output is deterministic regardless of map/slice iteration order
// upstream.
func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}
