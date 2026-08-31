// Package commercialapi answers Commercialization Sprint item 5
// directly: "Commercial API harus menjadi prioritas ... API yang dapat
// dipahami customer, bukan hanya internal package," with the named
// minimal endpoint set (POST /v1/evidence, GET /v1/evidence/{id},
// POST /v1/evidence/{id}/verify, GET /v1/evidence/{id}/custody, POST
// /v1/cases, GET /v1/cases/{id}, POST /v1/cases/{id}/decide, POST
// /v1/cases/{id}/actions, GET /v1/cases/{id}/dossier, GET
// /v1/cases/{id}/replay, POST /v1/packages/verify) and the explicit
// instruction "Jangan expose internal kernel structures."
//
// This package is the business-logic layer HTTP routes call into --
// following this repository's own "gateway carries no business logic,
// Gateway -> Engine Registry -> Kernel" principle (see veriqo/gateway/
// rest's own package doc comment). Store never re-implements any
// FROZEN kernel logic (see docs/VERIQO_CORE_TRUST_KERNEL_FREEZE.md):
// every write orchestrates the exact same cre/decision/action/manifest
// functions the rest of this repository's tests already exercise.
//
// Store also implements Commercialization Sprint item 19's minimal
// multi-tenancy: every record carries a TenantID, and every read/write
// method requires the caller's TenantID to match before returning
// anything -- see TestTenantAIsolationFromTenantB in store_test.go for
// the adversarial proof (cannot read, cannot modify, cannot replay
// another tenant's case).
//
// DURABILITY: NewStore builds an in-memory-only Store (restarting the
// process loses every case and evidence record) -- unchanged from this
// package's original shape, and still what every existing test and the
// zero-value-safe HTTP nil-store path uses. NewDurableStore, added to
// close the reviewer's explicit "PILOT-READY perlu kita pecah" critique
// (Commercial API Store belum durable is not a cosmetic gap: it touches
// data persistence, disaster recovery, evidence preservation, legal
// hold, audit continuity, backup, restore, RPO/RTO, and customer
// trust), makes every mutating call durable via pkg/storage/wal -- a
// real write-ahead log with fsync, CRC, and defect-classified recovery
// this repository already built and proved for the consensus layer.
// See durable.go for the mechanism: every successful CreateCase/
// SubmitEvidence/DecideCase/ActOnCase call appends its own INPUT (not a
// derived diff) to the WAL, and NewDurableStore reconstructs identical
// state on startup by replaying those inputs back through this Store's
// own real methods -- sound because manifest.Registry and
// audit.AuditStore are both already deterministic given the same call
// sequence (no wall-clock or random state feeds either one), so no new
// export/import surface was needed on either FROZEN package.
package commercialapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"time"

	"veriqo/pkg/commercial/dossier"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/commercial/telemetry"
	"veriqo/pkg/commercial/verticalslice"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/governance/data"
	"veriqo/pkg/insurance/action"
	"veriqo/pkg/insurance/causation"
	"veriqo/pkg/insurance/cre"
	"veriqo/pkg/insurance/decision"
	"veriqo/pkg/platform/audit"
	"veriqo/pkg/platform/security/keys"
	"veriqo/pkg/storage/wal"
)

var (
	ErrEmptyTenantID     = errors.New("commercialapi: TenantID must be non-empty")
	ErrEmptyEvidenceID   = errors.New("commercialapi: EvidenceID must be non-empty")
	ErrEmptyCaseID       = errors.New("commercialapi: CaseID must be non-empty")
	ErrCaseAlreadyExists = errors.New("commercialapi: a case with this CaseID already exists for this tenant")
	ErrCaseNotFound      = errors.New("commercialapi: no case found for this TenantID/CaseID")
	ErrEvidenceNotFound  = errors.New("commercialapi: no evidence found for this TenantID/EvidenceID")
	ErrTenantMismatch    = errors.New("commercialapi: the requested resource belongs to a different tenant")
	ErrNotYetDecided     = errors.New("commercialapi: this case has not been decided yet -- call decide before actions/dossier/replay")
	ErrAlreadyDecided    = errors.New("commercialapi: this case has already been decided -- decide is not re-callable")
	ErrNotDurable        = errors.New("commercialapi: this Store is in-memory only (built with NewStore, not NewDurableStore) -- there is nothing on disk to back up")
)

// EvidenceInput is POST /v1/evidence's request shape.
type EvidenceInput struct {
	TenantID   string
	CaseID     string
	EvidenceID string
	SHA256     string
	URI        string
	Filename   string
	MediaType  string
	ByteSize   int64
	Collector  string
	Source     string
	Domain     evidencefabric.DomainMetadata
	Tick       uint64
}

// DecideInput is POST /v1/cases/{id}/decide's request shape.
type DecideInput struct {
	TenantID                 string
	CaseID                   string
	Hypothesis               causation.Hypothesis
	SupportingEvidenceIDs    []string
	ContradictingEvidenceIDs []string
	FindingID                string
	Finding                  cre.FindingInput
	Outcome                  decision.Outcome
	Rationale                string
	LedgerActor              string
	Tick                     uint64
}

// ActionInput is POST /v1/cases/{id}/actions's request shape.
type ActionInput struct {
	TenantID        string
	CaseID          string
	Actor           string
	PolicyRef       string
	Scope           string
	PermittedAction action.Action
	Conditions      []string
	AuthorizedAt    uint64
	ExpiresAt       uint64
	ExecutingActor  string
	ExecutionAt     uint64
	LedgerActor     string
}

// CaseView is GET /v1/cases/{id}'s response shape -- a curated summary,
// never the raw internal cre.AuthorizedFinding/decision.Decision types
// verbatim (see this package's own doc comment: "Jangan expose internal
// kernel structures").
type CaseView struct {
	CaseID      string   `json:"case_id"`
	TenantID    string   `json:"tenant_id"`
	CreatedAt   uint64   `json:"created_at"`
	EvidenceIDs []string `json:"evidence_ids"`
	Decided     bool     `json:"decided"`
	Outcome     string   `json:"outcome,omitempty"`
	ActedOn     bool     `json:"acted_on"`
}

type evidenceEntry struct {
	tenantID string
	in       EvidenceInput
}

type caseEntry struct {
	tenantID    string
	caseID      string
	createdAt   uint64
	evidenceIDs []string

	decideInput *DecideInput
	af          cre.AuthorizedFinding
	d           decision.Decision

	actionInput *ActionInput
	aa          action.ActionAuthorization
	receipt     verticalslice.Receipt
}

// Store is the Commercial API's in-memory reference backing store.
// The zero value is not usable -- construct with NewStore.
type Store struct {
	mu        sync.Mutex
	manifests *manifest.Registry
	ledger    *audit.AuditStore
	cases     map[string]*caseEntry
	evidence  map[string]*evidenceEntry
	metrics   *telemetry.Metrics

	// wal is nil for an in-memory-only Store (NewStore). NewDurableStore
	// sets it; every mutating public method appends its own input to it
	// on success -- see durable.go. walDir is tracked separately (wal.Log
	// does not expose its own configured directory) so Backup can locate
	// the segment files to copy.
	wal    *wal.Log
	walDir string
	// closed is set once Close has been called on a durable Store -- see
	// durable.go's Close and Healthy. Always false for an in-memory-only
	// Store, which has nothing to close.
	closed bool

	// preservation is the real, already-built pkg/governance/data
	// retention/legal-hold engine (see preservation.go) -- reused, not
	// reinvented, for Commercialization Sprint P0-C.
	preservation          *data.Engine
	preservationPolicySet map[string]bool

	// keyManager is nil until EnableSigning is called (see crypto.go) --
	// this reference build's honest default is unsigned evidence and
	// dossiers, never silently claimed otherwise.
	keyManager *keys.Manager
	// signatures holds each evidence item's real EvidenceSignature (by
	// evidenceKey), so every projection of that evidence -- GetEvidence,
	// GenerateDossier's own inventory rebuild, not just SubmitEvidence's
	// immediate return value -- shows the same real signature.
	signatures map[string]evidencefabric.EvidenceSignature
}

// NewStore constructs an empty, in-memory-only Store with a fresh
// manifest registry and a fresh audit ledger. Nothing written to it
// survives a process restart -- see NewDurableStore (durable.go) for
// the durable alternative.
func NewStore() *Store {
	return &Store{
		manifests:             manifest.NewRegistry(),
		ledger:                audit.NewAuditStore(),
		cases:                 make(map[string]*caseEntry),
		evidence:              make(map[string]*evidenceEntry),
		metrics:               telemetry.New(),
		preservation:          data.New(),
		preservationPolicySet: make(map[string]bool),
		signatures:            make(map[string]evidencefabric.EvidenceSignature),
	}
}

// Ledger exposes the Store's underlying audit ledger, read-only, for
// callers (e.g. the HTTP layer's own audit/observability wiring) that
// need the raw hash-chained record set -- never for callers to append
// to directly; every write still goes through Store's own methods.
func (s *Store) Ledger() *audit.AuditStore { return s.ledger }

// Metrics exposes the Store's item-20 operational counters, read-only --
// see pkg/commercial/telemetry's own doc comment for exactly which
// method increments which counter.
func (s *Store) Metrics() *telemetry.Metrics { return s.metrics }

func caseKey(tenantID, caseID string) string         { return tenantID + "/" + caseID }
func evidenceKey(tenantID, evidenceID string) string { return tenantID + "/" + evidenceID }

// SubmitEvidence drives one evidence item through SOURCE -> ACQUIRE ->
// PRESERVE -> HASH -> PROVENANCE -> MANIFEST -> CUSTODY (the same real
// manifest.Registry state machine every other real evidence pipeline
// in this repository uses) and returns its canonical projection.
func (s *Store) SubmitEvidence(in EvidenceInput) (evidencefabric.EvidenceRecord, error) {
	rec, err := s.submitEvidenceCore(in)
	if err != nil {
		return rec, err
	}
	if err := s.appendWAL(in.Tick, walCommand{Kind: walCmdSubmitEvidence, Evidence: &in}); err != nil {
		return evidencefabric.EvidenceRecord{}, err
	}
	return rec, nil
}

func (s *Store) submitEvidenceCore(in EvidenceInput) (evidencefabric.EvidenceRecord, error) {
	if in.TenantID == "" {
		return evidencefabric.EvidenceRecord{}, ErrEmptyTenantID
	}
	if in.EvidenceID == "" {
		return evidencefabric.EvidenceRecord{}, ErrEmptyEvidenceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.manifests.RegisterDraft(manifest.Manifest{
		TenantID: in.TenantID, CaseID: in.CaseID, EvidenceID: in.EvidenceID, Version: 1,
		URI: in.URI, Filename: in.Filename, MediaType: in.MediaType, ByteSize: in.ByteSize,
		SHA256: in.SHA256, Method: "API_SUBMISSION", Collector: in.Collector, Source: in.Source,
		AcquiredAt: in.Tick, ReceivedAt: in.Tick, HashStatus: "COMPUTED", Classification: "INTERNAL",
		AcquisitionRecord: fmt.Sprintf("submitted via Commercial API v1 by tenant %s", in.TenantID),
	}); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.RecordCustodyEvent(in.EvidenceID, in.EvidenceID+"-received", "commercial-api", manifest.CustodyReceived, in.Tick, "received via Commercial API", ""); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.Advance(in.EvidenceID, manifest.StateIngested, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.RecordCustodyEvent(in.EvidenceID, in.EvidenceID+"-hashed", "commercial-api", manifest.CustodyHashed, in.Tick, "hash computed", in.SHA256); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.Advance(in.EvidenceID, manifest.StateIntegrityAssessed, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.Advance(in.EvidenceID, manifest.StateProvenanceComplete, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.RecordCustodyEvent(in.EvidenceID, in.EvidenceID+"-reviewed", "commercial-api", manifest.CustodyReviewed, in.Tick, "automated review complete", in.SHA256); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.Advance(in.EvidenceID, manifest.StateReadyForFinalization, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}
	if _, err := s.manifests.Advance(in.EvidenceID, manifest.StateFinalized, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: %w", err)
	}

	s.evidence[evidenceKey(in.TenantID, in.EvidenceID)] = &evidenceEntry{tenantID: in.TenantID, in: in}
	if ce, ok := s.cases[caseKey(in.TenantID, in.CaseID)]; ok {
		ce.evidenceIDs = append(ce.evidenceIDs, in.EvidenceID)
	}
	s.metrics.IncEvidenceIngestion()

	// Commercialization Sprint P0-C: every submitted evidence item is
	// placed under governance the moment it is finalized -- see
	// preservation.go. This is required, not best-effort: a failure
	// here means the evidence is finalized in the manifest but has no
	// retention/legal-hold coverage, which this Store refuses to leave
	// silently unnoticed.
	if err := s.ensurePreservationPolicyLocked(in.TenantID); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: preservation policy: %w", err)
	}
	if _, err := s.preservation.Ingest(in.EvidenceID, in.TenantID, commercialEvidenceClass, nil, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: preservation: %w", err)
	}

	rec, err := evidencefabric.FromRegistry(s.manifests, in.EvidenceID, in.Domain)
	if err != nil {
		return evidencefabric.EvidenceRecord{}, err
	}

	// Commercialization Sprint P0-D: sign the real, independently
	// re-derived manifest hash when signing is enabled -- see crypto.go.
	// Unsigned (nil Signature) is this reference build's honest default
	// absent EnableSigning, never hidden as if it were signed.
	if sig, err := s.signEvidenceIfEnabledLocked(rec.Integrity.ManifestHash, in.Tick); err != nil {
		return evidencefabric.EvidenceRecord{}, fmt.Errorf("commercialapi: SubmitEvidence: signing: %w", err)
	} else if sig != nil {
		rec.Signature = sig
		s.signatures[evidenceKey(in.TenantID, in.EvidenceID)] = *sig
	}
	return rec, nil
}

// GetEvidence returns the canonical projection for a previously
// submitted evidence item, scoped to tenantID.
func (s *Store) GetEvidence(tenantID, evidenceID string) (evidencefabric.EvidenceRecord, error) {
	s.mu.Lock()
	entry, ok := s.evidence[evidenceKey(tenantID, evidenceID)]
	sig, signed := s.signatures[evidenceKey(tenantID, evidenceID)]
	s.mu.Unlock()
	if !ok {
		return evidencefabric.EvidenceRecord{}, ErrEvidenceNotFound
	}
	if entry.tenantID != tenantID {
		return evidencefabric.EvidenceRecord{}, ErrTenantMismatch
	}
	rec, err := evidencefabric.FromRegistry(s.manifests, evidenceID, entry.in.Domain)
	if err != nil {
		return evidencefabric.EvidenceRecord{}, err
	}
	if signed {
		rec.Signature = &sig
	}
	return rec, nil
}

// VerifyEvidence independently re-verifies a previously submitted
// evidence item's manifest hash and custody chain, live, right now --
// not merely returning whatever Integrity.Verified was computed to at
// submission time.
func (s *Store) VerifyEvidence(tenantID, evidenceID string) (bool, error) {
	s.mu.Lock()
	entry, ok := s.evidence[evidenceKey(tenantID, evidenceID)]
	s.mu.Unlock()
	if !ok {
		return false, ErrEvidenceNotFound
	}
	if entry.tenantID != tenantID {
		return false, ErrTenantMismatch
	}
	m, err := s.manifests.Latest(evidenceID)
	if err != nil {
		return false, err
	}
	if err := manifest.VerifyManifestHash(m); err != nil {
		s.metrics.IncEvidenceVerificationFail()
		return false, nil
	}
	if err := s.manifests.VerifyCustodyChain(evidenceID); err != nil {
		s.metrics.IncCustodyChainFailure()
		return false, nil
	}
	return true, nil
}

// GetCustody returns evidenceID's full, real custody chain.
func (s *Store) GetCustody(tenantID, evidenceID string) ([]manifest.CustodyEvent, error) {
	s.mu.Lock()
	entry, ok := s.evidence[evidenceKey(tenantID, evidenceID)]
	s.mu.Unlock()
	if !ok {
		return nil, ErrEvidenceNotFound
	}
	if entry.tenantID != tenantID {
		return nil, ErrTenantMismatch
	}
	return s.manifests.CustodyChain(evidenceID), nil
}

// CreateCase registers a new, empty case for tenantID. When this Store
// is durable (NewDurableStore), the call is appended to the WAL on
// success before returning -- a caller who sees a nil error has a
// durably logged case, not just an in-memory one.
func (s *Store) CreateCase(tenantID, caseID string, tick uint64) error {
	if err := s.createCaseCore(tenantID, caseID, tick); err != nil {
		return err
	}
	return s.appendWAL(tick, walCommand{Kind: walCmdCreateCase, CreateCase: &createCaseArgs{TenantID: tenantID, CaseID: caseID, Tick: tick}})
}

func (s *Store) createCaseCore(tenantID, caseID string, tick uint64) error {
	if tenantID == "" {
		return ErrEmptyTenantID
	}
	if caseID == "" {
		return ErrEmptyCaseID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := caseKey(tenantID, caseID)
	if _, exists := s.cases[key]; exists {
		return ErrCaseAlreadyExists
	}
	s.cases[key] = &caseEntry{tenantID: tenantID, caseID: caseID, createdAt: tick}
	return nil
}

// GetCase returns a curated, non-internal summary of a case.
func (s *Store) GetCase(tenantID, caseID string) (CaseView, error) {
	s.mu.Lock()
	ce, ok := s.cases[caseKey(tenantID, caseID)]
	s.mu.Unlock()
	if !ok {
		return CaseView{}, ErrCaseNotFound
	}
	if ce.tenantID != tenantID {
		return CaseView{}, ErrTenantMismatch
	}
	view := CaseView{
		CaseID: ce.caseID, TenantID: ce.tenantID, CreatedAt: ce.createdAt,
		EvidenceIDs: append([]string(nil), ce.evidenceIDs...),
		Decided:     !ce.d.IsZero(), ActedOn: !ce.aa.IsZero(),
	}
	if !ce.d.IsZero() {
		view.Outcome = string(ce.d.Outcome())
	}
	return view, nil
}

// DecideCase drives the case's DECISION stage: cre.BuildFinding ->
// cre.AuthorizeGrounded -> decision.MakeDecision -> decision.AppendToLedger,
// against the Store's own already-finalized evidence manifests. Every
// evidence ID cited must have already been submitted via SubmitEvidence
// -- there is no way to reach a Decision citing evidence this Store
// never received.
func (s *Store) DecideCase(in DecideInput) (decision.Decision, error) {
	d, err := s.decideCaseCore(in)
	if err != nil {
		return d, err
	}
	if err := s.appendWAL(in.Tick, walCommand{Kind: walCmdDecideCase, Decide: &in}); err != nil {
		return decision.Decision{}, err
	}
	return d, nil
}

func (s *Store) decideCaseCore(in DecideInput) (decision.Decision, error) {
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ce, ok := s.cases[caseKey(in.TenantID, in.CaseID)]
	if !ok {
		return decision.Decision{}, ErrCaseNotFound
	}
	if ce.tenantID != in.TenantID {
		return decision.Decision{}, ErrTenantMismatch
	}
	if !ce.d.IsZero() {
		return decision.Decision{}, ErrAlreadyDecided
	}

	hs, err := causation.NewHypothesisSet(in.CaseID, in.FindingID, "Commercial API case decision")
	if err != nil {
		return decision.Decision{}, err
	}
	h := in.Hypothesis
	h.Status = ""
	if err := hs.Add(h); err != nil {
		return decision.Decision{}, err
	}
	for _, ev := range in.SupportingEvidenceIDs {
		if err := hs.AddSupportingEvidence(h.ID, ev); err != nil {
			return decision.Decision{}, err
		}
	}
	for _, ev := range in.ContradictingEvidenceIDs {
		if err := hs.AddContradictingEvidence(h.ID, ev); err != nil {
			return decision.Decision{}, err
		}
	}
	resolved, ok := hs.Get(h.ID)
	if !ok {
		return decision.Decision{}, fmt.Errorf("commercialapi: DecideCase: hypothesis %s not found after Add", h.ID)
	}

	f, err := cre.BuildFinding(hs, resolved, nil, in.Finding, in.FindingID, in.Tick)
	if err != nil {
		return decision.Decision{}, fmt.Errorf("commercialapi: DecideCase: %w", err)
	}
	af, err := cre.AuthorizeGrounded(f, hs, h.ID, nil, s.manifests, in.Tick)
	if err != nil {
		return decision.Decision{}, fmt.Errorf("commercialapi: DecideCase: %w", err)
	}
	d, err := decision.MakeDecision(af, in.Outcome, in.Rationale, in.Tick)
	if err != nil {
		return decision.Decision{}, fmt.Errorf("commercialapi: DecideCase: %w", err)
	}
	if _, err := decision.AppendToLedger(s.ledger, in.LedgerActor, d); err != nil {
		s.metrics.IncLedgerCommitFailure()
		return decision.Decision{}, fmt.Errorf("commercialapi: DecideCase: %w", err)
	}

	inCopy := in
	ce.decideInput = &inCopy
	ce.af = af
	ce.d = d
	s.metrics.RecordDecisionLatency(time.Since(start))
	return d, nil
}

// ActOnCase drives the case's AUTHORIZATION + ACTION + RECEIPT stages:
// action.AuthorizeAction -> action.AuthorizeExecution -> the real
// ledger appends -- requires the case to already be decided.
func (s *Store) ActOnCase(in ActionInput) (action.ActionAuthorization, verticalslice.Receipt, error) {
	aa, receipt, err := s.actOnCaseCore(in)
	if err != nil {
		return aa, receipt, err
	}
	if err := s.appendWAL(in.AuthorizedAt, walCommand{Kind: walCmdActOnCase, Action: &in}); err != nil {
		return action.ActionAuthorization{}, verticalslice.Receipt{}, err
	}
	return aa, receipt, nil
}

func (s *Store) actOnCaseCore(in ActionInput) (action.ActionAuthorization, verticalslice.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ce, ok := s.cases[caseKey(in.TenantID, in.CaseID)]
	if !ok {
		return action.ActionAuthorization{}, verticalslice.Receipt{}, ErrCaseNotFound
	}
	if ce.tenantID != in.TenantID {
		return action.ActionAuthorization{}, verticalslice.Receipt{}, ErrTenantMismatch
	}
	if ce.d.IsZero() {
		return action.ActionAuthorization{}, verticalslice.Receipt{}, ErrNotYetDecided
	}

	aa, err := action.AuthorizeAction(ce.d, in.Actor, in.PolicyRef, in.Scope, in.PermittedAction, in.Conditions, in.AuthorizedAt, in.ExpiresAt)
	if err != nil {
		s.metrics.IncAuthorizationDenial()
		return action.ActionAuthorization{}, verticalslice.Receipt{}, fmt.Errorf("commercialapi: ActOnCase: %w", err)
	}
	if _, err := action.AppendToLedger(s.ledger, in.LedgerActor, aa); err != nil {
		s.metrics.IncLedgerCommitFailure()
		return action.ActionAuthorization{}, verticalslice.Receipt{}, fmt.Errorf("commercialapi: ActOnCase: %w", err)
	}
	if err := action.AuthorizeExecution(aa, ce.d, in.ExecutingActor, in.PermittedAction, in.Scope, in.ExecutionAt); err != nil {
		s.metrics.IncActionFailure()
		return action.ActionAuthorization{}, verticalslice.Receipt{}, fmt.Errorf("commercialapi: ActOnCase: %w", err)
	}
	execRec, err := action.AppendExecutionToLedger(s.ledger, in.LedgerActor, aa, in.ExecutionAt)
	if err != nil {
		s.metrics.IncLedgerCommitFailure()
		return action.ActionAuthorization{}, verticalslice.Receipt{}, fmt.Errorf("commercialapi: ActOnCase: %w", err)
	}
	receipt := verticalslice.BuildReceipt(aa, ce.d, execRec, in.ExecutingActor, in.ExecutionAt)

	inCopy := in
	ce.actionInput = &inCopy
	ce.aa = aa
	ce.receipt = receipt
	return aa, receipt, nil
}

// GenerateDossier assembles the case's Evidence Dossier v1 from every
// real artifact this Store has recorded so far.
func (s *Store) GenerateDossier(tenantID, caseID string) (dossier.Dossier, error) {
	s.mu.Lock()
	ce, ok := s.cases[caseKey(tenantID, caseID)]
	s.mu.Unlock()
	if !ok {
		return dossier.Dossier{}, ErrCaseNotFound
	}
	if ce.tenantID != tenantID {
		return dossier.Dossier{}, ErrTenantMismatch
	}
	if ce.d.IsZero() {
		return dossier.Dossier{}, ErrNotYetDecided
	}

	var evidenceRecords []evidencefabric.EvidenceRecord
	for _, evID := range ce.evidenceIDs {
		s.mu.Lock()
		entry := s.evidence[evidenceKey(tenantID, evID)]
		sig, signed := s.signatures[evidenceKey(tenantID, evID)]
		s.mu.Unlock()
		var domain evidencefabric.DomainMetadata
		if entry != nil {
			domain = entry.in.Domain
		}
		rec, err := evidencefabric.FromRegistry(s.manifests, evID, domain)
		if err != nil {
			return dossier.Dossier{}, fmt.Errorf("commercialapi: GenerateDossier: %w", err)
		}
		if signed {
			rec.Signature = &sig
		}
		evidenceRecords = append(evidenceRecords, rec)
	}

	result := verticalslice.Result{
		Manifests: s.manifests, AuthorizedFinding: ce.af, Decision: ce.d,
		ActionAuthorization: ce.aa, Receipt: ce.receipt,
	}

	// dossier.New deliberately never invents Corroboration/Contradictions
	// on its own (see that package's own doc comment) -- it is this
	// Store's job, as the caller holding the real AuthorizedFinding, to
	// translate the Finding's own SupportedBy/ContradictedBy evidence
	// citations (already independently grounded by cre.AuthorizeGrounded
	// against FINALIZED, hash-verified manifests) into the dossier's
	// human-readable Corroboration/Contradictions rows.
	var hypothesisID string
	if ce.decideInput != nil {
		hypothesisID = string(ce.decideInput.Hypothesis.ID)
	}
	f := ce.af.Finding()
	var corroboration, contradictions []string
	for _, evID := range f.SupportedBy {
		corroboration = append(corroboration, fmt.Sprintf("%s supports hypothesis %s", evID, hypothesisID))
	}
	for _, evID := range f.ContradictedBy {
		contradictions = append(contradictions, fmt.Sprintf("%s contradicts hypothesis %s", evID, hypothesisID))
	}

	d, err := dossier.New(dossier.Input{
		Scope: caseID, Result: result, Evidence: evidenceRecords,
		Corroboration: corroboration, Contradictions: contradictions,
	})
	if err != nil {
		return dossier.Dossier{}, err
	}

	// Commercialization Sprint P0-D: sign the package hash, never the
	// other way around -- PackageHash is already computed by New over
	// every field except itself (and, per dossier's own VerifyPackageHash,
	// except PackageSignature too, since a signature over content can
	// never itself be part of what got hashed).
	s.mu.Lock()
	sigBytes, keyID, keyVersion, err := s.signDigestLocked(context.Background(), []byte(d.PackageHash), ce.d.DecidedAt())
	s.mu.Unlock()
	switch {
	case errors.Is(err, ErrSigningNotEnabled):
		// Unsigned is this reference build's honest default.
	case err != nil:
		return dossier.Dossier{}, fmt.Errorf("commercialapi: GenerateDossier: signing: %w", err)
	default:
		d.PackageSignature = &dossier.PackageSignature{
			Algorithm: "Ed25519", KeyID: keyID, KeyVersion: keyVersion,
			Signature: hex.EncodeToString(sigBytes), SignedPackageHash: d.PackageHash, SignedAtTick: ce.d.DecidedAt(),
		}
	}
	return d, nil
}

// WriteDossierPackage generates the case's dossier and writes its
// Machine Package (.zip) form to outPath.
func (s *Store) WriteDossierPackage(tenantID, caseID, outPath string) (dossier.Dossier, error) {
	d, err := s.GenerateDossier(tenantID, caseID)
	if err != nil {
		return dossier.Dossier{}, err
	}
	if err := dossier.WriteMachinePackage(d, s.manifests, s.ledger, outPath); err != nil {
		return dossier.Dossier{}, err
	}
	return d, nil
}

// ReplayResult is GET /v1/cases/{id}/replay's response shape.
type ReplayResult struct {
	OriginalDecisionHash string `json:"original_decision_hash"`
	ReplayedDecisionHash string `json:"replayed_decision_hash"`
	OriginalActionHash   string `json:"original_action_authorization_hash,omitempty"`
	ReplayedActionHash   string `json:"replayed_action_authorization_hash,omitempty"`
	Converged            bool   `json:"converged"`
}

// Replay independently re-runs the case's own recorded DECISION (and,
// if present, AUTHORIZATION+ACTION) inputs against the Store's already-
// finalized evidence -- the same evidence, re-processed -- and confirms
// the resulting hashes converge byte-identically with what was
// originally recorded. This is a genuine determinism proof (same
// inputs -> same outputs), not a re-run against a synthetically fresh
// evidence set (the manifests themselves are durable, already-finalized
// infrastructure in this Store's design, not something REPLAY re-derives
// from scratch).
func (s *Store) Replay(tenantID, caseID string) (ReplayResult, error) {
	s.mu.Lock()
	ce, ok := s.cases[caseKey(tenantID, caseID)]
	s.mu.Unlock()
	if !ok {
		return ReplayResult{}, ErrCaseNotFound
	}
	if ce.tenantID != tenantID {
		return ReplayResult{}, ErrTenantMismatch
	}
	if ce.decideInput == nil || ce.d.IsZero() {
		return ReplayResult{}, ErrNotYetDecided
	}

	hs, err := causation.NewHypothesisSet(ce.decideInput.CaseID, ce.decideInput.FindingID, "Commercial API replay")
	if err != nil {
		return ReplayResult{}, err
	}
	h := ce.decideInput.Hypothesis
	h.Status = ""
	if err := hs.Add(h); err != nil {
		return ReplayResult{}, err
	}
	for _, ev := range ce.decideInput.SupportingEvidenceIDs {
		if err := hs.AddSupportingEvidence(h.ID, ev); err != nil {
			return ReplayResult{}, err
		}
	}
	for _, ev := range ce.decideInput.ContradictingEvidenceIDs {
		if err := hs.AddContradictingEvidence(h.ID, ev); err != nil {
			return ReplayResult{}, err
		}
	}
	resolved, _ := hs.Get(h.ID)
	f, err := cre.BuildFinding(hs, resolved, nil, ce.decideInput.Finding, ce.decideInput.FindingID, ce.decideInput.Tick)
	if err != nil {
		return ReplayResult{}, err
	}
	af, err := cre.AuthorizeGrounded(f, hs, h.ID, nil, s.manifests, ce.decideInput.Tick)
	if err != nil {
		return ReplayResult{}, err
	}
	replayedDecision, err := decision.MakeDecision(af, ce.decideInput.Outcome, ce.decideInput.Rationale, ce.decideInput.Tick)
	if err != nil {
		return ReplayResult{}, err
	}

	result := ReplayResult{
		OriginalDecisionHash: ce.d.Hash(), ReplayedDecisionHash: replayedDecision.Hash(),
		Converged: ce.d.Hash() == replayedDecision.Hash(),
	}

	if ce.actionInput != nil && !ce.aa.IsZero() {
		replayedAA, err := action.AuthorizeAction(replayedDecision, ce.actionInput.Actor, ce.actionInput.PolicyRef,
			ce.actionInput.Scope, ce.actionInput.PermittedAction, ce.actionInput.Conditions,
			ce.actionInput.AuthorizedAt, ce.actionInput.ExpiresAt)
		if err != nil {
			return ReplayResult{}, err
		}
		result.OriginalActionHash = ce.aa.Hash()
		result.ReplayedActionHash = replayedAA.Hash()
		result.Converged = result.Converged && (ce.aa.Hash() == replayedAA.Hash())
	}

	if !result.Converged {
		s.metrics.IncReplayFailure()
	}
	return result, nil
}
