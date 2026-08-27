// Causation Output — blueprint §17. This file contains the ONLY code
// path in this package that produces natural-language causation text,
// and it is built so that the blueprint's forbidden output —
//
//	"Carrier caused the damage."
//
// — is structurally impossible to emit. Narrative (below) is always
// assembled from one of three fixed hedge templates in this file; there
// is no code path where a caller-supplied string becomes Narrative
// directly, and every template names a leading hypothesis only
// RELATIVE to its alternatives and always states why causation remains
// unresolved — reproducing the blueprint's own worked example:
//
//	"Evidence currently supports Hypothesis H3 more strongly than
//	alternative hypotheses; however, causation remains unresolved
//	because the custody record between discharge and warehouse
//	receipt is incomplete."
package causation

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/insurance/evidence"
)

// Explanation is Explain's structured output. LeadingHypothesis is
// empty ("") whenever no single hypothesis can be named as the
// relative front-runner (either nothing is supported yet, or two or
// more hypotheses are tied) — Contenders always holds every hypothesis
// currently sharing the best NetSupport score, so a caller can tell the
// difference between "one clear leader" (len(Contenders) == 1) and "no
// clear leader" (len(Contenders) != 1 or LeadingHypothesis == "").
type Explanation struct {
	CaseID   string `json:"case_id"`
	Question string `json:"question"`

	// LeadingHypothesis is the single best-supported hypothesis, or ""
	// when there is no single leader (see Contenders).
	LeadingHypothesis HypothesisID `json:"leading_hypothesis,omitempty"`
	// Contenders holds every hypothesis tied for the best NetSupport
	// score. len(Contenders) == 1 iff LeadingHypothesis != "".
	Contenders []HypothesisID `json:"contenders,omitempty"`

	// Caveats is the explicit, itemised list of reasons causation
	// remains unresolved — every MissingEvidence entry and every open
	// contradiction relevant to the leading hypothesis (or, when tied,
	// to the tied contenders) appears here. Caveats is never empty:
	// Explain always has at least one reason causation is not final,
	// because VICE never issues a final causation determination
	// (blueprint §0).
	Caveats []string `json:"caveats"`

	// Narrative is the rendered, hedged sentence — built ONLY from the
	// fixed templates in this file. See the file doc comment.
	Narrative string `json:"narrative"`
}

// ErrEmptyHypothesisSet is returned by Explain when hs has no
// hypotheses to reason about at all.
var ErrEmptyHypothesisSet = errors.New("causation: cannot explain a HypothesisSet with no hypotheses")

// Explain produces the blueprint's §17 causation output for hs. dg is
// optional (nil is fine) and, when supplied, makes the underlying
// NetSupport ranking independence-adjusted, so that dependent evidence
// shared between two hypotheses' supporting lists is never
// double-counted when deciding which hypothesis leads (golden rule,
// §36).
//
// Explain does not mutate hs and does not itself change any
// Hypothesis's Status — it only reads the Status/evidence already on
// record (call HypothesisSet.RecomputeStatuses first if evidence has
// changed since the last recompute).
func Explain(hs *HypothesisSet, dg *evidence.DependencyGraph) (Explanation, error) {
	if hs == nil {
		return Explanation{}, ErrEmptyHypothesisSet
	}
	all := hs.All()
	if len(all) == 0 {
		return Explanation{}, ErrEmptyHypothesisSet
	}

	if !anySupported(all) {
		return noSupportExplanation(hs, all), nil
	}

	type scored struct {
		h   Hypothesis
		net int
	}
	scoredList := make([]scored, len(all))
	for i, h := range all {
		scoredList[i] = scored{h: h, net: NetSupport(h, dg)}
	}
	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].net != scoredList[j].net {
			return scoredList[i].net > scoredList[j].net
		}
		return scoredList[i].h.ID < scoredList[j].h.ID // deterministic tiebreak for iteration only
	})

	topNet := scoredList[0].net
	var contenders []HypothesisID
	for _, s := range scoredList {
		if s.net == topNet {
			contenders = append(contenders, s.h.ID)
		}
	}
	contenders = SortedIDs(contenders)

	if len(contenders) > 1 {
		return tiedExplanation(hs, contenders), nil
	}
	return leadingExplanation(hs, scoredList[0].h), nil
}

// anySupported reports whether any hypothesis in all has at least one
// supporting-evidence entry recorded.
func anySupported(all []Hypothesis) bool {
	for _, h := range all {
		if len(h.SupportingEvidence) > 0 {
			return true
		}
	}
	return false
}

// leadingExplanation builds the Explanation for the single-leader case
// — the direct realisation of the blueprint's §17 worked example.
func leadingExplanation(hs *HypothesisSet, h Hypothesis) Explanation {
	var caveats []string
	var reason string

	switch {
	case len(h.MissingEvidence) > 0:
		// This is the blueprint's own §17 shape: the reason causation
		// remains unresolved IS the specific missing-evidence
		// description, verbatim.
		reason = h.MissingEvidence[0]
		for _, m := range h.MissingEvidence {
			caveats = append(caveats, fmt.Sprintf("%s: %s", h.ID, m))
		}
	case len(h.ContradictingEvidence) > 0:
		reason = fmt.Sprintf("%d item(s) of evidence still contradict Hypothesis %s", len(h.ContradictingEvidence), h.ID)
		caveats = append(caveats, fmt.Sprintf("%s is contradicted by: %s", h.ID, strings.Join(h.ContradictingEvidence, ", ")))
	default:
		// Even a hypothesis with clean SUPPORTED status (no missing
		// evidence, no contradicting evidence on record) is never
		// reported as a final causation determination — blueprint §0:
		// VICE MUST NOT determine legal liability finally, and the
		// golden rule (§36): never convert uncertainty into certainty.
		reason = fmt.Sprintf("VICE does not issue a final causation determination for Hypothesis %s without independent human review", h.ID)
		caveats = append(caveats, "no final causation determination is issued by VICE; human review is required before this finding is treated as conclusive")
	}

	if h.Status != StatusSupported {
		caveats = append(caveats, fmt.Sprintf("%s status is %s, not a confirmed conclusion", h.ID, h.Status))
	}

	narrative := fmt.Sprintf(
		"Evidence currently supports Hypothesis %s more strongly than alternative hypotheses; however, causation remains unresolved because %s.",
		h.ID, reason,
	)

	return Explanation{
		CaseID: hs.CaseID, Question: hs.Question,
		LeadingHypothesis: h.ID,
		Contenders:        []HypothesisID{h.ID},
		Caveats:           caveats,
		Narrative:         narrative,
	}
}

// tiedExplanation builds the Explanation when two or more hypotheses
// are tied for the best NetSupport score — the blueprint's own §35
// adversarial case #9 ("Conflicting survey reports") lands here: when
// evidence for two hypotheses is itself in conflict, Explain must NOT
// pick a false single winner.
func tiedExplanation(hs *HypothesisSet, contenders []HypothesisID) Explanation {
	names := make([]string, len(contenders))
	for i, id := range contenders {
		names[i] = string(id)
	}
	joined := strings.Join(names, ", ")

	caveats := []string{
		fmt.Sprintf("evidence does not currently favor one hypothesis over another among: %s", joined),
	}
	for _, id := range contenders {
		h, ok := hs.Get(id)
		if !ok {
			continue
		}
		if len(h.MissingEvidence) > 0 {
			caveats = append(caveats, fmt.Sprintf("%s: %s", id, strings.Join(h.MissingEvidence, "; ")))
		}
		if len(h.ContradictingEvidence) > 0 {
			caveats = append(caveats, fmt.Sprintf("%s is contradicted by: %s", id, strings.Join(h.ContradictingEvidence, ", ")))
		}
	}

	narrative := fmt.Sprintf(
		"Evidence does not currently favor one hypothesis over the others; Hypotheses %s remain comparably supported and causation remains unresolved.",
		joined,
	)

	return Explanation{
		CaseID: hs.CaseID, Question: hs.Question,
		LeadingHypothesis: "", // no single leader — see Contenders
		Contenders:        contenders,
		Caveats:           caveats,
		Narrative:         narrative,
	}
}

// noSupportExplanation builds the Explanation when NO hypothesis in the
// set has any supporting evidence recorded at all — never phrased as a
// negative causation finding (that would be its own unsupported
// conclusion), only as "unresolved".
func noSupportExplanation(hs *HypothesisSet, all []Hypothesis) Explanation {
	var caveats []string
	for _, h := range all {
		if len(h.MissingEvidence) > 0 {
			caveats = append(caveats, fmt.Sprintf("%s: %s", h.ID, strings.Join(h.MissingEvidence, "; ")))
		}
	}
	if len(caveats) == 0 {
		caveats = append(caveats, "no supporting or contradicting evidence has been recorded against any hypothesis in this set")
	}

	return Explanation{
		CaseID: hs.CaseID, Question: hs.Question,
		LeadingHypothesis: "",
		Caveats:           caveats,
		Narrative:         "No hypothesis in this causal question is currently supported by evidence on record; causation remains unresolved.",
	}
}
