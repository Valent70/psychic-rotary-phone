package cluster_test

import (
	"sync"
	"testing"
	"time"

	"veriqo/pkg/cluster"
	"veriqo/pkg/core"
)

func TestRegistry_UpsertAndGet(t *testing.T) {
	reg := cluster.NewRegistry()
	reg.Upsert(cluster.NodeInfo{
		ID:       "node1",
		Addr:     "10.0.0.1:7001",
		Region:   "us-east-1",
		Status:   cluster.NodeStatusHealthy,
		LastSeen: time.Now(),
	})
	n, ok := reg.Get("node1")
	if !ok || n.Addr != "10.0.0.1:7001" {
		t.Fatal("expected to retrieve node1")
	}
}

func TestRegistry_ListHealthy(t *testing.T) {
	reg := cluster.NewRegistry()
	for i, status := range []cluster.NodeStatus{
		cluster.NodeStatusHealthy,
		cluster.NodeStatusSuspect,
		cluster.NodeStatusHealthy,
	} {
		reg.Upsert(cluster.NodeInfo{
			ID:       core.NodeID(string(rune('A' + i))),
			Status:   status,
			LastSeen: time.Now(),
		})
	}
	healthy := reg.Healthy()
	if len(healthy) != 2 {
		t.Fatalf("expected 2 healthy nodes, got %d", len(healthy))
	}
}

func TestRegistry_Version(t *testing.T) {
	reg := cluster.NewRegistry()
	v1 := reg.Version()
	reg.Upsert(cluster.NodeInfo{ID: "n1", LastSeen: time.Now()})
	v2 := reg.Version()
	if v2 <= v1 {
		t.Fatal("version should increment after upsert")
	}
}

func TestRegistry_Remove(t *testing.T) {
	reg := cluster.NewRegistry()
	reg.Upsert(cluster.NodeInfo{ID: "n1", Status: cluster.NodeStatusHealthy, LastSeen: time.Now()})
	reg.Remove("n1")
	n, _ := reg.Get("n1")
	if n.Status != cluster.NodeStatusLeft {
		t.Fatalf("expected Left status after remove, got %s", n.Status)
	}
}

func TestMembership_JoinAndLeave(t *testing.T) {
	reg := cluster.NewRegistry()
	local := cluster.NodeInfo{ID: "local", Addr: "127.0.0.1:7000", LastSeen: time.Now()}
	m := cluster.NewMembership(local, reg)

	var events []cluster.Event
	var mu sync.Mutex
	m.OnEvent(func(e cluster.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	peer := cluster.NodeInfo{ID: "peer1", Addr: "127.0.0.1:7001", LastSeen: time.Now()}
	if err := m.Join(peer); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // let goroutine fire

	m.Leave("peer1")
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	joined := 0
	left := 0
	for _, e := range events {
		if e.Type == cluster.EventJoined {
			joined++
		}
		if e.Type == cluster.EventLeft {
			left++
		}
	}
	if joined == 0 {
		t.Fatal("expected join event")
	}
	if left == 0 {
		t.Fatal("expected leave event")
	}
}

func TestMembership_Heartbeat(t *testing.T) {
	reg := cluster.NewRegistry()
	local := cluster.NodeInfo{ID: "local", LastSeen: time.Now()}
	m := cluster.NewMembership(local, reg)
	_ = m.Join(cluster.NodeInfo{ID: "peer", Status: cluster.NodeStatusSuspect, LastSeen: time.Now().Add(-20 * time.Second)})
	m.Heartbeat("peer")
	n, _ := reg.Get("peer")
	if n.Status != cluster.NodeStatusHealthy {
		t.Fatalf("heartbeat should restore healthy status, got %s", n.Status)
	}
}

func TestPlacementEngine_LeastLoaded(t *testing.T) {
	reg := cluster.NewRegistry()
	reg.Upsert(cluster.NodeInfo{ID: "n1", Status: cluster.NodeStatusHealthy, Capacity: 0.9, LastSeen: time.Now()})
	reg.Upsert(cluster.NodeInfo{ID: "n2", Status: cluster.NodeStatusHealthy, Capacity: 0.2, LastSeen: time.Now()})
	reg.Upsert(cluster.NodeInfo{ID: "n3", Status: cluster.NodeStatusHealthy, Capacity: 0.5, LastSeen: time.Now()})

	pe := cluster.NewPlacementEngine(reg, cluster.PlacementLeastLoaded)
	selected, err := pe.Select("")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "n2" {
		t.Fatalf("expected n2 (lowest capacity), got %s", selected.ID)
	}
}

func TestPlacementEngine_RegionAffinity(t *testing.T) {
	reg := cluster.NewRegistry()
	reg.Upsert(cluster.NodeInfo{ID: "n1", Region: "us-east-1", Status: cluster.NodeStatusHealthy, LastSeen: time.Now()})
	reg.Upsert(cluster.NodeInfo{ID: "n2", Region: "eu-west-1", Status: cluster.NodeStatusHealthy, LastSeen: time.Now()})

	pe := cluster.NewPlacementEngine(reg, cluster.PlacementRegionAffinity)
	selected, err := pe.Select("eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "n2" {
		t.Fatalf("expected eu-west-1 node, got %s (region %s)", selected.ID, selected.Region)
	}
}

func TestPlacementEngine_NoHealthyNodes(t *testing.T) {
	reg := cluster.NewRegistry()
	pe := cluster.NewPlacementEngine(reg, cluster.PlacementRoundRobin)
	_, err := pe.Select("")
	if err == nil {
		t.Fatal("expected error when no healthy nodes")
	}
}
