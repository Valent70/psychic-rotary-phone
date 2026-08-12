// Package flowcontrol implements the credit-based backpressure and
// adaptive batching primitives the gap audit named explicitly (§4).
package flowcontrol

import (
	"context"
	"errors"
	"sync"
)

var ErrClosed = errors.New("flowcontrol: window closed")

// Window is a credit-based inflight limiter bounded by both entry count
// and byte size — a single slow/large entry can't starve the count budget
// and vice versa.
type Window struct {
	mu         sync.Mutex
	cond       *sync.Cond
	maxEntries int
	maxBytes   int64
	curEntries int
	curBytes   int64
	closed     bool
}

func NewWindow(maxEntries int, maxBytes int64) *Window {
	w := &Window{maxEntries: maxEntries, maxBytes: maxBytes}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// Acquire blocks until capacity for `entries`/`bytes` is available, ctx is
// done, or the window is closed.
func (w *Window) Acquire(ctx context.Context, entries int, bytes int64) error {
	done := make(chan struct{})
	var ctxErr error
	go func() {
		select {
		case <-ctx.Done():
			// Write ctxErr while holding w.mu so the waiting goroutine's
			// read below (also under w.mu) never races with this write —
			// found by go test -race, fixed by moving the write inside
			// the lock instead of before acquiring it.
			w.mu.Lock()
			ctxErr = ctx.Err()
			w.cond.Broadcast()
			w.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)

	w.mu.Lock()
	defer w.mu.Unlock()
	for {
		if w.closed {
			return ErrClosed
		}
		if ctxErr != nil {
			return ctxErr
		}
		if w.curEntries+entries <= w.maxEntries && w.curBytes+bytes <= w.maxBytes {
			w.curEntries += entries
			w.curBytes += bytes
			return nil
		}
		w.cond.Wait()
	}
}

// TryAcquire is the non-blocking variant.
func (w *Window) TryAcquire(entries int, bytes int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	if w.curEntries+entries <= w.maxEntries && w.curBytes+bytes <= w.maxBytes {
		w.curEntries += entries
		w.curBytes += bytes
		return true
	}
	return false
}

func (w *Window) Release(entries int, bytes int64) {
	w.mu.Lock()
	w.curEntries -= entries
	w.curBytes -= bytes
	if w.curEntries < 0 {
		w.curEntries = 0
	}
	if w.curBytes < 0 {
		w.curBytes = 0
	}
	w.mu.Unlock()
	w.cond.Broadcast()
}

// Close unblocks all waiters (they receive ErrClosed) to avoid goroutine
// leaks on shutdown.
func (w *Window) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Broadcast()
}

func (w *Window) InFlight() (entries int, bytes int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.curEntries, w.curBytes
}

// AdaptiveBatcher grows/shrinks its target batch size based on queue
// pressure sampled at Drain time.
type AdaptiveBatcher[T any] struct {
	mu      sync.Mutex
	queue   []T
	minSize int
	maxSize int
	curSize int
}

func NewAdaptiveBatcher[T any](minSize, maxSize int) *AdaptiveBatcher[T] {
	if minSize < 1 {
		minSize = 1
	}
	if maxSize < minSize {
		maxSize = minSize
	}
	return &AdaptiveBatcher[T]{minSize: minSize, maxSize: maxSize, curSize: minSize}
}

func (b *AdaptiveBatcher[T]) Push(item T) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queue = append(b.queue, item)
}

// Drain removes and returns up to the current adaptive batch size, then
// adjusts curSize based on remaining queue pressure: grows toward maxSize
// if the queue is still deep, shrinks toward minSize if it's shallow.
func (b *AdaptiveBatcher[T]) Drain() []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.curSize
	if n > len(b.queue) {
		n = len(b.queue)
	}
	out := make([]T, n)
	copy(out, b.queue[:n])
	b.queue = b.queue[n:]

	remaining := len(b.queue)
	switch {
	case remaining > b.curSize*2 && b.curSize < b.maxSize:
		b.curSize++
	case remaining < b.curSize/2 && b.curSize > b.minSize:
		b.curSize--
	}
	return out
}

func (b *AdaptiveBatcher[T]) QueueDepth() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue)
}

func (b *AdaptiveBatcher[T]) CurrentSize() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.curSize
}
