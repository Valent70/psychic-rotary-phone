// canonical_truth_normal_test.go is the WAVE A overall acceptance gate
// of the canonical-truth-path mandate, stated in the mandate's own
// words:
//
//	real source -> evidence -> trust -> truth -> decision -> ledger
//	-> certificate -> replay
//	adalah satu execution lineage, bukan kumpulan subsystem yang
//	kebetulan berjalan bersama.
//
// The distinction between "one lineage" and "subsystems that happen to
// run together" is not provable by observing that every subsystem
// produced output. It is provable by CHANGING one link and showing that
// everything downstream of it moves. That is what
// TestAcceptanceCanonicalTruthPathIsOneExecutionLineage does: it runs a
// control case, then three variants each perturbing exactly one link
// (the trust ledger, a source, the policy), and requires the downstream
// commitments to differ in each.
package acceptance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/canonical"
	"veriqo/pkg/execution"
	"veriqo/pkg/ledger"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/decision"
	"veriqo/pkg/moat/entity"
	"veriqo/pkg/moat/provenance"
	"veriqo/pkg/replay"
	"veriqo/pkg/trust/state"
	"veriqo/veriqo/kernel"
)

func truthAliases() []entity.Alias {
	// Two modelled alias kinds, not one: entity resolution only writes
	// the identity ledger when it MERGES aliases, so a single-alias case
	// would leave IdentityLedgerHead legitimately empty and this suite
	// would be asserting on a link that never had anything to record.
	return []entity.Alias{
		{Kind: "IMO", Value: "9074729"},
		{Kind: "MMSI", Value: "636014932"},
	}
}

func baseTruthCase() truthCase {
	return truthCase{
		objective: "canonical truth path acceptance", tenant: "acceptance-tenant",
		aliases: truthAliases(), predicate: "BERTH_ARRIVAL_TIME",
		submissions: []canonical.SourceSubmission{
			sub("AIS_FEED", "AIS_PROVIDER", "10:00", 0.8),
			sub("PORT_AUTHORITY_LOG", "PORT_AUTHORITY", "10:00", 0.9),
		},
		pattern: 0.4, price: 0.4, tick: 10,
	}
}

// TestAcceptanceCanonicalTruthPathIsOneExecutionLineage is the mandate's
// section I acceptance criterion, made mechanical.
func TestAcceptanceCanonicalTruthPathIsOneExecutionLineage(t *testing.T) {
	dir := t.TempDir()
	k := newTruthKernel(t)
	l := withDurableLedger(t, k, dir)

	// Both providers are assessed and trusted, so the control run is a
	// NORMAL-posture, releasable decision — the baseline every
	// perturbation below is measured against.
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)

	control := baseTruthCase().run(t, k)

	// --- Every link of the chain produced a real, non-empty artifact ---
	for name, got := range map[string]string{
		"intent_id":             control.Certificate.IntentID,
		"entity_id":             control.Certificate.EntityID,
		"evidence_package_id":   control.Correlation.EvidencePackageID,
		"identity_ledger_head":  control.Correlation.EntityIdentityLedgerHead,
		"trust_root_hash":       control.Canonical.Trust.RootHash,
		"trust_ledger_head":     control.Canonical.Trust.LedgerHead,
		"arbitration_hash":      control.Canonical.Certificate.ArbitrationHash,
		"truth_hash":            control.Canonical.Certificate.TruthHash,
		"decision_id":           control.Correlation.DecisionID,
		"execution_root_hash":   control.Certificate.ExecutionRootHash,
		"durable_ledger_head":   control.Certificate.DurableLedgerHead,
		"canonical_certificate": control.Certificate.Canonical.Hash,
		"lifecycle_certificate": control.Certificate.Hash,
		"replay_package_id":     control.Correlation.ReplayPackageID,
		"verification_cert_id":  control.Correlation.VerificationCertificateID,
	} {
		if got == "" {
			t.Errorf("%s is empty: the chain has a link that produced nothing", name)
		}
	}

	// --- The certificate genuinely verifies against its own fields ----
	if err := lifecycle.VerifyCertificate(control.Certificate); err != nil {
		t.Fatalf("lifecycle certificate does not verify: %v", err)
	}
	if err := canonical.VerifyCertificate(control.Certificate.Canonical); err != nil {
		t.Fatalf("canonical certificate does not verify: %v", err)
	}
	if err := control.Execution.Certificate.Assert(); err != nil {
		t.Fatalf("the execution's own replay verification did not match: %v", err)
	}

	// --- The durable ledger holds the whole minimum event set ---------
	events, err := l.EventsForCase(string(control.CaseID))
	if err != nil {
		t.Fatalf("reading durable ledger: %v", err)
	}
	if cov := ledger.KindCoverage(events); !cov.Complete() {
		t.Errorf("durable ledger is missing event kinds %v for a completed case", cov.Missing)
	}
	if head, _ := l.Head(); head != control.Certificate.DurableLedgerHead {
		t.Errorf("the certificate commits to durable head %s but the ledger's head is %s",
			control.Certificate.DurableLedgerHead, head)
	}
	if err := l.Verify(); err != nil {
		t.Errorf("durable ledger failed its own verification: %v", err)
	}

	// --- PERTURBATION 1: change TRUST, everything downstream moves ----
	// A byte-identical case, on a byte-identically-configured kernel,
	// except that one provider has been revoked. Nothing about the
	// evidence differs; only the trust ledger does.
	//
	// A SEPARATE kernel is required, not a second run on the same one:
	// pkg/moat/fusion refuses a duplicate evidence submission for the
	// same claim (a real anti-ballot-stuffing invariant), so re-running
	// an identical case against a live engine is not a thing the system
	// permits — which is itself correct.
	revokedK := newTruthKernel(t)
	withDurableLedger(t, revokedK, t.TempDir())
	assessTrust(t, revokedK, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, revokedK, "PORT_AUTHORITY", 0.95, 1)
	revokeTrust(t, revokedK, "AIS_PROVIDER", 2)
	afterRevocation := baseTruthCase().run(t, revokedK)

	if afterRevocation.Canonical.Trust.RootHash == control.Canonical.Trust.RootHash {
		t.Error("revoking a provider did not change the trust evaluation root")
	}
	if afterRevocation.Canonical.Certificate.Hash == control.Canonical.Certificate.Hash {
		t.Error("revoking a provider did not change the canonical certificate")
	}
	if afterRevocation.Certificate.ExecutionRootHash == control.Certificate.ExecutionRootHash {
		t.Error("revoking a provider did not change the execution root — trust is not inside the root")
	}
	if afterRevocation.Certificate.Hash == control.Certificate.Hash {
		t.Error("revoking a provider did not change the lifecycle certificate")
	}
	if len(afterRevocation.Canonical.Trust.Excluded) != 1 ||
		afterRevocation.Canonical.Trust.Excluded[0] != "AIS_FEED" {
		t.Errorf("a revoked provider's source was not excluded: excluded=%v",
			afterRevocation.Canonical.Trust.Excluded)
	}
	if afterRevocation.Canonical.Arbitration.EvidenceCount >= control.Canonical.Arbitration.EvidenceCount {
		t.Errorf("the revoked provider's evidence still reached arbitration: %d items vs %d in the control",
			afterRevocation.Canonical.Arbitration.EvidenceCount,
			control.Canonical.Arbitration.EvidenceCount)
	}

	// --- PERTURBATION 2: change a SOURCE, everything downstream moves -
	k2 := newTruthKernel(t)
	withDurableLedger(t, k2, t.TempDir())
	assessTrust(t, k2, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k2, "PORT_AUTHORITY", 0.95, 1)
	controlOnK2 := baseTruthCase().run(t, k2)

	k3 := newTruthKernel(t)
	withDurableLedger(t, k3, t.TempDir())
	assessTrust(t, k3, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k3, "PORT_AUTHORITY", 0.95, 1)
	changedSource := baseTruthCase()
	changedSource.submissions[0].Value = "10:47"
	movedSource := changedSource.run(t, k3)

	// NOTE, deliberately asserted on the replay package rather than on
	// Correlation.EvidencePackageID: those are two DIFFERENT identifiers
	// with the same name. Correlation.EvidencePackageID is
	// pkg/lifecycle's EvidencePlan hash — a commitment to what evidence
	// was REQUIRED — and correctly does not move when a source's value
	// changes. pkg/replay.ExecutionRecord.EvidencePackageID is the hash
	// of the submissions themselves. Only the second one answers "does
	// the record commit to the evidence", which is what this assertion
	// is for. The collision of names is recorded as an observation in
	// docs/governance/CANONICAL_TRUTH_PATH_RESIDUAL_REGISTER.md.
	if movedSource.Execution.ReplayPackage.Execution.EvidencePackageID ==
		controlOnK2.Execution.ReplayPackage.Execution.EvidencePackageID {
		t.Error("changing a source's reported value did not change the evidence package identity")
	}
	if movedSource.Certificate.ExecutionRootHash == controlOnK2.Certificate.ExecutionRootHash {
		t.Error("changing a source did not change the execution root")
	}
	if movedSource.Certificate.DurableLedgerHead == controlOnK2.Certificate.DurableLedgerHead {
		t.Error("changing a source did not change the durable ledger head it was recorded under")
	}

	// --- PERTURBATION 3: change the POLICY, the decision root moves ---
	k4 := newTruthKernel(t)
	withDurableLedger(t, k4, t.TempDir())
	assessTrust(t, k4, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k4, "PORT_AUTHORITY", 0.95, 1)

	strict := truthPolicy
	strict.Name = "canonical_truth_path_acceptance_strict_v1"
	strict.FlagThreshold = 0.05
	strict.EscalateThreshold = 0.1
	strictRes := runWithPolicy(t, k4, baseTruthCase(), strict)

	if strictRes.Canonical.Decision.Action == controlOnK2.Canonical.Decision.Action {
		t.Errorf("lowering the escalation threshold from %v to %v left the native action at %s — "+
			"the policy is not actually driving the decision",
			truthPolicy.EscalateThreshold, strict.EscalateThreshold,
			strictRes.Canonical.Decision.Action)
	}
	if strictRes.Correlation.DecisionID == controlOnK2.Correlation.DecisionID {
		t.Error("changing the policy did not change the decision identity")
	}
	if strictRes.Certificate.ExecutionRootHash == controlOnK2.Certificate.ExecutionRootHash {
		t.Error("changing the policy did not change the execution root")
	}

	// --- The whole chain replays, including trust and the ledger head -
	assertColdReplayReproducesEverything(t, control)
}

// runWithPolicy is baseTruthCase().run with an explicit policy — kept
// here rather than as another truthCase field because the policy is the
// thing under test in exactly one place.
func runWithPolicy(t *testing.T, k *kernel.Kernel, c truthCase, p decision.Policy) *lifecycle.Result {
	t.Helper()
	in := lifecycle.Intent{
		ActorID: "canonical-truth-acceptance", Objective: c.objective,
		Tenant: c.tenant, EntityAliases: c.aliases,
		TemporalScope: "acceptance", Tick: c.tick,
	}
	plan := lifecycle.PlanEvidence(in, nil)
	caseIn := canonical.CaseInput{
		Predicate: c.predicate, Submissions: c.submissions, Policy: p,
		PatternScore: c.pattern, PriceAnomaly: c.price, Tick: c.tick,
	}
	res, err := k.Lifecycle.RunUnified(context.Background(), in, plan, caseIn)
	if err != nil {
		t.Fatalf("RunUnified with policy %s: %v", p.Name, err)
	}
	return res
}

// assertColdReplayReproducesEverything runs the recorded replay package
// through a FRESH pkg/replay engine — one holding no pointer to the
// pipeline that produced the original — and requires an exact match,
// then confirms the replay genuinely carried trust and the durable
// ledger head rather than silently omitting them.
func assertColdReplayReproducesEverything(t *testing.T, res *lifecycle.Result) {
	t.Helper()
	pkg := res.Execution.ReplayPackage

	if len(pkg.Execution.TrustLedger) == 0 {
		t.Fatal("the replay package carries no trust ledger; trust cannot be reproduced from it")
	}
	if pkg.Execution.PolicyHash == "" {
		t.Error("the replay package carries no policy hash")
	}
	if pkg.Execution.DurableLedgerHead == "" {
		t.Error("the replay package carries no durable ledger head")
	}

	// Round-trip through bytes so the replay provably consumes DATA.
	raw, err := pkg.Marshal()
	if err != nil {
		t.Fatalf("marshalling replay package: %v", err)
	}
	restored, err := replay.Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshalling replay package: %v", err)
	}
	cert, err := replay.NewEngine().Replay(restored)
	if err != nil {
		t.Fatalf("cold replay failed: %v", err)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("cold replay diverged: %v", err)
	}

	// Trust must be one of the stages the replay actually compared, not
	// a field that survived serialization without participating.
	var sawTrust, sawPolicy, sawDurable bool
	for _, s := range pkg.Execution.Stages {
		switch s.Stage {
		case replay.StageTrust:
			sawTrust = true
		case replay.StagePolicy:
			sawPolicy = true
		case replay.StageDurableLedger:
			sawDurable = true
		}
	}
	if !sawTrust || !sawPolicy || !sawDurable {
		t.Errorf("replay stage set is incomplete: trust=%v policy=%v durable=%v",
			sawTrust, sawPolicy, sawDurable)
	}
}

// TestAcceptanceCanonicalTruthTrustedAndUntrustedProvidersDiffer is the
// mandate's section III acceptance criterion:
//
//	trusted provider vs untrusted provider -> outcome/review state must
//	differ.
func TestAcceptanceCanonicalTruthTrustedAndUntrustedProvidersDiffer(t *testing.T) {
	trustedK := newTruthKernel(t)
	assessTrust(t, trustedK, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, trustedK, "PORT_AUTHORITY", 0.95, 1)
	trusted := baseTruthCase().run(t, trustedK)

	// Identical case, identical evidence, on a kernel where neither
	// provider has ever been assessed.
	untrustedK := newTruthKernel(t)
	untrusted := baseTruthCase().run(t, untrustedK)

	if trusted.HumanReviewRequired {
		t.Error("a case whose every provider is TRUSTED still demanded human review")
	}
	if !untrusted.HumanReviewRequired {
		t.Error("a case whose providers have never been assessed did NOT demand human review; " +
			"UNKNOWN is being treated as if it were trusted")
	}
	if len(untrusted.TrustReviewReasons) == 0 {
		t.Error("human review was required with no per-source reason given")
	}

	for _, id := range []string{"AIS_FEED", "PORT_AUTHORITY_LOG"} {
		lvl, posture, ok := trustLevelOf(trusted, id)
		if !ok {
			t.Fatalf("trusted run did not evaluate %s at all", id)
		}
		if lvl != state.LevelTrusted || posture != canonical.PostureNormal {
			t.Errorf("trusted run: %s is %s/%s, want TRUSTED/NORMAL", id, lvl, posture)
		}
		lvl, posture, ok = trustLevelOf(untrusted, id)
		if !ok {
			t.Fatalf("untrusted run did not evaluate %s at all", id)
		}
		if lvl != state.LevelUnknown || posture != canonical.PostureRestricted {
			t.Errorf("untrusted run: %s is %s/%s, want UNKNOWN/RESTRICTED", id, lvl, posture)
		}
	}

	if trusted.Canonical.Certificate.TrustReviewRequired ==
		untrusted.Canonical.Certificate.TrustReviewRequired {
		t.Error("the certificate does not distinguish a reviewed case from an unreviewed one")
	}
	if trusted.Certificate.ExecutionRootHash == untrusted.Certificate.ExecutionRootHash {
		t.Error("two runs with materially different trust postures share an execution root")
	}
}

// TestAcceptanceCanonicalTruthDurableLedgerSurvivesTheProcess proves the
// on-disk record is checkable INDEPENDENTLY of the process that wrote
// it: a second ledger handle, opened over the same directory with no
// access to the first one's memory, reconstructs the same events and
// the same head.
//
// This is the in-process half of WAVE A item 6. The out-of-process half
// — a real SIGKILL and a genuinely fresh binary — is
// canonical_truth_crash_replay_test.go.
func TestAcceptanceCanonicalTruthDurableLedgerSurvivesTheProcess(t *testing.T) {
	dir := t.TempDir()
	k := newTruthKernel(t)
	l := withDurableLedger(t, k, dir)
	assessTrust(t, k, "AIS_PROVIDER", 0.9, 1)
	assessTrust(t, k, "PORT_AUTHORITY", 0.95, 1)

	res := baseTruthCase().run(t, k)
	wantHead, wantSeq := l.Head()
	if err := l.Close(); err != nil {
		t.Fatalf("closing the writing ledger: %v", err)
	}

	// A second handle over the same bytes, with no shared state.
	reopened, rep, err := ledger.Open(ledger.Config{Dir: dir})
	if err != nil {
		t.Fatalf("reopening the ledger: %v", err)
	}
	defer reopened.Close() //nolint:errcheck // test teardown
	if rep.FailedClosed {
		t.Fatalf("recovery failed closed on an undisturbed ledger: %+v", rep.Findings)
	}
	if rep.LostRecords != 0 || rep.TruncatedBytes != 0 {
		t.Errorf("a cleanly-closed ledger reported %d lost records and %d truncated bytes",
			rep.LostRecords, rep.TruncatedBytes)
	}

	gotHead, gotSeq := reopened.Head()
	if gotHead != wantHead || gotSeq != wantSeq {
		t.Fatalf("head after reopen = %s/%d, want %s/%d", gotHead, gotSeq, wantHead, wantSeq)
	}
	if gotHead != res.Certificate.DurableLedgerHead {
		t.Errorf("the certificate's durable head %s is not the reopened ledger's head %s",
			res.Certificate.DurableLedgerHead, gotHead)
	}

	events, err := reopened.EventsForCase(string(res.CaseID))
	if err != nil {
		t.Fatalf("reading events from the reopened ledger: %v", err)
	}
	if cov := ledger.KindCoverage(events); !cov.Complete() {
		t.Errorf("reopened ledger is missing kinds %v", cov.Missing)
	}
	// The DECISION event must name the execution root the run produced —
	// the join that ties the durable record to the computation.
	var found bool
	for _, ev := range events {
		if ev.Kind == ledger.EventDecision {
			found = true
			if !strings.Contains(ev.Detail, res.Certificate.ExecutionRootHash) {
				t.Errorf("the durable DECISION event does not name the execution root %s: %q",
					res.Certificate.ExecutionRootHash, ev.Detail)
			}
		}
	}
	if !found {
		t.Error("no DECISION event survived the reopen")
	}
}

// TestAcceptanceCanonicalTruthProvenanceScoreIsNotInterpretableAlone is
// the mandate's section IV acceptance criterion. It asserts on the REAL
// assessment a real run produced, not on a hand-built struct.
func TestAcceptanceCanonicalTruthProvenanceScoreIsNotInterpretableAlone(t *testing.T) {
	k := newTruthKernel(t)
	res := baseTruthCase().run(t, k)
	a := res.Canonical.Provenance

	// Neither source declares an upstream, so the honest finding is
	// UNKNOWN — and the ratio is nevertheless the trivial 1.0 that
	// section IV names as dangerous.
	if a.Status != provenance.StatusUnknown {
		t.Fatalf("provenance status = %s, want UNKNOWN for two sources with no declared ancestry", a.Status)
	}
	if a.Score < 1.0 {
		t.Fatalf("this test only means something when the score IS the misleading 1.0; got %v", a.Score)
	}
	if a.IsVerifiedIndependent() {
		t.Error("Score=1.0 with Status=UNKNOWN was reported as verified-independent — " +
			"exactly the false positive section IV exists to close")
	}
	if got := a.ScoreDisplay(); got != provenance.ScoreNotInterpretable {
		t.Errorf("ScoreDisplay() = %q, want %q", got, provenance.ScoreNotInterpretable)
	}
	if res.Canonical.Certificate.ProvenanceVerifiedIndependent {
		t.Error("the certificate claims verified independence for an UNKNOWN assessment")
	}
	if a.PairCount != 1 {
		t.Errorf("PairCount = %d for a two-source case, want 1", a.PairCount)
	}
	if a.Basis != provenance.BasisNoDeclarations {
		t.Errorf("Basis = %s, want NO_DECLARATIONS", a.Basis)
	}
	if a.AttestationComplete {
		t.Error("AttestationComplete is true for sources nobody has attested")
	}

	// The RISK model — the one place in this repository where an
	// independence ratio is genuinely load-bearing — must carry the same
	// epistemic standing rather than a bare number, and must SAY in its
	// own explanation that the independence term rests on nothing
	// compared.
	r := res.Canonical.Risk
	if r.ProvenanceVerifiedIndependent {
		t.Error("the risk result claims verified independence for an UNKNOWN assessment")
	}
	if r.ProvenanceStatus != provenance.StatusUnknown {
		t.Errorf("risk result provenance status = %s, want UNKNOWN", r.ProvenanceStatus)
	}
	if r.ProvenanceBasis != provenance.BasisNoDeclarations {
		t.Errorf("risk result provenance basis = %s, want NO_DECLARATIONS", r.ProvenanceBasis)
	}
	var saidSo bool
	for _, line := range r.Explanation {
		if strings.Contains(line, provenance.ScoreNotInterpretable) {
			saidSo = true
		}
	}
	if !saidSo {
		t.Errorf("the risk explanation never says the independence term is not interpretable:\n%v",
			r.Explanation)
	}

	// And the DAG's own explanation, which is what a consumer reads,
	// renders the display form rather than the raw number.
	fusionLines := res.Execution.Explanation.FusionExplanation.Lines
	if len(fusionLines) == 0 {
		t.Fatal("the decision explanation has no fusion lines")
	}
	if !strings.Contains(strings.Join(fusionLines, " "), provenance.ScoreNotInterpretable) {
		t.Errorf("the decision explanation renders a bare independence score: %v", fusionLines)
	}

	// A single-source case is the sharper form: PairCount 0, nothing
	// compared at all, and still a 1.0 ratio.
	single := baseTruthCase()
	single.submissions = single.submissions[:1]
	solo := single.run(t, newTruthKernel(t))
	if solo.Canonical.Provenance.PairCount != 0 {
		t.Errorf("single-source PairCount = %d, want 0", solo.Canonical.Provenance.PairCount)
	}
	if solo.Canonical.Provenance.Basis != provenance.BasisNoPairs {
		t.Errorf("single-source Basis = %s, want NO_PAIRS", solo.Canonical.Provenance.Basis)
	}
	if solo.Canonical.Provenance.IsVerifiedIndependent() {
		t.Error("a single-source case was reported as verified-independent")
	}
}

// TestAcceptanceCanonicalTruthNoTrustEngineIsHonestlySkipped guards the
// degenerate case in the opposite direction from the one above: a
// pipeline with NO trust engine must report that it made no trust
// finding, never that it made a favourable one.
func TestAcceptanceCanonicalTruthNoTrustEngineIsHonestlySkipped(t *testing.T) {
	p := canonical.NewPipeline(nil)
	p.TrustState = nil
	// A bare engine over that pipeline — the guarded execution.NewEngine
	// call is legal here because internal/entrypoints deliberately does
	// not scan _test.go files; a test proving the no-trust-engine branch
	// is honest cannot itself go through an orchestrator that always
	// attaches one.
	eng := execution.NewEngine(p)

	res, err := eng.Run(context.Background(), execution.Input{
		Context: execution.Context{
			ExecutionID: "no-trust-engine", CaseID: "no-trust-engine",
			Tenant: "acceptance-tenant", Actor: "acceptance",
			PolicyVersion: truthPolicy.Name, EvidencePackageID: "no-trust-engine-evidence",
			IdentityResolutionVersion: "acceptance", Tick: 1,
		},
		Case: canonical.CaseInput{
			Subject: "SUBJ", Predicate: "P", Policy: truthPolicy, Tick: 1,
			Submissions: []canonical.SourceSubmission{sub("S1", "PROV", "v", 0.8)},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Canonical.Trust.Configured {
		t.Fatal("a pipeline with no trust engine reported Configured=true")
	}
	if res.HumanReviewRequired {
		t.Error("a pipeline that made no trust finding demanded review as if it had made an adverse one")
	}
	var trustNode *execution.Node
	for i := range res.Trace.Nodes {
		if res.Trace.Nodes[i].StageID == execution.StageTrust {
			trustNode = &res.Trace.Nodes[i]
		}
	}
	if trustNode == nil {
		t.Fatal("TRUST_STATE is missing from the trace entirely")
	}
	if trustNode.Status != execution.StatusSkipped {
		t.Errorf("TRUST_STATE = %s with no trust engine attached, want SKIPPED — a consumer must "+
			"never read 'trust ran and found nothing wrong' off a deployment with no trust authority",
			trustNode.Status)
	}
	if !strings.Contains(trustNode.Detail, "no trust engine") {
		t.Errorf("TRUST_STATE detail does not say why it was skipped: %q", trustNode.Detail)
	}
}

// TestAcceptanceCanonicalTruthAllEvidenceRevokedFailsClosed is the limit
// case of "REVOKED provider's evidence cannot influence the decision at
// all": when that leaves nothing, there is no decision, not a decision
// over an empty set.
func TestAcceptanceCanonicalTruthAllEvidenceRevokedFailsClosed(t *testing.T) {
	k := newTruthKernel(t)
	revokeTrust(t, k, "AIS_PROVIDER", 1)
	revokeTrust(t, k, "PORT_AUTHORITY", 1)

	_, err := baseTruthCase().runErr(k)
	if err == nil {
		t.Fatal("a case whose every provider is REVOKED produced a decision")
	}
	if !errors.Is(err, canonical.ErrAllEvidenceUntrusted) {
		t.Fatalf("error = %v, want ErrAllEvidenceUntrusted", err)
	}
}
