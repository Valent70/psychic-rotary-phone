package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_LoadBeforePersist_ReturnsErrNotFound(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load before Persist: got %v, want ErrNotFound", err)
	}
}

func TestFileStore_PersistThenLoad_RoundTrips(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "nested", "dir", "state.json"))
	want := []byte(`{"hello":"world"}`)
	if err := s.Persist(want); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("round trip mismatch: got %s, want %s", got, want)
	}
}

func TestFileStore_SecondPersist_Overwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewFileStore(path)
	if err := s.Persist([]byte("v1")); err != nil {
		t.Fatalf("Persist v1: %v", err)
	}
	if err := s.Persist([]byte("v2")); err != nil {
		t.Fatalf("Persist v2: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("got %s, want v2", got)
	}
}

// TestFileStore_CrashBeforeRename_LeavesPriorStateIntact simulates a
// crash mid-Persist by manually replicating Persist's write-temp step
// WITHOUT the final rename, then confirms Load still returns the
// state from before the "crashed" write — the concrete atomicity
// guarantee this package exists for.
func TestFileStore_CrashBeforeRename_LeavesPriorStateIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewFileStore(path)
	if err := s.Persist([]byte("committed")); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Simulate a crash after the temp file is written but before
	// rename(2) — write a temp file in the same directory and leave
	// it there, without renaming over path.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".storage-tmp-*")
	if err != nil {
		t.Fatalf("simulate crash: create temp: %v", err)
	}
	if _, err := tmp.Write([]byte("torn-write-should-never-be-visible")); err != nil {
		t.Fatalf("simulate crash: write temp: %v", err)
	}
	tmp.Close()

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load after simulated crash: %v", err)
	}
	if string(got) != "committed" {
		t.Fatalf("crash-atomicity violated: got %q, want %q", got, "committed")
	}
}

func TestFileStore_Path(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s := NewFileStore(p)
	if s.Path() != p {
		t.Fatalf("Path() = %q, want %q", s.Path(), p)
	}
}
