---
id: S-212
title: Analytics — local adapter and scheduler mechanics
persona: platform-maintainer
area: analytics
priority: P1
status: active
release: false
modes: [local]
surfaces: [api]
---

## Goal

Verify local analytics mechanics that need controlled clocks or direct adapter
access and therefore are not release-journey outcomes.

## Steps

| # | Action | Expected |
|---|---|---|
| S-212.1 | Inspect `platform.adapterOptions.analytics` in `.kb/kb.config.jsonc`, then query for an event older than 90 days after startup and again after a day of uptime | The default retention is 90 days; events older than that age are absent after startup and at least daily while recent events remain queryable. |
| S-212.2 | Set `platform.adapterOptions.analytics.retentionDays` to `30`, restart the platform, and query for an event older than 30 days after startup and again after a day of uptime | SQLite analytics removes events older than the configured 30-day age on startup and at least daily. |
| S-212.3 | Have the fixture plugin perform cache, storage, and vector-store operations, then query `/api/v1/analytics/events?from=<before-operations>&to=<after-operations>` | The time range contains no raw cache, storage, or vector-store operation events; fixture LLM and embeddings usage events remain queryable with their usage metrics. |
