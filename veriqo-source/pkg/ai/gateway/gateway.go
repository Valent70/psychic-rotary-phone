// Package gateway is the AI Evidence Gateway (MIP-001 W9.3),
// enforcing Constitutional Articles 8, 20, 21 and 27.
//
// NO AI TOUCHES RAW EVIDENCE.
//
//	RAW EVIDENCE ──X── AI cannot access directly
//	APPROVED EVIDENCE PROJECTION ──→ AI
//
// Every AI access is evaluated on ten dimensions and, if permitted,
// yields a PROJECTION -- a bounded view carrying only what the purpose
// requires. The projection is the only surface a model ever sees. That
// matters commercially as much as legally: it is what prevents a
// customer's licensed data, another tenant's evidence, or privileged
// material from entering a model context.
//
// THE GATEWAY IS BUILT BEFORE THE INTELLIGENCES IT CONSTRAINS. Aureum
// and God of EYS do not exist in this build. That ordering is
// deliberate -- a boundary retrofitted after the systems it constrains
// have shipped is a boundary that will already have been bypassed.
//
// This package composes pkg/disclosure/access rather than
// reimplementing rights logic; MIP §7 forbids a second policy engine.
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/disclosure/access"
)

var (
	ErrNoModelIdentity = errors.New("gateway: model identity and version are required")
	ErrNoPurpose       = errors.New("gateway: an AI request must state a purpose")
	ErrNotAnAIRight    = errors.New("gateway: requested right is not an AI right (AI_PROCESS, RAG, TRAIN)")
	ErrDenied          = errors.New("gateway: access denied")
	ErrNoHumanReviewer = errors.New("gateway: a material AI contribution requires a named human reviewer (Article 27)")
	ErrForbiddenAction = errors.New("gateway: this action carries evidence authority and no AI may perform it (Article 8)")
	ErrEmptyEvidence   = errors.New("gateway: no evidence version supplied")
)

// ForbiddenActions are the operations no AI may perform, whatever its
// rights (AI-AUTHORITY-001 §2). Held as data so the prohibition is
// testable rather than conventional.
func ForbiddenActions() []string {
	return []string{
		"alter_evidence", "delete_evidence", "change_trust", "change_policy",
		"approve_disclosure", "confirm_privilege", "suppress_contradiction",
		"qualify_evidence", "sign_qualification", "determine_liability",
		"instruct_connector",
	}
}

// Model identifies the requesting intelligence.
type Model struct {
	ID       string
	Version  string
	Provider string
	// TrainingPermitted records whether this model's operator is
	// permitted to retain evidence for training. Separate from the TRAIN
	// right on the evidence: BOTH must permit it.
	TrainingPermitted bool
}

// Request is an AI's attempt to reach evidence.
type Request struct {
	Model             Model
	EvidenceVersionID string
	TenantID          string
	RecipientID       string
	Right             access.Right
	Purpose           string
	// Action is what the AI intends to do. Any forbidden action is
	// refused before rights are even consulted.
	Action string
	// Jurisdiction and License carry the external constraints an
	// evidence licence may impose beyond the case's own policy.
	Jurisdiction string
	License      string
	Tick         uint64
	// PrivilegeOverride must be explicit to reach privileged material.
	PrivilegeOverride string
}

// Projection is the bounded view an approved request yields. It
// deliberately carries no evidence bytes: VERIQO holds hashes and
// metadata, and the projection carries the subset of THAT which the
// purpose requires.
type Projection struct {
	EvidenceVersionID string   `json:"evidence_version_id"`
	TenantID          string   `json:"tenant_id"`
	Fields            []string `json:"fields"`
	Redacted          bool     `json:"redacted"`
	Purpose           string   `json:"purpose"`
	PolicyVersion     string   `json:"policy_version"`
	// MinimumNecessary records that the field set was narrowed to the
	// purpose rather than handed over wholesale.
	MinimumNecessary bool `json:"minimum_necessary"`
}

// Decision is the gateway's verdict.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	// Checks records all ten dimensions and their outcome, so a denial
	// names which dimension failed rather than being opaque.
	Checks map[string]bool `json:"checks"`
	// Projection is populated only when Allowed.
	Projection *Projection `json:"projection,omitempty"`
	ModelID    string      `json:"model_id"`
	Tick       uint64      `json:"tick"`
	// EventRequired is always true: an AI access attempt, permitted or
	// refused, is a ledger event (Articles 24, 27).
	EventRequired bool `json:"event_required"`
}

// CheckDimensions are the ten dimensions every AI access evaluates
// (AI-AUTHORITY-001 §4).
func CheckDimensions() []string {
	return []string{
		"purpose", "classification", "privilege", "protective_order",
		"rights", "license", "jurisdiction", "ai_processing_policy",
		"recipient", "minimum_necessary",
	}
}

// Policy is the deployment's AI processing policy.
type Policy struct {
	// AllowedModels, when non-empty, restricts which models may request
	// at all. An empty list means no model is pre-approved -- fail
	// closed, not open.
	AllowedModels []string
	// AllowedJurisdictions, when non-empty, restricts where processing
	// may occur.
	AllowedJurisdictions []string
	// LicensesPermittingAI names evidence licences that permit AI
	// processing. A licence absent from this list does not permit it.
	LicensesPermittingAI []string
	// PurposeFields maps a purpose to the minimum necessary field set.
	// A purpose absent from this map yields no projection: we cannot
	// compute a minimum necessary disclosure for a purpose we do not
	// recognise.
	PurposeFields map[string][]string
	Version       string
}

// Evaluate runs the gateway. Fail-closed at every dimension.
//
// Order matters: forbidden actions are refused before rights are
// consulted, because a request to alter evidence is refused even from
// a caller holding every right. Article 8 is not a permission that can
// be granted.
func Evaluate(p Policy, g access.Grant, req Request) (Decision, error) {
	d := Decision{
		Checks: map[string]bool{}, ModelID: req.Model.ID,
		Tick: req.Tick, EventRequired: true,
	}
	for _, dim := range CheckDimensions() {
		d.Checks[dim] = false
	}

	// Structural validation first.
	if strings.TrimSpace(req.Model.ID) == "" || strings.TrimSpace(req.Model.Version) == "" {
		return d, ErrNoModelIdentity
	}
	if strings.TrimSpace(req.Purpose) == "" {
		return d, ErrNoPurpose
	}
	if strings.TrimSpace(req.EvidenceVersionID) == "" {
		return d, ErrEmptyEvidence
	}
	if !req.Right.IsAIRight() {
		return d, fmt.Errorf("%w: %q", ErrNotAnAIRight, req.Right)
	}

	// Article 8 gate: forbidden actions are refused unconditionally.
	for _, f := range ForbiddenActions() {
		if req.Action == f {
			d.Reason = fmt.Sprintf("action %q carries evidence authority; no AI may perform it (Article 8)", req.Action)
			return d, nil
		}
	}

	// 1. Model must be pre-approved.
	if !contains(p.AllowedModels, req.Model.ID) {
		d.Reason = fmt.Sprintf("model %q is not in the deployment's approved model list", req.Model.ID)
		return d, nil
	}

	// 2. Purpose must be recognised, or no minimum-necessary set exists.
	fields, ok := p.PurposeFields[req.Purpose]
	if !ok || len(fields) == 0 {
		d.Reason = fmt.Sprintf("purpose %q has no defined minimum-necessary field set; no projection can be computed", req.Purpose)
		return d, nil
	}
	d.Checks["purpose"] = true

	// 3. Jurisdiction.
	if len(p.AllowedJurisdictions) > 0 && !contains(p.AllowedJurisdictions, req.Jurisdiction) {
		d.Reason = fmt.Sprintf("jurisdiction %q is not permitted for AI processing", req.Jurisdiction)
		return d, nil
	}
	d.Checks["jurisdiction"] = true

	// 4. Licence must positively permit AI processing.
	if !contains(p.LicensesPermittingAI, req.License) {
		d.Reason = fmt.Sprintf("evidence licence %q does not permit AI processing", req.License)
		return d, nil
	}
	d.Checks["license"] = true

	// 5. TRAIN additionally requires the model operator's own permission.
	if req.Right == access.Train && !req.Model.TrainingPermitted {
		d.Reason = fmt.Sprintf("model %q is not permitted to retain evidence for training", req.Model.ID)
		return d, nil
	}
	d.Checks["ai_processing_policy"] = true

	// 6. Delegate rights, privilege, protective order and content level
	// to the disclosure layer -- no second policy engine here.
	dec, err := access.Evaluate(g, access.Request{
		EvidenceVersionID: req.EvidenceVersionID,
		RecipientID:       req.RecipientID,
		Right:             req.Right,
		Purpose:           req.Purpose,
		Tick:              req.Tick,
		PrivilegeOverride: req.PrivilegeOverride,
	})
	if err != nil {
		return d, err
	}
	if !dec.Allowed {
		d.Reason = "disclosure layer refused: " + dec.Reason
		return d, nil
	}
	d.Checks["rights"] = true
	d.Checks["privilege"] = true
	d.Checks["protective_order"] = true
	d.Checks["recipient"] = true
	d.Checks["classification"] = true

	// 7. Build the minimum-necessary projection.
	proj := Projection{
		EvidenceVersionID: req.EvidenceVersionID, TenantID: req.TenantID,
		Fields: append([]string(nil), fields...), Purpose: req.Purpose,
		PolicyVersion: g.PolicyVersion, MinimumNecessary: true,
		Redacted: g.Content == access.C2Redacted,
	}
	sort.Strings(proj.Fields)
	d.Checks["minimum_necessary"] = true

	d.Allowed = true
	d.Projection = &proj
	d.Reason = fmt.Sprintf("model %s@%s permitted %s for purpose %q over %d field(s)",
		req.Model.ID, req.Model.Version, req.Right, req.Purpose, len(proj.Fields))
	return d, nil
}

// Contribution is the AI contribution record (MIP W9.4), delivering
// Article 27 -- No Silent AI Influence.
//
// InputEvidenceVersionIDs, not just IDs: a contribution made against
// version 2 of an artifact is not the same contribution once version 3
// exists.
type Contribution struct {
	ModelID                 string   `json:"model_id"`
	ModelVersion            string   `json:"model_version"`
	PromptHash              string   `json:"prompt_hash"`
	InputEvidenceIDs        []string `json:"input_evidence_ids"`
	InputEvidenceVersionIDs []string `json:"input_evidence_version_ids"`
	PolicyVersion           string   `json:"policy_version"`
	ToolCalls               []string `json:"tool_calls,omitempty"`
	OutputHash              string   `json:"output_hash"`
	HumanReviewer           string   `json:"human_reviewer"`
	Purpose                 string   `json:"purpose"`
	Tick                    uint64   `json:"tick"`
	// Material marks a contribution that shaped a finding. A material
	// contribution requires a named human reviewer.
	Material bool `json:"material"`
}

// HashText is the canonical hash for prompts and outputs.
func HashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NewContribution builds a contribution record, refusing an incomplete
// one. A material contribution without a named human reviewer is
// exactly the silent influence Article 27 forbids.
func NewContribution(m Model, prompt, output, purpose, policyVersion, reviewer string,
	evidenceIDs, evidenceVersionIDs, toolCalls []string, material bool, tick uint64) (Contribution, error) {

	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Version) == "" {
		return Contribution{}, ErrNoModelIdentity
	}
	if strings.TrimSpace(purpose) == "" {
		return Contribution{}, ErrNoPurpose
	}
	if strings.TrimSpace(policyVersion) == "" {
		return Contribution{}, errors.New("gateway: contribution must carry a policy version (Article 7)")
	}
	if material && strings.TrimSpace(reviewer) == "" {
		return Contribution{}, ErrNoHumanReviewer
	}
	c := Contribution{
		ModelID: m.ID, ModelVersion: m.Version,
		PromptHash: HashText(prompt), OutputHash: HashText(output),
		InputEvidenceIDs:        append([]string(nil), evidenceIDs...),
		InputEvidenceVersionIDs: append([]string(nil), evidenceVersionIDs...),
		PolicyVersion:           policyVersion, ToolCalls: append([]string(nil), toolCalls...),
		HumanReviewer: reviewer, Purpose: purpose, Tick: tick, Material: material,
	}
	sort.Strings(c.InputEvidenceIDs)
	sort.Strings(c.InputEvidenceVersionIDs)
	return c, nil
}

// Traceable reports whether a contribution can be audited back to its
// inputs -- the operational test for Article 27.
func (c Contribution) Traceable() bool {
	return c.ModelID != "" && c.ModelVersion != "" &&
		c.PromptHash != "" && c.OutputHash != "" &&
		c.PolicyVersion != "" && len(c.InputEvidenceVersionIDs) > 0 &&
		(!c.Material || c.HumanReviewer != "")
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
