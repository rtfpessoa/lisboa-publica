# Vehicle state, plates and CP scheduled endpoints

26 September 2026. The user authorized implementation, Maat fixes, commit, push and deployment following the independently accepted plan. Main owns implementation, tests and every correction; existing real `quasar-alpha` reviewers at `xhigh` perform independent reviews only.

## Implemented scope

- Format four recognized Portuguese road plate layouts for bus details/fleet; preserve raw API/history registrations and non-road/unknown IDs. Plate searches accept compact, spaced and hyphenated full/partial queries without changing model/ID matching.
- Retain recognized Hub/CM source stop status, bounded source stop references and verified retained station names. Preserve original observation clocks in live cache and immutable revisions. Current stopped observations remain normal; older states are explicitly last known. Missing status remains unknown; estimated Metro status is labelled estimated. Missing updates after five minutes replace inactive wording; expiry remains one hour.
- Capture CP scheduled full first/last stop IDs/names before Lisbon filtering. Exact observed joins require explicit valid source operating date, matching active plan, route, trip and calendar. Scheduled trip tables distinguish full endpoints from first/last retained Lisbon times. Legacy CP caches refresh once through the existing collector and restore normal TTL after an attempt.
- Extend one OpenAPI spec and regenerate Go/oazapfts types. No new source requests/polling loop, historical state payloads, database schema, fake GPS markers, forced speed zero, synthetic observations or storage/rate changes.
- Previously accepted dismissible popup work is preserved. Direct CP realtime access/reuse, quotas, source timestamps and identifier crosswalks remain unadmitted; additional position coverage and measured delay are not claimed.

## Checks and corrections

[Saved validation logs](research/cp-service-stopped/validation/) include Postgres full race (15.538s), CockroachDB full race against an owned local instance (161.904s), TypeScript/Vite build and ten search repetitions. Generator comparison, Go vet and `git diff --check` pass.

The populated official resource scenario preserves 1,000 source rows per provider, all eight providers, one-hour churn, 64 read revisions and capped retention/ledgers. It fills the new status/stop/date fields and CP service objects. Peak RSS is 984,121,344 bytes (938.53 MiB), below the existing 1,280 MiB container budget; heap 826,029,288 bytes and runtime system memory 989,594,088 bytes. Actual official static/live cache reservations remain admitted. This local macOS scenario is not a Linux production RSS guarantee.

First review found two issues, fixed by main: an old retained notice still called positions inactive; full CP endpoints were paired with unqualified regional departure/end times. Browser/backend regressions now assert no-update wording and explicit first/last local times, while the API keeps existing local time/filter semantics. First reviewer finds no remaining concrete code issue, with acceptance contingent on saved final validation results.

Earlier browser runs exposed obsolete raw-plate/no-update assertions and one intermittent search-opening failure; the corrected expectations and ten repeated search checks pass. The final full fixture suite and independent final verdict are recorded below when complete. An older overloaded local Cockroach test run was interrupted; an initial background test node did not survive its tool lifecycle. The owned foreground instance produced the passing full race result above. Production data was not used for these tests.

## Commit and rollout status

The initial popup-only commit attempt passed the normal supported-language gate with TypeScript unchecked, then failed at 1Password signing. SSH authentication also failed through that agent; the user has been asked to unlock it. No bypass, push or deployment has occurred. The Go-containing candidate's normal Maat result, repairs, final independent review, signed commit and production evidence will be recorded here as they complete.
