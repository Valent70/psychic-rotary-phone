package supplychain

import (
	"context"
	"testing"
)

func TestOfflineKEVFixtureQueryReturnsKnownEntries(t *testing.T) {
	fixture := NewOfflineKEVFixture(map[string]KEVEntry{
		"CVE-2021-44228": {CVEID: "CVE-2021-44228", DateAdded: "2021-12-10", DueDate: "2021-12-24", KnownRansomwareUse: true},
	})
	if fixture.Mode() != "SIMULATED" || fixture.SourceType() != "TEST_FIXTURE" || fixture.Kind() != SourceKindKEV {
		t.Fatalf("unexpected provenance: mode=%s source=%s kind=%s", fixture.Mode(), fixture.SourceType(), fixture.Kind())
	}

	found, err := fixture.Query(context.Background(), []string{"CVE-2021-44228", "CVE-9999-0000"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found) != 1 || found[0].CVEID != "CVE-2021-44228" {
		t.Fatalf("expected exactly the known CVE, got %+v", found)
	}
	if !found[0].KnownRansomwareUse {
		t.Fatal("expected KnownRansomwareUse to survive the fixture round trip")
	}
}

func TestOfflineKEVFixtureQueryEmptyForUnknownCVEs(t *testing.T) {
	fixture := NewOfflineKEVFixture(nil)
	found, err := fixture.Query(context.Background(), []string{"CVE-0000-0000"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected no matches against an empty KEV fixture, got %+v", found)
	}
}

func TestOfflineVEXFixtureQueryReturnsKnownStatements(t *testing.T) {
	fixture := NewOfflineVEXFixture(map[string]VEXStatement{
		vexKey("veriqo-kernel", "GO-2099-0001"): {
			Product: "veriqo-kernel", VulnerabilityID: "GO-2099-0001",
			Status: VEXStatusNotAffected, Justification: "vulnerable code path is compiled out in this build",
		},
	})
	if fixture.Mode() != "SIMULATED" || fixture.SourceType() != "TEST_FIXTURE" || fixture.Kind() != SourceKindVEX {
		t.Fatalf("unexpected provenance: mode=%s source=%s kind=%s", fixture.Mode(), fixture.SourceType(), fixture.Kind())
	}

	found, err := fixture.Query(context.Background(), "veriqo-kernel", []string{"GO-2099-0001", "GO-2099-9999"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found) != 1 || found[0].Status != VEXStatusNotAffected {
		t.Fatalf("expected exactly the known statement, got %+v", found)
	}

	// A statement scoped to a different product must not match, even
	// for the same vulnerability ID -- VEX is product-specific.
	found, err = fixture.Query(context.Background(), "unrelated-product", []string{"GO-2099-0001"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected no match for a different product, got %+v", found)
	}
}

func TestApplyVEXJustifications_NotAffectedSuppressesFailingFinding(t *testing.T) {
	c := goodContract()
	deps := []Dependency{{Module: "example.com/vulnerable", Version: "v1.0.0"}}
	provider := NewOfflineFixtureProvider(map[string][]Vulnerability{
		"example.com/vulnerable@v1.0.0": {{ID: "GO-2099-0001", Package: "example.com/vulnerable", Severity: SeverityHigh}},
	})
	policy := Policy{FailOn: []Severity{SeverityCritical, SeverityHigh}}

	vexed := ApplyVEXJustifications(policy, []VEXStatement{
		{Product: "veriqo-kernel", VulnerabilityID: "GO-2099-0001", Status: VEXStatusNotAffected, Justification: "unreachable code path"},
	})
	if _, ok := vexed.Justifications["GO-2099-0001"]; !ok {
		t.Fatal("expected a not_affected VEX statement to add a Justifications entry")
	}
	// The original policy must not be mutated.
	if policy.Justifications != nil {
		t.Fatalf("expected the original policy's Justifications to remain nil, got %v", policy.Justifications)
	}

	result, err := RunQualification(context.Background(), c, deps, provider, vexed)
	if err != nil {
		t.Fatalf("RunQualification: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected a VEX not_affected finding to pass, got failure: %s", result.FailureReason)
	}
}

func TestApplyVEXJustifications_FixedSuppressesFailingFinding(t *testing.T) {
	policy := Policy{FailOn: []Severity{SeverityCritical}}
	vexed := ApplyVEXJustifications(policy, []VEXStatement{
		{Product: "p", VulnerabilityID: "GO-2099-0002", Status: VEXStatusFixed},
	})
	if reason := vexed.Justifications["GO-2099-0002"]; reason == "" {
		t.Fatal("expected a fixed VEX statement (with no explicit Justification text) to still add a synthesized reason")
	}
}

func TestApplyVEXJustifications_AffectedDoesNotSuppress(t *testing.T) {
	c := goodContract()
	deps := []Dependency{{Module: "example.com/vulnerable", Version: "v1.0.0"}}
	provider := NewOfflineFixtureProvider(map[string][]Vulnerability{
		"example.com/vulnerable@v1.0.0": {{ID: "GO-2099-0003", Package: "example.com/vulnerable", Severity: SeverityCritical}},
	})
	policy := Policy{FailOn: []Severity{SeverityCritical}}

	vexed := ApplyVEXJustifications(policy, []VEXStatement{
		{Product: "veriqo-kernel", VulnerabilityID: "GO-2099-0003", Status: VEXStatusAffected, Justification: "confirmed exploitable"},
	})
	if _, ok := vexed.Justifications["GO-2099-0003"]; ok {
		t.Fatal("expected an 'affected' VEX statement NOT to add a Justifications entry")
	}

	result, err := RunQualification(context.Background(), c, deps, provider, vexed)
	if err == nil {
		t.Fatal("expected RunQualification to still fail for an 'affected' VEX status")
	}
	if result.Pass {
		t.Fatal("expected Pass=false: an 'affected' VEX statement must not grant an exception")
	}
}

func TestApplyVEXJustifications_UnderInvestigationDoesNotSuppress(t *testing.T) {
	policy := Policy{FailOn: []Severity{SeverityHigh}}
	vexed := ApplyVEXJustifications(policy, []VEXStatement{
		{Product: "p", VulnerabilityID: "GO-2099-0004", Status: VEXStatusUnderInvestigation},
	})
	if _, ok := vexed.Justifications["GO-2099-0004"]; ok {
		t.Fatal("expected 'under_investigation' NOT to add a Justifications entry")
	}
}

func TestBuildIntelligenceReport_CombinesAllFourSources(t *testing.T) {
	deps := []Dependency{
		{Module: "example.com/kev-and-critical", Version: "v1.0.0"},
		{Module: "example.com/vexed-clean", Version: "v1.0.0"},
	}
	vulnProvider := NewOfflineFixtureProvider(map[string][]Vulnerability{
		"example.com/kev-and-critical@v1.0.0": {
			{ID: "CVE-2021-44228", Package: "example.com/kev-and-critical", Severity: SeverityCritical},
		},
		"example.com/vexed-clean@v1.0.0": {
			{ID: "GO-2099-0005", Package: "example.com/vexed-clean", Severity: SeverityHigh},
		},
	})
	kevProvider := NewOfflineKEVFixture(map[string]KEVEntry{
		"CVE-2021-44228": {CVEID: "CVE-2021-44228", KnownRansomwareUse: true},
	})
	vexProvider := NewOfflineVEXFixture(map[string]VEXStatement{
		vexKey("veriqo-kernel", "GO-2099-0005"): {
			Product: "veriqo-kernel", VulnerabilityID: "GO-2099-0005",
			Status: VEXStatusNotAffected, Justification: "not reachable in this deployment",
		},
	})
	policy := Policy{FailOn: []Severity{SeverityCritical, SeverityHigh}}

	report, err := BuildIntelligenceReport(context.Background(), "veriqo-kernel", deps, vulnProvider, kevProvider, vexProvider, policy, 0, true)
	if err != nil {
		t.Fatalf("BuildIntelligenceReport: %v", err)
	}

	if report.SASTFindingCount != 0 || !report.SASTPass {
		t.Fatalf("expected the caller-supplied SAST result to pass through unchanged, got %+v", report)
	}
	if report.VulnerabilityReport.DependencyCount != 2 {
		t.Fatalf("expected dependency_count=2, got %d", report.VulnerabilityReport.DependencyCount)
	}
	if len(report.VulnerabilityReport.Findings) != 2 {
		t.Fatalf("expected both findings surfaced, got %+v", report.VulnerabilityReport.Findings)
	}
	if report.KEVMatchedCount != 1 || report.KEVMatchedIDs[0] != "CVE-2021-44228" {
		t.Fatalf("expected exactly the KEV-listed CVE matched, got count=%d ids=%v", report.KEVMatchedCount, report.KEVMatchedIDs)
	}
	if report.VEXExceptionCount != 1 || report.VEXExceptionIDs[0] != "GO-2099-0005" {
		t.Fatalf("expected exactly the VEX not_affected finding recorded as an exception, got count=%d ids=%v", report.VEXExceptionCount, report.VEXExceptionIDs)
	}
	// GO-2099-0005 was VEX-suppressed, but CVE-2021-44228 (a real KEV
	// hit) was never VEXed and remains an unjustified CRITICAL finding
	// -- so the overall vulnerability half still fails, even though a
	// KEV match by itself does not directly gate Pass (see
	// IntelligenceReport.Pass's doc comment).
	if report.VulnerabilityReport.Pass {
		t.Fatal("expected the vulnerability report to still fail on the un-VEXed KEV-listed CRITICAL finding")
	}
	if report.Pass {
		t.Fatal("expected overall Pass=false since the vulnerability half failed despite SAST passing")
	}
}

func TestBuildIntelligenceReport_NilKEVAndVEXProvidersAreOptional(t *testing.T) {
	deps := []Dependency{{Module: "example.com/clean", Version: "v1.0.0"}}
	vulnProvider := NewOfflineFixtureProvider(nil)
	policy := Policy{FailOn: []Severity{SeverityCritical}}

	report, err := BuildIntelligenceReport(context.Background(), "veriqo-kernel", deps, vulnProvider, nil, nil, policy, 3, true)
	if err != nil {
		t.Fatalf("BuildIntelligenceReport with nil KEV/VEX providers: %v", err)
	}
	if !report.Pass || report.KEVMatchedCount != 0 || report.VEXExceptionCount != 0 {
		t.Fatalf("expected a clean pass with no KEV/VEX activity, got %+v", report)
	}
	if report.SASTFindingCount != 3 {
		t.Fatalf("expected SASTFindingCount to pass through, got %d", report.SASTFindingCount)
	}
}

func TestBuildIntelligenceReport_SASTFailureFailsOverallEvenIfVulnPasses(t *testing.T) {
	deps := []Dependency{{Module: "example.com/clean", Version: "v1.0.0"}}
	vulnProvider := NewOfflineFixtureProvider(nil)
	policy := Policy{FailOn: []Severity{SeverityCritical}}

	report, err := BuildIntelligenceReport(context.Background(), "veriqo-kernel", deps, vulnProvider, nil, nil, policy, 5, false)
	if err != nil {
		t.Fatalf("BuildIntelligenceReport: %v", err)
	}
	if !report.VulnerabilityReport.Pass {
		t.Fatal("expected the vulnerability half alone to pass (no findings)")
	}
	if report.Pass {
		t.Fatal("expected overall Pass=false when SAST failed, even though the vulnerability half passed -- the two categories are not conflated")
	}
}
