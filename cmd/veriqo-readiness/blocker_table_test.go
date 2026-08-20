package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veriqo/internal/assurance"
)

func TestBuildReadinessTableShapeAndBlockerGap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unit.txt"), []byte("gate: unit\nexit: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := assurance.NewRegistry()
	if err := reg.Register(assurance.Gate{ID: "unit", Description: "unit tests", Mandatory: true,
		RequiredStatus: assurance.StatusVerified, OwnerPackage: "./...", ExitCriteria: "go test ./... passes"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Attach("unit", assurance.StatusVerified, assurance.NewEvidence("unit", "go test ./...", "ok", 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(assurance.Gate{ID: "pentest", Description: "independent penetration test", Mandatory: true,
		RequiredStatus: assurance.StatusQualified, OwnerPackage: "external", ExitCriteria: "signed report from an external security vendor"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Block("pentest", "requires an independent external vendor"); err != nil {
		t.Fatal(err)
	}

	blockers := map[string]productionBlocker{
		"pentest": {ID: "pentest", Name: "Independent penetration test",
			EvidenceRequirements: []string{"scope_document", "vendor_signed_report"}},
	}

	rows := buildReadinessTable(reg, dir, blockers)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	var unitRow, pentestRow *ReadinessRow
	for i := range rows {
		switch rows[i].GateID {
		case "unit":
			unitRow = &rows[i]
		case "pentest":
			pentestRow = &rows[i]
		}
	}
	if unitRow == nil || pentestRow == nil {
		t.Fatalf("missing expected rows: %+v", rows)
	}

	if unitRow.EvidencePresent != "unit.txt" {
		t.Errorf("unit EvidencePresent = %q, want unit.txt", unitRow.EvidencePresent)
	}
	if unitRow.RemainingGap != "none" {
		t.Errorf("unit RemainingGap = %q, want none (satisfied gate)", unitRow.RemainingGap)
	}
	if unitRow.VerificationResult != "VERIFIED (SATISFIED)" {
		t.Errorf("unit VerificationResult = %q, want VERIFIED (SATISFIED)", unitRow.VerificationResult)
	}

	if pentestRow.EvidenceRequired != "scope_document; vendor_signed_report" {
		t.Errorf("pentest EvidenceRequired = %q, want the blocker's real evidence_requirements joined", pentestRow.EvidenceRequired)
	}
	if pentestRow.RemainingGap != "requires an independent external vendor" {
		t.Errorf("pentest RemainingGap = %q, want the real blocker reason", pentestRow.RemainingGap)
	}
	if pentestRow.EvidencePresent != "NONE" {
		t.Errorf("pentest EvidencePresent = %q, want NONE (no file written for it)", pentestRow.EvidencePresent)
	}

	md := renderReadinessTableMarkdown(rows)
	for _, want := range []string{"STATUS", "ACCEPTANCE CRITERION", "EVIDENCE REQUIRED", "EVIDENCE PRESENT", "VERIFICATION RESULT", "REMAINING GAP"} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered table missing required column header %q", want)
		}
	}
}

func TestBuildBlockerEvidenceMapFindsRealArtifactsAndSharesCrossBlockerOnes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"multi_region_dr-multi-container-drill.txt",
		"blockers-qualification-report.json",
		"external-qualification.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	reg := assurance.NewRegistry()
	if err := reg.Register(assurance.Gate{ID: "multi_region_dr", Description: "dr", Mandatory: true,
		RequiredStatus: assurance.StatusQualified, ExitCriteria: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Block("multi_region_dr", "requires real multi-region infra"); err != nil {
		t.Fatal(err)
	}

	blockers := map[string]productionBlocker{
		"multi_region_dr": {ID: "multi_region_dr", Name: "Multi-region DR"},
		"hsm_kms":         {ID: "hsm_kms", Name: "HSM/KMS"},
	}

	bem := buildBlockerEvidenceMap(reg, dir, blockers)
	dr, ok := bem.Blockers["multi_region_dr"]
	if !ok {
		t.Fatal("missing multi_region_dr entry")
	}
	if len(dr.EvidenceArtifacts) != 1 || dr.EvidenceArtifacts[0] != "multi_region_dr-multi-container-drill.txt" {
		t.Errorf("multi_region_dr EvidenceArtifacts = %v", dr.EvidenceArtifacts)
	}
	if len(dr.SharedCrossBlocker) != 2 {
		t.Errorf("expected 2 shared cross-blocker artifacts, got %v", dr.SharedCrossBlocker)
	}

	hsm, ok := bem.Blockers["hsm_kms"]
	if !ok {
		t.Fatal("missing hsm_kms entry")
	}
	if len(hsm.EvidenceArtifacts) != 0 {
		t.Errorf("hsm_kms should have zero blocker-specific artifacts in this fixture, got %v", hsm.EvidenceArtifacts)
	}
	if len(hsm.SharedCrossBlocker) != 2 {
		t.Errorf("hsm_kms should still share the 2 cross-blocker artifacts, got %v", hsm.SharedCrossBlocker)
	}
	// hsm_kms was never registered in reg, proving the map still reports
	// an honest UNKNOWN/not-satisfied entry rather than silently omitting
	// a blocker the registry doesn't (yet) know about.
	if hsm.GateStatus != "UNKNOWN" || hsm.Satisfied {
		t.Errorf("hsm_kms unregistered gate should report UNKNOWN/not satisfied, got %+v", hsm)
	}
}
