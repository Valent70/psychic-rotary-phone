// Package casefabric is VERIQO's one canonical case spine.
//
// Before this package the repository had an insurance case, a maritime
// investigation, a commodity case and a supply-chain case, each with its
// own identity, its own lifecycle and its own idea of what "resolved"
// means. Each was reasonable on its own. Together they meant VERIQO had
// no answer to "what is a case?", and a claim about cross-domain
// evidence could not be checked because there was nothing shared to
// check it against.
//
// The fabric is the shared answer:
//
//	Case Identity -> Case State -> Case Scope -> Case Evidence
//	 -> Case Hypotheses -> Case Claims -> Case Resolution -> Case Outcome
//
// A domain does not get its own case. It gets a projection: its own
// richer vocabulary mapped onto these canonical phases, registered here,
// so an insurance PAYMENT_AUTHORIZED and a maritime EVIDENCE_SECURED are
// both discoverably positions in one lifecycle. A domain state that maps
// to nothing is refused — that is Law 3, no domain may bypass the
// fabric, enforced rather than asserted.
//
// Anti-duplication: this package deliberately reuses pkg/proof for
// scope, jurisdiction, time windows, propositions, findings and
// decisions. It does not define a second Finding, a second authority
// check or a second adjudication guard, and it does not own a ledger.
// It composes what already exists into one case.
package casefabric

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/proof"
)

// Phase is the canonical, domain-neutral position of a case.
//
// The set is deliberately small. A phase is not a workflow step; it is
// the answer to "how far has this case got in establishing what it is
// about?", and every domain's much longer state list projects onto it.
type Phase string

const (
	// PhaseOpened: the case exists and has an identity. Nothing is
	// scoped yet.
	PhaseOpened Phase = "OPENED"
	// PhaseScoped: the matter, jurisdiction and time window are fixed.
	// Evidence gathered outside them is out of scope by construction.
	PhaseScoped Phase = "SCOPED"
	// PhaseEvidenceGathering: evidence is being acquired and pinned.
	PhaseEvidenceGathering Phase = "EVIDENCE_GATHERING"
	// PhaseHypothesesFormed: rival explanations are on the record, so
	// the case can be argued against rather than only for.
	PhaseHypothesesFormed Phase = "HYPOTHESES_FORMED"
	// PhaseUnderQualification: claims are being qualified through the
	// EQF and sealed into proof objects.
	PhaseUnderQualification Phase = "UNDER_QUALIFICATION"
	// PhaseResolved: every material claim carries a sealed proof object
	// and the case has an outcome.
	PhaseResolved Phase = "RESOLVED"
	// PhaseSuspended: work has stopped for a stated reason — a legal
	// hold, a missing party, an unavailable source.
	PhaseSuspended Phase = "SUSPENDED"
	// PhaseClosed: terminal.
	PhaseClosed Phase = "CLOSED"
	// PhaseReopened: a closed case reopened on new evidence. It is a
	// distinct phase, not a silent return to an earlier one, because
	// what happened before reopening remains part of the record.
	PhaseReopened Phase = "REOPENED"
)

// Phases returns the canonical phases in lifecycle order.
func Phases() []Phase {
	return []Phase{PhaseOpened, PhaseScoped, PhaseEvidenceGathering, PhaseHypothesesFormed,
		PhaseUnderQualification, PhaseResolved, PhaseSuspended, PhaseClosed, PhaseReopened}
}

// allowedTransitions is the canonical lifecycle as data rather than
// as a chain of if-statements, so it can be read and audited whole.
var allowedTransitions = map[Phase][]Phase{
	PhaseOpened:             {PhaseScoped, PhaseSuspended, PhaseClosed},
	PhaseScoped:             {PhaseEvidenceGathering, PhaseSuspended, PhaseClosed},
	PhaseEvidenceGathering:  {PhaseHypothesesFormed, PhaseSuspended, PhaseClosed},
	PhaseHypothesesFormed:   {PhaseUnderQualification, PhaseEvidenceGathering, PhaseSuspended, PhaseClosed},
	PhaseUnderQualification: {PhaseResolved, PhaseEvidenceGathering, PhaseSuspended, PhaseClosed},
	PhaseResolved:           {PhaseClosed, PhaseSuspended},
	PhaseSuspended:          {PhaseScoped, PhaseEvidenceGathering, PhaseHypothesesFormed, PhaseUnderQualification, PhaseClosed},
	PhaseClosed:             {PhaseReopened},
	PhaseReopened:           {PhaseEvidenceGathering, PhaseUnderQualification, PhaseClosed},
}

// CanTransition reports whether a phase change is permitted.
func CanTransition(from, to Phase) bool {
	for _, p := range allowedTransitions[from] {
		if p == to {
			return true
		}
	}
	return false
}

var (
	ErrNoCaseID          = errors.New("casefabric: a case requires an identity")
	ErrNoTenant          = errors.New("casefabric: a case requires a tenant")
	ErrNoDomain          = errors.New("casefabric: a case requires a registered domain")
	ErrUnknownDomain     = errors.New("casefabric: the domain is not registered with the fabric")
	ErrUnmappedState     = errors.New("casefabric: the domain state maps to no canonical phase")
	ErrBadTransition     = errors.New("casefabric: the phase transition is not permitted")
	ErrNotScoped         = errors.New("casefabric: the case has no scope")
	ErrUnpinnedEvidence  = errors.New("casefabric: case evidence must pin a version and a content hash")
	ErrNoHypotheses      = errors.New("casefabric: qualification requires at least one rival hypothesis on the record")
	ErrClaimUnproven     = errors.New("casefabric: a material claim carries no sealed proof object")
	ErrAdjudication      = errors.New("casefabric: VERIQO does not adjudicate: an outcome may not name a prevailing party")
	ErrNoOutcome         = errors.New("casefabric: resolution requires a stated outcome")
	ErrDuplicateClaim    = errors.New("casefabric: the claim is already registered")
	ErrUnknownClaim      = errors.New("casefabric: no such claim in this case")
	ErrProofWrongCase    = errors.New("casefabric: the proof object belongs to another case")
	ErrProofWrongClaim   = errors.New("casefabric: the proof object proves another proposition")
	ErrSuspendNeedsCause = errors.New("casefabric: suspension requires a stated cause")
)

// --- Domain projection ------------------------------------------------

// Projection is how a domain joins the fabric: it declares its own state
// vocabulary and how each of its states sits in the canonical lifecycle.
//
// This is the anti-fragmentation mechanism. A domain keeps its own
// richer language — PAYMENT_AUTHORIZED means something specific to
// insurance and should not be flattened away — but every one of its
// states must land somewhere canonical, and the fabric refuses to
// register a domain that leaves states unmapped.
type Projection struct {
	// Domain is the domain's canonical name: "insurance", "maritime",
	// "commodity", "supplychain", "tradefinance", "dispute".
	Domain string
	// StateToPhase maps every state in the domain's vocabulary onto a
	// canonical phase.
	StateToPhase map[string]Phase
	// CanonicalPackage names the package that owns the domain's case
	// implementation, so the mapping is traceable to code.
	CanonicalPackage string
}

// Validate refuses a projection that would let a domain drift.
func (p Projection) Validate() error {
	if strings.TrimSpace(p.Domain) == "" {
		return ErrNoDomain
	}
	if len(p.StateToPhase) == 0 {
		return fmt.Errorf("casefabric: domain %q maps no states onto the fabric", p.Domain)
	}
	if strings.TrimSpace(p.CanonicalPackage) == "" {
		return fmt.Errorf("casefabric: domain %q names no canonical package", p.Domain)
	}
	valid := map[Phase]bool{}
	for _, ph := range Phases() {
		valid[ph] = true
	}
	for st, ph := range p.StateToPhase {
		if !valid[ph] {
			return fmt.Errorf("casefabric: domain %q maps state %q to unknown phase %q", p.Domain, st, ph)
		}
	}
	return nil
}

// Phase resolves a domain state to its canonical phase.
func (p Projection) Phase(domainState string) (Phase, error) {
	ph, ok := p.StateToPhase[domainState]
	if !ok {
		return "", fmt.Errorf("%w: domain %q state %q", ErrUnmappedState, p.Domain, domainState)
	}
	return ph, nil
}

// registry holds the registered domain projections. It is package-level
// because the set of domains is a property of the deployment, not of any
// one case.
var registry = struct {
	mu sync.RWMutex
	m  map[string]Projection
}{m: map[string]Projection{}}

// Register adds a domain projection. Registering the same domain twice
// replaces the mapping, which is how a domain evolves its vocabulary.
func Register(p Projection) error {
	if err := p.Validate(); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.m[p.Domain] = p
	return nil
}

// Lookup returns a registered projection.
func Lookup(domain string) (Projection, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	p, ok := registry.m[domain]
	return p, ok
}

// RegisteredDomains lists the domains currently on the fabric, sorted.
func RegisteredDomains() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]string, 0, len(registry.m))
	for d := range registry.m {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// --- Case elements ----------------------------------------------------

// Identity is what makes a case one case across every domain and
// tenant that touches it.
type Identity struct {
	CaseID   string
	TenantID string
	// Domain is the registered domain that opened the case. A case
	// opened in insurance and later joined by maritime keeps its opening
	// domain; participation is recorded separately.
	Domain string
	// ExternalRefs are the parties' own identifiers — a claim number, a
	// bill of lading, a policy number — kept so a case can be found by
	// what people actually call it.
	ExternalRefs map[string]string
}

// Mission is what the case is for: the question the case exists to
// answer, stated before the answer is known.
type Mission struct {
	Statement string
	// Intent names the operational purpose — "quantify the loss",
	// "establish the cause", "support the arbitration" — which governs
	// what evidence is in scope.
	Intent string
	// SetBy and SetAtTick record who fixed the mission, because a
	// mission rewritten after the evidence arrives is a different case.
	SetBy     string
	SetAtTick uint64
}

// Hypothesis is a rival explanation on the case record.
//
// The fabric requires at least one before qualification can begin. A
// case with a single explanation has not been investigated; it has been
// narrated.
type Hypothesis struct {
	ID          string
	Description string
	// Tested records that the hypothesis was actually evaluated, not
	// merely listed.
	Tested bool
	// Outcome is the result of testing, in the tester's own words.
	Outcome string
}

// Claim is a proposition the case must establish. Each claim is
// eventually carried by a sealed proof object or it is not established.
type Claim struct {
	ID          string
	Proposition proof.Proposition
	// Material marks a claim the case turns on. An immaterial claim may
	// remain unproven without blocking resolution; a material one may
	// not.
	Material bool
	// ProofHash is the canonical hash of the sealed proof object that
	// carries this claim. Empty until one is attached.
	ProofHash string
	// Stance and Sufficiency are copied from the attached proof object
	// so a case summary does not have to re-open every object.
	Stance      proof.Stance
	Sufficiency proof.Sufficiency
}

// Proven reports whether the claim is carried by a sufficient proof.
func (c Claim) Proven() bool {
	return c.ProofHash != "" && c.Sufficiency == proof.Sufficient && c.Stance == proof.Support
}

// EvidenceRef pins one piece of case evidence at a version.
type EvidenceRef = proof.EvidenceRef

// TimelineEntry is one thing that happened in the case. The timeline is
// append-only: entries are never edited, because the sequence of what
// was known when is itself evidence.
type TimelineEntry struct {
	SequenceNo  uint64
	Tick        uint64
	Actor       string
	Kind        string
	Description string
	// Phase records the canonical phase at the time of the entry.
	Phase Phase
	// EntryHash chains to the previous entry.
	PriorHash string
	EntryHash string
}

// Outcome is how a case ended. It is deliberately operational: what
// VERIQO did and what it established, never who should win.
type Outcome struct {
	// Disposition is the operational result: "evidence_package_delivered",
	// "referred_to_tribunal", "claim_withdrawn", "no_further_action".
	Disposition string
	// Summary states what the case established, in the fabric's terms.
	Summary string
	// EstablishedClaimIDs are the claims carried by sufficient proof.
	EstablishedClaimIDs []string
	// UnestablishedClaimIDs are the claims that were not, which is
	// reported with equal prominence.
	UnestablishedClaimIDs []string
	// Limitations carries forward the limits of every proof object the
	// outcome rests on.
	Limitations []string
	AtTick      uint64
}

// Validate refuses an outcome that adjudicates.
//
// It reuses proof.ProhibitedDecisionFields rather than keeping a second
// list: one place decides what adjudication looks like.
func (o Outcome) Validate() error {
	if strings.TrimSpace(o.Disposition) == "" {
		return ErrNoOutcome
	}
	haystack := strings.ToLower(o.Disposition + " " + o.Summary)
	// Split on anything that is not a word character. The underscore
	// counts as one: the prohibited terms include "liable_party" and
	// "prevailing_party", and splitting on underscore would break them
	// into halves that match nothing -- which is exactly how
	// "respondent found liable_party" slipped past an earlier version of
	// this check.
	words := strings.FieldsFunc(haystack, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9') && r != '_'
	})
	for _, banned := range proof.ProhibitedDecisionFields() {
		for _, w := range words {
			if w == banned {
				return fmt.Errorf("%w: %q", ErrAdjudication, banned)
			}
		}
	}
	return nil
}
