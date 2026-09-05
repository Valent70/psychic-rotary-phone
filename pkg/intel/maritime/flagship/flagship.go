// Package flagship is one maritime case, worked end to end.
//
// # Why one worked case is worth more than another hundred tests
//
// Everything else in this repository establishes that the machinery
// behaves. None of it shows a maritime reader what the machinery is
// FOR. A surveyor, an adjuster or a lawyer asked to evaluate VERIQO
// cannot read a mutation suite; they can read a case, and they will
// know within two pages whether the reasoning is competent.
//
// So this package builds the whole chain the audit named:
//
//	vessel -> cargo -> voyage -> port event -> AIS -> weather ->
//	document -> contradiction -> source independence -> hypotheses ->
//	decision -> passport -> disproof route
//
// and produces a Decision Passport from it.
//
// # It is synthetic, and that is stated everywhere it appears
//
// Every position, document, timestamp and party below was constructed
// for this case. It contains no real vessel, no real cargo and no real
// dispute. What it establishes is that the reasoning chain runs and
// produces a document a professional can attack. What it establishes
// about real data is NOTHING, and the passport says so on its face
// rather than in a footnote.
//
// The case was also built to come out AMBIGUOUS. A worked example that
// resolves cleanly demonstrates the machinery on the one input shape
// that never occurs, and it teaches the reader that VERIQO produces
// answers. The valuable demonstration is a case where the evidence is
// genuinely insufficient and the system says so while still being
// useful -- by naming exactly what would settle it, and what it would
// cost to get.
package flagship

import (
	"fmt"
	"time"

	"veriqo/pkg/intel/maritime"
	"veriqo/pkg/passport/commercial"
	"veriqo/pkg/qualification/independence"
)

// Window is the case's time window.
var (
	start = time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	end   = time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
)

func at(h, m int) time.Time {
	return start.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

func f(v float64) *float64 { return &v }

// Track builds the AIS track the case rests on.
//
// The shape: a laden-looking vessel proceeds through the Singapore
// Strait, stops reporting for just over six hours, and resumes 3.1 NM
// away with a draught 6.3 m greater. That is the classic ship-to-ship
// transfer signature, and it is also what a jammed receiver plus a
// stale manually-entered draught looks like.
func Track() (*maritime.Track, error) {
	return maritime.NewTrack("MMSI:563117000", []maritime.Report{
		{VesselID: "MMSI:563117000", At: at(2, 0),
			Position: maritime.Position{LatDeg: 1.2183, LonDeg: 103.8210},
			SpeedKn:  f(11.4), CourseDeg: f(88), DraughtM: f(7.1),
			NavStatus: "UNDER_WAY_USING_ENGINE", ReceiverID: "TERRESTRIAL-SG-04",
			EvidenceRef: "evidenceversion:ais-a-0001"},
		{VesselID: "MMSI:563117000", At: at(3, 30),
			Position: maritime.Position{LatDeg: 1.2210, LonDeg: 103.9016},
			SpeedKn:  f(10.9), CourseDeg: f(91), DraughtM: f(7.1),
			NavStatus: "UNDER_WAY_USING_ENGINE", ReceiverID: "TERRESTRIAL-SG-04",
			EvidenceRef: "evidenceversion:ais-a-0002"},
		{VesselID: "MMSI:563117000", At: at(5, 12),
			Position: maritime.Position{LatDeg: 1.2244, LonDeg: 103.9788},
			SpeedKn:  f(4.2), CourseDeg: f(94), DraughtM: f(7.1),
			NavStatus: "UNDER_WAY_USING_ENGINE", ReceiverID: "TERRESTRIAL-SG-04",
			EvidenceRef: "evidenceversion:ais-a-0003"},
		// --- reporting gap, 6h14m ---
		{VesselID: "MMSI:563117000", At: at(11, 26),
			Position: maritime.Position{LatDeg: 1.2699, LonDeg: 104.0142},
			SpeedKn:  f(3.1), CourseDeg: f(15), DraughtM: f(13.4),
			NavStatus: "UNDER_WAY_USING_ENGINE", ReceiverID: "SAT-B-11",
			EvidenceRef: "evidenceversion:ais-a-0004"},
		{VesselID: "MMSI:563117000", At: at(13, 5),
			Position: maritime.Position{LatDeg: 1.3402, LonDeg: 104.0511},
			SpeedKn:  f(9.8), CourseDeg: f(22), DraughtM: f(13.4),
			NavStatus: "UNDER_WAY_USING_ENGINE", ReceiverID: "SAT-B-11",
			EvidenceRef: "evidenceversion:ais-a-0005"},
	})
}

// Accounts are the reports received about the transfer, from what look
// like five separate sources.
//
// Three of them are copies. Two share a transposed IMO digit that
// could not have been made independently, and one is a near-verbatim
// reprint. Copy detection is what turns "five sources agree" into
// "three observations, of which two may share an upstream".
func Accounts() []independence.Account {
	const wire = "Vessel reported alongside an unidentified tanker east of the " +
		"Singapore Strait during a six-hour AIS reporting gap on 14 June, with a " +
		"draught increase consistent with a cargo transfer."
	return []independence.Account{
		{ID: "acct:wire-1", Producer: "producer:maritime-wire",
			Values: map[string]string{"imo": "9401267", "date": "2026-06-14", "location": "east of strait"},
			Text:   wire},
		{ID: "acct:trade-daily", Producer: "producer:trade-daily",
			Values: map[string]string{"imo": "9401627", "date": "2026-06-14", "location": "east of strait"},
			Text:   wire + " Industry sources declined to comment."},
		{ID: "acct:bunker-report", Producer: "producer:bunker-report",
			Values: map[string]string{"imo": "9401627", "date": "2026-06-14", "location": "east of strait"},
			Text:   "A tanker was reported alongside an unidentified vessel east of the strait."},
		{ID: "acct:port-agent", Producer: "producer:port-agent-sg",
			Values: map[string]string{"imo": "9401267", "date": "2026-06-14", "location": "OPL anchorage"},
			Text: "Agent's note: vessel did not berth in the port limits on 14 June. " +
				"No terminal call was recorded against this hull."},
		{ID: "acct:class-society", Producer: "producer:class-society",
			Values: map[string]string{"imo": "9401267", "date": "2026-06-15", "location": "unknown"},
			Text: "Annual survey record updated 15 June. Draught marks inspected; no " +
				"remarks recorded regarding loading condition.",
		},
	}
}

// Passport builds the case's decision passport.
func Passport() (commercial.Passport, error) {
	tr, err := Track()
	if err != nil {
		return commercial.Passport{}, err
	}
	gaps := tr.Gaps(maritime.DefaultGapPolicy())
	if len(gaps) == 0 {
		return commercial.Passport{}, fmt.Errorf("flagship: the constructed track has no " +
			"reporting gap, so the case it was built to demonstrate does not arise")
	}
	tri := maritime.Explain(gaps[0]) // no modalities available: that is the finding

	an, err := independence.Detect(independence.DefaultPolicy(), Accounts()...)
	if err != nil {
		return commercial.Passport{}, err
	}
	eff, note := an.EffectiveCount()

	p := commercial.Passport{
		SyntheticNotice: "This case is synthetic. Every position, document, party and " +
			"timestamp in it was constructed by VERIQO. It contains no real vessel and " +
			"no real dispute, and establishes nothing whatever about VERIQO's behaviour " +
			"on real data.",
		Case: commercial.Case{
			ID: "CASE-2026-0614-SGSTRAIT", From: start, To: end,
			Jurisdiction: "Singapore / England & Wales (charterparty law and arbitration seat)",
			DeclaredPurpose: "Cargo quantity dispute under a voyage charter: to establish " +
				"whether cargo was loaded outside the nominated load port",
			Tenant: "tenant:demo",
		},
		Question: "Did MMSI 563117000 load cargo during the AIS reporting gap of " +
			"14 June 2026, at a position outside the nominated load port?",

		Observations: []commercial.Observation{
			{Ref: "O1", At: at(5, 12), SourceRef: "S1",
				What: "Last position before the gap: 1.2244N 103.9788E, speed over " +
					"ground 4.2 kn, reported draught 7.1 m, received by a terrestrial station."},
			{Ref: "O2", At: at(11, 26), SourceRef: "S1",
				What: "First position after the gap: 1.2699N 104.0142E, reported draught " +
					"13.4 m, received by a satellite receiver. The interval is 6 h 14 m " +
					"and the positions are 3.1 NM apart."},
			{Ref: "O3", At: at(11, 26), SourceRef: "S1",
				What: "Reported draught increased by 6.3 m across the gap. Draught is " +
					"entered manually by the crew and is not measured by the transponder."},
			{Ref: "O4", At: at(5, 12), SourceRef: "S2",
				What: "The receiver changed from TERRESTRIAL-SG-04 to SAT-B-11 across " +
					"the gap. The two networks have different coverage footprints."},
			{Ref: "O5", At: at(14, 0), SourceRef: "S3",
				What: "The port agent records no terminal call against this hull within " +
					"port limits on 14 June."},
			{Ref: "O6", At: at(9, 0), SourceRef: "S4",
				What: "Metocean record for the area and window: wind 8-11 kn, sea state " +
					"2, no condition that would prevent a transfer and none that would " +
					"require one."},
			{Ref: "O7", At: at(20, 0), SourceRef: "S5",
				What: "A bill of lading for the voyage states a loaded quantity of " +
					"31,450 MT at the nominated load port, dated 13 June."},
		},

		Sources: []commercial.Source{
			{Ref: "S1", Producer: "AIS network A (terrestrial and satellite receivers)",
				Vendor: "a commercial aggregator", Timestamp: at(11, 30),
				Acquisition: "commercial feed under a fixed-term licence",
				Rights:      "licensed for internal analysis; redistribution of raw positions prohibited",
				LawfulFor:   []string{"SCREEN", "LEAD", "CORROBORATE"}},
			{Ref: "S2", Producer: "AIS network A receiver metadata",
				Timestamp: at(11, 30), Acquisition: "same feed, metadata fields",
				Rights:    "as S1",
				LawfulFor: []string{"SCREEN", "LEAD", "CORROBORATE"}},
			{Ref: "S3", Producer: "port agent, Singapore", Timestamp: at(14, 0),
				Acquisition: "written statement supplied by the charterer",
				Rights:      "supplied for this dispute; onward disclosure requires the agent's consent",
				LawfulFor:   []string{"SCREEN", "LEAD", "CORROBORATE", "FOUND"}},
			{Ref: "S4", Producer: "national meteorological service", Timestamp: at(9, 0),
				Acquisition: "open data portal", Rights: "open licence, attribution required",
				LawfulFor: []string{"SCREEN", "LEAD", "CORROBORATE", "FOUND", "DISCLOSE"}},
			{Ref: "S5", Producer: "issuing carrier", Timestamp: at(20, 0),
				Acquisition: "document disclosed in the dispute",
				Rights: "disclosed for the purposes of this dispute only; use outside it " +
					"is a breach",
				LawfulFor: []string{"CORROBORATE", "FOUND"}},
		},

		Independence: commercial.Independence{
			Accounts: len(Accounts()), Producers: 5, EffectiveObservations: eff,
			Assessed: []string{"PRODUCER", "SHARED_DEVIATION", "TEXT_SIMILARITY"},
			Unknown: []string{"COMMERCIAL_OWNERSHIP", "UPSTREAM_FEED", "FUNDING",
				"EDITORIAL_CONTROL"},
			Note: note + " Two accounts share a transposed IMO digit (9401627 for " +
				"9401267) that the majority does not carry; a transposition is not the " +
				"kind of error two people make separately. Note also what is NOT " +
				"assessed: whether these producers share an owner, a feed or a funder " +
				"is unknown, and nothing here rules it out.",
		},

		Contradictions: []commercial.Contradiction{
			{Ref: "C1", Between: []string{"O3", "O7"},
				What: "The reported draught increase of 6.3 m implies a substantial " +
					"loading during the gap. The bill of lading states the full contracted " +
					"quantity was loaded at the nominated port the previous day. Both " +
					"cannot describe the same cargo.",
				Resolved: ""},
			{Ref: "C2", Between: []string{"O2", "O4"},
				What: "The gap is presented as an absence of transmission. The receiver " +
					"identity changed across it, so the gap may be an absence of RECEPTION " +
					"by a network whose footprint does not cover that position.",
				Resolved: ""},
			{Ref: "C3", Between: []string{"O5", "O7"},
				What: "The port agent records no terminal call on 14 June; the bill of " +
					"lading is dated 13 June. These are consistent with each other and " +
					"are recorded together because a reader checking C1 will otherwise " +
					"treat the agent's note as contradicting the bill.",
				Resolved: "Not a contradiction on inspection; retained so the check is visible."},
		},

		Hypotheses: []commercial.Hypothesis{
			{Ref: "H1", Standing: "OPEN",
				Statement: "Cargo was loaded from another vessel during the gap, and the " +
					"draught change records it.",
				SupportedBy: []string{"O1", "O2", "O3"}, Against: []string{"O7"},
				Discriminator: "an image or radar return placing a second hull alongside " +
					"within the window"},
			{Ref: "H2", Standing: "OPEN",
				Statement: "The vessel was not receivable by the terrestrial network at " +
					"that position, and the draught figure was corrected from a stale " +
					"value entered days earlier.",
				SupportedBy: []string{"O4", "O7"}, Against: []string{},
				Discriminator: "the receiving network's coverage footprint for the window, " +
					"and the vessel's draught entry history"},
			{Ref: "H3", Standing: "OPEN",
				Statement: "GNSS interference in the area denied the transponder a " +
					"position solution, producing the gap.",
				SupportedBy: []string{"O2"},
				Discriminator: "whether other vessels in the same area show simultaneous " +
					"gaps -- interference is regional and does not select one hull"},
			{Ref: "H4", Standing: "WEAKENED",
				Statement: "The transponder was switched off deliberately to conceal the " +
					"vessel's position.",
				Against: []string{"O4"},
				Discriminator: "the same coverage footprint that tests H2; a gap that " +
					"coincides exactly with a coverage boundary is weak evidence of intent"},
		},

		Qualification: commercial.Qualification{
			Verified: []string{
				"The AIS reports were received as recorded, and their digests match the " +
					"evidence versions cited.",
				"The interval and separation between O1 and O2 are arithmetically as stated.",
				"The metocean record is from an open source and is reproducible.",
			},
			Unverified: []string{
				"That the reported draught values describe the vessel's actual condition. " +
					"Draught is typed by the crew.",
				"That the identifier MMSI 563117000 was carried by the same hull " +
					"throughout the window. MMSI is reassignable.",
				"That the bill of lading records the quantity actually loaded.",
			},
			Refused: []string{
				"Any statement that the vessel 'went dark'. The phrase asserts intent, " +
					"and intent is not observable from a reporting gap.",
				"Any single-cause label for the gap. Five mechanisms are consistent with " +
					"it and nothing available separates them.",
				"Onward disclosure of the raw AIS positions, which the S1 licence prohibits.",
			},
			Unknown: []string{
				"Whether any second vessel was present. No modality capable of showing " +
					"one was consulted, because none is contracted for.",
				"Whether the five reporting accounts share an upstream. Copy detection " +
					"found three clusters and cannot establish that those three are " +
					"independent of each other.",
				"The receiving network's coverage footprint for that position and window. " +
					"The feed provider holds it; the consumer does not.",
				"The vessel's draught entry history, which would settle H2 directly.",
			},
		},

		Decision: commercial.Decision{
			Conclusion: "It is not established that cargo was loaded during the gap. The " +
				"observation is consistent with a transfer and equally consistent with a " +
				"coverage gap plus a corrected draught entry, and the evidence available " +
				"does not separate them. The dispute cannot be decided on this material.",
			ConfidenceState: "INSUFFICIENT_TO_DECIDE",
			Authority:       "human:m.okafor",
			AuthorityRole:   "CASE_OWNER",
			At:              at(30, 0),
		},

		DisproofRoute: []string{
			"Obtain a satellite image or radar return covering 1.22N-1.27N, " +
				"103.97E-104.02E between 05:12Z and 11:26Z on 14 June. A second hull " +
				"alongside establishes H1; an empty sea weakens it substantially. " +
				"Expected cost: a tasking order with a commercial imagery provider; " +
				"lead time days, not hours, and the window has passed, so only archive " +
				"holdings apply.",
			"Obtain the AIS provider's terrestrial coverage footprint for that position " +
				"and window. If the position lies outside it, H2 explains the gap with " +
				"no intent and H4 collapses.",
			"Obtain the vessel's draught entry history from the transponder log. A " +
				"draught unchanged for eleven days before the gap makes the 7.1 m figure " +
				"stale and removes the case's central observation.",
			"Obtain simultaneous tracks for other vessels in the area. Simultaneous gaps " +
				"across unrelated hulls establish H3 and eliminate the intent reading " +
				"entirely.",
			"Obtain the terminal's draught survey at the nominated load port. It is " +
				"measured rather than typed, and it settles what the vessel's condition " +
				"was on 13 June.",
			"Establish whether the five reporting accounts share an upstream. If all " +
				"three surviving clusters trace to one wire report, the corroboration " +
				"in this case is one source, not five.",
		},

		Replay: commercial.Replay{
			Manifest:  "replay:case-2026-0614-sgstrait",
			Policy:    "policy:baseline@1",
			Model:     "none -- no model was used in this chain",
			Version:   "veriqo/pkg/intel/maritime@round6",
			Hash:      "recomputed at render time by veriqo-verify",
			Signature: "unsigned in this build; signing requires the HSM tenancy (G1)",
		},

		Limitations: []string{
			"The case is synthetic in every particular.",
			"No imagery, radar, RF or port-record modality was consulted, because none " +
				"is contracted for. Every hypothesis below H1 is therefore untested " +
				"rather than tested and surviving.",
			"'No contradictions found' between any pair not listed above is a statement " +
				"about what was compared, not about the world.",
			"Copy detection reduced five accounts to " + fmt.Sprintf("%d", eff) +
				" clusters. It cannot establish that those clusters are independent of " +
				"one another; an undeclared shared upstream is invisible to it.",
			"The identifier is treated as an identifier throughout. No step in this " +
				"chain establishes which hull carried it.",
			"The triage of the reporting gap enumerates " +
				fmt.Sprintf("%d", len(tri.Explanations)) +
				" mechanisms and, with no independent modality available, separates none " +
				"of them.",
		},

		AssuranceState: "INTERNALLY_ASSURED",
		ExternalStatus: "NOT_EXTERNALLY_QUALIFIED",
	}
	return p, p.Validate()
}

// Triage returns the gap triage, for callers that want the mechanism
// table without the whole passport.
func Triage() (maritime.Triage, error) {
	tr, err := Track()
	if err != nil {
		return maritime.Triage{}, err
	}
	gaps := tr.Gaps(maritime.DefaultGapPolicy())
	if len(gaps) == 0 {
		return maritime.Triage{}, fmt.Errorf("flagship: no gap in the constructed track")
	}
	return maritime.Explain(gaps[0]), nil
}

// CopyAnalysis returns the copy detection over the case's accounts.
func CopyAnalysis() (*independence.Analysis, error) {
	return independence.Detect(independence.DefaultPolicy(), Accounts()...)
}
