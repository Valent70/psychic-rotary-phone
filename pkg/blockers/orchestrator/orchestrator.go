// Package orchestrator runs all eight blockers' fixture qualification
// pipelines against one blocker registry and produces one combined
// report. It is the shared core behind cmd/veriqo-qualification and
// test/e2e/eight_blockers: the command wraps it for a human-readable
// CLI run, the e2e test calls it directly with Overrides to inject a
// single blocker's failure and confirm the other seven are unaffected.
package orchestrator

import (
	"context"
	"fmt"

	"veriqo/pkg/blockers"
	"veriqo/pkg/blockers/dr"
	"veriqo/pkg/blockers/hsmkms"
	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/blockers/pentest"
	"veriqo/pkg/blockers/scale"
	"veriqo/pkg/blockers/soak"
	"veriqo/pkg/blockers/spiffe"
	"veriqo/pkg/blockers/supplychain"
)

// Outcome is one blocker's result from a single orchestrator run.
type Outcome struct {
	ID            string            `json:"id"`
	Status        string            `json:"status"`
	Pass          bool              `json:"pass"`
	FailureReason string            `json:"failure_reason,omitempty"`
	Measurements  map[string]string `json:"measurements,omitempty"`
	Error         string            `json:"error,omitempty"`
}

// Report is the combined result across all eight blockers.
type Report struct {
	Outcomes []Outcome `json:"outcomes"`
	AllPass  bool      `json:"all_pass"`
}

// Overrides lets a caller substitute a broken provider, connector set,
// or dependency set for exactly one blocker, so a test can inject a
// single failure and confirm the orchestrator isolates it rather than
// letting it cascade into -- or get silently absorbed by -- the other
// seven.
type Overrides struct {
	ScaleProvider       scale.NodeProvider
	DRProvider          dr.RegionProvider
	LiveDataConnectors  []livedata.FeedConnector
	SupplyChainDeps     []supplychain.Dependency
	SupplyChainProvider supplychain.VulnerabilityProvider
	SupplyChainPolicy   *supplychain.Policy
}

// RunAll runs every blocker present in registry, in the fixed order
// scale -> DR -> HSM/KMS -> live data -> soak -> SPIRE -> supply chain
// -> pentest, and returns one combined report. root is the repository
// root, used by supply-chain dependency discovery and the pentest
// preflight's git/source-hash check.
func RunAll(ctx context.Context, root string, registry *blockers.Registry, ov Overrides) Report {
	var outcomes []Outcome
	allPass := true

	record := func(id string, result blockers.RunResult, err error) {
		o := Outcome{ID: id, Pass: result.Pass, FailureReason: result.FailureReason, Measurements: result.Measurements}
		if err != nil {
			o.Error = err.Error()
		}
		if !result.Pass {
			allPass = false
		}
		outcomes = append(outcomes, o)
	}

	prep := func(id string) *blockers.Contract {
		c := registry.Get(id)
		if c == nil {
			return nil
		}
		c.MarkImplemented()
		c.MarkIntegrationTested()
		return c
	}

	if c := prep("scale_qualification"); c != nil {
		provider := ov.ScaleProvider
		if provider == nil {
			provider = scale.NewSimulatedNodeProvider()
		}
		result, err := scale.RunQualification(ctx, c, provider, 8, 2000)
		record("scale_qualification", result, err)
	}

	if c := prep("multi_region_dr"); c != nil {
		provider := ov.DRProvider
		if provider == nil {
			provider = dr.NewLocalRegionProvider()
		}
		result, err := dr.RunQualification(ctx, c, provider, 3)
		record("multi_region_dr", result, err)
	}

	if c := prep("hsm_kms"); c != nil {
		result, err := hsmkms.RunFailureMatrix(c)
		record("hsm_kms", result, err)
	}

	if c := prep("live_data"); c != nil {
		connectors := ov.LiveDataConnectors
		if connectors == nil {
			connectors = []livedata.FeedConnector{
				livedata.NewSyntheticConnector("SWIFT", 20), livedata.NewSyntheticConnector("AIS", 20),
				livedata.NewSyntheticConnector("SAR", 20), livedata.NewSyntheticConnector("BoL", 20),
			}
		}
		result, err := livedata.RunQualification(ctx, c, connectors)
		record("live_data", result, err)
	}

	if c := prep("soak_72h"); c != nil {
		result, _, err := soak.RunQualification(c, 0.05, func(i int) error { return nil })
		record("soak_72h", result, err)
	}

	if c := prep("spire_mtls"); c != nil {
		result, err := spiffe.RunQualification(c)
		record("spire_mtls", result, err)
	}

	if c := prep("supply_chain_scan"); c != nil {
		deps := ov.SupplyChainDeps
		var discoverErr error
		if deps == nil {
			deps, discoverErr = supplychain.DiscoverDependencies(ctx, root)
		}
		if discoverErr != nil {
			record("supply_chain_scan", blockers.RunResult{}, fmt.Errorf("discover dependencies: %w", discoverErr))
		} else {
			provider := ov.SupplyChainProvider
			if provider == nil {
				provider = supplychain.NewOfflineFixtureProvider(nil)
			}
			policy := supplychain.Policy{FailOn: []supplychain.Severity{supplychain.SeverityCritical, supplychain.SeverityHigh}}
			if ov.SupplyChainPolicy != nil {
				policy = *ov.SupplyChainPolicy
			}
			result, err := supplychain.RunQualification(ctx, c, deps, provider, policy)
			record("supply_chain_scan", result, err)
		}
	}

	if c := prep("pentest"); c != nil {
		result, err := pentest.RunQualification(ctx, c, root)
		record("pentest", result, err)
	}

	for _, id := range registry.IDs() {
		c := registry.Get(id)
		if c != nil && c.Status == blockers.StatusQualificationTested {
			_ = c.PromoteToReadyForRealQualification()
		}
	}
	for i := range outcomes {
		if c := registry.Get(outcomes[i].ID); c != nil {
			outcomes[i].Status = string(c.Status)
		}
	}

	return Report{Outcomes: outcomes, AllPass: allPass}
}
