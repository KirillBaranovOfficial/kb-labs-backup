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
| S-211.6 | Configure an installed adapter whose factory fails because of invalid options or a missing peer, then separately configure a missing package; restart the host, inspect health/status and call each affected slot | The host starts and serves health/status. `/health/adapters`, `kb doctor`, and `kb adapter list` identify each slot as `degraded` with reason `load-failed`, its configured package, and a correlation ID. A package-not-found failure preserves the underlying message and error code and suggests the exact `kb marketplace install <pkg> --scope platform` command; any other failure preserves the underlying message and error code and suggests `kb-create doctor --platform-root <platform-root>` (or the exact config fix when an option is named). If the host is stopped, `kb adapter list` reports runtime status as `unknown` and gives `kb-create start`. An operation through the degraded slot returns `KB_HOST_ADAPTER_UNAVAILABLE` with that reason and recovery action; every property access except `then` and symbol inspection throws this typed error. Other slots remain available. |
| S-211.7 | Run `kb config set --scope platform platform.adapterOptions.llm.baseURL 42 --json` with the OpenAI adapter bound, then repeat with a valid string; try binding a missing package or a package that does not declare the requested slot through both `kb config set --scope platform platform.adapters.llm <package>` and `kb adapter bind llm <package>` | The invalid option fails with `KB_CONFIG_INVALID`, names `platform.adapterOptions.llm.baseURL` and `Expected string`, and leaves the stored value unchanged; a valid string is accepted without a network request. A missing package fails through both binding commands with identical `KB_ADAPTER_NOT_INSTALLED` envelopes using message `Adapter package <pkg> is not installed on this platform.` and hint `kb marketplace install <pkg> --scope platform`. An installed package that does not declare the slot fails with `KB_ADAPTER_SLOT_MISMATCH`, naming the slot and package and giving the exact install command for a provider that declares that slot. If an option's schema package is absent, the write fails with `KB_ADAPTER_NOT_INSTALLED`. Rejected writes leave config unchanged. |

## Evidence

These steps are covered by adapter unit and integration tests. Redis and Qdrant
require non-default services, the host degradation step requires a deliberately
missing adapter package, the config step checks rejection before persistence,
and the SQLite prune-log step depends on the 24-hour pruning timer, so they are
outside release journey coverage.
