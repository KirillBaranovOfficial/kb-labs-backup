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

A project user configures a cron-triggered workflow and verifies that the schedule is visible to the runtime.

## Prerequisites

- [ ] Project workflow definition that supports a cron trigger.

## Steps

| # | Action | Expected |
|---|---|---|
| S-044.1 | Add a cron trigger to the project workflow definition and run `kb workflow lint` | Definition passes validation. |
| S-044.2 | Run `kb workflow list --type=cron` | Workflow is listed with its schedule/trigger information. |
| S-044.3 | Open the project’s Cron view in Studio or inspect the workflow schedule API | Configured expression and workflow are visible for the selected project. |
