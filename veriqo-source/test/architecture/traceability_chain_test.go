package architecture

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"veriqo/pkg/assurance"
	"veriqo/pkg/constitution"
)

// Traceability integrity.
//
// The review asked for one thing to be made unbreakable:
//
//	Requirement -> Control -> Code -> Test -> Evidence -> Report
//
// Every previous test in this repository checked ONE hop. The matrix
// checks that a control names code. TestEveryRuntimeEvidenceRefResolves
// checks that a citation resolves. Nothing checked the CHAIN, and a
// chain is exactly the thing that can be intact at every joint and
// still not reach from end to end -- because the joints are checked by
// different tests that never compare notes.
//
// This file walks the whole chain for every article and reports where
// it breaks. A break is not always a defect: an article that is
// honestly OPEN has no code, and demanding one would push the matrix
// towards claiming implementations that do not exist. So the rule is
// stated as: the chain must be intact UP TO the point the article's own
// verdict says it stops, and no further.
//
// That formulation is what makes the test useful rather than
// aspirational. It catches the two real failures:
//
//   - a link missing BEFORE the declared stopping point, which means
//     the verdict is claiming more than the chain supports;
//   - a link present AFTER it, which means the verdict is stale and the
//     article has quietly progressed without anybody reassessing it.

// hop is one link in the chain.
type hop struct {
	name    string
	present func(assurance.Trace) bool
	ref     func(assurance.Trace) string
}

// chain is the six hops, in order. Requirement is the constitutional
// article itself; Report is the assurance matrix row that renders it.
func chain() []hop {
	return []hop{
		{"CONTROL", func(t assurance.Trace) bool { return strings.TrimSpace(t.Control) != "" },
			func(t assurance.Trace) string { return t.Control }},
		{"CODE", func(t assurance.Trace) bool { return t.Code },
			func(t assurance.Trace) string { return t.CodeRef }},
		{"CALLED", func(t assurance.Trace) bool { return t.Called },
			func(t assurance.Trace) string { return t.CalledRef }},
		{"TEST", func(t assurance.Trace) bool { return t.Test },
			func(t assurance.Trace) string { return t.TestRef }},
		{"EVIDENCE", func(t assurance.Trace) bool { return t.Evidence },
			func(t assurance.Trace) string { return t.EvidenceRef }},
		{"RUNTIME_EVIDENCE", func(t assurance.Trace) bool { return t.RuntimeEvidence },
			func(t assurance.Trace) string { return t.RuntimeEvidenceRef }},
	}
}

// TestTheRequirementChainReachesEveryArticle is the REQUIREMENT hop:
// every constitutional article must have a control that serves it, and
// every control must serve an article that exists.
//
// Without this, the chain could be perfect from CONTROL onwards while
// starting from nothing.
func TestTheRequirementChainReachesEveryArticle(t *testing.T) {
	traces := assurance.Matrix()
	served := map[int]bool{}
	for _, tr := range traces {
		served[tr.Article] = true
	}

	var orphanControls, unservedArticles []string
	for _, a := range constitution.Articles() {
		if !served[a.Number] {
			unservedArticles = append(unservedArticles,
				fmt.Sprintf("article %d (%s) has no control in the matrix", a.Number, a.Title))
		}
	}
	known := map[int]bool{}
	for _, a := range constitution.Articles() {
		known[a.Number] = true
	}
	for _, tr := range traces {
		if !known[tr.Article] {
			orphanControls = append(orphanControls,
				fmt.Sprintf("control %q serves article %d, which is not in the constitution", tr.Control, tr.Article))
		}
	}

	sort.Strings(unservedArticles)
	sort.Strings(orphanControls)
	if len(unservedArticles) > 0 || len(orphanControls) > 0 {
		t.Fatalf("the requirement chain does not reach end to end.\n  %s",
			strings.Join(append(unservedArticles, orphanControls...), "\n  "))
	}
}

// TestNoLinkInTheChainIsSkipped is the integrity property.
//
// A trace with CODE and TEST but no CALLED describes a control that is
// tested and never invoked, being reported as though the chain ran
// through it. That is the shape of every "we have a test for that"
// claim that turns out not to be wired up.
func TestNoLinkInTheChainIsSkipped(t *testing.T) {
	var breaks []string
	for _, tr := range assurance.Matrix() {
		hops := chain()
		firstAbsent := -1
		for i, h := range hops {
			if !h.present(tr) {
				firstAbsent = i
				break
			}
		}
		if firstAbsent < 0 {
			continue // the whole chain is intact
		}
		// Everything after the first absent hop must also be absent.
		for i := firstAbsent + 1; i < len(hops); i++ {
			if hops[i].present(tr) {
				breaks = append(breaks, fmt.Sprintf(
					"article %d (%s): %s is absent but %s is present (%q). "+
						"A later link cannot be reached through a missing earlier one",
					tr.Article, tr.Control, hops[firstAbsent].name, hops[i].name, hops[i].ref(tr)))
			}
		}
	}
	if len(breaks) > 0 {
		sort.Strings(breaks)
		t.Fatalf("the traceability chain is broken.\n  %s", strings.Join(breaks, "\n  "))
	}
}

// TestEveryPresentLinkNamesItsReference. A link asserted with no
// reference is the "spreadsheet of good intentions" the matrix exists
// to avoid: it reads as a completed hop and points at nothing.
func TestEveryPresentLinkNamesItsReference(t *testing.T) {
	var unref []string
	for _, tr := range assurance.Matrix() {
		for _, h := range chain() {
			if h.present(tr) && strings.TrimSpace(h.ref(tr)) == "" {
				unref = append(unref, fmt.Sprintf("article %d (%s): %s is claimed with no reference",
					tr.Article, tr.Control, h.name))
			}
		}
	}
	if len(unref) > 0 {
		sort.Strings(unref)
		t.Fatalf("links are claimed without naming what satisfies them.\n  %s", strings.Join(unref, "\n  "))
	}
}

// TestEveryCodeReferencePointsAtSomethingThatExists is the CODE hop
// checked against the filesystem rather than against itself.
//
// A CodeRef naming a package that was renamed or deleted would keep the
// matrix green while the control it describes had ceased to exist.
func TestEveryCodeReferencePointsAtSomethingThatExists(t *testing.T) {
	root := repoRoot(t)
	var missing []string
	for _, tr := range assurance.Matrix() {
		if !tr.Code {
			continue
		}
		for _, pkg := range packagePathsIn(tr.CodeRef) {
			dir := filepath.Join(root, strings.TrimPrefix(pkg, "veriqo/"))
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				missing = append(missing, fmt.Sprintf("article %d (%s): CodeRef names %q, which is not a package in this module",
					tr.Article, tr.Control, pkg))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("code references point at packages that do not exist.\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestEveryTestReferenceNamesATestThatExists is the TEST hop, checked
// the same way: against the test functions actually declared.
//
// This is the link most likely to rot silently, because renaming a test
// is a refactor nobody thinks of as touching the assurance matrix.
func TestEveryTestReferenceNamesATestThatExists(t *testing.T) {
	root := repoRoot(t)
	declared := declaredTestNames(t, root)
	var missing []string
	for _, tr := range assurance.Matrix() {
		if !tr.Test {
			continue
		}
		for _, name := range testNamesIn(tr.TestRef) {
			if !declared[name] {
				missing = append(missing, fmt.Sprintf("article %d (%s): TestRef names %s, which is not declared anywhere",
					tr.Article, tr.Control, name))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("the matrix cites tests that do not exist.\n"+
			"A renamed test leaves the matrix green while the control loses its proof.\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// TestTheChainScanIsNotVacuous. If the reference parsers extracted
// nothing, the two tests above would pass over an empty set.
func TestTheChainScanIsNotVacuous(t *testing.T) {
	pkgs, tests := 0, 0
	for _, tr := range assurance.Matrix() {
		pkgs += len(packagePathsIn(tr.CodeRef))
		tests += len(testNamesIn(tr.TestRef))
	}
	if pkgs == 0 {
		t.Fatal("no package path was extracted from any CodeRef: the code-hop check governs nothing")
	}
	if tests == 0 {
		t.Fatal("no test name was extracted from any TestRef: the test-hop check governs nothing")
	}
}

// TestTheReportRendersEveryArticle is the REPORT hop. An article that
// the matrix holds but the rendered report omits would be invisible to
// every reader who does not read Go.
func TestTheReportRendersEveryArticle(t *testing.T) {
	rows, err := assurance.Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	report := assurance.Render(rows)
	for _, tr := range assurance.Matrix() {
		if !strings.Contains(report, fmt.Sprintf("%d", tr.Article)) {
			t.Errorf("the rendered report omits article %d", tr.Article)
		}
	}
	if len(rows) != len(assurance.Matrix()) {
		t.Fatalf("the report renders %d rows for %d traces", len(rows), len(assurance.Matrix()))
	}
}

// --- reference parsing --------------------------------------------------

// packagePathsIn extracts "veriqo/pkg/..." package paths from a
// free-text reference.
func packagePathsIn(ref string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(ref, func(r rune) bool {
		return r == ' ' || r == ',' || r == '(' || r == ')' || r == ';' || r == '\n'
	}) {
		if !strings.HasPrefix(field, "veriqo/") {
			continue
		}
		// Trim a trailing symbol: veriqo/pkg/proof.NewFinding -> the
		// package is everything before the last dot in the final
		// segment.
		p := field
		if i := strings.LastIndex(p, "/"); i >= 0 {
			last := p[i+1:]
			if j := strings.Index(last, "."); j >= 0 {
				p = p[:i+1] + last[:j]
			}
		}
		p = strings.TrimRight(p, ".,;")
		out = append(out, p)
	}
	return out
}

// testNamesIn extracts Test... identifiers from a free-text reference.
func testNamesIn(ref string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(ref, func(r rune) bool {
		return r == ' ' || r == ',' || r == '(' || r == ')' || r == ';' || r == '\n'
	}) {
		field = strings.TrimRight(field, ".,;")
		if strings.HasPrefix(field, "Test") && len(field) > 4 {
			out = append(out, field)
		}
	}
	return out
}

// declaredTestNames collects every Test function declared in the module.
func declaredTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if name := info.Name(); name == "vendor" || (strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "func Test") {
				continue
			}
			rest := strings.TrimPrefix(line, "func ")
			if i := strings.Index(rest, "("); i > 0 {
				out[rest[:i]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("collecting test names: %v", err)
	}
	return out
}
