// Command veriqo-kernel-demo is a real, runnable end-to-end
// demonstration of the consolidated Core Runtime / Execution / Evidence
// / Knowledge / Intelligence stack built this session, wired via
// veriqo/kernel.Kernel. Run with: go run ./cmd/veriqo-kernel-demo
package main

import (
	"encoding/json"
	"fmt"

	"veriqo/pkg/moat/contradiction"
	"veriqo/veriqo/core/execution"
	"veriqo/veriqo/kernel"
	"veriqo/veriqo/moat/scorecard"
)

func main() {
	k, err := kernel.New()
	if err != nil {
		panic(err)
	}
	defer k.Shutdown()

	fmt.Println("== Core Runtime health ==")
	for name, status := range k.HealthReport() {
		fmt.Printf("  %-14s %s\n", name, status)
	}

	fmt.Println("\n== Execution: evidence.add_node -> reasoning.assert (real engines) ==")
	p := execution.Pipeline{Name: "evidence-reasoning-demo", Steps: []execution.Step{
		{Name: "add_node", Capability: "evidence.add_node", Method: "add_node", Args: func(ctx *execution.ExecutionContext) map[string]string {
			return map[string]string{"kind": "raw_observation", "payload": "vessel IMO9999999 observed at sea"}
		}},
		{Name: "assert", Capability: "reasoning.assert", Method: "assert", DependsOn: []string{"add_node"}, Args: func(ctx *execution.ExecutionContext) map[string]string {
			return map[string]string{"subject": "vessel/IMO9999999", "predicate": "status", "object": "at-sea"}
		}},
	}}
	trace, err := k.Execution.Execute(p, nil)
	if err != nil {
		panic(err)
	}
	for _, a := range trace.Attempts {
		fmt.Printf("  step=%-10s status=%-11s result=%v\n", a.StepName, a.Status, a.Result)
	}

	fmt.Println("\n== Intelligence Loop: Bayesian learning + causal explanation + counterfactuals ==")
	// Matches every other call in this demo (kernel.New, Execution.Execute,
	// Intelligence.Resolve below): panic on error rather than silently
	// continue with a demo trace that no longer reflects what it claims
	// to. A dropped Observe error here would let the later Resolve call
	// run against silently-incomplete evidence without any signal.
	observations := []contradiction.RawObservation{
		{ClaimKey: "vessel/IMO9999999|position", SourceID: "radar-7", Value: "at-sea", SourceReliability: 0.92, Tick: 1},
		{ClaimKey: "vessel/IMO9999999|position", SourceID: "satellite-3", Value: "at-sea", SourceReliability: 0.88, Tick: 1},
		{ClaimKey: "vessel/IMO9999999|position", SourceID: "anonymous-tip", Value: "in-port", SourceReliability: 0.25, Tick: 1},
	}
	for _, o := range observations {
		if err := k.Intelligence.Observe(o); err != nil {
			panic(err)
		}
	}
	exp, err := k.Intelligence.Resolve("vessel/IMO9999999|position", 1, 0)
	if err != nil {
		panic(err)
	}
	b, _ := json.MarshalIndent(exp, "  ", "  ")
	fmt.Println("  " + string(b))

	fmt.Println("\n== Shared Trust Calculus (Evidence + Intelligence read the SAME posterior) ==")
	fmt.Printf("  radar-7 reputation (via Intelligence): %.4f\n", k.Intelligence.Reputation("radar-7"))
	fmt.Printf("  radar-7 score (via shared Calculus):   %.4f\n", k.TrustCalculus.Score("radar-7"))

	fmt.Println("\n== MOAT Scorecard: continuous tracker ==")
	tracker := scorecard.NewTracker(0)
	tracker.Record(1, scorecard.Inputs{EnginesRegistered: 3})
	tracker.Record(2, scorecard.Inputs{EnginesRegistered: 5, TrustLedgerVerified: true})
	tracker.Record(3, scorecard.Inputs{EnginesRegistered: 7, TrustLedgerVerified: true, KnowledgeChainVerified: true,
		ArbitratedClaimsCount: 1, ContradictionsResolved: 1, ReplayVerificationsTotal: 2, ReplayVerificationsPass: 2, EvidenceGraphNodeCount: 1})
	fmt.Println("  " + tracker.String())
}
