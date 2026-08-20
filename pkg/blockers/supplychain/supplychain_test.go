package supplychain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"veriqo/pkg/blockers"
)

func goodContract() *blockers.Contract {
	c := &blockers.Contract{
		ID: "supply_chain_scan", Name: "Supply-chain security scan", Version: "v1",
		RequiredCapabilities:      []string{"sast_pipeline", "vulnerability_db_client"},
		RealWorldDependencies:     []string{"network_access_to_vulnerability_databases"},
		TestProfile:               blockers.TestProfile{FixtureMode: true, SyntheticMode: true, ProductionRequiredForVerification: true},
		EvidenceRequirements:      []string{"vulnerability_db_query_log"},
		FailureConditions:         []string{"unjustified_high_or_critical_finding"},
		AcceptanceCriteria:        []string{"vulnerability_db_query_succeeded_and_clean"},
		RealQualificationRequired: true,
	}
	c.MarkImplemented()
	c.MarkIntegrationTested()
	return c
}

func TestDiscoverDependenciesRunsRealGoList(t *testing.T) {
	deps, err := DiscoverDependencies(context.Background(), "../../..")
	if err != nil {
		t.Fatalf("DiscoverDependencies: %v", err)
	}
	// This repo enforces zero external dependencies (see the
	// zero_dependency readiness gate), so a correct real `go list -m
	// all` run over it should report no dependency modules beyond the
	// main module itself, which DiscoverDependencies already excludes.
	if len(deps) != 0 {
		t.Fatalf("expected zero external dependencies for this repo, got %v", deps)
	}
}

func TestRunQualificationCleanPasses(t *testing.T) {
	c := goodContract()
	deps := []Dependency{{Module: "example.com/clean", Version: "v1.0.0"}}
	provider := NewOfflineFixtureProvider(nil)
	policy := Policy{FailOn: []Severity{SeverityCritical, SeverityHigh}}
	result, err := RunQualification(context.Background(), c, deps, provider, policy)
	if err != nil {
		t.Fatalf("RunQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass, got failure: %s", result.FailureReason)
	}
	if c.Status != blockers.StatusQualificationTested {
		t.Fatalf("expected QUALIFICATION_TESTED, got %s", c.Status)
	}
}

func TestRunQualificationUnjustifiedHighFails(t *testing.T) {
	c := goodContract()
	deps := []Dependency{{Module: "example.com/vulnerable", Version: "v1.0.0"}}
	provider := NewOfflineFixtureProvider(map[string][]Vulnerability{
		"example.com/vulnerable@v1.0.0": {{ID: "GO-2099-0001", Package: "example.com/vulnerable", Severity: SeverityHigh, FixedIn: "v1.0.1"}},
	})
	policy := Policy{FailOn: []Severity{SeverityCritical, SeverityHigh}}
	result, err := RunQualification(context.Background(), c, deps, provider, policy)
	if err == nil {
		t.Fatal("expected RunQualification to return an error for a failed fixture run")
	}
	if result.Pass {
		t.Fatal("expected Pass=false for an unjustified HIGH finding")
	}
}

func TestJustifiedFindingPasses(t *testing.T) {
	c := goodContract()
	deps := []Dependency{{Module: "example.com/vulnerable", Version: "v1.0.0"}}
	provider := NewOfflineFixtureProvider(map[string][]Vulnerability{
		"example.com/vulnerable@v1.0.0": {{ID: "GO-2099-0001", Package: "example.com/vulnerable", Severity: SeverityHigh, FixedIn: "v1.0.1"}},
	})
	policy := Policy{
		FailOn:         []Severity{SeverityCritical, SeverityHigh},
		Justifications: map[string]string{"GO-2099-0001": "vendored patch already applied, tracked in ticket VERIQO-123"},
	}
	result, err := RunQualification(context.Background(), c, deps, provider, policy)
	if err != nil {
		t.Fatalf("RunQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected a justified finding to pass, got failure: %s", result.FailureReason)
	}
}

func TestRunQualificationRejectsRealModeProvider(t *testing.T) {
	c := goodContract()
	provider := NewHTTPVulnerabilityProvider("https://vuln.go.dev", "GoVuln")
	_, err := RunQualification(context.Background(), c, nil, provider, Policy{})
	if err == nil {
		t.Fatal("RunQualification must refuse a REAL-mode provider")
	}
}

func TestHTTPVulnerabilityProviderQueriesRealHTTPAgainstFixtureServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		module := r.URL.Query().Get("module")
		resp := httpVulnResponse{Module: module}
		if module == "example.com/vulnerable" {
			resp.Vulnerabilities = []Vulnerability{{ID: "GO-2099-0002", Package: module, Severity: SeverityCritical}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	provider := NewHTTPVulnerabilityProvider(srv.URL, "TestFixtureHTTP")
	if provider.Mode() != "REAL" {
		t.Fatalf("expected Mode()==REAL for a genuine network-backed provider, got %s", provider.Mode())
	}
	found, err := provider.Query(context.Background(), []Dependency{
		{Module: "example.com/clean", Version: "v1.0.0"},
		{Module: "example.com/vulnerable", Version: "v2.0.0"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found) != 1 || found[0].ID != "GO-2099-0002" {
		t.Fatalf("expected exactly the vulnerable module's finding, got %+v", found)
	}
}

func TestHTTPVulnerabilityProviderPropagatesNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	provider := NewHTTPVulnerabilityProvider(srv.URL, "TestFixtureHTTP")
	_, err := provider.Query(context.Background(), []Dependency{{Module: "example.com/x", Version: "v1.0.0"}})
	if err == nil {
		t.Fatal("expected a 403 response to surface as an error, matching this sandbox's real vuln.go.dev/osv.dev behavior")
	}
}
