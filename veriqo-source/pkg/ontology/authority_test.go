package ontology

import (
	"strings"
	"testing"
)

// TestEveryCanonicalObjectDeclaresItsAuthority is the completeness
// rule. An object whose authority is undeclared is an object nobody has
// had to think about, and it will be the one a dispute turns on.
func TestEveryCanonicalObjectDeclaresItsAuthority(t *testing.T) {
	if err := ValidateAuthorities(); err != nil {
		t.Fatal(err)
	}
}

// TestAnIncompleteQuintupleIsRefused proves each of the five fields is
// load-bearing rather than aspirational.
func TestAnIncompleteQuintupleIsRefused(t *testing.T) {
	full := AuthorityDecl{
		Type: AuthorityEpistemic, Subject: "an analyst",
		Basis: "a pinned policy version", Scope: "one claim", Time: "while the case is open",
	}
	if err := full.Validate(); err != nil {
		t.Fatalf("a complete declaration must validate: %v", err)
	}
	for _, tc := range []struct {
		name   string
		breaks func(*AuthorityDecl)
		want   string
	}{
		{"no type", func(d *AuthorityDecl) { d.Type = "" }, "canonical authority type"},
		{"an invented type", func(d *AuthorityDecl) { d.Type = "SUPERUSER" }, "canonical authority type"},
		{"no subject", func(d *AuthorityDecl) { d.Subject = "" }, "SUBJECT"},
		{"no basis", func(d *AuthorityDecl) { d.Basis = "" }, "BASIS"},
		{"no scope", func(d *AuthorityDecl) { d.Scope = "" }, "SCOPE"},
		{"no time", func(d *AuthorityDecl) { d.Time = "" }, "TIME"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := full
			tc.breaks(&d)
			err := d.Validate()
			if err == nil {
				t.Fatal("an incomplete declaration validated")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error naming %s, got %v", tc.want, err)
			}
		})
	}
}

// TestTheImplementationIsNotABasis. "The code allows it" describes how
// a permission is currently enforced, not why anyone has it. A basis
// like that is how an implementation detail becomes a right.
func TestTheImplementationIsNotABasis(t *testing.T) {
	for _, basis := range []string{
		"the code allows it",
		"it is possible to call the setter",
		"no restriction is enforced",
		"by convention only",
	} {
		d := AuthorityDecl{
			Type: AuthorityRegistry, Subject: "any caller",
			Basis: basis, Scope: "everything", Time: "always",
		}
		if err := d.Validate(); err == nil {
			t.Fatalf("%q was accepted as a basis for authority", basis)
		}
	}
}

// TestProvenanceAndAuthorityAreSeparate is the distinction the review
// asked to be preserved. An object may have impeccable provenance and
// no epistemic authority at all -- a document from Lloyd's List is well
// sourced and concludes nothing.
func TestProvenanceAndAuthorityAreSeparate(t *testing.T) {
	doc, ok := AuthorityOf(ObjectDocument)
	if !ok {
		t.Fatal("ObjectDocument declares no authority")
	}
	if doc.Type != AuthorityAcquisition {
		t.Fatalf("a document's authority is acquisition, got %s", doc.Type)
	}
	finding, ok := AuthorityOf(ObjectFinding)
	if !ok {
		t.Fatal("ObjectFinding declares no authority")
	}
	if finding.Type != AuthorityEpistemic {
		t.Fatalf("a finding's authority is epistemic, got %s", finding.Type)
	}
	if doc.Type == finding.Type {
		t.Fatal("acquiring a document and concluding something from it are the same authority")
	}
}

// TestNoObjectClaimsUnboundedAuthority. An authority with no time bound
// and no scope bound is ownership, not authority.
func TestNoObjectClaimsUnboundedAuthority(t *testing.T) {
	for typ, d := range authorityDecls {
		if strings.TrimSpace(d.Scope) == "" || strings.EqualFold(strings.TrimSpace(d.Scope), "everything") {
			t.Errorf("%s claims unbounded scope", typ)
		}
		if strings.EqualFold(strings.TrimSpace(d.Time), "always") {
			t.Errorf("%s claims unbounded time", typ)
		}
	}
}

// TestOnlyPeopleHoldAdjudicativeAuthority. Article 16: the platform
// does not determine legal liability. An adjudicative authority whose
// subject is a package would be the system adjudicating.
func TestOnlyPeopleHoldAdjudicativeAuthority(t *testing.T) {
	for typ, d := range authorityDecls {
		if d.Type != AuthorityAdjudicative {
			continue
		}
		if strings.HasPrefix(d.Subject, "veriqo/") {
			t.Errorf("%s gives adjudicative authority to the package %q: the system would be adjudicating", typ, d.Subject)
		}
	}
}

// TestDeclaratoryObjectsDoNotDetermineAnything. VERIQO records a
// vessel's IMO number; it does not decide it. A declaratory object
// whose basis is a VERIQO rule would mean VERIQO had become the source
// of truth for a fact about the world.
func TestDeclaratoryObjectsDeferToTheWorld(t *testing.T) {
	for typ, d := range authorityDecls {
		if d.Type != AuthorityDeclaratory {
			continue
		}
		if strings.Contains(strings.ToLower(d.Basis), "veriqo determines") {
			t.Errorf("%s claims VERIQO determines a fact about the world", typ)
		}
	}
}

// TestTheReportNamesEveryObject. A report that silently omitted an
// object would let one slip out of review.
func TestTheReportNamesEveryObject(t *testing.T) {
	report := AuthorityReport()
	for _, c := range typeContracts {
		if !strings.Contains(report, string(c.Type)) {
			t.Errorf("the authority report omits %s", c.Type)
		}
	}
}
