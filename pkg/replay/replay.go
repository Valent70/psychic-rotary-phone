// Package replay closes PHASE 9 and PHASE 10 of the v7.10.3
// production-readiness audit.
//
// PHASE 9 (IVF 2.0) — v7.10.3's replay proved exactly one thing: that
// a plurality winner could be recomputed from raw fusion records. That
// is one stage out of eleven. Everything after arbitration — truth,
// dependency discounting, risk, decision, twin, economic consequence —
// was covered only by the certificate hash, which proves the OUTPUT
// was not edited, not that the output was REACHABLE from the input.
// This package replays the whole lifecycle:
//
//	Raw Evidence -> Evidence Graph -> Provenance -> Truth -> Causal
//	-> Risk -> Decision -> Digital Twin -> Economic Consequence
//
// PHASE 10 (replay identity separation) — v7.10.3 set
// ReplayID = CanonicalCertificate.Hash, which makes "the replay
// matched" tautological: the identifier of the check was the thing
// being checked. This package gives every artifact its own identity:
//
//	ExecutionID              — identity of the ORIGINAL execution
//	EvidencePackageID        — identity of the INPUT evidence set
//	ReplayPackageID          — identity of the REPLAY REQUEST
//	OriginalResultHash       — what the original run produced
//	ReplayResultHash         — what the independent re-run produced
//	VerificationCertificateID— identity of the VERDICT
//
// and the assertion under test is the audit's own wording:
//
//	ReplayResultHash == OriginalResultHash
//
// INDEPENDENCE is structural, not promised. Replay() takes ONLY
// serialized bytes. It constructs brand-new engine instances via
// canonical.NewPipeline and rebuilds the dependency graph from its
// ledger alone via evidencegraph.Rebuild. It holds no pointer to, and
// cannot reach, the pipeline that produced the original result. The
// only way for a replay to match is for the computation to actually be
// reproducible from the recorded inputs.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/evidencegraph"
)

// Errors returned by the replay engine.
var (
	ErrPackageTampered   = errors.New("replay: package hash does not match its own content")
	ErrLedgerTampered    = errors.New("replay: dependency ledger failed independent verification")
	ErrExecutionMismatch = errors.New("replay: replayed result differs from the original result")
	ErrEmptyPackage      = errors.New("replay: execution record carries no case input")
	ErrCertTampered      = errors.New("replay: verification certificate hash does not match its own fields")
)

// StageResult is one lifecycle stage's deterministic fingerprint. The
// stage list is fixed and ordered; a replay reports the FIRST stage at
// which it diverged, which turns "replay failed" into an actionable
// defect location.
type StageResult struct {
	Stage string `json:"stage"`
	Hash  string `json:"hash"`
}

// Canonical stage names, in lifecycle order.
const (
	StageEvidence      = "evidence"
	StageDependency    = "dependency_graph"
	StageProvenance    = "provenance"
	StageArbitration   = "truth_arbitration"
	StageContradiction = "contradiction"
	StageCausal        = "causal"
	StageRisk          = "risk"
	StageDecision      = "decision"
	StageTwin          = "digital_twin"
	StageEconomic      = "economic_consequence"
	StageCertificate   = "canonical_certificate"
	// StageIdentity binds IdentityLedgerHead into the same tamper-
	// detection ResultHash every other stage participates in (audit
	// follow-up on P0-E: "IdentityLedgerHead belum ikut masuk ke
	// Replay tamper-detection hash"). Appended last rather than
	// inserted in resolution order so existing StageOrder indices
	// used elsewhere are not renumbered.
	StageIdentity = "identity_ledger"
)

// StageOrder is the authoritative ordering used for divergence
// reporting.
var StageOrder = []string{
	StageEvidence, StageDependency, StageProvenance, StageArbitration,
	StageContradiction, StageCausal, StageRisk, StageDecision, StageTwin,
	StageEconomic, StageCertificate, StageIdentity,
}

// ExecutionRecord is the complete, self-contained record of one
// canonical execution — everything needed to reproduce it, and nothing
// that points back at the engine that ran it.
type ExecutionRecord struct {
	ExecutionID        string                           `json:"execution_id"`
	EvidencePackageID  string                           `json:"evidence_package_id"`
	ActorID            string                           `json:"actor_id"`
	CaseInput          canonical.CaseInput              `json:"case_input"`
	DependencyLedger   []evidencegraph.DependencyRecord `json:"dependency_ledger"`
	Stages             []StageResult                    `json:"stages"`
	OriginalResultHash string                           `json:"original_result_hash"`
	SchemaVersion      string                           `json:"schema_version"`
	// IdentityLedgerHead is the real *identity.Resolver.Head() the
	// original execution's identity resolution was bound to, when the
	// caller had one (empty otherwise -- nil-safe, preserving every
	// prior caller's exact behavior byte-for-byte). Closing a real gap
	// named by audit follow-up P0-E: cold replay previously carried
	// Evidence + Dependency + Decision + Twin but never Identity, so
	// "same posterior, same decision, same twin" could be reproduced
	// from an export that had silently forgotten WHICH identity-ledger
	// state those conclusions were reached against. It is carried
	// through JSON round-trip like every other field here (see
	// SchemaVersion's own comment on why this struct is the complete,
	// self-contained record) -- an independent replayer can compare it
	// against its own ledger's Head() without needing the pointer that
	// produced it, the same "data, not engine pointers" discipline
	// ReplayPackage's own doc comment states.
	//
	// This field now DOES feed Replay()'s own tamper-detection: it is
	// folded into ResultHash as its own StageIdentity fingerprint (see
	// stageFingerprints), exactly like every other stage. Record()
	// commits a fingerprint of the value it was given; Replay()
	// recomputes a fingerprint from exec.IdentityLedgerHead as read off
	// the (possibly tampered) package. An untampered round trip
	// reproduces the identical fingerprint; a tampered one changes
	// ReplayResultHash without touching the OriginalResultHash already
	// committed at Record time, so Replay() catches it the same way it
	// already catches a tampered CaseInput -- closing what was
	// previously an honestly-documented scope limitation (this
	// comment used to say the field survived serialization but did not
	// participate in tamper-detection; a later round's own audit named
	// that gap explicitly, and it is closed here).
	IdentityLedgerHead string `json:"identity_ledger_head,omitempty"`
}

// SchemaVersion for the replay contract (PHASE 51).
const SchemaVersion = "veriqo.replay/v1"

// ReplayPackage is a REQUEST to replay an execution. Its identity is
// deliberately distinct from the execution's: two independent auditors
// replaying the same execution produce two ReplayPackageIDs and two
// verification certificates over one ExecutionID.
type ReplayPackage struct {
	ReplayPackageID string          `json:"replay_package_id"`
	RequestedBy     string          `json:"requested_by"`
	Nonce           string          `json:"nonce"`
	Execution       ExecutionRecord `json:"execution"`
}

// VerificationCertificate is the verdict of one replay.
type VerificationCertificate struct {
	VerificationCertificateID string `json:"verification_certificate_id"`
	ExecutionID               string `json:"execution_id"`
	EvidencePackageID         string `json:"evidence_package_id"`
	ReplayPackageID           string `json:"replay_package_id"`
	OriginalResultHash        string `json:"original_result_hash"`
	ReplayResultHash          string `json:"replay_result_hash"`
	Match                     bool   `json:"match"`
	DivergedStage             string `json:"diverged_stage,omitempty"`
	DivergedOriginalHash      string `json:"diverged_original_hash,omitempty"`
	DivergedReplayHash        string `json:"diverged_replay_hash,omitempty"`
	StagesCompared            int    `json:"stages_compared"`
	SchemaVersion             string `json:"schema_version"`
}

func hashJSON(prefix string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|", prefix)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Record captures a completed canonical execution as an independently
// replayable ExecutionRecord. It reads only the RESULT and the INPUT —
// it does not retain the pipeline.
func Record(actorID string, in canonical.CaseInput, res *canonical.CanonicalResult, depLedger []evidencegraph.DependencyRecord, identityLedgerHead string) (ExecutionRecord, error) {
	if len(in.Submissions) == 0 {
		return ExecutionRecord{}, ErrEmptyPackage
	}
	rec := ExecutionRecord{
		ActorID: actorID, CaseInput: in, DependencyLedger: depLedger,
		SchemaVersion: SchemaVersion, IdentityLedgerHead: identityLedgerHead,
	}
	var err error
	if rec.EvidencePackageID, err = hashJSON("veriqo.evidence_package/v1", in.Submissions); err != nil {
		return ExecutionRecord{}, err
	}
	if rec.ExecutionID, err = hashJSON("veriqo.execution/v1", struct {
		Actor string              `json:"actor"`
		Case  canonical.CaseInput `json:"case"`
		Deps  []evidencegraph.DependencyRecord
	}{actorID, in, depLedger}); err != nil {
		return ExecutionRecord{}, err
	}
	rec.Stages = stageFingerprints(res, identityLedgerHead)
	rec.OriginalResultHash = resultHash(rec.Stages)
	return rec, nil
}

// stageFingerprints computes one deterministic hash per lifecycle
// stage from a canonical result. Every stage is derived from the
// RESULT objects, never from engine internals, so a replay computes
// them the same way from its own result.
//
// identityLedgerHead is folded in as its own StageIdentity fingerprint
// (audit follow-up: IdentityLedgerHead previously survived
// serialization but did not participate in ResultHash, so tampering it
// after Record went undetected by Replay). Both callers pass the value
// straight from the ExecutionRecord they hold -- Record from the value
// it was just given, Replay from exec.IdentityLedgerHead as read off
// the (possibly tampered) package -- so an untampered round trip
// reproduces the identical fingerprint and a tampered one does not,
// the exact same mechanism CaseInput tampering already relies on via
// resultHash comparison against the ORIGINAL committed hash.
func stageFingerprints(res *canonical.CanonicalResult, identityLedgerHead string) []StageResult {
	h := func(prefix string, v any) string {
		s, err := hashJSON(prefix, v)
		if err != nil {
			return "ERR:" + err.Error()
		}
		return s
	}
	return []StageResult{
		{StageEvidence, h(StageEvidence, res.Arbitration.Explanation)},
		{StageDependency, h(StageDependency, struct {
			Root      string             `json:"root"`
			Shared    map[string]float64 `json:"shared"`
			Effective map[string]float64 `json:"effective"`
			Families  [][]string         `json:"families"`
		}{res.Dependency.RootHash, res.Dependency.SharedFraction, res.Dependency.Effective, res.Dependency.Families})},
		{StageProvenance, h(StageProvenance, res.Provenance)},
		{StageArbitration, h(StageArbitration, struct {
			Winner string  `json:"winner"`
			Conf   float64 `json:"conf"`
			Runner string  `json:"runner"`
			RConf  float64 `json:"rconf"`
			Contra bool    `json:"contra"`
		}{res.Arbitration.Winner, res.Arbitration.WinnerConfidence, res.Arbitration.RunnerUp, res.Arbitration.RunnerUpConfidence, res.Arbitration.Contradiction})},
		{StageContradiction, h(StageContradiction, res.Truth.Observation)},
		{StageCausal, h(StageCausal, res.CausalSupport)},
		{StageRisk, h(StageRisk, res.Risk)},
		{StageDecision, h(StageDecision, struct {
			Action string  `json:"action"`
			Score  float64 `json:"score"`
			Policy string  `json:"policy"`
		}{string(res.Decision.Action), res.Decision.RiskScore, res.Decision.PolicyName})},
		{StageTwin, h(StageTwin, res.Twin.Attributes)},
		{StageEconomic, h(StageEconomic, res.EconomicImpact)},
		{StageCertificate, h(StageCertificate, struct {
			Winner   string  `json:"winner"`
			Prov     string  `json:"prov"`
			Label    string  `json:"label"`
			Score    float64 `json:"score"`
			Action   string  `json:"action"`
			DepRoot  string  `json:"dep_root"`
			DepMax   float64 `json:"dep_max"`
			Families int     `json:"families"`
		}{res.Certificate.ArbitrationWinner, string(res.Certificate.ProvenanceStatus),
			string(res.Certificate.RiskLabel), res.Certificate.RiskScore,
			string(res.Certificate.DecisionAction), res.Certificate.DependencyRootHash,
			res.Certificate.MaxDependencyDiscount, res.Certificate.IndependentFamilies})},
		{StageIdentity, h(StageIdentity, identityLedgerHead)},
	}
}

// resultHash folds every stage fingerprint into one result hash.
// Deliberately NOT the canonical certificate hash: the certificate
// includes engine-position-dependent values (TwinHead, ledger-chained
// arbitration hash) that legitimately differ between a live engine and
// a clean replay engine. Conflating "the ledger is at a different
// position" with "the computation diverged" is exactly the false
// negative PHASE 10 exists to remove.
func resultHash(stages []StageResult) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.replay_result/v1|")
	for _, s := range stages {
		fmt.Fprintf(h, "%s=%s|", s.Stage, s.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NewReplayPackage builds a replay REQUEST with its own identity.
func NewReplayPackage(requestedBy, nonce string, exec ExecutionRecord) (ReplayPackage, error) {
	p := ReplayPackage{RequestedBy: requestedBy, Nonce: nonce, Execution: exec}
	id, err := hashJSON("veriqo.replay_package/v1", struct {
		By    string `json:"by"`
		Nonce string `json:"nonce"`
		Exec  string `json:"exec"`
	}{requestedBy, nonce, exec.ExecutionID})
	if err != nil {
		return ReplayPackage{}, err
	}
	p.ReplayPackageID = id
	return p, nil
}

// Engine is the independent replay engine. It holds NO reference to
// any live pipeline — construction takes no arguments, by design.
type Engine struct{}

// NewEngine constructs the replay engine.
func NewEngine() *Engine { return &Engine{} }

// Replay independently re-executes a recorded canonical execution and
// issues a VerificationCertificate. Nothing but the serialized package
// is consulted.
func (Engine) Replay(pkg ReplayPackage) (VerificationCertificate, error) {
	exec := pkg.Execution
	if len(exec.CaseInput.Submissions) == 0 {
		return VerificationCertificate{}, ErrEmptyPackage
	}

	// 1. Verify the recorded dependency ledger independently BEFORE
	//    using it. A replay that trusts a tampered dependency model is
	//    not a verification.
	rebuilt, err := evidencegraph.Rebuild(exec.DependencyLedger)
	if err != nil {
		return VerificationCertificate{}, fmt.Errorf("%w: %v", ErrLedgerTampered, err)
	}

	// 2. Round-trip the case input through serialization so the replay
	//    provably consumes DATA, not a shared in-memory object.
	raw, err := json.Marshal(exec.CaseInput)
	if err != nil {
		return VerificationCertificate{}, err
	}
	var caseIn canonical.CaseInput
	if err := json.Unmarshal(raw, &caseIn); err != nil {
		return VerificationCertificate{}, err
	}

	// 3. Fresh engines. No access to the originals.
	p := canonical.NewPipeline(nil)
	p.Dependencies = rebuilt

	res, err := p.RunCanonical(exec.ActorID, caseIn)
	if err != nil {
		return VerificationCertificate{}, fmt.Errorf("replay: re-execution failed: %w", err)
	}

	replayStages := stageFingerprints(res, exec.IdentityLedgerHead)
	cert := VerificationCertificate{
		ExecutionID: exec.ExecutionID, EvidencePackageID: exec.EvidencePackageID,
		ReplayPackageID: pkg.ReplayPackageID, OriginalResultHash: exec.OriginalResultHash,
		ReplayResultHash: resultHash(replayStages), StagesCompared: len(replayStages),
		SchemaVersion: SchemaVersion,
	}
	cert.Match = cert.ReplayResultHash == cert.OriginalResultHash
	if !cert.Match {
		cert.DivergedStage, cert.DivergedOriginalHash, cert.DivergedReplayHash = firstDivergence(exec.Stages, replayStages)
	}
	cert.VerificationCertificateID = certificateID(cert)
	return cert, nil
}

// firstDivergence locates the earliest lifecycle stage whose
// fingerprint differs.
func firstDivergence(original, replayed []StageResult) (stage, origHash, replHash string) {
	om := map[string]string{}
	for _, s := range original {
		om[s.Stage] = s.Hash
	}
	rm := map[string]string{}
	for _, s := range replayed {
		rm[s.Stage] = s.Hash
	}
	for _, name := range StageOrder {
		o, okO := om[name]
		r, okR := rm[name]
		if !okO || !okR || o != r {
			return name, o, r
		}
	}
	return "", "", ""
}

func certificateID(c VerificationCertificate) string {
	h := sha256.New()
	fmt.Fprintf(h, "veriqo.verification_certificate/v1|exec=%s|evpkg=%s|replaypkg=%s|orig=%s|repl=%s|match=%v|div=%s|n=%d",
		c.ExecutionID, c.EvidencePackageID, c.ReplayPackageID, c.OriginalResultHash,
		c.ReplayResultHash, c.Match, c.DivergedStage, c.StagesCompared)
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyCertificate independently recomputes a verification
// certificate's own ID — the verdict is itself tamper-evident.
func VerifyCertificate(c VerificationCertificate) error {
	if certificateID(c) != c.VerificationCertificateID {
		return ErrCertTampered
	}
	return nil
}

// Assert is the audit's stated assertion, as a callable check:
// ReplayResultHash == OriginalResultHash.
func (c VerificationCertificate) Assert() error {
	if err := VerifyCertificate(c); err != nil {
		return err
	}
	if !c.Match {
		return fmt.Errorf("%w: stage=%s original=%s replay=%s",
			ErrExecutionMismatch, c.DivergedStage, c.DivergedOriginalHash, c.DivergedReplayHash)
	}
	return nil
}

// Marshal/Unmarshal give the replay package a wire form, so an
// external auditor can be handed a file and reach the same verdict on
// their own machine with their own binary.
func (p ReplayPackage) Marshal() ([]byte, error) { return json.Marshal(p) }

// Unmarshal parses a replay package and re-derives its ID, rejecting
// packages whose declared identity does not match their content.
func Unmarshal(data []byte) (ReplayPackage, error) {
	var p ReplayPackage
	if err := json.Unmarshal(data, &p); err != nil {
		return ReplayPackage{}, err
	}
	want, err := hashJSON("veriqo.replay_package/v1", struct {
		By    string `json:"by"`
		Nonce string `json:"nonce"`
		Exec  string `json:"exec"`
	}{p.RequestedBy, p.Nonce, p.Execution.ExecutionID})
	if err != nil {
		return ReplayPackage{}, err
	}
	if want != p.ReplayPackageID {
		return ReplayPackage{}, ErrPackageTampered
	}
	return p, nil
}
