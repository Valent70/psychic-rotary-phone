package lifecycle

import (
	"errors"
	"testing"
)

func activeModel(t *testing.T, r *Registry, id, ver string, tick uint64) {
	t.Helper()
	if _, err := r.RegisterModel(Model{ModelID: id, Version: ver, Type: "risk"}, "eng", tick); err != nil {
		t.Fatal(err)
	}
	key := id + "@" + ver
	if _, err := r.TransitionModel(key, ModelValidated, "eng", "", "", tick, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCalibration(key, "cal-"+ver); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel(key, ModelCalibrated, "eng", "", "", tick, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel(key, ModelApproved, "eng", "risk-officer", "", tick, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel(key, ModelActive, "eng", "risk-officer", "", tick, nil); err != nil {
		t.Fatal(err)
	}
}

func activeSource(t *testing.T, r *Registry, id, ver string, tick uint64) {
	t.Helper()
	if _, err := r.RegisterSource(Source{SourceID: id, Version: ver, Authority: "registry",
		Provider: "p", EvidenceClass: "AIS", Reliability: 0.8}, "eng", tick); err != nil {
		t.Fatal(err)
	}
	key := id + "@" + ver
	for _, s := range []SourceState{SourceValidated, SourceTrusted, SourceActive} {
		if _, err := r.TransitionSource(key, s, "eng", "data-officer", "", tick, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelCannotSkipStates(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterModel(Model{ModelID: "m", Version: "1", Type: "risk"}, "eng", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel("m@1", ModelActive, "eng", "officer", "", 1, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("DRAFT -> ACTIVE must be illegal, got %v", err)
	}
}

func TestApprovalRequiresCalibrationArtifact(t *testing.T) {
	r := NewRegistry()
	_, _ = r.RegisterModel(Model{ModelID: "m", Version: "1"}, "eng", 1)
	_, _ = r.TransitionModel("m@1", ModelValidated, "eng", "", "", 1, nil)
	_, _ = r.TransitionModel("m@1", ModelCalibrated, "eng", "", "", 1, nil)
	if _, err := r.TransitionModel("m@1", ModelApproved, "eng", "officer", "", 1, nil); !errors.Is(err, ErrCalibrationRequired) {
		t.Fatalf("approval without calibration must be refused, got %v", err)
	}
}

func TestApprovalRequiresNamedApprover(t *testing.T) {
	r := NewRegistry()
	_, _ = r.RegisterModel(Model{ModelID: "m", Version: "1"}, "eng", 1)
	_, _ = r.TransitionModel("m@1", ModelValidated, "eng", "", "", 1, nil)
	_ = r.SetCalibration("m@1", "cal")
	_, _ = r.TransitionModel("m@1", ModelCalibrated, "eng", "", "", 1, nil)
	if _, err := r.TransitionModel("m@1", ModelApproved, "eng", "", "", 1, nil); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approver requirement, got %v", err)
	}
}

func TestRecalibratingAnActiveModelIsRefused(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "m", "1", 1)
	if err := r.SetCalibration("m@1", "cal-new"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("silently recalibrating an active model must be refused, got %v", err)
	}
}

func TestActivatingANewVersionDeprecatesThePrevious(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "m", "1", 1)
	activeModel(t, r, "m", "2", 5)
	old, _ := r.Model("m@1")
	if old.State != ModelDeprecated {
		t.Fatalf("previous version must be deprecated, got %s", old.State)
	}
	if old.EffectiveTo != 5 {
		t.Fatalf("previous version must be closed at the activation tick, got %d", old.EffectiveTo)
	}
	b := r.BindingAt(5)
	if len(b.Models) != 1 || b.Models[0] != "m@2" {
		t.Fatalf("exactly one version may be active, got %v", b.Models)
	}
}

func TestRetiredModelIsTerminal(t *testing.T) {
	r := NewRegistry()
	_, _ = r.RegisterModel(Model{ModelID: "m", Version: "1"}, "eng", 1)
	if _, err := r.TransitionModel("m@1", ModelRetired, "eng", "", "eol", 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionModel("m@1", ModelValidated, "eng", "", "", 2, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("RETIRED must be terminal, got %v", err)
	}
}

func TestRevokedSourceIsTerminalAndCannotBeReinstated(t *testing.T) {
	r := NewRegistry()
	activeSource(t, r, "ais-a", "1", 1)
	if _, err := r.TransitionSource("ais-a@1", SourceRevoked, "eng", "", "forged feed", 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionSource("ais-a@1", SourceActive, "eng", "officer", "oops", 3, nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revocation must be terminal, got %v", err)
	}
}

func TestSourceActivationRequiresApprover(t *testing.T) {
	r := NewRegistry()
	_, _ = r.RegisterSource(Source{SourceID: "s", Version: "1"}, "eng", 1)
	_, _ = r.TransitionSource("s@1", SourceValidated, "eng", "", "", 1, nil)
	_, _ = r.TransitionSource("s@1", SourceTrusted, "eng", "", "", 1, nil)
	if _, err := r.TransitionSource("s@1", SourceActive, "eng", "", "", 1, nil); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected approver requirement, got %v", err)
	}
}

func TestDegradedSourceCanRecoverButSuspendedMustBeRevalidated(t *testing.T) {
	r := NewRegistry()
	activeSource(t, r, "s", "1", 1)
	if _, err := r.TransitionSource("s@1", SourceDegraded, "eng", "", "drift", 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionSource("s@1", SourceActive, "eng", "officer", "recovered", 3, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionSource("s@1", SourceSuspended, "eng", "", "incident", 4, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionSource("s@1", SourceActive, "eng", "officer", "", 5, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("a suspended source must be revalidated first, got %v", err)
	}
	if _, err := r.TransitionSource("s@1", SourceValidated, "eng", "", "", 5, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateRegistrationRejected(t *testing.T) {
	r := NewRegistry()
	_, _ = r.RegisterModel(Model{ModelID: "m", Version: "1"}, "eng", 1)
	if _, err := r.RegisterModel(Model{ModelID: "m", Version: "1"}, "eng", 2); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}

func TestBackdatedEventRejected(t *testing.T) {
	r := NewRegistry()
	_, _ = r.RegisterModel(Model{ModelID: "m", Version: "1"}, "eng", 10)
	if _, err := r.RegisterModel(Model{ModelID: "m", Version: "2"}, "eng", 5); !errors.Is(err, ErrNonMonotonicTick) {
		t.Fatalf("expected monotonic tick rejection, got %v", err)
	}
}

func TestBindingAtAnswersHistoricallyNotCurrently(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "risk", "1", 1)
	activeSource(t, r, "ais-a", "1", 1)
	atTen := r.BindingAt(10)

	activeModel(t, r, "risk", "2", 20)
	stillAtTen := r.BindingAt(10)

	if atTen.Hash != stillAtTen.Hash {
		t.Fatalf("a later activation changed a historical binding: %v vs %v", atTen, stillAtTen)
	}
	now := r.BindingAt(20)
	if now.Hash == atTen.Hash {
		t.Fatal("the current binding must differ once a new model version is active")
	}
}

func TestReplayUnderADifferentModelVersionIsDetected(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "risk", "1", 1)
	activeSource(t, r, "ais-a", "1", 1)
	committed := r.BindingAt(10)
	if err := r.VerifyBinding(committed); err != nil {
		t.Fatalf("a fresh binding must verify: %v", err)
	}

	// A different registry that happens to run risk@2 at the same tick.
	other := NewRegistry()
	activeModel(t, other, "risk", "2", 1)
	activeSource(t, other, "ais-a", "1", 1)
	if err := other.VerifyBinding(committed); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("replaying under a different model version must be detected, got %v", err)
	}
}

func TestBindingIsSensitiveToSourceVersionToo(t *testing.T) {
	a := NewRegistry()
	activeModel(t, a, "risk", "1", 1)
	activeSource(t, a, "ais", "1", 1)
	b := NewRegistry()
	activeModel(t, b, "risk", "1", 1)
	activeSource(t, b, "ais", "2", 1)
	if b.VerifyBinding(a.BindingAt(5)) == nil {
		t.Fatal("a different source version must invalidate the binding")
	}
}

func TestBindingPinsTheGovernanceLedgerPosition(t *testing.T) {
	a := NewRegistry()
	activeModel(t, a, "risk", "1", 1)
	first := a.BindingAt(5)
	// A governance event that does not change the ACTIVE set — a new
	// draft model. The active set is identical; the ledger head is not.
	if _, err := a.RegisterModel(Model{ModelID: "other", Version: "1"}, "eng", 6); err != nil {
		t.Fatal(err)
	}
	second := a.BindingAt(6)
	if first.Hash == second.Hash {
		t.Fatal("a governance event must move the binding hash even when the active set is unchanged")
	}
}

func TestRollbackReactivatesTheDeclaredTarget(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "risk", "1", 1)
	if _, err := r.RegisterModel(Model{ModelID: "risk", Version: "2", Type: "risk",
		ParentVersion: "1", RollbackTarget: "risk@1"}, "eng", 5); err != nil {
		t.Fatal(err)
	}
	_, _ = r.TransitionModel("risk@2", ModelValidated, "eng", "", "", 5, nil)
	_ = r.SetCalibration("risk@2", "cal-2")
	_, _ = r.TransitionModel("risk@2", ModelCalibrated, "eng", "", "", 5, nil)
	_, _ = r.TransitionModel("risk@2", ModelApproved, "eng", "officer", "", 5, nil)
	_, _ = r.TransitionModel("risk@2", ModelActive, "eng", "officer", "", 5, nil)

	if _, err := r.RollbackModel("risk@2", "sre", "officer", "elevated ECE in production", 9); err != nil {
		t.Fatal(err)
	}
	b := r.BindingAt(9)
	if len(b.Models) != 1 || b.Models[0] != "risk@1" {
		t.Fatalf("rollback should restore risk@1, got %v", b.Models)
	}
}

func TestRollbackWithoutATargetIsRefused(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "risk", "1", 1)
	if _, err := r.RollbackModel("risk@1", "sre", "officer", "", 2); !errors.Is(err, ErrNoRollbackTarget) {
		t.Fatalf("expected no-rollback-target error, got %v", err)
	}
}

func TestLedgerIsTamperEvident(t *testing.T) {
	r := NewRegistry()
	activeModel(t, r, "risk", "1", 1)
	activeSource(t, r, "ais", "1", 1)
	if err := r.VerifyChain(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.ledger[3].Approver = "someone-else"
	r.mu.Unlock()
	if err := r.VerifyChain(); !errors.Is(err, ErrLedgerTampered) {
		t.Fatalf("expected tamper detection, got %v", err)
	}
}

func TestUnknownEntryIsAnErrorNotASilentNoop(t *testing.T) {
	r := NewRegistry()
	if _, err := r.TransitionModel("ghost@1", ModelValidated, "eng", "", "", 1, nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("expected unknown entry, got %v", err)
	}
	if _, err := r.TransitionSource("ghost@1", SourceValidated, "eng", "", "", 1, nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("expected unknown entry, got %v", err)
	}
	if err := r.SetCalibration("ghost@1", "c"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("expected unknown entry, got %v", err)
	}
}
