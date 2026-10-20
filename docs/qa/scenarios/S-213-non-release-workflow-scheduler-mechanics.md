---
id: S-213
title: Workflow — empty-queue polling mechanics
persona: platform-maintainer
area: workflow
priority: P1
status: active
release: false
modes: [local]
surfaces: [api]
---

## Goal

Verify scheduler polling mechanics with a controlled local queue.

## Steps

| # | Action | Expected |
|---|---|---|
| S-213.1 | Leave the registered project's workflow daemon running with no queued jobs for one minute, then query `/api/v1/projects/<PROJECT_ID>/rest/analytics/events?type=cache.setIfNotExists.error` | The empty-queue poll creates no cache operation analytics rows; a lock held by another worker is treated as contention and does not create an error row in that project. |
