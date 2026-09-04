package architecture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This file exists because of a review finding that no test would have
// caught. The committed runtime artefact was corrected so that reverse
// closure precedes qualification and resolution comes last -- and a
// closure report from an earlier round went on describing the old,
// unlawful order. A reader grepping the repository found the stale
// prose first and had no way to tell it from current fact.
//
// The verification the reviewer performed by hand was: does every
// document that quotes the runtime ledger agree with the ledger that is
// actually committed? That is a mechanical question, so it belongs in a
// test rather than in a reviewer's patience.
//
// A superseded round report is a legitimate historical record and must
// not be rewritten to say something its round did not say. So the rule
// is not "every document must match"; it is "every document must match,
// or must say out loud that it is quoting history". The marker below is
// that statement, and it has to sit next to the quotation, where a
// reader looking at the quotation will see it.

// historicalMarker licenses a quotation of a superseded ledger. It must
// appear within markerWindow lines above the citation.
const historicalMarker = "<!-- RUNTIME-LEDGER: historical -->"

// markerWindow is how far above a citation the marker may sit. A fenced
// block plus a sentence of framing fits comfortably; a marker further
// away than this is no longer next to the thing it licenses.
const markerWindow = 8

// auditCitation matches a runtime ledger citation in prose: the record
// index, then whatever names the action on the same line. Both the
// "AUDIT-007-case.qualification_begun" form used by the matrix and the
// "AUDIT-007  qualification_begun" form used in narrative tables are
// matched, because a stale citation is equally misleading in either.
var auditCitation = regexp.MustCompile(`AUDIT-(\d{3})[-\s]+([a-z][a-z0-9._]*)`)

type runtimeArtefactRecord struct {
	Index  int    `json:"index"`
	Action string `json:"action"`
}

type runtimeArtefactFile struct {
	Records []runtimeArtefactRecord `json:"records"`
}

// TestNoDocumentMisquotesTheRuntimeLedger reads every markdown file
// under docs/ and holds each citation of the runtime ledger against the
// ledger that is actually committed.
func TestNoDocumentMisquotesTheRuntimeLedger(t *testing.T) {
	root := repoRoot(t)
	actions := committedLedgerActions(t, root)

	var complaints []string
	docs := filepath.Join(root, "docs")
	err := filepath.Walk(docs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			for _, m := range auditCitation.FindAllStringSubmatch(line, -1) {
				index, convErr := strconv.Atoi(m[1])
				if convErr != nil {
					continue
				}
				cited := normaliseAction(m[2])
				actual, known := actions[index]
				switch {
				case known && normaliseAction(actual) == cited:
					// Agrees with the committed artefact.
				case markerPrecedes(lines, i):
					// Explicitly quoting a superseded ledger.
				case !known:
					complaints = append(complaints, fmt.Sprintf(
						"%s:%d cites AUDIT-%03d, but the committed artefact has no record at that index",
						rel, i+1, index))
				default:
					complaints = append(complaints, fmt.Sprintf(
						"%s:%d cites AUDIT-%03d as %q; the committed artefact records %q at that index",
						rel, i+1, index, m[2], actual))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs: %v", err)
	}

	if len(complaints) > 0 {
		t.Fatalf("documents disagree with evidence/RUNTIME_EVIDENCE.json.\n"+
			"Either correct the document, or -- if it is deliberately quoting a\n"+
			"superseded ledger -- put %q within %d lines above the citation.\n\n  %s",
			historicalMarker, markerWindow, strings.Join(complaints, "\n  "))
	}
}

// TestTheHistoricalMarkerIsActuallyLoadBearing proves the escape hatch
// is narrow. A marker parked at the top of a long document would license
// every stale citation below it, which is the same as having no rule.
func TestTheHistoricalMarkerIsActuallyLoadBearing(t *testing.T) {
	lines := []string{historicalMarker}
	for i := 0; i < markerWindow+2; i++ {
		lines = append(lines, "filler")
	}
	if markerPrecedes(lines, markerWindow+1) {
		t.Fatalf("a marker %d lines above a citation still licenses it: the window does not bind", markerWindow+1)
	}
	if !markerPrecedes(lines, markerWindow) {
		t.Fatalf("a marker exactly %d lines above a citation should license it", markerWindow)
	}
}

// TestAStaleCitationIsRecognisedAsStale is the negative case: it proves
// the comparison would actually have caught the defect that prompted
// this file, rather than passing vacuously.
func TestAStaleCitationIsRecognisedAsStale(t *testing.T) {
	root := repoRoot(t)
	actions := committedLedgerActions(t, root)

	// The superseded artefact recorded case.resolved at index 009. The
	// committed one records it at 013.
	actual, ok := actions[9]
	if !ok {
		t.Fatal("the committed artefact has no record 009")
	}
	if normaliseAction(actual) == normaliseAction("resolved") {
		t.Fatalf("record 009 is still %q: the sequencing fix is not in the committed artefact", actual)
	}
	if got := actions[13]; normaliseAction(got) != "resolved" {
		t.Fatalf("record 013 should be the resolution, got %q", got)
	}
	if got := actions[7]; normaliseAction(got) != "reverse_closed" {
		t.Fatalf("record 007 should be the reverse closure, got %q", got)
	}
}

// committedLedgerActions reads the artefact that is actually in the
// repository. Reading the file rather than re-running the generator is
// the point: a generator that produces a lawful stream while a stale
// file sits in git is exactly the failure this catches.
func committedLedgerActions(t *testing.T, root string) map[int]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "evidence", "RUNTIME_EVIDENCE.json"))
	if err != nil {
		t.Fatalf("reading the committed runtime artefact: %v", err)
	}
	var f runtimeArtefactFile
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("RUNTIME_EVIDENCE.json: %v", err)
	}
	if len(f.Records) == 0 {
		t.Fatal("the committed runtime artefact records nothing")
	}
	actions := make(map[int]string, len(f.Records))
	for _, r := range f.Records {
		actions[r.Index] = r.Action
	}
	return actions
}

// normaliseAction drops the event family prefix so that a document
// writing "qualification_begun" is compared fairly against an artefact
// writing "case.qualification_begun". The family is part of the closed
// taxonomy and is not what a stale citation gets wrong; the verb is.
func normaliseAction(a string) string {
	a = strings.TrimSpace(strings.ToLower(a))
	if i := strings.LastIndex(a, "."); i >= 0 {
		a = a[i+1:]
	}
	return a
}

// markerPrecedes reports whether the historical marker sits within
// markerWindow lines above the given line.
func markerPrecedes(lines []string, at int) bool {
	from := at - markerWindow
	if from < 0 {
		from = 0
	}
	for i := from; i <= at && i < len(lines); i++ {
		if strings.Contains(lines[i], historicalMarker) {
			return true
		}
	}
	return false
}
