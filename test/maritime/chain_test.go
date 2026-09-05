// Package maritime is the worked chain the audit asked for: one
// vertical, end to end, with an outsider invited to break it.
//
// # Why this test exists, and why it is not in test/integration
//
// test/integration proves the kernel composes. This proves something
// commercially different: that a MARITIME question, asked the way a
// customer asks it, comes out the other end as a Decision Passport a
// challenger can attack.
//
// The audit's warning was that VERIQO could become an assurance
// bureaucracy machine -- more registers, more ledgers, more manifests,
// without a single real investigation moving faster. This file is the
// counterweight. It runs:
//
//	observation -> evidence -> provenance -> independence
//	            -> contradiction -> hypothesis -> decision passport
//	            -> replay
//
// on a case with the shape real ones have: sources that look
// independent and are not, an anomaly with three innocent
// explanations, and a question the evidence does NOT settle.
//
// # The result it is designed to produce
//
// Not a finding. The case is built so that the honest answer is "no
// hypothesis is meaningfully ahead", because that is the answer real
// evidence gives most of the time, and a pipeline that can only
// produce conclusions is a pipeline that will produce them when it
// should not.
package maritime

import (
	"strings"
	"testing"
	"time"

	"veriqo/pkg/intel/maritime"
	"veriqo/pkg/intel/source"
	"veriqo/pkg/passport"
	"veriqo/pkg/qualification/independence"
)

var t0 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

// TestTheMaritimeChainProducesAChallengeablePassport.
func TestTheMaritimeChainProducesAChallengeablePassport(t *testing.T) {
	// --- OBSERVATION --------------------------------------------------
	//
	// A vessel with a six-hour gap in its track, a draught change
	// across it, and another vessel nearby before the gap.
	var reports []maritime.Report
	for i := 0; i <= 12; i++ { // 3 hours before the gap
		reports = append(reports, maritime.Report{
			VesselID: "imo:9074729", At: t0.Add(time.Duration(i*15) * time.Minute),
			Position: maritime.Position{LatDeg: 1.0, LonDeg: 103.8},
			SpeedKn:  f(0.4), DraughtM: f(7.1), ReceiverID: "terrestrial-sg",
			EvidenceRef: "evidenceversion:ais-1",
		})
	}
	// Six hours of silence, then the vessel reappears, deeper.
	for i := 0; i <= 8; i++ {
		reports = append(reports, maritime.Report{
			VesselID: "imo:9074729",
			At:       t0.Add(9*time.Hour + time.Duration(i*15)*time.Minute),
			Position: maritime.Position{LatDeg: 1.05, LonDeg: 103.85},
			SpeedKn:  f(0.5), DraughtM: f(13.4), ReceiverID: "satellite",
			EvidenceRef: "evidenceversion:ais-2",
		})
	}
	track, err := maritime.NewTrack("imo:9074729", reports)
	if err != nil {
		t.Fatalf("track: %v", err)
	}

	// The second vessel, present before the gap.
	var other []maritime.Report
	for i := 0; i <= 12; i++ {
		other = append(other, maritime.Report{
			VesselID: "imo:9111111", At: t0.Add(time.Duration(i*15) * time.Minute),
			Position: maritime.Position{LatDeg: 1.001, LonDeg: 103.801},
			SpeedKn:  f(0.3), ReceiverID: "terrestrial-sg",
			EvidenceRef: "evidenceversion:ais-3",
		})
	}
	otherTrack, err := maritime.NewTrack("imo:9111111", other)
	if err != nil {
		t.Fatal(err)
	}

	// --- ANOMALIES ----------------------------------------------------
	gaps := track.Gaps(maritime.GapPolicy{MinGap: 4 * time.Hour,
		ExpectedInterval: 15 * time.Minute, CoastalOnly: true})
	if len(gaps) != 1 {
		t.Fatalf("%d gaps", len(gaps))
	}
	draught := track.DraughtChange(1)
	if len(draught) != 1 {
		t.Fatalf("%d draught changes", len(draught))
	}
	rv := maritime.FindRendezvous(track, otherTrack,
		maritime.RendezvousPolicy{WithinNM: 0.5, MinDuration: 2 * time.Hour,
			PairWindow: 15 * time.Minute, MaxSpeedKn: 3})
	if len(rv) != 1 {
		t.Fatalf("%d rendezvous", len(rv))
	}

	// Every anomaly must carry its innocent explanations, or the
	// passport below would be an accusation.
	for _, a := range append(append([]maritime.Anomaly{}, gaps...), draught...) {
		if len(a.Alternatives) == 0 || strings.TrimSpace(a.Diagnostic) == "" {
			t.Fatalf("%s carries no alternatives or no diagnostic", a.Kind)
		}
	}

	// --- SOURCE INDEPENDENCE ------------------------------------------
	//
	// The trap real cases contain: three "sources" that are one
	// producer. Both AIS feeds and the port agent's report all
	// originate with the same terrestrial network.
	g, err := independence.NewGraph(
		independence.Node{ID: "ais-network", Producer: "ais-network-a"},
		independence.Node{ID: "feed-vendor-x", Producer: "vendor-x",
			DerivedFrom: []string{"ais-network"}},
		independence.Node{ID: "feed-vendor-y", Producer: "vendor-y",
			DerivedFrom: []string{"ais-network"}},
		independence.Node{ID: "port-agent-report", Producer: "port-agent-sg"},
		independence.Node{ID: "anonymous-tip", Producer: independence.UnknownProducer},
	)
	if err != nil {
		t.Fatalf("independence graph: %v", err)
	}
	count, err := g.CountProducers("feed-vendor-x", "feed-vendor-y", "port-agent-report",
		"anonymous-tip")
	if err != nil {
		t.Fatal(err)
	}
	if count.Observed != 4 {
		t.Fatalf("observed = %d", count.Observed)
	}
	// Two vendors reselling one network are one producer; the port
	// agent is a second; the tip resolves to nobody.
	if count.IndependentProducers != 2 {
		t.Fatalf("four sources resolved to %d independent producers",
			count.IndependentProducers)
	}
	if !count.Unassessable {
		t.Fatal("an unattributed tip did not make the structure unassessable")
	}
	if count.SatisfiesCorroboration(2) {
		t.Fatal("a structure containing an unresolvable source satisfied a corroboration " +
			"requirement its known part happens to meet")
	}

	// --- SOURCE TRUST VECTORS -----------------------------------------
	tip := trustVector(t, "anonymous-tip", map[source.Dimension]source.Grade{
		source.Attribution:        source.Unknown,
		source.Provenance:         source.Unknown,
		source.Independence:       source.Unknown,
		source.ReliabilityHistory: source.NotAssessed,
	})
	if d, _ := tip.Weakest(); d != source.Attribution && d != source.ReliabilityHistory {
		t.Fatalf("the tip's weakest dimension is %s", d)
	}
	// The tip is lawful to hold and still cannot found anything: those
	// are separate questions, and the vector keeps them separate.
	if err := tip.Usable(); err != nil {
		t.Fatalf("a lawfully held tip was made unusable: %v", err)
	}
	tipMaterial := source.Material{
		ID: "mat:tip-1", Class: source.AnonymousDisclosure,
		Lawfulness: source.Established, ObservedAt: t0, ContentHash: "h",
		Basis: &source.LegalBasis{Jurisdiction: "Singapore", Purpose: "cargo investigation",
			Opinion: "memo 2026-02", By: "external counsel", At: t0.Add(-24 * time.Hour)},
	}
	if err := tipMaterial.Permit(source.Found, t0); err == nil {
		t.Fatal("an anonymous tip was permitted to found a finding")
	}
	if err := tipMaterial.Permit(source.Lead, t0); err != nil {
		t.Fatalf("an anonymous tip was refused as a lead: %v", err)
	}

	// --- HYPOTHESES AND THE PASSPORT ----------------------------------
	d := passport.Decision{
		Case:     "case-sg-2026-014",
		Subject:  "IMO 9074729, Singapore eastern anchorage, 1-2 March 2026",
		Question: "Did this vessel load cargo during the six-hour reporting gap?",
		Counts: passport.Counts{
			Observations: len(reports) + len(other), Producers: 3,
			IndependentProducers: count.IndependentProducers,
			Contradictions:       1, Unresolved: 2, Unassessable: 1,
		},
		EvidenceQuality: "ATTRIBUTION (the tip resolves to no producer)",
		Timeline: []string{
			t0.Format("2006-01-02 15:04") + "  continuous reporting begins, draught 7.1 m",
			t0.Add(3*time.Hour).Format("2006-01-02 15:04") + "  last report before the gap",
			t0.Add(9*time.Hour).Format("2006-01-02 15:04") + "  reporting resumes, draught 13.4 m",
		},
		Contradictions: []string{
			"the port agent records no cargo operation in the window; the draught change " +
				"is consistent with one",
		},
		Unresolved: []string{
			"whether the gap is a transmitter event or a receiver event: the network is " +
				"terrestrial-only and the vessel was at the edge of its coverage",
			"whether the 7.1 m draught was current or stale at the time it was reported",
		},
		Hypotheses: []passport.Hypothesis{
			{ID: "H1", Weight: 0.38,
				Statement: "cargo was loaded during the gap"},
			{ID: "H2", Weight: 0.34,
				Statement: "ballast was taken on and the earlier draught was stale"},
			{ID: "H3", Weight: 0.28,
				Statement: "the draught values are a data-entry artefact and nothing happened"},
			{ID: "H4", Weight: 0, Eliminated: true,
				Statement: "the vessel left the anchorage and returned",
				Because: "the position either side of the gap differs by under 4 NM, which " +
					"no departure and return could produce in six hours at anchorage speeds"},
		},
		Basis: "a 6.3 m increase in reported draught across a six-hour reporting gap, with " +
			"a second vessel alongside for three hours before it. The draught change is " +
			"the only physical observation; everything else is context",
		Limitations: []string{
			"draught is entered by hand and is stale as often as it is wrong",
			"the reporting gap is a fact about the RECORD: the observing network is " +
				"terrestrial-only and this position is at the edge of its coverage",
			"proximity is not a transfer: the two vessels were in the same anchorage",
			"the anonymous tip is a lead only and founds nothing",
		},
		Route: passport.Route{
			Overturns: "the hypothesis that cargo was loaded during the gap",
			Steps: []passport.Step{
				{N: 1, Action: "obtain the terminal's berth and crane records for the window",
					Party: "the receiver, from the terminal", Produces: "a record of any " +
						"cargo operation", Effect: "no operation recorded by the terminal " +
						"moves weight from H1 to H2 and H3", Cost: "days"},
				{N: 2, Action: "obtain the coverage map of the observing network for the " +
					"window", Party: "the AIS provider",
					Produces: "whether the gap is a receiver gap",
					Effect: "a coverage hole at that position removes the gap as evidence " +
						"of anything", Cost: "days"},
				{N: 3, Action: "obtain the vessel's ballast log for the voyage",
					Party: "the owner", Produces: "an alternative account of the draught change",
					Effect: "a matching ballast operation makes H2 the leading account",
					Cost:   "weeks"},
				{N: 4, Action: "obtain a second AIS feed from a network with satellite coverage",
					Party: "a second provider", Produces: "an independent track across the gap",
					Effect: "a continuous track across the window settles the question " +
						"outright", Cost: "commercial",
					Blocked: "no contract with a satellite AIS provider exists (ED-007)"},
			},
			IfAllFail: "the draught change stands unexplained. That is not evidence that " +
				"cargo was loaded: it means no available evidence distinguishes three " +
				"accounts, and the case remains undecided",
		},
		ReplayVerifiable:    true,
		Qualification:       "INTERNALLY_ASSURED",
		ExternallyValidated: false,
	}

	if err := d.Validate(); err != nil {
		t.Fatalf("the passport does not validate: %v", err)
	}

	// --- THE ANSWER THE CASE ACTUALLY GIVES ---------------------------
	//
	// A pipeline that can only produce conclusions will produce them
	// when it should not. This case is built so the honest answer is
	// that nothing is decided.
	lead, decided := d.Leading()
	if lead.ID != "H1" {
		t.Fatalf("leading hypothesis is %s", lead.ID)
	}
	if decided {
		t.Fatalf("0.38 against 0.34 was reported as decided; a pipeline that can only " +
			"produce conclusions will produce them when it should not")
	}

	out := d.Render()
	for _, want := range []string{
		"NO HYPOTHESIS IS MEANINGFULLY AHEAD",
		"republication is not corroboration",
		"UNASSESSABLE           1",
		"DISPROOF ROUTE",
		"BLOCKED:  no contract with a satellite AIS provider",
		"EXTERNAL VALIDATION  NOT PERFORMED",
		"the case remains undecided",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the passport omits %q:\n%s", want, out)
		}
	}

	// The eliminated hypothesis is kept, with its reason: knowing what
	// was considered and excluded is most of what makes a conclusion
	// defensible.
	if !strings.Contains(out, "x H4") || !strings.Contains(out, "no departure and return") {
		t.Fatalf("the eliminated hypothesis was dropped:\n%s", out)
	}
}

func trustVector(t *testing.T, id string, override map[source.Dimension]source.Grade) *source.Vector {
	t.Helper()
	var rs []source.Reading
	for _, d := range source.Dimensions() {
		g := source.Confirmed
		if o, ok := override[d]; ok {
			g = o
		}
		r := source.Reading{Dimension: d, Grade: g, Basis: "assessed for this case"}
		if g == source.NotAssessed {
			r.Basis = ""
		}
		if g == source.Partial {
			r.Remainder = "stated"
		}
		rs = append(rs, r)
	}
	v, err := source.NewVector(id, rs...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestThePassportRefusesToLeadWithItsConclusion.
//
// Ordering is not presentation here: a document that leads with its
// conclusion and buries its contradictions is making an argument, and
// this is not supposed to be an argument.
func TestThePassportRefusesToLeadWithItsConclusion(t *testing.T) {
	d := minimal()
	out := d.Render()
	iContra := strings.Index(out, "CONTRADICTIONS")
	iHyp := strings.Index(out, "HYPOTHESES")
	iQual := strings.Index(out, "QUALIFICATION")
	if iContra < 0 || iHyp < 0 || iQual < 0 {
		t.Fatalf("the passport is missing a section:\n%s", out)
	}
	if iContra > iHyp {
		t.Fatal("the hypotheses are rendered above the contradictions")
	}
	if iQual < iHyp {
		t.Fatal("the qualification is rendered above the hypotheses")
	}
}

// TestAPassportWithNoAlternativesIsRefused. A conclusion with no
// alternatives considered is an assertion.
func TestAPassportWithNoAlternativesIsRefused(t *testing.T) {
	d := minimal()
	d.Hypotheses = nil
	if err := d.Validate(); err == nil {
		t.Fatal("a passport with no competing hypotheses validated")
	}
	d = minimal()
	d.Limitations = nil
	if err := d.Validate(); err == nil {
		t.Fatal("a passport with no limitations validated")
	}
}

// TestTheHeaderCountsCannotDriftFromTheLists.
//
// A summary that disagrees with its own body is how a document becomes
// a misrepresentation without anybody lying.
func TestTheHeaderCountsCannotDriftFromTheLists(t *testing.T) {
	d := minimal()
	d.Counts.Contradictions = 7
	if err := d.Validate(); err == nil {
		t.Fatal("a header claiming seven contradictions over a list of one validated")
	}
	d = minimal()
	d.Counts.IndependentProducers = d.Counts.Producers + 1
	if err := d.Validate(); err == nil {
		t.Fatal("more independent producers than producers validated")
	}
	d = minimal()
	d.Counts.Producers = d.Counts.Observations + 1
	if err := d.Validate(); err == nil {
		t.Fatal("more producers than observations validated")
	}
}

func minimal() passport.Decision {
	return passport.Decision{
		Case: "c", Subject: "s", Question: "q?", Basis: "b",
		EvidenceQuality: "ATTRIBUTION",
		Counts: passport.Counts{Observations: 3, Producers: 2, IndependentProducers: 1,
			Contradictions: 1, Unresolved: 1},
		Contradictions: []string{"two records disagree"},
		Unresolved:     []string{"which one is right"},
		Hypotheses: []passport.Hypothesis{
			{ID: "H1", Weight: 0.6, Statement: "a"},
			{ID: "H2", Weight: 0.4, Statement: "b"},
		},
		Limitations: []string{"nothing here has been externally reviewed"},
		Route: passport.Route{
			Overturns: "the leading account",
			Steps: []passport.Step{{N: 1, Action: "obtain the other record",
				Party: "the counterparty", Produces: "a second figure",
				Effect: "a differing figure removes the basis"}},
			IfAllFail: "the account stands on the evidence presented, which is not proof",
		},
		Qualification: "INTERNALLY_ASSURED",
	}
}
