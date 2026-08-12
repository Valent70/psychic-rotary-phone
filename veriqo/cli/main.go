// Command veriqo is the unified CLI surface: `veriqo [--state=path]
// <engine> <method> --flag=value ...`. The engine/method dispatch
// table itself is generated (see generated_commands.go) directly from
// veriqo/contracts/*.yaml — this file is the only hand-written part:
// the optional global --state flag, argument splitting, and the
// registry construction shared by every command.
package main

import (
	"flag"
	"fmt"
	"os"

	"veriqo/veriqo/registry"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("veriqo", flag.ContinueOnError)
	statePath := fs.String("state", "", "path to a persistence state file (optional; empty = purely in-memory, resets every invocation)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return usageError()
	}
	engine, method := rest[0], rest[1]
	key := engine + " " + method
	handler, ok := commandTable[key]
	if !ok {
		return fmt.Errorf("unknown command %q — run 'veriqo help' for the list", key)
	}

	var opts []registry.Option
	if *statePath != "" {
		opts = append(opts, registry.WithStatePath(*statePath))
	}
	reg, err := registry.New(opts...)
	if err != nil {
		return fmt.Errorf("building engine registry: %w", err)
	}
	if err := handler(reg, rest[2:]); err != nil {
		return err
	}
	if reg.HasPersistence() {
		if err := reg.SaveState(); err != nil {
			return fmt.Errorf("saving state to %s: %w", *statePath, err)
		}
	}
	return nil
}

func usageError() error {
	fmt.Fprintln(os.Stderr, "usage: veriqo [--state=path] <engine> <method> [--flag=value ...]")
	fmt.Fprintln(os.Stderr, "\navailable commands:")
	for k := range commandTable {
		fmt.Fprintln(os.Stderr, "  veriqo", k)
	}
	return fmt.Errorf("missing engine/method")
}
