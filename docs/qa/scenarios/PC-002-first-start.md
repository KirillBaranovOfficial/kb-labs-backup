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
| PC-002.1 | Run `kb-create start --platform-root "$PLATFORM_ROOT" --json` | Host services start under the configured network offset. |
| PC-002.2 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | JSON lists each configured host service and its current state. |
| PC-002.3 | Check the configured gateway and workflow health endpoints | Each endpoint returns a successful health response. |
| PC-002.4 | Run `kb-create stop --platform-root "$PLATFORM_ROOT" --json` | Managed services stop cleanly. |
