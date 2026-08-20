package ontology

import "testing"

func vesselType() TypeDef {
	return TypeDef{
		Name: "Vessel", Kind: KindEntity, Version: 1,
		Fields: []FieldSpec{
			{Name: "imo", Kind: FieldString, Required: true},
			{Name: "flag", Kind: FieldString, Required: false},
		},
	}
}

func sanctionActionType() TypeDef {
	return TypeDef{
		Name: "FlagShadowFleet", Kind: KindAction, Version: 1,
		Fields:        []FieldSpec{{Name: "reason", Kind: FieldString, Required: true}},
		RequiresKinds: []string{"Vessel"},
	}
}

func TestRegisterAndValidate(t *testing.T) {
	m := NewManager()
	if _, err := m.RegisterType(vesselType()); err != nil {
		t.Fatalf("register: %v", err)
	}
	in := Instance{TypeName: "Vessel", Fields: map[string]string{"imo": "1234567"}}
	id, err := m.Accept(in)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
	got, ok := m.Instance(id)
	if !ok || got.Fields["imo"] != "1234567" {
		t.Fatalf("instance lookup mismatch: %+v", got)
	}
}

func TestValidateMissingRequired(t *testing.T) {
	m := NewManager()
	m.RegisterType(vesselType())
	_, err := m.Accept(Instance{TypeName: "Vessel", Fields: map[string]string{}})
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}

func TestUnknownType(t *testing.T) {
	m := NewManager()
	_, err := m.Accept(Instance{TypeName: "Nonexistent", Fields: map[string]string{}})
	if err == nil {
		t.Fatal("expected ErrUnknownType")
	}
}

func TestActionRequiresKinds(t *testing.T) {
	m := NewManager()
	m.RegisterType(vesselType())
	m.RegisterType(sanctionActionType())
	vesselID, _ := m.Accept(Instance{TypeName: "Vessel", Fields: map[string]string{"imo": "1234567"}})

	// Missing refs -> rejected.
	_, err := m.Accept(Instance{TypeName: "FlagShadowFleet", Fields: map[string]string{"reason": "AIS gap"}})
	if err == nil {
		t.Fatal("expected ErrMissingRequires")
	}

	// With required ref -> accepted.
	_, err = m.Accept(Instance{
		TypeName: "FlagShadowFleet",
		Fields:   map[string]string{"reason": "AIS gap"},
		Refs:     []string{vesselID},
	})
	if err != nil {
		t.Fatalf("expected acceptance with ref present: %v", err)
	}
}

func TestSchemaEvolutionVersioning(t *testing.T) {
	m := NewManager()
	m.RegisterType(vesselType())
	v2 := vesselType()
	v2.Version = 2
	v2.Fields = append(v2.Fields, FieldSpec{Name: "owner", Kind: FieldRef, Required: false})
	if _, err := m.RegisterType(v2); err != nil {
		t.Fatalf("register v2: %v", err)
	}
	latest, ok := m.TypeByName("Vessel")
	if !ok || latest.Version != 2 {
		t.Fatalf("expected latest version 2, got %+v", latest)
	}
	// Re-registering identical (name,version) is rejected.
	if _, err := m.RegisterType(vesselType()); err == nil {
		t.Fatal("expected ErrTypeExists for duplicate (name,version)")
	}
}

func TestKindsOf(t *testing.T) {
	m := NewManager()
	m.RegisterType(vesselType())
	m.RegisterType(sanctionActionType())
	entities := m.KindsOf(KindEntity)
	if len(entities) != 1 || entities[0] != "Vessel" {
		t.Fatalf("unexpected entity kinds: %v", entities)
	}
	actions := m.KindsOf(KindAction)
	if len(actions) != 1 || actions[0] != "FlagShadowFleet" {
		t.Fatalf("unexpected action kinds: %v", actions)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	m := NewManager()
	m.RegisterType(vesselType())
	m.Accept(Instance{TypeName: "Vessel", Fields: map[string]string{"imo": "1234567"}})
	if err := m.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain: %v", err)
	}
	// Tamper with an entry directly.
	m.mu.Lock()
	m.changelog[0].InstID = "tampered"
	m.mu.Unlock()
	if err := m.VerifyChain(); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestReplayDeterminism(t *testing.T) {
	build := func() *Manager {
		m := NewManager()
		m.RegisterType(vesselType())
		m.RegisterType(sanctionActionType())
		vesselID, _ := m.Accept(Instance{TypeName: "Vessel", Fields: map[string]string{"imo": "1234567"}})
		m.Accept(Instance{TypeName: "FlagShadowFleet", Fields: map[string]string{"reason": "AIS gap"}, Refs: []string{vesselID}})
		return m
	}
	a, b := build(), build()
	logA, logB := a.Changelog(), b.Changelog()
	if len(logA) != len(logB) {
		t.Fatalf("changelog length mismatch: %d vs %d", len(logA), len(logB))
	}
	for i := range logA {
		if logA[i].Hash != logB[i].Hash {
			t.Fatalf("changelog hash mismatch at %d: %s vs %s", i, logA[i].Hash, logB[i].Hash)
		}
	}
}
