// This file answers Commercialization Sprint item 5 directly: the
// Commercial API v1 surface, "API yang dapat dipahami customer, bukan
// hanya internal package." Every route is a thin HTTP adapter over
// pkg/commercial/api.Store -- this file carries no business logic of
// its own, matching this package's own "Gateway -> Engine Registry ->
// Kernel" principle. Every response is a curated, commercially
// understandable shape (evidencefabric.EvidenceRecord, dossier.Dossier,
// commercialapi.CaseView, ...) -- never a raw internal kernel type
// (cre.AuthorizedFinding, causation.HypothesisSet, ...) serialized
// verbatim, per the reviewer's own explicit instruction "Jangan expose
// internal kernel structures."
//
// Identity/Authorization for these routes is the SAME JWT+RBAC
// middleware NewServer already composes around every other route.
// HONEST GAP: TenantID is currently a caller-supplied field (request
// body or query parameter), not yet cryptographically bound to the
// verified JWT identity -- see docs/VERIQO_PILOT_MODE_AND_DEPLOYMENT_READINESS.md,
// "production identity/mTLS" (a MUST-CLOSE-BEFORE-PAID-PILOT item).
// pkg/commercial/api.Store's own tenant-namespaced isolation (see its
// own doc comment) is real and tested; binding the tenant claim to a
// verified token is the remaining integration step, named here rather
// than silently assumed.
package rest

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"os"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/commercial/packageverify"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
)

// commercialV1Routes returns every hand-written v1 route this file
// adds. A nil store (every caller of NewServer before this route
// family existed) makes every one of these routes 404, same as
// insuranceRoutes' and lifecycleRoutes' own nil-safety.
func commercialV1Routes(store *commercialapi.Store) map[string]http.HandlerFunc {
	if store == nil {
		return nil
	}
	return map[string]http.HandlerFunc{
		"POST /v1/evidence":             handleV1SubmitEvidence(store),
		"GET /v1/evidence/{id}":         handleV1GetEvidence(store),
		"POST /v1/evidence/{id}/verify": handleV1VerifyEvidence(store),
		"GET /v1/evidence/{id}/custody": handleV1GetCustody(store),
		"POST /v1/cases":                handleV1CreateCase(store),
		"GET /v1/cases/{id}":            handleV1GetCase(store),
		"POST /v1/cases/{id}/decide":    handleV1DecideCase(store),
		"POST /v1/cases/{id}/actions":   handleV1ActOnCase(store),
		"GET /v1/cases/{id}/dossier":    handleV1GetDossier(store),
		"GET /v1/cases/{id}/replay":     handleV1Replay(store),
		"POST /v1/packages/verify":      handleV1VerifyPackage(),
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// statusForStoreError maps a commercialapi error to the right HTTP
// status -- never a bare 500 for an ordinary "not found" or "wrong
// tenant" outcome.
func statusForStoreError(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case err == commercialapi.ErrCaseNotFound, err == commercialapi.ErrEvidenceNotFound:
		return http.StatusNotFound
	case err == commercialapi.ErrTenantMismatch:
		return http.StatusForbidden
	case err == commercialapi.ErrCaseAlreadyExists, err == commercialapi.ErrAlreadyDecided:
		return http.StatusConflict
	case err == commercialapi.ErrNotYetDecided:
		return http.StatusUnprocessableEntity
	case err == commercialapi.ErrEmptyTenantID, err == commercialapi.ErrEmptyCaseID, err == commercialapi.ErrEmptyEvidenceID:
		return http.StatusBadRequest
	default:
		return http.StatusUnprocessableEntity
	}
}

type reqDomainMetadata struct {
	Maritime  *evidencefabric.MaritimeMetadata  `json:"maritime,omitempty"`
	Insurance *evidencefabric.InsuranceMetadata `json:"insurance,omitempty"`
	Trade     *evidencefabric.TradeMetadata     `json:"trade,omitempty"`
}

func (r reqDomainMetadata) toDomain() evidencefabric.DomainMetadata {
	return evidencefabric.DomainMetadata{Maritime: r.Maritime, Insurance: r.Insurance, Trade: r.Trade}
}

type reqSubmitEvidence struct {
	TenantID   string            `json:"tenant_id"`
	CaseID     string            `json:"case_id"`
	EvidenceID string            `json:"evidence_id"`
	SHA256     string            `json:"sha256"`
	URI        string            `json:"uri"`
	Filename   string            `json:"filename"`
	MediaType  string            `json:"media_type"`
	ByteSize   int64             `json:"byte_size"`
	Collector  string            `json:"collector"`
	Source     string            `json:"source"`
	Domain     reqDomainMetadata `json:"domain"`
	Tick       uint64            `json:"tick"`
}

func handleV1SubmitEvidence(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req reqSubmitEvidence
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		rec, err := store.SubmitEvidence(commercialapi.EvidenceInput{
			TenantID: req.TenantID, CaseID: req.CaseID, EvidenceID: req.EvidenceID, SHA256: req.SHA256,
			URI: req.URI, Filename: req.Filename, MediaType: req.MediaType, ByteSize: req.ByteSize,
			Collector: req.Collector, Source: req.Source, Domain: req.Domain.toDomain(), Tick: req.Tick,
		})
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusCreated, rec)
	}
}

func handleV1GetEvidence(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		rec, err := store.GetEvidence(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, rec)
	}
}

func handleV1VerifyEvidence(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TenantID string `json:"tenant_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		verified, err := store.VerifyEvidence(req.TenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"verified": verified})
	}
}

func handleV1GetCustody(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		chain, err := store.GetCustody(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, chain)
	}
}

func handleV1CreateCase(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TenantID string `json:"tenant_id"`
			CaseID   string `json:"case_id"`
			Tick     uint64 `json:"tick"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if err := store.CreateCase(req.TenantID, req.CaseID, req.Tick); err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"case_id": req.CaseID})
	}
}

func handleV1GetCase(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		view, err := store.GetCase(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

type reqFindingInput struct {
	ContractBasis          string `json:"contract_basis"`
	ObligationRef          string `json:"obligation_ref"`
	EventRef               string `json:"event_ref"`
	QuantumRef             string `json:"quantum_ref"`
	SourceInferenceTraceID string `json:"source_inference_trace_id"`
	HumanReviewRequired    bool   `json:"human_review_required"`
	HumanReviewedBy        string `json:"human_reviewed_by"`
}

type reqDecideCase struct {
	TenantID   string `json:"tenant_id"`
	Hypothesis struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	} `json:"hypothesis"`
	SupportingEvidenceIDs    []string        `json:"supporting_evidence_ids"`
	ContradictingEvidenceIDs []string        `json:"contradicting_evidence_ids"`
	FindingID                string          `json:"finding_id"`
	Finding                  reqFindingInput `json:"finding"`
	Outcome                  string          `json:"outcome"`
	Rationale                string          `json:"rationale"`
	LedgerActor              string          `json:"ledger_actor"`
	Tick                     uint64          `json:"tick"`
}

// respDecision is the curated Decision summary -- never the internal
// decision.Decision type serialized as-is (it has no exported fields
// to serialize anyway, by design; this is the honest, intentional
// projection).
type respDecision struct {
	Outcome           string `json:"outcome"`
	Rationale         string `json:"rationale"`
	DecidedAt         uint64 `json:"decided_at"`
	Hash              string `json:"hash"`
	FindingHash       string `json:"finding_hash"`
	AuthorizationHash string `json:"authorization_hash"`
	HypothesisID      string `json:"hypothesis_id"`
}

func handleV1DecideCase(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req reqDecideCase
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		caseID := r.PathValue("id")
		d, err := store.DecideCase(commercialapi.DecideInput{
			TenantID: req.TenantID, CaseID: caseID,
			Hypothesis: causation.Hypothesis{
				ID: causation.HypothesisID(req.Hypothesis.ID), Description: req.Hypothesis.Description,
			},
			SupportingEvidenceIDs:    req.SupportingEvidenceIDs,
			ContradictingEvidenceIDs: req.ContradictingEvidenceIDs,
			FindingID:                req.FindingID,
			Finding: cre.FindingInput{
				CaseID: caseID, ContractBasis: req.Finding.ContractBasis, ObligationRef: req.Finding.ObligationRef,
				EventRef: req.Finding.EventRef, QuantumRef: req.Finding.QuantumRef,
				SourceInferenceTraceID: req.Finding.SourceInferenceTraceID,
				HumanReviewRequired:    req.Finding.HumanReviewRequired, HumanReviewedBy: req.Finding.HumanReviewedBy,
			},
			Outcome: decision.Outcome(req.Outcome), Rationale: req.Rationale, LedgerActor: req.LedgerActor, Tick: req.Tick,
		})
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, respDecision{
			Outcome: string(d.Outcome()), Rationale: d.Rationale(), DecidedAt: d.DecidedAt(), Hash: d.Hash(),
			FindingHash: d.FindingHash(), AuthorizationHash: d.AuthorizationHash(), HypothesisID: d.HypothesisID(),
		})
	}
}

type reqActOnCase struct {
	TenantID        string   `json:"tenant_id"`
	Actor           string   `json:"actor"`
	PolicyRef       string   `json:"policy_ref"`
	Scope           string   `json:"scope"`
	PermittedAction string   `json:"permitted_action"`
	Conditions      []string `json:"conditions"`
	AuthorizedAt    uint64   `json:"authorized_at"`
	ExpiresAt       uint64   `json:"expires_at"`
	ExecutingActor  string   `json:"executing_actor"`
	ExecutionAt     uint64   `json:"execution_at"`
	LedgerActor     string   `json:"ledger_actor"`
}

type respAction struct {
	Authorization struct {
		Actor           string   `json:"actor"`
		PolicyRef       string   `json:"policy_ref"`
		Scope           string   `json:"scope"`
		PermittedAction string   `json:"permitted_action"`
		Conditions      []string `json:"conditions,omitempty"`
		AuthorizedAt    uint64   `json:"authorized_at"`
		ExpiresAt       uint64   `json:"expires_at"`
		Hash            string   `json:"hash"`
	} `json:"authorization"`
	Receipt struct {
		ReceiptID        string `json:"receipt_id"`
		ExecutedBy       string `json:"executed_by"`
		ExecutedAt       uint64 `json:"executed_at"`
		LedgerRecordHash string `json:"ledger_record_hash"`
	} `json:"receipt"`
}

func handleV1ActOnCase(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req reqActOnCase
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		aa, receipt, err := store.ActOnCase(commercialapi.ActionInput{
			TenantID: req.TenantID, CaseID: r.PathValue("id"), Actor: req.Actor, PolicyRef: req.PolicyRef,
			Scope: req.Scope, PermittedAction: action.Action(req.PermittedAction), Conditions: req.Conditions,
			AuthorizedAt: req.AuthorizedAt, ExpiresAt: req.ExpiresAt, ExecutingActor: req.ExecutingActor,
			ExecutionAt: req.ExecutionAt, LedgerActor: req.LedgerActor,
		})
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		var resp respAction
		resp.Authorization.Actor = aa.Actor()
		resp.Authorization.PolicyRef = aa.PolicyRef()
		resp.Authorization.Scope = aa.Scope()
		resp.Authorization.PermittedAction = string(aa.PermittedAction())
		resp.Authorization.Conditions = aa.Conditions()
		resp.Authorization.AuthorizedAt = aa.AuthorizedAt()
		resp.Authorization.ExpiresAt = aa.ExpiresAt()
		resp.Authorization.Hash = aa.Hash()
		resp.Receipt.ReceiptID = receipt.ReceiptID
		resp.Receipt.ExecutedBy = receipt.ExecutedBy
		resp.Receipt.ExecutedAt = receipt.ExecutedAt
		resp.Receipt.LedgerRecordHash = receipt.LedgerRecordHash
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleV1GetDossier serves the Dossier's "Human" form (JSON, the
// same content RenderMarkdown/the PDF renderer would present) by
// default, or its "Machine" form (a .zip download) when
// ?format=package is given.
func handleV1GetDossier(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		caseID := r.PathValue("id")

		if r.URL.Query().Get("format") == "package" {
			tmp, err := os.CreateTemp("", "veriqo-dossier-*.zip")
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, err)
				return
			}
			tmpPath := tmp.Name()
			tmp.Close()
			defer os.Remove(tmpPath)

			if _, err := store.WriteDossierPackage(tenantID, caseID, tmpPath); err != nil {
				writeAPIError(w, statusForStoreError(err), err)
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", "attachment; filename=\""+caseID+"-dossier.zip\"")
			http.ServeFile(w, r, tmpPath)
			return
		}

		d, err := store.GenerateDossier(tenantID, caseID)
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
}

func handleV1Replay(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenant_id")
		result, err := store.Replay(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// handleV1VerifyPackage is stateless and requires no tenant scoping --
// verifying an independently-held Machine Package is, by design, a
// capability anyone holding the file has, per pkg/commercial/
// packageverify's whole purpose.
func handleV1VerifyPackage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tmp, err := os.CreateTemp("", "veriqo-verify-upload-*.zip")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if _, err := tmp.ReadFrom(r.Body); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}

		zr, err := zip.OpenReader(tmp.Name())
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		defer zr.Close()

		results, err := packageverify.VerifyZip(&zr.Reader)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		status := http.StatusOK
		if !packageverify.AllPassed(results) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]any{
			"all_passed": packageverify.AllPassed(results),
			"checks":     results,
		})
	}
}
