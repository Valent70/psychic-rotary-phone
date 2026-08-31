# VERIQO Real-World Network Model (P3)

The four real-world networks VERIQO must eventually connect to —
maritime, insurance, commodity, and financial — with, for each, what
exists in code today and what genuinely does not.

**Nothing described as a live integration exists.** Every connector in
this repository is a contract plus a *simulated* source. That is stated
in each package's own doc comment, and — more usefully — enforced in
code: the simulated connectors emit only `ModeSynthetic` and **have no
code path capable of producing `ModeLive`**. A synthetic feed cannot be
mistaken for a real one by accident.

---

## 1. What blocks these integrations

Not engineering. Each requires something only a commercial relationship
produces:

- A **data contract** with a provider (AIS aggregator, market data
  vendor, customs broker, carrier).
- A **counterparty agreement** — an insurer, a bank, a terminal
  operator willing to exchange evidence.
- **Platform membership** — eBL platforms operate under rulebooks with
  admission requirements.
- **Regulatory standing** for some sources (customs, payment rails).

This is why P3 is documented rather than built. Writing a connector
against an API you have no contract to call produces untested code
shaped by a guess at the payload.

---

## 2. The nine questions every integration must answer

Applied uniformly. An integration that cannot answer all nine is not
ready to carry evidence, however well it moves data.

| # | Question | Why it decides evidential value |
|---|---|---|
| 1 | **Source** — who is the authoritative origin? | An aggregator is not the origin. Provenance must record the actual origin, not the last hop. |
| 2 | **Contract** — what are the licensing, redistribution, and retention terms? | Evidence you may not retain or show a counterparty is not evidence you can use in a dispute. |
| 3 | **Identity** — how is the source authenticated, and how are entities it names resolved? | A vessel ID from an unauthenticated feed is an assertion, not a fact. |
| 4 | **Acquisition** — push, pull, batch, streaming? What are the gaps? | Gap behaviour determines whether absence means "did not happen" or "we were not listening". |
| 5 | **Integrity** — what is hashed, and at what point? | Hash at acquisition, before any normalization, or the hash attests to your transformation rather than their data. |
| 6 | **Provenance** — what is recorded about how it arrived? | Must survive into the custody chain and be legible to a challenger years later. |
| 7 | **Retention** — how long, under whose obligation, and what happens on legal hold? | Provider terms and legal-hold obligations conflict more often than expected. |
| 8 | **Reconciliation** — how is it checked against another source? | Single-source evidence is assertion. Reconciliation is where contradiction gets detected. |
| 9 | **Failure handling** — what happens on outage, malformed data, or provider dispute? | Silent gaps are the worst failure mode: they look like normal quiet. |

The existing connector contracts already implement the machinery for
questions 4–6 and 9. Questions 1–3, 7, and 8 are where a real
integration does its distinctive work.

---

## 3. Maritime

| Integration | Built | Not built |
|---|---|---|
| **AIS** | A full streaming connector with reconnect/backoff, a wire-message schema, and hand-written synthetic fixtures explicitly **not** captured from any real feed. No test dials a real address. | Any commercial AIS provider contract. |
| **Port / terminal events** | Nothing specific. | Terminal operating system integrations; each major terminal differs. |
| **Vessel identity / registry** | Vessel identity is recorded as an **asserted** string. | Resolution against IMO/flag-state registries. Today an identifier proves it was asserted, not that it is correct. |
| **eBL platforms** | A Bill of Lading / customs ingestion contract with structural and semantic validation (instrument type known, container numbers well-formed). | Membership of any eBL platform, and its rulebook obligations. |

**Distinctive hard problem:** AIS is *reported by the vessel*, and can
be switched off, spoofed, or misconfigured. Its evidential value is
therefore in **corroboration and contradiction** — an AIS gap alongside
a port record is a finding — not in treating position reports as
ground truth. Any maritime integration that presents AIS as fact rather
than as one attested source has misunderstood the product.

## 4. Insurance

| Integration | Built | Not built |
|---|---|---|
| **Claims / policy evidence** | An ingestion contract with validation and a simulated connector, honestly labeled. | Any insurer or claims-management-system connection. |
| **Broker / adjuster / surveyor** | The evidence model carries `party_id` and `evidence_kind` (`SURVEY`, `ADJUSTER_REPORT`, `FNOL`, `INVOICE`). | Party identity verification; submission workflows for third-party professionals. |
| **P&I clubs** | Nothing specific. | Club-specific reporting requirements. |
| **Claims data** | Full case → decision → action → dossier pipeline. | Bordereaux formats, claims-system schemas. |

**Distinctive hard problem:** the parties are **structurally adversarial
at the margin** — insurer, insured, broker, and surveyor have partly
opposed interests. This is where VERIQO's independent verifiability is
worth most: each party can verify the record without trusting the
others. It is also where the trusted-key-registry question becomes
operationally unavoidable rather than optional, because "who signed
this" is precisely what is in dispute.

## 5. Commodity

| Integration | Built | Not built |
|---|---|---|
| **B/L and customs** | The BoL contract: parsing, structural and semantic validation. | Any carrier or customs EDI feed; this sandbox has no reachable path to one. |
| **Trader systems** | Nothing specific. | CTRM integrations. |
| **Refinery / terminal** | Nothing specific. | Quantity and quality certificate ingestion. |
| **Cargo data** | Trade domain metadata (`document_type`, `transfer_event_id`, `holder_identity`). | Assay results, inspection certificates, loss reports. |

**Distinctive hard problem:** commodity evidence is heavily
**measurement-based** — quantity and quality figures produced by
instruments and inspectors, where disputes turn on *whose measurement*
and *taken how*. The provenance model must capture instrument identity
and method, not merely a number and a source name. The evidence model
supports this; no integration exercises it yet.

## 6. Financial

| Integration | Built | Not built |
|---|---|---|
| **Payment evidence** | A payment/transaction ingestion contract with a simulated connector; explicitly no path to SWIFT, ACH, or any card network. | Any real payment rail. |
| **Trade finance / banks** | Nothing specific. | LC and guarantee lifecycle integration. |
| **Collateral / transaction evidence** | The generic evidence pipeline applies. | Collateral management systems. |

**Distinctive hard problem:** the **regulatory perimeter**. Payment and
trade-finance data carries AML, sanctions, and banking-secrecy
obligations that constrain what may be retained and shown. VERIQO
performs **no** sanctions or watch-list screening and asserts no such
capability. An integration here needs compliance design before
engineering design, and the retention/legal-hold model must be
reconciled with obligations that may *require* deletion and
*prohibit* it in the same dataset.

---

## 7. What a first real integration should look like

Not the largest network — the one where all nine questions are
answerable:

1. **One source, one counterparty.** A single provider with a real
   contract, feeding a single pilot customer's real workflow.
2. **Answer the nine questions in writing before writing code.** The
   answers determine the connector's shape; discovering them afterward
   means rewriting it.
3. **Hash at acquisition**, before normalization, so integrity attests
   to what the provider sent rather than to your processing of it.
4. **Reconcile against something.** A single unreconciled source
   produces attested assertions. The second source is where
   contradictions — the thing customers actually pay to detect — become
   visible.
5. **Make gaps explicit.** Record feed outages as events. Silence that
   is not recorded as silence will later be read as evidence that
   nothing happened.
6. **Keep synthetic and live structurally distinguishable**, exactly as
   the existing connectors do. A live-mode flag that a synthetic path
   can never set is worth more than a convention people are asked to
   respect.

---

## 8. Honest summary

The **contract layer** for external ingestion is real, tested, and
reusable: validation, state machines, failure handling, and a
synthetic/live separation enforced by the type system rather than by
discipline.

The **live provider integrations are entirely absent**, in all four
networks, and are blocked on commercial relationships rather than
engineering capacity.

Any material describing VERIQO's real-world connectivity must say this.
Describing the connector contracts as "AIS integration" or "payment
integration" without the word *simulated* would be exactly the kind of
overstatement the connectors' own doc comments were written to prevent.
