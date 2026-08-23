// trust.go closes WAVE A item 2 of the canonical-truth-path mandate:
//
//	"Trust module exists != Trust is operationally part of decision."
//
// What already existed before this file, and is deliberately NOT
// rebuilt here:
//
//   - pkg/trust/state.Engine is a complete, tested trust state machine
//     over an append-only, hash-chained transition ledger, with decay,
//     terminal revocation, escalation and per-tick StateAt. This file
//     calls it; it does not reimplement any part of it.
//   - pkg/canonical.Pipeline.Trust (*trustcalc.Calculus) already existed
//     as a field. The mandate's finding was exact: RunCanonical never
//     read it. That is closed here too — recordTrustObservations folds
//     the same per-source trust posture into the shared Calculus
//     namespace every other engine on the Kernel reads.
//   - pkg/canonical/dependency.go already owns the ONE seam where a
//     source's evidence weight is decided (resolveDependencies ->
//     fusion.RegisterSource). Trust weighting is applied at that same
//     seam rather than inside fusion, for exactly the reasons
//     dependency.go's own package comment gives.
//
// WHY UNKNOWN DOES NOT DOWNWEIGHT. The mandate's minimum policy is
// "VERIFIED -> normal weighting; UNKNOWN -> restricted / mandatory human
// review; REVOKED -> evidence cannot influence the decision". This file
// implements UNKNOWN as a RESTRICTED POSTURE (human review required,
// recorded in the certificate and the execution root) at weight 1.0,
// not as a numeric penalty. That is a deliberate, statable position:
// "we have never assessed this provider" is not evidence that its
// report is worth less, it is evidence that VERIQO may not act on it
// unreviewed. A numeric penalty would additionally have silently
// rescaled every existing case in the repository the moment trust was
// wired in, which would have made the change unauditable — and would
// have asserted a quantitative belief (0.5x) that nothing supports.
// Adverse states that VERIQO HAS assessed (DEGRADED, SUSPECT) do carry
// a real numeric penalty, and REVOKED is a hard exclusion.
package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"veriqo/pkg/core/trustcalc"
	"veriqo/pkg/trust/state"
)

// ErrAllEvidenceUntrusted is returned when trust weighting excluded
// EVERY source in a case — a fail-closed outcome, not a decision made
// on nothing. It is the mandate's "REVOKED provider's evidence cannot
// influence the decision at all" taken to its limit: if that leaves no
// evidence, there is no decision, not a decision from an empty set.
var ErrAllEvidenceUntrusted = errors.New("canonical: every source was excluded by trust policy; refusing to decide on no evidence")

// TrustPosture is what the trust policy concluded about one source's
// participation in this decision.
type TrustPosture string

const (
	// PostureNormal: the subject has a positive, assessed trust state.
	// Evidence participates at full weight and needs no extra review.
	PostureNormal TrustPosture = "NORMAL"
	// PostureRestricted: the subject's evidence participates, but the
	// decision may not be released without human review. This is what
	// an UNKNOWN (never-assessed) provider gets, and what an assessed
	// but declining one gets alongside a numeric penalty.
	PostureRestricted TrustPosture = "RESTRICTED"
	// PostureExcluded: the subject's evidence is removed from the case
	// before fusion ever sees it. A REVOKED provider gets this.
	PostureExcluded TrustPosture = "EXCLUDED"
)

// TrustRule is one row of the trust weighting policy: what a given
// pkg/trust/state.Level means for evidence weight and review posture.
// The policy is data, not a switch buried in a function, so it can be
// read, diffed, hashed and replayed — the same discipline
// pkg/execution's `graph` var already applies to the DAG shape.
type TrustRule struct {
	Level   state.Level  `json:"level"`
	Weight  float64      `json:"weight"`
	Posture TrustPosture `json:"posture"`
	Why     string       `json:"why"`
}

// TrustPolicy is the named, versioned, hashable set of TrustRules a
// case was decided under.
type TrustPolicy struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []TrustRule `json:"rules"`
}

// DefaultTrustPolicy is the mandate's minimum policy, made explicit.
// Every level pkg/trust/state models has a row: an unmapped level would
// be an invisible hole, so ruleFor fails closed to EXCLUDED rather than
// defaulting to permissive.
func DefaultTrustPolicy() TrustPolicy {
	return TrustPolicy{
		Name:    "veriqo.canonical.trust_weighting",
		Version: "v1",
		Rules: []TrustRule{
			{state.LevelTrusted, 1.0, PostureNormal,
				"assessed and currently trusted: normal evidence weighting"},
			{state.LevelProvisional, 1.0, PostureNormal,
				"assessed on thin evidence but not adverse: normal weighting"},
			{state.LevelUnknown, 1.0, PostureRestricted,
				"never assessed: evidence is not devalued, but the decision may not be released without human review"},
			{state.LevelDegraded, 0.5, PostureRestricted,
				"measurable decline in assessed trust: evidence weight halved and human review required"},
			{state.LevelSuspect, 0.25, PostureRestricted,
				"contradicted or escalated: evidence weight heavily discounted and human review required"},
			{state.LevelRevoked, 0.0, PostureExcluded,
				"terminal revocation: this source's evidence cannot influence a new decision at all"},
		},
	}
}

// Hash is the policy's content commitment — folded into the trust
// evaluation's own RootHash, so a case cannot be replayed under a
// different trust policy without detection.
func (p TrustPolicy) Hash() string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.trust_policy/v1|name=%s|version=%s|", p.Name, p.Version)
	rules := append([]TrustRule(nil), p.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].Level < rules[j].Level })
	for _, r := range rules {
		fmt.Fprintf(h, "level=%s|weight=%s|posture=%s|", r.Level,
			strconv.FormatFloat(r.Weight, 'g', 17, 64), r.Posture)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (p TrustPolicy) ruleFor(level state.Level) TrustRule {
	for _, r := range p.Rules {
		if r.Level == level {
			return r
		}
	}
	// Fail closed: a level this policy does not model is excluded, never
	// silently admitted at full weight.
	return TrustRule{Level: level, Weight: 0, Posture: PostureExcluded,
		Why: "trust level " + string(level) + " is not modelled by this policy; failing closed"}
}

// SourceTrust is one source's trust posture in this case.
type SourceTrust struct {
	SourceID string       `json:"source_id"`
	Subject  string       `json:"subject"` // the trust-ledger subject this source was evaluated as
	Level    state.Level  `json:"level"`
	Score    float64      `json:"score"`  // the trust engine's decayed effective score
	Weight   float64      `json:"weight"` // the multiplicative factor applied to evidence weight
	Posture  TrustPosture `json:"posture"`
	Reason   string       `json:"reason"`
	// Certificate is the hash of the trust transition that produced this
	// state, empty when the subject has never been assessed.
	Certificate string `json:"certificate"`
}

// TrustEvaluation is the auditable artifact of one trust pass over a
// case's sources — the trust counterpart of DependencyEvaluation, and
// deliberately the same shape of thing: what was decided, per source,
// under which named policy, committed to one RootHash.
type TrustEvaluation struct {
	// Configured is false only when no trust engine is attached to the
	// Pipeline at all. It is never used to soften a finding — it is the
	// honest statement that this deployment has no trust authority, which
	// a consumer must be able to tell apart from "every subject was
	// assessed and found fine".
	Configured bool `json:"configured"`
	// CaseSubject is the trust subject of the case as a whole (the
	// resolved entity under investigation), distinct from the per-source
	// provider subjects below.
	CaseSubject string      `json:"case_subject"`
	CaseLevel   state.Level `json:"case_level"`
	// Sources is one entry per submitted source, in sorted source-ID
	// order.
	Sources []SourceTrust `json:"sources"`
	// Excluded names every source whose evidence was removed before
	// fusion, in sorted order.
	Excluded []string `json:"excluded"`
	// ReviewRequired is true when ANY source landed in a RESTRICTED or
	// EXCLUDED posture. It is the mandate's "mandatory human review".
	ReviewRequired bool `json:"review_required"`
	// ReviewReasons explains, per source, why review is required.
	ReviewReasons []string `json:"review_reasons"`
	// PolicyHash and LedgerHead are the two commitments that make this
	// evaluation replayable and tamper-evident: which policy decided it,
	// and which trust-ledger state it was decided against.
	PolicyHash string `json:"policy_hash"`
	LedgerHead string `json:"ledger_head"`
	// RootHash commits to everything above.
	RootHash string `json:"root_hash"`
}

// TrustSubjectFor is the ONE rule mapping a submission to the trust
// subject it is evaluated as. A submission's Provider (the organisation
// operating the feed) is the trust-bearing party when declared;
// otherwise the source ID itself is. Exported because pkg/lifecycle and
// pkg/rwc must be able to register trust for exactly the subject the
// canonical path will look up, with no second, drifting copy of this
// rule.
func TrustSubjectFor(s SourceSubmission) string {
	if s.Provider != "" {
		return s.Provider
	}
	return string(s.SourceID)
}

// evaluateTrust reads the trust state of every source's subject at the
// case's tick and applies the policy. It is a pure read of the trust
// ledger: it appends nothing, so two runs over the same ledger and tick
// produce byte-identical evaluations, which is what makes cold replay
// of trust possible at all (see pkg/replay's TrustLedger field).
func (p *Pipeline) evaluateTrust(in CaseInput) TrustEvaluation {
	policy := p.TrustPolicy
	if len(policy.Rules) == 0 {
		policy = DefaultTrustPolicy()
	}
	ev := TrustEvaluation{
		Configured:  p.TrustState != nil,
		CaseSubject: in.Subject,
		CaseLevel:   state.LevelUnknown,
		PolicyHash:  policy.Hash(),
	}

	subs := append([]SourceSubmission(nil), in.Submissions...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].SourceID < subs[j].SourceID })

	if p.TrustState == nil {
		// No trust authority attached. Every source is recorded as
		// UNCONFIGURED at full weight — deliberately NOT as "trusted",
		// and deliberately NOT review-required, because a deployment that
		// has not wired trust at all has not made a trust finding of any
		// kind and must not be reported as having made one. This is the
		// honest degenerate case, and it is exactly what every caller
		// constructed before this round looked like.
		for _, s := range subs {
			ev.Sources = append(ev.Sources, SourceTrust{
				SourceID: string(s.SourceID), Subject: TrustSubjectFor(s),
				Level: state.LevelUnknown, Weight: 1.0, Posture: PostureNormal,
				Reason: "no trust engine attached to this pipeline; trust did not participate",
			})
		}
		ev.RootHash = hashTrustEvaluation(ev)
		return ev
	}

	ev.LedgerHead = TrustLedgerHead(p.TrustState)
	if in.Subject != "" {
		ev.CaseLevel = p.TrustState.StateAt(in.Subject, in.Tick).Level
	}

	for _, s := range subs {
		subject := TrustSubjectFor(s)
		st := p.TrustState.StateAt(subject, in.Tick)
		rule := policy.ruleFor(st.Level)
		entry := SourceTrust{
			SourceID: string(s.SourceID), Subject: subject, Level: st.Level,
			Score: st.EffectiveScore, Weight: rule.Weight, Posture: rule.Posture,
			Reason: rule.Why, Certificate: st.Certificate,
		}
		ev.Sources = append(ev.Sources, entry)
		switch rule.Posture {
		case PostureExcluded:
			ev.Excluded = append(ev.Excluded, string(s.SourceID))
			ev.ReviewRequired = true
			ev.ReviewReasons = append(ev.ReviewReasons,
				string(s.SourceID)+" (subject "+subject+"): "+rule.Why)
		case PostureRestricted:
			ev.ReviewRequired = true
			ev.ReviewReasons = append(ev.ReviewReasons,
				string(s.SourceID)+" (subject "+subject+"): "+rule.Why)
		}
	}
	sort.Strings(ev.Excluded)
	ev.RootHash = hashTrustEvaluation(ev)
	return ev
}

func hashTrustEvaluation(ev TrustEvaluation) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.trust_evaluation/v1|configured=%v|case=%s|caselevel=%s|policy=%s|head=%s|review=%v|",
		ev.Configured, ev.CaseSubject, ev.CaseLevel, ev.PolicyHash, ev.LedgerHead, ev.ReviewRequired)
	for _, s := range ev.Sources {
		fmt.Fprintf(h, "src=%s|subj=%s|level=%s|score=%s|weight=%s|posture=%s|cert=%s|",
			s.SourceID, s.Subject, s.Level,
			strconv.FormatFloat(s.Score, 'g', 17, 64),
			strconv.FormatFloat(s.Weight, 'g', 17, 64), s.Posture, s.Certificate)
	}
	for _, x := range ev.Excluded {
		fmt.Fprintf(h, "excluded=%s|", x)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TrustLedgerHead is the single commitment to a trust engine's whole
// history: the hash of its last transition. It is what replay compares,
// what the execution root commits to, and therefore what changes the
// moment a trust ledger is tampered with. Exported so pkg/lifecycle and
// pkg/replay derive it the same way, from the same function.
//
// It is defined here rather than as a method on state.Engine because
// pkg/trust/state deliberately exposes its ledger as data (Ledger())
// and verifies it independently (VerifyChain()); a head is a view over
// that data, and keeping the view here means the state package stays a
// pure state machine with no notion of who is committing to it.
func TrustLedgerHead(e *state.Engine) string {
	if e == nil {
		return ""
	}
	l := e.Ledger()
	if len(l) == 0 {
		// A trust engine that exists but has recorded nothing is a real,
		// distinguishable state — not the same as no engine at all, which
		// is the empty string. Naming it explicitly keeps those two apart
		// in every hash this value feeds.
		return "veriqo.trust_ledger/v1:empty"
	}
	return l[len(l)-1].Hash
}

// applyTrustWeights folds the trust evaluation's per-source weight into
// the dependency evaluation's effective weights, and removes excluded
// sources from the case entirely.
//
// Order matters and is deliberate: dependency discounting runs FIRST
// (it answers "how much of this evidence is redundant"), then trust
// multiplies (it answers "how much may this source's evidence count at
// all"). Both can only ever LOWER a weight, never raise one, so the
// composition preserves dependency.go's own conservatism invariant.
func applyTrustWeights(dep DependencyEvaluation, tr TrustEvaluation, subs []SourceSubmission) ([]SourceSubmission, DependencyEvaluation, error) {
	excluded := make(map[string]bool, len(tr.Excluded))
	for _, id := range tr.Excluded {
		excluded[id] = true
	}
	weights := make(map[string]float64, len(tr.Sources))
	for _, s := range tr.Sources {
		weights[s.SourceID] = s.Weight
	}

	kept := make([]SourceSubmission, 0, len(subs))
	for _, s := range subs {
		if excluded[string(s.SourceID)] {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		return nil, dep, fmt.Errorf("%w: excluded=%v", ErrAllEvidenceUntrusted, tr.Excluded)
	}

	for id, eff := range dep.Effective {
		if excluded[id] {
			delete(dep.Effective, id)
			continue
		}
		f, ok := weights[id]
		if !ok {
			f = 1.0
		}
		w := eff * f
		if w < minEffectiveReliability {
			w = minEffectiveReliability
		}
		if w > eff {
			w = eff // defence in depth: trust may never inflate a weight
		}
		dep.Effective[id] = w
	}
	return kept, dep, nil
}

// recordTrustObservations closes the literal finding "RunCanonical
// tidak membaca Pipeline.Trust": the per-source trust posture this case
// was decided under is written into the SAME shared trustcalc.Calculus
// namespace every other engine on the Kernel reads
// (trustcalc.NamespaceEvidenceSource), so a canonical run genuinely
// participates in the OS-wide trust namespace instead of leaving that
// field inert.
//
// It is called AFTER the decision is computed and its result feeds
// nothing inside this run — deliberately. A trust observation that fed
// back into the same run's own weights would make the run's output
// depend on how many times it had been executed, which would make cold
// replay structurally impossible. Trust READ is causal within a run
// (see evaluateTrust); trust WRITE is a consequence of it.
func (p *Pipeline) recordTrustObservations(tr TrustEvaluation) {
	if p.Trust == nil {
		return
	}
	for _, s := range tr.Sources {
		if s.Subject == "" {
			continue
		}
		// A source admitted at full weight is a success observation for
		// its provider; a discounted or excluded one is a failure
		// observation. This is the real Beta-update the Calculus models,
		// not a synthetic score.
		//nolint:errcheck // Observe's only error mode is weight outside
		// (0,1]; 1 is a fixed literal here, so it cannot fail.
		_, _ = p.Trust.Observe(
			trustcalc.NamespacedSubject(trustcalc.NamespaceEvidenceSource, s.Subject),
			s.Posture == PostureNormal, 1)
	}
}
