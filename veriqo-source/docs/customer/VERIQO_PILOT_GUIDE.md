# VERIQO Pilot Guide

For a team about to run a VERIQO pilot. It states what a pilot is for,
what VERIQO will and will not do during it, and what a successful pilot
looks like — including how to conclude honestly that it failed.

Read alongside `docs/VERIQO_READINESS_TIER_FRAMEWORK.md`, which states
precisely which readiness tier VERIQO currently occupies and what is
still open.

---

## 1. What a VERIQO pilot is for

A pilot answers one question: **when a dispute, claim, or trade event
goes wrong, can you show a third party what you knew, when you knew it,
and what you decided — in a form they can check without trusting you?**

That is the capability under test. Not throughput, not UI polish, not
integration breadth.

Good pilot candidates are cases where:

- Evidence arrives from more than one party, and the parties do not
  fully trust each other.
- The record matters months or years later — a claim that may be
  litigated, a cargo release that may be challenged, a document transfer
  someone may dispute.
- Today, the answer to "how do we prove what we knew?" is a shared drive
  and a person's memory.

Poor pilot candidates: high-volume routine transactions where nothing is
ever contested. VERIQO will work, but the pilot will not tell you
anything.

---

## 2. What is real today

Everything in this list is implemented and tested, and you can exercise
it yourself during the pilot:

- **Evidence intake** with hash, provenance, chain of custody, and a
  real state machine to FINALIZED.
- **Grounded decisions** — a decision cannot cite evidence that was not
  submitted and finalized. There is no override.
- **Action authorization** — an action cannot be executed unless a
  decision exists and the authorization actually covers it.
- **Dossier export**, human-readable and machine-verifiable.
- **Independent verification** by a separate binary that reads only the
  exported file.
- **Replay** — re-derive a decision from its recorded inputs and confirm
  the hashes converge byte-identically.
- **Durability** — the store is WAL-backed and survives process crashes;
  backup and restore share one code path with crash recovery.
- **Tenant isolation**, with the acting tenant bound to your verified
  identity.
- **Retention and legal hold**, with purge structurally blocked while a
  hold is in place.

---

## 3. What is NOT real today

Stated plainly so it is not discovered mid-pilot:

| Not available | Consequence for your pilot |
|---|---|
| **OIDC / SSO** | Authentication is JWT + API key issued by the deployment, not federated through your IdP. Plan for a small number of pilot credentials, not a company-wide rollout. |
| **HTTP routes for retention / legal hold / signing** | These capabilities work but are driven through the Go API. If your pilot needs to place holds from your own systems, raise it during scoping. |
| **HSM / cloud-KMS key custody** | Signing is real Ed25519 with a real key lifecycle, but keys live in process memory or a local file, not a hardware module. Adequate for a pilot's trust model; not permanent production custody. |
| **Signature verification without key distribution** | The verifier does real signature checks *when given a trusted-key registry*. Without one it reports `SKIP` — honestly, never a false pass. If your counterparty must verify signatures, you need a key-distribution step. |
| **Distributed tracing / alerting** | Metrics, logs and audit are real. Paging and span-level tracing are not built; your own monitoring stack scrapes `GET /v1/metrics`. |
| **Live external integrations** (real AIS, real eBL platforms, insurer systems) | Not built. Evidence enters through the API from your systems. See §6. |
| **Independent penetration test** | Not yet performed. If your security team requires one before real data, that must be scheduled. |
| **Legal qualification** (MLETR conformance, admissibility) | VERIQO takes no legal position. See the Legal Positioning Statement and Jurisdiction Disclaimer. |

---

## 4. Pilot phases

**Phase 0 — Scoping (before any data).**
Pick 2–5 real historical cases whose outcome you already know, ideally
including one that was contested. Agree what "we could have proven this"
would mean for each. Identify who on your side computes hashes and
submits evidence.

**Phase 1 — Replay history.**
Load those known cases. Do not use live matters yet. You are checking
whether VERIQO's record of a case you already understand matches your
own understanding — and whether the exported dossier would have helped
in the dispute that actually happened.

**Phase 2 — Parallel run.**
Run VERIQO alongside your existing process on live matters. VERIQO is
not the system of record yet. Submit evidence as it arrives, decide in
VERIQO as you decide in your real process, and compare.

**Phase 3 — Third-party test.**
The real test. Export a dossier and give it, with the verifier binary,
to someone outside your team — a counterparty, a broker, your own legal
counsel. Ask them to check it without help from you. Their reaction is
the pilot's actual result.

---

## 5. What success and failure look like

**Success indicators**

- A third party ran the verifier and understood what it told them.
- At least one case produced a dossier that would have shortened or
  strengthened a real dispute.
- Your team could state, for a given case, exactly what was known at
  each point — from the system, not from memory.
- Replay converged on every case (it should; a divergence is a finding
  worth reporting to us immediately).

**Honest failure indicators** — a pilot that produces these has told you
something valuable, and you should not continue:

- Your evidence does not actually arrive in a form anyone hashes or
  preserves, and the discipline change is bigger than the benefit.
- Nobody outside your team cares whether the record is independently
  verifiable, because disputes in your business are settled on
  relationships rather than evidence.
- The cases you care about turn on legal interpretation rather than
  factual record — VERIQO establishes facts and lineage, and takes no
  legal position.

---

## 6. Data you must bring

VERIQO holds hashes and metadata, not your documents. You bring:

- The evidence bytes themselves, in your own storage, at a URI you
  control.
- A SHA-256 computed at acquisition — before the file passes through
  converters, email gateways, or scanning tools.
- Case and evidence identifiers meaningful in your own systems.
- A person who can say what each item is and where it came from. The
  `collector` and `source` fields are only as good as this.

---

## 7. Operating the pilot

- **Health:** `GET /livez` (process alive), `GET /readyz` (fit to serve
  traffic — reports 503 when a dependency is not).
- **Metrics:** `GET /v1/metrics`. Watch
  `evidence_verification_failures`, `custody_chain_failures`,
  `ledger_commit_failures`, and `replay_failures`. Any of these moving
  off zero is a real finding, not noise.
- **Backups:** see the Backup and Restore Procedure. Run at least one
  real restore drill during the pilot rather than trusting that backups
  work.
- **Support:** see the Support and Diagnostics guide for what to collect
  before raising an issue.

---

## 8. Ending the pilot

Whatever the outcome, you keep:

- Your evidence bytes — we never had them.
- Every exported dossier package. They are self-contained and remain
  verifiable with the standalone verifier, with no VERIQO service
  reachable and no licence in force.

Ask for a WAL backup of your tenant's store before shutdown if you want
the full record rather than the exported dossiers alone.
