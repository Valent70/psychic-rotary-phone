// Package explanation closes PHASE 26: the single consolidated
// DecisionExplanation.
//
// Before this package, VERIQO had four explanations — dependency, trust,
// authz and simulation — each correct and each partial. A reviewer who
// asked "why was this vessel flagged?" got four answers and had to join
// them by hand. The auditor's requirement was one canonical schema,
// generated from actual execution artifacts, never a template:
//
//	"Tidak boleh ada: decision = DENY, explanation = generic 'policy denied'."
//
// The design commitment that makes that enforceable is the Chain: an
// explicit ordered walk
//
//	source → evidence → dependency → weight → contradiction → posterior
//	  → causal effect → risk → policy → final decision
//
// where each Link names the stage, the input it consumed and the output
// it produced. Build() refuses to emit an explanation whose chain is
// broken, so a decision either explains itself end to end or it is not
// explained at all. The whole structure is content-addressed, so the
// audit's critical test — modify one stage, the explanation and its hash
// must both change — holds by construction rather than by discipline.
package explanation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrIncomplete refuses an explanation missing a mandatory section.
	ErrIncomplete = errors.New("explanation: mandatory section missing")
	// ErrChainBroken refuses an explanation whose stage chain does not
	// run from source to decision.
	ErrChainBroken = errors.New("explanation: causal chain from source to decision is broken")
	// ErrTemplated refuses a generic, content-free rationale — the exact
	// failure mode the audit named.
	ErrTemplated = errors.New("explanation: rationale is generic and carries no execution content")
	// ErrAlreadyCommitted refuses a second commitment: FinalCommitment is
	// write-once by construction, never overwritten.
	ErrAlreadyCommitted = errors.New("explanation: already committed; a final commitment cannot be replaced")
	// ErrNotCommitted is returned by VerifyFinal for an explanation whose
	// two-phase commitment was never completed.
	ErrNotCommitted = errors.New("explanation: not committed to an execution root")
)

// Stage names one link of the canonical chain, in order.
type Stage string

const (
	StageSource        Stage = "SOURCE"
	StageEvidence      Stage = "EVIDENCE"
	StageIdentity      Stage = "IDENTITY"
	StageDependency    Stage = "DEPENDENCY"
	StageWeight        Stage = "WEIGHT"
	StageTruth         Stage = "TRUTH"
	StageContradiction Stage = "CONTRADICTION"
	StageFusion        Stage = "FUSION"
	StageTemporal      Stage = "TEMPORAL"
	StageCausal        Stage = "CAUSAL"
	StageRisk          Stage = "RISK"
	StageTrust         Stage = "TRUST"
	StagePolicy        Stage = "POLICY"
	StageSimulation    Stage = "SIMULATION"
	StageDecision      Stage = "DECISION"
)

// requiredChain is the minimum walk an explanation must cover. Optional
// stages (identity, temporal, simulation) may appear but are not
// required, because a case with no temporal model legitimately has no
// temporal link — whereas a case with no dependency link means the
// PHASE 1 gate was bypassed.
var requiredChain = []Stage{
	StageSource, StageEvidence, StageDependency, StageWeight,
	StageTruth, StageFusion, StageRisk, StagePolicy, StageDecision,
}

// Link is one step of the derivation.
type Link struct {
	Stage Stage `json:"stage"`
	// Input names what this stage consumed (a source id, a claim key, a
	// hash) — not a prose description.
	Input string `json:"input"`
	// Output names what it produced.
	Output string `json:"output"`
	// Value is the numeric result where the stage produces one.
	Value float64 `json:"value"`
	// HasValue distinguishes "produced 0.0" from "produced nothing".
	HasValue bool `json:"has_value"`
	// Detail is the human-readable derivation, generated from the
	// artifact rather than written by hand.
	Detail string `json:"detail"`
	// ArtifactHash pins the engine artifact this link was read from.
	ArtifactHash string `json:"artifact_hash"`
}

// Section is one named explanation block (the audit's field list).
type Section struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Lines   []string `json:"lines"`
	Hash    string   `json:"hash"`
}

func newSection(name, summary string, lines []string) Section {
	s := Section{Name: name, Summary: summary, Lines: append([]string(nil), lines...)}
	var sb strings.Builder
	sb.WriteString("explanation_section/v1\n" + name + "\n" + summary + "\n")
	for _, l := range s.Lines {
		sb.WriteString(l + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	s.Hash = hex.EncodeToString(sum[:])
	return s
}

// Alternative is a rejected option and the reason it lost. An
// explanation that cannot say what it did NOT do is not an explanation.
type Alternative struct {
	Action     string  `json:"action"`
	Score      float64 `json:"score"`
	RejectedBy string  `json:"rejected_by"`
	Reason     string  `json:"reason"`
}

// DecisionExplanation is the canonical schema the audit specified.
type DecisionExplanation struct {
	DecisionID string  `json:"decision_id"`
	Subject    string  `json:"subject"`
	Intent     string  `json:"intent"`
	Decision   string  `json:"decision"`
	Confidence float64 `json:"confidence"`

	EvidenceExplanation      Section `json:"evidence_explanation"`
	IdentityExplanation      Section `json:"identity_explanation"`
	DependencyExplanation    Section `json:"dependency_explanation"`
	TruthExplanation         Section `json:"truth_explanation"`
	ContradictionExplanation Section `json:"contradiction_explanation"`
	FusionExplanation        Section `json:"fusion_explanation"`
	TemporalExplanation      Section `json:"temporal_explanation"`
	CausalExplanation        Section `json:"causal_explanation"`
	RiskExplanation          Section `json:"risk_explanation"`
	TrustExplanation         Section `json:"trust_explanation"`
	PolicyExplanation        Section `json:"policy_explanation"`
	SimulationExplanation    Section `json:"simulation_explanation"`

	AlternativesRejected []Alternative `json:"alternatives_rejected"`
	MissingEvidence      []string      `json:"missing_evidence"`
	ConflictingEvidence  []string      `json:"conflicting_evidence"`
	Assumptions          []string      `json:"assumptions"`
	Uncertainty          []string      `json:"uncertainty"`

	PolicyVersion  string   `json:"policy_version"`
	ModelVersions  []string `json:"model_versions"`
	SourceVersions []string `json:"source_versions"`

	EvidenceRootHash  string `json:"evidence_root_hash"`
	ExecutionRootHash string `json:"execution_root_hash"`
	DependencyRoot    string `json:"dependency_root_hash"`
	KnowledgeRoot     string `json:"knowledge_root_hash"`
	BindingHash       string `json:"binding_hash"`
	ReplayID          string `json:"replay_id"`

	Chain []Link `json:"chain"`
	Tick  uint64 `json:"tick"`

	// Hash is the content hash: everything Build knows before the
	// execution DAG's root exists. It is fixed the instant Build
	// returns and is never recomputed or mutated afterward — including
	// by Commit.
	Hash string `json:"hash"`

	// FinalCommitment is phase two: a domain-separated hash over (Hash,
	// ExecutionRootHash), produced exactly once by Commit after the DAG
	// root is known. Its existence is what makes ExecutionRootHash safe
	// to set post-Build without reopening Hash: the content commitment
	// and the root binding are two independent, individually verifiable
	// hashes rather than one hash computed too early and then edited.
	FinalCommitment string `json:"final_commitment_hash"`
}

// Input carries the raw artifacts Build consolidates. Everything here
// is produced by a real engine; this package computes nothing about the
// world, it only renders what the engines decided.
type Input struct {
	DecisionID string
	Subject    string
	Intent     string
	Action     string
	Confidence float64
	Tick       uint64

	// Per-stage lines produced by the engines themselves.
	EvidenceLines      []string
	IdentityLines      []string
	DependencyLines    []string
	TruthLines         []string
	ContradictionLines []string
	FusionLines        []string
	TemporalLines      []string
	CausalLines        []string
	RiskLines          []string
	TrustLines         []string
	PolicyLines        []string
	SimulationLines    []string

	Alternatives        []Alternative
	MissingEvidence     []string
	ConflictingEvidence []string
	Assumptions         []string
	Uncertainty         []string

	PolicyVersion  string
	ModelVersions  []string
	SourceVersions []string

	// EvidenceRootHash is known before the DAG runs and is part of the
	// content hash. ExecutionRootHash deliberately has no counterpart
	// here: it is the root over every DAG node including this
	// explanation's own node, so it cannot exist yet when Build runs.
	// It is bound in afterward by Commit, not accepted as an input.
	EvidenceRootHash string
	DependencyRoot   string
	KnowledgeRoot    string
	BindingHash      string
	ReplayID         string

	Chain []Link
}

// genericPhrases are rationales that carry no execution content. They
// are rejected rather than tolerated, because a system that emits them
// is indistinguishable from one that does not explain at all.
var genericPhrases = []string{
	"policy denied", "denied by policy", "not allowed", "see logs",
	"no reason given", "n/a", "unknown", "default",
}

// Build consolidates the artifacts into one explanation and validates
// it. It is the only constructor: there is deliberately no way to
// assemble a DecisionExplanation field by field and hash it, because
// that path would let a caller emit an unvalidated one.
func Build(in Input) (DecisionExplanation, error) {
	if in.DecisionID == "" || in.Subject == "" || in.Action == "" {
		return DecisionExplanation{}, fmt.Errorf("%w: decision id, subject and action", ErrIncomplete)
	}

	e := DecisionExplanation{
		DecisionID: in.DecisionID, Subject: in.Subject, Intent: in.Intent,
		Decision: in.Action, Confidence: in.Confidence, Tick: in.Tick,
		PolicyVersion: in.PolicyVersion,
		ModelVersions: sortedCopy(in.ModelVersions), SourceVersions: sortedCopy(in.SourceVersions),
		EvidenceRootHash: in.EvidenceRootHash,
		DependencyRoot:   in.DependencyRoot, KnowledgeRoot: in.KnowledgeRoot,
		BindingHash: in.BindingHash, ReplayID: in.ReplayID,
		AlternativesRejected: append([]Alternative(nil), in.Alternatives...),
		MissingEvidence:      sortedCopy(in.MissingEvidence),
		ConflictingEvidence:  sortedCopy(in.ConflictingEvidence),
		Assumptions:          sortedCopy(in.Assumptions),
		Uncertainty:          sortedCopy(in.Uncertainty),
		Chain:                append([]Link(nil), in.Chain...),
	}
	sort.SliceStable(e.AlternativesRejected, func(i, j int) bool {
		return e.AlternativesRejected[i].Action < e.AlternativesRejected[j].Action
	})

	e.EvidenceExplanation = newSection("evidence", summarise(in.EvidenceLines), in.EvidenceLines)
	e.IdentityExplanation = newSection("identity", summarise(in.IdentityLines), in.IdentityLines)
	e.DependencyExplanation = newSection("dependency", summarise(in.DependencyLines), in.DependencyLines)
	e.TruthExplanation = newSection("truth", summarise(in.TruthLines), in.TruthLines)
	e.ContradictionExplanation = newSection("contradiction", summarise(in.ContradictionLines), in.ContradictionLines)
	e.FusionExplanation = newSection("fusion", summarise(in.FusionLines), in.FusionLines)
	e.TemporalExplanation = newSection("temporal", summarise(in.TemporalLines), in.TemporalLines)
	e.CausalExplanation = newSection("causal", summarise(in.CausalLines), in.CausalLines)
	e.RiskExplanation = newSection("risk", summarise(in.RiskLines), in.RiskLines)
	e.TrustExplanation = newSection("trust", summarise(in.TrustLines), in.TrustLines)
	e.PolicyExplanation = newSection("policy", summarise(in.PolicyLines), in.PolicyLines)
	e.SimulationExplanation = newSection("simulation", summarise(in.SimulationLines), in.SimulationLines)

	if err := e.validate(); err != nil {
		return DecisionExplanation{}, err
	}
	e.Hash = e.computeHash()
	return e, nil
}

func (e DecisionExplanation) validate() error {
	// Mandatory sections. Identity, temporal and simulation are
	// legitimately optional; the rest are not.
	mandatory := map[string]Section{
		"evidence":   e.EvidenceExplanation,
		"dependency": e.DependencyExplanation,
		"truth":      e.TruthExplanation,
		"fusion":     e.FusionExplanation,
		"risk":       e.RiskExplanation,
		"policy":     e.PolicyExplanation,
	}
	var missing []string
	for name, s := range mandatory {
		if len(s.Lines) == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: %s", ErrIncomplete, strings.Join(missing, ", "))
	}
	if e.EvidenceRootHash == "" {
		return fmt.Errorf("%w: evidence root hash", ErrIncomplete)
	}
	// A decision with no rejected alternative means nothing was
	// considered; that is a lookup, not a decision.
	if len(e.AlternativesRejected) == 0 {
		return fmt.Errorf("%w: alternatives_rejected", ErrIncomplete)
	}
	// Generic-rationale check across every rendered line.
	for _, s := range []Section{e.PolicyExplanation, e.RiskExplanation, e.DecisionSectionProxy()} {
		if isGeneric(s) {
			return fmt.Errorf("%w: section %q says only %q", ErrTemplated, s.Name, s.Summary)
		}
	}
	// Chain coverage.
	present := map[Stage]bool{}
	for _, l := range e.Chain {
		present[l.Stage] = true
	}
	var gaps []string
	for _, s := range requiredChain {
		if !present[s] {
			gaps = append(gaps, string(s))
		}
	}
	if len(gaps) > 0 {
		return fmt.Errorf("%w: missing stages %s", ErrChainBroken, strings.Join(gaps, " -> "))
	}
	// Chain ordering: the required stages must appear in canonical
	// order. Out-of-order links mean the derivation being described is
	// not the derivation that ran.
	last := -1
	for _, l := range e.Chain {
		idx := requiredIndex(l.Stage)
		if idx < 0 {
			continue
		}
		if idx < last {
			return fmt.Errorf("%w: stage %s appears after a later stage", ErrChainBroken, l.Stage)
		}
		last = idx
	}
	return nil
}

// DecisionSectionProxy renders the decision itself as a Section so the
// generic-rationale check covers it too.
func (e DecisionExplanation) DecisionSectionProxy() Section {
	return newSection("decision", e.Decision+" at confidence "+fnum(e.Confidence),
		[]string{"decision " + e.Decision + " for " + e.Subject})
}

func requiredIndex(s Stage) int {
	for i, r := range requiredChain {
		if r == s {
			return i
		}
	}
	return -1
}

func isGeneric(s Section) bool {
	if len(s.Lines) == 0 {
		return false
	}
	joined := strings.ToLower(strings.Join(append(s.Lines, s.Summary), " "))
	// Generic only if EVERY line is short and matches a generic phrase:
	// a rich explanation that happens to contain the words "policy
	// denied" alongside real content is fine.
	for _, l := range s.Lines {
		low := strings.ToLower(strings.TrimSpace(l))
		generic := false
		for _, g := range genericPhrases {
			if low == g || (len(low) < 24 && strings.Contains(low, g)) {
				generic = true
				break
			}
		}
		if !generic {
			return false
		}
	}
	_ = joined
	return true
}

func summarise(lines []string) string {
	if len(lines) == 0 {
		return "not applicable to this execution"
	}
	return lines[0]
}

// Trace renders the source→decision walk as ordered prose. This is what
// a reviewer reads; the structured form is what a machine checks.
func (e DecisionExplanation) Trace() []string {
	out := make([]string, 0, len(e.Chain))
	for i, l := range e.Chain {
		line := strconv.Itoa(i+1) + ". " + string(l.Stage) + ": " + l.Input + " -> " + l.Output
		if l.HasValue {
			line += " (" + fnum(l.Value) + ")"
		}
		if l.Detail != "" {
			line += " — " + l.Detail
		}
		out = append(out, line)
	}
	return out
}

// Why returns the shortest honest answer: the decision, the single
// highest-value risk driver, and the policy rule that fired.
func (e DecisionExplanation) Why() string {
	driver := ""
	for _, l := range e.Chain {
		if l.Stage == StageRisk && l.Detail != "" {
			driver = l.Detail
		}
	}
	rule := e.PolicyExplanation.Summary
	return e.Decision + " for " + e.Subject + " — " + driver + "; policy: " + rule
}

func (e DecisionExplanation) computeHash() string {
	var sb strings.Builder
	sb.WriteString("decision_explanation/v1\n")
	sb.WriteString("id=" + e.DecisionID + "\nsubject=" + e.Subject + "\nintent=" + e.Intent + "\n")
	sb.WriteString("decision=" + e.Decision + "\nconfidence=" + fnum(e.Confidence) + "\n")
	sb.WriteString("tick=" + strconv.FormatUint(e.Tick, 10) + "\n")
	for _, s := range []Section{
		e.EvidenceExplanation, e.IdentityExplanation, e.DependencyExplanation,
		e.TruthExplanation, e.ContradictionExplanation, e.FusionExplanation,
		e.TemporalExplanation, e.CausalExplanation, e.RiskExplanation,
		e.TrustExplanation, e.PolicyExplanation, e.SimulationExplanation,
	} {
		sb.WriteString("section:" + s.Name + "=" + s.Hash + "\n")
	}
	for _, a := range e.AlternativesRejected {
		sb.WriteString("alt=" + a.Action + "|" + fnum(a.Score) + "|" + a.RejectedBy + "|" + a.Reason + "\n")
	}
	writeAll := func(k string, v []string) {
		for _, x := range v {
			sb.WriteString(k + "=" + x + "\n")
		}
	}
	writeAll("missing", e.MissingEvidence)
	writeAll("conflicting", e.ConflictingEvidence)
	writeAll("assumption", e.Assumptions)
	writeAll("uncertainty", e.Uncertainty)
	writeAll("model", e.ModelVersions)
	writeAll("source", e.SourceVersions)
	sb.WriteString("policy_version=" + e.PolicyVersion + "\n")
	// execution_root is deliberately excluded from the content hash: it
	// does not exist until the DAG this explanation is a node of has
	// finished, which is after Build returns. Binding it in here would
	// force either a placeholder (the v7.12.0 defect: Hash computed over
	// a value later overwritten, so the shipped artifact never verifies
	// against its own Hash) or a circular dependency between the DAG
	// root and one of its own node hashes. Commit binds it separately.
	sb.WriteString("evidence_root=" + e.EvidenceRootHash + "\n")
	sb.WriteString("dependency_root=" + e.DependencyRoot + "\nknowledge_root=" + e.KnowledgeRoot + "\n")
	sb.WriteString("binding=" + e.BindingHash + "\nreplay=" + e.ReplayID + "\n")
	for _, l := range e.Chain {
		sb.WriteString("link=" + string(l.Stage) + "|" + l.Input + "|" + l.Output + "|" +
			fnum(l.Value) + "|" + strconv.FormatBool(l.HasValue) + "|" + l.Detail + "|" +
			l.ArtifactHash + "\n")
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// computeCommitment binds the fixed content hash to the execution root
// hash. Domain-separated from computeHash so the two can never collide
// or be confused for one another.
func (e DecisionExplanation) computeCommitment() string {
	h := sha256.New()
	fmt.Fprintf(h, "decision_explanation.final_commitment/v1|content=%s|execution_root=%s",
		e.Hash, e.ExecutionRootHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Commit is phase two of the explanation's commitment protocol. Build
// fixes Hash over everything knowable before the execution DAG exists;
// once the DAG has run and its root hash is known, Commit binds that
// root to the explanation by setting ExecutionRootHash and computing
// FinalCommitment over (Hash, ExecutionRootHash) — without touching
// Hash itself. A caller can therefore never reach the v7.12.0 defect
// (a shipped explanation whose Hash was computed before a field it
// covers was overwritten): ExecutionRootHash is set exactly once, here,
// and every subsequent Verify recomputes FinalCommitment fresh from the
// two hashes it binds.
func Commit(e DecisionExplanation, executionRootHash string) (DecisionExplanation, error) {
	if e.Hash == "" {
		return DecisionExplanation{}, fmt.Errorf("%w: cannot commit an unbuilt explanation", ErrIncomplete)
	}
	if e.computeHash() != e.Hash {
		return DecisionExplanation{}, errors.New("explanation: content hash mismatch — refusing to commit a tampered explanation")
	}
	if e.FinalCommitment != "" {
		return DecisionExplanation{}, ErrAlreadyCommitted
	}
	if executionRootHash == "" {
		return DecisionExplanation{}, fmt.Errorf("%w: execution root hash", ErrIncomplete)
	}
	e.ExecutionRootHash = executionRootHash
	e.FinalCommitment = e.computeCommitment()
	return e, nil
}

// Verify recomputes the content hash from the explanation's own fields
// and, if the explanation claims to be committed, independently
// recomputes and checks the final commitment too. It accepts both an
// uncommitted explanation (fresh from Build, ExecutionRootHash and
// FinalCommitment both empty) and a fully committed one; it rejects
// anything in between, which can only arise from post-hoc field
// mutation rather than the Commit function.
func Verify(e DecisionExplanation) error {
	if e.computeHash() != e.Hash {
		return errors.New("explanation: content hash mismatch — the explanation has been edited")
	}
	switch {
	case e.ExecutionRootHash == "" && e.FinalCommitment == "":
		return nil // built, not yet committed — a valid intermediate state
	case e.ExecutionRootHash == "" || e.FinalCommitment == "":
		return errors.New("explanation: partially committed — execution root and final commitment must both be set or both be empty")
	case e.computeCommitment() != e.FinalCommitment:
		return errors.New("explanation: final commitment mismatch — the execution root binding has been edited")
	default:
		return nil
	}
}

// VerifyFinal additionally requires that the explanation is fully
// committed. This is the check production callers and replay must use
// on "the exact artifact emitted by production execution": an
// explanation that never received its execution-root commitment is
// incomplete, not just unverifiable.
func VerifyFinal(e DecisionExplanation) error {
	if err := Verify(e); err != nil {
		return err
	}
	if e.FinalCommitment == "" {
		return ErrNotCommitted
	}
	return nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func fnum(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }
