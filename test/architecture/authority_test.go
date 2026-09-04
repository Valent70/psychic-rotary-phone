package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"veriqo/pkg/findings"
)

// FC-001 AUTHORITY_DIFFUSION, checked at the module level.
//
// pkg/findings closes the class structurally: the mint witness is an
// unexported field, so no other package can produce an authoritative
// Finding. That is the real protection.
//
// This file is the SCANNER that proves the protection is still in
// place -- and, more importantly, that it would notice if somebody
// tried. A structural guarantee nobody checks is a structural
// guarantee until the day the field is exported "temporarily".

// findingsPackage is the only package permitted to construct an
// authoritative object.
const findingsPackage = "pkg/findings"

// mintLiteralsOutsideFindings walks the module for composite literals
// of findings.Finding constructed anywhere but pkg/findings.
//
// Finding such a literal is not itself a defect -- a test constructs
// one deliberately to prove it is inert. What matters is that the
// literal cannot be MINTED, which the type system guarantees; this
// scanner reports where they occur so a reviewer can confirm each one
// is a demonstration rather than an attempt.
func mintLiteralsOutsideFindings(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(filepath.ToSlash(rel), findingsPackage+"/") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Errorf("architecture: %s does not parse: %v", path, perr)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "findings" || sel.Sel.Name != "Finding" {
				return true
			}
			out = append(out, rel)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("architecture: walking %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// TestTheScannerCatchesAGraphPackageThatMintsFindings is the mutation
// test the register cites.
//
// The mutation is not a code edit: it is constructing the value the
// invariant forbids and asking the system to accept it. A package
// outside pkg/findings CAN write the struct literal -- Go permits it,
// and pretending otherwise would be the overclaim. What it cannot do
// is produce one that passes findings.Require.
func TestTheScannerCatchesAGraphPackageThatMintsFindings(t *testing.T) {
	// Construct the value exactly as a graph package would, from
	// outside pkg/findings, with every exported field set to look
	// authoritative.
	forged := findings.Finding{
		ID: "finding:minted-by-the-graph", TenantID: "t-acme", CaseID: "case-1",
		ClaimID: "claim:c1", ProofID: "reverseproof:rp1",
		Statement:               "the graph concluded this on its own authority",
		ApprovedBy:              "human:reviewer-1",
		Limitations:             []string{"none"},
		AlternativeExplanations: []string{"none"},
	}
	if forged.Minted() {
		t.Fatal("MUTANT SURVIVED: a package outside pkg/findings produced a value that " +
			"reports itself minted")
	}
	if err := findings.Require(forged); err == nil {
		t.Fatal("MUTANT SURVIVED: a fabricated finding passed the consumer guard")
	}
	if _, err := forged.Digest(); err == nil {
		t.Fatal("MUTANT SURVIVED: a fabricated finding produced an authoritative digest")
	}

	// And the scanner itself must be capable of seeing such a literal,
	// or the check above is the only protection and nobody would
	// notice a new one appearing.
	found := mintLiteralsOutsideFindings(t, repoRoot(t))
	if len(found) == 0 {
		t.Fatal("the scanner found no Finding literal outside pkg/findings, including the " +
			"one in this function; it is not matching")
	}
	sawThisFile := false
	for _, p := range found {
		if strings.HasSuffix(p, "authority_test.go") {
			sawThisFile = true
		}
	}
	if !sawThisFile {
		t.Fatalf("the scanner missed the literal in this very file; it is matching too "+
			"tightly. Found: %v", found)
	}
}

// TestEveryFindingLiteralOutsideFindingsIsADemonstration.
//
// The scanner reports where authoritative objects are constructed
// outside their package. Each such site must be a test proving the
// value is inert -- never production code building one to use.
func TestEveryFindingLiteralOutsideFindingsIsADemonstration(t *testing.T) {
	found := mintLiteralsOutsideFindings(t, repoRoot(t))
	var production []string
	for _, p := range found {
		if !strings.HasSuffix(p, "_test.go") {
			production = append(production, p)
		}
	}
	if len(production) > 0 {
		t.Fatalf("production code constructs findings.Finding outside pkg/findings.\n"+
			"Such a value can never be minted, so this is either dead code or an attempt "+
			"to conclude without authority:\n  %s", strings.Join(production, "\n  "))
	}
}
