package rest

import (
	"os"
	"strings"
	"testing"
)

// forbiddenGovernedDecisionMarkers are the identifiers that only ever
// belong to a REAL governed decision -- one that went through
// pkg/lifecycle.Orchestrator.RunUnified's full chain and ended up in a
// signed certificate (see respRunUnified in lifecycle_route.go, and
// pkg/lifecycle.Result/pkg/execution's own Result types). Registry's
// engines are fine-grained component operations by design (see
// docs/ARCHITECTURE.md, "The canonical execution API, and Registry's
// real role"); none of them should ever start returning one of these,
// because that would mean a second, competing path to producing this
// system's output of record has quietly opened.
var forbiddenGovernedDecisionMarkers = []string{
	"CertificateHash",
	"certificate_hash",
	"DecisionID",
	"decision_id",
	"ExecutionRootHash",
	"execution_root_hash",
}

// registryArchitectureScanFiles are the source files that define what
// veriqo/registry.Registry-backed HTTP routes can return.
// lifecycle_route.go and lifecycle_route_test.go are deliberately
// excluded: they are the ONE path allowed to carry these markers.
var registryArchitectureScanFiles = []string{
	"generated_routes.go",
	"../../registry/registry.go",
}

// TestRegistryNeverProducesGovernedDecisionArtifacts is the enforced
// half of the Registry-vs-Lifecycle architectural decision recorded in
// docs/ARCHITECTURE.md: "Registry is a compatibility/control surface.
// Lifecycle/Execution is the ONE canonical execution API for governed
// decisions." A prior external audit flagged that running both systems
// side by side, with no stated canonical path, risks recreating the
// exact "three engines / two truths" problem this project has already
// had to unwind once. This test makes the split machine-checked rather
// than a convention a future change could silently violate: it scans
// Registry's own source and its generated HTTP routes for any of the
// identity fields that only a real, certificate-backed governed
// decision (produced exclusively by pkg/lifecycle.Orchestrator.
// RunUnified) is entitled to return, and fails the build the day either
// one appears -- catching the drift before it ships, not at the next
// audit.
func TestRegistryNeverProducesGovernedDecisionArtifacts(t *testing.T) {
	for _, relPath := range registryArchitectureScanFiles {
		raw, err := os.ReadFile(relPath) // #nosec G304 -- fixed, hardcoded list of this package's own source files
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		src := string(raw)
		for _, marker := range forbiddenGovernedDecisionMarkers {
			if strings.Contains(src, marker) {
				t.Errorf("%s contains %q -- Registry engines must never return a governed-decision identity field; that capability belongs exclusively to pkg/lifecycle.Orchestrator.RunUnified (see docs/ARCHITECTURE.md)",
					relPath, marker)
			}
		}
	}
}
