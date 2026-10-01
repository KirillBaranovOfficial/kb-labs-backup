---
id: S-198
title: Projects — Keep workflow and cron runs within their project
persona: solo-developer
area: workflows
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, studio, api]
---

## Goal

Run workflows and scheduled work for multiple repositories while keeping
definitions, executions, history, and logs scoped to each project.

## Prerequisites

- [ ] Projects A and B are registered and have different workflow definitions and cron schedules.
- [ ] Both projects have workflow storage configured and the gateway can start their runtimes.

## Steps

| # | Action | Expected |
|---|---|---|
| S-198.1 | From Project A, run its workflow and then run `kb workflow runs list --json`; repeat from Project B | Each command runs that project's workflow and lists its run ID and workflow ID. Neither project's list includes the other project's run. |
| S-198.2 | Inspect each run's detail and step logs using `kb workflow runs view <run-id>` | The run output and captured logs contain only that project's fixture data and are stored in that project's runtime history. |
| S-198.3 | Create and enable a cron in Project A, then inspect `kb workflow crons list --json` from both project roots | Project A lists the enabled cron; Project B does not list it. Each cron targets a workflow in its own project. |
| S-198.4 | Advance the scheduler through Project A's due time and inspect both project histories and Studio's selected-project Crons and Runs views | Exactly the Project A scheduled run appears in A's history and views; B shows no cron or run from A. Logs are available only while viewing A. |

## Dependencies

- S-002, S-044, S-055, S-184 and S-187 cover individual workflow, cron,
  Studio, and installed-project behavior. This case verifies the two-project
  boundary through the full run and log lifecycle.
