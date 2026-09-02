# VERIQO OS Architecture

**Status:** FROZEN — MIP-001 W0
**Supersedes:** nothing. Sits above every prior architecture document
as the integrating frame; prior documents remain valid within their
scope.

---

## 1. The reframing

VERIQO is **not** maritime AI plus dark web plus insurance plus dispute
resolution plus an LLM. That composition produces what the architecture
review correctly called a *Frankenstein platform* — fifteen products
sharing a logo.

VERIQO is an **Evidence Operating System**. Maritime, commodity,
supply chain, insurance, trade finance, eBL, financial investigation,
dispute, and dark web are **domain intelligence capabilities that
consume the same evidence fabric**.

The architectural law:

> **ONE CANONICAL STATE. MANY DOMAIN PROJECTIONS.**

The second law:

> **INTELLIGENCE MAY PROPOSE. EVIDENCE AUTHORITY MUST REMAIN
> DETERMINISTIC, POLICY-BOUND, AUDITABLE, REPLAYABLE, AND HUMAN-GOVERNED
> WHERE REQUIRED.**

The third law:

> **NO DOMAIN MAY BYPASS THE EVIDENCE, POLICY, RIGHTS, PROVENANCE,
> QUALIFICATION, DISCLOSURE, OR REPLAY FABRIC.**

---

## 2. Five planes

```
┌──────────────────────────────────────────────────────────┐
│  1. INTELLIGENCE PLANE                                   │
│     Aureum · God of EYS · LLM · Dark Web · ML · Analytics│
│     May PROPOSE. Holds no evidence authority.            │
├──────────────────────────────────────────────────────────┤
│  2. EPISTEMIC PLANE                                      │
│     Reverse Proof · Hypothesis · Contradiction · Trust    │
│     Observability · Qualification · Next Best Evidence    │
├──────────────────────────────────────────────────────────┤
│  3. EVIDENCE PLANE                                       │
│     IEAP · Raw · Provenance · Lineage · WORM             │
│     Independence · Integrity · Custody · Versioning       │
├──────────────────────────────────────────────────────────┤
│  4. GOVERNANCE PLANE                                     │
│     NEP · Rights · Privilege · PO · Disclosure · Dissent  │
│     Conflict · Procedural Neutrality · AI Authority       │
├──────────────────────────────────────────────────────────┤
│  5. TRUST / EXECUTION KERNEL                             │
│     Identity · Policy · Determinism · Ledger · Raft       │
│     Replay · Cryptography · KMS/HSM · Attestation         │
└──────────────────────────────────────────────────────────┘
```

**The invariant that makes this an OS rather than a diagram:** a domain
sits only at plane 1 and reaches planes 2–5 through their published
contracts. There is no path from a domain adapter to storage, ledger,
or policy that skips the intervening planes. `pkg/constitution`'s
Article 8 and the third law above are the executable form of this.

---

## 3. Canonical evidence lifecycle

Every case follows this, including dark web. There is not a separate
"fast path".

```
CLAIM / QUESTION → CASE CONSTITUTION → REVERSE PROOF
  → WHAT MUST BE TRUE? → WHAT SHOULD WE OBSERVE?
  → NEXT BEST EVIDENCE → RIGHTS + AUTHORITY
  → INDEPENDENT ACQUISITION → RAW PRESERVATION
  → INTEGRITY + PROVENANCE → SOURCE INDEPENDENCE
  → OBSERVABILITY GATE → CONTRADICTION MATRIX
  → ALTERNATIVE HYPOTHESES → TRUST CALCULUS
  → DISSENT → EPISTEMIC QUALIFICATION
  → PROCEDURAL QUALIFICATION → DISCLOSURE GOVERNANCE
      ├── ORIGINAL   → WORM
      └── DERIVATIVE → REDACTION / ZKP
  → COMMON FACT PACK → INDEPENDENT VERIFIER → REPLAY
```

Note the ordering that is easy to get wrong and expensive to fix:
**rights precede acquisition**, **raw preservation precedes integrity
assessment**, and **observability precedes contradiction** — because
you cannot score a contradiction against evidence you were never in a
position to observe.

---

## 4. Where each plane lives in this repository

The architecture is not a greenfield. This table is the binding map
from the plane model onto code that already exists, and it is what
MIP §7 ("does this capability already exist?") is answered against.

| Plane | Capability | Existing implementation | Status |
|---|---|---|---|
| 5 | Ledger | `pkg/platform/audit` (hash-chained, Merkle) | EXISTS |
| 5 | Policy / purpose binding | `pkg/authz` | EXISTS |
| 5 | Identity | `pkg/identity`, `pkg/platform/security` | EXISTS |
| 5 | Determinism / canonicalization | `pkg/canonical/jcs` | EXISTS |
| 5 | Consensus | `pkg/consensus/raftlite`, `pkg/cluster` | EXISTS |
| 5 | Crypto / KMS abstraction | `pkg/platform/security/keys` | EXISTS |
| 5 | Storage / WAL | `pkg/storage`, `pkg/storage/wal` | EXISTS |
| 5 | Replay | `pkg/replay`, `commercialapi.Store.Replay` | EXISTS |
| 3 | Evidence manifest + custody FSM | `pkg/evidence/manifest` (FROZEN) | EXISTS |
| 3 | Canonical evidence projection | `pkg/commercial/evidencefabric` | EXISTS |
| 3 | Retention / legal hold | `pkg/governance/data` | EXISTS |
| 3 | Provenance | `pkg/evidence/provenance`, `pkg/insurance/cre/provenance.go` | EXISTS |
| 3 | Acquisition contracts | `pkg/connector/{aisstream,bol,insurance,payment,sar}` | EXISTS (simulated sources) |
| 2 | Trust calculus | `pkg/core/trustcalc`, `pkg/trust` | EXISTS |
| 2 | Contradiction | `pkg/insurance/contradiction`, `pkg/moat/contradiction` | EXISTS |
| 2 | Hypothesis | `pkg/insurance/causation` | EXISTS |
| 2 | Dependency / corroboration | `pkg/insurance/evidence/{dependency,corroboration}.go` | EXISTS |
| 2 | **Reverse proof** | — | **BUILT THIS ROUND** (`pkg/qualification/reverseproof`) |
| 2 | **Observed absence** | — | **BUILT THIS ROUND** (`pkg/qualification/observability`) |
| 2 | **Source independence (canonical)** | — | **BUILT THIS ROUND** (`pkg/qualification/independence`) |
| 2 | **Qualification states** | — | **BUILT THIS ROUND** (`pkg/qualification/state`) |
| 2 | **Next best evidence** | — | **BUILT THIS ROUND** (`pkg/qualification/nextbest`) |
| 4 | **Constitution (executable)** | — | **BUILT THIS ROUND** (`pkg/constitution`) |
| 4 | **Disclosure P/C model** | — | **BUILT THIS ROUND** (`pkg/disclosure/access`) |
| 4 | Dissent | partial (`contradiction` tests) | **CANONICALIZED THIS ROUND** |
| 1 | **AI evidence gateway** | — | **BUILT THIS ROUND** (`pkg/ai/gateway`) |
| 5 | **Canonical EventEnvelope** | — | **BUILT THIS ROUND** (`pkg/contract/event`) |
| 1 | Aureum / God of EYS / dark web | — | **NOT BUILT** — see §6 |

---

## 5. The anti-duplication rule

MIP §7 forbids a second authoritative engine for a capability an
existing primitive can carry. Concretely:

**Forbidden:**
```
ledger + audit-ledger + disclosure-ledger + privilege-ledger + redaction-ledger
```

**Required:**
```
ONE ledger, carrying disclosure · redaction · privilege · dissent events
```

Same for policy:

```
ONE policy plane
 ├── Acquisition   ├── Rights      ├── Disclosure
 ├── AI            ├── Privilege   ├── Protective Order
 ├── Neutrality    └── Qualification
```

This round adds **no new ledger and no new policy engine.** The new
packages emit into the existing `pkg/platform/audit` chain and express
their rules as data the existing policy plane evaluates.

---

## 6. What is deliberately not built

Named here rather than discovered later, per MIP §32 rule 18.

| Capability | Why not built |
|---|---|
| Aureum AI, God of EYS | Require an actual model deployment and an investigation corpus. The **gateway they must pass through** is built and enforced; the intelligences themselves are not. |
| Dark web acquisition | Requires legal review of acquisition authority per jurisdiction. The acquisition **contract** shape exists; no live acquisition path is built, and building one without that review would violate Article 4. |
| ZKP prover | A real proving system is a substantial cryptographic dependency. Article 9's *bounded meaning* rule is expressed in the constitution; no prover is built, and none is claimed. |
| PDF/XLSX/PPTX redaction workers | Require document-format toolchains and an adversarial recovery lab. The redaction **state machine and assurance record** are specified; the media workers are not built. |
| Multi-region, HSM tenancy, pentest | External procurement — see `docs/VERIQO_EXTERNAL_QUALIFICATION_TRACK.md`. |

Per MIP §34, each of these is `DESIGNED`, not `IMPLEMENTED`. None is
reported as done.

---

## 7. Commercial product modes

Private Intelligence Mode and Neutral Evidence Mode run on the **same**
Evidence OS. The distinction is policy and configuration, never a
separate architecture or a forked codebase. This is what makes the
neutrality claim credible: a neutral case is not running different
software that could quietly behave differently.

---

## 8. One-sentence lock

> VERIQO is an Evidence Operating System that independently acquires,
> preserves, connects, tests, qualifies, bounds, discloses, verifies,
> and replays evidence — while Aureum, God of EYS, dark web, maritime,
> commodity, insurance, supply chain, finance, and dispute intelligence
> are capabilities wholly subject to the Evidence Constitution, IEAP,
> EQF, NEP, policy, ledger, cryptography, independent verification, and
> replay.
