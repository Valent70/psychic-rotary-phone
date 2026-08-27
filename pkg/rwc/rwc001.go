package rwc

// rwc001.go is RWC-001: vessel/port physical suitability, five
// adversarial candidates against real port-authority constraint figures.
//
// PORT NOTE (R22). The originating branch also carried a
// BuildRWC001CaseWithAIS variant that sourced the case's logical tick
// from a fixture-file-backed pkg/maritime evidence adapter. That adapter
// does not exist on this branch and was deliberately NOT ported: this
// branch's real AIS ingestion path is pkg/connector/aisstream, which
// mints canonical evidence through the audited source -> adapter ->
// contract chain (see internal/nobypass.ingestionConstructors) and
// requires both live network egress and a RightsState above
// UNKNOWN_PENDING_CONTRACT. Neither is available here. Adding a second,
// fixture-only maritime evidence source alongside it would be exactly
// the parallel ingestion path internal/nobypass exists to prevent, so
// RWC-001's tick stays caller-injected and the absence of a real AIS
// feed is reported as an open external dependency rather than papered
// over with a fixture that resembles one.
import (
	"encoding/json"

	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/entity"
)

// RWC001BaselineVessel is candidate A from the brief §4, the vessel the
// other four adversarial candidates are each a single-field mutation of
// (as the brief specifies: "B: LOA 151m, otherwise compatible", etc).
// Defined once here so every candidate literally shares every field
// except the one the brief names — this is what makes the later
// mutation tests (mutation_test.go) a true single-field perturbation of
// this exact struct, not a re-typed lookalike.
var RWC001BaselineVessel = Vessel{
	Name: "RWC-001-CANDIDATE-A", LOAMeters: 140, DraftMeters: 7.2,
	Geared: true, CraneCapacityT: 30, GrabCapacityCBM: 12, Bowthruster: true,
}

// RWC001Candidate is one adversarial test vessel plus the port it is
// evaluated against, exactly as specified in the brief's §4.
type RWC001Candidate struct {
	ID     string // "A".."E"
	Vessel Vessel
	Port   string // key into Ports
}

// RWC001Candidates returns the five adversarial candidates from the
// brief §4, each derived from RWC001BaselineVessel by exactly the one
// mutation the brief specifies — never a freshly hand-typed struct per
// candidate, so there is no risk of an unintended second field drifting
// along with the intended one.
func RWC001Candidates() []RWC001Candidate {
	b := RWC001BaselineVessel

	candA := b
	candA.Name = "RWC-001-CANDIDATE-A"

	candB := b
	candB.Name = "RWC-001-CANDIDATE-B"
	candB.LOAMeters = 151

	candC := b
	candC.Name = "RWC-001-CANDIDATE-C"
	candC.DraftMeters = 8.4

	candD := b
	candD.Name = "RWC-001-CANDIDATE-D"
	candD.Geared = false

	candE := b
	candE.Name = "RWC-001-CANDIDATE-E"
	// draft 7.2 unchanged from baseline, per the brief's own §4 spec for E

	return []RWC001Candidate{
		{ID: "A", Vessel: candA, Port: "AKONIKIEN"},
		{ID: "B", Vessel: candB, Port: "AKONIKIEN"},
		{ID: "C", Vessel: candC, Port: "AKONIKIEN"},
		{ID: "D", Vessel: candD, Port: "AKONIKIEN"},
		{ID: "E", Vessel: candE, Port: "DOUALA"},
	}
}

// vesselDeclarationValue canonically encodes the vessel-facing fields a
// charterer declaration would actually assert (excluding the internal
// candidate Name/ID bookkeeping) — this is the Value submitted as
// evidence, so it must be exactly what a real declaration document would
// contain, not an internal test fixture identifier.
func vesselDeclarationValue(v Vessel) (string, error) {
	decl := struct {
		LOAMeters       float64 `json:"loa_m"`
		DraftMeters     float64 `json:"draft_m"`
		Geared          bool    `json:"geared"`
		CraneCapacityT  float64 `json:"crane_capacity_t"`
		GrabCapacityCBM float64 `json:"grab_capacity_cbm"`
		Bowthruster     bool    `json:"bowthruster"`
	}{v.LOAMeters, v.DraftMeters, v.Geared, v.CraneCapacityT, v.GrabCapacityCBM, v.Bowthruster}
	b, err := json.Marshal(decl)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildRWC001Case builds the native CaseRequest for one candidate — the
// constraint evaluation (EvaluateVesselAtPort) runs FIRST and its
// ViolationRatio is what feeds the native risk/decision path
// (PatternScore/PriceAnomaly), so the decision that later emerges from
// decision.Engine.Decide is a real function of this evidence, not a
// label attached after the fact. tick is the caller-injected logical
// clock (baseTime) — see corpus.go for where a fixed value is chosen.
func BuildRWC001Case(c RWC001Candidate, tick uint64) (CaseRequest, ConstraintResult, error) {
	port, portKnown := Ports[c.Port]
	if !portKnown {
		cr := ConstraintResult{Port: c.Port, Vessel: c.Vessel.Name} // Evaluated stays 0 -> INSUFFICIENT_EVIDENCE
		req, err := buildVesselPortCase("RWC-001-"+c.ID, c.Vessel, port, c.Port, cr, tick)
		return req, cr, err
	}
	return BuildVesselPortCase("RWC-001-"+c.ID, c.Vessel, port, c.Port, tick)
}

// BuildVesselPortCase is the generic, case-ID-parameterized builder
// BuildRWC001Case is a thin wrapper over. It is exported so callers that
// need an arbitrary (Vessel, Port) pair not registered in the Ports
// table — mutation_test.go's adversarial-replay tests (brief §13) — can
// build a real native CaseRequest without duplicating this wiring.
func BuildVesselPortCase(caseID string, v Vessel, p Port, portLabel string, tick uint64) (CaseRequest, ConstraintResult, error) {
	cr := EvaluateVesselAtPort(v, p)
	req, err := buildVesselPortCase(caseID, v, p, portLabel, cr, tick)
	return req, cr, err
}

func buildVesselPortCase(caseID string, v Vessel, p Port, portLabel string, cr ConstraintResult, tick uint64) (CaseRequest, error) {
	declValue, err := vesselDeclarationValue(v)
	if err != nil {
		return CaseRequest{}, err
	}

	req := CaseRequest{
		CaseID:    caseID,
		Objective: "RWC-001 Ranchan Maritime: assess vessel/port physical suitability, " + caseID + " vs " + portLabel,
		Tenant:    "veriqo-rwc-v2",
		EntityAliases: []entity.Alias{
			{Kind: "NAME", Value: v.Name},
		},
		Predicate: string(CategoryVesselConstraint) + ":" + portLabel,
		Submissions: []canonical.SourceSubmission{
			{
				SourceID:        "RANCHAN_CHARTERER_VESSEL_DECLARATION",
				Value:           declValue,
				BaseReliability: 0.95,
				Provider:        "RANCHAN_MARITIME_SDN_BHD",
			},
		},
		Policy:       VesselPortPolicy,
		PatternScore: cr.ViolationRatio(),
		PriceAnomaly: cr.ViolationRatio(), // see policy.go doc comment: single composite RWC signal fed to both risk-model input slots
		Tick:         tick,
	}
	return req, nil
}
