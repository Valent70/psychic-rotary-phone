// This file answers Commercialization Sprint P0-A: "Commercial API
// Store masih in-memory ... Itu bukan masalah kosmetik. Itu menyentuh:
// data persistence, disaster recovery, evidence preservation, legal
// hold, audit continuity, backup, restore, RPO/RTO, customer trust."
//
// The mechanism is event sourcing over the real pkg/storage/wal write-
// ahead log (fsync, CRC, defect-classified recovery -- already built
// and proved for the consensus layer, reused here rather than
// reinvented): every successful mutating call appends its own INPUT to
// the WAL, and NewDurableStore reconstructs identical state on startup
// by replaying those inputs back through this Store's own real,
// unexported "*Core" methods (the same logic the public methods
// already run -- replay is not a second implementation of anything).
// This is sound because manifest.Registry and audit.AuditStore are
// both already deterministic given an identical call sequence: neither
// reads the wall clock or any randomness, so replaying the same
// (actor, action, payload) or (evidenceID, state, tick) sequence
// through their real public methods reproduces byte-identical hashes,
// not merely equivalent-looking state. No new export/import surface
// was added to either FROZEN package for this.
package commercialapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"veriqo/pkg/storage/wal"
)

type walCommandKind string

const (
	walCmdCreateCase     walCommandKind = "CREATE_CASE"
	walCmdSubmitEvidence walCommandKind = "SUBMIT_EVIDENCE"
	walCmdDecideCase     walCommandKind = "DECIDE_CASE"
	walCmdActOnCase      walCommandKind = "ACT_ON_CASE"
)

// createCaseArgs is CreateCase's argument tuple, captured verbatim so
// replay calls the exact same core logic with the exact same inputs.
type createCaseArgs struct {
	TenantID string
	CaseID   string
	Tick     uint64
}

// walCommand is one WAL record's decoded payload -- exactly one of the
// four pointer fields is non-nil, matching Kind.
type walCommand struct {
	Kind       walCommandKind
	CreateCase *createCaseArgs `json:",omitempty"`
	Evidence   *EvidenceInput  `json:",omitempty"`
	Decide     *DecideInput    `json:",omitempty"`
	Action     *ActionInput    `json:",omitempty"`
}

// appendWAL is a no-op for an in-memory-only Store (s.wal == nil,
// NewStore's default) -- every existing caller of NewStore keeps
// working exactly as before. For a durable Store, it encodes cmd and
// appends it to the WAL; a non-nil error here means the operation that
// just succeeded in memory is NOT yet durably confirmed -- callers
// must treat it as a failed write (this package's own public methods
// do exactly that, returning the zero value and this error instead of
// the in-memory result).
func (s *Store) appendWAL(tick uint64, cmd walCommand) error {
	if s.wal == nil {
		return nil
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("commercialapi: encoding WAL command: %w", err)
	}
	if _, err := s.wal.Append(tick, payload); err != nil {
		return fmt.Errorf("commercialapi: appending to WAL (write not durable): %w", err)
	}
	return nil
}

// NewDurableStore opens (or creates) a WAL at walDir and reconstructs a
// Store from it: every record ever durably appended is replayed, in
// order, through the same core logic the live public methods use, so
// the returned Store's manifests, ledger, cases, and evidence are
// byte-identical to what they were the moment before whatever stopped
// the previous process. The returned *wal.RecoveryReport is the real,
// defect-classified account of what pkg/storage/wal found on disk
// (DefectNone on a clean shutdown; DefectCorruptTail/PartialRecord
// etc. on a crash mid-write, truncated and reported, never silently
// treated as if it did not happen). A replay failure (a record whose
// command the Store's own core logic refuses) is returned as an error,
// not silently skipped -- a corrupted or foreign WAL must never be
// mistaken for a smaller, valid one.
func NewDurableStore(walDir string) (*Store, *wal.RecoveryReport, error) {
	log, report, err := wal.Open(wal.Config{Dir: walDir, Durable: true})
	if err != nil {
		return nil, report, fmt.Errorf("commercialapi: NewDurableStore: opening WAL at %s: %w", walDir, err)
	}
	s := NewStore()
	s.wal = log
	s.walDir = walDir

	records, err := log.Read()
	if err != nil {
		return nil, report, fmt.Errorf("commercialapi: NewDurableStore: reading WAL for replay: %w", err)
	}
	for _, rec := range records {
		var cmd walCommand
		if err := json.Unmarshal(rec.Payload, &cmd); err != nil {
			return nil, report, fmt.Errorf("commercialapi: NewDurableStore: decoding WAL record seq=%d: %w", rec.Seq, err)
		}
		if err := s.replayCommand(cmd); err != nil {
			return nil, report, fmt.Errorf("commercialapi: NewDurableStore: replaying WAL record seq=%d (%s): %w", rec.Seq, cmd.Kind, err)
		}
	}
	return s, report, nil
}

// replayCommand calls this Store's own unexported core logic directly
// -- never the public wrapper -- so replay reconstructs state without
// re-appending the very records it is reading.
func (s *Store) replayCommand(cmd walCommand) error {
	switch cmd.Kind {
	case walCmdCreateCase:
		if cmd.CreateCase == nil {
			return fmt.Errorf("commercialapi: CREATE_CASE record missing its args")
		}
		return s.createCaseCore(cmd.CreateCase.TenantID, cmd.CreateCase.CaseID, cmd.CreateCase.Tick)
	case walCmdSubmitEvidence:
		if cmd.Evidence == nil {
			return fmt.Errorf("commercialapi: SUBMIT_EVIDENCE record missing its input")
		}
		_, err := s.submitEvidenceCore(*cmd.Evidence)
		return err
	case walCmdDecideCase:
		if cmd.Decide == nil {
			return fmt.Errorf("commercialapi: DECIDE_CASE record missing its input")
		}
		_, err := s.decideCaseCore(*cmd.Decide)
		return err
	case walCmdActOnCase:
		if cmd.Action == nil {
			return fmt.Errorf("commercialapi: ACT_ON_CASE record missing its input")
		}
		_, _, err := s.actOnCaseCore(*cmd.Action)
		return err
	default:
		return fmt.Errorf("commercialapi: unknown WAL command kind %q", cmd.Kind)
	}
}

// Close releases this Store's WAL handle (a no-op for an in-memory-only
// Store). Safe to call once after the Store is no longer needed. After
// Close, Healthy reports false -- see Healthy's own doc comment, which
// this method's own P0-6 readiness-probe consumer (GET /v1/readyz)
// relies on to stop reporting ready once shutdown has begun.
func (s *Store) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.wal == nil {
		return nil
	}
	return s.wal.Close()
}

// Healthy answers Commercialization Sprint item P0-6/7 ("liveness/
// readiness probes"): whether this Store is currently fit to serve
// requests, and, on false, why. An in-memory-only Store (NewStore) is
// always healthy until Close is called -- there is no durability
// dependency to be unready for. A durable Store (NewDurableStore) is
// healthy exactly while its WAL is open (i.e. Close has not been
// called) -- this is a liveness-of-the-handle check, not a disk-space
// or fsync-latency probe (neither is information this Store's own
// wal.Log exposes today, named here rather than silently assumed).
func (s *Store) Healthy() (bool, string) {
	s.mu.Lock()
	closed := s.closed
	durable := s.wal != nil
	s.mu.Unlock()
	if closed {
		return false, "store is closed"
	}
	if !durable {
		return true, "in-memory store (no durability dependency)"
	}
	return true, "durable store, WAL open"
}

// Backup copies this Store's entire WAL directory -- every segment
// that makes up its durable history -- to destDir, which must not
// already exist. It first calls Read on the WAL, which fsyncs the
// currently-open segment before returning, so the copy always includes
// every record a caller has ever been told was durably written. Backup
// is a no-op returning ErrNotDurable for an in-memory-only Store: there
// is nothing on disk to copy.
func (s *Store) Backup(destDir string) error {
	if s.wal == nil {
		return ErrNotDurable
	}
	if _, err := s.wal.Read(); err != nil {
		return fmt.Errorf("commercialapi: Backup: flushing WAL before copy: %w", err)
	}
	if err := copyDirTree(s.walDir, destDir); err != nil {
		return fmt.Errorf("commercialapi: Backup: %w", err)
	}
	return nil
}

// RestoreStoreFromBackup builds a fresh, durable Store at walDir (which
// must not already exist) by copying backupDir's contents into it and
// then replaying it exactly as NewDurableStore would any other WAL
// directory -- backup and normal crash recovery are the same code
// path, which is the point: a backup is not a separate, untested
// format, it is a copy of the real thing.
func RestoreStoreFromBackup(backupDir, walDir string) (*Store, *wal.RecoveryReport, error) {
	if err := copyDirTree(backupDir, walDir); err != nil {
		return nil, nil, fmt.Errorf("commercialapi: RestoreStoreFromBackup: copying %s to %s: %w", backupDir, walDir, err)
	}
	return NewDurableStore(walDir)
}

// copyDirTree copies every regular file directly under src into a
// freshly-created dst (WAL directories are flat -- segment files only,
// no subdirectories -- so this deliberately does not recurse).
func copyDirTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading source directory %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return fmt.Errorf("creating destination directory %s: %w", dst, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o640); err != nil {
			return fmt.Errorf("writing %s: %w", e.Name(), err)
		}
	}
	return nil
}
