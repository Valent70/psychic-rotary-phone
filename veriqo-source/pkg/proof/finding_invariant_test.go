package proof

import (
	"errors"
	"testing"

	"veriqo/pkg/canonical/jcs"
)

// The review asked for a harder invariant than "the constructor is
// unexported". Every Finding must carry exactly one proof object,
// exactly one case, exactly one authority path, and lineage that cannot
// be edited after the fact. These tests hold each of those four.

// sufficientObject returns a sealed, sufficient object built from the
// package's shared fixture.
func sufficientObject(t *testing.T) Object {
	t.Helper()
	o, err := Seal(good(t))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if o.Sufficiency != Sufficient {
		t.Fatalf("the fixture must be sufficient, got %s", o.Sufficiency)
	}
	return o
}

// sealedBypassingValidation produces an object that carries a valid
// canonical hash but skipped Seal's validation.
//
// This models the only way the case and proposition checks in
// NewFinding can be reached: an object that did not come from Seal.
// Seal itself refuses a missing scope or proposition, so through the
// normal path those checks are unreachable -- they are the second line,
// for an object deserialized from a snapshot or reconstructed by a
// future caller, where Seal's refusal did not travel with the bytes.
// Testing them through a hand-sealed object is the honest way to show
// they do something, rather than asserting an unreachable branch.
func sealedBypassingValidation(t *testing.T, breaks func(*Object)) Object {
	t.Helper()
	o := good(t)
	breaks(&o)
	o.Stance = o.deriveStance()
	o.Sufficiency = o.deriveSufficiency(o.Stance)
	o.CanonicalHash = ""
	o.Signature = ""
	h, err := jcs.Hash(o.hashableView())
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	o.CanonicalHash = h
	if err := VerifyHash(o); err != nil {
		t.Fatalf("the hand-sealed object must still verify: %v", err)
	}
	if o.Sufficiency != Sufficient {
		t.Fatalf("the hand-sealed object must be sufficient, got %s", o.Sufficiency)
	}
	return o
}

// TestAFindingNamesExactlyOneProofOneCaseOneAuthority is the positive
// statement of the invariant.
func TestAFindingNamesExactlyOneProofOneCaseOneAuthority(t *testing.T) {
	o := sufficientObject(t)
	f, err := NewFinding(o, 7)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	if f.ProofHash() != o.CanonicalHash {
		t.Fatalf("finding names proof %q, object is %q", f.ProofHash(), o.CanonicalHash)
	}
	if f.CaseID() != o.Scope.CaseID {
		t.Fatalf("finding names case %q, object scope is %q", f.CaseID(), o.Scope.CaseID)
	}
	if f.AuthorityPath() != FindingAuthorityPath {
		t.Fatalf("authority path is %q, want %q", f.AuthorityPath(), FindingAuthorityPath)
	}
	if err := f.VerifyIntegrity(); err != nil {
		t.Fatalf("a freshly derived finding must verify: %v", err)
	}
}

// TestAFindingWithoutACaseIsRefused. A finding attributable to no case
// could be attached to any case later, which is the same as belonging
// to none.
func TestAFindingWithoutACaseIsRefused(t *testing.T) {
	o := sealedBypassingValidation(t, func(o *Object) { o.Scope.CaseID = "" })
	if _, err := NewFinding(o, 7); !errors.Is(err, ErrFindingWithoutCase) {
		t.Fatalf("want ErrFindingWithoutCase, got %v", err)
	}
}

// TestAFindingWithoutAPropositionIsRefused. A finding that states no
// proposition states nothing.
func TestAFindingWithoutAPropositionIsRefused(t *testing.T) {
	o := sealedBypassingValidation(t, func(o *Object) { o.Proposition.ID = "" })
	if _, err := NewFinding(o, 7); !errors.Is(err, ErrFindingWithoutProposition) {
		t.Fatalf("want ErrFindingWithoutProposition, got %v", err)
	}
}

// TestTheZeroFindingHasNoAuthorityPath proves a struct literal cannot
// impersonate a derived finding. This is the shape a forged finding
// would take if one crossed a process boundary.
func TestTheZeroFindingHasNoAuthorityPath(t *testing.T) {
	var f Finding
	if f.AuthorityPath() != "" {
		t.Fatalf("the zero finding claims an authority path: %q", f.AuthorityPath())
	}
	if err := f.VerifyIntegrity(); !errors.Is(err, ErrZeroFinding) {
		t.Fatalf("want ErrZeroFinding, got %v", err)
	}
}

// TestLineageCannotBeEditedAfterDerivation proves the accessors hand
// back copies. A caller who mutates what Limitations returns has
// mutated their own slice, not the finding.
func TestLineageCannotBeEditedAfterDerivation(t *testing.T) {
	base := good(t)
	base.Limitations = []string{"fixture data", "no external attestation"}
	o, err := Seal(base)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	f, err := NewFinding(o, 7)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	before := len(f.Limitations())
	stolen := f.Limitations()
	for i := range stolen {
		stolen[i] = "no limitations at all"
	}
	after := f.Limitations()
	if len(after) != before {
		t.Fatalf("limitation count changed from %d to %d", before, len(after))
	}
	for _, l := range after {
		if l == "no limitations at all" {
			t.Fatal("a caller edited the finding's limitations through the accessor")
		}
	}
	if err := f.VerifyIntegrity(); err != nil {
		t.Fatalf("the finding must still verify after an attempted edit: %v", err)
	}
}

// TestATamperedFindingFailsIntegrity is the check that matters for a
// finding that crossed a boundary. Inside the package the fields are
// reachable, which is exactly how a deserializer would reach them.
func TestATamperedFindingFailsIntegrity(t *testing.T) {
	o := sufficientObject(t)
	f, err := NewFinding(o, 7)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	for _, tc := range []struct {
		name   string
		breaks func(*Finding)
		want   error
	}{
		{name: "the stance is flipped", breaks: func(f *Finding) { f.stance = Contradict }, want: ErrFindingTampered},
		{name: "the statement is rewritten", breaks: func(f *Finding) { f.statement = "something else entirely" }, want: ErrFindingTampered},
		{name: "the case is swapped", breaks: func(f *Finding) { f.caseID = "SOME-OTHER-CASE" }, want: ErrFindingTampered},
		{name: "the qualification is upgraded", breaks: func(f *Finding) { f.qualification = "QUALIFIED" }, want: ErrFindingTampered},
		{name: "the authority path is forged", breaks: func(f *Finding) { f.authorityPath = "caseproofgraph.BuildFinding" }, want: ErrFindingAuthorityPath},
		{name: "the case is erased", breaks: func(f *Finding) { f.caseID = "" }, want: ErrFindingWithoutCase},
		{name: "the proof link is erased", breaks: func(f *Finding) { f.proofHash = "" }, want: ErrFindingWithoutProof},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered := f
			tc.breaks(&tampered)
			err := tampered.VerifyIntegrity()
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

// TestTheAuthorityPathIsCoveredByTheHash proves the path is not
// decoration. If it were outside the hash, a forged path would survive
// integrity verification.
func TestTheAuthorityPathIsCoveredByTheHash(t *testing.T) {
	o := sufficientObject(t)
	f, err := NewFinding(o, 7)
	if err != nil {
		t.Fatalf("NewFinding: %v", err)
	}
	forged := f
	forged.authorityPath = "caseproofgraph.BuildFinding"
	h, err := findingHash(forged)
	if err != nil {
		t.Fatalf("findingHash: %v", err)
	}
	if h == f.hash {
		t.Fatal("changing the authority path did not change the hash: the path is outside the lineage")
	}
}
