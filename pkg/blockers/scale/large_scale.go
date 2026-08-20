package scale

import (
	"context"
	"errors"
	"fmt"
	"time"

	"veriqo/pkg/blockers"
	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/governance/reconciliation"
)

// LargeScaleReport is external audit item A7's required qualification
// output: node identities, the generated workload's declared origin,
// throughput, and a full A6 reconciliation.Report over what was
// actually submitted vs. processed — reusing the same accounting audit
// item A6 built (see docs/AUDIT_A1_A18_GAP_MAPPING.md's A6 entry), not
// a second implementation of accept/reject/lost counting.
type LargeScaleReport struct {
	NodeCount        int
	EnvelopeCount    int
	Seed             uint64
	NodeIdentities   []NodeIdentity
	DataOrigin       livedata.DataMode // always ModeRealDerivedBenchmark, see GenerateWorkload
	Elapsed          time.Duration
	ThroughputPerSec float64
	Reconciliation   reconciliation.Report
}

// Pass reports whether the run's reconciliation satisfied every
// acceptance invariant — the same pass/fail definition A6 itself uses,
// not a separately-invented one.
func (r LargeScaleReport) Pass() bool { return r.Reconciliation.Pass() }

// buildNodeIdentities deterministically assigns audit item A7's 9-field
// identity to each provisioned node — no time.Now(): LaunchTick is the
// caller-supplied logical tick, and every other field is a pure function
// of the node's own ID plus the caller-supplied release/environment
// metadata, so two runs against the same provisioned node set produce
// identical identities.
func buildNodeIdentities(nodes []Node, releaseVersion, sourceHash, environmentHash string, launchTick uint64) []NodeIdentity {
	out := make([]NodeIdentity, len(nodes))
	for i, n := range nodes {
		out[i] = NodeIdentity{
			NodeID: n.ID, InstanceID: "instance-" + n.ID,
			// Region/AZ/ImageDigest are deterministically derived from the
			// node ID here because this package's providers (Simulated and
			// HTTP-local) have no real cloud placement to report; a real
			// deployment's NodeProvider would supply its own
			// provider-reported Region/AZ/ImageDigest instead of this
			// derivation — see HTTPNodeProvider's own doc comment on being
			// infra-agnostic.
			Region: "sim-region", AZ: "sim-az-" + fmt.Sprint(i%3),
			ImageDigest: "sha256:unset-no-real-image-in-this-provider",
			ReleaseVersion: releaseVersion, SourceHash: sourceHash, EnvironmentHash: environmentHash,
			LaunchTick: launchTick,
		}
	}
	return out
}

// RunLargeScaleQualification is external audit item A7's workload
// generator + qualification-controller entry point: provisions
// nodeCount nodes, generates envelopeCount deterministic
// REAL_DERIVED_BENCHMARK-labeled envelopes from seed (GenerateWorkload),
// distributes them round-robin, collects results, and reconciles
// (audit item A6's engine) submitted-vs-processed — the same discipline
// RunQualification already applies at smaller scale, extended with real
// node identity and reconciliation-grade accounting rather than the
// narrower IntegrityReport.
//
// This function can genuinely run at nodeCount=100/envelopeCount=1,000,000
// against SimulatedNodeProvider or HTTPNodeProvider — nothing in it is
// scale-limited — but doing so was NOT executed in this session (see
// docs/AUDIT_A1_A18_GAP_MAPPING.md's A7 entry): this environment has
// neither 100 real nodes nor the wall-clock budget to prove it here.
// What this session DID run is recorded honestly in the evidence bundle
// this command produces, at whatever scale it was actually run.
func RunLargeScaleQualification(ctx context.Context, contract *blockers.Contract, provider NodeProvider,
	nodeCount, envelopeCount int, seed uint64, releaseVersion, sourceHash, environmentHash string, launchTick uint64,
) (blockers.RunResult, LargeScaleReport, error) {
	if provider.Mode() == "REAL" {
		return blockers.RunResult{}, LargeScaleReport{}, errors.New("scale: RunLargeScaleQualification refuses a REAL-mode provider; submit that evidence through pkg/governance/qualification instead")
	}

	nodes, err := provider.Provision(ctx, nodeCount)
	if err != nil {
		return blockers.RunResult{}, LargeScaleReport{}, fmt.Errorf("scale: provision: %w", err)
	}
	defer func() { _ = provider.Destroy(ctx, nodes) }()

	workload := GenerateWorkload(seed, envelopeCount)

	start := time.Now()
	for i, env := range workload {
		node := nodes[i%len(nodes)]
		rec := EvidenceRecord{ID: env.Envelope.ID, Payload: env.Envelope.Payload}
		if err := provider.Submit(ctx, node, rec); err != nil {
			return blockers.RunResult{}, LargeScaleReport{}, fmt.Errorf("scale: submit %s: %w", rec.ID, err)
		}
	}

	processed, err := provider.Collect(ctx)
	if err != nil {
		return blockers.RunResult{}, LargeScaleReport{}, fmt.Errorf("scale: collect: %w", err)
	}
	elapsed := time.Since(start)

	processedIDs := make(map[string]bool, len(processed))
	for _, p := range processed {
		processedIDs[p.RecordID] = true
	}

	eng := reconciliation.NewEngine()
	for _, env := range workload {
		if !processedIDs[env.Envelope.ID] {
			continue // never reached a node -- correctly absent from InputCount, contributes to LostCount below
		}
		eng.Process(env.Envelope)
	}
	recon := eng.Finalize(envelopeCount)

	report := LargeScaleReport{
		NodeCount: nodeCount, EnvelopeCount: envelopeCount, Seed: seed,
		NodeIdentities: buildNodeIdentities(nodes, releaseVersion, sourceHash, environmentHash, launchTick),
		DataOrigin:     livedata.ModeRealDerivedBenchmark,
		Elapsed:        elapsed, ThroughputPerSec: float64(envelopeCount) / elapsed.Seconds(),
		Reconciliation: recon,
	}

	result := blockers.RunResult{
		BlockerID: contract.ID, Mode: provider.Mode(), Pass: report.Pass(),
		Measurements: map[string]string{
			"node_count":         fmt.Sprintf("%d", nodeCount),
			"envelope_count":     fmt.Sprintf("%d", envelopeCount),
			"seed":               fmt.Sprintf("%d", seed),
			"data_origin":        string(report.DataOrigin),
			"elapsed":            elapsed.String(),
			"throughput_per_s":   fmt.Sprintf("%.2f", report.ThroughputPerSec),
			"input_count":        fmt.Sprintf("%d", recon.InputCount),
			"accepted_count":     fmt.Sprintf("%d", recon.AcceptedCount),
			"lost_count":         fmt.Sprintf("%d", recon.LostCount),
			"duplicate_count":    fmt.Sprintf("%d", recon.DuplicateCount),
			"final_root":         recon.FinalRoot,
		},
	}
	if !report.Pass() {
		result.FailureReason = fmt.Sprintf("reconciliation invariants violated: %v", recon.Verify())
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, report, err
	}
	return result, report, nil
}
