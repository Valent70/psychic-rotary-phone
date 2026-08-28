// Package invariants implements FINAL INTERNAL CHECK item B
// ("Cross-domain invariant"): the reviewer's own four named numeric
// relationships that must hold ACROSS pkg/insurance/quantum,
// pkg/insurance/reserve, pkg/insurance/payment and pkg/insurance/recovery,
// which no single one of those packages can check on its own since each
// only ever sees its own figure:
//
//	Payment amount      <= authorized quantum
//	Reserve amount      == canonical quantum basis
//	Settlement amount   == payment amount +/- documented adjustment
//	Recovery            <= legally/contractually recoverable amount
//
// This package holds no state and performs no I/O — every function is a
// pure comparison over quantum.Amount values already computed elsewhere,
// matching this program's own "never a second engine, only a real check"
// discipline (Final Design §39). Callers (pkg/insurance/casestate,
// pkg/insurance/recovery call sites, pkg/insurance/ingestion) invoke
// these at the point a cross-domain figure is actually accepted, so a
// violation is refused AT THE BOUNDARY rather than merely observable
// after the fact.
package invariants

import (
	"errors"
	"fmt"

	"veriqo/pkg/insurance/quantum"
)

var (
	// ErrPaymentExceedsQuantum: a payment amount exceeded the quantum
	// figure it was authorized against.
	ErrPaymentExceedsQuantum = errors.New("invariants: payment amount exceeds the authorized quantum figure")
	// ErrReserveNotAtQuantumBasis: a reserve amount does not equal the
	// canonical quantum figure it was opened from.
	ErrReserveNotAtQuantumBasis = errors.New("invariants: reserve amount does not equal its own canonical quantum basis")
	// ErrSettlementAdjustmentExceeded: a settled amount differs from the
	// instructed payment amount by more than the documented adjustment
	// allowance.
	ErrSettlementAdjustmentExceeded = errors.New("invariants: settlement amount differs from the payment amount by more than the documented adjustment allowance")
	// ErrRecoveryExceedsRecoverable: a recovery target's own pursued
	// amount exceeds what is legally/contractually recoverable.
	ErrRecoveryExceedsRecoverable = errors.New("invariants: recovery amount exceeds the legally/contractually recoverable amount")
	// ErrNegativeAmount is returned by every check below for any
	// negative operand — none of these four relationships is meaningful
	// for a negative figure, and this package refuses to guess an
	// intended sign.
	ErrNegativeAmount = errors.New("invariants: amount must be >= 0")
)

// CheckPaymentWithinQuantum enforces "Payment amount <= authorized
// quantum": paymentAmount must never exceed authorizedQuantum. A
// payment strictly LESS than the quantum figure is valid (a partial
// payment) — only exceeding it is a violation.
func CheckPaymentWithinQuantum(paymentAmount, authorizedQuantum quantum.Amount) error {
	if paymentAmount < 0 || authorizedQuantum < 0 {
		return ErrNegativeAmount
	}
	if paymentAmount > authorizedQuantum {
		return fmt.Errorf("%w: payment=%s authorized_quantum=%s", ErrPaymentExceedsQuantum, paymentAmount, authorizedQuantum)
	}
	return nil
}

// CheckReserveMatchesQuantumBasis enforces "Reserve amount == canonical
// quantum basis": a reserve must be opened at EXACTLY the quantum
// figure it is founded on — unlike payment (which may be partial), a
// reserve that silently drifted from its own founding figure would be
// exactly the "one gap, two numbers" drift this program's own governing
// rules forbid.
func CheckReserveMatchesQuantumBasis(reserveAmount, quantumBasis quantum.Amount) error {
	if reserveAmount < 0 || quantumBasis < 0 {
		return ErrNegativeAmount
	}
	if reserveAmount != quantumBasis {
		return fmt.Errorf("%w: reserve=%s quantum_basis=%s", ErrReserveNotAtQuantumBasis, reserveAmount, quantumBasis)
	}
	return nil
}

// CheckSettlementWithinAdjustment enforces "Settlement amount == payment
// amount +/- documented adjustment": the absolute difference between
// settledAmount and paymentAmount must not exceed maxAdjustment.
// maxAdjustment == 0 means EXACT match is required (no adjustment
// documented) — this is the honest default, matching this codebase's
// "an undocumented allowance is not an allowance" discipline; a caller
// with a genuine documented adjustment (an FX rounding policy, a
// disclosed fee schedule) passes it explicitly.
func CheckSettlementWithinAdjustment(paymentAmount, settledAmount, maxAdjustment quantum.Amount) error {
	if paymentAmount < 0 || settledAmount < 0 || maxAdjustment < 0 {
		return ErrNegativeAmount
	}
	delta := paymentAmount - settledAmount
	if delta < 0 {
		delta = -delta
	}
	if delta > maxAdjustment {
		return fmt.Errorf("%w: payment=%s settled=%s delta=%s max_adjustment=%s",
			ErrSettlementAdjustmentExceeded, paymentAmount, settledAmount, delta, maxAdjustment)
	}
	return nil
}

// CheckRecoveryWithinRecoverable enforces "Recovery <= legally/
// contractually recoverable amount": the amount actually pursued or
// recovered must never exceed the amount a real legal/contractual basis
// (e.g. recovery.Basis, the underlying bailee-liability or subrogation
// theory) supports.
func CheckRecoveryWithinRecoverable(recoveryAmount, recoverableAmount quantum.Amount) error {
	if recoveryAmount < 0 || recoverableAmount < 0 {
		return ErrNegativeAmount
	}
	if recoveryAmount > recoverableAmount {
		return fmt.Errorf("%w: recovery=%s recoverable=%s", ErrRecoveryExceedsRecoverable, recoveryAmount, recoverableAmount)
	}
	return nil
}
