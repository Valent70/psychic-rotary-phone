// Ugly-world handling: what the detectors must do when the data is
// real rather than constructed.
//
// # The failure this file prevents
//
// A detector finds a 40-knot jump between two AIS reports and the
// obvious label is SPOOFING. It is also the wrong label, because the
// same observation is produced by GNSS jamming, by a failing GPS
// receiver, by a relay that stamped a message with its own arrival
// time, and by a message that simply arrived late and out of order.
//
// Every one of those is common. GNSS interference in particular is now
// routine in several sea areas -- reporting from AIS aggregators and
// maritime authorities through 2025 described large and rising numbers
// of vessels affected by jamming near specific coastlines, though I
// have not independently verified those counts and would treat the
// specific figures as needing a primary source.
//
// The point does not depend on the figures. It is that a system which
// prints SPOOFING on an observation consistent with four mundane
// causes will be confidently wrong in public, on a data partner's own
// data, in front of the people who know that sea area best.
//
// # The rule
//
// A single AIS feed cannot distinguish these causes. No amount of
// cleverness applied to one modality separates a spoofed position from
// a jammed one, because in both cases the receiver reports a position
// it believes. Separating them requires an INDEPENDENT MODALITY --
// satellite imagery, terrestrial RF, a port record, a crew statement --
// covering the same window.
//
// So Explain() returns the competing causes and, for each, the
// modality that would discriminate it. It does not return a most
// likely cause, and there is deliberately no function that does.
package maritime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrSingleCause is naming one cause where several are consistent.
var ErrSingleCause = errors.New("maritime: one cause named where several are consistent")

// Cause is a mechanism that produces an anomalous observation.
type Cause string

const (
	// Spoofing: a transmitter deliberately reporting a false position
	// or identity.
	Spoofing Cause = "GNSS_OR_AIS_SPOOFING"
	// Jamming: GNSS denial. The receiver's own position solution is
	// wrong and the vessel may not know.
	Jamming Cause = "GNSS_INTERFERENCE"
	// EquipmentFault: a failing receiver, antenna or transponder.
	EquipmentFault Cause = "EQUIPMENT_FAULT"
	// RelayArtefact: the message is fine and the pipeline damaged it --
	// a gateway stamping arrival time as position time, a duplicated
	// or reordered feed.
	RelayArtefact Cause = "RELAY_OR_INGEST_ARTEFACT"
	// LateOrOutOfOrder: the message is correct and arrived out of
	// sequence, which fabricates an apparent jump.
	LateOrOutOfOrder Cause = "LATE_OR_OUT_OF_ORDER_REPORT"
	// ClockError: the reporting clock is wrong, so the observation is
	// real and its time is not.
	ClockError Cause = "CLOCK_ERROR"
	// GenuineMovement: the vessel did the thing. Always on the list,
	// because a detector whose explanation set omits "it happened" has
	// assumed its own conclusion.
	GenuineMovement Cause = "GENUINE_MOVEMENT"
)

// Modality is a source of evidence independent of AIS.
type Modality string

const (
	SatelliteImagery Modality = "SATELLITE_IMAGERY"
	TerrestrialRF    Modality = "TERRESTRIAL_RF_OR_RADAR"
	PortRecord       Modality = "PORT_OR_TERMINAL_RECORD"
	ReceiverLog      Modality = "RECEIVER_OR_TRANSPONDER_LOG"
	FeedAudit        Modality = "FEED_INGEST_AUDIT"
	CrewOrOperator   Modality = "CREW_OR_OPERATOR_STATEMENT"
	// NoneAvailable is the honest answer when nothing separates the
	// causes. It is a value rather than an empty field so that "we
	// have no discriminator" is a recorded finding.
	NoneAvailable Modality = "NONE_AVAILABLE"
)

// Explanation is one candidate cause with what would test it.
type Explanation struct {
	Cause Cause `json:"cause"`
	// Mechanism says how this cause produces the observation seen.
	Mechanism string `json:"mechanism"`
	// Discriminator is the evidence that would separate this cause
	// from the others. It names a modality because a discriminator
	// within AIS is not one.
	Discriminator string `json:"discriminator"`
	// Needs is the modality the discriminator requires.
	Needs Modality `json:"needs"`
	// Mundane marks explanations that require nobody to have done
	// anything wrong. Ordering by this rather than by likelihood keeps
	// the boring causes in front of the analyst, where they belong.
	Mundane bool `json:"mundane"`
}

// Triage is the set of causes consistent with an observation.
//
// It has no Verdict field and no MostLikely method. That is not an
// omission: the whole value of this type is that it cannot be reduced
// to a label by a caller in a hurry.
type Triage struct {
	Anomaly      Anomaly       `json:"anomaly"`
	Explanations []Explanation `json:"explanations"`
	// Available are modalities the caller says they can obtain. It is
	// supplied by the caller because VERIQO cannot know what a
	// customer has contracted for.
	Available []Modality `json:"available,omitempty"`
}

// Explain returns the causes consistent with an anomaly.
//
// The set depends on the anomaly kind, because a reporting gap and an
// impossible speed do not have the same mundane explanations.
func Explain(a Anomaly, available ...Modality) Triage {
	var ex []Explanation
	switch {
	case strings.Contains(a.Kind, "SPEED"), strings.Contains(a.Kind, "COLLISION"):
		ex = positionJumpCauses()
	case strings.Contains(a.Kind, "GAP"):
		ex = gapCauses()
	case strings.Contains(a.Kind, "NULL"):
		ex = nullPositionCauses()
	case strings.Contains(a.Kind, "DRAUGHT"):
		ex = draughtCauses()
	default:
		ex = positionJumpCauses()
	}
	sort.SliceStable(ex, func(i, j int) bool {
		if ex[i].Mundane != ex[j].Mundane {
			return ex[i].Mundane // mundane first, deliberately
		}
		return ex[i].Cause < ex[j].Cause
	})
	return Triage{Anomaly: a, Explanations: ex, Available: available}
}

func positionJumpCauses() []Explanation {
	return []Explanation{
		{Cause: LateOrOutOfOrder, Mundane: true,
			Mechanism: "a report arriving after a later one makes the track double back, " +
				"producing an implied speed no hull could reach",
			Discriminator: "the ingest log's receive timestamps against the reported " +
				"position timestamps",
			Needs: FeedAudit},
		{Cause: RelayArtefact, Mundane: true,
			Mechanism: "a gateway stamped its own arrival time onto the position, so the " +
				"position is right and the time is not",
			Discriminator: "the same message as received by a second aggregator",
			Needs:         FeedAudit},
		{Cause: ClockError, Mundane: true,
			Mechanism: "the reporting clock is offset, so a correct position carries a " +
				"wrong time and the interval between two reports is fictional",
			Discriminator: "the offset between this transponder's timestamps and the " +
				"receiving station's, over a long window",
			Needs: ReceiverLog},
		{Cause: EquipmentFault, Mundane: true,
			Mechanism: "a failing receiver or antenna produces intermittent wrong " +
				"position solutions without anybody intending it",
			Discriminator: "the receiver's own fault log and satellite-count record " +
				"for the window",
			Needs: ReceiverLog},
		{Cause: Jamming, Mundane: false,
			Mechanism: "GNSS denial in the area: the vessel's receiver reports a wrong " +
				"position it believes to be correct, and so do its neighbours",
			Discriminator: "whether OTHER vessels in the same area show the same " +
				"displacement in the same window -- jamming is regional, spoofing of " +
				"one vessel is not",
			Needs: TerrestrialRF},
		{Cause: Spoofing, Mundane: false,
			Mechanism: "a transmitter deliberately reporting a false position, or " +
				"another vessel's identity",
			Discriminator: "an image or radar return placing the hull somewhere the " +
				"AIS track says it is not, at a known time",
			Needs: SatelliteImagery},
		{Cause: GenuineMovement, Mundane: true,
			Mechanism: "the vessel moved as reported and the limit applied to it is " +
				"wrong for this hull class",
			Discriminator: "the vessel's registered class and design speed",
			Needs:         PortRecord},
	}
}

func gapCauses() []Explanation {
	return []Explanation{
		{Cause: RelayArtefact, Mundane: true,
			Mechanism: "the vessel transmitted and no receiver in range recorded it; " +
				"terrestrial AIS coverage is not global and satellite revisit is not " +
				"continuous",
			Discriminator: "the coverage footprint of the receiving network over the " +
				"window, which the feed provider holds and the consumer usually does not",
			Needs: FeedAudit},
		{Cause: EquipmentFault, Mundane: true,
			Mechanism: "the transponder failed or was powered down for maintenance",
			Discriminator: "the vessel's own engineering log and the transponder's " +
				"power history",
			Needs: ReceiverLog},
		{Cause: Jamming, Mundane: false,
			Mechanism:     "GNSS denial left the transponder without a position to report",
			Discriminator: "whether other vessels in the area show simultaneous gaps",
			Needs:         TerrestrialRF},
		{Cause: Spoofing, Mundane: false,
			Mechanism: "the transponder was switched off to conceal an activity, or the " +
				"vessel transmitted under another identity during the window",
			Discriminator: "an image at a point inside the gap, or a port record placing " +
				"the vessel somewhere its track does not go",
			Needs: SatelliteImagery},
		{Cause: GenuineMovement, Mundane: true,
			Mechanism: "the vessel was alongside or at anchor and reporting at the low " +
				"rate that the standard specifies for a stationary vessel",
			Discriminator: "the port's berth record for the window",
			Needs:         PortRecord},
	}
}

func nullPositionCauses() []Explanation {
	return []Explanation{
		{Cause: EquipmentFault, Mundane: true,
			Mechanism: "no position fix, so the transponder transmits the default " +
				"sentinel rather than a position",
			Discriminator: "the receiver's satellite-count record at the time",
			Needs:         ReceiverLog},
		{Cause: RelayArtefact, Mundane: true,
			Mechanism: "a parser wrote a zero where the field was absent, so the null " +
				"is the pipeline's and not the vessel's",
			Discriminator: "the raw sentence as received, before parsing",
			Needs:         FeedAudit},
		{Cause: Jamming, Mundane: false,
			Mechanism:     "GNSS denial removed the fix entirely",
			Discriminator: "simultaneous loss of fix across vessels in the area",
			Needs:         TerrestrialRF},
		{Cause: Spoofing, Mundane: false,
			Mechanism:     "a deliberately malformed transmission",
			Discriminator: "an image or radar return for the window",
			Needs:         SatelliteImagery},
	}
}

func draughtCauses() []Explanation {
	return []Explanation{
		{Cause: RelayArtefact, Mundane: true,
			Mechanism: "draught is entered by the crew, not measured by the " +
				"transponder, so a change may be a correction of a stale value rather " +
				"than a change in the world",
			Discriminator: "the vessel's own loading record for the window",
			Needs:         PortRecord},
		{Cause: EquipmentFault, Mundane: true,
			Mechanism: "a data-entry error in either the earlier or the later value",
			Discriminator: "the terminal's draught survey, which is measured rather " +
				"than typed",
			Needs: PortRecord},
		{Cause: GenuineMovement, Mundane: true,
			Mechanism: "the vessel loaded or discharged, which is what a draught change " +
				"normally means and is not by itself evidence of anything irregular",
			Discriminator: "the terminal's berth and crane records for the window",
			Needs:         PortRecord},
		{Cause: Spoofing, Mundane: false,
			Mechanism:     "the reported draught was falsified to disguise a transfer",
			Discriminator: "an independent draught survey or a cargo document",
			Needs:         PortRecord},
	}
}

// Distinguishable reports whether the caller's available modalities
// can separate the causes at all.
//
// When they cannot, the honest output is the set with a note that
// nothing on hand separates them -- not the most plausible member of
// the set.
func (t Triage) Distinguishable() bool {
	have := map[Modality]bool{}
	for _, m := range t.Available {
		have[m] = true
	}
	for _, e := range t.Explanations {
		if !have[e.Needs] {
			return false
		}
	}
	return len(t.Explanations) > 0
}

// Undiscriminated returns the causes the caller currently cannot rule
// in or out, with what each would need.
func (t Triage) Undiscriminated() []Explanation {
	have := map[Modality]bool{}
	for _, m := range t.Available {
		have[m] = true
	}
	var out []Explanation
	for _, e := range t.Explanations {
		if !have[e.Needs] {
			out = append(out, e)
		}
	}
	return out
}

// Needs returns the modalities that would settle the question, in a
// fixed order, so the answer can be turned into a purchase.
func (t Triage) Needs() []Modality {
	seen := map[Modality]bool{}
	var out []Modality
	for _, e := range t.Undiscriminated() {
		if !seen[e.Needs] {
			seen[e.Needs] = true
			out = append(out, e.Needs)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Statement is what may be said about the anomaly, in one sentence
// that a lawyer could read aloud.
//
// It never names a cause when several remain. This is the sentence
// that stops "AIS gap" becoming "the vessel went dark to conceal a
// transfer" somewhere between the detector and the slide deck.
func (t Triage) Statement() string {
	n := len(t.Undiscriminated())
	switch {
	case len(t.Explanations) == 0:
		return "No mechanism was enumerated for this observation, so nothing may be " +
			"concluded from it."
	case n == 0:
		return fmt.Sprintf("Every enumerated cause for this %s can be tested with the "+
			"evidence available; the observation is decidable and has not yet been decided.",
			t.Anomaly.Kind)
	case n == len(t.Explanations):
		return fmt.Sprintf("This %s is consistent with %d distinct causes and nothing "+
			"available separates them. It is an observation, not a finding, and may not "+
			"be reported as one.", t.Anomaly.Kind, n)
	}
	return fmt.Sprintf("This %s is consistent with %d causes that the evidence on hand "+
		"cannot separate. Naming any one of them would be a choice, not a conclusion.",
		t.Anomaly.Kind, n)
}

// Report renders the triage.
func (t Triage) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "OBSERVATION  %s [%s]\n", t.Anomaly.Kind, t.Anomaly.VesselID)
	fmt.Fprintf(&b, "             %s\n", t.Anomaly.Observation)
	fmt.Fprintf(&b, "             %s to %s\n",
		t.Anomaly.From.UTC().Format(time.RFC3339), t.Anomaly.To.UTC().Format(time.RFC3339))
	b.WriteString("\n  CONSISTENT CAUSES -- mundane first, and none is selected\n")
	for _, e := range t.Explanations {
		tag := "  "
		if !e.Mundane {
			tag = "! "
		}
		fmt.Fprintf(&b, "  %s%s\n", tag, e.Cause)
		b.WriteString(wrapM("      how:   ", e.Mechanism))
		b.WriteString(wrapM("      test:  ", e.Discriminator))
		fmt.Fprintf(&b, "      needs: %s\n", e.Needs)
	}
	if need := t.Needs(); len(need) > 0 {
		b.WriteString("\n  NOT OBTAINABLE FROM AIS. To decide this, acquire:\n")
		for _, m := range need {
			fmt.Fprintf(&b, "    %s\n", m)
		}
	}
	b.WriteString("\n")
	b.WriteString(wrapM("  ", t.Statement()))
	return b.String()
}

func wrapM(label, text string) string {
	const width = 78
	indent := strings.Repeat(" ", len(label))
	var b strings.Builder
	line := label
	for i, word := range strings.Fields(text) {
		if i > 0 && len(line)+1+len(word) > width {
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			line = indent
		} else if i > 0 {
			line += " "
		}
		line += word
	}
	b.WriteString(strings.TrimRight(line, " ") + "\n")
	return b.String()
}
