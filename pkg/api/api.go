// Package api is the VERIQO enterprise API surface.
//
// # What the specification requires of every endpoint
//
//	versioned, authenticated, authorized, audited, idempotent,
//	replayable
//
// Those are not six middleware layers to remember to apply. They are
// preconditions the router refuses to dispatch without, so an endpoint
// registered without one of them does not serve traffic -- it fails at
// registration, in a build, rather than in production with a gap.
//
// # Why this package has no HTTP in it
//
// The transport is a detail and the guarantees are not. Keeping the
// contract free of net/http means the same registration rules apply to
// an HTTP handler, a gRPC method or an internal call, and that a test
// of the rules does not need a server.
//
// # Errors carry a refusal or a failure, never both
//
// An API that returns 500 for a policy denial teaches its callers to
// retry a refusal. The response type carries the four-valued outcome,
// and the transport maps it -- rather than each handler choosing.
package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/audit"
	"veriqo/pkg/authority"
	"veriqo/pkg/contract"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/identity"
	"veriqo/pkg/policy"
	"veriqo/pkg/resilience"
)

var (
	ErrUnversioned   = errors.New("api: an endpoint must declare its version")
	ErrNoCapability  = errors.New("api: an endpoint must declare the capability it requires")
	ErrNotIdempotent = errors.New("api: a state-changing endpoint must be idempotent")
	ErrNotAudited    = errors.New("api: an endpoint must declare its audit action")
	ErrNotReplayable = errors.New("api: a state-changing endpoint must be replayable")
	ErrUnknownRoute  = errors.New("api: unknown route")
	ErrDuplicate     = errors.New("api: duplicate route")
	ErrNoKey         = errors.New("api: a state-changing request must carry an idempotency key")
	ErrNoPurpose     = errors.New("api: a request must declare its purpose")
)

// Version is the API version. It is in the route, not a header,
// because a version a caller can forget to send is a version that
// defaults to whatever the server currently is.
const Version = "v1"

// Method is the kind of operation.
type Method string

const (
	Get    Method = "GET"
	List   Method = "LIST"
	Create Method = "CREATE"
	Update Method = "UPDATE"
	Action Method = "ACTION"
)

// ChangesState reports whether a method may alter anything. GET and
// LIST do not; everything else does, and the extra requirements
// follow from that rather than from a per-route flag somebody sets.
func (m Method) ChangesState() bool { return m == Create || m == Update || m == Action }

func (m Method) Valid() bool {
	switch m {
	case Get, List, Create, Update, Action:
		return true
	}
	return false
}

// Endpoint is a registered operation with its guarantees declared.
type Endpoint struct {
	// Resource is the path segment: evidence, entities, claims...
	Resource string
	Method   Method
	// Operation names the action within the resource.
	Operation string

	// Capability is what the caller must hold.
	Capability authority.Capability
	// AuditAction is the action name written to the ledger.
	AuditAction string
	// Severity is the audit severity.
	Severity audit.Severity

	// Purposes limits which declared purposes may reach this
	// endpoint. Empty means every purpose, and Validate refuses that
	// for a state-changing endpoint: unrestricted mutation is a
	// decision, not a default.
	Purposes []policy.Purpose

	// Replayable marks an endpoint whose effect can be reconstructed.
	// Required for anything that changes state.
	Replayable bool

	// RateLimitPerSecond and Concurrency bound the endpoint. Zero
	// means unbounded, which Validate refuses.
	RateLimitPerSecond float64
	Burst              int
	Concurrency        int

	// MinClassification is the classification FLOOR the endpoint
	// serves: the least sensitive marking anything reachable through
	// it can carry.
	//
	// It is required, and it is a floor rather than the answer: the
	// handler holds the actual object and its marking may be stricter.
	// Declaring it means the authorisation decision has a
	// classification to work with BEFORE the object is fetched, which
	// is the only point at which a caller can be turned away without
	// the material having been read.
	MinClassification classification.Marking
}

// Route is the endpoint's addressable name.
func (e Endpoint) Route() string {
	return fmt.Sprintf("/%s/%s/%s", Version, e.Resource, e.Operation)
}

// Validate enforces the six guarantees.
func (e Endpoint) Validate() error {
	if strings.TrimSpace(e.Resource) == "" || strings.TrimSpace(e.Operation) == "" {
		return errors.New("api: an endpoint needs a resource and an operation")
	}
	if !e.Method.Valid() {
		return fmt.Errorf("api: unknown method %q on %s", e.Method, e.Route())
	}
	if !e.Capability.Valid() {
		return fmt.Errorf("%w: %s", ErrNoCapability, e.Route())
	}
	if strings.TrimSpace(e.AuditAction) == "" {
		return fmt.Errorf("%w: %s", ErrNotAudited, e.Route())
	}
	if !e.Severity.Valid() {
		return fmt.Errorf("api: %s has unknown audit severity %q", e.Route(), e.Severity)
	}
	if !e.MinClassification.Valid() {
		return fmt.Errorf("api: %s declares no classification floor, so a caller cannot be "+
			"turned away before the material is read", e.Route())
	}
	if e.RateLimitPerSecond <= 0 || e.Burst <= 0 || e.Concurrency <= 0 {
		return fmt.Errorf("api: %s declares no rate limit or concurrency bound; an "+
			"unbounded endpoint is a denial-of-service surface", e.Route())
	}
	if e.Method.ChangesState() {
		if !e.Replayable {
			return fmt.Errorf("%w: %s", ErrNotReplayable, e.Route())
		}
		if len(e.Purposes) == 0 {
			return fmt.Errorf("api: %s changes state and permits every purpose; "+
				"unrestricted mutation is a decision, not a default", e.Route())
		}
		// A state-changing endpoint whose capability is only VIEW is a
		// mis-declaration that would let a reader mutate.
		if e.Capability == authority.View {
			return fmt.Errorf("%w: %s changes state and requires only VIEW",
				ErrNoCapability, e.Route())
		}
	}
	for _, p := range e.Purposes {
		if !p.Valid() {
			return fmt.Errorf("api: %s names unknown purpose %q", e.Route(), p)
		}
	}
	return nil
}

// Request is an inbound call.
type Request struct {
	Route     string
	Principal identity.Principal
	Grants    []authority.Grant
	Purpose   policy.Purpose
	TenantID  string
	CaseID    string

	// IdempotencyKey is required for state-changing calls.
	IdempotencyKey string

	// Args carry the operation's parameters.
	Args map[string]string

	At    time.Time
	Trace string
}

// Response is the outcome.
type Response struct {
	Outcome contract.Outcome `json:"outcome"`
	// Reason explains a refusal or a failure.
	Reason string `json:"reason,omitempty"`
	// Rule names the policy rule for a refusal, so a caller can act.
	Rule string `json:"rule,omitempty"`
	// Body is the operation's result.
	Body any `json:"body,omitempty"`
	// ReplayReference is how to reconstruct this call.
	ReplayReference string `json:"replay_reference,omitempty"`
	// Obligations the caller must honour.
	Obligations []policy.Obligation `json:"obligations,omitempty"`
	Trace       string              `json:"trace"`
}

// Retryable reports whether a caller should try again.
//
// A refusal is NOT retryable: retrying a policy denial produces
// another denial and a second audit record. Only a genuine failure is,
// and even then the idempotency key makes the retry safe.
func (r Response) Retryable() bool { return r.Outcome == contract.Failed }

// Handler performs the work.
type Handler func(Request) (body any, outcome contract.Outcome, err error)

// Router dispatches to registered endpoints.
type Router struct {
	endpoints map[string]Endpoint
	handlers  map[string]Handler
	limiters  map[string]*resilience.Limiter
	bulkheads map[string]*resilience.Bulkhead
	idem      *resilience.Idempotency
	engine    *policy.Engine
	clock     contract.Clock
}

// NewRouter builds a router.
func NewRouter(engine *policy.Engine, clock contract.Clock, idem *resilience.Idempotency) (*Router, error) {
	if engine == nil {
		return nil, errors.New("api: no policy engine; an unauthorised router serves anything")
	}
	if clock == nil {
		return nil, errors.New("api: no clock")
	}
	if idem == nil {
		return nil, errors.New("api: no idempotency register; a retry would perform the " +
			"work twice")
	}
	return &Router{endpoints: map[string]Endpoint{}, handlers: map[string]Handler{},
		limiters:  map[string]*resilience.Limiter{},
		bulkheads: map[string]*resilience.Bulkhead{},
		idem:      idem, engine: engine, clock: clock}, nil
}

// Register adds an endpoint. It refuses one that does not declare its
// guarantees -- in a build, rather than in production with a gap.
func (r *Router) Register(e Endpoint, h Handler) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if h == nil {
		return fmt.Errorf("api: %s has no handler", e.Route())
	}
	route := e.Route()
	if _, dup := r.endpoints[route]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicate, route)
	}
	l, err := resilience.NewLimiter(route, e.RateLimitPerSecond, e.Burst, r.clock)
	if err != nil {
		return err
	}
	b, err := resilience.NewBulkhead(route, e.Concurrency)
	if err != nil {
		return err
	}
	r.endpoints[route] = e
	r.handlers[route] = h
	r.limiters[route] = l
	r.bulkheads[route] = b
	return nil
}

// Routes returns every registered route, sorted.
func (r *Router) Routes() []string {
	out := make([]string, 0, len(r.endpoints))
	for k := range r.endpoints {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Endpoint returns a registered endpoint.
func (r *Router) Endpoint(route string) (Endpoint, error) {
	e, ok := r.endpoints[route]
	if !ok {
		return Endpoint{}, fmt.Errorf("%w: %s", ErrUnknownRoute, route)
	}
	return e, nil
}

// Dispatch runs a request through every guarantee in order.
//
// The order matters and is the content of the method:
//
//	route -> purpose -> idempotency -> rate -> concurrency
//	      -> policy -> handler
//
// Policy comes AFTER the resilience checks so that a flood of
// unauthorised requests is shed before it reaches the authorisation
// path, and BEFORE the handler so nothing runs unauthorised.
func (r *Router) Dispatch(req Request) Response {
	resp := Response{Trace: req.Trace, Outcome: contract.Refused}

	e, err := r.Endpoint(req.Route)
	if err != nil {
		resp.Reason = err.Error()
		resp.Rule = "api/unknown-route"
		return resp
	}
	if !req.Purpose.Valid() {
		resp.Reason = ErrNoPurpose.Error()
		resp.Rule = "api/purpose-required"
		return resp
	}
	if len(e.Purposes) > 0 && !containsPurpose(e.Purposes, req.Purpose) {
		resp.Reason = fmt.Sprintf("%s does not serve purpose %s", e.Route(), req.Purpose)
		resp.Rule = "api/purpose-not-served"
		return resp
	}
	if req.At.IsZero() {
		resp.Reason = "the request carries no instant, so the call cannot be replayed"
		resp.Rule = "api/instant-required"
		return resp
	}

	// Idempotency, before any work.
	var idemKey string
	if e.Method.ChangesState() {
		if strings.TrimSpace(req.IdempotencyKey) == "" {
			resp.Reason = ErrNoKey.Error()
			resp.Rule = "api/idempotency-key-required"
			return resp
		}
		idemKey = e.Route() + "#" + req.TenantID + "#" + req.IdempotencyKey
		prior, err := r.idem.Begin(idemKey)
		if err != nil {
			resp.Reason = err.Error()
			resp.Rule = "api/idempotent-replay"
			if errors.Is(err, resilience.ErrDuplicate) {
				// The original result, not a second execution.
				resp.Outcome = contract.Succeeded
				resp.Body = prior.ResultHash
				resp.Reason = "this operation already completed; the original result is returned"
			}
			return resp
		}
	}

	// Resilience.
	if err := r.limiters[e.Route()].Allow(); err != nil {
		r.abandon(e, idemKey)
		resp.Reason = err.Error()
		resp.Rule = "api/rate-limit"
		return resp
	}
	release, err := r.bulkheads[e.Route()].Acquire()
	if err != nil {
		r.abandon(e, idemKey)
		resp.Reason = err.Error()
		resp.Rule = "api/concurrency"
		return resp
	}
	defer release()

	// Authorisation.
	decision := r.engine.Decide(policy.Request{
		Principal: req.Principal, Grants: req.Grants,
		Action: e.Capability, Purpose: req.Purpose,
		TenantID: req.TenantID, CaseID: req.CaseID,
		ObjectType: e.Resource, At: req.At,
		Classification: e.MinClassification,
		Attributes:     req.Args,
	})
	if !decision.Permitted() {
		r.abandon(e, idemKey)
		resp.Reason = decision.Reason
		resp.Rule = decision.Rule
		return resp
	}
	resp.Obligations = decision.Obligations

	// The work.
	body, outcome, herr := r.handlers[e.Route()](req)
	resp.Outcome = outcome
	resp.Body = body
	if herr != nil {
		resp.Reason = herr.Error()
		if outcome == contract.Succeeded {
			resp.Outcome = contract.Failed
		}
	}
	if e.Method.ChangesState() {
		if resp.Outcome == contract.Succeeded {
			_ = r.idem.Complete(idemKey, fmt.Sprint(body))
		} else {
			r.idem.Abandon(idemKey)
		}
		resp.ReplayReference = fmt.Sprintf("%s@%s/%s", e.Route(),
			req.At.UTC().Format(time.RFC3339), req.IdempotencyKey)
	}
	return resp
}

func (r *Router) abandon(e Endpoint, key string) {
	if e.Method.ChangesState() && key != "" {
		r.idem.Abandon(key)
	}
}

func containsPurpose(ps []policy.Purpose, p policy.Purpose) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

// Describe renders the surface for documentation.
func (r *Router) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "VERIQO API %s\n", Version)
	for _, route := range r.Routes() {
		e := r.endpoints[route]
		state := "read"
		if e.Method.ChangesState() {
			state = "state-changing, idempotent, replayable"
		}
		fmt.Fprintf(&b, "  %-6s %-40s %-10s %s\n", e.Method, route, e.Capability, state)
		fmt.Fprintf(&b, "         audit %s (%s), %.0f/s burst %d, concurrency %d\n",
			e.AuditAction, e.Severity, e.RateLimitPerSecond, e.Burst, e.Concurrency)
	}
	b.WriteString("  Every endpoint is versioned in its path, authenticated, authorized, " +
		"audited and rate-bounded; every state-changing endpoint is idempotent and " +
		"replayable. An endpoint missing any of these fails registration.\n")
	return b.String()
}
