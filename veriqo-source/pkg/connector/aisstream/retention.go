package aisstream

import "sync"

// RawRetention is the pluggable policy for what happens to each raw
// message's bytes after this connector has processed it. There is
// deliberately no code path in this package that discards raw bytes
// unconditionally without going through a RawRetention the caller
// chose — even "discard everything" (NoRetention) is an explicit,
// visible choice a caller opts into, not a silent default.
type RawRetention interface {
	// Retain is called once per raw message received (after the
	// malformed-message check has already counted/reported the
	// message — Retain sees every raw payload, valid or not, so a
	// retained buffer can support forensic replay of what was
	// actually on the wire).
	Retain(seq uint64, raw []byte)
}

// NoRetention explicitly discards every raw payload. Using it is an
// explicit opt-out a caller must choose (New defaults to
// RingBufferRetention instead), never the implicit behavior.
type NoRetention struct{}

// Retain is a no-op.
func (NoRetention) Retain(uint64, []byte) {}

// RetainedMessage is one entry returned by RingBufferRetention.Recent.
type RetainedMessage struct {
	Seq uint64
	Raw []byte
}

type ringEntry struct {
	seq uint64
	raw []byte
}

// RingBufferRetention keeps the N most recently retained raw messages
// in memory, oldest evicted first once the ring is full. This is the
// default policy New uses when Config.RawRetention is nil.
type RingBufferRetention struct {
	mu      sync.Mutex
	max     int
	entries []ringEntry
}

// NewRingBufferRetention returns a RingBufferRetention keeping at most
// max most-recent entries. max <= 0 is treated as 1.
func NewRingBufferRetention(max int) *RingBufferRetention {
	if max <= 0 {
		max = 1
	}
	return &RingBufferRetention{max: max}
}

// Retain stores a defensive copy of raw, evicting the oldest entry if
// the ring is already at capacity.
func (r *RingBufferRetention) Retain(seq uint64, raw []byte) {
	cp := append([]byte(nil), raw...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, ringEntry{seq: seq, raw: cp})
	if len(r.entries) > r.max {
		r.entries = r.entries[len(r.entries)-r.max:]
	}
}

// Recent returns a snapshot of the currently retained messages,
// oldest first.
func (r *RingBufferRetention) Recent() []RetainedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RetainedMessage, len(r.entries))
	for i, e := range r.entries {
		out[i] = RetainedMessage{Seq: e.seq, Raw: append([]byte(nil), e.raw...)}
	}
	return out
}
