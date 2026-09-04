package identity

import (
	"errors"
	"testing"
	"time"

	"veriqo/pkg/contract"
)

func at(h int) time.Time { return time.Date(2026, 9, 4, h, 0, 0, 0, time.UTC) }

func human(id string) Principal {
	return Principal{
		ID: contract.ID("human:" + id), Kind: Human, TenantID: "t-acme",
		NotBefore: at(9), NotAfter: at(17),
	}
}

func agent(id string) Principal {
	return Principal{
		ID: contract.ID("agent:" + id), Kind: Agent, TenantID: "t-acme",
		SPIFFE:    "spiffe://veriqo.internal/agent/" + id,
		NotBefore: at(8), NotAfter: at(23),
	}
}

// TestADelegationIntersectsTheWindow is the substantive rule. A naive
// implementation copies the agent's own window, and the agent then
// outlives the session that authorised it.
func TestADelegationIntersectsTheWindow(t *testing.T) {
	a, err := Delegate(human("analyst-1"), agent("research"))
	if err != nil {
		t.Fatal(err)
	}
	if !a.NotBefore.Equal(at(9)) {
		t.Fatalf("NotBefore %v: the delegation started before the delegator did", a.NotBefore)
	}
	if !a.NotAfter.Equal(at(17)) {
		t.Fatalf("NotAfter %v: the delegation outlives the delegator", a.NotAfter)
	}
	if a.OnBehalfOf == nil || *a.OnBehalfOf != contract.ID("human:analyst-1") {
		t.Fatal("the delegation does not record who it acts for")
	}
	// And the agent keeps its own identity: audit must not attribute
	// the agent's actions to the human.
	if a.ID != contract.ID("agent:research") {
		t.Fatalf("the agent lost its own identity: %s", a.ID)
	}
}

// TestADelegationCannotWiden is the same rule stated as a refusal: an
// unbounded agent credential does not become unbounded authority.
func TestADelegationCannotWiden(t *testing.T) {
	unbounded := agent("forever")
	unbounded.NotAfter = time.Time{}
	a, err := Delegate(human("analyst-1"), unbounded)
	if err != nil {
		t.Fatal(err)
	}
	if a.Unbounded() {
		t.Fatal("an unbounded agent credential survived delegation from a bounded human")
	}
	if !a.NotAfter.Equal(at(17)) {
		t.Fatalf("NotAfter %v, want the delegator's %v", a.NotAfter, at(17))
	}
}

func TestDelegationIsRefusedAcrossTenants(t *testing.T) {
	other := agent("research")
	other.TenantID = "t-other"
	_, err := Delegate(human("analyst-1"), other)
	if !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("cross-tenant delegation error = %v, want ErrCrossTenant", err)
	}
}

func TestSelfDelegationIsRefused(t *testing.T) {
	h := human("analyst-1")
	if _, err := Delegate(h, h); !errors.Is(err, ErrSelfDelegation) {
		t.Fatalf("self-delegation error = %v", err)
	}
}

func TestDisjointWindowsProduceNoDelegation(t *testing.T) {
	night := agent("night")
	night.NotBefore = at(18)
	night.NotAfter = at(23)
	if _, err := Delegate(human("analyst-1"), night); err == nil {
		t.Fatal("a delegation with an empty intersected window was accepted")
	}
}

// TestAnExpiredCredentialIsNotAPrincipal.
func TestAnExpiredCredentialIsNotAPrincipal(t *testing.T) {
	h := human("analyst-1")
	if err := h.Active(at(12)); err != nil {
		t.Fatalf("live credential rejected: %v", err)
	}
	if err := h.Active(at(18)); !errors.Is(err, ErrCredentialStale) {
		t.Fatalf("expired credential accepted at 18:00: %v", err)
	}
	if err := h.Active(at(8)); !errors.Is(err, ErrCredentialStale) {
		t.Fatalf("not-yet-valid credential accepted at 08:00: %v", err)
	}
	// The boundary: NotAfter is exclusive.
	if err := h.Active(at(17)); err == nil {
		t.Fatal("NotAfter treated as inclusive")
	}
}

// TestValidityIsSeparateFromWellFormedness. A historical audit record
// names principals whose credentials have long since expired, and it
// must stay readable.
func TestValidityIsSeparateFromWellFormedness(t *testing.T) {
	h := human("analyst-1")
	if err := h.Validate(); err != nil {
		t.Fatalf("well-formed principal rejected: %v", err)
	}
	if err := h.Active(at(23)); err == nil {
		t.Fatal("premise changed: this principal should be expired at 23:00")
	}
	// Validate still passes; only Active fails. If these merged, an
	// audit trail would become unreadable as its principals aged out.
	if err := h.Validate(); err != nil {
		t.Fatalf("an expired principal became malformed: %v", err)
	}
}

func TestAPrincipalMustBeAnchoredToATenant(t *testing.T) {
	p := human("x")
	p.TenantID = ""
	if err := p.Validate(); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("untenanted principal accepted: %v", err)
	}
}

// TestOnlyAgentsRequireHumanReviewOfOutput pins Law 7 at the point it
// is decided, rather than leaving each caller to remember it.
func TestOnlyAgentsRequireHumanReviewOfOutput(t *testing.T) {
	if !Agent.RequiresHumanReviewOfOutput() {
		t.Fatal("agent output does not require human review; Law 7 is not enforced")
	}
	for _, k := range []Kind{Human, Service, Device, Connector, Source} {
		if k.RequiresHumanReviewOfOutput() {
			t.Fatalf("%s wrongly routed through the AI review rule", k)
		}
	}
}

func TestAutomatedKinds(t *testing.T) {
	if Human.IsAutomated() {
		t.Fatal("HUMAN classified as automated")
	}
	for _, k := range []Kind{Agent, Service, Connector, Source} {
		if !k.IsAutomated() {
			t.Fatalf("%s not classified as automated", k)
		}
	}
}

func TestSPIFFEIDValidation(t *testing.T) {
	good := []string{
		"spiffe://veriqo.internal/agent/research",
		"spiffe://example.org/ns/prod/sa/ingest",
	}
	for _, s := range good {
		if err := ValidateSPIFFEID(s); err != nil {
			t.Errorf("valid SPIFFE ID %q rejected: %v", s, err)
		}
	}
	bad := []string{
		"",
		"spiffe://veriqo.internal",           // no path
		"spiffe://veriqo.internal/",          // empty path segment
		"https://veriqo.internal/agent/x",    // wrong scheme
		"spiffe://VERIQO.internal/agent/x",   // uppercase trust domain
		"spiffe://veriqo.internal/agent/x#f", // fragment
		"spiffe://veriqo.internal/agent/x?q", // query
	}
	for _, s := range bad {
		if err := ValidateSPIFFEID(s); err == nil {
			t.Errorf("malformed SPIFFE ID %q accepted", s)
		}
	}
}

func TestTrustDomainExtraction(t *testing.T) {
	td, err := TrustDomain("spiffe://veriqo.internal/agent/research")
	if err != nil {
		t.Fatal(err)
	}
	if td != "veriqo.internal" {
		t.Fatalf("trust domain = %q", td)
	}
}

// TestSyntacticSPIFFEValidationIsNotAttestation is documentation held
// in place by a test: nothing in this package proves a workload holds
// the SVID it names. If a future change makes Validate() imply
// attestation, gate G7 would silently become satisfiable from inside.
func TestSyntacticSPIFFEValidationIsNotAttestation(t *testing.T) {
	imposter := Principal{
		ID: "service:imposter", Kind: Service, TenantID: "t-acme",
		SPIFFE: "spiffe://veriqo.internal/agent/some-other-workload",
	}
	if err := imposter.Validate(); err != nil {
		t.Fatalf("a syntactically valid SPIFFE ID was rejected: %v", err)
	}
	// The point: acceptance here means the string is well-formed and
	// nothing more. Attestation is G7 and lives outside this package.
}
