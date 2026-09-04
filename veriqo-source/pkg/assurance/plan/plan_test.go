package plan

import (
	"errors"
	"strings"
	"testing"

	"veriqo/pkg/assurance"
	"veriqo/pkg/qualification/ledger"
)

// TestThePlanCoversEveryOpenControl. A plan that quietly omitted a gap
// would let it disappear from view without being closed.
func TestThePlanCoversEveryOpenControl(t *testing.T) {
	rows, err := assurance.Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	planned := map[int]bool{}
	for _, i := range Items {
		planned[i.Article] = true
	}
	var missing []string
	for _, r := range rows {
		switch r.Verdict {
		case assurance.AssuranceGap, assurance.ExternalQualification:
			if !planned[r.Article] {
				missing = append(missing, r.Control)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d open control(s) have no qualification evidence plan:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestThePlanDoesNotPlanForClosedControls. An item for an article that
// is OPEN or already closed would be planning work that does not follow
// from the matrix.
func TestThePlanDoesNotPlanForClosedControls(t *testing.T) {
	rows, err := assurance.Assemble()
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	verdicts := map[int]assurance.Verdict{}
	for _, r := range rows {
		verdicts[r.Article] = r.Verdict
	}
	for _, i := range Items {
		v, ok := verdicts[i.Article]
		if !ok {
			t.Errorf("the plan covers article %d, which is not in the matrix", i.Article)
			continue
		}
		if v != assurance.AssuranceGap && v != assurance.ExternalQualification {
			t.Errorf("article %d is %s; planning a promotion for it is planning work the matrix does not call for",
				i.Article, v)
		}
	}
}

// TestEveryItemIsComplete. Five fields, each load-bearing.
func TestEveryItemIsComplete(t *testing.T) {
	if err := Validate(Items); err != nil {
		t.Fatal(err)
	}
}

// TestVeriqoCannotValidateItsOwnPromotionToQualified is the rule that
// keeps the plan from being a plan to remain self-tested forever while
// producing documents that read like qualification.
func TestVeriqoCannotValidateItsOwnPromotionToQualified(t *testing.T) {
	bad := Item{
		Article: 99, Control: "a control", From: ledger.Assured, To: ledger.Qualified,
		Obligation: "o", Method: "m", Artefact: "a", Criteria: "c",
		Validator: VeriqoEngineering,
	}
	if err := bad.Validate(); !errors.Is(err, ErrSelfValidate) {
		t.Fatalf("want ErrSelfValidate, got %v", err)
	}
}

// TestNoItemPlansAPromotionVeriqoCannotMake. Every actionable item must
// stop at the internal ceiling.
func TestNoItemPlansAPromotionVeriqoCannotMake(t *testing.T) {
	for _, i := range Items {
		if !i.Actionable() {
			continue
		}
		if i.To > ledger.InternalCeiling() {
			t.Errorf("article %d is marked actionable but plans a promotion to %s, above the internal ceiling %s",
				i.Article, i.To, ledger.InternalCeiling())
		}
		if i.Validator.IsExternal() {
			t.Errorf("article %d is marked actionable but names the external validator %q",
				i.Article, i.Validator)
		}
	}
}

// TestEveryBlockedItemNamesItsBlocker. "Blocked" with no reason is
// indistinguishable from "not started".
func TestEveryBlockedItemNamesItsBlocker(t *testing.T) {
	for _, i := range Items {
		if i.Actionable() && i.Validator.IsExternal() {
			t.Errorf("article %d names an external validator but is not marked blocked", i.Article)
		}
		if !i.Actionable() && strings.TrimSpace(i.Blocker) == "" {
			t.Errorf("article %d is blocked with no stated blocker", i.Article)
		}
	}
}

// TestMostOfThePlanIsBlocked is the finding, asserted so that it cannot
// quietly stop being true.
//
// If a future round makes most items actionable, either the assurance
// ladder was weakened or an outside party was engaged. Both are
// significant enough that a test should force somebody to say which.
func TestMostOfThePlanIsBlocked(t *testing.T) {
	s := Summarize(Items)
	if s.Total == 0 {
		t.Fatal("the plan is empty")
	}
	if s.BlockedExternal <= s.Actionable {
		t.Fatalf("%d of %d items are blocked on an outside party. That is no longer the majority, "+
			"which means either an outside party was engaged (say so) or the ladder was weakened (do not)",
			s.BlockedExternal, s.Total)
	}
}

// TestTheHeadlineCannotBeQuotedAsProgress.
func TestTheHeadlineCannotBeQuotedAsProgress(t *testing.T) {
	h := Summarize(Items).Headline()
	if !strings.Contains(h, "does not shrink by working harder") {
		t.Fatal("the headline does not say that the blocked count is not an engineering backlog")
	}
}

// TestThePlanStopsShortOfProduction. No plan written before deployment
// can schedule production evidence.
func TestThePlanStopsShortOfProduction(t *testing.T) {
	for _, i := range Items {
		if i.To >= ledger.ProductionProven {
			t.Errorf("article %d plans a promotion to %s, which no pre-deployment plan can schedule",
				i.Article, i.To)
		}
	}
	if !strings.Contains(AccreditationTrack, "PRODUCTION_PROVEN") {
		t.Fatal("the plan does not state where it stops")
	}
}

// TestArticle18PlansForAnAdversarialLabNotAnAssessor. Article 18's
// obligation is that somebody FAILS to recover redacted content, which
// is falsification, not examination.
func TestArticle18PlansForAnAdversarialLabNotAnAssessor(t *testing.T) {
	for _, i := range Items {
		if i.Article != 18 {
			continue
		}
		if i.Validator != AdversarialLab {
			t.Fatalf("article 18's validator is %q; recovering redacted content is an attack, not a review",
				i.Validator)
		}
		if !strings.Contains(i.Blocker, "unmeasured") {
			t.Fatal("article 18's blocker does not state that the real-world refusal rate is unmeasured")
		}
		return
	}
	t.Fatal("article 18 has no plan item")
}

// TestTheReportSeparatesActionableFromBlocked.
func TestTheReportSeparatesActionableFromBlocked(t *testing.T) {
	r := Report(Items)
	for _, section := range []string{"ACTIONABLE BY VERIQO TODAY", "BLOCKED ON AN OUTSIDE PARTY"} {
		if !strings.Contains(r, section) {
			t.Errorf("the report omits the %q section", section)
		}
	}
	if !strings.Contains(r, "This is NOT a backlog") {
		t.Fatal("the report does not say it is not a backlog")
	}
}
