package supplychain

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestEmbeddedFixtureLoadsAndLooksUpRealResult(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(0)
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}

	found, err := db.Lookup("example.com/vulnerable", "v1.0.0")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly 1 vulnerability, got %d: %+v", len(found), found)
	}
	v := found[0]
	if v.ID != "GO-2099-0001" {
		t.Errorf("ID = %q, want GO-2099-0001", v.ID)
	}
	if v.Package != "example.com/vulnerable" {
		t.Errorf("Package = %q, want example.com/vulnerable", v.Package)
	}
	if v.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want HIGH", v.Severity)
	}
	if v.FixedIn != "v1.0.1" {
		t.Errorf("FixedIn = %q, want v1.0.1", v.FixedIn)
	}
	if v.AffectedRange == "" {
		t.Error("AffectedRange should be populated from the fixture")
	}
	if v.Description == "" {
		t.Error("Description should be populated from the fixture")
	}

	// A second module in the same fixture, to prove Lookup keys per
	// module@version rather than returning everything.
	found2, err := db.Lookup("example.com/critical", "v2.3.4")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(found2) != 1 || found2[0].Severity != SeverityCritical {
		t.Fatalf("expected exactly 1 CRITICAL finding for example.com/critical@v2.3.4, got %+v", found2)
	}
}

func TestLookupOnGenuinelyCleanModuleReturnsEmptyNotError(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(0)
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}
	found, err := db.Lookup("example.com/totally-clean", "v9.9.9")
	if err != nil {
		t.Fatalf("Lookup on a loaded, fresh database for an unlisted module must not error, got: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected zero findings for an unlisted module, got %+v", found)
	}
}

func TestLookupOnZeroValueDatabaseFailsClosed(t *testing.T) {
	var db FixtureVulnerabilityDatabase // never loaded
	_, err := db.Lookup("example.com/vulnerable", "v1.0.0")
	if err == nil {
		t.Fatal("Lookup on an unloaded database must fail closed, got nil error")
	}
	if !errors.Is(err, ErrDatabaseNotLoaded) {
		t.Fatalf("expected ErrDatabaseNotLoaded, got: %v", err)
	}
}

func TestLookupOnNilDatabaseFailsClosed(t *testing.T) {
	var db *FixtureVulnerabilityDatabase // nil pointer, not even zero-value struct
	_, err := db.Lookup("example.com/vulnerable", "v1.0.0")
	if !errors.Is(err, ErrDatabaseNotLoaded) {
		t.Fatalf("expected ErrDatabaseNotLoaded from a nil database, got: %v", err)
	}
}

func TestLookupOnStaleDatabaseFailsClosed(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(60) // 60-second staleness policy
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}
	// The embedded fixture's acquired_at is a fixed 2026 timestamp; a
	// clock far beyond it plus the 60s policy must report stale rather
	// than silently answering "no vulnerabilities".
	db.now = func() uint64 { return db.snapshot.AcquiredAt + 1_000_000 }

	_, err = db.Lookup("example.com/totally-clean", "v9.9.9")
	if err == nil {
		t.Fatal("Lookup on a stale database must fail closed, got nil error")
	}
	if !errors.Is(err, ErrDatabaseStale) {
		t.Fatalf("expected ErrDatabaseStale, got: %v", err)
	}

	// Even a module with a real fixtured finding must still fail closed
	// when stale -- staleness must not be silently bypassed just
	// because there happens to be a match.
	_, err = db.Lookup("example.com/vulnerable", "v1.0.0")
	if !errors.Is(err, ErrDatabaseStale) {
		t.Fatalf("expected ErrDatabaseStale even for a module with a fixtured finding, got: %v", err)
	}
}

func TestLookupWithinStaleWindowSucceeds(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(3600) // 1 hour policy
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}
	db.now = func() uint64 { return db.snapshot.AcquiredAt + 10 } // 10s later, well within policy

	found, err := db.Lookup("example.com/vulnerable", "v1.0.0")
	if err != nil {
		t.Fatalf("Lookup within the staleness window must succeed, got: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 finding, got %+v", found)
	}
}

func TestVersionSnapshotHashFreshnessCoverageReturnRealFixtureValues(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(0)
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}

	if db.Version() != "fixture-2026.08.21" {
		t.Errorf("Version() = %q, want fixture-2026.08.21", db.Version())
	}
	if db.SnapshotHash() == "" {
		t.Error("SnapshotHash() must not be empty for a loaded database")
	}
	if len(db.SnapshotHash()) != 64 { // sha256 hex digest length
		t.Errorf("SnapshotHash() = %q, expected a 64-character sha256 hex digest", db.SnapshotHash())
	}

	fresh := db.Freshness()
	if fresh.AcquiredAt == 0 {
		t.Error("Freshness().AcquiredAt must be populated from the fixture")
	}
	if fresh.CoveragePeriodStart == 0 || fresh.CoveragePeriodEnd == 0 {
		t.Errorf("Freshness() coverage period must be populated, got %+v", fresh)
	}
	if fresh.CoveragePeriodStart >= fresh.CoveragePeriodEnd {
		t.Errorf("coverage period start must precede end, got %+v", fresh)
	}

	cov := db.Coverage()
	if cov.ScannerVersion == "" {
		t.Error("Coverage().ScannerVersion must be populated from the fixture")
	}
	if cov.ScanTime == 0 {
		t.Error("Coverage().ScanTime must be populated from the fixture")
	}
}

func TestSnapshotHashChangesWithFixtureContent(t *testing.T) {
	original := bytes.Clone(embeddedFixtureJSON)
	dbOriginal, err := LoadFixtureVulnerabilityDatabase(original, 0)
	if err != nil {
		t.Fatalf("LoadFixtureVulnerabilityDatabase(original): %v", err)
	}

	mutated := bytes.Replace(bytes.Clone(embeddedFixtureJSON), []byte("fixture-2026.08.21"), []byte("fixture-2026.08.22"), 1)
	if bytes.Equal(mutated, embeddedFixtureJSON) {
		t.Fatal("test bug: mutation did not actually change the fixture bytes")
	}
	dbMutated, err := LoadFixtureVulnerabilityDatabase(mutated, 0)
	if err != nil {
		t.Fatalf("LoadFixtureVulnerabilityDatabase(mutated): %v", err)
	}

	if dbOriginal.SnapshotHash() == dbMutated.SnapshotHash() {
		t.Fatal("SnapshotHash must change when the underlying fixture content changes -- it must be a real content hash, not a static string")
	}
	// Sanity: hashing the same bytes twice is deterministic.
	dbAgain, err := LoadFixtureVulnerabilityDatabase(original, 0)
	if err != nil {
		t.Fatalf("LoadFixtureVulnerabilityDatabase(original again): %v", err)
	}
	if dbOriginal.SnapshotHash() != dbAgain.SnapshotHash() {
		t.Fatal("SnapshotHash must be deterministic for identical content")
	}
}

func TestLoadFixtureVulnerabilityDatabaseRejectsMalformedJSON(t *testing.T) {
	_, err := LoadFixtureVulnerabilityDatabase([]byte("{not valid json"), 0)
	if err == nil {
		t.Fatal("expected an error for malformed fixture JSON")
	}
}

func TestLoadFixtureVulnerabilityDatabaseRejectsMissingMetadata(t *testing.T) {
	_, err := LoadFixtureVulnerabilityDatabase([]byte(`{"entries": {}}`), 0)
	if err == nil {
		t.Fatal("expected an error for a fixture missing required source/database_version metadata")
	}
}

func TestFixtureVulnerabilityDatabaseSatisfiesVulnerabilityProvider(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(0)
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}
	if db.Mode() != "SIMULATED" {
		t.Errorf("Mode() = %q, want SIMULATED -- an offline fixture must never claim REAL", db.Mode())
	}
	if db.SourceType() != "OFFLINE_SNAPSHOT" {
		t.Errorf("SourceType() = %q, want OFFLINE_SNAPSHOT", db.SourceType())
	}

	found, err := db.Query(context.Background(), []Dependency{
		{Module: "example.com/vulnerable", Version: "v1.0.0"},
		{Module: "example.com/totally-clean", Version: "v9.9.9"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly 1 aggregated finding, got %+v", found)
	}
}

func TestFixtureVulnerabilityDatabaseQueryFailsClosedWhenNotLoaded(t *testing.T) {
	var db FixtureVulnerabilityDatabase
	_, err := db.Query(context.Background(), []Dependency{{Module: "example.com/x", Version: "v1.0.0"}})
	if !errors.Is(err, ErrDatabaseNotLoaded) {
		t.Fatalf("Query on an unloaded database must fail closed with ErrDatabaseNotLoaded, got: %v", err)
	}
}

// TestFixtureVulnerabilityDatabaseUsableThroughRunQualification proves
// the "extend, don't duplicate" requirement structurally: a
// VulnerabilityDatabase is a VulnerabilityProvider, so it plugs
// directly into the existing RunQualification pipeline with no adapter.
func TestFixtureVulnerabilityDatabaseUsableThroughRunQualification(t *testing.T) {
	db, err := NewEmbeddedFixtureVulnerabilityDatabase(0)
	if err != nil {
		t.Fatalf("NewEmbeddedFixtureVulnerabilityDatabase: %v", err)
	}
	c := goodContract()
	deps := []Dependency{{Module: "example.com/vulnerable", Version: "v1.0.0"}}
	policy := Policy{
		FailOn:         []Severity{SeverityCritical, SeverityHigh},
		Justifications: map[string]string{"GO-2099-0001": "fixture-only finding, tracked for documentation purposes only"},
	}
	result, err := RunQualification(context.Background(), c, deps, db, policy)
	if err != nil {
		t.Fatalf("RunQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass with the finding justified, got failure: %s", result.FailureReason)
	}
	if result.Measurements["source_type"] != "OFFLINE_SNAPSHOT" {
		t.Fatalf("expected source_type OFFLINE_SNAPSHOT in measurements, got %+v", result.Measurements)
	}
}
