---
id: S-143
title: Workflow — Rerun a failed run
persona: solo-developer
area: workflow
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A user retries a failed workflow with its original inputs, either rerunning all jobs or only failed jobs, and can identify the new run separately from the original.

## Prerequisites

- [ ] Host running and workflow commands available in a registered project.
- [ ] Workflow `retryable` has a deterministic first run that fails one named job after recording its input.
- [ ] In secured mode, an authenticated gateway session is available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-143.1 | Run `kb workflow runs rerun <failed-run-id> --json` | JSON reports `ok: true` with a new run ID and its initial status; the new ID differs from the source ID. |
| S-143.2 | Wait up to 60 seconds, then run `kb workflow runs view <new-run-id> --json` | New run contains the same workflow ID and resolved inputs as the source; all jobs are executed and the final status reflects their result. |
| S-143.3 | Run `kb workflow runs rerun <failed-run-id> --failed-only --json` | JSON returns a distinct new run ID and its initial status; the failed or interrupted job is replayed while successful jobs retain their prior outputs. |
| S-143.4 | Run `kb workflow runs rerun run-does-not-exist --json` | CLI returns non-zero with a typed not-found error naming the run and a hint to list available runs; no new run is created. |
