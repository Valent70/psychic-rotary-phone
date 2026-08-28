package casepack

import (
	"fmt"

	"veriqo/pkg/geospatial"
	"veriqo/pkg/insurance/auditlink"
	"veriqo/pkg/insurance/casestate"
	"veriqo/pkg/insurance/dispute"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/payment"
	"veriqo/pkg/insurance/policy"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/recovery"
	"veriqo/pkg/insurance/regulatory"
	"veriqo/pkg/insurance/reserve"
	"veriqo/pkg/insurance/salvage"
	"veriqo/pkg/platform/audit"
)

// This file closes the VERIQO Final Remaining Gap Closure Order's P0
// §6 ("VERIQO Golden Cross-Domain Case") and §7 ("Full Insurance
// End-to-End"): one authoritative case proving that the subsystems this
// round added — geospatial/geofencing, the party relationship layer,
// salvage, and co-insurance/reinsurance allocation — are CONNECTED to
// the rest of the domain, not four more islands sitting next to it.
//
// Rather than inventing an eighth synthetic narrative from nothing,
// DriveGolden extends CASE-INS-002 (cargo damage / reefer), which
// already carries the mandate's own named chain about as far as any one
// of the seven cases does: Commodity(refrigerated cargo) -> Cargo ->
// B/L -> Vessel(carrier) -> Voyage(implicit) -> Port/Terminal(warehouse)
// -> Incident(temperature excursion) -> Policy -> Claim -> Evidence ->
// Causation -> Quantum -> Coverage -> Recovery -> Mitigation -> Deadline
// -> Human Review -> Dossier/Certificate -> Ledger, ALL OF IT already
// proven end to end by the real facade (TestEveryCaseDrivesTheFullFacadePath)
// and already carrying its own SALVAGE_SALE evidence record that no
// prior round had actually wired into the salvage package. DriveGolden
// runs the standard Drive() unchanged first — reusing every one of
// those proven steps rather than re-deriving them — and then layers the
// four previously-unconnected subsystems on top of the SAME case:
//
//  1. Geospatial: the incident location is resolved against a real
//     geofenced discharge-terminal zone, and the vessel's own two-fix
//     track is checked for physical plausibility.
//  2. Party network: a broker relationship is declared, evidenced,
//     consented to, and used to gate a permission — proving the
//     relationship layer is not merely a schema.
//  3. Salvage: the case's own SALVAGE_SALE evidence backs a real
//     salvage.Operation, disposed, and its net value is fed into a
//     genuine quantum RECOMPUTATION (via quantum.Compute directly, the
//     same pure function ComputeQuantum calls) proving the salvage
//     figure actually reduces the indicative claim value rather than
//     sitting beside it unused.
//  4. Co-insurance/reinsurance: the resulting payment is split across a
//     co-insurer and the named insurer, and the insurer's own primary
//     share is further split with a reinsurer — both allocations
//     verified to sum to EXACTLY their input, per policy.Allocation's
//     own exactness guarantee.
//  5. Dispute: one unresolved contractual question (whether the
//     temperature excursion falls within the policy's own notice
//     window) is opened as a dispute.Issue with both sides' positions
//     recorded, unreconciled — never a decided winner.
//
// This is not a duplicate engine for any of the above (Final Design
// §39): every step below calls the SAME package the standalone unit
// tests already exercise (salvage.Registry, policy.Version.Allocate*,
// party.RelationshipRegistry, geospatial.Registry, dispute.Matter) —
// DriveGolden's only original content is the WIRING between them.
type GoldenResult struct {
	*Result

	// ---- Geospatial ----
	IncidentZones []geospatial.Geofence
	// VesselImpliedKnotsHundredths is the vessel's implied speed in
	// hundredths of a knot (1834 == 18.34 knots) — a fixed-point integer
	// at the pkg/insurance boundary, matching quantum.Amount's own
	// float64-avoidance discipline (pkg/insurance/guardrails' whole-tree
	// scan enforces "no opaque float field anywhere in this domain" and
	// does not distinguish a confidence score from a physical
	// measurement by name, so the fix here is the same fixed-point
	// conversion this codebase already applies at every other
	// money/rate boundary — see geospatial.ImpliedSpeedKnots for the
	// underlying float64 computation, which stays float64 inside that
	// package because haversine geodesy genuinely needs it).
	VesselImpliedKnotsHundredths int64
	VesselSpeedPlausible         bool

	// ---- Party network ----
	Relationships        *party.RelationshipRegistry
	BrokerRelationshipID string

	// ---- Salvage, genuinely reducing quantum ----
	SalvageRegistry       *salvage.Registry
	SalvageOperationID    string
	SalvageNetValue       quantum.EvidenceBackedAmount
	QuantumWithoutSalvage quantum.Calculation
	QuantumWithSalvage    quantum.Calculation

	// ---- Co-insurance / reinsurance ----
	PolicyVersionWithParticipants policy.Version
	CoInsuranceAllocation         []policy.Allocation
	ReinsuranceAllocation         []policy.Allocation

	// ---- Dispute ----
	DisputeMatter *dispute.Matter

	// ---- Reserve (Round 8: claim reserve lifecycle) ----
	Reserve *reserve.Reserve

	// ---- Recovery / subrogation (Round 8: a REAL target, not the empty
	// mechanism the base Drive() path exercises with nil targets) ----
	RecoveryRegistry *recovery.Registry
	RecoveryTargetID string

	// ---- Regulatory (Round 8: genuinely wired for the first time —
	// pkg/insurance/regulatory had zero callers anywhere before this) ----
	RegulatoryMatter *regulatory.Matter

	// ---- Payment lifecycle (Round 9: closes gap G1, "Payment
	// Lifecycle") ----
	Payment *payment.PaymentRecord

	// ---- Unified audit (Round 9: closes gap G2, "Unified Audit") ----
	// AuditStore is the ONE shared platform/audit ledger this case's own
	// lifecycle trail, payment history and reserve history are all
	// mirrored into via pkg/insurance/auditlink — see attachUnifiedAudit.
	AuditStore *audit.AuditStore

	// ---- Canonical case state machine (Round 10) ----
	// Lifecycle is a SEPARATE, parallel CaseLifecycle driven through the
	// SAME golden-case figures (reserve amount, payment amount/actors)
	// as the manually-orchestrated attachReserve/attachPayment steps
	// above — proving the production state machine (whose own
	// correctness is independently established by
	// pkg/insurance/casestate's own 17-case test suite) reaches the SAME
	// real domain outcome as this file's own hand-sequenced steps, not
	// merely a label alongside them.
	Lifecycle *casestate.CaseLifecycle
}

// DriveGolden drives CASE-INS-002 through the standard Drive() path,
// then layers geospatial, party-relationship, salvage, co-/reinsurance
// and dispute proof on top of that SAME driven case. ledger, if
// non-nil, is passed through to Drive() exactly as any caller of Drive
// would; a nil ledger is valid (Drive treats it as "no canonical
// lineage binding", the same as every existing case's own tests do when
// they pass nil).
func DriveGolden() (*GoldenResult, error) {
	c, err := Get(CaseCargoDamageReefer)
	if err != nil {
		return nil, fmt.Errorf("casepack: golden: %w", err)
	}
	base, err := Drive(c, nil)
	if err != nil {
		return nil, fmt.Errorf("casepack: golden: base Drive failed: %w", err)
	}
	gr := &GoldenResult{Result: base}

	if err := gr.attachGeospatial(); err != nil {
		return nil, fmt.Errorf("casepack: golden: geospatial: %w", err)
	}
	if err := gr.attachRelationships(); err != nil {
		return nil, fmt.Errorf("casepack: golden: relationships: %w", err)
	}
	if err := gr.attachSalvage(); err != nil {
		return nil, fmt.Errorf("casepack: golden: salvage: %w", err)
	}
	if err := gr.attachParticipation(); err != nil {
		return nil, fmt.Errorf("casepack: golden: participation: %w", err)
	}
	if err := gr.attachDispute(); err != nil {
		return nil, fmt.Errorf("casepack: golden: dispute: %w", err)
	}
	if err := gr.attachReserve(); err != nil {
		return nil, fmt.Errorf("casepack: golden: reserve: %w", err)
	}
	if err := gr.attachRecovery(); err != nil {
		return nil, fmt.Errorf("casepack: golden: recovery: %w", err)
	}
	if err := gr.attachRegulatory(); err != nil {
		return nil, fmt.Errorf("casepack: golden: regulatory: %w", err)
	}
	if err := gr.attachPayment(); err != nil {
		return nil, fmt.Errorf("casepack: golden: payment: %w", err)
	}
	if err := gr.attachLifecycle(); err != nil {
		return nil, fmt.Errorf("casepack: golden: lifecycle: %w", err)
	}
	if err := gr.attachUnifiedAudit(); err != nil {
		return nil, fmt.Errorf("casepack: golden: unified audit: %w", err)
	}
	return gr, nil
}

// ---- 1. Geospatial ----

func (gr *GoldenResult) attachGeospatial() error {
	terminal := geospatial.Geofence{
		ID: "TERMINAL-CALDER-BAY", Kind: geospatial.ZoneKindTerminal,
		Polygon: geospatial.Polygon{
			{Lat: 51.40, Lon: -3.00}, {Lat: 51.40, Lon: -2.90},
			{Lat: 51.45, Lon: -2.90}, {Lat: 51.45, Lon: -3.00},
		},
		EffectiveFrom: 0,
	}
	reg := geospatial.NewRegistry()
	if err := reg.Register(terminal); err != nil {
		return err
	}
	incidentLocation := geospatial.Coordinate{Lat: 51.42, Lon: -2.95} // inside the terminal
	gr.IncidentZones = reg.Containing(incidentLocation, 2000)
	if len(gr.IncidentZones) == 0 {
		return fmt.Errorf("golden case incident location did not resolve inside its own discharge terminal geofence")
	}

	a := geospatial.Fix{Coordinate: geospatial.Coordinate{Lat: 50.90, Lon: -3.50}, Tick: 0}
	b := geospatial.Fix{Coordinate: incidentLocation, Tick: 3600} // one simulated hour
	knots, err := geospatial.ImpliedSpeedKnots(a, b, 1.0)
	if err != nil {
		return err
	}
	gr.VesselImpliedKnotsHundredths = int64(knots*100 + 0.5)
	gr.VesselSpeedPlausible = !geospatial.IsImpossibleSpeed(knots)
	return nil
}

// ---- 2. Party relationships ----

func (gr *GoldenResult) attachRelationships() error {
	reg, err := party.NewRelationshipRegistry(string(gr.CaseID))
	if err != nil {
		return err
	}
	rel, err := party.New("REL-GOLDEN-BROKER", string(gr.CaseID),
		"PTY-002-BROKER", "PTY-002-CARGO-OWNER", party.RoleBroker, 0)
	if err != nil {
		return err
	}
	if err := reg.Register(rel); err != nil {
		return err
	}
	if err := reg.AddProvenance("REL-GOLDEN-BROKER", gr.Built.ID("POLICY")); err != nil {
		return err
	}
	if err := reg.RecordConsent("REL-GOLDEN-BROKER", gr.Built.ID("NOTICE_EMAIL")); err != nil {
		return err
	}
	if err := reg.GrantPermissions("REL-GOLDEN-BROKER",
		party.PermissionSubmitClaim, party.PermissionViewEvidence, party.PermissionReceiveNotice,
		// AccessCaseRoom, added for Round 5's own required end-to-end
		// chain (§28: "... -> Case Room -> Dossier -> Cold Replay"): a
		// broker who may submit claims and view evidence plausibly also
		// needs the case room itself, and pkg/insurance/caseroom's own
		// authorization layer (built this round) needs a real,
		// already-registered relationship that genuinely holds this
		// permission to prove against, rather than a purpose-built
		// fixture disconnected from the golden case's own narrative.
		party.PermissionAccessCaseRoom); err != nil {
		return err
	}
	if !reg.EffectiveAt("REL-GOLDEN-BROKER", 100) {
		return fmt.Errorf("golden case broker relationship should be effective after consent, was not")
	}
	gr.Relationships = reg
	gr.BrokerRelationshipID = "REL-GOLDEN-BROKER"
	return nil
}

// ---- 3. Salvage, genuinely reducing quantum ----

func (gr *GoldenResult) attachSalvage() error {
	reg, err := salvage.NewRegistry(string(gr.CaseID))
	if err != nil {
		return err
	}
	op, err := salvage.New("SLV-GOLDEN-1", string(gr.CaseID), "CLM-"+string(gr.CaseID),
		"partially spoiled produce cargo, FIC-CU-4471203")
	if err != nil {
		return err
	}
	if err := reg.Register(op); err != nil {
		return err
	}
	if err := reg.AddEvidence("SLV-GOLDEN-1", gr.Built.ID("SALVAGE_SALE")); err != nil {
		return err
	}
	if err := reg.EngageContractor("SLV-GOLDEN-1", "PTY-002-SALVOR", 700); err != nil {
		return err
	}
	proceeds := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(8_500), gr.Built.ID("SALVAGE_SALE"))
	expenses := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(900), gr.Built.ID("SALVAGE_SALE"))
	if err := reg.RecordDisposal("SLV-GOLDEN-1", salvage.DisposalSold, proceeds, expenses, 720); err != nil {
		return err
	}
	gr.SalvageRegistry = reg
	gr.SalvageOperationID = "SLV-GOLDEN-1"

	netValue := reg.TotalNetValueForClaim("CLM-" + string(gr.CaseID))
	gr.SalvageNetValue = netValue

	// Genuine recomputation: the SAME quantum.Compute pure function
	// ComputeQuantum itself calls (pkg/insurance/quantum/quantum.go),
	// called here directly with the base case's own recorded inputs but
	// with the Salvage operand replaced twice: once with ZERO (isolating
	// what the figure would be with no salvage credited at all) and once
	// with the golden case's own salvage.Registry net value — computed
	// moments ago from a REAL Operation this file just registered,
	// evidenced, and disposed, not from the base case's own pre-baked
	// scenario figure (CASE-INS-002's quantumInputFor already assumes a
	// 31,000 salvage credit as PART OF ITS OWN NARRATIVE; substituting
	// that unchanged would prove nothing new). The delta between these
	// two recomputations is exactly this round's own SalvageNetValue —
	// see TestGoldenSalvageGenuinelyReducesQuantum — which is what
	// proves the salvage SUBSYSTEM built this round genuinely drives the
	// figure, not merely that a number labelled "salvage" sits beside it.
	without := gr.QuantumInput
	without.CalculationID = "QC-GOLDEN-WITHOUT-SALVAGE"
	without.Salvage = quantum.EvidenceBackedAmount{}
	withoutCalc, err := quantum.Compute(without)
	if err != nil {
		return fmt.Errorf("recomputing without salvage: %w", err)
	}
	gr.QuantumWithoutSalvage = withoutCalc

	with := gr.QuantumInput
	with.CalculationID = "QC-GOLDEN-WITH-SALVAGE"
	with.Salvage = netValue
	withCalc, err := quantum.Compute(with)
	if err != nil {
		return fmt.Errorf("recomputing with salvage: %w", err)
	}
	gr.QuantumWithSalvage = withCalc

	if err := reg.MarkAllocated("SLV-GOLDEN-1"); err != nil {
		return err
	}
	return nil
}

// ---- 4. Co-insurance / reinsurance allocation ----

func (gr *GoldenResult) attachParticipation() error {
	v := gr.PolicyVersion
	v.Participants = []policy.Participant{
		{PartyID: "PTY-002-CO-INSURER", Role: policy.ParticipantCoInsurer, BasisPoints: 30_000},
		{PartyID: "PTY-002-REINSURER", Role: policy.ParticipantReinsurer, BasisPoints: 40_000, Basis: policy.BasisTreaty},
	}
	gr.PolicyVersionWithParticipants = v

	payment := gr.QuantumWithSalvage.IndicativeClaimValue
	if payment <= 0 {
		return fmt.Errorf("golden case indicative claim value must be positive to allocate, got %s", payment)
	}

	coAlloc, err := v.AllocateCoInsurance(payment)
	if err != nil {
		return err
	}
	var sum quantum.Amount
	for _, a := range coAlloc {
		sum += a.Amount
	}
	if sum != payment {
		return fmt.Errorf("co-insurance allocations summed to %s, expected exactly %s", sum, payment)
	}
	gr.CoInsuranceAllocation = coAlloc

	var insurerPrimary quantum.Amount
	for _, a := range coAlloc {
		if a.Role == policy.AllocationRoleInsurerPrimary {
			insurerPrimary = a.Amount
		}
	}
	if insurerPrimary <= 0 {
		return fmt.Errorf("golden case insurer primary share must be positive, got %s", insurerPrimary)
	}
	reAlloc, err := v.AllocateReinsurance(insurerPrimary)
	if err != nil {
		return err
	}
	var reSum quantum.Amount
	for _, a := range reAlloc {
		reSum += a.Amount
	}
	if reSum != insurerPrimary {
		return fmt.Errorf("reinsurance allocations summed to %s, expected exactly %s", reSum, insurerPrimary)
	}
	gr.ReinsuranceAllocation = reAlloc
	return nil
}

// ---- 5. Dispute ----

func (gr *GoldenResult) attachDispute() error {
	forum := dispute.Forum{
		GoverningLaw: "the law of Jurisdiction A", Jurisdiction: "Jurisdiction A courts",
		SourceDocument: "POL-" + string(gr.CaseID), SourceClause: "cl. 7.2", SourceVersion: "V1",
	}
	m, err := dispute.NewMatter("DIS-GOLDEN-1", string(gr.CaseID), "CLM-"+string(gr.CaseID), forum, 800)
	if err != nil {
		return err
	}
	issue, err := dispute.NewIssue("ISS-GOLDEN-1",
		"Was notice of the temperature excursion given within the policy's cl. 7.2 window?")
	if err != nil {
		return err
	}
	if err := m.AddIssue(issue); err != nil {
		return err
	}
	if err := m.RecordPosition("ISS-GOLDEN-1", dispute.Position{
		Party: "PTY-002-CARGO-OWNER", Contention: "Notice was given as soon as the excursion was discovered on discharge.",
		ReliedOnEvidence: []string{gr.Built.ID("NOTICE_EMAIL")}, ReliedOnClauses: []string{"cl. 7.2"},
		RecordedAtTick: 800,
	}); err != nil {
		return err
	}
	if err := m.RecordPosition("ISS-GOLDEN-1", dispute.Position{
		Party: "PTY-002-INSURER", Contention: "The secondary temperature log shows the excursion was detectable earlier than the discovery date claimed.",
		ReliedOnEvidence: []string{gr.Built.ID("TEMP_LOG_SECONDARY")}, ReliedOnClauses: []string{"cl. 7.2"},
		RecordedAtTick: 800,
	}); err != nil {
		return err
	}
	if err := m.AddIssueEvidence("ISS-GOLDEN-1",
		[]string{gr.Built.ID("NOTICE_EMAIL")}, []string{gr.Built.ID("TEMP_LOG_SECONDARY")}, nil); err != nil {
		return err
	}
	gr.DisputeMatter = m
	return nil
}

// ---- 6. Reserve (Round 8) ----
//
// Reserve closes the gap Round 7's Insurance System Completeness Audit
// found: no claim reserve concept existed anywhere in pkg/insurance.
// The initial reserve is set from the SAME QuantumWithSalvage figure
// attachSalvage just computed (never a fresh, disconnected number),
// then approved by a DIFFERENT party than the one who proposed it —
// exercising the package's segregation-of-duties rule for real, not
// merely proving it in reserve's own standalone unit tests.
func (gr *GoldenResult) attachReserve() error {
	r, err := reserve.New("RSV-GOLDEN-1", "CLM-"+string(gr.CaseID), string(gr.CaseID),
		gr.QuantumWithSalvage.IndicativeClaimValue, "PTY-002-INSURER", party.RoleInsurer,
		"initial reserve set from quantum-with-salvage indicative claim value", 900)
	if err != nil {
		return err
	}
	if err := r.Approve("PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, 910); err != nil {
		return err
	}
	gr.Reserve = r
	return nil
}

// ---- 7. Recovery / subrogation (Round 8) ----
//
// The base Drive() path already calls Facade.AnalyzeRecovery on every
// case (drive.go), but always with nil targets — a real mechanism
// exercised with zero content. This registers one REAL Target: the
// carrier, pursued on a bailee-liability theory for the same
// temperature-excursion evidence attachDispute already cites, proving
// the recovery/subrogation domain actually operates end to end on this
// case rather than merely running an empty loop.
func (gr *GoldenResult) attachRecovery() error {
	reg, err := recovery.NewRegistry(string(gr.CaseID))
	if err != nil {
		return err
	}
	basis := recovery.Basis{
		Category: recovery.BasisBaileeLiability,
		Detail:   "carrier held the cargo as bailee during the temperature-excursion custody interval per the secondary temperature log",
	}
	loss := recovery.Money{AmountMinor: int64(gr.QuantumWithSalvage.IndicativeClaimValue), Currency: "USD"}
	target, err := recovery.New("RCV-GOLDEN-1", string(gr.CaseID), "PTY-002-CARRIER", basis, loss)
	if err != nil {
		return err
	}
	if err := reg.Register(target); err != nil {
		return err
	}
	if err := reg.AddSupportingEvidence("RCV-GOLDEN-1", gr.Built.ID("TEMP_LOG_SECONDARY")); err != nil {
		return err
	}
	if err := reg.SetNoticeStatus("RCV-GOLDEN-1", recovery.NoticeStatusSent); err != nil {
		return err
	}
	if err := reg.SetLimitationDeadline("RCV-GOLDEN-1", 5000); err != nil {
		return err
	}
	if _, err := reg.RefreshLimitationStatus("RCV-GOLDEN-1", 900); err != nil {
		return err
	}
	if err := reg.SetRecoveryStatus("RCV-GOLDEN-1", recovery.RecoveryStatusPursuing); err != nil {
		return err
	}
	gr.RecoveryRegistry = reg
	gr.RecoveryTargetID = "RCV-GOLDEN-1"
	return nil
}

// ---- 8. Regulatory (Round 8) ----
//
// pkg/insurance/regulatory had zero callers anywhere in this
// repository before this round — a genuinely unintegrated package,
// unlike recovery (wired but empty) or gap (already genuinely
// integrated via Facade.ComputeGapAssessment). This opens one matter,
// alleges a reefer-temperature-logging failure, advances it through a
// real investigation, and records a NOT_PROVEN regulatory finding —
// exercising RecordFinding's own settlement-cannot-prove rule against
// this exact case for the first time anywhere in the codebase.
func (gr *GoldenResult) attachRegulatory() error {
	m, err := regulatory.NewMatter("REG-GOLDEN-1", string(gr.CaseID), "Port State Control Authority", "Jurisdiction A", 950)
	if err != nil {
		return err
	}
	alleg, err := regulatory.NewAllegation("ALG-GOLDEN-1", "alleged failure to maintain reefer temperature logs per regulation")
	if err != nil {
		return err
	}
	if err := m.AddAllegation(alleg); err != nil {
		return err
	}
	if err := m.Advance(regulatory.StageInvestigation, "authority opened investigation", 960); err != nil {
		return err
	}
	if err := m.RecordFinding("ALG-GOLDEN-1", regulatory.FindingRegulatory, regulatory.ResultNotProven,
		"Port State Control Authority", "PSC-FINDING-2026-014"); err != nil {
		return err
	}
	if err := m.Advance(regulatory.StageClosedNoAction, "no violation found; matter closed", 970); err != nil {
		return err
	}
	gr.RegulatoryMatter = m
	return nil
}

// ---- 9. Payment lifecycle (Round 9) ----
//
// Closes gap G1 ("Payment Lifecycle"): pkg/insurance/policy already
// computes WHO gets paid HOW MUCH (attachParticipation's own
// CoInsuranceAllocation), but nothing tracked whether that amount was
// ever authorized, instructed, or paid. This drives the named
// insurer's own primary allocation share through the FULL lifecycle —
// create, authorize (by a different party — segregation of duties),
// instruct (by a party with EXECUTION authority — the second,
// disjoint segregation), settle — linking the resulting PaymentRecord
// back to the SAME allocation and quantum figure by reference, never a
// second computed amount.
func (gr *GoldenResult) attachPayment() error {
	var insurerPrimary policy.Allocation
	found := false
	for _, a := range gr.CoInsuranceAllocation {
		if a.Role == policy.AllocationRoleInsurerPrimary {
			insurerPrimary = a
			found = true
		}
	}
	if !found {
		return fmt.Errorf("golden case has no insurer primary allocation to pay")
	}

	p, err := payment.New("PAY-GOLDEN-1", "CLM-"+string(gr.CaseID), string(gr.CaseID),
		"PTY-002-INSURER", insurerPrimary.Amount, "IDEM-GOLDEN-1",
		"PTY-002-CLAIMS-HANDLER", "payment created from insurer primary co-insurance allocation", 1000)
	if err != nil {
		return err
	}
	p.LinkAllocation(string(insurerPrimary.Role), insurerPrimary.PartyID)
	p.LinkQuantum(gr.QuantumWithSalvage.CalculationID)

	if _, err := p.Authorize("PTY-002-INSURER", party.RoleInsurer, "authorized for payment to co-insurer", 1010); err != nil {
		return err
	}
	if _, err := p.Instruct("PTY-002-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-GOLDEN-1", 1020); err != nil {
		return err
	}
	if err := p.Settle("PTY-002-BANK", "confirmed by SWIFT acknowledgement", 1030); err != nil {
		return err
	}

	rec := p.Reconcile(insurerPrimary.Amount)
	if rec.Adequacy != payment.AdequacyExact {
		return fmt.Errorf("golden case payment did not reconcile exactly against its own allocation: %s", rec.Adequacy)
	}
	gr.Payment = p
	return nil
}

// ---- 10. Unified audit (Round 9) ----
//
// Closes gap G2 ("Unified Audit"): mirrors this case's own lifecycle
// trail (gr.Facade.Case().StateLog(), the same data
// gr.Dossier.LifecycleAuditTrail was built from), the golden payment's
// history, and the golden reserve's history into ONE shared
// pkg/platform/audit.AuditStore via pkg/insurance/auditlink, then
// records the resulting root hash on the Dossier — proof the
// dossier's own audit trail and the platform's shared ledger are one
// verifiable chain, not two independent truths.
func (gr *GoldenResult) attachUnifiedAudit() error {
	store := audit.NewAuditStore()
	if _, err := auditlink.MirrorCase(store, gr.Facade.Case(), string(gr.CaseID)); err != nil {
		return err
	}
	if gr.Payment != nil {
		if _, err := auditlink.MirrorPaymentHistory(store, gr.Payment); err != nil {
			return err
		}
	}
	if gr.Reserve != nil {
		if _, err := auditlink.MirrorReserveHistory(store, gr.Reserve); err != nil {
			return err
		}
	}
	if gr.Lifecycle != nil {
		if _, err := auditlink.MirrorLifecycleHistory(store, gr.Lifecycle); err != nil {
			return err
		}
	}
	events, err := auditlink.VerifyCanonicalAuthority(store)
	if err != nil {
		return fmt.Errorf("unified audit ledger failed its own canonical-authority verification: %w", err)
	}
	if len(events) != len(store.Snapshot()) {
		return fmt.Errorf("reconstructed %d canonical events but the ledger holds %d records", len(events), len(store.Snapshot()))
	}
	gr.AuditStore = store
	if gr.Dossier != nil {
		gr.Dossier.SetUnifiedAudit(store.RootHash(), len(store.Snapshot()))
	}
	return nil
}

// ---- 11. Canonical case state machine (Round 10) ----
//
// Closes this program's own Round 10 self-review "next hidden gap":
// drives a REAL pkg/insurance/casestate.CaseLifecycle for the golden
// case all the way from INVITED to CLOSED (via RECOVERY_OPEN ->
// RECOVERY_RESOLVED), using the SAME insurer-primary co-insurance
// allocation amount attachPayment pays — proving the independently-
// tested state machine reaches a real RESERVED/PAYMENT_AUTHORIZED/
// PAYMENT_EXECUTED outcome on THIS repository's own cross-domain case,
// not only on the package's own synthetic unit-test fixtures.
func (gr *GoldenResult) attachLifecycle() error {
	var insurerPrimary policy.Allocation
	found := false
	for _, a := range gr.CoInsuranceAllocation {
		if a.Role == policy.AllocationRoleInsurerPrimary {
			insurerPrimary = a
			found = true
		}
	}
	if !found {
		return fmt.Errorf("golden case has no insurer primary allocation for the lifecycle machine to pay")
	}

	cl, err := casestate.New(string(gr.CaseID) + "-LIFECYCLE")
	if err != nil {
		return err
	}
	if _, err := cl.Transition(casestate.StateAccepted, "PTY-002-BROKER", party.RoleBroker, "", "IDEM-LC-1", "broker accepted case invitation", 1100); err != nil {
		return err
	}
	if _, err := cl.Transition(casestate.StateEvidenceExchanged, "PTY-002-BROKER", party.RoleBroker, gr.Built.ID("NOTICE_EMAIL"), "IDEM-LC-2", "evidence exchanged", 1110); err != nil {
		return err
	}
	if _, err := cl.Transition(casestate.StateUnderReview, "PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, "", "IDEM-LC-3", "under review", 1120); err != nil {
		return err
	}
	if _, err := cl.Quantify(gr.QuantumWithSalvage, "PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, "IDEM-LC-4", 1130); err != nil {
		return err
	}
	if _, err := cl.OpenReserve("RSV-LC-GOLDEN-1", "CLM-"+string(gr.CaseID), insurerPrimary.Amount,
		"PTY-002-INSURER", party.RoleInsurer, "reserve opened from insurer primary allocation", "IDEM-LC-5", 1140); err != nil {
		return err
	}
	if err := cl.ApproveReserve("PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, 1145); err != nil {
		return err
	}
	if _, err := cl.AuthorizePayment("PAY-LC-GOLDEN-1", "CLM-"+string(gr.CaseID), "PTY-002-INSURER", insurerPrimary.Amount, "IDEM-LC-6",
		"PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, "PTY-002-INSURER", party.RoleInsurer,
		"payment authorized against approved reserve", 1150); err != nil {
		return err
	}
	if _, err := cl.ExecutePayment("PTY-002-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-LC-GOLDEN-1", "IDEM-LC-7", 1160); err != nil {
		return err
	}
	if _, err := cl.Transition(casestate.StateRecoveryOpen, "PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, "", "IDEM-LC-8", "recovery opened against carrier", 1170); err != nil {
		return err
	}
	if _, err := cl.Transition(casestate.StateRecoveryResolved, "PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, "", "IDEM-LC-9", "recovery resolved", 1180); err != nil {
		return err
	}
	if _, err := cl.Transition(casestate.StateClosed, "PTY-002-CLAIMS-HANDLER", party.RoleClaimsHandler, "", "IDEM-LC-10", "case closed", 1190); err != nil {
		return err
	}

	// Replay determinism: reconstruct purely from History() and confirm
	// it reproduces the identical end state and history length — the
	// same property GoldenColdReplay proves for the rest of the case,
	// applied here to the lifecycle machine's own log.
	replayed, err := casestate.Replay(cl.CaseID, cl.History())
	if err != nil {
		return fmt.Errorf("lifecycle replay diverged: %w", err)
	}
	if replayed.State() != cl.State() || len(replayed.History()) != len(cl.History()) {
		return fmt.Errorf("lifecycle replay produced a different end state or history length")
	}

	gr.Lifecycle = cl
	return nil
}

// ---- Cold replay of the Golden Case ----

// GoldenColdReplay proves the Master Closure Mandate's own P0 §37
// ("Golden Case Cold Replay"): export a canonical snapshot of the
// underlying case, discard the original, reconstruct, replay, and
// compare authoritative outputs. Reuses ColdReplay's own machinery over
// CASE-INS-002 (the base case DriveGolden extends) for the base result,
// then re-runs the golden layer's own deterministic computations
// (salvage net value, quantum-with-salvage, allocations) from scratch
// and compares them to the ORIGINAL golden run's outputs, exactly the
// property TestDriveGoldenIsDeterministic already checks between two
// live runs — this is the same check with one leg cold: the base
// case's replay leg genuinely never touches the original in-memory
// Case (see ColdReplay itself), and the golden layer above it is pure
// functions of that replayed result plus fixed, hard-coded golden-scenario
// data, so re-running it after a cold replay is exactly as deterministic
// as running it twice live.
func GoldenColdReplay() (live *GoldenResult, coldReport ColdReplayReport, err error) {
	c, err := Get(CaseCargoDamageReefer)
	if err != nil {
		return nil, ColdReplayReport{}, err
	}
	liveBase, replayedBase, report, err := ColdReplay(c)
	if err != nil {
		return nil, ColdReplayReport{}, err
	}
	_ = liveBase
	if !report.Pass() {
		return nil, report, fmt.Errorf("casepack: golden cold replay: base case cold replay failed: %v", report.Failures)
	}

	live = &GoldenResult{Result: replayedBase}
	if err := live.attachGeospatial(); err != nil {
		return nil, report, err
	}
	if err := live.attachRelationships(); err != nil {
		return nil, report, err
	}
	if err := live.attachSalvage(); err != nil {
		return nil, report, err
	}
	if err := live.attachParticipation(); err != nil {
		return nil, report, err
	}
	if err := live.attachDispute(); err != nil {
		return nil, report, err
	}
	if err := live.attachReserve(); err != nil {
		return nil, report, err
	}
	if err := live.attachRecovery(); err != nil {
		return nil, report, err
	}
	if err := live.attachRegulatory(); err != nil {
		return nil, report, err
	}
	if err := live.attachPayment(); err != nil {
		return nil, report, err
	}
	if err := live.attachLifecycle(); err != nil {
		return nil, report, err
	}
	if err := live.attachUnifiedAudit(); err != nil {
		return nil, report, err
	}
	return live, report, nil
}

// ---- Readiness-gate evidence ----

// GoldenAssuranceSummary is the derived evidence artifact for the
// mandate's own "Gate 5 — Cross-Domain Golden Case": every claim this
// file makes, checked again here so a caller (cmd/veriqo-readiness)
// gets one aggregated verdict rather than re-deriving it from
// GoldenResult's raw fields. Pass is derived from Failures — the same
// no-settable-verdict discipline every gate in this domain uses.
type GoldenAssuranceSummary struct {
	Ran      bool     `json:"ran"`
	Failures []string `json:"failures,omitempty"`

	EvidenceRootHash               string `json:"evidence_root_hash,omitempty"`
	IncidentZoneID                 string `json:"incident_zone_id,omitempty"`
	VesselSpeedPlausible           bool   `json:"vessel_speed_plausible"`
	BrokerRelationshipActive       bool   `json:"broker_relationship_active"`
	SalvageNetValue                string `json:"salvage_net_value,omitempty"`
	QuantumReducedBySalvageExactly bool   `json:"quantum_reduced_by_salvage_exactly"`
	CoInsuranceAllocationExact     bool   `json:"co_insurance_allocation_exact"`
	ReinsuranceAllocationExact     bool   `json:"reinsurance_allocation_exact"`
	DisputeBothPositionsRecorded   bool   `json:"dispute_both_positions_recorded"`

	// ---- Round 8 additions ----
	ReserveAuthorized           bool `json:"reserve_authorized"`
	ReserveReconciliationExact  bool `json:"reserve_reconciliation_exact"`
	RecoveryTargetRegistered    bool `json:"recovery_target_registered"`
	RegulatoryFindingRecorded   bool `json:"regulatory_finding_recorded"`
	EvidenceSufficiencyAssessed bool `json:"evidence_sufficiency_assessed"`

	// ---- Round 9 additions ----
	PaymentSettledAndReconciled bool `json:"payment_settled_and_reconciled"`
	AuditUnified                bool `json:"audit_unified"`

	// ---- Round 10 additions ----
	CaseLifecycleGoverned bool `json:"case_lifecycle_governed"`

	ColdReplayMatches bool `json:"cold_replay_matches"`
}

// Pass is derived from Failures.
func (s GoldenAssuranceSummary) Pass() bool { return s.Ran && len(s.Failures) == 0 }

// RunGoldenAssurance drives the Golden Case live, cold-replays it, and
// checks every cross-domain claim this file makes. Never panics: any
// failure is reported as a named entry in Failures, matching
// RunAssurance's own discipline for the seven-case pack.
func RunGoldenAssurance() GoldenAssuranceSummary {
	s := GoldenAssuranceSummary{}
	live, err := DriveGolden()
	if err != nil {
		s.Failures = append(s.Failures, fmt.Sprintf("DriveGolden failed outright: %v", err))
		return s
	}
	s.Ran = true
	s.EvidenceRootHash = live.Manifest.EvidenceRootHash

	if len(live.IncidentZones) == 0 {
		s.Failures = append(s.Failures, "incident location did not resolve inside any geofence")
	} else {
		s.IncidentZoneID = live.IncidentZones[0].ID
	}
	s.VesselSpeedPlausible = live.VesselSpeedPlausible
	if !s.VesselSpeedPlausible {
		s.Failures = append(s.Failures, "vessel track implied an impossible speed")
	}

	s.BrokerRelationshipActive = live.Relationships.EffectiveAt(live.BrokerRelationshipID, 500)
	if !s.BrokerRelationshipActive {
		s.Failures = append(s.Failures, "broker relationship was not effective")
	}

	s.SalvageNetValue = live.SalvageNetValue.Amount.String()
	diff := live.QuantumWithoutSalvage.IndicativeClaimValue - live.QuantumWithSalvage.IndicativeClaimValue
	s.QuantumReducedBySalvageExactly = diff == live.SalvageNetValue.Amount
	if !s.QuantumReducedBySalvageExactly {
		s.Failures = append(s.Failures, fmt.Sprintf(
			"quantum did not drop by exactly the salvage net value: diff=%s salvageNet=%s",
			diff, live.SalvageNetValue.Amount))
	}

	var coSum, insurerPrimary quantum.Amount
	for _, a := range live.CoInsuranceAllocation {
		coSum += a.Amount
		if a.Role == policy.AllocationRoleInsurerPrimary {
			insurerPrimary = a.Amount
		}
	}
	s.CoInsuranceAllocationExact = coSum == live.QuantumWithSalvage.IndicativeClaimValue
	if !s.CoInsuranceAllocationExact {
		s.Failures = append(s.Failures, fmt.Sprintf(
			"co-insurance allocations summed to %s, expected %s", coSum, live.QuantumWithSalvage.IndicativeClaimValue))
	}
	var reSum quantum.Amount
	for _, a := range live.ReinsuranceAllocation {
		reSum += a.Amount
	}
	s.ReinsuranceAllocationExact = reSum == insurerPrimary
	if !s.ReinsuranceAllocationExact {
		s.Failures = append(s.Failures, fmt.Sprintf(
			"reinsurance allocations summed to %s, expected insurer primary share %s", reSum, insurerPrimary))
	}

	if issue, ok := live.DisputeMatter.Issue("ISS-GOLDEN-1"); ok {
		s.DisputeBothPositionsRecorded = len(issue.Positions) == 2 &&
			len(issue.SupportingEvidence) > 0 && len(issue.ContradictingEvidence) > 0
	}
	if !s.DisputeBothPositionsRecorded {
		s.Failures = append(s.Failures, "dispute issue did not record both parties' positions with supporting and contradicting evidence")
	}

	s.ReserveAuthorized = live.Reserve != nil && live.Reserve.Status() == reserve.StatusApproved
	if !s.ReserveAuthorized {
		s.Failures = append(s.Failures, "reserve was not approved")
	}
	if live.Reserve != nil {
		rec := live.Reserve.Reconcile(live.QuantumWithSalvage.IndicativeClaimValue)
		s.ReserveReconciliationExact = rec.Adequacy == reserve.AdequacyAdequate
		if !s.ReserveReconciliationExact {
			s.Failures = append(s.Failures, fmt.Sprintf(
				"reserve reconciliation against its own founding quantum figure was not exact: %s", rec.Adequacy))
		}
	}

	s.RecoveryTargetRegistered = live.RecoveryRegistry != nil && live.RecoveryRegistry.Count() == 1
	if !s.RecoveryTargetRegistered {
		s.Failures = append(s.Failures, "recovery target was not registered")
	}

	s.RegulatoryFindingRecorded = live.RegulatoryMatter != nil &&
		live.RegulatoryMatter.Stage() == regulatory.StageClosedNoAction &&
		len(live.RegulatoryMatter.Allegations()) == 1
	if !s.RegulatoryFindingRecorded {
		s.Failures = append(s.Failures, "regulatory matter did not reach a recorded finding and closure")
	}

	s.EvidenceSufficiencyAssessed = live.Dossier != nil && len(live.Dossier.EvidenceSufficiency) > 0
	if !s.EvidenceSufficiencyAssessed {
		s.Failures = append(s.Failures, "evidence sufficiency (gap package) was not assessed for this case")
	}

	if live.Payment != nil {
		var insurerPrimary quantum.Amount
		for _, a := range live.CoInsuranceAllocation {
			if a.Role == policy.AllocationRoleInsurerPrimary {
				insurerPrimary = a.Amount
			}
		}
		rec := live.Payment.Reconcile(insurerPrimary)
		s.PaymentSettledAndReconciled = live.Payment.Status() == payment.StatusPaid &&
			rec.Adequacy == payment.AdequacyExact &&
			live.Payment.AllocationPartyID != "" && live.Payment.QuantumCalculationID != ""
	}
	if !s.PaymentSettledAndReconciled {
		s.Failures = append(s.Failures, "golden payment was not settled, linked, and exactly reconciled against its own allocation")
	}

	s.AuditUnified = live.Dossier != nil && live.Dossier.AuditUnified
	if !s.AuditUnified {
		s.Failures = append(s.Failures, "case audit trail and platform ledger were not unified into one verifiable chain")
	}

	s.CaseLifecycleGoverned = live.Lifecycle != nil &&
		live.Lifecycle.State() == casestate.StateClosed &&
		live.Lifecycle.Reserve != nil && live.Lifecycle.Reserve.Status() == reserve.StatusApproved &&
		live.Lifecycle.Payment != nil && live.Lifecycle.Payment.Status() == payment.StatusPaid
	if !s.CaseLifecycleGoverned {
		s.Failures = append(s.Failures, "case lifecycle state machine did not reach CLOSED with a genuinely approved reserve and paid payment")
	}

	replayed, coldReport, err := GoldenColdReplay()
	if err != nil {
		s.Failures = append(s.Failures, fmt.Sprintf("GoldenColdReplay failed outright: %v", err))
	} else if !coldReport.Pass() {
		s.Failures = append(s.Failures, fmt.Sprintf("base cold replay did not pass: %v", coldReport.Failures))
	} else {
		reserveMatches := live.Reserve != nil && replayed.Reserve != nil &&
			live.Reserve.CurrentAmount() == replayed.Reserve.CurrentAmount() &&
			live.Reserve.Status() == replayed.Reserve.Status() &&
			len(live.Reserve.History()) == len(replayed.Reserve.History())
		recoveryMatches := live.RecoveryRegistry != nil && replayed.RecoveryRegistry != nil &&
			live.RecoveryRegistry.Count() == replayed.RecoveryRegistry.Count()
		regulatoryMatches := live.RegulatoryMatter != nil && replayed.RegulatoryMatter != nil &&
			live.RegulatoryMatter.Stage() == replayed.RegulatoryMatter.Stage() &&
			len(live.RegulatoryMatter.Allegations()) == len(replayed.RegulatoryMatter.Allegations())
		paymentMatches := live.Payment != nil && replayed.Payment != nil &&
			live.Payment.CurrentAmount() == replayed.Payment.CurrentAmount() &&
			live.Payment.Status() == replayed.Payment.Status() &&
			len(live.Payment.History()) == len(replayed.Payment.History())
		// AuditUnified (a boolean) matching, not the root hash itself:
		// the two runs mirror into two DIFFERENT AuditStore instances
		// (each with its own fresh hash chain starting from empty), so
		// their root hashes are expected to differ in VALUE while both
		// independently verify and both cover the same event COUNT —
		// checked here — never the same literal hash.
		auditMatches := live.Dossier != nil && replayed.Dossier != nil &&
			live.Dossier.AuditUnified && replayed.Dossier.AuditUnified &&
			live.Dossier.CanonicalAuditEventCount == replayed.Dossier.CanonicalAuditEventCount
		lifecycleMatches := live.Lifecycle != nil && replayed.Lifecycle != nil &&
			live.Lifecycle.State() == replayed.Lifecycle.State() &&
			len(live.Lifecycle.History()) == len(replayed.Lifecycle.History())

		s.ColdReplayMatches = live.Manifest.EvidenceRootHash == replayed.Manifest.EvidenceRootHash &&
			live.QuantumWithSalvage.IndicativeClaimValue == replayed.QuantumWithSalvage.IndicativeClaimValue &&
			reserveMatches && recoveryMatches && regulatoryMatches && paymentMatches && auditMatches && lifecycleMatches
		if !s.ColdReplayMatches {
			s.Failures = append(s.Failures, fmt.Sprintf(
				"cold-replayed golden case diverged from the live run (reserve=%v recovery=%v regulatory=%v payment=%v audit=%v lifecycle=%v)",
				reserveMatches, recoveryMatches, regulatoryMatches, paymentMatches, auditMatches, lifecycleMatches))
		}
	}

	return s
}
