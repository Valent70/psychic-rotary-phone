// Package resilience holds the operational hardening the
// specification's overlay names: circuit breaker, rate limit,
// bulkhead, idempotency and backpressure.
//
// # Why these live together
//
// They are one decision made four ways: what does VERIQO do when a
// dependency is failing, saturated, or being asked the same thing
// twice. Splitting them into four packages would let each be
// configured independently, and the failure mode of that is a circuit
// breaker that opens while a bulkhead keeps admitting work into a
// queue nobody drains.
//
// # The rule that separates this from a retry library
//
// A REFUSAL under load is a designed outcome and is not a failure.
// The same distinction the corpus makes, applied to operations: a
// request shed by backpressure has not failed, it has been declined,
// and reporting it as an error makes the system look broken while it
// is protecting itself.
//
// # Time is injected, never read
//
// Every component takes a clock. A resilience layer that read the wall
// clock could not be tested for its own timing behaviour, and the
// timing behaviour is the whole content.
package resilience

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"veriqo/pkg/contract"
)

var (
	ErrOpen         = errors.New("resilience: the circuit is open")
	ErrRateLimited  = errors.New("resilience: rate limit exceeded")
	ErrBulkheadFull = errors.New("resilience: the bulkhead is full")
	ErrShed         = errors.New("resilience: the request was shed under backpressure")
	ErrDuplicate    = errors.New("resilience: this operation was already performed")
	ErrInFlight     = errors.New("resilience: an identical operation is in flight")
	ErrNoClock      = errors.New("resilience: no clock; a resilience layer that reads the " +
		"wall clock cannot be tested for its own timing")
	ErrBadConfig = errors.New("resilience: invalid configuration")
)

// --- Circuit breaker -----------------------------------------------------

// CircuitState is the breaker's position.
type CircuitState string

const (
	Closed   CircuitState = "CLOSED"    // calls pass
	Open     CircuitState = "OPEN"      // calls are refused
	HalfOpen CircuitState = "HALF_OPEN" // one probe is admitted
)

// BreakerConfig configures a circuit breaker.
type BreakerConfig struct {
	// FailureThreshold is consecutive failures before opening.
	FailureThreshold int
	// OpenFor is how long the circuit stays open before a probe.
	OpenFor time.Duration
	// SuccessesToClose is how many probe successes close it. More than
	// one matters: a single success against a flapping dependency
	// closes the circuit into the next failure.
	SuccessesToClose int
}

func (c BreakerConfig) validate() error {
	if c.FailureThreshold <= 0 {
		return fmt.Errorf("%w: FailureThreshold must be positive", ErrBadConfig)
	}
	if c.OpenFor <= 0 {
		return fmt.Errorf("%w: OpenFor must be positive", ErrBadConfig)
	}
	if c.SuccessesToClose <= 0 {
		return fmt.Errorf("%w: SuccessesToClose must be positive", ErrBadConfig)
	}
	return nil
}

// Breaker is a circuit breaker.
type Breaker struct {
	mu    sync.Mutex
	name  string
	cfg   BreakerConfig
	clock contract.Clock

	state     CircuitState
	failures  int
	successes int
	openedAt  time.Time
	// probing marks that a half-open probe is in flight, so only one
	// is admitted at a time.
	probing bool

	// counters for the audit record.
	refused int
	tripped int
}

// NewBreaker builds one.
func NewBreaker(name string, cfg BreakerConfig, clock contract.Clock) (*Breaker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		return nil, ErrNoClock
	}
	return &Breaker{name: name, cfg: cfg, clock: clock, state: Closed}, nil
}

// State returns the current position, advancing OPEN to HALF_OPEN when
// the window has elapsed.
func (b *Breaker) State() CircuitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	return b.state
}

func (b *Breaker) advance() {
	if b.state == Open && !b.clock.Now().Before(b.openedAt.Add(b.cfg.OpenFor)) {
		b.state = HalfOpen
		b.successes = 0
		b.probing = false
	}
}

// Allow reports whether a call may proceed.
//
// In HALF_OPEN it admits exactly one probe. Admitting several would
// send a burst at a dependency that has just been failing, which is
// the behaviour a breaker exists to prevent.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()

	switch b.state {
	case Closed:
		return nil
	case HalfOpen:
		if b.probing {
			b.refused++
			return fmt.Errorf("%w: %s is half-open and a probe is already in flight",
				ErrOpen, b.name)
		}
		b.probing = true
		return nil
	default:
		b.refused++
		return fmt.Errorf("%w: %s opened at %s after %d consecutive failures",
			ErrOpen, b.name, b.openedAt.Format(time.RFC3339), b.cfg.FailureThreshold)
	}
}

// Success records a successful call.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	if b.state == HalfOpen {
		b.probing = false
		b.successes++
		if b.successes >= b.cfg.SuccessesToClose {
			b.state = Closed
			b.successes = 0
		}
	}
}

// Failure records a failed call.
//
// A REFUSAL is not a failure and must not be passed here: the caller
// distinguishes them, because only the caller knows whether the
// dependency declined by design or broke.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	if b.state == HalfOpen {
		// A failed probe reopens immediately. Counting it towards the
		// threshold would admit further probes at a dependency that
		// has just told us it is still down.
		b.state = Open
		b.openedAt = b.clock.Now()
		b.tripped++
		return
	}
	b.failures++
	if b.failures >= b.cfg.FailureThreshold {
		b.state = Open
		b.openedAt = b.clock.Now()
		b.failures = 0
		b.tripped++
	}
}

// Stats reports the breaker's counters.
func (b *Breaker) Stats() (state CircuitState, refused, tripped int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	return b.state, b.refused, b.tripped
}

// --- Rate limit ----------------------------------------------------------

// Limiter is a token bucket.
//
// It is a bucket rather than a fixed window because a fixed window
// admits twice the rate across a boundary, and a dependency that
// publishes a rate limit means the burst too.
type Limiter struct {
	mu     sync.Mutex
	name   string
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
	clock  contract.Clock

	refused int
}

// NewLimiter builds one.
func NewLimiter(name string, perSecond float64, burst int, clock contract.Clock) (*Limiter, error) {
	if perSecond <= 0 || burst <= 0 {
		return nil, fmt.Errorf("%w: rate and burst must be positive", ErrBadConfig)
	}
	if clock == nil {
		return nil, ErrNoClock
	}
	return &Limiter{name: name, rate: perSecond, burst: float64(burst),
		tokens: float64(burst), last: clock.Now(), clock: clock}, nil
}

// Allow takes a token.
func (l *Limiter) Allow() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens < 1 {
		l.refused++
		return fmt.Errorf("%w: %s permits %.2f/s with a burst of %.0f",
			ErrRateLimited, l.name, l.rate, l.burst)
	}
	l.tokens--
	return nil
}

// Refused reports how many calls were declined.
func (l *Limiter) Refused() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.refused
}

// --- Bulkhead ------------------------------------------------------------

// Bulkhead bounds concurrency for one dependency, so a slow dependency
// cannot consume every worker and take the rest of the system with it.
type Bulkhead struct {
	mu       sync.Mutex
	name     string
	limit    int
	inFlight int
	refused  int
}

// NewBulkhead builds one.
func NewBulkhead(name string, limit int) (*Bulkhead, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: a bulkhead limit must be positive; an unbounded "+
			"bulkhead is not a bulkhead", ErrBadConfig)
	}
	return &Bulkhead{name: name, limit: limit}, nil
}

// Acquire takes a slot and returns the release function.
//
// The release is returned rather than being a separate method so that
// a caller cannot acquire without holding the means to release --
// which is the leak that turns a bulkhead into a deadlock.
func (b *Bulkhead) Acquire() (release func(), err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight >= b.limit {
		b.refused++
		return nil, fmt.Errorf("%w: %s permits %d concurrent call(s)",
			ErrBulkheadFull, b.name, b.limit)
	}
	b.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.inFlight--
			b.mu.Unlock()
		})
	}, nil
}

// InFlight reports the current occupancy.
func (b *Bulkhead) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}

// Refused reports how many were turned away.
func (b *Bulkhead) Refused() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refused
}

// --- Idempotency ---------------------------------------------------------

// Outcome is a recorded operation result.
type Outcome struct {
	Key        string
	ResultHash string
	At         time.Time
}

// Idempotency records what has already been done.
//
// # Why in-flight is a distinct state
//
// Two identical requests arriving concurrently is the ordinary case
// for a retry, and the wrong answer is to perform the work twice. The
// second caller gets ErrInFlight -- not a duplicate result and not a
// second execution -- so it can wait or report a conflict rather than
// creating a second ledger entry.
type Idempotency struct {
	mu       sync.Mutex
	done     map[string]Outcome
	inFlight map[string]time.Time
	clock    contract.Clock
	// ttl bounds how long an in-flight marker is honoured, so a
	// crashed caller does not block a key forever.
	ttl time.Duration
}

// NewIdempotency builds a register.
func NewIdempotency(clock contract.Clock, inFlightTTL time.Duration) (*Idempotency, error) {
	if clock == nil {
		return nil, ErrNoClock
	}
	if inFlightTTL <= 0 {
		return nil, fmt.Errorf("%w: an in-flight marker with no TTL blocks a key forever "+
			"when a caller crashes", ErrBadConfig)
	}
	return &Idempotency{done: map[string]Outcome{}, inFlight: map[string]time.Time{},
		clock: clock, ttl: inFlightTTL}, nil
}

// Begin claims a key.
//
// It returns the previous outcome when the operation has already run,
// so a retry receives the ORIGINAL result rather than performing the
// work again.
func (i *Idempotency) Begin(key string) (prior Outcome, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if key == "" {
		return Outcome{}, fmt.Errorf("%w: an empty idempotency key matches everything",
			ErrBadConfig)
	}
	if o, ok := i.done[key]; ok {
		return o, fmt.Errorf("%w: %s completed at %s", ErrDuplicate, key,
			o.At.Format(time.RFC3339))
	}
	now := i.clock.Now()
	if started, ok := i.inFlight[key]; ok {
		if now.Sub(started) < i.ttl {
			return Outcome{}, fmt.Errorf("%w: %s started at %s", ErrInFlight, key,
				started.Format(time.RFC3339))
		}
		// The marker expired: the previous caller is presumed gone.
	}
	i.inFlight[key] = now
	return Outcome{}, nil
}

// Complete records the result and releases the key.
func (i *Idempotency) Complete(key, resultHash string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.inFlight[key]; !ok {
		if _, done := i.done[key]; done {
			return fmt.Errorf("%w: %s", ErrDuplicate, key)
		}
		return fmt.Errorf("resilience: %s was completed without being begun", key)
	}
	delete(i.inFlight, key)
	i.done[key] = Outcome{Key: key, ResultHash: resultHash, At: i.clock.Now()}
	return nil
}

// Abandon releases a key without recording a result, for an operation
// that failed and may legitimately be retried.
func (i *Idempotency) Abandon(key string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.inFlight, key)
}

// Completed reports how many operations are recorded.
func (i *Idempotency) Completed() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.done)
}

// --- Backpressure --------------------------------------------------------

// Priority classifies work for shedding.
//
// The order is the argument: when a system is saturated it sheds
// BACKGROUND before INTERACTIVE, and it never sheds SAFETY. A
// scheduler that shed uniformly would drop an incident-response action
// to make room for a report.
type Priority int

const (
	Background  Priority = iota // batch, backfill, reindex
	Interactive                 // a person is waiting
	Safety                      // incident response, revocation, audit
)

func (p Priority) String() string {
	switch p {
	case Background:
		return "BACKGROUND"
	case Interactive:
		return "INTERACTIVE"
	case Safety:
		return "SAFETY"
	}
	return fmt.Sprintf("Priority(%d)", int(p))
}

// Backpressure sheds work by priority as a queue fills.
type Backpressure struct {
	mu       sync.Mutex
	capacity int
	depth    int
	// thresholds map a priority to the depth above which it is shed,
	// as a fraction of capacity.
	shedAbove map[Priority]float64
	shed      map[Priority]int
}

// NewBackpressure builds one with the default thresholds.
func NewBackpressure(capacity int) (*Backpressure, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("%w: capacity must be positive", ErrBadConfig)
	}
	return &Backpressure{capacity: capacity,
		shedAbove: map[Priority]float64{
			Background:  0.5,
			Interactive: 0.9,
			// Safety is absent: it is never shed. Its absence is the
			// implementation, so a threshold cannot be configured for
			// it by accident.
		},
		shed: map[Priority]int{}}, nil
}

// Admit decides whether to accept work.
func (b *Backpressure) Admit(p Priority) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if p != Safety {
		threshold, ok := b.shedAbove[p]
		if !ok {
			return fmt.Errorf("%w: unknown priority %s", ErrBadConfig, p)
		}
		if float64(b.depth) >= threshold*float64(b.capacity) {
			b.shed[p]++
			return fmt.Errorf("%w: %s work is shed above %.0f%% of capacity; the queue is "+
				"at %d of %d", ErrShed, p, threshold*100, b.depth, b.capacity)
		}
	}
	if b.depth >= b.capacity {
		// Even SAFETY cannot be admitted past capacity -- but it is
		// reported as saturation rather than as shedding, because the
		// remedy is different.
		b.shed[p]++
		return fmt.Errorf("resilience: the queue is full at %d; even %s work cannot be "+
			"admitted", b.capacity, p)
	}
	b.depth++
	return nil
}

// Release frees a slot.
func (b *Backpressure) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.depth > 0 {
		b.depth--
	}
}

// Depth reports the current queue depth.
func (b *Backpressure) Depth() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.depth
}

// Shed reports how much was shed per priority.
func (b *Backpressure) Shed() map[Priority]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[Priority]int, len(b.shed))
	for k, v := range b.shed {
		out[k] = v
	}
	return out
}

// --- Reporting -----------------------------------------------------------

// IsRefusal reports whether an error is a designed decline rather than
// a failure.
//
// The same distinction the corpus makes, applied to operations: a
// request shed under load has not failed, and reporting it as an error
// makes the system look broken while it is protecting itself.
func IsRefusal(err error) bool {
	return errors.Is(err, ErrOpen) || errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrBulkheadFull) || errors.Is(err, ErrShed) ||
		errors.Is(err, ErrDuplicate) || errors.Is(err, ErrInFlight)
}

// Outcome classifies an error for the audit record.
func Classify(err error) contract.Outcome {
	switch {
	case err == nil:
		return contract.Succeeded
	case IsRefusal(err):
		return contract.Refused
	default:
		return contract.Failed
	}
}

// Report renders a set of components for an operations view.
func Report(breakers []*Breaker, limiters []*Limiter, bulkheads []*Bulkhead,
	bp *Backpressure) string {
	var out []string
	for _, b := range breakers {
		state, refused, tripped := b.Stats()
		out = append(out, fmt.Sprintf("  breaker  %-20s %-10s refused %d, tripped %d",
			b.name, state, refused, tripped))
	}
	for _, l := range limiters {
		out = append(out, fmt.Sprintf("  limiter  %-20s %.2f/s refused %d",
			l.name, l.rate, l.Refused()))
	}
	for _, bh := range bulkheads {
		out = append(out, fmt.Sprintf("  bulkhead %-20s %d/%d in flight, refused %d",
			bh.name, bh.InFlight(), bh.limit, bh.Refused()))
	}
	sort.Strings(out)
	s := "RESILIENCE\n"
	for _, line := range out {
		s += line + "\n"
	}
	if bp != nil {
		s += fmt.Sprintf("  queue    depth %d of %d\n", bp.Depth(), bp.capacity)
		shed := bp.Shed()
		for _, p := range []Priority{Background, Interactive, Safety} {
			if n := shed[p]; n > 0 {
				s += fmt.Sprintf("    shed %-12s %d\n", p, n)
			}
		}
	}
	s += "  A refusal under load is a designed outcome, not a failure.\n"
	return s
}
