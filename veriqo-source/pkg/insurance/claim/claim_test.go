package claim

import (
	"errors"
	"testing"
)

func TestRegisterRejectsEmptyType(t *testing.T) {
	r := NewTypeRegistry()
	if err := r.Register(TypeDefinition{}); !errors.Is(err, ErrEmptyType) {
		t.Fatalf("expected ErrEmptyType, got %v", err)
	}
}

func TestRegisterRejectsNoRequiredEvidence(t *testing.T) {
	r := NewTypeRegistry()
	if err := r.Register(TypeDefinition{Type: TypeCargoDamage}); !errors.Is(err, ErrNoRequiredEvidence) {
		t.Fatalf("expected ErrNoRequiredEvidence, got %v", err)
	}
}

func TestRegisterRejectsDuplicateType(t *testing.T) {
	r := NewTypeRegistry()
	def := TypeDefinition{Type: TypeCargoDamage, RequiredEvidence: []string{"survey"}}
	if err := r.Register(def); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(def); !errors.Is(err, ErrDuplicateType) {
		t.Fatalf("expected ErrDuplicateType, got %v", err)
	}
}

func TestExtensibleRegistryAcceptsANonBuiltinType(t *testing.T) {
	// blueprint §4: "Jangan hard-code hanya cargo damage. Buat
	// extensible registry." -- a type NOT among the package's own
	// named constants must still register cleanly.
	r := NewTypeRegistry()
	custom := Type("EXOTIC_NEW_PERIL_NOT_IN_BLUEPRINT")
	if err := r.Register(TypeDefinition{Type: custom, RequiredEvidence: []string{"whatever_evidence"}}); err != nil {
		t.Fatalf("expected a non-builtin type to register successfully: %v", err)
	}
	if !r.IsRegistered(custom) {
		t.Fatal("expected custom type to be registered")
	}
}

func TestDefaultTypesRegisterCleanly(t *testing.T) {
	r := NewTypeRegistry()
	for _, def := range DefaultTypes() {
		if err := r.Register(def); err != nil {
			t.Fatalf("registering default type %s: %v", def.Type, err)
		}
	}
	if got := len(r.All()); got != KnownSeedTypeCount() {
		t.Fatalf("expected %d registered default types, got %d", KnownSeedTypeCount(), got)
	}
}

// TestKnownSeedTypeCountMatchesBlueprint pins the seed list against
// blueprint §4's own enumeration (24 claim types).
func TestKnownSeedTypeCountMatchesBlueprint(t *testing.T) {
	// Blueprint §4's list: CARGO_DAMAGE, CARGO_SHORTAGE,
	// CARGO_CONTAMINATION, CARGO_NON_DELIVERY, CARGO_THEFT,
	// REEFER_FAILURE, WET_DAMAGE, FIRE, COLLISION, GROUNDING,
	// CONTACT_DAMAGE, MACHINERY_DAMAGE, HULL_DAMAGE, POLLUTION,
	// GENERAL_AVERAGE, SALVAGE, DELAY, DEMURRAGE, DETENTION,
	// FREIGHT_LOSS, LIABILITY_TO_THIRD_PARTY, ENVIRONMENTAL_DAMAGE,
	// WAR_RISK, CYBER_MARINE, MULTIMODAL_DAMAGE = 25 entries.
	if got := KnownSeedTypeCount(); got != 25 {
		t.Fatalf("expected 25 claim types per VICE blueprint §4, got %d", got)
	}
	if got := len(DefaultTypes()); got != 25 {
		t.Fatalf("expected DefaultTypes to seed 25 definitions, got %d", got)
	}
}

func TestNewRejectsEmptyClaimID(t *testing.T) {
	if _, err := New("", "CASE-1", TypeCargoDamage, "PTY-001", nil); !errors.Is(err, ErrEmptyClaimID) {
		t.Fatalf("expected ErrEmptyClaimID, got %v", err)
	}
}

func TestNewRejectsEmptyCaseID(t *testing.T) {
	if _, err := New("CLM-1", "", TypeCargoDamage, "PTY-001", nil); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestNewRejectsEmptyClaimant(t *testing.T) {
	if _, err := New("CLM-1", "CASE-1", TypeCargoDamage, "", nil); !errors.Is(err, ErrEmptyClaimant) {
		t.Fatalf("expected ErrEmptyClaimant, got %v", err)
	}
}

func TestNewRejectsUnregisteredTypeWhenRegistryProvided(t *testing.T) {
	r := NewTypeRegistry()
	if _, err := New("CLM-1", "CASE-1", TypeCargoDamage, "PTY-001", r); !errors.Is(err, ErrUnregisteredType) {
		t.Fatalf("expected ErrUnregisteredType, got %v", err)
	}
}

func TestNewSucceedsWithRegisteredType(t *testing.T) {
	r := NewTypeRegistry()
	r.Register(TypeDefinition{Type: TypeCargoDamage, RequiredEvidence: []string{"survey"}})
	c, err := New("CLM-1", "CASE-1", TypeCargoDamage, "PTY-001", r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Status != StatusRegistered {
		t.Fatalf("expected default status REGISTERED, got %s", c.Status)
	}
}

func TestNewAllowsNilRegistry(t *testing.T) {
	// A nil registry means "don't validate against a registry" (e.g.
	// early intake before types are configured) -- must not panic.
	if _, err := New("CLM-1", "CASE-1", TypeCargoDamage, "PTY-001", nil); err != nil {
		t.Fatalf("expected nil registry to skip type validation, got %v", err)
	}
}
