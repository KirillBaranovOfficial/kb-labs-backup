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

- [ ] Platform installed with access to the package registry.
- [ ] Project A and project B initialized; project B has no storage adapter config.

## Steps

| # | Action | Expected |
|---|---|---|
| S-048.1 | In project A, run `kb marketplace install @kb-labs/adapters-fs@<version> --scope platform`, then `kb adapter bind storage @kb-labs/adapters-fs --dry-run --json` | The adapter is installed in the machine store; bind returns `ok: true` and `dryRun: true` without changing project A config. |
| S-048.2 | Run `kb adapter bind storage @kb-labs/adapters-fs --json` | Reports the project A config file and successful binding. |
| S-048.3 | Run `kb config set platform.adapterOptions.storage '{"baseDir":".kb/data"}' --json` | Reports the project-scoped value was saved. |
| S-048.4 | Inspect project A and project B configs | Project A has the storage binding and options; project B remains unchanged. |
| S-048.5 | Run the project's documented `e2e-hello` workflow and inspect its completed run | The workflow succeeds while project A's storage adapter is bound, exercising the configured storage capability. |
| S-048.6 | Run `kb adapter unbind storage --json` and inspect project A config | The binding is removed from project A; its adapter options remain available and project B is unchanged. |
