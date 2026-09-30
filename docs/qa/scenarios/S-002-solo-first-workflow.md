---
id: S-002
title: Solo — First AI Workflow
persona: solo-developer
area: workflow
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Developer starts platform services and runs their first workflow — either via CLI or Studio UI.
Validates that the workflow engine is reachable, a run is created, and result is visible.

## Prerequisites

- [ ] KB Labs installed (`kb-create` done, project exists)
- [ ] Services not yet started
- [ ] Node.js 24+, no port conflicts on 4000, 5050, 7778, 3000

---

## Steps

| # | Action | Expected |
|---|---|---|
| S-002.1 | Start the installed host with `kb-create start --platform-root "$PLATFORM_ROOT" --json` | Host reports success and its managed project runtime can be reached. |
| S-002.2 | From the registered project, run `kb workflow run --workflow-id=<project>/<workflow> --input='{"key":"value"}'` | Run is accepted and returns a run ID. |
| S-002.3 | Run `kb workflow runs list` | The new run appears with its workflow name and current status. |
| S-002.4 | Run `kb workflow runs view <runId>` | Run reaches a terminal status and displays step results. |
| S-002.5 | Open the Studio URL served by the installed host | Studio shell loads for the active project. |
| S-002.6 | Open Studio Workflows → Runs and select the run | The same run ID and terminal status appear in Studio. |
| S-002.7 | Run `kb-create stop --platform-root "$PLATFORM_ROOT" --json` | Host stops successfully. |
