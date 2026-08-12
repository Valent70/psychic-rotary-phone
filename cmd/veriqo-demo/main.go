// Command veriqo-demo is Veriqo's investor-facing CLI — the concrete
// answer to "cmd/veriqo-demo CLI (currently a Go test, not
// investor-facing CLI)" and to Masalah_terbesar_nomor_1.docx's item 8,
// "Real-world Demonstration". It is a real, runnable binary (not a
// `go test`) that an investor or evaluator can execute directly:
//
//	go run ./cmd/veriqo-demo -scenario dark-vessel -tick 100
//
// It wires together every layer built across this project in one
// end-to-end, config-driven, multi-agent-orchestrated run:
//
//	YAML policy config -> maritime domain ontology -> Collector Agent ->
//	Fusion Agent (Truth Arbitration) -> Reasoning Agent (Causal) ->
//	Decision Agent (risk policy) -> Digital Twin Agent -> Reporting Agent
//
// each running as a workflow.Step under workflow.Executor, checkpointed
// to an audit.AuditStore after every step, so the run is resumable and
// auditable — not a linear script.
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"veriqo/pkg/engine"
	"veriqo/pkg/moat/causal"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/domain/maritime"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/kg"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/config"
	"veriqo/pkg/trust"
	"veriqo/pkg/verification"
	"veriqo/pkg/workflow"
)

//go:embed policy.yaml
var defaultPolicyYAML string

// reasoningOutput is the reasoning agent's typed output, shared by the
// decision and twin agents. Using a single named type (rather than two
// separately-declared anonymous structs with identical fields) matters
// here: Go type assertions match exact dynamic types, and two anonymous
// struct literals declared in different functions are distinct types
// even with identical field lists.
type reasoningOutput struct {
	Fusion  map[string]fusion.ArbitrationResult
	Support float64
}

const actorID = "veriqo-demo-cli"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("veriqo-demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	scenario := fs.String("scenario", "dark-vessel", "demo scenario to run (currently: dark-vessel)")
	tick := fs.Uint64("tick", 100, "logical tick for this run (no wall-clock time is used anywhere in the kernel)")
	configPath := fs.String("config", "", "path to a policy YAML file (defaults to the embedded policy.yaml)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *scenario != "dark-vessel" {
		fmt.Fprintf(stderr, "veriqo-demo: unknown scenario %q (only \"dark-vessel\" is implemented)\n", *scenario)
		return 2
	}

	yamlSrc := defaultPolicyYAML
	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			fmt.Fprintf(stderr, "veriqo-demo: reading config: %v\n", err)
			return 1
		}
		yamlSrc = string(raw)
	}

	sources, policies, err := config.LoadDocument(yamlSrc)
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-demo: parsing config: %v\n", err)
		return 1
	}
	if len(policies) == 0 {
		fmt.Fprintln(stderr, "veriqo-demo: config defines no policies")
		return 1
	}

	report, err := runDarkVesselDemo(sources, policies[0], *tick)
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-demo: run failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, report)
	return 0
}

// demoState carries the shared, stateful engines each workflow step
// operates on. Agents close over this struct rather than passing large
// engines through workflow.AgentInput.Outputs — Outputs carries the
// per-step *results* used for the final report and inter-step data
// flow, while demoState is the shared "service layer" every real
// multi-agent runtime needs (the fusion/causal/decision/twin engines
// are themselves stateful, hash-chained logs, not pure values).
type demoState struct {
	sourceProfiles []fusion.SourceProfile
	policy         decision.Policy
	graph          *kg.Graph
	fusionEngine   *fusion.Engine
	causalGraph    *causal.Graph
	decisionEngine *decision.Engine
	twinRegistry   *digitaltwin.Registry
	ontology       *maritime.Registry
	trustKernel    *trust.Kernel

	vessel     maritime.Entity
	sanction   maritime.Entity
	twinID     digitaltwin.EntityID
	bundlePath string
}

func runDarkVesselDemo(sources []fusion.SourceProfile, policy decision.Policy, tick uint64) (string, error) {
	st := &demoState{sourceProfiles: sources, policy: policy}
	store := audit.NewAuditStore()
	ex := workflow.NewExecutor(store)
	sched := workflow.NewScheduler()

	var trace strings.Builder

	plan := workflow.Plan{
		Name: "dark_vessel_investigation",
		Steps: []workflow.Step{
			{Name: "ontology", Agent: st.ontologyAgent(&trace)},
			{Name: "collector", DependsOn: []string{"ontology"}, Agent: st.collectorAgent(&trace)},
			{Name: "fusion", DependsOn: []string{"collector"}, Agent: st.fusionAgent(&trace)},
			{Name: "reasoning", DependsOn: []string{"fusion"}, Agent: st.reasoningAgent(&trace)},
			{Name: "decision", DependsOn: []string{"reasoning"}, Agent: st.decisionAgent(&trace)},
			{Name: "verify", DependsOn: []string{"reasoning", "decision"}, Agent: st.verifyAgent(&trace)},
			{Name: "twin", DependsOn: []string{"decision", "reasoning"}, Agent: st.twinAgent(&trace)},
			{Name: "reporting", DependsOn: []string{"twin", "decision", "verify"}, Agent: st.reportingAgent(&trace)},
		},
	}

	record, err := sched.Schedule(plan, tick)
	if err != nil {
		return "", fmt.Errorf("scheduling: %w", err)
	}
	record, err = ex.Run(plan, record)
	if err != nil {
		return "", fmt.Errorf("executing (run_id=%s, failed at step index %d): %w", record.RunID, record.NextIndex, err)
	}

	final, ok := record.Completed["reporting"].Output.(string)
	if !ok {
		return "", errors.New("reporting step produced no output")
	}
	var out strings.Builder
	out.WriteString(trace.String())
	out.WriteString(final)
	fmt.Fprintf(&out, "\n[workflow] run_id=%s  plan=%s  steps_completed=%d/%d  audit_checkpoints=%d\n",
		record.RunID, record.PlanName, len(record.Completed), len(record.Order), len(store.Snapshot()))
	return out.String(), nil
}

func (st *demoState) ontologyAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		st.ontology = maritime.NewRegistry()
		vessel, err := st.ontology.RegisterEntity(maritime.KindVessel, map[string]string{"imo": "9876543"}, map[string]string{"name": "MV OPAQUE STAR"}, in.Tick)
		if err != nil {
			return nil, err
		}
		broker, err := st.ontology.RegisterEntity(maritime.KindBroker, map[string]string{"name": "Nautilus Brokerage FZE"}, nil, in.Tick)
		if err != nil {
			return nil, err
		}
		insurer, err := st.ontology.RegisterEntity(maritime.KindInsurer, map[string]string{"name": "Horizon P&I Mutual"}, nil, in.Tick)
		if err != nil {
			return nil, err
		}
		country, err := st.ontology.RegisterEntity(maritime.KindCountry, map[string]string{"iso": "IR"}, nil, in.Tick)
		if err != nil {
			return nil, err
		}
		sanction, err := st.ontology.RegisterEntity(maritime.KindSanctionEntry, map[string]string{"list": "OFAC-SDN", "ref": "IMO9876543"}, nil, in.Tick)
		if err != nil {
			return nil, err
		}
		if _, err := st.ontology.RegisterRelation(vessel.ID, maritime.RelBrokeredBy, broker.ID, in.Tick+1); err != nil {
			return nil, err
		}
		if _, err := st.ontology.RegisterRelation(broker.ID, maritime.RelInsuredBy, insurer.ID, in.Tick+1); err != nil {
			return nil, err
		}
		if _, err := st.ontology.RegisterRelation(insurer.ID, maritime.RelDomiciledIn, country.ID, in.Tick+1); err != nil {
			return nil, err
		}
		if _, err := st.ontology.RegisterRelation(country.ID, maritime.RelSanctioned, sanction.ID, in.Tick+1); err != nil {
			return nil, err
		}
		st.vessel = vessel
		st.sanction = sanction
		st.twinID = digitaltwin.EntityID(vessel.ID)

		path := st.ontology.MultiHop(vessel.ID, 4)[sanction.ID]
		fmt.Fprintf(trace, "[ontology] registered %d maritime entities; multi-hop link vessel->sanction: %v\n", st.ontology.EntityCount(), path)
		return vessel.ID, nil
	}
}

func (st *demoState) collectorAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		st.graph = kg.NewGraph()
		st.fusionEngine = fusion.NewEngine(st.graph)
		for _, sp := range st.sourceProfiles {
			if err := st.fusionEngine.RegisterSource(sp); err != nil {
				return nil, fmt.Errorf("registering source %s: %w", sp.ID, err)
			}
		}
		subject := "VESSEL:" + st.vessel.ID
		claims := []struct {
			predicate string
			src       fusion.SourceID
			value     string
		}{
			{"AIS_STATUS", "AIS_VENDOR_ALPHA", "TRANSPONDER_OFF"},
			{"AIS_STATUS", "AIS_VENDOR_BETA", "TRANSPONDER_OFF"},
			{"AIS_STATUS", "SAR_IMAGERY_VENDOR", "TRANSPONDER_OFF"},
			{"STS_TRANSFER", "SAR_IMAGERY_VENDOR", "OBSERVED_ADJACENT_VESSEL"},
			{"CARGO_MANIFEST", "PORT_AUTHORITY_TPP", "MANIFEST_MISMATCH"},
			{"SANCTION_LINK", "SANCTIONS_LIST_OFAC", "MATCHED"},
		}
		n := 0
		for _, c := range claims {
			claim := fusion.Claim{Subject: subject, Predicate: c.predicate}
			if _, err := st.fusionEngine.Submit(c.src, claim, c.value, in.Tick); err != nil {
				return nil, fmt.Errorf("submitting evidence %s/%s: %w", c.predicate, c.src, err)
			}
			n++
		}
		fmt.Fprintf(trace, "[collector] submitted %d evidence items across %d sources for %s\n", n, len(st.sourceProfiles), subject)
		return subject, nil
	}
}

func (st *demoState) fusionAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		subject := in.Outputs["collector"].(string)
		results := make(map[string]fusion.ArbitrationResult)
		for _, predicate := range []string{"AIS_STATUS", "STS_TRANSFER", "CARGO_MANIFEST", "SANCTION_LINK"} {
			claim := fusion.Claim{Subject: subject, Predicate: predicate}
			res, err := st.fusionEngine.Arbitrate(actorID, claim, in.Tick+1)
			if err != nil {
				return nil, fmt.Errorf("arbitrating %s: %w", predicate, err)
			}
			results[predicate] = res
			fmt.Fprintf(trace, "[fusion] %-16s -> %-24s confidence=%.3f (evidence=%d, contradiction=%v)\n",
				predicate, res.Winner, res.WinnerConfidence, res.EvidenceCount, res.Contradiction)
		}
		return results, nil
	}
}

func (st *demoState) reasoningAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		results := in.Outputs["fusion"].(map[string]fusion.ArbitrationResult)
		st.causalGraph = causal.NewGraph()
		aisOff := causal.NodeID("AIS_OFF")
		sts := causal.NodeID("STS_TRANSFER")
		cargo := causal.NodeID("CARGO_CHANGE")
		ownership := causal.NodeID("OWNERSHIP_CHANGE")

		if _, err := st.causalGraph.Observe(actorID, aisOff, sts, results["AIS_STATUS"].WinnerConfidence, 1, "", in.Tick+2); err != nil {
			return nil, err
		}
		if _, err := st.causalGraph.Observe(actorID, sts, cargo, results["STS_TRANSFER"].WinnerConfidence, 1, "", in.Tick+2); err != nil {
			return nil, err
		}
		if _, err := st.causalGraph.Observe(actorID, cargo, ownership, results["CARGO_MANIFEST"].WinnerConfidence, 1, "", in.Tick+2); err != nil {
			return nil, err
		}

		paths, err := st.causalGraph.Why(ownership)
		if err != nil {
			return nil, fmt.Errorf("why(ownership_change): %w", err)
		}
		before, after := st.causalGraph.WhatIf(ownership, aisOff)
		fmt.Fprintf(trace, "[reasoning] root-cause paths to OWNERSHIP_CHANGE: %d found; counterfactual (remove AIS_OFF): support %.3f -> %.3f\n",
			len(paths), before, after)
		return reasoningOutput{Fusion: results, Support: st.causalGraph.AggregateSupport(ownership)}, nil
	}
}

func (st *demoState) decisionAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		ro := in.Outputs["reasoning"].(reasoningOutput)
		st.decisionEngine = decision.NewEngine()
		if err := st.decisionEngine.RegisterPolicy(st.policy); err != nil {
			return nil, fmt.Errorf("registering policy: %w", err)
		}
		values := map[string]float64{
			"ais_gap_confidence":       ro.Fusion["AIS_STATUS"].WinnerConfidence,
			"sts_transfer_confidence":  ro.Fusion["STS_TRANSFER"].WinnerConfidence,
			"cargo_change_confidence":  ro.Fusion["CARGO_MANIFEST"].WinnerConfidence,
			"sanction_link_confidence": ro.Fusion["SANCTION_LINK"].WinnerConfidence,
		}
		subject := "VESSEL:" + st.vessel.ID
		d, err := st.decisionEngine.Decide(actorID, subject, st.policy.Name, values, in.Tick+3)
		if err != nil {
			return nil, fmt.Errorf("deciding: %w", err)
		}
		fmt.Fprintf(trace, "[decision] policy=%s risk_score=%.4f action=%s\n", st.policy.Name, d.RiskScore, d.Action)
		return d, nil
	}
}

func (st *demoState) verifyAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		subject := "VESSEL:" + st.vessel.ID
		claim := fusion.Claim{Subject: subject, Predicate: "SANCTION_LINK"}
		ro := in.Outputs["reasoning"].(reasoningOutput)
		result := ro.Fusion["SANCTION_LINK"]

		bundle, err := engine.BuildFusionBundle(st.fusionEngine, claim, actorID, in.Tick+1, result)
		if err != nil {
			return nil, fmt.Errorf("building IVF bundle: %w", err)
		}

		v := verification.NewVerifier()
		v.RegisterReplayFunc("fusion", engine.ReplayFusion)
		vr, err := v.Verify("fusion", bundle)
		if err != nil {
			return nil, fmt.Errorf("independent verification: %w", err)
		}

		// Trust Kernel: certify each source that testified on
		// SANCTION_LINK from REAL agreement-with-winner evidence drawn
		// from this same fusion arbitration — not synthetic scores.
		if st.trustKernel == nil {
			st.trustKernel = trust.NewKernel()
			policy := trust.TrustPolicy{
				Name: "source-reliability", CertifyThreshold: 0.6,
				Rules: []trust.TrustRule{
					trust.RuleFromEvidenceKey("agreement_with_winner", 0.7, "agreement_with_winner"),
					trust.RuleFromEvidenceKey("base_reliability", 0.3, "base_reliability"),
				},
			}
			if err := st.trustKernel.RegisterPolicy(policy); err != nil {
				return nil, fmt.Errorf("registering trust policy: %w", err)
			}
		}
		evidenceItems := st.fusionEngine.EvidenceFor(claim)
		var certified int
		for _, ev := range evidenceItems {
			agree := 0.0
			if ev.Value == result.Winner {
				agree = 1.0
			}
			te := trust.TrustEvidence{"agreement_with_winner": agree, "base_reliability": ev.Reliability}
			_, cert, err := st.trustKernel.Certify(string(ev.Source), "source-reliability", te, in.Tick+1)
			if err != nil {
				return nil, fmt.Errorf("certifying source %s: %w", ev.Source, err)
			}
			if cert != nil {
				certified++
			}
		}

		// The bundle's filename is derived from its own content hash
		// (RootHash), not a random temp name — os.CreateTemp's random
		// suffix would make this CLI's report non-reproducible at a
		// fixed tick, breaking the same end-to-end determinism
		// TestRunDarkVesselDemoDeterministic checks for every other
		// stage of this pipeline.
		bundleJSON, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling bundle: %w", err)
		}
		st.bundlePath = filepath.Join(os.TempDir(), fmt.Sprintf("veriqo-bundle-%s.json", bundle.Manifest.RootHash[:16]))
		if err := os.WriteFile(st.bundlePath, bundleJSON, 0o644); err != nil {
			return nil, fmt.Errorf("writing bundle file: %w", err)
		}

		fmt.Fprintf(trace, "[verify] IVF bundle: subject=%s records=%d root=%s...\n",
			bundle.ReplayPackage.Evidence.Subject, bundle.Manifest.RecordCount, bundle.Manifest.RootHash[:16])
		fmt.Fprintf(trace, "[verify] independent replay on a BRAND-NEW fusion engine (zero shared state with the run above):\n")
		fmt.Fprintf(trace, "[verify]   claimed    = %s\n", bundle.ReplayPackage.ClaimedResult)
		fmt.Fprintf(trace, "[verify]   recomputed = %s\n", vr.RecomputedResult)
		fmt.Fprintf(trace, "[verify] manifest_valid=%v replay_valid=%v -> ", vr.ManifestValid, vr.ReplayValid)
		if vr.Certificate != nil {
			fmt.Fprintf(trace, "VERIFIED (certificate %s...)\n", vr.Certificate.CertificateHash[:16])
		} else {
			fmt.Fprintf(trace, "NOT VERIFIED\n")
		}
		fmt.Fprintf(trace, "[verify] trust kernel: %d/%d sources certified >= 0.60 reliability threshold\n", certified, len(evidenceItems))
		fmt.Fprintf(trace, "[verify] bundle written to disk: %s\n", st.bundlePath)
		fmt.Fprintf(trace, "[verify] an outside party can re-check it in a SEPARATE process, trusting nothing from this run, via:\n")
		fmt.Fprintf(trace, "[verify]   go run ./cmd/veriqo-verify -bundle %s -domain fusion\n", st.bundlePath)

		return vr, nil
	}
}

func (st *demoState) twinAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		d := in.Outputs["decision"].(decision.Decision)
		st.twinRegistry = digitaltwin.NewRegistry()

		ro := in.Outputs["reasoning"].(reasoningOutput)
		for predicate, res := range ro.Fusion {
			_ = predicate
			if err := st.twinRegistry.ApplyArbitration(actorID, st.twinID, res, in.Tick+4); err != nil {
				return nil, fmt.Errorf("applying arbitration to twin: %w", err)
			}
		}
		if err := st.twinRegistry.ApplyDecision(actorID, st.twinID, d, in.Tick+4); err != nil {
			return nil, fmt.Errorf("applying decision to twin: %w", err)
		}
		twin, ok := st.twinRegistry.Get(st.twinID)
		if !ok {
			return nil, errors.New("twin not found after apply")
		}
		risk := twin.Risk[st.policy.Name]
		fmt.Fprintf(trace, "[twin] entity=%s attributes_tracked=%d risk_action=%s\n", st.twinID, len(twin.Attributes), risk.Action)
		return twin, nil
	}
}

func (st *demoState) reportingAgent(trace *strings.Builder) workflow.AgentFunc {
	return func(in workflow.AgentInput) (any, error) {
		twin := in.Outputs["twin"].(digitaltwin.Twin)
		d := in.Outputs["decision"].(decision.Decision)
		vr := in.Outputs["verify"].(verification.VerificationResult)
		risk := twin.Risk[st.policy.Name]

		var b strings.Builder
		b.WriteString("\n================ VERIQO INVESTOR DEMO — DARK VESSEL INVESTIGATION ================\n")
		fmt.Fprintf(&b, "Vessel:            IMO 9876543 (MV OPAQUE STAR)\n")
		fmt.Fprintf(&b, "Ontology entity:   %s\n", st.vessel.ID)
		fmt.Fprintf(&b, "Sanction linkage:  %s\n", st.sanction.ID)
		fmt.Fprintf(&b, "Policy applied:    %s (config-driven, loaded from YAML)\n", st.policy.Name)
		fmt.Fprintf(&b, "Final risk score:  %.4f\n", risk.RiskScore)
		fmt.Fprintf(&b, "Final action:      %s\n", risk.Action)
		b.WriteString("Explanation trail:\n")
		for _, line := range d.Explanation {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(&b, "  - %s\n", line)
			}
		}
		b.WriteString("Independent verification (Independent Verification Framework):\n")
		fmt.Fprintf(&b, "  - manifest_valid=%v replay_valid=%v certificate_issued=%v\n", vr.ManifestValid, vr.ReplayValid, vr.Certificate != nil)
		fmt.Fprintf(&b, "  - external validator command: go run ./cmd/veriqo-verify -bundle %s -domain fusion\n", st.bundlePath)
		b.WriteString("=====================================================================================\n")
		return b.String(), nil
	}
}
