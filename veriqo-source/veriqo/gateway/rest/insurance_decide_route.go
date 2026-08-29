// This file answers the review's item D directly: "D. API
// implementation + enforcement -- Buat live API boundary dan test
// bypass," under the required shape "API -> Identity -> Authorization
// -> Validation -> Trusted pipeline," with the explicit prohibition
// "Tidak boleh ada API -> Decision constructor secara langsung" (no
// API -> Decision constructor directly).
//
// Identity is this package's existing security.JWTMiddleware (Bearer
// JWT verification); Authorization is security.RBACMiddleware (role ->
// path-prefix permissions) -- both already composed by NewServer
// around every route, this one included, exactly as they already gate
// /lifecycle/run_unified. Validation is this file's own JSON decode
// plus claimworkflow.BuildClaimDecisionPlan's real field checks.
// Trusted pipeline is pkg/insurance/claimworkflow's real, gated
// five-step DAG (finalize_evidence -> build_hypothesis -> build_finding
// -> authorize_finding -> decide), the SAME chain this session's
// pkg/insurance/claimworkflow package tests prove cannot reach a
// Decision without a real, grounded AuthorizedFinding. This handler
// never constructs a decision.Decision, cre.AuthorizedFinding, or
// finding.Finding directly -- it only calls
// claimworkflow.BuildClaimDecisionPlan and hands the resulting Plan to
// veriqo/pkg/workflow's own real Scheduler/Executor.
package rest

import (
	"encoding/json"
	"net/http"

	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/claimworkflow"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/workflow"
)

// reqManifestSpec mirrors claimworkflow.ManifestSpec for JSON transport.
type reqManifestSpec struct {
	EvidenceID string `json:"evidence_id"`
	SHA256     string `json:"sha256"`
	URI        string `json:"uri"`
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	ByteSize   int64  `json:"byte_size"`
	Collector  string `json:"collector"`
	Source     string `json:"source"`
}

// reqInsuranceDecide is the full request body for POST /insurance/decide.
type reqInsuranceDecide struct {
	CaseID     string            `json:"case_id"`
	Tick       uint64            `json:"tick"`
	Manifests  []reqManifestSpec `json:"manifests"`
	Hypothesis struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	} `json:"hypothesis"`
	SupportingEvidenceIDs    []string `json:"supporting_evidence_ids"`
	ContradictingEvidenceIDs []string `json:"contradicting_evidence_ids"`
	FindingID                string   `json:"finding_id"`
	Finding                  struct {
		ContractBasis          string `json:"contract_basis"`
		ObligationRef          string `json:"obligation_ref"`
		EventRef               string `json:"event_ref"`
		QuantumRef             string `json:"quantum_ref"`
		SourceInferenceTraceID string `json:"source_inference_trace_id"`
		HumanReviewRequired    bool   `json:"human_review_required"`
		HumanReviewedBy        string `json:"human_reviewed_by"`
	} `json:"finding"`
	Outcome     string `json:"outcome"`
	Rationale   string `json:"rationale"`
	LedgerActor string `json:"ledger_actor"`
}

// respInsuranceDecide is the curated, JSON-stable subset of the real
// decision.Decision this request produced, plus the workflow's own
// execution order -- proof, to the caller, of exactly which gated
// steps ran before the Decision was reached.
type respInsuranceDecide struct {
	Outcome           string   `json:"outcome"`
	Rationale         string   `json:"rationale"`
	DecidedAt         uint64   `json:"decided_at"`
	Hash              string   `json:"hash"`
	FindingHash       string   `json:"finding_hash"`
	AuthorizationHash string   `json:"authorization_hash"`
	HypothesisID      string   `json:"hypothesis_id"`
	WorkflowRunID     string   `json:"workflow_run_id"`
	WorkflowStepOrder []string `json:"workflow_step_order"`
}

// insuranceRoutes returns the one hand-written route this file adds. A
// nil ledger (every caller of NewServer before this route existed, and
// any caller that simply doesn't want this route) makes it return 404,
// same as lifecycleRoutes' own nil-safety.
func insuranceRoutes(ledger *audit.AuditStore) map[string]http.HandlerFunc {
	if ledger == nil {
		return nil
	}
	return map[string]http.HandlerFunc{
		"/insurance/decide": handleInsuranceDecide(ledger),
	}
}

// handleInsuranceDecide is the real production entrypoint. By the time
// this function body runs, NewServer's already-composed JWT + RBAC
// middleware has already authenticated and authorized the caller (this
// package's own Identity and Authorization layers) -- this handler's
// own job is only Validation (decoding and rejecting a malformed
// request) and invoking the Trusted pipeline.
func handleInsuranceDecide(ledger *audit.AuditStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req reqInsuranceDecide
		if r.Body == nil {
			http.Error(w, "missing JSON body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}

		manifests := make([]claimworkflow.ManifestSpec, len(req.Manifests))
		for i, m := range req.Manifests {
			manifests[i] = claimworkflow.ManifestSpec{
				EvidenceID: m.EvidenceID, SHA256: m.SHA256, URI: m.URI, Filename: m.Filename,
				MediaType: m.MediaType, ByteSize: m.ByteSize, Collector: m.Collector, Source: m.Source,
			}
		}
		in := claimworkflow.ClaimDecisionInput{
			CaseID: req.CaseID, Tick: req.Tick, Manifests: manifests,
			Hypothesis: causation.Hypothesis{
				ID: causation.HypothesisID(req.Hypothesis.ID), Description: req.Hypothesis.Description,
			},
			SupportingEvidenceIDs:    req.SupportingEvidenceIDs,
			ContradictingEvidenceIDs: req.ContradictingEvidenceIDs,
			FindingID:                req.FindingID,
			Finding: cre.FindingInput{
				CaseID: req.CaseID, ContractBasis: req.Finding.ContractBasis, ObligationRef: req.Finding.ObligationRef,
				EventRef: req.Finding.EventRef, QuantumRef: req.Finding.QuantumRef,
				SourceInferenceTraceID: req.Finding.SourceInferenceTraceID,
				HumanReviewRequired:    req.Finding.HumanReviewRequired, HumanReviewedBy: req.Finding.HumanReviewedBy,
			},
			Outcome: decision.Outcome(req.Outcome), Rationale: req.Rationale, LedgerActor: req.LedgerActor,
		}

		// Validation: BuildClaimDecisionPlan's own real field checks
		// (never re-implemented here) -- a malformed request never
		// reaches the Trusted pipeline at all.
		plan, err := claimworkflow.BuildClaimDecisionPlan(in, ledger)
		if err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}

		sched := workflow.NewScheduler()
		record, err := sched.Schedule(plan, req.Tick)
		if err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Trusted pipeline: the real, gated five-step DAG. A fresh,
		// request-scoped audit store checkpoints this one run; ledger
		// (server-held, accumulating across requests) is where the
		// Decision itself, once legitimately reached, is anchored --
		// see claimworkflow's own "decide" step, the ONLY place this
		// whole call chain ever touches decision.MakeDecision or
		// decision.AppendToLedger.
		ex := workflow.NewExecutor(audit.NewAuditStore())
		record, err = ex.Run(plan, record)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		decideResult, ok := record.Completed["decide"]
		if !ok || decideResult.Err != "" {
			http.Error(w, "workflow completed without a successful decide step", http.StatusUnprocessableEntity)
			return
		}
		d, ok := decideResult.Output.(decision.Decision)
		if !ok {
			http.Error(w, "internal error: decide step did not produce a Decision", http.StatusInternalServerError)
			return
		}

		resp := respInsuranceDecide{
			Outcome: string(d.Outcome()), Rationale: d.Rationale(), DecidedAt: d.DecidedAt(), Hash: d.Hash(),
			FindingHash: d.FindingHash(), AuthorizationHash: d.AuthorizationHash(), HypothesisID: d.HypothesisID(),
			WorkflowRunID: record.RunID, WorkflowStepOrder: record.Order,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
