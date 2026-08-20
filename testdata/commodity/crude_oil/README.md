# commodity/crude_oil -- placeholder

No real commercial crude_oil cargo/quality/quantity data exists in this
session (no data contract, no licensed feed). This directory is a
placeholder so a future real dataset has a defined home with the same
provenance discipline as `testdata/maritime/`: any file placed here
must declare `data_quality_level`, `data_origin`
(`pkg/blockers/livedata.DataMode`), and `rights_status`
(`pkg/datarights.RightsStatus`) -- never silently assumed LIVE.

RWC-002 (Gunung Kemala EN590, `pkg/rwc/rwc002.go`) is this repo's real,
already-implemented commodity validation case; its own fixture values
live in that Go file, not duplicated here, to avoid two sources of
truth for the same case.
