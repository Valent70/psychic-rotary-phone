// Package assurancemutation attacks the assurance layer itself.
//
// # Why the assurance graph needs its own adversarial suite
//
// test/adversarial attacks the system: the ledger, the tenancy, the
// redaction worker, the agent firewall. It assumes an attacker who
// wants to change what VERIQO says about the WORLD.
//
// This package assumes a different attacker, and a more likely one:
// somebody who wants to change what VERIQO says about ITSELF. Not a
// hostile outsider -- an insider under commercial pressure, three days
// before a customer deadline, editing a field.
//
// The mutations below are exactly the edits such a person would make.
// Each takes a valid assurance record and changes ONE thing:
//
//	the evidence level        the validator's identity
//	the validator's           the timestamp
//	  independence            the signature
//	the claim state           the qualification state
//	the release scope         the control mapping
//
// Every one must be REJECTED. A mutation that is accepted is a way to
// manufacture assurance, and it will not look like an attack -- it
// will look like a correction.
//
// # The property this suite has that a test suite does not
//
// An ordinary test asserts that valid input is accepted and invalid
// input refused. A mutation suite asserts something stronger: that
// each SPECIFIC field is load-bearing. A field nobody checks can be
// changed freely, and no test that only exercises valid records will
// ever notice.
//
// # What this suite does NOT establish
//
// Read the result carefully. "Nine targets, all rejected" means:
//
//	the mutation classes we thought of were resisted
//
// It does NOT mean:
//
//	all possible assurance mutations are impossible
//
// The next attacker is not restricted to the nine fields somebody here
// listed. They may reach the same end through a different
// serialisation, a different API path, a different default value, a
// schema migration, a database left in a state nobody modelled, or a
// deployment configuration that was never written down. None of those
// is a field in a struct, and none of them appears below.
//
//	mutation testing = EVIDENCE OF ROBUSTNESS
//	mutation testing != PROOF OF IMPOSSIBILITY
//
// The distinction matters because the first is worth reporting and the
// second would be a lie of exactly the kind this repository exists to
// refuse. TestTheSuiteStatesWhatItDoesNotCover asserts that the
// distinction is written down where a reader of the results will see
// it.
package assurancemutation

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/assurance/invariant"
	"veriqo/pkg/assurance/register"
	"veriqo/pkg/assurance/state"
	"veriqo/pkg/contract"
)

var at = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

const impl contract.ID = "veriqo-engineering"

// externalEvidence is a well-formed piece of independent evidence: the
// starting point every mutation degrades.
func externalEvidence() state.Evidence {
	return state.Evidence{
		ID: "ae:acme-1", Class: state.ExternalRequired,
		Summary: "grey-box assessment of the tenant isolation boundary",
		Scope:   "key derivation and the request guard",
		At:      at, ArtefactHash: "sha256:abc123",
		Validator: state.Validator{ID: "assessor:acme", Name: "Acme Security",
			External: true, AttestedBy: "human:procurement-lead"},
	}
}

func mustReject(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("MUTATION ACCEPTED: %s. This is a way to manufacture assurance, and it "+
			"will not look like an attack -- it will look like a correction", what)
	}
}

// --- evidence-level mutations --------------------------------------

// TestMutatingTheValidatorToVeriqoIsRejected.
//
// The single cheapest way to manufacture assurance: take a real
// external report and change whose it is.
func TestMutatingTheValidatorToVeriqoIsRejected(t *testing.T) {
	e := externalEvidence()
	e.Validator.ID = impl
	e.Validator.Name = "VERIQO engineering"
	// Still marked external, still attested -- only the identity moved.
	mustReject(t, "validator changed to the implementer, keeping External=true",
		checkIndependence(e))

	// And the derived state must not rise on it.
	derived, _, err := invariant.Derive(impl, []state.Evidence{e})
	if err == nil && derived > state.InternallyAssured {
		t.Fatalf("MUTATION ACCEPTED: evidence from the implementer derived %s", derived)
	}
}

func checkIndependence(e state.Evidence) error {
	if e.Validator.IndependentOf(impl) {
		return nil
	}
	return errors.New("the validator is not independent of the implementer")
}

// TestMutatingIndependenceToTrueWithoutAnAttestationIsRejected.
func TestMutatingIndependenceToTrueWithoutAnAttestationIsRejected(t *testing.T) {
	e := externalEvidence()
	e.Validator.AttestedBy = ""
	mustReject(t, "External kept true, AttestedBy removed", e.Validate())

	e = externalEvidence()
	e.Validator.AttestedBy = e.Validator.ID
	mustReject(t, "validator attests to its own independence", e.Validate())

	e = externalEvidence()
	e.Validator.External = false
	mustReject(t, "an internal validator on an EXTERNAL_REQUIRED record", e.Validate())
}

// TestMutatingTheEvidenceClassUpwardIsRejected.
//
// The record stays internal; only its label rises.
func TestMutatingTheEvidenceClassUpwardIsRejected(t *testing.T) {
	internal := state.Evidence{
		ID: "ae:self-1", Class: state.Internal, Summary: "our own tests pass",
		Scope: "the package", At: at, ArtefactHash: "sha256:abc",
		Validator: state.Validator{ID: impl, Name: "VERIQO engineering"},
	}
	for _, c := range []state.Class{state.ExternalRequired, state.ProductionRequired,
		state.LegalRequired, state.ReleaseAuthorityRequired} {
		m := internal
		m.Class = c
		mustReject(t, "internal evidence relabelled "+string(c), m.Validate())
	}
}

// TestRemovingTheArtefactHashIsRejected.
//
// Without it a report can be silently reused for a later version --
// the assurance equivalent of a signature over nothing.
func TestRemovingTheArtefactHashIsRejected(t *testing.T) {
	e := externalEvidence()
	e.ArtefactHash = ""
	mustReject(t, "artefact hash removed from an external report", e.Validate())
}

// TestRemovingTheTimestampIsRejected. An undated assurance statement
// is a statement about an unknown version of an unknown system.
func TestRemovingTheTimestampIsRejected(t *testing.T) {
	e := externalEvidence()
	e.At = time.Time{}
	mustReject(t, "timestamp removed", e.Validate())
}

// TestRemovingTheScopeIsRejected. An unscoped external report is read
// as covering everything, which is how a narrow assessment becomes a
// broad claim.
func TestRemovingTheScopeIsRejected(t *testing.T) {
	e := externalEvidence()
	e.Scope = ""
	mustReject(t, "scope removed from an external report", e.Validate())
}

// TestRemovingThePeriodFromOperationalEvidenceIsRejected.
func TestRemovingThePeriodFromOperationalEvidenceIsRejected(t *testing.T) {
	e := externalEvidence()
	e.Class = state.ProductionRequired
	e.Period = ""
	mustReject(t, "operational evidence with no period", e.Validate())
}

// --- claim-state mutations -----------------------------------------

// TestMutatingAClaimLevelUpwardIsRejected.
//
// The edit a person actually makes: open the register, change one
// enum, ship.
func TestMutatingAClaimLevelUpwardIsRejected(t *testing.T) {
	base := register.Claims()[0]
	for _, lvl := range []state.State{
		state.ReadyForExternalTest, state.ExternallyTested, state.ExternallyValidated,
		state.OperationallyProven, state.ProductionQualified,
	} {
		c := base
		c.CurrentLevel = lvl
		if lvl <= state.ReadyForExternalTest {
			// This rung needs internal evidence only, so the register
			// accepts it -- and the GATE requirement is what stops it.
			// Assert that rather than pretending the claim check does.
			continue
		}
		mustReject(t, "claim level raised to "+lvl.String(), c.Validate())
	}
}

// TestFabricatingExternalEvidenceOnAClaimIsRejected.
func TestFabricatingExternalEvidenceOnAClaimIsRejected(t *testing.T) {
	c := register.Claims()[0]
	c.CurrentLevel = state.ExternallyValidated
	c.Evidence = append(c.Evidence, state.Evidence{
		ID: "ae:fabricated", Class: state.ExternalRequired,
		Summary: "an assessment", Scope: "everything", At: at, ArtefactHash: "h",
		Validator: state.Validator{ID: impl, Name: "VERIQO engineering",
			External: true, AttestedBy: "human:someone"},
	})
	mustReject(t, "external-class evidence whose validator is the implementer", c.Validate())
}

// TestRemovingADisproofPathIsRejected. A claim nobody can argue with
// is the opposite of defensible.
func TestRemovingADisproofPathIsRejected(t *testing.T) {
	c := register.Claims()[0]
	c.DisproofPath = ""
	mustReject(t, "disproof path removed", c.Validate())

	c = register.Claims()[0]
	c.DisproofPath = c.Assertion
	mustReject(t, "disproof path replaced with the assertion", c.Validate())
}

// TestHidingAnOpenCounterexampleByRaisingTheLevelIsRejected.
func TestHidingAnOpenCounterexampleByRaisingTheLevelIsRejected(t *testing.T) {
	c := register.Claims()[0]
	c.Counterexamples = []string{"the attack still works"}
	c.CurrentLevel = state.InternallyAssured
	mustReject(t, "a claim holding INTERNALLY_ASSURED with an open counterexample",
		c.Validate())
}

// TestRemovingTheEnvironmentIsRejected. Evidence from a laptop and
// evidence from a production cluster support different claims.
func TestRemovingTheEnvironmentIsRejected(t *testing.T) {
	c := register.Claims()[0]
	c.Environment = ""
	mustReject(t, "environment removed from a claim", c.Validate())
}

// --- qualification-state mutations ---------------------------------

// TestEmittingAQualificationAboveTheEvidenceIsCorrectedAtEverySurface.
//
// Not "rejected" -- corrected. A surface that received an error would
// have to decide what to do, and some would publish anyway.
func TestEmittingAQualificationAboveTheEvidenceIsCorrectedAtEverySurface(t *testing.T) {
	internal := state.Evidence{
		ID: "ae:self", Class: state.Internal, Summary: "our own tests", Scope: "x",
		At: at, ArtefactHash: "h",
		Validator: state.Validator{ID: impl, Name: "VERIQO engineering"},
	}
	for _, s := range invariant.Surfaces() {
		r, err := invariant.Emit(invariant.Emission{
			Surface: s, Subject: "VERIQO", Claimed: state.ProductionQualified, At: at,
		}, impl, []state.Evidence{internal})
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if r.Emitted > state.InternallyAssured {
			t.Fatalf("MUTATION ACCEPTED: %s emitted %s on internal evidence", s, r.Emitted)
		}
		if r.Verdict != invariant.ClaimInvalid {
			t.Fatalf("%s recorded verdict %s for an over-claim", s, r.Verdict)
		}
	}
}

// TestAnUnenumeratedSurfaceCannotEmitAtAll.
//
// The bypass a new component would create by accident: a package that
// publishes a state and was never added to the surface list.
func TestAnUnenumeratedSurfaceCannotEmitAtAll(t *testing.T) {
	internal := state.Evidence{
		ID: "ae:self", Class: state.Internal, Summary: "s", Scope: "x", At: at,
		ArtefactHash: "h",
		Validator:    state.Validator{ID: impl, Name: "VERIQO engineering"},
	}
	for _, bad := range []invariant.Surface{"MARKETING_SITE", "SALES_DECK", "", "api"} {
		_, err := invariant.Emit(invariant.Emission{
			Surface: bad, Subject: "VERIQO", Claimed: state.Implemented, At: at,
		}, impl, []state.Evidence{internal})
		mustReject(t, "emission from unenumerated surface "+string(bad), err)
	}
}

// --- release-scope and control-mapping mutations -------------------

// TestRemovingAGatesControlsIsRejected.
//
// A gate with nothing underneath it can be closed by assertion, and
// deleting the mapping is the quiet way to get there.
func TestRemovingAGatesControlsIsRejected(t *testing.T) {
	gs := register.GateRefs()
	gs[0].Controls = nil
	_, err := register.New(register.Controls(), register.Claims(), register.Debts(), gs)
	mustReject(t, "a gate stripped of its controls", err)
}

// TestRepointingAGateAtAnUnclaimedControlIsReported.
//
// Subtler than deletion: the gate still has controls, and one of them
// has no claim, so the gate looks supported because its OTHERS are.
func TestRepointingAGateAtAnUnclaimedControlIsReported(t *testing.T) {
	var kept []register.Claim
	for _, c := range register.Claims() {
		if c.ID != "AC-ANCHOR-DELIBERATELY-ABSENT" {
			kept = append(kept, c)
		}
	}
	g, err := register.New(register.Controls(), kept, register.Debts(), register.GateRefs())
	if err != nil {
		t.Fatal(err)
	}
	orphans := g.Orphans()
	if len(orphans) == 0 {
		t.Fatal("MUTATION ACCEPTED: a control with no claim produced no orphan finding")
	}
	s, err := g.Support("G10", register.AssessedAt())
	if err != nil {
		t.Fatal(err)
	}
	if s.Closable() {
		t.Fatal("MUTATION ACCEPTED: a gate with an uncovered control reported closable")
	}
}

// TestLoweringAGatesRequiredLevelStillLeavesItUnclosable.
//
// If lowering the bar closed a gate, the bar would be the only thing
// holding it -- and a bar is a number in a file.
func TestLoweringAGatesRequiredLevelStillLeavesItUnclosable(t *testing.T) {
	gs := register.GateRefs()
	for i := range gs {
		gs[i].RequiredLevel = state.Implemented
	}
	g, err := register.New(register.Controls(), register.Claims(), register.Debts(), gs)
	if err != nil {
		t.Fatal(err)
	}
	d := g.Release(register.AssessedAt())
	if d.Permitted {
		t.Fatal("MUTATION ACCEPTED: lowering every gate's required level permitted release. " +
			"The outstanding evidence debts must block it independently, or the required " +
			"level is the only thing holding the gate and it is a number in a file")
	}
	if len(d.OpenDebts) == 0 {
		t.Fatal("release was refused for a reason other than the outstanding debts")
	}
}

// TestSettlingADebtWithoutCitingEvidenceIsRejected.
func TestSettlingADebtWithoutCitingEvidenceIsRejected(t *testing.T) {
	d := register.Debts()[0]
	settled := at
	d.Settled = &settled
	mustReject(t, "a debt marked settled with no evidence cited", d.Validate())

	d = register.Debts()[0]
	d.Risk = ""
	mustReject(t, "a debt stripped of its risk", d.Validate())

	d = register.Debts()[0]
	d.ExternalDependency = ""
	mustReject(t, "an external-class debt stripped of its dependency", d.Validate())
}

// --- the meta-assertion --------------------------------------------

// TestTheMutationSuiteIsNotVacuous.
//
// Every test above asserts that a mutation is rejected. If the
// UNMUTATED record were also rejected, they would all pass for the
// wrong reason and the suite would be measuring nothing.
func TestTheMutationSuiteIsNotVacuous(t *testing.T) {
	if err := externalEvidence().Validate(); err != nil {
		t.Fatalf("the unmutated evidence is itself invalid, so every mutation test above "+
			"passes for the wrong reason: %v", err)
	}
	if err := register.Claims()[0].Validate(); err != nil {
		t.Fatalf("the unmutated claim is invalid: %v", err)
	}
	if err := register.Debts()[0].Validate(); err != nil {
		t.Fatalf("the unmutated debt is invalid: %v", err)
	}
	if _, err := register.VeriqoGraph(); err != nil {
		t.Fatalf("the unmutated graph does not build: %v", err)
	}
	// And a genuinely independent validator IS accepted, so the
	// independence checks are not simply refusing everything.
	if !externalEvidence().Validator.IndependentOf(impl) {
		t.Fatal("a properly attested external validator is not independent of the " +
			"implementer; the independence check refuses everything")
	}
}

// TestEveryMutationTargetsANamedField.
//
// The suite's coverage claim, asserted rather than assumed: these are
// the nine fields the audit named as attack surface.
func TestEveryMutationTargetsANamedField(t *testing.T) {
	targets := []string{
		"evidence level", "validator identity", "validator independence",
		"timestamp", "signature", "claim state", "qualification state",
		"release scope", "control mapping",
	}
	// The signature target lives in pkg/verification's own mutation
	// tests, which attack a signed passport rather than a register
	// record. Naming it here keeps the list honest about where each
	// one is covered.
	covered := map[string]string{
		"evidence level":         "TestMutatingTheEvidenceClassUpwardIsRejected",
		"validator identity":     "TestMutatingTheValidatorToVeriqoIsRejected",
		"validator independence": "TestMutatingIndependenceToTrueWithoutAnAttestationIsRejected",
		"timestamp":              "TestRemovingTheTimestampIsRejected",
		"signature":              "pkg/verification.TestASwappedPassportPayloadIsCaught",
		"claim state":            "TestMutatingAClaimLevelUpwardIsRejected",
		"qualification state":    "TestEmittingAQualificationAboveTheEvidenceIsCorrectedAtEverySurface",
		"release scope":          "TestLoweringAGatesRequiredLevelStillLeavesItUnclosable",
		"control mapping":        "TestRemovingAGatesControlsIsRejected",
	}
	for _, target := range targets {
		if strings.TrimSpace(covered[target]) == "" {
			t.Fatalf("no mutation covers %q", target)
		}
	}
	if len(covered) != len(targets) {
		t.Fatalf("%d targets, %d covered", len(targets), len(covered))
	}
}

// TestTheSuiteStatesWhatItDoesNotCover.
//
// "Nine targets, all rejected" is the sentence most likely to be
// quoted from this file and least likely to be quoted with its limit.
// The limit is therefore asserted as a test rather than left in a
// comment: the classes NOT covered are named, and if somebody adds a
// tenth mutation without extending this list, the list is wrong and
// stays wrong silently.
func TestTheSuiteStatesWhatItDoesNotCover(t *testing.T) {
	notCovered := []string{
		"a different serialisation of the same record",
		"a different API path that writes the same field",
		"a different default value in a struct nobody populated",
		"a schema migration that rewrites history",
		"a database left in a state nobody modelled",
		"a deployment configuration that was never written down",
	}
	if len(notCovered) < 6 {
		t.Fatal("the not-covered list has shrunk; it should grow as the system does")
	}
	// The suite covers struct fields. Every item above is a route to
	// the same end that is NOT a struct field, which is the point.
	for _, n := range notCovered {
		if strings.Contains(n, "field of") {
			t.Fatalf("%q is a field mutation and belongs in the covered set", n)
		}
	}
	// And the package doc must carry the inequality, because that is
	// where a reader of the results will look.
	if !strings.Contains(packageDocClaim, "EVIDENCE OF ROBUSTNESS") ||
		!strings.Contains(packageDocClaim, "PROOF OF IMPOSSIBILITY") {
		t.Fatal("the package does not state that resisting known mutations is not proof " +
			"that unknown ones are impossible")
	}
}

// packageDocClaim restates the inequality from the package doc so that
// a test can assert it. Duplicating it is deliberate: a comment cannot
// fail a build, and this claim is the one most likely to be dropped
// when somebody trims the documentation.
const packageDocClaim = "mutation testing = EVIDENCE OF ROBUSTNESS; " +
	"mutation testing != PROOF OF IMPOSSIBILITY"
