package coverage

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// ---- CoverageFact / CoverageQuestion / CoverageConflict validation ----

func TestCoverageFactValidateRejectsEmptyID(t *testing.T) {
	f := CoverageFact{Status: StatusSupported}
	if err := f.Validate(); !errors.Is(err, ErrEmptyFactID) {
		t.Fatalf("expected ErrEmptyFactID, got %v", err)
	}
}

func TestCoverageFactValidateRejectsUnknownStatus(t *testing.T) {
	f := CoverageFact{FactID: "F1", Status: FactStatus("MAYBE")}
	if err := f.Validate(); !errors.Is(err, ErrUnknownFactStatus) {
		t.Fatalf("expected ErrUnknownFactStatus, got %v", err)
	}
}

func TestCoverageFactValidateAcceptsEachKnownStatus(t *testing.T) {
	for _, s := range []FactStatus{StatusSupported, StatusDisputed, StatusPartial, StatusInsufficientEvidence} {
		f := CoverageFact{FactID: "F1", Status: s}
		if err := f.Validate(); err != nil {
			t.Fatalf("status %s: unexpected error: %v", s, err)
		}
	}
}

func TestCoverageQuestionValidateRejectsEmptyFields(t *testing.T) {
	if err := (CoverageQuestion{}).Validate(); !errors.Is(err, ErrEmptyQuestionID) {
		t.Fatalf("expected ErrEmptyQuestionID, got %v", err)
	}
	if err := (CoverageQuestion{QuestionID: "Q1"}).Validate(); !errors.Is(err, ErrEmptyQuestion) {
		t.Fatalf("expected ErrEmptyQuestion, got %v", err)
	}
}

func TestNewCoverageConflictRejectsEmptyID(t *testing.T) {
	if _, err := NewCoverageConflict("", "desc", nil, nil); !errors.Is(err, ErrEmptyConflictID) {
		t.Fatalf("expected ErrEmptyConflictID, got %v", err)
	}
}

// TestNewCoverageConflictIsAlwaysDisputed proves, structurally, that a
// CoverageConflict cannot be constructed with any status other than
// DISPUTED — the blueprint golden rule that a contested tension is
// "never silently resolved either way" is enforced by the
// constructor, not left to caller discipline.
func TestNewCoverageConflictIsAlwaysDisputed(t *testing.T) {
	c, err := NewCoverageConflict("CX-1", "desc", []string{"F1", "F2"}, []string{"§8"})
	if err != nil {
		t.Fatalf("NewCoverageConflict: %v", err)
	}
	if c.Status != StatusDisputed {
		t.Fatalf("expected StatusDisputed, got %s", c.Status)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCoverageConflictValidateRejectsNonDisputedStatus(t *testing.T) {
	c := CoverageConflict{ConflictID: "CX-1", Status: StatusSupported}
	if err := c.Validate(); !errors.Is(err, ErrConflictNotDisputed) {
		t.Fatalf("expected ErrConflictNotDisputed, got %v", err)
	}
}

func TestSortedCopyDoesNotMutateInput(t *testing.T) {
	in := []string{"c", "a", "b"}
	out := sortedCopy(in)
	if in[0] != "c" || in[1] != "a" || in[2] != "b" {
		t.Fatalf("sortedCopy mutated its input: %v", in)
	}
	if out[0] != "a" || out[1] != "b" || out[2] != "c" {
		t.Fatalf("sortedCopy did not sort: %v", out)
	}
}

// ---- Golden Rule #1 (blueprint §36): structurally impossible to
// produce a covered:true/false verdict anywhere in this package's
// public output types. ----
//
// This is checked two ways: (a) by construction — none of the types
// below declare any such field, which is itself a compile-time
// guarantee (code review can see the full field list); and (b) at
// test time via reflection, so a future edit that accidentally adds a
// verdict-shaped field fails CI rather than relying on someone
// noticing in review.
func TestCoverageAnalysisHasNoCoverageVerdictField(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(CoverageAnalysis{}),
		reflect.TypeOf(CoverageFact{}),
		reflect.TypeOf(CoverageQuestion{}),
		reflect.TypeOf(CoverageConflict{}),
	}
	for _, typ := range types {
		assertNoCoverageVerdictField(t, typ)
	}
}

var forbiddenVerdictNames = []string{
	"Covered", "IsCovered", "CoverageDecision", "CoverageVerdict",
	"Approved", "IsApproved", "Denied", "IsDenied", "Rejected", "IsRejected", "Settled",
}

func assertNoCoverageVerdictField(t *testing.T, typ reflect.Type) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		for _, forbidden := range forbiddenVerdictNames {
			if strings.EqualFold(f.Name, forbidden) {
				t.Fatalf("%s carries a field named %q — blueprint §18 forbids producing COVERED=TRUE automatically; this package must never expose a coverage verdict", typ.Name(), f.Name)
			}
		}
		// Any bool field whose name contains "cover" is suspicious
		// regardless of exact spelling — ReviewRequired (bool, no
		// "cover" in its name) is fine and expected; a field like
		// "CoverageOK bool" would not be.
		if f.Type.Kind() == reflect.Bool && strings.Contains(strings.ToLower(f.Name), "cover") {
			t.Fatalf("%s.%s is a bool field whose name contains \"cover\" — this looks like a coverage verdict field, which this package must never expose", typ.Name(), f.Name)
		}
	}
}

// TestCoverageAnalysisFieldSetIsExactlyTheFourBlueprintCategoriesPlusIdentity
// pins CoverageAnalysis's exported field names, so this list is the
// single place a reviewer needs to check that nothing beyond blueprint
// §18's four categories (plus case/claim/policy identity and the
// ReviewRequired marker) has snuck in.
func TestCoverageAnalysisFieldSetIsExactlyTheFourBlueprintCategoriesPlusIdentity(t *testing.T) {
	want := map[string]bool{
		"CaseID": true, "ClaimID": true, "PolicyID": true, "PolicyVersionID": true,
		"Facts": true, "Questions": true, "Conflicts": true,
		"ReviewRequired": true, "ReviewReasons": true,
	}
	typ := reflect.TypeOf(CoverageAnalysis{})
	if typ.NumField() != len(want) {
		t.Fatalf("CoverageAnalysis has %d fields, expected exactly %d: %v", typ.NumField(), len(want), want)
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !want[name] {
			t.Fatalf("CoverageAnalysis has unexpected field %q", name)
		}
	}
}
