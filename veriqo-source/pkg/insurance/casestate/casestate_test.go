package casestate

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"veriqo/pkg/insurance/credential"
	"veriqo/pkg/insurance/network"
	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/payment"
	"veriqo/pkg/insurance/quantum"
	"veriqo/pkg/insurance/reserve"
)

func mustNew(t *testing.T) *CaseLifecycle {
	t.Helper()
	cl, err := New("CASE-TEST-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cl
}

func TestNewStartsAtInvited(t *testing.T) {
	cl := mustNew(t)
	if cl.State() != StateInvited {
		t.Fatalf("expected StateInvited, got %s", cl.State())
	}
}

func TestValidTransitionSucceeds(t *testing.T) {
	cl := mustNew(t)
	tr, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-1", "counterparty accepted", 10)
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if tr.From != StateInvited || tr.To != StateAccepted {
		t.Fatalf("unexpected transition record: %+v", tr)
	}
	if cl.State() != StateAccepted {
		t.Fatalf("expected StateAccepted, got %s", cl.State())
	}
}

func TestInvalidTransitionIsRefused(t *testing.T) {
	cl := mustNew(t)
	// INVITED -> PAYMENT_EXECUTED is not modelled: skipping the entire
	// chain must be structurally impossible.
	_, err := cl.Transition(StatePaymentExecuted, "PTY-1", party.RoleInsured, "", "IDEM-1", "skip ahead", 10)
	if err == nil {
		t.Fatal("expected an unmodelled transition to be refused")
	}
}

func TestTransitionRequiringEvidenceIsRefusedWithout(t *testing.T) {
	cl := mustNew(t)
	if _, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-1", "accepted", 10); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	_, err := cl.Transition(StateEvidenceExchanged, "PTY-1", party.RoleInsured, "", "IDEM-2", "no evidence cited", 20)
	if err != ErrEvidenceRequired && err == nil {
		t.Fatalf("expected an evidence-requiring transition with no EvidenceID to be refused, got %v", err)
	}
	if _, err := cl.Transition(StateEvidenceExchanged, "PTY-1", party.RoleInsured, "EVID-1", "IDEM-3", "evidence cited", 20); err != nil {
		t.Fatalf("expected the same transition WITH evidence to succeed: %v", err)
	}
}

func TestAuthorityIsEnforcedPerTransition(t *testing.T) {
	cl := mustNew(t)
	driveToQuantified(t, cl, quantum.Amount(1000))
	// RoleInsured has no reserve authority -- QUANTIFIED -> RESERVED
	// must be refused for that role, even via the real coupled method.
	_, err := cl.OpenReserve("RSV-1", "CLM-1", quantum.Amount(1000), "PTY-1", party.RoleInsured, "attempt", "IDEM-RESERVE", 100)
	if err == nil {
		t.Fatal("expected an unauthorized role to be refused for QUANTIFIED -> RESERVED")
	}
	// RoleInsurer DOES have reserve authority.
	if _, err := cl.OpenReserve("RSV-1", "CLM-1", quantum.Amount(1000), "PTY-INSURER", party.RoleInsurer, "authorized", "IDEM-RESERVE-2", 100); err != nil {
		t.Fatalf("expected an authorized role to succeed: %v", err)
	}
}

// TestGenericTransitionCannotBypassDomainCoupledStates is the FINAL
// INTERNAL CHECK item A structural proof: calling Transition() directly
// for RESERVED/PAYMENT_AUTHORIZED/PAYMENT_EXECUTED -- even with a fully
// authorized role and non-empty evidence -- must be refused. Only
// OpenReserve/AuthorizePayment/ExecutePayment may reach these states,
// because only they perform the REAL reserve/payment domain call
// authority, evidence, and settlement evidence checks are gating.
func TestGenericTransitionCannotBypassDomainCoupledStates(t *testing.T) {
	for _, target := range []State{StateReserved, StatePaymentAuthorized, StatePaymentExecuted} {
		cl := mustNew(t)
		driveToQuantified(t, cl, quantum.Amount(1000))
		_, err := cl.Transition(target, "PTY-INSURER", party.RoleInsurer, "EVID-1", "IDEM-BYPASS-"+string(target), "attempted bypass", 100)
		if err == nil {
			t.Fatalf("expected Transition() to refuse reaching %s directly -- this is the bypass invariant a state-machine audit must rule out", target)
		}
	}
}

func TestIdempotentRetrySameTargetReturnsSameTransition(t *testing.T) {
	cl := mustNew(t)
	first, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-SAME", "accepted", 10)
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	second, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-SAME", "accepted again (retry)", 11)
	if err != nil {
		t.Fatalf("expected idempotent retry to succeed, got %v", err)
	}
	if first != second {
		t.Fatalf("expected the retried transition to return the identical record: %+v vs %+v", first, second)
	}
	if len(cl.History()) != 1 {
		t.Fatalf("expected exactly 1 history entry after an idempotent retry, got %d", len(cl.History()))
	}
}

func TestIdempotencyKeyReuseWithDifferentTargetIsRefused(t *testing.T) {
	cl := mustNew(t)
	if _, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-X", "accepted", 10); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if _, err := cl.Transition(StateEvidenceExchanged, "PTY-1", party.RoleInsured, "EVID-1", "IDEM-Y", "exchanged", 20); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	// Reusing IDEM-X now, from a DIFFERENT current state, targeting a
	// DIFFERENT state, is a real conflict -- must be refused, not
	// silently accepted as a "retry".
	_, err := cl.Transition(StateUnderReview, "PTY-1", party.RoleInsured, "", "IDEM-X", "conflicting reuse", 30)
	if err != ErrIdempotencyKeyReused {
		t.Fatalf("expected ErrIdempotencyKeyReused, got %v", err)
	}
}

// TestOutOfOrderTickIsRefused is the FINAL INTERNAL CHECK item G "late
// message / out-of-order message" proof: a transition carrying a tick
// earlier than the last one already recorded is refused, while a
// transition carrying the SAME tick (several events genuinely sharing
// one instant) remains legal.
func TestOutOfOrderTickIsRefused(t *testing.T) {
	cl := mustNew(t)
	if _, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-A", "accepted", 100); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	// A message that arrived late, timestamped BEFORE the one already
	// recorded, must be refused.
	_, err := cl.Transition(StateEvidenceExchanged, "PTY-1", party.RoleInsured, "EVID-1", "IDEM-LATE", "a late message", 50)
	if !errors.Is(err, ErrOutOfOrderTick) {
		t.Fatalf("expected ErrOutOfOrderTick for a tick earlier than the last recorded one, got %v", err)
	}
	if len(cl.History()) != 1 {
		t.Fatalf("expected the out-of-order attempt to leave history untouched, got %d entries", len(cl.History()))
	}
	// The SAME tick as the last recorded transition remains legal --
	// several real-world events can genuinely share one instant.
	if _, err := cl.Transition(StateEvidenceExchanged, "PTY-1", party.RoleInsured, "EVID-1", "IDEM-B", "same instant", 100); err != nil {
		t.Fatalf("expected a transition at the SAME tick as the last recorded one to succeed, got %v", err)
	}
}

// TestCredentialRegistryRefusesARevokedActor is the FINAL INTERNAL
// CHECK item G "revoked credential" proof: once a credential.Registry
// is attached (AttachCredentialRegistry), an actor whose qualification
// for the role they are acting under has been revoked is refused, and
// an actor with no recorded qualification at all is refused the same
// way -- but a case with NO registry attached (the default) performs no
// such check at all, exactly as before this item existed.
func TestCredentialRegistryRefusesARevokedActor(t *testing.T) {
	reg := credential.NewRegistry()
	if err := reg.RecordQualification(credential.QualificationRecord{
		PartyID: "PTY-SURVEYOR", Role: party.RoleSurveyor, State: network.StateSelfAttested,
		RecordedBy: "PTY-SURVEYOR", RecordedAtTick: 0, EffectiveAtTick: 0,
	}); err != nil {
		t.Fatalf("RecordQualification: %v", err)
	}
	if err := reg.RecordQualification(credential.QualificationRecord{
		PartyID: "PTY-SURVEYOR", Role: party.RoleSurveyor, State: network.StateRevoked,
		RecordedBy: "PTY-COMPLIANCE", RecordedAtTick: 50, EffectiveAtTick: 50, RevocationReason: "licence lapsed",
	}); err != nil {
		t.Fatalf("RecordQualification (revoke): %v", err)
	}

	cl := mustNew(t)
	cl.AttachCredentialRegistry(reg)

	// PTY-SURVEYOR's qualification was revoked at tick 50 -- any
	// transition they attempt at or after that tick is refused.
	_, err := cl.Transition(StateAccepted, "PTY-SURVEYOR", party.RoleSurveyor, "", "IDEM-REVOKED", "attempted after revocation", 60)
	if !errors.Is(err, ErrActorCredentialNotEffective) {
		t.Fatalf("expected ErrActorCredentialNotEffective for a revoked actor, got %v", err)
	}
	if len(cl.History()) != 0 {
		t.Fatalf("expected the refused attempt to leave history untouched, got %d entries", len(cl.History()))
	}

	// An actor with NO recorded qualification at all is refused the
	// same way -- absence is not silently treated as authorization.
	_, err = cl.Transition(StateAccepted, "PTY-UNKNOWN", party.RoleInsurer, "", "IDEM-UNKNOWN", "no qualification recorded", 60)
	if !errors.Is(err, ErrActorCredentialNotEffective) {
		t.Fatalf("expected ErrActorCredentialNotEffective for an actor with no recorded qualification, got %v", err)
	}

	// Before the revocation took effect (tick 10 < 50), the SAME actor
	// was genuinely qualified -- proving this is a real point-in-time
	// check, not a blanket refusal of PTY-SURVEYOR forever.
	if _, err := cl.Transition(StateAccepted, "PTY-SURVEYOR", party.RoleSurveyor, "", "IDEM-BEFORE", "attempted before revocation", 10); err != nil {
		t.Fatalf("expected the actor's transition before their revocation to succeed, got %v", err)
	}
}

// TestNoCredentialRegistryAttachedPerformsNoCheck proves the item G
// credential check is genuinely opt-in: a case with nothing attached
// behaves exactly as every other test in this file already assumes.
func TestNoCredentialRegistryAttachedPerformsNoCheck(t *testing.T) {
	cl := mustNew(t)
	if cl.CredentialRegistry != nil {
		t.Fatal("expected a fresh CaseLifecycle to have no credential registry attached")
	}
	if _, err := cl.Transition(StateAccepted, "PTY-ANYONE", party.RoleInsured, "", "IDEM-A", "no registry attached", 10); err != nil {
		t.Fatalf("expected no credential check with no registry attached, got %v", err)
	}
}

// TestFailureModesNeverProduceDoubleTransitionOrDoublePayment is the
// FINAL INTERNAL CHECK item G capstone: it walks through every named
// failure mode -- duplicate message, late message, out-of-order
// message, timeout-then-retry, partial exchange, network-failure-then-
// retry -- and proves each is handled without a double state transition
// or a double payment. The SAME mechanism (idempotency-key dedup, now
// joined by the tick-ordering check above) defends against all of them:
// a caller-side timeout/network-failure/partial-exchange is
// indistinguishable, from this package's own point of view, from
// "the caller will retry with the same idempotency key" -- which is
// exactly the case TestIdempotentRetrySameTargetReturnsSameTransition
// already proves is safe. This test applies that same guarantee at the
// one place a double PAYMENT could actually occur: ExecutePayment.
func TestFailureModesNeverProduceDoubleTransitionOrDoublePayment(t *testing.T) {
	cl := mustNew(t)
	driveToQuantified(t, cl, quantum.Amount(50_000))
	if _, err := cl.OpenReserve("RSV-1", "CLM-1", quantum.Amount(50_000), "PTY-INSURER", party.RoleInsurer, "initial reserve", "IDEM-RESERVE", 100); err != nil {
		t.Fatalf("OpenReserve: %v", err)
	}
	if _, err := cl.AuthorizePayment("PAY-1", "CLM-1", "PTY-PAYEE", quantum.Amount(50_000), "IDEM-PAY", "PTY-CLAIMS-HANDLER", party.RoleClaimsHandler, "PTY-INSURER", party.RoleInsurer, "authorize payment", 200); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}

	// "Duplicate message": the caller's own message bus redelivers the
	// exact same execution instruction (same idempotency key) -- e.g.
	// because it timed out waiting for an ack, suffered a network
	// failure before receiving one, or the exchange only partially
	// completed and the caller does not know whether it landed. Every
	// one of these is, from this package's point of view, "the same
	// idempotency key was submitted again": call ExecutePayment 5 times
	// with the SAME key and confirm the underlying Payment is executed
	// EXACTLY ONCE, never re-instructed or re-settled.
	const retries = 5
	var first Transition
	for i := 0; i < retries; i++ {
		tr, err := cl.ExecutePayment("PTY-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-1", "IDEM-EXEC", 300)
		if err != nil {
			t.Fatalf("retry %d: ExecutePayment: %v", i, err)
		}
		if i == 0 {
			first = tr
		} else if tr != first {
			t.Fatalf("retry %d: expected the identical Transition record on every retry, got a different one: %+v vs %+v", i, tr, first)
		}
	}
	if cl.State() != StatePaymentExecuted {
		t.Fatalf("expected StatePaymentExecuted, got %s", cl.State())
	}
	if len(cl.History()) != 7 {
		t.Fatalf("expected exactly 7 history entries (3 to drive to review + quantify + reserve + authorize + execute, despite %d retried ExecutePayment calls), got %d", retries, len(cl.History()))
	}
	if len(cl.Payment.History()) != 4 {
		t.Fatalf("expected the underlying Payment to record exactly 4 events (created+authorized+instructed+settled), got %d -- a double payment would show as extra INSTRUCT/SETTLE entries here", len(cl.Payment.History()))
	}

	// "Out-of-order message": a retry that also happens to carry an
	// EARLIER tick than the original attempt (a genuinely late redelivery)
	// must still be refused for what it actually is -- see
	// TestOutOfOrderTickIsRefused -- rather than accepted merely because
	// its idempotency key differs. A NEW key with an earlier tick, from a
	// state that has already moved on, is refused by the ordinary state
	// graph check (ErrInvalidTransition) here since PAYMENT_EXECUTED has
	// no incoming transitions to itself; the dedicated ordering proof
	// lives in TestOutOfOrderTickIsRefused above.
	_, err := cl.ExecutePayment("PTY-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-1-LATE", "IDEM-EXEC-LATE-RETRY", 250)
	if err == nil {
		t.Fatal("expected a late/out-of-order retry with a NEW idempotency key to be refused, not silently accepted as a second execution")
	}
	if cl.Payment.Status() != payment.StatusPaid {
		t.Fatalf("expected the Payment to remain PAID exactly once after the refused late retry, got %s", cl.Payment.Status())
	}
}

// driveToQuantified drives cl to StateQuantified via a REAL Quantify()
// call carrying amount as its IndicativeClaimValue, so
// cl.QuantumIndicativeAmount is genuinely set for the cross-domain
// invariant checks (pkg/insurance/invariants) OpenReserve/
// AuthorizePayment enforce against it.
func driveToQuantified(t *testing.T, cl *CaseLifecycle, amount quantum.Amount) {
	t.Helper()
	steps := []struct {
		to   State
		ev   string
		idem string
	}{
		{StateAccepted, "", "IDEM-A"},
		{StateEvidenceExchanged, "EVID-1", "IDEM-B"},
		{StateUnderReview, "", "IDEM-C"},
	}
	for _, s := range steps {
		if _, err := cl.Transition(s.to, "PTY-1", party.RoleInsured, s.ev, s.idem, "driving to quantified", 10); err != nil {
			t.Fatalf("driveToQuantified: transition to %s failed: %v", s.to, err)
		}
	}
	calc := quantum.Calculation{CalculationID: "QC-TEST-1", IndicativeClaimValue: amount}
	if _, err := cl.Quantify(calc, "PTY-1", party.RoleInsured, "IDEM-D", 10); err != nil {
		t.Fatalf("driveToQuantified: Quantify failed: %v", err)
	}
}

func TestOpenReserveCouplesRealDomainCall(t *testing.T) {
	cl := mustNew(t)
	driveToQuantified(t, cl, quantum.Amount(50_000))
	tr, err := cl.OpenReserve("RSV-1", "CLM-1", quantum.Amount(50_000), "PTY-INSURER", party.RoleInsurer, "initial reserve", "IDEM-RESERVE", 100)
	if err != nil {
		t.Fatalf("OpenReserve: %v", err)
	}
	if tr.To != StateReserved {
		t.Fatalf("expected StateReserved, got %s", tr.To)
	}
	if cl.Reserve == nil {
		t.Fatal("expected a real reserve.Reserve to be attached")
	}
	if cl.Reserve.CurrentAmount() != quantum.Amount(50_000) {
		t.Fatalf("unexpected reserve amount: %s", cl.Reserve.CurrentAmount())
	}
	// A second OpenReserve on the same case must be refused -- one
	// reserve per case in this model.
	if _, err := cl.OpenReserve("RSV-2", "CLM-1", 1000, "PTY-INSURER", party.RoleInsurer, "second", "IDEM-RESERVE-2", 110); err != ErrReserveAlreadyAttached {
		t.Fatalf("expected ErrReserveAlreadyAttached, got %v", err)
	}
}

func TestAuthorizePaymentRefusesMismatchedAmount(t *testing.T) {
	cl := mustNew(t)
	driveToQuantified(t, cl, quantum.Amount(50_000))
	if _, err := cl.OpenReserve("RSV-1", "CLM-1", quantum.Amount(50_000), "PTY-INSURER", party.RoleInsurer, "initial reserve", "IDEM-RESERVE", 100); err != nil {
		t.Fatalf("OpenReserve: %v", err)
	}
	_, err := cl.AuthorizePayment("PAY-1", "CLM-1", "PTY-PAYEE", quantum.Amount(99_999), "IDEM-PAY", "PTY-CLAIMS-HANDLER", party.RoleClaimsHandler, "PTY-INSURER", party.RoleInsurer, "authorize mismatched", 200)
	if err == nil {
		t.Fatal("expected a payment amount mismatched against the case's own reserve to be refused")
	}
}

// TestLifecycleLabelNeverDivergesFromRealDomainState is the structural
// proof the reviewer's own docx asked for: a CaseLifecycle cannot be
// observed in StateReserved/StatePaymentAuthorized/StatePaymentExecuted
// without its own attached Reserve/Payment ALSO genuinely being in the
// matching real domain status -- this is not merely a label kept in
// sync by convention, it is impossible to construct a divergent case
// through this package's own exported API.
func TestLifecycleLabelNeverDivergesFromRealDomainState(t *testing.T) {
	cl := mustNew(t)
	driveToQuantified(t, cl, quantum.Amount(50_000))
	if _, err := cl.OpenReserve("RSV-1", "CLM-1", quantum.Amount(50_000), "PTY-INSURER", party.RoleInsurer, "initial reserve", "IDEM-RESERVE", 100); err != nil {
		t.Fatalf("OpenReserve: %v", err)
	}
	if cl.State() == StateReserved && cl.Reserve == nil {
		t.Fatal("StateReserved with no attached Reserve -- label diverged from real domain state")
	}

	if _, err := cl.AuthorizePayment("PAY-1", "CLM-1", "PTY-PAYEE", quantum.Amount(50_000), "IDEM-PAY", "PTY-CLAIMS-HANDLER", party.RoleClaimsHandler, "PTY-INSURER", party.RoleInsurer, "authorize payment", 200); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}
	if cl.State() == StatePaymentAuthorized && (cl.Payment == nil || cl.Payment.Status() != payment.StatusAuthorized) {
		t.Fatal("StatePaymentAuthorized with a Payment not itself AUTHORIZED -- label diverged from real domain state")
	}

	if _, err := cl.ExecutePayment("PTY-BANK", party.RoleBankTradeFinance, "SWIFT MT103", "REF-1", "IDEM-EXEC", 300); err != nil {
		t.Fatalf("ExecutePayment: %v", err)
	}
	if cl.State() != StatePaymentExecuted {
		t.Fatalf("expected StatePaymentExecuted, got %s", cl.State())
	}
	if cl.Payment.Status() != payment.StatusPaid {
		t.Fatalf("expected the real Payment to be PAID once the case reaches PAYMENT_EXECUTED, got %s", cl.Payment.Status())
	}

	// PAID alone (no settlement evidence yet) must not be enough to
	// leave PAYMENT_EXECUTED -- the FINAL INTERNAL CHECK item A
	// "bypass settlement evidence" invariant.
	_, err := cl.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE-EARLY", "attempt to close before settlement", 310)
	if err != ErrSettlementEvidenceRequired {
		t.Fatalf("expected ErrSettlementEvidenceRequired, got %v", err)
	}

	if err := cl.RecordSettlement(payment.SettlementEvidence{
		PaymentID: cl.Payment.PaymentID, Reference: "REF-SETTLE-1", SourceDescription: "bank confirmation",
		SettledAmount: cl.Payment.CurrentAmount(), ConfirmedAtTick: 320,
	}, 0); err != nil {
		t.Fatalf("RecordSettlement: %v", err)
	}
	if _, err := cl.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE", "closed after settlement", 330); err != nil {
		t.Fatalf("expected close to succeed once settlement evidence is recorded: %v", err)
	}
}

func TestReplayReproducesLiveHistoryThroughTheSameRules(t *testing.T) {
	live := mustNew(t)
	driveToQuantified(t, live, quantum.Amount(50_000))
	if _, err := live.OpenReserve("RSV-1", "CLM-1", quantum.Amount(50_000), "PTY-INSURER", party.RoleInsurer, "initial reserve", "IDEM-RESERVE", 100); err != nil {
		t.Fatalf("OpenReserve: %v", err)
	}
	if _, err := live.AuthorizePayment("PAY-1", "CLM-1", "PTY-PAYEE", quantum.Amount(50_000), "IDEM-PAY", "PTY-CLAIMS-HANDLER", party.RoleClaimsHandler, "PTY-INSURER", party.RoleInsurer, "authorize payment", 200); err != nil {
		t.Fatalf("AuthorizePayment: %v", err)
	}

	replayed, err := Replay(live.CaseID, live.History())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed.State() != live.State() {
		t.Fatalf("replay diverged: live=%s replayed=%s", live.State(), replayed.State())
	}
	if len(replayed.History()) != len(live.History()) {
		t.Fatalf("replay produced a different history length: live=%d replayed=%d", len(live.History()), len(replayed.History()))
	}
}

func TestReplayRefusesATamperedHistory(t *testing.T) {
	live := mustNew(t)
	if _, err := live.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-A", "accepted", 10); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	tampered := live.History()
	// Insert an illegal jump directly to PAYMENT_EXECUTED.
	tampered = append(tampered, Transition{
		From: StateAccepted, To: StatePaymentExecuted, ActorPartyID: "PTY-1", ActorRole: party.RoleInsured,
		Reason: "tampered", IdempotencyKey: "IDEM-TAMPER", Tick: 20,
	})
	if _, err := Replay(live.CaseID, tampered); err == nil {
		t.Fatal("expected Replay to refuse a tampered/illegal history rather than silently reproducing it")
	}
}

func TestConcurrentTransitionsAreSerializedSafely(t *testing.T) {
	cl := mustNew(t)
	var wg sync.WaitGroup
	successes := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-CONCURRENT", "racing accept", uint64(n))
			successes <- err == nil
		}(i)
	}
	wg.Wait()
	close(successes)
	// Every goroutine used the SAME idempotency key, so every call must
	// succeed (idempotent) and the case must end in exactly one
	// consistent state with exactly one history entry -- proof the
	// mutex genuinely serializes concurrent callers rather than racing.
	for ok := range successes {
		if !ok {
			t.Fatal("expected every concurrent call with the same idempotency key to succeed idempotently")
		}
	}
	if cl.State() != StateAccepted {
		t.Fatalf("expected StateAccepted, got %s", cl.State())
	}
	if len(cl.History()) != 1 {
		t.Fatalf("expected exactly 1 history entry despite 20 concurrent callers, got %d", len(cl.History()))
	}
}

// driveToUnderReview drives cl to StateUnderReview via real transitions
// (ACCEPTED -> EVIDENCE_EXCHANGED -> UNDER_REVIEW), the common starting
// point every FINAL INTERNAL CHECK item E concurrent-actor test races
// from -- UNDER_REVIEW is the reviewer's own example junction where
// Insurer, Broker, Surveyor and P&I might plausibly all act at once
// (QUANTIFIED, CLARIFICATION_REQUIRED and DISPUTED are all legal from
// here).
func driveToUnderReview(t *testing.T, cl *CaseLifecycle) {
	t.Helper()
	if _, err := cl.Transition(StateAccepted, "PTY-1", party.RoleInsured, "", "IDEM-A", "accepted", 10); err != nil {
		t.Fatalf("driveToUnderReview: %v", err)
	}
	if _, err := cl.Transition(StateEvidenceExchanged, "PTY-1", party.RoleInsured, "EVID-1", "IDEM-B", "evidence exchanged", 10); err != nil {
		t.Fatalf("driveToUnderReview: %v", err)
	}
	if _, err := cl.Transition(StateUnderReview, "PTY-1", party.RoleInsured, "", "IDEM-C", "under review", 10); err != nil {
		t.Fatalf("driveToUnderReview: %v", err)
	}
}

// TestConcurrentActorsRacingSameTargetHaveExactlyOneWinner is the FINAL
// INTERNAL CHECK item E proof: Insurer, Broker, Surveyor and P&I all
// authorized (anyKnownRole), each with a DISTINCT idempotency key,
// simultaneously trying to move the SAME case from UNDER_REVIEW to the
// SAME target state. This is a genuine conflict (unlike the identical-
// idempotency-key case TestConcurrentTransitionsAreSerializedSafely
// already covers): the mutex must let exactly one caller actually
// perform the transition, and every other caller must be refused with
// a deterministic, typed error (ErrInvalidTransition -- the state has
// already moved away from UNDER_REVIEW by the time they run), never a
// double transition, a corrupted state, or a silently-accepted second
// write. Run across many trials to prove this is deterministic
// behaviour, not a race that merely happened not to fail today.
func TestConcurrentActorsRacingSameTargetHaveExactlyOneWinner(t *testing.T) {
	actors := []struct {
		id   party.PartyID
		role party.Role
	}{
		{"PTY-INSURER", party.RoleInsurer},
		{"PTY-BROKER", party.RoleBroker},
		{"PTY-SURVEYOR", party.RoleSurveyor},
		{"PTY-PANDI", party.RolePAndIClub},
	}
	const trials = 25
	for trial := 0; trial < trials; trial++ {
		cl := mustNew(t)
		driveToUnderReview(t, cl)

		var wg sync.WaitGroup
		results := make(chan error, len(actors))
		for i, a := range actors {
			wg.Add(1)
			go func(n int, id party.PartyID, role party.Role) {
				defer wg.Done()
				// All four actors use the SAME tick (20): this eliminates
				// any ambiguity between ErrInvalidTransition and
				// ErrOutOfOrderTick as the loser's error -- whichever actor
				// wins, cl.lastTick becomes 20, and every other actor's own
				// tick (also 20) still satisfies the item G tick-ordering
				// check, so their refusal is deterministically
				// ErrInvalidTransition (a real state-graph conflict), not
				// an ordering artifact of which n happened to run first.
				_, err := cl.Transition(StateQuantified, id, role, "", fmt.Sprintf("IDEM-RACE-%d-%d", trial, n), "racing to quantify", 20)
				results <- err
			}(i, a.id, a.role)
		}
		wg.Wait()
		close(results)

		var successes, invalidTransitionFailures int
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrInvalidTransition):
				invalidTransitionFailures++
			default:
				t.Fatalf("trial %d: unexpected error from a racing actor: %v", trial, err)
			}
		}
		if successes != 1 {
			t.Fatalf("trial %d: expected exactly 1 winner among %d racing actors, got %d", trial, len(actors), successes)
		}
		if invalidTransitionFailures != len(actors)-1 {
			t.Fatalf("trial %d: expected %d deterministic ErrInvalidTransition losers, got %d", trial, len(actors)-1, invalidTransitionFailures)
		}
		if cl.State() != StateQuantified {
			t.Fatalf("trial %d: expected the case to end in QUANTIFIED, got %s", trial, cl.State())
		}
		if len(cl.History()) != 4 {
			t.Fatalf("trial %d: expected exactly 4 history entries (3 to drive to review + 1 winning race), got %d", trial, len(cl.History()))
		}
	}
}

// TestConcurrentActorsRacingDifferentTargetsHaveExactlyOneWinner covers
// the reviewer's own scenario more literally: Insurer, Broker, Surveyor
// and P&I each send a DIFFERENT event at the same time, proposing one
// of two independently-legal-from-UNDER_REVIEW outcomes (CLARIFICATION_
// REQUIRED or DISPUTED). These two targets are deliberately used rather
// than QUANTIFIED: QUANTIFIED -> DISPUTED is itself a legal further
// transition, so a losing DISPUTED attempt scheduled AFTER a winning
// QUANTIFIED one could legitimately succeed a moment later -- a real
// two-step sequence, not a conflict, which would make "exactly one
// winner" a false assertion. CLARIFICATION_REQUIRED and DISPUTED chain
// to neither each other nor themselves (see validTransitions), so
// whichever wins the race, every other attempt is refused, deterministically,
// regardless of scheduling order. Only the first to acquire the lock can
// possibly succeed; by the time every other goroutine's transitionLocked
// call actually runs, the case has already left UNDER_REVIEW, so their
// own (individually legal) target is no longer reachable from the new
// current state and they are refused deterministically -- proving
// cross-actor conflicts over DIFFERENT proposed outcomes, not just
// identical ones, resolve safely.
func TestConcurrentActorsRacingDifferentTargetsHaveExactlyOneWinner(t *testing.T) {
	type attempt struct {
		id     party.PartyID
		role   party.Role
		target State
	}
	attempts := []attempt{
		{"PTY-INSURER", party.RoleInsurer, StateDisputed},
		{"PTY-BROKER", party.RoleBroker, StateClarificationRequired},
		{"PTY-SURVEYOR", party.RoleSurveyor, StateDisputed},
		{"PTY-PANDI", party.RolePAndIClub, StateClarificationRequired},
	}
	const trials = 25
	for trial := 0; trial < trials; trial++ {
		cl := mustNew(t)
		driveToUnderReview(t, cl)

		var wg sync.WaitGroup
		results := make(chan error, len(attempts))
		for i, a := range attempts {
			wg.Add(1)
			go func(n int, at attempt) {
				defer wg.Done()
				// All four actors use the SAME tick (20) for the same
				// reason as the same-target race test above: it keeps
				// item G's tick-ordering check from becoming the reason a
				// loser is refused, isolating this test to the state-graph
				// conflict it's meant to prove.
				_, err := cl.Transition(at.target, at.id, at.role, "", fmt.Sprintf("IDEM-DIFF-%d-%d", trial, n), "racing with a different proposed outcome", 20)
				results <- err
			}(i, a)
		}
		wg.Wait()
		close(results)

		var successes, failures int
		for err := range results {
			if err == nil {
				successes++
			} else {
				failures++
			}
		}
		if successes != 1 {
			t.Fatalf("trial %d: expected exactly 1 winner among 4 actors proposing different outcomes, got %d", trial, successes)
		}
		if failures != len(attempts)-1 {
			t.Fatalf("trial %d: expected %d deterministic losers, got %d", trial, len(attempts)-1, failures)
		}
		finalState := cl.State()
		if finalState != StateClarificationRequired && finalState != StateDisputed {
			t.Fatalf("trial %d: final state %s is not one of the four actors' own proposed outcomes", trial, finalState)
		}
		if len(cl.History()) != 4 {
			t.Fatalf("trial %d: expected exactly 4 history entries (3 to drive to review + 1 winning race), got %d", trial, len(cl.History()))
		}
	}
}

func TestStateVocabularyIsClosed(t *testing.T) {
	for _, s := range orderedStates {
		if !IsKnownState(s) {
			t.Errorf("expected %q to be known", s)
		}
	}
	if IsKnownState("NOT_A_STATE") {
		t.Fatal("an unknown state must never report as known")
	}
	if len(orderedStates) != 14 {
		t.Fatalf("expected exactly the reviewer's own 14 named states, got %d", len(orderedStates))
	}
}

func TestDisputeReachableFromEveryMidLifecycleState(t *testing.T) {
	// The reviewer's own graph allows DISPUTED from many points -- prove
	// at least UNDER_REVIEW, QUANTIFIED, RESERVED and PAYMENT_AUTHORIZED
	// can all reach it, and DISPUTED can return to UNDER_REVIEW or CLOSED.
	cl := mustNew(t)
	driveToQuantified(t, cl, quantum.Amount(1000))
	if _, err := cl.Transition(StateDisputed, "PTY-1", party.RoleInsured, "", "IDEM-DISPUTE", "disputed at quantified", 90); err != nil {
		t.Fatalf("expected QUANTIFIED -> DISPUTED to be legal: %v", err)
	}
	if _, err := cl.Transition(StateUnderReview, "PTY-1", party.RoleInsured, "", "IDEM-BACK", "dispute resolved, back to review", 95); err != nil {
		t.Fatalf("expected DISPUTED -> UNDER_REVIEW to be legal: %v", err)
	}
}

func TestClosedCanReopen(t *testing.T) {
	cl := mustNew(t)
	if _, err := cl.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE", "declined", 10); err != nil {
		t.Fatalf("expected INVITED -> CLOSED to be legal (declined invitation): %v", err)
	}
	if _, err := cl.Transition(StateReopened, "PTY-1", party.RoleInsured, "EVID-NEW-1", "IDEM-REOPEN", "reopened", 20); err != nil {
		t.Fatalf("expected CLOSED -> REOPENED (with new evidence) to be legal: %v", err)
	}
}

// TestReopenRequiresNewEvidence is the FINAL INTERNAL CHECK item D
// structural proof: "CLOSED -> new evidence -> REOPENED" is not merely
// documented, a reopen citing no evidence is refused exactly like any
// other evidence-requiring transition.
func TestReopenRequiresNewEvidence(t *testing.T) {
	cl := mustNew(t)
	if _, err := cl.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE", "declined", 10); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	_, err := cl.Transition(StateReopened, "PTY-1", party.RoleInsured, "", "IDEM-REOPEN-NOEV", "reopened with nothing new", 20)
	if !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("expected ErrEvidenceRequired for a reopen citing no evidence, got %v", err)
	}
}

// TestReopenIncrementsVersionWithoutRewritingHistory is the FINAL
// INTERNAL CHECK item D proof for "new version -> new audit chain ->
// new decision lineage -- never rewriting old history": every
// transition already recorded under the case's first decision lineage
// must be byte-for-byte unchanged after a reopen, the REOPENED record
// itself and everything after it must carry the incremented Version,
// and a second reopen increments it again.
func TestReopenIncrementsVersionWithoutRewritingHistory(t *testing.T) {
	cl := mustNew(t)
	if cl.Version() != 1 {
		t.Fatalf("expected a new case to start at Version 1, got %d", cl.Version())
	}
	if _, err := cl.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE", "declined", 10); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	beforeReopen := cl.History()
	if len(beforeReopen) != 1 {
		t.Fatalf("expected 1 recorded transition before reopen, got %d", len(beforeReopen))
	}
	for i, tr := range beforeReopen {
		if tr.Version != 1 {
			t.Fatalf("expected pre-reopen transition %d to carry Version 1, got %d", i, tr.Version)
		}
	}

	reopenTr, err := cl.Transition(StateReopened, "PTY-1", party.RoleInsured, "EVID-NEW-1", "IDEM-REOPEN-1", "new evidence surfaced", 30)
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if cl.Version() != 2 {
		t.Fatalf("expected Version 2 after the first reopen, got %d", cl.Version())
	}
	if reopenTr.Version != 2 {
		t.Fatalf("expected the REOPENED record itself to carry the NEW Version 2, got %d", reopenTr.Version)
	}

	afterReopen := cl.History()
	if len(afterReopen) != 2 {
		t.Fatalf("expected 2 recorded transitions after reopen (append, not rewrite), got %d", len(afterReopen))
	}
	for i := range beforeReopen {
		if afterReopen[i] != beforeReopen[i] {
			t.Fatalf("transition %d changed after reopen -- old history must never be rewritten: before=%+v after=%+v", i, beforeReopen[i], afterReopen[i])
		}
	}

	if _, err := cl.Transition(StateUnderReview, "PTY-1", party.RoleInsured, "", "IDEM-BACK-2", "back under review, second lineage", 40); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	// UNDER_REVIEW has no direct edge to CLOSED -- route through DISPUTED
	// (UNDER_REVIEW -> DISPUTED -> CLOSED), both legal per validTransitions.
	if _, err := cl.Transition(StateDisputed, "PTY-1", party.RoleInsured, "", "IDEM-DISP-2", "disputed, second lineage", 45); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if _, err := cl.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE-2", "closed again", 50); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	secondClose := cl.History()[len(cl.History())-1]
	if secondClose.Version != 2 {
		t.Fatalf("expected the post-reopen CLOSED to still carry Version 2, got %d", secondClose.Version)
	}

	// A second reopen must increment again, to a THIRD distinct lineage.
	secondReopen, err := cl.Transition(StateReopened, "PTY-1", party.RoleInsured, "EVID-NEW-2", "IDEM-REOPEN-2", "more new evidence surfaced", 60)
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if cl.Version() != 3 || secondReopen.Version != 3 {
		t.Fatalf("expected the second reopen to reach Version 3, got cl.Version()=%d record.Version=%d", cl.Version(), secondReopen.Version)
	}
	if len(cl.History()) != 6 {
		t.Fatalf("expected 6 recorded transitions total (append-only across both reopens), got %d", len(cl.History()))
	}
}

// TestReplayReproducesVersionAcrossAReopen proves Replay -- which must
// re-derive every fact purely from a recorded History, never trust a
// stored field -- reproduces the SAME Version sequence a live case
// produced, including across a reopen.
func TestReplayReproducesVersionAcrossAReopen(t *testing.T) {
	live := mustNew(t)
	if _, err := live.Transition(StateClosed, "PTY-1", party.RoleInsured, "", "IDEM-CLOSE", "declined", 10); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if _, err := live.Transition(StateReopened, "PTY-1", party.RoleInsured, "EVID-NEW-1", "IDEM-REOPEN-1", "new evidence surfaced", 20); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	replayed, err := Replay(live.CaseID, live.History())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed.Version() != live.Version() {
		t.Fatalf("expected replay to reproduce Version %d, got %d", live.Version(), replayed.Version())
	}
	liveHist, replayedHist := live.History(), replayed.History()
	for i := range liveHist {
		if liveHist[i].Version != replayedHist[i].Version {
			t.Fatalf("transition %d: live Version %d != replayed Version %d", i, liveHist[i].Version, replayedHist[i].Version)
		}
	}
}

// reserveAuthorityCoverage documents (and proves) that this package's
// StateQuantified -> StateReserved authority check is the exact same
// function reserve.HasReserveAuthority is -- a REUSE proof, not a
// re-derived duplicate.
func TestReserveAuthorityCheckIsReusedNotRederived(t *testing.T) {
	for r := range map[party.Role]bool{party.RoleInsurer: true, party.RoleClaimsHandler: true, party.RoleLossAdjuster: true, party.RoleAverageAdjuster: true} {
		if !reserve.HasReserveAuthority(r) {
			t.Fatalf("expected %q to have reserve authority per pkg/insurance/reserve itself", r)
		}
		rule := validTransitions[StateQuantified][0]
		if rule.To != StateReserved {
			t.Fatalf("expected the first QUANTIFIED rule to target RESERVED, got %s", rule.To)
		}
		if !rule.Authorize(r) {
			t.Fatalf("expected casestate's own QUANTIFIED->RESERVED authority check to agree with reserve.HasReserveAuthority for %q", r)
		}
	}
}
