// Session update — closes further roadmap-adjacent coverage:
// pkg/kernel/ontology's checkKind was only 13.6% covered — only the
// FieldString empty-value path was ever exercised. This exercises
// every FieldKind's success and failure branch: FieldBool,
// FieldNumber (integer, decimal, negative, and malformed), FieldRef,
// and the unknown-kind default branch.
package ontology

import "testing"

func typeWithField(kind FieldKind) TypeDef {
	return TypeDef{
		Name: "GapProbe", Kind: KindEntity, Version: 1,
		Fields: []FieldSpec{{Name: "v", Kind: kind, Required: true}},
	}
}

func TestCheckKind_Bool(t *testing.T) {
	m := NewManager()
	m.RegisterType(typeWithField(FieldBool))

	if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": "true"}}); err != nil {
		t.Fatalf("expected \"true\" to validate as bool: %v", err)
	}
	if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": "false"}}); err != nil {
		t.Fatalf("expected \"false\" to validate as bool: %v", err)
	}
	if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": "maybe"}}); err == nil {
		t.Fatal("expected an error for a non-bool value")
	}
}

func TestCheckKind_Number(t *testing.T) {
	m := NewManager()
	m.RegisterType(typeWithField(FieldNumber))

	valid := []string{"42", "3.14", "-7", "-2.5", "0"}
	for _, v := range valid {
		if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": v}}); err != nil {
			t.Fatalf("expected %q to validate as a number: %v", v, err)
		}
	}
	invalid := []string{"", "-", "12x", "1.2.3", "abc"}
	for _, v := range invalid {
		if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": v}}); err == nil {
			t.Fatalf("expected %q to be rejected as not-a-number", v)
		}
	}
}

func TestCheckKind_Ref(t *testing.T) {
	m := NewManager()
	m.RegisterType(typeWithField(FieldRef))

	if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": "some-id"}}); err != nil {
		t.Fatalf("expected a non-empty ref to validate: %v", err)
	}
	if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": ""}}); err == nil {
		t.Fatal("expected an error for an empty ref value")
	}
}

func TestCheckKind_UnknownFieldKindRejected(t *testing.T) {
	m := NewManager()
	m.RegisterType(typeWithField(FieldKind("UNKNOWN_KIND")))
	if _, err := m.Accept(Instance{TypeName: "GapProbe", Fields: map[string]string{"v": "anything"}}); err == nil {
		t.Fatal("expected an error for an unrecognized FieldKind")
	}
}
