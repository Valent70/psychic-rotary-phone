package provenance

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func at(h int) time.Time { return time.Date(2026, 9, 4, h, 0, 0, 0, time.UTC) }

func record() Record {
	obs := at(6)
	return Record{
		ID: "provenance:p1", EvidenceID: "evidence:e1", TenantID: "t-acme",
		Path: []Hop{
			{PartyID: "terminal-ops", Role: Observer, At: at(6)},
			{PartyID: "surveyor-ltd", Role: Producer, At: at(7)},
			{PartyID: "market-data-co", Role: Aggregator, At: at(8)},
			{PartyID: "veriqo", Role: Recipient, At: at(9)},
		},
		ObservedAt: &obs, ReceivedAt: at(9),
		LicenceID:         "lic-1",
		SourceContentHash: strings.Repeat("a", 64),
	}
}

// TestProducerIsNotSource is the distinction that makes the
// independence engine possible.
func TestProducerIsNotSource(t *testing.T) {
	r := record()
	p, err := r.ProducerID()
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.SourceID()
	if err != nil {
		t.Fatal(err)
	}
	if p == s {
		t.Fatal("producer and source resolved to the same party; the fields are not distinct")
	}
	if p != "terminal-ops" {
		t.Fatalf("producer = %q, want the FIRST observer", p)
	}
	if s != "market-data-co" {
		t.Fatalf("source = %q, want the hop before VERIQO", s)
	}
}

// TestTwoFeedsThroughDifferentAggregatorsShareAProducer.
//
// This is the case Law 6 is about, and it only works because the
// producer is resolved from the first hop rather than the last.
func TestTwoFeedsThroughDifferentAggregatorsShareAProducer(t *testing.T) {
	a := record()
	b := record()
	b.ID = "provenance:p2"
	b.Path[2].PartyID = "another-aggregator"

	pa, _ := a.ProducerID()
	pb, _ := b.ProducerID()
	if pa != pb {
		t.Fatalf("two feeds off one observer resolved to different producers: %s / %s", pa, pb)
	}
	sa, _ := a.SourceID()
	sb, _ := b.SourceID()
	if sa == sb {
		t.Fatal("premise changed: the two feeds should have different sources")
	}
}

// TestAnInterestedHopAnywhereIsReported.
//
// A neutral producer's data forwarded by a claimant's lawyer went
// through an interested party. Reading only the source or only the
// producer misses it.
func TestAnInterestedHopAnywhereIsReported(t *testing.T) {
	r := record()
	r.Path = append(r.Path[:3:3],
		Hop{PartyID: "claimant-solicitors", Role: Distributor, At: at(8), Interested: true},
		Hop{PartyID: "veriqo", Role: Recipient, At: at(9)},
	)
	interested, who := r.PassedThroughAnInterestedParty()
	if !interested {
		t.Fatal("a path through an interested party reported as clean")
	}
	if len(who) != 1 || !strings.Contains(who[0], "claimant-solicitors") {
		t.Fatalf("the interested party was not named: %v", who)
	}
	// Neither endpoint is interested, which is the point.
	if r.Path[0].Interested || r.Path[len(r.Path)-1].Interested {
		t.Fatal("premise changed: the interest is in the middle of the path")
	}
	if !strings.Contains(r.Describe(), "*") {
		t.Fatalf("Describe() does not mark the interested hop: %s", r.Describe())
	}
}

// TestTimeCannotRunBackwardsAlongThePath. A hop that handled the
// material before the party it received it from is a fabricated or
// mis-transcribed chain.
func TestTimeCannotRunBackwardsAlongThePath(t *testing.T) {
	r := record()
	r.Path[2].At = at(5)
	if err := r.Validate(); !errors.Is(err, ErrTimeInverted) {
		t.Fatalf("a path with inverted time was accepted: %v", err)
	}
}

// TestAPathMustEndAtVeriqo.
func TestAPathMustEndAtVeriqo(t *testing.T) {
	r := record()
	r.Path = r.Path[:3]
	if err := r.Validate(); !errors.Is(err, ErrNoTerminus) {
		t.Fatalf("a path not ending at VERIQO was accepted: %v", err)
	}

	// And RECIPIENT may not appear in the middle.
	r = record()
	r.Path[1].Role = Recipient
	if err := r.Validate(); err == nil {
		t.Fatal("RECIPIENT in the middle of the path was accepted")
	}
}

// TestAPathMustNameWhoObserved. Without it, evidence has a delivery
// route and no origin.
func TestAPathMustNameWhoObserved(t *testing.T) {
	r := record()
	r.Path = []Hop{
		{PartyID: "market-data-co", Role: Aggregator, At: at(8)},
		{PartyID: "veriqo", Role: Recipient, At: at(9)},
	}
	if err := r.Validate(); !errors.Is(err, ErrNoProducer) {
		t.Fatalf("a path with no observer or producer was accepted: %v", err)
	}
}

// TestACycleIsRefused. A party appearing twice makes the path
// unorderable and usually means two chains were spliced.
func TestACycleIsRefused(t *testing.T) {
	r := record()
	r.Path[2].PartyID = "surveyor-ltd"
	if err := r.Validate(); !errors.Is(err, ErrCycle) {
		t.Fatalf("a repeated party was accepted: %v", err)
	}
}

// TestTransformingPartiesAreDistinguishedFromCarriers.
func TestTransformingPartiesAreDistinguishedFromCarriers(t *testing.T) {
	r := record()
	tp := r.TransformingParties()
	if len(tp) != 2 {
		t.Fatalf("TransformingParties = %v, want the producer and the aggregator", tp)
	}
	for _, role := range []Role{Distributor, Custodian, Recipient, Observer} {
		if role.Transforms() {
			t.Errorf("%s is classified as transforming", role)
		}
	}
	for _, role := range []Role{Producer, Aggregator} {
		if !role.Transforms() {
			t.Errorf("%s is not classified as transforming", role)
		}
	}
}

// TestLatencyIsUnavailableRatherThanZero. A missing observation time
// must not read as "observed the instant it arrived".
func TestLatencyIsUnavailableRatherThanZero(t *testing.T) {
	r := record()
	d, ok := r.Latency()
	if !ok || d != 3*time.Hour {
		t.Fatalf("latency = %v, %v", d, ok)
	}
	r.ObservedAt = nil
	d, ok = r.Latency()
	if ok {
		t.Fatal("latency was reported for evidence with no observation time")
	}
	if d != 0 {
		t.Fatalf("the unavailable latency is %v rather than the zero value", d)
	}
}

// TestARecordWithoutASourceHashCannotBeConfirmed. There would be
// nothing a provider could be asked to check against.
func TestARecordWithoutASourceHashCannotBeConfirmed(t *testing.T) {
	r := record()
	r.SourceContentHash = ""
	if err := r.Validate(); err == nil {
		t.Fatal("a provenance record with no source content hash was accepted")
	}
}

func TestARecordWithoutALicenceIsRefused(t *testing.T) {
	r := record()
	r.LicenceID = ""
	if err := r.Validate(); err == nil {
		t.Fatal("material held under unknown terms was accepted")
	}
}

// TestTheDigestCoversThePath. If the path were outside the digest,
// the delivery route could be rewritten after the fact.
func TestTheDigestCoversThePath(t *testing.T) {
	base, err := record().Digest()
	if err != nil {
		t.Fatal(err)
	}
	r := record()
	r.Path[0].PartyID = "somebody-else"
	got, err := r.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got == base {
		t.Fatal("rewriting the observer did not change the provenance digest")
	}

	r = record()
	r.Path[1].Interested = true
	got, _ = r.Digest()
	if got == base {
		t.Fatal("marking a hop interested did not change the digest")
	}
}

// TestASingleHopPathIsVeriqoObservingDirectly.
func TestASingleHopPathIsVeriqoObservingDirectly(t *testing.T) {
	r := record()
	r.Path = []Hop{{PartyID: "veriqo", Role: Observer, At: at(9)}}
	// Still needs a terminus, so a direct observation is two hops or
	// one that is both. The stricter reading is correct: VERIQO
	// observing is still VERIQO receiving.
	if err := r.Validate(); err == nil {
		t.Fatal("a path with no RECIPIENT terminus was accepted")
	}
	r.Path = []Hop{
		{PartyID: "veriqo-sensor", Role: Observer, At: at(9)},
		{PartyID: "veriqo", Role: Recipient, At: at(9)},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("a direct observation was refused: %v", err)
	}
	s, _ := r.SourceID()
	if s != "veriqo-sensor" {
		t.Fatalf("source = %q", s)
	}
}
