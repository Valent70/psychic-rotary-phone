package resolution

import (
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/entity"
	"veriqo/pkg/qualification/independence"
)

func d(y int) time.Time         { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
func to(t time.Time) *time.Time { return &t }

var now = d(2026)

func cfg() Config {
	c := DefaultConfig()
	c.At = now
	c.PolicyVersion = contract.Version{Component: "resolution", Revision: 1}
	c.OntologyVersion = contract.Version{Component: "maritime", Revision: 1}
	return c
}

func vessel(id string, ids ...entity.Identifier) entity.Entity {
	return entity.Entity{
		ID: contract.ID("entity:" + id), Kind: entity.Vessel, TenantID: "t-acme",
		Identifiers: ids,
	}
}

func ident(s entity.Scheme, v string, from, until time.Time, refs ...string) entity.Identifier {
	if len(refs) == 0 {
		refs = []string{"ev:" + v}
	}
	return entity.Identifier{Scheme: s, Value: v,
		Scope: contract.Interval{From: from, To: to(until)}, EvidenceRefs: refs}
}

// TestAReassignedMMSIDoesNotMerge is the failure this engine exists to
// prevent. Two vessels, five years apart, one number.
func TestAReassignedMMSIDoesNotMerge(t *testing.T) {
	a := vessel("v1", ident(entity.MMSI, "235098765", d(2014), d(2016)))
	b := vessel("v2", ident(entity.MMSI, "235098765", d(2022), d(2024)))

	r, err := Resolve(a, b, nil, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution.PermitsMerge() {
		t.Fatalf("TWO VESSELS SHARING A REASSIGNED MMSI RESOLVED %s", r.Resolution)
	}
	// And the reason must be visible, not merely the absence of a merge.
	if !strings.Contains(r.Explain(), "NON-OVERLAPPING") {
		t.Fatalf("the reflagging trap was not reported:\n%s", r.Explain())
	}
}

// TestAnOverlappingMMSIMatchStillNeedsAReviewer. Even a real overlap
// on a reassignable scheme is not enough on its own.
func TestAnOverlappingMMSIMatchStillNeedsAReviewer(t *testing.T) {
	a := vessel("v1", ident(entity.MMSI, "235098765", d(2020), d(2024)))
	b := vessel("v2", ident(entity.MMSI, "235098765", d(2021), d(2025)))

	r, err := Resolve(a, b, []Finding{
		{Signal: NameSimilarity, Verdict: Supports, Weight: 0.6, Detail: "identical names"},
	}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution == SameEntity && !r.ReviewerRequired {
		t.Fatal("a merge on a reassignable identifier was approved with no reviewer")
	}
	if err := RequireSameEntity(r); err == nil {
		t.Fatal("RequireSameEntity permitted a merge that needs review")
	}
	if !strings.Contains(r.ReviewReason, "reassignable") {
		t.Fatalf("the review reason does not name the cause: %q", r.ReviewReason)
	}
}

// TestADefinitiveIdentifierMatchResolvesCleanly. A rule that refused
// everything would satisfy the tests above and be useless.
func TestADefinitiveIdentifierMatchResolvesCleanly(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030), "ev:reg-a"))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030), "ev:reg-b"))

	c := cfg()
	c.Sources = map[string]independence.Source{
		"ev:reg-a": fullSource("registry-a"),
		"ev:reg-b": fullSource("registry-b"),
	}
	r, err := Resolve(a, b, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution != SameEntity {
		t.Fatalf("an IMO match resolved %s (confidence %.2f)\n%s",
			r.Resolution, r.IdentityConfidence, r.Explain())
	}
	if err := RequireSameEntity(r); err != nil {
		t.Fatalf("a clean IMO match was refused: %v", err)
	}
}

// TestTwoDifferentIMOsOverAnOverlappingPeriodAreContradicted. A
// permanent identifier is not reassigned, so two of them at once means
// two things.
func TestTwoDifferentIMOsOverAnOverlappingPeriodAreContradicted(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030)))
	b := vessel("v2", ident(entity.IMO, "9111111", d(2010), d(2030)))

	r, err := Resolve(a, b, []Finding{
		{Signal: NameSimilarity, Verdict: Supports, Weight: 0.9, Detail: "identical names"},
		{Signal: OperationalBehaviour, Verdict: Supports, Weight: 0.9, Detail: "same route"},
	}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution.PermitsMerge() {
		t.Fatal("A CONTRADICTION WAS OUTWEIGHED: two distinct permanent identifiers merged")
	}
	if len(r.Contradictions) == 0 {
		t.Fatal("the contradiction was not recorded")
	}
}

// TestAContradictionIsAVetoNotAWeight is the rule stated directly:
// however much supporting weight is piled on, a contradicted pair
// cannot reach SAME_ENTITY.
func TestAContradictionIsAVetoNotAWeight(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030)))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030)))

	overwhelming := []Finding{
		{Signal: NameSimilarity, Verdict: Supports, Weight: 1.0, Detail: "identical"},
		{Signal: Ownership, Verdict: Supports, Weight: 1.0, Detail: "same owner"},
		{Signal: RegistryConsistency, Verdict: Supports, Weight: 1.0, Detail: "same flag"},
		{Signal: SpatialConsistency, Verdict: Supports, Weight: 1.0, Detail: "co-located"},
		{Signal: Contradiction, Verdict: Contradicts, Weight: 1.0,
			Detail: "both were in different ports on the same day"},
	}
	r, err := Resolve(a, b, overwhelming, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution.PermitsMerge() {
		t.Fatalf("A VETO WAS OUTVOTED at confidence %.2f", r.IdentityConfidence)
	}
	// The result is RELATED because ownership supports a connection,
	// which is a genuinely different statement from SAME.
	if r.Resolution != RelatedEntity {
		t.Fatalf("resolution = %s, want RELATED_ENTITY", r.Resolution)
	}
}

// TestUnresolvedIsTheAnswerWhenNothingWasAssessed, and the unassessed
// signals are named so a caller can tell ignorance from difference.
func TestUnresolvedIsTheAnswerWhenNothingWasAssessed(t *testing.T) {
	a := vessel("v1", ident(entity.Internal, "a", d(2020), d(2030)))
	b := vessel("v2", ident(entity.Internal, "b", d(2020), d(2030)))

	r, err := Resolve(a, b, nil, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution.Determined() {
		t.Fatalf("a pair with no assessed signal resolved %s", r.Resolution)
	}
	if len(r.Unassessed) < 8 {
		t.Fatalf("only %d signals reported unassessed: %v", len(r.Unassessed), r.Unassessed)
	}
	if !strings.Contains(r.Explain(), "nobody ran this signal") {
		t.Fatalf("the explanation does not distinguish unassessed from neutral:\n%s", r.Explain())
	}
}

// TestOneSourceClusterIsNotCorroboration. Agreement from a single
// source cluster is one observation repeated.
func TestOneSourceClusterIsNotCorroboration(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030), "ev:feed-1"))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030), "ev:feed-2"))

	sameProducer := fullSource("provider-x")
	c := cfg()
	c.Sources = map[string]independence.Source{
		"ev:feed-1": sameProducer,
		"ev:feed-2": sameProducer,
	}
	r, err := Resolve(a, b, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution == SameEntity && !r.ReviewerRequired {
		t.Fatal("a merge supported by one source cluster was approved without review")
	}
	if !strings.Contains(r.ReviewReason, "one source cluster") {
		t.Fatalf("the review reason does not name the cause: %q", r.ReviewReason)
	}
}

// TestPossibleSameEntityAlwaysRequiresAReviewer.
func TestPossibleSameEntityAlwaysRequiresAReviewer(t *testing.T) {
	a := vessel("v1", ident(entity.Internal, "a", d(2020), d(2030)))
	b := vessel("v2", ident(entity.Internal, "b", d(2020), d(2030)))
	r, err := Resolve(a, b, []Finding{
		{Signal: NameSimilarity, Verdict: Supports, Weight: 0.5, Detail: "similar"},
		{Signal: TemporalConsistency, Verdict: Neutral, Weight: 0.3, Detail: "compatible"},
	}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution != PossibleSameEntity {
		t.Fatalf("resolution = %s (confidence %.2f)", r.Resolution, r.IdentityConfidence)
	}
	if !r.ReviewerRequired {
		t.Fatal("POSSIBLE_SAME_ENTITY did not require a reviewer")
	}
	if err := RequireSameEntity(r); !errors.Is(err, ErrNotResolved) {
		t.Fatalf("RequireSameEntity accepted POSSIBLE_SAME_ENTITY: %v", err)
	}
}

// TestTheResultRecordsItsVersionsAndReplayReference. A resolution that
// cannot be replayed is an opinion with a timestamp.
func TestTheResultRecordsItsVersionsAndReplayReference(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030)))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030)))
	r, err := Resolve(a, b, nil, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.PolicyVersion.Zero() || r.OntologyVersion.Zero() {
		t.Fatal("the result does not record the versions it ran under")
	}
	if r.ReplayReference == "" {
		t.Fatal("the result carries no replay reference")
	}
	if !strings.Contains(r.ReplayReference, "2026") {
		t.Fatalf("the replay reference does not pin the instant: %q", r.ReplayReference)
	}
}

// TestAnUnversionedResolutionIsRefused.
func TestAnUnversionedResolutionIsRefused(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030)))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030)))
	c := cfg()
	c.PolicyVersion = contract.Version{}
	if _, err := Resolve(a, b, nil, c); !errors.Is(err, contract.ErrUnversioned) {
		t.Fatalf("an unversioned resolution was produced: %v", err)
	}
	c = cfg()
	c.At = time.Time{}
	if _, err := Resolve(a, b, nil, c); err == nil {
		t.Fatal("a resolution with no instant was produced")
	}
}

// TestCrossTenantAndCrossKindResolutionAreRefused.
func TestCrossTenantAndCrossKindResolutionAreRefused(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030)))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030)))

	other := b
	other.TenantID = "t-beta"
	if _, err := Resolve(a, other, nil, cfg()); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant resolution was produced: %v", err)
	}

	org := b
	org.Kind = entity.Organisation
	if _, err := Resolve(a, org, nil, cfg()); !errors.Is(err, ErrDifferentKind) {
		t.Fatalf("a vessel was resolved against an organisation: %v", err)
	}

	if _, err := Resolve(a, a, nil, cfg()); !errors.Is(err, ErrSameEntity) {
		t.Fatal("an entity was resolved against itself")
	}
}

// TestIdentityConfidenceIsNotTheDecision. A caller reading only the
// number has re-created the blended score this package avoids -- so
// the number and the resolution must be able to disagree.
func TestIdentityConfidenceIsNotTheDecision(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030)))
	b := vessel("v2", ident(entity.IMO, "9111111", d(2010), d(2030)))
	r, err := Resolve(a, b, []Finding{
		{Signal: NameSimilarity, Verdict: Supports, Weight: 1.0, Detail: "identical"},
	}, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if r.IdentityConfidence < 0.4 {
		t.Fatalf("premise changed: confidence is %.2f", r.IdentityConfidence)
	}
	if r.Resolution.PermitsMerge() {
		t.Fatal("a high confidence overrode a contradiction")
	}
}

// TestResolutionIsDeterministic. Two runs over the same inputs must
// produce the same result, or replay cannot compare them.
func TestResolutionIsDeterministic(t *testing.T) {
	a := vessel("v1", ident(entity.IMO, "9074729", d(2010), d(2030), "ev:1", "ev:2"))
	b := vessel("v2", ident(entity.IMO, "9074729", d(2010), d(2030), "ev:3"))
	c := cfg()
	c.Sources = map[string]independence.Source{
		"ev:1": fullSource("s1"), "ev:2": fullSource("s2"), "ev:3": fullSource("s3"),
	}
	first, err := Resolve(a, b, nil, c)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := Resolve(a, b, nil, c)
		if err != nil {
			t.Fatal(err)
		}
		if got.Resolution != first.Resolution || got.IdentityConfidence != first.IdentityConfidence {
			t.Fatalf("resolution varies between runs: %s/%.4f vs %s/%.4f",
				got.Resolution, got.IdentityConfidence, first.Resolution, first.IdentityConfidence)
		}
		if strings.Join(got.SourceClusters, ",") != strings.Join(first.SourceClusters, ",") {
			t.Fatalf("source clusters vary: %v vs %v", got.SourceClusters, first.SourceClusters)
		}
		if got.Explain() != first.Explain() {
			t.Fatal("the explanation varies between runs")
		}
	}
}

func fullSource(id string) independence.Source {
	m := map[independence.Dimension]string{}
	for _, d := range independence.Dimensions() {
		m[d] = id + "-" + string(d)
	}
	return independence.Source{ID: id, Attributes: m}
}
