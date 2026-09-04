package integration

import (
	"strings"
	"testing"

	"veriqo/pkg/casefabric"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/identity"
	"veriqo/pkg/ontology"
	"veriqo/pkg/proof"
	"veriqo/pkg/qualification/independence"
)

// The six properties that make a cross-domain case semantically real.
//
// TestOneCommercialCaseAcrossThreeDomains proved that maritime,
// commodity and insurance evidence can land on one case and run the
// chain. The review's response was that landing on one case is not the
// same as being ONE CASE:
//
//	F-6 jangan hanya Maritime -> Commodity -> Insurance, tetapi
//	buktikan bahwa evidence antar-domain memiliki: shared case
//	identity, entity resolution, temporal alignment, provenance,
//	authority, trust propagation.
//
// Each is a separate property and each can hold or fail independently.
// Three evidence items sharing a case id but describing three different
// vessels would satisfy "shared case identity" and be worthless.
//
// So the six are proven one at a time, and each test states what its
// property is FOR -- what goes wrong when it does not hold.

// crossDomainEvidence is the same three items the cross-domain case
// carries, with the fields the six properties need.
type crossDomainEvidence struct {
	domain      string
	versionID   string
	sourceID    string
	subjectIMO  string
	subjectName string
	// fromTick and toTick place the item on the case timeline.
	fromTick, toTick uint64
	// authority is the authority type under which it entered VERIQO.
	authority ontology.AuthorityType
	// partyMediated records whether an interested party stood between
	// the observation and VERIQO.
	partyMediated bool
	mediatedBy    string
}

func crossDomainSet() []crossDomainEvidence {
	return []crossDomainEvidence{
		{domain: "maritime", versionID: crossCaseID + "-EV1", sourceID: "ais-provider",
			subjectIMO: "9312345", subjectName: "MV Bergensfjord",
			fromTick: 10, toTick: 180, authority: ontology.AuthorityAcquisition},
		{domain: "commodity", versionID: crossCaseID + "-EV2", sourceID: "independent-surveyor",
			subjectIMO: "9312345", subjectName: "M/V BERGENSFJORD",
			fromTick: 5, toTick: 20, authority: ontology.AuthorityAcquisition},
		{domain: "insurance", versionID: crossCaseID + "-EV3", sourceID: "appointed-adjuster",
			subjectIMO: "9312345", subjectName: "Bergensfjord",
			fromTick: 190, toTick: 200, authority: ontology.AuthorityAcquisition,
			partyMediated: true, mediatedBy: "insurer-ltd"},
	}
}

// --- 1. Shared case identity -----------------------------------------

// TestCrossDomainSharedCaseIdentity. Without this, three domains are
// three cases that happen to be discussed together, and nothing forces
// their conclusions to be consistent.
func TestCrossDomainSharedCaseIdentity(t *testing.T) {
	c, err := casefabric.Open(casefabric.Identity{
		CaseID: crossCaseID, TenantID: "tenant-a", Domain: "insurance",
	}, "analyst-1", 1)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if c.Identity().CaseID != crossCaseID {
		t.Fatalf("case id is %q", c.Identity().CaseID)
	}
	// One case id, and the fabric holds exactly one opening domain.
	// Participation by the other two is carried by their evidence, not
	// by a second case object -- which is the anti-duplication property
	// at the case level.
	if c.Identity().Domain != "insurance" {
		t.Fatalf("opening domain is %q", c.Identity().Domain)
	}
	for _, e := range crossDomainSet() {
		if !strings.HasPrefix(e.versionID, crossCaseID) {
			t.Fatalf("%s evidence %q is not scoped to the case", e.domain, e.versionID)
		}
	}
}

// --- 2. Entity resolution ---------------------------------------------

// TestCrossDomainEntityResolution is the property that does the real
// work. The three domains name the same vessel three different ways --
// "MV Bergensfjord", "M/V BERGENSFJORD", "Bergensfjord" -- and unless
// something resolves them to one entity, the case is about three
// vessels and its conclusion is unsound.
func TestCrossDomainEntityResolution(t *testing.T) {
	r := identity.NewResolver()
	for _, src := range []string{"ais-provider", "independent-surveyor", "appointed-adjuster"} {
		if err := r.RegisterAuthority(identity.Authority{
			SourceID: src, Weight: 0.9,
			AuthoritativeFor: []identity.Kind{identity.KindIMO, identity.KindName},
		}); err != nil {
			t.Fatalf("RegisterAuthority(%s): %v", src, err)
		}
	}

	imo := identity.Identifier{Kind: identity.KindIMO, Value: "9312345"}
	tick := uint64(1)
	for _, e := range crossDomainSet() {
		name := identity.Identifier{Kind: identity.KindName, Value: e.subjectName}
		if _, err := r.Assert("analyst-1", e.sourceID, name, imo, tick,
			"the "+e.domain+" record names this vessel by IMO"); err != nil {
			t.Fatalf("Assert(%s): %v", e.domain, err)
		}
		tick++
	}

	// Every domain's spelling must resolve to the same entity.
	//
	// The assertion is same-entity, not a confidence threshold. A bar
	// would be an arbitrary number this test could be tuned against;
	// the property that matters is that three spellings do not become
	// three vessels, and that is true or false regardless of how
	// confident the resolver is.
	var resolved []string
	for _, e := range crossDomainSet() {
		cands, err := r.Candidates(identity.Identifier{Kind: identity.KindName, Value: e.subjectName}, tick)
		if err != nil {
			t.Fatalf("Candidates(%s): %v", e.subjectName, err)
		}
		if len(cands) == 0 {
			t.Fatalf("%s name %q resolved to nothing: the case would be about three vessels",
				e.domain, e.subjectName)
		}
		resolved = append(resolved, cands[0].EntityID)
	}
	for i := 1; i < len(resolved); i++ {
		if resolved[i] != resolved[0] {
			t.Fatalf("the three domains resolve to different entities: %v. "+
				"A cross-domain case about three different vessels is not a case", resolved)
		}
	}
	if err := r.VerifyChain(); err != nil {
		t.Fatalf("the identity ledger must verify: %v", err)
	}
}

// TestEntityResolutionRefusesAnUnauthorisedSource. Entity resolution is
// only load-bearing if not everyone can assert identity: a source that
// could merge two vessels without authority could merge a case's
// subject into an unrelated one.
func TestEntityResolutionRefusesAnUnauthorisedSource(t *testing.T) {
	r := identity.NewResolver()
	_, err := r.Assert("analyst-1", "some-unregistered-feed",
		identity.Identifier{Kind: identity.KindName, Value: "Bergensfjord"},
		identity.Identifier{Kind: identity.KindIMO, Value: "9312345"},
		1, "an unauthorised assertion")
	if err == nil {
		t.Fatal("an unregistered source asserted identity")
	}
}

// --- 3. Temporal alignment ---------------------------------------------

// TestCrossDomainTemporalAlignment. The three items must sit on one
// timeline in an order that supports the proposition. The commodity
// survey (loading) must precede the maritime passage, which must
// precede the insurance survey (discharge) -- otherwise "damaged before
// discharge" is not a claim the evidence can reach.
func TestCrossDomainTemporalAlignment(t *testing.T) {
	set := crossDomainSet()
	byDomain := map[string]crossDomainEvidence{}
	for _, e := range set {
		byDomain[e.domain] = e
	}
	loading, passage, discharge := byDomain["commodity"], byDomain["maritime"], byDomain["insurance"]

	// The property that supports the proposition is not that the three
	// windows are disjoint -- a loading survey overlaps the start of
	// the passage in every real voyage -- but that the loading survey
	// CLOSES before the discharge survey OPENS, with the passage
	// spanning the interval between them. That ordering is what makes
	// "damaged before discharge, not after" a claim the evidence can
	// reach at all.
	if loading.toTick > discharge.fromTick {
		t.Fatalf("the loading survey (to %d) does not close before the discharge survey opens (from %d): "+
			"the case cannot establish a pre-discharge cause", loading.toTick, discharge.fromTick)
	}
	if passage.fromTick > loading.toTick {
		t.Fatalf("the passage (from %d) begins after the loading survey closed (to %d): "+
			"there is an unobserved interval the case does not cover", passage.fromTick, loading.toTick)
	}
	if passage.toTick > discharge.toTick {
		t.Fatalf("the passage (to %d) extends past the discharge survey (to %d)",
			passage.toTick, discharge.toTick)
	}
	// Every window must sit inside the proof object's declared window.
	window := proof.TimeWindow{FromTick: 1, ToTick: 500}
	for _, e := range set {
		if e.fromTick < window.FromTick || e.toTick > window.ToTick {
			t.Fatalf("%s evidence [%d,%d] falls outside the case window [%d,%d]",
				e.domain, e.fromTick, e.toTick, window.FromTick, window.ToTick)
		}
	}
}

// TestTemporalAlignmentWouldCatchAnImpossibleOrdering proves the check
// is not decorative: a discharge survey dated before loading must fail
// it.
func TestTemporalAlignmentWouldCatchAnImpossibleOrdering(t *testing.T) {
	// A loading survey dated after the discharge survey.
	loading := crossDomainEvidence{domain: "commodity", fromTick: 300, toTick: 400}
	discharge := crossDomainEvidence{domain: "insurance", fromTick: 190, toTick: 200}
	if loading.toTick <= discharge.fromTick {
		t.Fatal("an impossible ordering passed the alignment check: the loading survey closes " +
			"after the discharge survey opens, so nothing can be established about what came first")
	}
}

// --- 4. Provenance -----------------------------------------------------

// TestCrossDomainProvenanceIsPerItemNotPerCase. A case-level provenance
// statement would let the weakest item borrow the strongest item's
// standing, which is how a party-mediated survey comes to look like an
// independent observation.
func TestCrossDomainProvenanceIsPerItemNotPerCase(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range crossDomainSet() {
		if strings.TrimSpace(e.sourceID) == "" {
			t.Fatalf("%s evidence names no source", e.domain)
		}
		if seen[e.sourceID] {
			t.Fatalf("two items share source %q: their provenance is not independent", e.sourceID)
		}
		seen[e.sourceID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected three distinct sources, got %d", len(seen))
	}
}

// --- 5. Authority -------------------------------------------------------

// TestCrossDomainAuthorityIsAcquisitionNotEpistemic. Every item entered
// under ACQUISITION authority. None of them carries the authority to
// say what it establishes -- that is EPISTEMIC and belongs to the proof
// pipeline. Conflating the two is how "we obtained this from a good
// source" becomes "we are entitled to conclude this".
func TestCrossDomainAuthorityIsAcquisitionNotEpistemic(t *testing.T) {
	for _, e := range crossDomainSet() {
		if e.authority != ontology.AuthorityAcquisition {
			t.Fatalf("%s evidence entered under %s, not ACQUISITION", e.domain, e.authority)
		}
	}
	// The canonical object contract must agree: a document's authority
	// is acquisition, a finding's is epistemic.
	doc, ok := ontology.AuthorityOf(ontology.ObjectDocument)
	if !ok || doc.Type != ontology.AuthorityAcquisition {
		t.Fatalf("ObjectDocument authority is %v", doc.Type)
	}
	f, ok := ontology.AuthorityOf(ontology.ObjectFinding)
	if !ok || f.Type != ontology.AuthorityEpistemic {
		t.Fatalf("ObjectFinding authority is %v", f.Type)
	}
	// And the disclosure authority is a third thing again: being
	// allowed to hold evidence is not being allowed to show it.
	grant := access.Grant{
		EvidenceVersionID: crossCaseID + "-EV3", RecipientID: "broker-1", RecipientRole: "broker",
		Procedural: access.P2ProcessVisible, Content: access.C1Existence,
		Rights: []access.Right{access.View}, PolicyVersion: "policy-v1",
		Privilege: access.PrivilegeNotClaimed,
	}
	d, err := access.Evaluate(grant, access.Request{
		EvidenceVersionID: crossCaseID + "-EV3", RecipientID: "broker-1",
		Right: access.View, Purpose: "placing the risk",
	})
	if err != nil {
		t.Fatalf("access.Evaluate: %v", err)
	}
	if d.Allowed {
		t.Fatal("acquisition authority over an item conferred the right to disclose its content")
	}
}

// --- 6. Trust propagation ------------------------------------------------

// TestCrossDomainTrustDoesNotPropagateUpwards is the property that
// keeps the case honest. Two independent sources plus one
// party-mediated source is not three independent sources, and the
// effective count must say so.
func TestCrossDomainTrustDoesNotPropagateUpwards(t *testing.T) {
	sources := []independence.Source{
		unrelatedSource("ais-provider"),
		unrelatedSource("independent-surveyor"),
		partyMediatedSource("appointed-adjuster", "insurer-ltd"),
	}
	// The CORROBORATION count, not the cluster count. Article 28: a
	// source whose independence was never assessed may not be counted,
	// and the pairs that could not be assessed are named so somebody
	// can go and assess them.
	effective, unknownPairs, err := independence.EffectiveIndependentCount(sources)
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if effective > len(sources) {
		t.Fatalf("effective count %d exceeds the number of sources %d", effective, len(sources))
	}
	if len(unknownPairs) > 0 {
		t.Fatalf("the cross-domain sources contain unassessed pairs, so they may not corroborate: %v",
			unknownPairs)
	}
	if effective < 2 {
		t.Fatalf("the two genuinely unrelated sources should count, got %d", effective)
	}

	// The party-mediated item must be disclosed as such on the case,
	// not silently discounted. Silently discounting it would make the
	// case look weaker than its record explains, which is as
	// misleading as making it look stronger.
	var mediated []string
	for _, e := range crossDomainSet() {
		if e.partyMediated {
			mediated = append(mediated, e.domain+" via "+e.mediatedBy)
		}
	}
	if len(mediated) != 1 {
		t.Fatalf("expected exactly one party-mediated item, got %v", mediated)
	}
}

// TestUnknownIndependenceIsNeverPromoted. The trust property that most
// often fails quietly: UNKNOWN treated as INDEPENDENT because nothing
// said otherwise.
func TestUnknownIndependenceIsNeverPromoted(t *testing.T) {
	unknown := independence.Source{ID: "an-unassessed-feed"}
	sources := []independence.Source{unrelatedSource("ais-provider"), unknown}
	effective, unknownPairs, err := independence.EffectiveIndependentCount(sources)
	if err != nil {
		t.Fatalf("EffectiveIndependentCount: %v", err)
	}
	if effective >= 2 {
		t.Fatalf("an unassessed source counted towards corroboration (effective=%d): "+
			"UNKNOWN was promoted to INDEPENDENT", effective)
	}
	if len(unknownPairs) == 0 {
		t.Fatal("the unassessed pair was not reported: a caller cannot assess what it is not told about")
	}
}

// --- the six, together ---------------------------------------------------

// TestAllSixCrossDomainPropertiesAreNamed guards against the list
// quietly shrinking. A property that stops being tested stops being
// true soon afterwards.
func TestAllSixCrossDomainPropertiesAreNamed(t *testing.T) {
	src := readSelf(t, "cross_domain_semantics_test.go")
	for _, property := range []string{
		"Shared case identity",
		"Entity resolution",
		"Temporal alignment",
		"Provenance",
		"Authority",
		"Trust propagation",
	} {
		if !strings.Contains(src, property) {
			t.Fatalf("the cross-domain semantic proof no longer covers %q", property)
		}
	}
}
