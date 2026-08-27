// Package casepack is the synthetic case pack the Final Design §33
// mandates: CASE-INS-001 through CASE-INS-007, built now, from nothing
// but this repository, so that engineering never waits for customer
// data ("Claude tidak boleh menunggu customer").
//
// Its purpose is stated by the design document itself and is worth
// repeating because it governs every choice below: **"Tujuannya bukan
// investor. Tujuannya complete engineering coverage."** These cases
// exist to drive every path of the insurance domain end to end — not to
// look impressive.
//
// # Everything here is fictional, and that is enforced
//
// Every vessel, company, person, port, jurisdiction, policy number and
// document reference in this package is invented. Jurisdictions are
// generic ("Jurisdiction A", "Jurisdiction B"); flag states are
// invented; parties are named by ROLE plus an invented name.
//
// Where a case is *inspired by* a real CATEGORY of dispute — general
// average versus war-risk cover, an intermediary bribery-risk pattern,
// a regulatory settlement structure — it is kept as an abstracted
// scenario TYPE, exactly as the Final Design itself models CASE-INS-005
// (fully generic, no real names at all). No real company, vessel or
// individual is named, no real judgment is reproduced or used as a
// rule, and no case output is a guilt or liability finding about
// anyone, fictional or otherwise.
//
// TestNoRealWorldEntityAppearsInThePack enforces the naming rule by
// scanning this package's own source, so the constraint survives a
// future author who has not read the design documents.
//
// # Synthetic is labelled synthetic
//
// Every case declares Origin = provenance.OriginSynthetic and
// Classification = envelope.ClassificationFixture. The Final Design
// forbids mixing synthetic with live data and forbids calling a
// historical replay "live"; this package's Case type has no field that
// could claim live provenance, and
// TestAFixtureCaseCanNeverReportAsLive proves it.
//
// # The seven cases
//
//	CASE-INS-001  Port-call / demurrage        laytime chronology contradiction
//	CASE-INS-002  Cargo damage / reefer        temperature excursion, notice, mitigation
//	CASE-INS-003  Commodity document integrity quantity mismatch across documents
//	CASE-INS-004  General average vs war risk  LEGAL_INTERPRETATION_REQUIRED
//	CASE-INS-005  Bribery-risk intermediary    HIGH_RISK_TRANSACTION, NO_BRIBERY_DETERMINATION
//	CASE-INS-006  Regulatory settlement        settlement != proven, monitor != completed
//	CASE-INS-007  Cross-border maritime dispute forum, holds, competing positions
package casepack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	evidenceapi "veriqo/pkg/evidence/api"
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/governance/envelope"
	insevidence "veriqo/pkg/insurance/evidence"
	"veriqo/pkg/insurance/party"
)

// CaseID is one of the seven synthetic case identifiers.
type CaseID string

const (
	CasePortCallDemurrage    CaseID = "CASE-INS-001"
	CaseCargoDamageReefer    CaseID = "CASE-INS-002"
	CaseCommodityDocuments   CaseID = "CASE-INS-003"
	CaseGeneralAverage       CaseID = "CASE-INS-004"
	CaseBriberyRisk          CaseID = "CASE-INS-005"
	CaseRegulatorySettlement CaseID = "CASE-INS-006"
	CaseCrossBorderDispute   CaseID = "CASE-INS-007"
)

// AllCaseIDs returns the seven identifiers in order.
func AllCaseIDs() []CaseID {
	return []CaseID{
		CasePortCallDemurrage, CaseCargoDamageReefer, CaseCommodityDocuments,
		CaseGeneralAverage, CaseBriberyRisk, CaseRegulatorySettlement,
		CaseCrossBorderDispute,
	}
}

// Origin is the provenance classification every case in this pack
// carries. It is a constant, not a field, so no case can declare
// anything else.
const Origin = provenance.OriginSynthetic

// Classification is the envelope classification every case in this pack
// carries. Also a constant, for the same reason.
const Classification = envelope.ClassificationFixture

// Limitations is what a synthetic case explicitly does NOT establish.
// The envelope contract requires a fixture to declare its limitations,
// and these are this pack's, stated once.
func Limitations() []string {
	return []string{
		"This is a synthetic case constructed for engineering coverage. It establishes " +
			"that the insurance domain's code paths execute end to end; it establishes nothing " +
			"whatever about any real vessel, cargo, party, policy, claim or dispute.",
		"No figure, time, document or party in this case corresponds to any real-world fact.",
		"Passing this case is not qualification of any capability against live data.",
	}
}

// ---- The scenario time scale -----------------------------------------
//
// pkg/insurance/timeline is the one package in pkg/insurance that
// commits to a concrete time unit: Event.EventTimeUTC is documented as
// Unix epoch seconds, while evidence.Record and the rest of the domain
// use an opaque tick. A synthetic case has to satisfy BOTH, and a case
// that used one scale for its evidence and another for its events would
// manufacture timeline conflicts that are artifacts of the mismatch
// rather than properties of the scenario.
//
// So every case declares its times on ONE scale — scenario hours — and
// both the evidence and the events are projected from it. 1 hour-tick =
// 1 hour of the scenario's own timeline; ScenarioEpoch is an arbitrary
// fixed base far from any real date.
const (
	// ScenarioEpoch is the Unix-epoch second every scenario's hour 0 maps
	// to. Deliberately a round, obviously-synthetic value.
	ScenarioEpoch uint64 = 1_800_000_000
	// SecondsPerHourTick is the projection factor.
	SecondsPerHourTick uint64 = 3600
)

// EpochSeconds projects a scenario hour-tick onto the Unix-epoch-second
// scale timeline.Event requires. Every case's evidence and events go
// through it, so the two are always on the same scale.
func EpochSeconds(hourTick uint64) uint64 {
	return ScenarioEpoch + hourTick*SecondsPerHourTick
}

// ---- The case descriptor --------------------------------------------

// Party is one fictional participant, named by role plus an invented
// name. EntityRef is an invented canonical-identity reference so the
// case can exercise the lineage ENTITY path.
type Party struct {
	ID        party.PartyID
	Name      string
	Roles     []party.Role
	EntityRef string
}

// EvidenceSpec describes one piece of synthetic evidence to build. It
// is a spec rather than a built record so each case can be constructed
// fresh (content-addressed IDs are derived from content, so a rebuilt
// case reproduces identical EvidenceIDs — which is what makes these
// cases deterministic fixtures).
type EvidenceSpec struct {
	// Key is a stable, human-readable handle used within a case to refer
	// to this record (e.g. "NOR", "AIS_ARRIVAL"). It is NOT the
	// EvidenceID — that comes from the content hash.
	Key string
	// Subject/Predicate/Object/Source are the ontology fields. They are
	// what the content hash is over.
	Subject   string
	Predicate string
	Object    string
	Source    string
	// ObservedAt is the tick the source reports.
	ObservedAt uint64
	// DocumentType is the insurance-domain document classification.
	DocumentType string
	// SourceParty is who submitted it.
	SourceParty party.PartyID
	// EvidenceOrigin is the insurance-domain origin classification.
	EvidenceOrigin insevidence.Origin
	// Rights is the rights state to record. Defaults to
	// UNKNOWN_PENDING_CONTRACT when unset, matching evidence.New.
	Rights provenance.RightsState
	// Attributes carries any extra recorded facts (e.g. a stated
	// quantity), which also enter the content hash.
	Attributes map[string]string
}

// Case is one synthetic case's declarative description. Deliberately a
// DESCRIPTION and not a pre-built object graph: each Build* function in
// scenarios.go turns it into real domain objects through the same
// facade a production case uses, which is the Final Design's §20 "one
// engine, not two" rule applied to fixtures.
//
// There is no field on this type that could claim live provenance, a
// verdict, a liability finding or a confidence score — see the guardrail
// tests.
type Case struct {
	ID CaseID
	// Title is a short description of the scenario TYPE.
	Title string
	// Narrative is what the case is about, in neutral terms.
	Narrative string
	// EngineeringCoverage names which code paths this case exists to
	// exercise. This is the case's real purpose (Final Design §33).
	EngineeringCoverage []string
	// ExpectedOutputs names, in the design documents' own vocabulary,
	// what the case must produce. These are STATUS words like
	// LEGAL_INTERPRETATION_REQUIRED or NO_BRIBERY_DETERMINATION — never
	// a determination about anyone.
	ExpectedOutputs []string

	Parties  []Party
	Evidence []EvidenceSpec
}

var (
	ErrUnknownCase   = errors.New("casepack: unknown synthetic case ID")
	ErrDuplicateKey  = errors.New("casepack: duplicate evidence key within a case")
	ErrEmptyKey      = errors.New("casepack: every evidence spec needs a stable key")
	ErrNoParties     = errors.New("casepack: every synthetic case needs at least one party")
	ErrNoEvidence    = errors.New("casepack: every synthetic case needs at least one piece of evidence")
	ErrNoCoverage    = errors.New("casepack: every synthetic case must declare which code paths it exercises")
	ErrNoExpectation = errors.New("casepack: every synthetic case must declare its expected outputs")
)

// Validate checks a case descriptor's own internal consistency.
func (c Case) Validate() error {
	if c.ID == "" {
		return ErrUnknownCase
	}
	if len(c.Parties) == 0 {
		return ErrNoParties
	}
	if len(c.Evidence) == 0 {
		return ErrNoEvidence
	}
	if len(c.EngineeringCoverage) == 0 {
		return ErrNoCoverage
	}
	if len(c.ExpectedOutputs) == 0 {
		return ErrNoExpectation
	}
	seen := map[string]bool{}
	for _, e := range c.Evidence {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if seen[e.Key] {
			return fmt.Errorf("%w: %s", ErrDuplicateKey, e.Key)
		}
		seen[e.Key] = true
	}
	return nil
}

// ---- Building real evidence from a spec ------------------------------

// BuildEvidence turns one spec into a real, content-addressed
// insurance evidence record. The EvidenceID is derived by
// pkg/evidence/ontology from the content — this package mints nothing,
// exactly like every other producer of insurance evidence.
func (c Case) BuildEvidence(spec EvidenceSpec) (insevidence.Record, error) {
	// origin_class is stamped by evidenceapi.SyntheticDocument itself, in
	// the hashed attributes, so a record's synthetic provenance cannot be
	// separated from the record and cannot be overridden here.
	attrs := map[string]string{
		"synthetic_case": string(c.ID),
		// document_hash is required by pkg/evidence/ontology for a
		// Document. For a synthetic record there is no real document to
		// hash, so the value is derived deterministically from the case
		// and the spec key and is explicitly labelled synthetic — never a
		// fabricated hex digest that could be mistaken for the digest of a
		// real file.
		"document_hash": "synthetic:" + string(c.ID) + ":" + spec.Key,
	}
	for k, v := range spec.Attributes {
		attrs[k] = v
	}
	// Minted through the canonical evidence contract, never by an
	// ontology.Evidence literal here. internal/nobypass's
	// canonical_evidence_production_coverage gate counts every file that
	// constructs canonical evidence outside the audited source -> adapter
	// -> contract chain; a fixture producer minting its own records would
	// be exactly such a second ingestion path. Widening that allowlist to
	// accommodate test data would weaken a real gate, so the construction
	// lives inside the contract instead — see
	// evidenceapi.SyntheticDocument, which also stamps the SYNTHETIC
	// origin into the hashed attributes.
	//
	// ObservedAt is projected onto the same scale the case's events use
	// (EpochSeconds). Without that projection the timeline's
	// document-issued-after-event check fires on every record, as an
	// artifact of two different time scales rather than a property of the
	// scenario.
	ev, err := evidenceapi.SyntheticDocument(
		spec.Subject, spec.Predicate, spec.Object, spec.Source,
		EpochSeconds(spec.ObservedAt), 0.9, attrs)
	if err != nil {
		return insevidence.Record{}, fmt.Errorf("casepack: building evidence %s/%s: %w", c.ID, spec.Key, err)
	}
	rec, err := insevidence.New(string(c.ID), ev, spec.SourceParty, spec.EvidenceOrigin)
	if err != nil {
		return insevidence.Record{}, fmt.Errorf("casepack: wrapping evidence %s/%s: %w", c.ID, spec.Key, err)
	}
	rec.DocumentType = spec.DocumentType
	if spec.Rights != "" {
		rec.Rights = spec.Rights
	}
	return rec, nil
}

// BuiltEvidence is a case's evidence, built and keyed both ways: by the
// case's own stable key and by content-addressed EvidenceID.
type BuiltEvidence struct {
	ByKey   map[string]insevidence.Record
	Records []insevidence.Record
}

// ID returns the content-addressed EvidenceID for a case-local key.
func (b BuiltEvidence) ID(key string) string { return b.ByKey[key].EvidenceID() }

// IDs returns the content-addressed EvidenceIDs for several keys, in
// the order given.
func (b BuiltEvidence) IDs(keys ...string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, b.ByKey[k].EvidenceID())
	}
	return out
}

// BuildAllEvidence builds every record in the case, in declaration
// order.
func (c Case) BuildAllEvidence() (BuiltEvidence, error) {
	out := BuiltEvidence{ByKey: map[string]insevidence.Record{}}
	for _, spec := range c.Evidence {
		rec, err := c.BuildEvidence(spec)
		if err != nil {
			return BuiltEvidence{}, err
		}
		out.ByKey[spec.Key] = rec
		out.Records = append(out.Records, rec)
	}
	return out, nil
}

// PartySpecs returns the case's parties in declaration order.
func (c Case) PartySpecs() []Party { return c.Parties }

// ContentHash is a deterministic SHA-256 over the case's declarative
// content: its identity, its parties, and every evidence spec's hashed
// fields. Two runs of the same source produce the same hash, and any
// edit to a case changes it — which is what lets a fixture case be
// referenced by hash in an envelope or a governance document without
// that reference silently going stale.
//
// Hand-rolled and field-ordered for the same cross-language
// reproducibility reason every other hash in this codebase documents.
func (c Case) ContentHash() string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.insurance.casepack/v1\nid=%s\ntitle=%s\n", c.ID, c.Title)
	for _, p := range c.Parties {
		fmt.Fprintf(&b, "party=%s|%s|%s\n", p.ID, p.Name, p.EntityRef)
		roles := make([]string, 0, len(p.Roles))
		for _, r := range p.Roles {
			roles = append(roles, string(r))
		}
		sort.Strings(roles)
		for _, r := range roles {
			fmt.Fprintf(&b, "role=%s\n", r)
		}
	}
	for _, e := range c.Evidence {
		fmt.Fprintf(&b, "evidence=%s|%s|%s|%s|%s|%d|%s|%s|%s\n",
			e.Key, e.Subject, e.Predicate, e.Object, e.Source,
			e.ObservedAt, e.DocumentType, e.SourceParty, e.EvidenceOrigin)
		keys := make([]string, 0, len(e.Attributes))
		for k := range e.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "attr=%s=%s\n", k, e.Attributes[k])
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// EvidenceTypes returns the distinct document types this case uses,
// sorted — the set a preservation order over this case must declare.
func (c Case) EvidenceTypes() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range c.Evidence {
		if e.DocumentType != "" && !seen[e.DocumentType] {
			seen[e.DocumentType] = true
			out = append(out, e.DocumentType)
		}
	}
	sort.Strings(out)
	return out
}

// FixtureEnvelope builds the declared-fixture evidence envelope for this
// case. It is a FIXTURE by construction and carries this pack's
// limitations, which is why
// canonical.Binding.AttachExternalEvidence will refuse it as external
// evidence — exactly the behaviour the "never mix synthetic with live"
// rule requires.
func (c Case) FixtureEnvelope(release, commit, sourceHash, binaryHash, sbomHash string, validFrom, validUntil uint64) envelope.Envelope {
	arts := []envelope.Artifact{{
		Name:  string(c.ID) + "-synthetic-case",
		Hash:  c.ContentHash(),
		Bytes: uint64(len(c.Evidence)),
	}}
	return envelope.Envelope{
		ContractVersion:  envelope.ContractVersion,
		GateID:           "live_data",
		Release:          release,
		Commit:           commit,
		SourceHash:       sourceHash,
		BinaryHash:       binaryHash,
		SBOMHash:         sbomHash,
		Environment:      "ci-sandbox",
		Measurement:      map[string]string{"evidence_records": fmt.Sprint(len(c.Evidence))},
		Artifacts:        arts,
		ArtifactRootHash: envelope.ArtifactRoot(arts),
		ProviderID:       "veriqo-internal-synthetic",
		ReviewerID:       "veriqo-internal-synthetic",
		ValidFrom:        validFrom,
		ValidUntil:       validUntil,
		Limitations:      Limitations(),
		OriginKind:       Origin,
		RightsState:      provenance.RightsInternalOnly,
		Attestation:      provenance.AttestationSelfAsserted,
		Classification:   Classification,
	}
}

// Registry -------------------------------------------------------------

// Get returns one synthetic case by ID.
func Get(id CaseID) (Case, error) {
	for _, c := range All() {
		if c.ID == id {
			return c, nil
		}
	}
	return Case{}, fmt.Errorf("%w: %s", ErrUnknownCase, id)
}

// All returns all seven synthetic cases, in order.
func All() []Case {
	return []Case{
		casePortCallDemurrage(),
		caseCargoDamageReefer(),
		caseCommodityDocuments(),
		caseGeneralAverage(),
		caseBriberyRisk(),
		caseRegulatorySettlement(),
		caseCrossBorderDispute(),
	}
}
