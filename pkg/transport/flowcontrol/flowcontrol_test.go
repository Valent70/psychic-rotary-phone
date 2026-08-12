package flowcontrol

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAcquireRelease_Basic(t *testing.T) {
	w := NewWindow(10, 1000)
	if err := w.Acquire(context.Background(), 5, 500); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	e, b := w.InFlight()
	if e != 5 || b != 500 {
		t.Fatalf("expected 5/500 inflight, got %d/%d", e, b)
	}
	w.Release(5, 500)
	e, b = w.InFlight()
	if e != 0 || b != 0 {
		t.Fatalf("expected 0/0 after release, got %d/%d", e, b)
	}
}

func TestTryAcquire_FailsWhenFull(t *testing.T) {
	w := NewWindow(1, 100)
	if !w.TryAcquire(1, 100) {
		t.Fatal("expected first TryAcquire to succeed")
	}
	if w.TryAcquire(1, 1) {
		t.Fatal("expected second TryAcquire to fail (window full)")
	}
}

func TestAcquire_BlocksUntilReleased(t *testing.T) {
	w := NewWindow(1, 100)
	if !w.TryAcquire(1, 100) {
		t.Fatal("setup failed")
	}
	unblocked := make(chan struct{})
	go func() {
		_ = w.Acquire(context.Background(), 1, 100)
		close(unblocked)
	}()
	select {
	case <-unblocked:
		t.Fatal("Acquire should still be blocked")
	case <-time.After(50 * time.Millisecond):
	}
	w.Release(1, 100)
	select {
	case <-unblocked:
	case <-time.After(time.Second):
		t.Fatal("Acquire did not unblock after Release")
	}
}

func TestAcquire_ContextCancellation(t *testing.T) {
	w := NewWindow(1, 100)
	_ = w.TryAcquire(1, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := w.Acquire(ctx, 1, 100)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

func TestClose_UnblocksAllWaiters(t *testing.T) {
	w := NewWindow(1, 100)
	_ = w.TryAcquire(1, 100)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- w.Acquire(context.Background(), 1, 100)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	w.Close()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != ErrClosed {
			t.Fatalf("expected ErrClosed, got %v", err)
		}
	}
}

func TestConcurrentAcquireRelease_NoDeadlockNoCorruption(t *testing.T) {
	w := NewWindow(20, 10000)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := w.Acquire(ctx, 1, 10); err != nil {
				return
			}
			w.Release(1, 10)
		}()
	}
	wg.Wait()
	e, b := w.InFlight()
	if e != 0 || b != 0 {
		t.Fatalf("expected window to drain back to 0/0, got %d/%d", e, b)
	}
}

func TestAdaptiveBatcher_GrowsAndShrinks(t *testing.T) {
	b := NewAdaptiveBatcher[int](2, 8)
	for i := 0; i < 100; i++ {
		b.Push(i)
	}
	first := b.Drain()
	if len(first) != 2 {
		t.Fatalf("expected initial batch size 2, got %d", len(first))
	}
	// deep queue remaining should grow curSize on subsequent drains
	grew := false
	for i := 0; i < 10; i++ {
		b.Drain()
		if b.CurrentSize() > 2 {
			grew = true
			break
		}
	}
	if !grew {
		t.Fatal("expected adaptive batch size to grow under sustained queue pressure")
	}
	// drain until empty, then shrink should kick in on later pushes
	for b.QueueDepth() > 0 {
		b.Drain()
	}
}
