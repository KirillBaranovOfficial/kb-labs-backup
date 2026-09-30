---
id: S-048
title: Adapters — bind a platform adapter with project config
persona: solo-developer
area: config
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

An installed platform adapter is bound to one project slot and its options are saved in that project’s user config.

## Prerequisites

- [ ] Adapter installed at platform scope and two projects available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-048.1 | Run `kb adapter bind storage <package-id> --dry-run --json` in project A | Returns `ok: true` and `dryRun: true`; writes no config. |
| S-048.2 | Run it without `--dry-run` | Reports project A config file and successful binding. |
| S-048.3 | Run `kb config set platform.adapterOptions.storage <json-value>` | Reports the value was saved. |
| S-048.4 | Inspect both project configs | Project A has binding and options; project B is unchanged. |
