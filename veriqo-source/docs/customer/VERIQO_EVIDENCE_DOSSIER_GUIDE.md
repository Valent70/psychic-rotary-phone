# VERIQO Evidence Dossier Guide

How to read a VERIQO Evidence Dossier — written for the person receiving
one, who may be an adjuster, a broker, counsel, an arbitrator, or a
counterparty with no reason to trust whoever sent it.

---

## 1. What you have been given

A dossier is the complete, self-contained record of one case: the
evidence it rests on, the decision reached, the authorization to act,
and the cryptographic material needed to check all of it.

It comes in two forms of the same content:

- **Human form** — readable JSON or rendered Markdown/PDF. What you
  read.
- **Machine form** — a `.zip` "Machine Package". What you *verify*.

If you were sent only the human form and the matter is contested, ask
for the machine form. The readable document is a rendering; the package
is the thing that can be checked.

---

## 2. Check it before you read it

Before drawing any conclusion from a dossier's contents, verify it:

```
veriqo-commercial-verify -package case.zip
```

Exit code `0` means every check passed. Exit `1` means at least one
check failed — **stop and raise it**; a dossier that fails verification
should not be relied upon.

Read the `SKIP` lines, not just the verdict. A `SKIP` means a check
could not be performed with what you were given — most commonly
signature verification, which needs a trusted key registry you obtained
independently. A dossier that verifies with signatures skipped is
*intact*, not *authenticated*. See the Unified Verifier Specification.

The verifier is a separate binary that reads only your file. It does not
contact the sender's servers, which is precisely why its answer is worth
something.

---

## 3. What each section tells you

### Case identity

Two identifiers, and the difference matters:

- **`scope`** — the case ID as the submitting party named it. Their
  reference, meaningful in their system.
- **`case_id`** — the Decision's own finding hash. A cryptographic
  identity derived from the case's content. Two dossiers with the same
  `case_id` describe the same decision; a dossier whose contents were
  altered cannot keep the same one.

Cite the `case_id` when corresponding about a specific dossier.

### Evidence inventory

Every item the decision rests on, each with its identity, provenance,
integrity, custody chain, source, timing, trust classification, and
domain metadata. See the Evidence Model document for what each facet
means.

What to look at first:

- **`state`** — should be `FINALIZED`. Only finalized evidence can
  ground a decision.
- **`verified`** — re-derived at generation time, not a stored flag.
- **`custody`** — the ordered chain. Look at who handled the item and
  when. A short chain is not suspicious; an *inconsistent* one is.
- **`sha256`** — if you hold the underlying document, hash it yourself
  and compare. This is the step that connects the paper in your hand to
  the record in the dossier, and nobody else can do it for you.
- **`signature`** — **absent means unsigned.** It is not concealed; it
  is simply not there. Do not read an unsigned record as an
  authenticated one.

### Corroboration and contradictions

Derived from the actual grounded finding, not written prose:
`corroboration` lists evidence supporting the hypothesis;
`contradictions` lists evidence against it.

**A populated `contradictions` list is a sign of a well-kept record,
not a weak case.** The system records contradicting evidence because
real matters contain it. A dossier from a contested case with an empty
contradictions list deserves a question about what was submitted, not
automatic confidence.

### Decision

The outcome, the rationale, the logical time it was decided, and the
hashes binding it to its finding and authorization. The decision is
once-only: a case cannot be re-decided, so there is no possibility of a
quietly revised conclusion. A changed view requires a new case, which
leaves both records standing.

### Action authorization and receipt

Present when the case was acted upon. Records who was authorized to do
what, under which policy reference, within which validity window, and
who actually executed it. Authorization and execution are separate
gates: an execution outside what the authorization covers is refused,
not logged as an exception.

### Limitations

**Always present, and the section to read most carefully.** It states
what this dossier does *not* establish. It is not boilerplate to skip —
it is where the document tells you the boundary of its own claims,
including that it takes no legal position.

### Package hash and signature

`package_hash` is computed over every field except itself and the
signature. `package_signature`, when present, is a real Ed25519
signature over that hash. Absent means unsigned.

---

## 4. What a verified dossier does and does not establish

**It does establish**, when verification passes:

- The record is internally consistent and unaltered since generation.
- Each evidence item's manifest is unaltered and its custody chain is
  unbroken.
- The decision and authorization hashes the dossier claims genuinely
  appear in the ledger the verifier parsed independently.
- The ledger's own hash chain is intact from genesis — no record was
  inserted, removed, reordered, or edited.
- With a trusted key registry: which key attested to the record, and
  that the key was not revoked.

**It does not establish**:

- That the underlying documents are **truthful**. A surveyor's report
  can be intact, in custody, signed — and wrong. Integrity is not
  veracity.
- That asserted identities (vessel, party, policy) were checked against
  an authoritative external registry. They were recorded as asserted.
- That the decision was **correct**, only that it followed from the
  recorded inputs and cited real, finalized evidence.
- Anything about **legal admissibility or evidential weight**. See the
  Legal Positioning Statement and the Jurisdiction Disclaimer.

---

## 5. Practical checks for a recipient

1. Verify the package. Record the exit code and the full check output —
   including `SKIP` lines — with your file.
2. Hash any underlying document you hold; compare to `sha256`.
3. Read Limitations before Conclusions.
4. Check the evidence timing against your own account of events. Ticks
   are logical, not wall-clock, so ask the sender for the tick-to-date
   mapping if sequence matters to your position.
5. If signatures were skipped and authenticity matters, ask the sender
   for their trusted key registry through a channel independent of the
   dossier itself, then re-verify.
6. Keep the `.zip`. It stays verifiable indefinitely with no VERIQO
   service reachable and no licence in force.

---

## 6. If verification fails

A `FAIL` is a substantive finding. Do not discard the package and do not
quietly re-request a "fixed" one — the failing artifact is itself
evidence about the record's handling.

Record which check failed, keep the original file unmodified, and raise
it with the sender in writing. `ledger_hash_chain` and
`lineage_decision` failures are materially more serious than a single
evidence-item failure: they indicate the record as a whole does not hang
together, rather than one item being damaged.
