---
id: S-046
title: S-046 — Manage project plugins and adapters in Studio
persona: solo-developer
area: studio
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

A Studio user selects among registered projects and verifies that each shows its own plugin and adapter set.

## Prerequisites

- [ ] Studio host, two registered projects, and distinct plugin and adapter sets for each.

## Steps

| # | Action | Expected |
|---|---|---|
| S-046.1 | Open Studio and select the project named `project-a` in the project selector | The active project selector displays `project-a`. |
| S-046.2 | Open `/plugins` and `/adapters` with project A selected | The Plugins view lists only `@kb-labs/workflow`; the Adapters view lists only project A's bound storage adapter. |
| S-046.3 | Select the project named `project-b`, then reopen `/plugins` and `/adapters` | The selector displays `project-b`; Plugins lists only `@kb-labs/commit` and Adapters lists only project B's bound storage adapter. |
