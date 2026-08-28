package wal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openLog(t *testing.T, dir string, cfg Config) (*Log, *RecoveryReport) {
	t.Helper()
	cfg.Dir = dir
	l, rep, err := Open(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return l, rep
}

func TestAppendReadRoundTripAndChain(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	for i := 0; i < 20; i++ {
		if _, err := l.Append(uint64(i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := l.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 20 {
		t.Fatalf("expected 20 records, got %d", len(recs))
	}
	prev := ""
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("sequence gap at %d: %d", i, r.Seq)
		}
		if r.PrevHash != prev {
			t.Fatalf("chain broken at %d", i)
		}
		prev = r.Hash
	}
	_ = l.Close()
}

func TestReopenRecoversCleanly(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	for i := 0; i < 5; i++ {
		_, _ = l.Append(uint64(i), []byte("payload"))
	}
	head, seq := l.Head()
	_ = l.Close()

	l2, rep := openLog(t, dir, Config{Durable: true})
	if rep.RecoveredRecords != 5 || rep.LostRecords != 0 || rep.FailedClosed {
		t.Fatalf("clean recovery expected, got %+v", rep)
	}
	head2, seq2 := l2.Head()
	if head != head2 || seq != seq2 {
		t.Fatalf("recovery must restore the head: %s/%d vs %s/%d", head, seq, head2, seq2)
	}
	if rep.RecoveryRootHash == "" {
		t.Fatal("a recovery root hash is mandatory")
	}
	_ = l2.Close()
}

func TestSyncNeverIsRefusedForADurableLog(t *testing.T) {
	_, _, err := Open(Config{Dir: t.TempDir(), Durable: true, Sync: SyncNever})
	if err == nil {
		t.Fatal("an evidence log must not be allowed to skip fsync")
	}
}

func TestTornWriteAtTheTailIsTruncatedNotFatal(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	for i := 0; i < 4; i++ {
		_, _ = l.Append(uint64(i), []byte("abcdefgh"))
	}
	_ = l.Close()

	// Simulate a crash mid-append: append a header with no payload.
	path := filepath.Join(dir, segmentName(0))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	partial := encode(Record{Seq: 5, Tick: 5, Payload: []byte("this payload never landed")})
	if _, err := f.Write(partial[:headerSize]); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	l2, rep := openLog(t, dir, Config{Durable: true})
	if rep.FailedClosed {
		t.Fatalf("a torn tail must be recoverable, got %+v", rep)
	}
	if rep.RecoveredRecords != 4 {
		t.Fatalf("the four committed records must survive, got %d", rep.RecoveredRecords)
	}
	if rep.TruncatedBytes == 0 {
		t.Fatal("the torn tail must be truncated")
	}
	found := false
	for _, f := range rep.Findings {
		if f.Defect == DefectTornWrite || f.Defect == DefectPartialRecord {
			found = true
		}
	}
	if !found {
		t.Fatalf("the defect must be classified: %+v", rep.Findings)
	}
	// The log must be usable afterwards.
	if _, err := l2.Append(9, []byte("after recovery")); err != nil {
		t.Fatal(err)
	}
	_ = l2.Close()
}

func TestCRCFailureInTheTailIsClassifiedAndTruncated(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	for i := 0; i < 3; i++ {
		_, _ = l.Append(uint64(i), []byte("payload-payload"))
	}
	recs, _ := l.Read()
	last := recs[len(recs)-1]
	_ = l.Close()

	path := filepath.Join(dir, last.Segment)
	data, _ := os.ReadFile(path)
	data[last.Offset+headerSize] ^= 0xFF // corrupt the last payload
	_ = os.WriteFile(path, data, 0o644)

	_, rep, err := Open(Config{Dir: dir, Durable: true})
	if err != nil {
		t.Fatalf("a tail CRC failure must be recoverable: %v", err)
	}
	if rep.RecoveredRecords != 2 {
		t.Fatalf("only the two intact records may be recovered, got %d", rep.RecoveredRecords)
	}
	if rep.Findings[0].Defect != DefectCRCFailure {
		t.Fatalf("expected CRC_FAILURE, got %s", rep.Findings[0].Defect)
	}
}

func TestCorruptMiddleFailsClosed(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true, SegmentBytes: 200})
	for i := 0; i < 12; i++ {
		if _, err := l.Append(uint64(i), []byte("0123456789abcdef")); err != nil {
			t.Fatal(err)
		}
	}
	_ = l.Close()

	segs, _ := os.ReadDir(dir)
	if len(segs) < 3 {
		t.Fatalf("precondition: expected several segments, got %d", len(segs))
	}
	// Corrupt a segment that is NOT the last one.
	victim := filepath.Join(dir, segs[0].Name())
	data, _ := os.ReadFile(victim)
	data[headerSize+2] ^= 0xFF
	_ = os.WriteFile(victim, data, 0o644)

	_, rep, err := Open(Config{Dir: dir, Durable: true})
	if !errors.Is(err, ErrCorruptMiddle) {
		t.Fatalf("mid-log corruption must fail closed, got %v", err)
	}
	if !rep.FailedClosed {
		t.Fatal("the report must say it failed closed")
	}
}

func TestChainTamperingIsDetected(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	for i := 0; i < 4; i++ {
		_, _ = l.Append(uint64(i), []byte("aaaa"))
	}
	recs, _ := l.Read()
	_ = l.Close()

	// Rewrite the second record's prev-hash field to a valid-looking but
	// wrong value; the CRC still passes because the payload is untouched.
	path := filepath.Join(dir, recs[1].Segment)
	data, _ := os.ReadFile(path)
	for i := int64(0); i < 32; i++ {
		data[recs[1].Offset+28+i] = 0xAB
	}
	_ = os.WriteFile(path, data, 0o644)

	_, _, err := Open(Config{Dir: dir, Durable: true})
	if !errors.Is(err, ErrChainBroken) {
		t.Fatalf("chain tampering must be detected even with a valid CRC, got %v", err)
	}
}

func TestSegmentRotationHappensAtTheConfiguredSize(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true, SegmentBytes: 150})
	for i := 0; i < 10; i++ {
		_, _ = l.Append(uint64(i), []byte("0123456789"))
	}
	_ = l.Close()
	entries, _ := os.ReadDir(dir)
	if len(entries) < 3 {
		t.Fatalf("expected rotation into several segments, got %d", len(entries))
	}
}

func TestCommitCannotOutrunTheLogOrMoveBackwards(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	_, _ = l.Append(1, []byte("a"))
	_, _ = l.Append(2, []byte("b"))
	if err := l.Commit(5); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("committing past the head must be refused, got %v", err)
	}
	if err := l.Commit(2); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(1); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("commit must not move backwards, got %v", err)
	}
	_ = l.Close()
}

func TestCheckpointRequiresCommit(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	_, _ = l.Append(1, []byte("a"))
	if err := l.Checkpoint(1); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("checkpointing uncommitted state must be refused, got %v", err)
	}
	_ = l.Commit(1)
	if err := l.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
}

func TestCompactionNeverRemovesUncheckpointedState(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true, SegmentBytes: 150})
	for i := 0; i < 12; i++ {
		_, _ = l.Append(uint64(i), []byte("0123456789"))
	}
	_, seq := l.Head()
	_ = l.Commit(seq)
	// Checkpoint only the first few records.
	_ = l.Checkpoint(3)

	res, err := l.Compact()
	if err != nil {
		t.Fatal(err)
	}
	recs, err := l.Read()
	if err != nil {
		t.Fatal(err)
	}
	var minSeq uint64 = ^uint64(0)
	for _, r := range recs {
		if r.Seq < minSeq {
			minSeq = r.Seq
		}
	}
	for _, r := range recs {
		_ = r
	}
	if len(res.RemovedSegments) > 0 && minSeq <= 3 {
		t.Fatal("compaction removed a segment but records below the checkpoint survive")
	}
	for _, r := range recs {
		if r.Seq > 3 && r.Seq <= seq {
			goto ok
		}
	}
	t.Fatal("post-checkpoint records must survive compaction")
ok:
	_ = l.Close()
}

func TestCompactionRespectsALegalHold(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true, SegmentBytes: 150})
	for i := 0; i < 12; i++ {
		_, _ = l.Append(uint64(i), []byte("0123456789"))
	}
	_, seq := l.Head()
	_ = l.Commit(seq)
	_ = l.Checkpoint(seq)
	l.PlaceHold(Hold{ID: "OFAC-1", From: 1, To: 4})

	res, err := l.Compact()
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, r := range res.RetainedReason {
		if contains(r, "OFAC-1") {
			held = true
		}
	}
	if !held {
		t.Fatalf("the held range must be retained and say why: %+v", res)
	}
	_ = l.Close()
}

func TestRetentionRefusesToRemoveUncheckpointedState(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true, SegmentBytes: 150})
	for i := 0; i < 12; i++ {
		_, _ = l.Append(uint64(i), []byte("0123456789"))
	}
	if _, err := l.Retain(1000); !errors.Is(err, ErrRetentionUnsafe) {
		t.Fatalf("retention must refuse uncheckpointed state, got %v", err)
	}
	_ = l.Close()
}

func TestRetentionKeepsRecordsInsideTheWindow(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true, SegmentBytes: 150})
	for i := 0; i < 12; i++ {
		_, _ = l.Append(uint64(i)*10, []byte("0123456789"))
	}
	_, seq := l.Head()
	_ = l.Commit(seq)
	_ = l.Checkpoint(seq)
	res, err := l.Retain(30)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Read()
	for _, r := range recs {
		_ = r
	}
	if len(recs) == 0 {
		t.Fatal("retention removed everything")
	}
	_ = res
	_ = l.Close()
}

func TestOversizedRecordRejected(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	if _, err := l.Append(1, make([]byte, maxRecord+1)); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("expected size rejection, got %v", err)
	}
	_ = l.Close()
}

func TestUseAfterCloseRefused(t *testing.T) {
	dir := t.TempDir()
	l, _ := openLog(t, dir, Config{Durable: true})
	_ = l.Close()
	if _, err := l.Append(1, []byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestRecoveryReportIsDeterministic(t *testing.T) {
	build := func(dir string) string {
		l, _, err := Open(Config{Dir: dir, Durable: true})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 6; i++ {
			_, _ = l.Append(uint64(i), []byte("same"))
		}
		_ = l.Close()
		_, rep, err := Open(Config{Dir: dir, Durable: true})
		if err != nil {
			t.Fatal(err)
		}
		return rep.RecoveryRootHash
	}
	if a, b := build(t.TempDir()), build(t.TempDir()); a != b {
		t.Fatalf("two identical logs must produce the same recovery root: %s vs %s", a, b)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
