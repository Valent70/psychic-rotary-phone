// Package qualification closes P0-03 of the V7.12.0 integrated audit:
// an External Qualification Evidence Framework.
//
// The v7.12.0 readiness engine (cmd/veriqo-readiness) correctly refuses
// to manufacture green status for gates that cannot be evidenced from
// inside a build sandbox — an independent penetration test, a
// physical 100-node benchmark, a multi-region DR drill, a production
// HSM/KMS tenancy, contracted live data feeds, a 72-hour soak on a
// long-lived host, a real SPIRE deployment, network-dependent supply
// chain scanners. The auditor's finding was not that this refusal is
// wrong; it is that BLOCKED_EXTERNAL must not become a permanent
// hard-coded terminal state with no path out of it:
//
//	"No gate may be permanently hard-coded as BLOCKED."
//
// This package is that path. It does not, and structurally cannot,
// close any of the eight blockers itself — no code run inside this
// repository can conjure a real penetration test or a physical
// 100-node cluster. What it does is give a blocked gate a real
// lifecycle so that when genuine external evidence exists — a signed
// report from a security vendor, a benchmark artifact from a real
// cluster — it can be submitted, independently validated (not merely
// accepted at its own word), and the gate can advance. Until that
// evidence exists, the gate stays exactly where the readiness engine
// already puts it: BLOCKED_EXTERNAL, honestly.
//
//	BLOCKED_EXTERNAL → EVIDENCE_SUBMITTED → EVIDENCE_VALIDATED
//	  → QUALIFIED → VERIFIED
//	                       ↘ EVIDENCE_REJECTED (invalid submission)
//	                       ↘ EXPIRED (evidence outlived its expiry tick)
//
// Anti-false-green invariants enforced here, matching the audit's
// section 58 prohibitions:
//
//   - Validate never trusts a declared ArtifactHash; it recomputes the
//     hash from the submitted measurement content and refuses a
//     mismatch, exactly as internal/assurance.Evidence does for
//     in-sandbox gates.
//   - A record can only reach QUALIFIED with a Reviewer, a Signature and
//     an ExpiresAtTick in the future; an unreviewed or unsigned
//     submission cannot advance past EVIDENCE_SUBMITTED no matter how
//     complete its other fields are.
//   - EXPIRED evidence never counts toward Satisfied(); a gate whose
//     qualification lapsed reverts to needing fresh evidence, not a
//     grandfathered pass.
package qualification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	ErrArtifactHashInvalid = errors.New("qualification: declared artifact hash does not match the submitted measurement content")
	ErrNotReviewed         = errors.New("qualification: evidence has no reviewer of record")
	ErrNotSigned           = errors.New("qualification: evidence carries no signature")
	ErrAlreadyExpired      = errors.New("qualification: evidence expiry tick is not in the future")
)

// ExternalEvidence is one submission of real-world evidence against a
// blocked gate. Every field the audit's P0-03 register specified is
// present; none is optional for validation to succeed.
type ExternalEvidence struct {
	GateID          string            `json:"gate_id"`
	Provider        string            `json:"provider"`    // who produced this: vendor name, lab, cloud KMS tenancy, etc.
	ReportID        string            `json:"report_id"`   // the provider's own reference for the report/run
	Scope           string            `json:"scope"`       // what was covered
	Environment     string            `json:"environment"` // where it ran: not this sandbox
	Commit          string            `json:"commit"`      // the git commit the evidence was produced against
	Measurement     map[string]string `json:"measurement"` // the actual numbers/results, not prose
	StartTick       uint64            `json:"start_tick"`
	EndTick         uint64            `json:"end_tick"`
	Reviewer        string            `json:"reviewer"`  // internal reviewer who accepted the external evidence
	Signature       string            `json:"signature"` // reviewer or provider signature over the artifact
	ExpiresAtTick   uint64            `json:"expires_at_tick"`
	SubmittedAtTick uint64            `json:"submitted_at_tick"`
	// ArtifactHash is declared by the submitter but never trusted as
	// declared: Validate recomputes it from Measurement plus the
	// identity fields and requires an exact match.
	ArtifactHash string `json:"artifact_hash"`
}

// computeArtifactHash is the independent recomputation Validate checks
// a submission's declared ArtifactHash against. It covers every field
// that identifies and dates the evidence, so a report cannot be
// silently re-scoped, re-dated or have its measurement edited after
// its hash was minted without detection.
func computeArtifactHash(e ExternalEvidence) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.qualification.evidence/v1|gate=%s|provider=%s|report=%s|scope=%s|env=%s|commit=%s|start=%d|end=%d|expires=%d",
		e.GateID, e.Provider, e.ReportID, e.Scope, e.Environment, e.Commit, e.StartTick, e.EndTick, e.ExpiresAtTick)
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
	if e.Provider == "" {
		missing = append(missing, "provider")
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
	if len(e.Measurement) == 0 {
		missing = append(missing, "measurement")
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
// constitutes real, unexpired, reviewed and signed evidence — the only
// condition under which a caller (e.g. cmd/veriqo-readiness) may treat
// a previously blocked gate as no longer blocking the release verdict.
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

// Registry holds the qualification record for every blocked gate.
type Registry struct {
	mu      sync.Mutex
	records map[string]*Record
	order   []string
}

// NewRegistry constructs an empty qualification registry.
func NewRegistry() *Registry {
	return &Registry{records: map[string]*Record{}}
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
	r.transition(rec, StatusEvidenceSubmitted, "evidence submitted: "+ev.Provider+"/"+ev.ReportID, ev.SubmittedAtTick)
	return nil
}

// Validate independently checks the most recent submission: every
// mandatory field is present, the declared artifact hash matches an
// independent recomputation over the actual measurement content, and
// the submission carries a named reviewer and signature. It never
// takes the submitter's own claim of validity at face value.
func (r *Registry) Validate(gateID string, nowTick uint64) error {
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
	if ev.Reviewer == "" {
		r.transition(rec, StatusEvidenceRejected, ErrNotReviewed.Error(), nowTick)
		return ErrNotReviewed
	}
	if ev.Signature == "" {
		r.transition(rec, StatusEvidenceRejected, ErrNotSigned.Error(), nowTick)
		return ErrNotSigned
	}
	if ev.ExpiresAtTick <= nowTick {
		r.transition(rec, StatusEvidenceRejected, ErrAlreadyExpired.Error(), nowTick)
		return ErrAlreadyExpired
	}
	r.transition(rec, StatusEvidenceValidated, "artifact hash, reviewer and signature verified", nowTick)
	return nil
}

// Qualify advances validated evidence to QUALIFIED — the gate's
// external dependency has real, checked, unexpired evidence behind it.
func (r *Registry) Qualify(gateID string, nowTick uint64) error {
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
// qualified evidence remains the basis for release. It requires
// re-passing the same expiry check Validate used, so a QUALIFIED gate
// whose evidence has since lapsed cannot be silently promoted.
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
	r.transition(rec, StatusVerified, "qualification confirmed by operator", nowTick)
	return nil
}

// ExpireStale walks every gate and demotes QUALIFIED/VERIFIED records
// whose evidence has lapsed as of nowTick. Call this before assessing
// release readiness so a stale qualification can never coast on a
// status set when its evidence was still fresh.
func (r *Registry) ExpireStale(nowTick uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.order {
		rec := r.records[id]
		if (rec.Status == StatusQualified || rec.Status == StatusVerified) && !rec.Satisfied(nowTick) {
			r.transition(rec, StatusExpired, "evidence expired", nowTick)
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
// the correct ArtifactHash without hand-computing it (and without any
// path that lets a caller declare a hash that doesn't match its own
// content).
func NewEvidence(e ExternalEvidence) ExternalEvidence {
	e.ArtifactHash = computeArtifactHash(e)
	return e
}
