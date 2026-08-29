# VERIQO -- MLETR / eBL Reliability Conformance Mapping v0.2

## A note on provenance before anything else

The reviewer's instruction (`Mengubah_fokus_VERIQO_sekarang.docx`) asks for
"Revision 0.2" of a prior positioning document -- referred to in that
docx as "dokumen Claude" -- with four named changes. That prior document
is **not present anywhere in this repository or in this session's own
history** (a repo-wide search of `docs/*.md` and every PDF filename
under `docs/` for "MLETR", "eBL", and "Article 12(b)" found nothing). It
was evidently produced in a different conversation this session has no
access to.

Rather than guess at text this session has never seen, this is a
**freshly constructed v0.2** that applies all four requested changes and
the new positioning sentence directly, grounded in VERIQO's real,
already-built, already-tested technical capabilities (drawn from six
rounds of audited work in this repository) rather than in an
unverifiable prior draft. Where the original document's exact wording
would matter (e.g. for a diff against "what changed"), that comparison
cannot honestly be made here -- only the requested *end state* is
produced.

## Primary-source verification attempt (P1, this round)

The reviewer's follow-up instruction (`Yang_selesai_dan_masih_harus_diselesaikan.docx`)
explicitly asked: "Verifikasi sumber hukum primer untuk Articles 9-18
dan jangan publish low-confidence mapping sebagai settled legal
conclusion" (verify primary legal sources for Articles 9-18, and do not
publish a low-confidence mapping as a settled legal conclusion).

This was genuinely attempted this round, not skipped. `WebSearch`
located the correct official source (UNCITRAL's own MLETR ebook PDF at
`uncitral.un.org`), and `WebFetch` was then used to try to retrieve its
actual text. **The attempt failed for infrastructure reasons, not lack
of trying**: this session's network egress proxy blocks every domain
tried -- `uncitral.un.org` (the official UNCITRAL source),
`en.wikipedia.org`, `www.wto.org`, and `unece.org` were all rejected
with `EGRESS_BLOCKED` before any content could be retrieved. No
alternative unblocked mirror of the primary MLETR text was found within
this round's effort.

**The practical consequence, stated exactly as the reviewer's own
instruction requires: nothing in this document is published as a
settled legal conclusion.** Every article-level claim below already
carried (from the first v0.2 draft, before this verification attempt)
an explicit confidence label and a "verify against official text"
caveat; that discipline is unchanged and, if anything, now has extra
weight, since primary-source verification was attempted and concretely
could not be completed this round. Articles 13, 16, 17, and 18 remain
explicitly LOW CONFIDENCE below. Articles 9, 11, and 12 are labeled
HIGHER confidence only because their general function is widely and
consistently documented across public MLETR explanatory literature this
session already has general knowledge of -- **that is still not the same
as primary-source verification, and is not represented as such
anywhere in this document.**

**This document is a technical-evidentiary self-assessment written by an
engineering audit process. It is not a legal opinion, and it does not
constitute legal advice.** Every claim about legal effect, evidentiary
weight, or regulatory conformance below must be independently validated
against (a) the authoritative UNCITRAL MLETR 2017 text and any
explanatory notes, (b) the specific enacting legislation of whichever
jurisdiction(s) a transaction is governed by (MLETR is a *model* law --
enacting states adapt it), and (c) qualified legal counsel, before this
document is relied upon externally with any party named in it (an eBL
platform, a marine surveyor, a P&I club, a trade-finance bank, an
insurer, or a court). This caveat is not boilerplate -- it is the
literal content of CHANGE 4 below, restated up front so it cannot be
skimmed past.

---

## CHANGE 1 -- The core claim, reworded

**Was:** "Veriqo is built for Article 12(b)."

**Now:**

> "Veriqo is architected to provide transaction-specific evidence that
> may support Article 12(b) where the relevant MLETR function has in
> fact been fulfilled."

The difference is not stylistic. The original phrasing implies Veriqo
itself *satisfies* a statutory requirement. The revised phrasing states
what Veriqo actually does: it produces evidence *about* whether a
transaction's electronic transferable record (ETR) practice satisfied a
reliable-method standard -- the legal conclusion of whether that
standard was actually met is for the enacting jurisdiction's law, the
parties' chosen platform and process, and ultimately a court or
regulator to determine. Veriqo is evidentiary infrastructure, not a
compliance certification authority.

---

## CHANGE 2 -- Article 12 (change of medium): a 7-factor technical mapping

Article 12 of MLETR addresses **change of medium** -- the functional-
equivalence requirements that must hold when a transferable document or
instrument moves between paper and electronic form (or back), so that
the change does not break the continuity of the "reliable method"
standard the record depends on, and does not create two simultaneously
valid originals.

The seven factors below are this engineering assessment's own working
decomposition of what a change-of-medium event must be able to
demonstrate to a third party -- they are **not** a verbatim reproduction
of Article 12's sub-paragraph numbering (which requires the official
UNCITRAL text and counsel to map precisely; see the provenance note
above). They are organized to match what a technical evidence system
can actually attest to.

| Article 12 factor | What MLETR asks | What Veriqo provides | Evidence artifact | Current status | External dependency | Independent validation required |
|---|---|---|---|---|---|---|
| **1. Reliability of method** | The method used for the change of medium must be as reliable as appropriate for the purpose | A hash-chained, JCS-canonicalized event record of the change-of-medium act itself (who initiated it, when, under which policy) | `manifest.CustodyEvent` (action=TRANSFORMED/DERIVED) chained via `EventHash`/`PreviousHash` | Engineering Verified (Level 2) -- real, tested code in this repo | The reliable-method *standard itself* (what counts as "appropriate") is set by the enacting legislation and/or the eBL platform's own rules, not by Veriqo | Yes -- a qualified assessor must confirm the specific hash-chain/canonicalization scheme meets the applicable reliability threshold in the relevant jurisdiction |
| **2. Integrity through the change** | The record's content must be demonstrably unchanged in substance across the medium change | `ManifestHash`/`computeManifestHash` -- a deterministic, content-only hash independently re-verifiable via `VerifyManifestHash` | `Manifest.ManifestHash` + `VerifyManifestHash` | Engineering Verified -- proven by adversarial tampering tests (`TestVerifyManifestHashDetectsTampering`) | None for the hash mechanism itself; the *upstream* content capture (was the paper document faithfully digitized?) is outside Veriqo's control | Yes -- the digitization/capture step upstream of Veriqo needs its own independent attestation |
| **3. Singularity (no duplicate originals)** | Only one version of the record may be capable of exercising the underlying rights at any time | `StateFinalized` immutability + `Supersede`'s single-successor lineage (a FINALIZED version can only be superseded once, never edited) | `manifest.Registry.Advance`/`Supersede`, `ErrFinalizedIsImmutable`, `ErrAlreadySuperseded` | Engineering Verified -- proven by `TestFinalizedManifestIsImmutable`, `TestSupersedeRefusesAnUnfinalizedParent` | Whether the PAPER original was actually deprived of legal effect at the moment of change is an act outside Veriqo (a platform/registry/carrier action), not something Veriqo's own hash chain can observe or enforce | Yes -- singularity of the *legal* instrument requires the eBL platform's own exclusivity mechanism; Veriqo can only attest to singularity within its own evidence record |
| **4. Identification of the controller at the moment of change** | The person entitled to control the record at the time of the medium change must be identifiable | Custody-event `Actor` field, attributed and hash-chain-covered | `CustodyEvent.Actor`, `hasCustodyActionBoundToContent` | Engineering Verified for the custody-event mechanism itself | Actor identity assurance (was "PTY-1" really that party?) depends on an external identity/authentication system feeding Veriqo, e.g. `pkg/security/identity`'s SPIFFE-based provider, which is not yet wired into this custody-event path (see the companion OS Integration Audit gap document) | Yes -- identity binding strength depends entirely on the upstream identity provider actually used in a given deployment |
| **5. Time and place of the change** | When and (where relevant) where the change of medium occurred must be recorded | `CustodyEvent.Tick` and `Manifest.FinalizedAt`, covered by the same hash chain | `CustodyEvent`, `Manifest.FinalizedAt` | Engineering Verified for the recording mechanism | Veriqo's `tick` is an application-defined monotonic counter, not a certified/synchronized wall-clock timestamp or trusted timestamping authority (RFC 3161 or equivalent) unless the deployment wires one in | Yes -- if legal "time" precision is required, a trusted timestamping authority should sit upstream of or alongside the tick value |
| **6. Non-repudiation / attributability** | The party responsible for the change of medium action must not be able to credibly deny it | Hash-chain covering `Actor` + `ContentHash` + `PreviousHash`, refusing any reordering or substitution (`TestReorderedCustodyChainFailsVerification`) | Custody hash chain | Engineering Verified for internal consistency | Cryptographic non-repudiation in the strict sense (a signature the actor cannot deny producing) requires the signing infrastructure in `pkg/platform/security/keys` to actually be wired to custody-event submission, which is not yet the case in this repository today | Yes -- true non-repudiation requires per-actor signing keys bound to custody events, a real but currently unbuilt integration |
| **7. Independent verifiability / replay** | An independent party must be able to confirm, after the fact, that the change of medium happened as claimed | Deterministic replay: an independent party can reconstruct the same `ManifestHash`/`CustodyChainHead` from the same event sequence on a fresh registry | `TestReplayReproducesIdenticalFinalizedState`, and (as of the most recent round) a real multi-node raft consensus proof (`TestManifestCluster_FinalizedEvidenceConvergesAcrossRealConsensus`) | Engineering Verified, and the strongest-evidenced row in this table -- this is VERIQO's core differentiator | The independent party still needs access to the event log/snapshot and the verification tooling; Veriqo does not yet ship a standalone, non-Go, third-party-operable verifier | Yes -- for court/regulator use, an independently-implemented (not just independently-run) verifier would strengthen the claim further |

---

## CHANGE 3 -- MLETR function boundary table

This table is reproduced exactly as specified by the reviewer, since it
was handed over as ready content rather than column headers to fill in:

| MLETR function | eBL platform | VERIQO |
|---|---|---|
| Identify ETR | PRIMARY | Evidence support |
| Integrity | PRIMARY | STRONG |
| Exclusive control | PRIMARY | OUT OF SCOPE |
| Identify controller | PRIMARY | Evidence support |
| Transfer control | PRIMARY | Evidence capture/verification |
| Singularity | PRIMARY | Evidence support |
| Time/place | PRIMARY | Strong evidence support |
| Transaction auditability | Shared | VERY STRONG |
| Independent replay | Usually external capability | VERIQO CORE |

Read plainly: for every function that actually CONSTITUTES the legal
instrument (identifying the ETR, holding exclusive control, transferring
control), the eBL platform is PRIMARY and Veriqo is, at most, an
evidence-support layer sitting alongside it -- and for "Exclusive
control" specifically, Veriqo is explicitly OUT OF SCOPE: Veriqo does
not hold, assert, or adjudicate who currently controls an ETR. Where
Veriqo is strong-to-core is in the two functions a platform typically
does NOT specialize in: transaction auditability and independent
replay -- i.e., proving after the fact what happened and letting someone
who was not part of the original transaction verify it independently.

---

## CHANGE 4 -- What Veriqo cannot claim

Stated explicitly, because the reviewer is right that this increases
credibility rather than undermining it:

- **Veriqo cannot claim to make an ETR "reliable."** Reliability under
  MLETR is a property of the *method* used by the platform/process that
  issues, controls, and transfers the record. Veriqo can produce
  evidence about how that method performed in a specific transaction; it
  cannot retroactively make an unreliable method reliable, and it does
  not certify a method as reliable in the abstract.
- **Veriqo cannot claim exclusive control.** "Exclusive control" is a
  legal/technical property the eBL platform itself must establish and
  maintain (see the boundary table above). Veriqo has no mechanism that
  asserts, grants, or arbitrates who currently controls an ETR.
- **Veriqo cannot claim to be a system of record for the ETR itself.**
  The eBL platform is the system of record for the transferable
  instrument. Veriqo is a system of record for *evidence about* events
  concerning that instrument -- a distinct, narrower claim.
- **Veriqo cannot claim cryptographic non-repudiation today.** As Change
  2's table notes, per-actor signing is not yet wired to custody-event
  submission in this repository. Until it is, "non-repudiation" claims
  should be limited to internal hash-chain consistency, not
  cryptographic proof against a specific signer.
- **Veriqo cannot claim identity assurance.** Custody-event actors are
  recorded as attributed strings; whether "PTY-1" really was the party
  claimed depends on the identity system feeding Veriqo in a given
  deployment, which is not yet integrated (see the companion OS
  Integration Audit document).
- **Veriqo cannot claim trusted timestamping.** Tick values are
  application-defined, not RFC 3161 (or equivalent) certified
  timestamps, unless a deployment separately wires one in.
- **Veriqo cannot claim legal conformance with MLETR or any enacting
  jurisdiction's legislation.** Every mapping in this document is a
  technical-evidentiary self-assessment, not a legal determination. Only
  qualified counsel, applied to a specific transaction and a specific
  jurisdiction's enacted law, can make that determination.
- **Veriqo cannot claim its distributed-replication and snapshot-
  restoration proofs were run over a real network.** The most recent
  engineering round proved real multi-node raft consensus using this
  repository's own in-process transport (`raftlite.MemTransport`), not
  the full mTLS `pkg/transport/rafttcp` + multi-process/Docker harness.
  This is a genuine, real consensus proof -- but it is not yet a
  real-socket, real-process, real-network-partition proof for the
  Evidence/Manifest authority layer specifically. Stated honestly rather
  than implied as complete.
- **Veriqo cannot claim system-level authority assurance yet.** Every
  hardening round to date has proven the Evidence/Manifest/Hypothesis/
  Finding authority packages safe *in isolation*. Whether that safety
  survives integration with VERIQO's own API, Workflow, Knowledge,
  Intelligence, Decision, Ledger, and Cluster layers is the subject of
  the separate, not-yet-executed "OS Integration Audit" -- see the
  companion gap-analysis document delivered alongside this one.

---

## The one-sentence positioning statement

Per the reviewer's own request, verbatim:

> "Veriqo does not make an electronic transferable record legally
> reliable; it makes the evidence of how a reliable method performed in
> a specific transaction independently verifiable."

This is the load-bearing sentence for the whole document: it separates
Veriqo's actual, provable claim (evidentiary verifiability) from the
claim it must never make (legal reliability), in one line simple enough
to survive being quoted out of context by a lawyer, regulator, or
investor.

---

## Toward the fuller mapping (Article 9/10/11/12/13/16/17/18)

The reviewer's final verdict names the next high-value deliverable as a
full, line-by-line "MLETR/eBL Reliability Conformance Mapping v0.2" (now
this document) extending across Articles 9, 10, 11, 12, 13, 16, 17, and
18: Article -> eBL function -> Veriqo capability -> evidence artifact ->
technical proof -> external dependency -> legal boundary -> readiness
status.

This v0.2 delivers that structure fully for Article 12 (Change 2's
table) and the cross-cutting function boundary (Change 3's table). A
best-effort, GENERAL-FUNCTION-LEVEL first pass at the remaining seven
articles follows below, using the same eight-column structure -- but
each article's *general function* (not its verbatim statutory text,
which this session cannot independently confirm without the official
UNCITRAL text) is described in normal legal-technology terms, flagged
individually as low-confidence where this session's knowledge is
general rather than clause-verified, and every row repeats the same
external-dependency and validation-required discipline as Change 2.

| Article (general function, not verbatim text -- verify against official MLETR text) | eBL function | Veriqo capability | Evidence artifact | Technical proof | External dependency | Legal boundary | Readiness status |
|---|---|---|---|---|---|---|---|
| **Art. 9** -- General reliability standard for the method used to identify a person and indicate that person's intention (the umbrella functional-equivalence test) | Establishes and operates the reliable method itself | Evidence that a specific instance of the method executed as designed (inputs, outputs, actor, time) | `CustodyEvent` chain, `ManifestHash` | `TestReplayReproducesIdenticalFinalizedState`, hash-chain adversarial tests | The reliability *standard* (what threshold counts as reliable) is set by the platform + enacting law, not Veriqo | Veriqo does not set or certify the reliability threshold | Engineering Verified (evidence layer only) |
| **Art. 10** -- Control (functional equivalent of possession) | Establishes and enforces exclusive control | NONE -- explicitly out of scope (Change 3) | n/a | n/a | Entirely the eBL platform's responsibility | Veriqo makes no control-related claim | Out of scope by design |
| **Art. 11** -- Transfer of control (functional equivalent of endorsement/delivery) | Executes and records the transfer act | Evidence capture of the transfer event as reported to Veriqo (custody event) | `CustodyEvent` (action reflecting transfer), hash chain | Same custody-chain tamper-detection tests as Change 2 row 6 | Veriqo does not itself effect or validate the transfer -- it records what the platform reports | Veriqo's record is evidence OF a claimed transfer, not proof the transfer was legally effective | Engineering Verified (evidence capture only) |
| **Art. 12** -- Change of medium | See Change 2's full 7-factor table above | (see above) | (see above) | (see above) | (see above) | (see above) | Engineering Verified, most fully mapped article in this document |
| **Art. 13** -- (general function believed to concern requirements applicable to the use of ETRs / party autonomy considerations; this session's confidence on the precise scope of this specific article number is LOW without the official text) | Varies by the specific requirement | Evidence-layer support only where the requirement concerns integrity/attribution/timing, which Veriqo already provides generically | Same mechanisms as above, applied generically | Same test suite | Depends on the specific requirement | **Flagged for review**: this row should not be relied on until the specific article text is confirmed | Low confidence -- requires official text before further mapping |
| **Art. 16** -- (general function believed to concern amendment of an electronic transferable record; LOW confidence on precise scope) | Executes and records amendments | Evidence capture of an amendment event, superseded-version lineage | `manifest.Registry.Supersede`, `MarkSuperseded` | `TestSupersedeCreatesNewVersionWithoutRewritingHistory` | Whether an "amendment" under this article maps cleanly onto Veriqo's "Supersede" concept needs legal confirmation | **Flagged for review** | Low confidence -- plausible technical mapping, unconfirmed legal mapping |
| **Art. 17** -- (general function believed to concern replacement of an electronic transferable record with a paper-based transferable document or instrument, or the reverse; LOW confidence) | Executes the replacement/reconversion | Same change-of-medium evidence mechanisms as Article 12, applied in reverse | Same as Change 2's table | Same as Change 2's table | Same dependencies as Article 12 | **Flagged for review** -- may substantially overlap Article 12's own scope; needs counsel to disambiguate | Low confidence |
| **Art. 18** -- (general function believed to concern requirements for cross-border recognition of electronic transferable records; LOW confidence) | Ensures the ETR is recognized across jurisdictions | Veriqo's evidence artifacts are jurisdiction-agnostic by construction (content-hash-based, no jurisdiction-specific encoding) -- a NEUTRAL property that MAY be relevant to cross-border recognition arguments, but this is a legal argument this session cannot make | n/a | n/a | Cross-border legal recognition is entirely a matter of the enacting jurisdictions' own law and any applicable treaty/reciprocity arrangement | **Flagged for review** -- do not present Veriqo's jurisdiction-agnostic evidence format as itself satisfying any cross-border recognition requirement without counsel confirming the argument | Low confidence |

**Explicit flag:** the rows for Articles 13, 16, 17, and 18 are marked
LOW CONFIDENCE because this session does not have verified access to the
precise UNCITRAL MLETR article text for those specific numbers and is
describing plausible, generally-understood functions rather than
clause-verified ones. Publishing this table externally without first
replacing those four rows' descriptions with counsel-verified text would
be exactly the overclaiming this whole revision exercise exists to
prevent. Articles 9, 11, and 12 are HIGHER confidence because their
general function (reliability standard, transfer, change of medium) is
widely and consistently documented in public MLETR explanatory
materials and is consistent with how the reviewer's own docx used them.
None of the eight articles in this table -- 9, 11, and 12 included --
have been checked against primary UNCITRAL source text by this session:
a genuine attempt was made (see "Primary-source verification attempt"
near the top of this document) and was blocked by network egress
restrictions before any official text could be retrieved. "Higher
confidence" above means general-knowledge confidence, not
primary-source-verified confidence, and this document does not conflate
the two.
