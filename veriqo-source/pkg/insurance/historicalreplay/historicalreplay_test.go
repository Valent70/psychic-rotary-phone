package historicalreplay

import (
	"strings"
	"testing"

	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/insurance/casepack"
)

func driveGoldenForTest(t *testing.T) (*casepack.GoldenResult, bool) {
	t.Helper()
	live, coldReport, err := casepack.GoldenColdReplay()
	if err != nil {
		t.Fatalf("GoldenColdReplay: %v", err)
	}
	return live, coldReport.Pass()
}

func TestBuildRedactedCaseRefusesNilResult(t *testing.T) {
	if _, err := BuildRedactedCase(nil, true, PermissionFull); err != ErrNilGoldenResult {
		t.Fatalf("expected ErrNilGoldenResult, got %v", err)
	}
}

func TestBuildRedactedCaseRefusesUnknownLevel(t *testing.T) {
	gr, replayed := driveGoldenForTest(t)
	if _, err := BuildRedactedCase(gr, replayed, "NOT_A_LEVEL"); err == nil {
		t.Fatal("expected an unknown PermissionLevel to be refused")
	}
}

func TestBuildRedactedCaseAllStagesPresentAtEveryLevel(t *testing.T) {
	gr, replayed := driveGoldenForTest(t)
	if !replayed {
		t.Fatal("expected the underlying golden case's own cold replay to pass before testing redaction on top of it")
	}
	for _, level := range []PermissionLevel{PermissionFull, PermissionRedacted, PermissionSummaryOnly} {
		rc, err := BuildRedactedCase(gr, replayed, level)
		if err != nil {
			t.Fatalf("BuildRedactedCase(%s): %v", level, err)
		}
		if !rc.AllStagesPresent() {
			var missing []string
			for _, s := range rc.Stages {
				if !s.Present {
					missing = append(missing, string(s.Stage))
				}
			}
			t.Fatalf("expected all 12 named stages present at level %s, missing: %v", level, missing)
		}
		if len(rc.Stages) != 12 {
			t.Fatalf("expected exactly 12 stages (the reviewer's own named chain), got %d", len(rc.Stages))
		}
		if rc.OriginClass != provenance.OriginReplay {
			t.Fatalf("expected OriginClass to be OriginReplay, got %s", rc.OriginClass)
		}
	}
}

func TestFullLevelIncludesPartyPseudonymsAndExactAmount(t *testing.T) {
	gr, replayed := driveGoldenForTest(t)
	rc, err := BuildRedactedCase(gr, replayed, PermissionFull)
	if err != nil {
		t.Fatalf("BuildRedactedCase: %v", err)
	}
	if len(rc.PartyPseudonyms) == 0 {
		t.Fatal("expected PermissionFull to include party pseudonym mappings")
	}
	if rc.QuantumIndicativeClaimValue == nil {
		t.Fatal("expected PermissionFull to include the exact quantum figure")
	}
	if rc.PartyPseudonymCount != len(rc.PartyPseudonyms) {
		t.Fatalf("expected PartyPseudonymCount (%d) to match the map length (%d)", rc.PartyPseudonymCount, len(rc.PartyPseudonyms))
	}
}

func TestRedactedLevelHidesIdentitiesButKeepsAmount(t *testing.T) {
	gr, replayed := driveGoldenForTest(t)
	rc, err := BuildRedactedCase(gr, replayed, PermissionRedacted)
	if err != nil {
		t.Fatalf("BuildRedactedCase: %v", err)
	}
	if rc.PartyPseudonyms != nil {
		t.Fatal("expected PermissionRedacted to carry NO party pseudonym mapping at all")
	}
	if rc.PartyPseudonymCount == 0 {
		t.Fatal("expected PartyPseudonymCount to still be reported at REDACTED level")
	}
	if rc.QuantumIndicativeClaimValue == nil {
		t.Fatal("expected PermissionRedacted to still include the exact quantum figure")
	}
	for _, s := range rc.Stages {
		if s.Detail != "" {
			t.Fatalf("expected no stage Detail at PermissionRedacted, got %q for stage %s", s.Detail, s.Stage)
		}
	}
}

func TestSummaryOnlyLevelHidesIdentitiesAndExactAmount(t *testing.T) {
	gr, replayed := driveGoldenForTest(t)
	rc, err := BuildRedactedCase(gr, replayed, PermissionSummaryOnly)
	if err != nil {
		t.Fatalf("BuildRedactedCase: %v", err)
	}
	if rc.PartyPseudonyms != nil {
		t.Fatal("expected PermissionSummaryOnly to carry NO party pseudonym mapping")
	}
	if rc.QuantumIndicativeClaimValue != nil {
		t.Fatal("expected PermissionSummaryOnly to carry NO exact quantum figure")
	}
	if rc.QuantumMagnitudeBand == "" {
		t.Fatal("expected PermissionSummaryOnly to still report a magnitude band")
	}
	for _, s := range rc.Stages {
		if s.Detail != "" {
			t.Fatalf("expected no stage Detail at PermissionSummaryOnly, got %q for stage %s", s.Detail, s.Stage)
		}
		if strings.Contains(s.Detail, "PTY-") {
			t.Fatalf("expected no real party identifier to leak into stage detail at SUMMARY_ONLY: %q", s.Detail)
		}
	}
}

func TestPermissionLevelVocabularyIsClosed(t *testing.T) {
	for _, l := range []PermissionLevel{PermissionFull, PermissionRedacted, PermissionSummaryOnly} {
		if !IsKnownPermissionLevel(l) {
			t.Errorf("expected %q to be known", l)
		}
	}
	if IsKnownPermissionLevel("PRODUCTION_QUALIFIED") {
		t.Fatal("PermissionLevel must never accept an unrelated vocabulary")
	}
}

func TestReplayVerifiedCarriesThroughEveryLevel(t *testing.T) {
	gr, _ := driveGoldenForTest(t)
	rcTrue, err := BuildRedactedCase(gr, true, PermissionSummaryOnly)
	if err != nil {
		t.Fatalf("BuildRedactedCase: %v", err)
	}
	if !rcTrue.ReplayVerified {
		t.Fatal("expected ReplayVerified true to carry through")
	}
	rcFalse, err := BuildRedactedCase(gr, false, PermissionSummaryOnly)
	if err != nil {
		t.Fatalf("BuildRedactedCase: %v", err)
	}
	if rcFalse.ReplayVerified {
		t.Fatal("expected ReplayVerified false to carry through honestly, never silently upgraded to true")
	}
	for _, s := range rcFalse.Stages {
		if s.Stage == StageReplay && s.Present {
			t.Fatal("expected the REPLAY stage to report Present=false when replay was not actually verified")
		}
	}
}
