package domain

import (
	"veriqo/pkg/hypothesis"
	"veriqo/pkg/reverseproof"
)

// Maritime is the deepest vertical, and the one where Law 5 is
// hardest to hold.
//
// # Absence of AIS is not evidence of concealment
//
// This is the domain's characteristic error, and it is worth stating
// plainly because it is committed constantly:
//
//	not transmitting  !=  deliberately dark  !=  concealing activity
//
// A gap in AIS coverage has at least five ordinary explanations --
// terrestrial receiver range, satellite revisit gaps, equipment
// failure, a legitimate security-related switch-off, and data-provider
// processing loss -- before any of them is deliberate concealment. The
// dark-activity template carries all of them as hypotheses, so an
// analyst has to score against them rather than skip past them.
//
// The consequence is that "the vessel went dark" is a claim about the
// DATA, and "the vessel concealed its activity" is a claim about the
// world, and the second needs evidence the first does not supply.

// MaritimeTemplates returns the maritime claim shapes.
func MaritimeTemplates() []Template {
	return []Template{
		{
			ID: "MAR-DARK-ACTIVITY", Domain: "maritime",
			Question: "did the vessel deliberately conceal its activity during a gap in AIS?",
			Conditions: []reverseproof.Condition{
				cond("C1", "a transmission gap actually occurred in the vessel's own data",
					"a contiguous period with no position report attributable to this vessel",
					0.4, 0.1, true, "AIS provider"),
				cond("C2", "the gap is not explained by receiver coverage",
					"terrestrial and satellite coverage maps for the gap's area and period",
					0.85, 0.3, true, "AIS provider", "satellite operator"),
				cond("C3", "the gap is not explained by equipment failure",
					"the vessel's technical log, a class or port-state inspection record",
					0.7, 0.6, true, "operator", "flag state", "port state"),
				cond("C4", "the vessel was somewhere during the gap",
					"an independent observation -- SAR, optical, a port call, a bunker "+
						"delivery note -- placing it",
					0.9, 0.7, true, "SAR provider", "EO provider", "port authority"),
				cond("C5", "the position at gap end is inconsistent with a lawful passage "+
					"from the position at gap start",
					"a speed-and-distance reconciliation over the gap",
					0.8, 0.1, true, "internal analysis"),
				cond("C6", "an activity requiring concealment is evidenced",
					"an STS transfer, a sanctioned-port call, or a cargo change across the gap",
					0.95, 0.8, true, "SAR provider", "cargo documents", "port records"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the vessel deliberately disabled or spoofed its transponder to "+
					"conceal an activity",
					[]string{"a gap unexplained by coverage", "an inconsistent position at gap end",
						"an evidenced activity during the gap"},
					"full coverage over the gap area with no reports from any vessel"),
				hyp("H2", "the gap is a receiver-coverage artefact",
					[]string{"the gap area is outside terrestrial range",
						"other vessels in the same area show the same gap",
						"the gap ends where coverage resumes"}),
				hyp("H3", "the transponder failed",
					[]string{"a technical log entry", "a repair record",
						"an inspection deficiency", "intermittent reports rather than a clean gap"}),
				hyp("H4", "the operator switched off for a lawful security reason",
					[]string{"transit of a high-risk area", "an industry security advisory in force",
						"a reported switch-off to the flag state or a naval authority"}),
				hyp("H5", "the data provider lost or filtered the reports",
					[]string{"the same gap absent from a second provider's feed",
						"a provider-acknowledged outage"}),
			},
			Refusals: []string{
				"a gap in a single provider's feed is a statement about that feed, not about " +
					"the vessel",
			},
		},
		{
			ID: "MAR-STS-TRANSFER", Domain: "maritime",
			Question: "did a ship-to-ship transfer occur between these two vessels?",
			Conditions: []reverseproof.Condition{
				cond("C1", "both vessels were in the same place at the same time",
					"positions within transfer range over a contiguous period",
					0.7, 0.2, true, "AIS provider", "SAR provider"),
				cond("C2", "both were substantially stationary or moving together",
					"speed and heading consistent with a moored transfer",
					0.8, 0.2, true, "AIS provider"),
				cond("C3", "the period was long enough for a transfer of the quantity alleged",
					"a duration consistent with the pumping rate for that cargo and quantity",
					0.75, 0.1, true, "internal analysis"),
				cond("C4", "a cargo change is evidenced on at least one vessel",
					"a draft change, a cargo declaration, or a subsequent discharge quantity",
					0.9, 0.6, true, "port authority", "survey", "EO provider"),
				cond("C5", "the rendezvous is not explained by an ordinary operation",
					"the absence of a bunkering, crew-change or towage record for the period",
					0.6, 0.4, true, "operator", "port authority"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "a cargo transfer took place",
					[]string{"co-location", "matched stationarity", "a cargo change on one vessel"}),
				hyp("H2", "the vessels bunkered or exchanged crew or stores",
					[]string{"a short rendezvous", "a bunker delivery note", "no cargo change"}),
				hyp("H3", "the co-location is a positional artefact",
					[]string{"low position accuracy", "a shared anchorage",
						"many vessels co-located in the same area"}),
				hyp("H4", "the vessels were anchored in the same designated area with no "+
					"interaction",
					[]string{"an anchorage designation", "a separation greater than transfer range",
						"independent arrival and departure times"}),
			},
		},
		{
			ID: "MAR-VOYAGE-RECONSTRUCTION", Domain: "maritime",
			Question: "what route did the vessel take between these two port calls?",
			Conditions: []reverseproof.Condition{
				cond("C1", "the departure is evidenced", "a port record or a pilot log",
					0.6, 0.2, true, "port authority"),
				cond("C2", "the arrival is evidenced", "a port record or a berth allocation",
					0.6, 0.2, true, "port authority"),
				cond("C3", "the track between them is continuous",
					"position reports with no unexplained gap",
					0.8, 0.1, true, "AIS provider"),
				cond("C4", "the track is physically possible",
					"speeds within the vessel's capability and a navigable route",
					0.9, 0.1, true, "internal analysis"),
				cond("C5", "the identity is the same vessel throughout",
					"a permanent identifier present across the whole track",
					0.95, 0.2, true, "registry", "AIS provider"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the reconstructed track is the voyage the vessel performed",
					[]string{"a continuous physically-possible track",
						"a consistent permanent identifier"}),
				hyp("H2", "the track conflates two vessels sharing a reassignable identifier",
					[]string{"an MMSI or call-sign match with no IMO",
						"an implausible speed between consecutive reports",
						"two simultaneous positions"}),
				hyp("H3", "part of the track is spoofed",
					[]string{"positions inconsistent with an independent observation",
						"an implausibly regular track", "a position over land"}),
			},
		},
		{
			ID: "MAR-ROUTE-DEVIATION", Domain: "maritime",
			Question: "did the vessel deviate from the contractual or customary route?",
			Conditions: []reverseproof.Condition{
				cond("C1", "a contractual or customary route is established",
					"a charterparty clause or a route the vessel and its peers customarily take",
					0.85, 0.3, true, "contract", "AIS provider"),
				cond("C2", "the actual track departs from it materially",
					"a distance or time difference beyond ordinary navigational variation",
					0.7, 0.1, true, "internal analysis"),
				cond("C3", "the departure is not explained by weather or traffic",
					"meteorological and routeing data for the period",
					0.8, 0.3, true, "weather provider"),
				cond("C4", "the departure is not explained by a permitted liberty",
					"the liberty clauses of the governing contract",
					0.9, 0.2, true, "contract"),
			},
			Hypotheses: []hypothesis.Hypothesis{
				hyp("H1", "the vessel deviated without contractual justification",
					[]string{"a material departure", "no weather explanation",
						"no applicable liberty"}),
				hyp("H2", "the route was weather-routed",
					[]string{"adverse conditions on the direct route",
						"a routeing advice", "peers deviating similarly"}),
				hyp("H3", "the deviation was within a permitted liberty",
					[]string{"a liberty clause covering the call", "notice to the charterer"}),
				hyp("H4", "the customary route was mis-established",
					[]string{"wide variation among peer voyages",
						"a seasonal or traffic-separation change"}),
			},
		},
	}
}
