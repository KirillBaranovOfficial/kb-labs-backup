---
id: S-043
title: Projects — Register projects and isolate CLI extensions
persona: solo-developer
area: config
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Multiple registered projects retain distinct plugin, adapter, and runtime state.

## Prerequisites

- [ ] Two initialized project directories, platform root, two plugins, and two platform-installed adapters.

## Steps

| # | Action | Expected |
|---|---|---|
| S-043.1 | From each initialized project root run `kb project add <absolute-project-path> --json` | Each response contains `ok: true`, a stable project ID, canonical path, and name; the two IDs differ. |
| S-043.2 | Run `kb project list --json` | The response contains both IDs, names, canonical paths, and statuses; `total` equals `2`. |
| S-043.3 | From each project root install a different exact plugin version with `kb marketplace install <package-id>@<version>` | Each project's `.kb/marketplace.lock` contains its own installed package and does not contain the other project's package. |
| S-043.4 | Run `kb adapter bind storage <package-id>` from each project root with distinct installed packages | Each project's `.kb/kb.config.json` records its own storage binding; the other project's binding is unchanged. |
| S-043.5 | Run one project-scoped plugin command from each project root | The command resolves that project's plugin installation and does not resolve the other project's plugin. |
