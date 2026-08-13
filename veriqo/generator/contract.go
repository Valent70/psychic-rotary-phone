// Package generator implements the "Contract -> Generator -> API/SDK/CLI"
// pipeline from the API/SDK/CLI consolidation architecture doc: it
// reads the canonical contracts under veriqo/contracts/*.yaml and
// mechanically emits real, compilable Go source for the CLI and the Go
// SDK, so a change to a contract propagates to both without either
// being hand-edited to match.
//
// Honest scope for this session (stated plainly, per this project's
// consistent practice): the generator emits Go only. The architecture
// doc's long-term vision (Python/TypeScript/Java SDKs, gRPC, OpenAPI
// docs) is NOT built this session — those are additional template
// sets over the exact same Contract model defined here, not a new
// design problem, and are left as an explicitly named next step (see
// the session report). What IS real: the contract files are parsed
// with this repo's own zero-dependency YAML-subset parser
// (pkg/platform/config), the emitted .go files are committed and
// compiled as part of this same repository, and cli/veriqo actually
// runs the real engines end-to-end (see generator's own test and
// examples/quickstart.sh).
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veriqo/pkg/platform/config"
)

// Param is one method parameter's contract-declared name and type.
// Supported types are deliberately minimal — string, float, uint —
// matching what a CLI flag or an HTTP query parameter can carry
// without ambiguity; a method needing a richer type (e.g. Intent's
// Action enum) is represented as `string` at the contract boundary and
// converted inside the registry adapter, keeping the transport layer
// (this generator's whole job) free of domain-specific types.
type Param struct {
	Name string
	Type string // "string" | "float" | "uint"
}

// Method is one callable operation on an engine.
type Method struct {
	Name        string
	Description string
	Params      []Param
	Returns     string
}

// Contract is one engine's full canonical, generator-readable surface.
type Contract struct {
	Engine      string
	Description string
	Methods     []Method
}

// GoType returns the Go type a contract Param.Type maps to.
func (p Param) GoType() string {
	switch p.Type {
	case "string":
		return "string"
	case "float":
		return "float64"
	case "uint":
		return "uint64"
	default:
		return "string"
	}
}

// LoadContracts parses every *.yaml file in dir into a Contract,
// sorted by engine name for deterministic (replay-stable) generator
// output — regenerating from the same contracts always produces
// byte-identical Go source, matching this repo's DARI discipline.
func LoadContracts(dir string) ([]Contract, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("generator: reading contracts dir %s: %w", dir, err)
	}
	var contracts []Contract
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- e.Name() comes from this process's own os.ReadDir listing, not external input
		if err != nil {
			return nil, fmt.Errorf("generator: reading %s: %w", e.Name(), err)
		}
		c, err := parseContract(string(b))
		if err != nil {
			return nil, fmt.Errorf("generator: parsing %s: %w", e.Name(), err)
		}
		contracts = append(contracts, c)
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Engine < contracts[j].Engine })
	return contracts, nil
}

func parseContract(src string) (Contract, error) {
	root, err := config.Parse(src)
	if err != nil {
		return Contract{}, err
	}
	c := Contract{
		Engine:      root.Get("engine").String(),
		Description: root.Get("description").String(),
	}
	if c.Engine == "" {
		return Contract{}, fmt.Errorf("contract missing required 'engine' field")
	}
	methodsNode := root.Get("methods")
	for _, m := range methodsNode.Items() {
		method := Method{
			Name:        m.Get("name").String(),
			Description: m.Get("description").String(),
			Returns:     m.Get("returns").String(),
		}
		if method.Name == "" {
			return Contract{}, fmt.Errorf("engine %q has a method with no name", c.Engine)
		}
		if paramsNode := m.Get("params"); paramsNode != nil {
			for _, p := range paramsNode.Items() {
				method.Params = append(method.Params, Param{
					Name: p.Get("name").String(),
					Type: p.Get("type").String(),
				})
			}
		}
		c.Methods = append(c.Methods, method)
	}
	return c, nil
}

// GoMethodName converts a contract's snake_case method name to the
// CamelCase Go method name the registry adapter exposes (e.g.
// "verify_ledger" -> "VerifyLedger"). This is the ONE naming
// convention every generated artifact (CLI dispatch, SDK wrapper) and
// every registry.*API method must agree on — a mismatch here is a
// build-time error in the generated code, not a silent runtime bug,
// because the emitted Go source calls the method by name directly.
func GoMethodName(snake string) string {
	parts := strings.Split(snake, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

// GoEngineName converts a contract's engine name to the exported
// registry field name (e.g. "trust" -> "Trust").
func GoEngineName(engine string) string {
	if engine == "" {
		return ""
	}
	return strings.ToUpper(engine[:1]) + engine[1:]
}
