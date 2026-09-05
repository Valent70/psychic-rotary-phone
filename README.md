# VERIQO

**Evidence-Qualified Intelligence OS**

VERIQO does not tell you what is true. It tells you what a claim is worth,
what would change it, and what nobody has checked.

---

## Status

**SPECIFICATIONALLY IMPLEMENTED. NOT PRODUCTION QUALIFIED.**

That distinction is load-bearing and the repository enforces it in code rather
than stating it in prose:

- the qualification kernel is implemented and internally assured
- **nothing** in the assurance register is above `INTERNALLY_ASSURED`
- all twenty production gates are blocking; thirteen need a party that is not VERIQO
- release is refused, with every reason enumerated
- eleven evidence debts are open, each with an owner and a stated consequence

`INTERNALLY_ASSURED` is the highest rung reachable without an outside party.
Every rung above it is *defined* in terms of somebody else's work, so VERIQO
cannot reach one by trying harder. That is Law 11, and it is enforced by the
type system, not by policy.

```
go run ./cmd/veriqoctl assurance    # the master assurance graph
go run ./cmd/veriqoctl gates        # what blocks release, and who could unblock it
go run ./cmd/veriqoctl scorecard    # nine dimensions, no aggregate score
./scripts/verify.sh                 # what passes -- and what is deliberately NOT run
```

---

## What it is

Three systems occupy adjacent ground. Palantir answers *what is happening and
what should we do*. Quantexa answers *what is connected to what*. Perplexity
answers *what does the corpus say*.

VERIQO occupies a fourth: the **proof and qualification world**.

> A claim is not qualified by how strongly the system believes it. It is
> qualified by the evidence supporting it, the evidence contradicting it, the
> ability to reproduce the reasoning, the ability to disprove it, and — where
> required — the independent evidence that the control itself works.

**VERIQO is neutral.** It does not conclude fraud, liability or coverage. Five
acts are named in code as permanently forbidden to automation, and an artefact
recording any of them fails validation rather than merely failing to be promoted.

## Two loops

```
        INTELLIGENCE LOOP                    ASSURANCE LOOP
        observe                              specify
          -> acquire                           -> implement
          -> resolve                           -> test
          -> analyse                           -> attack
          -> hypothesise                       -> counterexample
          -> contradict                        -> fix
          -> qualify                           -> independently validate
          -> decide                            -> operate -> measure -> requalify
              \                                     /
               \___________  QUALIFIED  ___________/
                             DECISION
                                |
                          DECISION PASSPORT
```

The qualification kernel is the moat. Domain intelligence is the market surface.
Neither is the product on its own.

## Layout

```
pkg/canonical/jcs        RFC 8785 -- everything hashes through here
pkg/contract             four-valued Outcome, versions, injected clocks
pkg/ledger  pkg/audit    durable hash chain, signed checkpoints
pkg/tenant  pkg/policy   cryptographic isolation, deny-overrides ABAC
pkg/evidence/...         versions, quality, redaction worker and corpus
pkg/provenance  custody  origin that processing cannot launder
pkg/entity  resolution   five outcomes, never a silent merge
pkg/claim  reverseproof  disproof paths, and no CONFIRMED verdict
pkg/hypothesis quantum   ACH scoring; arithmetic that refuses bad bases
pkg/uncertainty trust    nine dimensions and no aggregate score
pkg/findings passport    one mint authority; limitations inside the signature
pkg/ai  pkg/agents       the qualification ladder and the tool firewall
--------------------------------------------------------------------------
pkg/assurance/state      Law 11 and the eleven-rung assurance ladder
pkg/assurance/register   claims, evidence debt, master assurance graph
pkg/assurance/epistemic  ESTIMATE != MEASURED != VALIDATED != PROVEN
pkg/assurance/capsule    the auditor capsule
pkg/verification         the Independent Verification Kit
pkg/gates  scorecard     twenty gates with a lifecycle; nine dimensions
--------------------------------------------------------------------------
pkg/intel/source         where material came from, and what that permits
pkg/intel/maritime       vessel behaviour, with innocent explanations attached
--------------------------------------------------------------------------
cmd/veriqoctl            what VERIQO believes about itself
cmd/veriqo-verify        what a third party can establish without asking it
test/adversarial         tests that assume an attacker
```

## For an assessor

You should not have to trust this repository. Build the capsule and check it:

```
go run ./cmd/veriqoctl capsule ./capsule
go run ./cmd/veriqo-verify ./capsule
```

`veriqo-verify` recomputes rather than reads. Artefact digests come from the
bytes. The ledger is rehashed from genesis. The passport digest is recomputed
from its payload *before* the signature is checked. And the qualification state
is **derived** and then compared with what the bundle claims — a bundle
asserting more than it carries is contradicted, not believed.

It also states, on every run, the three things it cannot establish: key
authenticity, existence in time, and anything about evidence that was left out.

## Licence and provenance of this work

Every figure in every report is produced by code in this repository. Where a
number is a judgement it is labelled `ESTIMATE` at every point it appears,
including inside the program that prints it.

Nothing here has been examined, attacked, validated or corroborated by any
party outside VERIQO. That is the single most important fact about this
repository, and it is stated in code, in the reports, and here.
