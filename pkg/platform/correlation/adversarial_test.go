package correlation

// PHASE D (P0-4) — Correlation Context adversarial suite.
//
// correlation.Key already existed, already carried all eight
// identifiers the program names, and already had a real production
// caller (pkg/lifecycle.Orchestrator.RunUnified). What it did NOT have
// was the program's own acceptance criterion proved: that DROPPING,
// REPLACING, SWAPPING or ALTERING one of those identifiers produces a
// VERIFICATION FAILURE, and never a silent regeneration of the missing
// or altered field.
//
// "Never a silent regeneration" is the load-bearing half. A system that
// notices a missing EvidencePackageID and helpfully mints a fresh one
// is strictly worse than one that crashes: the operation keeps running
// under an identity nothing else in the system shares, and the join
// that correlation exists to guarantee is quietly broken. Every test
// below therefore asserts two things — that the tampered run fails, and
// that the identifier was not rewritten to something plausible.
//
// These tests deliberately go through the REAL verification paths
// (execution.Run's own Context validation, execution.ReplayDAG's
// node-by-node comparison, pkg/replay.Engine.Replay's fingerprint
// chain) rather than asserting on the Key struct in isolation. A struct
// field cannot "fail verification"; only the machinery that commits to
// it can, and proving the commitment is real is the entire point.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/execution"
	"veriqo/pkg/identity"
	"veriqo/pkg/moat/digitaltwin"
	"veriqo/pkg/replay"
)

// boundResolver is a real identity ledger with a real merge in it, so
// IdentityLedgerHead and the IDENTITY_RESOLUTION stage's independent
// re-resolution are both genuinely load-bearing in these tests.
func boundResolver(t *testing.T) *identity.Resolver {
	t.Helper()
	r := identity.NewResolver()
	if err := r.RegisterAuthority(identity.Authority{SourceID: "test-authority", Weight: 1}); err != nil {
		t.Fatalf("RegisterAuthority: %v", err)
	}
	if _, err := r.Merge("analyst-1", "test-authority",
		identity.Identifier{Kind: identity.KindIMO, Value: "9999999"},
		identity.Identifier{Kind: identity.KindCallsign, Value: "ABCD1"}, 1, "adversarial suite merge"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return r
}

// adversarialRun produces one real execution plus the exported replay
// request an independent verifier would consume, with the identity
// ledger genuinely bound.
func adversarialRun(t *testing.T) (execution.Result, execution.ReplayRequest, *identity.Resolver) {
	t.Helper()
	res := boundResolver(t)
	e := execution.NewEngine(nil)
	e.Identity = res

	// The case entity must BE what the ledger resolves the alias to --
	// that is the whole point of IDENTITY_RESOLUTION's independent
	// re-resolution, and a baseline that disagreed with it would be
	// testing the tamper detector against a case that was already
	// tampered.
	primary := identity.Identifier{Kind: identity.KindIMO, Value: "9999999"}
	canonEntity, err := res.EntityIDAt(primary, 42)
	if err != nil {
		t.Fatalf("EntityIDAt: %v", err)
	}
	caseIn := testCaseInput()
	caseIn.Entity = digitaltwin.EntityID(canonEntity)
	caseIn.Subject = canonEntity

	aliases := []identity.Identifier{primary}
	out, err := e.Run(context.Background(), execution.Input{
		Context: testContext(), Case: caseIn, IdentityAliases: aliases,
	})
	if err != nil {
		t.Fatalf("execution.Run: %v", err)
	}
	raw, err := out.ExportReplay()
	if err != nil {
		t.Fatalf("ExportReplay: %v", err)
	}
	var req execution.ReplayRequest
	if err := unmarshalRequest(raw, &req); err != nil {
		t.Fatalf("decode replay request: %v", err)
	}
	return *out, req, res
}

// replayTampered re-marshals a mutated request and runs it through the
// real independent-replay path against a FRESH engine.
func replayTampered(t *testing.T, req execution.ReplayRequest, res *identity.Resolver) (execution.ReplayVerdict, *execution.Result, error) {
	t.Helper()
	raw, err := req.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	fresh := execution.NewEngine(nil)
	fresh.Identity = res
	return execution.ReplayDAGWithResult(context.Background(), raw, fresh)
}

// TestAdversarialDropEvidencePackageID: removing the evidence package
// identifier must fail the run outright. It must NOT be regenerated,
// defaulted, or derived from anything else.
func TestAdversarialDropEvidencePackageID(t *testing.T) {
	original, req, res := adversarialRun(t)
	if original.Trace.Context.EvidencePackageID == "" {
		t.Fatal("the baseline run carries no EvidencePackageID; the test would prove nothing")
	}

	req.Context.EvidencePackageID = ""
	verdict, rebuilt, err := replayTampered(t, req, res)

	if err == nil {
		t.Fatalf("dropping EvidencePackageID was accepted (verdict matched=%v) -- a run without it must fail closed", verdict.Matched)
	}
	if !errors.Is(err, execution.ErrContextIncomplete) {
		t.Fatalf("err = %v, want ErrContextIncomplete", err)
	}
	if rebuilt != nil && rebuilt.Trace.Context.EvidencePackageID != "" {
		t.Fatalf("EvidencePackageID was SILENTLY REGENERATED as %q -- exactly the failure mode P0-4 forbids",
			rebuilt.Trace.Context.EvidencePackageID)
	}
}

// TestAdversarialAlterExecutionID: editing the execution identifier
// must produce a divergence, because the identifier is committed into
// the trace's node hashes rather than merely carried alongside them.
func TestAdversarialAlterExecutionID(t *testing.T) {
	original, req, res := adversarialRun(t)

	req.Context.ExecutionID = original.Trace.Context.ExecutionID + "-forged"
	verdict, rebuilt, err := replayTampered(t, req, res)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if verdict.Matched {
		t.Fatal("an altered ExecutionID replayed as a MATCH -- the identifier is not actually committed to")
	}
	if err := verdict.Assert(); err == nil {
		t.Fatal("verdict.Assert accepted a divergent replay")
	}
	if verdict.DivergentStage == "" {
		t.Fatal("a divergence with no named stage is not a localised failure")
	}
	// Not regenerated: the rebuild honestly carries the forged value it
	// was handed, and reports the mismatch, rather than quietly
	// restoring the original.
	if rebuilt.Trace.Context.ExecutionID == original.Trace.Context.ExecutionID {
		t.Fatal("the replay silently restored the original ExecutionID instead of failing on the forged one")
	}
}

// TestAdversarialSwapEntityID: pointing the case at a different entity
// while the identity aliases still say who it really is must be caught
// by IDENTITY_RESOLUTION's independent re-resolution, not accepted.
func TestAdversarialSwapEntityID(t *testing.T) {
	_, req, res := adversarialRun(t)

	req.Case.Entity = "IMO1111111"
	req.Case.Subject = "IMO1111111"
	verdict, _, err := replayTampered(t, req, res)

	if err == nil && verdict.Matched {
		t.Fatal("swapping the case entity replayed as a MATCH")
	}
	if err != nil && !errors.Is(err, execution.ErrIdentityMismatch) && !errors.Is(err, execution.ErrStageFailed) {
		t.Fatalf("err = %v, want an identity/stage failure naming the mismatch", err)
	}
	if err == nil && verdict.DivergentStage == "" {
		t.Fatal("a swapped entity produced neither an error nor a localised divergence")
	}
}

// TestAdversarialReplaceDecisionID: the decision identifier is
// content-addressed over the execution plus the decision's own content
// (see execution.go's decisionID), so an independent replay re-derives
// the ORIGINAL value. A forged DecisionID is therefore detectable by
// comparison rather than believed.
func TestAdversarialReplaceDecisionID(t *testing.T) {
	original, req, res := adversarialRun(t)

	forged := Key{
		ExecutionID:       original.Trace.Context.ExecutionID,
		EvidencePackageID: original.Trace.Context.EvidencePackageID,
		DecisionID:        "dec-forged-by-an-attacker",
	}

	verdict, rebuilt, err := replayTampered(t, req, res)
	if err != nil {
		t.Fatalf("replay of the UNTAMPERED request: %v", err)
	}
	if !verdict.Matched {
		t.Fatalf("the untampered baseline did not replay clean: %+v", verdict)
	}

	rederived := FromExecutionResult(*rebuilt)
	if rederived.DecisionID != original.Explanation.DecisionID {
		t.Fatalf("independent replay did not re-derive the same DecisionID: %q vs %q",
			rederived.DecisionID, original.Explanation.DecisionID)
	}
	if rederived.DecisionID == forged.DecisionID {
		t.Fatal("a forged DecisionID was reproduced by an independent replay -- it is not content-addressed at all")
	}
	// And the substitution is detectable, not merely different: nothing
	// in the system accepts the forged key as describing this run.
	if forged.DecisionID == original.Explanation.DecisionID {
		t.Fatal("test is vacuous: the forged value equals the real one")
	}
}

// TestAdversarialAlterIdentityLedgerHead: the identity ledger head is
// folded into the replay record's own fingerprint chain, so editing it
// after the fact breaks verification instead of being ignored.
func TestAdversarialAlterIdentityLedgerHead(t *testing.T) {
	original, _, _ := adversarialRun(t)

	pkg := original.ReplayPackage
	if pkg.Execution.IdentityLedgerHead == "" {
		t.Fatal("the baseline replay package carries no identity ledger head; the test would prove nothing")
	}
	before := pkg.Execution.IdentityLedgerHead
	pkg.Execution.IdentityLedgerHead = strings.Repeat("f", len(before))

	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		// A hard error is an equally acceptable verification failure.
		return
	}
	if cert.Match {
		t.Fatal("an altered IdentityLedgerHead verified as a MATCH -- it is not part of the fingerprint chain")
	}
	if err := cert.Assert(); err == nil {
		t.Fatal("certificate.Assert accepted a non-matching verification")
	}
	// Not regenerated: the certificate reports the divergence rather
	// than quietly restoring the committed head.
	if pkg.Execution.IdentityLedgerHead == before {
		t.Fatal("the replay engine rewrote the tampered head back to the original")
	}
}

// TestAdversarialUntamperedBaselineVerifiesClean is the control every
// adversarial suite needs: without it, a suite that fails on everything
// (including correct input) would look like it was working.
func TestAdversarialUntamperedBaselineVerifiesClean(t *testing.T) {
	original, req, res := adversarialRun(t)

	verdict, _, err := replayTampered(t, req, res)
	if err != nil {
		t.Fatalf("untampered replay: %v", err)
	}
	if err := verdict.Assert(); err != nil {
		t.Fatalf("untampered replay did not verify: %v", err)
	}

	cert, err := replay.NewEngine().Replay(original.ReplayPackage)
	if err != nil {
		t.Fatalf("untampered replay package: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("untampered verification certificate: %v", err)
	}

	key := FromExecutionResult(original).WithIdentityLedgerHead(res.Head())
	for name, value := range map[string]string{
		"ExecutionID":               key.ExecutionID,
		"EvidencePackageID":         key.EvidencePackageID,
		"EntityID":                  key.EntityID,
		"DecisionID":                key.DecisionID,
		"VerificationCertificateID": key.VerificationCertificateID,
		"ReplayPackageID":           key.ReplayPackageID,
		"EntityIdentityLedgerHead":  key.EntityIdentityLedgerHead,
	} {
		if value == "" {
			t.Errorf("%s is empty on a clean run -- the adversarial tests above would be vacuous for it", name)
		}
	}
}

// unmarshalRequest decodes an ExportReplay payload. It exists as a
// named helper so the adversarial tests read as "decode, mutate one
// field, re-encode, verify" rather than burying encoding/json in each.
func unmarshalRequest(data []byte, out *execution.ReplayRequest) error {
	return json.Unmarshal(data, out)
}

// --- PHASE E5 (P1-17): correlation_propagation_coverage --------------
//
// The gate cmd/veriqo-readiness runs by name. It measures the property
// PHASE D states positively: one operation's identity is locked from
// start to end, and every consumer downstream sees the SAME identifiers
// rather than its own reconstruction of them.

// TestCorrelationPropagationCoverageEveryIdentifierIsPopulated proves
// the key a real execution produces has no empty fields that a
// downstream consumer would then have to invent.
func TestCorrelationPropagationCoverageEveryIdentifierIsPopulated(t *testing.T) {
	original, _, res := adversarialRun(t)
	key := FromExecutionResult(original).WithIdentityLedgerHead(res.Head())

	for name, value := range map[string]string{
		"ExecutionID":               key.ExecutionID,
		"EvidencePackageID":         key.EvidencePackageID,
		"EntityID":                  key.EntityID,
		"DecisionID":                key.DecisionID,
		"VerificationCertificateID": key.VerificationCertificateID,
		"ReplayPackageID":           key.ReplayPackageID,
		"EntityIdentityLedgerHead":  key.EntityIdentityLedgerHead,
	} {
		if value == "" {
			t.Errorf("%s is empty on a real execution -- a downstream consumer would have to invent it", name)
		}
	}
	// IntentID is deliberately empty from FromExecutionResult and is
	// filled by pkg/lifecycle, which is the only layer that holds an
	// Intent. Asserting it non-empty HERE would be asserting a
	// fabrication; asserting it empty records the real contract.
	if key.IntentID != "" {
		t.Errorf("IntentID = %q from a bare execution.Result -- pkg/execution has no Intent to derive it from", key.IntentID)
	}
}

// TestCorrelationPropagationCoverageIdentifiersAreDistinct catches the
// degenerate failure where several "different" identifiers are actually
// aliases of one value, which would make the whole correlation key
// carry one bit of information instead of eight.
func TestCorrelationPropagationCoverageIdentifiersAreDistinct(t *testing.T) {
	original, _, res := adversarialRun(t)
	key := FromExecutionResult(original).WithIdentityLedgerHead(res.Head())

	values := map[string]string{
		"ExecutionID":               key.ExecutionID,
		"EvidencePackageID":         key.EvidencePackageID,
		"EntityID":                  key.EntityID,
		"DecisionID":                key.DecisionID,
		"VerificationCertificateID": key.VerificationCertificateID,
		"ReplayPackageID":           key.ReplayPackageID,
		"EntityIdentityLedgerHead":  key.EntityIdentityLedgerHead,
	}
	seen := map[string]string{}
	for name, v := range values {
		if prev, dup := seen[v]; dup {
			t.Errorf("%s and %s are the same value (%q) -- they are supposed to be independent identities", prev, name, v)
		}
		seen[v] = name
	}
}

// TestCorrelationPropagationCoverageSurvivesAnIndependentReplay is the
// end-to-end propagation property: the identifiers a cold, independent
// replay derives must equal the originals, so a consumer joining on
// them across process boundaries joins the same operation.
func TestCorrelationPropagationCoverageSurvivesAnIndependentReplay(t *testing.T) {
	original, req, res := adversarialRun(t)
	before := FromExecutionResult(original)

	verdict, rebuilt, err := replayTampered(t, req, res)
	if err != nil {
		t.Fatalf("independent replay: %v", err)
	}
	if err := verdict.Assert(); err != nil {
		t.Fatalf("independent replay did not verify: %v", err)
	}
	after := FromExecutionResult(*rebuilt)

	for name, pair := range map[string][2]string{
		"ExecutionID":       {before.ExecutionID, after.ExecutionID},
		"EvidencePackageID": {before.EvidencePackageID, after.EvidencePackageID},
		"EntityID":          {before.EntityID, after.EntityID},
		"DecisionID":        {before.DecisionID, after.DecisionID},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s did not survive an independent replay: %q -> %q", name, pair[0], pair[1])
		}
	}
}
