// Package replayacceptance closes V7.11.1 P0-4.
//
// The audit's complaint was specific: an existing replay test proved
// ProcessID stability, which is the one field that would stay identical
// even if the reasoning changed completely. The acceptance criterion it
// asked for instead is 100 iterations checking, every time:
//
//	ReplayResultHash
//	Decision
//	Explanation
//	Certificate
//
// This file asserts all four across 100 runs, and then asserts the
// contrapositive — that perturbing any single input makes them differ —
// because a determinism test that cannot fail is measuring nothing.
package replayacceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/fusion"
	"veriqo/pkg/moat/intelligence/risk"
	"veriqo/pkg/replay"
)

const iterations = 100

func caseInput() canonical.CaseInput {
	return canonical.CaseInput{
		Entity: digitaltwin.EntityID("IMO9999999"), Subject: "IMO9999999", Predicate: "port_call",
		Submissions: []canonical.SourceSubmission{
			{SourceID: fusion.SourceID("ais-vendor-a"), Value: "singapore",
				BaseReliability: 0.8, Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("ais-vendor-b"), Value: "singapore",
				BaseReliability: 0.8, Provider: "sat-x", UpstreamID: "feed-sat-x"},
			{SourceID: fusion.SourceID("port-authority"), Value: "singapore",
				BaseReliability: 0.9, Provider: "port", UpstreamID: "feed-port"},
			{SourceID: fusion.SourceID("broker-report"), Value: "jakarta",
				BaseReliability: 0.5, Provider: "broker",
				Dependencies: []evidencegraph.DependencyRecord{
					{UpstreamID: evidencegraph.NodeID("model-detector-v3"),
						Kind: evidencegraph.DependsOnTransformation, Confidence: 0.6}}},
		},
		Policy: risk.DefaultPolicy(), PatternScore: 0.7, PriceAnomaly: 0.9,
		EconomicChannels: map[string]float64{"freight": 1_000_000, "insurance": 250_000},
		Tick:             42,
	}
}

func execContext() execution.Context {
	return execution.Context{ExecutionID: "acc-1", CaseID: "case-1", Tenant: "acme",
		Actor: "acceptance", PolicyVersion: "sanctions-screen@3", EvidencePackageID: "evp-1",
		IdentityResolutionVersion: "ident@2", Tick: 42, ReplayMetadata: "acceptance"}
}

type fingerprint struct {
	ExecutionRoot    string `json:"execution_root_hash"`
	ReplayResultHash string `json:"replay_result_hash"`
	OriginalHash     string `json:"original_result_hash"`
	Decision         string `json:"decision"`
	ExplanationHash  string `json:"explanation_hash"`
	CertificateID    string `json:"certificate_id"`
	CertificateMatch bool   `json:"certificate_match"`
	DependencyRoot   string `json:"dependency_root_hash"`
}

func fingerprintOf(t *testing.T) fingerprint {
	t.Helper()
	e := execution.NewEngine(nil)
	res, err := e.Run(context.Background(), execution.Input{Context: execContext(), Case: caseInput()})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	return fingerprint{
		ExecutionRoot:    res.ExecutionRootHash,
		ReplayResultHash: res.Certificate.ReplayResultHash,
		OriginalHash:     res.Certificate.OriginalResultHash,
		Decision:         res.Decision,
		ExplanationHash:  res.Explanation.Hash,
		CertificateID:    res.Certificate.VerificationCertificateID,
		CertificateMatch: res.Certificate.Match,
		DependencyRoot:   res.Explanation.DependencyRoot,
	}
}

// Report is the evidence artifact for this acceptance gate.
type Report struct {
	Iterations            int         `json:"iterations"`
	Stable                bool        `json:"stable"`
	Fields                []string    `json:"fields_compared"`
	Reference             fingerprint `json:"reference"`
	FirstDivergence       int         `json:"first_divergence_iteration"`
	DivergentField        string      `json:"divergent_field,omitempty"`
	PerturbationsRun      int         `json:"perturbations_run"`
	PerturbationsDetected int         `json:"perturbations_detected"`
}

func writeEvidence(t *testing.T, r Report) {
	t.Helper()
	dir := filepath.Join("..", "..", "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if b, err := json.MarshalIndent(r, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "replay-determinism-100x.json"), append(b, '\n'), 0o644)
	}
}

func TestOneHundredReplaysAgreeOnEveryMaterialField(t *testing.T) {
	ref := fingerprintOf(t)
	rep := Report{Iterations: iterations, Reference: ref, FirstDivergence: -1,
		Fields: []string{"ReplayResultHash", "Decision", "Explanation", "Certificate",
			"ExecutionRootHash", "DependencyRoot"}}

	if !ref.CertificateMatch {
		t.Fatal("the reference run's own certificate must report a match")
	}
	if ref.ReplayResultHash == "" || ref.ExplanationHash == "" || ref.Decision == "" {
		t.Fatalf("the reference fingerprint is incomplete: %+v", ref)
	}

	for i := 1; i < iterations; i++ {
		got := fingerprintOf(t)
		switch {
		case got.ReplayResultHash != ref.ReplayResultHash:
			rep.DivergentField = "ReplayResultHash"
		case got.OriginalHash != ref.OriginalHash:
			rep.DivergentField = "OriginalResultHash"
		case got.Decision != ref.Decision:
			rep.DivergentField = "Decision"
		case got.ExplanationHash != ref.ExplanationHash:
			rep.DivergentField = "Explanation"
		case got.CertificateID != ref.CertificateID:
			rep.DivergentField = "Certificate"
		case !got.CertificateMatch:
			rep.DivergentField = "CertificateMatch"
		case got.ExecutionRoot != ref.ExecutionRoot:
			rep.DivergentField = "ExecutionRootHash"
		case got.DependencyRoot != ref.DependencyRoot:
			rep.DivergentField = "DependencyRoot"
		}
		if rep.DivergentField != "" {
			rep.FirstDivergence = i
			writeEvidence(t, rep)
			t.Fatalf("iteration %d diverged on %s:\n  want %+v\n  got  %+v",
				i, rep.DivergentField, ref, got)
		}
	}
	rep.Stable = true

	// Contrapositive: perturbing any single input must be detected. A
	// determinism gate that passes on a changed input is not a gate.
	perturbations := []struct {
		name  string
		apply func(*canonical.CaseInput, *execution.Context)
	}{
		{"evidence value", func(c *canonical.CaseInput, _ *execution.Context) {
			c.Submissions[0].Value = "jakarta"
		}},
		{"source reliability", func(c *canonical.CaseInput, _ *execution.Context) {
			c.Submissions[2].BaseReliability = 0.4
		}},
		{"shared provider", func(c *canonical.CaseInput, _ *execution.Context) {
			c.Submissions[1].Provider = "sat-y"
			c.Submissions[1].UpstreamID = "feed-sat-y"
		}},
		{"pattern score", func(c *canonical.CaseInput, _ *execution.Context) {
			c.PatternScore = 0.1
		}},
		{"policy version", func(_ *canonical.CaseInput, x *execution.Context) {
			x.PolicyVersion = "sanctions-screen@4"
		}},
		{"tenant", func(_ *canonical.CaseInput, x *execution.Context) { x.Tenant = "other" }},
	}
	for _, p := range perturbations {
		rep.PerturbationsRun++
		in := caseInput()
		ctx := execContext()
		p.apply(&in, &ctx)
		e := execution.NewEngine(nil)
		res, err := e.Run(context.Background(), execution.Input{Context: ctx, Case: in})
		if err != nil {
			t.Fatalf("perturbation %q failed to execute: %v", p.name, err)
		}
		changed := res.ExecutionRootHash != ref.ExecutionRoot ||
			res.Explanation.Hash != ref.ExplanationHash ||
			res.Certificate.ReplayResultHash != ref.ReplayResultHash
		if !changed {
			writeEvidence(t, rep)
			t.Fatalf("perturbing %q changed nothing; the fingerprint is not sensitive", p.name)
		}
		rep.PerturbationsDetected++
	}
	writeEvidence(t, rep)
	t.Logf("%d/%d replays stable across %d fields; %d/%d perturbations detected",
		iterations, iterations, len(rep.Fields), rep.PerturbationsDetected, rep.PerturbationsRun)
}

// TestDAGReplayMatchesOneHundredTimes exercises the serialized replay
// path — bytes in, verdict out — rather than re-running the engine and
// comparing, which is a weaker claim.
func TestDAGReplayMatchesOneHundredTimes(t *testing.T) {
	e := execution.NewEngine(nil)
	res, err := e.Run(context.Background(), execution.Input{Context: execContext(), Case: caseInput()})
	if err != nil {
		t.Fatal(err)
	}
	req := execution.ReplayRequest{Context: execContext(), Case: caseInput(),
		Committed: res.Trace}
	data, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < iterations; i++ {
		v, err := execution.ReplayDAG(context.Background(), data, execution.NewEngine(nil))
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if err := v.Assert(); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if v.NodesCompared == 0 {
			t.Fatalf("iteration %d compared no nodes", i)
		}
	}
}

// TestCertificateVerificationIsIndependentlyCheckable confirms the
// certificate can be validated on its own, without the engine that
// produced it.
func TestCertificateVerificationIsIndependentlyCheckable(t *testing.T) {
	e := execution.NewEngine(nil)
	res, err := e.Run(context.Background(), execution.Input{Context: execContext(), Case: caseInput()})
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.VerifyCertificate(res.Certificate); err != nil {
		t.Fatalf("the certificate must verify standalone: %v", err)
	}
	if err := res.Certificate.Assert(); err != nil {
		t.Fatalf("a matching certificate must assert cleanly: %v", err)
	}
	tampered := res.Certificate
	tampered.ReplayResultHash = "deadbeef"
	if err := replay.VerifyCertificate(tampered); err == nil {
		t.Fatal("an edited certificate must fail verification")
	}
}
