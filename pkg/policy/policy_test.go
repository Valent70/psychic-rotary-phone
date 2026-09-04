package policy

import (
	"strings"
	"testing"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/identity"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func engine(t *testing.T, extra ...Rule) *Engine {
	t.Helper()
	e, err := New(contract.Version{Component: "baseline", Revision: 1},
		append(Baseline(), extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func req(action authority.Capability, role authority.Role) Request {
	p := identity.Principal{
		ID: "human:analyst-1", Kind: identity.Human, TenantID: "t-acme",
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
	}
	return Request{
		Principal:      p,
		Grants:         []authority.Grant{{Principal: p.ID, Role: role, TenantID: "t-acme"}},
		Action:         action,
		Purpose:        CaseInvestigation,
		TenantID:       "t-acme",
		CaseID:         "case-1",
		ObjectType:     "Evidence",
		Classification: classification.MustNew(classification.Internal),
		At:             now,
		Attributes:     map[string]string{"clearance": "SECRET"},
	}
}

// TestAnEmptyPolicySetIsRefusedAtConstruction. Denying silently at
// every call would look like a working system with a permissions bug.
func TestAnEmptyPolicySetIsRefusedAtConstruction(t *testing.T) {
	if _, err := New(contract.Version{Component: "x", Revision: 1}); err == nil {
		t.Fatal("an empty policy set was accepted")
	}
}

func TestAnUnversionedPolicySetIsRefused(t *testing.T) {
	if _, err := New(contract.Version{}, Baseline()...); err == nil {
		t.Fatal("an unversioned policy set was accepted; its decisions could not be replayed")
	}
}

// TestNoPurposeIsADenial. Purpose limitation cannot be optional.
func TestNoPurposeIsADenial(t *testing.T) {
	e := engine(t)
	r := req(authority.View, authority.Analyst)
	r.Purpose = ""
	d := e.Decide(r)
	if d.Permitted() {
		t.Fatal("a request with no declared purpose was permitted")
	}
	if !strings.Contains(d.Reason, "purpose") {
		t.Fatalf("the denial did not name the reason: %q", d.Reason)
	}
}

// TestNoInstantIsADenial: a decision made against the wall clock
// cannot be replayed.
func TestNoInstantIsADenial(t *testing.T) {
	r := req(authority.View, authority.Analyst)
	r.At = time.Time{}
	if engine(t).Decide(r).Permitted() {
		t.Fatal("a request with no decision instant was permitted")
	}
}

// TestNothingApplicableIsADenial: the combining algorithm's default.
func TestNothingApplicableIsADenial(t *testing.T) {
	e, err := New(contract.Version{Component: "narrow", Revision: 1}, Rule{
		Name:     "never",
		Applies:  func(Request) bool { return false },
		Evaluate: func(Request) Decision { return Decision{Effect: Permit} },
	})
	if err != nil {
		t.Fatal(err)
	}
	d := e.Decide(req(authority.View, authority.Analyst))
	if d.Permitted() {
		t.Fatal("a request no rule addressed was permitted")
	}
	if d.Rule != "core/default-deny" {
		t.Fatalf("denial attributed to %q", d.Rule)
	}
}

// TestDenyOverridesPermit. A single DENY ends the evaluation whatever
// else permits, and the order of the rules must not change that.
func TestDenyOverridesPermit(t *testing.T) {
	deny := Rule{
		Name:    "test/deny-everything",
		Applies: func(Request) bool { return true },
		Evaluate: func(Request) Decision {
			return Decision{Effect: Deny, Reason: "test"}
		},
	}
	e := engine(t, deny)
	d := e.Decide(req(authority.View, authority.Analyst))
	if d.Permitted() {
		t.Fatal("a DENY was overridden by an earlier PERMIT")
	}
	if d.Rule != "test/deny-everything" {
		t.Fatalf("the denying rule was not named: %q", d.Rule)
	}
}

// --- The core checks a rule set cannot override -------------------------

// TestARuleCannotPermitAcrossTenants. This is the attack: a customer
// (or a mistake) adds a permissive rule, and tenant isolation is gone.
func TestARuleCannotPermitAcrossTenants(t *testing.T) {
	permitAll := Rule{
		Name:     "test/permit-everything",
		Applies:  func(Request) bool { return true },
		Evaluate: func(Request) Decision { return Decision{Effect: Permit} },
	}
	e := engine(t, permitAll)
	r := req(authority.View, authority.Analyst)
	r.TenantID = "t-beta" // resource in another tenant
	d := e.Decide(r)
	if d.Permitted() {
		t.Fatal("A PERMISSIVE RULE DEFEATED TENANT ISOLATION")
	}
	if d.Rule != "core/tenant-isolation" {
		t.Fatalf("denied by %q rather than the core isolation check", d.Rule)
	}
}

// TestARuleCannotBuyAnAgentAnApproval: Law 7 sits in core.
func TestARuleCannotBuyAnAgentAnApproval(t *testing.T) {
	permitAll := Rule{
		Name:     "test/permit-everything",
		Applies:  func(Request) bool { return true },
		Evaluate: func(Request) Decision { return Decision{Effect: Permit} },
	}
	e := engine(t, permitAll)
	r := req(authority.Approve, authority.CaseOwner)
	r.Principal.ID = "agent:research"
	r.Principal.Kind = identity.Agent
	r.Grants[0].Principal = "agent:research"
	d := e.Decide(r)
	if d.Permitted() {
		t.Fatal("A PERMISSIVE RULE BOUGHT AN AGENT AN APPROVAL")
	}
	if d.Rule != "core/authority" {
		t.Fatalf("denied by %q rather than the core authority check", d.Rule)
	}
}

// TestARuleCannotOverrideNoTraining.
func TestARuleCannotOverrideNoTraining(t *testing.T) {
	permitAll := Rule{
		Name:     "test/permit-everything",
		Applies:  func(Request) bool { return true },
		Evaluate: func(Request) Decision { return Decision{Effect: Permit} },
	}
	e := engine(t, permitAll)
	r := req(authority.View, authority.Analyst)
	r.Purpose = ModelTraining
	r.Classification = classification.MustNew(classification.Internal, classification.NoTraining)
	if e.Decide(r).Permitted() {
		t.Fatal("NO_TRAINING material was released for training")
	}
}

func TestARuleCannotOverrideNoExport(t *testing.T) {
	e := engine(t)
	r := req(authority.Export, authority.CaseOwner)
	r.Purpose = CustomerExport
	r.Classification = classification.MustNew(classification.Internal, classification.NoExport)
	if e.Decide(r).Permitted() {
		t.Fatal("NO_EXPORT material was exported")
	}
}

func TestAnExpiredCredentialIsDenied(t *testing.T) {
	e := engine(t)
	r := req(authority.View, authority.Analyst)
	r.At = now.Add(2 * time.Hour)
	r.Principal.NotAfter = now.Add(time.Hour)
	d := e.Decide(r)
	if d.Permitted() {
		t.Fatal("an expired credential was honoured")
	}
	if d.Rule != "core/credential-window" {
		t.Fatalf("denied by %q", d.Rule)
	}
}

// TestAGrantScopedToAnotherCaseDoesNotApply.
func TestAGrantScopedToAnotherCaseDoesNotApply(t *testing.T) {
	e := engine(t)
	r := req(authority.View, authority.Analyst)
	r.Grants[0].CaseID = "case-99"
	if e.Decide(r).Permitted() {
		t.Fatal("a grant for another case authorised this one")
	}
}

// --- Obligations --------------------------------------------------------

// TestAPermitCarriesItsObligations. A permit whose obligations are
// dropped is a denial that did not happen.
func TestAPermitCarriesItsObligations(t *testing.T) {
	e := engine(t)
	r := req(authority.Export, authority.CaseOwner)
	r.Purpose = CustomerExport
	r.Classification = classification.MustNew(classification.Restricted)
	d := e.Decide(r)
	if !d.Permitted() {
		t.Fatalf("a legitimate export was denied: %s / %s", d.Rule, d.Reason)
	}
	want := map[string]bool{
		ObligationWatermark:     false,
		ObligationRecordPurpose: false,
		ObligationAuditElevated: false,
	}
	for _, o := range d.Obligations {
		if _, ok := want[o.Kind]; ok {
			want[o.Kind] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("export permitted without the %s obligation", k)
		}
	}
}

// TestAnAgentProposalCarriesAHumanReviewObligation is Law 7 expressed
// as an obligation rather than a refusal: the agent may propose, and
// the proposal is not usable until somebody looks at it.
func TestAnAgentProposalCarriesAHumanReviewObligation(t *testing.T) {
	e := engine(t)
	r := req(authority.Propose, authority.AI)
	r.Principal.ID = "agent:research"
	r.Principal.Kind = identity.Agent
	r.Grants[0].Principal = "agent:research"
	r.Grants[0].Role = authority.AI
	d := e.Decide(r)
	if !d.Permitted() {
		t.Fatalf("an agent could not propose: %s / %s", d.Rule, d.Reason)
	}
	found := false
	for _, o := range d.Obligations {
		if o.Kind == ObligationHumanReview {
			found = true
		}
	}
	if !found {
		t.Fatal("an agent proposal was permitted with no human-review obligation")
	}
}

// TestObligationsAreDeterministic: two identical requests must produce
// byte-identical decisions, or the audit record depends on map order.
func TestObligationsAreDeterministic(t *testing.T) {
	e := engine(t)
	r := req(authority.Export, authority.CaseOwner)
	r.Purpose = CustomerExport
	r.Classification = classification.MustNew(classification.Restricted, classification.PersonalData)
	first := e.Decide(r)
	for i := 0; i < 50; i++ {
		got := e.Decide(r)
		if len(got.Obligations) != len(first.Obligations) {
			t.Fatalf("obligation count varies: %d vs %d", len(got.Obligations), len(first.Obligations))
		}
		for j := range got.Obligations {
			if got.Obligations[j] != first.Obligations[j] {
				t.Fatalf("obligation order varies at %d: %v vs %v",
					j, got.Obligations[j], first.Obligations[j])
			}
		}
	}
}

// --- Clearance ----------------------------------------------------------

// TestAPrincipalWithNoStatedClearanceGetsPublic. The dangerous default
// is to infer clearance from what was asked for.
func TestAPrincipalWithNoStatedClearanceGetsPublic(t *testing.T) {
	e := engine(t)
	r := req(authority.View, authority.Analyst)
	delete(r.Attributes, "clearance")
	r.Classification = classification.MustNew(classification.Restricted)
	if e.Decide(r).Permitted() {
		t.Fatal("an unstated clearance read RESTRICTED material")
	}
}

func TestPersonalDataNeedsTheCaveatAuthorisation(t *testing.T) {
	e := engine(t)
	r := req(authority.View, authority.Analyst)
	r.Classification = classification.MustNew(classification.Internal, classification.PersonalData)
	if e.Decide(r).Permitted() {
		t.Fatal("a SECRET clearance read PERSONAL_DATA without the caveat authorisation")
	}
	r.Attributes["clearance_caveats"] = "PERSONAL_DATA"
	d := e.Decide(r)
	if !d.Permitted() {
		t.Fatalf("an authorised reader was refused: %s / %s", d.Rule, d.Reason)
	}
	found := false
	for _, o := range d.Obligations {
		if o.Kind == ObligationRedactProperty {
			found = true
		}
	}
	if !found {
		t.Fatal("personal data released with no redaction obligation")
	}
}

// TestResidencyIsEnforcedWhenBothAttributesArePresent.
func TestResidencyIsEnforcedWhenBothAttributesArePresent(t *testing.T) {
	e := engine(t)
	r := req(authority.View, authority.Analyst)
	r.Attributes["data_residency"] = "eu-west"
	r.Attributes["processing_region"] = "us-east"
	d := e.Decide(r)
	if d.Permitted() {
		t.Fatal("material was processed outside its residency")
	}
	r.Attributes["processing_region"] = "eu-west"
	if !e.Decide(r).Permitted() {
		t.Fatal("an in-region request was denied")
	}
}

func TestEveryDenialNamesARule(t *testing.T) {
	e := engine(t)
	cases := []func(*Request){
		func(r *Request) { r.Purpose = "" },
		func(r *Request) { r.At = time.Time{} },
		func(r *Request) { r.TenantID = "t-beta" },
		func(r *Request) { r.Grants = nil },
		func(r *Request) { r.Principal.TenantID = "" },
	}
	for i, mutate := range cases {
		r := req(authority.View, authority.Analyst)
		mutate(&r)
		d := e.Decide(r)
		if d.Permitted() {
			t.Fatalf("case %d was permitted", i)
		}
		if d.Rule == "" || d.Reason == "" {
			t.Errorf("case %d denied with rule=%q reason=%q", i, d.Rule, d.Reason)
		}
	}
}

func TestPurposeEnumIsClosed(t *testing.T) {
	if Purpose("because I need it").Valid() {
		t.Fatal("free-text purpose accepted")
	}
	for _, p := range Purposes() {
		if !p.Valid() {
			t.Errorf("%s is listed but not valid", p)
		}
	}
}
