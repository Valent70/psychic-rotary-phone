// Command gen runs the Contract -> Generator step: reads
// veriqo/contracts/*.yaml and mechanically writes every derived
// artifact — the CLI dispatch table, the Go SDK, the REST gateway
// route table, and the Python/TypeScript SDKs — from that one source.
// Run it with:
//
//	go run ./veriqo/generator/cmd/gen
//
// Regenerating is idempotent and deterministic: the same contracts
// always produce byte-identical output (contracts are loaded in
// sorted-engine-name order), matching this repo's replay-determinism
// discipline applied to its own build tooling.
package main

import (
	"fmt"
	"os"

	"veriqo/veriqo/generator"
)

type target struct {
	name string
	gen  func([]generator.Contract) ([]byte, error)
	path string
}

func main() {
	contracts, err := generator.LoadContracts("veriqo/contracts")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}

	targets := []target{
		{"CLI", generator.GenerateCLI, "veriqo/cli/generated_commands.go"},
		{"Go SDK", generator.GenerateSDK, "veriqo/sdk/go/generated_client.go"},
		{"REST gateway", generator.GenerateREST, "veriqo/gateway/rest/generated_routes.go"},
		{"Python SDK", generator.GeneratePython, "veriqo/sdk/python/generated_client.py"},
		{"TypeScript SDK", generator.GenerateTypeScript, "veriqo/sdk/ts/generated_client.ts"},
	}

	for _, t := range targets {
		src, err := t.gen(contracts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen: %s: %v\n", t.name, err)
			os.Exit(1)
		}
		if err := os.WriteFile(t.path, src, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "gen: writing %s: %v\n", t.name, err)
			os.Exit(1)
		}
	}

	fmt.Printf("gen: generated %d artifact(s) from %d contract(s)\n", len(targets), len(contracts))
}
