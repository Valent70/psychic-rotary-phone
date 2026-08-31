package commercialapi

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/storage/wal"
)

// TestNewDurableStoreReconstructsIdenticalStateAfterRestart is the
// central claim of P0-A: build a durable Store, run a full case
// lifecycle through it, "restart" (open a brand new Store against the
// same WAL directory -- the only thing a real process restart would
// leave behind), and prove the reconstructed Decision/Action hashes
// are byte-identical to the originals, not merely equivalent-looking.
func TestNewDurableStoreReconstructsIdenticalStateAfterRestart(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "wal")
	s1, report, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	if report.RecoveredRecords != 0 {
		t.Fatalf("expected a fresh WAL to recover 0 records, got %d", report.RecoveredRecords)
	}

	const tenant, caseID = "tenant-durable-A", "CASE-DURABLE-1"
	if err := s1.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s1, tenant, caseID, "EV-API-1", 10)
	d1, err := s1.DecideCase(decideInput(tenant, caseID))
	if err != nil {
		t.Fatalf("DecideCase: %v", err)
	}
	aa1, receipt1, err := s1.ActOnCase(actionInput(tenant, caseID))
	if err != nil {
		t.Fatalf("ActOnCase: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// "Restart": a brand new process would only ever see the WAL
	// directory again, never s1 itself.
	s2, report2, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore (restart): %v", err)
	}
	defer s2.Close()
	if report2.ReplayedRecords != 4 {
		t.Fatalf("expected 4 replayed records (create case, submit evidence, decide, act), got %d", report2.ReplayedRecords)
	}

	view, err := s2.GetCase(tenant, caseID)
	if err != nil {
		t.Fatalf("GetCase after restart: %v", err)
	}
	if !view.Decided || !view.ActedOn || len(view.EvidenceIDs) != 1 {
		t.Fatalf("unexpected case view after restart: %+v", view)
	}

	// The strong claim: not "a case exists," but "the exact same
	// Decision/Action, byte for byte." Prove it by re-deciding is
	// refused (ErrAlreadyDecided) and by an independent replay against
	// the restarted Store converging on the ORIGINAL hashes.
	if _, err := s2.DecideCase(decideInput(tenant, caseID)); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("expected ErrAlreadyDecided against the restarted Store, got %v", err)
	}
	replay, err := s2.Replay(tenant, caseID)
	if err != nil {
		t.Fatalf("Replay after restart: %v", err)
	}
	if !replay.Converged {
		t.Fatalf("expected replay to converge after restart, got %+v", replay)
	}
	if replay.OriginalDecisionHash != d1.Hash() {
		t.Fatalf("restarted Store's own Decision hash %s does not match the original %s", replay.OriginalDecisionHash, d1.Hash())
	}
	if replay.OriginalActionHash != aa1.Hash() {
		t.Fatalf("restarted Store's own Action hash %s does not match the original %s", replay.OriginalActionHash, aa1.Hash())
	}

	dos, err := s2.GenerateDossier(tenant, caseID)
	if err != nil {
		t.Fatalf("GenerateDossier after restart: %v", err)
	}
	if dos.PackageHash == "" {
		t.Fatal("expected a populated dossier after restart")
	}
	_ = receipt1
}

// TestDurableStoreCrossRestartTenantIsolationStillHolds proves P0-A
// did not weaken P0-B/item-19's own isolation guarantee: a case
// reconstructed from the WAL is still scoped to its real tenant only.
func TestDurableStoreCrossRestartTenantIsolationStillHolds(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "wal")
	s1, _, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	const tenant, caseID = "tenant-durable-iso-A", "CASE-DURABLE-ISO-1"
	if err := s1.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	s1.Close()

	s2, _, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore (restart): %v", err)
	}
	defer s2.Close()

	if _, err := s2.GetCase("tenant-durable-iso-B", caseID); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("expected ErrCaseNotFound for a different tenant after restart, got %v", err)
	}
}

// TestDurableStoreRecoversFromATornWrite proves the reviewer's own
// "kalau server mati, data saya bagaimana?" scenario directly: a crash
// mid-append (a torn write -- header written, payload absent) must be
// truncated and reported, never silently treated as data loss beyond
// the one unconfirmed record, and every record before it must still be
// there after restart.
func TestDurableStoreRecoversFromATornWrite(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "wal")
	s1, _, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	const tenant, caseID = "tenant-durable-torn-A", "CASE-DURABLE-TORN-1"
	if err := s1.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s1, tenant, caseID, "EV-DURABLE-TORN-1", 10)
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a crash mid-append: truncate the last few bytes off the
	// single WAL segment so the last record's header exists but its
	// payload/hash does not.
	entries, err := os.ReadDir(walDir)
	if err != nil {
		t.Fatalf("reading WAL dir: %v", err)
	}
	var segPath string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".wal" {
			segPath = filepath.Join(walDir, e.Name())
		}
	}
	if segPath == "" {
		t.Fatal("expected a .wal segment file to exist")
	}
	info, err := os.Stat(segPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.Truncate(segPath, info.Size()-8); err != nil {
		t.Fatalf("truncating to simulate a torn write: %v", err)
	}

	s2, report, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore after simulated crash: %v", err)
	}
	defer s2.Close()

	if report.FailedClosed {
		t.Fatalf("expected a torn TAIL write to be a recoverable truncation, not a fail-closed refusal: %+v", report)
	}
	if len(report.Findings) == 0 {
		t.Fatal("expected the recovery report to record at least one Finding for the truncated tail")
	}

	// The case creation record (the first append) must have survived
	// intact; only the tail is uncertain.
	view, err := s2.GetCase(tenant, caseID)
	if err != nil {
		t.Fatalf("GetCase after crash recovery: %v", err)
	}
	if view.CaseID != caseID {
		t.Fatalf("unexpected case view after crash recovery: %+v", view)
	}
}

// TestStoreBackupAndRestoreRoundTrip proves the reviewer's explicit
// "restore test" requirement: back up a live durable Store, then
// restore into a completely separate directory (as a real disaster
// recovery would -- the original is never touched again), and confirm
// the restored Store's state matches exactly.
func TestStoreBackupAndRestoreRoundTrip(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "wal")
	s1, _, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	const tenant, caseID = "tenant-durable-backup-A", "CASE-DURABLE-BACKUP-1"
	if err := s1.CreateCase(tenant, caseID, 0); err != nil {
		t.Fatalf("CreateCase: %v", err)
	}
	mustSubmitEvidence(t, s1, tenant, caseID, "EV-API-1", 10)
	d1, err := s1.DecideCase(decideInput(tenant, caseID))
	if err != nil {
		t.Fatalf("DecideCase: %v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	if err := s1.Backup(backupDir); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	restoredWALDir := filepath.Join(t.TempDir(), "restored-wal")
	s2, _, err := RestoreStoreFromBackup(backupDir, restoredWALDir)
	if err != nil {
		t.Fatalf("RestoreStoreFromBackup: %v", err)
	}
	defer s2.Close()

	d2View, err := s2.GetCase(tenant, caseID)
	if err != nil {
		t.Fatalf("GetCase on restored Store: %v", err)
	}
	if d2View.Outcome != string(d1.Outcome()) {
		t.Fatalf("restored Store's outcome %s does not match original %s", d2View.Outcome, d1.Outcome())
	}

	replay, err := s2.Replay(tenant, caseID)
	if err != nil {
		t.Fatalf("Replay on restored Store: %v", err)
	}
	if replay.OriginalDecisionHash != d1.Hash() {
		t.Fatalf("restored Store's Decision hash %s does not match the pre-backup original %s", replay.OriginalDecisionHash, d1.Hash())
	}
}

// TestBackupOnInMemoryStoreRefusesHonestly proves Backup never
// pretends an in-memory-only Store has something durable to copy.
func TestBackupOnInMemoryStoreRefusesHonestly(t *testing.T) {
	s := NewStore()
	if err := s.Backup(filepath.Join(t.TempDir(), "backup")); !errors.Is(err, ErrNotDurable) {
		t.Fatalf("expected ErrNotDurable for an in-memory Store's Backup, got %v", err)
	}
}

// TestCloseOnInMemoryStoreIsANoOp guards NewStore's own zero-cost
// contract: every existing caller of NewStore (every pre-P0-A test in
// this package) must keep working with no behavior change.
func TestCloseOnInMemoryStoreIsANoOp(t *testing.T) {
	s := NewStore()
	if err := s.Close(); err != nil {
		t.Fatalf("expected Close on an in-memory Store to be a no-op, got %v", err)
	}
}

// TestHealthyReflectsStoreLifecycle proves Store.Healthy (the readiness
// probe's own dependency check -- see veriqo/gateway/rest's readyzHandler)
// tells the truth across every stage of a Store's lifecycle: an
// in-memory Store is always healthy, a durable Store is healthy while
// open, and unhealthy once Close has been called.
func TestHealthyReflectsStoreLifecycle(t *testing.T) {
	mem := NewStore()
	if healthy, detail := mem.Healthy(); !healthy || detail == "" {
		t.Fatalf("expected an in-memory Store to be healthy with a non-empty detail, got healthy=%v detail=%q", healthy, detail)
	}

	walDir := filepath.Join(t.TempDir(), "wal")
	s, _, err := NewDurableStore(walDir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	if healthy, detail := s.Healthy(); !healthy || detail == "" {
		t.Fatalf("expected an open durable Store to be healthy with a non-empty detail, got healthy=%v detail=%q", healthy, detail)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if healthy, detail := s.Healthy(); healthy || detail == "" {
		t.Fatalf("expected a closed durable Store to be unhealthy with a non-empty detail, got healthy=%v detail=%q", healthy, detail)
	}
}

// TestNewDurableStoreRefusesAForeignPayload proves replay does not
// silently ignore a record it cannot make sense of -- protecting
// against a WAL directory that was not actually written by this
// package (or was corrupted in a way recovery's own CRC check did not
// already catch, e.g. valid JSON that decodes to an unknown command
// kind).
func TestNewDurableStoreRefusesAForeignPayload(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "wal")
	log, _, err := wal.Open(wal.Config{Dir: walDir, Durable: true})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	if _, err := log.Append(0, []byte(`{"Kind":"NOT_A_REAL_COMMAND"}`)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, _, err := NewDurableStore(walDir); err == nil {
		t.Fatal("expected NewDurableStore to refuse a WAL containing an unknown command kind")
	}
}
