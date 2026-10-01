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
| S-003.1 | Run `kb scaffold run plugin demo --mode standalone --yes` | Scaffold creates the plugin package tree under `.kb/plugins/demo` in standalone mode. |
| S-003.2 | Build the generated plugin using its documented package build command | Entry package builds successfully and emits its manifest. |
| S-003.3 | Run `kb marketplace link --link .kb/plugins/demo/packages/demo-entry` | `kb marketplace plugins list --json` shows `demo` as a local project plugin, and `.kb/marketplace.lock` records its relative path. |
| S-003.4 | Run `kb --help` and `kb demo hello` | The `demo` command group is listed and the command prints `Hello, World from demo!`. |
| S-003.5 | Change the greeting in the generated core source, rebuild with `pnpm -w build`, and run `kb demo hello` again | The command prints the edited greeting, showing the author loop works without publishing. |
| S-003.6 | Run `kb marketplace plugins unlink demo` | `demo` is removed from the project plugin list and `.kb/marketplace.lock`. |
