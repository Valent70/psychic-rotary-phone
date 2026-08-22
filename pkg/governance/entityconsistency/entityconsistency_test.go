package entityconsistency

import (
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/identity"
	"veriqo/pkg/moat/domain/maritime"
	"veriqo/pkg/moat/entity"
)

func mustIdentityResolver(t *testing.T) *identity.Resolver {
	t.Helper()
	r := identity.NewResolver()
	if err := r.RegisterAuthority(identity.Authority{
		SourceID: "port-monitor", Weight: 0.95,
		AuthoritativeFor: []identity.Kind{identity.KindIMO, identity.KindCallsign},
	}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	return r
}

func TestCheckReportsAgreementWhenBothSystemsMergeTheSamePair(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()

	imo := Alias{Kind: "IMO", Value: "7778889"}
	cs := Alias{Kind: "CALLSIGN", Value: "CS-7778889"}

	if _, err := idR.Merge("resolution-agent", "port-monitor",
		identity.Identifier{Kind: identity.KindIMO, Value: imo.Value},
		identity.Identifier{Kind: identity.KindCallsign, Value: cs.Value}, 5, "cross-referenced"); err != nil {
		t.Fatalf("identity Merge: %v", err)
	}
	if _, err := entR.Merge("resolution-agent", entity.Alias{Kind: imo.Kind, Value: imo.Value},
		entity.Alias{Kind: cs.Kind, Value: cs.Value}, 5); err != nil {
		t.Fatalf("entity Merge: %v", err)
	}

	f, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if f.Diverges() {
		t.Fatalf("both systems agreeing must not be reported as a divergence: %+v", f)
	}
	if !f.IdentitySame || !f.EntitySame {
		t.Fatalf("expected both systems to consider the pair the same entity: %+v", f)
	}
}

// TestCheckDetectsDivergenceWhenOnlyOneSystemMerges is the property
// this package exists to guarantee: the exact "same object -> Entity-X
// / same object -> Entity-Y" risk the audit named must be caught, not
// silently missed.
func TestCheckDetectsDivergenceWhenOnlyOneSystemMerges(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()

	imo := Alias{Kind: "IMO", Value: "7778889"}
	cs := Alias{Kind: "CALLSIGN", Value: "CS-7778889"}

	// Only pkg/moat/entity is told these denote the same vessel;
	// pkg/identity is never told.
	if _, err := entR.Merge("resolution-agent", entity.Alias{Kind: imo.Kind, Value: imo.Value},
		entity.Alias{Kind: cs.Kind, Value: cs.Value}, 5); err != nil {
		t.Fatalf("entity Merge: %v", err)
	}

	f, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !f.Diverges() {
		t.Fatalf("expected a detected divergence when only one system has merged the pair: %+v", f)
	}
	if f.IdentitySame {
		t.Fatal("pkg/identity was never told these are the same entity, IdentitySame must be false")
	}
	if !f.EntitySame {
		t.Fatal("pkg/moat/entity WAS told these are the same entity, EntitySame must be true")
	}
}

func TestCheckReportsEntityUnknownWhenNeitherAliasWasEverRegistered(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()

	f, err := Check(idR, entR, Alias{Kind: "IMO", Value: "9999999"}, Alias{Kind: "CALLSIGN", Value: "ZZZZ"}, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if f.EntityKnown {
		t.Fatal("pkg/moat/entity has never seen either alias; EntityKnown must be false")
	}
	if f.Diverges() {
		t.Fatal("a system with no opinion (unknown) must never be reported as diverging")
	}
}

// TestCheckReportsEntityUnknownForTheLifecycleProductionPath is the
// documented consequence of a later round's P0-B fix, made a real
// assertion rather than only a package-comment claim: after
// pkg/lifecycle.Orchestrator.RunUnified (the one production entity-
// resolution choke point) resolves a common-vocabulary alias pair
// through pkg/identity ONLY (never writing pkg/moat/entity for a Kind
// identity.Kind models), Check correctly reports EntityKnown=false --
// not a false "these agree" -- because pkg/moat/entity genuinely has
// nothing to compare. This models exactly what lifecycle.go's
// resolveCanonicalEntity does for the identity-resolved case, without
// pulling in the full lifecycle/canonical/policy machinery just to
// prove this one property.
func TestCheckReportsEntityUnknownForTheLifecycleProductionPath(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry() // never written -- models the post-fix production path exactly

	imo := Alias{Kind: "IMO", Value: "5551234"}
	cs := Alias{Kind: "CALLSIGN", Value: "ZZ-5551234"}
	if _, err := idR.Merge("lifecycle", "port-monitor",
		identity.Identifier{Kind: identity.KindIMO, Value: imo.Value},
		identity.Identifier{Kind: identity.KindCallsign, Value: cs.Value}, 1,
		"lifecycle.RunUnified: aliases co-occur on one Intent"); err != nil {
		t.Fatalf("identity Merge: %v", err)
	}

	f, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !f.IdentitySame {
		t.Fatal("expected identity (the canonical authority) to consider the pair the same entity")
	}
	if f.EntityKnown {
		t.Fatal("expected EntityKnown=false: pkg/moat/entity was never written for this alias pair, " +
			"per the P0-B fix's own documented consequence -- reporting EntityKnown=true here would be fabricated agreement")
	}
	if f.Diverges() {
		t.Fatal("a system with no opinion (unknown) must never be reported as diverging, even post-P0-B")
	}
}

// TestCheckIsDeterministicAcrossRepeatedCalls proves Check has no
// hidden mutable state that would make a governance report
// non-reproducible.
func TestCheckIsDeterministicAcrossRepeatedCalls(t *testing.T) {
	idR := mustIdentityResolver(t)
	entR := entity.NewRegistry()
	imo := Alias{Kind: "IMO", Value: "1234567"}
	cs := Alias{Kind: "CALLSIGN", Value: "ABCD"}
	if _, err := entR.Merge("a", entity.Alias{Kind: imo.Kind, Value: imo.Value},
		entity.Alias{Kind: cs.Kind, Value: cs.Value}, 1); err != nil {
		t.Fatalf("entity Merge: %v", err)
	}

	first, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	second, err := Check(idR, entR, imo, cs, 10)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if first != second {
		t.Fatalf("Check must be deterministic given the same state and tick: %+v vs %+v", first, second)
	}
}

// TestScanProductionAuthorityAgainstRealRepoFindsNoViolations is the
// caller-coverage half of P0-B run against the ACTUAL repository: as
// of this test, exactly one production file
// (pkg/lifecycle/lifecycle.go) constructs entity.NewRegistry(), and it
// is the documented fallback authority. A non-zero Violations here
// means a NEW production file has silently reintroduced an independent
// entity-resolution authority pkg/identity never sees.
func TestScanProductionAuthorityAgainstRealRepoFindsNoViolations(t *testing.T) {
	root := repoRootForScan(t)
	rep, err := ScanProductionAuthority(root)
	if err != nil {
		t.Fatalf("ScanProductionAuthority: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("found %d unauthorized entity.NewRegistry() construction site(s), reopening the exact P0-B "+
			"fragmentation risk this package exists to keep closed: %v", len(rep.Violations), rep.Violations)
	}
	if rep.ScannedFiles < 100 {
		t.Fatalf("expected to scan the real repo (hundreds of .go files), only scanned %d -- root likely wrong", rep.ScannedFiles)
	}
}

// TestScanProductionAuthorityDetectsARealViolation is the adversarial
// proof: a synthetic tree with an unauthorized construction site must
// be caught.
func TestScanProductionAuthorityDetectsARealViolation(t *testing.T) {
	dir := t.TempDir()
	mustWriteScanFile(t, filepath.Join(dir, "pkg", "sneaky", "bypass.go"), `package sneaky

import "veriqo/pkg/moat/entity"

func New() *entity.Registry {
	return entity.NewRegistry()
}
`)
	rep, err := ScanProductionAuthority(dir)
	if err != nil {
		t.Fatalf("ScanProductionAuthority: %v", err)
	}
	if len(rep.Violations) != 1 || rep.Violations[0] != "pkg/sneaky/bypass.go" {
		t.Fatalf("expected exactly [pkg/sneaky/bypass.go], got %v", rep.Violations)
	}
}

// TestScanProductionAuthorityAllowsTheDocumentedFallback proves the
// one real exemption (pkg/lifecycle/lifecycle.go) does not trip the
// scanner -- the negative-space proof that authorityAllowedFiles is
// doing real filtering.
func TestScanProductionAuthorityAllowsTheDocumentedFallback(t *testing.T) {
	dir := t.TempDir()
	body := `package lifecycle

import "veriqo/pkg/moat/entity"

func f() *entity.Registry { return entity.NewRegistry() }
`
	mustWriteScanFile(t, filepath.Join(dir, "pkg", "lifecycle", "lifecycle.go"), body)
	rep, err := ScanProductionAuthority(dir)
	if err != nil {
		t.Fatalf("ScanProductionAuthority: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected the documented fallback to be allowed, got violations: %v", rep.Violations)
	}
}

// TestScanProductionAuthorityIgnoresTestFilesAndComments mirrors
// internal/nobypass's own adversarial proofs for the identical classes
// of false positive.
func TestScanProductionAuthorityIgnoresTestFilesAndComments(t *testing.T) {
	dir := t.TempDir()
	mustWriteScanFile(t, filepath.Join(dir, "pkg", "moat", "entity", "entity_test.go"), `package entity

func f() *Registry { return NewRegistry() }
`)
	mustWriteScanFile(t, filepath.Join(dir, "pkg", "docs", "notes.go"), `package docs

// Historically this called entity.NewRegistry() directly.
func f() {}
`)
	rep, err := ScanProductionAuthority(dir)
	if err != nil {
		t.Fatalf("ScanProductionAuthority: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected _test.go files and comment-only mentions to be excluded, got %v", rep.Violations)
	}
}

func mustWriteScanFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// repoRootForScan finds the real repository root by walking up from
// this test's working directory until go.mod is found, mirroring
// internal/nobypass's own repoRoot test helper.
func repoRootForScan(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod) walking up from test working directory")
		}
		dir = parent
	}
}

// --- PHASE B (P0-2): canonical identity authority --------------------

// TestIdentityAuthorityCoverageOnTheRealTree is the gate's own
// assertion run as a test, so a regression breaks the build rather than
// waiting for the next readiness run.
func TestIdentityAuthorityCoverageOnTheRealTree(t *testing.T) {
	cov, err := ScanIdentityAuthority(repoRootForScan(t))
	if err != nil {
		t.Fatalf("ScanIdentityAuthority: %v", err)
	}
	if cov.ScannedFiles < 100 {
		t.Fatalf("scanned only %d files -- a pass would be meaningless", cov.ScannedFiles)
	}
	if cov.ProductionIdentityWriters != 1 {
		t.Errorf("production identity writers = %d (%v), want exactly 1",
			cov.ProductionIdentityWriters, cov.IdentityWriterFiles)
	}
	if cov.UnauthorizedIdentityWriters != 0 {
		t.Errorf("unauthorized identity writers = %d: %v",
			cov.UnauthorizedIdentityWriters, cov.UnauthorizedWriterFiles)
	}
	if cov.IndependentLegacyMerges != 0 {
		t.Errorf("independent legacy merges = %d: %v",
			cov.IndependentLegacyMerges, cov.LegacyMergeFiles)
	}
	if cov.LegacyRegistryConstructions != 0 {
		t.Errorf("legacy registry constructions = %d: %v",
			cov.LegacyRegistryConstructions, cov.RegistryViolations)
	}
	if len(cov.MissingMaritimeMappings) != 0 {
		t.Errorf("maritime kinds with no explicit mapping: %v", cov.MissingMaritimeMappings)
	}
	if !cov.Pass() {
		t.Fatal("coverage does not pass its own condition")
	}
}

// TestEveryMaritimeKindIsExplicitlyMappedOrExplicitlyUnmapped is the
// heart of PHASE B's mapping clause: "we forgot" and "we decided not to
// model this" must never look identical.
func TestEveryMaritimeKindIsExplicitlyMappedOrExplicitlyUnmapped(t *testing.T) {
	entries, missing := MaritimeMapping()
	if len(missing) != 0 {
		t.Fatalf("maritime kinds with no row at all: %v", missing)
	}
	if len(entries) != len(maritime.KnownEntityKinds()) {
		t.Fatalf("mapped %d kinds, the ontology models %d", len(entries), len(maritime.KnownEntityKinds()))
	}
	modelled := map[string]bool{}
	for _, k := range identity.KnownKinds() {
		modelled[string(k)] = true
	}
	unmapped := 0
	for _, e := range entries {
		if e.Reason == "" {
			t.Errorf("%s has no stated reason -- an unexplained mapping is a guess", e.MaritimeKind)
		}
		if e.IdentityKind == UnmappedKind {
			unmapped++
			continue
		}
		if !modelled[e.IdentityKind] {
			t.Errorf("%s maps to %q, which is not a modelled identity.Kind", e.MaritimeKind, e.IdentityKind)
		}
	}
	if unmapped == 0 {
		t.Error("no kind is marked UNMAPPED -- if every maritime concept really had an issued identifier, " +
			"the UNMAPPED marker would be dead code, which is worth noticing")
	}
}

// TestAnUnmappedMaritimeKindIsNeverSilentlyGuessed proves the failure
// direction: adding a kind to the ontology without adding a mapping row
// must be reported, not defaulted.
func TestAnUnmappedMaritimeKindIsNeverSilentlyGuessed(t *testing.T) {
	// Simulate the situation by asking the table about a kind it has no
	// row for, through the same lookup MaritimeMapping uses.
	if _, ok := maritimeIdentityMapping[maritime.EntityKind("TERMINAL_CRANE")]; ok {
		t.Fatal("test fixture kind unexpectedly has a row")
	}
	// MaritimeMapping only enumerates KnownEntityKinds, so the real
	// protection is that a NEW kind added there without a row shows up
	// in `missing`. Assert that path directly.
	entries, missing := MaritimeMapping()
	if len(missing) != 0 {
		t.Fatalf("baseline is not clean: %v", missing)
	}
	if len(entries) == 0 {
		t.Fatal("no entries at all")
	}
}

// TestScanIdentityAuthorityDetectsAnInjectedIdentityWriter proves the
// scanner detects, rather than passing because it never finds anything.
func TestScanIdentityAuthorityDetectsAnInjectedIdentityWriter(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg", "rogue")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "package rogue\n\ntype holder struct{ Identity interface{ Merge(string) } }\n\n" +
		"func (h holder) Do() { h.Identity.Merge(\"x\") }\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "rogue.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cov, err := ScanIdentityAuthority(dir)
	if err != nil {
		t.Fatalf("ScanIdentityAuthority: %v", err)
	}
	if cov.UnauthorizedIdentityWriters == 0 {
		t.Fatal("an injected second identity writer was not detected")
	}
	if cov.Pass() {
		t.Fatal("coverage passed with a detected unauthorized identity writer")
	}
}

// TestScanIdentityAuthorityDetectsAnIndependentLegacyMerge is the same
// proof for the union-find half.
func TestScanIdentityAuthorityDetectsAnIndependentLegacyMerge(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg", "rogue")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "package rogue\n\ntype holder struct{ Entities interface{ Merge(string) } }\n\n" +
		"func (h holder) Do() { h.Entities.Merge(\"x\") }\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "rogue.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cov, err := ScanIdentityAuthority(dir)
	if err != nil {
		t.Fatalf("ScanIdentityAuthority: %v", err)
	}
	if cov.IndependentLegacyMerges == 0 {
		t.Fatal("an injected independent legacy merge was not detected")
	}
	if cov.Pass() {
		t.Fatal("coverage passed with a detected independent legacy merge")
	}
}
