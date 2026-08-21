package aisstream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// messageTypeHeartbeat marks a keepalive frame rather than an AIS
// data report. This is this package's OWN synthetic protocol
// convention (see the wireMessage doc comment below) — it exists so
// this connector has something concrete to detect and respond to for
// the heartbeat-handling requirement, exercised entirely against the
// fake transport in tests, never against a real feed.
const messageTypeHeartbeat = "Heartbeat"

// wireMessage is this connector's own simplified, illustrative wire
// schema for a single AIS report delivered over the stream. Field
// names are loosely inspired by publicly documented AIS/AISstream
// terminology — MMSI, IMO, CallSign, ShipName, Latitude/Longitude,
// Cog (course over ground) / Sog (speed over ground),
// NavigationalStatus, Destination — but this struct is NOT a claim of
// byte-for-byte compatibility with the real aisstream.io wire
// protocol. The JSON fixtures used in client_test.go are hand-written,
// plausible AIS-message-shaped test data, explicitly labeled synthetic
// there — they were not captured from any real feed.
type wireMessage struct {
	MessageType  string   `json:"MessageType"`
	MMSI         *int64   `json:"MMSI,omitempty"`
	IMO          *int64   `json:"IMO,omitempty"`
	CallSign     string   `json:"CallSign,omitempty"`
	ShipName     string   `json:"ShipName,omitempty"`
	Latitude     *float64 `json:"Latitude,omitempty"`
	Longitude    *float64 `json:"Longitude,omitempty"`
	Cog          *float64 `json:"Cog,omitempty"`
	Sog          *float64 `json:"Sog,omitempty"`
	NavStatus    *int     `json:"NavigationalStatus,omitempty"`
	Destination  string   `json:"Destination,omitempty"`
	TimestampUTC int64    `json:"TimestampUTC"`
}

// Malformed-message reasons. Wrapped with ErrMalformedMessage by
// decodeWireMessage's caller so every rejection is both a concrete,
// inspectable reason and matchable via errors.Is(err,
// ErrMalformedMessage).
var (
	errMissingMessageType = errors.New("missing MessageType")
	errMissingTimestamp   = errors.New("missing or non-positive TimestampUTC")
	errMissingMMSI        = errors.New("missing MMSI")
	errInvalidMMSI        = errors.New("MMSI must be positive")
)

// validate enforces the required-field contract: MessageType and a
// positive TimestampUTC are always required; MMSI is required (and
// must be positive) for every message type except the heartbeat.
func (w wireMessage) validate() error {
	if strings.TrimSpace(w.MessageType) == "" {
		return errMissingMessageType
	}
	if w.TimestampUTC <= 0 {
		return errMissingTimestamp
	}
	if w.MessageType == messageTypeHeartbeat {
		return nil
	}
	if w.MMSI == nil {
		return errMissingMMSI
	}
	if *w.MMSI <= 0 {
		return errInvalidMMSI
	}
	return nil
}

// canonicalBytes is the deterministic, field-ordered representation of
// this message's DECODED semantic content — independent of the raw
// JSON's key order or whitespace. It is hashed separately from the raw
// payload as this connector's canonical_hash: two payloads that differ
// only in JSON formatting but decode to identical fields produce the
// same canonical_hash while still producing different payload_hash
// values (see client_test.go's hashing tests).
func (w wireMessage) canonicalBytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "message_type=%s\n", w.MessageType)
	fmt.Fprintf(&b, "mmsi=%s\n", i64Str(w.MMSI))
	fmt.Fprintf(&b, "imo=%s\n", i64Str(w.IMO))
	fmt.Fprintf(&b, "call_sign=%s\n", w.CallSign)
	fmt.Fprintf(&b, "ship_name=%s\n", w.ShipName)
	fmt.Fprintf(&b, "latitude=%s\n", f64Str(w.Latitude))
	fmt.Fprintf(&b, "longitude=%s\n", f64Str(w.Longitude))
	fmt.Fprintf(&b, "cog=%s\n", f64Str(w.Cog))
	fmt.Fprintf(&b, "sog=%s\n", f64Str(w.Sog))
	fmt.Fprintf(&b, "nav_status=%s\n", intStr(w.NavStatus))
	fmt.Fprintf(&b, "destination=%s\n", w.Destination)
	fmt.Fprintf(&b, "timestamp_utc=%d\n", w.TimestampUTC)
	return []byte(b.String())
}

func i64Str(p *int64) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.FormatInt(*p, 10)
}

func f64Str(p *float64) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.FormatFloat(*p, 'f', 6, 64)
}

func intStr(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.Itoa(*p)
}

// sha256Hex is the one hashing primitive this package uses for both
// payload_hash (over raw bytes) and canonical_hash (over
// canonicalBytes) — always SHA-256, always hex-encoded.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// subscriptionMessage is the outbound frame this connector sends right
// after a successful connect, carrying the API key and this
// subscription's configured filters to the server side. Field names
// loosely follow publicly documented AISstream subscription
// conventions; like wireMessage, this is illustrative and not a claim
// of exact wire compatibility.
type subscriptionMessage struct {
	APIKey             string        `json:"APIKey"`
	BoundingBoxes      []BoundingBox `json:"BoundingBoxes,omitempty"`
	FilterShipMMSI     []string      `json:"FilterShipMMSI,omitempty"`
	FilterMessageTypes []string      `json:"FilterMessageTypes,omitempty"`
}

// decodeWireMessage unmarshals raw and validates the required-field
// contract in one step. Any failure — invalid JSON or a missing
// required field — is a malformed message.
func decodeWireMessage(raw []byte) (wireMessage, error) {
	var w wireMessage
	if err := json.Unmarshal(raw, &w); err != nil {
		return wireMessage{}, fmt.Errorf("invalid json: %w", err)
	}
	if err := w.validate(); err != nil {
		return wireMessage{}, err
	}
	return w, nil
}
