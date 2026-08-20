package maritime

import "testing"

func mustRegister(t *testing.T, r *Registry, kind EntityKind, key string) Entity {
	t.Helper()
	e, err := r.RegisterEntity(kind, map[string]string{"key": key}, nil, 1)
	if err != nil {
		t.Fatalf("register %s: %v", key, err)
	}
	return e
}

func TestOwnershipGraph_UltimateExcludesIntermediateShell(t *testing.T) {
	r := NewRegistry()
	shell := mustRegister(t, r, KindShellCompany, "SHELL_A")
	owner := mustRegister(t, r, KindBeneficialOwner, "PERSON_X")
	vessel := mustRegister(t, r, KindVessel, "IMO1")

	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(owner.ID, shell.ID, 1.0, OwnershipBeneficial, 2))
	must(t, og.RecordOwnership(shell.ID, vessel.ID, 1.0, OwnershipBeneficial, 3))

	// v7.9 audit §14 fix: UltimateBeneficialOwners must exclude the
	// intermediate SHELL and return only the true ultimate owner
	// (PERSON_X). AllOwnersInChain is the old "everyone in the chain"
	// view.
	ubos := og.UltimateBeneficialOwners(vessel.ID, OwnershipBeneficial, 0.25, 5)
	if len(ubos) != 1 || ubos[0].OwnerID != owner.ID {
		t.Fatalf("expected exactly [PERSON_X] as ultimate owner, got %+v", ubos)
	}
	if ubos[0].EffectivePercent != 1.0 {
		t.Errorf("effective percent = %v, want 1.0", ubos[0].EffectivePercent)
	}

	all := og.AllOwnersInChain(vessel.ID, OwnershipBeneficial, 0.25, 5)
	if len(all) != 2 {
		t.Fatalf("expected 2 owners in full chain (shell + person), got %d: %+v", len(all), all)
	}
	for _, u := range all {
		if u.OwnerID == shell.ID && !u.IsIntermediate {
			t.Errorf("shell should be marked IsIntermediate")
		}
		if u.OwnerID == owner.ID && u.IsIntermediate {
			t.Errorf("ultimate person owner should NOT be marked IsIntermediate")
		}
	}
}

func TestOwnershipGraph_PartialStakeDilutesThroughChain(t *testing.T) {
	r := NewRegistry()
	shell := mustRegister(t, r, KindShellCompany, "SHELL_B")
	owner := mustRegister(t, r, KindBeneficialOwner, "PERSON_Y")
	vessel := mustRegister(t, r, KindVessel, "IMO2")

	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(owner.ID, shell.ID, 0.5, OwnershipBeneficial, 1))
	must(t, og.RecordOwnership(shell.ID, vessel.ID, 0.6, OwnershipBeneficial, 2))

	ubos := og.UltimateBeneficialOwners(vessel.ID, OwnershipBeneficial, 0.1, 5)
	found := map[string]float64{}
	for _, u := range ubos {
		found[u.OwnerID] = u.EffectivePercent
	}
	want := 0.5 * 0.6
	if diff := found[owner.ID] - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ultimate owner effective percent = %v, want %v", found[owner.ID], want)
	}
}

func TestOwnershipGraph_ThresholdExcludesSmallStakes(t *testing.T) {
	r := NewRegistry()
	shell := mustRegister(t, r, KindShellCompany, "SHELL_C")
	minorOwner := mustRegister(t, r, KindBeneficialOwner, "PERSON_Z")
	vessel := mustRegister(t, r, KindVessel, "IMO3")

	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(minorOwner.ID, shell.ID, 0.1, OwnershipBeneficial, 1))
	must(t, og.RecordOwnership(shell.ID, vessel.ID, 1.0, OwnershipBeneficial, 2))

	ubos := og.UltimateBeneficialOwners(vessel.ID, OwnershipBeneficial, 0.25, 5)
	for _, u := range ubos {
		if u.OwnerID == minorOwner.ID {
			t.Fatalf("minor owner with 10%% stake should be excluded at 25%% threshold, got %+v", u)
		}
	}
}

func TestOwnershipGraph_CyclicOwnershipDoesNotHang(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindShellCompany, "SHELL_CYCLE_A")
	b := mustRegister(t, r, KindShellCompany, "SHELL_CYCLE_B")
	c := mustRegister(t, r, KindShellCompany, "SHELL_CYCLE_C")

	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(a.ID, b.ID, 1.0, OwnershipLegal, 1))
	must(t, og.RecordOwnership(b.ID, c.ID, 1.0, OwnershipLegal, 2))
	must(t, og.RecordOwnership(c.ID, a.ID, 1.0, OwnershipLegal, 3)) // cycle back to A

	done := make(chan []UBOResult, 1)
	go func() {
		done <- og.AllOwnersInChain(c.ID, OwnershipLegal, 0.1, 10)
	}()
	res := <-done
	if len(res) == 0 {
		t.Fatalf("expected at least one owner found in cycle traversal")
	}
}

// TestOwnershipGraph_CircularOwnershipFindingsDetectsCycle covers v7.9
// §16: cycles are surfaced as explicit intelligence findings.
func TestOwnershipGraph_CircularOwnershipFindingsDetectsCycle(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindShellCompany, "CYCLE2_A")
	b := mustRegister(t, r, KindShellCompany, "CYCLE2_B")
	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(a.ID, b.ID, 1.0, OwnershipLegal, 1))
	must(t, og.RecordOwnership(b.ID, a.ID, 1.0, OwnershipLegal, 2))

	findings := og.CircularOwnershipFindings(OwnershipLegal, 10)
	if len(findings) == 0 {
		t.Fatalf("expected at least one circular ownership finding")
	}
}

func TestOwnershipGraph_VerifyChainDetectsTamper(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindBeneficialOwner, "PERSON_TAMPER")
	b := mustRegister(t, r, KindShellCompany, "SHELL_TAMPER")
	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(a.ID, b.ID, 0.9, OwnershipLegal, 1))

	if err := og.VerifyChain(); err != nil {
		t.Fatalf("expected clean chain, got %v", err)
	}
	og.log[0].Percent = 0.1
	if err := og.VerifyChain(); err == nil {
		t.Fatalf("expected VerifyChain to detect tampering")
	}
}

func TestOwnershipGraph_InvalidPercentRejected(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindBeneficialOwner, "PERSON_BAD_PCT")
	b := mustRegister(t, r, KindShellCompany, "SHELL_BAD_PCT")
	og := NewOwnershipGraph(r)
	if err := og.RecordOwnership(a.ID, b.ID, 0, OwnershipLegal, 1); err != ErrInvalidPercent {
		t.Errorf("expected ErrInvalidPercent for 0, got %v", err)
	}
	if err := og.RecordOwnership(a.ID, b.ID, 1.5, OwnershipLegal, 1); err != ErrInvalidPercent {
		t.Errorf("expected ErrInvalidPercent for 1.5, got %v", err)
	}
}

// TestOwnershipGraph_CorrectionSupersedesNotSums is the exact v7.9
// audit fix: a re-filed stake for the SAME (from,to,type) triple must
// REPLACE the prior effective percent, not sum with it (the "60%+20%
// = effective 80%" bug).
func TestOwnershipGraph_CorrectionSupersedesNotSums(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindBeneficialOwner, "PERSON_CORRECT")
	b := mustRegister(t, r, KindShellCompany, "SHELL_CORRECT")
	og := NewOwnershipGraph(r)

	must(t, og.RecordOwnership(a.ID, b.ID, 0.6, OwnershipLegal, 1))
	must(t, og.RecordOwnership(a.ID, b.ID, 0.2, OwnershipLegal, 2)) // correction, not a second owner

	direct := og.DirectOwners(b.ID)
	if len(direct) != 1 {
		t.Fatalf("expected exactly 1 CURRENT edge after correction, got %d: %+v", len(direct), direct)
	}
	if direct[0].Percent != 0.2 {
		t.Errorf("expected current effective percent = 0.2 (latest filing), got %v", direct[0].Percent)
	}

	hist := og.OwnershipHistory(a.ID, b.ID, OwnershipLegal)
	if len(hist) != 2 {
		t.Fatalf("expected 2 historical facts, got %d", len(hist))
	}
	if hist[0].Status != OwnershipStatusSuperseded || hist[0].Percent != 0.6 {
		t.Errorf("expected first fact SUPERSEDED at 0.6, got %+v", hist[0])
	}
	if hist[1].Status != OwnershipStatusActive || hist[1].Percent != 0.2 {
		t.Errorf("expected second fact ACTIVE at 0.2, got %+v", hist[1])
	}
	if hist[0].EffectiveTo != hist[1].EffectiveFrom {
		t.Errorf("expected superseded fact's EffectiveTo to equal the correction's tick")
	}

	c := mustRegister(t, r, KindBeneficialOwner, "PERSON_CORRECT_2")
	must(t, og.RecordOwnership(c.ID, b.ID, 0.3, OwnershipLegal, 3))
	direct2 := og.DirectOwners(b.ID)
	if len(direct2) != 2 {
		t.Fatalf("expected 2 concurrent owners (different FromID), got %d: %+v", len(direct2), direct2)
	}
}

// TestOwnershipGraph_TotalityViolationSurfaced is the v7.9 §15 fix:
// >100% direct ownership of one type must be reported, not silently
// capped.
func TestOwnershipGraph_TotalityViolationSurfaced(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindBeneficialOwner, "PERSON_TOTAL_A")
	b := mustRegister(t, r, KindBeneficialOwner, "PERSON_TOTAL_B")
	target := mustRegister(t, r, KindShellCompany, "SHELL_TOTAL")
	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(a.ID, target.ID, 0.7, OwnershipLegal, 1))
	must(t, og.RecordOwnership(b.ID, target.ID, 0.7, OwnershipLegal, 2)) // 1.4 total, impossible

	violations := og.ValidateTotality()
	if len(violations) != 1 {
		t.Fatalf("expected 1 totality violation, got %d: %+v", len(violations), violations)
	}
	if violations[0].Total < 1.39 || violations[0].Total > 1.41 {
		t.Errorf("expected uncapped total ~1.4, got %v", violations[0].Total)
	}

	must(t, og.RecordOwnership(a.ID, target.ID, 0.9, OwnershipVoting, 3))
	violations2 := og.ValidateTotality()
	for _, v := range violations2 {
		if v.Type == OwnershipVoting {
			t.Errorf("a single 90%% VOTING edge alone should not trigger a totality violation: %+v", v)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOwnershipGraph_AsOfPointInTimeReplay is the auditor's explicit
// §19 worked example: A owns 60% at tick 1, corrected to 20% at tick
// 6. Querying "as of tick 3" must return 60%; "as of tick 8" must
// return 20% — G(t), not only G(current).
func TestOwnershipGraph_AsOfPointInTimeReplay(t *testing.T) {
	r := NewRegistry()
	a := mustRegister(t, r, KindBeneficialOwner, "PERSON_ASOF")
	b := mustRegister(t, r, KindShellCompany, "SHELL_ASOF")
	og := NewOwnershipGraph(r)
	must(t, og.RecordOwnership(a.ID, b.ID, 0.6, OwnershipLegal, 1))
	must(t, og.RecordOwnership(a.ID, b.ID, 0.2, OwnershipLegal, 6))

	early := og.DirectOwnersAsOf(b.ID, OwnershipLegal, 3)
	if len(early) != 1 || early[0].Percent != 0.6 {
		t.Fatalf("as-of tick 3: expected 60%%, got %+v", early)
	}
	late := og.DirectOwnersAsOf(b.ID, OwnershipLegal, 8)
	if len(late) != 1 || late[0].Percent != 0.2 {
		t.Fatalf("as-of tick 8: expected 20%%, got %+v", late)
	}
	// As-of a tick before ANY record must show no owners yet.
	before := og.DirectOwnersAsOf(b.ID, OwnershipLegal, 0)
	if len(before) != 0 {
		t.Fatalf("as-of tick 0 (before first record): expected no owners, got %+v", before)
	}
	// Current view must match the latest as-of.
	current := og.DirectOwners(b.ID)
	if len(current) != 1 || current[0].Percent != 0.2 {
		t.Fatalf("current view should match latest correction, got %+v", current)
	}
}
