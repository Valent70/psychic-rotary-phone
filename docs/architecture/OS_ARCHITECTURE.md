# Veriqo OS Architecture

## Layer Map

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         DOMAIN APPLICATIONS                             │
│   maritime_os  │  commodity_os  │  trade_os  │  supplychain_os          │
│   (pkg/domain/maritime, commodity, trade, supplychain)                   │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │ via vos.OS interface only
┌──────────────────────────▼──────────────────────────────────────────────┐
│                            OS FACADE (pkg/os)                           │
│   StartProcess  │  AppendEvidence  │  EvaluateRisk  │  Decide           │
│   AssessCompliance  │  UpdateDigitalTwin  │  StopProcess                │
│   Scheduler (priority-queue)  │  Registry  │  Digital Twin              │
└──────────────────────────┬──────────────────────────────────────────────┘
                           │
        ┌──────────────────┼───────────────────────────┐
        │                  │                           │
┌───────▼──────┐  ┌────────▼───────┐  ┌───────────────▼────────┐
│  CONSENSUS   │  │   STORAGE      │  │   SECURITY             │
│  pkg/        │  │   pkg/         │  │   pkg/                 │
│  consensus/  │  │   storage/     │  │   security/            │
│  raft        │  │   ├── wal      │  │   ├── identity         │
│              │  │   ├── evidence │  │   └── policy           │
│  Raft:       │  │   └── snapshot │  │                        │
│  - election  │  │                │  │  ABAC + hierarchical   │
│  - repl      │  │  WAL:          │  │  SPIFFE-compatible     │
│  - snapshot  │  │  - CRC32       │  │                        │
│  - joint     │  │  - fsync       │  │  PLATFORM              │
│    consensus │  │  - segments    │  │  pkg/platform/         │
│  - leader    │  │                │  │  telemetry             │
│    transfer  │  │  Evidence:     │  │  (OTel SDK)            │
└──────────────┘  │  - Merkle      │  └────────────────────────┘
                  │  - SHA-256     │
                  │  - immutable   │  ┌────────────────────────┐
                  │                │  │   UCR ENGINE           │
                  │  Snapshot:     │  │   pkg/ucr              │
                  │  - streaming   │  │                        │
                  │  - incremental │  │  - WorkingMemory       │
                  │  - checksum    │  │  - ReasoningGraph      │
                  └────────────────┘  │  - OntologyCache       │
                                      │  - CausalPlanner       │
                                      │  - Uncertainty         │
                                      │  - ExplanationGraph    │
                                      └────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                        KERNEL (pkg/kernel)                              │
│              Formal FSM — deterministic, replay-safe                    │
│              Zero external dependencies                                 │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                        CORE (pkg/core)                                  │
│   TraceID  │  VectorClock  │  DRC  │  DARI  │  Decision  │  Severity   │
│   TenantID  │  ProcessID  │  DomainID  │  NodeID  │  Seq                │
└─────────────────────────────────────────────────────────────────────────┘
```

## DARI Contract Flow

Every OS operation produces and propagates a DARI contract:

```
Input → [DRC validation] → Execute → [Evidence append] → Output + DARI
                                              ↓
                                     Merkle chain in
                                     evidence.Store
                                              ↓
                                     Replay engine
                                     verifies hash
```

## Consensus State Machine

```
Follower ──coin──→ Candidate ──won election──→ Leader
    ↑                   │                        │
    └──────term > current──────────────────────-─┘
                        │
                     timeout
                        │
                        ↓
                    Dead (stopped)
```

## Domain Pipeline Template

All four domain pipelines follow this deterministic template:

```
Event Ingestion
     │
     ▼
[1] os.StartProcess(spec) → ProcessID
     │
     ▼
[2] os.AppendEvidence(pid, kind, payload) → evidence.Record
     │
     ▼
[3] os.EvaluateRisk(pid, input) → RiskResult + evidence
     │
     ▼
[4] os.AssessCompliance(pid, input) → ComplianceResult + evidence
     │
     ▼
[5] os.Decide(pid, input) → core.Decision + evidence
     │
     ▼
[6] os.UpdateDigitalTwin(pid, delta) → TwinStateHash + evidence
     │
     ▼
PipelineResult { ProcessID, RiskResult, ComplianceResult, Decision, TwinHash, DARI }
```

## Import Rules

Domain packages (`pkg/domain/*`) MUST NOT import:
- Each other
- `pkg/consensus/raft` directly
- `pkg/storage/evidence` directly

Domain packages MAY import:
- `veriqo/pkg/os` (OS interface only)
- `veriqo/pkg/core` (shared types)

## VEP (Veriqo Engine Package) Map

| VEP | Package | Status |
|-----|---------|--------|
| VEP-001 | `pkg/core` | ✅ Complete |
| VEP-002 | `pkg/kernel/fsm` + `pkg/os` | ✅ Complete |
| VEP-006 | `pkg/storage/evidence` | ✅ Complete |
| VEP-021 | `pkg/security/policy` | ✅ Complete |
| VEP-023 | `pkg/platform/telemetry` | ✅ Complete |
| VEP-024 | `pkg/security/identity` | ✅ Complete |
| VEP-026 | `pkg/domain/maritime` | ✅ Complete |
| VEP-027 | `pkg/domain/commodity` | ✅ Complete |
| VEP-028 | `pkg/domain/supplychain` | ✅ Complete |
| VEP-031 | `pkg/domain/trade` | ✅ Complete |
| VEP-034 | `pkg/ucr` | ✅ Complete |
| VEP-035 | `pkg/os` (Digital Twin) | ✅ Complete |
| — | `pkg/consensus/raft` | ✅ Complete (WAL gap filled) |
| — | `pkg/storage/wal` | ✅ Complete (was missing) |
| — | `pkg/storage/snapshot` | ✅ Complete (was missing) |
| — | `pkg/cluster` | ✅ Complete (new) |
| — | `pkg/replay` | ✅ Complete (new) |
