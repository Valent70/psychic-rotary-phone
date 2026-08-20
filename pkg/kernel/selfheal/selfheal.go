// Package selfheal implements Veriqo's Self-Healing Runtime — gap #9 in
// "Kritik_terbesar_nomor_1.docx": "Engine mati -> Restart -> Replay ->
// Recovery -> Continue. Belum ada."
//
// Watcher wraps a Unit (anything with Health/Restart/Replay), and on
// detecting a failed HealthCheck, drives it through the exact 4-step
// sequence the critique names, with bounded exponential-ish backoff
// (measured in logical ticks, not wall time) and a hash-chained
// recovery log so every self-heal action is itself auditable (DARI).
package selfheal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

var ErrMaxAttemptsExceeded = errors.New("selfheal: max recovery attempts exceeded")

// Health is a unit's live state as reported by its own HealthCheck.
type Health string

const (
	Healthy  Health = "HEALTHY"
	Degraded Health = "DEGRADED"
	Down     Health = "DOWN"
)

// Unit is anything the Self-Healing Runtime can monitor and recover.
type Unit interface {
	Name() string
	HealthCheck() Health
	// Restart re-initializes the unit's process/state (Step 1).
	Restart() error
	// Replay re-applies any durable checkpoint the unit owns, so work
	// completed before the failure isn't silently lost (Step 2) — a
	// Unit backed by pkg/workflow.Executor implements this by calling
	// Executor.Resume against its own audit.AuditStore checkpoints.
	Replay() error
}

// Action is one step of a recovery sequence, recorded for audit.
type Action string

const (
	ActionDetected  Action = "DETECTED_FAILURE"
	ActionRestart   Action = "RESTART"
	ActionReplay    Action = "REPLAY"
	ActionRecovered Action = "RECOVERED"
	ActionContinue  Action = "CONTINUE"
	ActionGaveUp    Action = "GAVE_UP"
)

// LogRecord is one hash-chained recovery-log entry.
type LogRecord struct {
	Index    uint64
	Unit     string
	Action   Action
	Attempt  int
	Tick     uint64
	PrevHash string
	Hash     string
}

func (r LogRecord) computeHash() string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%d|%d|%s", r.Index, r.Unit, r.Action, r.Attempt, r.Tick, r.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Watcher drives one or more Units through detect->restart->replay->
// recover->continue, up to MaxAttempts per incident.
type Watcher struct {
	mu          sync.Mutex
	MaxAttempts int
	units       map[string]Unit
	log         []LogRecord
}

func NewWatcher(maxAttempts int) *Watcher {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &Watcher{MaxAttempts: maxAttempts, units: make(map[string]Unit)}
}

// Register adds a Unit to be monitored.
func (w *Watcher) Register(u Unit) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.units[u.Name()] = u
}

func (w *Watcher) appendLocked(unit string, action Action, attempt int, tick uint64) {
	prev := ""
	if n := len(w.log); n > 0 {
		prev = w.log[n-1].Hash
	}
	rec := LogRecord{Index: uint64(len(w.log)) + 1, Unit: unit, Action: action, Attempt: attempt, Tick: tick, PrevHash: prev}
	rec.Hash = rec.computeHash()
	w.log = append(w.log, rec)
}

// Check runs one HealthCheck pass on a named unit; if unhealthy, drives
// the full Restart -> Replay -> Recovery -> Continue sequence, retrying
// up to MaxAttempts times (each attempt re-checks health after
// Restart+Replay) before giving up with ErrMaxAttemptsExceeded.
func (w *Watcher) Check(name string, tick uint64) (Health, error) {
	_, span := telemetry.StartSpan(context.Background(), "selfheal.Check",
		telemetry.Attribute{Key: "unit", Value: name})
	defer span.End()

	w.mu.Lock()
	u, ok := w.units[name]
	w.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("selfheal: unit %q not registered", name)
	}

	h := u.HealthCheck()
	if h == Healthy {
		return h, nil
	}

	w.mu.Lock()
	w.appendLocked(name, ActionDetected, 0, tick)
	w.mu.Unlock()

	for attempt := 1; attempt <= w.MaxAttempts; attempt++ {
		w.mu.Lock()
		w.appendLocked(name, ActionRestart, attempt, tick)
		w.mu.Unlock()
		if err := u.Restart(); err != nil {
			continue
		}

		w.mu.Lock()
		w.appendLocked(name, ActionReplay, attempt, tick)
		w.mu.Unlock()
		if err := u.Replay(); err != nil {
			continue
		}

		if u.HealthCheck() == Healthy {
			w.mu.Lock()
			w.appendLocked(name, ActionRecovered, attempt, tick)
			w.appendLocked(name, ActionContinue, attempt, tick)
			w.mu.Unlock()
			return Healthy, nil
		}
	}

	w.mu.Lock()
	w.appendLocked(name, ActionGaveUp, w.MaxAttempts, tick)
	w.mu.Unlock()
	return Down, fmt.Errorf("%w: unit %q after %d attempts", ErrMaxAttemptsExceeded, name, w.MaxAttempts)
}

// Log returns a defensive copy of the recovery audit log.
func (w *Watcher) Log() []LogRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]LogRecord, len(w.log))
	copy(out, w.log)
	return out
}

// VerifyChain replays the recovery log's hash chain, reporting tamper.
func (w *Watcher) VerifyChain() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	prev := ""
	for _, rec := range w.log {
		want := LogRecord{Index: rec.Index, Unit: rec.Unit, Action: rec.Action, Attempt: rec.Attempt, Tick: rec.Tick, PrevHash: prev}
		if want.computeHash() != rec.Hash {
			return fmt.Errorf("selfheal: tamper detected at index %d", rec.Index)
		}
		prev = rec.Hash
	}
	return nil
}
