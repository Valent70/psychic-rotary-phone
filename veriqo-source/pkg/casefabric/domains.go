package casefabric

// This file is the fabric's answer to the question the architecture kept
// dodging: are the domains actually one system?
//
// Each projection below maps a domain's real state vocabulary onto the
// canonical phases. The mappings are asserted by tests against the
// domains' own state lists, so a domain that adds a state without
// mapping it fails the build rather than quietly drifting off the
// fabric.
//
// The projections are registered at init because the set of domains is a
// property of this deployment. A deployment that ships fewer domains
// registers fewer; nothing here is optional decoration.

// Domain names, canonical.
const (
	DomainInsurance    = "insurance"
	DomainMaritime     = "maritime"
	DomainCommodity    = "commodity"
	DomainSupplyChain  = "supplychain"
	DomainTradeFinance = "tradefinance"
	DomainDispute      = "dispute"
)

// insuranceProjection maps pkg/insurance/casestate's fourteen states.
//
// Note where the money states land: PAYMENT_AUTHORIZED and
// PAYMENT_EXECUTED are both PhaseResolved, because by the time VERIQO
// authorizes a payment the epistemic work is done — paying is an
// operational consequence of a resolved case, not a further step in
// establishing it.
var insuranceProjection = Projection{
	Domain:           DomainInsurance,
	CanonicalPackage: "veriqo/pkg/insurance/casestate",
	StateToPhase: map[string]Phase{
		"INVITED":                PhaseOpened,
		"ACCEPTED":               PhaseScoped,
		"EVIDENCE_EXCHANGED":     PhaseEvidenceGathering,
		"UNDER_REVIEW":           PhaseHypothesesFormed,
		"CLARIFICATION_REQUIRED": PhaseEvidenceGathering,
		"QUANTIFIED":             PhaseUnderQualification,
		"RESERVED":               PhaseUnderQualification,
		"PAYMENT_AUTHORIZED":     PhaseResolved,
		"PAYMENT_EXECUTED":       PhaseResolved,
		"RECOVERY_OPEN":          PhaseReopened,
		"RECOVERY_RESOLVED":      PhaseResolved,
		"DISPUTED":               PhaseUnderQualification,
		"CLOSED":                 PhaseClosed,
		"REOPENED":               PhaseReopened,
	},
}

var maritimeProjection = Projection{
	Domain:           DomainMaritime,
	CanonicalPackage: "veriqo/pkg/domain/maritime",
	StateToPhase: map[string]Phase{
		"INCIDENT_REPORTED":  PhaseOpened,
		"INVESTIGATION_OPEN": PhaseScoped,
		"EVIDENCE_SECURED":   PhaseEvidenceGathering,
		"CAUSE_HYPOTHESISED": PhaseHypothesesFormed,
		"UNDER_ANALYSIS":     PhaseUnderQualification,
		"FINDINGS_ISSUED":    PhaseResolved,
		"SUSPENDED":          PhaseSuspended,
		"CLOSED":             PhaseClosed,
	},
}

var commodityProjection = Projection{
	Domain:           DomainCommodity,
	CanonicalPackage: "veriqo/pkg/domain/commodity",
	StateToPhase: map[string]Phase{
		"CONSIGNMENT_FLAGGED":  PhaseOpened,
		"SCOPE_AGREED":         PhaseScoped,
		"SAMPLES_COLLECTED":    PhaseEvidenceGathering,
		"CONTAMINATION_THEORY": PhaseHypothesesFormed,
		"ASSAY_UNDER_REVIEW":   PhaseUnderQualification,
		"QUALITY_DETERMINED":   PhaseResolved,
		"SUSPENDED":            PhaseSuspended,
		"CLOSED":               PhaseClosed,
	},
}

var supplyChainProjection = Projection{
	Domain:           DomainSupplyChain,
	CanonicalPackage: "veriqo/pkg/domain/supplychain",
	StateToPhase: map[string]Phase{
		"DISRUPTION_DETECTED": PhaseOpened,
		"IMPACT_SCOPED":       PhaseScoped,
		"TRACE_COLLECTED":     PhaseEvidenceGathering,
		"ORIGIN_HYPOTHESISED": PhaseHypothesesFormed,
		"UNDER_ATTRIBUTION":   PhaseUnderQualification,
		"ORIGIN_ESTABLISHED":  PhaseResolved,
		"SUSPENDED":           PhaseSuspended,
		"CLOSED":              PhaseClosed,
	},
}

var tradeFinanceProjection = Projection{
	Domain:           DomainTradeFinance,
	CanonicalPackage: "veriqo/pkg/domain/trade",
	StateToPhase: map[string]Phase{
		"PRESENTATION_RECEIVED": PhaseOpened,
		"TERMS_SCOPED":          PhaseScoped,
		"DOCUMENTS_EXAMINED":    PhaseEvidenceGathering,
		"DISCREPANCY_ALLEGED":   PhaseHypothesesFormed,
		"UNDER_DETERMINATION":   PhaseUnderQualification,
		"DETERMINATION_ISSUED":  PhaseResolved,
		"SUSPENDED":             PhaseSuspended,
		"CLOSED":                PhaseClosed,
	},
}

// disputeProjection is deliberately shaped so that no state means
// "decided". A VERIQO dispute case ends by delivering an evidence
// package to whoever decides; it never ends by deciding.
var disputeProjection = Projection{
	Domain:           DomainDispute,
	CanonicalPackage: "veriqo/pkg/insurance/dispute",
	StateToPhase: map[string]Phase{
		"MATTER_NOTIFIED":            PhaseOpened,
		"ISSUES_FRAMED":              PhaseScoped,
		"DISCLOSURE_EXCHANGED":       PhaseEvidenceGathering,
		"POSITIONS_STATED":           PhaseHypothesesFormed,
		"EVIDENCE_UNDER_ASSESSMENT":  PhaseUnderQualification,
		"EVIDENCE_PACKAGE_DELIVERED": PhaseResolved,
		"SUSPENDED":                  PhaseSuspended,
		"CLOSED":                     PhaseClosed,
	},
}

// CanonicalProjections returns the projections this deployment ships.
func CanonicalProjections() []Projection {
	return []Projection{
		insuranceProjection, maritimeProjection, commodityProjection,
		supplyChainProjection, tradeFinanceProjection, disputeProjection,
	}
}

func init() {
	for _, p := range CanonicalProjections() {
		if err := Register(p); err != nil {
			// A malformed built-in projection is a programming error in
			// this file, not a runtime condition: fail loudly at start
			// rather than serving a fabric with a domain missing.
			panic("casefabric: built-in projection is invalid: " + err.Error())
		}
	}
}
