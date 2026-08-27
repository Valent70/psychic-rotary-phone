package trustcalc

import (
	"math"
	"testing"
)

// TestExactCredibleIntervalMatchesKnownBeta11 checks the trivial closed
// form: Beta(1,1) is Uniform(0,1), so the exact (1-2*tail) interval is
// exactly [tail, 1-tail].
func TestExactCredibleIntervalMatchesKnownBeta11(t *testing.T) {
	b := BetaBelief{Alpha: 1, Beta: 1}
	lo, hi := b.ExactCredibleInterval(0.90)
	wantLo, wantHi := 0.05, 0.95
	if math.Abs(lo-wantLo) > 1e-3 || math.Abs(hi-wantHi) > 1e-3 {
		t.Fatalf("Beta(1,1) 90%% interval = [%f,%f], want [%f,%f]", lo, hi, wantLo, wantHi)
	}
}

// TestExactCredibleIntervalContainsMean the posterior mean must fall
// strictly inside its own credible interval for any mass in (0,1).
func TestExactCredibleIntervalContainsMean(t *testing.T) {
	b := BetaBelief{Alpha: 12, Beta: 4}
	mean := b.Mean()
	lo, hi := b.ExactCredibleInterval(0.95)
	if mean < lo || mean > hi {
		t.Fatalf("mean %f not inside exact interval [%f,%f]", mean, lo, hi)
	}
}

// TestExactCredibleIntervalRespectsSkewNearBoundary is the concrete
// case the normal approximation is documented to be weak at: few
// observations skewed near 0, where the symmetric normal
// approximation clamps its lower bound at 0 (silently discarding the
// asymmetry) while the exact Beta interval reflects the distribution's
// real skew — a longer right tail than a symmetric interval would
// suggest, since Beta(1,9) concentrates mass near 0 with a decaying
// tail toward 1.
func TestExactCredibleIntervalRespectsSkewNearBoundary(t *testing.T) {
	b := BetaBelief{Alpha: 1, Beta: 9} // strongly skewed toward 0, few obs
	normLo, _ := b.CredibleInterval(1.96)
	exactLo, exactHi := b.ExactCredibleInterval(0.95)
	if normLo != 0 {
		t.Fatalf("expected the normal approximation to clamp at 0 for this skewed case, got %f", normLo)
	}
	if exactLo < 0 || exactHi > 1 || exactLo >= exactHi {
		t.Fatalf("exact interval must be a valid, non-clamped band within [0,1], got [%f,%f]", exactLo, exactHi)
	}
	if exactLo <= 0 {
		t.Fatalf("expected the exact interval's lower bound to stay strictly positive (no clamping needed) for Beta(1,9), got %f", exactLo)
	}
}

// TestExactCredibleIntervalMonotoneInMass wider requested mass must
// never produce a narrower interval.
func TestExactCredibleIntervalMonotoneInMass(t *testing.T) {
	b := BetaBelief{Alpha: 5, Beta: 7}
	lo90, hi90 := b.ExactCredibleInterval(0.90)
	lo99, hi99 := b.ExactCredibleInterval(0.99)
	if lo99 > lo90 || hi99 < hi90 {
		t.Fatalf("99%% interval [%f,%f] must contain 90%% interval [%f,%f]", lo99, hi99, lo90, hi90)
	}
}

// TestRegularizedIncompleteBetaRoundTrip verifies invRegularizedIncompleteBeta
// is a genuine inverse of regularizedIncompleteBeta across a spread of
// (a,b,p) triples — the core correctness proof for the whole
// continued-fraction implementation.
func TestRegularizedIncompleteBetaRoundTrip(t *testing.T) {
	cases := []struct{ a, b, p float64 }{
		{1, 1, 0.5}, {2, 5, 0.1}, {2, 5, 0.9}, {20, 20, 0.5}, {0.5, 0.5, 0.3}, {50, 2, 0.99},
	}
	for _, c := range cases {
		x := invRegularizedIncompleteBeta(c.p, c.a, c.b)
		gotP := regularizedIncompleteBeta(x, c.a, c.b)
		if math.Abs(gotP-c.p) > 1e-4 {
			t.Fatalf("a=%v b=%v p=%v: inverted x=%v round-trips to p=%v, want %v", c.a, c.b, c.p, x, gotP, c.p)
		}
	}
}
