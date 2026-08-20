# Maritime Data Quality Levels

Per external audit item 12: "Khusus data maritime, gunakan beberapa
level ... Core engine harus mengetahui level tersebut." This file
defines the six levels; each fixture under `testdata/maritime/*/` states
its own level in its own JSON (`"data_quality_level"` field) so a
consumer never has to infer it from which subdirectory a file happens to
sit in.

| Level | Name | Meaning | Present in this pack? |
|---|---|---|---|
| LEVEL 0 | `SYNTHETIC` | Fabricated for structural/schema testing only; no claim of correspondence to any real vessel, port, or event. | Yes — every `*.level0.json` fixture. |
| LEVEL 1 | `REPLAY` | A previously-real (or previously-synthetic) record replayed verbatim for determinism/regression testing. | Yes — every `*.level1-replay.json` fixture, replaying a LEVEL 0 fixture's own content unchanged. |
| LEVEL 2 | `REAL_DERIVED` | Deterministically generated FROM a real algorithm/seed (e.g. `pkg/blockers/scale.GenerateWorkload`), not an independent observation, but not arbitrary either. | Yes — see `testdata/scale/derived/`. |
| LEVEL 3 | `CUSTOMER_OWNED` | Real data supplied by a real customer under their own data-rights terms. | **No** — placeholder only, see below. |
| LEVEL 4 | `LICENSED_LIVE` | Real live data under a commercial license from a real maritime data provider (AIS feed, port authority system, etc). | **No** — placeholder only, see below. |
| LEVEL 5 | `INDEPENDENTLY_ATTESTED` | Real data whose provenance has been independently verified/attested by a third party (e.g. a notarized port authority record). | **No** — placeholder only, see below. |

## Why LEVEL 3–5 are placeholders

This session has no maritime data contract, no licensed AIS/port-system
feed, and no independent attester. Fabricating LEVEL 3/4/5-labeled data
here would misrepresent this pack's actual provenance to exactly the
degree external audit item 1's fail-closed rule exists to prevent
("VERIQO harus fail closed jika SYNTHETIC atau REPLAY diklaim sebagai
LIVE"). Each of `ais/`, `portcall/`, `nor/`, `sof/`, `claims/` contains a
`LEVEL3-5-PLACEHOLDER.md` stating this explicitly, so an operator who
later obtains a real feed knows exactly where the real fixture belongs
and what shape (`data_quality_level`, `rights_status`, `data_origin`)
it must declare.

## Relationship to `pkg/blockers/livedata.DataMode` and `pkg/datarights.RightsStatus`

`data_quality_level` here is a maritime-domain-specific axis, distinct
from but related to `pkg/blockers/livedata.DataMode` (SYNTHETIC / REPLAY
/ REAL_DERIVED_BENCHMARK / LIVE_LICENSED / LIVE_CUSTOMER_OWNED) and
`pkg/datarights.RightsStatus` — the mapping is roughly LEVEL0→SYNTHETIC,
LEVEL1→REPLAY, LEVEL2→REAL_DERIVED_BENCHMARK, LEVEL3→LIVE_CUSTOMER_OWNED,
LEVEL4→LIVE_LICENSED, LEVEL5→LIVE_LICENSED plus an independent
attestation record. Each fixture's JSON carries both its
`data_quality_level` and (where applicable) a `data_origin` field using
the real `DataMode` vocabulary, so a caller can validate one against the
other rather than trusting either alone.
