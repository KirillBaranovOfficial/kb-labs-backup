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
| PC-003.1 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` on a healthy install | Structured result reports passing checks. |
| PC-003.2 | Stop a configured service and run `kb-dev status --json` | Status identifies the unavailable service. |
| PC-003.3 | Run the doctor again | Finding identifies the problem and provides a hint without printing secrets or a raw stack trace. |
