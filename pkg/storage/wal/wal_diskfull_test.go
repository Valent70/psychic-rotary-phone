package wal

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestActualDiskFullDuringAppendReturnsErrorAndLeavesPriorStateIntact mounts
// a real, tiny tmpfs -- a genuine ENOSPC-capable filesystem, not a mocked
// writer -- and proves Append fails cleanly once the kernel actually runs
// out of space, and that the previously-committed record survives a
// reopen afterwards. Skipped where the sandbox cannot mount a filesystem
// (no CAP_SYS_ADMIN): that is an environment limitation, not something a
// unit test can substitute for.
func TestActualDiskFullDuringAppendReturnsErrorAndLeavesPriorStateIntact(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("tmpfs quota exhaustion requires Linux")
	}
	dir := t.TempDir()
	mountPoint := filepath.Join(dir, "tinyfs")
	if err := os.Mkdir(mountPoint, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("mount", "-t", "tmpfs", "-o", "size=32k", "tmpfs", mountPoint).CombinedOutput(); err != nil { // #nosec G204 -- fixed argv, no external input
		t.Skipf("cannot mount a tmpfs in this sandbox (needs CAP_SYS_ADMIN): %v: %s", err, out)
	}
	defer func() { _ = exec.Command("umount", mountPoint).Run() }() // #nosec G204 -- fixed argv, no external input

	l, _, err := Open(Config{Dir: mountPoint, SegmentBytes: 1 << 30, Sync: SyncAlways, Durable: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const goodPayload = "first-record-fits-in-a-fresh-32k-tmpfs"
	if _, err := l.Append(1, []byte(goodPayload)); err != nil {
		t.Fatalf("first append should fit: %v", err)
	}
	if err := l.Commit(1); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Keep appending until the real tmpfs genuinely runs out of space --
	// this is kernel ENOSPC, not a simulated error.
	var sawErr error
	big := make([]byte, 4096)
	for i := 0; i < 64; i++ {
		if _, err := l.Append(2, big); err != nil {
			sawErr = err
			break
		}
	}
	if sawErr == nil {
		t.Fatal("expected Append to eventually fail once the real tmpfs quota is exhausted")
	}
	_ = l.Close() // best-effort; the filesystem is already full

	l2, rep, err := Open(Config{Dir: mountPoint, SegmentBytes: 1 << 30, Sync: SyncAlways, Durable: true})
	if err != nil {
		t.Fatalf("reopen after a disk-full append must still recover: %v", err)
	}
	defer func() { _ = l2.Close() }()
	if rep.FailedClosed {
		t.Fatalf("a torn write from ENOSPC must be truncated, not treated as unrecoverable corruption: %+v", rep)
	}

	recs, err := l2.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.Seq == 1 && string(r.Payload) == goodPayload {
			found = true
		}
	}
	if !found {
		t.Fatal("the record committed before disk-full must survive recovery intact")
	}
}
