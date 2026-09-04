// Package audit turns operations into ledger records and carries the
// execution context that makes them reconstructable.
//
// # Why this is not "logging"
//
// A log line is written for a human reading it later. An audit record
// is written so a third party can reconstruct what happened without
// trusting the party that wrote it. The difference shows up in three
// places, and this package exists to hold all three:
//
//  1. the record is appended to a hash chain, so an entry cannot be
//     removed or edited without breaking the chain;
//  2. the record carries the VERSIONS the operation ran under, so
//     "what happened" includes "under which rules";
//  3. an operation that FAILS is recorded exactly as one that
//     succeeds. A system that only audits its successes has an audit
//     trail of its successes.
//
// # Context is mandatory, and the compiler is not enough
//
// The specification enumerates what every execution must carry:
// trace, tenant, case, workflow, agent, model version, policy version,
// evidence version. A struct with eight optional fields gets called
// with three of them filled in. So Context.Validate refuses the ones
// that are always required and States() reports what is missing --
// which is how the observability gate checks coverage rather than
// assuming it.
package audit

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
	"veriqo/pkg/ledger"
	"veriqo/pkg/policy"
)

var (
	ErrNoTrace     = errors.New("audit: no trace id; the operation cannot be correlated")
	ErrNoContext   = errors.New("audit: incomplete execution context")
	ErrNotRecorded = errors.New("audit: the operation was not recorded")
)

// Severity separates ordinary activity from things a security team
// must see. It is not a log level: it selects the retention and the
// alerting path.
type Severity string

const (
	Routine  Severity = "ROUTINE"
	Elevated Severity = "ELEVATED"
	Security Severity = "SECURITY"
)

func (s Severity) Valid() bool {
	switch s {
	case Routine, Elevated, Security:
		return true
	}
	return false
}

// Context is the execution context every audited operation carries.
type Context struct {
	TraceID  string `json:"trace_id"`
	TenantID string `json:"tenant_id"`
	CaseID   string `json:"case_id,omitempty"`

	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`

	Versions        contract.VersionSet `json:"versions"`
	EvidenceVersion string              `json:"evidence_version,omitempty"`
}

// Validate refuses a context that cannot support reconstruction.
func (c Context) Validate() error {
	if strings.TrimSpace(c.TraceID) == "" {
		return ErrNoTrace
	}
	if strings.TrimSpace(c.TenantID) == "" {
		return fmt.Errorf("%w: no tenant", ErrNoContext)
	}
	if !c.Versions.Complete() {
		return fmt.Errorf("%w: missing %v", contract.ErrUnversioned, c.Versions.Missing())
	}
	return nil
}

// Missing names the optional-but-expected fields that were not set.
//
// It exists so the observability gate can measure how often operations
// are recorded without a case or workflow, rather than everybody
// assuming they always are.
func (c Context) Missing() []string {
	var m []string
	if c.CaseID == "" {
		m = append(m, "case_id")
	}
	if c.WorkflowID == "" {
		m = append(m, "workflow_id")
	}
	if c.EvidenceVersion == "" {
		m = append(m, "evidence_version")
	}
	sort.Strings(m)
	return m
}

// Operation is what is being audited.
type Operation struct {
	Action   string
	Subject  string
	Purpose  policy.Purpose
	Decision policy.Decision
	Severity Severity

	// Reason is the human-supplied justification. It is required for
	// SECURITY-severity operations, because "why" with only an enum
	// value is not a reason anyone can assess.
	Reason string

	InputHash  string
	OutputHash string
}

// Recorder appends audited operations to a ledger.
type Recorder struct {
	l   *ledger.Ledger
	ctx Context
}

// NewRecorder binds a ledger to an execution context.
func NewRecorder(l *ledger.Ledger, ctx Context) (*Recorder, error) {
	if l == nil {
		return nil, errors.New("audit: no ledger")
	}
	if err := ctx.Validate(); err != nil {
		return nil, err
	}
	return &Recorder{l: l, ctx: ctx}, nil
}

// Context returns the bound context.
func (r *Recorder) Context() Context { return r.ctx }

// Record appends the operation and returns the ledger record.
//
// It takes the outcome explicitly rather than inferring it from an
// error, because REFUSED and FAILED are both non-success and only one
// of them is a defect -- and an inference here would collapse them.
func (r *Recorder) Record(p identity.Principal, op Operation, outcome contract.Outcome, at time.Time) (ledger.Record, error) {
	if !outcome.Valid() {
		return ledger.Record{}, fmt.Errorf("audit: outcome %q", outcome)
	}
	if !op.Severity.Valid() {
		return ledger.Record{}, fmt.Errorf("audit: severity %q", op.Severity)
	}
	if op.Severity == Security && strings.TrimSpace(op.Reason) == "" {
		return ledger.Record{}, fmt.Errorf("%w: a SECURITY-severity operation with no stated reason", ErrNoContext)
	}
	if p.TenantID != r.ctx.TenantID {
		return ledger.Record{}, fmt.Errorf("%w: principal is in %s, context is %s",
			contract.ErrCrossTenant, p.TenantID, r.ctx.TenantID)
	}

	e := ledger.Event{
		Actor:          p.ID,
		TenantID:       r.ctx.TenantID,
		Action:         op.Action,
		Subject:        op.Subject,
		At:             at,
		Purpose:        string(op.Purpose),
		Reason:         op.Reason,
		PolicyDecision: decisionString(op.Decision),
		Versions:       r.ctx.Versions,
		Outcome:        outcome,
		Result:         resultString(r.ctx, op),
		InputHash:      op.InputHash,
		OutputHash:     op.OutputHash,
	}
	if p.OnBehalfOf != nil {
		e.OnBehalf = string(*p.OnBehalfOf)
	}
	return r.l.Append(e)
}

func decisionString(d policy.Decision) string {
	if d.Effect == "" {
		return ""
	}
	s := string(d.Effect) + " " + d.Rule
	if len(d.Obligations) > 0 {
		var kinds []string
		for _, o := range d.Obligations {
			kinds = append(kinds, o.Kind)
		}
		sort.Strings(kinds)
		s += " [" + strings.Join(kinds, ",") + "]"
	}
	return s
}

func resultString(c Context, op Operation) string {
	parts := []string{"trace=" + c.TraceID, "severity=" + string(op.Severity)}
	if c.CaseID != "" {
		parts = append(parts, "case="+c.CaseID)
	}
	if c.WorkflowID != "" {
		parts = append(parts, "workflow="+c.WorkflowID)
	}
	if c.AgentID != "" {
		parts = append(parts, "agent="+c.AgentID)
	}
	if c.EvidenceVersion != "" {
		parts = append(parts, "evidence="+c.EvidenceVersion)
	}
	return strings.Join(parts, " ")
}

// Guard wraps an operation so that it CANNOT run without being
// recorded.
//
// This is the structural version of "audit everything". A call site
// that forgets to record is not a call site that records less: it is
// one that does not compile against this API, because the work only
// runs inside the closure Guard drives.
//
// The record is written for a failure and a refusal exactly as for a
// success. A system that audits only what worked has an audit trail of
// what worked.
func (r *Recorder) Guard(
	p identity.Principal,
	op Operation,
	at time.Time,
	work func() (result string, outcome contract.Outcome, err error),
) (ledger.Record, error) {
	if !op.Decision.Permitted() {
		rec, rerr := r.Record(p, op, contract.Refused, at)
		if rerr != nil {
			return ledger.Record{}, rerr
		}
		return rec, fmt.Errorf("%w: %s: %s", policy.ErrDenied, op.Decision.Rule, op.Decision.Reason)
	}

	res, outcome, workErr := work()
	if res != "" {
		op.OutputHash = res
	}
	if workErr != nil && outcome == contract.Succeeded {
		// A caller that returns an error alongside SUCCEEDED is
		// confused; recording it as a success would put the confusion
		// in the ledger.
		outcome = contract.Failed
	}
	rec, rerr := r.Record(p, op, outcome, at)
	if rerr != nil {
		return ledger.Record{}, fmt.Errorf("%w: %v (the operation itself returned %v)",
			ErrNotRecorded, rerr, workErr)
	}
	return rec, workErr
}
