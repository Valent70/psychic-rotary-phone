package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// VERIQO's anti-duplication rule has always been checked against the
// code that exists. That is not enough. The review put the risk
// precisely: if a developer one day adds
//
//	func BuildFinding(...) proof.Finding
//
// to the graph package, the system is back to two finding authorities
// and every test still passes, because every test was written about
// today's call graph.
//
// So this file does not check today's callers. It parses the module and
// enforces a rule over the shape of the import graph, which holds for
// code that has not been written yet: an authoritative object may be
// constructed only inside the package that owns the decision, and by
// the orchestration layer that drives the chain.
//
// Why commands and tests are allowed. cmd/ binaries and tests are the
// callers that RUN the chain -- something has to invoke Seal to seal
// anything. What must never happen is a LIBRARY package acquiring the
// ability to mint an authoritative object as a side effect of doing its
// own job, because a library is a thing other code composes with
// unknowingly. A command is a top-level program a human ran.

// authority names one authoritative constructor and the package that
// owns it.
type authority struct {
	// decision is the question only this constructor may answer.
	decision string
	// pkgPath is the import path of the owning package.
	pkgPath string
	// funcs are the exported constructors that produce the object.
	funcs []string
}

// theFiveAuthorities is the list the review asked to be proven:
// ONE PROOF, ONE FINDING, ONE DECISION, ONE CASE RESOLUTION, ONE LEDGER.
//
// Case resolution and the ledger are methods on a type rather than
// package-level functions, so they are proven by a different means --
// see TestCaseResolutionHasOneAuthority and TestLedgerHasOneAuthority
// below. The four here are package-level constructors and are proven by
// the call rule.
var theFiveAuthorities = []authority{
	{
		decision: "ONE PROOF AUTHORITY: is this proof object sealed, and is it sufficient?",
		pkgPath:  "veriqo/pkg/proof",
		funcs:    []string{"Seal"},
	},
	{
		decision: "ONE FINDING AUTHORITY: does a finding exist for this proof object?",
		pkgPath:  "veriqo/pkg/proof",
		funcs:    []string{"NewFinding"},
	},
	{
		decision: "ONE DECISION AUTHORITY: has an authority adopted this finding, and what follows?",
		pkgPath:  "veriqo/pkg/proof",
		funcs:    []string{"Authorize", "Decide"},
	},
}

// TestNoLibraryPackageCanMintAnAuthoritativeObject is the future-proof
// rule. It fails for code that does not exist yet.
func TestNoLibraryPackageCanMintAnAuthoritativeObject(t *testing.T) {
	root := repoRoot(t)
	var complaints []string

	for _, a := range theFiveAuthorities {
		owner := path.Base(a.pkgPath)
		for _, call := range findCalls(t, root, owner, a.funcs) {
			if call.inOwningPackage || call.inCommand || call.inTest {
				continue
			}
			complaints = append(complaints, fmt.Sprintf(
				"%s calls %s.%s at %s\n      violates: %s",
				call.pkg, owner, call.fn, call.pos, a.decision))
		}
	}

	if len(complaints) > 0 {
		sort.Strings(complaints)
		t.Fatalf("a library package acquired the ability to mint an authoritative object.\n"+
			"Derivation is permitted; a second construction path is not. If the package\n"+
			"needs the object, take it as a parameter -- the way caseproofgraph.Build\n"+
			"takes findings after it stopped calling proof.NewFinding.\n\n  %s",
			strings.Join(complaints, "\n  "))
	}
}

// TestTheAuthorityRuleIsNotVacuous proves the scan finds real calls. A
// rule that matches nothing anywhere would pass forever without
// governing anything, which is the failure mode of every architecture
// test written and then forgotten.
func TestTheAuthorityRuleIsNotVacuous(t *testing.T) {
	root := repoRoot(t)
	total := 0
	for _, a := range theFiveAuthorities {
		total += len(findCalls(t, root, path.Base(a.pkgPath), a.funcs))
	}
	if total == 0 {
		t.Fatal("the scan found no call to any authoritative constructor anywhere in the module: " +
			"the rule governs nothing, so it would not catch a violation either")
	}
}

// TestCaseResolutionHasOneAuthority proves that exactly one function
// in the module produces a casefabric.Outcome.
//
// Keying on the RETURN TYPE rather than on the name is deliberate. The
// module contains several functions called Resolve -- identity
// resolution, entity resolution, evidence-api resolution -- and none of
// them resolves a case. A rule that flagged them would be noise, and a
// rule tuned until the noise stopped would stop catching anything. The
// question that matters is not "who is called Resolve" but "who can
// produce the object that says a case is over".
func TestCaseResolutionHasOneAuthority(t *testing.T) {
	root := repoRoot(t)
	producers := findProducersOf(t, root, "Outcome", "casefabric")
	if len(producers) != 1 {
		sort.Strings(producers)
		t.Fatalf("exactly one function may produce a casefabric.Outcome, found %d:\n  %s\n"+
			"A second producer is a second answer to \"may this case resolve?\".",
			len(producers), strings.Join(producers, "\n  "))
	}
	if !strings.Contains(producers[0], "pkg/casefabric") {
		t.Fatalf("case resolution authority left pkg/casefabric: %s", producers[0])
	}
}

// TestLedgerHasOneAuthority proves that exactly one function produces an
// audit.AuditRecord.
//
// A word on what this does NOT claim. The module holds several
// hash-linked structures -- the event contract chain, the write-ahead
// log, the evidence store. Each is a real chain and each is legitimate;
// they are storage and transport mechanisms, not the audit ledger.
// Claiming "one hash chain in VERIQO" would be false, so the rule is
// stated over the canonical audit record instead, which is the object
// the constitution's ledger obligations are written about.
func TestLedgerHasOneAuthority(t *testing.T) {
	root := repoRoot(t)
	producers := findProducersOf(t, root, "AuditRecord", "audit")
	if len(producers) != 1 {
		sort.Strings(producers)
		t.Fatalf("exactly one function may produce an audit.AuditRecord, found %d:\n  %s",
			len(producers), strings.Join(producers, "\n  "))
	}
	if !strings.Contains(producers[0], "pkg/platform/audit") {
		t.Fatalf("the ledger authority moved out of pkg/platform/audit: %s", producers[0])
	}
}

// TestTheProducerScanIsNotVacuous guards the two rules above the same
// way TestTheAuthorityRuleIsNotVacuous guards the call rule: a type name
// nothing returns would make both tests pass while governing nothing.
func TestTheProducerScanIsNotVacuous(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct{ typeName, pkg string }{
		{"Outcome", "casefabric"},
		{"AuditRecord", "audit"},
		{"Finding", "proof"},
	} {
		if got := findProducersOf(t, root, tc.typeName, tc.pkg); len(got) == 0 {
			t.Fatalf("nothing in the module produces %s.%s: the scan is broken, not the architecture",
				tc.pkg, tc.typeName)
		}
	}
}

// --- the scanner -----------------------------------------------------

type callSite struct {
	pkg             string
	fn              string
	pos             string
	inOwningPackage bool
	inCommand       bool
	inTest          bool
}

// findCalls walks the module for selector calls of the form
// <ownerAlias>.<fn>(...), where ownerAlias is how the file imported the
// owning package.
func findCalls(t *testing.T, root, ownerPkgName string, fns []string) []callSite {
	t.Helper()
	want := map[string]bool{}
	for _, f := range fns {
		want[f] = true
	}
	var out []callSite
	forEachGoFile(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
		alias := importAlias(file, ownerPkgName)
		if alias == "" {
			return
		}
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		ast.Inspect(file, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != alias || !want[sel.Sel.Name] {
				return true
			}
			out = append(out, callSite{
				pkg:             pkgDir,
				fn:              sel.Sel.Name,
				pos:             rel + ":" + strconv.Itoa(fset.Position(sel.Pos()).Line),
				inOwningPackage: strings.HasSuffix(pkgDir, "pkg/"+ownerPkgName),
				inCommand:       strings.HasPrefix(pkgDir, "cmd/"),
				inTest:          strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(pkgDir, "test/"),
			})
			return true
		})
	})
	return out
}

// findProducersOf returns every non-test function or method in the
// module whose declared results include the named type, qualified by
// the named package (or unqualified, when declared in that package
// itself).
//
// It reads declarations rather than calls, so a producer added tomorrow
// is caught the moment it is written, whether or not anything calls it
// yet. That is the point: the review's worry was a function that exists
// before anybody notices it.
func findProducersOf(t *testing.T, root, typeName, pkgName string) []string {
	t.Helper()
	var out []string
	forEachGoFile(t, root, func(rel string, fset *token.FileSet, file *ast.File) {
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(filepath.ToSlash(rel), "test/") {
			return
		}
		own := file.Name.Name == pkgName
		alias := importAlias(file, pkgName)
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Type.Results == nil {
				continue
			}
			for _, res := range fd.Type.Results.List {
				if !resultIsType(res.Type, typeName, alias, own) {
					continue
				}
				// Returning the type is not the same as producing it.
				// Accessors return a stored value; error paths return
				// the zero value; wrappers forward what an authority
				// gave them. Only a function that populates a
				// composite literal of the type is constructing one,
				// and construction is what an authority does.
				if !constructsNonEmpty(fd, typeName, alias, own) {
					continue
				}
				name := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					name = receiverName(fd.Recv.List[0].Type) + "." + name
				}
				out = append(out, fmt.Sprintf("%s (%s:%d)", name, rel, fset.Position(fd.Pos()).Line))
				break
			}
		}
	})
	return out
}

// constructsNonEmpty reports whether a declaration builds a composite
// literal of the type with at least one field set.
//
// The empty literal is excluded on purpose: `return Outcome{}, err` is
// how every error path in the module spells "no outcome", and counting
// those as construction would make every function that can fail look
// like an authority.
func constructsNonEmpty(fd *ast.FuncDecl, typeName, alias string, own bool) bool {
	if fd.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || len(cl.Elts) == 0 {
			return true
		}
		if resultIsType(cl.Type, typeName, alias, own) {
			found = true
		}
		return !found
	})
	return found
}

// resultIsType reports whether a result expression names the target
// type, either as pkg.Type from outside or as Type from within.
func resultIsType(e ast.Expr, typeName, alias string, own bool) bool {
	switch v := e.(type) {
	case *ast.StarExpr:
		return resultIsType(v.X, typeName, alias, own)
	case *ast.Ident:
		return own && v.Name == typeName
	case *ast.SelectorExpr:
		id, ok := v.X.(*ast.Ident)
		return ok && alias != "" && id.Name == alias && v.Sel.Name == typeName
	}
	return false
}

func receiverName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return receiverName(v.X)
	case *ast.Ident:
		return v.Name
	}
	return "?"
}

// importAlias returns the identifier a file uses for the named package,
// or "" if the file does not import it.
func importAlias(file *ast.File, pkgName string) string {
	for _, im := range file.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		if path.Base(p) != pkgName {
			continue
		}
		if im.Name != nil {
			return im.Name.Name
		}
		return pkgName
	}
	return ""
}

// forEachGoFile parses every Go file in the module, skipping vendored
// and hidden trees.
func forEachGoFile(t *testing.T, root string, fn func(rel string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if info.IsDir() {
			if name == "vendor" || name == "testdata" || (strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil // a file that does not parse is the compiler's complaint, not ours
		}
		rel, _ := filepath.Rel(root, p)
		fn(filepath.ToSlash(rel), fset, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
}

// --- negative proofs -------------------------------------------------
//
// An architecture test that has never failed is a hypothesis, not a
// control. These run the same scanners over a synthetic module that
// contains exactly the violation the review warned about, and fail if
// the scanner lets it through.

// TestTheScannerCatchesAGraphPackageThatMintsFindings builds the
// reviewer's scenario literally -- a caseproofgraph package that grows a
// BuildFinding calling proof.NewFinding -- and proves the call rule
// sees it.
func TestTheScannerCatchesAGraphPackageThatMintsFindings(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "pkg/caseproofgraph/build.go", `package caseproofgraph

import "veriqo/pkg/proof"

// The violation: a library package that can mint a finding.
func BuildFinding(o proof.Object) (proof.Finding, error) {
	return proof.NewFinding(o, 0)
}
`)
	calls := findCalls(t, root, "proof", []string{"NewFinding"})
	if len(calls) != 1 {
		t.Fatalf("expected the scanner to see one call, saw %d", len(calls))
	}
	c := calls[0]
	if c.inOwningPackage || c.inCommand || c.inTest {
		t.Fatalf("the violation was excused as owning/command/test: %+v", c)
	}
}

// TestTheScannerDoesNotFlagACommandThatDrivesTheChain proves the
// exemption is real and not an accident of the same scan: something has
// to call Seal for anything to be sealed.
func TestTheScannerDoesNotFlagACommandThatDrivesTheChain(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "cmd/veriqo-runtime-evidence/main.go", `package main

import "veriqo/pkg/proof"

func main() { _, _ = proof.NewFinding(proof.Object{}, 0) }
`)
	calls := findCalls(t, root, "proof", []string{"NewFinding"})
	if len(calls) != 1 {
		t.Fatalf("expected one call, got %d", len(calls))
	}
	if !calls[0].inCommand {
		t.Fatal("a cmd/ caller was not recognised as the orchestration layer")
	}
}

// TestTheScannerCatchesASecondOutcomeProducer proves the producer rule
// would see a second answer to "may this case resolve?" appearing in a
// package that has no business answering it.
func TestTheScannerCatchesASecondOutcomeProducer(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "pkg/insurance/shortcut/shortcut.go", `package shortcut

import "veriqo/pkg/casefabric"

// The violation: a second construction site for the object that says a
// case is over.
func Settle(d string) casefabric.Outcome {
	return casefabric.Outcome{Disposition: d, Summary: "settled"}
}
`)
	got := findProducersOf(t, root, "Outcome", "casefabric")
	if len(got) != 1 {
		t.Fatalf("the scanner missed a second Outcome producer, found %v", got)
	}
}

// TestTheScannerIgnoresAZeroValueReturn proves the discrimination that
// makes the producer rule usable: every error path in the module
// returns the zero value, and counting those as construction would make
// the rule fire everywhere and therefore mean nothing.
func TestTheScannerIgnoresAZeroValueReturn(t *testing.T) {
	root := t.TempDir()
	writeGo(t, root, "pkg/somewhere/somewhere.go", `package somewhere

import (
	"errors"

	"veriqo/pkg/casefabric"
)

func Try() (casefabric.Outcome, error) {
	return casefabric.Outcome{}, errors.New("no")
}
`)
	if got := findProducersOf(t, root, "Outcome", "casefabric"); len(got) != 0 {
		t.Fatalf("a zero-value error return was counted as construction: %v", got)
	}
}

func writeGo(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
