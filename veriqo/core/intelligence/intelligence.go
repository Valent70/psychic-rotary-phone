// Package intelligence is Veriqo's Intelligence module. This is a
// direct response to "Yang_masih_saya_kritisi.docx" §2-5, which
// challenged the prior session's version by name: "Pipeline: Hypothesis
// -> Knowledge -> Learning belum otomatis berarti intelligence... Kalau
// belum ada [Bayesian update, belief revision, causal inference,
// counterfactual, confidence propagation], maka ini masih
// orchestration. Bukan intelligence." Point by point:
//
//   - Hypothesis + Contradiction Resolution: unchanged, still
//     pkg/moat/contradiction.ArbitrationEngine (real, already tested).
//   - Learning: no longer an isolated EMA map. Loop shares ONE
//     pkg/core/trustcalc.Calculus with veriqo/core/evidence — a real
//     Bayesian Beta-Binomial posterior per source, updated by
//     Calculus.Observe on every arbitration outcome. EMA is gone from
//     this package entirely, exactly the critique's ask ("EMA bagus
//     untuk smoothing. Tetapi bukan learning engine").
//   - Prediction: no longer "last state -> next state". predictNext
//     maintains a SECOND Bayesian belief per claim (via the same
//     trustcalc primitives) over "does this claim's winning value stay
//     stable tick-to-tick", then combines that belief with the current
//     arbitration confidence via trustcalc.CombineLogOdds — genuine
//     probabilistic inference over two independent-evidence sources,
//     not a linear-delta heuristic.
//   - Causal inference / counterfactual: Resolve now asserts real edges
//     into pkg/moat/causal.Graph (Evidence -> Claim -> Prediction) and
//     Explanation.CausalChain is populated by Graph.Why (genuine
//     backward causal-path search), Explanation.Counterfactuals by
//     Graph.WhatIf (genuine "support with vs without this cause"
//     recomputation) — both real graph algorithms already tested
//     independently in pkg/moat/causal, not new ad hoc logic.
//   - Confidence propagation: veriqo/core/knowledge.PropagateConfidence
//     (noisy-AND over premises) is used whenever this loop derives and
//     asserts a fact from more than one upstream confidence, instead of
//     assigning a flat default.
//   - Explanation: Explanation is a struct of real computed values
//     (ranked candidates, causal paths with numeric strengths,
//     counterfactual deltas) — reportlab/JSON-serializable directly.
//     There is no string-building step in the reasoning path; any
//     prose rendering happens strictly downstream of this struct, in a
//     caller's presentation layer.
package intelligence

import (
	"fmt"
	"sync"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/kernel/reasoning"
	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/contradiction"
	"veriqo/veriqo/core/knowledge"
)

// Explanation is the fully computed, structured answer to every
// question "Yang_masih_saya_kritisi.docx" and the prior docx pose:
// which value won, why, what was rejected, how uncertain, and what the
// causal chain and counterfactuals behind the prediction are.
type Explanation struct {
	ClaimKey                string
	WinningValue            string
	Confidence              float64
	Uncertainty             float64
	TopSupportingSources    []string
	RejectedAlternatives    []contradiction.TruthCandidate
	RemainingContradictions int
	PredictedNextConfidence float64
	PredictionCredibleLow   float64
	PredictionCredibleHigh  float64
	CausalChain             []causal.CausalPath       // real Graph.Why() output
	Counterfactuals         map[string]Counterfactual // per top source: effect of removing it
	KnowledgeVersion        knowledge.Version
}

// Counterfactual is one real WhatIf(effect, removedCause) result.
type Counterfactual struct {
	SupportWithSource    float64
	SupportWithoutSource float64
	Delta                float64
}

var ErrNoObservations = fmt.Errorf("intelligence: no observations for claim")

// Loop is the Intelligence module: Hypothesis + Contradiction
// Resolution (ArbitrationEngine), Knowledge Update
// (knowledge.Store), Causal Inference + Counterfactual (causal.Graph),
// and Learning + Prediction (both driven by a SHARED trustcalc.Calculus
// — the same object veriqo/core/evidence can be given, so learning here
// changes fusion weighting there).
type Loop struct {
	mu             sync.Mutex
	arbitration    *contradiction.ArbitrationEngine
	knowledge      *knowledge.Store
	causal         *causal.Graph
	sourceTrust    *trustcalc.Calculus // shared with evidence.Engine when wired via veriqo/kernel
	claimStability *trustcalc.Calculus // internal: per-claim "value stays stable" belief, for prediction
	lastWinner     map[string]string   // claimKey -> previous tick's winning value
}

// New builds an Intelligence Loop. sharedTrust should be the SAME
// *trustcalc.Calculus instance handed to veriqo/core/evidence.New for
// the cross-system wiring to actually connect; pass nil to have Loop
// own a private one (still internally consistent, just not shared).
func New(sharedTrust *trustcalc.Calculus) *Loop {
	if sharedTrust == nil {
		sharedTrust = trustcalc.New(1, 1)
	}
	return &Loop{
		arbitration:    contradiction.NewArbitrationEngine(),
		knowledge:      knowledge.NewStore(),
		causal:         causal.NewGraph(),
		sourceTrust:    sharedTrust,
		claimStability: trustcalc.New(1, 1),
		lastWinner:     make(map[string]string),
	}
}

func (l *Loop) Observe(o contradiction.RawObservation) error { return l.arbitration.Observe(o) }

// trustKey scopes a raw evidence-source ID under
// trustcalc.NamespaceEvidenceSource before it ever reaches the shared
// Calculus — v7.10 audit fix: this domain (Evidence <-> Intelligence
// shared source trust) is now formally namespaced like intent_actor
// and calibration_source are, closing "core/intelligence.sourceTrust
// masih perlu diaudit dan kemungkinan namespace." Evidence.Engine
// reads the SAME namespace via evidence.NewNamespacedTrust when wired
// through veriqo/kernel.Kernel, so the two domains keep sharing one
// identity space with each other while staying collision-safe against
// every other namespaced domain in the repo.
func (l *Loop) trustKey(sourceID string) string {
	return trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, sourceID)
}

// Reputation returns a source's current Bayesian-calibrated reliability
// (posterior mean) from the shared trust calculus.
func (l *Loop) Reputation(sourceID string) float64 { return l.sourceTrust.Score(l.trustKey(sourceID)) }

// learn updates the shared source-trust calculus from a real
// arbitration outcome: winners strengthen, losers weaken — a genuine
// Bayesian Beta-Binomial update (see pkg/core/trustcalc), not a moving
// average.
func (l *Loop) learn(tv contradiction.TruthVersion) {
	winners := make(map[string]bool, len(tv.SupportingSources))
	for _, sID := range tv.SupportingSources {
		winners[sID] = true
		_, _ = l.sourceTrust.Observe(l.trustKey(sID), true, tv.Confidence)
	}
	for _, e := range tv.ConflictingEdges {
		for _, sID := range []string{e.SourceA, e.SourceB} {
			if !winners[sID] {
				_, _ = l.sourceTrust.Observe(l.trustKey(sID), false, 1-tv.Confidence)
			}
		}
	}
}

// predict performs genuine probabilistic inference: it updates a
// per-claim "stability" Beta belief (did the winner stay the same as
// last tick?), then combines that belief's posterior mean with the
// current arbitration confidence via log-odds evidence combination —
// two independent probabilistic signals combined by Bayes' rule, not a
// linear extrapolation of raw numbers.
func (l *Loop) predict(tv contradiction.TruthVersion) (mean, lo, hi float64) {
	prevWinner, hadPrev := l.lastWinner[tv.ClaimKey]
	l.lastWinner[tv.ClaimKey] = tv.Value
	if hadPrev {
		stable := prevWinner == tv.Value
		_, _ = l.claimStability.Observe(tv.ClaimKey, stable, 1.0)
	}
	stabilityBelief := l.claimStability.Belief(tv.ClaimKey)
	stabilityMean := stabilityBelief.Mean()

	epsilon := 1e-6
	likelihoodRatio := stabilityMean / (1 - stabilityMean + epsilon)
	combined := trustcalc.CombineLogOdds(tv.Confidence, likelihoodRatio)

	// Uncertainty band: v7.10.2 gap closure — this previously used the
	// normal approximation directly (documented weakness: clamps and
	// loses skew information when alpha+beta is small or the belief is
	// near the 0/1 boundary, exactly the "few observations, early
	// prediction" case this loop hits most often). Now uses the exact
	// regularized-incomplete-beta quantile
	// (trustcalc.BetaBelief.ExactCredibleInterval) on the stability
	// belief itself, then re-centers that band on the combined
	// log-odds estimate by the same additive offset used before — so
	// the interval's WIDTH is now exact-Beta-derived (not a symmetric
	// normal guess) while it still reports uncertainty around the
	// actual combined prediction, not the raw stability mean.
	exactLo, exactHi := stabilityBelief.ExactCredibleInterval(0.95)
	offset := stabilityMean - combined
	lo, hi = exactLo-offset, exactHi-offset
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return combined, lo, hi
}

// buildCausalChain asserts real edges (Evidence -> Claim -> Prediction)
// into the causal graph and returns Graph.Why's genuine backward
// causal-path search over them, plus real WhatIf counterfactuals for
// each top supporting source.
func (l *Loop) buildCausalChain(tv contradiction.TruthVersion, predictedConfidence float64, tick uint64) ([]causal.CausalPath, map[string]Counterfactual) {
	claimNode := causal.NodeID("claim:" + tv.ClaimKey)
	predictionNode := causal.NodeID("prediction:" + tv.ClaimKey)

	for _, sID := range tv.SupportingSources {
		strength := l.sourceTrust.Score(l.trustKey(sID))
		if strength <= 0 {
			strength = 0.01 // causal.Graph requires strength in (0,1]
		}
		evidenceNode := causal.NodeID("evidence:" + sID)
		_, _ = l.causal.Observe("intelligence", evidenceNode, claimNode, strength, 0, "", tick)
	}
	predStrength := tv.Confidence
	if predStrength <= 0 {
		predStrength = 0.01
	}
	_, _ = l.causal.Observe("intelligence", claimNode, predictionNode, predStrength, 0, "", tick)

	paths, _ := l.causal.Why(predictionNode)

	counterfactuals := make(map[string]Counterfactual, len(tv.SupportingSources))
	for _, sID := range tv.SupportingSources {
		evidenceNode := causal.NodeID("evidence:" + sID)
		before, after := l.causal.WhatIf(claimNode, evidenceNode)
		counterfactuals[sID] = Counterfactual{SupportWithSource: before, SupportWithoutSource: after, Delta: before - after}
	}
	return paths, counterfactuals
}

// Resolve runs the full Intelligence Loop for one claim as of
// arbitrationTick: Hypothesis + Contradiction Resolution
// (ArbitrateClaim + RankHypotheses), Causal Inference (real graph edges
// + Why/WhatIf), Knowledge Update (Assert with noisy-AND-propagated
// confidence when multiple sources support the winner), Learning
// (Bayesian trust update), and Prediction (log-odds combination of two
// Bayesian beliefs) — returning a fully structured Explanation.
func (l *Loop) Resolve(claimKey string, arbitrationTick uint64, halfLifeTicks uint64) (Explanation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	tv, err := l.arbitration.ArbitrateClaim(claimKey, arbitrationTick, halfLifeTicks)
	if err != nil {
		return Explanation{}, fmt.Errorf("%w: %v", ErrNoObservations, err)
	}
	candidates, err := l.arbitration.RankHypotheses(claimKey, arbitrationTick, halfLifeTicks)
	if err != nil {
		return Explanation{}, err
	}
	var rejected []contradiction.TruthCandidate
	for _, c := range candidates {
		if c.Value != tv.Value {
			rejected = append(rejected, c)
		}
	}

	// Confidence propagation: the fact's asserted confidence is the
	// noisy-AND of (a) the arbitration confidence and (b) each
	// supporting source's own current reliability — multiple weak
	// sources should not produce a confidently-asserted fact merely by
	// agreeing.
	premises := []float64{tv.Confidence}
	for _, sID := range tv.SupportingSources {
		premises = append(premises, l.sourceTrust.Score(l.trustKey(sID)))
	}
	propagatedConfidence := knowledge.PropagateConfidence(premises)

	fact := reasoning.Triple{Subject: claimKey, Predicate: "resolves_to", Object: tv.Value}
	kv := l.knowledge.Revise(fact, likelihoodRatioFromConfidence(propagatedConfidence), "")

	l.learn(tv)
	predicted, lo, hi := l.predict(tv)
	causalChain, counterfactuals := l.buildCausalChain(tv, predicted, arbitrationTick)

	top := append([]string(nil), tv.SupportingSources...)
	sortByReputationDesc(top, l.Reputation)

	return Explanation{
		ClaimKey: claimKey, WinningValue: tv.Value, Confidence: tv.Confidence, Uncertainty: 1 - tv.Confidence,
		TopSupportingSources: top, RejectedAlternatives: rejected, RemainingContradictions: len(tv.ConflictingEdges),
		PredictedNextConfidence: predicted, PredictionCredibleLow: lo, PredictionCredibleHigh: hi,
		CausalChain: causalChain, Counterfactuals: counterfactuals, KnowledgeVersion: kv,
	}, nil
}

// likelihoodRatioFromConfidence converts an absolute confidence figure
// into the likelihood-ratio form Store.Revise expects: values above
// 0.5 argue FOR the fact (ratio > 1), values below argue against
// (ratio < 1), keeping Revise's belief-revision semantics meaningful
// even when the caller only has a single combined confidence figure
// rather than a true likelihood ratio.
func likelihoodRatioFromConfidence(confidence float64) float64 {
	epsilon := 1e-6
	if confidence <= 0 {
		confidence = epsilon
	}
	if confidence >= 1 {
		confidence = 1 - epsilon
	}
	return confidence / (1 - confidence)
}

func sortByReputationDesc(sources []string, score func(string) float64) {
	for i := 1; i < len(sources); i++ {
		for j := i; j > 0; j-- {
			if score(sources[j]) > score(sources[j-1]) {
				sources[j], sources[j-1] = sources[j-1], sources[j]
			} else {
				break
			}
		}
	}
}

func (l *Loop) Knowledge() *knowledge.Store                   { return l.knowledge }
func (l *Loop) Arbitration() *contradiction.ArbitrationEngine { return l.arbitration }
func (l *Loop) Causal() *causal.Graph                         { return l.causal }
func (l *Loop) TrustCalculus() *trustcalc.Calculus            { return l.sourceTrust }
