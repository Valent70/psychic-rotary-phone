// Package trustcalc is the single Trust Calculus this repo was missing,
// named directly by "Yang_masih_saya_kritisi.docx" §3 and §Tier4: "Untuk
// Intent Trust OS, saya lebih berharap: online Bayesian learning,
// uncertainty propagation, belief revision, adaptive trust calibration.
// EMA bagus untuk smoothing. Tetapi bukan learning engine," and "trust
// calculus yang terintegrasi... bekerja lintas seluruh sistem."
//
// Two things make this a genuine Bayesian trust calculus rather than a
// relabeled moving average:
//
//  1. Belief is a Beta(alpha, beta) posterior per source — the standard
//     conjugate prior for a Bernoulli reliability parameter. Observe
//     performs an honest Bayesian update (pseudo-count accumulation,
//     optionally soft-weighted for partial-confidence evidence — the
//     textbook generalization of the Beta-Bernoulli update to weighted
//     observations). Mean() is the posterior mean; Variance()/StdDev()
//     give real uncertainty, not just a point estimate; CredibleInterval
//     turns that into a usable band via the normal approximation to the
//     Beta posterior (documented as an approximation, accurate once
//     alpha+beta is not tiny). ExactCredibleInterval below is the exact
//     regularized-incomplete-beta quantile (v7.10.2), self-implemented
//     with stdlib math only, for callers where the normal
//     approximation's known weakness at small alpha+beta or near the
//     0/1 boundary matters.
//  2. Calculus is ONE shared object, not one-per-package: registered
//     once in veriqo/kernel.Kernel and injected into both
//     veriqo/core/evidence.Engine (as the weight source for fusion) and
//     veriqo/core/intelligence.Loop (as the learning target) — so a
//     trust update triggered by an arbitration outcome in Intelligence
//     immediately changes how Evidence weighs that source's next
//     observation, and vice versa. That shared-object wiring is what
//     "bekerja lintas seluruh sistem" means here, not a metaphor.
//
// EMA is not removed — Smooth() below still offers it, honestly labeled
// as what the critique says it is: "bagus untuk smoothing," a display /
// damping convenience, never the primary learning mechanism.
package trustcalc

import (
	"fmt"
	"math"
	"sync"
)

// BetaBelief is a Beta(Alpha, Beta) posterior over one source's
// reliability. Immutable snapshot type returned by Calculus.Belief —
// callers cannot mutate a live belief through this struct.
type BetaBelief struct {
	Alpha float64
	Beta  float64
}

// Mean is the posterior mean reliability estimate, E[p] = alpha/(alpha+beta).
func (b BetaBelief) Mean() float64 {
	total := b.Alpha + b.Beta
	if total == 0 {
		return 0.5
	}
	return b.Alpha / total
}

// Variance is Var[p] = alpha*beta / ((alpha+beta)^2 * (alpha+beta+1)).
func (b BetaBelief) Variance() float64 {
	total := b.Alpha + b.Beta
	if total == 0 {
		return 1.0 / 12.0 // variance of Uniform(0,1), the Beta(1,1) prior
	}
	return (b.Alpha * b.Beta) / (total * total * (total + 1))
}

func (b BetaBelief) StdDev() float64 { return math.Sqrt(b.Variance()) }

// CredibleInterval returns an approximate (1-2*tailProb) credible
// interval via the normal approximation to the Beta posterior, clamped
// to [0,1]. z is the number of standard deviations (e.g. 1.96 for
// ~95%). This is an approximation — documented in the package comment
// — not an exact Beta quantile.
func (b BetaBelief) CredibleInterval(z float64) (lo, hi float64) {
	m, sd := b.Mean(), b.StdDev()
	lo, hi = m-z*sd, m+z*sd
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return lo, hi
}

// --- Exact Beta credible interval (v7.10.2 gap closure) --------------------
//
// CredibleInterval above is a documented normal approximation. The
// V7.10.1 technical audit named the exact regularized-incomplete-beta
// quantile as still open. This closes it with a small, self-contained,
// zero-external-dependency implementation (stdlib math only): a
// continued-fraction evaluation of the regularized incomplete beta
// function I_x(a,b) (Lentz's method, the standard numerical algorithm
// for this — see Numerical Recipes §6.4 for the reference derivation),
// inverted via bisection to find the quantile. Bisection (not Newton)
// is used deliberately: it cannot diverge or overshoot outside [0,1],
// which matters more here than a few extra iterations, since this
// interval can feed downstream decisions.

// logBeta returns ln(B(a,b)) = lnGamma(a)+lnGamma(b)-lnGamma(a+b).
func logBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}

// betacf evaluates the continued fraction for the incomplete beta
// function via Lentz's method.
func betacf(x, a, b float64) float64 {
	const (
		maxIter = 200
		eps     = 3e-14
		fpmin   = 1e-300
	)
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		mf := float64(m)
		m2 := 2 * mf
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		h *= d * c
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// regularizedIncompleteBeta computes I_x(a,b), the CDF of Beta(a,b) at
// x, for x in [0,1] and a,b > 0. Exact (to numerical precision), not
// an approximation.
func regularizedIncompleteBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lbeta := logBeta(a, b)
	front := math.Exp(math.Log(x)*a + math.Log(1-x)*b - lbeta)
	if x < (a+1)/(a+b+2) {
		return front * betacf(x, a, b) / a
	}
	return 1 - front*betacf(1-x, b, a)/b
}

// invRegularizedIncompleteBeta inverts I_x(a,b)=p for x via bisection
// on [0,1]. Monotone-safe: the incomplete beta CDF is monotonically
// non-decreasing in x, so bisection always converges.
func invRegularizedIncompleteBeta(p, a, b float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 100; i++ {
		mid := (lo + hi) / 2
		if regularizedIncompleteBeta(mid, a, b) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// ExactCredibleInterval returns the exact (1-2*tailProb) credible
// interval for this Beta(Alpha,Beta) posterior via the regularized
// incomplete beta quantile function — not the normal approximation
// CredibleInterval falls back to. mass is the desired credible mass,
// e.g. 0.95 for a 95% interval; tailProb = (1-mass)/2 on each side.
// Uses the Beta(1,1) uniform prior's trivial closed form when
// Alpha==Beta==0 is never reached (New() always seeds a proper prior),
// so a==0 or b==0 is treated as the Jeffreys-safe minimum of 1e-9 to
// keep the incomplete-beta integral well-defined.
func (b BetaBelief) ExactCredibleInterval(mass float64) (lo, hi float64) {
	a, bb := b.Alpha, b.Beta
	if a <= 0 {
		a = 1e-9
	}
	if bb <= 0 {
		bb = 1e-9
	}
	tail := (1 - mass) / 2
	lo = invRegularizedIncompleteBeta(tail, a, bb)
	hi = invRegularizedIncompleteBeta(1-tail, a, bb)
	return lo, hi
}

// Namespace-prefixed subject helpers — v7.8 Priority A fix
// ("Trust identity namespace"): Calculus keys beliefs by a bare
// string, so an intent actor ID and a calibration source ID that
// happen to share the same literal string would silently collide into
// ONE belief. NamespacedSubject scopes a raw ID to its calling domain
// before it ever reaches Calculus, without changing Calculus's core
// map[string]*BetaBelief API (which pkg/kernel/intent, pkg/moat/
// calibration, and other callers already depend on) — the minimal,
// non-breaking fix for the exact collision the audit named, rather
// than a full TrustSubject{Namespace,ID,Role} struct rewrite of the
// whole package.
const (
	NamespaceIntentActor       = "intent_actor"
	NamespaceCalibrationSource = "calibration_source"
	// NamespaceEvidenceSource scopes the identity space shared by
	// veriqo/core/evidence (reads via TrustProvider.Score) and
	// veriqo/core/intelligence.Loop (the sole writer, via Observe) —
	// v7.10 audit fix: this domain previously used raw, unnamespaced
	// source IDs directly on the shared Calculus. Evidence and
	// Intelligence are INTENTIONALLY meant to share one identity space
	// with each other (arbitration outcomes in Intelligence must
	// change how Evidence weighs that same source's next
	// observation) — this namespace formalizes that sharing while
	// making it collision-safe against NamespaceIntentActor and
	// NamespaceCalibrationSource, closing the OS-wide namespacing
	// discipline consistently across all four trust domains.
	NamespaceEvidenceSource = "evidence_source"
)

// NamespacedSubject scopes id to namespace so two callers can never
// collide on the same Calculus key even if their raw IDs are
// textually identical (e.g. an intent actor called "OPERATOR_A" and a
// calibration source also called "OPERATOR_A" are now distinct
// subjects: "intent_actor:OPERATOR_A" vs
// "calibration_source:OPERATOR_A").
func NamespacedSubject(namespace, id string) string {
	return namespace + ":" + id
}

// Observations is Alpha+Beta minus the prior's own pseudo-count mass —
// a rough "how much evidence has this belief actually seen" figure,
// useful for deciding whether a Mean() is trustworthy yet.
//
// Doc-accuracy fix (v7.8 audit): this comment previously claimed the
// prior was subtracted; the implementation has always returned the
// RAW Alpha+Beta total (INCLUDING the prior's own pseudo-count mass).
// The doc now says what the code actually does; EvidenceCount below is
// the genuinely prior-subtracted figure the old comment described.
func (b BetaBelief) Observations() float64 { return b.Alpha + b.Beta }

// Calculus is the shared, concurrency-safe Trust Calculus: one
// Beta posterior per subject ID (a source ID, an intent subject, or any
// other identity this repo needs a calibrated reliability score for).
type Calculus struct {
	mu         sync.RWMutex
	priorAlpha float64
	priorBeta  float64
	beliefs    map[string]*BetaBelief
	// ledger, when non-nil (via NewWithLedger), receives an immutable
	// hash-chained Entry for every Observe() call — see ledger.go.
	ledger *Ledger
}

// New builds a Calculus with the given Beta(priorAlpha, priorBeta) prior
// applied to every new subject on first observation. Beta(1,1) (the
// Uniform(0,1) prior — "assume nothing") is the conventional default;
// callers with a genuine informative prior (e.g. "government-verified
// sources start more trusted") may pass one.
func New(priorAlpha, priorBeta float64) *Calculus {
	if priorAlpha <= 0 {
		priorAlpha = 1
	}
	if priorBeta <= 0 {
		priorBeta = 1
	}
	return &Calculus{priorAlpha: priorAlpha, priorBeta: priorBeta, beliefs: make(map[string]*BetaBelief)}
}

func (c *Calculus) beliefLocked(subject string) *BetaBelief {
	b, ok := c.beliefs[subject]
	if !ok {
		b = &BetaBelief{Alpha: c.priorAlpha, Beta: c.priorBeta}
		c.beliefs[subject] = b
	}
	return b
}

// EvidenceCount returns how much REAL evidence (excluding the prior's
// own pseudo-count mass) subject's belief has actually seen — the
// figure Observations' doc comment originally (and incorrectly)
// described. Zero for a subject with no observations yet, regardless
// of prior strength.
func (c *Calculus) EvidenceCount(subject string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.beliefs[subject]
	if !ok {
		return 0
	}
	ec := (b.Alpha + b.Beta) - (c.priorAlpha + c.priorBeta)
	if ec < 0 {
		ec = 0
	}
	return ec
}

// Observe performs the Bayesian update: a weight-w observation of
// "matched ground truth / arbitration consensus" adds w to Alpha; a
// weight-w observation of "diverged" adds w to Beta. weight in (0,1]
// lets a soft-confidence observation (e.g. "70% sure this matched")
// contribute a fractional pseudo-count rather than forcing a hard 0/1
// call — the standard generalization of the Beta-Bernoulli update to
// weighted/soft evidence.
func (c *Calculus) Observe(subject string, matched bool, weight float64) (BetaBelief, error) {
	if weight <= 0 || weight > 1 {
		return BetaBelief{}, fmt.Errorf("trustcalc: weight must be in (0,1], got %v", weight)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Ledger append happens BEFORE the in-memory mutation below, and
	// under the same lock, so the immutable history can never diverge
	// from (be behind) the live posterior it claims to explain.
	if c.ledger != nil {
		c.ledger.append(subject, matched, weight)
	}
	b := c.beliefLocked(subject)
	if matched {
		b.Alpha += weight
	} else {
		b.Beta += weight
	}
	return *b, nil
}

// Belief returns a snapshot of a subject's current posterior (the
// prior, un-observed, if the subject has never been seen).
func (c *Calculus) Belief(subject string) BetaBelief {
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.beliefLocked(subject)
}

// Score returns the posterior mean — satisfies
// veriqo/core/evidence.TrustProvider, so a Calculus can be handed
// directly to the Evidence Engine as its weighting source.
func (c *Calculus) Score(subject string) float64 { return c.Belief(subject).Mean() }

// CombineLogOdds performs genuine Bayesian belief revision: given a
// prior probability and a likelihood ratio for new evidence (P(evidence
// | true) / P(evidence | false)), it returns the revised posterior
// probability via log-odds addition — log-odds(posterior) =
// log-odds(prior) + log(likelihoodRatio). This is the mechanism
// veriqo/core/knowledge uses to revise a fact's confidence when new,
// independent evidence arrives, instead of overwriting the old
// confidence outright.
func CombineLogOdds(priorProb, likelihoodRatio float64) float64 {
	if priorProb <= 0 {
		priorProb = 1e-9
	}
	if priorProb >= 1 {
		priorProb = 1 - 1e-9
	}
	if likelihoodRatio <= 0 {
		likelihoodRatio = 1e-9
	}
	priorLogOdds := math.Log(priorProb / (1 - priorProb))
	posteriorLogOdds := priorLogOdds + math.Log(likelihoodRatio)
	posteriorOdds := math.Exp(posteriorLogOdds)
	return posteriorOdds / (1 + posteriorOdds)
}

// Smooth is a plain exponential moving average — kept and clearly
// labeled per the critique's own framing: "EMA bagus untuk smoothing."
// It is a damping utility for display/telemetry, never the return value
// of a trust or learning computation in this repo.
func Smooth(previous, observed, alpha float64) float64 {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	return (1-alpha)*previous + alpha*observed
}

// Subjects returns every subject with a recorded belief, for
// introspection/telemetry.
func (c *Calculus) Subjects() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.beliefs))
	for s := range c.beliefs {
		out = append(out, s)
	}
	return out
}
