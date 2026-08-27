package precedence

import (
	"fmt"

	"veriqo/pkg/authz"
	"veriqo/pkg/governance/hitl"
)

// FromAuthzEffect maps a pkg/authz policy Effect into the lattice.
// pkg/authz keeps its own ALLOW/DENY vocabulary and tests unchanged
// (NON-NEGOTIABLE RULE #1: integrate, don't rewrite); this is the
// single place that vocabulary is opted into the shared precedence.
func FromAuthzEffect(source string, e authz.Effect, reason string) (Signal, error) {
	switch e {
	case authz.Allow:
		return Signal{Source: source, Action: ActionAllow, Reason: reason}, nil
	case authz.Deny:
		return Signal{Source: source, Action: ActionDeny, Reason: reason}, nil
	default:
		return Signal{}, fmt.Errorf("%w: authz effect %q", ErrUnknownAction, e)
	}
}

// FromHITLAction maps a pkg/governance/hitl reviewer verdict into the
// lattice. A human REJECT stops the action without asserting the
// systemic DENY a machine policy would (it is reversible by a future
// review, a DENY conceptually is not), so it maps to BLOCK, one level
// under DENY; REQUEST_MORE_EVIDENCE and DEFER both mean "not resolved
// yet" and map to REVIEW.
func FromHITLAction(source string, a hitl.Action, reason string) (Signal, error) {
	switch a {
	case hitl.ActionApprove:
		return Signal{Source: source, Action: ActionAllow, Reason: reason}, nil
	case hitl.ActionReject:
		return Signal{Source: source, Action: ActionBlock, Reason: reason}, nil
	case hitl.ActionEscalate:
		return Signal{Source: source, Action: ActionEscalate, Reason: reason}, nil
	case hitl.ActionRequestMoreEvidence, hitl.ActionDefer:
		return Signal{Source: source, Action: ActionReview, Reason: reason}, nil
	default:
		return Signal{}, fmt.Errorf("%w: hitl action %q", ErrUnknownAction, a)
	}
}
