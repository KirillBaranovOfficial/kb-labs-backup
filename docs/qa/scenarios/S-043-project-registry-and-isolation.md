---
id: S-043
title: Projects — register and isolate project extensions
persona: solo-developer
area: config
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Multiple registered projects retain distinct plugin, adapter, and runtime state.

## Prerequisites

- [ ] Two initialized project directories, platform root, two plugins, and two platform-installed adapters.

## Steps

| # | Action | Expected |
|---|---|---|
| S-043.1 | Run `kb project add <project-a>` and `kb project add <project-b>` | Each reports a stable, distinct project ID. |
| S-043.2 | Run `kb project list --json` | Both IDs and paths appear. |
| S-043.3 | Install different plugins in the two project roots | Each project lock records only its own plugin. |
| S-043.4 | Run `kb adapter bind storage <package-id>` from each project with distinct packages | Each project config records its own binding; the other config is unchanged. |
| S-043.5 | Change the working directory between the two project roots, then select each project in Studio | CLI operations resolve the current project; Studio shows the selected project and its matching plugin/adapter set. |
