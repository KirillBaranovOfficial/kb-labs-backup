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
| PC-002.1 | With no project registered, run `kb-create start --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok: true`, `operation: "start"`, and `host.pid` greater than 0. Starting the platform host does not require project registration. |
| PC-002.2 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok: true`, `host.state: "running"`, `running: true`, and `reachable: true`. |
| PC-002.3 | Request `GET /ready` from the configured gateway and `GET /api/v1/projects/$PROJECT_ID/workflow/health` | Both requests return HTTP 200 with JSON response bodies. |
| PC-002.4 | Run `kb-create stop --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok: true` and `operation: "stop"`; a subsequent status reports `host.state: "stopped"` and `running: false`. |
| PC-002.5 | In local and secured mode, run a registered project workflow with `kb workflow run`, then inspect the returned run ID until it is terminal | The workflow reaches `success`; its first state lease operation reaches the host's live StateBroker address. The installed platform config contains no host service URLs or fixed host-service ports. |
