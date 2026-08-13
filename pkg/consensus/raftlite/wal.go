package raftlite

import (
	"bytes"
	"encoding/gob"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
)

// StateSnapshot is the full durable state a Storage implementation must
// persist atomically: everything raftlite needs to resume correctly
// after a process restart.
type StateSnapshot struct {
	Term              uint64
	VotedFor          string
	Log               []LogEntry
	LogBase           uint64
	LastSnapshotIndex uint64
	LastSnapshotTerm  uint64
}

// Storage is the durability seam Node uses to survive process restarts.
// Persist must be atomic at the OS failure boundary: after any crash,
// Load must return either the state from BEFORE the failed Persist call
// or the state from a FULLY completed Persist call — never a mix of the
// two (a "torn write"). A nil Storage in Config is valid: raftlite then
// runs exactly as every prior session left it, fully in-memory, with
// zero behavior change — this feature is additive, not a rewrite.
type Storage interface {
	Persist(s StateSnapshot) error
	Load() (StateSnapshot, bool, error)
}

// FileStorage is a real, stdlib-only Storage backed by one file, made
// crash-atomic via write-temp + fsync + rename: POSIX rename(2) is
// atomic, so a crash at any point while writing the temp file leaves the
// real file exactly as it was before this Persist call. Recovery can
// never observe a half-written state.
//
// Honest scope: this is a full-state "atomic replace" WAL, not a
// segmented incremental-append log — every Persist call rewrites the
// entire durable state in one atomic file swap (O(log size) per call,
// not O(1)). That trades write amplification for a much simpler,
// obviously-correct crash-consistency argument, which is the right
// tradeoff at raftlite's target log sizes (bounded by snapshot
// compaction well before this becomes a bottleneck). A production
// deployment with sustained high write volume and large uncompacted
// logs would want to switch to a segmented, incremental-append WAL —
// named explicitly here as follow-up work, not hidden.
type FileStorage struct {
	mu   sync.Mutex
	path string
}

func NewFileStorage(path string) *FileStorage {
	return &FileStorage{path: path}
}

var errCorruptWAL = errors.New("raftlite: WAL file present but checksum/decode failed")

type walRecord struct {
	Payload []byte
	CRC32C  uint32
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func (f *FileStorage) Persist(s StateSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var payloadBuf bytes.Buffer
	if err := gob.NewEncoder(&payloadBuf).Encode(s); err != nil {
		return err
	}
	payload := payloadBuf.Bytes()
	rec := walRecord{Payload: payload, CRC32C: crc32.Checksum(payload, crcTable)}

	var out bytes.Buffer
	if err := gob.NewEncoder(&out).Encode(rec); err != nil {
		return err
	}

	dir := filepath.Dir(f.path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".wal-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out.Bytes()); err != nil {
		_ = tmp.Close() // best-effort cleanup; the write error below is what matters
		_ = os.Remove(tmpPath)
		return err
	}
	// fsync: force the new state to durable storage BEFORE the rename
	// that makes it visible — otherwise a crash right after a
	// not-yet-flushed rename could still lose the write on some
	// filesystems/mount options.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	// Atomic swap. After this line returns, Load() sees either the OLD
	// complete file (crash before this point) or the NEW complete file
	// (crash after) — never a torn mix of the two.
	if err := os.Rename(tmpPath, f.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (f *FileStorage) Load() (StateSnapshot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return StateSnapshot{}, false, nil
		}
		return StateSnapshot{}, false, err
	}
	var rec walRecord
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&rec); err != nil {
		return StateSnapshot{}, false, errCorruptWAL
	}
	if crc32.Checksum(rec.Payload, crcTable) != rec.CRC32C {
		return StateSnapshot{}, false, errCorruptWAL
	}
	var snap StateSnapshot
	if err := gob.NewDecoder(bytes.NewReader(rec.Payload)).Decode(&snap); err != nil {
		return StateSnapshot{}, false, errCorruptWAL
	}
	return snap, true, nil
}
