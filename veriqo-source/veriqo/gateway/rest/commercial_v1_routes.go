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
//
// TENANT BINDING (Commercialization Sprint P0-B, closing the prior
// round's own named gap "TenantID is currently a caller-supplied
// field ... not yet cryptographically bound to the verified JWT
// identity"): every tenant-scoped handler below resolves its effective
// tenant through effectiveTenantID, never by trusting the request body
// or query string directly. When this deployment has JWTMiddleware
// configured (a verified subject is present in the request context),
// the caller-supplied tenant_id MUST be one that subject's
// pkg/commercial/tenancy.Membership grant covers -- refused with 403
// otherwise, even for a perfectly valid JWT naming a different tenant.
// When this deployment has no JWTMiddleware configured at all (no
// authenticated identity exists to bind to), the caller-supplied
// tenant_id is used exactly as the pre-P0-B behavior did -- binding is
// meaningless without an identity in the first place, and this is the
// mode every pre-P0-B test of these routes still runs in.
package rest

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	commercialapi "veriqo/pkg/commercial/api"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/commercial/packageverify"
	"veriqo/pkg/commercial/tenancy"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/security"
)

var (
	errTenantIDRequiredForAuthenticatedCaller = errors.New("tenant_id is required for an authenticated caller")
	errTenantNotAuthorizedForSubject          = errors.New("the authenticated subject is not authorized to act as this tenant")
)

// Commercialization Sprint P0-6 security hardening: none of these
// routes previously bounded request body size before decoding, an
// unbounded-body-size DoS vector (a caller can send an arbitrarily
// large body and force this process to buffer all of it). maxJSONBody
// covers every JSON metadata route; maxPackageUpload is larger,
// sized for the raw .zip bytes handleV1VerifyPackage accepts.
const (
	maxJSONBodyBytes      = 1 << 20  // 1 MiB -- generous for any JSON metadata payload these routes accept
	maxPackageUploadBytes = 64 << 20 // 64 MiB -- generous for a Machine Package .zip
)

// limitBody wraps r.Body in http.MaxBytesReader so a decode that reads
// past limit fails cleanly (json.Decode returns an error whose message
// names the limit) instead of this process buffering an unbounded
// request body in memory.
func limitBody(w http.ResponseWriter, r *http.Request, limit int64) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
}

// effectiveTenantID is the one place these routes decide which tenant
// a request may act as -- see this file's own doc comment for the
// full rationale.
func effectiveTenantID(r *http.Request, membership *tenancy.Membership, requestedTenantID string) (string, error) {
	claims, ok := security.ClaimsFromContext(r.Context())
	if !ok {
		return requestedTenantID, nil
	}
	if requestedTenantID == "" {
		return "", errTenantIDRequiredForAuthenticatedCaller
	}
	if membership == nil || !membership.IsAuthorized(claims.Subject, requestedTenantID) {
		return "", errTenantNotAuthorizedForSubject
	}
	return requestedTenantID, nil
}

func writeTenantBindingError(w http.ResponseWriter, err error) {
	status := http.StatusForbidden
	if errors.Is(err, errTenantIDRequiredForAuthenticatedCaller) {
		status = http.StatusBadRequest
	}
	writeAPIError(w, status, err)
}

// commercialV1Routes returns every hand-written v1 route this file
// adds. A nil store (every caller of NewServer before this route
// family existed) makes every one of these routes 404, same as
// insuranceRoutes' and lifecycleRoutes' own nil-safety. membership may
// be nil (see effectiveTenantID: an authenticated caller then always
// refuses, since there is nothing to authorize against -- a safe,
// fail-closed default, never a silent fall-back to trusting the
// client).
func commercialV1Routes(store *commercialapi.Store, membership *tenancy.Membership) map[string]http.HandlerFunc {
	if store == nil {
		return nil
	}
	return map[string]http.HandlerFunc{
		"POST /v1/evidence":             handleV1SubmitEvidence(store, membership),
		"GET /v1/evidence/{id}":         handleV1GetEvidence(store, membership),
		"POST /v1/evidence/{id}/verify": handleV1VerifyEvidence(store, membership),
		"GET /v1/evidence/{id}/custody": handleV1GetCustody(store, membership),
		"POST /v1/cases":                handleV1CreateCase(store, membership),
		"GET /v1/cases/{id}":            handleV1GetCase(store, membership),
		"POST /v1/cases/{id}/decide":    handleV1DecideCase(store, membership),
		"POST /v1/cases/{id}/actions":   handleV1ActOnCase(store, membership),
		"GET /v1/cases/{id}/dossier":    handleV1GetDossier(store, membership),
		"GET /v1/cases/{id}/replay":     handleV1Replay(store, membership),
		"POST /v1/packages/verify":      handleV1VerifyPackage(),
		"GET /v1/metrics":               handleV1Metrics(store),
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

func handleV1SubmitEvidence(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r, maxJSONBodyBytes)
		var req reqSubmitEvidence
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		tenantID, err := effectiveTenantID(r, membership, req.TenantID)
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		rec, err := store.SubmitEvidence(commercialapi.EvidenceInput{
			TenantID: tenantID, CaseID: req.CaseID, EvidenceID: req.EvidenceID, SHA256: req.SHA256,
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

func handleV1GetEvidence(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := effectiveTenantID(r, membership, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		rec, err := store.GetEvidence(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, rec)
	}
}

func handleV1VerifyEvidence(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r, maxJSONBodyBytes)
		var req struct {
			TenantID string `json:"tenant_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tenantID, err := effectiveTenantID(r, membership, req.TenantID)
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		verified, err := store.VerifyEvidence(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"verified": verified})
	}
}

func handleV1GetCustody(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := effectiveTenantID(r, membership, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		chain, err := store.GetCustody(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, chain)
	}
}

func handleV1CreateCase(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r, maxJSONBodyBytes)
		var req struct {
			TenantID string `json:"tenant_id"`
			CaseID   string `json:"case_id"`
			Tick     uint64 `json:"tick"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		tenantID, err := effectiveTenantID(r, membership, req.TenantID)
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		if err := store.CreateCase(tenantID, req.CaseID, req.Tick); err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"case_id": req.CaseID})
	}
}

func handleV1GetCase(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := effectiveTenantID(r, membership, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
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

func handleV1DecideCase(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r, maxJSONBodyBytes)
		var req reqDecideCase
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		tenantID, err := effectiveTenantID(r, membership, req.TenantID)
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		caseID := r.PathValue("id")
		d, err := store.DecideCase(commercialapi.DecideInput{
			TenantID: tenantID, CaseID: caseID,
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

func handleV1ActOnCase(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r, maxJSONBodyBytes)
		var req reqActOnCase
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		tenantID, err := effectiveTenantID(r, membership, req.TenantID)
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		aa, receipt, err := store.ActOnCase(commercialapi.ActionInput{
			TenantID: tenantID, CaseID: r.PathValue("id"), Actor: req.Actor, PolicyRef: req.PolicyRef,
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
func handleV1GetDossier(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := effectiveTenantID(r, membership, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
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

func handleV1Replay(store *commercialapi.Store, membership *tenancy.Membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := effectiveTenantID(r, membership, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeTenantBindingError(w, err)
			return
		}
		result, err := store.Replay(tenantID, r.PathValue("id"))
		if err != nil {
			writeAPIError(w, statusForStoreError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// handleV1Metrics serves this Store's item-20 operational counters
// (pkg/commercial/telemetry.Snapshot). It is deliberately not
// tenant-scoped: these are operator-facing operational metrics, not
// tenant data, the same distinction /healthz already draws.
func handleV1Metrics(store *commercialapi.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.Metrics().Snapshot())
	}
}

// handleV1VerifyPackage is stateless and requires no tenant scoping --
// verifying an independently-held Machine Package is, by design, a
// capability anyone holding the file has, per pkg/commercial/
// packageverify's whole purpose. HONEST SCOPE: this HTTP convenience
// route always verifies with a nil trusted-key registry (every
// signature/key-state check reports SKIP, never a false PASS) --
// accepting a caller-supplied registry over this raw-body-upload route
// needs a request shape (multipart, most likely) this route does not
// have yet, named here rather than silently assumed. The standalone
// cmd/veriqo-commercial-verify CLI -- the actual "independent
// verifier" this whole capability exists for -- already supports
// -trusted-keys fully; this route is a same-process convenience, not
// the verifier of record.
func handleV1VerifyPackage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r, maxPackageUploadBytes)
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

		results, err := packageverify.VerifyZip(&zr.Reader, nil)
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
