package network

import (
	"context"
	"testing"

	"veriqo/pkg/insurance/party"
)

// referenceRegistryAdapter is a COMPILE-TIME CONTRACT CHECK ONLY,
// matching referenceAdapter's own discipline: it proves
// ExternalRegistryAdapter (and therefore all six named registry
// interfaces, which are identical in shape) is implementable, and
// never fabricates a success answer.
type referenceRegistryAdapter struct{}

func (referenceRegistryAdapter) QueryRegistry(context.Context, RegistryQuery) (QualificationState, error) {
	return "", errNotARealCounterparty
}

var (
	_ InsurerRegistryAdapter       = referenceRegistryAdapter{}
	_ BrokerRegistryAdapter        = referenceRegistryAdapter{}
	_ PAndIRegistryAdapter         = referenceRegistryAdapter{}
	_ SurveyorAccreditationAdapter = referenceRegistryAdapter{}
	_ RegulatoryRegistryAdapter    = referenceRegistryAdapter{}
	_ CorporateRegistryAdapter     = referenceRegistryAdapter{}
)

func TestRegistrySourceVocabularyIsClosed(t *testing.T) {
	for _, s := range []RegistrySource{
		RegistrySourceInsurer, RegistrySourceBroker, RegistrySourcePAndI,
		RegistrySourceSurveyor, RegistrySourceRegulatory, RegistrySourceCorporate,
	} {
		if !IsKnownRegistrySource(s) {
			t.Errorf("expected %q to be a known registry source", s)
		}
	}
	if IsKnownRegistrySource("NOT_A_REGISTRY") {
		t.Fatal("an unknown source must never report as known")
	}
}

func TestRegistryQueryValidateRefusesMisroute(t *testing.T) {
	// P&I registry asked to qualify a regulator: a real misroute.
	q := RegistryQuery{Source: RegistrySourcePAndI, PartyID: "PTY-1", Role: party.RoleRegulator}
	if err := q.Validate(); err == nil {
		t.Fatal("expected a misrouted RegistryQuery to be refused")
	}
	ok := RegistryQuery{Source: RegistrySourcePAndI, PartyID: "PTY-1", Role: party.RolePAndIClub}
	if err := ok.Validate(); err != nil {
		t.Fatalf("expected a correctly routed RegistryQuery to pass, got %v", err)
	}
}

func TestRegistryDirectoryRefusesUnregisteredSource(t *testing.T) {
	d := NewRegistryDirectory()
	_, err := d.Query(context.Background(), RegistryQuery{Source: RegistrySourceInsurer, PartyID: "PTY-1", Role: party.RoleInsurer})
	if err == nil {
		t.Fatal("an empty RegistryDirectory must refuse every query, never fabricate an answer")
	}
}

func TestRegistryDirectoryRoutesToTheDeclaredSourceOnly(t *testing.T) {
	d := NewRegistryDirectory()
	if err := d.Register(RegistrySourceInsurer, referenceRegistryAdapter{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !d.Registered(RegistrySourceInsurer) {
		t.Fatal("expected RegistrySourceInsurer to be registered")
	}
	if d.Registered(RegistrySourceBroker) {
		t.Fatal("expected RegistrySourceBroker to be unregistered")
	}
	// Registering the same source twice must be refused.
	if err := d.Register(RegistrySourceInsurer, referenceRegistryAdapter{}); err == nil {
		t.Fatal("expected a duplicate Register on the same source to be refused")
	}
	// Querying a registered source dispatches to its own adapter (which
	// never fabricates success) rather than being refused outright.
	_, err := d.Query(context.Background(), RegistryQuery{Source: RegistrySourceInsurer, PartyID: "PTY-1", Role: party.RoleInsurer})
	if err == nil {
		t.Fatal("expected the reference adapter's own honest non-success to propagate")
	}
}

func TestRegistrySourceRoutesRoleIsNotUniversal(t *testing.T) {
	if RegistrySourcePAndI.RoutesRole(party.RoleRegulator) {
		t.Fatal("the P&I registry must not be modelled as a qualification authority for a regulator")
	}
	if !RegistrySourceRegulatory.RoutesRole(party.RoleRegulator) {
		t.Fatal("the regulatory registry must be modelled as a qualification authority for a regulator")
	}
}
