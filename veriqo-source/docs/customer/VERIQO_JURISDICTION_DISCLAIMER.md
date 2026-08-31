# VERIQO Jurisdiction Disclaimer

**This document is not legal advice.** It is written by the engineering
team that builds VERIQO, not by lawyers, and it is not a legal opinion
about any jurisdiction. Obtain independent legal advice in each
jurisdiction where you intend to rely on VERIQO output.

---

## 1. The disclaimer

VERIQO produces technical records: hashes, custody chains, decisions
grounded on cited evidence, and tamper-evident audit trails. Whether
such a record:

- is **admissible** in a particular court, tribunal, or arbitration;
- carries **evidential weight**, and how much;
- satisfies a **statutory or regulatory** record-keeping obligation;
- meets the requirements of a **platform's own rulebook**;
- constitutes a valid **electronic transferable record** or satisfies
  MLETR-derived legislation;
- suffices to **transfer, endorse, or discharge** any right or
  obligation,

is determined entirely by applicable law, the rules of the relevant
forum, the terms of contracts between the parties, and the judgment of
qualified counsel in that jurisdiction. **VERIQO makes no representation
on any of these points.**

Producing a record with VERIQO does not create, transfer, extinguish, or
evidence any legal right. It records technical facts about artifacts and
decisions.

---

## 2. Why no jurisdiction list is given

It would be straightforward to publish a table of jurisdictions with
green ticks. It would also be misleading, and this project does not do
it.

- The engineering team is not qualified to render legal opinions in any
  jurisdiction.
- Attempts to verify conformance claims against primary legal sources
  could not reach an authority able to confirm them from this
  engagement's position — documented in
  `docs/VERIQO_MLETR_EBL_CONFORMANCE_MAPPING_V0_2.md`.
- Admissibility and evidential weight are usually decided
  case-by-case, on the facts, not conferred categorically on a
  technology.
- Legislation implementing model laws differs materially between
  enacting states; "MLETR-based" does not mean "identical".

A vendor-published jurisdiction table would invite exactly the reliance
this disclaimer exists to prevent.

---

## 3. Electronic transferable records specifically

Where VERIQO is used in connection with electronic bills of lading or
other electronic transferable records, note:

- Legal effect typically depends on the **platform or system of record**
  under which the instrument is issued and transferred, and on that
  platform's rulebook and the contractual framework binding its
  participants.
- VERIQO is normally **evidence infrastructure around** those events —
  recording that a transfer event occurred, with what document hash and
  what custody — rather than the system in which the instrument itself
  exists.
- Nothing VERIQO does substitutes for the legal requirements of a
  reliable system under applicable transferable-records legislation, and
  no assessment against those requirements has been obtained.

The approved formulation:

> *"VERIQO is designed to provide technical evidence infrastructure
> around electronic transferable record events; legal effect and
> jurisdiction-specific MLETR qualification remain subject to applicable
> law, platform rules and independent legal review."*

---

## 4. Data protection and cross-border transfer

Where VERIQO processes personal data, the customer is responsible for
establishing the lawful basis, completing any required assessment, and
putting a data processing agreement in place. VERIQO's technical
retention, legal-hold, and purge controls are described in
`VERIQO_DATA_HANDLING_POLICY.md` — they are technical controls
available to support compliance, not compliance itself.

Note the deliberate design constraint stated in that policy: the audit
ledger is hash-chained and append-only, so **VERIQO can record that a
record was purged but cannot make it as though the record never
existed.** Where an erasure obligation would require the latter, this
must be resolved before a pilot begins.

Data residency is determined by where the deployment runs, which is a
deployment decision, not a property of the software.

---

## 5. Sanctions, export control, and regulated activity

VERIQO is general-purpose evidence infrastructure. Using it in
connection with maritime, trade, commodity, insurance, or trade-finance
activity does not relieve any party of obligations under sanctions
regimes, export controls, anti-money-laundering rules, or
sector-specific regulation. VERIQO performs **no** screening of parties,
vessels, cargoes, or counterparties against any sanctions or watch list,
and asserts no such capability.

---

## 6. No verification of asserted identities

VERIQO records identifiers as asserted by the submitting party — vessel
identities, policy numbers, party identifiers, holder identities. It
does **not** validate them against any authoritative registry. An
identifier appearing in a VERIQO record is evidence that it was
asserted, not that it is correct.

---

## 7. Reliance

Any party receiving a VERIQO dossier — a counterparty, insurer,
adjuster, broker, tribunal, or court — should treat it as a technical
record whose integrity can be independently checked, and should form
their own view, with their own advisors, on what it means legally.

Every dossier carries its own Limitations section stating what it does
not establish. That section is part of the document and should be read
with it.

---

## 8. Precedence

Where this disclaimer conflicts with marketing material, a proposal, a
presentation, or anything said in conversation, **this disclaimer
prevails**. If you have been told something inconsistent with it, treat
that statement as withdrawn and ask for it in writing.
