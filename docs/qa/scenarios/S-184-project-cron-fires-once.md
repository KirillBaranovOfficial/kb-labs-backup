---
id: S-184
title: Workflow — a project cron fires once in its project
persona: solo-developer
area: workflow
priority: P0
status: planned
release: true
modes: [local, secured]
surfaces: [cli, studio, api]
---

## Goal

Schedule a workflow for one project, inspect its schedule and prove it fires
once at the due instant without crossing into another project.

## Prerequisites

- [ ] Projects A and B are registered, and Project A contains workflow `cron-target`.
- [ ] A deterministic scheduler clock can advance through one due instant without wall-clock sleep.
- [ ] The test fixture can inspect both projects' workflow run histories.

## Steps

| # | Action | Expected |
|---|---|---|
| S-184.1 | In Project A create `.kb/crons/daily.yml` with `schedule: "0 9 * * *"`, `timezone: Europe/Moscow`, `enabled: true`, and `target: { workflow: cron-target, input: { source: fixture } }`; omit timezone in a second fixture cron, then run `kb workflow crons list --json` | The Cron view and `GET /api/v1/crons` list the Project A cron with target workflow, input, expression and timezone; the second cron reports the default timezone `UTC`. |
| S-184.2 | Advance the deterministic scheduler clock through the next due instant for `daily` | Exactly one run appears in Project A's `kb workflow runs list` / run history with scheduled-trigger metadata identifying the cron. |
| S-184.3 | Inspect Project B's Cron view/API and workflow run history after that due instant | Project B has no Project A cron and no run attributed to `daily`. |
| S-184.4 | Run `kb workflow crons disable daily`, run `kb workflow crons list --json`, then advance through the next due instant | The list and `.kb/crons/daily.yml` show `daily` disabled and no new run is created for it. |
| S-184.5 | Skip a due instant while the cron is enabled, advance beyond it, and inspect the run history | The missed occurrence is not caught up; no run is created for the skipped instant. |

## Pass criteria

The owner decision defines `.kb/crons/<id>.yml`, UTC as the default timezone,
the `kb workflow crons list|enable|disable` commands, `/api/v1/crons`, the
Studio Crons page and no catch-up for missed runs. The workflow stream report
describes that implementation, but this checkout does not contain its new
cron entity/runtime changes. Its journey still uses a 65-second quiet-interval
wait and no deterministic clock is evidenced here. This case remains planned
until a deterministic scheduler clock drives the due and disabled checks.
