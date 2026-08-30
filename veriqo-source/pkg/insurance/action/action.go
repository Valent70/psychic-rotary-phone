// Package action closes the reviewer's item 17/G directly: "Setelah
// Decision dibuat, kita harus bertanya: Apa yang terjadi kalau Decision
// kemudian dipakai untuk action?" (After a Decision is made, we must
// ask: what happens when the Decision is then used for an action?),
// naming the required shape "Decision -> Action Authorization
// Boundary" with the explicit prohibition "Jangan sampai: trusted
// Decision -> arbitrary action" (a trusted Decision must never grant
// an arbitrary action) and the required fields: "decision authority,
// actor, policy, scope, expiry, permitted action, conditions, audit
// trail."
//
// A decision.Decision, on its own, authorizes NOTHING beyond its own
// existence -- there is no exported way to go from a Decision straight
// to "this may now be acted on." The only way to obtain a real,
// checkable permission to act is AuthorizeAction, which binds a
// SPECIFIC actor to a SPECIFIC, closed-vocabulary Action, within an
// explicit scope, under explicit conditions, expiring at an explicit
// tick -- never open-ended, never inferred, never "any action this
// Decision's Outcome might plausibly justify."
//
// This follows the identical sealed-type discipline
// cre.AuthorizedFinding and decision.Decision already use: every field
// of ActionAuthorization is unexported, so `action.ActionAuthorization{
// permittedAction: ActionApproveSettlement}` cannot be written from
// outside this package at all -- the only way in is AuthorizeAction's
// own gate.
package action

import (
	"errors"
	"fmt"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/insurance/decision"
)

// Action is the closed, small vocabulary of downstream actions a
// Decision may ever authorize -- deliberately NOT a caller-supplied
// free string, matching decision.Outcome's own discipline. The three
// named here are exactly the reviewer's own named examples (Approve
// claim -> Settlement; Insurance notification; Trade-finance action),
// generalised to categories rather than any specific vendor or
// counterparty system (see pkg/insurance/guardrails' whole-tree ban on
// hard-coded vendors).
type Action string

const (
	ActionApproveSettlement    Action = "APPROVE_SETTLEMENT"
	ActionSendNotification     Action = "SEND_NOTIFICATION"
	ActionInitiateTradeFinance Action = "INITIATE_TRADE_FINANCE_ACTION"
	ActionInitiateRecovery     Action = "INITIATE_RECOVERY_ACTION"
)

var knownActions = map[Action]bool{
	ActionApproveSettlement: true, ActionSendNotification: true,
	ActionInitiateTradeFinance: true, ActionInitiateRecovery: true,
}

// IsKnownAction reports whether a is one of the four modelled actions.
func IsKnownAction(a Action) bool { return knownActions[a] }

// ActionAuthorization is a Decision-bound, actor-bound, scope-bound,
// time-bound grant to perform exactly one Action. Every field is
// unexported; see this file's own package doc comment for why. Obtain
// one only via AuthorizeAction.
type ActionAuthorization struct {
	// decisionHash binds this authorization to the EXACT Decision that
	// justified it -- never re-derived, never caller-supplied. See
	// VerifyActionAuthorization: an ActionAuthorization minted against
	// Decision A can never be validated against Decision B, even if B
	// carries the identical Outcome.
	decisionHash string

	// actor is WHO may exercise this authorization -- the reviewer's
	// named "actor" field. An authorization with no actor authorizes no
	// one.
	actor string

	// policyRef names the specific policy/rule that permits this
	// action -- the reviewer's named "policy" field. Mirrors
	// cre.FindingInput.ContractBasis's own discipline: a reference to a
	// real, external, checkable authority, not a bare boolean.
	policyRef string

	// scope narrows what this authorization actually covers (e.g. a
	// specific ClaimID or CaseID) -- the reviewer's named "scope"
	// field. An authorization to ActionApproveSettlement for
	// CLM-1 must never be usable for CLM-2.
	scope string

	// permittedAction is the ONE Action this authorization grants --
	// the reviewer's named "permitted action" field. There is no way to
	// authorize "any action"; a separate ActionAuthorization is required
	// per action.
	permittedAction Action

	// conditions is the closed, ordered list of conditions this
	// authorization is subject to -- the reviewer's named "conditions"
	// field. Purely descriptive at this package's own boundary (this
	// package does not evaluate condition predicates), but permanently
	// recorded and hashed, so a condition cannot be silently dropped
	// between authorization and execution.
	conditions []string

	authorizedAt uint64
	// expiresAt is the tick after which this authorization is no
	// longer valid -- the reviewer's named "expiry" field. Strictly
	// required to be > authorizedAt (see AuthorizeAction): an
	// authorization that is already expired the moment it is minted is
	// refused outright, never silently accepted as a curiosity.
	expiresAt uint64

	// hash is this authorization's own canonical commitment -- a
	// deterministic, pure function of every field above, computed once
	// inside AuthorizeAction and never mutated afterward.
	hash string
}

var (
	// ErrDecisionNotAuthoritative is AuthorizeAction's refusal when d is
	// the zero decision.Decision -- i.e. it never actually came out of
	// decision.MakeDecision. This is the direct implementation of
	// "trusted Decision -> arbitrary action" being impossible from the
	// OTHER direction too: there is no Decision at all to found an
	// action authorization on.
	ErrDecisionNotAuthoritative = errors.New("action: cannot authorize an action from an unauthoritative (zero-value) Decision")
	ErrEmptyActor               = errors.New("action: actor must be non-empty")
	ErrEmptyPolicyRef           = errors.New("action: policyRef must be non-empty")
	ErrEmptyScope               = errors.New("action: scope must be non-empty")
	ErrUnknownAction            = errors.New("action: unknown Action")
	ErrAlreadyExpired           = errors.New("action: expiresAt must be strictly after authorizedAt -- an authorization that is already expired at the moment of authorization is refused, not silently accepted")
)

// actionHashInput is the exact, ordered set of fields canonicalized/
// hashed to produce ActionAuthorization.hash -- decoupled from
// ActionAuthorization's own internal layout for the same reason
// decision.decisionHashInput is a separate type from Decision.
type actionHashInput struct {
	DecisionHash    string   `json:"decision_hash"`
	Actor           string   `json:"actor"`
	PolicyRef       string   `json:"policy_ref"`
	Scope           string   `json:"scope"`
	PermittedAction string   `json:"permitted_action"`
	Conditions      []string `json:"conditions"`
	AuthorizedAt    uint64   `json:"authorized_at"`
	ExpiresAt       uint64   `json:"expires_at"`
}

// AuthorizeAction is the ONLY exported function in this package that
// can produce a populated ActionAuthorization. It requires d to be a
// genuine, authoritative Decision (not the zero value), actor and
// policyRef and scope to be non-empty, permittedAction to be a known
// value, and expiresAt to be strictly after tick (authorizedAt) --
// every one of the reviewer's named required fields is either a
// required, validated parameter or independently derived, never
// optional and never defaulted to "unrestricted."
//
// conditions is copied defensively (see cloneConditions) so a slice
// the caller mutates afterward can never reach back into the sealed
// authorization's own state.
func AuthorizeAction(d decision.Decision, actor, policyRef, scope string, permittedAction Action, conditions []string, tick, expiresAt uint64) (ActionAuthorization, error) {
	if d.IsZero() {
		return ActionAuthorization{}, ErrDecisionNotAuthoritative
	}
	if strings.TrimSpace(actor) == "" {
		return ActionAuthorization{}, ErrEmptyActor
	}
	if strings.TrimSpace(policyRef) == "" {
		return ActionAuthorization{}, ErrEmptyPolicyRef
	}
	if strings.TrimSpace(scope) == "" {
		return ActionAuthorization{}, ErrEmptyScope
	}
	if !IsKnownAction(permittedAction) {
		return ActionAuthorization{}, fmt.Errorf("%w: %q", ErrUnknownAction, permittedAction)
	}
	if expiresAt <= tick {
		return ActionAuthorization{}, fmt.Errorf("%w: authorizedAt=%d expiresAt=%d", ErrAlreadyExpired, tick, expiresAt)
	}

	aa := ActionAuthorization{
		decisionHash: d.Hash(), actor: actor, policyRef: policyRef, scope: scope,
		permittedAction: permittedAction, conditions: cloneConditions(conditions),
		authorizedAt: tick, expiresAt: expiresAt,
	}
	aa.hash = jcs.MustHash(actionHashInput{
		DecisionHash: aa.decisionHash, Actor: aa.actor, PolicyRef: aa.policyRef, Scope: aa.scope,
		PermittedAction: string(aa.permittedAction), Conditions: aa.conditions,
		AuthorizedAt: aa.authorizedAt, ExpiresAt: aa.expiresAt,
	})
	return aa, nil
}

func cloneConditions(c []string) []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c))
	copy(out, c)
	return out
}

// DecisionHash / Actor / PolicyRef / Scope / PermittedAction /
// AuthorizedAt / ExpiresAt / Hash are ActionAuthorization's read-only
// accessors. Conditions returns a defensive copy -- mutating the
// result can never reach the sealed original, mirroring
// cre.AuthorizedFinding.Finding()'s own discipline.
func (aa ActionAuthorization) DecisionHash() string    { return aa.decisionHash }
func (aa ActionAuthorization) Actor() string           { return aa.actor }
func (aa ActionAuthorization) PolicyRef() string       { return aa.policyRef }
func (aa ActionAuthorization) Scope() string           { return aa.scope }
func (aa ActionAuthorization) PermittedAction() Action { return aa.permittedAction }
func (aa ActionAuthorization) Conditions() []string    { return cloneConditions(aa.conditions) }
func (aa ActionAuthorization) AuthorizedAt() uint64    { return aa.authorizedAt }
func (aa ActionAuthorization) ExpiresAt() uint64       { return aa.expiresAt }
func (aa ActionAuthorization) Hash() string            { return aa.hash }

// IsZero reports whether aa is the unpopulated zero value -- the only
// value obtainable outside this package without calling AuthorizeAction.
func (aa ActionAuthorization) IsZero() bool { return aa.hash == "" }

var (
	// ErrActionHashMismatch is VerifyActionAuthorization's refusal when
	// aa's own recorded hash does not match what its own recorded
	// fields recompute to -- proof of post-authorization tampering,
	// mirroring decision.ErrDecisionHashMismatch.
	ErrActionHashMismatch = errors.New("action: recorded hash does not match recomputed hash")

	// ErrActionDecisionMismatch is VerifyActionAuthorization's and
	// AuthorizeExecution's refusal when aa's cited DecisionHash does not
	// match the Decision it is checked against -- an authorization
	// minted for Decision A is never valid against Decision B.
	ErrActionDecisionMismatch = errors.New("action: ActionAuthorization's cited DecisionHash does not match the given Decision")
)

// VerifyActionAuthorization re-derives aa's own hash from its own
// recorded fields (catching tampering) AND confirms aa's cited
// DecisionHash actually matches d's real Hash (catching an
// authorization that claims a Decision it was never actually minted
// from) -- the same re-verify-by-recomputing discipline
// decision.VerifyDecisionProvenance already uses one layer up.
func VerifyActionAuthorization(aa ActionAuthorization, d decision.Decision) error {
	if aa.IsZero() {
		return fmt.Errorf("%w: ActionAuthorization is the zero value", ErrActionHashMismatch)
	}
	want := jcs.MustHash(actionHashInput{
		DecisionHash: aa.decisionHash, Actor: aa.actor, PolicyRef: aa.policyRef, Scope: aa.scope,
		PermittedAction: string(aa.permittedAction), Conditions: aa.conditions,
		AuthorizedAt: aa.authorizedAt, ExpiresAt: aa.expiresAt,
	})
	if want != aa.hash {
		return fmt.Errorf("%w: recorded=%s recomputed=%s", ErrActionHashMismatch, aa.hash, want)
	}
	if d.IsZero() {
		return fmt.Errorf("%w: given Decision is the zero value", ErrActionDecisionMismatch)
	}
	if aa.decisionHash != d.Hash() {
		return fmt.Errorf("%w: DecisionHash %s does not match the given Decision's real Hash %s", ErrActionDecisionMismatch, aa.decisionHash, d.Hash())
	}
	return nil
}

var (
	// ErrActionAuthorizationExpired is AuthorizeExecution's refusal when
	// tick has passed aa's own expiresAt -- a structurally valid
	// authorization is still refused once its expiry has passed; the
	// grant is time-bound, not permanent.
	ErrActionAuthorizationExpired = errors.New("action: ActionAuthorization has expired")

	// ErrActionMismatch is AuthorizeExecution's refusal when the action
	// actually being attempted does not match aa.permittedAction -- an
	// authorization minted for ActionSendNotification can never be used
	// to justify ActionApproveSettlement, even against the identical
	// Decision, actor and scope.
	ErrActionMismatch = errors.New("action: attempted action does not match the ActionAuthorization's own permitted action")

	// ErrActorMismatch is AuthorizeExecution's refusal when the actor
	// attempting execution is not the actor this authorization was
	// granted to.
	ErrActorMismatch = errors.New("action: attempting actor does not match the ActionAuthorization's own actor")

	// ErrScopeMismatch is AuthorizeExecution's refusal when the scope
	// being acted on does not match aa.scope -- an authorization scoped
	// to one claim can never justify acting on a different one.
	ErrScopeMismatch = errors.New("action: attempted scope does not match the ActionAuthorization's own scope")
)

// AuthorizeExecution is the ONE gate a downstream action-executor
// (settlement processor, notification dispatcher, trade-finance
// initiator, ...) must call before actually performing attemptedAction
// against attemptedScope as attemptingActor. It is deliberately
// narrow: it does not perform the action itself (this package has no
// opinion on HOW a settlement or notification is carried out -- see
// this package's own doc comment on why real integrations are P2/
// external), it only answers "is this specific actor, right now,
// permitted to take this specific action within this specific scope,
// under this specific authorization" -- the last gate before "trusted
// Decision -> arbitrary action" would otherwise become possible.
//
// d is required (not merely aa) so this function independently
// re-confirms aa is bound to a real, non-zero, currently-in-hand
// Decision -- a caller cannot satisfy this gate with an
// ActionAuthorization alone, divorced from the Decision that supposedly
// justified it.
func AuthorizeExecution(aa ActionAuthorization, d decision.Decision, attemptingActor string, attemptedAction Action, attemptedScope string, tick uint64) error {
	if err := VerifyActionAuthorization(aa, d); err != nil {
		return err
	}
	if tick >= aa.expiresAt {
		return fmt.Errorf("%w: expired at tick=%d, attempted at tick=%d", ErrActionAuthorizationExpired, aa.expiresAt, tick)
	}
	if attemptingActor != aa.actor {
		return fmt.Errorf("%w: authorized actor=%q attempted actor=%q", ErrActorMismatch, aa.actor, attemptingActor)
	}
	if attemptedAction != aa.permittedAction {
		return fmt.Errorf("%w: authorized action=%q attempted action=%q", ErrActionMismatch, aa.permittedAction, attemptedAction)
	}
	if attemptedScope != aa.scope {
		return fmt.Errorf("%w: authorized scope=%q attempted scope=%q", ErrScopeMismatch, aa.scope, attemptedScope)
	}
	return nil
}

// AuditPayload is the plain, exported, JSON-serializable snapshot of an
// ActionAuthorization's own committed fields -- for writing to an audit
// ledger, mirroring decision.AuditPayload's own one-way discipline:
// nothing in this package ever reconstructs a live ActionAuthorization
// from an AuditPayload.
type AuditPayload struct {
	DecisionHash        string   `json:"decision_hash"`
	Actor               string   `json:"actor"`
	PolicyRef           string   `json:"policy_ref"`
	Scope               string   `json:"scope"`
	PermittedAction     string   `json:"permitted_action"`
	Conditions          []string `json:"conditions"`
	AuthorizedAt        uint64   `json:"authorized_at"`
	ExpiresAt           uint64   `json:"expires_at"`
	ActionAuthorization string   `json:"action_authorization_hash"`
}

// ToAuditPayload returns aa's audit-ledger snapshot. Refuses the zero
// ActionAuthorization -- there is nothing authoritative to record.
func (aa ActionAuthorization) ToAuditPayload() (AuditPayload, error) {
	if aa.IsZero() {
		return AuditPayload{}, fmt.Errorf("%w: cannot produce an audit payload for the zero-value ActionAuthorization", ErrDecisionNotAuthoritative)
	}
	return AuditPayload{
		DecisionHash: aa.decisionHash, Actor: aa.actor, PolicyRef: aa.policyRef, Scope: aa.scope,
		PermittedAction: string(aa.permittedAction), Conditions: aa.Conditions(),
		AuthorizedAt: aa.authorizedAt, ExpiresAt: aa.expiresAt, ActionAuthorization: aa.hash,
	}, nil
}
