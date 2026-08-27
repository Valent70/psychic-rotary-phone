// Package supplychain implements the Supply Chain domain OS pipeline.
// VEP-028: SupplyChain — shipment events → SupplyChain Engine → Risk (delay/route)
// → Compliance (export/import) → Digital Twin logistic world state.
package supplychain

import (
	"context"
	"fmt"
	"time"

	"veriqo/pkg/core"
	vos "veriqo/pkg/os"
)

// ShipmentStatus is the current status of a shipment.
type ShipmentStatus string

const (
	ShipmentPending   ShipmentStatus = "pending"
	ShipmentInTransit ShipmentStatus = "in_transit"
	ShipmentDelayed   ShipmentStatus = "delayed"
	ShipmentDelivered ShipmentStatus = "delivered"
	ShipmentHeld      ShipmentStatus = "held_customs"
)

// ShipmentEvent is a logistics event in the supply chain.
type ShipmentEvent struct {
	ShipmentID         string
	CargoType          string
	OriginPort         string
	DestinationPort    string
	OriginCountry      string
	DestinationCountry string
	CarrierID          string
	EstimatedArrival   time.Time
	ActualArrival      time.Time
	DelayHours         float64
	Status             ShipmentStatus
	ValueUSD           float64
	WeightKG           float64
	DualUseGoods       bool // export control flag
	Timestamp          time.Time
}

// ShipmentRiskFactors computes risk factors for a shipment.
func ShipmentRiskFactors(event ShipmentEvent) map[string]float64 {
	factors := map[string]float64{}

	// Significant delay.
	if event.DelayHours > 48 {
		factors["delay_risk"] = minF(event.DelayHours/240.0, 1.0)
	}

	// Dual-use goods require export control compliance.
	if event.DualUseGoods {
		factors["dual_use"] = 0.8
	}

	// Route through high-risk regions.
	highRiskCountries := map[string]bool{"IR": true, "KP": true, "SY": true, "VE": true}
	if highRiskCountries[event.OriginCountry] || highRiskCountries[event.DestinationCountry] {
		factors["high_risk_route"] = 0.9
	}

	// Customs hold.
	if event.Status == ShipmentHeld {
		factors["customs_hold"] = 0.7
	}

	// High value with unknown carrier.
	if event.ValueUSD > 1_000_000 && event.CarrierID == "" {
		factors["unknown_carrier"] = 0.5
	}

	return factors
}

// ─── Pipeline ─────────────────────────────────────────────────────────────────

// Pipeline is the end-to-end supply chain trust pipeline.
type Pipeline struct {
	os       vos.OS
	tenantID core.TenantID
}

// NewPipeline creates a supply chain Pipeline.
func NewPipeline(os vos.OS, tenantID core.TenantID) *Pipeline {
	return &Pipeline{os: os, tenantID: tenantID}
}

// PipelineResult is the output of processing one shipment event.
type PipelineResult struct {
	ProcessID        core.ProcessID
	ShipmentEvent    ShipmentEvent
	RiskResult       vos.RiskResult
	ComplianceResult vos.ComplianceResult
	Decision         core.Decision
	TwinHash         vos.TwinStateHash
	DARI             core.DARI
}

// Process runs a shipment event through the full supply chain pipeline.
func (p *Pipeline) Process(ctx context.Context, event ShipmentEvent) (*PipelineResult, error) {
	traceID := core.NewTraceID()

	pid, err := p.os.StartProcess(ctx, vos.ProcessSpec{
		TenantID: p.tenantID,
		DomainID: "supplychain",
		Name:     fmt.Sprintf("shipment-%s", event.ShipmentID),
		Priority: 6,
	})
	if err != nil {
		return nil, fmt.Errorf("supplychain: start process: %w", err)
	}

	_, _ = p.os.AppendEvidence(ctx, pid, vos.EvidenceInput{
		Kind:    "supplychain.shipment.ingest",
		Payload: []byte(fmt.Sprintf(`{"shipment_id":%q,"status":%q}`, event.ShipmentID, event.Status)),
	})

	factors := ShipmentRiskFactors(event)
	risk, err := p.os.EvaluateRisk(ctx, pid, vos.RiskInput{
		EntityID:   event.ShipmentID,
		EntityType: "shipment",
		Factors:    factors,
	})
	if err != nil {
		return nil, fmt.Errorf("supplychain: risk: %w", err)
	}

	compliance, err := p.os.AssessCompliance(ctx, pid, vos.ComplianceInput{
		PolicyID: "supplychain.export.control.policy",
		Subject:  map[string]any{"carrier": event.CarrierID},
		Resource: map[string]any{"cargo": event.CargoType, "dual_use": event.DualUseGoods},
		Action:   "ship",
	})
	if err != nil {
		return nil, fmt.Errorf("supplychain: compliance: %w", err)
	}

	decision, err := p.os.Decide(ctx, pid, vos.DecisionInput{
		Kind:    "supplychain.shipment.decision",
		Factors: map[string]any{"risk_score": risk.Score},
	})
	if err != nil {
		return nil, fmt.Errorf("supplychain: decide: %w", err)
	}

	twinHash, err := p.os.UpdateDigitalTwin(ctx, pid, vos.TwinDelta{
		EntityID: event.ShipmentID,
		Properties: map[string]any{
			"status":      string(event.Status),
			"risk_score":  risk.Score,
			"compliant":   compliance.Compliant,
			"approved":    decision.Approved,
			"delay_hours": event.DelayHours,
		},
		Timestamp: event.Timestamp,
	})
	if err != nil {
		return nil, fmt.Errorf("supplychain: twin: %w", err)
	}

	return &PipelineResult{
		ProcessID:        pid,
		ShipmentEvent:    event,
		RiskResult:       risk,
		ComplianceResult: compliance,
		Decision:         decision,
		TwinHash:         twinHash,
		DARI: core.DARI{
			TraceID:          traceID,
			ExecutionGraphID: "supplychain-pipeline-v1",
			Timestamp:        time.Now().UTC(),
		},
	}, nil
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
