// Package livedata is the live_data blocker's qualification machinery:
// a FeedConnector abstraction for commercial data sources (SWIFT, AIS,
// SAR, BoL), a normalization/dedup pipeline, and fixture connectors
// that produce clearly-provenanced synthetic or replayed records --
// never records tagged LIVE, which the pipeline refuses to accept from
// anything but a real connector. It qualifies the *ingestion and
// anti-replay logic*; the live_data blocker itself still requires the
// commercial data contracts and credentials that only a real
// procurement action can produce.
package livedata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"veriqo/pkg/blockers"
)

// DataMode is the provenance tag every record must carry honestly.
//
// External audit item A2 ("Data Origin Enforcement") requires the
// canonical 5-value enum LIVE_LICENSED / LIVE_CUSTOMER_OWNED /
// REAL_DERIVED_BENCHMARK / REPLAY / SYNTHETIC, with SYNTHETIC and REPLAY
// and REAL_DERIVED_BENCHMARK never silently promotable to LIVE, and
// REAL_DERIVED_BENCHMARK never silently promotable to "an independent
// real-world observation". ModeLive (the original 3-value enum's single
// LIVE tag) is kept, unmodified, as a backward-compatible alias so
// existing callers (test/e2e/eight_blockers's "lying connector" fixture)
// keep working unchanged — IsLive treats it as equivalent to the two new,
// more specific live variants.
type DataMode string

const (
	// ModeSynthetic is wholly fabricated test data.
	ModeSynthetic DataMode = "SYNTHETIC"
	// ModeReplay is a previously-seen real or synthetic record fed back
	// in, used specifically to exercise anti-replay defenses.
	ModeReplay DataMode = "REPLAY"
	// ModeRealDerivedBenchmark is real data that was resampled, seeded,
	// or otherwise mechanically derived from a genuine observation (e.g.
	// the A7 100-node workload generator's seeded envelopes) — real in
	// origin, but NOT itself an independent real-world observation. See
	// IsIndependentRealObservation.
	ModeRealDerivedBenchmark DataMode = "REAL_DERIVED_BENCHMARK"
	// ModeLiveLicensed is a genuine record from a live, licensed
	// commercial feed (e.g. a paid AIS/SWIFT data contract).
	ModeLiveLicensed DataMode = "LIVE_LICENSED"
	// ModeLiveCustomerOwned is a genuine record supplied directly by a
	// customer from their own operational systems (not a third-party
	// commercial feed).
	ModeLiveCustomerOwned DataMode = "LIVE_CUSTOMER_OWNED"
	// ModeLive is the original, less specific "genuine live record" tag,
	// kept only for backward compatibility with existing callers. New
	// code should use ModeLiveLicensed or ModeLiveCustomerOwned.
	ModeLive DataMode = "LIVE"
)

// IsLive reports whether m is any live-family tag (ModeLive,
// ModeLiveLicensed, or ModeLiveCustomerOwned) — the set of tags that
// assert "this record genuinely came from a real, currently-connected
// source", as opposed to SYNTHETIC/REPLAY/REAL_DERIVED_BENCHMARK, none
// of which may ever be treated as live (audit item A2's core rule:
// SYNTHETIC != LIVE, REPLAY != LIVE).
func IsLive(m DataMode) bool {
	switch m {
	case ModeLive, ModeLiveLicensed, ModeLiveCustomerOwned:
		return true
	default:
		return false
	}
}

// IsIndependentRealObservation reports whether m asserts a genuinely
// independent real-world observation — true only for the live-family
// tags. REAL_DERIVED_BENCHMARK is real in origin but mechanically
// derived/reseeded, so it is deliberately NOT independent (audit item
// A2's second rule: REAL_DERIVED_BENCHMARK != INDEPENDENT_REAL_OBSERVATION)
// — a benchmark run over 1,000,000 seeded envelopes must never be
// reported as "1,000,000 independent real-world observations".
func IsIndependentRealObservation(m DataMode) bool {
	return IsLive(m)
}

// Record is one unit of data from a feed, already carrying its
// provenance tag and a content hash computed by the connector that
// produced it.
type Record struct {
	ID        string
	Source    string // "SWIFT", "AIS", "SAR", "BoL"
	Payload   string
	DataMode  DataMode
	Timestamp time.Time
	Hash      string
}

// ComputeHash returns the canonical content hash for a record, over the
// fields that identify it as the same real-world event -- source,
// payload and timestamp, deliberately excluding DataMode and ID so a
// record replayed under a different mode or ID is still recognized as
// the same content for dedup purposes.
func ComputeHash(source, payload string, ts time.Time) string {
	sum := sha256.Sum256([]byte(source + "\x00" + payload + "\x00" + ts.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}

var errFeedExhausted = errors.New("livedata: feed exhausted")

// FeedConnector is one data source's connection. Real implementations
// authenticate to and stream from a genuine commercial feed;
// SyntheticConnector and ReplayConnector are fixtures.
type FeedConnector interface {
	Mode() string // "SIMULATED" or "REAL"
	Source() string
	Connect(ctx context.Context) error
	Authenticate(ctx context.Context, credentials string) error
	// Receive returns the next record, or errFeedExhausted when the
	// fixture's supply is empty.
	Receive(ctx context.Context) (Record, error)
	Close(ctx context.Context) error
}

// SyntheticConnector produces wholly fabricated records for one source,
// always tagged ModeSynthetic.
type SyntheticConnector struct {
	source    string
	records   []Record
	pos       int
	connected bool
	authed    bool
}

// NewSyntheticConnector builds a connector for source that will emit n
// synthetic records when drained via Receive.
func NewSyntheticConnector(source string, n int) *SyntheticConnector {
	records := make([]Record, n)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		payload := fmt.Sprintf("%s-synthetic-payload-%04d", source, i)
		records[i] = Record{
			ID: fmt.Sprintf("%s-%04d", source, i), Source: source, Payload: payload,
			DataMode: ModeSynthetic, Timestamp: ts, Hash: ComputeHash(source, payload, ts),
		}
	}
	return &SyntheticConnector{source: source, records: records}
}

func (c *SyntheticConnector) Mode() string   { return "SIMULATED" }
func (c *SyntheticConnector) Source() string { return c.source }

func (c *SyntheticConnector) Connect(ctx context.Context) error {
	c.connected = true
	return nil
}

func (c *SyntheticConnector) Authenticate(ctx context.Context, credentials string) error {
	if !c.connected {
		return errors.New("livedata: Authenticate called before Connect")
	}
	if credentials == "" {
		return errors.New("livedata: empty credentials rejected")
	}
	c.authed = true
	return nil
}

func (c *SyntheticConnector) Receive(ctx context.Context) (Record, error) {
	if !c.authed {
		return Record{}, errors.New("livedata: Receive called before Authenticate")
	}
	if c.pos >= len(c.records) {
		return Record{}, errFeedExhausted
	}
	rec := c.records[c.pos]
	c.pos++
	return rec, nil
}

func (c *SyntheticConnector) Close(ctx context.Context) error { c.connected = false; return nil }

// ReplayConnector wraps a fixed set of records and re-emits them tagged
// ModeReplay, deliberately simulating a replay attack (or a legitimate
// backfill) so the pipeline's anti-replay defense can be exercised
// against something other than the happy path.
type ReplayConnector struct {
	source    string
	records   []Record
	pos       int
	connected bool
	authed    bool
}

// NewReplayConnector re-emits the given records with DataMode forced to
// ModeReplay, regardless of what mode they originally carried.
func NewReplayConnector(source string, records []Record) *ReplayConnector {
	replayed := make([]Record, len(records))
	for i, r := range records {
		r.DataMode = ModeReplay
		replayed[i] = r
	}
	return &ReplayConnector{source: source, records: replayed}
}

func (c *ReplayConnector) Mode() string   { return "SIMULATED" }
func (c *ReplayConnector) Source() string { return c.source }
func (c *ReplayConnector) Connect(ctx context.Context) error {
	c.connected = true
	return nil
}
func (c *ReplayConnector) Authenticate(ctx context.Context, credentials string) error {
	if !c.connected {
		return errors.New("livedata: Authenticate called before Connect")
	}
	c.authed = true
	return nil
}
func (c *ReplayConnector) Receive(ctx context.Context) (Record, error) {
	if !c.authed {
		return Record{}, errors.New("livedata: Receive called before Authenticate")
	}
	if c.pos >= len(c.records) {
		return Record{}, errFeedExhausted
	}
	rec := c.records[c.pos]
	c.pos++
	return rec, nil
}
func (c *ReplayConnector) Close(ctx context.Context) error { c.connected = false; return nil }

// Pipeline normalizes and dedups incoming records. It refuses any
// record whose DataMode is empty or LIVE-but-produced-by-a-SIMULATED-
// mode connector, and rejects duplicates by content hash regardless of
// what DataMode the duplicate claims -- that is precisely what a
// replay attack looks like.
type Pipeline struct {
	mu       sync.Mutex
	seenHash map[string]bool
	Accepted []Record
	Rejected []RejectedRecord
}

// RejectedRecord is a record the pipeline refused, with why.
type RejectedRecord struct {
	Record Record
	Reason string
}

func NewPipeline() *Pipeline {
	return &Pipeline{seenHash: make(map[string]bool)}
}

// EnforceLiveProvenance is the one provenance rule this whole blocker
// exists to make impossible to violate: content tagged with any
// live-family mode (see IsLive) must never have been produced by
// anything other than a REAL-mode producer, and content with no
// provenance tag at all is refused outright rather than defaulted to
// anything. It is deliberately generic over "what produced this" (a
// livedata.FeedConnector's Mode(), or any other producer's equivalent
// REAL/SIMULATED self-report) so other packages that need to enforce
// this exact rule against their own data-origin-tagged types call this
// instead of reimplementing it a second time -- see
// pkg/evidenceorigin.ValidateClaim, which wraps this directly.
func EnforceLiveProvenance(mode DataMode, producerMode string) error {
	if mode == "" {
		return errors.New("missing data_mode provenance tag")
	}
	if IsLive(mode) && producerMode != "REAL" {
		return fmt.Errorf("a SIMULATED-mode connector must never produce a record tagged %s", mode)
	}
	return nil
}

// Ingest validates, normalizes and dedups one record. connectorMode is
// the producing connector's Mode() ("SIMULATED" or "REAL") -- passed
// explicitly so the pipeline can refuse a fixture that lies about being
// LIVE, which is exactly the failure mode this whole blocker exists to
// prevent.
func (p *Pipeline) Ingest(rec Record, connectorMode string) (accepted bool, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := EnforceLiveProvenance(rec.DataMode, connectorMode); err != nil {
		return p.reject(rec, err.Error())
	}
	wantHash := ComputeHash(rec.Source, rec.Payload, rec.Timestamp)
	if rec.Hash != wantHash {
		return p.reject(rec, "content hash does not match recomputed hash")
	}
	if p.seenHash[rec.Hash] {
		return p.reject(rec, "duplicate content hash (replay)")
	}
	p.seenHash[rec.Hash] = true
	p.Accepted = append(p.Accepted, rec)
	return true, ""
}

func (p *Pipeline) reject(rec Record, reason string) (bool, string) {
	p.Rejected = append(p.Rejected, RejectedRecord{Record: rec, Reason: reason})
	return false, reason
}

// drain reads every record from a connector into the pipeline.
func drain(ctx context.Context, conn FeedConnector, pipeline *Pipeline, credentials string) error {
	if err := conn.Connect(ctx); err != nil {
		return fmt.Errorf("livedata: connect %s: %w", conn.Source(), err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.Authenticate(ctx, credentials); err != nil {
		return fmt.Errorf("livedata: authenticate %s: %w", conn.Source(), err)
	}
	for {
		rec, err := conn.Receive(ctx)
		if errors.Is(err, errFeedExhausted) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("livedata: receive from %s: %w", conn.Source(), err)
		}
		pipeline.Ingest(rec, conn.Mode())
	}
}

// RunQualification drains every connector into one pipeline, then
// replays every accepted record back through it via a ReplayConnector
// to prove the anti-replay defense actually rejects a genuine repeat,
// and records the outcome on contract.
func RunQualification(ctx context.Context, contract *blockers.Contract, connectors []FeedConnector) (blockers.RunResult, error) {
	for _, c := range connectors {
		if c.Mode() == "REAL" {
			return blockers.RunResult{}, fmt.Errorf("livedata: RunQualification refuses REAL-mode connector %s; submit that evidence through pkg/governance/qualification instead", c.Source())
		}
	}

	pipeline := NewPipeline()
	for _, conn := range connectors {
		if err := drain(ctx, conn, pipeline, "fixture-credentials"); err != nil {
			return blockers.RunResult{}, err
		}
	}
	firstPassAccepted := len(pipeline.Accepted)
	firstPassRejected := len(pipeline.Rejected)

	replay := NewReplayConnector("replay-of-accepted", pipeline.Accepted)
	if err := drain(ctx, replay, pipeline, "fixture-credentials"); err != nil {
		return blockers.RunResult{}, err
	}
	replayAccepted := len(pipeline.Accepted) - firstPassAccepted
	replayRejected := len(pipeline.Rejected) - firstPassRejected

	everyAccepted := true
	for _, rec := range pipeline.Accepted[:firstPassAccepted] {
		if IsLive(rec.DataMode) {
			everyAccepted = false
		}
	}

	pass := firstPassAccepted > 0 && replayAccepted == 0 && replayRejected == firstPassAccepted && everyAccepted
	result := blockers.RunResult{
		BlockerID: contract.ID,
		Mode:      "FIXTURE",
		Pass:      pass,
		Measurements: map[string]string{
			"connectors":          fmt.Sprintf("%d", len(connectors)),
			"first_pass_accepted": fmt.Sprintf("%d", firstPassAccepted),
			"first_pass_rejected": fmt.Sprintf("%d", firstPassRejected),
			"replay_accepted":     fmt.Sprintf("%d", replayAccepted),
			"replay_rejected":     fmt.Sprintf("%d", replayRejected),
		},
	}
	if !pass {
		result.FailureReason = "anti-replay defense did not reject every replayed record, or a fixture record was tagged LIVE"
	}

	if err := contract.RecordFixtureRun(result); err != nil {
		return result, err
	}
	return result, nil
}
