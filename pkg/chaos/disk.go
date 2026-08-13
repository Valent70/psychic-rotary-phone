package chaos

import (
	"math/rand"

	"veriqo/pkg/consensus/raftlite"
)

// DiskCorruptingSnapshotter wraps a real raftlite.Snapshotter and
// randomly corrupts bytes returned by Snapshot() before they'd be
// written/transmitted, and/or corrupts bytes handed to Restore() before
// they'd be read back — simulating bit rot, a torn write, or a truncated
// transfer. Because the underlying Snapshotter (e.g. KGAdapter, backed
// by kg.Graph's hash-chain) independently verifies integrity on Restore,
// this proves the fail-closed property end-to-end: corruption must be
// DETECTED and REJECTED, never silently accepted into live state.
type DiskCorruptingSnapshotter struct {
	Inner          raftlite.Snapshotter
	CorruptOnWrite float64 // probability of corrupting bytes as they're "written" (Snapshot output)
	CorruptOnRead  float64 // probability of corrupting bytes as they're "read back" (Restore input)
	Rng            *rand.Rand
}

func NewDiskCorruptingSnapshotter(inner raftlite.Snapshotter, seed int64, corruptOnWrite, corruptOnRead float64) *DiskCorruptingSnapshotter {
	return &DiskCorruptingSnapshotter{Inner: inner, CorruptOnWrite: corruptOnWrite, CorruptOnRead: corruptOnRead, Rng: rand.New(rand.NewSource(seed))} //nolint:gosec // G404: deterministic seeded fault injection for reproducible chaos tests
}

func corrupt(rng *rand.Rand, data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	out := append([]byte{}, data...)
	// Flip a handful of random bytes — enough to break any hash check
	// deterministically, mirroring what a torn write or bit-flip does.
	n := 1 + rng.Intn(3)
	for i := 0; i < n; i++ {
		idx := rng.Intn(len(out))
		out[idx] ^= 0xFF
	}
	return out
}

func (s *DiskCorruptingSnapshotter) Snapshot() ([]byte, error) {
	data, err := s.Inner.Snapshot()
	if err != nil {
		return nil, err
	}
	if s.CorruptOnWrite > 0 && s.Rng.Float64() < s.CorruptOnWrite {
		data = corrupt(s.Rng, data)
	}
	return data, nil
}

func (s *DiskCorruptingSnapshotter) Restore(data []byte) error {
	if s.CorruptOnRead > 0 && s.Rng.Float64() < s.CorruptOnRead {
		data = corrupt(s.Rng, data)
	}
	return s.Inner.Restore(data)
}
