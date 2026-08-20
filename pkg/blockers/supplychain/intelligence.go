// This file is the second half of the supply_chain_scan blocker's
// audit point: "gosec + staticcheck tidak sama dengan vulnerability
// database qualification" -- SAST (a single build-time analysis of
// this repo's own source, already closed for real; see
// evidence/supply_chain_scan-gosec-full.txt) is a DIFFERENT category
// of evidence than vulnerability intelligence about the dependencies
// this repo consumes. supplychain.go already separates SAST from
// dependency-vulnerability-database scanning; this file further
// separates "a dependency has a known vulnerability" (VulnerabilityProvider)
// from "that vulnerability is being actively exploited in the wild"
// (the KEV catalog) and from "our specific product is not actually
// exploitable by it" (a VEX statement) -- three genuinely different
// claims a real security program tracks as three different pieces of
// evidence, not one undifferentiated "scan passed" bit.
package supplychain

import (
	"context"
	"fmt"
)

// SourceKind classifies WHAT KIND of security evidence a source
// produces. A caller assembling one audit bundle (see
// BuildIntelligenceReport) uses this to keep "the SAST scanners ran
// clean" separate from "the dependency tree has no known-exploited
// vulnerabilities" -- exactly the distinction this package's audit
// brief drew.
type SourceKind string

const (
	SourceKindSAST   SourceKind = "SAST"
	SourceKindVulnDB SourceKind = "VULNERABILITY_DATABASE"
	SourceKindKEV    SourceKind = "KEV"
	SourceKindVEX    SourceKind = "VEX"
)

// Kind reports the source kind for the two existing VulnerabilityProvider
// implementations. Both are vulnerability-database queries (a fixture
// standing in for one, or a genuine HTTP-backed one) -- neither is
// SAST, KEV, or VEX evidence.
func (p *OfflineFixtureProvider) Kind() SourceKind    { return SourceKindVulnDB }
func (p *HTTPVulnerabilityProvider) Kind() SourceKind { return SourceKindVulnDB }

// KEVEntry is one entry from CISA's Known Exploited Vulnerabilities
// catalog: a CVE confirmed to have been exploited in the wild, which
// CISA Binding Operational Directive 22-01 treats as materially more
// urgent than an ordinary vulnerability-database finding of the same
// severity -- the same reasoning KEVMatchedCount below exists for.
type KEVEntry struct {
	CVEID              string
	DateAdded          string
	DueDate            string
	KnownRansomwareUse bool
}

// KEVProvider queries the KEV catalog for a set of CVE IDs, following
// the same Mode()/SourceType() shape VulnerabilityProvider already
// uses so KEV evidence fits the same provenance model.
type KEVProvider interface {
	Mode() string // "SIMULATED" or "REAL"
	SourceType() string
	Kind() SourceKind
	Query(ctx context.Context, cveIDs []string) ([]KEVEntry, error)
}

// OfflineKEVFixture returns a fixed, deterministic KEV catalog subset
// for known CVE IDs -- SourceType "TEST_FIXTURE", never claiming to be
// a live sync of the real catalog at cisa.gov/known-exploited-vulnerabilities-catalog.
type OfflineKEVFixture struct {
	Known map[string]KEVEntry // keyed by CVE ID
}

// NewOfflineKEVFixture builds a fixture seeded with known KEV entries.
func NewOfflineKEVFixture(known map[string]KEVEntry) *OfflineKEVFixture {
	return &OfflineKEVFixture{Known: known}
}

func (p *OfflineKEVFixture) Mode() string       { return "SIMULATED" }
func (p *OfflineKEVFixture) SourceType() string { return "TEST_FIXTURE" }
func (p *OfflineKEVFixture) Kind() SourceKind   { return SourceKindKEV }

func (p *OfflineKEVFixture) Query(ctx context.Context, cveIDs []string) ([]KEVEntry, error) {
	var found []KEVEntry
	for _, id := range cveIDs {
		if e, ok := p.Known[id]; ok {
			found = append(found, e)
		}
	}
	return found, nil
}

// VEXStatus is one of the four machine-readable exploitability
// statuses in the real CSAF/OpenVEX vocabulary.
type VEXStatus string

const (
	// VEXStatusNotAffected asserts the product is not exploitable via
	// this vulnerability (e.g. the vulnerable code path is unreachable,
	// or a compensating control fully mitigates it).
	VEXStatusNotAffected VEXStatus = "not_affected"
	// VEXStatusAffected asserts the product genuinely is vulnerable.
	VEXStatusAffected VEXStatus = "affected"
	// VEXStatusFixed asserts a fix has already shipped for this product.
	VEXStatusFixed VEXStatus = "fixed"
	// VEXStatusUnderInvestigation asserts the status is not yet determined.
	VEXStatusUnderInvestigation VEXStatus = "under_investigation"
)

// VEXStatement is one Vulnerability Exploitability eXchange assertion
// about a specific product's exposure to a specific vulnerability.
type VEXStatement struct {
	Product         string
	VulnerabilityID string
	Status          VEXStatus
	Justification   string
}

// VEXProvider queries for VEX statements about a product's exposure to
// a set of vulnerability IDs, following the same Mode()/SourceType()
// shape VulnerabilityProvider and KEVProvider already use.
type VEXProvider interface {
	Mode() string // "SIMULATED" or "REAL"
	SourceType() string
	Kind() SourceKind
	Query(ctx context.Context, product string, vulnerabilityIDs []string) ([]VEXStatement, error)
}

// OfflineVEXFixture returns a fixed, deterministic set of VEX
// statements for known (product, vulnerability ID) pairs -- SourceType
// "TEST_FIXTURE", never claiming to be a live feed from a real VEX
// document repository.
type OfflineVEXFixture struct {
	Known map[string]VEXStatement // keyed by product + "|" + VulnerabilityID
}

// NewOfflineVEXFixture builds a fixture seeded with known VEX statements.
func NewOfflineVEXFixture(known map[string]VEXStatement) *OfflineVEXFixture {
	return &OfflineVEXFixture{Known: known}
}

func (p *OfflineVEXFixture) Mode() string       { return "SIMULATED" }
func (p *OfflineVEXFixture) SourceType() string { return "TEST_FIXTURE" }
func (p *OfflineVEXFixture) Kind() SourceKind   { return SourceKindVEX }

func vexKey(product, vulnID string) string { return product + "|" + vulnID }

func (p *OfflineVEXFixture) Query(ctx context.Context, product string, vulnerabilityIDs []string) ([]VEXStatement, error) {
	var found []VEXStatement
	for _, id := range vulnerabilityIDs {
		if s, ok := p.Known[vexKey(product, id)]; ok {
			found = append(found, s)
		}
	}
	return found, nil
}

// ApplyVEXJustifications folds every statement in statements whose
// Status is "not_affected" or "fixed" into policy.Justifications,
// keyed by VulnerabilityID -- reusing the exact exception mechanism
// Run already checks (the gosec-suppression-style Justifications map)
// rather than teaching Run a second, parallel justification path.
// Only these two statuses grant an exception: "affected" and
// "under_investigation" are left exactly as unjustified as they
// already were, since neither one asserts the product is actually
// safe from the vulnerability. Returns a new Policy; the one passed in
// is not mutated, and an existing Justifications entry for the same ID
// is overwritten by the VEX-derived one (VEX is expected to be the
// more current, machine-verifiable source for that ID).
func ApplyVEXJustifications(policy Policy, statements []VEXStatement) Policy {
	justifications := make(map[string]string, len(policy.Justifications)+len(statements))
	for k, v := range policy.Justifications {
		justifications[k] = v
	}
	for _, s := range statements {
		if s.Status != VEXStatusNotAffected && s.Status != VEXStatusFixed {
			continue
		}
		reason := s.Justification
		if reason == "" {
			reason = fmt.Sprintf("VEX status=%s for product=%s", s.Status, s.Product)
		}
		justifications[s.VulnerabilityID] = reason
	}
	policy.Justifications = justifications
	return policy
}

// IntelligenceReport combines SAST evidence (a caller-supplied count
// and pass/fail -- this package does not run gosec/staticcheck itself,
// see the package doc comment in supplychain.go) with this package's
// own vulnerability-database findings, KEV catalog matches, and
// VEX-justified exceptions into one evidence bundle, so a real CI
// script can report a single unified result without conflating SAST
// and vulnerability intelligence.
type IntelligenceReport struct {
	SASTFindingCount int  `json:"sast_finding_count"`
	SASTPass         bool `json:"sast_pass"`

	VulnerabilityReport Report `json:"vulnerability_report"`

	// KEVMatchedCount/KEVMatchedIDs: how many (and which) of
	// VulnerabilityReport.Findings' IDs also appear in the KEV catalog.
	// A KEV match means the vulnerability is confirmed exploited in the
	// wild, not merely theoretically exploitable -- materially more
	// urgent than an ordinary finding of the same severity.
	KEVMatchedCount int      `json:"kev_matched_count"`
	KEVMatchedIDs   []string `json:"kev_matched_ids,omitempty"`

	// VEXExceptionCount/VEXExceptionIDs: which finding IDs were folded
	// into the policy's Justifications by ApplyVEXJustifications before
	// Run evaluated pass/fail (i.e. a VEX "not_affected" or "fixed"
	// statement, not a hand-written Policy.Justifications entry).
	VEXExceptionCount int      `json:"vex_exception_count"`
	VEXExceptionIDs   []string `json:"vex_exception_ids,omitempty"`

	// Pass is the combined SAST-and-vulnerability-intelligence verdict:
	// both halves must pass. It deliberately does not weight KEV
	// matches into pass/fail here -- KEVMatchedCount/IDs is surfaced so
	// a caller's own policy can decide how urgently to react, the same
	// way Policy.FailOn is the caller's own choice for severities.
	Pass bool `json:"pass"`
}

// BuildIntelligenceReport is the single entry point a CI script uses
// to assemble one unified evidence bundle: it queries vulnProvider for
// vulnerability-database findings against deps, asks vexProvider (if
// non-nil) for VEX statements about product's exposure to those exact
// finding IDs and folds any "not_affected"/"fixed" statements into
// policy's Justifications BEFORE evaluating pass/fail (see
// ApplyVEXJustifications), evaluates the resulting policy against the
// findings using the same logic Run uses, checks which of those
// finding IDs also appear in the KEV catalog via kevProvider (if
// non-nil), and combines all of that with the caller-supplied SAST
// result.
//
// vulnProvider is queried exactly once (not once here and again inside
// Run) so a real network-backed provider is not charged for a second
// HTTP round trip just to compute the VEX exception list.
func BuildIntelligenceReport(
	ctx context.Context,
	product string,
	deps []Dependency,
	vulnProvider VulnerabilityProvider,
	kevProvider KEVProvider,
	vexProvider VEXProvider,
	policy Policy,
	sastFindingCount int,
	sastPass bool,
) (IntelligenceReport, error) {
	findings, err := vulnProvider.Query(ctx, deps)
	if err != nil {
		return IntelligenceReport{}, fmt.Errorf("supplychain: query vulnerability provider: %w", err)
	}

	effectivePolicy := policy
	var vexExceptionIDs []string
	if vexProvider != nil && len(findings) > 0 {
		ids := make([]string, len(findings))
		for i, f := range findings {
			ids[i] = f.ID
		}
		statements, err := vexProvider.Query(ctx, product, ids)
		if err != nil {
			return IntelligenceReport{}, fmt.Errorf("supplychain: query VEX provider: %w", err)
		}
		effectivePolicy = ApplyVEXJustifications(policy, statements)
		for _, s := range statements {
			if s.Status == VEXStatusNotAffected || s.Status == VEXStatusFixed {
				vexExceptionIDs = append(vexExceptionIDs, s.VulnerabilityID)
			}
		}
	}

	// Mirrors Run's own unjustified-finding logic exactly (same
	// package, so reusing Policy.failsOn directly) rather than calling
	// Run and re-querying vulnProvider a second time.
	var unjustified []Vulnerability
	for _, v := range findings {
		if effectivePolicy.failsOn(v.Severity) {
			if _, justified := effectivePolicy.Justifications[v.ID]; !justified {
				unjustified = append(unjustified, v)
			}
		}
	}
	report := Report{
		DependencyCount: len(deps), Findings: findings, Unjustified: unjustified,
		Pass: len(unjustified) == 0,
	}

	var kevMatchedIDs []string
	if kevProvider != nil && len(findings) > 0 {
		ids := make([]string, len(findings))
		for i, f := range findings {
			ids[i] = f.ID
		}
		entries, err := kevProvider.Query(ctx, ids)
		if err != nil {
			return IntelligenceReport{}, fmt.Errorf("supplychain: query KEV provider: %w", err)
		}
		for _, e := range entries {
			kevMatchedIDs = append(kevMatchedIDs, e.CVEID)
		}
	}

	return IntelligenceReport{
		SASTFindingCount:    sastFindingCount,
		SASTPass:            sastPass,
		VulnerabilityReport: report,
		KEVMatchedCount:     len(kevMatchedIDs),
		KEVMatchedIDs:       kevMatchedIDs,
		VEXExceptionCount:   len(vexExceptionIDs),
		VEXExceptionIDs:     vexExceptionIDs,
		Pass:                sastPass && report.Pass,
	}, nil
}
