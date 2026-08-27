// Package maritime implements the Maritime domain OS pipeline.
// This is the VEP-026 Maritime domain, wired through the OS interface.
// Pipeline: AIS/SAR ingest → Maritime Engine → Risk → Compliance → Decision → Digital Twin.
package maritime

import (
	"context"
	"fmt"
	"time"

	"veriqo/pkg/core"
	vos "veriqo/pkg/os"
)

// ─── Maritime domain types ────────────────────────────────────────────────────

// VesselType classifies a vessel.
type VesselType string

const (
	VesselTypeCargo     VesselType = "cargo"
	VesselTypeTanker    VesselType = "tanker"
	VesselTypePassenger VesselType = "passenger"
	VesselTypeFishing   VesselType = "fishing"
	VesselTypeVLCC      VesselType = "vlcc"
	VesselTypeUnknown   VesselType = "unknown"
)

// AISEvent is a real-time vessel position report from AIS feed.
type AISEvent struct {
	MMSI       string
	IMO        string
	VesselName string
	VesselType VesselType
	Latitude   float64
	Longitude  float64
	Speed      float64 // knots
	Heading    float64 // degrees
	PortCallID string  // if at port
	Timestamp  time.Time
}

// SAREvent is a synthetic aperture radar detection event.
type SAREvent struct {
	DetectionID string
	Latitude    float64
	Longitude   float64
	Length      float64 // metres
	Confidence  float64 // 0–1
	Timestamp   time.Time
}

// PortCall is a vessel port call record.
type PortCall struct {
	CallID       string
	MMSI         string
	PortUNLOCode string
	PortName     string
	Country      string
	ATA          time.Time // actual time arrival
	ATD          time.Time // actual time departure
	Sanctioned   bool
}

// SanctionedPorts is the set of sanctioned port UNLO codes.
// In production this is loaded from OFAC/UN sanctions databases.
var SanctionedPorts = map[string]bool{
	"IRBND": true, // Bandar Abbas, Iran
	"SYRLT": true, // Latakia, Syria
	"CUBHA": true, // Havana, Cuba
	"KPNAM": true, // Nampo, North Korea
}

// ─── Maritime risk scores ─────────────────────────────────────────────────────

// VesselRiskFactors computes risk factor scores for a vessel.
func VesselRiskFactors(ais AISEvent, portCall *PortCall) map[string]float64 {
	factors := map[string]float64{}

	// Sanctioned port call.
	if portCall != nil && SanctionedPorts[portCall.PortUNLOCode] {
		factors["sanctioned_port"] = 1.0
	}

	// AIS dark period (speed=0 + heading unknown can indicate intentional AIS off).
	if ais.Speed == 0 && ais.Heading == 0 {
		factors["ais_anomaly"] = 0.6
	}

	// High speed in port area.
	if ais.Speed > 15 && portCall != nil {
		factors["speed_anomaly"] = 0.4
	}

	// Unknown vessel type increases uncertainty.
	if ais.VesselType == VesselTypeUnknown {
		factors["unknown_vessel"] = 0.3
	}

	return factors
}

// ─── Maritime Pipeline ────────────────────────────────────────────────────────

// Pipeline is the end-to-end maritime trust pipeline.
// It processes AIS/SAR events through risk → compliance → decision → digital twin.
type Pipeline struct {
	os       vos.OS
	tenantID core.TenantID
}

// NewPipeline creates a maritime Pipeline.
func NewPipeline(os vos.OS, tenantID core.TenantID) *Pipeline {
	return &Pipeline{os: os, tenantID: tenantID}
}

// PipelineResult is the full output of processing one maritime event.
type PipelineResult struct {
	ProcessID        core.ProcessID
	AISEvent         AISEvent
	RiskResult       vos.RiskResult
	ComplianceResult vos.ComplianceResult
	Decision         core.Decision
	TwinHash         vos.TwinStateHash
	DARI             core.DARI
}

// Process runs an AIS event through the full maritime trust pipeline.
// Every step is evidenced; the result is deterministic for the same input.
func (p *Pipeline) Process(ctx context.Context, event AISEvent, portCall *PortCall) (*PipelineResult, error) {
	traceID := core.NewTraceID()

	// 1. Start OS process.
	pid, err := p.os.StartProcess(ctx, vos.ProcessSpec{
		TenantID: p.tenantID,
		DomainID: "maritime",
		Name:     fmt.Sprintf("maritime-vessel-%s", event.MMSI),
		Priority: 7,
		Metadata: map[string]string{
			"mmsi":   event.MMSI,
			"vessel": event.VesselName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("maritime: start process: %w", err)
	}

	// 2. Append AIS ingestion evidence.
	_, err = p.os.AppendEvidence(ctx, pid, vos.EvidenceInput{
		Kind:    "maritime.ais.ingest",
		Payload: []byte(fmt.Sprintf(`{"mmsi":%q,"lat":%f,"lon":%f}`, event.MMSI, event.Latitude, event.Longitude)),
		Meta:    map[string]string{"trace_id": traceID.String()},
	})
	if err != nil {
		return nil, fmt.Errorf("maritime: append evidence: %w", err)
	}

	// 3. Compute risk factors and evaluate risk.
	factors := VesselRiskFactors(event, portCall)
	risk, err := p.os.EvaluateRisk(ctx, pid, vos.RiskInput{
		EntityID:   event.MMSI,
		EntityType: "vessel",
		Factors:    factors,
	})
	if err != nil {
		return nil, fmt.Errorf("maritime: evaluate risk: %w", err)
	}

	// 4. Assess compliance (sanction rules).
	policyID := "maritime.sanction.policy"
	sanctioned := portCall != nil && SanctionedPorts[portCall.PortUNLOCode]
	compInput := vos.ComplianceInput{
		PolicyID: policyID,
		Subject:  map[string]any{"mmsi": event.MMSI, "vessel_type": string(event.VesselType)},
		Resource: map[string]any{"port": ""},
		Action:   "port_call",
	}
	if portCall != nil {
		compInput.Resource["port"] = portCall.PortUNLOCode
		compInput.Resource["sanctioned"] = sanctioned
	}
	compliance, err := p.os.AssessCompliance(ctx, pid, compInput)
	if err != nil {
		return nil, fmt.Errorf("maritime: assess compliance: %w", err)
	}

	// 5. Produce decision.
	decision, err := p.os.Decide(ctx, pid, vos.DecisionInput{
		Kind:    "maritime.vessel.decision",
		Factors: map[string]any{"risk_score": risk.Score},
	})
	if err != nil {
		return nil, fmt.Errorf("maritime: decide: %w", err)
	}

	// 6. Update digital twin world state.
	status := "at-sea"
	if portCall != nil {
		status = "at-port"
		if sanctioned {
			status = "sanctioned-port"
		}
	}
	twinHash, err := p.os.UpdateDigitalTwin(ctx, pid, vos.TwinDelta{
		EntityID: event.MMSI,
		Properties: map[string]any{
			"vessel_name": event.VesselName,
			"status":      status,
			"risk_score":  risk.Score,
			"lat":         event.Latitude,
			"lon":         event.Longitude,
			"approved":    decision.Approved,
		},
		Timestamp: event.Timestamp,
	})
	if err != nil {
		return nil, fmt.Errorf("maritime: update twin: %w", err)
	}

	return &PipelineResult{
		ProcessID:        pid,
		AISEvent:         event,
		RiskResult:       risk,
		ComplianceResult: compliance,
		Decision:         decision,
		TwinHash:         twinHash,
		DARI: core.DARI{
			TraceID:          traceID,
			ExecutionGraphID: "maritime-pipeline-v1",
			Timestamp:        time.Now().UTC(),
		},
	}, nil
}
