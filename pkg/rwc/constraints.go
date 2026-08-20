package rwc

import "fmt"

// Vessel is a candidate vessel's declared physical/operational
// specification, as supplied by a charterer/broker declaration. Field
// names mirror the RWC-001 corpus's adversarial candidate fields
// exactly (LOA, draft, geared, crane, grabs, bowthruster).
type Vessel struct {
	Name            string
	LOAMeters       float64
	DraftMeters     float64
	Geared          bool
	CraneCapacityT  float64 // 0 = not stated
	GrabCapacityCBM float64 // 0 = not stated
	Bowthruster     bool
	IMO             string // optional; "" if not supplied (RWC-001 candidates have none)
	MMSI            string // optional
}

// ConstraintCheck is one named, evaluable dimension of vessel/port
// physical suitability, and its outcome against a specific Port. This is
// new domain logic (baseline doc gap G2 — pkg/moat/domain/maritime has
// no LOA/draft/geared/crane fields at all) that feeds the NATIVE
// decision.Engine as a computed factor; it does not replace or bypass
// decision.Engine — see policy.go.
type ConstraintCheck struct {
	Name       string
	Evaluated  bool // false = port/vessel data insufficient to evaluate this dimension at all
	Violated   bool // true = hard constraint violated (evaluated=true required)
	Unresolved bool // true = evaluable in principle but the corpus itself says live/tide-dependent confirmation is required
	Detail     string
}

// ConstraintResult is every dimension checked for one vessel-at-port
// evaluation, plus the derived violation ratio fed to the native risk
// model (see policy.go's PatternScore wiring).
type ConstraintResult struct {
	Port           string
	Vessel         string
	Checks         []ConstraintCheck
	HardViolations []string // Check.Name for every Violated=true
	Unresolved     []string // Check.Name for every Unresolved=true
	Evaluated      int      // count of Check.Evaluated=true
}

// EvaluateVesselAtPort deterministically compares a candidate vessel's
// declared specification against one port's constraint table. It is a
// pure function of (vessel, port) — no case-specific branching, no
// vessel-name or case-ID comparison anywhere in this function, so the
// same logic runs identically whether the caller is RWC-001 candidate A,
// B, C, D, E, or any future candidate. This is what makes the resulting
// PASS/FAIL/CONDITIONAL verdict "emerge from the native reasoning path"
// rather than being hard-coded per case, per the brief's §4 requirement.
func EvaluateVesselAtPort(v Vessel, p Port) ConstraintResult {
	res := ConstraintResult{Port: p.Name, Vessel: v.Name}

	add := func(c ConstraintCheck) {
		res.Checks = append(res.Checks, c)
		if !c.Evaluated {
			return
		}
		res.Evaluated++
		if c.Violated {
			res.HardViolations = append(res.HardViolations, c.Name)
		}
		if c.Unresolved {
			res.Unresolved = append(res.Unresolved, c.Name)
		}
	}

	// LOA
	if p.MaxLOA > 0 {
		violated := v.LOAMeters > p.MaxLOA
		add(ConstraintCheck{
			Name: "LOA", Evaluated: true, Violated: violated,
			Detail: fmt.Sprintf("vessel LOA=%.1fm vs port max=%.1fm", v.LOAMeters, p.MaxLOA),
		})
	} else {
		add(ConstraintCheck{Name: "LOA", Evaluated: false, Detail: "port has no stated LOA limit in corpus"})
	}

	// Draft — operational limit takes precedence when the corpus states
	// one distinct from the absolute max (Akonikien); otherwise the
	// absolute max is the binding figure.
	switch {
	case p.TideDependent:
		// The corpus itself frames this port's usable draft as varying
		// with real-time tide (see Port.TideDependent doc). Whether the
		// vessel's static draft is numerically under the recommended
		// figure is checked and reported, but the dimension is marked
		// Unresolved regardless of that numeric result, because the
		// corpus explicitly says operational/tide confirmation is
		// required and no live tide feed exists in this environment
		// (baseline doc §1).
		limit := p.MaxDraftOperational
		if limit == 0 {
			limit = p.MaxDraftAbsolute
		}
		withinStatedFigure := limit == 0 || v.DraftMeters <= limit
		add(ConstraintCheck{
			Name: "DRAFT", Evaluated: true, Violated: false, Unresolved: true,
			Detail: fmt.Sprintf(
				"vessel draft=%.1fm; port channel draft varies with tide (tide-dependent port); static figure comparison (within_stated_recommended=%v) is not sufficient — real-time tide confirmation required and unavailable (no live data feed in this environment)",
				v.DraftMeters, withinStatedFigure),
		})
	case p.MaxDraftOperational > 0:
		violated := v.DraftMeters > p.MaxDraftOperational
		add(ConstraintCheck{
			Name: "DRAFT", Evaluated: true, Violated: violated,
			Detail: fmt.Sprintf("vessel draft=%.1fm vs port recommended operational max=%.1fm", v.DraftMeters, p.MaxDraftOperational),
		})
	case p.MaxDraftAbsolute > 0:
		violated := v.DraftMeters > p.MaxDraftAbsolute
		add(ConstraintCheck{
			Name: "DRAFT", Evaluated: true, Violated: violated,
			Detail: fmt.Sprintf("vessel draft=%.1fm vs port max draft=%.1fm", v.DraftMeters, p.MaxDraftAbsolute),
		})
	default:
		add(ConstraintCheck{Name: "DRAFT", Evaluated: false, Detail: "port has no stated draft limit in corpus"})
	}

	// Geared requirement
	if p.RequiresGeared {
		violated := !v.Geared
		add(ConstraintCheck{
			Name: "GEARED", Evaluated: true, Violated: violated,
			Detail: fmt.Sprintf("port requires geared vessel; vessel geared=%v", v.Geared),
		})
	} else {
		add(ConstraintCheck{Name: "GEARED", Evaluated: false, Detail: "port has no stated gear requirement in corpus"})
	}

	return res
}

// ViolationRatio is the deterministic [0,1] signal fed to the native
// risk/decision path (see policy.go). It is computed purely from
// counts, never from case identity:
//
//	1.0 if any hard constraint was violated
//	0.5 if no hard violation but at least one dimension is unresolved
//	0.0 if every evaluated dimension passed cleanly
//
// This 3-band scheme is documented (not silently chosen) because it is
// the one free design parameter in this adapter: the underlying
// composite risk formula (pkg/moat/intelligence/risk.Model.Score) is
// fixed and audited, but what number RWC feeds into it as
// PatternScore/PriceAnomalyScore is this package's decision, made once
// here, applied uniformly to every case.
func (r ConstraintResult) ViolationRatio() float64 {
	if len(r.HardViolations) > 0 {
		return 1.0
	}
	if len(r.Unresolved) > 0 {
		return 0.5
	}
	return 0.0
}
