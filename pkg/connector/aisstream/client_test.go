package aisstream

// All fixtures below are hand-written, synthetic, illustrative AIS-
// message-shaped JSON — they follow this package's own wireMessage
// schema (see message.go's doc comment), NOT a claim of byte-for-byte
// fidelity to any real aisstream.io payload, and were never captured
// from a real feed. Every test in this file runs against fakeTransport
// / fakeStream below: no test in this package dials a real network
// address.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
)

// --- fakes -----------------------------------------------------------

type staticSecretProvider struct {
	key string
	err error
}

func (s staticSecretProvider) APIKey() (string, error) { return s.key, s.err }

// fakeStream is an in-memory MessageStream: it serves a fixed queue of
// messages, then blocks on ctx.Done() once exhausted (simulating an
// idle-but-open connection) unless closed first.
type fakeStream struct {
	mu       sync.Mutex
	messages [][]byte
	idx      int
	writes   [][]byte
	closed   bool
}

func newFakeStream(messages ...[]byte) *fakeStream {
	return &fakeStream{messages: messages}
}

func (f *fakeStream) ReadMessage(ctx context.Context) ([]byte, error) {
	f.mu.Lock()
	if f.idx < len(f.messages) {
		m := f.messages[f.idx]
		f.idx++
		f.mu.Unlock()
		return m, nil
	}
	f.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeStream) WriteMessage(_ context.Context, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("fakeStream: write after close")
	}
	f.writes = append(f.writes, append([]byte(nil), data...))
	return nil
}

func (f *fakeStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeStream) Writes() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.writes...)
}

// fakeTransport fails its first failuresRemaining Connect calls, then
// succeeds by invoking streamFactory for every call thereafter.
type fakeTransport struct {
	mu                sync.Mutex
	failuresRemaining int
	streamFactory     func() MessageStream
	connectCalls      int
	lastURL           string
}

func (f *fakeTransport) Connect(_ context.Context, url string) (MessageStream, error) {
	f.mu.Lock()
	f.connectCalls++
	f.lastURL = url
	if f.failuresRemaining > 0 {
		f.failuresRemaining--
		f.mu.Unlock()
		return nil, errors.New("fake dial failure")
	}
	f.mu.Unlock()
	return f.streamFactory(), nil
}

func (f *fakeTransport) ConnectCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectCalls
}

// collectingHandler implements Handler, recording every callback for
// assertions. Accepted/Filtered/Rejected/Heartbeat are also
// broadcastable via channels for tests that need to synchronize on a
// specific count without polling.
type collectingHandler struct {
	mu         sync.Mutex
	accepted   []NormalizedEvent
	evidences  []provenance.ExternalEvidence
	filtered   []provenance.ExternalEvidence
	filterWhys []string
	rejected   [][]byte
	rejectErrs []error
	heartbeats []int64

	acceptedCh chan struct{} // signaled once per HandleAccepted, buffered
}

func newCollectingHandler(bufferedAccepts int) *collectingHandler {
	return &collectingHandler{acceptedCh: make(chan struct{}, bufferedAccepts)}
}

func (h *collectingHandler) HandleAccepted(event NormalizedEvent, evidence provenance.ExternalEvidence) {
	h.mu.Lock()
	h.accepted = append(h.accepted, event)
	h.evidences = append(h.evidences, evidence)
	h.mu.Unlock()
	select {
	case h.acceptedCh <- struct{}{}:
	default:
	}
}

func (h *collectingHandler) HandleFiltered(evidence provenance.ExternalEvidence, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.filtered = append(h.filtered, evidence)
	h.filterWhys = append(h.filterWhys, reason)
}

func (h *collectingHandler) HandleRejected(raw []byte, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rejected = append(h.rejected, append([]byte(nil), raw...))
	h.rejectErrs = append(h.rejectErrs, err)
}

func (h *collectingHandler) HandleHeartbeat(observedAt int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.heartbeats = append(h.heartbeats, observedAt)
}

func (h *collectingHandler) snapshot() (accepted int, filtered int, rejected int, heartbeats int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.accepted), len(h.filtered), len(h.rejected), len(h.heartbeats)
}

// --- synthetic fixtures (see file-level comment) ----------------------

func mustJSON(t *testing.T, v map[string]interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustJSON: %v", err)
	}
	return raw
}

func positionReportFixture(mmsi int64, lat, lon float64, ts int64) map[string]interface{} {
	return map[string]interface{}{
		"MessageType":        "PositionReport",
		"MMSI":               mmsi,
		"IMO":                9123456,
		"CallSign":           "TEST1",
		"ShipName":           "MV SYNTHETIC EXAMPLE",
		"Latitude":           lat,
		"Longitude":          lon,
		"Cog":                87.3,
		"Sog":                12.1,
		"NavigationalStatus": 0,
		"Destination":        "SINGAPORE",
		"TimestampUTC":       ts,
	}
}

func heartbeatFixture(ts int64) map[string]interface{} {
	return map[string]interface{}{
		"MessageType":  messageTypeHeartbeat,
		"TimestampUTC": ts,
	}
}

// --- config validation -------------------------------------------------

func validTestConfig() Config {
	return Config{
		URL:               "wss://example.invalid/stream",
		InitialBackoff:    10 * time.Millisecond,
		MaxBackoff:        100 * time.Millisecond,
		BackoffMultiplier: 2.0,
		ProviderID:        "test-provider",
		DatasetID:         "test-dataset",
		SourceID:          "test-source",
	}
}

func TestConfigValidate_RejectsBadConfig(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(Config) Config
	}{
		{"empty_url", func(c Config) Config { c.URL = ""; return c }},
		{"empty_provider_id", func(c Config) Config { c.ProviderID = ""; return c }},
		{"empty_dataset_id", func(c Config) Config { c.DatasetID = ""; return c }},
		{"empty_source_id", func(c Config) Config { c.SourceID = ""; return c }},
		{"zero_initial_backoff", func(c Config) Config { c.InitialBackoff = 0; return c }},
		{"max_less_than_initial", func(c Config) Config { c.MaxBackoff = c.InitialBackoff / 2; return c }},
		{"multiplier_too_small", func(c Config) Config { c.BackoffMultiplier = 1; return c }},
		{"negative_max_retries", func(c Config) Config { c.MaxRetries = -1; return c }},
		{"negative_heartbeat_timeout", func(c Config) Config { c.HeartbeatTimeout = -1; return c }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.mutate(validTestConfig())
			if err := cfg.validate(); err == nil {
				t.Fatalf("expected validate() to reject config, got nil error")
			} else if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("expected ErrConfigInvalid, got %v", err)
			}
		})
	}
}

func TestConfigValidate_AcceptsGoodConfig(t *testing.T) {
	if err := validTestConfig().validate(); err != nil {
		t.Fatalf("expected valid config to pass, got %v", err)
	}
}

// --- New() dependency checks --------------------------------------------

func TestNew_RejectsNilDependencies(t *testing.T) {
	cfg := validTestConfig()
	if _, err := New(cfg, nil, &fakeTransport{}); !errors.Is(err, ErrSecretProviderNil) {
		t.Fatalf("expected ErrSecretProviderNil, got %v", err)
	}
	if _, err := New(cfg, staticSecretProvider{key: "k"}, nil); !errors.Is(err, ErrTransportNil) {
		t.Fatalf("expected ErrTransportNil, got %v", err)
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	// MaxRetries is a field withDefaults() never backfills (unlike
	// e.g. ProviderID, which New() would silently repair before
	// validate() ever saw it empty), so this genuinely exercises
	// New()'s validate() call.
	cfg := validTestConfig()
	cfg.MaxRetries = -1
	if _, err := New(cfg, staticSecretProvider{key: "k"}, &fakeTransport{}); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid, got %v", err)
	}
}

// --- malformed message rejection (table-driven) -------------------------

func TestDecodeWireMessage_MalformedRejection(t *testing.T) {
	cases := []struct {
		name    string
		raw     []byte
		wantErr bool
	}{
		{"invalid_json", []byte(`{not json`), true},
		{"missing_message_type", mustJSON(t, map[string]interface{}{"MMSI": 123456789, "TimestampUTC": 1000}), true},
		{"missing_timestamp", mustJSON(t, map[string]interface{}{"MessageType": "PositionReport", "MMSI": 123456789}), true},
		{"missing_mmsi", mustJSON(t, map[string]interface{}{"MessageType": "PositionReport", "TimestampUTC": 1000}), true},
		{"zero_mmsi", mustJSON(t, map[string]interface{}{"MessageType": "PositionReport", "MMSI": 0, "TimestampUTC": 1000}), true},
		{"valid_position_report", mustJSON(t, positionReportFixture(123456789, 1.29, 103.85, 1700000000)), false},
		{"valid_heartbeat_without_mmsi", mustJSON(t, heartbeatFixture(1700000000)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeWireMessage(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// --- hashing: sensitivity and stability ---------------------------------

func TestHashes_PayloadHashChangesWithPayload_StableForIdenticalInput(t *testing.T) {
	a := mustJSON(t, positionReportFixture(111111111, 1.0, 2.0, 1700000000))
	b := mustJSON(t, positionReportFixture(222222222, 1.0, 2.0, 1700000000)) // different MMSI
	aAgain := mustJSON(t, positionReportFixture(111111111, 1.0, 2.0, 1700000000))

	ha, hb, ha2 := sha256Hex(a), sha256Hex(b), sha256Hex(aAgain)
	if ha == hb {
		t.Fatalf("expected different payloads to hash differently, both got %s", ha)
	}
	if ha != ha2 {
		t.Fatalf("expected identical payloads to hash identically: %s vs %s", ha, ha2)
	}
}

func TestHashes_CanonicalHashIndependentOfJSONFormatting(t *testing.T) {
	// Same semantic content, deliberately different key order and
	// whitespace in the raw JSON text.
	raw1 := []byte(`{"MessageType":"PositionReport","MMSI":123456789,"Latitude":1.29,"Longitude":103.85,"TimestampUTC":1700000000}`)
	raw2 := []byte("{\n  \"TimestampUTC\": 1700000000,\n  \"Longitude\": 103.85,\n  \"Latitude\": 1.29,\n  \"MMSI\": 123456789,\n  \"MessageType\": \"PositionReport\"\n}")

	w1, err := decodeWireMessage(raw1)
	if err != nil {
		t.Fatalf("decode raw1: %v", err)
	}
	w2, err := decodeWireMessage(raw2)
	if err != nil {
		t.Fatalf("decode raw2: %v", err)
	}

	payloadHash1 := sha256Hex(raw1)
	payloadHash2 := sha256Hex(raw2)
	if payloadHash1 == payloadHash2 {
		t.Fatalf("expected different raw bytes to produce different payload hashes")
	}

	canonicalHash1 := sha256Hex(w1.canonicalBytes())
	canonicalHash2 := sha256Hex(w2.canonicalBytes())
	if canonicalHash1 != canonicalHash2 {
		t.Fatalf("expected semantically identical messages to produce identical canonical hashes: %s vs %s", canonicalHash1, canonicalHash2)
	}

	// And canonical hash DOES change when semantic content changes.
	raw3 := []byte(`{"MessageType":"PositionReport","MMSI":999999999,"Latitude":1.29,"Longitude":103.85,"TimestampUTC":1700000000}`)
	w3, err := decodeWireMessage(raw3)
	if err != nil {
		t.Fatalf("decode raw3: %v", err)
	}
	canonicalHash3 := sha256Hex(w3.canonicalBytes())
	if canonicalHash3 == canonicalHash1 {
		t.Fatalf("expected different MMSI to change the canonical hash")
	}
}

// --- end-to-end accepted flow + provenance defaults ----------------------

func TestConnector_FullFlow_AcceptedMessagesAndProvenanceDefaults(t *testing.T) {
	msgs := [][]byte{
		mustJSON(t, positionReportFixture(111111111, 1.30, 103.80, 1700000001)),
		mustJSON(t, positionReportFixture(222222222, 1.31, 103.81, 1700000002)),
	}
	stream := newFakeStream(msgs...)
	transport := &fakeTransport{streamFactory: func() MessageStream { return stream }}

	cfg := validTestConfig()
	c, err := New(cfg, staticSecretProvider{key: "test-key"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	h := newCollectingHandler(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()

	for i := 0; i < 2; i++ {
		select {
		case <-h.acceptedCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for accepted message %d", i)
		}
	}
	cancel()
	<-done

	accepted, filtered, rejected, heartbeats := h.snapshot()
	if accepted != 2 || filtered != 0 || rejected != 0 || heartbeats != 0 {
		t.Fatalf("unexpected counts: accepted=%d filtered=%d rejected=%d heartbeats=%d", accepted, filtered, rejected, heartbeats)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for i, ev := range h.evidences {
		if err := ev.Validate(); err != nil {
			t.Fatalf("evidence[%d] failed Validate: %v", i, err)
		}
		if ev.OriginClass != provenance.OriginRealExternalUnverified {
			t.Fatalf("evidence[%d]: expected OriginRealExternalUnverified, got %q", i, ev.OriginClass)
		}
		if ev.RightsState != provenance.RightsUnknownPendingContract {
			t.Fatalf("evidence[%d]: expected RightsUnknownPendingContract, got %q", i, ev.RightsState)
		}
		if ev.PayloadHash != h.accepted[i].PayloadHash {
			t.Fatalf("evidence[%d]: payload hash not carried through to normalized event", i)
		}
	}

	first := h.accepted[0]
	if first.MMSI != 111111111 || first.IMO != 9123456 || first.CallSign != "TEST1" || first.ShipName != "MV SYNTHETIC EXAMPLE" {
		t.Fatalf("ship_identity facet not decoded correctly: %+v", first)
	}
	if !first.HasPosition || first.Latitude != 1.30 || first.Longitude != 103.80 {
		t.Fatalf("position facet not decoded correctly: %+v", first)
	}
	if !first.HasCourse || !first.HasSpeed {
		t.Fatalf("course/speed facets missing: %+v", first)
	}
	if !first.HasNavStatus || first.NavStatus != 0 {
		t.Fatalf("navigation_status facet not decoded correctly: %+v", first)
	}
	if first.Destination != "SINGAPORE" {
		t.Fatalf("destination facet not decoded correctly: %+v", first)
	}
	if first.ObservedAt != 1700000001 {
		t.Fatalf("expected ObservedAt to equal the message's own reported timestamp, got %d", first.ObservedAt)
	}

	// Sequence/delivery tracking: strictly increasing across messages.
	if h.accepted[1].DeliverySeq <= h.accepted[0].DeliverySeq {
		t.Fatalf("expected strictly increasing delivery sequence, got %d then %d", h.accepted[0].DeliverySeq, h.accepted[1].DeliverySeq)
	}
	if h.evidences[0].DeliveryID == h.evidences[1].DeliveryID {
		t.Fatalf("expected distinct delivery IDs, both got %q", h.evidences[0].DeliveryID)
	}
}

// --- malformed messages: rejected, counted, never silently dropped -------

func TestConnector_MalformedMessages_RejectedAndCounted(t *testing.T) {
	msgs := [][]byte{
		mustJSON(t, positionReportFixture(111111111, 1.0, 2.0, 1700000001)), // valid
		[]byte(`{not valid json`), // malformed: bad JSON
		mustJSON(t, map[string]interface{}{"MessageType": "PositionReport", "TimestampUTC": 1700000002}), // malformed: missing MMSI
		mustJSON(t, positionReportFixture(222222222, 1.0, 2.0, 1700000003)),                              // valid
	}
	stream := newFakeStream(msgs...)
	transport := &fakeTransport{streamFactory: func() MessageStream { return stream }}
	cfg := validTestConfig()
	c, err := New(cfg, staticSecretProvider{key: "k"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := newCollectingHandler(4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()

	for i := 0; i < 2; i++ {
		select {
		case <-h.acceptedCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for accepted message %d", i)
		}
	}
	// Give the rejected-count a moment to catch up (they interleave
	// with accepted messages but are processed synchronously in the
	// same goroutine before the next accept, so by the time we've
	// seen 2 accepts both rejects have already been recorded).
	cancel()
	<-done

	accepted, filtered, rejected, _ := h.snapshot()
	if accepted != 2 {
		t.Fatalf("expected 2 accepted, got %d", accepted)
	}
	if filtered != 0 {
		t.Fatalf("expected 0 filtered, got %d", filtered)
	}
	if rejected != 2 {
		t.Fatalf("expected 2 rejected, got %d", rejected)
	}
	snap := c.Metrics().Snapshot()
	if snap.Rejected != 2 {
		t.Fatalf("expected metrics.Rejected == 2, got %d", snap.Rejected)
	}
	if snap.Received != 4 {
		t.Fatalf("expected metrics.Received == 4 (every message counted, none silently dropped), got %d", snap.Received)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for i, err := range h.rejectErrs {
		if !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("rejectErrs[%d] does not wrap ErrMalformedMessage: %v", i, err)
		}
	}
	if len(h.rejected[0]) == 0 {
		t.Fatalf("expected raw bytes of the rejected message to be preserved")
	}
}

// --- filtering: bounding box, MMSI, message type --------------------------

func TestConnector_Filtering(t *testing.T) {
	cases := []struct {
		name       string
		cfgMutate  func(Config) Config
		msg        map[string]interface{}
		wantAccept bool
		wantReason string
	}{
		{
			name:       "mmsi_filter_admits_match",
			cfgMutate:  func(c Config) Config { c.MMSIFilter = []int64{111111111}; return c },
			msg:        positionReportFixture(111111111, 1.0, 2.0, 1700000001),
			wantAccept: true,
		},
		{
			name:       "mmsi_filter_rejects_non_match",
			cfgMutate:  func(c Config) Config { c.MMSIFilter = []int64{111111111}; return c },
			msg:        positionReportFixture(999999999, 1.0, 2.0, 1700000001),
			wantAccept: false,
			wantReason: "mmsi_not_in_filter",
		},
		{
			name:       "message_type_filter_admits_match",
			cfgMutate:  func(c Config) Config { c.MessageTypeFilter = []string{"PositionReport"}; return c },
			msg:        positionReportFixture(111111111, 1.0, 2.0, 1700000001),
			wantAccept: true,
		},
		{
			name:       "message_type_filter_rejects_non_match",
			cfgMutate:  func(c Config) Config { c.MessageTypeFilter = []string{"ShipStaticData"}; return c },
			msg:        positionReportFixture(111111111, 1.0, 2.0, 1700000001),
			wantAccept: false,
			wantReason: "message_type_not_in_filter",
		},
		{
			name: "bounding_box_admits_inside_point",
			cfgMutate: func(c Config) Config {
				c.BoundingBoxes = []BoundingBox{{{0, 100}, {5, 110}}}
				return c
			},
			msg:        positionReportFixture(111111111, 1.29, 103.85, 1700000001),
			wantAccept: true,
		},
		{
			name: "bounding_box_rejects_outside_point",
			cfgMutate: func(c Config) Config {
				c.BoundingBoxes = []BoundingBox{{{0, 100}, {5, 110}}}
				return c
			},
			msg:        positionReportFixture(111111111, 40.0, -70.0, 1700000001),
			wantAccept: false,
			wantReason: "outside_bounding_boxes",
		},
		{
			name: "bounding_box_reversed_corners_still_works",
			cfgMutate: func(c Config) Config {
				c.BoundingBoxes = []BoundingBox{{{5, 110}, {0, 100}}} // corners swapped
				return c
			},
			msg:        positionReportFixture(111111111, 1.29, 103.85, 1700000001),
			wantAccept: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := newFakeStream(mustJSON(t, tc.msg))
			transport := &fakeTransport{streamFactory: func() MessageStream { return stream }}
			cfg := tc.cfgMutate(validTestConfig())
			c, err := New(cfg, staticSecretProvider{key: "k"}, transport)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			h := newCollectingHandler(1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Run(ctx, h) }()

			if tc.wantAccept {
				select {
				case <-h.acceptedCh:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for accepted message")
				}
			} else {
				// Poll metrics until the message has been processed
				// (filtered), bounded by a real timeout.
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					if c.Metrics().Snapshot().Received >= 1 {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
			cancel()
			<-done

			accepted, filtered, _, _ := h.snapshot()
			if tc.wantAccept {
				if accepted != 1 || filtered != 0 {
					t.Fatalf("expected accepted=1 filtered=0, got accepted=%d filtered=%d", accepted, filtered)
				}
			} else {
				if accepted != 0 || filtered != 1 {
					t.Fatalf("expected accepted=0 filtered=1, got accepted=%d filtered=%d", accepted, filtered)
				}
				h.mu.Lock()
				gotReason := h.filterWhys[0]
				h.mu.Unlock()
				if gotReason != tc.wantReason {
					t.Fatalf("expected filter reason %q, got %q", tc.wantReason, gotReason)
				}
			}
		})
	}
}

// --- heartbeat handling ----------------------------------------------------

func TestConnector_Heartbeat_DetectedCountedAndAcked(t *testing.T) {
	stream := newFakeStream(
		mustJSON(t, heartbeatFixture(1700000000)),
		mustJSON(t, positionReportFixture(111111111, 1.0, 2.0, 1700000001)),
	)
	transport := &fakeTransport{streamFactory: func() MessageStream { return stream }}
	cfg := validTestConfig()
	cfg.RespondToHeartbeat = true
	c, err := New(cfg, staticSecretProvider{key: "k"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := newCollectingHandler(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()

	select {
	case <-h.acceptedCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for accepted message")
	}
	cancel()
	<-done

	accepted, filtered, rejected, heartbeats := h.snapshot()
	if heartbeats != 1 {
		t.Fatalf("expected 1 heartbeat observed, got %d", heartbeats)
	}
	if accepted != 1 || filtered != 0 || rejected != 0 {
		t.Fatalf("heartbeat must not be counted as accepted/filtered/rejected: accepted=%d filtered=%d rejected=%d", accepted, filtered, rejected)
	}
	snap := c.Metrics().Snapshot()
	if snap.Heartbeats != 1 {
		t.Fatalf("expected metrics.Heartbeats == 1, got %d", snap.Heartbeats)
	}
	writes := stream.Writes()
	// writes[0] is always the initial subscription frame sent by
	// connectAndSubscribe; the heartbeat ack is the next write.
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 writes (subscription + heartbeat ack), got %d", len(writes))
	}
	if !strings.Contains(string(writes[1]), "HeartbeatAck") {
		t.Fatalf("expected second write to be a HeartbeatAck, got %s", writes[1])
	}
}

func TestConnector_HeartbeatTimeout_TriggersReconnect(t *testing.T) {
	// A stream that never sends anything: with a short
	// HeartbeatTimeout configured, readLoop must time out and Run
	// must reconnect, repeatedly, until ctx is canceled.
	transport := &fakeTransport{streamFactory: func() MessageStream { return newFakeStream() }}
	cfg := validTestConfig()
	cfg.HeartbeatTimeout = 15 * time.Millisecond
	cfg.InitialBackoff = 5 * time.Millisecond
	cfg.MaxBackoff = 20 * time.Millisecond
	c, err := New(cfg, staticSecretProvider{key: "k"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := newCollectingHandler(1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()
	<-done

	snap := c.Metrics().Snapshot()
	if snap.Reconnects < 2 {
		t.Fatalf("expected at least 2 reconnects from repeated heartbeat timeouts, got %d", snap.Reconnects)
	}
	if snap.ReconnectFailures != 0 {
		t.Fatalf("expected 0 dial failures (only read timeouts), got %d", snap.ReconnectFailures)
	}
}

// --- reconnect with bounded, growing, capped backoff -----------------------

func TestConnector_ReconnectBackoff_GrowsAndCaps(t *testing.T) {
	blockingStream := newFakeStream() // no messages; blocks until ctx done
	transport := &fakeTransport{
		failuresRemaining: 3,
		streamFactory:     func() MessageStream { return blockingStream },
	}
	cfg := validTestConfig()
	cfg.InitialBackoff = 100 * time.Millisecond
	cfg.MaxBackoff = 300 * time.Millisecond
	cfg.BackoffMultiplier = 2.0

	c, err := New(cfg, staticSecretProvider{key: "k"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var mu sync.Mutex
	var recorded []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		recorded = append(recorded, d)
		mu.Unlock()
		return nil // never actually sleep in this test
	}

	h := newCollectingHandler(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()

	// Wait until the connector has stopped failing (i.e. connected
	// successfully after the 3 injected failures), bounded by a real
	// timeout, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Metrics().Snapshot().Reconnects >= 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 3 {
		t.Fatalf("expected exactly 3 recorded backoff sleeps (one per dial failure), got %d: %v", len(recorded), recorded)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond} // 100 -> 200 -> 400 capped to 300
	for i, w := range want {
		if recorded[i] != w {
			t.Fatalf("backoff[%d]: expected %v, got %v (full sequence: %v)", i, w, recorded[i], recorded)
		}
	}
	snap := c.Metrics().Snapshot()
	if snap.ReconnectFailures != 3 {
		t.Fatalf("expected 3 reconnect failures, got %d", snap.ReconnectFailures)
	}
	if snap.Reconnects != 1 {
		t.Fatalf("expected exactly 1 successful reconnect, got %d", snap.Reconnects)
	}
	if transport.ConnectCalls() != 4 {
		t.Fatalf("expected 4 total connect attempts (3 failed + 1 success), got %d", transport.ConnectCalls())
	}
}

func TestConnector_MaxRetriesExceeded_ReturnsError(t *testing.T) {
	transport := &fakeTransport{failuresRemaining: 1000} // always fails
	cfg := validTestConfig()
	cfg.MaxRetries = 3
	cfg.InitialBackoff = time.Millisecond
	cfg.MaxBackoff = 4 * time.Millisecond

	c, err := New(cfg, staticSecretProvider{key: "k"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil } // never really sleep

	h := newCollectingHandler(1)
	err = c.Run(context.Background(), h)
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Fatalf("expected ErrMaxRetriesExceeded, got %v", err)
	}
	if got := transport.ConnectCalls(); got != cfg.MaxRetries+1 {
		t.Fatalf("expected %d connect attempts (MaxRetries+1), got %d", cfg.MaxRetries+1, got)
	}
	snap := c.Metrics().Snapshot()
	if snap.ReconnectFailures != uint64(cfg.MaxRetries+1) {
		t.Fatalf("expected %d recorded reconnect failures, got %d", cfg.MaxRetries+1, snap.ReconnectFailures)
	}
}

// --- context cancellation --------------------------------------------------

func TestConnector_ContextCancel_CleanExit(t *testing.T) {
	stream := newFakeStream() // never sends anything; blocks on ctx.Done()
	transport := &fakeTransport{streamFactory: func() MessageStream { return stream }}
	c, err := New(validTestConfig(), staticSecretProvider{key: "k"}, transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := newCollectingHandler(1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()

	// Give Run a moment to reach the blocked read, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not exit within 2s of ctx cancellation")
	}
}

func TestConnector_Run_RejectsNilHandler(t *testing.T) {
	c, err := New(validTestConfig(), staticSecretProvider{key: "k"}, &fakeTransport{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Run(context.Background(), nil); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("expected ErrConfigInvalid for nil handler, got %v", err)
	}
}

func TestConnector_MissingAPIKey_CountedAsReconnectFailure(t *testing.T) {
	transport := &fakeTransport{failuresRemaining: 0, streamFactory: func() MessageStream { return newFakeStream() }}
	cfg := validTestConfig()
	cfg.MaxRetries = 1
	cfg.InitialBackoff = time.Millisecond
	cfg.MaxBackoff = 2 * time.Millisecond
	c, err := New(cfg, staticSecretProvider{key: ""}, transport) // blank key
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	err = c.Run(context.Background(), newCollectingHandler(1))
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Fatalf("expected ErrMaxRetriesExceeded (never even reaching the transport), got %v", err)
	}
	if transport.ConnectCalls() != 0 {
		t.Fatalf("expected transport.Connect to never be called when the API key is blank, got %d calls", transport.ConnectCalls())
	}
}

// --- ontology bridge (PART 4) -----------------------------------------------

func TestNormalizedEvent_ToOntologyEvidence_ValidAISObservation(t *testing.T) {
	w, err := decodeWireMessage(mustJSON(t, positionReportFixture(123456789, 1.29, 103.85, 1700000000)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	event := normalize(w, 1, 1700000005, 1700000006, "payloadhash", "canonicalhash")

	ev, err := event.ToOntologyEvidence("aisstream-ws", 0.6)
	if err != nil {
		t.Fatalf("ToOntologyEvidence: %v", err)
	}
	if ev.Type != ontology.TypeAISObservation {
		t.Fatalf("expected TypeAISObservation, got %v", ev.Type)
	}
	if ev.Class() != ontology.ClassPrimary {
		t.Fatalf("expected AIS observations to be primary evidence, got %v", ev.Class())
	}
	if ev.Attributes["mmsi"] != "123456789" {
		t.Fatalf("expected attributes[mmsi] == 123456789, got %q", ev.Attributes["mmsi"])
	}
	if ev.ObservedAt != 1700000000 {
		t.Fatalf("expected ObservedAt to equal the source timestamp, got %d", ev.ObservedAt)
	}
	if ev.ReceivedAt != 1700000005 {
		t.Fatalf("expected ReceivedAt to equal the connector's own receipt time, got %d", ev.ReceivedAt)
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("bridged ontology.Evidence failed Validate: %v", err)
	}
}

func TestNormalizedEvent_PreservesPayloadHash_AndImmutableObservedAt(t *testing.T) {
	raw := mustJSON(t, positionReportFixture(555555555, 10.0, 20.0, 1650000000))
	w, err := decodeWireMessage(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	payloadHash := sha256Hex(raw)
	canonicalHash := sha256Hex(w.canonicalBytes())

	event := normalize(w, 42, 1650000010, 1650000020, payloadHash, canonicalHash)
	if event.PayloadHash != payloadHash {
		t.Fatalf("expected PayloadHash to be carried through unchanged")
	}
	if event.CanonicalHash != canonicalHash {
		t.Fatalf("expected CanonicalHash to be carried through unchanged")
	}
	if event.ObservedAt != 1650000000 {
		t.Fatalf("expected ObservedAt to be the source's own reported timestamp, got %d", event.ObservedAt)
	}
	if event.ReceivedAt != 1650000010 || event.NormalizedAt != 1650000020 {
		t.Fatalf("expected ReceivedAt and NormalizedAt to be independently settable, got ReceivedAt=%d NormalizedAt=%d", event.ReceivedAt, event.NormalizedAt)
	}
	if event.ObservedAt == event.ReceivedAt || event.ReceivedAt == event.NormalizedAt {
		t.Fatalf("expected three genuinely distinct bitemporal fields in this test, got ObservedAt=%d ReceivedAt=%d NormalizedAt=%d",
			event.ObservedAt, event.ReceivedAt, event.NormalizedAt)
	}
}

// --- retention -------------------------------------------------------------

func TestRingBufferRetention_KeepsOnlyMostRecentN(t *testing.T) {
	r := NewRingBufferRetention(2)
	r.Retain(1, []byte("a"))
	r.Retain(2, []byte("b"))
	r.Retain(3, []byte("c"))
	recent := r.Recent()
	if len(recent) != 2 {
		t.Fatalf("expected 2 retained entries, got %d", len(recent))
	}
	if recent[0].Seq != 2 || recent[1].Seq != 3 {
		t.Fatalf("expected the two most recent entries (seq 2,3), got seq %d,%d", recent[0].Seq, recent[1].Seq)
	}
}

func TestNoRetention_DiscardsExplicitly(t *testing.T) {
	var r RawRetention = NoRetention{}
	// Must not panic; nothing to assert on since NoRetention has no
	// observable state — the point is it's an explicit, named choice,
	// not the silent default (see TestConnector_DefaultsToRetainingRawBytes).
	r.Retain(1, []byte("x"))
}

func TestConnector_DefaultsToRetainingRawBytes(t *testing.T) {
	c, err := New(validTestConfig(), staticSecretProvider{key: "k"}, &fakeTransport{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rb, ok := c.retention.(*RingBufferRetention)
	if !ok {
		t.Fatalf("expected default RawRetention to be a *RingBufferRetention, got %T", c.retention)
	}
	if len(rb.Recent()) != 0 {
		t.Fatalf("expected a fresh default retention buffer to be empty")
	}
}
