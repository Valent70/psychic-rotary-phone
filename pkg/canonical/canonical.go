// Package canonical closes the single biggest P0 the V7.10.1 whole-
// repo technical audit named: VERIQO had two parallel, non-identical
// intelligence chains — the investor-facing "WOW demo" path (Fusion ->
// Contradiction -> HBayes -> Temporal -> Decision -> DigitalTwin, see
// test/integration/wow_demo_test.go) and the core intelligence-loop
// path (veriqo/core/intelligence.Loop: Contradiction -> Knowledge ->
// Causal -> Learning -> Prediction) — plus Provenance, the composite
// Risk model, and Verification/Certification each living as
// independently-callable, independently-proven engines with no single
// function that runs all of them, in order, over one input, and emits
// one certificate.
//
// Pipeline is that single function. It does not reimplement any
// engine's logic — every step is a direct call into the existing,
// already-audited package (pkg/moat/provenance, pkg/moat/fusion,
// pkg/moat/contradiction, pkg/moat/causal, pkg/moat/intelligence/risk,
// pkg/moat/decision, pkg/moat/digitaltwin) — this package is
// composition and a canonical certificate, nothing more. Every
// sub-engine's own hash-chained ledger and VerifyChain/Rebuild
// guarantee is untouched and still independently checkable; Pipeline
// adds one more guarantee on top: a single deterministic certificate
// hash covering the whole chain's outcome, so "did VERIQO reach this
// conclusion via its full canonical path" is one hash comparison, not
// an audit of five separate ledgers.
//
// Honest scope: Pipeline covers Evidence/Provenance/Fusion/Truth
// (Contradiction)/Causal/Risk/Decision/DigitalTwin/Certificate — the
// P0 named chain. Intent verification (pkg/kernel/intent) is
// deliberately NOT folded in here: intent verification is a
// standalone claim-vs-outcome check with its own lifecycle (a
// Statement can be verified long before or after a given case's
// evidence arrives), so forcing it into this per-case pipeline would
// misrepresent its actual temporal relationship to the rest of the
// chain. It shares the same *trustcalc.Calculus (see NewPipeline) so
// results still land in one trust namespace, exactly like
// veriqo/kernel.Kernel already wires Intent and Evidence together.
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/moat/provenance"
	"veriqo/pkg/trust/state"
)

// ErrNoSubmissions is returned when a CaseInput has no source
// submissions to arbitrate.
var ErrNoSubmissions = errors.New("canonical: case has no source submissions")

// SourceSubmission is one source's assertion for one case, plus the
// provenance/reliability metadata Fusion and Provenance need.
type SourceSubmission struct {
	SourceID        fusion.SourceID
	Value           string
	BaseReliability float64 // (0,1]
	Provider        string  // shared-upstream hint for fusion's own correlation handling
	UpstreamID      string  // optional: declared to provenance.Graph if non-empty

	// Dependencies are additional, explicit dependency assertions for
	// this source beyond the Provider/UpstreamID shorthand — e.g.
	// DependsOnTransformation for two analysts running the same
	// detection model over different raw feeds. They are declared into
	// pkg/moat/evidencegraph and DO affect this source's effective
	// evidence weight (PHASE 1 of the v7.10.3 audit). NodeID/SourceID/
	// TxTick are filled in automatically from this submission.
	Dependencies []evidencegraph.DependencyRecord
}

// CausalLink is one optional cause->claim edge to assert into the
// shared causal graph before computing aggregate support — e.g. "this
// source's own known unreliability history supports doubting its
// claim".
type CausalLink struct {
	Cause     causal.NodeID
	Strength  float64
	Lag       uint64
	Condition string
}

// CaseInput is everything one canonical run needs.
//
// Policy note: RunCanonical feeds decision.Engine via
// risk.ToDecisionValues, which emits exactly one canonical factor key
// ("tbml_composite_risk_score" — see that function's doc comment for
// why this is deliberately a single factor, not a re-exposed
// PatternScore/PriceAnomalyScore pair). Policy must declare a factor
// with that same name to actually consume the computed risk score;
// risk.DefaultPolicy() is the ready-made reference policy for this.
// Supplying a Policy with different factor names is not rejected
// (callers may have legitimate reasons to score differently), but its
// Decision.RiskScore will then diverge from Risk.Score — exactly the
// "canonical projection, not a second recompute" invariant
// risk.ToDecisionValues' own doc comment warns about.
type CaseInput struct {
	Entity           digitaltwin.EntityID
	Subject          string
	Predicate        string
	Submissions      []SourceSubmission
	CausalLinks      []CausalLink
	Policy           decision.Policy
	PatternScore     float64 // optional, pkg/moat/intelligence/patterns output if available
	PriceAnomaly     float64 // optional, pkg/moat/intelligence/pricing output if available
	EconomicChannels map[string]float64
	Tick             uint64
}

// CanonicalCertificate is the single hash covering the deterministic
// outcome of the whole chain — see package comment. Two RunCanonical
// calls over byte-identical CaseInput always produce an identical
// Hash; VerifyCertificate independently recomputes it from the
// CanonicalResult's own fields (never from wall-clock or any hidden
// state) and rejects a mismatch.
type CanonicalCertificate struct {
	Subject           string
	Predicate         string
	ArbitrationWinner string
	ArbitrationHash   string // fusion FusionRecord.Hash for this claim's arbitration
	TruthHash         string // contradiction.Record.Hash for this claim
	ProvenanceStatus  provenance.ProvenanceStatus
	RiskLabel         risk.Label
	RiskScore         float64
	DecisionAction    decision.Action
	TwinHead          string

	// DependencyRootHash commits to the exact evidence dependency
	// ledger the case was decided under; MaxDependencyDiscount and
	// IndependentFamilies expose the correlation posture of the
	// evidence set. All three are inside Hash: a case cannot be
	// replayed under a different dependency model without detection.
	DependencyRootHash    string
	MaxDependencyDiscount float64
	IndependentFamilies   int

	// TrustRootHash, TrustLedgerHead and TrustReviewRequired are the
	// canonical-truth-path mandate's WAVE A item 2 commitment: trust is
	// not an inert module hanging off the side of the pipeline, it is
	// INSIDE the certificate hash. Tampering with the trust ledger moves
	// TrustLedgerHead, which moves TrustRootHash, which moves Hash, which
	// moves the execution root one layer up (see pkg/execution). That is
	// the mechanical form of "tamper trust ledger -> execution root
	// changes".
	//
	// TrustReviewRequired is carried in the certificate rather than only
	// on the result because it is a RELEASE condition: a consumer holding
	// nothing but this certificate must be able to tell that the decision
	// it commits to may not be acted on without a human.
	TrustRootHash       string
	TrustLedgerHead     string
	TrustReviewRequired bool

	// ProvenanceVerifiedIndependent is IndependenceAssessment.
	// IsVerifiedIndependent() for this case, committed here so a
	// certificate holder never has to re-derive independence from a bare
	// score (mandate section IV). It is false for every case in this
	// repository today, which is the honest answer: no attestation
	// mechanism exists in this environment.
	ProvenanceVerifiedIndependent bool

	Hash string
}

// CanonicalResult is the full, explainable output of one RunCanonical
// call — every layer's own result object, plus the certificate.
//
// Schema-level provenance (audit item P0-A's explicit follow-up:
// "bukan lagi soal jumlah struct... schema-level provenance, bukan
// hanya stage-level hashing" -- not just how many structs exist, but
// real per-field provenance, not only stage-level hashing), domain by
// domain, stated honestly per domain rather than claimed uniformly:
//   - Evidence/Truth: Arbitration carries per-source fusion.Contribution
//     records and Truth carries the full contradiction.Observation this
//     verdict was ingested from -- real, per-input provenance.
//   - Provenance: IndependenceAssessment IS the provenance computation
//     itself (Status + Score), not a derived summary of one.
//   - Causal: CausalSupport is a scalar; the CausalLinks it was
//     aggregated from are in the caller's own CaseInput, not
//     duplicated onto the result -- traceable by replaying with the
//     same input, not self-contained.
//   - Risk: Breakdown ([]FactorContribution, one entry per real input
//     signal with Name/RawValue/Weight/Contribution) and Explanation
//     ([]string, real prose keyed to which factors crossed threshold)
//     -- see TestRunCanonicalDomainOutputsCarryRealSchemaLevelProvenance
//     for the live proof these are populated, not empty placeholders.
//   - Decision: Explanation ([]string) is the policy engine's own
//     stated reasoning, checked by the same test above.
//   - Digital Twin: AttributeState/RiskState carry Confidence/
//     UpdatedAtTick but no independent "why" text of their own -- a
//     twin state's provenance is its SOURCE record (the Decision or
//     evidence submission that produced it), not duplicated onto the
//     twin to avoid two copies of the same explanation drifting apart.
//     This is a real, understood design choice, not a silent gap.
//   - Identity: entity resolution's own provenance (which source
//     asserted which alias, under which authority, at which tick) lives
//     in pkg/identity.Resolver's event ledger, one layer above
//     CanonicalResult (see pkg/lifecycle.Orchestrator.Identity) --
//     RunCanonical itself only ever sees an already-resolved Subject
//     string, so it is not this struct's field to carry.
type CanonicalResult struct {
	Arbitration    fusion.ArbitrationResult
	Truth          contradiction.Record
	CausalSupport  float64 // 0 if no CausalLinks supplied
	Provenance     provenance.IndependenceAssessment
	Risk           risk.Result
	Decision       decision.Decision
	Twin           digitaltwin.Twin
	EconomicImpact digitaltwin.EconomicImpactResult
	// Dependency is the mandatory pre-fusion dependency evaluation —
	// never nil-valued on a successful run (see dependency.go).
	Dependency DependencyEvaluation
	// Trust is the mandatory pre-fusion TRUST evaluation (see trust.go).
	// Like Dependency it is never optional and never nil-valued on a
	// successful run: there is no code path from RunCanonical's entry to
	// Fusion.Submit that does not pass through evaluateTrust first.
	Trust       TrustEvaluation
	Certificate CanonicalCertificate
}

// Pipeline composes every MOAT engine the canonical path needs. All
// sub-engines are exported so existing tooling (CLI/SDK, dashboards)
// can still reach into any one layer directly — Pipeline does not
// hide them, it just also offers the single composed call.
type Pipeline struct {
	Trust *trustcalc.Calculus
	// TrustState is the trust STATE MACHINE (pkg/trust/state) every
	// canonical run consults before fusion — the canonical-truth-path
	// mandate's WAVE A item 2. It is distinct from Trust above:
	// Trust is the Bayesian belief Calculus shared OS-wide (what VERIQO
	// believes about a subject's accuracy), TrustState is the governed,
	// hash-chained, revocable trust POSTURE (whether VERIQO may act on
	// that subject's evidence at all). RunCanonical reads TrustState
	// causally and writes Trust as a consequence — see trust.go.
	//
	// Never nil for a Pipeline built by NewPipeline. A hand-constructed
	// Pipeline with a nil TrustState is still valid and produces an
	// honest "Configured: false" TrustEvaluation rather than a silent
	// full-trust default.
	TrustState *state.Engine
	// TrustPolicy is the named, hashable weighting policy TrustState's
	// levels are interpreted under. Zero value means DefaultTrustPolicy.
	TrustPolicy   TrustPolicy
	Provenance    *provenance.Graph
	KG            *kg.Graph
	Fusion        *fusion.Engine
	Contradiction *contradiction.Engine
	Causal        *causal.Graph
	Risk          risk.Model
	Decision      *decision.Engine
	Twin          *digitaltwin.Registry
	// Dependencies is the evidence dependency graph every canonical run
	// MUST consult before fusion (PHASE 1 gate).
	Dependencies *evidencegraph.Graph
}

// NewPipeline constructs a canonical Pipeline. calc may be nil, in
// which case a private Calculus is created — but callers wiring this
// into veriqo/kernel.Kernel should pass the SAME Calculus the kernel
// already shares across Evidence/Intelligence/Intent/Calibration, so
// canonical-path trust updates land in the one shared trust namespace
// (see pkg/core/trustcalc's package comment on why that sharing
// matters).
func NewPipeline(calc *trustcalc.Calculus) *Pipeline {
	if calc == nil {
		calc = trustcalc.New(1, 1)
	}
	g := kg.NewGraph()
	return &Pipeline{
		Trust: calc,
		// A real trust state machine on every pipeline, not an optional
		// extra: the mandate's finding was that trust existed but never
		// participated. Half-life 1000 ticks and a neutral prior of 0.5
		// are pkg/trust/state's own documented defaults (NewEngine's
		// zero-half-life fallback is 1000); 0.5 is the honest neutral
		// point for a Beta(1,1)-equivalent "we know nothing" posture,
		// matching the uninformative prior veriqo/kernel.New already
		// constructs for the shared Calculus.
		TrustState:    state.NewEngine(1000, 0.5),
		TrustPolicy:   DefaultTrustPolicy(),
		Provenance:    provenance.New(),
		KG:            g,
		Fusion:        fusion.NewEngine(g),
		Contradiction: contradiction.NewEngine(),
		Causal:        causal.NewGraph(),
		Risk:          risk.NewModel(),
		Decision:      decision.NewEngine(),
		Twin:          digitaltwin.NewRegistry(),
		Dependencies:  evidencegraph.NewGraph(),
	}
}

// RunCanonical executes the full P0 canonical chain — Evidence
// (Fusion.Submit) -> Provenance (declare + compute independence) ->
// Truth Arbitration (Fusion.Arbitrate) -> Contradiction Ingest ->
// Causal aggregate support (if links supplied) -> Risk.ScoreWithProvenance
// (computed, never caller-asserted independence — the v7.9/v7.10
// audit invariant) -> Decision -> DigitalTwin (attribute + risk +
// causal projection) -> Economic Impact -> CanonicalCertificate — as
// ONE deterministic sequence over one input.
func (p *Pipeline) RunCanonical(actorID string, in CaseInput) (*CanonicalResult, error) {
	if len(in.Submissions) == 0 {
		return nil, ErrNoSubmissions
	}
	claim := fusion.Claim{Subject: in.Subject, Predicate: in.Predicate}

	// --- Evidence Dependency Graph (MANDATORY, pre-fusion) ----------
	// PHASE 1 gate: there is no path from here to Fusion.Submit that
	// skips this call. The weights returned are the ONLY weights the
	// fusion engine is given.
	depEval, err := p.resolveDependencies(in)
	if err != nil {
		return nil, err
	}

	// --- Trust (MANDATORY, pre-fusion) ------------------------------
	// WAVE A item 2. Like the dependency gate above, there is no path
	// from here to Fusion.Submit that skips this: the submissions the
	// loop below iterates are `admitted`, which applyTrustWeights
	// produced, and the weights it registers are the trust-adjusted
	// weights. A REVOKED provider's evidence is not downweighted, it is
	// not present.
	trustEval := p.evaluateTrust(in)
	admitted, depEval, err := applyTrustWeights(depEval, trustEval, in.Submissions)
	if err != nil {
		return nil, err
	}

	// --- Evidence + Provenance ------------------------------------
	sourceIDs := make([]string, 0, len(admitted))
	for _, s := range admitted {
		effective, ok := depEval.Effective[string(s.SourceID)]
		if !ok {
			// Unreachable by construction; kept as a hard structural
			// assertion so a future refactor that bypasses the resolver
			// fails loudly instead of silently fusing undiscounted.
			return nil, fmt.Errorf("canonical: source %s was not dependency-evaluated (fusion refused)", s.SourceID)
		}
		if err := p.Fusion.RegisterSource(fusion.SourceProfile{
			ID: s.SourceID, BaseReliability: effective, Provider: s.Provider,
		}); err != nil {
			return nil, fmt.Errorf("canonical: registering source %s: %w", s.SourceID, err)
		}
		if s.UpstreamID != "" {
			if err := p.Provenance.Declare(string(s.SourceID), s.UpstreamID); err != nil {
				return nil, fmt.Errorf("canonical: declaring provenance for %s: %w", s.SourceID, err)
			}
		}
		if _, err := p.Fusion.Submit(s.SourceID, claim, s.Value, in.Tick); err != nil {
			return nil, fmt.Errorf("canonical: submitting evidence from %s: %w", s.SourceID, err)
		}
		sourceIDs = append(sourceIDs, string(s.SourceID))
	}

	// --- Truth Arbitration ------------------------------------------
	arb, err := p.Fusion.Arbitrate(actorID, claim, in.Tick)
	if err != nil {
		return nil, fmt.Errorf("canonical: arbitration: %w", err)
	}
	var arbHash string
	for _, rec := range p.Fusion.ReplayAll() {
		if rec.ClaimKey == claim.Key() {
			arbHash = rec.Hash // last one wins, ReplayAll is chronological
		}
	}

	// --- Contradiction / Truth Engine --------------------------------
	var winning, losing []string
	for _, ev := range p.Fusion.EvidenceFor(claim) {
		if ev.Value == arb.Winner {
			winning = append(winning, string(ev.Source))
		} else {
			losing = append(losing, string(ev.Source))
		}
	}
	truthRec, err := p.Contradiction.Ingest(contradiction.Observation{
		ClaimKey: claim.Key(), Winner: arb.Winner, WinnerConfidence: arb.WinnerConfidence,
		RunnerUp: arb.RunnerUp, RunnerUpConfidence: arb.RunnerUpConfidence, Contradiction: arb.Contradiction,
		WinningSources: winning, LosingSources: losing, Tick: in.Tick,
	})
	if err != nil {
		return nil, fmt.Errorf("canonical: contradiction ingest: %w", err)
	}

	// --- Causal (optional) --------------------------------------------
	var causalSupport float64
	claimNode := causal.NodeID("claim:" + claim.Key())
	if len(in.CausalLinks) > 0 {
		for _, link := range in.CausalLinks {
			if _, err := p.Causal.Observe(actorID, link.Cause, claimNode, link.Strength, link.Lag, link.Condition, in.Tick); err != nil {
				return nil, fmt.Errorf("canonical: causal observe: %w", err)
			}
		}
		causalSupport = p.Causal.AggregateSupport(claimNode)
	}

	// --- Provenance independence (COMPUTED, never asserted) ----------
	provAssess, err := p.Provenance.Assess(sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("canonical: provenance assess: %w", err)
	}

	// --- Composite Risk (ScoreWithProvenance = computed independence) -
	riskResult, err := p.Risk.ScoreWithProvenance(risk.CompositeInput{
		PatternScore: in.PatternScore, PriceAnomalyScore: in.PriceAnomaly,
		SourceIDs: sourceIDs,
	}, p.Provenance)
	if err != nil {
		return nil, fmt.Errorf("canonical: risk score: %w", err)
	}

	// --- Decision ------------------------------------------------------
	if err := p.Decision.RegisterPolicy(in.Policy); err != nil {
		return nil, fmt.Errorf("canonical: register policy: %w", err)
	}
	values := risk.ToDecisionValues(riskResult)
	dec, err := p.Decision.Decide(actorID, in.Subject, in.Policy.Name, values, in.Tick)
	if err != nil {
		return nil, fmt.Errorf("canonical: decide: %w", err)
	}

	// --- Digital Twin (attribute + risk + causal projection) ---------
	if err := p.Twin.ApplyArbitration(actorID, in.Entity, arb, in.Tick); err != nil {
		return nil, fmt.Errorf("canonical: twin apply arbitration: %w", err)
	}
	if err := p.Twin.ApplyDecision(actorID, in.Entity, dec, in.Tick); err != nil {
		return nil, fmt.Errorf("canonical: twin apply decision: %w", err)
	}
	if len(in.CausalLinks) > 0 {
		if err := p.Twin.ApplyCausalProjection(actorID, in.Entity, in.Policy.Name, causalSupport, in.Tick); err != nil {
			return nil, fmt.Errorf("canonical: twin apply causal projection: %w", err)
		}
	}
	twin, _ := p.Twin.Get(in.Entity)

	// --- Economic impact (optional) -----------------------------------
	var econ digitaltwin.EconomicImpactResult
	if len(in.EconomicChannels) > 0 {
		econ = digitaltwin.ComputeEconomicImpact(digitaltwin.EconomicImpactInput{
			Channels: in.EconomicChannels, RiskScore: dec.RiskScore,
		})
	}

	cert := CanonicalCertificate{
		Subject: in.Subject, Predicate: in.Predicate,
		ArbitrationWinner: arb.Winner, ArbitrationHash: arbHash, TruthHash: truthRec.Hash,
		ProvenanceStatus: provAssess.Status, RiskLabel: riskResult.Label, RiskScore: riskResult.Score,
		DecisionAction: dec.Action, TwinHead: p.Twin.Head(),
		DependencyRootHash: depEval.RootHash, MaxDependencyDiscount: depEval.MaxDiscount,
		IndependentFamilies:           depEval.IndependentFamilyCount(),
		TrustRootHash:                 trustEval.RootHash,
		TrustLedgerHead:               trustEval.LedgerHead,
		TrustReviewRequired:           trustEval.ReviewRequired,
		ProvenanceVerifiedIndependent: provAssess.IsVerifiedIndependent(),
	}
	cert.Hash = hashCertificate(cert)

	// Trust WRITE happens last, after the decision is fixed, and feeds
	// nothing in this run — see recordTrustObservations' doc comment on
	// why a feedback loop here would make cold replay impossible.
	p.recordTrustObservations(trustEval)

	return &CanonicalResult{
		Arbitration: arb, Truth: truthRec, CausalSupport: causalSupport,
		Provenance: provAssess, Risk: riskResult, Decision: dec, Twin: twin,
		EconomicImpact: econ, Dependency: depEval, Trust: trustEval, Certificate: cert,
	}, nil
}

func hashCertificate(c CanonicalCertificate) string {
	raw := fmt.Sprintf("subject=%s|predicate=%s|winner=%s|arb=%s|truth=%s|prov=%s|label=%s|score=%.6f|action=%s|twin=%s|deproot=%s|depmax=%.6f|depfam=%d|trustroot=%s|trusthead=%s|trustreview=%v|provverified=%v",
		c.Subject, c.Predicate, c.ArbitrationWinner, c.ArbitrationHash, c.TruthHash,
		c.ProvenanceStatus, c.RiskLabel, c.RiskScore, c.DecisionAction, c.TwinHead,
		c.DependencyRootHash, c.MaxDependencyDiscount, c.IndependentFamilies,
		c.TrustRootHash, c.TrustLedgerHead, c.TrustReviewRequired, c.ProvenanceVerifiedIndependent)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ErrCertificateTamperDetected is returned by VerifyCertificate when a
// certificate's Hash does not match what its own fields recompute to.
var ErrCertificateTamperDetected = errors.New("canonical: certificate hash does not match its own fields")

// VerifyCertificate independently recomputes a certificate's hash from
// its own fields and compares — the canonical path's own verification
// step (distinct from, and complementary to, pkg/verification's IVF,
// which verifies the separate engine/evidence-bundle model). A
// mismatch means the certificate was altered after issuance, or was
// never genuinely produced by RunCanonical.
func VerifyCertificate(c CanonicalCertificate) error {
	if hashCertificate(c) != c.Hash {
		return ErrCertificateTamperDetected
	}
	return nil
}

// SortedSourceIDs is a small helper exposed for callers/tests that
// want a canonical, deterministic ordering of a case's source IDs
// (e.g. for display) without duplicating sort.Strings everywhere.
func SortedSourceIDs(subs []SourceSubmission) []string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, string(s.SourceID))
	}
	sort.Strings(out)
	return out
}
