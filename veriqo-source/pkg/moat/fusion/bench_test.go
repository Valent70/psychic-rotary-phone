package fusion

import (
	"fmt"
	"testing"

	"veriqo/pkg/moat/kg"
)

// BenchmarkArbitrateSingleValue measures the cost of one arbitration with
// 3 corroborating sources and a KG sink attached (the realistic path).
func BenchmarkArbitrateSingleValue(b *testing.B) {
	graph := kg.NewGraph()
	e := NewEngine(graph)
	_ = e.RegisterSource(SourceProfile{ID: "src-a", BaseReliability: 0.7})
	_ = e.RegisterSource(SourceProfile{ID: "src-b", BaseReliability: 0.8})
	_ = e.RegisterSource(SourceProfile{ID: "src-c", BaseReliability: 0.9})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claim := Claim{Subject: fmt.Sprintf("VESSEL:%d", i), Predicate: "FLAG_STATE"}
		_, _ = e.Submit("src-a", claim, "PANAMA", 1)
		_, _ = e.Submit("src-b", claim, "PANAMA", 2)
		_, _ = e.Submit("src-c", claim, "PANAMA", 3)
		_, _ = e.Arbitrate("bench-actor", claim, 4)
	}
}

// BenchmarkFuseGroup isolates the pure Bayesian combination cost from
// Submit/KG-write overhead.
func BenchmarkFuseGroup(b *testing.B) {
	group := []Evidence{
		{Reliability: 0.7}, {Reliability: 0.8}, {Reliability: 0.9}, {Reliability: 0.6},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fuseGroup(group)
	}
}
