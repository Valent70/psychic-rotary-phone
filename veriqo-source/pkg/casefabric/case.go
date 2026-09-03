package casefabric

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/proof"
)

// Case is one case on the fabric.
//
// Every mutation goes through a method that records a timeline entry, so
// the case's history is a by-product of using it rather than something a
// caller must remember to write. The zero Case is not usable: Open is
// the only constructor.
type Case struct {
	mu sync.RWMutex

	identity Identity
	phase    Phase
	mission  Mission
	scope    proof.Scope
	juris    proof.Jurisdiction
	window   proof.TimeWindow

	evidence   []EvidenceRef
	hypotheses []Hypothesis
	claims     map[string]*Claim
	claimOrder []string

	timeline []TimelineEntry
	outcome  *Outcome
}

// Open starts a case in the canonical PhaseOpened.
//
// The domain must already be registered. That ordering is deliberate:
// a case cannot exist in a domain the fabric does not know about, which
// is how "no domain may bypass the fabric" stops being a slogan.
func Open(id Identity, openedBy string, tick uint64) (*Case, error) {
	if strings.TrimSpace(id.CaseID) == "" {
		return nil, ErrNoCaseID
	}
	if strings.TrimSpace(id.TenantID) == "" {
		return nil, ErrNoTenant
	}
	if strings.TrimSpace(id.Domain) == "" {
		return nil, ErrNoDomain
	}
	if _, ok := Lookup(id.Domain); !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDomain, id.Domain)
	}

	refs := make(map[string]string, len(id.ExternalRefs))
	for k, v := range id.ExternalRefs {
		refs[k] = v
	}
	id.ExternalRefs = refs

	c := &Case{identity: id, phase: PhaseOpened, claims: map[string]*Claim{}}
	c.appendLocked(tick, openedBy, "case_opened", "case "+id.CaseID+" opened in domain "+id.Domain)
	return c, nil
}

// --- Accessors (all return copies) ------------------------------------

func (c *Case) Identity() Identity {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id := c.identity
	refs := make(map[string]string, len(id.ExternalRefs))
	for k, v := range id.ExternalRefs {
		refs[k] = v
	}
	id.ExternalRefs = refs
	return id
}

func (c *Case) Phase() Phase {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.phase
}

func (c *Case) Mission() Mission {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mission
}

func (c *Case) Scope() (proof.Scope, proof.Jurisdiction, proof.TimeWindow) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.scope
	s.Boundaries = append([]string(nil), c.scope.Boundaries...)
	return s, c.juris, c.window
}

func (c *Case) Evidence() []EvidenceRef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]EvidenceRef(nil), c.evidence...)
}

func (c *Case) Hypotheses() []Hypothesis {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Hypothesis(nil), c.hypotheses...)
}

func (c *Case) Claims() []Claim {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Claim, 0, len(c.claimOrder))
	for _, id := range c.claimOrder {
		out = append(out, *c.claims[id])
	}
	return out
}

func (c *Case) Timeline() []TimelineEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]TimelineEntry(nil), c.timeline...)
}

// Outcome returns the case outcome, or false if the case has none.
func (c *Case) Outcome() (Outcome, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.outcome == nil {
		return Outcome{}, false
	}
	o := *c.outcome
	o.EstablishedClaimIDs = append([]string(nil), c.outcome.EstablishedClaimIDs...)
	o.UnestablishedClaimIDs = append([]string(nil), c.outcome.UnestablishedClaimIDs...)
	o.Limitations = append([]string(nil), c.outcome.Limitations...)
	return o, true
}

// --- Timeline ---------------------------------------------------------

// appendLocked writes one hash-chained timeline entry. Callers hold the
// write lock, or are constructing the case.
func (c *Case) appendLocked(tick uint64, actor, kind, description string) {
	prior := ""
	if n := len(c.timeline); n > 0 {
		prior = c.timeline[n-1].EntryHash
	}
	e := TimelineEntry{
		SequenceNo: uint64(len(c.timeline)), Tick: tick, Actor: actor,
		Kind: kind, Description: description, Phase: c.phase, PriorHash: prior,
	}
	// A timeline entry hash cannot fail on these field types; MustHash
	// keeps the append path from returning an error nothing can trigger.
	e.EntryHash = jcs.MustHash(map[string]any{
		"seq": e.SequenceNo, "tick": e.Tick, "actor": e.Actor, "kind": e.Kind,
		"description": e.Description, "phase": string(e.Phase), "prior": e.PriorHash,
	})
	c.timeline = append(c.timeline, e)
}

// VerifyTimeline recomputes the chain. Any edited or reordered entry
// breaks it.
func (c *Case) VerifyTimeline() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i, e := range c.timeline {
		want := jcs.MustHash(map[string]any{
			"seq": e.SequenceNo, "tick": e.Tick, "actor": e.Actor, "kind": e.Kind,
			"description": e.Description, "phase": string(e.Phase), "prior": e.PriorHash,
		})
		if want != e.EntryHash {
			return fmt.Errorf("casefabric: timeline entry %d has been altered", i)
		}
		if i == 0 {
			continue
		}
		if e.PriorHash != c.timeline[i-1].EntryHash || e.SequenceNo != c.timeline[i-1].SequenceNo+1 {
			return fmt.Errorf("casefabric: timeline entry %d does not follow its predecessor", i)
		}
	}
	return nil
}

// --- Lifecycle --------------------------------------------------------

// SetScope fixes what the case is about and moves it to PhaseScoped.
//
// Scope is set once per phase entry rather than continuously, because a
// scope that moves with the evidence is not a scope.
func (c *Case) SetScope(s proof.Scope, j proof.Jurisdiction, w proof.TimeWindow, m Mission, by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.TrimSpace(s.CaseID) == "" {
		s.CaseID = c.identity.CaseID
	}
	if s.CaseID != c.identity.CaseID {
		return fmt.Errorf("casefabric: scope names case %q, this is case %q", s.CaseID, c.identity.CaseID)
	}
	if strings.TrimSpace(j.Code) == "" {
		return fmt.Errorf("%w: no jurisdiction", ErrNotScoped)
	}
	if !w.Valid() {
		return fmt.Errorf("%w: the time window is not ordered", ErrNotScoped)
	}
	if strings.TrimSpace(m.Statement) == "" {
		return fmt.Errorf("%w: no mission statement", ErrNotScoped)
	}
	if c.phase != PhaseOpened && c.phase != PhaseSuspended {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseScoped)
	}

	c.scope, c.juris, c.window, c.mission = s, j, w, m
	c.phase = PhaseScoped
	c.appendLocked(tick, by, "scope_set", "scoped to "+m.Statement+" in "+j.Code)
	return nil
}

// AddEvidence pins evidence into the case and enters
// PhaseEvidenceGathering from PhaseScoped.
func (c *Case) AddEvidence(refs []EvidenceRef, by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Law 7: no post-resolution epistemic mutation. A resolved case's
	// evidential picture is fixed; changing it afterwards would alter
	// the basis of a decision already taken. Reopening is the lawful
	// route, and it clears the outcome first.
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "adding evidence")
	}
	if c.phase == PhaseOpened {
		return fmt.Errorf("%w: evidence cannot be added before the case is scoped", ErrNotScoped)
	}
	if c.phase == PhaseClosed {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseEvidenceGathering)
	}
	for _, r := range refs {
		if strings.TrimSpace(r.EvidenceVersionID) == "" || strings.TrimSpace(r.SHA256) == "" {
			return fmt.Errorf("%w: %q", ErrUnpinnedEvidence, r.EvidenceID)
		}
	}
	c.evidence = append(c.evidence, refs...)
	if c.phase == PhaseScoped {
		c.phase = PhaseEvidenceGathering
	}
	c.appendLocked(tick, by, "evidence_added", fmt.Sprintf("%d evidence version(s) pinned", len(refs)))
	return nil
}

// AddHypothesis records a rival explanation and, from
// PhaseEvidenceGathering, enters PhaseHypothesesFormed.
func (c *Case) AddHypothesis(h Hypothesis, by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Law 7: no post-resolution epistemic mutation. A resolved case's
	// evidential picture is fixed; changing it afterwards would alter
	// the basis of a decision already taken. Reopening is the lawful
	// route, and it clears the outcome first.
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "adding a hypothesis")
	}
	if strings.TrimSpace(h.ID) == "" || strings.TrimSpace(h.Description) == "" {
		return fmt.Errorf("casefabric: a hypothesis requires an id and a description")
	}
	for _, existing := range c.hypotheses {
		if existing.ID == h.ID {
			return fmt.Errorf("casefabric: hypothesis %q is already on the record", h.ID)
		}
	}
	if c.phase == PhaseOpened || c.phase == PhaseScoped || c.phase == PhaseClosed {
		return fmt.Errorf("%w: cannot form hypotheses in %s", ErrBadTransition, c.phase)
	}
	c.hypotheses = append(c.hypotheses, h)
	if c.phase == PhaseEvidenceGathering {
		c.phase = PhaseHypothesesFormed
	}
	c.appendLocked(tick, by, "hypothesis_added", h.ID+": "+h.Description)
	return nil
}

// TestHypothesis records that a hypothesis was actually evaluated.
func (c *Case) TestHypothesis(id, outcome, by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "testing a hypothesis")
	}
	for i := range c.hypotheses {
		if c.hypotheses[i].ID == id {
			c.hypotheses[i].Tested = true
			c.hypotheses[i].Outcome = outcome
			c.appendLocked(tick, by, "hypothesis_tested", id+": "+outcome)
			return nil
		}
	}
	return fmt.Errorf("casefabric: no hypothesis %q in this case", id)
}

// RegisterClaim adds a proposition the case must establish.
func (c *Case) RegisterClaim(cl Claim, by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Law 7: no post-resolution epistemic mutation. A resolved case's
	// evidential picture is fixed; changing it afterwards would alter
	// the basis of a decision already taken. Reopening is the lawful
	// route, and it clears the outcome first.
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "registering a claim")
	}
	if strings.TrimSpace(cl.ID) == "" || strings.TrimSpace(cl.Proposition.Statement) == "" {
		return fmt.Errorf("casefabric: a claim requires an id and a falsifiable proposition")
	}
	if _, exists := c.claims[cl.ID]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateClaim, cl.ID)
	}
	if c.phase == PhaseClosed {
		return fmt.Errorf("%w: cannot register a claim in %s", ErrBadTransition, c.phase)
	}
	cl.ProofHash = ""
	cl.Stance = proof.Unknown
	cl.Sufficiency = proof.NotDetermined
	stored := cl
	c.claims[cl.ID] = &stored
	c.claimOrder = append(c.claimOrder, cl.ID)
	c.appendLocked(tick, by, "claim_registered", cl.ID+": "+cl.Proposition.Statement)
	return nil
}

// BeginQualification moves the case to PhaseUnderQualification.
//
// It requires at least one hypothesis on the record. A case that
// qualifies claims without ever naming a rival explanation is testing
// nothing, and the fabric will not let it pretend otherwise.
func (c *Case) BeginQualification(by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.hypotheses) == 0 {
		return ErrNoHypotheses
	}
	if !CanTransition(c.phase, PhaseUnderQualification) {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseUnderQualification)
	}
	c.phase = PhaseUnderQualification
	c.appendLocked(tick, by, "qualification_begun", fmt.Sprintf("%d claim(s) under qualification", len(c.claimOrder)))
	return nil
}

// AttachProof binds a sealed proof object to a registered claim.
//
// The object is re-verified and checked to belong to this case and this
// proposition. A proof object about a different proposition, however
// well sealed, does not establish this claim.
func (c *Case) AttachProof(claimID string, o proof.Object, by string, tick uint64) error {
	if err := proof.VerifyHash(o); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "attaching a proof object")
	}
	cl, ok := c.claims[claimID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownClaim, claimID)
	}
	if o.Scope.CaseID != c.identity.CaseID {
		return fmt.Errorf("%w: object is scoped to %q", ErrProofWrongCase, o.Scope.CaseID)
	}
	if o.Proposition.ID != cl.Proposition.ID {
		return fmt.Errorf("%w: object proves %q, claim is %q", ErrProofWrongClaim, o.Proposition.ID, cl.Proposition.ID)
	}
	cl.ProofHash = o.CanonicalHash
	cl.Stance = o.Stance
	cl.Sufficiency = o.Sufficiency
	c.appendLocked(tick, by, "proof_attached",
		fmt.Sprintf("claim %s: %s / %s", claimID, o.Stance, o.Sufficiency))
	return nil
}

// requireAttachedProof checks the hash belongs to a claim on this case.
func (c *Case) requireAttachedProof(proofHash string) error {
	if strings.TrimSpace(proofHash) == "" {
		return fmt.Errorf("%w: the decision carries no proof lineage", ErrDecisionNotOnThisCase)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, id := range c.claimOrder {
		if c.claims[id].ProofHash == proofHash {
			return nil
		}
	}
	return fmt.Errorf("%w: no claim on this case carries proof %s", ErrDecisionNotOnThisCase, proofHash)
}

// RecordReverseClosure records that the reverse direction closed over
// this case's subject.
//
// It is a case event, not a bookkeeping detail: the closure is what
// makes the reverse direction a gate rather than an audit, and a gate
// that leaves no trace on the record cannot be checked afterwards.
func (c *Case) RecordReverseClosure(subject string, holds bool, by string, tick uint64) error {
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("%w: the closure names no subject", ErrReverseNotClosed)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "recording a reverse closure")
	}
	verdict := "closure holds"
	if !holds {
		verdict = "closure does NOT hold"
	}
	c.appendLocked(tick, by, "reverse_closed", subject+": "+verdict)
	return nil
}

// RecordFinding records that a finding was founded on an attached proof
// object.
//
// The case does not create the finding — pkg/proof is the sole finding
// authority — it records that one exists, so the ledger carries the
// step in its lawful position.
func (c *Case) RecordFinding(findingHash, proofHash, by string, tick uint64) error {
	if strings.TrimSpace(findingHash) == "" {
		return fmt.Errorf("casefabric: a finding record requires the finding's hash")
	}
	if err := c.requireAttachedProof(proofHash); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "recording a finding")
	}
	c.appendLocked(tick, by, "finding_founded", "finding "+findingHash+" founded on proof "+proofHash)
	return nil
}

// RecordAuthorizedDecision records that an authority adopted a finding.
func (c *Case) RecordAuthorizedDecision(d proof.Decision, by string, tick uint64) error {
	if d.IsZero() {
		return ErrNoAuthorizedDecision
	}
	dh, _, _, proofHash := d.Lineage()
	if err := c.requireAttachedProof(proofHash); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == PhaseResolved {
		return fmt.Errorf("%w: %s", ErrPostResolutionMutation, "recording a decision")
	}
	c.appendLocked(tick, by, "decision_authorized",
		"decision "+dh+" authorized by "+d.Authorized().AuthorizerID()+": "+d.Action())
	return nil
}

// UnprovenMaterialClaims lists the material claims not carried by a
// sufficient proof object. It is exported because it is the answer to
// "why can this case not be resolved?".
func (c *Case) UnprovenMaterialClaims() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	for _, id := range c.claimOrder {
		cl := c.claims[id]
		if cl.Material && !cl.Proven() {
			out = append(out, id)
		}
	}
	return out
}

// UntestedHypotheses lists rival explanations nobody evaluated.
func (c *Case) UntestedHypotheses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	for _, h := range c.hypotheses {
		if !h.Tested {
			out = append(out, h.ID)
		}
	}
	return out
}

// ResolutionGate is what a case must present in order to resolve.
//
// It exists because "every material claim is proven" was not a strong
// enough entry condition. A reviewer reading the runtime artefact — not
// the report — found that resolution was being reached before the
// reverse direction had closed, which made reverse proof a
// retrospective audit rather than a constitutional gate. That inverts
// what the reverse direction is for: it answers "what evidence would
// actually be needed to justify this finding?", and a finding already
// final when the question is asked has been rubber-stamped, not gated.
//
// So resolution now consumes a finalized chain rather than asserting
// one. Every field is evidence that a prior step really happened, and
// none of them is a boolean the caller can simply set to true and be
// believed — Decision carries a lineage this package re-checks against
// the case's own attached proof.
type ResolutionGate struct {
	// Decision is the authorized decision the resolution rests on. It
	// carries its own lineage: decision -> authorized finding -> finding
	// -> proof object, each hash-bound to the last.
	Decision proof.Decision
	// ReverseClosureHolds is fref.Closure.Holds for this case's subject.
	// A closure that does not hold means the finding rests on evidence
	// no proof obligation required, or an obligation went unmet.
	ReverseClosureHolds bool
	// ClosureSubject is the proposition the closure was computed over,
	// recorded so a closure for a different claim cannot be presented.
	ClosureSubject string
	// ClosureExplanation is fref.Closure.Explain(), carried so a refused
	// resolution can say precisely what failed rather than "closure did
	// not hold".
	ClosureExplanation string
}

// Validate refuses a gate that is structurally incomplete.
func (g ResolutionGate) Validate() error {
	if g.Decision.IsZero() {
		return ErrNoAuthorizedDecision
	}
	if strings.TrimSpace(g.ClosureSubject) == "" {
		return fmt.Errorf("%w: the closure names no subject", ErrReverseNotClosed)
	}
	if !g.ReverseClosureHolds {
		detail := g.ClosureExplanation
		if detail == "" {
			detail = "no explanation was carried"
		}
		return fmt.Errorf("%w: %s", ErrReverseNotClosed, detail)
	}
	return nil
}

// Resolve closes qualification with an outcome.
//
// It refuses while any material claim is unproven or any hypothesis
// untested, it refuses an outcome that adjudicates, and — since the
// sequencing audit — it refuses without a ResolutionGate proving that
// the reverse direction closed, the proof object was sealed, a finding
// was founded on it and an authority adopted that finding.
//
// The established and unestablished claim lists are computed here
// rather than accepted from the caller, so a resolution cannot quietly
// omit what it failed to establish.
func (c *Case) Resolve(gate ResolutionGate, disposition, summary, by string, tick uint64) (Outcome, error) {
	// The case's own epistemic state first: these say what the case
	// failed to establish, which is the more useful answer when both
	// this and the sequencing gate are unsatisfied.
	if unproven := c.UnprovenMaterialClaims(); len(unproven) > 0 {
		return Outcome{}, fmt.Errorf("%w: %s", ErrClaimUnproven, strings.Join(unproven, ", "))
	}
	if untested := c.UntestedHypotheses(); len(untested) > 0 {
		return Outcome{}, fmt.Errorf("casefabric: cannot resolve with untested hypotheses: %s", strings.Join(untested, ", "))
	}
	// Then the sequencing gate.
	if err := gate.Validate(); err != nil {
		return Outcome{}, err
	}
	// The decision must rest on a proof object this case actually
	// attached. A valid decision about some other matter is still not a
	// basis for resolving this one.
	_, _, _, proofHash := gate.Decision.Lineage()
	if err := c.requireAttachedProof(proofHash); err != nil {
		return Outcome{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !CanTransition(c.phase, PhaseResolved) {
		return Outcome{}, fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseResolved)
	}

	o := Outcome{Disposition: disposition, Summary: summary, AtTick: tick}
	for _, id := range c.claimOrder {
		if c.claims[id].Proven() {
			o.EstablishedClaimIDs = append(o.EstablishedClaimIDs, id)
		} else {
			o.UnestablishedClaimIDs = append(o.UnestablishedClaimIDs, id)
		}
	}
	if err := o.Validate(); err != nil {
		return Outcome{}, err
	}

	c.outcome = &o
	c.phase = PhaseResolved
	c.appendLocked(tick, by, "case_resolved",
		fmt.Sprintf("%s: %d established, %d unestablished",
			disposition, len(o.EstablishedClaimIDs), len(o.UnestablishedClaimIDs)))
	return o, nil
}

// AddOutcomeLimitations records the limits the outcome inherits from the
// proof objects it rests on. Limitations are additive and deduplicated:
// a limit stated once is stated for the case.
func (c *Case) AddOutcomeLimitations(limits []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.outcome == nil {
		return fmt.Errorf("casefabric: the case has no outcome to qualify")
	}
	seen := map[string]bool{}
	for _, l := range c.outcome.Limitations {
		seen[l] = true
	}
	for _, l := range limits {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			c.outcome.Limitations = append(c.outcome.Limitations, l)
			seen[l] = true
		}
	}
	sort.Strings(c.outcome.Limitations)
	return nil
}

// Suspend halts a case with a stated cause.
func (c *Case) Suspend(cause, by string, tick uint64) error {
	if strings.TrimSpace(cause) == "" {
		return ErrSuspendNeedsCause
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !CanTransition(c.phase, PhaseSuspended) {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseSuspended)
	}
	c.phase = PhaseSuspended
	c.appendLocked(tick, by, "case_suspended", cause)
	return nil
}

// Close terminates the case.
func (c *Case) Close(reason, by string, tick uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !CanTransition(c.phase, PhaseClosed) {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseClosed)
	}
	c.phase = PhaseClosed
	c.appendLocked(tick, by, "case_closed", reason)
	return nil
}

// Reopen reopens a closed case on new evidence.
//
// The prior record is retained in full — that is the point of a distinct
// phase rather than a return to an earlier one.
func (c *Case) Reopen(reason, by string, tick uint64) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("casefabric: reopening requires a stated reason")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !CanTransition(c.phase, PhaseReopened) {
		return fmt.Errorf("%w: %s -> %s", ErrBadTransition, c.phase, PhaseReopened)
	}
	c.phase = PhaseReopened
	c.outcome = nil
	c.appendLocked(tick, by, "case_reopened", reason)
	return nil
}

// SyncDomainState moves the canonical phase to wherever the domain's own
// state says it should be.
//
// This is how a domain stays on the fabric while keeping its own
// vocabulary. A domain state that maps to no canonical phase is refused
// outright — the domain must extend its registered projection, not drift
// away from the fabric.
func (c *Case) SyncDomainState(domainState, by string, tick uint64) error {
	c.mu.RLock()
	domain := c.identity.Domain
	c.mu.RUnlock()

	p, ok := Lookup(domain)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownDomain, domain)
	}
	target, err := p.Phase(domainState)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if target == c.phase {
		return nil
	}
	if !CanTransition(c.phase, target) {
		return fmt.Errorf("%w: domain state %q would move %s -> %s", ErrBadTransition, domainState, c.phase, target)
	}
	c.phase = target
	c.appendLocked(tick, by, "domain_state_synced", domain+" "+domainState+" -> "+string(target))
	return nil
}
