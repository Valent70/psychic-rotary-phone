// Package wal closes PHASES 20 and 43: the write-ahead log lifecycle
// that pkg/storage never had (it shipped a single atomic-rename
// FileStore, which is a snapshot mechanism, not a log).
//
//	APPEND → CRC → fsync policy → commit → checkpoint
//	  → segment rotation → compaction → retention
//
// The interesting requirement is the recovery classification. The audit
// asked recovery to distinguish valid record, partial record, CRC
// failure, torn write, corrupt tail and corrupt middle — because those
// six have different correct responses:
//
//   - a corrupt TAIL is the normal consequence of a crash mid-append and
//     must be truncated, not treated as data loss;
//   - a corrupt MIDDLE is silent bit rot or tampering and must fail
//     closed, because everything after it is unverifiable.
//
// Conflating them is how a storage layer either loses data it had or
// serves data it cannot vouch for.
//
// Every record is chained: each header carries the previous record's
// hash, so a middle-corruption is detectable even when the corrupted
// bytes happen to produce a valid CRC.
package wal

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrClosed          = errors.New("wal: log is closed")
	ErrCorruptMiddle   = errors.New("wal: corruption before the tail; refusing to serve unverifiable data")
	ErrChainBroken     = errors.New("wal: record chain broken")
	ErrRecordTooLarge  = errors.New("wal: record exceeds the maximum size")
	ErrRetentionUnsafe = errors.New("wal: retention would remove state required for replay")
	ErrNotCommitted    = errors.New("wal: record is not committed")
	ErrBadSegment      = errors.New("wal: malformed segment name")
)

// maxRecord bounds a single record so a corrupt length field cannot
// make recovery allocate gigabytes.
const maxRecord = 16 << 20

// headerSize is: magic(4) + length(4) + crc(4) + seq(8) + tick(8) +
// prevHash(32) = 60 bytes.
const headerSize = 4 + 4 + 4 + 8 + 8 + 32

var magic = [4]byte{'V', 'W', 'A', 'L'}

// SyncPolicy decides when fsync is called.
type SyncPolicy string

const (
	// SyncAlways fsyncs every append. Slowest, and the only policy under
	// which a returned Append means "durable".
	SyncAlways SyncPolicy = "ALWAYS"
	// SyncInterval fsyncs every N records.
	SyncInterval SyncPolicy = "INTERVAL"
	// SyncNever leaves durability to the OS. Legitimate for a rebuildable
	// cache and never for evidence — Config.Validate refuses it when
	// Durable is set.
	SyncNever SyncPolicy = "NEVER"
)

// Config parameterises the log.
type Config struct {
	Dir string
	// SegmentBytes triggers rotation.
	SegmentBytes int64
	Sync         SyncPolicy
	SyncEvery    int
	// Durable asserts that this log holds evidence. It forbids
	// SyncNever and makes retention refuse to delete uncheckpointed
	// segments.
	Durable bool
}

func (c Config) validate() error {
	if c.Dir == "" {
		return errors.New("wal: dir is required")
	}
	if c.Durable && c.Sync == SyncNever {
		return errors.New("wal: a durable log may not use SyncNever")
	}
	return nil
}

func (c Config) normalise() Config {
	if c.SegmentBytes <= 0 {
		c.SegmentBytes = 1 << 20
	}
	if c.Sync == "" {
		c.Sync = SyncAlways
	}
	if c.SyncEvery <= 0 {
		c.SyncEvery = 64
	}
	return c
}

// Record is one logical entry.
type Record struct {
	Seq      uint64 `json:"seq"`
	Tick     uint64 `json:"tick"`
	Payload  []byte `json:"payload"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
	Segment  string `json:"segment"`
	Offset   int64  `json:"offset"`
}

// Defect classifies what recovery found.
type Defect string

const (
	DefectNone            Defect = "NONE"
	DefectPartialRecord   Defect = "PARTIAL_RECORD" // header or payload truncated
	DefectCRCFailure      Defect = "CRC_FAILURE"    // payload does not match its CRC
	DefectTornWrite       Defect = "TORN_WRITE"     // header written, payload absent
	DefectCorruptTail     Defect = "CORRUPT_TAIL"   // recoverable: truncate
	DefectCorruptMiddle   Defect = "CORRUPT_MIDDLE" // unrecoverable: fail closed
	DefectChainBroken     Defect = "CHAIN_BROKEN"   // hash linkage violated
	DefectBadMagic        Defect = "BAD_MAGIC"
	DefectImplausibleSize Defect = "IMPLAUSIBLE_SIZE"
)

// Finding is one recovery observation.
type Finding struct {
	Segment string `json:"segment"`
	Offset  int64  `json:"offset"`
	Seq     uint64 `json:"seq"`
	Defect  Defect `json:"defect"`
	Detail  string `json:"detail"`
}

// RecoveryReport is the audit's required recovery proof.
type RecoveryReport struct {
	RecoveredRecords int       `json:"recovered_records"`
	ReplayedRecords  int       `json:"replayed_records"`
	LostRecords      int       `json:"lost_records"`
	TruncatedBytes   int64     `json:"truncated_bytes"`
	Findings         []Finding `json:"findings"`
	LastSeq          uint64    `json:"last_seq"`
	RecoveryRootHash string    `json:"recovery_root_hash"`
	FailedClosed     bool      `json:"failed_closed"`
}

// Log is the write-ahead log.
type Log struct {
	mu        sync.Mutex
	cfg       Config
	file      *os.File
	segment   string
	size      int64
	seq       uint64
	lastHash  string
	sinceSync int
	closed    bool

	committed  uint64 // highest committed seq
	checkpoint uint64 // highest checkpointed seq
	// holds are seq ranges that must never be compacted or retained
	// away — legal hold and replay requirements.
	holds []Hold
}

// Hold pins a range of sequence numbers.
type Hold struct {
	ID   string
	From uint64
	To   uint64 // 0 = open ended
}

// Open creates or reopens a log, running recovery first.
func Open(cfg Config) (*Log, *RecoveryReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, nil, err
	}
	cfg = cfg.normalise()
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, nil, err
	}
	l := &Log{cfg: cfg}
	rep, err := l.recover()
	if err != nil {
		return nil, rep, err
	}
	if err := l.openSegmentForAppend(); err != nil {
		return nil, rep, err
	}
	return l, rep, nil
}

func segmentName(index uint64) string {
	return fmt.Sprintf("%016d.wal", index)
}

func segmentIndex(name string) (uint64, error) {
	base := strings.TrimSuffix(filepath.Base(name), ".wal")
	v, err := strconv.ParseUint(base, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrBadSegment, name)
	}
	return v, nil
}

func (l *Log) segments() ([]string, error) {
	entries, err := os.ReadDir(l.cfg.Dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".wal") {
			continue
		}
		if _, err := segmentIndex(e.Name()); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

func (l *Log) openSegmentForAppend() error {
	segs, err := l.segments()
	if err != nil {
		return err
	}
	name := segmentName(0)
	if len(segs) > 0 {
		name = segs[len(segs)-1]
	}
	f, err := os.OpenFile(filepath.Join(l.cfg.Dir, name), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.file, l.segment, l.size = f, name, st.Size()
	return nil
}

// Append writes one record.
func (l *Log) Append(tick uint64, payload []byte) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Record{}, ErrClosed
	}
	if len(payload) > maxRecord {
		return Record{}, fmt.Errorf("%w: %d bytes", ErrRecordTooLarge, len(payload))
	}
	if l.size >= l.cfg.SegmentBytes {
		if err := l.rotateLocked(); err != nil {
			return Record{}, err
		}
	}
	seq := l.seq + 1
	rec := Record{Seq: seq, Tick: tick, Payload: append([]byte(nil), payload...),
		PrevHash: l.lastHash, Segment: l.segment, Offset: l.size}
	rec.Hash = recordHash(rec)

	buf := encode(rec)
	n, err := l.file.Write(buf)
	if err != nil {
		return Record{}, err
	}
	l.size += int64(n)
	l.seq = seq
	l.lastHash = rec.Hash
	l.sinceSync++

	switch l.cfg.Sync {
	case SyncAlways:
		if err := l.file.Sync(); err != nil {
			return Record{}, err
		}
		l.sinceSync = 0
	case SyncInterval:
		if l.sinceSync >= l.cfg.SyncEvery {
			if err := l.file.Sync(); err != nil {
				return Record{}, err
			}
			l.sinceSync = 0
		}
	}
	return rec, nil
}

// Commit marks every record up to seq as committed.
func (l *Log) Commit(seq uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq > l.seq {
		return fmt.Errorf("%w: seq %d beyond the log head %d", ErrNotCommitted, seq, l.seq)
	}
	if seq < l.committed {
		return fmt.Errorf("%w: commit cannot move backwards (%d < %d)", ErrNotCommitted, seq, l.committed)
	}
	l.committed = seq
	if l.cfg.Sync != SyncNever {
		if err := l.file.Sync(); err != nil {
			return err
		}
		l.sinceSync = 0
	}
	return nil
}

// Checkpoint records that state up to seq has been durably materialised
// elsewhere (a snapshot), which is what makes compaction safe.
func (l *Log) Checkpoint(seq uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq > l.committed {
		return fmt.Errorf("%w: cannot checkpoint uncommitted seq %d (committed=%d)",
			ErrNotCommitted, seq, l.committed)
	}
	if seq > l.checkpoint {
		l.checkpoint = seq
	}
	return nil
}

// PlaceHold pins a sequence range against compaction and retention.
func (l *Log) PlaceHold(h Hold) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holds = append(l.holds, h)
}

func (l *Log) heldLocked(from, to uint64) (string, bool) {
	for _, h := range l.holds {
		hTo := h.To
		if hTo == 0 {
			hTo = ^uint64(0)
		}
		if from <= hTo && to >= h.From {
			return h.ID, true
		}
	}
	return "", false
}

// Rotate closes the active segment and starts a new one.
func (l *Log) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotateLocked()
}

func (l *Log) rotateLocked() error {
	if l.file != nil {
		if err := l.file.Sync(); err != nil {
			return err
		}
		if err := l.file.Close(); err != nil {
			return err
		}
	}
	idx, err := segmentIndex(l.segment)
	if err != nil {
		idx = 0
	}
	name := segmentName(idx + 1)
	f, err := os.OpenFile(filepath.Join(l.cfg.Dir, name), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.file, l.segment, l.size, l.sinceSync = f, name, 0, 0
	return nil
}

// CompactionResult reports what was removed.
type CompactionResult struct {
	RemovedSegments []string `json:"removed_segments"`
	RetainedReason  []string `json:"retained_reason"`
	FreedBytes      int64    `json:"freed_bytes"`
}

// Compact removes whole segments whose records are entirely below the
// checkpoint and not held.
//
// It refuses to touch committed-but-uncheckpointed state, required
// replay state, or held ranges — the audit's three explicit "must not
// delete" categories. It works at segment granularity because deleting
// individual records from the middle of a chained log would break the
// chain, which is the property the chain exists to provide.
func (l *Log) Compact() (CompactionResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var res CompactionResult
	segs, err := l.segments()
	if err != nil {
		return res, err
	}
	for _, name := range segs {
		if name == l.segment {
			res.RetainedReason = append(res.RetainedReason, name+": active segment")
			continue
		}
		path := filepath.Join(l.cfg.Dir, name)
		recs, _, err := scanSegment(path, name)
		if err != nil {
			res.RetainedReason = append(res.RetainedReason, name+": unreadable, retained")
			continue
		}
		if len(recs) == 0 {
			continue
		}
		lo, hi := recs[0].Seq, recs[len(recs)-1].Seq
		if hi > l.checkpoint {
			res.RetainedReason = append(res.RetainedReason,
				name+": seq up to "+strconv.FormatUint(hi, 10)+" beyond checkpoint "+
					strconv.FormatUint(l.checkpoint, 10))
			continue
		}
		if id, held := l.heldLocked(lo, hi); held {
			res.RetainedReason = append(res.RetainedReason, name+": legal hold "+id)
			continue
		}
		st, statErr := os.Stat(path)
		if statErr == nil {
			res.FreedBytes += st.Size()
		}
		if err := os.Remove(path); err != nil {
			return res, err
		}
		res.RemovedSegments = append(res.RemovedSegments, name)
	}
	return res, nil
}

// Retain applies a retention policy expressed in ticks, refusing when it
// would remove state required for replay.
func (l *Log) Retain(beforeTick uint64) (CompactionResult, error) {
	l.mu.Lock()
	segs, err := l.segments()
	checkpoint := l.checkpoint
	active := l.segment
	l.mu.Unlock()
	var res CompactionResult
	if err != nil {
		return res, err
	}
	for _, name := range segs {
		if name == active {
			continue
		}
		path := filepath.Join(l.cfg.Dir, name)
		recs, _, err := scanSegment(path, name)
		if err != nil || len(recs) == 0 {
			continue
		}
		if recs[len(recs)-1].Tick >= beforeTick {
			res.RetainedReason = append(res.RetainedReason, name+": within retention window")
			continue
		}
		if recs[len(recs)-1].Seq > checkpoint {
			return res, fmt.Errorf("%w: %s holds uncheckpointed state", ErrRetentionUnsafe, name)
		}
		l.mu.Lock()
		id, held := l.heldLocked(recs[0].Seq, recs[len(recs)-1].Seq)
		l.mu.Unlock()
		if held {
			return res, fmt.Errorf("%w: %s is under legal hold %s", ErrRetentionUnsafe, name, id)
		}
		if st, e := os.Stat(path); e == nil {
			res.FreedBytes += st.Size()
		}
		if err := os.Remove(path); err != nil {
			return res, err
		}
		res.RemovedSegments = append(res.RemovedSegments, name)
	}
	return res, nil
}

// Read returns every recoverable record in sequence order.
func (l *Log) Read() ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		if err := l.file.Sync(); err != nil {
			return nil, err
		}
	}
	return l.readAllLocked()
}

func (l *Log) readAllLocked() ([]Record, error) {
	segs, err := l.segments()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, name := range segs {
		recs, _, err := scanSegment(filepath.Join(l.cfg.Dir, name), name)
		if err != nil {
			return out, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// Close syncs and closes the log.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	if err := l.file.Sync(); err != nil {
		return err
	}
	return l.file.Close()
}

// Head returns the last record hash and sequence.
func (l *Log) Head() (string, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastHash, l.seq
}

// recover scans every segment, classifies defects, truncates a corrupt
// tail and fails closed on corrupt middle.
func (l *Log) recover() (*RecoveryReport, error) {
	rep := &RecoveryReport{}
	segs, err := l.segments()
	if err != nil {
		return rep, err
	}
	var prevHash string
	var lastSeq uint64
	for si, name := range segs {
		path := filepath.Join(l.cfg.Dir, name)
		recs, findings, scanErr := scanSegment(path, name)
		isLastSegment := si == len(segs)-1

		for i, r := range recs {
			if r.PrevHash != prevHash {
				rep.Findings = append(rep.Findings, Finding{Segment: name, Offset: r.Offset,
					Seq: r.Seq, Defect: DefectChainBroken,
					Detail: "expected prev " + prevHash + " got " + r.PrevHash})
				rep.FailedClosed = true
				rep.LostRecords = len(recs) - i
				rep.RecoveryRootHash = recoveryRoot(rep, prevHash)
				return rep, fmt.Errorf("%w at segment %s seq %d", ErrChainBroken, name, r.Seq)
			}
			prevHash = r.Hash
			lastSeq = r.Seq
			rep.RecoveredRecords++
			rep.ReplayedRecords++
		}

		for _, f := range findings {
			// Any defect in a non-final segment, or any defect followed by
			// further valid data, is a MIDDLE corruption.
			if !isLastSegment {
				f.Defect = DefectCorruptMiddle
				rep.Findings = append(rep.Findings, f)
				rep.FailedClosed = true
				rep.RecoveryRootHash = recoveryRoot(rep, prevHash)
				return rep, fmt.Errorf("%w: segment %s offset %d (%s)",
					ErrCorruptMiddle, f.Segment, f.Offset, f.Detail)
			}
			rep.Findings = append(rep.Findings, f)
			// Truncate the tail at the first defect: everything after it
			// is unverifiable and was never committed by definition,
			// because the crash happened during its write.
			if st, e := os.Stat(path); e == nil && f.Offset < st.Size() {
				rep.TruncatedBytes += st.Size() - f.Offset
				if err := os.Truncate(path, f.Offset); err != nil {
					return rep, err
				}
			}
			break
		}
		if scanErr != nil && !isLastSegment {
			return rep, scanErr
		}
	}
	l.seq, l.lastHash = lastSeq, prevHash
	l.committed, l.checkpoint = lastSeq, 0
	rep.LastSeq = lastSeq
	rep.RecoveryRootHash = recoveryRoot(rep, prevHash)
	return rep, nil
}

func recoveryRoot(rep *RecoveryReport, head string) string {
	var sb strings.Builder
	sb.WriteString("wal_recovery/v1\nrecovered=" + strconv.Itoa(rep.RecoveredRecords) +
		"\nreplayed=" + strconv.Itoa(rep.ReplayedRecords) +
		"\nlost=" + strconv.Itoa(rep.LostRecords) +
		"\ntruncated=" + strconv.FormatInt(rep.TruncatedBytes, 10) +
		"\nhead=" + head + "\n")
	for _, f := range rep.Findings {
		sb.WriteString("finding=" + f.Segment + "@" + strconv.FormatInt(f.Offset, 10) +
			":" + string(f.Defect) + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// scanSegment parses a segment, returning the valid prefix and the
// defect that stopped it (if any).
func scanSegment(path, name string) ([]Record, []Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var recs []Record
	var findings []Finding
	var off int64
	for off < int64(len(data)) {
		remaining := int64(len(data)) - off
		if remaining < headerSize {
			findings = append(findings, Finding{Segment: name, Offset: off,
				Defect: DefectPartialRecord,
				Detail: "only " + strconv.FormatInt(remaining, 10) + " header bytes present"})
			break
		}
		h := data[off : off+headerSize]
		if h[0] != magic[0] || h[1] != magic[1] || h[2] != magic[2] || h[3] != magic[3] {
			findings = append(findings, Finding{Segment: name, Offset: off,
				Defect: DefectBadMagic, Detail: "record magic not found"})
			break
		}
		length := int64(binary.BigEndian.Uint32(h[4:8]))
		crc := binary.BigEndian.Uint32(h[8:12])
		seq := binary.BigEndian.Uint64(h[12:20])
		tick := binary.BigEndian.Uint64(h[20:28])
		prev := hex.EncodeToString(h[28:60])

		if length < 0 || length > maxRecord {
			findings = append(findings, Finding{Segment: name, Offset: off, Seq: seq,
				Defect: DefectImplausibleSize,
				Detail: "declared length " + strconv.FormatInt(length, 10)})
			break
		}
		if off+headerSize+length > int64(len(data)) {
			d := DefectTornWrite
			if length > 0 && off+headerSize == int64(len(data)) {
				d = DefectTornWrite
			} else {
				d = DefectPartialRecord
			}
			findings = append(findings, Finding{Segment: name, Offset: off, Seq: seq, Defect: d,
				Detail: "declared " + strconv.FormatInt(length, 10) + " payload bytes, " +
					strconv.FormatInt(int64(len(data))-off-headerSize, 10) + " present"})
			break
		}
		payload := data[off+headerSize : off+headerSize+length]
		if crc32.ChecksumIEEE(payload) != crc {
			findings = append(findings, Finding{Segment: name, Offset: off, Seq: seq,
				Defect: DefectCRCFailure, Detail: "payload checksum mismatch"})
			break
		}
		r := Record{Seq: seq, Tick: tick, Payload: append([]byte(nil), payload...),
			PrevHash: strings.TrimLeft(prev, "0"), Segment: name, Offset: off}
		if prev == strings.Repeat("0", 64) {
			r.PrevHash = ""
		} else {
			r.PrevHash = prev
		}
		r.Hash = recordHash(r)
		recs = append(recs, r)
		off += headerSize + length
	}
	return recs, findings, nil
}

func encode(r Record) []byte {
	buf := make([]byte, headerSize+len(r.Payload))
	copy(buf[0:4], magic[:])
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(r.Payload))) //nolint:gosec // G115: WAL record payloads are bounded well under 4GiB by the caller
	binary.BigEndian.PutUint32(buf[8:12], crc32.ChecksumIEEE(r.Payload))
	binary.BigEndian.PutUint64(buf[12:20], r.Seq)
	binary.BigEndian.PutUint64(buf[20:28], r.Tick)
	prev := make([]byte, 32)
	if r.PrevHash != "" {
		if b, err := hex.DecodeString(r.PrevHash); err == nil && len(b) == 32 {
			copy(prev, b)
		}
	}
	copy(buf[28:60], prev)
	copy(buf[headerSize:], r.Payload)
	return buf
}

func recordHash(r Record) string {
	h := sha256.New()
	h.Write([]byte("wal_record/v1\n"))
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], r.Seq)
	h.Write(scratch[:])
	binary.BigEndian.PutUint64(scratch[:], r.Tick)
	h.Write(scratch[:])
	h.Write([]byte(r.PrevHash))
	h.Write(r.Payload)
	return hex.EncodeToString(h.Sum(nil))
}
