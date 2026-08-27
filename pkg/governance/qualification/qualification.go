// Package qualification closes P0-03 of the V7.12.0 integrated audit
// (an External Qualification Evidence Framework) and, in this
// revision, the P0 "fix the external qualification system before
// closing the eight blockers" layer the V7.12.1 Palantir-style audit
// demanded: the original version treated Reviewer != "" and
// Signature != "" as sufficient evidence that a reviewer and a
// signature existed. That is not evidence, per that audit's Rule 3:
//
//	"A string called signature is NOT evidence of a signature. A
//	reviewer name is NOT proof of reviewer identity. An artifact hash
//	is NOT proof that the artifact was produced by the claimed
//	provider."
//
// This revision adds a real trust model:
//
//   - A Provider and a Reviewer are registered identities with an
//     Ed25519 public key, a validity window, and (for providers) the
//     specific gates they are authorized to attest to — not free-text
//     names.
//   - Every submission must carry a ProviderSignature and a
//     ReviewerSignature, each independently verified against the
//     corresponding registered public key over the evidence's content
//     hash. An unregistered, expired or revoked provider/reviewer
//     cannot produce evidence that validates, no matter what string is
//     in the submission.
//   - Every submission is release-bound: its Commit and SourceHash
//     must match the exact release being assessed. Evidence produced
//     against one commit cannot qualify a different one.
//
// None of this closes any of the eight blockers itself — no code run
// inside this repository can conjure a real penetration-test vendor
// or a physical 100-node cluster, and TrustRegistry ships with zero
// registered providers or reviewers by default, so nothing can be
// validated until a human operator registers a real one. What this
// package now guarantees is that when real external evidence does
// arrive, it cannot be validated by anything less than a genuine,
// verifiable, release-bound cryptographic signature from a registered
// identity.
package qualification

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"veriqo/pkg/platform/telemetry"
)

// Status is the lifecycle state of one gate's external qualification.
type Status string

const (
	StatusBlockedExternal   Status = "BLOCKED_EXTERNAL"
	StatusEvidenceSubmitted Status = "EVIDENCE_SUBMITTED"
	StatusEvidenceValidated Status = "EVIDENCE_VALIDATED"
	StatusQualified         Status = "QUALIFIED"
	StatusVerified          Status = "VERIFIED"
	StatusEvidenceRejected  Status = "EVIDENCE_REJECTED"
	StatusExpired           Status = "EXPIRED"
)

var (
	ErrUnknownGate         = errors.New("qualification: unknown gate")
	ErrDuplicateGate       = errors.New("qualification: gate already registered")
	ErrIllegalTransition   = errors.New("qualification: illegal lifecycle transition")
	ErrEvidenceIncomplete  = errors.New("qualification: evidence submission is missing a mandatory field")
	ErrArtifactHashInvalid = errors.New("qualification: declared artifact hash does not match the submitted content")
	ErrAlreadyExpired      = errors.New("qualification: evidence expiry tick is not in the future")

	// Trust-model errors (V7.12.1 Layer A hardening).
	ErrUnknownProvider       = errors.New("qualification: provider is not a registered trust anchor")
	ErrProviderRevoked       = errors.New("qualification: provider credential is revoked")
	ErrProviderInactive      = errors.New("qualification: provider credential is outside its validity window")
	ErrProviderNotAuthorized = errors.New("qualification: provider is not authorized for this gate")
	ErrUnknownReviewer       = errors.New("qualification: reviewer is not a registered trust anchor")
	ErrReviewerRevoked       = errors.New("qualification: reviewer credential is revoked")
	ErrReviewerInactive      = errors.New("qualification: reviewer credential is outside its validity window")
	ErrProviderSignature     = errors.New("qualification: provider signature does not verify against the registered public key")
	ErrReviewerSignature     = errors.New("qualification: reviewer signature does not verify against the registered public key")
	ErrReleaseMismatch       = errors.New("qualification: evidence is bound to a different release (commit/source_hash mismatch)")
)

// Provider is a registered, trusted source of external evidence — a
// penetration-testing vendor, a cloud KMS tenancy, a benchmark lab.
// Free-text provider names are never trusted; only a ProviderID that
// resolves to one of these, with a live signature check, is.
type Provider struct {
	ProviderID          string   `json:"provider_id"`
	LegalName           string   `json:"legal_name"`
	ProviderType        string   `json:"provider_type"` // e.g. penetration_testing_vendor, cloud_provider, benchmark_lab
	PublicKeyHex        string   `json:"public_key"`    // hex-encoded Ed25519 public key
	ValidFromTick       uint64   `json:"valid_from_tick"`
	ValidUntilTick      uint64   `json:"valid_until_tick"`
	Revoked             bool     `json:"revoked"`
	AuthorizedGateTypes []string `json:"authorized_gate_types"`
}

func (p Provider) activeAt(tick uint64) bool {
	return !p.Revoked && tick >= p.ValidFromTick && tick <= p.ValidUntilTick
}

func (p Provider) authorizedFor(gateID string) bool {
	for _, g := range p.AuthorizedGateTypes {
		if g == gateID {
			return true
		}
	}
	return false
}

func (p Provider) publicKey() (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(p.PublicKeyHex)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("qualification: provider %s has a malformed public key", p.ProviderID)
	}
	return ed25519.PublicKey(raw), nil
}

// Reviewer is a registered, trusted internal identity permitted to
// accept external evidence on VERIQO's behalf. A reviewer email
// string is never, by itself, proof of identity; only a ReviewerID
// that resolves to one of these, with a live signature check, is.
type Reviewer struct {
	ReviewerID     string `json:"reviewer_id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	PublicKeyHex   string `json:"public_key"`
	Authority      string `json:"authority"`
	ValidFromTick  uint64 `json:"valid_from_tick"`
	ValidUntilTick uint64 `json:"valid_until_tick"`
	Revoked        bool   `json:"revoked"`
}

func (r Reviewer) activeAt(tick uint64) bool {
	return !r.Revoked && tick >= r.ValidFromTick && tick <= r.ValidUntilTick
}

func (r Reviewer) publicKey() (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(r.PublicKeyHex)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("qualification: reviewer %s has a malformed public key", r.ReviewerID)
	}
	return ed25519.PublicKey(raw), nil
}

// TrustRegistry holds every registered provider and reviewer.
// A freshly constructed registry is empty: no evidence can be
// validated against it until an operator explicitly registers a real
// provider or reviewer's real public key, which is precisely the
// point — nothing here can be true by default.
type TrustRegistry struct {
	mu        sync.Mutex
	providers map[string]Provider
	reviewers map[string]Reviewer
}

// NewTrustRegistry constructs an empty trust registry.
func NewTrustRegistry() *TrustRegistry {
	return &TrustRegistry{providers: map[string]Provider{}, reviewers: map[string]Reviewer{}}
}

func (t *TrustRegistry) RegisterProvider(p Provider) error {
	if _, err := p.publicKey(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.providers[p.ProviderID] = p
	return nil
}

func (t *TrustRegistry) RegisterReviewer(r Reviewer) error {
	if _, err := r.publicKey(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reviewers[r.ReviewerID] = r
	return nil
}

func (t *TrustRegistry) RevokeProvider(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.providers[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, id)
	}
	p.Revoked = true
	t.providers[id] = p
	return nil
}

func (t *TrustRegistry) RevokeReviewer(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.reviewers[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownReviewer, id)
	}
	r.Revoked = true
	t.reviewers[id] = r
	return nil
}

func (t *TrustRegistry) Provider(id string) (Provider, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.providers[id]
	return p, ok
}

func (t *TrustRegistry) Reviewer(id string) (Reviewer, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.reviewers[id]
	return r, ok
}

// ExternalEvidence is one submission of real-world evidence against a
// blocked gate. Every field the audit's release-binding rule (Rule 4)
// requires is present.
type ExternalEvidence struct {
	GateID     string `json:"gate_id"`
	ProviderID string `json:"provider_id"` // must resolve in the TrustRegistry
	ReviewerID string `json:"reviewer_id"` // must resolve in the TrustRegistry

	ReportID    string            `json:"report_id"`
	Scope       string            `json:"scope"`
	Environment string            `json:"environment"` // where it ran: not this sandbox
	Measurement map[string]string `json:"measurement"` // the actual numbers/results, not prose
	StartTick   uint64            `json:"start_tick"`
	EndTick     uint64            `json:"end_tick"`

	// Release binding (Rule 4): evidence from one release cannot
	// qualify a different one.
	Commit     string `json:"commit"`
	SourceHash string `json:"source_hash"`
	BuildHash  string `json:"build_hash,omitempty"`

	ExpiresAtTick   uint64 `json:"expires_at_tick"`
	SubmittedAtTick uint64 `json:"submitted_at_tick"`

	// ArtifactHash is declared by the submitter but never trusted as
	// declared: Validate recomputes it from every other field above
	// and requires an exact match, so nothing here can be edited after
	// the signatures below were computed without detection.
	ArtifactHash string `json:"artifact_hash"`

	// ProviderSignature and ReviewerSignature are each verified
	// independently against the registered Ed25519 public key for
	// ProviderID / ReviewerID, over ArtifactHash — never accepted as a
	// non-empty string.
	ProviderSignature string `json:"provider_signature"`
	ReviewerSignature string `json:"reviewer_signature"`
}

// computeArtifactHash is the independent recomputation Validate checks
// a submission's declared ArtifactHash against, and the exact bytes
// both signatures below must cover. It includes the release-binding
// fields, so re-pointing a signed submission at a different commit
// after the fact invalidates the hash the signatures were made over.
func computeArtifactHash(e ExternalEvidence) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.qualification.evidence/v2|gate=%s|provider=%s|reviewer=%s|report=%s|scope=%s|env=%s|"+
		"commit=%s|source_hash=%s|build_hash=%s|start=%d|end=%d|expires=%d",
		e.GateID, e.ProviderID, e.ReviewerID, e.ReportID, e.Scope, e.Environment,
		e.Commit, e.SourceHash, e.BuildHash, e.StartTick, e.EndTick, e.ExpiresAtTick)
	keys := make([]string, 0, len(e.Measurement))
	for k := range e.Measurement {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "|m:%s=%s", k, e.Measurement[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (e ExternalEvidence) validateComplete() error {
	var missing []string
	if e.ProviderID == "" {
		missing = append(missing, "provider_id")
	}
	if e.ReviewerID == "" {
		missing = append(missing, "reviewer_id")
	}
	if e.ReportID == "" {
		missing = append(missing, "report_id")
	}
	if e.Scope == "" {
		missing = append(missing, "scope")
	}
	if e.Environment == "" {
		missing = append(missing, "environment")
	}
	if e.Commit == "" {
		missing = append(missing, "commit")
	}
	if e.SourceHash == "" {
		missing = append(missing, "source_hash")
	}
	if len(e.Measurement) == 0 {
		missing = append(missing, "measurement")
	}
	if e.ProviderSignature == "" {
		missing = append(missing, "provider_signature")
	}
	if e.ReviewerSignature == "" {
		missing = append(missing, "reviewer_signature")
	}
	if e.EndTick < e.StartTick {
		missing = append(missing, "end_tick before start_tick")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrEvidenceIncomplete, strings.Join(missing, ", "))
	}
	if computeArtifactHash(e) != e.ArtifactHash {
		return ErrArtifactHashInvalid
	}
	return nil
}

// verifySignature checks a hex-encoded Ed25519 signature over the
// evidence's ArtifactHash against a specific public key.
func verifySignature(pub ed25519.PublicKey, artifactHash, sigHex string) bool {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, []byte(artifactHash), sig)
}

// Transition is one recorded lifecycle step, kept for audit history —
// a qualification record's status is never edited in place, only
// advanced with the reason recorded.
type Transition struct {
	From   Status `json:"from"`
	To     Status `json:"to"`
	Reason string `json:"reason"`
	Tick   uint64 `json:"tick"`
}

// Record is the full qualification history of one blocked gate.
type Record struct {
	GateID      string             `json:"gate_id"`
	Description string             `json:"description"`
	Blocker     string             `json:"blocker"`
	Status      Status             `json:"status"`
	Evidence    []ExternalEvidence `json:"evidence,omitempty"`
	History     []Transition       `json:"history"`
}

// Satisfied reports whether the gate's qualification currently
// constitutes real, unexpired, cryptographically verified evidence —
// the only condition under which a caller (e.g. cmd/veriqo-readiness)
// may treat a previously blocked gate as no longer blocking the
// release verdict.
func (r Record) Satisfied(nowTick uint64) bool {
	if r.Status != StatusQualified && r.Status != StatusVerified {
		return false
	}
	for _, e := range r.Evidence {
		if e.ExpiresAtTick > nowTick {
			return true
		}
	}
	return false
}

// Registry holds the qualification record for every blocked gate. It
// consults a TrustRegistry for every validation, never the
// submission's own claims about who signed it.
type Registry struct {
	mu      sync.Mutex
	records map[string]*Record
	order   []string
	trust   *TrustRegistry
}

// NewRegistry constructs an empty qualification registry bound to a
// trust registry. Pass qualification.NewTrustRegistry() for a fresh,
// empty trust anchor set (nothing validates until real providers and
// reviewers are registered into it).
func NewRegistry(trust *TrustRegistry) *Registry {
	if trust == nil {
		trust = NewTrustRegistry()
	}
	return &Registry{records: map[string]*Record{}, trust: trust}
}

// RegisterBlocked declares a gate BLOCKED_EXTERNAL — the honest,
// unfakeable starting state every one of the eight external gates
// begins in.
func (r *Registry) RegisterBlocked(gateID, description, blocker string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.records[gateID]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicateGate, gateID)
	}
	r.records[gateID] = &Record{
		GateID: gateID, Description: description, Blocker: blocker,
		Status: StatusBlockedExternal,
	}
	r.order = append(r.order, gateID)
	return nil
}

func (r *Registry) transition(rec *Record, to Status, reason string, tick uint64) {
	rec.History = append(rec.History, Transition{From: rec.Status, To: to, Reason: reason, Tick: tick})
	rec.Status = to
}

// SubmitEvidence records a real-world evidence submission against a
// blocked gate. Submission alone never advances a gate past
// EVIDENCE_SUBMITTED — it only makes the evidence available for
// Validate.
func (r *Registry) SubmitEvidence(gateID string, ev ExternalEvidence) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	switch rec.Status {
	case StatusBlockedExternal, StatusEvidenceRejected, StatusExpired:
	default:
		return fmt.Errorf("%w: cannot submit evidence while %s is %s", ErrIllegalTransition, gateID, rec.Status)
	}
	ev.GateID = gateID
	rec.Evidence = append(rec.Evidence, ev)
	r.transition(rec, StatusEvidenceSubmitted, "evidence submitted: provider="+ev.ProviderID+" report="+ev.ReportID, ev.SubmittedAtTick)
	return nil
}

// Validate is the full cryptographic and release-binding check: it
// independently recomputes the artifact hash, resolves ProviderID and
// ReviewerID against the TrustRegistry (rejecting anything unknown,
// revoked or outside its validity window), verifies both signatures
// against those registered public keys, and rejects evidence bound to
// any commit or source hash other than releaseCommit/releaseSourceHash.
// It never takes the submitter's own claim of validity, identity or
// authorization at face value.
func (r *Registry) Validate(gateID string, nowTick uint64, releaseCommit, releaseSourceHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	if rec.Status != StatusEvidenceSubmitted {
		return fmt.Errorf("%w: %s is %s, not EVIDENCE_SUBMITTED", ErrIllegalTransition, gateID, rec.Status)
	}
	ev := rec.Evidence[len(rec.Evidence)-1]

	if err := ev.validateComplete(); err != nil {
		r.transition(rec, StatusEvidenceRejected, err.Error(), nowTick)
		return err
	}
	if ev.ExpiresAtTick <= nowTick {
		r.transition(rec, StatusEvidenceRejected, ErrAlreadyExpired.Error(), nowTick)
		return ErrAlreadyExpired
	}
	if ev.Commit != releaseCommit || ev.SourceHash != releaseSourceHash {
		r.transition(rec, StatusEvidenceRejected, ErrReleaseMismatch.Error(), nowTick)
		return ErrReleaseMismatch
	}

	provider, ok := r.trust.Provider(ev.ProviderID)
	if !ok {
		r.transition(rec, StatusEvidenceRejected, ErrUnknownProvider.Error(), nowTick)
		return fmt.Errorf("%w: %s", ErrUnknownProvider, ev.ProviderID)
	}
	if provider.Revoked {
		r.transition(rec, StatusEvidenceRejected, ErrProviderRevoked.Error(), nowTick)
		return ErrProviderRevoked
	}
	if !provider.activeAt(nowTick) {
		r.transition(rec, StatusEvidenceRejected, ErrProviderInactive.Error(), nowTick)
		return ErrProviderInactive
	}
	if !provider.authorizedFor(gateID) {
		r.transition(rec, StatusEvidenceRejected, ErrProviderNotAuthorized.Error(), nowTick)
		return ErrProviderNotAuthorized
	}

	reviewer, ok := r.trust.Reviewer(ev.ReviewerID)
	if !ok {
		r.transition(rec, StatusEvidenceRejected, ErrUnknownReviewer.Error(), nowTick)
		return fmt.Errorf("%w: %s", ErrUnknownReviewer, ev.ReviewerID)
	}
	if reviewer.Revoked {
		r.transition(rec, StatusEvidenceRejected, ErrReviewerRevoked.Error(), nowTick)
		return ErrReviewerRevoked
	}
	if !reviewer.activeAt(nowTick) {
		r.transition(rec, StatusEvidenceRejected, ErrReviewerInactive.Error(), nowTick)
		return ErrReviewerInactive
	}

	providerPub, err := provider.publicKey()
	if err != nil {
		r.transition(rec, StatusEvidenceRejected, err.Error(), nowTick)
		return err
	}
	if !verifySignature(providerPub, ev.ArtifactHash, ev.ProviderSignature) {
		r.transition(rec, StatusEvidenceRejected, ErrProviderSignature.Error(), nowTick)
		return ErrProviderSignature
	}
	reviewerPub, err := reviewer.publicKey()
	if err != nil {
		r.transition(rec, StatusEvidenceRejected, err.Error(), nowTick)
		return err
	}
	if !verifySignature(reviewerPub, ev.ArtifactHash, ev.ReviewerSignature) {
		r.transition(rec, StatusEvidenceRejected, ErrReviewerSignature.Error(), nowTick)
		return ErrReviewerSignature
	}

	r.transition(rec, StatusEvidenceValidated, "artifact hash, release binding, provider and reviewer signatures all verified", nowTick)
	return nil
}

// Qualify advances validated evidence to QUALIFIED — the gate's
// external dependency has real, checked, unexpired, cryptographically
// authenticated evidence behind it.
func (r *Registry) Qualify(gateID string, nowTick uint64) error {
	_, span := telemetry.StartSpan(context.Background(), "qualification.Qualify",
		telemetry.Attribute{Key: "gate_id", Value: gateID})
	defer span.End()

	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	if rec.Status != StatusEvidenceValidated {
		return fmt.Errorf("%w: %s is %s, not EVIDENCE_VALIDATED", ErrIllegalTransition, gateID, rec.Status)
	}
	r.transition(rec, StatusQualified, "validated evidence accepted", nowTick)
	return nil
}

// VerifyGate is the final step: an operator of record confirms the
// qualified evidence remains the basis for release. It re-checks
// expiry AND re-checks that the provider/reviewer credentials backing
// the evidence have not since been revoked — a QUALIFIED gate whose
// trust anchor lapsed after qualification cannot coast to VERIFIED.
func (r *Registry) VerifyGate(gateID string, nowTick uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[gateID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownGate, gateID)
	}
	if rec.Status != StatusQualified {
		return fmt.Errorf("%w: %s is %s, not QUALIFIED", ErrIllegalTransition, gateID, rec.Status)
	}
	if !rec.Satisfied(nowTick) {
		r.transition(rec, StatusExpired, "no unexpired evidence at verification time", nowTick)
		return ErrAlreadyExpired
	}
	ev := rec.Evidence[len(rec.Evidence)-1]
	if provider, ok := r.trust.Provider(ev.ProviderID); !ok || provider.Revoked || !provider.activeAt(nowTick) {
		r.transition(rec, StatusExpired, "provider credential no longer trusted at verification time", nowTick)
		return ErrProviderRevoked
	}
	if reviewer, ok := r.trust.Reviewer(ev.ReviewerID); !ok || reviewer.Revoked || !reviewer.activeAt(nowTick) {
		r.transition(rec, StatusExpired, "reviewer credential no longer trusted at verification time", nowTick)
		return ErrReviewerRevoked
	}
	r.transition(rec, StatusVerified, "qualification confirmed by operator; trust anchors still valid", nowTick)
	return nil
}

// ExpireStale walks every gate and demotes QUALIFIED/VERIFIED records
// whose evidence has lapsed, or whose backing trust anchors have been
// revoked, as of nowTick. Call this before assessing release readiness
// so neither a stale qualification nor a since-revoked credential can
// ever coast on a status set when both were still valid.
func (r *Registry) ExpireStale(nowTick uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.order {
		rec := r.records[id]
		if rec.Status != StatusQualified && rec.Status != StatusVerified {
			continue
		}
		stale := !rec.Satisfied(nowTick)
		if !stale && len(rec.Evidence) > 0 {
			ev := rec.Evidence[len(rec.Evidence)-1]
			if provider, ok := r.trust.Provider(ev.ProviderID); !ok || provider.Revoked || !provider.activeAt(nowTick) {
				stale = true
			}
			if reviewer, ok := r.trust.Reviewer(ev.ReviewerID); !ok || reviewer.Revoked || !reviewer.activeAt(nowTick) {
				stale = true
			}
		}
		if stale {
			r.transition(rec, StatusExpired, "evidence or trust anchor no longer valid", nowTick)
		}
	}
}

// Get returns one gate's qualification record.
func (r *Registry) Get(gateID string) (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[gateID]
	if !ok {
		return Record{}, false
	}
	return *rec, true
}

// Records returns every qualification record in registration order.
func (r *Registry) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, *r.records[id])
	}
	return out
}

// JSON renders every qualification record, stably, for evidence/*.json
// artifacts and the readiness manifest.
func (r *Registry) JSON() ([]byte, error) {
	return json.MarshalIndent(r.Records(), "", "  ")
}

// NewEvidence content-addresses a submission from its identity fields
// and measurement, so a caller building an ExternalEvidence value gets
// the correct ArtifactHash without hand-computing it. It does NOT, and
// cannot, produce ProviderSignature or ReviewerSignature: those must
// come from the actual provider and reviewer's private keys, which
// this package never has access to.
func NewEvidence(e ExternalEvidence) ExternalEvidence {
	e.ArtifactHash = computeArtifactHash(e)
	return e
}
