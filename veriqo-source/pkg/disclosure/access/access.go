// Package access implements the two-dimensional disclosure model
// (MIP-001 W6), purpose-bound rights (W6.2), controlled view sessions
// (W6.1), privilege status (W6.3) and protective orders (W6.4).
//
// THE TWO DIMENSIONS MUST NEVER COLLAPSE INTO ONE INTEGER.
//
//	Procedural visibility  P0..P5  -- who knows this exists
//	Content access         C0..C5  -- who can see what it says
//
// They are genuinely orthogonal. A party may know a document exists
// and is privileged (P3/C1) without ever seeing a word of it. An
// expert may read it in a controlled enclave without the party knowing
// it was produced (P2/C5). Collapsing them into a single "access
// level" makes both of those states inexpressible, and systems that do
// it end up leaking one dimension to grant the other.
//
// PURPOSE-BOUND RIGHTS. Article 20: access does not imply use. Ten
// separately-granted rights, of which AI_PROCESS, RAG and TRAIN are
// three distinct grants -- permission to run a model over evidence
// once is not permission to index it for retrieval, and neither is
// permission to train on it. Collapsing those three is the most common
// way evidence rights are silently exceeded.
//
// This package composes pkg/authz's purpose binding rather than
// replacing it: MIP §7 forbids a second policy engine, and none is
// added here.
package access

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Procedural is the visibility dimension: who knows this exists.
type Procedural int

const (
	P0Invisible        Procedural = iota // nobody outside the process knows
	P1Metadata                           // metadata only
	P2ProcessVisible                     // visible within the evidence process
	P3PartyVisible                       // the parties know it exists
	P4ExpertVisible                      // appointed experts know
	P5AuthorityVisible                   // the tribunal/authority knows
)

func (p Procedural) String() string {
	switch p {
	case P0Invisible:
		return "P0_INVISIBLE"
	case P1Metadata:
		return "P1_METADATA"
	case P2ProcessVisible:
		return "P2_PROCESS_VISIBLE"
	case P3PartyVisible:
		return "P3_PARTY_VISIBLE"
	case P4ExpertVisible:
		return "P4_EXPERT_VISIBLE"
	case P5AuthorityVisible:
		return "P5_AUTHORITY_VISIBLE"
	default:
		return "P_UNKNOWN"
	}
}

// Content is the access dimension: who can see what it says.
type Content int

const (
	C0None               Content = iota // no access
	C1Existence                         // existence only
	C2Redacted                          // a redacted derivative
	C3ControlledFullView                // full view, controlled session
	C4Export                            // export permitted
	C5PrivilegedEnclave                 // privileged enclave access
)

func (c Content) String() string {
	switch c {
	case C0None:
		return "C0_NONE"
	case C1Existence:
		return "C1_EXISTENCE"
	case C2Redacted:
		return "C2_REDACTED"
	case C3ControlledFullView:
		return "C3_CONTROLLED_FULL_VIEW"
	case C4Export:
		return "C4_EXPORT"
	case C5PrivilegedEnclave:
		return "C5_PRIVILEGED_ENCLAVE"
	default:
		return "C_UNKNOWN"
	}
}

// Right is a purpose-bound use. Each is granted separately (Article 20).
type Right string

const (
	View         Right = "VIEW"
	Search       Right = "SEARCH"
	Copy         Right = "COPY"
	Print        Right = "PRINT"
	Download     Right = "DOWNLOAD"
	Export       Right = "EXPORT"
	Redistribute Right = "REDISTRIBUTE"
	AIProcess    Right = "AI_PROCESS"
	RAG          Right = "RAG"
	Train        Right = "TRAIN"
)

// Rights returns all ten purpose-bound rights.
func Rights() []Right {
	return []Right{View, Search, Copy, Print, Download, Export, Redistribute, AIProcess, RAG, Train}
}

// AIRights are the three rights that put evidence in front of a model.
// They are separate grants: one does not imply another.
func AIRights() []Right { return []Right{AIProcess, RAG, Train} }

// IsAIRight reports whether a right places evidence before a model.
func (r Right) IsAIRight() bool {
	for _, a := range AIRights() {
		if r == a {
			return true
		}
	}
	return false
}

// PrivilegeStatus is the privilege lifecycle (W6.3). VERIQO enforces
// these; it never determines them (Article 19).
type PrivilegeStatus string

const (
	PrivilegeNotClaimed         PrivilegeStatus = "NOT_CLAIMED"
	PrivilegeClaimed            PrivilegeStatus = "CLAIMED"
	PrivilegePendingReview      PrivilegeStatus = "PENDING_REVIEW"
	PrivilegeConfirmed          PrivilegeStatus = "CONFIRMED"
	PrivilegeDisputed           PrivilegeStatus = "DISPUTED"
	PrivilegePartiallyConfirmed PrivilegeStatus = "PARTIALLY_CONFIRMED"
	PrivilegeWaived             PrivilegeStatus = "WAIVED"
	PrivilegeRejected           PrivilegeStatus = "REJECTED"
	PrivilegeReleased           PrivilegeStatus = "RELEASED"
)

// restrictsByDefault reports whether a status puts material behind the
// default-deny wall. Claimed and pending-review restrict as firmly as
// confirmed: a privilege claim is protected while it is being decided,
// because the alternative would make the claim pointless.
func (p PrivilegeStatus) restrictsByDefault() bool {
	switch p {
	case PrivilegeClaimed, PrivilegePendingReview, PrivilegeConfirmed,
		PrivilegeDisputed, PrivilegePartiallyConfirmed:
		return true
	default:
		return false
	}
}

// ProtectiveOrder carries a forum's designation and its constraints
// (W6.4). A protective order is NOT privilege -- they are separate
// mechanisms that happen to both restrict, and conflating them loses
// the fact that a PO can be varied by the forum while privilege
// cannot.
type ProtectiveOrder struct {
	Reference       string
	Jurisdiction    string
	Forum           string
	Designation     string
	AllowedRoles    []string
	AllowedPurposes []Right
	MaxContent      Content
	Watermark       bool
	MFARequired     bool
	ExpiryTick      uint64
}

// Grant is what a recipient has been given for one evidence version.
type Grant struct {
	EvidenceVersionID string
	RecipientID       string
	RecipientRole     string
	Procedural        Procedural
	Content           Content
	Rights            []Right
	PolicyVersion     string
	Privilege         PrivilegeStatus
	ProtectiveOrder   *ProtectiveOrder
	ExpiryTick        uint64
}

// Request is an attempt to exercise a right.
type Request struct {
	EvidenceVersionID string
	RecipientID       string
	Right             Right
	Purpose           string
	Tick              uint64
	// PrivilegeOverride records an explicit authorization to reach
	// privileged material. Absent it, privileged material is
	// default-deny (AI-AUTHORITY-001 §7).
	PrivilegeOverride string
}

// Decision is the outcome of evaluating a request.
type Decision struct {
	Allowed           bool   `json:"allowed"`
	Reason            string `json:"reason"`
	EvidenceVersionID string `json:"evidence_version_id"`
	RecipientID       string `json:"recipient_id"`
	Right             string `json:"right"`
	PolicyVersion     string `json:"policy_version"`
	Tick              uint64 `json:"tick"`
	// EventRequired is always true. Article 24 admits no disclosure
	// path that does not emit a ledger event -- including a DENIED one,
	// because a pattern of refused requests is itself probative.
	EventRequired bool `json:"event_required"`
}

var (
	ErrNoGrant        = errors.New("access: no grant exists for this recipient and evidence version")
	ErrUnknownRight   = errors.New("access: not a canonical purpose-bound right")
	ErrEmptyPolicy    = errors.New("access: grant must carry a policy version (Article 7)")
	ErrGrantExpired   = errors.New("access: grant has expired")
	ErrEmptyRecipient = errors.New("access: recipient must be non-empty")
)

// minimumContentFor is the content level a right cannot function
// below. Exercising a right above a recipient's content level is a
// privilege escalation regardless of whether the right was granted.
func minimumContentFor(r Right) Content {
	switch r {
	case View, Search, AIProcess, RAG, Train:
		return C3ControlledFullView
	case Copy, Print, Download, Export, Redistribute:
		return C4Export
	default:
		return C1Existence
	}
}

// ValidateGrant checks a grant is internally coherent.
func ValidateGrant(g Grant) error {
	if strings.TrimSpace(g.RecipientID) == "" {
		return ErrEmptyRecipient
	}
	if strings.TrimSpace(g.PolicyVersion) == "" {
		return ErrEmptyPolicy
	}
	for _, r := range g.Rights {
		if !isKnownRight(r) {
			return fmt.Errorf("%w: %q", ErrUnknownRight, r)
		}
	}
	return nil
}

func isKnownRight(r Right) bool {
	for _, k := range Rights() {
		if r == k {
			return true
		}
	}
	return false
}

// Evaluate decides one request against one grant. It is fail-closed
// throughout: every unmet condition denies, and the reason is always
// stated so a denial is auditable rather than merely negative.
//
// The order of checks is deliberate, hardest constraint first, so the
// reported reason names the most fundamental obstacle rather than an
// incidental one.
func Evaluate(g Grant, req Request) (Decision, error) {
	d := Decision{
		EvidenceVersionID: req.EvidenceVersionID, RecipientID: req.RecipientID,
		Right: string(req.Right), PolicyVersion: g.PolicyVersion,
		Tick: req.Tick, EventRequired: true,
	}
	if err := ValidateGrant(g); err != nil {
		return d, err
	}
	if !isKnownRight(req.Right) {
		return d, fmt.Errorf("%w: %q", ErrUnknownRight, req.Right)
	}
	if g.RecipientID != req.RecipientID || g.EvidenceVersionID != req.EvidenceVersionID {
		d.Reason = "no grant exists for this recipient and evidence version"
		return d, nil
	}

	// 1. Privilege: default-deny, and an override must be explicit.
	if g.Privilege.restrictsByDefault() && strings.TrimSpace(req.PrivilegeOverride) == "" {
		d.Reason = fmt.Sprintf("privilege status %s restricts by default; no explicit authorization supplied", g.Privilege)
		return d, nil
	}

	// 2. Expiry.
	if g.ExpiryTick > 0 && req.Tick > g.ExpiryTick {
		d.Reason = fmt.Sprintf("grant expired at tick %d, request at %d", g.ExpiryTick, req.Tick)
		return d, nil
	}

	// 3. Protective order, where one applies.
	if po := g.ProtectiveOrder; po != nil {
		if po.ExpiryTick > 0 && req.Tick > po.ExpiryTick {
			d.Reason = fmt.Sprintf("protective order %s expired at tick %d", po.Reference, po.ExpiryTick)
			return d, nil
		}
		if len(po.AllowedRoles) > 0 && !contains(po.AllowedRoles, g.RecipientRole) {
			d.Reason = fmt.Sprintf("protective order %s does not permit role %q", po.Reference, g.RecipientRole)
			return d, nil
		}
		if len(po.AllowedPurposes) > 0 && !containsRight(po.AllowedPurposes, req.Right) {
			d.Reason = fmt.Sprintf("protective order %s does not permit purpose %s", po.Reference, req.Right)
			return d, nil
		}
		if g.Content > po.MaxContent {
			d.Reason = fmt.Sprintf("protective order %s caps content at %s, grant carries %s",
				po.Reference, po.MaxContent, g.Content)
			return d, nil
		}
	}

	// 4. The right itself must be separately granted (Article 20).
	if !containsRight(g.Rights, req.Right) {
		d.Reason = fmt.Sprintf("right %s was not granted; access does not imply use", req.Right)
		return d, nil
	}

	// 5. Content level must support the right.
	if need := minimumContentFor(req.Right); g.Content < need {
		d.Reason = fmt.Sprintf("right %s requires content level %s, grant carries %s", req.Right, need, g.Content)
		return d, nil
	}

	d.Allowed = true
	d.Reason = fmt.Sprintf("right %s granted at %s/%s under policy %s",
		req.Right, g.Procedural, g.Content, g.PolicyVersion)
	return d, nil
}

// GrantedRights returns a grant's rights, sorted, for reporting.
func GrantedRights(g Grant) []string {
	out := make([]string, 0, len(g.Rights))
	for _, r := range g.Rights {
		out = append(out, string(r))
	}
	sort.Strings(out)
	return out
}

// ControlledViewSession binds a full-view session to its constraints
// (W6.1).
//
// VIEWED != REVIEWED != ACKNOWLEDGED != ACCEPTED. The four states are
// separate fields precisely because a party that opened a document has
// not thereby reviewed it, and a party that reviewed it has not
// thereby accepted it. Systems that record only "accessed" cannot
// answer the question that actually matters in a dispute.
type ControlledViewSession struct {
	SessionID          string
	CaseID             string
	EvidenceVersionID  string
	RecipientID        string
	Purpose            string
	PolicyVersion      string
	ProtectiveOrder    string
	Privilege          PrivilegeStatus
	ExpiryTick         uint64
	DeviceTrusted      bool
	MFAVerified        bool
	Watermarked        bool
	DisclosureDecision string

	Viewed       bool
	Reviewed     bool
	Acknowledged bool
	Accepted     bool
}

var (
	ErrSessionUntrustedDevice = errors.New("access: controlled view requires a trusted device")
	ErrSessionNoMFA           = errors.New("access: controlled view requires MFA")
	ErrSessionNoWatermark     = errors.New("access: controlled view requires watermarking")
	ErrSessionExpired         = errors.New("access: controlled view session has expired")
	ErrSessionNoDecision      = errors.New("access: controlled view requires a recorded disclosure decision (Article 24)")
)

// OpenSession validates that a controlled view may begin. Every
// condition is mandatory: a "controlled" view missing any of them is
// simply a view.
func (s ControlledViewSession) OpenSession(nowTick uint64) error {
	switch {
	case !s.DeviceTrusted:
		return ErrSessionUntrustedDevice
	case !s.MFAVerified:
		return ErrSessionNoMFA
	case !s.Watermarked:
		return ErrSessionNoWatermark
	case strings.TrimSpace(s.DisclosureDecision) == "":
		return ErrSessionNoDecision
	case s.ExpiryTick > 0 && nowTick > s.ExpiryTick:
		return ErrSessionExpired
	}
	return nil
}

// EngagementLevel reports the strongest recorded engagement, refusing
// to infer a stronger one from a weaker.
func (s ControlledViewSession) EngagementLevel() string {
	switch {
	case s.Accepted:
		return "ACCEPTED"
	case s.Acknowledged:
		return "ACKNOWLEDGED"
	case s.Reviewed:
		return "REVIEWED"
	case s.Viewed:
		return "VIEWED"
	default:
		return "NOT_OPENED"
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func containsRight(hay []Right, needle Right) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
