---
id: S-048
title: Adapters — override and restore the project storage default
persona: solo-developer
area: config
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

The built-in filesystem storage adapter works by default. A project can override
its base directory, write workflow run records there, then unbind the override
and return to the platform default without affecting another project.

## Prerequisites

- [ ] Platform installed with the built-in filesystem storage adapter.
- [ ] Projects A and B initialized with no project-level storage adapter config.
- [ ] The workflow service persists run records through `IStorage` at
  `<storage-root>/workflows/<runId>.json` (owned by the workflow stream).

## Steps

| # | Action | Expected |
|---|---|---|
| S-048.1 | With project A and B both using defaults, run the `e2e-hello` workflow from each project and inspect each completed run record | Each record exists under its project's `.kb/data/workflows/<runId>.json`; each contains its own `runId` and terminal `success` status. Neither project has a project-level storage override. |
| S-048.2 | In project A, run `kb config set platform.adapterOptions.storage '{"baseDir":".kb/override-data"}' --json`, then `kb adapter bind storage @kb-labs/adapters-fs --json` | Reports successful project-scoped binding; project A config selects the built-in fs adapter with the override base directory. Project B remains unconfigured and keeps the platform default. |
| S-048.3 | Run the `e2e-hello` workflow in project A and inspect its completed run record | `<project-A>/.kb/override-data/workflows/<runId>.json` exists and contains the matching `runId` and terminal `success` status. The same run ID has no record under `<project-A>/.kb/data/workflows/`. |
| S-048.4 | Run the `e2e-hello` workflow in project B and inspect its completed run record | The new record is under `<project-B>/.kb/data/workflows/<runId>.json`; project B still uses the default while project A is overridden. |
| S-048.5 | In project A, run `kb adapter unbind storage --json`, then run a new `e2e-hello` workflow and inspect its record | The new run record is under `<project-A>/.kb/data/workflows/<runId>.json`, with matching `runId` and terminal `success`; no record for that run exists under `.kb/override-data`. Project B still writes to its default. |
| S-048.6 | Inspect project A and project B configs and run `kb adapter list --json` from each | Project A no longer has a storage binding or storage options and reports the built-in fs default at its `.kb/data`; project B remains on its own `.kb/data` default. |

## Dependencies

- S-048.1, S-048.3–S-048.5 depend on the workflow stream persisting run records
  through the effective project `IStorage` adapter.
