---
id: S-211
title: Optional adapters — expose backend failures to callers and operators
persona: platform-operator
area: platform-core
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli, api]
---

## Goal

When an optional adapter backend fails, the caller receives a typed failure and
operators can identify the backend failure from contextual logs where the
adapter contract provides logging.

## Steps

| # | Action | Expected |
|---|---|---|
| S-211.1 | Configure Qdrant, make a vector-store upsert fail persistently, then perform the operation through the Mind/vector-store caller | The adapter makes only the configured bounded retry attempts, then returns catalogued `KB_ADAPTER_OPERATION_FAILED` naming `vectorStore`; the command fails with an actionable hint and never loops indefinitely or reports success. |
| S-211.2 | Stop or interrupt the configured Redis service, then run a cache operation through the platform | The operator log records the Redis connection failure with adapter context; the cache command fails with catalogued `KB_ADAPTER_OPERATION_FAILED` naming `cache`, and does not return a cache miss or success. |
| S-211.3 | Make SQLite pruning fail during the daily retention pass and inspect operator logs | A contextual platform log records the prune failure and analytics adapter path; no process warning is emitted and no adapter operation is written as a product analytics event. |
| S-211.4 | Cause an adapter operation to fail after its bounded retries, then invoke it through a CLI/API caller | The caller receives `{ "ok": false, "error": ... }` with catalogued `KB_ADAPTER_OPERATION_FAILED`, the adapter slot, a plain-language reason, and an exact recovery hint; no raw error or stack is returned. |
| S-211.5 | Cause a non-missing filesystem access error, or corrupt an existing local snapshot/workspace registry record, then query or read the affected adapter resource | The caller reports the filesystem or parse failure rather than `not found`; the user-facing result contains no raw error or stack, and the matching structured log retains the cause. |

## Evidence

These steps are covered by adapter unit and integration tests. Redis and Qdrant
require non-default services, and the SQLite prune-log step depends on the
24-hour pruning timer, so they are outside release journey coverage.
