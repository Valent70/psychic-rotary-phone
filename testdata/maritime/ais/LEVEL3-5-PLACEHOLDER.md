# LEVEL 3-5 AIS data -- placeholder

No real, customer-owned, licensed, or independently-attested AIS feed
exists in this session. When a real AIS provider contract exists
(tracked in `testdata/provenance/contracts/`), place the real fixture
here with `data_quality_level` set to `LEVEL3_CUSTOMER_OWNED`,
`LEVEL4_LICENSED_LIVE`, or `LEVEL5_INDEPENDENTLY_ATTESTED` as
appropriate, and `rights_status` advanced through
`pkg/datarights.RightsStatus`'s real transition chain (never jumped
straight to `QUALIFIED` from `CONTRACT_PENDING` -- see
`pkg/datarights`'s own doc comment).
