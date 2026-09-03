package ontology

import (
	"strings"
	"testing"
)

// TestEveryCanonicalObjectHasACompleteContract is what turns "forty
// canonical objects" from a count into a claim.
//
// A type with a blank in any of the nine declarations is a name, not a
// canonical object, and this test refuses to let one ship behind the
// number.
func TestEveryCanonicalObjectHasACompleteContract(t *testing.T) {
	if err := ValidateObjectContracts(); err != nil {
		t.Fatalf("ValidateObjectContracts: %v", err)
	}
	if len(TypeContracts()) != len(KnownObjectTypes()) {
		t.Fatalf("%d contracts for %d registered types: the count and the contract have diverged",
			len(TypeContracts()), len(KnownObjectTypes()))
	}
}

// TestTheCountIsFortyAndEveryOneIsContracted keeps both halves honest.
// Either alone is misleading.
func TestTheCountIsFortyAndEveryOneIsContracted(t *testing.T) {
	if n := len(KnownObjectTypes()); n != 40 {
		t.Fatalf("expected 40 canonical object types, got %d", n)
	}
	for _, ty := range KnownObjectTypes() {
		c, ok := ContractForType(ty)
		if !ok {
			t.Fatalf("object type %q has no contract and is therefore a name, not a canonical object", ty)
		}
		if missing := c.Incomplete(); len(missing) > 0 {
			t.Fatalf("object type %q declares no %s", ty, strings.Join(missing, ", "))
		}
	}
}

// TestEveryObjectDeclaresAnOwner: a canonical object with no owning
// package has no one to ask when its semantics are in question.
func TestEveryObjectDeclaresAnOwner(t *testing.T) {
	for _, c := range TypeContracts() {
		if !strings.HasPrefix(c.Owner, "veriqo/pkg/") {
			t.Fatalf("object type %q names owner %q, which is not a VERIQO package", c.Type, c.Owner)
		}
	}
}

// TestHashSemanticsAreConsistent is the point of declaring
// canonicalization per object: forty different canonicalizations would
// mean forty different hashes for the same bytes.
func TestHashSemanticsAreConsistent(t *testing.T) {
	for _, c := range TypeContracts() {
		if !strings.Contains(c.Canonicalization, "JCS") {
			t.Fatalf("object type %q canonicalizes as %q: every hashed VERIQO object uses RFC 8785 JCS",
				c.Type, c.Canonicalization)
		}
		if !strings.Contains(c.Canonicalization, "excluded from its own computation") {
			t.Fatalf("object type %q does not state that the hash field is excluded from its own hash", c.Type)
		}
	}
}

// TestEveryObjectDeclaresAMutationRule is the declaration that decides
// whether an object can be quietly edited. Every one must have an
// answer, and the answer is never "anything may change".
func TestEveryObjectDeclaresAMutationRule(t *testing.T) {
	for _, c := range TypeContracts() {
		lower := strings.ToLower(c.MutationRule)
		constraining := false
		for _, k := range []string{"immutable", "never", "append", "version", "new ", "only", "copy", "refused", "cannot", "fixed in advance"} {
			if strings.Contains(lower, k) {
				constraining = true
				break
			}
		}
		if !constraining {
			t.Fatalf("object type %q states a mutation rule that constrains nothing: %q", c.Type, c.MutationRule)
		}
	}
}

// TestEveryObjectIsReplayable: an object that cannot be reproduced from
// the record is one an outside party has to take VERIQO's word for.
func TestEveryObjectIsReplayable(t *testing.T) {
	for _, c := range TypeContracts() {
		lower := strings.ToLower(c.ReplayBehaviour)
		if !strings.Contains(lower, "reproduc") && !strings.Contains(lower, "re-deriv") &&
			!strings.Contains(lower, "recomput") && !strings.Contains(lower, "verif") &&
			!strings.Contains(lower, "restore") && !strings.Contains(lower, "re-run") {
			t.Fatalf("object type %q states no reproduction path: %q", c.Type, c.ReplayBehaviour)
		}
	}
}

// TestEveryObjectDeclaresProvenanceAndAuthority separates where an
// instance came from (provenance) from who may create or change it
// (authority). Collapsing them loses the question that matters in a
// dispute: not just where is this from, but who was entitled to put it
// there.
func TestEveryObjectDeclaresProvenanceAndAuthority(t *testing.T) {
	for _, c := range TypeContracts() {
		if len(strings.Fields(c.Provenance)) < 3 {
			t.Fatalf("object type %q states provenance too thinly: %q", c.Type, c.Provenance)
		}
		if len(strings.Fields(c.Authority)) < 3 {
			t.Fatalf("object type %q states authority too thinly: %q", c.Type, c.Authority)
		}
		if strings.EqualFold(c.Provenance, c.Authority) {
			t.Fatalf("object type %q gives the same answer for provenance and authority", c.Type)
		}
	}
}

// TestNoContractForAnUnregisteredType: documentation of an object that
// does not exist is worse than none.
func TestNoContractForAnUnregisteredType(t *testing.T) {
	for _, c := range TypeContracts() {
		if !IsKnownObjectType(c.Type) {
			t.Fatalf("contract declared for unregistered object type %q", c.Type)
		}
	}
}

// TestTheProofObjectContractMatchesTheCode is a spot check that the
// contract describes the implementation rather than an aspiration.
func TestTheProofObjectContractMatchesTheCode(t *testing.T) {
	c, ok := ContractForType(ObjectProofObject)
	if !ok {
		t.Fatal("ProofObject has no contract")
	}
	if c.Owner != "veriqo/pkg/proof" {
		t.Fatalf("ProofObject is owned by %q", c.Owner)
	}
	if !strings.Contains(c.MutationRule, "overwrites any author-supplied stance") {
		t.Fatalf("the contract must state the derived-not-asserted rule: %q", c.MutationRule)
	}
	if !strings.Contains(c.ContentHash, "twenty-three components") {
		t.Fatalf("the contract must state what the hash covers: %q", c.ContentHash)
	}
}

func TestRenderObjectContractsCoversEveryTypeAndField(t *testing.T) {
	out := RenderObjectContracts()
	for _, ty := range KnownObjectTypes() {
		if !strings.Contains(out, string(ty)) {
			t.Fatalf("object type %q missing from the render", ty)
		}
	}
	for _, f := range []string{"OBJECT ID", "SCHEMA VERSION", "CANONICALIZATION", "CONTENT HASH",
		"PROVENANCE", "AUTHORITY", "LIFECYCLE", "MUTATION RULE", "REPLAY"} {
		if !strings.Contains(out, f) {
			t.Fatalf("declaration %q missing from the render", f)
		}
	}
}
