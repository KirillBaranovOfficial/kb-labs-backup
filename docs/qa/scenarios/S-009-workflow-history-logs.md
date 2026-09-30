---
id: S-009
title: S-009 — Inspect workflow history and logs
persona: solo-developer
area: workflow
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project user finds previous workflow runs and inspects a run’s terminal state and step output.

## Prerequisites

- [ ] At least two workflow runs have completed in the selected project.

## Steps

| # | Action | Expected |
|---|---|---|
| S-009.1 | Run `kb workflow runs list` | Recent runs show run ID, workflow, status, and time. |
| S-009.2 | Run `kb workflow runs view <runId>` | Detail shows the selected run and its terminal status. |
| S-009.3 | Run `kb workflow runs logs <runId>` | Step logs are visible for that run. |
| S-009.4 | Select a failed run | The failure reason and failed step are visible in run detail or logs. |
