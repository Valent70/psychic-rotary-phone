package rights

import (
	"errors"
	"testing"
	"time"

	"veriqo/pkg/policy"
)

var t0 = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func ais() Licence {
	until := t0.AddDate(1, 0, 0)
	return Licence{
		ID: "lic-ais", Licensor: "AIS Provider Ltd",
		Grants: []Grant{
			{Use: UseProcess}, {Use: UseStore}, {Use: UseDisplay},
			{Use: UseDerive, Condition: "aggregate only"},
		},
		Purposes:      []policy.Purpose{policy.CaseInvestigation, policy.CustomerExport},
		Territories:   []string{"GB", "SG"},
		CustomerScope: []string{"acme"},
		RetainUntil:   &until,
		Attribution:   "Source: AIS Provider Ltd",
	}
}

func registry() Licence {
	return Licence{
		ID: "lic-registry", Licensor: "Public Registry",
		Grants: []Grant{
			{Use: UseProcess}, {Use: UseStore}, {Use: UseDisplay},
			{Use: UseDerive}, {Use: UseExport},
		},
		Purposes:                policy.Purposes(),
		RedistributionPermitted: true,
	}
}

func ctx(u Use) Context {
	return Context{Use: u, Purpose: policy.CaseInvestigation,
		Territory: "GB", Customer: "acme", At: t0}
}

// TestAnUngrantedUseIsProhibited. Silence is not permission.
func TestAnUngrantedUseIsProhibited(t *testing.T) {
	_, err := Check(ais(), ctx(UseExport))
	if !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("an ungranted use was permitted: %v", err)
	}
	if _, err := Check(ais(), ctx(UseTrain)); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("TRAIN was permitted by a licence that never granted it: %v", err)
	}
}

// TestTheZeroLicencePermitsNothing.
func TestTheZeroLicencePermitsNothing(t *testing.T) {
	for _, u := range Uses() {
		if _, err := Check(Licence{}, ctx(u)); err == nil {
			t.Errorf("the zero licence permitted %s", u)
		}
	}
}

// TestTheSixQuestionsAreAnsweredSeparately. Collapsing them forces the
// most permissive reading of every licence.
func TestTheSixQuestionsAreAnsweredSeparately(t *testing.T) {
	l := ais()
	permitted := map[Use]bool{}
	for _, u := range Uses() {
		p, err := Check(l, ctx(u))
		permitted[u] = err == nil && p.Permitted
	}
	if !permitted[UseProcess] || !permitted[UseDisplay] || !permitted[UseDerive] {
		t.Fatalf("granted uses were refused: %v", permitted)
	}
	if permitted[UseExport] || permitted[UseTrain] {
		t.Fatalf("ungranted uses were permitted: %v", permitted)
	}
}

// TestAConditionTravelsWithItsPermission. A permission whose condition
// is dropped is a permission the licensor did not give.
func TestAConditionTravelsWithItsPermission(t *testing.T) {
	p, err := Check(ais(), ctx(UseDerive))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range p.Conditions {
		if c == "aggregate only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the aggregate-only condition did not travel: %v", p.Conditions)
	}
}

func TestAttributionIsRequiredOnDisplayAndDerivation(t *testing.T) {
	p, err := Check(ais(), ctx(UseDisplay))
	if err != nil {
		t.Fatal(err)
	}
	if p.Attribution == "" {
		t.Fatal("a display permission carried no attribution")
	}
}

func TestPurposeTerritoryAndCustomerEachRefuseSeparately(t *testing.T) {
	l := ais()

	c := ctx(UseProcess)
	c.Purpose = policy.ModelTraining
	if _, err := Check(l, c); !errors.Is(err, ErrPurposeNotLicensed) {
		t.Errorf("unlicensed purpose accepted: %v", err)
	}

	c = ctx(UseProcess)
	c.Territory = "US"
	if _, err := Check(l, c); !errors.Is(err, ErrOutOfTerritory) {
		t.Errorf("out-of-territory use accepted: %v", err)
	}

	c = ctx(UseProcess)
	c.Customer = "beta-corp"
	if _, err := Check(l, c); !errors.Is(err, ErrScopeNotLicensed) {
		t.Errorf("out-of-scope customer accepted: %v", err)
	}
}

// TestRetentionGovernsMoreThanStorage: you cannot derive from what you
// were required to delete.
func TestRetentionGovernsMoreThanStorage(t *testing.T) {
	l := ais()
	c := ctx(UseDerive)
	c.At = t0.AddDate(2, 0, 0)
	if _, err := Check(l, c); !errors.Is(err, ErrRetentionEnded) {
		t.Fatalf("material was derived from after its retention ended: %v", err)
	}
}

func TestRedistributionIsSeparateFromExport(t *testing.T) {
	l := registry()
	c := ctx(UseExport)
	if _, err := Check(l, c); err != nil {
		t.Fatalf("a permitted export was refused: %v", err)
	}
	// Same licence, redistribution permitted -> still fine.
	c.Redistributing = true
	if _, err := Check(l, c); err != nil {
		t.Fatalf("permitted redistribution refused: %v", err)
	}
	// A licence that permits export but not redistribution.
	l.RedistributionPermitted = false
	if _, err := Check(l, c); err == nil {
		t.Fatal("redistribution was permitted by an export grant alone")
	}
}

// --- Combine: the intersection rule -------------------------------------

// TestADerivativeTakesTheIntersection is the rule that keeps licensed
// data out of exports its licensor never permitted.
func TestADerivativeTakesTheIntersection(t *testing.T) {
	d, err := Combine("lic-finding-1", ais(), registry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.grant(UseExport); ok {
		t.Fatal("THE DERIVATIVE MAY BE EXPORTED THOUGH ONE SOURCE PROHIBITS IT")
	}
	if _, ok := d.grant(UseProcess); !ok {
		t.Fatal("a use both sources granted was lost")
	}
	// The commercial feed's territory and customer limits govern.
	if len(d.Territories) != 2 || d.Territories[0] != "GB" {
		t.Fatalf("territories = %v, want the restrictive source's", d.Territories)
	}
	if len(d.CustomerScope) != 1 || d.CustomerScope[0] != "acme" {
		t.Fatalf("customer scope = %v", d.CustomerScope)
	}
	// And its purposes.
	if len(d.Purposes) != 2 {
		t.Fatalf("purposes = %v, want the intersection", d.Purposes)
	}
}

// TestConditionsAccumulateAcrossSources. An aggregate-only condition on
// one source binds the derivative even though the other had none.
func TestConditionsAccumulateAcrossSources(t *testing.T) {
	d, err := Combine("lic-finding-1", ais(), registry())
	if err != nil {
		t.Fatal(err)
	}
	g, ok := d.grant(UseDerive)
	if !ok {
		t.Fatal("DERIVE was lost though both sources grant it")
	}
	if g.Condition != "aggregate only" {
		t.Fatalf("condition = %q, want the restrictive source's", g.Condition)
	}
}

// TestAnEmptyRestrictionMeansUnrestricted. This is the asymmetry that
// a naive set intersection gets backwards: intersecting {GB,SG} with
// "unrestricted" must yield {GB,SG}, not the empty set.
func TestAnEmptyRestrictionMeansUnrestricted(t *testing.T) {
	d, err := Combine("lic-x", ais(), registry())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Territories) == 0 {
		t.Fatal("intersecting with an unrestricted licence produced an unrestricted result")
	}
	// And the other way: two unrestricted sources stay unrestricted.
	d2, err := Combine("lic-y", registry(), registry())
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Territories) != 0 {
		t.Fatalf("two unrestricted sources produced %v", d2.Territories)
	}
}

// TestTheShortestRetentionGoverns.
func TestTheShortestRetentionGoverns(t *testing.T) {
	short := ais()
	shortUntil := t0.AddDate(0, 1, 0)
	short.RetainUntil = &shortUntil

	d, err := Combine("lic-x", short, ais())
	if err != nil {
		t.Fatal(err)
	}
	if d.RetainUntil == nil || !d.RetainUntil.Equal(shortUntil) {
		t.Fatalf("retention = %v, want the shortest %v", d.RetainUntil, shortUntil)
	}
}

func TestRedistributionIsLostIfAnySourceProhibitsIt(t *testing.T) {
	d, err := Combine("lic-x", ais(), registry())
	if err != nil {
		t.Fatal(err)
	}
	if d.RedistributionPermitted {
		t.Fatal("redistribution survived a source that prohibits it")
	}
}

func TestAttributionsAccumulate(t *testing.T) {
	r := registry()
	r.Attribution = "Source: Public Registry"
	d, err := Combine("lic-x", ais(), r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Attribution == "" || d.Attribution == "Source: AIS Provider Ltd" {
		t.Fatalf("attribution = %q, want both licensors", d.Attribution)
	}
}

// TestDisjointSourcesProduceALicenceThatPermitsNothing, and that is a
// legitimate answer: it says these sources cannot lawfully be combined.
func TestDisjointSourcesProduceALicenceThatPermitsNothing(t *testing.T) {
	a := ais()
	b := ais()
	b.ID = "lic-b"
	b.Purposes = []policy.Purpose{policy.SecurityIncident}
	d, err := Combine("lic-x", a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Grants) != 0 {
		t.Fatalf("a derivative of purpose-disjoint sources granted %v", d.Grants)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("the empty derivative is not a valid licence: %v", err)
	}
	if _, err := Check(d, ctx(UseProcess)); err == nil {
		t.Fatal("the empty derivative permitted a use")
	}
}

func TestCombineRefusesNoSources(t *testing.T) {
	if _, err := Combine("lic-x"); !errors.Is(err, ErrNoLicence) {
		t.Fatalf("a derivative with no sources was licensed: %v", err)
	}
}

// --- Validation ---------------------------------------------------------

// TestALicenceThatGrantsMustNamePurposes. Unrestricted is expressible;
// it just has to be a decision rather than an omission.
func TestALicenceThatGrantsMustNamePurposes(t *testing.T) {
	l := Licence{ID: "l", Licensor: "x", Grants: []Grant{{Use: UseProcess}}}
	if err := l.Validate(); !errors.Is(err, ErrPurposeNotLicensed) {
		t.Fatalf("a licence with grants and no purposes was accepted: %v", err)
	}
	l.Purposes = policy.Purposes()
	if err := l.Validate(); err != nil {
		t.Fatalf("an explicitly unrestricted licence was refused: %v", err)
	}
}

// TestTrainingRequiresAnExplicitPurpose. Material in weights cannot be
// removed, so this grant is not inferable from a general permission.
func TestTrainingRequiresAnExplicitPurpose(t *testing.T) {
	l := Licence{
		ID: "l", Licensor: "x",
		Grants:   []Grant{{Use: UseTrain}},
		Purposes: []policy.Purpose{policy.CaseInvestigation},
	}
	if err := l.Validate(); !errors.Is(err, ErrPurposeNotLicensed) {
		t.Fatalf("TRAIN was granted without licensing MODEL_TRAINING: %v", err)
	}
	l.Purposes = append(l.Purposes, policy.ModelTraining)
	if err := l.Validate(); err != nil {
		t.Fatalf("a properly stated training licence was refused: %v", err)
	}
}

func TestMalformedLicencesAreRefused(t *testing.T) {
	bad := []Licence{
		{},
		{ID: "l"},
		{Licensor: "x"},
		{ID: "l", Licensor: "x", Grants: []Grant{{Use: "SELL"}}, Purposes: policy.Purposes()},
		{ID: "l", Licensor: "x", Grants: []Grant{{Use: UseProcess}, {Use: UseProcess}}, Purposes: policy.Purposes()},
		{ID: "l", Licensor: "x", Grants: []Grant{{Use: UseProcess}}, Purposes: []policy.Purpose{"WHATEVER"}},
	}
	for i, l := range bad {
		if err := l.Validate(); err == nil {
			t.Errorf("malformed licence %d accepted", i)
		}
	}
}

func TestCheckRefusesAZeroInstant(t *testing.T) {
	c := ctx(UseProcess)
	c.At = time.Time{}
	if _, err := Check(ais(), c); err == nil {
		t.Fatal("retention was evaluated with no instant")
	}
}
