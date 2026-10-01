---
id: S-011
title: Marketplace — Enable and disable an installed plugin
persona: solo-developer
area: marketplace
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer disables an installed plugin for the current project without uninstalling it, then enables it again.

## Prerequisites

- [ ] The `@kb-labs/commit` plugin is installed in the current project.
- [ ] Run commands from that project root.

## Steps

| # | Action | Expected |
|---|---|---|
| S-011.1 | Run `kb marketplace plugins disable @kb-labs/commit` | CLI reports `Disabled @kb-labs/commit (project)`. The package remains installed. |
| S-011.2 | Run `kb marketplace plugins list` | The output shows `@kb-labs/commit` in the disabled count and marks its row `⏸`. |
| S-011.3 | Run `kb commit --help` | Commit plugin commands are not available while the package is disabled. |
| S-011.4 | Run `kb marketplace plugins enable @kb-labs/commit` | CLI reports `Enabled @kb-labs/commit (project)`. |
| S-011.5 | Run `kb marketplace plugins list` and `kb commit --help` | The package is shown as enabled and its commit commands are available. |
| S-011.6 | Run `kb marketplace plugins disable @kb-labs/not-installed` | Command reports the package cannot be found and leaves the marketplace lock unchanged. |
