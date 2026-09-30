---
id: PC-003
title: PC-003 — Diagnose platform health
persona: solo-developer
area: platform-core
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer checks V2 installation and runtime health and gets named findings for a broken setup.

## Prerequisites

- [ ] V2 platform root and runtime config.

## Steps

| # | Action | Expected |
|---|---|---|
| PC-003.1 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` on a healthy running install | Exits 0; JSON has `ok`, `installed`, `running`, and `reachable` set to `true`, with `installation.ok` and `platform.ok` set to `true`. |
| PC-003.2 | Move `.kb/v2/receipt.json` out of the platform root and run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Exits 1; JSON has `ok: false`, `installed: false`, `running: false`, `reachable: false`, and `next: "kb-create install"`. |
| PC-003.3 | Restore `.kb/v2/receipt.json` and run the doctor again | The healthy result from PC-003.1 is restored; no secret value or stack trace appears in terminal output. |
