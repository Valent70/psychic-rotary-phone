// Package ai holds the classification every AI-produced artefact
// carries, and the gate it must pass to rise.
//
// # Law 7, as a ladder
//
//	AI boleh: extract, classify, propose, link, hypothesize, summarize
//	AI tidak boleh sendirian: qualify fact, approve merge,
//	                          declare fraud, declare liability,
//	                          declare coverage
//
// The ladder makes that operational:
//
//	DRAFT                  a model produced it; nobody has looked
//	ASSISTED               a person worked with it and it is theirs
//	REVIEW_REQUIRED        it is proposed for use and needs a reviewer
//	QUALIFICATION_ELIGIBLE a reviewer accepted it; it may enter the
//	                       qualification ladder
//	QUALIFIED              it went through qualification
//
// # The rule that carries the law
//
// An artefact may rise by at most ONE level per act, and the act that
// raises it must be performed by a principal who is not the producer.
// A model cannot promote its own output, and an agent cannot promote
// another agent's -- which is the loophole a permission check at each
// site would leave open.
//
// # Automated qualification is possible and narrow
//
// The specification allows it for a low-risk class under explicit
// policy. That is modelled rather than banned, because banning it
// outright would push the capability into somewhere unaudited. It
// requires a named policy, a declared risk class, and it is recorded
// as automated so a reader can tell.
package ai

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
)

var (
	ErrUnknownLevel       = errors.New("ai: unknown assurance level")
	ErrSkippedLevel       = errors.New("ai: an artefact may rise by at most one level per act")
	ErrSelfPromotion      = errors.New("ai: a producer may not promote its own output")
	ErrAutomatedPromotion = errors.New("ai: an automated principal may not perform this promotion")
	ErrNoPolicy           = errors.New("ai: automated qualification requires a named policy and risk class")
	ErrDemotionOnly       = errors.New("ai: this transition is a demotion and needs no promotion path")
	ErrNoProducer         = errors.New("ai: an AI artefact must name what produced it")
	ErrForbiddenAct       = errors.New("ai: this act is not available to an automated principal")
)

// Level is the assurance ladder for AI-produced material.
type Level int

const (
	// Draft is the zero value: a model produced it and nobody has
	// looked. It is the zero value deliberately -- an artefact whose
	// level nobody set must not read as reviewed.
	Draft Level = iota
	Assisted
	ReviewRequired
	QualificationEligible
	Qualified
)

var levelNames = map[Level]string{
	Draft: "DRAFT", Assisted: "ASSISTED", ReviewRequired: "REVIEW_REQUIRED",
	QualificationEligible: "QUALIFICATION_ELIGIBLE", Qualified: "QUALIFIED",
}

func Levels() []Level {
	return []Level{Draft, Assisted, ReviewRequired, QualificationEligible, Qualified}
}

func (l Level) String() string {
	if n, ok := levelNames[l]; ok {
		return n
	}
	return fmt.Sprintf("Level(%d)", int(l))
}

func (l Level) Valid() bool { _, ok := levelNames[l]; return ok }

// RequiresHuman reports whether reaching this level needs a person.
// Everything above ASSISTED does.
func (l Level) RequiresHuman() bool { return l >= ReviewRequired }

// MayFoundAConclusion reports whether material at this level may
// underwrite a finding. Only QUALIFIED may.
func (l Level) MayFoundAConclusion() bool { return l == Qualified }

// Act is what an AI component did. The permitted set is Law 7's first
// list; the forbidden set is its second.
type Act string

const (
	Extract     Act = "EXTRACT"
	Classify    Act = "CLASSIFY"
	Propose     Act = "PROPOSE"
	Link        Act = "LINK"
	Hypothesize Act = "HYPOTHESIZE"
	Summarize   Act = "SUMMARIZE"

	// The forbidden acts, named so they can be refused explicitly
	// rather than by omission.
	QualifyFact      Act = "QUALIFY_FACT"
	ApproveMerge     Act = "APPROVE_MERGE"
	DeclareFraud     Act = "DECLARE_FRAUD"
	DeclareLiability Act = "DECLARE_LIABILITY"
	DeclareCoverage  Act = "DECLARE_COVERAGE"
)

// PermittedActs are what an automated principal may do alone.
func PermittedActs() []Act {
	return []Act{Extract, Classify, Propose, Link, Hypothesize, Summarize}
}

// ForbiddenActs are what it may never do alone.
func ForbiddenActs() []Act {
	return []Act{QualifyFact, ApproveMerge, DeclareFraud, DeclareLiability, DeclareCoverage}
}

func (a Act) Permitted() bool {
	for _, x := range PermittedActs() {
		if x == a {
			return true
		}
	}
	return false
}

func (a Act) Forbidden() bool {
	for _, x := range ForbiddenActs() {
		if x == a {
			return true
		}
	}
	return false
}

// CheckAct is Law 7 as a function. It is called before an automated
// principal does anything, and it refuses the second list outright --
// not because a policy says so, but because the act is not available.
func CheckAct(p identity.Principal, a Act) error {
	if !a.Permitted() && !a.Forbidden() {
		return fmt.Errorf("ai: unknown act %q", a)
	}
	if !p.Kind.IsAutomated() {
		return nil
	}
	if a.Forbidden() {
		return fmt.Errorf("%w: %s is %s and may not %s alone; it may %s",
			ErrForbiddenAct, p.ID, p.Kind, a,
			strings.Join(actNames(PermittedActs()), ", "))
	}
	return nil
}

func actNames(as []Act) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = string(a)
	}
	return out
}

// Producer identifies what made an artefact.
type Producer struct {
	PrincipalID  contract.ID `json:"principal_id"`
	ModelID      string      `json:"model_id,omitempty"`
	ModelVersion string      `json:"model_version,omitempty"`
	PromptHash   string      `json:"prompt_hash,omitempty"`
	Automated    bool        `json:"automated"`
}

func (p Producer) Validate() error {
	if p.PrincipalID == "" {
		return ErrNoProducer
	}
	if p.ModelID != "" && p.ModelVersion == "" {
		return fmt.Errorf("ai: model %s is named with no version", p.ModelID)
	}
	return nil
}

// Promotion is one recorded rise in level.
type Promotion struct {
	From, To Level       `json:"from,to"`
	By       contract.ID `json:"by"`
	At       time.Time   `json:"at"`
	Reason   string      `json:"reason"`
	// Automated marks a promotion performed without a human, which is
	// permitted only under a named policy for a declared risk class.
	Automated  bool   `json:"automated,omitempty"`
	PolicyName string `json:"policy_name,omitempty"`
	RiskClass  string `json:"risk_class,omitempty"`
}

// Artefact is an AI-produced item with its level and history.
type Artefact struct {
	ID       contract.ID `json:"id"`
	TenantID string      `json:"tenant_id"`
	Act      Act         `json:"act"`
	Producer Producer    `json:"producer"`

	Level   Level       `json:"level"`
	History []Promotion `json:"history,omitempty"`

	// Content is the digest of what was produced; the material itself
	// lives in the evidence fabric.
	ContentHash string `json:"content_hash"`
}

func (a Artefact) Validate() error {
	if a.ID == "" {
		return fmt.Errorf("%w: artefact has no id", contract.ErrMalformedID)
	}
	if err := a.Producer.Validate(); err != nil {
		return err
	}
	if !a.Level.Valid() {
		return fmt.Errorf("%w: %v", ErrUnknownLevel, a.Level)
	}
	if !a.Act.Permitted() {
		return fmt.Errorf("ai: artefact %s records the act %s, which an AI component may "+
			"not perform", a.ID, a.Act)
	}
	if strings.TrimSpace(a.ContentHash) == "" {
		return fmt.Errorf("ai: artefact %s has no content hash", a.ID)
	}
	// The history must actually lead to the current level.
	want := Draft
	for _, p := range a.History {
		if p.From != want {
			return fmt.Errorf("ai: artefact %s has a promotion from %s where the history "+
				"is at %s", a.ID, p.From, want)
		}
		want = p.To
	}
	if want != a.Level {
		return fmt.Errorf("ai: artefact %s is at %s and its history ends at %s",
			a.ID, a.Level, want)
	}
	return nil
}

// AutomatedPolicy permits a promotion with no human, for a declared
// low-risk class.
type AutomatedPolicy struct {
	Name string
	// RiskClass is what the policy covers. A policy that covers
	// everything is not a low-risk exception.
	RiskClass string
	// MaxLevel bounds what automation may reach.
	MaxLevel Level
	// Version ties the permission to a policy revision.
	Version contract.Version
}

func (p AutomatedPolicy) Validate() error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.RiskClass) == "" {
		return fmt.Errorf("%w: name=%q class=%q", ErrNoPolicy, p.Name, p.RiskClass)
	}
	if p.Version.Zero() {
		return fmt.Errorf("%w: the policy is unversioned", ErrNoPolicy)
	}
	if p.MaxLevel > QualificationEligible {
		return fmt.Errorf("%w: %s permits automation up to %s; QUALIFIED is not reachable "+
			"without a person under any policy", ErrNoPolicy, p.Name, p.MaxLevel)
	}
	return nil
}

// Promote raises an artefact by one level.
//
// The rules, in the order they are checked:
//
//  1. one level at a time -- a jump from DRAFT to QUALIFIED skips
//     every check the intermediate levels exist to perform;
//  2. not by the producer -- a model cannot promote its own output,
//     and an agent cannot promote its own;
//  3. an automated promoter needs a policy, a risk class, and cannot
//     reach QUALIFIED at all.
func Promote(a Artefact, to Level, by identity.Principal, at time.Time, reason string,
	policy *AutomatedPolicy) (Artefact, error) {

	if err := a.Validate(); err != nil {
		return Artefact{}, err
	}
	if !to.Valid() {
		return Artefact{}, fmt.Errorf("%w: %v", ErrUnknownLevel, to)
	}
	if to <= a.Level {
		return Artefact{}, fmt.Errorf("%w: %s -> %s", ErrDemotionOnly, a.Level, to)
	}
	if to != a.Level+1 {
		return Artefact{}, fmt.Errorf("%w: %s -> %s skips %s",
			ErrSkippedLevel, a.Level, to, a.Level+1)
	}
	if strings.TrimSpace(reason) == "" {
		return Artefact{}, errors.New("ai: a promotion must state why")
	}
	if at.IsZero() {
		return Artefact{}, errors.New("ai: a promotion needs an instant")
	}
	if by.TenantID != a.TenantID {
		return Artefact{}, fmt.Errorf("%w: promoter is in %s", contract.ErrCrossTenant, by.TenantID)
	}

	// A producer cannot promote its own output. This covers the direct
	// case and the delegated one: an agent launched by the producing
	// analyst is the producing analyst for this purpose.
	if by.ID == a.Producer.PrincipalID {
		return Artefact{}, fmt.Errorf("%w: %s produced %s", ErrSelfPromotion, by.ID, a.ID)
	}
	if by.OnBehalfOf != nil && *by.OnBehalfOf == a.Producer.PrincipalID {
		return Artefact{}, fmt.Errorf("%w: %s acts on behalf of the producer %s",
			ErrSelfPromotion, by.ID, a.Producer.PrincipalID)
	}

	p := Promotion{From: a.Level, To: to, By: by.ID, At: at, Reason: reason}

	if by.Kind.IsAutomated() {
		if policy == nil {
			return Artefact{}, fmt.Errorf("%w: %s is %s and no automated-qualification "+
				"policy was supplied", ErrAutomatedPromotion, by.ID, by.Kind)
		}
		if err := policy.Validate(); err != nil {
			return Artefact{}, err
		}
		if to > policy.MaxLevel {
			return Artefact{}, fmt.Errorf("%w: policy %s permits automation to %s, not %s",
				ErrAutomatedPromotion, policy.Name, policy.MaxLevel, to)
		}
		if to == Qualified {
			return Artefact{}, fmt.Errorf("%w: QUALIFIED is not reachable without a person",
				ErrAutomatedPromotion)
		}
		p.Automated = true
		p.PolicyName = policy.Name
		p.RiskClass = policy.RiskClass
	}

	out := a
	out.Level = to
	out.History = append(append([]Promotion(nil), a.History...), p)
	return out, nil
}

// Demote lowers an artefact. It needs no promotion path and no
// separation: concluding less about material is always safe.
func Demote(a Artefact, to Level, by contract.ID, at time.Time, reason string) (Artefact, error) {
	if !to.Valid() {
		return Artefact{}, fmt.Errorf("%w: %v", ErrUnknownLevel, to)
	}
	if to >= a.Level {
		return Artefact{}, fmt.Errorf("ai: %s -> %s is not a demotion", a.Level, to)
	}
	out := a
	out.Level = to
	out.History = append(append([]Promotion(nil), a.History...),
		Promotion{From: a.Level, To: to, By: by, At: at, Reason: reason})
	return out, nil
}

// AutomatedPromotions returns the rises performed without a human, so
// a reader can tell which parts of an artefact's standing rest on a
// policy rather than on somebody looking.
func (a Artefact) AutomatedPromotions() []Promotion {
	var out []Promotion
	for _, p := range a.History {
		if p.Automated {
			out = append(out, p)
		}
	}
	return out
}

// Provenance renders the artefact's standing for a reader.
func (a Artefact) Provenance() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] produced by %s", a.ID, a.Level, a.Producer.PrincipalID)
	if a.Producer.ModelID != "" {
		fmt.Fprintf(&b, " using %s@%s", a.Producer.ModelID, a.Producer.ModelVersion)
	}
	b.WriteString("\n")
	for _, p := range a.History {
		mark := ""
		if p.Automated {
			mark = fmt.Sprintf(" [AUTOMATED under %s / %s]", p.PolicyName, p.RiskClass)
		}
		fmt.Fprintf(&b, "  %s -> %s by %s at %s: %s%s\n", p.From, p.To, p.By,
			p.At.UTC().Format(time.RFC3339), p.Reason, mark)
	}
	if n := len(a.AutomatedPromotions()); n > 0 {
		fmt.Fprintf(&b, "  %d of %d promotion(s) were automated: no person examined this "+
			"material at those steps\n", n, len(a.History))
	}
	if !a.Level.MayFoundAConclusion() {
		fmt.Fprintf(&b, "  this artefact is %s and may not found a conclusion\n", a.Level)
	}
	return b.String()
}

// SortArtefacts orders deterministically for rendering.
func SortArtefacts(as []Artefact) {
	sort.Slice(as, func(i, j int) bool { return as[i].ID < as[j].ID })
}
