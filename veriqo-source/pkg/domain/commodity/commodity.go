// Package commodity implements the Commodity domain OS pipeline.
// VEP-027: Commodity — trade flow events → Commodity Engine → Risk/Compliance → Digital Twin.
package commodity

import (
	"context"
	"fmt"
	"time"

	"veriqo/pkg/core"
	vos "veriqo/pkg/os"
)

// ─── Commodity types ──────────────────────────────────────────────────────────

// CommodityType classifies the commodity.
type CommodityType string

const (
	CommodityOil      CommodityType = "crude_oil"
	CommodityGas      CommodityType = "lng"
	CommodityGrain    CommodityType = "grain"
	CommodityMetal    CommodityType = "metal"
	CommodityChemical CommodityType = "chemical"
)

// TradeFlowEvent represents a commodity trade flow.
type TradeFlowEvent struct {
	TradeID            string
	CommodityType      CommodityType
	OriginCountry      string
	DestinationCountry string
	VolumeMetricTons   float64
	ValueUSD           float64
	SellerEntityID     string
	BuyerEntityID      string
	Timestamp          time.Time
}

// RestrictedCountries is the set of countries under trade restrictions.
// In production this is loaded from the relevant sanctions databases.
var RestrictedCountries = map[string]bool{
	"IR": true, // Iran
	"KP": true, // North Korea
	"SY": true, // Syria
	"CU": true, // Cuba
	"RU": true, // Russia (OFAC SDN)
}

// TradeFlowRiskFactors computes risk factors for a commodity trade flow.
func TradeFlowRiskFactors(event TradeFlowEvent) map[string]float64 {
	factors := map[string]float64{}

	if RestrictedCountries[event.OriginCountry] {
		factors["restricted_origin"] = 1.0
	}
	if RestrictedCountries[event.DestinationCountry] {
		factors["restricted_destination"] = 0.9
	}
	// Abnormally large volume.
	if event.VolumeMetricTons > 500_000 {
		factors["large_volume"] = 0.4
	}
	// High value with no volume (suspicious valuation).
	if event.ValueUSD > 0 && event.VolumeMetricTons == 0 {
		factors["value_mismatch"] = 0.7
	}
	// Oil to restricted destination via certain routes.
	if event.CommodityType == CommodityOil && (RestrictedCountries[event.DestinationCountry] || RestrictedCountries[event.OriginCountry]) {
		factors["oil_sanction"] = 1.0
	}

	return factors
}

// ─── Pipeline ─────────────────────────────────────────────────────────────────

// Pipeline is the end-to-end commodity trust pipeline.
type Pipeline struct {
	os       vos.OS
	tenantID core.TenantID
}

// NewPipeline creates a commodity Pipeline.
func NewPipeline(os vos.OS, tenantID core.TenantID) *Pipeline {
	return &Pipeline{os: os, tenantID: tenantID}
}

// PipelineResult is the output of processing one commodity trade flow event.
type PipelineResult struct {
	ProcessID        core.ProcessID
	TradeFlowEvent   TradeFlowEvent
	RiskResult       vos.RiskResult
	ComplianceResult vos.ComplianceResult
	Decision         core.Decision
	TwinHash         vos.TwinStateHash
	DARI             core.DARI
}

// Process runs a commodity trade flow event through the pipeline.
func (p *Pipeline) Process(ctx context.Context, event TradeFlowEvent) (*PipelineResult, error) {
	traceID := core.NewTraceID()

	pid, err := p.os.StartProcess(ctx, vos.ProcessSpec{
		TenantID: p.tenantID,
		DomainID: "commodity",
		Name:     fmt.Sprintf("commodity-trade-%s", event.TradeID),
		Priority: 6,
	})
	if err != nil {
		return nil, fmt.Errorf("commodity: start process: %w", err)
	}

	_, _ = p.os.AppendEvidence(ctx, pid, vos.EvidenceInput{
		Kind:    "commodity.trade.ingest",
		Payload: []byte(fmt.Sprintf(`{"trade_id":%q,"commodity":%q}`, event.TradeID, event.CommodityType)),
	})

	factors := TradeFlowRiskFactors(event)
	risk, err := p.os.EvaluateRisk(ctx, pid, vos.RiskInput{
		EntityID:   event.TradeID,
		EntityType: "trade_flow",
		Factors:    factors,
	})
	if err != nil {
		return nil, fmt.Errorf("commodity: risk: %w", err)
	}

	compliance, err := p.os.AssessCompliance(ctx, pid, vos.ComplianceInput{
		PolicyID: "commodity.trade.sanctions.policy",
		Subject:  map[string]any{"seller": event.SellerEntityID, "buyer": event.BuyerEntityID},
		Resource: map[string]any{"commodity": string(event.CommodityType), "origin": event.OriginCountry},
		Action:   "trade",
	})
	if err != nil {
		return nil, fmt.Errorf("commodity: compliance: %w", err)
	}

	decision, err := p.os.Decide(ctx, pid, vos.DecisionInput{
		Kind:    "commodity.trade.decision",
		Factors: map[string]any{"risk_score": risk.Score},
	})
	if err != nil {
		return nil, fmt.Errorf("commodity: decide: %w", err)
	}

	twinHash, err := p.os.UpdateDigitalTwin(ctx, pid, vos.TwinDelta{
		EntityID: event.TradeID,
		Properties: map[string]any{
			"commodity":  string(event.CommodityType),
			"risk_score": risk.Score,
			"compliant":  compliance.Compliant,
			"approved":   decision.Approved,
		},
		Timestamp: event.Timestamp,
	})
	if err != nil {
		return nil, fmt.Errorf("commodity: twin: %w", err)
	}

	return &PipelineResult{
		ProcessID:        pid,
		TradeFlowEvent:   event,
		RiskResult:       risk,
		ComplianceResult: compliance,
		Decision:         decision,
		TwinHash:         twinHash,
		DARI: core.DARI{
			TraceID:          traceID,
			ExecutionGraphID: "commodity-pipeline-v1",
			Timestamp:        time.Now().UTC(),
		},
	}, nil
}
