# IEAP-001 — Independent Evidence Acquisition Protocol

**Status:** FROZEN — MIP-001 W0 / W2
**Constitutional basis:** Articles 2, 4, 5, 28

---

## 1. Purpose

IEAP governs how evidence enters VERIQO. Its central claim is narrow
and defensible: **VERIQO can show what it acquired, from where, under
what authority, and that the raw artifact was preserved before anything
touched it.** It makes no claim that acquired content is true —
Article 2.

---

## 2. Acquisition flow

```
Reverse Proof → Evidence Requirement → Source Eligibility
  → Rights Check → Independence Check → Neutral Selection
  → Connector → Raw Response → Hash → WORM
  → Manifest → Receipt → Evidence Version
```

The ordering is constitutional, not stylistic:

- **Rights check precedes connector contact** (Article 4). A rights
  failure means the network call never happens. A system that calls
  first and audits after has already violated the article regardless of
  what the audit says.
- **Hash and WORM precede manifest** (Article 5). A parsed
  representation that outlives its raw original is invalid evidence.
- **Independence check precedes neutral selection** so that source
  choice cannot be steered toward a same-root cluster.

---

## 3. Fail-closed rules

Each is an unconditional refusal, not a warning:

| Condition | Outcome |
|---|---|
| No rights | `NO_CONTACT` — connector is never invoked |
| Invalid certificate | `QUARANTINE` |
| Raw cannot be preserved | `ACQUISITION_FAIL` — the acquisition is void, not degraded |
| Hash mismatch | `INVALID` |
| Party-supplied credential | `PARTY_MEDIATED` — acquired, but marked non-independent |
| Unapproved connector | `DENY` |
| AI instructing a connector | `DENY` (Article 8) |

`PARTY_MEDIATED` deserves emphasis: evidence obtained using a party's
own credentials is still evidence, but it is **not independently
acquired**, and it can never satisfy an independence requirement. The
protocol records the distinction rather than discarding the artifact.

---

## 4. Connector authority boundary

A connector **may**: contact an approved endpoint, use its designated
credential, receive a response, write raw intake, emit an acquisition
event.

A connector **may not**: access arbitrary vault objects, access another
tenant's data, access unrelated credentials, modify policy, sign
findings, modify evidence, invoke AI, browse arbitrary internet, or
change qualification.

This is a capability boundary, not a code review guideline. A connector
that could do any of the second list would make Articles 4 and 8
unenforceable.

---

## 5. Three lineage dimensions

Provenance is not one chain. IEAP maintains three, and conflating them
is how false independence claims arise:

1. **Source lineage** — provider → source artifact
2. **Acquisition lineage** — VERIQO acquisition → raw artifact
3. **Transformation lineage** — raw → normalization → observation → finding

```
Provider → Source Artifact → VERIQO Acquisition → Raw Artifact
  → Normalization → Observation → Finding
```

Every transition is hash-bound. Two artifacts may share transformation
lineage while differing in source lineage, or vice versa — and only
source-lineage divergence bears on independence.

---

## 6. Acquisition receipt and manifest

The **receipt** attests to one acquisition event: what was requested,
from which source, under which rights profile, at which logical time,
producing which raw hash.

The **manifest** is the canonical, JCS-canonicalized record over which
the manifest hash is computed and, where signing is enabled, signed.

Verification points, per MIP W3.2 — integrity is re-checked at each,
not assumed to persist:

```
initial acquisition · vault transfer · before transformation
after transformation · before review · before analysis
before export · replay · restore · failover · periodic audit
```

---

## 7. WORM vault requirements

```
Object versioning · Retention lock · Legal hold · Encryption
CMEK/KMS · Signed manifests · Access control · Custody history
Cross-region backup · External checkpoint
```

Four distinctions the vault must keep separate:

```
Immutable   ≠ Accessible
Restricted  ≠ Deleted
Legal Hold  ≠ Privilege
WORM        ≠ Truth
```

The last is the one that matters commercially. A WORM vault proves an
artifact has not changed since it was stored. It says nothing whatever
about whether the artifact was true when stored.

---

## 8. Current implementation status

Per MIP §34 taxonomy:

| Component | Status |
|---|---|
| Evidence manifest + custody FSM | `IMPLEMENTED`, `ADVERSARIAL_TESTED` (`pkg/evidence/manifest`, FROZEN) |
| Hash / canonicalization | `IMPLEMENTED`, `REPLAY_VERIFIED` (`pkg/canonical/jcs`) |
| Retention / legal hold | `IMPLEMENTED`, `INTEGRATION_TESTED` (`pkg/governance/data`) |
| Acquisition contracts | `IMPLEMENTED` with **simulated** sources (`pkg/connector/*`) |
| Rights-before-contact gate | `DESIGNED` — the rule is constitutional and checked in `pkg/constitution`; no live connector currently performs a real acquisition to gate |
| WORM vault | `DESIGNED` — retention lock and legal hold exist; object-store WORM binding does not |
| Independent attestation | `DESIGNED` — external dependency |

**No live provider integration exists in any domain.** Every connector
emits `ModeSynthetic` and has no code path capable of producing
`ModeLive` — enforced by type, not convention. See
`docs/VERIQO_REAL_WORLD_NETWORK_MODEL.md`.
