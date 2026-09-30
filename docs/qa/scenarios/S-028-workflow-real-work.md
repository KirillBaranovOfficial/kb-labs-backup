---
id: S-028
title: S-028 — Workflow performs real work
persona: solo-developer
area: workflow
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project workflow uses `builtin:shell` to write a file and make an HTTP request, then exposes the command results in run logs.

## Prerequisites

- [ ] Project workflow runtime, a local HTTP test endpoint, and a disposable marker-file path.

## Steps

| # | Action | Expected |
|---|---|---|
| S-028.1 | Define a `builtin:shell` step with `run: printf done > /tmp/kb-workflow-marker` | Workflow definition passes lint. |
| S-028.2 | Define a `builtin:shell` step with `run: curl -fsS http://127.0.0.1:<test-port>/health` | Workflow definition passes lint; endpoint is reachable from the workflow runtime. |
| S-028.3 | Run the workflow and inspect `kb workflow runs logs <runId>` | File exists with `done`; logs include the HTTP response and successful command status. |
