# Real-World Evidence: What Is Missing, Precisely

**Status:** the architecture is IMPLEMENTED. The evidence is a FIXTURE.
**Blocker:** `LIVE_DATA` — `BLOCKED_EXTERNAL`, unchanged.
**Executable form:** `pkg/casefabric/semantics.go` · asserted by `semantics_test.go`

---

## 1. The gap, stated once

VERIQO's six domains now declare their semantics as data: what they work in, what
counts as evidence, what their claims must show, what would falsify them, and how
a case ends. Thirty-five evidence classes across six domains, each naming a real
source.

**Every one of those thirty-five is currently a fixture.** Not a stub, not a
mock — a hand-authored value in a test. The chain executes over them correctly,
which proves the machinery composes. It proves nothing about behaviour on real
rights-aware commercial data.

That is the largest remaining distance between *engineering excellence* and a
*commercial intelligence platform*, and no amount of further engineering closes
it. It closes when somebody signs a data agreement.

---

## 2. What "rights-aware" means here, and why it is the hard part

The naive reading of this gap is "connect the APIs". It is not that.

Every one of these sources arrives with a licence, and the licence governs what
VERIQO may do with the data — not merely whether it may fetch it. The ten
purpose-bound rights in `pkg/disclosure/access` exist because `VIEW`, `EXPORT`,
`AI_PROCESS`, `RAG` and `TRAIN` are five separate grants, and most commercial
data agreements grant some and refuse others.

So a real connector is not an HTTP client. It is:

- a **licence** whose terms are encoded as rights, not summarised in prose;
- an **acquisition authority** check that runs *before* contact (Article 4 —
  a rights failure denies before the request is made, never after);
- a **provenance record** naming source, licence and acquisition path;
- an **independence assessment** — many of these sources are aggregators, and
  three feeds off one root are one source (Article 3);
- a **retention and erasure** position, because commercial data agreements
  routinely require deletion on termination.

`pkg/authz`, `pkg/qualification/independence` and `pkg/governance/data`
already implement all five. What is missing is a real licence to run them against.

---

## 3. The thirty-five classes, and how much is party-mediated

Fourteen of the thirty-five are **party-mediated**: acquired through a party to
the matter rather than independently. That is not a defect — a bill of lading is
the carrier's document and there is no independent copy — but it is a fact about
what those classes can establish, and `Semantics.PartyMediatedClasses()` reports
it per domain rather than letting a domain describe itself more flatteringly than
the evidence allows.

`TestEveryDomainHasPartyMediatedEvidence` fails for any domain claiming all its
evidence is independently acquired, because for a real matter that claim is not
credible.

---

## 4. Per-source acquisition requirements

| Source family | Domains | What acquisition actually requires |
|---|---|---|
| **AIS** (terrestrial + satellite) | maritime | A commercial aggregator agreement. Coverage is uneven and the gaps matter evidentially — an AIS gap is an *absence*, and carries weight only after the nine-condition observability gate |
| **SAR imagery** | maritime | A tasking or archive agreement with an SAR provider. Tasking has lead time, which is a live constraint on what can be evidenced after the fact |
| **Optical EO** | maritime | An EO provider agreement. Cloud cover is a coverage-adequacy question, not an inference |
| **Bill of lading / eBL** | commodity, trade finance | Carrier or MLETR-conformant platform access. Platform reliability is itself an external qualification question — VERIQO records the platform's assertion and does not certify it |
| **Customs** | supply chain | Authority-by-authority access, each with its own legal basis. Not a single integration |
| **Sanctions lists** | supply chain | Issuing-authority feeds, **versioned**: the list in force at the material time governs, not today's list (Article 7) |
| **Insurance / P&I** | insurance | Bilateral agreements with insurers and clubs. Most of what arrives is party-mediated by construction |
| **Port call records** | maritime | Port authority or agent access, jurisdiction by jurisdiction |
| **Weather hindcast** | maritime | A meteorological service agreement; usually the least encumbered of these |
| **Dark web** | (not built) | An acquisition capability that does not exist. It is `DESIGNED` and named as such — see the constitutional proof audit |
| **Aureum** | (not built) | Not built |

---

## 5. What would change when real data arrives — and what would not

**Would change.** The evidence set stops being hand-authored. Independence
assessment starts returning `DEPENDENT` and `UNKNOWN` verdicts that no fixture
produces. Coverage gaps become real, so the observability gate starts refusing
absences that the fixtures let through. Qualification states other than
`SUPPORTED` start appearing, and the reverse-proof gap analysis starts reporting
`Unattempted` requirements nobody can obtain.

That is the point. The fixtures exercise the happy path; real data exercises the
refusals, and the refusals are the product.

**Would not change.** No package. The connectors would populate
`pkg/evidence/manifest` exactly as the fixtures do. The chain, the qualification
rules, the proof object, the case fabric and the ledger are indifferent to where
evidence came from, which is what makes them worth having.

---

## 6. Honest status

| | |
|---|---|
| Architecture for rights-aware acquisition | **IMPLEMENTED** — authz, independence, provenance, retention |
| Domain semantics naming real sources | **IMPLEMENTED** — 35 classes, 6 domains, declared as data |
| Real data agreements | **NONE** |
| `LIVE_DATA` blocker | **BLOCKED_EXTERNAL** — unchanged, and this document does not move it |

---

## Appendix — the six domains as declared

```
=== MARITIME ===
  ONTOLOGY        Vessel, Voyage, Port, Event, Causation, Loss
  EVIDENCE        AIS position track <- AIS aggregator (terrestrial and satellite)
                  SAR imagery <- synthetic-aperture radar provider
                  optical EO imagery <- earth-observation provider
                  port call record <- port authority or agent
                  weather hindcast <- meteorological service
                  vessel log extract <- the operator  [party-mediated]
                  class survey report <- classification society
  OBLIGATIONS     the vessel deviated from its declared route
                    requires: a declared route; a position track covering the window; an observability verdict for any gap in the track
                    falsified by: a position track within the declared corridor for the whole window
                  a dark period was deliberate rather than a coverage gap
                    requires: coverage adequacy for the window; an OBSERVED_ABSENT verdict, not an unattempted one; a rival explanation tested
                    falsified by: a coverage gap in the source that explains the absence without any act of the vessel
  RULES           an AIS gap is an absence, and an absence carries weight only after the nine-condition observability gate
                  vessel identity below the resolution threshold stays unresolved rather than merging two vessels
                  position from a single provider is a single source however many feeds it aggregates
  OUTCOMES        findings_issued, evidence_package_delivered, no_further_action, referred_to_authority

=== COMMODITY ===
  ONTOLOGY        Cargo, Shipment, Document, Loss, Quantum, Causation
  EVIDENCE        independent assay certificate <- accredited laboratory
                  draft survey report <- appointed surveyor
                  loading tally <- the terminal  [party-mediated]
                  sealed sample chain of custody <- the sampling party  [party-mediated]
                  temperature and humidity log <- container telemetry provider
                  bill of lading <- the carrier  [party-mediated]
  OBLIGATIONS     the cargo was off-specification before loading
                    requires: a pre-load sample analysed by an independent laboratory; an unbroken sample chain of custody; the in-transit hypothesis tested
                    falsified by: a clean pre-load sample from a properly sealed and custodied specimen
                  the shortage arose in carriage rather than at loading
                    requires: a load-port figure; a discharge-port figure; a stated measurement tolerance for both
                    falsified by: a discrepancy within the combined measurement tolerance of the two surveys
  RULES           an assay from a laboratory a party appointed is party-mediated, and independence is assessed accordingly
                  quantity claims are never established from a single measurement without a stated tolerance
                  a sealed sample with a broken custody chain is evidence of the break, not of the contents
  OUTCOMES        quality_determined, evidence_package_delivered, no_further_action, referred_to_arbitration

=== SUPPLYCHAIN ===
  ONTOLOGY        Shipment, Organization, Event, Breach, Responsibility, Timeline
  EVIDENCE        customs declaration <- the customs authority
                  sanctions and screening list <- the issuing authority
                  carrier milestone feed <- the carrier  [party-mediated]
                  supplier audit report <- an appointed auditor
                  certificate of origin <- the issuing chamber or authority
                  purchase order and contract <- the transacting parties  [party-mediated]
  OBLIGATIONS     the disruption originated at a named tier-2 supplier
                    requires: a traced chain from the disruption to that tier; the intermediate tiers excluded; an alternative origin tested
                    falsified by: an intermediate tier that independently accounts for the disruption
                  the consignment involves a sanctioned party
                    requires: the list version in force at the material time; an identity match above the resolution threshold; the beneficial-ownership chain where the listing reaches it
                    falsified by: a name match that fails identity resolution, which is a coincidence of names
  RULES           a sanctions name match is a hypothesis until identity resolution clears the threshold; it is never a finding on its own
                  the list version in force at the material time governs, not today's list -- Article 7
                  a carrier's own milestone feed is party-mediated and is not independent corroboration of the carrier's performance
  OUTCOMES        origin_established, evidence_package_delivered, no_further_action, referred_to_authority

=== INSURANCE ===
  ONTOLOGY        Claim, Policy, Loss, Quantum, Causation, Obligation
  EVIDENCE        policy wording and endorsements <- the insurer  [party-mediated]
                  loss adjuster report <- an appointed adjuster
                  P&I club correspondence <- the club  [party-mediated]
                  repair invoice and quotation <- the repairer
                  survey report <- an appointed surveyor
                  notice of claim <- the claimant  [party-mediated]
  OBLIGATIONS     the loss falls within the policy's insured perils
                    requires: the policy version in force at the loss; the proximate cause established; each exclusion considered and addressed
                    falsified by: an exclusion that applies on the established facts
                  the quantum is the sum claimed
                    requires: an evidence-backed amount for every component; the deductible and limits applied; any betterment identified
                    falsified by: a component with no evidential backing, or double-counting between components
  RULES           coverage is a question for the insurer and the policy; VERIQO establishes facts and does not determine liability
                  every amount cites the evidence it rests on; an unbacked figure is not a quantum
                  payment authority and payment execution authority are disjoint role sets -- one party never does both
  OUTCOMES        evidence_package_delivered, quantum_computed, no_further_action, referred_to_arbitration

=== TRADEFINANCE ===
  ONTOLOGY        Contract, Clause, Document, Transaction, Obligation, Breach
  EVIDENCE        documentary credit and amendments <- the issuing bank
                  presented document set <- the beneficiary  [party-mediated]
                  electronic bill of lading record <- an MLETR-conformant eBL platform
                  inspection certificate <- the named inspection body
                  SWIFT message trace <- the messaging network
  OBLIGATIONS     the presentation is compliant with the credit
                    requires: the credit and every amendment in force; each required document present; each discrepancy identified against a specific term
                    falsified by: a document that fails a stated term of the credit
                  the eBL holder is the party claiming to be
                    requires: the platform's transfer record; an unbroken transfer chain from issuance; the platform's own reliability assessment
                    falsified by: a transfer in the chain that the platform cannot evidence
  RULES           discrepancy is determined against a stated term of the credit, never as a general impression of the document set
                  an eBL platform's reliability is an external qualification question; VERIQO records the platform's assertion and does not certify it
                  a document a party presented is party-mediated evidence of the transaction, not independent evidence of the facts it recites
  OUTCOMES        determination_issued, evidence_package_delivered, no_further_action, referred_to_issuing_bank

=== DISPUTE ===
  ONTOLOGY        Claim, Counterclaim, Contradiction, ProofObligation, Timeline, Finding
  EVIDENCE        pleadings and statements of case <- the parties  [party-mediated]
                  disclosed document set <- the disclosing party  [party-mediated]
                  expert report <- an appointed expert
                  witness statement <- the witness  [party-mediated]
                  contemporaneous record <- whichever party held it  [party-mediated]
  OBLIGATIONS     the evidence package is complete for the issues framed
                    requires: every framed issue decomposed into supporting, contradicting and missing evidence; each party's position recorded verbatim; the legal questions separated from the factual ones
                    falsified by: a framed issue with no evidence decomposition
  RULES           VERIQO furnishes facts, evidence, timelines, contradictions, causation hypotheses, quantum and proof obligations; the arbitrator, court or authorized decision-maker decides
                  two parties' positions sitting side by side, unreconciled, IS the output -- neither is weighted
                  a legal question is marked AWAITING_LEGAL_INTERPRETATION and left there; the system does not answer it
  OUTCOMES        evidence_package_delivered, no_further_action, matter_withdrawn

```
