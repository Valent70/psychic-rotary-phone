package resilience

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

// testClock is a controllable clock. The resilience layer's whole
// content is its timing behaviour, so the clock is injected.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// --- Circuit breaker ------------------------------------------------------

func breaker(t *testing.T, c *testClock) *Breaker {
	t.Helper()
	b, err := NewBreaker("registry", BreakerConfig{
		FailureThreshold: 3, OpenFor: 30 * time.Second, SuccessesToClose: 2}, c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTheBreakerOpensAfterConsecutiveFailuresAndRefuses(t *testing.T) {
	c := newClock()
	b := breaker(t, c)

	for i := 0; i < 3; i++ {
		if err := b.Allow(); err != nil {
			t.Fatalf("call %d refused while closed: %v", i, err)
		}
		b.Failure()
	}
	if b.State() != Open {
		t.Fatalf("state = %s after three failures", b.State())
	}
	err := b.Allow()
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("an open breaker admitted a call: %v", err)
	}
	// The refusal names the cause, so an operator is not left guessing.
	if !strings.Contains(err.Error(), "consecutive failures") {
		t.Fatalf("the refusal does not explain itself: %v", err)
	}
}

// TestASuccessResetsTheFailureRun. Three failures spread across
// successes are not a failing dependency.
func TestASuccessResetsTheFailureRun(t *testing.T) {
	c := newClock()
	b := breaker(t, c)
	for i := 0; i < 5; i++ {
		b.Allow()
		b.Failure()
		b.Allow()
		b.Success()
	}
	if b.State() != Closed {
		t.Fatalf("alternating failures and successes opened the breaker (%s)", b.State())
	}
}

// TestHalfOpenAdmitsExactlyOneProbe.
//
// Admitting several would send a burst at a dependency that has just
// been failing, which is what a breaker exists to prevent.
func TestHalfOpenAdmitsExactlyOneProbe(t *testing.T) {
	c := newClock()
	b := breaker(t, c)
	for i := 0; i < 3; i++ {
		b.Allow()
		b.Failure()
	}
	c.advance(31 * time.Second)
	if b.State() != HalfOpen {
		t.Fatalf("state = %s after the open window elapsed", b.State())
	}
	if err := b.Allow(); err != nil {
		t.Fatalf("the first probe was refused: %v", err)
	}
	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("A SECOND PROBE WAS ADMITTED: %v", err)
	}
}

// TestAFailedProbeReopensImmediately.
//
// Counting it towards the threshold would admit further probes at a
// dependency that has just said it is still down.
func TestAFailedProbeReopensImmediately(t *testing.T) {
	c := newClock()
	b := breaker(t, c)
	for i := 0; i < 3; i++ {
		b.Allow()
		b.Failure()
	}
	c.advance(31 * time.Second)
	b.Allow()
	b.Failure()
	if b.State() != Open {
		t.Fatalf("a failed probe left the breaker %s", b.State())
	}
	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatal("a call was admitted immediately after a failed probe")
	}
}

// TestClosingNeedsMoreThanOneSuccess. A single success against a
// flapping dependency closes the circuit into the next failure.
func TestClosingNeedsMoreThanOneSuccess(t *testing.T) {
	c := newClock()
	b := breaker(t, c)
	for i := 0; i < 3; i++ {
		b.Allow()
		b.Failure()
	}
	c.advance(31 * time.Second)
	b.Allow()
	b.Success()
	if b.State() != HalfOpen {
		t.Fatalf("one success closed the breaker (%s)", b.State())
	}
	b.Allow()
	b.Success()
	if b.State() != Closed {
		t.Fatalf("two successes left the breaker %s", b.State())
	}
}

func TestABreakerNeedsAClockAndAValidConfig(t *testing.T) {
	if _, err := NewBreaker("x", BreakerConfig{FailureThreshold: 3,
		OpenFor: time.Second, SuccessesToClose: 1}, nil); !errors.Is(err, ErrNoClock) {
		t.Fatal("a breaker with no clock was built")
	}
	for _, cfg := range []BreakerConfig{
		{OpenFor: time.Second, SuccessesToClose: 1},
		{FailureThreshold: 1, SuccessesToClose: 1},
		{FailureThreshold: 1, OpenFor: time.Second},
	} {
		if _, err := NewBreaker("x", cfg, newClock()); !errors.Is(err, ErrBadConfig) {
			t.Errorf("an invalid config was accepted: %+v", cfg)
		}
	}
}

// --- Rate limit -----------------------------------------------------------

// TestTheBucketRefillsOverTime, and does not admit a double rate
// across a boundary the way a fixed window does.
func TestTheBucketRefillsOverTime(t *testing.T) {
	c := newClock()
	l, err := NewLimiter("provider", 10, 5, c)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := l.Allow(); err != nil {
			t.Fatalf("burst call %d refused: %v", i, err)
		}
	}
	if err := l.Allow(); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("the burst was exceeded: %v", err)
	}
	// Half a second at 10/s refills five tokens... but the burst caps
	// it, so only five are available however long we wait.
	c.advance(time.Second)
	admitted := 0
	for i := 0; i < 20; i++ {
		if l.Allow() == nil {
			admitted++
		}
	}
	if admitted != 5 {
		t.Fatalf("%d calls admitted after a one-second refill; the burst is 5", admitted)
	}
	if l.Refused() == 0 {
		t.Fatal("no refusals were counted")
	}
}

func TestALimiterNeedsAPositiveRate(t *testing.T) {
	if _, err := NewLimiter("x", 0, 5, newClock()); !errors.Is(err, ErrBadConfig) {
		t.Fatal("a zero rate was accepted")
	}
	if _, err := NewLimiter("x", 5, 0, newClock()); !errors.Is(err, ErrBadConfig) {
		t.Fatal("a zero burst was accepted")
	}
}

// --- Bulkhead -------------------------------------------------------------

// TestTheBulkheadBoundsConcurrency, so a slow dependency cannot take
// the rest of the system with it.
func TestTheBulkheadBoundsConcurrency(t *testing.T) {
	b, err := NewBulkhead("ocr", 2)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := b.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	r2, err := b.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(); !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("a third concurrent call was admitted: %v", err)
	}
	r1()
	if _, err := b.Acquire(); err != nil {
		t.Fatalf("a slot was not freed: %v", err)
	}
	r2()
	if b.Refused() != 1 {
		t.Fatalf("refused = %d", b.Refused())
	}
}

// TestReleasingTwiceDoesNotFreeTwoSlots. A double release would let
// occupancy drift below zero and the bulkhead stop bounding anything.
func TestReleasingTwiceDoesNotFreeTwoSlots(t *testing.T) {
	b, _ := NewBulkhead("ocr", 1)
	release, err := b.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	release()
	if b.InFlight() != 0 {
		t.Fatalf("in flight = %d after repeated release", b.InFlight())
	}
	// And the bulkhead still bounds at one.
	if _, err := b.Acquire(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Acquire(); !errors.Is(err, ErrBulkheadFull) {
		t.Fatal("the bulkhead stopped bounding after a double release")
	}
}

func TestAnUnboundedBulkheadIsRefused(t *testing.T) {
	if _, err := NewBulkhead("x", 0); !errors.Is(err, ErrBadConfig) {
		t.Fatal("an unbounded bulkhead was built")
	}
}

// TestTheBulkheadIsConcurrencySafe.
func TestTheBulkheadIsConcurrencySafe(t *testing.T) {
	b, _ := NewBulkhead("ocr", 4)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if release, err := b.Acquire(); err == nil {
				release()
			}
		}()
	}
	wg.Wait()
	if b.InFlight() != 0 {
		t.Fatalf("in flight = %d after every caller released", b.InFlight())
	}
}

// --- Idempotency ----------------------------------------------------------

// TestARetryReceivesTheOriginalResultRatherThanRunningAgain.
func TestARetryReceivesTheOriginalResultRatherThanRunningAgain(t *testing.T) {
	c := newClock()
	i, err := NewIdempotency(c, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i.Begin("op-1"); err != nil {
		t.Fatal(err)
	}
	if err := i.Complete("op-1", "result-hash-a"); err != nil {
		t.Fatal(err)
	}
	prior, err := i.Begin("op-1")
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("a completed operation was begun again: %v", err)
	}
	if prior.ResultHash != "result-hash-a" {
		t.Fatalf("the retry did not receive the original result: %q", prior.ResultHash)
	}
}

// TestAConcurrentDuplicateIsInFlightNotDuplicate.
//
// The second caller must be able to tell "already done" from "being
// done", because the responses differ.
func TestAConcurrentDuplicateIsInFlightNotDuplicate(t *testing.T) {
	c := newClock()
	i, _ := NewIdempotency(c, time.Minute)
	i.Begin("op-1")
	_, err := i.Begin("op-1")
	if !errors.Is(err, ErrInFlight) {
		t.Fatalf("a concurrent duplicate returned %v, want ErrInFlight", err)
	}
	if errors.Is(err, ErrDuplicate) {
		t.Fatal("an in-flight operation was reported as already complete")
	}
}

// TestAnExpiredInFlightMarkerReleasesTheKey. A crashed caller must not
// block a key forever.
func TestAnExpiredInFlightMarkerReleasesTheKey(t *testing.T) {
	c := newClock()
	i, _ := NewIdempotency(c, time.Minute)
	i.Begin("op-1")
	c.advance(2 * time.Minute)
	if _, err := i.Begin("op-1"); err != nil {
		t.Fatalf("an expired in-flight marker still blocked the key: %v", err)
	}
}

// TestAbandonPermitsARetry, for an operation that failed.
func TestAbandonPermitsARetry(t *testing.T) {
	c := newClock()
	i, _ := NewIdempotency(c, time.Minute)
	i.Begin("op-1")
	i.Abandon("op-1")
	if _, err := i.Begin("op-1"); err != nil {
		t.Fatalf("an abandoned key was not released: %v", err)
	}
	if i.Completed() != 0 {
		t.Fatal("an abandoned operation was recorded as completed")
	}
}

func TestAnEmptyIdempotencyKeyIsRefused(t *testing.T) {
	i, _ := NewIdempotency(newClock(), time.Minute)
	if _, err := i.Begin(""); !errors.Is(err, ErrBadConfig) {
		t.Fatal("an empty key was accepted; it would match everything")
	}
}

func TestCompletingWithoutBeginningIsRefused(t *testing.T) {
	i, _ := NewIdempotency(newClock(), time.Minute)
	if err := i.Complete("op-1", "h"); err == nil {
		t.Fatal("an operation was completed without being begun")
	}
}

// --- Backpressure ---------------------------------------------------------

// TestSheddingIsByPriorityAndSafetyIsNeverShed.
//
// A scheduler that shed uniformly would drop an incident-response
// action to make room for a report.
func TestSheddingIsByPriorityAndSafetyIsNeverShed(t *testing.T) {
	bp, err := NewBackpressure(10)
	if err != nil {
		t.Fatal(err)
	}
	// Fill to 50%: background starts shedding.
	for i := 0; i < 5; i++ {
		if err := bp.Admit(Background); err != nil {
			t.Fatalf("background admitted %d before the threshold: %v", i, err)
		}
	}
	if err := bp.Admit(Background); !errors.Is(err, ErrShed) {
		t.Fatalf("background was admitted at 50%%: %v", err)
	}
	// Interactive still passes until 90%.
	for i := 0; i < 4; i++ {
		if err := bp.Admit(Interactive); err != nil {
			t.Fatalf("interactive shed at depth %d: %v", bp.Depth(), err)
		}
	}
	if err := bp.Admit(Interactive); !errors.Is(err, ErrShed) {
		t.Fatalf("interactive was admitted at 90%%: %v", err)
	}
	// Safety passes to capacity.
	if err := bp.Admit(Safety); err != nil {
		t.Fatalf("SAFETY WORK WAS SHED: %v", err)
	}
	// And past capacity even safety cannot be admitted, reported as
	// saturation rather than shedding.
	err = bp.Admit(Safety)
	if err == nil {
		t.Fatal("the queue admitted work past its capacity")
	}
	if errors.Is(err, ErrShed) {
		t.Fatal("saturation was reported as shedding; the remedies differ")
	}
}

// TestSafetyHasNoConfigurableThreshold. Its absence from the map is
// the implementation, so one cannot be set by accident.
func TestSafetyHasNoConfigurableThreshold(t *testing.T) {
	bp, _ := NewBackpressure(10)
	if _, ok := bp.shedAbove[Safety]; ok {
		t.Fatal("SAFETY has a shed threshold; it could be configured to shed")
	}
}

func TestBackpressureReleasesSlots(t *testing.T) {
	bp, _ := NewBackpressure(4)
	bp.Admit(Safety)
	bp.Admit(Safety)
	if bp.Depth() != 2 {
		t.Fatalf("depth = %d", bp.Depth())
	}
	bp.Release()
	bp.Release()
	bp.Release() // extra releases must not go negative
	if bp.Depth() != 0 {
		t.Fatalf("depth = %d after over-release", bp.Depth())
	}
}

// --- The distinction that ties this to the rest of the system -------------

// TestARefusalUnderLoadIsNotAFailure.
//
// Reporting it as an error makes the system look broken while it is
// protecting itself.
func TestARefusalUnderLoadIsNotAFailure(t *testing.T) {
	refusals := []error{ErrOpen, ErrRateLimited, ErrBulkheadFull, ErrShed,
		ErrDuplicate, ErrInFlight}
	for _, e := range refusals {
		if !IsRefusal(e) {
			t.Errorf("%v is not classified as a refusal", e)
		}
		if Classify(e) != contract.Refused {
			t.Errorf("%v classifies as %s", e, Classify(e))
		}
		if Classify(e).IsDefect() {
			t.Errorf("%v classifies as a defect", e)
		}
	}
	if Classify(nil) != contract.Succeeded {
		t.Fatal("a nil error does not classify as success")
	}
	if Classify(errors.New("the parser blew up")) != contract.Failed {
		t.Fatal("a genuine failure does not classify as FAILED")
	}
}

// TestTheReportStatesTheDistinction.
func TestTheReportStatesTheDistinction(t *testing.T) {
	c := newClock()
	b := breaker(t, c)
	l, _ := NewLimiter("provider", 10, 5, c)
	bh, _ := NewBulkhead("ocr", 2)
	bp, _ := NewBackpressure(10)
	r := Report([]*Breaker{b}, []*Limiter{l}, []*Bulkhead{bh}, bp)
	if !strings.Contains(r, "designed outcome, not a failure") {
		t.Fatalf("the report does not state the distinction:\n%s", r)
	}
	for _, want := range []string{"breaker", "limiter", "bulkhead", "queue"} {
		if !strings.Contains(r, want) {
			t.Errorf("the report omits %s:\n%s", want, r)
		}
	}
}
