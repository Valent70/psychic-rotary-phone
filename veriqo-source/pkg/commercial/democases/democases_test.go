package democases

import (
	"archive/zip"
	"path/filepath"
	"testing"

	"veriqo/pkg/commercial/packageverify"
)

// runCaseAssertions is the shared shape every demo case must satisfy:
// a real Decision and Action, a Dossier whose package hash verifies,
// and a Machine Package (.zip) that the standalone, separately-built
// packageverify checks accept -- proving each demo case is a real,
// exportable, independently verifiable artifact, not just a Go value
// that happens to compile.
func runCaseAssertions(t *testing.T, c Case, wantEvidenceCount int) {
	t.Helper()

	if c.Decision.IsZero() {
		t.Fatal("expected a populated, non-zero Decision")
	}
	if c.Action.IsZero() {
		t.Fatal("expected a populated, non-zero ActionAuthorization")
	}

	dos, err := c.Dossier()
	if err != nil {
		t.Fatalf("Dossier: %v", err)
	}
	if dos.PackageHash == "" {
		t.Fatal("expected a non-empty dossier package hash")
	}
	if len(dos.EvidenceInventory) != wantEvidenceCount {
		t.Fatalf("expected %d evidence items in the inventory, got %d", wantEvidenceCount, len(dos.EvidenceInventory))
	}
	// dossier.New populates Case.CaseID from the Decision's own
	// FindingHash (see pkg/commercial/dossier/dossier.go) -- the
	// case identity as this Store's caller supplied it lives in
	// Case.Scope instead.
	if dos.Case.Scope != c.CaseID {
		t.Fatalf("expected dossier Case.Scope=%s, got %s", c.CaseID, dos.Case.Scope)
	}
	if dos.Case.CaseID == "" {
		t.Fatal("expected a non-empty Case.CaseID (the Decision's FindingHash)")
	}

	outPath := filepath.Join(t.TempDir(), c.CaseID+".zip")
	if _, err := c.WriteMachinePackage(outPath); err != nil {
		t.Fatalf("WriteMachinePackage: %v", err)
	}
	r, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("opening machine package: %v", err)
	}
	defer r.Close()

	results, err := packageverify.VerifyZip(&r.Reader)
	if err != nil {
		t.Fatalf("VerifyZip: %v", err)
	}
	if !packageverify.AllPassed(results) {
		t.Fatalf("expected the independent verifier to accept this real demo case's package (skips allowed), got: %+v", results)
	}
}

func TestBuildEBLTransferDisputeCase(t *testing.T) {
	c, err := BuildEBLTransferDisputeCase()
	if err != nil {
		t.Fatalf("BuildEBLTransferDisputeCase: %v", err)
	}
	if c.Decision.Outcome() != "APPROVED" {
		t.Fatalf("expected APPROVED outcome, got %s", c.Decision.Outcome())
	}
	runCaseAssertions(t, c, 2)
}

func TestBuildMaritimeIncidentCase(t *testing.T) {
	c, err := BuildMaritimeIncidentCase()
	if err != nil {
		t.Fatalf("BuildMaritimeIncidentCase: %v", err)
	}
	if c.Decision.Outcome() != "ESCALATED" {
		t.Fatalf("expected ESCALATED outcome (a genuine contradiction, not auto-resolved), got %s", c.Decision.Outcome())
	}
	runCaseAssertions(t, c, 2)
}

func TestBuildInsuranceClaimCase(t *testing.T) {
	c, err := BuildInsuranceClaimCase()
	if err != nil {
		t.Fatalf("BuildInsuranceClaimCase: %v", err)
	}
	if c.Decision.Outcome() != "APPROVED" {
		t.Fatalf("expected APPROVED outcome, got %s", c.Decision.Outcome())
	}
	runCaseAssertions(t, c, 2)
}

// TestAllThreeDemoCasesUseDistinctTenantsAndCaseIDs guards against a
// copy-paste regression where two demo cases silently collide on the
// same tenant/case identity and would then interfere with each other
// if ever run against a shared Store.
func TestAllThreeDemoCasesUseDistinctTenantsAndCaseIDs(t *testing.T) {
	cases := []Case{}
	for _, build := range []func() (Case, error){BuildEBLTransferDisputeCase, BuildMaritimeIncidentCase, BuildInsuranceClaimCase} {
		c, err := build()
		if err != nil {
			t.Fatalf("building demo case: %v", err)
		}
		cases = append(cases, c)
	}
	seenTenant := map[string]bool{}
	seenCase := map[string]bool{}
	for _, c := range cases {
		if seenTenant[c.TenantID] {
			t.Fatalf("tenant ID %s reused across demo cases", c.TenantID)
		}
		if seenCase[c.CaseID] {
			t.Fatalf("case ID %s reused across demo cases", c.CaseID)
		}
		seenTenant[c.TenantID] = true
		seenCase[c.CaseID] = true
	}
}
