---
id: S-044
title: S-044 — Configure and inspect a workflow cron schedule
persona: solo-developer
area: workflow
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio, api]
---

## Goal

A project user enables a cron workflow, observes a scheduled run and its output, then disables the schedule and confirms later intervals do not start new runs.

## Prerequisites

- [ ] Registered project with the workflow daemon available.
- [ ] The project runtime remains available while an enabled schedule is registered.

## Steps

| # | Action | Expected |
|---|---|---|
| S-044.1 | Create `.kb/jobs/journey-cron.yml` with `schedule: '* * * * *'`, `autoStart: true`, and `enabled: true`, then run `kb workflow list --type=cron --json` | The runtime accepts and registers the enabled schedule for the selected project. |
| S-044.2 | Run `kb workflow list --type=cron --json` | The enabled schedule and its one-minute expression are visible. |
| S-044.3 | Inspect `kb workflow runs list --workflow=journey-cron --json` and `kb workflow runs logs <runId> --json` after the next interval | A scheduled run reaches success and its output contains the workflow's log line. |
| S-044.4 | Set `enabled: false`, confirm the cron entry reports disabled, then inspect history after one full interval | No additional scheduled run starts after the schedule is disabled. |
