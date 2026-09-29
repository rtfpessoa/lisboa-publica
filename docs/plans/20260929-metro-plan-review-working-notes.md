# Plan review working notes — 2026-09-29

Requested reviewer: quasar-alpha, xhigh, review-only. The tool accepted this override despite its omission from the displayed model list. No fallback reviewer has been used. Application implementation remains unchanged pending the revised-plan gate.

Early actionable findings accepted by the primary agent:

1. Hub `created_at` is model processing/publication in the inspected tracker, not the underlying original `hora`. Explicit unknown input age is required. Repeated/advancing model clocks cannot train new independent event samples or renew original-input validity.
2. Anonymous forecast ownership requires a precise assignment universe. Partial order alone cannot exclude hidden candidates. Separate deterministic evidence-supported association from estimated operational ownership; preserve unknown/unmatched alternatives and source identity independently.
3. Current service availability and archive durability require separate transactions. In-memory activation/handoff must be atomic under one bounded owner, with async fenced durable revisions, retry/capacity policy and recovery re-admission; no hidden disk-dependent current-link suppression.
4. Masked-identity validation uses chronological day/episode splits and source revision deduplication. Candidate removal/cohort thinning/short-turn insertion are mandatory rejection cases. Candidate vehicle identities are production inputs; the masked per-slot target, per-slot identity-derived features and future named corrections must not leak into evaluation. Source-dependent coordinates must be evaluated as model consistency rather than physical allocation.

Final review and amended contract will be recorded separately; these early findings are not a final acceptance verdict.
