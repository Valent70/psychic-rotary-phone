package aisstream

import "errors"

// Sentinel errors, matching this repo's established errors.New +
// fmt.Errorf("%w: ...", ...) wrapping convention (see e.g.
// pkg/evidence/provenance/provenance.go and pkg/evidence/ontology).
var (
	// ErrConfigInvalid is returned by Config.validate and by New when
	// a required dependency (handler, secret provider, transport) is
	// missing.
	ErrConfigInvalid = errors.New("aisstream: invalid configuration")
	// ErrTransportNil is returned by New when transport is nil.
	ErrTransportNil = errors.New("aisstream: transport is nil")
	// ErrSecretProviderNil is returned by New when secrets is nil.
	ErrSecretProviderNil = errors.New("aisstream: secret provider is nil")
	// ErrMissingAPIKey is returned when SecretProvider.APIKey()
	// succeeds but returns an empty/blank key.
	ErrMissingAPIKey = errors.New("aisstream: API key is empty")
	// ErrMalformedMessage wraps every reason a raw message was
	// rejected: invalid JSON, or valid JSON missing a required field.
	// Malformed messages are always counted and reported to Handler,
	// never silently dropped.
	ErrMalformedMessage = errors.New("aisstream: malformed message")
	// ErrMaxRetriesExceeded is returned by Run when Config.MaxRetries
	// consecutive reconnect failures have occurred.
	ErrMaxRetriesExceeded = errors.New("aisstream: maximum reconnect attempts exceeded")
	// ErrProvenanceInvalid is returned (and Run stops entirely,
	// fail-closed, rather than masking it with a reconnect) if a
	// constructed provenance.ExternalEvidence ever fails Validate.
	// Given how handleRaw populates every required field, this should
	// never trigger in practice; it exists as the fail-closed backstop
	// the audit mandate requires.
	ErrProvenanceInvalid = errors.New("aisstream: constructed provenance envelope failed validation")
)
