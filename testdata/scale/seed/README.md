# testdata/scale/seed

The scale-qualification harness (`pkg/blockers/scale.GenerateWorkload`)
takes a `(seed uint64, count int)` pair and deterministically produces
byte-identical envelopes every time — see
`pkg/blockers/scale/large_scale_test.go`'s
`TestGenerateWorkloadIsDeterministic`. There is no separate seed FILE
format to store: the seed is just the uint64 itself. This pack documents
the real seed values already used by this repo's own qualification runs
so a later run can be directly compared against them:

| Seed | Used by | Purpose |
|---|---|---|
| `42` | `testdata/scale/derived/sample-seed42-n5.json` (this pack) | small illustrative sample, 5 envelopes |
| `42` (n=5000) | `pkg/blockers/scale/large_scale_test.go` `TestRunLargeScaleQualificationCleanRun` | the real 10-node/5000-envelope qualification run this repo has actually executed |
| `43` | `large_scale_test.go` `TestGenerateWorkloadIsDeterministic` | proves a different seed produces different envelope hashes |
