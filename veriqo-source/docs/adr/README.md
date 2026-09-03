# Architecture Decision Records

An ADR records one decision: the context that forced it, what was decided, and
what it costs. They are append-only. A decision that turns out wrong gets a new
ADR that supersedes the old one; the old one stays, because the reasoning that
led to it is part of how the system got here.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-five-fabrics-not-five-packages.md) | The five fabrics are architectural capabilities, not five new packages | Accepted |
| [0002](0002-proof-object-as-the-unit-of-conclusion.md) | Every significant conclusion carries a sealed Proof Object | Accepted |
| [0003](0003-tamper-evident-chain-is-not-a-trusted-timestamp.md) | A self-hosted temporal chain and an RFC 3161 attestation are different types | Accepted |
| [0004](0004-engineering-and-assurance-are-separate-axes.md) | Completion is reported on two axes that never combine | Accepted |
| [0005](0005-dispute-evidence-support-not-adjudication.md) | VERIQO furnishes evidence to a decision-maker and never decides | Accepted |
