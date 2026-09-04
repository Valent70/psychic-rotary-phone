package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// FC-004 STALE_CITATION.
//
// The finding was nine articles citing tests that had been renamed
// away. The register that records the class is checked by
// failure_class_test.go; these three checks govern the OTHER things
// this repository cites -- runtime evidence records, and test names
// referenced from documentation.
//
// The class is closed when the invariant governs everything of that
// shape, which includes the citations nobody thought of as citations.

// runtimeEvidence is the shape of evidence/RUNTIME_EVIDENCE.json.
type runtimeEvidence struct {
	Schema  string `json:"schema"`
	CaseID  string `json:"case_id"`
	Records []struct {
		Index    int    `json:"index"`
		EventID  string `json:"event_id"`
		Actor    string `json:"actor"`
		Action   string `json:"action"`
		Hash     string `json:"hash"`
		PrevHash string `json:"prev_hash"`
	} `json:"records"`
}

func loadRuntimeEvidence(t *testing.T, root string) runtimeEvidence {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "evidence", "RUNTIME_EVIDENCE.json"))
	if err != nil {
		t.Fatalf("architecture: the runtime evidence artefact is missing: %v", err)
	}
	var re runtimeEvidence
	if err := json.Unmarshal(b, &re); err != nil {
		t.Fatalf("architecture: the runtime evidence artefact does not parse: %v", err)
	}
	if len(re.Records) == 0 {
		t.Fatal("architecture: the runtime evidence artefact carries no records; " +
			"every citation of it would resolve vacuously")
	}
	return re
}

// auditRef matches a citation of a runtime record, e.g.
// AUDIT-014-redaction.derivative_released.
var auditRef = regexp.MustCompile(`AUDIT-\d{3}-[a-z_]+\.[a-z_]+`)

// TestEveryCitedRuntimeRecordExists is the positive test.
//
// The documentation cites specific audit records as proof that a chain
// executed. A citation of a record the run did not emit is a claim
// about an execution that did not happen -- and it looks exactly like
// a claim about one that did.
func TestEveryCitedRuntimeRecordExists(t *testing.T) {
	root := repoRoot(t)
	re := loadRuntimeEvidence(t, root)

	emitted := map[string]bool{}
	for _, r := range re.Records {
		emitted[r.EventID] = true
	}

	cited := map[string][]string{} // event id -> files citing it
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
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".go":
		default:
			return nil
		}
		// The evidence artefact is the source, not a citation of itself.
		if strings.HasSuffix(path, "RUNTIME_EVIDENCE.json") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range auditRef.FindAllString(string(b), -1) {
			rel, _ := filepath.Rel(root, path)
			cited[m] = append(cited[m], rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("architecture: walking the tree: %v", err)
	}

	var stale []string
	for id, files := range cited {
		if !emitted[id] {
			sort.Strings(files)
			stale = append(stale, id+" cited in "+strings.Join(files, ", "))
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("documentation cites runtime records this run did not emit.\n"+
			"A citation of a record that does not exist is a claim about an execution "+
			"that did not happen:\n  %s", strings.Join(stale, "\n  "))
	}
}

// TestAStaleCitationIsRecognisedAsStale is the negative test.
//
// The check above passes today. This proves it is capable of failing:
// a fabricated record id must NOT resolve against the artefact. Without
// this, a bug that made every lookup succeed would leave the positive
// test green forever.
func TestAStaleCitationIsRecognisedAsStale(t *testing.T) {
	root := repoRoot(t)
	re := loadRuntimeEvidence(t, root)

	emitted := map[string]bool{}
	for _, r := range re.Records {
		emitted[r.EventID] = true
	}
	// The fabricated id is ASSEMBLED rather than written out. A
	// literal here would be picked up by the scanner in
	// TestEveryCitedRuntimeRecordExists as a stale citation in this
	// very file -- and adding an exception for this file would put a
	// hole in the check exactly where somebody could hide one.
	fabricated := "AUDIT-" + "999" + "-nothing.happened"
	if emitted[fabricated] {
		t.Fatal("a fabricated record id resolved against the runtime evidence; " +
			"the citation check cannot distinguish a real record from an invented one")
	}
	// And the resolver must actually resolve the real ones, or the
	// check above is vacuous in the other direction.
	if !emitted[re.Records[0].EventID] {
		t.Fatalf("the artefact's own first record %q does not resolve", re.Records[0].EventID)
	}
	// The record ids must also be unique: two records sharing an id
	// would make a citation ambiguous rather than stale, which is a
	// different failure with the same symptom.
	seen := map[string]bool{}
	for _, r := range re.Records {
		if seen[r.EventID] {
			t.Errorf("duplicate runtime record id %q", r.EventID)
		}
		seen[r.EventID] = true
	}
}

// testNameInProse matches a Go test name written in documentation.
var testNameInProse = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9]{6,}\b`)

// TestEveryTestReferenceNamesATestThatExists is the regression test:
// it governs the whole repository rather than the one register.
//
// This is the check that would have caught the original finding. Nine
// articles cited tests by name in prose; the tests were renamed; the
// prose stayed. Nothing failed, because prose does not compile.
func TestEveryTestReferenceNamesATestThatExists(t *testing.T) {
	root := repoRoot(t)
	declared := declaredTestNames(t, root)

	var stale []string
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
		// Documentation and non-test Go source. A _test.go file
		// mentioning a name it also declares is not a citation.
		if !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		for _, name := range testNameInProse.FindAllString(string(b), -1) {
			if !declared[name] {
				stale = append(stale, rel+" cites "+name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("architecture: walking the tree: %v", err)
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		unique := stale[:0]
		var last string
		for _, s := range stale {
			if s != last {
				unique = append(unique, s)
				last = s
			}
		}
		t.Fatalf("prose cites tests that are not declared anywhere.\n"+
			"Prose does not compile, so a renamed test leaves the claim standing:\n  %s",
			strings.Join(unique, "\n  "))
	}
}

// TestTheProseScannerIsCapableOfFailing guards the regression test
// above from becoming vacuous through an over-tight pattern.
func TestTheProseScannerIsCapableOfFailing(t *testing.T) {
	sample := "the invariant is held by TestSomethingThatWasRenamedAway in the suite"
	found := testNameInProse.FindAllString(sample, -1)
	if len(found) != 1 || found[0] != "TestSomethingThatWasRenamedAway" {
		t.Fatalf("the prose scanner extracted %v; it would miss a real citation", found)
	}
	// And it must not match ordinary prose that merely starts with
	// "Test", or every document would be full of false citations.
	if testNameInProse.MatchString("Testing is not the same as proving.") {
		t.Fatal("the prose scanner matches ordinary English; it would report false citations")
	}
}
