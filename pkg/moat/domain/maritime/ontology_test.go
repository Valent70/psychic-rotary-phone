package maritime

import "testing"

func vesselKey(imo string) map[string]string { return map[string]string{"imo": imo} }

func TestRegisterEntityDeterministicID(t *testing.T) {
	r1 := NewRegistry()
	r2 := NewRegistry()
	e1, err := r1.RegisterEntity(KindVessel, vesselKey("9876543"), map[string]string{"name": "MV Test"}, 1)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	e2, err := r2.RegisterEntity(KindVessel, vesselKey("9876543"), map[string]string{"name": "MV Test"}, 1)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if e1.ID != e2.ID {
		t.Fatalf("entity IDs diverged across independent registries: %s vs %s", e1.ID, e2.ID)
	}
}

func TestRegisterEntityDuplicateRejected(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterEntity(KindVessel, vesselKey("1"), nil, 1); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := r.RegisterEntity(KindVessel, vesselKey("1"), nil, 2); err != ErrDuplicateEntity {
		t.Fatalf("expected ErrDuplicateEntity, got %v", err)
	}
}

func TestRegisterEntityEmptyKeyRejected(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterEntity(KindVessel, nil, nil, 1); err != ErrEmptyNaturalKey {
		t.Fatalf("expected ErrEmptyNaturalKey, got %v", err)
	}
	if _, err := r.RegisterEntity(KindVessel, map[string]string{"imo": ""}, nil, 1); err != ErrEmptyNaturalKey {
		t.Fatalf("expected ErrEmptyNaturalKey for blank value, got %v", err)
	}
}

func TestRegisterRelationRequiresKnownEntities(t *testing.T) {
	r := NewRegistry()
	vessel, _ := r.RegisterEntity(KindVessel, vesselKey("1"), nil, 1)
	if _, err := r.RegisterRelation(vessel.ID, RelFlaggedIn, "unknown-id", 2); err != ErrUnknownEntity {
		t.Fatalf("expected ErrUnknownEntity, got %v", err)
	}
	country, _ := r.RegisterEntity(KindCountry, map[string]string{"iso": "PA"}, nil, 1)
	if _, err := r.RegisterRelation(vessel.ID, RelFlaggedIn, country.ID, 2); err != nil {
		t.Fatalf("register relation: %v", err)
	}
}

// TestMultiHopShipToSanction is the literal scenario the audit doc names:
// Vessel -> Ownership(Broker) -> Insurance -> Country -> SanctionEntry.
func TestMultiHopShipToSanction(t *testing.T) {
	r := NewRegistry()
	vessel, _ := r.RegisterEntity(KindVessel, vesselKey("9876543"), nil, 1)
	broker, _ := r.RegisterEntity(KindBroker, map[string]string{"name": "OpaqueTrading Ltd"}, nil, 1)
	insurer, _ := r.RegisterEntity(KindInsurer, map[string]string{"name": "ShadowP&I"}, nil, 1)
	country, _ := r.RegisterEntity(KindCountry, map[string]string{"iso": "IR"}, nil, 1)
	sanction, _ := r.RegisterEntity(KindSanctionEntry, map[string]string{"list": "OFAC-SDN", "ref": "12345"}, nil, 1)

	mustRel := func(from string, kind RelationKind, to string) {
		if _, err := r.RegisterRelation(from, kind, to, 2); err != nil {
			t.Fatalf("relation %s->%s: %v", from, to, err)
		}
	}
	mustRel(vessel.ID, RelBrokeredBy, broker.ID)
	mustRel(broker.ID, RelInsuredBy, insurer.ID)
	mustRel(insurer.ID, RelDomiciledIn, country.ID)
	mustRel(country.ID, RelSanctioned, sanction.ID)

	reached := r.MultiHop(vessel.ID, 4)
	path, ok := reached[sanction.ID]
	if !ok {
		t.Fatalf("expected to reach sanction entry within 4 hops, reached set: %v", reached)
	}
	want := []RelationKind{RelBrokeredBy, RelInsuredBy, RelDomiciledIn, RelSanctioned}
	if len(path) != len(want) {
		t.Fatalf("path length mismatch: got %v want %v", path, want)
	}
	for i := range want {
		if path[i] != want[i] {
			t.Fatalf("path[%d] = %s, want %s (full path %v)", i, path[i], want[i], path)
		}
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterEntity(KindVessel, vesselKey("1"), nil, 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain, got %v", err)
	}
	r.log[0].Payload = "tampered"
	if err := r.VerifyChain(); err != ErrTamperDetected {
		t.Fatalf("expected ErrTamperDetected, got %v", err)
	}
}

func TestToClaimAndToCausalNodeAreConsistent(t *testing.T) {
	r := NewRegistry()
	vessel, _ := r.RegisterEntity(KindVessel, vesselKey("9876543"), nil, 1)
	claim := vessel.ToClaim("FLAG_STATE")
	if claim.Subject != "VESSEL:"+vessel.ID {
		t.Fatalf("unexpected claim subject: %s", claim.Subject)
	}
	node := vessel.ToCausalNode()
	if string(node) != "VESSEL:"+vessel.ID {
		t.Fatalf("unexpected causal node: %s", node)
	}
}

func TestEntitiesOfKindAndCount(t *testing.T) {
	r := NewRegistry()
	r.RegisterEntity(KindVessel, vesselKey("1"), nil, 1)
	r.RegisterEntity(KindVessel, vesselKey("2"), nil, 1)
	r.RegisterEntity(KindPort, map[string]string{"unlocode": "IDTPP"}, nil, 1)
	if got := len(r.EntitiesOfKind(KindVessel)); got != 2 {
		t.Fatalf("expected 2 vessels, got %d", got)
	}
	if got := r.EntityCount(); got != 3 {
		t.Fatalf("expected 3 total entities, got %d", got)
	}
}

func TestDeriveEntityIDStableAcrossKeyOrder(t *testing.T) {
	id1, err := DeriveEntityID(KindVessel, map[string]string{"imo": "1", "name": "A"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := DeriveEntityID(KindVessel, map[string]string{"name": "A", "imo": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("ID depends on map iteration order: %s vs %s", id1, id2)
	}
}
