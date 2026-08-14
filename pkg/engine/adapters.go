package engine

import (
	"encoding/json"
	"fmt"

	"veriqo/pkg/evidence/api"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/verification"
)

// --- ContradictionArbitrationEngine adapter --------------------------------

// ContradictionArbitrationEngine wraps a real
// contradiction.ArbitrationEngine as a standardized Engine. Observations
// must be fed in advance via Observe (contradiction's own multi-source
// input shape doesn't fit the generic float-only Context, so this
// adapter's LoadContext only carries WHICH claim/tick to arbitrate —
// consistent with the audit doc's own Stage-2 "LoadContext" naming: the
// context here is "what to evaluate", not necessarily 100% of the raw
// input, exactly as a real orchestrator's LoadContext stage typically
// resolves references rather than embedding entire payloads).
type ContradictionArbitrationEngine struct {
	// inner is reached through pkg/evidence/api.Facade, the ONE
	// canonical Truth Arbitration path (audit item P0-A / R1: "Tidak
	// boleh ada bypass") — this adapter used to construct its own
	// private contradiction.ArbitrationEngine directly.
	inner        *api.Facade
	halfLifeTick uint64

	claimKey string
	tick     uint64
	lastTV   *contradiction.TruthVersion
}

func NewContradictionArbitrationEngine(halfLifeTicks uint64) *ContradictionArbitrationEngine {
	return &ContradictionArbitrationEngine{
		inner: api.New(nil, nil, api.Config{}), halfLifeTick: halfLifeTicks,
	}
}

// Observe feeds one raw observation into the underlying arbitration
// engine — called by the caller BEFORE Run, since Context alone cannot
// carry contradiction's multi-source string-valued observations.
func (e *ContradictionArbitrationEngine) Observe(o contradiction.RawObservation) error {
	return e.inner.ObserveRaw(o)
}

func (e *ContradictionArbitrationEngine) Name() string { return "contradiction" }

func (e *ContradictionArbitrationEngine) Initialize() error { return nil }

func (e *ContradictionArbitrationEngine) LoadContext(ctx Context) error {
	if ctx.Subject == "" {
		return fmt.Errorf("engine: contradiction adapter requires ctx.Subject as the claim key")
	}
	e.claimKey = ctx.Subject
	e.tick = ctx.Tick
	return nil
}

func (e *ContradictionArbitrationEngine) Evaluate() (Result, error) {
	tv, err := e.inner.ArbitrateClaim(e.claimKey, e.tick, e.halfLifeTick)
	if err != nil {
		return Result{}, err
	}
	e.lastTV = &tv
	return Result{
		Summary: fmt.Sprintf("claim=%s winner=%s confidence=%.4f conflicts=%d", tv.ClaimKey, tv.Value, tv.Confidence, len(tv.ConflictingEdges)),
		Score:   tv.Confidence,
		Evidence: []Evidence{
			{Kind: "contradiction.TruthVersion", Payload: fmt.Sprintf("%+v", tv)},
		},
	}, nil
}

func (e *ContradictionArbitrationEngine) Publish(Result) error {
	// The underlying ArbitrationEngine already appended the TruthVersion
	// to its own hash-chained ledger inside ArbitrateClaim (Evaluate
	// above) — publication already happened at the domain-engine level.
	// This stage is a no-op here by design, not by omission: the
	// contradiction engine's own append-only discipline IS the publish
	// step; nothing further needs to happen.
	return nil
}

func (e *ContradictionArbitrationEngine) Replayable() bool {
	if e.lastTV == nil {
		return false
	}
	return e.inner.VerifyRawTruthLedger(e.claimKey) == nil
}

// --- ContradictionArbitrationEngine: IVFCapable -----------------------

// contradictionArbitrationParams is packaged as the LAST evidence
// record (Kind="contradiction.ArbitrationParams") so that replay is
// self-contained in the bundle itself — a verifier running in a
// completely separate OS process (see cmd/veriqo-verify) has no access
// to this adapter's closure state and must be able to reconstruct
// ClaimKey/Tick/HalfLifeTick from the bundle alone.
type contradictionArbitrationParams struct {
	ClaimKey     string
	Tick         uint64
	HalfLifeTick uint64
}

// RawEvidence exports the TRUE raw observations behind the last
// ArbitrateClaim — not tv (the computed result) — so an independent
// verifier replays from evidence, never from the answer.
func (e *ContradictionArbitrationEngine) RawEvidence() ([]verification.EvidenceRecord, error) {
	obs := e.inner.RawObservations(e.claimKey)
	if len(obs) == 0 {
		return nil, fmt.Errorf("engine: no observations pending for claim %q", e.claimKey)
	}
	records := make([]verification.EvidenceRecord, 0, len(obs)+1)
	for i, o := range obs {
		payload, err := json.Marshal(o)
		if err != nil {
			return nil, fmt.Errorf("engine: marshaling observation %d: %w", i, err)
		}
		records = append(records, verification.EvidenceRecord{Kind: "contradiction.RawObservation", Index: uint64(i), Payload: payload})
	}
	params, err := json.Marshal(contradictionArbitrationParams{ClaimKey: e.claimKey, Tick: e.tick, HalfLifeTick: e.halfLifeTick})
	if err != nil {
		return nil, fmt.Errorf("engine: marshaling arbitration params: %w", err)
	}
	records = append(records, verification.EvidenceRecord{Kind: "contradiction.ArbitrationParams", Index: uint64(len(obs)), Payload: params})
	return records, nil
}

func (e *ContradictionArbitrationEngine) ClaimedResult() string {
	if e.lastTV == nil {
		return ""
	}
	return fmt.Sprintf("value=%s|confidence=%.10f", e.lastTV.Value, e.lastTV.Confidence)
}

func (e *ContradictionArbitrationEngine) IVFDomain() string { return "contradiction" }

// IVFReplayFunc delegates to ReplayContradiction (replay.go), a pure,
// exported function that reads ClaimKey/Tick/HalfLifeTick out of the
// bundle's own ArbitrationParams record rather than this adapter's
// closure state — so the EXACT SAME function also works when called
// from a completely separate OS process (see cmd/veriqo-verify), not
// only from this in-process convenience path.
func (e *ContradictionArbitrationEngine) IVFReplayFunc() verification.ReplayFunc {
	return ReplayContradiction
}

// --- DecisionPolicyEngine adapter ------------------------------------------

// DecisionPolicyEngine wraps a real decision.Engine — Context.Values
// maps directly onto the policy's factor values, a clean, genuine fit
// for the generic Context shape (no side-channel needed, unlike the
// contradiction adapter above).
type DecisionPolicyEngine struct {
	inner      *decision.Engine
	policy     decision.Policy
	policyName string
	actorID    string

	ctx          Context
	lastDecision *decision.Decision
}

func NewDecisionPolicyEngine(policy decision.Policy, actorID string) (*DecisionPolicyEngine, error) {
	e := decision.NewEngine()
	if err := e.RegisterPolicy(policy); err != nil {
		return nil, err
	}
	return &DecisionPolicyEngine{inner: e, policy: policy, policyName: policy.Name, actorID: actorID}, nil
}

func (e *DecisionPolicyEngine) Name() string { return "decision" }

func (e *DecisionPolicyEngine) Initialize() error { return nil }

func (e *DecisionPolicyEngine) LoadContext(ctx Context) error {
	if ctx.Subject == "" {
		return fmt.Errorf("engine: decision adapter requires ctx.Subject as the entity id")
	}
	e.ctx = ctx
	return nil
}

func (e *DecisionPolicyEngine) Evaluate() (Result, error) {
	dec, err := e.inner.Decide(e.actorID, e.ctx.Subject, e.policyName, e.ctx.Values, e.ctx.Tick)
	if err != nil {
		return Result{}, err
	}
	e.lastDecision = &dec
	return Result{
		Summary: fmt.Sprintf("subject=%s risk=%.4f action=%s", e.ctx.Subject, dec.RiskScore, dec.Action),
		Score:   dec.RiskScore,
		Evidence: []Evidence{
			{Kind: "decision.Decision", Payload: fmt.Sprintf("%+v", dec)},
		},
	}, nil
}

func (e *DecisionPolicyEngine) Publish(Result) error {
	// Same reasoning as ContradictionArbitrationEngine.Publish: decision
	// already appended its own hash-chained record inside Decide().
	return nil
}

func (e *DecisionPolicyEngine) Replayable() bool {
	return e.inner.VerifyChain() == nil
}

// --- DecisionPolicyEngine: IVFCapable ----------------------------------

// decisionRawContext is the JSON-serializable raw input to one Decide()
// call — the actual evidence an independent verifier needs, as opposed
// to Result.Summary's human-readable description of the outcome.
type decisionRawContext struct {
	ActorID    string
	Subject    string
	PolicyName string
	Values     map[string]float64
	Tick       uint64
}

func (e *DecisionPolicyEngine) RawEvidence() ([]verification.EvidenceRecord, error) {
	payload, err := json.Marshal(decisionRawContext{
		ActorID: e.actorID, Subject: e.ctx.Subject, PolicyName: e.policyName,
		Values: e.ctx.Values, Tick: e.ctx.Tick,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: marshaling decision context: %w", err)
	}
	return []verification.EvidenceRecord{{Kind: "decision.Context", Index: 0, Payload: payload}}, nil
}

func (e *DecisionPolicyEngine) ClaimedResult() string {
	if e.lastDecision == nil {
		return ""
	}
	return fmt.Sprintf("action=%s|risk=%.10f", e.lastDecision.Action, e.lastDecision.RiskScore)
}

func (e *DecisionPolicyEngine) IVFDomain() string { return "decision" }

// IVFReplayFunc delegates to ReplayDecision (replay.go). The policy
// itself is not embedded in the bundle (it is the disclosed RULE an
// auditor checks the decision against, published/versioned
// separately — see pkg/kernel/ontology for policy versioning — not
// evidence about the subject), so it must still be supplied by the
// caller; cmd/veriqo-verify takes it via an explicit -policy flag for
// exactly this reason.
func (e *DecisionPolicyEngine) IVFReplayFunc() verification.ReplayFunc {
	return ReplayDecision(e.policy)
}

// --- FusionEngineAdapter ----------------------------------------------

// FusionEngineAdapter wraps a real fusion.Engine as a standardized
// Engine — the third adapter, closing the "fusion has no adapter yet"
// open item named in this package's doc comment. Like the contradiction
// adapter, fusion's multi-source Submit shape doesn't fit the generic
// Context, so sources/evidence are pre-loaded via Submit before Run;
// LoadContext only carries which Claim+actorID+tick to arbitrate.
type FusionEngineAdapter struct {
	inner   *fusion.Engine
	actorID string

	claim  fusion.Claim
	tick   uint64
	lastAR *fusion.ArbitrationResult
}

// NewFusionEngineAdapter constructs a fusion.Engine backed by its own
// fresh knowledge graph (fusion.Engine writes arbitration outcomes into
// a kg.Graph as part of its own audit trail — see fusion.go writeToGraph).
func NewFusionEngineAdapter(actorID string) *FusionEngineAdapter {
	return &FusionEngineAdapter{inner: fusion.NewEngine(kg.NewGraph()), actorID: actorID}
}

// RegisterSource and Submit pre-load fusion's multi-source evidence,
// mirroring ContradictionArbitrationEngine.Observe's pattern.
func (e *FusionEngineAdapter) RegisterSource(p fusion.SourceProfile) error {
	return e.inner.RegisterSource(p)
}

func (e *FusionEngineAdapter) Submit(src fusion.SourceID, claim fusion.Claim, value string, tick uint64) (fusion.Evidence, error) {
	return e.inner.Submit(src, claim, value, tick)
}

func (e *FusionEngineAdapter) Name() string { return "fusion" }

func (e *FusionEngineAdapter) Initialize() error { return nil }

func (e *FusionEngineAdapter) LoadContext(ctx Context) error {
	if ctx.Subject == "" {
		return fmt.Errorf("engine: fusion adapter requires ctx.Subject as \"subject|predicate\"")
	}
	subject, predicate, ok := splitClaimKey(ctx.Subject)
	if !ok {
		return fmt.Errorf("engine: fusion adapter ctx.Subject must be \"subject|predicate\", got %q", ctx.Subject)
	}
	e.claim = fusion.Claim{Subject: subject, Predicate: predicate}
	e.tick = ctx.Tick
	return nil
}

func splitClaimKey(s string) (subject, predicate string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func (e *FusionEngineAdapter) Evaluate() (Result, error) {
	ar, err := e.inner.Arbitrate(e.actorID, e.claim, e.tick)
	if err != nil {
		return Result{}, err
	}
	e.lastAR = &ar
	return Result{
		Summary: fmt.Sprintf("claim=%s winner=%s confidence=%.4f contradiction=%v", e.claim.Key(), ar.Winner, ar.WinnerConfidence, ar.Contradiction),
		Score:   ar.WinnerConfidence,
		Evidence: []Evidence{
			{Kind: "fusion.ArbitrationResult", Payload: fmt.Sprintf("%+v", ar)},
		},
	}, nil
}

func (e *FusionEngineAdapter) Publish(Result) error {
	// fusion.Engine.Arbitrate already appended a FusionRecord to its own
	// hash-chained ledger AND wrote the outcome into its kg.Graph —
	// publication already happened at the domain-engine level, same
	// discipline as the contradiction and decision adapters above.
	return nil
}

func (e *FusionEngineAdapter) Replayable() bool {
	if e.lastAR == nil {
		return false
	}
	return e.inner.VerifyChain() == nil
}

// --- FusionEngineAdapter: IVFCapable -----------------------------------

// fusionArbitrationParams is packaged as the last evidence record for
// the same cross-process-replay reason as
// contradictionArbitrationParams above.
type fusionArbitrationParams struct {
	Subject   string
	Predicate string
	ActorID   string
	Tick      uint64
}

// RawEvidence exports the real fusion.Evidence records fused for this
// claim (source, value, reliability, provider, tick — the true inputs),
// not ArbitrationResult (the computed winner).
func (e *FusionEngineAdapter) RawEvidence() ([]verification.EvidenceRecord, error) {
	ev := e.inner.EvidenceFor(e.claim)
	if len(ev) == 0 {
		return nil, fmt.Errorf("engine: no evidence submitted for claim %q", e.claim.Key())
	}
	records := make([]verification.EvidenceRecord, 0, len(ev)+1)
	for i, item := range ev {
		payload, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("engine: marshaling fusion evidence %d: %w", i, err)
		}
		records = append(records, verification.EvidenceRecord{Kind: "fusion.Evidence", Index: uint64(i), Payload: payload})
	}
	params, err := json.Marshal(fusionArbitrationParams{Subject: e.claim.Subject, Predicate: e.claim.Predicate, ActorID: e.actorID, Tick: e.tick})
	if err != nil {
		return nil, fmt.Errorf("engine: marshaling fusion arbitration params: %w", err)
	}
	records = append(records, verification.EvidenceRecord{Kind: "fusion.ArbitrationParams", Index: uint64(len(ev)), Payload: params})
	return records, nil
}

func (e *FusionEngineAdapter) ClaimedResult() string {
	if e.lastAR == nil {
		return ""
	}
	return fmt.Sprintf("winner=%s|confidence=%.10f|contradiction=%v", e.lastAR.Winner, e.lastAR.WinnerConfidence, e.lastAR.Contradiction)
}

func (e *FusionEngineAdapter) IVFDomain() string { return "fusion" }

// IVFReplayFunc delegates to ReplayFusion (replay.go) for the same
// bundle-self-contained, cross-process-safe reason documented on
// ContradictionArbitrationEngine.IVFReplayFunc above.
func (e *FusionEngineAdapter) IVFReplayFunc() verification.ReplayFunc {
	return ReplayFusion
}
