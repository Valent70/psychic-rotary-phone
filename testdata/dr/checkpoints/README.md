# testdata/dr/checkpoints

Example shape a `pkg/blockers/dr` region checkpoint is expected to take
(region ID, monotonic sequence/tick, a deterministic hash of that
region's replicated state, and a PrevHash link to the previous checkpoint
for the same region — the same hash-chain shape used throughout this
repo, e.g. `pkg/moat/fusion`'s ledger entries). No file is placed here
yet: the real DR checkpoint type and its exact field names are defined in
`pkg/blockers/dr` itself (see that package's own tests for real,
generated checkpoint-chain examples) — duplicating placeholder JSON here
ahead of that real implementation would risk silently drifting out of
sync with it. This README exists so the directory's purpose is documented
even before real example files are added.
