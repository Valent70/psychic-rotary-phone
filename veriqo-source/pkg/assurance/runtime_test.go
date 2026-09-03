package assurance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runtimeArtefact mirrors evidence/RUNTIME_EVIDENCE.json.
type runtimeArtefact struct {
	Schema   string `json:"schema"`
	Boundary string `json:"boundary"`
	Records  []struct {
		EventID string `json:"event_id"`
		Action  string `json:"action"`
	} `json:"records"`
}

func loadRuntimeEvidence(t *testing.T) runtimeArtefact {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "evidence", "RUNTIME_EVIDENCE.json"))
	if err != nil {
		t.Fatalf("the runtime evidence artefact must exist: %v", err)
	}
	var a runtimeArtefact
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("RUNTIME_EVIDENCE.json: %v", err)
	}
	if len(a.Records) == 0 {
		t.Fatal("the artefact records no events")
	}
	return a
}

// TestEveryRuntimeEvidenceRefResolves is the check that makes the new
// column worth having: a row claiming runtime evidence must cite a
// record an actual run emitted.
//
// Without this, RUNTIME_EVIDENCE_REF would be a free-text field, which
// is exactly the "spreadsheet of good intentions" failure the rest of
// the matrix is built to avoid.
func TestEveryRuntimeEvidenceRefResolves(t *testing.T) {
	a := loadRuntimeEvidence(t)
	emitted := map[string]bool{}
	for _, r := range a.Records {
		emitted[r.EventID] = true
	}

	cited := 0
	for _, tr := range Matrix() {
		if !tr.RuntimeEvidence {
			continue
		}
		cited++
		// A ref may cite several ids and a trailing explanation. Every
		// AUDIT- token in it must be a record the run emitted.
		for _, tok := range strings.FieldsFunc(tr.RuntimeEvidenceRef, func(r rune) bool {
			return r == ' ' || r == ',' || r == '(' || r == ')'
		}) {
			if !strings.HasPrefix(tok, "AUDIT-") {
				continue
			}
			if !emitted[tok] {
				t.Fatalf("article %d cites runtime evidence %q, which no run emitted", tr.Article, tok)
			}
		}
	}
	if cited == 0 {
		t.Fatal("no article cites runtime evidence: the column governs nothing")
	}
}

// TestRuntimeEvidenceRequiresAReference: an asserted link with no id is
// refused, exactly like every other link in the chain.
func TestRuntimeEvidenceRequiresAReference(t *testing.T) {
	tr := full()
	tr.RuntimeEvidence = true
	tr.RuntimeEvidenceRef = ""
	if _, err := Assess(tr); !errors.Is(err, ErrUnreferenced) {
		t.Fatalf("expected ErrUnreferenced, got %v", err)
	}
}

// TestRuntimeEvidenceWithoutCodeIsImpossible: a record cannot be
// emitted by an implementation that does not exist.
func TestRuntimeEvidenceWithoutCodeIsImpossible(t *testing.T) {
	tr := Trace{
		Article: 1, Control: "c",
		RuntimeEvidence: true, RuntimeEvidenceRef: "AUDIT-001-case.opened",
	}
	if _, err := Assess(tr); !errors.Is(err, ErrImpossibleChain) {
		t.Fatalf("expected ErrImpossibleChain, got %v", err)
	}
}

// TestMissingRuntimeEvidenceIsAnAssuranceGapWithItsOwnReason proves the
// new link is doing work: a control that is tested, recorded and
// replayable but has never actually run still falls short, and Explain
// says which of the four causes it is.
func TestMissingRuntimeEvidenceIsAnAssuranceGap(t *testing.T) {
	tr := full()
	tr.RuntimeEvidence, tr.RuntimeEvidenceRef = false, ""
	tr.Qualification, tr.QualificationRef = false, ""
	tr.ExternalProof, tr.ExternalProofRef = false, ""

	v, err := Assess(tr)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if v != AssuranceGap {
		t.Fatalf("expected ASSURANCE_GAP, got %s", v)
	}
	if !strings.Contains(Explain(tr), "no executed run has produced one") {
		t.Fatalf("the explanation must name the missing runtime record, got %q", Explain(tr))
	}
}

// TestTheArtefactStatesItsOwnBoundary: the run's evidence is a fixture,
// and the artefact must say so rather than let a reader assume the
// records are production records.
func TestTheArtefactStatesItsOwnBoundary(t *testing.T) {
	a := loadRuntimeEvidence(t)
	lower := strings.ToLower(a.Boundary)
	if !strings.Contains(lower, "fixture") {
		t.Fatalf("the artefact must disclose that its evidence is a fixture, got %q", a.Boundary)
	}
	if !strings.Contains(lower, "blocked_external") && !strings.Contains(lower, "live_data") {
		t.Fatalf("the artefact must point at the blocker that remains, got %q", a.Boundary)
	}
}

// TestRuntimeEvidenceCoversTheWholeChain: the run must exercise the
// chain end to end, not just its easy half.
func TestRuntimeEvidenceCoversTheWholeChain(t *testing.T) {
	a := loadRuntimeEvidence(t)
	actions := map[string]bool{}
	for _, r := range a.Records {
		actions[r.Action] = true
	}
	for _, required := range []string{
		"case.opened", "case.scoped", "case.evidence_pinned",
		"case.hypothesis_recorded", "case.hypothesis_tested",
		"case.claim_registered", "case.qualification_begun",
		"case.proof_attached", "case.resolved", "proof.sealed",
	} {
		if !actions[required] {
			t.Fatalf("the executed run emitted no %q event: the chain was not run end to end", required)
		}
	}
}
