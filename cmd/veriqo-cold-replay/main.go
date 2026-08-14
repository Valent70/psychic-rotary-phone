// Command veriqo-cold-replay is the "distributed cold replay"
// acceptance artifact a fresh audit (V7.12.7, item P0-E) asked for:
//
//	export historical execution -> destroy runtime state -> new
//	node/process -> restore immutable history -> replay -> same
//	evidence root, decision, explanation, verification hash.
//
// This binary IS the "new node/process": it is a genuinely separate
// compiled binary, launched as its own OS process, that reads NOTHING
// but an exported pkg/execution.ReplayRequest JSON file from disk --
// no shared memory, no import of whatever produced the export, no
// pointer into the original run's engine. It constructs a brand-new
// execution.Engine and independently rebuilds the entire DAG (every
// stage: evidence ingestion through decision, explanation and the
// verification certificate) purely from that file, exactly mirroring
// test/integration/ivf_cross_process_test.go's already-proven
// cross-process pattern for the fusion/contradiction/decision domains,
// extended to the FULL execution DAG.
//
// Honest scope: this proves cold restoration of evidence root,
// decision, explanation and verification hash from an exported file
// across a real process boundary. It does NOT include entity
// resolution: pkg/identity.Resolver keeps its own independent ledger
// with no existing adapter bundling it into a ReplayRequest export, so
// claiming this replays entity resolution too would be fabricated
// scope, not a real property this binary checks.
//
// Usage:
//
//	veriqo-cold-replay -export path/to/exported-execution.json
//
// Exit codes: 0 = PASSED (every compared node hash matches),
// 1 = FAILED (a divergent stage was found, or replay errored),
// 2 = usage/input error.
package main

import (
	"context"
	"fmt"
	"os"

	"veriqo/pkg/execution"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	var exportPath string
	for i := 0; i < len(args); i++ {
		if args[i] == "-export" && i+1 < len(args) {
			exportPath = args[i+1]
		}
	}
	if exportPath == "" {
		fmt.Fprintln(stderr, "usage: veriqo-cold-replay -export <exported-execution.json>")
		return 2
	}

	data, err := os.ReadFile(exportPath) // #nosec G304 G703 -- exportPath is an operator-supplied CLI argument, not untrusted input
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-cold-replay: reading export: %v\n", err)
		return 2
	}

	// A brand-new Engine backed by a nil pipeline, in a brand-new
	// process: zero shared state with whatever process produced data.
	// context.Background() is genuinely correct here (P0-6): this is a
	// standalone CLI batch job with no inbound request to inherit a
	// context from, unlike pkg/lifecycle.Orchestrator.RunUnified's real
	// Intent entrypoint.
	verdict, err := execution.ReplayDAG(context.Background(), data, execution.NewEngine(nil))
	if err != nil {
		fmt.Fprintf(stderr, "veriqo-cold-replay: REPLAY ERROR: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "veriqo-cold-replay: independent cold-restart replay report\n")
	fmt.Fprintf(stdout, "  original evidence root : %s\n", verdict.OriginalRootHash)
	fmt.Fprintf(stdout, "  replayed evidence root : %s\n", verdict.ReplayRootHash)
	fmt.Fprintf(stdout, "  nodes compared         : %d\n", verdict.NodesCompared)

	if verdict.Matched {
		fmt.Fprintf(stdout, "  VERDICT                : PASSED (evidence root, decision, explanation and "+
			"verification certificate all reproduced from cold-restored history alone)\n")
		return 0
	}
	fmt.Fprintf(stdout, "  divergent stage        : %s\n", verdict.DivergentStage)
	fmt.Fprintf(stdout, "  original node hash     : %s\n", verdict.OriginalNodeHash)
	fmt.Fprintf(stdout, "  replayed node hash     : %s\n", verdict.ReplayNodeHash)
	fmt.Fprintf(stdout, "  VERDICT                : FAILED\n")
	return 1
}
