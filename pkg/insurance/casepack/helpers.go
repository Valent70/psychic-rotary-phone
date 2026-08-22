package casepack

import (
	"fmt"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claim"
	"veriqo/pkg/insurance/deadline"
	"veriqo/pkg/insurance/dossier"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/obligation"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/timeline"
	"veriqo/pkg/insurance/verification"
)

// This file holds the per-case wiring Drive needs. Each function is a
// small, total switch over the seven case IDs: every case gets a real,
// case-appropriate value, and the default arm is a genuine fallback
// rather than a panic, so adding an eighth case cannot silently produce
// a half-driven run.

// claimTypeFor maps each synthetic case to a real registered claim type
// from the claim package's own seed list. Nothing invents a type.
func claimTypeFor(id CaseID) claim.Type {
	switch id {
	case CasePortCallDemurrage:
		return claim.TypeDemurrage
	case CaseCargoDamageReefer:
		return claim.TypeReeferFailure
	case CaseCommodityDocuments:
		return claim.TypeCargoShortage
	case CaseGeneralAverage:
		return claim.TypeGeneralAverage
	case CaseBriberyRisk:
		return TypeCounterpartyRiskReview
	case CaseRegulatorySettlement:
		return TypeRegulatoryMatter
	case CaseCrossBorderDispute:
		return claim.TypeCargoDamage
	default:
		return claim.TypeCargoDamage
	}
}

// Two of the seven cases are not maritime casualty claims, and the
// claim registry's seed list — correctly — contains no casualty type
// that describes them. Rather than mislabel a regulatory matter as, say,
// a DELAY claim, the pack registers two additional types.
//
// This is exactly the extensibility the claim package was built for:
// its own doc comment says "Jangan hard-code hanya cargo damage. Buat
// extensible registry", and TypeDefinition is data registered at
// runtime precisely so a deployment can add the types it actually has.
const (
	TypeCounterpartyRiskReview claim.Type = "COUNTERPARTY_RISK_REVIEW"
	TypeRegulatoryMatter       claim.Type = "REGULATORY_MATTER"
)

// PackClaimTypes returns the additional TypeDefinitions this pack needs
// beyond claim.DefaultTypes().
func PackClaimTypes() []claim.TypeDefinition {
	return []claim.TypeDefinition{
		{
			Type:             TypeCounterpartyRiskReview,
			RequiredEvidence: []string{"engagement_letter", "due_diligence_record", "payment_instruction"},
			OptionalEvidence: []string{"benchmark_record", "correspondence"},
			PolicyQuestions: []string{
				"Has enhanced due diligence been completed and recorded?",
				"Has the beneficiary's ownership been independently verified?",
				"Which authority must approve or refuse this payment?",
			},
		},
		{
			Type:             TypeRegulatoryMatter,
			RequiredEvidence: []string{"regulatory_notice", "regulatory_record"},
			OptionalEvidence: []string{"regulatory_finding", "settlement_order"},
			PolicyQuestions: []string{
				"Which allegations, if any, has an authority actually determined?",
				"Is any monitorship obligation outstanding and not certified complete?",
				"Which policy responds, if any, to the recorded monetary outcomes?",
			},
		},
	}
}

// claimWithVersion returns a copy of cl pinned to the resolved policy
// version. The coverage engine cross-checks this, so the pin must be
// the version EffectiveAt resolved rather than whatever was on the claim
// when it was first registered.
func claimWithVersion(cl claim.Claim, versionID string) claim.Claim {
	cl.PolicyVersionID = versionID
	return cl
}

// verificationStatuses is what a reviewer has already determined about
// each record. Independent and regulatory sources are recorded as
// authenticity-supported; party-submitted records stay UNVERIFIED,
// which is the honest default and keeps the gap/coverage engines
// working on a realistic evidence set rather than an all-green one.
func verificationStatuses(built BuiltEvidence) map[string]insevidence.Status {
	out := map[string]insevidence.Status{}
	for _, rec := range built.Records {
		switch rec.Origin {
		case insevidence.OriginIndependent, insevidence.OriginRegulatory, insevidence.OriginSurveyor:
			out[rec.EvidenceID()] = insevidence.StatusAuthenticitySupported
		}
	}
	return out
}

// eventsFor builds each case's timeline events from its own evidence.
// Every event's SourceEvidence is a real content-addressed EvidenceID
// from the case's own built set — never an invented reference.
func eventsFor(c Case, built BuiltEvidence) ([]timeline.Event, error) {
	type spec struct {
		id        string
		kind      timeline.EventType
		utc       uint64
		certainty timeline.Certainty
		location  string
		actor     string
		sources   []string
	}
	var specs []spec

	switch c.ID {
	case CasePortCallDemurrage:
		specs = []spec{
			// The NOR is a notice of READINESS, not a claim notice, so it
			// is typed ARRIVAL rather than NOTICE: typing it NOTICE would
			// make the timeline's notice-before-incident check fire on a
			// document that is not a claim notice at all.
			{"EVT-001-NOR", timeline.TypeArrival, EpochSeconds(1008), timeline.CertaintyConfirmed,
				"Calder Bay, Jurisdiction A", "PTY-001-AGENT", []string{built.ID("NOR")}},
			{"EVT-001-ARRIVAL", timeline.TypeArrival, EpochSeconds(1008) + 360, timeline.CertaintyConfirmed,
				"Calder Bay anchorage", "PTY-001-OWNER", []string{built.ID("AIS_ARRIVAL")}},
			// The two readiness records are the case's REAL contradiction:
			// same event type, 300 seconds apart, from two sources.
			{"EVT-001-SOF-READY", timeline.TypeHandover, EpochSeconds(1013) + 2400, timeline.CertaintyProbable,
				"Calder Bay berth", "PTY-001-OWNER", []string{built.ID("SOF_BERTH_READY")}},
			{"EVT-001-TERMINAL-READY", timeline.TypeHandover, EpochSeconds(1013) + 2100, timeline.CertaintyProbable,
				"Calder Bay berth", "PTY-001-TERMINAL", []string{built.ID("TERMINAL_LOG")}},
		}
	case CaseCargoDamageReefer:
		specs = []spec{
			{"EVT-002-EXCURSION", timeline.TypeIncident, EpochSeconds(1042), timeline.CertaintyProbable,
				"in transit", "PTY-002-CARRIER", []string{built.ID("TEMP_LOG_PRIMARY")}},
			{"EVT-002-DELIVERY", timeline.TypeDelivery, EpochSeconds(1100), timeline.CertaintyConfirmed,
				"Calder Bay Cold Store", "PTY-002-WAREHOUSE", []string{built.ID("POD")}},
			{"EVT-002-DISCOVERY", timeline.TypeDamageDiscovery, EpochSeconds(1102), timeline.CertaintyConfirmed,
				"Calder Bay Cold Store", "PTY-002-CARGO-OWNER", []string{built.ID("DAMAGE_PHOTOS")}},
			{"EVT-002-SURVEY", timeline.TypeSurvey, EpochSeconds(1140), timeline.CertaintyConfirmed,
				"Calder Bay Cold Store", "PTY-002-SURVEYOR", []string{built.ID("SURVEYOR_REPORT")}},
			// The claim notice arrives AFTER the survey, which is the
			// canonical order this package expects — but well after the
			// policy's own 24-tick notice period, which is the case's real
			// point.
			{"EVT-002-CLAIM-NOTICE", timeline.TypeClaim, EpochSeconds(1220), timeline.CertaintyConfirmed,
				"claims desk", "PTY-002-CARGO-OWNER", []string{built.ID("NOTICE_EMAIL")}},
		}
	case CaseCommodityDocuments:
		specs = []spec{
			{"EVT-003-LOADING", timeline.TypeLoading, EpochSeconds(1000), timeline.CertaintyConfirmed,
				"load port, Jurisdiction A", "PTY-003-SELLER", []string{built.ID("BL_QUANTITY")}},
			{"EVT-003-SURVEY", timeline.TypeSurvey, EpochSeconds(1006), timeline.CertaintyConfirmed,
				"load port, Jurisdiction A", "PTY-003-INSPECTOR", []string{built.ID("DRAUGHT_SURVEY")}},
		}
	case CaseGeneralAverage:
		specs = []spec{
			{"EVT-004-DETENTION", timeline.TypeIncident, EpochSeconds(1000), timeline.CertaintyConfirmed,
				"declared high-risk area, Jurisdiction C", "PTY-004-SHIPOWNER", []string{built.ID("DETENTION_RECORD")}},
			{"EVT-004-RELEASE", timeline.TypeVoyage, EpochSeconds(1120), timeline.CertaintyConfirmed,
				"declared high-risk area, Jurisdiction C", "PTY-004-SHIPOWNER", []string{built.ID("RANSOM_PAYMENT")}},
			{"EVT-004-ADJUSTMENT", timeline.TypeSurvey, EpochSeconds(1400), timeline.CertaintyConfirmed,
				"average adjuster's office", "PTY-004-ADJUSTER", []string{built.ID("GA_ADJUSTMENT")}},
		}
	case CaseBriberyRisk:
		specs = []spec{
			{"EVT-005-ENGAGEMENT", timeline.TypeHandover, EpochSeconds(1000), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-005-PRINCIPAL", []string{built.ID("ENGAGEMENT_LETTER")}},
			{"EVT-005-DUE-DILIGENCE", timeline.TypeSurvey, EpochSeconds(1012), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-005-COMPLIANCE", []string{built.ID("FEE_BENCHMARK")}},
			{"EVT-005-PAYMENT-REQUEST", timeline.TypeClaim, EpochSeconds(1020), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-005-INTERMEDIARY", []string{built.ID("PAYMENT_INSTRUCTION")}},
		}
	case CaseRegulatorySettlement:
		specs = []spec{
			{"EVT-006-ALLEGATION", timeline.TypeIncident, EpochSeconds(1000), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-006-AUTHORITY", []string{built.ID("ALLEGATION_NOTICE")}},
			{"EVT-006-INVESTIGATION", timeline.TypeSurvey, EpochSeconds(1500), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-006-AUTHORITY", []string{built.ID("INVESTIGATION_REPORT")}},
			{"EVT-006-FINDING", timeline.TypeSurvey, EpochSeconds(1600), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-006-AUTHORITY", []string{built.ID("FINDING_NOTICE")}},
			{"EVT-006-SETTLEMENT", timeline.TypeRecovery, EpochSeconds(1800), timeline.CertaintyConfirmed,
				"Jurisdiction B", "PTY-006-AUTHORITY", []string{built.ID("SETTLEMENT_ORDER")}},
		}
	case CaseCrossBorderDispute:
		specs = []spec{
			{"EVT-007-DISPUTE-NOTICE", timeline.TypeNotice, EpochSeconds(2000), timeline.CertaintyConfirmed,
				"Jurisdiction A", "PTY-007-CLAIMANT", []string{built.ID("NOTICE_OF_DISPUTE")}},
			{"EVT-007-HOLD", timeline.TypeMitigation, EpochSeconds(2010), timeline.CertaintyConfirmed,
				"Jurisdiction A", "PTY-007-COUNSEL", []string{built.ID("HOLD_INSTRUCTION")}},
			{"EVT-007-CLAIM", timeline.TypeClaim, EpochSeconds(2100), timeline.CertaintyConfirmed,
				"Jurisdiction A", "PTY-007-CLAIMANT", []string{built.ID("CLAIMANT_POSITION")}},
			{"EVT-007-SETTLEMENT", timeline.TypeRecovery, EpochSeconds(2600), timeline.CertaintyConfirmed,
				"Jurisdiction A", "PTY-007-COUNSEL", []string{built.ID("SETTLEMENT_AGREEMENT")}},
		}
	}

	out := make([]timeline.Event, 0, len(specs))
	for _, s := range specs {
		e, err := timeline.New(s.id, s.kind, "", "", s.utc, s.sources, s.certainty, s.location, partyID(s.actor))
		if err != nil {
			return nil, fmt.Errorf("%s: building event %s: %w", c.ID, s.id, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// contradictionObservation is one evidence-backed observation fed into
// the real arbitration engine.
type contradictionObservation struct {
	rec         insevidence.Record
	value       string
	reliability float64
}

// contradictionObservationsFor returns the claim key each case's
// sources disagree about, and the observations themselves. Every case
// has a genuine disagreement — that is the point of the pack.
func contradictionObservationsFor(c Case, built BuiltEvidence) (string, []contradictionObservation) {
	switch c.ID {
	case CasePortCallDemurrage:
		return "berth_readiness_time", []contradictionObservation{
			{built.ByKey["SOF_BERTH_READY"], "13:40", 0.7},
			{built.ByKey["TERMINAL_LOG"], "13:35", 0.8},
		}
	case CaseCargoDamageReefer:
		return "peak_container_temperature", []contradictionObservation{
			{built.ByKey["TEMP_LOG_PRIMARY"], "8.2C", 0.8},
			{built.ByKey["TEMP_LOG_SECONDARY"], "4.1C", 0.6},
		}
	case CaseCommodityDocuments:
		return "cargo_quantity_mt", []contradictionObservation{
			{built.ByKey["BL_QUANTITY"], "98500", 0.6},
			{built.ByKey["DRAUGHT_SURVEY"], "97820", 0.85},
		}
	case CaseGeneralAverage:
		return "general_average_contribution_due", []contradictionObservation{
			{built.ByKey["GA_ADJUSTMENT"], "CONTRIBUTION_CALLED", 0.8},
			{built.ByKey["WAR_RISK_PREMIUM"], "ADDITIONAL_PREMIUM_ALREADY_PAID", 0.7},
		}
	case CaseBriberyRisk:
		return "intermediary_engagement_risk", []contradictionObservation{
			{built.ByKey["SCOPE_REVIEW"], "SCOPE_NOT_EVIDENCED", 0.8},
			{built.ByKey["COUNTER_EVIDENCE"], "EXPLANATION_OFFERED_UNCORROBORATED", 0.4},
		}
	case CaseRegulatorySettlement:
		return "allegation_status", []contradictionObservation{
			{built.ByKey["FINDING_NOTICE"], "RECORD_KEEPING_FINDING_MADE", 0.9},
			{built.ByKey["SETTLEMENT_ORDER"], "REMAINING_ALLEGATIONS_NOT_ADJUDICATED", 0.9},
		}
	case CaseCrossBorderDispute:
		return "cargo_condition_on_delivery", []contradictionObservation{
			{built.ByKey["CLAIMANT_POSITION"], "MATERIALLY_DIFFERENT", 0.7},
			{built.ByKey["RESPONDENT_POSITION"], "CONSISTENT_WITH_PRE_SHIPMENT", 0.7},
		}
	}
	return "unspecified", nil
}

// hypothesesFor builds each case's competing causal hypotheses. Every
// case gets at least two, because a single hypothesis is not a
// hypothesis set — it is an assertion, which is precisely what I-04
// forbids.
func hypothesesFor(c Case, built BuiltEvidence) (*causation.HypothesisSet, error) {
	question, specs := hypothesisSpecsFor(c, built)
	hs, err := causation.NewHypothesisSet(string(c.ID), "CLM-"+string(c.ID), question)
	if err != nil {
		return nil, fmt.Errorf("%s: NewHypothesisSet: %w", c.ID, err)
	}
	for _, s := range specs {
		h, err := causation.NewHypothesis(s.id, s.description)
		if err != nil {
			return nil, fmt.Errorf("%s: NewHypothesis %s: %w", c.ID, s.id, err)
		}
		if err := hs.Add(h); err != nil {
			return nil, fmt.Errorf("%s: adding %s: %w", c.ID, s.id, err)
		}
		for _, ev := range s.supporting {
			if err := hs.AddSupportingEvidence(s.id, ev); err != nil {
				return nil, fmt.Errorf("%s: supporting evidence for %s: %w", c.ID, s.id, err)
			}
		}
		for _, ev := range s.contradicting {
			if err := hs.AddContradictingEvidence(s.id, ev); err != nil {
				return nil, fmt.Errorf("%s: contradicting evidence for %s: %w", c.ID, s.id, err)
			}
		}
		for _, m := range s.missing {
			if err := hs.AddMissingEvidence(s.id, m); err != nil {
				return nil, fmt.Errorf("%s: missing evidence for %s: %w", c.ID, s.id, err)
			}
		}
	}
	return hs, nil
}

type hypothesisSpec struct {
	id            causation.HypothesisID
	description   string
	supporting    []string
	contradicting []string
	missing       []string
}

func hypothesisSpecsFor(c Case, built BuiltEvidence) (string, []hypothesisSpec) {
	switch c.ID {
	case CasePortCallDemurrage:
		return "What accounts for the difference between the recorded readiness times?", []hypothesisSpec{
			{"H1", "The vessel's own statement of facts records the operative readiness time",
				built.IDs("SOF_BERTH_READY"), built.IDs("TERMINAL_LOG"),
				[]string{"terminal operational record supporting readiness"}},
			{"H2", "The terminal's gate log records the operative readiness time",
				built.IDs("TERMINAL_LOG"), built.IDs("SOF_BERTH_READY"),
				[]string{"terminal operational record supporting readiness"}},
			{"H3", "The two records describe different operational milestones and are not in conflict",
				nil, nil, []string{"definition of readiness applied by each recorder"}},
		}
	case CaseCargoDamageReefer:
		return "What accounts for the cargo condition observed at delivery?", []hypothesisSpec{
			{"H1", "A temperature excursion during the sea passage",
				built.IDs("TEMP_LOG_PRIMARY", "DAMAGE_PHOTOS"), built.IDs("TEMP_LOG_SECONDARY"),
				[]string{"joint survey report", "reefer machinery maintenance record"}},
			{"H2", "A pre-shipment condition present before the container was sealed",
				built.IDs("POD"), built.IDs("DAMAGE_PHOTOS"),
				[]string{"pre-shipment inspection certificate"}},
			{"H3", "Handling or storage after delivery",
				nil, built.IDs("POD"), []string{"cold-store entry temperature log"}},
		}
	case CaseCommodityDocuments:
		return "What accounts for the difference between the documented and surveyed quantities?", []hypothesisSpec{
			{"H1", "A measurement difference inherent to the survey method",
				built.IDs("DRAUGHT_SURVEY"), nil, []string{"survey calculation working papers"}},
			{"H2", "A shortfall in the quantity actually loaded",
				built.IDs("DRAUGHT_SURVEY"), built.IDs("BL_QUANTITY", "PACKING_LIST"),
				[]string{"load-port weighbridge tickets"}},
			{"H3", "A transcription difference between the shipping documents",
				built.IDs("PACKING_LIST"), nil, []string{"the shipper's own loading tally"}},
		}
	case CaseGeneralAverage:
		return "What is the relationship between the war-risk arrangement and the general-average contribution?", []hypothesisSpec{
			{"H1", "The arrangement and the contribution operate independently",
				built.IDs("GA_ADJUSTMENT"), built.IDs("WAR_RISK_PREMIUM"),
				[]string{"legal interpretation of the incorporated clause"}},
			{"H2", "The arrangement bears on the contribution",
				built.IDs("WAR_RISK_PREMIUM", "WAR_RISK_CLAUSE"), built.IDs("GA_ADJUSTMENT"),
				[]string{"legal interpretation of the incorporated clause"}},
		}
	case CaseBriberyRisk:
		return "What accounts for the pattern observed in this engagement?", []hypothesisSpec{
			{"H1", "A commercially ordinary engagement documented to a poor standard",
				built.IDs("COUNTER_EVIDENCE"), built.IDs("FEE_BENCHMARK", "PAYMENT_INSTRUCTION"),
				[]string{"evidence of services actually rendered", "beneficiary ownership verification"}},
			{"H2", "An engagement structure carrying elevated risk requiring enhanced due diligence",
				built.IDs("PEP_DISCLOSURE", "SCOPE_REVIEW", "FEE_BENCHMARK", "PAYMENT_INSTRUCTION"),
				built.IDs("COUNTER_EVIDENCE"),
				[]string{"enhanced due diligence report", "beneficiary ownership verification"}},
		}
	case CaseRegulatorySettlement:
		return "What did the regulatory process actually establish?", []hypothesisSpec{
			{"H1", "Only the record-keeping allegation was determined; the remainder were not adjudicated",
				built.IDs("FINDING_NOTICE", "SETTLEMENT_ORDER"), nil,
				[]string{"any determination of the remaining allegations"}},
			{"H2", "The settlement resolved the matter without determining anything at all",
				built.IDs("SETTLEMENT_ORDER"), built.IDs("FINDING_NOTICE"),
				[]string{"the authority's own characterisation of the finding's scope"}},
		}
	case CaseCrossBorderDispute:
		return "What accounts for the difference between the parties' accounts of the cargo condition?", []hypothesisSpec{
			{"H1", "The condition changed during the carriage",
				built.IDs("CLAIMANT_POSITION"), built.IDs("RESPONDENT_POSITION"),
				[]string{"joint survey at discharge", "pre-shipment condition record"}},
			{"H2", "The condition was consistent throughout and the accounts describe the same state",
				built.IDs("RESPONDENT_POSITION"), built.IDs("CLAIMANT_POSITION"),
				[]string{"pre-shipment condition record"}},
		}
	}
	return "What accounts for the observed outcome?", []hypothesisSpec{
		{"H1", "Explanation one", nil, nil, []string{"further evidence"}},
		{"H2", "Explanation two", nil, nil, []string{"further evidence"}},
	}
}

// quantumInputFor returns each case's quantum operands and the
// claimant's own asserted figure. Every non-zero operand cites real
// evidence, because the §55 gate refuses a figure that came from
// nowhere.
func quantumInputFor(c Case, built BuiltEvidence) (quantum.ComputeInput, quantum.EvidenceBackedAmount) {
	in := quantum.ComputeInput{
		CalculationID: "QC-" + string(c.ID),
		Currency:      "USD",
		ExchangeRate:  quantum.UnitExchangeRate(),
		RateSource:    "case_currency",
		Mitigation:    quantum.NewEvidenceBackedAmount(0),
		Salvage:       quantum.NewEvidenceBackedAmount(0),
		Deductible:    quantum.NewEvidenceBackedAmount(0),
	}
	switch c.ID {
	case CaseCargoDamageReefer:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(186_000), built.ID("COMMERCIAL_INVOICE"))
		in.Salvage = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(31_000), built.ID("SALVAGE_SALE"))
		in.Deductible = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(5_000), built.ID("POLICY"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(212_000), built.ID("COMMERCIAL_INVOICE"))
	case CaseCommodityDocuments:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(204_000), built.ID("DRAUGHT_SURVEY"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(204_000), built.ID("INVOICE"))
	case CasePortCallDemurrage:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(47_500), built.ID("DEMURRAGE_CLAIM"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(61_250), built.ID("DEMURRAGE_CLAIM"))
	case CaseGeneralAverage:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1_250_000), built.ID("GA_ADJUSTMENT"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1_250_000), built.ID("GA_ADJUSTMENT"))
	case CaseBriberyRisk:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(340_000), built.ID("PAYMENT_INSTRUCTION"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(340_000), built.ID("PAYMENT_INSTRUCTION"))
	case CaseRegulatorySettlement:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(2_400_000), built.ID("SETTLEMENT_ORDER"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(2_400_000), built.ID("SETTLEMENT_ORDER"))
	case CaseCrossBorderDispute:
		in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(96_000), built.ID("CLAIMANT_POSITION"))
		return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(140_000), built.ID("CLAIMANT_POSITION"))
	}
	in.GrossLoss = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1), built.Records[0].EvidenceID())
	return in, quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1), built.Records[0].EvidenceID())
}

// noticeTicksFor returns each case's notice tick and notice deadline
// tick. CASE-INS-002 is deliberately LATE: it is the case that proves
// LATE NOTICE != COVERAGE DENIED end to end.
func noticeTicksFor(id CaseID) (noticeTick, deadlineTick uint64) {
	switch id {
	case CaseCargoDamageReefer:
		// Discovery at 2000, 24-tick notice period, notice at 2120.
		return 2120, 2024
	case CasePortCallDemurrage:
		return 2010, 2024
	default:
		return 2005, 2024
	}
}

// noticeAssessmentFor runs the I-03 notice engine over each case, using
// the same clause the coverage engine used.
func noticeAssessmentFor(c Case, built BuiltEvidence, noticeTick, deadlineTick uint64) (obligation.Assessment, error) {
	n, err := obligation.NewNotice("NOT-"+string(c.ID), string(c.ID))
	if err != nil {
		return obligation.Assessment{}, err
	}
	n.IncidentTime = 1990
	n.DiscoveryTime = 2000
	n.NoticeSentTime = noticeTick
	n.NoticeReceivedTime = noticeTick
	n.NoticeMethod = "email"
	if rec, ok := built.ByKey["NOTICE_EMAIL"]; ok {
		n.NoticeEvidence = []string{rec.EvidenceID()}
	}

	policyID := "POL-" + string(c.ID)
	o := obligation.Obligation{
		ObligationID:     "OBL-NOTICE-" + string(c.ID),
		CaseID:           string(c.ID),
		Duty:             "notify the insurer of the loss",
		SourceClause:     "cl. 7.2",
		SourceDocument:   policyID,
		SourceVersion:    "V1",
		TriggerEvent:     "DAMAGE_DISCOVERY",
		TriggerBasis:     obligation.TriggerFromDiscovery,
		RequiredEvidence: []string{"notice_letter"},
		DeadlineRuleID:   "DR-" + string(c.ID),
		ComplianceBasis:  obligation.ComplianceByReceived,
		ResponsibleParty: c.Parties[0].ID,
		Status:           obligation.StatusOpen,
	}
	rule, err := deadlineRuleFor(c.ID, policyID)
	if err != nil {
		return obligation.Assessment{}, err
	}
	return obligation.Assess(n, o, rule, 3000)
}

// authorizationsFor returns the recorded human authorizations for a
// case.
//
// The pack deliberately supplies NONE. Every synthetic case ends with
// its dossier's review questions outstanding, so the §57 Human Review
// Gate is exercised in its FAIL-CLOSED state — which is the state that
// matters. A pack that handed itself a rubber-stamp authorization would
// prove nothing about the gate it claims to exercise.
//
// A deployment with a real pkg/governance/hitl GovernedOutcome fills
// this in from that outcome; nothing here fabricates one.
func authorizationsFor(_ CaseID, _ *dossier.Dossier) []verification.ReviewAuthorization {
	return nil
}

// deadlineRuleFor is the notice-deadline rule each case's obligation
// points at. It is built from the same clause the coverage engine cites,
// so the two engines cannot silently disagree about which clause governs.
func deadlineRuleFor(id CaseID, policyID string) (deadline.Rule, error) {
	return deadline.New("DR-"+string(id), deadline.SourceTypePolicy, "cl. 7.2",
		policyID, "V1", "DAMAGE_DISCOVERY", 24, deadline.CalendarRuleCalendarDays, "UTC")
}

func partyID(s string) party.PartyID { return party.PartyID(s) }

// AuthorizationsSatisfying builds a well-formed authorization
// addressing every review question a dossier raised.
//
// It exists ONLY so a test can prove the §57 gate OPENS when it should.
// Drive itself never calls it: a pack that handed itself an
// authorization would exercise the gate's permissive path and never its
// fail-closed one, which is the path that matters. Both halves are
// proven separately, and neither is proven by the other.
func AuthorizationsSatisfying(d *dossier.Dossier, reviewerID, caseRef string, tick uint64) []verification.ReviewAuthorization {
	if d == nil {
		return nil
	}
	return []verification.ReviewAuthorization{{
		ReviewerID:         reviewerID,
		CaseRef:            caseRef,
		Rationale:          "every review question raised by this dossier was examined and addressed",
		AuthorizedTick:     tick,
		AddressedQuestions: append([]string(nil), d.HumanReviewQuestions...),
	}}
}
