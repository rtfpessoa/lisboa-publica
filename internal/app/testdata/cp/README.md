# CP public prediction fixture

Captured 26 September 2026 from the public TML Hub `/api/v1/realtime/eta/gtfs` endpoint. The JSON keeps five CP entities from the mixed response, its original FULL_DATASET header and source timestamps; other agencies and unrelated wrapper fields were removed. It contains no access credentials or private CP API calls.

`schedule.zip` contains corresponding original GTFS rows for three trips and their full national stop calls from active CP plan76XA2 (valid20260616–20261231). It is a small parser fixture, not a replacement network or a guarantee of current coverage. At the fixed source clock1790455682, normalization admits15 local upcoming calls, including2 at Lisboa Oriente. Trip ID suffixes remain unchanged and are not interpreted as operating dates.

Source details and primary-source links: [research](../../../../docs/research/CP-PUBLIC-PREDICTIONS.md).
