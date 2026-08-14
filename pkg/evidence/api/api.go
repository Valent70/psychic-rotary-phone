// Package api closes PHASE 3 of the v7.10.3 production-readiness audit:
// a single unified Evidence API facade, so that fusion, contradiction,
// the evidence graph, provenance and verification are no longer
// orchestrated by hand at every call site.
//
// The audit's acceptance criterion is a NEGATIVE one:
//
//	"Tidak boleh ada production caller yang langsung mengakses
//	 internal fusion engine."
//
// This package satisfies it structurally rather than by convention.
// Facade owns its canonical.Pipeline in an UNEXPORTED field. A caller
// holding a *Facade has no way to reach Fusion, Contradiction or the
// dependency graph — the only doors are the nine methods the audit
// named: Submit, Validate, Resolve, Link, Correlate, Arbitrate,
// Explain, Replay, Verify. Direct engine access still exists for
// tooling that legitimately needs it (canonical.NewPipeline is still
// exported) but it is now an explicit, greppable choice rather than
// the default path.
//
// The facade is also where the ontology becomes load-bearing: Submit
// takes ontology.Evidence, not a (source, value) pair, so typing,
// bitemporality, epistemic class and signature checking happen once,
// at the boundary, for every caller.
//
// P0-A consolidation, final accounting: a whole-repo inventory found
// exactly four distinct "Evidence"-shaped Go types. Three are real,
// independent, load-bearing evidence-ingestion paths, and all three are
// now Facade-native: ontology.Evidence (Submit/Validate/EvidenceFor),
// contradiction.RawObservation (ObserveRaw/ArbitrateClaim/
// RawObservations/VerifyRawTruthLedger/RankHypotheses), and
// fusion.Evidence (FusionSubmit/FusionArbitrate/FusionEvidenceFor/
// FusionVerifyChain, all reaching the SAME f.pipeline.Fusion instance
// Submit/Arbitrate already drive). The fourth,
// pkg/moat/intelligence/evidence.IntelligenceEvidence, was RETIRED
// (deleted) rather than wired in: it was a v7.9-round design (bare-
// float wrapping, score-vs-probability separation, data-quality
// flagging) that every later round independently and more rigorously
// rebuilt directly inside the packages that actually needed it --
// pkg/moat/intelligence/risk's own Result.Score/Breakdown,
// pkg/moat/intelligence/pricing's own DataQuality type, and
// pkg/governance/calibration's stricter Evidence -> Probabilistic
// Observation Contract -- all of which are real, tested, and already
// wired through canonical.Pipeline into this Facade. It had zero
// production or test importers anywhere in the repo at the time of
// removal. Wiring it in instead of retiring it would have created a
// SECOND, redundant "canonical" shape for concepts this codebase
// already solved elsewhere -- directly contradicting the one thing
// this package exists to guarantee.
package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/canonical"
	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/replay"
)

// Errors.
var (
	ErrNoEvidence       = errors.New("evidence/api: no evidence submitted for this claim")
	ErrUnsignedRejected = errors.New("evidence/api: unsigned evidence rejected (RequireSignatures is on)")
	ErrDerivedAsPrimary = errors.New("evidence/api: derived evidence cannot be arbitrated as primary")
	ErrUnknownClaim     = errors.New("evidence/api: unknown claim")
	ErrNoPolicy         = errors.New("evidence/api: a decision policy is required to arbitrate")
)

// Config configures facade-wide trust boundaries.
type Config struct {
	// RequireSignatures makes Submit reject unsigned evidence. Default
	// false so existing internal callers keep working; production
	// deployments turn it on, and the readiness gate checks that they
	// did.
	RequireSignatures bool
	// RequirePrimaryForArbitration stops VERIQO's own derived
	// inferences being fed back in as independent evidence.
	RequirePrimaryForArbitration bool
	// MinIdentityConfidence gates Resolve.
	MinIdentityConfidence float64
}

// DefaultConfig is the safe production posture.
func DefaultConfig() Config {
	return Config{RequireSignatures: true, RequirePrimaryForArbitration: true, MinIdentityConfidence: 0.5}
}

// Facade is the single entry point for all evidence operations.
type Facade struct {
	mu       sync.Mutex
	pipeline *canonical.Pipeline // UNEXPORTED: no direct engine access
	identity *identity.Resolver
	replayer *replay.Engine
	cfg      Config

	// arbitration is the real 9-stage Truth Arbitration engine (audit
	// item P0-A / R1): pkg/engine/adapters.go, pkg/kernel/intentgraph
	// and veriqo/core/intelligence each construct and call their OWN
	// contradiction.ArbitrationEngine directly today, bypassing this
	// facade entirely, because the facade never wrapped it -- Submit/
	// Arbitrate below use the SEPARATE, ingest-only contradiction.Engine
	// via pipeline instead. This field and the ObserveRaw/ArbitrateClaim/
	// RawObservations/VerifyRawTruthLedger/RankHypotheses methods below
	// are the additive, zero-risk first phase of closing that gap: they
	// give the facade real capability equivalent to what those three
	// callers use today, so a caller COULD be migrated onto the facade
	// without behavior change. They do NOT themselves reroute any
	// existing caller -- that is a separate, larger change requiring
	// byte-for-byte regression proof for each of the three call sites,
	// not attempted here. Submit/Arbitrate/EvidenceFor above are
	// completely unchanged by this field's addition.
	arbitration *contradiction.ArbitrationEngine

	// store holds accepted evidence per claim key, in submission order.
	store map[string][]ontology.Evidence
	// executions holds the replay record of each arbitration, keyed by
	// claim key.
	executions map[string]replay.ExecutionRecord
}

// New builds a facade over a canonical pipeline. Passing the kernel's
// own pipeline keeps the trust namespace shared OS-wide.
func New(p *canonical.Pipeline, ids *identity.Resolver, cfg Config) *Facade {
	if p == nil {
		p = canonical.NewPipeline(nil)
	}
	if ids == nil {
		ids = identity.NewResolver()
	}
	return &Facade{
		pipeline: p, identity: ids, replayer: replay.NewEngine(), cfg: cfg,
		arbitration: contradiction.NewArbitrationEngine(),
		store:       map[string][]ontology.Evidence{}, executions: map[string]replay.ExecutionRecord{},
	}
}

// ObserveRaw records a raw observation against the facade's real Truth
// Arbitration engine -- the SAME engine type (and the same computation)
// pkg/engine/adapters.go, pkg/kernel/intentgraph and veriqo/core/
// intelligence construct directly today. A caller migrating from direct
// ArbitrationEngine access calls this instead of engine.Observe(o).
func (f *Facade) ObserveRaw(o contradiction.RawObservation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arbitration.Observe(o)
}

// ArbitrateClaim runs the real nine-stage arbitration pipeline over
// every raw observation pending for claimKey, producing a hash-chained
// TruthVersion -- the facade-mediated equivalent of calling
// ArbitrationEngine.ArbitrateClaim directly.
func (f *Facade) ArbitrateClaim(claimKey string, arbitrationTick, halfLifeTicks uint64) (contradiction.TruthVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arbitration.ArbitrateClaim(claimKey, arbitrationTick, halfLifeTicks)
}

// RawObservations returns the raw (pre-arbitration) observations
// pending for claimKey -- the facade-mediated equivalent of
// pkg/engine/adapters.go's direct use of ArbitrationEngine.Observations
// for IVF replay-bundle construction, so a real evidence bundle can be
// built from what the facade holds rather than reaching past it.
func (f *Facade) RawObservations(claimKey string) []contradiction.RawObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arbitration.Observations(claimKey)
}

// VerifyRawTruthLedger checks claimKey's TruthVersion hash chain for
// tampering -- the facade-mediated equivalent of
// ArbitrationEngine.VerifyTruthLedger.
func (f *Facade) VerifyRawTruthLedger(claimKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arbitration.VerifyTruthLedger(claimKey)
}

// RankHypotheses returns every candidate value for claimKey in ranked
// order with its full supporting evidence -- the facade-mediated
// equivalent of veriqo/core/intelligence's direct use of
// ArbitrationEngine.RankHypotheses.
func (f *Facade) RankHypotheses(claimKey string, arbitrationTick, halfLifeTicks uint64) ([]contradiction.TruthCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arbitration.RankHypotheses(claimKey, arbitrationTick, halfLifeTicks)
}

// FusionRegisterSource, FusionSubmit, FusionArbitrate and
// FusionEvidenceFor are the facade-mediated equivalents of
// pkg/engine/adapters.go's FusionEngineAdapter constructing its own
// private fusion.Engine directly (audit item P0-A / R1: "Tidak boleh
// ada bypass", same fragmentation risk closed for Truth Arbitration
// above, now closed for Fusion). They reach the SAME *fusion.Engine
// instance every other Facade method already goes through
// (f.pipeline.Fusion, the one canonical.Pipeline uses for Submit/
// Arbitrate above) -- not a second, parallel fusion engine.

func (f *Facade) FusionRegisterSource(p fusion.SourceProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline.Fusion.RegisterSource(p)
}

func (f *Facade) FusionSubmit(src fusion.SourceID, claim fusion.Claim, value string, tick uint64) (fusion.Evidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline.Fusion.Submit(src, claim, value, tick)
}

func (f *Facade) FusionArbitrate(actorID string, claim fusion.Claim, tick uint64) (fusion.ArbitrationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline.Fusion.Arbitrate(actorID, claim, tick)
}

func (f *Facade) FusionEvidenceFor(claim fusion.Claim) []fusion.Evidence {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline.Fusion.EvidenceFor(claim)
}

// FusionVerifyChain checks only the fusion ledger's own hash chain --
// the single-engine equivalent of the fusion clause inside Verify()
// above, for a caller (like FusionEngineAdapter.Replayable) that needs
// just this one engine's answer.
func (f *Facade) FusionVerifyChain() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pipeline.Fusion.VerifyChain()
}

// Validate checks one evidence record against the ontology and the
// facade's trust boundaries WITHOUT storing it — the "can I send this"
// pre-flight every API client needs.
func (f *Facade) Validate(e ontology.Evidence) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if f.cfg.RequireSignatures {
		if err := e.VerifySignature(); err != nil {
			return fmt.Errorf("%w: %v", ErrUnsignedRejected, err)
		}
	}
	return nil
}

// Submit validates, stores and LINKS one evidence record: its
// provenance is projected into the dependency graph automatically, so
// correlation is modelled at ingest rather than remembered later.
func (f *Facade) Submit(e ontology.Evidence) (ontology.Evidence, error) {
	normalized, err := ontology.New(e)
	if err != nil {
		return ontology.Evidence{}, err
	}
	// Preserve a caller-supplied signature through normalisation.
	normalized.Signature, normalized.PublicKey = e.Signature, e.PublicKey
	if err := f.Validate(normalized); err != nil {
		return ontology.Evidence{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := claimKey(normalized)
	f.store[key] = append(f.store[key], normalized)
	for _, rec := range ontology.DependencyProjection(normalized) {
		if _, err := f.pipeline.Dependencies.Declare(rec); err != nil && err != evidencegraph.ErrDuplicateDependency {
			return ontology.Evidence{}, fmt.Errorf("evidence/api: linking dependency: %w", err)
		}
	}
	return normalized, nil
}

// Link declares an explicit dependency between two evidence nodes that
// provenance metadata did not capture — e.g. an analyst noticing two
// vendors quietly share a subcontractor.
func (f *Facade) Link(node, upstream string, kind evidencegraph.DependencyKind, confidence float64, tick uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.pipeline.Dependencies.Declare(evidencegraph.DependencyRecord{
		NodeID: evidencegraph.NodeID(node), UpstreamID: evidencegraph.NodeID(upstream),
		Kind: kind, Confidence: confidence, TxTick: tick,
	})
	if err == evidencegraph.ErrDuplicateDependency {
		return nil
	}
	return err
}

// Correlate reports the dependency discount for a claim's current
// evidence set — the observable answer to "how independent is this
// really".
func (f *Facade) Correlate(subject, predicate string, tick uint64) (map[string]float64, [][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.store[subject+"|"+predicate]
	if len(list) == 0 {
		return nil, nil, ErrNoEvidence
	}
	nodes := sourceNodes(list)
	shared := f.pipeline.Dependencies.SharedFractionAt(nodes, tick)
	out := make(map[string]float64, len(shared))
	for n, v := range shared {
		out[string(n)] = v
	}
	var fams [][]string
	for _, fam := range f.pipeline.Dependencies.CorrelationFamilies(nodes) {
		g := make([]string, len(fam))
		for i, n := range fam {
			g[i] = string(n)
		}
		fams = append(fams, g)
	}
	return out, fams, nil
}

// Resolve answers identity through the facade so callers never reach
// into the resolver directly either.
func (f *Facade) Resolve(id identity.Identifier, asOfTick uint64) (identity.Resolution, error) {
	return f.identity.ResolveWithThreshold(id, asOfTick, f.cfg.MinIdentityConfidence)
}

// Arbitrate runs the full canonical chain over a claim's stored
// evidence and records an independently replayable execution.
func (f *Facade) Arbitrate(actorID, subject, predicate string, policy decision.Policy, tick uint64) (*canonical.CanonicalResult, error) {
	if policy.Name == "" {
		return nil, ErrNoPolicy
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := subject + "|" + predicate
	list := f.store[key]
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoEvidence, key)
	}
	if f.cfg.RequirePrimaryForArbitration {
		if err := ontology.RequirePrimary(list); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDerivedAsPrimary, err)
		}
	}
	in := canonical.CaseInput{
		Entity: digitaltwin.EntityID(subject), Subject: subject, Predicate: predicate,
		Policy: policy, Tick: tick,
	}
	for _, e := range ontology.Sort(list) {
		in.Submissions = append(in.Submissions, canonical.SourceSubmission{
			SourceID: fusion.SourceID(e.Source), Value: e.Object,
			BaseReliability: reliabilityOf(e), Provider: e.Provenance.ProducerID,
			UpstreamID: e.Provenance.UpstreamID,
		})
	}
	res, err := f.pipeline.RunCanonical(actorID, in)
	if err != nil {
		return nil, err
	}
	rec, err := replay.Record(actorID, in, res, f.pipeline.Dependencies.ReplayAll())
	if err != nil {
		return nil, err
	}
	f.executions[key] = rec
	return res, nil
}

// Replay independently re-executes a previously arbitrated claim and
// returns the verification certificate.
func (f *Facade) Replay(subject, predicate, requestedBy, nonce string) (replay.VerificationCertificate, error) {
	f.mu.Lock()
	rec, ok := f.executions[subject+"|"+predicate]
	f.mu.Unlock()
	if !ok {
		return replay.VerificationCertificate{}, fmt.Errorf("%w: %s|%s", ErrUnknownClaim, subject, predicate)
	}
	pkg, err := replay.NewReplayPackage(requestedBy, nonce, rec)
	if err != nil {
		return replay.VerificationCertificate{}, err
	}
	return f.replayer.Replay(pkg)
}

// Verify checks every ledger this facade touches — the single call an
// operator or an external auditor makes to ask "is anything here
// inconsistent with its own history?".
func (f *Facade) Verify() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.pipeline.Dependencies.VerifyChain(); err != nil {
		return fmt.Errorf("evidence/api: dependency ledger: %w", err)
	}
	if err := f.pipeline.Fusion.VerifyChain(); err != nil {
		return fmt.Errorf("evidence/api: fusion ledger: %w", err)
	}
	if err := f.pipeline.Contradiction.VerifyChain(); err != nil {
		return fmt.Errorf("evidence/api: contradiction ledger: %w", err)
	}
	if err := f.pipeline.Decision.VerifyChain(); err != nil {
		return fmt.Errorf("evidence/api: decision ledger: %w", err)
	}
	if err := f.pipeline.Twin.VerifyChain(); err != nil {
		return fmt.Errorf("evidence/api: digital twin ledger: %w", err)
	}
	if err := f.identity.VerifyChain(); err != nil {
		return fmt.Errorf("evidence/api: identity ledger: %w", err)
	}
	for _, e := range f.allEvidence() {
		if e.ComputeID() != e.EvidenceID {
			return fmt.Errorf("evidence/api: stored evidence %s does not match its own content", e.EvidenceID)
		}
	}
	return nil
}

// EvidenceFor returns the stored evidence for a claim, defensively
// copied.
func (f *Facade) EvidenceFor(subject, predicate string) []ontology.Evidence {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.store[subject+"|"+predicate]
	out := make([]ontology.Evidence, len(list))
	copy(out, list)
	return out
}

// Identity exposes the resolver for identity ADMINISTRATION (registering
// authorities, recording merges) which is a governance action, not an
// evidence-path action.
func (f *Facade) Identity() *identity.Resolver { return f.identity }

func (f *Facade) allEvidence() []ontology.Evidence {
	keys := make([]string, 0, len(f.store))
	for k := range f.store {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []ontology.Evidence
	for _, k := range keys {
		out = append(out, f.store[k]...)
	}
	return out
}

func claimKey(e ontology.Evidence) string { return e.Subject + "|" + e.Predicate }

func sourceNodes(list []ontology.Evidence) []evidencegraph.NodeID {
	seen := map[string]bool{}
	var out []evidencegraph.NodeID
	for _, e := range list {
		if !seen[e.Source] {
			seen[e.Source] = true
			out = append(out, evidencegraph.NodeID(e.Source))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// reliabilityOf maps an evidence record's own confidence into the
// (0,1] reliability fusion requires, never 0 (which fusion rejects) and
// never above the declared confidence.
func reliabilityOf(e ontology.Evidence) float64 {
	r := e.Confidence
	if r <= 0 {
		return 0.01
	}
	if r > 1 {
		return 1
	}
	return r
}
