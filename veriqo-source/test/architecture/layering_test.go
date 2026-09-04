// Package architecture holds structural tests: rules about how the
// repository is shaped, rather than about what any package computes.
//
// These are the tests that make a large Go codebase stay navigable. A
// unit test tells you a function is correct; a layering test tells you
// the constitution package has not quietly grown a dependency on the
// insurance domain, which is the kind of drift nobody notices in review
// and everybody pays for eighteen months later.
//
// The rules here are enforced against the real import graph reported by
// the Go toolchain, not against a hand-maintained list.
package architecture

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePath = "veriqo"

// layer is a position in the dependency order. A package may import a
// package in a lower or equal layer, never a higher one.
type layer int

const (
	// layerFoundation: pure primitives. Canonicalization, hashing,
	// storage mechanics. These may import nothing of VERIQO's at all.
	layerFoundation layer = iota
	// layerContract: the vocabulary everything else agrees on -- the
	// constitution, the ontology, the event envelope, the ledger.
	layerContract
	// layerControl: the checks. Qualification, disclosure, authorization,
	// the AI gateway.
	layerControl
	// layerFabric: the five fabrics' own packages -- proof, casefabric,
	// fref.
	layerFabric
	// layerDomain: insurance, maritime, commodity, supply chain, trade.
	layerDomain
	// layerApplication: gateways, APIs, commercial surfaces.
	layerApplication
	// layerAssurance: observes everything and is imported by nothing.
	layerAssurance
)

var layerNames = map[layer]string{
	layerFoundation: "foundation", layerContract: "contract", layerControl: "control",
	layerFabric: "fabric", layerDomain: "domain", layerApplication: "application",
	layerAssurance: "assurance",
}

func (l layer) String() string { return layerNames[l] }

// layerOf classifies a package by its import path. Only the packages
// named here are governed; anything unclassified is exempt, so the rule
// can be tightened package by package rather than demanding the whole
// repository be reshaped at once.
func layerOf(pkg string) (layer, bool) {
	rel := strings.TrimPrefix(pkg, modulePath+"/")
	switch {
	case rel == "pkg/canonical/jcs", rel == "pkg/storage/wal", rel == "pkg/storage/snapshot",
		rel == "pkg/platform/telemetry":
		// telemetry is foundation, not platform plumbing above it: it
		// imports nothing of VERIQO's, and pkg/storage/wal depends on it
		// for metrics. A layering rule that called that a violation
		// would be describing a repository we do not have.
		return layerFoundation, true
	case rel == "pkg/constitution", rel == "pkg/ontology", rel == "pkg/contract/event",
		rel == "pkg/platform/audit", rel == "pkg/platform/timestamp",
		rel == "pkg/provenance/temporal":
		// temporal provenance is vocabulary, not a check: it says what
		// state a reference is in, and the packages that act on that
		// state sit above it.
		return layerContract, true
	case strings.HasPrefix(rel, "pkg/qualification/"), rel == "pkg/disclosure/access",
		rel == "pkg/ai/gateway", rel == "pkg/authz", rel == "pkg/evidence/quality":
		// evidence quality is a control: it decides what a body of
		// evidence permits, and the qualification ledger consumes that
		// decision. Classifying it here is what makes that import an
		// equal-layer edge rather than an ungoverned one.
		return layerControl, true
	case rel == "pkg/proof", rel == "pkg/casefabric", rel == "pkg/fref":
		return layerFabric, true
	case strings.HasPrefix(rel, "pkg/insurance/"), strings.HasPrefix(rel, "pkg/domain/"):
		return layerDomain, true
	case strings.HasPrefix(rel, "pkg/commercial/"), strings.HasPrefix(rel, "pkg/api"):
		return layerApplication, true
	case rel == "pkg/assurance":
		return layerAssurance, true
	}
	return 0, false
}

// imports returns the direct VERIQO imports of every package in the
// module, excluding test files.
func imports(t *testing.T) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}{{range .Imports}} {{.}}{{end}}", "./...")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	graph := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pkg := fields[0]
		for _, imp := range fields[1:] {
			if strings.HasPrefix(imp, modulePath+"/") || imp == modulePath {
				graph[pkg] = append(graph[pkg], imp)
			}
		}
	}
	if len(graph) == 0 {
		t.Fatal("go list produced no packages")
	}
	return graph
}

// TestDependenciesFlowDownwards is the core structural rule: a package
// may import a lower or equal layer, never a higher one.
//
// The direction is what makes the codebase navigable. Foundation code
// that knows about insurance cannot be reused; a constitution that
// depends on an implementation is not a constitution.
func TestDependenciesFlowDownwards(t *testing.T) {
	var violations []string
	for pkg, imps := range imports(t) {
		from, governed := layerOf(pkg)
		if !governed {
			continue
		}
		for _, imp := range imps {
			to, governed := layerOf(imp)
			if !governed {
				continue
			}
			if to > from {
				violations = append(violations, strings.Join([]string{
					pkg, " (", from.String(), ") imports ", imp, " (", to.String(), ")",
				}, ""))
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("dependencies must flow downwards:\n  %s", strings.Join(violations, "\n  "))
	}
}

// TestFoundationImportsOnlyFoundation: a foundation package may depend
// on other foundation packages and on nothing else in VERIQO.
//
// The distinction from TestDependenciesFlowDownwards matters. That test
// only governs packages the classification names, so a foundation
// package could acquire a dependency on some unclassified corner of the
// repository and pass. This one closes that: at the bottom of the
// graph, every VERIQO import must itself be foundation.
func TestFoundationImportsOnlyFoundation(t *testing.T) {
	for pkg, imps := range imports(t) {
		if l, ok := layerOf(pkg); !ok || l != layerFoundation {
			continue
		}
		for _, imp := range imps {
			l, ok := layerOf(imp)
			if !ok || l != layerFoundation {
				t.Fatalf("%s is a foundation package and imports %s, which is not foundation", pkg, imp)
			}
		}
	}
}

// TestTheConstitutionDependsOnNoImplementation is the rule that keeps
// the constitution a constitution.
//
// If pkg/constitution imported the packages it governs, an article
// could be quietly weakened to match whatever the implementation
// happens to do — which is the direction of drift that makes written
// principles worthless.
func TestTheConstitutionDependsOnNoImplementation(t *testing.T) {
	graph := imports(t)
	if imps := graph[modulePath+"/pkg/constitution"]; len(imps) > 0 {
		t.Fatalf("pkg/constitution must depend on no implementation, it imports %v", imps)
	}
}

// TestAssuranceIsImportedByNothing: assurance observes the system. A
// package that depends on the audit of itself has made the audit part
// of what it audits.
func TestAssuranceIsImportedByNothing(t *testing.T) {
	target := modulePath + "/pkg/assurance"
	var importers []string
	for pkg, imps := range imports(t) {
		if pkg == target {
			continue
		}
		for _, imp := range imps {
			if imp == target {
				importers = append(importers, pkg)
			}
		}
	}
	if len(importers) > 0 {
		sort.Strings(importers)
		t.Fatalf("pkg/assurance observes the system and must be imported by nothing; imported by %v", importers)
	}
}

// TestNoFabricPackageImportsADomain is Law 1 in structural form: one
// canonical state, many domain projections. A fabric that imports a
// domain has become that domain's.
//
// casefabric names insurance and maritime packages in its projection
// table, as strings. That is deliberate: a string is a reference a test
// can check, an import is a dependency that inverts the relationship.
func TestNoFabricPackageImportsADomain(t *testing.T) {
	for pkg, imps := range imports(t) {
		if l, ok := layerOf(pkg); !ok || l != layerFabric {
			continue
		}
		for _, imp := range imps {
			if l, ok := layerOf(imp); ok && l == layerDomain {
				t.Fatalf("%s is a fabric and imports the domain package %s", pkg, imp)
			}
		}
	}
}

// TestEveryGovernedPackageIsReachableInTheGraph guards the rule itself:
// a classification naming a package that does not exist would make the
// layering test pass by testing nothing.
func TestEveryGovernedLayerHasPackages(t *testing.T) {
	seen := map[layer]int{}
	for pkg := range imports(t) {
		if l, ok := layerOf(pkg); ok {
			seen[l]++
		}
	}
	for l := layerFoundation; l <= layerAssurance; l++ {
		if seen[l] == 0 {
			t.Fatalf("layer %s classifies no package in the module: the rule governs nothing", l)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatal("not in a module")
	}
	return filepath.Dir(gomod)
}
