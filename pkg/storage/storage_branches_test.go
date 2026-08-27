package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// These tests close V7.11.1 P0-6 for pkg/storage. The existing suite
// covered the happy path and one crash case; the branches added here are
// the DURABILITY ones — where a wrong answer means silently serving
// state the store cannot vouch for, or losing state it accepted.

func store(t *testing.T) *FileStore {
	t.Helper()
	return NewFileStore(filepath.Join(t.TempDir(), "state.bin"))
}

func TestLoadOnAMissingDirectoryIsNotFoundNotAPanic(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "does", "not", "exist", "state.bin"))
	if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing path must be ErrNotFound, got %v", err)
	}
}

func TestPersistCreatesTheParentDirectory(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(filepath.Join(dir, "nested", "deeper", "state.bin"))
	if err := s.Persist([]byte("payload")); err != nil {
		t.Fatalf("persist must create the directory it needs: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("round trip failed: %q", got)
	}
}

func TestEmptyPayloadRoundTripsRatherThanBeingTreatedAsAbsent(t *testing.T) {
	s := store(t)
	if err := s.Persist([]byte{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("a persisted empty value is present, not missing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty payload, got %d bytes", len(got))
	}
}

func TestLargePayloadIsNotTruncated(t *testing.T) {
	s := store(t)
	payload := bytes.Repeat([]byte("veriqo"), 1<<18) // ~1.5 MiB
	if err := s.Persist(payload); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload corrupted: wrote %d bytes, read %d", len(payload), len(got))
	}
}

func TestPersistIsAtomicUnderRepeatedOverwrite(t *testing.T) {
	s := store(t)
	// Each write is a full replacement; a reader must never observe a
	// blend of two generations. Interleaving distinct fixed-length
	// payloads makes a partial write detectable by content.
	a := bytes.Repeat([]byte("A"), 4096)
	b := bytes.Repeat([]byte("B"), 4096)
	for i := 0; i < 50; i++ {
		want := a
		if i%2 == 1 {
			want = b
		}
		if err := s.Persist(want); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("generation %d: store returned a blended or stale payload", i)
		}
	}
}

func TestNoTemporaryFilesSurviveASuccessfulPersist(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(filepath.Join(dir, "state.bin"))
	for i := 0; i < 5; i++ {
		if err := s.Persist([]byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.bin" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("a completed persist must leave exactly one file, found %v", names)
	}
}

func TestConcurrentReadersNeverSeeAPartialWrite(t *testing.T) {
	s := store(t)
	a := bytes.Repeat([]byte("A"), 8192)
	b := bytes.Repeat([]byte("B"), 8192)
	if err := s.Persist(a); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad int
	var mu sync.Mutex

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := s.Load()
				if err != nil {
					continue // a momentary absence during rename is not corruption
				}
				if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
					mu.Lock()
					bad++
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < 60; i++ {
		payload := a
		if i%2 == 1 {
			payload = b
		}
		if err := s.Persist(payload); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d reads observed a payload that was never written whole", bad)
	}
}

func TestLoadOnADirectoryPathFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(dir) // the path is a directory, not a file
	if _, err := s.Load(); err == nil {
		t.Fatal("loading a directory as state must fail rather than return garbage")
	}
}

func TestPersistOntoAnUnwritablePathReportsTheError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	s := NewFileStore(filepath.Join(locked, "state.bin"))
	if err := s.Persist([]byte("x")); err == nil {
		t.Fatal("an unwritable destination must surface an error, not fail silently")
	}
}

func TestPathAccessorReportsTheConfiguredLocation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.bin")
	if got := NewFileStore(p).Path(); got != p {
		t.Fatalf("Path must report the configured location, got %q", got)
	}
}
