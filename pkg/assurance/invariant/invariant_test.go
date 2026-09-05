package invariant

import (
	"strings"
	"testing"
	"time"

	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

var iat = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

const impl contract.ID = "veriqo-engineering"

func internal(class state.Class) state.Evidence {
	return state.Evidence{
		ID: "ae:int-" + contract.ID(class), Class: class, Summary: "our own tests",
		Scope: "the package", At: iat, ArtefactHash: "h",
		Validator: state.Validator{ID: impl, Name: "VERIQO engineering"},
	}
}

func external(class state.Class) state.Evidence {
	e := state.Evidence{
		ID: "ae:ext-" + contract.ID(class), Class: class,
		Summary: "an assessment", Scope: "the package", At: iat, ArtefactHash: "h",
		Validator: state.Validator{ID: "assessor:acme", Name: "Acme Security",
			External: true, AttestedBy: "human:procurement"},
	}
	if class == state.ProductionRequired {
		e.Period = "90 days"
	}
	return e
}

func emission(s state.State) Emission {
	return Emission{Surface: Report, Subject: "pkg/ledger", Claimed: s, At: iat}
}

// TestAnOverClaimIsCorrectedRatherThanRefused.
//
// A surface that received an error would have to decide what to do,
// and some of them would log it and publish the claim anyway. The
// over-claim must be impossible, not merely reported.
func TestAnOverClaimIsCorrectedRatherThanRefused(t *testing.T) {
	r, err := Emit(emission(state.ProductionQualified), impl,
		[]state.Evidence{internal(state.Internal)})
	if err != nil {
		t.Fatalf("Emit returned an error rather than a corrected value: %v", err)
	}
	if r.Verdict != ClaimInvalid {
		t.Fatalf("verdict = %s", r.Verdict)
	}
	if r.Emitted != state.InternallyAssured {
		t.Fatalf("emitted %s on internal evidence", r.Emitted)
	}
	if r.Sound() {
		t.Fatal("an invalid claim reported sound")
	}
	if !strings.Contains(r.String(), "QUALIFICATION_CLAIM_INVALID") {
		t.Fatalf("the rendering does not name the verdict: %s", r)
	}
}

// TestNoInternalEvidenceReachesPastInternallyAssured. The cap is Law
// 11 from the evidence's side: internal evidence of any quantity is
// still internal.
func TestNoInternalEvidenceReachesPastInternallyAssured(t *testing.T) {
	var many []state.Evidence
	for i := 0; i < 50; i++ {
		e := internal(state.Internal)
		e.ID = contract.ID("ae:int-" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
		many = append(many, e)
	}
	// Even with evidence records CLAIMING every higher class, an
	// internal validator caps the derivation.
	for _, c := range []state.Class{state.ExternalRequired, state.ProductionRequired,
		state.ReleaseAuthorityRequired} {
		e := internal(c)
		e.Class = c
		// state.Evidence.Validate refuses an internal validator on an
		// external class, which is the first line of defence.
		if err := e.Validate(); err == nil {
			t.Fatalf("internal evidence validated as class %s", c)
		}
	}
	derived, _, err := Derive(impl, many)
	if err != nil {
		t.Fatal(err)
	}
	if derived > state.InternallyAssured {
		t.Fatalf("fifty pieces of internal evidence derived %s", derived)
	}
}

// TestAClaimBelowTheEvidenceIsNotPromoted.
//
// A party is entitled to claim less than it could. Quietly upgrading
// them would make this package the thing it exists to prevent.
func TestAClaimBelowTheEvidenceIsNotPromoted(t *testing.T) {
	ev := []state.Evidence{internal(state.Internal), external(state.ExternalRequired)}
	r, err := Emit(emission(state.Implemented), impl, ev)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Understated {
		t.Fatalf("verdict = %s", r.Verdict)
	}
	if r.Emitted != state.Implemented {
		t.Fatalf("a modest claim was promoted to %s", r.Emitted)
	}
	if !r.Sound() {
		t.Fatal("an understated claim was treated as unsound")
	}
	if !strings.Contains(r.Reason, "may claim less than it could") {
		t.Fatalf("the reason does not explain the refusal to promote: %s", r.Reason)
	}
}

// TestNoEvidenceIsUnassessableNotInvalid. "We cannot tell" and "you
// are wrong" are different answers, and conflating them would make a
// surface with a missing file look like a liar.
func TestNoEvidenceIsUnassessableNotInvalid(t *testing.T) {
	r, err := Emit(emission(state.InternallyAssured), impl, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Unassessable {
		t.Fatalf("verdict = %s", r.Verdict)
	}
	if r.Emitted != state.Undefined {
		t.Fatalf("emitted %s with no evidence", r.Emitted)
	}
	if !strings.Contains(r.Reason, "different answers") {
		t.Fatalf("the reason does not distinguish the two: %s", r.Reason)
	}
}

// TestExternalEvidenceLiftsTheCap, so the test above is not passing by
// refusing everything.
func TestExternalEvidenceLiftsTheCap(t *testing.T) {
	ev := []state.Evidence{
		internal(state.Internal),
		external(state.ExternalRequired),
	}
	derived, cited, err := Derive(impl, ev)
	if err != nil {
		t.Fatal(err)
	}
	if derived != state.ExternallyValidated {
		t.Fatalf("derived %s from internal + external evidence", derived)
	}
	if len(cited) != 2 {
		t.Fatalf("%d evidence items cited", len(cited))
	}
	// And production evidence carries it further.
	ev = append(ev, external(state.ProductionRequired))
	derived, _, _ = Derive(impl, ev)
	if derived != state.OperationallyProven {
		t.Fatalf("derived %s with production evidence", derived)
	}
}

// TestEvidenceFromTheImplementerDoesNotLiftTheCapHoweverLabelled.
func TestEvidenceFromTheImplementerDoesNotLiftTheCapHoweverLabelled(t *testing.T) {
	self := external(state.ExternalRequired)
	self.Validator = state.Validator{ID: impl, Name: "VERIQO engineering",
		External: true, AttestedBy: "human:procurement"}
	derived, _, err := Derive(impl, []state.Evidence{internal(state.Internal), self})
	if err != nil {
		t.Fatal(err)
	}
	if derived > state.InternallyAssured {
		t.Fatalf("the implementer lifted its own cap to %s", derived)
	}
}

// TestEverySurfaceIsGovernedAndNewOnesAreDeliberate.
func TestEverySurfaceIsGovernedAndNewOnesAreDeliberate(t *testing.T) {
	if len(Surfaces()) < 10 {
		t.Fatalf("only %d surfaces are enumerated; a surface not on this list is one the "+
			"invariant does not reach", len(Surfaces()))
	}
	for _, s := range Surfaces() {
		e := Emission{Surface: s, Subject: "x", Claimed: state.ProductionQualified, At: iat}
		r, err := Emit(e, impl, []state.Evidence{internal(state.Internal)})
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if r.Emitted > state.InternallyAssured {
			t.Fatalf("%s emitted %s", s, r.Emitted)
		}
	}
	// A surface nobody added to the list is refused, so the addition
	// is a reviewed change rather than a typed string.
	bad := Emission{Surface: "MARKETING_SITE", Subject: "x",
		Claimed: state.Implemented, At: iat}
	if _, err := Emit(bad, impl, []state.Evidence{internal(state.Internal)}); err == nil {
		t.Fatal("an unenumerated surface emitted a state")
	}
}

// TestGuardBreaksTheBuildRatherThanCorrectingIt. In CI there is nobody
// reading the output who would notice a correction.
func TestGuardBreaksTheBuildRatherThanCorrectingIt(t *testing.T) {
	ev := []state.Evidence{internal(state.Internal)}
	e := Emission{Surface: ContinuousIntegration, Subject: "release",
		Claimed: state.ProductionQualified, At: iat}
	if err := Guard(e, impl, ev); err == nil {
		t.Fatal("Guard permitted an over-claim")
	}
	e.Claimed = state.InternallyAssured
	if err := Guard(e, impl, ev); err != nil {
		t.Fatalf("Guard refused a sound claim: %v", err)
	}
}

// TestTheLedgerMakesRepeatedOverClaimsVisible. One surface
// over-claiming once is a bug; the same surface doing it every release
// is a decision.
func TestTheLedgerMakesRepeatedOverClaimsVisible(t *testing.T) {
	var l Ledger
	ev := []state.Evidence{internal(state.Internal)}
	for i := 0; i < 3; i++ {
		if _, err := l.Emit(Emission{Surface: Export, Subject: "bundle",
			Claimed: state.ExternallyValidated, At: iat}, impl, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.Emit(emission(state.InternallyAssured), impl, ev); err != nil {
		t.Fatal(err)
	}
	if n := len(l.All()); n != 4 {
		t.Fatalf("%d emissions recorded", n)
	}
	if n := len(l.Invalid()); n != 3 {
		t.Fatalf("%d invalid claims", n)
	}
	if l.BySurface()[Export] != 3 {
		t.Fatalf("EXPORT shows %d invalid claims", l.BySurface()[Export])
	}
	rep := l.Report()
	if !strings.Contains(rep, "decided the") || !strings.Contains(rep, "obstacle") {
		t.Fatalf("the report does not distinguish a bug from a process: %s", rep)
	}
}

// TestAnEmissionMustNameItsSubject. A state about nothing is a state
// nobody can check.
func TestAnEmissionMustNameItsSubject(t *testing.T) {
	e := Emission{Surface: Report, Claimed: state.Implemented, At: iat}
	if _, err := Emit(e, impl, []state.Evidence{internal(state.Internal)}); err == nil {
		t.Fatal("an emission with no subject was accepted")
	}
}
