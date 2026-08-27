// Package trade implements the Trade domain OS pipeline.
// VEP-031: Trade — trade contracts → Contract Engine → Risk/Compliance → Ledger/Evidence
// → Digital Twin of contracts.
package trade

import (
	"context"
	"fmt"
	"time"

	"veriqo/pkg/core"
	vos "veriqo/pkg/os"
)

// ContractStatus is the lifecycle state of a trade contract.
type ContractStatus string

const (
	ContractDraft      ContractStatus = "draft"
	ContractActive     ContractStatus = "active"
	ContractDisputed   ContractStatus = "disputed"
	ContractCompleted  ContractStatus = "completed"
	ContractTerminated ContractStatus = "terminated"
)

// PaymentMethod classifies the settlement mechanism.
type PaymentMethod string

const (
	PaymentLetterOfCredit PaymentMethod = "letter_of_credit"
	PaymentDocCollection  PaymentMethod = "documentary_collection"
	PaymentOpenAccount    PaymentMethod = "open_account"
	PaymentAdvance        PaymentMethod = "advance_payment"
)

// TradeContract is a bilateral trade agreement.
type TradeContract struct {
	ContractID       string
	BuyerID          string
	SellerID         string
	BuyerCountry     string
	SellerCountry    string
	CommodityType    string
	VolumeMetricTons float64
	PricePerTon      float64
	CurrencyCode     string
	PaymentMethod    PaymentMethod
	Incoterms        string // e.g. "CIF", "FOB", "DDP"
	Status           ContractStatus
	SignedAt         time.Time
	DeliveryDeadline time.Time
}

// ContractRiskFactors computes risk factors for a trade contract.
func ContractRiskFactors(contract TradeContract) map[string]float64 {
	factors := map[string]float64{}

	restrictedCountries := map[string]bool{"IR": true, "KP": true, "SY": true}
	if restrictedCountries[contract.BuyerCountry] || restrictedCountries[contract.SellerCountry] {
		factors["restricted_counterparty"] = 1.0
	}

	// Open account with unknown buyer is high risk.
	if contract.PaymentMethod == PaymentOpenAccount && contract.BuyerID == "" {
		factors["payment_risk"] = 0.7
	}

	// Contract value risk.
	totalValue := contract.VolumeMetricTons * contract.PricePerTon
	switch {
	case totalValue > 100_000_000:
		factors["high_value"] = 0.6
	case totalValue > 10_000_000:
		factors["medium_value"] = 0.3
	}

	// Disputed contract.
	if contract.Status == ContractDisputed {
		factors["dispute"] = 0.8
	}

	return factors
}

// ─── Pipeline ─────────────────────────────────────────────────────────────────

// Pipeline is the end-to-end trade trust pipeline.
type Pipeline struct {
	os       vos.OS
	tenantID core.TenantID
}

// NewPipeline creates a trade Pipeline.
func NewPipeline(os vos.OS, tenantID core.TenantID) *Pipeline {
	return &Pipeline{os: os, tenantID: tenantID}
}

// PipelineResult is the output of processing one trade contract event.
type PipelineResult struct {
	ProcessID        core.ProcessID
	Contract         TradeContract
	RiskResult       vos.RiskResult
	ComplianceResult vos.ComplianceResult
	Decision         core.Decision
	TwinHash         vos.TwinStateHash
	DARI             core.DARI
}

// Process runs a trade contract through the full trade pipeline.
func (p *Pipeline) Process(ctx context.Context, contract TradeContract) (*PipelineResult, error) {
	traceID := core.NewTraceID()

	pid, err := p.os.StartProcess(ctx, vos.ProcessSpec{
		TenantID: p.tenantID,
		DomainID: "trade",
		Name:     fmt.Sprintf("trade-contract-%s", contract.ContractID),
		Priority: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("trade: start process: %w", err)
	}

	_, _ = p.os.AppendEvidence(ctx, pid, vos.EvidenceInput{
		Kind:    "trade.contract.ingest",
		Payload: []byte(fmt.Sprintf(`{"contract_id":%q,"status":%q}`, contract.ContractID, contract.Status)),
	})

	factors := ContractRiskFactors(contract)
	risk, err := p.os.EvaluateRisk(ctx, pid, vos.RiskInput{
		EntityID:   contract.ContractID,
		EntityType: "trade_contract",
		Factors:    factors,
	})
	if err != nil {
		return nil, fmt.Errorf("trade: risk: %w", err)
	}

	compliance, err := p.os.AssessCompliance(ctx, pid, vos.ComplianceInput{
		PolicyID: "trade.contract.sanctions.policy",
		Subject:  map[string]any{"buyer": contract.BuyerID, "seller": contract.SellerID},
		Resource: map[string]any{"commodity": contract.CommodityType},
		Action:   "execute_contract",
	})
	if err != nil {
		return nil, fmt.Errorf("trade: compliance: %w", err)
	}

	decision, err := p.os.Decide(ctx, pid, vos.DecisionInput{
		Kind:    "trade.contract.decision",
		Factors: map[string]any{"risk_score": risk.Score},
	})
	if err != nil {
		return nil, fmt.Errorf("trade: decide: %w", err)
	}

	twinHash, err := p.os.UpdateDigitalTwin(ctx, pid, vos.TwinDelta{
		EntityID: contract.ContractID,
		Properties: map[string]any{
			"status":     string(contract.Status),
			"risk_score": risk.Score,
			"compliant":  compliance.Compliant,
			"approved":   decision.Approved,
			"buyer":      contract.BuyerID,
			"seller":     contract.SellerID,
		},
		Timestamp: contract.SignedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("trade: twin: %w", err)
	}

	return &PipelineResult{
		ProcessID:        pid,
		Contract:         contract,
		RiskResult:       risk,
		ComplianceResult: compliance,
		Decision:         decision,
		TwinHash:         twinHash,
		DARI: core.DARI{
			TraceID:          traceID,
			ExecutionGraphID: "trade-pipeline-v1",
			Timestamp:        time.Now().UTC(),
		},
	}, nil
}
