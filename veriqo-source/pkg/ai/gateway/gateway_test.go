package gateway

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/disclosure/access"
)

func policy() Policy {
	return Policy{
		AllowedModels:        []string{"aureum-1"},
		AllowedJurisdictions: []string{"SG"},
		LicensesPermittingAI: []string{"licence-ai-ok"},
		PurposeFields: map[string][]string{
			"summarize_evidence": {"evidence_id", "sha256", "custody_head", "state"},
		},
		Version: "ai-policy-v1",
	}
}

func grant(rights ...access.Right) access.Grant {
	return access.Grant{
		EvidenceVersionID: "EV-1", RecipientID: "aureum-runner", RecipientRole: "service",
		Procedural: access.P2ProcessVisible, Content: access.C3ControlledFullView,
		Rights: rights, PolicyVersion: "policy-v1", Privilege: access.PrivilegeNotClaimed,
	}
}

func request() Request {
	return Request{
		Model:             Model{ID: "aureum-1", Version: "1.4", Provider: "internal"},
		EvidenceVersionID: "EV-1", TenantID: "tenant-a", RecipientID: "aureum-runner",
		Right: access.AIProcess, Purpose: "summarize_evidence",
		Jurisdiction: "SG", License: "licence-ai-ok", Tick: 10,
	}
}

func TestPermittedRequestYieldsAProjection(t *testing.T) {
	d, err := Evaluate(policy(), grant(access.AIProcess), request())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("expected allowed, got %q", d.Reason)
	}
	if d.Projection == nil {
		t.Fatal("a permitted request must yield a projection")
	}
	if !d.Projection.MinimumNecessary {
		t.Fatal("the projection must be marked minimum-necessary")
	}
	if len(d.Projection.Fields) != 4 {
		t.Fatalf("expected the purpose's four fields, got %v", d.Projection.Fields)
	}
}

// TestForbiddenActionRefusedEvenWithEveryRight is Article 8: it is not
// a permission that can be granted.
func TestForbiddenActionRefusedEvenWithEveryRight(t *testing.T) {
	g := grant(access.Rights()...)
	g.Content = access.C5PrivilegedEnclave

	for _, action := range ForbiddenActions() {
		req := request()
		req.Action = action
		req.PrivilegeOverride = "authorized"

		d, err := Evaluate(policy(), g, req)
		if err != nil {
			t.Fatalf("Evaluate %s: %v", action, err)
		}
		if d.Allowed {
			t.Fatalf("action %q must be refused even with every right", action)
		}
		if !strings.Contains(d.Reason, "Article 8") {
			t.Fatalf("the denial should cite Article 8, got %q", d.Reason)
		}
	}
}

// TestAIInstructingConnectorIsRefused is the indirect-authority case:
// an AI that can direct acquisition chooses what enters the fabric.
func TestAIInstructingConnectorIsRefused(t *testing.T) {
	req := request()
	req.Action = "instruct_connector"
	d, err := Evaluate(policy(), grant(access.AIProcess), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("an AI instructing a connector must be refused")
	}
}

// TestUnapprovedModelIsRefused proves the model allowlist fails
// closed: an empty list approves nobody.
func TestUnapprovedModelIsRefused(t *testing.T) {
	req := request()
	req.Model.ID = "rogue-model"
	d, err := Evaluate(policy(), grant(access.AIProcess), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("an unapproved model must be refused")
	}

	empty := policy()
	empty.AllowedModels = nil
	d2, err := Evaluate(empty, grant(access.AIProcess), request())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d2.Allowed {
		t.Fatal("an empty model allowlist must approve nobody, not everybody")
	}
}

// TestUnrecognisedPurposeYieldsNoProjection proves we cannot compute a
// minimum-necessary disclosure for a purpose we do not recognise.
func TestUnrecognisedPurposeYieldsNoProjection(t *testing.T) {
	req := request()
	req.Purpose = "have_a_look_around"
	d, err := Evaluate(policy(), grant(access.AIProcess), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed || d.Projection != nil {
		t.Fatal("an unrecognised purpose must yield no projection")
	}
}

func TestLicenceMustPositivelyPermitAI(t *testing.T) {
	req := request()
	req.License = "licence-no-ai"
	d, err := Evaluate(policy(), grant(access.AIProcess), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("a licence that does not permit AI processing must refuse")
	}
}

func TestJurisdictionIsEnforced(t *testing.T) {
	req := request()
	req.Jurisdiction = "XX"
	d, _ := Evaluate(policy(), grant(access.AIProcess), req)
	if d.Allowed {
		t.Fatal("a disallowed jurisdiction must refuse")
	}
}

// TestTrainRequiresBothEvidenceRightAndModelPermission proves the two
// permissions are independent: the evidence may permit training while
// the model operator may not, and vice versa.
func TestTrainRequiresBothEvidenceRightAndModelPermission(t *testing.T) {
	p := policy()
	p.PurposeFields["train_model"] = []string{"evidence_id"}

	// Evidence grants TRAIN, but the model is not permitted to retain.
	req := request()
	req.Right = access.Train
	req.Purpose = "train_model"
	g := grant(access.Train)
	g.Content = access.C4Export

	d, err := Evaluate(p, g, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("TRAIN must be refused when the model operator lacks training permission")
	}

	// Now the model is permitted too.
	req.Model.TrainingPermitted = true
	d2, err := Evaluate(p, g, req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !d2.Allowed {
		t.Fatalf("TRAIN should be permitted when both sides allow it: %s", d2.Reason)
	}
}

// TestRedactedEvidenceIsNotAutomaticallyAISafe is Article 21.
func TestRedactedEvidenceIsNotAutomaticallyAISafe(t *testing.T) {
	g := grant(access.View) // redacted derivative viewable, but no AI right
	g.Content = access.C2Redacted

	d, err := Evaluate(policy(), g, request())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("a redacted derivative cleared for viewing must not thereby be AI-processable")
	}
}

// TestPrivilegedMaterialDefaultDeniesToAI covers privileged material
// reaching a model.
func TestPrivilegedMaterialDefaultDeniesToAI(t *testing.T) {
	g := grant(access.AIProcess)
	g.Privilege = access.PrivilegeConfirmed

	d, err := Evaluate(policy(), g, request())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("privileged material must default-deny to AI")
	}
}

func TestNonAIRightIsRejected(t *testing.T) {
	req := request()
	req.Right = access.View
	if _, err := Evaluate(policy(), grant(access.View), req); !errors.Is(err, ErrNotAnAIRight) {
		t.Fatalf("expected ErrNotAnAIRight, got %v", err)
	}
}

func TestModelIdentityIsMandatory(t *testing.T) {
	req := request()
	req.Model.Version = ""
	if _, err := Evaluate(policy(), grant(access.AIProcess), req); !errors.Is(err, ErrNoModelIdentity) {
		t.Fatalf("expected ErrNoModelIdentity, got %v", err)
	}
}

// TestAllTenDimensionsAreReported proves a denial names which
// dimension failed rather than being opaque.
func TestAllTenDimensionsAreReported(t *testing.T) {
	d, err := Evaluate(policy(), grant(access.AIProcess), request())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(d.Checks) != 10 {
		t.Fatalf("expected all ten dimensions reported, got %d", len(d.Checks))
	}
	for _, dim := range CheckDimensions() {
		if _, ok := d.Checks[dim]; !ok {
			t.Fatalf("dimension %q missing from the report", dim)
		}
	}
}

func TestEveryAIAccessRequiresAnEvent(t *testing.T) {
	allowed, _ := Evaluate(policy(), grant(access.AIProcess), request())
	req := request()
	req.Model.ID = "rogue"
	denied, _ := Evaluate(policy(), grant(access.AIProcess), req)
	if !allowed.EventRequired || !denied.EventRequired {
		t.Fatal("both permitted and refused AI accesses must require a ledger event")
	}
}

// --- Contribution records ---

// TestMaterialContributionRequiresHumanReviewer is Article 27's
// operational teeth.
func TestMaterialContributionRequiresHumanReviewer(t *testing.T) {
	m := Model{ID: "aureum-1", Version: "1.4"}
	_, err := NewContribution(m, "prompt", "output", "summarize", "policy-v1", "",
		[]string{"E-1"}, []string{"EV-1"}, nil, true, 10)
	if !errors.Is(err, ErrNoHumanReviewer) {
		t.Fatalf("expected ErrNoHumanReviewer, got %v", err)
	}
}

func TestImmaterialContributionNeedsNoReviewer(t *testing.T) {
	m := Model{ID: "aureum-1", Version: "1.4"}
	if _, err := NewContribution(m, "p", "o", "summarize", "policy-v1", "",
		[]string{"E-1"}, []string{"EV-1"}, nil, false, 10); err != nil {
		t.Fatalf("an immaterial contribution should not require a reviewer: %v", err)
	}
}

// TestContributionBindsEvidenceVERSIONs proves the record pins
// versions, not just IDs: a contribution against version 2 is not the
// same contribution once version 3 exists.
func TestContributionBindsEvidenceVersions(t *testing.T) {
	m := Model{ID: "aureum-1", Version: "1.4"}
	c, err := NewContribution(m, "p", "o", "summarize", "policy-v1", "analyst-1",
		[]string{"E-1"}, []string{"EV-1-v2"}, []string{"search"}, true, 10)
	if err != nil {
		t.Fatalf("NewContribution: %v", err)
	}
	if len(c.InputEvidenceVersionIDs) != 1 || c.InputEvidenceVersionIDs[0] != "EV-1-v2" {
		t.Fatalf("the contribution must pin evidence versions, got %v", c.InputEvidenceVersionIDs)
	}
	if !c.Traceable() {
		t.Fatal("a complete contribution must be traceable")
	}
}

func TestContributionHashesPromptAndOutput(t *testing.T) {
	m := Model{ID: "aureum-1", Version: "1.4"}
	c, err := NewContribution(m, "the prompt", "the output", "summarize", "policy-v1", "analyst-1",
		[]string{"E-1"}, []string{"EV-1"}, nil, true, 10)
	if err != nil {
		t.Fatalf("NewContribution: %v", err)
	}
	if c.PromptHash == c.OutputHash {
		t.Fatal("distinct prompt and output must hash differently")
	}
	if c.PromptHash != HashText("the prompt") {
		t.Fatal("the prompt hash is not the canonical hash of the prompt")
	}
}

func TestUntraceableContributionIsDetected(t *testing.T) {
	c := Contribution{ModelID: "m", ModelVersion: "1", PromptHash: "a", OutputHash: "b", PolicyVersion: "p"}
	if c.Traceable() {
		t.Fatal("a contribution citing no evidence versions must not be traceable")
	}
}

func TestContributionRequiresPolicyVersion(t *testing.T) {
	m := Model{ID: "aureum-1", Version: "1.4"}
	if _, err := NewContribution(m, "p", "o", "summarize", "", "analyst-1",
		[]string{"E-1"}, []string{"EV-1"}, nil, false, 10); err == nil {
		t.Fatal("a contribution with no policy version must be refused")
	}
}
