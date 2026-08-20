package scale

import (
	"fmt"

	"veriqo/pkg/blockers/livedata"
	"veriqo/pkg/governance/reconciliation"
)

// GeneratedEnvelope is one workload unit produced by GenerateWorkload —
// a reconciliation.Envelope (so the exact-same reconciliation accounting
// audit item A6 built can be reused unmodified) plus the explicit
// DataOrigin tag external audit item A7 requires.
type GeneratedEnvelope struct {
	Envelope   reconciliation.Envelope
	DataOrigin livedata.DataMode
}

// splitmix64 — the same small, exactly-specified generator
// pkg/chaos.rng uses and for the same reason: math/rand's algorithm is
// not guaranteed stable across Go versions, and a workload generator
// whose output changes with the toolchain cannot be honestly called
// "the same benchmark run again". Duplicated here (not imported from
// pkg/chaos) because pkg/chaos's rng type is unexported and this
// package has no other dependency on pkg/chaos.
type splitmix64 struct{ state uint64 }

func (r *splitmix64) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// GenerateWorkload deterministically produces count envelopes from seed
// — the exact same (seed, count) always produces byte-identical
// envelopes (same IDs, same payloads, same hashes), which is what makes
// this a genuine, reproducible benchmark rather than arbitrary test
// filler.
//
// Every envelope's DataOrigin is livedata.ModeRealDerivedBenchmark —
// external audit item A7's explicit instruction: "1 juta generated
// envelopes harus dilabel REAL_DERIVED_BENCHMARK jika berasal dari real
// seed. Jangan klaim sebagai 1 juta independent real-world
// observations." This function has no code path that produces any other
// DataOrigin — see TestGeneratedWorkloadIsNeverIndependentObservation.
func GenerateWorkload(seed uint64, count int) []GeneratedEnvelope {
	r := &splitmix64{state: seed}
	out := make([]GeneratedEnvelope, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("env-%08d", i)
		payload := fmt.Sprintf("seed=%d|idx=%d|entropy=%d", seed, i, r.next())
		out[i] = GeneratedEnvelope{
			Envelope: reconciliation.Envelope{
				ID: id, Sequence: uint64(i), Hash: reconciliation.ComputeHash(payload),
				Payload: payload, Authorized: true,
			},
			DataOrigin: livedata.ModeRealDerivedBenchmark,
		}
	}
	return out
}
