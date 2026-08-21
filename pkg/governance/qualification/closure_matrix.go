package qualification

import "fmt"

// ClosureMatrixEntry closes PART 14 of the "Final Audit result" audit:
// a single Status (see Status above) answers "how far along is this
// gate", but not WHY it is not further along -- code done but no
// contract, or a contract in place but no cloud execution slot, read
// identically as "still BLOCKED_EXTERNAL". This file adds the six
// independent dimensions the mandate names, plus a FinalStatus that is
// COMPUTED, never independently settable, from the other six -- the
// mechanical answer to the mandate's own "NON-NEGOTIABLE RULE": never
// convert "technically possible" into "verified".
type DimensionStatus string

const (
	DimNotStarted            DimensionStatus = "NOT_STARTED"
	DimInProgress            DimensionStatus = "IN_PROGRESS"
	DimCodeComplete          DimensionStatus = "CODE_COMPLETE"
	DimWaitingData           DimensionStatus = "WAITING_DATA"
	DimWaitingContract       DimensionStatus = "WAITING_CONTRACT"
	DimWaitingCloudExecution DimensionStatus = "WAITING_CLOUD_EXECUTION"
	DimWaitingExternalReview DimensionStatus = "WAITING_EXTERNAL_REVIEW"
	DimEvidencePending       DimensionStatus = "EVIDENCE_PENDING"
	DimQualified             DimensionStatus = "QUALIFIED"
	DimVerified              DimensionStatus = "VERIFIED"
	DimRejected              DimensionStatus = "REJECTED"
)

var knownDimensionStatuses = map[DimensionStatus]bool{
	DimNotStarted: true, DimInProgress: true, DimCodeComplete: true,
	DimWaitingData: true, DimWaitingContract: true, DimWaitingCloudExecution: true,
	DimWaitingExternalReview: true, DimEvidencePending: true,
	DimQualified: true, DimVerified: true, DimRejected: true,
}

// closingValues are the only DimensionStatus values that do NOT block
// FinalStatus from advancing past this dimension -- everything else
// (including the zero value) is treated as blocking, fail-closed.
var closingValues = map[DimensionStatus]bool{
	DimCodeComplete: true, DimQualified: true, DimVerified: true,
}

// ClosureMatrixEntry is one gate's full six-dimension breakdown, plus
// the FinalStatus Compute derives from it.
type ClosureMatrixEntry struct {
	GateID string `json:"gate_id"`

	CodeStatus           DimensionStatus `json:"code_status"`
	DataStatus           DimensionStatus `json:"data_status"`
	ContractStatus       DimensionStatus `json:"contract_status"`
	ExecutionStatus      DimensionStatus `json:"execution_status"`
	ExternalReviewStatus DimensionStatus `json:"external_review_status"`
	EvidenceStatus       DimensionStatus `json:"evidence_status"`

	FinalStatus DimensionStatus `json:"final_status"`
	// FinalReason names WHICH dimension determined FinalStatus, so a
	// reader never has to guess -- e.g. "blocked by data_status" or
	// "closed: evidence_status".
	FinalReason string `json:"final_reason"`
}

// dimensionOrder is the fixed, deterministic order Compute checks
// dimensions in -- the FIRST blocking one found is reported as the
// reason, so two runs over the same input always name the same
// blocker, never an arbitrary one among several.
var dimensionOrder = []struct {
	name string
	get  func(e ClosureMatrixEntry) DimensionStatus
}{
	{"code_status", func(e ClosureMatrixEntry) DimensionStatus { return e.CodeStatus }},
	{"data_status", func(e ClosureMatrixEntry) DimensionStatus { return e.DataStatus }},
	{"contract_status", func(e ClosureMatrixEntry) DimensionStatus { return e.ContractStatus }},
	{"execution_status", func(e ClosureMatrixEntry) DimensionStatus { return e.ExecutionStatus }},
	{"external_review_status", func(e ClosureMatrixEntry) DimensionStatus { return e.ExternalReviewStatus }},
	{"evidence_status", func(e ClosureMatrixEntry) DimensionStatus { return e.EvidenceStatus }},
}

// ErrUnknownDimensionStatus is returned by Compute when any dimension
// carries a value outside this package's own known set -- an
// unrecognized status (e.g. a typo, or a newer schema's value fed to
// an older binary) is never treated as "must be fine, must be closing".
var ErrUnknownDimensionStatus = fmt.Errorf("qualification: unknown closure-matrix dimension status")

// Compute fills in FinalStatus and FinalReason on e and returns the
// updated entry. It NEVER trusts a caller-supplied FinalStatus (any
// existing value is overwritten) -- this is the mechanical guarantee
// that FinalStatus is always derived, never asserted.
//
// Rule, in order:
//  1. Any dimension == REJECTED -> FinalStatus = REJECTED.
//  2. Else, the first dimension (in dimensionOrder) whose value is not
//     one of {CODE_COMPLETE, QUALIFIED, VERIFIED} blocks closure --
//     FinalStatus becomes THAT dimension's own value, naming exactly
//     what is missing.
//  3. Else (every dimension closing) -> FinalStatus =
//     EvidenceStatus (QUALIFIED or VERIFIED), since evidence_status is
//     the ultimate "has this actually been proven" dimension.
func Compute(e ClosureMatrixEntry) (ClosureMatrixEntry, error) {
	for _, d := range dimensionOrder {
		v := d.get(e)
		if !knownDimensionStatuses[v] {
			return e, fmt.Errorf("%w: %s=%q", ErrUnknownDimensionStatus, d.name, v)
		}
	}
	for _, d := range dimensionOrder {
		if d.get(e) == DimRejected {
			e.FinalStatus = DimRejected
			e.FinalReason = fmt.Sprintf("rejected: %s", d.name)
			return e, nil
		}
	}
	for _, d := range dimensionOrder {
		v := d.get(e)
		if !closingValues[v] {
			e.FinalStatus = v
			e.FinalReason = fmt.Sprintf("blocked by %s", d.name)
			return e, nil
		}
	}
	e.FinalStatus = e.EvidenceStatus
	e.FinalReason = "closed: evidence_status"
	return e, nil
}
