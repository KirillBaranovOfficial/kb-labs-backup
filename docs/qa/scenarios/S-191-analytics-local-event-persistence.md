---
id: S-191
title: Analytics — persist and query local event data
persona: plugin-developer
area: analytics
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [api]
---

## Goal

Emit an analytics event, query it by type, source, and date range, and confirm
the default local SQLite adapter retains it across a platform restart.

## Prerequisites

- [ ] A registered project on a fresh default platform install using `@kb-labs/adapters-analytics-sqlite`.
- [ ] An authenticated API client and a fixture plugin that emits a uniquely named event through `platform.analytics.track`.

## Steps

| # | Action | Expected |
|---|---|---|
| S-191.1 | Have the fixture plugin call `platform.analytics.track("case.analytics.persist", { source: "case-fixture" })` | The event is accepted by the configured analytics adapter and stored with the generated event type, timestamp, and analytics context. |
| S-191.2 | GET `/api/v1/analytics/events?type=case.analytics.persist&source=case-fixture&from=<before-event>&to=<after-event>` | The response includes the emitted event and excludes fixture events outside the requested type, source, or date range. |
| S-191.3 | Restart the platform with `kb-create restart --platform-root "$PLATFORM_ROOT"`, then repeat the same analytics query | The same event remains queryable after restart from the default local SQLite adapter. |

## Pass criteria

The fresh default config selects `@kb-labs/adapters-analytics-sqlite`
(`.kb/kb.config.jsonc:2-14`; `reports/w3-marketplace.md:24` identifies
S-054 as the fresh-default adapter case, though that case is not in this
worktree). The SQLite adapter stores events in its per-project runtime
database and writes event context and payload (`adapters/analytics-sqlite/src/index.ts:130-170,233-257`). The REST query accepts `type`, `source`, `from`,
and `to` and forwards them to the analytics adapter
(`services/rest-api/app/src/routes/analytics.ts:33-93`); SQLite applies those
filters (`adapters/analytics-sqlite/src/index.ts:408-445`). S-121 covers the
Studio analytics views and filters, not ingestion, durable local persistence,
or the query API lifecycle covered here.
