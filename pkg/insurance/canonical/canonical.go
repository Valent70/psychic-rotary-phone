// Package canonical is the insurance domain's binding to the canonical
// VERIQO foundation closed by the pre-insurance round (R20). It is the
// positive half of the non-duplication rule: the functional spec's §3
// and the Final Design's §1 forbid a second identity system, evidence
// store, replay engine, decision engine, correlation key, policy
// registry or provenance model — and `pkg/insurance` already obeyed
// that prohibition — but nothing bound the insurance domain to the
// canonical primitives either. Before this package,
// `pkg/insurance/**` imported exactly two non-insurance VERIQO
// packages (`pkg/evidence/ontology` and `pkg/moat/contradiction`) and
// referenced `pkg/lineage`, `pkg/platform/correlation`,
// `pkg/governance/envelope` and `pkg/evidence/provenance` nowhere at
// all.
//
// What this package is:
//
//   - A Binding holds ONE lineage.CaseID and the canonical
//     *lineage.Ledger, and attaches each insurance artifact as a
//     lineage node carrying the REAL identifier the owning insurance
//     package already produced — a content-addressed
//     ontology.Evidence.EvidenceID, a policy.Version.VersionID, a
//     causation.HypothesisID, a contradiction record ID, a
//     verification.Manifest's evidence root hash. It mints no
//     identifier of its own, ever.
//   - BindCorrelation folds one execution's real correlation.Key into
//     the same case lineage via lineage.Ledger.FromCorrelation, which
//     already exists and is not re-implemented here.
//   - AttachExternalEvidence is the one door external (non-synthetic)
//     insurance evidence goes through: it requires a validated
//     governance/envelope.Envelope and refuses to attach evidence whose
//     rights do not permit the intended use.
//
// What this package is deliberately NOT:
//
//   - not a second case aggregate (pkg/insurance/case owns that),
//   - not a second lineage ledger (pkg/lineage owns that),
//   - not a correlation key of its own (pkg/platform/correlation owns
//     that),
//   - not a rights model (pkg/evidence/provenance owns that).
//
// The forbidden constructs the design documents name by name —
// InsuranceIdentity, InsuranceEvidenceStore, InsuranceReplayEngine,
// InsuranceDecisionEngine, InsuranceEvidenceEngine, InsuranceTrustEngine
// — are asserted absent from the whole insurance tree by
// TestNoForbiddenCanonicalDuplicateExists, which scans the source
// rather than trusting this comment.
package canonical

import (
	"errors"
	"fmt"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/envelope"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/lineage"
	"veriqo/pkg/platform/correlation"
)

// Subsystem strings recorded on every lineage node this package
// attaches. They name the package that genuinely produced the Ref, so a
// reader can go and look it up rather than guessing which ledger owns
// it — the same discipline lineage.Node.Subsystem already documents.
const (
	SubsystemEvidence      = "pkg/insurance/evidence (wrapping pkg/evidence/ontology)"
	SubsystemPolicy        = "pkg/insurance/policy.Version"
	SubsystemParty         = "pkg/identity (via pkg/insurance/party.Party.EntityRef)"
	SubsystemEvent         = "pkg/insurance/timeline.Event"
	SubsystemContradiction = "pkg/insurance/contradiction.ContradictionRecord"
	SubsystemHypothesis    = "pkg/insurance/causation.Hypothesis"
	SubsystemVerification  = "pkg/insurance/verification.Manifest"
	SubsystemEnvelope      = "pkg/governance/envelope.Envelope"
)

// Errors.
var (
	ErrNilLedger   = errors.New("canonical: a Binding requires the canonical *lineage.Ledger, never a private one")
	ErrEmptyCase   = errors.New("canonical: CaseID must be non-empty")
	ErrNoEntityRef = errors.New(
		"canonical: this party has no EntityRef — an insurance party is only a lineage ENTITY once " +
			"pkg/identity has resolved it; a PartyID is an insurance-local label, not a canonical identity")
	ErrEnvelopeRequired = errors.New(
		"canonical: external insurance evidence requires a governance/envelope.Envelope — " +
			"possession of a document is not provenance")
	ErrFixtureNotExternal = errors.New(
		"canonical: this envelope is a FIXTURE and cannot be attached as external evidence — " +
			"a fixture-origin record must never enter a case as if it came from outside")
	ErrRightsDeny   = errors.New("canonical: the evidence's rights state does not permit the intended use")
	ErrCaseMismatch = errors.New(
		"canonical: this evidence record belongs to a different insurance case")
)

// Binding is one insurance case's binding to the canonical foundation.
// Construct with New; the zero value is unusable by design.
type Binding struct {
	ledger *lineage.Ledger
	caseID lineage.CaseID
}

// New binds insuranceCaseID to the canonical case lineage ledger. The
// insurance CaseID and the lineage CaseID are deliberately the SAME
// string: the whole point of pkg/lineage is that one investigation has
// one CaseID, and giving insurance a second one would reintroduce the
// fragmentation that package exists to remove.
func New(ledger *lineage.Ledger, insuranceCaseID string) (*Binding, error) {
	if ledger == nil {
		return nil, ErrNilLedger
	}
	if insuranceCaseID == "" {
		return nil, ErrEmptyCase
	}
	return &Binding{ledger: ledger, caseID: lineage.CaseID(insuranceCaseID)}, nil
}

// CaseID returns the canonical case identifier this binding writes under.
func (b *Binding) CaseID() lineage.CaseID { return b.caseID }

// BindCorrelation registers, on this case, every node one real
// execution's correlation.Key already carries — intent, entity,
// evidence package, decision, replay and verification, plus the
// identity ledger head. It delegates entirely to
// lineage.Ledger.FromCorrelation: no identifier is re-derived, and an
// empty correlation field produces no node rather than a placeholder.
func (b *Binding) BindCorrelation(k correlation.Key, tick uint64) ([]lineage.Node, error) {
	return b.ledger.FromCorrelation(b.caseID, k, tick)
}

// AttachEvidence registers one insurance evidence record as an EVIDENCE
// node. The Ref is the record's content-addressed EvidenceID (the
// underlying ontology.Evidence's own identity) — this package never
// mints an insurance-local evidence identifier, which is precisely the
// "InsuranceEvidenceStore" the design documents forbid.
//
// upstream may name refs already registered on this case (for example
// the ENTITY node of the party that submitted it, or the parent
// evidence a derived document cites). lineage.Attach refuses a dangling
// upstream, so an unregistered ref is an error rather than a hole.
func (b *Binding) AttachEvidence(rec insevidence.Record, tick uint64, upstream ...string) (lineage.Node, error) {
	if string(b.caseID) != rec.CaseID {
		return lineage.Node{}, fmt.Errorf("%w: node case=%s record case=%s", ErrCaseMismatch, b.caseID, rec.CaseID)
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindEvidence, Ref: rec.EvidenceID(),
		Subsystem: SubsystemEvidence, Tick: tick, Upstream: upstream,
	})
}

// AttachExternalEvidence is the one door externally-sourced insurance
// evidence goes through. It enforces three things the design documents
// treat as non-negotiable, in order, and fails closed on each:
//
//  1. an envelope must exist and must Validate — the functional spec's
//     §52 external evidence contract, unmodified and not re-implemented;
//  2. the envelope must not be a FIXTURE — a fixture-origin record may
//     never enter a case as though it came from outside (Final Design
//     §39: "jangan mencampur synthetic dengan live data");
//  3. the record's rights must permit the intended use — possession is
//     not permission (spec §22).
//
// Only then is the evidence attached, with the envelope's own
// content-addressed ID recorded as an upstream node so the case lineage
// carries the provenance envelope, not just the document.
func (b *Binding) AttachExternalEvidence(rec insevidence.Record, env envelope.Envelope, use provenance.Use, tick uint64) ([]lineage.Node, error) {
	if env.ContractVersion == "" {
		return nil, ErrEnvelopeRequired
	}
	if err := env.Validate(); err != nil {
		return nil, fmt.Errorf("canonical: external evidence envelope invalid: %w", err)
	}
	if env.IsFixture() {
		return nil, fmt.Errorf("%w: gate=%s classification=%s", ErrFixtureNotExternal, env.GateID, env.Classification)
	}
	if !rec.Permits(use) {
		return nil, fmt.Errorf("%w: evidence=%s rights=%s use=%s", ErrRightsDeny, rec.EvidenceID(), rec.Rights, use)
	}

	envNode, err := b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindEvidence, Ref: env.ID(),
		Subsystem: SubsystemEnvelope, Tick: tick,
	})
	if err != nil {
		return nil, err
	}
	evNode, err := b.AttachEvidence(rec, tick, env.ID())
	if err != nil {
		return []lineage.Node{envNode}, err
	}
	return []lineage.Node{envNode, evNode}, nil
}

// AttachParty registers a party as an ENTITY node — but only once
// pkg/identity has resolved it, which is what a non-empty
// party.Party.EntityRef means. A PartyID alone is refused: it is an
// insurance-local label, and registering it as a canonical entity would
// be a second identity authority by the back door.
func (b *Binding) AttachParty(p party.Party, tick uint64) (lineage.Node, error) {
	if p.EntityRef == "" {
		return lineage.Node{}, fmt.Errorf("%w: party=%s", ErrNoEntityRef, p.PartyID)
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindEntity, Ref: p.EntityRef,
		Subsystem: SubsystemParty, Tick: tick,
	})
}

// AttachPolicyVersion registers the policy version that was actually
// effective for this case as a POLICY node. The Ref is the
// policy.Version's own VersionID — the version
// policy.History.EffectiveAt resolved for the incident tick, never
// "the policy" and never the latest version.
func (b *Binding) AttachPolicyVersion(v policy.Version, tick uint64, upstream ...string) (lineage.Node, error) {
	if v.VersionID == "" {
		return lineage.Node{}, errors.New("canonical: policy.Version.VersionID must be non-empty")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindPolicy, Ref: v.VersionID,
		Subsystem: SubsystemPolicy, Tick: tick, Upstream: upstream,
	})
}

// AttachEvent registers one reconstructed timeline event as an EVENT
// node, derived from the evidence that reported it.
func (b *Binding) AttachEvent(eventID string, tick uint64, sourceEvidenceIDs ...string) (lineage.Node, error) {
	if eventID == "" {
		return lineage.Node{}, errors.New("canonical: eventID must be non-empty")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindEvent, Ref: eventID,
		Subsystem: SubsystemEvent, Tick: tick, Upstream: sourceEvidenceIDs,
	})
}

// AttachContradiction registers one contradiction record as a
// CONTRADICTION node, derived from the two evidence records in tension.
func (b *Binding) AttachContradiction(contradictionID string, tick uint64, evidenceIDs ...string) (lineage.Node, error) {
	if contradictionID == "" {
		return lineage.Node{}, errors.New("canonical: contradictionID must be non-empty")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindContradiction, Ref: contradictionID,
		Subsystem: SubsystemContradiction, Tick: tick, Upstream: evidenceIDs,
	})
}

// AttachHypothesis registers one causation hypothesis as a HYPOTHESIS
// node, derived from its supporting evidence. Contradicting and missing
// evidence are deliberately not folded into Upstream: an upstream edge
// means "this was derived from that", and a hypothesis is not derived
// from the evidence that cuts against it. The full
// supporting/contradicting/missing decomposition stays where it
// belongs, on causation.Hypothesis.
func (b *Binding) AttachHypothesis(hypothesisID string, tick uint64, supportingEvidenceIDs ...string) (lineage.Node, error) {
	if hypothesisID == "" {
		return lineage.Node{}, errors.New("canonical: hypothesisID must be non-empty")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindHypothesis, Ref: hypothesisID,
		Subsystem: SubsystemHypothesis, Tick: tick, Upstream: supportingEvidenceIDs,
	})
}

// AttachVerification registers a verification.Manifest's evidence root
// hash as a VERIFICATION node. The Ref is that real, recomputable hash,
// so a reader holding the case's evidence set can independently
// reproduce it — which is exactly what makes this node checkable rather
// than declarative.
func (b *Binding) AttachVerification(evidenceRootHash string, tick uint64, upstream ...string) (lineage.Node, error) {
	if evidenceRootHash == "" {
		return lineage.Node{}, errors.New("canonical: evidenceRootHash must be non-empty")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindVerification, Ref: evidenceRootHash,
		Subsystem: SubsystemVerification, Tick: tick, Upstream: upstream,
	})
}

// AttachDecision registers an authorized human decision as a DECISION
// node. The Ref must be an identifier produced by the canonical
// decision machinery — a pkg/governance/hitl case's GovernedOutcome, or
// an explanation.DecisionExplanation's DecisionID. This package does
// not create decisions and has no way to express one; it records that a
// decision made elsewhere belongs to this case.
func (b *Binding) AttachDecision(decisionRef, subsystem string, tick uint64, upstream ...string) (lineage.Node, error) {
	if decisionRef == "" {
		return lineage.Node{}, errors.New("canonical: decisionRef must be non-empty")
	}
	if subsystem == "" {
		return lineage.Node{}, errors.New("canonical: a DECISION node must name the subsystem that produced it")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindDecision, Ref: decisionRef,
		Subsystem: subsystem, Tick: tick, Upstream: upstream,
	})
}

// AttachReplay and AttachOutcome record the canonical replay package
// identity and the real-world outcome respectively. Both take a Ref
// produced elsewhere, for the same reason AttachDecision does.
func (b *Binding) AttachReplay(replayPackageID string, tick uint64, upstream ...string) (lineage.Node, error) {
	if replayPackageID == "" {
		return lineage.Node{}, errors.New("canonical: replayPackageID must be non-empty")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindReplay, Ref: replayPackageID,
		Subsystem: "pkg/replay.ReplayPackage", Tick: tick, Upstream: upstream,
	})
}

// AttachOutcome records what actually happened in the real world —
// a recorded settlement, award, judgment or recovery reference. It is
// an OUTCOME node and never a conclusion this system computed.
func (b *Binding) AttachOutcome(outcomeRef, subsystem string, tick uint64, upstream ...string) (lineage.Node, error) {
	if outcomeRef == "" {
		return lineage.Node{}, errors.New("canonical: outcomeRef must be non-empty")
	}
	if subsystem == "" {
		return lineage.Node{}, errors.New("canonical: an OUTCOME node must name where the outcome was recorded")
	}
	return b.ledger.Attach(b.caseID, lineage.Node{
		Kind: lineage.KindOutcome, Ref: outcomeRef,
		Subsystem: subsystem, Tick: tick, Upstream: upstream,
	})
}

// Completeness is the canonical, DERIVED answer to "is this insurance
// case fully traceable from its CaseID alone". It is
// lineage.Ledger.Completeness verbatim — nothing here can set it, and
// an insurance case that has not yet produced a decision, verification,
// replay identity and recorded outcome honestly reports Complete=false.
func (b *Binding) Completeness() lineage.Completeness {
	return b.ledger.Completeness(b.caseID)
}

// VerifyChain independently recomputes this case's whole lineage hash
// chain. Delegates to pkg/lineage; no second chain exists.
func (b *Binding) VerifyChain() error { return b.ledger.VerifyChain(b.caseID) }

// Walk returns this case's lineage nodes in dependency order.
func (b *Binding) Walk() ([]lineage.Node, error) { return b.ledger.Walk(b.caseID) }

// HasRef reports whether ref is already registered as a node on this
// case. Callers building an Upstream list use it to declare only the
// links that genuinely resolve — lineage.Attach refuses a dangling
// upstream, and quietly dropping an unresolvable link is the honest
// behaviour: it records the derivation edges that exist rather than
// failing the whole attachment or inventing a node to point at.
func (b *Binding) HasRef(ref string) bool {
	c, ok := b.ledger.Case(b.caseID)
	if !ok {
		return false
	}
	for _, n := range c.Nodes {
		if n.Ref == ref {
			return true
		}
	}
	return false
}

// ResolvedUpstream filters candidates down to the refs already
// registered on this case, preserving order and dropping duplicates.
func (b *Binding) ResolvedUpstream(candidates ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		if c == "" || seen[c] || !b.HasRef(c) {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
