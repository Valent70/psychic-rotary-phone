package resource

import (
	"errors"
	"sync"
	"testing"
)

// These tests close V7.11.1 P0-6 for pkg/kernel/resource. The branches
// that matter here are the ones where a wrong answer means the kernel
// over-commits: double reservation, release of a grant that was never
// taken, budget shrinkage below what is already held, and concurrent
// reservation racing past the ceiling.

func manager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager()
	m.SetBudget(Memory, 1000)
	m.SetBudget(Worker, 4)
	return m
}

func TestReservationCannotExceedTheBudget(t *testing.T) {
	m := manager(t)
	if err := m.Reserve("a", Memory, 600, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Reserve("b", Memory, 600, 1); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("the second reservation must be refused, got %v", err)
	}
	if got := m.Available(Memory); got != 400 {
		t.Fatalf("availability must reflect only the granted reservation, got %d", got)
	}
}

func TestExactBudgetBoundaryIsAllowedAndOneMoreIsNot(t *testing.T) {
	m := manager(t)
	if err := m.Reserve("a", Memory, 1000, 1); err != nil {
		t.Fatalf("consuming the whole budget exactly must be allowed: %v", err)
	}
	if err := m.Reserve("b", Memory, 1, 1); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("one unit past the ceiling must be refused, got %v", err)
	}
	if m.Available(Memory) != 0 {
		t.Fatalf("a fully consumed budget must report zero available")
	}
	if u := m.Utilization(Memory); u != 1 {
		t.Fatalf("utilization must be 1.0 at full consumption, got %v", u)
	}
}

func TestUnbudgetedKindIsRefusedRatherThanTreatedAsUnlimited(t *testing.T) {
	m := NewManager()
	if err := m.Reserve("a", GPU, 1, 1); err == nil {
		t.Fatal("a kind with no declared budget must not grant unlimited capacity")
	}
	if got := m.Available(GPU); got != 0 {
		t.Fatalf("an unbudgeted kind must report zero available, got %d", got)
	}
}

func TestReleaseOfAnUnknownGrantIsAnError(t *testing.T) {
	m := manager(t)
	if err := m.Release("ghost", Memory); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("releasing a grant that was never taken must be an error, got %v", err)
	}
}

func TestDoubleReleaseDoesNotReturnCapacityTwice(t *testing.T) {
	m := manager(t)
	if err := m.Reserve("a", Memory, 400, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("a", Memory); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("a", Memory); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("a second release must be refused, got %v", err)
	}
	if got := m.Available(Memory); got != 1000 {
		t.Fatalf("capacity must be returned exactly once, got %d available", got)
	}
}

func TestReleaseAllFreesEveryKindForAHolder(t *testing.T) {
	m := manager(t)
	_ = m.Reserve("a", Memory, 500, 1)
	_ = m.Reserve("a", Worker, 2, 1)
	_ = m.Reserve("b", Memory, 200, 1)
	m.ReleaseAll("a")
	if got := m.Available(Memory); got != 800 {
		t.Fatalf("only holder a's memory must be freed, got %d", got)
	}
	if got := m.Available(Worker); got != 4 {
		t.Fatalf("holder a's workers must be freed, got %d", got)
	}
	if _, ok := m.GrantOf("b", Memory); !ok {
		t.Fatal("another holder's grant must be untouched")
	}
}

func TestReleaseAllOnAnUnknownHolderIsSafe(t *testing.T) {
	m := manager(t)
	_ = m.Reserve("a", Memory, 100, 1)
	m.ReleaseAll("nobody") // must not panic and must not free anything
	if got := m.Available(Memory); got != 900 {
		t.Fatalf("releasing an unknown holder must free nothing, got %d", got)
	}
}

func TestConcurrentReservationsNeverExceedTheBudget(t *testing.T) {
	m := NewManager()
	m.SetBudget(Worker, 10)
	var wg sync.WaitGroup
	granted := make([]bool, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			granted[i] = m.Reserve("h"+string(rune('a'+i%26))+string(rune('0'+i/26)),
				Worker, 1, 1) == nil
		}(i)
	}
	wg.Wait()
	count := 0
	for _, g := range granted {
		if g {
			count++
		}
	}
	if count > 10 {
		t.Fatalf("the ceiling must hold under concurrency: %d grants for a budget of 10", count)
	}
	if m.Available(Worker) != uint64(10-count) {
		t.Fatalf("accounting must be exact after concurrent reservation: %d available for %d grants",
			m.Available(Worker), count)
	}
}

func TestRankByPriorityIsDeterministic(t *testing.T) {
	m := manager(t)
	_ = m.Reserve("low", Memory, 100, 1)
	_ = m.Reserve("high", Memory, 100, 9)
	_ = m.Reserve("mid", Memory, 100, 5)
	first := m.RankByPriority(Memory)
	if len(first) != 3 || first[0].Holder != "high" {
		t.Fatalf("the highest priority must rank first: %+v", first)
	}
	for i := 0; i < 10; i++ {
		again := m.RankByPriority(Memory)
		for j := range first {
			if first[j].Holder != again[j].Holder {
				t.Fatal("ranking must be deterministic across calls")
			}
		}
	}
}

func TestUtilizationOfAnEmptyBudgetIsZeroNotNaN(t *testing.T) {
	m := NewManager()
	if u := m.Utilization(Storage); u != 0 {
		t.Fatalf("an undeclared budget must report 0 utilization, not %v", u)
	}
	m.SetBudget(Storage, 0)
	if u := m.Utilization(Storage); u != 0 {
		t.Fatalf("a zero budget must not divide by zero, got %v", u)
	}
}

func TestZeroAmountReservationIsRejected(t *testing.T) {
	m := manager(t)
	if err := m.Reserve("a", Memory, 0, 1); err == nil {
		t.Fatal("a zero-amount reservation is meaningless and must be refused")
	}
}
