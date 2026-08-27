# Real-World Insurance Network Report

Documents the `REAL_WORLD_NETWORK` category and the Party Authority
Model extensions built this round, closing the work order's §27 "Extend
Real-World Insurance Network model" item.

## Participant roles: 45 known, verified structurally

`pkg/insurance/party.KnownRoles()` returns 45 roles, asserted by count
in `TestKnownRolesCoversTheBlueprintList` (never a magic number that can
silently drift): 21 from the original VICE blueprint §3, 9 added for
recovery/dispute/regulatory coverage, 10 for the VERIQO Master Closure
Mandate's full chain ("Insured → Broker → Coverholder/MGA →
Underwriter → Insurer → Co-insurer → Reinsurer → P&I Club → Claims
Handler → Loss Adjuster → Surveyor → Average Adjuster → Expert →
Salvage Party → Recovery Party → Lawyer → Repairer → Bank/Trade
Finance → Other responsible counterparties"), and **5 new this round**
to reach the work order's own 20+ participant-type target:

| Role | Distinct from | Why it needed its own entry |
|---|---|---|
| `MARINE_SURVEY_COMPANY` | `SURVEYOR` | The organization a surveyor acts FOR — the Party Authority Model's `Organization` field (below) names this explicitly rather than conflating the individual and the firm. |
| `INSPECTION_COMPANY` | `MARINE_SURVEY_COMPANY` | A cargo/quality inspection firm is a different real-world entity with a different scope of engagement than a marine surveyor. |
| `PORT_OPERATOR` | `PORT_AUTHORITY` | An authority is the regulatory/statutory body; an operator is the commercial entity running terminal operations day to day. A claim can genuinely name either or both. |
| `COMMODITY_TRADER` | `CARGO_OWNER`, `SHIPPER`, `CONSIGNEE` | The counterparty that bought or sold the cargo itself, distinct from a title-holding/financing entity or a pure logistics role. |
| `INDEPENDENT_REVIEWER` | `AUDITOR`, `LOSS_ADJUSTER` | The functional spec §77 independent-review role the Dossier Verifier deliverable names by name — reviews process compliance from outside the claim, distinct from a process auditor or a claim-internal adjuster. |

## The Party Authority Model

`pkg/insurance/party/relationship.go`'s `Relationship` type already
carried the mandate's core fields (identity = `FromParty`/`ToParty`,
role = `Role`, authority = `Authority`, `EffectiveFrom`/`EffectiveTo`,
`Permissions`, `ConsentGiven`, `ProvenanceEvidenceIDs`, revocation,
`Status`). This round added the five still-missing fields the mandate
names by name, **additively** — every new field defaults to its zero
value and no existing caller's behavior changes:

- **`Organization`** — the organizational entity `FromParty` acts FOR
  (e.g. a named surveyor's employer), distinct from the party's own
  individual identity.
- **`Scope`** — free text bounding WHAT a relationship's authority
  covers (e.g. "cargo damage assessment for CLM-002 only; hull is out
  of scope") — the subject-matter boundary, distinct from
  `Permissions` (the system-action boundary).
- **`DelegatedFrom` / `CanDelegate`** — a real delegation chain. A
  relationship may only cite an ALREADY-REGISTERED relationship as its
  `DelegatedFrom`, and only if that source explicitly set
  `CanDelegate = true` (`RelationshipRegistry.Register` enforces both,
  returning `ErrDelegationSourceNotFound` / `ErrDelegationNotPermitted`
  otherwise). Because a delegation can only ever point to something
  registered strictly earlier, a delegation cycle cannot be
  constructed through normal use — no cycle-detection workaround is
  needed to make the invariant hold, though `DelegationChain` still
  walks defensively rather than trusting that blindly.
- **`Tenant`** — the operational unit (insurer, coverholder, MGA
  office) a relationship's authority was granted within, for a
  multi-tenant deployment.
- **`Jurisdiction`** — the governing law/forum a relationship's
  authority is granted under, distinct from `ContractualBasis` (the
  instrument) and from `dispute.Forum` (the forum for a *dispute*, not
  for the underlying relationship).

All five are tested in `TestPartyAuthorityModelFieldsRoundTrip` and
`TestDelegationRequiresAnAlreadyRegisteredPermittingSource`
(`pkg/insurance/party/relationship_test.go`).

## The golden cross-domain case's own network proof

`insurance_golden_cross_domain` (`REAL_WORLD_NETWORK`,
`VERIFIED_INTERNAL`) is the one case that proves geospatial
geofencing, the party relationship layer, salvage, and co-/reinsurance
allocation are genuinely **connected**, not merely present side by
side:

- A broker relationship is registered with real consent evidence and a
  real granted permission set (`ACCESS_CASE_ROOM`, `SUBMIT_CLAIM`).
- Salvage genuinely reduces the quantum figure by its own exact net
  value (`TestGoldenSalvageGenuinelyReducesQuantum`) — not a
  co-located but disconnected number.
- Co-insurance and reinsurance allocations sum to EXACTLY their input
  payment (`allocateByBasisPoints`'s largest-remainder exact-integer
  distribution — no rounding leakage).
- The whole extension survives a cold replay of the underlying case.

This round's `caseroom.RunAssurance` extends the same golden case one
step further: two relationships with different granted permissions see
genuinely different `caseroom.Section` sets from the identical dossier
— proving the Party Authority Model's `Permissions` field is not just
stored, but actually gates real content.

## `live_data`: the Live Data Admission Framework

`live_data` (`REAL_WORLD_NETWORK`, `BLOCKED_EXTERNAL`) is the other
half of this category. `pkg/blockers/livedata` implements the "SYNTHETIC
/ SIMULATED_LIVE / LIVE_LICENSED" labeling the work order requests,
under this codebase's own vocabulary:

| Work order term | This codebase's `DataMode` |
|---|---|
| `SYNTHETIC` | `ModeSynthetic` |
| `SIMULATED_LIVE` | `ModeReplay` — re-emitted historical records, `DataMode` forced to `REPLAY` regardless of the source's own claim |
| `LIVE_LICENSED` | `ModeLive`, and ONLY accepted when the ingesting connector's own `connectorMode == "REAL"` — `Pipeline.Ingest` refuses any `ModeLive` record from a `SIMULATED`-mode connector outright, so a simulator cannot manufacture a `LIVE` label |

A real content-hash dedup and a real anti-replay defense (every
accepted record replayed back and confirmed rejected) run across all
four source types (SWIFT/BoL/AIS/SAR). What remains `BLOCKED_EXTERNAL`
is exactly what the label names: a real commercial data contract with a
real feed provider — the pipeline that would ingest it already exists
and is tested against synthetic and replayed data.

## Evidence corroboration status

`pkg/insurance/evidence.Record.Corroboration()` (`corroboration.go`,
added this round) answers the work order's §28 request for
`UNKNOWN`/`CORROBORATED`/`CONTRADICTED`/`SUPERSEDED`/`REVOKED`
classification, as a pure derived view over signals this package
already maintained — `Status` (already carrying `StatusCorroborated`/
`StatusContradicted`, set by the real `pkg/insurance/contradiction`
arbitration engine adapter), `CorrectionSuperseded`, and `Rights ==
provenance.RightsRevoked` — rather than a sixth, independently-settable
field that could drift from the other five. Precedence: `REVOKED`
outranks `SUPERSEDED` outranks `CONTRADICTED`/`CORROBORATED` outranks
the honest default, `UNKNOWN`.
