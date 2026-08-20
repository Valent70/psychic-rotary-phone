// Package storage is the durability seam named as a gap in the prior
// session's report ("belum ada persistensi... dibutuhkan lapisan
// penyimpanan"): a small, stdlib-only, crash-atomic byte-blob store
// any caller can use to survive a process restart, generalized from
// the exact write-temp+fsync+rename pattern pkg/consensus/raftlite's
// FileStorage already proved in this repo (POSIX rename(2) is atomic,
// so a crash at any point while writing leaves the real file exactly
// as it was before this Persist call — never a torn write). This
// session's concrete consumer is veriqo/registry's Engine Registry,
// whose hash-chained ledgers need to survive across separate `veriqo`
// CLI/gateway process invocations to be more than a single-shot demo.
package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"veriqo/pkg/platform/telemetry"
)

// ErrNotFound is returned by Load when no state has ever been
// persisted at the store's path — a normal, expected condition on a
// process's first run, not an error callers should treat as fatal.
var ErrNotFound = errors.New("storage: no persisted state at this path")

// FileStore is a crash-atomic Store backed by one file.
type FileStore struct {
	path string
}

// NewFileStore builds a FileStore that persists to path. The
// directory containing path is created on first Persist if it does
// not already exist.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Path returns the file path this store persists to.
func (s *FileStore) Path() string { return s.path }

// Persist atomically writes data to the store's path: write to a
// temp file in the same directory, fsync it, then rename over the
// real path. A crash at any point before the rename completes leaves
// the previous Persist's data (or no file, on the very first call)
// untouched; a crash after cannot happen because rename(2) is a
// single atomic filesystem operation.
func (s *FileStore) Persist(data []byte) error {
	_, span := telemetry.StartSpan(context.Background(), "storage.Persist", telemetry.Attribute{Key: "path", Value: s.path})
	defer span.End()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("storage: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".storage-tmp-*")
	if err != nil {
		return fmt.Errorf("storage: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: once the rename below succeeds this is a
	// no-op (the temp path no longer exists under its temp name), so
	// the error from a successful-path Remove is deliberately ignored.
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // best-effort cleanup; the write error below is what matters
		return fmt.Errorf("storage: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("storage: atomic rename into place: %w", err)
	}
	return nil
}

// Load reads the most recently persisted data, or ErrNotFound if
// nothing has been persisted yet.
func (s *FileStore) Load() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: read %s: %w", s.path, err)
	}
	return data, nil
}
