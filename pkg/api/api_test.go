package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/audit"
	"veriqo/pkg/authority"
	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/identity"
	"veriqo/pkg/policy"
	"veriqo/pkg/resilience"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

var t0 = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func router(t *testing.T) (*Router, *fixedClock) {
	t.Helper()
	c := &fixedClock{t: t0}
	e, err := policy.New(contract.Version{Component: "baseline", Revision: 1},
		policy.Baseline()...)
	if err != nil {
		t.Fatal(err)
	}
	idem, err := resilience.NewIdempotency(c, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRouter(e, c, idem)
	if err != nil {
		t.Fatal(err)
	}
	return r, c
}

func ok(Request) (any, contract.Outcome, error) { return "result", contract.Succeeded, nil }

func request(route string, cap authority.Capability, role authority.Role) Request {
	p := identity.Principal{ID: "human:analyst-1", Kind: identity.Human, TenantID: "t-acme",
		NotBefore: t0.Add(-time.Hour), NotAfter: t0.Add(time.Hour)}
	return Request{
		Route: route, Principal: p,
		Grants:   []authority.Grant{{Principal: p.ID, Role: role, TenantID: "t-acme"}},
		Purpose:  policy.CaseInvestigation,
		TenantID: "t-acme", CaseID: "case-1",
		IdempotencyKey: "key-1",
		Args: map[string]string{
			"clearance":         "SECRET",
			"clearance_caveats": "PERSONAL_DATA",
		},
		At: t0, Trace: "tr-1",
	}
}

// TestAnEndpointMissingAGuaranteeFailsRegistration.
//
// In a build, rather than in production with a gap.
func TestAnEndpointMissingAGuaranteeFailsRegistration(t *testing.T) {
	r, _ := router(t)
	base := Endpoint{Resource: "claims", Method: Create, Operation: "propose",
		Capability: authority.Propose, AuditAction: "claim.proposed",
		Severity: audit.Elevated, Purposes: []policy.Purpose{policy.CaseInvestigation},
		Replayable: true, RateLimitPerSecond: 10, Burst: 20, Concurrency: 8,
		MinClassification: classification.MustNew(classification.Internal)}

	cases := map[string]func(*Endpoint){
		"no capability":  func(e *Endpoint) { e.Capability = "" },
		"no audit":       func(e *Endpoint) { e.AuditAction = "" },
		"not replayable": func(e *Endpoint) { e.Replayable = false },
		"no purposes":    func(e *Endpoint) { e.Purposes = nil },
		"no rate limit":  func(e *Endpoint) { e.RateLimitPerSecond = 0 },
		"no concurrency": func(e *Endpoint) { e.Concurrency = 0 },
		"no classification floor": func(e *Endpoint) {
			e.MinClassification = classification.Marking{}
		},
		"view only": func(e *Endpoint) { e.Capability = authority.View },
	}
	for name, mutate := range cases {
		e := base
		mutate(&e)
		if err := r.Register(e, ok); err == nil {
			t.Errorf("an endpoint with %s was registered", name)
		}
	}
	if err := r.Register(base, ok); err != nil {
		t.Fatalf("a complete endpoint was refused: %v", err)
	}
	if err := r.Register(base, nil); err == nil {
		t.Fatal("an endpoint with no handler was registered")
	}
}

// TestEveryVeriqoEndpointDeclaresItsGuarantees.
func TestEveryVeriqoEndpointDeclaresItsGuarantees(t *testing.T) {
	r, _ := router(t)
	for _, e := range Endpoints() {
		if err := r.Register(e, ok); err != nil {
			t.Errorf("%s: %v", e.Route(), err)
		}
	}
	if len(r.Routes()) != len(Endpoints()) {
		t.Fatalf("%d routes registered of %d", len(r.Routes()), len(Endpoints()))
	}
	// The version is in the path, not a header a caller can forget.
	for _, route := range r.Routes() {
		if !strings.HasPrefix(route, "/"+Version+"/") {
			t.Errorf("%s is not versioned in its path", route)
		}
	}
}

// TestAStateChangingCallNeedsAnIdempotencyKey.
func TestAStateChangingCallNeedsAnIdempotencyKey(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "claims", "propose")
	r.Register(e, ok)

	req := request(e.Route(), e.Capability, authority.Analyst)
	req.IdempotencyKey = ""
	resp := r.Dispatch(req)
	if resp.Outcome != contract.Refused {
		t.Fatalf("a keyless mutation was accepted: %+v", resp)
	}
	if resp.Rule != "api/idempotency-key-required" {
		t.Fatalf("refused by %q", resp.Rule)
	}
}

// TestARetryReturnsTheOriginalResultRatherThanRunningTwice.
func TestARetryReturnsTheOriginalResultRatherThanRunningTwice(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "claims", "propose")

	runs := 0
	r.Register(e, func(Request) (any, contract.Outcome, error) {
		runs++
		return "claim:c1", contract.Succeeded, nil
	})

	req := request(e.Route(), e.Capability, authority.Analyst)
	first := r.Dispatch(req)
	if first.Outcome != contract.Succeeded {
		t.Fatalf("the first call failed: %+v", first)
	}
	second := r.Dispatch(req)
	if runs != 1 {
		t.Fatalf("THE HANDLER RAN %d TIMES FOR ONE IDEMPOTENCY KEY", runs)
	}
	if second.Outcome != contract.Succeeded {
		t.Fatalf("the retry did not return the original result: %+v", second)
	}
	if !strings.Contains(second.Reason, "already completed") {
		t.Fatalf("the retry does not say it is a replay: %q", second.Reason)
	}
}

// TestAFailedCallReleasesItsIdempotencyKey, so a genuine retry can
// proceed.
func TestAFailedCallReleasesItsIdempotencyKey(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "claims", "propose")
	fail := true
	runs := 0
	r.Register(e, func(Request) (any, contract.Outcome, error) {
		runs++
		if fail {
			return nil, contract.Failed, errors.New("the store was unavailable")
		}
		return "claim:c1", contract.Succeeded, nil
	})

	req := request(e.Route(), e.Capability, authority.Analyst)
	if resp := r.Dispatch(req); resp.Outcome != contract.Failed {
		t.Fatalf("the failure was not reported: %+v", resp)
	}
	fail = false
	if resp := r.Dispatch(req); resp.Outcome != contract.Succeeded {
		t.Fatalf("the retry after a failure was blocked: %+v", resp)
	}
	if runs != 2 {
		t.Fatalf("the handler ran %d times", runs)
	}
}

// TestARefusalIsNotRetryable.
//
// Retrying a policy denial produces another denial and a second audit
// record.
func TestARefusalIsNotRetryable(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "claims", "propose")
	r.Register(e, ok)

	req := request(e.Route(), e.Capability, authority.Analyst)
	req.TenantID = "t-beta" // cross-tenant
	resp := r.Dispatch(req)
	if resp.Outcome != contract.Refused {
		t.Fatalf("a cross-tenant call was not refused: %+v", resp)
	}
	if resp.Retryable() {
		t.Fatal("A REFUSAL WAS REPORTED AS RETRYABLE")
	}
	if resp.Rule == "" {
		t.Fatal("the refusal names no rule, so a caller cannot act on it")
	}
	// A genuine failure is retryable.
	failing := Response{Outcome: contract.Failed}
	if !failing.Retryable() {
		t.Fatal("a failure is not retryable")
	}
}

// TestTheHandlerDoesNotRunOnADenial.
func TestTheHandlerDoesNotRunOnADenial(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "claims", "propose")
	ran := false
	r.Register(e, func(Request) (any, contract.Outcome, error) {
		ran = true
		return nil, contract.Succeeded, nil
	})
	req := request(e.Route(), e.Capability, authority.Analyst)
	req.Grants = nil // no authority
	r.Dispatch(req)
	if ran {
		t.Fatal("THE HANDLER RAN WITHOUT AUTHORISATION")
	}
}

// TestAnEndpointServesOnlyItsDeclaredPurposes.
func TestAnEndpointServesOnlyItsDeclaredPurposes(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "cases", "export")
	r.Register(e, ok)

	req := request(e.Route(), e.Capability, authority.CaseOwner)
	req.Purpose = policy.ModelTraining
	resp := r.Dispatch(req)
	if resp.Outcome != contract.Refused {
		t.Fatalf("an export served a training purpose: %+v", resp)
	}
	if resp.Rule != "api/purpose-not-served" {
		t.Fatalf("refused by %q", resp.Rule)
	}
	req.Purpose = policy.CustomerExport
	if resp := r.Dispatch(req); resp.Outcome != contract.Succeeded {
		t.Fatalf("a declared purpose was refused: %+v", resp)
	}
}

// TestTheRateLimitShedsBeforeAuthorisation.
//
// A flood of unauthorised requests is shed before it reaches the
// authorisation path.
func TestTheRateLimitShedsBeforeAuthorisation(t *testing.T) {
	r, c := router(t)
	e := Endpoint{Resource: "claims", Method: Get, Operation: "get",
		Capability: authority.View, AuditAction: "claim.read", Severity: audit.Routine,
		MinClassification:  classification.MustNew(classification.Internal),
		RateLimitPerSecond: 1, Burst: 2, Concurrency: 4}
	r.Register(e, ok)

	req := request(e.Route(), e.Capability, authority.Analyst)
	for i := 0; i < 2; i++ {
		if resp := r.Dispatch(req); resp.Outcome != contract.Succeeded {
			t.Fatalf("burst call %d refused: %+v", i, resp)
		}
	}
	resp := r.Dispatch(req)
	if resp.Outcome != contract.Refused || resp.Rule != "api/rate-limit" {
		t.Fatalf("the rate limit did not apply: %+v", resp)
	}
	c.advance(2 * time.Second)
	if resp := r.Dispatch(req); resp.Outcome != contract.Succeeded {
		t.Fatalf("the bucket did not refill: %+v", resp)
	}
}

// TestAPermitCarriesItsObligationsToTheCaller.
func TestAPermitCarriesItsObligationsToTheCaller(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "cases", "export")
	r.Register(e, ok)
	req := request(e.Route(), e.Capability, authority.CaseOwner)
	req.Purpose = policy.CustomerExport
	resp := r.Dispatch(req)
	if resp.Outcome != contract.Succeeded {
		t.Fatalf("the export was refused: %+v", resp)
	}
	if len(resp.Obligations) == 0 {
		t.Fatal("an export was permitted with no obligations returned to the caller")
	}
	found := false
	for _, o := range resp.Obligations {
		if o.Kind == policy.ObligationWatermark {
			found = true
		}
	}
	if !found {
		t.Fatalf("the watermark obligation was dropped: %v", resp.Obligations)
	}
}

// TestAStateChangingCallReturnsAReplayReference.
func TestAStateChangingCallReturnsAReplayReference(t *testing.T) {
	r, _ := router(t)
	e := findEndpoint(t, "claims", "propose")
	r.Register(e, ok)
	resp := r.Dispatch(request(e.Route(), e.Capability, authority.Analyst))
	if resp.ReplayReference == "" {
		t.Fatal("a mutation returned no replay reference")
	}
	if !strings.Contains(resp.ReplayReference, "key-1") {
		t.Fatalf("the replay reference does not pin the request: %q", resp.ReplayReference)
	}
}

// TestNoEndpointPerformsATwoPartyActAlone.
//
// Approving a finding, resolving a case and merging entities all
// require separation of duties. The API exposes them as ACTIONs
// requiring APPROVE, never as a create that a proposer could call.
func TestNoEndpointPerformsATwoPartyActAlone(t *testing.T) {
	for _, e := range Endpoints() {
		switch e.Operation {
		case "approve", "resolve":
			if e.Capability != authority.Approve {
				t.Errorf("%s requires %s rather than APPROVE", e.Route(), e.Capability)
			}
			if e.Severity != audit.Security {
				t.Errorf("%s is audited at %s rather than SECURITY", e.Route(), e.Severity)
			}
		}
		// And there is no single endpoint that merges entities.
		if e.Resource == "resolutions" && e.Method.ChangesState() &&
			e.Operation != "propose" {
			t.Errorf("%s mutates resolutions outside a proposal", e.Route())
		}
	}
}

// TestAnUnknownRouteIsRefusedNotFailed.
func TestAnUnknownRouteIsRefusedNotFailed(t *testing.T) {
	r, _ := router(t)
	resp := r.Dispatch(request("/v1/nope/nope", authority.View, authority.Analyst))
	if resp.Outcome != contract.Refused {
		t.Fatalf("an unknown route returned %s", resp.Outcome)
	}
	if resp.Retryable() {
		t.Fatal("an unknown route was reported as retryable")
	}
}

// TestARouterWithoutItsDependenciesIsRefused.
func TestARouterWithoutItsDependenciesIsRefused(t *testing.T) {
	c := &fixedClock{t: t0}
	e, _ := policy.New(contract.Version{Component: "b", Revision: 1}, policy.Baseline()...)
	idem, _ := resilience.NewIdempotency(c, time.Minute)
	if _, err := NewRouter(nil, c, idem); err == nil {
		t.Fatal("a router with no policy engine was built")
	}
	if _, err := NewRouter(e, c, nil); err == nil {
		t.Fatal("a router with no idempotency register was built")
	}
	if _, err := NewRouter(e, nil, idem); err == nil {
		t.Fatal("a router with no clock was built")
	}
}

// TestTheSurfaceDescribesItsGuarantees.
func TestTheSurfaceDescribesItsGuarantees(t *testing.T) {
	r, _ := router(t)
	for _, e := range Endpoints() {
		r.Register(e, ok)
	}
	d := r.Describe()
	if !strings.Contains(d, "fails registration") {
		t.Fatalf("the description does not state the rule:\n%s", d)
	}
	if !strings.Contains(d, "state-changing, idempotent, replayable") {
		t.Fatalf("the description does not mark mutations:\n%s", d)
	}
}

func findEndpoint(t *testing.T, resource, op string) Endpoint {
	t.Helper()
	for _, e := range Endpoints() {
		if e.Resource == resource && e.Operation == op {
			return e
		}
	}
	t.Fatalf("no endpoint %s/%s", resource, op)
	return Endpoint{}
}
