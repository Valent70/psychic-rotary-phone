// Package lineage closes PHASE D2 (P0-5, "Case Lineage") of the
// pre-insurance closure program. It is the one item in that program
// that is genuinely net-new rather than a consolidation: nothing in
// this repository previously had a CaseID that aggregated an entire
// investigation.
//
// What existed before, and why none of it was this:
//
//   - pkg/governance/hitl.Case has a CaseID, but it is a HUMAN REVIEW
//     workflow case (created, assigned, under review, executed) — it
//     tracks a reviewer's progress, not an investigation's evidence.
//   - pkg/platform/telemetry.Correlation has a CaseID field, but it is
//     a span ATTRIBUTE: a string stamped onto trace records for
//     joining. Nothing walks it, nothing checks it is complete.
//   - pkg/platform/correlation.Key joins one EXECUTION's seven
//     identifiers. An execution is smaller than a case: a case can
//     span several executions, corrections, contradictions and a later
//     recorded outcome.
//   - pkg/insurance/* has a real CaseID, but it is insurance business
//     logic, which this program explicitly puts out of scope.
//
// So Ledger below is the missing layer: ONE CaseID under which Intent,
// Evidence, Entity, Event, Contradiction, Hypothesis, Policy,
// Decision, Verification, Replay and Outcome are all registered as
// nodes, each carrying the REAL identifier its owning subsystem
// produced, each declaring which nodes it depends on, appended into a
// per-case hash chain so the lineage is tamper-evident like every
// other ledger in this codebase.
//
// Deliberately NOT in this package, and deliberately not attempted:
// any insurance, claim, coverage, quantum or dispute concept. This is
// the foundation such work would later sit on; the program's rule 8
// ("do NOT enter Insurance implementation") is respected literally.
// The only thing here that looks forward is the vocabulary: a Case
// aggregates exactly the eleven node kinds the program named, no more.
//
// Anti-false-green discipline: Completeness is DERIVED, never
// declared. There is no field a caller can set to say a case is
// complete, and Complete() is true only when every one of the required
// kinds is present, every declared dependency resolves to a node that
// actually exists in the same case, and the hash chain verifies.
package lineage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/platform/correlation"
)

// CaseID is the single identifier everything about one investigation
// hangs from.
type CaseID string

// Kind is one of the eleven node kinds a case aggregates. The set is
// closed: an unrecognised kind is refused, never stored as a
// pass-through string.
type Kind string

const (
	KindIntent        Kind = "INTENT"
	KindEvidence      Kind = "EVIDENCE"
	KindEntity        Kind = "ENTITY"
	KindEvent         Kind = "EVENT"
	KindContradiction Kind = "CONTRADICTION"
	KindHypothesis    Kind = "HYPOTHESIS"
	KindPolicy        Kind = "POLICY"
	KindDecision      Kind = "DECISION"
	KindVerification  Kind = "VERIFICATION"
	KindReplay        Kind = "REPLAY"
	KindOutcome       Kind = "OUTCOME"
)

// knownKinds is the closed vocabulary; ordered separately below for
// deterministic reporting.
var knownKinds = map[Kind]bool{
	KindIntent: true, KindEvidence: true, KindEntity: true, KindEvent: true,
	KindContradiction: true, KindHypothesis: true, KindPolicy: true,
	KindDecision: true, KindVerification: true, KindReplay: true, KindOutcome: true,
}

// requiredKinds are the kinds a case must carry before Complete() can
// be true. EVENT, CONTRADICTION and HYPOTHESIS are deliberately NOT
// required: a case where no source contradicted another, or where no
// competing hypothesis was ever raised, is a perfectly ordinary case,
// and demanding a node for one would push callers to fabricate one.
// The eight below are structural — an investigation without an intent,
// an entity, evidence, a policy, a decision, a verification, a replay
// identity and a recorded outcome is not a completed investigation.
var requiredKinds = []Kind{
	KindIntent, KindEntity, KindEvidence, KindPolicy,
	KindDecision, KindVerification, KindReplay, KindOutcome,
}

// KindOrder is every kind in a stable, human-meaningful order —
// roughly the order a real case produces them.
func KindOrder() []Kind {
	return []Kind{
		KindIntent, KindEntity, KindEvidence, KindEvent, KindContradiction,
		KindHypothesis, KindPolicy, KindDecision, KindVerification,
		KindReplay, KindOutcome,
	}
}

// Errors.
var (
	ErrEmptyCaseID      = errors.New("lineage: CaseID must be non-empty")
	ErrUnknownKind      = errors.New("lineage: unknown node kind")
	ErrEmptyRef         = errors.New("lineage: a node must carry the real identifier its owning subsystem produced")
	ErrDuplicateNode    = errors.New("lineage: this (kind, ref) is already registered on this case")
	ErrUnknownCase      = errors.New("lineage: unknown case")
	ErrDanglingUpstream = errors.New("lineage: node declares an upstream that is not registered on this case")
	ErrChainBroken      = errors.New("lineage: case lineage hash chain does not verify")
)

// Node is one traceable artifact in a case.
type Node struct {
	Kind Kind `json:"kind"`
	// Ref is the REAL identifier the owning subsystem produced — an
	// execution.Context.ExecutionID, an ontology.Evidence.EvidenceID, a
	// replay.VerificationCertificate.VerificationCertificateID. Never a
	// value this package invents.
	Ref string `json:"ref"`
	// Subsystem names where Ref came from, so a reader can go look it
	// up rather than guessing which ledger owns it.
	Subsystem string `json:"subsystem"`
	Tick      uint64 `json:"tick"`
	// Upstream lists the Refs this node was derived from. Every entry
	// must resolve to a node already registered on the same case;
	// Attach refuses a dangling reference rather than storing a
	// lineage with a hole in it.
	Upstream []string `json:"upstream,omitempty"`

	// Index and Hash are assigned by Attach; a caller-supplied value is
	// overwritten.
	Index uint64 `json:"index"`
	Hash  string `json:"hash"`
	Prev  string `json:"prev_hash"`
}

// key is the per-case uniqueness key for a node.
func (n Node) key() string { return string(n.Kind) + "|" + n.Ref }

// computeHash chains this node to its predecessor. Hand-rolled and
// field-ordered for the same cross-language reproducibility reason
// every other ledger in this codebase documents.
func (n Node) computeHash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.lineage.node/v1\nindex=%d\nprev=%s\nkind=%s\nref=%s\nsubsystem=%s\ntick=%d\n",
		n.Index, n.Prev, n.Kind, n.Ref, n.Subsystem, n.Tick)
	up := append([]string(nil), n.Upstream...)
	sort.Strings(up)
	for _, u := range up {
		fmt.Fprintf(&b, "upstream=%s\n", u)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Case is one investigation's whole lineage.
type Case struct {
	CaseID CaseID `json:"case_id"`
	Nodes  []Node `json:"nodes"`
	Head   string `json:"head"`
}

// Completeness is the derived answer to "is this case fully traceable
// from its CaseID". Nothing here is settable.
type Completeness struct {
	CaseID CaseID `json:"case_id"`
	// Complete is true only when MissingKinds is empty, Dangling is
	// empty and ChainVerified is true.
	Complete      bool     `json:"complete"`
	PresentKinds  []Kind   `json:"present_kinds"`
	MissingKinds  []Kind   `json:"missing_kinds,omitempty"`
	Dangling      []string `json:"dangling_upstream,omitempty"`
	ChainVerified bool     `json:"chain_verified"`
	NodeCount     int      `json:"node_count"`
}

// Ledger holds every case's lineage. Safe for concurrent use: a case's
// nodes arrive from whichever goroutine produced them.
type Ledger struct {
	mu    sync.Mutex
	cases map[CaseID]*Case
}

// NewLedger returns an empty ledger. An empty ledger reports every
// case as unknown, which is the correct, fail-closed default.
func NewLedger() *Ledger { return &Ledger{cases: map[CaseID]*Case{}} }

// Attach appends one node to a case's lineage, chaining it to the
// case's current head. It refuses an unknown kind, an empty ref, a
// duplicate (kind, ref), and any upstream reference that does not
// already resolve on this same case — a lineage with a hole in it is
// not a lineage.
func (l *Ledger) Attach(caseID CaseID, n Node) (Node, error) {
	if strings.TrimSpace(string(caseID)) == "" {
		return Node{}, ErrEmptyCaseID
	}
	if !knownKinds[n.Kind] {
		return Node{}, fmt.Errorf("%w: %q", ErrUnknownKind, n.Kind)
	}
	if strings.TrimSpace(n.Ref) == "" {
		return Node{}, fmt.Errorf("%w: kind=%s", ErrEmptyRef, n.Kind)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.cases[caseID]
	if !ok {
		c = &Case{CaseID: caseID}
		l.cases[caseID] = c
	}
	refs := make(map[string]bool, len(c.Nodes))
	for _, existing := range c.Nodes {
		if existing.key() == n.key() {
			return Node{}, fmt.Errorf("%w: %s", ErrDuplicateNode, n.key())
		}
		refs[existing.Ref] = true
	}
	for _, u := range n.Upstream {
		if !refs[u] {
			return Node{}, fmt.Errorf("%w: %s -> %s", ErrDanglingUpstream, n.Ref, u)
		}
	}

	n.Index = uint64(len(c.Nodes))
	n.Prev = c.Head
	n.Hash = n.computeHash()
	c.Nodes = append(c.Nodes, n)
	c.Head = n.Hash
	return n, nil
}

// Case returns a defensive copy of one case's whole lineage.
func (l *Ledger) Case(caseID CaseID) (Case, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.cases[caseID]
	if !ok {
		return Case{}, false
	}
	out := Case{CaseID: c.CaseID, Head: c.Head, Nodes: append([]Node(nil), c.Nodes...)}
	return out, true
}

// CaseIDs returns every known case, deterministically ordered.
func (l *Ledger) CaseIDs() []CaseID {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]CaseID, 0, len(l.cases))
	for id := range l.cases {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// VerifyChain independently recomputes every node hash and its linkage
// to the previous node. A single edited field anywhere in a case's
// lineage fails here.
func (l *Ledger) VerifyChain(caseID CaseID) error {
	c, ok := l.Case(caseID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	prev := ""
	for i, n := range c.Nodes {
		if n.Index != uint64(i) || n.Prev != prev {
			return fmt.Errorf("%w: node %d (%s) linkage", ErrChainBroken, i, n.key())
		}
		if n.computeHash() != n.Hash {
			return fmt.Errorf("%w: node %d (%s) content", ErrChainBroken, i, n.key())
		}
		prev = n.Hash
	}
	if prev != c.Head {
		return fmt.Errorf("%w: head", ErrChainBroken)
	}
	return nil
}

// Walk returns the case's nodes in dependency order: every node
// appears after every node it declares as upstream. Attach already
// guarantees upstream references resolve to EARLIER nodes (a node can
// only reference what is already registered), so the append order is
// already a valid topological order — Walk returns it as such rather
// than re-deriving one, and says so instead of implying a sort
// happened that did not.
func (l *Ledger) Walk(caseID CaseID) ([]Node, error) {
	c, ok := l.Case(caseID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCase, caseID)
	}
	return c.Nodes, nil
}

// Completeness derives whether this case is fully traceable from its
// CaseID alone.
func (l *Ledger) Completeness(caseID CaseID) Completeness {
	comp := Completeness{CaseID: caseID}
	c, ok := l.Case(caseID)
	if !ok {
		comp.MissingKinds = append([]Kind(nil), requiredKinds...)
		return comp
	}
	comp.NodeCount = len(c.Nodes)
	present := map[Kind]bool{}
	refs := map[string]bool{}
	for _, n := range c.Nodes {
		present[n.Kind] = true
		refs[n.Ref] = true
	}
	for _, k := range KindOrder() {
		if present[k] {
			comp.PresentKinds = append(comp.PresentKinds, k)
		}
	}
	for _, k := range requiredKinds {
		if !present[k] {
			comp.MissingKinds = append(comp.MissingKinds, k)
		}
	}
	for _, n := range c.Nodes {
		for _, u := range n.Upstream {
			if !refs[u] {
				comp.Dangling = append(comp.Dangling, n.Ref+" -> "+u)
			}
		}
	}
	comp.ChainVerified = l.VerifyChain(caseID) == nil
	comp.Complete = len(comp.MissingKinds) == 0 && len(comp.Dangling) == 0 && comp.ChainVerified
	return comp
}

// --- binding to the existing correlation primitive -------------------

// ErrCorrelationIncomplete is returned by FromCorrelation when the
// supplied key is missing an identifier the case lineage would need.
// It names every missing field at once rather than one per call.
var ErrCorrelationIncomplete = errors.New("lineage: correlation key is missing identifiers a case lineage requires")

// FromCorrelation registers the nodes one real execution's correlation
// key already carries, in dependency order, on the given case. It
// invents nothing: every Ref below is a field pkg/platform/correlation.
// Key genuinely holds, and a key field that is empty produces NO node
// rather than a placeholder one.
//
// This is deliberately a binding to the EXISTING correlation primitive
// rather than a second identifier set. The program's PHASE D names the
// same eight identifiers correlation.Key already models; duplicating
// them here would be exactly the "second correlation key" rule 0
// forbids.
//
// The kinds FromCorrelation cannot produce from a correlation key
// alone — EVIDENCE (per-record), EVENT, CONTRADICTION, HYPOTHESIS and
// OUTCOME — are the caller's to attach, because only the caller knows
// which specific records those were. That is why a case built from a
// correlation key alone reports Complete=false: it genuinely is not
// complete yet, and reporting otherwise would be the false green this
// whole program exists to prevent.
func (l *Ledger) FromCorrelation(caseID CaseID, k correlation.Key, tick uint64) ([]Node, error) {
	if strings.TrimSpace(string(caseID)) == "" {
		return nil, ErrEmptyCaseID
	}
	var missing []string
	if k.ExecutionID == "" {
		missing = append(missing, "execution_id")
	}
	if k.DecisionID == "" {
		missing = append(missing, "decision_id")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrCorrelationIncomplete, strings.Join(missing, ", "))
	}

	type spec struct {
		kind      Kind
		ref       string
		subsystem string
		upstream  []string
	}
	// Order matters: Attach refuses a dangling upstream, so each entry
	// may only reference refs produced by an EARLIER entry.
	specs := []spec{
		{KindIntent, k.IntentID, "pkg/lifecycle.Intent", nil},
		{KindEntity, k.EntityID, "pkg/identity (via pkg/lifecycle.resolveCanonicalEntity)", nil},
		{KindEvidence, k.EvidencePackageID, "pkg/execution.Context.EvidencePackageID", nil},
		{KindDecision, k.DecisionID, "pkg/explanation.DecisionExplanation", nil},
		{KindReplay, k.ReplayPackageID, "pkg/replay.ReplayPackage", nil},
		{KindVerification, k.VerificationCertificateID, "pkg/replay.VerificationCertificate", nil},
	}

	var attached []Node
	// Registered refs so far, so upstream links are only declared when
	// the referenced node actually got registered (an empty correlation
	// field produces no node, and nothing may then point at it).
	seen := map[string]bool{}
	link := func(candidates ...string) []string {
		var out []string
		for _, c := range candidates {
			if c != "" && seen[c] {
				out = append(out, c)
			}
		}
		return out
	}
	for _, s := range specs {
		if s.ref == "" {
			continue
		}
		switch s.kind {
		case KindEvidence:
			s.upstream = link(k.IntentID, k.EntityID)
		case KindDecision:
			s.upstream = link(k.EvidencePackageID, k.EntityID)
		case KindReplay:
			s.upstream = link(k.DecisionID)
		case KindVerification:
			s.upstream = link(k.ReplayPackageID, k.DecisionID)
		}
		n, err := l.Attach(caseID, Node{
			Kind: s.kind, Ref: s.ref, Subsystem: s.subsystem,
			Tick: tick, Upstream: s.upstream,
		})
		if err != nil {
			return attached, err
		}
		seen[s.ref] = true
		attached = append(attached, n)
	}

	// The identity ledger head is not a node of its own kind — it is
	// the commitment that makes the ENTITY node checkable — so it is
	// attached as an EVENT referencing the entity, which is exactly
	// what it is: a point in the identity ledger's history.
	if k.EntityIdentityLedgerHead != "" && k.EntityID != "" {
		n, err := l.Attach(caseID, Node{
			Kind: KindEvent, Ref: k.EntityIdentityLedgerHead,
			Subsystem: "pkg/identity.Resolver.Head", Tick: tick,
			Upstream: link(k.EntityID),
		})
		if err != nil {
			return attached, err
		}
		attached = append(attached, n)
	}
	return attached, nil
}
