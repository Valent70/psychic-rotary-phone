// ingest.go closes R-050 of the requirement traceability matrix: real
// ingestion CONTRACTS for the five real-world source types the audit
// named — AIS, SAR, BoL, insurance, payment. AIS already had a full
// treatment in pkg/connector/aisstream (wire schema, structural
// validation, canonicalization into ontology.TypeAISObservation); this
// file adds the shared seam the four NEW sibling packages
// (pkg/connector/sar, pkg/connector/bol, pkg/connector/insurance,
// pkg/connector/payment) all plug into, so five source types share one
// disciplined pipeline instead of five one-off ad-hoc parsers.
//
// The seam is deliberately a COMPOSITION of two existing engines, never
// a re-implementation of either:
//
//  1. pkg/blockers/livedata.FeedConnector / livedata.Pipeline — already
//     provides content-hash dedup, anti-replay defense, and the one
//     invariant every source in this package must never violate: a
//     SIMULATED-mode connector's record tagged LIVE is refused, full
//     stop. This file does not reimplement any of that; Drain calls
//     straight into livedata.Pipeline.Ingest for every record.
//  2. Decoder — the NEW seam this file adds — turns one livedata.Record's
//     opaque Payload string into canonical, ontology-validated evidence.
//     livedata deliberately treats Payload as opaque ("whatever the
//     source sends"); Decoder is where structural (parse/schema) and
//     semantic (domain-range) validation happens, per source type,
//     before anything is allowed to become ontology.Evidence.
//
// A record must pass BOTH layers to end up in IngestResult.Accepted:
// livedata's transport-level checks (data_mode tag present, hash
// matches content, not a replay, not an unearned LIVE claim) AND this
// package's decode-level checks (well-formed per the source's schema,
// semantically valid, and acceptable to ontology.New's own required-
// attribute and epistemic-class rules). Any single failure at any
// stage rejects the record with the stage and reason recorded — never
// silently dropped, never partially accepted.
package connector

import (
	"context"
	"errors"
	"fmt"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/dataquality"
	"veriqo/pkg/evidence/ontology"
)

// Decoder is implemented by each source-type package (sar, bol,
// insurance, payment; aisstream implements the equivalent shape under
// its own name for historical reasons predating this file). Decode
// must perform structural validation (can this be parsed as this
// source's wire schema at all?) and semantic validation (are the
// parsed values in-range and internally consistent?) BEFORE calling
// ontology.New — Decode returning a nil error is this package's only
// signal that a record is trustworthy enough to canonicalize.
type Decoder interface {
	// SourceType names the ingestion contract, matching the Source
	// string the paired livedata.FeedConnector uses (e.g. "SAR").
	SourceType() string
	// Decode parses and validates one raw record payload, and
	// canonicalizes it into ontology.Evidence. receivedAtTick is the
	// tick at which THIS pipeline is draining the feed (bitemporal
	// ReceivedAt, distinct from whatever ObservedAt the payload itself
	// declares) — see pkg/evidence/ontology's own bitemporality
	// contract.
	Decode(payload string, receivedAtTick uint64) (ontology.Evidence, error)
}

// RejectedRecord is one record Drain refused, with which stage refused
// it and why — deliberately mirroring livedata.RejectedRecord's shape
// (Record, Reason) but adding Stage, since this pipeline has two
// distinct fail-closed layers instead of livedata's one.
type RejectedRecord struct {
	Record livedata.Record
	Stage  string // "transport" (livedata.Pipeline) or "decode" (this package / ontology)
	Reason string
}

// IngestResult summarizes one Drain pass.
type IngestResult struct {
	SourceType string
	Ingested   int // records received from the connector at all
	Accepted   int // records that became valid ontology.Evidence
	Rejected   []RejectedRecord
}

// ErrNilDecoder is returned by Drain when dec is nil — a caller must
// always supply a decode contract; there is no "trust the payload"
// default.
var ErrNilDecoder = errors.New("connector: nil Decoder")

// ErrFeedExhausted is the sentinel every SIMULATED FeedConnector
// implemented by this package's sibling packages (sar, bol, insurance,
// payment) returns from Receive once its fixture supply is drained.
// Drain treats it as a normal end-of-feed signal, never as a failure.
var ErrFeedExhausted = errors.New("connector: feed exhausted")

// Drain connects, authenticates and fully drains feed, running every
// record it emits through pipeline (livedata's transport-level
// dedup/anti-replay/LIVE-refusal) and then, for whatever survives
// that, through dec.Decode (structural + semantic validation +
// ontology canonicalization). It never touches feed's Mode() to decide
// whether to run — a REAL-mode connector drains exactly the same way a
// SIMULATED one does; only livedata.Pipeline.Ingest's own mode check
// (comparing connectorMode against each record's declared DataMode)
// can refuse anything, exactly as it does today for every other
// blocker source. That is what "structurally supports a LIVE mode
// without ever fabricating what LIVE would mean" means in practice:
// nothing about Drain's code path forecloses a real connector, and
// nothing about it can be tricked into rewarding a fixture that lies.
func Drain(ctx context.Context, feed livedata.FeedConnector, pipeline *livedata.Pipeline, credentials string, dec Decoder, receivedAtTick uint64) (IngestResult, []ontology.Evidence, error) {
	if dec == nil {
		return IngestResult{}, nil, ErrNilDecoder
	}
	res := IngestResult{SourceType: dec.SourceType()}
	var evidence []ontology.Evidence

	if err := feed.Connect(ctx); err != nil {
		return res, nil, fmt.Errorf("connector: connect %s: %w", feed.Source(), err)
	}
	defer func() { _ = feed.Close(ctx) }()
	if err := feed.Authenticate(ctx, credentials); err != nil {
		return res, nil, fmt.Errorf("connector: authenticate %s: %w", feed.Source(), err)
	}

	for {
		rec, err := feed.Receive(ctx)
		if err != nil {
			if isFeedExhausted(err) {
				break
			}
			return res, evidence, fmt.Errorf("connector: receive from %s: %w", feed.Source(), err)
		}
		res.Ingested++

		accepted, reason := pipeline.Ingest(rec, feed.Mode())
		if !accepted {
			res.Rejected = append(res.Rejected, RejectedRecord{Record: rec, Stage: "transport", Reason: reason})
			continue
		}

		ev, err := dec.Decode(rec.Payload, receivedAtTick)
		if err != nil {
			res.Rejected = append(res.Rejected, RejectedRecord{Record: rec, Stage: "decode", Reason: err.Error()})
			continue
		}
		res.Accepted++
		evidence = append(evidence, ev)
	}
	return res, evidence, nil
}

// isFeedExhausted recognizes both this package's own ErrFeedExhausted
// (used by every SIMULATED connector in pkg/connector/{sar,bol,
// insurance,payment}) and livedata's own unexported exhaustion
// sentinel, matched by its one documented, stable message so that a
// caller who instead plugs in a livedata.SyntheticConnector or
// livedata.ReplayConnector directly still drains cleanly. livedata
// does not export that sentinel, and this package deliberately does
// not duplicate livedata's Receive/Connect/Authenticate state machine
// to get at it — string-matching the stable message is the least-
// duplicative option available. Any OTHER error from Receive still
// propagates as a real failure.
func isFeedExhausted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrFeedExhausted) {
		return true
	}
	return err.Error() == "livedata: feed exhausted"
}

// QualityVector derives a pkg/dataquality.Vector from one IngestResult
// — the plug point pkg/dataquality's own doc comment calls for
// ("Jangan gunakan satu score tanpa decomposition"). This package does
// not invent its own quality scoring model; it feeds dataquality the
// two dimensions an ingestion pipeline can honestly measure about
// itself (how much of what arrived was structurally/semantically
// valid, and how well-attributed it was), and leaves Freshness,
// Consistency and Reliability to callers who have the context (feed
// timestamps, cross-source contradiction checks, a dataquality.Learner
// with real observed-outcome history) this package does not have.
// Freshness and Consistency default to 1.0 (unknown-but-not-penalized)
// rather than 0.0 (unknown-and-worst-case) because this package has no
// basis to claim staleness or contradiction that it did not measure;
// Reliability defaults to 0.5 (neutral prior) for the same reason.
func QualityVector(res IngestResult) dataquality.Vector {
	total := res.Ingested
	if total == 0 {
		return dataquality.Vector{Completeness: 0, Freshness: 1, Validity: 0, Consistency: 1, Provenance: 1, Reliability: 0.5}
	}
	validity := float64(res.Accepted) / float64(total)
	return dataquality.Vector{
		Completeness: validity, // this pipeline has no partial-record concept: a record is whole or rejected
		Freshness:    1,
		Validity:     validity,
		Consistency:  1,
		Provenance:   1, // every accepted record carries a livedata content hash + DataMode tag
		Reliability:  0.5,
	}
}
