# Metro refresh sandbox experiment — 2026-10-24

This is a bounded sandbox observation, not a permanent capacity or physical
accuracy guarantee. The canonical [deployment guide](../../deploy/README.md)
describes the current configuration and operational checks.

## Change and pre-restart evidence

The running dashboard used image
`lisboa-publica-sandbox:40cb1c40633e80af0070cf98f6e0aeb506aef3cb`, with
image ID `sha256:d892b5f36792219e0a0ebb63de4a1a9b852ecacedac4b0e970d701197f288853`.
In the shared `/root/dev/server` Compose project, the protected dashboard
environment's explicit `METRO_REFRESH_MILLISECONDS` was changed from `500` to
`1000`. The application default remains 500 ms. The complete resolved Compose
configuration differed only in that value; the image, archive and database
mounts remained the same. The dashboard alone was recreated with `--no-deps`
and `--no-build`. Other containers' IDs, running and health states, and restart
counts matched the pre-change baseline. The dashboard became healthy at
12:34:56 UTC.

At 12:25:23 UTC, an earlier diagnostic showed a queue depth of 60/64, 20
cumulative full-queue rejections and a pending cut. The last captured diagnostic
before stopping, at 12:33:53 UTC, showed depth 13, 24 full-queue rejections,
zero delivery failures and a valid, nonpending cut. These are snapshots, not
an exact count of the queue at shutdown. Counters reset on restart.

## Backup and storage

After stopping the owner and draining its database clients, a protected fresh
archive tar was saved at
`/root/lisboa-release-40cb1c4-vWIZE9xT/transport-history.tar`: 163,553,280
bytes, SHA-256
`03a9de6403f85aa4971a1b6ddbf113d48b50f772f7e9f72bb4b466444d7937d8`.
The streamed tar matched that hash; its separately extracted copy in private
Colima volume `lisboapublica-40cb1c4-archive-20261024c` matched the quiesced
inventory, including 4,921 entries and 4,918 manifest block hashes.
This verifies that copy; it does not recover queued or other unacknowledged data.
No database or archive backup was restored over newer writes.

A fixed one-hour cohort from 2026-10-19 12:00 UTC contained 44 database
snapshots before and after the experiment, with the same fingerprint. At the
later check, pressure cleanup's cutoff was 2026-09-29 07:29:55 UTC, insertion
credit was 4,949 and no vacuum was pending. Database maintenance logged
periodic passes. The configured seven-day floor applies to pressure cleanup;
normal configured expiry can be shorter, and missing observations cannot be
recovered by that floor.

At the later check, the archive occupied 167,124,992 allocated bytes against
the sandbox's 1,000,000,000-byte archive cap. The shared filesystem had
2,788,794,368 bytes and 1,873,970 inodes available; the archive and database
filesystem guards each configured minimum floors of 2,147,483,648 free bytes
and 10,000 free inodes. Actual writes require additional headroom. These floors
do not reserve capacity against other writers or guarantee that seven days will fit.

## Observation

From 12:35:55 to 12:51:25 UTC, 32 approximately 30-second polls returned HTTP
200 for public health, Metro live, Metro patterns and Metro transport patterns.
Health and the sampled public Metro status were `ok`; both pattern views and
live history reported `collecting`. Maximum observed response times for those
four requests were 377, 1,409, 1,097 and 1,261 ms respectively. These samples
are not a latency percentile or a sustained-load test.

Fifteen distinct delivery diagnostics from 12:34:57 to 12:50:44 UTC showed
queue depths between 0 and 12/64. Depth rose to 12, drained to 0, and was 9
in the final diagnostic. That diagnostic reported 937 admitted and 928
dequeued tasks, zero `rejected_full`, `rejected_bytes`, `rejected_encoding`,
`rejected_stopped` or `delivery_failed`, and a valid nonpending cut with one
acknowledgement. No warning logs were observed in the sampled window. Queue
snapshots are approximate; a missing diagnostic is not evidence of health.

At 12:50 UTC, the manifest-referenced Metro detail block's SHA-256 matched the
file and its 85 decoded receipts included a latest receipt at 12:50:35 UTC,
after startup. This is durable detail evidence beyond the startup cut
acknowledgement. The two fully post-start database history buckets covering
12:35–12:40 and 12:40–12:45 UTC contained 1,253 and 1,252 rows, respectively;
buckets close after the additional 90-second source-freshness delay and a
subsequent operator save.

Fourteen live-frame samples between 12:38:18 and 12:50:30 UTC included train
source clocks; the oldest clock in any sample was at most 75.8 seconds behind
its sample time, and the newest at most 2.9 seconds behind. `sampled_at` in
delivery diagnostics and pattern `as_of` are local receipt clocks, not provider
clocks or proof of durability. Neither these observations nor inferred journeys
establish physical train identity, actual passage accuracy or provider update
frequency.

Stopping can discard queued or in-flight Metro tasks and unfinished in-memory
five-minute history buckets; the amount lost here is unknown. Earlier gaps
remain recorded where evidence exists. A one-second target can reduce the request
rate and delivery pressure but can delay or coarsen movement observations
compared with the 500 ms target. Monitor beyond this short experiment before concluding that the
rate is sustainable.
