---
id: PC-004
title: PC-004 — Restart and recover host services
persona: solo-developer
area: platform-core
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer restarts V2 host services and verifies the configured runtime returns to a healthy state.

## Prerequisites

- [ ] V2 host services running with a known runtime config and free network offset.

## Steps

| # | Action | Expected |
|---|---|---|
| PC-004.1 | Run `kb-create restart --platform-root "$PLATFORM_ROOT" --json` while the host is running | Exits 0; JSON has `ok: true`, `operation: "restart"`, and `host.pid` greater than 0 for the restarted host. |
| PC-004.2 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `host.state: "running"`, `running: true`, and `reachable: true`. |
| PC-004.3 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok`, `installed`, `running`, and `reachable` set to `true`; installation and platform checks pass. |
