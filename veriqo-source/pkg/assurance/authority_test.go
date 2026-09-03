package assurance

import (
	"strings"
	"testing"
)

// TestEveryDecisionHasExactlyOneAuthority is the rule the reviewer
// asked for: one authority, one decision path.
//
// Note what it does NOT check: that two packages avoid each other. Two
// packages can share no code, import nothing of each other's, and still
// both answer "is this sufficient?" — and the earlier anti-duplication
// tests, which looked for duplicate packages, would have passed.
func TestEveryDecisionHasExactlyOneAuthority(t *testing.T) {
	if err := ValidateAuthorities(); err != nil {
		t.Fatalf("ValidateAuthorities: %v", err)
	}
	for _, d := range DecisionAuthorities() {
		deciders := 1 // the authority itself
		for _, p := range d.Participants {
			if p.Role == RoleDecides {
				deciders++
			}
		}
		if deciders != 1 {
			t.Fatalf("decision %q has %d authorities", d.Decision, deciders)
		}
	}
}

// TestDerivationIsPermittedDecisionIsNot states the distinction that
// makes the audit useful rather than paranoid.
func TestDerivationIsPermittedDecisionIsNot(t *testing.T) {
	permitted := map[Role]bool{RoleDerives: true, RoleCopies: true, RoleRecords: true}
	for _, d := range DecisionAuthorities() {
		for _, p := range d.Participants {
			if !permitted[p.Role] {
				t.Fatalf("decision %q gives %s the role %q, which is not a non-authority role",
					d.Decision, p.Package, p.Role)
			}
		}
	}
	// And at least one decision must actually have derivers, or the
	// distinction is being asserted without being exercised.
	derivers := 0
	for _, d := range DecisionAuthorities() {
		for _, p := range d.Participants {
			if p.Role == RoleDerives {
				derivers++
			}
		}
	}
	if derivers == 0 {
		t.Fatal("no decision records a DERIVES participant: the distinction is unexercised")
	}
}

// TestTheThreeSufficiencyPackagesAreAudited is the reviewer's own
// example, checked directly.
func TestTheThreeSufficiencyPackagesAreAudited(t *testing.T) {
	d, ok := AuthorityFor("Is this proof object sufficient to found a finding?")
	if !ok {
		t.Fatal("sufficiency has no audited authority")
	}
	if d.Authority != "veriqo/pkg/proof" {
		t.Fatalf("sufficiency must be decided by pkg/proof, got %q", d.Authority)
	}
	roles := map[string]Role{}
	for _, p := range d.Participants {
		roles[p.Package] = p.Role
	}
	for _, pkg := range []string{"veriqo/pkg/casefabric", "veriqo/pkg/caseproofgraph"} {
		r, present := roles[pkg]
		if !present {
			t.Fatalf("%s touches sufficiency and is not audited", pkg)
		}
		if r == RoleDecides {
			t.Fatalf("%s is recorded as deciding sufficiency: that is a second authority", pkg)
		}
	}
}

// TestFindingAuthorityIsSingular is the second defect the reviewer
// found, held as a permanent rule.
func TestFindingAuthorityIsSingular(t *testing.T) {
	d, ok := AuthorityFor("Does a finding exist for this proof object?")
	if !ok {
		t.Fatal("finding creation has no audited authority")
	}
	if d.Authority != "veriqo/pkg/proof" || !strings.Contains(d.AuthorityEntryPoint, "NewFinding") {
		t.Fatalf("proof.NewFinding must be the sole finding authority, got %s / %s", d.Authority, d.AuthorityEntryPoint)
	}
	for _, p := range d.Participants {
		if p.Package == "veriqo/pkg/caseproofgraph" && p.Role != RoleCopies {
			t.Fatalf("caseproofgraph must only COPY a finding, got %q", p.Role)
		}
	}
	if !strings.Contains(d.WhyNotDuplicated, "materializes only") {
		t.Fatalf("the audit must record how the duplication was removed, got %q", d.WhyNotDuplicated)
	}
}

// TestNoPackageIsAuthorityAndParticipantForTheSameDecision catches an
// audit that lists a package twice to soften a finding.
func TestNoPackageIsAuthorityAndParticipantForTheSameDecision(t *testing.T) {
	for _, d := range DecisionAuthorities() {
		for _, p := range d.Participants {
			if p.Package == d.Authority {
				t.Fatalf("decision %q lists its authority %s as a participant too", d.Decision, p.Package)
			}
		}
	}
}

// TestEveryDecisionExplainsWhyItIsNotDuplicated: the explanation is the
// part a reviewer can attack, and a row without one asserts rather than
// argues.
func TestEveryDecisionExplainsWhyItIsNotDuplicated(t *testing.T) {
	for _, d := range DecisionAuthorities() {
		if len(strings.Fields(d.WhyNotDuplicated)) < 12 {
			t.Fatalf("decision %q explains itself too thinly: %q", d.Decision, d.WhyNotDuplicated)
		}
	}
}

// TestSequencingDecisionsAreAudited: the two the sequencing audit
// introduced must be in the table, or the audit lags the code.
func TestSequencingDecisionsAreAudited(t *testing.T) {
	for _, q := range []string{
		"May this case resolve?",
		"What level of proof has this conclusion reached?",
	} {
		if _, ok := AuthorityFor(q); !ok {
			t.Fatalf("decision %q is made by the system and is not audited", q)
		}
	}
}

func TestPackagesThatDecideIsSmall(t *testing.T) {
	byPkg := PackagesThatDecide()
	if len(byPkg) == 0 {
		t.Fatal("no package decides anything, which cannot be right")
	}
	// A package owning several decisions is fine. What would be wrong is
	// every package owning one, which would mean authority is diffuse.
	if len(byPkg) > len(DecisionAuthorities()) {
		t.Fatalf("%d packages for %d decisions: authority is diffuse", len(byPkg), len(DecisionAuthorities()))
	}
}

func TestAuthorityReportRendersRolesAndReasons(t *testing.T) {
	r := AuthorityReport()
	for _, role := range []Role{RoleDecides, RoleDerives, RoleCopies, RoleRecords} {
		if !strings.Contains(r, string(role)) {
			t.Fatalf("the report omits the %s role", role)
		}
	}
	if !strings.Contains(r, "WHY NOT DUPLICATED") {
		t.Fatal("the report must carry the justification, not just the table")
	}
}
