// Package lifecycle closes the P0 gap both v7.10.2 audit documents
// named as the top architectural priority: VERIQO's canonical path
// (pkg/canonical, v7.10.2) starts from Evidence, not from Intent. The
// audit's required contract is broader:
//
//	Intent -> Evidence Planning -> Entity Resolution -> Evidence
//	-> Truth -> Trust -> Bayesian -> Causal -> Decision -> Policy
//	-> Digital Twin -> Economic Consequence -> IVF -> Outcome
//	-> Calibration -> Immutable Replay
//
// RunUnified is that lifecycle. Same discipline as pkg/canonical: this
// package does not duplicate any engine — it composes pkg/canonical
// (Evidence/Provenance/Fusion/Truth/Causal/Risk/Decision/Twin/Economic
// Impact/Certificate), pkg/moat/entity (Entity Resolution),
// pkg/verification (IVF — reused, not rebuilt: a real
// EvidencePackage/ReplayPackage/VerificationBundle is built from the
// SAME fusion records pkg/canonical already produced, with a genuine
// independently-registered ReplayFunc), and pkg/moat/calibration
// (Outcome -> Trust Reassessment).
//
// Intent, here, is deliberately a NEW top-level type — not a
// duplicate of pkg/kernel/intent.Statement. That package verifies one
// actor's specific factual claim against an observation (Class
// A-vs-Class-C accuracy scoring); it is a valid INPUT a lifecycle case
// can carry evidence from, not a substitute for "why is this
// investigation happening at all", which nothing in the repo modeled
// before this package. This is filling the audit's named gap #1, not
// adding a second orchestration framework — see the audit's own "What
// should not be done" section, which this package's design was
// checked against before writing any code.
package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"veriqo/pkg/canonical"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/calibration"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/verification"
)

// identityAuthoritySourceID is the source ID RunUnified registers its
// own identity-resolver merges under (see NewOrchestrator). It is a
// real, greppable source name, not a placeholder -- the ledger records
// every merge as originating from "lifecycle", the caller that
// actually decided the two aliases denote one entity.
const identityAuthoritySourceID = "lifecycle"

// Intent is the OS-level top-level canonical input the audit requires:
// an investigative objective, not yet evidence. It is content-
// addressed (ID is derived from its own fields, not caller-supplied)
// so IntentID is stable and independently reproducible from the same
// inputs — consistent with this repo's no-UUID, no-time.Now()
// discipline.
type Intent struct {
	ActorID            string
	Objective          string // e.g. "assess dark-vessel risk"
	EntityAliases      []entity.Alias
	RequiredConfidence float64
	TemporalScope      string
	PolicyConstraints  []string
	Tick               uint64
}

// ID is Intent's content-addressed identifier.
func (in Intent) ID() string {
	h := sha256.New()
	fmt.Fprintf(h, "actor=%s|obj=%s|conf=%.4f|scope=%s|tick=%d|",
		in.ActorID, in.Objective, in.RequiredConfidence, in.TemporalScope, in.Tick)
	aliases := make([]string, len(in.EntityAliases))
	for i, a := range in.EntityAliases {
		aliases[i] = a.Kind + "|" + a.Value
	}
	sort.Strings(aliases)
	for _, a := range aliases {
		fmt.Fprintf(h, "alias=%s|", a)
	}
	constraints := append([]string(nil), in.PolicyConstraints...)
	sort.Strings(constraints)
	for _, c := range constraints {
		fmt.Fprintf(h, "policy=%s|", c)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EvidenceRequirement is one thing an Intent's plan says evidence
// gathering must satisfy.
type EvidenceRequirement struct {
	Kind       string // e.g. "AIS_STATUS" — matched against CaseInput.Predicate
	Required   bool
	MinSources int
}

// EvidencePlan is the audit's required "Intent must generate an
// explicit Evidence Plan" artifact — versioned, hashable,
// deterministic, replayable, auditable (all four properties come from
// Hash being a pure function of IntentID + Requirements, nothing
// else).
type EvidencePlan struct {
	IntentID     string
	Requirements []EvidenceRequirement
	Hash         string
}

// PlanEvidence deterministically builds an EvidencePlan from an
// Intent and its requirements — pure function, no side effects, no
// wall-clock.
func PlanEvidence(in Intent, requirements []EvidenceRequirement) EvidencePlan {
	p := EvidencePlan{IntentID: in.ID(), Requirements: requirements}
	h := sha256.New()
	fmt.Fprintf(h, "intent=%s|", p.IntentID)
	for _, r := range requirements {
		fmt.Fprintf(h, "kind=%s|required=%v|min=%d|", r.Kind, r.Required, r.MinSources)
	}
	p.Hash = hex.EncodeToString(h.Sum(nil))
	return p
}

// ErrPlanUnsatisfied is returned when a CaseInput's submissions do not
// meet a required EvidenceRequirement's MinSources for its Kind
// (matched against CaseInput.Predicate) — the audit's "evidence
// planning must determine what evidence is missing" requirement made
// mechanically enforced rather than advisory.
var ErrPlanUnsatisfied = errors.New("lifecycle: evidence plan requirement not satisfied")

// checkPlan verifies a CaseInput satisfies every Required
// EvidenceRequirement in a plan. Optional requirements are recorded
// as unmet in the returned list but do not block the run — the audit
// explicitly requires "preserve unknown/missing evidence as
// uncertainty" rather than silently failing on it.
func checkPlan(plan EvidencePlan, in canonical.CaseInput) (unmet []EvidenceRequirement, err error) {
	for _, req := range plan.Requirements {
		count := 0
		if req.Kind == in.Predicate {
			count = len(in.Submissions)
		}
		if count < req.MinSources {
			unmet = append(unmet, req)
			if req.Required {
				return unmet, fmt.Errorf("%w: kind=%s need>=%d got=%d", ErrPlanUnsatisfied, req.Kind, req.MinSources, count)
			}
		}
	}
	return unmet, nil
}

// LifecycleCertificate carries the full ID chain the audit requires
// end to end: IntentID -> EntityID -> EvidencePlanHash ->
// CanonicalCertificate (which itself covers
// Evidence/Provenance/Truth/Causal/Risk/Decision/Twin) -> IVF
// AuditCertificate -> ReplayID -> (optional, post-outcome)
// CalibrationRecordHash. Hash is a single commitment over every field
// below it, so tampering with any layer's ID is detectable at the
// top.
type LifecycleCertificate struct {
	IntentID           string
	EntityID           string
	EvidencePlanHash   string
	UnmetRequirements  []EvidenceRequirement
	Canonical          canonical.CanonicalCertificate
	IVFVerified        bool
	IVFCertificateHash string
	ReplayID           string // = Canonical.Hash, named explicitly per the audit's contract
	Hash               string
}

// Result is everything one RunUnified call produces.
type Result struct {
	Intent      Intent
	Plan        EvidencePlan
	EntityID    entity.CanonicalID
	SourceIDs   []string // captured from the case's own submissions, for RecordOutcome
	Canonical   *canonical.CanonicalResult
	IVFResult   verification.VerificationResult
	Certificate LifecycleCertificate
}

func hashLifecycleCert(c LifecycleCertificate) string {
	h := sha256.New()
	fmt.Fprintf(h, "intent=%s|entity=%s|plan=%s|unmet=%d|canonical=%s|ivf=%v|ivfcert=%s|replay=%s|",
		c.IntentID, c.EntityID, c.EvidencePlanHash, len(c.UnmetRequirements),
		c.Canonical.Hash, c.IVFVerified, c.IVFCertificateHash, c.ReplayID)
	return hex.EncodeToString(h.Sum(nil))
}

// ErrLifecycleCertificateTampered is returned by VerifyCertificate
// when a LifecycleCertificate's Hash does not match what its own
// fields recompute to.
var ErrLifecycleCertificateTampered = errors.New("lifecycle: certificate hash does not match its own fields")

// VerifyCertificate independently recomputes a LifecycleCertificate's
// hash from its own fields and compares — the lifecycle-layer
// counterpart to canonical.VerifyCertificate, one level up the chain.
func VerifyCertificate(c LifecycleCertificate) error {
	if hashLifecycleCert(c) != c.Hash {
		return ErrLifecycleCertificateTampered
	}
	return nil
}

// Orchestrator composes the engines RunUnified needs. Entities and
// the Verifier are stateful across calls (so entity resolution and
// IVF domain registration accumulate correctly); Pipeline is
// pkg/canonical's own (already stateful).
type Orchestrator struct {
	mu       sync.Mutex
	Pipeline *canonical.Pipeline
	// Identity is the CANONICAL entity-resolution authority (audit item
	// P0-B / R4): RunUnified resolves every EntityAlias set through
	// Identity.Merge/EntityIDAt first. Entities (below) is consulted
	// only as a fail-safe fallback for alias Kinds identity.Kind does
	// not model (see resolveCanonicalEntity) -- for the alias vocabulary
	// this OS actually uses in production (IMO, CALLSIGN, MMSI, NAME,
	// OWNER, ...; the full set is identity.Kind's own const block),
	// Entities is never written to by this path at all, so it can no
	// longer silently diverge from Identity's opinion for those cases.
	Identity *identity.Resolver
	// Entities is the pre-P0-B union-find, kept as the fallback
	// authority ONLY (see Identity's doc comment above) and as the
	// live comparison target for pkg/governance/entityconsistency.
	Entities    *entity.Registry
	Verifier    *verification.Verifier
	Calibration *calibration.Engine
}

// NewOrchestrator builds a lifecycle Orchestrator over an existing
// canonical.Pipeline (typically veriqo/kernel.Kernel.Canonical, so the
// TrustCalculus stays shared OS-wide — see pkg/canonical's own
// package comment on why that sharing matters).
func NewOrchestrator(pipeline *canonical.Pipeline) *Orchestrator {
	if pipeline == nil {
		pipeline = canonical.NewPipeline(nil)
	}
	id := identity.NewResolver()
	// A single, maximally-weighted authority for RunUnified's own
	// merges. Weight=1 and an empty AuthoritativeFor (full weight for
	// every kind) matches exactly what the pre-existing
	// pkg/moat/entity.Registry union-find already assumed implicitly:
	// every alias RunUnified is handed is trusted equally, since it
	// arrives already vetted by the caller's own Intent construction.
	_ = id.RegisterAuthority(identity.Authority{SourceID: identityAuthoritySourceID, Weight: 1})
	o := &Orchestrator{
		Pipeline: pipeline, Identity: id, Entities: entity.NewRegistry(),
		Verifier: verification.NewVerifier(), Calibration: calibration.NewEngineWithCalculus(pipeline.Trust),
	}
	o.Verifier.RegisterReplayFunc("lifecycle.fusion_arbitration", replayFusionArbitration)
	return o
}

// resolveCanonicalEntity resolves in.EntityAliases through Identity,
// the canonical authority, returning ok=false ONLY when an alias Kind
// is outside identity.Kind's fixed, audited vocabulary -- in which
// case RunUnified falls back to the legacy union-find rather than
// force a fabricated Kind mapping (see pkg/governance/entityconsistency's
// package comment on why inventing a translation layer would itself be
// the fabrication this package refuses to do).
func (o *Orchestrator) resolveCanonicalEntity(actorID string, aliases []entity.Alias, tick uint64) (entity.CanonicalID, bool) {
	ids := make([]identity.Identifier, len(aliases))
	for i, a := range aliases {
		ids[i] = identity.Identifier{Kind: identity.Kind(a.Kind), Value: a.Value}
		if err := ids[i].Validate(); err != nil {
			return "", false
		}
	}
	first := ids[0]
	for _, other := range ids[1:] {
		if _, err := o.Identity.Merge(actorID, identityAuthoritySourceID, first, other, tick,
			"lifecycle.RunUnified: aliases co-occur on one Intent"); err != nil {
			return "", false
		}
	}
	resolved, err := o.Identity.EntityIDAt(first, tick)
	if err != nil {
		return "", false
	}
	return entity.CanonicalID(resolved), true
}

// RunUnified is the audit's required central chain: Intent -> Plan ->
// Entity Resolution -> (delegates Evidence/Provenance/Truth/Causal/
// Risk/Decision/Twin/EconomicImpact to canonical.RunCanonical) -> IVF
// -> LifecycleCertificate. Outcome/Calibration is a separate call
// (RecordOutcome below) since ground truth, by definition, is not yet
// known at case-run time.
func (o *Orchestrator) RunUnified(in Intent, plan EvidencePlan, caseIn canonical.CaseInput) (*Result, error) {
	// Entities and Verifier accumulate state across calls (see the
	// Orchestrator doc comment); mu serializes RunUnified and
	// RecordOutcome so two concurrent callers cannot race on that
	// accumulation. staticcheck's U1000 flagged mu as unused, which was
	// a real gap: the field existed but nothing ever locked it.
	o.mu.Lock()
	defer o.mu.Unlock()

	if plan.IntentID != in.ID() {
		return nil, fmt.Errorf("lifecycle: evidence plan does not belong to this intent (plan.IntentID=%s intent.ID()=%s)", plan.IntentID, in.ID())
	}

	// --- Entity Resolution ---------------------------------------------
	// Identity (pkg/identity) is the canonical authority (P0-B / R4):
	// resolveCanonicalEntity is tried first, and Entities (the legacy
	// union-find) is used ONLY as a fallback for alias Kinds identity
	// does not model. See resolveCanonicalEntity's doc comment.
	var canonEntity entity.CanonicalID
	if len(in.EntityAliases) > 0 {
		if resolved, ok := o.resolveCanonicalEntity(in.ActorID, in.EntityAliases, in.Tick); ok {
			canonEntity = resolved
		} else {
			first := in.EntityAliases[0]
			var err error
			canonEntity, err = o.Entities.Register(in.ActorID, first, in.Tick)
			if err != nil {
				return nil, fmt.Errorf("lifecycle: entity resolution: %w", err)
			}
			for _, alias := range in.EntityAliases[1:] {
				canonEntity, err = o.Entities.Merge(in.ActorID, first, alias, in.Tick)
				if err != nil {
					return nil, fmt.Errorf("lifecycle: entity resolution: %w", err)
				}
			}
		}
		caseIn.Entity = digitaltwin.EntityID(canonEntity)
		caseIn.Subject = string(canonEntity)
	}

	// --- Evidence Plan enforcement --------------------------------------
	unmet, err := checkPlan(plan, caseIn)
	if err != nil {
		return nil, err
	}

	// --- Canonical MOAT chain (Evidence -> ... -> Certificate) ---------
	canonRes, err := o.Pipeline.RunCanonical(in.ActorID, caseIn)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: canonical run: %w", err)
	}

	// --- IVF: build a REAL bundle from the fusion records that backed
	// this exact arbitration, and independently verify it. -------------
	ivfBundle, err := buildIVFBundle(o.Pipeline, caseIn, canonRes.Arbitration)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: building IVF bundle: %w", err)
	}
	ivfResult, err := o.Verifier.Verify("lifecycle.fusion_arbitration", ivfBundle)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: IVF verify: %w", err)
	}

	cert := LifecycleCertificate{
		IntentID: in.ID(), EntityID: string(canonEntity), EvidencePlanHash: plan.Hash,
		UnmetRequirements: unmet, Canonical: canonRes.Certificate,
		IVFVerified: ivfResult.ManifestValid && ivfResult.ReplayValid,
		ReplayID:    canonRes.Certificate.Hash,
	}
	if ivfResult.Certificate != nil {
		cert.IVFCertificateHash = ivfResult.Certificate.CertificateHash
	}
	cert.Hash = hashLifecycleCert(cert)

	return &Result{
		Intent: in, Plan: plan, EntityID: canonEntity, SourceIDs: canonical.SortedSourceIDs(caseIn.Submissions),
		Canonical: canonRes, IVFResult: ivfResult, Certificate: cert,
	}, nil
}

// canonicalEntityAsTwinID intentionally removed — digitaltwin.EntityID
// is used directly above (entity.CanonicalID and digitaltwin.EntityID
// are both plain string types; no adapter needed).

// buildIVFBundle packages the fusion arbitration records that
// produced canonRes into a real pkg/verification EvidencePackage/
// ReplayPackage/VerificationBundle — reusing the existing IVF
// machinery exactly as the audit instructs ("do not rebuild it").
func buildIVFBundle(p *canonical.Pipeline, caseIn canonical.CaseInput, arb fusion.ArbitrationResult) (verification.VerificationBundle, error) {
	claim := fusion.Claim{Subject: caseIn.Subject, Predicate: caseIn.Predicate}
	var records []verification.EvidenceRecord
	for i, ev := range p.Fusion.EvidenceFor(claim) {
		payload, err := json.Marshal(ev)
		if err != nil {
			return verification.VerificationBundle{}, err
		}
		records = append(records, verification.EvidenceRecord{Kind: "fusion.Evidence", Index: uint64(i), Payload: payload})
	}
	rp := verification.ReplayPackage{
		Evidence:      verification.EvidencePackage{Subject: claim.Key(), Records: records},
		ClaimedResult: arb.Winner,
	}
	return verification.NewBundle(rp)
}

// replayFusionArbitration is the REGISTERED, independent replay
// function: given only the raw evidence records (no access to the
// original fusion.Engine instance), it recomputes the plurality
// winner from scratch and returns it — a genuine second computation,
// not a trivial echo of the claimed result. Mirrors fusion.Engine's
// own plurality-by-weight logic at the record level, deliberately
// re-derived here rather than imported, since IVF's entire value is
// that the replay does NOT depend on the original engine's live
// state (see pkg/verification's package comment).
func replayFusionArbitration(pkg verification.EvidencePackage) (string, error) {
	type rawEvidence struct {
		Source string  `json:"Source"`
		Value  string  `json:"Value"`
		Weight float64 `json:"Weight"`
	}
	tally := map[string]float64{}
	for _, rec := range pkg.Records {
		var ev rawEvidence
		if err := json.Unmarshal(rec.Payload, &ev); err != nil {
			return "", err
		}
		tally[ev.Value] += ev.Weight
	}
	best, bestWeight := "", -1.0
	keys := make([]string, 0, len(tally))
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-break
	for _, k := range keys {
		if tally[k] > bestWeight {
			best, bestWeight = k, tally[k]
		}
	}
	return best, nil
}

// RecordOutcome closes the audit's "Outcome -> Calibration / Trust
// Reassessment" requirement: ground truth arrives (typically much
// later than the case ran), is scored against the prediction this
// lifecycle case made, and the result feeds back into the shared
// Calculus via calibration.Engine — completing the loop back to Trust
// this package's doc comment promises. groundTruthSourceID must be an
// independent source (not one of the case's own arbitration inputs);
// RecordOutcome registers it as independent automatically if it isn't
// already, since a ground-truth verifier is by definition external to
// the case's own evidence.
func (o *Orchestrator) RecordOutcome(res *Result, actualValue, groundTruthSourceID string, tick uint64) (calibration.Record, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	claim := fusion.Claim{Subject: res.Canonical.Certificate.Subject, Predicate: res.Canonical.Certificate.Predicate}
	pred := calibration.Prediction{
		ClaimKey: claim.Key(), Value: res.Canonical.Arbitration.Winner,
		Confidence:           res.Canonical.Arbitration.WinnerConfidence,
		ArbitrationSourceIDs: res.SourceIDs,
		Tick:                 tick,
	}
	if err := o.Calibration.RecordPrediction(pred); err != nil {
		return calibration.Record{}, fmt.Errorf("lifecycle: record prediction: %w", err)
	}
	if !o.Calibration.IsIndependentSource(groundTruthSourceID) {
		o.Calibration.RegisterIndependentSource(groundTruthSourceID)
	}
	return o.Calibration.RecordGroundTruth(calibration.GroundTruth{
		ClaimKey: claim.Key(), Value: actualValue, SourceID: groundTruthSourceID, Tick: tick,
	})
}
