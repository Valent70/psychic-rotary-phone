// Package integration drives the canonical VERIQO pipeline end to end.
//
// The specification's section 71 names it:
//
//	SOURCE -> ACQUISITION -> RAW EVIDENCE + HASH + CUSTODY
//	       -> NORMALIZATION -> EVIDENCE QUALITY -> ENTITY RESOLVE
//	       -> ONTOLOGY/GRAPH -> CLAIM -> REVERSE PROOF -> HYPOTHESIS
//	       -> COUNTERFACTUAL -> CONTRADICTION MATRIX
//	       -> SOURCE INDEPENDENCE -> TRUST/UNCERTAINTY
//	       -> SELF-DOUBT -> QUALIFICATION -> HUMAN REVIEW
//	       -> FINDING -> PASSPORT -> REPLAY/AUDIT
//
// This test walks it. Its value is not that each stage works -- the
// unit tests establish that -- but that they COMPOSE: that the output
// of one stage is the input the next actually requires, and that the
// refusals hold when the chain is driven for real rather than through
// a fixture built to satisfy one package.
package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/authority"
	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/casefile"
	"veriqo/pkg/claim"
	"veriqo/pkg/contract"
	"veriqo/pkg/custody"
	"veriqo/pkg/entity"
	evidence "veriqo/pkg/evidence/version"
	"veriqo/pkg/findings"
	"veriqo/pkg/governance/classification"
	"veriqo/pkg/graph"
	"veriqo/pkg/hypothesis"
	"veriqo/pkg/identity"
	"veriqo/pkg/ontology"
	"veriqo/pkg/passport"
	"veriqo/pkg/provenance"
	"veriqo/pkg/provenance/temporal"
	"veriqo/pkg/qualification/independence"
	"veriqo/pkg/quantum"
	"veriqo/pkg/replay"
	"veriqo/pkg/resolution"
	"veriqo/pkg/reverseproof"
	"veriqo/pkg/trust"
	"veriqo/pkg/uncertainty"
)

var (
	t0  = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	y24 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	y25 = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
)

func to(t time.Time) *time.Time { return &t }

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "veriqo-ontology", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "integration", Revision: 1},
	}
}

func person(id string) identity.Principal {
	return identity.Principal{ID: contract.ID(id), Kind: identity.Human, TenantID: "t-acme",
		NotBefore: t0.Add(-time.Hour), NotAfter: t0.Add(time.Hour)}
}

type signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (s signer) Sign(m []byte) ([]byte, string, error) {
	return ed25519.Sign(s.priv, m), "veriqo-key-1", nil
}

func (s signer) Verify(m, sig []byte, id string) error {
	if !ed25519.Verify(s.pub, m, sig) {
		return errors.New("bad signature")
	}
	return nil
}

// TestTheCanonicalPipelineComposes drives every stage in order.
func TestTheCanonicalPipelineComposes(t *testing.T) {
	// --- SOURCE -> ACQUISITION -> RAW EVIDENCE ------------------------
	loadingSurvey := []byte("LOADING SURVEY: 60,000.000 MT, shore tank, 15C, in air, API MPMS")
	dischargeSurvey := []byte("DISCHARGE SURVEY: 58,200.000 MT, shore tank, 15C, in air, API MPMS")

	prov := provenance.Record{
		ID: "provenance:p1", EvidenceID: "evidence:e1", TenantID: "t-acme",
		Path: []provenance.Hop{
			{PartyID: "load-terminal", Role: provenance.Observer, At: y24},
			{PartyID: "independent-inspector-a", Role: provenance.Producer, At: y24.Add(time.Hour)},
			{PartyID: "veriqo", Role: provenance.Recipient, At: y24.Add(2 * time.Hour)},
		},
		ObservedAt: to(y24), ReceivedAt: y24.Add(2 * time.Hour),
		LicenceID: "lic-inspector-a", SourceContentHash: jcs.HashBytes(loadingSurvey),
	}
	if err := prov.Validate(); err != nil {
		t.Fatalf("provenance: %v", err)
	}
	producer, err := prov.ProducerID()
	if err != nil {
		t.Fatal(err)
	}
	if producer != "load-terminal" {
		t.Fatalf("producer = %s", producer)
	}

	// --- CUSTODY ------------------------------------------------------
	chain, err := custody.New("evidenceversion:e1v1", "t-acme", custody.Link{
		HolderID: "ingest-service", Action: custody.Acquired, At: y24.Add(2 * time.Hour),
		ReceivedDigest: jcs.HashBytes(loadingSurvey), ReleasedDigest: jcs.HashBytes(loadingSurvey),
		Authorization: "PERMIT baseline/clearance",
	})
	if err != nil {
		t.Fatalf("custody: %v", err)
	}
	if !chain.Intact() {
		t.Fatal("a fresh custody chain is broken")
	}

	// --- EVIDENCE VERSION ---------------------------------------------
	root := evidence.Version{
		ID: "evidenceversion:e1v1", EvidenceID: "evidence:e1", Version: 1,
		SHA256: jcs.HashBytes(loadingSurvey), MediaType: "text/plain",
		SizeBytes: int64(len(loadingSurvey)),
		SourceID:  "src:independent-inspector-a", ProducerID: producer, TenantID: "t-acme",
		AcquiredAt: y24.Add(2 * time.Hour), ObservedAt: to(y24),
		Mode: evidence.APIPull, RightsClass: "commercial-restricted",
		Classification: classification.MustNew(classification.Confidential),
		Validity:       temporal.Valid,
		Transform:      evidence.Acquisition,
		ProvenanceRef:  "provenance:p1", CustodyRef: "custody:c1",
	}
	evChain, err := evidence.NewChain(root)
	if err != nil {
		t.Fatalf("evidence chain: %v", err)
	}
	if err := evChain.Root().VerifyContent(loadingSurvey); err != nil {
		t.Fatalf("the acquisition does not verify against its bytes: %v", err)
	}

	// The discharge survey is a SECOND acquisition from a SECOND
	// producer, not a derivation of the first. It gets its own
	// provenance record, its own root version and its own digest;
	// nothing downstream may treat the two as one source (Law 6).
	prov2 := provenance.Record{
		ID: "provenance:p2", EvidenceID: "evidence:e2", TenantID: "t-acme",
		Path: []provenance.Hop{
			{PartyID: "discharge-terminal", Role: provenance.Observer, At: y24.AddDate(0, 1, 0)},
			{PartyID: "independent-inspector-b", Role: provenance.Producer, At: y24.AddDate(0, 1, 0).Add(time.Hour)},
			{PartyID: "veriqo", Role: provenance.Recipient, At: y24.AddDate(0, 1, 0).Add(2 * time.Hour)},
		},
		ObservedAt: to(y24.AddDate(0, 1, 0)), ReceivedAt: y24.AddDate(0, 1, 0).Add(2 * time.Hour),
		LicenceID: "lic-inspector-b", SourceContentHash: jcs.HashBytes(dischargeSurvey),
	}
	if err := prov2.Validate(); err != nil {
		t.Fatalf("provenance 2: %v", err)
	}
	producer2, err := prov2.ProducerID()
	if err != nil {
		t.Fatal(err)
	}
	if producer2 == producer {
		t.Fatal("the two surveys resolved to the same producer; the fixture no longer tests independence")
	}

	root2 := evidence.Version{
		ID: "evidenceversion:e2v1", EvidenceID: "evidence:e2", Version: 1,
		SHA256: jcs.HashBytes(dischargeSurvey), MediaType: "text/plain",
		SizeBytes: int64(len(dischargeSurvey)),
		SourceID:  "src:independent-inspector-b", ProducerID: producer2, TenantID: "t-acme",
		AcquiredAt: y24.AddDate(0, 1, 0).Add(2 * time.Hour), ObservedAt: to(y24.AddDate(0, 1, 0)),
		Mode: evidence.APIPull, RightsClass: "commercial-restricted",
		Classification: classification.MustNew(classification.Confidential),
		Validity:       temporal.Valid,
		Transform:      evidence.Acquisition,
		ProvenanceRef:  "provenance:p2", CustodyRef: "custody:c2",
	}
	evChain2, err := evidence.NewChain(root2)
	if err != nil {
		t.Fatalf("evidence chain 2: %v", err)
	}
	if err := evChain2.Root().VerifyContent(dischargeSurvey); err != nil {
		t.Fatalf("the discharge acquisition does not verify against its bytes: %v", err)
	}
	if evChain2.Root().SHA256 == evChain.Root().SHA256 {
		t.Fatal("the two surveys share a digest; the fixture is degenerate")
	}

	// --- ENTITY RESOLUTION --------------------------------------------
	vesselA := entity.Entity{
		ID: "entity:v1", Kind: entity.Vessel, TenantID: "t-acme",
		Identifiers: []entity.Identifier{{Scheme: entity.IMO, Value: "9074729",
			Scope:        contract.Interval{From: y24, To: to(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))},
			EvidenceRefs: []string{"evidenceversion:e1v1"}}},
	}
	vesselB := vesselA
	vesselB.ID = "entity:v2"
	vesselB.Identifiers = []entity.Identifier{{Scheme: entity.IMO, Value: "9074729",
		Scope:        contract.Interval{From: y24, To: to(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))},
		EvidenceRefs: []string{"evidenceversion:e2v1"}}}

	cfg := resolution.DefaultConfig()
	cfg.At = t0
	cfg.PolicyVersion = contract.Version{Component: "resolution", Revision: 1}
	cfg.OntologyVersion = contract.Version{Component: "veriqo-ontology", Revision: 1}
	cfg.Sources = map[string]independence.Source{
		"evidenceversion:e1v1": fullSource("inspector-a"),
		"evidenceversion:e2v1": fullSource("inspector-b"),
	}
	res, err := resolution.Resolve(vesselA, vesselB, nil, cfg)
	if err != nil {
		t.Fatalf("resolution: %v", err)
	}
	if err := resolution.RequireSameEntity(res); err != nil {
		t.Fatalf("two IMO-matched vessels did not resolve: %v\n%s", err, res.Explain())
	}

	// --- ONTOLOGY / GRAPH ---------------------------------------------
	ont, err := ontology.Veriqo(1)
	if err != nil {
		t.Fatal(err)
	}
	g, err := graph.New("t-acme", ont)
	if err != nil {
		t.Fatal(err)
	}
	lot := entity.Entity{ID: "entity:lot1", Kind: entity.CargoLot, TenantID: "t-acme",
		Attributes: []entity.Attribute{{Name: "commodity", Value: "crude",
			Scope:        contract.Interval{From: y24, To: to(y25)},
			EvidenceRefs: []string{"evidenceversion:e1v1"}}}}
	voyage := entity.Entity{ID: "entity:voy1", Kind: entity.Voyage, TenantID: "t-acme",
		Attributes: []entity.Attribute{{Name: "departed_at", Value: "2024-03-01",
			Scope:        contract.Interval{From: y24, To: to(y25)},
			EvidenceRefs: []string{"evidenceversion:e1v1"}}}}
	for _, n := range []struct {
		e  entity.Entity
		ot string
	}{{vesselA, "Vessel"}, {voyage, "Voyage"}, {lot, "CargoLot"}} {
		if err := g.AddNode(n.e, n.ot); err != nil {
			t.Fatalf("graph node %s: %v", n.e.ID, err)
		}
	}
	span := contract.Interval{From: y24, To: to(y25)}
	for _, e := range []graph.Edge{
		{ID: "edge:a", Type: "PERFORMED_VOYAGE", From: "entity:v1", To: "entity:voy1",
			Scope: span, EvidenceRefs: []string{"evidenceversion:e1v1"},
			Qualification: graph.Documented},
		{ID: "edge:b", Type: "CARRIED", From: "entity:voy1", To: "entity:lot1",
			Scope: span, EvidenceRefs: []string{"evidenceversion:e1v1"},
			Qualification: graph.Documented},
	} {
		if err := g.AddEdge(e); err != nil {
			t.Fatalf("graph edge %s: %v", e.ID, err)
		}
	}
	paths, err := g.Paths("entity:v1", "entity:lot1", graph.Options{MaxDepth: 4, At: y24.AddDate(0, 6, 0)})
	if err != nil || len(paths) != 1 {
		t.Fatalf("graph traversal: %v, %d path(s)", err, len(paths))
	}

	// --- QUANTUM (the arithmetic the claim rests on) -------------------
	basis := quantum.Basis{Method: "shore tank", TemperatureC: f(15), Density: f(0.8654),
		InVacuum: false, Standard: "API MPMS Ch.12"}
	q, err := quantum.Compute(quantum.Request{
		Loaded: quantum.Measurement{ID: "loading", Value: 60000, Unit: "MT", Basis: basis,
			At: y24, EvidenceRefs: []string{"evidenceversion:e1v1"}},
		Discharged: quantum.Measurement{ID: "discharge", Value: 58200, Unit: "MT", Basis: basis,
			At: y24.AddDate(0, 1, 0), EvidenceRefs: []string{"evidenceversion:e2v1"}},
		ContractQuantity: 60000,
		Tolerance: quantum.Tolerance{Percent: 0.5, Clause: "clause 4(b)",
			AtWhoseOption: "the seller"},
		Price: &quantum.Price{PerUnit: 620, Currency: "USD", Basis: "contract price",
			AsOf: y24, EvidenceRefs: []string{"evidenceversion:e1v1"}},
	})
	if err != nil {
		t.Fatalf("quantum: %v", err)
	}
	if q.ExcessOverTolerance != 1500 {
		t.Fatalf("excess = %.3f", q.ExcessOverTolerance)
	}
	// The money figure follows the excess, not the whole shortfall --
	// and the other reading travels with it rather than being dropped.
	if q.Amount == nil || *q.Amount != 1500*620 {
		t.Fatalf("amount = %v", q.Amount)
	}
	if q.Alternative == nil || q.Alternative.Quantity != 1800 {
		t.Fatalf("the alternative construction was not carried: %+v", q.Alternative)
	}

	// --- CLAIM ---------------------------------------------------------
	cl := claim.Claim{
		ID: "claim:c1", TenantID: "t-acme", CaseID: "case-1",
		Statement: "the cargo discharged was 1,500 MT short of the contractual entitlement",
		Scope: claim.Scope{Subject: "CargoLot L-77", Aspect: "quantity",
			Period: contract.Interval{From: y24, To: to(y25)}},
		SupportingEvidence:    []string{"evidenceversion:e1v1", "evidenceversion:e2v1"},
		AlternativeHypotheses: []string{"measurement conversion error"},
		DisproofPath: "a certified density measurement showing the loading and discharge " +
			"bases are not comparable would remove the arithmetic the claim rests on",
		Status:   claim.Supported,
		Versions: versions(),
	}
	if err := cl.Validate(); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// --- REVERSE PROOF -------------------------------------------------
	conds := []reverseproof.Condition{
		{ID: "C1", Must: "the cargo existed", Expected: "a loading survey",
			Sources: []string{"load terminal"}, Diagnosticity: 0.4, AcquisitionCost: 0.2,
			LegallyAccessible: true},
		{ID: "C2", Must: "the bases are comparable",
			Expected: "density and temperature at both ends on the same standard",
			Sources:  []string{"independent inspector"}, Diagnosticity: 0.95,
			AcquisitionCost: 0.4, LegallyAccessible: true},
		{ID: "C3", Must: "the difference exceeds the contractual tolerance",
			Expected: "the tolerance clause and the arithmetic",
			Sources:  []string{"contract"}, Diagnosticity: 0.6, AcquisitionCost: 0.1,
			LegallyAccessible: true},
	}
	rp, err := reverseproof.New(cl, "reverseproof:rp1", conds,
		[]string{"loading quantity overstated"})
	if err != nil {
		t.Fatalf("reverse proof: %v", err)
	}
	for _, c := range conds {
		if err := rp.Set(c.ID, reverseproof.Satisfied,
			[]string{"evidenceversion:e1v1"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if rp.Verdict() != reverseproof.VerdictFullyChecked {
		t.Fatalf("verdict = %s", rp.Verdict())
	}
	// And it still does not say the claim is proved.
	if !strings.Contains(rp.Report(), "does not establish the claim") {
		t.Fatal("the reverse proof report overstates itself")
	}

	// --- HYPOTHESES AND CONTRADICTION MATRIX ---------------------------
	m, err := hypothesis.NewMatrix("t-acme", "case-1", []hypothesis.Hypothesis{
		{ID: "H1", Statement: "cargo was physically lost",
			Expected: []string{"comparable bases", "a difference beyond tolerance"}},
		{ID: "H2", Statement: "a measurement-basis artefact",
			Expected: []string{"density mismatch", "temperature difference"}},
		{ID: "H3", Statement: "the loading quantity was overstated",
			Expected: []string{"a load-port reconciliation failure"}},
	}, versions())
	if err != nil {
		t.Fatalf("matrix: %v", err)
	}
	obs := hypothesis.Observation{ID: "comparable bases", Detail: "comparable bases",
		Reliability: 0.9, Independence: 0.9, Freshness: 0.9,
		TemporalFit: true, MeasurementCompatible: true,
		EvidenceRefs: []string{"evidenceversion:e1v1"}}
	if err := m.AddObservation(obs); err != nil {
		t.Fatal(err)
	}
	m.Set("H1", obs.ID, hypothesis.StronglyConsistent)
	m.Set("H2", obs.ID, hypothesis.StronglyInconsistent)
	m.Set("H3", obs.ID, hypothesis.NeutralC)
	assessment, err := m.Assess()
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	leader, decided := assessment.Leader()
	if !decided {
		t.Fatalf("the matrix did not separate the hypotheses:\n%s", assessment.Report())
	}
	if leader.Hypothesis.ID != "H1" {
		t.Fatalf("leader = %s", leader.Hypothesis.ID)
	}

	// --- SOURCE INDEPENDENCE AND TRUST ---------------------------------
	n, unknown, err := independence.EffectiveIndependentCount([]independence.Source{
		fullSource("inspector-a"), fullSource("inspector-b")})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(unknown) != 0 {
		t.Fatalf("independent count = %d, unknown = %v", n, unknown)
	}
	reg := trust.NewRegister()
	src, _ := trust.Uniform("src:inspector-a", trust.InSource)
	src, _ = src.Observe(120, 3)
	reg.Put(src)
	// And the flow to a conclusion does not exist.
	if _, err := trust.Propagate(src, trust.InConclusion, "claim:c1"); err == nil {
		t.Fatal("source trust propagated to conclusion trust")
	}

	// --- UNCERTAINTY ----------------------------------------------------
	var js []uncertainty.Judgement
	for _, d := range uncertainty.Dimensions() {
		l := uncertainty.High
		if d == uncertainty.Causal {
			l = uncertainty.Medium
		}
		if d == uncertainty.Completeness {
			l = uncertainty.Low
		}
		js = append(js, uncertainty.Judgement{Dimension: d, Level: l,
			Basis: "assessed against the case record"})
	}
	vec, err := uncertainty.New("claim:c1", js...)
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := vec.Weakest(); w != uncertainty.Completeness {
		t.Fatalf("weakest = %s", w)
	}

	// --- CASE, GATE, FINDING --------------------------------------------
	c, err := casefile.New("case-1", "t-acme", "Cargo discrepancy L-77", versions())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddClaim(cl, true, "the quantum rests entirely on this claim"); err != nil {
		t.Fatal(err)
	}
	if err := c.AttachProof("claim:c1", rp); err != nil {
		t.Fatal(err)
	}

	f, err := findings.Mint(findings.MintRequest{
		ID: "finding:f1", CaseID: "case-1", Claim: cl, Proof: rp, Confidence: vec,
		Limitations: []string{
			"the discharge survey covers one tank of three",
			q.Alternative.Why,
		},
		Proposer: person("human:analyst-1"), Approver: person("human:reviewer-1"),
		ApproverGrant: authority.Grant{Principal: "human:reviewer-1",
			Role: authority.Reviewer, TenantID: "t-acme"},
		At: t0,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := c.Resolve([]findings.Finding{f}, t0); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("the resolved case does not verify: %v", err)
	}

	// The confidence vector's weak dimensions travelled into the
	// finding's limitations without anybody copying them.
	joined := strings.Join(f.Limitations, " ")
	if !strings.Contains(joined, "evidence completeness confidence is LOW") {
		t.Fatalf("the weak dimension did not reach the finding:\n%v", f.Limitations)
	}

	// --- PASSPORT --------------------------------------------------------
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := signer{priv: priv, pub: pub}
	pp, err := passport.Issue(passport.IssueRequest{
		Finding: f, Qualification: "ASSURED", IssuedAt: t0, Signer: s,
	})
	if err != nil {
		t.Fatalf("passport: %v", err)
	}
	result, err := passport.Verify(pp, passport.VerifyOptions{
		Verifier: s, At: t0, Revocations: []passport.Revocation{}})
	if err != nil {
		t.Fatalf("passport verify: %v", err)
	}
	if !result.Trustworthy() {
		t.Fatalf("a freshly issued passport is not trustworthy: %+v", result)
	}
	// And it discloses that nobody outside VERIQO has looked.
	found := false
	for _, cav := range result.Caveats {
		if strings.Contains(cav, "no party outside VERIQO") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the passport does not disclose the absence of external validation: %v",
			result.Caveats)
	}

	// --- REPLAY -----------------------------------------------------------
	steps := []replay.Step{
		{ID: "resolve", Name: "entity resolution", Determinism: replay.Deterministic,
			InputRefs: []string{"entity:v1", "entity:v2"}, InputHash: "in-resolve",
			OutputHash: "out-resolve", Versions: versions(), At: t0},
		{ID: "quantum", Name: "quantum computation", Determinism: replay.Deterministic,
			InputRefs:  []string{"evidenceversion:e1v1", "evidenceversion:e2v1"},
			InputHash:  "in-quantum",
			OutputHash: "out-quantum", Versions: versions(), At: t0.Add(time.Minute)},
	}
	man, err := replay.New("replay:r1", "t-acme", "case-1", "finding:f1", steps,
		mustDigest(t, f), t0)
	if err != nil {
		t.Fatalf("replay manifest: %v", err)
	}
	rr, err := replay.Replay(man, exactExecutor{}, versions())
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Reproduced {
		t.Fatalf("the pipeline did not replay:\n%s", rr.Report())
	}
	if man.Coverage() != 1 {
		t.Fatalf("replay coverage = %v", man.Coverage())
	}
}

type exactExecutor struct{}

func (exactExecutor) Execute(s replay.Step) (string, error) { return s.OutputHash, nil }

func mustDigest(t *testing.T, f findings.Finding) string {
	t.Helper()
	d, err := f.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func f(v float64) *float64 { return &v }

func fullSource(id string) independence.Source {
	m := map[independence.Dimension]string{}
	for _, d := range independence.Dimensions() {
		m[d] = id + "-" + string(d)
	}
	return independence.Source{ID: id, Attributes: m}
}
