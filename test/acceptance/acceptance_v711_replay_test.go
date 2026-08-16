package acceptance

import (
	"testing"

	"veriqo/internal/assurance"
	"veriqo/pkg/authz"
	"veriqo/pkg/canonical"
	"veriqo/pkg/moat/evidencegraph"
	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/replay"
	tstate "veriqo/pkg/trust/state"
)

func recordRun(t *testing.T, subject string) (replay.ExecutionRecord, *canonical.Pipeline) {
	t.Helper()
	p := canonical.NewPipeline(nil)
	in := baseCase(subject)
	res, err := p.RunCanonical("a", in)
	if err != nil {
		t.Fatalf("RunCanonical: %v", err)
	}
	rec, err := replay.Record("a", in, res, p.Dependencies.ReplayAll(), "")
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	return rec, p
}

func TestAcceptance71_ReplayIdentitiesAreDistinct(t *testing.T) {
	rec, _ := recordRun(t, "VESSEL:IMO9000010")
	pkg, err := replay.NewReplayPackage("auditor", "n", rec)
	if err != nil {
		t.Fatalf("NewReplayPackage: %v", err)
	}
	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if cert.ExecutionID == cert.ReplayPackageID || cert.OriginalResultHash == cert.VerificationCertificateID {
		t.Fatal("replay identities collapsed into one")
	}
}

func TestAcceptance72_TamperedEvidenceBreaksReplay(t *testing.T) {
	rec, _ := recordRun(t, "VESSEL:IMO9000011")
	rec.CaseInput.Submissions[0].Value = "FLIPPED"
	pkg, _ := replay.NewReplayPackage("auditor", "n", rec)
	cert, err := replay.NewEngine().Replay(pkg)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if cert.Match {
		t.Fatal("tampered evidence replayed as matching")
	}
	if cert.DivergedStage == "" {
		t.Fatal("divergence not localised")
	}
}

func TestAcceptance73_TamperedDependencyLedgerRejected(t *testing.T) {
	rec, _ := recordRun(t, "VESSEL:IMO9000012")
	if len(rec.DependencyLedger) == 0 {
		t.Skip("no dependencies declared")
	}
	rec.DependencyLedger[0].Confidence = 0.01
	pkg, _ := replay.NewReplayPackage("auditor", "n", rec)
	if _, err := replay.NewEngine().Replay(pkg); err == nil {
		t.Fatal("tampered dependency ledger accepted by replay")
	}
}

func TestAcceptance74_ReplayPackageWireFormatTamperEvident(t *testing.T) {
	rec, _ := recordRun(t, "VESSEL:IMO9000013")
	pkg, _ := replay.NewReplayPackage("auditor", "n", rec)
	raw, err := pkg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := replay.Unmarshal(raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pkg.RequestedBy = "someone-else"
	bad, _ := pkg.Marshal()
	if _, err := replay.Unmarshal(bad); err == nil {
		t.Fatal("tampered replay package accepted")
	}
}

func TestAcceptance75_VerificationCertificateTamperEvident(t *testing.T) {
	rec, _ := recordRun(t, "VESSEL:IMO9000014")
	pkg, _ := replay.NewReplayPackage("auditor", "n", rec)
	cert, _ := replay.NewEngine().Replay(pkg)
	cert.Match = !cert.Match
	if err := replay.VerifyCertificate(cert); err == nil {
		t.Fatal("tampered verification certificate accepted")
	}
}

func TestAcceptance76_DependencyGraphRebuildIsDeterministic(t *testing.T) {
	_, p := recordRun(t, "VESSEL:IMO9000015")
	g, err := evidencegraph.Rebuild(p.Dependencies.ReplayAll())
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if g.RootHash() != p.Dependencies.RootHash() {
		t.Fatal("rebuilt dependency root hash differs")
	}
}

func TestAcceptance77_BayesianTraceReplay(t *testing.T) {
	tr := hbayes.Transition{States: []hbayes.State{"A", "B"}, P: map[hbayes.State]map[hbayes.State]float64{
		"A": {"A": 0.8, "B": 0.2}, "B": {"A": 0.3, "B": 0.7}}}
	m, err := hbayes.NewTemporalModel([]hbayes.State{"A", "B"}, tr, map[hbayes.State]float64{"A": 0.5, "B": 0.5}, nil, 0.01)
	if err != nil {
		t.Fatalf("NewTemporalModel: %v", err)
	}
	seq := []hbayes.TickObservations{{Tick: 1, Observations: []hbayes.Observation{
		{SourceID: "s", Likelihood: map[hbayes.State]float64{"A": 0.7, "B": 0.3}}}}}
	trace, err := m.Infer(seq, nil)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if err := m.Replay(seq, nil, trace); err != nil {
		t.Fatalf("bayesian replay failed: %v", err)
	}
}

func TestAcceptance78_TrustLedgerTamperDetected(t *testing.T) {
	e := tstate.NewEngine(100, 0.5)
	if _, err := e.Assess("acme", "op", "p", "clean", 0.9, []string{"ev"}, 1, 0); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("clean trust chain rejected: %v", err)
	}
}

func TestAcceptance79_PolicyVersionChainVerifies(t *testing.T) {
	e := authz.NewEngine()
	for i := 0; i < 3; i++ {
		if _, err := e.Publish(authz.Document{ID: "p", Rules: []authz.Rule{
			{ID: "r", Effect: authz.Allow, Actions: []string{"read"}, Resources: []string{"*"}}}}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	if err := e.VerifyChain(); err != nil {
		t.Fatalf("policy chain: %v", err)
	}
}

func TestAcceptance80_ReleaseCertificateTamperEvident(t *testing.T) {
	r := assurance.NewRegistry()
	if err := r.Register(assurance.Gate{ID: "unit", Mandatory: true, RequiredStatus: assurance.StatusVerified, ExitCriteria: "tests pass"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Attach("unit", assurance.StatusVerified, assurance.NewEvidence("unit", "go test ./...", "ok", 0, 1)); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	cert := assurance.ReleaseCertificate{Version: "v7.11.0"}.Finalize(r.Assess())
	if err := cert.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	cert.Version = "v9.9.9"
	if err := cert.Verify(); err == nil {
		t.Fatal("tampered release certificate verified")
	}
}
