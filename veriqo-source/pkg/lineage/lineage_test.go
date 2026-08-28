package lineage

import (
	"errors"
	"testing"

	"veriqo/pkg/platform/correlation"
)

const caseA CaseID = "case-dark-vessel-9998887"

// fullCase registers every node kind a completed investigation
// produces, in dependency order, using refs shaped like the real
// identifiers each owning subsystem emits.
func fullCase(t *testing.T, l *Ledger) {
	t.Helper()
	steps := []Node{
		{Kind: KindIntent, Ref: "intent-7f3a", Subsystem: "pkg/lifecycle.Intent", Tick: 1},
		{Kind: KindEntity, Ref: "entity-imo-9998887", Subsystem: "pkg/identity", Tick: 1},
		{Kind: KindEvidence, Ref: "ev-ais-a", Subsystem: "pkg/evidence/ontology", Tick: 2,
			Upstream: []string{"intent-7f3a", "entity-imo-9998887"}},
		{Kind: KindEvidence, Ref: "ev-port-b", Subsystem: "pkg/evidence/ontology", Tick: 2,
			Upstream: []string{"intent-7f3a", "entity-imo-9998887"}},
		{Kind: KindEvent, Ref: "identity-head-abc123", Subsystem: "pkg/identity.Resolver.Head", Tick: 2,
			Upstream: []string{"entity-imo-9998887"}},
		{Kind: KindContradiction, Ref: "contra-ais-vs-port", Subsystem: "pkg/moat/contradiction", Tick: 3,
			Upstream: []string{"ev-ais-a", "ev-port-b"}},
		{Kind: KindHypothesis, Ref: "hyp-dark", Subsystem: "pkg/moat/contradiction.TruthCandidate", Tick: 3,
			Upstream: []string{"contra-ais-vs-port"}},
		{Kind: KindPolicy, Ref: "policy-dark-vessel-v3", Subsystem: "pkg/moat/decision.Policy", Tick: 3},
		{Kind: KindDecision, Ref: "dec-91aa", Subsystem: "pkg/explanation", Tick: 4,
			Upstream: []string{"hyp-dark", "policy-dark-vessel-v3"}},
		{Kind: KindVerification, Ref: "vcert-55bb", Subsystem: "pkg/replay.VerificationCertificate", Tick: 5,
			Upstream: []string{"dec-91aa"}},
		{Kind: KindReplay, Ref: "rpkg-22cc", Subsystem: "pkg/replay.ReplayPackage", Tick: 5,
			Upstream: []string{"dec-91aa"}},
		{Kind: KindOutcome, Ref: "outcome-confirmed-dark", Subsystem: "pkg/lifecycle.RecordOutcome", Tick: 99,
			Upstream: []string{"dec-91aa", "vcert-55bb"}},
	}
	for _, n := range steps {
		if _, err := l.Attach(caseA, n); err != nil {
			t.Fatalf("Attach %s/%s: %v", n.Kind, n.Ref, err)
		}
	}
}

// TestFullCaseWalksEndToEndFromASingleCaseID is PHASE D2's acceptance
// criterion stated exactly: one CaseID, and every one of Intent,
// Evidence, Entity, Event, Contradiction, Hypothesis, Policy,
// Decision, Verification, Replay and Outcome is reachable from it, in
// dependency order, with completeness = true.
func TestFullCaseWalksEndToEndFromASingleCaseID(t *testing.T) {
	l := NewLedger()
	fullCase(t, l)

	nodes, err := l.Walk(caseA)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(nodes) != 12 {
		t.Fatalf("walked %d nodes, want 12", len(nodes))
	}

	// Every node appears after every node it depends on.
	seen := map[string]bool{}
	for i, n := range nodes {
		for _, u := range n.Upstream {
			if !seen[u] {
				t.Fatalf("node %d (%s) depends on %s, which has not appeared yet -- the walk is not in dependency order", i, n.Ref, u)
			}
		}
		seen[n.Ref] = true
	}

	// Every one of the eleven kinds is present.
	kinds := map[Kind]bool{}
	for _, n := range nodes {
		kinds[n.Kind] = true
	}
	for _, k := range KindOrder() {
		if !kinds[k] {
			t.Errorf("kind %s is not reachable from the CaseID", k)
		}
	}

	comp := l.Completeness(caseA)
	if !comp.Complete {
		t.Fatalf("case lineage completeness = false: missing=%v dangling=%v chain=%v",
			comp.MissingKinds, comp.Dangling, comp.ChainVerified)
	}
	if comp.NodeCount != 12 {
		t.Fatalf("NodeCount = %d, want 12", comp.NodeCount)
	}
}

func TestIncompleteCaseIsNeverReportedComplete(t *testing.T) {
	l := NewLedger()
	if _, err := l.Attach(caseA, Node{Kind: KindIntent, Ref: "intent-1", Subsystem: "pkg/lifecycle"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	comp := l.Completeness(caseA)
	if comp.Complete {
		t.Fatal("a case with only an Intent reported Complete")
	}
	if len(comp.MissingKinds) != len(requiredKinds)-1 {
		t.Fatalf("MissingKinds = %v, want the other %d required kinds", comp.MissingKinds, len(requiredKinds)-1)
	}
}

func TestUnknownCaseReportsEveryRequiredKindMissing(t *testing.T) {
	comp := NewLedger().Completeness("never-registered")
	if comp.Complete {
		t.Fatal("an unknown case reported Complete")
	}
	if len(comp.MissingKinds) != len(requiredKinds) {
		t.Fatalf("MissingKinds = %v", comp.MissingKinds)
	}
	if comp.ChainVerified {
		t.Fatal("an unknown case reported a verified chain")
	}
}

func TestAttachRefusesADanglingUpstream(t *testing.T) {
	l := NewLedger()
	_, err := l.Attach(caseA, Node{
		Kind: KindDecision, Ref: "dec-1", Subsystem: "pkg/explanation",
		Upstream: []string{"evidence-that-was-never-registered"},
	})
	if !errors.Is(err, ErrDanglingUpstream) {
		t.Fatalf("err = %v, want ErrDanglingUpstream -- a lineage with a hole in it is not a lineage", err)
	}
}

func TestAttackRefusesEmptyRefUnknownKindAndDuplicates(t *testing.T) {
	l := NewLedger()
	if _, err := l.Attach(caseA, Node{Kind: KindIntent, Ref: "  "}); !errors.Is(err, ErrEmptyRef) {
		t.Errorf("empty ref: err = %v, want ErrEmptyRef", err)
	}
	if _, err := l.Attach(caseA, Node{Kind: "SOMETHING_NEW", Ref: "x"}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("unknown kind: err = %v, want ErrUnknownKind", err)
	}
	if _, err := l.Attach("", Node{Kind: KindIntent, Ref: "x"}); !errors.Is(err, ErrEmptyCaseID) {
		t.Errorf("empty case: err = %v, want ErrEmptyCaseID", err)
	}
	if _, err := l.Attach(caseA, Node{Kind: KindIntent, Ref: "intent-1"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := l.Attach(caseA, Node{Kind: KindIntent, Ref: "intent-1"}); !errors.Is(err, ErrDuplicateNode) {
		t.Errorf("duplicate: err = %v, want ErrDuplicateNode", err)
	}
}

// TestCaseLineageIsTamperEvident applies this codebase's standing
// ledger discipline to the new one: a single edited field anywhere in a
// case's lineage breaks the chain, and Completeness stops reporting
// Complete as a direct consequence.
func TestCaseLineageIsTamperEvident(t *testing.T) {
	l := NewLedger()
	fullCase(t, l)
	if err := l.VerifyChain(caseA); err != nil {
		t.Fatalf("VerifyChain on an untouched case: %v", err)
	}

	l.cases[caseA].Nodes[4].Ref = "identity-head-FORGED"
	if err := l.VerifyChain(caseA); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("VerifyChain after tampering: err = %v, want ErrChainBroken", err)
	}
	if l.Completeness(caseA).Complete {
		t.Fatal("a tampered case still reported Complete")
	}
}

func TestReorderingNodesBreaksTheChain(t *testing.T) {
	l := NewLedger()
	fullCase(t, l)
	c := l.cases[caseA]
	c.Nodes[2], c.Nodes[3] = c.Nodes[3], c.Nodes[2]
	if err := l.VerifyChain(caseA); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("reordered lineage verified: %v", err)
	}
}

func TestCaseReturnsADefensiveCopy(t *testing.T) {
	l := NewLedger()
	fullCase(t, l)
	c, ok := l.Case(caseA)
	if !ok {
		t.Fatal("case not found")
	}
	c.Nodes[0].Ref = "mutated-through-the-copy"
	if err := l.VerifyChain(caseA); err != nil {
		t.Fatalf("mutating a returned copy corrupted the ledger: %v", err)
	}
}

// --- FromCorrelation: binding to the existing primitive ---------------

func fullKey() correlation.Key {
	return correlation.Key{
		IntentID:                  "intent-7f3a",
		ExecutionID:               "exec-11",
		EvidencePackageID:         "evpkg-22",
		EntityID:                  "entity-imo-9998887",
		DecisionID:                "dec-91aa",
		VerificationCertificateID: "vcert-55bb",
		ReplayPackageID:           "rpkg-22cc",
		EntityIdentityLedgerHead:  "identity-head-abc123",
	}
}

func TestFromCorrelationRegistersOnlyRealIdentifiers(t *testing.T) {
	l := NewLedger()
	nodes, err := l.FromCorrelation(caseA, fullKey(), 7)
	if err != nil {
		t.Fatalf("FromCorrelation: %v", err)
	}
	if len(nodes) != 7 {
		t.Fatalf("registered %d nodes, want 7 (6 correlation kinds + the identity ledger head)", len(nodes))
	}
	if err := l.VerifyChain(caseA); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	for _, n := range nodes {
		if n.Ref == "" {
			t.Fatalf("node %s has an empty ref -- FromCorrelation must never invent one", n.Kind)
		}
	}
}

// TestFromCorrelationOmitsEmptyFieldsInsteadOfInventingPlaceholders is
// the direct analogue of correlation.Key's own "IntentID stays empty
// rather than fabricated" discipline: a missing identifier produces NO
// node, never a placeholder one.
func TestFromCorrelationOmitsEmptyFieldsInsteadOfInventingPlaceholders(t *testing.T) {
	l := NewLedger()
	k := fullKey()
	k.IntentID = ""
	k.EntityIdentityLedgerHead = ""
	nodes, err := l.FromCorrelation(caseA, k, 7)
	if err != nil {
		t.Fatalf("FromCorrelation: %v", err)
	}
	for _, n := range nodes {
		if n.Kind == KindIntent {
			t.Fatal("an empty IntentID produced an INTENT node anyway")
		}
		if n.Kind == KindEvent {
			t.Fatal("an empty identity ledger head produced an EVENT node anyway")
		}
	}
	if got := l.Completeness(caseA); got.Complete {
		t.Fatal("a case missing its Intent reported Complete")
	}
}

// TestFromCorrelationAloneIsHonestlyIncomplete records the deliberate,
// documented fact that a correlation key does not carry everything a
// case needs -- and that this package reports that rather than papering
// over it.
func TestFromCorrelationAloneIsHonestlyIncomplete(t *testing.T) {
	l := NewLedger()
	if _, err := l.FromCorrelation(caseA, fullKey(), 7); err != nil {
		t.Fatalf("FromCorrelation: %v", err)
	}
	comp := l.Completeness(caseA)
	if comp.Complete {
		t.Fatal("a case built from a correlation key alone claimed to be complete")
	}
	missing := map[Kind]bool{}
	for _, k := range comp.MissingKinds {
		missing[k] = true
	}
	for _, want := range []Kind{KindPolicy, KindOutcome} {
		if !missing[want] {
			t.Errorf("%s is not reported missing, but a correlation key cannot supply it", want)
		}
	}
}

func TestFromCorrelationRefusesAnIncompleteKey(t *testing.T) {
	l := NewLedger()
	if _, err := l.FromCorrelation(caseA, correlation.Key{}, 1); !errors.Is(err, ErrCorrelationIncomplete) {
		t.Fatalf("err = %v, want ErrCorrelationIncomplete", err)
	}
}

func TestCaseIDsAreDeterministicallyOrdered(t *testing.T) {
	l := NewLedger()
	for _, id := range []CaseID{"c", "a", "b"} {
		if _, err := l.Attach(id, Node{Kind: KindIntent, Ref: "i-" + string(id)}); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	got := l.CaseIDs()
	want := []CaseID{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CaseIDs = %v, want %v", got, want)
		}
	}
}
