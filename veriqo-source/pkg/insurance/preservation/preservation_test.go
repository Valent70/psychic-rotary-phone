package preservation

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
	insevidence "veriqo/pkg/insurance/evidence"
)

func mustRecord(t *testing.T, caseID, subject string, observedAt uint64) insevidence.Record {
	t.Helper()
	ev, err := ontology.New(ontology.Evidence{
		Type:       ontology.TypeDocument,
		Subject:    subject,
		Predicate:  "describes",
		Object:     "cargo_condition",
		Source:     "src-" + subject,
		ObservedAt: observedAt,
		Confidence: 0.9,
		Attributes: map[string]string{"document_hash": "h-" + subject},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	rec, err := insevidence.New(caseID, ev, "PTY-1", insevidence.OriginSurveyor)
	if err != nil {
		t.Fatalf("insevidence.New: %v", err)
	}
	rec.DocumentType = "survey_report"
	return rec
}

func mustOrder(t *testing.T) *Order {
	t.Helper()
	o, err := New("PRES-1", "CASE-INS-002", TriggerIncidentDetected,
		"all reefer, cargo and delivery documentation for container FIC-CU-1",
		"claims operations custodian",
		[]string{"survey_report", "temperature_log", "proof_of_delivery", "photograph"},
		1000, 1500, "claims-officer-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return o
}

// ---- Construction: every §19 field is required -----------------------

func TestNewRequiresEveryStructuralField(t *testing.T) {
	cases := []struct {
		name string
		call func() (*Order, error)
		want error
	}{
		{"no id", func() (*Order, error) {
			return New("", "C", TriggerIncidentDetected, "s", "c", []string{"t"}, 1, 2, "a")
		}, ErrEmptyPreservationID},
		{"no case", func() (*Order, error) {
			return New("P", "", TriggerIncidentDetected, "s", "c", []string{"t"}, 1, 2, "a")
		}, ErrEmptyCaseID},
		{"bad trigger", func() (*Order, error) {
			return New("P", "C", Trigger("VIBES"), "s", "c", []string{"t"}, 1, 2, "a")
		}, ErrUnknownTrigger},
		{"no scope", func() (*Order, error) {
			return New("P", "C", TriggerIncidentDetected, "  ", "c", []string{"t"}, 1, 2, "a")
		}, ErrEmptyScope},
		{"no custodian", func() (*Order, error) {
			return New("P", "C", TriggerIncidentDetected, "s", " ", []string{"t"}, 1, 2, "a")
		}, ErrEmptyCustodian},
		{"no evidence types", func() (*Order, error) {
			return New("P", "C", TriggerIncidentDetected, "s", "c", nil, 1, 2, "a")
		}, ErrNoEvidenceTypes},
		{"no opener", func() (*Order, error) {
			return New("P", "C", TriggerIncidentDetected, "s", "c", []string{"t"}, 1, 2, "")
		}, ErrEmptyActor},
	}
	for _, tc := range cases {
		if _, err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, err)
		}
	}
}

// TestOpeningIsItselfLogged: the §20 workflow starts with the order
// being opened, and that is a custody event like any other.
func TestOpeningIsItselfLogged(t *testing.T) {
	o := mustOrder(t)
	log := o.CustodyLog()
	if len(log) != 1 || log[0].Action != ActionOrderOpened {
		t.Fatalf("the order's own opening must be the first custody event, got %+v", log)
	}
	if log[0].Actor != "claims-officer-1" {
		t.Fatalf("the opening event must name who opened it, got %q", log[0].Actor)
	}
}

// TestRightsDefaultToUnknownPendingContract: preservation says nothing
// about what may be DONE with the evidence.
func TestRightsDefaultToUnknownPendingContract(t *testing.T) {
	o := mustOrder(t)
	if o.Rights() != provenance.RightsUnknownPendingContract {
		t.Fatalf("Rights = %q, want the fail-closed default", o.Rights())
	}
}

// ---- Preservation ----------------------------------------------------

func TestPreserveRefusesAnotherCasesEvidence(t *testing.T) {
	o := mustOrder(t)
	foreign := mustRecord(t, "CASE-SOMEWHERE-ELSE", "s1", 100)
	if err := o.Preserve(foreign, "custodian", 1100); !errors.Is(err, ErrEvidenceNotInCase) {
		t.Fatalf("expected ErrEvidenceNotInCase, got %v", err)
	}
}

func TestPreserveIsIdempotentAndLogged(t *testing.T) {
	o := mustOrder(t)
	rec := mustRecord(t, "CASE-INS-002", "s1", 100)
	if err := o.Preserve(rec, "custodian", 1100); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	if err := o.Preserve(rec, "custodian", 1200); err != nil {
		t.Fatalf("re-preserving the same item must be a no-op, got %v", err)
	}
	if len(o.PreservedIDs()) != 1 {
		t.Fatalf("expected 1 preserved item, got %d", len(o.PreservedIDs()))
	}
	preservedEvents := 0
	for _, e := range o.CustodyLog() {
		if e.Action == ActionItemPreserved {
			preservedEvents++
		}
	}
	if preservedEvents != 1 {
		t.Fatalf("expected exactly one ITEM_PRESERVED event, got %d", preservedEvents)
	}
}

func TestPerItemEventsRequireACoveredItemAndAnActor(t *testing.T) {
	o := mustOrder(t)
	rec := mustRecord(t, "CASE-INS-002", "s1", 100)
	if err := o.Preserve(rec, "custodian", 1100); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	id := rec.EvidenceID()

	if err := o.RecordAccess("not-under-this-order", "reviewer", "", 1200); !errors.Is(err, ErrNotPreserved) {
		t.Fatalf("expected ErrNotPreserved, got %v", err)
	}
	if err := o.RecordAccess(id, "", "", 1200); !errors.Is(err, ErrEmptyActor) {
		t.Fatalf("expected ErrEmptyActor, got %v", err)
	}
	for _, fn := range []func() error{
		func() error { return o.RecordAccess(id, "reviewer", "opened in the workbench", 1200) },
		func() error { return o.RecordExport(id, "reviewer", "included in dossier export", 1300) },
		func() error { return o.RecordCorrection(id, "surveyor", "corrected temperature reading issued", 1400) },
		func() error { return o.RecordSupersession(id, "surveyor", "superseded by revised report", 1450) },
	} {
		if err := fn(); err != nil {
			t.Fatalf("logging a well-formed event: %v", err)
		}
	}
	if len(o.CustodyLog()) != 6 { // opened + preserved + 4
		t.Fatalf("expected 6 custody events, got %d", len(o.CustodyLog()))
	}
}

// ---- Hash: tamper evidence ------------------------------------------

// TestHashChangesWhenThePreservedSetChanges is the integrity property
// this package exists to provide.
func TestHashChangesWhenThePreservedSetChanges(t *testing.T) {
	o := mustOrder(t)
	rec1 := mustRecord(t, "CASE-INS-002", "s1", 100)
	if err := o.Preserve(rec1, "custodian", 1100); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	h1 := o.Hash()

	rec2 := mustRecord(t, "CASE-INS-002", "s2", 200)
	if err := o.Preserve(rec2, "custodian", 1150); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	h2 := o.Hash()
	if h1 == h2 {
		t.Fatal("adding an item to the preserved set must change the order's hash")
	}
	if o.Hash() != h2 {
		t.Fatal("Hash must be deterministic over an unchanged order")
	}
}

// TestHashIsIndependentOfPreservationOrder: two orders covering the same
// set hash identically regardless of the sequence items arrived in.
func TestHashIsIndependentOfPreservationOrder(t *testing.T) {
	recs := []insevidence.Record{
		mustRecord(t, "CASE-INS-002", "s1", 100),
		mustRecord(t, "CASE-INS-002", "s2", 200),
		mustRecord(t, "CASE-INS-002", "s3", 300),
	}
	a := mustOrder(t)
	for _, r := range recs {
		if err := a.Preserve(r, "custodian", 1100); err != nil {
			t.Fatalf("Preserve: %v", err)
		}
	}
	b := mustOrder(t)
	for i := len(recs) - 1; i >= 0; i-- {
		if err := b.Preserve(recs[i], "custodian", 1100); err != nil {
			t.Fatalf("Preserve: %v", err)
		}
	}
	if a.Hash() != b.Hash() {
		t.Fatal("the order hash must not depend on the sequence items were preserved in")
	}
}

func TestVerifyDetectsAHashMismatch(t *testing.T) {
	o := mustOrder(t)
	rec := mustRecord(t, "CASE-INS-002", "s1", 100)
	if err := o.Preserve(rec, "custodian", 1100); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	anchored := o.Hash()

	if r := o.Verify(anchored); !r.Pass() {
		t.Fatalf("an intact order must pass, failures: %v", r.Failures)
	}

	// Anything that changes the order's semantic content breaks the
	// anchored hash.
	if err := o.Preserve(mustRecord(t, "CASE-INS-002", "s2", 200), "custodian", 1200); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	r := o.Verify(anchored)
	if r.Pass() {
		t.Fatal("an order whose preserved set changed must fail against its anchored hash")
	}
	if !strings.Contains(strings.Join(r.Failures, " "), "recomputed") {
		t.Fatalf("the failure must name the mismatch, got %v", r.Failures)
	}
	if r.ChainVerified {
		t.Fatal("ChainVerified must be false on a hash mismatch")
	}
}

// ---- The §56 chain report --------------------------------------------

// TestChainReportPassIsDerivedFromFailures: there is no settable pass
// field anywhere.
func TestChainReportPassIsDerivedFromFailures(t *testing.T) {
	rt := reflect.TypeOf(ChainReport{})
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Name == "Pass" {
			t.Fatal("ChainReport must have no settable Pass field — Pass() is derived from Failures")
		}
	}
	r := ChainReport{}
	if !r.Pass() {
		t.Fatal("an empty failure list is a pass")
	}
	r.Failures = append(r.Failures, "something")
	if r.Pass() {
		t.Fatal("a non-empty failure list must not pass")
	}
}

// TestAnOrderCoveringNothingFails: the §56 gate requires evidence to
// actually be preserved, not merely an order to exist.
func TestAnOrderCoveringNothingFails(t *testing.T) {
	o := mustOrder(t)
	r := o.Verify("")
	if r.Pass() {
		t.Fatal("an order covering no evidence must not pass the preservation chain check")
	}
	if r.EvidencePreserved {
		t.Fatal("EvidencePreserved must be false")
	}
	joined := strings.Join(r.Failures, " ")
	if !strings.Contains(joined, "covers no evidence") {
		t.Fatalf("the failure must say so, got %v", r.Failures)
	}
}

// TestAnOrderWithNoAccessEventsStillPasses: an order nobody has yet
// touched is intact. Requiring an access event to have happened would
// push a caller to fabricate one.
func TestAnOrderWithNoAccessEventsStillPasses(t *testing.T) {
	o := mustOrder(t)
	if err := o.Preserve(mustRecord(t, "CASE-INS-002", "s1", 100), "custodian", 1100); err != nil {
		t.Fatalf("Preserve: %v", err)
	}
	r := o.Verify("")
	if !r.Pass() {
		t.Fatalf("an untouched but intact order must pass, failures: %v", r.Failures)
	}
	if !r.AccessEventsWellFormed || !r.ExportEventsWellFormed || !r.CorrectionEventsWellFormed {
		t.Fatal("well-formedness is vacuously true when there are no such events")
	}
}

// ---- Legal hold ------------------------------------------------------

// TestReleaseIsRefusedWhileAHoldIsInForce is the rule a legal hold
// exists to enforce.
func TestReleaseIsRefusedWhileAHoldIsInForce(t *testing.T) {
	o := mustOrder(t)
	if err := o.PlaceHold("legal counsel", "dispute opened", 1200); err != nil {
		t.Fatalf("PlaceHold: %v", err)
	}
	if o.HoldState() != HoldInForce {
		t.Fatalf("HoldState = %q", o.HoldState())
	}
	if err := o.Release("claims officer", "case closed", 1300); !errors.Is(err, ErrHoldInForce) {
		t.Fatalf("expected ErrHoldInForce, got %v", err)
	}
	if o.Released() {
		t.Fatal("the order must not have been released")
	}

	if err := o.ReleaseHold("legal counsel", "matter concluded", 1400); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if err := o.Release("claims officer", "case closed", 1500); err != nil {
		t.Fatalf("Release after the hold was lifted: %v", err)
	}
	if !o.Released() {
		t.Fatal("the order should now be released")
	}
	if err := o.Release("claims officer", "again", 1600); !errors.Is(err, ErrOrderReleased) {
		t.Fatalf("expected ErrOrderReleased, got %v", err)
	}
}

func TestHoldHistorySurvivesRelease(t *testing.T) {
	o := mustOrder(t)
	if err := o.PlaceHold("legal counsel", "regulatory request received", 1200); err != nil {
		t.Fatalf("PlaceHold: %v", err)
	}
	if err := o.ReleaseHold("legal counsel", "request withdrawn", 1400); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	placed, released := false, false
	for _, e := range o.CustodyLog() {
		if e.Action == ActionHoldPlaced {
			placed = true
		}
		if e.Action == ActionHoldReleased {
			released = true
		}
	}
	if !placed || !released {
		t.Fatal("both the placing and the lifting of the hold must survive in the custody log")
	}
	if o.HoldState() != HoldReleased {
		t.Fatalf("HoldState = %q, want RELEASED", o.HoldState())
	}
}

func TestReleasedOrderRefusesFurtherPreservation(t *testing.T) {
	o := mustOrder(t)
	if err := o.Release("claims officer", "no claim materialised", 1300); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := o.Preserve(mustRecord(t, "CASE-INS-002", "s1", 100), "custodian", 1400); !errors.Is(err, ErrOrderReleased) {
		t.Fatalf("expected ErrOrderReleased, got %v", err)
	}
}

// ---- Rights gate -----------------------------------------------------

// TestPermitsUseRequiresBothTheRecordAndTheOrder: a permissive order
// can never widen a restricted record, and a restrictive order narrows
// everything under it.
func TestPermitsUseRequiresBothTheRecordAndTheOrder(t *testing.T) {
	o := mustOrder(t)
	rec := mustRecord(t, "CASE-INS-002", "s1", 100)
	if err := o.Preserve(rec, "custodian", 1100); err != nil {
		t.Fatalf("Preserve: %v", err)
	}

	// Neither permits dispute use yet.
	if err := o.PermitsUse(rec, provenance.UseDispute); !errors.Is(err, ErrRightsDeny) {
		t.Fatalf("expected ErrRightsDeny, got %v", err)
	}

	// The record alone is not enough.
	rec.Rights = provenance.RightsDisputeUseAllowed
	if err := o.PermitsUse(rec, provenance.UseDispute); !errors.Is(err, ErrRightsDeny) {
		t.Fatal("a permissive record under a restrictive order must still be denied")
	}

	// Both permit: allowed.
	if err := o.SetRights(provenance.RightsDisputeUseAllowed, "legal counsel", 1200); err != nil {
		t.Fatalf("SetRights: %v", err)
	}
	if err := o.PermitsUse(rec, provenance.UseDispute); err != nil {
		t.Fatalf("with both permitting, use must be allowed: %v", err)
	}

	// A revoked record is denied whatever the order says.
	rec.Rights = provenance.RightsRevoked
	if err := o.PermitsUse(rec, provenance.UseDispute); !errors.Is(err, ErrRightsDeny) {
		t.Fatal("a REVOKED record must be denied under any order")
	}
}

func TestPermitsUseRefusesAnItemTheOrderDoesNotCover(t *testing.T) {
	o := mustOrder(t)
	rec := mustRecord(t, "CASE-INS-002", "s1", 100)
	if err := o.PermitsUse(rec, provenance.UseInternalOnly); !errors.Is(err, ErrNotPreserved) {
		t.Fatalf("expected ErrNotPreserved, got %v", err)
	}
}

// ---- Structural guardrails -------------------------------------------

func TestNoVerdictOrConfidenceFieldsInThisPackage(t *testing.T) {
	forbidden := []string{
		"verdict", "liable", "liability", "guilt", "approved", "denied",
		"denial", "payable", "confidence", "score",
	}
	types := []reflect.Type{
		reflect.TypeOf(Order{}), reflect.TypeOf(CustodyEvent{}), reflect.TypeOf(ChainReport{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.ToLower(f.Name)
			for _, bad := range forbidden {
				if strings.Contains(name, bad) {
					t.Fatalf("%s has field %q containing forbidden token %q", typ.Name(), f.Name, bad)
				}
			}
			if f.Type.Kind() == reflect.Float64 || f.Type.Kind() == reflect.Float32 {
				t.Fatalf("%s.%s is a float", typ.Name(), f.Name)
			}
		}
	}
}
