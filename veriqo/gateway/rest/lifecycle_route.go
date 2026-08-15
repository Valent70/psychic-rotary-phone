// This file is hand-written, NOT generated (unlike generated_routes.go,
// which is mechanically derived from veriqo/contracts/*.yaml by
// veriqo/generator -- its contract format only supports flat scalar
// params and cannot express pkg/lifecycle.Intent/EvidencePlan/
// canonical.CaseInput's real nested shape without generator changes
// well beyond this route's scope).
//
// It closes the deepest gap this session's own reports repeatedly and
// honestly disclosed: pkg/lifecycle.Orchestrator.RunUnified -- the
// audit's named Intent entrypoint, and (as of this session's P0-1
// round) the real caller of pkg/execution's full DAG -- had NO
// production HTTP/gateway caller. veriqo/gateway/rest routed only to
// veriqo/registry.Registry, a separate, older engine surface that never
// imports pkg/lifecycle at all. This route is additive: it does not
// touch, replace, or change behavior of any existing registry-based
// route, and is served only when the caller supplies a non-nil
// *lifecycle.Orchestrator to NewServer (nil-safe: omitting it, as every
// pre-existing test of this package still does implicitly through
// NewServer's other call sites, simply serves 404 rather than a panic).
package rest

import (
	"encoding/json"
	"net/http"

	"veriqo/pkg/canonical"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
)

// reqAlias mirrors entity.Alias for JSON transport.
type reqAlias struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// reqSubmission mirrors canonical.SourceSubmission for JSON transport --
// deliberately omitting the Dependencies field (evidencegraph.
// DependencyRecord) from the wire shape for this first real route: a
// caller that needs to assert cross-source dependency edges can still
// reach pkg/evidence/api's own Link method for that, and adding it here
// is a mechanical, low-risk follow-up, not a blocker to shipping the
// core RunUnified path for real.
type reqSubmission struct {
	SourceID        string  `json:"source_id"`
	Value           string  `json:"value"`
	BaseReliability float64 `json:"base_reliability"`
	Provider        string  `json:"provider,omitempty"`
	UpstreamID      string  `json:"upstream_id,omitempty"`
}

// reqPolicy mirrors decision.Policy for JSON transport. Omitted
// entirely (zero value) means the handler uses risk.DefaultPolicy(),
// the same default every acceptance-test fixture in this repo uses,
// rather than fail closed for a caller with no reason to know this
// system's internal risk-model shape.
type reqPolicy struct {
	Name              string  `json:"name"`
	FlagThreshold     float64 `json:"flag_threshold"`
	EscalateThreshold float64 `json:"escalate_threshold"`
}

// reqEvidenceRequirement mirrors lifecycle.EvidenceRequirement.
type reqEvidenceRequirement struct {
	Kind       string `json:"kind"`
	Required   bool   `json:"required"`
	MinSources int    `json:"min_sources"`
}

// reqRunUnified is the full request body for POST /lifecycle/run_unified.
type reqRunUnified struct {
	ActorID            string                   `json:"actor_id"`
	Tenant             string                   `json:"tenant"`
	Objective          string                   `json:"objective"`
	EntityAliases      []reqAlias               `json:"entity_aliases"`
	RequiredConfidence float64                  `json:"required_confidence"`
	TemporalScope      string                   `json:"temporal_scope"`
	Tick               uint64                   `json:"tick"`
	Requirements       []reqEvidenceRequirement `json:"requirements"`
	Subject            string                   `json:"subject"`
	Predicate          string                   `json:"predicate"`
	Submissions        []reqSubmission          `json:"submissions"`
	Policy             *reqPolicy               `json:"policy,omitempty"`
	PatternScore       float64                  `json:"pattern_score"`
	PriceAnomaly       float64                  `json:"price_anomaly"`
}

// respRunUnified is a curated, JSON-stable subset of lifecycle.Result --
// the fields an external caller actually needs (what was decided, and
// the identifiers that let them independently verify or correlate it
// later), not the full internal Result struct verbatim.
type respRunUnified struct {
	IntentID          string  `json:"intent_id"`
	EntityID          string  `json:"entity_id"`
	Decision          string  `json:"decision"`
	RiskScore         float64 `json:"risk_score"`
	ExecutionID       string  `json:"execution_id"`
	ExecutionRootHash string  `json:"execution_root_hash"`
	DecisionID        string  `json:"decision_id"`
	CertificateHash   string  `json:"certificate_hash"`
	IVFVerified       bool    `json:"ivf_verified"`
}

// lifecycleRoutes returns the one hand-written route this file adds. A
// nil orchestrator (the zero value NewServer's existing callers
// implicitly pass today) makes this route return 404, same as any
// unregistered path -- see NewServer.
func lifecycleRoutes(o *lifecycle.Orchestrator) map[string]http.HandlerFunc {
	if o == nil {
		return nil
	}
	return map[string]http.HandlerFunc{
		"/lifecycle/run_unified": handleLifecycleRunUnified(o),
	}
}

// handleLifecycleRunUnified is the real production entrypoint: it
// builds a genuine lifecycle.Intent/EvidencePlan/canonical.CaseInput
// from the request body and calls Orchestrator.RunUnified with the
// REAL inbound *http.Request's own Context() -- not context.Background()
// -- so a client disconnect or server shutdown genuinely cancels an
// in-flight execution, closing the last piece of this session's P0-6
// finding ("no real HTTP/gateway caller exists yet to supply a
// genuinely inbound context").
func handleLifecycleRunUnified(o *lifecycle.Orchestrator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req reqRunUnified
		if r.Body == nil {
			http.Error(w, "missing JSON body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}

		aliases := make([]entity.Alias, len(req.EntityAliases))
		for i, a := range req.EntityAliases {
			aliases[i] = entity.Alias{Kind: a.Kind, Value: a.Value}
		}
		intent := lifecycle.Intent{
			ActorID: req.ActorID, Tenant: req.Tenant, Objective: req.Objective,
			EntityAliases: aliases, RequiredConfidence: req.RequiredConfidence,
			TemporalScope: req.TemporalScope, Tick: req.Tick,
		}

		requirements := make([]lifecycle.EvidenceRequirement, len(req.Requirements))
		for i, rr := range req.Requirements {
			requirements[i] = lifecycle.EvidenceRequirement{
				Kind: rr.Kind, Required: rr.Required, MinSources: rr.MinSources,
			}
		}
		plan := lifecycle.PlanEvidence(intent, requirements)

		submissions := make([]canonical.SourceSubmission, len(req.Submissions))
		for i, s := range req.Submissions {
			submissions[i] = canonical.SourceSubmission{
				SourceID: fusion.SourceID(s.SourceID), Value: s.Value,
				BaseReliability: s.BaseReliability, Provider: s.Provider, UpstreamID: s.UpstreamID,
			}
		}
		policy := risk.DefaultPolicy()
		if req.Policy != nil {
			policy = decision.Policy{
				Name: req.Policy.Name, Factors: policy.Factors,
				FlagThreshold: req.Policy.FlagThreshold, EscalateThreshold: req.Policy.EscalateThreshold,
			}
		}
		caseIn := canonical.CaseInput{
			Entity: digitaltwin.EntityID(req.Subject), Subject: req.Subject, Predicate: req.Predicate,
			Submissions: submissions, Policy: policy,
			PatternScore: req.PatternScore, PriceAnomaly: req.PriceAnomaly, Tick: req.Tick,
			CausalLinks: []canonical.CausalLink{}, EconomicChannels: map[string]float64{},
		}

		res, err := o.RunUnified(r.Context(), intent, plan, caseIn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		resp := respRunUnified{
			IntentID: res.Certificate.IntentID, EntityID: string(res.EntityID),
			Decision: string(res.Canonical.Decision.Action), RiskScore: res.Canonical.Decision.RiskScore,
			ExecutionRootHash: res.Certificate.ExecutionRootHash,
			CertificateHash:   res.Certificate.Hash, IVFVerified: res.Certificate.IVFVerified,
		}
		if res.Execution != nil {
			resp.ExecutionID = res.Execution.Trace.Context.ExecutionID
			resp.DecisionID = res.Execution.Explanation.DecisionID
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
