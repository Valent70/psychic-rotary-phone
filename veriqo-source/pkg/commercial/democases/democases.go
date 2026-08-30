// Package democases answers Commercialization Sprint items 14-16 by
// name: three canonical demo cases (eBL transfer dispute, maritime
// incident, insurance claim), each real code that runs the full
// SOURCE..RECEIPT vertical slice through the actual, frozen kernel via
// pkg/commercial/api.Store -- the SAME Store a real Commercial API v1
// caller uses, not a shortcut around it. There is no synthetic
// "demo mode": these are ordinary cases whose evidence content
// happens to be illustrative rather than customer-submitted.
//
// Each case is deliberately built to expose, not obscure, the honest
// boundary the reviewer names in item 8 and item 12: what VERIQO can
// PROVE (hash integrity, custody continuity, decision provenance,
// deterministic replay) versus what remains outside its authority (a
// legal conclusion, a jurisdiction's evidentiary standard, a disputed
// fact only a court or arbitration panel can resolve). See
// docs/VERIQO_DEMO_CASES.md for the narrative write-up of what each
// case shows and does not show.
package democases

import (
	"fmt"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
)

// Case is the built, decided, and actioned state of one canonical
// demo case: everything needed to inspect its Decision/Action, or to
// generate and export its Evidence Dossier v1.
type Case struct {
	Store    *commercialapi.Store
	TenantID string
	CaseID   string
	Decision decision.Decision
	Action   action.ActionAuthorization
}

// Dossier generates this case's Evidence Dossier v1 (the "Human"
// form) from the same live Store state a real API caller would see.
func (c Case) Dossier() (dossier.Dossier, error) {
	return c.Store.GenerateDossier(c.TenantID, c.CaseID)
}

// WriteMachinePackage writes this case's Evidence Dossier v1 Machine
// Package (.zip) to outPath.
func (c Case) WriteMachinePackage(outPath string) (dossier.Dossier, error) {
	return c.Store.WriteDossierPackage(c.TenantID, c.CaseID, outPath)
}

type evidenceSpec struct {
	EvidenceID, SHA256, URI, Filename, MediaType, Collector, Source string
	ByteSize                                                        int64
	Domain                                                          evidencefabric.DomainMetadata
	Tick                                                            uint64
}

// caseSpec is the one shared shape every demo case is built from --
// the SAME fields a Commercial API v1 caller would send across
// POST /v1/evidence, POST /v1/cases/{id}/decide, and
// POST /v1/cases/{id}/actions, just supplied as Go values instead of
// JSON over HTTP.
type caseSpec struct {
	TenantID, CaseID                                string
	Evidence                                        []evidenceSpec
	Hypothesis                                      causation.Hypothesis
	SupportingEvidenceIDs, ContradictingEvidenceIDs []string
	FindingID                                       string
	Finding                                         cre.FindingInput
	Outcome                                         decision.Outcome
	Rationale                                       string
	PermittedAction                                 action.Action
	ActionScope, ActionPolicyRef, Actor             string
	Conditions                                      []string
}

func buildCase(spec caseSpec) (Case, error) {
	store := commercialapi.NewStore()

	if err := store.CreateCase(spec.TenantID, spec.CaseID, 0); err != nil {
		return Case{}, fmt.Errorf("democases: CreateCase(%s): %w", spec.CaseID, err)
	}
	for _, ev := range spec.Evidence {
		if _, err := store.SubmitEvidence(commercialapi.EvidenceInput{
			TenantID: spec.TenantID, CaseID: spec.CaseID, EvidenceID: ev.EvidenceID,
			SHA256: ev.SHA256, URI: ev.URI, Filename: ev.Filename, MediaType: ev.MediaType,
			ByteSize: ev.ByteSize, Collector: ev.Collector, Source: ev.Source, Domain: ev.Domain, Tick: ev.Tick,
		}); err != nil {
			return Case{}, fmt.Errorf("democases: SubmitEvidence(%s): %w", ev.EvidenceID, err)
		}
	}

	d, err := store.DecideCase(commercialapi.DecideInput{
		TenantID: spec.TenantID, CaseID: spec.CaseID, Hypothesis: spec.Hypothesis,
		SupportingEvidenceIDs: spec.SupportingEvidenceIDs, ContradictingEvidenceIDs: spec.ContradictingEvidenceIDs,
		FindingID: spec.FindingID, Finding: spec.Finding, Outcome: spec.Outcome, Rationale: spec.Rationale,
		LedgerActor: "democases-decision-" + spec.CaseID, Tick: 10,
	})
	if err != nil {
		return Case{}, fmt.Errorf("democases: DecideCase(%s): %w", spec.CaseID, err)
	}

	aa, _, err := store.ActOnCase(commercialapi.ActionInput{
		TenantID: spec.TenantID, CaseID: spec.CaseID, Actor: spec.Actor, PolicyRef: spec.ActionPolicyRef,
		Scope: spec.ActionScope, PermittedAction: spec.PermittedAction, Conditions: spec.Conditions,
		AuthorizedAt: 10, ExpiresAt: 1000, ExecutingActor: spec.Actor, ExecutionAt: 15,
		LedgerActor: "democases-action-" + spec.CaseID,
	})
	if err != nil {
		return Case{}, fmt.Errorf("democases: ActOnCase(%s): %w", spec.CaseID, err)
	}

	return Case{Store: store, TenantID: spec.TenantID, CaseID: spec.CaseID, Decision: d, Action: aa}, nil
}

// BuildEBLTransferDisputeCase is Demo 1: an electronic Bill of
// Lading whose custody the platform's own transfer log shows moving
// from the shipper to a financing bank. VERIQO can prove the
// issuance and transfer records are hash-intact, finalized, and
// mutually consistent; it explicitly cannot rule on whether that
// transfer satisfies MLETR "control" in the receiving jurisdiction --
// see docs/VERIQO_DEMO_CASES.md, Demo 1, "Remains a Legal Question."
func BuildEBLTransferDisputeCase() (Case, error) {
	const tenant = "tenant-demo-trade"
	const caseID = "VRQ-2026-0001"
	return buildCase(caseSpec{
		TenantID: tenant, CaseID: caseID,
		Evidence: []evidenceSpec{
			{
				EvidenceID: "EV-EBL-ISSUANCE", SHA256: "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd01",
				URI: "evidence://ebl-platform/issuance-EBL-88213.json", Filename: "ebl-issuance-88213.json",
				MediaType: "application/json", ByteSize: 2048, Collector: "ebl-platform-connector",
				Source: "electronic-bl-platform", Tick: 5,
				Domain: evidencefabric.DomainMetadata{Trade: &evidencefabric.TradeMetadata{
					DocumentType: "EBL", TransferEventID: "TRANSFER-0-ISSUANCE", HolderIdentity: "Shipper-Co",
				}},
			},
			{
				EvidenceID: "EV-EBL-TRANSFER-1", SHA256: "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd02",
				URI: "evidence://ebl-platform/transfer-EBL-88213-01.json", Filename: "ebl-transfer-88213-01.json",
				MediaType: "application/json", ByteSize: 1536, Collector: "ebl-platform-connector",
				Source: "electronic-bl-platform", Tick: 8,
				Domain: evidencefabric.DomainMetadata{Trade: &evidencefabric.TradeMetadata{
					DocumentType: "EBL", TransferEventID: "TRANSFER-1", HolderIdentity: "Bank-A",
				}},
			},
		},
		Hypothesis: causation.Hypothesis{
			ID:          "H1",
			Description: "the eBL was validly transferred from Shipper-Co to Bank-A, per the issuing platform's own transfer log",
		},
		SupportingEvidenceIDs: []string{"EV-EBL-ISSUANCE", "EV-EBL-TRANSFER-1"},
		FindingID:             "finding-ebl-transfer-1",
		Finding: cre.FindingInput{
			CaseID: caseID, ContractBasis: "ebl-transfer-clause-4.2", ObligationRef: "holder-succession-obligation",
			EventRef: "TRANSFER-1", QuantumRef: "N/A-no-quantum-calculation-applies", HumanReviewRequired: true,
		},
		Outcome:         decision.OutcomeApproved,
		Rationale:       "the issuance record and the sole transfer event are hash-verified, custody-continuous, and mutually consistent with no contradicting record",
		PermittedAction: action.ActionInitiateTradeFinance,
		ActionScope:     caseID,
		ActionPolicyRef: "policy-ebl-trade-finance-v1",
		Actor:           "trade-ops-1",
		Conditions:      []string{"counterparty_confirmation_pending"},
	})
}

// BuildMaritimeIncidentCase is Demo 2: an AIS track and a port
// arrival log that do not agree on the vessel's position at a given
// time, escalated for human review rather than auto-resolved. VERIQO
// can prove both records are hash-intact and can surface the
// contradiction precisely; it explicitly cannot determine WHY the
// two disagree (AIS spoofing, transponder fault, late port logging)
// -- see docs/VERIQO_DEMO_CASES.md, Demo 2, "Remains a Legal Question."
func BuildMaritimeIncidentCase() (Case, error) {
	const tenant = "tenant-demo-maritime"
	const caseID = "VRQ-2026-0002"
	return buildCase(caseSpec{
		TenantID: tenant, CaseID: caseID,
		Evidence: []evidenceSpec{
			{
				EvidenceID: "EV-AIS-TRACK-1", SHA256: "bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee01",
				URI: "evidence://aisstream/vessel-MV-EXAMPLE-track-20260115.json", Filename: "ais-track-20260115.json",
				MediaType: "application/json", ByteSize: 8192, Collector: "aisstream-connector",
				Source: "AIS_STREAM", Tick: 3,
				Domain: evidencefabric.DomainMetadata{Maritime: &evidencefabric.MaritimeMetadata{
					VesselIdentity: "IMO-9876543 / MV EXAMPLE", PortCode: "", EventKind: "AIS_STATUS",
					Location: "01.29N 103.85E (approx, per AIS)",
				}},
			},
			{
				EvidenceID: "EV-PORT-ARRIVAL-1", SHA256: "bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee02",
				URI: "evidence://port-authority/arrival-log-MV-EXAMPLE-20260115.json", Filename: "port-arrival-20260115.json",
				MediaType: "application/json", ByteSize: 1024, Collector: "port-event-connector",
				Source: "PORT_AUTHORITY_LOG", Tick: 4,
				Domain: evidencefabric.DomainMetadata{Maritime: &evidencefabric.MaritimeMetadata{
					VesselIdentity: "IMO-9876543 / MV EXAMPLE", PortCode: "SGSIN", EventKind: "PORT_EVENT",
					Location: "Singapore, berth 7 (per port log)",
				}},
			},
		},
		Hypothesis: causation.Hypothesis{
			ID:          "H1",
			Description: "MV EXAMPLE's AIS-reported position at 20260115T0600Z is inconsistent with the port authority's logged berth arrival time for the same vessel",
		},
		SupportingEvidenceIDs:    []string{"EV-AIS-TRACK-1"},
		ContradictingEvidenceIDs: []string{"EV-PORT-ARRIVAL-1"},
		FindingID:                "finding-maritime-deviation-1",
		Finding: cre.FindingInput{
			CaseID: caseID, ContractBasis: "voyage-monitoring-policy-v1", ObligationRef: "position-consistency-check",
			EventRef: "EV-AIS-TRACK-1", QuantumRef: "N/A-no-quantum-calculation-applies", HumanReviewRequired: true,
		},
		Outcome:         decision.OutcomeEscalated,
		Rationale:       "AIS track and port arrival log are each individually hash-verified but mutually contradictory on vessel position/time; a technical hash/custody check cannot resolve which record is accurate, so this is escalated for human review rather than auto-decided",
		PermittedAction: action.ActionSendNotification,
		ActionScope:     caseID,
		ActionPolicyRef: "policy-maritime-escalation-notify-v1",
		Actor:           "maritime-ops-1",
		Conditions:      []string{"underwriter_and_pandi_club_notified"},
	})
}

// BuildInsuranceClaimCase is Demo 3: a marine cargo claim with a
// surveyor report and adjuster evidence supporting settlement.
// VERIQO can prove the finding was grounded only in finalized,
// hash-verified evidence and that the resulting authorization traces
// back to that exact Decision; it explicitly does not determine
// whether the settlement amount is fair or whether the policy
// actually covers the loss under the governing law -- see
// docs/VERIQO_DEMO_CASES.md, Demo 3, "Remains a Legal Question."
func BuildInsuranceClaimCase() (Case, error) {
	const tenant = "tenant-demo-insurance"
	const caseID = "VRQ-2026-0003"
	return buildCase(caseSpec{
		TenantID: tenant, CaseID: caseID,
		Evidence: []evidenceSpec{
			{
				EvidenceID: "EV-SURVEY-1", SHA256: "cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff01",
				URI: "evidence://surveyor/independent-survey-CLM-4471.pdf", Filename: "survey-CLM-4471.pdf",
				MediaType: "application/pdf", ByteSize: 4096, Collector: "surveyor-independent-1",
				Source: "independent-surveyor", Tick: 6,
				Domain: evidencefabric.DomainMetadata{Insurance: &evidencefabric.InsuranceMetadata{
					ClaimID: "CLM-4471", PolicyID: "POL-9001", PartyID: "surveyor-independent-1", EvidenceKind: "SURVEY",
				}},
			},
			{
				EvidenceID: "EV-ADJUSTER-1", SHA256: "cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff02",
				URI: "evidence://adjuster/report-CLM-4471.pdf", Filename: "adjuster-report-CLM-4471.pdf",
				MediaType: "application/pdf", ByteSize: 3072, Collector: "adjuster-firm-2",
				Source: "adjuster-report", Tick: 7,
				Domain: evidencefabric.DomainMetadata{Insurance: &evidencefabric.InsuranceMetadata{
					ClaimID: "CLM-4471", PolicyID: "POL-9001", PartyID: "adjuster-firm-2", EvidenceKind: "ADJUSTER_REPORT",
				}},
			},
		},
		Hypothesis: causation.Hypothesis{
			ID:          "H1",
			Description: "water ingress during transit caused the cargo loss claimed under POL-9001",
		},
		SupportingEvidenceIDs: []string{"EV-SURVEY-1", "EV-ADJUSTER-1"},
		FindingID:             "finding-claim-4471-1",
		Finding: cre.FindingInput{
			CaseID: caseID, ContractBasis: "policy-clause-water-ingress-3.1", ObligationRef: "obl-settlement-4471",
			EventRef: "transit-event-4471", QuantumRef: "calc-4471-v1", HumanReviewRequired: true,
		},
		Outcome:         decision.OutcomeApproved,
		Rationale:       "the independent survey and adjuster report are hash-verified, finalized, and mutually consistent with the water-ingress hypothesis",
		PermittedAction: action.ActionApproveSettlement,
		ActionScope:     caseID,
		ActionPolicyRef: "policy-settlement-v1",
		Actor:           "claims-adjuster-1",
		Conditions:      []string{"reinspection_complete"},
	})
}
