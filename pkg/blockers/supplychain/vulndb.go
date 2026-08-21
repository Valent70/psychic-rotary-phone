package supplychain

// This file is PART 7 of the audit mandate: the repository-side
// abstraction for an *offline* vulnerability database ("local
// vulnerability mirror"), plus a fixture-backed implementation that
// works fully offline and its tests.
//
// Boundary, stated plainly (do not let this drift into a false-green
// claim):
//
//   - CODE-COMPLETE, right now, verified by the tests in
//     vulndb_test.go: the VulnerabilityDatabase interface, the
//     OfflineSnapshot metadata shape, FixtureVulnerabilityDatabase
//     (loads a real offline JSON fixture -- embedded via go:embed AND
//     loadable from arbitrary bytes), Lookup returning real results for
//     fixtured module@version pairs, fail-closed behavior on an
//     unloaded/stale database (never silently "no vulnerabilities"),
//     and SnapshotHash as a genuine sha256 content hash of the fixture
//     bytes (changes if the fixture content changes -- proven by
//     TestSnapshotHashChangesWithFixtureContent).
//   - NOT CLAIMED: that this repository's real, current vulnerability
//     exposure has been checked against a live database. That requires
//     running the actual supported scanner (govulncheck, backed by
//     vuln.go.dev/osv.dev) with unrestricted network access. This
//     sandbox's network policy returns 403 from vuln.go.dev, osv.dev,
//     and the GitHub advisory API alike -- reconfirmed across multiple
//     sessions, see evidence/supply_chain_scan-gosec-full.txt,
//     evidence/supply_chain_scan-gosec.txt and
//     evidence/supply_chain_scan-staticcheck.txt. HTTPVulnerabilityProvider
//     in supplychain.go is the real, unit-tested-against-httptest HTTP
//     client for that job; RunQualification structurally refuses to let
//     a REAL-mode provider (which includes a live VulnerabilityDatabase
//     backed by a network snapshot puller, were one added) advance a
//     contract's status -- that evidence belongs in
//     pkg/governance/qualification once unrestricted CI network access
//     makes it obtainable. Nothing in this file changes that boundary;
//     it only makes the offline half of the story real and testable.

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

//go:embed testdata/vulndb_fixture.json
var embeddedFixtureJSON []byte

// CoveragePeriod is the inclusive window (Unix seconds) the offline
// snapshot's data is asserted to cover. Both ends are required metadata
// per the audit mandate's field list.
type CoveragePeriod struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}

// OfflineSnapshot is the exact metadata set the audit mandate requires,
// field for field: source, database_version, snapshot_hash,
// acquired_at, coverage_period, scanner_version, scan_time.
//
// SnapshotHash is deliberately never trusted from input -- see
// LoadFixtureVulnerabilityDatabase, which always recomputes it from the
// raw fixture bytes so it is a genuine content hash rather than a
// literal a fixture author could forget to update.
type OfflineSnapshot struct {
	Source          string         `json:"source"`
	DatabaseVersion string         `json:"database_version"`
	SnapshotHash    string         `json:"snapshot_hash"`
	AcquiredAt      uint64         `json:"acquired_at"`
	CoveragePeriod  CoveragePeriod `json:"coverage_period"`
	ScannerVersion  string         `json:"scanner_version"`
	ScanTime        uint64         `json:"scan_time"`
}

// Freshness is VulnerabilityDatabase.Freshness's return shape: when the
// snapshot was pulled, and the period it claims to cover.
type Freshness struct {
	AcquiredAt          uint64
	CoveragePeriodStart uint64
	CoveragePeriodEnd   uint64
}

// Coverage is VulnerabilityDatabase.Coverage's return shape: which
// scanner produced the snapshot and when it ran.
type Coverage struct {
	ScannerVersion string
	ScanTime       uint64
}

// VulnerabilityDatabase extends VulnerabilityProvider (rather than
// duplicating it) with the per-lookup, provenance-bearing surface the
// audit mandate asks for. Any VulnerabilityDatabase is therefore also a
// VulnerabilityProvider and can be passed anywhere Run/RunQualification
// already accept one.
type VulnerabilityDatabase interface {
	VulnerabilityProvider

	// Lookup returns the known vulnerabilities for one module@version.
	// A loaded, fresh database with no matching entry returns
	// (nil, nil) -- a genuine negative result. An unloaded or stale
	// database MUST fail closed: see ErrDatabaseNotLoaded and
	// ErrDatabaseStale.
	Lookup(module, version string) ([]Vulnerability, error)

	// Version is the offline database's own version string
	// (OfflineSnapshot.DatabaseVersion).
	Version() string

	// SnapshotHash is the content hash of the offline snapshot this
	// instance was built from (OfflineSnapshot.SnapshotHash).
	SnapshotHash() string

	// Freshness exposes acquired_at and coverage_period.
	Freshness() Freshness

	// Coverage exposes scanner_version and scan_time.
	Coverage() Coverage
}

// ErrDatabaseNotLoaded is returned by Lookup on a
// FixtureVulnerabilityDatabase that was never populated from a
// snapshot -- the zero value, or one whose Load call failed. This is
// the fail-closed signal that distinguishes "never loaded" from "loaded
// and genuinely found nothing".
var ErrDatabaseNotLoaded = errors.New("supplychain: vulnerability database not loaded -- refusing to report an unqualified negative result")

// ErrDatabaseStale is returned by Lookup when a MaxAge policy was
// configured and the loaded snapshot's acquired_at is older than that
// policy allows. Like ErrDatabaseNotLoaded, this fails closed rather
// than silently returning a possibly-outdated "no vulnerabilities"
// result.
var ErrDatabaseStale = errors.New("supplychain: vulnerability database snapshot is stale -- refusing to report an unqualified negative result")

// fixtureFile is the on-disk/embedded JSON shape LoadFixtureVulnerabilityDatabase parses.
type fixtureFile struct {
	Source          string                     `json:"source"`
	DatabaseVersion string                     `json:"database_version"`
	AcquiredAt      uint64                     `json:"acquired_at"`
	CoveragePeriod  CoveragePeriod             `json:"coverage_period"`
	ScannerVersion  string                     `json:"scanner_version"`
	ScanTime        uint64                     `json:"scan_time"`
	Entries         map[string][]Vulnerability `json:"entries"`
}

// FixtureVulnerabilityDatabase is the concrete, fully-offline
// VulnerabilityDatabase implementation: SourceType is always
// "OFFLINE_SNAPSHOT" and Mode is always "SIMULATED" -- it never makes a
// network call, matching OfflineFixtureProvider's honesty convention
// elsewhere in this package.
//
// The zero value (&FixtureVulnerabilityDatabase{}) is intentionally
// inert: loaded is false, so Lookup fails closed with
// ErrDatabaseNotLoaded until one of the Load constructors below runs.
type FixtureVulnerabilityDatabase struct {
	snapshot OfflineSnapshot
	entries  map[string][]Vulnerability
	loaded   bool

	// maxAgeSeconds, if non-zero, is the staleness policy: Lookup fails
	// closed with ErrDatabaseStale once now-AcquiredAt exceeds it.
	maxAgeSeconds uint64
	// now is an injectable clock for tests; defaults to real wall time.
	now func() uint64
}

func (db *FixtureVulnerabilityDatabase) clock() uint64 {
	if db.now != nil {
		return db.now()
	}
	return uint64(time.Now().Unix()) // #nosec G115 -- time.Now().Unix() will not go negative until the year 1970, and this repo will not run then
}

// LoadFixtureVulnerabilityDatabase parses raw offline-snapshot JSON
// (the fixtureFile shape) and returns a loaded, ready-to-query
// database. SnapshotHash is always computed here, as sha256 of the
// exact bytes passed in -- never taken from the JSON itself -- so it is
// a genuine content hash: change one byte of data and the hash changes
// (see TestSnapshotHashChangesWithFixtureContent).
func LoadFixtureVulnerabilityDatabase(data []byte, maxAgeSeconds uint64) (*FixtureVulnerabilityDatabase, error) {
	var f fixtureFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("supplychain: parse offline vulnerability snapshot: %w", err)
	}
	if f.Source == "" || f.DatabaseVersion == "" {
		return nil, errors.New("supplychain: offline vulnerability snapshot missing required source/database_version metadata")
	}
	sum := sha256.Sum256(data)
	db := &FixtureVulnerabilityDatabase{
		snapshot: OfflineSnapshot{
			Source:          f.Source,
			DatabaseVersion: f.DatabaseVersion,
			SnapshotHash:    hex.EncodeToString(sum[:]),
			AcquiredAt:      f.AcquiredAt,
			CoveragePeriod:  f.CoveragePeriod,
			ScannerVersion:  f.ScannerVersion,
			ScanTime:        f.ScanTime,
		},
		entries:       f.Entries,
		loaded:        true,
		maxAgeSeconds: maxAgeSeconds,
	}
	return db, nil
}

// NewEmbeddedFixtureVulnerabilityDatabase loads the offline snapshot
// embedded at build time from testdata/vulndb_fixture.json -- no
// filesystem access and no network access at run time, so it works in
// this sandbox exactly as it will anywhere else. maxAgeSeconds is the
// optional staleness policy (0 disables staleness checking beyond
// "was it ever loaded").
func NewEmbeddedFixtureVulnerabilityDatabase(maxAgeSeconds uint64) (*FixtureVulnerabilityDatabase, error) {
	return LoadFixtureVulnerabilityDatabase(embeddedFixtureJSON, maxAgeSeconds)
}

// Mode implements VulnerabilityProvider. A fixture-backed offline
// mirror never touches the network, so it is always SIMULATED, never
// REAL -- RunQualification's REAL-mode refusal therefore applies to it
// exactly as it does to OfflineFixtureProvider.
func (db *FixtureVulnerabilityDatabase) Mode() string { return "SIMULATED" }

// SourceType implements VulnerabilityProvider.
func (db *FixtureVulnerabilityDatabase) SourceType() string { return "OFFLINE_SNAPSHOT" }

// Query implements VulnerabilityProvider by calling Lookup for each
// dependency and aggregating results. It propagates a fail-closed
// Lookup error immediately rather than silently skipping the
// dependency that triggered it.
func (db *FixtureVulnerabilityDatabase) Query(_ context.Context, deps []Dependency) ([]Vulnerability, error) {
	var all []Vulnerability
	for _, d := range deps {
		found, err := db.Lookup(d.Module, d.Version)
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	return all, nil
}

// Lookup returns the known vulnerabilities for module@version. It fails
// closed -- returning an explicit error, never a silent empty result --
// whenever the database is not in a state that can honestly answer:
//
//   - never loaded (the zero value, or a failed Load): ErrDatabaseNotLoaded
//   - loaded but past its configured staleness policy: ErrDatabaseStale
//
// Only once both checks pass does an empty return value mean what it
// looks like it means: this snapshot genuinely has no known
// vulnerability for that module@version.
func (db *FixtureVulnerabilityDatabase) Lookup(module, version string) ([]Vulnerability, error) {
	if db == nil || !db.loaded {
		return nil, ErrDatabaseNotLoaded
	}
	if db.maxAgeSeconds > 0 {
		now := db.clock()
		if now < db.snapshot.AcquiredAt || now-db.snapshot.AcquiredAt > db.maxAgeSeconds {
			return nil, ErrDatabaseStale
		}
	}
	key := module + "@" + version
	return db.entries[key], nil
}

// Version implements VulnerabilityDatabase.
func (db *FixtureVulnerabilityDatabase) Version() string { return db.snapshot.DatabaseVersion }

// SnapshotHash implements VulnerabilityDatabase.
func (db *FixtureVulnerabilityDatabase) SnapshotHash() string { return db.snapshot.SnapshotHash }

// Freshness implements VulnerabilityDatabase.
func (db *FixtureVulnerabilityDatabase) Freshness() Freshness {
	return Freshness{
		AcquiredAt:          db.snapshot.AcquiredAt,
		CoveragePeriodStart: db.snapshot.CoveragePeriod.Start,
		CoveragePeriodEnd:   db.snapshot.CoveragePeriod.End,
	}
}

// Coverage implements VulnerabilityDatabase.
func (db *FixtureVulnerabilityDatabase) Coverage() Coverage {
	return Coverage{ScannerVersion: db.snapshot.ScannerVersion, ScanTime: db.snapshot.ScanTime}
}

var (
	_ VulnerabilityProvider = (*FixtureVulnerabilityDatabase)(nil)
	_ VulnerabilityDatabase = (*FixtureVulnerabilityDatabase)(nil)
)
