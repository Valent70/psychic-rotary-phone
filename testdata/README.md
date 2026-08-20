# VERIQO Data Qualification Pack

External audit item 12 (`Hasil_audit_tambahan_yang_harus_dilakukan_sekarang_important.docx`,
section "12. Test data yang harus disiapkan sekarang"). This directory is
fixture/reference data only — nothing under `testdata/` is compiled or
executed by `go build`/`go vet` (the Go toolchain excludes any directory
named `testdata` from package builds by convention), and nothing here is
wired into a production decision path. It exists so a qualification run,
a new adapter implementation, or an auditor has real, versioned example
data to test against instead of inventing ad hoc fixtures per package.

## Layout

```
testdata/
    maritime/{ais,portcall,nor,sof,claims}/   -- maritime evidence-source fixtures, see LEVELS.md
    commodity/{crude_oil,coal,nickel}/        -- commodity-evidence fixtures (see that dir's own README — mostly placeholders, see below)
    security/{certificates,signatures}/       -- example key material / signed artifacts for qualification tests
    scale/{seed,derived}/                     -- scale-qualification workload seeds and one real derived sample
    dr/checkpoints/                           -- example DR checkpoint-chain shape
    provenance/{contracts,rights,provider}/   -- rights & provenance registry fixtures (pkg/datarights, pkg/provenance, pkg/evidenceorigin)
```

## Honesty discipline

This pack follows the same rule as every other artifact in this
repository: nothing here is fabricated to look more real than it is.

- **maritime/**: LEVEL 0 (synthetic) and LEVEL 1 (replay-shaped) fixtures
  are real, structured, and match the RWC-001 Ranchan Maritime case's own
  actual field values (`pkg/rwc/rwc001.go`), so they are usable as genuine
  test input, not disconnected sample data. LEVEL 2 (real-derived) is
  present only where it can honestly be derived from something real in
  this repo (e.g. the scale workload generator). **LEVEL 3 (customer-owned),
  LEVEL 4 (licensed live), and LEVEL 5 (independently attested) are
  documented as placeholders** — see `maritime/LEVELS.md` — because this
  environment has no real maritime data contract, no licensed feed, and no
  independent attester. Populating them with synthetic data and mislabeling
  it LEVEL 3+ would be exactly the fabrication this audit exists to catch.
- **commodity/**: placeholders only (see `commodity/README.md`) — no real
  crude oil/coal/nickel commercial data exists in this session.
- **scale/derived/sample-seed42-n5.json**: produced by actually running
  `pkg/blockers/scale.GenerateWorkload(42, 5)` in this session, not
  hand-typed — reproduce it yourself with the same call and the output
  will be byte-identical (that determinism is itself a tested property,
  see `pkg/blockers/scale/large_scale_test.go`).
- **security/**: a real, freshly generated Ed25519 test keypair and a real
  signature over a fixture manifest, produced with Go's standard
  `crypto/ed25519` — genuinely verifiable, but explicitly a **test** key,
  never used by any production signing path.
