// Package constitution is the executable form of the VERIQO Evidence
// Constitution (docs/constitution/EVIDENCE_CONSTITUTION.md).
//
// MIP-001 §4 states the requirement this package exists to satisfy:
// "These shall become executable policy tests, not merely
// documentation." Each of the thirty articles has a real check here
// that takes concrete facts and returns a determinate verdict.
//
// THREE VERDICTS, AND THE THIRD IS LOAD-BEARING. Satisfied and
// Violated are ordinary. NotEvaluable means the facts supplied do not
// carry what the article needs in order to be judged -- and it is
// never counted as a pass. Treating an unanswerable question as
// satisfied is precisely the failure this constitution exists to
// prevent; NotEvaluable is the constitutional analogue of the
// independent verifier's SKIP.
//
// AUTHORITY BOUNDARY. This package holds no authority of its own. It
// creates nothing, mutates nothing, signs nothing, and emits no
// events. Every check is a pure function over facts the caller
// supplies. That is deliberate: a constitution that could itself
// change state would be one more thing needing constitutional
// constraint.
package constitution

import (
	"fmt"
	"sort"
	"strings"
)

// Verdict is the outcome of checking one article against one set of
// facts.
type Verdict int

const (
	// NotEvaluable is the zero value on purpose: a check that forgets
	// to set a verdict reports "cannot judge", never "passes".
	NotEvaluable Verdict = iota
	Satisfied
	Violated
)

func (v Verdict) String() string {
	switch v {
	case Satisfied:
		return "SATISFIED"
	case Violated:
		return "VIOLATED"
	default:
		return "NOT_EVALUABLE"
	}
}

// Class records how an article is enforced. Stating this prevents the
// illusion that a document check equals a runtime guarantee.
type Class int

const (
	// Structural: the type system or state machine makes violation
	// unrepresentable.
	Structural Class = iota
	// Checked: a runtime check refuses the operation.
	Checked
	// Recorded: violation is not preventable, but is always detectable
	// after the fact from the ledger.
	Recorded
	// Declared: an organizational commitment the software surfaces but
	// cannot enforce alone. These require external attestation and are
	// NotEvaluable without one -- honestly, rather than defaulting to
	// satisfied.
	Declared
	// Bounded: enforced by the construction of the mechanism's own
	// semantics.
	Bounded
	// Pinned: enforced by version binding.
	Pinned
)

func (c Class) String() string {
	switch c {
	case Structural:
		return "STRUCTURAL"
	case Checked:
		return "CHECKED"
	case Recorded:
		return "RECORDED"
	case Declared:
		return "DECLARED"
	case Bounded:
		return "BOUNDED"
	case Pinned:
		return "PINNED"
	default:
		return "UNKNOWN"
	}
}

// Article is one constitutional invariant.
type Article struct {
	Number  int
	Title   string
	Class   Class
	Summary string
}

// Result is one article's verdict against one Subject.
type Result struct {
	Article int     `json:"article"`
	Title   string  `json:"title"`
	Class   string  `json:"class"`
	Verdict string  `json:"verdict"`
	Detail  string  `json:"detail"`
	verdict Verdict // typed copy for AllSatisfied/Violations
}

// Articles is the canonical, frozen article table. Order is by number;
// the table is returned as a copy so a caller cannot mutate the
// constitution.
func Articles() []Article {
	out := make([]Article, len(articles))
	copy(out, articles)
	return out
}

var articles = []Article{
	{1, "No Naked Facts", Checked, "No finding without evidence lineage."},
	{2, "No Truth by Acquisition", Bounded, "Acquisition establishes acquisition, not truth."},
	{3, "No Corroboration by Duplication", Checked, "Same-root data is one source."},
	{4, "No Authorization, No Contact", Checked, "Rights failure denies acquisition before contact."},
	{5, "Raw Before Transform", Checked, "Raw preserved before any transformation."},
	{6, "Immutable After Finalization", Structural, "A finalized version is never updated."},
	{7, "Historical Policy Pinning", Pinned, "Historical cases use their historical policy version."},
	{8, "AI Has No Evidence Authority", Checked, "AI cannot create, alter, qualify or sign evidence."},
	{9, "ZKP Has Bounded Meaning", Bounded, "A proof establishes only its predicate."},
	{10, "Replay Must Be Independent", Bounded, "Replay verifiable without trusting the runtime."},
	{11, "Disagreement Must Remain Visible", Recorded, "Dissent is never deleted or suppressed."},
	{12, "Procedural Symmetry", Recorded, "Same policy for all parties absent an authorized exception."},
	{13, "Party Influence Must Be Disclosed", Recorded, "Party influence on acquisition is recorded."},
	{14, "Conflict Must Be Declared", Recorded, "Conflicts are declared, never concealed."},
	{15, "No Outcome-Contingent Neutrality", Declared, "No differential benefit from a dispute outcome."},
	{16, "No Adjudication by Platform", Declared, "VERIQO does not determine legal liability."},
	{17, "No Destructive Redaction", Structural, "The original is never modified."},
	{18, "No Visual-Only Redaction", Checked, "Content must be absent from the derivative's bytes."},
	{19, "Privilege Is Authority-Determined", Checked, "VERIQO enforces; it does not determine privilege."},
	{20, "Access Does Not Imply Use", Checked, "View, export, AI processing and training are separate grants."},
	{21, "Redaction Does Not Imply AI Eligibility", Bounded, "A redacted derivative must still pass AI policy."},
	{22, "Evidence Version Is Immutable", Structural, "Every derivative is a new version."},
	{23, "Audit Is Evidence", Recorded, "Process evidence is itself evidence."},
	{24, "No Silent Disclosure", Checked, "Every disclosure emits a ledger event."},
	{25, "No Silent Privilege Change", Checked, "Privilege transitions are immutable events."},
	{26, "No Silent Policy Retroactivity", Recorded, "Policy change is never quietly applied to history."},
	{27, "No Silent AI Influence", Checked, "Material AI contribution is recorded."},
	{28, "No Unsupported Independence", Checked, "Independence derives from lineage; UNKNOWN is not INDEPENDENT."},
	{29, "No Unqualified Absence", Checked, "OBSERVED_ABSENT only after the observability gate."},
	{30, "No Absolute Epistemic Claims", Declared, "Integrity, provenance, qualification, neutrality and legal determination are distinct."},
}

// ByNumber returns one article.
func ByNumber(n int) (Article, bool) {
	for _, a := range articles {
		if a.Number == n {
			return a, true
		}
	}
	return Article{}, false
}

// ---------------------------------------------------------------
// Facts
// ---------------------------------------------------------------

// Subject carries the facts to be judged. Every field is a pointer or
// a slice so that "not supplied" is distinguishable from "supplied and
// empty" -- the distinction between NotEvaluable and a real verdict.
type Subject struct {
	Finding       *FindingFacts
	Corroboration *CorroborationFacts
	Acquisition   *AcquisitionFacts
	Absence       *AbsenceFacts
	AI            *AIFacts
	Disclosure    *DisclosureFacts
	Redaction     *RedactionFacts
	Privilege     *PrivilegeFacts
	Dissent       *DissentFacts
	Policy        *PolicyFacts
	Procedure     *ProcedureFacts
	Version       *VersionFacts
	// Attestations records external attestations for Declared-class
	// articles, keyed by article number. Absent an attestation a
	// Declared article is NotEvaluable -- the software says so rather
	// than pretending a unit test settled a commercial arrangement.
	Attestations map[int]string
}

// FindingFacts backs Article 1.
type FindingFacts struct {
	ID string
	// EvidenceRefs are the evidence versions the finding cites.
	EvidenceRefs []string
	// LineageEstablished reports, per evidence ref, whether lineage was
	// actually traced. A citation without lineage does not satisfy
	// Article 1.
	LineageEstablished map[string]bool
}

// CorroborationFacts backs Articles 3 and 28.
type CorroborationFacts struct {
	// ClaimedIndependent is true when the caller asserts these sources
	// independently corroborate one another.
	ClaimedIndependent bool
	// SourceRoots maps source ID to its root origin. Two sources
	// sharing a root are one source (Article 3).
	SourceRoots map[string]string
	// DependencyKnown maps source-pair key to whether the dependency
	// relation was actually assessed. An unassessed pair is UNKNOWN,
	// and UNKNOWN is not INDEPENDENT (Article 28).
	DependencyKnown map[string]bool
}

// AcquisitionFacts backs Articles 4 and 5.
type AcquisitionFacts struct {
	SourceID string
	// RightsChecked and RightsGranted describe the pre-contact gate.
	RightsChecked bool
	RightsGranted bool
	// ContactMade reports whether the connector actually reached the
	// source. Contact without granted rights violates Article 4.
	ContactMade bool
	// RawPreserved and Transformed back Article 5: a transformation
	// that ran without the raw artifact preserved is a violation.
	RawPreserved bool
	Transformed  bool
}

// AbsenceFacts backs Article 29.
type AbsenceFacts struct {
	// ReportedState is the absence state the caller wishes to assert.
	ReportedState string
	// GateConditions are the nine observability conditions.
	// OBSERVED_ABSENT requires every one of them.
	GateConditions map[string]bool
}

// ObservabilityGateConditions is the fixed set of nine conditions
// OBSERVED_ABSENT requires (EQF-001 §4).
func ObservabilityGateConditions() []string {
	return []string{
		"adequate_source", "operational_availability", "known_coverage",
		"valid_query", "valid_expectation", "correct_temporal_scope",
		"correct_spatial_scope", "integrity", "review_where_material",
	}
}

// AIFacts backs Articles 8 and 27.
type AIFacts struct {
	ModelID string
	// Actions the AI performed or attempted.
	Actions []string
	// ContributionRecorded reports whether an AI contribution record
	// exists for a material contribution (Article 27).
	MaterialContribution bool
	ContributionRecorded bool
}

// ForbiddenAIActions are the actions no AI may perform (AI-AUTHORITY-001
// §2). Any of these appearing in AIFacts.Actions violates Article 8.
func ForbiddenAIActions() []string {
	return []string{
		"alter_evidence", "delete_evidence", "change_trust", "change_policy",
		"approve_disclosure", "confirm_privilege", "suppress_contradiction",
		"qualify_evidence", "sign_qualification", "determine_liability",
		"instruct_connector",
	}
}

// DisclosureFacts backs Articles 20 and 24.
type DisclosureFacts struct {
	// GrantedRights are the purposes explicitly granted.
	GrantedRights []string
	// ExercisedRights are the purposes actually exercised. Exercising a
	// right that was not granted violates Article 20.
	ExercisedRights []string
	// EventEmitted reports whether the disclosure emitted a ledger
	// event (Article 24).
	Occurred     bool
	EventEmitted bool
}

// RedactionFacts backs Articles 17 and 18.
type RedactionFacts struct {
	OriginalHashBefore string
	OriginalHashAfter  string
	// DerivativeCreated reports whether redaction produced a separate
	// derivative rather than editing in place.
	DerivativeCreated bool
	// RecoveryTestsRun and ContentRecoverable back Article 18: a
	// derivative whose content is still recoverable is visual-only
	// redaction.
	RecoveryTestsRun   bool
	ContentRecoverable bool
}

// PrivilegeFacts backs Articles 19 and 25.
type PrivilegeFacts struct {
	Status string
	// DeterminedBy identifies who made the substantive determination.
	// VERIQO determining it itself violates Article 19.
	DeterminedBy string
	// StatusChanged and EventEmitted back Article 25.
	StatusChanged bool
	EventEmitted  bool
}

// DissentFacts backs Article 11.
type DissentFacts struct {
	// Recorded and CarriedToFinding: a MATERIAL or CRITICAL dissent
	// recorded but absent from the finding is suppression.
	Recorded         []string // severities
	CarriedToFinding []string // severities present in the final finding
}

// PolicyFacts backs Articles 7 and 26.
type PolicyFacts struct {
	// CasePolicyVersion is the version in force when the case ran.
	CasePolicyVersion string
	// EvaluatedPolicyVersion is the version actually used to evaluate.
	EvaluatedPolicyVersion string
	// RetroactiveChange and ChangeRecorded back Article 26.
	RetroactiveChange bool
	ChangeRecorded    bool
}

// ProcedureFacts backs Articles 12, 13, 14 and 23.
type ProcedureFacts struct {
	// PartyPolicies maps party ID to the policy version applied. Two
	// parties under different policies without an authorized exception
	// violate Article 12.
	PartyPolicies       map[string]string
	AuthorizedException bool
	// PartyInfluenceRecorded backs Article 13.
	PartyInfluenceOccurred bool
	PartyInfluenceRecorded bool
	// ConflictsKnown vs ConflictsDeclared back Article 14.
	ConflictsKnown    int
	ConflictsDeclared int
	// ProcessEvidenceRetained backs Article 23.
	ProcessEvidenceRetained bool
}

// VersionFacts backs Articles 6 and 22.
type VersionFacts struct {
	Finalized bool
	// MutatedAfterFinalization is the Article 6 violation.
	MutatedAfterFinalization bool
	// DerivativeGotNewVersion backs Article 22.
	DerivativeCreated       bool
	DerivativeGotNewVersion bool
}

// ---------------------------------------------------------------
// Checking
// ---------------------------------------------------------------

func res(a Article, v Verdict, detail string) Result {
	return Result{
		Article: a.Number, Title: a.Title, Class: a.Class.String(),
		Verdict: v.String(), Detail: detail, verdict: v,
	}
}

// mustArticle panics only on a programming error inside this package
// (an article number that is not in the frozen table).
func mustArticle(n int) Article {
	a, ok := ByNumber(n)
	if !ok {
		panic(fmt.Sprintf("constitution: unknown article %d", n))
	}
	return a
}

// declared handles every Declared-class article uniformly: an external
// attestation makes it Satisfied; its absence makes it NotEvaluable.
// It is never Violated by software inspection alone, and never
// silently Satisfied.
func declared(s Subject, n int) Result {
	a := mustArticle(n)
	if att, ok := s.Attestations[n]; ok && strings.TrimSpace(att) != "" {
		return res(a, Satisfied, "external attestation supplied: "+att)
	}
	return res(a, NotEvaluable, "requires external attestation; not discharged by software inspection")
}

// Check evaluates every article it has facts for and returns one
// Result per article, ordered by article number. Articles with no
// supporting facts report NotEvaluable rather than being omitted, so a
// caller can always see the full constitutional surface.
func Check(s Subject) []Result {
	out := make([]Result, 0, len(articles))
	out = append(out,
		checkArticle1(s), checkArticle2(s), checkArticle3(s), checkArticle4(s),
		checkArticle5(s), checkArticle6(s), checkArticle7(s), checkArticle8(s),
		checkArticle9(s), checkArticle10(s), checkArticle11(s), checkArticle12(s),
		checkArticle13(s), checkArticle14(s), declared(s, 15), declared(s, 16),
		checkArticle17(s), checkArticle18(s), checkArticle19(s), checkArticle20(s),
		checkArticle21(s), checkArticle22(s), checkArticle23(s), checkArticle24(s),
		checkArticle25(s), checkArticle26(s), checkArticle27(s), checkArticle28(s),
		checkArticle29(s), declared(s, 30),
	)
	sort.Slice(out, func(i, j int) bool { return out[i].Article < out[j].Article })
	return out
}

// Violations returns only the violated results.
func Violations(results []Result) []Result {
	var out []Result
	for _, r := range results {
		if r.verdict == Violated {
			out = append(out, r)
		}
	}
	return out
}

// NotEvaluables returns the results that could not be judged. A caller
// reporting constitutional compliance must surface these -- they are
// not passes.
func NotEvaluables(results []Result) []Result {
	var out []Result
	for _, r := range results {
		if r.verdict == NotEvaluable {
			out = append(out, r)
		}
	}
	return out
}

// NoViolations reports whether nothing was violated. It deliberately
// does NOT assert that everything was satisfied: NotEvaluable articles
// remain unjudged, and a caller wanting full compliance must check
// NotEvaluables too.
func NoViolations(results []Result) bool { return len(Violations(results)) == 0 }
