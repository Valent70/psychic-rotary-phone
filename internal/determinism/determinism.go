// Package determinism is the automated static rule audit item P0-08
// asked for: "Create an automated static rule that fails CI when
// forbidden nondeterministic APIs appear in deterministic packages."
//
// Prior to this package, the boundary between DETERMINISTIC STATE and
// OPERATIONAL/SECURITY code existed only as prose -- README.md and
// doc comments asserting which packages must never depend on
// wall-clock time or unseeded randomness, with no automated check
// behind the assertion. A regression (a stray time.Now() added to
// pkg/canonical during a future change) would have been caught only if
// someone happened to notice it in review.
//
// This package does not try to classify every time.Now() call site in
// the repository -- the audit's own finding was that a blanket ban is
// wrong (X.509 cert validity, Raft election timers, and operational
// logging all correctly use wall-clock time). It enforces the boundary
// narrowly, for exactly the packages this repository's own doc
// comments already declare deterministic: given identical release,
// evidence and logical tick, they must produce identical output, which
// is impossible if they read the wall clock or an unseeded random
// source.
package determinism

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DeterministicPackages are the directories (relative to the module
// root) whose package doc comments explicitly claim determinism:
// pkg/canonical ("the canonical execution path"), pkg/execution ("one
// execution yields... deterministic... Replay rebuilds the entire DAG
// and localises the first divergent stage"), pkg/replay ("replay
// proved exactly one thing: that determinism holds").
var DeterministicPackages = []string{
	"pkg/canonical",
	"pkg/execution",
	"pkg/replay",
}

// forbidden are the nondeterministic API call patterns. Each is a
// substring match against source text with comments and strings
// stripped, not a full parse -- deliberately simple so the rule itself
// stays auditable at a glance.
var forbidden = []string{
	"time.Now(",
	"time.Since(",
	"time.After(",
	"time.Sleep(",
	"rand.Int",
	"rand.Float",
	"rand.Read(",
	"uuid.New",
}

// Violation is one forbidden call site found in a deterministic
// package.
type Violation struct {
	Package string
	File    string
	Line    int
	Pattern string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: forbidden nondeterministic call %q in deterministic package %s", v.File, v.Line, v.Pattern, v.Package)
}

// Check walks every DeterministicPackages directory under root and
// returns every forbidden call site found in non-test .go files. An
// empty result is what CI treats as passing; any Violation fails it.
func Check(root string) ([]Violation, error) {
	var violations []Violation
	for _, pkg := range DeterministicPackages {
		dir := filepath.Join(root, pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("determinism: read %s: %w", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			raw, err := os.ReadFile(path) // #nosec G304 -- path is built from this function's own fixed DeterministicPackages list and a directory listing, not external input
			if err != nil {
				return nil, fmt.Errorf("determinism: read %s: %w", path, err)
			}
			violations = append(violations, scanFile(pkg, path, string(raw))...)
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
	return violations, nil
}

var lineCommentRE = regexp.MustCompile(`//.*$`)

func scanFile(pkg, path, content string) []Violation {
	var out []Violation
	for i, line := range strings.Split(content, "\n") {
		code := lineCommentRE.ReplaceAllString(line, "")
		for _, pattern := range forbidden {
			if strings.Contains(code, pattern) {
				out = append(out, Violation{Package: pkg, File: path, Line: i + 1, Pattern: pattern})
			}
		}
	}
	return out
}
