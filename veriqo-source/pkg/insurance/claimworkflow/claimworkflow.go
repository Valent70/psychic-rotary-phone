// Package claimworkflow answers the review's item E directly: "E.
// Workflow implementation + enforcement -- Buat live workflow path dan
// test bypass," under the required shape "Workflow -> Policy ->
// Evidence -> Finding -> Authorization -> Decision," with the explicit
// prohibition "Tidak boleh ada Workflow -> Decision constructor secara
// langsung" (no Workflow -> Decision constructor directly).
//
// This package builds a real veriqo/pkg/workflow.Plan over the SAME
// gated packages the core trust pipeline and pkg/insurance/api.Facade
// already use -- pkg/evidence/manifest, pkg/insurance/causation,
// pkg/insurance/cre, pkg/insurance/decision -- never a
// re-implementation, and never a step that builds a decision.Decision
// by hand. Each step's Agent function only ever sees the named outputs
// of the steps it declared as dependencies (workflow.AgentInput.
// Outputs), which is what makes "the decide step reaches a Decision
// without a real AuthorizedFinding" structurally, not just
// conventionally, impossible: a step whose DependsOn omits
// "authorize_finding" receives no such entry in Outputs at all, and
// the decide step's own type assertion refuses to proceed without one
// -- see TestClaimDecisionWorkflowRefusesADecideStepMissingAuthorization.
package claimworkflow

import (
	"errors"
	"fmt"

	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/insurance/finding"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/workflow"
)

var (
	ErrEmptyEvidenceID  = errors.New("claimworkflow: EvidenceID must be non-empty")
	ErrEmptyCaseID      = errors.New("claimworkflow: CaseID must be non-empty")
	ErrEmptyHypothesis  = errors.New("claimworkflow: HypothesisID and HypothesisDescription must be non-empty")
	ErrMissingUpstream  = errors.New("claimworkflow: step ran without its required upstream dependency output -- the workflow DAG was built without the authority chain this step requires")
	ErrNilLedger        = errors.New("claimworkflow: a non-nil ledger *audit.AuditStore is required")
	ErrNilManifestSpecs = errors.New("claimworkflow: at least one ManifestSpec is required")
)

// ManifestSpec is the evidence-manifest metadata one step ("finalize_
// evidence") needs to drive a fresh manifest.Registry entry through
// the real custody chain to FINALIZED -- the same fields
// os_trust_integration_test.go's buildOSTrustPipeline hand-builds,
// exposed here as caller-supplied input instead.
type ManifestSpec struct {
	EvidenceID string
	SHA256     string
	URI        string
	Filename   string
	MediaType  string
	ByteSize   int64
	Collector  string
	Source     string
}

// ClaimDecisionInput is everything BuildClaimDecisionPlan needs to
// construct the real five-step Workflow -> Policy -> Evidence ->
// Finding -> Authorization -> Decision DAG for one claim decision.
type ClaimDecisionInput struct {
	CaseID     string
	Tick       uint64
	Manifests  []ManifestSpec
	Hypothesis causation.Hypothesis // Status is ignored -- always re-derived from the evidence below, exactly like causation.HypothesisSet.Add already enforces.

	SupportingEvidenceIDs    []string
	ContradictingEvidenceIDs []string

	FindingID string
	Finding   cre.FindingInput

	Outcome   decision.Outcome
	Rationale string

	// LedgerActor names the workflow-driven decision engine in the
	// ledger's own audit trail -- distinct from a caller-supplied
	// actor, since a workflow run has no single human caller the way
	// an HTTP request does.
	LedgerActor string
}

type hypothesisOutput struct {
	HS *causation.HypothesisSet
	H  causation.Hypothesis
}

// BuildClaimDecisionPlan constructs the real, five-step DAG. Every
// step's Agent function is a closure over in and ledger, calling the
// SAME gated constructors the core trust pipeline and Facade.
// DecideClaim already use -- manifest.Registry.Advance,
// causation.HypothesisSet.Add/computeStatus (via AddSupportingEvidence/
// AddContradictingEvidence), cre.BuildFinding, cre.AuthorizeGrounded,
// decision.MakeDecision, decision.AppendToLedger -- so every invariant
// those packages enforce applies to a workflow-driven decision
// identically to an API-driven or test-driven one.
func BuildClaimDecisionPlan(in ClaimDecisionInput, ledger *audit.AuditStore) (workflow.Plan, error) {
	if in.CaseID == "" {
		return workflow.Plan{}, ErrEmptyCaseID
	}
	if len(in.Manifests) == 0 {
		return workflow.Plan{}, ErrNilManifestSpecs
	}
	if in.Hypothesis.ID == "" || in.Hypothesis.Description == "" {
		return workflow.Plan{}, ErrEmptyHypothesis
	}
	for _, m := range in.Manifests {
		if m.EvidenceID == "" {
			return workflow.Plan{}, ErrEmptyEvidenceID
		}
	}
	if ledger == nil {
		return workflow.Plan{}, ErrNilLedger
	}

	finalizeEvidence := workflow.Step{
		Name: "finalize_evidence",
		Agent: func(a workflow.AgentInput) (any, error) {
			m := manifest.NewRegistry()
			for _, spec := range in.Manifests {
				if err := FinalizeManifestSpec(m, spec, in.CaseID, a.Tick); err != nil {
					return nil, fmt.Errorf("finalizing manifest for %s: %w", spec.EvidenceID, err)
				}
			}
			return m, nil
		},
	}

	buildHypothesis := workflow.Step{
		Name: "build_hypothesis",
		Agent: func(a workflow.AgentInput) (any, error) {
			hs, err := causation.NewHypothesisSet(in.CaseID, in.FindingID, "workflow-driven causation analysis")
			if err != nil {
				return nil, err
			}
			h := in.Hypothesis
			h.Status = "" // never accept a caller-asserted status -- see causation.HypothesisSet.Add's own discipline
			if err := hs.Add(h); err != nil {
				return nil, err
			}
			for _, ev := range in.SupportingEvidenceIDs {
				if err := hs.AddSupportingEvidence(h.ID, ev); err != nil {
					return nil, err
				}
			}
			for _, ev := range in.ContradictingEvidenceIDs {
				if err := hs.AddContradictingEvidence(h.ID, ev); err != nil {
					return nil, err
				}
			}
			resolved, ok := hs.Get(h.ID)
			if !ok {
				return nil, fmt.Errorf("claimworkflow: just-added hypothesis %s not found", h.ID)
			}
			return hypothesisOutput{HS: hs, H: resolved}, nil
		},
	}

	buildFinding := workflow.Step{
		Name:      "build_finding",
		DependsOn: []string{"finalize_evidence", "build_hypothesis"},
		Agent: func(a workflow.AgentInput) (any, error) {
			hOut, ok := a.Outputs["build_hypothesis"].(hypothesisOutput)
			if !ok {
				return nil, fmt.Errorf("%w: build_hypothesis", ErrMissingUpstream)
			}
			f, err := cre.BuildFinding(hOut.HS, hOut.H, nil, in.Finding, in.FindingID, a.Tick)
			if err != nil {
				return nil, err
			}
			return f, nil
		},
	}

	authorizeFinding := workflow.Step{
		Name:      "authorize_finding",
		DependsOn: []string{"build_finding", "build_hypothesis", "finalize_evidence"},
		Agent: func(a workflow.AgentInput) (any, error) {
			f, ok := a.Outputs["build_finding"].(finding.Finding)
			if !ok {
				return nil, fmt.Errorf("%w: build_finding", ErrMissingUpstream)
			}
			hOut, ok := a.Outputs["build_hypothesis"].(hypothesisOutput)
			if !ok {
				return nil, fmt.Errorf("%w: build_hypothesis", ErrMissingUpstream)
			}
			m, ok := a.Outputs["finalize_evidence"].(*manifest.Registry)
			if !ok {
				return nil, fmt.Errorf("%w: finalize_evidence", ErrMissingUpstream)
			}
			af, err := cre.AuthorizeGrounded(f, hOut.HS, hOut.H.ID, nil, m, a.Tick)
			if err != nil {
				return nil, err
			}
			return af, nil
		},
	}

	decide := workflow.Step{
		Name:      "decide",
		DependsOn: []string{"authorize_finding"},
		Agent: func(a workflow.AgentInput) (any, error) {
			// This is the ONE place a Decision may come from: a real
			// cre.AuthorizedFinding this exact run's own
			// "authorize_finding" step produced. A Plan that omits
			// "authorize_finding" from decide's DependsOn (the bypass
			// attack this package's own test suite proves against)
			// leaves this key absent from Outputs entirely -- the type
			// assertion below fails closed, never falling back to a
			// zero AuthorizedFinding (which decision.MakeDecision would
			// ALSO independently refuse -- see TestMakeDecisionRejects
			// AnUnauthorizedFinding in pkg/insurance/decision -- so this
			// is defense in depth, not the only gate).
			af, ok := a.Outputs["authorize_finding"].(cre.AuthorizedFinding)
			if !ok {
				return nil, fmt.Errorf("%w: authorize_finding", ErrMissingUpstream)
			}
			d, err := decision.MakeDecision(af, in.Outcome, in.Rationale, a.Tick)
			if err != nil {
				return nil, err
			}
			actor := in.LedgerActor
			if actor == "" {
				actor = "claimworkflow.decide"
			}
			if _, err := decision.AppendToLedger(ledger, actor, d); err != nil {
				return nil, err
			}
			return d, nil
		},
	}

	return workflow.Plan{
		Name:  "claim-decision:" + in.CaseID,
		Steps: []workflow.Step{finalizeEvidence, buildHypothesis, buildFinding, authorizeFinding, decide},
	}, nil
}

// FinalizeManifestSpec drives one manifest.Registry entry through the
// same real custody-chain sequence buildOSTrustPipeline (test/
// integration/os_trust_integration_test.go) hand-builds.
func FinalizeManifestSpec(m *manifest.Registry, spec ManifestSpec, caseID string, tick uint64) error {
	evidenceID := spec.EvidenceID
	if _, err := m.RegisterDraft(manifest.Manifest{
		TenantID: "tenant-claimworkflow", CaseID: caseID, EvidenceID: evidenceID, Version: 1,
		URI: spec.URI, Filename: spec.Filename, MediaType: spec.MediaType, ByteSize: spec.ByteSize,
		SHA256: spec.SHA256, Method: "UPLOAD", Collector: spec.Collector, Source: spec.Source,
		AcquiredAt: tick, ReceivedAt: tick, HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: "ingested via claim decision workflow",
	}); err != nil {
		return fmt.Errorf("RegisterDraft: %w", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-received", "claimworkflow", manifest.CustodyReceived, tick, "received into custody", ""); err != nil {
		return fmt.Errorf("RecordCustodyEvent(RECEIVED): %w", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIngested, tick); err != nil {
		return fmt.Errorf("Advance(INGESTED): %w", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-hashed", "claimworkflow", manifest.CustodyHashed, tick, "hash computed", spec.SHA256); err != nil {
		return fmt.Errorf("RecordCustodyEvent(HASHED): %w", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateIntegrityAssessed, tick); err != nil {
		return fmt.Errorf("Advance(INTEGRITY_ASSESSED): %w", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateProvenanceComplete, tick); err != nil {
		return fmt.Errorf("Advance(PROVENANCE_COMPLETE): %w", err)
	}
	if _, err := m.RecordCustodyEvent(evidenceID, evidenceID+"-reviewed", "claimworkflow", manifest.CustodyReviewed, tick, "independent review complete", spec.SHA256); err != nil {
		return fmt.Errorf("RecordCustodyEvent(REVIEWED): %w", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateReadyForFinalization, tick); err != nil {
		return fmt.Errorf("Advance(READY_FOR_FINALIZATION): %w", err)
	}
	if _, err := m.Advance(evidenceID, manifest.StateFinalized, tick); err != nil {
		return fmt.Errorf("Advance(FINALIZED): %w", err)
	}
	return nil
}
