package constitution

import (
	"fmt"
	"sort"
	"strings"
)

// This file holds one check per article. Each follows the same shape:
// if the Subject does not carry the facts the article needs, report
// NotEvaluable with the reason; otherwise return a determinate
// verdict. No check ever reports Satisfied on absent facts.

// Article 1 -- No Naked Facts.
func checkArticle1(s Subject) Result {
	a := mustArticle(1)
	if s.Finding == nil {
		return res(a, NotEvaluable, "no finding facts supplied")
	}
	f := s.Finding
	if len(f.EvidenceRefs) == 0 {
		return res(a, Violated, fmt.Sprintf("finding %q cites no evidence", f.ID))
	}
	var naked []string
	for _, ref := range f.EvidenceRefs {
		if !f.LineageEstablished[ref] {
			naked = append(naked, ref)
		}
	}
	if len(naked) > 0 {
		sort.Strings(naked)
		return res(a, Violated, fmt.Sprintf("finding %q cites evidence with no established lineage: %s",
			f.ID, strings.Join(naked, ", ")))
	}
	return res(a, Satisfied, fmt.Sprintf("finding %q cites %d evidence refs, all with established lineage",
		f.ID, len(f.EvidenceRefs)))
}

// Article 2 -- No Truth by Acquisition. Bounded: the system carries no
// "truth" field to conflate with acquisition, so the article holds by
// construction. Reported explicitly rather than omitted.
func checkArticle2(s Subject) Result {
	a := mustArticle(2)
	if s.Acquisition == nil {
		return res(a, NotEvaluable, "no acquisition facts supplied")
	}
	return res(a, Satisfied,
		"acquisition records establish acquisition only; no truth attribute is derivable from acquisition alone")
}

// Article 3 -- No Corroboration by Duplication.
func checkArticle3(s Subject) Result {
	a := mustArticle(3)
	if s.Corroboration == nil {
		return res(a, NotEvaluable, "no corroboration facts supplied")
	}
	c := s.Corroboration
	if !c.ClaimedIndependent {
		return res(a, Satisfied, "no independent-corroboration claim was made")
	}
	if len(c.SourceRoots) < 2 {
		return res(a, NotEvaluable,
			"independent corroboration claimed but fewer than two sources supplied to compare")
	}
	// Group sources by root: two sources sharing a root are one source.
	byRoot := map[string][]string{}
	for src, root := range c.SourceRoots {
		byRoot[root] = append(byRoot[root], src)
	}
	for root, srcs := range byRoot {
		if len(srcs) > 1 {
			sort.Strings(srcs)
			return res(a, Violated, fmt.Sprintf(
				"independent corroboration claimed, but sources %s share root origin %q -- same-root data is one source",
				strings.Join(srcs, ", "), root))
		}
	}
	return res(a, Satisfied, fmt.Sprintf("%d sources, %d distinct roots", len(c.SourceRoots), len(byRoot)))
}

// Article 4 -- No Authorization, No Contact.
func checkArticle4(s Subject) Result {
	a := mustArticle(4)
	if s.Acquisition == nil {
		return res(a, NotEvaluable, "no acquisition facts supplied")
	}
	q := s.Acquisition
	if !q.RightsChecked {
		if q.ContactMade {
			return res(a, Violated, fmt.Sprintf(
				"contact was made with source %q without any rights check", q.SourceID))
		}
		return res(a, NotEvaluable, "rights were never checked and no contact occurred; nothing to judge")
	}
	if q.ContactMade && !q.RightsGranted {
		return res(a, Violated, fmt.Sprintf(
			"rights were denied for source %q yet contact was made", q.SourceID))
	}
	if !q.RightsGranted {
		return res(a, Satisfied, fmt.Sprintf("rights denied for source %q and no contact was made", q.SourceID))
	}
	return res(a, Satisfied, fmt.Sprintf("rights granted for source %q before contact", q.SourceID))
}

// Article 5 -- Raw Before Transform.
func checkArticle5(s Subject) Result {
	a := mustArticle(5)
	if s.Acquisition == nil {
		return res(a, NotEvaluable, "no acquisition facts supplied")
	}
	q := s.Acquisition
	if q.Transformed && !q.RawPreserved {
		return res(a, Violated,
			"a transformation ran without the raw artifact preserved; the parsed form would outlive its original")
	}
	if !q.Transformed {
		return res(a, Satisfied, "no transformation ran")
	}
	return res(a, Satisfied, "raw artifact preserved before transformation")
}

// Article 6 -- Immutable After Finalization.
func checkArticle6(s Subject) Result {
	a := mustArticle(6)
	if s.Version == nil {
		return res(a, NotEvaluable, "no version facts supplied")
	}
	v := s.Version
	if !v.Finalized {
		return res(a, Satisfied, "version is not finalized; immutability does not yet bind")
	}
	if v.MutatedAfterFinalization {
		return res(a, Violated, "a finalized evidence version was mutated in place")
	}
	return res(a, Satisfied, "finalized version was not mutated")
}

// Article 7 -- Historical Policy Pinning.
func checkArticle7(s Subject) Result {
	a := mustArticle(7)
	if s.Policy == nil {
		return res(a, NotEvaluable, "no policy facts supplied")
	}
	p := s.Policy
	if p.CasePolicyVersion == "" || p.EvaluatedPolicyVersion == "" {
		return res(a, NotEvaluable, "policy versions not both supplied")
	}
	if p.CasePolicyVersion != p.EvaluatedPolicyVersion {
		return res(a, Violated, fmt.Sprintf(
			"case ran under policy %q but was evaluated under %q", p.CasePolicyVersion, p.EvaluatedPolicyVersion))
	}
	return res(a, Satisfied, fmt.Sprintf("evaluated under the case's own policy version %q", p.CasePolicyVersion))
}

// Article 8 -- AI Has No Evidence Authority.
func checkArticle8(s Subject) Result {
	a := mustArticle(8)
	if s.AI == nil {
		return res(a, NotEvaluable, "no AI facts supplied")
	}
	forbidden := map[string]bool{}
	for _, f := range ForbiddenAIActions() {
		forbidden[f] = true
	}
	var bad []string
	for _, act := range s.AI.Actions {
		if forbidden[act] {
			bad = append(bad, act)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return res(a, Violated, fmt.Sprintf("model %q performed or attempted forbidden action(s): %s",
			s.AI.ModelID, strings.Join(bad, ", ")))
	}
	return res(a, Satisfied, fmt.Sprintf("model %q performed no action carrying evidence authority", s.AI.ModelID))
}

// Article 9 -- ZKP Has Bounded Meaning. Bounded by construction: this
// build ships no prover, so no proof can overclaim.
func checkArticle9(_ Subject) Result {
	a := mustArticle(9)
	return res(a, NotEvaluable,
		"no ZKP prover exists in this build; the article binds any future proof to its predicate but has nothing to judge")
}

// Article 10 -- Replay Must Be Independent. Bounded by the existence
// of a separately-compiled verifier binary.
func checkArticle10(_ Subject) Result {
	a := mustArticle(10)
	return res(a, Satisfied,
		"cmd/veriqo-commercial-verify is a separate binary that reads only an exported package and makes no runtime calls")
}

// Article 11 -- Disagreement Must Remain Visible.
func checkArticle11(s Subject) Result {
	a := mustArticle(11)
	if s.Dissent == nil {
		return res(a, NotEvaluable, "no dissent facts supplied")
	}
	d := s.Dissent
	carried := map[string]int{}
	for _, sev := range d.CarriedToFinding {
		carried[sev]++
	}
	var dropped []string
	for _, sev := range d.Recorded {
		if sev != "MATERIAL" && sev != "CRITICAL" {
			continue
		}
		if carried[sev] > 0 {
			carried[sev]--
			continue
		}
		dropped = append(dropped, sev)
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		return res(a, Violated, fmt.Sprintf(
			"%d recorded dissent(s) of severity %s did not reach the final finding",
			len(dropped), strings.Join(dropped, ", ")))
	}
	return res(a, Satisfied, fmt.Sprintf("%d dissent(s) recorded; every MATERIAL/CRITICAL one reached the finding",
		len(d.Recorded)))
}

// Article 12 -- Procedural Symmetry.
func checkArticle12(s Subject) Result {
	a := mustArticle(12)
	if s.Procedure == nil {
		return res(a, NotEvaluable, "no procedure facts supplied")
	}
	p := s.Procedure
	if len(p.PartyPolicies) < 2 {
		return res(a, NotEvaluable, "fewer than two parties supplied; symmetry is not yet meaningful")
	}
	seen := map[string][]string{}
	for party, pol := range p.PartyPolicies {
		seen[pol] = append(seen[pol], party)
	}
	if len(seen) == 1 {
		return res(a, Satisfied, "all parties evaluated under one policy version")
	}
	if p.AuthorizedException {
		return res(a, Satisfied, fmt.Sprintf(
			"%d distinct policy versions across parties, under a recorded authorized exception", len(seen)))
	}
	var pols []string
	for pol := range seen {
		pols = append(pols, pol)
	}
	sort.Strings(pols)
	return res(a, Violated, fmt.Sprintf(
		"parties evaluated under differing policy versions (%s) with no authorized exception recorded",
		strings.Join(pols, ", ")))
}

// Article 13 -- Party Influence Must Be Disclosed.
func checkArticle13(s Subject) Result {
	a := mustArticle(13)
	if s.Procedure == nil {
		return res(a, NotEvaluable, "no procedure facts supplied")
	}
	p := s.Procedure
	if !p.PartyInfluenceOccurred {
		return res(a, Satisfied, "no party influence on acquisition occurred")
	}
	if !p.PartyInfluenceRecorded {
		return res(a, Violated, "party influenced acquisition and it was not recorded as process evidence")
	}
	return res(a, Satisfied, "party influence occurred and was recorded")
}

// Article 14 -- Conflict Must Be Declared.
func checkArticle14(s Subject) Result {
	a := mustArticle(14)
	if s.Procedure == nil {
		return res(a, NotEvaluable, "no procedure facts supplied")
	}
	p := s.Procedure
	if p.ConflictsKnown < 0 || p.ConflictsDeclared < 0 {
		return res(a, NotEvaluable, "conflict counts not supplied")
	}
	if p.ConflictsDeclared < p.ConflictsKnown {
		return res(a, Violated, fmt.Sprintf("%d conflict(s) known, only %d declared",
			p.ConflictsKnown, p.ConflictsDeclared))
	}
	return res(a, Satisfied, fmt.Sprintf("%d known conflict(s), all declared", p.ConflictsKnown))
}

// Article 17 -- No Destructive Redaction.
func checkArticle17(s Subject) Result {
	a := mustArticle(17)
	if s.Redaction == nil {
		return res(a, NotEvaluable, "no redaction facts supplied")
	}
	r := s.Redaction
	if r.OriginalHashBefore == "" || r.OriginalHashAfter == "" {
		return res(a, NotEvaluable, "original hashes not both supplied")
	}
	if r.OriginalHashBefore != r.OriginalHashAfter {
		return res(a, Violated, "the original artifact's hash changed across redaction; the original was modified")
	}
	if !r.DerivativeCreated {
		return res(a, Violated, "redaction produced no separate derivative")
	}
	return res(a, Satisfied, "original unchanged; redaction produced a separate derivative")
}

// Article 18 -- No Visual-Only Redaction.
func checkArticle18(s Subject) Result {
	a := mustArticle(18)
	if s.Redaction == nil {
		return res(a, NotEvaluable, "no redaction facts supplied")
	}
	r := s.Redaction
	if !r.RecoveryTestsRun {
		return res(a, NotEvaluable, "no recovery tests were run; visual-only redaction cannot be ruled out")
	}
	if r.ContentRecoverable {
		return res(a, Violated, "redacted content remains recoverable from the derivative")
	}
	return res(a, Satisfied, "recovery tests ran and found no recoverable redacted content")
}

// Article 19 -- Privilege Is Authority-Determined.
func checkArticle19(s Subject) Result {
	a := mustArticle(19)
	if s.Privilege == nil {
		return res(a, NotEvaluable, "no privilege facts supplied")
	}
	p := s.Privilege
	if p.DeterminedBy == "" {
		return res(a, NotEvaluable, "no determining authority recorded")
	}
	if strings.EqualFold(p.DeterminedBy, "VERIQO") || strings.EqualFold(p.DeterminedBy, "system") {
		return res(a, Violated, fmt.Sprintf(
			"privilege status %q was determined by %q; VERIQO enforces privilege but must not determine it",
			p.Status, p.DeterminedBy))
	}
	return res(a, Satisfied, fmt.Sprintf("privilege determined by external authority %q", p.DeterminedBy))
}

// Article 20 -- Access Does Not Imply Use.
func checkArticle20(s Subject) Result {
	a := mustArticle(20)
	if s.Disclosure == nil {
		return res(a, NotEvaluable, "no disclosure facts supplied")
	}
	d := s.Disclosure
	granted := map[string]bool{}
	for _, g := range d.GrantedRights {
		granted[g] = true
	}
	var exceeded []string
	for _, e := range d.ExercisedRights {
		if !granted[e] {
			exceeded = append(exceeded, e)
		}
	}
	if len(exceeded) > 0 {
		sort.Strings(exceeded)
		return res(a, Violated, fmt.Sprintf("right(s) exercised without a matching grant: %s",
			strings.Join(exceeded, ", ")))
	}
	return res(a, Satisfied, fmt.Sprintf("%d right(s) exercised, all separately granted", len(d.ExercisedRights)))
}

// Article 21 -- Redaction Does Not Imply AI Eligibility.
func checkArticle21(s Subject) Result {
	a := mustArticle(21)
	if s.Redaction == nil || s.Disclosure == nil {
		return res(a, NotEvaluable, "redaction and disclosure facts both required")
	}
	granted := map[string]bool{}
	for _, g := range s.Disclosure.GrantedRights {
		granted[g] = true
	}
	for _, aiRight := range []string{"AI_PROCESS", "RAG", "TRAIN"} {
		for _, ex := range s.Disclosure.ExercisedRights {
			if ex == aiRight && !granted[aiRight] {
				return res(a, Violated, fmt.Sprintf(
					"%s was exercised on a redacted derivative without its own grant; redaction is not AI eligibility", aiRight))
			}
		}
	}
	return res(a, Satisfied, "no AI right was exercised on the derivative without its own grant")
}

// Article 22 -- Evidence Version Is Immutable.
func checkArticle22(s Subject) Result {
	a := mustArticle(22)
	if s.Version == nil {
		return res(a, NotEvaluable, "no version facts supplied")
	}
	v := s.Version
	if !v.DerivativeCreated {
		return res(a, Satisfied, "no derivative was created")
	}
	if !v.DerivativeGotNewVersion {
		return res(a, Violated, "a derivative was created without allocating a new version")
	}
	return res(a, Satisfied, "derivative allocated its own version")
}

// Article 23 -- Audit Is Evidence.
func checkArticle23(s Subject) Result {
	a := mustArticle(23)
	if s.Procedure == nil {
		return res(a, NotEvaluable, "no procedure facts supplied")
	}
	if !s.Procedure.ProcessEvidenceRetained {
		return res(a, Violated, "process evidence was not retained; the record of how evidence was handled is itself evidence")
	}
	return res(a, Satisfied, "process evidence retained")
}

// Article 24 -- No Silent Disclosure.
func checkArticle24(s Subject) Result {
	a := mustArticle(24)
	if s.Disclosure == nil {
		return res(a, NotEvaluable, "no disclosure facts supplied")
	}
	d := s.Disclosure
	if !d.Occurred {
		return res(a, Satisfied, "no disclosure occurred")
	}
	if !d.EventEmitted {
		return res(a, Violated, "a disclosure occurred without emitting a ledger event")
	}
	return res(a, Satisfied, "disclosure emitted a ledger event")
}

// Article 25 -- No Silent Privilege Change.
func checkArticle25(s Subject) Result {
	a := mustArticle(25)
	if s.Privilege == nil {
		return res(a, NotEvaluable, "no privilege facts supplied")
	}
	p := s.Privilege
	if !p.StatusChanged {
		return res(a, Satisfied, "privilege status did not change")
	}
	if !p.EventEmitted {
		return res(a, Violated, "privilege status changed without an immutable event")
	}
	return res(a, Satisfied, "privilege status change emitted an immutable event")
}

// Article 26 -- No Silent Policy Retroactivity.
func checkArticle26(s Subject) Result {
	a := mustArticle(26)
	if s.Policy == nil {
		return res(a, NotEvaluable, "no policy facts supplied")
	}
	p := s.Policy
	if !p.RetroactiveChange {
		return res(a, Satisfied, "no retroactive policy application occurred")
	}
	if !p.ChangeRecorded {
		return res(a, Violated, "a policy change was applied to a historical result without being recorded")
	}
	return res(a, Satisfied, "retroactive policy application was recorded")
}

// Article 27 -- No Silent AI Influence.
func checkArticle27(s Subject) Result {
	a := mustArticle(27)
	if s.AI == nil {
		return res(a, NotEvaluable, "no AI facts supplied")
	}
	ai := s.AI
	if !ai.MaterialContribution {
		return res(a, Satisfied, "no material AI contribution occurred")
	}
	if !ai.ContributionRecorded {
		return res(a, Violated, fmt.Sprintf(
			"model %q made a material contribution with no AI contribution record", ai.ModelID))
	}
	return res(a, Satisfied, fmt.Sprintf("model %q's material contribution was recorded", ai.ModelID))
}

// Article 28 -- No Unsupported Independence.
func checkArticle28(s Subject) Result {
	a := mustArticle(28)
	if s.Corroboration == nil {
		return res(a, NotEvaluable, "no corroboration facts supplied")
	}
	c := s.Corroboration
	if !c.ClaimedIndependent {
		return res(a, Satisfied, "no independence claim was made")
	}
	if len(c.DependencyKnown) == 0 {
		return res(a, Violated,
			"independence claimed with no dependency assessment at all; UNKNOWN is not INDEPENDENT")
	}
	var unknown []string
	for pair, known := range c.DependencyKnown {
		if !known {
			unknown = append(unknown, pair)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return res(a, Violated, fmt.Sprintf(
			"independence claimed while dependency remains unassessed for: %s -- UNKNOWN is not INDEPENDENT",
			strings.Join(unknown, ", ")))
	}
	return res(a, Satisfied, fmt.Sprintf("all %d dependency relation(s) assessed from lineage", len(c.DependencyKnown)))
}

// Article 29 -- No Unqualified Absence.
func checkArticle29(s Subject) Result {
	a := mustArticle(29)
	if s.Absence == nil {
		return res(a, NotEvaluable, "no absence facts supplied")
	}
	ab := s.Absence
	if ab.ReportedState == "" {
		return res(a, NotEvaluable, "no absence state reported")
	}
	if ab.ReportedState != "OBSERVED_ABSENT" {
		return res(a, Satisfied, fmt.Sprintf(
			"absence reported as %q, which carries no evidential weight and needs no gate", ab.ReportedState))
	}
	var missing []string
	for _, cond := range ObservabilityGateConditions() {
		if !ab.GateConditions[cond] {
			missing = append(missing, cond)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return res(a, Violated, fmt.Sprintf(
			"OBSERVED_ABSENT asserted with %d unmet observability condition(s): %s",
			len(missing), strings.Join(missing, ", ")))
	}
	return res(a, Satisfied, "OBSERVED_ABSENT asserted with all nine observability conditions met")
}
