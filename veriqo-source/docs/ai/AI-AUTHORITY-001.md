# AI-AUTHORITY-001 — AI Authority Boundary

**Status:** FROZEN — MIP-001 W0 / W9
**Constitutional basis:** Articles 8, 20, 21, 27
**Executable form:** `pkg/ai/gateway`

---

## 1. The rule

> **AI has no evidence authority.**

AI may propose, summarize, hypothesize, correlate, and recommend. It
may not create, alter, qualify, or sign authoritative evidence. This
holds for Aureum, God of EYS, Claude, GPT, in-house ML, and any future
model — the boundary is a property of the architecture, not a policy
about a particular vendor.

---

## 2. What AI may and may not do

**May:**
```
Reasoning · Discovery · Hypothesis generation · Investigation planning
Evidence summarization · Pattern analysis · Next-evidence recommendation
Natural-language investigation
```

**May not:**
```
alter evidence · delete evidence · change trust · change policy
approve disclosure · confirm privilege · suppress contradiction
qualify authoritative evidence · sign final qualification
determine legal liability · instruct a connector
```

The last is easy to overlook and the most dangerous: an AI that can
direct an acquisition connector has acquired evidence authority
indirectly, by choosing what enters the fabric. IEAP-001 §3 denies it
explicitly.

---

## 3. The gateway is mandatory

No AI touches raw evidence. Every access passes `pkg/ai/gateway`:

```
AI Request → Purpose Check → Evidence Classification → Privilege Check
  → Protective Order → License / Rights → AI Processing Policy
  → Minimum Necessary Disclosure → Approved Evidence Projection → AI
```

```
RAW EVIDENCE ──X── AI cannot access directly

APPROVED EVIDENCE PROJECTION ──→ AI
```

The projection is the only surface AI sees. This matters commercially
as much as legally: it is what prevents a customer's licensed data or
another tenant's evidence from entering a model context.

---

## 4. Ten evaluation dimensions

Every access evaluates: purpose, classification, privilege, protective
order, rights, license, jurisdiction, AI processing policy, recipient,
minimum necessary disclosure.

**Redacted evidence does not automatically become AI-safe** (Article
21). Redaction serves a disclosure purpose; AI processing is a
*different* purpose with different rights. A derivative cleared for
opposing counsel's eyes is not thereby cleared for a training corpus.

---

## 5. AI contribution record (W9.4)

Every material AI contribution records:

```
Model · Model Version · Prompt Hash · Input Evidence IDs
Input Evidence Version IDs · Policy Version · Tool Calls
Output Hash · Human Reviewer · Purpose · Timestamp
```

This delivers Article 27 — **No Silent AI Influence**. If a model
shaped a finding, the fact pack shows which model, on which evidence
versions, under which policy, reviewed by whom.

Note `Input Evidence Version IDs`, not just IDs: a contribution made
against version 2 of an artifact is not the same contribution if
version 3 later exists.

---

## 6. Purpose separation (Article 20)

```
VIEW ≠ SEARCH ≠ COPY ≠ PRINT ≠ DOWNLOAD ≠ EXPORT
     ≠ REDISTRIBUTE ≠ AI_PROCESS ≠ RAG ≠ TRAIN
```

Ten separately-granted rights. `AI_PROCESS`, `RAG`, and `TRAIN` are
three distinct grants: permission to run a model over evidence once is
not permission to index it for retrieval, and neither is permission to
train on it. Collapsing them is the single most common way evidence
rights are silently exceeded.

---

## 7. Privileged material defaults

Unless explicitly authorized, privileged material gets:

```
No general search · No general RAG · No cross-case learning
No Common Fact Pack · No export
```

Default-deny, not default-allow-with-audit.

---

## 8. Where Aureum and God of EYS sit

Both are **Intelligence Plane** capabilities. Their outputs enter EQF
as hypotheses or observations *requiring qualification* — never as
findings.

```
Aureum / God of EYS → Hypothesis → EQF → Qualification
```

God of EYS may report *"these entities may be connected."* VERIQO then
asks: what evidence supports this? what contradicts it? what
alternative exists? what source is independent? what is still missing?
That interrogation is what keeps analytics epistemically disciplined
rather than merely fluent.

---

## 9. Implementation status

| Component | Status |
|---|---|
| AI evidence gateway (10 checks, fail-closed) | `IMPLEMENTED`, `UNIT_TESTED`, `ADVERSARIAL_TESTED` |
| AI contribution record | `IMPLEMENTED`, `UNIT_TESTED` |
| Purpose-separated rights (10 rights) | `IMPLEMENTED`, `UNIT_TESTED` |
| Privileged default-deny | `IMPLEMENTED`, `ADVERSARIAL_TESTED` |
| Aureum AI | `NOT BUILT` — requires a model deployment |
| God of EYS | `NOT BUILT` — requires an investigation corpus |
| Connector-instruction denial | `IMPLEMENTED` as a constitutional check; no live connector exists to attack |

The gateway is built and enforced **before** the intelligences that
must pass through it exist. That ordering is deliberate: a boundary
retrofitted after the systems it constrains have shipped is a boundary
that will have been bypassed.
