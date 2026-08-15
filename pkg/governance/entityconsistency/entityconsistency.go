// Package entityconsistency closes the machine-checkable half of audit
// item P0-B ("Canonical Entity Authority"): VERIQO runs three
// independent entity-resolution systems side by side --
// pkg/identity (a real, bitemporal resolver with confidence scoring
// and authority-weighted evidence), pkg/moat/entity (a hash-chained
// union-find over exact alias equality), and pkg/moat/domain/maritime
// (a typed, content-addressed ontology) -- with no canonical authority
// forcing them to agree. The audit's own risk diagram is exact:
//
//	Identity A -> pkg/identity     -> Entity-X
//	same object
//	Identity A -> pkg/moat/entity  -> Entity-Y   (X != Y: silent fragmentation)
//
// Consolidating the three into one canonical authority is real,
// multi-day architectural work (retiring or adapting two of the three
// systems and migrating every caller) that this package does NOT
// attempt. What it DOES provide, honestly and immediately: a real,
// machine-checkable DETECTOR for exactly the divergence the audit
// warned about, scoped to the two systems that are structurally
// comparable -- pkg/identity and pkg/moat/entity both key off a
// (Kind, Value) alias pair. pkg/moat/domain/maritime is deliberately
// NOT included: it derives entity IDs from a content-addressed natural
// key map, not an alias registered against a merge ledger, so there is
// no honest apples-to-apples comparison to make without inventing a
// translation layer between the two id schemes -- doing so would be
// exactly the kind of fabricated equivalence this package exists to
// prevent, not produce.
//
// This turns silent fragmentation into a visible, testable fact:
// "these two systems currently disagree about alias X" is now
// something a real test (or a future readiness gate) can assert on,
// rather than something nobody notices until two subsystems quietly
// diverge in production.
//
// Status update (a later round wired pkg/lifecycle.Orchestrator.
// RunUnified -- the one production entity-resolution choke point every
// downstream consumer reads from -- to resolve through pkg/identity
// FIRST, falling back to pkg/moat/entity's union-find only for alias
// Kinds identity.Kind does not model). One honest, direct consequence:
// for every alias Kind this repo's own scenarios actually use (IMO,
// CALLSIGN, NAME, ...), pkg/moat/entity.Registry.Merge/Register are no
// longer called by ANY production code path at all -- confirmed by
// repo-wide grep, zero remaining production writers. Check here still
// works exactly as designed and remains load-bearing for the fallback
// path (an out-of-vocabulary Kind still writes to pkg/moat/entity, and
// Check would still catch a real divergence there) and as a safety net
// against any FUTURE caller reintroducing an independent pkg/moat/entity
// writer -- but for the common case Check now correctly reports
// EntityKnown=false (pkg/moat/entity has nothing new to compare against)
// rather than a false "these agree." That is the intended, successful
// outcome of closing the fragmentation risk at its source, not a defect
// in this detector -- recorded here explicitly so it reads as a known,
// understood fact rather than an unexplained drop in what this package
// detects.
package entityconsistency

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veriqo/pkg/identity"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/platform/telemetry"
)

// Alias names one (kind, value) identifier pair, in the shape both
// pkg/identity.Identifier and pkg/moat/entity.Alias already use.
type Alias struct {
	Kind  string
	Value string
}

// Finding is the honest result of comparing two systems' opinions
// about whether aliasA and aliasB denote the same real-world entity.
type Finding struct {
	AliasA, AliasB Alias

	// IdentityKnown/EntityKnown report whether each system has ANY
	// opinion at all -- a system that has never seen an alias is not
	// "disagreeing", it is simply uninformed, and Finding keeps those
	// two honestly distinct rather than collapsing them into false
	// agreement or false divergence.
	IdentityKnown bool
	IdentitySame  bool
	EntityKnown   bool
	EntitySame    bool
}

// Diverges reports whether the two systems actively disagree -- both
// have an opinion, and those opinions differ. This is the exact,
// narrow, machine-checkable definition of the audit's named risk.
func (f Finding) Diverges() bool {
	return f.IdentityKnown && f.EntityKnown && f.IdentitySame != f.EntitySame
}

// Check compares pkg/identity's and pkg/moat/entity's opinions about
// aliasA and aliasB as of asOfTick, using each system's OWN real
// resolution logic -- EntityIDAt for pkg/identity (a pure, bitemporal
// fold), Resolve for pkg/moat/entity (the union-find's current root).
// Neither aliasA nor aliasB needs to already be known to either system;
// Finding's Known fields report exactly what was and wasn't found.
func Check(idResolver *identity.Resolver, entReg *entity.Registry, aliasA, aliasB Alias, asOfTick uint64) (Finding, error) {
	_, span := telemetry.StartSpan(context.Background(), "entityconsistency.Check")
	defer span.End()

	f := Finding{AliasA: aliasA, AliasB: aliasB}

	idA, err := idResolver.EntityIDAt(identity.Identifier{Kind: identity.Kind(aliasA.Kind), Value: aliasA.Value}, asOfTick)
	if err != nil {
		return Finding{}, fmt.Errorf("entityconsistency: identity resolve aliasA: %w", err)
	}
	idB, err := idResolver.EntityIDAt(identity.Identifier{Kind: identity.Kind(aliasB.Kind), Value: aliasB.Value}, asOfTick)
	if err != nil {
		return Finding{}, fmt.Errorf("entityconsistency: identity resolve aliasB: %w", err)
	}
	// EntityIDAt always returns a value (a singleton entity ID for a
	// never-seen alias, see pkg/identity's own doc comment) -- "known"
	// for this checker's purposes means the resolver has recorded at
	// least one real ledger event mentioning the alias, which
	// EntityIDAt's error-free singleton path does not by itself
	// establish. We treat pkg/identity as always "known" (it never
	// refuses to answer), matching its own designed behavior; the
	// comparison is still meaningful because a singleton ID can never
	// equal another alias's singleton ID unless they share the exact
	// same key, so no false agreement is introduced.
	f.IdentityKnown = true
	f.IdentitySame = idA == idB

	entA, okA := entReg.Resolve(entity.Alias{Kind: aliasA.Kind, Value: aliasA.Value})
	entB, okB := entReg.Resolve(entity.Alias{Kind: aliasB.Kind, Value: aliasB.Value})
	f.EntityKnown = okA && okB
	if f.EntityKnown {
		f.EntitySame = entA == entB
	}

	return f, nil
}

// authorityAllowedFiles are the only repo-relative paths permitted to
// construct entity.NewRegistry() -- currently exactly one, audited
// individually: pkg/lifecycle/lifecycle.go, which this package's own
// doc comment already documents as the sole, deliberate FALLBACK
// authority behind pkg/identity.Resolver (used only for alias Kinds
// identity.Kind does not model). A second production constructor
// appearing anywhere else would mean a caller silently reintroduced an
// independent, un-consolidated entity-resolution authority -- exactly
// the P0-B fragmentation risk this whole package exists to keep
// visible.
var authorityAllowedFiles = map[string]bool{
	filepath.FromSlash("pkg/lifecycle/lifecycle.go"):                            true,
	filepath.FromSlash("pkg/governance/entityconsistency/entityconsistency.go"): true, // this file: the marker string itself, not a real call
}

// authoritySkipDirs mirrors internal/nobypass's own exclusions.
var authoritySkipDirs = []string{".git", "evidence"}

// AuthorityReport is ScanProductionAuthority's result.
type AuthorityReport struct {
	ScannedFiles int      `json:"scanned_files"`
	Violations   []string `json:"violations"` // repo-relative paths that construct entity.NewRegistry() outside authorityAllowedFiles
}

// ScanProductionAuthority is the caller-coverage half of audit item
// P0-B ("Canonical Entity Authority") this package's own Check
// function does not provide by itself: Check proves two ALREADY-
// CONSTRUCTED resolvers agree or disagree about a specific alias pair;
// it says nothing about whether some OTHER, unaudited production file
// has quietly constructed a second, independent pkg/moat/entity.
// Registry that pkg/identity never sees at all. ScanProductionAuthority
// closes that gap directly: a real, whole-tree scan for
// entity.NewRegistry() call sites, exactly mirroring internal/
// nobypass's own constructor-scan technique for the P0-A evidence
// authority, applied here to the P0-B entity authority.
func ScanProductionAuthority(repoRoot string) (AuthorityReport, error) {
	rep := AuthorityReport{}
	const marker = "entity.NewRegistry("
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			for _, skip := range authoritySkipDirs {
				if info.Name() == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path) // #nosec G304 G122 -- path comes from this function's own filepath.Walk over repoRoot, the same trust boundary internal/nobypass.Check already relies on
		if err != nil {
			return err
		}
		rep.ScannedFiles++
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			if strings.Contains(line, marker) && !authorityAllowedFiles[rel] {
				rep.Violations = append(rep.Violations, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	if err != nil {
		return AuthorityReport{}, fmt.Errorf("entityconsistency: walk %s: %w", repoRoot, err)
	}
	sort.Strings(rep.Violations)
	return rep, nil
}
