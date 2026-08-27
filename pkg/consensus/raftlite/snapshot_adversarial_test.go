package raftlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

// checksummedFSM is a Snapshotter fixture whose Restore performs REAL
// integrity verification -- unlike sumFSM (snapshot_test.go), which
// silently resets to zero on a length mismatch and never otherwise
// validates its input. That silence is fine for sumFSM's own purpose
// (proving the InstallSnapshot code PATH is exercised), but it cannot
// distinguish "corrupted body, checksum mismatch" from "truncated, too
// short to even contain a checksum" -- exactly the two adversarial
// snapshot failure modes the R-029 caveat named as untested. This fixture
// closes that: Snapshot() appends a real SHA-256 checksum over the
// value, and Restore() verifies it, returning a distinct sentinel error
// for each failure mode.
type checksummedFSM struct {
	value uint64
}

const checksummedFSMBodyLen = 8

var (
	errSnapshotTruncated = errors.New("checksummedFSM: snapshot truncated (shorter than body+checksum)")
	errSnapshotCorrupted = errors.New("checksummedFSM: snapshot checksum mismatch")
)

func (f *checksummedFSM) Apply(e LogEntry) error {
	if len(e.Command) == checksummedFSMBodyLen {
		f.value = binary.LittleEndian.Uint64(e.Command)
	}
	return nil
}

func (f *checksummedFSM) Snapshot() ([]byte, error) {
	body := make([]byte, checksummedFSMBodyLen)
	binary.LittleEndian.PutUint64(body, f.value)
	sum := sha256.Sum256(body)
	return append(body, sum[:]...), nil
}

func (f *checksummedFSM) Restore(data []byte) error {
	if len(data) < checksummedFSMBodyLen+sha256.Size {
		return errSnapshotTruncated
	}
	body := data[:checksummedFSMBodyLen]
	wantSum := data[checksummedFSMBodyLen : checksummedFSMBodyLen+sha256.Size]
	gotSum := sha256.Sum256(body)
	if !bytes.Equal(gotSum[:], wantSum) {
		return errSnapshotCorrupted
	}
	f.value = binary.LittleEndian.Uint64(body)
	return nil
}

// newAdversarialSnapshotNode builds a single, standalone node (no live
// cluster, no goroutines) with a checksummedFSM wired in -- these tests
// exercise HandleInstallSnapshot directly, as a real Raft RPC handler
// would be invoked, not through a running Run() loop.
func newAdversarialSnapshotNode(t *testing.T) (*Node, *checksummedFSM) {
	t.Helper()
	fsm := &checksummedFSM{}
	n := NewNode(Config{ID: "A", Peers: []string{"leader"}, Transport: NewMemTransport()})
	n.SetSnapshotter(fsm)
	return n, fsm
}

// installBaseline performs one genuine, valid InstallSnapshot so the
// adversarial tests below have real prior state to prove stays
// UNTOUCHED by a subsequent bad attempt -- "nothing happened" is a much
// weaker (and, for a snapshot fixture starting at the zero value,
// easily confusable with "the bad snapshot silently succeeded")
// assertion than "the specific, distinct prior state is still there".
func installBaseline(t *testing.T, n *Node, fsm *checksummedFSM, index uint64, value uint64) InstallSnapshotArgs {
	t.Helper()
	fsm.value = value
	data, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("test setup: Snapshot(): %v", err)
	}
	args := InstallSnapshotArgs{Term: 1, LeaderID: "leader", LastIncludedIndex: index, LastIncludedTerm: 1, Data: data}
	reply := n.HandleInstallSnapshot(args)
	if reply.Term != 1 {
		t.Fatalf("test setup: baseline install reply term = %d, want 1", reply.Term)
	}
	if n.LastSnapshotIndex() != index {
		t.Fatalf("test setup: baseline install did not take effect: LastSnapshotIndex()=%d, want %d", n.LastSnapshotIndex(), index)
	}
	if fsm.value != value {
		t.Fatalf("test setup: baseline FSM value = %d, want %d", fsm.value, value)
	}
	return args
}

// TestHandleInstallSnapshot_CorruptedBytesFailsClosed is the first
// adversarial scenario the R-029 caveat named: a snapshot whose bytes
// have been corrupted in transit or at rest. HandleInstallSnapshot's
// own doc comment already promises fail-closed behavior here ("if
// Restore() errors, the node's prior state is left untouched"); this
// test is the adversarial proof that promise actually holds, with a
// Snapshotter that can actually detect corruption (see checksummedFSM).
func TestHandleInstallSnapshot_CorruptedBytesFailsClosed(t *testing.T) {
	n, fsm := newAdversarialSnapshotNode(t)
	installBaseline(t, n, fsm, 5, 100)

	fsm.value = 999 // a genuinely valid encoding of a DIFFERENT value...
	corrupted, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	fsm.value = 100      // restore fixture state; only `corrupted` bytes should carry 999
	corrupted[0] ^= 0xFF // ...then flip a body byte so its checksum no longer matches

	args := InstallSnapshotArgs{Term: 1, LeaderID: "leader", LastIncludedIndex: 9, LastIncludedTerm: 1, Data: corrupted}
	reply := n.HandleInstallSnapshot(args)

	if reply.Term != 1 {
		t.Fatalf("reply term = %d, want 1 (corruption is not a term-level rejection)", reply.Term)
	}
	if n.LastSnapshotIndex() != 5 {
		t.Fatalf("LastSnapshotIndex() = %d, want 5 (unchanged) -- a corrupted snapshot must never be installed", n.LastSnapshotIndex())
	}
	if n.LastSnapshotTerm() != 1 {
		t.Fatalf("LastSnapshotTerm() changed despite corrupted install being rejected")
	}
	if fsm.value != 100 {
		t.Fatalf("FSM value = %d, want 100 (unchanged) -- Restore's error must leave prior state untouched, not partially apply", fsm.value)
	}
}

// TestHandleInstallSnapshot_TruncatedFailsClosed is the second
// adversarial scenario: a snapshot payload cut short, as a connection
// drop mid-transfer would produce. Distinct failure mode from
// corruption (checksummedFSM.Restore returns errSnapshotTruncated, not
// errSnapshotCorrupted), but the required outcome is identical:
// fail closed, prior state untouched.
func TestHandleInstallSnapshot_TruncatedFailsClosed(t *testing.T) {
	n, fsm := newAdversarialSnapshotNode(t)
	installBaseline(t, n, fsm, 5, 100)

	fsm.value = 999
	full, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	fsm.value = 100
	truncated := full[:4] // shorter than checksummedFSMBodyLen+sha256.Size

	args := InstallSnapshotArgs{Term: 1, LeaderID: "leader", LastIncludedIndex: 9, LastIncludedTerm: 1, Data: truncated}
	reply := n.HandleInstallSnapshot(args)

	if reply.Term != 1 {
		t.Fatalf("reply term = %d, want 1", reply.Term)
	}
	if n.LastSnapshotIndex() != 5 {
		t.Fatalf("LastSnapshotIndex() = %d, want 5 (unchanged) -- a truncated snapshot must never be installed", n.LastSnapshotIndex())
	}
	if fsm.value != 100 {
		t.Fatalf("FSM value = %d, want 100 (unchanged)", fsm.value)
	}
}

// TestHandleInstallSnapshot_StaleSnapshotInstallAttemptRefused is the
// third adversarial scenario: a stale/outdated InstallSnapshot RPC --
// e.g. a retried or reordered message from a leader whose OWN state has
// since moved on, or a message from a leader that has itself been
// superseded -- carrying a LastIncludedIndex the node has already moved
// past. Unlike the two tests above, the payload itself here is
// perfectly well-formed and would decode successfully; what must be
// refused is installing it AT ALL, purely on staleness. This exercises
// HandleInstallSnapshot's `args.LastIncludedIndex <= n.lastSnapshotIndex`
// guard adversarially: the stale attempt deliberately carries a
// DIFFERENT value than the current baseline, so silently accepting it
// would be immediately visible as data loss/rollback, not just a
// no-op.
func TestHandleInstallSnapshot_StaleSnapshotInstallAttemptRefused(t *testing.T) {
	n, fsm := newAdversarialSnapshotNode(t)
	installBaseline(t, n, fsm, 10, 100)

	fsm.value = 7 // a stale, OLDER, well-formed snapshot -- valid on its own terms
	staleData, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	fsm.value = 100 // restore fixture state to the real baseline before the attempt

	for _, staleIndex := range []uint64{10, 6, 1} {
		args := InstallSnapshotArgs{Term: 1, LeaderID: "leader", LastIncludedIndex: staleIndex, LastIncludedTerm: 1, Data: staleData}
		reply := n.HandleInstallSnapshot(args)
		if reply.Term != 1 {
			t.Fatalf("staleIndex=%d: reply term = %d, want 1", staleIndex, reply.Term)
		}
		if n.LastSnapshotIndex() != 10 {
			t.Fatalf("staleIndex=%d: LastSnapshotIndex() = %d, want 10 (unchanged) -- a stale/outdated InstallSnapshot must be refused, never roll state backward", staleIndex, n.LastSnapshotIndex())
		}
		if fsm.value != 100 {
			t.Fatalf("staleIndex=%d: FSM value = %d, want 100 (unchanged) -- accepting the stale snapshot would have rolled it back to 7", staleIndex, fsm.value)
		}
	}
}

// TestHandleInstallSnapshot_ValidSnapshotStillInstallsCleanly is the
// control: proves the three adversarial tests above are actually
// exercising real rejection logic, not a Snapshotter/handler that
// simply never installs anything.
func TestHandleInstallSnapshot_ValidSnapshotStillInstallsCleanly(t *testing.T) {
	n, fsm := newAdversarialSnapshotNode(t)
	installBaseline(t, n, fsm, 5, 100)

	fsm.value = 200
	data, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	fsm.value = 100

	args := InstallSnapshotArgs{Term: 1, LeaderID: "leader", LastIncludedIndex: 9, LastIncludedTerm: 1, Data: data}
	n.HandleInstallSnapshot(args)

	if n.LastSnapshotIndex() != 9 {
		t.Fatalf("a genuinely valid, non-stale snapshot must still install: LastSnapshotIndex() = %d, want 9", n.LastSnapshotIndex())
	}
	if fsm.value != 200 {
		t.Fatalf("a genuinely valid, non-stale snapshot must still install: FSM value = %d, want 200", fsm.value)
	}
}
