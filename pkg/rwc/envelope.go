package rwc

import (
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/envelope"
)

// EnvelopeGateID is the gate this corpus's evidence is ABOUT. RWC v2
// exercises real-world-derived maritime/commodity data, so the gate it
// speaks to is live_data — and the whole point of declaring it is that a
// FIXTURE envelope can never qualify that gate. Declaring the gate and
// being refused for it is a stronger, checkable statement than saying
// nothing.
const EnvelopeGateID = "live_data"

// EnvelopeOrigin is this corpus's honest origin class.
//
// It is REAL_DERIVED_BENCHMARK, not SYNTHETIC and not any
// REAL_EXTERNAL_* class. The figures in ports.go and rwc002.go are
// literal transcriptions of real port-authority and broker-supplied
// values, so calling them SYNTHETIC would understate them. But no value
// in this corpus was fetched from, or attested by, the originating
// authority through any channel this system controls, so claiming
// REAL_EXTERNAL_UNVERIFIED — which pkg/connector's real adapters use for
// data that genuinely arrived over a wire from a named provider —
// would overstate them. REAL_DERIVED_BENCHMARK is the class that
// actually fits: real-world-derived reference data, entered by hand.
const EnvelopeOrigin = provenance.OriginRealDerivedBenchmark

// EnvelopeClassification is FIXTURE, unconditionally.
//
// The corpus DATA is real-world-derived (see EnvelopeOrigin), but the
// MEASUREMENT this envelope carries is a run of this repository against
// its own in-process engines in a sandbox with no live infrastructure,
// no network egress, and no third-party attestation. Under
// pkg/governance/envelope's own vocabulary that is a FIXTURE, and
// declaring it is the conservative under-claim envelope.Check explicitly
// permits (see its "declaring a real-origin measurement as a fixture is
// a conservative under-claim" branch).
const EnvelopeClassification = envelope.ClassificationFixture

// EnvelopeProviderID is deliberately NOT a registered trust anchor.
// pkg/governance/qualification.TrustRegistry has no entry for it and
// none is added by this package, so envelope.Validator.Check refuses
// this envelope with ErrUnknownProvider before it can qualify anything.
// That refusal is the mechanism, not an oversight: see
// TestCorpusEnvelopeCannotQualifyABlockedGate.
const EnvelopeProviderID = "veriqo-rwc-v2-corpus"

// ReleaseIdentity is the release an RWC v2 evidence bundle belongs to.
// Every field is a value the caller must genuinely possess.
// envelope.Envelope.Validate refuses an envelope missing any of them, so
// there is no path here that invents one.
type ReleaseIdentity struct {
	Release    string
	Commit     string
	SourceHash string
	BinaryHash string
	SBOMHash   string
}

// Complete reports whether every field of the release identity is
// present. A caller that cannot supply one must not emit an envelope at
// all — see cmd/veriqo-rwc-v2, which writes a named refusal into the
// bundle rather than filling a blank with a plausible-looking value.
func (r ReleaseIdentity) Complete() bool {
	return r.Release != "" && r.Commit != "" && r.SourceHash != "" &&
		r.BinaryHash != "" && r.SBOMHash != ""
}

// Limitations is what an RWC v2 evidence bundle explicitly does NOT
// prove. envelope.Envelope.Validate requires a FIXTURE envelope to
// declare at least one, "because a fixture that declares no limitation
// is precisely the artifact this project exists not to produce".
//
// Every line below is a statement about this specific corpus that was
// checked against the code before being written here, not a generic
// disclaimer.
func Limitations() []string {
	return []string{
		"No live data feed of any kind was consulted: no AIS, no port-authority " +
			"system, no vessel registry, no tide service. Every figure in this corpus " +
			"was entered by hand from supplied real-world text.",
		"RWC-002's vessel identity check is deterministic offline arithmetic on the " +
			"claimed identifiers themselves (IMO check digit per IMO Resolution " +
			"A.600(15), MMSI MID prefix lookup against a small local table). It can " +
			"prove a claimed identifier is internally malformed; it cannot confirm that " +
			"the vessel exists, or that this identifier belongs to it.",
		"The MagicPort and MarineTraffic references in the RWC-002 corpus are " +
			"themselves broker assertions that those sources were checked. Neither was " +
			"queried by this system, so nothing here corroborates them.",
		"The ledger anchor recorded per case is an in-process, in-memory hash-chain " +
			"head (pkg/moat/fusion.Engine.Head). It is not persisted, not written to a " +
			"write-ahead log, and not anchored to any external system.",
		"The replay performed per case is an in-process re-execution through a fresh " +
			"pkg/canonical.Pipeline. Cross-process cold DAG replay is a separate " +
			"capability with its own binary (cmd/veriqo-cold-replay); this bundle " +
			"exports the request bytes for it but does not itself run it.",
		"The environment is a sandbox with no external infrastructure. Nothing in " +
			"this bundle bears on hsm_kms, multi_region_dr, pentest, " +
			"scale_qualification, soak_72h, spire_mtls or supply_chain_scan, and it " +
			"cannot qualify live_data.",
	}
}

// CorpusEnvelope builds the declared-FIXTURE evidence envelope for one
// RWC v2 run. It mirrors pkg/insurance/casepack.Case.FixtureEnvelope's
// caller-supplied-identity shape deliberately: this repository has one
// envelope contract, and a second way to describe fixture evidence would
// be exactly the duplicate abstraction that contract exists to prevent.
//
// arts must be the REAL artifacts of the run (name, content hash, byte
// size); ArtifactRootHash is recomputed from them here rather than
// accepted from a caller, and envelope.Validate independently recomputes
// it again.
func CorpusEnvelope(id ReleaseIdentity, arts []envelope.Artifact, measurement map[string]string, validFrom, validUntil uint64) envelope.Envelope {
	return envelope.Envelope{
		ContractVersion:  envelope.ContractVersion,
		GateID:           EnvelopeGateID,
		Release:          id.Release,
		Commit:           id.Commit,
		SourceHash:       id.SourceHash,
		BinaryHash:       id.BinaryHash,
		SBOMHash:         id.SBOMHash,
		Environment:      "ci-sandbox",
		Measurement:      measurement,
		Artifacts:        arts,
		ArtifactRootHash: envelope.ArtifactRoot(arts),
		ProviderID:       EnvelopeProviderID,
		ReviewerID:       EnvelopeProviderID,
		ValidFrom:        validFrom,
		ValidUntil:       validUntil,
		Limitations:      Limitations(),
		OriginKind:       EnvelopeOrigin,
		RightsState:      provenance.RightsInternalOnly,
		Attestation:      provenance.AttestationSelfAsserted,
		Classification:   EnvelopeClassification,
	}
}
