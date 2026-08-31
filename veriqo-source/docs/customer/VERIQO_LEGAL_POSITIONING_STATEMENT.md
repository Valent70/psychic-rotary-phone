# VERIQO Legal Positioning Statement

The language VERIQO uses about itself, and the language it will not use.
This is a discipline document: it exists so that no VERIQO document,
proposal, dossier, or conversation overstates what the technology
establishes.

**This document is not legal advice**, and nothing in it constitutes a
legal opinion. See `VERIQO_JURISDICTION_DISCLAIMER.md`.

---

## 1. The positioning, in one sentence

> **VERIQO is technical evidence infrastructure.** It establishes the
> integrity, provenance, custody, and lineage of records and of the
> decisions grounded on them, in a form a third party can verify
> independently. It does not determine legal effect, admissibility, or
> evidential weight — those are questions of applicable law, forum
> rules, and independent legal judgment.

---

## 2. Language VERIQO uses

| Use | Because |
|---|---|
| "Court and arbitration **evidence support**" | Describes assisting a process, not guaranteeing an outcome in it. |
| "**Independently verifiable** record" | Precisely true: a separate binary verifies it without trusting us. |
| "**Tamper-evident** audit trail" | Accurate: alteration is *detectable*. Not "tamper-proof". |
| "Establishes **integrity and lineage**" | The actual technical claim. |
| "Evidence infrastructure **around** electronic transferable record events" | Positions VERIQO beside the legal instrument, not as it. |
| "**Designed to support** [obligation]" | Honest about intent without asserting compliance. |

## 3. Language VERIQO does not use

| Never say | Why |
|---|---|
| "Court-admissible" / "admissible evidence" | Admissibility is decided by a court under its own rules. No vendor can confer it. |
| "Legally binding" | VERIQO records facts about records; it does not create legal obligations. |
| "Tamper-proof" | Overclaims. Data can be destroyed or truncated; what VERIQO guarantees is *detection*. |
| "MLETR-compliant" / "MLETR-qualified" | A legal qualification requiring jurisdiction-specific analysis VERIQO has not obtained. |
| "Proves the document is genuine/truthful" | Integrity is not veracity. An intact document can be false. |
| "Guarantees you will win the dispute" (in any phrasing) | Not a claim any evidence system can make. |
| "Certified" / "accredited" (unqualified) | VERIQO holds no certification or accreditation. |
| "Production-qualified" (at present) | Not true today. See the Readiness Tier Framework. |
| "Fully cryptographically independent verifier" (unqualified) | See §5. |

---

## 4. The integrity/veracity distinction

The most important line in this document.

**VERIQO can establish:** that this artifact is byte-identical to the
one submitted; that its custody chain is unbroken; that the decision
cited evidence which genuinely existed and was finalized; that the
audit ledger has not been altered; and — with key distribution — which
key attested to it.

**VERIQO cannot establish:** that the artifact's *contents are true*.
A surveyor's report can be perfectly intact, in unbroken custody,
cryptographically signed, and simply wrong. A hash proves nothing about
honesty.

Any VERIQO material that blurs this line is defective and should be
corrected.

---

## 5. Positioning the independent verifier

The verifier is a genuine differentiator and should be described
accurately:

> VERIQO's independent verifier is a separate binary that checks a
> dossier package using only the file itself — package hash, manifest
> integrity, raw evidence hashes, custody chain, Merkle root, lineage
> against the independently-parsed ledger, and the ledger's own hash
> chain from genesis. **Signature and key-state verification are
> cryptographically real when the verifying party supplies a trusted key
> registry obtained through their own channel**; absent such a registry
> those two checks report `SKIP` — explicitly, never a false pass.

Do not describe verification as "complete" or "fully cryptographic"
without stating the trusted-key-registry condition. Do not describe the
HTTP verification route as independent verification — it runs in the
server whose claims may be in dispute.

---

## 6. Positioning MLETR and electronic transferable records

VERIQO has not obtained a legal opinion confirming MLETR conformance in
any jurisdiction, and cannot produce one. Repeated attempts to verify
this against primary legal sources are documented in
`docs/VERIQO_MLETR_EBL_CONFORMANCE_MAPPING_V0_2.md`.

The approved formulation, to be used verbatim where the topic arises
including with investors:

> *"VERIQO is designed to provide technical evidence infrastructure
> around electronic transferable record events; legal effect and
> jurisdiction-specific MLETR qualification remain subject to applicable
> law, platform rules and independent legal review."*

Note what this does: it states a real technical capability, and locates
legal qualification where it belongs — with applicable law and
independent counsel. It neither claims conformance nor pretends the
subject is irrelevant.

**Scope note:** MLETR qualification is a *vertical* legal question
affecting electronic transferable records specifically. It is not a
gating condition on VERIQO's broader applicability to maritime
intelligence, commodity risk, insurance evidence, dispute evidence,
trade finance, and supply chain — none of which depend on it.

---

## 7. Positioning readiness

Use the four-tier framework
(`docs/VERIQO_READINESS_TIER_FRAMEWORK.md`), never a single word.
"Pilot-ready" alone is exactly the imprecision that framework was
written to correct. State the tier, and state what is still open at the
tier above.

Never present `READY_FOR_REAL_QUALIFICATION` as qualification. The
former means the internal harness passes; the latter requires an
external engagement that has not happened. Conflating them in a
proposal would be a material misrepresentation.

---

## 8. In dossiers

Every dossier carries a Limitations section stating what it does not
establish. It is not boilerplate and must not be trimmed for
presentation. A dossier that omits its limitations misrepresents itself
regardless of how accurate its other content is.

---

## 9. Applying this

When drafting any customer-facing material, check each claim:

1. Is it about **integrity/lineage** (VERIQO's domain) or about
   **truth, legality, or outcome** (not VERIQO's domain)?
2. Does it depend on an operational precondition — key distribution,
   TLS configuration, external qualification — that is not stated?
3. Would it still be defensible if read aloud by opposing counsel?

If any answer is uncomfortable, the claim needs rewording, not a
footnote.
