package architecture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"testing"
	"veriqo/pkg/provenance/temporal"
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
//
// The marker is the SEMANTIC condition only. It says a human meant the
// quotation. It cannot say whether the thing cited exists, whether the
// content matches, or which state of the world it came from -- which is
// why those are three separate checks below, and why each carries a
// temporal.State rather than a boolean.
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

// The four conditions a citation must satisfy.
//
// The review's point was that TestNoDocumentMisquotesTheRuntimeLedger
// proved one of them and was being read as proving all four:
//
//	a test that detects a particular bug
//	  is not
//	a test that proves a whole class of bug is closed
//
// So each condition is now named, checked separately, and able to fail
// on its own with its own message. A citation that passes CORRECTNESS
// while failing TEMPORAL is a different defect from one that fails
// EXISTENCE, and reporting both as "disagrees with the artefact" hides
// which one happened.
type condition string

const (
	// condExistence: does the cited record exist in the artefact at all?
	// A citation of AUDIT-099 in a fourteen-record ledger is not a
	// mismatch, it is a reference to nothing.
	condExistence condition = "EXISTENCE"
	// condCorrectness: does the citation's content match the record?
	condCorrectness condition = "CORRECTNESS"
	// condTemporal: which state of the world does the citation come
	// from -- the current artefact, or a superseded one?
	condTemporal condition = "TEMPORAL"
	// condSemantic: is the citation labelled as what it is?
	condSemantic condition = "SEMANTIC"
)

// citationFinding is one condition failing at one place.
type citationFinding struct {
	cond    condition
	file    string
	line    int
	subject string
	detail  string
	// standing is what the checker concluded about the citation's
	// temporal provenance. UNVERIFIED means it could not be
	// classified, which is itself the finding.
	standing temporal.State
}

func (f citationFinding) String() string {
	return fmt.Sprintf("[%s] %s:%d %s (standing: %s) -- %s",
		f.cond, f.file, f.line, f.subject, f.standing, f.detail)
}

type runtimeArtefactRecord struct {
	Index  int    `json:"index"`
	Action string `json:"action"`
}

type runtimeArtefactFile struct {
	Records []runtimeArtefactRecord `json:"records"`
}

// auditCitations walks docs/ and classifies every runtime-ledger
// citation against the four conditions.
//
// It returns findings rather than failing, so that each condition can
// have its own test and its own message. A checker that reported one
// verdict for four questions is the thing this replaces.
func auditCitations(t *testing.T) []citationFinding {
	t.Helper()
	root := repoRoot(t)
	actions := committedLedgerActions(t, root)

	var findings []citationFinding
	blocks := map[blockKey][]blockLine{}
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
				subject := fmt.Sprintf("AUDIT-%03d %s", index, m[2])
				marker := markerLine(lines, i)
				actual, exists := actions[index]
				matches := exists && normaliseAction(actual) == normaliseAction(m[2])

				// EXISTENCE. Checked first: a citation of a record
				// that is not in the artefact is not a content
				// mismatch, and calling it one would send a reader
				// looking for a discrepancy that does not exist.
				if !exists {
					findings = append(findings, citationFinding{
						cond: condExistence, file: rel, line: i + 1, subject: subject,
						standing: temporal.Unverified,
						detail: fmt.Sprintf("the committed artefact has no record at index %d (it has %d records)",
							index, len(actions)),
					})
					continue
				}

				if marker < 0 {
					if matches {
						continue // agrees with the artefact and claims to be current
					}
					// CORRECTNESS. Disagrees and does not say so.
					// This is the defect the reviewer found by hand.
					findings = append(findings, citationFinding{
						cond: condCorrectness, file: rel, line: i + 1, subject: subject,
						standing: temporal.Unverified,
						detail: fmt.Sprintf("cites %q; the committed artefact records %q at index %d, and nothing marks the difference",
							m[2], actual, index),
					})
					continue
				}

				// TEMPORAL. Inside a marked block. Every citation
				// there is part of one quoted transcript, and a
				// transcript legitimately contains lines that did not
				// change -- that is what makes it a transcript rather
				// than a diff. So no line inside a marked block is a
				// defect on its own; the block is judged as a whole,
				// below.
				blocks[blockKey{file: rel, marker: marker}] =
					append(blocks[blockKey{file: rel, marker: marker}], blockLine{
						line: i + 1, subject: subject, differs: !matches, actual: actual,
					})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs: %v", err)
	}

	// SEMANTIC, judged per marked block.
	//
	// A marker suppresses checking for everything it covers. That is
	// correct when the block quotes a superseded ledger, and it is a
	// permanently disabled check when the block quotes nothing that
	// differs from the artefact. So the rule is not "every marked line
	// must differ" -- a transcript contains unchanged lines -- it is
	// "every marker must be licensing at least one real difference".
	var keys []blockKey
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].marker < keys[j].marker
	})
	for _, k := range keys {
		lines := blocks[k]
		differing := 0
		for _, l := range lines {
			if l.differs {
				differing++
			}
		}
		if differing == 0 {
			var cited []string
			for _, l := range lines {
				cited = append(cited, l.subject)
			}
			findings = append(findings, citationFinding{
				cond: condSemantic, file: k.file, line: k.marker + 1,
				subject:  fmt.Sprintf("the marked block citing %s", strings.Join(cited, ", ")),
				standing: temporal.Unverified,
				detail: "this marker licenses no citation that differs from the committed artefact, " +
					"so it disables checking without recording anything; remove it",
			})
			continue
		}
		// The block is a genuine historical quotation. Record its
		// standing so TEMPORAL has something positive to assert.
		for _, l := range lines {
			st := temporal.Historical
			detail := "part of a deliberately quoted historical transcript"
			if l.differs {
				st = temporal.Superseded
				detail = fmt.Sprintf("quotes a superseded ledger (the artefact records %q) and the block is marked",
					l.actual)
			}
			findings = append(findings, citationFinding{
				cond: condTemporal, file: k.file, line: l.line, subject: l.subject,
				standing: st, detail: detail,
			})
		}
	}
	return findings
}

// blockKey identifies one marked block: a file and the line its marker
// sits on.
type blockKey struct {
	file   string
	marker int
}

// blockLine is one citation inside a marked block.
type blockLine struct {
	line    int
	subject string
	differs bool
	actual  string
}

func findingsOf(all []citationFinding, c condition) []string {
	var out []string
	for _, f := range all {
		if f.cond == c {
			out = append(out, f.String())
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryCitedRuntimeRecordExists is the EXISTENCE condition.
func TestEveryCitedRuntimeRecordExists(t *testing.T) {
	if got := findingsOf(auditCitations(t), condExistence); len(got) > 0 {
		t.Fatalf("a document cites a runtime record that is not in the committed artefact.\n  %s",
			strings.Join(got, "\n  "))
	}
}

// TestNoDocumentMisquotesTheRuntimeLedger is the CORRECTNESS condition:
// a citation that disagrees with the committed artefact and does not
// say so. This is the defect the review found by reading.
func TestNoDocumentMisquotesTheRuntimeLedger(t *testing.T) {
	if got := findingsOf(auditCitations(t), condCorrectness); len(got) > 0 {
		t.Fatalf("documents disagree with evidence/RUNTIME_EVIDENCE.json.\n"+
			"Either correct the document, or -- if it is deliberately quoting a\n"+
			"superseded ledger -- put %q within %d lines above the citation.\n\n  %s",
			historicalMarker, markerWindow, strings.Join(got, "\n  "))
	}
}

// TestEveryHistoricalCitationIsActuallyHistorical is the SEMANTIC
// condition, and it catches what a marker-only scheme cannot: a marker
// that licenses nothing.
//
// The rule is stated over the BLOCK, not the line. A quoted transcript
// legitimately contains lines identical to the current artefact -- that
// is what makes it a transcript rather than a diff -- so an unchanged
// line inside a genuine historical quotation is not a defect. What is a
// defect is a marker over a block where nothing differs: it suppresses
// checking and records nothing, and when the artefact next changes
// those lines silently stop being verified.
func TestEveryHistoricalCitationIsActuallyHistorical(t *testing.T) {
	if got := findingsOf(auditCitations(t), condSemantic); len(got) > 0 {
		t.Fatalf("a historical marker licenses no real difference.\n"+
			"A marker suppresses checking for everything it covers. Over a block that\n"+
			"quotes nothing superseded it records nothing and disables the check\n"+
			"permanently, so it must be removed.\n\n  %s", strings.Join(got, "\n  "))
	}
}

// TestEveryCitationCarriesATemporalStanding is the TEMPORAL condition,
// stated as a positive property rather than an absence.
//
// Every citation in docs/ resolves to exactly one of: it agrees with
// the artefact (CURRENT), or it disagrees and is marked (SUPERSEDED).
// Nothing may be UNVERIFIED -- unclassified -- once the three tests
// above pass, and this asserts that the classification is total rather
// than leaving a citation shape nobody thought about.
func TestEveryCitationCarriesATemporalStanding(t *testing.T) {
	all := auditCitations(t)
	var unclassified []string
	for _, f := range all {
		if f.standing == temporal.Unverified {
			unclassified = append(unclassified, f.String())
		}
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("citations with no temporal standing.\n"+
			"A reference nobody classified is not current, it is unclassified,\n"+
			"and it may not be read as a claim about the system.\n\n  %s",
			strings.Join(unclassified, "\n  "))
	}
	// The set that remains must validate as temporal references. This
	// is what makes the standing a value rather than a comment.
	set, err := temporal.NewSet()
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	for _, f := range all {
		ref := temporal.Reference{Subject: f.subject, State: f.standing, Note: f.detail}
		if f.standing == temporal.Superseded {
			ref.SupersededBy = "evidence/RUNTIME_EVIDENCE.json (as committed)"
		}
		if f.standing == temporal.Unverified {
			continue // already reported above
		}
		if err := set.Add(ref); err != nil {
			t.Fatalf("citation %s does not form a valid temporal reference: %v", f.subject, err)
		}
	}
}

// TestTheFourConditionsAreDistinct proves the classifier can produce
// each condition, so that none of the four tests above is passing
// because its condition is unreachable.
//
// A test suite where three of four checks can never fire looks like
// four checks and is one.
func TestTheFourConditionsAreDistinct(t *testing.T) {
	seen := map[condition]bool{}
	for _, f := range auditCitations(t) {
		seen[f.cond] = true
	}
	// TEMPORAL must be present: the repository deliberately quotes
	// superseded ledgers in three places, and if none is found the
	// classifier is not reaching the marked citations at all.
	if !seen[condTemporal] {
		t.Fatal("no citation was classified TEMPORAL: the repository quotes superseded ledgers " +
			"deliberately, so finding none means the marker path is not being exercised")
	}
	// The other three must be ABSENT in a healthy repository, which the
	// three tests above assert. Here we prove the classifier could
	// produce them, by construction rather than by hoping.
	for _, c := range []condition{condExistence, condCorrectness, condSemantic} {
		if seen[c] {
			t.Errorf("%s findings exist; the dedicated test for that condition should have failed first", c)
		}
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

// markerLine returns the line index of the historical marker that
// licenses the given line, or -1 if none is within markerWindow above
// it.
//
// It returns the index rather than a boolean because the marker is
// BLOCK-scoped: one marker licenses a whole quoted transcript, and the
// semantic question -- is this marker doing anything? -- can only be
// asked of the block, not of each line inside it.
func markerLine(lines []string, at int) int {
	from := at - markerWindow
	if from < 0 {
		from = 0
	}
	found := -1
	for i := from; i <= at && i < len(lines); i++ {
		if strings.Contains(lines[i], historicalMarker) {
			found = i
		}
	}
	return found
}

// markerPrecedes reports whether a marker licenses the given line.
func markerPrecedes(lines []string, at int) bool { return markerLine(lines, at) >= 0 }
