package ontology

import (
	"errors"
	"testing"

	"veriqo/pkg/platform/audit"
)

func TestValidateRejectsUnknownObjectType(t *testing.T) {
	o := Object{ObjectType: "NotARealType", ObjectID: "e1", TenantID: "t1"}
	if err := o.Validate(); !errors.Is(err, ErrUnknownObjectType) {
		t.Fatalf("expected ErrUnknownObjectType, got %v", err)
	}
}

func TestValidateRejectsEmptyObjectID(t *testing.T) {
	o := Object{ObjectType: ObjectCase, ObjectID: "", TenantID: "t1"}
	if err := o.Validate(); !errors.Is(err, ErrEmptyObjectID) {
		t.Fatalf("expected ErrEmptyObjectID, got %v", err)
	}
}

func TestValidateRejectsEmptyTenantID(t *testing.T) {
	o := Object{ObjectType: ObjectCase, ObjectID: "c1", TenantID: ""}
	if err := o.Validate(); !errors.Is(err, ErrEmptyTenantID) {
		t.Fatalf("expected ErrEmptyTenantID, got %v", err)
	}
}

func TestKnownObjectTypesCoversCoreVocabulary(t *testing.T) {
	for _, want := range []ObjectType{
		ObjectCase, ObjectEvidence, ObjectFact, ObjectContract, ObjectParty,
		ObjectClause, ObjectBreach, ObjectCausation, ObjectFinding, ObjectResolutionPackage,
	} {
		if !IsKnownObjectType(want) {
			t.Fatalf("expected %q to be a known core object type", want)
		}
	}
}

func TestRegisterObjectTypeExtendsVocabulary(t *testing.T) {
	custom := ObjectType("DomainPackWidget")
	if IsKnownObjectType(custom) {
		t.Fatalf("did not expect %q to be known before registration", custom)
	}
	RegisterObjectType(custom)
	if !IsKnownObjectType(custom) {
		t.Fatalf("expected %q to be known after registration", custom)
	}
	found := false
	for _, t2 := range KnownObjectTypes() {
		if t2 == custom {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected KnownObjectTypes() to include %q", custom)
	}
}

func TestLinkValidateRejectsUnknownLinkType(t *testing.T) {
	l := Link{LinkType: "NOT_A_REAL_LINK", FromType: ObjectCase, FromID: "c1", ToType: ObjectEvidence, ToID: "e1", TenantID: "t1"}
	if err := l.Validate(); !errors.Is(err, ErrUnknownLinkType) {
		t.Fatalf("expected ErrUnknownLinkType, got %v", err)
	}
}

func TestLinkValidateRejectsUnknownEndpointObjectType(t *testing.T) {
	l := Link{LinkType: LinkCaseHasEvidence, FromType: "Bogus", FromID: "c1", ToType: ObjectEvidence, ToID: "e1", TenantID: "t1"}
	if err := l.Validate(); !errors.Is(err, ErrUnknownObjectType) {
		t.Fatalf("expected ErrUnknownObjectType, got %v", err)
	}
}

func TestLinkValidateRejectsEmptyEndpointIDs(t *testing.T) {
	l := Link{LinkType: LinkCaseHasEvidence, FromType: ObjectCase, FromID: "", ToType: ObjectEvidence, ToID: "e1", TenantID: "t1"}
	if err := l.Validate(); !errors.Is(err, ErrEmptyObjectID) {
		t.Fatalf("expected ErrEmptyObjectID, got %v", err)
	}
}

func TestCreateObjectRegistersAndRefusesDuplicate(t *testing.T) {
	r := NewRegistry()
	o := Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "tenant-1"}
	created, err := r.CreateObject(o, "actor-1", 100, nil)
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if created.CreatedAt != 100 {
		t.Fatalf("expected CreatedAt to be set from tick, got %d", created.CreatedAt)
	}
	got, ok := r.Get(ObjectCase, "case-1", "tenant-1")
	if !ok {
		t.Fatal("expected object to be retrievable after CreateObject")
	}
	if got.Key() != o.Key() {
		t.Fatalf("got %v, want key %v", got, o.Key())
	}
	if _, err := r.CreateObject(o, "actor-1", 101, nil); !errors.Is(err, ErrDuplicateObject) {
		t.Fatalf("expected ErrDuplicateObject on re-create, got %v", err)
	}
}

func TestCreateObjectRefusesInvalidObject(t *testing.T) {
	r := NewRegistry()
	_, err := r.CreateObject(Object{ObjectType: "Bogus", ObjectID: "x", TenantID: "t1"}, "actor-1", 1, nil)
	if err == nil {
		t.Fatal("expected an error for an invalid object")
	}
}

func TestExecuteActionRefusesOnPolicyDenial(t *testing.T) {
	r := NewRegistry()
	deny := func() error { return errors.New("purpose binding does not authorize this actor") }
	_, err := r.ExecuteAction("Test", "actor-1", 1, deny,
		func() error { return nil },
		func() (string, error) { return "should not run", nil })
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
}

func TestExecuteActionRefusesOnValidationFailure(t *testing.T) {
	r := NewRegistry()
	executed := false
	_, err := r.ExecuteAction("Test", "actor-1", 1, nil,
		func() error { return errors.New("command is malformed") },
		func() (string, error) { executed = true; return "", nil })
	if err == nil {
		t.Fatal("expected a validation error")
	}
	if executed {
		t.Fatal("execute must not run when command validation fails")
	}
}

func TestExecuteActionRefusesOnExecutionFailure(t *testing.T) {
	r := NewRegistry()
	_, err := r.ExecuteAction("Test", "actor-1", 1, nil,
		func() error { return nil },
		func() (string, error) { return "", errors.New("deterministic execution blew up") })
	if err == nil {
		t.Fatal("expected an execution error")
	}
}

func TestExecuteActionMirrorsToAuditStoreWhenAttached(t *testing.T) {
	r := NewRegistry()
	store := audit.NewAuditStore()
	r.AttachAuditStore(store)
	res, err := r.ExecuteAction("Test", "actor-1", 42, nil,
		func() error { return nil },
		func() (string, error) { return "did the thing", nil })
	if err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}
	if res.Outcome != "did the thing" {
		t.Fatalf("unexpected outcome %q", res.Outcome)
	}
	records := store.Snapshot()
	if len(records) != 1 {
		t.Fatalf("expected exactly one audit record, got %d", len(records))
	}
	if records[0].Action != "Test" {
		t.Fatalf("expected audit record action %q, got %q", "Test", records[0].Action)
	}
}

func TestExecuteActionDoesNotMirrorWhenNoAuditStoreAttached(t *testing.T) {
	r := NewRegistry()
	if r.AuditStore != nil {
		t.Fatal("expected a fresh registry to have no attached audit store")
	}
	_, err := r.ExecuteAction("Test", "actor-1", 1, nil,
		func() error { return nil },
		func() (string, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}
}

func TestTransitionObjectStateUpdatesStateAndRefusesUnknownObject(t *testing.T) {
	r := NewRegistry()
	if _, err := r.TransitionObjectState(ObjectCase, "missing", "t1", "OPEN", "actor-1", 1, nil); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}
	if _, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "t1", State: "DRAFT"}, "actor-1", 1, nil); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	updated, err := r.TransitionObjectState(ObjectCase, "case-1", "t1", "OPEN", "actor-1", 2, nil)
	if err != nil {
		t.Fatalf("TransitionObjectState: %v", err)
	}
	if updated.State != "OPEN" {
		t.Fatalf("expected state OPEN, got %q", updated.State)
	}
}

func TestCreateLinkRequiresBothEndpointsToExist(t *testing.T) {
	r := NewRegistry()
	if _, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "t1"}, "actor-1", 1, nil); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	l := Link{LinkType: LinkCaseHasEvidence, FromType: ObjectCase, FromID: "case-1", ToType: ObjectEvidence, ToID: "evidence-1", TenantID: "t1"}
	if _, err := r.CreateLink(l, "actor-1", 2, nil); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound for missing target, got %v", err)
	}
	if _, err := r.CreateObject(Object{ObjectType: ObjectEvidence, ObjectID: "evidence-1", TenantID: "t1"}, "actor-1", 2, nil); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	created, err := r.CreateLink(l, "actor-1", 3, nil)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("expected first link between this pair to be version 1, got %d", created.Version)
	}
	if created.CreatedBy != "actor-1" {
		t.Fatalf("expected CreatedBy to be set, got %q", created.CreatedBy)
	}
}

func TestCreateLinkVersionsRepeatedLinksBetweenSamePair(t *testing.T) {
	r := NewRegistry()
	must := func(err error) {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	_, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	_, err = r.CreateObject(Object{ObjectType: ObjectEvidence, ObjectID: "evidence-1", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	l := Link{LinkType: LinkCaseHasEvidence, FromType: ObjectCase, FromID: "case-1", ToType: ObjectEvidence, ToID: "evidence-1", TenantID: "t1"}
	first, err := r.CreateLink(l, "actor-1", 2, nil)
	must(err)
	second, err := r.CreateLink(l, "actor-1", 3, nil)
	must(err)
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("expected versions 1 and 2, got %d and %d", first.Version, second.Version)
	}
}

func TestLinksFromAndLinksToTraversal(t *testing.T) {
	r := NewRegistry()
	must := func(err error) {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	_, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	_, err = r.CreateObject(Object{ObjectType: ObjectEvidence, ObjectID: "evidence-1", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	_, err = r.CreateObject(Object{ObjectType: ObjectEvidence, ObjectID: "evidence-2", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	_, err = r.CreateLink(Link{LinkType: LinkCaseHasEvidence, FromType: ObjectCase, FromID: "case-1", ToType: ObjectEvidence, ToID: "evidence-1", TenantID: "t1"}, "actor-1", 2, nil)
	must(err)
	_, err = r.CreateLink(Link{LinkType: LinkCaseHasEvidence, FromType: ObjectCase, FromID: "case-1", ToType: ObjectEvidence, ToID: "evidence-2", TenantID: "t1"}, "actor-1", 3, nil)
	must(err)

	from := r.LinksFrom(ObjectCase, "case-1")
	if len(from) != 2 {
		t.Fatalf("expected 2 outbound links from case-1, got %d", len(from))
	}
	to := r.LinksTo(ObjectEvidence, "evidence-1")
	if len(to) != 1 || to[0].ToID != "evidence-1" {
		t.Fatalf("expected exactly one inbound link to evidence-1, got %v", to)
	}
	if len(r.LinksTo(ObjectEvidence, "evidence-99")) != 0 {
		t.Fatal("expected no links to a nonexistent object")
	}
}

func TestByTypeReturnsOnlyMatchingObjectsInRegistrationOrder(t *testing.T) {
	r := NewRegistry()
	must := func(err error) {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	_, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	_, err = r.CreateObject(Object{ObjectType: ObjectEvidence, ObjectID: "evidence-1", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	_, err = r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-2", TenantID: "t1"}, "actor-1", 1, nil)
	must(err)
	cases := r.ByType(ObjectCase)
	if len(cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(cases))
	}
	if cases[0].ObjectID != "case-1" || cases[1].ObjectID != "case-2" {
		t.Fatalf("expected registration order, got %v", cases)
	}
}

func TestObjectKeyIsScopedByTenant(t *testing.T) {
	a := Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "tenant-a"}
	b := Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "tenant-b"}
	if a.Key() == b.Key() {
		t.Fatal("expected different tenants to produce different keys for the same ObjectID")
	}
}

func TestRegistryIsTenantIsolatedAcrossCreateAndGet(t *testing.T) {
	r := NewRegistry()
	_, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "tenant-a"}, "actor-1", 1, nil)
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if _, ok := r.Get(ObjectCase, "case-1", "tenant-b"); ok {
		t.Fatal("expected object registered under tenant-a to not be visible under tenant-b")
	}
	// Same ObjectID+ObjectType under a different tenant is a distinct object, not a duplicate.
	if _, err := r.CreateObject(Object{ObjectType: ObjectCase, ObjectID: "case-1", TenantID: "tenant-b"}, "actor-1", 1, nil); err != nil {
		t.Fatalf("expected same ObjectID under a different tenant to be a distinct object, got %v", err)
	}
}
