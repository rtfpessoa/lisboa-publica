# Metro captured regression inputs

Captured 2026-09-28 during the read-only production investigation. These are retained source/cache publications, not synthetic or independent physical telemetry. Gzip decompression reproduces the original bytes. No clocks or associations were altered.

The direct capture contains eight reads of the cached direct API result, approximately 500 ms apart. Repeated publications do not prove 500 ms upstream refreshes. The static file is the then-active public Metro GTFS/cache topology. The 5B frame records the original failed vehicle association; it is historical evidence, not a required expected association. Source/receipt clocks remain in the files.

| File | Original bytes | SHA-256 of original bytes |
|---|---:|---|
| `metro-debug-direct-capture.json.gz` | 435344 | `c894daf657516b52a2be7775231276543a5b2398b3dc13fc53bb9c2637f85d42` |
| `metro-debug-static.json.gz` | 2501531 | `e09c7e9aaaa23ee79ef4f93115663e2428840eabc684d27b38643d5b3c9e03ca` |
| `metro-debug-5B.json.gz` | 32981 | `a4e6db7e54dd69ff7d55c9405373dbfc7cc944eb9a5abe43100957a8d4d4e251` |

Sources: the deployed dashboard public API/cache reads at `https://lisboapublica.rtfpessoa.xyz`, and the direct Metro publication source identified within each captured result. No credentials or authentication headers are included. The source clocks are preserved for replay at the captured time, rather than interpreted as current data.
