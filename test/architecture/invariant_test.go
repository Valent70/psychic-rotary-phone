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
)

// Law 11 is a system-wide invariant, and this file is what makes that
// sentence true rather than aspirational.
//
//	No system surface may emit an assurance state higher than the
//	state derived from the evidence.
//
// pkg/assurance/invariant is the chokepoint. A chokepoint only works
// if there is no way around it, and "there is no way around it" is a
// property of the WHOLE MODULE, not of that package. So this scanner
// reads every file and looks for the two ways around it:
//
//  1. a package outside the assurance layer constructing a high
//     assurance state directly, and
//  2. a package writing one of the high state NAMES as a string,
//     which is how a state reaches a report, an export or an API
//     response without ever being a typed value.
//
// The second is the one that actually happens. Nobody bypasses a type
// system on purpose; somebody writes `"PRODUCTION_QUALIFIED"` into a
// JSON field because that is what the spreadsheet said.

// highStates are the assurance states that require a party who is not
// the implementer. Emitting one of these is the act being governed.
var highStates = []string{
	"EXTERNALLY_TESTED",
	"EXTERNALLY_VALIDATED",
	"OPERATIONALLY_PROVEN",
	"PRODUCTION_QUALIFIED",
}

// assuranceLayer is where reasoning ABOUT assurance states legitimately
// lives. Files under these prefixes may name the states, because
// naming them is their job.
var assuranceLayer = []string{
	"pkg/assurance/",
	"pkg/verification/",
	"pkg/qualification/",
	"pkg/readiness/",
	"pkg/gates/",
	"pkg/scorecard/",
	"test/",
	"cmd/veriqoctl/",
	"cmd/veriqo-verify/",
}

func inAssuranceLayer(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range assuranceLayer {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// goFiles walks the module and returns every .go file, relative to
// root.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "dist", "vendor", "capsule":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("architecture: walking the module: %v", err)
	}
	sort.Strings(out)
	return out
}

// TestNoPackageOutsideTheAssuranceLayerNamesAHighAssuranceState.
//
// This is the invariant's enforcement. A package that can write
// "PRODUCTION_QUALIFIED" into an output can publish an assurance state
// the evidence does not support, and it will not look like a bypass --
// it will look like a field being set.
func TestNoPackageOutsideTheAssuranceLayerNamesAHighAssuranceState(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offences []string

	for _, rel := range goFiles(t, root) {
		if inAssuranceLayer(rel) {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("architecture: parsing %s: %v", rel, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, s := range highStates {
				if strings.Contains(lit.Value, s) {
					pos := fset.Position(lit.Pos())
					offences = append(offences, rel+":"+itoa(pos.Line)+" names "+s)
				}
			}
			return true
		})
	}

	if len(offences) > 0 {
		sort.Strings(offences)
		t.Fatalf("%d place(s) outside the assurance layer name a high assurance state "+
			"directly. Each is a surface that can publish a state the evidence does not "+
			"support, and none of them will look like a bypass -- they look like a field "+
			"being set:\n  %s\n\nRoute the value through "+
			"pkg/assurance/invariant.Emit, which returns the derived state and cannot "+
			"return more than the evidence supports.",
			len(offences), strings.Join(offences, "\n  "))
	}
}

// TestTheScannerWouldCatchABypass.
//
// A scanner that has never caught anything might be working or might
// be looking in the wrong place, and the difference matters more here
// than almost anywhere else in the module.
func TestTheScannerWouldCatchABypass(t *testing.T) {
	dir := t.TempDir()
	src := `package leak

// A perfectly ordinary-looking function in a perfectly ordinary
// package, doing the thing the invariant exists to prevent.
func Status() map[string]string {
	return map[string]string{"assurance": "PRODUCTION_QUALIFIED"}
}
`
	path := filepath.Join(dir, "leak.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		for _, s := range highStates {
			if strings.Contains(lit.Value, s) {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Fatal("the scanner did not see a literal high assurance state in a synthetic " +
			"package; the check above is passing because it looks at nothing")
	}
}

// TestTheAssuranceLayerIsNotTheWholeModule.
//
// The exemption list is the scanner's blind spot. If it grew to cover
// most of the module the check above would still pass and would mean
// nothing.
func TestTheAssuranceLayerIsNotTheWholeModule(t *testing.T) {
	root := repoRoot(t)
	all := goFiles(t, root)
	exempt := 0
	for _, rel := range all {
		if inAssuranceLayer(rel) {
			exempt++
		}
	}
	if exempt*2 > len(all) {
		t.Fatalf("%d of %d files are exempt from the assurance-state scan. The exemption "+
			"list has grown to cover most of the module, which means the check passes "+
			"without checking anything", exempt, len(all))
	}
	if len(all)-exempt < 30 {
		t.Fatalf("only %d files are actually scanned", len(all)-exempt)
	}
}

// TestEverySurfaceInTheInvariantIsReachableFromTheModule.
//
// The Surface enumeration is a claim about where assurance states can
// reach the world. A surface named there and absent from the module is
// a claim about nothing; a place in the module that publishes state
// and is absent from there is a hole.
func TestEverySurfaceInTheInvariantIsReachableFromTheModule(t *testing.T) {
	root := repoRoot(t)
	// The surfaces that must correspond to something real today. UI
	// and AUTOMATION are deliberately enumerated ahead of existing --
	// the invariant should govern a surface before somebody builds it,
	// not after.
	want := map[string]string{
		"RELEASE_AUTHORITY":    "pkg/scorecard",
		"PASSPORT_ISSUER":      "pkg/passport",
		"API":                  "pkg/api",
		"CLI":                  "cmd/veriqoctl",
		"REPORT":               "pkg/assurance/register",
		"QUALIFICATION_LEDGER": "pkg/qualification/ledger",
		"AUDITOR_CAPSULE":      "pkg/assurance/capsule",
		"EXPORT":               "pkg/verification",
	}
	for surface, dir := range want {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("surface %s names %s, which does not exist: %v", surface, dir, err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
