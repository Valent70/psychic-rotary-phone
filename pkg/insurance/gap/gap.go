// Package gap implements two closely-related blueprint engines that
// share one job: telling the truth about what is NOT known yet.
//
//   - The Missing Evidence Engine (§26). The blueprint's own words:
//     "Jangan hanya mencari evidence yang ada. VICE harus mencari: Apa
//     evidence yang seharusnya ada tetapi belum tersedia?" — this
//     package never inspects evidence content; it computes a real
//     set-difference between a claim.TypeDefinition's declared
//     RequiredEvidence/OptionalEvidence and the DocumentTypes actually
//     present in an evidence.Registry (or a plain []evidence.Record),
//     and reports the result as an EVIDENCE_GAP.
//   - Evidence Sufficiency (§27). For any named issue (Causation,
//     Quantum, Notice, Coverage, or any other issue name a downstream
//     package invents — this package treats issue names as open
//     strings, never a closed enum), sufficiency is exactly one of the
//     blueprint's own four values: COMPLETE, PARTIAL, INSUFFICIENT,
//     CONTRADICTED.
//
// Golden rule (§36, the one this package exists to enforce
// mechanically): "When evidence is insufficient, the correct output is
// INSUFFICIENT_EVIDENCE — not a guessed conclusion." Nothing in this
// package ever defaults to COMPLETE; a Sufficiency value is always the
// deterministic result of comparing what was required against what was
// actually submitted, never an assumption.
package gap

import (
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/evidence"
)

// ---- Missing Evidence Engine (§26) ----

var (
	ErrNilTypeDefinition  = errors.New("gap: claim.TypeDefinition must be non-zero (at least Type and RequiredEvidence set)")
	ErrEmptyRequiredInDef = errors.New("gap: claim.TypeDefinition.RequiredEvidence must declare at least one item")
)

// Report is the blueprint's own EVIDENCE_GAP output (§26): for one
// claim type, which required and optional evidence items are present
// versus missing, computed as a genuine set difference against what
// was actually submitted — never a hard-coded list.
type Report struct {
	ClaimType claim.Type `json:"claim_type"`

	RequiredPresent []string `json:"required_present"`
	RequiredMissing []string `json:"required_missing"`

	OptionalPresent []string `json:"optional_present"`
	OptionalMissing []string `json:"optional_missing"`
}

// IsComplete reports whether every required evidence item for this
// claim type has been submitted. It says nothing about optional
// evidence, and nothing about whether the submitted evidence is itself
// authentic, corroborated, or sufficient in substance — that is
// Sufficiency's job, below.
func (r Report) IsComplete() bool { return len(r.RequiredMissing) == 0 }

// DetectGaps computes the EVIDENCE_GAP report for one claim type: def
// supplies the claim type's declared required_evidence and
// optional_evidence (blueprint §4); present is the set of
// evidence.DocumentType strings actually submitted for this claim, in
// whatever order the caller has them. present is matched exactly
// against def's declared item strings — the caller is responsible for
// using the same document-type vocabulary on both sides (the
// blueprint's own worked example in §26 does this: RequiredEvidence
// item "raw_logger_data" is only satisfied by a submitted DocumentType
// of exactly "raw_logger_data").
//
// The result never omits a genuinely-missing item and never reports an
// item as present unless it was actually found in present — this
// function performs a real comparison, not a lookup into any
// precomputed or hard-coded table.
func DetectGaps(def claim.TypeDefinition, present []string) (Report, error) {
	if def.Type == "" {
		return Report{}, ErrNilTypeDefinition
	}
	if len(def.RequiredEvidence) == 0 {
		return Report{}, ErrEmptyRequiredInDef
	}

	have := make(map[string]bool, len(present))
	for _, p := range present {
		if p != "" {
			have[p] = true
		}
	}

	rPresent, rMissing := splitByPresence(def.RequiredEvidence, have)
	oPresent, oMissing := splitByPresence(def.OptionalEvidence, have)

	return Report{
		ClaimType:       def.Type,
		RequiredPresent: rPresent,
		RequiredMissing: rMissing,
		OptionalPresent: oPresent,
		OptionalMissing: oMissing,
	}, nil
}

// DetectGapsFromRegistry is DetectGaps convenience wrapper that reads
// the submitted document-type set directly from an evidence.Registry,
// restricted to records belonging to caseID. Every Record's
// DocumentType field (evidence.Record.DocumentType — blueprint §8) is
// what "present" evidence means here; a Record with an empty
// DocumentType contributes nothing (it cannot satisfy any named
// requirement) and is silently skipped, never counted as satisfying an
// arbitrary requirement.
func DetectGapsFromRegistry(def claim.TypeDefinition, reg *evidence.Registry, caseID string) (Report, error) {
	if reg == nil {
		return DetectGaps(def, nil)
	}
	var present []string
	for _, rec := range reg.All() {
		if rec.CaseID != caseID {
			continue
		}
		if rec.DocumentType != "" {
			present = append(present, rec.DocumentType)
		}
	}
	return DetectGaps(def, present)
}

// splitByPresence partitions items (declared required_evidence or
// optional_evidence entries, in the claim type's own declared order)
// into (present, missing) against have, deterministically. Output
// order matches items' own declared order — no re-sorting that would
// obscure which item was declared where.
func splitByPresence(items []string, have map[string]bool) (present, missing []string) {
	for _, item := range items {
		if have[item] {
			present = append(present, item)
		} else {
			missing = append(missing, item)
		}
	}
	return present, missing
}

// ---- Evidence Sufficiency (§27) ----

// Status is the blueprint's own closed four-value sufficiency enum
// (§27) — deliberately closed, unlike claim.Type or the issue-name
// strings Sufficiency accepts below, because §27 states it as an exact
// four-item list.
type Status string

const (
	StatusComplete     Status = "COMPLETE"
	StatusPartial      Status = "PARTIAL"
	StatusInsufficient Status = "INSUFFICIENT"
	StatusContradicted Status = "CONTRADICTED"
)

var knownStatuses = map[Status]bool{
	StatusComplete: true, StatusPartial: true, StatusInsufficient: true, StatusContradicted: true,
}

// IsKnownStatus reports whether s is one of the four modelled
// sufficiency statuses.
func IsKnownStatus(s Status) bool { return knownStatuses[s] }

// Sufficiency is one issue's evidence-sufficiency rating (§27's own
// worked example: "Causation: PARTIAL", "Quantum: COMPLETE", ...).
// Issue is deliberately an open string, not a closed enum: the
// blueprint names Causation, Quantum, Notice and Coverage only as
// EXAMPLES ("Setiap claim issue: ..."), and other packages this one
// must not import (causation, coverage, quantum, ...) will each report
// sufficiency for their own issue independently.
//
// Design decision — how §27's "Coverage: HUMAN_REVIEW_REQUIRED"
// worked example maps onto the four-value Status enum:
//
// §27 states the sufficiency enum as an exact, closed four-item list
// (COMPLETE / PARTIAL / INSUFFICIENT / CONTRADICTED), then in its own
// worked example immediately below uses a FIFTH value —
// HUMAN_REVIEW_REQUIRED — for the Coverage row. Reading this literally
// as a fifth Status value would silently reopen an enum the same
// section just declared closed, and it would collide with the
// blueprint's broader vocabulary: HUMAN_REVIEW_REQUIRED is one of the
// eight top-level outputs VICE must produce everywhere (§0), not a
// sufficiency rating specifically — it is orthogonal to "how much
// evidence exists for this issue" and instead means "a human, not
// VICE, must decide this regardless of how the evidence nets out".
//
// The faithful, structurally clean reading is therefore: Coverage's
// evidence sufficiency is STILL one of the four core values (computed
// the same way as any other issue, from its own required/available
// evidence comparison), and HumanReviewRequired is a SEPARATE boolean
// flag that can be set on ANY issue's Sufficiency independently of its
// Status — exactly the shape the blueprint's principle at §0 already
// requires ("VICE MUST NOT ... automatically approve/reject") and
// which §18 restates for coverage specifically ("Bukan: COVERED = TRUE
// secara otomatis"). This also lets a genuinely COMPLETE issue still
// be flagged for human review (e.g. a coverage question that is
// evidentially settled but legally novel), which a single closed
// five-value enum could never express.
type Sufficiency struct {
	Issue  string `json:"issue"`
	Status Status `json:"status"`
	// Reason is a short, human-readable explanation of why Status was
	// reached — e.g. "2 of 3 required evidence items present" or
	// "conflicting survey reports for the same custody interval".
	Reason string `json:"reason,omitempty"`
	// HumanReviewRequired flags that this issue needs human review
	// regardless of Status — orthogonal to evidence completeness (see
	// the design-decision comment above). Never set implicitly by this
	// package's own computations; callers set it explicitly when their
	// own domain logic (coverage, legal novelty, policy ambiguity, ...)
	// determines it applies.
	HumanReviewRequired bool `json:"human_review_required,omitempty"`
}

var (
	ErrEmptyIssue        = errors.New("gap: Sufficiency.Issue must be non-empty")
	ErrUnknownStatus     = errors.New("gap: unknown Status")
	ErrDuplicateIssue    = errors.New("gap: this Issue already has a Sufficiency rating in this Assessment")
	ErrIssueNotAssessed  = errors.New("gap: Issue has no Sufficiency rating in this Assessment")
	ErrNegativeCounts    = errors.New("gap: presentCount and requiredCount must be >= 0")
	ErrPresentExceedsReq = errors.New("gap: presentCount cannot exceed requiredCount")
)

// Validate reports whether s is well-formed: a non-empty Issue and a
// recognised Status.
func (s Sufficiency) Validate() error {
	if s.Issue == "" {
		return ErrEmptyIssue
	}
	if !IsKnownStatus(s.Status) {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, s.Status)
	}
	return nil
}

// ComputeSufficiency is the golden-rule-enforcing core of §27: given
// how many required evidence items an issue needs and how many are
// actually present, it returns the correct Status — never guessed,
// always the direct consequence of the two counts.
//
//   - requiredCount == 0 is refused (ErrNegativeCounts's sibling case,
//     handled by the caller: an issue with zero required evidence is
//     not this function's problem to rate — callers should not call it
//     for an issue nothing was ever required for).
//   - presentCount == 0 (nothing required is present)      -> INSUFFICIENT
//   - 0 < presentCount < requiredCount (some but not all)  -> PARTIAL
//   - presentCount == requiredCount (everything present)   -> COMPLETE
//
// contradicted, when true, overrides the count-based result entirely:
// even if every required item is nominally present, actively
// conflicting evidence for this issue means the correct output is
// CONTRADICTED, not COMPLETE — matching §27's own status name and the
// blueprint's general rule that corroboration and contradiction are
// tracked independently (§9, §25).
//
// This function never returns StatusComplete when presentCount is 0,
// and never silently drops missing items from the caller's view — it
// is the caller's job (typically via DetectGaps first) to know exactly
// which items are missing; this function only turns the resulting
// counts into the correctly-graded Status.
func ComputeSufficiency(issue string, requiredCount, presentCount int, contradicted bool) (Sufficiency, error) {
	if issue == "" {
		return Sufficiency{}, ErrEmptyIssue
	}
	if requiredCount < 0 || presentCount < 0 {
		return Sufficiency{}, ErrNegativeCounts
	}
	if presentCount > requiredCount {
		return Sufficiency{}, ErrPresentExceedsReq
	}

	if contradicted {
		return Sufficiency{
			Issue:  issue,
			Status: StatusContradicted,
			Reason: fmt.Sprintf("%d of %d required evidence item(s) present, but evidence for %q is contradicted", presentCount, requiredCount, issue),
		}, nil
	}

	switch {
	case requiredCount == 0 || presentCount == 0:
		return Sufficiency{
			Issue:  issue,
			Status: StatusInsufficient,
			Reason: fmt.Sprintf("%d of %d required evidence item(s) present", presentCount, requiredCount),
		}, nil
	case presentCount < requiredCount:
		return Sufficiency{
			Issue:  issue,
			Status: StatusPartial,
			Reason: fmt.Sprintf("%d of %d required evidence item(s) present", presentCount, requiredCount),
		}, nil
	default: // presentCount == requiredCount, requiredCount > 0
		return Sufficiency{
			Issue:  issue,
			Status: StatusComplete,
			Reason: fmt.Sprintf("%d of %d required evidence item(s) present", presentCount, requiredCount),
		}, nil
	}
}

// SufficiencyFromReport derives an issue's Sufficiency directly from a
// Report's own required-evidence counts — the intended way to go from
// DetectGaps straight to a §27 rating without ever hand-computing (and
// risking miscounting) the present/missing split a second time.
func SufficiencyFromReport(issue string, r Report, contradicted bool) (Sufficiency, error) {
	total := len(r.RequiredPresent) + len(r.RequiredMissing)
	return ComputeSufficiency(issue, total, len(r.RequiredPresent), contradicted)
}

// ---- Assessment: multi-issue sufficiency aggregate (§27, §29) ----

// Assessment holds Sufficiency ratings for every issue analysed on one
// claim — the aggregate shape §27's worked example shows (Causation,
// Quantum, Notice, Coverage all rated independently on the same
// claim) and that §29's final case-status summary reads from.
//
// Unlike claim.TypeRegistry or evidence.Registry, Assessment is not
// safe for concurrent use by multiple goroutines without external
// synchronization: it is intended to be built up once, by a single
// analysis pass over one claim's issues, then read freely afterward —
// mirroring how the causation/coverage/quantum packages each produce
// one Sufficiency value per issue and hand it here.
type Assessment struct {
	ClaimID string
	ratings map[string]Sufficiency
	order   []string
}

// NewAssessment returns an empty Sufficiency assessment for claimID.
func NewAssessment(claimID string) *Assessment {
	return &Assessment{ClaimID: claimID, ratings: make(map[string]Sufficiency)}
}

// Set records (or overwrites) the Sufficiency rating for s.Issue.
// Refuses an invalid Sufficiency (see Validate) and a re-rating of an
// issue already present under a DIFFERENT status without going
// through Overwrite — this package never silently lets a later,
// possibly-careless call clobber an earlier finding.
func (a *Assessment) Set(s Sufficiency) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if existing, ok := a.ratings[s.Issue]; ok && existing != s {
		return fmt.Errorf("%w: %s (call Overwrite if this is intentional)", ErrDuplicateIssue, s.Issue)
	}
	if _, ok := a.ratings[s.Issue]; !ok {
		a.order = append(a.order, s.Issue)
	}
	a.ratings[s.Issue] = s
	return nil
}

// Overwrite records s.Issue's Sufficiency unconditionally, whether or
// not that issue already has a rating — for re-analysis passes (e.g.
// new evidence arrives and a previously-COMPLETE issue must be
// recomputed).
func (a *Assessment) Overwrite(s Sufficiency) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if _, ok := a.ratings[s.Issue]; !ok {
		a.order = append(a.order, s.Issue)
	}
	a.ratings[s.Issue] = s
	return nil
}

// Get returns the Sufficiency rating for issue.
func (a *Assessment) Get(issue string) (Sufficiency, bool) {
	s, ok := a.ratings[issue]
	return s, ok
}

// All returns every recorded Sufficiency in the order issues were
// first Set, for deterministic output.
func (a *Assessment) All() []Sufficiency {
	out := make([]Sufficiency, 0, len(a.order))
	for _, issue := range a.order {
		out = append(out, a.ratings[issue])
	}
	return out
}

// SortedByIssue returns every recorded Sufficiency sorted
// lexicographically by Issue name — for callers (e.g. the dossier
// generator) that want stable output independent of analysis order.
func (a *Assessment) SortedByIssue() []Sufficiency {
	out := a.All()
	sort.Slice(out, func(i, j int) bool { return out[i].Issue < out[j].Issue })
	return out
}

// IsFullyComplete reports whether every recorded issue is COMPLETE and
// not flagged for human review. An Assessment with zero recorded
// issues is NOT fully complete — an empty assessment means nothing has
// been evaluated yet, which is exactly the kind of unassessed state
// the golden rule (§36) forbids treating as a passing result.
func (a *Assessment) IsFullyComplete() bool {
	if len(a.order) == 0 {
		return false
	}
	for _, issue := range a.order {
		s := a.ratings[issue]
		if s.Status != StatusComplete || s.HumanReviewRequired {
			return false
		}
	}
	return true
}

// HasInsufficientOrContradicted reports whether ANY recorded issue is
// INSUFFICIENT or CONTRADICTED — the single check a caller needs to
// enforce the golden rule ("the correct output is INSUFFICIENT_EVIDENCE
// — not a guessed conclusion") before allowing any downstream engine to
// proceed as though the claim were fully evidenced.
func (a *Assessment) HasInsufficientOrContradicted() bool {
	for _, issue := range a.order {
		s := a.ratings[issue]
		if s.Status == StatusInsufficient || s.Status == StatusContradicted {
			return true
		}
	}
	return false
}

// Count returns the number of issues recorded in the assessment.
func (a *Assessment) Count() int { return len(a.order) }
