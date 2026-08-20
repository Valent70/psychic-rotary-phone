//go:build linux

// Session update — closes further roadmap-adjacent coverage in
// pkg/kernel/resource: Manager.GrantOf (accounting-layer query, 0%
// covered) and OSEnforcer.SetPIDsLimit (real cgroup v1 fork-bomb
// protection, 0% covered — the pids controller was configured nowhere
// in the existing suite even though AddPID already confines processes
// into the pids hierarchy).
package resource

import (
	"os"
	"testing"
)

func TestGrantOf_FoundAndNotFound(t *testing.T) {
	m := NewManager()
	m.SetBudget(Memory, 1000)
	if err := m.Reserve("holder-a", Memory, 200, 5); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	g, ok := m.GrantOf("holder-a", Memory)
	if !ok {
		t.Fatal("expected a grant for holder-a/Memory")
	}
	if g.Amount != 200 || g.Priority != 5 {
		t.Fatalf("unexpected grant contents: %+v", g)
	}

	if _, ok := m.GrantOf("holder-a", CPU); ok {
		t.Fatal("expected no grant for a Kind never reserved")
	}
	if _, ok := m.GrantOf("nobody", Memory); ok {
		t.Fatal("expected no grant for an unknown holder")
	}
}

func TestOSEnforcer_SetPIDsLimit_RealForkBombProtection(t *testing.T) {
	e := NewOSEnforcer("veriqo-resource-test")
	if !e.Available() {
		t.Skip("cgroup v1 not writable in this environment")
	}
	holder := "pidslimit-holder"
	if err := e.CreateGroup(holder); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	defer e.RemoveGroup(holder)

	if err := e.SetPIDsLimit(holder, 4); err != nil {
		t.Fatalf("SetPIDsLimit: %v", err)
	}
	// Confirm the kernel actually accepted and reports back the limit —
	// not just that the write call returned nil.
	raw, err := os.ReadFile(pidsRoot + "/" + e.prefix + "/" + holder + "/pids.max")
	if err != nil {
		t.Fatalf("reading back pids.max: %v", err)
	}
	if got := string(trimNewline(raw)); got != "4" {
		t.Fatalf("want pids.max=4, kernel reports %q", got)
	}
}
