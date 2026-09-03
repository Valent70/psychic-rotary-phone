// Package proof carries the VERIQO Proof Object: the canonical,
// cryptographically bound record behind every significant conclusion.
//
// The Proof Object exists because a conclusion on its own is not
// reviewable. "The cargo was contaminated before loading" is a sentence;
// what makes it evidence is the whole apparatus behind it — which
// evidence, whose, how independent, what contradicts it, what is missing,
// what would have falsified it, who was allowed to see it, whether a
// model touched it, and what remains outside VERIQO's power to establish.
// This package makes that apparatus a single value that travels with the
// conclusion and can be hashed, signed, replayed and disputed.
//
// The twenty-three components are fixed by the architecture. They are
// not a suggested schema: Validate refuses an object missing any
// load-bearing one, so an incomplete Proof Object cannot be presented as
// a complete one.
//
// What this package deliberately does not do:
//
//   - It does not qualify evidence. pkg/qualification owns that, and the
//     Proof Object carries its verdict rather than recomputing it.
//   - It does not decide disclosure. pkg/disclosure/access owns that.
//   - It does not authorize. Authorization is a separate act performed on
//     a sufficient object, never a field an author can set.
//   - It does not assert external qualification. That is an outside
//     party's word, and its zero value says so.
package proof

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/qualification/reverseproof"
	"veriqo/pkg/qualification/state"
)

// Stance is what the evidence set does to the proposition.
//
// Unknown is the zero value on purpose. A Proof Object whose author
// forgot to record a stance says "we do not know", never "supported".
type Stance int

const (
	// Unknown: the evidence neither supports nor contradicts the
	// proposition to any material degree, or has not been assessed.
	Unknown Stance = iota
	// Support: the evidence set, as qualified, supports the proposition.
	Support
	// Contradict: the evidence set, as qualified, cuts against it.
	Contradict
)

func (s Stance) String() string {
	switch s {
	case Support:
		return "SUPPORT"
	case Contradict:
		return "CONTRADICT"
	default:
		return "UNKNOWN"
	}
}

// Sufficiency records whether the qualified evidence carries the
// proposition far enough to found a finding.
//
// NotDetermined is the zero value: sufficiency is a judgement that must
// be made, and an object that never made it is not sufficient.
type Sufficiency int

const (
	NotDetermined Sufficiency = iota
	Sufficient
	Insufficient
)

func (s Sufficiency) String() string {
	switch s {
	case Sufficient:
		return "SUFFICIENT"
	case Insufficient:
		return "INSUFFICIENT"
	default:
		return "NOT_DETERMINED"
	}
}

// ExternalStatus is the standing of this conclusion with parties outside
// VERIQO — an accredited lab, a class society, a court, a regulator, a
// trusted timestamp authority.
//
// NotSought is the zero value. VERIQO never begins by assuming an
// outside party has blessed its work.
type ExternalStatus int

const (
	// NotSought: no external qualification has been attempted.
	NotSought ExternalStatus = iota
	// Sought: requested, no result yet.
	Sought
	// Qualified: an identified external party qualified this conclusion.
	Qualified
	// Refused: an external party considered and declined to qualify it.
	Refused
	// Unavailable: no qualifying party exists or is reachable for this
	// proposition. Distinct from Refused — nobody said no, there is
	// nobody to ask.
	Unavailable
)

func (e ExternalStatus) String() string {
	switch e {
	case Sought:
		return "SOUGHT"
	case Qualified:
		return "EXTERNALLY_QUALIFIED"
	case Refused:
		return "REFUSED"
	case Unavailable:
		return "UNAVAILABLE"
	default:
		return "NOT_SOUGHT"
	}
}

// Satisfied reports whether an outside party actually stands behind
// this. Only Qualified does. Sought is not qualified; Unavailable is not
// qualified; and a zero value is certainly not.
func (e ExternalStatus) Satisfied() bool { return e == Qualified }

// --- Component types -------------------------------------------------

// Proposition is the thing being proved. It is a claim about the world,
// stated so that it could in principle be false.
type Proposition struct {
	ID string
	// Statement is the proposition in plain language. It must be
	// falsifiable: "the vessel deviated from the declared route between
	// 03:00 and 07:00 UTC on 12 March", not "the vessel behaved oddly".
	Statement string
	// SubjectType and SubjectID bind the proposition to a canonical
	// ontology object, so two propositions about the same vessel are
	// discoverably about the same vessel.
	SubjectType string
	SubjectID   string
}

// Scope is what the proposition is and is not about. A conclusion read
// outside its scope is a different conclusion.
type Scope struct {
	CaseID string
	// Matter names the dispute, claim or investigation.
	Matter string
	// Boundaries states what is deliberately excluded.
	Boundaries []string
}

// Jurisdiction is the legal frame the conclusion is stated within.
// The same facts qualify differently in different fora.
type Jurisdiction struct {
	Code string
	// Forum is the court, tribunal or authority, where one is known.
	Forum string
	// GoverningLaw is the law named by the contract or the seat.
	GoverningLaw string
}

// TimeWindow is the interval the proposition speaks to. Evidence
// outside it may inform but does not establish.
type TimeWindow struct {
	FromTick uint64
	ToTick   uint64
}

// Valid reports whether the window is ordered and non-degenerate.
func (w TimeWindow) Valid() bool { return w.ToTick >= w.FromTick }

// EvidenceRef pins one piece of evidence at a specific version. A Proof
// Object built against version 2 is not a proof object about version 3.
type EvidenceRef struct {
	EvidenceID        string
	EvidenceVersionID string
	SHA256            string
	// SourceID is the independence-assessable origin, not the custodian.
	SourceID string
}

// Quality is the assessed evidential quality of the set as a whole.
type Quality struct {
	// Assessed is false until someone actually assessed it; an
	// unassessed set is never treated as high quality.
	Assessed bool
	// Grade is a short, policy-defined label ("primary", "derived",
	// "hearsay"). VERIQO does not invent a numeric quality score here.
	Grade string
	// Concerns names specific quality problems found.
	Concerns []string
}

// Contradiction is a specific conflict inside or against the evidence
// set. Contradictions are carried, never quietly resolved by averaging.
type Contradiction struct {
	ID string
	// Between names the conflicting evidence version IDs.
	Between []string
	// Description states the conflict.
	Description string
	// Material marks a contradiction that bears on the proposition
	// rather than on some incidental detail.
	Material bool
	// Resolved records that an authorized human resolved it, and how.
	Resolved   bool
	Resolution string
}

// MissingEvidence is evidence the proposition needs and does not have.
type MissingEvidence struct {
	ConditionID string
	Description string
	// Obtainable distinguishes "we have not got it" from "it cannot be
	// got" — a distinction that changes what the absence means.
	Obtainable bool
	// Reason explains an Obtainable=false entry.
	Reason string
}

// TrustAssessment is the trust standing of the sources behind the set.
type TrustAssessment struct {
	Assessed bool
	// EffectiveSourceCount is the count after dependent sources are
	// clustered — three feeds off one root are one source.
	EffectiveSourceCount int
	// Verdicts records the independence verdict per source pair or
	// cluster, so an UNKNOWN is visible rather than absorbed.
	Verdicts map[string]independence.Verdict
	Concerns []string
}

// Authority is who was entitled to reach this conclusion.
type Authority struct {
	AuthorityID string
	Role        string
	// PolicyVersion pins the policy under which the authority acted, so
	// a later policy change does not retroactively re-authorize.
	PolicyVersion string
}

// DisclosureState is the two-dimensional disclosure standing carried
// from pkg/disclosure/access. It is deliberately two integers: a
// procedural level and a content level never collapse into one.
type DisclosureState struct {
	Procedural int
	Content    int
	// Privilege is the privilege status string as recorded by the
	// access package.
	Privilege string
	// ProtectiveOrderIDs names any orders restricting this conclusion.
	ProtectiveOrderIDs []string
}

// AIAccessState records what, if anything, a model contributed.
type AIAccessState struct {
	// ModelTouched is false for a conclusion no model participated in.
	ModelTouched bool
	// ContributionIDs pin the gateway contribution records.
	ContributionIDs []string
	// MaterialContribution marks an AI contribution that bore on the
	// conclusion rather than on formatting or search.
	MaterialContribution bool
	// HumanReviewerID must be present when the contribution is material.
	HumanReviewerID string
}

// Provenance is where the Proof Object itself came from.
type Provenance struct {
	GeneratedBy string
	// GeneratedAtTick is logical, not wall-clock: wall-clock time is an
	// attestation question, handled by pkg/platform/timestamp.
	GeneratedAtTick uint64
	// PipelineVersion identifies the code path that produced this.
	PipelineVersion string
	// InputHashes pin every input the generation consumed.
	InputHashes []string
}

// ExternalQualification is an outside party's standing on this
// conclusion. Its zero value is NotSought.
type ExternalQualification struct {
	Status ExternalStatus
	// QualifierID names the external party. Required when Status is
	// Qualified or Refused — an anonymous blessing is not a blessing.
	QualifierID string
	// Reference is the external party's own identifier for their act.
	Reference string
	// Note carries the party's stated basis or reservation.
	Note string
}

// Validate refuses an external qualification that claims a party
// without naming one.
func (e ExternalQualification) Validate() error {
	switch e.Status {
	case Qualified, Refused:
		if strings.TrimSpace(e.QualifierID) == "" {
			return fmt.Errorf("proof: %s requires a named QualifierID", e.Status)
		}
	}
	return nil
}

// --- The Proof Object ------------------------------------------------

var (
	ErrNoProposition        = errors.New("proof: a proof object requires a falsifiable proposition")
	ErrNoScope              = errors.New("proof: a proof object requires a scope")
	ErrNoJurisdiction       = errors.New("proof: a proof object requires a jurisdiction")
	ErrBadTimeWindow        = errors.New("proof: the time window is not ordered")
	ErrNoEvidence           = errors.New("proof: a proof object requires at least one pinned evidence version")
	ErrUnpinnedEvidence     = errors.New("proof: every evidence reference must pin a version and a content hash")
	ErrNoAuthority          = errors.New("proof: a proof object requires a named authority and policy version")
	ErrNoProvenance         = errors.New("proof: a proof object requires provenance")
	ErrNoReverseProof       = errors.New("proof: a proof object requires a reverse-proof requirement set")
	ErrUnreviewedAI         = errors.New("proof: a material AI contribution requires a named human reviewer")
	ErrLimitationsRequired  = errors.New("proof: a proof object must state its limitations")
	ErrStanceUnsupported    = errors.New("proof: a SUPPORT stance requires a qualification state that supports it")
	ErrSufficiencyUndecided = errors.New("proof: sufficiency must be determined before a proof object is sealed")
)

// Object is the VERIQO Proof Object: everything behind one conclusion.
//
// The twenty-three architectural components map onto these fields.
// Decision and Finding are deliberately absent: they are downstream of
// the Proof Object and produced by the pipeline in this package, never
// set by whoever assembles the evidence.
type Object struct {
	Proposition           Proposition
	Scope                 Scope
	Jurisdiction          Jurisdiction
	TimeWindow            TimeWindow
	EvidenceSet           []EvidenceRef
	Independence          []independence.Assessment
	Quality               Quality
	Contradictions        []Contradiction
	MissingEvidence       []MissingEvidence
	ReverseProof          reverseproof.RequirementSet
	ReverseProofGap       reverseproof.Gap
	NextBestEvidence      []string
	Trust                 TrustAssessment
	Qualification         state.Qualification
	Authority             Authority
	Disclosure            DisclosureState
	AIAccess              AIAccessState
	Limitations           []string
	Provenance            Provenance
	ReplayReference       string
	ExternalQualification ExternalQualification

	// Stance and Sufficiency are derived, not asserted. Seal computes
	// them from the qualification state and the gap analysis; a caller
	// setting them by hand is overridden.
	Stance      Stance
	Sufficiency Sufficiency

	// CanonicalHash is the JCS hash of every field above. It is
	// populated by Seal and excluded from its own computation.
	CanonicalHash string
	// Signature is applied after sealing, by a signer this package does
	// not own. An unsigned object is a valid proof object; it is simply
	// not an attested one.
	Signature string
}

// hashableView is the canonical projection hashed by Seal. It excludes
// CanonicalHash and Signature — a hash cannot cover itself, and a
// signature is applied to the hash rather than included in it.
func (o Object) hashableView() map[string]any {
	ev := make([]any, 0, len(o.EvidenceSet))
	for _, e := range o.EvidenceSet {
		ev = append(ev, map[string]any{
			"evidence_id": e.EvidenceID, "version_id": e.EvidenceVersionID,
			"sha256": e.SHA256, "source_id": e.SourceID,
		})
	}
	contra := make([]any, 0, len(o.Contradictions))
	for _, c := range o.Contradictions {
		between := append([]string(nil), c.Between...)
		sort.Strings(between)
		contra = append(contra, map[string]any{
			"id": c.ID, "between": toAny(between), "description": c.Description,
			"material": c.Material, "resolved": c.Resolved, "resolution": c.Resolution,
		})
	}
	missing := make([]any, 0, len(o.MissingEvidence))
	for _, m := range o.MissingEvidence {
		missing = append(missing, map[string]any{
			"condition_id": m.ConditionID, "description": m.Description,
			"obtainable": m.Obtainable, "reason": m.Reason,
		})
	}
	return map[string]any{
		"proposition": map[string]any{
			"id": o.Proposition.ID, "statement": o.Proposition.Statement,
			"subject_type": o.Proposition.SubjectType, "subject_id": o.Proposition.SubjectID,
		},
		"scope": map[string]any{
			"case_id": o.Scope.CaseID, "matter": o.Scope.Matter,
			"boundaries": toAny(sortedCopy(o.Scope.Boundaries)),
		},
		"jurisdiction": map[string]any{
			"code": o.Jurisdiction.Code, "forum": o.Jurisdiction.Forum,
			"governing_law": o.Jurisdiction.GoverningLaw,
		},
		"time_window":       map[string]any{"from": o.TimeWindow.FromTick, "to": o.TimeWindow.ToTick},
		"evidence_set":      ev,
		"quality":           map[string]any{"assessed": o.Quality.Assessed, "grade": o.Quality.Grade, "concerns": toAny(sortedCopy(o.Quality.Concerns))},
		"contradictions":    contra,
		"missing_evidence":  missing,
		"next_best":         toAny(sortedCopy(o.NextBestEvidence)),
		"effective_sources": o.Trust.EffectiveSourceCount,
		"qualification": map[string]any{
			"claim_id": o.Qualification.ClaimID, "state": string(o.Qualification.State),
			"policy_version": o.Qualification.PolicyVersion,
		},
		"authority": map[string]any{
			"authority_id": o.Authority.AuthorityID, "role": o.Authority.Role,
			"policy_version": o.Authority.PolicyVersion,
		},
		"disclosure": map[string]any{
			"procedural": o.Disclosure.Procedural, "content": o.Disclosure.Content,
			"privilege": o.Disclosure.Privilege,
			"orders":    toAny(sortedCopy(o.Disclosure.ProtectiveOrderIDs)),
		},
		"ai_access": map[string]any{
			"model_touched": o.AIAccess.ModelTouched,
			"contributions": toAny(sortedCopy(o.AIAccess.ContributionIDs)),
			"material":      o.AIAccess.MaterialContribution,
			"reviewer":      o.AIAccess.HumanReviewerID,
		},
		"limitations": toAny(sortedCopy(o.Limitations)),
		"provenance": map[string]any{
			"generated_by": o.Provenance.GeneratedBy, "tick": o.Provenance.GeneratedAtTick,
			"pipeline_version": o.Provenance.PipelineVersion,
			"input_hashes":     toAny(sortedCopy(o.Provenance.InputHashes)),
		},
		"replay_reference": o.ReplayReference,
		"external_qualification": map[string]any{
			"status": o.ExternalQualification.Status.String(),
			"id":     o.ExternalQualification.QualifierID,
			"ref":    o.ExternalQualification.Reference,
			"note":   o.ExternalQualification.Note,
		},
		"stance":      o.Stance.String(),
		"sufficiency": o.Sufficiency.String(),
	}
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

func toAny(s []string) []any {
	a := make([]any, 0, len(s))
	for _, v := range s {
		a = append(a, v)
	}
	return a
}

// Validate refuses a Proof Object that is structurally incomplete.
//
// It is deliberately strict about the components that make a conclusion
// reviewable. An object with no reverse-proof set, no limitations, or
// unpinned evidence is not a weaker proof object — it is one that cannot
// be checked, and this package will not produce one.
func (o Object) Validate() error {
	if strings.TrimSpace(o.Proposition.ID) == "" || strings.TrimSpace(o.Proposition.Statement) == "" {
		return ErrNoProposition
	}
	if strings.TrimSpace(o.Scope.CaseID) == "" {
		return ErrNoScope
	}
	if strings.TrimSpace(o.Jurisdiction.Code) == "" {
		return ErrNoJurisdiction
	}
	if !o.TimeWindow.Valid() {
		return ErrBadTimeWindow
	}
	if len(o.EvidenceSet) == 0 {
		return ErrNoEvidence
	}
	for _, e := range o.EvidenceSet {
		if strings.TrimSpace(e.EvidenceVersionID) == "" || strings.TrimSpace(e.SHA256) == "" {
			return fmt.Errorf("%w: %q", ErrUnpinnedEvidence, e.EvidenceID)
		}
	}
	if strings.TrimSpace(o.Authority.AuthorityID) == "" || strings.TrimSpace(o.Authority.PolicyVersion) == "" {
		return ErrNoAuthority
	}
	if strings.TrimSpace(o.Provenance.GeneratedBy) == "" || strings.TrimSpace(o.Provenance.PipelineVersion) == "" {
		return ErrNoProvenance
	}
	if strings.TrimSpace(o.ReverseProof.ClaimID) == "" {
		return ErrNoReverseProof
	}
	if len(o.Limitations) == 0 {
		return ErrLimitationsRequired
	}
	if o.AIAccess.MaterialContribution && strings.TrimSpace(o.AIAccess.HumanReviewerID) == "" {
		return ErrUnreviewedAI
	}
	if err := o.ExternalQualification.Validate(); err != nil {
		return err
	}
	return nil
}

// MaterialContradictions returns the unresolved contradictions that bear
// on the proposition. A proof object carrying these is not thereby
// wrong — but it cannot be sufficient.
func (o Object) MaterialContradictions() []Contradiction {
	var out []Contradiction
	for _, c := range o.Contradictions {
		if c.Material && !c.Resolved {
			out = append(out, c)
		}
	}
	return out
}

// UnobtainableEvidence returns the missing evidence that cannot be got.
// This is the set that turns an evidential gap into a permanent
// limitation rather than a next-best action.
func (o Object) UnobtainableEvidence() []MissingEvidence {
	var out []MissingEvidence
	for _, m := range o.MissingEvidence {
		if !m.Obtainable {
			out = append(out, m)
		}
	}
	return out
}

// deriveStance computes the stance from the qualification state rather
// than trusting whatever the caller set.
func (o Object) deriveStance() Stance {
	switch o.Qualification.State {
	case state.Supported, state.SupportedWithExceptions, state.Qualified,
		state.QualifiedWithDissent, state.SupportedBySingleHighAssuranceSource:
		return Support
	case state.Contradicted:
		return Contradict
	default:
		// INCONCLUSIVE, INSUFFICIENT_EVIDENCE, NOT_OBSERVABLE,
		// NOT_COLLECTABLE and the empty state all mean the same thing
		// for a proposition: we do not know.
		return Unknown
	}
}

// deriveSufficiency decides whether the object founds a finding.
//
// Sufficiency is conjunctive and fails closed. A Support stance is
// necessary but nowhere near enough: unresolved material contradictions,
// an unsatisfied reverse-proof set, an unassessed source set, or a
// material AI contribution nobody reviewed each defeat it.
func (o Object) deriveSufficiency(stance Stance) Sufficiency {
	if stance != Support {
		return Insufficient
	}
	if len(o.MaterialContradictions()) > 0 {
		return Insufficient
	}
	if !o.Trust.Assessed || o.Trust.EffectiveSourceCount < 1 {
		return Insufficient
	}
	if !o.Quality.Assessed {
		return Insufficient
	}
	if o.AIAccess.MaterialContribution && strings.TrimSpace(o.AIAccess.HumanReviewerID) == "" {
		return Insufficient
	}
	// The reverse proof must actually be complete. We defer to the EQF's
	// own judgement here rather than re-deriving it: reverseproof.Analyze
	// already knows which requirements are material and whether every
	// rival explanation was tested, and a second opinion computed in this
	// package would be a duplicate authority on the same question.
	if !o.ReverseProofGap.Complete {
		return Insufficient
	}
	return Sufficient
}

// Seal validates the object, derives its stance and sufficiency, and
// computes the canonical hash.
//
// Seal is the only way to obtain a hashed Proof Object. Stance and
// Sufficiency are overwritten with the derived values, so an author
// cannot declare a conclusion sufficient by writing the word.
func Seal(o Object) (Object, error) {
	if err := o.Validate(); err != nil {
		return Object{}, err
	}
	o.Stance = o.deriveStance()
	o.Sufficiency = o.deriveSufficiency(o.Stance)
	o.CanonicalHash = ""
	o.Signature = ""

	h, err := jcs.Hash(o.hashableView())
	if err != nil {
		return Object{}, fmt.Errorf("proof: canonical hash: %w", err)
	}
	o.CanonicalHash = h
	return o, nil
}

// VerifyHash recomputes the canonical hash and compares it. Any mutation
// of any hashed component breaks it.
func VerifyHash(o Object) error {
	claimed := o.CanonicalHash
	if strings.TrimSpace(claimed) == "" {
		return errors.New("proof: object carries no canonical hash")
	}
	bare := o
	bare.CanonicalHash = ""
	bare.Signature = ""
	h, err := jcs.Hash(bare.hashableView())
	if err != nil {
		return fmt.Errorf("proof: canonical hash: %w", err)
	}
	if h != claimed {
		return fmt.Errorf("proof: canonical hash mismatch: object has been altered since sealing")
	}
	return nil
}
