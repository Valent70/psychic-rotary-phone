// corpus.go assembles VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2 per the
// brief's §14: RWC-001 + RWC-002, versioned and additive. There is no
// pre-existing RWC v1 corpus in this repository to preserve (baseline
// doc's correction to the brief's premise, docs/
// RWC_V2_NATIVE_INTEGRATION_BASELINE.md, top of file) — V2 is therefore
// the first version, and this file's structure (a named, versioned
// registry a future V3 would append to, never rewrite) is what makes it
// additive-by-construction rather than additive-by-promise.
package rwc

const CorpusVersion = "VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2"

// CorpusCaseKind distinguishes the two shapes of case this corpus
// contains — a physical vessel/port suitability judgment (RWC-001) vs a
// claim/provenance separation case (RWC-002) — so a generic runner
// (cmd/veriqo-rwc-v2) can classify each case's result correctly without
// hard-coding case IDs.
type CorpusCaseKind string

const (
	KindVesselPortSuitability CorpusCaseKind = "VESSEL_PORT_SUITABILITY"
	KindProvenanceClaim       CorpusCaseKind = "PROVENANCE_CLAIM"
)

// CorpusCase is one entry in the V2 corpus registry: enough metadata for
// a runner to build, execute, classify, and replay-verify it uniformly.
type CorpusCase struct {
	ID      string
	RWCID   string // "RWC-001" | "RWC-002"
	Kind    CorpusCaseKind
	Request CaseRequest
	// Constraint is set (non-nil semantics via Evaluated>0 or the zero
	// value) only for Kind==KindVesselPortSuitability.
	Constraint ConstraintResult
	// RedFlagsMatched is set only for the RWC-002 transaction-sequence
	// case.
	RedFlagsMatched []string
}

// BuildCorpusV2 builds every case in VERIQO_REAL_WORLD_VALIDATION_CORPUS_V2
// — RWC-001's five adversarial candidates (brief §4) plus RWC-002's five
// claim-category cases (brief §5) — deterministically from tick.
func BuildCorpusV2(tick uint64) ([]CorpusCase, error) {
	var cases []CorpusCase

	for _, c := range RWC001Candidates() {
		req, cr, err := BuildRWC001Case(c, tick)
		if err != nil {
			return nil, err
		}
		cases = append(cases, CorpusCase{
			ID: req.CaseID, RWCID: "RWC-001", Kind: KindVesselPortSuitability,
			Request: req, Constraint: cr,
		})
	}

	viReq, _, err := BuildRWC002VesselIdentityCase(tick)
	if err != nil {
		return nil, err
	}
	cases = append(cases, CorpusCase{ID: viReq.CaseID, RWCID: "RWC-002", Kind: KindProvenanceClaim, Request: viReq})

	vpReq := BuildRWC002VoyagePositionCase(tick)
	cases = append(cases, CorpusCase{ID: vpReq.CaseID, RWCID: "RWC-002", Kind: KindProvenanceClaim, Request: vpReq})

	ciReq := BuildRWC002CargoIdentityCase(tick)
	cases = append(cases, CorpusCase{ID: ciReq.CaseID, RWCID: "RWC-002", Kind: KindProvenanceClaim, Request: ciReq})

	deReq := BuildRWC002DocumentExistenceCase(tick)
	cases = append(cases, CorpusCase{ID: deReq.CaseID, RWCID: "RWC-002", Kind: KindProvenanceClaim, Request: deReq})

	txReq, redFlags := BuildRWC002TransactionCase(tick)
	cases = append(cases, CorpusCase{ID: txReq.CaseID, RWCID: "RWC-002", Kind: KindProvenanceClaim, Request: txReq, RedFlagsMatched: redFlags})

	return cases, nil
}
