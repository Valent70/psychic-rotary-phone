package datarights

import (
	"errors"
	"testing"
)

// TestContractPendingCannotSkipToQualified is the audit's central
// concern: a source with a pending contract must never be treated as
// qualified evidence just because paperwork is in motion.
func TestContractPendingCannotSkipToQualified(t *testing.T) {
	if err := Advance(ContractPending, Qualified); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("expected ErrIllegalTransition for CONTRACT_PENDING -> QUALIFIED, got %v", err)
	}
	if err := Advance(ContractPending, Verified); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("expected ErrIllegalTransition for CONTRACT_PENDING -> VERIFIED, got %v", err)
	}
}

// TestNothingBeforeEvidenceValidatedCanReachQualifiedOrVerified checks
// every status strictly earlier than EVIDENCE_VALIDATED against both
// QUALIFIED and VERIFIED as direct targets -- the general form of the
// rule the CONTRACT_PENDING test above is one instance of.
func TestNothingBeforeEvidenceValidatedCanReachQualifiedOrVerified(t *testing.T) {
	tooEarly := []RightsStatus{
		PubliclyDocumented, CapabilityConfirmed, AccessPending,
		ContractPending, Contracted, PilotExecuted, EvidenceSubmitted,
	}
	for _, s := range tooEarly {
		for _, target := range []RightsStatus{Qualified, Verified} {
			if err := Advance(s, target); !errors.Is(err, ErrIllegalTransition) {
				t.Errorf("%s -> %s: expected ErrIllegalTransition, got %v", s, target, err)
			}
		}
	}
}

// TestFullLegitimateChainSucceedsStepByStep proves the entire ordered
// progression from the bottom to the top is achievable one legal step
// at a time.
func TestFullLegitimateChainSucceedsStepByStep(t *testing.T) {
	chain := []RightsStatus{
		PubliclyDocumented, CapabilityConfirmed, AccessPending, ContractPending,
		Contracted, PilotExecuted, EvidenceSubmitted, EvidenceValidated,
		Qualified, Verified,
	}
	for i := 1; i < len(chain); i++ {
		if err := Advance(chain[i-1], chain[i]); err != nil {
			t.Fatalf("%s -> %s: expected success, got %v", chain[i-1], chain[i], err)
		}
	}
}

// TestSingleStepSkipIsAlwaysRejected sweeps every pair of statuses two
// or more ranks apart and confirms Advance always rejects them, not
// just the QUALIFIED/VERIFIED cases called out above.
func TestSingleStepSkipIsAlwaysRejected(t *testing.T) {
	for i, from := range statusOrder {
		for j, to := range statusOrder {
			if j <= i+1 {
				continue // no-op, downgrade, or legal single step
			}
			if err := Advance(from, to); !errors.Is(err, ErrIllegalTransition) {
				t.Errorf("%s -> %s (skips %d status(es)): expected ErrIllegalTransition, got %v", from, to, j-i-1, err)
			}
		}
	}
}

// TestDowngradeIsPermittedAndDocumentedBehavior proves a real system's
// need to un-qualify a source whose rights lapsed is honored: a
// downgrade from QUALIFIED back to CONTRACT_PENDING (e.g. the contract
// backing the qualification expired) succeeds in one call, regardless
// of how many stages it crosses in reverse.
func TestDowngradeIsPermittedAndDocumentedBehavior(t *testing.T) {
	if err := Advance(Qualified, ContractPending); err != nil {
		t.Fatalf("expected downgrade QUALIFIED -> CONTRACT_PENDING to be permitted, got %v", err)
	}
	if !IsDowngrade(Qualified, ContractPending) {
		t.Fatal("expected IsDowngrade(QUALIFIED, CONTRACT_PENDING) == true")
	}
	if IsDowngrade(ContractPending, Qualified) {
		t.Fatal("expected IsDowngrade(CONTRACT_PENDING, QUALIFIED) == false (that direction is a forward skip, not a downgrade)")
	}
	// Re-earning QUALIFIED after a downgrade is subject to the exact
	// same forward-path rule as the first time -- no shortcut for having
	// held it before.
	if err := Advance(ContractPending, Qualified); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("expected re-qualification to still require the full forward path, got %v", err)
	}
}

func TestNoOpReassertionIsPermitted(t *testing.T) {
	if err := Advance(Contracted, Contracted); err != nil {
		t.Fatalf("expected re-asserting the same status to be a no-op success, got %v", err)
	}
}

func TestAdvanceRejectsUnknownStatus(t *testing.T) {
	if err := Advance("BOGUS", Qualified); !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("expected ErrUnknownStatus for unknown current status, got %v", err)
	}
	if err := Advance(PubliclyDocumented, "BOGUS"); !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("expected ErrUnknownStatus for unknown target status, got %v", err)
	}
}

func TestRecordAdvanceTracksHistoryAndRejectsIllegalMoves(t *testing.T) {
	rec := NewRecord("source-1")
	if rec.Status != PubliclyDocumented {
		t.Fatalf("expected new record to start at PUBLICLY_DOCUMENTED, got %s", rec.Status)
	}
	steps := []RightsStatus{CapabilityConfirmed, AccessPending, ContractPending}
	for _, s := range steps {
		if err := rec.Advance(s); err != nil {
			t.Fatalf("advance to %s: %v", s, err)
		}
	}
	if err := rec.Advance(Qualified); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("expected the record to refuse CONTRACT_PENDING -> QUALIFIED, got %v", err)
	}
	// The record must be unchanged after a rejected transition.
	if rec.Status != ContractPending {
		t.Fatalf("expected status to remain CONTRACT_PENDING after a rejected advance, got %s", rec.Status)
	}
	wantHistory := []RightsStatus{PubliclyDocumented, CapabilityConfirmed, AccessPending, ContractPending}
	if len(rec.History) != len(wantHistory) {
		t.Fatalf("expected history %v, got %v", wantHistory, rec.History)
	}
	for i, s := range wantHistory {
		if rec.History[i] != s {
			t.Fatalf("history[%d]: expected %s, got %s", i, s, rec.History[i])
		}
	}
}

func TestRecordAtLeast(t *testing.T) {
	rec := NewRecord("source-1")
	for _, s := range []RightsStatus{CapabilityConfirmed, AccessPending, ContractPending, Contracted, PilotExecuted} {
		if err := rec.Advance(s); err != nil {
			t.Fatal(err)
		}
	}
	if !rec.AtLeast(ContractPending) {
		t.Fatal("expected AtLeast(CONTRACT_PENDING) == true once past it")
	}
	if rec.AtLeast(Qualified) {
		t.Fatal("expected AtLeast(QUALIFIED) == false before reaching it")
	}
}
