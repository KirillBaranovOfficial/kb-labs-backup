---
id: S-027
title: S-027 — Follow live workflow progress
persona: solo-developer
area: workflow
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A user watches an active workflow run and sees progress updates before it completes.

## Prerequisites

- [ ] A workflow with multiple observable steps and an active run ID.

## Steps

| # | Action | Expected |
|---|---|---|
| S-027.1 | Start the workflow with `kb workflow run --workflow-id=<project>/multi-step` | CLI returns a run ID. |
| S-027.2 | Run `kb workflow runs watch <runId>` | Live events identify state changes and completed steps while the run is active. |
| S-027.3 | Observe the terminal event | Watch output reports the final run status and exits. |
