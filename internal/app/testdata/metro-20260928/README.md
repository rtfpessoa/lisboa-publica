# Metro captured regression inputs

Captured 2026-09-28 during the read-only production investigation. These are retained source/cache publications, not synthetic or independent physical telemetry. Gzip decompression reproduces the original bytes. No clocks or associations were altered.

The direct capture contains eight reads of the cached direct API result, approximately 500 ms apart. Repeated publications do not prove 500 ms upstream refreshes. The static file is the then-active public Metro GTFS/cache topology. The 5B frame records the original failed vehicle association; it is historical evidence, not a required expected association. Source/receipt clocks remain in the files.

| File | Original bytes | SHA-256 of original bytes |
|---|---:|---|
| `metro-debug-direct-capture.json.gz` | 435344 | `c894daf657516b52a2be7775231276543a5b2398b3dc13fc53bb9c2637f85d42` |
| `metro-debug-static.json.gz` | 2501531 | `e09c7e9aaaa23ee79ef4f93115663e2428840eabc684d27b38643d5b3c9e03ca` |
| `metro-debug-5B.json.gz` | 32981 | `a4e6db7e54dd69ff7d55c9405373dbfc7cc944eb9a5abe43100957a8d4d4e251` |

Sources: the deployed dashboard public API/cache reads at `https://lisboapublica.rtfpessoa.xyz`, and the direct Metro publication source identified within each captured result. No credentials or authentication headers are included. The source clocks are preserved for replay at the captured time, rather than interpreted as current data.

## Red-line direction correction inputs

The two `red-direction-*` files retain the exact direct/static cache bytes from the separate 2026-09-28 Red-line investigation. They support the two-row/three-pattern regression and five-reference forecast-retention replay. These inputs are not independent physical directions. Seeded prior state and complete-cohort cases in the correction tests are explicitly synthetic/reconstructed, not retained observations.

| File | Original bytes | SHA-256 of original bytes |
|---|---:|---|
| `red-direction-direct.json.gz` | 73481 | `e6c30d6cc8fdb64a4af985e2d0f1222112c9bc6b5c783f5bde85e671d7f22e7d` |
| `red-direction-static.json.gz` | 6128515 | `f67b7f044882dd1fea27bdac5837da81f3a7e3e61d0cd92b592f55921eeb6316` |

## Original-clock Hub model replay, 2026-09-29

`metro-model-positions-20260929.json.gz` retains 12 successful public Hub position responses collected at one-second request starts, 13:47:10.977511–13:47:22.017617 UTC. Each response contains 30 Metro references (360 rows total). Original decoded bytes: 224951; SHA-256: `0ae46cd09b89bf0ee058894644155ae4d106f61b5d959faa3ae91649e8e024cd`. Original millisecond publication clocks, string direction, trip/pattern/shape context and local receipt clocks are preserved. No Metro authenticated API calls were made for this capture.

Changed-coordinate publication intervals in this short sample include 12.813 and 14.252 seconds; it does not establish a guaranteed cadence. Model replay admits 360 context rows and retains 30 references with six three-position directions. These are ETA-derived model positions, without independent physical timing/direction truth. Static evidence uses the retained artifact above; context compatibility is tested rather than assumed.
