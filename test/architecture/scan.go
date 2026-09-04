package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Source scanning for the architecture tests.
//
// These helpers exist so the citation checks read the MODULE rather
// than a hand-maintained list. A list of test names would itself go
// stale, which is the failure class (FC-004) the checks are about --
// so the scanner walks the tree and parses it.
//
// Parsing rather than grepping is deliberate. A regex over source text
// matches a test name inside a comment, inside a string literal, or
// inside a block somebody commented out, and would therefore report a
// citation as satisfied by a test that no longer runs.

// repoRoot walks up from the test's working directory until it finds
// go.mod.
//
// It fails the test rather than returning an error, because every
// caller would otherwise have to decide what to do when the module
// cannot be located -- and there is only one sensible answer.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("architecture: locating the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("architecture: no go.mod found above the working directory; " +
				"the scanner cannot locate the module it is supposed to govern")
		}
		dir = parent
	}
}

// declaredTestNames returns every Test/Benchmark/Fuzz/Example function
// declared anywhere in the module.
//
// It parses each _test.go file, so a name that appears only in a
// comment or a string is not counted -- which is the difference
// between "this test exists" and "this name appears in the source".
func declaredTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip directories that are not part of the module's own
			// source: vendored code and VCS metadata would add names
			// this module does not own.
			switch info.Name() {
			case ".git", "vendor", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file that does not parse cannot be said to declare
			// anything. Reporting it is better than silently treating
			// its tests as absent.
			t.Errorf("architecture: %s does not parse: %v", path, perr)
			return nil
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil {
				continue
			}
			name := fn.Name.Name
			if strings.HasPrefix(name, "Test") ||
				strings.HasPrefix(name, "Benchmark") ||
				strings.HasPrefix(name, "Fuzz") ||
				strings.HasPrefix(name, "Example") {
				out[name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("architecture: walking %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatal("architecture: the scanner found no test declarations at all; " +
			"every citation check built on it would pass vacuously")
	}
	return out
}
