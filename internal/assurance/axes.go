package assurance

// PHASE E3 (P0-8) — Readiness Engine Separation.
//
// The pre-insurance closure program's structural complaint about this
// package was precise: a gate's readiness was collapsed into ONE
// answer. `scale_qualification` reports BLOCKED, and that single word
// has to carry three completely different facts at once — that the
// harness is real and passes, that it has been qualified as far as this
// sandbox honestly allows, and that the literal 100-node/1M-envelope
// acceptance criterion still needs infrastructure nobody has bought.
// Collapsing those three into "BLOCKED" loses information in the
// direction that matters least (it under-reports real engineering
// progress) but it ALSO loses it in the direction that matters most: a
// reader cannot tell a gate that is blocked because the code is missing
// from a gate that is blocked because a purchase order is missing.
//
// This file separates them into four explicit axes per gate:
//
//	ENGINEERING  does the code exist, and does its own gate command pass?
//	INTERNAL     has it been qualified as far as this environment allows?
//	EXTERNAL     has real external evidence qualified it?
//	FINAL        the composed answer, which is BLOCKED_EXTERNAL whenever
//	             EXTERNAL is, no matter how green the first two are.
//
// The axes are DERIVED, never declared — exactly like EffectiveStatus.
// There is no setter that writes PASS into an axis; Axes() computes
// each one from the same evidence artifacts Assess() already reads.
// Adding this file does not, and structurally cannot, advance any
// gate's Status: Assess(), Satisfied(), EffectiveStatus() and the
// release Verdict are all untouched by everything here. The eight
// externally-blocked mandatory gates stay BLOCKED, with their existing
// named blockers, and the release verdict stays NOT_PRODUCTION_READY —
// this only changes how that fact is REPORTED, adding the engineering
// and internal detail the single word was hiding.

import "sort"

// AxisStatus is the closed vocabulary the four axes report in. Like
// Status, "Done"/"Closed"/"Complete" are deliberately absent.
type AxisStatus string

const (
	// AxisNotApplicable is used when an axis has no meaning for a gate —
	// e.g. EXTERNAL on a gate with no external dependency.
	AxisNotApplicable AxisStatus = "NOT_APPLICABLE"
	// AxisNotRun means no evidence exists for this axis at all. It is
	// the honest default and the fail-closed value.
	AxisNotRun AxisStatus = "NOT_RUN"
	// AxisPass means this axis has a passing, hash-verified artifact.
	AxisPass AxisStatus = "PASS"
	// AxisFail means this axis has an artifact and the artifact records
	// a failure.
	AxisFail AxisStatus = "FAIL"
	// AxisInternalQualified is the honest ceiling for anything proven
	// only inside this build environment. It is NEVER a synonym for
	// VERIFIED or QUALIFIED: a container drill is real evidence about
	// the harness, and no evidence at all about production.
	AxisInternalQualified AxisStatus = "INTERNAL_QUALIFIED"
	// AxisExternalQualified means real, release-bound, signed external
	// evidence advanced this gate through pkg/governance/qualification.
	AxisExternalQualified AxisStatus = "EXTERNAL_QUALIFIED"
	// AxisBlockedExternal names a real-world dependency: money, a
	// contract, physical infrastructure, an independent third party.
	AxisBlockedExternal AxisStatus = "BLOCKED_EXTERNAL"
	// AxisWaived mirrors StatusWaived on the FINAL axis.
	AxisWaived AxisStatus = "WAIVED"
	// AxisReady is the FINAL axis's only affirmative value, and it
	// requires every other axis to have earned it.
	AxisReady AxisStatus = "READY"
	// AxisNotReady is the FINAL axis's value when the gate simply does
	// not meet its own RequiredStatus, with no external excuse.
	AxisNotReady AxisStatus = "NOT_READY"
)

// GateAxes is one gate's four-axis readiness report.
type GateAxes struct {
	GateID      string     `json:"gate_id"`
	Mandatory   bool       `json:"mandatory"`
	Engineering AxisStatus `json:"engineering"`
	Internal    AxisStatus `json:"internal"`
	External    AxisStatus `json:"external"`
	Final       AxisStatus `json:"final"`
	// ExternalDependency echoes the gate's own named blocker, so the
	// EXTERNAL axis is never a bare BLOCKED_EXTERNAL with no reason.
	ExternalDependency string `json:"external_dependency,omitempty"`
	// Note explains, in one sentence, why FINAL is what it is.
	Note string `json:"note,omitempty"`
	// Canonical is the single-word readiness taxonomy every report,
	// register and PDF in this program is required to use verbatim
	// instead of inventing its own vocabulary. It is derived, never
	// declared -- see canonicalStatus below for the exact rule.
	Canonical CanonicalStatus `json:"canonical_status"`
}

// CanonicalStatus is the one-source-of-truth readiness vocabulary this
// program's own governing documents mandate: no gap may be reported
// under two different names across the manifest, the gap-closure
// report, the external-dependency register and the PDF deliverables.
// It is a strict, mechanical function of the four axes above -- there
// is no separate place that assigns it by hand, so it cannot drift
// from what Axes() actually computed.
type CanonicalStatus string

const (
	// CanonicalVerifiedInternal: FINAL is READY. Either the gate has no
	// external dependency at all, or it had one and real EXTERNAL_QUALIFIED
	// evidence closed it. This is the ceiling any gate can honestly reach
	// from inside this repository and its own qualification harnesses.
	CanonicalVerifiedInternal CanonicalStatus = "VERIFIED_INTERNAL"
	// CanonicalReadyForExternalQualification: the gate's own code and its
	// in-sandbox qualification harness both pass (ENGINEERING=PASS,
	// INTERNAL=INTERNAL_QUALIFIED) but FINAL is still BLOCKED_EXTERNAL --
	// every internally-closeable half of the work is done; only the
	// external act itself (a vendor, a purchase order, a physical host,
	// an independent auditor) is missing.
	CanonicalReadyForExternalQualification CanonicalStatus = "READY_FOR_EXTERNAL_QUALIFICATION"
	// CanonicalBlockedExternal: FINAL is BLOCKED_EXTERNAL and the internal
	// qualification step itself could not even be attempted (INTERNAL is
	// NOT_RUN or NOT_APPLICABLE) -- typically because the external
	// dependency is a precondition for running any qualification drill at
	// all (you cannot internally qualify against a real HSM without an
	// HSM; you cannot admit a live data feed without a live data
	// contract; you cannot run a pentest without an independent pentest
	// firm).
	CanonicalBlockedExternal CanonicalStatus = "BLOCKED_EXTERNAL"
	// CanonicalNotReady: FINAL is NOT_READY (the gate fails its own
	// required status on engineering grounds, with no external excuse
	// available) or WAIVED (a waiver is a visibly weaker verdict, never a
	// synonym for readiness).
	CanonicalNotReady CanonicalStatus = "NOT_READY"
)

// canonicalStatus derives the CanonicalStatus from a gate's already-computed
// four axes. See the constants above for the exact rule; this function is
// the only place that rule is implemented.
func canonicalStatus(a GateAxes) CanonicalStatus {
	switch a.Final {
	case AxisReady:
		return CanonicalVerifiedInternal
	case AxisBlockedExternal:
		if a.Internal == AxisInternalQualified {
			return CanonicalReadyForExternalQualification
		}
		return CanonicalBlockedExternal
	default: // AxisNotReady, AxisWaived
		return CanonicalNotReady
	}
}

// Axes derives this gate's four-axis report. Every value comes from
// evidence already attached to the gate; nothing here can be set by
// hand.
//
// The engineering axis reads EngineeringEvidence when present and falls
// back to the gate's main Evidence, because for the great majority of
// gates the gate command IS the engineering proof. The internal axis
// reads InternalEvidence only: an in-sandbox qualification drill is a
// deliberate, separate act, and inferring one from a passing unit test
// would be precisely the false-green inflation this package exists to
// prevent.
func (g Gate) Axes() GateAxes {
	a := GateAxes{
		GateID: g.ID, Mandatory: g.Mandatory,
		ExternalDependency: g.ExternalDependency,
	}

	a.Engineering = axisFromEvidence(g.EngineeringEvidence)
	if a.Engineering == AxisNotRun {
		a.Engineering = axisFromEvidence(g.Evidence)
	}

	switch axisFromEvidence(g.InternalEvidence) {
	case AxisPass:
		a.Internal = AxisInternalQualified
	case AxisFail:
		a.Internal = AxisFail
	default:
		// A gate with no external dependency has nothing an "internal
		// qualification" step would add beyond its own engineering gate:
		// there is no production environment it is being contrasted
		// against. Reporting NOT_RUN there would read as a gap that does
		// not exist.
		if g.ExternalDependency == "" {
			a.Internal = AxisNotApplicable
		} else {
			a.Internal = AxisNotRun
		}
	}

	switch {
	case g.ExternalDependency == "":
		a.External = AxisNotApplicable
	case g.EffectiveStatus() == StatusBlocked:
		a.External = AxisBlockedExternal
	case g.EffectiveStatus().AtLeast(StatusQualified):
		// The gate carried a named external dependency AND reached
		// QUALIFIED or better, which in this codebase happens only when
		// cmd/veriqo-readiness attached real, validated external
		// qualification evidence (see its loadExternalQualifications).
		a.External = AxisExternalQualified
	default:
		a.External = AxisBlockedExternal
	}

	// FINAL. The rule the program stated verbatim: external BLOCKED
	// makes final BLOCKED_EXTERNAL, however green engineering and
	// internal are.
	switch {
	case g.Waiver != nil && g.Waiver.valid() == nil:
		a.Final = AxisWaived
		a.Note = "waived: a waiver never produces READY, only a visibly weaker verdict"
	case a.External == AxisBlockedExternal:
		a.Final = AxisBlockedExternal
		a.Note = "engineering and internal qualification cannot substitute for the named external dependency"
	case a.Engineering == AxisFail:
		a.Final = AxisNotReady
		a.Note = "the gate's own command failed; this is an engineering gap, not an external one"
	case g.Satisfied():
		a.Final = AxisReady
	default:
		a.Final = AxisNotReady
		a.Note = "gate does not meet its own required status"
	}
	a.Canonical = canonicalStatus(a)
	return a
}

// axisFromEvidence is the shared, evidence-only derivation: PASS needs
// a hash-verified artifact recording exit code 0; an artifact recording
// anything else is FAIL; no artifact at all is NOT_RUN. An artifact
// whose hash does not match its content counts as no artifact (see
// Evidence.Passing), which is why a tampered artifact degrades rather
// than passes.
func axisFromEvidence(list []Evidence) AxisStatus {
	if len(list) == 0 {
		return AxisNotRun
	}
	sawVerified := false
	for _, e := range list {
		if e.Passing() {
			return AxisPass
		}
		if e.Verify() == nil {
			sawVerified = true
		}
	}
	if sawVerified {
		return AxisFail
	}
	return AxisNotRun
}

// AttachEngineering records an artifact against a gate's ENGINEERING
// axis without touching its Status. This is how a blocked gate can
// honestly report that its harness is real and passing while its
// overall status stays BLOCKED.
func (r *Registry) AttachEngineering(gateID string, ev Evidence) error {
	return r.attachAxis(gateID, ev, func(g *Gate, e Evidence) {
		g.EngineeringEvidence = append(g.EngineeringEvidence, e)
	})
}

// AttachInternal records an artifact against a gate's INTERNAL
// axis without touching its Status — an in-sandbox qualification drill
// (a container cluster, a 60-minute soak) whose honest ceiling is
// INTERNAL_QUALIFIED.
func (r *Registry) AttachInternal(gateID string, ev Evidence) error {
	return r.attachAxis(gateID, ev, func(g *Gate, e Evidence) {
		g.InternalEvidence = append(g.InternalEvidence, e)
	})
}

func (r *Registry) attachAxis(gateID string, ev Evidence, apply func(*Gate, Evidence)) error {
	g, ok := r.gates[gateID]
	if !ok {
		return unknownGate(gateID)
	}
	if err := ev.Verify(); err != nil {
		return err
	}
	apply(g, ev)
	return nil
}

// AxesReport is the whole-release four-axis view, gate by gate, plus
// the counts an operator actually reads first.
type AxesReport struct {
	Gates []GateAxes `json:"gates"`
	// BlockedExternalMandatory names every mandatory gate whose FINAL
	// axis is BLOCKED_EXTERNAL. This list is the register the
	// residual-external-gate document is generated from.
	BlockedExternalMandatory []string `json:"blocked_external_mandatory,omitempty"`
	// EngineeringPassing counts gates whose ENGINEERING axis is PASS —
	// the number that "BLOCKED" used to hide.
	EngineeringPassing int `json:"engineering_passing"`
	EngineeringTotal   int `json:"engineering_total"`
	InternalQualified  int `json:"internal_qualified"`
	ExternalQualified  int `json:"external_qualified"`

	// CanonicalSummary is the same 60 (or however many are registered)
	// gates, counted once each under the one-source-of-truth taxonomy
	// above. VerifiedInternal + ReadyForExternalQualification +
	// BlockedExternal + NotReady always equals len(Gates) exactly --
	// every gate lands in exactly one bucket, never zero and never two.
	VerifiedInternal              int `json:"canonical_verified_internal"`
	ReadyForExternalQualification int `json:"canonical_ready_for_external_qualification"`
	BlockedExternal               int `json:"canonical_blocked_external"`
	NotReady                      int `json:"canonical_not_ready"`
}

// Axes computes the four-axis report for every registered gate, in
// stable gate-ID order.
func (r *Registry) Axes() AxesReport {
	ids := append([]string(nil), r.order...)
	sort.Strings(ids)
	rep := AxesReport{}
	for _, id := range ids {
		a := r.gates[id].Axes()
		rep.Gates = append(rep.Gates, a)
		rep.EngineeringTotal++
		if a.Engineering == AxisPass {
			rep.EngineeringPassing++
		}
		if a.Internal == AxisInternalQualified {
			rep.InternalQualified++
		}
		if a.External == AxisExternalQualified {
			rep.ExternalQualified++
		}
		if a.Mandatory && a.Final == AxisBlockedExternal {
			rep.BlockedExternalMandatory = append(rep.BlockedExternalMandatory, a.GateID)
		}
		switch a.Canonical {
		case CanonicalVerifiedInternal:
			rep.VerifiedInternal++
		case CanonicalReadyForExternalQualification:
			rep.ReadyForExternalQualification++
		case CanonicalBlockedExternal:
			rep.BlockedExternal++
		case CanonicalNotReady:
			rep.NotReady++
		}
	}
	return rep
}
