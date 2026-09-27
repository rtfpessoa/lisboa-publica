# Vehicle popup validation — 2026-09-27

The runtime source validated here includes all eight operators,
independent arrival/departure evidence and safe full-journey associations.
No production source is certified to collect actual stop occurrences;
synthetic PostgreSQL events test the durable boundary without claiming source coverage.

| Check | Result | Evidence |
|---|---|---|
| Normal Maat commit hook | Go score 89, delta 0, no critical regressions, no suppressions; TypeScript unchecked | [Maat](maat.txt) |
| PostgreSQL-backed complete Go race suite | Passed | [Go](go-race.txt) |
| TypeScript build and frontend fixtures | Build passed; 91 browser tests passed, 2 optional full-geometry browser tests skipped | [Browser](browser.txt) |
| Generated Go/TypeScript contracts | Exact regeneration comparison passed | `scripts/check-generated.sh` |
| Go vet and affected documentation links | Passed | Local verification |
| Linux/ARM64 full resource workload | Passed in 113.59 seconds under a hard 1024 MiB memory and swap limit, exit 0, no OOM kill | [Resource log](linux-memory.txt) |

The resource workload used the eleven downloaded official archives,
all eight providers, 64 retained revisions, 20,000 pending historical rows,
512 distinct forecast snapshots and a complete overlapping static refresh.
It retained all 1,621 CM published paths and 57,286 visits,
performed concurrent frozen-reference navigation and decoded two near-16 MiB ETA feeds.
The test ran without network access in an isolated Linux/ARM64 container,
with two CPUs, `GOMAXPROCS=2` and `GOMEMLIMIT=768MiB`.
The deployed container retains its existing 1280 MiB limit.

Earlier CM-only compaction failed the isolated 1024 MiB limit during refresh.
Applying lossless visit storage to every road operator resolved that failure.
No source visits, original clocks, retained revisions or production limits were removed or relaxed.
