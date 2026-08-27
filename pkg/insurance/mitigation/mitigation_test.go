package mitigation

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"veriqo/pkg/insurance/party"
)

// ---- Amount ----

func TestNewAmountRejectsEmptyCurrency(t *testing.T) {
	if _, err := NewAmount(100, ""); !errors.Is(err, ErrEmptyCurrency) {
		t.Fatalf("expected ErrEmptyCurrency, got %v", err)
	}
}

func TestAmountAddRejectsCurrencyMismatch(t *testing.T) {
	usd, _ := NewAmount(100, "USD")
	idr, _ := NewAmount(100, "IDR")
	if _, err := usd.Add(idr); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}
}

func TestAmountSubRejectsCurrencyMismatch(t *testing.T) {
	usd, _ := NewAmount(100, "USD")
	idr, _ := NewAmount(100, "IDR")
	if _, err := usd.Sub(idr); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}
}

func TestAmountAddAndSub(t *testing.T) {
	a, _ := NewAmount(700, "USD")
	b, _ := NewAmount(300, "USD")
	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if sum.MinorUnits != 1000 || sum.Currency != "USD" {
		t.Fatalf("expected 1000 USD, got %d %s", sum.MinorUnits, sum.Currency)
	}
	diff, err := a.Sub(b)
	if err != nil {
		t.Fatalf("Sub: %v", err)
	}
	if diff.MinorUnits != 400 {
		t.Fatalf("expected 400, got %d", diff.MinorUnits)
	}
}

// TestSumIsExactIntegerArithmetic is the blueprint-required proof that
// aggregating cost/avoided_loss figures is exact integer arithmetic
// with no floating-point drift, even across many additions of awkward
// cent values (the kind of value — $0.10, $0.20, ... — that famously
// does NOT sum exactly under float64).
func TestSumIsExactIntegerArithmetic(t *testing.T) {
	caseID := "CASE-SUM"
	tl, err := NewTimeline(caseID)
	if err != nil {
		t.Fatalf("NewTimeline: %v", err)
	}

	// 50 actions with deliberately awkward cent values: 1, 3, 7, 11,
	// ... cents, i.e. minorUnits = 4*i + 1 for i in [0, 50). This is
	// not a round number of cents and does not divide evenly, which is
	// exactly the shape of value that reveals float64 rounding drift
	// if it were present.
	const n = 50
	var wantTotal int64
	for i := 0; i < n; i++ {
		minorUnits := int64(4*i + 1) // 1, 5, 9, 13, ...
		wantTotal += minorUnits
		cost, err := NewAmount(minorUnits, "USD")
		if err != nil {
			t.Fatalf("NewAmount: %v", err)
		}
		avoided, err := NewAmount(minorUnits*2+1, "USD") // also awkward, independent series
		if err != nil {
			t.Fatalf("NewAmount: %v", err)
		}
		a, err := New(caseID, actionIDFor(i), "test mitigation step", uint64(i), party.PartyID("PTY-1"), &cost, &avoided, nil, "")
		if err != nil {
			t.Fatalf("New action %d: %v", i, err)
		}
		if err := tl.Add(a); err != nil {
			t.Fatalf("Add action %d: %v", i, err)
		}
	}

	impact, err := tl.Impact()
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if impact.TotalCost.MinorUnits != wantTotal {
		t.Fatalf("expected exact TotalCost %d, got %d", wantTotal, impact.TotalCost.MinorUnits)
	}

	// Hand-computed expected avoided-loss total: sum over i of
	// (2*(4*i+1) + 1) = sum of (8*i + 3) for i in [0, 50).
	var wantAvoided int64
	for i := 0; i < n; i++ {
		wantAvoided += int64(8*i + 3)
	}
	if impact.TotalAvoidedLoss.MinorUnits != wantAvoided {
		t.Fatalf("expected exact TotalAvoidedLoss %d, got %d", wantAvoided, impact.TotalAvoidedLoss.MinorUnits)
	}

	wantNet := wantAvoided - wantTotal
	if impact.NetImpact.MinorUnits != wantNet {
		t.Fatalf("expected exact NetImpact %d, got %d", wantNet, impact.NetImpact.MinorUnits)
	}
	if impact.Status != QuantificationComplete {
		t.Fatalf("expected QuantificationComplete (every action fully quantified), got %s", impact.Status)
	}
}

func actionIDFor(i int) string {
	return "ACT-" + string(rune('A'+i%26)) + itoa(i)
}

// itoa avoids importing strconv purely for a test helper's readability;
// stdlib-only is non-negotiable across this module, and strconv IS
// stdlib, but a tiny local helper keeps this test file's import list
// obviously minimal. (strconv is stdlib and would be equally fine.)
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ---- Action ----

func TestNewActionRejectsEmptyCaseID(t *testing.T) {
	if _, err := New("", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, nil, ""); !errors.Is(err, ErrEmptyCaseID) {
		t.Fatalf("expected ErrEmptyCaseID, got %v", err)
	}
}

func TestNewActionRejectsEmptyActionID(t *testing.T) {
	if _, err := New("CASE-1", "", "Cargo isolated", 100, "PTY-1", nil, nil, nil, ""); !errors.Is(err, ErrEmptyActionID) {
		t.Fatalf("expected ErrEmptyActionID, got %v", err)
	}
}

func TestNewActionRejectsEmptyMitigationAction(t *testing.T) {
	if _, err := New("CASE-1", "ACT-1", "", 100, "PTY-1", nil, nil, nil, ""); !errors.Is(err, ErrEmptyMitigationName) {
		t.Fatalf("expected ErrEmptyMitigationName, got %v", err)
	}
}

func TestNewActionRejectsEmptyActor(t *testing.T) {
	if _, err := New("CASE-1", "ACT-1", "Cargo isolated", 100, "", nil, nil, nil, ""); !errors.Is(err, ErrEmptyActor) {
		t.Fatalf("expected ErrEmptyActor, got %v", err)
	}
}

func TestNewActionRejectsNegativeCost(t *testing.T) {
	bad := Amount{MinorUnits: -1, Currency: "USD"}
	if _, err := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", &bad, nil, nil, ""); !errors.Is(err, ErrNegativeCost) {
		t.Fatalf("expected ErrNegativeCost, got %v", err)
	}
}

func TestNewActionRejectsNegativeAvoidedLoss(t *testing.T) {
	bad := Amount{MinorUnits: -1, Currency: "USD"}
	if _, err := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, &bad, nil, ""); !errors.Is(err, ErrNegativeAvoidedLoss) {
		t.Fatalf("expected ErrNegativeAvoidedLoss, got %v", err)
	}
}

func TestNewActionAllowsNilCostAndAvoidedLoss(t *testing.T) {
	a, err := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, []string{"EV-1"}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Cost != nil || a.AvoidedLoss != nil {
		t.Fatalf("expected nil Cost and AvoidedLoss to remain nil, got %+v / %+v", a.Cost, a.AvoidedLoss)
	}
	if a.SupportingEvidence[0] != "EV-1" {
		t.Fatalf("expected supporting evidence to be preserved, got %v", a.SupportingEvidence)
	}
}

// TestActionSupportingEvidenceIsIndependentCopy ensures New() does not
// alias the caller's slice — mutating the caller's slice afterward must
// not silently rewrite an already-recorded Action's evidence list
// (the general "never overwrite evidence, create a new version"
// discipline extended to this package's own inputs, blueprint §36).
func TestActionSupportingEvidenceIsIndependentCopy(t *testing.T) {
	ev := []string{"EV-1", "EV-2"}
	a, err := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, ev, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ev[0] = "TAMPERED"
	if a.SupportingEvidence[0] != "EV-1" {
		t.Fatalf("Action.SupportingEvidence was aliased to the caller's slice: got %v", a.SupportingEvidence)
	}
}

// ---- Timeline ----

func TestNewTimelineRejectsEmptyCaseID(t *testing.T) {
	if _, err := NewTimeline(""); !errors.Is(err, ErrTimelineEmptyCaseID) {
		t.Fatalf("expected ErrTimelineEmptyCaseID, got %v", err)
	}
}

func TestTimelineAddRejectsMismatchedCaseID(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	a, _ := New("CASE-OTHER", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, nil, "")
	if err := tl.Add(a); !errors.Is(err, ErrCaseIDMismatch) {
		t.Fatalf("expected ErrCaseIDMismatch, got %v", err)
	}
}

func TestTimelineAddRejectsDuplicateActionID(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	a, _ := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, nil, "")
	if err := tl.Add(a); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := tl.Add(a); !errors.Is(err, ErrDuplicateActionID) {
		t.Fatalf("expected ErrDuplicateActionID, got %v", err)
	}
}

func TestTimelineAddRejectsUnknownReference(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	a, _ := New("CASE-1", "ACT-2", "Repacking", 200, "PTY-1", nil, nil, nil, "ACT-1-NEVER-ADDED")
	if err := tl.Add(a); !errors.Is(err, ErrReferencesUnknown) {
		t.Fatalf("expected ErrReferencesUnknown, got %v", err)
	}
}

func TestTimelineAllPreservesAddOrder(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	a1, _ := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, nil, "")
	a2, _ := New("CASE-1", "ACT-2", "Repacking", 200, "PTY-1", nil, nil, nil, "ACT-1")
	tl.Add(a1)
	tl.Add(a2)
	all := tl.All()
	if len(all) != 2 || all[0].ActionID != "ACT-1" || all[1].ActionID != "ACT-2" {
		t.Fatalf("expected [ACT-1 ACT-2] in order, got %+v", all)
	}
	if tl.Count() != 2 {
		t.Fatalf("expected Count 2, got %d", tl.Count())
	}
}

func TestTimelineGet(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	a1, _ := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, nil, "")
	tl.Add(a1)
	got, ok := tl.Get("ACT-1")
	if !ok || got.MitigationAction != "Cargo isolated" {
		t.Fatalf("expected to find ACT-1, got %+v ok=%v", got, ok)
	}
	if _, ok := tl.Get("NOPE"); ok {
		t.Fatalf("expected ACT-NOPE not to be found")
	}
}

// ---- Impact: golden-rule proof (§36) that unquantified is NOT zero ----

// TestImpactRefusesWhenNothingIsQuantified is the strongest form of the
// golden-rule proof: a Timeline where every Action's Cost and
// AvoidedLoss are nil must NOT silently produce a zero-valued Impact
// (which would look identical to "every action legitimately cost and
// avoided nothing") — it must refuse outright.
func TestImpactRefusesWhenNothingIsQuantified(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	a, _ := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, nil, nil, "")
	tl.Add(a)
	if _, err := tl.Impact(); !errors.Is(err, ErrNoQuantifiedAmounts) {
		t.Fatalf("expected ErrNoQuantifiedAmounts, got %v", err)
	}
}

// TestImpactExcludesUnquantifiedFromSums proves the distinction the
// golden rule requires is REAL, not cosmetic: an unknown (nil)
// Cost/AvoidedLoss is excluded from the sum entirely, while an
// EXPLICIT known Amount{MinorUnits: 0, ...} — "this step is known to
// have cost nothing" — is a genuine value that DOES participate.
func TestImpactExcludesUnquantifiedFromSums(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")

	// ACT-1: cost unknown (nil), avoided_loss known = 1000.
	loss1, _ := NewAmount(1000, "USD")
	a1, _ := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", nil, &loss1, nil, "")

	// ACT-2: cost known = 500, avoided_loss unknown (nil).
	cost2, _ := NewAmount(500, "USD")
	a2, _ := New("CASE-1", "ACT-2", "Repacking", 200, "PTY-1", &cost2, nil, nil, "ACT-1")

	// ACT-3: cost EXPLICITLY known to be zero (a free-of-charge step)
	// and avoided_loss known = 0 too — both are real known zeros, not
	// "unknown", and must be counted in the sum without being flagged
	// as unquantified.
	zero, _ := NewAmount(0, "USD")
	a3, _ := New("CASE-1", "ACT-3", "Drying (in-house, no cost)", 300, "PTY-1", &zero, &zero, nil, "ACT-2")

	for _, a := range []Action{a1, a2, a3} {
		if err := tl.Add(a); err != nil {
			t.Fatalf("Add %s: %v", a.ActionID, err)
		}
	}

	impact, err := tl.Impact()
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}

	// TotalCost must be exactly 500 (from ACT-2) + 0 (from ACT-3's
	// EXPLICIT known zero) = 500. If ACT-1's nil Cost had been
	// silently treated as a known zero, the sum would still
	// numerically read 500 here -- so the decisive proof is the
	// flagging below, not the sum alone.
	if impact.TotalCost.MinorUnits != 500 {
		t.Fatalf("expected TotalCost 500, got %d", impact.TotalCost.MinorUnits)
	}
	// TotalAvoidedLoss must be exactly 1000 (from ACT-1) + 0 (ACT-3) = 1000.
	if impact.TotalAvoidedLoss.MinorUnits != 1000 {
		t.Fatalf("expected TotalAvoidedLoss 1000, got %d", impact.TotalAvoidedLoss.MinorUnits)
	}

	// The decisive proof: ACT-1 (nil Cost) must be flagged as
	// unquantified-for-cost, and ACT-2 (nil AvoidedLoss) must be
	// flagged as unquantified-for-avoided-loss. ACT-3, whose Cost and
	// AvoidedLoss were both an EXPLICIT known zero, must appear in
	// NEITHER list -- proving a known zero and an unknown value are
	// tracked as genuinely different things, not merged into "0".
	if !contains(impact.ActionsWithUnquantifiedCost, "ACT-1") {
		t.Fatalf("expected ACT-1 flagged as unquantified cost, got %v", impact.ActionsWithUnquantifiedCost)
	}
	if contains(impact.ActionsWithUnquantifiedCost, "ACT-3") {
		t.Fatalf("ACT-3 has an EXPLICIT known-zero cost and must not be flagged as unquantified, got %v", impact.ActionsWithUnquantifiedCost)
	}
	if !contains(impact.ActionsWithUnquantifiedAvoidedLoss, "ACT-2") {
		t.Fatalf("expected ACT-2 flagged as unquantified avoided_loss, got %v", impact.ActionsWithUnquantifiedAvoidedLoss)
	}
	if contains(impact.ActionsWithUnquantifiedAvoidedLoss, "ACT-3") {
		t.Fatalf("ACT-3 has an EXPLICIT known-zero avoided_loss and must not be flagged as unquantified, got %v", impact.ActionsWithUnquantifiedAvoidedLoss)
	}

	if impact.Status != QuantificationPartial {
		t.Fatalf("expected QuantificationPartial (ACT-1 and ACT-2 each have one unquantified figure), got %s", impact.Status)
	}

	wantNet := impact.TotalAvoidedLoss.MinorUnits - impact.TotalCost.MinorUnits
	if impact.NetImpact.MinorUnits != wantNet {
		t.Fatalf("expected NetImpact %d, got %d", wantNet, impact.NetImpact.MinorUnits)
	}
}

func TestImpactRejectsCurrencyMismatchAcrossActions(t *testing.T) {
	tl, _ := NewTimeline("CASE-1")
	usdCost, _ := NewAmount(100, "USD")
	a1, _ := New("CASE-1", "ACT-1", "Cargo isolated", 100, "PTY-1", &usdCost, nil, nil, "")
	idrCost, _ := NewAmount(100, "IDR")
	a2, _ := New("CASE-1", "ACT-2", "Repacking", 200, "PTY-1", &idrCost, nil, nil, "ACT-1")
	tl.Add(a1)
	tl.Add(a2)
	if _, err := tl.Impact(); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// ---- The blueprint's own worked example, §22 ----
//
// "Cargo isolated -> Repacking -> Drying -> Salvage sale."
//
// This models a realistic shape of that chain: the first three steps
// each incur a known cost but their avoided-loss contribution isn't
// individually quantifiable until the cargo is finally disposed of via
// salvage sale, where BOTH a cost (broker fee) and the recovered value
// (avoided_loss) become known. That is exactly the golden-rule
// situation this package exists to model honestly: three actions with
// a real, non-zero, currently-unknown avoided_loss, not a guessed
// zero.
func TestMitigationChainCargoIsolatedRepackingDryingSalvageSale(t *testing.T) {
	const caseID = "CASE-CARGO-WETDAMAGE-001"
	tl, err := NewTimeline(caseID)
	if err != nil {
		t.Fatalf("NewTimeline: %v", err)
	}

	surveyor := party.PartyID("PTY-SURVEYOR-1")
	salvor := party.PartyID("PTY-SALVOR-1")

	isolationCost, _ := NewAmount(150_00, "USD") // $1,500.00
	isolated, err := New(caseID, "ACT-1-ISOLATION", "Cargo isolated", 1_000, surveyor,
		&isolationCost, nil, []string{"EV-SURVEY-001"}, "")
	if err != nil {
		t.Fatalf("New (isolation): %v", err)
	}
	if err := tl.Add(isolated); err != nil {
		t.Fatalf("Add (isolation): %v", err)
	}

	repackingCost, _ := NewAmount(320_00, "USD") // $3,200.00
	repacking, err := New(caseID, "ACT-2-REPACKING", "Repacking", 2_000, surveyor,
		&repackingCost, nil, []string{"EV-REPACK-INVOICE-001"}, "ACT-1-ISOLATION")
	if err != nil {
		t.Fatalf("New (repacking): %v", err)
	}
	if err := tl.Add(repacking); err != nil {
		t.Fatalf("Add (repacking): %v", err)
	}

	dryingCost, _ := NewAmount(180_00, "USD") // $1,800.00
	drying, err := New(caseID, "ACT-3-DRYING", "Drying", 3_000, surveyor,
		&dryingCost, nil, []string{"EV-DRYING-INVOICE-001"}, "ACT-2-REPACKING")
	if err != nil {
		t.Fatalf("New (drying): %v", err)
	}
	if err := tl.Add(drying); err != nil {
		t.Fatalf("Add (drying): %v", err)
	}

	salvageCost, _ := NewAmount(50_00, "USD")       // $500.00 broker fee
	salvageAvoided, _ := NewAmount(1_200_00, "USD") // $12,000.00 recovered
	salvage, err := New(caseID, "ACT-4-SALVAGE-SALE", "Salvage sale", 4_000, salvor,
		&salvageCost, &salvageAvoided, []string{"EV-SALVAGE-CONTRACT-001", "EV-SALVAGE-RECEIPT-001"}, "ACT-3-DRYING")
	if err != nil {
		t.Fatalf("New (salvage sale): %v", err)
	}
	if err := tl.Add(salvage); err != nil {
		t.Fatalf("Add (salvage sale): %v", err)
	}

	if tl.Count() != 4 {
		t.Fatalf("expected 4 actions in the chain, got %d", tl.Count())
	}

	// Sanity: the chain really is a chain -- each step (after the
	// first) references the one before it.
	for _, want := range []struct{ id, prior string }{
		{"ACT-2-REPACKING", "ACT-1-ISOLATION"},
		{"ACT-3-DRYING", "ACT-2-REPACKING"},
		{"ACT-4-SALVAGE-SALE", "ACT-3-DRYING"},
	} {
		got, ok := tl.Get(want.id)
		if !ok {
			t.Fatalf("expected %s on the timeline", want.id)
		}
		if got.ReferencesActionID != want.prior {
			t.Fatalf("expected %s to reference %s, got %q", want.id, want.prior, got.ReferencesActionID)
		}
	}

	impact, err := tl.Impact()
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}

	wantTotalCost := int64(150_00 + 320_00 + 180_00 + 50_00) // 700_00 = $7,000.00
	if impact.TotalCost.MinorUnits != wantTotalCost {
		t.Fatalf("expected TotalCost %d, got %d", wantTotalCost, impact.TotalCost.MinorUnits)
	}

	wantTotalAvoided := int64(1_200_00) // only the salvage sale's avoided_loss is known
	if impact.TotalAvoidedLoss.MinorUnits != wantTotalAvoided {
		t.Fatalf("expected TotalAvoidedLoss %d, got %d", wantTotalAvoided, impact.TotalAvoidedLoss.MinorUnits)
	}

	wantNet := wantTotalAvoided - wantTotalCost // 1_200_00 - 700_00 = 500_00 = $5,000.00
	if impact.NetImpact.MinorUnits != wantNet {
		t.Fatalf("expected NetImpact %d ($%d.%02d), got %d", wantNet, wantNet/100, wantNet%100, impact.NetImpact.MinorUnits)
	}
	if impact.NetImpact.Currency != "USD" {
		t.Fatalf("expected NetImpact currency USD, got %s", impact.NetImpact.Currency)
	}

	// Isolation, repacking and drying each have an unknown avoided_loss
	// at the time they were recorded -- this must be visible on the
	// Impact, not silently swallowed into a total that looks complete.
	for _, id := range []string{"ACT-1-ISOLATION", "ACT-2-REPACKING", "ACT-3-DRYING"} {
		if !contains(impact.ActionsWithUnquantifiedAvoidedLoss, id) {
			t.Fatalf("expected %s flagged as unquantified avoided_loss, got %v", id, impact.ActionsWithUnquantifiedAvoidedLoss)
		}
	}
	if len(impact.ActionsWithUnquantifiedCost) != 0 {
		t.Fatalf("expected every action's cost to be known, got unquantified costs: %v", impact.ActionsWithUnquantifiedCost)
	}
	if impact.Status != QuantificationPartial {
		t.Fatalf("expected QuantificationPartial (avoided_loss unknown for 3 of 4 actions), got %s", impact.Status)
	}
	if impact.TotalActionCount != 4 {
		t.Fatalf("expected TotalActionCount 4, got %d", impact.TotalActionCount)
	}
}

// ---- Public-API surface: no "reasonableness" judgment anywhere (§22) ----
//
// Blueprint §22, verbatim: "VICE menghitung impact tetapi tidak
// menentukan apakah mitigation 'reasonable' secara hukum" -- VICE
// computes impact but does not decide whether a mitigation is legally
// "reasonable". This test enforces that by reflection over every
// exported struct's field names and every exported type's method set
// in this package, rather than relying on code review alone (though
// this package's author also manually re-read every exported
// identifier below before considering the package done, per the
// task's own instruction to document that check).
func TestPublicAPIHasNoReasonablenessJudgment(t *testing.T) {
	forbidden := []string{"reasonable", "unreasonable", "reasonableness", "adequate", "adequacy", "legally", "lawful", "legal"}

	checkName := func(kind, name string) {
		lower := strings.ToLower(name)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("%s %q contains forbidden reasonableness/legal-adequacy term %q -- blueprint §22 forbids VICE from judging whether a mitigation is legally reasonable", kind, name, f)
			}
		}
	}

	structTypes := []reflect.Type{
		reflect.TypeOf(Amount{}),
		reflect.TypeOf(Action{}),
		reflect.TypeOf(Impact{}),
	}
	for _, typ := range structTypes {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			checkName("field "+typ.Name()+".", f.Name)
			// Also check the JSON tag, in case a field name is
			// innocuous but its serialized name is not.
			if tag, ok := f.Tag.Lookup("json"); ok {
				checkName("json tag on "+typ.Name()+".", tag)
			}
		}
	}

	// Method sets: pointer receivers where used (Timeline), value
	// receivers otherwise (Amount).
	methodHolders := []reflect.Type{
		reflect.TypeOf(&Timeline{}),
		reflect.TypeOf(Amount{}),
	}
	for _, typ := range methodHolders {
		for i := 0; i < typ.NumMethod(); i++ {
			checkName("method "+typ.String()+".", typ.Method(i).Name)
		}
	}

	// Package-level exported function names (New, NewAmount, NewTimeline).
	for _, name := range []string{"New", "NewAmount", "NewTimeline"} {
		checkName("function ", name)
	}

	// QuantificationStatus's own enumerated values are the closest
	// thing to a "verdict" this package emits -- confirm they state
	// only completeness-of-data facts (COMPLETE/PARTIAL), never a
	// reasonableness or legal-adequacy conclusion.
	for _, v := range []QuantificationStatus{QuantificationComplete, QuantificationPartial} {
		checkName("QuantificationStatus value ", string(v))
	}
}
