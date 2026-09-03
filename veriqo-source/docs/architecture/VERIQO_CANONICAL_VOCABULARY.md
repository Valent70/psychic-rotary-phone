# VERIQO Canonical Vocabulary — FROZEN

**Status:** FROZEN. Amendments require an explicit vocabulary amendment, not a new document.
**Executable form:** `pkg/assurance/fabric.go` · asserted by `pkg/assurance/fabric_test.go`

---

## 1. Why this document exists

By this round VERIQO's own documents used all of these terms, each meaning
roughly one of five things:

> Unified Evidence · Evidence Fabric · Evidence Engine · Trust Engine ·
> Trust Kernel · Knowledge Fabric · Intelligent Fabric · Intelligence Layer ·
> Qualification · EQF · Case lineage · Workflow · Case Engine · Orchestrator ·
> Execution

Left alive, that list guarantees an engineer, an investor, a reviewer and the
next agent each read a **different architecture** out of the same repository.
That is not a documentation problem. It is how a system acquires four
implementations of one idea.

The vocabulary is therefore frozen to **five fabrics**, each with exactly one
canonical name, and every term above is retired to one of them.

---

## 2. The five fabrics

| Fabric | Full name | What it is for |
|---|---|---|
| **TECP** | Trust & Evidence Control Plane | Hold the canonical state of evidence, who may touch it, and what was done to it |
| **EQF** | Epistemic Qualification Fabric | Decide what the evidence supports, what it does not, and what is missing |
| **IF** | Intelligent Fabric | Propose explanations. Intelligence proposes; it never concludes |
| **CRF** | Case Resolution Fabric | Hold one case across every domain, from identity to outcome |
| **FREF** | Forward–Reverse Execution Fabric | Run both directions over the same evidence, and prove they close |

### Composition

```
① TECP    Identity · Trust · Evidence · Provenance · Authority · Policy · Audit · Ledger
② EQF     Qualification · Independence · Contradiction · Reverse Proof ·
          Proof Obligations · Next Best Evidence · Uncertainty
③ IF      Knowledge · Reasoning · Causal · Bayesian · Counterfactual ·
          Temporal · Economic · Digital Twin
④ CRF     Case · Mission · Intent · Hypothesis · Claim · Evidence ·
          Timeline · Finding · Resolution · Outcome
⑤ FREF    FORWARD  Observation → Evidence → Knowledge → Reasoning → Trust → Finding → Decision
          REVERSE  Decision/Claim → Proof Obligations → Required Evidence →
                   Evidence Gap → Contradiction → Qualification → Next Best Evidence
```

---

## 3. The five are **not** five packages

This is the load-bearing rule and it is enforced by test.

Creating `pkg/tecp`, `pkg/eqf`, `pkg/if`, `pkg/crf` and a `pkg/fref` engine
would have violated the anti-duplication rule the architecture sets for itself:
five new façades over capabilities that already exist, and five more places for
a second implementation to hide.

A fabric is therefore a **claim about existing code**, audited along eleven
dimensions. `TestNoFabricHasItsOwnPackage` fails the build if `pkg/tecp`,
`pkg/eqf`, `pkg/crf`, `pkg/intelligentfabric`, `pkg/trustengine` or
`pkg/caseengine` ever appears.

`pkg/fref` is the single exception, and deliberately so: it is a **contract**
that refuses out-of-order executions, not an engine that runs them. It owns no
stage — every one of its fourteen stages names another package as the authority,
and `VerifyAgainstContract` reports a stage that ran anywhere else as drift.

---

## 4. Retired vocabulary

Each retired name has exactly one canonical replacement.
`TestEveryRetiredSynonymHasExactlyOneReplacement` fails if any name maps to two
fabrics — that would not be a retirement, only a new ambiguity.

| Retired name | Canonical fabric |
|---|---|
| Unified Evidence | TECP |
| Evidence Fabric | TECP |
| Evidence Engine | TECP |
| Trust Engine | TECP |
| Trust Kernel | TECP |
| Qualification (as an architecture layer) | EQF |
| EQF (as a package name) | EQF (the fabric) |
| epistemic layer | EQF |
| Knowledge Fabric | IF |
| Intelligent Fabric | IF |
| Intelligence Layer | IF |
| moat | IF |
| Case Engine | CRF |
| Case Resolution Engine | CRF |
| Case lineage | CRF |
| case workflow | CRF |
| Orchestrator | FREF |
| Execution | FREF |
| universal workflow | FREF |
| pipeline | FREF |

**"Trust Kernel"** deserves a note. It named something real — the frozen core in
`docs/VERIQO_CORE_TRUST_KERNEL_FREEZE.md` — and that freeze declaration stands.
What is retired is the *term* as an architecture-level name, because it competed
with "Trust Engine" and "Evidence Fabric" for the same territory. The frozen
core is part of TECP.

---

## 5. How to use this

- **Naming a new package:** it belongs inside one of the five fabrics. If it
  appears to need a sixth, that is a vocabulary amendment, argued explicitly.
- **Writing a document:** use the canonical name. If you reach for a retired one,
  the table above says what you meant.
- **Reviewing a claim:** ask which fabric, then read that fabric's row in
  `docs/architecture/FIVE_FABRIC_CAPABILITY_AUDIT.md`, which names the entry
  point, the call graph and the fail-closed behaviour you can check.
