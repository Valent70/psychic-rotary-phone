package explanation

import (
	"errors"
	"testing"
)

func goodChain() []Link {
	return []Link{
		{Stage: StageSource, Input: "ais-vendor-a", Output: "submission", Detail: "declared reliability 0.8", ArtifactHash: "h1"},
		{Stage: StageEvidence, Input: "submission", Output: "ev-9f2", Detail: "AIS position report", ArtifactHash: "h2"},
		{Stage: StageDependency, Input: "ev-9f2", Output: "shared_fraction", Value: 0.5, HasValue: true, Detail: "shares satellite provider X with ais-vendor-b", ArtifactHash: "h3"},
		{Stage: StageWeight, Input: "0.8", Output: "effective_weight", Value: 0.4, HasValue: true, Detail: "discounted by shared ancestry", ArtifactHash: "h4"},
		{Stage: StageTruth, Input: "claim:location", Output: "winner=port-of-singapore", Value: 0.71, HasValue: true, Detail: "plurality over 3 sources", ArtifactHash: "h5"},
		{Stage: StageContradiction, Input: "claim:location", Output: "contradiction_score", Value: 0.22, HasValue: true, Detail: "runner-up within 0.22", ArtifactHash: "h6"},
		{Stage: StageFusion, Input: "3 submissions", Output: "posterior", Value: 0.68, HasValue: true, Detail: "2 independent families", ArtifactHash: "h7"},
		{Stage: StageCausal, Input: "posterior", Output: "causal_support", Value: 0.31, HasValue: true, Detail: "AIS gap -> dark activity, strength 0.6", ArtifactHash: "h8"},
		{Stage: StageRisk, Input: "causal_support", Output: "tbml_composite_risk_score", Value: 0.82, HasValue: true, Detail: "pattern 0.7 and price anomaly 0.9 dominate", ArtifactHash: "h9"},
		{Stage: StagePolicy, Input: "0.82", Output: "FLAG", Detail: "escalate_threshold 0.9 not reached; flag_threshold 0.6 exceeded", ArtifactHash: "h10"},
		{Stage: StageDecision, Input: "FLAG", Output: "decision", Detail: "action FLAG recorded at ledger index 41", ArtifactHash: "h11"},
	}
}

func goodInput() Input {
	return Input{
		DecisionID: "d1", Subject: "IMO9999999", Intent: "sanctions-screen", Action: "FLAG",
		Confidence: 0.68, Tick: 42,
		EvidenceLines:      []string{"3 submissions from 2 independent families"},
		IdentityLines:      []string{"IMO9999999 resolved from MMSI 123456789 with discriminating power 1.00"},
		DependencyLines:    []string{"ais-vendor-a and ais-vendor-b share satellite provider X; weight discounted 0.8 -> 0.4"},
		TruthLines:         []string{"winner port-of-singapore at 0.71 over runner-up 0.49"},
		ContradictionLines: []string{"contradiction score 0.22"},
		FusionLines:        []string{"posterior 0.68 over 2 independent families"},
		CausalLines:        []string{"AIS gap causes dark activity with strength 0.6"},
		RiskLines:          []string{"composite risk 0.82 driven by price anomaly 0.9"},
		TrustLines:         []string{"entity trust DEGRADED at 0.41, decayed from 0.62"},
		PolicyLines:        []string{"flag_threshold 0.6 exceeded by 0.82; escalate_threshold 0.9 not reached"},
		Alternatives: []Alternative{
			{Action: "MONITOR", Score: 0.6, RejectedBy: "risk_policy", Reason: "risk 0.82 above flag threshold"},
			{Action: "ESCALATE", Score: 0.9, RejectedBy: "risk_policy", Reason: "risk 0.82 below escalate threshold"},
		},
		MissingEvidence:  []string{"SAR coverage for 2026-08-09T00:00Z..06:00Z"},
		Assumptions:      []string{"vendor-declared reliability is accurate to +/- 0.1"},
		Uncertainty:      []string{"posterior interval 0.55..0.79"},
		PolicyVersion:    "sanctions-screen@3",
		ModelVersions:    []string{"risk@2", "hbayes@1"},
		SourceVersions:   []string{"ais@1", "sar@2"},
		EvidenceRootHash: "ev-root",
		DependencyRoot:   "dep-root",
		KnowledgeRoot:    "k-root",
		BindingHash:      "bind-1",
		ReplayID:         "replay-1",
		Chain:            goodChain(),
	}
}

func TestBuildProducesAWalkFromSourceToDecision(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	trace := e.Trace()
	if len(trace) != len(goodChain()) {
		t.Fatalf("trace must cover every link, got %d", len(trace))
	}
	if e.Why() == "" {
		t.Fatal("Why must produce a one-line answer")
	}
	if err := Verify(e); err != nil {
		t.Fatal(err)
	}
}

func TestMissingMandatorySectionIsRefused(t *testing.T) {
	in := goodInput()
	in.DependencyLines = nil
	if _, err := Build(in); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("a decision with no dependency explanation must be refused, got %v", err)
	}
}

func TestExplanationWithNoRejectedAlternativeIsRefused(t *testing.T) {
	in := goodInput()
	in.Alternatives = nil
	if _, err := Build(in); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("a decision that considered nothing is a lookup, got %v", err)
	}
}

func TestGenericPolicyRationaleIsRefused(t *testing.T) {
	in := goodInput()
	in.PolicyLines = []string{"policy denied"}
	if _, err := Build(in); !errors.Is(err, ErrTemplated) {
		t.Fatalf("the audit's named failure mode must be refused, got %v", err)
	}
}

func TestRichPolicyLineContainingGenericWordsIsAccepted(t *testing.T) {
	in := goodInput()
	in.PolicyLines = []string{"policy denied because factor tbml_composite_risk_score=0.82 exceeded flag_threshold=0.6 under sanctions-screen@3"}
	if _, err := Build(in); err != nil {
		t.Fatalf("a substantive line must not be misclassified as generic: %v", err)
	}
}

func TestBrokenChainIsRefused(t *testing.T) {
	in := goodInput()
	var pruned []Link
	for _, l := range in.Chain {
		if l.Stage == StageWeight {
			continue
		}
		pruned = append(pruned, l)
	}
	in.Chain = pruned
	if _, err := Build(in); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("a chain missing the weight stage must be refused, got %v", err)
	}
}

func TestOutOfOrderChainIsRefused(t *testing.T) {
	in := goodInput()
	c := in.Chain
	// Move the risk link before the truth link.
	c[4], c[8] = c[8], c[4]
	in.Chain = c
	if _, err := Build(in); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("an out-of-order derivation must be refused, got %v", err)
	}
}

func TestModifyingOneStageChangesTheExplanationAndItsHash(t *testing.T) {
	base, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	for i := range goodChain() {
		in := goodInput()
		in.Chain[i].Value += 0.01
		in.Chain[i].HasValue = true
		mod, err := Build(in)
		if err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
		if mod.Hash == base.Hash {
			t.Fatalf("modifying chain link %d (%s) must change the explanation hash",
				i, in.Chain[i].Stage)
		}
	}
}

func TestModifyingASectionChangesTheHash(t *testing.T) {
	base, _ := Build(goodInput())
	in := goodInput()
	in.RiskLines = []string{"composite risk 0.83 driven by price anomaly 0.9"}
	mod, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Hash == base.Hash || mod.RiskExplanation.Hash == base.RiskExplanation.Hash {
		t.Fatal("a changed risk explanation must change both the section and the root hash")
	}
}

func TestModelAndSourceVersionsAreInsideTheHash(t *testing.T) {
	base, _ := Build(goodInput())
	in := goodInput()
	in.ModelVersions = []string{"risk@3", "hbayes@1"}
	mod, _ := Build(in)
	if mod.Hash == base.Hash {
		t.Fatal("a different model version must change the explanation hash")
	}
	in2 := goodInput()
	in2.SourceVersions = []string{"ais@2", "sar@2"}
	mod2, _ := Build(in2)
	if mod2.Hash == base.Hash {
		t.Fatal("a different source version must change the explanation hash")
	}
}

func TestHashIsOrderIndependentForSets(t *testing.T) {
	a, _ := Build(goodInput())
	in := goodInput()
	in.ModelVersions = []string{"hbayes@1", "risk@2"}
	in.Alternatives = []Alternative{in.Alternatives[1], in.Alternatives[0]}
	b, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash != b.Hash {
		t.Fatal("set ordering must not change the explanation hash")
	}
}

func TestVerifyDetectsPostHocEditing(t *testing.T) {
	e, _ := Build(goodInput())
	e.Decision = "ALLOW"
	if err := Verify(e); err == nil {
		t.Fatal("editing the decision after the fact must fail verification")
	}
}

func TestMissingEvidenceRootHashRefused(t *testing.T) {
	in := goodInput()
	in.EvidenceRootHash = ""
	if _, err := Build(in); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("an explanation with no evidence root cannot be replayed, got %v", err)
	}
}

// --- Two-phase commitment (P0-01): Build fixes Hash before the
// execution DAG's root exists; Commit binds that root afterward without
// ever touching Hash. These tests are the audit's named regression
// suite for the defect where ExecutionRootHash was overwritten after
// Hash had already been computed over it, so the shipped artifact never
// verified against its own Hash.

func TestFinalExplanationVerifies(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Commit(e, "exec-root-abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFinal(committed); err != nil {
		t.Fatalf("a freshly committed explanation must pass final verification: %v", err)
	}
}

func TestUncommittedExplanationFailsFinalVerificationButNotVerify(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(e); err != nil {
		t.Fatalf("an uncommitted-but-untampered explanation must still pass content verification: %v", err)
	}
	if err := VerifyFinal(e); !errors.Is(err, ErrNotCommitted) {
		t.Fatalf("an uncommitted explanation must fail final verification, got %v", err)
	}
}

func TestNoPostHashMutation(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	preCommitHash := e.Hash
	committed, err := Commit(e, "exec-root-abc")
	if err != nil {
		t.Fatal(err)
	}
	if committed.Hash != preCommitHash {
		t.Fatal("Commit must never change Hash — only Build may set it")
	}
}

func TestCommitmentCycleImpossible(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Commit(e, "exec-root-abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(committed, "exec-root-xyz"); !errors.Is(err, ErrAlreadyCommitted) {
		t.Fatalf("committing twice must be refused, got %v", err)
	}
}

func TestExecutionRootTamperFails(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Commit(e, "exec-root-abc")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate exactly the v7.12.0 defect: overwriting ExecutionRootHash
	// after the explanation was already finalized, without recomputing
	// FinalCommitment.
	committed.ExecutionRootHash = "exec-root-attacker-substituted"
	if err := VerifyFinal(committed); err == nil {
		t.Fatal("mutating ExecutionRootHash after Commit must be detected")
	}
}

func TestExplanationContentTamperFails(t *testing.T) {
	e, err := Build(goodInput())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Commit(e, "exec-root-abc")
	if err != nil {
		t.Fatal(err)
	}
	committed.Decision = "ALLOW"
	if err := VerifyFinal(committed); err == nil {
		t.Fatal("mutating decided content after Commit must be detected")
	}
}

func TestOptionalSectionsMayBeEmpty(t *testing.T) {
	in := goodInput()
	in.TemporalLines = nil
	in.SimulationLines = nil
	in.IdentityLines = nil
	e, err := Build(in)
	if err != nil {
		t.Fatalf("optional sections must be allowed to be empty: %v", err)
	}
	if e.TemporalExplanation.Summary == "" {
		t.Fatal("an empty section must still say so explicitly")
	}
}

func TestDecisionIsNotSelfExplainingWithoutSubject(t *testing.T) {
	in := goodInput()
	in.Subject = ""
	if _, err := Build(in); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected incomplete error, got %v", err)
	}
}
