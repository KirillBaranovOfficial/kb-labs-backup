---
id: S-144
title: Workflow — Restart a run from a saved step
persona: solo-developer
area: workflow
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A user resumes a completed workflow from a selected step using its saved snapshot, preserving earlier outputs without repeating earlier side effects.

## Prerequisites

- [ ] Host running and workflow commands available in a registered project.
- [ ] Workflow `snapshot-demo` has two steps: step `prepare` writes a marker once and exports an output; step `finish` consumes that output and fails on the initial run.
- [ ] The failed run has a restart snapshot; in secured mode, an authenticated gateway session is available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-144.1 | Run `kb workflow runs restart <run-id> --from-step=finish --json` | JSON reports `ok: true`, a new run ID, source run ID, and `fromStepId: finish`. |
| S-144.2 | Wait up to 60 seconds, then inspect the new run with `kb workflow runs view <new-run-id> --json` | The `prepare` step is restored from the snapshot and not executed again; `finish` runs with the saved `prepare` output. |
| S-144.3 | Run `kb workflow runs restart <run-id> --from-step=missing-step --json` | CLI returns non-zero with a typed validation error naming `missing-step`; no new run is created. |
| S-144.4 | Run `kb workflow runs restart <active-run-id> --json` for an active run | CLI returns non-zero with a conflict error that says active runs cannot be restarted; the original run remains active. |
