package connectors

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/policy"
	"veriqo/pkg/rights"
)

var now = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func licence(id string, uses ...rights.Use) rights.Licence {
	var gs []rights.Grant
	for _, u := range uses {
		gs = append(gs, rights.Grant{Use: u})
	}
	until := now.AddDate(1, 0, 0)
	return rights.Licence{ID: id, Licensor: "a licensor", Grants: gs,
		Purposes:      []policy.Purpose{policy.CaseInvestigation},
		CustomerScope: []string{"acme"},
		RetainUntil:   &until}
}

// fakeSource is a test double. It is named "fake" so nothing mistakes
// it for a provider integration.
type fakeSource struct {
	caps      SourceCapabilities
	available bool
}

func (f fakeSource) Capabilities() SourceCapabilities { return f.caps }

func (f fakeSource) Discover(_ context.Context, q Query) (Availability, error) {
	if err := CheckDiscovery(f.caps, q); err != nil {
		return Availability{}, err
	}
	a := Availability{SourceID: f.caps.SourceID, Available: f.available, Count: 12,
		CoverageGap: "no coverage south of 60S"}
	// Discovery and acquisition are separately licensed.
	_, err := CheckAcquisition(f.caps, AcquisitionRequest{
		Capability: q.Capability, Subject: q.Subject, Purpose: q.Purpose,
		Customer: q.Customer, CaseID: "case-1", At: now})
	a.AcquisitionPermitted = err == nil
	if err != nil {
		a.PermissionNote = err.Error()
	}
	return a, nil
}

func (f fakeSource) Acquire(_ context.Context, req AcquisitionRequest) (Acquired, error) {
	p, err := CheckAcquisition(f.caps, req)
	if err != nil {
		return Acquired{}, err
	}
	return Acquired{SourceID: f.caps.SourceID, Content: []byte("position report"),
		MediaType: "application/json", RetrievedAt: req.At, Permission: p}, nil
}

func (f fakeSource) Validate(context.Context, string) (bool, error) { return true, nil }

func caps(id, producer string, uses ...rights.Use) SourceCapabilities {
	if len(uses) == 0 {
		uses = []rights.Use{rights.UseProcess, rights.UseStore, rights.UseDerive}
	}
	return SourceCapabilities{
		SourceID: id, ProducerID: producer,
		Kinds:           []Capability{VesselPosition},
		AcquisitionMode: "terrestrial and satellite receivers",
		Licence:         licence("lic-"+id, uses...),
		CoverageNotes:   []string{"no coverage south of 60S", "satellite revisit up to 6h"},
		LatencyTypical:  5 * time.Minute,
		RateLimit:       1000, RateLimitWindow: time.Hour,
	}
}

func request() AcquisitionRequest {
	return AcquisitionRequest{Capability: VesselPosition, Subject: "IMO9074729",
		Purpose: policy.CaseInvestigation, Customer: "acme", At: now, CaseID: "case-1"}
}

// TestASourceMustNameItsProducer.
//
// A source that will not say who makes its observations cannot be
// assessed for independence against any other source, which is the
// whole basis of corroboration.
func TestASourceMustNameItsProducer(t *testing.T) {
	c := caps("feed-a", "")
	if err := c.Validate(); !errors.Is(err, ErrNoProducer) {
		t.Fatalf("a source with no producer was accepted: %v", err)
	}
	if !strings.Contains(err(c).Error(), "cannot be assessed for independence") {
		t.Fatalf("the refusal does not state the consequence")
	}
}

func err(c SourceCapabilities) error { return c.Validate() }

// TestFiveSourcesCanBeOneObservation.
//
// The question a planner has is not "how many sources" but "how many
// independent observations".
func TestFiveSourcesCanBeOneObservation(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"feed-a", "feed-b", "feed-c", "feed-d", "feed-e"} {
		if err := r.Add(fakeSource{caps: caps(id, "producer-x"), available: true}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.Offering(VesselPosition)); n != 5 {
		t.Fatalf("%d sources offer positions", n)
	}
	producers := r.IndependentProducers(VesselPosition)
	if len(producers) != 1 {
		t.Fatalf("IndependentProducers = %v", producers)
	}
	rep := r.Report()
	if !strings.Contains(rep, "they do not corroborate one another") {
		t.Fatalf("the report does not warn that the sources share a producer:\n%s", rep)
	}
}

// TestDiscoveryAndAcquisitionAreSeparatelyLicensed.
//
// Many feeds permit a metadata search under terms that do not permit
// retrieving the record.
func TestDiscoveryAndAcquisitionAreSeparatelyLicensed(t *testing.T) {
	// A licence permitting processing but NOT storage: the record can
	// be searched and cannot enter the evidence fabric.
	s := fakeSource{caps: caps("feed-a", "producer-x", rights.UseProcess), available: true}

	av, derr := s.Discover(context.Background(), Query{Capability: VesselPosition,
		Subject: "IMO9074729", Purpose: policy.CaseInvestigation, Customer: "acme"})
	if derr != nil {
		t.Fatalf("discovery was refused: %v", derr)
	}
	if !av.Available {
		t.Fatal("the source reported nothing available")
	}
	if av.AcquisitionPermitted {
		t.Fatal("acquisition was reported permitted under a process-only licence")
	}
	if !strings.Contains(av.PermissionNote, "cannot enter the evidence fabric") {
		t.Fatalf("the note does not explain the refusal: %q", av.PermissionNote)
	}

	if _, aerr := s.Acquire(context.Background(), request()); !errors.Is(aerr, ErrNotLicensed) {
		t.Fatalf("an unlicensed acquisition succeeded: %v", aerr)
	}
}

// TestTheLicenceIsCheckedBeforeTheFetch.
//
// A connector that fetched first would already hold the data.
func TestTheLicenceIsCheckedBeforeTheFetch(t *testing.T) {
	s := fakeSource{caps: caps("feed-a", "producer-x"), available: true}
	req := request()
	req.Customer = "beta-corp" // outside the licensed customer scope

	got, err := s.Acquire(context.Background(), req)
	if err == nil {
		t.Fatal("material was acquired for an out-of-scope customer")
	}
	if len(got.Content) != 0 {
		t.Fatal("CONTENT WAS RETURNED ALONGSIDE THE REFUSAL")
	}
	if !errors.Is(err, ErrNotLicensed) {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// TestAnAcquisitionMustBeTiedToACase. Material obtained outside one
// has no purpose to be limited by.
func TestAnAcquisitionMustBeTiedToACase(t *testing.T) {
	req := request()
	req.CaseID = ""
	if err := req.Validate(); err == nil {
		t.Fatal("an acquisition with no case was accepted")
	}
	req = request()
	req.Purpose = ""
	if err := req.Validate(); err == nil {
		t.Fatal("an acquisition with no purpose was accepted")
	}
}

// TestASourceMustStateItsCoverageGaps.
//
// A caller needs the gaps to interpret an absence: "no position
// reports" from a source with no southern coverage says nothing about
// a vessel at 65S.
func TestASourceMustStateItsCoverageGaps(t *testing.T) {
	c := caps("feed-a", "producer-x")
	c.CoverageNotes = nil
	if err := c.Validate(); err == nil {
		t.Fatal("a source claiming total coverage was accepted")
	}
	// And the gap travels with a discovery result.
	s := fakeSource{caps: caps("feed-a", "producer-x")}
	av, _ := s.Discover(context.Background(), Query{Capability: VesselPosition,
		Purpose: policy.CaseInvestigation, Customer: "acme"})
	if av.CoverageGap == "" {
		t.Fatal("a discovery result carries no coverage gap")
	}
}

// TestASourceMustDeclareARateLimit. An unbounded connector is a way to
// be cut off mid-case.
func TestASourceMustDeclareARateLimit(t *testing.T) {
	c := caps("feed-a", "producer-x")
	c.RateLimit = 0
	if err := c.Validate(); err == nil {
		t.Fatal("a source with no rate limit was accepted")
	}
}

// TestASourceMustSayHowItObtainsWhatItSells. It bears on independence
// and on quality.
func TestASourceMustSayHowItObtainsWhatItSells(t *testing.T) {
	c := caps("feed-a", "producer-x")
	c.AcquisitionMode = ""
	if err := c.Validate(); err == nil {
		t.Fatal("a source that does not say how it obtains its data was accepted")
	}
}

// TestASourceWithNoLicenceIsRefused.
func TestASourceWithNoLicenceIsRefused(t *testing.T) {
	c := caps("feed-a", "producer-x")
	c.Licence = rights.Licence{}
	if err := c.Validate(); !errors.Is(err, ErrNoLicence) {
		t.Fatalf("a source with no licence was accepted: %v", err)
	}
}

// TestAnUnofferedCapabilityIsRefused.
func TestAnUnofferedCapabilityIsRefused(t *testing.T) {
	s := fakeSource{caps: caps("feed-a", "producer-x")}
	req := request()
	req.Capability = BankRecord
	if _, err := s.Acquire(context.Background(), req); !errors.Is(err, ErrNotCapable) {
		t.Fatalf("a capability the source does not offer was acquired: %v", err)
	}
}

// TestNoProviderNameAppearsInTheVocabulary.
//
// The capability vocabulary describes EVIDENCE KINDS. A vendor name in
// it would make the core depend on a commercial relationship.
func TestNoProviderNameAppearsInTheVocabulary(t *testing.T) {
	vendors := []string{"kpler", "spire", "marinetraffic", "refinitiv", "bloomberg",
		"dowjones", "lloyds", "clarksons", "windward"}
	for _, c := range Capabilities() {
		lower := strings.ToLower(string(c))
		for _, v := range vendors {
			if strings.Contains(lower, v) {
				t.Errorf("the capability %s names a vendor", c)
			}
		}
	}
}

// TestCapabilitiesIsAnswerableWithoutANetworkCall.
//
// A planner needs to know what could supply a missing observation
// before deciding to ask, so the method takes no context.
func TestCapabilitiesIsAnswerableWithoutANetworkCall(t *testing.T) {
	s := fakeSource{caps: caps("feed-a", "producer-x")}
	// The compile-time property: Capabilities() takes no ctx. This
	// asserts the behaviour a planner depends on.
	c := s.Capabilities()
	if c.SourceID != "feed-a" {
		t.Fatalf("SourceID = %s", c.SourceID)
	}
	if len(c.Kinds) == 0 {
		t.Fatal("a source described nothing without being contacted")
	}
}

// TestDuplicateSourcesAreRefused.
func TestDuplicateSourcesAreRefused(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(fakeSource{caps: caps("feed-a", "producer-x")}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(fakeSource{caps: caps("feed-a", "producer-y")}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("a duplicate source id was accepted: %v", err)
	}
	if _, err := r.Get("nope"); !errors.Is(err, ErrUnknownSource) {
		t.Fatal("an unknown source resolved")
	}
}

// TestASuccessfulAcquisitionCarriesItsPermission, so the evidence
// version can record the terms it was obtained under.
func TestASuccessfulAcquisitionCarriesItsPermission(t *testing.T) {
	s := fakeSource{caps: caps("feed-a", "producer-x"), available: true}
	got, err := s.Acquire(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Permission.Permitted {
		t.Fatal("an acquisition succeeded with no permission recorded")
	}
	if got.Permission.Licence == "" {
		t.Fatal("the permission does not name the licence it rests on")
	}
	if len(got.Content) == 0 {
		t.Fatal("a permitted acquisition returned nothing")
	}
}
