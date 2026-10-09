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
| S-028.4 | Add a shell step with workflow `env: { SHARED_VALUE: workflow }` and `with.env: { SHARED_VALUE: step, STEP_ONLY: declared }`; have it print `PATH`, `HOME`, `KB_PLATFORM_ROOT`, `KB_WORKSPACE_ROOT`, `SHARED_VALUE`, `STEP_ONLY`, and an undeclared host variable | The shell receives runtime `PATH` and `HOME`, the platform-owned root values, `SHARED_VALUE=workflow` (workflow env takes precedence over `with.env`), and `STEP_ONLY=declared`; the undeclared host variable is absent. Step diagnostics show environment key names but omit environment values. |
| S-028.5 | Declare `W01_SECRET` in workflow `secrets` and `W01_HOOK_SECRET` only on a job hook step, provide both through the configured secret provider, run the workflow, then inspect server logs for the run | No job or step log record contains either secret value. Ordinary environment values such as `NODE_ENV=production` and a log message containing `production` remain readable. A provided secret shorter than 8 characters is rejected while loading the workflow with `KB_CONFIG_INVALID`; its hint identifies the configured provider source and the next action. A missing declared secret fails with `KB_CONFIG_SECRET_MISSING` and identifies the secret name. |
