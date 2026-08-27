// Package fsm implements a formal, deterministic, replay-safe Finite State Machine.
// VEP-002: Kernel Runtime — all state transitions are logged and replayable.
package fsm

import (
	"fmt"
	"sync"
)

// State is a named node in the state graph.
type State string

// Event is a named trigger that causes a transition.
type Event string

// Action is executed during a transition. It receives the context and returns an error.
type Action func(ctx *Context) error

// Guard is a predicate that must return true for a transition to fire.
type Guard func(ctx *Context) bool

// Transition describes a single edge in the state graph.
type Transition struct {
	From   State
	Event  Event
	To     State
	Guard  Guard
	Action Action
}

// Context carries mutable state through a transition.
type Context struct {
	From    State
	Event   Event
	To      State
	Payload any
	meta    map[string]any
}

// Set stores a key-value pair in the transition context.
func (c *Context) Set(key string, val any) {
	if c.meta == nil {
		c.meta = make(map[string]any)
	}
	c.meta[key] = val
}

// Get retrieves a value from the transition context.
func (c *Context) Get(key string) (any, bool) {
	if c.meta == nil {
		return nil, false
	}
	v, ok := c.meta[key]
	return v, ok
}

// LogEntry records a single state transition for replay.
type LogEntry struct {
	From    State
	Event   Event
	To      State
	Payload any
}

// Machine is a thread-safe deterministic FSM.
type Machine struct {
	mu          sync.RWMutex
	current     State
	transitions []Transition
	log         []LogEntry
	onEnter     map[State][]Action
	onExit      map[State][]Action
}

// New creates a new FSM starting in the given initial state.
func New(initial State, transitions []Transition) *Machine {
	return &Machine{
		current:     initial,
		transitions: transitions,
		onEnter:     make(map[State][]Action),
		onExit:      make(map[State][]Action),
	}
}

// OnEnter registers an action to run when entering a state.
func (m *Machine) OnEnter(s State, a Action) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEnter[s] = append(m.onEnter[s], a)
}

// OnExit registers an action to run when leaving a state.
func (m *Machine) OnExit(s State, a Action) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onExit[s] = append(m.onExit[s], a)
}

// Current returns the current state.
func (m *Machine) Current() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Fire attempts to transition on the given event.
// Returns ErrNoTransition if no matching transition exists.
// Returns ErrGuardFailed if a guard blocked the transition.
func (m *Machine) Fire(event Event, payload any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := &Context{From: m.current, Event: event, Payload: payload}

	t, err := m.findTransition(ctx)
	if err != nil {
		return err
	}

	ctx.To = t.To

	// Exit hooks
	for _, a := range m.onExit[m.current] {
		if err := a(ctx); err != nil {
			return fmt.Errorf("fsm: onExit(%s): %w", m.current, err)
		}
	}

	// Transition action
	if t.Action != nil {
		if err := t.Action(ctx); err != nil {
			return fmt.Errorf("fsm: action(%s→%s on %s): %w", t.From, t.To, event, err)
		}
	}

	// Enter hooks
	for _, a := range m.onEnter[t.To] {
		if err := a(ctx); err != nil {
			return fmt.Errorf("fsm: onEnter(%s): %w", t.To, err)
		}
	}

	m.log = append(m.log, LogEntry{From: m.current, Event: event, To: t.To, Payload: payload})
	m.current = t.To
	return nil
}

// Can returns true if the event can fire in the current state.
func (m *Machine) Can(event Event) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ctx := &Context{From: m.current, Event: event}
	_, err := m.findTransition(ctx)
	return err == nil
}

// Log returns a copy of the transition log.
func (m *Machine) Log() []LogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]LogEntry, len(m.log))
	copy(cp, m.log)
	return cp
}

// Replay reconstructs machine state from a log.
// The machine is reset to initial before replay begins.
func (m *Machine) Replay(initial State, log []LogEntry) error {
	m.mu.Lock()
	m.current = initial
	m.log = nil
	m.mu.Unlock()

	for _, e := range log {
		if err := m.Fire(e.Event, e.Payload); err != nil {
			return fmt.Errorf("fsm: replay failed at (%s→%s on %s): %w", e.From, e.To, e.Event, err)
		}
	}
	return nil
}

// findTransition returns the first matching transition (must be called under lock).
func (m *Machine) findTransition(ctx *Context) (Transition, error) {
	for _, t := range m.transitions {
		if t.From != m.current || t.Event != ctx.Event {
			continue
		}
		if t.Guard != nil && !t.Guard(ctx) {
			return Transition{}, &ErrGuardFailed{From: t.From, Event: ctx.Event, To: t.To}
		}
		return t, nil
	}
	return Transition{}, &ErrNoTransition{State: m.current, Event: ctx.Event}
}

// ─── Errors ───────────────────────────────────────────────────────────────────

// ErrNoTransition is returned when no transition matches the current state + event.
type ErrNoTransition struct {
	State State
	Event Event
}

func (e *ErrNoTransition) Error() string {
	return fmt.Sprintf("fsm: no transition from %q on event %q", e.State, e.Event)
}

// ErrGuardFailed is returned when a guard predicate blocks a transition.
type ErrGuardFailed struct {
	From  State
	Event Event
	To    State
}

func (e *ErrGuardFailed) Error() string {
	return fmt.Sprintf("fsm: guard blocked %q→%q on event %q", e.From, e.To, e.Event)
}
