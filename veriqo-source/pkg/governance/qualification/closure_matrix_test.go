package qualification

import (
	"errors"
	"testing"
)

func fullyClosedEntry() ClosureMatrixEntry {
	return ClosureMatrixEntry{
		GateID:     "example_gate",
		CodeStatus: DimCodeComplete, DataStatus: DimCodeComplete, ContractStatus: DimCodeComplete,
		ExecutionStatus: DimCodeComplete, ExternalReviewStatus: DimCodeComplete, EvidenceStatus: DimVerified,
	}
}

func TestComputeVerifiedOnlyWhenEveryDimensionIsClosing(t *testing.T) {
	e, err := Compute(fullyClosedEntry())
	if err != nil {
		t.Fatal(err)
	}
	if e.FinalStatus != DimVerified {
		t.Fatalf("expected VERIFIED, got %s (%s)", e.FinalStatus, e.FinalReason)
	}
}

func TestComputeCannotReachVerifiedWithAnyDimensionStillWaiting(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(e ClosureMatrixEntry) ClosureMatrixEntry
		wantDim DimensionStatus
	}{
		{"code_status not started", func(e ClosureMatrixEntry) ClosureMatrixEntry { e.CodeStatus = DimNotStarted; return e }, DimNotStarted},
		{"data_status waiting", func(e ClosureMatrixEntry) ClosureMatrixEntry { e.DataStatus = DimWaitingData; return e }, DimWaitingData},
		{"contract_status waiting", func(e ClosureMatrixEntry) ClosureMatrixEntry { e.ContractStatus = DimWaitingContract; return e }, DimWaitingContract},
		{"execution_status waiting cloud", func(e ClosureMatrixEntry) ClosureMatrixEntry { e.ExecutionStatus = DimWaitingCloudExecution; return e }, DimWaitingCloudExecution},
		{"external_review_status waiting", func(e ClosureMatrixEntry) ClosureMatrixEntry {
			e.ExternalReviewStatus = DimWaitingExternalReview
			return e
		}, DimWaitingExternalReview},
		{"evidence_status pending", func(e ClosureMatrixEntry) ClosureMatrixEntry { e.EvidenceStatus = DimEvidencePending; return e }, DimEvidencePending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, err := Compute(c.mutate(fullyClosedEntry()))
			if err != nil {
				t.Fatal(err)
			}
			if e.FinalStatus == DimVerified {
				t.Fatalf("VERIFIED must be unreachable while %s: got FinalStatus=%s", c.name, e.FinalStatus)
			}
			if e.FinalStatus != c.wantDim {
				t.Fatalf("expected FinalStatus=%s, got %s (%s)", c.wantDim, e.FinalStatus, e.FinalReason)
			}
		})
	}
}

// --- PART 15-style anti-false-green tests, at the closure-matrix level ---

func TestRealButUnauthorizedDataCannotBecomeQualifiedEvidence(t *testing.T) {
	// "AIS API connectivity cannot become live_data VERIFIED": code and
	// execution can both be real and done, but if the data itself is
	// still contractually unauthorized, the gate must not close.
	e := fullyClosedEntry()
	e.GateID = "live_data"
	e.DataStatus = DimWaitingContract // connectivity works; rights do not exist yet
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus == DimVerified || got.FinalStatus == DimQualified {
		t.Fatalf("real-but-unauthorized data must not close the gate, got %s", got.FinalStatus)
	}
}

func TestTenContainersCannotBecome100NodeVerified(t *testing.T) {
	e := fullyClosedEntry()
	e.GateID = "scale_qualification"
	e.ExecutionStatus = DimWaitingCloudExecution // 10-container drill done; 100-node run is not
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus == DimVerified {
		t.Fatal("a 10-container drill must never compute as 100-node VERIFIED")
	}
}

func TestSixtyMinuteSoakCannotBecome72HourVerified(t *testing.T) {
	e := fullyClosedEntry()
	e.GateID = "soak_72h"
	e.ExecutionStatus = DimWaitingCloudExecution
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus == DimVerified {
		t.Fatal("a 60-minute soak must never compute as 72-hour VERIFIED")
	}
}

func TestSVIDIssuanceAloneCannotBecomeSPIRETransportVerified(t *testing.T) {
	e := fullyClosedEntry()
	e.GateID = "spire_mtls"
	e.ExternalReviewStatus = DimWaitingExternalReview // production attestor not yet reviewed/qualified
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus == DimVerified {
		t.Fatal("SVID issuance alone must never compute as full SPIRE transport VERIFIED")
	}
}

func TestInternalSecurityTestsCannotBecomeIndependentPentestVerified(t *testing.T) {
	e := fullyClosedEntry()
	e.GateID = "pentest"
	e.ExternalReviewStatus = DimWaitingExternalReview // internal adversarial probes done; vendor report is not
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus == DimVerified {
		t.Fatal("internal adversarial probes must never compute as independent pentest VERIFIED")
	}
}

func TestRejectedOnAnyDimensionForcesFinalStatusRejected(t *testing.T) {
	e := fullyClosedEntry()
	e.EvidenceStatus = DimRejected
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus != DimRejected {
		t.Fatalf("expected REJECTED to propagate to FinalStatus, got %s", got.FinalStatus)
	}
}

func TestComputeFailsClosedOnUnknownDimensionValue(t *testing.T) {
	e := fullyClosedEntry()
	e.DataStatus = DimensionStatus("MADE_UP_STATUS")
	if _, err := Compute(e); !errors.Is(err, ErrUnknownDimensionStatus) {
		t.Fatalf("expected ErrUnknownDimensionStatus, got %v", err)
	}
}

func TestComputeIgnoresAnyCallerSuppliedFinalStatus(t *testing.T) {
	e := fullyClosedEntry()
	e.DataStatus = DimWaitingData
	e.FinalStatus = DimVerified // an attempt to smuggle a false-green verdict straight through
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalStatus == DimVerified {
		t.Fatal("Compute must never trust a caller-supplied FinalStatus; it must always recompute from the six real dimensions")
	}
}

func TestFinalReasonNamesTheBlockingDimensionDeterministically(t *testing.T) {
	e := fullyClosedEntry()
	e.DataStatus = DimWaitingData
	e.ContractStatus = DimWaitingContract // two blockers at once; data_status comes first in dimensionOrder
	got, err := Compute(e)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalReason != "blocked by data_status" {
		t.Fatalf("expected the first blocking dimension (data_status) to be named, got %q", got.FinalReason)
	}
}
