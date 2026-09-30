---
id: S-028
title: Workflow — Run shell work and inspect its output
persona: solo-developer
area: workflow
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project workflow performs a local shell operation and an HTTP health request, then exposes both command outputs in run logs.

## Prerequisites

- [ ] Host running with workflow commands available from a registered project.
- [ ] A disposable HTTP health endpoint is running on a test port reachable from the workflow runtime and returns body `healthy` with HTTP 200.
- [ ] A disposable marker file path inside the project is selected.
- [ ] In secured mode, an authenticated gateway session is available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-028.1 | Create `.kb/workflows/real-work.yml` with manual trigger and a `builtin:shell` step whose `with.command` is `printf done > .kb/workflow-marker` | `kb workflow lint` reports the definition valid. |
| S-028.2 | Add a second `builtin:shell` step with `with.command` set to `curl -fsS http://127.0.0.1:<test-port>/health`, replacing `<test-port>` with the health endpoint's port | `kb workflow lint` reports both steps valid. |
| S-028.3 | Run `kb workflow run --workflow-id=real-work`, wait for terminal status, then run `kb workflow runs logs <run-id>` | Run status is `success`; `.kb/workflow-marker` contains exactly `done`; logs include `healthy` for the HTTP step and successful completion for both step IDs. |
