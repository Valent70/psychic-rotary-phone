package access

import (
	"errors"
	"strings"
	"testing"
)

func grant(rights ...Right) Grant {
	return Grant{
		EvidenceVersionID: "EV-1", RecipientID: "counsel-A", RecipientRole: "counsel",
		Procedural: P3PartyVisible, Content: C4Export,
		Rights: rights, PolicyVersion: "policy-v1", Privilege: PrivilegeNotClaimed,
	}
}

func request(r Right) Request {
	return Request{EvidenceVersionID: "EV-1", RecipientID: "counsel-A", Right: r, Purpose: "review", Tick: 10}
}

// TestTheTwoDimensionsAreIndependent proves the states that a
// collapsed single "access level" could not express.
func TestTheTwoDimensionsAreIndependent(t *testing.T) {
	// A party knows it exists but cannot read a word: P3/C1.
	g := grant(View)
	g.Procedural = P3PartyVisible
	g.Content = C1Existence
	d, err := Evaluate(g, request(View))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("C1_EXISTENCE must not permit VIEW even at P3_PARTY_VISIBLE")
	}

	// An expert reads it in an enclave while procedural visibility stays
	// low: P2/C5.
	g2 := grant(View)
	g2.Procedural = P2ProcessVisible
	g2.Content = C5PrivilegedEnclave
	d2, err := Evaluate(g2, request(View))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !d2.Allowed {
		t.Fatalf("C5 should permit VIEW regardless of low procedural visibility: %s", d2.Reason)
	}
}

// TestAccessDoesNotImplyUse is Article 20, and the commercially
// dangerous case: a VIEW grant does not confer TRAIN.
func TestAccessDoesNotImplyUse(t *testing.T) {
	g := grant(View)
	for _, r := range []Right{Export, Download, AIProcess, RAG, Train, Redistribute} {
		d, err := Evaluate(g, request(r))
		if err != nil {
			t.Fatalf("Evaluate %s: %v", r, err)
		}
		if d.Allowed {
			t.Fatalf("a VIEW-only grant must not permit %s", r)
		}
		if !strings.Contains(d.Reason, "access does not imply use") {
			t.Fatalf("the denial should cite the rule, got %q", d.Reason)
		}
	}
}

// TestThreeAIRightsAreSeparateGrants proves AI_PROCESS does not imply
// RAG, and neither implies TRAIN -- the most common way evidence
// rights are silently exceeded.
func TestThreeAIRightsAreSeparateGrants(t *testing.T) {
	g := grant(AIProcess)
	for _, r := range []Right{RAG, Train} {
		d, err := Evaluate(g, request(r))
		if err != nil {
			t.Fatalf("Evaluate %s: %v", r, err)
		}
		if d.Allowed {
			t.Fatalf("an AI_PROCESS grant must not confer %s", r)
		}
	}
	d, err := Evaluate(g, request(AIProcess))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("the granted right should be permitted: %s", d.Reason)
	}
}

// TestPrivilegedMaterialIsDefaultDeny covers the privilege-leakage
// adversarial case (MIP §23).
func TestPrivilegedMaterialIsDefaultDeny(t *testing.T) {
	for _, st := range []PrivilegeStatus{
		PrivilegeClaimed, PrivilegePendingReview, PrivilegeConfirmed,
		PrivilegeDisputed, PrivilegePartiallyConfirmed,
	} {
		g := grant(View)
		g.Privilege = st
		d, err := Evaluate(g, request(View))
		if err != nil {
			t.Fatalf("Evaluate %s: %v", st, err)
		}
		if d.Allowed {
			t.Fatalf("privilege status %s must default-deny", st)
		}
	}
}

// TestClaimedPrivilegeRestrictsAsFirmlyAsConfirmed is the subtle one:
// protecting a claim only after it is confirmed would make the claim
// pointless.
func TestClaimedPrivilegeRestrictsAsFirmlyAsConfirmed(t *testing.T) {
	claimed := grant(View)
	claimed.Privilege = PrivilegeClaimed
	d, err := Evaluate(claimed, request(View))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("a merely CLAIMED privilege must still restrict")
	}
}

func TestExplicitOverrideReachesPrivilegedMaterial(t *testing.T) {
	g := grant(View)
	g.Privilege = PrivilegeConfirmed
	req := request(View)
	req.PrivilegeOverride = "tribunal order 2026-07 permits counsel review"

	d, err := Evaluate(g, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("an explicit authorization should reach privileged material: %s", d.Reason)
	}
}

func TestWaivedAndReleasedPrivilegeDoNotRestrict(t *testing.T) {
	for _, st := range []PrivilegeStatus{PrivilegeWaived, PrivilegeReleased, PrivilegeRejected, PrivilegeNotClaimed} {
		g := grant(View)
		g.Privilege = st
		d, err := Evaluate(g, request(View))
		if err != nil {
			t.Fatalf("Evaluate %s: %v", st, err)
		}
		if !d.Allowed {
			t.Fatalf("status %s should not restrict, got %q", st, d.Reason)
		}
	}
}

// TestProtectiveOrderViolation covers the protective-order adversarial
// case (MIP §23).
func TestProtectiveOrderViolation(t *testing.T) {
	g := grant(Export)
	g.ProtectiveOrder = &ProtectiveOrder{
		Reference: "PO-1", AllowedRoles: []string{"expert"},
		AllowedPurposes: []Right{View}, MaxContent: C3ControlledFullView,
	}
	d, err := Evaluate(g, request(Export))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("a protective order permitting only role=expert must deny role=counsel")
	}
	if !strings.Contains(d.Reason, "PO-1") {
		t.Fatalf("the denial should name the order, got %q", d.Reason)
	}
}

func TestProtectiveOrderCapsContentLevel(t *testing.T) {
	g := grant(View)
	g.Content = C5PrivilegedEnclave
	g.ProtectiveOrder = &ProtectiveOrder{
		Reference: "PO-2", AllowedRoles: []string{"counsel"},
		AllowedPurposes: []Right{View}, MaxContent: C2Redacted,
	}
	d, err := Evaluate(g, request(View))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("a protective order capping content at C2 must deny a C5 grant")
	}
}

func TestProtectiveOrderExpiry(t *testing.T) {
	g := grant(View)
	g.ProtectiveOrder = &ProtectiveOrder{
		Reference: "PO-3", AllowedRoles: []string{"counsel"},
		AllowedPurposes: []Right{View}, MaxContent: C5PrivilegedEnclave, ExpiryTick: 5,
	}
	d, _ := Evaluate(g, request(View)) // request is at tick 10
	if d.Allowed {
		t.Fatal("an expired protective order must deny")
	}
}

func TestGrantExpiry(t *testing.T) {
	g := grant(View)
	g.ExpiryTick = 5
	d, _ := Evaluate(g, request(View))
	if d.Allowed {
		t.Fatal("an expired grant must deny")
	}
}

// TestContentLevelMustSupportTheRight proves a granted right still
// fails when the content level cannot carry it -- a privilege
// escalation guard independent of the grant list.
func TestContentLevelMustSupportTheRight(t *testing.T) {
	g := grant(Export)
	g.Content = C3ControlledFullView // export needs C4
	d, err := Evaluate(g, request(Export))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("EXPORT at C3 must be denied")
	}
}

// TestCrossRecipientAccessDenied covers the cross-tenant/wrong-subject
// adversarial case.
func TestCrossRecipientAccessDenied(t *testing.T) {
	g := grant(View)
	req := request(View)
	req.RecipientID = "opposing-counsel-B"
	d, err := Evaluate(g, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("a grant to one recipient must not serve another")
	}
}

func TestWrongEvidenceVersionDenied(t *testing.T) {
	g := grant(View)
	req := request(View)
	req.EvidenceVersionID = "EV-99"
	d, _ := Evaluate(g, req)
	if d.Allowed {
		t.Fatal("a grant for one evidence version must not serve another")
	}
}

// TestEveryDecisionRequiresAnEvent is Article 24: including denials,
// because a pattern of refused requests is itself probative.
func TestEveryDecisionRequiresAnEvent(t *testing.T) {
	allowed, _ := Evaluate(grant(View), request(View))
	denied, _ := Evaluate(grant(View), request(Train))
	if !allowed.EventRequired || !denied.EventRequired {
		t.Fatal("both permitted and refused decisions must require a ledger event")
	}
}

func TestDecisionAlwaysCarriesAReason(t *testing.T) {
	for _, r := range Rights() {
		d, err := Evaluate(grant(View), request(r))
		if err != nil {
			t.Fatalf("Evaluate %s: %v", r, err)
		}
		if d.Reason == "" {
			t.Fatalf("no reason given for %s", r)
		}
	}
}

func TestValidateGrantRequiresPolicyVersion(t *testing.T) {
	g := grant(View)
	g.PolicyVersion = ""
	if err := ValidateGrant(g); !errors.Is(err, ErrEmptyPolicy) {
		t.Fatalf("expected ErrEmptyPolicy, got %v", err)
	}
}

func TestUnknownRightIsRejected(t *testing.T) {
	if _, err := Evaluate(grant(View), request(Right("TELEPATHY"))); !errors.Is(err, ErrUnknownRight) {
		t.Fatalf("expected ErrUnknownRight, got %v", err)
	}
}

func TestTenRightsAreDefined(t *testing.T) {
	if len(Rights()) != 10 {
		t.Fatalf("the specification names ten purpose-bound rights, got %d", len(Rights()))
	}
	if len(AIRights()) != 3 {
		t.Fatalf("expected three AI rights, got %d", len(AIRights()))
	}
}

// --- Controlled view sessions ---

func session() ControlledViewSession {
	return ControlledViewSession{
		SessionID: "S-1", EvidenceVersionID: "EV-1", RecipientID: "counsel-A",
		Purpose: "review", PolicyVersion: "policy-v1",
		DeviceTrusted: true, MFAVerified: true, Watermarked: true,
		DisclosureDecision: "DD-1", ExpiryTick: 100,
	}
}

// TestControlledViewRequiresEveryCondition proves a "controlled" view
// missing any condition is simply a view.
func TestControlledViewRequiresEveryCondition(t *testing.T) {
	cases := map[string]func(*ControlledViewSession){
		"untrusted device": func(s *ControlledViewSession) { s.DeviceTrusted = false },
		"no MFA":           func(s *ControlledViewSession) { s.MFAVerified = false },
		"no watermark":     func(s *ControlledViewSession) { s.Watermarked = false },
		"no decision":      func(s *ControlledViewSession) { s.DisclosureDecision = "" },
	}
	for name, mut := range cases {
		s := session()
		mut(&s)
		if err := s.OpenSession(10); err == nil {
			t.Fatalf("%s must prevent the session opening", name)
		}
	}
	if err := session().OpenSession(10); err != nil {
		t.Fatalf("a fully-conditioned session should open: %v", err)
	}
}

func TestControlledViewSessionExpires(t *testing.T) {
	if err := session().OpenSession(101); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

// TestViewedIsNotReviewedIsNotAccepted proves the four engagement
// states never collapse -- the question that actually matters in a
// dispute is which one occurred.
func TestViewedIsNotReviewedIsNotAccepted(t *testing.T) {
	s := session()
	if got := s.EngagementLevel(); got != "NOT_OPENED" {
		t.Fatalf("expected NOT_OPENED, got %s", got)
	}
	s.Viewed = true
	if got := s.EngagementLevel(); got != "VIEWED" {
		t.Fatalf("expected VIEWED, got %s", got)
	}
	s.Reviewed = true
	if got := s.EngagementLevel(); got != "REVIEWED" {
		t.Fatalf("viewing must not imply reviewing; got %s", got)
	}
	s.Acknowledged = true
	if got := s.EngagementLevel(); got != "ACKNOWLEDGED" {
		t.Fatalf("expected ACKNOWLEDGED, got %s", got)
	}
	s.Accepted = true
	if got := s.EngagementLevel(); got != "ACCEPTED" {
		t.Fatalf("expected ACCEPTED, got %s", got)
	}
}

// TestAcknowledgementIsNotInferredFromViewing covers MIP §23's
// "acknowledgement manipulation" case.
func TestAcknowledgementIsNotInferredFromViewing(t *testing.T) {
	s := session()
	s.Viewed = true
	if s.Acknowledged || s.Accepted || s.Reviewed {
		t.Fatal("viewing must not set any stronger engagement state")
	}
	if s.EngagementLevel() == "ACKNOWLEDGED" {
		t.Fatal("a viewed-only session must not report acknowledgement")
	}
}
