package aisstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"veriqo/pkg/evidence/provenance"
)

// SleepFunc is the injectable delay primitive Run uses between
// reconnect attempts. Tests replace it with a function that records
// the requested durations and returns immediately, so backoff growth
// can be asserted without the test actually waiting seconds.
type SleepFunc func(ctx context.Context, d time.Duration) error

// defaultSleep is a context-cancellable real sleep: it returns
// ctx.Err() promptly if ctx is canceled before d elapses.
func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func defaultNow() uint64 { return uint64(time.Now().Unix()) } // #nosec G115 -- Unix() is positive for any realistic clock (1970..292 billion AD)

// Connector is one AISstream subscription's runtime state: its
// config, its dependencies (SecretProvider, Transport, RawRetention),
// its metrics, and its monotonic delivery counter.
type Connector struct {
	cfg       Config
	secrets   SecretProvider
	transport Transport
	retention RawRetention
	metrics   *Metrics

	seq atomic.Uint64

	// sleep and now are overridden by tests (this package's own
	// _test.go, same package) to make backoff and timestamp
	// generation deterministic without touching exported API.
	sleep SleepFunc
	now   func() uint64
}

// New constructs a Connector. cfg is validated (after defaults are
// backfilled) and secrets/transport must both be non-nil — a
// Connector can never exist in a state where either dependency is
// missing.
func New(cfg Config, secrets SecretProvider, transport Transport) (*Connector, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, ErrSecretProviderNil
	}
	if transport == nil {
		return nil, ErrTransportNil
	}
	retention := cfg.RawRetention
	if retention == nil {
		retention = NewRingBufferRetention(256)
	}
	c := &Connector{
		cfg:       cfg,
		secrets:   secrets,
		transport: transport,
		retention: retention,
		metrics:   &Metrics{},
		sleep:     defaultSleep,
		now:       defaultNow,
	}
	return c, nil
}

// Metrics returns this connector's counters.
func (c *Connector) Metrics() *Metrics { return c.metrics }

// nextBackoff computes the next backoff duration: cur multiplied by
// cfg.BackoffMultiplier, capped at cfg.MaxBackoff. A non-positive
// input (should not normally happen given Config.validate) resets to
// cfg.InitialBackoff rather than producing a zero/negative delay.
func nextBackoff(cur time.Duration, cfg Config) time.Duration {
	next := time.Duration(float64(cur) * cfg.BackoffMultiplier)
	if next > cfg.MaxBackoff {
		next = cfg.MaxBackoff
	}
	if next <= 0 {
		next = cfg.InitialBackoff
	}
	return next
}

// Run connects, subscribes, and processes messages until ctx is
// canceled, or Config.MaxRetries consecutive reconnect failures occur
// (dial failures and dropped/timed-out connections share one failure
// streak, reset on every successful connect). MaxRetries == 0 means
// unlimited retries — Run then exits only via ctx cancellation.
//
// Every raw message read is reported to h through exactly one of its
// four methods (see Handler) before the next message is read —
// nothing is ever silently dropped.
func (c *Connector) Run(ctx context.Context, h Handler) error {
	if h == nil {
		return fmt.Errorf("%w: handler is nil", ErrConfigInvalid)
	}

	backoff := c.cfg.InitialBackoff
	failures := 0
	first := true

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !first {
			if c.cfg.MaxRetries > 0 && failures > c.cfg.MaxRetries {
				return fmt.Errorf("%w: %d consecutive failures", ErrMaxRetriesExceeded, failures)
			}
			if serr := c.sleep(ctx, backoff); serr != nil {
				return serr
			}
			backoff = nextBackoff(backoff, c.cfg)
		}
		first = false

		stream, err := c.connectAndSubscribe(ctx)
		if err != nil {
			c.metrics.ReconnectFailures.Add(1)
			failures++
			continue
		}

		// A successful connect resets the failure streak and backoff
		// — this attempt earned a fresh start, regardless of how many
		// prior attempts failed.
		failures = 0
		backoff = c.cfg.InitialBackoff
		c.metrics.Reconnects.Add(1)

		loopErr := c.readLoop(ctx, stream, h)
		_ = stream.Close()

		if loopErr != nil {
			if errors.Is(loopErr, ErrProvenanceInvalid) {
				// Fail closed: a provenance contract violation is a
				// programming error, never something a reconnect can
				// paper over.
				return loopErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Dropped connection or heartbeat timeout: loop back
			// around and reconnect, subject to the same backoff/
			// max-retries discipline as a dial failure.
			failures++
			continue
		}

		// readLoop returns nil only after observing ctx cancellation.
		return ctx.Err()
	}
}

// connectAndSubscribe fetches the API key, dials the transport, and
// sends this connector's subscription frame. Any failure at any step
// closes the stream (if one was opened) and returns a wrapped error.
func (c *Connector) connectAndSubscribe(ctx context.Context) (MessageStream, error) {
	key, err := c.secrets.APIKey()
	if err != nil {
		return nil, fmt.Errorf("aisstream: fetching API key: %w", err)
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrMissingAPIKey
	}

	stream, err := c.transport.Connect(ctx, c.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("aisstream: connect: %w", err)
	}

	sub := subscriptionMessage{
		APIKey:             key,
		BoundingBoxes:      c.cfg.BoundingBoxes,
		FilterMessageTypes: c.cfg.MessageTypeFilter,
	}
	for _, m := range c.cfg.MMSIFilter {
		sub.FilterShipMMSI = append(sub.FilterShipMMSI, strconv.FormatInt(m, 10))
	}
	raw, err := json.Marshal(sub) // #nosec G117 -- sub.APIKey is deliberately marshaled here: sending the API key IS the AISstream subscription handshake, over the already-established (real deployments: TLS) stream; it is never logged or persisted, only written to the wire
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("aisstream: encoding subscription message: %w", err)
	}
	if err := stream.WriteMessage(ctx, raw); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("aisstream: sending subscription message: %w", err)
	}
	return stream, nil
}

// readLoop reads and processes messages until either ctx is canceled
// (returns nil: a clean shutdown, not a failure) or a read fails for
// any other reason (returns a wrapped error, signaling the caller to
// reconnect) or handleRaw itself returns a fail-closed error (returned
// verbatim, propagated all the way up through Run).
func (c *Connector) readLoop(ctx context.Context, stream MessageStream, h Handler) error {
	for {
		rctx := ctx
		var cancel context.CancelFunc
		if c.cfg.HeartbeatTimeout > 0 {
			rctx, cancel = context.WithTimeout(ctx, c.cfg.HeartbeatTimeout)
		}
		raw, err := stream.ReadMessage(rctx)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("aisstream: read failed (dropped connection or heartbeat timeout): %w", err)
		}
		if err := c.handleRaw(ctx, raw, h, stream); err != nil {
			return err
		}
	}
}

// handleRaw processes exactly one raw message: malformed-message
// rejection, heartbeat detection, hashing, provenance construction
// (with the hard-required OriginRealExternalUnverified /
// RightsUnknownPendingContract defaults), filtering, and
// normalization. It returns a non-nil error ONLY for the fail-closed
// ErrProvenanceInvalid case; every other outcome is reported to h and
// returns nil so the read loop continues.
func (c *Connector) handleRaw(ctx context.Context, raw []byte, h Handler, stream MessageStream) error {
	c.metrics.Received.Add(1)
	seq := c.seq.Add(1)
	c.retention.Retain(seq, raw)

	w, err := decodeWireMessage(raw)
	if err != nil {
		c.metrics.Rejected.Add(1)
		h.HandleRejected(raw, fmt.Errorf("%w: %v", ErrMalformedMessage, err))
		return nil
	}

	if w.MessageType == messageTypeHeartbeat {
		c.metrics.Heartbeats.Add(1)
		h.HandleHeartbeat(w.TimestampUTC)
		if c.cfg.RespondToHeartbeat {
			ack, mErr := json.Marshal(struct {
				MessageType string `json:"MessageType"`
			}{MessageType: "HeartbeatAck"})
			if mErr == nil {
				// Best-effort: a failed ack is not fatal on its own —
				// a truly dead connection is caught by the next
				// read's own heartbeat timeout.
				_ = stream.WriteMessage(ctx, ack)
			}
		}
		return nil
	}

	receivedAt := c.now()
	payloadHash := sha256Hex(raw)
	canonicalHash := sha256Hex(w.canonicalBytes())

	ev := provenance.ExternalEvidence{
		SourceID:   c.cfg.SourceID,
		ProviderID: c.cfg.ProviderID,
		DatasetID:  c.cfg.DatasetID,
		DeliveryID: fmt.Sprintf("seq-%d", seq),

		// Hard requirement, unconditional: this package never
		// constructs an ExternalEvidence with any other OriginClass/
		// RightsState. See config.go's package doc comment.
		OriginClass: provenance.OriginRealExternalUnverified,
		RightsState: provenance.RightsUnknownPendingContract,

		ReceivedAt: receivedAt,
		ObservedAt: uint64(w.TimestampUTC), // #nosec G115 -- decodeWireMessage's validate() already rejected TimestampUTC <= 0 before handleRaw ever reaches this line

		PayloadHash:   payloadHash,
		CanonicalHash: canonicalHash,
		SchemaVersion: provenance.SchemaVersionV1,

		CorrectionState:  provenance.CorrectionNone,
		AttestationState: provenance.AttestationUnattested,
	}
	if err := ev.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrProvenanceInvalid, err)
	}

	if reason, ok := c.passesFilters(w); !ok {
		c.metrics.Filtered.Add(1)
		h.HandleFiltered(ev, reason)
		return nil
	}

	normalizedAt := c.now()
	event := normalize(w, seq, receivedAt, normalizedAt, payloadHash, canonicalHash)
	c.metrics.Accepted.Add(1)
	h.HandleAccepted(event, ev)
	return nil
}
