// Package intent closes the one ITIOS pillar this repo genuinely did
// not have before this session: Intent Verification.
//
//	Intent -> Expected Outcome -> Observed Outcome -> Deviation -> Trust Delta
//
// It is deliberately thin. VERIQO already has a real Decision
// Intelligence engine (pkg/moat/decision, including WHY/WHY-NOT via
// ExplainBreakdown and Counterfactual) and a real hash-chained Trust
// Kernel (pkg/trust). Building a THIRD, parallel scoring system here
// would duplicate both and contradict the project's own DARI principle
// (append-only, single source of truth per concern). Intent
// Verification's actual job is narrower and different from either:
// given what a decision *expected* to happen, and what *actually*
// happened, compute how far reality deviated from intent, and feed
// that deviation into the existing Trust Kernel as evidence — closing
// the loop from "we decided X" to "X worked, and here's the receipt".
package intent

import (
	"fmt"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/trust"
)

// Statement is what the system intended to happen, declared before
// execution — the ITIOS pipeline's actual starting point, upstream of
// the Decision engine's RiskScore/Action output.
type Statement struct {
	ID             string
	ActorID        string
	Subject        string
	ExpectedAction decision.Action
	ExpectedRisk   float64 // the risk score the Decision engine predicted
	Tick           uint64
}

// Observation is what actually happened, measured after execution —
// independent of what was predicted, the same way pkg/verification
// insists on an independently-derived certificate rather than a
// self-reported one.
type Observation struct {
	ObservedAction decision.Action
	ObservedRisk   float64 // a re-measured or ground-truth risk score
	Tick           uint64
}

// Verdict is the result of comparing Statement to Observation: the
// actual Deviation, and the TrustDelta it produces once fed through
// the real Trust Kernel (not a new scoring formula — this reuses
// trust.Kernel.Evaluate so the number means the same thing everywhere
// else in the repo it appears).
type Verdict struct {
	IntentID       string
	ActionMatched  bool    // did the observed action equal the expected action?
	RiskDeviation  float64 // observed - expected, signed: positive means reality was riskier than predicted
	AbsDeviation   float64
	TrustDelta     float64 // this verdict's contribution to subject trust, via trust.Kernel
	TrustScore     trust.TrustScore
	VerifiedAtTick uint64
}

// accuracyPolicy is the one trust.TrustPolicy this package registers:
// a subject's Intent-Verification trust is exactly "how consistently
// do outcomes match what was predicted for this subject" — built from
// two rules over the SAME evidence keys the Verifier below always
// supplies, so it is fully deterministic and replay-stable.
func accuracyPolicy() trust.TrustPolicy {
	return trust.TrustPolicy{
		Name: "intent-verification-accuracy",
		Rules: []trust.TrustRule{
			trust.RuleFromEvidenceKey("action_matched", 0.6, "action_matched"),
			trust.RuleInverse("low_risk_deviation", 0.4, "abs_risk_deviation"),
		},
		CertifyThreshold: 0.7,
	}
}

// accuracyPolicyWithCalculus extends accuracyPolicy with a third rule
// that folds the SHARED trustcalc.Calculus belief for this subject
// into the certified score. This is the formal answer to the audit
// finding ("Hasil_audit_brutal...docx" §6-9): Intent's Verifier used
// only its own private trust.Kernel while Evidence/Intelligence shared
// one trustcalc.Calculus, so two trust universes ran in parallel. The
// fix is not to delete trust.Kernel (it still owns the
// certificate/policy/ledger machinery nothing else replaces) — it is
// to make the relationship formal: Calculus -> Trust Kernel ->
// Certificate/Ledger, exactly the pipeline the audit asked for,
// implemented as a real weighted rule (not a cosmetic extra field).
func accuracyPolicyWithCalculus() trust.TrustPolicy {
	return trust.TrustPolicy{
		Name: "intent-verification-accuracy",
		Rules: []trust.TrustRule{
			trust.RuleFromEvidenceKey("action_matched", 0.4, "action_matched"),
			trust.RuleInverse("low_risk_deviation", 0.3, "abs_risk_deviation"),
			trust.RuleFromEvidenceKey("shared_calculus_belief", 0.3, "shared_calculus_belief"),
		},
		CertifyThreshold: 0.7,
	}
}

// Verifier runs the Intent Verification loop and owns the Trust Kernel
// instance its verdicts feed — one Verifier per subject population
// (e.g. per fleet, per tenant), matching how trust.Kernel is meant to
// be scoped. calc, when non-nil (see NewVerifierWithCalculus), is the
// SAME *trustcalc.Calculus instance veriqo/kernel.Kernel hands to the
// Evidence Engine and Intelligence Loop — every Verify() call both
// writes an Observe() into that shared belief AND reads it back into
// the certified trust score, so an Intent outcome for subject S
// changes the exact belief Evidence/Intelligence will use for S next,
// not a disconnected copy.
type Verifier struct {
	trustKernel *trust.Kernel
	calc        *trustcalc.Calculus
}

func NewVerifier() (*Verifier, error) {
	k := trust.NewKernel()
	if err := k.RegisterPolicy(accuracyPolicy()); err != nil {
		return nil, fmt.Errorf("intent: registering accuracy policy: %w", err)
	}
	return &Verifier{trustKernel: k}, nil
}

// NewVerifierWithCalculus builds a Verifier wired to a shared
// trustcalc.Calculus — the constructor veriqo/kernel.Kernel uses so
// Intent participates in the SAME trust calculus as Evidence and
// Intelligence, per the audit's explicit P0 priority. calc must be
// non-nil; callers that do not have (or do not want) a shared calculus
// should use NewVerifier instead, which is unchanged and still fully
// functional on its own.
func NewVerifierWithCalculus(calc *trustcalc.Calculus) (*Verifier, error) {
	if calc == nil {
		return nil, fmt.Errorf("intent: NewVerifierWithCalculus requires a non-nil shared trustcalc.Calculus")
	}
	k := trust.NewKernel()
	if err := k.RegisterPolicy(accuracyPolicyWithCalculus()); err != nil {
		return nil, fmt.Errorf("intent: registering accuracy policy: %w", err)
	}
	return &Verifier{trustKernel: k, calc: calc}, nil
}

// SharedCalculus returns the Verifier's shared trustcalc.Calculus, or
// nil if it was built with NewVerifier (no shared calculus).
func (v *Verifier) SharedCalculus() *trustcalc.Calculus { return v.calc }

// Verify closes the loop: Intent -> Expected -> Observed -> Deviation
// -> Trust Delta. abs_risk_deviation is clamped to [0,1] before being
// handed to the trust rule (RuleInverse expects evidence already on a
// meaningful scale; a raw unbounded deviation would let one wild
// observation dominate the trust score, which is not the property an
// investor-facing trust number should have).
func (v *Verifier) Verify(stmt Statement, obs Observation) (Verdict, error) {
	matched := stmt.ExpectedAction == obs.ObservedAction
	rawDev := obs.ObservedRisk - stmt.ExpectedRisk
	absDev := rawDev
	if absDev < 0 {
		absDev = -absDev
	}
	clamped := absDev
	if clamped > 1 {
		clamped = 1
	}

	matchedEvidence := 0.0
	if matched {
		matchedEvidence = 1.0
	}
	evidence := trust.TrustEvidence{
		"action_matched":     matchedEvidence,
		"abs_risk_deviation": clamped,
	}
	if v.calc != nil {
		// P0 fix: this Verify() call is an independently re-measured
		// outcome for stmt.Subject (see package doc). Write it into the
		// SAME Calculus Evidence/Intelligence share (not a bridge
		// wrapper — a direct Observe on the shared object), then fold
		// the freshly-updated belief back into this certificate's own
		// score via the "shared_calculus_belief" rule registered in
		// accuracyPolicyWithCalculus.
		if _, err := v.calc.Observe(trustcalc.NamespacedSubject(trustcalc.NamespaceIntentActor, stmt.Subject), matched, 1.0); err != nil {
			return Verdict{}, fmt.Errorf("intent: shared calculus observe: %w", err)
		}
		evidence["shared_calculus_belief"] = v.calc.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceIntentActor, stmt.Subject))
	}

	before := v.currentScore(stmt.Subject)
	policyName := "intent-verification-accuracy"
	score, err := v.trustKernel.Evaluate(stmt.Subject, policyName, evidence, obs.Tick)
	if err != nil {
		return Verdict{}, fmt.Errorf("intent: trust evaluation: %w", err)
	}

	return Verdict{
		IntentID:       stmt.ID,
		ActionMatched:  matched,
		RiskDeviation:  rawDev,
		AbsDeviation:   absDev,
		TrustDelta:     score.Score - before,
		TrustScore:     score,
		VerifiedAtTick: obs.Tick,
	}, nil
}

// Certify additionally issues a hash-chained TrustCertificate if the
// subject's accuracy score clears the policy's CertifyThreshold —
// exposed separately from Verify so a caller can decide whether a
// single verdict should mint a certificate or just update the running
// score.
func (v *Verifier) Certify(stmt Statement, obs Observation) (Verdict, *trust.TrustCertificate, error) {
	verdict, err := v.Verify(stmt, obs)
	if err != nil {
		return Verdict{}, nil, err
	}
	matchedEvidence := 0.0
	if verdict.ActionMatched {
		matchedEvidence = 1.0
	}
	certEvidence := trust.TrustEvidence{
		"action_matched":     matchedEvidence,
		"abs_risk_deviation": verdict.AbsDeviation,
	}
	if v.calc != nil {
		// v.Verify above already Observe()'d into the shared calculus;
		// read back the same post-update belief so Certify's score is
		// computed on identical evidence to Verify's, not a stale copy.
		certEvidence["shared_calculus_belief"] = v.calc.Score(trustcalc.NamespacedSubject(trustcalc.NamespaceIntentActor, stmt.Subject))
	}
	_, cert, err := v.trustKernel.Certify(stmt.Subject, "intent-verification-accuracy", certEvidence, obs.Tick)
	if err != nil {
		return verdict, nil, fmt.Errorf("intent: certify: %w", err)
	}
	return verdict, cert, nil
}

func (v *Verifier) currentScore(subjectID string) float64 {
	certs := v.trustKernel.CertificatesFor(subjectID)
	if len(certs) == 0 {
		return 0
	}
	return certs[len(certs)-1].Score
}

// Ledger exposes the underlying hash-chained trust certificate ledger
// for external audit — the same VerifyLedger() discipline used
// everywhere else evidence is chained in this repo.
func (v *Verifier) Ledger() []trust.TrustCertificate { return v.trustKernel.Ledger() }
func (v *Verifier) VerifyLedger() error              { return v.trustKernel.VerifyLedger() }

// RestoreLedger reloads a previously-persisted, hash-verified trust
// ledger into this Verifier's internal trust.Kernel — delegates
// straight to trust.Kernel.RestoreLedger, so Intent-Verification
// accuracy certificates issued in an earlier process continue the
// same chain after a restart.
func (v *Verifier) RestoreLedger(certs []trust.TrustCertificate) error {
	return v.trustKernel.RestoreLedger(certs)
}
