// Package risk implements VERIQO's composite Trade-Based Money
// Laundering (TBML) / sanctions-evasion risk model — CRITICAL_FINDINGS'
// "Missing Risk Models" category, specifically "Sanctions evasion
// scoring" and "TBML detection".
//
// Honest scope: this is an explainable, deterministic combination of
// signals already produced elsewhere in the kernel (pattern-library
// hits from pkg/moat/intelligence/patterns, price anomaly from
// pkg/moat/intelligence/pricing, evidence-provenance independence from
// pkg/evidence/graph, and calibration trust weight from
// pkg/moat/calibration) into one composite score with a full factor
// breakdown. It is NOT a machine-learned model trained on labeled TBML
// cases — no such labeled dataset exists in this environment
// (CRITICAL_FINDINGS' "Missing Ground Truth" category names this
// explicitly). What this package closes is the *architecture*: given
// real signals, produce one explainable composite score that is
// consumable by the existing decision.Engine (as named factors) and
// the existing calibration.Engine (as a Prediction awaiting ground
// truth) — so when a real labeled case does arrive, closing the loop is
// a data-wiring exercise, not a redesign.
package risk

import (
	"context"
	"errors"
	"strconv"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/provenance"
	"veriqo/pkg/platform/telemetry"
)

// CompositeInput is every named signal the risk model combines. Zero
// value for any field means "no information available for this input"
// and it contributes zero weight (rather than being silently treated
// as a full-confidence negative), so partial evidence never masquerades
// as a clean bill of health.
type CompositeInput struct {
	// PatternScore is patterns.AggregateScore() over a Signal
	// evaluation — sanctions-evasion/transshipment behavioral score.
	PatternScore float64
	// PriceAnomalyScore is pricing.AnomalyResult.AnomalyScore() — TBML
	// over/under-invoicing indicator.
	PriceAnomalyScore float64
	// ProvenanceIndependenceRatio is a caller-asserted independence
	// figure. DEPRECATED as of v7.10: prefer SourceIDs + a
	// provenance.Graph so independence is COMPUTED (v7.9 audit P0
	// "Provenance dihitung, bukan diberikan"), not asserted. This field
	// remains for callers with no provenance.Graph available; when
	// SourceIDs is non-empty AND a Graph is supplied to
	// ScoreWithProvenance, the computed ratio overrides this field.
	ProvenanceIndependenceRatio float64
	// provenanceAssessment is the full epistemic assessment
	// ScoreWithProvenance computed, when it computed one. Unexported: a
	// caller must not be able to assert one, for exactly the reason
	// ProvenanceIndependenceRatio itself may not be asserted on the
	// canonical path (see ScoreCanonical).
	provenanceAssessment *provenance.IndependenceAssessment
	// HasProvenanceData is false when the caller has no provenance
	// assessment available; when false, ProvenanceIndependenceRatio is
	// ignored (treated as neutral 1.0) rather than penalizing the score
	// for missing metadata unrelated to the underlying risk.
	HasProvenanceData bool
	// SourceIDs is every evidence source PatternScore/PriceAnomalyScore
	// were computed from — required for ScoreWithProvenance's computed
	// independence; ignored by the plain Score method.
	SourceIDs []string
	// provenanceComputed is set ONLY by ScoreWithProvenance when it
	// actually computed ProvenanceIndependenceRatio from a
	// provenance.Graph. Unexported so it can never be set directly by
	// a caller asserting it — the only way to make ScoreCanonical
	// accept HasProvenanceData=true is to go through
	// ScoreWithProvenance first. This is the v7.10 audit fix for
	// "caller-asserted provenance masih bisa lewat legacy Risk API":
	// the canonical entry point now mechanically rejects an asserted-
	// only ratio instead of merely documenting that it shouldn't be
	// used.
	provenanceComputed bool
}

// FactorContribution mirrors decision.FactorContribution's shape
// (this package intentionally does not import a private type, to stay
// decoupled) — one named input's contribution to the composite score.
type FactorContribution struct {
	Name         string
	RawValue     float64
	Weight       float64
	Contribution float64
}

// Label is a human-readable risk tier.
type Label string

const (
	LabelLow      Label = "LOW"
	LabelMedium   Label = "MEDIUM"
	LabelHigh     Label = "HIGH"
	LabelCritical Label = "CRITICAL"
)

// Result is the composite risk model's output.
type Result struct {
	Score       float64
	Label       Label
	Breakdown   []FactorContribution
	Explanation []string

	// The four fields below carry the EPISTEMIC standing of the
	// independence term this score was computed with — the
	// canonical-truth-path mandate's section IV applied to the one place
	// in this repository where an independence ratio is genuinely
	// load-bearing. They are populated only by ScoreWithProvenance (the
	// canonical path); a plain Score call leaves them zero, which is the
	// honest answer for a caller that supplied a bare asserted ratio.
	//
	// ProvenanceVerifiedIndependent is the ONLY field a consumer may read
	// as "these sources are independent". ProvenanceStatus/Basis/
	// PairCount explain why it is what it is.
	ProvenanceStatus              provenance.ProvenanceStatus
	ProvenanceBasis               provenance.EvidenceBasis
	ProvenancePairCount           int
	ProvenanceVerifiedIndependent bool
}

// Model computes composite TBML/sanctions-evasion risk. Thresholds are
// fields, not constants, so an operator can tune them per deployment
// (the same "knowledge artifact, not buried constant" discipline
// pkg/moat/decision.Policy already established).
type Model struct {
	FlagThreshold     float64
	EscalateThreshold float64
	CriticalThreshold float64
}

// NewModel returns a Model with defaults consistent with
// decision.Policy's existing convention (Flag/Escalate) plus one
// additional Critical tier for the composite risk model specifically.
func NewModel() Model {
	return Model{FlagThreshold: 0.4, EscalateThreshold: 0.65, CriticalThreshold: 0.85}
}

// ScoreWithProvenance is like Score but computes provenance
// independence from g (a shared provenance.Graph) over in.SourceIDs
// instead of trusting a caller-asserted ratio — the v7.9 audit P0 fix.
// When in.SourceIDs is empty, falls back to Score's legacy
// caller-asserted behavior (so existing callers with no graph wiring
// are unaffected).
// WHY THE DISCOUNT IS NOT RECALIBRATED FOR UNVERIFIED INDEPENDENCE.
// The canonical-truth-path mandate's section IV asks that nowhere in
// the codebase read a bare independence Score as if it meant
// "independent" -- a Ratio of 1.0 arises trivially when there was no
// source pair to compare at all. This function was audited against that
// requirement in round R24 and deliberately LEFT its arithmetic
// unchanged, for a reason worth stating rather than assuming:
//
// the discount is `0.5 + 0.5*independence`, so a HIGH independence
// value RAISES the composite risk and a low one LOWERS it. Treating an
// unverified 1.0 as full independence is therefore the CONSERVATIVE
// direction: it produces the highest score the inputs can justify and
// cannot cause an under-flag. "Correcting" it by discounting
// unestablished independence would systematically lower risk scores for
// exactly the cases with the thinnest provenance, which is the opposite
// of fail-safe.
//
// What WAS wrong, and is fixed, is the INTERPRETATION: a reader of a
// Result could not tell whether the independence term rested on a real
// pairwise comparison or on nothing. Result now carries
// ProvenanceBasis, ProvenancePairCount and ProvenanceVerifiedIndependent,
// and Explanation says so in words. See Assess/IsVerifiedIndependent.
func (m Model) ScoreWithProvenance(in CompositeInput, g *provenance.Graph) (Result, error) {
	if g != nil && len(in.SourceIDs) > 0 {
		// Assess, not ComputeIndependence: the same ratio, plus the
		// status/basis/pair-count that make it readable.
		a, err := g.Assess(in.SourceIDs)
		if err != nil {
			return Result{}, err
		}
		in.ProvenanceIndependenceRatio = a.Score
		in.HasProvenanceData = true
		in.provenanceComputed = true
		in.provenanceAssessment = &a
	}
	return m.Score(in), nil
}

// ErrAssertedProvenanceOnCanonicalPath is returned by ScoreCanonical
// when the caller claims HasProvenanceData without having gone
// through ScoreWithProvenance to compute it.
var ErrAssertedProvenanceOnCanonicalPath = errors.New("risk: caller-asserted provenance is not permitted on the canonical scoring path; call ScoreWithProvenance(in, graph) first, or leave HasProvenanceData false")

// ScoreCanonical is the production entry point decision.Engine wiring
// SHOULD use: it mechanically refuses to score an input that claims
// HasProvenanceData=true without that ratio having actually been
// computed by ScoreWithProvenance from a real provenance.Graph — v7.10
// audit fix, see CompositeInput.provenanceComputed doc. Score() itself
// remains available and unrestricted for legacy/no-provenance callers
// and internal use by ScoreWithProvenance, but is no longer the path
// production decision wiring should call directly when provenance data
// might be present.
func (m Model) ScoreCanonical(in CompositeInput) (Result, error) {
	if in.HasProvenanceData && !in.provenanceComputed {
		return Result{}, ErrAssertedProvenanceOnCanonicalPath
	}
	return m.Score(in), nil
}

// Score computes the composite result. The weighting is a fixed,
// documented formula (not learned) so every number in Breakdown is
// independently reproducible from CompositeInput alone — required for
// the existing IVF (Independent Verification Function) replay guarantee
// the rest of the kernel provides.
func (m Model) Score(in CompositeInput) Result {
	_, span := telemetry.StartSpan(context.Background(), "risk.Score")
	defer span.End()

	independence := 1.0
	if in.HasProvenanceData {
		independence = clamp01(in.ProvenanceIndependenceRatio)
	}

	// Base weighted combination of the two primary signals.
	const patternWeight = 0.6
	const priceWeight = 0.4
	rawComposite := patternWeight*clamp01(in.PatternScore) + priceWeight*clamp01(in.PriceAnomalyScore)

	// Provenance independence acts as a multiplicative discount, not
	// an additive factor: a HIGH pattern score built entirely on
	// correlated/dependent sources should be discounted toward
	// uncertainty, not treated as equally confident to one built on
	// independent sources. A floor of 0.5 prevents total independence
	// data (rare, single-source cases) from zeroing out an otherwise
	// strong signal — it discounts confidence, it does not erase it.
	discount := 0.5 + 0.5*independence
	composite := clamp01(rawComposite * discount)

	label := LabelLow
	switch {
	case composite >= m.CriticalThreshold:
		label = LabelCritical
	case composite >= m.EscalateThreshold:
		label = LabelHigh
	case composite >= m.FlagThreshold:
		label = LabelMedium
	}

	breakdown := []FactorContribution{
		{Name: "sanctions_evasion_pattern_score", RawValue: in.PatternScore, Weight: patternWeight, Contribution: patternWeight * clamp01(in.PatternScore)},
		{Name: "price_anomaly_score", RawValue: in.PriceAnomalyScore, Weight: priceWeight, Contribution: priceWeight * clamp01(in.PriceAnomalyScore)},
		{Name: "provenance_independence_discount", RawValue: independence, Weight: 0, Contribution: discount},
	}

	expl := []string{}
	if in.PatternScore >= 0.5 {
		expl = append(expl, "Sanctions-evasion / transshipment pattern library returned a high aggregate score.")
	}
	if in.PriceAnomalyScore >= 0.5 {
		// v7.9 §11: price anomaly is reframed explicitly as a
		// CONTRIBUTION to TBML evidence, not a direct TBML finding —
		// many legitimate factors (grade, incoterms, freight, spot vs
		// contract) can produce a price deviation.
		expl = append(expl, "Commodity price deviates significantly from reference distribution — a potential valuation-anomaly CONTRIBUTION to TBML risk (not itself proof of TBML; legitimate causes include grade, incoterms, freight, and spot-vs-contract pricing).")
	}
	if in.HasProvenanceData && independence < 0.5 {
		expl = append(expl, "Contributing evidence sources are largely provenance-dependent (shared upstream) — composite score discounted for reduced independent confirmation.")
	}
	// Section IV: an independence term that rests on nothing compared
	// must SAY so, in the explanation a human reads, rather than
	// presenting a trivial 1.0 as a finding.
	if a := in.provenanceAssessment; a != nil && !a.IsVerifiedIndependent() {
		expl = append(expl, "Source independence is NOT established ("+string(a.Status)+
			", basis "+string(a.Basis)+", "+strconv.Itoa(a.PairCount)+
			" source pair(s) compared, attestation_complete="+strconv.FormatBool(a.AttestationComplete)+
			"). The independence term in this score is therefore "+provenance.ScoreNotInterpretable+
			" as a claim about the world; it is applied in the conservative direction (no discount), "+
			"which cannot lower this score.")
	}
	if len(expl) == 0 {
		expl = append(expl, "No individual factor crossed its own significance threshold.")
	}

	out := Result{Score: composite, Label: label, Breakdown: breakdown, Explanation: expl}
	if a := in.provenanceAssessment; a != nil {
		out.ProvenanceStatus = a.Status
		out.ProvenanceBasis = a.Basis
		out.ProvenancePairCount = a.PairCount
		out.ProvenanceVerifiedIndependent = a.IsVerifiedIndependent()
	}
	return out
}

// ToDecisionValues projects a computed Result into the
// map[string]float64 vocabulary decision.Engine.Decide expects.
//
// v7.9 §3 fix ("bridge belum sepenuhnya semantic-consistent"): this
// now emits ONE canonical factor — the Result's own already-discounted
// Score — instead of re-exposing PatternScore/PriceAnomalyScore
// separately for the Decision Engine to recombine with its own
// weighted-sum formula. The prior version let Risk Model say
// "Pattern+Price+Provenance -> Composite A" while Decision Engine
// independently recomputed "Pattern+Price -> Score B" with NO
// provenance discount at all, so A != B for the same input — exactly
// the audit's "canonical projection, not recompute" finding. Decide is
// a generic weighted-factor engine used by many callers, so rather
// than teach it a second (multiplicative) formula, risk hands it the
// single already-final number as a weight-1.0 factor: Decision now
// mechanically reproduces Risk Model's own Score, not a divergent
// second opinion.
func ToDecisionValues(res Result) map[string]float64 {
	return map[string]float64{
		"tbml_composite_risk_score": res.Score,
	}
}

// DefaultPolicy returns a decision.Policy consuming EXACTLY the single
// canonical factor ToDecisionValues produces, using the same
// Flag/Escalate convention as Model's own thresholds — so
// Decision.RiskScore is mechanically IDENTICAL to Result.Score for the
// same input (see TestDecisionConsistency), not a second, divergent
// computation.
func DefaultPolicy() decision.Policy {
	return decision.Policy{
		Name: "tbml_sanctions_evasion_v2",
		Factors: []decision.FactorWeight{
			{Name: "tbml_composite_risk_score", Weight: 1.0},
		},
		FlagThreshold:     0.4,
		EscalateThreshold: 0.65,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
