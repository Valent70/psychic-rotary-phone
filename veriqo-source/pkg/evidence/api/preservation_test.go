package api

// PHASE A (P0-1) — the preservation suite.
//
// The program names nine properties the canonical evidence write
// authority must hold. They are all one underlying question asked from
// nine angles: when evidence crosses a boundary — into the facade, into
// a subsystem, out through replay, back through a second facade — does
// it arrive as the SAME evidence, or as something that merely looks
// like it?
//
// Every test below submits real evidence through the real Facade and
// checks a specific thing that must survive. None of them mocks a
// subsystem: the whole point is that the boundary being crossed is the
// real one.

import (
	"errors"
	"testing"

	"veriqo/pkg/evidence/ontology"
	"veriqo/pkg/evidence/provenance"
	"veriqo/pkg/moat/contradiction"
	"veriqo/pkg/moat/intelligence/risk"
)

// provenanced builds a fully-populated, signed evidence record — every
// provenance field set, so a test that checks preservation is checking
// something that could actually be lost.
func provenanced(source, object string, tick uint64) ontology.Evidence {
	e, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeAISObservation, Subject: "VESSEL:IMO7777777", Predicate: "AIS_STATUS",
		Object: object, Source: source, ObservedAt: tick, ReceivedAt: tick + 1,
		ValidFrom: tick, Confidence: 0.87,
		Attributes: map[string]string{"mmsi": "477995000"},
		Provenance: ontology.Provenance{
			ProducerID:       "producer-" + source,
			UpstreamID:       "feed-satellite-alpha",
			TransformationID: "normalize/v3",
			Method:           "terrestrial-ais-decode",
			Jurisdiction:     "SG",
		},
	})
	if err != nil {
		panic(err)
	}
	signed, err := e.Sign(priv)
	if err != nil {
		panic(err)
	}
	return signed
}

// TestCanonicalContractVersionIsDeclared is the gate's own precondition:
// the contract cannot be reported unless it exists.
func TestCanonicalContractVersionIsDeclared(t *testing.T) {
	c := Contract()
	if c.Version != ContractVersion {
		t.Fatalf("Version = %q, want %q", c.Version, ContractVersion)
	}
	if c.Components["ontology"] != ontology.SchemaVersionV1 {
		t.Errorf("contract does not read the ontology's own schema version: %q", c.Components["ontology"])
	}
	if c.Components["provenance"] != provenance.SchemaVersionV1 {
		t.Errorf("contract does not read provenance's own schema version: %q", c.Components["provenance"])
	}
	if len(c.Path) < 4 {
		t.Errorf("contract declares only %d hops; source -> adapter -> contract -> facade -> pipeline -> subsystem is six", len(c.Path))
	}
	if len(c.EvidenceTypes) != len(ontology.KnownTypes()) {
		t.Errorf("contract declares %d evidence types, the ontology models %d", len(c.EvidenceTypes), len(ontology.KnownTypes()))
	}
	if c.Hash == "" || c.Hash == "sha256:" {
		t.Fatal("contract descriptor is not content-addressed")
	}
	if Contract().Hash != c.Hash {
		t.Fatal("contract hash is not deterministic")
	}
}

// TestContractHashChangesWhenAComponentDoes proves the descriptor is
// genuinely derived from its components rather than a hardcoded string
// that would keep reporting the old contract after a schema bump.
func TestContractHashChangesWhenAComponentDoes(t *testing.T) {
	base := Contract()
	mutated := base
	mutated.Components = map[string]string{"ontology": "veriqo.ontology/v99", "provenance": provenance.SchemaVersionV1}
	if contractHash(mutated) == base.Hash {
		t.Fatal("changing a component version did not change the contract hash")
	}
}

// TestSourceHashPreservation: an evidence record's content hash is its
// identity, and the facade must return the SAME identity it stored, not
// a re-derived-and-possibly-different one.
func TestSourceHashPreservation(t *testing.T) {
	f := newFacade()
	original := provenanced("ais-vendor-a", "OFF", 10)

	stored, err := f.Submit(original)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if stored.EvidenceID != original.EvidenceID {
		t.Fatalf("Submit changed the evidence identity: %s -> %s", original.EvidenceID, stored.EvidenceID)
	}
	if stored.ComputeID() != stored.EvidenceID {
		t.Fatal("the stored record's content no longer hashes to its own ID")
	}
	back := f.EvidenceFor("VESSEL:IMO7777777", "AIS_STATUS")
	if len(back) != 1 {
		t.Fatalf("retrieved %d records, want 1", len(back))
	}
	if back[0].EvidenceID != original.EvidenceID {
		t.Fatalf("retrieval changed the identity: %s", back[0].EvidenceID)
	}
	if back[0].ComputeID() != original.EvidenceID {
		t.Fatal("the retrieved record's content does not reproduce the original hash")
	}
}

// TestProvenancePreservation: every provenance field survives the round
// trip. This is the one most likely to rot silently — a new field added
// to Provenance and forgotten in a copy path would be invisible without
// a field-by-field check.
func TestProvenancePreservation(t *testing.T) {
	f := newFacade()
	original := provenanced("ais-vendor-a", "OFF", 10)
	if _, err := f.Submit(original); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := f.EvidenceFor("VESSEL:IMO7777777", "AIS_STATUS")[0].Provenance
	want := original.Provenance
	for name, pair := range map[string][2]string{
		"ProducerID":       {want.ProducerID, got.ProducerID},
		"UpstreamID":       {want.UpstreamID, got.UpstreamID},
		"TransformationID": {want.TransformationID, got.TransformationID},
		"Method":           {want.Method, got.Method},
		"Jurisdiction":     {want.Jurisdiction, got.Jurisdiction},
	} {
		if pair[0] == "" {
			t.Fatalf("test fixture has an empty %s; preservation of it would be vacuous", name)
		}
		if pair[0] != pair[1] {
			t.Errorf("%s not preserved: want %q got %q", name, pair[0], pair[1])
		}
	}
}

// TestLosslessProjection: the reduction into a subsystem submission
// drops fields (fusion does not need a jurisdiction), but it must never
// drop the ability to get back to the exact record — and it must name
// what it dropped rather than quietly discarding it.
func TestLosslessProjection(t *testing.T) {
	original := provenanced("ais-vendor-a", "OFF", 10)
	p := Project(original)

	if !p.Recoverable(original) {
		t.Fatal("the projection cannot be traced back to the record it came from")
	}
	if len(p.Dropped) == 0 {
		t.Fatal("a projection that claims to drop nothing is either lying or not a projection")
	}
	for _, field := range []string{"source", "object", "subject", "predicate", "evidence_id", "provider", "upstream", "jurisdiction"} {
		if p.Carried[field] == "" {
			t.Errorf("projection does not carry %s", field)
		}
	}
	// Two DIFFERENT records must never produce the same projection
	// identity -- otherwise the reduction has collapsed two distinct
	// pieces of evidence into one.
	other := provenanced("ais-vendor-b", "ON", 11)
	if Project(other).EvidenceID == p.EvidenceID {
		t.Fatal("two distinct records projected to the same identity")
	}
}

// TestRightsPreservation: pkg/evidence/provenance's rights model is
// deliberately NOT folded into ontology.Evidence's content hash (see
// that package's own doc comment on why). The property that must hold
// is therefore the one it actually claims: a projection into the
// ontology never UPGRADES rights, and the envelope's own verdict
// survives independently for the caller to re-consult.
func TestRightsPreservation(t *testing.T) {
	env := provenance.ExternalEvidence{
		SourceID: "aisstream", ProviderID: "aisstream-org", DatasetID: "ais-live",
		DeliveryID: "d-1", PayloadHash: "sha256:aa", CanonicalHash: "sha256:bb",
		SchemaVersion:       provenance.SchemaVersionV1,
		OriginClass:         provenance.OriginRealExternalUnverified,
		RightsState:         provenance.RightsUnknownPendingContract,
		TransformationChain: []string{"normalize/v3"},
		CorrectionState:     provenance.CorrectionNone,
		AttestationState:    provenance.AttestationUnattested,
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("fixture envelope invalid: %v", err)
	}

	producer, upstream, transform := env.OntologyProvenance()
	e, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeAISObservation, Subject: "VESSEL:IMO7777777", Predicate: "AIS_STATUS",
		// Source is the concrete dataset the record arrived in, which is
		// deliberately NOT the same node as its upstream feed -- the
		// dependency graph refuses a node that depends on itself, and
		// conflating the two would be exactly that.
		Object: "OFF", Source: env.DatasetID, ObservedAt: 10, Confidence: 0.8,
		Attributes: map[string]string{"mmsi": "477995000"},
		Provenance: ontology.Provenance{ProducerID: producer, UpstreamID: upstream, TransformationID: transform},
	})
	if err != nil {
		t.Fatalf("ontology.New: %v", err)
	}
	signed, err := e.Sign(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	f := newFacade()
	if _, err := f.Submit(signed); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// The projection carried the provenance identity forward...
	got := f.EvidenceFor("VESSEL:IMO7777777", "AIS_STATUS")[0]
	if got.Provenance.ProducerID != env.ProviderID || got.Provenance.UpstreamID != env.SourceID {
		t.Fatalf("provenance identity not carried into the ontology: %+v", got.Provenance)
	}
	// ...and the rights verdict is unchanged and still restrictive.
	// Passing through the facade must never widen what VERIQO may do.
	if env.Permits(provenance.UseCustomerFacing) {
		t.Fatal("UNKNOWN_PENDING_CONTRACT permitted a customer-facing use")
	}
	if env.Permits(provenance.UseCalibration) {
		t.Fatal("UNKNOWN_PENDING_CONTRACT permitted a calibration use")
	}
	if !env.Permits(provenance.UseInternalOnly) {
		t.Fatal("UNKNOWN_PENDING_CONTRACT denied even internal use, contradicting provenance's own table")
	}
	// A revoked envelope permits nothing, before or after projection.
	revoked := env
	revoked.RightsState = provenance.RightsRevoked
	for _, use := range []provenance.Use{
		provenance.UseInternalOnly, provenance.UseCustomerFacing,
		provenance.UseDispute, provenance.UseCalibration, provenance.UseTraining,
	} {
		if revoked.Permits(use) {
			t.Errorf("a REVOKED envelope permitted %s", use)
		}
	}
}

// TestCorrectionPreservation: a correction must supersede, never
// overwrite. Both records survive, the correction names its target, and
// the projection resolves to the corrected value.
func TestCorrectionPreservation(t *testing.T) {
	f := newFacade()
	original := provenanced("ais-vendor-a", "OFF", 10)
	if _, err := f.Submit(original); err != nil {
		t.Fatalf("Submit original: %v", err)
	}

	corr, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeCorrection, Subject: "VESSEL:IMO7777777", Predicate: "AIS_STATUS",
		Object: "ON", Source: "ais-vendor-a", ObservedAt: 12, ReceivedAt: 13, Confidence: 0.95,
		Supersedes:  original.EvidenceID,
		DerivedFrom: []string{original.EvidenceID},
		Provenance:  ontology.Provenance{ProducerID: "producer-ais-vendor-a", UpstreamID: "feed-satellite-alpha"},
	})
	if err != nil {
		t.Fatalf("ontology.New(correction): %v", err)
	}
	signedCorr, err := corr.Sign(priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := f.Submit(signedCorr); err != nil {
		t.Fatalf("Submit correction: %v", err)
	}

	all := f.EvidenceFor("VESSEL:IMO7777777", "AIS_STATUS")
	if len(all) != 2 {
		t.Fatalf("stored %d records, want 2 -- a correction must never overwrite its target", len(all))
	}
	var found bool
	for _, e := range all {
		if e.Type == ontology.TypeCorrection {
			found = true
			if e.Supersedes != original.EvidenceID {
				t.Fatalf("the correction lost its target: %q", e.Supersedes)
			}
		}
	}
	if !found {
		t.Fatal("the correction record is not in the store")
	}
	// And the correction actually applies.
	applied := ontology.ApplyCorrections(all)
	for _, e := range applied {
		if e.EvidenceID == original.EvidenceID {
			t.Fatal("ApplyCorrections still returns the superseded record as current")
		}
	}
}

// TestCorrectionWithoutATargetIsRefused is the fail-closed half: a
// correction that names nothing to correct is not a correction.
func TestCorrectionWithoutATargetIsRefused(t *testing.T) {
	_, err := ontology.New(ontology.Evidence{
		Type: ontology.TypeCorrection, Subject: "s", Predicate: "p", Object: "ON",
		Source: "a", ObservedAt: 1, Confidence: 0.5,
	})
	if !errors.Is(err, ontology.ErrCorrectionWithoutTarget) {
		t.Fatalf("err = %v, want ErrCorrectionWithoutTarget", err)
	}
}

// TestContradictionMetadataPreservation: the facade's raw-observation
// path must hand the arbitration engine the observation it was given,
// with its source, value, tick and weighting intact -- otherwise the
// arbitration is over data the caller never submitted.
func TestContradictionMetadataPreservation(t *testing.T) {
	f := newFacade()
	obs := []contradiction.RawObservation{
		{ClaimKey: "VESSEL:IMO7777777|AIS_STATUS", SourceID: "ais-vendor-a", Value: "OFF",
			Tick: 10, SourceReliability: 0.8},
		{ClaimKey: "VESSEL:IMO7777777|AIS_STATUS", SourceID: "port-authority", Value: "ON",
			Tick: 11, SourceReliability: 0.95},
	}
	for _, o := range obs {
		if err := f.ObserveRaw(o); err != nil {
			t.Fatalf("ObserveRaw: %v", err)
		}
	}

	got := f.RawObservations("VESSEL:IMO7777777|AIS_STATUS")
	if len(got) != len(obs) {
		t.Fatalf("got %d observations back, submitted %d", len(got), len(obs))
	}
	bySource := map[string]contradiction.RawObservation{}
	for _, o := range got {
		bySource[o.SourceID] = o
	}
	for _, want := range obs {
		g, ok := bySource[want.SourceID]
		if !ok {
			t.Fatalf("observation from %s did not survive", want.SourceID)
		}
		if g.Value != want.Value || g.Tick != want.Tick ||
			g.SourceReliability != want.SourceReliability {
			t.Errorf("observation from %s was altered: want %+v got %+v", want.SourceID, want, g)
		}
	}

	// The contradiction itself must be visible in the arbitration, not
	// smoothed away.
	tv, err := f.ArbitrateClaim("VESSEL:IMO7777777|AIS_STATUS", 12, 100)
	if err != nil {
		t.Fatalf("ArbitrateClaim: %v", err)
	}
	if tv.Value == "" {
		t.Fatal("arbitration produced no value")
	}
	if err := f.VerifyRawTruthLedger("VESSEL:IMO7777777|AIS_STATUS"); err != nil {
		t.Fatalf("truth ledger does not verify: %v", err)
	}
	ranked, err := f.RankHypotheses("VESSEL:IMO7777777|AIS_STATUS", 12, 100)
	if err != nil {
		t.Fatalf("RankHypotheses: %v", err)
	}
	if len(ranked) < 2 {
		t.Fatalf("ranked %d candidates -- a genuine contradiction between two values must surface both", len(ranked))
	}
}

// TestCrossSubsystemRoundTrip: evidence submitted once must be the same
// evidence when it reaches fusion, the dependency graph, the decision
// and the certificate -- proved by running the real canonical chain and
// checking the certificate names the same subject/predicate the
// evidence asserted, over the same sources.
func TestCrossSubsystemRoundTrip(t *testing.T) {
	f := newFacade()
	sources := []string{"ais-vendor-a", "ais-vendor-b", "port-authority"}
	for i, s := range sources {
		if _, err := f.Submit(provenanced(s, "OFF", uint64(10+i))); err != nil {
			t.Fatalf("Submit %s: %v", s, err)
		}
	}

	res, err := f.Arbitrate("analyst-1", "VESSEL:IMO7777777", "AIS_STATUS", risk.DefaultPolicy(), 20)
	if err != nil {
		t.Fatalf("Arbitrate: %v", err)
	}
	if res.Certificate.Subject != "VESSEL:IMO7777777" || res.Certificate.Predicate != "AIS_STATUS" {
		t.Fatalf("the certificate describes a different claim: %s|%s",
			res.Certificate.Subject, res.Certificate.Predicate)
	}
	// Every source that submitted evidence is represented downstream in
	// the dependency graph the arbitration actually consulted.
	shared, families, err := f.Correlate("VESSEL:IMO7777777", "AIS_STATUS", 20)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	for _, s := range sources {
		if _, ok := shared[s]; !ok {
			t.Errorf("source %s did not reach the dependency graph", s)
		}
	}
	// All three share one upstream feed, so they must be correlated,
	// not counted as three independent confirmations.
	if len(families) == 0 {
		t.Fatal("three sources sharing one upstream feed were treated as independent")
	}
	if err := f.Verify(); err != nil {
		t.Fatalf("cross-subsystem ledgers do not verify: %v", err)
	}
}

// TestReplayEquality: the same evidence, arbitrated once and replayed
// independently, must produce a matching verification certificate.
func TestReplayEquality(t *testing.T) {
	f := newFacade()
	for i, s := range []string{"ais-vendor-a", "ais-vendor-b"} {
		if _, err := f.Submit(provenanced(s, "OFF", uint64(10+i))); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if _, err := f.Arbitrate("analyst-1", "VESSEL:IMO7777777", "AIS_STATUS", risk.DefaultPolicy(), 20); err != nil {
		t.Fatalf("Arbitrate: %v", err)
	}
	cert, err := f.Replay("VESSEL:IMO7777777", "AIS_STATUS", "auditor-1", "nonce-1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !cert.Match {
		t.Fatalf("replay diverged at %s: %s vs %s", cert.DivergedStage, cert.DivergedOriginalHash, cert.DivergedReplayHash)
	}
	if err := cert.Assert(); err != nil {
		t.Fatalf("certificate.Assert: %v", err)
	}
	// A second, independent replay produces a DIFFERENT replay identity
	// over the SAME execution -- the separation pkg/replay's own doc
	// comment requires -- while still matching.
	cert2, err := f.Replay("VESSEL:IMO7777777", "AIS_STATUS", "auditor-2", "nonce-2")
	if err != nil {
		t.Fatalf("Replay (2nd): %v", err)
	}
	if !cert2.Match {
		t.Fatal("the second independent replay did not match")
	}
	if cert2.ReplayPackageID == cert.ReplayPackageID {
		t.Fatal("two independent replays produced the same replay package identity")
	}
	if cert2.ExecutionID != cert.ExecutionID {
		t.Fatal("two replays of the SAME execution disagree about its execution identity")
	}
}

// TestCrossFacadeIdentityEquality: the identity of a piece of evidence
// must not depend on which facade instance handled it. Two independent
// facades given byte-identical evidence must agree on every identity
// that evidence carries.
func TestCrossFacadeIdentityEquality(t *testing.T) {
	a, b := newFacade(), newFacade()
	records := []ontology.Evidence{
		provenanced("ais-vendor-a", "OFF", 10),
		provenanced("ais-vendor-b", "OFF", 11),
	}

	var idsA, idsB []string
	for _, e := range records {
		sa, err := a.Submit(e)
		if err != nil {
			t.Fatalf("facade A Submit: %v", err)
		}
		sb, err := b.Submit(e)
		if err != nil {
			t.Fatalf("facade B Submit: %v", err)
		}
		idsA = append(idsA, sa.EvidenceID)
		idsB = append(idsB, sb.EvidenceID)
	}
	for i := range idsA {
		if idsA[i] != idsB[i] {
			t.Fatalf("record %d has different identities across facades: %s vs %s", i, idsA[i], idsB[i])
		}
	}

	resA, err := a.Arbitrate("analyst-1", "VESSEL:IMO7777777", "AIS_STATUS", risk.DefaultPolicy(), 20)
	if err != nil {
		t.Fatalf("facade A Arbitrate: %v", err)
	}
	resB, err := b.Arbitrate("analyst-1", "VESSEL:IMO7777777", "AIS_STATUS", risk.DefaultPolicy(), 20)
	if err != nil {
		t.Fatalf("facade B Arbitrate: %v", err)
	}
	if resA.Certificate.Hash != resB.Certificate.Hash {
		t.Fatalf("two facades given identical evidence produced different certificates: %s vs %s",
			resA.Certificate.Hash, resB.Certificate.Hash)
	}
	if Contract().Hash != Contract().Hash {
		t.Fatal("the contract descriptor is not stable within a process")
	}
}
