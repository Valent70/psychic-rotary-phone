// Package supplychain is the supply_chain_scan blocker's dependency-
// vulnerability half (SAST -- gosec and staticcheck -- was already
// closed for real in an earlier round; see
// evidence/supply_chain_scan-gosec-full.txt). It defines a
// VulnerabilityProvider abstraction, a real dependency-discovery step
// (`go list -m all`, the same command the zero_dependency gate already
// runs), a policy engine, and both a deterministic OfflineFixtureProvider
// and a genuine HTTP-backed provider. This sandbox's network policy
// blocks vuln.go.dev, osv.dev and the GitHub advisory API (confirmed
// with real requests in an earlier round -- all three returned 403), so
// HTTPVulnerabilityProvider is unit-tested against a local httptest
// server here rather than the real endpoints; pointing BaseURL at a
// real one is a configuration change, not a rewrite.
package supplychain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"veriqo/pkg/blockers"
)

// Severity mirrors the levels gosec and every vulnerability DB use.
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
)

// Dependency is one module in the build, as `go list -m all` reports.
type Dependency struct {
	Module  string
	Version string
}

// Vulnerability is one finding against a dependency.
type Vulnerability struct {
	ID       string
	Package  string
	Severity Severity
	FixedIn  string
}

// VulnerabilityProvider queries a vulnerability database for a set of
// dependencies. SourceType distinguishes a real external authority from
// a fixture, mirroring the ExternalEvidence provenance model in
// pkg/governance/qualification.
type VulnerabilityProvider interface {
	Mode() string // "SIMULATED" or "REAL"
	SourceType() string
	Query(ctx context.Context, deps []Dependency) ([]Vulnerability, error)
}

// DiscoverDependencies runs the real `go list -m all` command against
// dir and parses its output into Dependency records, skipping the
// first line (the main module, which has no version). This is the same
// command the zero_dependency readiness gate already runs.
func DiscoverDependencies(ctx context.Context, dir string) ([]Dependency, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "all")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("supplychain: go list -m all: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var deps []Dependency
	for i, line := range lines {
		if i == 0 || line == "" { // skip the main module itself
			continue
		}
		fields := strings.Fields(line)
		dep := Dependency{Module: fields[0]}
		if len(fields) > 1 {
			dep.Version = fields[1]
		}
		deps = append(deps, dep)
	}
	return deps, nil
}

// OfflineFixtureProvider returns a fixed, deterministic vulnerability
// set for known module@version pairs -- SourceType "TEST_FIXTURE",
// never claiming to be a live vulnerability database.
type OfflineFixtureProvider struct {
	Known map[string][]Vulnerability // keyed by "module@version"
}

// NewOfflineFixtureProvider builds a fixture seeded with known findings.
func NewOfflineFixtureProvider(known map[string][]Vulnerability) *OfflineFixtureProvider {
	return &OfflineFixtureProvider{Known: known}
}

func (p *OfflineFixtureProvider) Mode() string       { return "SIMULATED" }
func (p *OfflineFixtureProvider) SourceType() string { return "TEST_FIXTURE" }

func (p *OfflineFixtureProvider) Query(ctx context.Context, deps []Dependency) ([]Vulnerability, error) {
	var found []Vulnerability
	for _, d := range deps {
		key := d.Module + "@" + d.Version
		found = append(found, p.Known[key]...)
	}
	return found, nil
}

// httpVulnResponse is the JSON shape HTTPVulnerabilityProvider expects
// back per queried dependency.
type httpVulnResponse struct {
	Module          string          `json:"module"`
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
}

// HTTPVulnerabilityProvider queries a real HTTPS vulnerability database.
// Mode is always "REAL": this genuinely goes over the network to an
// external authority, which is exactly why RunQualification below
// refuses to run against it -- REAL-mode evidence belongs in
// pkg/governance/qualification, not this package's fixture pipeline.
type HTTPVulnerabilityProvider struct {
	Client     *http.Client
	BaseURL    string
	SourceName string
}

func NewHTTPVulnerabilityProvider(baseURL, sourceName string) *HTTPVulnerabilityProvider {
	return &HTTPVulnerabilityProvider{Client: &http.Client{Timeout: 10 * time.Second}, BaseURL: baseURL, SourceName: sourceName}
}

func (p *HTTPVulnerabilityProvider) Mode() string       { return "REAL" }
func (p *HTTPVulnerabilityProvider) SourceType() string { return p.SourceName }

func (p *HTTPVulnerabilityProvider) Query(ctx context.Context, deps []Dependency) ([]Vulnerability, error) {
	var all []Vulnerability
	for _, d := range deps {
		url := fmt.Sprintf("%s/v1/query?module=%s&version=%s", p.BaseURL, d.Module, d.Version)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("supplychain: build request for %s: %w", d.Module, err)
		}
		resp, err := p.Client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("supplychain: query %s for %s: %w", p.SourceName, d.Module, err)
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("supplychain: %s returned status %d for %s", p.SourceName, resp.StatusCode, d.Module)
				return
			}
			var parsed httpVulnResponse
			if decErr := json.NewDecoder(resp.Body).Decode(&parsed); decErr != nil {
				err = fmt.Errorf("supplychain: decode response for %s: %w", d.Module, decErr)
				return
			}
			all = append(all, parsed.Vulnerabilities...)
		}()
		if err != nil {
			return nil, err
		}
	}
	return all, nil
}

// Policy decides which findings block the release.
type Policy struct {
	// FailOn lists severities that fail the scan unless justified.
	FailOn []Severity
	// Justifications maps a vulnerability ID to a named reason it is
	// accepted despite matching FailOn -- the gosec-suppression pattern
	// (`// #nosec G<rule> -- reason`) applied to dependency findings.
	Justifications map[string]string
}

func (p Policy) failsOn(sev Severity) bool {
	for _, s := range p.FailOn {
		if s == sev {
			return true
		}
	}
	return false
}

// Report is the outcome of running the pipeline once.
type Report struct {
	DependencyCount int             `json:"dependency_count"`
	Findings        []Vulnerability `json:"findings"`
	Unjustified     []Vulnerability `json:"unjustified"`
	Pass            bool            `json:"pass"`
}

// Run discovers nothing itself -- deps is supplied by the caller (real
// DiscoverDependencies output, or a fixture set in tests) -- queries
// provider, and applies policy.
func Run(ctx context.Context, deps []Dependency, provider VulnerabilityProvider, policy Policy) (Report, error) {
	findings, err := provider.Query(ctx, deps)
	if err != nil {
		return Report{}, fmt.Errorf("supplychain: query: %w", err)
	}
	var unjustified []Vulnerability
	for _, v := range findings {
		if policy.failsOn(v.Severity) {
			if _, justified := policy.Justifications[v.ID]; !justified {
				unjustified = append(unjustified, v)
			}
		}
	}
	return Report{
		DependencyCount: len(deps), Findings: findings, Unjustified: unjustified,
		Pass: len(unjustified) == 0,
	}, nil
}

// RunQualification runs the pipeline against a fixture/simulated
// provider and records the outcome on contract. It refuses a REAL-mode
// provider -- that evidence belongs in pkg/governance/qualification.
func RunQualification(ctx context.Context, contract *blockers.Contract, deps []Dependency, provider VulnerabilityProvider, policy Policy) (blockers.RunResult, error) {
	if provider.Mode() == "REAL" {
		return blockers.RunResult{}, errors.New("supplychain: RunQualification refuses a REAL-mode provider; submit that evidence through pkg/governance/qualification instead")
	}

	report, err := Run(ctx, deps, provider, policy)
	if err != nil {
		return blockers.RunResult{}, err
	}

	result := blockers.RunResult{
		BlockerID: contract.ID, Mode: provider.Mode(), Pass: report.Pass,
		Measurements: map[string]string{
			"dependency_count": fmt.Sprintf("%d", report.DependencyCount),
			"findings":         fmt.Sprintf("%d", len(report.Findings)),
			"unjustified":      fmt.Sprintf("%d", len(report.Unjustified)),
			"source_type":      provider.SourceType(),
		},
	}
	if !report.Pass {
		ids := make([]string, len(report.Unjustified))
		for i, v := range report.Unjustified {
			ids[i] = v.ID
		}
		result.FailureReason = fmt.Sprintf("unjustified findings: %v", ids)
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}
