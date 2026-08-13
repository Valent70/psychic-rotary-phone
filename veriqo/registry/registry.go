// Package registry is the "Engine Registry" from the API/SDK/CLI
// consolidation architecture doc: a single seam that the CLI, the REST
// gateway, and the generated SDK all call through instead of each
// having its own copy of business logic.
//
//	Gateway -> Engine Registry -> Kernel
//
// Honest scope for this session: this wires TWO real engines already
// built and tested elsewhere in this repo — pkg/trust (Trust Kernel)
// and pkg/kernel/intent (Intent Verification) — behind flat,
// primitive-typed methods, because a CLI flag or an HTTP query
// parameter is always a string/number, never a Go struct. The engine
// APIs (TrustAPI, IntentAPI) are a thin adaptation layer, NOT a new
// business-logic layer: every method here does argument
// marshalling/unmarshalling only and then calls straight into the real
// pkg/trust / pkg/kernel/intent code that this project has already
// built, tested, and hardened across many prior sessions. No new trust
// or intent computation is introduced here.
//
// This session closes the gap named above: all seven engines named in
// the architecture doc (Trust, Intent, Evidence, Verification, Replay,
// Reasoning, Policy) are now registered here, following the exact
// pattern proven by TrustAPI/IntentAPI — flat primitive-typed methods,
// zero new business logic, pure adaptation over already-tested
// pkg/evidence/graph, pkg/kernel/reasoning, pkg/kernel/policy,
// pkg/verification, and pkg/engine code. This file also adds the
// persistence layer the prior report named as missing: an optional,
// opt-in WithStatePath Option that restores/saves every engine's
// hash-chained ledger (or, for the Evidence/Reasoning engines, full
// state) via pkg/storage's crash-atomic FileStore, so state survives
// across separate CLI/gateway process invocations instead of resetting
// on every run.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"

	"veriqo/pkg/engine"
	"veriqo/pkg/evidence/graph"
	"veriqo/pkg/kernel/intent"
	"veriqo/pkg/kernel/policy"
	"veriqo/pkg/kernel/reasoning"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/storage"
	"veriqo/pkg/trust"
	"veriqo/pkg/verification"
)

// TrustAPI adapts pkg/trust.Kernel to flat, transport-friendly
// arguments. It owns exactly one policy, "default" — registered from a
// fixed rule set here rather than accepting arbitrary rule code over a
// wire protocol, which would mean shipping executable code through a
// REST/CLI boundary. This is a deliberate, documented constraint, not
// an oversight: real deployments needing multiple/custom policies
// should construct a *trust.Kernel directly via the pkg/trust package
// and are not limited by this adapter.
type TrustAPI struct {
	kernel *trust.Kernel
}

// NewTrustAPI builds a TrustAPI with one built-in policy: a two-signal
// reliability policy ("win_rate" and inverse "contradiction_rate"),
// the same shape as pkg/trust's own documented example policy.
func NewTrustAPI() *TrustAPI {
	k := trust.NewKernel()
	_ = k.RegisterPolicy(trust.TrustPolicy{
		Name: "default",
		Rules: []trust.TrustRule{
			trust.RuleFromEvidenceKey("win_rate", 0.6, "win_rate"),
			trust.RuleInverse("low_contradiction", 0.4, "contradiction_rate"),
		},
		CertifyThreshold: 0.7,
	})
	return &TrustAPI{kernel: k}
}

// Evaluate is Stage 3+4+5 of the Trust pipeline exposed over a flat
// boundary: winRate and contradictionRate are each expected in [0,1].
func (a *TrustAPI) Evaluate(subjectID string, winRate, contradictionRate float64, tick uint64) (trust.TrustScore, error) {
	return a.kernel.Evaluate(subjectID, "default", trust.TrustEvidence{
		"win_rate":           winRate,
		"contradiction_rate": contradictionRate,
	}, tick)
}

// Certify is Stage 6: Evaluate, then issue a hash-chained certificate
// if the score clears the policy's CertifyThreshold.
func (a *TrustAPI) Certify(subjectID string, winRate, contradictionRate float64, tick uint64) (trust.TrustScore, error) {
	score, _, err := a.kernel.Certify(subjectID, "default", trust.TrustEvidence{
		"win_rate":           winRate,
		"contradiction_rate": contradictionRate,
	}, tick)
	return score, err
}

// Ledger returns the full hash-chained certificate history.
func (a *TrustAPI) Ledger() ([]trust.TrustCertificate, error) {
	return a.kernel.Ledger(), nil
}

// VerifyLedger re-derives every certificate's hash and confirms chain
// linkage — the tamper-evidence check exposed as its own callable.
func (a *TrustAPI) VerifyLedger() (string, error) {
	if err := a.kernel.VerifyLedger(); err != nil {
		return "", err
	}
	return "ledger verified: hash chain intact", nil
}

// IntentAPI adapts pkg/kernel/intent.Verifier to flat arguments.
type IntentAPI struct {
	verifier *intent.Verifier
}

// NewIntentAPI builds an IntentAPI wrapping a real intent.Verifier
// (which itself owns its own internal trust.Kernel scoped to Intent
// Verification accuracy — see pkg/kernel/intent's own doc comment).
func NewIntentAPI() (*IntentAPI, error) {
	v, err := intent.NewVerifier()
	if err != nil {
		return nil, err
	}
	return &IntentAPI{verifier: v}, nil
}

// Verify closes Intent -> Expected -> Observed -> Deviation -> Trust
// Delta. expectedAction/observedAction must be one of MONITOR, FLAG,
// ESCALATE (pkg/moat/decision's real Action values).
func (a *IntentAPI) Verify(intentID, subject, expectedAction, observedAction string, expectedRisk, observedRisk float64, tick uint64) (intent.Verdict, error) {
	stmt := intent.Statement{ID: intentID, Subject: subject, ExpectedAction: decision.Action(expectedAction), ExpectedRisk: expectedRisk, Tick: tick}
	obs := intent.Observation{ObservedAction: decision.Action(observedAction), ObservedRisk: observedRisk, Tick: tick}
	return a.verifier.Verify(stmt, obs)
}

// Certify is Verify, plus issuing a hash-chained certificate if the
// subject's running Intent-Verification-accuracy trust clears its
// policy threshold.
func (a *IntentAPI) Certify(intentID, subject, expectedAction, observedAction string, expectedRisk, observedRisk float64, tick uint64) (intent.Verdict, error) {
	stmt := intent.Statement{ID: intentID, Subject: subject, ExpectedAction: decision.Action(expectedAction), ExpectedRisk: expectedRisk, Tick: tick}
	obs := intent.Observation{ObservedAction: decision.Action(observedAction), ObservedRisk: observedRisk, Tick: tick}
	verdict, _, err := a.verifier.Certify(stmt, obs)
	return verdict, err
}

// Ledger returns this Verifier's full hash-chained trust certificate
// history.
func (a *IntentAPI) Ledger() ([]trust.TrustCertificate, error) {
	return a.verifier.Ledger(), nil
}

// EvidenceAPI adapts pkg/evidence/graph.Graph to flat arguments.
type EvidenceAPI struct {
	graph *graph.Graph
}

func NewEvidenceAPI() *EvidenceAPI { return &EvidenceAPI{graph: graph.NewGraph()} }

// AddNode registers (or dedupes onto) a content-addressed evidence
// node and returns its NodeID as a string.
func (a *EvidenceAPI) AddNode(kind, payload string) (string, error) {
	return string(a.graph.AddNode(kind, payload)), nil
}

// AddEdge records a provenance relationship; relation must be one of
// derived_from, supports, contradicts, corrects.
func (a *EvidenceAPI) AddEdge(from, to, relation string) (string, error) {
	if err := a.graph.AddEdge(graph.NodeID(from), graph.NodeID(to), graph.Relation(relation)); err != nil {
		return "", err
	}
	return fmt.Sprintf("edge added: %s --%s--> %s", from, relation, to), nil
}

// Lineage returns the full ancestry of a node.
func (a *EvidenceAPI) Lineage(id string) ([]graph.Node, error) {
	return a.graph.Lineage(graph.NodeID(id))
}

// Influenced returns everything a node influenced forward.
func (a *EvidenceAPI) Influenced(id string) ([]graph.Node, error) {
	return a.graph.Influenced(graph.NodeID(id))
}

// Stats reports the current node/edge counts.
func (a *EvidenceAPI) Stats() (string, error) {
	return fmt.Sprintf("nodes=%d edges=%d", a.graph.NodeCount(), a.graph.EdgeCount()), nil
}

// ReasoningAPI adapts pkg/kernel/reasoning.KB to flat arguments.
type ReasoningAPI struct {
	kb *reasoning.KB
}

func NewReasoningAPI() *ReasoningAPI { return &ReasoningAPI{kb: reasoning.NewKB()} }

// Assert adds one ground fact to the knowledge base.
func (a *ReasoningAPI) Assert(subject, predicate, object string) (string, error) {
	a.kb.Assert(reasoning.Triple{Subject: subject, Predicate: predicate, Object: object})
	return "asserted", nil
}

// Infer runs forward-chaining inference to a fixpoint and returns how
// many new facts were derived.
func (a *ReasoningAPI) Infer() (uint64, error) {
	n, err := a.kb.Infer()
	return uint64(n), err //nolint:gosec // G115: n is a monotonic count of newly derived facts, never negative
}

// Query returns every fact matching a pattern; a leading "?" on any
// field (e.g. "?x") matches any value at that position.
func (a *ReasoningAPI) Query(subject, predicate, object string) ([]reasoning.Triple, error) {
	return a.kb.Query(reasoning.Triple{Subject: subject, Predicate: predicate, Object: object}), nil
}

// Explain returns the derivation trace for a derived fact.
func (a *ReasoningAPI) Explain(subject, predicate, object string) (reasoning.Derivation, error) {
	t := reasoning.Triple{Subject: subject, Predicate: predicate, Object: object}
	d, ok := a.kb.Explain(t)
	if !ok {
		return reasoning.Derivation{}, fmt.Errorf("reasoning: no derivation for %s — fact is directly asserted or unknown", t)
	}
	return d, nil
}

// PolicyAPI adapts pkg/kernel/policy.Gate to flat arguments, wired
// with one fixed, documented rule chain (the same "one built-in
// policy" constraint TrustAPI documents): ontology must be valid,
// then a minimum trust-score floor, then ESCALATE routes to
// REQUIRE_APPROVAL, then a known-action allow-list, then fail-closed
// Deny by default.
type PolicyAPI struct {
	gate *policy.Gate
}

func NewPolicyAPI() *PolicyAPI {
	g := policy.NewGate(policy.Deny)
	g.RegisterRule(policy.RuleOntologyMustBeValid("ontology_valid"))
	g.RegisterRule(policy.RuleMinTrustScore("min_trust_score", 0.5))
	g.RegisterRule(policy.RuleEscalatedRequiresApproval("escalate_requires_approval"))
	g.RegisterRule(policy.RuleAllowKnownActions("allow_known_actions", "MONITOR", "FLAG", "ESCALATE"))
	return &PolicyAPI{gate: g}
}

// Evaluate gates one action. ontologyValid must be "true" or "false".
func (a *PolicyAPI) Evaluate(action, subject, riskAction string, trustScore float64, ontologyValid string) (string, error) {
	ok := ontologyValid == "true"
	v, err := a.gate.Evaluate(policy.Input{
		Action: action, Subject: subject, RiskAction: riskAction,
		TrustScore: &trustScore, OntologyOK: &ok,
	})
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// Ledger returns the full hash-chained gating decision log.
func (a *PolicyAPI) Ledger() ([]policy.Record, error) { return a.gate.Ledger(), nil }

// VerifyChain re-derives every logged decision's hash and confirms
// chain linkage.
func (a *PolicyAPI) VerifyChain() (string, error) {
	if err := a.gate.VerifyChain(); err != nil {
		return "", err
	}
	return "policy ledger verified: hash chain intact", nil
}

// VerificationAPI adapts pkg/verification.Verifier to flat arguments,
// pre-wired with the two real, independent replay functions already
// proven in pkg/engine (contradiction and fusion arbitration) — no new
// verification logic is introduced here.
type VerificationAPI struct {
	verifier *verification.Verifier
}

func NewVerificationAPI() *VerificationAPI {
	v := verification.NewVerifier()
	v.RegisterReplayFunc("contradiction", engine.ReplayContradiction)
	v.RegisterReplayFunc("fusion", engine.ReplayFusion)
	return &VerificationAPI{verifier: v}
}

// Verify builds a tamper-evident bundle from recordsJSON (a JSON array
// of verification.EvidenceRecord) + claimedResult, then independently
// replays it against the registered domain and reports whether the
// manifest and the replay both check out.
func (a *VerificationAPI) Verify(domain, subject, recordsJSON, claimedResult string) (string, error) {
	var records []verification.EvidenceRecord
	if err := json.Unmarshal([]byte(recordsJSON), &records); err != nil {
		return "", fmt.Errorf("verification: parsing records_json: %w", err)
	}
	pkg := verification.EvidencePackage{Subject: subject, Records: verification.SortRecordsByIndex(records)}
	bundle, err := verification.NewBundle(verification.ReplayPackage{Evidence: pkg, ClaimedResult: claimedResult})
	if err != nil {
		return "", err
	}
	result, err := a.verifier.Verify(domain, bundle)
	if err != nil {
		return "", err
	}
	status := "REJECTED"
	if result.ManifestValid && result.ReplayValid {
		status = "VERIFIED"
	}
	return fmt.Sprintf("status=%s manifest_valid=%t replay_valid=%t recomputed_result=%q recomputed_root=%s",
		status, result.ManifestValid, result.ReplayValid, result.RecomputedResult, result.RecomputedRoot), nil
}

// VerifyCertificate lets a third party (holding only the certificate)
// confirm it has not been altered.
func (a *VerificationAPI) VerifyCertificate(certificateJSON string) (string, error) {
	var c verification.AuditCertificate
	if err := json.Unmarshal([]byte(certificateJSON), &c); err != nil {
		return "", fmt.Errorf("verification: parsing certificate_json: %w", err)
	}
	if !verification.VerifyCertificate(c) {
		return "", fmt.Errorf("verification: certificate hash mismatch — tampered or corrupt")
	}
	return "certificate valid", nil
}

// ReplayAPI exposes the same independent-recomputation functions
// VerificationAPI uses internally, standalone, for callers that only
// need stage 5/6 (deterministic replay) without the full manifest +
// certificate pipeline.
type ReplayAPI struct{}

func NewReplayAPI() *ReplayAPI { return &ReplayAPI{} }

func (a *ReplayAPI) Contradiction(subject, recordsJSON string) (string, error) {
	pkg, err := decodeEvidencePackage(subject, recordsJSON)
	if err != nil {
		return "", err
	}
	return engine.ReplayContradiction(pkg)
}

func (a *ReplayAPI) Fusion(subject, recordsJSON string) (string, error) {
	pkg, err := decodeEvidencePackage(subject, recordsJSON)
	if err != nil {
		return "", err
	}
	return engine.ReplayFusion(pkg)
}

func decodeEvidencePackage(subject, recordsJSON string) (verification.EvidencePackage, error) {
	var records []verification.EvidenceRecord
	if err := json.Unmarshal([]byte(recordsJSON), &records); err != nil {
		return verification.EvidencePackage{}, fmt.Errorf("replay: parsing records_json: %w", err)
	}
	return verification.EvidencePackage{Subject: subject, Records: verification.SortRecordsByIndex(records)}, nil
}

// Registry is the single, named lookup surface every transport
// (CLI/REST/SDK) resolves an engine call through.
type Registry struct {
	Trust        *TrustAPI
	Intent       *IntentAPI
	Evidence     *EvidenceAPI
	Reasoning    *ReasoningAPI
	Policy       *PolicyAPI
	Verification *VerificationAPI
	Replay       *ReplayAPI

	store *storage.FileStore
}

// Option configures Registry construction.
type Option func(*options)

type options struct {
	statePath string
}

// WithStatePath opts a Registry into persistence: on New, prior state
// at path (if any) is restored; SaveState subsequently persists the
// current state back to the same path. Without this option, a
// Registry is purely in-memory (today's behavior, unchanged).
func WithStatePath(path string) Option {
	return func(o *options) { o.statePath = path }
}

// snapshot is the full durable state New restores and SaveState
// persists — a plain, JSON-marshalable struct independent of any
// engine's internal representation, so it is itself the stable
// contract a persistence layer commits to.
type snapshot struct {
	TrustLedger    []trust.TrustCertificate
	IntentLedger   []trust.TrustCertificate
	PolicyLedger   []policy.Record
	ReasoningFacts []reasoning.Triple
	EvidenceNodes  []graph.Node
	EvidenceEdges  []graph.Edge
}

// New builds a Registry with every engine adapter wired and ready. By
// default it is purely in-memory; pass WithStatePath to opt into
// crash-atomic persistence across process invocations.
func New(opts ...Option) (*Registry, error) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}

	intentAPI, err := NewIntentAPI()
	if err != nil {
		return nil, fmt.Errorf("registry: building intent engine: %w", err)
	}
	reg := &Registry{
		Trust:        NewTrustAPI(),
		Intent:       intentAPI,
		Evidence:     NewEvidenceAPI(),
		Reasoning:    NewReasoningAPI(),
		Policy:       NewPolicyAPI(),
		Verification: NewVerificationAPI(),
		Replay:       NewReplayAPI(),
	}

	if o.statePath != "" {
		reg.store = storage.NewFileStore(o.statePath)
		if err := reg.restoreState(); err != nil {
			return nil, fmt.Errorf("registry: restoring state from %s: %w", o.statePath, err)
		}
	}
	return reg, nil
}

// HasPersistence reports whether this Registry was built with
// WithStatePath.
func (r *Registry) HasPersistence() bool { return r.store != nil }

func (r *Registry) restoreState() error {
	data, err := r.store.Load()
	if errors.Is(err, storage.ErrNotFound) {
		return nil // first run at this path — nothing to restore
	}
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decoding snapshot: %w", err)
	}
	if err := r.Trust.kernel.RestoreLedger(snap.TrustLedger); err != nil {
		return fmt.Errorf("restoring trust ledger: %w", err)
	}
	if err := r.Intent.verifier.RestoreLedger(snap.IntentLedger); err != nil {
		return fmt.Errorf("restoring intent ledger: %w", err)
	}
	if err := r.Policy.gate.RestoreLedger(snap.PolicyLedger); err != nil {
		return fmt.Errorf("restoring policy ledger: %w", err)
	}
	for _, t := range snap.ReasoningFacts {
		r.Reasoning.kb.Assert(t)
	}
	for _, n := range snap.EvidenceNodes {
		r.Evidence.graph.AddNode(n.Kind, n.Payload)
	}
	for _, e := range snap.EvidenceEdges {
		_ = r.Evidence.graph.AddEdge(e.From, e.To, e.Relation) // already-consistent edges never error
	}
	return nil
}

// SaveState persists this Registry's current full state to its
// configured store. Returns an error if this Registry was built
// without WithStatePath — persistence is opt-in, and a caller that
// never asked for it should get a clear error, not a silent no-op.
func (r *Registry) SaveState() error {
	if r.store == nil {
		return fmt.Errorf("registry: SaveState called but no state path was configured (use WithStatePath)")
	}
	snap := snapshot{
		TrustLedger:    r.Trust.kernel.Ledger(),
		IntentLedger:   r.Intent.verifier.Ledger(),
		PolicyLedger:   r.Policy.gate.Ledger(),
		ReasoningFacts: r.Reasoning.kb.Facts(),
		EvidenceNodes:  r.Evidence.graph.AllNodes(),
		EvidenceEdges:  r.Evidence.graph.AllEdges(),
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: encoding snapshot: %w", err)
	}
	return r.store.Persist(data)
}
