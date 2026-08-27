# Veriqo Kernel — SLOs & Operational Targets

Honest scope statement: the latency figures below are measured from
this repository's own Go benchmarks in this sandbox (single process, no
network, no real disk fsync hardware profile beyond the sandbox's own),
not from a production multi-host deployment. They are presented as
**baseline reference numbers to design against**, not as production SLA
commitments — that distinction matters for anyone doing technical
diligence on this document.

## Measured baseline latencies (this repository's benchmarks)

| Operation | Measured (sandbox) | Source |
|---|---|---|
| `fusion.Engine.Arbitrate` | ~206 µs/op (SHA-256-hashing bound, 3 KG writes) | `pkg/moat/fusion` benchmark, prior session |
| `fusion.fuseGroup` (pure math, no I/O) | ~113 ns/op | `pkg/moat/fusion` benchmark, prior session |
| `consensus/durability` WAL append, `SyncAlways` | ~820 µs/op (fsync-bound) | `pkg/consensus/durability` benchmark, prior session |
| `consensus/durability` WAL append, `SyncBatch` | ~3.2 µs/op | `pkg/consensus/durability` benchmark, prior session |
| `consensus/node.Propose` end-to-end (double-fsync) | ~336/sec | `pkg/consensus/node` benchmark, prior session — flagged as an open optimization target |

Run `go test ./... -bench=. -benchmem` in `pkg/moat/fusion`,
`pkg/consensus/raftlite`, and `pkg/transport/flowcontrol` to reproduce
current numbers on your own hardware; benchmark output is not re-pasted
here as a static claim precisely because it drifts with hardware and Go
version.

## Proposed production SLO targets (design targets, not yet measured at scale)

| SLO | Target | Rationale |
|---|---|---|
| p50 risk-scoring pipeline latency (evidence submit → decision) | < 50 ms | Sum of fusion arbitrate + decision decide + twin update, single-node, warm cache |
| p99 risk-scoring pipeline latency | < 250 ms | Allows for GC pauses and occasional fsync-bound writes under `SyncAlways` |
| Event ingest throughput (single node) | ≥ 1,000 evidence submissions/sec | Derived from `fuseGroup`'s ~113ns pure-math cost; actual ceiling is I/O-bound (WAL fsync), not CPU-bound |
| Cluster availability (3-node, majority quorum) | 99.9% | Standard majority-quorum Raft assumption; NOT yet validated under real multi-host network partition — only `SimNetwork`/`MemTransport` in-process partition testing has been done |
| Consensus leader-election recovery time after leader failure | < 1 s | Matches `pkg/consensus/raftlite`'s configured election timeout range (150–300 ms) plus one heartbeat interval margin |
| Audit log durability (no acknowledged write lost) | 100% under single-node crash | Proven by `pkg/consensus/durability` torn-tail/corruption-recovery chaos tests; NOT yet proven under multi-node network partition + crash combined |

## Error budget & recovery

- `pkg/workflow`'s Checkpoint/Recovery mechanism is the primary error
  budget consumer for orchestrated multi-step runs: a crashed step
  resumes from its last checkpoint rather than restarting the whole
  pipeline (`TestExecutorResumeAfterSimulatedCrash`).
- `pkg/consensus/durability`'s WAL torn-tail auto-recovery vs.
  sealed-segment hard-fail policy is the error budget consumer for
  single-node storage faults.
- No cross-region or multi-datacenter failure domain has been designed
  or tested — explicitly out of scope for this repository's current
  state.

## What is NOT yet instrumented

`workflow_steps_total`, `workflow_errors_total`, and
`risk_score_latency_seconds` are named as the concrete metrics to add in
`pkg/platform/observability` per the audit doc's own recommendation —
they are **not yet implemented** in this session; `pkg/platform/observability`
currently exports the metrics wired in prior sessions (see that
package's own tests for the current metric set). This is listed as an
explicit open item in `README.md`.
