package fsm_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"veriqo/pkg/kernel/fsm"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

const (
	stateLocked   fsm.State = "locked"
	stateUnlocked fsm.State = "unlocked"
	stateBroken   fsm.State = "broken"

	eventCoin  fsm.Event = "coin"
	eventPush  fsm.Event = "push"
	eventBreak fsm.Event = "break"
)

func turnstile() *fsm.Machine {
	return fsm.New(stateLocked, []fsm.Transition{
		{From: stateLocked, Event: eventCoin, To: stateUnlocked},
		{From: stateUnlocked, Event: eventPush, To: stateLocked},
		{From: stateLocked, Event: eventBreak, To: stateBroken},
		{From: stateUnlocked, Event: eventBreak, To: stateBroken},
	})
}

// ─── Basic transitions ────────────────────────────────────────────────────────

func TestMachine_BasicTransition(t *testing.T) {
	m := turnstile()
	if err := m.Fire(eventCoin, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Current() != stateUnlocked {
		t.Fatalf("expected unlocked, got %q", m.Current())
	}
	if err := m.Fire(eventPush, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Current() != stateLocked {
		t.Fatalf("expected locked, got %q", m.Current())
	}
}

func TestMachine_NoTransitionError(t *testing.T) {
	m := turnstile()
	err := m.Fire(eventPush, nil) // push while locked → no transition
	var noT *fsm.ErrNoTransition
	if !errors.As(err, &noT) {
		t.Fatalf("expected ErrNoTransition, got %v", err)
	}
}

func TestMachine_GuardBlocks(t *testing.T) {
	blocked := true
	m := fsm.New(stateLocked, []fsm.Transition{
		{
			From:  stateLocked,
			Event: eventCoin,
			To:    stateUnlocked,
			Guard: func(_ *fsm.Context) bool { return !blocked },
		},
	})
	err := m.Fire(eventCoin, nil)
	var gf *fsm.ErrGuardFailed
	if !errors.As(err, &gf) {
		t.Fatalf("expected ErrGuardFailed, got %v", err)
	}
}

func TestMachine_ActionExecuted(t *testing.T) {
	var called bool
	m := fsm.New(stateLocked, []fsm.Transition{
		{
			From:   stateLocked,
			Event:  eventCoin,
			To:     stateUnlocked,
			Action: func(_ *fsm.Context) error { called = true; return nil },
		},
	})
	if err := m.Fire(eventCoin, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("action not called")
	}
}

func TestMachine_ActionError_DoesNotTransition(t *testing.T) {
	want := errors.New("boom")
	m := fsm.New(stateLocked, []fsm.Transition{
		{
			From:   stateLocked,
			Event:  eventCoin,
			To:     stateUnlocked,
			Action: func(_ *fsm.Context) error { return want },
		},
	})
	if err := m.Fire(eventCoin, nil); err == nil {
		t.Fatal("expected error from action")
	}
	if m.Current() != stateLocked {
		t.Fatalf("state must not change on action error, got %q", m.Current())
	}
}

// ─── Log & Replay ─────────────────────────────────────────────────────────────

func TestMachine_ReplayIsIdentical(t *testing.T) {
	m := turnstile()
	events := []fsm.Event{eventCoin, eventPush, eventCoin, eventBreak}
	for _, e := range events {
		if err := m.Fire(e, nil); err != nil {
			t.Fatalf("unexpected error firing %q: %v", e, err)
		}
	}
	finalState := m.Current()
	log := m.Log()

	m2 := turnstile()
	if err := m2.Replay(stateLocked, log); err != nil {
		t.Fatalf("replay error: %v", err)
	}
	if m2.Current() != finalState {
		t.Fatalf("replay final state %q ≠ original %q", m2.Current(), finalState)
	}
}

func TestMachine_LogIsOrdered(t *testing.T) {
	m := turnstile()
	_ = m.Fire(eventCoin, nil)
	_ = m.Fire(eventPush, nil)
	log := m.Log()
	if len(log) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(log))
	}
	if log[0].Event != eventCoin || log[1].Event != eventPush {
		t.Fatalf("unexpected log order: %v", log)
	}
}

// ─── OnEnter / OnExit hooks ───────────────────────────────────────────────────

func TestMachine_OnEnterOnExit(t *testing.T) {
	m := turnstile()
	var seq []string
	m.OnExit(stateLocked, func(_ *fsm.Context) error { seq = append(seq, "exit:locked"); return nil })
	m.OnEnter(stateUnlocked, func(_ *fsm.Context) error { seq = append(seq, "enter:unlocked"); return nil })
	if err := m.Fire(eventCoin, nil); err != nil {
		t.Fatal(err)
	}
	if len(seq) != 2 || seq[0] != "exit:locked" || seq[1] != "enter:unlocked" {
		t.Fatalf("unexpected hook sequence: %v", seq)
	}
}

// ─── Can ──────────────────────────────────────────────────────────────────────

func TestMachine_Can(t *testing.T) {
	m := turnstile()
	if !m.Can(eventCoin) {
		t.Fatal("should be able to fire coin when locked")
	}
	if m.Can(eventPush) {
		t.Fatal("should not be able to fire push when locked")
	}
}

// ─── Concurrency ─────────────────────────────────────────────────────────────

func TestMachine_ConcurrentReads(t *testing.T) {
	m := turnstile()
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Current()
			_ = m.Can(eventCoin)
		}()
	}
	wg.Wait()
}

// ─── Property tests ───────────────────────────────────────────────────────────

func TestMachine_PropertyDeterministicReplay(t *testing.T) {
	// Property: any sequence of valid events can be replayed identically.
	sequences := [][]fsm.Event{
		{eventCoin},
		{eventCoin, eventPush},
		{eventCoin, eventBreak},
		{eventBreak},
		{eventCoin, eventPush, eventCoin, eventPush},
	}
	for _, seq := range sequences {
		m := turnstile()
		for _, e := range seq {
			_ = m.Fire(e, nil)
		}
		want := m.Current()
		log := m.Log()

		m2 := turnstile()
		if err := m2.Replay(stateLocked, log); err != nil {
			t.Fatalf("seq %v: replay error: %v", seq, err)
		}
		if m2.Current() != want {
			t.Fatalf("seq %v: got %q want %q", seq, m2.Current(), want)
		}
	}
}

// ─── Benchmark ───────────────────────────────────────────────────────────────

func BenchmarkMachine_Fire(b *testing.B) {
	m := turnstile()
	b.ResetTimer()
	for i := range b.N {
		if i%2 == 0 {
			_ = m.Fire(eventCoin, nil)
		} else {
			_ = m.Fire(eventPush, nil)
		}
	}
}

func BenchmarkMachine_Replay(b *testing.B) {
	m := turnstile()
	for range 1000 {
		_ = m.Fire(eventCoin, nil)
		_ = m.Fire(eventPush, nil)
	}
	log := m.Log()
	b.ResetTimer()
	for range b.N {
		m2 := turnstile()
		_ = m2.Replay(stateLocked, log)
	}
}

// Ensure unused import doesn't fail.
var _ = fmt.Sprintf
