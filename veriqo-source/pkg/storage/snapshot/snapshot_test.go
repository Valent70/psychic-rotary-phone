package snapshot_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"testing"

	"veriqo/pkg/storage/snapshot"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "snaptest-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func buildSnap(id string, index snapshot.Index, data []byte) *snapshot.Snapshot {
	b := snapshot.NewBuilder(id, index, 1, []string{"node1", "node2", "node3"})
	return b.Build(data)
}

func TestSnapshot_Verify(t *testing.T) {
	s := buildSnap("snap-1", 100, []byte("state-data"))
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshot_VerifyBadChecksum(t *testing.T) {
	s := buildSnap("snap-x", 1, []byte("data"))
	s.Meta.Checksum = [32]byte{0xFF} // corrupt
	if err := s.Verify(); err == nil {
		t.Fatal("expected verification error")
	}
}

func TestStore_SaveAndLoad(t *testing.T) {
	dir := tempDir(t)
	store, err := snapshot.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	snap := buildSnap("snap-100", 100, []byte("fsm-state-at-100"))
	if err := store.Save(snap); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load("snap-100")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Data, snap.Data) {
		t.Fatal("loaded data mismatch")
	}
}

func TestStore_Latest(t *testing.T) {
	dir := tempDir(t)
	store, _ := snapshot.OpenStore(dir)

	store.Save(buildSnap("snap-50", 50, []byte("state-50")))
	store.Save(buildSnap("snap-100", 100, []byte("state-100")))
	store.Save(buildSnap("snap-200", 200, []byte("state-200")))

	latest := store.Latest()
	if latest == nil || latest.Index != 200 {
		t.Fatalf("expected latest index=200, got %v", latest)
	}
}

func TestStore_Catalog(t *testing.T) {
	dir := tempDir(t)
	store, _ := snapshot.OpenStore(dir)

	for i := range 5 {
		store.Save(buildSnap(fmt.Sprintf("snap-%d", i*10), snapshot.Index(i*10), []byte("data")))
	}
	cat := store.Catalog()
	if len(cat) != 5 {
		t.Fatalf("expected 5 catalog entries, got %d", len(cat))
	}
}

func TestStore_Durability(t *testing.T) {
	dir := tempDir(t)

	store1, _ := snapshot.OpenStore(dir)
	store1.Save(buildSnap("snap-persist", 42, []byte("persistent-state")))

	// Reopen
	store2, _ := snapshot.OpenStore(dir)
	cat := store2.Catalog()
	if len(cat) == 0 {
		t.Fatal("catalog empty after reopen")
	}
	loaded, err := store2.Load("snap-persist")
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Data) != "persistent-state" {
		t.Errorf("wrong data after reopen: %q", loaded.Data)
	}
}

func TestSnapshot_Reader(t *testing.T) {
	snap := buildSnap("snap-stream", 10, []byte("stream-me"))
	r := snap.Reader()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, snap.Data) {
		t.Fatal("reader data mismatch")
	}
}

func TestDiff_Apply(t *testing.T) {
	base := buildSnap("base", 1, []byte("hello world this is base data"))
	target := buildSnap("target", 2, []byte("hello world this is target data extended"))

	delta := snapshot.Diff(base, target)
	reconstructed, err := snapshot.Apply(base, delta)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reconstructed.Data, target.Data) {
		t.Fatalf("apply mismatch:\ngot  %q\nwant %q", reconstructed.Data, target.Data)
	}
}

func TestInstallFromStream(t *testing.T) {
	data := []byte("snapshot data from leader")
	checksum := sha256.Sum256(data)
	r := bytes.NewReader(data)

	snap, err := snapshot.InstallFromStream(r, checksum, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap.Data, data) {
		t.Fatal("stream install data mismatch")
	}
}

func TestInstallFromStream_ChecksumMismatch(t *testing.T) {
	data := []byte("good data")
	badChecksum := [32]byte{0xDE, 0xAD}
	r := bytes.NewReader(data)
	_, err := snapshot.InstallFromStream(r, badChecksum, int64(len(data)))
	if err == nil {
		t.Fatal("expected checksum error")
	}
}

func BenchmarkBuilder_Build(b *testing.B) {
	data := make([]byte, 1*1024*1024) // 1 MiB
	b.ResetTimer()
	for i := range b.N {
		bld := snapshot.NewBuilder(fmt.Sprintf("bench-%d", i), snapshot.Index(i), 1, nil)
		_ = bld.Build(data)
	}
}
