package resource

import "testing"

func TestReserveWithinBudget(t *testing.T) {
	m := NewManager()
	m.SetBudget(Memory, 1000)
	if err := m.Reserve("engineA", Memory, 400, 5); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if avail := m.Available(Memory); avail != 600 {
		t.Fatalf("expected 600 available, got %d", avail)
	}
}

func TestReserveOverBudgetRejected(t *testing.T) {
	m := NewManager()
	m.SetBudget(CPU, 100)
	if err := m.Reserve("engineA", CPU, 80, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Reserve("engineB", CPU, 30, 1); err == nil {
		t.Fatal("expected ErrOverBudget")
	}
	// Rejected reservation must not have altered accounting.
	if avail := m.Available(CPU); avail != 20 {
		t.Fatalf("expected 20 still available after rejected reserve, got %d", avail)
	}
}

func TestReserveIdempotentResize(t *testing.T) {
	m := NewManager()
	m.SetBudget(Worker, 10)
	m.Reserve("engineA", Worker, 4, 1)
	if err := m.Reserve("engineA", Worker, 6, 1); err != nil {
		t.Fatalf("resize reserve: %v", err)
	}
	if avail := m.Available(Worker); avail != 4 {
		t.Fatalf("expected 4 available after resize to 6/10, got %d", avail)
	}
}

func TestReleaseFreesQuota(t *testing.T) {
	m := NewManager()
	m.SetBudget(Storage, 500)
	m.Reserve("engineA", Storage, 300, 1)
	if err := m.Release("engineA", Storage); err != nil {
		t.Fatalf("release: %v", err)
	}
	if avail := m.Available(Storage); avail != 500 {
		t.Fatalf("expected full budget restored, got %d", avail)
	}
}

func TestReleaseUnknownGrant(t *testing.T) {
	m := NewManager()
	if err := m.Release("nobody", GPU); err == nil {
		t.Fatal("expected ErrUnknownGrant")
	}
}

func TestReleaseAll(t *testing.T) {
	m := NewManager()
	m.SetBudget(Memory, 100)
	m.SetBudget(CPU, 100)
	m.Reserve("engineA", Memory, 50, 1)
	m.Reserve("engineA", CPU, 50, 1)
	m.ReleaseAll("engineA")
	if avail := m.Available(Memory); avail != 100 {
		t.Fatalf("memory not fully released: %d", avail)
	}
	if avail := m.Available(CPU); avail != 100 {
		t.Fatalf("cpu not fully released: %d", avail)
	}
}

func TestUtilization(t *testing.T) {
	m := NewManager()
	m.SetBudget(Bandwidth, 200)
	m.Reserve("engineA", Bandwidth, 50, 1)
	if u := m.Utilization(Bandwidth); u != 0.25 {
		t.Fatalf("expected utilization 0.25, got %f", u)
	}
}

func TestRankByPriority(t *testing.T) {
	m := NewManager()
	m.SetBudget(GPU, 10)
	m.Reserve("low", GPU, 1, 1)
	m.Reserve("high", GPU, 1, 9)
	m.Reserve("mid", GPU, 1, 5)
	ranked := m.RankByPriority(GPU)
	if len(ranked) != 3 || ranked[0].Holder != "high" || ranked[1].Holder != "mid" || ranked[2].Holder != "low" {
		t.Fatalf("unexpected ranking: %+v", ranked)
	}
}
