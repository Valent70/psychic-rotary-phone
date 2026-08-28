// Package scale is the scale_qualification blocker's qualification
// machinery: a NodeProvider abstraction plus a workload runner that
// distributes evidence records across nodes and verifies exactly-once
// delivery, throughput, and convergence. SimulatedNodeProvider is a
// goroutine-based fixture (mode "SIMULATED") standing in for the real
// 100-physical/cloud-node deployment the scale_qualification blocker
// ultimately requires. HTTPNodeProvider is the promised "provider
// swap, not a rewrite": it satisfies the identical NodeProvider
// interface but Submit/Collect are real HTTP calls to real,
// independently-running cmd/veriqo-scale-node processes -- this
// package stays infra-agnostic (it does not itself provision
// machines or containers, matching pkg/blockers/dr's RegionProvider
// pattern), so HTTPNodeProvider works unmodified whether those
// processes are Docker containers on one host or real nodes spread
// across real datacenters; only the addresses handed to it change.
package scale

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"veriqo/pkg/blockers"
	"veriqo/pkg/blockers/pentest"
)

// Node identifies one participant in a scale run.
type Node struct {
	ID string
}

// EvidenceRecord is one unit of work distributed to a node. In a real
// deployment this is a genuine evidence record; here it is a synthetic
// stand-in with the same shape. Origin names the workload's real
// submitter/source (PART 11's "workload origin" requirement) -- e.g.
// which upstream feed or generator produced this record. It is
// optional: a zero-value Origin is a real, honest "not recorded" fact
// for callers (like RunQualification's own synthetic workload) that
// have no meaningful origin to report, not a fabricated placeholder.
type EvidenceRecord struct {
	ID      string
	Payload string
	Origin  string
}

// NodeProvider provisions nodes, accepts evidence submissions addressed
// to a specific node, and reports back everything every node actually
// processed. Real implementations run on physical or cloud nodes;
// SimulatedNodeProvider runs each node as a goroutine.
type NodeProvider interface {
	// Provision brings up n nodes and returns their identities.
	Provision(ctx context.Context, n int) ([]Node, error)
	// Submit hands one evidence record to a specific node for processing.
	Submit(ctx context.Context, node Node, rec EvidenceRecord) error
	// Collect blocks until all submitted records have been processed by
	// their nodes (or ctx expires) and returns every processed record
	// together with the ID of the node that processed it.
	Collect(ctx context.Context) ([]ProcessedRecord, error)
	// Destroy tears the nodes down.
	Destroy(ctx context.Context, nodes []Node) error
	// Mode identifies how this provider produces its nodes -- "SIMULATED"
	// or "REAL". RunQualification refuses to record a REAL-mode result;
	// that path belongs to pkg/governance/qualification.
	Mode() string
}

// ProcessedRecord is what a node reports back after handling a record.
type ProcessedRecord struct {
	NodeID     string
	RecordID   string
	ReceivedAt time.Time
}

// SimulatedNodeProvider runs each node as its own goroutine reading from
// a dedicated inbox channel. It is a genuine concurrent system --
// records really are distributed across independently scheduled
// goroutines and really can race -- but it runs on one process, so it
// qualifies the *distribution and integrity-checking logic*, not real
// multi-machine network/failure behavior.
//
// Processed records accumulate directly into a mutex-protected slice
// (collected) rather than a second channel: an earlier version funneled
// every node's completions through one shared, fixed-capacity
// (1<<16) `processed` channel that nothing drained until Collect ran --
// which only happens AFTER every Submit call in a large run has
// already returned. Past that channel's capacity, every node's send to
// it blocked, which stalled that node's inbox drain, which in turn
// blocked Submit once the (also fixed-capacity) inboxes filled --  a
// real deadlock, not a slow path: reproduced directly via
// TestScaleQualification100Nodes1MEnvelopes, which hung for the
// entirety of its 5-minute test timeout with near-zero CPU time
// (blocked, not working) before this fix. An unbounded slice under one
// mutex has no such capacity ceiling to exceed.
type SimulatedNodeProvider struct {
	mu        sync.Mutex
	inboxes   map[string]chan EvidenceRecord
	collected []ProcessedRecord
	wg        sync.WaitGroup
}

// NewSimulatedNodeProvider constructs an empty provider. Call Provision
// before submitting work.
func NewSimulatedNodeProvider() *SimulatedNodeProvider {
	return &SimulatedNodeProvider{
		inboxes: make(map[string]chan EvidenceRecord),
	}
}

func (p *SimulatedNodeProvider) Mode() string { return "SIMULATED" }

// Provision starts n goroutines, each with its own buffered inbox.
func (p *SimulatedNodeProvider) Provision(ctx context.Context, n int) ([]Node, error) {
	if n <= 0 {
		return nil, errors.New("scale: node count must be positive")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := make([]Node, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("sim-node-%04d", i)
		inbox := make(chan EvidenceRecord, 4096)
		p.inboxes[id] = inbox
		nodes = append(nodes, Node{ID: id})
		p.wg.Add(1)
		go p.runNode(ctx, id, inbox)
	}
	return nodes, nil
}

func (p *SimulatedNodeProvider) runNode(ctx context.Context, id string, inbox chan EvidenceRecord) {
	defer p.wg.Done()
	for {
		select {
		case rec, ok := <-inbox:
			if !ok {
				return
			}
			p.mu.Lock()
			p.collected = append(p.collected, ProcessedRecord{NodeID: id, RecordID: rec.ID, ReceivedAt: time.Now()})
			p.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// Submit routes rec to node's inbox. Real network partition/loss
// behavior is out of scope for a single-process fixture; the fakeProvider
// in this package's tests exercises loss/duplication detection directly
// instead.
func (p *SimulatedNodeProvider) Submit(ctx context.Context, node Node, rec EvidenceRecord) error {
	p.mu.Lock()
	inbox, ok := p.inboxes[node.ID]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("scale: unknown node %q", node.ID)
	}
	select {
	case inbox <- rec:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Collect closes every inbox once queued work has drained, waits for
// all node goroutines to exit, and returns everything they processed.
func (p *SimulatedNodeProvider) Collect(ctx context.Context) ([]ProcessedRecord, error) {
	p.mu.Lock()
	for _, inbox := range p.inboxes {
		close(inbox)
	}
	p.mu.Unlock()

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	p.mu.Lock()
	out := append([]ProcessedRecord(nil), p.collected...)
	p.mu.Unlock()
	return out, nil
}

// Destroy is a no-op for the simulated provider: goroutines already
// exited in Collect. A real provider would deprovision machines here.
func (p *SimulatedNodeProvider) Destroy(ctx context.Context, nodes []Node) error { return nil }

// httpProcessedRecord mirrors cmd/veriqo-scale-node's wire format for
// one entry in a GET /collect response.
type httpProcessedRecord struct {
	NodeID     string    `json:"node_id"`
	RecordID   string    `json:"record_id"`
	ReceivedAt time.Time `json:"received_at"`
}

// HTTPNodeProvider drives real cmd/veriqo-scale-node processes over
// real HTTP. It does not provision or destroy the processes themselves
// -- callers point it at addresses of already-running nodes (real
// Docker containers, real VMs, whatever), matching pkg/blockers/dr's
// RegionProvider split between "the consensus/logic abstraction" and
// "who actually stands up the infrastructure".
type HTTPNodeProvider struct {
	// NodeAddrs maps a node ID to its base URL, e.g.
	// "http://127.0.0.1:18090". Every ID Provision(ctx, n) is asked
	// for must have an entry here.
	NodeAddrs map[string]string
	Token     string
	Client    *http.Client
}

func (p *HTTPNodeProvider) Mode() string { return "REAL" }

func (p *HTTPNodeProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// Provision returns the first n node IDs from NodeAddrs, sorted for
// determinism. It does not start any process -- every address in
// NodeAddrs must already be a live, reachable veriqo-scale-node.
func (p *HTTPNodeProvider) Provision(ctx context.Context, n int) ([]Node, error) {
	if n <= 0 {
		return nil, errors.New("scale: node count must be positive")
	}
	if len(p.NodeAddrs) < n {
		return nil, fmt.Errorf("scale: HTTPNodeProvider configured with %d node addresses, need %d", len(p.NodeAddrs), n)
	}
	ids := make([]string, 0, len(p.NodeAddrs))
	for id := range p.NodeAddrs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodes := make([]Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = Node{ID: ids[i]}
	}
	return nodes, nil
}

// Submit POSTs rec as JSON to node's real /submit endpoint.
func (p *HTTPNodeProvider) Submit(ctx context.Context, node Node, rec EvidenceRecord) error {
	addr, ok := p.NodeAddrs[node.ID]
	if !ok {
		return fmt.Errorf("scale: unknown node %q", node.ID)
	}
	body, err := json.Marshal(EvidenceRecord{ID: rec.ID, Payload: rec.Payload, Origin: rec.Origin})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/submit", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("scale: submit to %s: %w", node.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("scale: submit to %s: status %d: %s", node.ID, resp.StatusCode, b)
	}
	return nil
}

// Collect GETs /collect from every configured node in turn and
// aggregates their real, independently-reported results.
func (p *HTTPNodeProvider) Collect(ctx context.Context) ([]ProcessedRecord, error) {
	var out []ProcessedRecord
	for id, addr := range p.NodeAddrs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/collect", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+p.Token)
		resp, err := p.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("scale: collect from %s: %w", id, err)
		}
		var recs []httpProcessedRecord
		decErr := json.NewDecoder(resp.Body).Decode(&recs)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("scale: collect from %s: status %d", id, resp.StatusCode)
		}
		if decErr != nil {
			return nil, fmt.Errorf("scale: collect from %s: decode: %w", id, decErr)
		}
		for _, r := range recs {
			out = append(out, ProcessedRecord(r))
		}
	}
	return out, nil
}

// Destroy is a no-op: this provider never owned the processes' lifecycle.
func (p *HTTPNodeProvider) Destroy(ctx context.Context, nodes []Node) error { return nil }

// DuplicatePolicy names this package's explicit, tested behavior when a
// record is processed more than once. PolicyReject -- the only policy
// this package currently implements -- fails the run: a duplicate
// almost always signals a real correctness bug (a queue's at-least-once
// redelivery the processing layer failed to deduplicate, or two nodes
// racing to claim the same record), not a benign event to silently
// paper over. Naming the policy explicitly, even with one member, keeps
// "we reject duplicates" a stated design decision rather than an
// accident of checkIntegrity's original loop structure.
type DuplicatePolicy string

// PolicyReject is this package's implemented DuplicatePolicy: any
// record seen more times than it was submitted fails the run's
// integrity check (see IntegrityReport.Clean and RunQualification).
const PolicyReject DuplicatePolicy = "REJECT"

// IntegrityReport is the result of comparing what was submitted against
// what was actually processed.
type IntegrityReport struct {
	Submitted  int
	Processed  int
	Lost       []string
	Duplicated []string
	// Unauthorized lists any NodeID processed records were attributed to
	// that was never among the nodes this run actually provisioned --
	// PART 11's "zero unauthorized acceptance" requirement. checkIntegrity
	// itself never populates this (it has no registered-node list to
	// check against); RunQualification fills it in via checkAuthorization
	// once it has both the provisioned node set and the processed
	// records.
	Unauthorized []string
	// Policy names the duplicate-handling policy Clean()'s Duplicated
	// check enforces -- always PolicyReject today (see DuplicatePolicy's
	// own doc comment).
	Policy DuplicatePolicy
	// MerkleRoot is a deterministic aggregate hash over the reconciled
	// (RecordID, NodeID) processed-record set, sorted by RecordID so the
	// result depends only on WHAT was processed, not the order Collect
	// happened to return it in. Changes whenever the underlying data
	// changes -- see merkleRoot's own doc comment.
	MerkleRoot string
	// InputLedgerHash is a hash-chained, append-only ledger over every
	// record ID submitted, in submission order.
	InputLedgerHash string
	// OutputLedgerHash is a hash-chained, append-only ledger over every
	// processed record actually collected, in collection order.
	OutputLedgerHash string
}

func (r IntegrityReport) Clean() bool {
	return len(r.Lost) == 0 && len(r.Duplicated) == 0 && len(r.Unauthorized) == 0
}

// checkIntegrity compares submitted record IDs against processed ones,
// and builds the Merkle root and both hash-chained ledgers required by
// PART 11's evidence requirements over that same submitted/processed
// data. It does not check node authorization -- see checkAuthorization,
// which RunQualification runs separately once it has the provisioned
// node set.
func checkIntegrity(submittedIDs []string, processed []ProcessedRecord) IntegrityReport {
	want := make(map[string]int, len(submittedIDs))
	for _, id := range submittedIDs {
		want[id]++
	}
	seen := make(map[string]int, len(processed))
	for _, p := range processed {
		seen[p.RecordID]++
	}
	report := IntegrityReport{
		Submitted: len(submittedIDs), Processed: len(processed), Policy: PolicyReject,
		MerkleRoot: merkleRoot(processed), InputLedgerHash: ledgerHash(submittedIDs),
		OutputLedgerHash: ledgerHash(outputLedgerEntries(processed)),
	}
	for id, wantCount := range want {
		gotCount := seen[id]
		if gotCount == 0 {
			report.Lost = append(report.Lost, id)
		} else if gotCount > wantCount {
			report.Duplicated = append(report.Duplicated, id)
		}
	}
	return report
}

// checkAuthorization scans processed against the set of node IDs
// provider.Provision actually returned for this run -- the ONLY nodes
// authorized to submit evidence -- and reports any processed record
// whose NodeID is not among them. A NodeProvider that lets an
// unregistered node's submission through (or misreports which node
// processed a record) is caught here even though checkIntegrity's own
// lost/duplicate accounting, which only tracks RecordID, would not
// otherwise notice.
func checkAuthorization(nodes []Node, processed []ProcessedRecord) []string {
	registered := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		registered[n.ID] = true
	}
	seen := make(map[string]bool)
	var unauthorized []string
	for _, p := range processed {
		if !registered[p.NodeID] && !seen[p.NodeID] {
			seen[p.NodeID] = true
			unauthorized = append(unauthorized, p.NodeID)
		}
	}
	return unauthorized
}

// merkleRoot computes a real Merkle tree root (stdlib crypto/sha256
// only) over recs, sorted by RecordID first so the result is a pure
// function of WHAT was processed rather than the order Collect
// happened to return it -- which, for the goroutine-based simulated
// provider especially, is itself a genuine concurrent race and
// therefore not meaningful evidence to hash order-sensitively. An odd
// node at any level is promoted unchanged to the next level rather than
// duplicated, so the root is well-defined for any non-empty input size.
func merkleRoot(recs []ProcessedRecord) string {
	if len(recs) == 0 {
		return ""
	}
	sorted := append([]ProcessedRecord(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RecordID < sorted[j].RecordID })

	leaves := make([][]byte, len(sorted))
	for i, r := range sorted {
		h := sha256.Sum256([]byte(fmt.Sprintf("veriqo.scale.merkle-leaf/v1|%s|%s", r.RecordID, r.NodeID)))
		leaves[i] = h[:]
	}
	for len(leaves) > 1 {
		next := make([][]byte, 0, (len(leaves)+1)/2)
		for i := 0; i < len(leaves); i += 2 {
			if i+1 < len(leaves) {
				pair := append(append([]byte{}, leaves[i]...), leaves[i+1]...)
				sum := sha256.Sum256(pair)
				next = append(next, sum[:])
			} else {
				next = append(next, leaves[i])
			}
		}
		leaves = next
	}
	return hex.EncodeToString(leaves[0])
}

// ledgerHash builds a hash-chained, append-only ledger over entries in
// the given order and returns the final chain hash -- the same
// technique pkg/blockers/soak's checkpoint chain uses: altering or
// reordering any entry changes the final hash.
func ledgerHash(entries []string) string {
	prev := ""
	for _, e := range entries {
		sum := sha256.Sum256([]byte(prev + "|" + e))
		prev = hex.EncodeToString(sum[:])
	}
	return prev
}

// outputLedgerEntries renders each processed record as one ledger
// entry, in the order Collect returned them -- a real, if racy across
// providers, record of collection EVENTS (as opposed to merkleRoot's
// order-independent fingerprint of the reconciled DATA).
func outputLedgerEntries(processed []ProcessedRecord) []string {
	out := make([]string, len(processed))
	for i, p := range processed {
		out[i] = fmt.Sprintf("%s|%s|%d", p.RecordID, p.NodeID, p.ReceivedAt.UnixNano())
	}
	return out
}

// Reconciliation is scale's final cross-check over one qualification
// run: does every provisioned node show up in the processed set, and
// is the run's integrity report clean end to end.
type Reconciliation struct {
	NodeCount          int
	NodesWithNoTraffic []string
	RecordsReconciled  int
	MerkleRoot         string
	Consistent         bool
	Detail             string
}

func reconcile(nodes []Node, submittedIDs []string, processed []ProcessedRecord, report IntegrityReport) Reconciliation {
	sawTraffic := make(map[string]bool, len(nodes))
	for _, p := range processed {
		sawTraffic[p.NodeID] = true
	}
	var idle []string
	for _, n := range nodes {
		if !sawTraffic[n.ID] {
			idle = append(idle, n.ID)
		}
	}

	consistent := report.Clean() && report.Processed == len(submittedIDs)
	detail := "reconciled: every submitted record accounted for exactly once by an authorized node"
	if !consistent {
		detail = fmt.Sprintf("reconciliation failed: submitted=%d processed=%d lost=%d duplicated=%d unauthorized=%d",
			len(submittedIDs), report.Processed, len(report.Lost), len(report.Duplicated), len(report.Unauthorized))
	}
	return Reconciliation{
		NodeCount: len(nodes), NodesWithNoTraffic: idle, RecordsReconciled: report.Processed,
		MerkleRoot: report.MerkleRoot, Consistent: consistent, Detail: detail,
	}
}

// RunQualification distributes recordCount evidence records round-robin
// across nodeCount nodes provisioned from provider, waits for
// processing, checks integrity (loss, duplication, and unauthorized
// acceptance), computes a Merkle root and both ledgers over the
// reconciled dataset, reconciles per-node coverage, resolves this
// build's release identity (via pkg/blockers/pentest's already-audited
// Preflight, not a second copy of that git/source-hash resolution
// logic), measures throughput, and records the outcome on contract via
// RecordFixtureRun. It refuses to run against a provider whose Mode()
// is "REAL" -- that evidence belongs in pkg/governance/qualification,
// not here.
//
// Honest scope: nodeCount/recordCount are the CALLER's own acceptance
// parameters -- this function will run at nodeCount=100,
// recordCount=1_000_000 against SimulatedNodeProvider exactly as
// readily as it runs at 8/2000 (see TestScaleQualification100Nodes1MEnvelopes),
// but doing so exercises this package's OWN distribution and
// integrity-checking logic on one process's goroutines, not real
// 100-node infrastructure. That remains open; a provider whose Mode()
// reports "REAL" is refused above specifically so this simulated path
// can never be mistaken for it.
func RunQualification(ctx context.Context, contract *blockers.Contract, provider NodeProvider, nodeCount, recordCount int) (blockers.RunResult, error) {
	if provider.Mode() == "REAL" {
		return blockers.RunResult{}, errors.New("scale: RunQualification refuses a REAL-mode provider; submit that evidence through pkg/governance/qualification instead")
	}

	nodes, err := provider.Provision(ctx, nodeCount)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("scale: provision: %w", err)
	}
	defer func() { _ = provider.Destroy(ctx, nodes) }()

	submittedIDs := make([]string, 0, recordCount)
	start := time.Now()
	for i := 0; i < recordCount; i++ {
		id := fmt.Sprintf("ev-%08d", i)
		node := nodes[i%len(nodes)]
		rec := EvidenceRecord{ID: id, Payload: "synthetic", Origin: "scale.RunQualification.synthetic-workload"}
		if err := provider.Submit(ctx, node, rec); err != nil {
			return blockers.RunResult{}, fmt.Errorf("scale: submit %s: %w", id, err)
		}
		submittedIDs = append(submittedIDs, id)
	}

	processed, err := provider.Collect(ctx)
	if err != nil {
		return blockers.RunResult{}, fmt.Errorf("scale: collect: %w", err)
	}
	elapsed := time.Since(start)

	report := checkIntegrity(submittedIDs, processed)
	report.Unauthorized = checkAuthorization(nodes, processed)
	reconciliation := reconcile(nodes, submittedIDs, processed, report)

	release, releaseErr := pentest.Preflight(ctx, ".")

	result := blockers.RunResult{
		BlockerID: contract.ID,
		Mode:      provider.Mode(),
		Pass:      report.Clean() && reconciliation.Consistent,
		Measurements: map[string]string{
			"node_count":                  fmt.Sprintf("%d", nodeCount),
			"records_submitted":           fmt.Sprintf("%d", report.Submitted),
			"records_processed":           fmt.Sprintf("%d", report.Processed),
			"elapsed":                     elapsed.String(),
			"throughput_per_s":            fmt.Sprintf("%.2f", float64(recordCount)/elapsed.Seconds()),
			"unauthorized_count":          fmt.Sprintf("%d", len(report.Unauthorized)),
			"duplicate_policy":            string(report.Policy),
			"merkle_root":                 report.MerkleRoot,
			"input_ledger_hash":           report.InputLedgerHash,
			"output_ledger_hash":          report.OutputLedgerHash,
			"reconciliation_consistent":   fmt.Sprintf("%v", reconciliation.Consistent),
			"nodes_with_no_traffic_count": fmt.Sprintf("%d", len(reconciliation.NodesWithNoTraffic)),
		},
	}
	if releaseErr == nil {
		result.Measurements["release_commit"] = release.Commit
		result.Measurements["release_source_hash"] = release.SourceHash
		result.Measurements["release_identity_source"] = release.IdentitySource
	} else {
		result.Measurements["release_identity_error"] = releaseErr.Error()
	}
	if !result.Pass {
		result.FailureReason = fmt.Sprintf("lost=%d duplicated=%d unauthorized=%d reconciled=%v",
			len(report.Lost), len(report.Duplicated), len(report.Unauthorized), reconciliation.Consistent)
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}
