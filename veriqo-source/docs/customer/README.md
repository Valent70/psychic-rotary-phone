# VERIQO Customer Documentation Set

The customer- and reviewer-facing document set, closing the P1
"customer readiness" work order. A prior round named most of these as
deliberate gaps on the grounds that they were post-gate sales collateral;
they are now written, under the same honesty discipline as every other
document in this repository — every capability claim cites the code or
test behind it, and every gap is named rather than omitted.

Start with the readiness position:
**`../VERIQO_READINESS_TIER_FRAMEWORK.md`** — the four-tier framework
(DEMO-READY → DESIGN-PARTNER READY → PAID-PILOT READY → PRODUCTION
QUALIFIED) and VERIQO's honest position against each tier.

## By audience

**Integrating engineer**
| Document | Purpose |
|---|---|
| [`VERIQO_COMMERCIAL_API_V1_OPENAPI.yaml`](VERIQO_COMMERCIAL_API_V1_OPENAPI.yaml) | The interface contract: 15 paths, 16 schemas, validated. |
| [`VERIQO_CUSTOMER_INTEGRATION_GUIDE.md`](VERIQO_CUSTOMER_INTEGRATION_GUIDE.md) | The model behind the API — logical time, tenancy, grounding, error semantics, a worked sequence. |
| [`VERIQO_EVIDENCE_MODEL.md`](VERIQO_EVIDENCE_MODEL.md) | What an evidence item is: the eight facets, the lifecycle, retention. |
| [`VERIQO_REPLAY_SPECIFICATION.md`](VERIQO_REPLAY_SPECIFICATION.md) | What replay re-derives, why it is deterministic, and its precise scope. |

**Recipient of a dossier / verifying party**
| Document | Purpose |
|---|---|
| [`VERIQO_EVIDENCE_DOSSIER_GUIDE.md`](VERIQO_EVIDENCE_DOSSIER_GUIDE.md) | How to read a dossier, and what a verified one does and does not establish. |
| [`VERIQO_UNIFIED_VERIFIER_SPECIFICATION.md`](VERIQO_UNIFIED_VERIFIER_SPECIFICATION.md) | Every check, the three statuses, and why `SKIP` is neither pass nor fail. |

**Security / procurement reviewer**
| Document | Purpose |
|---|---|
| [`VERIQO_SECURITY_FAQ.md`](VERIQO_SECURITY_FAQ.md) | Direct answers, including the "no" answers: no OIDC, no HSM custody, no pen test, no encryption at rest. |
| [`VERIQO_DATA_HANDLING_POLICY.md`](VERIQO_DATA_HANDLING_POLICY.md) | What is held, where, how long — and why purge cannot erase that a record existed. |
| [`VERIQO_SLA_SLO_DRAFT.md`](VERIQO_SLA_SLO_DRAFT.md) | **Draft, not a commitment.** Draft targets plus what must exist before any SLA can be offered. |

**Operator**
| Document | Purpose |
|---|---|
| [`VERIQO_BACKUP_AND_RESTORE_PROCEDURE.md`](VERIQO_BACKUP_AND_RESTORE_PROCEDURE.md) | Mechanism, recovery-report reading, post-restore verification, and the open deployment items. |
| [`VERIQO_INCIDENT_RESPONSE_PROCEDURE.md`](VERIQO_INCIDENT_RESPONSE_PROCEDURE.md) | Severity model where integrity outranks availability; preserve-before-remediate. Written, not yet exercised. |
| [`VERIQO_SUPPORT_AND_DIAGNOSTICS.md`](VERIQO_SUPPORT_AND_DIAGNOSTICS.md) | Triage order, metric interpretation, what to collect before escalating. |

**Pilot sponsor / legal**
| Document | Purpose |
|---|---|
| [`VERIQO_PILOT_GUIDE.md`](VERIQO_PILOT_GUIDE.md) | What a pilot is for, what is and is not real, phases, and honest failure criteria. |
| [`VERIQO_LEGAL_POSITIONING_STATEMENT.md`](VERIQO_LEGAL_POSITIONING_STATEMENT.md) | The language VERIQO uses and refuses; the integrity/veracity distinction. |
| [`VERIQO_JURISDICTION_DISCLAIMER.md`](VERIQO_JURISDICTION_DISCLAIMER.md) | Not legal advice; why no jurisdiction table is published. |

## Three things every reader should know

1. **VERIQO does not store evidence documents** — only hashes,
   provenance, custody, and the decisions grounded on them. The bytes
   stay in your custody.
2. **`SKIP` in a verification report is not a pass.** It means a check
   could not be performed with the input supplied — most often signature
   verification, which needs a trusted key registry obtained
   out-of-band.
3. **Integrity is not veracity.** VERIQO can establish that a record is
   unaltered and genuinely linked to what it claims. It cannot establish
   that the document's contents are true, and it takes no legal
   position.
