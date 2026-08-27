package config

import (
	"fmt"

	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/fusion"
)

// SourceProfileSpec + PolicySpec are the YAML-facing shapes. LoadSourceProfiles
// and LoadPolicies convert a parsed *Node document into the exact types
// fusion.Engine.RegisterSource / decision.Engine.RegisterPolicy expect,
// so operators edit a YAML file instead of recompiling Go to change a
// reliability score or a risk threshold — the concrete fix for
// "hard-coded di Go" named in the current task.
//
// Expected document shape:
//
//   sources:
//     - id: AIS_PROVIDER_A
//       reliability: 0.9
//       provider: SATCOM_ALPHA
//     - id: PORT_AUTHORITY_TPP
//       reliability: 0.97
//
//   policies:
//     - name: dark_vessel_risk
//       flag_threshold: 0.4
//       escalate_threshold: 0.7
//       factors:
//         - name: ais_gap_confidence
//           weight: 0.4
//         - name: sts_transfer_confidence
//           weight: 0.35
//         - name: cargo_change_confidence
//           weight: 0.25

// LoadSourceProfiles reads the "sources:" block into fusion.SourceProfile
// values, ready to pass to Engine.RegisterSource.
func LoadSourceProfiles(root *Node) ([]fusion.SourceProfile, error) {
	block := root.Get("sources")
	if block == nil {
		return nil, nil
	}
	var out []fusion.SourceProfile
	for _, item := range block.Items() {
		idNode := item.Get("id")
		if idNode == nil || idNode.String() == "" {
			return nil, fmt.Errorf("config: source entry missing 'id'")
		}
		relNode := item.Get("reliability")
		if relNode == nil {
			return nil, fmt.Errorf("config: source %q missing 'reliability'", idNode.String())
		}
		rel, err := relNode.Float64()
		if err != nil {
			return nil, fmt.Errorf("config: source %q reliability: %w", idNode.String(), err)
		}
		out = append(out, fusion.SourceProfile{
			ID:              fusion.SourceID(idNode.String()),
			BaseReliability: rel,
			Provider:        item.Get("provider").String(),
		})
	}
	return out, nil
}

// LoadPolicies reads the "policies:" block into decision.Policy values,
// ready to pass to Engine.RegisterPolicy.
func LoadPolicies(root *Node) ([]decision.Policy, error) {
	block := root.Get("policies")
	if block == nil {
		return nil, nil
	}
	var out []decision.Policy
	for _, item := range block.Items() {
		nameNode := item.Get("name")
		if nameNode == nil || nameNode.String() == "" {
			return nil, fmt.Errorf("config: policy entry missing 'name'")
		}
		flagT, err := item.Get("flag_threshold").Float64()
		if err != nil {
			return nil, fmt.Errorf("config: policy %q flag_threshold: %w", nameNode.String(), err)
		}
		escT, err := item.Get("escalate_threshold").Float64()
		if err != nil {
			return nil, fmt.Errorf("config: policy %q escalate_threshold: %w", nameNode.String(), err)
		}
		factorsBlock := item.Get("factors")
		if factorsBlock == nil {
			return nil, fmt.Errorf("config: policy %q missing 'factors'", nameNode.String())
		}
		var factors []decision.FactorWeight
		for _, f := range factorsBlock.Items() {
			fname := f.Get("name")
			if fname == nil || fname.String() == "" {
				return nil, fmt.Errorf("config: policy %q has a factor missing 'name'", nameNode.String())
			}
			w, err := f.Get("weight").Float64()
			if err != nil {
				return nil, fmt.Errorf("config: policy %q factor %q weight: %w", nameNode.String(), fname.String(), err)
			}
			factors = append(factors, decision.FactorWeight{Name: fname.String(), Weight: w})
		}
		out = append(out, decision.Policy{
			Name:              nameNode.String(),
			Factors:           factors,
			FlagThreshold:     flagT,
			EscalateThreshold: escT,
		})
	}
	return out, nil
}

// LoadDocument parses raw YAML-subset text and returns both source
// profiles and policies in one call — the entry point cmd/veriqo-demo
// uses to load its investor-demo configuration file.
func LoadDocument(src string) ([]fusion.SourceProfile, []decision.Policy, error) {
	root, err := Parse(src)
	if err != nil {
		return nil, nil, err
	}
	sources, err := LoadSourceProfiles(root)
	if err != nil {
		return nil, nil, err
	}
	policies, err := LoadPolicies(root)
	if err != nil {
		return nil, nil, err
	}
	return sources, policies, nil
}
