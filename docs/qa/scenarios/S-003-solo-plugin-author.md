---
id: S-003
title: S-003 — Scaffold and link a plugin author’s local loop
persona: plugin-author
area: plugins
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A plugin author scaffolds a plugin, builds it, links it into a project with the V2 marketplace, invokes a command, and sees an edit after rebuilding.

## Prerequisites

- [ ] V2 platform and initialized project; pnpm and workspace dependencies available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-003.1 | Run `kb scaffold run plugin demo --yes` | Scaffold creates the plugin package tree under `.kb/plugins/demo`. |
| S-003.2 | Build the generated plugin using its documented package build command | Entry package builds successfully and emits its manifest. |
| S-003.3 | Run `kb marketplace link --link .kb/plugins/demo/packages/demo-entry` | Local plugin is linked to the project. |
| S-003.4 | Run `kb --help` and invoke a generated plugin command | Plugin command group is listed and the command returns its documented output. |
| S-003.5 | Edit the command output, rebuild, and invoke it again | New output appears, showing the author loop works without publishing. |
