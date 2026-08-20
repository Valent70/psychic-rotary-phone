package selfheal

import "testing"

// flakyUnit fails HealthCheck a fixed number of times, then recovers
// once Restart+Replay have both been called at least once.
type flakyUnit struct {
	name         string
	failUntil    int
	checks       int
	restartCalls int
	replayCalls  int
	restartErrs  int
	replayErrs   int
}

func (u *flakyUnit) Name() string { return u.name }

func (u *flakyUnit) HealthCheck() Health {
	u.checks++
	if u.checks <= u.failUntil {
		return Down
	}
	return Healthy
}

func (u *flakyUnit) Restart() error {
	u.restartCalls++
	if u.restartCalls <= u.restartErrs {
		return errFake
	}
	return nil
}

func (u *flakyUnit) Replay() error {
	u.replayCalls++
	if u.replayCalls <= u.replayErrs {
		return errFake
	}
	return nil
}

var errFake = fakeErr("fake failure")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

func TestRecoversWithinMaxAttempts(t *testing.T) {
	u := &flakyUnit{name: "engineA", failUntil: 2} // 2nd HealthCheck onward reports Healthy after restart+replay
	w := NewWatcher(5)
	w.Register(u)
	h, err := w.Check("engineA", 1)
	if err != nil {
		t.Fatalf("expected recovery, got err: %v", err)
	}
	if h != Healthy {
		t.Fatalf("expected Healthy, got %s", h)
	}
	if u.restartCalls == 0 || u.replayCalls == 0 {
		t.Fatal("expected Restart and Replay to have been called")
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	u := &flakyUnit{name: "engineB", failUntil: 100} // never recovers
	w := NewWatcher(3)
	w.Register(u)
	_, err := w.Check("engineB", 1)
	if err == nil {
		t.Fatal("expected ErrMaxAttemptsExceeded")
	}
	log := w.Log()
	gaveUp := false
	for _, rec := range log {
		if rec.Action == ActionGaveUp {
			gaveUp = true
		}
	}
	if !gaveUp {
		t.Fatal("expected a GAVE_UP record in the log")
	}
}

func TestHealthyUnitSkipsRecovery(t *testing.T) {
	u := &flakyUnit{name: "engineC", failUntil: 0}
	w := NewWatcher(3)
	w.Register(u)
	h, err := w.Check("engineC", 1)
	if err != nil || h != Healthy {
		t.Fatalf("expected immediate healthy, got h=%s err=%v", h, err)
	}
	if len(w.Log()) != 0 {
		t.Fatalf("expected no recovery log entries for a healthy unit, got %d", len(w.Log()))
	}
}

func TestRecoveryLogSequenceOrder(t *testing.T) {
	u := &flakyUnit{name: "engineD", failUntil: 2}
	w := NewWatcher(5)
	w.Register(u)
	w.Check("engineD", 7)
	log := w.Log()
	if len(log) == 0 {
		t.Fatal("expected recovery log entries")
	}
	if log[0].Action != ActionDetected {
		t.Fatalf("expected first entry DETECTED_FAILURE, got %s", log[0].Action)
	}
	last := log[len(log)-1]
	if last.Action != ActionContinue {
		t.Fatalf("expected last entry CONTINUE, got %s", last.Action)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	u := &flakyUnit{name: "engineE", failUntil: 1}
	w := NewWatcher(3)
	w.Register(u)
	w.Check("engineE", 1)
	if err := w.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain: %v", err)
	}
	w.mu.Lock()
	w.log[0].Unit = "tampered"
	w.mu.Unlock()
	if err := w.VerifyChain(); err == nil {
		t.Fatal("expected tamper detection")
	}
}
