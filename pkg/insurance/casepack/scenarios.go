package casepack

import (
	"veriqo/pkg/evidence/provenance"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
)

// This file holds the seven synthetic case descriptors. Every name in
// it is invented. See the package doc for the naming rule and
// TestNoRealWorldEntityAppearsInThePack for its enforcement.
//
// Ticks: each case uses its own small tick scale where 1 tick = 1 hour
// of the scenario's own timeline, with the case opening around tick
// 1000. Nothing outside a case compares its ticks with another's.

// ---- CASE-INS-001 — Port call / demurrage ----------------------------

// casePortCallDemurrage is the Final Design §27 narrative, made fully
// synthetic: a vessel's readiness chronology is recorded four different
// ways by four different sources, and they do not agree.
//
// The output is NOT "the carrier is liable". It is: four facts, one
// contradiction, one named missing piece of evidence, the contract
// clause that makes the chronology matter, and a review task with an
// owner — exactly the §27 worked example.
func casePortCallDemurrage() Case {
	const (
		owner      = party.PartyID("PTY-001-OWNER")
		charterer  = party.PartyID("PTY-001-CHARTERER")
		terminal   = party.PartyID("PTY-001-TERMINAL")
		agent      = party.PartyID("PTY-001-AGENT")
		insurer    = party.PartyID("PTY-001-INSURER")
		aisService = party.PartyID("PTY-001-AIS-PROVIDER")
	)
	return Case{
		ID:    CasePortCallDemurrage,
		Title: "Port call and laytime chronology, with conflicting readiness records",
		Narrative: "A bulk carrier arrives at a port in Jurisdiction A. Four independent sources " +
			"record the arrival and readiness chronology, and they disagree by minutes to hours. " +
			"A demurrage exposure turns on which chronology governs. The system reconstructs the " +
			"timeline, surfaces the contradiction, names the missing terminal operational record, " +
			"and routes the question to a human — it does not decide whose clock was right.",
		EngineeringCoverage: []string{
			"timeline reconstruction from four independent sources",
			"contradiction detection across sources reporting the same claim key",
			"evidence dependency graph (independent-source counting)",
			"deadline rule extraction from a charterparty clause",
			"obligation graph: clause -> duty -> trigger -> deadline -> responsible party",
			"case lineage: evidence, events, contradictions under one CaseID",
		},
		ExpectedOutputs: []string{
			"CONTRADICTION: readiness chronology contains conflicting records",
			"MISSING_EVIDENCE: terminal operational record supporting readiness",
			"HUMAN_REVIEW_REQUIRED",
			"NO_LIABILITY_DETERMINATION",
		},
		Parties: []Party{
			{ID: owner, Name: "Northwind Bulk Owners Ltd", Roles: []party.Role{party.RoleShipowner}, EntityRef: "ENT-FIC-NORTHWIND"},
			{ID: charterer, Name: "Ardent Grain Charterers SA", Roles: []party.Role{party.RoleCharterer}, EntityRef: "ENT-FIC-ARDENT"},
			{ID: terminal, Name: "Calder Bay Terminal Operations", Roles: []party.Role{party.RoleTerminal}, EntityRef: "ENT-FIC-CALDERBAY"},
			{ID: agent, Name: "Calder Bay Port Agency", Roles: []party.Role{party.RoleForwarder}},
			{ID: insurer, Name: "Meridian Marine Mutual", Roles: []party.Role{party.RoleInsurer}, EntityRef: "ENT-FIC-MERIDIAN"},
			{ID: aisService, Name: "Generic AIS Data Provider", Roles: []party.Role{party.RoleSurveyor}},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "NOR", Subject: "MV_ARDENT_MERIDIAN", Predicate: "tendered", Object: "notice_of_readiness",
				Source: "port_agency_filing", ObservedAt: 1008, DocumentType: "notice_of_readiness",
				SourceParty: agent, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"stated_time": "08:00", "port": "Calder Bay, Jurisdiction A"},
			},
			{
				Key: "AIS_ARRIVAL", Subject: "MV_ARDENT_MERIDIAN", Predicate: "entered", Object: "anchorage_area",
				Source: "ais_position_feed", ObservedAt: 1008, DocumentType: "ais_record",
				SourceParty: aisService, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"stated_time": "08:06", "note": "vendor-neutral positional feed"},
			},
			{
				Key: "SOF_BERTH_READY", Subject: "MV_ARDENT_MERIDIAN", Predicate: "recorded", Object: "berth_readiness",
				Source: "statement_of_facts", ObservedAt: 1013, DocumentType: "statement_of_facts",
				SourceParty: owner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"stated_time": "13:40"},
			},
			{
				Key: "TERMINAL_LOG", Subject: "MV_ARDENT_MERIDIAN", Predicate: "recorded", Object: "berth_readiness",
				Source: "terminal_gate_log", ObservedAt: 1013, DocumentType: "terminal_record",
				SourceParty: terminal, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{"stated_time": "13:35"},
			},
			{
				Key: "CHARTERPARTY", Subject: "CP-FIC-2026-014", Predicate: "establishes", Object: "laytime_and_notice_terms",
				Source: "contract_repository", ObservedAt: 900, DocumentType: "charterparty",
				SourceParty: charterer, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{"clause": "cl. 18", "governing_law": "the law of Jurisdiction A"},
			},
			{
				Key: "DEMURRAGE_CLAIM", Subject: "CLM-FIC-001", Predicate: "asserts", Object: "demurrage_exposure",
				Source: "owner_claim_submission", ObservedAt: 1200, DocumentType: "claim_submission",
				SourceParty: owner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"basis": "laytime exceeded on the owner's chronology"},
			},
		},
	}
}

// ---- CASE-INS-002 — Cargo damage / reefer ----------------------------

// caseCargoDamageReefer is the Final Design §30 secondary case: a
// temperature excursion on a refrigerated container, discovered at
// delivery, with a notice deadline, a missing joint survey, and a
// mitigation action.
//
// The design document's own closing line for this case is the constraint
// the whole scenario is built to respect: **Damage ≠ Liability.**
func caseCargoDamageReefer() Case {
	const (
		cargoOwner = party.PartyID("PTY-002-CARGO-OWNER")
		carrier    = party.PartyID("PTY-002-CARRIER")
		surveyor   = party.PartyID("PTY-002-SURVEYOR")
		insurer    = party.PartyID("PTY-002-INSURER")
		warehouse  = party.PartyID("PTY-002-WAREHOUSE")
	)
	return Case{
		ID:    CaseCargoDamageReefer,
		Title: "Refrigerated cargo damage discovered at delivery, following a temperature excursion",
		Narrative: "A refrigerated container of perishable produce is delivered in Jurisdiction A. " +
			"Damage is discovered on opening. The container's own temperature logger records an " +
			"excursion above the contractual threshold during the sea passage; a second logger " +
			"reading disagrees. Notice is given after the policy deadline. A joint survey was " +
			"never held. The system records the observed damage, the excursion, the late notice " +
			"and the missing survey — and determines neither cause nor liability.",
		EngineeringCoverage: []string{
			"notice assessment: late notice with a recorded delay",
			"LATE NOTICE != COVERAGE DENIED separation, end to end",
			"causation hypotheses with supporting/contradicting/missing evidence",
			"quantum computation with evidence-backed operands and a claimed-vs-supported discrepancy",
			"mitigation action recording and impact",
			"preservation order with a custodian and a chain of custody",
			"evidence gap detection against the claim type's required evidence",
		},
		ExpectedOutputs: []string{
			"NOTICE_STATUS: LATE",
			"COVERAGE_EFFECT: NOT_DETERMINED_REQUIRES_POLICY_AND_LEGAL_REVIEW",
			"MISSING_EVIDENCE: joint survey report",
			"CAUSATION: competing hypotheses, no single asserted cause",
			"QUANTUM: indicative only, with an unresolved claimed-vs-supported gap",
			"HUMAN_REVIEW_REQUIRED",
		},
		Parties: []Party{
			{ID: cargoOwner, Name: "Verdant Produce Importers BV", Roles: []party.Role{party.RoleCargoOwner, party.RoleClaimant, party.RoleConsignee}, EntityRef: "ENT-FIC-VERDANT"},
			{ID: carrier, Name: "Coastline Container Lines", Roles: []party.Role{party.RoleCarrier}, EntityRef: "ENT-FIC-COASTLINE"},
			{ID: surveyor, Name: "Harbourline Independent Surveyors", Roles: []party.Role{party.RoleSurveyor}, EntityRef: "ENT-FIC-HARBOURLINE"},
			{ID: insurer, Name: "Meridian Marine Mutual", Roles: []party.Role{party.RoleInsurer, party.RoleCargoInsurer}, EntityRef: "ENT-FIC-MERIDIAN"},
			{ID: warehouse, Name: "Calder Bay Cold Store", Roles: []party.Role{party.RoleWarehouse}},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "BILL_OF_LADING", Subject: "BL-FIC-002-0091", Predicate: "evidences", Object: "carriage_contract",
				Source: "shipping_documents", ObservedAt: 1000, DocumentType: "bill_of_lading",
				SourceParty: cargoOwner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"container": "FIC-CU-4471203", "stated_setpoint_c": "2.0"},
			},
			{
				Key: "TEMP_LOG_PRIMARY", Subject: "FIC-CU-4471203", Predicate: "recorded", Object: "temperature_excursion",
				Source: "container_data_logger", ObservedAt: 1042, DocumentType: "temperature_log",
				SourceParty: cargoOwner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"peak_c": "8.2", "duration_minutes": "94", "threshold_c": "5.0"},
			},
			{
				Key: "TEMP_LOG_SECONDARY", Subject: "FIC-CU-4471203", Predicate: "recorded", Object: "temperature_within_range",
				Source: "carrier_reefer_monitoring", ObservedAt: 1042, DocumentType: "temperature_log",
				SourceParty: carrier, EvidenceOrigin: insevidence.OriginCarrier,
				Attributes: map[string]string{"peak_c": "4.1", "note": "carrier-side monitoring extract"},
			},
			{
				Key: "POD", Subject: "FIC-CU-4471203", Predicate: "evidences", Object: "delivery",
				Source: "delivery_records", ObservedAt: 1100, DocumentType: "proof_of_delivery",
				SourceParty: warehouse, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"remark": "container seal intact on arrival"},
			},
			{
				Key: "DAMAGE_PHOTOS", Subject: "FIC-CU-4471203", Predicate: "depicts", Object: "cargo_condition_at_opening",
				Source: "consignee_photographs", ObservedAt: 1102, DocumentType: "photograph",
				SourceParty: cargoOwner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"count": "18"},
			},
			{
				Key: "SURVEYOR_REPORT", Subject: "FIC-CU-4471203", Predicate: "describes", Object: "cargo_condition",
				Source: "single_appointed_surveyor", ObservedAt: 1140, DocumentType: "survey_report",
				SourceParty: surveyor, EvidenceOrigin: insevidence.OriginSurveyor,
				Attributes: map[string]string{"attendance": "consignee-appointed only; carrier did not attend"},
			},
			{
				Key: "NOTICE_EMAIL", Subject: "CLM-FIC-002", Predicate: "notifies", Object: "loss",
				Source: "claims_correspondence", ObservedAt: 1220, DocumentType: "notice_letter",
				SourceParty: cargoOwner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"recipient": "insurer claims desk"},
			},
			{
				Key: "POLICY", Subject: "POL-FIC-002", Predicate: "establishes", Object: "cargo_cover_terms",
				Source: "policy_repository", ObservedAt: 500, DocumentType: "policy_document",
				SourceParty: insurer, EvidenceOrigin: insevidence.OriginInsurer,
				Attributes: map[string]string{"notice_clause": "cl. 7.2", "notice_period_hours": "24"},
			},
			{
				Key: "COMMERCIAL_INVOICE", Subject: "INV-FIC-002-3310", Predicate: "states", Object: "cargo_value",
				Source: "trade_documents", ObservedAt: 990, DocumentType: "invoice",
				SourceParty: cargoOwner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"currency": "USD", "stated_value": "186000.00"},
			},
			{
				Key: "SALVAGE_SALE", Subject: "FIC-CU-4471203", Predicate: "records", Object: "salvage_proceeds",
				Source: "salvage_sale_account", ObservedAt: 1400, DocumentType: "salvage_account",
				SourceParty: cargoOwner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"currency": "USD", "stated_value": "31000.00"},
			},
		},
	}
}

// ---- CASE-INS-003 — Commodity document integrity ---------------------

// caseCommodityDocuments is the Final Design §31 case: a quantity
// mismatch across the shipping documents.
//
// The document's own instruction is the whole point of the scenario:
// the output is DOCUMENT_INCONSISTENCY, **bukan FRAUD** — not fraud.
// Payment or release may be held pending review under the applicable
// workflow and authority, while a human looks at it.
func caseCommodityDocuments() Case {
	const (
		seller    = party.PartyID("PTY-003-SELLER")
		buyer     = party.PartyID("PTY-003-BUYER")
		inspector = party.PartyID("PTY-003-INSPECTOR")
		bank      = party.PartyID("PTY-003-BANK")
	)
	return Case{
		ID:    CaseCommodityDocuments,
		Title: "Commodity document integrity: a quantity mismatch across the shipping set",
		Narrative: "A bulk commodity parcel moves under a documentary credit. The bill of lading, " +
			"the packing list and the commercial invoice all state one quantity; the independent " +
			"draught survey states a materially lower one. The system reports a document " +
			"inconsistency with every figure traced to its source document, and requires a " +
			"surveyor review. It does not report fraud, and it makes no finding about anyone.",
		EngineeringCoverage: []string{
			"cross-document field comparison with per-figure source citation",
			"contradiction record over a shared claim key across four documents",
			"evidence dependency graph proving three documents are not three independent sources",
			"gap assessment naming the review required",
			"rights gate: a restricted document cannot be exported",
		},
		ExpectedOutputs: []string{
			"DOCUMENT_INCONSISTENCY",
			"NO_FRAUD_DETERMINATION",
			"REQUIRED: surveyor review",
			"HUMAN_REVIEW_REQUIRED",
		},
		Parties: []Party{
			{ID: seller, Name: "Silverline Commodities Pte", Roles: []party.Role{party.RoleShipper}, EntityRef: "ENT-FIC-SILVERLINE"},
			{ID: buyer, Name: "Ridgeway Processing Co", Roles: []party.Role{party.RoleConsignee, party.RoleCargoOwner}, EntityRef: "ENT-FIC-RIDGEWAY"},
			{ID: inspector, Name: "Harbourline Independent Surveyors", Roles: []party.Role{party.RoleSurveyor}, EntityRef: "ENT-FIC-HARBOURLINE"},
			{ID: bank, Name: "Fictional Trade Finance Bank", Roles: []party.Role{party.RoleBroker}},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "BL_QUANTITY", Subject: "BL-FIC-003-7712", Predicate: "states", Object: "cargo_quantity",
				Source: "shipping_documents", ObservedAt: 1000, DocumentType: "bill_of_lading",
				SourceParty: seller, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"stated_quantity_mt": "98500", "commodity": "bulk agricultural commodity"},
			},
			{
				Key: "PACKING_LIST", Subject: "PL-FIC-003-7712", Predicate: "states", Object: "cargo_quantity",
				Source: "shipping_documents", ObservedAt: 1000, DocumentType: "packing_list",
				SourceParty: seller, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"stated_quantity_mt": "98500"},
			},
			{
				Key: "INVOICE", Subject: "INV-FIC-003-7712", Predicate: "states", Object: "cargo_quantity",
				Source: "trade_documents", ObservedAt: 1002, DocumentType: "invoice",
				SourceParty: seller, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"stated_quantity_mt": "98500", "currency": "USD"},
			},
			{
				Key: "DRAUGHT_SURVEY", Subject: "SUR-FIC-003-7712", Predicate: "states", Object: "cargo_quantity",
				Source: "independent_draught_survey", ObservedAt: 1006, DocumentType: "survey_report",
				SourceParty: inspector, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"stated_quantity_mt": "97820", "method": "draught survey at load port"},
			},
			{
				Key: "CREDIT_TERMS", Subject: "LC-FIC-003-0044", Predicate: "establishes", Object: "documentary_credit_terms",
				Source: "bank_documents", ObservedAt: 900, DocumentType: "documentary_credit",
				SourceParty: bank, EvidenceOrigin: insevidence.OriginIndependent,
				// Deliberately INTERNAL_ONLY: the bank's credit terms are the
				// one restricted document in this case, so the pack exercises
				// the rights gate refusing a dispute or customer-facing use of
				// a document VERIQO holds but may not distribute.
				Rights:     provenance.RightsInternalOnly,
				Attributes: map[string]string{"tolerance_percent": "0.5"},
			},
		},
	}
}

// ---- CASE-INS-004 — General average versus war-risk cover -------------

// caseGeneralAverage is the Final Design §34 case, and it is the one
// that most needs its constraint restating.
//
// The design document says, in as many words: do not put a real
// judgment in as a "VERIQO decision", and do not hard-code a real
// judgment as a rule. What it asks for instead is exactly the shape
// below — a set of recorded FACTS, the contract clauses in play, the
// question, and the single output:
//
//	VERIQO: LEGAL_INTERPRETATION_REQUIRED
//
// A real reported decision on this category of question may be attached
// to the legal question as a dispute.HistoricalReference — reading
// material for the human who must answer it — and nothing in this
// package or any other consults it. No such reference is included here:
// the scenario is fully abstract, and the reference slot exists for a
// deployment that has a licensed case-law source.
func caseGeneralAverage() Case {
	const (
		shipowner = party.PartyID("PTY-004-SHIPOWNER")
		cargoInt  = party.PartyID("PTY-004-CARGO-INTEREST")
		charterer = party.PartyID("PTY-004-CHARTERER")
		adjuster  = party.PartyID("PTY-004-ADJUSTER")
		warRisk   = party.PartyID("PTY-004-WAR-RISK-INSURER")
	)
	return Case{
		ID:    CaseGeneralAverage,
		Title: "General average contribution where a war-risk arrangement is also in place",
		Narrative: "A vessel transiting a high-risk area in Jurisdiction C is detained. A ransom " +
			"is paid and the vessel released. A general average adjustment is issued calling for " +
			"contribution from the cargo interest. An additional war-risk premium had been paid " +
			"by the charterer, and the bill of lading contains an incorporation clause. The " +
			"question of whether the insurance arrangement displaces the general average " +
			"contribution is a question of law. The system records the facts and the clauses, " +
			"and answers LEGAL_INTERPRETATION_REQUIRED.",
		EngineeringCoverage: []string{
			"dispute matter with a recorded forum (governing law, jurisdiction, arbitration seat)",
			"legal question with the one-valued LEGAL_INTERPRETATION_REQUIRED status",
			"related factual issues moved to AWAITING_LEGAL_INTERPRETATION",
			"competing party positions recorded side by side, unreconciled",
			"historical-reference slot proven inert (carries no weight in any computation)",
		},
		ExpectedOutputs: []string{
			"LEGAL_INTERPRETATION_REQUIRED",
			"NO_VERIQO_LEGAL_DETERMINATION",
			"ISSUE_STATUS: AWAITING_LEGAL_INTERPRETATION",
		},
		Parties: []Party{
			{ID: shipowner, Name: "Northwind Bulk Owners Ltd", Roles: []party.Role{party.RoleShipowner}, EntityRef: "ENT-FIC-NORTHWIND"},
			{ID: cargoInt, Name: "Ridgeway Processing Co", Roles: []party.Role{party.RoleCargoOwner}, EntityRef: "ENT-FIC-RIDGEWAY"},
			{ID: charterer, Name: "Ardent Grain Charterers SA", Roles: []party.Role{party.RoleCharterer}, EntityRef: "ENT-FIC-ARDENT"},
			{ID: adjuster, Name: "Fictional Average Adjusters LLP", Roles: []party.Role{party.RoleLossAdjuster}},
			{ID: warRisk, Name: "Fictional War Risks Association", Roles: []party.Role{party.RoleInsurer}},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "DETENTION_RECORD", Subject: "MV_NORTHWIND_STAR", Predicate: "records", Object: "detention_event",
				Source: "owner_incident_report", ObservedAt: 1000, DocumentType: "incident_report",
				SourceParty: shipowner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"area": "declared high-risk area, Jurisdiction C"},
			},
			{
				Key: "RANSOM_PAYMENT", Subject: "MV_NORTHWIND_STAR", Predicate: "records", Object: "payment_made_for_release",
				Source: "owner_disbursement_account", ObservedAt: 1120, DocumentType: "disbursement_account",
				SourceParty: shipowner, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"currency": "USD", "recorded_amount": "fact recorded; no determination as to recoverability"},
			},
			{
				Key: "GA_ADJUSTMENT", Subject: "GA-FIC-004-01", Predicate: "issues", Object: "general_average_adjustment",
				Source: "average_adjuster_statement", ObservedAt: 1400, DocumentType: "average_adjustment",
				SourceParty: adjuster, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"calls_for": "contribution from cargo interest"},
			},
			{
				Key: "WAR_RISK_PREMIUM", Subject: "WR-FIC-004-77", Predicate: "records", Object: "additional_premium_paid",
				Source: "charterer_payment_record", ObservedAt: 980, DocumentType: "premium_record",
				SourceParty: charterer, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{"paid_by": "charterer", "cover": "war risks, transit of the declared area"},
			},
			{
				Key: "WAR_RISK_CLAUSE", Subject: "CP-FIC-004-021", Predicate: "contains", Object: "war_risk_clause",
				Source: "charterparty", ObservedAt: 900, DocumentType: "charterparty",
				SourceParty: charterer, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{"clause": "cl. 34", "governing_law": "the law of Jurisdiction A"},
			},
			{
				Key: "BL_INCORPORATION", Subject: "BL-FIC-004-5510", Predicate: "contains", Object: "incorporation_clause",
				Source: "shipping_documents", ObservedAt: 950, DocumentType: "bill_of_lading",
				SourceParty: cargoInt, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"clause": "cl. 1", "incorporates": "charterparty terms"},
			},
		},
	}
}

// ---- CASE-INS-005 — Bribery-risk intermediary -------------------------

// caseBriberyRisk is the Final Design §35 case, reproduced with its
// required output verbatim.
//
// The design document is emphatic about how this one is built: it is
// entirely GENERIC — an Intermediary, a PEP relationship, an unclear
// scope of work, an above-benchmark fee, a third-party bank account —
// and its own forbidden list names hard-coding a real company as a
// bribery classifier target. No real party appears here, and the
// scenario deliberately names its participants only by role.
//
// The required output, verbatim from §35:
//
//	HIGH_RISK_TRANSACTION
//	Reasons: 4 independent red flags.
//	Action: EDD + compliance review.
//	Conclusion: NO BRIBERY DETERMINATION.
func caseBriberyRisk() Case {
	const (
		principal    = party.PartyID("PTY-005-PRINCIPAL")
		intermediary = party.PartyID("PTY-005-INTERMEDIARY")
		compliance   = party.PartyID("PTY-005-COMPLIANCE")
		payingBank   = party.PartyID("PTY-005-PAYING-BANK")
	)
	return Case{
		ID:    CaseBriberyRisk,
		Title: "Intermediary payment request carrying multiple independent bribery-risk red flags",
		Narrative: "A principal is asked to pay an intermediary for services in Jurisdiction B. " +
			"Four independent red flags are present: a disclosed politically-exposed-person " +
			"relationship, a scope of work that does not describe deliverables, a fee materially " +
			"above the recorded benchmark, and a payment instruction to a bank account in the " +
			"name of a third party in a different jurisdiction. The system reports " +
			"HIGH_RISK_TRANSACTION, names each red flag with its evidence, and requires enhanced " +
			"due diligence. It makes NO bribery determination about anyone.",
		EngineeringCoverage: []string{
			"red-flag enumeration with per-flag evidence citation and no aggregate score",
			"three-way evidence decomposition (supporting / contradicting / missing)",
			"escalation to a compliance authority as a recorded review requirement",
			"structural proof that no determination field exists on any output type",
		},
		ExpectedOutputs: []string{
			"HIGH_RISK_TRANSACTION",
			"RED_FLAGS: 4 independent",
			"EDD_REQUIRED",
			"NO_BRIBERY_DETERMINATION",
		},
		Parties: []Party{
			{ID: principal, Name: "Ridgeway Processing Co", Roles: []party.Role{party.RoleCargoOwner}, EntityRef: "ENT-FIC-RIDGEWAY"},
			{ID: intermediary, Name: "Intermediary (role-designated; no real party)", Roles: []party.Role{party.RoleForwarder}},
			{ID: compliance, Name: "Compliance Function (internal)", Roles: []party.Role{party.RoleAuditor}},
			{ID: payingBank, Name: "Fictional Paying Bank", Roles: []party.Role{party.RoleBroker}},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "ENGAGEMENT_LETTER", Subject: "ENG-FIC-005-01", Predicate: "records", Object: "intermediary_engagement",
				Source: "contract_repository", ObservedAt: 1000, DocumentType: "engagement_letter",
				SourceParty: principal, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"scope_description": "assistance with local approvals; no deliverables specified"},
			},
			{
				Key: "PEP_DISCLOSURE", Subject: "ENG-FIC-005-01", Predicate: "discloses", Object: "pep_relationship",
				Source: "onboarding_questionnaire", ObservedAt: 1004, DocumentType: "due_diligence_record",
				SourceParty: compliance, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"red_flag": "RF-1", "nature": "declared relationship with a politically exposed person"},
			},
			{
				Key: "SCOPE_REVIEW", Subject: "ENG-FIC-005-01", Predicate: "assesses", Object: "scope_of_work",
				Source: "internal_scope_review", ObservedAt: 1010, DocumentType: "due_diligence_record",
				SourceParty: compliance, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"red_flag": "RF-2", "finding": "scope does not describe measurable deliverables"},
			},
			{
				Key: "FEE_BENCHMARK", Subject: "ENG-FIC-005-01", Predicate: "compares", Object: "fee_against_benchmark",
				Source: "internal_benchmark_table", ObservedAt: 1012, DocumentType: "benchmark_record",
				SourceParty: compliance, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"red_flag": "RF-3", "finding": "fee materially above the recorded internal benchmark"},
			},
			{
				Key: "PAYMENT_INSTRUCTION", Subject: "PAY-FIC-005-88", Predicate: "instructs", Object: "third_party_account_payment",
				Source: "payment_request", ObservedAt: 1020, DocumentType: "payment_instruction",
				SourceParty: intermediary, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{
					"red_flag": "RF-4",
					"finding":  "beneficiary account is in the name of a third party, in a jurisdiction other than the place of performance",
				},
			},
			{
				Key: "COUNTER_EVIDENCE", Subject: "ENG-FIC-005-01", Predicate: "records", Object: "intermediary_explanation",
				Source: "intermediary_correspondence", ObservedAt: 1030, DocumentType: "correspondence",
				SourceParty: intermediary, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{"content": "explanation offered for the account arrangement; not yet corroborated"},
			},
		},
	}
}

// ---- CASE-INS-006 — Regulatory settlement -----------------------------

// caseRegulatorySettlement is the Final Design §36 case, built to carry
// the two rules that section states as CRITICAL:
//
//	Settlement          ≠  every allegation proven
//	Monitor requirement ≠  monitor completed
//
// Both are enforced by pkg/insurance/regulatory; this case is the
// end-to-end proof that the enforcement holds when driven through the
// full chain.
func caseRegulatorySettlement() Case {
	const (
		respondent = party.PartyID("PTY-006-RESPONDENT")
		authority  = party.PartyID("PTY-006-AUTHORITY")
		monitor    = party.PartyID("PTY-006-MONITOR")
		insurer    = party.PartyID("PTY-006-INSURER")
	)
	return Case{
		ID:    CaseRegulatorySettlement,
		Title: "Regulatory matter resolved by settlement, with an ongoing monitorship",
		Narrative: "A supervisory authority in Jurisdiction B opens a matter against a company " +
			"following three allegations about record-keeping, escalation and third-party due " +
			"diligence. The matter resolves by settlement, with a fine, a disgorgement, and an " +
			"independent monitor required to report annually for three years. No allegation is " +
			"adjudicated. The system records the settlement as determining nothing, and reports " +
			"the monitorship as required and NOT certified complete.",
		EngineeringCoverage: []string{
			"regulatory chain: allegation -> investigation -> settlement -> fine -> disgorgement -> monitor",
			"settlement leaves every un-adjudicated allegation NOT_DETERMINED",
			"a prior genuine finding survives a subsequent settlement",
			"monitor requirement and monitor completion held as separate facts",
			"completion blockers naming each unfinished obligation",
		},
		ExpectedOutputs: []string{
			"SETTLEMENT_RECORDED",
			"PROVEN_ALLEGATIONS: none by settlement",
			"MONITOR_REQUIRED_NOT_CERTIFIED_COMPLETE",
			"COMPLETION_BLOCKED",
		},
		Parties: []Party{
			{ID: respondent, Name: "Silverline Commodities Pte", Roles: []party.Role{party.RoleRespondent}, EntityRef: "ENT-FIC-SILVERLINE"},
			{ID: authority, Name: "Supervisory Authority of Jurisdiction B (fictional)", Roles: []party.Role{party.RoleRegulator}},
			{ID: monitor, Name: "Fictional Independent Monitor LLP", Roles: []party.Role{party.RoleAuditor}},
			{ID: insurer, Name: "Meridian Marine Mutual", Roles: []party.Role{party.RoleInsurer}, EntityRef: "ENT-FIC-MERIDIAN"},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "ALLEGATION_NOTICE", Subject: "REG-FIC-006", Predicate: "alleges", Object: "three_allegations",
				Source: "authority_notice", ObservedAt: 1000, DocumentType: "regulatory_notice",
				SourceParty: authority, EvidenceOrigin: insevidence.OriginRegulatory,
				Attributes: map[string]string{"count": "3"},
			},
			{
				Key: "INVESTIGATION_REPORT", Subject: "REG-FIC-006", Predicate: "records", Object: "investigation",
				Source: "authority_investigation", ObservedAt: 1500, DocumentType: "regulatory_record",
				SourceParty: authority, EvidenceOrigin: insevidence.OriginRegulatory,
				Attributes: map[string]string{"status": "concluded without adjudication of the allegations"},
			},
			{
				Key: "FINDING_NOTICE", Subject: "REG-FIC-006", Predicate: "finds", Object: "record_keeping_deficiency",
				Source: "authority_finding", ObservedAt: 1600, DocumentType: "regulatory_finding",
				SourceParty: authority, EvidenceOrigin: insevidence.OriginRegulatory,
				Attributes: map[string]string{"paragraph": "para. 12", "scope": "record-keeping allegation only"},
			},
			{
				Key: "SETTLEMENT_ORDER", Subject: "REG-FIC-006", Predicate: "records", Object: "settlement",
				Source: "settlement_order", ObservedAt: 1800, DocumentType: "settlement_order",
				SourceParty: authority, EvidenceOrigin: insevidence.OriginRegulatory,
				Attributes: map[string]string{
					"without_admission": "the order records resolution without admission as to the remaining allegations",
					"monitor_clause":    "s. 7",
				},
			},
			{
				Key: "MONITOR_APPOINTMENT", Subject: "REG-FIC-006-MON", Predicate: "appoints", Object: "independent_monitor",
				Source: "monitor_appointment_letter", ObservedAt: 1820, DocumentType: "regulatory_record",
				SourceParty: monitor, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"term": "three annual reports"},
			},
		},
	}
}

// ---- CASE-INS-007 — Cross-border maritime dispute ---------------------

// caseCrossBorderDispute exercises the full I-08 dispute domain: a
// recorded forum with governing law, jurisdiction and arbitration seat;
// an evidence hold; competing party positions on the same issues; and a
// settlement that determines nothing.
func caseCrossBorderDispute() Case {
	const (
		claimant   = party.PartyID("PTY-007-CLAIMANT")
		respondent = party.PartyID("PTY-007-RESPONDENT")
		counsel    = party.PartyID("PTY-007-COUNSEL")
		insurer    = party.PartyID("PTY-007-INSURER")
	)
	return Case{
		ID:    CaseCrossBorderDispute,
		Title: "Cross-border maritime dispute referred to arbitration under a seated clause",
		Narrative: "A cargo claim between parties incorporated in different jurisdictions becomes " +
			"a dispute. The charterparty nominates the law of Jurisdiction A, the courts of " +
			"Jurisdiction A, and arbitration seated in Jurisdiction A. An evidence hold is placed. " +
			"Both sides record positions on the same three issues. The matter settles without any " +
			"allegation being adjudicated. The system records every step and determines nothing.",
		EngineeringCoverage: []string{
			"dispute forum with governing law, jurisdiction and arbitration seat, all source-cited",
			"dispute stage machine with a recorded skip (mediation not held)",
			"legal hold placed, evidence in scope recorded, hold released with reasons",
			"competing positions on shared issues held side by side",
			"settlement outcome proving nothing, from three directions",
			"preservation order under legal hold refusing release",
		},
		ExpectedOutputs: []string{
			"FORUM_RECORDED",
			"ISSUE_STATUS: CONTESTED",
			"SETTLEMENT_RECORDED",
			"PROVEN_ALLEGATIONS: none",
			"NO_LIABILITY_DETERMINATION",
		},
		Parties: []Party{
			{ID: claimant, Name: "Verdant Produce Importers BV", Roles: []party.Role{party.RoleClaimant, party.RoleCargoOwner}, EntityRef: "ENT-FIC-VERDANT"},
			{ID: respondent, Name: "Coastline Container Lines", Roles: []party.Role{party.RoleRespondent, party.RoleCarrier}, EntityRef: "ENT-FIC-COASTLINE"},
			{ID: counsel, Name: "Fictional Maritime Counsel LLP", Roles: []party.Role{party.RoleLegalCounsel}},
			{ID: insurer, Name: "Meridian Marine Mutual", Roles: []party.Role{party.RoleInsurer}, EntityRef: "ENT-FIC-MERIDIAN"},
		},
		Evidence: []EvidenceSpec{
			{
				Key: "CHARTERPARTY_007", Subject: "CP-FIC-007-330", Predicate: "establishes", Object: "governing_law_and_forum",
				Source: "contract_repository", ObservedAt: 900, DocumentType: "charterparty",
				SourceParty: respondent, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{
					"clause":           "cl. 41",
					"governing_law":    "the law of Jurisdiction A",
					"jurisdiction":     "the courts of Jurisdiction A",
					"arbitration_seat": "Seat City, Jurisdiction A",
				},
			},
			{
				Key: "NOTICE_OF_DISPUTE", Subject: "DISP-FIC-007", Predicate: "notifies", Object: "dispute",
				Source: "claimant_notice", ObservedAt: 2000, DocumentType: "notice_letter",
				SourceParty: claimant, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"issues_raised": "3"},
			},
			{
				Key: "HOLD_INSTRUCTION", Subject: "DISP-FIC-007", Predicate: "instructs", Object: "evidence_hold",
				Source: "counsel_instruction", ObservedAt: 2010, DocumentType: "legal_instruction",
				SourceParty: counsel, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"scope": "all voyage, cargo and correspondence records for the disputed carriage"},
			},
			{
				Key: "CLAIMANT_POSITION", Subject: "DISP-FIC-007", Predicate: "states", Object: "claimant_position",
				Source: "statement_of_claim", ObservedAt: 2100, DocumentType: "pleading",
				SourceParty: claimant, EvidenceOrigin: insevidence.OriginClaimant,
				Attributes: map[string]string{"summary": "carriage condition and delivery condition differ materially"},
			},
			{
				Key: "RESPONDENT_POSITION", Subject: "DISP-FIC-007", Predicate: "states", Object: "respondent_position",
				Source: "statement_of_defence", ObservedAt: 2200, DocumentType: "pleading",
				SourceParty: respondent, EvidenceOrigin: insevidence.OriginRespondent,
				Attributes: map[string]string{"summary": "condition on delivery is consistent with the recorded pre-shipment condition"},
			},
			{
				Key: "SETTLEMENT_AGREEMENT", Subject: "DISP-FIC-007", Predicate: "records", Object: "settlement",
				Source: "settlement_agreement", ObservedAt: 2600, DocumentType: "settlement_agreement",
				SourceParty: counsel, EvidenceOrigin: insevidence.OriginIndependent,
				Attributes: map[string]string{"terms": "compromised without admission; no allegation adjudicated"},
			},
		},
	}
}
