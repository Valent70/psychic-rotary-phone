# VERIQO SLA / SLO — DRAFT

> **THIS IS A DRAFT AND NOT A COMMITMENT.** No service level in this
> document is contractually offered or agreed. It is a starting point
> for a conversation with a pilot customer about what VERIQO could
> commit to, and — more usefully — about what it cannot yet commit to
> and why.
>
> A contractual SLA requires production infrastructure, an operations
> rota, measured baselines, and legal review. VERIQO has none of these
> today. Presenting this document as an offered SLA would be a
> misrepresentation.

---

## 1. Why there is no SLA yet

An availability commitment requires four things VERIQO does not have:

1. **Production infrastructure.** Deployments run on a single host with
   a local WAL. There is no multi-region deployment, no automated
   failover, and no load balancing across replicas.
2. **Measured baselines.** No production traffic has been observed.
   Committing to a number nobody has measured is guesswork.
3. **An operations rota.** No on-call rotation exists, so no response
   time can be promised.
4. **Automated backup.** RPO is currently set by whatever the operator
   schedules, not by the product.

Each is deployment or business work, not engineering work on the
codebase. Until they exist, the honest answer to "what's your SLA?" is
"we don't offer one yet, and here is exactly what's missing."

---

## 2. What could be committed today

These are properties of the software as built and could be committed
without further engineering:

| Commitment | Basis |
|---|---|
| **Acknowledged writes are durable.** A `2xx` response means the operation was written to the WAL and fsynced before the response was sent. | Design property; proven by crash-recovery and torn-write tests. |
| **Recovery preserves every acknowledged write.** After a process crash, restart reconstructs byte-identical state; at most one in-flight, never-acknowledged operation is lost, and its loss is reported. | Proven by `TestDurableStoreRecoversFromATornWrite` and a live kill/restart against the compiled binary. |
| **Replay determinism.** Given unchanged inputs and unchanged finalized evidence, replay converges byte-identically — within a software version. | Proven; see the Replay Specification. |
| **Exported dossiers remain verifiable indefinitely**, with no VERIQO service reachable and no licence in force. | Packages are self-contained; the verifier is a standalone binary. |
| **Tenant isolation.** No tenant can read, modify, or replay another tenant's case. | Structural, adversarially tested. |
| **Tamper-evidence.** Any alteration of the audit ledger is detectable by verification. | Hash chain verified from genesis. |

These are the commitments actually worth making about an evidence
system, and they are stronger than an availability percentage. Lead with
them.

---

## 3. Draft SLOs (targets, not commitments)

Proposed as pilot targets to be measured, then revised from evidence.

### Availability

| Metric | Draft target | Notes |
|---|---|---|
| Gateway availability (`/livez` 200) | 99.0 % monthly | Single-host. Excludes planned maintenance. **A 99.9 % target is not achievable without redundancy that does not exist.** |
| Readiness (`/readyz` 200 when live) | 99.0 % monthly | Distinguishes "process up" from "fit to serve". |

### Latency

| Operation | Draft target (p95) | Notes |
|---|---|---|
| `POST /v1/evidence` | < 250 ms | Includes an fsync; disk-bound. |
| `POST /v1/cases/{id}/decide` | < 500 ms | Includes grounding against evidence. |
| `GET` reads | < 100 ms | In-memory projection. |
| `GET /v1/cases/{id}/dossier?format=package` | < 5 s | Builds and zips; scales with evidence count. |

**Unmeasured in production.** Treat as hypotheses to validate during a
pilot, not as figures to quote.

### Durability and recovery

| Metric | Draft target | Notes |
|---|---|---|
| RPO — process crash | ≤ 1 unacknowledged operation | Software property, not a promise about the host. |
| RPO — host loss | = backup interval | **Set by the operator's backup schedule.** With no automated backup, this is unbounded — the most important gap in this table. |
| RTO | Unmeasured | Dominated by WAL replay time. Measure during the pilot's restore drill. |

### Correctness

| Metric | Target | Notes |
|---|---|---|
| `replay_failures` | 0 | Any non-zero value is a defect, not a budget to spend. |
| `ledger_commit_failures` | 0 | Same. |
| `custody_chain_failures` | 0 | Same. |

**These have no error budget.** An availability blip is tolerable; a
record that does not verify is a failure of the product's entire
purpose. This asymmetry should survive into any real SLA.

---

## 4. Support response — draft

No on-call rotation exists. These are proposed for a pilot with
business-hours support only:

| Severity | Draft response | Definition |
|---|---|---|
| SEV-1 | 4 business hours | Integrity in question or evidence may be lost. |
| SEV-2 | 8 business hours | Confidentiality or authorization boundary. |
| SEV-3 | 1 business day | Availability loss, integrity intact. |
| SEV-4 | 3 business days | Degraded or anomalous. |

Severity definitions match the Incident Response Procedure. Note that
integrity outranks availability, which is deliberate and should be
preserved in any negotiated SLA.

---

## 5. Explicit exclusions

Any eventual SLA must exclude:

- **Availability of evidence documents.** They are in the customer's
  storage; VERIQO never had them.
- **Correctness of customer-supplied content** — hashes, identifiers,
  rationales, and asserted identities are recorded as given.
- **Legal outcomes.** See the Legal Positioning Statement and
  Jurisdiction Disclaimer.
- **Cross-version replay determinism.** Guaranteed within a software
  version; not claimed across versions.
- **Third-party verification behaviour** where the verifying party has
  not obtained a trusted key registry — signature checks will `SKIP`,
  correctly.

---

## 6. To turn this into a real SLA

1. Deploy on infrastructure with redundancy and automated failover.
2. Automate backups and measure the actual RPO achieved.
3. Run the restore drill; record measured RTO.
4. Measure latency and availability under real traffic for a full
   pilot period.
5. Establish an on-call rotation.
6. Complete an independent penetration test — most enterprise customers
   gate an SLA on it.
7. Have the resulting commitments reviewed legally.

Steps 1–5 are the ones that convert the draft targets above from
hypotheses into numbers worth signing.
