package entityconsistency

// PHASE B (P0-2) — Canonical Identity Authority.
//
// ScanProductionAuthority (this package's existing scan) already proves
// no production file outside pkg/lifecycle constructs an independent
// pkg/moat/entity.Registry. That is the "no second REGISTRY" half.
//
// The program asks for something stricter and differently shaped:
//
//	Identifier -> CanonicalIdentityAuthority -> Canonical Entity ID
//	-> Identity Ledger Head -> all downstream systems
//
// with production identity WRITERS = 1, unauthorized writers = 0,
// independent legacy merges = 0, maritime identity mappings explicit,
// and unknown mappings marked UNMAPPED rather than silently guessed.
//
// Three of those five were unproven before this file:
//
//   - "writers = 1" was never counted. A registry constructed in one
//     place can still be WRITTEN from many, and a write is what
//     actually merges two identities.
//   - "independent legacy merges = 0" was never counted either.
//     pkg/lifecycle's own documented fallback path (an alias Kind
//     identity.Kind does not model) legitimately writes the legacy
//     union-find; anything ELSE doing so is an independent legacy
//     merge, and nothing counted them.
//   - "unknown mappings marked UNMAPPED" had no representation at all.
//     MaritimeMapping below gives it one: every maritime entity kind
//     is explicitly either mapped to an identity.Kind or explicitly
//     marked UNMAPPED, and a kind absent from the table is a failure
//     rather than an implicit UNMAPPED — because "we forgot" and "we
//     decided not to model this" must not look identical.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veriqo/pkg/identity"
	"veriqo/pkg/moat/domain/maritime"
)

// UnmappedKind is the explicit marker for a maritime entity kind that
// pkg/identity deliberately does not model. It is a real value in the
// table, never an absence — see this file's own doc comment.
const UnmappedKind = "UNMAPPED"

// maritimeIdentityMapping is the explicit, exhaustive mapping from
// every maritime ontology entity kind to the pkg/identity Kind that
// carries its identity, or to UnmappedKind with a stated reason.
//
// Every entry was decided individually. An entry is UNMAPPED only when
// the maritime concept genuinely has no stable, externally-issued
// identifier of its own — not merely because nobody has got to it yet.
var maritimeIdentityMapping = map[maritime.EntityKind]struct {
	Kind   string
	Reason string
}{
	maritime.KindVessel: {string(identity.KindIMO),
		"a vessel's IMO number is issued once and never reused -- the highest-discriminating identifier this system models"},
	maritime.KindPort: {string(identity.KindPort),
		"a port is identified by its UN/LOCODE-style shared location code, which pkg/identity models directly"},
	maritime.KindCommodity: {string(identity.KindCommodity),
		"a commodity is identified by its classification code, modelled directly"},
	maritime.KindBroker: {string(identity.KindOrganization),
		"a broker is a legal organization; its identity is the organization's, not a broker-specific scheme"},
	maritime.KindInsurer: {string(identity.KindOrganization),
		"an insurer is a legal organization, same reasoning as a broker; an LEI-bearing insurer resolves through KindLEI via the organization"},
	maritime.KindCountry: {string(identity.KindGeographicEntity),
		"a country is the broadest geographic entity kind, which pkg/identity models"},
	maritime.KindInsurance: {string(identity.KindRegistryID),
		"an insurance policy is identified by the policy number its issuing registry assigned"},
	maritime.KindVoyage: {UnmappedKind,
		"a voyage has no globally-issued identifier: it is a derived span between two port calls, identified " +
			"differently by every operator. Mapping it to KindShipment would assert an equivalence that is not true"},
	maritime.KindCargo: {UnmappedKind,
		"a cargo parcel's identity in practice IS its bill of lading, and that is modelled as KindBillOfLading on " +
			"the BoL itself. Giving the cargo node its own identity kind would create two identities for one thing"},
	maritime.KindSanctionEntry: {UnmappedKind,
		"a sanctions listing is a statement ABOUT an entity, not an entity with an identity of its own; its " +
			"identity is the listing authority's record ID, which belongs to that authority's registry, not here"},
	maritime.KindOwnership: {UnmappedKind,
		"an ownership record is a relationship between a vessel and an organization, both of which are already " +
			"identified; the relationship itself is not separately issued an identifier by anyone"},
}

// MaritimeMappingEntry is one row of the explicit mapping, in the
// machine-readable form the gate's evidence artifact carries.
type MaritimeMappingEntry struct {
	MaritimeKind string `json:"maritime_kind"`
	IdentityKind string `json:"identity_kind"` // an identity.Kind, or UNMAPPED
	Reason       string `json:"reason"`
}

// MaritimeMapping returns the explicit mapping, deterministically
// ordered, along with any maritime kind that has NO row at all.
// A missing row is the failure case the whole table exists to prevent.
func MaritimeMapping() (entries []MaritimeMappingEntry, missing []string) {
	known := map[string]bool{}
	for _, k := range identity.KnownKinds() {
		known[string(k)] = true
	}
	for _, k := range maritime.KnownEntityKinds() {
		row, ok := maritimeIdentityMapping[k]
		if !ok {
			missing = append(missing, string(k))
			continue
		}
		if row.Kind != UnmappedKind && !known[row.Kind] {
			missing = append(missing, fmt.Sprintf("%s -> %s (not a modelled identity.Kind)", k, row.Kind))
			continue
		}
		if strings.TrimSpace(row.Reason) == "" {
			missing = append(missing, fmt.Sprintf("%s (no stated reason)", k))
			continue
		}
		entries = append(entries, MaritimeMappingEntry{
			MaritimeKind: string(k), IdentityKind: row.Kind, Reason: row.Reason,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].MaritimeKind < entries[j].MaritimeKind })
	sort.Strings(missing)
	return entries, missing
}

// identityWriteMarkers are the calls that MUTATE canonical identity.
// Reads (Resolve, EntityIDAt, Candidates, History, Head) are absent
// deliberately: a read cannot fragment identity, and forbidding reads
// would make the canonical authority unusable.
var identityWriteMarkers = []string{
	".Identity.Merge(", ".Identity.Assert(", ".Identity.Correct(", ".Identity.Unmerge(",
}

// legacyMergeMarkers are the calls that write the pre-canonical
// union-find registry. Exactly one production file may contain them.
var legacyMergeMarkers = []string{
	".Entities.Merge(", ".Entities.Register(",
}

// identityWriterAllowed is the audited set of files permitted to WRITE
// canonical identity.
//
//   - pkg/lifecycle/lifecycle.go is the ONE production identity writer:
//     resolveCanonicalEntity merges the aliases that co-occur on a single
//     Intent, under a single named authority ("lifecycle"), against the
//     single shared *identity.Resolver veriqo/kernel constructs.
var identityWriterAllowed = map[string]bool{
	filepath.FromSlash("pkg/lifecycle/lifecycle.go"): true,
	// This file names the markers as string literals, the same
	// self-reference every scanner in this repository has to exempt.
	filepath.FromSlash("pkg/governance/entityconsistency/identityauthority.go"): true,
}

// legacyMergeAllowed is the audited set of files permitted to write the
// legacy union-find. Deliberately the SAME single file: the fallback is
// a documented branch of the canonical resolver's own code path, not a
// separate authority.
var legacyMergeAllowed = map[string]bool{
	filepath.FromSlash("pkg/lifecycle/lifecycle.go"):                            true,
	filepath.FromSlash("pkg/governance/entityconsistency/identityauthority.go"): true,
}

// IdentityAuthorityCoverage is the machine-readable evidence the
// canonical_identity_authority_coverage gate attaches. Each field maps
// directly onto one clause of the program's PASS condition.
type IdentityAuthorityCoverage struct {
	ScannedFiles int `json:"scanned_files"`
	// ProductionIdentityWriters must be exactly 1.
	ProductionIdentityWriters int      `json:"production_identity_writers"`
	IdentityWriterFiles       []string `json:"identity_writer_files"`
	// UnauthorizedIdentityWriters must be 0.
	UnauthorizedIdentityWriters int      `json:"unauthorized_identity_writers"`
	UnauthorizedWriterFiles     []string `json:"unauthorized_writer_files,omitempty"`
	// IndependentLegacyMerges must be 0: a legacy union-find write
	// outside the canonical resolver's own documented fallback branch.
	IndependentLegacyMerges int      `json:"independent_legacy_merges"`
	LegacyMergeFiles        []string `json:"legacy_merge_files,omitempty"`
	// LegacyRegistryConstructions is the existing ScanProductionAuthority
	// result, folded in rather than re-implemented.
	LegacyRegistryConstructions int      `json:"legacy_registry_constructions"`
	RegistryViolations          []string `json:"registry_violations,omitempty"`
	// MaritimeMappings is the explicit table; MissingMaritimeMappings
	// must be empty.
	MaritimeMappings        []MaritimeMappingEntry `json:"maritime_mappings"`
	MissingMaritimeMappings []string               `json:"missing_maritime_mappings,omitempty"`
	// UnmappedMaritimeKinds counts rows explicitly marked UNMAPPED --
	// reported, not failed: an explicit UNMAPPED is the correct answer
	// for a concept nobody issues an identifier for.
	UnmappedMaritimeKinds int `json:"unmapped_maritime_kinds"`
}

// Pass is the gate's condition, stated once and without thresholds.
func (c IdentityAuthorityCoverage) Pass() bool {
	return c.ProductionIdentityWriters == 1 &&
		c.UnauthorizedIdentityWriters == 0 &&
		c.IndependentLegacyMerges == 0 &&
		c.LegacyRegistryConstructions == 0 &&
		len(c.MissingMaritimeMappings) == 0
}

// ScanIdentityAuthority walks the real source tree and answers every
// clause of PHASE B's PASS condition at once.
func ScanIdentityAuthority(repoRoot string) (IdentityAuthorityCoverage, error) {
	cov := IdentityAuthorityCoverage{}

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
		raw, err := os.ReadFile(path) // #nosec G304 G122 -- path comes from this function's own filepath.Walk over repoRoot, the same trust boundary ScanProductionAuthority already relies on
		if err != nil {
			return err
		}
		cov.ScannedFiles++
		content := string(raw)
		slash := filepath.ToSlash(rel)

		if containsAnyRealCall(content, identityWriteMarkers) && !isScannerSelfReference(rel) {
			cov.IdentityWriterFiles = append(cov.IdentityWriterFiles, slash)
			if !identityWriterAllowed[rel] {
				cov.UnauthorizedWriterFiles = append(cov.UnauthorizedWriterFiles, slash)
			}
		}
		if containsAnyRealCall(content, legacyMergeMarkers) && !isScannerSelfReference(rel) {
			if !legacyMergeAllowed[rel] {
				cov.LegacyMergeFiles = append(cov.LegacyMergeFiles, slash)
			}
		}
		return nil
	})
	if err != nil {
		return IdentityAuthorityCoverage{}, fmt.Errorf("entityconsistency: walk %s: %w", repoRoot, err)
	}

	sort.Strings(cov.IdentityWriterFiles)
	sort.Strings(cov.UnauthorizedWriterFiles)
	sort.Strings(cov.LegacyMergeFiles)
	cov.ProductionIdentityWriters = len(cov.IdentityWriterFiles)
	cov.UnauthorizedIdentityWriters = len(cov.UnauthorizedWriterFiles)
	cov.IndependentLegacyMerges = len(cov.LegacyMergeFiles)

	// Fold in the EXISTING registry-construction scan rather than
	// re-implementing it.
	auth, err := ScanProductionAuthority(repoRoot)
	if err != nil {
		return IdentityAuthorityCoverage{}, err
	}
	cov.LegacyRegistryConstructions = len(auth.Violations)
	cov.RegistryViolations = auth.Violations

	cov.MaritimeMappings, cov.MissingMaritimeMappings = MaritimeMapping()
	for _, m := range cov.MaritimeMappings {
		if m.IdentityKind == UnmappedKind {
			cov.UnmappedMaritimeKinds++
		}
	}
	return cov, nil
}

// isScannerSelfReference excludes this file, which necessarily contains
// the literal markers it searches for.
func isScannerSelfReference(rel string) bool {
	return rel == filepath.FromSlash("pkg/governance/entityconsistency/identityauthority.go")
}

func containsAnyRealCall(raw string, markers []string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		for _, m := range markers {
			if strings.Contains(line, m) {
				return true
			}
		}
	}
	return false
}
