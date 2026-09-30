---
id: S-008
title: S-008 — Cancel a running workflow
persona: solo-developer
area: workflow
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A user cancels an active workflow run and confirms the run reaches cancelled state.

## Prerequisites

- [ ] A workflow with a controllably long running step and an active run ID.

## Steps

| # | Action | Expected |
|---|---|---|
| S-008.1 | Start the workflow with `kb workflow run --workflow-id=<project>/long-task` | CLI returns a run ID for the active run. |
| S-008.2 | Run `kb workflow runs cancel <runId>` | Cancellation request succeeds. |
| S-008.3 | Run `kb workflow runs view <runId>` | Run state is `cancelled`; it is not still running. |
