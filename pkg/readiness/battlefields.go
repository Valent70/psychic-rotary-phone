package readiness

import (
	"fmt"
	"sort"
	"strings"
)

// Battlefield is a place where VERIQO stops being tested against
// itself.
//
// The five below are not extra work alongside the procurement graph;
// four of them ARE blockers on it, restated in the form that says what
// winning looks like. A blocker says what must be bought. A
// battlefield says what would have to happen for VERIQO to have been
// wrong, and what result would count as a pass.
//
// The distinction matters because a procurement item can be discharged
// by receiving a document. A battlefield cannot: the fourth one below
// is passed only if professionals who were not told VERIQO's
// conclusion can follow the reasoning to their own.
type Battlefield struct {
	// N is the audit's numbering, kept so the two documents can be
	// read side by side.
	N    int    `json:"n"`
	Name string `json:"name"`
	// Question is what the battlefield actually tests, phrased so it
	// can come out badly.
	Question string `json:"question"`
	// Blockers are the procurement items that must clear first.
	Blockers []string `json:"blockers,omitempty"`
	// Measures are what is counted, with the denominator named.
	Measures []string `json:"measures"`
	// Pass states the result that would count as passing. Stating it
	// in advance is what stops a disappointing result being
	// reinterpreted afterwards as a partial success.
	Pass string `json:"pass"`
	// Fail states the result that would count as failing, in the same
	// breath, because a pass condition with no matching fail condition
	// is a target rather than a test.
	Fail string `json:"fail"`
	// WhyNotYet says what stops it starting today.
	WhyNotYet string `json:"why_not_yet"`
}

// Battlefields returns the five, in the audit's order.
func Battlefields() []Battlefield {
	return []Battlefield{
		{N: 1, Name: "Real maritime data",
			Question: "Does VERIQO stay honest when the feed is not one VERIQO built?",
			Blockers: []string{"B-AIS"},
			Measures: []string{
				"positions ingested / positions offered, per feed",
				"reports refused / reports ingested, with the reason class for each refusal",
				"anomalies raised / vessel-days observed",
				"anomalies whose triage needs a modality the contract does not include / \n\t\t\t\t\tanomalies raised",
			},
			Pass: "the refusal rate is explainable line by line, and every anomaly " +
				"raised carries a triage naming what would settle it",
			Fail: "anomalies are raised that the system cannot explain the basis of, or " +
				"the refusal rate is high and nobody can say why",
			WhyNotYet: "no data contract exists; AIS, registry, metocean and port-event " +
				"feeds each need usage rights that permit founding a decision, not " +
				"merely screening",
		},
		{N: 2, Name: "Real documents",
			Question: "Does the redaction and extraction machinery hold on documents " +
				"nobody constructed for it?",
			Blockers: []string{"B-CORPUS"},
			Measures: []string{
				"accepted / submitted",
				"refused / submitted, by refusal class",
				"failed (crashed or hung) / submitted -- distinct from refused",
				"leakage: documents where a term survived verification, / accepted",
				"false refusal: documents refused that a human judges processable, / refused",
				"processing time, p50 and p99",
			},
			Pass: "leakage is zero on a corpus of at least several hundred documents " +
				"with real variation, and every refusal class is one a customer would " +
				"accept as reasonable",
			Fail: "any leakage at all, or a false-refusal rate that makes the tool " +
				"unusable on the customer's own filing",
			WhyNotYet: "there are 23 internal fixtures. They are not a sample of " +
				"anything, and the population they would be a sample of is undefined " +
				"until a partner defines it",
		},
		{N: 3, Name: "Independent attack",
			Question: "Do the boundaries hold against somebody who did not write them?",
			Blockers: []string{"B-PENTEST", "B-REDTEAM"},
			Measures: []string{
				"findings by severity / findings reported",
				"findings / surface, for each of API, tenant, authority, ledger, " +
					"redaction and AI-boundary",
				"elapsed hours to first finding (a duration, with no denominator, " +
					"and named as such)",
				"findings VERIQO's own adversarial suite had already covered / total",
			},
			Pass: "no critical finding, and the last measure is LOW -- a suite that had " +
				"already covered everything the team found means the team did not look " +
				"where VERIQO could not see",
			Fail: "a critical finding, or a high overlap with the internal suite, which " +
				"indicates the engagement reproduced VERIQO's own assumptions",
			WhyNotYet: "no firm is engaged, and the code should be frozen at a release " +
				"candidate first -- a test of code that then changes assesses nothing",
		},
		{N: 4, Name: "Real dispute case, read blind",
			Question: "Can a maritime professional follow the reasoning to their own " +
				"conclusion without being told VERIQO's?",
			Measures: []string{
				"readers who reach a conclusion / readers given the passport",
				"readers whose conclusion matches VERIQO's / readers who reach one",
				"readers who identify the same decisive uncertainty / readers",
				"reasoning steps a reader could not follow, per reader",
			},
			Pass: "readers reach their own conclusion and identify the same decisive " +
				"uncertainty VERIQO did. Agreement with VERIQO's conclusion is NOT the " +
				"pass condition and must not be reported as one",
			Fail: "readers cannot follow the chain, or they agree with VERIQO for " +
				"reasons the passport does not contain -- which means the document " +
				"persuaded rather than explained",
			WhyNotYet: "it needs a real or legally shareable historical case, and four " +
				"professionals -- a lawyer, a surveyor, an adjuster, a maritime expert " +
				"-- who have not been told the answer. None of that is a purchase VERIQO " +
				"has made",
		},
		{N: 5, Name: "Operational endurance",
			Question: "Does the system stay correct when the infrastructure misbehaves?",
			Blockers: []string{"B-INFRA", "B-SOAK"},
			Measures: []string{
				"consecutive hours survived at each stage -- 24h, 72h, 7d, 30d (a " +
					"duration, with no denominator, and named as such)",
				"ledger divergences / restarts",
				"replay divergences after restore / replays attempted",
				"duplicate messages accepted twice / duplicates injected",
				"decisions a re-run does not reproduce / decisions taken under clock drift",
			},
			Pass: "thirty days with node failure, network partition, credential " +
				"rotation, restore, snapshot, replay, duplicate injection and clock " +
				"drift, and zero ledger or replay divergence",
			Fail: "any divergence that a re-run does not reproduce identically, at any " +
				"stage. A divergence that appears once and not again is worse than one " +
				"that appears every time",
			WhyNotYet: "the longest run in this repository is seconds, in one process, " +
				"on one host. There is no deployment to endure anything",
		},
	}
}

// BattlefieldReport renders the five.
func BattlefieldReport() string {
	bs := Battlefields()
	var b strings.Builder
	b.WriteString("THE FIVE BATTLEFIELDS\n")
	b.WriteString("  where VERIQO stops being tested against itself.\n")
	b.WriteString("  Each states its pass AND its fail condition, in advance.\n\n")
	blocked := map[string]bool{}
	for _, f := range bs {
		fmt.Fprintf(&b, "  %d. %s\n", f.N, f.Name)
		b.WriteString(wrapB("     ", f.Question))
		if len(f.Blockers) > 0 {
			sort.Strings(f.Blockers)
			fmt.Fprintf(&b, "     needs first: %s\n", strings.Join(f.Blockers, ", "))
			for _, x := range f.Blockers {
				blocked[x] = true
			}
		}
		b.WriteString("     measures:\n")
		for _, m := range f.Measures {
			b.WriteString(wrapB("       - ", m))
		}
		b.WriteString(wrapB("     PASS: ", f.Pass))
		b.WriteString(wrapB("     FAIL: ", f.Fail))
		b.WriteString(wrapB("     not yet: ", f.WhyNotYet))
		b.WriteString("\n")
	}
	names := make([]string, 0, len(blocked))
	for k := range blocked {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "  %d battlefields; %d procurement blocker(s) stand in front of them:\n",
		len(bs), len(names))
	fmt.Fprintf(&b, "  %s\n\n", strings.Join(names, ", "))
	b.WriteString(wrapB("  ", "Battlefield 4 is the one that cannot be discharged by "+
		"receiving a document. Its pass condition is deliberately NOT agreement with "+
		"VERIQO: a reader who agrees for reasons the passport does not contain has been "+
		"persuaded, which is the failure the passport format exists to prevent."))
	return b.String()
}

func wrapB(label, text string) string {
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
