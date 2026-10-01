---
id: S-027
title: Workflow — Follow live progress from the CLI
persona: solo-developer
area: workflow
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A user watches a multi-step workflow and sees step transitions before the run finishes.

## Prerequisites

- [ ] Host running with workflow commands available from a registered project.
- [ ] Workflow `multi-step` has two local steps, each held at a controllable test gate long enough to observe a transition.
- [ ] In secured mode, an authenticated gateway session is available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-027.1 | Run `kb workflow run --workflow-id=multi-step` and copy its run ID | CLI returns a non-empty run ID. |
| S-027.2 | Run `kb workflow runs watch <run-id>` while the run is active | Output includes events for the run ID and identifies both step IDs as they enter and leave execution. |
| S-027.3 | Wait for the workflow to finish | Watch output includes the terminal event with status `success`, then the command exits 0 within 60 seconds of the last step completing. |
