---
id: PC-002
title: PC-002 — Start the V2 host and confirm services
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer starts the V2 host runtime and confirms its managed services become healthy.

## Prerequisites

- [ ] V2 installation with a configured platform root and `kb-dev` runtime configuration.

## Steps

| # | Action | Expected |
|---|---|---|
| PC-002.1 | With no project registered, run `kb-create start --platform-root "$PLATFORM_ROOT" --json` | Exits 0; on success, JSON has `ok: true`, `operation: "start"`, and `host.pid` greater than 0. Starting the platform host does not require project registration. If startup fails, `KB_HOST_START_FAILED` identifies the process exit code or signal, or the readiness URL, last HTTP status or probe error, and elapsed wait; it includes the last host stderr lines and uses any typed host startup code as the error cause. |
| PC-002.2 | Set `ACTIVE_VERSION_ROOT="$(cd "$PLATFORM_ROOT/current" && pwd -P)"`; run `kb-create status --platform-root "$PLATFORM_ROOT" --json`, then repeat with `--platform-root "$PLATFORM_ROOT/current"` and `--platform-root "$ACTIVE_VERSION_ROOT"` | Each invocation exits 0; JSON has `ok: true`, `host.state: "running"`, `running: true`, and `reachable: true`. All three paths identify the same installation and running host. |
| PC-002.3 | Request `GET /ready` from the configured gateway and `GET /api/v1/projects/$PROJECT_ID/workflow/health` | Both requests return HTTP 200 with JSON response bodies. Gateway readiness reports every required machine-level upstream ready; the workflow service health request starts the registered project's lazy runtime and returns `{ok:true,data:<workflow health payload>}`. The project router calls the health path declared by that service's manifest, independent of its business-route rewrite prefix. |
| PC-002.4 | Run `kb-create stop --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok: true` and `operation: "stop"`; a subsequent status reports `host.state: "stopped"` and `running: false`. |
| PC-002.5 | In local and secured mode, on a fresh host with no project runtime active, run a registered project workflow with `kb workflow run`, then inspect the returned run ID until it is terminal and inspect host health | The workflow reaches `success`; its first state lease operation reaches the host's live StateBroker address; host health lists the project runtime as ready after the first invocation. Gateway readiness reports every required machine-level upstream ready without requiring an idle project runtime. The installed platform config contains no host service URLs or fixed host-service ports. |
| PC-002.6 | From an unregistered temporary directory, run `kb --version` | The CLI prints its version and exits successfully without requiring project registration or initializing project-scoped adapters. |
| PC-002.7 | From an unregistered solo development checkout with no `.kb/kb.config.json` and no `platform.mode`, run `kb hello --json` | The command exits 0 and returns `Hello World`. Missing user config and an omitted development mode are valid states; built-in adapter defaults remain available. |
| PC-002.8 | With a project registered as `$PROJECT_ID` at `$PROJECT_ROOT`, inspect host health and request `GET /api/v1/projects/$PROJECT_ID/rest/health` through the gateway | Host health lists `$PROJECT_ID` as a ready runtime and the project REST health route returns HTTP 200 with `{ok:true,data:<REST health payload>}`. The project router calls the health path declared by that service's manifest. The runtime serves the canonical registered project root rather than the platform root. |
