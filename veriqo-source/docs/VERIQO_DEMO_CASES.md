# VERIQO Commercialization Sprint -- Demo Cases (Items 14-17)

This document answers Commercialization Sprint items 14-16 ("3 canonical
demo cases") and item 17 ("customer-facing metrics") by name. Every case
below is real, runnable code, not a mockup: `pkg/commercial/democases`
builds each case through the exact, frozen kernel (`cre` -> `decision` ->
`action`) via the same `pkg/commercial/api.Store` a real Commercial API v1
caller uses, and `cmd/veriqo-demo-cases` writes each one's actual Evidence
Dossier v1 -- both the human Markdown and the machine `.zip` package -- to
disk. Every package this command produces independently verifies against
`cmd/veriqo-commercial-verify`, the standalone verifier a customer would run
without trusting VERIQO's own producing process.

To regenerate all three:

```
go run ./cmd/veriqo-demo-cases <output-dir>
```

To independently verify any one of the resulting packages:

```
go run ./cmd/veriqo-commercial-verify -package <output-dir>/demo1-ebl-transfer-dispute.zip
```

Each case below follows the same four-part honesty framework the reviewer
names: **What VERIQO Knows** (independently, technically verified facts),
**What VERIQO Does NOT Know** (facts outside this evidence set or outside
VERIQO's authority to determine), **What VERIQO Can Prove Technically**
(the specific cryptographic/structural claims a third party can re-check),
and **What Remains a Legal Question** (the determination only a court,
arbitration panel, or the parties' governing contract can make).

---

## Demo 1 -- Case #VRQ-2026-0001: eBL Transfer Dispute

**Scenario.** An electronic Bill of Lading (eBL) is issued to Shipper-Co
and, per the issuing platform's own transfer log, later transferred to
Bank-A. A dispute arises over whether that transfer was valid and whether
Bank-A now holds the eBL. Built by `democases.BuildEBLTransferDisputeCase`.

**What VERIQO Knows**
- The issuance record (`EV-EBL-ISSUANCE`) and the sole recorded transfer
  event (`EV-EBL-TRANSFER-1`) are both present, FINALIZED, and hash-intact.
- Both records were submitted through the same custody-chain state
  machine (RECEIVED -> HASHED -> REVIEWED -> FINALIZED), so their custody
  history is continuous and independently re-derivable.
- The transfer record names Bank-A as the new holder; nothing in this
  evidence set contradicts that record.

**What VERIQO Does NOT Know**
- Whether the eBL platform itself is the sole, authoritative registry for
  this document, or whether a parallel paper original also exists.
- Whether Bank-A's internal onboarding/KYC on this transfer was itself
  valid -- that is outside this evidence set entirely.
- Anything about the underlying trade contract's substantive terms
  (payment terms, INCOTERMS, etc.) beyond the transfer event itself.

**What VERIQO Can Prove Technically**
- `EV-EBL-ISSUANCE` and `EV-EBL-TRANSFER-1`'s manifest hashes independently
  re-derive and match what is recorded (`manifest.VerifyManifestHash`).
- Each record's custody chain is a verified hash chain from GENESIS
  (`manifest.VerifyCustodyChainRecords`).
- The Decision approving the transfer is grounded ONLY in evidence that
  was FINALIZED and hash-verified at authorization time
  (`cre.AuthorizeGrounded`) -- it did not silently accept a stale or
  unfinalized record.
- The Decision, the Action Authorization, and the resulting Receipt form
  a single, independently re-hashable chain (`decision.VerifyDecisionProvenance`,
  `action.VerifyActionAuthorization`).
- Replaying the case's recorded inputs reproduces byte-identical Decision
  and Authorization hashes (`verticalslice.Replay`) -- this is not a
  one-time computation that cannot be checked again later.

**Remains a Legal Question**
- Whether this transfer satisfies MLETR "control" and "exclusive control"
  in the receiving jurisdiction -- see
  `docs/VERIQO_MLETR_EBL_RELIABILITY_CONFORMANCE_MAPPING.md` (an earlier
  round's honest primary-source-verification gap on this exact point).
- Whether the transfer is legally effective against third parties under
  the governing law of the underlying trade contract.
- Any dispute over the substantive commercial terms the eBL evidences.

---

## Demo 2 -- Case #VRQ-2026-0002: Maritime Incident (Position Contradiction)

**Scenario.** An AIS track for MV EXAMPLE and a port authority's berth
arrival log disagree on the vessel's position/time on 2026-01-15. Rather
than silently picking one record, the case is escalated for human review.
Built by `democases.BuildMaritimeIncidentCase`.

**What VERIQO Knows**
- `EV-AIS-TRACK-1` (AIS_STATUS) and `EV-PORT-ARRIVAL-1` (PORT_EVENT) are
  each individually hash-intact and FINALIZED.
- The two records are mutually inconsistent on the vessel's position/time
  for the same voyage leg -- VERIQO surfaced this as a genuine
  contradiction (`Contradictions: EV-PORT-ARRIVAL-1 contradicts hypothesis
  H1` in the generated dossier), not a silently-resolved discrepancy.

**What VERIQO Does NOT Know**
- WHY the two records disagree: AIS spoofing, a transponder fault, a
  delayed or manually-entered port log, or a genuine deviation are all
  consistent with this evidence set. VERIQO does not have the specialized
  maritime-domain intelligence to adjudicate among them from these two
  records alone.
- Which record, if either, is more reliable in general -- that would
  require either a track record of each source's historical reliability
  or additional corroborating evidence not present in this case.

**What VERIQO Can Prove Technically**
- Both records independently hash-verify and custody-chain-verify on
  their own.
- The contradiction itself is a first-class, machine-checkable fact: the
  Finding's `ContradictedBy` field cites `EV-PORT-ARRIVAL-1` by ID, and
  `cre.AuthorizeGrounded` confirmed that record is real and FINALIZED
  before allowing the contradiction to be recorded at all (a fabricated
  or unfinalized "contradiction" cannot enter the Decision).
- The Decision's Outcome is `ESCALATED`, not `APPROVED` or `DENIED` --
  the system's own recorded outcome reflects that a technical
  hash/custody check cannot resolve which record is accurate.
- The resulting `SEND_NOTIFICATION` action (to the underwriter and P&I
  club) is itself authorized against this exact `ESCALATED` Decision, and
  that link independently re-verifies.

**Remains a Legal Question**
- Which record is authoritative for any subsequent claim, dispute, or
  regulatory finding -- that determination belongs to the investigating
  authority, the P&I club's own inquiry, or a tribunal, informed by (but
  not made by) this dossier.
- Any liability arising from an actual deviation, if one occurred.

---

## Demo 3 -- Case #VRQ-2026-0003: Insurance Claim (Marine Cargo)

**Scenario.** A cargo claim under POL-9001 is supported by an independent
surveyor's report and an adjuster's report, both attributing the loss to
water ingress during transit. Built by `democases.BuildInsuranceClaimCase`.

**What VERIQO Knows**
- `EV-SURVEY-1` and `EV-ADJUSTER-1` are both FINALIZED, hash-intact, and
  mutually consistent with the water-ingress hypothesis.
- No contradicting evidence was submitted in this case.

**What VERIQO Does NOT Know**
- Whether the surveyor's or adjuster's professional conclusions are
  themselves correct -- VERIQO verifies that these documents are what
  they claim to be (intact, custody-tracked, attributable to their
  stated source) and were actually cited as the basis for the Finding;
  it does not re-derive their professional conclusions from first
  principles.
- Whether POL-9001's policy wording actually covers this specific loss
  scenario, exclusions included.
- The fair settlement quantum -- `QuantumRef` here names a reference to
  an external calculation (`calc-4471-v1`), not a VERIQO-computed amount.

**What VERIQO Can Prove Technically**
- Both evidence items independently hash-verify and custody-chain-verify.
- The Finding was authorized ONLY because both cited evidence items
  resolved to real, FINALIZED, hash-verified manifests
  (`cre.AuthorizeGrounded`) -- a Finding citing missing or unfinalized
  evidence is refused outright, proven by
  `TestDecideCaseRejectsUngroundedEvidence` in this Store's own test
  suite.
- The `APPROVE_SETTLEMENT` Action Authorization's own hash embeds the
  exact Decision hash it was authorized against
  (`action.VerifyActionAuthorization`), so the settlement authorization
  cannot be silently detached from, or reattached to, a different
  Decision after the fact.
- Replay reproduces identical Decision and Authorization hashes from the
  same recorded inputs.

**Remains a Legal Question**
- Whether the policy, correctly interpreted, covers this loss.
- Whether the settlement amount computed by `calc-4471-v1` is fair or
  contractually correct.
- Any dispute over the surveyor's or adjuster's professional findings
  themselves.

---

## Customer-Facing Metrics (Item 17)

The reviewer's explicit instruction is that these are **pilot hypotheses
to be measured during a real pilot, not universal claims** -- VERIQO has
not run a paid pilot and has no measured before/after data of its own.
The BEFORE column below describes a typical unassisted manual-review
workflow reported in this industry (evidence gathering, cross-referencing
records, cross-team hand-off); the AFTER column is a **target**, not a
result, framed exactly as such.

| Metric | BEFORE (typical manual baseline) | AFTER (pilot hypothesis, target) |
|---|---|---|
| Investigation time per case | Days, spread across multiple systems and hand-offs | Target: reduced by up to 80% for cases whose evidence is fully digital and already in VERIQO |
| Evidence retrieval | Manual search across email, shared drives, and vendor portals | A single case dossier assembling every submitted evidence item with verified integrity |
| Contradictions discovered | Often found late, by a human noticing a discrepancy | Target: surfaced automatically at decision time, as Demo 2 shows |
| False positives (flagged issues that turn out not to matter) | No consistent baseline; varies by reviewer | Target: reduced by 40%, to be measured against a specific pilot's real flag volume |
| Detection lead time (incident to first flag) | Hours to days depending on reporting chain | Target: improved by 12 hours where source evidence already flows into VERIQO in near-real-time |
| Manual review workload | 100% of cases reviewed manually end-to-end | Target: reduced by 60% for the subset of cases where VERIQO's grounded Finding is directly actionable, with human review retained for every escalated or contested case |
| Time to a documented decision | Varies widely; often undocumented until settlement | A Decision, Authorization, and Receipt are recorded and hash-verifiable the moment a human authorizes an action |

**Honest caveat, stated once and meant literally**: every "Target" figure
above is a hypothesis to validate against a specific pilot customer's real
volume and workflow, not a claim already demonstrated by this codebase.
Item 8's marketing-language discipline applies here without exception --
these numbers must never be presented to a prospect as measured results
before a pilot has actually measured them.
